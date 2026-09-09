package statsig

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/chenyme/grok2api/backend/internal/domain/settings"
	domain "github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"github.com/chenyme/grok2api/backend/internal/infra/statsigeval"
	"testing"
	"time"
)

type memoryRepo struct {
	state    domain.State
	revision uint64
	fail     bool
}

func (r *memoryRepo) Load(context.Context) (domain.State, uint64, error) {
	raw, _ := json.Marshal(r.state)
	var s domain.State
	_ = json.Unmarshal(raw, &s)
	return s, r.revision, nil
}
func (r *memoryRepo) Save(_ context.Context, s domain.State, revision uint64) (uint64, error) {
	if r.fail || revision != r.revision {
		return revision, errors.New("conflict")
	}
	r.state = s
	r.revision++
	return r.revision, nil
}
func sample(first byte, hex string) domain.Sample {
	seed := make([]byte, 48)
	seed[0] = first
	return domain.Sample{Seed: base64.RawStdEncoding.EncodeToString(seed), HEX: hex, Paths: []string{"a", "b", "c", "d"}}
}

func testService(t *testing.T) (*Service, *memoryRepo, *settings.StatsigBuiltinConfig) {
	t.Helper()
	repo := &memoryRepo{state: domain.State{Status: "unverified"}}
	cfg := settings.DefaultStatsigBuiltin()
	s, err := New(context.Background(), repo, nil, func() (string, settings.StatsigBuiltinConfig) { return "builtin", cfg }, func(context.Context, uint64) (domain.Capture, error) {
		return domain.Capture{Samples: []domain.Sample{sample(1, "ab"), sample(2, "cd")}, Fingerprint: "test"}, nil
	}, func(context.Context, uint64, domain.Sample) error { return nil }, func(context.Context, settings.StatsigBuiltinConfig, string) (string, error) {
		return `{"code":"function computeHex(seed,paths){return seed[0]===1?'ab':'cd';}"}`, nil
	}, func(domain.Sample, string, string, int64) (string, error) { return "signature", nil })
	if err != nil {
		t.Fatal(err)
	}
	s.config = func() (string, settings.StatsigBuiltinConfig) { return "builtin", cfg }
	s.eval = func(_ context.Context, code string, sample domain.Sample) (string, error) {
		return statsigeval.Evaluate(statsigeval.Input{Code: code, Sample: sample})
	}
	return s, repo, &cfg
}

func TestModelRepairActivationAndRollback(t *testing.T) {
	s, repo, _ := testService(t)
	if err := s.Update(context.Background(), "refresh"); err != nil {
		t.Fatal(err)
	}
	if repo.state.Active == nil || repo.state.Active.Source != "model" || s.Status().State != "ready" {
		t.Fatal("model code was not activated")
	}
	first := repo.state.Active.ID
	s.capture = func(context.Context, uint64) (domain.Capture, error) {
		return domain.Capture{Samples: []domain.Sample{sample(1, "ab"), sample(2, "cd")}, Fingerprint: "next"}, nil
	}
	if err := s.Update(context.Background(), "refresh"); err != nil {
		t.Fatal(err)
	}
	if repo.state.Previous == nil || repo.state.Active.ID == first {
		t.Fatal("previous version not retained")
	}
	if err := s.Update(context.Background(), "rollback"); err != nil {
		t.Fatal(err)
	}
	if s.Status().ActiveID != first {
		t.Fatal("rollback failed")
	}
}

func TestRejectedCodeNeverReplacesActiveVersion(t *testing.T) {
	s, repo, cfg := testService(t)
	cfg.LLMEnabled = false
	if err := s.Update(context.Background(), "refresh"); err != nil {
		t.Fatal(err)
	}
	previous := repo.state.Active.ID
	cfg.LLMEnabled = true
	s.complete = func(context.Context, settings.StatsigBuiltinConfig, string) (string, error) {
		return `{"code":"function computeHex(){return 'ab';}"}`, nil
	}
	if err := s.Update(context.Background(), "refresh"); err == nil {
		t.Fatal("code that fails second sample was accepted")
	}
	if repo.state.Active.ID != previous || s.Status().State != "degraded" {
		t.Fatal("active version lost")
	}
	if repo.state.NextCheck == nil || time.Until(*repo.state.NextCheck) < 4*time.Minute {
		t.Fatal("missing failure backoff")
	}
}

func TestUpstreamAndPersistenceFailuresDoNotActivate(t *testing.T) {
	s, repo, _ := testService(t)
	s.verify = func(context.Context, uint64, domain.Sample) error { return errors.New("upstream rejected") }
	if err := s.Update(context.Background(), "refresh"); err == nil || s.Status().ActiveID != "" {
		t.Fatal("unaccepted material activated")
	}
	s.verify = func(context.Context, uint64, domain.Sample) error { return nil }
	repo.fail = true
	if err := s.Update(context.Background(), "refresh"); err == nil || s.Status().ActiveID != "" {
		t.Fatal("unpersisted version activated")
	}
}

func TestInvalidatedVersionStaysBlockedAfterFailedRefresh(t *testing.T) {
	s, _, _ := testService(t)
	if err := s.Update(context.Background(), "refresh"); err != nil {
		t.Fatal(err)
	}
	s.Invalidate()
	s.capture = func(context.Context, uint64) (domain.Capture, error) { return domain.Capture{}, errors.New("offline") }
	_ = s.Update(context.Background(), "refresh")
	if _, err := s.Sign(context.Background(), "POST", "/test"); err == nil {
		t.Fatal("known invalid signature resumed")
	}
}

func TestSettingChangeDiscardsCandidate(t *testing.T) {
	s, _, cfg := testService(t)
	s.verify = func(context.Context, uint64, domain.Sample) error { cfg.LLMModel = "changed"; return nil }
	if err := s.Update(context.Background(), "refresh"); err == nil || s.Status().ActiveID != "" {
		t.Fatal("candidate activated after settings changed")
	}
}
