package ui

import (
	"strings"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// Log find (Ctrl+F) — the search bar already filters the IC log (icLogFiltered);
// this file focuses it from the keyboard and highlights the matched words so a
// hit is visible at a glance, not just implied by the rows that survived the
// filter. The filter still runs: a non-empty query hides non-matching rows, and
// the highlight marks WHERE within each survivor the query matched.

// searchFillColor is the translucent band behind each find match (find-in-page
// convention: a warm tint the text stays legible through). Fixed, not themed —
// it has to read as "search hit" against any skin.
var searchFillColor = sdl.Color{R: 255, G: 210, B: 70, A: 96}

// matchSpan is a rune range [start,end) of one match within a log row.
type matchSpan struct{ start, end int }

// searchMatches returns the rune ranges of every case-insensitive occurrence of
// query within text. Pure — no SDL — so the matching is drivable in a test.
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

// focusLogSearch puts the caret into the log find field (the "logsearch" text
// field in the log panel). If that panel isn't on screen the focus is still
// armed, so the field is ready the moment the panel is next drawn.
func (a *App) focusLogSearch() {
	if a.ctx != nil {
		a.ctx.focusID = "logsearch"
	}
}

// drawLogSearchHighlight fills the search-match band behind one display row
// (called inside the line loop, under the text — the same layering the selection
// highlight uses). A non-empty a.logSearch must already be active.
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
