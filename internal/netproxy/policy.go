package netproxy

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// A Policy is immutable once published: a refresh swaps the pointer, so every dial reads it without a lock.

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

// ErrProxyUnresolvable fails a request whose proxy can't be worked out (a PAC or WPAD setup this platform
// can't evaluate), because going direct would leak traffic the user believes is tunnelled.
var ErrProxyUnresolvable = errors.New("netproxy: the system uses an automatic proxy script this platform cannot evaluate")

// Policy is one resolved configuration.
type Policy struct {
	// Mode is the user's setting, so the readout can say whether this was detected or chosen.
	Mode Mode
	// Source names the origin for the Settings readout.
	Source string
	// HTTP and HTTPS are the proxies per scheme, nil for direct; every OS configures them separately.
	HTTP  *url.URL
	HTTPS *url.URL
	// Bypass lists the destinations that go direct regardless.
	Bypass *bypassList
	// AutoScript marks a PAC URL or WPAD setup this build can't evaluate; requests fail rather than leak direct.
	AutoScript bool
}

// direct is the policy before discovery runs and for ModeDirect; non-nil so the read path needs no nil check.
var direct = &Policy{Mode: ModeDirect, Source: "no proxy"}

func (p *Policy) resolve(u *url.URL) (*url.URL, error) {
	if p == nil || u == nil {
		return nil, nil
	}
	if p.Bypass.match(u.Hostname()) {
		return nil, nil
	}
	var via *url.URL
	switch u.Scheme {
	case "https", "wss":
		via = p.HTTPS
	case "http", "ws":
		via = p.HTTP
	default:
		// Treat an unknown scheme as https, the stricter of the two.
		via = p.HTTPS
	}
	if via == nil && p.AutoScript {
		return nil, ErrProxyUnresolvable
	}
	return via, nil
}

// Describe is the Settings status line, so the user can see what was detected and where it came from.
func (p *Policy) Describe() string {
	if p == nil {
		return "no proxy"
	}
	var b strings.Builder
	switch {
	case p.AutoScript && p.HTTPS == nil && p.HTTP == nil:
		b.WriteString("automatic proxy script (cannot be evaluated on this platform)")
	case p.HTTPS != nil && p.HTTP != nil && *p.HTTPS == *p.HTTP:
		b.WriteString(p.HTTPS.Host)
	case p.HTTPS == nil && p.HTTP == nil:
		b.WriteString("no proxy")
	default:
		if p.HTTPS != nil {
			b.WriteString("https via " + p.HTTPS.Host)
		}
		if p.HTTP != nil {
			if b.Len() > 0 {
				b.WriteString(", ")
			}
			b.WriteString("http via " + p.HTTP.Host)
		}
	}
	if p.Source != "" {
		b.WriteString(" (" + p.Source + ")")
	}
	if n := p.bypassCount(); n > 0 {
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

func (p *Policy) bypassCount() int {
	if p.Bypass == nil {
		return 0
	}
	n := len(p.Bypass.hosts) + len(p.Bypass.nets) + len(p.Bypass.ips)
	if p.Bypass.noDot {
		n++
	}
	return n
}

// parseProxyURL supplies a missing scheme, as url.Parse reads "proxy.example:8080" as scheme "proxy.example";
// it returns nil, not an error, so a bad entry doesn't discard its neighbours.
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
		// net/http dials all four natively, including SOCKS.
	default:
		return nil
	}
	return u
}

// parseProxyServer reads a bare `host:port` or a `scheme=host:port` list split by `;` or spaces.
func parseProxyServer(raw string) (httpVia, httpsVia *url.URL) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "=") {
		via := parseProxyURL(raw)
		return via, via
	}
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
		case "http":
			httpVia = via
		case "https":
			httpsVia = via
		case "socks":
			// A socks entry applies to both schemes when nothing more specific was named, as browsers read it.
			via.Scheme = "socks5"
			if httpVia == nil {
				httpVia = via
			}
			if httpsVia == nil {
				httpsVia = via
			}
		}
	}
	return httpVia, httpsVia
}

// envPolicy reads the environment directly: http.ProxyFromEnvironment memoises and could never see a refresh.
func envPolicy(getenv func(string) string) *Policy {
	pick := func(names ...string) *url.URL {
		for _, n := range names {
			if v := getenv(n); v != "" {
				if u := parseProxyURL(v); u != nil {
					return u
				}
			}
		}
		return nil
	}
	// Lowercase first: uppercase HTTP_PROXY is unsafe, as CGI puts a request's Proxy header there.
	httpsVia := pick("https_proxy", "HTTPS_PROXY")
	httpVia := pick("http_proxy")
	// The standard library ignores ALL_PROXY, but curl and the SOCKS ecosystem use it.
	if all := pick("all_proxy", "ALL_PROXY"); all != nil {
		if httpsVia == nil {
			httpsVia = all
		}
		if httpVia == nil {
			httpVia = all
		}
	}
	if httpVia == nil && httpsVia == nil {
		return nil
	}
	no := getenv("no_proxy")
	if no == "" {
		no = getenv("NO_PROXY")
	}
	return &Policy{
		Mode:   ModeSystem,
		Source: "environment",
		HTTP:   httpVia,
		HTTPS:  httpsVia,
		Bypass: parseBypass(no),
	}
}

func manualPolicy(raw string) *Policy {
	via := parseProxyURL(raw)
	if via == nil {
		return nil
	}
	return &Policy{
		Mode:   ModeManual,
		Source: "set in Settings",
		HTTP:   via,
		HTTPS:  via,
		Bypass: parseBypass(""),
	}
}

// policyProxy keeps OS discovery free of http.Request and lets a test install a bare function.
func policyProxy(p *Policy) ProxyFunc {
	return func(req *http.Request) (*url.URL, error) { return p.resolve(req.URL) }
}
