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
	sockets := netproxy.New(nil)
	sockets.Apply(netproxy.ModeManual, proxy.URL, "")
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
	// An http asset is forwarded, so it loads through a proxy that only allows CONNECT to 443.
	if !slices.Equal(proxy.Forwards(), []string{proxiedAddr}) {
		t.Errorf("proxy was asked to forward %v", proxy.Forwards())
	}
}

func TestServerListGoesThroughTheProxy(t *testing.T) {
	sockets, proxy := proxiedTarget(t, "[]")
	if _, err := FetchServerList(context.Background(), sockets, "http://"+proxiedAddr+"/servers"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(proxy.Forwards(), []string{proxiedAddr}) {
		t.Errorf("proxy was asked to forward %v", proxy.Forwards())
	}
}

// A client lives for the whole process, so it must follow a changed setting.
func TestAssetsFollowAChangedSetting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "sprite")
	}))
	defer srv.Close()
	target := strings.TrimPrefix(srv.URL, "http://")
	first, second := netproxytest.Start(t, target), netproxytest.Start(t, target)
	sockets := netproxy.New(nil)
	sockets.Apply(netproxy.ModeManual, first.URL, "")
	c := NewClientNotFoundTTL(0, sockets)
	if _, err := c.Fetch(context.Background(), "http://"+proxiedAddr+"/a.png"); err != nil {
		t.Fatal(err)
	}
	sockets.Apply(netproxy.ModeManual, second.URL, "")
	if _, err := c.Fetch(context.Background(), "http://"+proxiedAddr+"/b.png"); err != nil {
		t.Fatal(err)
	}
	if got := len(first.Forwards()); got != 1 {
		t.Errorf("the old proxy saw %d requests, want 1", got)
	}
	if got := len(second.Forwards()); got != 1 {
		t.Errorf("the new proxy saw %d requests, want 1", got)
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
	sockets := netproxy.New(nil)
	sockets.Apply(netproxy.ModeManual, proxy.URL, "")
	c := NewClientNotFoundTTL(0, sockets)
	c.tune = func(tr *http.Transport) {
		tuneAssetTransport(tr)
		tr.TLSClientConfig.InsecureSkipVerify = true // the test server's certificate is self-signed
	}

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

// The proxy resolves a proxied name.
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
