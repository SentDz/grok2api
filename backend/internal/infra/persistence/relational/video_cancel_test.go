package relational

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

func TestCancelMediaJobFencesWorkersAndRecovery(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t)
	key := clientKeyModel{Name: "cancel-test", Prefix: "cancel-test", SecretHash: testSecretHash, EncryptedSecret: testEncryptedToken, Enabled: true}
	if err := db.db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewMediaJobRepository(db)
	tickets := NewMediaUploadTicketRepository(db)
	now := time.Now().UTC()
	for _, state := range []media.Status{media.StatusQueued, media.StatusInProgress, media.StatusCompleted, media.StatusFailed} {
		t.Run(string(state), func(t *testing.T) {
			job := testMediaJob("cancel-"+string(state), 0, key.ID, state, now)
			job.InputJSON = `{"image":"file_id:input_test"}`
			job.Diagnostics.Advance(media.VideoEvent{Stage: "poll_video", StartedAt: now})
			job.DiagnosticsDirty = true
			if err := repo.CreateMediaJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if state == media.StatusInProgress {
				var err error
				job, _, err = repo.TryClaimMediaJob(ctx, job.ID, now, now.Add(time.Hour), "old-worker-claim-token")
				if err != nil {
					t.Fatal(err)
				}
			}
			cancelled, err := repo.CancelMediaJob(ctx, job.ID, now.Add(time.Second))
			if state == media.StatusCompleted || state == media.StatusFailed {
				if !errors.Is(err, repository.ErrConflict) {
					t.Fatalf("terminal cancellation: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cancelled.Status != media.StatusFailed || cancelled.ErrorCode != media.VideoCancelledErrorCode || cancelled.CompletedAt == nil || cancelled.LeaseUntil != nil || cancelled.ClaimToken != "" || cancelled.InputJSON != job.InputJSON {
				t.Fatalf("cancelled job = %#v", cancelled)
			}
			if current := cancelled.Diagnostics.Current(); current == nil || current.Error != media.VideoCancelledMessage || current.FinishedAt == nil {
				t.Fatalf("diagnostics = %#v", current)
			}
			for _, next := range []media.Status{media.StatusInProgress, media.StatusCompleted, media.StatusFailed} {
				job.Status, job.ErrorCode = next, ""
				if err := repo.UpdateMediaJob(ctx, job); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("stale %s write succeeded: %v", next, err)
				}
			}
			if _, claimed, err := repo.TryClaimMediaJob(ctx, job.ID, now.Add(3*time.Hour), now.Add(4*time.Hour), "new-worker-claim-token"); err != nil || claimed {
				t.Fatalf("cancelled job reclaimed: %v, %v", claimed, err)
			}
			if err := tickets.BindJobResultAsset(ctx, job.ID, "late-asset"); !errors.Is(err, repository.ErrNotFound) {
				t.Fatalf("late upload accepted: %v", err)
			}
			if _, err := repo.CancelMediaJob(ctx, job.ID, now.Add(time.Minute)); err != nil {
				t.Fatalf("repeat cancellation: %v", err)
			}
		})
	}
	if _, err := repo.CancelMediaJob(ctx, "missing", now); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("missing cancellation: %v", err)
	}
}
