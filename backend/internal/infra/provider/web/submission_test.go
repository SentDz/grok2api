package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	egressdomain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
)

const submissionTestAgent = "Mozilla/5.0 submission-test"

func newSubmissionTestManager(t *testing.T, cipher *security.Cipher, upstreamURL string) *infraegress.Manager {
	t.Helper()
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	var proxied atomic.Int32
	forward := httputil.NewSingleHostReverseProxy(upstream)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		if r.Method != http.MethodConnect {
			forward.ServeHTTP(w, r)
			return
		}
		if r.Host != upstream.Host {
			http.Error(w, "unexpected proxy destination", http.StatusBadGateway)
			return
		}
		remote, err := net.DialTimeout("tcp", upstream.Host, time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer remote.Close()
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		go func() { _, _ = io.Copy(remote, buffered) }()
		_, _ = io.Copy(client, remote)
	}))
	t.Cleanup(proxy.Close)
	t.Cleanup(func() {
		if proxied.Load() == 0 {
			t.Error("submission did not traverse the proxy")
		}
	})
	encryptedProxy, err := cipher.Encrypt(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	return infraegress.NewManager(&recordingWebEgressRepository{node: egressdomain.Node{
		ID: 1, Name: "submit", Scope: egressdomain.ScopeWebSubmit, Enabled: true,
		Health: 1, EncryptedProxyURL: encryptedProxy, UserAgent: submissionTestAgent,
	}}, cipher)
}

func TestVideoSubmissionProxyExcludesPreparation(t *testing.T) {
	for _, model := range []string{"grok-imagine-video", "grok-imagine-video-1.5", "extension"} {
		for _, bound := range []uint64{0, 1} {
			t.Run(fmt.Sprintf("%s/bound=%d", model, bound), func(t *testing.T) {
				var uploaded, submitted, posts atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					submission := r.URL.Path == "/rest/app-chat/conversations/new"
					if (r.Header.Get("User-Agent") == submissionTestAgent) != submission {
						t.Errorf("wrong route for %s: user-agent=%q", r.URL.Path, r.Header.Get("User-Agent"))
					}
					switch r.URL.Path {
					case "/http/upload-file-v2/direct":
						uploaded.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						_, _ = io.WriteString(w, `{"fileMetadata":{"fileMetadataId":"reference-id","fileUri":"users/test/reference"}}`)
					case "/rest/media/post/create":
						posts.Add(1)
						_, _ = io.WriteString(w, `{"post":{"id":"reference-post"}}`)
					case "/rest/app-chat/conversations/new":
						submitted.Add(1)
						_, _ = io.WriteString(w, `{"result":{"response":{"streamingVideoGenerationResponse":{"progress":100,"videoUrl":"users/test/result.mp4"}}}}`)
					default:
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				cipher, err := security.NewCipher("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
				if err != nil {
					t.Fatal(err)
				}
				token, err := cipher.Encrypt("test-sso")
				if err != nil {
					t.Fatal(err)
				}
				manager := newSubmissionTestManager(t, cipher, server.URL)
				adapter := NewAdapter(Config{BaseURL: server.URL, StatsigMode: "manual", StatsigManualValue: "test", VideoTimeoutSeconds: 5}, manager, cipher, nil, nil)
				request := provider.VideoRequest{
					Credential: account.Credential{ID: 42, Provider: account.ProviderWeb, WebTier: account.WebTierSuper, EncryptedAccessToken: token, EgressNodeID: bound},
					Model:      model, Prompt: "test", Duration: 6,
					ImageURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
				}
				if model == "extension" {
					request.Model = "grok-imagine-video-1.5"
					request.Operation = provider.VideoOperationExtend
					request.VideoExtensionStartTime = 1
					request.VideoURL = "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(append([]byte{0, 0, 0, 24}, []byte("ftypisom0000")...))
				}
				ctx, trace := infraegress.WithTrace(context.Background())
				result, err := adapter.GenerateVideo(ctx, request)
				if err != nil || result.URL != "https://assets.grok.com/users/test/result.mp4" {
					t.Fatalf("result=%#v err=%v", result, err)
				}
				if uploaded.Load() != 1 || submitted.Load() != 1 || posts.Load() != 1 {
					t.Fatalf("uploads=%d submissions=%d posts=%d", uploaded.Load(), submitted.Load(), posts.Load())
				}
				if selection, ok := trace.Selection(egressdomain.ScopeWebSubmit); !ok || selection.NodeID != 1 || !selection.Proxied {
					t.Fatalf("submission trace=%#v present=%t", selection, ok)
				}
			})
		}
	}
}
