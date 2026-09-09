package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func TestVideoDiagnosticFailurePreservesStageAndRedactsSecrets(t *testing.T) {
	service := &Service{}
	service.UpdateVideoDiagnosticsEnabled(true)
	job := media.Job{}
	job.Diagnostics.Advance(media.VideoEvent{Stage: "upload_image", StartedAt: time.Now().UTC(), ItemIndex: 2, ItemTotal: 3})
	err := provider.WrapVideoStage(provider.VideoStageCreate, 403, errors.New("upload rejected; sso=bare-sso; Authorization: Bearer secret-token; https://assets.example/private?token=secret-query\nCookie: sso=secret-cookie"))
	service.recordVideoDiagnosticFailure(&job, err)
	service.recordVideoDiagnosticFailure(&job, errors.New("upstream unavailable"))
	event := job.Diagnostics.Current()
	if event.Stage != "upload_image" || event.HTTPStatus != 403 || event.ItemIndex != 2 || event.FinishedAt == nil || !strings.Contains(event.Error, "upload rejected") {
		t.Fatalf("event = %#v", event)
	}
	for _, secret := range []string{"bare-sso", "secret-token", "secret-query", "secret-cookie", "assets.example", "upstream unavailable"} {
		if strings.Contains(event.Error, secret) {
			t.Fatalf("diagnostic contains %q", secret)
		}
	}
}

type videoDiagnosticsWriteRecorder struct {
	repository.MediaJobRepository
	dirtyWrites []bool
}

func (r *videoDiagnosticsWriteRecorder) UpdateMediaJob(_ context.Context, job media.Job) error {
	r.dirtyWrites = append(r.dirtyWrites, job.DiagnosticsDirty)
	return nil
}

func TestVideoDiagnosticsSwitchStopsWritesAndCanResume(t *testing.T) {
	repo := &videoDiagnosticsWriteRecorder{}
	service := &Service{mediaJobs: repo}
	job := media.Job{ID: "test", UpdatedAt: time.Now().UTC()}
	service.recordVideoStep(context.Background(), &job, "load_image", 1, 1)
	service.recordVideoDiagnosticFailure(&job, errors.New("disabled failure"))
	if len(repo.dirtyWrites) != 0 || len(job.Diagnostics.Events) != 0 || job.DiagnosticsDirty {
		t.Fatal("diagnostics must be disabled by default")
	}
	service.UpdateVideoDiagnosticsEnabled(true)
	service.recordVideoStep(context.Background(), &job, "upload_image", 1, 1)
	if len(repo.dirtyWrites) != 1 || !repo.dirtyWrites[0] || job.DiagnosticsDirty {
		t.Fatalf("stage write = %#v, dirty=%v", repo.dirtyWrites, job.DiagnosticsDirty)
	}
	service.recordVideoStep(context.Background(), &job, "upload_image", 1, 1)
	if len(repo.dirtyWrites) != 1 {
		t.Fatal("duplicate stage wrote immediately")
	}
	job.UpdatedAt = time.Now().Add(-16 * time.Second)
	service.recordVideoStep(context.Background(), &job, "upload_image", 1, 1)
	if len(repo.dirtyWrites) != 2 || repo.dirtyWrites[1] {
		t.Fatal("heartbeat rewrote diagnostic JSON")
	}
	service.UpdateVideoDiagnosticsEnabled(false)
	service.recordVideoStep(context.Background(), &job, "submit_video", 0, 0)
	service.recordVideoDiagnosticFailure(&job, errors.New("disabled failure"))
	if len(repo.dirtyWrites) != 2 || len(job.Diagnostics.Events) != 1 || job.Diagnostics.Current().Error != "" || job.DiagnosticsDirty {
		t.Fatal("disabling changed the existing history")
	}
	service.UpdateVideoDiagnosticsEnabled(true)
	service.recordVideoStep(context.Background(), &job, "wait_generation", 0, 0)
	if len(repo.dirtyWrites) != 3 || !repo.dirtyWrites[2] || job.Diagnostics.Current().Stage != "wait_generation" {
		t.Fatal("recording did not resume")
	}
}
