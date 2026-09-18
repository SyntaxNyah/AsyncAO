package ui

import (
	"path/filepath"
	"testing"
)

// TestIsEvidenceImageName pins the evidence browse's file filter to the decode
// pipeline's image list (PNG/WebP/GIF/APNG/AVIF, case-insensitive), so the browser
// never offers a file the thumbnailer can't sniff.
func TestIsEvidenceImageName(t *testing.T) {
	yes := []string{"knife.png", "K.WEBP", "x.gif", "y.apng", "z.avif"}
	no := []string{"note.txt", "track.opus", "knife", "x.png.bak", ""}
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

// TestEvidenceRel pins the browsed-path → evidence-field conversion: inside the
// evidence folder it yields the bare name, and escaping it yields ../… — the shape
// Evidence() resolves since #127.
func TestEvidenceRel(t *testing.T) {
	ev := filepath.Join("mount", "evidence")
	cases := []struct {
		path string
		want string
	}{
		{filepath.Join(ev, "knife.png"), "knife.png"},
		{filepath.Join(ev, "cases", "knife.png"), "cases/knife.png"},
		{filepath.Join(ev, "..", "characters", "foo", "char_icon.png"), "../characters/foo/char_icon.png"},
	}
	for _, c := range cases {
		if got := evidenceRel(ev, c.path); got != c.want {
			t.Errorf("evidenceRel(%q, %q) = %q, want %q", ev, c.path, got, c.want)
		}
	}
	// No evidence dir: the basename is the best we can do.
	if got := evidenceRel("", filepath.Join("x", "knife.png")); got != "knife.png" {
		t.Errorf("evidenceRel(empty, ...) = %q, want knife.png", got)
	}
}
