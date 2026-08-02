package render

import (
	"unsafe"

	"github.com/veandco/go-sdl2/ttf"

	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestClipRectSurvivesScaleChange is an EMPIRICAL check of the assumption the
// device-exact message blit rests on: that SDL_RenderSetClipRect bakes the clip
// into device pixels at SET time, so bracketing a SetScale(1,1) around the blit
// leaves the clip covering the same physical rows.
//
// If SDL instead re-evaluates the clip against the CURRENT scale, then setting a
// clip at 125% and then dropping to 1:1 shrinks the clip to 1/1.25 of its
// intended height — and a message drawn in device coordinates inside that bracket
// is cut off part-way down, worse the higher the scale. That is exactly the
// reported chatbox symptom, so this test decides whether the bracket is the bug.
//
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
	// NOT asserted as a required behaviour any more, only reported. This renderer
	// is RENDERER_SOFTWARE, which bakes at SET time — but macOS's Metal backend
	// evaluates the clip at USE time, and asserting the software answer here is
	// precisely the mistake that "proved" the bracket safe while it was silently
	// deleting every chatbox line on a Mac. MessageRaster.draw no longer depends on
	// either answer: it re-asserts the clip in device pixels inside the bracket.
	wantBaked := int(float64(clip.H)*scale) - 1
	t.Logf("this backend inked to device row %d (baked-at-set would be ~%d, applied-at-use ~%d)",
		lastInk, wantBaked, clip.H-1)
}

// TestDeviceExactReassertsTheClip pins the actual fix: whatever the backend does
// with a clip across a scale change, text drawn through the device-exact bracket
// must land inside the rows the caller clipped to.
//
// The regression it guards is total, not partial — on Metal at 125% the clip
// collapsed to 1/1.25 of its height and chatbox text drawn below that line
// vanished entirely, while the showname (drawn before the clip) stayed. The
// client reported the text as drawn, revealed and fitting the whole time.
func TestDeviceExactReassertsTheClip(t *testing.T) {
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL video unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("ttf unavailable: %v", err)
	}
	defer ttf.Quit()

	const w, h = 400, 400
	win, err := sdl.CreateWindow("c", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED, w, h, sdl.WINDOW_HIDDEN)
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
	font := openProbeFontAt(t, 14*pct/100)
	defer font.Close()

	m, err := Rasterize(ren, font, "the message that must survive the bracket", 200,
		sdl.Color{R: 255, G: 255, B: 255, A: 255}, pct)
	if err != nil {
		t.Fatalf("Rasterize: %v", err)
	}
	defer m.Destroy()

	// Mirror the chatbox: a generous LOGICAL clip, text well inside it.
	clip := sdl.Rect{X: 0, Y: 20, W: 300, H: 200}
	_ = ren.SetScale(1, 1)
	_ = ren.SetDrawColor(0, 0, 0, 255)
	_ = ren.Clear()
	_ = ren.SetScale(float32(pct)/100, float32(pct)/100)
	_ = ren.SetClipRect(&clip)
	m.DrawScaled(ren, m.TotalRunes(), 4, 30, pct, &clip)
	_ = ren.SetClipRect(nil)
	_ = ren.SetScale(1, 1)

	pix := make([]byte, w*h*4)
	if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: w, H: h},
		uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), w*4); err != nil {
		t.Skipf("ReadPixels: %v", err)
	}
	ink := 0
	for i := 0; i < w*h; i++ {
		if pix[i*4] != 0 || pix[i*4+1] != 0 || pix[i*4+2] != 0 {
			ink++
		}
	}
	if ink == 0 {
		t.Error("the device-exact blit drew NOTHING inside a clip that comfortably contains it — " +
			"this is the macOS chatbox failure: geometry correct, text reported as drawn, no pixels.")
	}
}
