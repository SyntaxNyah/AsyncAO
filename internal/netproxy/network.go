package netproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

const (
	directDialTimeout = 30 * time.Second
	directKeepAlive   = 30 * time.Second
	// tlsSessionCacheEntries is the resumable TLS sessions one transport keeps; the client talks to few hosts.
	tlsSessionCacheEntries = 32
)

// Mode is the user's choice of where the configuration comes from.
type Mode int

const (
	// ModeSystem takes the machine's own configuration; the default.
	ModeSystem Mode = iota
	// ModeDirect never proxies, whatever the machine says.
	ModeDirect
	// ModeManual uses the typed address and bypass list, whatever the machine says.
	ModeManual
	// ModeCount bounds the preference clamp.
	ModeCount
)

// Kind selects how a client's connections reach the proxy.
type Kind int

const (
	// Web forwards http:// through the proxy and tunnels https:// with CONNECT.
	Web Kind = iota
	// Game tunnels everything with CONNECT, as a forward proxy cannot carry a WebSocket.
	Game
	// GameSkipVerify is Game without TLS verification, for the user's option on self-signed servers.
	GameSkipVerify
	kindCount
)

// DialFunc opens one connection.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// ErrBadProxyAddress fails connections when the address in Settings can't be read, so a typo shows up as an error.
var ErrBadProxyAddress = errors.New("netproxy: the proxy address in Settings could not be read")

// Network is one resolved configuration and the transports built for it, immutable once built.
type Network struct {
	// source names the origin for the Settings readout.
	source string
	// via is the one proxy every connection uses, nil for direct.
	via *url.URL
	// bypass lists the destinations that go direct; nil still sends loopback direct.
	bypass *platform.ProxyBypass
	// block fails every connection that isn't bypassed.
	block      error
	transports [kindCount]*http.Transport
}

func newNetwork(source string, via *url.URL, bypass *platform.ProxyBypass, block error) *Network {
	n := &Network{source: source, via: via, bypass: bypass, block: block}
	for kind := range n.transports {
		n.transports[kind] = n.newTransport(Kind(kind), dialDirect)
	}
	return n
}

// newTransport clones the stdlib's defaults, so its timeouts stay.
func (n *Network) newTransport(kind Kind, direct DialFunc) *http.Transport {
	t := &http.Transport{}
	if defaults, ok := http.DefaultTransport.(*http.Transport); ok {
		t = defaults.Clone()
	}
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	t.TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(tlsSessionCacheEntries)
	if kind == Web {
		t.Proxy = n.proxyForRequest
		t.DialContext = direct
		return t
	}
	t.Proxy = nil
	t.DialContext = n.tunnelDialer(direct)
	t.TLSClientConfig.InsecureSkipVerify = kind == GameSkipVerify
	return t
}

// Client returns an HTTP client of the given kind. 0 means no overall timeout.
func (n *Network) Client(kind Kind, timeout time.Duration) *http.Client {
	return &http.Client{Transport: n.transports[kind], Timeout: timeout}
}

// WebClient is a Web client with its own connection pool: direct opens its direct and proxy connections, and tune
// adjusts its transport.
func (n *Network) WebClient(direct DialFunc, tune func(*http.Transport)) *http.Client {
	t := n.newTransport(Web, direct)
	tune(t)
	return &http.Client{Transport: t}
}

func (n *Network) closeIdleConnections() {
	for _, t := range n.transports {
		t.CloseIdleConnections()
	}
}

// route returns the proxy for host, nil for direct.
func (n *Network) route(host string) (*url.URL, error) {
	if n.bypass.Match(host) {
		return nil, nil
	}
	return n.via, n.block
}

func (n *Network) proxyForRequest(req *http.Request) (*url.URL, error) {
	return n.route(req.URL.Hostname())
}

// tunnelDialer dials a CONNECT tunnel through the proxy, or direct when the route is direct.
func (n *Network) tunnelDialer(direct DialFunc) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("netproxy: %w", err)
		}
		via, err := n.route(host)
		if err != nil {
			return nil, err
		}
		if via == nil {
			return direct(ctx, network, addr)
		}
		return dialTunnel(ctx, via, addr)
	}
}

// Proxied reports whether a connection to host would not go direct, so a caller knows not to look the name up itself.
func (n *Network) Proxied(host string) bool {
	via, err := n.route(host)
	return via != nil || err != nil
}

// Blocked reports whether connections are being refused.
func (n *Network) Blocked() bool { return n.block != nil }

// UsesProxy reports whether connections go through a proxy.
func (n *Network) UsesProxy() bool { return n.via != nil }

// Describe is the Settings status line: what is in effect and where it came from, never any proxy credentials.
func (n *Network) Describe() string {
	var b strings.Builder
	switch {
	case n.block != nil:
		b.WriteString("connections blocked")
	case n.via != nil:
		b.WriteString(n.via.Scheme + "://" + n.via.Host)
	default:
		b.WriteString("no proxy")
	}
	if n.source != "" {
		b.WriteString(" (" + n.source + ")")
	}
	return b.String()
}

func resolveNetwork(mode Mode, address, bypass string, system func() platform.ProxySettings) *Network {
	switch mode {
	case ModeDirect:
		return newNetwork("set in Settings", nil, nil, nil)
	case ModeManual:
		list := platform.ParseProxyBypass(bypass)
		via := parseProxyURL(address)
		if via == nil {
			return newNetwork("the address in Settings can't be read", nil, list, ErrBadProxyAddress)
		}
		return newNetwork("set in Settings", via, list, nil)
	}
	s := system()
	via := parseProxyServer(s.Server)
	if via == nil {
		return newNetwork("no proxy configured on this computer", nil, nil, nil)
	}
	return newNetwork(s.Source, via, s.Bypass, nil)
}

// parseProxyURL supplies a missing scheme, as url.Parse reads "proxy.example:8080" as scheme "proxy.example"; it
// returns nil for anything unusable.
func parseProxyURL(raw string) *url.URL {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil
	}
	if u.Scheme != "http" && u.Scheme != schemeHTTPS {
		return nil
	}
	return u
}

// parseProxyServer reads a bare host:port or a scheme=host:port list split by ; or spaces, preferring the https entry.
func parseProxyServer(raw string) *url.URL {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "=") {
		return parseProxyURL(raw)
	}
	var httpsVia, httpVia *url.URL
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ' ' || r == '\t' }) {
		scheme, addr, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		via := parseProxyURL(addr)
		if via == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(scheme)) {
		case schemeHTTPS:
			httpsVia = via
		case "http":
			httpVia = via
		}
	}
	if httpsVia != nil {
		return httpsVia
	}
	return httpVia
}

func dialDirect(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: directDialTimeout, KeepAlive: directKeepAlive}
	return d.DialContext(ctx, network, addr)
}
