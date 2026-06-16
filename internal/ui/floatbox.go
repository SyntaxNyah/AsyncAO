package ui

// The floating Extras box: a non-invasive, on-top panel hosting every AsyncAO
// feature an AO2 theme has no button for. Unlike the other courtroom popups it
// does NOT block — the scene, chat and logs stay live underneath. The kit has no
// z-aware input, so instead of fencing the whole screen (a modal) the courtroom
// pass runs pointer-blind only while the cursor sits over a box footprint
// (boxFencesPointer + fencePointer), then the boxes draw last with real input.
// Opened by the Extras button or the 'x' hotkey (toggles a.showWidgets).
//
// Widgets live in the main grid but TEAR OUT: drag one past a small threshold
// and it pops into its own little floating box you move and close independently
// (closing returns it to the grid). Every box shares one per-frame mouse-press
// edge so exactly one of them grabs a given press.

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	extrasBoxW   = int32(380) // main box default width
	extrasBoxH   = int32(360) // main box default height
	extrasMinW   = int32(300) // resize floor: 2 columns stay readable
	extrasMinH   = int32(340) // resize floor: all 13 widgets' rows + hint still fit
	extrasTitleH = int32(26)  // title bar / drag handle height (main + torn boxes)
	extrasGripSz = int32(16)  // bottom-right resize grip

	detachedBoxW   = int32(176) // a torn-off widget's own little box (default)
	detachedBoxH   = int32(66)
	detachedMinW   = int32(120) // resize floor: the label + close still fit
	detachedMinH   = int32(54)
	detachedGripSz = int32(12) // smaller resize grip for the little torn-off boxes
	extrasTearPx   = int32(8)  // drag a grid cell this far to tear it loose
)

// extrasWidget is one entry in the Extras box: a labelled action you click to
// run or drag out into its own floating box.
type extrasWidget struct {
	label, desc, key string // key = hotkey id ("" = none), surfaced in the tooltip
	run              func()
}

// detachedWidget is a widget torn out into its own box at (x,y), sized w×h. id
// indexes the canonical extrasWidgets table; (x,y) is the raw (pre-clamp)
// top-left; w/h are 0 until the box is resized (then its user size).
type detachedWidget struct {
	id   int
	x, y int32
	w, h int32
}

