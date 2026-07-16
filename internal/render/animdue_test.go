package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestNextAnimDue pins the frame pacer's content clock: a static stage reports
// nothing, a live multi-frame layer reports the time to ITS next flip (the
// earliest across layers wins), invisible/finished/frozen layers don't count,
// and a running ramp (crossfade) reports 0 = continuous. Pages are built by
// hand (frame textures are never dereferenced), so no SDL init is needed.
func TestNextAnimDue(t *testing.T) {
	v := NewViewport(nil)
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true

	if _, ok := v.NextAnimDue(scene); ok {
		t.Fatal("an empty stage must report no animation")
	}

	// A 3-frame speaker loop at 100 ms, 40 ms into the current frame → 60 ms due.
	v.speakerAnim.page = &TexturePage{
		Frames: make([]*sdl.Texture, 3),
		Delays: []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond},
	}
	v.speakerAnim.elapsed = 40 * time.Millisecond
	due, ok := v.NextAnimDue(scene)
	if !ok || due != 60*time.Millisecond {
		t.Fatalf("speaker loop: due=%v ok=%v, want 60ms true", due, ok)
	}

	// A faster background (20 ms remaining) wins the min.
	v.bgAnim.page = &TexturePage{
		Frames: make([]*sdl.Texture, 2),
		Delays: []time.Duration{50 * time.Millisecond, 50 * time.Millisecond},
	}
	v.bgAnim.elapsed = 30 * time.Millisecond
	if due, ok = v.NextAnimDue(scene); !ok || due != 20*time.Millisecond {
		t.Fatalf("bg loop must win the min: due=%v ok=%v, want 20ms true", due, ok)
	}

	// The speaker turning invisible drops it from the schedule (bg remains).
	scene.Speaker.Visible = false
	if due, ok = v.NextAnimDue(scene); !ok || due != 20*time.Millisecond {
		t.Fatalf("invisible speaker must not count: due=%v ok=%v", due, ok)
	}
	scene.Speaker.Visible = true

	// An overdue frame (elapsed past its delay) is never negative — it lands on
	// the schedule floor (a sustained-overdue asset must not free-run the loop;
	// a legit one-off overdue flip draws at most one floor late, sub-frame).
	v.bgAnim.elapsed = 80 * time.Millisecond
	if due, ok = v.NextAnimDue(scene); !ok || due != minAnimFrameDelay {
		t.Fatalf("overdue flip must clamp to the schedule floor: due=%v ok=%v, want %v true", due, ok, minAnimFrameDelay)
	}

	// A finished one-shot (preanim on its last frame) stops scheduling.
	v.bgAnim.page = nil
	v.speakerAnim.finished = true
	if _, ok = v.NextAnimDue(scene); ok {
		t.Fatal("a finished one-shot must not schedule redraws")
	}
	v.speakerAnim.finished = false

	// A frozen frame (delay ≤ 0 — advance() treats it as stopped) doesn't count.
	v.speakerAnim.page.Delays = []time.Duration{0, 0, 0}
	if _, ok = v.NextAnimDue(scene); ok {
		t.Fatal("zero-delay frames are frozen, not animating")
	}

	// A running speaker-swap crossfade is continuous: due 0 while it ramps —
	// but only when the knob is actually on.
	v.speakerAnim.fadeLeft = 120 * time.Millisecond
	if _, ok = v.NextAnimDue(scene); ok {
		t.Fatal("fadeLeft without the crossfade knob must not hold the rate")
	}
	v.crossfade = 200 * time.Millisecond
	if due, ok = v.NextAnimDue(scene); !ok || due != 0 {
		t.Fatalf("a running crossfade is continuous: due=%v ok=%v, want 0 true", due, ok)
	}
}

