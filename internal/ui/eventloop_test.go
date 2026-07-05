package ui

import (
	"testing"
	"time"

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
		a.lastFrameDrawn = time.Now().Add(-2 * paceHeartbeat)
		if a.SkipFrame(true, false) {
			t.Errorf("exp=%v: past the heartbeat a real frame must draw", exp)
		}
	}
}

// TestNextWakeDelay pins the scheduled-wake math: the heartbeat bounds an
// empty schedule, a focused caret and a RUNNING server clock pull the wake to
// their next due, a PAUSED clock schedules nothing, and the floor prevents a
// busy-spin on an overdue deadline.
func TestNextWakeDelay(t *testing.T) {
	a := expApp(t)

	// Empty schedule: the heartbeat remainder (fresh frame → ≈ paceHeartbeat).
	if d := a.NextWakeDelay(); d < paceHeartbeat-100*time.Millisecond || d > paceHeartbeat {
		t.Errorf("empty schedule: wake = %v, want ≈ the %v heartbeat", d, paceHeartbeat)
	}

	// A focused caret 300 ms into its blink: the flip is ~200 ms out.
	a.ctx.focusID = "field"
	a.ctx.caretAcc = 300 * time.Millisecond
	if d := a.NextWakeDelay(); d > 200*time.Millisecond || d < 100*time.Millisecond {
		t.Errorf("caret at 300ms: wake = %v, want ≈ 200ms", d)
	}
	a.ctx.focusID = ""

	// A running server clock with 2.4 s left: wake just past the 0.4 s boundary.
	a.sess = &courtroom.Session{}
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Running: true, Deadline: time.Now().Add(2400 * time.Millisecond)}
	if d := a.NextWakeDelay(); d < 300*time.Millisecond || d > 400*time.Millisecond+2*timerTickSlack {
		t.Errorf("running clock at x.4s: wake = %v, want ≈ 400ms+slack", d)
	}
	// Paused: frozen readout, nothing scheduled — back to the heartbeat bound.
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Left: 90 * time.Second}
	if d := a.NextWakeDelay(); d < paceHeartbeat-100*time.Millisecond {
		t.Errorf("paused clock: wake = %v, want the heartbeat bound", d)
	}

	// Overdue heartbeat: floored, never zero/negative (busy-spin guard).
	a.lastFrameDrawn = time.Now().Add(-time.Hour)
	if d := a.NextWakeDelay(); d != minWakeDelay {
		t.Errorf("overdue: wake = %v, want the %v floor", d, minWakeDelay)
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
