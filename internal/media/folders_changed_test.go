package media

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestFoldersChangedFindsAnEditedFile: a file edited in place, which leaves its folder's time alone, is reported once,
// and an untouched file never is.
func TestFoldersChangedFindsAnEditedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "characters/witch/(a)normal.png", "old")
	writeFile(t, dir, "characters/witch/(a)happy.png", "same")
	fs, _ := newTestFolders(t, dir)
	edited := File(folderOrigin + "characters/witch/(a)normal.png")
	untouched := File(folderOrigin + "characters/witch/(a)happy.png")
	for _, f := range []File{edited, untouched} {
		if _, err := fs.Data(f); err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
	}
	if got := fs.Changed(); len(got) != 0 {
		t.Fatalf("nothing was edited, Changed() = %v", got)
	}

	p := filepath.Join(dir, "characters", "witch", "(a)normal.png")
	if err := os.WriteFile(p, []byte("new and longer"), testFileMode); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(testLater)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if got := fs.Changed(); !slices.Equal(got, []File{edited}) {
		t.Fatalf("Changed() = %v, want only %s", got, edited)
	}
	if got := fs.Changed(); len(got) != 0 {
		t.Errorf("an edit seen once was reported again: %v", got)
	}

	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := fs.Changed(); !slices.Equal(got, []File{edited}) {
		t.Errorf("a deleted file was not reported: %v", got)
	}
}

// TestFoldersChangedForgetsTheOldMounts: changing the mounts drops the files read through the old ones.
func TestFoldersChangedForgetsTheOldMounts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "background/court/defenseempty.png", "x")
	fs, _ := newTestFolders(t, dir)
	if _, err := fs.Data(File(folderOrigin + "background/court/defenseempty.png")); err != nil {
		t.Fatal(err)
	}
	fs.Mount([]string{t.TempDir()}, []string{folderOrigin})
	if err := os.Remove(filepath.Join(dir, "background", "court", "defenseempty.png")); err != nil {
		t.Fatal(err)
	}
	if got := fs.Changed(); len(got) != 0 {
		t.Errorf("a file of an unmounted folder was reported: %v", got)
	}
}
