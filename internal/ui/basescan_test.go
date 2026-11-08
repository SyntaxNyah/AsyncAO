package ui

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	baseTreeDirMode  = 0o755
	baseTreeFileMode = 0o644
	// folderCheckWait bounds how long a test waits for the scan goroutine.
	folderCheckWait = 5 * time.Second
)

// writeBaseTree lays out a folder from a rel→content map, creating parents; entries ending in "/" are empty folders.
func writeBaseTree(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, baseTreeDirMode); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), baseTreeDirMode); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), baseTreeFileMode); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestBaseScanCountsWhatIsActuallyThere: the counts and pills come off the disk, not from the fact a folder was added.
func TestBaseScanCountsWhatIsActuallyThere(t *testing.T) {
	const wantChars, wantBgs = 3, 2
	dir := writeBaseTree(t, t.TempDir(), map[string]string{
		"characters/witch/char.ini":         "[Emotions]\nnumber = 1\n",
		"characters/beato/(a)idle.png":      "x",
		"characters/nobody/":                "",
		"background/court/defenseempty.png": "x",
		"background/lobby/":                 "",
		"sounds/music/theme.opus":           "x",
		"evidence/knife.png":                "x",
	})

	s := scanBaseFolder(dir)
	if s.err != "" {
		t.Fatalf("scan failed: %s", s.err)
	}
	if s.chars != wantChars || s.bgs != wantBgs {
		t.Errorf("chars, bgs = %d, %d; want %d, %d", s.chars, s.bgs, wantChars, wantBgs)
	}
	if want := []string{"Characters", "Backgrounds", "Sounds", "Evidence"}; !slices.Equal(s.badges, want) {
		t.Errorf("badges = %v, want %v", s.badges, want)
	}
	if s.suggest != "" {
		t.Errorf("a correct folder was second-guessed with %q", s.suggest)
	}
}

// TestBaseScanAcceptsAPackOfOneKind: a pack of only backgrounds and music is a base, not a mistake.
func TestBaseScanAcceptsAPackOfOneKind(t *testing.T) {
	dir := writeBaseTree(t, t.TempDir(), map[string]string{
		"background/court/defenseempty.png": "x",
		"sounds/music/theme.opus":           "x",
	})
	s := scanBaseFolder(dir)
	if !s.looksLikeBase() {
		t.Fatal("a backgrounds-and-music pack was reported as not an AO base")
	}
	if want := []string{"Backgrounds", "Sounds"}; !slices.Equal(s.badges, want) {
		t.Errorf("badges = %v, want %v", s.badges, want)
	}
	music := writeBaseTree(t, t.TempDir(), map[string]string{"sounds/music/theme.opus": "x"})
	if !scanBaseFolder(music).looksLikeBase() {
		t.Error("a music-only pack was reported as not an AO base")
	}
}

// TestBaseScanCorrectsTheTwoNearMisses: a folder one level too high or too deep is caught, with the right one offered.
func TestBaseScanCorrectsTheTwoNearMisses(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	writeBaseTree(t, root, map[string]string{
		"base/characters/witch/char.ini": "[Options]\n",
		"base/background/court/":         "",
	})

	up := scanBaseFolder(root)
	if up.looksLikeBase() {
		t.Fatal("the install root was accepted as a base")
	}
	if up.suggest != base {
		t.Errorf("one level too high: suggest = %q, want %q", up.suggest, base)
	}
	deep := scanBaseFolder(filepath.Join(base, baseDirCharacters))
	if deep.looksLikeBase() {
		t.Fatal("the characters/ folder was accepted as a base")
	}
	if deep.suggest != base {
		t.Errorf("one level too deep: suggest = %q, want the parent %q", deep.suggest, base)
	}
}

