package gateway

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountapp "github.com/chenyme/grok2api/backend/internal/application/account"
	clientkeyapp "github.com/chenyme/grok2api/backend/internal/application/clientkey"
	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
)

type cancellableVideoAdapter struct {
	videoCreateFailoverAdapter
	started chan struct{}
}

func (a *cancellableVideoAdapter) GenerateVideo(ctx context.Context, _ provider.VideoRequest) (provider.VideoResult, error) {
	provider.ReportVideoStep(ctx, "poll_video")
	close(a.started)
	<-ctx.Done()
	// Even an adapter returning a late success must not revive a cancelled job.
	return provider.VideoResult{AssetID: "late-video", ContentType: "video/mp4"}, nil
}

func newVideoCancellationFixture(t *testing.T, adapter provider.Adapter) (*Service, media.Job) {
	t.Helper()
	ctx := context.Background()
	db, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts, models := relational.NewAccountRepository(db), relational.NewModelRepository(db)
	audits, jobs, keys := relational.NewAuditRepository(db), relational.NewMediaJobRepository(db), relational.NewClientKeyRepository(db)
	key, err := keys.Create(ctx, clientkey.Key{Name: "cancel-test", Prefix: "cancel-test", SecretHash: strings.Repeat("a", 64), EncryptedSecret: "encrypted", Enabled: true, RPMLimit: 60, MaxConcurrent: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.UpsertDiscovered(ctx, account.ProviderWeb, []string{"grok-imagine-video"}); err != nil {
		t.Fatal(err)
	}
	var first account.Credential
	for _, name := range []string{"first", "second"} {
		credential, _, err := accounts.UpsertByIdentity(ctx, account.Credential{
			Provider: account.ProviderWeb, AuthType: account.AuthTypeSSO, WebTier: account.WebTierSuper,
			Name: name, SourceKey: name, EncryptedAccessToken: name + "-token", ExpiresAt: time.Now().Add(time.Hour),
			Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := models.ReplaceAccountCapabilities(ctx, credential.ID, []string{"grok-imagine-video"}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if first.ID == 0 {
			first = credential
		}
	}
	route, err := models.GetByProviderUpstream(ctx, account.ProviderWeb, "grok-imagine-video")
	if err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry(adapter)
	sticky := memory.NewStickyStore()
	accountService := accountapp.NewService(accounts, audits, memory.NewDeviceSessionStore(), sticky, registry, testCipher(t), nil)
	selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), sticky, registry, time.Hour, time.Second, time.Minute)
	service := NewService(models, audits, accountService, clientkeyapp.NewService(keys, nil, nil, 60, 4, nil), registry, selector, nil, 3)
	service.ConfigureMedia(jobs, 1)
	service.UpdateVideoDiagnosticsEnabled(true)
	service.UpdateVideoMaxAttempts(10)
	now := time.Now().UTC()
	job := media.Job{ID: "cancel-job", RequestID: "cancel-request", ClientKeyID: key.ID, ClientKeyName: key.Name,
		AccountID: first.ID, AccountName: first.Name, Provider: string(account.ProviderWeb), Model: route.PublicID,
		ModelRouteID: route.ID, UpstreamModel: route.UpstreamModel, Operation: media.VideoOperationGenerate,
		Prompt: "test", Seconds: 5, Quality: "720p", Status: media.StatusQueued, InputJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := jobs.CreateMediaJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	return service, job
}

func TestForceCancelVideoStopsWorker(t *testing.T) {
	for _, mode := range []string{"queued", "local worker", "other instance"} {
		t.Run(mode, func(t *testing.T) {
			adapter := &cancellableVideoAdapter{started: make(chan struct{})}
			service, job := newVideoCancellationFixture(t, adapter)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan struct{})
			if mode != "queued" {
				go func() { service.processVideoJob(ctx, job.ID); close(done) }()
				select {
				case <-adapter.started:
				case <-ctx.Done():
					t.Fatal("worker did not start")
				}
			}
			canceler := service
			if mode == "other instance" {
				canceler = &Service{mediaJobs: service.mediaJobs, clientKeys: service.clientKeys, audits: service.audits, logger: service.logger}
			}
			if err := canceler.CancelVideoJob(ctx, job.ID); err != nil {
				t.Fatal(err)
			}
			if mode == "queued" {
				service.processVideoJob(ctx, job.ID)
				select {
				case <-adapter.started:
					t.Fatal("cancelled queued job was submitted")
				default:
				}
			} else {
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("cancelled worker still running")
				}
			}
			stored, err := service.mediaJobs.GetMediaJob(ctx, job.ID, job.ClientKeyID)
			if err != nil || stored.Status != media.StatusFailed || stored.ErrorCode != media.VideoCancelledErrorCode || stored.UsageRecordedAt == nil || stored.ResultAssetID != "" {
				t.Fatalf("cancelled job: %#v, %v", stored, err)
			}
			lease, err := service.selector.AcquirePinned(ctx, account.ProviderWeb, stored.AccountID, job.ModelRouteID, job.UpstreamModel, "", true)
			if err != nil {
				t.Fatalf("cancellation did not release account capacity: %v", err)
			}
			lease.Release()
		})
	}
}

func TestVideoSubmission404DoesNotRetryAnotherAccount(t *testing.T) {
	adapter := &videoCreateFailoverAdapter{status: http.StatusNotFound, failures: map[uint64]int{1: 10, 2: 10}}
	service, job := newVideoCancellationFixture(t, adapter)
	service.processVideoJob(context.Background(), job.ID)
	stored, err := service.mediaJobs.GetMediaJob(context.Background(), job.ID, job.ClientKeyID)
	if err != nil || stored.Status != media.StatusFailed || len(adapter.Attempts()) != 1 {
		t.Fatalf("404 task: status=%s attempts=%v error=%v", stored.Status, adapter.Attempts(), err)
	}
}

func TestCancelVideoBeforeWorkerRegistrationDoesNotSubmit(t *testing.T) {
	adapter := &videoCreateFailoverAdapter{}
	service, job := newVideoCancellationFixture(t, adapter)
	ctx := context.Background()
	claimed, ok, err := service.claimVideoJob(ctx, job.ID)
	if err != nil || !ok {
		t.Fatalf("claim: %v, %v", ok, err)
	}
	if err := service.CancelVideoJob(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	route, err := service.models.Get(ctx, job.ModelRouteID)
	if err != nil {
		t.Fatal(err)
	}
	service.runVideoJob(ctx, claimed, route)
	if len(adapter.Attempts()) != 0 {
		t.Fatalf("cancelled job submitted: %v", adapter.Attempts())
	}
}
