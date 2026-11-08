package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

func writeUIPack(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	const dirMode, fileMode = 0o755, 0o644
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), fileMode); err != nil {
		t.Fatal(err)
	}
}

// layeredFolderApp is a headless App whose one folder is layered over origin.
func layeredFolderApp(t *testing.T, origin string) (*App, string) {
	t.Helper()
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	a.urls = courtroom.NewURLBuilder(origin)
	dir := t.TempDir()
	a.setSources(true, true, []string{dir})
	return a, dir
}

func TestFolderOriginIsTheNormalizedServerOrigin(t *testing.T) {
	a, dir := layeredFolderApp(t, "http://h/base") // an ASS field without its trailing slash
	writeUIPack(t, dir, "characters/witch/(a)normal.png")

	if !a.d.Manager.PackCovers("http://h/base/characters/witch/(a)normal") {
		t.Error("the folder does not answer for the server's asset")
	}
}

func TestNoFoldersUnmountsEverything(t *testing.T) {
	a, _ := layeredFolderApp(t, "http://h/base/")

	a.setSources(true, true, nil)
	if mounts, _, _ := a.d.Manager.FolderStatus(); len(mounts) != 0 {
		t.Errorf("mounts %v after the last folder was removed", mounts)
	}
}

func TestFocusGainFindsAFileDroppedIn(t *testing.T) {
	const origin = "http://h/base/"
	a, dir := layeredFolderApp(t, origin)
	normal := origin + "characters/witch/(a)normal"
	happy := origin + "characters/witch/(a)happy"

	writeUIPack(t, dir, "characters/witch/(a)other.png")
	if a.d.Manager.PackCovers(normal) {
		t.Fatal("found a file that isn't there")
	}
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	if a.d.Manager.PackCovers(normal) {
		t.Fatal("the folder was read again before its check expired, so this test proves nothing")
	}

	a.NoteFocusGained()
	if !a.d.Manager.PackCovers(normal) {
		t.Error("focus gain did not look at the folder again")
	}

	// A second focus gain right away is rate limited.
	writeUIPack(t, dir, "characters/witch/(a)happy.png")
	a.NoteFocusGained()
	if a.d.Manager.PackCovers(happy) {
		t.Error("a focus gain inside the minimum interval re-checked the folders")
	}
}
