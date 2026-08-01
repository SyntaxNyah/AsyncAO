// Package netproxy is the single answer to "which proxy, if any, carries this
// request". Every outbound HTTP(S) transport in AsyncAO installs Proxy, and
// nothing constructs an http.Transport without it — a gate test enforces that,
// because the failure it prevents is silent.
//
// It has to be its own leaf package rather than a helper inside internal/network:
// internal/protocol (the game socket) has no internal dependencies at all, and
// making it import the asset-fetching layer to learn about proxies would be a
// worse trade than one dependency-free package both can share.
//
// WHY THIS EXISTS. A zero-value http.Transport has Proxy == nil, and a nil Proxy
// means NEVER proxy — it is not "use the default", it is an opt-out. AsyncAO had
// three hand-built transports and inherited http.DefaultTransport (which DOES set
// http.ProxyFromEnvironment) at six other call sites, so a user behind a proxy got
// a split tunnel: the lobby list and the updater went through it while the game
// socket and every single asset went around it. That is worse than either
// extreme, because the lobby looks healthy and nothing says why the room will not
// load.
//
// SCOPE, stated honestly. This package answers from the ENVIRONMENT today
// (HTTP_PROXY / HTTPS_PROXY / NO_PROXY, plus http, https and socks5 first hops,
// all of which net/http handles natively). Reading the operating system's own
// proxy configuration — the Windows WinHTTP/WinINET settings, macOS's
// SystemConfiguration, a PAC script — is a separate layer that plugs in through
// SetResolver, and is not here yet. SetResolver is the seam, not a placeholder:
// the OS reader has to do a syscall or a registry read, which cannot happen per
// request on a hot path, so it belongs in a layer that can cache and refresh on
// its own terms.
package netproxy

import (
	"net/http"
	"net/url"
	"sync/atomic"
)

// resolver is the installed proxy resolver, or nil for the environment default.
// An atomic pointer because Proxy is called from every transport goroutine while
// the OS-config layer may refresh it: the read path must not take a lock, and a
// resolver swap must not tear.
//
// Pointer-to-func rather than a func value because atomic.Pointer needs a
// pointer type; the indirection is one load on a path that is already doing a
// DNS lookup and a TCP connect.
var resolver atomic.Pointer[ProxyFunc]

// ProxyFunc is the shape net/http wants for Transport.Proxy: it returns the
// proxy URL for req, nil for "connect directly", or an error to fail the request.
type ProxyFunc func(*http.Request) (*url.URL, error)

// SetResolver installs f as the process-wide proxy resolver, replacing whatever
// was there. Pass nil to fall back to the environment.
//
// Deliberately process-wide and not per-client: a proxy is a property of the
// machine's network, not of one connection, and threading it through every
// constructor is exactly how one transport gets missed. Callers that need a
// per-request decision make it inside f, which receives the request.
func SetResolver(f ProxyFunc) {
	if f == nil {
		resolver.Store(nil)
		return
	}
	resolver.Store(&f)
}

// Proxy is the value every http.Transport.Proxy field must hold.
//
// It is not simply http.ProxyFromEnvironment because that function memoises the
// environment behind a sync.Once on first use, process-wide — so nothing read
// after the first request can ever change the answer. Routing through here keeps
// a later resolver swap (a settings change, an OS-config refresh) effective.
func Proxy(req *http.Request) (*url.URL, error) {
	if f := resolver.Load(); f != nil {
		return (*f)(req)
	}
	return http.ProxyFromEnvironment(req)
}

// ForURL reports the proxy that would carry a request to u, or nil for direct.
//
// The caller it exists for is DNS pre-resolution: when a proxy carries the
// request, the ORIGIN hostname must never be resolved locally — the proxy
// resolves it, and a local lookup would publish to the machine's resolver
// exactly the server name the user routed through a proxy to keep private. The
// proxy's OWN hostname still resolves locally, which is unavoidable and correct.
func ForURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	// Proxy resolvers read only req.URL (this is true of
	// http.ProxyFromEnvironment and of anything modelled on it), so a bare
	// request is enough and avoids allocating a body or a header map.
	p, err := Proxy(&http.Request{URL: u})
	if err != nil {
		// An unresolvable proxy config is not a licence to go direct: report that
		// SOMETHING is configured so the caller takes the cautious branch. The
		// request itself will fail later with the real error.
		return &url.URL{}
	}
	return p
}

// Transport returns an http.Transport with the client's proxy resolver already
// installed, cloned from http.DefaultTransport so it also carries the stdlib's
// dial, TLS-handshake and idle-connection timeouts.
//
// Cloning matters more than it looks: the bug this package was written for was a
// hand-built `&http.Transport{TLSClientConfig: ...}` that silently dropped not
// just Proxy but every one of those timeouts along with it.
func Transport() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok { // only reachable if something replaced http.DefaultTransport
		return &http.Transport{Proxy: Proxy}
	}
	c := t.Clone()
	c.Proxy = Proxy
	return c
}
