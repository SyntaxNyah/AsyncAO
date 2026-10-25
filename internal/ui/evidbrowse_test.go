package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsEvidenceImageName pins the evidence browse's file filter to the decode
// pipeline's image list (PNG/WebP/GIF/APNG/AVIF, case-insensitive), so the browser
// never offers a file the thumbnailer can't sniff.
//
// This is also the rule that keeps credit.txt out of the listing: the old "Choose"
// grid applied NO extension filter at all and showed it, which is one of the three
// things the playtest killed that grid for.
func TestIsEvidenceImageName(t *testing.T) {
	yes := []string{"knife.png", "K.WEBP", "x.gif", "y.apng", "z.avif"}
	no := []string{"note.txt", "track.opus", "knife", "x.png.bak", "", "credit.txt"}
	for _, n := range yes {
		if !isEvidenceImageName(n) {
			t.Errorf("isEvidenceImageName(%q) = false, want true", n)
		}
	}
	for _, n := range no {
		if isEvidenceImageName(n) {
			t.Errorf("isEvidenceImageName(%q) = true, want false", n)
		}
	}
}

// TestEvidenceRelFromMerged pins the merged browse path → evidence-field
// conversion: a file in the mounts' evidence/ folder yields its bare asset path, a
// subfolder keeps its subfolder, and a file ANYWHERE ELSE in the merged tree climbs
// out with a single "../" — the shape Evidence() resolves since #127, and exactly
// how AO spells cross-folder reuse ("../characters/Ridelle/char_icon.png").
//
// The "../" can never STACK, which is the property the field depends on: the browser
// clamps at the merged root, so nothing it can produce has more than one level to
// climb. That is what makes an escaped value always resolve INSIDE the origin —
// instead of leaking a filesystem layout into the field, which is the bug the
// playtest found ("../../UPDATES/minimal/base/evidence/empty.png").
func TestEvidenceRelFromMerged(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"evidence/knife.png", "knife.png"},
		{"evidence/cases/knife.png", "cases/knife.png"},
		{"evidence/cases/deep/knife.png", "cases/deep/knife.png"},
		{"characters/foo/char_icon.png", "../characters/foo/char_icon.png"},
		{"background/gs4/defenseempty.png", "../background/gs4/defenseempty.png"},
		// A file at the merged ROOT: one "../" from evidence/ and no more.
		{"config.png", "../config.png"},
	}
	for _, c := range cases {
		got := evidenceRelFromMerged(c.path)
		if got != c.want {
			t.Errorf("evidenceRelFromMerged(%q) = %q, want %q", c.path, got, c.want)
		}
		if strings.Contains(strings.TrimPrefix(got, "../"), "..") {
			t.Errorf("evidenceRelFromMerged(%q) = %q — a second \"..\" would escape the origin", c.path, got)
		}
	}
}

// TestMergedParentDirClampsAtRoot pins the sandbox: ".." walks up the merged tree
// and stops at the root. There is no drives sentinel above it (that is the
// filesystem browser's parentBrowseDir, whose "" means "This PC"), which is what
// makes the merged browse unable to leave the mounts.
func TestMergedParentDirClampsAtRoot(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{"", ""},
		{"evidence", ""},
		{"evidence/cases", "evidence"},
		{"characters/foo/bar", "characters/foo"},
	}
	for _, c := range cases {
		if got := mergedParentDir(c.dir); got != c.want {
			t.Errorf("mergedParentDir(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
}

// TestIsZipMount pins the one mount kind the merged listing skips, so a pack folder
// named "thing.ZIP" is recognized as a pack rather than walked as a directory.
func TestIsZipMount(t *testing.T) {
	for _, m := range []string{"pack.zip", `C:\AO2\PACK.ZIP`} {
		if !isZipMount(m) {
			t.Errorf("isZipMount(%q) = false, want true", m)
		}
	}
	for _, m := range []string{`C:\AO2\base`, "folder", ""} {
		if isZipMount(m) {
			t.Errorf("isZipMount(%q) = true, want false", m)
		}
	}
}

// TestListMergedMountDirMergesFirstMountWins pins the core of the merged listing:
// one virtual directory listed across several mounts is the UNION of what they hold,
// de-duplicated case-insensitively with the EARLIER mount's spelling and kind
// winning — "the complete form of a hypothetical fully merged base folder", where
// "one folder has an updated image over the other, it will only show the updated
// image" (Crystalwarrior).
//
// The keep rule is the real evidence-image filter, so the same call also pins that
// non-images (a credit.txt beside the art) never reach the list — the second half of
// what the retired "Choose" grid got wrong.
func TestListMergedMountDirMergesFirstMountWins(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	// Both mounts carry evidence/: the first shadows the second for the two names
	// they share (in DIFFERENT case, so the dedup is proven case-insensitive), and
	// the second contributes rows of its own.
	mustWriteTree(t, first, map[string]string{
		"evidence/knife.png":       "a",
		"evidence/CREDIT.txt":      "a", // non-image: must never be listed
		"evidence/cases/badge.png": "a",
	})
	mustWriteTree(t, second, map[string]string{
		"evidence/KNIFE.png":       "b", // shadowed by the first
		"evidence/extra.png":       "b", // only here
		"evidence/cases/BADGE.png": "b", // shadowed by the first
	})

	got, more, errStr := listMergedMountDir([]string{first, second}, "evidence", isEvidenceImageName)
	if errStr != "" {
		t.Fatalf("unexpected error: %q", errStr)
	}
	if more != 0 {
		t.Fatalf("more = %d, want 0", more)
	}
	// Directories first, then the merged image set by name: cases/, extra.png,
	// knife.png. No credit.txt, one knife (not two), one cases/ (not two).
	want := []browseEntry{
		{name: "cases", isDir: true},
		{name: "extra.png"},
		{name: "knife.png"},
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entries[%d] = %+v, want %+v (full: %+v)", i, got[i], want[i], got)
		}
	}
}

// TestListMergedMountDirSkipsPacksAndMissingFolders pins the two cases that must
// stay silent rather than surfacing as errors: a .zip mount (not enumerable here —
// see listMergedMountDir's doc) and a mount with no such subfolder, which is the
// normal shape of a layered set.
func TestListMergedMountDirSkipsPacksAndMissingFolders(t *testing.T) {
	root := t.TempDir()
	only := filepath.Join(root, "only")
	mustWriteTree(t, only, map[string]string{"evidence/knife.png": "a"})

	// A pack mount named in the list, and one mount that has no evidence/ at all.
	pack := filepath.Join(root, "pack.zip")
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o777); err != nil {
		t.Fatal(err)
	}
	got, _, errStr := listMergedMountDir([]string{pack, empty, only}, "evidence", isEvidenceImageName)
	if errStr != "" {
		t.Fatalf("a missing folder or an unreadable pack must not be an error, got %q", errStr)
	}
	if len(got) != 1 || got[0].name != "knife.png" {
		t.Fatalf("entries = %+v, want just knife.png", got)
	}

	// No mounts at all: an empty listing and no error (the caller reports the
	// no-sources state itself).
	got, more, errStr := listMergedMountDir(nil, "evidence", isEvidenceImageName)
	if len(got) != 0 || more != 0 || errStr != "" {
		t.Fatalf("no mounts = %+v/%d/%q, want empty/0/\"\"", got, more, errStr)
	}
}

// mustWriteTree creates name→content files under root, making parent directories.
// Local to this file (the merged-browse tests are its only caller).
func mustWriteTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}
