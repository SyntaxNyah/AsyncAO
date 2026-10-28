package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// sdlSoftwareRenderer is the name SDL gives its CPU renderer.
const sdlSoftwareRenderer = "software"

// newHeadlessWindow opens a hidden window on the dummy video driver, where no
// Vulkan surface exists, so OpenGPU's device tiers fail the way they do on a
// machine without a driver.
func newHeadlessWindow(t *testing.T) *sdl.Window {
	t.Helper()
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	t.Setenv(vulkanDriverFilesEnv, os.Getenv(vulkanDriverFilesEnv)) // restored after OpenGPU sets it
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	window, err := sdl.CreateWindow("gpu", 0, 0, 640, 480, sdl.WINDOW_HIDDEN)
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

// TestOpenGPUAlwaysDraws pins the last resort: with no GPU driver usable, the
// window still gets a renderer and a backend name to show, rather than an error
// that would stop the client from starting.
func TestOpenGPUAlwaysDraws(t *testing.T) {
	window := newHeadlessWindow(t)
	g, err := OpenGPU(window, GPUOptions{VSync: true})
	if err != nil {
		t.Fatalf("OpenGPU: %v", err)
	}
	defer g.Close()
	if g.Renderer() == nil {
		t.Fatal("no renderer")
	}
	if g.Backend() == "" {
		t.Error("no backend name for the debug overlay and the About screen")
	}
}

// TestOpenGPUForceSoftwareWithoutDriverUsesSDLSoftware pins the force-software
// flag on a build with no software Vulkan driver: it must skip the hardware
// driver and land on SDL's CPU renderer, which has no SDL_GPU device.
func TestOpenGPUForceSoftwareWithoutDriverUsesSDLSoftware(t *testing.T) {
	window := newHeadlessWindow(t)
	g, err := OpenGPU(window, GPUOptions{ForceSoftware: true})
	if err != nil {
		t.Fatalf("OpenGPU: %v", err)
	}
	defer g.Close()
	if got := g.Backend(); got != sdlSoftwareRenderer {
		t.Errorf("backend = %q, want %q", got, sdlSoftwareRenderer)
	}
	if g.HasDevice() {
		t.Error("SDL's software renderer reported an SDL_GPU device")
	}
}

// TestOpenGPUUnusableSoftwareDriverFallsThrough pins that a software driver
// manifest that cannot load (missing, or a broken install) falls through to SDL's
// renderer instead of failing startup, and that the Vulkan loader was pointed at
// it for the attempt.
func TestOpenGPUUnusableSoftwareDriverFallsThrough(t *testing.T) {
	window := newHeadlessWindow(t)
	missing := filepath.Join(t.TempDir(), "lvp_icd.x86_64.json")
	g, err := OpenGPU(window, GPUOptions{ForceSoftware: true, SoftwareDriverFile: missing})
	if err != nil {
		t.Fatalf("OpenGPU: %v", err)
	}
	defer g.Close()
	if got := os.Getenv(vulkanDriverFilesEnv); got != missing {
		t.Errorf("%s = %q, want the software driver manifest %q", vulkanDriverFilesEnv, got, missing)
	}
	if got := g.Backend(); got != sdlSoftwareRenderer {
		t.Errorf("backend = %q, want %q", got, sdlSoftwareRenderer)
	}
}
