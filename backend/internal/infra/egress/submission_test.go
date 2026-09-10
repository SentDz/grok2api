package egress

import (
	"context"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	domain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
)

func TestWebSubmissionRouting(t *testing.T) {
	cipher, err := security.NewCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := cipher.Encrypt("http://submit.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := cipher.Encrypt("cf_clearance=proxy-cookie")
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name       string
		scope      domain.Scope
		bound      uint64
		otherNodes bool
		cooling    bool
		wantNode   uint64
		wantErr    bool
	}{
		{name: "unbound submission", scope: domain.ScopeWebSubmit, wantNode: 1},
		{name: "unbound upload", scope: domain.ScopeWeb},
		{name: "unbound download", scope: domain.ScopeWebAsset},
		{name: "bound submission", scope: domain.ScopeWebSubmit, bound: 1, wantNode: 1},
		{name: "bound upload ignores other proxies", scope: domain.ScopeWeb, bound: 1, otherNodes: true},
		{name: "bound download ignores resource proxy", scope: domain.ScopeWebAsset, bound: 1, otherNodes: true},
		{name: "cooling submission fails closed", scope: domain.ScopeWebSubmit, bound: 1, cooling: true, wantErr: true},
		{name: "cooling submission does not block upload", scope: domain.ScopeWeb, bound: 1, cooling: true},
		{name: "cooling submission does not block download", scope: domain.ScopeWebAsset, bound: 1, cooling: true},
		{name: "dedicated submission preferred", scope: domain.ScopeWebSubmit, otherNodes: true, wantNode: 1},
		{name: "ordinary Web binding preserved", scope: domain.ScopeWebSubmit, bound: 2, otherNodes: true, wantNode: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := domain.Node{ID: 1, Name: "submit", Scope: domain.ScopeWebSubmit, Enabled: true, Health: 1, EncryptedProxyURL: proxy, EncryptedCloudflareCookie: cookie}
			if tc.cooling {
				node.CooldownUntil = &until
			}
			nodes := []domain.Node{node}
			if tc.otherNodes {
				nodes = append(nodes, domain.Node{ID: 2, Scope: domain.ScopeWeb, Enabled: true, Health: 1, EncryptedProxyURL: proxy}, domain.Node{ID: 3, Scope: domain.ScopeWebAsset, Enabled: true, Health: 1, EncryptedProxyURL: proxy})
			}
			manager := NewManager(egressRepositoryTestStub{nodes: nodes}, cipher)
			lease, err := manager.AcquireCredential(context.Background(), tc.scope, account.Credential{ID: 42, Provider: account.ProviderWeb, EgressNodeID: tc.bound, EncryptedCloudflareCookie: cookie})
			if tc.wantErr {
				if err == nil {
					lease.Release()
					t.Fatal("unavailable submission proxy accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			if lease.NodeID != tc.wantNode || (lease.ProxyURL != "") != (tc.wantNode != 0) {
				t.Fatalf("node = %d, proxied = %t; want node %d", lease.NodeID, lease.ProxyURL != "", tc.wantNode)
			}
			if tc.bound == 1 && tc.wantNode == 0 && lease.CFCookies != "" {
				t.Fatal("proxy-bound clearance leaked onto direct connection")
			}
		})
	}
}

func TestWebSubmissionPreservesLegacyFallback(t *testing.T) {
	cipher, err := security.NewCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := cipher.Encrypt("http://fallback.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultOperationsConfig()
	config.Fallbacks[domain.ScopeWeb] = domain.FallbackConfig{Mode: domain.FallbackModeFixed, NodeID: 1}
	manager := NewManager(fallbackEgressRepository{
		egressRepositoryTestStub: egressRepositoryTestStub{nodes: []domain.Node{{ID: 1, Scope: domain.ScopeWeb, Enabled: true, Health: 1, EncryptedProxyURL: proxy}}},
		config:                   config,
	}, cipher)
	lease, err := manager.AcquireCredential(context.Background(), domain.ScopeWebSubmit, account.Credential{ID: 42, Provider: account.ProviderWeb})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if lease.NodeID != 1 || lease.Scope != domain.ScopeWeb {
		t.Fatalf("legacy fallback changed: node=%d scope=%s", lease.NodeID, lease.Scope)
	}
}
