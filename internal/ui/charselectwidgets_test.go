package ui

import (
	"image"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Char select's widgets and its name column (#20, C1/C2/C3). These tests pin the
// three things the screen would otherwise get quietly wrong: the name column that six
// corpus themes NEED (skip it and their art shows three orphan slot frames), the
// all-or-nothing rule that decides whose control row a frame is drawing, and the
// behaviour at BOTH ends of the window range — a theme far bigger than the 640x480
// floor and a theme far smaller than it.
//
// stageCharSelect / stockCharSelectDesign live in charselectlayout_test.go.

// nativeCharSelect stages a design table at Native fit and returns the canvas plus the
// grid plan for a window, with no app-chrome band in the way.
func nativeCharSelect(t *testing.T, d charSelectDesign, w, h int32) (*App, *charSelectLayoutCache, charGridPlan) {
	t.Helper()
	a := stageCharSelect(t, d)
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	a.charSelLay.valid = false
	lay := a.charSelectLayoutIn(w, h, 0)
	return a, lay, a.charSelectGridPlan(w, h, 0, lay)
}

// TestCharNameColumnCoversTheOrphanSlotFrames is C1's whole reason to exist. Six
// corpus themes (P5Theme, HDF-Standard, MOS 2xDark, MOSDefaultDark2.8,
// MOSDefaultLight2.8, DR Theme) paint TEN slot columns from x=25 but restrict
// char_buttons to seven from x=226; char_list covers exactly the three the grid never
// uses. The column must therefore resolve, sit where the art's orphan frames are, and
// never touch the grid.
func TestCharNameColumnCoversTheOrphanSlotFrames(t *testing.T) {
	const w, h int32 = 1920, 1080
	a, lay, g := nativeCharSelect(t, stockCharSelectDesign(), w, h)
	if !g.themed {
		t.Fatal("fixture did not produce a themed grid")
	}
	col, ok := a.charSelectNameListRect(lay)
	if !ok {
		t.Fatal("no name column: the six themes that need one would show orphan slot frames")
	}
	// The stock table's char_list is 25,36,190,596 — design x 25..215 — which is
	// where those themes paint their first three columns (they end at 218).
	if want := lay.area.X + 25; col.X != want {
		t.Errorf("column x=%d, want the design origin %d", col.X, want)
	}
	if want := lay.area.Y + 36; col.Y != want {
		t.Errorf("column y=%d, want the design origin %d", col.Y, want)
	}
	if col.W != 190 || col.H != 596 {
		t.Errorf("column %dx%d, want the design 190x596 at 1:1", col.W, col.H)
	}
	buttons, ok := lay.rect(charSelectButtonsKey)
	if !ok {
		t.Fatal("char_buttons vanished")
	}
	if ov := intersectRect(col, buttons); ov.W > 0 && ov.H > 0 {
		t.Errorf("the name column overlaps the grid by %dx%d", ov.W, ov.H)
	}
	if col.X+col.W > buttons.X {
		t.Errorf("column right edge %d runs into char_buttons at %d", col.X+col.W, buttons.X)
	}
}

// TestCharNameColumnSkippedWhenItWouldSitOnTheGrid is the mirror case, and the reason
// the column is not simply drawn whenever the table resolves one. AAI declares no
// char_list and hands the whole area to char_buttons = 25,36,663,596, so the INHERITED
// default column lands under its grid. Stock AO2 draws both (ui_char_list is a sibling
// created before ui_char_buttons, so the tree shows through the 7 px gutters); that is
// a stacking artefact, not a design. The roster switch must survive the skip.
func TestCharNameColumnSkippedWhenItWouldSitOnTheGrid(t *testing.T) {
	const w, h int32 = 1920, 1080
	aai := stockCharSelectDesign()
	aai.r[charSelectButtonsKey] = theme.Rect{X: 25, Y: 36, W: 663, H: 596} // AAI, verbatim
	a, lay, g := nativeCharSelect(t, aai, w, h)
	if !g.themed {
		t.Fatal("fixture did not produce a themed grid")
	}
	if !a.charSelectWidgetsThemed(lay, g) {
		t.Fatal("AAI-shaped themes must still get the theme's own control row")
	}
	if col, ok := a.charSelectNameListRect(lay); ok {
		t.Errorf("drew a name column at %+v, on top of char_buttons", col)
	}
	// AO2's own ten columns, from the art: ((663-60)/(7+60))+1 = 10.
	if g.cols != 10 {
		t.Errorf("AAI grid = %d columns, want the art's 10", g.cols)
	}
	strip, ok := a.charSelectTabStripRect(lay)
	if !ok {
		t.Fatal("no fallback home for the Characters/Wardrobe switch — the Wardrobe tab is unreachable")
	}
	if strip.W < 1 || strip.H < 1 {
		t.Errorf("degenerate tab strip %+v", strip)
	}
	if strip.X+strip.W > lay.area.X+lay.area.W || strip.Y+strip.H > lay.area.Y+lay.area.H {
		t.Errorf("tab strip %+v escapes the canvas %+v", strip, lay.area)
	}
}

// TestCharNameColumnHonoursAThemeThatSaysNo covers the two real ways a theme declines
// the column outright: CCWidescreen ships `char_list = 0, 0, 0, 0` and Mobile parks it
// at x=99999. Both must produce no column and no complaint — and, again, must keep the
// roster switch reachable.
func TestCharNameColumnHonoursAThemeThatSaysNo(t *testing.T) {
	const w, h int32 = 1920, 1080
	for name, r := range map[string]theme.Rect{
		"CCWidescreen (0x0)": {X: 0, Y: 0, W: 0, H: 0},
		"Mobile (x=99999)":   {X: 99999, Y: 36, W: 190, H: 596},
	} {
		d := stockCharSelectDesign()
		d.r[charSelectListKey] = r
		a, lay, g := nativeCharSelect(t, d, w, h)
		if !a.charSelectWidgetsThemed(lay, g) {
			t.Fatalf("%s: lost the themed control row over an absent name column", name)
		}
		if col, ok := a.charSelectNameListRect(lay); ok {
			t.Errorf("%s: drew a column at %+v the theme asked to hide", name, col)
		}
		if _, ok := a.charSelectTabStripRect(lay); !ok {
			t.Errorf("%s: no fallback home for the roster switch", name)
		}
	}
}

// TestCharSelectThemedRowNeedsItsThreeEssentials pins the all-or-nothing rule. Drop any
// one of search / spectate / leave and the screen must keep AsyncAO's own header row
// rather than ship half a themed one — a themed search box with a floating client
// Spectate button is worse than either whole answer.
func TestCharSelectThemedRowNeedsItsThreeEssentials(t *testing.T) {
	const w, h int32 = 1920, 1080
	if a, lay, g := nativeCharSelect(t, stockCharSelectDesign(), w, h); !a.charSelectWidgetsThemed(lay, g) {
		t.Fatal("the complete stock table did not produce a themed control row")
	}
	for _, key := range charSelectEssentialKeys {
		d := stockCharSelectDesign()
		d.r[key] = theme.Rect{X: 11037, Y: 11037, W: 120, H: 22} // AO2's hide convention
		a, lay, g := nativeCharSelect(t, d, w, h)
		if a.charSelectWidgetsThemed(lay, g) {
			t.Errorf("hiding %s still produced a themed control row", key)
		}
	}
	// A classic (non-themed) grid never carries the themed row either: there is no
	// canvas layout for it to be consistent with.
	a := testTabApp(t)
	lay := a.charSelectLayout(w, h)
	if a.charSelectWidgetsThemed(lay, a.charSelectGridPlan(w, h, 0, lay)) {
		t.Error("a themeless install claimed the themed control row")
	}
}

// TestCharSelectThemedRowNeedsItsEssentialsONSCREEN is the half the first version of
// the gate was missing, and it shipped an unusable screen.
//
// Char-select rects are deliberately never clamped into the canvas — char_buttons
// overhangs by 30 px BY DESIGN and shifting it back would walk every column off the
// artwork — so "resolved" says a rect is big enough, never that it is reachable.
// ThemeFitCrop scales UP on purpose: at 1920x1080 with the shipped chrome band the
// stock table scales by 2.69 and the canvas becomes {0,-336,1920,1796}, putting
// char_search at y=-318, back_to_lobby at y=-323 and spectator at y=1385 on a 1080 px
// window. All three resolved, so the themed row was taken and the client's own header
// was suppressed: no search, no Spectate, no way out. The draw pushes lay.clip, so
// they were painted nowhere and hovering() rejected them.
func TestCharSelectThemedRowNeedsItsEssentialsONSCREEN(t *testing.T) {
	const w, h int32 = 1920, 1080
	a := stageCharSelect(t, stockCharSelectDesign())
	a.tabs = []*courtTab{{}} // the shipped chrome band
	a.activeTab = 0
	a.d.Prefs.SetThemeFit(config.ThemeFitCrop)
	a.charSelLay.valid = false
	lay := a.charSelectLayout(w, h)
	if !lay.valid {
		t.Fatal("crop canvas did not build")
	}
	if lay.area.Y >= 0 && lay.area.Y+lay.area.H <= h {
		t.Fatalf("fixture failed to overflow the window: canvas %+v in %dx%d", lay.area, w, h)
	}
	offScreen := 0
	for _, key := range charSelectEssentialKeys {
		r, ok := lay.rect(key)
		if !ok {
			t.Fatalf("%s did not even resolve — this test's premise is gone", key)
		}
		if vis := intersectRect(r, lay.clip); vis.W < minThemedElementPx || vis.H < minThemedElementPx {
			offScreen++
		}
	}
	if offScreen == 0 {
		t.Fatal("crop kept every essential control on screen — the regression cannot be exercised")
	}
	g := a.charSelectGridPlan(w, h, a.topChromeH(), lay)
	if a.charSelectWidgetsThemed(lay, g) {
		t.Errorf("%d of %d essential controls are off the paint region, but the screen still "+
			"dropped its own header row", offScreen, len(charSelectEssentialKeys))
	}
	// Native, the shipped default, keeps everything on screen and keeps the themed row.
	a.d.Prefs.SetThemeFit(config.ThemeFitNative)
	a.charSelLay.valid = false
	nat := a.charSelectLayout(w, h)
	if !a.charSelectWidgetsThemed(nat, a.charSelectGridPlan(w, h, a.topChromeH(), nat)) {
		t.Error("the on-screen test cost Native its themed control row")
	}
}

// TestCharSelectTinyCellFallsBackToTheClassicScreen pins the cell floor against the
// one corpus theme that exercises the downscale path.
//
// `microsoft surface 4k webao` declares a canvas far larger than any real window, so
// at config.MinWindowW/H it scales to about a quarter. With the floor at 8 px the
// themed grid was KEPT there on a 14 px cell: 64 px icon textures crushed to 14 px,
// star and caption both suppressed, and — because the widget gate correctly fell back
// — AsyncAO's own header row sitting over a themed grid. The pre-#20 screen gave that
// user 64 px captioned cells, so the themed path has to stand down.
func TestCharSelectTinyCellFallsBackToTheClassicScreen(t *testing.T) {
	const w, h = config.MinWindowW, config.MinWindowH
	big := stockCharSelectDesign()
	big.r[charSelectCanvasKey] = theme.Rect{W: 2592, H: 1719} // microsoft surface 4k webao
	_, lay, g := nativeCharSelect(t, big, w, h)
	if !lay.valid {
		t.Fatal("the 4K canvas did not build")
	}
	if cell := int32(float64(charSelectButtonPx) * lay.cellScale()); cell >= charCellMinPx {
		t.Fatalf("fixture cell is %d px, at or above the %d px floor — it proves nothing", cell, charCellMinPx)
	}
	if g.themed {
		t.Errorf("a %d px cell kept the themed grid; the classic screen draws %d px captioned cells",
			int32(float64(charSelectButtonPx)*lay.cellScale()), iconCell)
	}
	if g.cell != iconCell || !g.caption {
		t.Errorf("the fallback is not the classic plan (cell %d, caption %v)", g.cell, g.caption)
	}
	// The floor has to be a size a face is actually recognisable at, which is why it
	// is pinned to the kit's own smallest character-icon draw site rather than to a
	// number somebody liked.
	if charCellMinPx < msgBubbleIconPx {
		t.Errorf("charCellMinPx = %d, below the kit's smallest character-icon draw (%d px)",
			charCellMinPx, msgBubbleIconPx)
	}
	// And a theme that fits the floor keeps its grid: the stock table at 640x480 is
	// still comfortably above it.
	_, stockLay, stockG := nativeCharSelect(t, stockCharSelectDesign(), w, h)
	if !stockG.themed {
		t.Errorf("the stock table lost its themed grid at the window floor (cell scale %v)", stockLay.cellScale())
	}
}

// TestCharListTabsShowWhichRosterIsActive pins the roster switch's selected state.
//
// It was invisible: the active chip was Fill(chip, ColAccent) followed by
// Button(chip, ...) on the IDENTICAL rect, and Button fills again with an opaque
// ColPanel/ColPanelHi. Both chips then took Button's ColAccent border, so Characters
// and Wardrobe rendered pixel-for-pixel the same whichever one you were on. The test
// captures the two chips and requires them to differ.
func TestCharListTabsShowWhichRosterIsActive(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	a.uiScalePct = 100
	strip := sdl.Rect{X: 10, Y: 10, W: charSelectTabsW(ctx), H: 22}
	// Park the pointer far away: hover must not be what makes the two differ.
	ctx.mouseX, ctx.mouseY = -100, -100
	shot := func() []byte {
		ctx.Fill(sdl.Rect{X: 0, Y: 0, W: 300, H: 60}, ColBackground)
		a.drawCharListTabs(strip)
		return readRegion(t, ren, strip)
	}
	a.charTab = charTabServer
	onChars := shot()
	a.charTab = charTabWardrobe
	onWardrobe := shot()
	if sameRegion(onChars, onWardrobe) {
		t.Error("the Characters and Wardrobe chips render identically — nothing tells the user which roster is live")
	}
	// Both chips must stay clickable when the column is too narrow for their natural
	// widths; the old code clipped the second one to whatever was left, which could be
	// a pixel — an unreachable Wardrobe tab.
	a.charTab = charTabServer
	narrow := sdl.Rect{X: 0, Y: 0, W: charSelectTabsW(ctx) / 2, H: 22}
	ctx.mouseX, ctx.mouseY = narrow.X+narrow.W-4, narrow.Y+narrow.H/2 // over the LAST chip
	ctx.clicked = true
	a.drawCharListTabs(narrow)
	ctx.clicked = false
	if a.charTab != charTabWardrobe {
		t.Error("the Wardrobe chip is unreachable in a column too narrow for both labels")
	}
}

// TestCharSelectControlsAtTheWindowFloor is C3, both ends.
//
// UP: eleven corpus themes are smaller than config.MinWindowW/H and letterbox
// permanently. That is correct, and their controls must stay at full size and stay
// usable — nothing may be dropped just because the window is at its floor.
//
// DOWN: only `microsoft surface 4k webao` exercises the downscale path. Its 22 px
// design controls land on 5 px at 640x480, below the layout's own minThemedElementPx,
// so the themed row must FALL BACK to AsyncAO's header rather than leave the user
// clicking invisible slivers.
func TestCharSelectControlsAtTheWindowFloor(t *testing.T) {
	const w, h = config.MinWindowW, config.MinWindowH

	// --- the small end: a canvas that fits inside the floor with room to spare, laid
	// out the way a genuinely small theme lays itself out (everything inside its own
	// design box). Mobile's 391 px width, at a height that clears 480.
	small := charSelectDesign{spacing: [2]int{4, 4}, r: map[string]theme.Rect{
		charSelectCanvasKey:    {W: 391, H: 400},
		charSelectSearchKey:    {X: 8, Y: 6, W: 110, H: 20},
		charSelectTakenKey:     {X: 124, Y: 6, W: 70, H: 20},
		charSelectBackKey:      {X: 200, Y: 6, W: 80, H: 20},
		charSelectSpectatorKey: {X: 288, Y: 6, W: 90, H: 20},
		charSelectLeftKey:      {X: 8, Y: 374, W: 40, H: 18},
		charSelectRightKey:     {X: 52, Y: 374, W: 40, H: 18},
		charSelectListKey:      {X: 8, Y: 32, W: 110, H: 336},
		charSelectButtonsKey:   {X: 124, Y: 32, W: 260, H: 336},
	}}
	a, lay, g := nativeCharSelect(t, small, w, h)
	if lay.scaleX != 1 || lay.scaleY != 1 {
		t.Fatalf("a canvas smaller than the window must be 1:1, got (%v,%v)", lay.scaleX, lay.scaleY)
	}
	if lay.area.X <= 0 && lay.area.Y <= 0 {
		t.Error("a letterboxed canvas is not centred: there are no bars around it")
	}
	if !a.charSelectWidgetsThemed(lay, g) {
		t.Fatal("a letterboxed small theme lost its controls at the window floor")
	}
	for _, key := range charSelectEssentialKeys {
		r, ok := lay.rect(key)
		if !ok {
			t.Fatalf("%s vanished on a 1:1 canvas", key)
		}
		if def := small.r[key]; int32(def.H) != r.H || int32(def.W) != r.W {
			t.Errorf("%s = %dx%d at 1:1, want the design %dx%d — a letterboxed theme must not shrink",
				key, r.W, r.H, def.W, def.H)
		}
	}
	if _, ok := a.charSelectNameListRect(lay); !ok {
		t.Error("a small theme's name column vanished at the window floor")
	}

	// --- the big end: the only corpus theme that downscales at the floor.
	big := stockCharSelectDesign()
	big.r[charSelectCanvasKey] = theme.Rect{W: 2592, H: 1719} // microsoft surface 4k webao
	a4k, lay4k, g4k := nativeCharSelect(t, big, w, h)
	if lay4k.scaleX >= 1 {
		t.Fatalf("the 4K fixture failed to downscale: scale %v", lay4k.scaleX)
	}
	if a4k.charSelectWidgetsThemed(lay4k, g4k) {
		t.Error("a 4K theme at the 640x480 floor kept a themed control row of 5 px slivers")
	}
	// ...and the same theme in a window it fits gets its controls back.
	_, layBig, gBig := nativeCharSelect(t, big, 2592, 1719)
	if !a4k.charSelectWidgetsThemed(layBig, gBig) {
		t.Error("the 4K theme lost its controls in a window sized for it")
	}
}

// TestCharSelectFilterIsAO2sTakenCheckbox pins filter_character_list
// (AO2-Client charselect.cpp:347-372): the taken checkbox first (default CHECKED =
// show everyone), then the search text. One predicate, because the grid and the name
// column index into the same filtered order.
func TestCharSelectFilterIsAO2sTakenCheckbox(t *testing.T) {
	rows := charListRows{
		n:     4,
		chars: []courtroom.CharacterSlot{{Name: "Phoenix"}, {Name: "Edgeworth", Taken: true}, {Name: "Franziska"}, {Name: "Godot", Taken: true}},
		lower: []string{"phoenix", "edgeworth", "franziska", "godot"},
	}
	if got := rows.matches("", true); got != 4 {
		t.Errorf("unfiltered = %d, want every slot (4)", got)
	}
	if got := rows.matches("", false); got != 2 {
		t.Errorf("taken hidden = %d, want 2", got)
	}
	if rows.hidden(1, "", true) || !rows.hidden(1, "", false) {
		t.Error("the taken arm does not follow the checkbox")
	}
	if got := rows.matches("z", true); got != 1 { // Franziska
		t.Errorf("search 'z' = %d, want 1", got)
	}
	if got := rows.matches("o", false); got != 1 { // Phoenix; Godot is taken
		t.Errorf("search 'o' with taken hidden = %d, want 1", got)
	}
	// matches() must agree with hidden() exactly, or the scrollbar and the grid
	// disagree about how much content there is.
	for _, q := range []string{"", "o", "zzz"} {
		for _, show := range []bool{true, false} {
			want := int32(0)
			for i := 0; i < rows.n; i++ {
				if !rows.hidden(i, q, show) {
					want++
				}
			}
			if got := rows.matches(q, show); got != want {
				t.Errorf("matches(%q, %v) = %d, hidden() says %d", q, show, got, want)
			}
		}
	}
	// The wardrobe has no taken slots, so the taken arm can never hide one of its rows.
	ward := charListRows{n: 2, names: []string{"Blue Badger", "Steel Samurai"}, lower: []string{"blue badger", "steel samurai"}}
	if ward.taken(0) || ward.matches("", false) != 2 {
		t.Error("the wardrobe lost rows to a filter that cannot apply to it")
	}
}

// TestCharSelectTakenFilterDefaultsToShowing pins the seed. AO2 sets the checkbox
// CHECKED on every join (charselect.cpp:99) and never persists it; the zero value
// would silently hide every taken character on a fresh session.
func TestCharSelectTakenFilterDefaultsToShowing(t *testing.T) {
	a := testTabApp(t)
	if !a.charShowTaken {
		t.Error("a fresh session hides taken characters — AO2 seeds char_taken checked")
	}
	if a.charListSel != -1 {
		t.Errorf("a fresh session starts with name-column selection %d, want none (-1)", a.charListSel)
	}
	a.charShowTaken, a.charListSel = false, 7
	a.resetSessionState()
	if !a.charShowTaken || a.charListSel != -1 {
		t.Errorf("reconnecting kept the old filter/selection: showTaken=%v sel=%d", a.charShowTaken, a.charListSel)
	}
}

// TestCharListSelectionDropsOnTabSwitch: the two tabs index different slices, so a
// selection carried across would point at a different character — or past the end of a
// shorter list.
func TestCharListSelectionDropsOnTabSwitch(t *testing.T) {
	a := testTabApp(t)
	a.charListSel, a.charListScroll = 12, 340
	a.selectCharTab(charTabServer) // no-op: already there
	if a.charListSel != 12 || a.charListScroll != 340 {
		t.Error("re-selecting the current tab reset the column")
	}
	a.selectCharTab(charTabWardrobe)
	if a.charTab != charTabWardrobe {
		t.Fatalf("tab = %d, want the wardrobe", a.charTab)
	}
	if a.charListSel != -1 || a.charListScroll != 0 {
		t.Errorf("switching tabs kept sel=%d scroll=%d", a.charListSel, a.charListScroll)
	}
}

// TestRevealRowInGridMovesTheLeastPossible pins the list→grid half of the shared
// selection. Clicking down a column of names must not jog the grid on every click, so
// an already-visible cell moves nothing; a cell above or below scrolls exactly far
// enough to bring it in.
func TestRevealRowInGridMovesTheLeastPossible(t *testing.T) {
	g := charGridPlan{cols: 7, pitchY: 67, cell: 60, view: sdl.Rect{Y: 100, H: 596}}
	// Row 0 while parked at the top: nothing to do.
	if got := revealRowInGrid(g, 0, 0); got != 0 {
		t.Errorf("row 0 at scroll 0 moved to %d", got)
	}
	// A visible middle row: still nothing.
	if got := revealRowInGrid(g, 3*7, 0); got != 0 {
		t.Errorf("a visible row scrolled the grid to %d", got)
	}
	// Below the fold: scroll just enough that the cell's bottom edge lands on the
	// viewport's.
	last := revealRowInGrid(g, 20*7, 0)
	if want := 20*67 + 60 - 596; last != int32(want) {
		t.Errorf("row 20 revealed at %d, want %d", last, want)
	}
	// Above the fold: scroll to the row's own top, never past it.
	if got := revealRowInGrid(g, 2*7, 900); got != 2*67 {
		t.Errorf("row 2 from scroll 900 revealed at %d, want %d", got, 2*67)
	}
	// Degenerate plans are returned untouched rather than dividing by zero.
	if got := revealRowInGrid(charGridPlan{}, 5, 42); got != 42 {
		t.Errorf("an empty plan moved the scroll to %d", got)
	}
}

// TestCharListRowHNeverCollapses: the row height is font-derived so a DPI change moves
// the column with everything else, but it can never fall below a clickable box — a
// zero-height row is an invisible, un-hittable name.
func TestCharListRowHNeverCollapses(t *testing.T) {
	for _, fontH := range []int32{0, 1, 8, 14, 20, 48} {
		got := charListRowH(fontH)
		if got < charListRowMinH {
			t.Errorf("font %d px gives a %d px row, below the %d px floor", fontH, got, charListRowMinH)
		}
		if fontH+charListRowPadPx > charListRowMinH && got != fontH+charListRowPadPx {
			t.Errorf("font %d px gives %d, want %d", fontH, got, fontH+charListRowPadPx)
		}
	}
}

// TestThemedGridLeavesTheWheelForTheNameColumn is the one place the two views compete
// for input. WheelIn is single-consumer and the grid draws first; with the classic
// full-window band the grid would swallow every notch aimed at the column, which
// shares its Y range, and the names would never scroll.
func TestThemedGridLeavesTheWheelForTheNameColumn(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	const w, h int32 = 1920, 1080
	a, lay, g := nativeCharSelect(t, stockCharSelectDesign(), w, h)
	a.ctx = ctx
	if !g.themed {
		t.Fatal("fixture did not produce a themed grid")
	}
	col, ok := a.charSelectNameListRect(lay)
	if !ok {
		t.Fatal("no name column to compete with")
	}
	// Pointer over the NAME COLUMN: the grid must not take the wheel.
	ctx.mouseX, ctx.mouseY = col.X+col.W/2, col.Y+col.H/2
	ctx.wheelY, ctx.wheelTaken = -1, false
	if got := a.charGridScroll(w, "charscroll", g, 0, 1000); got != 0 {
		t.Errorf("the grid scrolled to %d on a wheel notch aimed at the name column", got)
	}
	if ctx.wheelTaken {
		t.Error("the grid consumed a wheel notch over the name column")
	}
	// Pointer over the GRID: it does take the wheel.
	ctx.mouseX, ctx.mouseY = g.view.X+g.view.W/2, g.view.Y+g.view.H/2
	ctx.wheelY, ctx.wheelTaken = -1, false
	if got := a.charGridScroll(w, "charscroll", g, 0, 1000); got == 0 {
		t.Error("the grid ignored a wheel notch aimed at itself")
	}
}

// drawableCharSelect stages a real Ctx (dummy video driver) over the stock table so
// the widget draws can actually run.
func drawableCharSelect(t *testing.T, w, h int32) (*App, *charSelectLayoutCache, charGridPlan) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a, lay, g := nativeCharSelect(t, stockCharSelectDesign(), w, h)
	a.ctx = ctx
	a.uiScalePct = 100
	return a, lay, g
}

