package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The reserved top band (#14): the menu bar plus the docked server-tab strip, and
// the rule that every screen's content starts below it.

// TestTopChromeHIsTheSumOfTheTwoStrips pins the accessor itself — that it really is
// the two strips added together and not one of them, because a screen offsetting by
// only the menu bar would put its first row straight under the tab chips.
func TestTopChromeHIsTheSumOfTheTwoStrips(t *testing.T) {
	a := testTabApp(t)
	if got := a.topChromeH(); got != menuBarH {
		t.Errorf("with no sessions the band = %d, want just the menu bar (%d)", got, menuBarH)
	}
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	if got, want := a.topChromeH(), menuBarH+tabBarH; got != want {
		t.Errorf("with a session the band = %d, want menu bar + strip (%d)", got, want)
	}
}

// TestTopChromeHDropsTheMenuBarOnFullWindowOverlays pins the L3 decision: gif
// export, replay, the scene maker and theater mode all PREEMPT the screen dispatch
// and take the whole window, so the menu bar neither paints nor reserves space
// there. The tab strip does still paint over them (app.go draws it after the
// dispatch), so its band survives — which is why the scene maker's content top, the
// download chip and the update chip all read this accessor rather than menuBarH.
func TestTopChromeHDropsTheMenuBarOnFullWindowOverlays(t *testing.T) {
	cases := []struct {
		name string
		set  func(*App)
	}{
		{"gif export", func(a *App) { a.gifExporting = true }},
		{"replay", func(a *App) { a.replaying, a.replayRoom = true, newRoomForTest(t) }},
		{"scene maker", func(a *App) { a.makerOpen = true }},
		{"theater", func(a *App) { a.theaterOn = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := testTabApp(t)
			a.tabs = []*courtTab{{}}
			a.activeTab = 0
			tc.set(a)
			if a.menuBarShows() {
				t.Fatalf("%s must not show the menu bar", tc.name)
			}
			if got, want := a.topChromeH(), int32(tabBarH); got != want {
				t.Errorf("band = %d, want just the tab strip (%d)", got, want)
			}
		})
	}
}

// TestHidingTheMenuBarGivesTheBandBack pins the Hide-UI entry for the menu strip:
// hiding it must not merely stop the paint, it must surrender the reserved band, or
// every screen would keep offsetting its content past a strip that is not there. The
// tab strip is unaffected — it docks under the bar and reads topChromeH itself.
func TestHidingTheMenuBarGivesTheBandBack(t *testing.T) {
	a := testTabApp(t)
	a.seedHiddenFromPrefs() // the production path that builds a.hidden
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	if got, want := a.topChromeH(), menuBarH+tabBarH; got != want {
		t.Fatalf("band = %d before hiding, want %d", got, want)
	}
	a.setPanelHidden(panelMenuBar, true)
	if a.menuBarShows() {
		t.Error("a hidden menu bar must not reserve its band")
	}
	if got := a.menuBarHeight(); got != 0 {
		t.Errorf("menuBarHeight = %d while hidden, want 0", got)
	}
	if got, want := a.topChromeH(), int32(tabBarH); got != want {
		t.Errorf("band = %d while hidden, want just the tab strip (%d)", got, want)
	}
	// Reversible, and the hide survives a prefs round trip like every other piece.
	a.setPanelHidden(panelMenuBar, false)
	if got, want := a.topChromeH(), menuBarH+tabBarH; got != want {
		t.Errorf("band = %d after unhiding, want %d", got, want)
	}
	a.setPanelHidden(panelMenuBar, true)
	a.seedHiddenFromPrefs()
	if !a.panelHidden(panelMenuBar) {
		t.Error("hiding the menu bar did not survive seedHiddenFromPrefs")
	}
	// The no-strand invariant is about the toolbox/Settings pair, so hiding the bar
	// must never be "corrected" away the way a stranded pair is.
	if a.panelHidden(panelToolbox) {
		t.Error("hiding the menu bar must not disturb the toolbox lifeline")
	}
}

// TestFloatingChipsClearTheChromeBand pins the two chips that used to be anchored at
// a bare tabBarH+4 and would otherwise be buried under the relocated strip.
func TestFloatingChipsClearTheChromeBand(t *testing.T) {
	a := testTabApp(t)
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.updateChipLabel = "Update available"
	if got := a.updateChipRect(1280).Y; got < a.topChromeH() {
		t.Errorf("update chip Y = %d, want it at or below the chrome band %d", got, a.topChromeH())
	}
}

// TestThemedCanvasSitsUnderTheChromeBand is the courtroom half of the fix: the
// theme's design canvas is laid out inside the height LEFT under the band and
// offset down by it, so it can never slide beneath the strips.
func TestThemedCanvasSitsUnderTheChromeBand(t *testing.T) {
	a := testTabApp(t)
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.themeRects = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		"viewport":  {X: 0, Y: 0, W: 256, H: 192},
	}
	const w, h = 1152, 864
	band := a.topChromeH()
	lay := a.themeWindowLayout(w, h)
	if !lay.valid {
		t.Fatal("themeWindowLayout did not validate")
	}
	if lay.topOff != band {
		t.Errorf("cache topOff = %d, want the live band %d", lay.topOff, band)
	}
	if lay.area.Y < band {
		t.Errorf("canvas top = %d, want it at or below the band %d", lay.area.Y, band)
	}
	if lay.area.Y+lay.area.H > h {
		t.Errorf("canvas bottom = %d, escapes the %d-tall window", lay.area.Y+lay.area.H, h)
	}
	// Every widget the theme places is inside the canvas, so none of them can be
	// under the strips either.
	for key, r := range lay.r {
		if key == "showname" || key == "message" {
			continue // chatbox-relative by design (theme_layout.go)
		}
		if r.Y < band {
			t.Errorf("themed %q at Y=%d is under the chrome band %d", key, r.Y, band)
		}
	}
}

