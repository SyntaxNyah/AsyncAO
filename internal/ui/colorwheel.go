package ui

// HSV colour-wheel picker for the log-selection highlight colour: a hue/
// saturation disc (drag to pick), a brightness slider, a live swatch, and a
// hex field. The disc is rendered to a texture ONCE (cached) — per-frame it's
// just a Copy + a few rects, so the picker costs nothing on idle settings
// frames. Settings-only; not on any hot path.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// colorWheelDiam is the picker disc diameter (a "giant" wheel per the request).
const colorWheelDiam = 150

// icColorWheelCaption is the free-hex picker's heading. It MUST fit the
// picker's fixed 348px width (see icColorWheelRect, minus the 8px side
// margins the label draws at) — the draw clips it as a hard guard, and
// icColorWheel_test.go pins that it fits at the default chrome font so a
// future wording edit can't silently reopen the overflow bug (§3.4). It
// still conveys both halves: exact colour for AsyncAO players, nearest AO
// colour for everyone else.
const icColorWheelCaption = "Chat colour — exact for AsyncAO, nearest AO for others"

// nameColor maps a speaker name to a stable, smooth colour: an FNV-1a hash of
// the name picks the hue, the caller's saturation/value (0..1) fill it out.
// Deterministic (same name → same colour every launch, unlike a map-seeded
// hash) and allocation-free, so it's cheap to call inline per visible name —
// no cache to go stale when the sliders move.
func nameColor(name string, sat, val float64) sdl.Color {
	var h uint32 = 2166136261 // FNV-1a offset basis
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 16777619 // FNV-1a prime
	}
	r, g, b := hsvToRGB(float64(h%360)/360, sat, val)
	return sdl.Color{R: r, G: g, B: b, A: 255}
}

// Per-character chatbox tint (#14): blend a hint of the speaker's stable hue into the flat
// chatbox panel so each character's box carries their colour while staying dark enough to
// read light text on. Viewer-local; shares nameColor's hue, so a tinted box and a coloured
// name match for the same speaker.
const (
	chatboxTintSat = 0.85 // vivid hue source
	chatboxTintVal = 0.55 // mid-tone so the blend shifts colour without lightening the box
	chatboxTintMix = 30   // percent of the speaker hue mixed into the base (subtle, stays dark)
)

// chatboxTintFor blends the speaker's hue into base. Pure, allocation-free.
func chatboxTintFor(name string, base sdl.Color) sdl.Color {
	t := nameColor(name, chatboxTintSat, chatboxTintVal)
	return sdl.Color{
		R: mixByte(base.R, t.R, chatboxTintMix),
		G: mixByte(base.G, t.G, chatboxTintMix),
		B: mixByte(base.B, t.B, chatboxTintMix),
		A: base.A,
	}
}

// mixByte blends a→b by mix percent (0..100): a*(100-mix)/100 + b*mix/100.
func mixByte(a, b uint8, mix int) uint8 {
	return uint8((int(a)*(100-mix) + int(b)*mix) / 100)
}

