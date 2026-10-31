package host

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Graphics is how the window is drawn; only the composition root's OpenGraphics knows which kind it is.
type Graphics interface {
	// Renderer is what the compositor and the UI draw with.
	Renderer() *sdl.Renderer
	// Device is the SDL_GPU device the UI library shares; nil when the graphics has none.
	Device() *sdl.GPUDevice
	// Present shows the frame; its error goes to a GPUWatcher.
	Present() error
	// Name says what is drawing, for the log, debug overlay and About screen, e.g. "vulkan (NVIDIA GeForce RTX 3070)".
	Name() string
	// Software reports drawing on the CPU (lavapipe or SDL's software renderer).
	Software() bool
	// Close destroys the renderer, then anything under it.
	Close()
}

// vulkanDriverFilesEnv replaces the Vulkan loader's driver search; the lavapipe manifest selects software.
const vulkanDriverFilesEnv = "VK_DRIVER_FILES"

// GraphicsOptions configures how OpenGraphics picks the graphics.
type GraphicsOptions struct {
	VSync bool
	// ForceSoftware skips the hardware driver, for machines whose GPU driver is broken.
	ForceSoftware bool
	// SoftwareDriverFile is the bundled lavapipe manifest, or "" when this build ships none.
	SoftwareDriverFile string
}

// OpenGraphics tries an SDL_GPU device on hardware, then lavapipe (SDL doesn't fall back), then SDL's own renderer.
func OpenGraphics(window *sdl.Window, opts GraphicsOptions) (Graphics, error) {
	flags := uint32(0)
	if opts.VSync {
		flags = sdl.RENDERER_PRESENTVSYNC
	}
	if !opts.ForceSoftware {
		b, err := openDeviceGraphics(window, flags, false)
		if err == nil {
			return b, nil
		}
		log.Printf("gpu: hardware %s driver unavailable: %v", gpuDriverLabel, err)
	}
	if opts.SoftwareDriverFile != "" {
		var b *deviceGraphics
		err := withEnv(vulkanDriverFilesEnv, opts.SoftwareDriverFile, func() (err error) {
			b, err = openDeviceGraphics(window, flags, true)
			return err
		})
		if err == nil {
			return b, nil
		}
		log.Printf("gpu: software driver %s unavailable: %v", opts.SoftwareDriverFile, err)
	}
	return openSDLRendererGraphics(window, flags, opts.ForceSoftware)
}

// withEnv runs fn with name set, then restores it; left set it would leak into a client relaunched after device loss.
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

// rendererGraphics is the part every graphics shares: renderer, name and whether it is software.
type rendererGraphics struct {
	renderer *sdl.Renderer
	name     string
	software bool
}

func (b *rendererGraphics) Renderer() *sdl.Renderer { return b.renderer }
func (b *rendererGraphics) Present() error          { return b.renderer.Present() }
func (b *rendererGraphics) Name() string            { return b.name }
func (b *rendererGraphics) Software() bool          { return b.software }

// deviceGraphics draws through an SDL_GPU device, so the compositor and the UI library share one device.
type deviceGraphics struct {
	rendererGraphics
	device *sdl.GPUDevice
}

var _ Graphics = (*deviceGraphics)(nil)

// openDeviceGraphics creates a device on the platform's driver and a renderer on it.
func openDeviceGraphics(window *sdl.Window, flags uint32, software bool) (*deviceGraphics, error) {
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
	return &deviceGraphics{rendererGraphics: rendererGraphics{renderer: ren, name: name, software: software}, device: device}, nil
}

func (b *deviceGraphics) Device() *sdl.GPUDevice { return b.device }

func (b *deviceGraphics) Close() {
	_ = b.renderer.Destroy()
	b.device.Destroy()
}

// sdlRendererGraphics is SDL's own renderer choice, with no SDL_GPU device.
type sdlRendererGraphics struct {
	rendererGraphics
}

var _ Graphics = (*sdlRendererGraphics)(nil)

// openSDLRendererGraphics opens SDL's default renderer, or its software renderer on failure or when forced.
func openSDLRendererGraphics(window *sdl.Window, flags uint32, software bool) (*sdlRendererGraphics, error) {
	var errs []error
	if !software {
		ren, err := sdl.CreateRenderer(window, -1, flags)
		if err == nil {
			return newSDLRendererGraphics(ren, false), nil
		}
		errs = append(errs, err)
	}
	ren, err := sdl.CreateRenderer(window, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}
	return newSDLRendererGraphics(ren, true), nil
}

func newSDLRendererGraphics(ren *sdl.Renderer, software bool) *sdlRendererGraphics {
	name := "unknown"
	if info, err := ren.GetInfo(); err == nil {
		name = info.Name
	}
	return &sdlRendererGraphics{rendererGraphics{renderer: ren, name: name, software: software}}
}

func (b *sdlRendererGraphics) Device() *sdl.GPUDevice { return nil }

func (b *sdlRendererGraphics) Close() { _ = b.renderer.Destroy() }
