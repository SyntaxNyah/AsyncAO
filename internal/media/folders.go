package media

// Folders is the player's folders and .zip packs as one source, read a folder at a time. A folder's listing is a page;
// readers find pages with no lock, and the one writer replaces a page whole, never changes it. A miss re-checks a
// folder once its check is no longer trusted: a create, delete or rename in a folder changes its modified time, so one
// stat per mount says whether to list it again. Stale ends every check's trust, for when the player may have changed
// the folders. .zip packs are indexed when mounted and never change under a page.

import (
	"archive/zip"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/safepath"
)

const (
	// folderTrust is how long a folder check answers without asking the disk again, unless Stale ends it first.
	folderTrust = 30 * time.Second
	// folderPageCap bounds the pages kept (hard rule 4); past it they start over, which only costs re-reading folders.
	folderPageCap = 8192
	// folderMountCap bounds the mounts, as a page file names its mount in one byte.
	folderMountCap = 255
	// zipMountCap bounds open .zip packs (hard rule 4); each holds a descriptor and its central directory.
	zipMountCap = 8
	// zipEntryMaxBytes refuses larger entries: the size is the archive's word, and reading allocates it whole.
	zipEntryMaxBytes = 64 << 20
	// folderMaxDepth is the deepest relative path answered, as for every other walk of user folders.
	folderMaxDepth = safepath.MaxDepth
)

var (
	// errNotAMount rejects a mount that is neither a folder nor a .zip.
	errNotAMount = errors.New("not a folder or a .zip pack")
	// errTooManyZips reports the zipMountCap refusal.
	errTooManyZips = errors.New("too many .zip packs mounted")
	// errEntryTooLarge refuses an entry over zipEntryMaxBytes.
	errEntryTooLarge = errors.New("pack entry is too large")
)

// MountError names the mount a Mount error came from, for the Settings status line.
type MountError struct {
	Mount string
	Err   error
}

func (e *MountError) Error() string { return "media: mount " + e.Mount + ": " + e.Err.Error() }
func (e *MountError) Unwrap() error { return e.Err }

// Folders is described above.
type Folders struct {
	state atomic.Pointer[folderSet]
	mu    sync.Mutex // held only by Mount, the one writer of state
	now   func() time.Time
}

// NewFolders starts with no mounts; now is the clock, time.Now outside tests.
func NewFolders(now func() time.Time) *Folders { return &Folders{now: now} }

// folderSet is one published state: the mounts, the URL prefixes that map into them, and their pages.
type folderSet struct {
	mounts  *mountList
	origins []string
	pages   *pageTable
}

// mountList is an opened mount set, closed with its last reader, so a swap never closes a .zip mid-read.
type mountList struct {
	paths   []string
	mounts  []folderMount
	errs    []error
	refs    atomic.Int32
	closeUp sync.Once
}

// pageTable holds a mount set's pages; gen is bumped by Stale, and a page from an older gen is not trusted.
type pageTable struct {
	m   sync.Map // folded folder path to *page
	n   atomic.Int64
	gen atomic.Uint64
}

// page is one folder's listing across every mount, first mount winning, with what it was checked against.
type page struct {
	gen   uint64
	at    time.Time
	real  []string    // per folder mount, the folder's on-disk path, "" where it has none
	mod   []time.Time // per folder mount, the folder's modified time when listed
	files map[string]pageFile
}

// pageFile is where one file of a page lives: its mount and its on-disk name.
type pageFile struct {
	mount uint8
	name  string
}

// folderMount is a folder or a .zip pack.
type folderMount interface {
	close() error
}

// Mount replaces the mount set and the URL prefixes that map into it; an unchanged mount set keeps its pages and
// open packs. It returns the mounts that could not be opened. Call it off the render thread: it opens .zip packs.
func (fs *Folders) Mount(paths, origins []string) []error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	cur := fs.state.Load()
	if cur != nil && slices.Equal(cur.mounts.paths, paths) {
		fs.state.Store(&folderSet{mounts: cur.mounts, origins: origins, pages: cur.pages})
		return cur.mounts.errs
	}
	var next *folderSet
	if len(paths) > 0 {
		list := openMounts(paths)
		next = &folderSet{mounts: list, origins: origins, pages: &pageTable{}}
	}
	fs.state.Store(next)
	if cur != nil {
		cur.mounts.release() // the publishing reference; readers keep theirs until done
	}
	if next == nil {
		return nil
	}
	return next.mounts.errs
}

