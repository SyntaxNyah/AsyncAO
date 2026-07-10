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