// TestExportFrameCarriesNoChromeBand pins the L3 rule that a video/comic frame must
// not contain the client's own furniture: drawExportScene goes through themeLayout
// (band-free) while the live courtroom goes through themeWindowLayout, and the two
// share one cache — so the cache key has to include the band or an export would
// inherit the window's offset (a black stripe across every exported frame).
func TestExportFrameCarriesNoChromeBand(t *testing.T) {
	a := testTabApp(t)
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.themeRects = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		"viewport":  {X: 0, Y: 0, W: 256, H: 192},
	}
	const w, h = 1152, 864
	if band := a.topChromeH(); a.themeWindowLayout(w, h).area.Y < band {
		t.Fatalf("the window layout did not reserve the band (%d)", band)
	}
	// SAME dimensions, no band: if the cache key ignored topOff this would hand back
	// the window's entry and the assertion below would fail.
	lay := a.themeLayout(w, h)
	if lay.topOff != 0 {
		t.Fatalf("export layout topOff = %d, want 0", lay.topOff)
	}
	if want := int32(h-579) / 2; lay.area.Y != want {
		t.Errorf("export canvas Y = %d, want the plain centring %d — client chrome leaked into the frame", lay.area.Y, want)
	}
}

// TestLobbyUtilRowStaysOnScreen is L4: the lobby header's right-hand cluster used to
// be pinned by raw `w - <literal> - pad` rects, and at config.MinWindowW two of them
// (What's New at -78, Logs at -198) were simply off the window.
func TestLobbyUtilRowStaysOnScreen(t *testing.T) {
	for _, w := range []int32{config.MinWindowW, 800, 1024, config.DefaultWindowW, 1920} {
		btnW := lobbyUtilWidth(w)
		if btnW < lobbyUtilBtnMinW {
			t.Errorf("w=%d: button width %d is below the floor %d", w, btnW, lobbyUtilBtnMinW)
		}
		for i := int32(0); i < lobbyUtilBtnCount; i++ {
			x := lobbyUtilStop(w, i)
			if x < pad {
				t.Errorf("w=%d: button %d starts at x=%d, off the left edge", w, i, x)
			}
			if x+btnW > w-pad {
				t.Errorf("w=%d: button %d ends at x=%d, past the right edge %d", w, i, x+btnW, w-pad)
			}
		}
		// Stops must stay in right-to-left order, or two buttons would swap places.
		for i := int32(1); i < lobbyUtilBtnCount; i++ {
			if lobbyUtilStop(w, i) > lobbyUtilStop(w, i-1) {
				t.Errorf("w=%d: stop %d is right of stop %d", w, i, i-1)
			}
		}
	}
	// At the shipped default window the row is byte-identical to the historical
	// layout: full width, and the old literal offsets reproduced exactly.
	const defW = config.DefaultWindowW
	if got := lobbyUtilWidth(defW); got != lobbyUtilBtnW {
		t.Errorf("default window button width = %d, want the historical %d", got, lobbyUtilBtnW)
	}
	for i, literal := range []int32{110, 230, 350, 470, 590, 710, 830} {
		if got, want := lobbyUtilStop(defW, int32(i)), defW-literal-pad; got != want {
			t.Errorf("stop %d = %d, want the historical %d (the un-squeezed row must not move)", i, got, want)
		}
	}
}

