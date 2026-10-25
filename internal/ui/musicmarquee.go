package ui

// The music banner's marquee: AO2's ScrollText, ported.
// src/scrolltext.cpp, constructed at courtroom.cpp:171 and parented inside
// (ScrollText::updateText, scrolltext.cpp:51) and a free-running clock alone cannot
// Pinned by musicmarquee_test.go — the constants against scrolltext.cpp, the
// pre-roll/step/wrap of the scroll position, the slot's restart-on-new-title and its
// zero-allocation steady state, and (in theme_layout.go) that the draw still joins

import "time"

const (
	// ao2MarqueeSeparator is what AO2 puts between repeats of the title
	// (scrolltext.cpp:12, `setSeparator("   ---   ")`). It is appended to the text
	// ONCE and the result is tiled, so it appears exactly between copies.
	ao2MarqueeSeparator = "   ---   "

	// ao2MarqueeStartPx is the scroll position a fresh title starts at
	// (scrolltext.cpp:51, `scrollPos = -64`). Negative means "not moving yet": the
	// draw clamps a negative position to zero, so the title sits still at the left
	// margin for the first 32 ticks and only then starts to travel. That pause is
	// what makes a title readable before it moves.
	ao2MarqueeStartPx = -64

	// ao2MarqueeStepPx / ao2MarqueeTickMs are AO2's scroll rate: two pixels every
	// fifty milliseconds (scrolltext.cpp:145 `scrollPos = (scrollPos + 2) % ...`,
	// with the timer's interval set at :15). Forty pixels a second.
	ao2MarqueeStepPx = 2
	ao2MarqueeTickMs = 50

	// marqueeMaxTiles bounds the repeat loop. AO2's is `while (x < width())` with
	// no bound of its own, which is safe there because wholeTextSize.width() is at
	// least one title wide and the widget is a chatbox-sized strip. Ours needs a
	// named cap (hard rule 4) because the tile step is theme data: a theme could
	// declare a music_name rect thousands of pixels wide, and the separator is only
	// nine characters, so the loop count is attacker-influenced. Eight copies of a
	// title that is by definition WIDER than the widget already overfills any
	// widget by a factor of eight; past that the extra draws would be off-screen.
	marqueeMaxTiles = 8
)

// marqueeScrolls is AO2's scrollEnabled: the title travels only when it does not
// fit (scrolltext.cpp:47, `singleTextWidth > width() - leftMargin * 2`).
func marqueeScrolls(textW, avail int32) bool { return textW > avail }

// marqueeScrollPos is AO2's scrollPos at a given elapsed time, in pixels.
func marqueeScrollPos(elapsed time.Duration, wholeW int32) int32 {
	if wholeW <= 0 {
		return 0
	}
	ticks := int64(elapsed / (ao2MarqueeTickMs * time.Millisecond))
	if ticks < 0 {
		ticks = 0
	}
	raw := int64(ao2MarqueeStartPx) + ticks*ao2MarqueeStepPx
	if raw < 0 {
		return int32(raw) // the pre-roll pause: still clamped to 0 by marqueeFirstX
	}
	return int32(raw % int64(wholeW))
}

// marqueeFirstX is where the first copy of the tiled title is drawn, relative to
// the widget's left edge: AO2's `qMin(-scrollPos, 0) + leftMargin`
// (scrolltext.cpp:75).
// The qMin is what makes the negative start a PAUSE rather than the title sliding
// in from the right: a negative scrollPos yields a positive -scrollPos, which the
// min clamps to 0, so the text stays pinned at the margin until the counter
// crosses zero.
func marqueeFirstX(scrollPos, leftMargin int32) int32 {
	off := -scrollPos
	if off > 0 {
		off = 0
	}
	return off + leftMargin
}

// marqueeSlot is the phase of the ONE scrolling label the client has, plus the
// tiled string that phase belongs to.
// It exists because AO2 resets the scroll whenever the text changes
// (ScrollText::setText → updateText, scrolltext.cpp:24-28/51): a new track has to
// start from the pre-roll pause, not halfway through the previous track's cycle.
type marqueeSlot struct {
	label string        // the title the epoch belongs to
	tiled string        // label + ao2MarqueeSeparator, built once per title
	epoch time.Duration // the animation-clock reading when that title appeared
}

// frame returns what to draw and how long it has been on screen: AO2's tiled
// `m_text + separator` (scrolltext.cpp:52) and the elapsed time to feed
// marqueeScrollPos, restarting the clock when the title changes. now is the
// free-running animation clock (Viewport.AnimClock).
// THE CONCATENATION LIVES HERE, not at the draw site, because it is the one
// allocation in a marquee frame. Built at the draw site it ran on EVERY frame the
// banner scrolled — a heap allocation inside the render loop, which is what the
func (m *marqueeSlot) frame(label string, now time.Duration) (string, time.Duration) {
	if m.label != label || m.tiled == "" {
		m.label, m.tiled, m.epoch = label, label+ao2MarqueeSeparator, now
	}
	if d := now - m.epoch; d > 0 {
		return m.tiled, d
	}
	// The clock went backwards (a fresh Viewport, a replay scrub). Re-anchor
	// rather than return a negative elapsed, which marqueeScrollPos would floor
	// to the pre-roll anyway but which would then never advance.
	m.epoch = now
	return m.tiled, 0
}
