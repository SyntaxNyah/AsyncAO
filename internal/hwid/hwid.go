// Package hwid derives AsyncAO's HDID — the stable, hard-to-spoof device
// identifier that AO servers key bans on (sent in the HI and CC packets).
//
// It combines every machine/account identity root the OS exposes (per-OS
// platform code in roots()), salted-SHA-256 hashes them so no raw hardware value
// crosses the wire, and degrades gracefully: an unreadable root is skipped, and
// if NONE are readable it falls back to the hostname — never to a shared
// constant, which would collide every locked-down machine into one ban.
//
// The id is then bound to ONE server: what a server receives is
// sha256(server address + device hash), so the same PC shows a different HDID
// on every server it joins. See For.
//
// Design notes (why this shape):
//   - Roots are per-OS-INSTALL / per-ACCOUNT and not user-settable in any UI: a
//     Windows account SID, the MachineGuid, /etc/machine-id, the macOS hardware
//     UUID. Renaming the PC (the old hostname hash's weakness) no longer mints a
//     new identity — only a new account / OS reinstall / hardware swap does.
//   - It is an EXACT hash of whatever roots are present, with NO fuzzy matching:
//     change real hardware and you get a brand-new, unrelated id, so a genuine
//     hardware change can never be false-flagged against an existing ban.
//   - PER-SERVER BINDING closes the replay hole a shared id leaves open. With one
//     id for every server (the AO norm, and what this package used to send), any
//     operator — or anyone who reads a server's ban table or its unencrypted
//     traffic — holds a value that IS you on every other server: they can wear it
//     to get you banned somewhere you have never been, and they can correlate you
//     across servers that have no other link. Mixing the server address into the
//     hash makes a harvested id inert anywhere but the server it came from.
//     The cost is deliberate: HDID bans no longer follow a device between
//     servers. They were never shared in practice (no AO server family syncs ban
//     tables), so this gives up a theoretical reach for a real defence.
//   - The device hash NEVER leaves this package. device() is unexported and For
//     is the only exported id, so no call site can put a cross-server value on
//     the wire — the property above is a compile error to break, not a promise.
//   - Editing the source to spoof buys nothing: identity comes from RUNTIME
//     reads, not editable constants. Changing the salt only re-rolls the editor
//     into a fresh id (caught server-side via IP/stability); it cannot forge a
//     specific victim's id.
//
// device() reads the OS roots once per process and memoises; For hashes on the
// connect path (cold, once per server join), so nothing here touches the
// render/hot path and it adds zero per-frame cost.
package hwid

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
)

const (
	// salt namespaces AsyncAO HDIDs and versions the scheme (bump the suffix to
	// rotate every device id at once). Constant on purpose — see the package doc.
	// v2 = per-server binding; every v1 id rotated when For replaced the shared id.
	salt = "AsyncAO-HDID-v2\x00"
	// idPrefix keeps the HDID recognisably ours and lets a server sanity-check
	// its shape (prefix + 64 lowercase hex).
	idPrefix = "asyncao-"
	// fieldSep joins identity roots before hashing; \x1f (unit separator) cannot
	// appear in any root value, so distinct root sets cannot collide.
	fieldSep = "\x1f"
	// serverLabel and deviceLabel name the two halves of a per-server id so the
	// hashed string reads as a pair of labelled fields, and so a server address
	// can never be mistaken for a device hash (or the reverse) by the hash input.
	serverLabel = "server:"
	deviceLabel = "hdid:"
	// genericFallback is the absolute last resort: no root readable AND no
	// hostname. Constant, but only reachable on a machine that exposes nothing.
	genericFallback = "fallback=asyncao"
	// wsScheme / wssScheme are stripped when reducing an address to a server key:
	// the same server reached over ws:// and wss:// is one server, not two.
	wsScheme  = "ws://"
	wssScheme = "wss://"
)

var (
	once   sync.Once
	cached string
)

// For returns the HDID to send to the server at address (the ws:// or wss:// URL
// this client dialled). It is stable for that server and unrelated to the id
// every other server sees, so a value harvested on one server cannot be worn on
// another.
//
// It hashes on each call rather than memoising per server: connecting is a cold
// path (once per join), and a keyed cache would be an unbounded map of every
// address a long session ever touched.
func For(address string) string {
	sum := sha256.Sum256([]byte(salt + serverLabel + serverKey(address) + fieldSep + deviceLabel + device()))
	return idPrefix + hex.EncodeToString(sum[:])
}

// serverKey reduces a connection address to the server it names, so that the
// same server always mints the same HDID however the address was spelled. Bans
// have to stick: without this, joining through wss:// after a ws:// ban — or
// through a differently-cased hostname — would hand the server a fresh id and
// evade the ban by accident.
//
// AO WebSocket URLs are scheme + host + port with no path (network.splitWSURL
// enforces that), so lowercasing the whole remainder is safe: hostnames are
// case-insensitive and a port is digits. A host that resolves to the same server
// under two DIFFERENT names still yields two ids — an inherent limit of keying on
// the address, and the address is the only server identity a hostile server
// cannot choose for itself (a name it advertises, it can).
func serverKey(address string) string {
	s := strings.ToLower(strings.TrimSpace(address))
	if rest, ok := strings.CutPrefix(s, wssScheme); ok {
		s = rest
	} else if rest, ok := strings.CutPrefix(s, wsScheme); ok {
		s = rest
	}
	return strings.TrimSuffix(s, "/")
}

// device returns the salted hash of this machine's identity roots, computed once
// per process. It is the FUEL for an HDID and never an HDID itself: it stays
// unexported so that the only id any caller can obtain is bound to one server.
func device() string {
	once.Do(func() { cached = compute() })
	return cached
}

func compute() string {
	parts := roots() // platform-specific, strongest first
	if len(parts) == 0 {
		// No stable root readable — fall back to the hostname so two such machines
		// still differ. This is the ONLY case where a rename moves the id; on any
		// normal machine the roots carry identity and a rename is invisible.
		if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
			parts = []string{"host=" + strings.TrimSpace(h)}
		} else {
			parts = []string{genericFallback}
		}
	}
	sum := sha256.Sum256([]byte(salt + strings.Join(parts, fieldSep)))
	return hex.EncodeToString(sum[:])
}
