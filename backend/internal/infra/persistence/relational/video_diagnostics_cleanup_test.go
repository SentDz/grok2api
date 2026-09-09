package relational

import (
	"context"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/media"
)

func TestVideoDiagnosticsCleanupResumesAndPreservesTasks(t *testing.T) {
	ctx := context.Background()
	database := openTestDatabase(t)
	repo := NewMediaJobRepository(database)
	key := clientKeyModel{Name: "cleanup", Prefix: "cleanup", SecretHash: testSecretHash, EncryptedSecret: testEncryptedToken, Enabled: true, RPMLimit: 60, MaxConcurrent: 4}
	if err := database.db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	day := 24 * time.Hour
	due := now.Add(day)
	jobs := []media.Job{
		testMediaJob("a_completed", 0, key.ID, media.StatusCompleted, now.Add(-time.Hour)),
		testMediaJob("b_failed", 0, key.ID, media.StatusFailed, now.Add(-time.Hour)),
		testMediaJob("c_later", 0, key.ID, media.StatusCompleted, now),
		testMediaJob("d_active", 0, key.ID, media.StatusInProgress, now),
		testMediaJob("e_queued", 0, key.ID, media.StatusQueued, now),
	}
	for i := range jobs {
		jobs[i].Diagnostics.Advance(media.VideoEvent{Stage: "upload_image", StartedAt: now})
		jobs[i].Diagnostics.Attempt = 1
		if i < 3 {
			finished := now
			jobs[i].CompletedAt = &finished
		}
		if i == 2 {
			finished := due.Add(10 * time.Minute)
			jobs[i].CompletedAt = &finished
		}
		if i == 1 {
			jobs[i].ErrorCode, jobs[i].ErrorMessage = "generation_failed", "public failure"
		}
		if err := repo.CreateMediaJob(ctx, jobs[i]); err != nil {
			t.Fatal(err)
		}
	}
	run := func(at time.Time, want int64) {
		t.Helper()
		count, err := repo.CleanupVideoDiagnostics(ctx, at, day, 1)
		if err != nil || count != want {
			t.Fatalf("cleanup count=%d want=%d err=%v", count, want, err)
		}
	}
	run(now, 0)
	run(due.Add(-time.Second), 0)
	run(due, 1)
	// A new repository instance resumes the durable cursor and fixed cutoff.
	repo = NewMediaJobRepository(database)
	run(due.Add(20*time.Minute), 1)
	run(due.Add(21*time.Minute), 0)
	for i, job := range jobs {
		stored, err := repo.GetMediaJob(ctx, job.ID, key.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (stored.Diagnostics.Current() == nil) != (i < 2) {
			t.Fatalf("job %s history=%#v", job.ID, stored.Diagnostics)
		}
		if stored.Status != job.Status || stored.Prompt != job.Prompt || stored.ErrorMessage != job.ErrorMessage || !stored.UpdatedAt.Equal(job.UpdatedAt) {
			t.Fatalf("cleanup changed task metadata: %#v", stored)
		}
	}
	// A delayed ordinary task update cannot resurrect a cleared history.
	if err := repo.UpdateMediaJob(ctx, jobs[0]); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetMediaJob(ctx, jobs[0].ID, key.ID)
	if err != nil || stored.Diagnostics.Current() != nil {
		t.Fatal("ordinary update resurrected logs")
	}
	run(due.Add(day), 0)
	run(due.Add(day+22*time.Minute), 1)
	if _, err := repo.CleanupVideoDiagnostics(ctx, now, 0, 1); err == nil {
		t.Fatal("accepted zero interval")
	}
}
