package ui

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// expApp is testTabApp inside the heartbeat window, so SkipFrame decisions
// reflect the tested condition and not staleness. The experimental loop pref
// defaults ON.
func expApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.lastFrameDrawn = time.Now()
	return a
}

// TestSkipFrameExpExtendsToMenus pins the experimental loop's widened gate:
// static menu screens skip; async lobby sweeps, the Settings screen, and the
// classic mode keep rendering.
func TestSkipFrameExpExtendsToMenus(t *testing.T) {
	a := expApp(t)

	if !a.SkipFrame(true, false) {
		t.Error("exp: a static lobby must skip")
	}
	a.lobbyFetching = true
	if a.SkipFrame(true, false) {
		t.Error("exp: a lobby refresh in flight must keep rendering (spinner + row repaints)")
	}
	a.lobbyFetching = false
	a.pinging = true
	if a.SkipFrame(true, false) {
		t.Error("exp: a ping sweep must keep rendering")
	}
	a.pinging = false

	a.screen = ScreenSettings
	if a.SkipFrame(true, false) {
		t.Error("exp: Settings never skips (live style preview, mic meter)")
	}
	a.screen = ScreenAbout
	if !a.SkipFrame(true, false) {
		t.Error("exp: a static About screen must skip")
	}

	// Kill switch: classic mode never skips outside the courtroom.
	a.screen = ScreenLobby
	a.d.Prefs.SetEventDrivenLoop(false)
	if a.SkipFrame(true, false) {
		t.Error("classic: the lobby must keep its idle render")
	}
}

// TestSkipFrameCaretScheduled pins the caret conversion: under the
// experimental loop a focused field SKIPS while its blink state matches the
// drawn frame (the wake is scheduled via NextWakeDelay instead), and refuses
// the moment a flip is showable. Classic keeps the old always-render refusal.
func TestSkipFrameCaretScheduled(t *testing.T) {
	a := expApp(t)
	a.ctx.focusID = "field"
	a.ctx.caretOn = true
	a.drawnCaretOn = true

	if !a.SkipFrame(true, false) {
		t.Error("exp: a focused field with an un-flipped caret must skip (the flip wake is scheduled)")
	}
	a.ctx.caretOn = false // BeginFrame flipped it: the drawn frame is stale now
	if a.SkipFrame(true, false) {
		t.Error("exp: a pending caret flip must render")
	}
	if !a.RenderNeeded() {
		t.Error("a caret flip is redraw-worthy damage")
	}

	a.ctx.caretOn = true // matches drawn again
	a.d.Prefs.SetEventDrivenLoop(false)
	a.room = &courtroom.Courtroom{}
	a.sess = &courtroom.Session{}
	if a.SkipFrame(true, false) {
		t.Error("classic: a focused field must keep the idle render (caret blinks per frame)")
	}
}

// TestSkipFrameDamageRefuses pins the damage sources: drained packets
// (uiDirty) refuse the skip until a real frame absorbs them.
func TestSkipFrameDamageRefuses(t *testing.T) {
	a := expApp(t)
	if !a.SkipFrame(true, false) {
		t.Fatal("precondition: the static lobby should skip")
	}
	a.uiDirty = true
	if a.SkipFrame(true, false) {
		t.Error("exp: pending packet damage must render")
	}
	if !a.RenderNeeded() {
		t.Error("uiDirty is redraw-worthy damage")
	}
}

