package ui

import (
	"fmt"
	"math"
	"strconv"

	"github.com/veandco/go-sdl2/sdl"
)

// AsyncAO chrome themes (#M3): user-pickable palettes for the CLIENT UI itself,
// independent of AO2 courtroom themes. The chosen preset becomes the BASE palette
// (activeKitColors) that applyThemePalette restores to; a server theme's stylesheet
// colours still overlay it (with none — the common case — the preset IS the chrome).
// 100% local; the kit colours are package vars read as values, so the render loop stays
// 0-alloc — only a theme/preset change reassigns them.

// chromePreset is one named palette: the 7 base kit colours in defaultKitColors order
// (Background, Panel, PanelHi, Accent, Text, TextDim, Danger).
type chromePreset struct {
	key    string // persisted id
	name   string // Settings label
	colors [7]sdl.Color
}

// chromePresets are the built-in chrome palettes. "dark" mirrors the stock look.
// "soft"/"warm" are the eye-friendly pair (lower contrast, a single calm accent, dim
// but readable secondary text) — added so the default look can be made gentler without
// a global recolour: pick one in Settings → Theme, it applies live and persists.
var chromePresets = []chromePreset{
	{"dark", "Dark", defaultKitColors},
	{"soft", "Soft Dark", [7]sdl.Color{ // gentle contrast, one calm blue accent, warm-neutral dark
		{R: 28, G: 30, B: 34, A: 255}, {R: 40, G: 43, B: 49, A: 255}, {R: 55, G: 59, B: 67, A: 255},
		{R: 122, G: 174, B: 232, A: 255}, {R: 221, G: 223, B: 228, A: 255}, {R: 148, G: 152, B: 160, A: 255},
		{R: 226, G: 98, B: 104, A: 255},
	}},
	{"warm", "Warm", [7]sdl.Color{ // low-blue, sepia-leaning dark for long sessions; amber accent
		{R: 32, G: 29, B: 26, A: 255}, {R: 45, G: 41, B: 37, A: 255}, {R: 60, G: 55, B: 49, A: 255},
		{R: 232, G: 172, B: 112, A: 255}, {R: 228, G: 222, B: 212, A: 255}, {R: 160, G: 150, B: 138, A: 255},
		{R: 222, G: 102, B: 92, A: 255},
	}},
	{"midnight", "Midnight", [7]sdl.Color{
		{R: 12, G: 14, B: 22, A: 255}, {R: 20, G: 24, B: 36, A: 255}, {R: 34, G: 40, B: 56, A: 255},
		{R: 110, G: 150, B: 240, A: 255}, {R: 222, G: 226, B: 238, A: 255}, {R: 132, G: 138, B: 154, A: 255},
		{R: 232, G: 84, B: 92, A: 255},
	}},
	{"light", "Light", [7]sdl.Color{
		{R: 234, G: 235, B: 240, A: 255}, {R: 248, G: 248, B: 251, A: 255}, {R: 226, G: 228, B: 234, A: 255},
		{R: 56, G: 108, B: 210, A: 255}, {R: 30, G: 30, B: 38, A: 255}, {R: 110, G: 112, B: 122, A: 255},
		{R: 196, G: 52, B: 52, A: 255},
	}},
	{"contrast", "High contrast", [7]sdl.Color{
		{R: 0, G: 0, B: 0, A: 255}, {R: 16, G: 16, B: 16, A: 255}, {R: 44, G: 44, B: 44, A: 255},
		{R: 255, G: 222, B: 0, A: 255}, {R: 255, G: 255, B: 255, A: 255}, {R: 205, G: 205, B: 205, A: 255},
		{R: 255, G: 70, B: 70, A: 255},
	}},
}

// chromeCustomKey selects the user-defined "Custom" scheme (colours read live from
// prefs, not the static chromePresets table).
const chromeCustomKey = "custom"

