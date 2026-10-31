//go:build windows

package ui

import (
	"syscall"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Reliable per-monitor DPI on Windows. GetDisplayDPI commonly reports a flat 96 under per-monitor-v2 DPI awareness
// (SDL3's default), so boot auto-scale detection stayed at 100 on a HiDPI monitor. user32!GetDpiForWindow
// (Windows 10 1607+) returns the effective DPI for the window's monitor and honours per-monitor-v2.
// The HWND comes from Window.Win32Handle. This is a plain Win32 syscall, not an SDL render call, but it touches
// the SDL window handle so it runs on the locked main thread, as every caller does (boot seed and the
// display-change handler).
// syscall.NewLazyDLL avoids a new dependency (golang.org/x/sys is not in go.mod, and adding it needs a written
// justification per CLAUDE.md); a LazyDLL resolves the proc on first use and is safe at package scope.

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procGetDpiForWindow = user32.NewProc("GetDpiForWindow")
)

// queryWindowDPI returns the effective DPI for the monitor hosting win, and true on success. It returns (0, false)
// if the window handle can't be reached, GetDpiForWindow is unavailable (pre-1607) or it returns 0; the caller then
// tries sdl.GetDisplayDPI. Render thread only.
func queryWindowDPI(win *sdl.Window) (float64, bool) {
	if win == nil {
		return 0, false
	}
	hwnd := win.Win32Handle()
	if hwnd == nil {
		return 0, false
	}
	// Guard the lazy proc: on pre-1607 Windows the symbol is absent and a bare Call would panic.
	if procGetDpiForWindow.Find() != nil {
		return 0, false
	}
	// GetDpiForWindow returns a UINT DPI (96 = 100%); 0 means failure. There is no SetLastError contract, so the
	// return value is the only success signal.
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(hwnd))
	if dpi == 0 {
		return 0, false
	}
	return float64(dpi), true
}
