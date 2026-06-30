package render

import (
	"fmt"
	"image"
	"os"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// newHeadlessRenderer spins SDL with the dummy video driver and a software
// renderer — texture and draw calls work without a GPU or a display.
func newHeadlessRenderer(t testing.TB) (*sdl.Renderer, func()) {
	t.Helper()
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	window, err := sdl.CreateWindow("bench", 0, 0, 640, 480, sdl.WINDOW_HIDDEN)
	if err != nil {
		sdl.Quit()
		t.Skipf("window unavailable: %v", err)
	}
	ren, err := sdl.CreateRenderer(window, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		window.Destroy()
		sdl.Quit()
		t.Skipf("software renderer unavailable: %v", err)
	}
	return ren, func() {
		ren.Destroy()
		window.Destroy()
		sdl.Quit()
	}
}

// decodedFixture builds a fake decoded sprite (2 frames, 64×64).
func decodedFixture() *assets.Decoded {
	const w, h, frames = 64, 64, 2
	d := &assets.Decoded{Animated: true, Width: w, Height: h}
	for i := 0; i < frames; i++ {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for p := 0; p < len(img.Pix); p += 4 {
			img.Pix[p] = byte(80 * (i + 1))
			img.Pix[p+3] = 0xFF
		}
		d.Frames = append(d.Frames, img)
		d.Delays = append(d.Delays, 50*time.Millisecond)
	}
	return d
}

// TestPinnedPagesSurviveEvictionPressure pins the theme-chrome tier: a
// pinned page must outlive any amount of LRU churn (the black-flashing
// backdrop bug), replace in place, and leave on Remove.
func TestPinnedPagesSurviveEvictionPressure(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	if err := store.UploadPinned("theme://courtroombackground", decodedFixture()); err != nil {
		t.Fatalf("pinned upload: %v", err)
	}
	// Churn the LRU well past any plausible recency window.
	for i := 0; i < 200; i++ {
		if err := store.Upload(fmt.Sprintf("base/churn-%d", i), decodedFixture()); err != nil {
			t.Fatalf("churn upload %d: %v", i, err)
		}
		store.DrainDestroyQueue()
	}
	if _, ok := store.Get("theme://courtroombackground"); !ok {
		t.Fatal("pinned page evicted under LRU churn")
	}

	// Replacement keeps exactly one resident page and bumps the generation.
	genBefore := store.Generation()
	if err := store.UploadPinned("theme://courtroombackground", decodedFixture()); err != nil {
		t.Fatalf("pinned replace: %v", err)
	}
	if store.Generation() == genBefore {
		t.Error("pinned replace did not bump the generation")
	}
	if page, ok := store.Get("theme://courtroombackground"); !ok || len(page.Frames) != 2 {
		t.Error("pinned page broken after replace")
	}

	store.Remove("theme://courtroombackground")
	if _, ok := store.Get("theme://courtroombackground"); ok {
		t.Error("pinned page survived Remove")
	}
}

func TestTextureStoreUploadAndEvict(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	if err := store.Upload("base/a", decodedFixture()); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !store.Contains("base/a") {
		t.Error("texture missing after upload")
	}
	page, ok := store.Get("base/a")
	if !ok || len(page.Frames) != 2 || page.W != 64 {
		t.Fatalf("page = %+v ok=%v", page, ok)
	}
	store.t1.Remove("base/a")
	store.DrainDestroyQueue()
	if store.Contains("base/a") {
		t.Error("texture survived eviction")
	}
}

// benchScene assembles a paired scene with every layer active.
func benchScene(store *TextureStore) *courtroom.Scene {
	sc := &courtroom.Scene{
		BackgroundBase: "bg",
		DeskBase:       "desk",
		ShowDesk:       true,
		PairActive:     true,
		SpeakerInFront: true,
		Speaker: courtroom.SpriteLayer{
			IdleBase: "spk", TalkBase: "spk", Active: "spk",
			Visible: true, OffsetX: 5, OffsetY: -5,
		},
		Pair: courtroom.SpriteLayer{
			IdleBase: "pair", Active: "pair",
			Visible: true, Flip: true, OffsetX: 20,
		},
	}
	return sc
}

// BenchmarkRenderFrame is the §15 gate: a full paired-scene render pass must
// stay far under 16 ms with ZERO heap allocations in steady state.
func BenchmarkRenderFrame(b *testing.B) {
	ren, cleanup := newHeadlessRenderer(b)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Purge()
	for _, base := range []string{"bg", "desk", "spk", "pair"} {
		if err := store.Upload(base, decodedFixture()); err != nil {
			b.Fatal(err)
		}
	}
	vp := NewViewport(store)
	scene := benchScene(store)
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}
	const frameDt = 16 * time.Millisecond

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vp.Update(scene, frameDt)
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		vp.Render(ren, scene, rect)
	}
}

