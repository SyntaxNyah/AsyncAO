package ui

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// compApp is expApp for the compositor decision: a fresh-walked static lobby,
// with the pointer snapshots aligned so motion tests start from "unmoved".
func compApp(t *testing.T) *App {
	t.Helper()
	a := expApp(t)
	a.drawnMouseX, a.drawnMouseY = a.ctx.mouseX, a.ctx.mouseY
	return a
}

// TestDamageAccumulator pins the region bookkeeping: rects union and consume,
// overflow promotes to full, TakeDamage clamps to the logical frame, and a
// walk with an empty accumulator repaints everything (never nothing).
func TestDamageAccumulator(t *testing.T) {
	a := compApp(t)

	a.damageRect(sdl.Rect{X: 10, Y: 10, W: 20, H: 10})
	a.damageRect(sdl.Rect{X: 40, Y: 30, W: 10, H: 10})
	a.damageRect(sdl.Rect{}) // empty = "nothing drawn there", not damage
	clip, clipped := a.TakeDamage(200, 200)
	if !clipped {
		t.Fatal("two rects must clip, not full-frame")
	}
	if want := (sdl.Rect{X: 10, Y: 10, W: 40, H: 30}); clip != want {
		t.Fatalf("union = %+v, want %+v", clip, want)
	}
	if _, clipped := a.TakeDamage(200, 200); clipped {
		t.Fatal("TakeDamage must consume: an empty accumulator is a full-frame walk")
	}

	// Overflow promotes to full.
	for i := 0; i < dmgRectCap+1; i++ {
		a.damageRect(sdl.Rect{X: int32(i), Y: 0, W: 1, H: 1})
	}
	if _, clipped := a.TakeDamage(200, 200); clipped {
		t.Fatal("rect overflow must promote to a full-frame walk")
	}

	// Full swallows rects; the frame bound clamps oversized damage.
	a.damageRect(sdl.Rect{X: 0, Y: 0, W: 5, H: 5})
	a.damageAll()
	if _, clipped := a.TakeDamage(200, 200); clipped {
		t.Fatal("damageAll must win over accumulated rects")
	}
	a.damageRect(sdl.Rect{X: -50, Y: 150, W: 500, H: 500})
	clip, clipped = a.TakeDamage(200, 200)
	if !clipped || clip != (sdl.Rect{X: 0, Y: 150, W: 200, H: 50}) {
		t.Fatalf("damage must clamp to the frame, got %+v clipped=%v", clip, clipped)
	}
}

// TestWalkNeededStaticScreen pins the headline: a static screen walks NOTHING
// — no input, no damage, no ceremonies, a resting pointer — pass after pass.
func TestWalkNeededStaticScreen(t *testing.T) {
	a := compApp(t)
	for i := 0; i < 3; i++ {
		if a.WalkNeeded(true, false) {
			t.Fatalf("pass %d: a static lobby must not walk", i)
		}
	}
	if a.dmgFull || a.dmgCount != 0 {
		t.Error("a no-walk decision must accumulate no damage")
	}
}

// TestWalkNeededDiscreteSources pins the whole-frame triggers: real input,
// packet damage (uiDirty), texture traffic (store generation), and damage
// pre-accumulated by the loop (canvas rebuilds).
func TestWalkNeededDiscreteSources(t *testing.T) {
	a := compApp(t)
	if !a.WalkNeeded(true, true) {
		t.Error("real input must walk")
	}
	if _, clipped := a.TakeDamage(100, 100); clipped {
		t.Error("input damage is full-frame")
	}

	a.uiDirty = true
	if !a.WalkNeeded(true, false) {
		t.Error("uiDirty (packets) must walk")
	}
	a.uiDirty = false
	a.dmgFull, a.dmgCount = false, 0

	a.DamageAll() // the loop's resize / targets-reset / scale-change path
	if !a.WalkNeeded(true, false) {
		t.Error("pre-accumulated damage must walk on its own")
	}
	a.dmgFull = false
}

