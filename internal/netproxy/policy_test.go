package netproxy

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func resolveVia(t *testing.T, p *Policy, raw string) (*url.URL, error) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p.resolve(u)
}

// TestParseProxyServerReadsThePerSchemeList: every platform writes the proxy
// either as a bare host:port or as a `scheme=host:port` list. Reading only the
// first entry of the list — or treating the whole string as one URL — sends
// https traffic to the http proxy or to nothing at all.
func TestParseProxyServerReadsThePerSchemeList(t *testing.T) {
	httpVia, httpsVia := parseProxyServer("http=a.test:8080;https=b.test:8443;ftp=c.test:21")
	if httpVia == nil || httpVia.Host != "a.test:8080" {
		t.Errorf("http proxy = %v, want a.test:8080", httpVia)
	}
	if httpsVia == nil || httpsVia.Host != "b.test:8443" {
		t.Errorf("https proxy = %v, want b.test:8443", httpsVia)
	}

	// The bare form applies to both.
	httpVia, httpsVia = parseProxyServer("proxy.test:3128")
	if httpVia == nil || httpsVia == nil || httpVia.Host != "proxy.test:3128" || httpsVia.Host != "proxy.test:3128" {
		t.Errorf("bare host:port = (%v, %v), want both proxy.test:3128", httpVia, httpsVia)
	}

	// A socks entry fills whichever scheme nothing more specific claimed.
	httpVia, httpsVia = parseProxyServer("socks=s.test:1080;https=b.test:8443")
	if httpsVia == nil || httpsVia.Host != "b.test:8443" {
		t.Errorf("an explicit https entry must win over socks, got %v", httpsVia)
	}
	if httpVia == nil || httpVia.Scheme != "socks5" {
		t.Errorf("http proxy = %v, want the socks entry promoted to socks5", httpVia)
	}

	// One malformed entry must not discard the entries beside it.
	httpVia, httpsVia = parseProxyServer("http=;https=b.test:8443")
	if httpsVia == nil {
		t.Error("a malformed http entry discarded a valid https entry")
	}
	if httpVia != nil {
		t.Errorf("malformed http entry produced %v, want nil", httpVia)
	}
}

// TestParseProxyURLSuppliesAMissingScheme: "proxy.test:8080" parses as scheme
// "proxy.test" if handed straight to url.Parse, which yields a proxy the
// transport cannot dial and no error anywhere.
func TestParseProxyURLSuppliesAMissingScheme(t *testing.T) {
	u := parseProxyURL("proxy.test:8080")
	if u == nil || u.Scheme != "http" || u.Host != "proxy.test:8080" {
		t.Fatalf("parseProxyURL(bare) = %v, want http://proxy.test:8080", u)
	}
	for _, raw := range []string{"socks5://s.test:1080", "https://p.test:443", "socks5h://s.test:1080"} {
		if got := parseProxyURL(raw); got == nil {
			t.Errorf("%q rejected — net/http dials all of these natively", raw)
		}
	}
	for _, raw := range []string{"", "   ", "ftp://p.test:21", "://nonsense"} {
		if got := parseProxyURL(raw); got != nil {
			t.Errorf("parseProxyURL(%q) = %v, want nil", raw, got)
		}
	}
}

// TestPolicyResolvesPerScheme: http and https are configured separately on every
// platform and real deployments differ between them.
func TestPolicyResolvesPerScheme(t *testing.T) {
	p := &Policy{
		HTTP:   parseProxyURL("a.test:8080"),
		HTTPS:  parseProxyURL("b.test:8443"),
		Bypass: parseBypass("skip.test"),
	}
	for _, tc := range []struct{ url, want string }{
		{"http://x.test/a", "a.test:8080"},
		{"https://x.test/a", "b.test:8443"},
		// ws:// and wss:// are the game socket, and they must follow their plain
		// HTTP equivalents — that is how the handshake is actually carried.
		{"ws://x.test/a", "a.test:8080"},
		{"wss://x.test/a", "b.test:8443"},
	} {
		got, err := resolveVia(t, p, tc.url)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.Host != tc.want {
			t.Errorf("%s resolved to %v, want %s", tc.url, got, tc.want)
		}
	}
	if got, _ := resolveVia(t, p, "https://skip.test/a"); got != nil {
		t.Errorf("a bypassed host resolved to %v, want direct", got)
	}
}

