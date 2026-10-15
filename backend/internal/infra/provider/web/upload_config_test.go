package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	egressdomain "github.com/chenyme/grok2api/backend/internal/domain/egress"
	infraegress "github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chenyme/grok2api/backend/internal/infra/provider"
)

func TestUploadTimeoutReadsLiveConfigAndIncludesResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"fileMetadata":`)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
		_, _ = io.WriteString(w, `{"fileMetadataId":"reference-id","fileUri":"users/test/reference"}}`)
	}))
	defer server.Close()
	manager := infraegress.NewManager(egressRepositoryStub{}, nil)
	adapter := NewAdapter(Config{BaseURL: server.URL}, manager, nil, nil, nil)
	staleConfig := adapter.config()
	if staleConfig.UploadTimeout != time.Minute || staleConfig.ReferenceImageUploadProxy {
		t.Fatal("upload defaults changed")
	}
	lease, err := manager.Acquire(context.Background(), egressdomain.ScopeWeb, "")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	file := provider.ImageInput{Data: []byte("test"), MIMEType: "image/png", Filename: "reference.png"}
	for _, timeout := range []time.Duration{40 * time.Millisecond, time.Second} {
		liveConfig := staleConfig
		liveConfig.UploadTimeout = timeout
		adapter.UpdateConfig(liveConfig)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, err := adapter.uploadFileV2Direct(ctx, staleConfig, lease, "test-sso", file, server.URL, imagineSelfUploadSource, "video_reference_upload")
		cancel()
		if timeout < 150*time.Millisecond {
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "V2 上传文件响应无效") {
				t.Fatalf("expected response body deadline, got %v", err)
			}
		} else if err != nil {
			t.Fatalf("updated timeout not applied: %v", err)
		}
	}
}

func TestReferenceImageUploadProxyCannotSilentlyUseDirect(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	manager := infraegress.NewManager(egressRepositoryStub{}, nil)
	adapter := NewAdapter(Config{BaseURL: server.URL, ReferenceImageUploadProxy: true}, manager, nil, nil, nil)
	lease, err := manager.Acquire(context.Background(), egressdomain.ScopeWeb, "")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	_, err = adapter.uploadVideoReferenceImage(context.Background(), adapter.config(), lease, account.Credential{}, "test-sso", provider.ImageInput{})
	if err == nil || !strings.Contains(err.Error(), "没有可用代理节点") || requests.Load() != 0 {
		t.Fatalf("missing proxy must fail before upload: err=%v requests=%d", err, requests.Load())
	}
}
