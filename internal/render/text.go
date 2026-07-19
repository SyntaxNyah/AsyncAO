package render

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// AO chat text colors (TEXT_COLOR field), AO2-Client palette.
var textColors = []sdl.Color{
	{R: 255, G: 255, B: 255, A: 255}, // 0 white
	{R: 0, G: 255, B: 0, A: 255},     // 1 green
	{R: 255, G: 0, B: 0, A: 255},     // 2 red
	{R: 255, G: 165, B: 0, A: 255},   // 3 orange
	{R: 45, G: 150, B: 255, A: 255},  // 4 blue
	{R: 255, G: 255, B: 0, A: 255},   // 5 yellow
	{R: 255, G: 192, B: 203, A: 255}, // 6 pink (legacy)
	{R: 0, G: 255, B: 255, A: 255},   // 7 cyan
	{R: 192, G: 192, B: 192, A: 255}, // 8 gray
}

// TextColorCount is the number of AO chat colors the palette defines —
// the IC color cycler wraps at this (MS text_color wire values 0..N-1).
var TextColorCount = len(textColors)

// DefaultDevScale is the "no device upscaling" font scale (100 = 1:1). A
// MessageRaster built at this scale draws its device-px textures 1:1 into
// logical space — the pre-#77 behavior, and what every export/offscreen path
// pins to so the live UI scale can't leak into export resolution.
const DefaultDevScale int32 = 100

// logicalFromDevice converts a device-pixel measurement back to LOGICAL pixels
// for the #77 crisp-scaling model: fonts are opened at pt×(devScale/100) so
// glyphs rasterize at final device size, and this divides the resulting device
// metric down to the logical rect the kit lays out in (the renderer's own
// SetScale then multiplies it back up 1:1 onto device pixels — no blur).
//
// ROUNDING RULE — "round half up" (add half the divisor before dividing). This
// is the roadmap's flagged off-by-one failure mode at odd/non-integer scales;
// it MUST match ui.devToLogical exactly (see internal/ui/ui.go), or a label and
// a message raster of the same string would disagree by a pixel. devScale<=0 or
// ==100 is the identity fast path.
func logicalFromDevice(device, devScale int32) int32 {
	if devScale <= 0 || devScale == DefaultDevScale {
		return device
	}
	return (device*DefaultDevScale + devScale/2) / devScale
}

// LogicalFromDevice exposes the #77 rounding rule so the ui package's
// cross-package test can assert its own uiLogicalFromDevice agrees exactly. Not
// used on any draw path — the internal logicalFromDevice is.
func LogicalFromDevice(device, devScale int32) int32 { return logicalFromDevice(device, devScale) }

// TextColor maps an AO color index to RGBA (out of range → white).
func TextColor(index int) sdl.Color {
	if index < 0 || index >= len(textColors) {
		return textColors[0]
	}
	return textColors[index]
}

// textColorNames parallels textColors: the labels the IC color dropdown
// shows (AO2-Client's set_text_color_dropdown lists names, not swatches).
var textColorNames = []string{
	"White", "Green", "Red", "Orange", "Blue", "Yellow", "Pink", "Cyan", "Gray",
}

// TextColorNames exposes the dropdown option list. Callers treat it as
// read-only (shared backing array).
func TextColorNames() []string { return textColorNames }

// Extended AsyncAO chat colors (#98). These are NOT wire text_color values —
// the AO MS text_color field MUST stay 0..8 or strict clients (LemmyAO-style)
// drop the whole message. They travel ONLY as inline \c<Code> markup, exactly
// like the \cr rainbow: another AsyncAO client renders Color precisely, while a
// standard AO2 client drops the unknown escape and falls back to Wire (the
// nearest standard palette index we ship in the MS text_color field). Codes
// avoid the reserved markup letters r/b/i and the \c lead-in 'c', plus digits
// 0..8 — the gate set is mirrored by courtroom.ExtColorCodes (a test pins it).
type ExtColor struct {
	Name  string
	Code  byte      // inline letter: \c<Code>
	Color sdl.Color // exact render color
	Wire  int       // nearest standard palette index (0..8) for non-AsyncAO clients
}

