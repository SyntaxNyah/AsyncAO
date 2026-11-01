package platform

import (
	"strings"
	"testing"
)

// windowsProxyOverride is a real Windows ProxyOverride value, in the shape a corporate machine writes.
const windowsProxyOverride = "<local>;localhost;127.*;10.*;172.16.*;192.168.*"

func TestBypassMatchesARealWindowsOverride(t *testing.T) {
	b := ParseProxyBypass(windowsProxyOverride)
	for _, host := range []string{"intranet", "localhost", "127.0.0.1", "10.0.0.7", "172.16.4.1", "192.168.1.50", "192.168.99.240"} {
		if !b.Match(host) {
			t.Errorf("%q was not bypassed", host)
		}
	}
	for _, host := range []string{"example.com", "11.0.0.7", "172.17.4.1", "192.169.1.50"} {
		if b.Match(host) {
			t.Errorf("%q was bypassed", host)
		}
	}
}

func TestBypassNoDotIsTheLocalMacro(t *testing.T) {
	with := ParseProxyBypass("<local>")
	if !with.Match("intranet") || !with.Match("build-server") {
		t.Error("<local> must bypass every dotless host name")
	}
	if with.Match("intranet.corp.example") {
		t.Error("<local> must not bypass a dotted name")
	}
	if with.Match("10.0.0.7") {
		t.Error("<local> must not match IP literals")
	}
	if ParseProxyBypass("localhost").Match("intranet") {
		t.Error("a list without <local> must not bypass dotless names")
	}
}

func TestBypassOctetWildcardsBecomeNetworks(t *testing.T) {
	for _, tc := range []struct{ rule, in, out string }{
		{"10.*", "10.255.255.254", "11.0.0.1"},
		{"172.16.*", "172.16.255.1", "172.17.0.1"},
		{"192.168.1.*", "192.168.1.255", "192.168.2.1"},
	} {
		b := ParseProxyBypass(tc.rule)
		if !b.Match(tc.in) {
			t.Errorf("%s not matched by %q", tc.in, tc.rule)
		}
		if b.Match(tc.out) {
			t.Errorf("%s matched by %q", tc.out, tc.rule)
		}
	}
	if b := ParseProxyBypass("foo.*"); b.Match("foo.com") || b.Match("foo") {
		t.Error("a non-numeric wildcard must match nothing")
	}
}

func TestBypassDomainRulesCoverSubdomainsOnly(t *testing.T) {
	for _, rule := range []string{"foo.com", ".foo.com", "*.foo.com"} {
		b := ParseProxyBypass(rule)
		for _, host := range []string{"foo.com", "api.foo.com", "a.b.foo.com", "FOO.COM"} {
			if !b.Match(host) {
				t.Errorf("%q not matched by %q", host, rule)
			}
		}
		for _, host := range []string{"notfoo.com", "foo.com.evil.test", "barfoo.com"} {
			if b.Match(host) {
				t.Errorf("%q matched by %q", host, rule)
			}
		}
	}
}

// macOS writes abbreviated prefixes (169.254/16) that netip rejects.
func TestBypassCIDRAndShortCIDR(t *testing.T) {
	b := ParseProxyBypass("169.254/16,10.1.0.0/16")
	if !b.Match("169.254.10.1") {
		t.Error("short-form 169.254/16 did not match")
	}
	if !b.Match("10.1.2.3") || b.Match("10.2.2.3") {
		t.Error("a full CIDR was misapplied")
	}
}

func TestBypassLoopbackIsAlwaysDirect(t *testing.T) {
	var none *ProxyBypass
	for _, b := range []*ProxyBypass{none, ParseProxyBypass("example.com"), ParseProxyBypass("<-loopback>")} {
		for _, host := range []string{"localhost", "127.0.0.1", "127.9.9.9", "::1", "[::1]"} {
			if !b.Match(host) {
				t.Errorf("%q did not go direct", host)
			}
		}
	}
	if none.Match("example.com") {
		t.Error("a nil list bypassed a public host")
	}
}

func TestBypassStarMeansEverything(t *testing.T) {
	b := ParseProxyBypass("*")
	for _, host := range []string{"example.com", "10.0.0.1", "intranet", "2606:4700::1111"} {
		if !b.Match(host) {
			t.Errorf("* did not cover %q", host)
		}
	}
}

func TestBypassIsCapped(t *testing.T) {
	b := ParseProxyBypass(strings.Repeat("a.test,", maxBypassEntries) + "past.test")
	if b.Match("past.test") {
		t.Error("an entry past the cap was kept")
	}
	if got := len(b.hosts); got != maxBypassEntries {
		t.Errorf("kept %d entries, want %d", got, maxBypassEntries)
	}
}

func TestBypassSeparators(t *testing.T) {
	for _, list := range []string{"a.test;b.test", "a.test,b.test", "a.test; b.test", "a.test ,b.test"} {
		b := ParseProxyBypass(list)
		if !b.Match("a.test") || !b.Match("b.test") {
			t.Errorf("list %q did not yield both entries", list)
		}
	}
}
