package ui

// Per-element theme font tests (#39). Everything here is pure Go — the resolved
// table, the size fold and the user-override precedence are all decided off the
// SDL path, so they pin without a renderer. The face-set behaviour that DOES need
// SDL_ttf lives in themefaces_test.go.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// writeThemeFonts fabricates a theme dir with a courtroom_fonts.ini and returns
// the loaded Theme plus its content root.
func writeThemeFonts(t *testing.T, name, fonts string, files map[string]string) (*theme.Theme, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, theme.ThemesDirName, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, theme.FontsFileName), []byte(fonts), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th, err := theme.Load(name, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	return th, root
}

// TestThemeFontElemOrder pins the ui enum against theme.FontElements. The apply
// fills slot i from FontElements[i], so a divergence would silently give one
// element another's family and size.
func TestThemeFontElemOrder(t *testing.T) {
	if int(themeFontElemCount) != len(theme.FontElements) {
		t.Fatalf("themeFontElemCount = %d, theme.FontElements has %d entries",
			themeFontElemCount, len(theme.FontElements))
	}
	want := map[themeFontElem]string{
		elemShowname:      "showname",
		elemMessage:       "message",
		elemICChatlog:     "ic_chatlog",
		elemServerChatlog: "server_chatlog",
		elemMusicList:     "music_list",
		elemMusicName:     "music_name",
		elemAreaList:      "area_list",
		elemDebugLog:      "debug_log",
	}
	for el, id := range want {
		if theme.FontElements[el] != id {
			t.Errorf("element %d = %q, want %q", el, theme.FontElements[el], id)
		}
	}
}

// TestThemeFontPxIsQtsConversion pins the half that was simply missing: a POINT is
// 1/72 inch and Qt renders at a logical 96 DPI, so courtroom_fonts.ini's point
// sizes are P×96/72 PIXELS. Treating them as pixels rendered every themed element
// a third too small — aceattorney2x's 24 pt message opened at 24 px where AO2
// gives 32, which is most of "the text is the wrong size".
func TestThemeFontPxIsQtsConversion(t *testing.T) {
	// Every size aceattorney2x actually declares, plus the corpus boundaries.
	for _, tc := range []struct{ points, want int }{
		{24, 32}, // message
		{12, 16}, // showname
		{10, 13}, // ic_chatlog, evidence_description
		{8, 11},  // ms/server_chatlog, music_list, music_name, area_list
		{14, 19}, // evidence_name
		{16, 21}, // clock_N
		{6, 8},   // 3DS Widescreen's music_list — the smallest in the corpus
		{9, 12},
	} {
		if got := themeFontPx(tc.points); got != tc.want {
			t.Errorf("themeFontPx(%d pt) = %d px, want %d", tc.points, got, tc.want)
		}
	}
}

// TestThemeFontPctRoundTripsToThePixelSize is the SECOND truncation. The percent is
// resolved back to a face at UIFontSize×pct/100, which rounds DOWN — so a percent
// that is merely close reopens a pixel short, and the two truncations compounded
// into a 7 px face for a declared 8 pt.
//
// It asserts the ROUND TRIP rather than a magic percent: whatever the scale is,
// asking for P points must reopen at exactly themeFontPx(P).
func TestThemeFontPctRoundTripsToThePixelSize(t *testing.T) {
	for points := 6; points <= 32; points++ {
		pct := themeFontPct(points)
		reopened := UIFontSize * pct / 100 // exactly what buildSet does
		if want := themeFontPx(points); reopened != want {
			t.Errorf("%d pt → %d%% → reopens at %d px, want %d", points, pct, reopened, want)
		}
	}
}

// TestElemPctFolds pins the fold of theme size × user zoom, the untouched
// pass-through for an element the theme didn't size, and the clamps.
func TestElemPctFolds(t *testing.T) {
	a := testTabApp(t)
	for _, tc := range []struct {
		name          string
		themePct      int
		userPct, want int
	}{
		{"undressed passes the user zoom straight through", 0, 137, 137},
		{"undressed at any zoom", 0, 75, 75},
		{"theme 200% at default zoom", 200, 100, 200},
		{"theme 200% doubled by a 150% zoom", 200, 150, 300},
		{"theme 50% (music_list = 6) survives the log zoom floor", 50, 100, 50},
		{"clamped at the ceiling", 400, 250, themeFontMaxPct},
		{"clamped at the floor", 25, 50, themeFontMinPct},
	} {
		a.themeFonts = themeFontTable{}
		a.themeFonts.e[elemMusicList].pct = tc.themePct
		if got := a.elemPct(elemMusicList, tc.userPct); got != tc.want {
			t.Errorf("%s: elemPct(theme=%d, user=%d) = %d, want %d",
				tc.name, tc.themePct, tc.userPct, got, tc.want)
		}
	}
	// A dressed element must NOT leak into its neighbours.
	a.themeFonts = themeFontTable{}
	a.themeFonts.e[elemICChatlog].pct = 200
	if got := a.elemPct(elemServerChatlog, 100); got != 100 {
		t.Errorf("server_chatlog picked up ic_chatlog's size: %d", got)
	}
}

