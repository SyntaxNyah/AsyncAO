//go:build windows && !race

package render

// EXCLUDED FROM THE RACE BUILD, MEASURED WHY: go-sdl2 v0.4.40's
// SysWMInfo.cptr() (sdl/syswm.go:109, the same helper GetWMInfo calls
// internally) does an unsafe.Pointer conversion that Go's checkptr
// instrumentation rejects as "converted pointer straddles multiple
// allocations" — a FATAL, unrecoverable runtime.throw, not a normal panic.
// checkptr is automatically enabled by `-race` (confirmed empirically on
// this box: `go run .` prints false, `go run -race .` prints true for a
// file gated on the `race` build tag — the same mechanism this file's tag
// relies on), so this is a go-sdl2 dependency limitation triggered by ANY
// call to a real Window.GetWMInfo() under -race, not something introduced
// by LinkOwner or fixable in this codebase. It was never hit before this
// task because GetWMInfo had no test exercising it with a real (non-dummy-
// driver) window; internal/ui/dpiseed_windows.go's own queryWindowDPI calls
// the identical GetWMInfo API and has no test file at all today. Production
// builds (build.ps1, a plain `go build`) never pass -race, so this cannot
// affect a shipped binary — only `go test -race` on this one test. The
// portable internal/render/previewowner_test.go and the internal/ui source
// gate (previewwindowowner_test.go) both stay in the race build; only the
// positive OS-truth assertion that must construct two real windows and
// call GetWMInfo on them is excluded.

import (
	"os"
	"syscall"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestPreviewWindowLinkOwnerSetsRealOwnerOnWindows drives the ACTUAL
// production method (LinkOwner) against two real native windows — the real
// "windows" SDL video driver, not the dummy driver every other test in this
// package uses, because the dummy driver has no real HWND for GetWMInfo to
// return and this seam is pure Win32 syscall plumbing that only exists on a
// real window. It then reads back OS TRUTH through a DIFFERENT Win32 call
// than the one LinkOwner itself uses (GetWindowLongPtrW here vs LinkOwner's
// own SetWindowLongPtrW) — this is verification, not a mirror of the fix's
// logic: it never looks at PreviewWindow's internal bookkeeping or
// re-implements the syscall, only asks Windows what it actually did.
//
// Matches internal/ui/dpiseed_windows.go's own class of exposure: this file
// only builds and runs on Windows, and .github/workflows/ci.yml's only
// `go test` job runs on ubuntu-latest, so this test never executes in CI —
// only locally, on this dev box, exactly like the DPI-seed code it mirrors
// the file-pair pattern from.
//
// HOW THIS FAILS IF THE FIX IS DELETED: revert LinkOwner's body to a no-op
// (or drop/comment out its SetWindowLongPtrW call) and GetWindowLongPtrW
// reads back 0 instead of the main window's HWND below — this test goes
// red. If LinkOwner were rewritten to set some Go-side bookkeeping field
// instead of making the real syscall, the same OS-truth readback still
// fails, because nothing here ever inspects PreviewWindow's fields to
// decide pass/fail.
func TestPreviewWindowLinkOwnerSetsRealOwnerOnWindows(t *testing.T) {
	prevDriver, hadPrevDriver := os.LookupEnv("SDL_VIDEODRIVER")
	os.Unsetenv("SDL_VIDEODRIVER") // force the real "windows" driver — see doc above
	defer func() {
		if hadPrevDriver {
			os.Setenv("SDL_VIDEODRIVER", prevDriver)
		} else {
			os.Unsetenv("SDL_VIDEODRIVER")
		}
	}()

	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer sdl.Quit()

	// WINDOW_HIDDEN: SDL still creates the underlying native window (a real
	// HWND) and only skips ShowWindow, so this never flashes anything
	// visible on screen during a test run.
	mainWin, err := sdl.CreateWindow("linkowner-test-main", 0, 0, 64, 64, sdl.WINDOW_HIDDEN)
	if err != nil {
		t.Skipf("main window unavailable: %v", err)
	}
	defer mainWin.Destroy()

	pw := NewPreviewWindow()
	if err := pw.Open("linkowner-test-preview", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	if !pw.LinkOwner(mainWin) {
		t.Fatal("LinkOwner reported failure against two real native windows")
	}

	mainInfo, err := mainWin.GetWMInfo()
	if err != nil || mainInfo == nil {
		t.Skipf("main window HWND unavailable: %v", err)
	}
	mainHWND := uintptr(mainInfo.GetWindowsInfo().Window)

	previewInfo, err := pw.win.GetWMInfo()
	if err != nil || previewInfo == nil {
		t.Skipf("preview window HWND unavailable: %v", err)
	}
	previewHWND := uintptr(previewInfo.GetWindowsInfo().Window)

	// A fresh LazyDLL proc, deliberately NOT the one LinkOwner itself calls
	// (SetWindowLongPtrW) — GetWindowLongPtrW is the read-side counterpart,
	// so this is independent OS-truth verification, not a re-assertion of
	// the same call the fix already made.
	user32 := syscall.NewLazyDLL("user32.dll")
	procGetWindowLongPtrW := user32.NewProc("GetWindowLongPtrW")
	if procGetWindowLongPtrW.Find() != nil {
		t.Skip("GetWindowLongPtrW unavailable on this OS")
	}
	idx := int32(gwlpHwndParent) // through a variable: converting the negative constant directly overflows uint64 at compile time
	got, _, _ := procGetWindowLongPtrW.Call(previewHWND, uintptr(uint64(int64(idx))))
	if got != mainHWND {
		t.Fatalf("GWLP_HWNDPARENT readback = %x, want the main window's HWND %x — LinkOwner did not "+
			"actually install the OS-level owner relationship", got, mainHWND)
	}
}
