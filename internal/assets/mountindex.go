package assets

// MountIndex maps local mount folders and .zip packs, so a lookup is a map hit instead of a directory walk.
// BuildMountIndex blocks, so never call it on the render thread (hard rule 2).

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
	// No entry-count or byte cap: local mounts are the user's and must never be silently truncated.
	mountIndexMaxDepth = safepath.MaxDepth
	// mountBadCap bounds the quarantine of pack files that failed to decode.
	mountBadCap = 256
	// mountArchiveCap bounds open .zip handles (hard rule 4); each holds a descriptor and its central directory.
	mountArchiveCap = 8
	// mountZipEntryMaxBytes refuses larger entries: the size is attacker-controlled and ReadFile allocates it whole.
	mountZipEntryMaxBytes = 64 << 20
)

// indexedExts is the stem mask's bit table, at most 16 entries; order does not matter, so append freely.
var indexedExts = [...]string{
	config.ExtPNG, config.ExtWebP, config.ExtAPNG, config.ExtGIF,
	config.ExtJPG, config.ExtAVIF,
	config.ExtOpus, config.ExtOgg, config.ExtWAV, config.ExtMP3,
}

// Fails to compile (unsigned overflow) if indexedExts exceeds the uint16 mask.
const _ = uint(16 - len(indexedExts))

var (
	// errNotAMount rejects a mount that is neither a folder nor a .zip.
	errNotAMount = errors.New("not a folder or a .zip pack")
	// errTooManyArchives reports the mountArchiveCap refusal.
	errTooManyArchives = errors.New("too many .zip packs mounted")
	// errEntryTooLarge refuses an entry over mountZipEntryMaxBytes.
	errEntryTooLarge = errors.New("pack entry is too large")
)

// mountError names the mount a build error came from, for the Settings status line.
type mountError struct {
	mount string
	err   error
}

func (e *mountError) Error() string { return "assets: mount " + e.mount + ": " + e.err.Error() }
func (e *mountError) Unwrap() error { return e.err }

// Mount reports which configured mount failed.
func (e *mountError) Mount() string { return e.mount }

// extBit returns ext's mask bit and whether it is indexed; an unindexed one falls back to an exact-key probe.
func extBit(ext string) (uint16, bool) {
	for i, e := range indexedExts {
		if e == ext {
			return 1 << uint(i), true
		}
	}
	return 0, false
}

// packFile locates one indexed file; rel is empty when it equals the folded key, saving string data.
type packFile struct {
	rel   string
	mount uint8
}

// MountIndex maps local mount contents; see the file header.
type MountIndex struct {
	sources []mountSource
	mounts  []string // display/identity copy of the configured mount list

	files map[string]packFile // folded rel (WITH extension) -> location
	stems map[string]uint16   // folded rel WITHOUT extension -> indexed-extension mask

	entries int

	bad quarantine

	// refs keeps archives open across a Rescan: close comes with the last reference, not the pointer swap.
	refs    atomic.Int32
	retired atomic.Bool
	closeUp sync.Once
}

// mountSource is a folder or archive, read-only once built; read must be safe for concurrent use.
type mountSource interface {
	// walk visits every file with a forward-slash relative path; returning false stops it.
	walk(fn func(rel string) bool) error
	read(rel string) ([]byte, error)
	// close releases any OS handle; call it once.
	close() error
	name() string
}

// BuildMountIndex walks every mount in order and blocks; a mount that cannot open is skipped with its error.
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
	}
	return ix, errs
}