// chromePresetIndex returns the index of the preset with key, or 0 (Dark) if unknown.
func chromePresetIndex(key string) int {
	for i := range chromePresets {
		if chromePresets[i].key == key {
			return i
		}
	}
	return 0
}

// customChromeColors builds the user's Custom palette: each kit slot is the saved hex
// colour, or the stock dark colour when that slot is blank / unparseable — so a
// half-filled custom scheme is always coherent (no black-on-black slots).
func (a *App) customChromeColors() [7]sdl.Color {
	out := defaultKitColors
	hex := a.d.Prefs.CustomChrome()
	for i := range out {
		if col, ok := parseHexColor(hex[i]); ok {
			out[i] = col
		}
	}
	// Readability floor: a custom scheme gets no AO2-theme overlay, so the kit-wide
	// guard in applyThemePalette never runs for it. Re-derive Text (and dim text)
	// from the Panel's lightness when the chosen Text would vanish into the Panel, so
	// a user can't accidentally paint the Settings text invisible and get stuck.
	if absInt(colLuma(out[4])-colLuma(out[1])) < minInkSkinContrast {
		if colLuma(out[1]) < paletteLightPanelLuma {
			out[4] = defaultKitColors[4] // light ink on a dark panel
		} else {
			out[4] = paletteDarkText // dark ink on a light panel
		}
		out[5] = scaleColor(out[4], paletteDimPct)
	}
	return out
}

// applyChromePreset makes the preset for key the base palette and re-overlays the current
// AO2 theme (if any), then purges the colour-keyed text cache so labels re-rasterise in
// the new colours. Called on startup (from prefs) and when the user picks a theme. The
// "custom" key reads the user's saved colours instead of the static table.
func (a *App) applyChromePreset(key string) {
	if key == chromeCustomKey {
		activeKitColors = a.customChromeColors()
	} else {
		activeKitColors = chromePresets[chromePresetIndex(key)].colors
	}
	applyThemePalette(a.themePalette) // restore the new base, then re-lay the theme over it
	a.ctx.purgeTextCache()
}

// drawChromeSettings draws the AsyncAO chrome-theme picker (#M3): a colour swatch + a
// button per preset, the active one ringed. Picking one applies it live and persists it.
// Settings-only; never a hot path.
func (a *App) drawChromeSettings(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	cur := a.d.Prefs.ChromeTheme()
	c.Label(pad, y, "Client UI colours — AsyncAO's own panels, separate from AO2 courtroom themes.", ColTextDim)
	y += 24
	// Quick accent picker (#41): one click to edit JUST the UI accent on the Custom
	// scheme (the colour wheel below opens on the Accent slot), so "recolour the accent"
	// doesn't need hunting through the full editor.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Customize accent…") {
		a.d.Prefs.SetChromeTheme(chromeCustomKey)
		a.applyChromePreset(chromeCustomKey)
		a.chromeEditSlot = 3 // the Accent slot → the wheel below edits it directly
	}
	c.LabelClipped(pad+180, y+5, w-pad-180-scrollBarW, "Recolour the whole UI accent — or pick a preset / edit every colour below.", ColTextDim)
	y += btnH + 12
	x := pad
	for i := range chromePresets {
		p := &chromePresets[i]
		sw := sdl.Rect{X: x, Y: y + 3, W: 22, H: 22}
		c.Fill(sw, p.colors[1])                                         // Panel
		c.Fill(sdl.Rect{X: x + 14, Y: y + 3, W: 8, H: 22}, p.colors[3]) // Accent stripe
		c.Border(sw, ColTextDim)
		bw := c.TextWidth(p.name) + 18
		btn := sdl.Rect{X: x + 26, Y: y, W: bw, H: btnH}
		clicked := c.Button(btn, p.name)
		if p.key == cur {
			c.Border(btn, ColAccent) // ring the active preset
		}
		if clicked {
			a.d.Prefs.SetChromeTheme(p.key)
			a.applyChromePreset(p.key)
		}
		x += 26 + bw + 14
		if x > w-170 { // wrap to a second row on a narrow window
			x = pad
			y += btnH + 8
		}
	}
	// The user-defined "Custom" scheme, picked like any preset; its swatch previews
	// the saved colours (stock dark for any slot left blank).
	custom := a.customChromeColors()
	csw := sdl.Rect{X: x, Y: y + 3, W: 22, H: 22}
	c.Fill(csw, custom[1])                                        // Panel
	c.Fill(sdl.Rect{X: x + 14, Y: y + 3, W: 8, H: 22}, custom[3]) // Accent stripe
	c.Border(csw, ColTextDim)
	cbw := c.TextWidth("Custom") + 18
	cbtn := sdl.Rect{X: x + 26, Y: y, W: cbw, H: btnH}
	if c.Button(cbtn, "Custom") {
		a.d.Prefs.SetChromeTheme(chromeCustomKey)
		a.applyChromePreset(chromeCustomKey)
	}
	if cur == chromeCustomKey {
		c.Border(cbtn, ColAccent) // ring the active scheme
	}
	y += btnH + 10
	if cur == chromeCustomKey { // edit the colours only when Custom is the active scheme
		y = a.drawCustomChromeEditor(y)
	}
	return y + 10
}

