package netproxy

import (
	"net/netip"
	"strings"
)

// A permissive mistake in the bypass list is a privacy leak: traffic the user believes is proxied goes direct.

// maxBypassEntries bounds the match loop (hard rule 4); extra entries count into dropped for the readout.
const maxBypassEntries = 128

// bitsPerOctet converts a count of IPv4 labels to a prefix length.
const bitsPerOctet = 8

// bypassList is a "these hosts go direct" rule set, immutable once published in a Policy.
type bypassList struct {
	// noDot is the Windows `<local>` macro: bypass any host name with no period.
	noDot bool
	// hosts match themselves and subdomains, never notfoo.com; lowercased at parse so matching doesn't allocate.
	hosts []string
	nets  []netip.Prefix
	ips   []netip.Addr
	// dropped counts rules we can't represent, shown because that traffic goes where the user didn't expect.
	dropped int
}

// parseBypass accepts both separators: Windows writes `;`, environment variables and macOS write `,`.
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
		b.noDot = true
		return
	case "<-loopback>":
		// Deliberately ignored: loopback is always direct (see match), as a proxy would break local test servers.
		// Counted so the readout admits it.
		b.dropped++
		return
	case "*":
		// "Bypass everything": configured, but off.
		b.nets = append(b.nets, netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0"))
		b.noDot = true
		b.hosts = append(b.hosts, "")
		return
	}
	// A CIDR, including macOS's short form (169.254/16), which netip rejects.
	if slash := strings.IndexByte(entry, '/'); slash >= 0 {
		if p, ok := parsePrefixLoose(entry); ok {
			b.nets = append(b.nets, p)
		} else {
			b.dropped++
		}
		return
	}
	// `10.*`, `192.168.1.*`: the label count before the star gives the mask (/8, /16, /24).
	if strings.HasSuffix(entry, ".*") {
		if p, ok := octetWildcard(strings.TrimSuffix(entry, ".*")); ok {
			b.nets = append(b.nets, p)
			return
		}
		// A non-numeric wildcard ("foo.*") would mean every TLD, so it is dropped.
		b.dropped++
		return
	}
	if addr, err := netip.ParseAddr(strings.Trim(entry, "[]")); err == nil {
		b.ips = append(b.ips, addr)
		return
	}
	// `*.foo.com`, `.foo.com` and `foo.com` all mean the domain and everything under it.
	b.hosts = append(b.hosts, strings.TrimPrefix(strings.TrimPrefix(entry, "*"), "."))
}

func parsePrefixLoose(entry string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(entry); err == nil {
		return p.Masked(), true
	}
	addr, bits, ok := strings.Cut(entry, "/")
	if !ok || strings.Contains(addr, ":") {
		return netip.Prefix{}, false
	}
	for n := strings.Count(addr, ".") + 1; n < 4; n++ {
		addr += ".0"
	}
	p, err := netip.ParsePrefix(addr + "/" + bits)
	if err != nil {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

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
	return netip.PrefixFrom(addr, bitsPerOctet*len(labels)).Masked(), true
}

// count is how many rules are in force, for the readout.
func (b *bypassList) count() int {
	if b == nil {
		return 0
	}
	n := len(b.hosts) + len(b.nets) + len(b.ips)
	if b.noDot {
		n++
	}
	return n
}

// match reports whether host (bare, no port or brackets) goes direct. Loopback is always direct, as in the
// standard library, because a proxy carrying 127.0.0.1 would break local test servers.
func (b *bypassList) match(host string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	// ParseAddr allocates an error on a hostname, so this runs once per dial and never on a render or decode path.
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
		// An IP literal can never satisfy a name rule.
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