// TestBaseScanReadsAZipPack: a .zip is mountable, so it is described from its central directory.
func TestBaseScanReadsAZipPack(t *testing.T) {
	const wantChars, wantBgs = 3, 1
	path := filepath.Join(t.TempDir(), "mypack.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{
		"characters/witch/char.ini",
		"characters/witch/(a)idle.webp",
		"characters/beato/(a)idle.webp",
		`characters\lambda\char.ini`, // a pack zipped by a Windows tool
		"background/court/defenseempty.png",
		"sounds/music/theme.opus",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	s := scanBaseFolder(path)
	if s.err != "" {
		t.Fatalf("scan failed: %s", s.err)
	}
	if !s.isZip {
		t.Error("a .zip was walked as a folder")
	}
	if s.chars != wantChars || s.bgs != wantBgs {
		t.Errorf("chars, bgs = %d, %d; want %d, %d (one per character, not per sprite)", s.chars, s.bgs, wantChars, wantBgs)
	}
	if want := []string{"Characters", "Backgrounds", "Sounds"}; !slices.Equal(s.badges, want) {
		t.Errorf("badges = %v, want %v", s.badges, want)
	}
}

// TestBaseScanReportsAnUnreadablePick: a path that isn't there reports an error, not a row of confident zeroes.
func TestBaseScanReportsAnUnreadablePick(t *testing.T) {
	if s := scanBaseFolder(filepath.Join(t.TempDir(), "nope")); s.err == "" {
		t.Error("a missing folder scanned clean")
	}
	file := filepath.Join(t.TempDir(), "notafolder.txt")
	if err := os.WriteFile(file, []byte("x"), baseTreeFileMode); err != nil {
		t.Fatal(err)
	}
	if s := scanBaseFolder(file); s.err == "" {
		t.Error("a plain file was accepted as a folder")
	}
	if s := scanBaseFolder("   "); s.err == "" {
		t.Error("an empty path scanned clean")
	}
}

// TestBaseScanFolderNamesMatchTheURLBuilder keeps the scanned folder names tied to the layout the client requests.
func TestBaseScanFolderNamesMatchTheURLBuilder(t *testing.T) {
	u := courtroom.NewURLBuilder("http://example.test/base/")
	for _, tc := range []struct{ name, url string }{
		{baseDirCharacters, u.CharFolder("witch")},
		{baseDirBackground, u.BackgroundsRoot()},
		{baseDirSounds, u.SFX("realization")},
		{baseDirEvidence, u.Evidence("knife.png")},
	} {
		if !strings.Contains(tc.url, "/"+tc.name+"/") {
			t.Errorf("the scan looks for %q but the URL builder asks for %q", tc.name, tc.url)
		}
	}
}

// waitFolderChecks runs checkFolders until every listed folder has a report.
func waitFolderChecks(t *testing.T, mounts, auto []string) {
	t.Helper()
	deadline := time.Now().Add(folderCheckWait)
	for time.Now().Before(deadline) {
		checkFolders(mounts, auto)
		if _, pending := firstUnscanned(mounts, auto); !pending && !settings.folderScanRunning {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the folder checks never finished")
}

// TestFolderChecksCoverEveryListedFolderAndForgetRemovedOnes: listed and auto-mounted folders all get a report, and
// a folder taken off the list loses its report.
func TestFolderChecksCoverEveryListedFolderAndForgetRemovedOnes(t *testing.T) {
	t.Cleanup(forgetFolderScans)
	t.Cleanup(func() { clear(settings.folderOpen) })
	forgetFolderScans()
	good := writeBaseTree(t, t.TempDir(), map[string]string{"background/court/": ""})
	empty := t.TempDir()
	auto := writeBaseTree(t, t.TempDir(), map[string]string{"sounds/music/": ""})

	waitFolderChecks(t, []string{good, empty}, []string{auto})
	if !settings.folderScans[good].looksLikeBase() || !settings.folderScans[auto].looksLikeBase() {
		t.Error("a base folder was not recognised")
	}
	if settings.folderScans[empty].looksLikeBase() {
		t.Error("an empty folder was recognised as a base")
	}

	settings.folderOpen[empty] = true
	waitFolderChecks(t, []string{good}, nil)
	if _, kept := settings.folderScans[empty]; kept {
		t.Error("a removed folder kept its report")
	}
	if _, kept := settings.folderOpen[empty]; kept {
		t.Error("a removed folder kept its open state")
	}
}

// TestReplaceMountKeepsItsPlace: taking the offered correction swaps the folder without moving it in the search order.
func TestReplaceMountKeepsItsPlace(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	first, wrong, last, right := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	a.setAssetSourceMode(assetSrcLayered, []string{first, wrong, last})

	const wrongAt = 1
	a.replaceMount(wrongAt, right)
	if _, got := a.d.Prefs.LocalAssets(); !slices.Equal(got, []string{first, right, last}) {
		t.Errorf("mounts = %v, want the corrected folder in the same place", got)
	}
}

// TestDropOnTheAssetsTabAddsAFolder: a dropped folder or .zip is added as is, and a dropped file adds its folder.
func TestDropOnTheAssetsTabAddsAFolder(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	folder := t.TempDir()
	pack := filepath.Join(t.TempDir(), "pack.zip")
	fileDir := writeBaseTree(t, t.TempDir(), map[string]string{"characters/witch/char.ini": "x"})
	file := filepath.Join(fileDir, "characters", "witch", "char.ini")

	for _, drop := range []string{folder, pack, file} {
		resolveDroppedMount(drop)
		select {
		case path := <-settings.mountDropRes:
			settings.mountDropRes <- path
		case <-time.After(folderCheckWait):
			t.Fatalf("the drop of %s never resolved", drop)
		}
		a.pollMountDrop()
	}
	want := []string{folder, pack, filepath.Dir(file)}
	if _, got := a.d.Prefs.LocalAssets(); !slices.Equal(got, want) {
		t.Errorf("mounts = %v, want %v", got, want)
	}
}