// extrasWidgets returns the canonical widget table, built once and cached. The
// closures capture the stable *App receiver, so caching them is safe and drops
// the per-frame slice/closure allocations the inline build used to cost.
func (a *App) extrasWidgets() []extrasWidget {
	if a.extrasWidgetCache == nil {
		a.extrasWidgetCache = []extrasWidget{
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
	}
	return a.extrasWidgetCache
}

// courtModalOpen reports whether a blocking courtroom popup is up. The box (and
// its torn-off widgets) yields to those and reappears when they close.
func (a *App) courtModalOpen() bool {
	return a.showIni || a.bgPick.show || a.showEvid || a.showModcall ||
		a.showUICfg || a.showLogin || a.pairPopupOpen || a.showPair
}

// extrasSurfaceLive reports whether the Extras surface (the MAIN box and/or any
// torn-off boxes) may show at all: a live courtroom with no blocking popup over
// it. Torn-off boxes ride on this alone, so they persist when the main box is
// closed — closing the main box must not yank the widgets you dragged out.
func (a *App) extrasSurfaceLive() bool {
	return a.room != nil && a.sess != nil && !a.courtModalOpen()
}

// extrasBoxVisible reports whether the MAIN box should draw: opened (showWidgets)
// on a live surface. (Torn-off boxes are gated only by extrasSurfaceLive.)
func (a *App) extrasBoxVisible() bool {
	return a.showWidgets && a.extrasSurfaceLive()
}

// extrasBoxRect is the main box's screen rect: the (possibly user-resized) size
// at the dragged position once placed, else a centered-near-the-top default.
// Size clamps to [min, window] and the position clamps fully on-screen, so a
// resize or a moved-then-shrunk window can't strand it off-edge or oversize it.
func (a *App) extrasBoxRect(w, h int32) sdl.Rect {
	bw, bh := extrasBoxW, extrasBoxH
	if a.extrasUserW > 0 {
		bw = a.extrasUserW
	}
	if a.extrasUserH > 0 {
		bh = a.extrasUserH
	}
	hiW, hiH := w-16, h-16 // never wider/taller than the window
	if hiW < extrasMinW {
		hiW = extrasMinW
	}
	if hiH < extrasMinH {
		hiH = extrasMinH
	}
	bw, bh = clampI32(bw, extrasMinW, hiW), clampI32(bh, extrasMinH, hiH)
	x, y := a.extrasBoxX, a.extrasBoxY
	if !a.extrasPlaced {
		x, y = (w-bw)/2, 76
	}
	maxX, maxY := w-bw-8, h-bh-8
	if maxX < 8 {
		maxX = 8
	}
	if maxY < 8 {
		maxY = 8
	}
	return sdl.Rect{X: clampI32(x, 8, maxX), Y: clampI32(y, 8, maxY), W: bw, H: bh}
}

// detachedBoxRect is the i-th torn-off widget's screen rect: its (possibly
// resized) size clamped to [min, window], placed at its clamped-on-screen top-left.
func (a *App) detachedBoxRect(i int, w, h int32) sdl.Rect {
	d := a.extrasDetached[i]
	bw, bh := detachedBoxW, detachedBoxH
	if d.w > 0 {
		bw = d.w
	}
	if d.h > 0 {
		bh = d.h
	}
	hiW, hiH := w-8, h-8
	if hiW < detachedMinW {
		hiW = detachedMinW
	}
	if hiH < detachedMinH {
		hiH = detachedMinH
	}
	bw, bh = clampI32(bw, detachedMinW, hiW), clampI32(bh, detachedMinH, hiH)
	maxX, maxY := w-bw-4, h-bh-4
	if maxX < 4 {
		maxX = 4
	}
	if maxY < 4 {
		maxY = 4
	}
	return sdl.Rect{X: clampI32(d.x, 4, maxX), Y: clampI32(d.y, 4, maxY), W: bw, H: bh}
}

// widgetDetached reports whether widget id is currently torn out (so the grid
// skips it).
func (a *App) widgetDetached(id int) bool {
	for _, d := range a.extrasDetached {
		if d.id == id {
			return true
		}
	}
	return false
}

// boxFencesPointer reports whether the courtroom pass should run pointer-blind
// this frame: any Extras box is up under the cursor, or a box drag/resize is in
// flight (so a fast drag can't leak a click to the scene between frames). Gated
// on extrasSurfaceLive — NOT extrasBoxVisible — so torn-off boxes still fence
// the scene when the main box is closed (else clicks would leak through them).
func (a *App) boxFencesPointer(w, h int32) bool {
	if !a.extrasSurfaceLive() {
		return false
	}
	if a.extrasDragging || a.extrasDetachDragging || a.extrasPressing || a.extrasResizing || a.extrasDetachResizing {
		return true
	}
	mx, my := a.ctx.mouseX, a.ctx.mouseY
	if a.showWidgets && pointIn(mx, my, a.extrasBoxRect(w, h)) {
		return true
	}
	for i := range a.extrasDetached {
		if pointIn(mx, my, a.detachedBoxRect(i, w, h)) {
			return true
		}
	}
	return false
}

// handleExtrasDrag moves the main box by its title bar. pressed is this frame's
// shared, unconsumed mouse-press edge — zeroed when this handle grabs it, so one
// press moves one box. Runs in the box's own pass (real pointer).
func (a *App) handleExtrasDrag(handle sdl.Rect, w, h int32, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
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

// handleDetachedDrag moves the i-th torn-off box by its title bar, sharing the
// per-frame press edge and the (single, one-at-a-time) grab offset.
func (a *App) handleDetachedDrag(i int, handle sdl.Rect, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
		a.extrasDetachDragging = true
		a.extrasDetachIdx = i
		a.extrasGrabDX, a.extrasGrabDY = c.mouseX-a.extrasDetached[i].x, c.mouseY-a.extrasDetached[i].y
	}
	if a.extrasDetachDragging && a.extrasDetachIdx == i {
		if !c.mouseDown {
			a.extrasDetachDragging = false
		} else {
			a.extrasDetached[i].x = c.mouseX - a.extrasGrabDX
			a.extrasDetached[i].y = c.mouseY - a.extrasGrabDY
		}
	}
}

// detachWidget tears widget id out of the grid into a new box centred under the
// cursor, and starts dragging it so it follows straight from the same gesture.
func (a *App) detachWidget(id int, mx, my int32) {
	if a.widgetDetached(id) {
		return // defensive: the grid already hides detached ids
	}
	x, y := mx-detachedBoxW/2, my-extrasTitleH/2
	a.extrasDetached = append(a.extrasDetached, detachedWidget{id: id, x: x, y: y})
	a.extrasDetachDragging = true
	a.extrasDetachIdx = len(a.extrasDetached) - 1
	a.extrasGrabDX, a.extrasGrabDY = mx-x, my-y
}

// reattachWidget closes the i-th torn-off box, returning its widget to the grid.
func (a *App) reattachWidget(i int) {
	a.extrasDetached = append(a.extrasDetached[:i], a.extrasDetached[i+1:]...)
	a.extrasDetachDragging = false
}

// extrasTearDetect starts a tear-off when grid cell id is press-dragged past the
// threshold; the plain click (release in place) is left to the cell's Button.
// Returns true once it tears — the caller must stop drawing the now-stale grid
// this frame.
func (a *App) extrasTearDetect(id int, cell sdl.Rect, pressed *bool) bool {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, cell) {
		*pressed = false
		a.extrasPressing = true
		a.extrasPressID = id
		a.extrasPressX, a.extrasPressY = c.mouseX, c.mouseY
	}
	if a.extrasPressing && a.extrasPressID == id && c.mouseDown &&
		(absInt(int(c.mouseX-a.extrasPressX)) > int(extrasTearPx) ||
			absInt(int(c.mouseY-a.extrasPressY)) > int(extrasTearPx)) {
		a.extrasPressing = false
		a.detachWidget(id, c.mouseX, c.mouseY)
		return true
	}
	return false
}

