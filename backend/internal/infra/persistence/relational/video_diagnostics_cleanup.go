package relational

import (
	"context"
	"fmt"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CleanupVideoDiagnostics clears terminal histories in bounded transactions.
// The cursor and batch commit together; an interrupted cycle resumes at the
// original cutoff, and concurrent instances serialize on the cursor row.
func (r *MediaJobRepository) CleanupVideoDiagnostics(ctx context.Context, now time.Time, interval time.Duration, limit int) (int64, error) {
	if interval <= 0 {
		return 0, fmt.Errorf("video diagnostic cleanup interval must be positive")
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var cleared int64
	err := r.db.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state := videoDiagnosticsCleanupModel{ID: 1, LastRunAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, 1).Error; err != nil {
			return err
		}
		if state.Cutoff == nil {
			if now.Before(state.LastRunAt.Add(interval)) {
				return nil
			}
			state.Cutoff = &now
		}
		var ids []string
		// Match the partial index predicate so disabled/cleared jobs do not
		// require scanning their potentially large input and prompt columns.
		query := tx.Model(&mediaJobModel{}).
			Where("id > ? AND status IN ('completed','failed') AND completed_at <= ? AND diagnostics <> '{}'", state.AfterID, *state.Cutoff)
		if err := query.Select("id").Order("id ASC").Limit(limit).Find(&ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			result := tx.Model(&mediaJobModel{}).
				Where("id IN ? AND status IN ?", ids, []media.Status{media.StatusCompleted, media.StatusFailed}).
				UpdateColumn("diagnostics", "{}")
			if result.Error != nil {
				return result.Error
			}
			cleared = result.RowsAffected
		}
		if len(ids) < limit {
			state.LastRunAt, state.Cutoff = now, nil
			state.AfterID = ""
		} else {
			state.AfterID = ids[len(ids)-1]
		}
		return tx.Model(&videoDiagnosticsCleanupModel{}).Where("id = ?", state.ID).
			Updates(map[string]any{"last_run_at": state.LastRunAt, "cutoff": state.Cutoff, "after_id": state.AfterID}).Error
	})
	if err != nil {
		return 0, err
	}
	return cleared, nil
}
