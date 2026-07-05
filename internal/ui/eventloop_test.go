package ui

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
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
	if !a.RenderNeeded(true) {
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
// (uiDirty) refuse the skip until a real frame absorbs them — in BOTH modes,
// since the heartbeat became opt-in (a static classic screen has no periodic
// frame left to absorb a missed signal).
func TestSkipFrameDamageRefuses(t *testing.T) {
	a := expApp(t)
	if !a.SkipFrame(true, false) {
		t.Fatal("precondition: the static lobby should skip")
	}
	a.uiDirty = true
	if a.SkipFrame(true, false) {
		t.Error("exp: pending packet damage must render")
	}
	if !a.RenderNeeded(true) {
		t.Error("uiDirty is redraw-worthy damage")
	}

	// Classic: the same damage check stands in for the retired always-on
	// heartbeat (an OOC line on a static courtroom must not wait forever).
	b := expApp(t)
	b.d.Prefs.SetEventDrivenLoop(false)
	b.room = &courtroom.Courtroom{}
	b.sess = &courtroom.Session{}
	if !b.SkipFrame(true, false) {
		t.Fatal("precondition: the static classic courtroom should skip")
	}
	b.uiDirty = true
	if b.SkipFrame(true, false) {
		t.Error("classic: pending packet damage must render (no heartbeat to absorb it)")
	}
}

// TestSkipFrameSharedRefusals pins the refusals shared by BOTH modes: input
// this pass, a live voice session (voicePump is Frame-driven), an open
// animated sprite preview — and the staleness heartbeat, which is a TOGGLE
// now (default OFF after the playtest verdict on the hardcoded 2 fps floor):
// stale alone must not force a frame until the user opts back in.
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
		a.lastFrameDrawn = time.Now().Add(-2 * paceHeartbeat)
		if !a.SkipFrame(true, false) {
			t.Errorf("exp=%v: heartbeat OFF (default): a stale static screen must keep skipping", exp)
		}
		a.d.Prefs.SetPaceHeartbeat(true)
		if a.SkipFrame(true, false) {
			t.Errorf("exp=%v: heartbeat ON: past the heartbeat a real frame must draw", exp)
		}
	}
}

