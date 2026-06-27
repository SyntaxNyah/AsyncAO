// Package hwid derives AsyncAO's HDID — the stable, hard-to-spoof device
// identifier that AO servers key bans on (sent in the HI and CC packets).
//
// It combines every machine/account identity root the OS exposes (per-OS
// platform code in roots()), salted-SHA-256 hashes them so no raw hardware value
// crosses the wire, and degrades gracefully: an unreadable root is skipped, and
// if NONE are readable it falls back to the hostname — never to a shared
// constant, which would collide every locked-down machine into one ban.
//
// Design notes (why this shape):
//   - Roots are per-OS-INSTALL / per-ACCOUNT and not user-settable in any UI: a
//     Windows account SID, the MachineGuid, /etc/machine-id, the macOS hardware
//     UUID. Renaming the PC (the old hostname hash's weakness) no longer mints a
//     new identity — only a new account / OS reinstall / hardware swap does.
//   - It is an EXACT hash of whatever roots are present, with NO fuzzy matching:
//     change real hardware and you get a brand-new, unrelated id, so a genuine
//     hardware change can never be false-flagged against an existing ban.
//   - The salt is a fixed constant (not per-server) so a ban placed on one
//     server still matches the same device elsewhere — the AO norm. The id is
//     therefore the same everywhere: hashed, but a stable pseudonym, not anonymity.
//   - Editing the source to spoof buys nothing: identity comes from RUNTIME
//     reads, not editable constants. Changing the salt only re-rolls the editor
//     into a fresh id (caught server-side via IP/stability); it cannot forge a
//     specific victim's id.
//
// Compute is called once at connect time (cold path) and memoised, so it never
// touches the render/hot path and adds zero per-frame cost.
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
	salt = "AsyncAO-HDID-v1\x00"
	// idPrefix keeps the HDID recognisably ours and lets a server sanity-check
	// its shape (prefix + 64 lowercase hex).
	idPrefix = "asyncao-"
	// fieldSep joins identity roots before hashing; \x1f (unit separator) cannot
	// appear in any root value, so distinct root sets cannot collide.
	fieldSep = "\x1f"
	// genericFallback is the absolute last resort: no root readable AND no
	// hostname. Constant, but only reachable on a machine that exposes nothing.
	genericFallback = "fallback=asyncao"
)

var (
	once   sync.Once
	cached string
)

// Compute returns this machine's HDID, computing it once per process.
func Compute() string {
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
	return idPrefix + hex.EncodeToString(sum[:])
}
