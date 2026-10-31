// Package netproxy is the client's socket factory: every outbound TCP connection is opened here, so what Settings shows
// and what the client does cannot disagree. A connection goes direct, through a CONNECT tunnel or through SOCKS5.
package netproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

const (
	directDialTimeout = 30 * time.Second
	directKeepAlive   = 30 * time.Second
	// maxTrackedConns bounds the open connections Apply closes (hard rule 4); the client opens far
	// fewer, so the cap is never reached in practice.
	maxTrackedConns = 512
	// tlsSessionCacheEntries is the resumable TLS sessions one transport keeps; the client talks to few hosts.
	tlsSessionCacheEntries = 32
)

// errNotConfigured fails connections made before the first Apply, so a wiring mistake can't go direct.
var errNotConfigured = errors.New("netproxy: connection attempted before the proxy setting was applied")

// DialFunc opens one connection.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Factory opens sockets according to the policy in force. It is safe from any goroutine.
type Factory struct {
	system        func() platform.ProxySettings
	getenv        func(string) string
	policy        atomic.Pointer[Policy]
	shared        *http.Transport // CONNECT-always, for the game WebSocket
	sharedForward *http.Transport // forwards http://, CONNECTs https://, for plain fetches

	mu    sync.Mutex
	conns map[*trackedConn]struct{}
}

// New returns a factory that refuses connections until Apply; system reads the OS proxy settings and may block.
func New(system func() platform.ProxySettings, getenv func(string) string) *Factory {
	f := &Factory{system: system, getenv: getenv, conns: map[*trackedConn]struct{}{}}
	f.policy.Store(&Policy{Mode: ModeDirect, Source: "not configured yet", Block: errNotConfigured})
	f.shared = f.NewTransport()
	f.sharedForward = f.ForwardTransport(dialDirect)
	return f
}

// Direct returns a factory that never proxies, for tests and tools with no proxy setting.
func Direct() *Factory {
	f := New(func() platform.ProxySettings { return platform.ProxySettings{} }, func(string) string { return "" })
	f.Apply(ModeDirect, "")
	return f
}

// Apply resolves and publishes the policy, then closes every open connection so nothing keeps
// serving on the old route: the game socket drops with the proxy, by design. It may block on the
// OS, so never call it on the render thread.
func (f *Factory) Apply(mode Mode, manualURL string) *Policy {
	p := resolvePolicy(mode, manualURL, f.getenv, f.system)
	f.policy.Store(p)
	f.closeAllConns()
	return p
}

// Current is the policy in force, never nil.
func (f *Factory) Current() *Policy { return f.policy.Load() }

// Proxied reports whether a connection to host would not go direct, so a caller knows not to look the name up itself.
func (f *Factory) Proxied(host string) bool {
	via, err := f.policy.Load().route(host)
	return via != nil || err != nil
}

// DialContext opens a connection to addr by the current policy.
func (f *Factory) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return f.Dialer(dialDirect)(ctx, network, addr)
}

// Dialer is DialContext with a caller's own direct path, such as a DNS cache; proxied connections never use it.
func (f *Factory) Dialer(direct DialFunc) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("netproxy: %w", err)
		}
		via, err := f.policy.Load().route(host)
		if err != nil {
			return nil, err
		}
		var conn net.Conn
		if via == nil {
			conn, err = direct(ctx, network, addr)
		} else {
			conn, err = dialTunnel(ctx, via, addr)
		}
		if err != nil {
			return nil, err
		}
		return f.track(conn), nil
	}
}

// NewTransport clones the stdlib's defaults, so its timeouts stay, with every connection coming from this factory.
// Proxy stays nil: routing happens at the socket, and a second decision here could disagree with it.
func (f *Factory) NewTransport() *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	t := base.Clone()
	t.Proxy = nil
	t.DialContext = f.DialContext
	t.TLSClientConfig = tlsConfigOf(t)
	t.TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(tlsSessionCacheEntries)
	return t
}

