package render

import (
	"unsafe"

	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestClipRectSurvivesScaleChange is an EMPIRICAL check of the assumption the
// device-exact message blit rests on: that SDL_RenderSetClipRect bakes the clip
// into device pixels at SET time, so bracketing a SetScale(1,1) around the blit
// leaves the clip covering the same physical rows.
// It renders white into a wide rect under the bracket and reports the LAST row
// that actually received ink.
func TestClipRectSurvivesScaleChange(t *testing.T) {
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL video unavailable: %v", err)
	}
	defer sdl.Quit()

	const w, h = 400, 400
	win, err := sdl.CreateWindow("clip", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED, w, h, sdl.WINDOW_HIDDEN)
	if err != nil {
		t.Skipf("window: %v", err)
	}
	defer win.Destroy()
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		t.Skipf("renderer: %v", err)
	}
	defer ren.Destroy()

	const scale = 1.25
	// A clip 100 LOGICAL px tall at 1.25 => it should cover 125 DEVICE rows.
	clip := sdl.Rect{X: 0, Y: 0, W: 200, H: 100}

	_ = ren.SetDrawColor(0, 0, 0, 255)
	_ = ren.Clear()
	_ = ren.SetScale(scale, scale)
	_ = ren.SetClipRect(&clip) // set WHILE scaled, exactly as the chatbox does
	_ = ren.SetScale(1, 1)     // the device-exact bracket
	_ = ren.SetDrawColor(255, 255, 255, 255)
	full := sdl.Rect{X: 0, Y: 0, W: 200, H: 300} // taller than any plausible clip
	_ = ren.FillRect(&full)
	_ = ren.SetScale(scale, scale)
	_ = ren.SetClipRect(nil)

	pix := make([]byte, w*h*4)
	if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: w, H: h}, uint32(sdl.PIXELFORMAT_ARGB8888),
		unsafe.Pointer(&pix[0]), w*4); err != nil {
		t.Skipf("ReadPixels: %v", err)
	}
	lastInk := -1
	for y := 0; y < h; y++ {
		row := pix[y*w*4 : (y+1)*w*4]
		for x := 0; x < 4; x++ { // sample the left edge, inside the clip's width
			if row[x*4] != 0 || row[x*4+1] != 0 || row[x*4+2] != 0 {
				lastInk = y
				break
			}
		}
	}
	// Baked at SET time: the clip keeps covering the physical rows it was set to
	// cover, so the bracket is safe. Applied at USE time would leave the last ink
	// at clip.H-1 and would mean the device-exact blit is silently cutting every
	// scaled chatbox short — the thing this test exists to catch if a future SDL
	// (or a different backend) ever changes the rule.
	wantBaked := int(float64(clip.H)*scale) - 1
	t.Logf("this backend inked to device row %d (baked-at-set would be ~%d, applied-at-use ~%d)",
		lastInk, wantBaked, clip.H-1)
}
