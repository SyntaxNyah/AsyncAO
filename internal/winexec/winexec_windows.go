//go:build windows

// Package winexec suppresses a console child's window: release builds link -H=windowsgui, so each child would
// pop its own. CREATE_NO_WINDOW leaves the child's GUI windows alone, which HideWindow can't guarantee.
package winexec

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Win32 CREATE_NO_WINDOW process-creation flag.
const createNoWindow = 0x08000000

// Hide stops a console window appearing for cmd; call it after exec.Command and before Start, Run or Output. nil-safe.
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

// AllowSetForeground gives the child our foreground rights, as Windows refuses them to a CREATE_NO_WINDOW
// child and its dialog can appear without focus. Call after Start, before it shows a window; best-effort.
func AllowSetForeground(pid int) {
	_, _, _ = procAllowSetForegroundWnd.Call(uintptr(pid))
}
