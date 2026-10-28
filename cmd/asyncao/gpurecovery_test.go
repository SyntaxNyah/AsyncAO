package main

import (
	"flag"
	"slices"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestGPURecoveryArgs pins the restarted client's command line: the loss is
// counted, the software driver is added when the watcher escalates (and kept when
// it was forced), and every argument parses under the client's real flag names.
func TestGPURecoveryArgs(t *testing.T) {
	const master = "https://servers.example/list"
	cases := []struct {
		name         string
		recovery     render.GPURecovery
		opts         render.BackendOptions
		recoveries   int
		debug        bool
		wantSoftware bool
		wantCount    int
	}{
		{"first loss", render.GPURestart, render.BackendOptions{VSync: true}, 0, false, false, 1},
		{"late loss after a restart counts as a first loss", render.GPURestart, render.BackendOptions{VSync: true}, 1, false, false, 1},
		{"second loss escalates to software", render.GPURestartSoftware, render.BackendOptions{VSync: true}, 1, true, true, 2},
		{"forced software stays forced", render.GPURestart, render.BackendOptions{ForceSoftware: true}, 0, false, true, 1},
	}
	for _, tc := range cases {
		args := gpuRecoveryArgs(tc.recovery, master, tc.opts, tc.recoveries, tc.debug)

		fs := flag.NewFlagSet("asyncao", flag.ContinueOnError)
		gotMaster := fs.String("master", "", "")
		gotVsync := fs.Bool("vsync", false, "")
		gotSoftware := fs.Bool("software-gpu", false, "")
		gotCount := fs.Int("gpu-recovery", 0, "")
		gotDebug := fs.Bool("debug", false, "")
		if err := fs.Parse(args); err != nil {
			t.Fatalf("%s: %v does not parse: %v", tc.name, args, err)
		}
		if *gotMaster != master || *gotVsync != tc.opts.VSync || *gotDebug != tc.debug {
			t.Errorf("%s: %v lost the run's settings", tc.name, args)
		}
		if *gotSoftware != tc.wantSoftware {
			t.Errorf("%s: software-gpu = %v, want %v", tc.name, *gotSoftware, tc.wantSoftware)
		}
		if *gotCount != tc.wantCount {
			t.Errorf("%s: gpu-recovery = %d, want %d", tc.name, *gotCount, tc.wantCount)
		}
		if slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "-server") }) {
			t.Errorf("%s: %v reconnects -server; the restarted client reopens its saved tabs instead", tc.name, args)
		}
	}
}
