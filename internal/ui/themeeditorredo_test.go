package ui

// The REDO affordance, both of its doors.
//
// Redo is a shipped feature with two spellings (Ctrl+Y, Ctrl+Shift+Z) and a header
// button, and the ring publishes canUndo/canRedo to say whether either can do
// anything right now. canRedo shipped UNCALLED: the header drew a live-looking Redo
// over an empty tail, and nothing in the suite would have noticed if the tail had
// stopped being reachable at all. These gates are that hole, closed from three
// sides — the chord through the real event path, the button through a real frame,
// and a census so the guard cannot be quietly unwired again.

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// driveCtrlShiftKey delivers ONE real Ctrl+Shift key press, at the outermost level a
// test can reach: SDL's own modifier state (BeginFrame derives ctrlHeld/shiftHeld
// from it, exactly as the live loop does), Ctx.HandleEvent to fold the chord into
// c.hotkey — there is no Ctrl+Shift+Z keysym to stamp, HandleEvent collapses every
// non-clipboard Ctrl combination onto one keysym plus the modifier flags (#96) — and
// then App.Frame, which is the only caller of editorUndoChord.
func driveCtrlShiftKey(t *testing.T, a *App, key sdl.Keycode) {
	t.Helper()
	sdl.SetModState(sdl.KMOD_LCTRL | sdl.KMOD_LSHIFT)
	defer sdl.SetModState(sdl.KMOD_NONE)
	a.ctx.BeginFrame(frameHarnessDt)
	if !a.ctx.ctrlHeld || !a.ctx.shiftHeld {
		t.Skip("this SDL build does not honour SetModState; the chord gates need it")
	}
	a.ctx.HandleEvent(&sdl.KeyboardEvent{
		Type:   sdl.KEYDOWN,
		Keysym: sdl.Keysym{Sym: key, Mod: sdl.KMOD_LCTRL | sdl.KMOD_LSHIFT},
	})
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// editorHeaderBtnCentre is the middle of the n-th header button counting from the
// RIGHT edge (0 = Back), derived from the band's own published constants so a
// re-spaced row moves the press with it instead of silently missing.
func editorHeaderBtnCentre(w int32, fromRight int32) (int32, int32) {
	x := w - editRowPad - editorHeaderBtnW - fromRight*(editorHeaderBtnW+editorHeaderBtnGap)
	return x + editorHeaderBtnW/2, editBannerH / 2
}

// editorHeaderRedoFromRight / editorHeaderUndoFromRight are those two buttons'
// places in the row: Back, Save, Redo, Undo, laid out from the right edge
// (drawEditorHeader).
const (
	editorHeaderRedoFromRight = 2
	editorHeaderUndoFromRight = 3
)

// TestRedoRidesTheRealKeyDispatch proves the feature end to end before anything
// gates it: an edit, Ctrl+Z, Ctrl+Shift+Z, and the value is back — through SDL's
// event, the kit's HandleEvent, App.Frame and the chord claimer, with nothing called
// by hand.
//
// TestEditorRedoRidesCtrlShiftZ pins the same spelling one layer in (it stamps
// c.hotkey and calls editorUndoChord itself, which is what lets it run without a
// window). This one is the layer OUT: it would fail if App.Frame stopped claiming
// the chord pre-screen, or if HandleEvent stopped routing Ctrl+Z to c.hotkey at all
// — neither of which the inner gate can see.
func TestRedoRidesTheRealKeyDispatch(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	tgt := tgts[0]

	base, ok := a.te.doc.read(tgt, editFieldZ)
	if !ok {
		t.Fatal("the fixture element has no z to edit")
	}
	if !a.editorApply(mutateElemField(tgt, editFieldZ, editValueInt(base.i[0]+3))) {
		t.Fatal("the fixture edit did nothing — there would be nothing to undo")
	}
	edited, _ := a.te.doc.read(tgt, editFieldZ)

	driveCtrlShiftKey(t, a, sdl.K_z) // Ctrl+SHIFT+Z on an untouched tail is a no-op…
	if got, _ := a.te.doc.read(tgt, editFieldZ); got != edited {
		t.Fatalf("a redo with nothing undone moved z to %d, want the edit's %d", got.i[0], edited.i[0])
	}

	driveHotkey(a, sdl.K_z) // …plain Ctrl+Z steps back…
	if got, _ := a.te.doc.read(tgt, editFieldZ); got != base {
		t.Fatalf("Ctrl+Z left z at %d, want the pre-edit %d", got.i[0], base.i[0])
	}
	if !a.te.ring.canRedo() {
		t.Fatal("the ring reports nothing to redo immediately after an undo — the tail is gone")
	}

	driveCtrlShiftKey(t, a, sdl.K_z) // …and Ctrl+Shift+Z steps forward again.
	if got, _ := a.te.doc.read(tgt, editFieldZ); got != edited {
		t.Fatalf("Ctrl+Shift+Z left z at %d, want the redone %d — the redo tail is unreachable "+
			"through the real key path", got.i[0], edited.i[0])
	}
	if a.te.ring.canRedo() {
		t.Error("the ring still offers a redo after the tail was consumed")
	}
}

// TestTheHeaderRedoButtonStepsTheTimeline drives the OTHER door — the band's own
// button, pressed through a real frame at the geometry drawEditorHeader publishes.
//
// It is the gate that would catch a guard wired backwards: editorHeaderBtn swallows
// the click when the action is unavailable, so a `!canRedo()` (or a canUndo() copied
// into the Redo row) makes the button refuse the one press that is legal, and this
// fails on the value.
func TestTheHeaderRedoButtonStepsTheTimeline(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	tgt := tgts[0]

	base, _ := a.te.doc.read(tgt, editFieldZ)
	if !a.editorApply(mutateElemField(tgt, editFieldZ, editValueInt(base.i[0]+5))) {
		t.Fatal("the fixture edit did nothing")
	}
	edited, _ := a.te.doc.read(tgt, editFieldZ)

	// Undo through the band's Undo button, which must be live now.
	if !a.te.ring.canUndo() {
		t.Fatal("the ring reports nothing to undo after an edit")
	}
	ux, uy := editorHeaderBtnCentre(frameHarnessW, editorHeaderUndoFromRight)
	driveClickAt(a, ux, uy)
	if got, _ := a.te.doc.read(tgt, editFieldZ); got != base {
		t.Fatalf("the header's Undo left z at %d, want the pre-edit %d — the press missed the "+
			"button, or the gate refused a legal undo", got.i[0], base.i[0])
	}

	// …and forward again through Redo.
	if !a.te.ring.canRedo() {
		t.Fatal("nothing to redo after an undo")
	}
	rx, ry := editorHeaderBtnCentre(frameHarnessW, editorHeaderRedoFromRight)
	driveClickAt(a, rx, ry)
	if got, _ := a.te.doc.read(tgt, editFieldZ); got != edited {
		t.Fatalf("the header's Redo left z at %d, want the redone %d", got.i[0], edited.i[0])
	}

	// Pressing it again with the tail spent changes nothing and leaves the editor up:
	// a disabled button is refused, never a crash and never a second step.
	driveClickAt(a, rx, ry)
	if a.te == nil {
		t.Fatal("a press on the exhausted Redo button released the editor")
	}
	if got, _ := a.te.doc.read(tgt, editFieldZ); got != edited {
		t.Errorf("a second Redo press moved z to %d — the timeline stepped past its own end", got.i[0])
	}
}

// TestADisabledHeaderButtonSwallowsItsClick drives editorHeaderBtn itself, both
// arms, under an identical press. Without this the gate above cannot tell "the guard
// refused the click" from "the action had nothing to do": both leave the document
// alone, and only one of them is the affordance.
func TestADisabledHeaderButtonSwallowsItsClick(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a) // the kit needs one real frame before it will answer a hover

	r := sdl.Rect{X: 100, Y: 2, W: editorHeaderBtnW, H: editBannerH - 4}
	press := func(enabled bool) bool {
		a.ctx.BeginFrame(frameHarnessDt)
		a.ctx.mouseX, a.ctx.mouseY = r.X+r.W/2, r.Y+r.H/2
		a.ctx.downX, a.ctx.downY = a.ctx.mouseX, a.ctx.mouseY
		a.ctx.clicked = true
		return a.editorHeaderBtn(r, "Redo", enabled)
	}
	if !press(true) {
		t.Fatal("an ENABLED header button did not report a committed click on its own rect — " +
			"the gate below would pass for the wrong reason")
	}
	if press(false) {
		t.Error("a DISABLED header button reported a click — the refusal has to happen at the " +
			"button, or every caller has to remember to re-ask whether its action was legal")
	}
}

