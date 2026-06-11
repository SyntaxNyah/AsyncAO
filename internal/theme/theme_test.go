package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTheme fabricates an AO2-style theme folder.
func writeTheme(t *testing.T, root, name string, design, fonts string) {
	t.Helper()
	dir := filepath.Join(root, ThemesDirName, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if design != "" {
		if err := os.WriteFile(filepath.Join(dir, DesignFileName), []byte(design), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if fonts != "" {
		if err := os.WriteFile(filepath.Join(dir, FontsFileName), []byte(fonts), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// aoDefaultDesign mimics real AO2-Client default theme keys.
const aoDefaultDesign = `; AO2 theme
viewport = 0, 0, 256, 192
chatbox = 2, 143, 252, 49
chat_arrow = 236, 178, 16, 16
showname = 4, 1, 250, 13
[Dimensions]
width = 714
height = 668
`

const aoDefaultFonts = `message = 9
message_color = 255, 255, 255
showname = 8
showname_bold = 1
showname_color = 0, 255, 165
`

func TestThemeLoadsAO2DesignAndFonts(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, DefaultThemeName, aoDefaultDesign, aoDefaultFonts)

	th, err := Load(DefaultThemeName, []string{root})
	if err != nil {
		t.Fatal(err)
	}

	r, ok := th.ElementRect("chatbox")
	if !ok || r != (Rect{X: 2, Y: 143, W: 252, H: 49}) {
		t.Errorf("chatbox rect = %+v ok=%v", r, ok)
	}
	if !r.Valid() {
		t.Error("chatbox rect must be valid")
	}
	if _, ok := th.ElementRect("nonexistent"); ok {
		t.Error("missing element reported present")
	}

	msg := th.Font("message")
	if msg.Size != 9 || msg.Color != (RGB{255, 255, 255}) || msg.Bold {
		t.Errorf("message font = %+v", msg)
	}
	sn := th.Font("showname")
	if sn.Size != 8 || !sn.Bold || sn.Color != (RGB{0, 255, 165}) {
		t.Errorf("showname font = %+v", sn)
	}

	if v, ok := th.design.GetSection("Dimensions", "width"); !ok || v != "714" {
		t.Errorf("[Dimensions]/width = %q ok=%v", v, ok)
	}
}

// TestThemeOverridesFallBackToDefault pins AO2's lookup ladder: the active
// theme wins where it defines keys; everything else falls back to default.
func TestThemeOverridesFallBackToDefault(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, DefaultThemeName, aoDefaultDesign, aoDefaultFonts)
	writeTheme(t, root, "midnight", "chatbox = 10, 100, 300, 60\n", "")

	th, err := Load("midnight", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := th.ElementRect("chatbox"); r.W != 300 {
		t.Errorf("active theme override lost: %+v", r)
	}
	if r, ok := th.ElementRect("viewport"); !ok || r.W != 256 {
		t.Errorf("default-theme fallback broken: %+v ok=%v", r, ok)
	}
	if f := th.Font("showname"); !f.Bold {
		t.Error("fonts must fall back to default theme")
	}
}

func TestThemeFindAssetProbesExtensionsAndDirs(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, DefaultThemeName, aoDefaultDesign, "")
	writeTheme(t, root, "midnight", "x = 1,1,1,1\n", "")

	defDir := filepath.Join(root, ThemesDirName, DefaultThemeName)
	midDir := filepath.Join(root, ThemesDirName, "midnight")
	if err := os.WriteFile(filepath.Join(defDir, "chatbox.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(midDir, "chat_arrow.webp"), []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := Load("midnight", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	exts := []string{".webp", ".apng", ".gif", ".png"}

	if path, ok := th.FindAsset("chat_arrow", exts); !ok || filepath.Dir(path) != midDir {
		t.Errorf("chat_arrow = %q ok=%v, want midnight dir", path, ok)
	}
	if path, ok := th.FindAsset("chatbox", exts); !ok || filepath.Dir(path) != defDir {
		t.Errorf("chatbox = %q ok=%v, want default-theme fallback", path, ok)
	}
	if _, ok := th.FindAsset("missing_element", exts); ok {
		t.Error("missing asset reported found")
	}
}

func TestINIToleratesCommentsAndMissingFile(t *testing.T) {
	ini, err := LoadINI(filepath.Join(t.TempDir(), "absent.ini"))
	if err == nil {
		t.Error("missing file should report the error")
	}
	if ini == nil || ini.Len() != 0 {
		t.Error("missing file must still return a usable empty INI")
	}

	path := filepath.Join(t.TempDir(), "x.ini")
	content := "; comment\n# also comment\nkey = value with = sign\nbroken-line\n[Sec]\nInner=2\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ini, err = LoadINI(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ini.Get("KEY"); !ok || v != "value with = sign" {
		t.Errorf("Get(KEY) = %q ok=%v", v, ok)
	}
	if v, ok := ini.GetSection("sec", "inner"); !ok || v != "2" {
		t.Errorf("section get = %q ok=%v", v, ok)
	}
}

// TestHasFont distinguishes "theme defines this element" from the parser's
// built-in defaults — appliers keep their own colors otherwise.
func TestHasFont(t *testing.T) {
	dir := t.TempDir()
	themes := filepath.Join(dir, "themes", "x")
	if err := os.MkdirAll(themes, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := "message = 16\nshowname_color = 10, 20, 30\n"
	if err := os.WriteFile(filepath.Join(themes, FontsFileName), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Load("x", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if !th.HasFont("message") || !th.HasFont("showname") {
		t.Error("defined elements must report HasFont")
	}
	if th.HasFont("music_display") {
		t.Error("undefined element must not report HasFont")
	}
	if c := th.Font("showname").Color; c.R != 10 || c.G != 20 || c.B != 30 {
		t.Errorf("showname color = %+v", c)
	}
}