// TestCharNameListClickSelectsAndRevealsIsTheSharedSelection is C1's payoff, drawn for
// real: a click on a name must set the selection the GRID paints, and scroll the grid
// far enough to show that character's cell. A double click picks, which is AO2's
// itemDoubleClicked — not exercised here because picking leaves the screen.
func TestCharNameListClickSelectsAndRevealsIsTheSharedSelection(t *testing.T) {
	const w, h int32 = 1920, 1080
	a, lay, g := drawableCharSelect(t, w, h)
	col, ok := a.charSelectNameListRect(lay)
	if !ok {
		t.Fatal("no name column")
	}
	// A roster long enough that the last name is far off the grid's first page.
	const n = 400
	rows := charListRows{n: n, chars: make([]courtroom.CharacterSlot, n), lower: make([]string, n)}
	for i := range rows.chars {
		rows.chars[i].Name = "char" + string(rune('A'+i%26))
		rows.lower[i] = rows.chars[i].Name
	}
	rowH := charListRowH(a.fontLineH())
	// Click the third visible name (row 2 of the body, which starts under the header).
	const target = 2
	a.ctx.mouseX = col.X + 4
	a.ctx.mouseY = col.Y + rowH + target*rowH + rowH/2
	a.ctx.clicked = true
	a.drawCharNameList(lay, rows, "", true, g)
	if a.charListSel != target {
		t.Fatalf("clicking name %d selected %d", target, a.charListSel)
	}
	if a.charScroll != 0 {
		t.Errorf("revealing an already-visible cell scrolled the grid to %d", a.charScroll)
	}
	// Now a name far down the list: the grid must follow.
	a.ctx.clicked = true
	a.charListScroll = int32(300) * rowH // scroll the column to name 300
	a.ctx.mouseY = col.Y + rowH + rowH/2 // the first row now visible
	a.drawCharNameList(lay, rows, "", true, g)
	if a.charListSel < 290 {
		t.Fatalf("a scrolled column selected %d, want a name near 300", a.charListSel)
	}
	if a.charScroll <= 0 {
		t.Errorf("selecting a name below the fold left the grid at %d", a.charScroll)
	}
	// And the revealed cell really is inside the viewport once the grid snaps to it.
	ord := int32(a.charListSel)
	cell := g.cellAt(ord, a.charScroll)
	if cell.Y < g.view.Y || cell.Y+cell.H > g.view.Y+g.view.H {
		t.Errorf("cell %+v is not inside the viewport %+v after the reveal", cell, g.view)
	}
}

