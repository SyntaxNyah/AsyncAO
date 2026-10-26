package ui

// THE CHATBOX FOLLOWS ITS OWN CRAWL. One concern, one file.
//
// CANON. ui_vp_message is a QTextEdit with BOTH scrollbars policy-disabled
// (courtroom.cpp:97-101), so AO2 shows no bar — but it is still a scrolling
// viewport, and chat_tick drives it explicitly on every revealed character
// (courtroom.cpp:4514-4521):
//
//	// This should always be done AFTER setHtml. Scroll the chat window with
//	// the text.
//	QTextCursor cursor = ui_vp_message->textCursor();
//	cursor.setPosition(real_tick_pos);
//	ui_vp_message->setTextCursor(cursor);
//	real_tick_pos += f_char_length;
//	ui_vp_message->ensureCursorVisible();
//
// ensureCursorVisible is Qt's minimum-movement scroll: it does nothing while the
// insertion point is already inside the viewport, and otherwise scrolls just far
// enough to bring it back. So a message that FITS never moves, and a taller one
// follows the reveal. There is no reset on settle — the last tick leaves the view
// at the end of the text and it stays there for the linger, which is why AO2's
// long posts read as "scrolled to the bottom" once they finish.
//
// AsyncAO clipped and stopped. The raster drew from a fixed origin under a clip
// set to the chatbox, so everything past the last visible row was simply cut,
// and the typewriter carried on revealing letters into the void below the box —
// with no bar, no wheel and no way to see them. AO2's own row pitch landing in
// this batch (the message box got taller rows, so fewer of them fit) is what made
// an already-possible overflow easy to hit with four short lines.
//
// ON SETTLE it rests at the END, and that is canon rather than a choice: nothing
// resets the cursor after the last tick, so AO2's long posts sit scrolled to the
// bottom through the linger and are cleared by the next message
// (courtroom.cpp:4227).
//
// This file is that one Qt call, in the arithmetic the raster path can use:
// pure, allocation-free, and a no-op — an exact zero — for every message that
// fits, which is essentially all of them.
//
// AND THE TOP EDGE IT IMPLIES. A scroll draws the message from
// textOrigin-offset, which is ABOVE the origin, so the rows that scrolled out
// have to be cut by something. Qt gets that for free and it is the same fact
// that gives it the scroll: ui_vp_message is a CHILD widget positioned inside
// the chatbox (courtroom.cpp:3310), so its viewport clips at the top of the
// MESSAGE rect. AsyncAO clips with one renderer-wide rect and had been setting
// it to the whole chatbox — under which the scrolled-off rows painted straight
// over the showname plate. chatCrawlClip is that top edge, and nothing else:
// the offset and the edge that contains it are one rule and live together,
// because a draw site that takes one without the other is the defect.
//
// NOT COVERED, honestly: the ANIMATED path (#M5 shake/wave/rainbow) draws from
// AnimatedText, which bakes a per-glyph y at layout time and exposes no row
// lookup, so an overlong message wearing inline text effects still clips. Both
// draw sites pass the same offset, so it will follow the moment AnimatedText
// grows the equivalent of RowOfRune; giving it one is a change to the animated
// layout, not to this rule.

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// crawlRow is the display row holding the LAST revealed rune, i.e. where AO2
// puts its cursor before calling ensureCursorVisible (chat_tick's real_tick_pos).
//
// visible is a COUNT, so the rune at index visible-1 is the last one drawn; a
// count of 0 or 1 (a shout or preanim with nothing revealed, or the very first
// character) is row 0, which is the top of the box and therefore no scroll at
// all — matching AO2, whose cursor sits at position 0 before the first tick.
//
// The row itself comes off the raster's own line table (RowOfRune), the same one
// the selection geometry reads, so the answer can never drift from where the
// glyphs are.
func crawlRow(m *render.MessageRaster, visible int) int {
	if m == nil || visible <= 1 {
		return 0
	}
	return m.RowOfRune(visible - 1)
}

// chatCrawlScrollPx is ensureCursorVisible as a Y offset to SUBTRACT from the
// message origin: 0 while the revealing row is already inside availH, otherwise
// exactly far enough to bring that row's bottom to the box's bottom edge.
//
// Minimum movement, like Qt's: it never scrolls past what is needed, so the
// reader keeps as much of the earlier text on screen as the box can hold, and a
// message whose last row is the first row that does not fit scrolls by one row
// and not by the whole overflow.
//
// It is deliberately derived from the row the CRAWL is on rather than from the
// message's total height. Anchoring to the end instead would jump a six-line post
// to its bottom before a single character of it had been revealed, which is the
// opposite of following the reveal.
func chatCrawlScrollPx(row int, lineH, availH int32) int32 {
	if row <= 0 || lineH <= 0 || availH <= 0 {
		return 0
	}
	bottom := int32(row+1) * lineH // the revealing row's bottom edge, from the origin
	if bottom <= availH {
		return 0 // already visible: Qt does nothing, and neither do we
	}
	return bottom - availH
}

