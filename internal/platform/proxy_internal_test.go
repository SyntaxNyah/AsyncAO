package platform

import (
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
	for _, host := range []string{"printer.local", "169.254.1.1", "intranet"} {
		if !got.Bypass.Match(host) {
			t.Errorf("%q was not bypassed", host)
		}
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

func TestEnvironmentBeatsTheOSAndBothSpellingsWork(t *testing.T) {
	readOS := func() ProxySettings { return ProxySettings{Server: "os.test:1"} }
	for _, name := range []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "all_proxy", "ALL_PROXY"} {
		got := proxySettings(func(key string) string {
			if key == name {
				return "env.test:2"
			}
			return ""
		}, readOS)
		if got.Server != "env.test:2" {
			t.Errorf("%s: server = %q", name, got.Server)
		}
	}
	if got := proxySettings(func(string) string { return "" }, readOS); got.Server != "os.test:1" {
		t.Errorf("with no variables, server = %q", got.Server)
	}
}

func TestEnvironmentBypassApplies(t *testing.T) {
	for _, spelling := range []string{"no_proxy", "NO_PROXY"} {
		got := proxySettings(func(key string) string {
			switch key {
			case "http_proxy":
				return "env.test:2"
			case spelling:
				return "direct.test"
			}
			return ""
		}, nil)
		if !got.Bypass.Match("direct.test") || got.Bypass.Match("other.test") {
			t.Errorf("%s was not applied", spelling)
		}
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
