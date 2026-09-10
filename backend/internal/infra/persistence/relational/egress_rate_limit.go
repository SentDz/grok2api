package relational

import (
	"context"
	"time"

	"github.com/chenyme/grok2api/backend/internal/repository"
	"gorm.io/gorm"
)

func (r *EgressRepository) RateLimitEgressNode(ctx context.Context, id uint64, until time.Time) error {
	result := r.db.db.WithContext(ctx).Model(&egressNodeModel{}).Where("id = ?", id).Updates(map[string]any{
		"rate_limit_until": gorm.Expr("CASE WHEN rate_limit_until IS NULL OR rate_limit_until < ? THEN ? ELSE rate_limit_until END", until, until),
		"updated_at":       time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *EgressRepository) GetEgressNodeRateLimit(ctx context.Context, id uint64) (*time.Time, error) {
	var row egressNodeModel
	if err := r.db.db.WithContext(ctx).Select("rate_limit_until").First(&row, id).Error; err != nil {
		return nil, mapError(err)
	}
	return row.RateLimitUntil, nil
}
