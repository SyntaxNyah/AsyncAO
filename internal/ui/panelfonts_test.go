package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestPanelFontRowsMatchTheElementList is the pin that makes the persisted keys
// safe. The overrides are stored by AO2 IDENTIFIER, not by index, precisely
// because theme.FontElements is APPEND-ONLY and its indices are load-bearing —
// storing an index would silently re-point every saved override the next time an
// element joins that list. This fails if a row names an identifier that is not
// really an element, or if an element gains no row.
func TestPanelFontRowsMatchTheElementList(t *testing.T) {
	seen := map[string]bool{}
	for i := range panelFontRows {
		row := &panelFontRows[i]
		idx := panelFontElementIndex(row.id)
		if idx < 0 {
			t.Errorf("row %q is not an entry in theme.FontElements — a saved override under that key would never apply", row.id)
			continue
		}
		if themeFontElem(idx) != row.el {
			t.Errorf("row %q maps to element %d but theme.FontElements has it at %d — the override would dress the WRONG panel",
				row.id, row.el, idx)
		}
		if seen[row.id] {
			t.Errorf("row %q appears twice; the second would shadow the first", row.id)
		}
		seen[row.id] = true
		if row.label == "" || row.hint == "" {
			t.Errorf("row %q needs a label and a hint — an unlabelled font box is unusable", row.id)
		}
	}
	for _, id := range theme.FontElements {
		if !seen[id] {
			t.Errorf("element %q has no Settings row, so its font cannot be overridden", id)
		}
	}
}

// TestPxToScalePctRoundTrips: the size a user types has to be the size they get.
// The percent is applied to UIFontSize with integer division when the face is
// opened, so rounding DOWN here reopens one pixel small — the same round-trip
// bug themeFontPct was written to avoid for a theme's point sizes.
func TestPxToScalePctRoundTrips(t *testing.T) {
	for px := config.PanelFontMinPx; px <= config.PanelFontMaxPx; px++ {
		pct := pxToScalePct(px)
		got := UIFontSize * pct / DefaultScalePct
		want := clampInt(px, UIFontSize*themeFontMinPct/DefaultScalePct, UIFontSize*themeFontMaxPct/DefaultScalePct)
		if got != want {
			t.Errorf("%d px -> %d%% -> reopens at %d px, want %d", px, pct, got, want)
		}
	}
	if pxToScalePct(0) != 0 || pxToScalePct(-5) != 0 {
		t.Error("a non-positive size must mean NO override, not a clamped one")
	}
}

// typePanelSize replays a size being typed one character at a time, exactly as
// the field delivers it, and returns what the box would SHOW after each
// keystroke. Driving it character by character is the entire point: the bug this
// pins was invisible to any test that handed over a finished string.
func typePanelSize(t *testing.T, a *App, rowID, typed string) []string {
	t.Helper()
	fieldID := "panelsize-" + rowID
	a.ctx.focusID = fieldID // the box has focus for the whole run, as when typing
	var shown []string
	for i := 1; i <= len(typed); i++ {
		cur := a.d.Prefs.PanelFontFor(rowID)
		a.commitPanelSize(fieldID, rowID, cur, typed[:i])
		shown = append(shown, a.panelSizeText(fieldID, rowID, a.d.Prefs.PanelFontFor(rowID).SizePx))
	}
	return shown
}

