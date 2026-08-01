package netproxy

import (
	"strings"
	"testing"
)

// windowsProxyOverride is a real Windows ProxyOverride value, in the shape a
// corporate machine actually writes. It is the fixture for this whole file
// because every off-the-shelf translation of it into the standard library's
// matcher was measured leaking FIVE OF FIVE of its rules.
const windowsProxyOverride = "<local>;localhost;127.*;10.*;172.16.*;192.168.*"

func TestBypassMatchesARealWindowsOverride(t *testing.T) {
	b := parseBypass(windowsProxyOverride)
	for _, host := range []string{
		"intranet",       // <local> — no dot. Inexpressible in the stdlib matcher.
		"localhost",      //
		"127.0.0.1",      // 127.*
		"10.0.0.7",       // 10.*    — a wildcard the stdlib matcher cannot apply to an IP
		"172.16.4.1",     // 172.16.*
		"192.168.1.50",   // 192.168.*
		"192.168.99.240", //
	} {
		if !b.match(host) {
			t.Errorf("%q was NOT bypassed — it would be sent to the proxy", host)
		}
	}
	for _, host := range []string{
		"example.com",   // ordinary public name
		"11.0.0.7",      // adjacent to 10.* but outside it
		"172.17.4.1",    // adjacent to 172.16.* but outside it
		"192.169.1.50",  // adjacent to 192.168.* but outside it
		"attacker.test", //
	} {
		if b.match(host) {
			t.Errorf("%q WAS bypassed — traffic the user expects to be proxied would go direct", host)
		}
	}
}

// TestBypassNoDotIsTheLocalMacro pins the single most common real entry.
// Microsoft defines <local> as "any host name that does not contain a period",
// and it is what makes http://intranet/ resolve inside a corporate network.
func TestBypassNoDotIsTheLocalMacro(t *testing.T) {
	with := parseBypass("<local>")
	if !with.match("intranet") || !with.match("build-server") {
		t.Error("<local> must bypass every dotless host name")
	}
	if with.match("intranet.corp.example") {
		t.Error("<local> must NOT bypass a dotted name")
	}
	if with.match("10.0.0.7") {
		t.Error("<local> must not swallow IP literals — they contain dots and are not local by name")
	}
	without := parseBypass("localhost")
	if without.match("intranet") {
		t.Error("a list without <local> must not bypass dotless names")
	}
}

// TestBypassOctetWildcardsBecomeCIDRs is the other half of the measured library
// bug: a `10.*` entry is not a domain rule, and treating it as one means it can
// never match the address it exists for.
func TestBypassOctetWildcardsBecomeCIDRs(t *testing.T) {
	for _, tc := range []struct {
		rule       string
		in, out    string
		wantPrefix string
	}{
		{"10.*", "10.255.255.254", "11.0.0.1", "/8"},
		{"172.16.*", "172.16.255.1", "172.17.0.1", "/16"},
		{"192.168.1.*", "192.168.1.255", "192.168.2.1", "/24"},
	} {
		t.Run(tc.rule, func(t *testing.T) {
			b := parseBypass(tc.rule)
			if len(b.nets) != 1 {
				t.Fatalf("%q produced %d networks, want 1 (want a %s)", tc.rule, len(b.nets), tc.wantPrefix)
			}
			if !b.match(tc.in) {
				t.Errorf("%s not matched by %q", tc.in, tc.rule)
			}
			if b.match(tc.out) {
				t.Errorf("%s WAS matched by %q — the mask is too wide", tc.out, tc.rule)
			}
		})
	}
	// A NON-numeric trailing wildcard has no representable meaning and must be
	// dropped loudly rather than guessed at.
	b := parseBypass("foo.*")
	if len(b.nets)+len(b.hosts)+len(b.ips) != 0 || b.dropped != 1 {
		t.Errorf("foo.* should be dropped and counted, got %+v", b)
	}
}