// TestWalkNeededCaret pins the caret tier: a blink flip walks exactly the
// focused field's rect; an unflipped caret walks nothing.
func TestWalkNeededCaret(t *testing.T) {
	a := compApp(t)
	a.ctx.focusID = "field"
	a.ctx.focusRect = sdl.Rect{X: 30, Y: 40, W: 100, H: 22}
	a.ctx.caretOn = true
	a.drawnCaretOn = true

	if a.WalkNeeded(true, false) {
		t.Fatal("matching caret state: no walk")
	}
	a.ctx.caretOn = false // BeginFrame flipped it
	if !a.WalkNeeded(true, false) {
		t.Fatal("a caret flip must walk")
	}
	clip, clipped := a.TakeDamage(500, 500)
	if !clipped || clip != a.ctx.focusRect {
		t.Fatalf("caret damage = %+v clipped=%v, want exactly the field rect", clip, clipped)
	}
}

// TestWalkNeededHoverCensus pins the motion tier: dead-space motion walks
// nothing at all; crossing a recorded hover rect walks exactly that rect; a
// drag or a showing tooltip promotes to full; census overflow is conservative.
func TestWalkNeededHoverCensus(t *testing.T) {
	a := compApp(t)
	btn := sdl.Rect{X: 100, Y: 100, W: 80, H: 24}
	a.ctx.BeginDraw()
	a.ctx.noteHoverRect(btn)

	// Dead-space motion: pointer moved, nothing crossed → zero walks.
	a.drawnMouseX, a.drawnMouseY = 10, 10
	a.ctx.mouseX, a.ctx.mouseY = 20, 30
	if a.WalkNeeded(true, false) {
		t.Fatal("dead-space motion must not walk")
	}

	// Crossing INTO the button: walk, damage = the button.
	a.ctx.mouseX, a.ctx.mouseY = 110, 110
	if !a.WalkNeeded(true, false) {
		t.Fatal("crossing into a hover rect must walk")
	}
	clip, clipped := a.TakeDamage(500, 500)
	if !clipped || clip != btn {
		t.Fatalf("crossing damage = %+v clipped=%v, want the button rect", clip, clipped)
	}
	a.drawnMouseX, a.drawnMouseY = 110, 110

	// Moving WITHIN the button: still hovered, nothing flips → no walk.
	a.ctx.mouseX, a.ctx.mouseY = 120, 112
	if a.WalkNeeded(true, false) {
		t.Fatal("motion within one hover state must not walk")
	}

	// A drag tracks the pointer anywhere → full.
	a.ctx.mouseDown = true
	a.ctx.mouseX = 121
	if !a.WalkNeeded(true, false) {
		t.Fatal("a drag must walk")
	}
	if _, clipped := a.TakeDamage(500, 500); clipped {
		t.Error("drag damage is full-frame")
	}
	a.ctx.mouseDown = false
	a.drawnMouseX = 121

	// A showing tooltip rides the cursor → full on any move.
	a.drawnTipShowing = true
	a.ctx.mouseX = 125
	if !a.WalkNeeded(true, false) {
		t.Fatal("motion with a tooltip showing must walk")
	}
	if _, clipped := a.TakeDamage(500, 500); clipped {
		t.Error("tooltip-follow damage is full-frame")
	}
	a.drawnTipShowing = false
	a.drawnMouseX = 125

	// Census overflow: conservative full on any move.
	a.ctx.hoverRectsFull = true
	a.ctx.mouseX = 126
	if !a.WalkNeeded(true, false) {
		t.Fatal("a full census must walk on motion")
	}
	if _, clipped := a.TakeDamage(500, 500); clipped {
		t.Error("census-overflow damage is full-frame")
	}
}

