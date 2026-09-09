package relational

import (
	"context"
	"github.com/chenyme/grok2api/backend/internal/domain/settings"
	"github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatsigSecretsAndVersionsAreEncrypted(t *testing.T) {
	ctx := context.Background()
	db, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "statsig.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	cipher, _ := security.NewCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	opts := settings.DefaultStatsigBuiltin()
	opts.LLMKey = "private-model-key"
	runtime := NewRuntimeSettingsRepository(db, cipher)
	if _, _, err = runtime.Save(ctx, settings.Config{ProviderWeb: settings.ProviderWebConfig{StatsigBuiltin: &opts}}, 0); err != nil {
		t.Fatal(err)
	}
	loaded, _, _, _, err := runtime.Get(ctx)
	if err != nil || loaded.ProviderWeb.StatsigBuiltin.LLMKey != opts.LLMKey {
		t.Fatal("key round trip failed", err)
	}
	if opts.LLMKey != "private-model-key" {
		t.Fatal("save mutated caller configuration")
	}
	repo := NewStatsigRepository(db, cipher)
	state := statsig.State{Status: "ready", Active: &statsig.Version{ID: "version-one", Code: "private-algorithm"}}
	revision, err := repo.Save(ctx, state, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, rev, err := repo.Load(ctx)
	if err != nil || rev != revision || got.Active.ID != "version-one" {
		t.Fatal("version round trip failed", err)
	}
	if _, err = repo.Save(ctx, state, 0); err == nil {
		t.Fatal("stale update accepted")
	}
	var rows []runtimeSettingsModel
	if err = db.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(row.ValueJSON, "private-model-key") || strings.Contains(row.ValueJSON, "private-algorithm") {
			t.Fatal("secret stored as plaintext")
		}
	}
}
