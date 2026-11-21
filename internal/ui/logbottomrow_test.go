package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Issue #116: a log row the bottom edge cuts in half still draws, as the top edge's half row does.
func TestOOCLogDrawsHalfVisibleBottomRow(t *testing.T) {
	const (
		targetW, targetH = int32(400), int32(200)
		visibleRows      = 2
		lineCount        = 6
		listX, listY     = int32(10), int32(10)
		listW            = int32(300)
		pointerAway      = int32(-1000)
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
	a.oocPct = DefaultScalePct
	for range lineCount {
		a.pushOOC("MMMMMMMMMMMMMMMM", "", false)
	}
	lineH := int32(a.elemFont(elemServerChatlog, a.oocPct).Height()) + logRowGapPx
	list := sdl.Rect{X: listX, Y: listY, W: listW, H: visibleRows*lineH + lineH/2}
	a.oocScroll, a.oocStick = 0, false
	ctx.mouseX, ctx.mouseY = pointerAway, pointerAway

	frame, err := ct.Capture(ren, func(sdl.Rect) { a.drawOOCLogList(list, false) })
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	bg := frame.RGBAAt(int(targetW-1), int(targetH-1))
	for y := list.Y + visibleRows*lineH; y < list.Y+list.H; y++ {
		for x := list.X; x < list.X+list.W/2; x++ {
			if frame.RGBAAt(int(x), int(y)) != bg {
				return
			}
		}
	}
	t.Fatalf("no ink in the half-visible bottom row (y %d..%d)", list.Y+visibleRows*lineH, list.Y+list.H)
}