// TestNextWakeDelay pins the scheduled-wake math: with the safety heartbeat
// OFF (the default) an empty schedule parks at the flat housekeeping cadence
// no matter how stale the screen is (the busy-spin guard); heartbeat ON keeps
// the classic time-to-stale remainder with its overdue floor; a focused caret
// and a RUNNING server clock pull the wake to their next due as REAL
// deadlines (scheduled=true — the expiry renders); a PAUSED clock schedules
// nothing; and the FROZEN sentinel silences the caret/clock wakes while the
// local alarm still lands its due-fire.
func TestNextWakeDelay(t *testing.T) {
	a := expApp(t)

	// Heartbeat OFF (default): the flat housekeeping cadence, unscheduled.
	if d, sched := a.NextWakeDelay(true); d != paceHeartbeat || sched {
		t.Errorf("empty schedule (heartbeat off): wake = %v sched=%v, want the flat %v cadence, unscheduled", d, sched, paceHeartbeat)
	}
	// …and a screen stale for an HOUR parks exactly the same. The remainder
	// math would pin at the minWakeDelay floor and busy-spin the park two
	// hundred times a second — the regression this base must never grow back.
	a.lastFrameDrawn = time.Now().Add(-time.Hour)
	if d, sched := a.NextWakeDelay(true); d != paceHeartbeat || sched {
		t.Errorf("stale + heartbeat off: wake = %v sched=%v, want the flat cadence (busy-spin guard)", d, sched)
	}

	// Heartbeat ON: the time-to-stale remainder — floored when overdue,
	// ≈ the full window right after a frame.
	a.d.Prefs.SetPaceHeartbeat(true)
	if d, _ := a.NextWakeDelay(true); d != minWakeDelay {
		t.Errorf("overdue heartbeat: wake = %v, want the %v floor", d, minWakeDelay)
	}
	a.lastFrameDrawn = time.Now()
	if d, _ := a.NextWakeDelay(true); d < paceHeartbeat-100*time.Millisecond || d > paceHeartbeat {
		t.Errorf("fresh frame + heartbeat on: wake = %v, want ≈ the %v remainder", d, paceHeartbeat)
	}
	a.d.Prefs.SetPaceHeartbeat(false)

	// A focused caret 300 ms into its blink: the flip is ~200 ms out, and it
	// is a REAL deadline (the expiry renders the flip).
	a.ctx.focusID = "field"
	a.ctx.caretAcc = 300 * time.Millisecond
	if d, sched := a.NextWakeDelay(true); d > 200*time.Millisecond || d < 100*time.Millisecond || !sched {
		t.Errorf("caret at 300ms: wake = %v sched=%v, want ≈ 200ms, scheduled", d, sched)
	}
	// FROZEN idle (typed 0): the caret stops blinking — no flip wake.
	a.d.Prefs.SetIdleFPS(config.FPSZero)
	if d, sched := a.NextWakeDelay(true); d != paceHeartbeat || sched {
		t.Errorf("frozen idle: caret wake = %v sched=%v, want none", d, sched)
	}
	// The sentinel is per focus state: only the IDLE knob is frozen, so the
	// UNFOCUSED schedule keeps its caret flip.
	if d, sched := a.NextWakeDelay(false); d > 200*time.Millisecond || !sched {
		t.Errorf("unfocused with only idle frozen: caret wake = %v sched=%v, want the flip", d, sched)
	}
	a.d.Prefs.SetIdleFPS(0)
	a.ctx.focusID = ""

	// A running server clock with 2.4 s left: wake just past the 0.4 s
	// boundary (the readout ticks on the second).
	a.sess = &courtroom.Session{}
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Running: true, Deadline: time.Now().Add(2400 * time.Millisecond)}
	if d, sched := a.NextWakeDelay(true); d < 300*time.Millisecond || d > 400*time.Millisecond+2*timerTickSlack || !sched {
		t.Errorf("running clock at x.4s: wake = %v sched=%v, want ≈ 400ms+slack, scheduled", d, sched)
	}
	// Frozen holds the chip's readout: no per-second wake.
	a.d.Prefs.SetIdleFPS(config.FPSZero)
	if d, sched := a.NextWakeDelay(true); d != paceHeartbeat || sched {
		t.Errorf("frozen idle: clock wake = %v sched=%v, want none", d, sched)
	}
	// …but the LOCAL alarm still fires. Far out (90.2 s): frozen schedules NO
	// per-second tick where unfrozen would (the readout is decoration)…
	a.timerEndAt = time.Now().Add(90*time.Second + 200*time.Millisecond)
	if d, sched := a.NextWakeDelay(true); d != paceHeartbeat || sched {
		t.Errorf("frozen + far alarm: wake = %v sched=%v, want no per-second tick", d, sched)
	}
	// …while CLOSE to due, frozen wakes exactly once, at the fire moment.
	a.timerEndAt = time.Now().Add(250 * time.Millisecond)
	if d, sched := a.NextWakeDelay(true); !sched || d < 150*time.Millisecond || d > 250*time.Millisecond+2*timerTickSlack {
		t.Errorf("frozen + due alarm: wake = %v sched=%v, want ≈ its due-fire", d, sched)
	}
	a.timerEndAt = time.Time{}
	a.d.Prefs.SetIdleFPS(0)

	// Paused server clock: frozen readout, nothing scheduled — back to the
	// flat cadence.
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Left: 90 * time.Second}
	if d, sched := a.NextWakeDelay(true); d != paceHeartbeat || sched {
		t.Errorf("paused clock: wake = %v sched=%v, want the flat cadence", d, sched)
	}
}

