package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

var errVideoCancelled = errors.New(media.VideoCancelledMessage)

type videoExecution struct {
	cancel context.CancelCauseFunc
}

func (s *Service) CancelVideoJob(ctx context.Context, id string) error {
	job, err := s.mediaJobs.CancelMediaJob(ctx, id, time.Now().UTC())
	if err != nil {
		return err
	}
	s.mediaMu.Lock()
	if execution := s.videoExecutions[id]; execution != nil {
		execution.cancel(errVideoCancelled)
	}
	s.mediaMu.Unlock()

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizationTimeout)
	defer cancel()
	s.cancelBillingReservation("video_usage_" + job.ID)
	if err := s.recordVideoAudit(cleanupCtx, job, max(int64(0), job.CompletedAt.Sub(job.CreatedAt).Milliseconds()), 0, nil); err != nil {
		s.logger.Error("video_usage_record_failed", "job_id", job.ID, "error", err)
	}
	s.releaseVideoInputs(job)
	return nil
}

func (s *Service) videoExecutionContext(parent context.Context, job media.Job) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	execution := &videoExecution{cancel: cancel}
	s.mediaMu.Lock()
	if s.videoExecutions == nil {
		s.videoExecutions = make(map[string]*videoExecution)
	}
	s.videoExecutions[job.ID] = execution
	s.mediaMu.Unlock()
	checkOwnership := func() bool {
		readCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		defer stop()
		current, err := s.mediaJobs.GetMediaJob(readCtx, job.ID, job.ClientKeyID)
		if errors.Is(err, repository.ErrNotFound) || (err == nil && (current.ErrorCode == media.VideoCancelledErrorCode || current.ClaimToken != job.ClaimToken)) {
			cancel(errVideoCancelled)
			return false
		}
		return true
	}
	if job.ClaimToken != "" {
		checkOwnership()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if job.ClaimToken == "" {
			return
		}
		// Persisted cancellation also reaches workers on other instances, and
		// covers cancellation between claiming a job and registering its context.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !checkOwnership() {
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel(nil)
		<-done
		s.mediaMu.Lock()
		if s.videoExecutions[job.ID] == execution {
			delete(s.videoExecutions, job.ID)
		}
		s.mediaMu.Unlock()
	}
}
