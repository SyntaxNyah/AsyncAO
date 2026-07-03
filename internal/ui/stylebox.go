package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// stylePathBox is the side of the draw-a-path square (#34).
const stylePathBox = int32(112)

// pathCellCenter maps a packed 4-bit X/Y waypoint to the centre of its cell inside box.
func pathCellCenter(p byte, box sdl.Rect) (x, y int32) {
	return box.X + int32(p>>4)*box.W/16 + box.W/32, box.Y + int32(p&0x0F)*box.H/16 + box.H/32
}

// pathStrokeCap bounds the freehand stroke buffer (rule §17.4).
const pathStrokeCap = 256

// drawStylePathEditor draws the "draw your own motion path" box (#34, B2): DRAG inside it to
// sketch a loop (sampled to up to 16 waypoints on release), or CLICK to drop a single waypoint;
// centre = the sprite's rest spot. Your sprite loops through the path — overriding the Move
// cycle — transmitted + stacking. Shows the live stroke / saved path; a Clear button removes it.
func (a *App) drawStylePathEditor(x, y int32, p config.SpriteStylePref) int32 {
	c := a.ctx
	c.Label(x, y, "Or draw a custom path (drag to sketch · click to add a point · centre = rest):", ColTextDim)
	y += 18
	box := sdl.Rect{X: x, Y: y, W: stylePathBox, H: stylePathBox}
	c.Fill(box, ColPanel)
	c.Border(box, ColAccent)
	c.Fill(sdl.Rect{X: box.X + box.W/2 - 1, Y: box.Y + 4, W: 2, H: box.H - 8}, ColPanelHi) // crosshair = rest
	c.Fill(sdl.Rect{X: box.X + 4, Y: box.Y + box.H/2 - 1, W: box.W - 8, H: 2}, ColPanelHi)

	inBox := pointIn(c.mouseX, c.mouseY, box)
	press := c.mouseDown && !a.pathPrevDown
	a.pathPrevDown = c.mouseDown
	if press && inBox { // begin a stroke
		a.pathDrawing, a.pathStroke = true, a.pathStroke[:0]
	}
	if a.pathDrawing && c.mouseDown && len(a.pathStroke) < pathStrokeCap {
		a.pathStroke = append(a.pathStroke, sdl.Point{
			X: int32(clampInt(int(c.mouseX), int(box.X), int(box.X+box.W))),
			Y: int32(clampInt(int(c.mouseY), int(box.Y), int(box.Y+box.H))),
		})
	}
	if a.pathDrawing && !c.mouseDown { // release: a real drag samples a new path; a tap adds one point
		a.pathDrawing = false
		switch {
		case strokeMoved(a.pathStroke):
			p.Path, p.PathLen = samplePathStroke(a.pathStroke, box)
			a.d.Prefs.SetSpriteStyle(p)
		case inBox && int(p.PathLen) < len(p.Path):
			gx := uint8(clampInt(int((c.mouseX-box.X)*16/box.W), 0, 15))
			gy := uint8(clampInt(int((c.mouseY-box.Y)*16/box.H), 0, 15))
			p.Path[p.PathLen] = gx<<4 | gy
			p.PathLen++
			a.d.Prefs.SetSpriteStyle(p)
		}
		a.pathStroke = a.pathStroke[:0]
	}

	// The live stroke while sketching, else the saved path (dots + connecting loop).
	if a.pathDrawing && len(a.pathStroke) > 1 {
		_ = c.Ren.SetDrawColor(ColTierGreen.R, ColTierGreen.G, ColTierGreen.B, 255)
		for i := 1; i < len(a.pathStroke); i++ {
			_ = c.Ren.DrawLine(a.pathStroke[i-1].X, a.pathStroke[i-1].Y, a.pathStroke[i].X, a.pathStroke[i].Y)
		}
	} else if n := int(p.PathLen); n >= 1 {
		_ = c.Ren.SetDrawColor(ColAccent.R, ColAccent.G, ColAccent.B, 255)
		for i := 0; i < n; i++ {
			ax, ay := pathCellCenter(p.Path[i], box)
			if n >= 2 {
				bx, by := pathCellCenter(p.Path[(i+1)%n], box)
				_ = c.Ren.DrawLine(ax, ay, bx, by)
			}
			c.Fill(sdl.Rect{X: ax - 3, Y: ay - 3, W: 6, H: 6}, ColTierGreen)
		}
	}
	rx := box.X + box.W + 8
	if c.Button(sdl.Rect{X: rx, Y: box.Y, W: 84, H: btnH}, "Clear path") {
		p.Path, p.PathLen = [16]uint8{}, 0
		a.d.Prefs.SetSpriteStyle(p)
	}
	if p.PathLen > 0 { // undo the last waypoint (handy when click-building a path point by point)
		if c.Button(sdl.Rect{X: rx, Y: box.Y + btnH + 6, W: 84, H: btnH}, "Undo point") {
			p.PathLen--
			p.Path[p.PathLen] = 0 // zero the freed slot so equal paths stay == (the pref is compared by value)
			a.d.Prefs.SetSpriteStyle(p)
		}
	}
	ly := box.Y + 2*(btnH+6)
	c.Label(rx, ly, "Up to 16 points;", ColTextDim)
	c.Label(rx, ly+16, "loops forever.", ColTextDim)
	return y + stylePathBox + 8
}

