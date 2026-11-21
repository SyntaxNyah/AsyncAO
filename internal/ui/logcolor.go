package ui

import (
	"strings"
	"unicode"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// Inline-colour rendering for the IC log: maps the chatbox's StyleRuns (see buildColorSpans) onto the log's wrapped
// rows.

// collapseStyled folds whitespace as the log's word wrap does (strings.Fields), keeping the head length and the body's
// style runs in step with the folded text; body runes past the runs take the default colour.
func collapseStyled(text string, headRunes int, styles []courtroom.StyleRun) (string, int, []courtroom.StyleRun) {
	src := []rune(text)
	body := make([]courtroom.StyleRun, 0, len(src))
	for _, s := range styles {
		one := s
		one.Len = 1
		for range s.Len {
			body = append(body, one)
		}
	}
	var b strings.Builder
	head := 0
	out := make([]courtroom.StyleRun, 0, len(styles))
	keep := func(r rune, i int) {
		b.WriteRune(r)
		if i < headRunes {
			head++
			return
		}
		run := courtroom.StyleRun{Len: 1, Color: courtroom.ColorDefault}
		if k := i - headRunes; k < len(body) {
			run = body[k]
		}
		if n := len(out); n > 0 && out[n-1].Color == run.Color && out[n-1].Bold == run.Bold && out[n-1].Italic == run.Italic {
			out[n-1].Len++
			return
		}
		out = append(out, run)
	}
	space := -1 // the first rune of a whitespace run after kept text; it becomes the run's one space
	for i, r := range src {
		if unicode.IsSpace(r) {
			if space < 0 && b.Len() > 0 {
				space = i
			}
			continue
		}
		if space >= 0 {
			keep(' ', space)
			space = -1
		}
		keep(r, i)
	}
	return b.String(), head, out
}

// inlineSpanAt returns the colour spans for one wrapped log row: a default-colour span for the head (timestamp and
// speaker), then the style runs covering body runes [bodyOff, bodyOff+bodyLen). Pure, so it is testable without SDL.
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
