package theme

// QSettings parity tests for the INI reader.
//
// AO2-Client reads every theme INI (and every char.ini —
// text_file_functions.cpp:411) through QSettings::IniFormat, so anything
// QSettings silently normalises is data a theme author has already relied on.
// Two such normalisations were missing here and cost real shipped themes real
// geometry; the fixtures below are the exact lines that broke, transcribed from
// the themes rather than invented.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testBOM spells the UTF-8 byte-order mark out here rather than reusing the
// parser's own constant, so these tests compile — and fail — against the
// pre-fix reader too.
const testBOM = "\ufeff"

// TestParseINIStripsUTF8BOM pins that a byte-order mark never becomes part of
// the first key. Qt strips it, so a BOM'd file's first entry loads under its
// plain name; without this the key was "\ufeffshowname" and the entry vanished.
func TestParseINIStripsUTF8BOM(t *testing.T) {
	ini, err := ParseINI(strings.NewReader(testBOM + "showname = 16\nmessage = 12\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := ini.Get("showname"); !ok || v != "16" {
		t.Errorf("showname = %q ok=%v, want \"16\" — the BOM ate the first key", v, ok)
	}
	if _, ok := ini.Get(testBOM + "showname"); ok {
		t.Error("the BOM-prefixed key must not exist at all")
	}
	if v, ok := ini.Get("message"); !ok || v != "12" {
		t.Errorf("message = %q ok=%v", v, ok)
	}

	// A BOM is by definition a FILE prefix. A U+FEFF anywhere else is ordinary
	// content, so stripping it there would be inventing behaviour Qt has not
	// got — the guard is deliberately first-line only.
	ini, err = ParseINI(strings.NewReader("showname = 16\n" + testBOM + "message = 12\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ini.Get("message"); ok {
		t.Error("a mid-file U+FEFF must stay part of the key (it is not a BOM)")
	}
}

// TestBOMThemeKeepsItsFontSize is the Uminek2x/Uminek3x case end to end: those
// themes ship a BOM'd courtroom_fonts.ini whose very first line is the showname
// size, so the whole file's first declaration was being dropped and the showname
// silently fell back to the parser's 12.
func TestBOMThemeKeepsItsFontSize(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ThemesDirName, "bommed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fonts := testBOM + "showname = 16\nshowname_font = arnopro\nshowname_color = 255, 255, 255\n"
	if err := os.WriteFile(filepath.Join(dir, FontsFileName), []byte(fonts), 0o644); err != nil {
		t.Fatal(err)
	}
	// The same theme's design INI opens with a comment line, so the BOM lands on
	// a ';' — that must still be recognised as a comment, not smuggled through.
	design := testBOM + "; Client size.\ncourtroom = 0, 0, 1072, 600\n"
	if err := os.WriteFile(filepath.Join(dir, DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatal(err)
	}

	th, err := Load("bommed", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	sn := th.Font("showname")
	if sn.Size != 16 || !sn.SizeSet {
		t.Errorf("showname font = %+v, want size 16 declared", sn)
	}
	if !th.HasFont("showname") {
		t.Error("HasFont(showname) must be true on a BOM'd file")
	}
	if r, ok := th.ElementRect("courtroom"); !ok || r != (Rect{X: 0, Y: 0, W: 1072, H: 600}) {
		t.Errorf("courtroom rect = %+v ok=%v", r, ok)
	}
}

// TestElementRectTruncatesInlineComment pins the three rects that shipped
// broken. Each declares a valid four-tuple followed by a ';' comment; without
// truncation the fourth strconv.Atoi failed, H came back 0, and Rect.Valid()
// then rejected the whole element.
func TestElementRectTruncatesInlineComment(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ThemesDirName, "commented")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Verbatim shapes from Lymantriina and "final fantasy 1".
	design := "" +
		"music_name = 1, 1, 134, 22  ; Relative to music_display.\n" +
		"ic_chat_name = 780, 368, 90, 24  ; Figure out the difference in modern AO.\n" +
		"ooc_chat_message = 585, 324, 227, 23;492, 281, 222, 19\n" +
		"music_label = 0, 0, 0, 0  ; Removed in favor of icons.\n"
	if err := os.WriteFile(filepath.Join(dir, DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Load("commented", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		want Rect
	}{
		{"music_name", Rect{X: 1, Y: 1, W: 134, H: 22}},
		{"ic_chat_name", Rect{X: 780, Y: 368, W: 90, H: 24}},
		{"ooc_chat_message", Rect{X: 585, Y: 324, W: 227, H: 23}},
	} {
		got, ok := th.ElementRect(tc.key)
		if !ok || got != tc.want {
			t.Errorf("%s = %+v ok=%v, want %+v", tc.key, got, ok, tc.want)
		}
		if !got.Valid() {
			t.Errorf("%s must be a usable rect, got %+v", tc.key, got)
		}
	}
	// A theme that deliberately zeroes an element still reads as zero — the
	// comment is stripped, the author's intent is not overridden.
	if got, ok := th.ElementRect("music_label"); !ok || got != (Rect{}) {
		t.Errorf("music_label = %+v ok=%v, want the declared 0,0,0,0", got, ok)
	}
}

// TestINIQtCommentSemantics pins the exact QSettings::IniFormat rules the
// truncation implements, including the two it must NOT overreach into.
// Every expectation here was measured against a compiled Qt 6.5.3 probe.
func TestINIQtCommentSemantics(t *testing.T) {
	src := "" +
		"clock_0_align = center;\n" + // trailing ';' with nothing after it
		"empty_after = ;justacomment\n" + // value is entirely comment
		"hash_inline = a # b\n" + // '#' inline is ORDINARY TEXT in Qt
		"trailing_ws = 22   ; note\n" + // whitespace between value and comment
		"; whole line comment\n" +
		"# hash comment line\n" +
		"foo;bar = 1\n" + // ';' before the '=' kills the whole line
		"[Sec] ; header with a note\n" +
		"inner = 5\n"
	ini, err := ParseINI(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, want string }{
		{"clock_0_align", "center"},
		{"empty_after", ""},
		{"hash_inline", "a # b"},
		{"trailing_ws", "22"},
	} {
		if got, ok := ini.Get(tc.key); !ok || got != tc.want {
			t.Errorf("%s = %q ok=%v, want %q", tc.key, got, ok, tc.want)
		}
	}
	if _, ok := ini.Get("foo;bar"); ok {
		t.Error(`"foo;bar = 1" must be dropped: Qt truncates before the '=' so the line has no separator`)
	}
	if _, ok := ini.Get("foo"); ok {
		t.Error(`"foo;bar = 1" must not leave a bare "foo" key either`)
	}
	// The header's trailing comment must not stop the section from opening.
	if got, ok := ini.GetSection("sec", "inner"); !ok || got != "5" {
		t.Errorf("[Sec] ; note → sec/inner = %q ok=%v, want \"5\"", got, ok)
	}
	// Whole-line comments stay comments now that the ';' rule is line-level.
	if ini.Len() != 5 {
		t.Errorf("key count = %d, want 5 (4 root keys + sec/inner)", ini.Len())
	}
}

// TestAtoiTrimMatchesQtToInt pins the deliberate error-swallowing in atoiTrim.
// QString::toInt() returns 0 for junk and AO2 uses that unchecked, so a stricter
// parser here would reject tuples that render fine in the client every theme was
// authored against.
func TestAtoiTrimMatchesQtToInt(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"22", 22},
		{"  22  ", 22},
		{"8SS", 0}, // Qt 6.5.3: QString("8SS").toInt() == 0
		{"", 0},
		{"-15", -15}, // negative showname Y is legitimate theme data
	} {
		if got := atoiTrim(tc.in); got != tc.want {
			t.Errorf("atoiTrim(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
