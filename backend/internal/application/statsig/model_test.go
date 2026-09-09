package statsig

import (
	"context"
	"encoding/json"
	"github.com/chenyme/grok2api/backend/internal/domain/settings"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalModelProtocolAndRedirectProtection(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	redirect := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("wrong model request")
		}
		if redirect {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(307)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["model"] != "test-model" {
			t.Error("model missing")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"code"}}]}`))
	}))
	defer server.Close()
	cfg := settings.DefaultStatsigBuiltin()
	cfg.LLMProvider = "external"
	cfg.LLMURL = server.URL + "/v1"
	cfg.LLMKey = "test-secret"
	cfg.LLMModel = "test-model"
	value, err := CompleteExternal(context.Background(), cfg, "prompt")
	if err != nil || value != "code" {
		t.Fatal(value, err)
	}
	redirect = true
	_, err = CompleteExternal(context.Background(), cfg, "prompt")
	if err == nil || redirected || strings.Contains(err.Error(), cfg.LLMKey) {
		t.Fatal("redirect followed or secret leaked")
	}
}