// TestCharTakenCheckboxIsTheFilter draws the themed control row and clicks AO2's
// char_taken checkbox: it must flip the filter (not merely repaint) and reset both
// scroll offsets, which were measured against the old content height.
func TestCharTakenCheckboxIsTheFilter(t *testing.T) {
	const w, h int32 = 1920, 1080
	a, lay, g := drawableCharSelect(t, w, h)
	r, ok := lay.rect(charSelectTakenKey)
	if !ok {
		t.Fatal("char_taken did not resolve on the stock table")
	}
	if !a.charShowTaken {
		t.Fatal("fixture did not start with the AO2 default (checked = show taken)")
	}
	a.charScroll, a.charListScroll = 500, 700
	a.ctx.mouseX, a.ctx.mouseY = r.X+checkboxBoxPx/2, r.Y+r.H/2
	a.ctx.clicked = true
	if a.drawCharSelectControls(lay, g) {
		t.Fatal("the taken checkbox changed the screen")
	}
	if a.charShowTaken {
		t.Error("clicking char_taken did not clear the filter")
	}
	if a.charScroll != 0 || a.charListScroll != 0 {
		t.Errorf("a filter change left stale scroll offsets: grid %d, column %d", a.charScroll, a.charListScroll)
	}
	// A frame with no click leaves it alone.
	a.ctx.clicked = false
	a.drawCharSelectControls(lay, g)
	if a.charShowTaken {
		t.Error("the filter re-checked itself on a quiet frame")
	}
}

