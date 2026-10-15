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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	egressdomain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/media"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
	"github.com/chenyme/grok2api/backend/internal/infra/security"
)

const submissionTestAgent = "Mozilla/5.0 submission-test"

func newSubmissionTestManager(t *testing.T, cipher *security.Cipher, upstreamURL string, connections ...*sync.Map) *infraegress.Manager {
	t.Helper()
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		t.Fatal(err)
	}
	var proxied atomic.Int32
	forward := httputil.NewSingleHostReverseProxy(upstream)
	director := forward.Director
	forward.Director = func(r *http.Request) {
		director(r)
		r.Header.Set("X-Test-Proxy", "true")
	}
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
		for _, connected := range connections {
			connected.Store(remote.LocalAddr().String(), true)
		}
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

func TestVideoReferenceUploadProxyRouting(t *testing.T) {
	for _, proxyUploads := range []bool{false, true} {
		for _, model := range []string{"grok-imagine-video", "grok-imagine-video-1.5", "references", "extension"} {
			for _, bound := range []uint64{0, 1} {
				t.Run(fmt.Sprintf("%s/bound=%d/proxyUploads=%t", model, bound, proxyUploads), func(t *testing.T) {
					var uploaded, submitted, posts atomic.Int32
					var connections sync.Map
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						submission := r.URL.Path == "/rest/app-chat/conversations/new"
						wantProxy := submission || (proxyUploads && model != "extension" && r.URL.Path == "/http/upload-file-v2/direct")
						_, tunneled := connections.Load(r.RemoteAddr)
						if (tunneled || r.Header.Get("X-Test-Proxy") == "true") != wantProxy {
							t.Errorf("wrong proxy route for %s", r.URL.Path)
						}
						if (r.Header.Get("User-Agent") == submissionTestAgent) != wantProxy {
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
					manager := newSubmissionTestManager(t, cipher, server.URL, &connections)
					adapter := NewAdapter(Config{BaseURL: server.URL, StatsigMode: "manual", StatsigManualValue: "test", VideoTimeoutSeconds: 5}, manager, cipher, nil, nil)
					liveConfig := adapter.config()
					liveConfig.ReferenceImageUploadProxy = proxyUploads
					adapter.UpdateConfig(liveConfig)
					request := provider.VideoRequest{
						Credential: account.Credential{ID: 42, Provider: account.ProviderWeb, WebTier: account.WebTierSuper, EncryptedAccessToken: token, EgressNodeID: bound},
						Model:      model, Prompt: "test", Duration: 6,
						ImageURL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
					}
					wantUploads := int32(1)
					if model == "references" {
						request.Model = "grok-imagine-video-1.5"
						request.ReferenceURLs = []string{request.ImageURL, request.ImageURL}
						request.ImageURL = ""
						wantUploads = 2
					}
					if model == "extension" {
						request.Model = "grok-imagine-video-1.5"
						request.Operation = provider.VideoOperationExtend
						request.VideoExtensionStartTime = 1
						request.VideoURL = "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(append([]byte{0, 0, 0, 24}, []byte("ftypisom0000")...))
					}
					ctx, trace := infraegress.WithTrace(context.Background())
					ctx = provider.WithVideoEventReporter(ctx, func(event media.VideoEvent) {
						if event.Stage == "upload_image" {
							mode := "direct"
							if proxyUploads {
								mode = "proxy"
							}
							if event.EgressMode != mode || event.Request != "video_reference_upload" {
								t.Errorf("upload diagnostic route=%+v; want %s", event, mode)
							}
						}
					})
					result, err := adapter.GenerateVideo(ctx, request)
					if err != nil || result.URL != "https://assets.grok.com/users/test/result.mp4" {
						t.Fatalf("result=%#v err=%v", result, err)
					}
					if uploaded.Load() != wantUploads || submitted.Load() != 1 || posts.Load() != wantUploads {
						t.Fatalf("uploads=%d submissions=%d posts=%d", uploaded.Load(), submitted.Load(), posts.Load())
					}
					if selection, ok := trace.Selection(egressdomain.ScopeWebSubmit); !ok || selection.NodeID != 1 || !selection.Proxied {
						t.Fatalf("submission trace=%#v present=%t", selection, ok)
					}
				})
			}
		}
	}
}