// TestLobbyDirectConnectRowStaysOnScreen is the OTHER half of L4. The wave fixed the
// header's right-hand cluster; the direct-connect row underneath it kept walking the
// opposite way off the same window — `fieldX + 290 / +390 / +560` put the TLS
// checkbox, "Save to phone book" and the whole Connect button outside a
// config.MinWindowW window, and Connect is the row's entire point.
//
// The measured widths below are a real chrome font's, give or take; the layout is pure
// arithmetic over them, so the property holds for any font that produces them.
func TestLobbyDirectConnectRowStaysOnScreen(t *testing.T) {
	const fullW, shortW, tlsW, saveW = int32(332), int32(96), int32(90), int32(142)
	for _, w := range []int32{config.MinWindowW, 700, 800, 1024, config.DefaultWindowW, 1920} {
		row := lobbyDirectRow(w, fullW, shortW, tlsW, saveW)
		right := row.connX + dcConnectW
		switch {
		case right > w-pad:
			t.Errorf("w=%d: Connect ends at %d, past the right edge %d", w, right, w-pad)
		case row.fieldX < pad || row.tlsX < pad || row.saveX < pad || row.connX < pad:
			t.Errorf("w=%d: a control starts off the left edge (%+v)", w, row)
		case row.fieldW < dcFieldWMin:
			t.Errorf("w=%d: the input shrank to %d, below the floor %d", w, row.fieldW, dcFieldWMin)
		case row.fieldW > dcFieldWMax:
			t.Errorf("w=%d: the input grew to %d, past its comfortable width %d", w, row.fieldW, dcFieldWMax)
		}
		// Left-to-right order, or two controls swap places / overlap.
		if !(row.fieldX+row.fieldW <= row.tlsX && row.tlsX+tlsW <= row.saveX && row.saveX+saveW <= row.connX) {
			t.Errorf("w=%d: the controls are out of order or overlapping: %+v", w, row)
		}
		if row.label != "" && row.labelX+labelWidthFor(row.label, fullW, shortW) > row.fieldX {
			t.Errorf("w=%d: the caption %q runs into the input", w, row.label)
		}
	}
	// At the shipped default window the row is the historical layout: the full caption
	// and the full-width input.
	def := lobbyDirectRow(config.DefaultWindowW, fullW, shortW, tlsW, saveW)
	if def.label != dcLabelFull || def.fieldW != dcFieldWMax {
		t.Errorf("default window: caption %q / field %d, want the un-squeezed %q / %d", def.label, def.fieldW, dcLabelFull, dcFieldWMax)
	}
	// And a genuinely tiny window sheds the caption rather than the button.
	if tiny := lobbyDirectRow(480, fullW, shortW, tlsW, saveW); tiny.connX+dcConnectW > 480-pad {
		t.Errorf("even below MinWindowW, Connect must stay on the window: %+v", tiny)
	}
}