// drawNameColorPicker draws the per-speaker name-colour controls — a toggle,
// saturation + brightness rows (brightness floored so names stay readable), and
// a live preview of sample names in their computed colours. OFF by default;
// returns the y below the section. Settings-only, not a hot path.
func (a *App) drawNameColorPicker(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // rebase into the settings content card
	_ = w
	on := a.d.Prefs.NameColorsOn()
	if next := c.Checkbox(pad, y, "Colour each speaker's name (OFF by default): every name gets its own stable colour from its text", on); next != on {
		a.d.Prefs.SetNameColors(next)
	}
	y += 26
	if !on {
		return y
	}
	sat, val := a.d.Prefs.NameColorSat(), a.d.Prefs.NameColorVal()
	ns := a.sliderRow(y, "Name saturation", sat, 5, 0, 100)
	c.Label(pad+270, y+4, "0 = grey · 100 = vivid", ColTextDim)
	y += 30
	nv := a.sliderRow(y, "Name brightness", val, 5, 50, 100)
	c.Label(pad+270, y+4, "kept ≥ 50 so names stay readable on the dark panel", ColTextDim)
	y += 30
	if ns != sat || nv != val {
		a.d.Prefs.SetNameColorSatVal(ns, nv)
	}
	// Live preview — sample names in their hash colours at the current sliders.
	fs, fv := float64(a.d.Prefs.NameColorSat())/100, float64(a.d.Prefs.NameColorVal())/100
	c.Label(pad, y+4, "Preview:", ColTextDim)
	px := pad + 70
	for _, nm := range []string{"Phoenix", "Edgeworth", "Maya", "Franziska", "Klavier", "Apollo"} {
		c.Label(px, y+4, nm, nameColor(nm, fs, fv))
		px += c.TextWidth(nm) + 16
	}
	y += 28
	return y
}

// hsvToRGB converts h,s,v in [0,1] to 8-bit RGB.
func hsvToRGB(h, s, v float64) (uint8, uint8, uint8) {
	if s <= 0 {
		g := uint8(v*255 + 0.5)
		return g, g, g
	}
	h = math.Mod(h, 1)
	if h < 0 {
		h += 1
	}
	hh := h * 6
	i := int(hh)
	f := hh - float64(i)
	p := v * (1 - s)
	q := v * (1 - s*f)
	t := v * (1 - s*(1-f))
	var r, g, b float64
	switch i % 6 {
	case 0:
		r, g, b = v, t, p
	case 1:
		r, g, b = q, v, p
	case 2:
		r, g, b = p, v, t
	case 3:
		r, g, b = p, q, v
	case 4:
		r, g, b = t, p, v
	default:
		r, g, b = v, p, q
	}
	return uint8(r*255 + 0.5), uint8(g*255 + 0.5), uint8(b*255 + 0.5)
}

// rgbToHSV converts 8-bit RGB to h,s,v in [0,1].
func rgbToHSV(r, g, b uint8) (h, s, v float64) {
	rf, gf, bf := float64(r)/255, float64(g)/255, float64(b)/255
	mx := math.Max(rf, math.Max(gf, bf))
	mn := math.Min(rf, math.Min(gf, bf))
	v = mx
	d := mx - mn
	if mx > 0 {
		s = d / mx
	}
	if d <= 0 {
		return 0, s, v // grey: hue undefined
	}
	switch mx {
	case rf:
		h = (gf - bf) / d
	case gf:
		h = (bf-rf)/d + 2
	default:
		h = (rf-gf)/d + 4
	}
	h /= 6
	if h < 0 {
		h += 1
	}
	return h, s, v
}

// parseHex6 parses "RRGGBB" (with optional leading #) into a packed 0xRRGGBB,
// reporting success.
func parseHex6(s string) (int, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(s) != 6 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// ensureColorWheel builds (once) and returns the hue/saturation disc texture:
// hue around the ring, saturation along the radius, at full value. Pixels
// outside the disc are transparent. Cached for the App's lifetime.
func (a *App) ensureColorWheel() *sdl.Texture {
	if a.colorWheel != nil {
		return a.colorWheel
	}
	const d = colorWheelDiam
	pix := make([]byte, d*d*4) // ABGR8888 == [R,G,B,A] bytes (render/textures.go)
	rad := float64(d) / 2
	for py := 0; py < d; py++ {
		for px := 0; px < d; px++ {
			dx := float64(px) - rad + 0.5
			dy := float64(py) - rad + 0.5
			dist := math.Hypot(dx, dy)
			i := (py*d + px) * 4
			if dist > rad {
				continue // transparent outside the disc (alpha stays 0)
			}
			hue := math.Atan2(dy, dx) / (2 * math.Pi)
			r, g, b := hsvToRGB(hue, dist/rad, 1)
			pix[i], pix[i+1], pix[i+2], pix[i+3] = r, g, b, 255
		}
	}
	tex, err := a.ctx.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_STATIC, d, d)
	if err != nil {
		return nil
	}
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	_ = tex.Update(nil, unsafe.Pointer(&pix[0]), d*4)
	a.colorWheel = tex
	return tex
}

