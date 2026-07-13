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
// Immediate, Additive, SFX, emoji, FX, React, input) has a distinct editor label, and an
// override repositions it through slotRect — so users can drag them apart in Edit Layout.
func TestICBarSlotsAreEditable(t *testing.T) {
	slots := []string{slotICColor, slotICShowname, slotICImmediate, slotICAdditive, slotICSFX, slotICEmoji, slotICFx, slotICReact, slotICInput}
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

	// Additive is the newest pulled-out piece (2.8 servers only): un-edited it draws at
	// its default spot (pixel-identical to the old fixed offset), and an override moves it.
	addDef := sdl.Rect{X: 120, Y: 60, W: 84, H: 26}
	if got := a.slotRect(slotICAdditive, addDef, 1000, 800); got != addDef {
		t.Errorf("Additive no override: slotRect = %+v, want the default %+v", got, addDef)
	}
	a.classicOv = map[string][4]float64{slotICAdditive: {0.1, 0.2, 0.084, 0.0325}}
	if got, want := a.slotRect(slotICAdditive, addDef, 1000, 800), (sdl.Rect{X: 100, Y: 160, W: 84, H: 26}); got != want {
		t.Errorf("Additive override: slotRect = %+v, want the moved spot %+v", got, want)
	}
}

// TestThemeKeysExposeAsyncICControls pins #4b: the AsyncAO-only IC controls are listed
// in themeLayoutKeys, so a custom theme that defines asyncao_ic_<x> in its design.ini has
// those rects loaded — letting theme-makers place colour/SFX/buttons separately instead
// of having AsyncAO cram them into ao2_ic_chat_message.
func TestThemeKeysExposeAsyncICControls(t *testing.T) {
	want := []string{
		"asyncao_ic_color", "asyncao_ic_immediate", "asyncao_ic_sfx",
		"asyncao_ic_emoji", "asyncao_ic_fx", "asyncao_ic_react",
	}
	have := map[string]bool{}
	for _, k := range themeLayoutKeys {
		have[k] = true
	}
	for _, k := range want {
		if !have[k] {
			t.Errorf("themeLayoutKeys missing %q — a theme can't position it (#4b)", k)
		}
	}
}