// drawCustomChromeEditor edits the 7 kit colours with a colour WHEEL (playtest:
// "improve custom colours to like a wheel"): a row of swatches picks which kit
// colour to edit, then a hue/saturation disc + a brightness slider + a hex field
// set it. A blank/untouched slot uses the stock dark colour. Edits apply live
// (recolour + cache purge) and persist. Reuses the highlight picker's wheel.
func (a *App) drawCustomChromeEditor(y int32) int32 {
	c := a.ctx
	pad := a.formX
	labels := [7]string{"Background", "Panel", "Raised panel", "Accent", "Text", "Dim text", "Danger"}
	hex := a.d.Prefs.CustomChrome()
	colOf := func(i int) sdl.Color {
		if col, ok := parseHexColor(hex[i]); ok {
			return col
		}
		return defaultKitColors[i] // stock dark for blank / unparseable
	}
	apply := func(i int, col sdl.Color) {
		hex[i] = fmt.Sprintf("%02x%02x%02x", col.R, col.G, col.B)
		a.d.Prefs.SetCustomChrome(hex)
		a.applyChromePreset(chromeCustomKey) // live recolour
	}
	if a.chromeEditSlot < 0 || a.chromeEditSlot >= len(labels) {
		a.chromeEditSlot = 0
	}

	c.LabelClipped(pad, y, a.formW, "Pick a colour, then drag the wheel / brightness or type a hex. Untouched slots stay stock dark.", ColTextDim)
	y += 24
	// Swatch row — click to choose which kit colour the wheel edits.
	sx := pad
	for i := range labels {
		sw := sdl.Rect{X: sx, Y: y, W: 28, H: 28}
		c.Fill(sw, colOf(i))
		ring := ColTextDim
		if i == a.chromeEditSlot {
			ring = ColAccent
		}
		c.Border(sw, ring)
		if c.clicked && c.hovering(sw) {
			a.chromeEditSlot = i
		}
		c.TooltipAfter("chromeslot"+strconv.Itoa(i), sw, labels[i])
		sx += 34
	}
	y += 36
	sel := a.chromeEditSlot
	c.Label(pad, y, "Editing: "+labels[sel], ColAccent)
	y += 22

	y = a.wheelHexEditor(y, "chromehex", &a.chromeHexBuf, colOf(sel), func(col sdl.Color) {
		apply(sel, col)
	})
	if c.Button(sdl.Rect{X: pad, Y: y, W: 130, H: btnH}, "Reset all to Dark") {
		a.d.Prefs.SetCustomChrome([7]string{})
		a.applyChromePreset(chromeCustomKey)
	}
	return y + btnH + 10
}