// labelWidthFor maps a chosen caption back to the width the layout was given for it.
func labelWidthFor(label string, fullW, shortW int32) int32 {
	if label == dcLabelFull {
		return fullW
	}
	return shortW
}

// TestThemedWidgetsStayClearOfTheBandInEveryFitMode is the all-modes half of the rule
// above. offY is only >= the band while the canvas FITS the room under it: Crop scales
// UP by design and Custom's pan can push the canvas anywhere, so in those two the
// centring term goes negative and the canvas top rises past the band. lay.area is the
// clamp target for every themed widget, so without a floor a themed button lands under
// an opaque strip — painted over AND fenced out of every hit test.
func TestThemedWidgetsStayClearOfTheBandInEveryFitMode(t *testing.T) {
	modes := []struct {
		name string
		mode int
	}{
		{"native", config.ThemeFitNative},
		{"letterbox", config.ThemeFitLetterbox},
		{"stretch", config.ThemeFitStretch},
		{"crop", config.ThemeFitCrop},
		{"custom", config.ThemeFitCustom},
	}
	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			a := testTabApp(t)
			a.tabs = []*courtTab{{}}
			a.activeTab = 0
			a.themeRects = map[string]theme.Rect{
				"courtroom":   {X: 0, Y: 0, W: 714, H: 579},
				"viewport":    {X: 0, Y: 0, W: 714, H: 382}, // top-anchored: the first thing to escape
				"ao2_chatbox": {X: 0, Y: 415, W: 714, H: 164},
			}
			a.d.Prefs.SetThemeFit(tc.mode)
			if tc.mode == config.ThemeFitCustom {
				a.d.Prefs.SetThemeFitZoom(200) // zoom past the window …
				a.d.Prefs.SetThemeFitPan(0, -40)
			}
			const w, h = 1152, 864
			band := a.topChromeH()
			lay := a.themeWindowLayout(w, h)
			if !lay.valid {
				t.Fatal("themeWindowLayout did not validate")
			}
			for key, r := range lay.r {
				if key == "showname" || key == "message" {
					continue // chatbox-relative by design (theme_layout.go)
				}
				if r.Y < band {
					t.Errorf("themed %q at Y=%d is under the %d px chrome band: painted over and input-dead", key, r.Y, band)
				}
			}
		})
	}
	// And the overflow modes really do overflow, or the loop above proved nothing.
	a := testTabApp(t)
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.themeRects = map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		"viewport":  {X: 0, Y: 0, W: 714, H: 382},
	}
	a.d.Prefs.SetThemeFit(config.ThemeFitCrop)
	if lay := a.themeWindowLayout(1152, 864); lay.area.Y >= a.topChromeH() {
		t.Fatalf("fixture: Crop must push the canvas top (%d) above the band (%d), or nothing is being tested",
			lay.area.Y, a.topChromeH())
	}
}