var extColors = []ExtColor{
	{"Purple", 'p', sdl.Color{R: 150, G: 90, B: 245, A: 255}, 4},
	{"Magenta", 'm', sdl.Color{R: 255, G: 70, B: 230, A: 255}, 6},
	{"Teal", 't', sdl.Color{R: 40, G: 200, B: 190, A: 255}, 7},
	{"Lime", 'l', sdl.Color{R: 170, G: 240, B: 70, A: 255}, 1},
	{"Gold", 'g', sdl.Color{R: 255, G: 200, B: 40, A: 255}, 5},
	{"Coral", 'o', sdl.Color{R: 255, G: 120, B: 90, A: 255}, 3},
	{"Sky", 'k', sdl.Color{R: 110, G: 200, B: 255, A: 255}, 4},
	{"Lavender", 'v', sdl.Color{R: 205, G: 170, B: 255, A: 255}, 4},
}

// ExtColorCount / ExtColorAt expose the extended palette (read-only) to the IC
// colour dropdown and the send path.
func ExtColorCount() int        { return len(extColors) }
func ExtColorAt(i int) ExtColor { return extColors[i] }

// ExtColorByCode resolves an inline \c<code> letter to its render color; ok is
// false for an unknown code, and the caller renders the message default.
func ExtColorByCode(code byte) (sdl.Color, bool) {
	for i := range extColors {
		if extColors[i].Code == code {
			return extColors[i].Color, true
		}
	}
	return textColors[0], false
}

