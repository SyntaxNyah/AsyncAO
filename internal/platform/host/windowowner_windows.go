package host

import (
	"syscall"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// A Win32 owner link makes the window manager keep a window z-ordered with its owner.
var (
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	// SetLastError disambiguates SetWindowLongPtrW's return value; see LinkWindowOwner.
	procSetLastError = kernel32.NewProc("SetLastError")
)

// gwlpHwndParent is GWLP_HWNDPARENT (-8); SetWindowLongPtrW then installs an owner, not a WS_CHILD parent.
const gwlpHwndParent = -8

// LinkWindowOwner makes owner the Win32 owner of child; call once after child opens. Main thread only.
func LinkWindowOwner(child, owner *sdl.Window) bool {
	if child == nil || owner == nil {
		return false
	}
	childHandle, ownerHandle := child.Win32Handle(), owner.Win32Handle()
	if childHandle == nil || ownerHandle == nil {
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
	prev, _, callErr := procSetWindowLongPtrW.Call(uintptr(childHandle), uintptr(uint64(int64(idx))), uintptr(ownerHandle))
	if prev == 0 {
		if errno, ok := callErr.(syscall.Errno); !ok || errno != 0 {
			return false
		}
	}
	return true
}
