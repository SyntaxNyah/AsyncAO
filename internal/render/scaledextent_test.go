package render

import (
	"testing"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// TestDrawScaledCoversSameExtentAsDraw is the A/B for the reported regression:
// the IC chatbox is fine at 100% UI scale and loses the bottom of the message at
// 125%. 100% is exactly where deviceExact() returns false (the pre-fix scaled
// path) and 125% is where it returns true (the device-exact blit added in
// v1.87.0), so the two paths are what differ.
//
// Same raster, same origin, same clip: Draw and DrawScaled must ink the same
// physical rows. If DrawScaled stops short, the device-exact blit is losing
// lines and that is the bug.
func TestDrawScaledCoversSameExtentAsDraw(t *testing.T) {
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL video unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("ttf unavailable: %v", err)
	}
	defer ttf.Quit()

	const w, h = 600, 600
	win, err := sdl.CreateWindow("x", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED, w, h, sdl.WINDOW_HIDDEN)
	if err != nil {
		t.Skipf("window: %v", err)
	}
	defer win.Destroy()
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		t.Skipf("renderer: %v", err)
	}
	defer ren.Destroy()

	const pct = 125
	font := openProbeFontAt(t, 14*pct/100) // the DEVICE face, as renderRaster uses
	defer font.Close()

	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	const text = "one two three four five six seven eight nine ten eleven twelve thirteen"
	const wrapLogical = 160 // logical px; Rasterize scales it to device itself

	lastRow := func(exact bool) int {
		m, err := Rasterize(ren, font, text, wrapLogical, white, pct)
		if err != nil {
			t.Fatalf("Rasterize: %v", err)
		}
		defer m.Destroy()
		if m.Lines() < 3 {
			t.Fatalf("need a multi-line message to see the cut; got %d lines", m.Lines())
		}
		_ = ren.SetScale(1, 1)
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		_ = ren.SetScale(float32(pct)/100, float32(pct)/100)
		if exact {
			m.DrawScaled(ren, m.TotalRunes(), 4, 4, pct, nil)
		} else {
			m.Draw(ren, m.TotalRunes(), 4, 4)
		}
		_ = ren.SetScale(1, 1)

		pix := make([]byte, w*h*4)
		if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: w, H: h},
			uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), w*4); err != nil {
			t.Skipf("ReadPixels: %v", err)
		}
		last := -1
		for yy := 0; yy < h; yy++ {
			row := pix[yy*w*4 : (yy+1)*w*4]
			for xx := 0; xx < w; xx++ {
				if row[xx*4] != 0 || row[xx*4+1] != 0 || row[xx*4+2] != 0 {
					last = yy
					break
				}
			}
		}
		return last
	}

	scaled := lastRow(false) // the pre-v1.87.0 path — what 100% still uses
	exact := lastRow(true)   // the device-exact path — what 125% now uses
	t.Logf("last inked DEVICE row: scaled(Draw)=%d  exact(DrawScaled)=%d", scaled, exact)
	if scaled < 0 || exact < 0 {
		t.Fatal("nothing inked at all — the harness drew no text")
	}
	// A couple of rows of rounding slack is fine; losing a whole line is not.
	if diff := scaled - exact; diff > 4 {
		t.Errorf("the device-exact blit inks %d device rows SHORTER than the scaled path "+
			"(scaled=%d exact=%d). That is the chatbox losing the bottom of the message at a "+
			"scaled UI while 100%% — which never takes this path — stays correct.",
			diff, scaled, exact)
	}
}

func openProbeFontAt(t *testing.T, size int) *ttf.Font {
	t.Helper()
	for _, p := range []string{`C:\Windows\Fonts\segoeui.ttf`, `C:\Windows\Fonts\arial.ttf`} {
		if f, err := ttf.OpenFont(p, size); err == nil {
			return f
		}
	}
	t.Skip("no probe font available")
	return nil
}
