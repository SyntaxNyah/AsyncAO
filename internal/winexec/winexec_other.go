//go:build !windows

// Package winexec is a no-op off Windows, which has no console window to suppress.
package winexec

import "os/exec"

// Hide is a no-op on non-Windows platforms.
func Hide(cmd *exec.Cmd) {}

// AllowSetForeground is a no-op on non-Windows platforms.
func AllowSetForeground(pid int) {}