// TestSkipFrameFrozen pins the typed-0 FROZEN sentinel across both loop
// modes: the decoration refusals (caret blink, clock readouts) gate off in
// the state whose knob froze — and ONLY that state — while a RUNNING local
// alarm still keeps classic frames coming (its due-fire is Frame-driven).
func TestSkipFrameFrozen(t *testing.T) {
	// Experimental: a pending caret flip is normally damage; frozen it isn't.
	a := expApp(t)
	a.ctx.focusID = "field"
	a.ctx.caretOn = true
	a.drawnCaretOn = false // a flip is showable
	if !a.RenderNeeded(true) {
		t.Fatal("precondition: a pending caret flip is damage while unfrozen")
	}
	a.d.Prefs.SetIdleFPS(config.FPSZero)
	if a.RenderNeeded(true) {
		t.Error("frozen idle: the caret flip must not count as damage")
	}
	if !a.SkipFrame(true, false) {
		t.Error("frozen idle: the static screen must skip despite the caret")
	}
	if !a.RenderNeeded(false) {
		t.Error("only the IDLE knob is frozen: the unfocused state still draws its caret flip")
	}

	// Classic: the caret and clock refusals gate off under frozen…
	a.d.Prefs.SetEventDrivenLoop(false)
	a.room = &courtroom.Courtroom{}
	a.sess = &courtroom.Session{}
	if !a.SkipFrame(true, false) {
		t.Error("classic frozen: a focused field must not force the idle render")
	}
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Running: true, Deadline: time.Now().Add(time.Minute)}
	if !a.SkipFrame(true, false) {
		t.Error("classic frozen: a ticking server-clock readout holds — still skips")
	}
	// …but a RUNNING local alarm is an EVENT, not decoration: it must keep
	// frames coming or it would only ring when unrelated damage landed.
	a.timerEndAt = time.Now().Add(time.Minute)
	if a.SkipFrame(true, false) {
		t.Error("classic frozen: a running local alarm must keep frames coming (pollTimer is Frame-driven)")
	}
	a.timerEndAt = time.Time{}

	// The background knob freezes the unfocused state the same way — and only
	// that state.
	a.d.Prefs.SetIdleFPS(0)
	a.d.Prefs.SetUnfocusedFPS(config.FPSZero)
	if !a.SkipFrame(false, false) {
		t.Error("classic frozen background: the focused field must not force the render")
	}
	if a.SkipFrame(true, false) {
		t.Error("focused with only the BACKGROUND knob frozen: the caret refusal stands")
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

// TestIsRealInput pins the input-grace classification: only genuine user
// input arms the full-rate grace. Window/driver housekeeping still renders a
// frame (sawEvent) but must not hold max fps — with a big animated sprite on
// stage, its texture traffic fired such events every few seconds and the
// client burst to full rate for the grace second (playtest, test2).
func TestIsRealInput(t *testing.T) {
	real := []sdl.Event{
		&sdl.MouseMotionEvent{}, &sdl.MouseButtonEvent{}, &sdl.MouseWheelEvent{},
		&sdl.KeyboardEvent{}, &sdl.TextInputEvent{}, &sdl.TextEditingEvent{},
		&sdl.DropEvent{}, &sdl.TouchFingerEvent{},
	}
	for _, ev := range real {
		if !IsRealInput(ev) {
			t.Errorf("%T must count as real input", ev)
		}
	}
	housekeeping := []sdl.Event{
		&sdl.WindowEvent{}, &sdl.RenderEvent{}, &sdl.QuitEvent{},
		&sdl.UserEvent{}, &sdl.ClipboardEvent{}, &sdl.AudioDeviceEvent{},
	}
	for _, ev := range housekeeping {
		if IsRealInput(ev) {
			t.Errorf("%T must NOT arm the input grace", ev)
		}
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
