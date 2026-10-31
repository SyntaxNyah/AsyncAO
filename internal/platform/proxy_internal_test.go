package platform

import (
	"strings"
	"testing"
)

const scutilManual = `<dictionary> {
    ExceptionsList : <array> {
        0 : *.local
        1 : 169.254/16
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
	got := parseScutil(scutilManual)
	if want := "http=proxy.corp.example:8080;https=sproxy.corp.example:8443"; got.Server != want {
		t.Errorf("server = %q, want %q", got.Server, want)
	}
	if want := "*.local,169.254/16,<local>"; got.Bypass != want {
		t.Errorf("bypass = %q, want %q", got.Bypass, want)
	}
	if got.Source == "" {
		t.Error("no source for the readout")
	}
}

// macOS keeps host and port after the box is unticked.
func TestParseScutilIgnoresDisabledProxies(t *testing.T) {
	got := parseScutil("<dictionary> {\n    HTTPEnable : 0\n    HTTPProxy : stale.example\n    HTTPPort : 8080\n}")
	if got.Server != "" {
		t.Errorf("server = %q", got.Server)
	}
}

func TestParseScutilReadsSocksAndProxyScript(t *testing.T) {
	got := parseScutil("<dictionary> {\n    SOCKSEnable : 1\n    SOCKSProxy : s.example\n    SOCKSPort : 1080\n}")
	if got.Server != "socks=s.example:1080" {
		t.Errorf("server = %q", got.Server)
	}
	const script = "http://wpad.corp.example/proxy.pac"
	got = parseScutil("<dictionary> {\n    ProxyAutoConfigEnable : 1\n    ProxyAutoConfigURLString : " + script + "\n}")
	if !strings.HasPrefix(script, "http") || got.AutoConfigURL != script {
		t.Errorf("script = %q", got.AutoConfigURL)
	}
}

func TestParseScutilDiscoveryAloneIsNoProxy(t *testing.T) {
	got := parseScutil("<dictionary> {\n    ProxyAutoDiscoveryEnable : 1\n}")
	if got != (ProxySettings{}) {
		t.Errorf("got %+v", got)
	}
}

func TestParseScutilEmptyIsNoProxy(t *testing.T) {
	for _, out := range []string{"", "<dictionary> {\n    FTPPassive : 1\n}", "garbage"} {
		if got := parseScutil(out); got != (ProxySettings{}) {
			t.Errorf("parseScutil(%q) = %+v", out, got)
		}
	}
}
