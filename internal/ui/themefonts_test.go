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
	}
	for el, id := range want {
		if theme.FontElements[el] != id {
			t.Errorf("element %d = %q, want %q", el, theme.FontElements[el], id)
		}
	}
}

// TestThemeFontPct pins the point-size ↔ percent identity: every scaled face is
// opened at UIFontSize×pct/100, so P points must be exactly P×100/UIFontSize.
func TestThemeFontPct(t *testing.T) {
	for _, tc := range []struct{ points, want int }{
		{12, 100}, {18, 150}, {24, 200}, {8, 66}, {6, 50}, {9, 75}, {13, 108},
	} {
		if got := themeFontPct(tc.points); got != tc.want {
			t.Errorf("themeFontPct(%d) = %d, want %d", tc.points, got, tc.want)
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
	fams := []string{"Fam A", "Fam B", "Fam C", "Fam D", "Fam E", "Fam F", "Fam G"}
	for i, id := range theme.FontElements {
		fonts += id + " = 12\n" + id + "_font = " + fams[i] + "\n"
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
