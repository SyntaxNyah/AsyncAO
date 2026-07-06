package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// expApp is testTabApp with a fresh lastFrameDrawn, so SkipFrame decisions
// reflect the tested condition and not the idle-rate re-render. The experimental
// event-driven loop pref defaults ON.
func expApp(t *testing.T) *App {
	t.Helper()
	a := testTabApp(t)
	a.lastFrameDrawn = time.Now()
	return a
}

// TestSkipFrameExpExtendsToMenus pins the experimental loop's widened gate:
// static menu screens (Settings included) skip; async lobby sweeps, a live
// Settings surface, and the classic mode keep rendering.
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

	// Settings now skips when static (the user's ask — "virtually nothing moves").
	a.screen = ScreenSettings
	if !a.SkipFrame(true, false) {
		t.Error("exp: a static Settings screen must skip")
	}
	// …but a live surface on Settings still forces frames through the later gates
	// (here a sprite preview; the mic-test meter and streaming damage likewise).
	a.previewBase = "srv/characters/witch/(a)normal"
	if a.SkipFrame(true, false) {
		t.Error("exp: a live Settings preview must keep rendering")
	}
	a.previewBase = ""
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
// this pass, a live voice session (voicePump is Frame-driven), an open animated
// sprite preview, and on-screen animated chrome.
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
	}
}

// TestSkipFrameIdleCadence pins the heartbeat rework: the fixed 500 ms heartbeat
// is gone. Under the event-driven loop a static courtroom SKIPS however stale it
// gets — NextWakeDelay's idle-rate wake redraws it (or nothing does at idle=off);
// the park owns the cadence, not SkipFrame. The classic loop re-renders once per
// idle budget instead, and idle=off makes it skip indefinitely too.
func TestSkipFrameIdleCadence(t *testing.T) {
	a := expApp(t)
	a.room = &courtroom.Courtroom{}
	a.sess = &courtroom.Session{}

	// Event-driven: however stale, a static courtroom keeps skipping.
	a.lastFrameDrawn = time.Now().Add(-time.Hour)
	if !a.SkipFrame(true, false) {
		t.Error("exp: a stale static courtroom still skips (NextWakeDelay owns the idle re-render, not a heartbeat)")
	}

	// Classic, idle rate on: past one idle budget it re-renders.
	a.d.Prefs.SetEventDrivenLoop(false)
	a.d.Prefs.SetIdleFPS(4) // 250 ms budget
	a.lastFrameDrawn = time.Now()
	if !a.SkipFrame(true, false) {
		t.Error("classic: a just-drawn static courtroom skips within the idle budget")
	}
	a.lastFrameDrawn = time.Now().Add(-time.Second) // well past 250 ms
	if a.SkipFrame(true, false) {
		t.Error("classic: past the idle budget the static courtroom re-renders")
	}

	// Classic, idle=off: it skips indefinitely (leans on caret/timer/input).
	a.d.Prefs.SetIdleFPS(config.FPSOff)
	a.lastFrameDrawn = time.Now().Add(-time.Hour)
	if !a.SkipFrame(true, false) {
		t.Error("classic idle=off: no heartbeat — a static courtroom skips indefinitely")
	}
}

// TestSkipFrameBackgroundOff pins the 0-fps background cap: an UNFOCUSED window
// with UnfocusedFPS=off skips everything — even a full-rate input grace that
// would normally force a frame — so a stage animation can't drive frames while
// tabbed out. A FOCUSED window is unaffected, a live event always renders, and
// a voice/mic session still renders (its audio engine is frame-driven).
func TestSkipFrameBackgroundOff(t *testing.T) {
	a := expApp(t)
	a.d.Prefs.SetUnfocusedFPS(config.FPSOff)
	a.NoteInput() // full-rate input grace active: would normally force a render

	if !a.SkipFrame(false, false) {
		t.Error("bg-off + unfocused must skip even during the input grace (0-fps ceiling)")
	}
	if a.SkipFrame(true, false) {
		t.Error("bg-off must not make a FOCUSED window skip (it ignores the unfocused cap)")
	}
	if a.SkipFrame(false, true) {
		t.Error("a live event must render even when unfocused + bg-off")
	}
	a.voiceJoined = true
	if a.SkipFrame(false, false) {
		t.Error("a live voice session must keep rendering when unfocused + bg-off")
	}
	a.voiceJoined = false
}

