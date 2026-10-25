package netproxy

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// A Policy is an immutable answer to "where does traffic go". One is built by
// discovery, published through SetPolicy, and read by every dial without a lock.
// Nothing mutates a Policy after it is published — a refresh builds a new one and
// swaps the pointer — which is what makes the read path safe from every
// transport goroutine at once.

// Mode is where the policy came from, and it is a USER choice rather than a
// detected fact: a user who can see that AsyncAO found a proxy must be able to
// say "not for this app" in one click, because a detected proxy that cannot
// carry AO traffic would otherwise leave them with no way back.
type Mode int

const (
	// ModeSystem takes the machine's own configuration — the shipped default,
	// because a client that ignores it is the defect this exists to fix.
	ModeSystem Mode = iota
	// ModeDirect never proxies, whatever the machine says. The escape hatch.
	ModeDirect
	// ModeManual uses one operator-supplied URL for everything.
	ModeManual
	// ModeCount bounds the cycle control and the preference clamp.
	ModeCount
)

// ErrProxyUnresolvable is returned for a request that a configured proxy should
// carry but whose destination we cannot work out — in practice, a machine
// configured with an automatic proxy script (PAC or WPAD) on a platform where
// AsyncAO cannot evaluate one.
//
// Returning an error FAILS THE REQUEST rather than quietly connecting direct,
// and that is the whole point. Falling back to direct is precisely the defect
// being fixed: traffic the user believes is tunnelled would leave the machine,
// and it would do so on exactly the configurations that are hardest to notice.
// The user-facing side of this is a banner naming the situation and offering the
// two ways out (enter the proxy by hand, or switch to Never use a proxy) — an
// error with no explanation would be worse than the leak.
var ErrProxyUnresolvable = errors.New("netproxy: the system uses an automatic proxy script this platform cannot evaluate")

// Policy is one resolved configuration.
type Policy struct {
	// Mode is the user's setting, carried so the readout can say whether what
	// follows was detected or chosen.
	Mode Mode
	// Source names where this came from, for the Settings readout: a user has to
	// be able to see WHY their traffic is going somewhere without reading a log.
	Source string
	// HTTP and HTTPS are the proxies for the two schemes; nil means direct.
	// They are separate because every OS configures them separately and real
	// deployments differ between them.
	HTTP  *url.URL
	HTTPS *url.URL
	// Bypass lists the destinations that go direct regardless.
	Bypass *bypassList
	// AutoScript marks a machine configured with a PAC URL or WPAD that this
	// build cannot evaluate. Requests that are not bypassed then fail with
	// ErrProxyUnresolvable instead of leaking direct.
	AutoScript bool
}

// direct is the policy installed before discovery has run and whenever the user
// picks ModeDirect. Not a nil *Policy: a nil check on the read path is one more
// thing to get wrong, and "no proxy" is a perfectly good answer to publish.
var direct = &Policy{Mode: ModeDirect, Source: "no proxy"}

// resolve answers for one destination.
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
		// An unknown scheme reaching a proxy decision is not something to guess
		// about. Treat it as https, the stricter of the two.
		via = p.HTTPS
	}
	if via == nil && p.AutoScript {
		return nil, ErrProxyUnresolvable
	}
	return via, nil
}

// Describe is the one-line status the Settings readout shows. It exists because
// the largest risk in honouring the system proxy is honouring one that cannot
// carry AO traffic: the user has to be able to SEE what was detected and where
// it came from, without a debug log and without guessing.
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

// parseProxyURL turns one OS-supplied proxy value into a URL. The value may be a
// bare `host:port`, which every platform writes and which url.Parse reads as a
// scheme — so a scheme is supplied when one is missing rather than letting
// "proxy.example:8080" parse as scheme "proxy.example".
//
// Returns nil for anything unusable rather than an error: a malformed entry in a
// multi-scheme list must not discard the entries beside it.
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
		// net/http dials all four natively; no dependency is needed for SOCKS.
	default:
		return nil
	}
	return u
}

// parseProxyServer reads the per-scheme proxy string every platform writes in
// one of two shapes: a bare `host:port` meaning "everything", or a
// `scheme=host:port` list separated by `;` or spaces.
//
// The list form is why this cannot be a one-liner. Windows writes
// `http=a:8080;https=b:8080;ftp=c:8080`, and reading only the first entry — or
// treating the whole string as one URL — sends https traffic to the http proxy
// or to nothing at all.
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
			// A socks entry applies to both schemes when nothing more specific
			// was named, which is how every browser reads it.
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

// envPolicy is what the process environment says. It is the ONLY discovery every
// platform shares, and on Linux it is the whole story — there is no OS-level
// proxy setting there, only this convention, and a desktop's GUI proxy setting
// does not reach a non-GLib application. That is a platform fact, not a gap in
// AsyncAO.
//
// Implemented against the environment directly rather than through
// http.ProxyFromEnvironment because that function memoises its answer behind a
// process-wide sync.Once on first use, so a refresh could never see a change.
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
	// Lowercase first for the two that have a documented conflict: HTTP_PROXY is
	// deliberately NOT read from an uppercase name by the standard library
	// either, because CGI puts a request's Proxy header there.
	httpsVia := pick("https_proxy", "HTTPS_PROXY")
	httpVia := pick("http_proxy")
	// ALL_PROXY is honoured here and is NOT honoured by the standard library —
	// curl and the SOCKS ecosystem both use it, and a user who set only that
	// would otherwise see AsyncAO ignore the one variable they set.
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

// manualPolicy is the user's own proxy URL, applied to both schemes.
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

// policyProxy is the ProxyFunc backed by a Policy. Installed by SetPolicy so the
// resolver seam and the policy model stay separable: a test can still install a
// bare function, and the OS-discovery layer does not have to know about
// http.Request.
func policyProxy(p *Policy) ProxyFunc {
	return func(req *http.Request) (*url.URL, error) { return p.resolve(req.URL) }
}
