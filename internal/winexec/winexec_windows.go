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

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	procAllowSetForegroundWnd = user32.NewProc("AllowSetForegroundWindow")
)

// AllowSetForeground donates THIS process's foreground-activation rights to the
// child pid. A CREATE_NO_WINDOW child (our dialog shell-outs) is a background
// process Windows refuses foreground activation: its TopMost dialog gets
// z-order but its Activate() calls no-op, so the dialog can appear without
// focus — or, some shells, not visibly at all. We ARE the foreground process
// (the user just clicked us), so we may hand the right down. Call after
// Start(), before the child shows its window. Best-effort: a failed call just
// leaves today's behavior.
func AllowSetForeground(pid int) {
	_, _, _ = procAllowSetForegroundWnd.Call(uintptr(pid))
}
