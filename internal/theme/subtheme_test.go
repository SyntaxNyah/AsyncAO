package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubthemeTierProbeOrderMatchesAO2 pins the subtheme tier against
// AO2-Client's get_asset_paths (path_functions.cpp:175-216), which is the canon
// this whole interop exists for. Of the eight tiers there, three are ours:
//
//	:190-193  themes/<theme>/<subtheme>/<element>   subtheme path
//	:198-201  themes/<theme>/<element>              theme path
//	:202-205  themes/<default>/<element>            default theme path — p_default_theme
//	                                                is passed with NO subtheme
//
// The asymmetric last line is the part that needs a gate: it looks like an
// oversight, "fixing" it into symmetry would resolve files AO2 cannot, and one
// user's "night" would start reading out of the bundled default's "night".
func TestSubthemeTierProbeOrderMatchesAO2(t *testing.T) {
	const rootA, rootB, name, sub = "C:/Custom", "C:/App", "CoolTheme", "night"

	th, err := Load(name, sub, []string{rootA, rootB})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	dirs := th.Dirs()

	// Per ROOT the subtheme precedes its theme, and root order (priority) is
	// preserved: a custom folder's theme still outranks the app directory's.
	want := []string{
		filepath.Join(rootA, ThemesDirName, name, sub),
		filepath.Join(rootA, ThemesDirName, name),
		filepath.Join(rootB, ThemesDirName, name, sub),
		filepath.Join(rootB, ThemesDirName, name),
	}
	for i, w := range want {
		if i >= len(dirs) || dirs[i] != w {
			t.Fatalf("probe order = %v,\nwant it to start with %v", dirs, want)
		}
	}
	// The default tier exists and is subtheme-free.
	sawDefault := false
	for _, d := range dirs {
		if strings.EqualFold(filepath.Base(d), sub) && strings.Contains(d, DefaultThemeName) {
			t.Errorf("default tier %q carries the subtheme; AO2 passes p_default_theme "+
				"with no subtheme component (path_functions.cpp:202-205)", d)
		}
		if d == filepath.Join(rootA, ThemesDirName, DefaultThemeName) {
			sawDefault = true
		}
	}
	if !sawDefault {
		t.Errorf("default-theme tier missing from %v", dirs)
	}
}

// TestSubthemeIsInertWhenAbsentAndNeverReordersTheOldList is the other half of
// the promise: with no subtheme the probe list must be EXACTLY what it was
// before subthemes existed, because every theme in the wild is loaded that way.
func TestSubthemeIsInertWhenAbsentAndNeverReordersTheOldList(t *testing.T) {
	const root, name = "C:/App", "CoolTheme"

	plain, err := Load(name, "", []string{root})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{
		filepath.Join(root, ThemesDirName, name),
		filepath.Join(root, name), // the #21 flat tier
		filepath.Join(root, ThemesDirName, DefaultThemeName),
	}
	got := plain.Dirs()
	if len(got) != len(want) {
		t.Fatalf("no-subtheme dirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("no-subtheme dirs = %v, want %v (differs at %d)", got, want, i)
		}
	}

	// A subtheme that cannot be a folder name is the same as none at all — the
	// list must not grow a garbage directory for it.
	for _, bad := range []string{"..", "../secret", `night\deep`, "night/deep", "C:/x", ".hidden", "   "} {
		hostile, err := Load(name, bad, []string{root})
		if err != nil {
			t.Fatalf("Load(%q): %v", bad, err)
		}
		if hostile.Sub != "" {
			t.Errorf("subtheme %q survived sanitizing as %q", bad, hostile.Sub)
		}
		if len(hostile.Dirs()) != len(want) {
			t.Errorf("subtheme %q added directories: %v", bad, hostile.Dirs())
		}
	}
}

// TestSubthemeResolvesFilesAndKeysAheadOfTheTheme is the behaviour a user sees:
// the subtheme's INI keys win, its art wins, and everything it does NOT override
// still comes from the theme (and then from default).
func TestSubthemeResolvesFilesAndKeysAheadOfTheTheme(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Nocturne", "chatbox = 1, 2, 3, 4\nshowname = 5, 6, 7, 8\n", "message = 9\n")

	subDir := filepath.Join(root, ThemesDirName, "Nocturne", "night")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The subtheme overrides ONE rect and adds one file; it says nothing about
	// showname, which must keep coming from the parent theme.
	if err := os.WriteFile(filepath.Join(subDir, DesignFileName), []byte("chatbox = 10, 20, 30, 40\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "chatbox.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ThemesDirName, "Nocturne", "chatbox.png"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := Load("Nocturne", "night", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := th.ElementRect("chatbox"); !ok || r != (Rect{X: 10, Y: 20, W: 30, H: 40}) {
		t.Errorf("chatbox = %v (ok=%v), want the subtheme's 10,20,30,40", r, ok)
	}
	if r, ok := th.ElementRect("showname"); !ok || r != (Rect{X: 5, Y: 6, W: 7, H: 8}) {
		t.Errorf("showname = %v (ok=%v), want the parent theme's 5,6,7,8", r, ok)
	}
	if spec := th.Font("message"); !spec.SizeSet || spec.Size != 9 {
		t.Errorf("message font = %+v, want the parent theme's size 9", spec)
	}
	path, ok := th.FindAsset("chatbox", []string{".png"})
	if !ok || filepath.Dir(path) != subDir {
		t.Errorf("chatbox.png resolved to %q, want the subtheme's copy in %q", path, subDir)
	}
}

// TestSanitizeSubthemeRefusesEverythingThatIsNotOneFolderName states the rule
// once, exhaustively, because this value is joined onto a filesystem path and
// arrives from a hand-editable preferences file.
func TestSanitizeSubthemeRefusesEverythingThatIsNotOneFolderName(t *testing.T) {
	ok := []string{"night", "alt", "christmas", "aa_investigations", "Night-2", strings.Repeat("n", SubthemeNameLen)}
	for _, s := range ok {
		if got := SanitizeSubtheme(s); got != s {
			t.Errorf("SanitizeSubtheme(%q) = %q, want it kept", s, got)
		}
	}
	if got := SanitizeSubtheme("  night  "); got != "night" {
		t.Errorf("SanitizeSubtheme trimmed = %q, want %q", got, "night")
	}
	bad := []string{
		"", "   ", "..", "../..", "night/..", "night/deep", `night\deep`, "/night", `\night`,
		"C:/night", "c:night", ".", ".night", strings.Repeat("n", SubthemeNameLen+1),
	}
	for _, s := range bad {
		if got := SanitizeSubtheme(s); got != "" {
			t.Errorf("SanitizeSubtheme(%q) = %q, want it refused", s, got)
		}
	}
}
