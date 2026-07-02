package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
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

// TestTalkBudget pins the blip-cadence floor (playtest: "at a lower framerate
// the blips are ALSO at a lower framerate"): while a message plays, the frame
// budget must never be slower than the typewriter's rune interval — one rune
// per frame keeps every blip boundary on its own frame — and never faster than
// the user's cap.
func TestTalkBudget(t *testing.T) {
	a := &App{}
	full := paceBudget(60)

	// No room: the flat staticTalkFPS cadence.
	if got, want := a.talkBudget(full), paceBudget(staticTalkFPS); got != want {
		t.Fatalf("no-room talk budget = %v, want %v", got, want)
	}

	// A faster typewriter tightens the budget to its rune interval.
	a.room = &courtroom.Courtroom{}
	a.room.Typewriter.Interval = 20 * time.Millisecond
	if got := a.talkBudget(full); got != 20*time.Millisecond {
		t.Fatalf("fast text talk budget = %v, want the 20ms rune interval", got)
	}

	// …but never past the frame cap.
	a.room.Typewriter.Interval = 5 * time.Millisecond
	if got := a.talkBudget(full); got != full {
		t.Fatalf("talk budget must floor at the cap budget: got %v, want %v", got, full)
	}

	// A slower typewriter than staticTalkFPS keeps the base cadence (the crawl
	// doesn't need MORE frames, but motion between runes still reads smoother).
	a.room.Typewriter.Interval = 200 * time.Millisecond
	if got, want := a.talkBudget(full), paceBudget(staticTalkFPS); got != want {
		t.Fatalf("slow text talk budget = %v, want the base %v", got, want)
	}
}

// TestPaceHelpers pins the tiny pace math: non-positive fps = uncapped, and
// clampDur is [lo,hi] inclusive.
func TestPaceHelpers(t *testing.T) {
	if paceBudget(0) != 0 || paceBudget(-3) != 0 {
		t.Error("non-positive fps must mean uncapped (0)")
	}
	if paceBudget(50) != 20*time.Millisecond {
		t.Errorf("paceBudget(50) = %v, want 20ms", paceBudget(50))
	}
	lo, hi := 10*time.Millisecond, 100*time.Millisecond
	if clampDur(5*time.Millisecond, lo, hi) != lo {
		t.Error("clampDur must floor at lo")
	}
	if clampDur(500*time.Millisecond, lo, hi) != hi {
		t.Error("clampDur must cap at hi")
	}
	if clampDur(50*time.Millisecond, lo, hi) != 50*time.Millisecond {
		t.Error("clampDur must pass an in-range value through")
	}
}
