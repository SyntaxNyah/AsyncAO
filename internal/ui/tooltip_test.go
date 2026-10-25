package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestTipBoxStaysOnScreen pins the reported bug: a long server-description tooltip must never run
// off-screen. tipBox flips the box to the other side of the pointer and clamps it inside the
// window; a box that fits the window must end up fully on-screen, and an oversized one must still
// have a non-negative top-left (pinned to the margin, never drawn off the left/top).
//
// The empty avoid rect is the "no anchor" case, which must be byte-identical to the
// placement before tooltips learned to dodge the control they describe.
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
		b := tipBox(tc.mx, tc.my, tc.boxW, tc.boxH, w, h, sdl.Rect{})
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
		// An empty avoid must not change the answer at all.
		if got := tipBoxDefault(tc.mx, tc.my, tc.boxW, tc.boxH, w, h); got != b {
			t.Errorf("%s: empty avoid changed placement: %+v vs %+v", tc.name, b, got)
		}
	}
}

// TestTipBoxDodgesItsAnchor pins the reported overlap: the compact toolbox sits in
// the window's bottom-right corner, on top of the AO2 theme's own button cluster,
// and its instant tooltip flipped UP onto that cluster and onto the strip's own
// chips. tipBox now takes the armed tip's anchor and places the box clear of it —
// while keeping the on-screen invariant above.
func TestTipBoxDodgesItsAnchor(t *testing.T) {
	const w, h = int32(944), int32(600)
	// The measured default toolbox strip: {818, 574, 122, 22} at 944x600.
	strip := sdl.Rect{X: 818, Y: 574, W: 122, H: 22}
	for _, tc := range []struct {
		name               string
		mx, my, boxW, boxH int32
		avoid              sdl.Rect
	}{
		{"cursor on the bottom-right strip", 830, 585, 262, 30, strip},
		{"cursor on the strip's left chip", 900, 585, 262, 30, strip},
		{"cursor on a mid-screen control", 400, 300, 200, 40, sdl.Rect{X: 380, Y: 290, W: 120, H: 24}},
		{"tall hint over a top-left control", 40, 40, 200, 200, sdl.Rect{X: 20, Y: 20, W: 200, H: 60}},
	} {
		b := tipBox(tc.mx, tc.my, tc.boxW, tc.boxH, w, h, tc.avoid)
		if _, hit := b.Intersect(&tc.avoid); hit {
			t.Errorf("%s: tooltip %+v still covers its anchor %+v", tc.name, b, tc.avoid)
		}
		if b.X < 0 || b.Y < 0 || b.X+b.W > w || b.Y+b.H > h {
			t.Errorf("%s: dodging pushed the box off-screen: %+v", tc.name, b)
		}
	}
}

// TestTipBoxFallsBackWhenNothingDodges pins the degradation rule: when the avoided
// rect leaves no room at all, staying ON-SCREEN wins over staying clear. Returning
// an off-screen box to satisfy the new constraint would resurrect the bug
// TestTipBoxStaysOnScreen exists for.
func TestTipBoxFallsBackWhenNothingDodges(t *testing.T) {
	const w, h = int32(800), int32(600)
	whole := sdl.Rect{X: 0, Y: 0, W: w, H: h}
	b := tipBox(400, 300, 200, 40, w, h, whole)
	if b.X < 0 || b.Y < 0 || b.X+b.W > w || b.Y+b.H > h {
		t.Fatalf("fallback box is off-screen: %+v", b)
	}
	if want := tipBoxDefault(400, 300, 200, 40, w, h); b != want {
		t.Errorf("fallback = %+v, want the default placement %+v", b, want)
	}
}

// TestUnionRectGroupsTheWholeSurface pins TooltipGroup's arithmetic: a container's
// claim widens the anchor to cover the whole strip, so the hint clears the hovered
// chip AND its neighbours.
func TestUnionRectGroupsTheWholeSurface(t *testing.T) {
	chip := sdl.Rect{X: 900, Y: 574, W: 22, H: 22}
	strip := sdl.Rect{X: 818, Y: 574, W: 122, H: 22}
	got := unionRect(chip, strip)
	if got != strip {
		t.Errorf("union(chip, strip) = %+v, want the strip %+v", got, strip)
	}
	if got := unionRect(chip, sdl.Rect{}); got != chip {
		t.Errorf("an empty group must leave the anchor alone: %+v", got)
	}
}

// TestOnlyOneTooltipIsArmedPerFrame pins the exclusivity the kit already has by
// construction (one tipText string, cleared every BeginFrame, last writer wins)
// and that hovering() honours the overlay fence — so of two stacked controls only
// the TOPMOST can arm a hint. A future second painter would break this.
func TestOnlyOneTooltipIsArmedPerFrame(t *testing.T) {
	c := &Ctx{}
	under := sdl.Rect{X: 100, Y: 100, W: 200, H: 40}
	over := sdl.Rect{X: 120, Y: 100, W: 100, H: 40}
	c.mouseX, c.mouseY = 150, 110

	// The overlay ABOVE publishes its fence first, exactly as a courtroom pass does.
	c.fenceOverlay(over)
	c.Tooltip(under, "the widget underneath")
	if c.tipText != "" {
		t.Fatalf("a fenced widget armed a tooltip: %q", c.tipText)
	}
	// The overlay's own widgets suspend the fence for themselves.
	prev := c.pushOverlayOwner(0)
	c.Tooltip(over, "the overlay on top")
	c.popOverlayOwner(prev)
	if c.tipText != "the overlay on top" {
		t.Fatalf("tipText = %q, want the topmost widget's hint", c.tipText)
	}
	if c.tipAnchor != over {
		t.Errorf("tipAnchor = %+v, want the armed widget's rect %+v", c.tipAnchor, over)
	}
}