// TestCharSelectPageArrowsStepWholePages wires AO2's char_select_left/right onto our
// scrolling grid: a press must move by as many whole rows as the viewport holds, and
// the left arrow must be able to bring the grid back to the top.
func TestCharSelectPageArrowsStepWholePages(t *testing.T) {
	const w, h int32 = 1920, 1080
	a, lay, g := drawableCharSelect(t, w, h)
	right, ok := lay.rect(charSelectRightKey)
	if !ok {
		t.Fatal("char_select_right did not resolve")
	}
	wantPage := (g.view.H / g.pitchY) * g.pitchY
	if wantPage < g.pitchY {
		t.Fatal("fixture viewport holds no whole row")
	}
	a.ctx.mouseX, a.ctx.mouseY = right.X+right.W/2, right.Y+right.H/2
	a.ctx.clicked = true
	a.drawCharSelectPageArrows(lay, g)
	if a.charScroll != wantPage {
		t.Errorf("the next-page arrow moved %d px, want one whole page of rows (%d)", a.charScroll, wantPage)
	}
	if a.charScroll%g.pitchY != 0 {
		t.Errorf("a page step of %d is not a whole number of %d px rows", a.charScroll, g.pitchY)
	}
	left, ok := lay.rect(charSelectLeftKey)
	if !ok {
		t.Fatal("char_select_left did not resolve")
	}
	a.ctx.mouseX, a.ctx.mouseY = left.X+left.W/2, left.Y+left.H/2
	a.ctx.clicked = true
	a.drawCharSelectPageArrows(lay, g)
	if a.charScroll != 0 {
		t.Errorf("the previous-page arrow left the grid at %d, want the top", a.charScroll)
	}
	// The arrows follow the active tab's grid, not always the Characters one.
	a.charTab = charTabWardrobe
	a.ctx.mouseX, a.ctx.mouseY = right.X+right.W/2, right.Y+right.H/2
	a.ctx.clicked = true
	a.drawCharSelectPageArrows(lay, g)
	if a.iniScroll != wantPage || a.charScroll != 0 {
		t.Errorf("on the Wardrobe tab the arrows moved the wrong grid: ini=%d chars=%d", a.iniScroll, a.charScroll)
	}
}

