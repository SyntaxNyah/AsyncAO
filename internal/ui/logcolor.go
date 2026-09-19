package ui

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Inline-colour rendering for the IC log (#123): an IC message's inline colour
// markup now colours its log line, not just the live chatbox. The chatbox already
// parses markup into StyleRuns and resolves them to colours (buildColorSpans);
// this file maps those runs onto the log's wrapped display rows and draws them.

// inlineSpanAt returns the render colour spans for one wrapped log row's body:
// a leading default-colour span for the head (timestamp + "<speaker>: "), then
// the style runs covering the body runes [bodyOff, bodyOff+bodyLen). Pure — no
// SDL — so the mapping is drivable in a test.
func inlineSpanAt(styles []courtroom.StyleRun, headRunes, bodyOff, bodyLen int, def sdl.Color) []render.ColorSpan {
	out := make([]render.ColorSpan, 0, len(styles)+1)
	if headRunes > 0 {
		out = append(out, render.ColorSpan{Len: headRunes, Color: def})
	}
	if bodyLen <= 0 || len(styles) == 0 {
		return out
	}
	full := buildColorSpans(styles, def)
	pos := 0
	for _, s := range full {
		start, end := pos, pos+s.Len
		pos = end
		if end <= bodyOff {
			continue
		}
		if start >= bodyOff+bodyLen {
			break
		}
		lo, hi := start, end
		if lo < bodyOff {
			lo = bodyOff
		}
		if hi > bodyOff+bodyLen {
			hi = bodyOff + bodyLen
		}
		if hi <= lo {
			continue
		}
		out = append(out, render.ColorSpan{Len: hi - lo, Color: s.Color, Bold: s.Bold, Italic: s.Italic})
	}
	return out
}

// drawColoredLogRow draws one log row whose spans may carry more than one colour.
// A row that needs the emoji/mixed-script raster, or that no single face covers,
// falls back to a single colour — coverage wins over colour, the same rule the
// log's existing name-split already follows (logselect_wire.go logRowSplit).
func (a *App) drawColoredLogRow(font, emojiFont *ttf.Font, x, y, wrapW int32, text string, spans []render.ColorSpan) {
	c := a.ctx
	if len(spans) == 0 || (emojiFont != nil && render.NeedsEmojiFallback(text)) || !c.coversFace(font, text) {
		col := sdl.Color{}
		if len(spans) > 0 {
			col = spans[0].Color
		}
		a.labelEmoji(font, emojiFont, x, y, wrapW, text, col)
		return
	}
	rs := []rune(text)
	px, used := x, int32(0)
	pos := 0
	for _, s := range spans {
		n := s.Len
		if pos >= len(rs) {
			break
		}
		if pos+n > len(rs) {
			n = len(rs) - pos
		}
		if n <= 0 {
			continue
		}
		seg := string(rs[pos : pos+n])
		w := fontWidth(font, seg)
		c.LabelClippedFont(font, px, y, wrapW-used, seg, s.Color)
		px += w
		used += w
		pos += n
	}
}
