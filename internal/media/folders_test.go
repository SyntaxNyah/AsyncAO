package media

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	folderOrigin   = "http://host/base/"
	testFileMode = 0o644
	testDirMode  = 0o755
	// testLater moves a folder's modified time past any file system's timestamp precision.
	testLater = time.Minute
)

// testClock is a clock a test moves by hand.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestFolders(t *testing.T, mounts ...string) (*Folders, *testClock) {
	t.Helper()
	clock := &testClock{t: time.Unix(0, 0)}
	fs := NewFolders(clock.now)
	if errs := fs.Mount(mounts, []string{folderOrigin}); len(errs) > 0 {
		t.Fatalf("Mount: %v", errs)
	}
	t.Cleanup(func() { fs.Mount(nil, nil) })
	return fs, clock
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), testDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), testFileMode); err != nil {
		t.Fatal(err)
	}
}

// touchLater marks a folder as changed, as creating a file in it does on a disk with coarse timestamps too.
func touchLater(t *testing.T, dir string) {
	t.Helper()
	later := time.Now().Add(testLater)
	if err := os.Chtimes(dir, later, later); err != nil {
		t.Fatal(err)
	}
}

func dataOf(t *testing.T, fs *Folders, file string) (string, bool) {
	t.Helper()
	b, err := fs.Data(File(folderOrigin + file))
	if err != nil {
		if !IsAbsent(err) {
			t.Fatalf("Data(%s): %v", file, err)
		}
		return "", false
	}
	return string(b), true
}

func TestFolderFileIsFoundWhateverItsCase(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Characters/Phoenix/(a)Normal.PNG", "sprite")
	fs, _ := newTestFolders(t, root)

	if got, ok := dataOf(t, fs, "characters/phoenix/(a)normal.png"); !ok || got != "sprite" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestHasIsUnknownUntilAWorkerHasCheckedTheFolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "sounds/general/hit.opus", "x")
	fs, _ := newTestFolders(t, root)
	file := File(folderOrigin + "sounds/general/hit.opus")

	if a := fs.Has(file); a != Unknown {
		t.Fatalf("before any check: %d, want Unknown", a)
	}
	dataOf(t, fs, "sounds/general/hit.opus")
	if a := fs.Has(file); a != Yes {
		t.Fatalf("after the check: %d, want Yes", a)
	}
	if a := fs.Has(File(folderOrigin + "sounds/general/miss.opus")); a != No {
		t.Fatalf("a file the checked folder lacks: %d, want No", a)
	}
}

func TestFileDroppedInIsFoundOnceTheCheckIsNoLongerTrusted(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "characters/phoenix/(a)normal.png", "old")
	fs, clock := newTestFolders(t, root)
	if _, ok := dataOf(t, fs, "characters/phoenix/(a)point.png"); ok {
		t.Fatal("found a file that is not there yet")
	}

	writeFile(t, root, "characters/phoenix/(a)point.png", "new")
	touchLater(t, filepath.Join(root, "characters", "phoenix"))
	if _, ok := dataOf(t, fs, "characters/phoenix/(a)point.png"); ok {
		t.Fatal("a trusted check read the disk again")
	}
	clock.advance(folderTrust)
	if got, ok := dataOf(t, fs, "characters/phoenix/(a)point.png"); !ok || got != "new" {
		t.Fatalf("after the trust ran out: %q, %v", got, ok)
	}
}

func TestStaleMakesTheNextMissLookAgain(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "characters/edgeworth/(a)normal.png", "x")
	fs, _ := newTestFolders(t, root)
	dataOf(t, fs, "characters/edgeworth/char.ini")

	writeFile(t, root, "characters/edgeworth/char.ini", "[Options]")
	touchLater(t, filepath.Join(root, "characters", "edgeworth"))
	fs.Stale()
	if got, ok := dataOf(t, fs, "characters/edgeworth/char.ini"); !ok || got != "[Options]" {
		t.Fatalf("after Stale: %q, %v", got, ok)
	}
}

func TestANewCharacterFolderIsFound(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "characters/phoenix/char.ini", "x")
	fs, _ := newTestFolders(t, root)
	if _, ok := dataOf(t, fs, "characters/maya/char.ini"); ok {
		t.Fatal("found a folder that is not there yet")
	}

	writeFile(t, root, "characters/maya/char.ini", "maya")
	fs.Stale()
	if got, ok := dataOf(t, fs, "characters/maya/char.ini"); !ok || got != "maya" {
		t.Fatalf("new folder: %q, %v", got, ok)
	}
}

func TestEarlierMountWins(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeFile(t, first, "background/court/defenseempty.png", "first")
	writeFile(t, second, "background/court/defenseempty.png", "second")
	writeFile(t, second, "background/court/witnessempty.png", "only second")
	fs, _ := newTestFolders(t, first, second)

	if got, _ := dataOf(t, fs, "background/court/defenseempty.png"); got != "first" {
		t.Fatalf("shadowed file came from %q", got)
	}
	if got, _ := dataOf(t, fs, "background/court/witnessempty.png"); got != "only second" {
		t.Fatalf("second mount's own file: %q", got)
	}
}

func TestZipPackIsServedAndCannotEscape(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "pack.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{"Characters/Maya/char.ini": "maya", "../escape.txt": "no"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	fs, _ := newTestFolders(t, zipPath)

	if got, ok := dataOf(t, fs, "characters/maya/char.ini"); !ok || got != "maya" {
		t.Fatalf("zip entry: %q, %v", got, ok)
	}
	if _, ok := dataOf(t, fs, "../escape.txt"); ok {
		t.Fatal("an entry escaping the pack was served")
	}
}

func TestLinkedFoldersAreNotFollowed(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFile(t, outside, "secret.png", "private")
	if err := os.MkdirAll(filepath.Join(root, "characters"), testDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "characters", "linked")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	fs, _ := newTestFolders(t, root)

	if _, ok := dataOf(t, fs, "characters/linked/secret.png"); ok {
		t.Fatal("a file behind a link was served")
	}
}

func TestTheServerFormatListIsNeverAnswered(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ManifestFile, `{"images":[".png"]}`)
	fs, _ := newTestFolders(t, root)

	if _, ok := dataOf(t, fs, ManifestFile); ok {
		t.Fatal("a pack answered for the server's format list")
	}
}

func TestRemountingTheSameFoldersKeepsWhatWasChecked(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "evidence/badge.png", "x")
	fs, _ := newTestFolders(t, root)
	dataOf(t, fs, "evidence/badge.png")

	const otherOrigin = "http://mirror/base/"
	fs.Mount([]string{root}, []string{folderOrigin, otherOrigin})
	if a := fs.Has(File(otherOrigin + "evidence/badge.png")); a != Yes {
		t.Fatalf("new origin on the same folders: %d, want Yes from the kept page", a)
	}
}

func TestAnUnopenableMountIsReported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	fs := NewFolders(time.Now)
	errs := fs.Mount([]string{missing}, []string{folderOrigin})
	if len(errs) != 1 {
		t.Fatalf("errors %v, want one for the missing folder", errs)
	}
	if fs.Has(File(folderOrigin+"x.png")) == Yes {
		t.Fatal("an unopenable mount answered")
	}
}
