package render

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestIdleBreathZeroAlloc pins #122: the bob + breathing-scale on every sprite renders with
// zero per-frame heap allocations (pure math off the fxClock, folded into the existing scale +
// position). Off → byte-identical (the SpriteFX zero value has IdleBreath false).
func TestIdleBreathZeroAlloc(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	for _, base := range []string{"bg", "desk", "spk", "pair"} {
		if err := store.Upload(base, decodedFixture()); err != nil {
			t.Fatal(err)
		}
	}
	vp := NewViewport(store)
	scene := benchScene(store)
	vp.SetSpriteFX(SpriteFX{IdleBreath: true, BreathBob: true, BreathScale: true, BreathAmp: 40, BreathSpeed: 50})
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("idle-breath render allocates %.1f objects/op, want 0 (#122 zero-perf constraint)", allocs)
	}
}

// TestBreathPeriod pins the speed→period mapping: slowest at speed 1, fastest at 100, clamped.
func TestBreathPeriod(t *testing.T) {
	if got := breathPeriod(1); got != breathPeriodMax {
		t.Errorf("speed 1 → %v, want the slow max %v", got, breathPeriodMax)
	}
	if got := breathPeriod(100); got != breathPeriodMin {
		t.Errorf("speed 100 → %v, want the fast min %v", got, breathPeriodMin)
	}
	if got := breathPeriod(0); got != breathPeriodMax { // under-range clamps to slowest
		t.Errorf("speed 0 → %v, want %v", got, breathPeriodMax)
	}
	if breathPeriod(50) <= breathPeriodMin || breathPeriod(50) >= breathPeriodMax {
		t.Error("mid speed should sit strictly between the bounds")
	}
}

// TestBreathTransform pins the offsets: at a zero-sine phase (clock 0) there's no bob and the
// scale-pulse is at its low point; the amplitude scales both and is clamped.
func TestBreathTransform(t *testing.T) {
	vp := sdl.Rect{W: 512, H: 384}
	// clock 0 → sin 0 → bob 0, scaleAdd at the (1+sin)/2 = 0.5 midpoint of [0,max].
	bob, add := breathTransform(0, vp, 100, 50)
	if bob != 0 {
		t.Errorf("bob at sin=0 = %d, want 0", bob)
	}
	if add < 0 || add > breathScaleMax {
		t.Errorf("scaleAdd = %d, want within [0,%d]", add, breathScaleMax)
	}
	// A quarter period later sin=1 → maximum bob and scaleAdd at amp 100.
	q := breathPeriod(50) / 4
	bob2, add2 := breathTransform(q, vp, 100, 50)
	if bob2 <= 0 {
		t.Errorf("bob at sin=1 = %d, want positive", bob2)
	}
	if add2 != breathScaleMax {
		t.Errorf("scaleAdd at sin=1, amp 100 = %d, want %d", add2, breathScaleMax)
	}
	// Zero amplitude is clamped to a floor (never a divide/zero-motion surprise) but stays tiny.
	if b, _ := breathTransform(q, vp, 0, 50); b < 0 {
		t.Errorf("amp 0 bob = %d, want non-negative", b)
	}
}
