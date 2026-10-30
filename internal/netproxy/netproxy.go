// Package netproxy picks the proxy for a request; every http.Transport must install Proxy, or it fails silently.
package netproxy

import (
	"net/http"
	"net/url"
	"sync/atomic"
)

// resolver is the installed resolver, nil for the environment default; atomic because Proxy must not lock
// on the transport goroutines.
var resolver atomic.Pointer[ProxyFunc]

// ProxyFunc is net/http's Transport.Proxy shape: the proxy URL, nil for direct, or an error to fail the request.
type ProxyFunc func(*http.Request) (*url.URL, error)

// SetResolver installs the process-wide resolver, as threading it through constructors would miss a transport.
func SetResolver(f ProxyFunc) {
	if f == nil {
		resolver.Store(nil)
		return
	}
	resolver.Store(&f)
}

// Proxy is the value every Transport.Proxy must hold; http.ProxyFromEnvironment memoises behind a sync.Once.
func Proxy(req *http.Request) (*url.URL, error) {
	if f := resolver.Load(); f != nil {
		return (*f)(req)
	}
	return http.ProxyFromEnvironment(req)
}

// ForURL reports the proxy for u, or nil for direct. DNS pre-resolution needs it: resolving a proxied origin
// locally would publish the name to the machine's resolver.
func ForURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	// Resolvers read only req.URL, so a bare request is enough.
	p, err := Proxy(&http.Request{URL: u})
	if err != nil {
		// An unresolvable config is no licence to go direct: report one so the caller stays cautious.
		return &url.URL{}
	}
	return p
}

// Transport clones http.DefaultTransport; a hand-built one would silently drop Proxy and the stdlib's timeouts.
func Transport() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok { // only reachable if something replaced http.DefaultTransport
		return &http.Transport{Proxy: Proxy}
	}
	c := t.Clone()
	c.Proxy = Proxy
	return c
}
