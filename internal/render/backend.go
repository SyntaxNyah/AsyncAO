package render

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Backend is how the window is drawn. The composition root opens one (OpenBackend)
// and hands it to what needs it; nothing else knows which kind it is. Each kind is
// its own implementation, so a native backend later is an addition to OpenBackend's
// order, not an edit to the others.
type Backend interface {
	// Renderer is what the compositor and the UI draw with.
	Renderer() *sdl.Renderer
	// Device is the SDL_GPU device the renderer draws through, which the UI library
	// shares; nil when the backend has none.
	Device() *sdl.GPUDevice
	// Present shows the frame. Its error goes to a GPUWatcher.
	Present() error
	// Name says what is drawing, for the log, the debug overlay and the About
	// screen: "vulkan (NVIDIA GeForce RTX 3070)", "vulkan (llvmpipe …)", "direct3d11".
	Name() string
	// Software reports drawing on the CPU (lavapipe or SDL's software renderer).
	Software() bool
	// Close destroys the renderer, then anything under it.
	Close()
}

// vulkanDriverFilesEnv is the Vulkan loader's setting that replaces its driver
// search with the listed driver files (ICD manifests). Pointing it at the bundled
// lavapipe manifest is how the software tier is selected.
const vulkanDriverFilesEnv = "VK_DRIVER_FILES"

// BackendOptions configures how OpenBackend picks the backend. Built by the
// composition root from flags and the build's staged files.
type BackendOptions struct {
	VSync bool
	// ForceSoftware skips the hardware driver, for machines whose GPU driver is
	// broken.
	ForceSoftware bool
	// SoftwareDriverFile is the bundled software Vulkan driver's manifest
	// (lavapipe), or "" when this build ships none.
	SoftwareDriverFile string
}

// OpenBackend opens the first backend that works for window, in order:
//   - an SDL_GPU device on the platform's hardware driver (Vulkan off macOS, so the
//     UI library's SPIR-V shaders load; Metal on macOS);
//   - the same on the bundled software driver, lavapipe. SDL does not fall back by
//     itself: with no Vulkan driver, device creation just fails;
//   - SDL's own renderer, so a build with no software driver staged, or a headless
//     test, still draws. The UI library will need a device; the compositor does not.
func OpenBackend(window *sdl.Window, opts BackendOptions) (Backend, error) {
	flags := uint32(0)
	if opts.VSync {
		flags = sdl.RENDERER_PRESENTVSYNC
	}
	if !opts.ForceSoftware {
		b, err := openDeviceBackend(window, flags, false)
		if err == nil {
			return b, nil
		}
		log.Printf("gpu: hardware %s driver unavailable: %v", gpuDriverLabel, err)
	}
	if opts.SoftwareDriverFile != "" {
		var b *deviceBackend
		err := withEnv(vulkanDriverFilesEnv, opts.SoftwareDriverFile, func() (err error) {
			b, err = openDeviceBackend(window, flags, true)
			return err
		})
		if err == nil {
			return b, nil
		}
		log.Printf("gpu: software driver %s unavailable: %v", opts.SoftwareDriverFile, err)
	}
	return openSDLRendererBackend(window, flags, opts.ForceSoftware)
}

// withEnv runs fn with the environment variable name set to value, then puts the
// variable back as it was. The Vulkan loader reads its driver list when a device is
// created, so the setting is needed only for that call; left set, it would leak into
// every process started later — the client relaunched after a device loss would
// draw on lavapipe while told to use the hardware driver.
func withEnv(name, value string, fn func() error) error {
	prev, had := os.LookupEnv(name)
	if err := os.Setenv(name, value); err != nil {
		return err
	}
	defer func() {
		if had {
			_ = os.Setenv(name, prev)
		} else {
			_ = os.Unsetenv(name)
		}
	}()
	return fn()
}

// rendererBackend is the half every backend shares: a renderer that draws the
// window, its name and whether it is software.
type rendererBackend struct {
	renderer *sdl.Renderer
	name     string
	software bool
}

func (b *rendererBackend) Renderer() *sdl.Renderer { return b.renderer }
func (b *rendererBackend) Present() error          { return b.renderer.Present() }
func (b *rendererBackend) Name() string            { return b.name }
func (b *rendererBackend) Software() bool          { return b.software }

// deviceBackend draws through an SDL_GPU device with SDL's gpu renderer on it, so
// the courtroom compositor and the UI library share one device.
type deviceBackend struct {
	rendererBackend
	device *sdl.GPUDevice
}

var _ Backend = (*deviceBackend)(nil)

// openDeviceBackend creates a device on the platform's driver and a renderer on
// it; software records that the driver is the software one.
func openDeviceBackend(window *sdl.Window, flags uint32, software bool) (*deviceBackend, error) {
	device, err := sdl.CreateGPUDevice(gpuShaderFormats, gpuDriver)
	if err != nil {
		return nil, err
	}
	ren, err := sdl.CreateGPURenderer(device, window, flags)
	if err != nil {
		device.Destroy()
		return nil, err
	}
	name := device.Driver()
	if adapter := device.Name(); adapter != "" {
		name = fmt.Sprintf("%s (%s)", name, adapter)
	}
	return &deviceBackend{rendererBackend: rendererBackend{renderer: ren, name: name, software: software}, device: device}, nil
}

func (b *deviceBackend) Device() *sdl.GPUDevice { return b.device }

func (b *deviceBackend) Close() {
	_ = b.renderer.Destroy()
	b.device.Destroy()
}

// sdlRendererBackend is SDL's own renderer choice (Direct3D 11, Metal, OpenGL,
// or its software renderer), with no SDL_GPU device.
type sdlRendererBackend struct {
	rendererBackend
}

var _ Backend = (*sdlRendererBackend)(nil)

// openSDLRendererBackend opens SDL's default renderer, or its software renderer
// when that fails or software was forced.
func openSDLRendererBackend(window *sdl.Window, flags uint32, software bool) (*sdlRendererBackend, error) {
	var errs []error
	if !software {
		ren, err := sdl.CreateRenderer(window, -1, flags)
		if err == nil {
			return newSDLRendererBackend(ren, false), nil
		}
		errs = append(errs, err)
	}
	ren, err := sdl.CreateRenderer(window, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}
	return newSDLRendererBackend(ren, true), nil
}

func newSDLRendererBackend(ren *sdl.Renderer, software bool) *sdlRendererBackend {
	name := "unknown"
	if info, err := ren.GetInfo(); err == nil {
		name = info.Name
	}
	return &sdlRendererBackend{rendererBackend{renderer: ren, name: name, software: software}}
}

func (b *sdlRendererBackend) Device() *sdl.GPUDevice { return nil }

func (b *sdlRendererBackend) Close() { _ = b.renderer.Destroy() }
