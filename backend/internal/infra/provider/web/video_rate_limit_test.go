package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	domain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

func TestOnlyVideoSubmission429CoolsNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":8,"message":"Too many requests. Wait a moment and try again."}}`)
	}))
	defer server.Close()
	adapter, credential := testMediaAdapter(t, server.URL)
	adapter.egress = newSubmissionTestManager(t, adapter.cipher, server.URL)
	lease, err := adapter.egress.AcquireCredential(context.Background(), domain.ScopeWebSubmit, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	// A generic media POST 429 is not sufficient to quarantine the node.
	response, err := adapter.postJSON(context.Background(), adapter.config(), lease, "test", server.URL+"/rest/media/post/create", map[string]string{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	check, err := adapter.egress.AcquireCredential(context.Background(), domain.ScopeWebSubmit, credential)
	if err != nil {
		t.Fatalf("non-submission request cooled the node: %v", err)
	}
	check.Release()
	_, err = adapter.postVideoSubmission(context.Background(), adapter.config(), lease, "test", map[string]string{}, server.URL)
	var limited *provider.VideoSubmissionRateLimitError
	if !errors.As(err, &limited) || limited.NodeID != 1 || limited.CoolingError != nil || limited.HTTPStatusCode() != 429 {
		t.Fatalf("submission error=%v", err)
	}
	if time.Until(limited.CooldownUntil) < 179*time.Second {
		t.Fatal("three-minute hold missing")
	}
	request, _ := http.NewRequest("GET", server.URL, nil)
	if _, err := lease.Do(request); !errors.Is(err, infraegress.ErrNodeRateLimited) {
		t.Fatalf("held lease bypassed cooldown: %v", err)
	}
}