// ForwardTransport builds an HTTP transport that proxies from the one policy through the stdlib:
// plain http:// is forwarded to the proxy as an ordinary request, https:// is tunnelled with
// CONNECT, and a block fails the request. SOCKS, bypass and direct dials still go through the
// factory's own socket path (forwardDial), so SOCKS keeps sending host names, never a local lookup.
func (f *Factory) ForwardTransport(direct DialFunc) *http.Transport {
	t := f.NewTransport()
	t.Proxy = f.proxyForRequest
	t.DialContext = f.forwardDial(direct)
	return t
}

// proxyForRequest hands the stdlib an http/https proxy to forward or CONNECT through, nil for SOCKS,
// bypass and direct (forwardDial routes those), and the block error when connections are refused.
func (f *Factory) proxyForRequest(req *http.Request) (*url.URL, error) {
	via, err := f.policy.Load().route(req.URL.Hostname())
	if err != nil {
		return nil, err
	}
	if via != nil && (via.Scheme == "http" || via.Scheme == schemeHTTPS) {
		return via, nil
	}
	return nil, nil
}

// forwardDial dials the http/https proxy itself directly (the stdlib is tunnelling or forwarding
// through it), and routes everything else — SOCKS, bypass, direct — through the socket path.
func (f *Factory) forwardDial(direct DialFunc) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if via := f.policy.Load().Via; via != nil &&
			(via.Scheme == "http" || via.Scheme == schemeHTTPS) && addr == proxyAddress(via) {
			conn, err := direct(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return f.track(conn), nil
		}
		return f.Dialer(direct)(ctx, network, addr)
	}
}

// trackedConn lets Apply close a live connection, not just an idle one: every socket the factory
// opens is registered, and closed when the policy changes, so nothing keeps serving on a route the
// user has switched away from.
type trackedConn struct {
	net.Conn
	f *Factory
}

func (c *trackedConn) Close() error {
	c.f.forget(c)
	return c.Conn.Close()
}

// track registers conn so Apply can close it; over the cap it is left untracked rather than growing
// without bound (hard rule 4) — unreachable in practice, and such a conn still works.
func (f *Factory) track(conn net.Conn) net.Conn {
	c := &trackedConn{Conn: conn, f: f}
	f.mu.Lock()
	if len(f.conns) < maxTrackedConns {
		f.conns[c] = struct{}{}
	}
	f.mu.Unlock()
	return c
}

func (f *Factory) forget(c *trackedConn) {
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()
}

func (f *Factory) closeAllConns() {
	f.mu.Lock()
	open := make([]*trackedConn, 0, len(f.conns))
	for c := range f.conns {
		open = append(open, c)
	}
	clear(f.conns)
	f.mu.Unlock()
	for _, c := range open {
		_ = c.Conn.Close()
	}
}

// Client returns an HTTP client for plain fetches (assets, server lists, updates): it forwards
// http:// and tunnels https:// with CONNECT. 0 means no overall timeout.
func (f *Factory) Client(timeout time.Duration) *http.Client {
	return &http.Client{Transport: f.sharedForward, Timeout: timeout}
}

// TunnelClient tunnels every request with CONNECT, for the game WebSocket, which a forward proxy
// cannot carry. Its connection drops on the next Apply like any other.
func (f *Factory) TunnelClient() *http.Client {
	return &http.Client{Transport: f.shared}
}

// InsecureClient skips TLS verification, for the user's option on self-signed servers. Like every
// connection the factory opens, its socket is dropped on the next Apply.
func (f *Factory) InsecureClient() *http.Client {
	t := f.NewTransport()
	t.TLSClientConfig.InsecureSkipVerify = true
	return &http.Client{Transport: t}
}

// tlsConfigOf keeps the clone's own config where it has one, as that can carry the protocols it offers.
func tlsConfigOf(t *http.Transport) *tls.Config {
	if t.TLSClientConfig != nil {
		return t.TLSClientConfig
	}
	return &tls.Config{}
}

func dialDirect(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: directDialTimeout, KeepAlive: directKeepAlive}
	return d.DialContext(ctx, network, addr)
}
