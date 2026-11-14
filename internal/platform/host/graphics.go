package host

import (
	"log"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Graphics is the renderer OpenGraphics chose for the window.
type Graphics struct {
	// Renderer is what the compositor and the UI draw with; destroying it is the caller's job.
	Renderer *sdl.Renderer
	// Name says what is drawing, for the log, debug overlay and About screen, e.g. "direct3d11".
	Name string
	// Software reports drawing on the CPU with SDL's software renderer.
	Software bool
	// Format names the pixel format the frame is drawn into, which sets the cost of every blended draw on the CPU.
	// Empty on a GPU renderer.
	Format string
}

// GraphicsOptions configures how OpenGraphics picks the graphics.
type GraphicsOptions struct {
	VSync bool
	// ForceSoftware skips the GPU, for machines whose graphics driver is broken.
	ForceSoftware bool
}

// OpenGraphics opens SDL's renderer on the GPU, else SDL's software renderer. SDL's default choice can land on its
// software renderer by itself, so Software follows the renderer's name, not which call opened it.
func OpenGraphics(window *sdl.Window, opts GraphicsOptions) (*Graphics, error) {
	flags := uint32(0)
	if opts.VSync {
		flags = sdl.RENDERER_PRESENTVSYNC
	}
	if !opts.ForceSoftware {
		ren, err := sdl.CreateRenderer(window, -1, flags)
		if err == nil {
			return newGraphics(window, ren), nil
		}
		log.Printf("gpu: no GPU renderer: %v", err)
	}
	ren, err := sdl.CreateRenderer(window, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		return nil, err
	}
	return newGraphics(window, ren), nil
}

func newGraphics(window *sdl.Window, ren *sdl.Renderer) *Graphics {
	name := "unknown"
	if info, err := ren.GetInfo(); err == nil {
		name = info.Name
	}
	g := &Graphics{Renderer: ren, Name: name, Software: name == sdl.SoftwareRendererName}
	if g.Software {
		g.Format = window.SurfaceFormatName()
	}
	return g
}
