// Package hwid derives AsyncAO's HDID — the stable, hard-to-spoof device
// identifier that AO servers key bans on (sent in the HI and CC packets).
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
func For(address string) string {
	sum := sha256.Sum256([]byte(salt + serverLabel + serverKey(address) + fieldSep + deviceLabel + device()))
	return idPrefix + hex.EncodeToString(sum[:])
}

// serverKey reduces a connection address to the server it names, so that the
// same server always mints the same HDID however the address was spelled. Bans
// have to stick: without this, joining through wss:// after a ws:// ban — or
// through a differently-cased hostname — would hand the server a fresh id and
// evade the ban by accident.
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