// chatCrawlOffset is the two above composed for a draw site: the LOGICAL pixels
// the message (and its selection highlight, which must move with it) shifts up so
// the currently-revealing row stays in the box.
//
// availH is the room the text actually has — the clip the caller is about to set,
// measured from the text origin, NOT the chatbox rect: the two differ by the
// name-plate strip, and AO2's message widget is likewise positioned inside its
// chatbox rather than over it (courtroom.cpp:3310).
func chatCrawlOffset(m *render.MessageRaster, visible int, availH int32) int32 {
	if m == nil {
		return 0
	}
	return chatCrawlScrollPx(crawlRow(m, visible), m.LineH(), availH)
}

// chatCrawlClip is the rect a SCROLLED message may paint in: the chatbox with
// its top edge raised to the text origin.
//
// ONLY THE TOP MOVES, and that is deliberate. The right and bottom edges stay
// the chatbox's because AO2 does not confine this text to the message rect
// either — the widget is re-parented to the Courtroom and merely offset by the
// chatbox origin (courtroom.cpp:3310), which is why eight design files in the
// reference corpus ship a message rect that overhangs its own chatbox (CCSmol's
// right edge is 259 in a 256-wide box; Mobile's 393 in 392). Tightening those
// edges would start cutting their last column and last line. The TOP is the one
// edge Qt's child viewport genuinely enforces, and the one the scroll needs.
//
// A text origin at or above the box top is the unscrolled classic case and
// answers the box unchanged, so an ordinary post is byte-identical. An origin at
// or below the BOTTOM edge is an overhanging theme with no room at all: the old
// clip painted nothing there (every row starts below the box) and this answers
// clipNowhere, which is the same picture said honestly — never an empty rect,
// because SDL reads w<=0||h<=0 as "clipping OFF" (see clipNowhere).
//
// anim IS THE #M5 PAINTER — the live *render.AnimatedText or nil — and a
// non-nil one takes the whole box back. AnimatedText DISPLACES glyphs off their
// layout row — a wave is round(sin·waveAmpPx), so up to 3 px, and shake/bounce
// the same order — so on the FIRST row, whose layout origin IS textTop, the
// raised edge shaves the crests off every span. And it shaves them for nothing:
// the animated painter cannot be scrolled at all. The offset both draw sites
// compute is chatCrawlOffset(a.msRaster, …) and msAnim XOR msRaster is a
// construction invariant (ensureChatRaster builds exactly one per message), so a
// live msAnim means a nil raster means a structural 0 — the top edge is
// protecting a scroll that is not there. See the file header's "NOT COVERED,
// honestly" note: giving AnimatedText a row lookup is what would let it scroll,
// and that day this parameter goes away with it.
//
// WHY THE PAINTER ITSELF AND NOT A BOOL. The `animated bool` this used to take
// put the decision at the two draw sites as `a.msAnim != nil`, and a bare bool
// argument is the shape whose POLARITY a refactor flips in silence: rewriting
// both sites to `a.msAnim == nil` restores BOTH defects at once (the crests are
// shaved again, and every ordinary raster message gets the whole box back, i.e.
// the field-8 name-plate defect) while every test in the package stays green —
// the direct-call gates pass their own literals and a source gate that only
// asks whether the argument NAMES msAnim cannot tell the two spellings apart.
// Taking the pointer deletes the invertible term from the API: the sites hand
// over a.msAnim and nothing else type-checks, and the one nil test that remains
// lives here, next to the paragraph that justifies it, under the direct
// behavioural gates in chatcrawlscroll_test.go.
//
// THE TIGHT FORM ON PURPOSE. The equivalent test at the draw site is
// `msgScroll == 0`, which is TRUE for the animated painter for the reason just
// given but ALSO for every ordinary post that fits — i.e. essentially all of
// them. That would hand the whole client's normal message the name plate back as
// a legal paint target to fix a defect only the displacing painter can have, so
// the narrow question ("is this the painter that draws above its own row?") is
// the one asked. Every non-animated case, scrolled or not, is byte-identical.
func chatCrawlClip(box sdl.Rect, textTop int32, anim *render.AnimatedText) sdl.Rect {
	if anim != nil || textTop <= box.Y {
		return box
	}
	box.H -= textTop - box.Y
	box.Y = textTop
	if box.H <= 0 || box.W <= 0 {
		return clipNowhere
	}
	return box
}
