package gateway

import (
	"context"
	"testing"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	"github.com/chenyme/grok2api/backend/internal/domain/audit"
	egressdomain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/media"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

type submissionAuditRepository struct {
	repository.EgressRepository
	node egressdomain.Node
}

func (r submissionAuditRepository) ListEgressNodes(_ context.Context, scope egressdomain.Scope, _ repository.SortQuery) ([]egressdomain.Node, error) {
	if scope == r.node.Scope {
		return []egressdomain.Node{r.node}, nil
	}
	return nil, nil
}

func TestSubmissionAuditSurvivesDirectMediaTraffic(t *testing.T) {
	cipher, err := security.NewCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := cipher.Encrypt("http://submit.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	manager := infraegress.NewManager(submissionAuditRepository{node: egressdomain.Node{ID: 7, Name: "submit", Scope: egressdomain.ScopeWebSubmit, Enabled: true, Health: 1, EncryptedProxyURL: proxy}}, cipher)
	ctx, trace := infraegress.WithTrace(context.Background())
	for _, scope := range []egressdomain.Scope{egressdomain.ScopeWeb, egressdomain.ScopeWebSubmit, egressdomain.ScopeWeb, egressdomain.ScopeWebAsset} {
		lease, err := manager.Acquire(ctx, scope, "test")
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
	}
	record := audit.Record{}
	applyAuditEgress(&record, trace, account.ProviderWeb)
	if record.EgressNodeID == nil || *record.EgressNodeID != 7 || record.EgressScope != string(egressdomain.ScopeWebSubmit) || record.EgressMode != audit.EgressModeProxy {
		t.Fatalf("audit node=%v scope=%s mode=%s", record.EgressNodeID, record.EgressScope, record.EgressMode)
	}
	job := media.Job{}
	applyMediaJobEgress(&job, trace, account.ProviderWeb)
	if job.EgressNodeID == nil || *job.EgressNodeID != 7 || job.EgressScope != record.EgressScope || job.EgressMode != string(audit.EgressModeProxy) {
		t.Fatalf("job node=%v scope=%s mode=%s", job.EgressNodeID, job.EgressScope, job.EgressMode)
	}
}
