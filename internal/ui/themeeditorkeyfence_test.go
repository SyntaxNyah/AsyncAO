package ui

// THE EDITOR'S KEYS YIELD TO A MODAL (v1.90.0 W10).
//
// THE HOLE. drawThemeEditor raises c.modalOn for the embedded file browser BEFORE the
// key handler runs, and its comment says so — but modalOn only gates hovering(), so the
// raise buys the POINTER half its protection and the keyboard half none. With the
// browser up over the editor, DELETE deleted the selected element, H and L toggled
// hidden and locked, and the arrows nudged it, all underneath a panel that owns the
// screen. The pointer half was covered separately (`if demoBrowser.open { return }` in
// editorCanvasInput); the keys were not, and could not have been before this wave,
// because nothing modal could be opened over this screen until the image intake was.
//
// AND IT SPENDS THE KEY. handleEditorKeys zeroes c.keyPressed on every arm it takes, so
// the loss is not only a stray edit: a modal that later wants Escape, or grows a text
// field, would be handed a key the editor had already eaten. The browser reads no keys
// today and has no field — c.focusID stays empty — which is exactly why the existing
// focused-field yield does not cover this.
//
// The gate drives REAL FRAMES through App.Frame, so it fails if the guard is deleted
// and it cannot go green against a re-implementation of it.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// openBrowserOverEditor puts the in-app file browser up in the image-intake purpose the
// editor opens it in, and takes it back down when the test ends.
//
// The state is set rather than opened through openDemoBrowserFor because the FENCE is
// what is under test and the fence reads demoBrowser.open — while the real open path
// spawns a directory-listing goroutine over the user's home folder, which would put a
// machine-dependent disk walk inside a gate about a keystroke. demoBrowser is a
// package-level singleton (resetContentPanel documents the same discipline), so the
// cleanup is not optional.
func openBrowserOverEditor(t *testing.T) {
	t.Helper()
	demoBrowser.open = true
	demoBrowser.purpose = purposeThemeImage
	t.Cleanup(func() {
		demoBrowser.open = false
		demoBrowser.purpose = purposeVideo
	})
}

// driveEditorKey runs ONE real frame carrying a plain key press, seeded where
// HandleEvent seeds it: after BeginFrame (which clears the per-frame input snapshot)
// and before App.Frame.
//
// ON c.keyPressed, never c.hotkey, which is the opposite of driveHotkey and is the
// distinction handleEditorKeys itself is built on — its Ctrl chords are claimed
// pre-screen, and everything it reads in-draw is a PLAIN key.
func driveEditorKey(a *App, key sdl.Keycode) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.keyPressed = key
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// TestAModalOverTheEditorTakesTheKeys is TREATMENT then CONTROL, in that order because
// the treatment is the destructive one: if the fence leaks, the element is gone and the
// control below has nothing left to prove itself with.
func TestAModalOverTheEditorTakesTheKeys(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0}, [2]int{editorKeyFenceGapPx, 0})
	defer cleanup()
	a.te.selectOnly(tgts[0])
	before := a.te.doc.elementCount()
	if before < 2 {
		t.Fatalf("the fixture staged %d elements, want 2 — the control half needs one to spare", before)
	}

	// TREATMENT: the browser owns the frame, so the editor's key map must not run.
	openBrowserOverEditor(t)
	driveEditorKey(a, sdl.K_DELETE)
	if a.te == nil {
		t.Fatal("a keystroke under the file browser closed the editor")
	}
	if got := a.te.doc.elementCount(); got != before {
		t.Errorf("DELETE under the open file browser removed an element (%d → %d) — the surface behind a "+
			"modal must be dead to the KEYBOARD as well as the pointer, or one press edits a document the "+
			"user cannot even see", before, got)
	}
	// The other half of the same claim: the key is not merely harmless, it is UNSPENT.
	// handleEditorKeys zeroes c.keyPressed on the arms it takes, so a leak would also
	// swallow the keystroke on the way past — leaving the modal with nothing to read.
	if a.ctx.keyPressed != sdl.K_DELETE {
		t.Errorf("c.keyPressed = %d after a frame under the modal, want it untouched (%d) — the editor "+
			"consumed a key that belongs to whatever owns the frame", a.ctx.keyPressed, sdl.K_DELETE)
	}

	// The two other plain keys the map claims, for the same reason: hidden and locked
	// are document state, and a browser press must not have flipped either.
	el := a.te.doc.element(tgts[0])
	if el == nil {
		t.Fatal("the selected element vanished under the modal")
	}
	if el.Hidden || el.Locked {
		t.Errorf("hidden=%v locked=%v before any H/L was pressed — the fixture is not clean", el.Hidden, el.Locked)
	}
	for _, k := range []sdl.Keycode{sdl.K_h, sdl.K_l} {
		driveEditorKey(a, k)
	}
	if el = a.te.doc.element(tgts[0]); el == nil || el.Hidden || el.Locked {
		t.Error("H / L reached the editor under the open file browser — the whole plain-key map has to yield " +
			"to a modal, not just the destructive arm of it")
	}

	// CONTROL: with the browser down the very same key must work, or everything above
	// passed by pressing a key that does nothing anyway.
	demoBrowser.open = false
	driveEditorKey(a, sdl.K_DELETE)
	if got := a.te.doc.elementCount(); got != before-1 {
		t.Fatalf("with no modal open DELETE left %d elements, want %d — the treatment half of this gate "+
			"proved nothing, because the keystroke never deleted anything in the first place", got, before-1)
	}
}

// editorKeyFenceGapPx separates the fixture's two elements in design px. Larger than
// editorGateBoxPx so the two boxes cannot overlap, which keeps the selection this gate
// makes unambiguous.
const editorKeyFenceGapPx = editorGateBoxPx + 16