// Stale ends every folder check's trust, so the next miss in each folder looks at the disk again.
func (fs *Folders) Stale() {
	if s := fs.state.Load(); s != nil {
		s.pages.gen.Add(1)
	}
}

// Mounts returns the mounted paths, nil when none.
func (fs *Folders) Mounts() []string {
	if s := fs.state.Load(); s != nil {
		return s.mounts.paths
	}
	return nil
}

// Errors returns the mounts that could not be opened.
func (fs *Folders) Errors() []error {
	if s := fs.state.Load(); s != nil {
		return s.mounts.errs
	}
	return nil
}

// Has answers from the pages with no I/O: Unknown for a folder not checked lately, so only a worker looks.
func (fs *Folders) Has(f File) Answer {
	s := fs.state.Load()
	if s == nil {
		return No
	}
	dir, name, ok := s.locate(string(f))
	if !ok {
		return No
	}
	p := s.pages.load(dir)
	if p == nil || !s.pages.fresh(p, fs.now()) {
		return Unknown
	}
	if _, ok := p.files[name]; ok {
		return Yes
	}
	return No
}

// Data reads the file, checking its folder first when the check is no longer trusted; pool workers only.
func (fs *Folders) Data(f File) ([]byte, error) {
	s := fs.state.Load()
	if s == nil {
		return nil, ErrAbsent
	}
	dir, name, ok := s.locate(string(f))
	if !ok {
		return nil, ErrAbsent
	}
	return s.read(dir, name, fs.now())
}

// Size reports the pages held.
func (fs *Folders) Size() Contents {
	n := 0
	if s := fs.state.Load(); s != nil {
		n = int(s.pages.n.Load())
	}
	return Contents{Name: "folders", Entries: n}
}

// Read reads a path relative to the mounts, checking its folder like Data; workers only.
func (fs *Folders) Read(rel string) ([]byte, error) {
	s := fs.state.Load()
	if s == nil {
		return nil, ErrAbsent
	}
	dir, name, ok := splitFolded(foldPath(rel))
	if !ok {
		return nil, ErrAbsent
	}
	return s.read(dir, name, fs.now())
}

// Covers reports whether a URL maps into the mounts and a trusted page holds it, with no I/O, so on any thread.
func (fs *Folders) Covers(rawURL string) bool {
	return fs.Has(File(rawURL)) == Yes
}

// RelOf strips the longest mapped prefix from a URL, or reports that the URL is not under the mounts.
func (fs *Folders) RelOf(rawURL string) (string, bool) {
	s := fs.state.Load()
	if s == nil {
		return "", false
	}
	return s.relOf(rawURL)
}

func (s *folderSet) relOf(rawURL string) (string, bool) {
	best, bestLen := "", -1
	for _, o := range s.origins {
		if len(o) > bestLen && strings.HasPrefix(rawURL, o) {
			best, bestLen = rawURL[len(o):], len(o)
		}
	}
	return best, bestLen >= 0
}

// locate maps a URL to its folded folder and name; a server's format list is never answered from a pack, or it
// would reseed the host's order.
func (s *folderSet) locate(rawURL string) (dir, name string, ok bool) {
	rel, ok := s.relOf(rawURL)
	if !ok || rel == "" || strings.HasSuffix(rel, "/") {
		return "", "", false
	}
	dir, name, ok = splitFolded(foldPath(rel))
	if !ok || name == ManifestFile {
		return "", "", false
	}
	return dir, name, true
}

func (s *folderSet) read(dir, name string, now time.Time) ([]byte, error) {
	if !s.mounts.acquire() {
		return nil, ErrAbsent // swapped out and closed between load and read
	}
	defer s.mounts.release()
	p := s.refresh(dir, now)
	pf, ok := p.files[name]
	if !ok {
		return nil, ErrAbsent
	}
	switch m := s.mounts.mounts[pf.mount].(type) {
	case *dirMount:
		return m.read(path.Join(p.real[pf.mount], pf.name))
	case *zipMount:
		return m.read(pf.name)
	}
	return nil, ErrAbsent
}

