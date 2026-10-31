//go:build !windows

package platform

import "os/exec"

// Command prepares a child process; only Windows has a console window to suppress.
func Command(name string, args ...string) *Process { return exec.Command(name, args...) }
