package main

import (
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// gpuGiveUpMessage tells the user why the client closes when even the software
// driver lost its device, so it doesn't just vanish.
const gpuGiveUpMessage = "The graphics device stopped working, and restarting AsyncAO " +
	"(also on the software renderer) did not help. AsyncAO will close now.\n\n" +
	"Updating or reinstalling your graphics driver usually fixes this."

// gpuRecoveryArgs is the command line for the client restarted after a GPU device
// loss: the same master server, vsync and debug settings, the losses in a row for
// the render.GPUWatcher of the new run (1 again after a late loss, which the watcher
// answers with a plain restart), and the software driver once the watcher asks for
// it (or it was already forced). -server is left out: the restarted client
// reopens its saved tabs instead (Deps.RestoreTabs).
func gpuRecoveryArgs(recovery render.GPURecovery, masterURL string, opts render.BackendOptions, recoveries int, debug bool) []string {
	next := 1
	if recovery == render.GPURestartSoftware {
		next = recoveries + 1
	}
	args := []string{
		"-master=" + masterURL,
		"-vsync=" + strconv.FormatBool(opts.VSync),
		"-gpu-recovery=" + strconv.Itoa(next),
	}
	if recovery == render.GPURestartSoftware || opts.ForceSoftware {
		args = append(args, "-software-gpu")
	}
	if debug {
		args = append(args, "-debug")
	}
	return args
}
