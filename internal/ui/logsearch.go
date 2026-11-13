package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// Log find (Ctrl+F): the search bar already filters the IC log (icLogFiltered); this file focuses it from the
// keyboard and highlights where in each surviving row the query matched.

// searchFillColor is the translucent band behind each find match; fixed, not themed, so it reads on any skin.
var searchFillColor = sdl.Color{R: 255, G: 210, B: 70, A: 96}

// matchSpan is a rune range [start,end) of one match within a log row.
type matchSpan struct{ start, end int }

// searchMatches returns the rune ranges of every case-insensitive occurrence of query within text.
func searchMatches(text, query string) []matchSpan {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	lower := strings.ToLower(text)
	var out []matchSpan
	from := 0
	for {
		b := strings.Index(lower[from:], q)
		if b < 0 {
			break
		}
		b += from
		start := utf8.RuneCountInString(text[:b])
		end := start + utf8.RuneCountInString(text[b:b+len(q)])
		out = append(out, matchSpan{start: start, end: end})
		from = b + len(q)
		if from >= len(lower) {
			break
		}
	}
	return out
}

// focusLogSearch puts the caret into the log find field; the focus stays armed if the panel is off screen.
func (a *App) focusLogSearch() {
	if a.ctx != nil {
		a.ctx.focusID = "logsearch"
	}
}

// drawLogSearchHighlight fills the match band behind one display row, under the text like the selection highlight.
func (a *App) drawLogSearchHighlight(which, li int, listX, y, wrapW, lineH int32, text string, font *ttf.Font) {
	q := strings.TrimSpace(a.logSearch)
	if q == "" {
		return
	}
	matches := searchMatches(text, q)
	if len(matches) == 0 {
		return
	}
	runes := []rune(text)
	indent := a.logRowIndent(which, li)
	for _, m := range matches {
		x0 := listX + indent + a.logPrefixWidth(which, li, text, font, runes, m.start)
		x1 := listX + indent + a.logPrefixWidth(which, li, text, font, runes, m.end)
		if x1 > x0 {
			a.ctx.Fill(sdl.Rect{X: x0, Y: y, W: x1 - x0, H: lineH}, searchFillColor)
		}
	}
}
