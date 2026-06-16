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
	extrasBoxW   = int32(380) // main box width
	extrasBoxH   = int32(360) // main box height
	extrasTitleH = int32(26)  // title bar / drag handle height (main + torn boxes)

	detachedBoxW = int32(176) // a torn-off widget's own little box
	detachedBoxH = int32(66)
	extrasTearPx = int32(8) // drag a grid cell this far to tear it loose
)

// extrasWidget is one entry in the Extras box: a labelled action you click to
// run or drag out into its own floating box.
type extrasWidget struct {
	label, desc, key string // key = hotkey id ("" = none), surfaced in the tooltip
	run              func()
}

// detachedWidget is a widget torn out into its own box at (x,y). id indexes the
// canonical extrasWidgets table; (x,y) is the raw (pre-clamp) top-left.
type detachedWidget struct {
	id   int
	x, y int32
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

// extrasBoxVisible reports whether the floating Extras surface should draw:
// opened, in a live courtroom, and not shadowed by a blocking popup.
func (a *App) extrasBoxVisible() bool {
	return a.showWidgets && a.room != nil && a.sess != nil && !a.courtModalOpen()
}

// extrasBoxRect is the main box's screen rect: the dragged position once placed,
// else a centered-near-the-top default. Always clamped fully on-screen so a
// resize or a moved-then-shrunk window can't strand it off-edge.
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

// detachedBoxRect is the i-th torn-off widget's screen rect, clamped on-screen.
func (a *App) detachedBoxRect(i int, w, h int32) sdl.Rect {
	d := a.extrasDetached[i]
	maxX, maxY := w-detachedBoxW-4, h-detachedBoxH-4
	if maxX < 4 {
		maxX = 4
	}
	if maxY < 4 {
		maxY = 4
	}
	return sdl.Rect{X: clampI32(d.x, 4, maxX), Y: clampI32(d.y, 4, maxY), W: detachedBoxW, H: detachedBoxH}
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
// this frame: any Extras box is up under the cursor, or a box drag is in flight
// (so a fast drag can't leak a click to the scene between frames).
func (a *App) boxFencesPointer(w, h int32) bool {
	if !a.extrasBoxVisible() {
		return false
	}
	if a.extrasDragging || a.extrasDetachDragging || a.extrasPressing {
		return true
	}
	mx, my := a.ctx.mouseX, a.ctx.mouseY
	if pointIn(mx, my, a.extrasBoxRect(w, h)) {
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
	if !a.extrasBoxVisible() {
		return
	}
	c := a.ctx
	// One mouse-press edge per frame, shared by every box so exactly one grabs
	// a given press.
	pressed := c.mouseDown && !a.extrasPrevDown
	a.extrasPrevDown = c.mouseDown
	if !c.mouseDown {
		a.extrasPressing = false // a cell press can't outlive the button
		if a.extrasDragging || a.extrasDetachDragging {
			c.clicked = false // a finished drag isn't a click on whatever's now underneath
		}
	}

	a.drawExtrasMainBox(w, h, &pressed)
	if !a.showWidgets {
		return // a widget's × closed the surface this frame
	}
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
		return
	}
	a.handleExtrasDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 30, H: extrasTitleH}, w, h, pressed)

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
	c.LabelClipped(r.X+10, r.Y+r.H-18, r.W-20,
		"Drag a widget out to pop it loose · drag the title to move · × closes", ColTextDim)
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
		body := sdl.Rect{X: r.X + 8, Y: r.Y + extrasTitleH + 6, W: r.W - 16, H: r.H - extrasTitleH - 12}
		if c.Button(body, wd.label) {
			wd.run()
			return
		}
		c.TooltipAfter("fdetach:"+wd.label, body, wd.desc)
	}
}
