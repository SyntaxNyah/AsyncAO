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

// drawNameColorPicker draws the per-speaker name-colour controls — a toggle,
// saturation + brightness rows (brightness floored so names stay readable), and
// a live preview of sample names in their computed colours. OFF by default;
// returns the y below the section. Settings-only, not a hot path.
func (a *App) drawNameColorPicker(y, w int32) int32 {
	c := a.ctx
	on := a.d.Prefs.NameColorsOn()
	if next := c.Checkbox(pad, y, "Colour each speaker's name (OFF by default): every name gets its own stable colour from its text", on); next != on {
		a.d.Prefs.SetNameColors(next)
	}
	y += 26
	if !on {
		return y
	}
	sat, val := a.d.Prefs.NameColorSat(), a.d.Prefs.NameColorVal()
	ns := a.numberRow(y, "Name saturation", sat, 5, 0, 100)
	c.Label(pad+270, y+4, "0 = grey · 100 = vivid", ColTextDim)
	y += 30
	nv := a.numberRow(y, "Name brightness", val, 5, 50, 100)
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

// drawHighlightPicker draws the colour-wheel picker for the selection highlight
// and returns the next y. Reads/writes the packed pref; the wheel sets hue+sat
// (keeping the current brightness), the slider sets brightness, the hex field
// sets all three.
func (a *App) drawHighlightPicker(y, w int32) int32 {
	c := a.ctx
	cur := a.d.Prefs.HighlightColorRGB()
	r8, g8, b8 := uint8(cur>>16), uint8(cur>>8), uint8(cur)
	h, s, v := rgbToHSV(r8, g8, b8)

	c.Label(pad, y, "Log selection highlight colour (drag the wheel, slide brightness, or type a hex code):", ColText)
	wy := y + 22

	// --- hue/saturation wheel ---
	const diam = colorWheelDiam
	rad := float64(diam) / 2
	wheel := sdl.Rect{X: pad, Y: wy, W: diam, H: diam}
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
			a.d.Prefs.SetHighlightColor(int(nr)<<16 | int(ng)<<8 | int(nb))
		}
	}

	// --- brightness slider (vertical: full at top, black at bottom) ---
	sl := sdl.Rect{X: wheel.X + diam + 18, Y: wy, W: 26, H: diam}
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
		a.d.Prefs.SetHighlightColor(int(nr)<<16 | int(ng)<<8 | int(nb))
	}

	// --- swatch + hex field ---
	hx := sl.X + sl.W + 20
	c.Fill(sdl.Rect{X: hx, Y: wy, W: 80, H: 34}, sdl.Color{R: r8, G: g8, B: b8, A: 255})
	c.Border(sdl.Rect{X: hx, Y: wy, W: 80, H: 34}, ColPanelHi)
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

	return wy + diam + 12
}
