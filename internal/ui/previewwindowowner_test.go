package ui

// The preview pop-out's z-order wiring: "popped out emote preview should
// have shared priority with main async window, i.e. moving on top of other
// windows when async is selected". The actual mechanism (a Win32 owner
// relationship) lives in render.PreviewWindow.LinkOwner — see its doc for
// the measured reasoning (a focus-hook approach was tried live and refuted
// first: Window.Raise() also steals input focus, so it either steals focus
// back onto the preview or, bidirectionally, ping-pongs focus forever).
//
// This file only pins that internal/ui's ONE production call site
// (openDetachedPreview, the sole caller of Preview.Open/OpenAtClamped in
// the whole repo) actually reaches LinkOwner after a successful open. It is
// a source-level gate, the same style as srcgate_test.go's
// containsCall/funcBodySource helpers already used elsewhere in this
// package (e.g. emotepreviewfollow_test.go's
// TestEveryEmotePickImplementationFollowsThePinnedPreview): it reads
// PRODUCTION source and contains none of LinkOwner's own syscall logic, so
// it cannot go green against a re-implementation, and it runs on every
// platform (no build tag, no SDL init) because it never touches a real
// window — the Win32-specific behavior itself is pinned separately in
// internal/render/previewowner_windows_test.go against real OS truth.

import "testing"

// TestOpenDetachedPreviewLinksTheWindowOwner drives the real source of
// openDetachedPreview and demands it call LinkOwner. Deleting the
// `a.d.Preview.LinkOwner(a.ctx.win)` line this task adds (or renaming it
// away) makes this test fail loudly; nothing here reimplements what
// LinkOwner does, only that openDetachedPreview calls it.
func TestOpenDetachedPreviewLinksTheWindowOwner(t *testing.T) {
	body := funcBodySource(t, "previewwindow.go", "openDetachedPreview")
	if !containsCall(body, "LinkOwner") {
		t.Fatal("openDetachedPreview never calls LinkOwner — a popped-out preview would keep its " +
			"own independent z-order instead of following the main window (the whole point of this seam)")
	}
}
