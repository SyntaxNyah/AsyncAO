package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestSamplePathStroke pins the freehand-path sampling (#34 B2): a stroke reduces to up to 16
// evenly-spaced waypoints on the box's 16×16 grid, keeping the first + last.
func TestSamplePathStroke(t *testing.T) {
	box := sdl.Rect{X: 0, Y: 0, W: 160, H: 160} // 10 px per grid cell
	var stroke []sdl.Point
	for x := int32(0); x <= 160; x += 4 { // a horizontal stroke across the middle (41 raw points)
		stroke = append(stroke, sdl.Point{X: x, Y: 80})
	}
	pts, n := samplePathStroke(stroke, box)
	if n != 16 {
		t.Fatalf("sampled %d points, want 16", n)
	}
	if gx := pts[0] >> 4; gx > 1 { // first point near the left edge
		t.Errorf("first x-grid = %d, want ~0", gx)
	}
	if gx := pts[n-1] >> 4; gx < 9 { // last point near the right edge
		t.Errorf("last x-grid = %d, want near 15", gx)
	}
	if gy := pts[0] & 0x0F; gy < 6 || gy > 9 { // y stayed mid (80/10 ≈ 8)
		t.Errorf("y-grid = %d, want ~8", gy)
	}
	if _, m := samplePathStroke([]sdl.Point{{X: 1, Y: 1}}, box); m != 0 {
		t.Error("a single point should sample to no path")
	}
}

// TestStrokeMoved distinguishes a drag (sketch a path) from a tap (add one point).
func TestStrokeMoved(t *testing.T) {
	tap := []sdl.Point{{X: 50, Y: 50}, {X: 51, Y: 50}, {X: 50, Y: 51}}
	if strokeMoved(tap) {
		t.Error("a tap should not count as moved")
	}
	drag := []sdl.Point{{X: 10, Y: 10}, {X: 40, Y: 10}, {X: 40, Y: 40}}
	if !strokeMoved(drag) {
		t.Error("a wide drag should count as moved")
	}
}
