//go:build !windows

// Package winexec suppresses the console window Windows pops for a console child
// process. Off Windows there is no such concept, so Hide is a no-op.
package winexec

import "os/exec"

// Hide is a no-op on non-Windows platforms.
func Hide(cmd *exec.Cmd) {}

// AllowSetForeground is a no-op on non-Windows platforms.
func AllowSetForeground(pid int) {}
