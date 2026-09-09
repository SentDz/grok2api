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
	if !s.videoDiagnosticsEnabled.Load() {
		job.DiagnosticsDirty = false
		return
	}
	now := time.Now().UTC()
	changed := job.Diagnostics.Advance(media.VideoEvent{
		Stage: stage, StartedAt: now, Attempt: job.Diagnostics.Attempt,
		AccountID: job.AccountID, AccountName: job.AccountName, ItemIndex: index, ItemTotal: total,
	})
	if !changed && now.Sub(job.UpdatedAt) < 15*time.Second {
		return
	}
	job.UpdatedAt = now
	job.DiagnosticsDirty = job.DiagnosticsDirty || changed
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.mediaJobs.UpdateMediaJob(writeCtx, *job); err != nil && s.logger != nil {
		s.logger.Warn("video_job_diagnostics_write_failed", "job_id", job.ID, "error", err)
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
	message := videoDiagnosticSecretPattern.ReplaceAllString(err.Error(), "$1[REDACTED]")
	message = sanitizeDiagnosticText(message, 1024)
	// Asset URLs and upload tickets can carry private input or bearer access.
	message = diagnosticURLPattern.ReplaceAllString(message, "[REDACTED_URL]")
	job.Diagnostics.Fail(message, status, time.Now().UTC())
	job.DiagnosticsDirty = job.Diagnostics.Current() != nil
}
