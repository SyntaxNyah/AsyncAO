package theme

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestResolveFamiliesAgainstRealSystemFonts probes what a user can actually TYPE
// into a font box and have work.
//
// The resolver matches a declared family against a font FILE STEM (plus a small
// alias table). Windows names its font files by an abbreviation — comic.ttf,
// times.ttf, segoeui.ttf, trebuc.ttf — not by family, so the question this
// answers is how many ordinary font names a user would reasonably type actually
// resolve. A name that silently resolves to nothing is indistinguishable, on
// screen, from the feature not working.
func TestResolveFamiliesAgainstRealSystemFonts(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("system font folder layout is Windows-specific here")
	}
	win := os.Getenv("WINDIR")
	if win == "" {
		t.Skip("no WINDIR")
	}
	sys := []string{filepath.Join(win, "Fonts")}

	// Names a user would plausibly type, as they appear in any font menu.
	want := []string{
		"Arial", "Comic Sans MS", "Times New Roman", "Verdana", "Tahoma",
		"Segoe UI", "Courier New", "Georgia", "Trebuchet MS", "Impact",
		"Calibri", "Consolas", "Cambria",
	}
	got := ResolveFamilies(want, nil, sys)
	var ok, missing []string
	for _, fam := range want {
		if got[fam] != "" {
			ok = append(ok, fam+" -> "+filepath.Base(got[fam]))
		} else {
			missing = append(missing, fam)
		}
	}
	t.Logf("RESOLVED  (%d/%d): %v", len(ok), len(want), ok)
	t.Logf("UNRESOLVED(%d/%d): %v", len(missing), len(want), missing)
}
