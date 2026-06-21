package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// minVisibleStyleOpacity floors the Fade slider so a style can't go invisible. It
// mirrors courtroom.minVisibleOpacity (the render side floors it too); kept here
// because config/courtroom consts aren't exported.
const minVisibleStyleOpacity = 25

// The floating Sprite Style box (#104): the non-intrusive, draggable cousin of
// the Extras box for the transmitted Sprite Style (#103). It floats ON TOP of the
// live courtroom so you can recolour / glow / fade your character ON THE FLY
// without leaving the chat — the scene, IC input and logs stay live underneath
// (it shares the Extras surface's pointer fence + single press edge). Opened from
// the Extras "Sprite Style" entry; drawn only while open, so it's free when shut.
// Every control writes the sticky pref immediately, so a change shows on your very
// next message. Session-only open + position (a quick tool, not persisted chrome).

const (
	styleBoxW       = int32(268)
	styleBoxPad     = int32(10)
	styleSwatchSz   = int32(26)
	styleSwatchGap  = int32(6)
	styleSwatchCols = 5
)

// styleBoxRect is the box's screen rect: a fixed width, a height that grows when
// the tint controls are showing, clamped fully on-screen at its dragged position
// (else a default tucked under the top-right, clear of the stage centre).
func (a *App) styleBoxRect(w, h int32) sdl.Rect {
	bh := extrasTitleH + styleBoxPad // title + top pad
	bh += 50                         // 3-line "what it does / who sees it" note
	bh += 26                         // Tint row
	if a.d.Prefs.SpriteStyle().Tint {
		rows := int32((len(spriteStylePresets) + styleSwatchCols - 1) / styleSwatchCols)
		bh += rows*(styleSwatchSz+styleSwatchGap) + 6 // preset swatches
	}
	bh += 30 // opacity
	bh += 26 // glow / wobble / spin row
	bh += 30 // clear button
	bh += styleBoxPad
	bw := styleBoxW
	if bw > w-16 {
		bw = w - 16
	}
	if bh > h-16 {
		bh = h - 16
	}
	x, y := a.styleBoxX, a.styleBoxY
	if !a.styleBoxPlaced {
		x, y = w-bw-24, 96
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

// drawSpriteStyleBox paints the floating Sprite Style panel and handles its input.
// pressed is the Extras surface's shared press edge (the title bar consumes it for
// dragging). Mirrors drawFavEmoteBox's frame.
func (a *App) drawSpriteStyleBox(w, h int32, pressed *bool) {
	c := a.ctx
	p := a.d.Prefs.SpriteStyle()
	r := a.styleBoxRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)

	// Title bar / drag handle + close.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: extrasTitleH}, ColPanelHi)
	c.Label(r.X+8, r.Y+6, "Sprite Style", ColText)
	// Live swatch in the title bar (white = no tint).
	swCol := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	if p.Tint {
		swCol = sdl.Color{R: p.R, G: p.G, B: p.B, A: 255}
	}
	sw := sdl.Rect{X: r.X + r.W - 52, Y: r.Y + 5, W: 22, H: extrasTitleH - 10}
	c.Fill(sw, swCol)
	c.Border(sw, ColBackground)
	if c.Button(sdl.Rect{X: r.X + r.W - 24, Y: r.Y + 4, W: 18, H: extrasTitleH - 8}, "x") {
		a.showStyleBox = false
		return
	}
	a.handleStyleBoxDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 56, H: extrasTitleH}, w, h, pressed)

	x := r.X + styleBoxPad
	y := r.Y + extrasTitleH + 6

	// What it does + how others see it (the user asked for this in the box).
	noteW := r.W - styleBoxPad*2
	c.LabelClipped(x, y, noteW, "Restyle YOUR own character live.", ColTextDim)
	y += 16
	c.LabelClipped(x, y, noteW, "Other AsyncAO players see your colours;", ColTextDim)
	y += 15
	c.LabelClipped(x, y, noteW, "AO2 / webAO see a normal character.", ColTextDim)
	y += 19

	// Recolour toggle.
	if next := c.Checkbox(x, y, "Recolour (tint)", p.Tint); next != p.Tint {
		p.Tint = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26

	if p.Tint {
		// Preset swatches — click to set the colour.
		px, col := x, 0
		for _, pr := range spriteStylePresets {
			sq := sdl.Rect{X: px, Y: y, W: styleSwatchSz, H: styleSwatchSz}
			c.Fill(sq, sdl.Color{R: pr.r, G: pr.g, B: pr.b, A: 255})
			if p.R == pr.r && p.G == pr.g && p.B == pr.b {
				c.Border(sq, ColText) // highlight the active preset
			} else {
				c.Border(sq, ColBackground)
			}
			if c.clicked && c.hovering(sq) {
				p.R, p.G, p.B = pr.r, pr.g, pr.b
				a.d.Prefs.SetSpriteStyle(p)
			}
			col++
			if col >= styleSwatchCols {
				col, px = 0, x
				y += styleSwatchSz + styleSwatchGap
			} else {
				px += styleSwatchSz + styleSwatchGap
			}
		}
		if col != 0 {
			y += styleSwatchSz + styleSwatchGap
		}
		y += 6
	}

	// Opacity (25..100; below the floor renders at the floor, never invisible).
	op := int32(p.Opacity)
	if op == 0 {
		op = 100
	}
	c.Label(x, y+2, "Fade", ColTextDim)
	if nop := c.Slider("styleOpacity", sdl.Rect{X: x + 44, Y: y, W: r.W - 44 - styleBoxPad*2, H: 16}, op, 100); nop != op {
		switch {
		case nop >= 100:
			p.Opacity = 0 // opaque
		case nop < minVisibleStyleOpacity:
			p.Opacity = minVisibleStyleOpacity
		default:
			p.Opacity = uint8(nop)
		}
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 30

	// Glow / Wobble / Spin.
	if next := c.Checkbox(x, y, "Glow", p.Glow); next != p.Glow {
		p.Glow = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+86, y, "Wobble", p.Wobble); next != p.Wobble {
		p.Wobble = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+176, y, "Spin", p.Spin); next != p.Spin {
		p.Spin = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26

	if c.Button(sdl.Rect{X: x, Y: y, W: r.W - styleBoxPad*2, H: btnH}, "Clear style") {
		a.d.Prefs.SetSpriteStyle(config.SpriteStylePref{})
	}
}

// handleStyleBoxDrag moves the box by its title bar, mirroring handleFavBoxDrag.
func (a *App) handleStyleBoxDrag(handle sdl.Rect, w, h int32, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
		r := a.styleBoxRect(w, h)
		a.styleBoxDragging = true
		a.styleBoxGrabDX, a.styleBoxGrabDY = c.mouseX-r.X, c.mouseY-r.Y
	}
	if !c.mouseDown {
		a.styleBoxDragging = false
	}
	if a.styleBoxDragging {
		a.styleBoxX, a.styleBoxY = c.mouseX-a.styleBoxGrabDX, c.mouseY-a.styleBoxGrabDY
		a.styleBoxPlaced = true
	}
}
