package assets

// MountIndex is the pre-built map of everything in the user's local mount
// folders and .zip packs, so a layered lookup is an in-memory map hit instead of
// a directory walk or a stat storm on the fetch path.
//
// THE ONE KEY SPACE. Every map and set in this file is keyed by the FOLDED REL
// INCLUDING ITS EXTENSION — the output of foldRel. `files`, the quarantine and
// Covers' exact arm all use that identical string, and a pack transport URL is
// defined as MountLayer.LocalOrigin() + <that folded rel>. So URL→key is one
// CutPrefix and key→URL is one concatenation; nothing ever re-folds. Two earlier
// drafts of this design shipped a quarantine that was silently dead code because
// it was written with a URL and read with a rel — hence the single stated rule.
//
// The index is IMMUTABLE once published, with exactly one bounded mutable
// member: the quarantine. Rebuilding it (the user's Rescan) deliberately forgets
// the quarantine, because a rescan is the user asserting their folders changed,
// which is the only realistic moment a damaged file gets fixed.
//
// Threading: BuildMountIndex blocks and MUST run on a goroutine, never on the
// render thread (rule 2 — no synchronous disk I/O on the render path). Lookups
// run on pool workers. Reference counting keeps an archive handle alive while a
// worker is mid-read across a Rescan that swaps the index out from under it.

