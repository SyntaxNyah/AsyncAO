package render

import (
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
		t.Errorf("render frame allocates %.1f objects/op, want 0 (PROMPT.md §12)", allocs)
	}
}