// refresh returns dir's page, listing it again only if a folder mount's copy changed since its last check.
func (s *folderSet) refresh(dir string, now time.Time) *page {
	prev := s.pages.load(dir)
	if prev != nil && s.pages.fresh(prev, now) {
		return prev
	}
	mounts := s.mounts.mounts
	next := &page{gen: s.pages.gen.Load(), at: now, real: make([]string, len(mounts)), mod: make([]time.Time, len(mounts))}
	changed := prev == nil
	for i, m := range mounts {
		d, ok := m.(*dirMount)
		if !ok {
			continue
		}
		if real, mod, found := d.resolve(dir); found {
			next.real[i], next.mod[i] = real, mod
		}
		if prev != nil && (prev.real[i] != next.real[i] || !prev.mod[i].Equal(next.mod[i])) {
			changed = true
		}
	}
	if changed {
		next.files = s.list(dir, next.real)
	} else {
		next.files = prev.files
	}
	s.pages.store(dir, next)
	return next
}

// list builds a page's files, first mount winning (AO2 mount-path order).
func (s *folderSet) list(dir string, real []string) map[string]pageFile {
	files := map[string]pageFile{}
	for i, m := range s.mounts.mounts {
		var names []string
		switch m := m.(type) {
		case *dirMount:
			if real[i] != "" || dir == "" {
				names = m.list(real[i])
			}
		case *zipMount:
			names = m.dirs[dir]
		}
		for _, n := range names {
			key := foldSegment(path.Base(n))
			if _, dup := files[key]; !dup {
				files[key] = pageFile{mount: uint8(i), name: n}
			}
		}
	}
	return files
}

func (t *pageTable) load(dir string) *page {
	if v, ok := t.m.Load(dir); ok {
		return v.(*page)
	}
	return nil
}

func (t *pageTable) store(dir string, p *page) {
	if _, loaded := t.m.Swap(dir, p); loaded {
		return
	}
	if t.n.Add(1) > folderPageCap {
		t.m.Clear()
		t.n.Store(0)
	}
}

func (t *pageTable) fresh(p *page, now time.Time) bool {
	return p.gen == t.gen.Load() && now.Sub(p.at) < folderTrust
}

func openMounts(paths []string) *mountList {
	list := &mountList{paths: append([]string(nil), paths...)}
	list.refs.Store(1) // the publishing reference, dropped when a later Mount replaces it
	zips := 0
	for _, p := range paths {
		if p == "" || len(list.mounts) >= folderMountCap {
			continue
		}
		var m folderMount
		var err error
		if strings.EqualFold(filepath.Ext(p), ".zip") {
			if zips >= zipMountCap {
				err = errTooManyZips
			} else if m, err = openZip(p); err == nil {
				zips++
			}
		} else {
			m, err = openDir(p)
		}
		if err != nil {
			list.errs = append(list.errs, &MountError{Mount: p, Err: err})
			continue
		}
		list.mounts = append(list.mounts, m)
	}
	return list
}

func (l *mountList) acquire() bool {
	for {
		n := l.refs.Load()
		if n <= 0 {
			return false
		}
		if l.refs.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (l *mountList) release() {
	if l.refs.Add(-1) == 0 {
		l.closeUp.Do(func() {
			for _, m := range l.mounts {
				_ = m.close()
			}
		})
	}
}

// dirMount is a folder.
type dirMount struct{ root string }

func openDir(p string) (*dirMount, error) {
	st, err := os.Stat(p) // Stat, so a mount folder that is itself a link is accepted
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, errNotAMount
	}
	return &dirMount{root: filepath.Clean(p)}, nil
}

func (d *dirMount) close() error { return nil }

func (d *dirMount) read(rel string) ([]byte, error) {
	p, err := safepath.Join(d.root, rel)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		return nil, ErrAbsent // gone since the listing, or empty: the next source answers
	}
	return b, nil
}

// resolve finds the on-disk folder for a folded path, segment by segment so case may differ and no link is followed,
// as no walk of user folders follows one.
func (d *dirMount) resolve(folded string) (real string, mod time.Time, ok bool) {
	st, err := os.Stat(d.root)
	if err != nil || !st.IsDir() {
		return "", time.Time{}, false
	}
	mod = st.ModTime()
	if folded == "" {
		return "", mod, true
	}
	if safepath.UnsafeRel(folded) || safepath.TooDeep(folded, folderMaxDepth) {
		return "", time.Time{}, false
	}
	for _, seg := range strings.Split(folded, "/") {
		name, segMod, found := d.child(real, seg)
		if !found {
			return "", time.Time{}, false
		}
		real, mod = path.Join(real, name), segMod
	}
	return real, mod, true
}

