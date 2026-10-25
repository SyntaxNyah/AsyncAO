package ui

// THE CHATBOX FOLLOWS ITS OWN CRAWL. One concern, one file.
// (courtroom.cpp:97-101), so AO2 shows no bar — but it is still a scrolling
// (courtroom.cpp:4514-4521):
// (courtroom.cpp:4227).
// the chatbox (courtroom.cpp:3310), so its viewport clips at the top of the

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// crawlRow is the display row holding the LAST revealed rune, i.e. where AO2
// puts its cursor before calling ensureCursorVisible (chat_tick's real_tick_pos).
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
// It is deliberately derived from the row the CRAWL is on rather than from the
// message's total height. Anchoring to the end instead would jump a six-line post
// to its bottom before a single character of it had been revealed, which is the
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
// chatbox origin (courtroom.cpp:3310), which is why eight design files in the
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
