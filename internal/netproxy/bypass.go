package netproxy

import (
	"net/netip"
	"strings"
)

// The bypass list is the only thing standing between "proxy everything" and
// breaking someone's LAN AO server, and getting it wrong in the permissive
// direction is a privacy leak: traffic the user believes is proxied goes direct.
//
// This is hand-written rather than delegated to golang.org/x/net/http/httpproxy,
// and that is not a not-invented-here choice. Two reasons, in order:
//
//  1. It is measurably WRONG for this job. Fed a real Windows ProxyOverride —
//     `<local>;localhost;127.*;10.*;172.16.*;192.168.*` — through the `;`→`,`
//     translation every off-the-shelf library ships, five of five rules leaked.
//     `<local>` is inexpressible there at all, and `10.*` / `172.16.*` /
//     `192.168.*` can NEVER match an IP literal, because a non-IP entry becomes
//     a domain match and a domain match declines every address.
//  2. net/http VENDORS httpproxy; the copy is not importable from outside the
//     standard library, so using it means a new module for a matcher we would
//     have to wrap and correct anyway.

// maxBypassEntries caps how many rules one list can contribute. The raw string
// comes from the operating system's configuration — a registry value, an
// environment variable — so it is externally controlled and could be arbitrarily
// long. 128 is far past any real configuration (the largest observed is under
// 20) and keeps the match loop bounded (hard rule 4). Entries past the cap are
// counted into dropped, so the Settings readout can say so rather than silently
// matching less than the user configured.
const maxBypassEntries = 128

// bypassList is a parsed "these hosts go direct" rule set. Immutable once
// built: it is published inside a Policy snapshot and read from every dial.
type bypassList struct {
	// noDot is the Windows `<local>` macro — bypass any host name containing no
	// period. Microsoft documents it exactly that way, and it is the single most
	// common entry in a real corporate configuration, because it is what makes
	// `http://intranet/` resolve inside the network.
	noDot bool
	// hosts match themselves AND their subdomains: "foo.com" matches foo.com and
	// api.foo.com, but never notfoo.com. Lowercased at parse time so matching
	// never allocates a folded copy.
	hosts []string
	nets  []netip.Prefix
	ips   []netip.Addr
	// dropped counts entries we understood the shape of but cannot represent.
	// Surfaced in the readout: a user whose rule was ignored should be told,
	// because the consequence is their traffic going somewhere they did not
	// expect.
	dropped int
}

// parseBypass builds a matcher from an OS-supplied bypass string. Both
// separators are accepted unconditionally — Windows writes `;`, environment
// variables and macOS write `,`, and no real entry contains either character, so
// splitting on both costs nothing and removes a per-platform parameter that
// would only ever be got wrong.
func parseBypass(raw string) *bypassList {
	b := &bypassList{}
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' }) {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if len(b.hosts)+len(b.nets)+len(b.ips) >= maxBypassEntries {
			b.dropped++
			continue
		}
		b.add(entry)
	}
	return b
}

func (b *bypassList) add(entry string) {
	switch entry {
	case "<local>":
		// Windows' macro for "anything without a dot is inside the network".
		b.noDot = true
		return
	case "<-loopback>":
		// The inverse macro: "DO send loopback through the proxy". Deliberately
		// ignored rather than implemented — loopback is always direct here (see
		// match), because a proxy that carried 127.0.0.1 would break the local
		// test servers this client is developed against, and no AsyncAO user has
		// a reason to want it. Counted so the readout admits it was ignored.
		b.dropped++
		return
	case "*":
		// "Bypass everything" — a legitimate way to say "configured, but off".
		b.nets = append(b.nets, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0"))
		b.noDot = true
		b.hosts = append(b.hosts, "")
		return
	}
	// A CIDR, including macOS's short form (169.254/16 rather than
	// 169.254.0.0/16) which netip rejects outright.
	if slash := strings.IndexByte(entry, '/'); slash >= 0 {
		if p, ok := parsePrefixLoose(entry); ok {
			b.nets = append(b.nets, p)
		} else {
			b.dropped++
		}
		return
	}
	// `10.*`, `172.16.*`, `192.168.1.*` — a whole-octet wildcard. The label count
	// before the star gives the mask: one label is a /8, two a /16, three a /24.
	// This is the case every off-the-shelf translation drops.
	if strings.HasSuffix(entry, ".*") {
		if p, ok := octetWildcard(strings.TrimSuffix(entry, ".*")); ok {
			b.nets = append(b.nets, p)
			return
		}
		// A non-numeric trailing wildcard ("foo.*") has no meaning we can
		// represent — it would mean "every TLD" — so it is dropped, loudly.
		b.dropped++
		return
	}
	if addr, err := netip.ParseAddr(strings.Trim(entry, "[]")); err == nil {
		b.ips = append(b.ips, addr)
		return
	}
	// `*.foo.com`, `.foo.com` and `foo.com` all mean the same thing to every
	// implementation that matters: the domain and everything under it.
	b.hosts = append(b.hosts, strings.TrimPrefix(strings.TrimPrefix(entry, "*"), "."))
}

// parsePrefixLoose accepts both `10.0.0.0/8` and macOS's abbreviated `169.254/16`.
func parsePrefixLoose(entry string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(entry); err == nil {
		return p.Masked(), true
	}
	addr, bits, ok := strings.Cut(entry, "/")
	if !ok || strings.Contains(addr, ":") {
		return netip.Prefix{}, false
	}
	// Pad the abbreviated address out to four octets and retry.
	for n := strings.Count(addr, ".") + 1; n < 4; n++ {
		addr += ".0"
	}
	p, err := netip.ParsePrefix(addr + "/" + bits)
	if err != nil {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

// octetWildcard turns the numeric prefix of an `N.N.*` rule into a CIDR.
func octetWildcard(prefix string) (netip.Prefix, bool) {
	labels := strings.Split(prefix, ".")
	if len(labels) < 1 || len(labels) > 3 {
		return netip.Prefix{}, false
	}
	padded := prefix
	for n := len(labels); n < 4; n++ {
		padded += ".0"
	}
	addr, err := netip.ParseAddr(padded)
	if err != nil || !addr.Is4() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, 8*len(labels)).Masked(), true
}

// match reports whether host goes DIRECT. host is a bare hostname or IP literal
// with no port and no brackets.
//
// Loopback is always direct, ahead of every configured rule. That mirrors what
// the standard library does unconditionally, so AsyncAO can never contradict the
// rest of the Go ecosystem about localhost — and a proxy carrying 127.0.0.1
// would break the loopback test servers this client is developed against.
func (b *bypassList) match(host string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	// The IP path. ParseAddr on a hostname allocates an error, so this runs once
	// per DIAL and never on a render or decode path; the connection it is about
	// to make costs orders of magnitude more.
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.IsLoopback() {
			return true
		}
		if b == nil {
			return false
		}
		for _, ip := range b.ips {
			if ip == addr {
				return true
			}
		}
		for _, n := range b.nets {
			if n.Contains(addr) {
				return true
			}
		}
		// An IP literal can never satisfy a name rule, and the whole reason this
		// matcher exists is that the obvious library silently pretends otherwise.
		return false
	}
	if b == nil {
		return false
	}
	if b.noDot && strings.IndexByte(host, '.') < 0 {
		return true
	}
	for _, h := range b.hosts {
		if h == "" { // the `*` rule
			return true
		}
		if host == h {
			return true
		}
		if len(host) > len(h) && strings.HasSuffix(host, h) && host[len(host)-len(h)-1] == '.' {
			return true
		}
	}
	return false
}
