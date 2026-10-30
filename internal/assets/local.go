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

// LocalScheme prefixes mount-folder asset URLs; the origin hashes the mount set, so cache keys stay disjoint.
const LocalScheme = "local://"

// localNotFoundCacheSize and localNotFoundCacheTTL bound the negative cache. It must be missMemo, not expirable.LRU:
// golang-lru v2.0.7 starts an unstoppable janitor goroutine per instance, and fetchers are minted often.
const (
	localNotFoundCacheSize = 2048
	localNotFoundCacheTTL  = 30 * time.Second
)

// localFoldScanChunk, localFoldScanCap and localFoldMaxDepth bound readRelFold: a user may mount a drive root.
const (
	localFoldScanChunk = 256
	localFoldScanCap   = 16384
	localFoldMaxDepth  = safepath.MaxDepth
)

// LocalFetcher serves bytes from mount folders in order, first hit wins (AO2-Client mount path semantics).
type LocalFetcher struct {
	mounts []string
	origin string
	// notFound memoizes conclusive misses (hard rule 6); mutex-guarded for pool workers, with no janitor goroutine.
	notFound *missMemo
}

// NewLocalFetcher roots a fetcher at the ordered mount folders; empty entries are dropped.
func NewLocalFetcher(mounts []string) *LocalFetcher {
	return newLocalFetcher(mounts, localNotFoundCacheTTL)
}

// newLocalFetcher lets tests shrink the negative-cache TTL; unexported so no outside caller can weaken hard rule 6.
func newLocalFetcher(mounts []string, notFoundTTL time.Duration) *LocalFetcher {
	cleaned := cleanMounts(mounts)
	return &LocalFetcher{
		mounts:   cleaned,
		origin:   localOriginOf(cleaned),
		notFound: newMissMemo(localNotFoundCacheSize, notFoundTTL),
	}
}

// LocalOriginFor returns the origin of NewLocalFetcher(mounts).BaseURL() without minting a fetcher and its memo.
func LocalOriginFor(mounts []string) string {
	return localOriginOf(cleanMounts(mounts))
}

// cleanMounts is the normalization the origin hash uses: empties dropped, the rest cleaned, order preserved.
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

// localOriginOf builds the origin from a cleaned mount list; it identifies this exact mount set, order included.
func localOriginOf(cleaned []string) string {
	return LocalScheme + "m-" + cache.Key(strings.Join(cleaned, string(os.PathListSeparator))) + "/"
}

// BaseURL returns the origin bases are built from: local://m-<mountset>/.
func (l *LocalFetcher) BaseURL() string {
	return l.origin
}

// Mounts returns the resolved mount list.
func (l *LocalFetcher) Mounts() []string {
	out := make([]string, len(l.mounts))
	copy(out, l.mounts)
	return out
}

// Fetch reads a local:// URL from each mount in order; a miss on all three spellings is memoized (hard rule 6).
func (l *LocalFetcher) Fetch(_ context.Context, rawURL string) ([]byte, error) {
	rel, ok := strings.CutPrefix(rawURL, l.origin)
	if !ok {
		return nil, fmt.Errorf("assets: %q is not under local origin %q", rawURL, l.origin)
	}
	if len(l.mounts) == 0 {
		return nil, fmt.Errorf("%w: no local mount folders configured", network.ErrAssetNotFound)
	}
	// Keyed by full URL; the origin in it hashes the mount set, so a mount change can never read a stale entry.
	if l.notFound.has(rawURL) {
		return nil, fmt.Errorf("%w: %s (missing, negative-cached)", network.ErrAssetNotFound, rel)
	}
	// Attempt 1: the rel verbatim, for escaped names written by the exporter.
	if data, err := l.readRel(rel); err != nil {
		return nil, err // hard I/O error (not just "missing") — surface it
	} else if data != nil {
		return data, nil
	}
	// Attempt 2: percent-decoded per segment, for names with spaces and parens; skip on a malformed escape.
	decoded := rel
	if dec, ok := decodeRel(rel); ok {
		decoded = dec
	}
	if decoded != rel {
		if data, err := l.readRel(decoded); err != nil {
			return nil, err
		} else if data != nil {
			return data, nil
		}
	}
	// Attempt 3: case-insensitive per segment; URLs are lowercased but mounts keep their case, unlike on ext4.
	if data, err := l.readRelFold(decoded); err != nil {
		return nil, err
	} else if data != nil {
		return data, nil
	}
	// Only a miss is memoized; a hard I/O error returned above is not conclusive and must stay retryable.
	l.notFound.add(rawURL)
	return nil, fmt.Errorf("%w: %s (searched %d mounts)", network.ErrAssetNotFound, rel, len(l.mounts))
}