// strokeMoved reports whether a freehand stroke is a real drag (vs a click/tap): its bounding
// box spans more than a few pixels.
func strokeMoved(stroke []sdl.Point) bool {
	if len(stroke) < 3 {
		return false
	}
	minX, maxX, minY, maxY := stroke[0].X, stroke[0].X, stroke[0].Y, stroke[0].Y
	for _, sp := range stroke {
		if sp.X < minX {
			minX = sp.X
		}
		if sp.X > maxX {
			maxX = sp.X
		}
		if sp.Y < minY {
			minY = sp.Y
		}
		if sp.Y > maxY {
			maxY = sp.Y
		}
	}
	return maxX-minX > 8 || maxY-minY > 8
}

// samplePathStroke reduces a raw freehand stroke to up to len(pts) evenly-spaced waypoints, packed
// as 4-bit X/Y bytes on the box's grid. Returns the points + count (>=2, else 0). The array size
// (and so the point cap) tracks config.SpriteStylePref.Path.
func samplePathStroke(stroke []sdl.Point, box sdl.Rect) (pts [16]uint8, count uint8) {
	n := len(stroke)
	if n < 2 || box.W <= 0 || box.H <= 0 {
		return pts, 0
	}
	k := len(pts)
	if n < k {
		k = n
	}
	for i := 0; i < k; i++ {
		sp := stroke[i*(n-1)/(k-1)] // evenly spaced, including the first + last points
		gx := uint8(clampInt(int((sp.X-box.X)*16/box.W), 0, 15))
		gy := uint8(clampInt(int((sp.Y-box.Y)*16/box.H), 0, 15))
		pts[i] = gx<<4 | gy
	}
	return pts, uint8(k)
}

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
	case courtroom.MotionShake:
		return "Move: Shake"
	case courtroom.MotionSpiral:
		return "Move: Spiral"
	case courtroom.MotionPendulum:
		return "Move: Pendulum"
	default:
		return "Move: None"
	}
}

// restyleName labels the extra per-pixel restyle for its cycle button (the "10 more restyles" set).
func restyleName(r uint8) string {
	switch courtroom.VariantEffect(r) {
	case courtroom.VariantRedscale:
		return "Restyle: Redscale"
	case courtroom.VariantGreenscale:
		return "Restyle: Greenscale"
	case courtroom.VariantBluescale:
		return "Restyle: Bluescale"
	case courtroom.VariantSolarize:
		return "Restyle: Solarize"
	case courtroom.VariantThreshold:
		return "Restyle: Threshold"
	case courtroom.VariantDuotone:
		return "Restyle: Duotone"
	case courtroom.VariantWarm:
		return "Restyle: Warm"
	case courtroom.VariantCool:
		return "Restyle: Cool"
	case courtroom.VariantNeon:
		return "Restyle: Neon"
	case courtroom.VariantInfrared:
		return "Restyle: Infrared"
	case courtroom.VariantPixelArt:
		return "Restyle: Pixel art"
	default:
		return "Restyle: None"
	}
}

