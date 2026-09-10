package egress

import (
	"context"
	"errors"
	"fmt"
	"time"

	domain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/repository"
)

var ErrVideoRetryEgressUnavailable = errors.New("没有其他可用的视频提交代理节点")
var ErrNodeRateLimited = errors.New("代理节点因视频提交 429 冷却中")

type videoRetryNodeKey struct{}

// A retry route is temporary and never changes the account's persisted binding.
func WithVideoRetryNode(ctx context.Context, nodeID uint64) context.Context {
	return context.WithValue(ctx, videoRetryNodeKey{}, nodeID)
}

func (m *Manager) CoolVideoNode(ctx context.Context, nodeID uint64) (time.Time, error) {
	until := time.Now().UTC().Add(domain.VideoRateLimitCooldown)
	if nodeID == 0 {
		return until, nil
	}
	var err error
	if store, ok := m.repository.(repository.EgressRateLimitRepository); ok {
		err = store.RateLimitEgressNode(ctx, nodeID, until)
	} else {
		var node domain.Node
		node, err = m.repository.GetEgressNode(ctx, nodeID)
		if err == nil {
			node.RateLimitUntil = &until
			_, err = m.repository.UpdateEgressNode(ctx, node)
		}
	}
	if err != nil {
		return until, err
	}
	for _, scope := range allEgressScopes() {
		m.invalidateNodes(scope)
	}
	return until, nil
}

// SelectVideoRetryNode reads fresh node state and never falls back to direct.
// Exclusions last for the whole video job, even after a node's hold expires.
func (m *Manager) SelectVideoRetryNode(ctx context.Context, excluded []uint64) (uint64, error) {
	blocked := make(map[uint64]bool, len(excluded))
	for _, id := range excluded {
		blocked[id] = true
	}
	now := time.Now().UTC()
	config, supported, err := m.loadOperationsConfig(ctx, now)
	if err != nil {
		return 0, err
	}
	reserved := map[uint64]bool{}
	if supported {
		for _, scope := range allEgressScopes() {
			fallback := config.FallbackFor(scope)
			if fallback.Mode == domain.FallbackModeFixed {
				reserved[fallback.NodeID] = true
			}
		}
	}
	for _, scope := range []domain.Scope{domain.ScopeWebSubmit, domain.ScopeWeb} {
		nodes, err := m.repository.ListEgressNodes(ctx, scope, repository.SortQuery{})
		if err != nil {
			return 0, err
		}
		var available []domain.Node
		for _, node := range nodes {
			if blocked[node.ID] || reserved[node.ID] || !node.Enabled || node.EncryptedProxyURL == "" || node.RateLimited(now) {
				continue
			}
			if node.CooldownUntil != nil && now.Before(*node.CooldownUntil) && !m.isProxyPoolNode(node) {
				continue
			}
			available = append(available, node)
		}
		if len(available) > 0 {
			return m.selectNode(available, "").ID, nil
		}
	}
	if supported {
		for _, scope := range []domain.Scope{domain.ScopeWebSubmit, domain.ScopeWeb} {
			fallback := config.FallbackFor(scope)
			if fallback.Mode != domain.FallbackModeFixed || blocked[fallback.NodeID] {
				continue
			}
			node, err := m.fixedFallbackNode(ctx, domain.ScopeWebSubmit, fallback.NodeID)
			if err == nil {
				return node.ID, nil
			}
		}
	}
	return 0, ErrVideoRetryEgressUnavailable
}

func (m *Manager) checkNodeRateLimit(ctx context.Context, node domain.Node) error {
	if node.ID == 0 || node.Scope == domain.ScopeBuild {
		return nil
	}
	until := node.RateLimitUntil
	if store, ok := m.repository.(repository.EgressRateLimitRepository); ok {
		var err error
		until, err = store.GetEgressNodeRateLimit(ctx, node.ID)
		if err != nil {
			return err
		}
	} else {
		current, err := m.repository.GetEgressNode(ctx, node.ID)
		if err != nil {
			return err
		}
		until = current.RateLimitUntil
	}
	if until != nil && time.Now().UTC().Before(*until) {
		m.invalidateNodes(node.Scope)
		return fmt.Errorf("%w: %d，至 %s", ErrNodeRateLimited, node.ID, until.UTC().Format(time.RFC3339))
	}
	return nil
}
