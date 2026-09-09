package web

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type builtinSignerStub struct {
	err         error
	invalidated bool
}

func (s *builtinSignerStub) Sign(context.Context, string, string) (string, error) {
	return "local-signature", s.err
}
func (s *builtinSignerStub) Invalidate() { s.invalidated = true }

func TestBuiltinSigningDoesNotUseExternalService(t *testing.T) {
	adapter := &Adapter{cfg: Config{StatsigMode: "builtin", StatsigSignerURL: "http://unreachable.invalid/sign"}}
	signer := &builtinSignerStub{}
	adapter.SetBuiltinStatsig(signer)
	req, _ := http.NewRequest("POST", "https://grok.com/rest/app-chat/conversations/new", nil)
	if err := adapter.applySignedStatsig(context.Background(), req, "", nil); err != nil || req.Header.Get("x-statsig-id") != "local-signature" {
		t.Fatal("builtin signing failed", err)
	}
	signer.err = errors.New("unavailable")
	if err := adapter.applySignedStatsig(context.Background(), req, "", nil); err == nil || req.Header.Get("x-statsig-id") != "" {
		t.Fatal("missing builtin signature did not fail closed")
	}
	if !adapter.invalidateSignedStatsig("POST", req.URL.String()) || !signer.invalidated {
		t.Fatal("builtin invalidation not forwarded")
	}
}