// TestWalkNeededCeremonyAndRoomState pins the message tiers: a busy room
// walks at the talk cadence with viewport ∪ chatbox ∪ log damage, and a room
// STATE change that landed on a skipped pass (the linger expiring, a queue
// pop) walks the same regions even though nothing else fired.
func TestWalkNeededCeremonyAndRoomState(t *testing.T) {
	a := compApp(t)
	a.room = newRoomForTest(t)
	a.sess = &courtroom.Session{}
	a.screen = ScreenCourtroom
	a.drawnVPRect = sdl.Rect{X: 0, Y: 0, W: 320, H: 240}
	a.drawnChatRect = sdl.Rect{X: 0, Y: 180, W: 320, H: 60}
	a.drawnLogRect = sdl.Rect{X: 330, Y: 0, W: 150, H: 240}

	// Idle room, everything matching: static.
	if a.WalkNeeded(true, false) {
		t.Fatal("an idle courtroom must not walk")
	}

	// The typewriter advanced on a pumped pass (VisibleRunes moved): the
	// ceremony regions walk.
	a.room.Scene.VisibleRunes = 7
	a.drawnRoomShown = 3
	if !a.WalkNeeded(true, false) {
		t.Fatal("a typewriter advance must walk")
	}
	clip, clipped := a.TakeDamage(1000, 1000)
	if !clipped {
		t.Fatal("ceremony damage must be region-clipped")
	}
	want := unionRect(unionRect(a.drawnVPRect, a.drawnChatRect), a.drawnLogRect)
	if clip != want {
		t.Fatalf("ceremony damage = %+v, want vp∪chat∪log = %+v", clip, want)
	}
	a.drawnRoomShown = a.room.Scene.VisibleRunes

	// Talk-cadence throttle: a busy room fresh off a walk waits its budget…
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(1, "Witch", "the crawl paces the walks")})
	if !a.roomBusy() {
		t.Fatal("precondition: the enqueued message must make the room busy")
	}
	// Resync the drawn snapshots to "the last walk saw this state" so only
	// the busy-cadence branch decides.
	a.drawnRoomPhase = a.room.Phase()
	a.drawnRoomQueue = a.room.QueueLen()
	a.drawnRoomShown = a.room.Scene.VisibleRunes
	a.lastFrameDrawn = time.Now()
	if a.WalkNeeded(true, false) {
		t.Fatal("a busy room inside its talk budget must wait")
	}
	// …and walks once it elapses.
	a.lastFrameDrawn = time.Now().Add(-time.Second)
	if !a.WalkNeeded(true, false) {
		t.Fatal("a busy room past its talk budget must walk")
	}
}

// TestWalkNeededStageCensus pins the viewport tier: an AnimGen move walks
// exactly the stage rect; a ramp (shout punch inflates PAST the stage) and an
// unmasked stage (clip-sprites off) promote to full.
func TestWalkNeededStageCensus(t *testing.T) {
	a := compApp(t)
	a.d.Viewport = testViewport()
	a.drawnVPRect = sdl.Rect{X: 8, Y: 8, W: 320, H: 240}
	a.drawnAnimGen = a.d.Viewport.AnimGen() + 1 // a flip landed since the last walk

	if !a.WalkNeeded(true, false) {
		t.Fatal("an AnimGen move must walk")
	}
	clip, clipped := a.TakeDamage(1000, 1000)
	if !clipped || clip != a.drawnVPRect {
		t.Fatalf("stage damage = %+v clipped=%v, want the stage rect", clip, clipped)
	}

	// Clip-sprites OFF: offsets may spill anywhere → full.
	a.d.Prefs.SetClipSpritesToStage(false)
	a.drawnAnimGen = a.d.Viewport.AnimGen() + 1
	if !a.WalkNeeded(true, false) {
		t.Fatal("unmasked stage: an AnimGen move must still walk")
	}
	if _, clipped := a.TakeDamage(1000, 1000); clipped {
		t.Error("unmasked stage damage must be full-frame")
	}
}

