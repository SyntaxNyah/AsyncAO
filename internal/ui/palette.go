package ui

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Command palette (#39, Ctrl+Space): one fuzzy search over EVERYTHING —
// every Extras action (the canonical client-action table, so the two can't
// drift) plus this server's slash-command reference (software-aware, from the
// mod dashboard's data). Running an action fires it; picking a slash command
// INSERTS its form into the OOC input for you to fill in — the palette never
// sends a server command itself. Drawn over the courtroom only while open;
// zero cost otherwise.

const (
	paletteW       = int32(540)
	paletteRowH    = int32(24)
	paletteMaxN    = 12 // visible matches (the query narrows the rest)
	paletteInputID = "palette_q"
)

// paletteMatch is one filtered row: an Extras action (widget >= 0) or a slash
// command reference line (cmd != "").
type paletteMatch struct {
	label, hint string
	widget      int    // index into extrasWidgets(), -1 for command rows
	cmd         string // the command form to insert ("" for actions)
}

// fuzzyMatch reports whether every rune of query appears in s in order
// (case-insensitive) — the classic subsequence filter, cheap and forgiving.
func fuzzyMatch(s, query string) bool {
	if query == "" {
		return true
	}
	s, query = strings.ToLower(s), strings.ToLower(query)
	i := 0
	for _, r := range s {
		if i < len(query) && r == rune(query[i]) {
			i++
		}
	}
	return i == len(query)
}

// paletteCommandForm extracts the insertable command from a reference line
// ("Ban — /ban -i <ipid> …" → "/ban -i <ipid> …"); lines without a slash form
// (footnotes) return "".
func paletteCommandForm(line string) string {
	if i := strings.Index(line, "/"); i >= 0 {
		return strings.TrimSpace(line[i:])
	}
	return ""
}

// paletteMatches builds the filtered rows for the query: actions first (the
// things you run), then this server's slash commands. Bounded by paletteMaxN.
func (a *App) paletteMatches(query string) []paletteMatch {
	out := make([]paletteMatch, 0, paletteMaxN)
	for i, w := range a.extrasWidgets() {
		if len(out) >= paletteMaxN {
			return out
		}
		if fuzzyMatch(w.label+" "+w.desc, query) {
			out = append(out, paletteMatch{label: w.label, hint: w.desc, widget: i})
		}
	}
	if a.sess != nil {
		for _, line := range courtroom.CommandReference(a.detectedSoftware()) {
			if len(out) >= paletteMaxN {
				return out
			}
			cmd := paletteCommandForm(line)
			if cmd == "" || !fuzzyMatch(line, query) {
				continue
			}
			out = append(out, paletteMatch{label: cmd, hint: "server command — inserts into OOC", widget: -1, cmd: cmd})
		}
	}
	return out
}

// togglePalette opens/closes the palette (the Ctrl+Space hotkey).
func (a *App) togglePalette() {
	a.paletteOpen = !a.paletteOpen
	if a.paletteOpen {
		a.paletteQuery = ""
		a.paletteSel = 0
		a.ctx.focusID = paletteInputID // type immediately, and binds stand down
	} else if a.ctx.focusID == paletteInputID {
		a.ctx.focusID = ""
	}
}

// runPaletteMatch executes a picked row: an action fires (palette closes
// first, so an action that opens a screen isn't covered), a command form
// lands in the OOC input ready to fill in.
func (a *App) runPaletteMatch(m paletteMatch) {
	a.paletteOpen = false
	if a.ctx.focusID == paletteInputID {
		a.ctx.focusID = ""
	}
	if m.widget >= 0 {
		ws := a.extrasWidgets()
		if m.widget < len(ws) {
			ws[m.widget].run()
		}
		return
	}
	if m.cmd != "" {
		a.stashOOCUndo()         // a command template overwrites the OOC draft — Ctrl+Z recovers it
		a.oocInput = m.cmd       // both layouts' OOC fields read this
		a.ctx.focusID = "oocmsg" // caret into the default layout's OOC box
	}
}

// drawPalette renders the overlay: a centred search field + the filtered rows.
// ↑/↓ move, Enter runs, Esc closes (closeTopOverlay), click-outside closes.
func (a *App) drawPalette(w, h int32) {
	if !a.paletteOpen {
		return
	}
	c := a.ctx
	matches := a.paletteMatches(a.paletteQuery)
	if a.paletteSel >= len(matches) {
		a.paletteSel = len(matches) - 1
	}
	if a.paletteSel < 0 {
		a.paletteSel = 0
	}
	pw := paletteW
	if pw > w-2*pad {
		pw = w - 2*pad
	}
	ph := fieldH + 10 + int32(len(matches))*paletteRowH + 12
	px := (w - pw) / 2
	py := h / 6
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)

	prev := a.paletteQuery
	var submit bool
	a.paletteQuery, submit = c.TextField(paletteInputID, sdl.Rect{X: px + 6, Y: py + 6, W: pw - 12, H: fieldH}, a.paletteQuery, "type a command… (↑↓ pick · Enter run · Esc close)")
	if a.paletteQuery != prev {
		a.paletteSel = 0 // a new query restarts the selection at the best match
	}
	// Keyboard: ↑/↓ steer even while the field has focus (arrows don't type).
	switch c.keyPressed {
	case sdl.K_DOWN:
		if a.paletteSel < len(matches)-1 {
			a.paletteSel++
		}
		c.keyPressed = 0
	case sdl.K_UP:
		if a.paletteSel > 0 {
			a.paletteSel--
		}
		c.keyPressed = 0
	}
	ry := py + 6 + fieldH + 4
	for i, m := range matches {
		r := sdl.Rect{X: px + 4, Y: ry, W: pw - 8, H: paletteRowH - 2}
		if i == a.paletteSel {
			c.Fill(r, ColPanelHi)
			c.Border(r, ColAccent)
		} else if c.hovering(r) {
			c.Fill(r, ColPanel)
		}
		labW := c.TextWidth(m.label)
		c.LabelClipped(r.X+6, r.Y+3, r.W-12, m.label, ColText)
		if hintW := r.W - 12 - labW - 14; hintW > 60 {
			c.LabelClipped(r.X+6+labW+14, r.Y+3, hintW, m.hint, ColTextDim)
		}
		if c.clicked && c.hovering(r) {
			a.runPaletteMatch(m)
			c.clicked = false
			return
		}
		ry += paletteRowH
	}
	if len(matches) == 0 {
		c.LabelClipped(px+8, ry+2, pw-16, "no matches — try fewer letters", ColTextDim)
	}
	if submit && len(matches) > 0 {
		a.runPaletteMatch(matches[a.paletteSel])
		return
	}
	// A click outside the panel closes it (and doesn't leak underneath).
	if c.clicked && !pointIn(c.mouseX, c.mouseY, panel) {
		a.paletteOpen = false
		if c.focusID == paletteInputID {
			c.focusID = ""
		}
		c.clicked = false
	}
}
