//go:build windows

package render

import (
	"syscall"

	"github.com/veandco/go-sdl2/sdl"
)

// Ties the detached preview window's real OS window to the main window's, so
// the window manager keeps them z-ordered together — requirement: "popped
// out emote preview should have shared priority with main async window,
// i.e. moving on top of other windows when async is selected".
//
// WHY NOT A Raise() FOCUS HOOK: the obvious-looking fix (on the main
// window's WINDOWEVENT_FOCUS_GAINED, call preview.Raise()) is wrong and was
// measured wrong, not reasoned wrong — go-sdl2's own Window.Raise() doc says
// it "raises the window above other windows AND SETS THE INPUT FOCUS", so a
// one-directional hook steals focus back onto the preview on every click of
// the main window, and the natural bidirectional version anyone would reach
// for next ("keep them glued together both ways") is a genuine unbounded
// focus ping-pong: a live two-window SDL probe measured 16 alternating
// Raise() calls in 3.5 wall-clock seconds with no sign of converging.
//
// The correct mechanism is a native Win32 OWNER relationship
// (SetWindowLongPtrW(previewHwnd, GWLP_HWNDPARENT, mainHwnd)) — NOT a
// WS_CHILD parent (the preview stays a real, independently focusable
// top-level window) and NOT SDL_SetWindowAlwaysOnTop (that pins the window
// above literally everything on the desktop, including unrelated apps,
// which the "when async is selected" qualifier rules out). A live 3-window
// probe against the real "windows" SDL driver confirmed, by walking the
// actual OS z-order (GetTopWindow/GetWindow(GW_HWNDNEXT), not SDL's opinion
// of itself): once the owner relationship is set, raising the main window
// pulls the preview directly above it and pushes down a previously-topmost
// UNRELATED third window, with ZERO SDL focus events posted for the preview
// at all — the preview's z-order moves but its focus state never does. The
// same probe also confirmed ownership alone does not force the preview
// above an unrelated window that's merely in the foreground while AsyncAO
// itself is backgrounded (owner-window z-order only "sticks" while the
// owner itself is foreground) — so this cannot regress into an unwanted
// always-on-top over other applications.
//
// syscall.NewLazyDLL avoids a new dependency (golang.org/x/sys is not in
// go.mod; adding it would need a written justification per CLAUDE.md) — the
// same idiom internal/ui/dpiseed_windows.go and internal/winexec already use
// for user32.dll.
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
//
// Reports true if the owner relationship was actually installed, false on
// any of: p/mainWin nil, either window closed, GetWMInfo unavailable (can
// fail — e.g. no native window handle exists yet), the SetWindowLongPtrW
// symbol missing (pre-existing OS unlikely on the 64-bit-only build this
// project ships, but checked rather than assumed), or the call itself
// failing. The caller (internal/ui's openDetachedPreview) does not act on
// the result — a failed link just leaves today's behavior (independent
// z-order, no focus theft either way) — but the bool lets tests pin real
// success against real failure without inspecting private state.
//
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
