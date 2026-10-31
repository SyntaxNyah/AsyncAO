package netproxy

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

// Mode is a user choice, so a user can refuse a detected proxy that can't carry AO traffic.
type Mode int

const (
	// ModeSystem takes the machine's own configuration; the default.
	ModeSystem Mode = iota
	// ModeDirect never proxies, whatever the machine says.
	ModeDirect
	// ModeManual uses one operator-supplied URL for everything.
	ModeManual
	// ModeCount bounds the cycle control and the preference clamp.
	ModeCount
)

var (
	// ErrProxyUnresolvable fails connections on a machine using a proxy script, because going direct would leak traffic
	// the user believes is tunnelled.
	ErrProxyUnresolvable = errors.New("netproxy: the system uses an automatic proxy script AsyncAO cannot run")
	// ErrBadProxyAddress fails connections when the address in Settings can't be read, for the same reason.
	ErrBadProxyAddress = errors.New("netproxy: the proxy address in Settings could not be read")
)

// Policy is one resolved configuration, immutable once published.
type Policy struct {
	// Mode is the user's setting, so the readout can say whether this was detected or chosen.
	Mode Mode
	// Source names the origin for the Settings readout.
	Source string
	// Via is the one proxy every connection uses, nil for direct: a socket has no scheme to choose by.
	Via *url.URL
	// Bypass lists the destinations that go direct regardless.
	Bypass *bypassList
	// Block fails every connection that isn't bypassed, when the proxy can't be worked out.
	Block error
}

// route returns the proxy for host, nil for direct. A blocked policy still lets bypassed hosts through: we know exactly
// where those go, and blocking them would break the LAN and loopback for nothing.
func (p *Policy) route(host string) (*url.URL, error) {
	if p.Bypass.match(host) {
		return nil, nil
	}
	if p.Block != nil {
		return nil, p.Block
	}
	return p.Via, nil
}

// Blocked reports whether connections are being refused rather than sent around the proxy.
func (p *Policy) Blocked() bool { return p.Block != nil }

// Describe is the Settings status line: what is in effect and where it came from, never any proxy credentials.
func (p *Policy) Describe() string {
	var b strings.Builder
	switch {
	case p.Block != nil:
		b.WriteString("connections blocked")
	case p.Via != nil:
		b.WriteString(p.Via.Scheme + "://" + p.Via.Host)
	default:
		b.WriteString("no proxy")
	}
	if p.Source != "" {
		b.WriteString(" (" + p.Source + ")")
	}
	if n := p.Bypass.count(); n > 0 {
		b.WriteString(", " + strconv.Itoa(n) + " bypass rule")
		if n != 1 {
			b.WriteByte('s')
		}
	}
	if p.Bypass != nil && p.Bypass.dropped > 0 {
		b.WriteString(", " + strconv.Itoa(p.Bypass.dropped) + " ignored")
	}
	return b.String()
}

// resolvePolicy applies the mode. In ModeSystem the environment comes first: a variable is an explicit per-launch
// instruction, while the OS setting is ambient.
func resolvePolicy(mode Mode, manualURL string, getenv func(string) string, system func() platform.ProxySettings) *Policy {
	switch mode {
	case ModeDirect:
		return &Policy{Mode: ModeDirect, Source: "set in Settings"}
	case ModeManual:
		via := parseProxyURL(manualURL)
		if via == nil {
			return &Policy{Mode: ModeManual, Source: "the address in Settings can't be read", Block: ErrBadProxyAddress}
		}
		return &Policy{Mode: ModeManual, Source: "set in Settings", Via: via}
	}
	if p := envPolicy(getenv); p != nil {
		return p
	}
	return systemPolicy(system())
}

func systemPolicy(s platform.ProxySettings) *Policy {
	bypass := parseBypass(s.Bypass)
	if via := parseProxyServer(s.Server); via != nil {
		return &Policy{Mode: ModeSystem, Source: s.Source, Via: via, Bypass: bypass}
	}
	if s.AutoConfigURL != "" {
		return &Policy{
			Mode:   ModeSystem,
			Source: "this computer uses a proxy script AsyncAO can't run: " + s.AutoConfigURL,
			Bypass: bypass,
			Block:  ErrProxyUnresolvable,
		}
	}
	return &Policy{Mode: ModeSystem, Source: "no proxy configured on this computer"}
}

// envPolicy reads both spellings of each variable, as curl and Go do; https comes first, the common choice for tunnels.
func envPolicy(getenv func(string) string) *Policy {
	for _, name := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "all_proxy", "ALL_PROXY"} {
		via := parseProxyURL(getenv(name))
		if via == nil {
			continue
		}
		no := getenv("no_proxy")
		if no == "" {
			no = getenv("NO_PROXY")
		}
		return &Policy{Mode: ModeSystem, Source: "environment variable " + name, Via: via, Bypass: parseBypass(no)}
	}
	return nil
}

// parseProxyURL supplies a missing scheme, as url.Parse reads "proxy.example:8080" as scheme "proxy.example"; it
// returns nil for anything unusable, so one bad entry doesn't discard its neighbours.
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
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
		return u
	}
	return nil
}

// parseProxyServer reads a bare host:port or a scheme=host:port list split by ; or spaces. Every connection takes the
// one proxy chosen here, preferring the https entry, as a CONNECT tunnel works through either http or https entries.
func parseProxyServer(raw string) *url.URL {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "=") {
		return parseProxyURL(raw)
	}
	var httpsVia, httpVia, socksVia *url.URL
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
		case "https":
			httpsVia = via
		case "http":
			httpVia = via
		case "socks":
			via.Scheme = "socks5"
			socksVia = via
		}
	}
	switch {
	case httpsVia != nil:
		return httpsVia
	case httpVia != nil:
		return httpVia
	}
	return socksVia
}
