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
const (
	localNotFoundCacheSize = 2048
	localNotFoundCacheTTL  = 30 * time.Second
)

// localFoldScanChunk / localFoldScanCap / localFoldMaxDepth bound the
// case-insensitive resolution readRelFold performs. A mount folder is arbitrary
// user-chosen content — somebody WILL mount a drive root — so every dimension of
// that walk carries a named cap (rule 4), and the product of the three is its
// whole worst case: mounts × localFoldMaxDepth directory scans, each reading at
// most localFoldScanCap entries in localFoldScanChunk-sized batches.
const (
	localFoldScanChunk = 256
	localFoldScanCap   = 16384
	localFoldMaxDepth  = safepath.MaxDepth
)

// LocalFetcher serves asset bytes from user-chosen mount folders instead of
// the network: the "Legacy support for servers without an asset server"
// checkbox. Mounts are searched in order, first hit wins (AO2-Client mount
// path semantics — any folder layout, not just a default /base).
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
// Every caller that wants the LABEL rather than the bytes must use this. Minting
// a fetcher for a label was the old idiom (NewMountLayer, ui/demofile.go
// mountOrigin) and it is not free: a fetcher carries the rule-6 negative memo,
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
	// Attempt 3: the decoded rel again, resolved case-INSENSITIVELY segment by
	// segment. Every URL that reaches this fetcher is lowercased by the builder
	// (courtroom seg/charSeg — asset hosts are case-sensitive and webAO-convention
	// mirrors ship lowercase), while a mount folder carries the case its AUTHOR
	// typed: "characters/SG/Faris NyanNyan/(b)suprised.png". On NTFS and on a
	// default APFS volume those name the same file and attempts 1-2 already
	// answered, which is why this went unnoticed on the Windows dev box; on ext4
	// they do not, so on the Linux AppImage/Flatpak (and a case-sensitive macOS
	// volume) EVERY asset of every capitalised pack folder was unreachable — a
	// whole-character miss, not an edge case.
	if data, err := l.readRelFold(decoded); err != nil {
		return nil, err
	} else if data != nil {
		return data, nil
	}
	// Every spelling missed in every mount: conclusive. Remember it so the next
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

// readRelFold is readRel's case-insensitive twin: same mount order, same
// first-hit-wins and same (nil,nil) "missing everywhere" answer, but each segment
// of rel is matched against the real directory entry names when the exact spelling
// is not there.
func (l *LocalFetcher) readRelFold(rel string) ([]byte, error) {
	// Defence in depth: the same rel already passed this guard in readRel above.
	// Kept because this walk appends path segments of its own, and a guard that
	// only ever ran on the other spelling is the kind that quietly stops covering.
	if safepath.UnsafeRel(rel) {
		return nil, fmt.Errorf("assets: refusing path escape %q", rel)
	}
	// The heal is depth-bounded (localFoldMaxDepth). A rel this deep is not an AO
	// path, and answering (nil,nil) — "missing everywhere", the same answer an
	// absent file gets — keeps it a MISS rather than an error: the exact spellings
	// already ran and missed, so there is nothing hostile to report, only nothing
	// to heal. Fetch memoizes that miss like any other.
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
		// Read errors are surfaced exactly as readRel surfaces them, deliberately:
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("assets: reading local asset %s: %w", p, err)
		}
	}
	return nil, nil
}

// foldJoin resolves rel under root one segment at a time, preferring the exact
// spelling and falling back to the directory's own entry name when only the case
// differs. ok=false as soon as a segment matches nothing, so a miss costs at most
// one scan per existing parent directory.
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
		// The one refusal (see the doc): never descend into, and never hand back, a
		// name that is neither a plain directory nor a regular file. safepath owns the
		// predicate because safepath.WalkFiles owns the same policy for the index walk.
		if safepath.UnsafePath(cand) {
			return "", false
		}
		p = cand
	}
	return p, true
}

// pathExists reports whether a path is there to be opened, following symlinks exactly
// as the os.ReadFile that ultimately reads it does. It is a SPELLING question only —
// "is this segment already correct?" — and answers nothing about whether the path may
// be served: foldJoin's safepath.UnsafePath check is what decides that, on the same
// candidate, one line later. Note where the two differ in kind — os.Stat SUCCEEDS on a
// FIFO or a device node without opening it, so this reports "already correct" for names
// the refusal one line later rejects, which is exactly the division of labour intended.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// foldLookup returns the entry of dir whose name equals want case-insensitively,
// reading the directory in bounded chunks (localFoldScanChunk/localFoldScanCap).
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
		// io.EOF (the directory is exhausted) or an unreadable directory. The
		// empty-batch guard is belt-and-braces: os.File.ReadDir(n>0) documents a
		// non-nil error with an empty slice, and this loop must not depend on it.
		if err != nil || len(ents) == 0 {
			break
		}
	}
	return best, found
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
