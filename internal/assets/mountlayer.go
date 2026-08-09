package assets

// MountLayer is the immutable wrapper the Manager consults on the fetch path: a
// MountIndex plus the set of asset origins whose URLs it may answer for.
//
// INDEX AND ORIGINS ARE SEPARATE ON PURPOSE. The index's contents are
// origin-independent (it holds rels), while the origins change whenever a tab
// connects or is activated. Baking the origin into the index would mean
// re-walking every mount on every origin change AND would silently limit the
// pack to a single tab, because the URL builder is per-tab session state and
// nothing re-derives it on a tab switch. Swapping tabs republishes this cheap
// wrapper and REUSES the index pointer.
//
// A pack transport URL — LocalOrigin() + <folded rel> — is a LABEL, not a
// fetchable address. Bytes are read through MountIndex.ReadFile, which holds the
// exact on-disk name; netFetch is never called with one. Its only consumers are
// DecodedAsset.URL and AudioAsset.URL, i.e. the T2 key and the quarantine round
// trip. That is why it may safely carry the decoded, lowercased folded rel.

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// mountLayerOriginCap bounds the registered origin list (rule 4). Sized off the
// tab ceiling rather than a hand-picked number: one origin per open tab is the
// worst case, and origins are DEDUPED first because several tabs on one server
// share an origin, which is the common case. A hand-picked smaller cap would
// silently leave a power user's excess tabs unlayered while Settings still
// reported a healthy index — the exact silent-inert failure this design keeps
// trying to avoid.
const mountLayerOriginCap = config.MaxMultiTabCap

// MountLayer is described above. Every field is read-only after construction.
type MountLayer struct {
	idx         *MountIndex
	origins     []string
	localOrigin string
}

// NewMountLayer wraps idx for the given asset origins. Origins MUST be the
// normalized values the URL builder actually prepends (its Origin(), never a raw
// server-supplied asset URL): the ASS field is stored verbatim and often omits
// its trailing slash, and an unnormalized origin makes RelOf hand back a rel with
// a leading slash that can never match an index key — leaving the pack inert
// while Settings cheerfully reports thousands of indexed files.
//
// mounts must be the SAME list idx was built from; the local origin is taken
// from the canonical LocalOriginFor helper rather than recomputed, so the pack's
// transport URLs land in exactly the keyspace the rest of the client expects.
// It is the ORIGIN helper and not NewLocalFetcher(mounts).BaseURL() because this
// constructor runs on every tab activation and only ever wanted the label — a
// fetcher is a byte source with a negative memo attached, and minting one per tab
// switch to read a string off it is exactly how a per-instance janitor goroutine
// became a per-tab-switch leak.
func NewMountLayer(idx *MountIndex, mounts []string, origins []string) *MountLayer {
	l := &MountLayer{idx: idx, localOrigin: LocalOriginFor(mounts)}
	seen := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if o == "" {
			continue
		}
		if _, dup := seen[o]; dup {
			continue
		}
		seen[o] = struct{}{}
		l.origins = append(l.origins, o)
		if len(l.origins) == mountLayerOriginCap {
			break
		}
	}
	return l
}

// Index returns the wrapped index (nil-safe).
func (l *MountLayer) Index() *MountIndex {
	if l == nil {
		return nil
	}
	return l.idx
}

// LocalOrigin is the pack transport-URL prefix.
func (l *MountLayer) LocalOrigin() string { return l.localOrigin }

// Origins returns the registered asset origins (test/diagnostic use).
func (l *MountLayer) Origins() []string { return l.origins }

// RelOf strips whichever registered origin url sits under, returning the
// origin-relative path. Zero allocation — the result is a substring.
//
// The LONGEST match wins: two servers can share a host while differing in their
// base path, and the shorter origin would otherwise claim the other's URLs and
// hand back a rel carrying the extra path segment.
func (l *MountLayer) RelOf(url string) (string, bool) {
	best, bestLen := "", -1
	for _, o := range l.origins {
		if len(o) > bestLen && strings.HasPrefix(url, o) {
			best, bestLen = url[len(o):], len(o)
		}
	}
	if bestLen < 0 {
		return "", false
	}
	return best, true
}

// LocalURLFor builds the pack transport URL for an INDEX KEY. The parameter is
// the folded rel, never the URL rel and never the exact on-disk name — see the
// one-key-space rule on MountIndex.
func (l *MountLayer) LocalURLFor(foldedRel string) string { return l.localOrigin + foldedRel }

// PackRelOf is the exact inverse of LocalURLFor: substring, zero allocation, no
// re-folding.
//
// It deliberately does NOT test for the local:// scheme generally. An archive
// replay's origin and a mount overlay's origin are also local:// but carry a
// different mount-set hash, and cutting against THIS layer's origin rejects them
// for free — which is what keeps a replay's URLs from being quarantined against
// the live pack's index.
func (l *MountLayer) PackRelOf(localURL string) (string, bool) {
	if l == nil {
		return "", false
	}
	return strings.CutPrefix(localURL, l.localOrigin)
}

// Covers reports whether the pack holds anything for a SERVER base or exact URL.
// Used by the rescan invalidation to drop exactly the textures the pack can
// re-answer, and by the content report's "also in your folders" tag.
//
// Both index namespaces are consulted because T1 carries both key shapes: the
// probe path uploads under an extensionLESS base, while evidence and music are
// delivered under their full URL with extension.
func (l *MountLayer) Covers(base string) bool {
	if l == nil || l.idx == nil {
		return false
	}
	rel, ok := l.RelOf(base)
	if !ok || mountLayerExcluded(rel) {
		return false
	}
	return l.idx.Covers(foldRel(rel))
}

// mountLayerExcluded refuses rels the pack must never answer for.
//
// A trailing slash is a directory listing (the iniswap and background pickers
// fetch the origin's autoindex HTML) — a pack has no such thing, and answering
// would replace a real listing with nothing.
//
// The manifest is excluded because extensions.json is the SERVER's format
// declaration and seeds that host's learned formats. A pack shipping its own
// copy — likely, since packs are often carved out of a server's base — would
// otherwise reseed the server's probe order from the pack author's conventions
// and cost a wrong-format probe on every cold asset for that host.
func mountLayerExcluded(rel string) bool {
	if rel == "" || strings.HasSuffix(rel, "/") {
		return true
	}
	last := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		last = rel[i+1:]
	}
	return strings.EqualFold(last, ManifestFileName)
}
