package render

import (
	"math"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// AO chat text colors (TEXT_COLOR field), AO2-Client palette.
//
//nolint:mnd // the AO2 palette, a named data table (rule 9)
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

// TextColorCount is the number of AO chat colors; MS text_color wire values are 0..N-1.
var TextColorCount = len(textColors)

// DefaultDevScale is 100 = no upscaling; exports pin to it so the UI scale can't leak into export resolution.
const DefaultDevScale int32 = 100

// logicalFromDevice converts a device-pixel metric to logical px, as fonts open at pt*(devScale/100).
func logicalFromDevice(device, devScale int32) int32 {
	if devScale <= 0 || devScale == DefaultDevScale {
		return device
	}
	return (device*DefaultDevScale + devScale/2) / devScale
}

// LogicalFromDevice exposes the rounding rule so the ui package can check its copy agrees; no draw path uses it.
func LogicalFromDevice(device, devScale int32) int32 { return logicalFromDevice(device, devScale) }

// deviceFromLogicalAt is the inverse of logicalFromDevice, shared with Badge so the arithmetic can't drift.
func deviceFromLogicalAt(v, devScale int32) int32 {
	if devScale <= 0 || devScale == DefaultDevScale {
		return v
	}
	return (v*devScale + DefaultDevScale/2) / DefaultDevScale
}

// DeviceFromLogicalAt exposes the rounding rule so the ui package can check its copy agrees; no draw path uses it.
func DeviceFromLogicalAt(v, devScale int32) int32 { return deviceFromLogicalAt(v, devScale) }

// deviceExactAt reports whether a texture at devScale draws device-exact at renderPct; both are percents.
func deviceExactAt(renderPct, devScale int32) bool {
	return renderPct > 0 && renderPct != DefaultDevScale && renderPct == devScale
}

// TextColor maps an AO color index to RGBA, white when out of range.
func TextColor(index int) sdl.Color {
	if index < 0 || index >= len(textColors) {
		return textColors[0]
	}
	return textColors[index]
}

// textColorNames parallels textColors, as AO2-Client's set_text_color_dropdown lists names.
var textColorNames = []string{
	"White", "Green", "Red", "Orange", "Blue", "Yellow", "Pink", "Cyan", "Gray",
}

// TextColorNames returns the dropdown options; callers must treat them as read-only.
func TextColorNames() []string { return textColorNames }

// ExtColor is an AsyncAO colour sent as \c<Code> markup: MS text_color must stay 0..8 or strict clients drop it.
// Codes avoid r/b/i, c and digits 0..8, and must match courtroom.ExtColorCodes.
type ExtColor struct {
	Name  string
	Code  byte      // inline letter: \c<Code>
	Color sdl.Color // exact render color
	Wire  int       // nearest standard palette index (0..8) for non-AsyncAO clients
}

//nolint:mnd // a named colour table (rule 9)
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

// ExtColorCount and ExtColorAt expose the extended palette (read-only).
func ExtColorCount() int        { return len(extColors) }
func ExtColorAt(i int) ExtColor { return extColors[i] }

// ExtColorByCode resolves an inline \c<code> letter; ok is false for an unknown code.
func ExtColorByCode(code byte) (sdl.Color, bool) {
	for i := range extColors {
		if extColors[i].Code == code {
			return extColors[i].Color, true
		}
	}
	return textColors[0], false
}

// NearestTextColorIndex maps RGB to the closest AO palette index, as MS text_color must stay 0..8.
func NearestTextColorIndex(r, g, b uint8) int {
	best, bestD := 0, math.MaxInt
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

// rasterLine is one wrapped line: a texture plus per-rune advances, so the reveal is just a src-rect width.
type rasterLine struct {
	tex      *sdl.Texture
	w, h     int32
	runes    int
	advances []int32 // advances[i] = pixel width of the first i runes
}

// rasterSpan is one same-color run within a line: its texture, advances and x offset.
type rasterSpan struct {
	tex      *sdl.Texture
	w, h     int32
	runes    int
	xOffset  int32
	yOff     int32 // baseline offset within the line (0 for same-font color spans; >0 aligns a shorter-ascent font in the emoji fallback)
	advances []int32
}

// MessageRaster rasterizes a message once; Draw reveals by rune count without allocating. One of lines or styled.
type MessageRaster struct {
	lines  []rasterLine
	styled [][]rasterSpan // nil unless this is a multi-color message
	lineH  int32
	text   string
	// lineRanges[i] is line i's [start,end) source-rune range; wrap-dropped separators sit between ranges.
	lineRanges []lineRange
	// centerOff[i] centres line i (webAO's "~~" prefix); nil = left-aligned. Set once, by Center.
	centerOff []int32
	// devScale is the device font scale; textures are device-sized, while selection metrics use logical px.
	devScale int32
	srcGet   sdl.Rect // scratch
	dstGet   sdl.Rect // scratch
	cgoClip  sdl.Rect // scratch for the device-exact clip re-assert (&local would escape through cgo)
}

// Center aligns lines to the centre of alignW (webAO "~~"); call once after rasterizing.
func (m *MessageRaster) Center(alignW int32) {
	// Line widths are device px, so the centring column must be too.
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

func (m *MessageRaster) lineOffset(i int) int32 {
	if i < len(m.centerOff) {
		return m.centerOff[i]
	}
	return 0
}

// Rasterize renders text wrapped to wrapW logical px with a font opened at devScale; render thread, once per message.
func Rasterize(ren *sdl.Renderer, font *ttf.Font, text string, wrapW int32, color sdl.Color, devScale int32) (*MessageRaster, error) {
	if devScale <= 0 {
		devScale = DefaultDevScale
	}
	wrapW = wrapW * devScale / DefaultDevScale // measure the wrap against the DEVICE glyphs
	m := &MessageRaster{text: text, lineH: FontLineSpacing(font), devScale: devScale}
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

// ColorSpan styles Len consecutive runes with resolved colours; spans partition the text in order.
type ColorSpan struct {
	Len    int
	Color  sdl.Color
	Bold   bool
	Italic bool
}

// spanStyle is the per-rune style runes are grouped by; comparable, so grouping is ==.
type spanStyle struct {
	color  sdl.Color
	bold   bool
	italic bool
}

// RasterizeStyled renders a multi-color message with one texture per same-color span; render thread, once per message.
func RasterizeStyled(ren *sdl.Renderer, font *ttf.Font, text string, spans []ColorSpan, wrapW int32, devScale int32) (*MessageRaster, error) {
	if devScale <= 0 {
		devScale = DefaultDevScale
	}
	wrapW = wrapW * devScale / DefaultDevScale // measure the wrap against the DEVICE glyphs (see Rasterize)
	m := &MessageRaster{text: text, lineH: FontLineSpacing(font), styled: [][]rasterSpan{}, devScale: devScale}
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

// perRuneStyles flattens spans to one style per rune, repeating the last for a short partition.
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
	tail := spanStyle{color: sdl.Color{R: math.MaxUint8, G: math.MaxUint8, B: math.MaxUint8, A: math.MaxUint8}}
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

// wrapStyled word-wraps runes to maxW, returning each line's [start,end) rune range so colors map by index.
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

// rasterizeStyledLine builds one line's spans, with a new span at each style change.
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

// buildSpan rasterizes one same-style run; font styles are restored so the shared font doesn't leak them.
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

// Draw blits the first visibleRunes runes without allocating; prefer DrawScaled when the scale is known.
func (m *MessageRaster) Draw(ren *sdl.Renderer, visibleRunes int, x, y int32) {
	m.draw(ren, visibleRunes, x, y, false, nil) // scaled path never brackets the scale, so no clip to re-assert
}

// DrawScaled is Draw given the renderer's scale; a match goes device-exact, keeping glyphs stable during the crawl.
func (m *MessageRaster) DrawScaled(ren *sdl.Renderer, visibleRunes int, x, y, renderPct int32, clip *sdl.Rect) {
	m.draw(ren, visibleRunes, x, y, m.deviceExact(renderPct), clip)
}

// deviceExact reports whether renderPct equals the raster's scale, needed for a 1:1 blit at the right size.
func (m *MessageRaster) deviceExact(renderPct int32) bool {
	return deviceExactAt(renderPct, m.devScale)
}

// proj maps a device-px measurement into the blit's space: unchanged when exact, else logical.
func (m *MessageRaster) proj(v int32, exact bool) int32 {
	if exact {
		return v
	}
	return logicalFromDevice(v, m.devScale)
}

func (m *MessageRaster) linePitch(exact bool) int32 {
	logical := logicalFromDevice(m.lineH, m.devScale)
	if exact {
		return m.deviceFromLogical(logical)
	}
	return logical
}

// draw is the shared body of Draw and DrawScaled; exact selects the device-exact projection.
func (m *MessageRaster) draw(ren *sdl.Renderer, visibleRunes int, x, y int32, exact bool, clip *sdl.Rect) {
	if exact {
		// The origin uses the same rule as RuneAt, so a click lands on the glyph it points at.
		x, y = m.deviceFromLogical(x), m.deviceFromLogical(y)
		// Drop to 1:1 before re-asserting the clip: backends disagree on when a scaled clip is converted.
		_ = ren.SetScale(1, 1)
		if clip != nil {
			m.cgoClip = sdl.Rect{
				X: m.deviceFromLogical(clip.X), Y: m.deviceFromLogical(clip.Y),
				W: m.deviceFromLogical(clip.W), H: m.deviceFromLogical(clip.H),
			}
			_ = ren.SetClipRect(&m.cgoClip) // scratch field: &local would escape through cgo
		}
	}
	if m.styled != nil {
		m.drawStyled(ren, visibleRunes, x, y, exact)
	} else {
		m.drawLines(ren, visibleRunes, x, y, exact)
	}
	if exact {
		// Recompute rather than GetScale through cgo: deviceExact means devScale equals renderPct.
		s := float32(m.devScale) / float32(DefaultDevScale)
		_ = ren.SetScale(s, s)
		if clip != nil {
			// Restore the caller's logical clip under the restored scale, so later draws clip as before on any backend.
			m.cgoClip = *clip
			_ = ren.SetClipRect(&m.cgoClip)
		}
	}
}

func (m *MessageRaster) drawLines(ren *sdl.Renderer, visibleRunes int, x, y int32, exact bool) {
	remaining := visibleRunes
	lineY := y
	lineStep := m.linePitch(exact) // #77: the folded logical pitch, in the blit's units
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
			// src stays in device px; dst is the same rect when exact, else logical for SetScale.
			width := line.advances[show]
			m.srcGet = sdl.Rect{X: 0, Y: 0, W: width, H: line.h}
			m.dstGet = sdl.Rect{X: x + m.proj(m.lineOffset(i), exact), Y: lineY,
				W: m.proj(width, exact), H: m.proj(line.h, exact)}
			_ = ren.Copy(line.tex, &m.srcGet, &m.dstGet)
		}
		remaining -= line.runes
		lineY += lineStep
	}
}

// drawStyled walks lines, then spans, revealing by rune count.
func (m *MessageRaster) drawStyled(ren *sdl.Renderer, visibleRunes int, x, y int32, exact bool) {
	remaining := visibleRunes
	lineY := y
	lineStep := m.linePitch(exact) // #77: the folded logical pitch, in the blit's units
	for li := range m.styled {
		spans := m.styled[li]
		if remaining <= 0 {
			return
		}
		lineX := x + m.proj(m.lineOffset(li), exact)
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
				// xOffset/yOff are device-px, so they project too; yOff baseline-aligns the emoji fallback.
				width := sp.advances[show]
				m.srcGet = sdl.Rect{X: 0, Y: 0, W: width, H: sp.h}
				m.dstGet = sdl.Rect{X: lineX + m.proj(sp.xOffset, exact), Y: lineY + m.proj(sp.yOff, exact),
					W: m.proj(width, exact), H: m.proj(sp.h, exact)}
				_ = ren.Copy(sp.tex, &m.srcGet, &m.dstGet)
			}
			remaining -= sp.runes
		}
		lineY += lineStep
	}
}

func (m *MessageRaster) Lines() int {
	if m.styled != nil {
		return len(m.styled)
	}
	return len(m.lines)
}

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

// PrefixWidth returns the width of the first n runes on the first line, the text-field caret metric.
func (m *MessageRaster) PrefixWidth(n int) int32 {
	return logicalFromDevice(m.prefixWidthDev(n), m.devScale) // #77: device advances → logical caret metric
}

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

// Height returns the logical height; it must match Draw's logical step, as device px is too tall above 100%.
func (m *MessageRaster) Height() int32 {
	n := len(m.lines)
	if m.styled != nil {
		n = len(m.styled)
	}
	return int32(n) * logicalFromDevice(m.lineH, m.devScale)
}

func (m *MessageRaster) Text() string { return m.text }

// Selection geometry: relative to the Draw origin, using the blits' own advances, so highlights can't drift.

// LineH returns Draw's logical line step, so a highlight lines up with the glyphs.
func (m *MessageRaster) LineH() int32 { return logicalFromDevice(m.lineH, m.devScale) }

// LineRange returns line i's [start,end) rune range; a dropped separator sits between ranges.
func (m *MessageRaster) LineRange(i int) (int, int) {
	if i < 0 || i >= len(m.lineRanges) {
		return 0, 0
	}
	lr := m.lineRanges[i]
	return lr.start, lr.end
}

// RowOfRune is rune n's display row, for AO2's ensureCursorVisible (courtroom.cpp:4514-4521).
func (m *MessageRaster) RowOfRune(n int) int {
	if m == nil || len(m.lineRanges) == 0 || n <= 0 {
		return 0
	}
	for i := len(m.lineRanges) - 1; i >= 0; i-- {
		if m.lineRanges[i].start <= n {
			return i
		}
	}
	return 0
}

// linePrefixW is the width of line li's first off drawn runes, for both raster shapes.
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

// RuneAt maps a point relative to the Draw origin to the nearest rune boundary, clamped to the block.
func (m *MessageRaster) RuneAt(relX, relY int32) int {
	n := len(m.lineRanges)
	if n == 0 || m.lineH <= 0 {
		return 0
	}
	// The caller is in logical px; the walk below is in device px.
	relX, relY = m.deviceFromLogical(relX), m.deviceFromLogical(relY)
	// Use Draw's stacking pitch, not raw lineH: they differ by up to a pixel per line, and the error accumulates.
	pitch := m.linePitch(true)
	if pitch <= 0 {
		pitch = m.lineH
	}
	li := int(relY / pitch)
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

// deviceFromLogical is the inverse of logicalFromDevice, for mouse points.
func (m *MessageRaster) deviceFromLogical(v int32) int32 {
	return deviceFromLogicalAt(v, m.devScale)
}

// LineSpanX returns line i's x-range for selection [lo,hi); ok=false when it doesn't touch the line.
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
	// Offsets and advances are device px, but the highlight draws in logical space.
	x0 := logicalFromDevice(off+m.linePrefixW(i, lo-start), m.devScale)
	x1 := logicalFromDevice(off+m.linePrefixW(i, hi-start), m.devScale)
	return x0, x1, true
}

// Destroy frees all textures; render thread only.
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