// TestRenderFrameZeroAllocs enforces the alloc gate in plain go test.
// TestPerRuneStyles pins the span→per-rune-style flattening the styled raster
// builds on (pure; no SDL), including bold/italic. A short partition repeats
// the last span's style.
func TestPerRuneStyles(t *testing.T) {
	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	red := sdl.Color{R: 200, G: 0, B: 0, A: 255}
	got := perRuneStyles([]rune("abcdef"), []ColorSpan{
		{Len: 2, Color: white},
		{Len: 4, Color: red, Bold: true},
	})
	for i, s := range got {
		want := spanStyle{color: white}
		if i >= 2 {
			want = spanStyle{color: red, bold: true}
		}
		if s != want {
			t.Errorf("rune %d style = %v, want %v", i, s, want)
		}
	}
	// Partition shorter than the text: the tail repeats the last span's style.
	short := perRuneStyles([]rune("abcdef"), []ColorSpan{{Len: 2, Color: white, Italic: true}})
	if (short[5] != spanStyle{color: white, italic: true}) {
		t.Errorf("tail style = %v, want last span", short[5])
	}
}

func TestRenderFrameZeroAllocs(t *testing.T) {
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
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("render frame allocates %.1f objects/op, want 0 (spec §12)", allocs)
	}
}

// TestRenderFrameRainbowZeroAllocs enforces the alloc gate on the sprite-FX ON
// path: the default frame test runs with every wash OFF, so it can't catch a
// regression in the SetColorMod / SetBlendMode / hue code. It drives the most
// expensive combinations — rainbow + additive glow + pair desync, and the solid
// wash + glow — and every one must still allocate nothing (spec §12).
func TestRenderFrameRainbowZeroAllocs(t *testing.T) {
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
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	for _, fx := range []SpriteFX{
		// Everything on at once: rainbow + glow + desync + per-char hue + the
		// motion effects (wobble + spin) — the worst case for the hot path.
		{Rainbow: true, Glow: true, PairDesync: true, PerCharHue: true, Wobble: true, Spin: true, Speed: 80, Vividness: 90},
		{Solid: true, Glow: true, Wobble: true, Spin: true, SolidR: 255, SolidG: 64, SolidB: 200},
	} {
		vp.SetSpriteFX(fx)
		allocs := testing.AllocsPerRun(200, func() {
			vp.Update(scene, 16*time.Millisecond)
			vp.Render(ren, scene, rect)
		})
		if allocs != 0 {
			t.Errorf("sprite-FX %+v render frame allocates %.1f objects/op, want 0 (spec §12)", fx, allocs)
		}
	}
}

// TestTransmittedSpriteStyleZeroAlloc pins the #103 hard constraint: a RECEIVED
// per-layer sprite style — recolour + opacity + glow + motion on BOTH the speaker
// and the pair, the worst case — renders with zero heap allocations, so a styled
// speaker can never degrade the render hot path.
func TestTransmittedSpriteStyleZeroAlloc(t *testing.T) {
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
	scene.Speaker.Style = courtroom.SpriteStyle{Tint: true, R: 255, G: 60, B: 60, Opacity: 60, Glow: true, Wobble: true, Spin: true, Motion: courtroom.MotionDrift, Brightness: 70, Scale: 130, Rotation: 64, FlipH: true}
	scene.Pair.Style = courtroom.SpriteStyle{HueCycle: true, Opacity: 80, Glow: true, Scale: 80, Rotation: 200}
	rect := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}
	allocs := testing.AllocsPerRun(200, func() {
		vp.Update(scene, 16*time.Millisecond)
		vp.Render(ren, scene, rect)
	})
	if allocs != 0 {
		t.Errorf("transmitted sprite style render allocates %.1f objects/op, want 0 (#103 zero-perf constraint)", allocs)
	}
}