// TestAutoScriptFailsClosed is the owner's ruling made testable. A machine
// configured with an automatic proxy script we cannot evaluate must FAIL, not
// quietly connect direct: falling back to direct is precisely the leak this
// package exists to close, on exactly the configurations that are hardest to
// notice.
func TestAutoScriptFailsClosed(t *testing.T) {
	p := &Policy{Mode: ModeSystem, AutoScript: true, Bypass: parseBypass("<local>")}
	if _, err := resolveVia(t, p, "https://server.test/a"); !errors.Is(err, ErrProxyUnresolvable) {
		t.Errorf("error = %v, want ErrProxyUnresolvable — going direct here is the leak", err)
	}
	// ...but a bypassed destination is not unresolvable: we know exactly where it
	// goes. Failing those too would break loopback and the LAN for no gain.
	for _, raw := range []string{"http://localhost:8080/a", "https://intranet/a", "http://127.0.0.1/a"} {
		got, err := resolveVia(t, p, raw)
		if err != nil || got != nil {
			t.Errorf("%s: got (%v, %v), want direct with no error", raw, got, err)
		}
	}
	// A known proxy alongside the script is not unresolvable either.
	p.HTTPS = parseProxyURL("b.test:8443")
	if got, err := resolveVia(t, p, "https://server.test/a"); err != nil || got == nil {
		t.Errorf("a resolvable https destination errored: (%v, %v)", got, err)
	}
}

// TestEnvPolicyReadsTheConventionalNames, including the two the standard library
// deliberately does not: ALL_PROXY (which curl and the SOCKS ecosystem use) and
// an uppercase HTTP_PROXY (which net/http skips because CGI puts a request
// header there — we skip it for the same reason).
func TestEnvPolicyReadsTheConventionalNames(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	if p := envPolicy(env(map[string]string{})); p != nil {
		t.Errorf("an empty environment produced %v, want nil so the OS layer gets a turn", p)
	}
	p := envPolicy(env(map[string]string{"https_proxy": "e.test:8443", "no_proxy": "skip.test,10.*"}))
	if p == nil || p.HTTPS == nil || p.HTTPS.Host != "e.test:8443" {
		t.Fatalf("https_proxy not honoured: %+v", p)
	}
	if !p.Bypass.match("skip.test") || !p.Bypass.match("10.0.0.1") {
		t.Error("no_proxy was not parsed into the bypass list")
	}
	if p.HTTP != nil {
		t.Errorf("http_proxy was unset but resolved to %v", p.HTTP)
	}

	// ALL_PROXY fills whichever scheme nothing more specific claimed.
	p = envPolicy(env(map[string]string{"ALL_PROXY": "socks5://s.test:1080", "https_proxy": "e.test:8443"}))
	if p.HTTPS == nil || p.HTTPS.Host != "e.test:8443" {
		t.Errorf("https_proxy must win over ALL_PROXY, got %v", p.HTTPS)
	}
	if p.HTTP == nil || p.HTTP.Scheme != "socks5" {
		t.Errorf("ALL_PROXY did not fill the unset http slot, got %v", p.HTTP)
	}

	// Uppercase HTTP_PROXY is deliberately NOT read.
	if p := envPolicy(env(map[string]string{"HTTP_PROXY": "cgi.test:80"})); p != nil {
		t.Errorf("uppercase HTTP_PROXY was honoured (%v) — a CGI request header must not become a proxy", p)
	}
}