// TestMusicNamePctFoldsTheCanvas pins the dominant cause of the clipped
// now-playing plate: music_name's RECT is multiplied by the canvas scale at layout
// build time, but its FACE was resolved at the theme's declared point size with no
// canvas factor at all — so on any fit that is not exactly 1:1 (Letterbox /
// Stretch / Crop / Custom, or Native in a window narrower than the design canvas)
// the box shrank and the glyphs did not. It was the only themed text surface that
// never got the fold the chatbox children have had since #21.
func TestMusicNamePctFoldsTheCanvas(t *testing.T) {
	a := testTabApp(t)
	a.themeFonts = themeFontTable{}
	a.themeFonts.e[elemMusicName].pct = 100

	full := &themeLayoutCache{textPct: DefaultScalePct} // 1:1 canvas
	half := &themeLayoutCache{textPct: 50}              // the canvas at half size

	atFull := a.themedChatPct(elemMusicName, DefaultScalePct, full)
	atHalf := a.themedChatPct(elemMusicName, DefaultScalePct, half)
	if atFull != 100 {
		t.Errorf("a 1:1 canvas must be the identity, got %d", atFull)
	}
	if atHalf >= atFull {
		t.Errorf("a half-size canvas must shrink the face: %d vs %d", atHalf, atFull)
	}
}

// TestElemLabelFontAtPctKeepsTheUndressedContract pins the #39 guarantee the
// canvas fold must not break: an element the theme says NOTHING about still draws
// in the fixed chrome face, byte-identically to a client with no theme at all. The
// consequence — an undressed music_name cannot shrink with the canvas — is
// deliberate, not an oversight.
func TestElemLabelFontAtPctKeepsTheUndressedContract(t *testing.T) {
	a := testTabApp(t)
	a.themeFonts = themeFontTable{}
	for _, pct := range []int{50, 100, 200} {
		if got := a.elemLabelFontAtPct(elemMusicName, pct); got != a.ctx.font {
			t.Errorf("undressed at %d%%: got %p, want the chrome face %p", pct, got, a.ctx.font)
		}
	}
}

// TestBuildThemeFontTablePerElement is the end-to-end resolution: a
// 3DS-Widescreen-shaped INI with two families across five elements produces the
// right per-element sizes, the right bold flags, and exactly TWO interned faces
// (real themes reuse families, so the face reads must dedupe).
func TestBuildThemeFontTablePerElement(t *testing.T) {
	th, _ := writeThemeFonts(t, "3DS", `showname = 12
showname_font = Ace Name
showname_bold = 1
message = 24
message_font = Igiari Cyrillic
ic_chatlog = 12
ic_chatlog_font = Igiari Cyrillic
music_list = 6
music_list_font = Ace Name
area_list = 6
`, map[string]string{
		"fonts/ace_name_regular.ttf": "A",
		"fonts/igiari-cyrillic.ttf":  "B",
	})
	var res themeApply
	res.buildThemeFontTable(th, nil)

	if n := len(res.faceData); n != 2 {
		t.Fatalf("interned %d faces, want 2 (one per DISTINCT family)", n)
	}
	if res.fontTable.e[elemShowname].face != res.fontTable.e[elemMusicList].face {
		t.Error("showname and music_list both declare Ace Name — they must share one interned face")
	}
	if res.fontTable.e[elemMessage].face == res.fontTable.e[elemShowname].face {
		t.Error("message declares a DIFFERENT family than showname; they must not share a face")
	}
	if got := res.fontTable.e[elemMessage].pct; got != themeFontPct(24) {
		t.Errorf("message pct = %d, want %d", got, themeFontPct(24))
	}
	if got := res.fontTable.e[elemMusicList].pct; got != themeFontPct(6) {
		t.Errorf("music_list pct = %d, want %d", got, themeFontPct(6))
	}
	if !res.fontTable.e[elemShowname].bold {
		t.Error("showname_bold = 1 must land on the table")
	}
	if res.fontTable.e[elemMessage].bold {
		t.Error("message declares no bold — it must stay false")
	}
	// area_list: sized, no family → dressed (its own pinned scale) with no face.
	al := res.fontTable.e[elemAreaList]
	if al.face != 0 || al.pct != themeFontPct(6) || !al.dressed() {
		t.Errorf("area_list = %+v, want sized-but-familyless", al)
	}
	// server_chatlog / music_name: undeclared → the zero (pre-#39) entry.
	for _, el := range []themeFontElem{elemServerChatlog, elemMusicName} {
		if res.fontTable.e[el].dressed() {
			t.Errorf("element %d = %+v, want the untouched zero entry", el, res.fontTable.e[el])
		}
	}
}

