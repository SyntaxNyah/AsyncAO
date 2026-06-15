package ui

// The floating Extras box: a non-invasive, on-top panel hosting every AsyncAO
// feature an AO2 theme has no button for. Unlike the other courtroom popups it
// does NOT block — the scene, chat and logs stay live underneath. The kit has no
// z-aware input, so instead of fencing the whole screen (a modal) the box hides
// the pointer from the courtroom pass only while the cursor sits over its own
// footprint (maybeSuppressForBox), then draws itself with real input on top.
// Opened by the Extras button or the 'x' hotkey (toggles a.showWidgets).
//
// Slice 0 (here): fixed position, Close, the widget grid, and the input fence —
// proving the courtroom stays live around it and clicks don't leak through.
// Move, resize, collapse and drag-a-widget-out build on this.

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	extrasBoxW   = int32(380) // box width (slice 0: fixed)
	extrasBoxH   = int32(360) // box height
	extrasTitleH = int32(26)  // title bar / drag handle height
)

// courtModalOpen reports whether a blocking courtroom popup is up. The box
// yields to those and reappears when they close.
func (a *App) courtModalOpen() bool {
	return a.showIni || a.bgPick.show || a.showEvid || a.showModcall ||
		a.showUICfg || a.showLogin || a.pairPopupOpen || a.showPair
}

// extrasBoxVisible reports whether the floating Extras box should draw: opened,
// in a live courtroom, and not shadowed by a blocking popup.
func (a *App) extrasBoxVisible() bool {
	return a.showWidgets && a.room != nil && a.sess != nil && !a.courtModalOpen()
}

// extrasBoxRect is the box's screen rect: the dragged position once placed, else
// a centered-near-the-top default. Always clamped fully on-screen so a resize or
// a moved-then-shrunk window can't strand it off-edge.
func (a *App) extrasBoxRect(w, h int32) sdl.Rect {
	x, y := a.extrasBoxX, a.extrasBoxY
	if !a.extrasPlaced {
		x, y = (w-extrasBoxW)/2, 76
	}
	maxX, maxY := w-extrasBoxW-8, h-extrasBoxH-8
	if maxX < 8 {
		maxX = 8
	}
	if maxY < 8 {
		maxY = 8
	}
	return sdl.Rect{X: clampI32(x, 8, maxX), Y: clampI32(y, 8, maxY), W: extrasBoxW, H: extrasBoxH}
}

// handleExtrasDrag moves the box by its title bar. The grab offset is captured
// on press so the box tracks the cursor without jumping. Runs in the box's own
// pass (real pointer), after the courtroom fence was restored.
func (a *App) handleExtrasDrag(handle sdl.Rect, w, h int32) {
	c := a.ctx
	pressed := c.mouseDown && !a.extrasPrevDown
	a.extrasPrevDown = c.mouseDown
	if pressed && c.mouseX >= handle.X && c.mouseX < handle.X+handle.W &&
		c.mouseY >= handle.Y && c.mouseY < handle.Y+handle.H {
		r := a.extrasBoxRect(w, h)
		a.extrasDragging = true
		a.extrasGrabDX, a.extrasGrabDY = c.mouseX-r.X, c.mouseY-r.Y
	}
	if !c.mouseDown {
		a.extrasDragging = false
	}
	if a.extrasDragging {
		a.extrasBoxX, a.extrasBoxY = c.mouseX-a.extrasGrabDX, c.mouseY-a.extrasGrabDY
		a.extrasPlaced = true
	}
}

// boxFencesPointer reports whether the courtroom pass should run pointer-blind
// this frame: the box is up and the cursor sits over its footprint, so a click
// in the box must not also hit the scene/log underneath.
func (a *App) boxFencesPointer(w, h int32) bool {
	if !a.extrasBoxVisible() {
		return false
	}
	r := a.extrasBoxRect(w, h)
	mx, my := a.ctx.mouseX, a.ctx.mouseY
	return mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H
}

// drawFloatingExtras paints the box on top of the live courtroom. Picking a
// widget runs its action but LEAVES the box open (non-invasive); a widget that
// opens its own blocking panel (Background, Evidence, …) hides the box until
// that panel closes, then it returns.
func (a *App) drawFloatingExtras(w, h int32) {
	if !a.extrasBoxVisible() {
		return
	}
	c := a.ctx
	r := a.extrasBoxRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	// Title bar — becomes the drag handle when move lands.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: extrasTitleH}, ColPanelHi)
	c.Label(r.X+10, r.Y+6, "AsyncAO Extras", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 26, Y: r.Y + 4, W: 20, H: extrasTitleH - 8}, "x") {
		a.showWidgets = false
		return
	}
	// Drag-to-move by the title bar (excluding the close button on the right).
	a.handleExtrasDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 30, H: extrasTitleH}, w, h)

	widgets := []struct {
		label, desc, key string // key = hotkey id ("" = none), surfaced in the tooltip
		run              func()
	}{
		{"Character", "Open character select", hotkeyCharMenu, func() { a.screen = ScreenCharSelect }},
		{"Random char", "Swap to a random free character", hotkeyRandomChar, func() { a.randomChar() }},
		{"Wardrobe", "Iniswap — borrow another character's look", hotkeyWardrobe, func() { a.openIniswap() }},
		{"Jukebox", "Your saved music playlists", hotkeyJukebox, func() { a.openIniswap(); a.wardSection = wardSectionJukebox }},
		{"Background", "Change the courtroom background", hotkeyBackground, func() { a.openBgPicker() }},
		{"Evidence", "Add / view case evidence", hotkeyEvidence, func() { a.showEvid = true }},
		{"Call Mod", "Call a moderator to this room", hotkeyModcall, func() { a.showModcall = true }},
		{"Pair", "Pair up — share the stage with another character", hotkeyPairMenu, func() { a.showPair = true }},
		{"Login", "Log in with saved credentials", hotkeyLogin, func() { a.openLoginDialog() }},
		{"Hide chrome", "Hide/show AsyncAO's on-screen widgets", hotkeyUIChrome, func() { a.showUICfg = true }},
		{"Theater", "Theater mode — stage only, Esc exits", hotkeyTheater, func() { a.setTheater(!a.theaterOn) }},
		{"Settings", "Open settings", hotkeySettings, func() { a.prevScreen = ScreenCourtroom; a.screen = ScreenSettings }},
		{"Disconnect", "Leave this server", "", func() { a.Disconnect() }},
	}
	const cols = int32(2)
	const cellH, gap = int32(34), int32(8)
	cellW := (r.W - 20 - gap) / cols
	gx, gy := r.X+10, r.Y+extrasTitleH+8
	for i, wd := range widgets {
		col, row := int32(i)%cols, int32(i)/cols
		br := sdl.Rect{X: gx + col*(cellW+gap), Y: gy + row*(cellH+gap), W: cellW, H: cellH}
		if c.Button(br, wd.label) {
			wd.run()
			return // an action can open a sub-panel / switch screen — stop drawing stale widgets this frame
		}
		tip := wd.desc
		if wd.key != "" {
			tip += "  ·  Ctrl+" + strings.ToUpper(a.hotkeyFor(wd.key))
		}
		c.TooltipAfter("fextra:"+wd.label, br, tip)
	}
	c.LabelClipped(r.X+10, r.Y+r.H-18, r.W-20, "Drag the title to move · stays open while you play · × closes", ColTextDim)
}
