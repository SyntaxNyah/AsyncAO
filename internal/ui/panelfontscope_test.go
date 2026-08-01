package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// The panel font SCOPE: setting a font for a panel dresses that whole panel —
// its labels, buttons, checkboxes and fields — rather than only the rows someone
// remembered to wire. These pin the two properties the design lives or dies on:
// it is total, and it is invisible when unset.

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
	a.themeFonts.e[elemMusicList] = themeElemFont{pct: 150, face: 1}
	if got := a.elemChromeFont(elemMusicList, DefaultScalePct); got != nil {
		t.Errorf("a THEME-dressed element armed the panel scope (%v) — the scope is for the user's own pick only", got)
	}
}

// TestPushPanelFontRestoresAndNeverSwapsTheChromeFont pins the lifetime rule.
// c.font is the identity every font close-guard compares against (buildSet's
// chromeShare, dropThemeElemSets, Destroy), so arming a panel must leave it
// untouched — assigning over it would make those guards protect the wrong
// pointer and Close a face still in use.
func TestPushPanelFontRestoresAndNeverSwapsTheChromeFont(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	chrome := c.font

	// A nil face writes nothing at all.
	prev := c.pushPanelFont(nil)
	if c.panelFont != nil || c.chromeFace() != chrome {
		t.Error("pushing nil armed something")
	}
	c.popPanelFont(prev)

	// A real face arms, and restores exactly.
	face := c.LogFont(DefaultScalePct)
	if face == nil {
		t.Skip("no font available headlessly")
	}
	prev = c.pushPanelFont(face)
	if c.font != chrome {
		t.Fatal("pushing a panel font MUTATED c.font — the close-guards now protect the wrong pointer")
	}
	if c.chromeFace() != face {
		t.Error("the armed face did not reach chromeFace()")
	}
	c.popPanelFont(prev)
	if c.panelFont != nil || c.chromeFace() != chrome {
		t.Error("pop did not restore the chrome face")
	}

	// Nesting: an inner panel restores the OUTER one, not nil. Panels do not nest
	// today, but a modal or an inline sub-panel would, and a pop-to-nil would
	// silently undress the rest of the outer panel.
	outer := c.pushPanelFont(face)
	inner := c.pushPanelFont(c.font) // pushing the chrome face is a deliberate no-op
	if c.chromeFace() != face {
		t.Error("arming the chrome face inside a panel cleared the panel's own")
	}
	c.popPanelFont(inner)
	if c.chromeFace() != face {
		t.Error("the inner pop undressed the outer panel")
	}
	c.popPanelFont(outer)
	if c.panelFont != nil {
		t.Error("the outer pop left the scope armed")
	}
}

// TestPanelWidthMemoIsKeyedByFace: widthCache is keyed by STRING alone and holds
// the chrome face's advances. A panel face writing into it would hand the next
// chrome label a width measured in a different font — wrong LAYOUT, not just
// wrong drawing.
func TestPanelWidthMemoIsKeyedByFace(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	face := c.LogFont(250) // a deliberately different size from the chrome face
	if face == nil || face == c.font {
		t.Skip("no distinct font available headlessly")
	}
	const s = "measure me"
	chromeW := c.TextWidth(s)
	before := len(c.widthCache)

	prev := c.pushPanelFont(face)
	panelW := c.TextWidth(s)
	c.popPanelFont(prev)

	if len(c.widthCache) != before {
		t.Error("a panel-face measurement was written into the chrome width cache")
	}
	if panelW == 0 {
		t.Fatal("the panel face measured nothing")
	}
	if got := c.TextWidth(s); got != chromeW {
		t.Errorf("chrome width changed to %d after a panel measured (was %d) — the caches crossed", got, chromeW)
	}
}

// TestPurgeTextCacheClearsThePanelWidthMemo is the graft that stops a
// use-after-free from becoming a wrong-number bug.
//
// The memo is keyed by FONT POINTER. SetThemeFaces calls dropThemeElemSets, which
// Closes every per-element face — exactly the faces a panel bracket arms — and it
// does NOT clear widthCache. Clearing in purgeTextCache is what catches it,
// because that is the one choke point every face-invalidating path reaches.
func TestPurgeTextCacheClearsThePanelWidthMemo(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	face := c.LogFont(250)
	if face == nil || face == c.font {
		t.Skip("no distinct font available headlessly")
	}
	prev := c.pushPanelFont(face)
	c.TextWidth("something")
	c.popPanelFont(prev)
	if len(c.panelWidth) == 0 {
		t.Fatal("the panel width memo stayed empty")
	}
	c.purgeTextCache()
	if len(c.panelWidth) != 0 {
		t.Error("purgeTextCache left the font-keyed memo populated — it would hold dangling pointers after a theme swap")
	}
}

