package egress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BrowserProxy keeps proxy passwords out of Chromium command-line arguments.
// Its loopback CONNECT listener uses the same proxy dialects as Build transport.
func BrowserProxy(ctx context.Context, proxyURL string) (string, func(), error) {
	client, err := newBuildClient(proxyURL, 20*time.Second)
	if err != nil {
		return "", nil, err
	}
	transport := client.Transport.(*http.Transport)
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "", nil, err
	}
	dial := func(ctx context.Context, target string) (net.Conn, error) {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return transport.DialContext(ctx, "tcp", target)
		}
		address := parsed.Host
		if parsed.Port() == "" {
			port := "80"
			if parsed.Scheme == "https" {
				port = "443"
			}
			address = net.JoinHostPort(parsed.Hostname(), port)
		}
		var conn net.Conn
		var err error
		d := &net.Dialer{Timeout: 15 * time.Second}
		if parsed.Scheme == "https" {
			conn, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", address)
		} else {
			conn, err = d.DialContext(ctx, "tcp", address)
		}
		if err != nil {
			return nil, err
		}
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(parsed.User.Username()+":"+password)))
		}
		if err = req.Write(conn); err != nil {
			conn.Close()
			return nil, err
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			conn.Close()
			return nil, err
		}
		if response.StatusCode != 200 {
			conn.Close()
			return nil, errors.New("browser upstream proxy refused CONNECT")
		}
		_ = conn.SetDeadline(time.Time{})
		return conn, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		allowed := host == "grok.com" || strings.HasSuffix(host, ".grok.com") || host == "x.ai" || strings.HasSuffix(host, ".x.ai") || host == "cloudflare.com" || strings.HasSuffix(host, ".cloudflare.com")
		if r.Method != http.MethodConnect || err != nil || port != "443" || !allowed {
			http.Error(w, "blocked", 403)
			return
		}
		remote, err := dial(ctx, r.Host)
		if err != nil {
			http.Error(w, "proxy unavailable", 502)
			return
		}
		local, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			return
		}
		deadline := time.Now().Add(2 * time.Minute)
		_ = remote.SetDeadline(deadline)
		_ = local.SetDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { remote.Close(); local.Close() })
		defer stop()
		defer local.Close()
		defer remote.Close()
		_, _ = io.WriteString(local, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() { _, _ = io.Copy(remote, local); remote.Close(); close(done) }()
		_, _ = io.Copy(local, remote)
		local.Close()
		<-done
	})}
	go func() { _ = server.Serve(listener) }()
	stop := func() { server.Close(); transport.CloseIdleConnections() }
	return "http://" + listener.Addr().String(), stop, nil
}
