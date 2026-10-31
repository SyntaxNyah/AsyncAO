package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// floatWin is a movable, resizable, non-blocking floating window; geometry is per-window session state.
// While the cursor is over one, boxFencesPointer blocks the courtroom's pointer, leaving it usable everywhere else.
type floatWin struct {
	x, y, w, h     int32 // current rect; w/h ≤ 0 = use the caller's default size
	placed         bool  // dragged/resized at least once (else centered default)
	deSeeded       bool  // de-overlap seed pass ran once for this open (A1 Phase 2)
	dragging       bool
	resizing       bool
	grabDX, grabDY int32
	// Last drawn size and window dims, stamped by rect() each frame, so floatWinDrag can snap to the screen edges.
	lastW, lastH, lastWinW, lastWinH int32
}

const (
	floatWinMargin = 8  // keep a window this far inside the window edges
	floatGripSz    = 16 // bottom-right resize grip size
	floatTitleH    = 30 // title bar / drag-handle height
	floatSnapPx    = 12 // a dragged edge within this many px of a screen edge / centre snaps to it
)

// rect clamps the window on-screen, using the default size and a centered position until first placed.
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
	w, h = value.Clamp(w, minW, hiW), value.Clamp(h, minH, hiH)
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
	return sdl.Rect{X: value.Clamp(x, floatWinMargin, maxX), Y: value.Clamp(y, floatWinMargin, maxY), W: w, H: h}
}

// snapToEdges nudges a dragging window to screen edges or centre within floatSnapPx; a no-op until rect() has run once.
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

// floatWinDrag moves a window by its handle; pressed is zeroed when grabbed, so one press moves one window.
func (a *App) floatWinDrag(fw *floatWin, handle sdl.Rect, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
		fw.dragging = true
		fw.grabDX, fw.grabDY = c.mouseX-handle.X, c.mouseY-handle.Y
	}
	if !c.mouseDown {
		if fw.dragging && fw.lastWinW > 0 && fw.lastWinH > 0 {
			// On release, cascade off a sibling it was dropped nearly on top of; fw.dragging is still true, so the
			// search skips this window.
			r := sdl.Rect{X: fw.x, Y: fw.y, W: fw.lastW, H: fw.lastH}
			nr := deOverlapRect(r, a.panelDeOverlapCensus(r, fw.lastWinW, fw.lastWinH), fw.lastWinW, fw.lastWinH)
			fw.x, fw.y = nr.X, nr.Y
		}
		fw.dragging = false
	}
	if fw.dragging {
		fw.x, fw.y = c.mouseX-fw.grabDX, c.mouseY-fw.grabDY
		fw.snapToEdges() // #21: snap to screen edges / centre while dragging
		if !a.siblingMagnetOff() {
			a.snapToSiblings(fw) // M3: additive piece-to-piece magnet (Shift or the Magnet chip bypasses)
		}
		fw.placed = true
	}
}

// floatWinResize grows from the bottom-right grip. Floored here: a size <= 0 would read in rect as "unset, use
// default".
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
