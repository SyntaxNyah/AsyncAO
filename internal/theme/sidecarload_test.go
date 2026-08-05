package theme

// theme.Load's sidecar tier (v1.90.0 W3).
//
// The three AO2 design INIs merge per key across the ladder because AO2 merges
// them. The AsyncAO extension tier does NOT, on two independent grounds, and both
// of them are tested here because they fail differently.

import (
	"os"
	"path/filepath"
	"testing"
)

// writeThemeWithSidecar creates <root>/themes/<name>/ with a design INI and,
// optionally, a sidecar declaring one element with the given id.
func writeThemeWithSidecar(t *testing.T, root, name, elementID string) {
	t.Helper()
	dir := filepath.Join(root, ThemesDirName, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	design := "courtroom = 0, 0, 714, 579\nviewport = 0, 0, 256, 192\n"
	if err := os.WriteFile(filepath.Join(dir, DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatal(err)
	}
	if elementID == "" {
		return
	}
	side := "[theme]\nformat = 1\n\n[element." + elementID + "]\nkind = gradient\nrect = 0, 0, 10, 10\nfill = #ffffff\n"
	if err := os.WriteFile(filepath.Join(dir, SidecarFileName), []byte(side), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestElementsDoNotInheritFromDefault is the AO2-parity rule the whole free-element
// model rests on: a theme's decoration is ITS OWN.
//
// The failure it prevents is not hypothetical. loadFirstINI merges the three design
// INIs per key across the ladder, and the ladder ends at the DEFAULT theme — so a
// sidecar read the same way would materialise the default theme's elements onto
// every theme that has none, which today is every theme in existence. The decoration
// would land on rects the author's canvas does not have, and there would be no
// author to report it to.
func TestElementsDoNotInheritFromDefault(t *testing.T) {
	root := t.TempDir()
	writeThemeWithSidecar(t, root, DefaultThemeName, "default_only")
	writeThemeWithSidecar(t, root, "plain", "") // an ordinary AO2 theme: no sidecar

	th, err := Load("plain", "", []string{root})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if sc := th.Sidecar(); sc != nil {
		t.Fatalf("a theme with NO sidecar picked one up (%d elements, from %q) — the extension tier is "+
			"read atomically from the theme's own directories and never inherited from the default theme",
			len(sc.Elements), th.SidecarDir())
	}
	// And the default theme itself still reads its own, or the test above proves
	// only that the loader is broken.
	def, err := Load(DefaultThemeName, "", []string{root})
	if err != nil {
		t.Fatalf("Load(default): %v", err)
	}
	if sc := def.Sidecar(); sc == nil || len(sc.Elements) != 1 {
		t.Fatalf("the default theme did not read its own sidecar: %v", sc)
	}
}

// TestSidecarIsReadAtomicallyFromTheFirstOwnDirectory pins the second half: within
// ONE theme, the sidecar comes from the first of its own directories that has one —
// whole, never merged per id with a lower tier's.
//
// Merging per id is the tempting bug, because that is exactly what the design INIs
// do one directory down. It would mean a subtheme could not REMOVE an element, only
// add to one — and "night mode has fewer decorations" is the first thing anybody
// tries.
func TestSidecarIsReadAtomicallyFromTheFirstOwnDirectory(t *testing.T) {
	root := t.TempDir()
	writeThemeWithSidecar(t, root, "layered", "base_element")
	sub := filepath.Join(root, ThemesDirName, "layered", "night")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	night := "[theme]\nformat = 1\n\n[element.night_element]\nkind = gradient\nrect = 0, 0, 10, 10\nfill = #000000\n"
	if err := os.WriteFile(filepath.Join(sub, SidecarFileName), []byte(night), 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := Load("layered", "night", []string{root})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sc := th.Sidecar()
	if sc == nil {
		t.Fatal("the subtheme's sidecar was not read at all")
	}
	if len(sc.Elements) != 1 || sc.Elements[0].ID != "night_element" {
		t.Fatalf("the subtheme tier produced %d elements (%v) — the sidecar is read WHOLE from the first "+
			"directory that has one, never merged with the tier below it", len(sc.Elements), sc.Elements)
	}
	if got := th.SidecarDir(); got != sub {
		t.Errorf("SidecarDir = %q, want %q", got, sub)
	}
}

// TestAnUnreadableSidecarNeverFailsTheThemeLoad is format rule 1 at the loader:
// reading never refuses. A theme whose extension tier cannot be parsed is exactly a
// stock AO2 theme, and a stock AO2 theme is a thing that renders.
func TestAnUnreadableSidecarNeverFailsTheThemeLoad(t *testing.T) {
	root := t.TempDir()
	writeThemeWithSidecar(t, root, "broken", "")
	// A file that is not INI at all: one enormous unterminated line.
	junk := make([]byte, IniDocByteCap+1)
	for i := range junk {
		junk[i] = 'x'
	}
	path := filepath.Join(root, ThemesDirName, "broken", SidecarFileName)
	if err := os.WriteFile(path, junk, 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := Load("broken", "", []string{root})
	if err != nil {
		t.Fatalf("an unreadable sidecar failed the whole theme load: %v", err)
	}
	if th.Sidecar() != nil {
		t.Fatal("an unreadable sidecar produced a Sidecar anyway")
	}
	if th.SidecarErr() == nil {
		t.Fatal("an unreadable sidecar was swallowed silently — the import report has nothing to show")
	}
	if _, ok := th.ElementRect("courtroom"); !ok {
		t.Fatal("the AO2 design INI stopped resolving because the extension tier was broken")
	}
}
