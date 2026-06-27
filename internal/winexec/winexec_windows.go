//go:build windows

// Package winexec suppresses the console window Windows pops for a console child
// process. Release builds link as the GUI subsystem (-H=windowsgui), so the app
// has no console of its own; without this, every console child (powershell, reg,
// ffmpeg, ...) spawns its OWN console window — the empty-PowerShell flash seen
// when browsing themes, exporting video, etc. CREATE_NO_WINDOW suppresses only
// the console and leaves any GUI window the child shows (e.g. a folder-picker
// dialog) untouched, which HideWindow/SW_HIDE can't guarantee.
package winexec

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Win32 CREATE_NO_WINDOW process-creation flag.
const createNoWindow = 0x08000000

// Hide configures cmd so no console window appears when it runs. Call it after
// exec.Command and before Start/Run/Output. nil-safe.
func Hide(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