// TestTheHeaderAsksTheRingWhetherItMayStep is the wiring catcher, and it is the one
// that keeps this from being a decoration.
//
// The enabled state is a SOURCE-LEVEL fact: with the guard deleted, both buttons draw
// live, every behavioural gate above still passes (an unavailable step is a no-op
// either way) and canRedo goes back to being unused — which is precisely the state
// this batch found it in. A census is the only witness for a call that was never
// written.
func TestTheHeaderAsksTheRingWhetherItMayStep(t *testing.T) {
	body := funcBodyInPackage(t, "func (a *App) drawEditorHeader(")
	for _, want := range []string{
		`a.editorHeaderBtn(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Redo", a.te.ring.canRedo())`,
		`a.editorHeaderBtn(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Undo", a.te.ring.canUndo())`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("drawEditorHeader no longer draws\n\t%s\nThe two timeline buttons are what "+
				"themeEditRing.canUndo/canRedo exist for; drawn through Ctx.Button they claim an "+
				"action the ring cannot perform, and the predicate goes dead again.", want)
		}
	}
	// And neither may go back to the ungated widget: c.Button draws AND answers, so a
	// stray call here is a live button whatever the ring says.
	for _, forbidden := range []string{`c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Redo")`,
		`c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Undo")`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("drawEditorHeader draws %s again — that widget cannot be disabled", forbidden)
		}
	}
}
