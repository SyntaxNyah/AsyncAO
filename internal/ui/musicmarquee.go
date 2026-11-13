package ui

// The music banner's marquee, ported from AO2's ScrollText (scrolltext.cpp, courtroom.cpp:171); every number is AO2's.

import "time"

const (
	// ao2MarqueeSeparator goes between repeats (scrolltext.cpp:12); appended once to the title, then tiled.
	ao2MarqueeSeparator = "   ---   "

	// ao2MarqueeStartPx is where a new title starts (scrolltext.cpp:51); negative clamps to 0, pausing it for 32 ticks.
	ao2MarqueeStartPx = -64

	// ao2MarqueeStepPx / ao2MarqueeTickMs: 2 px every 50 ms (scrolltext.cpp:145, interval set at :15).
	ao2MarqueeStepPx = 2
	ao2MarqueeTickMs = 50

	// marqueeMaxTiles caps the repeat loop (hard rule 4): a theme can declare a very wide music_name rect, and eight
	// copies of a title wider than the widget already overfill it.
	marqueeMaxTiles = 8
)

// marqueeScrolls is AO2's scrollEnabled (scrolltext.cpp:47); avail is already width - 2*leftMargin.
func marqueeScrolls(textW, avail int32) bool { return textW > avail }

// marqueeScrollPos is AO2's scrollPos in pixels, computed from a clock so dropped frames cannot make it drift.
// AO2 steps (scrollPos + 2) % wholeW from -64; C++ % truncates toward zero, so negative values just climb to 0.
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

// marqueeFirstX is AO2's `qMin(-scrollPos, 0) + leftMargin` (scrolltext.cpp:75); the qMin turns a negative position
// into a pause rather than a slide in from the right.
func marqueeFirstX(scrollPos, leftMargin int32) int32 {
	off := -scrollPos
	if off > 0 {
		off = 0
	}
	return off + leftMargin
}

// marqueeSlot holds the one marquee's start time and tiled string; AO2 restarts the scroll when the text changes
// (scrolltext.cpp:24-28). A single value, not a map keyed by widget id (hard rule 4).
type marqueeSlot struct {
	label string        // the title the epoch belongs to
	tiled string        // label + ao2MarqueeSeparator, built once per title
	epoch time.Duration // the animation-clock reading when that title appeared
}

// frame returns AO2's tiled `m_text + separator` (scrolltext.cpp:52) and the elapsed time, restarting on a new title.
// The concatenation is cached here because it is the one allocation a marquee frame would otherwise make.
func (m *marqueeSlot) frame(label string, now time.Duration) (string, time.Duration) {
	if m.label != label || m.tiled == "" {
		m.label, m.tiled, m.epoch = label, label+ao2MarqueeSeparator, now
	}
	if d := now - m.epoch; d > 0 {
		return m.tiled, d
	}
	// The clock went backwards (fresh Viewport, replay scrub); a negative elapsed would never advance.
	m.epoch = now
	return m.tiled, 0
}
