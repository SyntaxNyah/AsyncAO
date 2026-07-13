package ui

// "The text just disappears after Enter" recovery, new engine: every text
// field carries a bounded undo/redo history (fieldhistory.go). Out-of-band
// rewrites — the own-echo IC clear, a chat command, the OOC send, a palette
// template — land in it through the value-changed detector (fieldTrack), so
// Ctrl+Z restores the eaten line and Ctrl+Y re-applies the clear. These pins
// drive the same pure entry points textField uses.

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// TestEchoClearRecoverableViaHistory pins the mainline: the server echo
// clears the box (keep-until-echo), the next fieldTrack records it, and an
// undo step brings the sent line back — a redo re-clears.
func TestEchoClearRecoverableViaHistory(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx

	a.icInput, a.icPendingSent = "the eaten line", "the eaten line"
	c.fieldTrack("ic", a.icInput) // the field drew once with the line (first sight seeds)

	a.noteOwnICEcho()
	if a.icInput != "" {
		t.Fatalf("echo must clear the input, got %q", a.icInput)
	}
	h := c.fieldTrack("ic", a.icInput) // next draw: records the out-of-band clear
	if n := len(h.undo); n != 1 {
		t.Fatalf("the clear must land one undo step, got %d", n)
	}

	snap, ok := h.step(a.icInput, 0, false) // Ctrl+Z
	if !ok || snap.value != "the eaten line" {
		t.Fatalf("undo must restore the eaten line, got %q ok=%v", snap.value, ok)
	}
	a.icInput = snap.value

	snap, ok = h.step(a.icInput, 0, true) // Ctrl+Y
	if !ok || snap.value != "" {
		t.Fatalf("redo must re-apply the clear, got %q ok=%v", snap.value, ok)
	}
}

// TestAO2ColorSpliceAndUndo pins §3.8 select-and-colour: with a selection in the
// IC field, a colour cube wraps exactly the selected runes in that colour's AO2
// delimiters, and the splice is Ctrl+Z-able via the out-of-band history.
func TestAO2ColorSpliceAndUndo(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx

	a.icInput = "hello world"
	c.fieldTrack("ic", a.icInput) // first sight seeds the detector

	// Focus the IC field and select "world" ([6,11)).
	c.focusID = "ic"
	c.caretField = "ic"
	c.selAnchor, c.caret = 6, 11

	// Green (palette 1) → wraps the selection in the AO2 backtick pair.
	a.applyAO2ColorClick(1)
	if a.icInput != "hello `world`" {
		t.Fatalf("splice = %q, want \"hello `world`\" (selection wrapped)", a.icInput)
	}

	// The next fieldTrack sees the out-of-band change and records the prior value,
	// so Ctrl+Z restores the un-wrapped line.
	h := c.fieldTrack("ic", a.icInput)
	if n := len(h.undo); n != 1 {
		t.Fatalf("the splice must land one undo step, got %d", n)
	}
	snap, ok := h.step(a.icInput, 0, false) // Ctrl+Z
	if !ok || snap.value != "hello world" {
		t.Fatalf("undo must restore the pre-splice line, got %q ok=%v", snap.value, ok)
	}

	// No selection → the cube falls back to the whole-message colour (dropdown
	// parity), leaving the text itself untouched.
	c.selAnchor = -1
	a.icInput = "plain"
	a.applyAO2ColorClick(2) // red
	if a.icInput != "plain" {
		t.Fatalf("no-selection click must not alter the text, got %q", a.icInput)
	}
	if a.icColor != 2 {
		t.Fatalf("no-selection click must set the whole-message colour, icColor=%d want 2", a.icColor)
	}
}

// TestFieldHistoryCoalesceAndCap pins the burst rule (±1-rune edits inside
// the window collapse into one step; bigger jumps never do) and the bounded
// depth (hard rule 4).
func TestFieldHistoryCoalesceAndCap(t *testing.T) {
	h := &fieldHistory{}
	t0 := time.Now()

	// A typing burst: h → he → hel, 1 rune apart, well inside the window.
	h.record("", 0, "h", t0)
	h.record("h", 1, "he", t0.Add(50*time.Millisecond))
	h.record("he", 2, "hel", t0.Add(100*time.Millisecond))
	if n := len(h.undo); n != 1 {
		t.Fatalf("a typing burst must coalesce into one step, got %d", n)
	}

	// A big jump (the echo clear) inside the window still gets its own step —
	// a burst must never swallow a recoverable line.
	h.record("hel", 3, "", t0.Add(150*time.Millisecond))
	if n := len(h.undo); n != 2 {
		t.Fatalf("a multi-rune change must always push, got %d steps", n)
	}

	// Depth cap: hammer big alternating changes; the stack stays bounded.
	v := ""
	for i := 0; i < fieldUndoDepth*2; i++ {
		next := v + "xx" // ±2 runes → never coalesces
		h.record(v, 0, next, t0.Add(time.Duration(200+i)*time.Millisecond))
		v = next
	}
	if n := len(h.undo); n != fieldUndoDepth {
		t.Fatalf("undo depth = %d, want capped at %d", n, fieldUndoDepth)
	}

	// An edit after an undo forks history: redo dies.
	if _, ok := h.step(v, 0, false); !ok {
		t.Fatal("undo must pop")
	}
	if len(h.redo) != 1 {
		t.Fatalf("undo must feed redo, got %d", len(h.redo))
	}
	h.record(v, 0, v+"y", t0.Add(5*time.Second))
	if len(h.redo) != 0 {
		t.Fatal("a fresh edit must clear redo (history fork)")
	}
}

