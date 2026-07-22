package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFontFileResolvesBundledTTF: a theme that ships its own font, declared via
// message_font, resolves to the matching .ttf in the theme dir (family match wins
// over an unrelated font file).
func TestFontFileResolvesBundledTTF(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Aceternia", aoDefaultDesign, "message = 12\nmessage_font = Igiari\n")
	dir := filepath.Join(root, ThemesDirName, "Aceternia")
	for _, f := range []string{"Other.otf", "Igiari.ttf"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th, err := Load("Aceternia", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.Font("message").Font; got != "Igiari" {
		t.Errorf("message_font = %q, want Igiari", got)
	}
	if got := th.FontFile(); filepath.Base(got) != "Igiari.ttf" {
		t.Errorf("FontFile = %q, want the family-matching Igiari.ttf", got)
	}
}

// TestFontFileResolvesFromBaseFonts pins issue #39: an imported theme that
// declares message_font but ships no font of its own resolves the family from the
// content root's base "fonts/" folder — where AO themes expect their fonts to
// live — matching by a normalized name ("Ace Attorney" ↔ "ace_attorney.ttf").
func TestFontFileResolvesFromBaseFonts(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "DRRA", aoDefaultDesign, "message = 24\nmessage_font = Ace Attorney\n")
	fontsDir := filepath.Join(root, "fonts")
	if err := os.MkdirAll(fontsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"unrelated.ttf", "ace_attorney.ttf"} {
		if err := os.WriteFile(filepath.Join(fontsDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th, err := Load("DRRA", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.FontFile(); filepath.Base(got) != "ace_attorney.ttf" {
		t.Errorf("FontFile = %q, want base/fonts/ace_attorney.ttf", got)
	}
}

// TestFontFileBaseFontsNeedsDeclaredFamily: base/fonts holds many faces, so a
// theme that declares NO family must not grab an arbitrary one from it.
func TestFontFileBaseFontsNeedsDeclaredFamily(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Plain", aoDefaultDesign, "message = 12\n") // no message_font
	fontsDir := filepath.Join(root, "fonts")
	if err := os.MkdirAll(fontsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fontsDir, "some_font.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Load("Plain", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.FontFile(); got != "" {
		t.Errorf("FontFile = %q, want empty (no declared family → never pick from base/fonts)", got)
	}
}

// TestFontFileNoneWhenThemeShipsNoFont: a theme with no bundled font yields "".
func TestFontFileNoneWhenThemeShipsNoFont(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, DefaultThemeName, aoDefaultDesign, aoDefaultFonts)
	th, err := Load(DefaultThemeName, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.FontFile(); got != "" {
		t.Errorf("FontFile = %q, want empty (no bundled font)", got)
	}
}