// --- free-hex IC chat colour (v1.52.0, Tifera: "any hex while chatting") ---

// icColorWheelRect is the free-hex picker's floating rect, anchored just above
// the IC colour swatch (the IC bar sits near the bottom), clamped on-screen —
// derived from the remembered swatch rect so the draw and the pointer fence
// agree (the fxPickerRect pattern).
func (a *App) icColorWheelRect(w, h int32) sdl.Rect {
	const (
		pw = 12 + colorWheelDiam + 18 + 26 + 20 + 110 + 12 // wheel · brightness bar · swatch/hex column
		ph = colorWheelDiam + 40                           // + heading row and padding
	)
	x := a.icSwatchRect.X
	if x+pw > w-4 {
		x = w - 4 - pw
	}
	if x < 4 {
		x = 4
	}
	y := a.icSwatchRect.Y - ph - 6 // above the swatch
	if y < 4 {
		y = 4 // tiny window: clamp to the top rather than run off-screen
	}
	return sdl.Rect{X: x, Y: y, W: pw, H: ph}
}

// setICCustomRGB applies a wheel/slider/hex pick: the session colour updates
// live (the swatch + the next send use it) and the pick persists as the
// default a fresh tab seeds from.
func (a *App) setICCustomRGB(rgb int) {
	a.icCustomRGB = rgb & 0xFFFFFF
	a.d.Prefs.SetICCustomColor(a.icCustomRGB)
}

