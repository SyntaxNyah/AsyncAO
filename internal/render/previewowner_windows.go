//go:build windows

package render

import (
	"syscall"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// A Win32 owner link makes the window manager keep the preview z-ordered with the main window.
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	// SetLastError disambiguates SetWindowLongPtrW's return value; see LinkOwner.
	procSetLastError = kernel32.NewProc("SetLastError")
)

// gwlpHwndParent is GWLP_HWNDPARENT (-8); SetWindowLongPtrW then installs an owner, not a WS_CHILD parent.
const gwlpHwndParent = -8

// LinkOwner makes mainWin the Win32 owner of p's window; call once after a successful open. Render thread only.
func (p *PreviewWindow) LinkOwner(mainWin *sdl.Window) bool {
	if p == nil || p.win == nil || mainWin == nil {
		return false
	}
	child, owner := p.win.Win32Handle(), mainWin.Win32Handle()
	if child == nil || owner == nil {
		return false
	}
	if procSetWindowLongPtrW.Find() != nil {
		return false
	}
	// MSDN: 0 can also mean "no previous owner", so clear the last error and fail only if it became non-zero.
	// LazyProc.Call's error is always non-nil (GetLastError wrapped), so only the errno value matters.
	if procSetLastError.Find() == nil {
		procSetLastError.Call(0)
	}
	idx := int32(gwlpHwndParent) // sign-extended per SetWindowLongPtrW's LONG_PTR nIndex parameter
	prev, _, callErr := procSetWindowLongPtrW.Call(uintptr(child), uintptr(uint64(int64(idx))), uintptr(owner))
	if prev == 0 {
		if errno, ok := callErr.(syscall.Errno); !ok || errno != 0 {
			return false
		}
	}
	return true
}
