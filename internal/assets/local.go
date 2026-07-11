package assets

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// LocalScheme prefixes asset "URLs" served from local mount folders in
// no-streaming mode. The origin embeds a hash of the mount set, so different
// mount configurations occupy disjoint cache keyspace — exactly like two
// asset hosts never colliding.
const LocalScheme = "local://"

// LocalFetcher serves asset bytes from user-chosen mount folders instead of
// the network: the "Legacy support for servers without an asset server"
// checkbox. Mounts are searched in order, first hit wins (AO2-Client mount
// path semantics — any folder layout, not just a default /base).
//
// It satisfies the manager's Fetcher contract; a file missing from every
// mount maps to network.ErrAssetNotFound so learned formats, fallback
// chains, and missing-asset warnings behave identically to streaming.
type LocalFetcher struct {
	mounts []string
	origin string
}

// NewLocalFetcher roots a fetcher at the given ordered mount folders (each
// containing characters/, background/, sounds/, ...). Empty entries are
// dropped.
func NewLocalFetcher(mounts []string) *LocalFetcher {
	cleaned := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m == "" {
			continue
		}
		cleaned = append(cleaned, filepath.Clean(m))
	}
	// The origin identifies this exact mount set (order included) in cache
	// keys and the learned-format table.
	id := cache.Key(strings.Join(cleaned, string(os.PathListSeparator)))
	return &LocalFetcher{
		mounts: cleaned,
		origin: LocalScheme + "m-" + id + "/",
	}
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
func (l *LocalFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	rel, ok := strings.CutPrefix(rawURL, l.origin)
	if !ok {
		return nil, fmt.Errorf("assets: %q is not under local origin %q", rawURL, l.origin)
	}
	if len(l.mounts) == 0 {
		return nil, fmt.Errorf("%w: no local mount folders configured", network.ErrAssetNotFound)
	}
	// Attempt 1: the rel verbatim (escaped names written by the exporter).
	if data, err := l.readRel(rel); err != nil {
		return nil, err // hard I/O error (not just "missing") — surface it
	} else if data != nil {
		return data, nil
	}
	// Attempt 2: percent-decoded per segment. Decoding per segment (not the
	// whole rel) keeps a literal "%2F" inside a name from inventing a path
	// separator. A malformed escape (%zz) is not a real filename — skip the
	// decoded attempt rather than fail the fetch.
	if dec, ok := decodeRel(rel); ok && dec != rel {
		if data, err := l.readRel(dec); err != nil {
			return nil, err
		} else if data != nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("%w: %s (searched %d mounts)", network.ErrAssetNotFound, rel, len(l.mounts))
}

// readRel searches every mount for rel, returning (bytes,nil) on the first
// non-empty hit, (nil,nil) when rel is missing everywhere (so the caller can
// try another spelling), and (nil,err) on a real I/O error or a path escape.
// The ".." guard runs HERE so it re-checks the DECODED rel too — a "%2e%2e"
// must not slip past the escape check by decoding into "..".
func (l *LocalFetcher) readRel(rel string) ([]byte, error) {
	if strings.Contains(rel, "..") {
		return nil, fmt.Errorf("assets: refusing path escape %q", rel)
	}
	relNative := filepath.FromSlash(rel)
	for _, mount := range l.mounts {
		data, err := os.ReadFile(filepath.Join(mount, relNative))
		if err == nil && len(data) > 0 {
			return data, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("assets: reading local asset %s: %w", filepath.Join(mount, relNative), err)
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