// drawICColorWheel paints the free-hex chat-colour picker: the shared hue/sat
// disc, a brightness slider, a live swatch and a hex field. A non-blocking
// floating panel like the FX picker — chat stays live behind it; Esc, the
// Done chip, or clicking the swatch again closes it.
func (a *App) drawICColorWheel(w, h int32) {
	c := a.ctx
	r := a.icColorWheelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	// Clip to the panel interior (8px margin each side): the panel is sized
	// to the wheel/bar/swatch controls, not the caption, so an unclipped
	// c.Label would paint past the border onto the live scene behind this
	// floating popup (§3.4). LabelClipped shares the same text cache — no
	// allocation change.
	c.LabelClipped(r.X+8, r.Y+5, r.W-16, icColorWheelCaption, ColTextDim)

	cur := a.icCustomRGB
	r8, g8, b8 := uint8(cur>>16), uint8(cur>>8), uint8(cur)
	hh, ss, vv := rgbToHSV(r8, g8, b8)

	const diam = colorWheelDiam
	rad := float64(diam) / 2
	wheel := sdl.Rect{X: r.X + 12, Y: r.Y + 26, W: diam, H: diam}
	if tex := a.ensureColorWheel(); tex != nil {
		_ = c.Ren.Copy(tex, nil, &wheel)
	}
	ang := hh * 2 * math.Pi
	dotX := wheel.X + int32(rad+math.Cos(ang)*ss*rad)
	dotY := wheel.Y + int32(rad+math.Sin(ang)*ss*rad)
	c.Border(sdl.Rect{X: dotX - 5, Y: dotY - 5, W: 10, H: 10}, ColText)
	c.Border(sdl.Rect{X: dotX - 4, Y: dotY - 4, W: 8, H: 8}, ColBackground)
	// The popup is drawn over a fenced pointer (hovering() is false under a
	// modal), so it hit-tests raw like the editor chips do.
	if c.mouseDown && pointIn(c.mouseX, c.mouseY, wheel) {
		dx := float64(c.mouseX-wheel.X) - rad
		dy := float64(c.mouseY-wheel.Y) - rad
		if dist := math.Hypot(dx, dy); dist <= rad {
			nh := math.Atan2(dy, dx) / (2 * math.Pi)
			nr, ng, nb := hsvToRGB(nh, dist/rad, math.Max(vv, 0.05)) // keep brightness (floor so a black pick still shows hue)
			a.setICCustomRGB(int(nr)<<16 | int(ng)<<8 | int(nb))
		}
	}

	// Brightness slider (full at top → black at bottom) of the current hue/sat.
	sl := sdl.Rect{X: wheel.X + diam + 18, Y: wheel.Y, W: 26, H: diam}
	for i := int32(0); i < sl.H; i++ {
		br := 1 - float64(i)/float64(sl.H)
		rr, gg, bb := hsvToRGB(hh, ss, br)
		c.Fill(sdl.Rect{X: sl.X, Y: sl.Y + i, W: sl.W, H: 1}, sdl.Color{R: rr, G: gg, B: bb, A: 255})
	}
	c.Border(sl, ColPanelHi)
	knobY := sl.Y + int32((1-vv)*float64(sl.H-4))
	c.Border(sdl.Rect{X: sl.X - 2, Y: knobY, W: sl.W + 4, H: 4}, ColText)
	if c.mouseDown && pointIn(c.mouseX, c.mouseY, sl) {
		nv := clampF64(1-float64(c.mouseY-sl.Y)/float64(sl.H), 0, 1)
		nr, ng, nb := hsvToRGB(hh, ss, nv)
		a.setICCustomRGB(int(nr)<<16 | int(ng)<<8 | int(nb))
	}

	// Swatch + hex field + Done.
	hx := sl.X + sl.W + 20
	c.Fill(sdl.Rect{X: hx, Y: wheel.Y, W: 90, H: 34}, sdl.Color{R: r8, G: g8, B: b8, A: 255})
	c.Border(sdl.Rect{X: hx, Y: wheel.Y, W: 90, H: 34}, ColPanelHi)
	if c.focusID != "iccustomhex" {
		a.icColorHexBuf = fmt.Sprintf("%06x", cur) // reflect wheel/slider edits when not typing
	}
	if next, _ := c.TextField("iccustomhex", sdl.Rect{X: hx, Y: wheel.Y + 42, W: 100, H: fieldH}, a.icColorHexBuf, "RRGGBB"); next != a.icColorHexBuf {
		a.icColorHexBuf = next
		if rgb, ok := parseHex6(next); ok {
			a.setICCustomRGB(rgb)
		}
	}
	c.Label(hx, wheel.Y+42+fieldH+4, "hex code", ColTextDim)
	done := sdl.Rect{X: hx, Y: wheel.Y + diam - btnH, W: 90, H: btnH}
	a.rawChip(done, "Done")
	if c.clicked && pointIn(c.mouseX, c.mouseY, done) {
		a.showICColorWheel = false
	}
}

