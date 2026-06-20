package ui

import (
	"os"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
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

// TestGifChatboxHeightFitsMessage pins the fix for text clipping off the bottom
// of the WebP/GIF frame: the chatbox must size to FIT the rasterized message,
// not use a fixed fraction of the small capture frame. A multi-line message must
// produce a box tall enough to hold name-row + text (so nothing clips off-frame),
// taller than the old fixed ¼-frame box; a huge message caps at 3/5 of the frame.
func TestGifChatboxHeightFitsMessage(t *testing.T) {
	const vpH int32 = gifExportH // 360, the capture frame height
	const lineH int32 = 22       // a typical chat line height

	// One short line: floored to a consistent panel, still fits the text.
	if h := gifChatboxHeight(lineH, vpH); h < gifChatNameRowH+lineH || h < vpH/5 {
		t.Errorf("one-line box = %d, want >= name+text (%d) and >= floor (%d)", h, gifChatNameRowH+lineH, vpH/5)
	}

	// Four lines: the box must hold name-row + all four lines (no off-frame clip),
	// and be taller than the old fixed quarter-frame box that caused the bug.
	four := int32(4 * lineH)
	h4 := gifChatboxHeight(four, vpH)
	if h4 < gifChatNameRowH+four {
		t.Errorf("4-line box = %d, too short for name+text %d — message would clip off the frame", h4, gifChatNameRowH+four)
	}
	if oldQuarter := vpH / 4; h4 <= oldQuarter {
		t.Errorf("4-line box = %d, not taller than the old fixed quarter-frame box %d (the clipping bug)", h4, oldQuarter)
	}

	// A pathologically long message caps at 3/5 of the frame (then clips inside the
	// box, never swallowing the whole picture).
	if h := gifChatboxHeight(int32(40*lineH), vpH); h != vpH*3/5 {
		t.Errorf("huge-message box = %d, want the %d cap", h, vpH*3/5)
	}
}

// TestExportMaxFramesBoundsMemory pins the size feature's memory guard: a bigger
// export must keep the GIF paletted-frame budget by capping at proportionally
// fewer frames, never exceed the absolute cap, never drop below the floor, and
// never divide by zero on a degenerate size.
func TestExportMaxFramesBoundsMemory(t *testing.T) {
	if n := exportMaxFrames(gifExportW, gifExportH); n != maxGifFrames {
		t.Errorf("default size frames = %d, want the cap %d", n, maxGifFrames)
	}
	for _, h := range []int32{288, 360, 480, 540, 600, 720} {
		w := h * 4 / 3
		n := exportMaxFrames(w, h)
		if n < minExportFrames {
			t.Errorf("%dx%d frames = %d, below floor %d", w, h, n, minExportFrames)
		}
		if n > maxGifFrames {
			t.Errorf("%dx%d frames = %d, above cap %d", w, h, n, maxGifFrames)
		}
		// Budget respected whenever the floor didn't kick in.
		if n > minExportFrames && n*int(w)*int(h) > gifFrameBudgetBytes {
			t.Errorf("%dx%d: %d frames × %d px exceeds budget %d", w, h, n, int(w)*int(h), gifFrameBudgetBytes)
		}
	}
	if n := exportMaxFrames(0, 0); n != maxGifFrames {
		t.Errorf("zero size = %d, want the guard %d", n, maxGifFrames)
	}
}

// TestExportChatPctFitsAndClamps pins the chatbox-font fix: the export text size
// is derived from the CAPTURE height (not the live chat zoom), scales up with the
// frame and the user's TextScale, and stays within the chat-scale clamp — so long
// lines fit the small frame instead of overflowing.
func TestExportChatPctFitsAndClamps(t *testing.T) {
	// Bigger frame → bigger font (until the clamp); same chars-per-line.
	if a, b := exportChatPct(288, 100), exportChatPct(540, 100); a >= b {
		t.Errorf("font didn't grow with size: 288px=%d, 540px=%d", a, b)
	}
	// Higher TextScale → bigger; lower → smaller.
	if lo, hi := exportChatPct(360, 50), exportChatPct(360, 150); lo >= hi {
		t.Errorf("TextScale not honoured: 50%%=%d, 150%%=%d", lo, hi)
	}
	// Always within the chat-scale clamp, even at extremes.
	for _, c := range []struct {
		h  int32
		ts int
	}{{240, 50}, {360, 100}, {720, 200}, {360, 0}} {
		if p := exportChatPct(c.h, c.ts); p < config.MinChatScalePercent || p > config.MaxChatScalePercent {
			t.Errorf("exportChatPct(%d,%d)=%d out of [%d,%d]", c.h, c.ts, p, config.MinChatScalePercent, config.MaxChatScalePercent)
		}
	}
	// A 360px export at 100% is far smaller than a maxed-out live chat zoom (250%)
	// — which is the overflow bug it fixes.
	if exportChatPct(360, 100) >= config.MaxChatScalePercent {
		t.Errorf("default export text = %d, expected well below the %d live-zoom max", exportChatPct(360, 100), config.MaxChatScalePercent)
	}
}
