package ui

// THE EDITOR'S PANEL FENCE (v1.90.0 W7b) — docs/wip/THEME-EDITOR-DESIGN.md §3.1.
//
// THE DEFECT THESE GATES CLOSE. The Fonts panel shipped with NO input fence at all.
// editorFontPanelRect centres it INSIDE editorCanvasRect, and editorCanvasInput gates
// only on raw pointIn — so pressing any `<` / `>` / `x` on a font row ALSO ran
// editorProbe and editorBeginGesture on whatever element or AO2 widget sat beneath the
// panel: the art moved while a face was being picked. Below ~928 px the panel overlaps
// a rail outright, and there the rail's own ClickedIn saw the same press.
//
// Both halves are driven through App.Frame, never by calling the fence's predicate and
// asserting on it — a gate that checked editorPanelUp() would be a mirror of the fix
// (rule 11's mirror clause) and would stay green with both call sites deleted.

import (
	"strings"
	"testing"
)

// (A committed click is the frame harness's own driveClickAt: the kit sets c.clicked on
// the mouse-UP edge, which driveMouse deliberately never synthesises, so a ClickedIn /
// Button gate needs that spelling and a drag gate needs this one.)

// editorEyeToggleAt is the eye hit box of element rail row idx, in the rail's own
// arithmetic (drawEditorElementRail's body, then drawEditorElementRow's toggles).
//
// The coordinates are duplicated rather than exported, and the gate that uses them
// PROVES them first with the panel closed — a stale coordinate would otherwise make the
// fenced half pass vacuously by missing the row entirely.
func editorEyeToggleAt(idx int32) (x, y int32) {
	rowY := int32(editBannerH+editRowH) + idx*editRowH
	lockX := int32(editRailW - editToggleW - editRowPad)
	x = lockX - editToggleW - 2 + editToggleW/2
	y = rowY + editRowH/2
	return x, y
}

// TestAnOpenPanelFencesTheCanvasUnderIt is the headline half: a press inside the panel
// must not reach the canvas at all.
//
// The assertion is on the SELECTION and the GESTURE rather than on one element's rect,
// because that covers every shape the hole could take: with the fence missing the press
// either starts a drag on the box under the panel, or re-selects some AO2 widget there,
// or finds nothing and CLEARS the selection. All three are visible failures of "the
// surface behind a panel is click-dead"; all three fail this gate.
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
//
// Phase 1 (panel closed) proves the click really lands on the eye toggle. Phase 2 (panel
// open) proves the same click is dead. Design §3.1 fences the whole surface behind a
// panel, not merely the pixels it covers, which is why this holds at 1280 px where the
// panel and the rails do not overlap.
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
// the header draws before it is raised, so Undo/Redo/Save/Back/Fonts stay live with a
// panel open — including the button that closes it.
//
// That is only safe while the panel cannot reach the band, so the invariant is swept
// rather than assumed, at the window sizes the client actually runs at.
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

// TestBothHalvesOfTheFenceAskOnePredicate is the seam census.
//
// The two behavioural gates above fail if either call site is deleted. This one fails if
// a call site is REWRITTEN to ask the question in its own words — an inline
// `a.te.fontsOpen` in one place and editorPanelUp in the other is two predicates that
// agree today and drift the day W8 adds the preset gallery, which is exactly how the
// canvas ends up fenced against one panel and not the next.
func TestBothHalvesOfTheFenceAskOnePredicate(t *testing.T) {
	for _, decl := range []string{
		"func (a *App) drawThemeEditor(",
		"func (a *App) editorCanvasInput(",
	} {
		if body := funcBodyInPackage(t, decl); !strings.Contains(body, "editorPanelUp()") {
			t.Errorf("%q no longer asks editorPanelUp() — both halves of the panel fence must ask ONE "+
				"predicate, or a panel added later is fenced on one side only", decl)
		}
	}
	// The draw half must also RESTORE what it raised rather than assigning false: an open
	// dropdown belonging to the rails holds modalOn legitimately, and clearing it on the
	// way out would drop that fence and let the click behind it through.
	body := funcBodyInPackage(t, "func (a *App) drawThemeEditor(")
	if !strings.Contains(body, "c.modalOn = savedModal") {
		t.Error("drawThemeEditor no longer restores the modal fence it raised — release-and-restore is " +
			"the pattern (design §3.1); a bare `= false` drops a fence somebody else was holding")
	}
}
