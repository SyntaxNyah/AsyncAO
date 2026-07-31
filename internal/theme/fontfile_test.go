package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFontFileIsOnlyForThemesThatNameNothing pins FontFile's SCOPE, which is the
// whole client's font chain — not any one element's.
//
// A theme that names families dresses its elements individually (FontFiles, #39)
// and must not also impose a face on menus, the lobby and settings. FontFile used
// to return the declared MESSAGE family first, which predates per-element fonts:
// aceattorney2x's chatbox family became the app font, while the per-element table
// was separately applying that same face to the message anyway.
//
// The case it exists for survives: a theme that ships one .ttf and declares no
// family at all still restyles the client (the original #6 request).
func TestFontFileIsOnlyForThemesThatNameNothing(t *testing.T) {
	// (a) Names a family → dresses that element, imposes nothing globally.
	root := t.TempDir()
	writeTheme(t, root, "Named", aoDefaultDesign, "message = 12\nmessage_font = Igiari\n")
	if err := os.WriteFile(filepath.Join(root, ThemesDirName, "Named", "Igiari.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Load("Named", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if !th.NamesAnyFontFamily() {
		t.Error("a theme declaring message_font must report naming a family")
	}
	if got := th.FontFileFor("message", nil); filepath.Base(got) != "Igiari.ttf" {
		t.Errorf("the ELEMENT still resolves its own face: got %q", got)
	}
	if got := th.FontFile(); got != "" {
		t.Errorf("FontFile = %q, want none — a naming theme must not become the app font", got)
	}

	// (b) Names nothing but ships a font → the #6 client-wide install still works.
	root2 := t.TempDir()
	writeTheme(t, root2, "Bundled", aoDefaultDesign, "message = 12\n")
	if err := os.WriteFile(filepath.Join(root2, ThemesDirName, "Bundled", "OneFace.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	th2, err := Load("Bundled", []string{root2})
	if err != nil {
		t.Fatal(err)
	}
	if th2.NamesAnyFontFamily() {
		t.Error("a theme declaring only sizes must not report naming a family")
	}
	if got := th2.FontFile(); filepath.Base(got) != "OneFace.ttf" {
		t.Errorf("FontFile = %q, want the bundled OneFace.ttf (#6 must still work)", got)
	}
}

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
	if got := th.FontFileFor("message", nil); filepath.Base(got) != "Igiari.ttf" {
		t.Errorf("FontFileFor(message) = %q, want the family-matching Igiari.ttf", got)
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
	if got := th.FontFileFor("message", nil); filepath.Base(got) != "ace_attorney.ttf" {
		t.Errorf("FontFileFor(message) = %q, want base/fonts/ace_attorney.ttf", got)
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

// TestFontFileFromThemeFontsSubdir is the reporter's case for #39 (Lymantriina):
// the theme declares "IBM Plex Serif" and ships it in its OWN fonts/ subfolder.
// The pre-#39 scan skipped every directory entry, so the family never resolved
// and the theme's font silently never applied. AO2-Client registers <base>/fonts
// RECURSIVELY (main.cpp:44-55), so a subfolder is a normal place to find one.
func TestFontFileFromThemeFontsSubdir(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Lymantriina", aoDefaultDesign,
		"message = 13\nmessage_font = IBM Plex Serif\n")
	fontsDir := filepath.Join(root, ThemesDirName, "Lymantriina", "fonts")
	if err := os.MkdirAll(fontsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"IBMPlexMono-Regular.otf", "IBMPlexSerif-Regular.otf"} {
		if err := os.WriteFile(filepath.Join(fontsDir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th, err := Load("Lymantriina", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.FontFileFor("message", nil); filepath.Base(got) != "IBMPlexSerif-Regular.otf" {
		t.Errorf("FontFileFor(message) = %q, want the theme's own fonts/IBMPlexSerif-Regular.otf", got)
	}
	// FontFile is the CLIENT-WIDE chain, and a theme that names families dresses its
	// elements individually instead — see NamesAnyFontFamily.
	if got := th.FontFile(); got != "" {
		t.Errorf("FontFile = %q, want none: this theme names a family, so it must not also become the app font", got)
	}
}

// TestFontFileBaseFontsRecursive: <root>/fonts is walked recursively (AO2's
// QDirIterator::Subdirectories), but only as deep as fontScanMaxDepth — the
// named cap that keeps the walk bounded (hard rule 4).
func TestFontFileBaseFontsRecursive(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "DRRA", aoDefaultDesign, "message = 24\nmessage_font = Ace Attorney\n")
	nested := filepath.Join(root, "fonts", "aa")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "ace_attorney.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Load("DRRA", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := th.FontFileFor("message", nil); filepath.Base(got) != "ace_attorney.ttf" {
		t.Errorf("FontFileFor = %q, want the nested base/fonts/aa/ace_attorney.ttf", got)
	}

	// Past the depth cap it must NOT resolve.
	deepRoot := t.TempDir()
	writeTheme(t, deepRoot, "DRRA", aoDefaultDesign, "message = 24\nmessage_font = Ace Attorney\n")
	deep := filepath.Join(deepRoot, "fonts", "a", "b", "c", "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "ace_attorney.ttf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deepTheme, err := Load("DRRA", []string{deepRoot})
	if err != nil {
		t.Fatal(err)
	}
	if got := deepTheme.FontFileFor("message", nil); got != "" {
		t.Errorf("FontFileFor = %q, want empty past fontScanMaxDepth=%d", got, fontScanMaxDepth)
	}
}

// TestFontFileSystemDirAlias pins the system-font tier — the ONLY way a theme
// declaring plain "Arial" or "Times New Roman" (DRRetribution, KFO qHD, DR Theme)
// can resolve at all, since neither ships the file. AO2 gets this from Qt's
// system font database (get_qfont, courtroom.cpp:1263).
func TestFontFileSystemDirAlias(t *testing.T) {
	root := t.TempDir()
	sys := t.TempDir()
	for _, f := range []string{"arial.ttf", "times.ttf"} {
		if err := os.WriteFile(filepath.Join(sys, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeTheme(t, root, "DRRet", aoDefaultDesign,
		"message = 10\nmessage_font = Arial\nshowname = 8\nshowname_font = Times New Roman\n")
	th, err := Load("DRRet", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	files := th.FontFiles([]string{sys})
	if got := filepath.Base(files["message"]); got != "arial.ttf" {
		t.Errorf("message font = %q, want arial.ttf (plain stem match)", got)
	}
	if got := filepath.Base(files["showname"]); got != "times.ttf" {
		t.Errorf("showname font = %q, want times.ttf (via systemFontAliases)", got)
	}
	// Without the system dirs the same theme resolves NOTHING — the tier is what
	// makes it work, not a lucky match somewhere else.
	if got := th.FontFiles(nil); len(got) != 0 {
		t.Errorf("FontFiles(nil) = %v, want no matches without the system tier", got)
	}
}

// TestFontFilesPerElement is the core of #39: DIFFERENT elements resolve to
// DIFFERENT families in one bounded walk (3DS Widescreen's shape — Igiari
// Cyrillic for the chat surfaces, Ace Name for the name/list ones).
func TestFontFilesPerElement(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "3DS", aoDefaultDesign, `showname = 12
showname_font = Ace Name
message = 24
message_font = Igiari Cyrillic
ic_chatlog = 12
ic_chatlog_font = Igiari Cyrillic
music_list = 6
music_list_font = Ace Name
area_list = 6
`)
	dir := filepath.Join(root, ThemesDirName, "3DS", "fonts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"igiari-cyrillic.ttf", "ace_name_regular.ttf"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th, err := Load("3DS", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	files := th.FontFiles(nil)
	want := map[string]string{
		"showname":   "ace_name_regular.ttf",
		"message":    "igiari-cyrillic.ttf",
		"ic_chatlog": "igiari-cyrillic.ttf",
		"music_list": "ace_name_regular.ttf",
	}
	for id, base := range want {
		if got := filepath.Base(files[id]); got != base {
			t.Errorf("%s font = %q, want %q", id, got, base)
		}
	}
	// area_list declares a SIZE but no family — it must resolve to nothing rather
	// than inherit another element's face.
	if got, ok := files["area_list"]; ok {
		t.Errorf("area_list resolved to %q; a familyless element must stay unmapped", got)
	}
	if got := th.Font("area_list"); !got.SizeSet || got.Size != 6 {
		t.Errorf("area_list spec = %+v, want SizeSet with Size 6", got)
	}
}

// TestFontSpecFullParse pins every per-element attribute AO2's set_font reads
// (courtroom.cpp:1212), including the SizeSet/ColorSet flags that tell a declared
// value apart from the parser's default — HasFont conflates the two, so a theme
// that only sets a colour must not be read as also setting a size.
func TestFontSpecFullParse(t *testing.T) {
	root := t.TempDir()
	writeTheme(t, root, "Full", aoDefaultDesign, `showname = 14
showname_bold = 1
showname_sharp = 1
showname_font = Igiari
showname_color = 1, 2, 3
music_name_color = 4, 5, 6
`)
	th, err := Load("Full", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	sn := th.Font("showname")
	if sn.Size != 14 || !sn.SizeSet || !sn.Bold || !sn.Sharp || sn.Font != "Igiari" ||
		!sn.ColorSet || sn.Color != (RGB{1, 2, 3}) {
		t.Errorf("showname spec = %+v", sn)
	}
	// Colour only: the size is the parser's default and must be flagged as such.
	mn := th.Font("music_name")
	if mn.SizeSet || !mn.ColorSet || mn.Color != (RGB{4, 5, 6}) {
		t.Errorf("music_name spec = %+v, want ColorSet without SizeSet", mn)
	}
	// Nothing declared at all.
	if ml := th.Font("music_list"); ml.SizeSet || ml.ColorSet || ml.Bold || ml.Sharp || ml.Font != "" {
		t.Errorf("music_list spec = %+v, want all-unset", ml)
	}
}

// TestFontElementsIsAppendOnly pins the property that actually matters:
// internal/ui indexes its resolved per-element table by POSITION in this list, so
// an identifier inserted or reordered silently hands every later element another's
// family, size, bold and colour.
//
// Expressed as a frozen prefix rather than one flat expected list on purpose. A
// flat list makes an append and an insert look identical — both are just "the
// literal changed", and the reviewer's only defence is noticing which line moved.
// This way an APPEND is a deliberate one-line addition to `added`, while an INSERT
// fails loudly against the frozen prefix.
//
// The order is NOT AO2's set_fonts call order, and never was — AO2 calls
// music_name after area_list (courtroom.cpp:1201-1202). It does not need to be:
// the pairing that must hold is with internal/ui's themeFontElem enum, which
// TestThemeFontElemOrder pins from the other side.
func TestFontElementsIsAppendOnly(t *testing.T) {
	// Frozen: these seven shipped first, in this order. Never edit this slice —
	// only ever append to `added`.
	frozen := []string{"showname", "message", "ic_chatlog", "server_chatlog", "music_list", "music_name", "area_list"}
	// Appended since, in the order they were added.
	added := []string{"debug_log"}

	want := append(append([]string{}, frozen...), added...)
	if len(FontElements) != len(want) {
		t.Fatalf("FontElements has %d entries %v, want %d %v — a NEW identifier must be appended to `added`, never inserted",
			len(FontElements), FontElements, len(want), want)
	}
	for i, id := range want {
		if FontElements[i] != id {
			t.Errorf("FontElements[%d] = %q, want %q — reordering shifts every later element's resolved font",
				i, FontElements[i], id)
		}
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

// TestSharpAcceptsOnlyLiteralOne pins the exact polarity of the _sharp read.
//
// The key reads like a boolean and every instinct says to parse it as one, but
// AO2 is `bool antialias = get_design_element(id + "_sharp", ...) != "1"`
// (courtroom.cpp:1237). Only the literal string "1" disables antialiasing —
// "true", "yes" and "0" all leave it ON. Widening this to a boolean parser would
// flip exactly the elements a theme deliberately left smooth: aceattorney2x
// writes ic_chatlog_sharp = 0 and music_name_sharp = 0 on purpose, and under a
// permissive parser "0" would read as "declared, therefore sharp".
//
// Goes red if theme.go's `raw == "1"` becomes strconv.ParseBool, `raw != "0"`, or
// anything that trims and re-interprets the value.
func TestSharpAcceptsOnlyLiteralOne(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"1", true},
		{"0", false},
		{"true", false}, // NOT a spelling AO2 accepts
		{"yes", false},
		{"", false},
		{"11", false},
		{"1.0", false},
	} {
		root := t.TempDir()
		fonts := "showname = 14\n"
		if tc.raw != "" {
			fonts += "showname_sharp = " + tc.raw + "\n"
		}
		writeTheme(t, root, "Polarity", aoDefaultDesign, fonts)
		th, err := Load("Polarity", []string{root})
		if err != nil {
			t.Fatalf("load %q: %v", tc.raw, err)
		}
		if got := th.Font("showname").Sharp; got != tc.want {
			t.Errorf("showname_sharp = %q parsed as Sharp=%v, want %v "+
				"(AO2 courtroom.cpp:1237 compares against the literal \"1\")", tc.raw, got, tc.want)
		}
	}
}