// TestBuildThemeFontTableFaceCap pins hard rule 4 on the face cache: past
// themeFaceCap distinct families the overflow elements keep the client font
// rather than growing the cache without bound.
func TestBuildThemeFontTableFaceCap(t *testing.T) {
	fonts := ""
	files := map[string]string{}
	// Families are DERIVED from the element list, not a literal sized to match it.
	// A hand-written slice indexed by element position panics the moment an
	// identifier is appended to theme.FontElements — which is what adding
	// debug_log did — and the panic reads like a production bug rather than a
	// stale fixture.
	for i, id := range theme.FontElements {
		fonts += id + " = 12\n" + id + "_font = Fam " + string(rune('A'+i)) + "\n"
		files["fonts/fam"+string(rune('a'+i))+".ttf"] = "x"
	}
	th, _ := writeThemeFonts(t, "Many", fonts, files)
	var res themeApply
	res.buildThemeFontTable(th, nil)
	if n := len(res.faceData); n != themeFaceCap {
		t.Fatalf("interned %d faces, want the cap %d", n, themeFaceCap)
	}
	over := 0
	for _, e := range res.fontTable.e {
		if e.face == 0 {
			over++
		}
		if e.face > themeFaceCap {
			t.Fatalf("face slot %d exceeds the cap %d", e.face, themeFaceCap)
		}
	}
	if over != len(theme.FontElements)-themeFaceCap {
		t.Errorf("%d elements fell back to the client font, want %d",
			over, len(theme.FontElements)-themeFaceCap)
	}
}

// TestInternFaceSkipsUnreadable: a family that resolved to a path we can't read
// leaves the element on the client font instead of poisoning a face slot.
func TestInternFaceSkipsUnreadable(t *testing.T) {
	var res themeApply
	if got := res.internFace(filepath.Join(t.TempDir(), "nope.ttf")); got != 0 {
		t.Errorf("internFace(missing) = %d, want 0", got)
	}
	if len(res.faceData) != 0 {
		t.Errorf("an unreadable face must not occupy a slot, got %d", len(res.faceData))
	}
}

// TestThemeFontsSuppressedByUserOverride pins the precedence ladder: a manual
// font override (and the dyslexia toggle) is the USER's pick and outranks the
// theme's FAMILIES — but the theme's SIZES survive, because AO2 has no user font
// override to lose to and size is the substance of #39.
func TestThemeFontsSuppressedByUserOverride(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(a *App)
	}{
		{"manual font paths", func(a *App) { a.d.Prefs.SetFontPaths(`C:\some\font.ttf`) }},
		{"dyslexia toggle", func(a *App) { a.d.Prefs.SetDyslexiaFont(true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testTabApp(t)
			tc.apply(a)
			res := &themeApply{}
			res.fontTable.e[elemICChatlog] = themeElemFont{pct: 150, face: 1, bold: true}
			res.faceData = [][]byte{{0x00}}
			a.landThemeFonts(res)
			got := a.themeFonts.e[elemICChatlog]
			if got.face != 0 {
				t.Errorf("face = %d, want 0 (the user's font wins the FAMILY)", got.face)
			}
			if got.pct != 150 || !got.bold {
				t.Errorf("size/bold must survive a user font override, got %+v", got)
			}
			if a.ctx.themeFaceData != nil {
				t.Error("no theme faces may be installed while a user override is set")
			}
		})
	}
}

