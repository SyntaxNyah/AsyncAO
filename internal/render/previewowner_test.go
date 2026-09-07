package render

import "testing"

// TestPreviewWindowLinkOwnerDegradesSafely pins the "GetWMInfo can fail,
// degrade silently, never crash, never block the pop-out" guardrail for
// LinkOwner on every platform this project builds for — this file carries
// NO build tag, so it runs in CI's ubuntu-latest job too (unlike
// previewowner_windows_test.go's positive OS-truth assertion, which only
// runs on this Windows dev box). A nil receiver, a nil argument, and a
// never-opened PreviewWindow must all report failure (false) rather than
// panic, on both the real Win32 implementation and its !windows no-op
// sibling — the two files share this exact signature so callers (and this
// test) never have to know which one they're linked against.
func TestPreviewWindowLinkOwnerDegradesSafely(t *testing.T) {
	var nilPW *PreviewWindow
	if nilPW.LinkOwner(nil) {
		t.Error("LinkOwner on a nil *PreviewWindow reported true")
	}

	pw := NewPreviewWindow() // never opened: p.win is nil
	if pw.LinkOwner(nil) {
		t.Error("LinkOwner(nil) on a never-opened PreviewWindow reported true")
	}

	// Opened, but under the dummy driver (no real HWND on this backend) and
	// with a nil mainWin: still must not panic and must report false.
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	if err := pw.Open("linkowner-nilmain", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()
	if pw.LinkOwner(nil) {
		t.Error("LinkOwner(nil) on an open PreviewWindow reported true")
	}
}