// TestNextAnimDueFloor pins the schedule floor: an asset authored below
// minAnimFrameDelay (decoders honor any positive delay verbatim — a delay=1
// GIF is 10 ms, WebP/APNG/AVIF can author 1 ms) must schedule redraws at the
// floor, never free-run the loop (with the ∞ default cap the pacing sleep
// never fired and vsync — unreliable for windowed presents — was the only
// brake: the idle-CPU-burn class). Playback speed is untouched: advance()
// folds every elapsed frame per step.
func TestNextAnimDueFloor(t *testing.T) {
	v := NewViewport(nil)
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	v.speakerAnim.page = &TexturePage{
		Frames: make([]*sdl.Texture, 2),
		Delays: []time.Duration{5 * time.Millisecond, 5 * time.Millisecond},
	}
	if due, ok := v.NextAnimDue(scene); !ok || due != minAnimFrameDelay {
		t.Fatalf("sub-floor delays must schedule at the floor: due=%v ok=%v, want %v true", due, ok, minAnimFrameDelay)
	}
	// At-or-above-floor delays pace at their own authored cadence, untouched.
	v.speakerAnim.page.Delays = []time.Duration{80 * time.Millisecond, 80 * time.Millisecond}
	if due, ok := v.NextAnimDue(scene); !ok || due != 80*time.Millisecond {
		t.Fatalf("a normal delay must keep its authored cadence: due=%v ok=%v", due, ok)
	}
}

// TestNextAnimDueColdCrossfade pins the crossfade full-rate gate on a fade
// that is actually ADVANCING: fadeLeft only ticks down while the incoming
// sprite is resident (tickCold — a cold load deliberately doesn't consume the
// blend), so a fade armed toward a permanently-404 sprite is frozen — holding
// full rate for it redrew an unchanging stage forever. The blend still runs
// at full rate once the sprite lands (coldFor resets on residency).
func TestNextAnimDueColdCrossfade(t *testing.T) {
	v := NewViewport(nil)
	scene := &courtroom.Scene{}
	v.crossfade = 200 * time.Millisecond
	v.speakerAnim.fadeLeft = 120 * time.Millisecond
	v.speakerAnim.coldFor = 300 * time.Millisecond // base swapped, sprite never resolved
	if _, ok := v.NextAnimDue(scene); ok {
		t.Fatal("a frozen fade (cold incoming sprite) must not hold full rate")
	}
	v.speakerAnim.coldFor = 0 // the sprite landed: the blend actually advances
	if due, ok := v.NextAnimDue(scene); !ok || due != 0 {
		t.Fatalf("a running crossfade is continuous: due=%v ok=%v, want 0 true", due, ok)
	}
}

// TestNextAnimDueAllocs pins the pacer's clock at zero allocations — it runs
// once per frame on the main thread, so it must never touch the heap.
func TestNextAnimDueAllocs(t *testing.T) {
	v := NewViewport(nil)
	scene := &courtroom.Scene{}
	scene.Speaker.Visible = true
	v.speakerAnim.page = &TexturePage{
		Frames: make([]*sdl.Texture, 3),
		Delays: []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond},
	}
	if n := testing.AllocsPerRun(1000, func() { v.NextAnimDue(scene) }); n != 0 {
		t.Fatalf("NextAnimDue allocates %.1f/op, want 0", n)
	}
}

// threeFramePage builds a 3-frame, 100 ms/frame page by hand (the frame textures
// are never dereferenced by advanceSpeaker), so these tests need no SDL.
func threeFramePage() *TexturePage {
	return &TexturePage{
		Frames: make([]*sdl.Texture, 3),
		Delays: []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond},
	}
}