// wheelHexEditor is the shared colour-editing body (custom chrome + layout
// part colours): the cached hue/sat disc, a brightness slider, a live swatch
// and a hex field, all editing cur — apply fires on every change. fieldID
// keys the hex TextField; hexBuf is the caller's edit buffer (reflects wheel
// edits while not focused). Draws at the settings form origin; returns the
// next y. Settings-only, never a hot path.
func (a *App) wheelHexEditor(y int32, fieldID string, hexBuf *string, cur sdl.Color, apply func(sdl.Color)) int32 {
	c := a.ctx
	pad := a.formX
	hh, ss, vv := rgbToHSV(cur.R, cur.G, cur.B)
	const diam = colorWheelDiam
	rad := float64(diam) / 2
	wheel := sdl.Rect{X: pad, Y: y, W: diam, H: diam}
	if tex := a.ensureColorWheel(); tex != nil {
		_ = c.Ren.Copy(tex, nil, &wheel)
	}
	ang := hh * 2 * math.Pi
	dotX := wheel.X + int32(rad+math.Cos(ang)*ss*rad)
	dotY := wheel.Y + int32(rad+math.Sin(ang)*ss*rad)
	c.Border(sdl.Rect{X: dotX - 5, Y: dotY - 5, W: 10, H: 10}, ColText)
	c.Border(sdl.Rect{X: dotX - 4, Y: dotY - 4, W: 8, H: 8}, ColBackground)
	if c.mouseDown && c.hovering(wheel) {
		dx := float64(c.mouseX-wheel.X) - rad
		dy := float64(c.mouseY-wheel.Y) - rad
		if dist := math.Hypot(dx, dy); dist <= rad {
			nh := math.Atan2(dy, dx) / (2 * math.Pi)
			nr, ng, nb := hsvToRGB(nh, dist/rad, math.Max(vv, 0.05)) // keep brightness (floor so a near-black pick still shows hue)
			apply(sdl.Color{R: nr, G: ng, B: nb, A: 255})
		}
	}
	// Brightness slider (full at top → black at bottom), of the current hue/sat.
	slR := sdl.Rect{X: wheel.X + diam + 18, Y: y, W: 26, H: diam}
	for i := int32(0); i < slR.H; i++ {
		br := 1 - float64(i)/float64(slR.H)
		rr, gg, bb := hsvToRGB(hh, ss, br)
		c.Fill(sdl.Rect{X: slR.X, Y: slR.Y + i, W: slR.W, H: 1}, sdl.Color{R: rr, G: gg, B: bb, A: 255})
	}
	c.Border(slR, ColPanelHi)
	knobY := slR.Y + int32((1-vv)*float64(slR.H-4))
	c.Border(sdl.Rect{X: slR.X - 2, Y: knobY, W: slR.W + 4, H: 4}, ColText)
	if c.mouseDown && c.hovering(slR) {
		nv := clampF64(1-float64(c.mouseY-slR.Y)/float64(slR.H), 0, 1)
		nr, ng, nb := hsvToRGB(hh, ss, nv)
		apply(sdl.Color{R: nr, G: ng, B: nb, A: 255})
	}
	// Live swatch + hex field for the edited colour.
	hx := slR.X + slR.W + 20
	c.Fill(sdl.Rect{X: hx, Y: y, W: 90, H: 34}, cur)
	c.Border(sdl.Rect{X: hx, Y: y, W: 90, H: 34}, ColPanelHi)
	if c.focusID != fieldID {
		*hexBuf = fmt.Sprintf("%02x%02x%02x", cur.R, cur.G, cur.B) // reflect wheel/slider edits when not typing
	}
	if next, _ := c.TextField(fieldID, sdl.Rect{X: hx, Y: y + 42, W: 100, H: fieldH}, *hexBuf, "RRGGBB"); next != *hexBuf {
		*hexBuf = next
		if col, ok := parseHexColor(next); ok {
			apply(col)
		}
	}
	c.Label(hx, y+42+fieldH+4, "hex code", ColTextDim)
	return y + diam + 14
}
