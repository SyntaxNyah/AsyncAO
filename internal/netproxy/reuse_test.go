package netproxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy/netproxytest"
)

const http2Major = 2

func http2Target(t *testing.T) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, testBody)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://")
}

// A custom TLS dial or a replaced TLS config would silently drop these, costing every asset a connection.
func TestHTTPSThroughATunnelMultiplexesOverHTTP2(t *testing.T) {
	const concurrentRequests = 20
	proxy := netproxytest.Start(t, http2Target(t))
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)
	client := f.InsecureClient()
	address := "https://" + unreachable + "/"

	get := func() *http.Response {
		resp, err := client.Get(address)
		if err != nil {
			t.Error(err)
			return nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	// One request first, as simultaneous first requests can each open a connection before HTTP/2 is known.
	first := get()
	if first == nil || first.ProtoMajor != http2Major {
		t.Fatalf("first response = %+v, want HTTP/2", first)
	}
	var wg sync.WaitGroup
	for range concurrentRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			get()
		}()
	}
	wg.Wait()
	if got := len(proxy.Asked()); got != 1 {
		t.Errorf("used %d tunnels, want 1", got)
	}
}

func TestReconnectingResumesTheTLSSession(t *testing.T) {
	proxy := netproxytest.Start(t, http2Target(t))
	f := netproxy.New(noSystem, noEnv)
	f.Apply(netproxy.ModeManual, proxy.URL)
	client := f.InsecureClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", client.Transport)
	}
	address := "https://" + unreachable + "/"
	get := func() *http.Response {
		t.Helper()
		resp, err := client.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp
	}
	if get().TLS.DidResume {
		t.Error("the first connection can't have resumed")
	}
	transport.CloseIdleConnections()
	if !get().TLS.DidResume {
		t.Error("the second connection did a full handshake")
	}
	const wantTunnels = 2
	if got := len(proxy.Asked()); got != wantTunnels {
		t.Errorf("used %d tunnels, want %d", got, wantTunnels)
	}
}
