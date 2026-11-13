package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

const (
	// rendererStallLimit is how long one frame may sit in the graphics driver before it counts as frozen. Windows
	// marks a window not responding after 5 s; a working driver never comes close.
	rendererStallLimit = 6 * time.Second
	// stallCheckEvery is how often the stall check runs.
	stallCheckEvery = 500 * time.Millisecond
	// exitRendererStalled is the exit code of a client replaced after its graphics driver froze.
	exitRendererStalled = 3
)

// rendererStalledFlag marks the client that replaced one frozen in the graphics driver.
const rendererStalledFlag = "renderer-stalled"

// cpuFallbackArgs is the command line for the client that replaces one frozen in the graphics driver. It names the CPU
// renderer itself too, so a pref that failed to save can't bring the frozen driver back in a loop.
func cpuFallbackArgs(masterURL string, vsync, debug bool) []string {
	args := []string{
		"-master=" + masterURL,
		"-vsync=" + strconv.FormatBool(vsync),
		"-renderer=" + rendererSDL,
		"-software-gpu",
		"-" + rendererStalledFlag,
	}
	if debug {
		args = append(args, "-debug")
	}
	return args
}

// fallBackToCPU remembers the CPU renderer, starts a client on it and ends this one, whose main thread is stuck in the
// graphics driver; it runs on the stall check's goroutine.
func fallBackToCPU(prefs *config.AssetPreferences, args []string) {
	prefs.Update(func(sn *config.Settings) { sn.CPURenderer = true })
	if err := prefs.SaveNow(); err != nil {
		log.Printf("gpu: saving the CPU renderer choice: %v", err)
	}
	if err := platform.RestartProgram(args); err != nil {
		log.Printf("gpu: starting a client on the CPU renderer: %v", err)
		return
	}
	log.Printf("gpu: started a client on the CPU renderer; closing this one")
	os.Exit(exitRendererStalled)
}