// TestThemeFontsOffRestoresDefaults: with the setting off the table is zeroed, so
// every accessor returns the pre-#39 value — the opt-out has to be complete.
func TestThemeFontsOffRestoresDefaults(t *testing.T) {
	a := testTabApp(t)
	if !a.d.Prefs.ThemeFontsOn() {
		t.Fatal("theme fonts must ship ON (an AO2 theme's fonts are part of the theme)")
	}
	a.d.Prefs.SetThemeFonts(false)
	res := &themeApply{faceData: [][]byte{{0x00}}}
	for i := range res.fontTable.e {
		res.fontTable.e[i] = themeElemFont{pct: 200, face: 1, bold: true}
	}
	a.landThemeFonts(res)
	for el := themeFontElem(0); el < themeFontElemCount; el++ {
		if a.themeFonts.e[el].dressed() {
			t.Errorf("element %d still dressed with the setting off: %+v", el, a.themeFonts.e[el])
		}
		if got := a.elemPct(el, 123); got != 123 {
			t.Errorf("element %d elemPct = %d, want the user zoom 123 unchanged", el, got)
		}
		if a.elemBold(el) {
			t.Errorf("element %d still bold with the setting off", el)
		}
	}
	if a.ctx.themeFaceData != nil {
		t.Error("no theme faces may be installed with the setting off")
	}
}

// TestWrapCachesKeyOnResolvedPct pins the invalidation that keeps #42 closed
// under #39: the IC / OOC / area wrap caches key on the RESOLVED element scale,
// not on the raw user zoom. Keying on the zoom alone would leave every row
// wrapped at the previous theme's point size after a theme swap — measured in one
// size, drawn in another.
func TestWrapCachesKeyOnResolvedPct(t *testing.T) {
	a := testTabApp(t)
	a.logPct, a.oocPct, a.areaPct = DefaultScalePct, DefaultScalePct, DefaultScalePct
	a.icLog = append(a.icLog, icEntry{text: "Objection! That contradicts the autopsy report."})
	a.icLogSeq++
	a.oocLog = append(a.oocLog, "Welcome to the server, please read the rules.")
	a.oocSpeakers = append(a.oocSpeakers, "MOTD")
	a.oocSeq++
	a.sess = &courtroom.Session{Areas: []string{"Courtroom 1", "Courtroom 2"}}
	a.areaInfoSeq++

	const colW = 240
	a.icWrapped(colW, false)
	a.oocWrapped(colW)
	a.areaWrapped(nil, colW)
	if a.icWrapPct != DefaultScalePct || a.oocWrapPct != DefaultScalePct || a.areaWrapPct != DefaultScalePct {
		t.Fatalf("undressed wrap keys must be the user zoom: ic=%d ooc=%d area=%d",
			a.icWrapPct, a.oocWrapPct, a.areaWrapPct)
	}
	// A theme lands with its own sizes — nothing else changes (same log seq, same
	// width, same font generation), so ONLY the resolved pct can invalidate.
	a.themeFonts.e[elemICChatlog].pct = themeFontPct(18)
	a.themeFonts.e[elemServerChatlog].pct = themeFontPct(9)
	a.themeFonts.e[elemAreaList].pct = themeFontPct(6)
	a.icWrapped(colW, false)
	a.oocWrapped(colW)
	a.areaWrapped(nil, colW)
	if a.icWrapPct != themeFontPct(18) {
		t.Errorf("IC wrap kept the stale key %d, want %d", a.icWrapPct, themeFontPct(18))
	}
	if a.oocWrapPct != themeFontPct(9) {
		t.Errorf("OOC wrap kept the stale key %d, want %d", a.oocWrapPct, themeFontPct(9))
	}
	if a.areaWrapPct != themeFontPct(6) {
		t.Errorf("area wrap kept the stale key %d, want %d", a.areaWrapPct, themeFontPct(6))
	}
}

// TestThemeFontsPrefRoundTrips guards the known "saves but doesn't load" trap:
// the pref must be in the live struct, the on-disk DTO and the load overlay.
func TestThemeFontsPrefRoundTrips(t *testing.T) {
	a := testTabApp(t)
	if !a.d.Prefs.ThemeFontsOn() {
		t.Fatal("default must be ON")
	}
	a.d.Prefs.SetThemeFonts(false)
	if a.d.Prefs.ThemeFontsOn() {
		t.Fatal("SetThemeFonts(false) did not stick")
	}
	a.d.Prefs.SetThemeFonts(true)
	if !a.d.Prefs.ThemeFontsOn() {
		t.Fatal("SetThemeFonts(true) did not stick")
	}
}

