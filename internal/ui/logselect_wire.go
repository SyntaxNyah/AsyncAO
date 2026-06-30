package ui

// SDL/ttf wiring for IC/OOC log text selection. The pure model + hit-test +
// copy extraction live in logselect.go; this feeds them from the live logs,
// renders the highlight, and copies on Ctrl+C. All of it is gated on an active
// selection (or a press inside the log), so a log with nothing selected draws
// byte-identical to before.

import (
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// drawLogLineNamed draws one wrapped log line, tinting the speaker's name prefix
// in its per-speaker colour when name colours are on and speaker != "" (the
// caller passes "" for non-first rows and system lines, so no draw-time
// re-parsing of ": "). The rest of the line draws in col. Falls back to a plain
// draw otherwise. Shared by the IC and OOC log render paths.
func (a *App) drawLogLineNamed(font, emojiFont *ttf.Font, x, y, wrapW int32, line, speaker string, col sdl.Color, nameOn bool, sat, val float64, bold bool) {
	c := a.ctx
	if speaker != "" && (nameOn || bold) {
		// The speaker may sit AFTER a leading prefix ("16:11 LowGuy: …") when IC
		// timestamps are on, so locate it by index rather than HasPrefix: the prefix
		// (the timestamp, if any) bolds in the line colour, the name bolds + tints, and
		// the rest draws plain. This also lets name colours apply with timestamps on.
		// idx<0 (name not in the line — shouldn't happen) falls through to the plain draw.
		if idx := strings.Index(line, speaker); idx >= 0 {
			// An emoji line OR a mixed-script line no single face covers skips the
			// per-speaker split (one `font` can't do per-glyph faces) and renders whole
			// via the raster — coverage wins over the tint/bold for the rare mixed name.
			if (emojiFont != nil && render.NeedsEmojiFallback(line)) || !c.covers(line) {
				a.labelEmoji(font, emojiFont, x, y, wrapW, line, col)
				return
			}
			px, used := x, int32(0)
			if pre := line[:idx]; pre != "" { // timestamp / leading prefix — bold, line colour
				if pw, _, err := font.SizeUTF8(pre); err == nil {
					c.LabelClippedFont(font, px, y, wrapW-used, pre, col)
					if bold {
						c.LabelClippedFont(font, px+1, y, wrapW-used, pre, col)
					}
					px += int32(pw)
					used += int32(pw)
				}
			}
			if nw, _, err := font.SizeUTF8(speaker); err == nil {
				nameCol := col
				if nameOn {
					nameCol = nameColor(speaker, sat, val)
				}
				c.LabelClippedFont(font, px, y, wrapW-used, speaker, nameCol)
				if bold { // faux-bold: a 1px-shifted second pass thickens the strokes (no bold font needed)
					c.LabelClippedFont(font, px+1, y, wrapW-used, speaker, nameCol)
				}
				used += int32(nw)
				c.LabelClippedFont(font, px+int32(nw), y, wrapW-used, line[idx+len(speaker):], col)
				return
			}
		}
	}
	// Default path: labelEmoji is the byte-identical LabelClippedFont for plain
	// text (after one cheap scan), the cached multi-font raster for an emoji line.
	a.labelEmoji(font, emojiFont, x, y, wrapW, line, col)
}

const (
	logSelNone = iota
	logSelIC
	logSelOOC
)

// logSelAlpha keeps the highlight translucent so the text reads through it,
// whatever RGB the user picks in Settings.
const logSelAlpha = 96

// highlightFill is the configured selection colour (Settings → packed RGB) at
// the fixed translucent alpha. Read once per frame into a.logSelFill while a
// selection is active, so the per-line draw never locks prefs.
func (a *App) highlightFill() sdl.Color {
	rgb := a.d.Prefs.HighlightColorRGB()
	return sdl.Color{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: logSelAlpha}
}

// logLineCount / logLineText give the shared selection code random access to a
// log's CURRENT wrapped display lines. Selection only needs the displayed text
// and its row index, so IC (rows with a source entry) and OOC (flat strings)
// feed it the same way.
func (a *App) logLineCount(which int) int {
	if which == logSelIC {
		return len(a.icWrap)
	}
	return len(a.oocWrap)
}

func (a *App) logLineText(which, i int) string {
	if which == logSelIC {
		if i >= 0 && i < len(a.icWrap) {
			return a.icWrap[i].text
		}
		return ""
	}
	if i >= 0 && i < len(a.oocWrap) {
		return a.oocWrap[i]
	}
	return ""
}

// logPrefixWidth measures the first off runes of text in the log font — used
// only for the two partial-end lines of a selection (interior lines fill the
// whole column, so they need no measurement).
func logPrefixWidth(font *ttf.Font, runes []rune, off int) int32 {
	if font == nil || off <= 0 {
		return 0
	}
	if off > len(runes) {
		off = len(runes)
	}
	w, _, err := font.SizeUTF8(string(runes[:off]))
	if err != nil {
		return 0
	}
	return int32(w)
}

// logPointAt maps a pixel position to a selection point: the wrapped-line index
// from the scroll-adjusted y, and the rune offset from a binary search over
// that ONE line's prefix widths (no per-rune-per-row metrics).
func (a *App) logPointAt(which int, listX, listY, scroll, lineH, mx, my int32) selPoint {
	n := a.logLineCount(which)
	if n == 0 || lineH <= 0 {
		return selPoint{}
	}
	li := int((my - listY + scroll) / lineH)
	if li < 0 {
		li = 0
	}
	if li >= n {
		li = n - 1
	}
	runes := []rune(a.logLineText(which, li))
	font := a.ctx.LogFontFor(a.logPct, string(runes))
	off := hitTestRune(runes, mx-listX, func(r []rune) int32 { return logPrefixWidth(font, r, len(r)) })
	return selPoint{entry: li, off: off}
}

// handleLogSelect runs drag-select, click-to-clear, and Ctrl+C for one log. It
// must be called from the log's draw AFTER the scroll is finalized and BEFORE
// the line loop, so a real drag can swallow the frame's click (links/pins then
// don't also fire). No-op when the cursor isn't in this log and nothing is
// selected.
func (a *App) handleLogSelect(which int, list sdl.Rect, scroll, lineH, wrapW int32) {
	c := a.ctx
	n := a.logLineCount(which)
	if a.logSelActive {
		a.logSelFill = a.highlightFill() // cache once/frame; the per-line draw won't lock
	}
	inText := c.mouseX >= list.X && c.mouseX <= list.X+wrapW // exclude the scrollbar
	if a.logSelPressed && n > 0 && c.hovering(list) && inText {
		p := a.logPointAt(which, list.X, list.Y, scroll, lineH, c.mouseX, c.mouseY)
		a.logSelWhich = which
		a.logSelAnchor, a.logSelHead = p, p
		a.logSelActive = true
		a.logSelDragging = true
		a.chatSelActive = false // the log owns the highlight now (don't double-select with the chatbox)
	}
	if a.logSelDragging && a.logSelWhich == which {
		if c.mouseDown {
			a.logSelHead = a.logPointAt(which, list.X, list.Y, scroll, lineH, c.mouseX, c.mouseY)
		} else {
			a.logSelDragging = false
			if a.logSelAnchor.equal(a.logSelHead) {
				a.logSelActive = false // a plain click clears the selection
			} else {
				c.clicked = false // a real drag must not also open a link / pin
				c.focusID = ""    // ...and it unfocuses the IC/OOC input, so Ctrl+C copies the SELECTION, not the (still-focused) field
			}
		}
	}
	// Ctrl+C copies the active selection when no text field is focused (a
	// focused field's own copy wins). Consume copyReq so it fires once.
	if c.copyReq && c.focusID == "" && a.logSelActive && a.logSelWhich == which {
		lo, hi := orderSel(a.logSelAnchor, a.logSelHead)
		if !lo.equal(hi) {
			_ = sdl.SetClipboardText(selectedText(func(i int) string { return a.logLineText(which, i) }, lo, hi))
			a.warnLine = "Copied selection to clipboard"
			a.warnAt = time.Now()
		}
		c.copyReq = false
	}
	// Double-click selects the whole line under the cursor (the standard text
	// gesture) — highlight start→end so Ctrl+C / right-click copies the full line.
	// Replaces the old IC double-click-to-pair (pairing now lives on the player
	// list's Pair button). Event-gated: a normal frame never enters here.
	if c.dblClick && n > 0 && c.hovering(list) && inText {
		a.selectLogLine(which, list, scroll, lineH)
		c.clicked = false  // the line is selected — don't also open a link under it
		c.focusID = ""     // unfocus so Ctrl+C / right-click copies the selection
		c.dblClick = false // consume: this double-click was the log's
	}
	// Right-click copies. A deliberate SELECTION wins and is consumed (so a link
	// line's copy-URL and the unread pill don't also fire); otherwise copy the
	// whole line under the cursor WITHOUT consuming — an OOC link line then still
	// copies its URL (it runs after; last write wins) and the pill keeps its
	// gesture. Pin-to-notes moved to its chord (default Ctrl+N).
	if c.rightClicked && n > 0 && c.hovering(list) && inText {
		if a.logSelActive && a.logSelWhich == which {
			lo, hi := orderSel(a.logSelAnchor, a.logSelHead)
			if !lo.equal(hi) {
				_ = sdl.SetClipboardText(selectedText(func(i int) string { return a.logLineText(which, i) }, lo, hi))
				a.warnLine = "Copied selection to clipboard"
				a.warnAt = time.Now()
				c.rightClicked = false
			}
		}
		if c.rightClicked { // no selection consumed it: copy the hovered line
			li := a.logPointAt(which, list.X, list.Y, scroll, lineH, c.mouseX, c.mouseY).entry
			if t := a.logLineText(which, li); t != "" {
				_ = sdl.SetClipboardText(t)
				a.warnLine = "Copied line to clipboard"
				a.warnAt = time.Now()
			}
		}
	}
}

// selectLogLine selects the whole wrapped line under the cursor in log `which`
// (the double-click gesture). Anchors at the line's start and head at its end so
// the existing highlight + copy paths treat it like any drag selection.
func (a *App) selectLogLine(which int, list sdl.Rect, scroll, lineH int32) {
	c := a.ctx
	li := a.logPointAt(which, list.X, list.Y, scroll, lineH, c.mouseX, c.mouseY).entry
	a.logSelWhich = which
	a.logSelAnchor = selPoint{entry: li, off: 0}
	a.logSelHead = selPoint{entry: li, off: len([]rune(a.logLineText(which, li)))}
	a.logSelActive = true
	a.logSelDragging = false
}

// drawLogSelHighlight fills the selection background behind one display line
// (called inside the line loop, before the text). Interior lines fill the whole
// column; only the first/last partial lines measure an offset.
func (a *App) drawLogSelHighlight(which, li int, listX, y, wrapW, lineH int32, text string, font *ttf.Font) {
	if !a.logSelActive || a.logSelWhich != which {
		return
	}
	lo, hi := orderSel(a.logSelAnchor, a.logSelHead)
	if lo.equal(hi) || li < lo.entry || li > hi.entry {
		return
	}
	x0, x1 := listX, listX+wrapW
	runes := []rune(text)
	if li == lo.entry {
		x0 = listX + logPrefixWidth(font, runes, lo.off)
	}
	if li == hi.entry {
		x1 = listX + logPrefixWidth(font, runes, hi.off)
	}
	if x1 > x0 {
		a.ctx.Fill(sdl.Rect{X: x0, Y: y, W: x1 - x0, H: lineH}, a.logSelFill)
	}
}
