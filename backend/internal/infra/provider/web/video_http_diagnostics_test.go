package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	fhttptrace "github.com/bogdanfinn/fhttp/httptrace"
	egressdomain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/media"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

func TestVideoHTTPReportsWaitingBeforeServerResponds(t *testing.T) {
	for _, browser := range []bool{false, true} {
		t.Run(map[bool]string{false: "signer-net-http", true: "grok-browser-http"}[browser], func(t *testing.T) {
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				<-release
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, "ok")
			}))
			defer server.Close()
			defer unblock()
			adapter, credential := testMediaAdapter(t, server.URL)
			lease, err := adapter.egress.AcquireCredential(context.Background(), egressdomain.ScopeWeb, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			client := server.Client()
			do := client.Do
			if browser {
				do = lease.Do
			}
			observed := make(chan media.VideoEvent, 32)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = provider.WithVideoEventReporter(ctx, func(event media.VideoEvent) { observed <- event })
			ctx = withVideoRequest(ctx, "video_submit", lease)
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, bytes.NewBufferString("private-input"))
			done := make(chan error, 1)
			go func() {
				response, err := doVideoHTTP(request, do)
				if response != nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				done <- err
				close(observed)
			}()
			var events []media.VideoEvent
			for {
				select {
				case event, ok := <-observed:
					if !ok {
						t.Fatal("request ended before reporting wait for headers")
					}
					events = append(events, event)
					if event.Stage == "http_wait_headers" {
						if event.Request != "video_submit" || event.DeadlineAt == nil || event.EgressMode != "direct" {
							t.Fatalf("waiting event = %#v", event)
						}
						unblock()
						goto waitingObserved
					}
				case <-ctx.Done():
					t.Fatal("missing live wait-for-headers stage")
				}
			}
		waitingObserved:
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			for event := range observed {
				events = append(events, event)
			}
			last := events[len(events)-1]
			if last.Stage != "http_headers" || last.HTTPStatus != 202 {
				t.Fatalf("headers = %#v", last)
			}
			for i := 1; i < len(events); i++ {
				if events[i].StartedAt.Before(events[i-1].StartedAt) {
					t.Fatal("transport timestamps are out of order")
				}
			}
		})
	}
}

func TestVideoHTTPDoesNotRegressAfterEarlyResponse(t *testing.T) {
	var events []media.VideoEvent
	ctx := provider.WithVideoEventReporter(context.Background(), func(event media.VideoEvent) { events = append(events, event) })
	ctx = withVideoRequest(ctx, "video_submit", nil)
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://grok.example", nil)
	_, err := doVideoHTTP(request, func(r *http.Request) (*http.Response, error) {
		trace := fhttptrace.ContextClientTrace(r.Context())
		trace.GotConn(fhttptrace.GotConnInfo{})
		trace.GotFirstResponseByte()
		trace.WroteRequest(fhttptrace.WroteRequestInfo{})
		return &http.Response{StatusCode: 403, Body: http.NoBody}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Stage == "http_wait_headers" {
			t.Fatal("late write callback regressed the response phase")
		}
	}
}

func TestVideoURLStatsigTimelineIncludesPageFallbackAndSigning(t *testing.T) {
	signature := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 70))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			w.WriteHeader(404)
			_, _ = io.WriteString(w, "<html></html>")
		case "/":
			_, _ = io.WriteString(w, `<meta name="grok-site-verification" content="metadata">`)
		case "/sign":
			_ = json.NewEncoder(w).Encode(map[string]string{"x-statsig-id": signature})
		case "/rest/app-chat/conversations/new":
			if r.Header.Get("x-statsig-id") != signature {
				t.Error("signature was not forwarded")
			}
			_, _ = io.WriteString(w, `{"result":{"response":{"streamingVideoGenerationResponse":{"progress":100,"videoUrl":"users/test/result.mp4"}}}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapter, credential := testMediaAdapter(t, server.URL)
	adapter.cfg.StatsigMode, adapter.cfg.StatsigSignerURL = "url", server.URL+"/sign"
	adapter.statsig.validateEndpoint = func(context.Context, string) error { return nil }
	var events []media.VideoEvent
	ctx := provider.WithVideoEventReporter(context.Background(), func(event media.VideoEvent) { events = append(events, event) })
	_, err := adapter.GenerateVideo(ctx, provider.VideoRequest{Credential: credential, Model: "grok-imagine-video-1.5", Prompt: "private-prompt", Duration: 6})
	if err != nil {
		t.Fatal(err)
	}
	var stages []string
	for _, event := range events {
		stages = append(stages, event.Stage)
	}
	for _, stage := range []string{"acquire_submit_egress", "submit_egress_ready", "statsig_wait", "statsig_meta_index", "statsig_meta_root", "statsig_sign_url", "statsig_ready", "http_wait_headers", "wait_generation"} {
		if !slices.Contains(stages, stage) {
			t.Fatalf("missing %s in %v", stage, stages)
		}
	}
	for _, operation := range []string{"statsig_meta_index", "statsig_meta_root", "statsig_sign_url", "video_submit"} {
		if !slices.ContainsFunc(events, func(event media.VideoEvent) bool {
			return event.Request == operation && event.Stage == "http_headers" && event.HTTPStatus > 0
		}) {
			t.Fatalf("missing response status for %s", operation)
		}
	}
}

func TestVideoHTTPTimeoutKeepsTheLastObservedPhase(t *testing.T) {
	var events []media.VideoEvent
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = provider.WithVideoEventReporter(ctx, func(event media.VideoEvent) {
		events = append(events, event)
		if event.Stage == "http_wait_headers" {
			cancel()
		}
	})
	ctx = withVideoRequest(ctx, "video_submit", nil)
	request, _ := http.NewRequestWithContext(ctx, "POST", "https://grok.example", nil)
	_, err := doVideoHTTP(request, func(r *http.Request) (*http.Response, error) {
		trace := fhttptrace.ContextClientTrace(r.Context())
		trace.GotConn(fhttptrace.GotConnInfo{})
		trace.WroteRequest(fhttptrace.WroteRequestInfo{})
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	if !errors.Is(err, context.Canceled) || len(events) == 0 || events[len(events)-1].Stage != "http_wait_headers" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}