// TestPanelFontsApplyWithNoThemeLoaded is the regression pin for the report that
// the per-panel fonts "just don't work".
//
// They were resolved inside the branch that runs only when a theme LOADS, next to
// the theme's own font table. Anyone whose theme failed to load — none configured,
// a folder since moved, the stock default with no tree on disk — had the setting
// save perfectly and do nothing at all, with nothing on screen to say why. The
// user's fonts are the USER's, not the theme's, and must not need one to be
// present.
//
// Driven through applyThemeAsync + pollThemeApply, the real path, because the
// defect was entirely in WHERE the call sat rather than in what it did.
func TestPanelFontsApplyWithNoThemeLoaded(t *testing.T) {
	a := testTabApp(t)
	// Point the client at a theme that cannot possibly load.
	a.d.Prefs.ThemeDir = filepath.Join(t.TempDir(), "no-such-theme-root")
	a.d.Prefs.SetPanelFont("player_list", config.PanelFont{SizePx: 40})
	a.d.Prefs.SetPanelFont("ic_chatlog", config.PanelFont{SizePx: 30})

	awaitThemeApply(t, a)

	if got := a.themeFonts.e[elemPlayerList].pct; got != pxToScalePct(40) {
		t.Errorf("player_list pct = %d, want %d — a user's own font must not need a theme to load",
			got, pxToScalePct(40))
	}
	if got := a.themeFonts.e[elemICChatlog].pct; got != pxToScalePct(30) {
		t.Errorf("ic_chatlog pct = %d, want %d", got, pxToScalePct(30))
	}
	// And it reaches the accessor every draw site actually calls.
	if got := a.elemPct(elemPlayerList, DefaultScalePct); got != pxToScalePct(40) {
		t.Errorf("elemPct = %d, want the override to reach the draw sites", got)
	}
}

// TestPanelFontsCoverEveryPanelRow: every row the Settings screen offers has to
// survive the apply, or the row is a control that does nothing. Cheap insurance
// against a row being added to the table and nowhere else.
func TestPanelFontsCoverEveryPanelRow(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.ThemeDir = filepath.Join(t.TempDir(), "no-such-theme-root")
	for i := range panelFontRows {
		a.d.Prefs.SetPanelFont(panelFontRows[i].id, config.PanelFont{SizePx: 40})
	}
	awaitThemeApply(t, a)
	for i := range panelFontRows {
		row := &panelFontRows[i]
		if got := a.themeFonts.e[row.el].pct; got != pxToScalePct(40) {
			t.Errorf("row %q (element %d) resolved to pct %d, want %d — the Settings row does nothing",
				row.id, row.el, got, pxToScalePct(40))
		}
	}
}

