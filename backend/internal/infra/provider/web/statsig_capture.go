package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/account"
	domainegress "github.com/chenyme/grok2api/backend/internal/domain/egress"
	"github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"github.com/chenyme/grok2api/backend/internal/infra/egress"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const statsigCaptureHook = `(() => {
  window.__statsigCapture = null;
  const digest = crypto.subtle.digest.bind(crypto.subtle);
  crypto.subtle.digest = function(algorithm, data) {
    try {
      const text = new TextDecoder().decode(data);
      const salt = "obfiowerehiring";
      const position = text.indexOf(salt);
      if (position >= 0) {
        const meta = [...document.querySelectorAll("meta")].find(m => (m.name || "").replace(/[\u2010-\u2015]/g, "-") === "grok-site-verification");
        if (meta) window.__statsigCapture = {seed: meta.content, hex: text.slice(position + salt.length)};
      }
    } catch (_) {}
    return digest(algorithm, data);
  };
})();`

func (a *Adapter) CaptureStatsig(ctx context.Context, credential account.Credential) (statsig.Capture, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	token, err := a.cipher.Decrypt(credential.EncryptedAccessToken)
	if err != nil {
		return statsig.Capture{}, errors.New("cannot decrypt capture account")
	}
	lease, err := a.egress.AcquireCredential(ctx, domainegress.ScopeWeb, credential)
	if err != nil {
		return statsig.Capture{}, errors.New("cannot acquire capture account egress")
	}
	defer lease.Release()
	proxy, stop, err := egress.BrowserProxy(ctx, lease.ProxyURL)
	if err != nil {
		return statsig.Capture{}, errors.New("cannot configure browser egress")
	}
	defer stop()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.ProxyServer(proxy), chromedp.Flag("disable-quic", true), chromedp.Flag("disable-blink-features", "AutomationControlled"), chromedp.UserAgent(lease.UserAgent))
	if os.Getenv("GROK2API_CHROMIUM_NO_SANDBOX") == "true" {
		opts = append(opts, chromedp.NoSandbox)
	}
	opts = append(opts, chromedp.ModifyCmdFunc(func(cmd *exec.Cmd) {
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir(), "LANG=C.UTF-8"}
	}))
	allocator, stopAllocator := chromedp.NewExecAllocator(ctx, opts...)
	defer stopAllocator()
	browser, stopBrowser := chromedp.NewContext(allocator)
	defer stopBrowser()
	if err = chromedp.Run(browser, network.Enable(), chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(statsigCaptureHook).Do(ctx)
		if err != nil {
			return err
		}
		header := http.Header{}
		header.Set("Cookie", egress.BuildSSOCookie(token, lease.CFCookies))
		for _, cookie := range (&http.Request{Header: header}).Cookies() {
			if err := network.SetCookie(cookie.Name, cookie.Value).WithDomain("grok.com").WithPath("/").WithSecure(true).WithHTTPOnly(true).Do(ctx); err != nil {
				return err
			}
		}
		return nil
	})); err != nil {
		return statsig.Capture{}, errors.New("Chromium startup failed; check browser installation and sandbox support")
	}
	return captureStatsigSamples(browser, "https://grok.com/imagine")
}

func captureStatsigSamples(browser context.Context, target string) (statsig.Capture, error) {
	var err error
	var result statsig.Capture
	for iteration := 0; iteration < 2; iteration++ {
		var sample statsig.Sample
		var html string
		err = chromedp.Run(browser, chromedp.Navigate(target), chromedp.Poll(`!!window.__statsigCapture`, nil, chromedp.WithPollingInterval(time.Second), chromedp.WithPollingTimeout(20*time.Second)))
		if err != nil {
			// Trigger a non-media webpage request so the official signer executes.
			err = chromedp.Run(browser, chromedp.Evaluate(`fetch('/rest/modes',{method:'POST',headers:{'content-type':'application/json'},body:'{}'}).catch(()=>{});`, nil), chromedp.Poll(`!!window.__statsigCapture`, nil, chromedp.WithPollingTimeout(15*time.Second)))
		}
		if err != nil {
			return statsig.Capture{}, errors.New("browser did not capture a signature; check login, egress or Cloudflare challenge")
		}
		if err = chromedp.Run(browser, chromedp.Evaluate(`window.__statsigCapture`, &sample), chromedp.OuterHTML("html", &html)); err != nil {
			return statsig.Capture{}, errors.New("cannot read browser signature sample")
		}
		if len(html) > 4<<20 {
			return statsig.Capture{}, errors.New("capture page exceeds size limit")
		}
		sample.Paths = collectStatsigSVGPaths(html, nil)
		if len(sample.Paths) != 4 || len(sample.HEX) == 0 || len(sample.HEX) > 4096 {
			return statsig.Capture{}, errors.New("incomplete browser signature material")
		}
		seed, e := decodeStatsigSeed(sample.Seed)
		if e != nil || len(seed) != 48 {
			return statsig.Capture{}, errors.New("invalid browser signature seed")
		}
		result.Samples = append(result.Samples, sample)
		if iteration == 0 {
			urls := extractStatsigScriptURLs(html)
			if len(urls) > 32 {
				urls = urls[:32]
			}
			encoded, _ := json.Marshal(urls)
			script := `(async()=>{const found=[];for(const u of ` + string(encoded) + `){try{const r=await fetch(u);const t=await r.text();if(t.includes('obfiowerehiring') || (t.includes('animate') && t.includes('4096')))found.push(t.slice(0,64000));if(found.length>=3)break;}catch(_){}}return found.join('\n').slice(0,128000)})()`
			_ = chromedp.Run(browser, chromedp.Evaluate(script, &result.Scripts, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
			fingerprint := sha256.Sum256([]byte(strings.Join(sample.Paths, "\n") + result.Scripts))
			result.Fingerprint = hex.EncodeToString(fingerprint[:])
		}
	}
	return result, nil
}

func SignBuiltinSample(sample statsig.Sample, method, path string, now int64) (string, error) {
	seed, err := decodeStatsigSeed(sample.Seed)
	if err != nil {
		return "", err
	}
	return buildLocalStatsig(seed, sample.HEX, method, path, now)
}

func (a *Adapter) VerifyStatsig(ctx context.Context, credential account.Credential, sample statsig.Sample) error {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	token, err := a.cipher.Decrypt(credential.EncryptedAccessToken)
	if err != nil {
		return errors.New("cannot decrypt verification account")
	}
	lease, err := a.egress.AcquireCredential(ctx, domainegress.ScopeWeb, credential)
	if err != nil {
		return errors.New("cannot acquire verification egress")
	}
	defer lease.Release()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config().BaseURL+"/rest/rate-limits", strings.NewReader(`{"requestKind":"DEFAULT","modelName":"grok-4"}`))
	if err != nil {
		return err
	}
	request.Header = buildHeaders(token, lease, "application/json")
	applyAppHeaders(request.Header, a.config().BaseURL, a.config().BaseURL+"/")
	value, err := SignBuiltinSample(sample, http.MethodPost, "/rest/rate-limits", time.Now().Unix())
	if err != nil {
		return err
	}
	request.Header.Set("x-statsig-id", value)
	response, err := lease.DoDeferredForbidden(request)
	if err != nil {
		return errors.New("signature verification network failure")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if readErr != nil || len(body) > 64<<10 {
		return errors.New("invalid signature verification response")
	}
	if response.StatusCode == http.StatusForbidden && isStatsigRefreshableMediaError(newWebMediaUpstreamError(response.StatusCode, body, false), body) {
		return statsig.ErrRejected
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("signature verification returned HTTP %d", response.StatusCode)
	}
	var quota struct {
		Total *int `json:"totalQueries"`
	}
	if json.Unmarshal(body, &quota) != nil || quota.Total == nil {
		return errors.New("signature verification did not return quota JSON")
	}
	return nil
}
