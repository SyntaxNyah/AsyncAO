package assets

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestMountIndexFoldsCase is the case that passes on this Windows box by luck and
// fails on Linux without the exact-rel value. A pack authored with capitals and
// spaces must resolve from the lowercase, percent-escaped rel the URL builder
// produces AND still be readable, which requires the index to remember the EXACT
// on-disk name alongside the folded key.
func TestMountIndexFoldsCase(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, map[string]string{
		"characters/Phoenix Wright/(a)Normal.png": "sprite-bytes",
	})
	ix, errs := BuildMountIndex([]string{dir})
	defer ix.Retire()
	if len(errs) != 0 {
		t.Fatalf("build errors: %v", errs)
	}

	// This is exactly what the URL builder yields: percent-escaped, and the
	// server's own path casing has nothing to do with the pack's.
	key := foldRel("characters/Phoenix%20Wright/(a)Normal.png")
	f, ok := ix.LookupExact(key)
	if !ok {
		t.Fatalf("folded key %q not found; index holds %d entries", key, ix.entries)
	}
	data, err := ix.ReadFile(f, key)
	if err != nil {
		t.Fatalf("ReadFile on a case-folded hit: %v", err)
	}
	if string(data) != "sprite-bytes" {
		t.Errorf("read %q, want the pack's bytes", data)
	}
}

// TestFoldRelLeavesPlainPathsUntouched pins the zero-allocation fast path: a
// typical all-lowercase AO rel must come back as the SAME string, not a copy.
func TestFoldRelLeavesPlainPathsUntouched(t *testing.T) {
	plain := "characters/phoenix/(a)normal.png"
	if got := foldRel(plain); got != plain {
		t.Fatalf("foldRel changed a plain rel: %q", got)
	}
	if n := testing.AllocsPerRun(500, func() { _ = foldRel(plain) }); n != 0 {
		t.Errorf("foldRel allocates %.1f/op on a plain rel, want 0", n)
	}
	// And it really does fold when it must.
	if got := foldRel("characters/Phoenix%20Wright/A.PNG"); got != "characters/phoenix wright/a.png" {
		t.Errorf("foldRel = %q", got)
	}
}

// TestMountIndexLookupIsZeroAlloc pins the hot path. A cold room runs this once
// per candidate extension per sprite spelling per asset, so an allocation here
// multiplies by thousands.
func TestMountIndexLookupIsZeroAlloc(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, map[string]string{"characters/witch/(a)normal.png": "x"})
	ix, _ := BuildMountIndex([]string{dir})
	defer ix.Retire()

	hit := "characters/witch/(a)normal"
	miss := "characters/nobody/(a)normal"
	if n := testing.AllocsPerRun(500, func() { _, _ = ix.LookupStem(hit) }); n != 0 {
		t.Errorf("LookupStem hit allocates %.1f/op, want 0", n)
	}
	if n := testing.AllocsPerRun(500, func() { _, _ = ix.LookupStem(miss) }); n != 0 {
		t.Errorf("LookupStem miss allocates %.1f/op, want 0", n)
	}
	if n := testing.AllocsPerRun(500, func() { _ = ix.IsBad(hit) }); n != 0 {
		t.Errorf("IsBad on an empty quarantine allocates %.1f/op, want 0", n)
	}
}

// TestMountIndexStemMaskFindsEveryFormat pins the mask: a pack holding the same
// emote in two formats must report both bits, so the lookup can pick whichever
// the probe chain asks for first without touching the disk.
func TestMountIndexStemMaskFindsEveryFormat(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, map[string]string{
		"characters/witch/(a)normal.png":  "png",
		"characters/witch/(a)normal.webp": "webp",
		"characters/witch/(b)normal.gif":  "gif",
	})
	ix, _ := BuildMountIndex([]string{dir})
	defer ix.Retire()

	mask, ok := ix.LookupStem("characters/witch/(a)normal")
	if !ok {
		t.Fatal("stem not indexed")
	}
	for _, ext := range []string{".png", ".webp"} {
		bit, _ := extBit(ext)
		if mask&bit == 0 {
			t.Errorf("mask %b missing the %s bit", mask, ext)
		}
	}
	if bit, _ := extBit(".gif"); mask&bit != 0 {
		t.Error("the (a) stem reports a .gif that belongs to the (b) stem")
	}
}

