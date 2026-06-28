package ui

import "github.com/veandco/go-sdl2/sdl"

// floatWin is a generic movable + resizable, non-blocking floating window — the
// pattern the Extras box pioneered, factored out so the Pair menu and the Mod/CM
// panel can reuse it instead of being blocking modals. Geometry is session state
// (one position per window, shared across tabs); while the cursor is over an open
// floatWin the courtroom pass fences the pointer (boxFencesPointer), so the
// courtroom behind stays interactive everywhere else — you can still chat with
// one of these open.
type floatWin struct {
	x, y, w, h     int32 // current rect; w/h ≤ 0 = use the caller's default size
	placed         bool  // dragged/resized at least once (else centered default)
	dragging       bool
	resizing       bool
	grabDX, grabDY int32
	// Last drawn size + the window dims rect() saw — so floatWinDrag can snap to the screen edges
	// (#21) without every caller threading them through. Stamped by rect() each frame.
	lastW, lastH, lastWinW, lastWinH int32
}

const (
	floatWinMargin = 8  // keep a window this far inside the window edges
	floatGripSz    = 16 // bottom-right resize grip size
	floatTitleH    = 30 // title bar / drag-handle height
	floatSnapPx    = 12 // a dragged edge within this many px of a screen edge / centre snaps to it
)

// rect clamps the window on-screen, using the default size until first placed and
// a centered default position. Mirrors extrasBoxRect's clamping so a resize or a
// shrunk window can never strand it off-edge or oversize it.
func (fw *floatWin) rect(defW, defH, minW, minH, winW, winH int32) sdl.Rect {
	w, h := fw.w, fw.h
	if w <= 0 {
		w = defW
	}
	if h <= 0 {
		h = defH
	}
	hiW, hiH := winW-2*floatWinMargin, winH-2*floatWinMargin
	if hiW < minW {
		hiW = minW
	}
	if hiH < minH {
		hiH = minH
	}
	w, h = clampI32(w, minW, hiW), clampI32(h, minH, hiH)
	x, y := fw.x, fw.y
	if !fw.placed {
		x, y = (winW-w)/2, (winH-h)/2
	}
	maxX, maxY := winW-w-floatWinMargin, winH-h-floatWinMargin
	if maxX < floatWinMargin {
		maxX = floatWinMargin
	}
	if maxY < floatWinMargin {
		maxY = floatWinMargin
	}
	fw.lastW, fw.lastH, fw.lastWinW, fw.lastWinH = w, h, winW, winH // for drag-snap (#21)
	return sdl.Rect{X: clampI32(x, floatWinMargin, maxX), Y: clampI32(y, floatWinMargin, maxY), W: w, H: h}
}

// snapToEdges nudges a dragging window's top-left to the screen edges or centre when within
// floatSnapPx, using the size/window dims rect() last stamped (#21). Two windows snapped to the
// same edge thereby line up with each other. No-op until rect() has run once (lastWinW/H == 0).
func (fw *floatWin) snapToEdges() {
	w, h, winW, winH := fw.lastW, fw.lastH, fw.lastWinW, fw.lastWinH
	if winW <= 0 || winH <= 0 {
		return
	}
	near := func(a, b int32) bool { return a-b < floatSnapPx && b-a < floatSnapPx }
	switch {
	case near(fw.x, floatWinMargin):
		fw.x = floatWinMargin
	case near(fw.x+w, winW-floatWinMargin):
		fw.x = winW - floatWinMargin - w
	case near(fw.x+w/2, winW/2):
		fw.x = (winW - w) / 2
	}
	switch {
	case near(fw.y, floatWinMargin):
		fw.y = floatWinMargin
	case near(fw.y+h, winH-floatWinMargin):
		fw.y = winH - floatWinMargin - h
	case near(fw.y+h/2, winH/2):
		fw.y = (winH - h) / 2
	}
}

// floatWinDrag moves a window by its title-bar handle. pressed is the shared
// per-frame press edge — zeroed when this window grabs it, so one press moves one
// window. Runs in the box pass (real pointer).
func (a *App) floatWinDrag(fw *floatWin, handle sdl.Rect, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
		fw.dragging = true
		fw.grabDX, fw.grabDY = c.mouseX-handle.X, c.mouseY-handle.Y
	}
	if !c.mouseDown {
		fw.dragging = false
	}
	if fw.dragging {
		fw.x, fw.y = c.mouseX-fw.grabDX, c.mouseY-fw.grabDY
		fw.snapToEdges() // #21: snap to screen edges / centre while dragging
		fw.placed = true
	}
}

// floatWinResize grows a window from its bottom-right grip, pinning the top-left.
// Floors at min here so a far-inward drag can't drive the size to ≤0 (which rect
// would misread as "unset → default"); rect clamps the ceiling.
func (a *App) floatWinResize(fw *floatWin, grip, r sdl.Rect, minW, minH int32, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, grip) {
		*pressed = false
		fw.resizing = true
		fw.x, fw.y, fw.placed = r.X, r.Y, true // pin the corner so resizing doesn't re-center
		fw.grabDX, fw.grabDY = (r.X+r.W)-c.mouseX, (r.Y+r.H)-c.mouseY
	}
	if !c.mouseDown {
		fw.resizing = false
	}
	if fw.resizing {
		nw, nh := (c.mouseX+fw.grabDX)-r.X, (c.mouseY+fw.grabDY)-r.Y
		if nw < minW {
			nw = minW
		}
		if nh < minH {
			nh = minH
		}
		fw.w, fw.h = nw, nh
	}
}
