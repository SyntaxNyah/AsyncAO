//go:build windows

package render

import (
	"syscall"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Ties the detached preview window's real OS window to the main window's, so
// the window manager keeps them z-ordered together — requirement: "popped
// out emote preview should have shared priority with main async window,
// i.e. moving on top of other windows when async is selected".
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	// SetLastError is needed to disambiguate SetWindowLongPtrW's return
	// value: see the comment inside LinkOwner for why a bare 0 return is
	// ambiguous and MSDN's own documented workaround.
	procSetLastError = kernel32.NewProc("SetLastError")
)

// gwlpHwndParent is the Win32 GWLP_HWNDPARENT index for
// {Get,Set}WindowLongPtrW (MSDN-documented constant, -8). Passing it to
// SetWindowLongPtrW installs an OWNER, not a WS_CHILD parent.
const gwlpHwndParent = -8

// LinkOwner ties p's real OS window to mainWin as a Win32 owner, so the
// window manager keeps them z-ordered together (see the package-level
// comment above for the measured reasoning). Call once, right after a
// successful Open/OpenAt/OpenAtClamped.
// Render-thread only (hard rule 1): every branch here either touches an SDL
// window handle or nothing at all.
func (p *PreviewWindow) LinkOwner(mainWin *sdl.Window) bool {
	if p == nil || p.win == nil || mainWin == nil {
		return false
	}
	childInfo, err := p.win.GetWMInfo()
	if err != nil || childInfo == nil {
		return false
	}
	child := childInfo.GetWindowsInfo().Window
	ownerInfo, err := mainWin.GetWMInfo()
	if err != nil || ownerInfo == nil {
		return false
	}
	owner := ownerInfo.GetWindowsInfo().Window
	if child == nil || owner == nil {
		return false
	}
	if procSetWindowLongPtrW.Find() != nil {
		return false
	}
	// MSDN's own documented pattern for SetWindowLongPtrW: a 0 return is
	// ALSO the legitimate "this window had no owner before" value, so a
	// bare `prev == 0` check cannot tell success from failure on its own.
	// Clear the last-error slot first, then only treat a 0 return as a
	// genuine failure if GetLastError became non-zero as a result of THIS
	// call. (syscall.LazyProc.Call's returned error is always non-nil —
	// it is GetLastError() wrapped as a syscall.Errno regardless of
	// outcome, per the syscall package's own documented contract, the
	// same gotcha internal/ui/dpiseed_windows.go's GetDpiForWindow call
	// already works around — so the errno VALUE is what matters here, not
	// its nil-ness.)
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