// TestMountIndexFirstMountWins pins AO2 mount-path order: earlier mounts shadow
// later ones, which is what makes an override pack work.
func TestMountIndexFirstMountWins(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writePack(t, first, map[string]string{"characters/witch/(a)normal.png": "FIRST"})
	writePack(t, second, map[string]string{"characters/witch/(a)normal.png": "SECOND"})

	ix, _ := BuildMountIndex([]string{first, second})
	defer ix.Retire()
	key := "characters/witch/(a)normal.png"
	f, ok := ix.LookupExact(key)
	if !ok {
		t.Fatal("not indexed")
	}
	data, err := ix.ReadFile(f, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "FIRST" {
		t.Errorf("read %q, want FIRST — mount order is not first-hit-wins", data)
	}
}

// TestMountIndexSkipsSymlinks closes an exfiltration route: a third-party pack
// could otherwise ship a link at a plausible asset path pointing at a private
// file, which would be indexed, read, and uploaded as a texture. The ".." guard
// does not cover it, because the link's own path contains no "..".
func TestMountIndexSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "characters", "witch"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "characters", "witch", "(a)normal.png")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err) // Windows without dev mode
	}

	ix, _ := BuildMountIndex([]string{dir})
	defer ix.Retire()
	if _, ok := ix.LookupExact("characters/witch/(a)normal.png"); ok {
		t.Error("a symlinked file was indexed — it would be read and uploaded as a texture")
	}
}

// The path-escape PREDICATE gate moved to internal/safepath with the predicate
// itself (TestUnsafeRelRefusesPathEscape): it tested a pure function and nothing
// about mounting, and it now covers the theme packer's entry names too. What
// stays here are the two gates that exercise the mount PLUMBING —
// TestMountIndexSkipsSymlinks and TestMountIndexZipSlipRefused — because a guard
// that is implemented and simply not wired up is the failure this file catches.

// TestMountIndexZipPack pins the .zip mount end to end, with the fixture built in
// the test so no binary lands in the repo.
func TestMountIndexZipPack(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"characters/Witch/(a)Normal.png":     "zip-sprite",
		"background/court/defenseempty.webp": "zip-bg",
		"characters/witch/":                  "", // a directory entry must index nothing
	})

	ix, errs := BuildMountIndex([]string{zipPath})
	defer ix.Retire()
	if len(errs) != 0 {
		t.Fatalf("build errors: %v", errs)
	}
	files, mounts, truncated := ix.Stats()
	if truncated {
		t.Error("a three-entry zip reported truncated")
	}
	if mounts != 1 {
		t.Errorf("mounts = %d, want 1", mounts)
	}
	if files != 2 {
		t.Errorf("indexed %d files, want 2 (the directory entry must not count)", files)
	}

	key := foldRel("characters/Witch/(a)Normal.png")
	f, ok := ix.LookupExact(key)
	if !ok {
		t.Fatalf("zip entry %q not indexed", key)
	}
	data, err := ix.ReadFile(f, key)
	if err != nil {
		t.Fatalf("reading a zip entry: %v", err)
	}
	if string(data) != "zip-sprite" {
		t.Errorf("read %q from the zip", data)
	}
}

// TestMountIndexZipAndFolderInterleave pins that the two source kinds share one
// key space and one mount order — a folder can shadow a zip and vice versa.
func TestMountIndexZipAndFolderInterleave(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, map[string]string{"characters/witch/(a)normal.png": "FROM-FOLDER"})
	zipPath := writeZip(t, map[string]string{
		"characters/witch/(a)normal.png": "FROM-ZIP",
		"characters/witch/(b)normal.png": "ZIP-ONLY",
	})

	ix, _ := BuildMountIndex([]string{dir, zipPath}) // folder first
	defer ix.Retire()

	if got := mustRead(t, ix, "characters/witch/(a)normal.png"); got != "FROM-FOLDER" {
		t.Errorf("shared path resolved to %q, want the earlier FOLDER mount", got)
	}
	if got := mustRead(t, ix, "characters/witch/(b)normal.png"); got != "ZIP-ONLY" {
		t.Errorf("zip-only path resolved to %q", got)
	}
}

// TestMountIndexZipSlipRefused pins that a hostile archive cannot name an entry
// that escapes the pack.
func TestMountIndexZipSlipRefused(t *testing.T) {
	zipPath := writeZip(t, map[string]string{
		"../../evil.png":                 "pwned",
		"characters/witch/(a)normal.png": "fine",
	})
	ix, _ := BuildMountIndex([]string{zipPath})
	defer ix.Retire()

	if files, _, _ := ix.Stats(); files != 1 {
		t.Errorf("indexed %d entries, want 1 — the escaping name was not refused", files)
	}
	if _, ok := ix.LookupExact("../../evil.png"); ok {
		t.Error("a zip-slip entry is reachable")
	}
}

// fakeSource is a mountSource that yields synthetic rels without touching the
// disk, so the cap can be exercised at full scale in milliseconds instead of
// writing tens of thousands of real files into the gate's critical path.
type fakeSource struct{ n int }