import (
	"archive/zip"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

const (
	// mountIndexEntryBytes is the ACCOUNTED cost of one indexed file: the folded
	// key string, the packFile value, this entry's share of both maps' bucket
	// overhead, and the stem entry. Go maps also hold old and new bucket arrays
	// simultaneously while growing, so the real peak runs above nominal — the
	// figure is deliberately generous rather than a measured floor.
	mountIndexEntryBytes = 180
	// mountIndexByteCap bounds the ACCOUNTED footprint (entries ×
	// mountIndexEntryBytes), enforced DURING the walk so it stops at the cap
	// instead of discovering it afterwards.
	//
	// Sizing: cmd/asyncao installs a hard debug.SetMemoryLimit of 256 MiB and the
	// T2 tier already commits 128 MiB of that, with the decode pool's pixpool
	// classes on top — so the headroom this can spend is far smaller than the
	// total budget suggests, and pushing live heap toward SetMemoryLimit makes the
	// GC run continuously (which the 1 Hz GC-p99 metric watches). 8 MiB is ~46k
	// files, already far past any "small custom content pack".
	mountIndexByteCap = 8 << 20
	// mountIndexMaxEntries is the cap in entries, derived so the two constants can
	// never drift apart.
	mountIndexMaxEntries = mountIndexByteCap / mountIndexEntryBytes
	// mountIndexMaxDepth bounds directory recursion. It is safepath's shared
	// bound, aliased rather than copied so the mount index and the theme packer
	// can never drift into two different ideas of "too deep" (rule 4).
	mountIndexMaxDepth = safepath.MaxDepth
	// mountBadCap bounds the quarantine of pack files that failed to DECODE.
	//
	// Eviction is OLDEST-FIRST, deliberately inverting the stop-inserting-when-full
	// policy used by TextureStore.MarkMissing and courtroom.recordMissing. There, a
	// refused insert costs slightly worse UX. HERE it costs correctness: an
	// unquarantined corrupt pack file keeps winning the lookup, so the network is
	// never reached and the asset is permanently missingno. Evicting the oldest
	// guarantees the CURRENTLY demanded asset is always quarantined and therefore
	// always heals; a pack with more than this many corrupt files pays one extra
	// read per cycled-out entry, bounded by demand rate, never per frame.
	mountBadCap = 256
	// mountArchiveCap bounds simultaneously OPEN .zip handles (rule 4). Each is an
	// OS file descriptor plus its parsed central directory held for the index's
	// lifetime, so this is a real resource, not a counter.
	mountArchiveCap = 8
	// mountZipEntryMaxBytes refuses a single pack entry larger than this. A zip
	// entry's uncompressed size is attacker-controlled metadata, and ReadFile
	// allocates it in one piece; no legitimate AO asset comes close.
	mountZipEntryMaxBytes = 64 << 20
)

// indexedExts is the extension table the stem mask is bit-indexed over. Its
// length is the mask width, so it may never exceed 16 entries — the compile-time
// assertion below enforces that. Order is irrelevant to correctness (the mask is
// only ever consulted through extBit), so entries may be appended freely.
var indexedExts = [...]string{
	config.ExtPNG, config.ExtWebP, config.ExtAPNG, config.ExtGIF,
	config.ExtJPG, config.ExtAVIF,
	config.ExtOpus, config.ExtOgg, config.ExtWAV, config.ExtMP3,
}

// A stem's presence set is a uint16, so indexedExts must fit in 16 bits. This
// constant fails to compile (unsigned overflow) the moment a 17th is added.
const _ = uint(16 - len(indexedExts))

var (
	// errNotAMount rejects a configured mount that is neither a folder nor a .zip.
	errNotAMount = errors.New("not a folder or a .zip pack")
	// errTooManyArchives reports the mountArchiveCap refusal.
	errTooManyArchives = errors.New("too many .zip packs mounted")
	// errEntryTooLarge refuses a zip entry whose size exceeds mountZipEntryMaxBytes.
	errEntryTooLarge = errors.New("pack entry is too large")
)

// mountError names which mount a build error came from, so the Settings status
// line can say "Folder not found: C:\AO2\pack" instead of a bare errno.
type mountError struct {
	mount string
	err   error
}

func (e *mountError) Error() string { return "assets: mount " + e.mount + ": " + e.err.Error() }
func (e *mountError) Unwrap() error { return e.err }

// Mount reports which configured mount failed.
func (e *mountError) Mount() string { return e.mount }

// extBit returns the stem-mask bit for ext and whether ext is indexed at all.
// An unindexed extension is not an error: the lookup simply falls back to a
// direct exact-key probe, so adding a format to the probe chain without adding
// it here degrades to a map hit rather than a miss.
func extBit(ext string) (uint16, bool) {
	for i, e := range indexedExts {
		if e == ext {
			return 1 << uint(i), true
		}
	}
	return 0, false
}

// packFile locates one indexed file. rel is the EXACT on-disk (or in-archive)
// relative path; it is left EMPTY when that path equals the folded key, which is
// the common all-lowercase case and halves the index's string data. mount is the
// index of the source it came from.
type packFile struct {
	rel   string
	mount uint8
}

// MountIndex is described in the file header.
type MountIndex struct {
	sources []mountSource
	mounts  []string // display/identity copy of the configured mount list

	files map[string]packFile // folded rel (WITH extension) -> location
	stems map[string]uint16   // folded rel WITHOUT extension -> indexed-extension mask

	entries   int
	truncated bool

	bad quarantine

	// refs keeps archive handles alive across a Rescan: a pool worker that has
	// acquired the index may be mid-read when the render thread publishes a new
	// one and retires this one. Close happens when the last reference drops, not
	// when the pointer is swapped.
	refs    atomic.Int32
	retired atomic.Bool
	closeUp sync.Once
}

// mountSource is the folder-or-archive seam. Both implementations are read-only
// after construction; walk runs once at build time on a goroutine, read runs on
// pool workers and MUST be safe for concurrent use.
type mountSource interface {
	// walk visits every FILE under the source, passing the source-relative path
	// with forward slashes. Returning false stops the walk (the byte cap).
	walk(fn func(rel string) bool) error
	// read returns one entry's bytes by its exact source-relative path.
	read(rel string) ([]byte, error)
	// close releases any OS handle. Safe to call once.
	close() error
	// name is the configured mount string, for diagnostics.
	name() string
}

// BuildMountIndex walks every mount in order and returns the published index.
// BLOCKING — goroutine only.
//
// Order matters: mounts are searched first-hit-wins, so an earlier mount shadows
// a later one, exactly like AO2-Client mount paths. The walk therefore keeps the
// FIRST spelling it sees for a key and ignores later duplicates.
//
// Errors are deliberately not fatal. A mount that cannot be opened (an unplugged
// drive, a deleted folder, a corrupt zip) is skipped with its error recorded, and
// whatever else indexed still serves: the layer only ever ADDS hits over the
// network path, so partial degradation is always safe.
func BuildMountIndex(mounts []string) (*MountIndex, []error) {
	ix := &MountIndex{
		mounts: append([]string(nil), mounts...),
		files:  make(map[string]packFile),
		stems:  make(map[string]uint16),
	}
	ix.refs.Store(1) // the owner reference, dropped by Retire
	var errs []error
	archives := 0
	for i, m := range mounts {
		if m == "" || i > int(^uint8(0)) {
			continue
		}
		if isArchivePath(m) && archives >= mountArchiveCap {
			errs = append(errs, &mountError{mount: m, err: errTooManyArchives})
			continue
		}
		src, err := openMountSource(m)
		if err != nil {
			errs = append(errs, &mountError{mount: m, err: err})
			continue
		}
		if isArchivePath(m) {
			archives++
		}
		ix.sources = append(ix.sources, src)
		if err := ix.absorb(src, uint8(len(ix.sources)-1)); err != nil {
			errs = append(errs, &mountError{mount: m, err: err})
		}
		if ix.truncated {
			break
		}
	}
	return ix, errs
}

// absorb indexes one source's files, stopping at the byte cap.
func (ix *MountIndex) absorb(src mountSource, mount uint8) error {
	return src.walk(func(rel string) bool {
		if ix.entries >= mountIndexMaxEntries {
			ix.truncated = true
			return false
		}
		// Folder walks are already depth-bounded by safepath.WalkFiles; this
		// covers the OTHER source kind, whose entry names come straight out of a
		// zip central directory and never touch a walker.
		if safepath.TooDeep(rel, mountIndexMaxDepth) {
			return true
		}
		key := foldRel(rel)
		if _, dup := ix.files[key]; dup {
			return true // first mount wins (AO2 mount-path order)
		}
		exact := rel
		if exact == key {
			exact = "" // the common case: no second copy of the string
		}
		ix.files[key] = packFile{rel: exact, mount: mount}
		ix.entries++
		if ext := pathExt(key); ext != "" {
			if bit, ok := extBit(ext); ok {
				ix.stems[key[:len(key)-len(ext)]] |= bit
			}
		}
		return true
	})
}

// LookupStem returns the indexed-extension mask for a folded, extension-stripped
// rel. Zero allocations, and no lock: the maps are immutable after publication.
func (ix *MountIndex) LookupStem(foldedStem string) (uint16, bool) {
	m, ok := ix.stems[foldedStem]
	return m, ok
}

// LookupExact resolves a folded rel (WITH extension) to its location.
func (ix *MountIndex) LookupExact(foldedRel string) (packFile, bool) {
	f, ok := ix.files[foldedRel]
	return f, ok
}

// Covers reports whether the index holds anything for a folded rel, testing BOTH
// namespaces. Both are required: the probe path uploads textures under an
// extensionLESS base while the exact path (evidence, music) uses a full URL with
// its extension, so a stem-only test would silently miss every exact-keyed asset.
func (ix *MountIndex) Covers(folded string) bool {
	if _, ok := ix.stems[folded]; ok {
		return true
	}
	_, ok := ix.files[folded]
	return ok
}

// ReadFile returns one indexed file's bytes. Runs on pool workers.
func (ix *MountIndex) ReadFile(f packFile, foldedRel string) ([]byte, error) {
	if int(f.mount) >= len(ix.sources) {
		return nil, os.ErrNotExist
	}
	rel := f.rel
	if rel == "" {
		rel = foldedRel
	}
	return ix.sources[f.mount].read(rel)
}

// Stats reports what the settings status line shows.
func (ix *MountIndex) Stats() (files, mounts int, truncated bool) {
	return ix.entries, len(ix.mounts), ix.truncated
}

// Mounts returns the configured mount list this index was built from, so a
// caller can tell whether a pref change actually changed the mount SET.
func (ix *MountIndex) Mounts() []string { return ix.mounts }

// MarkBad quarantines a folded rel whose bytes failed to DECODE. Returns true on
// a new insert. Render thread only.
func (ix *MountIndex) MarkBad(foldedRel string) bool { return ix.bad.mark(foldedRel) }

// IsBad reports whether a folded rel is quarantined. Pool workers; the empty
// case costs one atomic load and takes no lock.
func (ix *MountIndex) IsBad(foldedRel string) bool { return ix.bad.has(foldedRel) }

// BadCount is the quarantine size, for the settings status line.
func (ix *MountIndex) BadCount() int64 { return ix.bad.n.Load() }

// acquire takes a read reference, returning false once the index is retired AND
// fully released. Callers that get true MUST call release.
func (ix *MountIndex) acquire() bool {
	if ix == nil {
		return false
	}
	for {
		n := ix.refs.Load()
		if n <= 0 {
			return false
		}
		if ix.refs.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// release drops a reference, closing the sources when the last one goes.
func (ix *MountIndex) release() {
	if ix == nil {
		return
	}
	if ix.refs.Add(-1) == 0 {
		ix.closeSources()
	}
}

// Retire drops the owner reference taken at build time. The index stops being
// acquirable, but any worker already inside a read keeps its handles until it
// releases — which is the whole reason archives are refcounted rather than
// closed when the layer pointer is swapped.
func (ix *MountIndex) Retire() {
	if ix == nil || ix.retired.Swap(true) {
		return
	}
	ix.release()
}

func (ix *MountIndex) closeSources() {
	ix.closeUp.Do(func() {
		for _, s := range ix.sources {
			_ = s.close()
		}
	})
}

// --- the quarantine ------------------------------------------------------------

// quarantine is the bounded set of pack entries that failed to decode. Written
// from the render thread (the upload pump's decode-error branch and the audio
// loader), read from pool workers on every pack lookup.
type quarantine struct {
	n   atomic.Int64
	mu  sync.Mutex
	set map[string]struct{}
	// ring records insertion order so eviction is oldest-first; see mountBadCap.
	ring [mountBadCap]string
	seq  int
}

// has is the hot path: the overwhelmingly common case is an empty quarantine, so
// it must cost one atomic load with no lock and no allocation.
func (q *quarantine) has(foldedRel string) bool {
	if q.n.Load() == 0 {
		return false
	}
	q.mu.Lock()
	_, bad := q.set[foldedRel]
	q.mu.Unlock()
	return bad
}

// mark inserts, evicting the oldest entry at the cap. Returns true only on a new
// insert, so the caller can log once per damaged file rather than once per retry.
func (q *quarantine) mark(foldedRel string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.set == nil {
		q.set = make(map[string]struct{})
	}
	if _, dup := q.set[foldedRel]; dup {
		return false
	}
	if len(q.set) >= mountBadCap {
		if old := q.ring[q.seq%mountBadCap]; old != "" {
			delete(q.set, old)
		}
	}
	q.set[foldedRel] = struct{}{}
	q.ring[q.seq%mountBadCap] = foldedRel
	q.seq++
	q.n.Store(int64(len(q.set)))
	return true
}

// --- key folding ---------------------------------------------------------------

// foldRel maps a URL-relative path onto the index's ONE key space: percent
// decoded per segment, then lowercased.
//
// It returns its input UNCHANGED — same string header, no allocation — when a
// single byte scan finds nothing to fold, which is the case for a typical
// all-lowercase AO path. Real character folders often DO need folding, though:
// the URL builder percent-escapes every segment, so "phoenix wright" arrives here
// as "phoenix%20wright". The cost is paid once per lookup on those, and never on
// the plain ones.
//
// Decoding per SEGMENT rather than over the whole string keeps a literal "%2F"
// inside a filename from inventing a path separator. Folding case is what makes a
// pack authored on Windows resolve identically on Linux, which is strictly better
// than today's reliance on NTFS being case-insensitive.
func foldRel(rel string) string {
	if !needsFold(rel) {
		return rel
	}
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if dec, ok := decodeSegment(part); ok {
			part = dec
		}
		parts[i] = strings.ToLower(part)
	}
	return strings.Join(parts, "/")
}

// needsFold is the zero-allocation fast path: a rel needs folding only if it
// holds an upper-case ASCII letter, a percent escape, a backslash, or a non-ASCII
// byte (which may be upper-case under Unicode rules).
func needsFold(rel string) bool {
	for i := 0; i < len(rel); i++ {
		c := rel[i]
		if (c >= 'A' && c <= 'Z') || c == '%' || c == '\\' || c >= 0x80 {
			return true
		}
	}
	return false
}

// decodeSegment percent-decodes one path segment. ok=false on a malformed escape
// — that is not a real filename, so the caller keeps the raw text rather than
// failing the lookup.
func decodeSegment(seg string) (string, bool) {
	if !strings.ContainsRune(seg, '%') {
		return seg, false
	}
	dec, err := url.PathUnescape(seg)
	if err != nil {
		return seg, false
	}
	return dec, true
}

// pathExt returns the final extension of a folded rel (including the dot), or ""
// when the last segment has none.
func pathExt(key string) string {
	for i := len(key) - 1; i >= 0 && key[i] != '/'; i-- {
		if key[i] == '.' {
			return key[i:]
		}
	}
	return ""
}

// --- sources -------------------------------------------------------------------

// isArchivePath reports whether a mount string names a .zip pack rather than a
// folder. Extension-based on purpose: it has to answer before the file is opened
// (for the open-archive cap) and it is what the Settings UI shows the user.
func isArchivePath(m string) bool {
	return strings.EqualFold(filepath.Ext(m), ".zip")
}

// openMountSource opens a folder or a .zip as a mount source.
func openMountSource(m string) (mountSource, error) {
	if isArchivePath(m) {
		return openZipSource(m)
	}
	st, err := os.Stat(m)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errNotAMount
	}
	return &dirSource{root: filepath.Clean(m)}, nil
}

// dirSource is a plain folder mount.
type dirSource struct{ root string }

func (d *dirSource) name() string { return d.root }
func (d *dirSource) close() error { return nil }
func (d *dirSource) read(rel string) ([]byte, error) {
	p, err := safepath.Join(d.root, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// walk indexes every regular file under the folder. SYMLINKS ARE SKIPPED and the
// recursion is depth-bounded — both live in safepath.WalkFiles, which is the one
// implementation this and internal/themepack share.
func (d *dirSource) walk(fn func(rel string) bool) error {
	return safepath.WalkFiles(d.root, mountIndexMaxDepth, fn)
}

// zipSource is a .zip pack mount.
//
// CONCURRENCY: zip.OpenReader parses the central directory once and keeps one
// os.File open. Each File.Open returns a fresh reader over an io.SectionReader on
// that shared *os.File, and os.File.ReadAt is safe for concurrent use, so several
// pool workers may read different entries at once. Nothing here mutates the
// Reader after construction.
//
// LIFETIME: the handle is owned by the MountIndex and closed only when the last
// reference drops (see acquire/release), never when the layer pointer is swapped
// — otherwise a Rescan during a read would close the file mid-ReadAt.
type zipSource struct {
	path   string
	rc     *zip.ReadCloser
	byName map[string]*zip.File
}

func (z *zipSource) name() string { return z.path }
func (z *zipSource) close() error { return z.rc.Close() }

func openZipSource(path string) (mountSource, error) {
	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	z := &zipSource{path: path, rc: rc, byName: make(map[string]*zip.File, len(rc.File))}
	for _, f := range rc.File {
		if f.FileInfo().IsDir() || safepath.UnsafeRel(f.Name) {
			continue // directory entries index nothing; zip-slip names are refused outright
		}
		if _, dup := z.byName[f.Name]; dup {
			continue // duplicate names are legal in the format; first wins, like mounts
		}
		z.byName[f.Name] = f
	}
	return z, nil
}

func (z *zipSource) walk(fn func(rel string) bool) error {
	for name := range z.byName {
		if !fn(name) { // zip names are already forward-slashed by the format
			return nil
		}
	}
	return nil
}

func (z *zipSource) read(rel string) ([]byte, error) {
	f, ok := z.byName[rel]
	if !ok {
		return nil, os.ErrNotExist
	}
	if f.UncompressedSize64 > mountZipEntryMaxBytes {
		return nil, errEntryTooLarge // attacker-controlled metadata; never allocate on its word
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := make([]byte, 0, f.UncompressedSize64)
	out, err := readAllInto(buf, rc, mountZipEntryMaxBytes)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// readAllInto reads r into buf's capacity, refusing to exceed limit — the header
// size is only a hint, so a lying zip cannot make this allocate without bound.
func readAllInto(buf []byte, r io.Reader, limit int64) ([]byte, error) {
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if int64(len(buf)) > limit {
			return nil, errEntryTooLarge
		}
		if err != nil {
			if err == io.EOF {
				return buf, nil
			}
			return nil, err
		}
	}
}
