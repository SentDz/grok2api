package relational

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
	"github.com/chenyme/grok2api/backend/internal/repository"
	"gorm.io/gorm"
)

// Uses a separate key in the existing versioned runtime settings table.
type StatsigRepository struct {
	database *Database
	cipher   *security.Cipher
}

func NewStatsigRepository(db *Database, cipher *security.Cipher) *StatsigRepository {
	return &StatsigRepository{db, cipher}
}
func (r *StatsigRepository) Load(ctx context.Context) (statsig.State, uint64, error) {
	var row runtimeSettingsModel
	err := r.database.db.WithContext(ctx).Where("key = ?", "statsig-builtin").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return statsig.State{Status: "unverified", Events: []statsig.Event{}}, 0, nil
	}
	if err != nil {
		return statsig.State{}, 0, err
	}
	var encrypted string
	if err = json.Unmarshal([]byte(row.ValueJSON), &encrypted); err != nil {
		return statsig.State{}, 0, err
	}
	plain, err := r.cipher.Decrypt(encrypted)
	if err != nil {
		return statsig.State{}, 0, err
	}
	var state statsig.State
	err = json.Unmarshal([]byte(plain), &state)
	return state, row.Revision, err
}
func (r *StatsigRepository) Save(ctx context.Context, state statsig.State, revision uint64) (uint64, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return revision, err
	}
	value, err := r.cipher.Encrypt(string(raw))
	if err != nil {
		return revision, err
	}
	encoded, _ := json.Marshal(value)
	row := runtimeSettingsModel{Key: "statsig-builtin", ValueJSON: string(encoded), Revision: revision + 1, UpdatedAt: time.Now().UTC()}
	if revision == 0 {
		err = r.database.db.WithContext(ctx).Create(&row).Error
		return row.Revision, mapError(err)
	}
	result := r.database.db.WithContext(ctx).Model(&runtimeSettingsModel{}).Where("key = ? AND revision = ?", row.Key, revision).Updates(map[string]any{"value_json": row.ValueJSON, "revision": row.Revision, "updated_at": row.UpdatedAt})
	if result.Error != nil {
		return revision, result.Error
	}
	if result.RowsAffected != 1 {
		return revision, repository.ErrConflict
	}
	return row.Revision, nil
}
