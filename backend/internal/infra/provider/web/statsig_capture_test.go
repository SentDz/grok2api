package web

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestChromiumCapturesSamePageSeedAndHEX(t *testing.T) {
	executable := os.Getenv("STATSIG_TEST_CHROMIUM")
	if executable == "" {
		t.Skip("set STATSIG_TEST_CHROMIUM for browser integration test")
	}
	data, err := os.ReadFile("testdata/statsig_live_pair.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample statsig.Sample
	if err = json.Unmarshal(data, &sample); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><meta name="grok-site-verification" content="%s"></head><body>`, sample.Seed)
		for _, path := range sample.Paths {
			fmt.Fprintf(w, `<svg><path d="%s"/></svg>`, path)
		}
		fmt.Fprintf(w, `<script>crypto.subtle.digest('SHA-256',new TextEncoder().encode('POST!/rest/modes!123obfiowerehiring%s'));</script></body></html>`, sample.HEX)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.ExecPath(executable))
	allocator, closeAllocator := chromedp.NewExecAllocator(ctx, opts...)
	defer closeAllocator()
	browser, closeBrowser := chromedp.NewContext(allocator)
	defer closeBrowser()
	err = chromedp.Run(browser, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(statsigCaptureHook).Do(ctx)
		return err
	}))
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureStatsigSamples(browser, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Samples) != 2 {
		t.Fatal("missing captures")
	}
	for _, got := range captured.Samples {
		if got.Seed != sample.Seed || got.HEX != sample.HEX || strings.Join(got.Paths, "\n") != strings.Join(sample.Paths, "\n") {
			t.Fatal("capture mixed page material")
		}
	}
}
