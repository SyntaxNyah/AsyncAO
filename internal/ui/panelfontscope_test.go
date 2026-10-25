package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// The panel font SCOPE: setting a font for a panel dresses that whole panel —
// its labels, buttons, checkboxes and fields — rather than only the rows someone
// remembered to wire. These pin the two properties the design lives or dies on:

// TestChromeFaceIsTheClientFontWhenUnset is requirement one, and the reason the
// scope is an OVERRIDE rather than a swap of c.font. With nothing armed, every
// widget must receive the identical chrome POINTER it always did — identical
// pointer in means an identical textCache key, an identical atlas slot and
// identical pixels out.
func TestChromeFaceIsTheClientFontWhenUnset(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	if c.panelFont != nil {
		t.Fatal("a fresh Ctx already has a panel font armed")
	}
	if c.chromeFace() != c.font {
		t.Error("chromeFace() differs from c.font with nothing armed — every label would miss its cache entry")
	}
	// An element the USER never touched must not arm anything, even when a THEME
	// dressed it. A theme that merely sizes its music list must not resize the
	// jukebox's buttons for someone who set nothing.
	a.skin.themeFonts.e[elemMusicList] = themeElemFont{pct: 150, face: 1}
	if got := a.elemChromeFont(elemMusicList, DefaultScalePct); got != nil {
		t.Errorf("a THEME-dressed element armed the panel scope (%v) — the scope is for the user's own pick only", got)
	}
}

// TestPanelScopeArmsUnderTheDyslexiaSwitch is the second graft. Under that
// switch landThemeFonts merges with faces=false, so a user who picked only a
// FAMILY for a panel gets no face of their own — but they still asked for that
// panel to be theirs, and without the scope it renders half in the accessibility
// font and half in the chrome one.
func TestPanelScopeArmsUnderTheDyslexiaSwitch(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			DyslexiaFont =

			true

	})
	res := &themeApply{}
	res.panelFace[elemPlayerList] = 1 // a FAMILY only, no size
	res.faceData = [][]byte{{0x00}}
	a.landThemeFonts(res)
	if !a.skin.themeFonts.e[elemPlayerList].userFont {
		t.Error("a family-only pick lost its panel scope under the dyslexia switch — the panel would render in two fonts")
	}
	// The accessibility font still claims the FACE; only the scope survives.
	if a.skin.themeFonts.e[elemPlayerList].face != 0 {
		t.Error("the dyslexia switch let a per-panel face through")
	}
}

// TestPanelScopeSurvivesThemeFontsOff: the "use the theme's fonts" checkbox is
// about the THEME. Turning it off must not silently disable a font the user set
// on a different screen.
func TestPanelScopeSurvivesThemeFontsOff(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.Update(func(sn *config.Settings) { sn.ThemeFonts = false })
	res := &themeApply{}
	res.panelPct[elemNotes] = 200
	a.landThemeFonts(res)
	if !a.skin.themeFonts.e[elemNotes].userFont {
		t.Error("the theme-fonts opt-out cleared the user's own panel scope")
	}
	if got := a.skin.themeFonts.e[elemNotes].pct; got != 200 {
		t.Errorf("the user's SIZE was cleared by the opt-out: pct = %d, want 200", got)
	}
	// The face itself only exists where a real font chain does; headlessly the
	// element set can come back empty, and that is not what this test is about.
	if a.ctx.font != nil {
		if got := a.elemChromeFont(elemNotes, DefaultScalePct); got == nil {
			t.Error("no face armed for a panel the user sized")
		}
	}
}

// TestPanelFontRowsAllHaveAPanel: a Settings row for an element nothing brackets
// is a control that half-works — the rows change, the panel does not. Ties the
// user-facing list to the bracket census above.
func TestPanelFontRowsAllHaveAPanel(t *testing.T) {
	// The chatbox elements are deliberately absent: showname and message are drawn
	// on the chatbox skin by the message raster, not by a chrome-faced panel, so
	// they have a row but no bracket.
	scoped := map[themeFontElem]bool{
		elemICChatlog: true, elemServerChatlog: true, elemMusicList: true,
		elemAreaList: true, elemPlayerList: true, elemNotes: true, elemFriends: true,
	}
	unscoped := map[themeFontElem]string{
		elemShowname:  "drawn on the chatbox skin by the message raster",
		elemMessage:   "drawn on the chatbox skin by the message raster",
		elemMusicName: "one line inside the music panel, which brackets already",
		elemDebugLog:  "drawn by drawThemedDebugLog, which has no chrome widgets",
	}
	for i := range panelFontRows {
		el := panelFontRows[i].el
		if scoped[el] {
			continue
		}
		if _, ok := unscoped[el]; !ok {
			t.Errorf("row %q has no panel bracket and no recorded reason — its buttons and labels would not follow the setting",
				panelFontRows[i].id)
		}
	}
	_ = config.PanelFontMinPx
}