// TestFinishedOneShotRestartsAsLoop pins the preanim-loop BUG fix and its
// interactions: (a) a one-shot preanim completes and latches finished; (b) when
// the courtroom then tells the SAME base to loop (PlayOnce=false without a base
// change — the collision case), advanceSpeaker restarts it as a clean loop that
// wraps without re-firing completion; (c) a layer that was never a one-shot is
// untouched. Also pins the guarded path at 0 allocations.
func TestFinishedOneShotRestartsAsLoop(t *testing.T) {
	page := threeFramePage()

	// (a) Drive a one-shot to completion. Each 100 ms step advances one frame; the
	// step that lands on the last frame latches finished and reports done ONCE.
	v := NewViewport(nil)
	v.speakerAnim.reset("characters/x/intro") // bind a base so it's a real layer
	done := 0
	for i := 0; i < 3; i++ {
		if v.advanceSpeaker(page, 100*time.Millisecond, true) {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("one-shot reported done %d times, want exactly 1", done)
	}
	if !v.speakerAnim.finished {
		t.Fatal("a completed one-shot must latch finished (last frame held)")
	}
	// A finished one-shot that keeps being told playOnce does nothing and never
	// re-reports (advance's finished guard).
	if v.advanceSpeaker(page, 100*time.Millisecond, true) {
		t.Error("a finished one-shot re-reported completion")
	}

	// (b) The courtroom clears PlayOnce on the SAME base (collision handoff). The
	// guard restarts the layer as a clean loop: finished cleared, frame reset, and
	// advancing now WRAPS instead of freezing — and never fires completion again.
	if v.advanceSpeaker(page, 100*time.Millisecond, false) {
		t.Error("a told-to-loop restart must not fire OnPreanimDone")
	}
	if v.speakerAnim.finished {
		t.Error("told-to-loop must clear the finished latch")
	}
	// The restart call above already consumed one 100 ms step (the guard resets
	// to frame 0, then that same tick's advance steps 0→1 — identical to any
	// post-reset tick; at real frame pacing dt ≪ delay so frame 0 shows fully).
	// Two more steps walk 1→2→wrap back to 0, proving the layer genuinely loops.
	for i := 0; i < 2; i++ {
		if v.advanceSpeaker(page, 100*time.Millisecond, false) {
			t.Fatalf("a loop must never report completion (step %d)", i)
		}
	}
	if v.speakerAnim.frame != 0 {
		t.Errorf("looping layer frame = %d, want it wrapped back to 0", v.speakerAnim.frame)
	}
	if v.speakerAnim.finished {
		t.Error("a looping layer must never latch finished")
	}

	// (c) Control: a layer that was NEVER a one-shot (idle/talk always runs with
	// playOnce=false) is never finished, so the guard never touches it — it just
	// loops. Advancing it many times keeps finished false and never reports done.
	idle := NewViewport(nil)
	idle.speakerAnim.reset("characters/x/(a)normal")
	for i := 0; i < 12; i++ {
		if idle.advanceSpeaker(page, 100*time.Millisecond, false) {
			t.Fatalf("a never-one-shot layer reported completion (step %d)", i)
		}
		if idle.speakerAnim.finished {
			t.Fatalf("a never-one-shot layer latched finished (step %d)", i)
		}
	}

	// 0-alloc on the guarded path (the collision restart + a loop step), matching
	// the render loop's zero-allocation contract.
	guarded := NewViewport(nil)
	guarded.speakerAnim.reset("characters/x/intro")
	guarded.speakerAnim.finished = true // pre-latch: exercise the restart branch every run
	if n := testing.AllocsPerRun(1000, func() {
		guarded.speakerAnim.finished = true
		guarded.advanceSpeaker(page, 100*time.Millisecond, false)
	}); n != 0 {
		t.Fatalf("told-to-loop restart allocates %.1f/op, want 0", n)
	}
}

// TestPreanimLoopFiresDoneOnce pins the opt-in "loop preanimations" pref: with it
// ON, a preanim keeps WRAPPING while it stays the active one-shot layer, but
// OnPreanimDone fires EXACTLY ONCE at the first completion (so the message
// lifecycle is byte-identical to loop-off), and the finished latch never sticks
// so NextAnimDue keeps scheduling the wraps.
func TestPreanimLoopFiresDoneOnce(t *testing.T) {
	page := threeFramePage()
	v := NewViewport(nil)
	v.SetPreanimLoop(true)
	v.speakerAnim.reset("characters/x/intro")

	done := 0
	// Run well past one full clip (3 frames) so it would loop several times.
	for i := 0; i < 12; i++ {
		if v.advanceSpeaker(page, 100*time.Millisecond, true) {
			done++
		}
		if v.speakerAnim.finished {
			t.Fatalf("loop-on preanim must not leave finished latched (step %d) — it would stall NextAnimDue", i)
		}
	}
	if done != 1 {
		t.Fatalf("loop-on preanim fired OnPreanimDone %d times, want exactly 1", done)
	}
	if !v.speakerAnim.loopReported {
		t.Error("the first completion must latch loopReported")
	}

	// A collided-base handoff with loop ON (courtroom clears PlayOnce, same base)
	// must still keep the layer looping cleanly, never re-firing completion.
	for i := 0; i < 6; i++ {
		if v.advanceSpeaker(page, 100*time.Millisecond, false) {
			t.Fatalf("a collided handoff with loop ON re-fired completion (step %d)", i)
		}
	}
	if v.speakerAnim.finished {
		t.Error("collided loop-on handoff must not latch finished")
	}
}
