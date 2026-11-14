package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Issue #61: the page arrows show only where they can move, as AO2's emote_left and emote_right (emotes.cpp:110-147).
func TestEmotePageArrowsHideAtTheEnds(t *testing.T) {
	const (
		targetW, targetH = int32(480), int32(240)
		emoteCount       = 60
		nextArrowX       = int32(34)
		inset            = int32(3)
		// gapX is between ">" (ending at 64) and the page counter (from 72), where nothing draws.
		gapX = int32(67)
	)
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	ct, err := render.NewCaptureTarget(ren, targetW, targetH)
	if err != nil {
		t.Skipf("capture target unavailable: %v", err)
	}
	defer ct.Close()

	a := testTabApp(t)
	a.ctx = ctx
	a.d.Prefs.Update(func(sn *config.Settings) { sn.EmoteButtonImages = false }) // text cells need no texture store
	a.emotes = make([]courtroom.Emote, emoteCount)
	for i := range a.emotes {
		a.emotes[i].Comment = "emote"
	}
	ctx.mouseX, ctx.mouseY = -targetW, -targetH
	grid := sdl.Rect{W: targetW, H: targetH}
	arrowY := grid.Y + grid.H - btnH

	drawn := func(x int32) bool {
		frame, err := ct.Capture(ren, func(sdl.Rect) { a.drawEmoteRow(grid, grid) })
		if err != nil {
			t.Skipf("offscreen capture unavailable headlessly: %v", err)
		}
		return frame.RGBAAt(int(x+inset), int(arrowY+inset)) != frame.RGBAAt(int(grid.X+gapX), int(arrowY+inset))
	}
	if !a.emoteGridScrolls() {
		_ = drawn(0) // the first draw stamps the grid metrics
	}
	if !a.emoteGridScrolls() {
		t.Fatal("fixture must need more than one page")
	}
	if drawn(grid.X) {
		t.Error("\"<\" shows on the first page, where it can't move")
	}
	if !drawn(grid.X + nextArrowX) {
		t.Error("\">\" is missing on the first page")
	}
	a.scrollEmoteRows(emoteCount)
	if !drawn(grid.X) {
		t.Error("\"<\" is missing on the last page")
	}
	if drawn(grid.X + nextArrowX) {
		t.Error("\">\" shows on the last page, where it can't move")
	}
}