// drawWheelPicker draws the settings pickers' shared inline core — the hue/sat
// disc, the vertical brightness bar, and a live swatch — reading cur (packed
// 0xRRGGBB) and writing every wheel/bar edit through set. The wheel sets hue+sat
// (keeping the current brightness, floored so a black pick still shows its hue);
// the bar sets brightness. Returns the next y and the swatch column's x (callers
// hang their own hex field / buttons there — each binds its own pref format).
func (a *App) drawWheelPicker(x, y int32, cur int, set func(rgb int)) (nextY, hexX int32) {
	c := a.ctx
	r8, g8, b8 := uint8(cur>>16), uint8(cur>>8), uint8(cur)
	h, s, v := rgbToHSV(r8, g8, b8)

	// --- hue/saturation wheel ---
	const diam = colorWheelDiam
	rad := float64(diam) / 2
	wheel := sdl.Rect{X: x, Y: y, W: diam, H: diam}
	if tex := a.ensureColorWheel(); tex != nil {
		_ = c.Ren.Copy(tex, nil, &wheel)
	}
	// Selector ring at the current hue/sat.
	ang := h * 2 * math.Pi
	dotX := wheel.X + int32(rad+math.Cos(ang)*s*rad)
	dotY := wheel.Y + int32(rad+math.Sin(ang)*s*rad)
	c.Border(sdl.Rect{X: dotX - 5, Y: dotY - 5, W: 10, H: 10}, ColText)
	c.Border(sdl.Rect{X: dotX - 4, Y: dotY - 4, W: 8, H: 8}, ColBackground)
	if c.mouseDown && c.hovering(wheel) {
		dx := float64(c.mouseX-wheel.X) - rad
		dy := float64(c.mouseY-wheel.Y) - rad
		if dist := math.Hypot(dx, dy); dist <= rad {
			nh := math.Atan2(dy, dx) / (2 * math.Pi)
			nr, ng, nb := hsvToRGB(nh, dist/rad, math.Max(v, 0.05)) // keep brightness (floor so a black pick still shows hue)
			set(int(nr)<<16 | int(ng)<<8 | int(nb))
		}
	}

	// --- brightness slider (vertical: full at top, black at bottom) ---
	sl := sdl.Rect{X: wheel.X + diam + 18, Y: y, W: 26, H: diam}
	for i := int32(0); i < sl.H; i++ { // gradient of the current hue/sat
		br := 1 - float64(i)/float64(sl.H)
		rr, gg, bb := hsvToRGB(h, s, br)
		c.Fill(sdl.Rect{X: sl.X, Y: sl.Y + i, W: sl.W, H: 1}, sdl.Color{R: rr, G: gg, B: bb, A: 255})
	}
	c.Border(sl, ColPanelHi)
	knobY := sl.Y + int32((1-v)*float64(sl.H-4))
	c.Border(sdl.Rect{X: sl.X - 2, Y: knobY, W: sl.W + 4, H: 4}, ColText)
	if c.mouseDown && c.hovering(sl) {
		nv := 1 - float64(c.mouseY-sl.Y)/float64(sl.H)
		nv = clampF64(nv, 0, 1)
		nr, ng, nb := hsvToRGB(h, s, nv)
		set(int(nr)<<16 | int(ng)<<8 | int(nb))
	}

	// --- live swatch (the caller's hex/buttons column starts at hexX) ---
	hexX = sl.X + sl.W + 20
	c.Fill(sdl.Rect{X: hexX, Y: y, W: 80, H: 34}, sdl.Color{R: r8, G: g8, B: b8, A: 255})
	c.Border(sdl.Rect{X: hexX, Y: y, W: 80, H: 34}, ColPanelHi)
	return y + diam + 12, hexX
}

// drawHighlightPicker draws the colour-wheel picker for the selection highlight
// and returns the next y. Reads/writes the packed pref; the wheel sets hue+sat
// (keeping the current brightness), the slider sets brightness, the hex field
// sets all three.
func (a *App) drawHighlightPicker(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // rebase into the settings content card
	_ = w
	cur := a.d.Prefs.HighlightColorRGB()

	c.Label(pad, y, "Log selection highlight colour (drag the wheel, slide brightness, or type a hex code):", ColText)
	wy := y + 22
	nextY, hx := a.drawWheelPicker(pad, wy, cur, func(rgb int) {
		a.d.Prefs.SetHighlightColor(rgb)
	})
	if c.focusID != "highlighthex" {
		a.colorHex = fmt.Sprintf("%06x", cur) // reflect wheel/slider edits when not typing
	}
	if next, _ := c.TextField("highlighthex", sdl.Rect{X: hx, Y: wy + 42, W: 100, H: fieldH}, a.colorHex, "RRGGBB"); next != a.colorHex {
		a.colorHex = next
		if rgb, ok := parseHex6(next); ok {
			a.d.Prefs.SetHighlightColor(rgb)
		}
	}
	c.Label(hx, wy+42+fieldH+4, "hex code", ColTextDim)
	return nextY
}
