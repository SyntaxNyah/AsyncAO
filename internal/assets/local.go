package assets

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

// LocalScheme prefixes asset "URLs" served from local mount folders in
// no-streaming mode. The origin embeds a hash of the mount set, so different
// mount configurations occupy disjoint cache keyspace — exactly like two
// asset hosts never colliding.
const LocalScheme = "local://"

// localNotFoundCacheSize / localNotFoundCacheTTL bound LocalFetcher's negative cache — the
// mount-side twin of network.Client's (network/client.go:74-77).
//
// Hard rule 6 ("never re-probe a cached 404 inside its TTL") was structurally unenforceable on the
// no-streaming path: the negative cache lives INSIDE network.Client, and Manager.netFetch routes a
// local:// URL to a LocalFetcher without ever touching it. So every re-demand of an absent asset
// re-walked the disk — and the live re-demand sites are relentless: the courtroom re-Prefetches
// Scene.DeskBase on EVERY IC message (courtroom.go setBackground/begin), which is why one missing
// background/<bg>/<pos>_overlay produced a probe (formats × spellings × mounts of os.ReadFile) every
// few seconds for a whole session. The memo makes the rule hold for BOTH byte sources, so "exactly
// one probe per asset" is a property of the Fetcher contract rather than of one implementation.
//
// The TTL is deliberately SHORTER than the network's five minutes. A mount folder is the one asset
// source the user edits mid-session — drop the missing file in and expect it to appear — so this
// trades a bounded staleness window for the guarantee. Mount-SET changes need no window at all:
// NewLocalFetcher hashes the mount list into the origin, so attaching or detaching a folder mints a
// new origin, a disjoint keyspace, and an empty memo (see localSourceFor).
//
// The memo is missMemo, not the expirable.LRU network.Client uses, and that is a HARD requirement
// rather than a preference: golang-lru v2.0.7 starts an unstoppable janitor goroutine per instance
// and a LocalFetcher is minted many times per session. See missmemo.go for the measurement.
const (
	localNotFoundCacheSize = 2048
	localNotFoundCacheTTL  = 30 * time.Second
)

// LocalFetcher serves asset bytes from user-chosen mount folders instead of
// the network: the "Legacy support for servers without an asset server"
// checkbox. Mounts are searched in order, first hit wins (AO2-Client mount
// path semantics — any folder layout, not just a default /base).
//
// It satisfies the manager's Fetcher contract; a file missing from every
// mount maps to network.ErrAssetNotFound so learned formats, fallback
// chains, and missing-asset warnings behave identically to streaming — and,
// like the streaming source, a conclusive miss is remembered rather than
// re-probed (notFound, see the consts above).
type LocalFetcher struct {
	mounts []string
	origin string
	// notFound memoizes conclusive misses (rule 6). missMemo is mutex-guarded,
	// which this needs (Fetch runs on pool workers), and janitor-free, which the
	// construction rate needs.
	notFound *missMemo
}

// NewLocalFetcher roots a fetcher at the given ordered mount folders (each
// containing characters/, background/, sounds/, ...). Empty entries are
// dropped.
func NewLocalFetcher(mounts []string) *LocalFetcher {
	return newLocalFetcher(mounts, localNotFoundCacheTTL)
}

// newLocalFetcher lets tests shrink the negative-cache TTL (network.newClient's
// idiom). Unexported on purpose: production has exactly one TTL, so no caller
// outside this package can weaken or disable rule 6's memo.
func newLocalFetcher(mounts []string, notFoundTTL time.Duration) *LocalFetcher {
	cleaned := cleanMounts(mounts)
	return &LocalFetcher{
		mounts:   cleaned,
		origin:   localOriginOf(cleaned),
		notFound: newMissMemo(localNotFoundCacheSize, notFoundTTL),
	}
}

// LocalOriginFor returns the local:// origin a mount set resolves against —
// byte-for-byte what NewLocalFetcher(mounts).BaseURL() returns — WITHOUT minting
// a fetcher.
//
// Every caller that wants the LABEL rather than the bytes must use this. Minting
// a fetcher for a label was the old idiom (NewMountLayer, ui/demofile.go
// mountOrigin) and it is not free: a fetcher carries the rule-6 negative memo,
// and NewMountLayer runs on EVERY tab activation (ui/tabs.go activateTab defers
// applyMountLayer → publishMountLayer, "the cheap origins-only transition").
// Under the old expirable.LRU memo that was a permanent goroutine per tab switch;
// the memo is janitor-free now, but a label still has no business allocating one.
func LocalOriginFor(mounts []string) string {
	return localOriginOf(cleanMounts(mounts))
}

// cleanMounts is the ONE normalization the origin hash is taken over: empty
// entries dropped, every survivor filepath.Clean'd, order preserved. Shared so
// LocalOriginFor and the fetcher can never disagree about what a mount set IS.
func cleanMounts(mounts []string) []string {
	cleaned := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m == "" {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(m))
	}
	return cleaned
}

// localOriginOf builds the origin from an ALREADY-cleaned mount list. The origin
// identifies this exact mount set (order included) in cache keys and the
// learned-format table.
func localOriginOf(cleaned []string) string {
	return LocalScheme + "m-" + cache.Key(strings.Join(cleaned, string(os.PathListSeparator))) + "/"
}

