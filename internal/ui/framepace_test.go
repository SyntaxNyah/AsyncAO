package ui

import (
	"image"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
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

// TestFramePaceUnfocusedFollowsAnim pins the unfocused tier onto a LIVE stage
// animation's schedule — an unfocused window is still visible (second-monitor
// play), and the flat trickle rate there was the "idle animations go choppy
// the moment I click into another window" report. With a resident 2×80 ms
// speaker loop: unfocused paces at the 80 ms flip cadence (not the flat
// 100 ms trickle), focused idle keeps its existing [full, idle] clamp, and a
// stage with nothing animating stays on the flat rate.
func TestFramePaceUnfocusedFollowsAnim(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a := testTabApp(t)
	a.room = &courtroom.Courtroom{}
	a.d.Viewport = render.NewViewport(store)
	budget := func(fps int) time.Duration { return time.Second / time.Duration(fps) }

	// Nothing animating: the flat unfocused trickle (regression guard).
	if got := a.FramePace(false); got != budget(10) {
		t.Fatalf("static unfocused pace = %v, want the flat 10 fps budget", got)
	}

	dec := &assets.Decoded{
		Frames:   []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 4, 4)), image.NewRGBA(image.Rect(0, 0, 4, 4))},
		Delays:   []time.Duration{80 * time.Millisecond, 80 * time.Millisecond},
		Animated: true,
		Width:    4, Height: 4,
	}
	if err := store.Upload("anim://speaker", dec); err != nil {
		t.Fatalf("upload: %v", err)
	}
	a.room.Scene.Speaker.Visible = true
	a.room.Scene.Speaker.Active = "anim://speaker"
	a.d.Viewport.Update(&a.room.Scene, 0) // bind the page (fresh frame: full 80 ms due)

	if got := a.FramePace(false); got != 80*time.Millisecond {
		t.Errorf("unfocused pace with a live 80ms loop = %v, want the 80ms flip cadence", got)
	}
	// Focused idle is unchanged: the same loop clamps at the 30 fps idle budget.
	if got := a.FramePace(true); got != budget(30) {
		t.Errorf("focused idle pace with the same loop = %v, want the idle cap %v", got, budget(30))
	}
}

// animSpeaker uploads a looping frames×delay speaker under base and binds it
// as the visible speaker (shared by the ceremony-ordering tests).
func animSpeaker(t *testing.T, store *render.TextureStore, a *App, base string, delay time.Duration) {
	t.Helper()
	dec := &assets.Decoded{
		Frames:   []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 4, 4)), image.NewRGBA(image.Rect(0, 0, 4, 4))},
		Delays:   []time.Duration{delay, delay},
		Animated: true,
		Width:    4, Height: 4,
	}
	if err := store.Upload(base, dec); err != nil {
		t.Fatalf("upload %s: %v", base, err)
	}
	a.room.Scene.Speaker.Visible = true
	a.room.Scene.Speaker.Active = base
	a.d.Viewport.Update(&a.room.Scene, 0) // bind the page (fresh frame: full delay due)
}

// TestFramePaceCeremonyBeatsSlowAnim is the regression test for the "text
// renders at idle fps over animated sprites" report: a message ceremony must
// pace at the TALK tier even when the speaker's animation flips slower — every
// mover schedules independently and the earliest deadline wins, so a slow
// lipflap can never drag the typewriter (and the blips it fires) down to the
// idle rate. A FASTER flip may still tighten the budget (it needs its frames).
func TestFramePaceCeremonyBeatsSlowAnim(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a := testTabApp(t)
	a.room = newRoomForTest(t) // a real room: enqueue runs the full begin() path
	a.d.Viewport = render.NewViewport(store)
	a.d.Prefs.SetIdleFPS(10) // the reported setting: idle floor at 10 fps
	budget := func(fps int) time.Duration { return time.Second / time.Duration(fps) }

	// Idle (no message) with a SLOW loop (2×200 ms): the content cadence,
	// clamped to the idle rate.
	animSpeaker(t, store, a, "anim://slowtalk", 200*time.Millisecond)
	if got := a.FramePace(true); got != budget(10) {
		t.Fatalf("idle pace over a slow loop = %v, want the 10 fps idle budget %v", got, budget(10))
	}

	// A message starts. Pin the typewriter to a plain 30 fps-ish crawl (the
	// default 18 ms interval would tighten the tier below the point) and
	// re-bind the slow speaker loop (begin() re-drives the scene).
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(1, "Witch", "the text must keep crawling")})
	if !a.roomBusy() {
		t.Fatal("test setup: the message never started a ceremony")
	}
	a.room.Typewriter.Interval = 50 * time.Millisecond
	animSpeaker(t, store, a, "anim://slowtalk", 200*time.Millisecond)
	if got, want := a.FramePace(true), paceBudget(staticTalkFPS); got != want {
		t.Errorf("ceremony over a slow animated speaker paces at %v, want the talk tier %v (the text-at-idle-fps bug)", got, want)
	}

	// A FAST lipflap (2×20 ms) tightens the ceremony below the talk tier.
	animSpeaker(t, store, a, "anim://fasttalk", 20*time.Millisecond)
	if got := a.FramePace(true); got != 20*time.Millisecond {
		t.Errorf("ceremony over a fast lipflap paces at %v, want its 20ms flip", got)
	}
}

// TestFramePaceUnlimited pins the ∞ knob semantics: an unlimited active cap
// uncaps interaction (vsync paces presents), an unlimited idle rate never
// throttles a static-but-rendering screen, and an unlimited background rate
// never throttles an unfocused window.
func TestFramePaceUnlimited(t *testing.T) {
	a := testTabApp(t)
	budget := func(fps int) time.Duration { return time.Second / time.Duration(fps) }

	a.d.Prefs.SetFPSCap(config.FPSUnlimited)
	if got := a.FramePace(true); got != budget(30) {
		t.Errorf("unlimited cap while idle = %v, want the untouched 30 fps idle budget", got)
	}
	a.NoteInput()
	if got := a.FramePace(true); got != 0 {
		t.Errorf("unlimited cap while interacting = %v, want 0 (uncapped)", got)
	}
	a.lastInputAt = time.Time{}

	a.d.Prefs.SetIdleFPS(config.FPSUnlimited)
	if got := a.FramePace(true); got != 0 {
		t.Errorf("unlimited idle rate = %v, want 0 (never throttle when idle)", got)
	}
	a.d.Prefs.SetUnfocusedFPS(config.FPSUnlimited)
	if got := a.FramePace(false); got != 0 {
		t.Errorf("unlimited background rate = %v, want 0 (never throttle unfocused)", got)
	}
}

// TestMotionGrace pins the pointer-motion split (experimental loop): bare
// motion holds full rate only through the short motion grace, while the
// classic loop keeps treating motion as plain input.
func TestMotionGrace(t *testing.T) {
	a := testTabApp(t)

	a.NoteMotion()
	if !a.wantsFullRate() {
		t.Error("a moving pointer must render at full rate")
	}
	a.lastMotionAt = time.Now().Add(-2 * motionInputGrace)
	if a.wantsFullRate() {
		t.Error("a stopped pointer must release full rate after the short motion grace")
	}

	// Classic loop: motion is plain input (byte-identical pacing to before).
	a.d.Prefs.SetEventDrivenLoop(false)
	a.NoteMotion()
	if time.Since(a.lastInputAt) > time.Second {
		t.Error("classic mode: NoteMotion must stamp the full input grace")
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