// TestWalkNeededContinuousSurfaces pins the full-budget tier: Settings (live
// meters), voice, the sprite preview, animated chrome and warning toasts walk
// at the active cap — throttled inside the budget, walking past it.
func TestWalkNeededContinuousSurfaces(t *testing.T) {
	states := []struct {
		name  string
		apply func(a *App)
	}{
		{"settings screen", func(a *App) { a.screen = ScreenSettings }},
		{"voice session", func(a *App) { a.voiceJoined = true }},
		{"sprite preview", func(a *App) { a.previewBase = "srv/characters/witch/(a)normal" }},
		{"animated chrome", func(a *App) { a.drawnAnimChrome = true }},
		{"debug overlay", func(a *App) { a.d.Prefs.SetDebugOverlay(true) }},
	}
	for _, st := range states {
		a := compApp(t)
		st.apply(a)
		a.lastFrameDrawn = time.Now() // fresh walk: inside the cap budget
		if a.WalkNeeded(true, false) {
			t.Errorf("%s: inside the cap budget must throttle", st.name)
		}
		a.lastFrameDrawn = time.Now().Add(-time.Second)
		if !a.WalkNeeded(true, false) {
			t.Errorf("%s: past the cap budget must walk (full)", st.name)
		}
		if _, clipped := a.TakeDamage(500, 500); clipped {
			t.Errorf("%s: continuous-surface damage is full-frame", st.name)
		}
	}
}

// TestWalkNeededTickingReadouts pins the per-second tier: a running local
// alarm (or a visible server clock) walks once per displayed second.
func TestWalkNeededTickingReadouts(t *testing.T) {
	a := compApp(t)
	a.timerEndAt = time.Now().Add(time.Minute)
	a.lastFrameDrawn = time.Now()
	if a.WalkNeeded(true, false) {
		t.Fatal("a ticking readout inside its second must wait")
	}
	a.lastFrameDrawn = time.Now().Add(-1100 * time.Millisecond)
	if !a.WalkNeeded(true, false) {
		t.Fatal("a ticking readout past its second must walk")
	}
}

// TestWalkNeededTooltipReveal pins the dwell tier: an elapsed tooltip dwell
// with nothing showing walks once (full — the box position isn't known until
// it draws); once shown, the trigger stops.
func TestWalkNeededTooltipReveal(t *testing.T) {
	a := compApp(t)
	a.ctx.tipHoverID = "btn"
	a.ctx.tipHoverSince = time.Now().Add(-2 * tooltipDwell)
	if !a.WalkNeeded(true, false) {
		t.Fatal("an elapsed tooltip dwell must walk the reveal")
	}
	a.dmgFull, a.dmgCount = false, 0
	a.drawnTipShowing = true // the reveal walk drew it
	a.drawnMouseX, a.drawnMouseY = a.ctx.mouseX, a.ctx.mouseY
	if a.WalkNeeded(true, false) {
		t.Fatal("a shown tooltip must stop the reveal trigger")
	}
}

// TestWalkNeededRiders pins the per-walk riders: any walk with a focused
// field keeps the field in the damage (Frame's caret snapshot is
// frame-global — a clip that excluded the field would strand stale caret
// pixels), and any walk with a tooltip showing covers the tip's last box.
func TestWalkNeededRiders(t *testing.T) {
	a := compApp(t)
	a.ctx.focusID = "field"
	a.ctx.focusRect = sdl.Rect{X: 400, Y: 400, W: 90, H: 20}
	a.ctx.caretOn = true
	a.drawnCaretOn = true // caret itself is NOT the trigger
	a.drawnTipShowing = true
	a.ctx.lastTipBox = sdl.Rect{X: 50, Y: 60, W: 120, H: 30}

	// Trigger an unrelated rect walk: a stage flip.
	a.d.Viewport = testViewport()
	a.drawnVPRect = sdl.Rect{X: 0, Y: 0, W: 10, H: 10}
	a.drawnAnimGen = a.d.Viewport.AnimGen() + 1
	if !a.WalkNeeded(true, false) {
		t.Fatal("stage flip must walk")
	}
	clip, clipped := a.TakeDamage(1000, 1000)
	if !clipped {
		t.Fatal("this walk should be region-clipped")
	}
	for _, must := range []sdl.Rect{a.ctx.focusRect, a.ctx.lastTipBox} {
		if r, ok := clip.Intersect(&must); !ok || r != must {
			t.Errorf("walk damage %+v must contain the rider rect %+v", clip, must)
		}
	}
}

