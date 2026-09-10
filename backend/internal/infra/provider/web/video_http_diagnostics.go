package web

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"

	fhttptrace "github.com/bogdanfinn/fhttp/httptrace"
	"github.com/chenyme/grok2api/backend/internal/domain/media"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

type videoRequestKey struct{}

// Request names are fixed operation labels, never URLs or request contents.
func withVideoRequest(ctx context.Context, name string, lease *infraegress.Lease) context.Context {
	event := media.VideoEvent{Request: name}
	if lease != nil {
		event.EgressNodeID, event.EgressNodeName = lease.NodeID, lease.NodeName
		event.EgressScope, event.EgressMode = string(lease.Scope), "direct"
		if lease.ProxyURL != "" {
			event.EgressMode = "proxy"
		}
	}
	return context.WithValue(ctx, videoRequestKey{}, event)
}

func videoRequestEvent(ctx context.Context, stage string, status int, err error) media.VideoEvent {
	event, _ := ctx.Value(videoRequestKey{}).(media.VideoEvent)
	event.Stage, event.HTTPStatus, event.StartedAt = stage, status, time.Now().UTC()
	if deadline, ok := ctx.Deadline(); ok {
		deadline = deadline.UTC()
		event.DeadlineAt = &deadline
	}
	if err != nil {
		event.Error = err.Error()
	}
	return event
}

func reportVideoRequest(ctx context.Context, stage string, status int, err error) {
	provider.ReportVideoEvent(ctx, videoRequestEvent(ctx, stage, status, err))
}

// HTTP trace callbacks run on transport goroutines. Queue their timestamps and
// persist only on the calling video worker, which owns the mutable job state.
func doVideoHTTP(request *http.Request, do func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	ctx := request.Context()
	if !provider.HasVideoEventReporter(ctx) {
		return do(request)
	}
	if event, _ := ctx.Value(videoRequestKey{}).(media.VideoEvent); event.Request == "" {
		return do(request)
	}
	events := make(chan media.VideoEvent, 128)
	var mu sync.Mutex
	phase := 0
	finished := false
	emit := func(stage string, rank int) {
		mu.Lock()
		defer mu.Unlock()
		if finished || (rank != 1 && rank < phase) {
			return
		}
		phase = rank
		select {
		case events <- videoRequestEvent(ctx, stage, 0, nil):
		default:
		}
	}
	gotConn := func() { emit("http_send", 2) }
	written := func(err error) {
		if err == nil {
			emit("http_wait_headers", 3)
		}
	}
	firstByte := func() { emit("http_first_byte", 4) }
	// The browser transport uses fhttp's context key; the URL signer uses net/http.
	traceCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn:              func(string) { emit("http_connect", 1) },
		GotConn:              func(httptrace.GotConnInfo) { gotConn() },
		WroteRequest:         func(info httptrace.WroteRequestInfo) { written(info.Err) },
		GotFirstResponseByte: firstByte,
	})
	traceCtx = fhttptrace.WithClientTrace(traceCtx, &fhttptrace.ClientTrace{
		GetConn:              func(string) { emit("http_connect", 1) },
		GotConn:              func(fhttptrace.GotConnInfo) { gotConn() },
		WroteRequest:         func(info fhttptrace.WroteRequestInfo) { written(info.Err) },
		GotFirstResponseByte: firstByte,
	})
	type result struct {
		response *http.Response
		err      error
		event    media.VideoEvent
	}
	done := make(chan result, 1)
	emit("http_connect", 1)
	go func() {
		response, err := do(request.WithContext(traceCtx))
		mu.Lock()
		finished = true
		mu.Unlock()
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		done <- result{response, err, videoRequestEvent(ctx, "http_headers", status, nil)}
	}()
	for {
		select {
		case event := <-events:
			provider.ReportVideoEvent(ctx, event)
		case value := <-done:
			for len(events) > 0 {
				provider.ReportVideoEvent(ctx, <-events)
			}
			if value.err == nil && value.response != nil {
				provider.ReportVideoEvent(ctx, value.event)
			}
			return value.response, value.err
		}
	}
}
