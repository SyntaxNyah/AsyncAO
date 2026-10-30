// Package hwid derives AsyncAO's HDID, the stable device identifier AO servers key bans on (sent in HI and CC).
package hwid

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
)

const (
	// salt namespaces the HDIDs and versions the scheme; bumping the suffix rotates every device id at once.
	salt = "AsyncAO-HDID-v2\x00"
	// idPrefix lets a server check the shape: prefix plus 64 lowercase hex.
	idPrefix = "asyncao-"
	// fieldSep is the unit separator, which can't appear in a root value, so distinct root sets can't collide.
	fieldSep = "\x1f"
	// serverLabel and deviceLabel keep a server address from being mistaken for a device hash in the hash input.
	serverLabel = "server:"
	deviceLabel = "hdid:"
	// genericFallback is the last resort, reached only when no root and no hostname are readable.
	genericFallback = "fallback=asyncao"
	// wsScheme and wssScheme are stripped: the same server over ws:// and wss:// is one server.
	wsScheme  = "ws://"
	wssScheme = "wss://"
)

var (
	once   sync.Once
	cached string
)

// For returns the HDID for address, unrelated to other servers' ids, so a harvested one can't be worn elsewhere.
// It hashes every call, because a per-server cache would be an unbounded map.
func For(address string) string {
	sum := sha256.Sum256([]byte(salt + serverLabel + serverKey(address) + fieldSep + deviceLabel + device()))
	return idPrefix + hex.EncodeToString(sum[:])
}

// serverKey makes bans stick across ws/wss and hostname case; AO URLs have no path (network.splitWSURL), so
// lowercasing is safe. Two names for one host still give two ids, a limit of keying on the address.
func serverKey(address string) string {
	s := strings.ToLower(strings.TrimSpace(address))
	if rest, ok := strings.CutPrefix(s, wssScheme); ok {
		s = rest
	} else if rest, ok := strings.CutPrefix(s, wsScheme); ok {
		s = rest
	}
	return strings.TrimSuffix(s, "/")
}

// device is computed once and unexported, so the only id callers get is bound to one server.
func device() string {
	once.Do(func() { cached = compute() })
	return cached
}

func compute() string {
	parts := roots() // platform-specific, strongest first
	if len(parts) == 0 {
		// No stable root: use the hostname so such machines still differ; only here does a rename move the id.
		if h, err := os.Hostname(); err == nil && strings.TrimSpace(h) != "" {
			parts = []string{"host=" + strings.TrimSpace(h)}
		} else {
			parts = []string{genericFallback}
		}
	}
	sum := sha256.Sum256([]byte(salt + strings.Join(parts, fieldSep)))
	return hex.EncodeToString(sum[:])
}
