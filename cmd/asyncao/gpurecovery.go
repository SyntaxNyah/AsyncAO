package main

import (
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// gpuGiveUpMessage tells the user why the client closes when even the software driver lost its device.
const gpuGiveUpMessage = "The graphics device stopped working, and restarting AsyncAO " +
	"(also on the software renderer) did not help. AsyncAO will close now.\n\n" +
	"Updating or reinstalling your graphics driver usually fixes this."

// gpuRecoveryArgs is the command line for the restart after a GPU device loss; -server is left out because
// the restarted client reopens its saved tabs.
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
