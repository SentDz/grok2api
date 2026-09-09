package settings

import (
	"context"
	settingsdomain "github.com/chenyme/grok2api/backend/internal/domain/settings"
	"testing"
	"time"
)

func TestStatsigBuiltinKeyRedactionPreservationAndClear(t *testing.T) {
	cfg := testConfig(t)
	cfg.Provider.Web.StatsigBuiltin = settingsdomain.DefaultStatsigBuiltin()
	cfg.Provider.Web.StatsigBuiltin.LLMKey = "secret"
	repo := &runtimeSettingsRepositoryStub{}
	service := NewService(cfg, time.Time{}, 0, repo, nil, nil)
	input := service.Get().Config
	if input.ProviderWeb.StatsigBuiltin.LLMKey != "" || !input.ProviderWeb.StatsigBuiltin.LLMKeyConfigured {
		t.Fatal("secret exposed in snapshot")
	}
	result, err := service.Update(context.Background(), 0, input)
	if err != nil {
		t.Fatal(err)
	}
	_, runtime := service.StatsigRuntime()
	if runtime.LLMKey != "secret" {
		t.Fatal("blank key overwrote saved key")
	}
	input = result.Config
	input.ProviderWeb.StatsigBuiltin = nil
	result, err = service.Update(context.Background(), result.Revision, input)
	if err != nil {
		t.Fatal(err)
	}
	_, runtime = service.StatsigRuntime()
	if runtime.LLMKey != "secret" {
		t.Fatal("legacy client erased key")
	}
	input = result.Config
	input.ProviderWeb.StatsigBuiltin.ClearLLMKey = true
	_, err = service.Update(context.Background(), result.Revision, input)
	if err != nil {
		t.Fatal(err)
	}
	_, runtime = service.StatsigRuntime()
	if runtime.LLMKey != "" {
		t.Fatal("clear key ignored")
	}
}