// TestCharSelectTakenFilterOnlyAppliesWhereItsCheckboxIs pins the rule the classic arm
// already followed and the themed one did not: a filter with no control is a trap.
//
// char_taken is deliberately NOT one of charSelectEssentialKeys, so the themed control
// row is taken whether or not the checkbox resolves — a theme may hide the key (AO2's
// 11037 convention) or the canvas may scale it under minThemedElementPx. Unguarded, a
// session that cleared the box and then changed theme would keep hiding every taken
// character with nothing on screen able to bring them back.
func TestCharSelectTakenFilterOnlyAppliesWhereItsCheckboxIs(t *testing.T) {
	const w, h int32 = 1920, 1080
	a, lay, _ := nativeCharSelect(t, stockCharSelectDesign(), w, h)
	if _, ok := lay.rect(charSelectTakenKey); !ok {
		t.Fatal("the stock table did not resolve char_taken — this test's premise is gone")
	}
	a.charShowTaken = false
	if a.charSelectShowTaken(true, lay) {
		t.Error("the checkbox is on screen, so the filter must follow it")
	}
	a.charShowTaken = true
	if !a.charSelectShowTaken(true, lay) {
		t.Error("a checked box must show taken characters")
	}
	// The classic screen has no checkbox at all: it must behave exactly as it did
	// before #20, whatever the session value says.
	a.charShowTaken = false
	if !a.charSelectShowTaken(false, lay) {
		t.Error("the classic screen inherited a filter it has no control for")
	}
	// A theme that hides the key keeps the themed row — and must lose the filter with
	// the control, not keep it invisibly.
	hidden := stockCharSelectDesign()
	hidden.r[charSelectTakenKey] = theme.Rect{X: 11037, Y: 7, W: 80, H: 22}
	b, hLay, hG := nativeCharSelect(t, hidden, w, h)
	if _, ok := hLay.rect(charSelectTakenKey); ok {
		t.Fatal("fixture failed to hide char_taken")
	}
	if !b.charSelectWidgetsThemed(hLay, hG) {
		t.Fatal("hiding char_taken must not cost a theme its control row (it is not an essential key)")
	}
	b.charShowTaken = false
	if !b.charSelectShowTaken(true, hLay) {
		t.Error("a theme with no char_taken box still applied the taken filter — the roster hides " +
			"characters the user cannot bring back")
	}
}

