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

// TestICBarSlotsAreEditable pins #4a: each IC-bar piece pulled out (colour, showname,
// SFX, emoji, FX, React, input) has a distinct editor label, and an override repositions
// it through slotRect — so users can drag them apart in Edit Layout.
func TestICBarSlotsAreEditable(t *testing.T) {
	slots := []string{slotICColor, slotICShowname, slotICSFX, slotICEmoji, slotICFx, slotICReact, slotICInput}
	seen := map[string]bool{}
	for _, s := range slots {
		label := classicSlotLabel(s)
		if label == "" || label == s {
			t.Errorf("slot %q has no editor label", s)
		}
		if seen[label] {
			t.Errorf("slot %q label %q is not distinct", s, label)
		}
		seen[label] = true
	}
	a := testTabApp(t)
	def := sdl.Rect{X: 100, Y: 50, W: 200, H: 24}
	if got := a.slotRect(slotICInput, def, 1000, 800); got != def {
		t.Errorf("no override: slotRect = %+v, want the default %+v", got, def)
	}
	a.classicOv = map[string][4]float64{slotICInput: {0.2, 0.1, 0.3, 0.04}}
	if got, want := a.slotRect(slotICInput, def, 1000, 800), (sdl.Rect{X: 200, Y: 80, W: 300, H: 32}); got != want {
		t.Errorf("override: slotRect = %+v, want the moved spot %+v", got, want)
	}
}
