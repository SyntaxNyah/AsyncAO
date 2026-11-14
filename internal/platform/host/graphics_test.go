package host

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// headlessWindowW and headlessWindowH are the hidden test window's size.
const (
	headlessWindowW = 640
	headlessWindowH = 480
)

// newHeadlessWindow opens a hidden window on the dummy video driver, which has no GPU renderer, so OpenGraphics's GPU
// tier fails the way it does on a machine without a driver.
func newHeadlessWindow(t *testing.T) *sdl.Window {
	t.Helper()
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	window, err := sdl.CreateWindow("gpu", 0, 0, headlessWindowW, headlessWindowH, sdl.WINDOW_HIDDEN)
	if err != nil {
		sdl.Quit()
		t.Skipf("window unavailable: %v", err)
	}
	t.Cleanup(func() {
		window.Destroy()
		sdl.Quit()
	})
	return window
}

// TestOpenGraphicsAlwaysDraws pins the last resort: with no GPU usable, the window still gets SDL's software renderer,
// reported as software, rather than an error that would stop the client from starting. SDL's own fallback to that
// renderer counts as software too, or the stall check would try to escape a renderer that can't freeze.
func TestOpenGraphicsAlwaysDraws(t *testing.T) {
	for _, force := range []bool{false, true} {
		window := newHeadlessWindow(t)
		g, err := OpenGraphics(window, GraphicsOptions{VSync: true, ForceSoftware: force})
		if err != nil {
			t.Fatalf("force=%v: OpenGraphics: %v", force, err)
		}
		if g.Name != sdl.SoftwareRendererName {
			t.Errorf("force=%v: graphics = %q, want %q", force, g.Name, sdl.SoftwareRendererName)
		}
		if !g.Software {
			t.Errorf("force=%v: SDL's software renderer did not report software — a GPUWatcher would escalate to it again", force)
		}
		_ = g.Renderer.Destroy()
	}
}