// TestCharSelectWardrobeTabHostsItsOwnDeferredActions is the other half of "one cell,
// two hosts". drawIniswapCell cannot act on the ★ or the right-click move menu inline —
// both rebuild the iniList / iniWardrobe / iniFolders slices the grid loop is ranging,
// which panicked on a remove — so it parks them and the HOST services them after the
// loop. Only the wardrobe modal ever did, so on the char-select Wardrobe tab the ★
// silently did nothing and a right-click armed a menu that was drawn nowhere; both then
// fired later, out of context, the next time the modal opened.
func TestCharSelectWardrobeTabHostsItsOwnDeferredActions(t *testing.T) {
	a, _, cleanup := cellChromeHarness(t)
	defer cleanup()
	a.serverKey = "ws://wardrobe.test"
	const w, h int32 = 320, 240
	lay := a.charSelectLayout(w, h) // themeless: the classic plan, which is what this tab had
	g := a.charSelectGridPlan(w, h, 40, lay)
	rows := func() charListRows {
		return charListRows{n: len(a.iniList), names: a.iniList, lower: a.iniLower}
	}

	// The ★.
	a.iniFavPending, a.iniFavPendingAdd = "Phoenix", true
	a.drawWardrobeGrid(w, h, g, rows(), "")
	if a.iniFavPending != "" {
		t.Error("a deferred ★ toggle survived the frame — on this screen the star does nothing")
	}
	if list := a.d.Prefs.WardrobeList(a.serverKey); len(list) != 1 || list[0] != "Phoenix" {
		t.Errorf("the wardrobe is %v, want the starred character to have been saved", list)
	}

	// The move-to-folder menu: Esc is handleIniFolderMenu's own close path, so a screen
	// that never runs it can never close what its own cells arm.
	a.iniMenuChar, a.iniMenuAt = "Phoenix", [2]int32{50, 50}
	a.ctx.escPressed = true
	a.drawWardrobeGrid(w, h, g, rows(), "")
	a.ctx.escPressed = false
	if a.iniMenuChar != "" {
		t.Error("the char-select Wardrobe tab never serviced the move-to-folder menu its own cells arm")
	}

	// ...and the empty-wardrobe arm returns early, so the host bookkeeping has to sit
	// OUTSIDE it: a ★ that emptied the list must still be applied on that very frame.
	a.iniList, a.iniLower, a.iniFolders, a.iniWardrobe, a.iniHoverIDs = nil, nil, nil, nil, nil
	a.iniFavPending, a.iniFavPendingAdd = "Phoenix", false
	a.drawWardrobeGrid(w, h, g, charListRows{}, "")
	if a.iniFavPending != "" {
		t.Error("the empty-wardrobe arm skipped the host bookkeeping")
	}
	if list := a.d.Prefs.WardrobeList(a.serverKey); len(list) != 0 {
		t.Errorf("the wardrobe is %v after an un-star, want it empty", list)
	}
}