// TestThemeTabBarKeyIsAThemeLayoutKey pins that a theme really can position the
// strip — the key has to be in themeLayoutKeys or pollThemeApply never reads it out
// of courtroom_design.ini and the themed editor never offers a drag box for it.
func TestThemeTabBarKeyIsAThemeLayoutKey(t *testing.T) {
	found := false
	for _, k := range themeLayoutKeys {
		if k == themeTabBarKey {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("themeLayoutKeys is missing %q — a theme could not place the server-tab strip (#14)", themeTabBarKey)
	}
	if !themeKeyEditable(themeTabBarKey) {
		t.Errorf("%q is not editable — the themed editor would refuse to drag it, which is the reported bug", themeTabBarKey)
	}
}

// stockLikeThemeLayout is a theme's design geometry as courtroom_design.ini gives it
// to us — WITHOUT "asyncao_tabbar", which is the case for every theme that exists
// (the key is an AsyncAO invention; the 74-theme reference corpus has none). A fresh
// map per call, because the apply path takes ownership of it.
func stockLikeThemeLayout() map[string]theme.Rect {
	return map[string]theme.Rect{
		"courtroom": {X: 0, Y: 0, W: 714, H: 579},
		"viewport":  {X: 0, Y: 0, W: 714, H: 382},
	}
}

// applyThemeGeometryForTest replays pollThemeApply's geometry step verbatim: seed the
// client-chrome keys into the PRISTINE map, keep that map as themeRectsOrig, and lay
// the persisted overrides over a copy. Tests that stage a.themeRects by hand skip all
// three, which is exactly how a key that never entered the apply path could look wired
// up while being dead in the running client.
func applyThemeGeometryForTest(a *App, layout map[string]theme.Rect) {
	a.tabBarSeeded = seedTabBarDesignRect(layout)
	a.themeRectsOrig = layout
	rects := make(map[string]theme.Rect, len(layout))
	for k, v := range layout {
		rects[k] = v
	}
	a.themeRects = a.applyRectOverrides(rects)
	a.themeLay.valid = false
}

// stageTabbarlessCourtroom builds an App on a themed courtroom whose theme is silent about
// the tab strip.
func stageTabbarlessCourtroom(t *testing.T, themeName string) *App {
	t.Helper()
	a := testTabApp(t)
	a.d.Prefs.SetTheme(themeName, "")
	a.d.Prefs.SetThemeLayout(true) // default-ON today; pinned so a default flip can't silently un-gate these
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.room = newRoomForTest(t)
	a.screen = ScreenCourtroom
	applyThemeGeometryForTest(a, stockLikeThemeLayout())
	return a
}

// TestTabStripIsEditableUnderAThemeThatNeverDeclaredIt is issue #14's first defect —
// the strip cannot be moved under a theme — and it was still UNSHIPPED after
// asyncao_tabbar was added to themeLayoutKeys.
//
// Registering the key only means it is READ IF THE THEME DECLARES IT: the apply path
// copies only keys ElementRect resolves, the layout cache is built by ranging that
// map, and the themed editor's editable set is built from the layout cache. No shipped
// theme declares the key, so the strip never entered any of the three and the themed
// editor handed out no drag box — while the classic slot route stays closed under a
// theme (drawCourtroom force-stops the classic editor there). The two existing tests
// both stage the key by hand, so neither could catch it.
func TestTabStripIsEditableUnderAThemeThatNeverDeclaredIt(t *testing.T) {
	const themeName = "stocklike"
	a := stageTabbarlessCourtroom(t, themeName)
	if !a.tabBarSeeded {
		t.Fatal("a theme with no asyncao_tabbar must get the key synthesized at apply time")
	}
	if _, ok := a.themeRectsOrig[themeTabBarKey]; !ok {
		t.Fatal("the seed must land in the PRISTINE map: reset/undo restore from it, and applyRectOverrides only rewrites keys that already exist")
	}

	const w, h = 714, 700
	lay := a.themeWindowLayout(w, h)
	if !lay.valid {
		t.Fatal("themeWindowLayout did not validate")
	}
	// The editable key set, exactly as drawLayoutEditor builds it.
	editable := false
	for k := range lay.r {
		if k == themeTabBarKey && themeKeyEditable(k) {
			editable = true
		}
	}
	if !editable {
		t.Fatal("the themed editor still offers no drag box for the server-tab strip")
	}
	box, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the strip's box must be a usable rect")
	}

	// Untouched, the seeded default must NOT move the strip: it stays in the client
	// chrome band, and the editor's box sits exactly on the painted strip rather than
	// somewhere the strip is not.
	if a.tabStripThemeParked() {
		t.Error("a synthesized default nobody has placed must not count as parked")
	}
	if box.Y != a.menuBarHeight() {
		t.Errorf("the drag box is at Y=%d, want the docked strip's own Y=%d — a box that is not on the widget is worse than no box", box.Y, a.menuBarHeight())
	}
	rects, _ := a.tabBarRects(w, h)
	if len(rects) == 0 {
		t.Fatal("fixture: the strip must have a chip")
	}
	if rects[0].X != box.X || rects[0].Y != box.Y {
		t.Errorf("painted strip starts at (%d,%d) but the editor's box is at (%d,%d)", rects[0].X, rects[0].Y, box.X, box.Y)
	}

	// A drag: the editor rewrites the live design rect and persists a per-key override
	// (this is exactly what drawLayoutEditor does at press/release —
	// TestThemedEditorDragMovesAndPersistsTheTabStrip drives the real thing).
	moved := theme.Rect{X: 280, Y: 267, W: 240, H: int(tabBarH)}
	a.themeRects[themeTabBarKey] = moved
	a.d.Prefs.SetThemeRectOverride(themeName, themeTabBarKey, [4]int{moved.X, moved.Y, moved.W, moved.H})
	a.themeLay.valid = false
	if !a.tabStripThemeParked() {
		t.Fatal("a moved strip must count as parked, or the drag has nowhere to take effect")
	}
	lay = a.themeWindowLayout(w, h)
	placed, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the moved rect must survive the layout transform")
	}
	def := sdl.Rect{X: 10, Y: a.menuBarHeight(), W: 120, H: tabBarH}
	if got := a.tabStripOrigin(def, w, h); got.X != placed.X || got.Y != placed.Y {
		t.Errorf("the painted strip is at (%d,%d), want the placed (%d,%d)", got.X, got.Y, placed.X, placed.Y)
	}

	// RE-APPLY. A theme reload hands us a map that STILL lacks the key, so without the
	// seed going into the pristine map first, applyRectOverrides has nothing to write
	// onto and the user's drag is silently lost on the next theme load.
	applyThemeGeometryForTest(a, stockLikeThemeLayout())
	if got := a.themeRects[themeTabBarKey]; got != moved {
		t.Errorf("after a theme re-apply the strip is at %+v, want the persisted %+v", got, moved)
	}
	if !a.tabStripThemeParked() {
		t.Error("the re-applied override must still read as parked")
	}
}

