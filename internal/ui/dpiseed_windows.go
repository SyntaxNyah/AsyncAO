//go:build windows

package ui

import (
	"syscall"

	"github.com/veandco/go-sdl2/sdl"
)

// #77 Part B — reliable per-monitor DPI on Windows.

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procGetDpiForWindow = user32.NewProc("GetDpiForWindow")
)

// queryWindowDPI returns the effective DPI for the monitor hosting win, and true
// on success. It falls back to (0, false) if the window handle can't be reached
// or GetDpiForWindow is unavailable (pre-1607) or returns 0 — the caller then
// tries sdl.GetDisplayDPI. Render thread only (touches the SDL window handle).
func queryWindowDPI(win *sdl.Window) (float64, bool) {
	if win == nil {
		return 0, false
	}
	info, err := win.GetWMInfo()
	if err != nil || info == nil {
		return 0, false
	}
	// The HWND lives in the Windows subsystem view of the SysWMInfo union.
	hwnd := info.GetWindowsInfo().Window
	if hwnd == nil {
		return 0, false
	}
	// Guard the lazy proc: on pre-1607 Windows the symbol is absent and a
	// bare Call would PANIC. Find reports that cleanly so we fall back.
	if procGetDpiForWindow.Find() != nil {
		return 0, false
	}
	// GetDpiForWindow returns a UINT DPI (96 = 100%); 0 means it failed
	// (e.g. the OS predates the API). No SetLastError contract, so the
	// return value is the only success signal.
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(hwnd))
	if dpi == 0 {
		return 0, false
	}
	return float64(dpi), true
}
