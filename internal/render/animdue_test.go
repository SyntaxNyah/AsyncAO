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

	// An overdue frame (elapsed past its delay) is due NOW, never negative.
	v.bgAnim.elapsed = 80 * time.Millisecond
	if due, ok = v.NextAnimDue(scene); !ok || due != 0 {
		t.Fatalf("overdue flip must clamp to 0: due=%v ok=%v", due, ok)
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