func (f *fakeSource) name() string                    { return "fake" }
func (f *fakeSource) close() error                    { return nil }
func (f *fakeSource) read(rel string) ([]byte, error) { return []byte(rel), nil }
func (f *fakeSource) walk(fn func(rel string) bool) error {
	for i := 0; i < f.n; i++ {
		if !fn("characters/c" + strconv.Itoa(i) + "/(a)normal.png") {
			return nil
		}
	}
	return nil
}

// TestMountIndexByteCapStopsTheWalk pins that the cap is enforced DURING the walk
// — it stops absorbing rather than discovering the overrun afterwards — and that
// the partial index still serves. Degrading is always safe here because the layer
// only ever ADDS hits over the network path.
func TestMountIndexByteCapStopsTheWalk(t *testing.T) {
	ix := &MountIndex{files: map[string]packFile{}, stems: map[string]uint16{}}
	ix.refs.Store(1)
	defer ix.Retire()
	src := &fakeSource{n: mountIndexMaxEntries + 64}
	ix.sources = append(ix.sources, src)
	if err := ix.absorb(src, 0); err != nil {
		t.Fatal(err)
	}

	got, _, truncated := ix.Stats()
	if !truncated {
		t.Error("the cap was reached without reporting truncated")
	}
	if got > mountIndexMaxEntries {
		t.Errorf("indexed %d entries, past the %d cap", got, mountIndexMaxEntries)
	}
	// The partial index still resolves what it did manage to index.
	if _, ok := ix.LookupExact("characters/c0/(a)normal.png"); !ok {
		t.Error("a truncated index lost the entries it had already absorbed")
	}
}

// TestMountIndexDepthCapSkipsDeepPaths pins the recursion bound.
func TestMountIndexDepthCapSkipsDeepPaths(t *testing.T) {
	ix := &MountIndex{files: map[string]packFile{}, stems: map[string]uint16{}}
	ix.refs.Store(1)
	defer ix.Retire()
	deep := strings.Repeat("a/", mountIndexMaxDepth+2) + "sprite.png"
	src := &stubWalker{rels: []string{deep, "characters/witch/(a)normal.png"}}
	ix.sources = append(ix.sources, src)
	if err := ix.absorb(src, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.LookupExact(deep); ok {
		t.Error("a path past mountIndexMaxDepth was indexed")
	}
	if _, ok := ix.LookupExact("characters/witch/(a)normal.png"); !ok {
		t.Error("a normal path was dropped alongside the deep one")
	}
}

type stubWalker struct{ rels []string }

func (s *stubWalker) name() string                    { return "stub" }
func (s *stubWalker) close() error                    { return nil }
func (s *stubWalker) read(rel string) ([]byte, error) { return []byte(rel), nil }
func (s *stubWalker) walk(fn func(rel string) bool) error {
	for _, r := range s.rels {
		if !fn(r) {
			return nil
		}
	}
	return nil
}

// TestQuarantineEvictsOldestAtCap pins the eviction DIRECTION, which is a
// correctness property here rather than a UX one: if a full quarantine refused
// new inserts, the currently demanded corrupt file would keep winning the lookup
// and its asset would be permanently missingno.
func TestQuarantineEvictsOldestAtCap(t *testing.T) {
	var q quarantine
	first := "characters/c0/(a)normal.png"
	for i := 0; i < mountBadCap; i++ {
		q.mark("characters/c" + strconv.Itoa(i) + "/(a)normal.png")
	}
	if !q.has(first) {
		t.Fatal("fixture: the first entry should still be quarantined at exactly the cap")
	}
	newest := "characters/newest/(a)normal.png"
	q.mark(newest)

	if q.n.Load() > mountBadCap {
		t.Errorf("quarantine grew to %d, past the %d cap", q.n.Load(), mountBadCap)
	}
	if q.has(first) {
		t.Error("the OLDEST entry survived eviction")
	}
	if !q.has(newest) {
		t.Error("the NEWEST entry was refused — the currently demanded asset would never heal")
	}
}

// TestQuarantineMarkReportsNewInsertsOnly lets the caller log once per damaged
// file instead of once per retry.
func TestQuarantineMarkReportsNewInsertsOnly(t *testing.T) {
	var q quarantine
	if !q.mark("a.png") {
		t.Error("the first mark should report a new insert")
	}
	if q.mark("a.png") {
		t.Error("a repeat mark reported a new insert")
	}
}

// TestRescanForgetsQuarantine pins that rebuilding the index clears it: a rescan
// is the user asserting their folders changed, which is the only realistic moment
// a damaged file gets fixed.
func TestRescanForgetsQuarantine(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, map[string]string{"characters/witch/(a)normal.png": "x"})
	ix, _ := BuildMountIndex([]string{dir})
	ix.MarkBad("characters/witch/(a)normal.png")
	if !ix.IsBad("characters/witch/(a)normal.png") {
		t.Fatal("fixture: mark did not take")
	}
	ix.Retire()

	fresh, _ := BuildMountIndex([]string{dir})
	defer fresh.Retire()
	if fresh.IsBad("characters/witch/(a)normal.png") {
		t.Error("a rescan inherited the quarantine — a fixed file would stay disabled")
	}
}

