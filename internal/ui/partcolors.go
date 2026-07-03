package ui

// Per-part layout colours (v1.52.0, Tifera: "color individual parts of the
// layout"). Each big courtroom panel — the log column, the OOC box, the emote
// grid, the chatbox — can carry its own background tint instead of the global
// chrome Panel colour. Overrides live in prefs as hex strings (blank = chrome
// default) and are parsed ONCE into an App-local cache, so the draw sites pay
// an array read per frame, never a parse or a pref lock.

import (
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

// Part indices into the pref array (config.LayoutPartColorCount pins the len).
const (
	partLog     = 0 // the log / right column panel
	partOOC     = 1 // the OOC box
	partEmotes  = 2 // the emote grid (default has NO backing — a tint adds one)
	partChatbox = 3 // the in-stage chatbox (base colour under opacity + speaker tint)
)

// partColorLabels are the Settings row names, indexed like the pref array.
var partColorLabels = [config.LayoutPartColorCount]string{"IC log / right column", "OOC box", "Emote grid", "Chatbox"}

// refreshPartColors re-parses the pref hexes into the draw cache. Called at
// launch and after every Settings edit — never per frame.
func (a *App) refreshPartColors() {
	hex := a.d.Prefs.LayoutPartColors()
	for i := range hex {
		col, ok := parseHexColor(hex[i])
		a.partColorOn[i] = ok
		if ok {
			a.partColors[i] = col
		}
	}
}

// partPanel reports part i's tint override (cache read, allocation-free).
func (a *App) partPanel(i int) (sdl.Color, bool) {
	return a.partColors[i], a.partColorOn[i]
}

// partPanelOr is partPanel with a fallback — the common fill-site shape.
func (a *App) partPanelOr(i int, def sdl.Color) sdl.Color {
	if col, ok := a.partPanel(i); ok {
		return col
	}
	return def
}

// drawPartColorSettings is the Settings → Theme "Layout part colours" section:
// one row per part — swatch, name, Edit (opens the shared wheel on that part)
// and Reset. Returns the next y. Settings-only; never a hot path.
func (a *App) drawPartColorSettings(y int32) int32 {
	c := a.ctx
	pad := a.formX
	c.Label(pad, y, "Layout part colours — tint each courtroom panel separately (blank = the chrome colour).", ColTextDim)
	y += 24

	for i := range partColorLabels {
		sw := sdl.Rect{X: pad, Y: y, W: 26, H: 22}
		cur, on := a.partPanel(i)
		if !on {
			cur = ColPanel // preview the chrome default the part would use
		}
		c.Fill(sw, cur)
		ring := ColTextDim
		if i == a.partEditSlot {
			ring = ColAccent
		}
		c.Border(sw, ring)
		c.Label(pad+34, y+3, partColorLabels[i], ColText)
		editR := sdl.Rect{X: pad + 220, Y: y, W: 54, H: 22}
		if c.Button(editR, "Edit") {
			a.partEditSlot = i
		}
		if on {
			resetR := sdl.Rect{X: editR.X + editR.W + 6, Y: y, W: 60, H: 22}
			if c.Button(resetR, "Reset") {
				a.d.Prefs.SetLayoutPartColor(i, "")
				a.refreshPartColors()
			}
		}
		y += 28
	}

	// The shared wheel edits the selected part (same feel as the custom
	// chrome editor above it).
	sel := a.partEditSlot
	if sel < 0 || sel >= len(partColorLabels) {
		sel = 0
		a.partEditSlot = 0
	}
	c.Label(pad, y, "Editing: "+partColorLabels[sel], ColAccent)
	y += 22
	cur, on := a.partPanel(sel)
	if !on {
		cur = ColPanel
	}
	y = a.wheelHexEditor(y, "parthex", &a.partHexBuf, cur, func(col sdl.Color) {
		a.d.Prefs.SetLayoutPartColor(sel, fmt.Sprintf("%02x%02x%02x", col.R, col.G, col.B))
		a.refreshPartColors()
	})
	return y + 8
}
