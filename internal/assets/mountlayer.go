package assets

// MountLayer is the immutable wrapper the Manager consults: a MountIndex plus the origins it may answer for.
// A pack transport URL (LocalOrigin() + folded rel) is a label for T2 and quarantine, never fetched.

import (
	"net/url"
	"os"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// mountLayerOriginCap is the tab ceiling (hard rule 4), as a smaller cap would silently leave tabs unlayered.
const mountLayerOriginCap = config.MaxMultiTabCap

// MountLayer is described above; its fields are read-only after construction.
type MountLayer struct {
	idx         *MountIndex
	origins     []string
	localOrigin string
}

// NewMountLayer wraps idx; origins must be the URL builder's Origin(), never a raw ASS field, whose missing
// slash breaks RelOf's match. It runs on every tab activation, so it must not mint a fetcher.
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

// Index returns the wrapped index; safe on nil.
func (l *MountLayer) Index() *MountIndex {
	if l == nil {
		return nil
	}
	return l.idx
}

// LocalOrigin returns the pack transport-URL prefix.
func (l *MountLayer) LocalOrigin() string { return l.localOrigin }

// Origins returns the registered asset origins.
func (l *MountLayer) Origins() []string { return l.origins }

// RelOf strips the longest registered origin: servers can share a host with different base paths.
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

// LocalURLFor builds the pack transport URL for a folded rel, never a URL rel or the on-disk name.
func (l *MountLayer) LocalURLFor(foldedRel string) string { return l.localOrigin + foldedRel }

// PackRelOf inverts LocalURLFor; it cuts this layer's origin, so replay URLs are never quarantined here.
func (l *MountLayer) PackRelOf(localURL string) (string, bool) {
	if l == nil {
		return "", false
	}
	return strings.CutPrefix(localURL, l.localOrigin)
}

// Covers reports whether the pack holds a server base or exact URL, as T1 has both key shapes.
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

// mountLayerExcluded refuses rels a pack must not answer: a pack's extensions.json would reseed the host's order.
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

// ReadFileDirect reads a file by relative path, bypassing the index, for sidecars like loop point .txt.
func (l *MountLayer) ReadFileDirect(rel string) ([]byte, error) {
	if l == nil || l.idx == nil {
		return nil, os.ErrNotExist
	}

	decoded := rel
	if d, err := url.PathUnescape(rel); err == nil {
		decoded = d
	}

	// Sidecars are usually not indexed, but try the index first.
	folded := foldRel(decoded)
	if f, ok := l.idx.files[folded]; ok {
		return l.idx.ReadFile(f, folded)
	}

	// Not indexed: read each source, using the exact path with no URL encoding.
	for _, src := range l.idx.sources {
		if data, err := src.read(decoded); err == nil {
			return data, nil
		}
	}

	return nil, os.ErrNotExist
}
