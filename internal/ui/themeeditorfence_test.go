package ui

// THE EDITOR'S PANEL FENCE (v1.90.0 W7b) — docs/wip/THEME-EDITOR-DESIGN.md §3.1.

import (
	"testing"
)

// (A committed click is the frame harness's own driveClickAt: the kit sets c.clicked on
// the mouse-UP edge, which driveMouse deliberately never synthesises, so a ClickedIn /
// Button gate needs that spelling and a drag gate needs this one.)

// editorEyeToggleAt is the eye hit box of element rail row idx, in the rail's own
// arithmetic (drawEditorElementRail's body, then drawEditorElementRow's toggles).
func editorEyeToggleAt(idx int32) (x, y int32) {
	rowY := int32(editBannerH+editRowH) + idx*editRowH
	lockX := int32(editRailW - editToggleW - editRowPad)
	x = lockX - editToggleW - 2 + editToggleW/2
	y = rowY + editRowH/2
	return x, y
}

// TestAnOpenPanelFencesTheCanvasUnderIt is the headline half: a press inside the panel
// must not reach the canvas at all.
func TestAnOpenPanelFencesTheCanvasUnderIt(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	tgt := tgts[0]
	a.te.selectOnly(tgt)

	panel := editorFontPanelRect(frameHarnessW, frameHarnessH)
	canvas := editorCanvasRect(frameHarnessW, frameHarnessH)
	px, py := panel.X+panel.W/2, panel.Y+panel.H/2
	if !pointIn(px, py, canvas) {
		t.Fatalf("the fixture panel centre (%d,%d) is outside the canvas rect %+v — this gate would "+
			"pass vacuously, because the hole it covers is precisely the panel sitting INSIDE the "+
			"canvas's own input region", px, py, canvas)
	}

	a.te.fontsOpen = true
	driveMouse(a, px, py, true) // press, still held: a gesture would be live right now

	if a.te == nil {
		t.Fatal("a press on the font panel closed the editor")
	}
	if a.te.drag.active() {
		t.Error("a press on the font panel started a CANVAS gesture — picking a face moved the art")
	}
	if a.te.selN != 1 || a.te.sel[0] != tgt {
		t.Errorf("a press on the font panel left the selection at %d target(s) (first %+v), want the one "+
			"element that was selected before it — the press reached editorProbe through the panel",
			a.te.selN, a.te.sel[0])
	}
	driveMouse(a, px, py, false)
}

// TestAnOpenPanelFencesTheRailsBehindIt is the other half, and it is a CONTROL plus a
// TREATMENT so it cannot pass by missing its target.
func TestAnOpenPanelFencesTheRailsBehindIt(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	idx, ok := tgts[0].elemIdx()
	if !ok {
		t.Fatal("the fixture target is not an element")
	}
	el := a.te.doc.element(tgts[0])
	if el == nil {
		t.Fatal("the fixture element vanished")
	}
	if el.Hidden {
		t.Fatal("fixture: the probe element must start visible")
	}
	x, y := editorEyeToggleAt(int32(idx))

	// CONTROL: the coordinate is live.
	driveClickAt(a, x, y)
	if !a.te.doc.element(tgts[0]).Hidden {
		t.Fatalf("clicking the eye at (%d,%d) with no panel open did not hide row %d — the gate below "+
			"would pass without proving anything", x, y, idx)
	}

	// TREATMENT: the same click, with the panel up, must do nothing.
	a.te.fontsOpen = true
	driveClickAt(a, x, y)
	if !a.te.doc.element(tgts[0]).Hidden {
		t.Error("a rail row toggled while a panel was open — the surface behind a panel must be " +
			"click-dead, or one press works two controls")
	}
	// And the fence must be RELEASED, not stranded: close the panel and the rail is live
	// again. A fence that persists across frames freezes the whole screen (the emoji
	// picker's own documented failure).
	a.te.fontsOpen = false
	driveClickAt(a, x, y)
	if a.te.doc.element(tgts[0]).Hidden {
		t.Error("the rail stayed dead after the panel closed — the fence was stranded rather than restored")
	}
}

// TestThePanelNeverCoversTheHeaderBand pins the ONE deliberate exception in the fence:
func TestThePanelNeverCoversTheHeaderBand(t *testing.T) {
	for _, wh := range [][2]int32{
		{640, 480}, {800, 600}, {1024, 576}, {1280, 720}, {1920, 1080}, {3840, 2160},
	} {
		r := editorFontPanelRect(wh[0], wh[1])
		if r.Y < editBannerH {
			t.Errorf("at %dx%d the font panel starts at y=%d, inside the %d px header band — the header "+
				"is drawn OUTSIDE the fence, so a panel that reached it would put two live controls "+
				"under one press", wh[0], wh[1], r.Y, editBannerH)
		}
	}
}
