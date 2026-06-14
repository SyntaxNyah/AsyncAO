package ui

// SDL/ttf wiring for IC/OOC log text selection. The pure model + hit-test +
// copy extraction live in logselect.go; this feeds them from the live logs,
// renders the highlight, and copies on Ctrl+C. All of it is gated on an active
// selection (or a press inside the log), so a log with nothing selected draws
// byte-identical to before.

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

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