// readRel returns the first non-empty hit, (nil,nil) when missing everywhere, or an error on I/O trouble or escape.
// The escape check is here so "%2e%2e" cannot decode into "..".
func (l *LocalFetcher) readRel(rel string) ([]byte, error) {
	if safepath.UnsafeRel(rel) {
		return nil, fmt.Errorf("assets: refusing path escape %q", rel)
	}
	for _, mount := range l.mounts {
		p, err := safepath.Join(mount, rel)
		// Unreachable while the check above holds: Join refuses what UnsafeRel refuses; kept in case that check moves.
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

// readRelFold is readRel matching each segment against real directory entry names when the exact spelling is absent.
func (l *LocalFetcher) readRelFold(rel string) ([]byte, error) {
	// Repeated from readRel because this walk appends path segments of its own.
	if safepath.UnsafeRel(rel) {
		return nil, fmt.Errorf("assets: refusing path escape %q", rel)
	}
	// A rel this deep is not an AO path; answer "missing" rather than an error, and Fetch memoizes it like any miss.
	if safepath.TooDeep(rel, localFoldMaxDepth) {
		return nil, nil
	}
	for _, mount := range l.mounts {
		p, ok := foldJoin(mount, rel)
		if !ok {
			continue
		}
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return data, nil
		}
		// Surface read errors as readRel does: a failing disk must not be memoized as "this asset does not exist".
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("assets: reading local asset %s: %w", p, err)
		}
	}
	return nil, nil
}

// foldJoin resolves rel per segment, exact spelling first, then a case-insensitive match; ok=false on no match.
func foldJoin(root, rel string) (string, bool) {
	p := root
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." {
			continue
		}
		cand := filepath.Join(p, seg)
		if !pathExists(cand) {
			name, ok := foldLookup(p, seg)
			if !ok {
				return "", false
			}
			cand = filepath.Join(p, name)
		}
		// Never descend into or return anything but a plain directory or regular file; WalkFiles has the same policy.
		if safepath.UnsafePath(cand) {
			return "", false
		}
		p = cand
	}
	return p, true
}

// pathExists answers only whether this spelling exists: os.Stat succeeds on a FIFO, which UnsafePath then rejects.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// foldLookup returns dir's entry equal to want case-insensitively, reading in bounded chunks.
func foldLookup(dir, want string) (string, bool) {
	f, err := os.Open(dir)
	if err != nil {
		return "", false
	}
	defer f.Close()
	best, found := "", false
	for scanned := 0; scanned < localFoldScanCap; {
		ents, err := f.ReadDir(localFoldScanChunk)
		for _, e := range ents {
			name := e.Name()
			if name == want {
				return name, true // exact: nothing can outrank it
			}
			if strings.EqualFold(name, want) && (!found || name < best) {
				best, found = name, true
			}
		}
		scanned += len(ents)
		// EOF or an unreadable directory; the empty-batch check avoids relying on ReadDir's documented error.
		if err != nil || len(ents) == 0 {
			break
		}
	}
	return best, found
}

// decodeRel percent-decodes each segment of rel; ok=false on a malformed escape, which is not a real filename.
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
