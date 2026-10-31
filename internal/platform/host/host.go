// Package host starts and ends the process: everything platform-specific that must be settled before SDL runs, then
// SDL itself and the hardware it opens. It runs on the locked main thread (hard rule 1).
package host

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// crashLogName is the file beside the executable that catches crashes no recover site saw.
const (
	crashLogName = "asyncao-crash.log"
	crashLogMode = 0o644
)

// App identifies the client to the OS and to SDL.
type App struct {
	Name    string
	Version string
	// ID is the reverse-DNS id Wayland, Flatpak and the audio mixer show.
	ID string
}

// Runtime is what the host hands the client once SDL is running.
type Runtime struct {
	Platform platform.Platform
	windows  []*sdl.Window
}

// WindowSpec describes a window to open.
type WindowSpec struct {
	Title         string
	Width, Height int32
}

// OpenMainWindow opens the centred main window; the host destroys it after the client returns.
func (r *Runtime) OpenMainWindow(spec WindowSpec) (*sdl.Window, error) {
	// The primary display is right here only: no window exists yet and no position is saved.
	window, err := sdl.CreateWindow(spec.Title, sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		spec.Width, spec.Height, sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE|highDPIFlags())
	if err != nil {
		return nil, err
	}
	r.windows = append(r.windows, window)
	// SDL3 sends no text-input events until this is on.
	if err := window.StartTextInput(); err != nil {
		log.Printf("text input unavailable: %v", err)
	}
	return window, nil
}

func (r *Runtime) closeWindows() {
	for i := len(r.windows) - 1; i >= 0; i-- {
		_ = r.windows[i].Destroy()
	}
	r.windows = nil
}

// Main runs client between process setup and teardown, and returns its error.
func Main(app App, probe platform.Probe, client func(*Runtime) error) error {
	// SDL demands the main OS thread for the whole lifetime.
	runtime.LockOSThread()

	detected := platform.Detect(probe)
	// Crash reports need every goroutine's stack, even in -H=windowsgui where stderr goes nowhere.
	debug.SetTraceback("all")
	setupCrashOutput()
	log.Print(detected.Report())

	if err := sdl.SetAppMetadata(app.Name, app.Version, app.ID); err != nil {
		log.Printf("app metadata: %v", err)
	}
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_AUDIO | sdl.INIT_EVENTS); err != nil {
		return fmt.Errorf("sdl: %w", err)
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		return fmt.Errorf("sdl_ttf: %w", err)
	}
	defer ttf.Quit()

	rt := &Runtime{Platform: detected}
	defer rt.closeWindows()
	return client(rt)
}

// setupCrashOutput writes crashes the per-path recover sites miss beside the executable.
func setupCrashOutput() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), crashLogName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, crashLogMode)
	if err != nil {
		return
	}
	// SetCrashOutput duplicates f's descriptor; we can close f immediately.
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		return
	}
	_ = f.Close()
}
