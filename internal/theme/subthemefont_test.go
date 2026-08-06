package theme

// Subtheme FONTS.
//
// The three font scanners used to decide "is this one of the active theme's own
// directories?" with `filepath.Base(dir) != t.Name`. That test is right for
// <root>/themes/<name> and for the flat tier, and WRONG for the tier AO2 puts
// FIRST: <root>/themes/<name>/<sub>, whose base is the subtheme's name. So every
// subtheme directory silently fell out of every font scan.
//
// Both gates below drive the real Load + the real resolvers, and both fail on
// the old filter. The boundary is now t.ownDirN via ownDirs(), which Load itself
// records — one spelling, and the one that already governs the sidecar.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFontIn drops a placeholder face file into an existing directory. The
// resolvers match on NAME and never open the file, so a byte of content is
// enough — the same shortcut every other font gate here takes.
func writeFontIn(t *testing.T, dir, file string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSubthemeShippingOneFaceIsNotInvisible is issue #6's client-wide install,
// asked of a SUBTHEME: a variant folder that ships a .ttf and declares no family
// must restyle the client exactly as its parent theme would.
//
// Before the fix FontFile returned "" here, and HasAnyFontFile answered "this
// installation has no fonts at all" — which is the advice-selecting predicate, so
// the user was told to go and point AsyncAO at their base.
func TestSubthemeShippingOneFaceIsNotInvisible(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Lantern", aoDefaultDesign, "message = 12\n")
	sub := filepath.Join(root, ThemesDirName, "Lantern", "night")
	writeFontIn(t, sub, "NightFace.ttf")

	th, err := Load("Lantern", "night", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.FontFile(); filepath.Base(got) != "NightFace.ttf" {
		t.Errorf("FontFile = %q, want the subtheme's NightFace.ttf — a subtheme's own face must install like its parent's", got)
	}
	if !th.HasAnyFontFile() {
		t.Error("HasAnyFontFile = false with a face sitting in the active subtheme — the client would tell this user to go and find their base")
	}
}

// TestSubthemeOverridesItsParentsFaceForOneFamily is the TIER ORDER, which is the
// property Load exists to guarantee: the subtheme directory is ahead of the theme
// directory, so a subtheme shipping its own file for a family the parent also
// ships must win.
//
// It could not, before: the subtheme dir was skipped outright, so the parent's
// file was the only candidate and the variant was a variant of everything except
// its typeface.
func TestSubthemeOverridesItsParentsFaceForOneFamily(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Lantern", aoDefaultDesign, "message = 12\nmessage_font = Igiari\n")
	parent := filepath.Join(root, ThemesDirName, "Lantern")
	writeFontIn(t, parent, "Igiari.ttf")
	// The subtheme ships its OWN Igiari, in a nested fonts/ folder so the walk has
	// to descend rather than just list — the layout real packs use.
	writeFontIn(t, filepath.Join(parent, "night", "fonts"), "Igiari.ttf")

	th, err := Load("Lantern", "night", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	got := th.FontFileFor("message", nil)
	if got == "" {
		t.Fatal("the declared family resolved to nothing at all")
	}
	if !strings.HasPrefix(got, filepath.Join(parent, "night")+string(filepath.Separator)) {
		t.Errorf("message resolved to %q, want the SUBTHEME's copy under %q — the subtheme tier is ahead of the theme tier (AO2 path_functions.cpp:190-193)",
			got, filepath.Join(parent, "night"))
	}
}

// TestOwnDirsStopsAtTheDefaultFallback is the other edge of the same boundary:
// widening the scan to the subtheme must NOT also widen it to the default theme,
// or a bundled default's face would be imposed on every theme that ships none.
func TestOwnDirsStopsAtTheDefaultFallback(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Bare", aoDefaultDesign, "message = 12\n")
	writeTheme(t, root, DefaultThemeName, aoDefaultDesign, "message = 12\n")
	writeFontIn(t, filepath.Join(root, ThemesDirName, DefaultThemeName), "DefaultFace.ttf")

	th, err := Load("Bare", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.FontFile(); got != "" {
		t.Errorf("FontFile = %q, want none — only the ACTIVE theme imposes a face on the whole client", got)
	}
	// And the boundary itself: every directory ownDirs hands out is one of this
	// theme's own tiers, and the fallback is past it.
	own := th.ownDirs()
	if len(own) == 0 || len(own) >= len(th.dirs) {
		t.Fatalf("ownDirs returned %d of %d dirs — it must be a non-empty PREFIX with the default fallback excluded", len(own), len(th.dirs))
	}
	for _, d := range own {
		if filepath.Base(d) == DefaultThemeName {
			t.Errorf("ownDirs includes the default-theme fallback %q", d)
		}
	}
}
