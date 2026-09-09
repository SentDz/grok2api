package gateway

import (
	"context"
	accountapp "github.com/chenyme/grok2api/backend/internal/application/account"
	clientkeyapp "github.com/chenyme/grok2api/backend/internal/application/clientkey"
	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/clientkey"
	"github.com/chenyme/grok2api/backend/internal/infra/persistence/relational"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/runtime/memory"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type repairModelAdapter struct {
	failoverAdapter
	used account.Provider
}

func (a *repairModelAdapter) Definition() provider.Definition {
	return provider.Definition{Provider: account.ProviderBuild, Conversation: provider.ConversationSurface{ChatCompletions: true}}
}
func (a *repairModelAdapter) ForwardResponse(_ context.Context, r provider.ResponseResourceRequest) (*provider.Response, error) {
	a.used = r.Credential.Provider
	return &provider.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"repair-response","choices":[{"message":{"content":"repaired-code"}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`))}, nil
}

func TestStatsigRepairUsesBuildPoolAndRecordsUsage(t *testing.T) {
	ctx := context.Background()
	db, err := relational.OpenSQLite(ctx, filepath.Join(t.TempDir(), "repair.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	accounts := relational.NewAccountRepository(db)
	models := relational.NewModelRepository(db)
	audits := relational.NewAuditRepository(db)
	keys := relational.NewClientKeyRepository(db)
	credential, _, err := accounts.UpsertByIdentity(ctx, account.Credential{Provider: account.ProviderBuild, Name: "repair-build", SourceKey: "repair", EncryptedAccessToken: "test", ExpiresAt: time.Now().Add(time.Hour), Enabled: true, AuthStatus: account.AuthStatusActive, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = models.UpsertDiscovered(ctx, account.ProviderBuild, []string{"grok-test"}); err != nil {
		t.Fatal(err)
	}
	if err = models.ReplaceAccountCapabilities(ctx, credential.ID, []string{"grok-test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	key, err := keys.Create(ctx, clientkey.Key{Name: "internal-repair", Prefix: "repair", SecretHash: strings.Repeat("a", 64), EncryptedSecret: "test-encrypted-key", Enabled: true, RPMLimit: 120, MaxConcurrent: 8})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &repairModelAdapter{}
	registry := provider.NewRegistry(adapter)
	sticky := memory.NewStickyStore()
	accountService := accountapp.NewService(accounts, audits, memory.NewDeviceSessionStore(), sticky, registry, testCipher(t), nil)
	selector := NewSelector(accounts, memory.NewConcurrencyLimiter(), sticky, registry, time.Hour, time.Second, time.Minute)
	s := NewService(models, audits, accountService, clientkeyapp.NewService(nil, nil, nil, 60, 4, nil), registry, selector, relational.NewResponseRepository(db), 1)
	s.ConfigureAccountTestKey(key)
	result, err := s.CompleteStatsigRepair(ctx, "grok-test", "repair prompt")
	if err != nil || result != "repaired-code" {
		t.Fatal(result, err)
	}
	if adapter.used != account.ProviderBuild {
		t.Fatal("repair escaped the Build pool")
	}
	logs, total, err := audits.List(ctx, 0, 10)
	if err != nil || total != 1 || logs[0].InputTokens != 100 || logs[0].OutputTokens != 20 || !strings.HasPrefix(logs[0].RequestID, "statsig_") {
		t.Fatalf("missing repair audit: total=%d err=%v", total, err)
	}
	if _, err = s.CompleteStatsigRepair(ctx, "Web/grok-test", "prompt"); err == nil {
		t.Fatal("non-Build model accepted")
	}
}
