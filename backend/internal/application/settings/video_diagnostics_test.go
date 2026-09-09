package settings

import (
	"context"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/infra/config"
)

func TestVideoDiagnosticsSettingsPersistAndPreserveOmittedFields(t *testing.T) {
	cfg := testConfig(t)
	if cfg.Audit.VideoDiagnosticsEnabled || cfg.Audit.VideoDiagnosticsCleanupIntervalDays != 7 {
		t.Fatal("unexpected defaults")
	}
	repo := &runtimeSettingsRepositoryStub{}
	var applied config.Config
	service := NewService(cfg, time.Time{}, 0, repo, nil, func(next config.Config) { applied = next })
	input := service.Get().Config
	input.Audit.VideoDiagnosticsEnabled = true
	input.Audit.VideoDiagnosticsCleanupIntervalDays = 3
	if _, err := service.Update(context.Background(), service.Get().Revision, input); err != nil {
		t.Fatal(err)
	}
	if !applied.Audit.VideoDiagnosticsEnabled || applied.Audit.VideoDiagnosticsCleanupIntervalDays != 3 {
		t.Fatalf("applied = %#v", applied.Audit)
	}
	loaded, _, _, err := LoadPersisted(context.Background(), cfg, repo)
	if err != nil || !loaded.Audit.VideoDiagnosticsEnabled || loaded.Audit.VideoDiagnosticsCleanupIntervalDays != 3 {
		t.Fatalf("loaded = %#v, err=%v", loaded.Audit, err)
	}
	input = service.Get().Config
	input.Audit.VideoDiagnosticsEnabled = false
	input.Audit.VideoDiagnosticsEnabledProvided = false
	input.Audit.VideoDiagnosticsCleanupIntervalDays = 0
	input.Audit.VideoDiagnosticsCleanupIntervalDaysProvided = false
	if _, err := service.Update(context.Background(), service.Get().Revision, input); err != nil {
		t.Fatal(err)
	}
	if !applied.Audit.VideoDiagnosticsEnabled || applied.Audit.VideoDiagnosticsCleanupIntervalDays != 3 {
		t.Fatal("old client reset diagnostic settings")
	}
	input.Audit.VideoDiagnosticsEnabledProvided = true
	if _, err := service.Update(context.Background(), service.Get().Revision, input); err != nil {
		t.Fatal(err)
	}
	if applied.Audit.VideoDiagnosticsEnabled {
		t.Fatal("explicit disable was lost")
	}
}

func TestVideoDiagnosticsSettingsRejectInvalidDaysAndLoadLegacyDefaults(t *testing.T) {
	cfg := testConfig(t)
	service := NewService(cfg, time.Time{}, 0, &runtimeSettingsRepositoryStub{}, nil, nil)
	for _, days := range []int{-1, 0, 366} {
		input := service.Get().Config
		input.Audit.VideoDiagnosticsCleanupIntervalDays = days
		if _, err := service.Update(context.Background(), service.Get().Revision, input); err == nil {
			t.Fatalf("accepted %d days", days)
		}
	}
	value := toDomainConfig(cfg)
	value.Audit.VideoDiagnosticsEnabled = nil
	value.Audit.VideoDiagnosticsCleanupIntervalDays = nil
	loaded, _, _, err := LoadPersisted(context.Background(), cfg, &runtimeSettingsRepositoryStub{value: value, found: true})
	if err != nil || loaded.Audit.VideoDiagnosticsEnabled || loaded.Audit.VideoDiagnosticsCleanupIntervalDays != 7 {
		t.Fatalf("legacy defaults = %#v, err=%v", loaded.Audit, err)
	}
}
