package main

import (
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ui"
)

// eventWindowID returns the WindowID of ev, if any; it must be kept in sync with internal/sdl's event set.
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

// eventForPreviewWindow reports whether ev is addressed to the preview window.
func eventForPreviewWindow(ev sdl.Event, previewWindowID uint32) bool {
	if previewWindowID == 0 {
		return false
	}
	id, ok := eventWindowID(ev)
	return ok && id == previewWindowID
}

// dispatchEvent routes between the preview window and the main Ctx, separate so tests drive the real function.
func dispatchEvent(ev sdl.Event, uiCtx *ui.Ctx, preview *render.PreviewWindow) (consumedByPreview bool) {
	if eventForPreviewWindow(ev, preview.ID()) {
		preview.HandleEvent(ev)
		return true
	}
	uiCtx.HandleEvent(ev)
	return false
}