// drawFloatingExtras paints the Extras surface (main box + every torn-off box)
// on top of the live courtroom. Picking a widget runs its action but LEAVES the
// box open (non-invasive); a widget that opens its own blocking panel hides the
// surface until that panel closes, then it returns.
func (a *App) drawFloatingExtras(w, h int32) {
	if !a.extrasSurfaceLive() {
		return
	}
	if !a.showWidgets && len(a.extrasDetached) == 0 {
		return // main box closed and nothing torn off — nothing to draw
	}
	c := a.ctx
	// One mouse-press edge per frame, shared by every box so exactly one grabs
	// a given press.
	pressed := c.mouseDown && !a.extrasPrevDown
	a.extrasPrevDown = c.mouseDown
	if !c.mouseDown {
		a.extrasPressing = false // a cell press can't outlive the button
		if a.extrasDragging || a.extrasDetachDragging || a.extrasResizing || a.extrasDetachResizing {
			c.clicked = false // a finished drag/resize isn't a click on whatever's now underneath
		}
	}

	if a.showWidgets {
		a.drawExtrasMainBox(w, h, &pressed)
	}
	// Torn-off widgets persist even with the main box closed.
	a.drawExtrasDetached(w, h, &pressed)
}

// drawExtrasMainBox paints the main box and its 2-column grid of (non-detached)
// widgets, with tear-off detection per cell.
func (a *App) drawExtrasMainBox(w, h int32, pressed *bool) {
	c := a.ctx
	r := a.extrasBoxRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	// Title bar / drag handle.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: extrasTitleH}, ColPanelHi)
	c.Label(r.X+10, r.Y+6, "AsyncAO Extras", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 26, Y: r.Y + 4, W: 20, H: extrasTitleH - 8}, "x") {
		a.showWidgets = false
		if !a.extrasCloseHintShown { // tell them how to get it back — once per session
			a.extrasCloseHintShown = true
			a.warnLine = clampLine("Extras hidden — press Ctrl+" + strings.ToUpper(a.hotkeyFor(hotkeyExtras)) + " or the ★ Extras button to reopen")
			a.warnAt = a.now()
		}
		return
	}
	a.handleExtrasDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 30, H: extrasTitleH}, w, h, pressed)

	// Bottom-right resize grip. Handled before the grid so a corner press resizes
	// rather than arming a tear on the cell beneath; it sits below the grid, so
	// drawing it here doesn't overlap any cell.
	grip := sdl.Rect{X: r.X + r.W - extrasGripSz, Y: r.Y + r.H - extrasGripSz, W: extrasGripSz, H: extrasGripSz}
	a.handleExtrasResize(grip, r, pressed)
	a.drawResizeGrip(grip)

	widgets := a.extrasWidgets()
	const cols = int32(2)
	const cellH, gap = int32(34), int32(8)
	cellW := (r.W - 20 - gap) / cols
	gx, gy := r.X+10, r.Y+extrasTitleH+8
	slot := int32(0) // visible cells compact past torn-off widgets
	for id, wd := range widgets {
		if a.widgetDetached(id) {
			continue
		}
		col, row := slot%cols, slot/cols
		slot++
		br := sdl.Rect{X: gx + col*(cellW+gap), Y: gy + row*(cellH+gap), W: cellW, H: cellH}
		// Tear-off takes priority: a press-drag past the threshold pops the
		// widget out; a plain click still runs it via the Button below.
		if a.extrasTearDetect(id, br, pressed) {
			return // grid changed — stop drawing stale cells this frame
		}
		if c.Button(br, wd.label) {
			wd.run()
			return // an action can open a sub-panel / switch screen — stop here
		}
		tip := wd.desc
		if wd.key != "" {
			tip += "  ·  Ctrl+" + strings.ToUpper(a.hotkeyFor(wd.key))
		}
		c.TooltipAfter("fextra:"+wd.label, br, tip)
	}
	c.LabelClipped(r.X+10, r.Y+r.H-18, r.W-20-extrasGripSz,
		"Drag a widget out to pop it loose · drag the title to move · × closes", ColTextDim)
}