// nextRestyle cycles None → the 10 restyles → None (the restyle values are contiguous).
func nextRestyle(r uint8) uint8 {
	switch {
	case r == 0:
		return uint8(courtroom.VariantRedscale)
	case r >= uint8(courtroom.VariantPixelArt):
		return 0
	default:
		return r + 1
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
	bh += 26                         // Tint + Hue-paint row
	if a.d.Prefs.SpriteStyle().Tint {
		rows := int32((len(spriteStylePresets) + styleSwatchCols - 1) / styleSwatchCols)
		bh += rows*(styleSwatchSz+styleSwatchGap) + 4 + 82 // preset swatches + Hue + R/G/B sliders
	}
	bh += 30                    // opacity (Fade)
	bh += 26                    // Rainbow / Mirror row
	bh += 66                    // Brightness / Size / Tilt sliders
	bh += 26                    // glow / wobble / spin row
	bh += 26                    // invert / grayscale / sepia row
	bh += 26                    // posterize row (#34)
	bh += 30                    // extra-restyle cycle (#M5+)
	bh += 30                    // movement-path cycle (#34)
	bh += 18 + stylePathBox + 8 // draw-your-own path editor (#34 B2)
	if a.d.Prefs.SpriteStyle().Outline {
		bh += 82 // outline-colour swatch + R/G/B sliders (only while Outline is on)
	}
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

	// Recolour toggle + the hue-paint mode (playtest: the multiply tint DARKENS
	// a colourful sprite — the ask was a recolour that only affects hue). Hue
	// paint = the tint applied over the sprite's GRAYSCALE variant: every pixel
	// takes the chosen hue while keeping its own light and shadow (luma × hue).
	// It's a composition of two EXISTING wire fields (Tint + Grayscale), so any
	// AsyncAO build — old or new — renders it identically; no wire change.
	if next := c.Checkbox(x, y, "Recolour (tint)", p.Tint); next != p.Tint {
		p.Tint = next
		a.d.Prefs.SetSpriteStyle(p)
	}
	huePaint := p.Tint && p.Grayscale
	hpRect := sdl.Rect{X: x + 132, Y: y, W: 22 + c.TextWidth("Hue paint"), H: 16}
	if next := c.Checkbox(x+132, y, "Hue paint", huePaint); next != huePaint {
		if next {
			p.Tint, p.Grayscale = true, true
			if p.R == p.G && p.G == p.B { // a hueless (gray/white) tint paints nothing — seed a visible hue
				p.R, p.G, p.B = 255, 0, 0
			}
		} else {
			p.Grayscale = false // back to the plain multiply tint
		}
		a.d.Prefs.SetSpriteStyle(p)
	}
	c.Tooltip(hpRect, "Paint the whole sprite ONE hue while keeping its own light and shadow — set the hue below. (The plain tint multiplies colours, which darkens; this recolours only the hue. Rainbow cycles the paint.)")
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
		// Hue: one slider that drives R/G/B together (a full-vividness hue).
		// With Hue paint on this IS the painted hue ("set all the colors to the
		// same hue, then edit that hue"); as a plain tint it's a pure-hue tint.
		// Derived from the stored RGB, so the swatches and RGB sliders move it.
		// The wheel helpers work in h ∈ [0,1]; the slider shows degrees.
		hue, _, _ := rgbToHSV(p.R, p.G, p.B)
		huePos := clampI32(int32(hue*360+0.5), 0, 359)
		c.Label(x, y, "Hue", ColTextDim)
		if n := c.Slider("styleHue", sdl.Rect{X: x + 44, Y: y, W: r.W - 44 - styleBoxPad*2, H: 14}, huePos, 359); n != huePos {
			p.R, p.G, p.B = hsvToRGB(float64(n)/360, 1, 1)
			a.d.Prefs.SetSpriteStyle(p)
		}
		y += 20
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
	// Extra restyle (#M5+): cycle one of 10 per-pixel looks (redscale, solarize, neon…), transmitted.
	rsb := sdl.Rect{X: x, Y: y, W: r.W - styleBoxPad*2, H: btnH}
	if c.Button(rsb, restyleName(p.Restyle)) {
		p.Restyle = nextRestyle(p.Restyle)
		a.d.Prefs.SetSpriteStyle(p)
	}
	c.Tooltip(rsb, "An extra per-pixel restyle for your sprite — redscale, greenscale, bluescale, solarize, threshold, duotone, warm, cool, neon, infrared, pixel art. Overrides Invert/Grayscale/Sepia/Posterize; other AsyncAO players see it.")
	y += 30
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
	y = a.drawStylePathEditor(x, y, p) // #34 B2: draw-your-own custom path

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
	if p.Outline { // custom outline colour (0,0,0 = the default white) — 3 sliders + a live swatch
		sw := sdl.Color{R: p.OutlineR, G: p.OutlineG, B: p.OutlineB, A: 255}
		if sw.R == 0 && sw.G == 0 && sw.B == 0 {
			sw = sdl.Color{R: 255, G: 255, B: 255, A: 255}
		}
		lblW := c.TextWidth("Outline colour")
		c.Label(x, y+1, "Outline colour", ColTextDim)
		swR := sdl.Rect{X: x + lblW + 6, Y: y, W: 16, H: 16}
		c.Fill(swR, sw)
		c.Border(swR, ColBackground)
		y += 20
		osw := r.W - 24 - styleBoxPad*2
		nr := c.Slider("outlineR", sdl.Rect{X: x + 22, Y: y, W: osw, H: 14}, int32(p.OutlineR), 255)
		c.Label(x, y, "R", ColTextDim)
		y += 20
		ng := c.Slider("outlineG", sdl.Rect{X: x + 22, Y: y, W: osw, H: 14}, int32(p.OutlineG), 255)
		c.Label(x, y, "G", ColTextDim)
		y += 20
		nb := c.Slider("outlineB", sdl.Rect{X: x + 22, Y: y, W: osw, H: 14}, int32(p.OutlineB), 255)
		c.Label(x, y, "B", ColTextDim)
		y += 22
		if nr != int32(p.OutlineR) || ng != int32(p.OutlineG) || nb != int32(p.OutlineB) {
			p.OutlineR, p.OutlineG, p.OutlineB = uint8(nr), uint8(ng), uint8(nb)
			a.d.Prefs.SetSpriteStyle(p)
		}
	}
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
