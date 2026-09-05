package main

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/ui"
)

// eventWindowID returns the WindowID carried by ev and ok=true, for every
// SDL event type go-sdl2 v0.4.40 defines a WindowID field for. ok=false for
// every other type (QuitEvent, joystick/controller events, …), which this
// loop never has to route by window because they aren't addressed to any
// one window. Extracted as its own function — rather than inlined into
// eventForPreviewWindow — because it is the one place that has to be kept in
// sync with go-sdl2's event set; everything downstream just asks "does this
// id match".
func eventWindowID(ev sdl.Event) (id uint32, ok bool) {
	switch e := ev.(type) {
	case *sdl.WindowEvent:
		return e.WindowID, true
	case *sdl.KeyboardEvent:
		return e.WindowID, true
	case *sdl.TextEditingEvent:
		return e.WindowID, true
	case *sdl.TextInputEvent:
		return e.WindowID, true
	case *sdl.MouseMotionEvent:
		return e.WindowID, true
	case *sdl.MouseButtonEvent:
		return e.WindowID, true
	case *sdl.MouseWheelEvent:
		return e.WindowID, true
	case *sdl.DropEvent:
		return e.WindowID, true
	case *sdl.UserEvent:
		return e.WindowID, true
	}
	return 0, false
}

// eventForPreviewWindow reports whether ev is addressed to the preview
// window identified by previewWindowID, so the run loop's event dispatch can
// route it to the preview's own handler instead of uiCtx.HandleEvent.
//
// previewWindowID == 0 means the preview is closed (PreviewWindow.ID's
// documented sentinel — 0 is never a real SDL window id), and every event
// then routes to the main Ctx exactly as before a preview window could
// exist: the overwhelmingly common case costs one uint32 comparison against
// a constant and nothing else.
func eventForPreviewWindow(ev sdl.Event, previewWindowID uint32) bool {
	if previewWindowID == 0 {
		return false
	}
	id, ok := eventWindowID(ev)
	return ok && id == previewWindowID
}

// dispatchEvent is the run loop's ENTIRE per-event routing decision between
// the preview window and the main Ctx, extracted into a named function for
// the same reason mainWindowShouldQuit is (quit.go): so it is testable
// without a live event loop or a live window, and so a test can drive the
// REAL function the loop calls rather than a re-implementation of it.
//
// THE COORDINATE-ALIASING GATE. ui.Ctx.HandleEvent writes every
// MouseMotionEvent/MouseButtonEvent's raw .X/.Y straight into the shared
// c.mouseX/c.mouseY with no window-origin check of its own (ui.go) — and
// SDL's mouse coordinates are window-CLIENT-relative, so an ungated second
// window's mouse motion would silently alias onto the main window's
// hit-testing space and produce phantom hovers/clicks. Routing a
// preview-window event to preview.HandleEvent INSTEAD of uiCtx.HandleEvent —
// never both — is what keeps that from happening. It reports whether the
// event was the preview's, so callers doing further main-window-only
// bookkeeping (display-change / focus / file-drop handling in run()) know to
// skip it too: those all read the MAIN window's own state and would at best
// waste a re-check, at worst mis-scope, if a preview-only event reached them.
func dispatchEvent(ev sdl.Event, uiCtx *ui.Ctx, preview *render.PreviewWindow) (consumedByPreview bool) {
	if eventForPreviewWindow(ev, preview.ID()) {
		preview.HandleEvent(ev)
		return true
	}
	uiCtx.HandleEvent(ev)
	return false
}
