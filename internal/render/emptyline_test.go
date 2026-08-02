package render

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

// TestRasterizeSurvivesEmptyLines is the regression test for a chatbox that goes
// completely blank while the showname above it still draws.
//
// Those two draw in the same box under the same clip, so the only way to get one
// without the other is msRaster == nil — and ensureChatRaster leaves it nil, and
// never records the attempt, whenever Rasterize returns an error. It then retries
// and fails identically on every frame, so the message never appears at all.
//
// wrapStyled appends {lineStart, n} unconditionally after its loop, so a message
// that ENDS in a newline (or contains two in a row) produces a zero-length line
// range. SDL_ttf refuses to render an empty string, so one blank line in an
// otherwise ordinary message takes the whole message down.
func TestRasterizeSurvivesEmptyLines(t *testing.T) {
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL video unavailable: %v", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		t.Skipf("ttf unavailable: %v", err)
	}
	defer ttf.Quit()

	win, err := sdl.CreateWindow("r", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED, 320, 240, sdl.WINDOW_HIDDEN)
	if err != nil {
		t.Skipf("window: %v", err)
	}
	defer win.Destroy()
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		t.Skipf("renderer: %v", err)
	}
	defer ren.Destroy()

	font := openProbeFont(t)
	defer font.Close()

	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	cases := []struct {
		name, text string
	}{
		{"trailing newline", "hello world\n"},
		{"doubled newline", "hello\n\nworld"},
		{"leading newline", "\nhello world"},
		{"only a newline", "\n"},
		{"long unbroken run then newline", strings.Repeat("A", 240) + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Rasterize(ren, font, tc.text, 200, white, DefaultDevScale)
			if err != nil {
				t.Fatalf("Rasterize(%q) failed: %v\n"+
					"A message with a blank line must still rasterize — ensureChatRaster drops the "+
					"whole raster on error, so the chatbox renders NOTHING (showname still draws) "+
					"and retries forever.", tc.text, err)
			}
			if m == nil {
				t.Fatalf("Rasterize(%q) returned a nil raster with no error", tc.text)
			}
			defer m.Destroy()
			// Not asserting a line count: a message that is ONLY a newline has
			// nothing to draw, and zero lines is the honest answer there
			// (chatMsgLines falls back to its own wrap for the box height). What
			// must never happen is the ERROR above — that is what leaves the
			// chatbox permanently blank under a showname that still draws.
			if tc.text != "\n" && m.Lines() == 0 {
				t.Errorf("Rasterize(%q) produced no lines", tc.text)
			}
		})
	}
}

func openProbeFont(t *testing.T) *ttf.Font {
	t.Helper()
	for _, p := range []string{`C:\Windows\Fonts\segoeui.ttf`, `C:\Windows\Fonts\arial.ttf`} {
		if f, err := ttf.OpenFont(p, 14); err == nil {
			return f
		}
	}
	t.Skip("no probe font available")
	return nil
}