// TestWardrobeGridPaintsTheHoverSelectorToo pins the two tabs' last visible divergence.
// Both grids draw on ONE lattice over ONE backdrop since #20, so the theme's
// char_selector — AO2's 62x62 hover halo, raised above every button
// (aocharbutton.cpp) — has to appear on whichever tab the pointer is over. It was wired
// on the Characters grid only, so the same cell in the same place behaved differently
// depending on which roster was showing.
func TestWardrobeGridPaintsTheHoverSelectorToo(t *testing.T) {
	a, ren, cleanup := cellChromeHarness(t)
	defer cleanup()
	const w, h int32 = 200, 120
	// A 1:1 themed lattice built by hand: canvas == viewport, so scaleToCanvas is the
	// identity and cell 0 lands at the design origin.
	view := sdl.Rect{X: 0, Y: 0, W: w, H: h}
	g := charGridPlan{
		view: view, themed: true, cols: 1, cell: charSelectButtonPx,
		pitchX: charSelectButtonPx + 7, pitchY: charSelectButtonPx + 7, haloPx: 1,
		canvas: view, designW: w, designH: h, designX: 20, designY: 20,
		designPitchX: charSelectButtonPx + 7, designPitchY: charSelectButtonPx + 7,
		originX: 20, originY: 20, trackX: w - scrollBarW,
	}
	cell := g.cellAt(0, 0)
	a.ctx.mouseX, a.ctx.mouseY = cell.X+cell.W/2, cell.Y+cell.H/2 // parked ON the cell
	rows := charListRows{n: len(a.iniList), names: a.iniList, lower: a.iniLower}
	shot := func() []byte {
		a.ctx.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
		a.drawWardrobeGrid(w, h, g, rows, "")
		return readRegion(t, ren, charSelectorRect(cell, g.haloPx))
	}
	shot() // warm the label cache
	without := shot()
	// A theme that ships char_selector. One flat 2x2 frame is enough: the halo is a
	// stretched blit, so any non-background pixel proves it painted.
	dec := &assets.Decoded{
		Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 2, 2))},
		Delays: []time.Duration{0}, Width: 2, Height: 2,
	}
	for i := range dec.Frames[0].Pix {
		dec.Frames[0].Pix[i] = 0xFF // opaque white: nothing else on this cell is white
	}
	if err := a.d.Store.UploadPinned(themeTexKey(themeStemCharSelector), dec); err != nil {
		t.Skipf("theme upload unavailable: %v", err)
	}
	a.themeTex[themeStemCharSelector] = true
	with := shot()
	if sameRegion(without, with) {
		t.Error("the Wardrobe grid ignored the theme's char_selector — the same cell shows a hover " +
			"halo on the Characters tab and none here")
	}
}

// TestCharSelectWidgetGateIsAllocFree keeps the per-frame decision off the heap. It
// runs once per char-select frame, alongside the grid plan (which has its own gate).
func TestCharSelectWidgetGateIsAllocFree(t *testing.T) {
	const w, h int32 = 1920, 1080
	a, lay, g := nativeCharSelect(t, stockCharSelectDesign(), w, h)
	if n := testing.AllocsPerRun(200, func() { a.charSelectWidgetsThemed(lay, g) }); n != 0 {
		t.Errorf("charSelectWidgetsThemed allocates %v per frame, want 0", n)
	}
	if n := testing.AllocsPerRun(200, func() { a.charSelectNameListRect(lay) }); n != 0 {
		t.Errorf("charSelectNameListRect allocates %v per frame, want 0", n)
	}
	rows := charListRows{n: 3, chars: []courtroom.CharacterSlot{{Name: "A"}, {Name: "B", Taken: true}, {Name: "C"}}, lower: []string{"a", "b", "c"}}
	if n := testing.AllocsPerRun(200, func() { rows.matches("b", false) }); n != 0 {
		t.Errorf("charListRows.matches allocates %v per frame, want 0", n)
	}
}
