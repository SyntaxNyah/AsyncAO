package ui

// "The text just disappears after Enter" recovery: every place the CLIENT
// removes a line from the IC/OOC input (own-echo clear, chat command, OOC
// send, palette insert) stashes it, and Ctrl+Z with that field focused swaps
// it back — a second press swaps forward again, so a half-typed draft is
// never lost either way.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/veandco/go-sdl2/sdl"
)

// TestEchoClearStashesAndCtrlZRestores pins the mainline: the server echo
// clears the box (keep-until-echo), the stash catches it, Ctrl+Z brings it back.
func TestEchoClearStashesAndCtrlZRestores(t *testing.T) {
	a := testTabApp(t)
	a.icInput, a.icPendingSent = "the eaten line", "the eaten line"
	a.noteOwnICEcho()
	if a.icInput != "" || a.icUndoText != "the eaten line" {
		t.Fatalf("echo clear must stash the line: input=%q stash=%q", a.icInput, a.icUndoText)
	}

	a.ctx.focusID = "ic"
	a.ctx.hotkey = sdl.K_z
	if !a.inputUndoChord() {
		t.Fatal("Ctrl+Z in the IC field must restore the eaten line")
	}
	if a.icInput != "the eaten line" {
		t.Errorf("restored input = %q", a.icInput)
	}
	if a.ctx.hotkey != 0 {
		t.Error("consumed chord must zero c.hotkey")
	}
}

// TestInputUndoSwapsWithDraft pins the swap semantics: a half-typed draft
// trades places with the recovered line, and a second press trades back.
func TestInputUndoSwapsWithDraft(t *testing.T) {
	a := testTabApp(t)
	a.icUndoText, a.icInput = "eaten", "half-typed"
	a.ctx.focusID = "ic"

	a.ctx.hotkey = sdl.K_z
	if !a.inputUndoChord() {
		t.Fatal("Ctrl+Z with a stash must fire")
	}
	if a.icInput != "eaten" || a.icUndoText != "half-typed" {
		t.Fatalf("swap: input=%q stash=%q", a.icInput, a.icUndoText)
	}

	a.ctx.hotkey = sdl.K_z
	if !a.inputUndoChord() {
		t.Fatal("second Ctrl+Z must swap back (one-slot redo)")
	}
	if a.icInput != "half-typed" || a.icUndoText != "eaten" {
		t.Fatalf("swap back: input=%q stash=%q", a.icInput, a.icUndoText)
	}
}

// TestInputUndoScope: no focus / empty stash falls through (the chord stays
// available to user hotkeys), and the OOC send path stashes for its fields.
func TestInputUndoScope(t *testing.T) {
	a := testTabApp(t)
	a.icUndoText = "stashed"
	a.ctx.hotkey = sdl.K_z
	a.ctx.focusID = ""
	if a.inputUndoChord() {
		t.Fatal("no field focused: the chord must fall through")
	}
	a.ctx.focusID = "ic"
	a.icUndoText = ""
	if a.inputUndoChord() {
		t.Fatal("empty stash: the chord must fall through (never eat the draft)")
	}

	// OOC send clears immediately (no echo protocol) — it must stash.
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { return nil }, "")
	a.oocInput = "an ooc line"
	a.submitOOC()
	if a.oocInput != "" || a.oocUndoText != "an ooc line" {
		t.Fatalf("OOC send must clear + stash: input=%q stash=%q", a.oocInput, a.oocUndoText)
	}
	a.ctx.focusID = "oocmsg"
	a.ctx.hotkey = sdl.K_z
	if !a.inputUndoChord() || a.oocInput != "an ooc line" {
		t.Fatalf("Ctrl+Z in the OOC box must restore, got %q", a.oocInput)
	}
}