// TestThemedEditorDragMovesAndPersistsTheTabStrip drives the REAL themed editor: a
// press on the strip's box, a drag, a release — and then the same reload the test
// above simulates. This is the end-to-end proof that the affordance issue #14 asked
// for actually exists, rather than the geometry merely being in the right maps.
//
// It asserts against the PAINTED BOX, not against "the stored design rect plus the
// delta". The strip is intrinsically sized, so its box is not the transform of its
// stored rect (tabStripCacheRect), and an assertion phrased as base+delta is satisfied
// by a drag that teleports the widget: measured before the fix, one pixel of movement
// moved the box {317,22,79,22} → {238,112,240,22} — 80 px left, 90 px down and 79→240
// wide — and the base+delta form sailed straight through it.
func TestThemedEditorDragMovesAndPersistsTheTabStrip(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	const themeName = "stocklike"
	a := stageTabbarlessCourtroom(t, themeName)
	a.ctx = ctx
	a.uiScalePct = 100

	const w, h = int32(714), int32(760)
	lay := a.themeWindowLayout(w, h)
	box, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the themed editor must be handed a box for the strip")
	}
	a.layoutEdit = true
	a.layoutSnap = false // exact arithmetic: no grid rounding, no piece-to-piece magnet

	// Press on the box.
	ctx.mouseX, ctx.mouseY = box.X+box.W/2, box.Y+box.H/2
	ctx.mouseDown, a.editPrev = true, false
	a.drawLayoutEditor(w, h, lay)
	if a.editKey != themeTabBarKey || a.editDrag != 1 {
		t.Fatalf("a press on the strip's box must grab it for a MOVE, got key=%q drag=%d", a.editKey, a.editDrag)
	}

	// Drag it down onto the canvas, then release.
	const dx, dy = int32(40), int32(300)
	ctx.mouseX, ctx.mouseY = box.X+box.W/2+dx, box.Y+box.H/2+dy
	a.drawLayoutEditor(w, h, a.themeWindowLayout(w, h))
	ctx.mouseDown = false
	a.drawLayoutEditor(w, h, a.themeWindowLayout(w, h))
	a.layoutEdit = false

	// The box travelled exactly as far as the cursor did, and did not resize.
	moved, ok := a.themeWindowLayout(w, h).rect(themeTabBarKey)
	if !ok {
		t.Fatal("the dragged rect must survive the layout transform")
	}
	if wantBox := (sdl.Rect{X: box.X + dx, Y: box.Y + dy, W: box.W, H: box.H}); moved != wantBox {
		t.Fatalf("after a (%d,%d) drag the box is %+v, want %+v (the cursor moved that far and no further, and a move must not resize)",
			dx, dy, moved, wantBox)
	}
	// And the strip really is painted there — box == widget.
	rects, _ := a.tabBarRects(w, h)
	if len(rects) == 0 || rects[0].X != moved.X || rects[0].Y != moved.Y {
		t.Fatalf("the strip paints at %+v, want the box's origin (%d,%d)", rects, moved.X, moved.Y)
	}

	want := a.themeRects[themeTabBarKey]
	ov := a.d.Prefs.ThemeRectOverrides(themeName)
	if got, ok := ov[themeTabBarKey]; !ok || got != [4]int{want.X, want.Y, want.W, want.H} {
		t.Fatalf("the release must persist the override, got %v (present=%v)", got, ok)
	}
	if !a.tabStripThemeParked() {
		t.Fatal("a dragged strip must read as parked")
	}

	def := sdl.Rect{X: 0, Y: a.menuBarHeight(), W: 120, H: tabBarH}
	if got := a.tabStripOrigin(def, w, h); got.X != moved.X || got.Y != moved.Y {
		t.Errorf("the strip paints at (%d,%d), want the dragged (%d,%d)", got.X, got.Y, moved.X, moved.Y)
	}
	// The placement survives a theme reload.
	applyThemeGeometryForTest(a, stockLikeThemeLayout())
	if got := a.themeRects[themeTabBarKey]; got != want {
		t.Errorf("the drag was lost on a theme reload: %+v, want %+v", got, want)
	}
	if got, ok := a.themeWindowLayout(w, h).rect(themeTabBarKey); !ok || got != moved {
		t.Errorf("after the reload the box is %+v (present=%v), want the dragged %+v", got, ok, moved)
	}
}

