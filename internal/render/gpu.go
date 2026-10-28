package render

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// vulkanDriverFilesEnv is the Vulkan loader's setting that replaces its driver
// search with the listed driver files (ICD manifests). Pointing it at the bundled
// lavapipe manifest is how the software tier is selected.
const vulkanDriverFilesEnv = "VK_DRIVER_FILES"

// GPUOptions configures how OpenGPU picks the renderer. Built by the composition
// root from flags and the build's staged files.
type GPUOptions struct {
	VSync bool
	// ForceSoftware skips the hardware driver, for machines whose GPU driver is
	// broken.
	ForceSoftware bool
	// SoftwareDriverFile is the bundled software Vulkan driver's manifest
	// (lavapipe), or "" when this build ships none.
	SoftwareDriverFile string
}

// GPU owns a window's renderer and the SDL_GPU device it draws through, so the
// courtroom compositor and the UI library share one device.
//
// The device is created first, on the driver the platform names (Vulkan off
// macOS, so the UI library's SPIR-V shaders load; Metal on macOS). Without a
// hardware driver it is created again on the bundled software driver. SDL does
// not fall back by itself: with no Vulkan driver, device creation just fails. When
// neither works — a development build with no software driver staged, or a
// headless test — SDL's own renderer choice keeps the client drawing; the UI
// library will need the device, the compositor does not.
type GPU struct {
	device   *sdl.GPUDevice // nil on SDL's own renderer
	renderer *sdl.Renderer
	backend  string
}

// OpenGPU creates the renderer for window, trying the hardware GPU driver, then
// the software driver, then SDL's own renderer.
func OpenGPU(window *sdl.Window, opts GPUOptions) (*GPU, error) {
	flags := uint32(0)
	if opts.VSync {
		flags = sdl.RENDERER_PRESENTVSYNC
	}
	if !opts.ForceSoftware {
		g, err := openOnDevice(window, flags)
		if err == nil {
			return g, nil
		}
		log.Printf("gpu: hardware %s driver unavailable: %v", gpuDriverLabel, err)
	}
	if opts.SoftwareDriverFile != "" {
		// The loader reads the setting at each instance creation, so it applies to
		// this attempt and to every rebuild after it.
		if err := os.Setenv(vulkanDriverFilesEnv, opts.SoftwareDriverFile); err != nil {
			return nil, err
		}
		g, err := openOnDevice(window, flags)
		if err == nil {
			return g, nil
		}
		log.Printf("gpu: software driver %s unavailable: %v", opts.SoftwareDriverFile, err)
	}
	return openSDLRenderer(window, flags, opts.ForceSoftware)
}

// openOnDevice creates a device on the platform's driver and a renderer on it.
func openOnDevice(window *sdl.Window, flags uint32) (*GPU, error) {
	device, err := sdl.CreateGPUDevice(gpuShaderFormats, gpuDriver)
	if err != nil {
		return nil, err
	}
	ren, err := sdl.CreateGPURenderer(device, window, flags)
	if err != nil {
		device.Destroy()
		return nil, err
	}
	backend := device.Driver()
	if name := device.Name(); name != "" {
		backend = fmt.Sprintf("%s (%s)", backend, name)
	}
	return &GPU{device: device, renderer: ren, backend: backend}, nil
}

// openSDLRenderer is the last resort: SDL's default renderer, or its software
// renderer when that fails or software was forced.
func openSDLRenderer(window *sdl.Window, flags uint32, software bool) (*GPU, error) {
	var errs []error
	if !software {
		ren, err := sdl.CreateRenderer(window, -1, flags)
		if err == nil {
			return wrapSDLRenderer(ren), nil
		}
		errs = append(errs, err)
	}
	ren, err := sdl.CreateRenderer(window, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}
	return wrapSDLRenderer(ren), nil
}

func wrapSDLRenderer(ren *sdl.Renderer) *GPU {
	backend := "unknown"
	if info, err := ren.GetInfo(); err == nil {
		backend = info.Name
	}
	return &GPU{renderer: ren, backend: backend}
}

// Renderer is the window's renderer.
func (g *GPU) Renderer() *sdl.Renderer { return g.renderer }

// Backend names what is drawing, for the debug overlay and the About screen:
// "vulkan (NVIDIA GeForce RTX 3070)", "vulkan (llvmpipe …)", or SDL's renderer
// name on the last resort.
func (g *GPU) Backend() string { return g.backend }

// HasDevice reports whether the renderer draws through an SDL_GPU device, which
// the UI library needs.
func (g *GPU) HasDevice() bool { return g.device != nil }

// Close destroys the renderer, then the device under it.
func (g *GPU) Close() {
	_ = g.renderer.Destroy()
	if g.device != nil {
		g.device.Destroy()
	}
}