// TestChatboxInkNeedsAnActualColourKey pins the gate on the chatbox ink, which
// used to be Theme.HasFont and had to become FontSpec.ColorSet.
//
// HasFont answers "did the theme mention this element AT ALL — size or colour",
// and FontSpec.Color carries the parser's WHITE default whenever no colour was
// declared. Together those meant a theme that only SIZED its chatbox — a very
// ordinary thing to write — silently forced white message and showname ink,
// discarding whatever the client had. AO2 cannot make this mistake: it reads the
// colour from its own key and the size key implies nothing (courtroom.cpp:1223-1225).
//
// Goes red if either gate drifts back to HasFont, or if some later refactor
// starts trusting FontSpec.Color without consulting ColorSet.
func TestChatboxInkNeedsAnActualColourKey(t *testing.T) {
	// Size only. HasFont says yes, ColorSet says no — and no is correct.
	sized, _ := writeThemeFonts(t, "SizeOnly", "message = 20\nshowname = 14\n", nil)
	if !sized.HasFont("message") {
		t.Fatal("test setup: HasFont must be true for a size-only theme, or this proves nothing")
	}
	if spec := sized.Font("message"); spec.ColorSet {
		t.Error("a theme that declared no message_color must not report ColorSet")
	}
	if spec := sized.Font("message"); spec.Color != (theme.RGB{R: 255, G: 255, B: 255}) {
		t.Errorf("the parser default is white (%v) — that is exactly why ColorSet has to be "+
			"consulted rather than the colour itself", spec.Color)
	}
	if spec := sized.Font("showname"); spec.ColorSet {
		t.Error("a theme that declared no showname_color must not report ColorSet")
	}

	// Colour declared: honoured, and reported as declared.
	coloured, _ := writeThemeFonts(t, "Coloured",
		"message = 20\nmessage_color = 10, 20, 30\nshowname_color = 40, 50, 60\n", nil)
	msg := coloured.Font("message")
	if !msg.ColorSet || msg.Color != (theme.RGB{R: 10, G: 20, B: 30}) {
		t.Errorf("message spec = %+v, want the declared colour with ColorSet", msg)
	}
	// Colour WITHOUT a size must still count — the two keys are independent.
	sn := coloured.Font("showname")
	if !sn.ColorSet || sn.Color != (theme.RGB{R: 40, G: 50, B: 60}) {
		t.Errorf("showname spec = %+v, want the declared colour even with no size key", sn)
	}
	if sn.SizeSet {
		t.Error("showname declared no size — SizeSet must stay false")
	}
}

// TestDeclaredFontInkIgnoresTheParserDefault asserts the GATE directly, which the
// test above cannot: that one pins the parser behaviour that makes the gate
// correct, but would stay green if the gate itself reverted to Theme.HasFont.
//
// Goes red the moment declaredFontInk trusts spec.Color without consulting
// spec.ColorSet — which is precisely the bug, since the parser's default colour is
// opaque white and would be reported as the theme's deliberate choice.
func TestDeclaredFontInkIgnoresTheParserDefault(t *testing.T) {
	// The exact shape a size-only theme produces: the WHITE parser default, unset.
	sizeOnly := theme.FontSpec{Size: 20, SizeSet: true, Color: theme.RGB{R: 255, G: 255, B: 255}}
	if _, ok := declaredFontInk(sizeOnly); ok {
		t.Error("a spec with ColorSet=false must report no ink — the white it carries is the " +
			"parser's default, not the theme's choice, and honouring it discards the client's own")
	}

	declared := theme.FontSpec{Color: theme.RGB{R: 10, G: 20, B: 30}, ColorSet: true}
	got, ok := declaredFontInk(declared)
	if !ok {
		t.Fatal("a declared colour must be reported")
	}
	if got.R != 10 || got.G != 20 || got.B != 30 || got.A != 255 {
		t.Errorf("ink = %+v, want the declared RGB at full alpha", got)
	}

	// A theme is allowed to declare white ON PURPOSE, and that must survive — it is
	// indistinguishable from the default by value, which is the whole point of the
	// separate flag.
	white := theme.FontSpec{Color: theme.RGB{R: 255, G: 255, B: 255}, ColorSet: true}
	if _, ok := declaredFontInk(white); !ok {
		t.Error("an explicitly declared white must be honoured, not mistaken for the default")
	}
}
