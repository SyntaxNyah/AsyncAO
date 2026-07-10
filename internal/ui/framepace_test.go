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

// TestFramePace pins the adaptive frame pacing (the GPU-burn fix): any recent
// input or live animation = the full cap, unfocused = the trickle, and the idle
// tier splits by loop mode. The classic loop naps at the idle rate; the event-
// driven loop paces a rendered-but-idle frame at the ACTIVE cap — a truly idle
// screen SKIPS and parks (NextWakeDelay owns the idle cadence there), so a
// FramePace-idle frame is a lone damage frame that must return to parking fast,
// not sleep out a whole idle period. Interaction ALWAYS restores the cap.
func TestFramePace(t *testing.T) {
	a := testTabApp(t)

	budget := func(fps int) time.Duration { return time.Second / time.Duration(fps) }

	// Pin the caps this test asserts against, independent of the shipped defaults
	// (which are now ∞ active / off idle / 5 unfocused).
	a.d.Prefs.SetFPSCap(60)
	a.d.Prefs.SetIdleFPS(2)
	a.d.Prefs.SetUnfocusedFPS(10)

	// Event-driven idle (the default loop): the active cap, not the idle rate.
	if got := a.FramePace(true); got != budget(60) {
		t.Fatalf("event-driven idle pace = %v, want the active cap %v (parking owns idle)", got, budget(60))
	}
	// Classic loop: the idle tier naps at the idle rate.
	a.d.Prefs.SetEventDrivenLoop(false)
	if got := a.FramePace(true); got != budget(2) {
		t.Fatalf("classic idle pace = %v, want the idle rate %v", got, budget(2))
	}
	a.d.Prefs.SetEventDrivenLoop(true)

	// Unfocused beats everything else (both modes).
	if got := a.FramePace(false); got != budget(10) {
		t.Errorf("unfocused pace = %v, want the 10 fps default budget", got)
	}
	// Input snaps to the full cap (the responsiveness guarantee).
	a.NoteInput()
	if got := a.FramePace(true); got != budget(60) {
		t.Errorf("post-input pace = %v, want the 60 fps active budget", got)
	}
	a.lastInputAt = time.Now().Add(-2 * time.Second) // grace expired (max hold is ~1s)

	// A live animation surface forces the full cap even with no input (the
	// replay transport here; the same branch covers maker/export/voice/toasts).
	a.replaying = true
	if got := a.FramePace(true); got != budget(60) {
		t.Errorf("replaying pace = %v, want the full cap", got)
	}
	a.replaying = false

	// Custom rates flow through: the classic idle tier follows the idle slider,
	// the active cap follows the active slider.
	a.d.Prefs.SetFPSCap(120)
	a.d.Prefs.SetIdleFPS(15)
	a.d.Prefs.SetEventDrivenLoop(false)
	if got := a.FramePace(true); got != budget(15) {
		t.Errorf("custom classic idle pace = %v, want 15 fps", got)
	}
	a.d.Prefs.SetEventDrivenLoop(true)
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

// TestAudioPaceActive pins the gate the main loop reads to keep audio smooth under
// a low present cap: advance the courtroom (and play its blips) at the fine audio
// cadence only when the LIVE room is typing AND the pacing budget is slower than the
// fine tick (else a single sleep would batch a whole present-period of blips). Replay
// / maker / export drive their own rooms, and a static room stays idle.
func TestAudioPaceActive(t *testing.T) {
	a := testTabApp(t)
	a.room = newRoomForTest(t) // a real room: HandleEvent runs the full begin() path

	if a.AudioActive() {
		t.Fatal("an idle room (no message) must not be audio-active")
	}
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(3, "Phoenix", "hello there")})
	if !a.AudioActive() {
		t.Fatalf("a typing room must be audio-active (phase %v)", a.room.Phase())
	}

	// Budget at/under the fine tick: nothing to spread → don't audio-pace.
	if a.AudioPaceActive(a.AudioFineTick()) {
		t.Error("a budget at the fine tick must not audio-pace")
	}
	// A slower budget with a typing room → audio-pace.
	if !a.AudioPaceActive(4 * a.AudioFineTick()) {
		t.Error("a budget slower than the fine tick with a typing room must audio-pace")
	}

	// Replay drives its own room: the live room is excluded from audio pacing.
	a.replaying = true
	if a.AudioActive() || a.AudioPaceActive(time.Second) {
		t.Error("replay must exclude the live room from audio pacing")
	}
	a.replaying = false

	// A nil room is never audio-active (lobby).
	a.room = nil
	if a.AudioActive() || a.AudioPaceActive(time.Second) {
		t.Error("a nil room (lobby) must not audio-pace")
	}
}

