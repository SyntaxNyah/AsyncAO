package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestICBarUnderStage pins issue #8: the IC input bar's default sits DIRECTLY under the
// stage, and the control-button block sits BELOW it — so the input is the first thing
// under the viewport (the classic AO spot, obvious where you talk IC) instead of buried
// below the control buttons.
func TestICBarUnderStage(t *testing.T) {
	vp := sdl.Rect{X: 8, Y: 8, W: 600, H: 450}
	const fH = int32(26)
	icBarTop, defY := icBarUnderStage(vp, fH)

	if want := vp.Y + vp.H + pad; icBarTop != want {
		t.Errorf("IC bar top = %d, want it directly under the stage (%d)", icBarTop, want)
	}
	if defY <= icBarTop+fH {
		t.Errorf("control block (defY=%d) must sit BELOW the IC bar (top=%d, height=%d)", defY, icBarTop, fH)
	}
}
