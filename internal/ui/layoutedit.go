package ui

// Live layout editor: select a themed widget with the mouse and move it
// across the screen, or grab its corner and shrink/grow it — on the fly,
// over the running courtroom. Edits are DESIGN-space overrides persisted
// per theme (prefs.ThemeRectOverrides), applied on top of the theme's
// courtroom_design.ini whenever it loads, so window resizes keep working
// and the theme's own file is never touched.
//
// While the editor is on, a full-screen input fence (the dropdown modal
// trick with an empty rect) keeps every real widget inert; the editor
// reads raw cursor coordinates instead.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// layoutHandlePx is the resize grip (bottom-right corner), screen px.
	layoutHandlePx = 12
	// layoutMinDesignPx floors edited widgets in design space (matches
	// the layout engine's own degenerate-rect rejection with margin).
	layoutMinDesignPx = 16
	// layoutGridDesign is the snap grid in design px — edits round to it when
	// snap is on, so widgets line up cleanly.
	layoutGridDesign = 8
)

// snapDesign rounds a design-space coordinate to the nearest grid line.
func snapDesign(v int) int {
	if v < 0 {
		return 0
	}
	return (v + layoutGridDesign/2) / layoutGridDesign * layoutGridDesign
}

// layoutUndoCap bounds the editor's undo/redo stacks (rule §17.4).
const layoutUndoCap = 64

