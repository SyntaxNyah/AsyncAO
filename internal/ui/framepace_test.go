package ui

import (
	"testing"
	"time"
)

// TestFramePace pins the adaptive frame pacing (the GPU-burn fix): idle = the
// calm rate, any recent input or live animation = the full cap, unfocused = the
// trickle — and the "10 fps band-aid" objection stays answered: interaction
// ALWAYS restores the full cap instantly.
func TestFramePace(t *testing.T) {
	a := testTabApp(t)

	budget := func(fps int) time.Duration { return time.Second / time.Duration(fps) }

	// Idle (no room, no input, focused): the idle rate.
	if got := a.FramePace(true); got != budget(30) {
		t.Fatalf("idle pace = %v, want the 30 fps default budget %v", got, budget(30))
	}
	// Unfocused beats everything else.
	if got := a.FramePace(false); got != budget(10) {
		t.Errorf("unfocused pace = %v, want the 10 fps default budget", got)
	}
	// Input snaps to the full cap (the responsiveness guarantee).
	a.NoteInput()
	if got := a.FramePace(true); got != budget(60) {
		t.Errorf("post-input pace = %v, want the 60 fps active budget", got)
	}
	a.lastInputAt = time.Now().Add(-2 * fullRateInputGrace) // grace expired → idle again
	if got := a.FramePace(true); got != budget(30) {
		t.Errorf("expired grace pace = %v, want idle again", got)
	}

	// A live animation surface forces the full cap even with no input (the
	// replay transport here; the same branch covers maker/export/voice/toasts).
	a.replaying = true
	if got := a.FramePace(true); got != budget(60) {
		t.Errorf("replaying pace = %v, want the full cap", got)
	}
	a.replaying = false

	// Custom rates flow through (and the sliders' live changes with them).
	a.d.Prefs.SetFPSCap(120)
	a.d.Prefs.SetIdleFPS(15)
	if got := a.FramePace(true); got != budget(15) {
		t.Errorf("custom idle pace = %v, want 15 fps", got)
	}
	a.NoteInput()
	if got := a.FramePace(true); got != budget(120) {
		t.Errorf("custom active pace = %v, want 120 fps", got)
	}

	// The perf HUD's scrolling graph keeps full rate.
	a.lastInputAt = time.Time{}
	a.perfHUD = true
	if got := a.FramePace(true); got != budget(120) {
		t.Errorf("perf-HUD pace = %v, want the full cap", got)
	}
}
