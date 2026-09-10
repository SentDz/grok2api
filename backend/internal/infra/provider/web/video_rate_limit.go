package web

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

func (a *Adapter) SelectVideoRetryNode(ctx context.Context, excluded []uint64) (uint64, error) {
	return a.egress.SelectVideoRetryNode(ctx, excluded)
}

func (a *Adapter) postVideoSubmission(ctx context.Context, cfg Config, lease *egress.Lease, token string, payload any, referer string) (*http.Response, error) {
	response, err := a.postJSONWithReferer(ctx, cfg, lease, token, cfg.BaseURL+"/rest/app-chat/conversations/new", payload, time.Duration(cfg.VideoTimeoutSeconds)*time.Second, referer)
	if err != nil || response.StatusCode != http.StatusTooManyRequests {
		return response, err
	}
	defer response.Body.Close()
	coolCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	until, coolErr := a.egress.CoolVideoNode(coolCtx, lease.NodeID)
	cancel()
	if coolErr != nil {
		a.log().Error("video_node_cooldown_failed", "node_id", lease.NodeID, "error", coolErr)
	}
	// The HTTP status already proves rejection even if reading its body fails.
	body, readErr := io.ReadAll(io.LimitReader(response.Body, webMediaDiagnosticBodyLimit+1))
	truncated := len(body) > webMediaDiagnosticBodyLimit
	if truncated {
		body = body[:webMediaDiagnosticBodyLimit]
	}
	upstream := newWebMediaUpstreamError(response.StatusCode, body, truncated)
	if readErr != nil {
		upstream.summary = "Grok Web 视频提交返回 HTTP 429，读取限流响应失败"
	}
	event := videoRequestEvent(ctx, "video_node_rate_limited", response.StatusCode, nil)
	if coolErr != nil {
		event.Stage = "video_node_cooldown_failed"
	} else if lease.NodeID == 0 {
		event.Stage = "video_direct_rate_limited"
	} else {
		event.CooldownUntil = &until
	}
	provider.ReportVideoEvent(ctx, event)
	return nil, &provider.VideoSubmissionRateLimitError{NodeID: lease.NodeID, CooldownUntil: until, CoolingError: coolErr, Err: upstream}
}