// TestHardCapBudget pins the INVIOLABLE ceiling the main loop sleeps
// uninterruptibly (so no input flood can exceed it): the active cap when
// focused, the unfocused cap when not, the ∞ sentinel as uncapped (0), and
// unfocused-off backstopped by the active cap.
func TestHardCapBudget(t *testing.T) {
	a := testTabApp(t)
	budget := func(fps int) time.Duration { return time.Second / time.Duration(fps) }

	// Defaults: focused → ∞ / uncapped (0 budget — vsync paces), unfocused → 5 fps.
	if got := a.HardCapBudget(true); got != 0 {
		t.Fatalf("focused hard cap = %v, want 0 (∞ / vsync is the shipped active default)", got)
	}
	if got := a.HardCapBudget(false); got != budget(config.UnfocusedFPSDefault) {
		t.Fatalf("unfocused hard cap = %v, want the unfocused default %v", got, budget(config.UnfocusedFPSDefault))
	}

	// Custom caps flow through both states.
	a.d.Prefs.SetFPSCap(30)
	a.d.Prefs.SetUnfocusedFPS(5)
	if got := a.HardCapBudget(true); got != budget(30) {
		t.Errorf("focused hard cap = %v, want 30 fps", got)
	}
	if got := a.HardCapBudget(false); got != budget(5) {
		t.Errorf("unfocused hard cap = %v, want 5 fps", got)
	}

	// ∞ (FPSUnlimited) is uncapped — 0 = no floor — in either state.
	a.d.Prefs.SetFPSCap(config.FPSUnlimited)
	a.d.Prefs.SetUnfocusedFPS(config.FPSUnlimited)
	if got := a.HardCapBudget(true); got != 0 {
		t.Errorf("focused ∞ hard cap = %v, want 0 (uncapped)", got)
	}
	if got := a.HardCapBudget(false); got != 0 {
		t.Errorf("unfocused ∞ hard cap = %v, want 0 (uncapped)", got)
	}

	// Unfocused OFF backstops at the active cap (a forced ceremony frame while
	// tabbed out still obeys something).
	a.d.Prefs.SetFPSCap(60)
	a.d.Prefs.SetUnfocusedFPS(config.FPSOff)
	if got := a.HardCapBudget(false); got != budget(60) {
		t.Errorf("unfocused-off hard cap = %v, want the active-cap backstop %v", got, budget(60))
	}
}

