package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

func TestVideoNotFoundStopsAfterOneRequest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		submission bool
		truncated  bool
	}{
		{"poll", false, false},
		{"submission", true, false},
		{"poll with truncated error body", false, true},
		{"submission with truncated error body", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if tc.truncated {
					w.Header().Set("Content-Length", "10000")
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":{"message":"Video not found"}}`)
			}))
			defer server.Close()
			adapter, credential := testMediaAdapter(t, server.URL)
			lease, err := adapter.egress.AcquireCredential(context.Background(), egress.ScopeWeb, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started := time.Now()
			if tc.submission {
				var response *http.Response
				response, err = adapter.postVideoSubmission(ctx, adapter.config(), lease, "test-sso", map[string]any{}, server.URL)
				if err == nil {
					_, err = adapter.finishVideoResponse(ctx, adapter.config(), lease, "test-sso", response, nil)
				}
			} else {
				_, err = adapter.pollMediaPostVideo(ctx, adapter.config(), lease, "test-sso", "8efff9ef-8aa4-4a6e-ab73-aa2b5c7bccb5", nil)
			}
			if status, ok := provider.ErrorHTTPStatus(err); !ok || status != http.StatusNotFound {
				t.Fatalf("expected HTTP 404, got %v", err)
			}
			if requests.Load() != 1 || time.Since(started) >= 2*time.Second {
				t.Fatalf("404 was retried: calls=%d duration=%s", requests.Load(), time.Since(started))
			}
		})
	}
}
