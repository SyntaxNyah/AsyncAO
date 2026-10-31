package main

import (
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/platform/host"
)

// gpuGiveUpMessage tells the user why the client closes when even the software driver lost its device.
const gpuGiveUpMessage = "The graphics device stopped working, and restarting AsyncAO " +
	"(also on the software renderer) did not help. AsyncAO will close now.\n\n" +
	"Updating or reinstalling your graphics driver usually fixes this."

// gpuRecoveryArgs is the command line for the restart after a GPU device loss; -server is left out because
// the restarted client reopens its saved tabs.
func gpuRecoveryArgs(recovery host.GPURecovery, masterURL string, opts host.GraphicsOptions, recoveries int, debug bool) []string {
	next := 1
	if recovery == host.GPURestartSoftware {
		next = recoveries + 1
	}
	args := []string{
		"-master=" + masterURL,
		"-vsync=" + strconv.FormatBool(opts.VSync),
		"-gpu-recovery=" + strconv.Itoa(next),
	}
	if recovery == host.GPURestartSoftware || opts.ForceSoftware {
		args = append(args, "-software-gpu")
	}
	if debug {
		args = append(args, "-debug")
	}
	return args
}