func cloneRects(m map[string]theme.Rect) map[string]theme.Rect {
	cp := make(map[string]theme.Rect, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// pushLayoutUndo snapshots the current rects BEFORE an edit and forks history
// (a fresh edit drops the redo stack).
func (a *App) pushLayoutUndo() {
	a.editUndo = append(a.editUndo, cloneRects(a.themeRects))
	if len(a.editUndo) > layoutUndoCap {
		a.editUndo = a.editUndo[1:]
	}
	a.editRedo = a.editRedo[:0]
}

// restoreLayout applies a snapshot to the live rects AND re-syncs the persisted
// overrides, so undo survives a theme reload (a key back at its original rect
// clears its override; otherwise it's re-written).
func (a *App) restoreLayout(themeName string, snap map[string]theme.Rect) {
	for k := range a.themeRects {
		r, ok := snap[k]
		if !ok {
			continue
		}
		a.themeRects[k] = r
		if r == a.themeRectsOrig[k] {
			a.d.Prefs.ClearThemeRectOverride(themeName, k)
		} else {
			a.d.Prefs.SetThemeRectOverride(themeName, k, [4]int{r.X, r.Y, r.W, r.H})
		}
	}
	a.themeLay.valid = false
}

// layoutEditSkip are rects the editor never touches: the stage frame
// itself and the chatbox-relative children (they ride the chatbox).
var layoutEditSkip = map[string]bool{
	"courtroom": true,
	"showname":  true,
	"message":   true,
}

// startLayoutEdit arms the editor (UI... panel; themed layout only).
// Open modals close — they'd be fenced shut and the editor overlay only
// draws when the themed path runs to its end.
func (a *App) startLayoutEdit() {
	a.layoutEdit = true
	a.showUICfg = false
	a.showIni, a.showEvid, a.showModcall, a.showLogin, a.showPair = false, false, false, false, false
	a.showModDash, a.banBoxKind, a.showCMPanel = false, 0, false
	a.bgPick.show = false
	a.editKey = ""
	a.editDrag = 0
	a.layoutSnap = true // tidy placement by default; toggle off in the editor
	a.editUndo, a.editRedo = nil, nil
}

// stopLayoutEdit disarms and releases the input fence.
func (a *App) stopLayoutEdit() {
	a.layoutEdit = false
	a.editKey = ""
	a.editDrag = 0
	a.ctx.modalOn = false
}

// openLayoutEditor launches the live layout editor from a menu entry (the discoverable front door),
// or flashes how to enable it when the current theme has no editable layout. The editor needs a
// theme that ships courtroom_design.ini and the theme-layout option on; on the bare default layout
// there are no editable boxes yet.
func (a *App) openLayoutEditor() {
	if a.themeLay.valid && a.d.Prefs.ThemeLayoutEnabled() {
		a.showUICfg = false
		a.startLayoutEdit()
		return
	}
	if !a.d.Prefs.ThemeLayoutEnabled() {
		a.warnLine = "Layout editor: turn on Settings → Use theme layout, then load a theme that ships a layout."
	} else {
		a.warnLine = "Layout editor: this theme has no editable layout (needs courtroom_design.ini) — try another theme."
	}
	a.warnAt = a.now()
}

// layoutEditFence claims the pointer for the editor BEFORE the themed
// widgets draw (they see hovering()==false everywhere and stay inert).
func (a *App) layoutEditFence() {
	if a.layoutEdit {
		a.ctx.modalOn = true // hovering() blanks everywhere; the editor uses raw pointIn
	}
}

// pointIn is the editor's raw hit test (hovering() is fenced on purpose).
func pointIn(x, y int32, r sdl.Rect) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// drawLayoutEditor paints the overlay and owns every interaction. Called
// LAST from the themed courtroom draw, with its layout cache.
func (a *App) drawLayoutEditor(w, h int32, lay *themeLayoutCache) {
	c := a.ctx
	themeName, _ := a.d.Prefs.Theme()
	if themeName == "" || lay.scaleX <= 0 || lay.scaleY <= 0 {
		a.stopLayoutEdit()
		return
	}

	// Banner + chrome (raw-hit buttons — the fence blocks kit ones).
	banner := "LAYOUT EDIT — drag = move, corner grip = resize, Tab = cycle overlapping boxes, right-click = reset, Ctrl+Z/Y = undo, Esc = exit"
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: 26}, sdl.Color{R: 0, G: 0, B: 0, A: 210})
	c.Label(pad, 5, banner, ColTierYellow)
	doneBtn := sdl.Rect{X: w - 70 - pad, Y: 2, W: 70, H: 22}
	resetBtn := sdl.Rect{X: doneBtn.X - 96, Y: 2, W: 90, H: 22}
	snapBtn := sdl.Rect{X: resetBtn.X - 106, Y: 2, W: 100, H: 22}
	snapLabel := "Snap: off"
	if a.layoutSnap {
		snapLabel = "Snap: on"
	}
	a.rawChip(doneBtn, "Done")
	a.rawChip(resetBtn, "Reset all")
	a.rawChip(snapBtn, snapLabel)

	pressed := c.mouseDown && !a.editPrev
	a.editPrev = c.mouseDown

	// Undo / redo (Ctrl+Z / Ctrl+Y): swap the whole rect map with a snapshot.
	if c.ctrlHeld && c.keyPressed == sdl.K_z && len(a.editUndo) > 0 {
		a.editRedo = append(a.editRedo, cloneRects(a.themeRects))
		snap := a.editUndo[len(a.editUndo)-1]
		a.editUndo = a.editUndo[:len(a.editUndo)-1]
		a.restoreLayout(themeName, snap)
		c.keyPressed = 0
	} else if c.ctrlHeld && c.keyPressed == sdl.K_y && len(a.editRedo) > 0 {
		a.editUndo = append(a.editUndo, cloneRects(a.themeRects))
		snap := a.editRedo[len(a.editRedo)-1]
		a.editRedo = a.editRedo[:len(a.editRedo)-1]
		a.restoreLayout(themeName, snap)
		c.keyPressed = 0
	}

	if c.escPressed || (c.clicked && pointIn(c.mouseX, c.mouseY, doneBtn)) {
		a.stopLayoutEdit()
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, resetBtn) {
		a.pushLayoutUndo()
		a.d.Prefs.ClearThemeRectOverride(themeName, "")
		for k, r := range a.themeRectsOrig {
			a.themeRects[k] = r
		}
		a.themeLay.valid = false
		a.pushDebug("layout edit: all overrides reset for " + themeName)
		return
	}

	if c.clicked && pointIn(c.mouseX, c.mouseY, snapBtn) {
		a.layoutSnap = !a.layoutSnap
	}

	// Editable keys (skip the design canvas + chatbox children).
	keys := make([]string, 0, len(lay.r))
	for k := range lay.r {
		if !layoutEditSkip[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	// The stack of boxes under the cursor, SMALLEST area first (stable). Tab cycles which one is
	// armed for a move, so a big box hidden under a small one is still reachable. The index resets
	// whenever the stack under the cursor changes.
	var stack []string
	for _, k := range keys {
		if pointIn(c.mouseX, c.mouseY, lay.r[k]) {
			stack = append(stack, k)
		}
	}
	sort.SliceStable(stack, func(i, j int) bool {
		ri, rj := lay.r[stack[i]], lay.r[stack[j]]
		return int64(ri.W)*int64(ri.H) < int64(rj.W)*int64(rj.H)
	})
	hoverKey := ""
	switch {
	case a.editDrag != 0:
		hoverKey = a.editKey // mid-drag: keep the grabbed box highlighted
	case len(stack) > 0:
		if sig := strings.Join(stack, "\x00"); sig != a.editPickSig {
			a.editPickSig, a.editPickIdx = sig, 0 // a new stack under the cursor
		}
		if c.keyPressed == sdl.K_TAB {
			a.editPickIdx++
			c.keyPressed = 0
		}
		a.editPickIdx %= len(stack)
		hoverKey = stack[a.editPickIdx]
	default:
		a.editPickSig, a.editPickIdx = "", 0
	}

	// Begin a drag on press. RESIZE takes priority and reaches the LARGEST box whose corner grip is
	// under the cursor — so a big box's grip can't be blocked by a small box sitting on its corner.
	// Otherwise MOVE the armed box (hoverKey, Tab-cyclable).
	if pressed && a.editDrag == 0 && c.mouseY > 26 {
		resizeKey := ""
		var gripArea int64 = -1
		for _, k := range keys {
			r := lay.r[k]
			grip := sdl.Rect{X: r.X + r.W - layoutHandlePx, Y: r.Y + r.H - layoutHandlePx, W: layoutHandlePx, H: layoutHandlePx}
			if pointIn(c.mouseX, c.mouseY, grip) {
				if area := int64(r.W) * int64(r.H); area > gripArea {
					resizeKey, gripArea = k, area
				}
			}
		}
		switch {
		case resizeKey != "":
			a.editKey, a.editDrag = resizeKey, 2 // resize
		case hoverKey != "":
			a.editKey, a.editDrag = hoverKey, 1 // move
		}
		if a.editDrag != 0 {
			a.editStart = [2]int32{c.mouseX, c.mouseY}
			a.editBase = a.themeRects[a.editKey]
			a.pushLayoutUndo() // snapshot before the move/resize (popped at release if it was a no-op)
		}
	}
	// Right-click resets the hovered widget to the theme's own rect.
	if c.rightClicked && hoverKey != "" {
		if orig, ok := a.themeRectsOrig[hoverKey]; ok {
			a.pushLayoutUndo()
			a.themeRects[hoverKey] = orig
			a.d.Prefs.ClearThemeRectOverride(themeName, hoverKey)
			a.themeLay.valid = false
		}
	}

	// Live drag: screen deltas map back to design space through the
	// layout scale; the cache invalidates per move (a ~40-rect rebuild).
	if a.editDrag != 0 && c.mouseDown && a.editKey != "" {
		dx := int(float64(c.mouseX-a.editStart[0]) / lay.scaleX)
		dy := int(float64(c.mouseY-a.editStart[1]) / lay.scaleY)
		r := a.editBase
		if a.editDrag == 1 {
			r.X += dx
			r.Y += dy
		} else {
			r.W += dx
			r.H += dy
			if r.W < layoutMinDesignPx {
				r.W = layoutMinDesignPx
			}
			if r.H < layoutMinDesignPx {
				r.H = layoutMinDesignPx
			}
		}
		if a.layoutSnap { // round to the grid so widgets line up cleanly
			if a.editDrag == 1 {
				r.X = snapDesign(r.X)
				r.Y = snapDesign(r.Y)
			} else {
				r.W = snapDesign(r.W)
				r.H = snapDesign(r.H)
				if r.W < layoutMinDesignPx {
					r.W = layoutMinDesignPx
				}
				if r.H < layoutMinDesignPx {
					r.H = layoutMinDesignPx
				}
			}
		}
		// Keep it on the stage (the engine's clamp would rescue it, but
		// editing should feel solid, not rubber-bandy).
		if court, ok := a.themeRectsOrig["courtroom"]; ok {
			if r.X < 0 {
				r.X = 0
			}
			if r.Y < 0 {
				r.Y = 0
			}
			if r.X+r.W > court.W {
				r.X = court.W - r.W
			}
			if r.Y+r.H > court.H {
				r.Y = court.H - r.H
			}
		}
		a.themeRects[a.editKey] = r
		a.themeLay.valid = false
	}
	// Release persists the edit.
	if a.editDrag != 0 && !c.mouseDown {
		if a.editKey != "" {
			r := a.themeRects[a.editKey]
			if r == a.editBase { // a click with no move: discard the begin snapshot
				if n := len(a.editUndo); n > 0 {
					a.editUndo = a.editUndo[:n-1]
				}
			} else {
				a.d.Prefs.SetThemeRectOverride(themeName, a.editKey, [4]int{r.X, r.Y, r.W, r.H})
			}
		}
		a.editDrag = 0
	}

	// Overlay: every editable rect outlined + named; selection pops.
	for _, k := range keys {
		r := lay.r[k]
		col := ColAccent
		if k == a.editKey {
			col = ColDanger
		} else if k == hoverKey {
			col = ColTierYellow
		}
		c.Border(r, col)
		c.Fill(sdl.Rect{X: r.X + r.W - layoutHandlePx, Y: r.Y + r.H - layoutHandlePx, W: layoutHandlePx, H: layoutHandlePx}, col)
		c.LabelClipped(r.X+3, r.Y+2, r.W-6, k, col)
	}
	if a.editKey != "" {
		r := a.themeRects[a.editKey]
		c.Label(pad, h-22, fmt.Sprintf("%s: x=%d y=%d w=%d h=%d (design px)", a.editKey, r.X, r.Y, r.W, r.H), ColText)
	}
	// Stacked-boxes hint: when several boxes overlap under the cursor, surface that Tab cycles them.
	if a.editDrag == 0 && len(stack) > 1 {
		c.Label(pad, h-40, fmt.Sprintf("%s — %d boxes stacked here, Tab to cycle (%d/%d)", hoverKey, len(stack), a.editPickIdx+1, len(stack)), ColTierYellow)
	}
}

// rawChip draws a button-look chip the fence can't block (raw hit test).
func (a *App) rawChip(r sdl.Rect, label string) {
	c := a.ctx
	bg := ColPanel
	if pointIn(c.mouseX, c.mouseY, r) {
		bg = ColPanelHi
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	c.LabelClipped(r.X+6, r.Y+3, r.W-12, label, ColText)
}

// applyRectOverrides lays the persisted edits for the active theme over
// a fresh design map (pollThemeApply calls this after every theme load).
func (a *App) applyRectOverrides(rects map[string]theme.Rect) map[string]theme.Rect {
	themeName, _ := a.d.Prefs.Theme()
	ov := a.d.Prefs.ThemeRectOverrides(themeName)
	if len(ov) == 0 {
		return rects
	}
	for k, v := range ov {
		if _, exists := rects[k]; exists && !layoutEditSkip[k] {
			rects[k] = theme.Rect{X: v[0], Y: v[1], W: v[2], H: v[3]}
		}
	}
	return rects
}
