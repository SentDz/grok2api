package relational

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	egressapp "github.com/chenyme/grok2api/backend/internal/application/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/account"
	domain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
)

func TestVideoNodeCooldownSurvivesProbesEditsAndOtherManagers(t *testing.T) {
	ctx := context.Background()
	db := openTestDatabase(t)
	repo := NewEgressRepository(db)
	cipher := egressOperationsCipher(t)
	proxy, err := cipher.Encrypt("http://proxy.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	node, err := repo.CreateEgressNode(ctx, domain.Node{Name: "pool", Scope: domain.ScopeWebSubmit, Enabled: true, ProxyPool: true, EncryptedProxyURL: proxy, Health: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateEgressNode(ctx, domain.Node{Name: "other", Scope: domain.ScopeWebSubmit, Enabled: true, EncryptedProxyURL: proxy, Health: 1})
	if err != nil {
		t.Fatal(err)
	}
	first, second := infraegress.NewManager(repo, cipher), infraegress.NewManager(repo, cipher)
	credential := account.Credential{ID: 42, Provider: account.ProviderWeb, EgressNodeID: node.ID}
	held, err := second.AcquireCredential(ctx, domain.ScopeWebSubmit, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	started := time.Now().UTC()
	until, err := first.CoolVideoNode(ctx, node.ID)
	if err != nil || until.Sub(started) < 3*time.Minute || until.Sub(started) > 3*time.Minute+time.Second {
		t.Fatalf("cooldown=%s err=%v", until.Sub(started), err)
	}
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://never-connect.example", nil)
	if _, err := held.Do(request); !errors.Is(err, infraegress.ErrNodeRateLimited) {
		t.Fatalf("held lease bypassed cooldown: %v", err)
	}
	if _, err := second.AcquireCredential(ctx, domain.ScopeWebSubmit, credential); !errors.Is(err, infraegress.ErrNodeRateLimited) {
		t.Fatalf("binding bypassed cooldown: %v", err)
	}
	if err := repo.UpdateEgressNodeHealth(ctx, node.ID, 1, 0, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateEgressNodeProbe(ctx, node.ID, proxy, domain.ProbeResult{Status: domain.ProbeStatusHealthy, TestedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// This stale snapshot predates the 429 and must not erase its separate hold.
	node.Name = "renamed"
	if _, err := repo.UpdateEgressNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetEgressNode(ctx, node.ID)
	if err != nil || !stored.RateLimited(time.Now().UTC()) {
		t.Fatalf("hold lost: %#v err=%v", stored.RateLimitUntil, err)
	}
	service := egressapp.NewService(repo, cipher, "test")
	public, _, err := service.List(ctx, 1, 20, "", egressapp.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range public {
		if value.ID == node.ID && (value.CooldownUntil == nil || value.LastError == "") {
			t.Fatal("proxy pool UI concealed rate-limit cooldown")
		}
	}
	selected, err := second.SelectVideoRetryNode(ctx, []uint64{node.ID})
	if err != nil || selected != other.ID {
		t.Fatalf("retry node=%d err=%v", selected, err)
	}
	lease, err := second.AcquireCredential(infraegress.WithVideoRetryNode(ctx, selected), domain.ScopeWebSubmit, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if lease.NodeID != other.ID {
		t.Fatal("retry followed original binding")
	}
	if _, err := second.SelectVideoRetryNode(ctx, []uint64{node.ID, other.ID}); !errors.Is(err, infraegress.ErrVideoRetryEgressUnavailable) {
		t.Fatalf("exhausted nodes: %v", err)
	}
	past := time.Now().UTC().Add(-time.Second)
	if err := db.db.Model(&egressNodeModel{}).Where("id = ?", node.ID).Update("rate_limit_until", past).Error; err != nil {
		t.Fatal(err)
	}
	third := infraegress.NewManager(repo, cipher)
	recovered, err := third.AcquireCredential(ctx, domain.ScopeWebSubmit, credential)
	if err != nil {
		t.Fatalf("expired hold still blocked: %v", err)
	}
	recovered.Release()
}
