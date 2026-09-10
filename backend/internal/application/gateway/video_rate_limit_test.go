package gateway

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountapp "github.com/chenyme/grok2api/backend/internal/application/account"
	clientkeyapp "github.com/chenyme/grok2api/backend/internal/application/clientkey"
	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	domainegress "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/media"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
)

type rateLimitVideoAdapter struct {
	videoCreateFailoverAdapter
	manager   *infraegress.Manager
	rejectAll bool
	nodes     []uint64
	accounts  []uint64
}

func (a *rateLimitVideoAdapter) QuotaMode(string) string { return account.QuotaModeWebVideo }
func (a *rateLimitVideoAdapter) SelectVideoRetryNode(ctx context.Context, excluded []uint64) (uint64, error) {
	return a.manager.SelectVideoRetryNode(ctx, excluded)
}
func (a *rateLimitVideoAdapter) GenerateVideo(ctx context.Context, request provider.VideoRequest) (provider.VideoResult, error) {
	lease, err := a.manager.AcquireCredential(ctx, domainegress.ScopeWebSubmit, request.Credential)
	if err != nil {
		return provider.VideoResult{}, err
	}
	defer lease.Release()
	a.nodes, a.accounts = append(a.nodes, lease.NodeID), append(a.accounts, request.Credential.ID)
	provider.ReportVideoStep(ctx, "http_wait_headers")
	if a.rejectAll || len(a.nodes) == 1 {
		until, err := a.manager.CoolVideoNode(ctx, lease.NodeID)
		return provider.VideoResult{}, provider.WrapVideoStage(provider.VideoStageCreate, 429, &provider.VideoSubmissionRateLimitError{
			NodeID: lease.NodeID, CooldownUntil: until, CoolingError: err, Err: videoHTTPStatusError{status: http.StatusTooManyRequests},
		})
	}
	return provider.VideoResult{AssetID: "video_asset_00001", ContentType: "video/mp4"}, nil
}

