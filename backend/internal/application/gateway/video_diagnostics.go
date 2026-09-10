package gateway

import (
	"context"
	"regexp"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

var videoDiagnosticSecretPattern = regexp.MustCompile(`(?i)(["']?(?:sso(?:-rw)?|session[_-]?id|upload[_-]?url)["']?\s*[:=]\s*["']?)[^"'\s,;}]+`)

func (s *Service) UpdateVideoDiagnosticsEnabled(enabled bool) {
	s.videoDiagnosticsEnabled.Store(enabled)
}

func (s *Service) recordVideoStep(ctx context.Context, job *media.Job, stage string, index, total int) {
	s.recordVideoEvent(ctx, job, media.VideoEvent{Stage: stage, ItemIndex: index, ItemTotal: total})
}

func (s *Service) recordVideoEvent(ctx context.Context, job *media.Job, event media.VideoEvent) {
	if !s.videoDiagnosticsEnabled.Load() {
		job.DiagnosticsDirty = false
		return
	}
	now := time.Now().UTC()
	if event.StartedAt.IsZero() {
		event.StartedAt = now
	}
	event.Attempt, event.AccountID, event.AccountName = job.Diagnostics.Attempt, job.AccountID, job.AccountName
	event.Error = sanitizeVideoDiagnosticError(event.Error)
	changed := job.Diagnostics.Advance(event)
	if !changed && now.Sub(job.UpdatedAt) < 15*time.Second {
		return
	}
	job.UpdatedAt = now
	job.DiagnosticsDirty = job.DiagnosticsDirty || changed
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.mediaJobs.UpdateMediaJob(writeCtx, *job); err != nil {
		if s.logger != nil {
			s.logger.Warn("video_job_diagnostics_write_failed", "job_id", job.ID, "error", err)
		}
		return
	}
	job.DiagnosticsDirty = false
}

func (s *Service) recordVideoDiagnosticFailure(job *media.Job, err error) {
	if !s.videoDiagnosticsEnabled.Load() {
		job.DiagnosticsDirty = false
		return
	}
	if err == nil {
		return
	}
	status, _ := provider.ErrorHTTPStatus(err)
	message := sanitizeVideoDiagnosticError(err.Error())
	job.Diagnostics.Fail(message, status, time.Now().UTC())
	job.DiagnosticsDirty = job.Diagnostics.Current() != nil
}

func sanitizeVideoDiagnosticError(message string) string {
	message = videoDiagnosticSecretPattern.ReplaceAllString(message, "$1[REDACTED]")
	message = sanitizeDiagnosticText(message, 1024)
	return diagnosticURLPattern.ReplaceAllString(message, "[REDACTED_URL]")
}