// TestTabStripIgnoresExportSpaceGeometry pins the export guard. Gif export, replay and
// the scene maker all PREEMPT the screen dispatch while leaving a.screen == Courtroom
// and a.room non-nil, and they rebuild the SHARED layout cache at the export frame's
// dimensions with no chrome band — so a strip that read the cache in those modes would
// teleport for the whole export.
func TestTabStripIgnoresExportSpaceGeometry(t *testing.T) {
	const themeName = "stocklike"
	base := stageTabbarlessCourtroom(t, themeName)
	parkTabStripForTest(base, themeName, theme.Rect{X: 300, Y: 400, W: 200, H: int(tabBarH)})
	const w, h = 714, 700
	base.themeWindowLayout(w, h)
	if _, ok := base.tabStripThemeRect(); !ok {
		t.Fatal("fixture: a parked strip must read the theme rect on an ordinary courtroom frame")
	}
	for _, tc := range []struct {
		name string
		set  func(*App)
	}{
		{"gif export", func(a *App) { a.gifExporting = true }},
		{"replay", func(a *App) { a.replaying, a.replayRoom = true, a.room }},
		{"scene maker", func(a *App) { a.makerOpen = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := stageTabbarlessCourtroom(t, themeName)
			parkTabStripForTest(a, themeName, theme.Rect{X: 300, Y: 400, W: 200, H: int(tabBarH)})
			a.themeWindowLayout(w, h)
			tc.set(a)
			if _, ok := a.tabStripThemeRect(); ok {
				t.Errorf("%s must not place the strip from the courtroom layout cache — that cache is rebuilt in EXPORT space", tc.name)
			}
		})
	}
}