func TestWebVideo429PreservesQuotaAndRotatesAccountAndNode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		nodes        int
		rejectAll    bool
		wantAttempts int
		wantStatus   media.Status
	}{
		{"one node stops without scanning accounts", 1, true, 1, media.StatusFailed},
		{"two nodes change account and bound node", 2, false, 2, media.StatusCompleted},
		{"two limited nodes stop after two accounts", 2, true, 2, media.StatusFailed},
		{"unlimited routing still bounds 429 to five nodes", 6, true, 5, media.StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "video-rate-limit.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.InitializeSchema(ctx); err != nil {
				t.Fatal(err)
			}
			accounts := relational.NewAccountRepository(db)
			models := relational.NewModelRepository(db)
			audits := relational.NewAuditRepository(db)
			jobs := relational.NewMediaJobRepository(db)
			keys := relational.NewClientKeyRepository(db)
			nodes := relational.NewEgressRepository(db)
			cipher := testCipher(t)
			var nodeIDs []uint64
			for i := 0; i < tc.nodes; i++ {
				proxy, err := cipher.Encrypt(fmt.Sprintf("http://proxy-%d.example:8080", i))
				if err != nil {
					t.Fatal(err)
				}
				node, err := nodes.CreateEgressNode(ctx, domainegress.Node{Name: fmt.Sprintf("node-%d", i), Scope: domainegress.ScopeWebSubmit, Enabled: true, ProxyPool: true, EncryptedProxyURL: proxy, Health: 1})
				if err != nil {
					t.Fatal(err)
				}
				nodeIDs = append(nodeIDs, node.ID)
			}
			key, err := keys.Create(ctx, clientkey.Key{Name: "test", Prefix: "test", SecretHash: strings.Repeat("a", 64), EncryptedSecret: "encrypted", Enabled: true, RPMLimit: 60, MaxConcurrent: 10})
			if err != nil {
				t.Fatal(err)
			}
			var credentials []account.Credential
			now := time.Now().UTC()
			for i := 0; i < 8; i++ {
				name := fmt.Sprintf("account-%d", i)
				token, err := cipher.Encrypt(name)
				if err != nil {
					t.Fatal(err)
				}
				credential, _, err := accounts.UpsertByIdentity(ctx, account.Credential{Provider: account.ProviderWeb, AuthType: account.AuthTypeSSO, WebTier: account.WebTierSuper,
					Name: name, SourceKey: name, EncryptedAccessToken: token, ExpiresAt: now.Add(time.Hour), Enabled: true, AuthStatus: account.AuthStatusActive, Priority: 100 - i, MaxConcurrent: 1})
				if err != nil {
					t.Fatal(err)
				}
				if err := accounts.SaveQuotaWindows(ctx, credential.ID, account.WebTierSuper, now, []account.QuotaWindow{
					{Mode: account.QuotaModeWebVideo720p, Remaining: 10, Total: 10}, {Mode: account.QuotaModeWebVideo, Remaining: 10, Total: 10},
				}); err != nil {
					t.Fatal(err)
				}
				if _, err := accounts.UpdateEgressBindings(ctx, account.ProviderWeb, []uint64{credential.ID}, &nodeIDs[0], account.EgressAssignmentManual, now); err != nil {
					t.Fatal(err)
				}
				credentials = append(credentials, credential)
			}
			if err := models.UpsertDiscovered(ctx, account.ProviderWeb, []string{"grok-imagine-video"}); err != nil {
				t.Fatal(err)
			}
			for _, credential := range credentials {
				if err := models.ReplaceAccountCapabilities(ctx, credential.ID, []string{"grok-imagine-video"}, now); err != nil {
					t.Fatal(err)
				}
			}
			route, err := models.GetByProviderUpstream(ctx, account.ProviderWeb, "grok-imagine-video")
			if err != nil {
				t.Fatal(err)
			}
			adapter := &rateLimitVideoAdapter{manager: infraegress.NewManager(nodes, cipher), rejectAll: tc.rejectAll}
			registry := provider.NewRegistry(adapter)
			sticky := memory.NewStickyStore()
			accountService := accountapp.NewService(accounts, audits, memory.NewDeviceSessionStore(), sticky, registry, cipher, nil)
			selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), sticky, registry, time.Hour, time.Second, time.Minute)
			service := NewService(models, audits, accountService, clientkeyapp.NewService(keys, nil, nil, 60, 10, nil), registry, selector, nil, 3)
			service.ConfigureMedia(jobs, 1)
			service.UpdateVideoMaxAttempts(-1)
			service.UpdateVideoDiagnosticsEnabled(true)
			job := media.Job{ID: "video-rate-limit", RequestID: "request-rate-limit", ClientKeyID: key.ID, ClientKeyName: key.Name, AccountID: credentials[0].ID, AccountName: credentials[0].Name,
				Provider: string(account.ProviderWeb), Model: route.PublicID, ModelRouteID: route.ID, UpstreamModel: route.UpstreamModel, Operation: provider.VideoOperationGenerate,
				Prompt: "test", Seconds: 6, Quality: "720p", Status: media.StatusInProgress, InputJSON: `{}`, CreatedAt: now, UpdatedAt: now}
			if err := jobs.CreateMediaJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			service.runVideoJob(ctx, job, route)
			stored, err := jobs.GetMediaJob(ctx, job.ID, key.ID)
			if err != nil || stored.Status != tc.wantStatus || len(adapter.nodes) != tc.wantAttempts {
				t.Fatalf("status=%s attempts=%v err=%v", stored.Status, adapter.nodes, err)
			}
			if tc.wantStatus == media.StatusFailed && stored.ErrorCode != "rate_limited" {
				t.Fatalf("failure code=%s", stored.ErrorCode)
			}
			seenNodes, seenAccounts := map[uint64]bool{}, map[uint64]bool{}
			for i, nodeID := range adapter.nodes {
				if seenNodes[nodeID] || seenAccounts[adapter.accounts[i]] {
					t.Fatalf("reused account/node: accounts=%v nodes=%v", adapter.accounts, adapter.nodes)
				}
				seenNodes[nodeID], seenAccounts[adapter.accounts[i]] = true, true
			}
			for _, credential := range credentials {
				windows, err := accounts.GetQuotaWindows(ctx, []uint64{credential.ID})
				if err != nil {
					t.Fatal(err)
				}
				for _, window := range windows[credential.ID] {
					if window.Remaining == 0 {
						t.Fatalf("429 exhausted account %d mode %s", credential.ID, window.Mode)
					}
					if (tc.rejectAll || credential.ID == adapter.accounts[0]) && window.Remaining != 10 {
						t.Fatalf("rejected account quota changed: %#v", window)
					}
				}
				persisted, err := accounts.Get(ctx, credential.ID)
				if err != nil || persisted.EgressNodeID != nodeIDs[0] || persisted.AuthStatus != account.AuthStatusActive {
					t.Fatalf("binding/auth mutated for %d", credential.ID)
				}
			}
		})
	}
}
