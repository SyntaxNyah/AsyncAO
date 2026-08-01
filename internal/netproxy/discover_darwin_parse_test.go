package netproxy

import (
	"strings"
	"testing"
)

// scutil's output is parsed on every platform's test run, deliberately: the
// parsing is where the bugs are, and gating it behind //go:build darwin would
// mean this file only ever ran on a machine nobody develops on.

// scutilManual is `scutil --proxy` output for a machine with HTTP and HTTPS
// proxies set by hand, "Exclude simple hostnames" ticked, and the two exception
// entries macOS ships by default.
const scutilManual = `<dictionary> {
    ExceptionsList : <array> {
        0 : *.local
        1 : 169.254/16
        2 : corp.example.com
    }
    ExcludeSimpleHostnames : 1
    FTPPassive : 1
    HTTPEnable : 1
    HTTPPort : 8080
    HTTPProxy : proxy.corp.example
    HTTPSEnable : 1
    HTTPSPort : 8443
    HTTPSProxy : sproxy.corp.example
}`

func TestParseScutilManualProxy(t *testing.T) {
	p := parseScutil(scutilManual)
	if p == nil {
		t.Fatal("a machine with two proxies configured produced no policy")
	}
	if p.HTTP == nil || p.HTTP.Host != "proxy.corp.example:8080" {
		t.Errorf("http = %v, want proxy.corp.example:8080", p.HTTP)
	}
	if p.HTTPS == nil || p.HTTPS.Host != "sproxy.corp.example:8443" {
		t.Errorf("https = %v, want sproxy.corp.example:8443", p.HTTPS)
	}
	if p.AutoScript {
		t.Error("an explicitly configured machine must not fail closed")
	}
	// The exception list, including macOS's abbreviated CIDR and its own
	// spelling of the <local> idea.
	for _, host := range []string{"printer.local", "169.254.1.1", "git.corp.example.com", "intranet"} {
		if !p.Bypass.match(host) {
			t.Errorf("%q was not bypassed", host)
		}
	}
	if p.Bypass.match("example.com") {
		t.Error("an unrelated host was bypassed")
	}
}

// TestParseScutilSocksPromotes: a SOCKS proxy with no HTTP proxy beside it
// applies to both schemes, which is how every other client reads it.
func TestParseScutilSocksPromotes(t *testing.T) {
	p := parseScutil(`<dictionary> {
    SOCKSEnable : 1
    SOCKSPort : 1080
    SOCKSProxy : socks.example
}`)
	if p == nil || p.HTTP == nil || p.HTTPS == nil {
		t.Fatalf("a SOCKS-only machine produced %+v, want both schemes filled", p)
	}
	if p.HTTP.Scheme != "socks5" || p.HTTP.Host != "socks.example:1080" {
		t.Errorf("http = %v, want socks5://socks.example:1080", p.HTTP)
	}
}

// TestParseScutilDisabledEntriesAreIgnored: macOS keeps the host and port after
// you untick the box, so honouring them would route a user through a proxy they
// explicitly turned off.
func TestParseScutilDisabledEntriesAreIgnored(t *testing.T) {
	p := parseScutil(`<dictionary> {
    HTTPEnable : 0
    HTTPPort : 8080
    HTTPProxy : stale.example
    HTTPSEnable : 0
    HTTPSProxy : stale.example
}`)
	if p != nil {
		t.Errorf("a machine with every proxy DISABLED produced %+v, want no policy", p)
	}
}

// TestParseScutilAutoConfigFailsClosed is the owner's ruling on the platform it
// actually bites. macOS can be configured with a proxy script, and evaluating one
// needs a JavaScript engine this client will not carry against its memory budget
// — so the honest answer is to say so and block, not to connect direct and hope.
func TestParseScutilAutoConfigFailsClosed(t *testing.T) {
	p := parseScutil(`<dictionary> {
    ProxyAutoConfigEnable : 1
    ProxyAutoConfigURLString : http://wpad.corp.example/proxy.pac
    ExceptionsList : <array> {
        0 : *.local
    }
}`)
	if p == nil || !p.AutoScript {
		t.Fatalf("a PAC-configured machine produced %+v, want a fail-closed policy", p)
	}
	if !strings.Contains(p.Source, "proxy.pac") {
		t.Errorf("Source = %q — it must name the script so the banner can be actionable", p.Source)
	}
	// Bypassed destinations still resolve: we know exactly where those go, and
	// blocking them would break loopback and the LAN for nothing.
	if _, err := resolveVia(t, p, "https://printer.local/x"); err != nil {
		t.Errorf("a bypassed host failed closed: %v", err)
	}
	// Auto-DISCOVERY without a script URL is the same situation.
	p = parseScutil("<dictionary> {\n    ProxyAutoDiscoveryEnable : 1\n}")
	if p == nil || !p.AutoScript {
		t.Errorf("WPAD-configured macOS produced %+v, want a fail-closed policy", p)
	}
}

// TestParseScutilEmptyIsNoPolicy: the common case is a machine with no proxy at
// all, and it must produce nil so the caller can say "no proxy configured"
// rather than a live policy that proxies nothing.
func TestParseScutilEmptyIsNoPolicy(t *testing.T) {
	for _, out := range []string{"", "<dictionary> {\n    FTPPassive : 1\n}", "garbage"} {
		if p := parseScutil(out); p != nil {
			t.Errorf("parseScutil(%q) = %+v, want nil", out, p)
		}
	}
}
