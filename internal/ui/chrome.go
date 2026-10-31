package ui

import (
	"fmt"
	"math"
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// AsyncAO chrome themes: user-pickable palettes for the client UI, independent of AO2 courtroom themes.
// The chosen preset is the base palette (activeKitColors) that applyThemePalette restores to; a server theme's
// stylesheet colours still overlay it.

// chromePreset is one named palette: the 7 base kit colours in defaultKitColors order
// (Background, Panel, PanelHi, Accent, Text, TextDim, Danger).
type chromePreset struct {
	key    string // persisted id
	name   string // Settings label
	colors [7]sdl.Color
}

// chromePresets are the built-in chrome palettes; "dark" mirrors the stock look, "soft"/"warm" are lower contrast.
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

// customChromeColors builds the Custom palette: each slot is its saved hex colour, or the stock dark colour if
// blank or unparseable.
func (a *App) customChromeColors() [7]sdl.Color {
	out := defaultKitColors
	hex := a.d.Prefs.Load().CustomChromeHex
	for i := range out {
		if c, ok := parse.HexColor(hex[i]); ok {
			col := sdl.Color(c)
			out[i] = col
		}
	}
	// Readability floor: a Custom scheme gets no theme overlay, so re-derive Text from the Panel's lightness when
	// it would vanish into the Panel.
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

// applyChromePreset makes the preset for key the base palette, re-overlays the current AO2 theme and purges the
// colour-keyed text cache. The "custom" key reads the saved colours.
func (a *App) applyChromePreset(key string) {
	if key == chromeCustomKey {
		activeKitColors = a.customChromeColors()
	} else {
		activeKitColors = chromePresets[chromePresetIndex(key)].colors
	}
	applyThemePalette(a.themePalette) // restore the new base, then re-lay the theme over it
	a.ctx.purgeTextCache()
}

// drawChromeSettings draws a swatch and button per preset, applying the pick live and persisting it.
func (a *App) drawChromeSettings(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	cur := a.d.Prefs.Load().ChromeTheme()
	c.Label(pad, y, "Client UI colours — AsyncAO's own panels, separate from AO2 courtroom themes.", ColTextDim)
	y += 24
	// Quick accent picker: edits just the accent of the Custom scheme.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Customize accent…") {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ChromeThemeKey = chromeCustomKey })
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
			{
				pv1 := p.key
				a.d.Prefs.Update(func(sn *config.Settings) {
					sn.ChromeThemeKey = pv1
				})
			}
			a.applyChromePreset(p.key)
		}
		x += 26 + bw + 14
		if x > w-170 { // wrap to a second row on a narrow window
			x = pad
			y += btnH + 8
		}
	}
	// The Custom scheme is picked like a preset; its swatch previews the saved colours.
	custom := a.customChromeColors()
	csw := sdl.Rect{X: x, Y: y + 3, W: 22, H: 22}
	c.Fill(csw, custom[1])                                        // Panel
	c.Fill(sdl.Rect{X: x + 14, Y: y + 3, W: 8, H: 22}, custom[3]) // Accent stripe
	c.Border(csw, ColTextDim)
	cbw := c.TextWidth("Custom") + 18
	cbtn := sdl.Rect{X: x + 26, Y: y, W: cbw, H: btnH}
	if c.Button(cbtn, "Custom") {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ChromeThemeKey = chromeCustomKey })
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

// shapePickerEntry is one chrome-shape preset in the Settings picker: persisted key and label.
type shapePickerEntry struct {
	key  string
	name string
}

// shapePickerEntries are the built-in shape presets; sharp leads as the default.
var shapePickerEntries = []shapePickerEntry{
	{shapeSharp, "Sharp"},
	{shapeRounded, "Rounded"},
	{shapePillKey, "Pill"},
}

// drawShapeSettings draws a button per shape preset, plus a corner-radius slider for "rounded" only.
// Picking applies live: it rebuilds and re-resolves the Ctx so the rest of this frame uses the new shape.
func (a *App) drawShapeSettings(y int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	cur := a.d.Prefs.ChromeShape()
	c.Label(pad, y, "Client UI shape — rounds AsyncAO's own buttons & panels; hit areas are unchanged.", ColTextDim)
	y += 24
	x := pad
	for i := range shapePickerEntries {
		e := &shapePickerEntries[i]
		bw := c.TextWidth(e.name) + 22
		btn := sdl.Rect{X: x, Y: y, W: bw, H: btnH}
		clicked := c.Button(btn, e.name)
		if e.key == cur {
			c.Border(btn, ColAccent) // ring the active preset
		}
		if clicked && e.key != cur {
			a.d.Prefs.SetChromeShape(e.key)
			// Re-resolve the Ctx mask pointers now: the build can destroy the pages the frame-start resolve points at.
			a.buildShapeMasks()
			a.refreshShapeMasks()
		}
		x += bw + 12
		if x > w-120 { // wrap on a narrow window
			x = pad
			y += btnH + 8
		}
	}
	y += btnH + 12
	// Corner-radius tier slider, only for rounded (pill is a full half-radius, sharp has no corners).
	if cur == shapeRounded {
		c.Label(pad, y, "Corner radius", ColTextDim)
		y += 20
		tier := int32(a.d.Prefs.ChromeShapeTier())
		// Slider yields a value in [0, shapeRadiusTiers-1]; apply + rebuild on change.
		if nv := c.Slider("shapeTier", sdl.Rect{X: pad, Y: y, W: 220, H: fieldH}, tier, shapeRadiusTiers-1); nv != tier {
			a.d.Prefs.SetChromeShapeTier(int(nv))
			// All tiers' masks are already resident, so a tier change only needs the per-frame resolve.
			a.refreshShapeMasks()
		}
		c.Label(pad+230, y+2, "smaller … larger", ColTextDim)
		y += fieldH + 10
	}
	return y + 6
}

// drawCustomChromeEditor edits the 7 kit colours: a swatch row picks the colour, then a wheel, brightness slider
// and hex field set it. A blank slot uses the stock dark colour; edits apply live and persist.
func (a *App) drawCustomChromeEditor(y int32) int32 {
	c := a.ctx
	pad := a.formX
	labels := [7]string{"Background", "Panel", "Raised panel", "Accent", "Text", "Dim text", "Danger"}
	hex := a.d.Prefs.Load().CustomChromeHex
	colOf := func(i int) sdl.Color {
		if c, ok := parse.HexColor(hex[i]); ok {
			col := sdl.Color(c)
			return col
		}
		return defaultKitColors[i] // stock dark for blank / unparseable
	}
	apply := func(i int, col sdl.Color) {
		hex[i] = fmt.Sprintf("%02x%02x%02x", col.R, col.G, col.B)
		a.d.Prefs.Update(func(sn *config.Settings) { sn.CustomChromeHex = hex })
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
		{
			pv2 := [7]string{}
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.CustomChromeHex = pv2
			})
		}
		a.applyChromePreset(chromeCustomKey)
	}
	return y + btnH + 10
}

// wheelHexEditor is the shared colour-editing body: hue/sat disc, brightness slider, swatch and hex field editing cur.
// apply fires on every change; fieldID keys the hex TextField; hexBuf is the caller's edit buffer. Returns next y.
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
		nv := value.Clamp(1-float64(c.mouseY-slR.Y)/float64(slR.H), 0, 1)
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
		if c, ok := parse.HexColor(next); ok {
			col := sdl.Color(c)
			apply(col)
		}
	}
	c.Label(hx, y+42+fieldH+4, "hex code", ColTextDim)
	return y + diam + 14
}