// TestResolvePolicyModeOrder: the environment is an explicit per-launch
// instruction, the OS setting is ambient, so the environment wins — and Direct
// consults neither.
func TestResolvePolicyModeOrder(t *testing.T) {
	osPolicy := &Policy{Mode: ModeSystem, Source: "operating system", HTTPS: parseProxyURL("os.test:8443")}
	restore := discoverSystem
	discoverSystem = func() *Policy { return osPolicy }
	t.Cleanup(func() { discoverSystem = restore })
	t.Setenv("https_proxy", "env.test:8443")

	if p := resolvePolicy(ModeSystem, ""); p.HTTPS == nil || p.HTTPS.Host != "env.test:8443" {
		t.Errorf("system mode chose %v, want the environment to win over the OS setting", p.HTTPS)
	}
	if p := resolvePolicy(ModeDirect, ""); p.HTTPS != nil || p.Mode != ModeDirect {
		t.Errorf("direct mode resolved to %+v, want no proxy at all", p)
	}
	if p := resolvePolicy(ModeManual, "m.test:9000"); p.HTTPS == nil || p.HTTPS.Host != "m.test:9000" {
		t.Errorf("manual mode chose %v, want the user's own URL", p.HTTPS)
	}
}

// TestManualModeWithAnUnusableURLFailsClosed: the user asked for a proxy. Going
// direct instead is the leak, so an address that cannot be read has to fail and
// say so rather than degrade into working-but-wrong.
func TestManualModeWithAnUnusableURLFailsClosed(t *testing.T) {
	p := resolvePolicy(ModeManual, "not a url at all")
	if !p.AutoScript {
		t.Fatal("an unreadable manual proxy silently became direct")
	}
	if _, err := resolveVia(t, p, "https://server.test/a"); !errors.Is(err, ErrProxyUnresolvable) {
		t.Errorf("error = %v, want ErrProxyUnresolvable", err)
	}
	if !strings.Contains(p.Describe(), "could not be read") {
		t.Errorf("Describe() = %q, want it to name the problem", p.Describe())
	}
}

// TestDescribeNamesTheSourceAndTheRules. The largest risk in honouring the
// system proxy is honouring one that cannot carry AO traffic, so the readout has
// to tell the user WHAT was found and WHERE it came from without a debug log.
func TestDescribeNamesTheSourceAndTheRules(t *testing.T) {
	p := &Policy{
		Mode:   ModeSystem,
		Source: "operating system",
		HTTP:   parseProxyURL("p.test:8080"),
		HTTPS:  parseProxyURL("p.test:8080"),
		Bypass: parseBypass("<local>;10.*;<-loopback>"),
	}
	got := p.Describe()
	for _, want := range []string{"p.test:8080", "operating system", "bypass rule", "ignored"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe() = %q, missing %q", got, want)
		}
	}
	if got := (&Policy{}).Describe(); !strings.Contains(got, "no proxy") {
		t.Errorf("an empty policy describes as %q, want it to say no proxy", got)
	}
	if got := (*Policy)(nil).Describe(); got != "no proxy" {
		t.Errorf("a nil policy describes as %q", got)
	}
}

// TestConfigureInstallsThePolicyIntoTheResolver ties the two halves together:
// after Configure, an ordinary transport asking Proxy() gets the policy's answer.
func TestConfigureInstallsThePolicyIntoTheResolver(t *testing.T) {
	t.Cleanup(Reset)
	Configure(ModeManual, "m.test:9000")
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "anywhere.test"}}
	got, err := Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Host != "m.test:9000" {
		t.Errorf("Proxy() = %v after Configure(manual), want m.test:9000", got)
	}
	if Current().Mode != ModeManual {
		t.Errorf("Current().Mode = %v, want ModeManual", Current().Mode)
	}
	Configure(ModeDirect, "m.test:9000")
	if got, _ := Proxy(req); got != nil {
		t.Errorf("Proxy() = %v after Configure(direct), want nil — Direct is the escape hatch and must be absolute", got)
	}
	Reset()
	if Current() != direct {
		t.Error("Reset did not restore the direct policy")
	}
}
