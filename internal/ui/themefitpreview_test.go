package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// TestThemeFitPreviewHintOnlyWithADesign: a theme without courtroom_design.ini shows its notice alone, not the
// drag-to-pan hint on top of it (#153).
func TestThemeFitPreviewHintOnlyWithADesign(t *testing.T) {
	const boxW, boxH = 560, 340
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()
	box := sdl.Rect{W: boxW, H: boxH}

	delete(a.themeRects, ao2CanvasKey)
	a.ctx.purgeTextCache()
	a.drawThemeFitPreview(box)
	if !textCacheHasLabel(a.ctx, themeFitNoDesignText) {
		t.Fatal("no notice for a theme without a design")
	}
	if textCacheHasLabel(a.ctx, themeFitPreviewHint) {
		t.Error("the drag-to-pan hint is drawn over the no-design notice")
	}

	a.themeRects[ao2CanvasKey] = a.themeRectsOrig[ao2CanvasKey]
	a.ctx.purgeTextCache()
	a.drawThemeFitPreview(box)
	if !textCacheHasLabel(a.ctx, themeFitPreviewHint) {
		t.Error("the drag-to-pan hint is missing for a theme with a design")
	}
}
