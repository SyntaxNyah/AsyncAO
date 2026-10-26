package ui

// The W4 font-intake gate: `[fonts]` + `[fontbind]` bind a courtroom element to a
// font FILE INSIDE THE THEME.
//
// It is the answer to the single most-reported theme confusion in this client.
// AO2's courtroom_fonts.ini names a FAMILY and leaves finding it to Qt's system font
// database, so a theme downloaded on its own — which is how almost all of them are
// shared — arrives with every family named and not one file present, renders in the
// client's own face, and says nothing. The sidecar's two sections close that: the
// theme carries its own type and it renders the same on a machine that has never
// heard of the family.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// copyFontFixture puts a real, openable font file at <dir>/<rel>. A real one,
// because internFace READS it and refuses an empty or unreadable file — a fixture of
// zero bytes would pass a binding test by taking the failure path.
func copyFontFixture(t *testing.T, dir, rel string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("fonts", "OpenDyslexic-Regular.otf"))
	if err != nil {
		t.Fatalf("reading the bundled font fixture: %v", err)
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, src, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSidecarFontBindResolvesAFileInsideTheTheme(t *testing.T) {
	dir := t.TempDir()
	copyFontFixture(t, dir, "fonts/CourtSerif.otf")
	copyFontFixture(t, dir, "BesideTheINI.otf")

	sc := theme.NewSidecar()
	sc.Fonts = []theme.FontFamily{
		{Family: "Court Serif", File: "CourtSerif.otf"},
		{Family: "Loose", File: "BesideTheINI.otf"},
		{Family: "Ghost", File: "not-on-disk.otf"},
	}
	sc.FontBind = []theme.KV{
		{Key: "message", Value: "Court Serif"},
		{Key: "showname", Value: "Loose"},   // the beside-the-INI rung
		{Key: "ic_chatlog", Value: "Ghost"}, // declared, missing
		{Key: "music_list", Value: "Never"}, // bound to a family [fonts] never declares
	}

	var res themeApply
	res.applySidecarFonts(sc, dir)

	idx := func(id string) int {
		for i, e := range theme.FontElements {
			if e == id {
				return i
			}
		}
		t.Fatalf("%q is not a FontElements id", id)
		return -1
	}
	if got := res.fontTable.e[idx("message")].face; got == 0 {
		t.Error("`message` bound to a family whose file is in <theme>/fonts resolved to no face — the " +
			"whole point of the binding is that the theme carries its own type")
	}
	if got := res.fontTable.e[idx("showname")].face; got == 0 {
		t.Error("`showname` bound to a file BESIDE the INI resolved to no face — a hand-authored theme " +
			"that dropped the .otf next to its sidecar is the ordinary shape, not an error")
	}
	// Two distinct files, two distinct slots: the intern is by resolved PATH, so a
	// theme that binds four elements to one family costs one face, and one that binds
	// two families costs two.
	if a, b := res.fontTable.e[idx("message")].face, res.fontTable.e[idx("showname")].face; a == b {
		t.Errorf("two different files interned to the same face slot (%d)", a)
	}
	// The two failures leave the element on whatever the AO2 tier gave it — the
	// author asked to CHANGE a face, not to lose one — and both are REPORTED, because
	// a font that silently does nothing is exactly how this feature confuses people.
	for _, id := range []string{"ic_chatlog", "music_list"} {
		if got := res.fontTable.e[idx(id)].face; got != 0 {
			t.Errorf("%q bound to an unresolvable family took face slot %d", id, got)
		}
	}
	if len(res.themeFontsMissing) != 2 {
		t.Errorf("%d families reported missing, want 2 (%v) — an unresolved binding must be visible where "+
			"it was typed, not only in a debug log", len(res.themeFontsMissing), res.themeFontsMissing)
	}

	// A theme with no [fontbind] changes nothing at all: the zero table is the
	// pre-W4 path, byte for byte.
	var untouched themeApply
	untouched.applySidecarFonts(theme.NewSidecar(), dir)
	if untouched.fontTable != (themeFontTable{}) || len(untouched.faceData) != 0 {
		t.Error("a sidecar with no [fontbind] touched the font table — every install without one must be " +
			"identical to the build before this landed")
	}
	// And a nil sidecar (every stock AO2 theme) is nil-safe.
	var stock themeApply
	stock.applySidecarFonts(nil, dir)
	if stock.fontTable != (themeFontTable{}) {
		t.Error("a nil sidecar touched the font table")
	}
}

// TestSidecarFontBindRefusesAPathOutsideTheTheme pins the guard, because a
// [fonts] file name comes out of a file a stranger wrote and the format's §6 says
// in as many words that no path may leave the theme folder.
func TestSidecarFontBindRefusesAPathOutsideTheTheme(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Dir(dir)
	copyFontFixture(t, outside, "Escaped.otf")

	for _, evil := range []string{"../Escaped.otf", "..\\Escaped.otf", "fonts/../../Escaped.otf"} {
		if _, ok := themeSidecarFontPath(dir, evil); ok {
			t.Errorf("themeSidecarFontPath accepted %q — a theme must not be able to read a file outside "+
				"its own folder", evil)
		}
	}
	// The legitimate spellings still resolve, or the guard would be refusing
	// everything and passing this test for the wrong reason.
	copyFontFixture(t, dir, "fonts/Fine.otf")
	if _, ok := themeSidecarFontPath(dir, "Fine.otf"); !ok {
		t.Error("a file under <theme>/fonts did not resolve")
	}
}

// TestElementFontAndSizeAreCarriedNotDrawn pins the OTHER half of W4's font
// decision, in both places it is stated.
//
// An element's own `font`/`size` are parsed and preserved and are NOT read by the
// format-1 renderer (paintElementText carries the measurement). That is a
// deliberate ruling, and the danger with a deliberate ruling is that it decays into
// a forgotten bug: fourteen shipped themes write those keys, so "nothing happens"
// has to be a documented answer rather than a discovery. This fails if the
// behaviour changes without the doc, or the doc without the behaviour.
func TestElementFontAndSizeAreCarriedNotDrawn(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	sc := theme.NewSidecar()
	sc.Elements = []theme.Element{{
		ID: "masthead", Kind: theme.ElemText, Band: theme.BandOverlay, Space: theme.SpaceCourtroom,
		Rect: theme.Rect{X: 20, Y: 20, W: 300, H: 40},
		Text: "AMADEUS", Font: "Terminal Mono", Size: 27,
		Color: theme.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
	}}
	a.themeSidecar = sc
	a.themeLay.valid = false
	a.drawCourtroom(1280, 720)
	if a.themeLay.elN != 1 {
		t.Fatalf("the fixture baked %d elements, want 1", a.themeLay.elN)
	}
	// CARRIED: the reader kept both values, so a save round-trips them and a later
	// format can start honouring them without an upgrade path.
	if got := sc.Elements[0]; got.Font != "Terminal Mono" || got.Size != 27 {
		t.Errorf("the reader dropped the element's font/size (%q / %d) — the keys must survive a load", got.Font, got.Size)
	}
	// NOT DRAWN: the baked row still points at the client chrome face.
	if got := a.themeLay.el[0].fontIdx; got != elemFontChrome {
		t.Errorf("the bake resolved fontIdx %d for an element that declares a font — if element fonts now "+
			"render, docs/THEME-FORMAT.md §3's \"carried, not drawn in format 1\" paragraph is a lie", got)
	}

	// And the format doc says so, in the section an author reads.
	src, err := os.ReadFile(filepath.Join("..", "..", "docs", "THEME-FORMAT.md"))
	if err != nil {
		t.Fatalf("reading the format doc: %v", err)
	}
	for _, want := range []string{"Carried, not drawn in format 1", "`font` and `size` are carried, not drawn in format 1"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("docs/THEME-FORMAT.md never says %q — an author who sets `size = 27` and sees nothing "+
				"has no way to find out that is the answer", want)
		}
	}
}
