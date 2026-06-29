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
