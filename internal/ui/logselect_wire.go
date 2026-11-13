package ui

// SDL/ttf wiring for IC/OOC log text selection. The pure model, hit-test and copy extraction are in logselect.go;
// this feeds them from the live logs, renders the highlight and copies on Ctrl+C. It only acts with an active
// selection or a press inside the log.

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// logRowSplit decides whether a log row draws as a bold speaker span (the leading prefix plus the speaker name)
// followed by plain message text, or as one plain-weight run, and where it splits. drawLogLineNamed draws it and
// logPrefixWidth measures it for selection, and neither may re-derive it, or the selection measures a row at plain
// weight while the draw puts a bold speaker on it.
// ok=false means the whole line draws and measures at plain weight via a.labelEmoji: no speaker, name colours and
// bold both off, speaker not in the line, a line no single face covers (or needing the emoji/mixed-script raster),
// or either segment's width lookup failing.
// boldEnd is a byte offset into line (nameStart+len(speaker)); convert with utf8.RuneCountInString for rune offsets.
// Both width lookups are proven here before either caller commits, so a failing SizeUTF8 on the speaker can't
// leave the prefix painted and then the whole line painted over it.
func (c *Ctx) logRowSplit(font, emojiFont *ttf.Font, line, speaker string, nameOn, bold bool) (nameStart, boldEnd int, ok bool) {
	if speaker == "" || !(nameOn || bold) {
		return 0, 0, false
	}
	// The speaker may follow a leading prefix ("16:11 LowGuy: …") when IC timestamps are on, so locate it by index.
	// The
	// prefix bolds in the line colour, the name bolds and tints, the rest draws plain.
	idx := strings.Index(line, speaker)
	if idx < 0 {
		return 0, 0, false // name not in the line — shouldn't happen; fall back to plain
	}
	// An emoji line or a mixed-script line no single face covers skips the per-speaker split and renders whole via
	// the
	// raster; coverage wins over the tint and bold.
	if (emojiFont != nil && render.NeedsEmojiFallback(line)) || !c.coversFace(font, line) {
		return 0, 0, false
	}
	// Widths come from the memo, never font.SizeUTF8 directly: this runs for every named row every frame, and a raw
	// SizeUTF8 is a cgo call into SDL_ttf's shaper.
	if pre := line[:idx]; pre != "" {
		if _, ok := c.fontTextWidthWeight(font, pre, bold); !ok {
			return 0, 0, false
		}
	}
	if _, ok := c.fontTextWidthWeight(font, speaker, bold); !ok {
		return 0, 0, false
	}
	return idx, idx + len(speaker), true
}

// drawLogLineNamed draws one wrapped log line, tinting the speaker's name prefix in its per-speaker colour when
// name colours are on and speaker != "" (the caller passes "" for non-first rows and system lines). The rest draws
// in col; otherwise a plain draw. Shared by the IC and OOC log paths.
func (a *App) drawLogLineNamed(font, emojiFont *ttf.Font, x, y, wrapW int32, line, speaker string, col sdl.Color, nameOn bool, sat, val float64, bold bool) {
	c := a.ctx
	// logRowSplit proves both segments will measure before anything is drawn, so there is no partial-draw state to
	// fall into the plain-weight fallback below.
	if nameStart, boldEnd, ok := c.logRowSplit(font, emojiFont, line, speaker, nameOn, bold); ok {
		// The weight rides the raster (LabelClippedFontWeight, SDL_ttf STYLE_BOLD), not a second pass one pixel to
		// the
		// right: at a fractional UI scale two copies land on different sub-pixel phases and the name reads doubled.
		// Widths
		// are measured at the same weight, so the message starts where the bold name ends.
		px, used := x, int32(0)
		if pre := line[:nameStart]; pre != "" { // timestamp / leading prefix — bold, line colour
			pw, _ := c.fontTextWidthWeight(font, pre, bold) // logRowSplit already proved this succeeds
			c.LabelClippedFontWeight(font, px, y, wrapW-used, pre, col, bold)
			px += pw
			used += pw
		}
		nw, _ := c.fontTextWidthWeight(font, speaker, bold) // logRowSplit already proved this succeeds
		nameCol := col
		if nameOn {
			nameCol = nameColor(speaker, sat, val)
		}
		c.LabelClippedFontWeight(font, px, y, wrapW-used, speaker, nameCol, bold)
		used += nw
		c.LabelClippedFont(font, px+nw, y, wrapW-used, line[boldEnd:], col)
		return
	}
	// Default path: labelEmoji is the plain LabelClippedFont for plain text, the cached multi-font raster for emoji.
	a.labelEmoji(font, emojiFont, x, y, wrapW, line, col)
}

const (
	logSelNone = iota
	logSelIC
	logSelOOC
)

// logSelAlpha keeps the highlight translucent so the text reads through it, whatever RGB is picked in Settings.
const logSelAlpha = 96

// highlightFill is the configured selection colour (packed RGB) at the fixed alpha. Read once per frame into
// a.logSelFill while a selection is active, so the line draw never locks prefs.
func (a *App) highlightFill() sdl.Color {
	rgb := a.d.Prefs.Load().HighlightColorRGB()
	return sdl.Color{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: logSelAlpha}
}