// TestSkipFrameSharedRefusals pins the refusals shared by BOTH modes: input
// this pass, the staleness heartbeat, a live voice session (voicePump is
// Frame-driven), and an open animated sprite preview.
func TestSkipFrameSharedRefusals(t *testing.T) {
	for _, exp := range []bool{true, false} {
		a := expApp(t)
		a.d.Prefs.SetEventDrivenLoop(exp)
		a.room = &courtroom.Courtroom{}
		a.sess = &courtroom.Session{}

		if a.SkipFrame(true, true) {
			t.Errorf("exp=%v: input this pass must render", exp)
		}
		if !a.SkipFrame(true, false) {
			t.Fatalf("exp=%v: precondition — static courtroom should skip", exp)
		}
		a.voiceJoined = true
		if a.SkipFrame(true, false) {
			t.Errorf("exp=%v: a live voice session must keep frames coming (voicePump)", exp)
		}
		a.voiceJoined = false
		a.previewBase = "srv/characters/witch/(a)normal"
		if a.SkipFrame(true, false) {
			t.Errorf("exp=%v: an open sprite preview animates at draw time — no skip", exp)
		}
		a.previewBase = ""
		a.drawnAnimChrome = true
		if a.SkipFrame(true, false) {
			t.Errorf("exp=%v: animated chrome on screen (theme art, splash, badge) must keep frames coming", exp)
		}
		a.drawnAnimChrome = false
		// Staleness: the classic loop heartbeats a real frame; the experimental
		// loop is damage-driven — a static screen stays at literally zero
		// renders no matter how long ago the last frame drew.
		a.lastFrameDrawn = time.Now().Add(-2 * paceHeartbeat)
		if exp {
			if !a.SkipFrame(true, false) {
				t.Error("exp: a static screen must render NOTHING regardless of staleness")
			}
		} else if a.SkipFrame(true, false) {
			t.Error("classic: past the heartbeat a real frame must draw")
		}
	}
}

// TestSkipFrameFrozenStates pins the 0 fps knob positions: Idle 0 freezes
// decoration while focused (animated chrome, scheduled anims — skipped), and
// Background 0 does the same while unfocused; real damage still renders in
// both, and the frozen caret stops counting as damage.
func TestSkipFrameFrozenStates(t *testing.T) {
	a := expApp(t)
	a.d.Prefs.SetIdleFPS(config.FPSZero)

	a.drawnAnimChrome = true // would refuse the skip normally
	if !a.SkipFrame(true, false) {
		t.Error("idle 0: animated chrome must FREEZE (skip), not keep rendering")
	}
	a.uiDirty = true
	if a.SkipFrame(true, false) {
		t.Error("idle 0: real damage (chat landing) must still render")
	}
	a.uiDirty = false
	// The frozen caret is not damage: a flip mismatch must not force frames.
	a.ctx.focusID = "field"
	a.ctx.caretOn = true
	a.drawnCaretOn = false
	if a.RenderNeeded() {
		t.Error("idle 0: a caret flip must not count as damage (the caret is frozen)")
	}
	if !a.SkipFrame(true, false) {
		t.Error("idle 0: a focused field must freeze with everything else")
	}

	// Background 0 freezes the unfocused window the same way.
	b := expApp(t)
	b.d.Prefs.SetUnfocusedFPS(config.FPSZero)
	b.drawnAnimChrome = true
	if !b.SkipFrame(false, false) {
		t.Error("background 0: an unfocused window must freeze decoration")
	}
	if b.SkipFrame(true, false) {
		t.Error("background 0 must not affect the FOCUSED window (chrome still renders)")
	}
}

// TestSkipFrameFrozenStageExempt pins the viewport exemption: 0 fps freezes
// UI decoration, never the scene — a scheduled sprite flip still renders,
// exactly on its own cadence.
func TestSkipFrameFrozenStageExempt(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a := expApp(t)
	a.room = newRoomForTest(t)
	a.d.Viewport = render.NewViewport(store)
	a.d.Prefs.SetIdleFPS(config.FPSZero)
	a.screen = ScreenLobby // any skippable screen; the stage check is screen-independent

	if !a.SkipFrame(true, false) {
		t.Fatal("precondition: a STATIC stage under idle 0 must skip")
	}
	animSpeaker(t, store, a, "anim://frozenidle", 120*time.Millisecond)
	if a.SkipFrame(true, false) {
		t.Error("idle 0 must NOT freeze the stage: a scheduled sprite flip renders")
	}
	if got := a.FramePace(true); got != 120*time.Millisecond {
		t.Errorf("frozen idle + live loop paces at %v, want the exact 120ms flip", got)
	}
}

