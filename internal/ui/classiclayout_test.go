package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// A zero App is enough here: slotRect touches only classicOv + classicEdit.

func TestSlotRectOverrideAndDefault(t *testing.T) {
	var a App
	def := sdl.Rect{X: 10, Y: 20, W: 100, H: 80}
	const w, h = 1000, 500

	// No override → the exact computed default (the safety invariant).
	if got := a.slotRect(slotOOC, def, w, h); got != def {
		t.Fatalf("no-override slotRect = %+v, want default %+v", got, def)
	}

	// Override stored as window fractions → frac→px.
	a.classicOv = map[string][4]float64{
		slotOOC: {0.5, 0.25, 0.2, 0.4}, // x=500 y=125 w=200 h=200
	}
	want := sdl.Rect{X: 500, Y: 125, W: 200, H: 200}
	if got := a.slotRect(slotOOC, def, w, h); got != want {
		t.Fatalf("override slotRect = %+v, want %+v", got, want)
	}
}

// TestSlotNoAlloc pins the hard rule: the render path (no edit mode) allocates
// nothing, even with an override present (the worst case runs fracToRect).
func TestSlotNoAlloc(t *testing.T) {
	var a App
	def := sdl.Rect{X: 1, Y: 2, W: 300, H: 200}
	const w, h = 800, 600
	a.classicOv = map[string][4]float64{
		slotOOC:      {0.1, 0.1, 0.3, 0.3},
		slotViewport: {0.2, 0.2, 0.5, 0.5},
	}
	if n := testing.AllocsPerRun(200, func() {
		_ = a.slotRect(slotOOC, def, w, h)
		_ = a.slotRect(slotViewport, def, w, h)
	}); n != 0 {
		t.Fatalf("slotRect allocates %v/op on the render path; want 0", n)
	}
}

func TestClassicEdgeAt(t *testing.T) {
	r := sdl.Rect{X: 100, Y: 100, W: 200, H: 120}
	const m = 12
	cases := []struct {
		name string
		x, y int32
		want uint8
	}{
		{"interior", 200, 160, 0},
		{"right edge", 300, 160, edgeR},
		{"left edge", 100, 160, edgeL},
		{"top edge", 200, 100, edgeT},
		{"bottom edge", 200, 220, edgeB},
		{"top-right corner", 300, 100, edgeR | edgeT},
		{"bottom-left corner", 100, 220, edgeL | edgeB},
		{"outside", 400, 160, 0},
	}
	for _, tc := range cases {
		if got := classicEdgeAt(tc.x, tc.y, r, m); got != tc.want {
			t.Errorf("%s: classicEdgeAt = %04b, want %04b", tc.name, got, tc.want)
		}
	}
}

func TestFracRectRoundTrip(t *testing.T) {
	const w, h = 1600, 900
	r := sdl.Rect{X: 240, Y: 90, W: 800, H: 450}
	got := fracToRect(rectToFrac(r, w, h), w, h)
	if got != r {
		t.Fatalf("round-trip = %+v, want %+v", got, r)
	}
}