// BaseURL returns the origin bases are built from: local://m-<mountset>/.
// Courtroom URL building appends the same relative paths it would append to
// an http asset URL.
func (l *LocalFetcher) BaseURL() string {
	return l.origin
}

// Mounts returns the resolved mount list (Settings UI display).
func (l *LocalFetcher) Mounts() []string {
	out := make([]string, len(l.mounts))
	copy(out, l.mounts)
	return out
}

// Fetch reads the file addressed by a local:// URL, trying each mount in
// order. The context is accepted for interface symmetry; local reads are not
// cancellable.
//
// The URLBuilder percent-escapes every path segment regardless of origin (it
// builds http URLs), so a mounted pack named "Phoenix Wright" arrives here as
// "phoenix%20wright". We try the RAW rel first (exported scene archives write
// escaped names symmetrically and must keep resolving byte-for-byte), then a
// percent-DECODED rel so real on-disk names with spaces/parens resolve too.
//
// A miss on BOTH spellings across every mount is conclusive, and conclusive
// misses are memoized for localNotFoundCacheTTL: hard rule 6 applies to this
// byte source exactly as it does to the streaming one.
func (l *LocalFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	rel, ok := strings.CutPrefix(rawURL, l.origin)
	if !ok {
		return nil, fmt.Errorf("assets: %q is not under local origin %q", rawURL, l.origin)
	}
	if len(l.mounts) == 0 {
		return nil, fmt.Errorf("%w: no local mount folders configured", network.ErrAssetNotFound)
	}
	// Rule 6: a remembered miss answers without touching the disk again. Keyed by
	// the FULL URL (the client-wide cache-key convention); the origin it embeds is
	// the mount-set generation, so a mount change can never read a stale entry.
	if l.notFound.has(rawURL) {
		return nil, fmt.Errorf("%w: %s (missing, negative-cached)", network.ErrAssetNotFound, rel)
	}
	// Attempt 1: the rel verbatim (escaped names written by the exporter).
	if data, err := l.readRel(rel); err != nil {
		return nil, err // hard I/O error (not just "missing") — surface it
	} else if data != nil {
		return data, nil
	}
	// Attempt 2: percent-decoded, segment by segment the way the URL was BUILT, so
	// real on-disk names with spaces and parens resolve. A malformed escape (%zz)
	// is not a real filename — skip the decoded attempt rather than fail the fetch.
	//
	// Note what this deliberately TOLERATES, measured rather than assumed:
	// url.PathUnescape decodes "%2F" to "/", so a segment holding one re-joins as a
	// real separator here. That is why a nested character identity escaped as a
	// single segment ("characters/sg%2Ffaris%20nyannyan/…") still resolved against a
	// mount while 404ing on any origin that does not decode encoded slashes — the
	// URL spelling was the defect, not this fetcher, and the builder now mints real
	// separators (courtroom charSeg). The tolerance stays: an archive exported
	// before that fix must keep replaying byte-for-byte.
	if dec, ok := decodeRel(rel); ok && dec != rel {
		if data, err := l.readRel(dec); err != nil {
			return nil, err
		} else if data != nil {
			return data, nil
		}
	}
	// Both spellings missed in every mount: conclusive. Remember it so the next
	// re-demand (the per-message desk/background Prefetch, the scene-warm keeper,
	// a fallback chain re-walking the same base) costs nothing. Only a MISS is
	// memoized — a hard I/O error returned above is not conclusive and must stay
	// re-tryable.
	l.notFound.add(rawURL)
	return nil, fmt.Errorf("%w: %s (searched %d mounts)", network.ErrAssetNotFound, rel, len(l.mounts))
}

// readRel searches every mount for rel, returning (bytes,nil) on the first
// non-empty hit, (nil,nil) when rel is missing everywhere (so the caller can
// try another spelling), and (nil,err) on a real I/O error or a path escape.
// The escape guard runs HERE so it re-checks the DECODED rel too — a "%2e%2e"
// must not slip past by decoding into ".." afterwards.
//
// It is safepath's predicate, not a private one: this was the third copy of the
// same boundary in the tree and the only one that missed absolute paths and
// Windows drive-relative names.
func (l *LocalFetcher) readRel(rel string) ([]byte, error) {
	if safepath.UnsafeRel(rel) {
		return nil, fmt.Errorf("assets: refusing path escape %q", rel)
	}
	for _, mount := range l.mounts {
		p, err := safepath.Join(mount, rel)
		// Defence in depth, and unreachable today: Join refuses exactly what
		// UnsafeRel refuses, and that ran above. Kept so this loop stays correct if
		// the guard above is ever moved or relaxed.
		if err != nil {
			return nil, fmt.Errorf("assets: refusing path escape %q", rel)
		}
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return data, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("assets: reading local asset %s: %w", p, err)
		}
	}
	return nil, nil
}

// decodeRel percent-decodes each '/'-separated segment of rel. ok=false when
// any segment holds a malformed escape (it is not a real filename, so the
// caller should not attempt it).
func decodeRel(rel string) (string, bool) {
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		dec, err := url.PathUnescape(part)
		if err != nil {
			return "", false
		}
		parts[i] = dec
	}
	return strings.Join(parts, "/"), true
}
