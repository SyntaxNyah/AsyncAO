package ui

// SDL/ttf wiring for IC/OOC log text selection. The pure model + hit-test +
// copy extraction live in logselect.go; this feeds them from the live logs,
// renders the highlight, and copies on Ctrl+C. All of it is gated on an active
// selection (or a press inside the log), so a log with nothing selected draws
// byte-identical to before.

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// logRowSplit decides whether one log row draws with a bold speaker span (the
// leading prefix, if any, plus the speaker name) followed by a plain message,
// or as a single plain-weight run — and if it splits, exactly where. This is
// the ONE place that gate lives: drawLogLineNamed draws it, logPrefixWidth
// measures it for selection/highlight, and neither may re-derive it
// independently, which is how the two drifted apart in the first place (a
// selection that always measured the row at plain weight while the draw put
// a BOLD speaker on it — "select empty space past the text").
//
// ok=false covers every case drawLogLineNamed's old inline gate fell through
// on: no speaker, name colours and bold both off, the speaker not found in
// the line, a line no single face covers (or that needs the emoji/mixed-
// script raster), or either segment's width lookup itself failing — every one
// of those draws (and must measure) the WHOLE line at plain weight via
// a.labelEmoji, never a partial bold span.
//
// boldEnd is a BYTE offset into line (nameStart+len(speaker), matching
// strings.Index's own unit) so callers slice line directly; a caller that
// needs a rune offset converts once with utf8.RuneCountInString, same as the
// rest of this file already does for wrap indices.
//
// Both width lookups are proven to succeed HERE, before either drawLogLineNamed
// or logPrefixWidth commits to anything — the old code drew the "pre" segment
// and only THEN checked whether the speaker segment would measure, so a rare
// SizeUTF8 failure on the speaker left "pre" painted once and the whole line
// painted again on top of it via the plain-weight fallback (a real double
// draw, not merely a mis-measurement).
func (c *Ctx) logRowSplit(font, emojiFont *ttf.Font, line, speaker string, nameOn, bold bool) (nameStart, boldEnd int, ok bool) {
	if speaker == "" || !(nameOn || bold) {
		return 0, 0, false
	}
	// The speaker may sit AFTER a leading prefix ("16:11 LowGuy: …") when IC
	// timestamps are on, so locate it by index rather than HasPrefix: the prefix
	// (the timestamp, if any) bolds in the line colour, the name bolds + tints, and
	// the rest draws plain. This also lets name colours apply with timestamps on.
	idx := strings.Index(line, speaker)
	if idx < 0 {
		return 0, 0, false // name not in the line — shouldn't happen; fall back to plain
	}
	// An emoji line OR a mixed-script line no single face covers skips the
	// per-speaker split (one `font` can't do per-glyph faces) and renders whole
	// via the raster — coverage wins over the tint/bold for the rare mixed name.
	if (emojiFont != nil && render.NeedsEmojiFallback(line)) || !c.coversFace(font, line) {
		return 0, 0, false
	}
	// Both widths come from the MEMO, never font.SizeUTF8 directly: this runs
	// for every named row of both logs on every frame, and a raw SizeUTF8
	// heap-allocates through cgo (go-sdl2 hands the out-params' addresses
	// across the boundary — the same escape the cgoRect idiom exists for).
	// Measured raw, it cost the whole-screen zero-alloc gates two allocations
	// per named row per frame — invisible until the OOC box became default and
	// the gate fixture finally had OOC lines in it.
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

// drawLogLineNamed draws one wrapped log line, tinting the speaker's name prefix
// in its per-speaker colour when name colours are on and speaker != "" (the
// caller passes "" for non-first rows and system lines, so no draw-time
// re-parsing of ": "). The rest of the line draws in col. Falls back to a plain
// draw otherwise. Shared by the IC and OOC log render paths.
func (a *App) drawLogLineNamed(font, emojiFont *ttf.Font, x, y, wrapW int32, line, speaker string, col sdl.Color, nameOn bool, sat, val float64, bold bool) {
	c := a.ctx
	// logRowSplit proves BOTH segments will measure before anything is drawn, so
	// there is no partial-draw state to fall out of into the plain-weight
	// fallback below (the double-draw the funky-fonts report traced).
	if nameStart, boldEnd, ok := c.logRowSplit(font, emojiFont, line, speaker, nameOn, bold); ok {
		// The weight rides the RASTER (LabelClippedFontWeight → SDL_ttf
		// STYLE_BOLD), not a second pass one pixel to the right. The old
		// faux-bold offset was a LOGICAL pixel, so at a fractional UI scale the
		// renderer spread the two copies 1×scale device px apart on different
		// sub-pixel phases and the name read DOUBLED beside message text on the
		// same row that stayed crisp (F1b). One texture, one blit, device-exact
		// at every scale — and the widths are measured at the SAME weight, so
		// the message still starts where the bold name ends.
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

// logPrefixWidth measures the first off runes of display row li of log `which`
// in the SAME weight(s) drawLogLineNamed draws that row with — the
// selection/hit-test twin of drawLogLineNamed, sharing its logRowSplit gate so
// the two can never disagree about where a row's bold speaker span ends. Used
// only for the two partial-end lines of a selection (interior lines fill the
// whole column, so they need no measurement).
//
// Before this, the selection side always measured at plain weight regardless
// of what drew — a named row's true on-screen width (bold speaker + plain
// message) was systematically under-measured by the bold/plain advance
// delta, so the mouse had to travel past the last glyph into empty space
// before hitTestRune's binary search would conclude the line was fully
// covered ("you have to select empty space past the text to select it all").
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
		// boldEndByte is a byte offset into `text`; runes/off are rune-indexed, so
		// fold it once, the same way the rest of this file turns byte spans from
		// strings.Index into rune counts for wrap indices.
		boldEnd := utf8.RuneCountInString(text[:boldEndByte])
		if off <= boldEnd {
			w, _ := c.fontTextWidthWeight(font, string(runes[:off]), bold)
			return w
		}
		boldW, _ := c.fontTextWidthWeight(font, string(runes[:boldEnd]), bold)
		restW, _ := c.fontTextWidthWeight(font, string(runes[boldEnd:off]), false)
		return boldW + restW
	}
	// No split: the whole row drew (and is measured) at plain weight — byte
	// identical to what this function always did before the bold-aware split above.
	w, _ := c.fontTextWidthWeight(font, string(runes[:off]), false)
	return w
}

// logRowFont is the face one display row of log `which` is DRAWN in. The IC and
// OOC logs are separate courtroom_fonts.ini elements (#39) with their own point
// sizes and families, so the selection hit-test has to ask per log — measuring
// both with the IC log's face put the caret on the wrong glyph in the OOC one.
func (a *App) logRowFont(which int, text string) *ttf.Font {
	if which == logSelIC {
		return a.elemFontFor(elemICChatlog, a.logPct, text)
	}
	return a.elemFontFor(elemServerChatlog, a.oocPct, text)
}

// logRowEmojiFont is logRowFont's emoji-fallback twin: the colour-emoji face at
// the SAME resolved scale the row's text draws at, so logRowSplit's "does this
// line need the per-glyph raster" gate matches drawLogLineNamed's exactly (#39:
// an emoji baseline that didn't match the row's own scale was label 16's bug).
func (a *App) logRowEmojiFont(which int) *ttf.Font {
	if which == logSelIC {
		return a.elemEmoji(elemICChatlog, a.logPct)
	}
	return a.elemEmoji(elemServerChatlog, a.oocPct)
}

// logRowBoldPref reports whether log `which` draws its speaker names bold —
// the global BoldNamesOn toggle OR'd with that log's own theme "*_bold"
// override (#39: IC and OOC dress separate elements, so they read separate
// overrides). The draw loop reads this once per frame; logRowNameStyle reads
// it on demand for selection — both through this one function, so a future
// change to the rule can't update one and silently leave the other behind.
func (a *App) logRowBoldPref(which int) bool {
	if which == logSelIC {
		return a.d.Prefs.BoldNamesOn() || a.elemBold(elemICChatlog)
	}
	return a.d.Prefs.BoldNamesOn() || a.elemBold(elemServerChatlog)
}

// logRowNameStyle resolves the (speaker, nameOn, bold) triple drawLogLineNamed
// draws display row li of log `which` with. OOC bakes its speaker into
// oocWrapName at wrap time (empty on every row but an entry's first); IC
// re-derives it from the entry every call, because it also depends on the
// live ghost span (ghosttext.go) and NameColorsOn, neither of which the wrap
// cache carries. This is the ONE place either is read from for selection, so
// a continuation row, a ghosted (queued-but-unspoken) row, or name colours
// being off can never be measured with a speaker the draw never tinted.
func (a *App) logRowNameStyle(which, li int) (speaker string, nameOn, bold bool) {
	nameOn = a.d.Prefs.NameColorsOn() // one global toggle, shared by both logs
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
	text := a.logLineText(which, li)
	runes := []rune(text)
	font := a.logRowFont(which, text)
	// A wrap continuation row draws indented (hanging indent), so its rune
	// hit-test starts at the same offset — clicks land on what's drawn.
	off := hitTestRune(runes, mx-listX-a.logRowIndent(which, li), func(r []rune) int32 {
		return a.logPrefixWidth(which, li, text, font, r, len(r))
	})
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
	// Double-click selects the WORD under the cursor, triple-click the whole
	// wrapped line (native text gestures — the playtest ask was copying one
	// word without taking the line; dragging still selects any range). The
	// old IC double-click-to-pair is long gone (pairing lives on the player
	// list). Event-gated: a normal frame never enters here.
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
// (the triple-click gesture). Anchors at the line's start and head at its end so
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

// selectLogWord selects the word under the cursor (the double-click gesture):
// the maximal non-space run around the hit boundary, within the one wrapped
// display line (a word split by the wrap selects its visible half — clicks
// land on what's drawn). Shares wordBoundsAt with the text fields and the
// chatbox so the three gestures can never disagree.
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
	// The measured endpoints shift by the row's hanging indent (interior rows
	// keep the full-column band), matching where the text actually draws.
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
