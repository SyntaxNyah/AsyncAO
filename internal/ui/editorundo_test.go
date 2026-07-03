package ui

// Layout-editor undo/redo arrives as a Ctrl CHORD: HandleEvent routes every
// non-clipboard Ctrl combination into c.hotkey and returns (#96), so the
// editors' old in-draw c.keyPressed checks were dead — Ctrl+Z did nothing and
// Ctrl+Y fired its default bind ("Reshow hidden sprites") mid-edit. These
// tests pin the fix: editorUndoChord owns Z/Y while an editor is armed and
// falls through otherwise (playtest: "Layout Editor doesn't register
// Ctrl-Z/Y").

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

func TestEditorUndoChordRoutesCtrlZY(t *testing.T) {
	a := testTabApp(t)
	a.classicEdit = true
	a.classicOv = map[string][4]float64{slotViewport: {0.1, 0.1, 0.5, 0.5}}
	a.pushClassicUndo()                                        // snapshot BEFORE the "drag"
	a.classicOv[slotViewport] = [4]float64{0.2, 0.2, 0.5, 0.5} // the drag

	a.ctx.hotkey = sdl.K_z
	if !a.editorUndoChord() {
		t.Fatal("Ctrl+Z while editing must be consumed by the editor")
	}
	if a.ctx.hotkey != 0 {
		t.Error("consumed chord must zero c.hotkey")
	}
	if got := a.classicOv[slotViewport]; got != ([4]float64{0.1, 0.1, 0.5, 0.5}) {
		t.Errorf("undo restored %v, want the pre-drag snapshot", got)
	}

	a.ctx.hotkey = sdl.K_y
	if !a.editorUndoChord() {
		t.Fatal("Ctrl+Y while editing must be consumed by the editor")
	}
	if got := a.classicOv[slotViewport]; got != ([4]float64{0.2, 0.2, 0.5, 0.5}) {
		t.Errorf("redo restored %v, want the dragged state back", got)
	}
}

func TestEditorUndoChordScope(t *testing.T) {
	a := testTabApp(t)

	// No editor armed: the chord falls through to normal hotkey dispatch.
	a.ctx.hotkey = sdl.K_z
	if a.editorUndoChord() {
		t.Fatal("no editor armed — Ctrl+Z must fall through")
	}
	if a.ctx.hotkey != sdl.K_z {
		t.Fatal("fall-through must leave the chord for the dispatcher")
	}

	// Armed with EMPTY history: still consumed, so the Ctrl+Y default bind
	// (Reshow hidden sprites) can never fire mid-edit.
	a.classicEdit = true
	a.ctx.hotkey = sdl.K_y
	if !a.editorUndoChord() || a.ctx.hotkey != 0 {
		t.Fatal("an armed editor owns Ctrl+Y even with empty history")
	}

	// Other chords pass untouched while editing.
	a.ctx.hotkey = sdl.K_s
	if a.editorUndoChord() {
		t.Fatal("only Z/Y belong to the editor; other chords must dispatch")
	}
}