// TestWalkNeededContinuousEraseWalk pins the self-ending-surface heal: a
// warning toast holds full-budget walks while it shows; when it expires on
// its own, the drawnContinuous snapshot buys exactly ONE more walk — the one
// that erases it from the canvas — and then the walks stop.
func TestWalkNeededContinuousEraseWalk(t *testing.T) {
	a := compApp(t)
	a.warnLine = "toast"
	a.warnAt = time.Now()
	a.lastFrameDrawn = time.Now().Add(-time.Second)
	if !a.WalkNeeded(true, false) {
		t.Fatal("a live toast must walk")
	}
	a.dmgFull, a.dmgCount = false, 0
	a.drawnContinuous = true // the walk above drew it

	a.warnAt = time.Now().Add(-2 * warnShowDuration) // the toast expired between passes
	if !a.WalkNeeded(true, false) {
		t.Fatal("an expired toast must get its erase walk")
	}
	if _, clipped := a.TakeDamage(500, 500); clipped {
		t.Error("the erase walk must repaint in full")
	}
	a.drawnContinuous = false // the erase walk saw no continuous surface
	if a.WalkNeeded(true, false) {
		t.Fatal("after the erase walk the screen is static again")
	}
}

// TestWalkNeededPendingPollResults pins the async-result probe: a result
// buffered for a Frame-driven poll (here the log browser's) walks the frame
// that will drain it, even on an otherwise static screen.
func TestWalkNeededPendingPollResults(t *testing.T) {
	a := compApp(t)
	a.logBrowserRes = make(chan logBrowserLoad, 1)
	if a.WalkNeeded(true, false) {
		t.Fatal("an empty result channel must not walk")
	}
	a.logBrowserRes <- logBrowserLoad{}
	if !a.WalkNeeded(true, false) {
		t.Fatal("a buffered poll result must walk (its poll only runs inside Frame)")
	}
	if _, clipped := a.TakeDamage(500, 500); clipped {
		t.Error("poll-result damage is full-frame")
	}
}

// TestWalkNeededStaticAllocs pins the decision's cost on the hot no-walk
// path: zero heap allocations per pass (rule §17 — the skip path runs at the
// panel rate).
func TestWalkNeededStaticAllocs(t *testing.T) {
	a := compApp(t)
	if n := testing.AllocsPerRun(1000, func() { a.WalkNeeded(true, false) }); n != 0 {
		t.Fatalf("WalkNeeded allocates %.1f/op on the static path, want 0", n)
	}
}

// TestHoverCrossingsOutParam pins the kit census reporter: crossings land in
// the caller's scratch, out-of-space and census overflow both report
// complete=false (the caller full-damages).
func TestHoverCrossingsOutParam(t *testing.T) {
	c := &Ctx{}
	c.BeginDraw()
	c.noteHoverRect(sdl.Rect{X: 0, Y: 0, W: 10, H: 10})
	c.noteHoverRect(sdl.Rect{X: 20, Y: 0, W: 10, H: 10})

	var out [4]sdl.Rect
	n, crossed, complete := c.HoverCrossings(-5, 5, 5, 5, out[:]) // enters rect 1 only
	if n != 1 || !crossed || !complete {
		t.Fatalf("single crossing: n=%d crossed=%v complete=%v", n, crossed, complete)
	}
	if out[0] != (sdl.Rect{X: 0, Y: 0, W: 10, H: 10}) {
		t.Fatalf("crossed rect = %+v", out[0])
	}

	// Both rects flip but out only holds one → incomplete.
	n, crossed, complete = c.HoverCrossings(5, 5, 25, 5, out[:1]) // leaves 1, enters 2
	if n != 1 || !crossed || complete {
		t.Fatalf("overflowing out: n=%d crossed=%v complete=%v, want 1 true false", n, crossed, complete)
	}

	c.hoverRectsFull = true
	if _, crossed, complete := c.HoverCrossings(0, 0, 1, 1, out[:]); !crossed || complete {
		t.Fatal("a full census must report crossed + incomplete")
	}
}

// testViewport builds a bare render.Viewport for gen-compare tests (no SDL
// needed — AnimGen/RampActive read plain fields).
func testViewport() *render.Viewport { return render.NewViewport(nil) }