// logLineCount / logLineText give the shared selection code random access to a log's current wrapped display lines.
// IC (rows with a source entry) and OOC (flat strings) feed it the same way.
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

// logPrefixWidth measures the first off runes of display row li of log `which` in the same weight(s)
// drawLogLineNamed draws it with; it shares logRowSplit so the two agree on where the bold speaker span ends.
// Used for the two partial-end lines of a selection (interior lines fill the column).
// Measuring at plain weight under-measures a named row by the bold/plain advance delta, so the mouse had to travel
// past the last glyph before hitTestRune concluded the line was fully covered.
// Every partial-row width goes through devTextWidthWeight, not fontTextWidthWeight: it does what the row's draw
// does (device-face SizeUTF8, uiLogicalFromDevice fold). The two faces are separately hinted and agree only at
// textDevPct==100; at a fractional UI scale a logical-only measure drifts, growing along a long wrapped row, until
// the highlight and copy land on different runes than the ones drawn. Both branches fold with the same
// c.textDevPct, so a bold+plain split sums to one device-exact width.
func (a *App) logPrefixWidth(which, li int, text string, font *ttf.Font, runes []rune, off int) int32 {
	c := a.ctx
	if font == nil || off <= 0 {
		return 0
	}
	if off > len(runes) {
		off = len(runes)
	}
	speaker, nameOn, bold := a.logRowNameStyle(which, li)
	if _, boldEndByte, ok := c.logRowSplit(font, a.logRowEmojiFont(which), text, speaker, nameOn, bold); ok {
		// boldEndByte is a byte offset into `text`; runes/off are rune-indexed, so fold it once.
		boldEnd := utf8.RuneCountInString(text[:boldEndByte])
		if off <= boldEnd {
			w, _ := c.devTextWidthWeight(font, string(runes[:off]), bold)
			return uiLogicalFromDevice(w, c.textDevPct)
		}
		boldW, _ := c.devTextWidthWeight(font, string(runes[:boldEnd]), bold)
		restW, _ := c.devTextWidthWeight(font, string(runes[boldEnd:off]), false)
		return uiLogicalFromDevice(boldW, c.textDevPct) + uiLogicalFromDevice(restW, c.textDevPct)
	}
	// No split: the whole row drew and is measured at plain weight (such as a MOTD continuation row).
	w, _ := c.devTextWidthWeight(font, string(runes[:off]), false)
	return uiLogicalFromDevice(w, c.textDevPct)
}

// logRowFont is the face one display row of log `which` is drawn in. The IC and OOC logs are separate
// courtroom_fonts.ini elements with their own sizes and families, so the hit-test asks per log.
func (a *App) logRowFont(which int, text string) *ttf.Font {
	if which == logSelIC {
		return a.elemFontFor(elemICChatlog, a.logPct, text)
	}
	return a.elemFontFor(elemServerChatlog, a.oocPct, text)
}

// logRowEmojiFont is logRowFont's emoji twin: the colour-emoji face at the same resolved scale the row's text draws
// at, so logRowSplit's "needs the per-glyph raster" check matches drawLogLineNamed's.
func (a *App) logRowEmojiFont(which int) *ttf.Font {
	if which == logSelIC {
		return a.elemEmoji(elemICChatlog, a.logPct)
	}
	return a.elemEmoji(elemServerChatlog, a.oocPct)
}

// logRowBoldPref reports whether log `which` draws speaker names bold: the global BoldNamesOn toggle OR'd with that
// log's theme "*_bold" override (IC and OOC dress separate elements). The draw loop and logRowNameStyle both read it
// through this one function.
func (a *App) logRowBoldPref(which int) bool {
	if which == logSelIC {
		return a.d.Prefs.Load().BoldNamesOn() || a.elemBold(elemICChatlog)
	}
	return a.d.Prefs.Load().BoldNamesOn() || a.elemBold(elemServerChatlog)
}

// logRowNameStyle resolves the (speaker, nameOn, bold) triple drawLogLineNamed draws display row li of log `which`
// with. OOC bakes its speaker into oocWrapName at wrap time (empty except an entry's first row); IC re-derives it
// each call, since it also depends on the live ghost span (ghosttext.go) and NameColorsOn, which the wrap cache
// doesn't carry. Selection reads it here only, so a continuation row, a ghosted row or name colours being off is
// never measured with a speaker the draw never tinted.
func (a *App) logRowNameStyle(which, li int) (speaker string, nameOn, bold bool) {
	nameOn = a.d.Prefs.Load().NameColors // one global toggle, shared by both logs
	bold = a.logRowBoldPref(which)
	if which != logSelIC {
		if li >= 0 && li < len(a.oocWrapName) {
			speaker = a.oocWrapName[li]
		}
		return speaker, nameOn, bold
	}
	if !nameOn || li < 0 || li >= len(a.icWrap) || !a.icRowIsEntryStart(li) {
		return "", nameOn, bold
	}
	entry := a.icWrap[li].entry
	if a.ghostLogSpan(a.icWrap).ghosted(li, entry) {
		return "", nameOn, bold // not spoken yet: drawn ghostInk-only, the name tint drops with it
	}
	return a.icLog[entry].speaker, nameOn, bold
}

