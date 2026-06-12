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

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// layoutHandlePx is the resize grip (bottom-right corner), screen px.
	layoutHandlePx = 12
	// layoutMinDesignPx floors edited widgets in design space (matches
	// the layout engine's own degenerate-rect rejection with margin).
	layoutMinDesignPx = 16
)

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
	a.editKey = ""
	a.editDrag = 0
}

// stopLayoutEdit disarms and releases the input fence.
func (a *App) stopLayoutEdit() {
	a.layoutEdit = false
	a.editKey = ""
	a.editDrag = 0
	a.ctx.modalOn = false
}

// layoutEditFence claims the pointer for the editor BEFORE the themed
// widgets draw (they see hovering()==false everywhere and stay inert).
func (a *App) layoutEditFence() {
	if a.layoutEdit {
		a.ctx.modalOn = true
		a.ctx.modalRect = sdl.Rect{} // empty: nothing hovers
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
	if themeName == "" || lay.scale <= 0 {
		a.stopLayoutEdit()
		return
	}

	// Banner + chrome (raw-hit buttons — the fence blocks kit ones).
	banner := "LAYOUT EDIT — drag moves, corner grip resizes, right-click resets a widget, Esc exits"
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: 26}, sdl.Color{R: 0, G: 0, B: 0, A: 210})
	c.Label(pad, 5, banner, ColTierYellow)
	doneBtn := sdl.Rect{X: w - 70 - pad, Y: 2, W: 70, H: 22}
	resetBtn := sdl.Rect{X: doneBtn.X - 96, Y: 2, W: 90, H: 22}
	a.rawChip(doneBtn, "Done")
	a.rawChip(resetBtn, "Reset all")

	pressed := c.mouseDown && !a.editPrev
	a.editPrev = c.mouseDown

	if c.escPressed || (c.clicked && pointIn(c.mouseX, c.mouseY, doneBtn)) {
		a.stopLayoutEdit()
		return
	}
	if c.clicked && pointIn(c.mouseX, c.mouseY, resetBtn) {
		a.d.Prefs.ClearThemeRectOverride(themeName, "")
		for k, r := range a.themeRectsOrig {
			a.themeRects[k] = r
		}
		a.themeLay.valid = false
		a.pushDebug("layout edit: all overrides reset for " + themeName)
		return
	}

	// Hover pick: the SMALLEST rect under the cursor wins (stable order
	// via sorted keys so ties don't flicker).
	keys := make([]string, 0, len(lay.r))
	for k := range lay.r {
		if !layoutEditSkip[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	hoverKey := ""
	var hoverArea int64 = 1 << 62
	for _, k := range keys {
		r := lay.r[k]
		if pointIn(c.mouseX, c.mouseY, r) {
			if area := int64(r.W) * int64(r.H); area < hoverArea {
				hoverKey, hoverArea = k, area
			}
		}
	}

	// Begin a drag on press: corner grip = resize, anywhere else = move.
	if pressed && a.editDrag == 0 && hoverKey != "" && c.mouseY > 26 {
		a.editKey = hoverKey
		r := lay.r[hoverKey]
		grip := sdl.Rect{X: r.X + r.W - layoutHandlePx, Y: r.Y + r.H - layoutHandlePx, W: layoutHandlePx, H: layoutHandlePx}
		if pointIn(c.mouseX, c.mouseY, grip) {
			a.editDrag = 2 // resize
		} else {
			a.editDrag = 1 // move
		}
		a.editStart = [2]int32{c.mouseX, c.mouseY}
		a.editBase = a.themeRects[hoverKey]
	}
	// Right-click resets the hovered widget to the theme's own rect.
	if c.rightClicked && hoverKey != "" {
		if orig, ok := a.themeRectsOrig[hoverKey]; ok {
			a.themeRects[hoverKey] = orig
			a.d.Prefs.ClearThemeRectOverride(themeName, hoverKey)
			a.themeLay.valid = false
		}
	}

	// Live drag: screen deltas map back to design space through the
	// layout scale; the cache invalidates per move (a ~40-rect rebuild).
	if a.editDrag != 0 && c.mouseDown && a.editKey != "" {
		dx := int(float64(c.mouseX-a.editStart[0]) / lay.scale)
		dy := int(float64(c.mouseY-a.editStart[1]) / lay.scale)
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
			a.d.Prefs.SetThemeRectOverride(themeName, a.editKey, [4]int{r.X, r.Y, r.W, r.H})
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
