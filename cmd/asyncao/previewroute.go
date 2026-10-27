package main

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
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
func dispatchEvent(ev sdl.Event, uiCtx *ui.Ctx, preview *render.PreviewWindow) (consumedByPreview bool) {
	if eventForPreviewWindow(ev, preview.ID()) {
		preview.HandleEvent(ev)
		return true
	}
	uiCtx.HandleEvent(ev)
	return false
}
