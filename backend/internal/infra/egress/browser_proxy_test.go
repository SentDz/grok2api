package egress

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestBrowserProxyAuthenticatedCONNECTAndDestinationRestriction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != "grok.com:443" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
			t.Error("invalid upstream CONNECT")
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_, _ = io.Copy(conn, conn)
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamURL.User = url.UserPassword("user", "pass")
	endpoint, closeProxy, err := BrowserProxy(ctx, upstreamURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxy()
	u, _ := url.Parse(endpoint)
	for _, target := range []string{"grok.com:443", "127.0.0.1:443", "example.com:443"} {
		conn, err := net.Dial("tcp", u.Host)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
		reader := bufio.NewReader(conn)
		response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if target != "grok.com:443" {
			if response.StatusCode != 403 {
				t.Error("untrusted destination accepted")
			}
			conn.Close()
			continue
		}
		if response.StatusCode != 200 {
			conn.Close()
			t.Fatal(response.StatusCode)
		}
		_, _ = io.WriteString(conn, "hello")
		buf := make([]byte, 5)
		_, err = io.ReadFull(reader, buf)
		conn.Close()
		if err != nil || string(buf) != "hello" {
			t.Fatal("tunnel failed", err)
		}
	}
}