// TestNextWakeDelay pins the park math and the render-vs-housekeeping flag: the
// idle-rate tick bounds an empty schedule and marks render, a focused caret and
// a RUNNING server clock pull the wake in (render — the clock tick MUST draw, the
// timer-freeze guard), idle=off falls back to the Background-only housekeeping
// floor with NO render (how idle=0 reaches zero redraws), and the floor guards a
// busy-spin on an overdue deadline.
func TestNextWakeDelay(t *testing.T) {
	a := expApp(t)

	// Idle-rate tick bounds an empty schedule and marks render.
	a.d.Prefs.SetIdleFPS(4) // 250 ms idle budget
	if d, render := a.NextWakeDelay(true); d > 250*time.Millisecond || d < 150*time.Millisecond || !render {
		t.Errorf("idle schedule: wake = %v render=%v, want ≈ 250ms + render", d, render)
	}

	// Isolate each scheduled deadline with the idle tick OFF.
	a.d.Prefs.SetIdleFPS(config.FPSOff)

	// A focused caret mid-blink: the flip pulls the wake in (render).
	a.ctx.focusID = "field"
	a.ctx.caretAcc = 300 * time.Millisecond
	if d, render := a.NextWakeDelay(true); d <= 0 || d >= caretBlink || !render {
		t.Errorf("caret flip: wake = %v render=%v, want a pending flip + render", d, render)
	}
	a.ctx.focusID = ""

	// A running server clock with 2.4 s left: wake just past the 0.4 s boundary (render).
	a.sess = &courtroom.Session{}
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Running: true, Deadline: time.Now().Add(2400 * time.Millisecond)}
	if d, render := a.NextWakeDelay(true); d < 300*time.Millisecond || d > 400*time.Millisecond+2*timerTickSlack || !render {
		t.Errorf("running clock at x.4s: wake = %v render=%v, want ≈ 400ms+slack + render", d, render)
	}
	// Paused: frozen readout, nothing scheduled — the housekeeping floor, no render.
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Left: 90 * time.Second}
	if d, render := a.NextWakeDelay(true); d != maxHousekeepingGap || render {
		t.Errorf("paused clock (idle=off): wake = %v render=%v, want the %v floor + no render", d, maxHousekeepingGap, render)
	}
	a.sess = nil

	// A demand-streamed grid with blank cells keeps the pump alive at idle=off:
	// wake at the demand cadence and MARK render (so the re-run draw re-demands).
	// The interval must beat the housekeeping floor or considerRender won't draw.
	a.drawnDemandPending = true
	if d, render := a.NextWakeDelay(true); d != assetDemandWakeInterval || !render {
		t.Errorf("demand pending (idle=off): wake = %v render=%v, want %v + render", d, render, assetDemandWakeInterval)
	}
	a.drawnDemandPending = false
	if d, render := a.NextWakeDelay(true); d != maxHousekeepingGap || render {
		t.Errorf("demand cleared (idle=off): wake = %v render=%v, want the %v floor + no render", d, render, maxHousekeepingGap)
	}

	// Overdue idle tick: floored, never zero/negative (busy-spin guard), render.
	a.d.Prefs.SetIdleFPS(4)
	a.lastFrameDrawn = time.Now().Add(-time.Hour)
	if d, render := a.NextWakeDelay(true); d != minWakeDelay || !render {
		t.Errorf("overdue: wake = %v render=%v, want the %v floor + render", d, minWakeDelay, render)
	}

	// Background OFF + unfocused: NO render wakes at all — even with an idle rate
	// set and a long-overdue idle tick, the window is asleep while tabbed out (a
	// 0-fps background cap reaches a true 0 fps). A FOCUSED window ignores it.
	a.d.Prefs.SetUnfocusedFPS(config.FPSOff)
	if d, render := a.NextWakeDelay(false); d != maxHousekeepingGap || render {
		t.Errorf("bg-off unfocused: wake = %v render=%v, want the %v floor + no render", d, render, maxHousekeepingGap)
	}
	if _, render := a.NextWakeDelay(true); !render {
		t.Error("bg-off must not silence a FOCUSED window's idle tick")
	}
}

// TestPollCharINIForcesRedraw pins the emote-load redraw: draining the async
// char.ini result clears the "Loading emotes…" state AND marks uiDirty, so the
// event-driven loop renders the freshly-loaded emote list at idle=0 instead of
// leaving it stranded until cursor motion. A nil-ini result takes the default
// (empty char.ini) branch — enough to exercise the clear + the dirty flag
// without firing the success path's network prefetch.
func TestPollCharINIForcesRedraw(t *testing.T) {
	a := testTabApp(t)
	if a.charINIres == nil {
		a.charINIres = make(chan charINIFetch, 1)
	}
	a.charINIBusy = true
	a.uiDirty = false
	a.charINIres <- charINIFetch{key: a.serverKey}
	a.pollCharINI()
	if a.charINIBusy {
		t.Error("pollCharINI must clear charINIBusy on a matching result")
	}
	if !a.uiDirty {
		t.Error("pollCharINI must mark uiDirty so the loaded emote list forces a redraw at idle=0")
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