// TestBypassDomainRulesCoverSubdomainsOnly: "foo.com" must match foo.com and
// api.foo.com but never notfoo.com. A bare HasSuffix — the obvious
// implementation — gets the last one wrong, and gets it wrong in the direction
// that sends traffic direct.
func TestBypassDomainRulesCoverSubdomainsOnly(t *testing.T) {
	for _, rule := range []string{"foo.com", ".foo.com", "*.foo.com"} {
		t.Run(rule, func(t *testing.T) {
			b := parseBypass(rule)
			for _, host := range []string{"foo.com", "api.foo.com", "a.b.foo.com", "FOO.COM"} {
				if !b.match(host) {
					t.Errorf("%q not matched by %q", host, rule)
				}
			}
			for _, host := range []string{"notfoo.com", "foo.com.evil.test", "barfoo.com"} {
				if b.match(host) {
					t.Errorf("%q WAS matched by %q — traffic would silently go direct", host, rule)
				}
			}
		})
	}
}

// TestBypassCIDRAndShortCIDR: macOS writes abbreviated prefixes (169.254/16)
// that netip rejects outright, so they have to be padded before parsing or the
// rule is silently lost.
func TestBypassCIDRAndShortCIDR(t *testing.T) {
	b := parseBypass("169.254/16,10.1.0.0/16")
	if !b.match("169.254.10.1") {
		t.Error("macOS short-form 169.254/16 was not honoured")
	}
	if !b.match("10.1.2.3") || b.match("10.2.2.3") {
		t.Error("a full CIDR was mis-applied")
	}
}

// TestBypassLoopbackIsAlwaysDirect: loopback bypasses ahead of every configured
// rule, matching what the standard library does unconditionally. A proxy
// carrying 127.0.0.1 would also break every loopback test server in this repo.
func TestBypassLoopbackIsAlwaysDirect(t *testing.T) {
	for _, list := range []string{"", "example.com", "<-loopback>"} {
		b := parseBypass(list)
		for _, host := range []string{"localhost", "127.0.0.1", "127.9.9.9", "::1", "[::1]"} {
			if !b.match(host) {
				t.Errorf("bypass %q did not send %q direct", list, host)
			}
		}
	}
	// <-loopback> is understood and deliberately ignored — but COUNTED, so the
	// readout can admit a rule was dropped rather than pretending it applied.
	if got := parseBypass("<-loopback>").dropped; got != 1 {
		t.Errorf("<-loopback> dropped count = %d, want 1", got)
	}
}

// TestBypassStarMeansEverything: "*" is a legitimate way to say "a proxy is
// configured but nothing goes through it".
func TestBypassStarMeansEverything(t *testing.T) {
	b := parseBypass("*")
	for _, host := range []string{"example.com", "10.0.0.1", "intranet", "2606:4700::1111"} {
		if !b.match(host) {
			t.Errorf("bypass * did not cover %q", host)
		}
	}
}

// TestBypassIsCapped: the raw string comes from the operating system and is
// externally controlled, so the rule set it can build has to be bounded
// (hard rule 4) — and the overflow has to be counted, not swallowed.
func TestBypassIsCapped(t *testing.T) {
	b := parseBypass(strings.Repeat("a.test,", maxBypassEntries+40))
	if got := len(b.hosts); got > maxBypassEntries {
		t.Errorf("parsed %d entries, cap is %d", got, maxBypassEntries)
	}
	if b.dropped == 0 {
		t.Error("entries past the cap were dropped silently — the readout must be able to say so")
	}
}

// TestBypassSeparators: Windows writes ';' and environment variables write ',';
// both are accepted unconditionally, because no real entry contains either and a
// per-platform separator parameter is one more thing to get wrong.
func TestBypassSeparators(t *testing.T) {
	for _, list := range []string{"a.test;b.test", "a.test,b.test", "a.test; b.test", "a.test ,b.test"} {
		b := parseBypass(list)
		if !b.match("a.test") || !b.match("b.test") {
			t.Errorf("list %q did not yield both entries: %+v", list, b)
		}
	}
}

// TestBypassNilIsSafe: a Policy with no bypass list at all is a normal state
// (a manual proxy has none), and the read path must not need a nil check at
// every call site.
func TestBypassNilIsSafe(t *testing.T) {
	var b *bypassList
	if !b.match("localhost") {
		t.Error("a nil bypass list must still send loopback direct")
	}
	if b.match("example.com") {
		t.Error("a nil bypass list must not bypass anything else")
	}
}