// NearestTextColorIndex maps an arbitrary RGB to the closest standard AO
// palette index (0..8) by squared RGB distance — the wire text_color fallback
// for a custom hex pick (v1.52.0), same contract as ExtColor.Wire: the MS
// field must stay 0..8 or strict servers (LemmyAO-style) drop the message,
// while non-AsyncAO clients still see a sensible colour.
func NearestTextColorIndex(r, g, b uint8) int {
	best, bestD := 0, 1<<62
	for i := range textColors {
		dr := int(textColors[i].R) - int(r)
		dg := int(textColors[i].G) - int(g)
		db := int(textColors[i].B) - int(b)
		if d := dr*dr + dg*dg + db*db; d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// rasterLine is one wrapped line: a texture plus per-rune prefix advances so
// the reveal is a pure src-rect width (spec §12: no per-character
// layout, no texture churn).
type rasterLine struct {
	tex      *sdl.Texture
	w, h     int32
	runes    int
	advances []int32 // advances[i] = pixel width of the first i runes
}

// rasterSpan is one same-color run within a wrapped line (the multi-color
// sibling of rasterLine): its own texture, prefix advances, and the x offset
// where it sits on the line. Built only for styled messages.
type rasterSpan struct {
	tex      *sdl.Texture
	w, h     int32
	runes    int
	xOffset  int32
	yOff     int32 // baseline offset within the line (0 for same-font color spans; >0 aligns a shorter-ascent font in the emoji fallback)
	advances []int32
}

// MessageRaster pre-rasterizes a full message once; Draw reveals it by rune
// count with zero allocations per frame. A message is EITHER single-color
// (lines, from Rasterize) OR multi-color (styled, from RasterizeStyled) — the
// single-color path is untouched so plain messages can't regress.
type MessageRaster struct {
	lines  []rasterLine
	styled [][]rasterSpan // nil unless this is a multi-color message
	lineH  int32
	text   string
	// lineRanges[i] is display line i's [start,end) SOURCE-rune range in the
	// original text (wrap-dropped separators sit between ranges). Built by
	// every rasterize path; it's what lets the chatbox selection map pixels →
	// text runes and back (RuneAt / LineSpanX) without re-deriving the wrap.
	lineRanges []lineRange
	// centerOff[i] horizontally centers line i within the chatbox width (webAO's
	// "~~" prefix). nil = left-aligned (the default / common case). Set once by
	// Center after rasterizing — never on the per-frame Draw path.
	centerOff []int32
	// devScale is the device font scale (#77): the textures were rasterized at
	// font pt×(devScale/100), so Draw divides every device-px dst dimension back
	// to logical px (logicalFromDevice). 100 = 1:1 (the pre-#77 path / exports).
	// The selection metrics also map through devScale — PrefixWidth / LineSpanX /
	// Height / LineH return LOGICAL px, and RuneAt scales an incoming LOGICAL
	// point up to device — so caret/highlight geometry stays aligned at any scale.
	devScale int32
	srcGet   sdl.Rect // scratch
	dstGet   sdl.Rect // scratch
}

// Center aligns every wrapped line to the centre of alignW px — the webAO "~~"
// convention. Call once after Rasterize / RasterizeStyled / RasterizeFallback; lines
// already at or past alignW stay flush left. Off the per-frame path, so the small
// per-line slice is fine.
func (m *MessageRaster) Center(alignW int32) {
	// #77: line widths are DEVICE px (measured from the device glyphs), so the
	// centering column must be device px too — scale the LOGICAL alignW up to
	// match. Draw divides the resulting device offset back to logical.
	if m.devScale > 0 && m.devScale != DefaultDevScale {
		alignW = alignW * m.devScale / DefaultDevScale
	}
	off := func(lineW int32) int32 {
		if d := (alignW - lineW) / 2; d > 0 {
			return d
		}
		return 0
	}
	if m.styled != nil {
		m.centerOff = make([]int32, len(m.styled))
		for i, spans := range m.styled {
			if n := len(spans); n > 0 {
				last := spans[n-1]
				m.centerOff[i] = off(last.xOffset + last.w) // spans tile L→R: last's right edge = line width
			}
		}
		return
	}
	m.centerOff = make([]int32, len(m.lines))
	for i := range m.lines {
		m.centerOff[i] = off(m.lines[i].w)
	}
}

// lineOffset returns the centering offset for line i (0 when left-aligned).
func (m *MessageRaster) lineOffset(i int) int32 {
	if i < len(m.centerOff) {
		return m.centerOff[i]
	}
	return 0
}

// Rasterize renders text wrapped to wrapW pixels in the given font/color.
// Call once per message (render thread). Wrapping goes through wrapStyled's
// position-preserving ranges (not the old strings.Fields wrapText, which
// collapsed whitespace runs and lost rune positions): identical visuals for
// normal text, faithful spacing for the rest, and — the point — every line
// knows its source-rune range, so the chatbox selection can map pixels to
// text (RuneAt / LineSpanX / LineRange).
// wrapW is LOGICAL px and devScale (#77) the device font scale the caller
// opened font at (100 = 1:1); the wrap column is measured against the DEVICE
// glyphs, so wrapW scales up here, and Draw divides the device dst back to
// logical. Callers pass render.DefaultDevScale (100) for the pre-#77 behavior.
func Rasterize(ren *sdl.Renderer, font *ttf.Font, text string, wrapW int32, color sdl.Color, devScale int32) (*MessageRaster, error) {
	if devScale <= 0 {
		devScale = DefaultDevScale
	}
	wrapW = wrapW * devScale / DefaultDevScale // measure the wrap against the DEVICE glyphs
	m := &MessageRaster{text: text, lineH: int32(font.Height()), devScale: devScale}
	if strings.TrimSpace(text) == "" {
		return m, nil
	}
	runes := []rune(text)
	for _, lr := range wrapStyled(font, runes, wrapW) {
		rl, err := rasterizeLine(ren, font, string(runes[lr.start:lr.end]), color)
		if err != nil {
			m.Destroy()
			return nil, err
		}
		m.lines = append(m.lines, rl)
		m.lineRanges = append(m.lineRanges, lr)
	}
	return m, nil
}

func rasterizeLine(ren *sdl.Renderer, font *ttf.Font, line string, color sdl.Color) (rasterLine, error) {
	runes := []rune(line)
	rl := rasterLine{runes: len(runes), advances: make([]int32, len(runes)+1)}
	for i := 1; i <= len(runes); i++ {
		w, _, err := font.SizeUTF8(string(runes[:i]))
		if err != nil {
			return rl, err
		}
		rl.advances[i] = int32(w)
	}
	if line == "" {
		return rl, nil
	}
	surf, err := font.RenderUTF8Blended(line, color)
	if err != nil {
		return rl, err
	}
	defer surf.Free()
	tex, err := ren.CreateTextureFromSurface(surf)
	if err != nil {
		return rl, err
	}
	rl.tex = tex
	rl.w = surf.W
	rl.h = surf.H
	return rl, nil
}

// ColorSpan styles Len consecutive runes of a message. The UI builds these
// from the courtroom style runs — palette index / default already resolved to
// a concrete color, and rainbow pre-expanded into per-rune spans — so render
// needs no palette knowledge. Spans partition the message text in order.
type ColorSpan struct {
	Len    int
	Color  sdl.Color
	Bold   bool
	Italic bool
}

// spanStyle is the resolved per-rune style the raster groups consecutive runes
// by (a new texture each time it changes). Comparable, so grouping is `==`.
type spanStyle struct {
	color  sdl.Color
	bold   bool
	italic bool
}

// RasterizeStyled renders a multi-color message: each wrapped line is split
// into same-color spans (one texture each), laid out left to right. Reveal
// stays a rune-count walk with zero per-frame allocations. Render thread, once
// per message.
func RasterizeStyled(ren *sdl.Renderer, font *ttf.Font, text string, spans []ColorSpan, wrapW int32, devScale int32) (*MessageRaster, error) {
	if devScale <= 0 {
		devScale = DefaultDevScale
	}
	wrapW = wrapW * devScale / DefaultDevScale // measure the wrap against the DEVICE glyphs (see Rasterize)
	m := &MessageRaster{text: text, lineH: int32(font.Height()), styled: [][]rasterSpan{}, devScale: devScale}
	runes := []rune(text)
	if len(runes) == 0 {
		return m, nil
	}
	styles := perRuneStyles(runes, spans)
	for _, lr := range wrapStyled(font, runes, wrapW) {
		line, err := rasterizeStyledLine(ren, font, runes[lr.start:lr.end], styles[lr.start:lr.end])
		if err != nil {
			m.Destroy()
			return nil, err
		}
		m.styled = append(m.styled, line)
		m.lineRanges = append(m.lineRanges, lr)
	}
	return m, nil
}

// perRuneStyles flattens the span partition into one style per rune (guarding a
// short partition by repeating the last span's style).
func perRuneStyles(runes []rune, spans []ColorSpan) []spanStyle {
	out := make([]spanStyle, len(runes))
	i := 0
	for _, sp := range spans {
		st := spanStyle{color: sp.Color, bold: sp.Bold, italic: sp.Italic}
		for k := 0; k < sp.Len && i < len(runes); k++ {
			out[i] = st
			i++
		}
	}
	tail := spanStyle{color: sdl.Color{R: 255, G: 255, B: 255, A: 255}}
	if len(spans) > 0 {
		s := spans[len(spans)-1]
		tail = spanStyle{color: s.Color, bold: s.Bold, italic: s.Italic}
	}
	for ; i < len(runes); i++ {
		out[i] = tail
	}
	return out
}

type lineRange struct{ start, end int }

// wrapStyled greedily word-wraps runes to maxW px, returning each line's
// [start,end) rune range so colors map by index. Breaks at the last space
// before the limit, hard-breaks an over-long word, and splits on '\n'. Mirrors
// wrapText's visuals but preserves rune positions (Fields would drop them).
func wrapStyled(font *ttf.Font, runes []rune, maxW int32) []lineRange {
	n := len(runes)
	if maxW <= 0 {
		return []lineRange{{0, n}}
	}
	var out []lineRange
	lineStart, lastSpace := 0, -1
	for i := 0; i < n; {
		if runes[i] == '\n' {
			out = append(out, lineRange{lineStart, i})
			i++
			lineStart, lastSpace = i, -1
			continue
		}
		if runes[i] == ' ' {
			lastSpace = i
		}
		w, _, _ := font.SizeUTF8(string(runes[lineStart : i+1]))
		if int32(w) > maxW && i > lineStart {
			if lastSpace > lineStart {
				out = append(out, lineRange{lineStart, lastSpace}) // break at the space, drop it
				lineStart = lastSpace + 1
			} else {
				out = append(out, lineRange{lineStart, i}) // over-long word: hard break
				lineStart = i
			}
			lastSpace = -1
			continue // re-measure from the new line start
		}
		i++
	}
	out = append(out, lineRange{lineStart, n})
	return out
}

// rasterizeStyledLine builds one wrapped line's spans — a new span each time
// the style changes, each with its own texture and prefix advances.
func rasterizeStyledLine(ren *sdl.Renderer, font *ttf.Font, runes []rune, styles []spanStyle) ([]rasterSpan, error) {
	var spans []rasterSpan
	var x int32
	for i := 0; i < len(runes); {
		j := i + 1
		for j < len(runes) && styles[j] == styles[i] {
			j++
		}
		sp, err := buildSpan(ren, font, runes[i:j], styles[i], x, 0) // same font across the line → no baseline offset
		if err != nil {
			for k := range spans {
				if spans[k].tex != nil {
					_ = spans[k].tex.Destroy()
				}
			}
			return nil, err
		}
		spans = append(spans, sp)
		x += sp.w
		i = j
	}
	return spans, nil
}

// buildSpan rasterizes one same-style run at xOffset with prefix advances.
// Bold/italic ride synthetic SDL_ttf font styles, restored on return (defer) so
// the shared cached font never leaks a style into other (plain) text.
func buildSpan(ren *sdl.Renderer, font *ttf.Font, runes []rune, st spanStyle, xOffset, yOff int32) (rasterSpan, error) {
	style := ttf.STYLE_NORMAL
	if st.bold {
		style |= ttf.STYLE_BOLD
	}
	if st.italic {
		style |= ttf.STYLE_ITALIC
	}
	if style != ttf.STYLE_NORMAL {
		font.SetStyle(style)
		defer font.SetStyle(ttf.STYLE_NORMAL) // always reset, even on error/panic
	}
	sp := rasterSpan{runes: len(runes), xOffset: xOffset, yOff: yOff, advances: make([]int32, len(runes)+1)}
	for i := 1; i <= len(runes); i++ {
		w, _, err := font.SizeUTF8(string(runes[:i]))
		if err != nil {
			return sp, err
		}
		sp.advances[i] = int32(w)
	}
	surf, err := font.RenderUTF8Blended(string(runes), st.color)
	if err != nil {
		return sp, err
	}
	defer surf.Free()
	tex, err := ren.CreateTextureFromSurface(surf)
	if err != nil {
		return sp, err
	}
	sp.tex = tex
	sp.w = surf.W
	sp.h = surf.H
	return sp, nil
}

// Draw blits the first visibleRunes runes at (x, y). Full lines blit whole;
// the partial line reveals via src-rect width — O(lines) per frame, zero
// allocations, zero texture churn.
func (m *MessageRaster) Draw(ren *sdl.Renderer, visibleRunes int, x, y int32) {
	if m.styled != nil {
		m.drawStyled(ren, visibleRunes, x, y)
		return
	}
	remaining := visibleRunes
	lineY := y
	logLineH := logicalFromDevice(m.lineH, m.devScale) // #77: step lines in LOGICAL px
	for i := range m.lines {
		line := &m.lines[i]
		if remaining <= 0 {
			return
		}
		show := line.runes
		if remaining < show {
			show = remaining
		}
		if line.tex != nil && show > 0 {
			// src stays in DEVICE px (the atlas/texture is device-sized); dst is the
			// LOGICAL rect (÷devScale) — the renderer's SetScale multiplies it back up
			// 1:1 onto device pixels, so the glyphs stay crisp (no bilinear stretch).
			width := line.advances[show]
			m.srcGet = sdl.Rect{X: 0, Y: 0, W: width, H: line.h}
			m.dstGet = sdl.Rect{X: x + logicalFromDevice(m.lineOffset(i), m.devScale), Y: lineY,
				W: logicalFromDevice(width, m.devScale), H: logicalFromDevice(line.h, m.devScale)}
			_ = ren.Copy(line.tex, &m.srcGet, &m.dstGet)
		}
		remaining -= line.runes
		lineY += logLineH
	}
}

// drawStyled is Draw for a multi-color message: walk lines, then the spans
// within each line, revealing by rune count (same zero-alloc src-rect trick).
func (m *MessageRaster) drawStyled(ren *sdl.Renderer, visibleRunes int, x, y int32) {
	remaining := visibleRunes
	lineY := y
	logLineH := logicalFromDevice(m.lineH, m.devScale) // #77: step lines in LOGICAL px
	for li := range m.styled {
		spans := m.styled[li]
		if remaining <= 0 {
			return
		}
		lineX := x + logicalFromDevice(m.lineOffset(li), m.devScale)
		for i := range spans {
			sp := &spans[i]
			if remaining <= 0 {
				break
			}
			show := sp.runes
			if remaining < show {
				show = remaining
			}
			if sp.tex != nil && show > 0 {
				// src device-px, dst the LOGICAL rect (÷devScale) — see Draw. xOffset/yOff
				// are device-px layout offsets so they divide too (yOff baseline-aligns the
				// emoji fallback; 0 for plain color spans).
				width := sp.advances[show]
				m.srcGet = sdl.Rect{X: 0, Y: 0, W: width, H: sp.h}
				m.dstGet = sdl.Rect{X: lineX + logicalFromDevice(sp.xOffset, m.devScale), Y: lineY + logicalFromDevice(sp.yOff, m.devScale),
					W: logicalFromDevice(width, m.devScale), H: logicalFromDevice(sp.h, m.devScale)}
				_ = ren.Copy(sp.tex, &m.srcGet, &m.dstGet)
			}
			remaining -= sp.runes
		}
		lineY += logLineH
	}
}

// Lines returns the number of wrapped display lines — so the UI can size a
// selection highlight to the actual text block (webAO-style "highlight the message").
func (m *MessageRaster) Lines() int {
	if m.styled != nil {
		return len(m.styled)
	}
	return len(m.lines)
}

// TotalRunes returns the rasterized rune count (lines joined).
func (m *MessageRaster) TotalRunes() int {
	if m.styled != nil {
		total := 0
		for _, spans := range m.styled {
			for i := range spans {
				total += spans[i].runes
			}
		}
		return total
	}
	total := 0
	for _, l := range m.lines {
		total += l.runes
	}
	return total
}

// PrefixWidth returns the pixel width of the first n runes on the FIRST line —
// the text-field caret metric. Fields raster their value unwrapped (a single
// line), and the raster already stores the cumulative per-rune advances the
// DRAW uses, so a caret measured here lands exactly where the glyphs are.
// BOTH raster shapes are covered: the single-font `lines` path AND the
// multi-font `styled` path — RasterizeFallback (what text fields use for
// non-Latin/emoji) builds the styled shape, and reading only `lines` returned
// 0 for it, pinning the drawn caret to the field's left edge. Clamped.
func (m *MessageRaster) PrefixWidth(n int) int32 {
	return logicalFromDevice(m.prefixWidthDev(n), m.devScale) // #77: device advances → logical caret metric
}

// prefixWidthDev is PrefixWidth in DEVICE px (the raw stored advances).
func (m *MessageRaster) prefixWidthDev(n int) int32 {
	if n <= 0 {
		return 0
	}
	if m.styled != nil { // multi-font/multi-color: walk the first line's spans
		if len(m.styled) == 0 {
			return 0
		}
		w := int32(0)
		remaining := n
		for i := range m.styled[0] {
			sp := &m.styled[0][i]
			show := sp.runes
			if remaining < show {
				show = remaining
			}
			if show > 0 && show < len(sp.advances) {
				w = sp.xOffset + sp.advances[show] // same x math as drawStyled's dst
			}
			remaining -= sp.runes
			if remaining <= 0 {
				break
			}
		}
		return w
	}
	if len(m.lines) == 0 {
		return 0
	}
	line := &m.lines[0]
	if n > line.runes {
		n = line.runes
	}
	if n >= len(line.advances) { // defensive: advances is runes+1 entries
		if l := len(line.advances); l > 0 {
			return line.advances[l-1]
		}
		return 0
	}
	return line.advances[n]
}

// Height returns the rasterized message's full LOGICAL pixel height (all wrapped
// lines stacked at the logical line height), so a caller can size a box to fit
// it. It MUST match Draw's per-line logical step (logicalFromDevice(lineH)) — a
// box sized in device px would be devScale× too tall at >100%. Zero for empty.
func (m *MessageRaster) Height() int32 {
	n := len(m.lines)
	if m.styled != nil {
		n = len(m.styled)
	}
	return int32(n) * logicalFromDevice(m.lineH, m.devScale)
}

// Text returns the rasterized source text.
func (m *MessageRaster) Text() string { return m.text }

// --- selection geometry (chatbox partial-copy) -------------------------------
// All coordinates are relative to the Draw origin and use the SAME per-line
// advances / span offsets / centering the blits use, so a highlight can never
// drift from the pixels. Everything here is measurement over prebuilt slices:
// zero allocations, safe in the per-frame highlight loop.

// LineH is the wrapped-line pitch Draw advances by — LOGICAL px (#77), matching
// Draw's per-line logical step so a selection highlight lines up with the glyphs.
func (m *MessageRaster) LineH() int32 { return logicalFromDevice(m.lineH, m.devScale) }

// LineRange returns display line i's [start,end) source-rune range. A wrap
// point's dropped separator sits BETWEEN ranges, so a selection dragged
// across lines naturally includes it in the copied text.
func (m *MessageRaster) LineRange(i int) (int, int) {
	if i < 0 || i >= len(m.lineRanges) {
		return 0, 0
	}
	lr := m.lineRanges[i]
	return lr.start, lr.end
}

// linePrefixW is the pixel width of line li's first off drawn runes — the
// per-line sibling of PrefixWidth, covering both raster shapes.
func (m *MessageRaster) linePrefixW(li, off int) int32 {
	if off <= 0 {
		return 0
	}
	if m.styled != nil {
		if li < 0 || li >= len(m.styled) {
			return 0
		}
		w := int32(0)
		remaining := off
		for i := range m.styled[li] {
			sp := &m.styled[li][i]
			show := sp.runes
			if remaining < show {
				show = remaining
			}
			if show > 0 && show < len(sp.advances) {
				w = sp.xOffset + sp.advances[show] // same x math as drawStyled's dst
			}
			remaining -= sp.runes
			if remaining <= 0 {
				break
			}
		}
		return w
	}
	if li < 0 || li >= len(m.lines) {
		return 0
	}
	line := &m.lines[li]
	if off > line.runes {
		off = line.runes
	}
	if off >= len(line.advances) { // defensive: advances is runes+1 entries
		if l := len(line.advances); l > 0 {
			return line.advances[l-1]
		}
		return 0
	}
	return line.advances[off]
}

// RuneAt maps a point (relative to the Draw origin) to the nearest source-
// rune BOUNDARY — the text-field caret rule, so a drag-selection snaps the
// way native editors do. Clamps outside the block (above → 0-ish, below →
// the last line, past a line's end → its end boundary).
func (m *MessageRaster) RuneAt(relX, relY int32) int {
	n := len(m.lineRanges)
	if n == 0 || m.lineH <= 0 {
		return 0
	}
	// The caller works in LOGICAL px; linePrefixW / lineOffset / lineH are DEVICE
	// px (#77), so scale the incoming point UP to device once, here, and the whole
	// walk below stays in the raster's native units.
	relX, relY = m.deviceFromLogical(relX), m.deviceFromLogical(relY)
	li := int(relY / m.lineH)
	if relY < 0 {
		li = 0
	}
	if li >= n {
		li = n - 1
	}
	start, end := m.lineRanges[li].start, m.lineRanges[li].end
	drawn := end - start
	x := relX - m.lineOffset(li)
	if x <= 0 {
		return start
	}
	prevW := int32(0)
	for i := 1; i <= drawn; i++ {
		w := m.linePrefixW(li, i)
		if x < (prevW+w)/2 {
			return start + i - 1
		}
		prevW = w
	}
	return end
}

// deviceFromLogical scales a LOGICAL coordinate up to the raster's DEVICE space
// (#77) — the inverse of logicalFromDevice, for mapping incoming mouse points.
func (m *MessageRaster) deviceFromLogical(v int32) int32 {
	if m.devScale <= 0 || m.devScale == DefaultDevScale {
		return v
	}
	return v * m.devScale / DefaultDevScale
}

// LineSpanX returns the pixel x-range on display line i covered by the
// source-rune selection [lo,hi), centering included; ok=false when the
// selection doesn't touch this line's drawn runes.
func (m *MessageRaster) LineSpanX(i, lo, hi int) (int32, int32, bool) {
	if i < 0 || i >= len(m.lineRanges) || hi <= lo {
		return 0, 0, false
	}
	start, end := m.lineRanges[i].start, m.lineRanges[i].end
	if lo < start {
		lo = start
	}
	if hi > end {
		hi = end
	}
	if hi <= lo {
		return 0, 0, false
	}
	off := m.lineOffset(i)
	// #77: offsets/advances are DEVICE px; the caller draws the highlight in
	// LOGICAL space, so divide both edges down (matching Draw's dst division).
	x0 := logicalFromDevice(off+m.linePrefixW(i, lo-start), m.devScale)
	x1 := logicalFromDevice(off+m.linePrefixW(i, hi-start), m.devScale)
	return x0, x1, true
}

// Destroy frees all line/span textures. Render thread only.
func (m *MessageRaster) Destroy() {
	for i := range m.lines {
		if m.lines[i].tex != nil {
			_ = m.lines[i].tex.Destroy()
			m.lines[i].tex = nil
		}
	}
	m.lines = nil
	for _, spans := range m.styled {
		for i := range spans {
			if spans[i].tex != nil {
				_ = spans[i].tex.Destroy()
				spans[i].tex = nil
			}
		}
	}
	m.styled = nil
}
