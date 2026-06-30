package ui

import "testing"

// TestTipBoxStaysOnScreen pins the reported bug: a long server-description tooltip must never run
// off-screen. tipBox flips the box to the other side of the pointer and clamps it inside the
// window; a box that fits the window must end up fully on-screen, and an oversized one must still
// have a non-negative top-left (pinned to the margin, never drawn off the left/top).
func TestTipBoxStaysOnScreen(t *testing.T) {
	const w, h = int32(800), int32(600)
	cases := []struct {
		name               string
		mx, my, boxW, boxH int32
	}{
		{"small near origin", 20, 20, 120, 30},
		{"small near right edge", 790, 20, 200, 30},
		{"small near bottom edge", 20, 590, 120, 30},
		{"wide desc near right edge", 760, 300, 460, 140},
		{"cursor in bottom-right corner", 799, 599, 460, 200},
		{"box wider than the window", 400, 300, 1000, 40}, // defensive: can't fit, must not go negative
		{"box taller than the window", 400, 300, 200, 800},
	}
	for _, tc := range cases {
		b := tipBox(tc.mx, tc.my, tc.boxW, tc.boxH, w, h)
		if b.X < 0 || b.Y < 0 {
			t.Errorf("%s: top-left off-screen: %+v", tc.name, b)
		}
		// A box that fits the window must be FULLY inside it (the reported overflow).
		if tc.boxW <= w-2*tooltipMargin && b.X+b.W > w {
			t.Errorf("%s: right edge off-screen: X=%d W=%d (w=%d)", tc.name, b.X, b.W, w)
		}
		if tc.boxH <= h-2*tooltipMargin && b.Y+b.H > h {
			t.Errorf("%s: bottom edge off-screen: Y=%d H=%d (h=%d)", tc.name, b.Y, b.H, h)
		}
	}
}