// TestMountIndexConcurrentReadsRaceClean drives the property the whole zip design
// rests on: several pool workers reading different entries of one archive at
// once, while the quarantine is written from another goroutine.
func TestMountIndexConcurrentReadsRaceClean(t *testing.T) {
	entries := map[string]string{}
	for i := 0; i < 32; i++ {
		entries["characters/c"+strconv.Itoa(i)+"/(a)normal.png"] = "body" + strconv.Itoa(i)
	}
	zipPath := writeZip(t, entries)
	dir := t.TempDir()
	writePack(t, dir, entries)

	ix, _ := BuildMountIndex([]string{zipPath, dir})
	defer ix.Retire()

	var wg sync.WaitGroup
	for w := 0; w < runtime.NumCPU()+4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 32; i++ {
				key := "characters/c" + strconv.Itoa(i) + "/(a)normal.png"
				if !ix.acquire() {
					return
				}
				if f, ok := ix.LookupExact(key); ok {
					if data, err := ix.ReadFile(f, key); err == nil && string(data) != "body"+strconv.Itoa(i) {
						t.Errorf("torn read: %q", data)
					}
				}
				_ = ix.IsBad(key)
				ix.release()
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 64; i++ {
			ix.MarkBad("characters/c" + strconv.Itoa(i%32) + "/(a)normal.png")
		}
	}()
	wg.Wait()
}

// TestRetiredIndexKeepsHandlesForInFlightReaders pins the lifetime rule: a Rescan
// that retires an index while a worker holds it must NOT close the archive under
// that worker's read.
func TestRetiredIndexKeepsHandlesForInFlightReaders(t *testing.T) {
	zipPath := writeZip(t, map[string]string{"characters/witch/(a)normal.png": "still-here"})
	ix, _ := BuildMountIndex([]string{zipPath})

	if !ix.acquire() {
		t.Fatal("could not acquire a fresh index")
	}
	ix.Retire() // the render thread publishes a new index and drops the owner ref

	key := "characters/witch/(a)normal.png"
	f, ok := ix.LookupExact(key)
	if !ok {
		t.Fatal("not indexed")
	}
	if data, err := ix.ReadFile(f, key); err != nil || string(data) != "still-here" {
		t.Fatalf("read after Retire failed: %q %v — the handle closed under an in-flight reader", data, err)
	}
	ix.release() // last reference: now it may close

	if ix.acquire() {
		t.Error("a fully released index is still acquirable")
		ix.release()
	}
}

// TestBuildMountIndexSurvivesABadMount pins that one unusable mount (an unplugged
// drive, a deleted folder, a corrupt zip) never costs the others.
func TestBuildMountIndexSurvivesABadMount(t *testing.T) {
	good := t.TempDir()
	writePack(t, good, map[string]string{"characters/witch/(a)normal.png": "ok"})
	missing := filepath.Join(t.TempDir(), "not-there")

	notAZip := filepath.Join(t.TempDir(), "broken.zip")
	if err := os.WriteFile(notAZip, []byte("this is not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	ix, errs := BuildMountIndex([]string{missing, notAZip, good})
	defer ix.Retire()
	if len(errs) != 2 {
		t.Errorf("got %d errors, want 2 (the missing folder and the corrupt zip): %v", len(errs), errs)
	}
	for _, err := range errs {
		var me *mountError
		if !asMountError(err, &me) {
			t.Errorf("error %v does not name its mount", err)
		}
	}
	if _, ok := ix.LookupExact("characters/witch/(a)normal.png"); !ok {
		t.Error("the good mount was lost because a sibling failed")
	}
}

// --- helpers -------------------------------------------------------------------

func writePack(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeZip builds a .zip fixture at test time — no binary fixture in the repo. A
// key ending in "/" is written as a directory entry.
func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, "/") {
			if _, err := w.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRead(t *testing.T, ix *MountIndex, key string) string {
	t.Helper()
	f, ok := ix.LookupExact(key)
	if !ok {
		t.Fatalf("%q not indexed", key)
	}
	data, err := ix.ReadFile(f, key)
	if err != nil {
		t.Fatalf("reading %q: %v", key, err)
	}
	return string(data)
}

func asMountError(err error, target **mountError) bool {
	me, ok := err.(*mountError)
	if ok {
		*target = me
	}
	return ok
}