// TestBeginFrameDisarmsThePanelScope: every bracket restores with defer, so this
// should never fire in practice. It exists because a panic unwinding a bracket
// must not leave the entire client dressed in one panel's font for the rest of
// the session — the same reasoning the overlay fence carries.
func TestBeginFrameDisarmsThePanelScope(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	face := c.LogFont(DefaultScalePct)
	if face == nil {
		t.Skip("no font available headlessly")
	}
	c.panelFont = face // simulate a bracket that never popped
	c.BeginFrame(0)
	if c.panelFont != nil {
		t.Error("BeginFrame left a stale panel font armed")
	}
}

// TestEveryPanelBracketsItsFont is the TOTALITY gate, and it is a source census
// because nothing at runtime can see it.
//
// The failure it prevents is precisely the one that was reported: a panel gains a
// font row, its rows get wired, and its headers and buttons quietly do not — so
// the setting looks broken. A panel with an element must bracket, and the percent
// it brackets with must be the SAME one its rows use, or the face is opened at
// one scale and the rows drawn at another.
func TestEveryPanelBracketsItsFont(t *testing.T) {
	for _, tc := range []struct {
		file, fn, elem, pct string
	}{
		{"playerlist.go", "drawPlayerList", "elemPlayerList", "a.playerPct"},
		{"notebook.go", "drawNotesTab", "elemNotes", "a.logPct"},
		{"friendstab.go", "drawFriendsTab", "elemFriends", "DefaultScalePct"},
		{"screens.go", "drawICLogList", "elemICChatlog", "a.logPct"},
		{"screens.go", "drawOOCLogList", "elemServerChatlog", "a.oocPct"},
		{"screens.go", "drawAreaList", "elemAreaList", "a.areaPct"},
		{"screens.go", "drawMusicList", "elemMusicList", "a.musicPct"},
	} {
		t.Run(tc.fn, func(t *testing.T) {
			src, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			body := funcBodyOf(string(src), "func (a *App) "+tc.fn+"(")
			if body == "" {
				t.Fatalf("could not find %s in %s — the gate went blind", tc.fn, tc.file)
			}
			want := "pushPanelFont(a.elemChromeFont(" + tc.elem + ", " + tc.pct + ")"
			if !strings.Contains(body, want) {
				t.Errorf("%s does not bracket its font with %q.\n"+
					"Without it the panel's labels and buttons keep the client font while its rows change — "+
					"which is exactly how this feature was reported broken.", tc.fn, want)
			}
			if !strings.Contains(body, "defer") || !strings.Contains(body, "popPanelFont(") {
				t.Errorf("%s arms a panel font without a deferred pop — an early return would leak it", tc.fn)
			}
		})
	}
}

// funcBodyOf returns the source of the function whose declaration starts with
// prefix, brace-balanced from its opening brace.
func funcBodyOf(src, prefix string) string {
	i := strings.Index(src, prefix)
	if i < 0 {
		return ""
	}
	open := strings.IndexByte(src[i:], '{')
	if open < 0 {
		return ""
	}
	start := i + open
	depth := 0
	for j := start; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : j+1]
			}
		}
	}
	return ""
}

// TestPanelScopeArmsUnderTheDyslexiaSwitch is the second graft. Under that
// switch landThemeFonts merges with faces=false, so a user who picked only a
// FAMILY for a panel gets no face of their own — but they still asked for that
// panel to be theirs, and without the scope it renders half in the accessibility
// font and half in the chrome one.
func TestPanelScopeArmsUnderTheDyslexiaSwitch(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetDyslexiaFont(true)
	res := &themeApply{}
	res.panelFace[elemPlayerList] = 1 // a FAMILY only, no size
	res.faceData = [][]byte{{0x00}}
	a.landThemeFonts(res)
	if !a.themeFonts.e[elemPlayerList].userFont {
		t.Error("a family-only pick lost its panel scope under the dyslexia switch — the panel would render in two fonts")
	}
	// The accessibility font still claims the FACE; only the scope survives.
	if a.themeFonts.e[elemPlayerList].face != 0 {
		t.Error("the dyslexia switch let a per-panel face through")
	}
}

// TestPanelScopeSurvivesThemeFontsOff: the "use the theme's fonts" checkbox is
// about the THEME. Turning it off must not silently disable a font the user set
// on a different screen.
func TestPanelScopeSurvivesThemeFontsOff(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetThemeFonts(false)
	res := &themeApply{}
	res.panelPct[elemNotes] = 200
	a.landThemeFonts(res)
	if !a.themeFonts.e[elemNotes].userFont {
		t.Error("the theme-fonts opt-out cleared the user's own panel scope")
	}
	if got := a.themeFonts.e[elemNotes].pct; got != 200 {
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
