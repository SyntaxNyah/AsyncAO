package platform

import (
	"net/netip"
	"strings"
)

// maxBypassEntries bounds the match loop (hard rule 4), as the list comes from outside the client.
const maxBypassEntries = 128

// bitsPerOctet converts a count of IPv4 labels to a prefix length.
const bitsPerOctet = 8

// ProxyBypass is the destinations the OS sends direct, immutable once parsed. A nil list still sends loopback direct.
type ProxyBypass struct {
	// all is the `*` rule.
	all bool
	// noDot is the Windows `<local>` macro: bypass any host name with no period.
	noDot bool
	// hosts match themselves and subdomains, never notfoo.com; lowercased at parse so matching doesn't allocate.
	hosts []string
	nets  []netip.Prefix
	ips   []netip.Addr
}

// ParseProxyBypass accepts both separators: Windows writes `;`, environment variables and macOS write `,`. It returns
// nil for an empty list.
func ParseProxyBypass(raw string) *ProxyBypass {
	entries := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	if len(entries) == 0 {
		return nil
	}
	b := &ProxyBypass{}
	for _, entry := range entries[:min(len(entries), maxBypassEntries)] {
		if entry = strings.ToLower(strings.TrimSpace(entry)); entry != "" {
			b.add(entry)
		}
	}
	return b
}

func (b *ProxyBypass) add(entry string) {
	switch entry {
	case "<local>":
		b.noDot = true
		return
	case "<-loopback>":
		// Loopback is always direct (see Match).
		return
	case "*":
		b.all = true
		return
	}
	// A CIDR, including macOS's short form (169.254/16), which netip rejects.
	if strings.IndexByte(entry, '/') >= 0 {
		if p, ok := parsePrefixLoose(entry); ok {
			b.nets = append(b.nets, p)
		}
		return
	}
	// `10.*`, `192.168.1.*`: the label count before the star gives the mask (/8, /16, /24). A non-numeric wildcard
	// ("foo.*") would mean every TLD, so it is skipped.
	if strings.HasSuffix(entry, ".*") {
		if p, ok := octetWildcard(strings.TrimSuffix(entry, ".*")); ok {
			b.nets = append(b.nets, p)
		}
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

// Match reports whether host (bare, no port or brackets) goes direct. Loopback always does, because a proxy can't reach
// this machine's loopback on our behalf.
func (b *ProxyBypass) Match(host string) bool {
	if host == "" {
		return false
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	// ParseAddr allocates an error on a hostname, so this runs once per dial and never on a render or decode path.
	addr, err := netip.ParseAddr(host)
	isIP := err == nil
	if isIP && addr.IsLoopback() {
		return true
	}
	if b == nil {
		return false
	}
	if b.all {
		return true
	}
	if isIP {
		return b.matchAddr(addr)
	}
	if b.noDot && strings.IndexByte(host, '.') < 0 {
		return true
	}
	for _, h := range b.hosts {
		if host == h || (len(host) > len(h) && strings.HasSuffix(host, h) && host[len(host)-len(h)-1] == '.') {
			return true
		}
	}
	return false
}

func (b *ProxyBypass) matchAddr(addr netip.Addr) bool {
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
	return false
}
