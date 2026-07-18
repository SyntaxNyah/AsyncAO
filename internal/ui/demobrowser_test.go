package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeDirEntry is a minimal browseDirEntry for the pure filter/sort/cap tests
// (no filesystem needed). The integration tests below use REAL os.DirEntry from
// t.TempDir() to prove the generic accepts them too.
type fakeDirEntry struct {
	name string
	dir  bool
}

func (f fakeDirEntry) Name() string { return f.name }
func (f fakeDirEntry) IsDir() bool  { return f.dir }

// TestFilterBrowseEntries pins the three filter rules and the sort: directories
// first, then case-insensitive name; recordings (.demo/.aorec, any case) kept,
// other files dropped; hidden dotfiles (and "."/"..") skipped entirely.
func TestFilterBrowseEntries(t *testing.T) {
	in := []fakeDirEntry{
		{name: "Zebra", dir: true},
		{name: "apple", dir: true},
		{name: ".hidden", dir: true},       // dotfile dir: skipped
		{name: "scene.DEMO", dir: false},   // recording (uppercase ext): kept
		{name: "clip.aorec", dir: false},   // recording: kept
		{name: "notes.txt", dir: false},    // non-recording: dropped
		{name: ".secret.demo", dir: false}, // dotfile (even a recording): skipped
		{name: "Beta", dir: true},
	}
	got := filterBrowseEntries(in)

	// Expected: dirs first (apple, Beta, Zebra — case-insensitive), then files
	// (clip.aorec, scene.DEMO — case-insensitive). Dotfiles and notes.txt gone.
	want := []browseEntry{
		{name: "apple", isDir: true},
		{name: "Beta", isDir: true},
		{name: "Zebra", isDir: true},
		{name: "clip.aorec", isDir: false},
		{name: "scene.DEMO", isDir: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestFilterBrowseEntriesCap proves the maxBrowseEntries cap truncates the list
// and overflowCount reports exactly the dropped remainder ("… and N more" math).
func TestFilterBrowseEntriesCap(t *testing.T) {
	const extra = 37
	total := maxBrowseEntries + extra
	in := make([]fakeDirEntry, 0, total)
	for i := 0; i < total; i++ {
		// All directories so every one is kept (isolate the cap from the filter).
		in = append(in, fakeDirEntry{name: dirName(i), dir: true})
	}
	got := filterBrowseEntries(in)
	if len(got) != maxBrowseEntries {
		t.Fatalf("filter kept %d, want the cap %d", len(got), maxBrowseEntries)
	}
	if more := overflowCount(in); more != extra {
		t.Fatalf("overflowCount = %d, want %d", more, extra)
	}

	// Under the cap: no overflow.
	few := in[:maxBrowseEntries-1]
	if more := overflowCount(few); more != 0 {
		t.Fatalf("overflowCount under cap = %d, want 0", more)
	}
}

// dirName makes distinct, sortable directory names for the cap test.
func dirName(i int) string {
	return "d" + string(rune('a'+i%26)) + string(rune('0'+(i/26)%10)) + string(rune('0'+i%10)) + "x"
}

// TestFilterBrowseEntriesCapMixed pins the cap/overflow math when the input mixes
// KEPT entries (dirs + recordings) with DROPPED ones (.txt, dotfiles): the cap
// applies to kept entries only, and overflowCount reports only the kept remainder
// — the two functions must agree on the keep rule (review find: the all-dirs cap
// test never exercised the filter branches inside the overflow walk).
func TestFilterBrowseEntriesCapMixed(t *testing.T) {
	const keptExtra = 5   // kept entries beyond the cap
	const droppedMix = 40 // .txt + dotfiles sprinkled through — never counted
	totalKept := maxBrowseEntries + keptExtra
	in := make([]fakeDirEntry, 0, totalKept+droppedMix)
	for i := 0; i < totalKept; i++ {
		if i%3 == 0 {
			in = append(in, fakeDirEntry{name: dirName(i) + ".demo", dir: false})
		} else {
			in = append(in, fakeDirEntry{name: dirName(i), dir: true})
		}
	}
	for i := 0; i < droppedMix; i++ {
		if i%2 == 0 {
			in = append(in, fakeDirEntry{name: dirName(i) + ".txt", dir: false})
		} else {
			in = append(in, fakeDirEntry{name: "." + dirName(i), dir: true})
		}
	}
	if got := filterBrowseEntries(in); len(got) != maxBrowseEntries {
		t.Fatalf("filter kept %d, want the cap %d", len(got), maxBrowseEntries)
	}
	if more := overflowCount(in); more != keptExtra {
		t.Fatalf("overflowCount = %d, want %d (dropped entries must not count)", more, keptExtra)
	}
}

// TestParentBrowseDir pins the parent walk, including the drive-root → drives
// sentinel on Windows and the POSIX "/" floor elsewhere.
func TestParentBrowseDir(t *testing.T) {
	// Drives view has no parent (stays "").
	if got := parentBrowseDir(""); got != "" {
		t.Errorf("parent of drives view: got %q, want \"\"", got)
	}

	if runtime.GOOS == "windows" {
		// A drive root walks to the drives sentinel.
		if got := parentBrowseDir(`C:\`); got != "" {
			t.Errorf("parent of C:\\ : got %q, want the drives sentinel \"\"", got)
		}
		// A nested dir walks up one level.
		if got := parentBrowseDir(`C:\Users\bob`); got != `C:\Users` {
			t.Errorf("parent of C:\\Users\\bob: got %q, want C:\\Users", got)
		}
	} else {
		// POSIX root stays at "/".
		if got := parentBrowseDir("/"); got != "/" {
			t.Errorf("parent of /: got %q, want /", got)
		}
		if got := parentBrowseDir("/home/bob"); got != "/home" {
			t.Errorf("parent of /home/bob: got %q, want /home", got)
		}
	}
}

// TestChildBrowseDir pins the descend join, including the drives-view special
// case where the row name IS the volume root.
func TestChildBrowseDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		if got := childBrowseDir("", `C:\`); got != `C:\` {
			t.Errorf("drives-view child: got %q, want C:\\", got)
		}
		if got := childBrowseDir(`C:\Users`, "bob"); got != `C:\Users\bob` {
			t.Errorf("child: got %q, want C:\\Users\\bob", got)
		}
	} else {
		if got := childBrowseDir("/home", "bob"); got != "/home/bob" {
			t.Errorf("child: got %q, want /home/bob", got)
		}
	}
}

// TestLoadBrowseDirFixture drives loadBrowseDir over a real t.TempDir() to prove
// the generic filter accepts os.DirEntry and the end-to-end listing matches the
// filter rules (dirs first, recordings only, dotfiles skipped).
func TestLoadBrowseDirFixture(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	must(os.Mkdir(filepath.Join(dir, ".hiddendir"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "a.demo"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "b.aorec"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "c.txt"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".d.demo"), []byte("x"), 0o644))

	entries, more, err := loadBrowseDir(dir)
	if err != nil {
		t.Fatalf("loadBrowseDir: %v", err)
	}
	if more != 0 {
		t.Fatalf("overflow = %d, want 0", more)
	}
	want := []browseEntry{
		{name: "sub", isDir: true},
		{name: "a.demo", isDir: false},
		{name: "b.aorec", isDir: false},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %+v, want %+v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, entries[i], want[i])
		}
	}
}

// TestLoadBrowseDirError proves an unreadable/nonexistent dir returns an error
// (surfaced as loadErr, leaving ".." and quick-jumps navigable) rather than
// panicking or returning junk.
func TestLoadBrowseDirError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, _, err := loadBrowseDir(missing); err == nil {
		t.Fatal("loadBrowseDir on a missing dir: want an error, got nil")
	}
}

// TestIsRecordingName pins the extension gate the browser shares with the drop
// path (case-insensitive .demo/.aorec, nothing else).
func TestIsRecordingName(t *testing.T) {
	yes := []string{"x.demo", "x.DEMO", "y.aorec", "Y.AoRec", "a.b.demo"}
	no := []string{"x.txt", "x.mp4", "demo", "x.demofoo", "x."}
	for _, n := range yes {
		if !isRecordingName(n) {
			t.Errorf("isRecordingName(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if isRecordingName(n) {
			t.Errorf("isRecordingName(%q) = true, want false", n)
		}
	}
}