// TestHoverDamage pins the dead-space motion rule: pointer movement redraws
// ONLY when it changes something — crossing a hover-reactive rect's boundary,
// dragging, or dragging a shown tooltip along. Motion over nothing renders
// nothing.
func TestHoverDamage(t *testing.T) {
	a := expApp(t)
	c := a.ctx
	c.BeginDraw()
	c.mouseX, c.mouseY = 10, 10
	_ = c.hovering(sdl.Rect{X: 100, Y: 100, W: 40, H: 20}) // the census records one button
	a.drawnMouseX, a.drawnMouseY = 10, 10

	if a.hoverDamage() {
		t.Error("a stationary pointer is never damage")
	}
	c.mouseX, c.mouseY = 30, 30
	if a.hoverDamage() {
		t.Error("dead-space motion must not redraw")
	}
	if !a.SkipFrame(true, false) {
		t.Error("a dead-space motion pass must still skip")
	}
	c.mouseX, c.mouseY = 110, 110
	if !a.hoverDamage() {
		t.Error("crossing INTO a hover-reactive rect must redraw (the highlight appears)")
	}
	if a.SkipFrame(true, false) {
		t.Error("a hover crossing must render")
	}
	a.drawnMouseX, a.drawnMouseY = 110, 110
	c.mouseX, c.mouseY = 125, 105
	if a.hoverDamage() {
		t.Error("moving WITHIN one hover state changes nothing — no redraw")
	}
	c.mouseX, c.mouseY = 300, 300
	if !a.hoverDamage() {
		t.Error("crossing OUT of a hover-reactive rect must redraw (the highlight clears)")
	}

	// A drag tracks the pointer wherever it is.
	a.drawnMouseX, a.drawnMouseY = 300, 300
	c.mouseX, c.mouseY = 301, 300
	c.mouseDown = true
	if !a.hoverDamage() {
		t.Error("a drag must redraw per move")
	}
	c.mouseDown = false
	// A shown tooltip is glued to the cursor.
	a.drawnTipShowing = true
	if !a.hoverDamage() {
		t.Error("a visible tooltip must follow the pointer")
	}
	a.drawnTipShowing = false

	// Census overflow: conservative — any move redraws.
	c.hoverRectsFull = true
	if !a.hoverDamage() {
		t.Error("a full census must fall back to redraw-on-motion")
	}
}