// awaitThemeApply kicks a theme apply and lands it, so a test can assert on what
// the render thread would actually be holding.
func awaitThemeApply(t *testing.T, a *App) {
	t.Helper()
	gen := a.applyThemeAsync()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if res := a.themeRes.Load(); res != nil && res.gen >= gen {
			a.pollThemeApply()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("theme apply did not publish a result")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestPanelFontFamilyReachesTheDrawSite drives a real FAMILY the whole way —
// preference, apply, resolution, landed table, and the accessor the roster's draw
// actually calls — because a report that the setting "does nothing" can only be
// answered by checking every link, and the family path had only ever been tested
// at the resolution step.
func TestPanelFontFamilyReachesTheDrawSite(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.ThemeDir = filepath.Join(t.TempDir(), "no-such-theme-root")

	// Arial resolves from the system font folder on Windows; elsewhere there is
	// no reliable system family, so the family half is skipped rather than faked.
	if runtime.GOOS != "windows" {
		t.Skip("no dependable system font family off Windows")
	}
	a.d.Prefs.SetPanelFont("player_list", config.PanelFont{Family: "Arial", SizePx: 26})
	awaitThemeApply(t, a)

	got := a.themeFonts.e[elemPlayerList]
	if got.pct != pxToScalePct(26) {
		t.Errorf("player_list pct = %d, want %d", got.pct, pxToScalePct(26))
	}
	if got.face == 0 {
		t.Fatalf("player_list resolved NO face for Arial; missing report = %v", a.panelFontMissing)
	}
	if len(a.panelFontMissing) != 0 {
		t.Errorf("Arial was reported missing: %v", a.panelFontMissing)
	}
	// The face bytes have to have been handed to the kit, or the index points at
	// nothing and the draw silently falls back to the client chain.
	if len(a.ctx.themeFaceData) == 0 {
		t.Error("the resolved face was never installed on Ctx — the draw would fall back")
	}
	if got.faceIdx() < 0 || got.faceIdx() >= len(a.ctx.themeFaceData) {
		t.Errorf("face index %d is outside the installed set of %d", got.faceIdx(), len(a.ctx.themeFaceData))
	}
}

// TestPanelSizeAcceptsATwoDigitNumber is the regression pin for the reported
// fault: the size box "defaults to weird numbers, you can't set it correctly".
//
// It clamped on EVERY keystroke and fed the clamped number straight back into the
// box, so typing the "1" of "18" became a 6 — the minimum — before the "8" could
// be typed. Every size below ten was unreachable, and every one above it had to
// be arrived at through a number the box had already rewritten.
func TestPanelSizeAcceptsATwoDigitNumber(t *testing.T) {
	a := testTabApp(t)
	shown := typePanelSize(t, a, "ic_chatlog", "18")

	if shown[0] != "1" {
		t.Errorf("after typing \"1\" the box shows %q — it must not rewrite a half-typed number", shown[0])
	}
	if shown[1] != "18" {
		t.Errorf("after typing \"18\" the box shows %q, want \"18\"", shown[1])
	}
	if got := a.d.Prefs.PanelFontFor("ic_chatlog").SizePx; got != 18 {
		t.Errorf("saved size = %d, want 18", got)
	}
	// A one-character size below the minimum must not have been saved on the way
	// through — that is the clamp coming back in a different disguise.
	if shown[0] == strconv.Itoa(config.PanelFontMinPx) {
		t.Error("the first keystroke was clamped to the minimum")
	}
}

// TestPanelSizeHoldsIncompleteInput: a number on its way somewhere is not a size,
// and must change nothing until it is one.
func TestPanelSizeHoldsIncompleteInput(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetPanelFont("music_list", config.PanelFont{SizePx: 20})

	fieldID := "panelsize-music_list"
	a.ctx.focusID = fieldID
	a.commitPanelSize(fieldID, "music_list", a.d.Prefs.PanelFontFor("music_list"), "1")
	if got := a.d.Prefs.PanelFontFor("music_list").SizePx; got != 20 {
		t.Errorf("a half-typed \"1\" overwrote the saved size with %d", got)
	}
	if got := a.panelSizeText(fieldID, "music_list", 20); got != "1" {
		t.Errorf("the box shows %q while typing, want the raw \"1\"", got)
	}
	// ...and the saved value comes back the moment focus leaves, so a box left
	// holding something unusable discards it rather than persisting it.
	a.ctx.focusID = ""
	if got := a.panelSizeText(fieldID, "music_list", 20); got != "20" {
		t.Errorf("unfocused the box shows %q, want the saved \"20\"", got)
	}
}

// TestPanelSizeClearsAndClamps: emptying the box is how a panel is put back, and
// a genuinely over-large number still lands on the ceiling.
func TestPanelSizeClearsAndClamps(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetPanelFont("area_list", config.PanelFont{SizePx: 20})
	fieldID := "panelsize-area_list"
	a.ctx.focusID = fieldID

	a.commitPanelSize(fieldID, "area_list", a.d.Prefs.PanelFontFor("area_list"), "")
	if got := a.d.Prefs.PanelFontFor("area_list").SizePx; got != 0 {
		t.Errorf("emptying the box left %d, want the override cleared", got)
	}

	a.commitPanelSize(fieldID, "area_list", a.d.Prefs.PanelFontFor("area_list"), "500")
	if got := a.d.Prefs.PanelFontFor("area_list").SizePx; got != config.PanelFontMaxPx {
		t.Errorf("an over-large size saved as %d, want the %d ceiling", got, config.PanelFontMaxPx)
	}

	// Non-numeric text is not a size and must not be saved as one.
	a.d.Prefs.SetPanelFont("area_list", config.PanelFont{SizePx: 20})
	a.commitPanelSize(fieldID, "area_list", a.d.Prefs.PanelFontFor("area_list"), "abc")
	if got := a.d.Prefs.PanelFontFor("area_list").SizePx; got != 20 {
		t.Errorf("garbage text changed the saved size to %d", got)
	}
}

// writeFontFile drops a file with a font extension so ResolveFamilies has
// something real to find. The bytes are not a valid font — nothing in this test
// opens it, only resolves the path.
func writeFontFile(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("not-a-real-font"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestApplyPanelFontsOverridesTheTheme is the feature in one assertion: what the
// user picked beats what the theme asked for, and size and family move
// independently.
func TestApplyPanelFontsOverridesTheTheme(t *testing.T) {
	th, root := writeThemeFonts(t, "Dressy", `ic_chatlog = 10
ic_chatlog_font = ThemeFace
music_list = 8
music_list_font = ThemeFace
`, map[string]string{"fonts/themeface.ttf": "T"})
	userDir := filepath.Join(root, "userfonts")
	writeFontFile(t, userDir, "myface.ttf")

	var res themeApply
	res.buildThemeFontTable(th, nil)
	themeFace := res.fontTable.e[elemICChatlog].face
	if themeFace == 0 {
		t.Fatal("fixture did not resolve the theme's own face")
	}

	res.applyPanelFonts(map[string]config.PanelFont{
		// family AND size
		"ic_chatlog": {Family: "myface", SizePx: 20},
		// size ONLY — the theme's family must survive
		"music_list": {SizePx: 30},
	}, []string{userDir}, nil)

	if got := res.panelPct[elemICChatlog]; got != pxToScalePct(20) {
		t.Errorf("ic_chatlog pct = %d, want %d (20 px)", got, pxToScalePct(20))
	}
	if res.panelFace[elemICChatlog] == 0 {
		t.Error("the user's family did not resolve — their pick must beat the theme's")
	}
	if res.panelFace[elemICChatlog] == themeFace {
		t.Error("the user's family interned to the theme's own face slot")
	}
	if got := res.panelPct[elemMusicList]; got != pxToScalePct(30) {
		t.Errorf("music_list pct = %d, want %d", got, pxToScalePct(30))
	}
	if res.panelFace[elemMusicList] != 0 {
		t.Error("a size-only override recorded a face — size and family must be independent")
	}
	// An element the user said nothing about is untouched.
	if res.panelPct[elemAreaList] != 0 || res.panelFace[elemAreaList] != 0 {
		t.Error("an element with no override was given one")
	}
}

// TestApplyPanelFontsReportsAMissingFamily: a font name that resolves to nothing
// is the single most likely way this confuses someone, so it has to be reported
// rather than silently doing nothing.
func TestApplyPanelFontsReportsAMissingFamily(t *testing.T) {
	th, _ := writeThemeFonts(t, "Plain", "ic_chatlog = 10\n", nil)
	var res themeApply
	res.buildThemeFontTable(th, nil)
	res.applyPanelFonts(map[string]config.PanelFont{
		"ic_chatlog": {Family: "ThisFontDoesNotExistAnywhere"},
	}, nil, nil)
	if len(res.panelFontsMissing) != 1 {
		t.Fatalf("missing report = %v, want one entry naming the element and the family", res.panelFontsMissing)
	}
	if res.panelFace[elemICChatlog] != 0 {
		t.Error("an unresolvable family recorded a face")
	}
}

// landWithPanel builds an apply carrying both a theme face and a user override,
// lands it, and returns the App — the shape every ladder case below needs.
func landWithPanel(t *testing.T, tune func(a *App)) *App {
	t.Helper()
	a := testTabApp(t)
	if tune != nil {
		tune(a)
	}
	res := &themeApply{}
	// The theme dressed ic_chatlog; the user overrode its size AND face, and
	// overrode only the SIZE of music_list.
	res.fontTable.e[elemICChatlog] = themeElemFont{pct: 100, face: 1, bold: true}
	res.fontTable.e[elemMusicList] = themeElemFont{pct: 100, face: 1}
	res.faceData = [][]byte{{0x00}, {0x01}}
	res.panelPct[elemICChatlog], res.panelFace[elemICChatlog] = 250, 2
	res.panelPct[elemMusicList] = 300
	a.landThemeFonts(res)
	return a
}

// TestThemeFontsOffKeepsTheUsersOwnPicks is the ladder's sharpest edge. The
// checkbox says "use the THEME's fonts"; reading it as "use no fonts but the
// client's" would silently disable a setting the user made somewhere else
// entirely, and they would have no way to tell which control had done it.
func TestThemeFontsOffKeepsTheUsersOwnPicks(t *testing.T) {
	a := landWithPanel(t, func(a *App) { a.d.Prefs.SetThemeFonts(false) })
	if got := a.themeFonts.e[elemICChatlog]; got.pct != 250 || got.face != 2 {
		t.Errorf("ic_chatlog = %+v, want the USER's pct 250 and face 2 to survive the theme-fonts opt-out", got)
	}
	if got := a.themeFonts.e[elemMusicList]; got.pct != 300 {
		t.Errorf("music_list pct = %d, want the user's 300", got.pct)
	}
	// ...but the THEME's own contribution is gone, which is what the box means.
	if a.themeFonts.e[elemICChatlog].bold {
		t.Error("the theme's bold survived the opt-out")
	}
	if got := a.themeFonts.e[elemMusicList].face; got != 0 {
		t.Errorf("music_list face = %d, want 0 — the user overrode only its SIZE, so the theme's face must go", got)
	}
}

// TestDyslexiaFontBeatsAPerPanelPick: the accessibility switch is not an
// ordinary preference. Someone who turns it on and still cannot read one panel,
// because of an override they set weeks ago, would be right to call that broken.
// Their SIZES still apply — the switch is about the letterforms.
func TestDyslexiaFontBeatsAPerPanelPick(t *testing.T) {
	a := landWithPanel(t, func(a *App) { a.d.Prefs.SetDyslexiaFont(true) })
	if got := a.themeFonts.e[elemICChatlog].face; got != 0 {
		t.Errorf("ic_chatlog face = %d, want 0 — the dyslexia font must claim every family", got)
	}
	if got := a.themeFonts.e[elemICChatlog].pct; got != 250 {
		t.Errorf("ic_chatlog pct = %d, want the user's 250 — the switch claims families, not sizes", got)
	}
}

// TestPerPanelPickBeatsAGlobalFontPath: a manual global font and a per-panel
// pick are both ordinary preferences, so the more specific one wins. Only the
// accessibility switch above outranks it.
func TestPerPanelPickBeatsAGlobalFontPath(t *testing.T) {
	a := landWithPanel(t, func(a *App) { a.d.Prefs.SetFontPaths(`C:\some\font.ttf`) })
	if got := a.themeFonts.e[elemICChatlog].face; got != 2 {
		t.Errorf("ic_chatlog face = %d, want the user's per-panel face 2 to beat their global font path", got)
	}
	// The element they did NOT name still loses its family to the global override.
	if got := a.themeFonts.e[elemMusicList].face; got != 0 {
		t.Errorf("music_list face = %d, want 0 — no per-panel pick, so the global path claims it", got)
	}
}

// TestMergePanelFontsIsTheLastWord pins the precedence ladder's shape: the
// merge overwrites whatever the theme (or the zeroing branches) left behind, and
// the faces flag is what the dyslexia switch uses to claim families while
// leaving sizes alone.
func TestMergePanelFontsIsTheLastWord(t *testing.T) {
	var res themeApply
	res.panelPct[elemICChatlog] = 250
	res.panelFace[elemICChatlog] = 2

	tbl := themeFontTable{}
	tbl.e[elemICChatlog] = themeElemFont{pct: 100, face: 1}
	res.mergePanelFonts(&tbl, true)
	if tbl.e[elemICChatlog].pct != 250 || tbl.e[elemICChatlog].face != 2 {
		t.Errorf("merge left %+v, want the user's pct 250 and face 2", tbl.e[elemICChatlog])
	}

	// faces=false is the dyslexia case: the accessibility font claims families,
	// the user's SIZES still apply.
	tbl = themeFontTable{}
	tbl.e[elemICChatlog] = themeElemFont{pct: 100, face: 0}
	res.mergePanelFonts(&tbl, false)
	if tbl.e[elemICChatlog].pct != 250 {
		t.Error("the dyslexia branch dropped the user's SIZE — only families are claimed")
	}
	if tbl.e[elemICChatlog].face != 0 {
		t.Error("the dyslexia branch let a per-panel FACE through")
	}

	// An element with no override is untouched by the merge.
	tbl = themeFontTable{}
	tbl.e[elemMusicList] = themeElemFont{pct: 77, face: 3}
	res.mergePanelFonts(&tbl, true)
	if tbl.e[elemMusicList].pct != 77 || tbl.e[elemMusicList].face != 3 {
		t.Errorf("merge disturbed an un-overridden element: %+v", tbl.e[elemMusicList])
	}
}