// logPointAt maps a pixel position to a selection point: the wrapped-line index from the scroll-adjusted y, and the
// rune offset from a binary search over that one line's prefix widths.
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
	text := a.logLineText(which, li)
	runes := []rune(text)
	font := a.logRowFont(which, text)
	// A wrap continuation row draws indented, so its rune hit-test starts at the same offset.
	off := hitTestRune(runes, mx-listX-a.logRowIndent(which, li), func(r []rune) int32 {
		return a.logPrefixWidth(which, li, text, font, r, len(r))
	})
	return selPoint{entry: li, off: off}
}

// handleLogSelect runs drag-select, click-to-clear and Ctrl+C for one log. Call it from the log's draw after the
// scroll is final and before the line loop, so a real drag can swallow the frame's click (links/pins then don't
// also fire). No-op when the cursor isn't in this log and nothing is selected.
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
	// Ctrl+C copies the active selection when no text field is focused (a focused field's own copy wins). copyReq is
	// consumed so it fires once.
	if c.copyReq && c.focusID == "" && a.logSelActive && a.logSelWhich == which {
		lo, hi := orderSel(a.logSelAnchor, a.logSelHead)
		if !lo.equal(hi) {
			_ = sdl.SetClipboardText(selectedText(func(i int) string { return a.logLineText(which, i) }, lo, hi))
			a.warnLine = "Copied selection to clipboard"
			a.warnAt = time.Now()
		}
		c.copyReq = false
	}
	// Double-click selects the word under the cursor, triple-click the whole wrapped line; dragging selects any
	// range.
	// Event-gated.
	if c.tripleClick && n > 0 && c.hovering(list) && inText {
		a.selectLogLine(which, list, scroll, lineH)
		c.clicked = false     // the line is selected — don't also open a link under it
		c.focusID = ""        // unfocus so Ctrl+C / right-click copies the selection
		c.tripleClick = false // consume: this triple-click was the log's
	}
	if c.dblClick && n > 0 && c.hovering(list) && inText {
		a.selectLogWord(which, list, scroll, lineH)
		c.clicked = false  // the word is selected — don't also open a link under it
		c.focusID = ""     // unfocus so Ctrl+C / right-click copies the selection
		c.dblClick = false // consume: this double-click was the log's
	}
	// Right-click copies. A deliberate selection wins and is consumed (so a link line's copy-URL and the unread pill
	// don't also fire); otherwise copy the whole line under the cursor without consuming, so an OOC link line still
	// copies its URL after (last write wins). Pin-to-notes has its own chord (default Ctrl+N).
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

// selectLogLine selects the whole wrapped line under the cursor in log `which` (triple-click): anchor at its start,
// head at its end, so the highlight and copy paths treat it like a drag.
func (a *App) selectLogLine(which int, list sdl.Rect, scroll, lineH int32) {
	c := a.ctx
	li := a.logPointAt(which, list.X, list.Y, scroll, lineH, c.mouseX, c.mouseY).entry
	a.logSelWhich = which
	a.logSelAnchor = selPoint{entry: li, off: 0}
	a.logSelHead = selPoint{entry: li, off: len([]rune(a.logLineText(which, li)))}
	a.logSelActive = true
	a.logSelDragging = false
}

// selectLogWord selects the word under the cursor (double-click): the maximal non-space run around the hit
// boundary, within one wrapped display line. It shares wordBoundsAt with the text fields and chatbox.
func (a *App) selectLogWord(which int, list sdl.Rect, scroll, lineH int32) {
	c := a.ctx
	p := a.logPointAt(which, list.X, list.Y, scroll, lineH, c.mouseX, c.mouseY)
	runes := []rune(a.logLineText(which, p.entry))
	lo, hi := wordBoundsAt(runes, p.off)
	if hi <= lo {
		return // empty line — nothing to select
	}
	a.logSelWhich = which
	a.logSelAnchor = selPoint{entry: p.entry, off: lo}
	a.logSelHead = selPoint{entry: p.entry, off: hi}
	a.logSelActive = true
	a.logSelDragging = false
}

// drawLogSelHighlight fills the selection background behind one display line, before the text. Interior lines fill
// the whole column; only the first and last partial lines measure an offset.
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
	// The measured endpoints shift by the row's hanging indent, matching where the text draws.
	if li == lo.entry {
		x0 = listX + a.logRowIndent(which, li) + a.logPrefixWidth(which, li, text, font, runes, lo.off)
	}
	if li == hi.entry {
		x1 = listX + a.logRowIndent(which, li) + a.logPrefixWidth(which, li, text, font, runes, hi.off)
	}
	if x1 > x0 {
		a.ctx.Fill(sdl.Rect{X: x0, Y: y, W: x1 - x0, H: lineH}, a.logSelFill)
	}
}