// TestNextWakeDelay pins the scheduled-wake math: the pump cadence bounds an
// empty schedule WITHOUT implying a render (scheduled=false — the literal-0
// idle), a focused caret and a RUNNING server clock pull the wake to their due
// (scheduled=true → one frame), a PAUSED clock schedules nothing, a frozen
// idle silences the caret, and the floor prevents a busy-spin on an overdue
// deadline.
func TestNextWakeDelay(t *testing.T) {
	a := expApp(t)

	// Empty schedule: the pump cadence cap, NOT a render.
	if d, sched := a.NextWakeDelay(); d != paceHeartbeat || sched {
		t.Errorf("empty schedule: wake = (%v, %v), want (%v, false — no render on expiry)", d, sched, paceHeartbeat)
	}

	// A focused caret 300 ms into its blink: the flip is ~200 ms out, and its
	// expiry renders the flip.
	a.ctx.focusID = "field"
	a.ctx.caretAcc = 300 * time.Millisecond
	if d, sched := a.NextWakeDelay(); !sched || d > 200*time.Millisecond || d < 100*time.Millisecond {
		t.Errorf("caret at 300ms: wake = (%v, %v), want ≈ 200ms scheduled", d, sched)
	}
	// A frozen idle rate silences the caret schedule (it stops blinking).
	a.d.Prefs.SetIdleFPS(config.FPSZero)
	if d, sched := a.NextWakeDelay(); sched || d != paceHeartbeat {
		t.Errorf("frozen idle: caret wake must vanish, got (%v, %v)", d, sched)
	}
	a.d.Prefs.SetIdleFPS(0)
	a.ctx.focusID = ""

	// A running server clock with 2.4 s left: wake just past the 0.4 s boundary.
	a.sess = &courtroom.Session{}
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Running: true, Deadline: time.Now().Add(2400 * time.Millisecond)}
	if d, sched := a.NextWakeDelay(); !sched || d < 300*time.Millisecond || d > 400*time.Millisecond+2*timerTickSlack {
		t.Errorf("running clock at x.4s: wake = (%v, %v), want ≈ 400ms+slack scheduled", d, sched)
	}
	// Paused: frozen readout, nothing scheduled — back to the pump cadence.
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Left: 90 * time.Second}
	if d, sched := a.NextWakeDelay(); sched || d != paceHeartbeat {
		t.Errorf("paused clock: wake = (%v, %v), want the unscheduled pump bound", d, sched)
	}

	// A pending auto-reconnect is a real deadline; overdue floors (never spins).
	a.autoReconnectAt = time.Now().Add(-time.Second)
	if d, sched := a.NextWakeDelay(); !sched || d != minWakeDelay {
		t.Errorf("overdue reconnect: wake = (%v, %v), want the %v floor, scheduled", d, sched, minWakeDelay)
	}
}

// TestNextHoverDue pins the Ctx hover deadlines: a pending TooltipAfter dwell
// reports its remainder, an already-shown one reports nothing (the reveal
// frame already drew), and the hover-preview dwell honours its enable flag.
func TestNextHoverDue(t *testing.T) {
	c := &Ctx{}
	if _, ok := c.NextHoverDue(); ok {
		t.Error("no pending dwells: ok must be false")
	}
	c.tipHoverID = "btn"
	c.tipHoverSince = time.Now().Add(-tooltipDwell / 2)
	if due, ok := c.NextHoverDue(); !ok || due <= 0 || due > tooltipDwell/2+50*time.Millisecond {
		t.Errorf("pending tooltip: due = %v ok=%v, want ≈ %v", due, ok, tooltipDwell/2)
	}
	c.tipHoverSince = time.Now().Add(-2 * tooltipDwell) // already showing
	if _, ok := c.NextHoverDue(); ok {
		t.Error("a shown tooltip needs no further wakes")
	}
	c.tipHoverID = ""
	c.hoverID = "emote:x"
	c.hoverSince = time.Now()
	c.hoverPreviewDelay = 3 * time.Second
	if _, ok := c.NextHoverDue(); ok {
		t.Error("hover previews disabled: no dwell to schedule")
	}
	c.hoverPreviewOn = true
	if due, ok := c.NextHoverDue(); !ok || due <= 0 || due > 3*time.Second {
		t.Errorf("pending preview dwell: due = %v ok=%v", due, ok)
	}
}

// TestCaretFlipQuery pins the two Ctx caret exposers the loop schedules with.
func TestCaretFlipQuery(t *testing.T) {
	c := &Ctx{}
	if _, ok := c.NextCaretFlip(); ok {
		t.Error("no focused field: no caret deadline")
	}
	if _, focused := c.CaretVisible(); focused {
		t.Error("no focused field: focused must be false")
	}
	c.focusID = "f"
	c.caretAcc = caretBlink - 100*time.Millisecond
	if due, ok := c.NextCaretFlip(); !ok || due != 100*time.Millisecond {
		t.Errorf("caret flip due = %v ok=%v, want 100ms", due, ok)
	}
	c.caretAcc = caretBlink + time.Millisecond // overdue clamps to 0
	if due, ok := c.NextCaretFlip(); !ok || due != 0 {
		t.Errorf("overdue caret flip = %v ok=%v, want 0", due, ok)
	}
}
