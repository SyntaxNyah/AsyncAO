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

// crashLogName is the file that catches crashes no recover site saw.
const (
	crashLogName    = "asyncao-crash.log"
	crashLogMode    = 0o644
	crashLogDirMode = 0o755
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
	Audio    AudioDevices
	Desktop  Desktop
	windows  []*sdl.Window
	// relaunch and relaunchArgs are set by RelaunchAfterExit.
	relaunch     bool
	relaunchArgs []string
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

// OpenExtraWindow opens a window beyond the main one with its own renderer; the caller destroys both.
func OpenExtraWindow(title string, w, h int32) (*sdl.Window, *sdl.Renderer, error) {
	win, err := sdl.CreateWindow(title, sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		w, h, sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE)
	if err != nil {
		return nil, nil, fmt.Errorf("window: %w", err)
	}
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_ACCELERATED|sdl.RENDERER_PRESENTVSYNC)
	if err != nil {
		// VMs and the dummy video driver have no accelerated backend.
		ren, err = sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
		if err != nil {
			_ = win.Destroy()
			return nil, nil, fmt.Errorf("renderer: %w", err)
		}
	}
	return win, ren, nil
}

// RelaunchAfterExit starts the client again with args once it has returned and SDL has quit.
func (r *Runtime) RelaunchAfterExit(args []string) {
	r.relaunch, r.relaunchArgs = true, args
}

func (r *Runtime) closeWindows() {
	for i := len(r.windows) - 1; i >= 0; i-- {
		_ = r.windows[i].Destroy()
	}
	r.windows = nil
}

// Main runs client between process setup and teardown, and returns its error.
func Main(app App, probe platform.Probe, client func(*Runtime) error) error {
	return mainWith(app, probe, platform.RestartProgram, client)
}

// mainWith is Main with the relaunch injected; it runs last, so the two clients never share SDL or the saved state.
func mainWith(app App, probe platform.Probe, relaunch func([]string) error, client func(*Runtime) error) (err error) {
	// SDL demands the main OS thread for the whole lifetime.
	runtime.LockOSThread()

	detected := platform.Detect(probe)
	// Crash reports need every goroutine's stack, even in -H=windowsgui where stderr goes nowhere.
	debug.SetTraceback("all")
	setupCrashOutput(app.Name)
	log.Print(detected.Report())

	rt := &Runtime{Platform: detected}
	defer func() {
		if err != nil || !rt.relaunch {
			return
		}
		if startErr := relaunch(rt.relaunchArgs); startErr != nil {
			log.Printf("relaunch: %v", startErr)
		}
	}()

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

	defer rt.closeWindows()
	return client(rt)
}

// setupCrashOutput writes crashes the per-path recover sites miss to a file.
func setupCrashOutput(appName string) {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	cacheDir, _ := os.UserCacheDir()
	f := openCrashLog(exeDir, cacheDir, appName)
	if f == nil {
		return
	}
	// SetCrashOutput duplicates f's descriptor; we can close f immediately.
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	_ = f.Close()
}

// openCrashLog prefers the executable's folder, which Program Files, a macOS bundle and a Flatpak don't let us write.
func openCrashLog(exeDir, cacheDir, appName string) *os.File {
	const flags = os.O_CREATE | os.O_APPEND | os.O_WRONLY
	if exeDir != "" {
		if f, err := os.OpenFile(filepath.Join(exeDir, crashLogName), flags, crashLogMode); err == nil {
			return f
		}
	}
	if cacheDir == "" {
		return nil
	}
	dir := filepath.Join(cacheDir, appName)
	if err := os.MkdirAll(dir, crashLogDirMode); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, crashLogName), flags, crashLogMode)
	if err != nil {
		return nil
	}
	return f
}
