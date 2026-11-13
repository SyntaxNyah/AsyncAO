package main

import (
	"flag"
	"testing"
)

// TestCPUFallbackArgs: the client that replaces a frozen one starts on SDL's CPU renderer by its own command line, so
// a pref that failed to save can't send it back to the frozen driver, and it keeps the run's settings.
func TestCPUFallbackArgs(t *testing.T) {
	const master = "https://servers.example/list"
	for _, debug := range []bool{false, true} {
		args := cpuFallbackArgs(master, true, debug)

		fs := flag.NewFlagSet("asyncao", flag.ContinueOnError)
		gotMaster := fs.String("master", "", "")
		gotVsync := fs.Bool("vsync", false, "")
		gotSoftware := fs.Bool("software-gpu", false, "")
		gotRenderer := fs.String("renderer", rendererGPU, "")
		gotStalled := fs.Bool(rendererStalledFlag, false, "")
		gotDebug := fs.Bool("debug", false, "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("%v does not parse: %v", args, err)
		}
		if *gotMaster != master || !*gotVsync || *gotDebug != debug {
			t.Errorf("%v lost the run's settings", args)
		}
		skips, err := skipsGPUDevice(*gotRenderer)
		if err != nil || !skips || !*gotSoftware {
			t.Errorf("%v doesn't start on SDL's CPU renderer", args)
		}
		if !*gotStalled {
			t.Errorf("%v doesn't tell the new client why it restarted", args)
		}
	}
}
