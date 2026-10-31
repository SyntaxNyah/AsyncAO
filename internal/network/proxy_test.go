package network

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy/netproxytest"
)

const proxiedAddr = "asyncao-test.invalid:80"

func proxiedTarget(t *testing.T, body string) (*netproxy.Factory, *netproxytest.Proxy) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	proxy := netproxytest.Start(t, strings.TrimPrefix(srv.URL, "http://"))
	sockets := netproxy.New(nil, func(string) string { return "" })
	sockets.Apply(netproxy.ModeManual, proxy.URL)
	return sockets, proxy
}

func TestAssetsGoThroughTheProxy(t *testing.T) {
	const body = "sprite"
	sockets, proxy := proxiedTarget(t, body)
	got, err := NewClientNotFoundTTL(0, sockets).Fetch(context.Background(), "http://"+proxiedAddr+"/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body = %q", got)
	}
	if !slices.Equal(proxy.Asked(), []string{proxiedAddr}) {
		t.Errorf("proxy was asked for %v", proxy.Asked())
	}
}

func TestServerListGoesThroughTheProxy(t *testing.T) {
	sockets, proxy := proxiedTarget(t, "[]")
	if _, err := FetchServerList(context.Background(), sockets, "http://"+proxiedAddr+"/servers"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(proxy.Asked(), []string{proxiedAddr}) {
		t.Errorf("proxy was asked for %v", proxy.Asked())
	}
}

// Tunnelling must not cost the asset client its HTTP/2: a room's sprites share one connection.
func TestAssetsKeepHTTP2ThroughTheProxy(t *testing.T) {
	const (
		requests   = 8
		http2Major = 2
		asset      = "https://asyncao-test.invalid:443/a"
	)
	var sawProto atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawProto.Store(int64(r.ProtoMajor))
		_, _ = io.WriteString(w, "sprite")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	proxy := netproxytest.Start(t, strings.TrimPrefix(srv.URL, "https://"))
	sockets := netproxy.New(nil, func(string) string { return "" })
	sockets.Apply(netproxy.ModeManual, proxy.URL)
	c := NewClientNotFoundTTL(0, sockets)
	transport, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", c.httpClient.Transport)
	}
	transport.TLSClientConfig.InsecureSkipVerify = true // the test server's certificate is self-signed

	for i := range requests {
		if _, err := c.Fetch(context.Background(), asset+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if sawProto.Load() != http2Major {
		t.Errorf("server saw HTTP/%d", sawProto.Load())
	}
	if got := len(proxy.Asked()); got != 1 {
		t.Errorf("used %d tunnels, want 1", got)
	}
}

// A direct ping would show the user's address to every server in the list.
func TestLatencyProbeGoesThroughTheProxy(t *testing.T) {
	sockets, proxy := proxiedTarget(t, "")
	if _, err := ProbeLatency(context.Background(), sockets, proxiedAddr); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(proxy.Asked(), []string{proxiedAddr}) {
		t.Errorf("proxy was asked for %v", proxy.Asked())
	}
}

// Resolving a proxied name locally would publish it to the machine's resolver.
func TestPreResolveSkipsProxiedHosts(t *testing.T) {
	const host = "asyncao-test.invalid"
	sockets, _ := proxiedTarget(t, "")
	c := NewClientNotFoundTTL(0, sockets)
	c.PreResolve(context.Background(), host)
	if _, _, found := c.dns.lookup(host); found {
		t.Error("a proxied host was looked up locally")
	}
}

func TestPreResolveWarmsDirectHosts(t *testing.T) {
	const host = "localhost"
	c := NewClientNotFoundTTL(0, netproxy.Direct())
	c.PreResolve(context.Background(), host)
	if _, _, found := c.dns.lookup(host); !found {
		t.Skip("this machine can't resolve localhost")
	}
}
