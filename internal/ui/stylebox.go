package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// motionName labels a transmitted sprite-motion path for the cycle button (#34).
func motionName(m uint8) string {
	switch m {
	case courtroom.MotionOrbit:
		return "Move: Orbit"
	case courtroom.MotionBounce:
		return "Move: Bounce"
	case courtroom.MotionSway:
		return "Move: Sway"
	case courtroom.MotionDrift:
		return "Move: Drift"
	default:
		return "Move: None"
	}
}

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
	styleBoxMinW    = int32(232) // floor for the right-edge width resize (swatches + sliders still fit)
	styleGripW      = int32(8)   // right-edge resize-grip hit width (clear of every control)
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
		bh += rows*(styleSwatchSz+styleSwatchGap) + 4 + 62 // preset swatches + R/G/B sliders
	}
	bh += 30 // opacity (Fade)
	bh += 26 // Rainbow / Mirror row
	bh += 66 // Brightness / Size / Tilt sliders
	bh += 26 // glow / wobble / spin row
	bh += 26 // invert / grayscale / sepia row
	bh += 26 // posterize row (#34)
	bh += 30 // movement-path cycle (#34)
	bh += 30 // clear button
	// #126 presets: a header, a Save row, and one row per saved mood.
	bh += 22 + 28 + int32(a.d.Prefs.StylePresetCount())*26
	bh += styleBoxPad
	bw := styleBoxW
	if a.styleBoxUserW > 0 {
		bw = a.styleBoxUserW // user-dragged width (the sliders / notes follow r.W)
	}
	if bw < styleBoxMinW {
		bw = styleBoxMinW
	}
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
		y += 4
		// Fine RGB control.
		swW := r.W - 24 - styleBoxPad*2
		nr := c.Slider("styleR", sdl.Rect{X: x + 22, Y: y, W: swW, H: 14}, int32(p.R), 255)
		c.Label(x, y, "R", ColTextDim)
		y += 20
		ng := c.Slider("styleG", sdl.Rect{X: x + 22, Y: y, W: swW, H: 14}, int32(p.G), 255)
		c.Label(x, y, "G", ColTextDim)
		y += 20
		nb := c.Slider("styleB", sdl.Rect{X: x + 22, Y: y, W: swW, H: 14}, int32(p.B), 255)
		c.Label(x, y, "B", ColTextDim)
		y += 22
		if nr != int32(p.R) || ng != int32(p.G) || nb != int32(p.B) {
			p.R, p.G, p.B = uint8(nr), uint8(ng), uint8(nb)
			a.d.Prefs.SetSpriteStyle(p)
		}
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

	// Rainbow (transmitted hue cycle) + Mirror.
	if next := c.Checkbox(x, y, "Rainbow", p.HueCycle); next != p.HueCycle {
		p.HueCycle = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+110, y, "Mirror", p.FlipH); next != p.FlipH {
		p.FlipH = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26

	// Brightness / Size / Tilt sliders.
	slX, slW := x+56, r.W-56-styleBoxPad*2
	br := int32(p.Brightness)
	if br == 0 {
		br = 100
	}
	c.Label(x, y+1, "Bright", ColTextDim)
	if n := c.Slider("styleBright", sdl.Rect{X: slX, Y: y, W: slW, H: 14}, br, 200); n != br {
		if n < 20 {
			n = 20
		}
		p.Brightness = uint8(n)
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 22
	sc := int32(p.Scale)
	if sc == 0 {
		sc = 100
	}
	c.Label(x, y+1, "Size", ColTextDim)
	if n := c.Slider("styleScale", sdl.Rect{X: slX, Y: y, W: slW, H: 14}, sc, 150); n != sc {
		if n < 50 {
			n = 50
		}
		p.Scale = uint8(n)
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 22
	tilt := int32(int(p.Rotation) * 360 / 256)
	c.Label(x, y+1, "Tilt", ColTextDim)
	if n := c.Slider("styleTilt", sdl.Rect{X: slX, Y: y, W: slW, H: 14}, tilt, 359); n != tilt {
		p.Rotation = uint8(n * 256 / 360)
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26

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

	// Invert / Grayscale — per-pixel effects (the renderer builds a cached variant
	// texture; the recolour/glow above still compose on top).
	if next := c.Checkbox(x, y, "Invert", p.Invert); next != p.Invert {
		p.Invert = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+86, y, "Grayscale", p.Grayscale); next != p.Grayscale {
		p.Grayscale = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+186, y, "Sepia", p.Sepia); next != p.Sepia { // #34 warm brown tone
		p.Sepia = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26
	if next := c.Checkbox(x, y, "Posterize", p.Posterize); next != p.Posterize { // #34 poster / cel look
		p.Posterize = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26
	// Movement path (#34): a transmitted looping motion — None → Orbit → Bounce → Sway →
	// Drift. Other AsyncAO players see your sprite follow it; it stacks with the effects
	// above. The viewer's ReduceMotion suppresses it (accessibility).
	mb := sdl.Rect{X: x, Y: y, W: r.W - styleBoxPad*2, H: btnH}
	if c.Button(mb, motionName(p.Motion)) {
		p.Motion = (p.Motion + 1) % courtroom.MotionCount
		a.d.Prefs.SetSpriteStyle(p)
	}
	c.Tooltip(mb, "A looping movement path your sprite follows on the viewport — orbit, bounce, sway or drift. Transmitted to other AsyncAO players; stacks with the colour/glow effects above.")
	y += 30

	// Outline / drop-shadow (#8) — silhouette effects drawn behind the sprite, transmitted.
	if next := c.Checkbox(x, y, "Outline", p.Outline); next != p.Outline {
		p.Outline = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	if next := c.Checkbox(x+86, y, "Shadow", p.DropShadow); next != p.DropShadow {
		p.DropShadow = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26
	if next := c.Checkbox(x, y, "Glitch", p.Glitch); next != p.Glitch { // #13 chromatic aberration
		p.Glitch = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	y += 26

	if c.Button(sdl.Rect{X: x, Y: y, W: r.W - styleBoxPad*2, H: btnH}, "Clear style") {
		a.d.Prefs.SetSpriteStyle(config.SpriteStylePref{})
	}
	y += btnH + 8
	a.drawStylePresets(c, x, y, r.W-styleBoxPad*2) // #126 saved moods + keybinds

	// Right-edge width grip: drag to widen the box (the notes / sliders / buttons
	// all follow r.W). Height stays content-driven. The hit strip sits in the right
	// margin, clear of every control (they stop at r.W - styleBoxPad). Handled with
	// the box's shared press edge, like the title-bar drag.
	grip := sdl.Rect{X: r.X + r.W - styleGripW, Y: r.Y + extrasTitleH, W: styleGripW, H: r.H - extrasTitleH}
	a.handleStyleResize(grip, r, pressed)
	for i := int32(0); i < 3; i++ { // three nubs down the right-edge centre so it reads as draggable
		c.Fill(sdl.Rect{X: r.X + r.W - 4, Y: r.Y + r.H/2 - 8 + i*8, W: 2, H: 3}, ColAccent)
	}
	c.Tooltip(grip, "Drag to resize the box width")
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

// handleStyleResize widens the box from its right-edge grip (height stays
// content-driven). Shares the Extras surface's press edge + the box's grab
// offset; styleBoxRect clamps the result to [styleBoxMinW, window].
func (a *App) handleStyleResize(grip, r sdl.Rect, pressed *bool) {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, grip) {
		*pressed = false
		a.styleBoxResizing = true
		a.styleBoxX, a.styleBoxY = r.X, r.Y // pin the corner so the box doesn't re-default
		a.styleBoxPlaced = true
		a.styleBoxGrabDX = (r.X + r.W) - c.mouseX
	}
	if !c.mouseDown {
		a.styleBoxResizing = false
	}
	if a.styleBoxResizing {
		nw := (c.mouseX + a.styleBoxGrabDX) - r.X
		if nw < styleBoxMinW {
			nw = styleBoxMinW
		}
		a.styleBoxUserW = nw // styleBoxRect clamps the window ceiling
	}
}