// TestEaseICScrollAnimates pins the IC-log smooth scroll onto the anim-chrome
// hook: mid-glide it marks frameAnimChrome, so at idle=0 the loop keeps
// rendering until the ease settles instead of freezing a message half-scrolled
// (the "the chat log jumps then stops" report). A settled offset or a 1:1
// scrollbar-drag snap marks nothing.
func TestEaseICScrollAnimates(t *testing.T) {
	a := &App{}
	a.frameDtMs = 16 // frame-rate-independent ease constant (promoted from sessionState)

	// Target far from the visual offset → still easing → animates, advancing partway.
	a.icScroll, a.icScrollVis, a.frameAnimChrome = 500, 0, false
	if got := a.easeICScroll(1000, false); got <= 0 || a.icScrollVis >= 500 {
		t.Errorf("ease should advance partway toward the target, got %v (vis=%v)", got, a.icScrollVis)
	}
	if !a.frameAnimChrome {
		t.Error("mid-ease must mark frameAnimChrome so the glide keeps rendering at idle=0")
	}

	// Settled within the deadband → no animation.
	a.icScroll, a.icScrollVis, a.frameAnimChrome = 500, 500, false
	a.easeICScroll(1000, false)
	if a.frameAnimChrome {
		t.Error("a settled scroll must not keep rendering")
	}

	// A scrollbar drag snaps 1:1 (no ease) → no animation even when far from target.
	a.icScroll, a.icScrollVis, a.frameAnimChrome = 0, 500, false
	a.easeICScroll(1000, true)
	if a.frameAnimChrome || a.icScrollVis != 0 {
		t.Errorf("a 1:1 drag snap must pin vis to target and not animate (vis=%v anim=%v)", a.icScrollVis, a.frameAnimChrome)
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
//
// NOTE: this pins FramePace's RETURN value only. The main loop now floors the
// actual inter-frame time at HardCapBudget (the unfocused cap), so when this
// 80 ms cadence is faster than the unfocused cap the RENDERED rate is the cap,
// not the cadence (the "caps are always obeyed" contract — the user's explicit
// ask). FramePace still returns the cadence as a tier hint; the loop clamps it.
// Don't "restore" a faster unfocused animation here thinking it regressed.
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
	a.d.Prefs.SetUnfocusedFPS(10) // pin the unfocused cap (shipped default is now 5)
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
	// Focused, the same live loop: the anim renders at its OWN 80 ms cadence — the
	// low idle default (2 fps) no longer bumps a slow loop up to the idle rate;
	// the content cadence rules (user ask: "cap to its refresh rate").
	if got := a.FramePace(true); got != 80*time.Millisecond {
		t.Errorf("focused pace with the 80ms loop = %v, want its 80ms flip cadence", got)
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

	// ∞ active cap uncaps interaction.
	a.d.Prefs.SetFPSCap(config.FPSUnlimited)
	a.NoteInput()
	if got := a.FramePace(true); got != 0 {
		t.Errorf("unlimited cap while interacting = %v, want 0 (uncapped)", got)
	}
	a.lastInputAt = time.Time{}

	// ∞ idle rate = "as fast as the active cap" (the Settings tooltip's own
	// words): the tier resolves THROUGH the cap, so a finite cap keeps ruling —
	// the old raw-0 return was indistinguishable from uncapped and, with the ∞
	// default cap, left an idle render loop with a zero-sleep budget (the
	// idle-CPU-burn class). Tested on the classic loop, where the tier is
	// observable.
	a.d.Prefs.SetFPSCap(60)
	a.d.Prefs.SetIdleFPS(config.FPSUnlimited)
	a.d.Prefs.SetEventDrivenLoop(false)
	if got := a.FramePace(true); got != paceBudget(60) {
		t.Errorf("unlimited idle rate = %v, want the active cap's budget %v", got, paceBudget(60))
	}
	// ∞ idle + ∞ cap: the finite backstop — never a zero budget for a frame
	// nothing demanded.
	a.d.Prefs.SetFPSCap(config.FPSUnlimited)
	if got := a.FramePace(true); got != paceBudget(config.FPSCapUnlimitedOff) {
		t.Errorf("∞ idle + ∞ cap = %v, want the %d fps backstop", got, config.FPSCapUnlimitedOff)
	}
	a.d.Prefs.SetFPSCap(60)
	a.d.Prefs.SetEventDrivenLoop(true)

	// ∞ background rate never throttles an unfocused window.
	a.d.Prefs.SetUnfocusedFPS(config.FPSUnlimited)
	if got := a.FramePace(false); got != 0 {
		t.Errorf("unlimited background rate = %v, want 0 (never throttle unfocused)", got)
	}
}

// TestFramePaceIdleOff pins the FPSOff ("0 = never redraw when idle") tier: a
// rendered-but-idle frame still needs SOME budget (it can't render at 0 fps), so
// off paces at the active cap in BOTH loop modes. The "no idle render at all"
// behaviour lives in the skip/park path (SkipFrame + NextWakeDelay), not here.
func TestFramePaceIdleOff(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetFPSCap(60)
	a.d.Prefs.SetIdleFPS(config.FPSOff)
	if got := a.FramePace(true); got != paceBudget(60) {
		t.Errorf("event-driven idle=off pace = %v, want the active cap (rendered idle frame ≠ 0 fps)", got)
	}
	a.d.Prefs.SetEventDrivenLoop(false)
	if got := a.FramePace(true); got != paceBudget(60) {
		t.Errorf("classic idle=off pace = %v, want the active cap", got)
	}
}

// TestFramePaceAnimatedChrome pins requirement #2's mechanism: a self-driven UI
// animation (NoteAnimating → drawnAnimChrome) paces at the ACTIVE cap in BOTH
// loop modes, so the motion stays smooth even with a low idle rate — it is not
// throttled to the idle tier the way a truly static screen is.
func TestFramePaceAnimatedChrome(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetFPSCap(60)
	a.d.Prefs.SetIdleFPS(2) // a low idle rate that must NOT throttle the animation
	a.drawnAnimChrome = true

	if got := a.FramePace(true); got != paceBudget(60) {
		t.Errorf("event-driven animated chrome = %v, want the active cap (not the idle rate)", got)
	}
	a.d.Prefs.SetEventDrivenLoop(false)
	if got := a.FramePace(true); got != paceBudget(60) {
		t.Errorf("classic animated chrome = %v, want the active cap (not the idle rate)", got)
	}

	// ∞ active cap: chrome animation gets the finite backstop, never 0 — a
	// perpetual census (FX chat text on a settled message, an animated theme
	// page, the viewport's ambient-FX census) re-arms indefinitely, and a zero
	// budget would disable pacing entirely (vsync is not a reliable throttle —
	// the idle-CPU-burn class). Input still returns the true uncapped full rate
	// through wantsFullRate, so responsiveness is untouched.
	a.d.Prefs.SetEventDrivenLoop(true)
	a.d.Prefs.SetFPSCap(config.FPSUnlimited)
	if got := a.FramePace(true); got != paceBudget(config.FPSCapUnlimitedOff) {
		t.Errorf("animated chrome under the ∞ cap = %v, want the %d fps backstop", got, config.FPSCapUnlimitedOff)
	}
}

// TestWantsFullRateStateGated pins the knob-not-state fix (the idle-CPU-burn
// report): the ambient-FX prefs alone must never hold full rate — they count
// only through the viewport draw-site census (Viewport.AmbientAnimating →
// NoteAnimating), which cannot fire on the lobby/Settings or an empty stage.
// The voice panel's OPEN flag no longer pins the rate either — only a live
// call (voiceJoined) does; a merely-open panel rides damage and input.
func TestWantsFullRateStateGated(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetRainbowSprites(true)
	a.d.Prefs.SetSpriteWobble(true)
	a.d.Prefs.SetSpriteSpin(true)
	a.d.Prefs.SetIdleBreath(true)
	a.d.Prefs.SetWeatherType(1)
	if a.wantsFullRate() {
		t.Fatal("FX pref knobs alone must not hold full rate (knob-not-state)")
	}
	a.showVoice = true
	if a.wantsFullRate() {
		t.Fatal("an open-but-idle voice panel must not hold full rate")
	}
	a.voiceJoined = true
	if !a.wantsFullRate() {
		t.Fatal("a live voice call needs full rate")
	}
}

// TestSkipFrameClassicCaret pins the classic loop's caret refusal as
// state-gated: a merely-FOCUSED field must not force a render every pass —
// the IC input is habitually focused in the courtroom, and the blanket focus
// refusal kept the classic loop rendering every pass forever (with the ∞
// default cap, a zero-sleep spin). Only a blink phase that actually FLIPPED
// since the last drawn frame refuses the skip.
func TestSkipFrameClassicCaret(t *testing.T) {
	a := testTabApp(t)
	a.d.Prefs.SetEventDrivenLoop(false)
	a.d.Prefs.SetIdleFPS(config.FPSOff) // no classic idle heartbeat in this test
	a.room = &courtroom.Courtroom{}
	a.sess = &courtroom.Session{}
	a.lastInputAt, a.lastMotionAt = time.Time{}, time.Time{}
	a.ctx.focusID = "ic-input"
	a.ctx.caretOn = true
	a.drawnCaretOn = true // the blink state already on screen
	if !a.SkipFrame(true, false) {
		t.Fatal("a focused-but-unflipped caret must skip (the blanket focus refusal was the classic render spin)")
	}
	a.ctx.caretOn = false // the blink flipped since the last drawn frame
	if a.SkipFrame(true, false) {
		t.Fatal("a stale caret flip must refuse the skip and draw")
	}
}

// TestMotionGrace pins the pointer-motion split (experimental loop): bare
// motion holds full rate only through the short motion grace, while the
// classic loop keeps treating motion as plain input.
func TestMotionGrace(t *testing.T) {
	a := testTabApp(t)

	// Default (v1.55.1): per-event motion redraw is ON, so bare motion does NOT arm
	// the full-rate grace — the motion event's own frame renders and the loop re-parks.
	a.NoteMotion()
	if a.wantsFullRate() {
		t.Error("default (per-event motion redraw ON): bare motion must NOT hold full rate")
	}

	// Off: motion arms the short full-rate grace (the pre-v1.55.1 behaviour).
	a.d.Prefs.SetMotionRedrawPerEvent(false)
	a.NoteMotion()
	if !a.wantsFullRate() {
		t.Error("per-event redraw off: a moving pointer must render at full rate")
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

// TestPaceHelpers pins the tiny pace math: non-positive fps = uncapped for
// paceBudget, rateBudget's off/∞ trichotomy, and clampDur is [lo,hi] inclusive.
func TestPaceHelpers(t *testing.T) {
	if paceBudget(0) != 0 || paceBudget(-3) != 0 {
		t.Error("non-positive fps must mean uncapped (0)")
	}
	if paceBudget(50) != 20*time.Millisecond {
		t.Errorf("paceBudget(50) = %v, want 20ms", paceBudget(50))
	}

	// rateBudget: the landmine guard — a 0 budget must be distinguishable as
	// "uncapped" (∞) vs "off" (FPSOff), never conflated.
	if b, off := rateBudget(config.FPSUnlimited); b != 0 || off {
		t.Errorf("rateBudget(∞) = (%v,%v), want (0,false) uncapped", b, off)
	}
	if b, off := rateBudget(config.FPSOff); b != 0 || !off {
		t.Errorf("rateBudget(off) = (%v,%v), want (0,true) off", b, off)
	}
	if b, off := rateBudget(4); b != 250*time.Millisecond || off {
		t.Errorf("rateBudget(4) = (%v,%v), want (250ms,false)", b, off)
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
