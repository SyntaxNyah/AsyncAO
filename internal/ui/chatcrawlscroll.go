package ui

// The chatbox follows its own crawl, as AO2's chat_tick does with ensureCursorVisible (courtroom.cpp:4514-4521).
// ui_vp_message is a QTextEdit with its scrollbars disabled (courtroom.cpp:97-101):
// minimum movement, so a message that fits never moves and a taller one follows the reveal.
// On settle it rests at the end, as in AO2, until the next message clears it (courtroom.cpp:4227).
// Scrolling draws above the text origin, so the top edge is clipped too: AO2's message widget is a child of the
// chatbox (courtroom.cpp:3310).
// Not covered: AnimatedText bakes per-glyph y and has no row lookup, so an overlong message with inline effects clips.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// crawlRow is the display row holding the last revealed rune, where AO2 puts its cursor (chat_tick's real_tick_pos).
// visible is a count, so 0 or 1 is row 0 and no scroll. The row comes from RowOfRune, as the selection geometry does.
func crawlRow(m *render.MessageRaster, visible int) int {
	if m == nil || visible <= 1 {
		return 0
	}
	return m.RowOfRune(visible - 1)
}

// chatCrawlScrollPx is ensureCursorVisible as a Y offset to subtract from the message origin: 0 while the revealing
// row fits in availH, otherwise just enough to bring that row's bottom to the box's bottom.
// It follows the crawl row, not the total height, so a tall post does not jump to its end before it is revealed.
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

// chatCrawlOffset is the logical pixels the message and its selection highlight shift up to keep the revealing row
// in view. availH is measured from the text origin, not the chatbox rect; they differ by the name-plate strip
// (courtroom.cpp:3310).
func chatCrawlOffset(m *render.MessageRaster, visible int, availH int32) int32 {
	if m == nil {
		return 0
	}
	return chatCrawlScrollPx(crawlRow(m, visible), m.LineH(), availH)
}

// chatCrawlClip is the rect a scrolled message may paint in: the chatbox with its top edge raised to the text origin.
// Only the top moves: AO2 does not confine the text on the other edges (courtroom.cpp:3310), and some themes' message
// rects overhang their chatbox.
// An origin at or below the bottom edge answers clipNowhere, never an empty rect, because SDL reads w<=0||h<=0 as
// clipping off.
// anim is the live *render.AnimatedText or nil. Non-nil takes the whole box back: it displaces glyphs off their layout
// row (shaving the first row's crests) and cannot be scrolled. Taking the painter rather than a bool keeps the
// polarity from being flipped silently at the draw sites.
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
