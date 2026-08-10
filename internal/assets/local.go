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

// localFoldScanChunk / localFoldScanCap / localFoldMaxDepth bound the
// case-insensitive resolution readRelFold performs. A mount folder is arbitrary
// user-chosen content — somebody WILL mount a drive root — so every dimension of
// that walk carries a named cap (rule 4), and the product of the three is its
// whole worst case: mounts × localFoldMaxDepth directory scans, each reading at
// most localFoldScanCap entries in localFoldScanChunk-sized batches.
//
// The chunk is one page-ish batch of dirents, so a directory is never
// materialized whole; the cap is two orders of magnitude past the largest real AO
// layout (the biggest content packs ship a few thousand characters), so no
// legitimate pack can be truncated by it while a mistaken mount costs one bounded
// scan. The depth is safepath's shared guard — the SAME bound the mount INDEX
// walk uses (mountIndexMaxDepth), so the two byte sources agree on how deep a
// mount can be read at all. AO layouts are three deep.
//
// The whole cost is paid on the miss path only, and at most once per URL per
// localNotFoundCacheTTL: the conclusive miss it ends in is memoized (rule 6).
const (
	localFoldScanChunk = 256
	localFoldScanCap   = 16384
	localFoldMaxDepth  = safepath.MaxDepth
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
// The URLBuilder percent-escapes and lowercases every path segment regardless of
// origin (it builds http URLs), so a mounted pack named "Phoenix Wright" arrives
// here as "phoenix%20wright". Three spellings are tried, in cost order: the RAW
// rel (exported scene archives write escaped names symmetrically and must keep
// resolving byte-for-byte), then the percent-DECODED rel so real on-disk names
// with spaces/parens resolve, then that same decoded rel resolved
// case-insensitively so a folder carrying its author's capitalisation resolves on
// a case-sensitive filesystem too (attempt 3 below — the one attempt that enumerates
// directories, and therefore the one that refuses any name the mount INDEX walk would
// have skipped: a symlink, a FIFO, a socket, a device node).
//
// A miss on ALL THREE spellings across every mount is conclusive, and conclusive
// misses are memoized for localNotFoundCacheTTL: hard rule 6 applies to this
// byte source exactly as it does to the streaming one — which is also what keeps
// attempt 3's directory scan from ever repeating for the same missing URL.
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
	//
	// MountIndex solved exactly this for the OTHER byte source, and says so:
	// foldRel's "folding case is what makes a pack authored on Windows resolve
	// identically on Linux, which is strictly better than today's reliance on NTFS
	// being case-insensitive" (mountindex.go). This is that same fold for the
	// no-streaming source, so "it is in my mounts" now means the same thing
	// whichever of the two answers — the Fetcher contract, not one implementation.
	//
	// Only the DECODED spelling is folded: the raw one exists for exported archives,
	// which write their escaped names to disk verbatim and are therefore already
	// answered byte-for-byte by attempt 1. (When the rel holds a malformed escape
	// there IS no decoded spelling and `decoded` is the raw rel — folding that is
	// the same one bounded walk over a name no file is likely to carry, not a
	// fourth attempt.)
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

// readRelFold is readRel's case-insensitive twin: same mount order, same
// first-hit-wins and same (nil,nil) "missing everywhere" answer, but each segment
// of rel is matched against the real directory entry names when the exact spelling
// is not there.
//
// It runs ONLY after both exact spellings have missed in every mount, so on a
// case-insensitive filesystem it is unreachable for any file that exists, and on a
// case-sensitive one it can only turn a miss into a hit — it can never shadow or
// re-point a lookup that already resolved.
//
// That is also why it is a third SPELLING tried across every mount rather than a
// third attempt inside the per-mount loop: the search has always been
// spelling-major and mount-minor (a raw hit in the LAST mount already beats a
// decoded hit in the first), and extending that order adds hits, while inverting
// it would silently re-point lookups that resolve today. That ordering is pinned by
// TestLocalFetcherFoldNeverOutranksAnExactHitInALaterMount, which needs TWO mounts
// to see it: with one mount foldJoin's os.Stat preference answers the same either
// way round, so a single-mount gate cannot tell an added hit from a stolen one.
//
// IT FOLDS ONLY ONTO PLAIN DIRECTORIES AND REGULAR FILES, and that is where it parts
// company with the exact spellings above. The argument that first stood here —
// "attempts 1-2 already reach a linked file through os.ReadFile, so the fold reaches
// the same names modulo case" — is false, and an adversarial mount review proved it
// with two packs: this walk ENUMERATES (foldLookup's os.Open + ReadDir), so it reaches
// names the caller never spelled. A mount holding characters/Up -> the parent directory
// served characters/up/secret.txt, and one holding characters/Evil/(a)Normal.png ->
// /outside/id_rsa answered the lowercased sprite URL with the key.
//
// That is the exfiltration route safepath.WalkFiles already closes for the mount INDEX,
// in its own words: "a link at characters/x/(a)a.png pointing at ~/.ssh/id_rsa would be
// indexed, read, and uploaded as a texture. The '..' guard alone does not cover it,
// because the link's own path never contains '..'". The two byte sources agreed on case
// and disagreed on everything the index walk skips; they agree on both now — foldJoin
// refuses any segment that lstats as neither a directory nor a regular file, which IS
// the shape of WalkFiles' refusal (it visits regular files, recurses into directories,
// and skips every other entry).
//
// A SYMLINK-ONLY REFUSAL IS NOT THAT SHAPE, and the first draft of this fix shipped one
// under this same sentence. The gap was measured, not argued: a mount holding
// characters/Phoenix/(a)Normal.png as a FIFO indexes as NOTHING (WalkFiles skips it, the
// index over that tree is empty) while the fold matched the authored case, handed the
// path back, and the os.ReadFile below blocked forever on a pool worker — opening a FIFO
// for reading waits for a writer, and Fetch above documents local reads as
// uncancellable. Device nodes are the same shape with an unbounded read instead of a
// stall. TestLocalFetcherFoldAndTheIndexAgreeAboutAFifo drives BOTH byte sources over
// that one tree, because "they agree" is the claim this refusal rests on.
//
// The EXACT spellings keep reading through a link — and into a FIFO, where they have
// always been able to block — deliberately and unchanged: that is pre-existing policy
// for a path the caller spelled out byte-for-byte, one os.ReadFile with no enumeration
// behind it, and NOT widening a policy is a different decision from narrowing one.
// TestLocalFetcherExactSpellingStillReadsThroughASymlink pins that half so the line
// drawn here stays visible.
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
		// a failing disk must not be memoized as "this asset does not exist" on one
		// spelling while the other reports it. The only NEW way to reach this branch
		// is a rel that folds onto a DIRECTORY — which the exact spellings already
		// error on today (EISDIR) and which no asset URL spells, since every one of
		// them carries a format extension.
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
//
// STANDALONE-UNSAFE — IT CONTAINS NO CONTAINMENT CHECK OF ITS OWN. It cannot escape
// root only because ".." was refused BEFORE it was called (safepath.UnsafeRel counts
// BOTH separators, so a Windows "a\..\b" segment is refused too) and because every
// name it appends was read out of a directory already inside root. readRelFold, its
// only caller, runs that guard and the depth bound immediately above the loop that
// calls this. It stays unexported for exactly that reason: a second call site that
// skipped the guard would turn this into the traversal primitive the rest of the file
// is built to refuse, and an exported one could not be audited by reading this file.
//
// A SEGMENT THAT LSTATS AS NEITHER A DIRECTORY NOR A REGULAR FILE ENDS THE WALK
// (ok=false, a miss): a symlink, a FIFO, a socket, a device node. That is
// safepath.UnsafePath, the by-path half of the refusal WalkFiles applies to the DirEntry
// it already holds, so the fold can only END on a name the mount INDEX would have
// indexed and can only descend through directories that walk would have recursed into.
// Both branches are checked, the exact-spelling one included: the
// exfiltration route is the ENUMERATION inside a linked directory, and it opens whether
// the link's own name was spelled exactly or reached by folding.
//
// Refusal ends the walk rather than looking for an acceptable case-variant sibling —
// WalkFiles skips such an entry and moves on, and choosing a variant here would make the
// served file depend on which of two entries happens to be a link, the unstable answer
// foldLookup's tie-break exists to avoid. readRelFold's doc carries the full argument,
// the review that forced it, and the second review that widened it past symlinks.
//
// The mount ROOT is not checked: the user chose it in Settings, and a policy about what
// a stranger's PACK may contain has nothing to say about the folder its owner mounted.
//
// Empty and "." segments are skipped rather than refused, so a doubled slash
// resolves exactly as it does through safepath.Join → filepath.Join, which cleans
// them away: the URL builder mints one for a char.ini anim with a leading slash
// (courtroom EmoteFolder), verbatim AO2 concatenation.
//
// THE EXACT-SPELLING PREFERENCE HERE AND foldLookup's EXACT EARLY RETURN ARE ONE
// GUARANTEE written twice, and neither is dead: this one is the fast path (a hit
// costs one os.Stat instead of scanning a characters/ folder holding thousands of
// packs) and the only answer for a directory bigger than localFoldScanCap, while
// foldLookup's covers an entry os.Stat cannot resolve (a dangling link — which the
// refusal below now turns into a miss, but WHICH name the scan answers with is still
// its question: without the early return a fold-equal sibling would answer in its
// place). Because each masks the other, the guarantee is pinned as a whole —
// TestLocalFetcherFoldPrefersTheExactlyCasedSegment fails when BOTH are removed —
// and foldLookup's half is driven on its own by
// TestFoldLookupReturnsTheExactNameOverASmallerVariant.
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
//
// strings.EqualFold rather than a lowercase compare: it allocates nothing, and for
// the ASCII names AO packs ship it is the same answer foldRel's strings.ToLower
// gives on the index side.
//
// TIES ARE RESOLVED, NOT LEFT TO THE FILESYSTEM. A pack CAN ship "(a)Normal.png"
// and "(a)normal.png" side by side on ext4, and returning whichever the directory
// happened to list first would make one machine serve a different sprite than the
// next — and the same machine a different one after a rehash. So: an EXACT match
// wins outright (it is also what the os.Stat in foldJoin already answered), and
// among case-variants the byte-lexicographically smallest name wins, which is
// stable across filesystems, mounts and runs. Past localFoldScanCap the scan
// stops, so a directory larger than the cap heals only from its scanned prefix —
// the one place the answer is the directory's order, and the price of the bound.
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
