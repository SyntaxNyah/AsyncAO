package ui

import (
	"os"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// newCaptureHarness spins SDL (dummy video) + ttf + a software renderer for the
// offscreen-capture tests. Skips (not fails) when SDL/ttf is unavailable.
func newCaptureHarness(t *testing.T) (*sdl.Renderer, func()) {
	t.Helper()
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	if err := ttf.Init(); err != nil {
		sdl.Quit()
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	win, err := sdl.CreateWindow("giftest", 0, 0, 320, 240, sdl.WINDOW_HIDDEN)
	if err != nil {
		ttf.Quit()
		sdl.Quit()
		t.Skipf("window unavailable: %v", err)
	}
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		win.Destroy()
		ttf.Quit()
		sdl.Quit()
		t.Skipf("software renderer unavailable: %v", err)
	}
	return ren, func() {
		ren.Destroy()
		win.Destroy()
		ttf.Quit()
		sdl.Quit()
	}
}

// TestGifChatboxCompositesAndReveals is the load-bearing headless proof for the
// "GIF shows people talking" fix: the conversation text must (1) actually render
// INTO the offscreen capture target the GIF is built from — not just to the
// screen — and (2) reveal more of itself as the typewriter advances, so the GIF
// animates instead of showing a static stage. It exercises the real render path
// (Rasterize + MessageRaster.Draw into a render.CaptureTarget); the full chatbox
// layout (drawGifChatbox) is thin drawing on top of this primitive.
func TestGifChatboxCompositesAndReveals(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	font, err := loadEmbeddedFont(UIFontSize)
	if err != nil {
		t.Skipf("embedded font unavailable: %v", err)
	}
	defer font.Close()

	ct, err := render.NewCaptureTarget(ren, 240, 64)
	if err != nil {
		t.Skipf("render targets unavailable: %v", err)
	}
	defer ct.Close()

	raster, err := render.Rasterize(ren, font, "The witness saw everything that night.", 224, sdl.Color{R: 240, G: 240, B: 240, A: 255})
	if err != nil {
		t.Fatalf("rasterize: %v", err)
	}
	defer raster.Destroy()
	total := raster.TotalRunes()
	if total < 10 {
		t.Fatalf("message rasterized to %d runes, want the whole line", total)
	}

	drawAt := func(visible int) func(dst sdl.Rect) {
		return func(dst sdl.Rect) {
			_ = ren.SetDrawColor(18, 18, 26, 255) // dark chatbox fill
			_ = ren.FillRect(&dst)
			raster.Draw(ren, visible, dst.X+6, dst.Y+6)
		}
	}

	// Count near-white (text) pixels — the dark fill is excluded by the threshold.
	countText := func(visible int) int {
		img, err := ct.Capture(ren, drawAt(visible))
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		n := 0
		for i := 0; i+3 < len(img.Pix); i += 4 {
			if img.Pix[i] > 150 && img.Pix[i+1] > 150 && img.Pix[i+2] > 150 {
				n++
			}
		}
		return n
	}

	none := countText(0)
	few := countText(3)
	all := countText(total)

	if none != 0 {
		t.Errorf("0 runes revealed but found %d text pixels — the dark fill leaked into the count", none)
	}
	if few == 0 {
		t.Error("3 runes revealed but no text pixels in the capture — the chatbox text never reached the offscreen target")
	}
	if all <= few {
		t.Errorf("full message has %d text pixels, not more than the 3-rune %d — the GIF wouldn't animate as the line types out", all, few)
	}
}