// TestSpriteFXHelpers pins the motion / per-character FX helpers: bounded,
// deterministic, allocation-free pure math.
func TestSpriteFXHelpers(t *testing.T) {
	cycle := cycleForSpeed(50)
	// charHueOffset: deterministic and bounded to [0,cycle); distinct names
	// land on distinct offsets (so per-character actually differs).
	off := charHueOffset("phoenix", cycle)
	if off < 0 || off >= cycle {
		t.Errorf("charHueOffset = %v, want [0,%v)", off, cycle)
	}
	if charHueOffset("phoenix", cycle) != off {
		t.Error("charHueOffset must be deterministic for the same name")
	}
	if charHueOffset("miles", cycle) == off {
		t.Error("distinct names should land on distinct hues")
	}
	if charHueOffset("x", 0) != 0 {
		t.Error("charHueOffset with cycle 0 must be 0 (guard)")
	}
	// spinDegrees stays within [0,360) across and beyond one period.
	for _, d := range []time.Duration{0, spinPeriod / 4, spinPeriod - 1, 5 * spinPeriod} {
		if deg := spinDegrees(d); deg < 0 || deg >= 360 {
			t.Errorf("spinDegrees(%v) = %v, want [0,360)", d, deg)
		}
	}
	// wobbleOffset never exceeds the amplitude band (vp.W / divisor).
	vp := sdl.Rect{W: 800, H: 600}
	maxAmp := int32(800/wobbleAmpDivisor) + 1
	for _, d := range []time.Duration{0, wobblePeriod / 3, wobblePeriod, 7 * wobblePeriod / 2} {
		dx, dy := wobbleOffset(d, vp)
		if dx < -maxAmp || dx > maxAmp || dy < -maxAmp || dy > maxAmp {
			t.Errorf("wobbleOffset(%v) = %d,%d exceeds amplitude %d", d, dx, dy, maxAmp)
		}
	}
}

// TestRainbowMod pins the hue colour-mod invariants: every channel stays in
// [floor,255] across a full cycle (so the wash tints rather than silhouettes
// and never wraps a uint8), the hue actually moves, and — critically — the
// cycle period is always > 0 so the frame loop's phase%cycle / divide can never
// panic, whatever the speed slider says.
func TestRainbowMod(t *testing.T) {
	const steps = 600
	cycle := cycleForSpeed(50)
	floor := floorForVividness(60)
	moved := false
	r0, g0, b0 := rainbowMod(0, cycle, floor)
	for i := 0; i <= steps; i++ {
		phase := time.Duration(int64(cycle) * int64(i) / int64(steps))
		r, g, b := rainbowMod(phase, cycle, floor)
		for _, c := range []uint8{r, g, b} {
			if int(c) < floor {
				t.Fatalf("phase %v channel %d below floor %d", phase, c, floor)
			}
		}
		if r != r0 || g != g0 || b != b0 {
			moved = true
		}
	}
	if !moved {
		t.Fatal("rainbowMod never varied across a full cycle")
	}
	// cycleForSpeed must stay strictly positive for every in-range and
	// out-of-range slider value — a zero period would panic the render loop.
	for _, s := range []int{-99, 0, 1, 50, 100, 9999} {
		if cycleForSpeed(s) <= 0 {
			t.Fatalf("cycleForSpeed(%d) = %v, want > 0", s, cycleForSpeed(s))
		}
	}
	// Defensive: a zero/negative cycle must not panic — it's clamped to
	// minRainbowCycle, so the result is identical to passing that period.
	gr, gg, gb := rainbowMod(123, 0, 0)
	er, eg, eb := rainbowMod(123, minRainbowCycle, 0)
	if gr != er || gg != eg || gb != eb {
		t.Fatalf("rainbowMod(zero cycle) = %d,%d,%d, want clamp to minRainbowCycle %d,%d,%d", gr, gg, gb, er, eg, eb)
	}
}

// TestViewportStickyScenery pins the black-background fix: flipping the
// scene to a not-yet-resident background keeps the previous scenery bound
// (and drawn) until the new texture lands; an empty base clears at once.
func TestViewportStickyScenery(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	if err := store.Upload("bg/defenseempty", decodedFixture()); err != nil {
		t.Fatal(err)
	}

	vp := NewViewport(store)
	scene := &courtroom.Scene{BackgroundBase: "bg/defenseempty"}
	vp.Update(scene, time.Millisecond)
	if vp.bgAnim.base != "bg/defenseempty" {
		t.Fatalf("bound %q, want defenseempty", vp.bgAnim.base)
	}

	// Speaker flips position; the witness background hasn't loaded yet.
	scene.BackgroundBase = "bg/witnessempty"
	vp.Update(scene, time.Millisecond)
	if vp.bgAnim.base != "bg/defenseempty" {
		t.Fatalf("sticky bind lost: bound %q, want defenseempty until witnessempty is resident", vp.bgAnim.base)
	}

	// The texture lands; the next Update swaps to it.
	if err := store.Upload("bg/witnessempty", decodedFixture()); err != nil {
		t.Fatal(err)
	}
	vp.Update(scene, time.Millisecond)
	if vp.bgAnim.base != "bg/witnessempty" {
		t.Fatalf("bound %q after upload, want witnessempty", vp.bgAnim.base)
	}

	// An explicit empty base clears immediately.
	scene.BackgroundBase = ""
	vp.Update(scene, time.Millisecond)
	if vp.bgAnim.base != "" {
		t.Fatalf("bound %q, want cleared", vp.bgAnim.base)
	}
}
