package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestApplyAnchorCornerGlue pins the point of the feature: a box saved at
// 1280×720 pinned bottom-right keeps its PIXEL size and its pixel distances
// to the right/bottom edges at any other window size — instead of drifting
// with the fractions.
func TestApplyAnchorCornerGlue(t *testing.T) {
	// Placed at (1000, 600, 200×80) in a 1280×720 window: 80 px from the right
	// edge, 40 px from the bottom.
	ov := rectToFrac(sdl.Rect{X: 1000, Y: 600, W: 200, H: 80}, 1280, 720)
	ar := anchorRef{h: anchorHigh, v: anchorHigh, winW: 1280, winH: 720}

	got := applyAnchor(ov, ar, 1920, 1080)
	want := sdl.Rect{X: 1920 - 80 - 200, Y: 1080 - 40 - 80, W: 200, H: 80}
	if got != want {
		t.Fatalf("grow: pinned rect = %+v, want %+v", got, want)
	}
	// Same window in = same rect out (the drag-write round-trip identity).
	if got := applyAnchor(ov, ar, 1280, 720); got != (sdl.Rect{X: 1000, Y: 600, W: 200, H: 80}) {
		t.Fatalf("identity: got %+v", got)
	}
	// A hard shrink clamps on-screen instead of stranding the box outside.
	small := applyAnchor(ov, ar, 240, 100)
	if small.X < 0 || small.Y < 0 {
		t.Fatalf("shrink must clamp on-screen, got %+v", small)
	}
}

// TestApplyAnchorCentre pins the centre mode: the box keeps its pixel offset
// from the window centre (here: dead centre stays dead centre).
func TestApplyAnchorCentre(t *testing.T) {
	ov := rectToFrac(sdl.Rect{X: 540, Y: 310, W: 200, H: 100}, 1280, 720) // centred
	ar := anchorRef{h: anchorMid, v: anchorMid, winW: 1280, winH: 720}
	got := applyAnchor(ov, ar, 1920, 1080)
	if cx, cy := got.X+got.W/2, got.Y+got.H/2; cx != 960 || cy != 540 {
		t.Fatalf("centre pin: centre = (%d,%d), want (960,540); rect %+v", cx, cy, got)
	}
	if got.W != 200 || got.H != 100 {
		t.Fatalf("centre pin must keep pixel size, got %dx%d", got.W, got.H)
	}
}

// TestApplyAnchorFractionAxis pins the mixed mode: an 'f' axis keeps today's
// proportional scaling while the pinned axis glues.
func TestApplyAnchorFractionAxis(t *testing.T) {
	ov := rectToFrac(sdl.Rect{X: 100, Y: 600, W: 200, H: 80}, 1280, 720)
	ar := anchorRef{h: anchorFrac, v: anchorHigh, winW: 1280, winH: 720} // "fb"
	got := applyAnchor(ov, ar, 2560, 1440)
	if got.X != int32(ov[0]*2560) || got.W != int32(ov[2]*2560) {
		t.Errorf("fraction axis must scale proportionally, got X=%d W=%d", got.X, got.W)
	}
	if got.Y != 1440-40-80 || got.H != 80 {
		t.Errorf("pinned axis must glue to the bottom, got Y=%d H=%d", got.Y, got.H)
	}
}

// TestSlotRectHonoursAnchor drives the real resolution path end to end: an
// override + pin resolve glued through slotRect at a new window size.
func TestSlotRectHonoursAnchor(t *testing.T) {
	var a App
	placed := sdl.Rect{X: 1000, Y: 600, W: 200, H: 80}
	a.classicOv = map[string][4]float64{slotOOC: rectToFrac(placed, 1280, 720)}
	a.classicAnchor = map[string]anchorRef{slotOOC: {h: anchorHigh, v: anchorHigh, winW: 1280, winH: 720}}
	def := sdl.Rect{X: 1, Y: 1, W: 10, H: 10}
	got := a.slotRect(slotOOC, def, 1920, 1080)
	want := sdl.Rect{X: 1640, Y: 960, W: 200, H: 80}
	if got != want {
		t.Fatalf("slotRect pinned = %+v, want %+v", got, want)
	}
	// Un-anchored slots keep the pure fraction path byte-identical.
	delete(a.classicAnchor, slotOOC)
	if got := a.slotRect(slotOOC, def, 1920, 1080); got != fracToRect(a.classicOv[slotOOC], 1920, 1080) {
		t.Fatalf("un-pinned slotRect must be plain fracToRect, got %+v", got)
	}
}

// TestAnchorModeRoundTrip pins parse/format inverses + the A-key cycle.
func TestAnchorModeRoundTrip(t *testing.T) {
	for _, m := range []string{"lt", "rt", "lb", "rb", "cc", "fb", "rf"} {
		h, v, ok := parseAnchorMode(m)
		if !ok {
			t.Fatalf("parseAnchorMode(%q) must accept a valid mode", m)
		}
		if got := formatAnchorMode(h, v); got != m {
			t.Errorf("round-trip %q → %q", m, got)
		}
	}
	if _, _, ok := parseAnchorMode("zz"); ok {
		t.Error("junk mode must not parse")
	}
	if got := nextAnchorMode(""); got != "lt" {
		t.Errorf("cycle start = %q, want lt", got)
	}
	if got := nextAnchorMode("cc"); got != "" {
		t.Errorf("cycle end must wrap to unpinned, got %q", got)
	}
	if got := nextAnchorMode("rf"); got != "" {
		t.Errorf("a hand-edited single-axis mode must restart the cycle, got %q", got)
	}
}

// TestParseAnchorsDropsJunk pins the load-time hygiene: malformed persisted
// entries never reach the resolver.
func TestParseAnchorsDropsJunk(t *testing.T) {
	in := map[string]config.ClassicAnchor{
		"good": {Mode: "rb", WinW: 1280, WinH: 720},
		"bad1": {Mode: "xx", WinW: 1280, WinH: 720},
		"bad2": {Mode: "rb", WinW: 0, WinH: 720},
	}
	out := parseAnchors(in)
	if len(out) != 1 {
		t.Fatalf("want exactly the one good entry, got %v", out)
	}
	if ar := out["good"]; ar.h != anchorHigh || ar.v != anchorHigh || ar.winW != 1280 {
		t.Errorf("good entry parsed wrong: %+v", ar)
	}
}
