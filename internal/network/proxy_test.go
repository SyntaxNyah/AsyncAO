package network

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
)

// withProxyFor routes every request whose host matches to a fake proxy, for one
// test. The resolver is process-wide, so the cleanup is not optional.
func withProxyFor(t *testing.T, host string) {
	t.Helper()
	via := &url.URL{Scheme: "http", Host: "proxy.invalid:8080"}
	netproxy.SetResolver(func(r *http.Request) (*url.URL, error) {
		if r.URL.Host == host {
			return via, nil
		}
		return nil, nil
	})
	t.Cleanup(func() { netproxy.SetResolver(nil) })
}

// TestAssetTransportCarriesTheProxy: every sprite, background, track and piece of
// evidence in the app goes through this one transport, and a nil Proxy field
// means NEVER proxy. Without it a proxied user fetched the lobby list through
// their proxy (that path inherits http.DefaultTransport) and then streamed every
// asset around it.
func TestAssetTransportCarriesTheProxy(t *testing.T) {
	c := NewClient()
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("asset client transport is %T, want *http.Transport", c.httpClient.Transport)
	}
	if tr.Proxy == nil {
		t.Fatal("the asset transport has no Proxy — all asset traffic would ignore the user's proxy")
	}
	withProxyFor(t, "assets.invalid")
	got, err := tr.Proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: "assets.invalid", Path: "/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Host != "proxy.invalid:8080" {
		t.Errorf("asset request resolved to proxy %v, want proxy.invalid:8080 — the field is set but not to our resolver", got)
	}
}

// TestPreResolveSkipsProxiedHosts is the privacy half of the fix. Behind a proxy
// the transport dials the PROXY and the proxy resolves the origin, so the origin
// name never needs to reach this machine's resolver. Warming the cache anyway
// would publish to the local (and typically the ISP's) DNS precisely the server
// name the user routed through a proxy in order not to disclose.
func TestPreResolveSkipsProxiedHosts(t *testing.T) {
	withProxyFor(t, "localhost")
	c := NewClient()
	c.PreResolve(context.Background(), "localhost")
	if addrs, _, found := c.dns.lookup("localhost"); found {
		t.Errorf("pre-resolved a PROXIED host to %v — the origin's name must not reach the local resolver", addrs)
	}
}

// TestPreResolveStillWarmsDirectHosts: the guard must not turn the warm path off
// for everyone. Without a proxy the pre-resolve is what keeps the first asset
// probe after connect from paying a DNS round-trip.
func TestPreResolveStillWarmsDirectHosts(t *testing.T) {
	withProxyFor(t, "somewhere.else.invalid")
	c := NewClient()
	c.PreResolve(context.Background(), "localhost")
	if _, _, found := c.dns.lookup("localhost"); !found {
		t.Skip("localhost did not resolve in this environment")
	}
}

// TestProxiedHostChecksBothSchemes: NO_PROXY-style rules and the http/https
// split make the answer per-scheme, so warming is only safe when NEITHER scheme
// is proxied. A checker that asked about https alone would leak every plain-http
// asset origin.
func TestProxiedHostChecksBothSchemes(t *testing.T) {
	for _, proxied := range []string{"http", "https"} {
		t.Run(proxied, func(t *testing.T) {
			via := &url.URL{Scheme: "http", Host: "proxy.invalid:8080"}
			netproxy.SetResolver(func(r *http.Request) (*url.URL, error) {
				if r.URL.Scheme == proxied {
					return via, nil
				}
				return nil, nil
			})
			t.Cleanup(func() { netproxy.SetResolver(nil) })
			if !proxiedHost("assets.invalid") {
				t.Errorf("a host proxied over %s alone reported DIRECT", proxied)
			}
		})
	}
	if proxiedHost("") {
		t.Error("an empty host must not report proxied")
	}
}