// TestFieldHistoriesBoundedAndIsolated pins the field-count LRU cap and the
// tab-switch wipe (multi-tab isolation: another session's draft must never
// resurface through a shared field id).
func TestFieldHistoriesBoundedAndIsolated(t *testing.T) {
	c := &Ctx{}
	for i := 0; i < fieldHistFieldsCap+4; i++ {
		id := string(rune('a' + i))
		c.fieldTrack(id, "seed")
	}
	if n := len(c.fieldHists); n != fieldHistFieldsCap {
		t.Fatalf("field histories = %d, want capped at %d", n, fieldHistFieldsCap)
	}

	c.fieldTrack("ic", "tab A draft")
	c.ClearFieldHistories() // the tab switch / fresh session hook
	if len(c.fieldHists) != 0 {
		t.Fatal("ClearFieldHistories must wipe every history")
	}
	// A fresh track after the wipe starts clean: the other tab's value is not
	// an "external change" to undo into.
	if h := c.fieldTrack("ic", "tab B draft"); len(h.undo) != 0 {
		t.Fatal("a post-wipe track must not record the previous tab's value")
	}
}

// TestUndoChordRouting pins the pre-screen conversion: with a field focused,
// Ctrl+Z/Ctrl+Y (and Ctrl+Shift+Z) become undoReq/redoReq and the raw chord
// is consumed — so a z/y-bound hotkey can't fire while typing. With no focus
// the chord passes through untouched.
func TestUndoChordRouting(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx

	route := func() { // the App.Frame conversion, verbatim gate
		if c.focusID != "" && !a.classicEdit && !a.layoutEdit {
			switch c.hotkey {
			case sdl.K_z:
				c.undoReq = !c.shiftHeld
				c.redoReq = c.shiftHeld
				c.hotkey = 0
			case sdl.K_y:
				c.redoReq = true
				c.hotkey = 0
			}
		}
	}

	c.focusID, c.hotkey = "ic", sdl.K_z
	route()
	if !c.undoReq || c.redoReq || c.hotkey != 0 {
		t.Fatalf("Ctrl+Z focused: undoReq=%v redoReq=%v hotkey=%v", c.undoReq, c.redoReq, c.hotkey)
	}

	c.undoReq, c.redoReq = false, false
	c.hotkey, c.shiftHeld = sdl.K_z, true
	route()
	if c.undoReq || !c.redoReq || c.hotkey != 0 {
		t.Fatal("Ctrl+Shift+Z focused must route to redo")
	}

	c.undoReq, c.redoReq, c.shiftHeld = false, false, false
	c.focusID, c.hotkey = "", sdl.K_z
	route()
	if c.undoReq || c.redoReq || c.hotkey != sdl.K_z {
		t.Fatal("no focus: the chord must fall through to the dispatcher")
	}
}

// TestWordBoundsAt pins the double-click word rule: a maximal non-space run;
// a boundary landing just past a word steps back onto it (clicking the right
// half of its last letter); only a click INSIDE a space run selects the run;
// clamped at the ends.
func TestWordBoundsAt(t *testing.T) {
	runes := []rune("fix  the broken english") // note the DOUBLE space after "fix"
	// f0 i1 x2 ␣3 ␣4 t5 h6 e7 ␣8 b9..n14 ␣15 e16..h22
	cases := []struct{ idx, lo, hi int }{
		{0, 0, 3},    // "fix"
		{6, 5, 8},    // "the"
		{3, 0, 3},    // boundary right after "fix" → the word, not the gap
		{4, 3, 5},    // inside the double-space run → the run
		{8, 5, 8},    // single space after "the" → "the"
		{22, 16, 23}, // "english", from its last rune
		{99, 16, 23}, // past the end clamps into the last word
	}
	for _, tc := range cases {
		if lo, hi := wordBoundsAt(runes, tc.idx); lo != tc.lo || hi != tc.hi {
			t.Errorf("wordBoundsAt(%d) = [%d,%d) want [%d,%d)", tc.idx, lo, hi, tc.lo, tc.hi)
		}
	}
	if lo, hi := wordBoundsAt(nil, 0); lo != 0 || hi != 0 {
		t.Errorf("empty text = [%d,%d), want [0,0)", lo, hi)
	}
}