// handleExtrasResize resizes the main box from its bottom-right grip, pinning the
// top-left so only width/height grow. Shares the per-frame press edge and the
// (one-at-a-time) grab offset; extrasBoxRect clamps the result to [min, window].
func (a *App) handleExtrasResize(grip, r sdl.Rect, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, grip) {
		*pressed = false
		a.extrasResizing = true
		a.extrasBoxX, a.extrasBoxY = r.X, r.Y // pin the corner so resizing doesn't re-center
		a.extrasPlaced = true
		a.extrasGrabDX, a.extrasGrabDY = (r.X+r.W)-c.mouseX, (r.Y+r.H)-c.mouseY
	}
	if !c.mouseDown {
		a.extrasResizing = false
	}
	if a.extrasResizing {
		// Floor at the minimum here (so a far-inward drag can't drive the size
		// to ≤0, which extrasBoxRect would misread as "unset → default"); the
		// window ceiling is clamped there.
		nw, nh := (c.mouseX+a.extrasGrabDX)-r.X, (c.mouseY+a.extrasGrabDY)-r.Y
		if nw < extrasMinW {
			nw = extrasMinW
		}
		if nh < extrasMinH {
			nh = extrasMinH
		}
		a.extrasUserW, a.extrasUserH = nw, nh
	}
}