// parkTabStripForTest records a strip placement the way the editor's drag release does:
// the live design rect AND the persisted override, which is the placement flag itself
// (tabStripThemeParked). Writing only the design rect used to be enough because
// parked-ness was inferred from geometry — the inference a drag landing on the pristine
// seed silently defeated, which is why it is gone.
func parkTabStripForTest(a *App, themeName string, r theme.Rect) {
	a.themeRects[themeTabBarKey] = r
	a.d.Prefs.SetThemeRectOverride(themeName, themeTabBarKey, [4]int{r.X, r.Y, r.W, r.H})
	a.themeLay.valid = false
}

// TestTabStripFollowsTheThemeRect pins the consumption side: with the themed
// courtroom on screen and the theme declaring asyncao_tabbar, the strip parks there;
// on any other screen the same cache must NOT move it (the strip paints over every
// screen, so a latched courtroom rect would teleport it on the lobby).
func TestTabStripFollowsTheThemeRect(t *testing.T) {
	a := testTabApp(t)
	a.tabs = []*courtTab{{}}
	a.activeTab = 0
	a.room = newRoomForTest(t)
	a.screen = ScreenCourtroom
	a.themeRects = map[string]theme.Rect{
		"courtroom":    {X: 0, Y: 0, W: 714, H: 579},
		"viewport":     {X: 0, Y: 0, W: 256, H: 192},
		themeTabBarKey: {X: 300, Y: 400, W: 200, H: 22},
	}
	const w, h = 714, 700
	lay := a.themeWindowLayout(w, h)
	if !lay.valid {
		t.Fatal("themeWindowLayout did not validate")
	}
	want, ok := lay.rect(themeTabBarKey)
	if !ok {
		t.Fatal("the theme's asyncao_tabbar rect did not survive the layout transform")
	}
	def := sdl.Rect{X: 10, Y: a.menuBarHeight(), W: 120, H: tabBarH}
	got := a.tabStripOrigin(def, w, h)
	if got.X != want.X || got.Y != want.Y {
		t.Errorf("themed strip origin = (%d,%d), want the theme's (%d,%d)", got.X, got.Y, want.X, want.Y)
	}
	if got.W != def.W || got.H != def.H {
		t.Errorf("themed strip size = %dx%d, want the intrinsic %dx%d (the strip is move-only)", got.W, got.H, def.W, def.H)
	}
	// Off the courtroom the cache is still valid, but the strip must go back to its
	// docked default.
	a.screen = ScreenLobby
	if got := a.tabStripOrigin(def, w, h); got != def {
		t.Errorf("on the lobby the strip = %+v, want the docked default %+v", got, def)
	}
	// Theater mode is stage-only: the themed courtroom is not what is drawn there.
	a.screen, a.theaterOn = ScreenCourtroom, true
	if got := a.tabStripOrigin(def, w, h); got != def {
		t.Errorf("in theater the strip = %+v, want the docked default %+v", got, def)
	}
}
