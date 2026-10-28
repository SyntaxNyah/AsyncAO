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
// Vulkan surface exists, so OpenBackend's device tiers fail the way they do on a
// machine without a driver.
func newHeadlessWindow(t *testing.T) *sdl.Window {
	t.Helper()
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	t.Setenv(vulkanDriverFilesEnv, os.Getenv(vulkanDriverFilesEnv)) // restored after OpenBackend sets it
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

// TestOpenBackendAlwaysDraws pins the last resort: with no GPU driver usable, the
// window still gets a renderer and a backend name to show, rather than an error
// that would stop the client from starting.
func TestOpenBackendAlwaysDraws(t *testing.T) {
	window := newHeadlessWindow(t)
	g, err := OpenBackend(window, BackendOptions{VSync: true})
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	defer g.Close()
	if g.Renderer() == nil {
		t.Fatal("no renderer")
	}
	if g.Name() == "" {
		t.Error("no backend name for the debug overlay and the About screen")
	}
}

// TestOpenBackendForceSoftwareWithoutDriverUsesSDLSoftware pins the force-software
// flag on a build with no software Vulkan driver: it must skip the hardware
// driver and land on SDL's CPU renderer, which has no SDL_GPU device.
func TestOpenBackendForceSoftwareWithoutDriverUsesSDLSoftware(t *testing.T) {
	window := newHeadlessWindow(t)
	g, err := OpenBackend(window, BackendOptions{ForceSoftware: true})
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	defer g.Close()
	if got := g.Name(); got != sdlSoftwareRenderer {
		t.Errorf("backend = %q, want %q", got, sdlSoftwareRenderer)
	}
	if g.Device() != nil {
		t.Error("SDL's software renderer reported an SDL_GPU device")
	}
	if !g.Software() {
		t.Error("SDL's software renderer did not report software — a GPUWatcher would escalate to it again")
	}
}

// TestOpenBackendUnusableSoftwareDriverFallsThrough pins that a software driver
// manifest that cannot load (missing, or a broken install) falls through to SDL's
// renderer instead of failing startup, and that the Vulkan loader setting is put
// back afterwards: left set, it would leak into the client relaunched after a
// device loss.
func TestOpenBackendUnusableSoftwareDriverFallsThrough(t *testing.T) {
	window := newHeadlessWindow(t)
	const userSetting = "user-chosen.json" // a Vulkan setting the user made before launch
	t.Setenv(vulkanDriverFilesEnv, userSetting)
	missing := filepath.Join(t.TempDir(), "lvp_icd.x86_64.json")
	g, err := OpenBackend(window, BackendOptions{ForceSoftware: true, SoftwareDriverFile: missing})
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	defer g.Close()
	if got := os.Getenv(vulkanDriverFilesEnv); got != userSetting {
		t.Errorf("%s = %q after OpenBackend, want the user's %q back", vulkanDriverFilesEnv, got, userSetting)
	}
	if got := g.Name(); got != sdlSoftwareRenderer {
		t.Errorf("backend = %q, want %q", got, sdlSoftwareRenderer)
	}
}
