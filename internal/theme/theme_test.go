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

// aoDefaultFonts mimics a real AO2 courtroom_fonts.ini: several elements, each
// with its own size, plus a per-element family and weight (#39). Deliberately no
// "message_font": the FontFile tests below pin that a theme declaring no family
// for the chatbox message never adopts an arbitrary font.
const aoDefaultFonts = `message = 9
message_color = 255, 255, 255
showname = 8
showname_bold = 1
showname_color = 0, 255, 165
showname_font = Igiari
ic_chatlog = 10
music_list = 8
`

func TestThemeLoadsAO2DesignAndFonts(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, DefaultThemeName, aoDefaultDesign, aoDefaultFonts)

	th, err := Load(DefaultThemeName, "", []string{root})
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
	// #39: every element carries its own size, and the parser distinguishes a
	// declared one from its own default.
	if ic := th.Font("ic_chatlog"); ic.Size != 10 || !ic.SizeSet {
		t.Errorf("ic_chatlog font = %+v, want size 10 declared", ic)
	}
	if ml := th.Font("music_list"); ml.Size != 8 || !ml.SizeSet {
		t.Errorf("music_list font = %+v, want size 8 declared", ml)
	}
	if an := th.Font("area_list"); an.SizeSet {
		t.Errorf("area_list font = %+v, want SizeSet false (undeclared)", an)
	}
	if sn.Font != "Igiari" {
		t.Errorf("showname_font = %q, want Igiari", sn.Font)
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

	th, err := Load("midnight", "", []string{root})
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

	th, err := Load("midnight", "", []string{root})
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

// TestFindAssetInRefusesAnEmptyStem is the guard FindAssetIn opens with, driven
// against the file it exists to refuse.
//
// An empty stem means "this variant does not apply to this theme". The chatbox's
// blank rung is the one caller that can produce one — a theme whose BASE skin
// already resolved to chatblank has no separate blank variant (internal/ui
// chatboxfit.go, chatboxRung.fileStem) — and without the guard filepath.Join
// builds <dir>/.png. That is a legal filename on every filesystem this ships on,
// so a theme could ship one and have it silently adopted as a variant skin:
// unnamed art pinned into T1 for as long as the theme is applied.
//
// The fixture ships exactly that dotfile in both entry points' path, so the test
// fails the moment the guard is deleted rather than passing vacuously — and the
// last block proves the directory is probeable at all, so "found nothing" above
// cannot be an accident of the fixture.
func TestFindAssetInRefusesAnEmptyStem(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, DefaultThemeName, aoDefaultDesign, "")
	dir := filepath.Join(root, ThemesDirName, DefaultThemeName)
	exts := []string{".webp", ".apng", ".gif", ".png"}

	dot := filepath.Join(dir, ".png")
	if err := os.WriteFile(dot, []byte("png"), 0o644); err != nil {
		t.Skipf("this filesystem will not hold a %q dotfile: %v", ".png", err)
	}
	if _, err := os.Stat(dot); err != nil {
		t.Skipf("the dotfile did not survive: %v", err)
	}

	if path, ok := FindAssetIn(dir, "", exts); ok {
		t.Errorf("the empty stem adopted %q — a dotfile is not a variant skin", path)
	}
	// The whole-theme ladder in front of it walks every theme dir through the same
	// helper, so it inherits the refusal; a theme that inherits from another must
	// not pick up the PARENT's dotfile either.
	th, err := Load(DefaultThemeName, "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if path, ok := th.FindAsset("", exts); ok {
		t.Errorf("FindAsset(\"\") resolved %q", path)
	}

	if err := os.WriteFile(filepath.Join(dir, "chat.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := FindAssetIn(dir, "chat", exts); !ok {
		t.Fatal("the fixture directory is not probeable at all — the empty-stem cases above prove nothing")
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
	th, err := Load("x", "", []string{dir})
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
