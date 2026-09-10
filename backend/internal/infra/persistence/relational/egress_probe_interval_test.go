package relational

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	egressapp "github.com/chenyme/grok2api/backend/internal/application/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/egress"
)

type previousProbeIntervalModel struct {
	ProbeIntervalSeconds int `gorm:"check:chk_egress_operations_config_probe_interval,probe_interval_seconds BETWEEN 60 AND 86400"`
}

func (previousProbeIntervalModel) TableName() string { return "egress_operations_config" }

func TestProbeIntervalUpgradePersistsDisabledAndLargeValues(t *testing.T) {
	db := openTestDatabase(t)
	ctx := context.Background()
	name := "chk_egress_operations_config_probe_interval"
	if err := db.db.Migrator().DropConstraint(&egressOperationsConfigModel{}, name); err != nil {
		t.Fatal(err)
	}
	if err := db.db.Migrator().CreateConstraint(&previousProbeIntervalModel{}, name); err != nil {
		t.Fatal(err)
	}
	if err := db.InitializeSchema(ctx); err != nil {
		t.Fatal(err)
	}
	definition, err := db.constraintDefinition(ctx, consoleConstraint{model: &egressOperationsConfigModel{}, table: "egress_operations_config", name: name})
	if err != nil || strings.Contains(definition, "86400") {
		t.Fatalf("constraint=%s err=%v", definition, err)
	}
	repo := NewEgressRepository(db)
	service := egressapp.NewService(repo, egressOperationsCipher(t), "test")
	for _, seconds := range []int{0, 60, 86401, 31536000, int(^uint(0) >> 1), 0} {
		saved, err := service.UpdateOperationsConfig(ctx, egressapp.OperationsConfigInput{ProbeIntervalSeconds: seconds, AssignmentIntervalSeconds: 300})
		if err != nil || saved.ProbeIntervalSeconds != seconds {
			t.Fatalf("save interval=%d saved=%d err=%v", seconds, saved.ProbeIntervalSeconds, err)
		}
		if err := db.InitializeSchema(ctx); err != nil {
			t.Fatal(err)
		}
		stored, err := repo.GetEgressOperationsConfig(ctx)
		if err != nil || stored.ProbeIntervalSeconds != seconds {
			t.Fatalf("reload interval=%d stored=%d err=%v", seconds, stored.ProbeIntervalSeconds, err)
		}
	}
	for _, seconds := range []int{-1, 1, 59} {
		if _, err := service.UpdateOperationsConfig(ctx, egressapp.OperationsConfigInput{ProbeIntervalSeconds: seconds, AssignmentIntervalSeconds: 300}); !errors.Is(err, egressapp.ErrInvalidInput) {
			t.Fatalf("invalid interval=%d err=%v", seconds, err)
		}
	}
}

type countingIntervalProber struct{ calls atomic.Int32 }

func (p *countingIntervalProber) ProbeEgressNode(context.Context, egress.Node) (egress.ProbeResult, error) {
	p.calls.Add(1)
	return egress.ProbeResult{Status: egress.ProbeStatusHealthy, Provider: egress.ProbeProviderCloudflare, TestedAt: time.Now().UTC()}, nil
}

func TestDisabledPeriodicProbesKeepManualChecksAndCanResume(t *testing.T) {
	db := openTestDatabase(t)
	ctx := context.Background()
	repo := NewEgressRepository(db)
	cipher := egressOperationsCipher(t)
	node := createHealthyEgressNode(t, ctx, repo, cipher, "manual-probe", 0)
	if err := db.db.Model(&egressNodeModel{}).Where("id = ?", node.ID).Update("last_probed_at", nil).Error; err != nil {
		t.Fatal(err)
	}
	service := egressapp.NewService(repo, cipher, "test")
	prober := &countingIntervalProber{}
	service.SetNodeProber(prober)
	save := func(seconds int) {
		t.Helper()
		if _, err := service.UpdateOperationsConfig(ctx, egressapp.OperationsConfigInput{ProbeIntervalSeconds: seconds, AssignmentIntervalSeconds: 300}); err != nil {
			t.Fatal(err)
		}
	}
	save(0)
	if err := service.RunMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if prober.calls.Load() != 0 {
		t.Fatal("disabled periodic probes still ran")
	}
	if due, err := repo.ListDueEgressNodes(ctx, time.Now().UTC(), 0, 32); err != nil || len(due) != 0 {
		t.Fatalf("disabled query returned nodes: %d err=%v", len(due), err)
	}
	if _, err := service.TestNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if prober.calls.Load() != 1 {
		t.Fatal("manual probe did not run")
	}
	if err := db.db.Model(&egressNodeModel{}).Where("id = ?", node.ID).Update("last_probed_at", time.Now().UTC().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	save(int(^uint(0) >> 1))
	if err := service.RunMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if prober.calls.Load() != 1 {
		t.Fatal("large interval overflow caused an immediate probe")
	}
	save(60)
	if err := service.RunMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if prober.calls.Load() != 2 {
		t.Fatal("periodic probes did not resume")
	}
}
