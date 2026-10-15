package settings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/infra/config"
)

func TestUploadSettingsPersistAndApplyWithoutRestart(t *testing.T) {
	cfg := testConfig(t)
	repo := &runtimeSettingsRepositoryStub{}
	var applied config.Config
	service := NewService(cfg, time.Time{}, 0, repo, nil, func(next config.Config) { applied = next })
	input := service.Get().Config
	if input.ProviderWeb.ReferenceImageUploadProxy || cfg.Provider.Web.UploadTimeout.Value() != time.Minute {
		t.Fatal("unexpected upload defaults")
	}
	input.ProviderWeb.ReferenceImageUploadProxy = true
	input.ProviderWeb.UploadTimeout = "180s"
	snapshot, err := service.Update(context.Background(), 0, input)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Provider.Web.ReferenceImageUploadProxy || applied.Provider.Web.UploadTimeout.Value() != 3*time.Minute || len(snapshot.RestartRequired) != 0 {
		t.Fatalf("upload settings did not hot apply: %+v", snapshot)
	}
	loaded, _, _, err := LoadPersisted(context.Background(), cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Provider.Web.ReferenceImageUploadProxy || loaded.Provider.Web.UploadTimeout.Value() != 3*time.Minute {
		t.Fatal("upload settings lost after reload")
	}
	// Older clients must not overwrite settings they do not know about.
	legacy := snapshot.Config
	legacy.ProviderWeb.ReferenceImageUploadProxy = false
	legacy.ProviderWeb.ReferenceImageUploadProxyProvided = false
	legacy.ProviderWeb.UploadTimeout = ""
	snapshot, err = service.Update(context.Background(), snapshot.Revision, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Provider.Web.ReferenceImageUploadProxy || applied.Provider.Web.UploadTimeout.Value() != 3*time.Minute {
		t.Fatal("legacy update reset upload settings")
	}
	input = snapshot.Config
	input.ProviderWeb.ReferenceImageUploadProxy = false
	input.ProviderWeb.UploadTimeout = "60s"
	if _, err := service.Update(context.Background(), snapshot.Revision, input); err != nil {
		t.Fatal(err)
	}
	if applied.Provider.Web.ReferenceImageUploadProxy || applied.Provider.Web.UploadTimeout.Value() != time.Minute {
		t.Fatal("explicit disable was not applied")
	}
	for _, invalid := range []string{"0s", "-1s", "500ms", "31m", "invalid"} {
		input = service.Get().Config
		input.ProviderWeb.UploadTimeout = invalid
		if _, err := service.Update(context.Background(), service.Get().Revision, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("upload timeout %q accepted: %v", invalid, err)
		}
	}
}

func TestLegacyPersistedSettingsPreserveUploadDefaults(t *testing.T) {
	for _, custom := range []bool{false, true} {
		cfg := testConfig(t)
		if custom {
			cfg.Provider.Web.ReferenceImageUploadProxy = true
			cfg.Provider.Web.UploadTimeout = config.Duration(2 * time.Minute)
		}
		persisted := toDomainConfig(cfg)
		persisted.ProviderWeb.UploadTimeout = 0
		persisted.ProviderWeb.ReferenceImageUploadProxy = nil
		repo := &runtimeSettingsRepositoryStub{value: persisted, found: true}
		loaded, _, _, err := LoadPersisted(context.Background(), cfg, repo)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Provider.Web.UploadTimeout != cfg.Provider.Web.UploadTimeout || loaded.Provider.Web.ReferenceImageUploadProxy != custom {
			t.Fatal("old persisted settings did not preserve base upload configuration")
		}
	}
}
