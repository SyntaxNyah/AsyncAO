package platform

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Win32 CREATE_NO_WINDOW process-creation flag.
const createNoWindow = 0x08000000

// Command prepares a child that shows no console: release builds link -H=windowsgui, so each child would pop its own.
// CREATE_NO_WINDOW leaves the child's GUI windows alone, which HideWindow can't guarantee.
func Command(name string, args ...string) *Process {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd
}