// child finds the folder under parent whose folded name is seg: the name as written first, then a listing.
func (d *dirMount) child(parent, seg string) (string, time.Time, bool) {
	if p, err := safepath.Join(d.root, path.Join(parent, seg)); err == nil {
		if st, err := os.Lstat(p); err == nil && st.IsDir() {
			return seg, st.ModTime(), true
		}
	}
	p, err := safepath.Join(d.root, parent)
	if err != nil {
		return "", time.Time{}, false
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", time.Time{}, false
	}
	for _, e := range entries {
		if e.IsDir() && foldSegment(e.Name()) == seg {
			st, err := e.Info()
			if err != nil {
				return "", time.Time{}, false
			}
			return e.Name(), st.ModTime(), true
		}
	}
	return "", time.Time{}, false
}

// list returns the names of the regular files directly in real; folders, links and devices are left out.
func (d *dirMount) list(real string) []string {
	p, err := safepath.Join(d.root, real)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type().IsRegular() && !safepath.TooDeep(path.Join(real, e.Name()), folderMaxDepth) {
			names = append(names, e.Name())
		}
	}
	return names
}

// zipMount is a .zip pack, indexed by folded folder when mounted.
type zipMount struct {
	rc     *zip.ReadCloser
	byName map[string]*zip.File
	dirs   map[string][]string // folded folder to the entry names directly in it
}

func openZip(p string) (*zipMount, error) {
	rc, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	z := &zipMount{rc: rc, byName: make(map[string]*zip.File, len(rc.File)), dirs: map[string][]string{}}
	for _, f := range rc.File {
		if f.FileInfo().IsDir() || safepath.UnsafeRel(f.Name) || safepath.TooDeep(f.Name, folderMaxDepth) {
			continue // folder entries hold nothing; a name escaping the pack is refused outright
		}
		if _, dup := z.byName[f.Name]; dup {
			continue // duplicate names are legal in the format; the first wins, like mounts
		}
		z.byName[f.Name] = f
		dir, _, ok := splitFolded(foldPath(f.Name))
		if ok {
			z.dirs[dir] = append(z.dirs[dir], f.Name)
		}
	}
	return z, nil
}

func (z *zipMount) close() error { return z.rc.Close() }

func (z *zipMount) read(name string) ([]byte, error) {
	f, ok := z.byName[name]
	if !ok {
		return nil, ErrAbsent
	}
	if f.UncompressedSize64 > zipEntryMaxBytes {
		return nil, errEntryTooLarge
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, zipEntryMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > zipEntryMaxBytes {
		return nil, errEntryTooLarge // the header size is only the archive's word
	}
	return b, nil
}

// foldPath percent-decodes each segment, so "%2F" cannot invent a separator, then lowercases for case-sensitive disks.
func foldPath(rel string) string {
	if !needsFold(rel) {
		return rel
	}
	parts := strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/")
	for i, part := range parts {
		parts[i] = foldSegment(part)
	}
	return strings.Join(parts, "/")
}

func foldSegment(seg string) string {
	if !needsFold(seg) {
		return seg
	}
	if strings.ContainsRune(seg, '%') {
		if dec, err := url.PathUnescape(seg); err == nil {
			seg = dec
		}
	}
	return strings.ToLower(seg)
}

// needsFold is the allocation-free fast path: true for upper-case ASCII, a percent escape, a backslash or non-ASCII.
func needsFold(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || c == '%' || c == '\\' || c >= 0x80 {
			return true
		}
	}
	return false
}

// splitFolded splits a folded relative path into its folder ("" at a mount's root) and name.
func splitFolded(folded string) (dir, name string, ok bool) {
	if folded == "" || safepath.UnsafeRel(folded) || safepath.TooDeep(folded, folderMaxDepth) {
		return "", "", false
	}
	i := strings.LastIndexByte(folded, '/')
	if i < 0 {
		return "", folded, true
	}
	return folded[:i], folded[i+1:], folded[i+1:] != ""
}