func (ix *MountIndex) absorb(src mountSource, mount uint8) error {
	return src.walk(func(rel string) bool {
		// Folder walks are depth-bounded by safepath.WalkFiles; zip names come straight from the central directory.
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

// LookupStem returns the extension mask for a folded stem, lock-free as the maps are immutable once published.
func (ix *MountIndex) LookupStem(foldedStem string) (uint16, bool) {
	m, ok := ix.stems[foldedStem]
	return m, ok
}

// LookupExact resolves a folded rel, with extension, to its location.
func (ix *MountIndex) LookupExact(foldedRel string) (packFile, bool) {
	f, ok := ix.files[foldedRel]
	return f, ok
}

// Covers tests both namespaces, as a stem-only test would miss every exact-keyed asset.
func (ix *MountIndex) Covers(folded string) bool {
	if _, ok := ix.stems[folded]; ok {
		return true
	}
	_, ok := ix.files[folded]
	return ok
}

// ReadFile returns one indexed file's bytes; pool workers only.
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

// Stats reports the file and mount counts.
func (ix *MountIndex) Stats() (files, mounts int) {
	return ix.entries, len(ix.mounts)
}

// Mounts returns the mount list, so a caller can tell whether a pref change changed the set.
func (ix *MountIndex) Mounts() []string { return ix.mounts }

// MarkBad quarantines a folded rel whose bytes failed to decode, returning true on a new insert; render thread only.
func (ix *MountIndex) MarkBad(foldedRel string) bool { return ix.bad.mark(foldedRel) }

// IsBad reports whether a folded rel is quarantined; pool workers, one atomic load when empty.
func (ix *MountIndex) IsBad(foldedRel string) bool { return ix.bad.has(foldedRel) }

// BadCount returns the quarantine size.
func (ix *MountIndex) BadCount() int64 { return ix.bad.n.Load() }

// acquire takes a read reference, false once retired and released; on true the caller must call release.
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

func (ix *MountIndex) release() {
	if ix == nil {
		return
	}
	if ix.refs.Add(-1) == 0 {
		ix.closeSources()
	}
}

// Retire drops the owner reference; a worker already reading keeps its handles until it releases.
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

// quarantine is the bounded set of entries that failed to decode, written by render, read by pool workers.
type quarantine struct {
	n   atomic.Int64
	mu  sync.Mutex
	set map[string]struct{}
	// ring records insertion order for oldest-first eviction.
	ring [mountBadCap]string
	seq  int
}

// has is the hot path: an empty quarantine must cost one atomic load, with no lock or allocation.
func (q *quarantine) has(foldedRel string) bool {
	if q.n.Load() == 0 {
		return false
	}
	q.mu.Lock()
	_, bad := q.set[foldedRel]
	q.mu.Unlock()
	return bad
}

// mark inserts, evicting the oldest at the cap; true only on a new insert, so callers log once per file.
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

// foldRel percent-decodes per segment so "%2F" cannot invent a separator, then lowercases for case-sensitive disks.
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

// needsFold is the zero-allocation fast path: true for upper-case ASCII, a percent escape, a backslash or non-ASCII.
func needsFold(rel string) bool {
	for i := 0; i < len(rel); i++ {
		c := rel[i]
		if (c >= 'A' && c <= 'Z') || c == '%' || c == '\\' || c >= 0x80 {
			return true
		}
	}
	return false
}

// decodeSegment percent-decodes one segment; on a malformed escape, ok=false and the caller keeps the raw text.
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

// pathExt returns a folded rel's final extension including the dot, or "".
func pathExt(key string) string {
	for i := len(key) - 1; i >= 0 && key[i] != '/'; i-- {
		if key[i] == '.' {
			return key[i:]
		}
	}
	return ""
}

// isArchivePath checks the .zip extension, since it must answer before the open-archive cap is checked.
func isArchivePath(m string) bool {
	return strings.EqualFold(filepath.Ext(m), ".zip")
}

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

// walk indexes every regular file; symlinks are skipped and depth is bounded by safepath.WalkFiles.
func (d *dirSource) walk(fn func(rel string) bool) error {
	return safepath.WalkFiles(d.root, mountIndexMaxDepth, fn)
}

// zipSource is a .zip mount, closed with the last reference, or a Rescan could close it mid-ReadAt.
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

// readAllInto refuses to exceed limit: the header size is only a hint, and a zip can lie.
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