// drawResizeGrip paints a bottom-right resize handle — a small plate with accent
// nicks stepping up the diagonal — so it reads as draggable rather than blending
// into the box edge. Shared by the main box and every torn-off box.
func (a *App) drawResizeGrip(grip sdl.Rect) {
	c := a.ctx
	c.Fill(grip, ColPanelHi)
	for i := int32(0); i < 3; i++ { // dots along the bottom-right diagonal
		d := 3 + i*4
		c.Fill(sdl.Rect{X: grip.X + grip.W - d - 2, Y: grip.Y + grip.H - d - 2, W: 2, H: 2}, ColAccent)
	}
}

// handleDetachedResize resizes the i-th torn-off box from its bottom-right grip,
// pinning the top-left. Shares the per-frame press edge and the (one-at-a-time)
// grab offset; detachedBoxRect clamps the result to [min, window].
func (a *App) handleDetachedResize(i int, grip, r sdl.Rect, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, grip) {
		*pressed = false
		a.extrasDetachResizing = true
		a.extrasDetachIdx = i
		a.extrasDetached[i].x, a.extrasDetached[i].y = r.X, r.Y // pin the corner
		a.extrasGrabDX, a.extrasGrabDY = (r.X+r.W)-c.mouseX, (r.Y+r.H)-c.mouseY
	}
	if a.extrasDetachResizing && a.extrasDetachIdx == i {
		if !c.mouseDown {
			a.extrasDetachResizing = false
		} else {
			nw, nh := (c.mouseX+a.extrasGrabDX)-r.X, (c.mouseY+a.extrasGrabDY)-r.Y
			if nw < detachedMinW {
				nw = detachedMinW
			}
			if nh < detachedMinH {
				nh = detachedMinH
			}
			a.extrasDetached[i].w, a.extrasDetached[i].h = nw, nh
		}
	}
}

// drawExtrasDetached paints every torn-off widget as its own small floating box:
// a title strip that drags + closes (closing returns the widget to the grid),
// and a body button that runs the widget.
func (a *App) drawExtrasDetached(w, h int32, pressed *bool) {
	c := a.ctx
	widgets := a.extrasWidgets()
	for i := 0; i < len(a.extrasDetached); i++ {
		id := a.extrasDetached[i].id
		if id < 0 || id >= len(widgets) {
			continue
		}
		wd := widgets[id]
		r := a.detachedBoxRect(i, w, h)
		c.Fill(r, ColPanel)
		c.Border(r, ColAccent)
		// Title strip = drag handle + close. Identity lives on the body button.
		c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: extrasTitleH}, ColPanelHi)
		if c.Button(sdl.Rect{X: r.X + r.W - 24, Y: r.Y + 4, W: 18, H: extrasTitleH - 8}, "x") {
			a.reattachWidget(i)
			return // slice mutated — stop drawing this frame
		}
		a.handleDetachedDrag(i, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 28, H: extrasTitleH}, pressed)
		// Bottom-right resize grip — handled before the body button so a corner
		// press resizes the box instead of running the widget.
		grip := sdl.Rect{X: r.X + r.W - detachedGripSz, Y: r.Y + r.H - detachedGripSz, W: detachedGripSz, H: detachedGripSz}
		a.handleDetachedResize(i, grip, r, pressed)
		body := sdl.Rect{X: r.X + 8, Y: r.Y + extrasTitleH + 6, W: r.W - 16, H: r.H - extrasTitleH - 12}
		if c.Button(body, wd.label) {
			wd.run()
			return
		}
		a.drawResizeGrip(grip) // over the body's corner, so it's always visible
		c.TooltipAfter("fdetach:"+wd.label, body, wd.desc)
	}
}
