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
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

const (
	directDialTimeout = 30 * time.Second
	directKeepAlive   = 30 * time.Second
	// maxTrackedTransports bounds the transports Apply flushes (hard rule 4); the client has very few.
	maxTrackedTransports = 16
)

// errNotConfigured fails connections made before the first Apply, so a wiring mistake can't go direct.
var errNotConfigured = errors.New("netproxy: connection attempted before the proxy setting was applied")

// DialFunc opens one connection.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Factory opens sockets according to the policy in force. It is safe from any goroutine.
type Factory struct {
	system func() platform.ProxySettings
	getenv func(string) string
	policy atomic.Pointer[Policy]
	shared *http.Transport

	mu         sync.Mutex
	transports []*http.Transport
}

// New returns a factory that refuses connections until Apply; system reads the OS proxy settings and may block.
func New(system func() platform.ProxySettings, getenv func(string) string) *Factory {
	f := &Factory{system: system, getenv: getenv}
	f.policy.Store(&Policy{Mode: ModeDirect, Source: "not configured yet", Block: errNotConfigured})
	f.shared = f.NewTransport()
	f.Track(f.shared)
	return f
}

// Direct returns a factory that never proxies, for tests and tools with no proxy setting.
func Direct() *Factory {
	f := New(func() platform.ProxySettings { return platform.ProxySettings{} }, func(string) string { return "" })
	f.Apply(ModeDirect, "")
	return f
}

// Apply resolves and publishes the policy; idle connections close so the next request takes the new route. Live
// connections keep their route until they reconnect. It may block on the OS, so never call it on the render thread.
func (f *Factory) Apply(mode Mode, manualURL string) *Policy {
	p := resolvePolicy(mode, manualURL, f.getenv, f.system)
	f.policy.Store(p)
	f.mu.Lock()
	transports := slices.Clone(f.transports)
	f.mu.Unlock()
	for _, t := range transports {
		t.CloseIdleConnections()
	}
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
		if via == nil {
			return direct(ctx, network, addr)
		}
		return dialTunnel(ctx, via, addr)
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
	return t
}

// Track makes Apply flush t's idle connections, as a reused connection would keep its old route.
func (f *Factory) Track(t *http.Transport) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transports = append(f.transports, t)
	if len(f.transports) > maxTrackedTransports {
		f.transports[0].CloseIdleConnections()
		f.transports = slices.Delete(f.transports, 0, 1)
	}
}

// Client returns an HTTP client on the shared transport; 0 means no overall timeout.
func (f *Factory) Client(timeout time.Duration) *http.Client {
	return &http.Client{Transport: f.shared, Timeout: timeout}
}

// InsecureClient skips TLS verification, for the user's option on self-signed servers. Its one connection is a
// WebSocket that leaves the idle pool, so it isn't tracked.
func (f *Factory) InsecureClient() *http.Client {
	t := f.NewTransport()
	t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &http.Client{Transport: t}
}

func dialDirect(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: directDialTimeout, KeepAlive: directKeepAlive}
	return d.DialContext(ctx, network, addr)
}
