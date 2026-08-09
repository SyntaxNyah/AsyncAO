package ui

// Gates for the shared text-field machinery this batch changed (ui.go): the
// word-granular edits, the STICKY view that stopped sliding text out from under a
// drag-selection, and the right-gutter reservation that stops typed text running
// under the IC character counter.

import (
	"go/ast"
	"testing"
)

// TestWordForwardBoundaryMirrorsTheBackwardOne pins Ctrl+Delete's reach as the
// exact mirror of Ctrl+Backspace's: one takes "the gap before it + word", the other
// "word + the gap after it".
func TestWordForwardBoundaryMirrorsTheBackwardOne(t *testing.T) {
	r := []rune("one  two three")
	for _, tc := range []struct{ pos, want int }{
		{0, 5},  // "one" + the two spaces
		{3, 5},  // sitting on the gap: just the gap
		{5, 9},  // "two" + one space
		{9, 14}, // "three", no trailing gap
		{14, 14},
		{-3, 5},  // a negative caret clamps to 0
		{99, 14}, // …and an overlong one to the end
	} {
		if got := wordForwardBoundary(r, tc.pos); got != tc.want {
			t.Errorf("wordForwardBoundary(%d) = %d, want %d", tc.pos, got, tc.want)
		}
	}
	// Rune-aware, never byte-slicing: a multibyte word must be crossed whole.
	if got := wordForwardBoundary([]rune("Häschen fünf"), 0); got != 8 {
		t.Errorf("a multibyte word ended at rune %d, want 8", got)
	}
}

// TestWordEditsMoveAndDelete drives editStep — the pure core both chords and both
// jumps go through — so the whole word-granular feature is pinned without SDL.
func TestWordEditsMoveAndDelete(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		caret     int
		in        editInput
		wantVal   string
		wantCaret int
	}{
		{"ctrl+backspace eats the word and its gap", "one two ", 8, editInput{wordBack: true}, "one ", 4},
		{"ctrl+delete eats forward, caret stays", "one two", 0, editInput{wordFwd: true}, "two", 0},
		{"ctrl+left jumps back a word", "one two", 7, editInput{op: editWordLeft}, "one two", 4},
		{"ctrl+right jumps forward a word", "one two", 0, editInput{op: editWordRight}, "one two", 4},
		{"a word delete over a SELECTION takes just the selection",
			"alpha beta gamma", 16, editInput{wordBack: true, selStart: 6, selEnd: 11}, "alpha gamma", 6},
		{"a word JUMP over a selection collapses then jumps",
			"alpha beta gamma", 16, editInput{op: editWordLeft, selStart: 6, selEnd: 11}, "alpha beta gamma", 0},
		{"ctrl+backspace at the head is inert", "one", 0, editInput{wordBack: true}, "one", 0},
		{"ctrl+delete at the tail is inert", "one", 3, editInput{wordFwd: true}, "one", 3},
	} {
		gotVal, gotCaret := editStep(tc.value, tc.caret, tc.in)
		if gotVal != tc.wantVal || gotCaret != tc.wantCaret {
			t.Errorf("%s: got (%q, %d), want (%q, %d)", tc.name, gotVal, gotCaret, tc.wantVal, tc.wantCaret)
		}
	}
}

// TestScrollKeepDoesNotMoveAVisibleCaret is the fix for the reported "text slides
// under the cursor mid-drag". The old rule re-derived the view from the caret every
// frame ("centre it"), so clicking into a long line moved the glyph you pressed on
// before the button came up. A native field does not move the view on a click at
// all, which is what returning `cur` unchanged gives.
func TestScrollKeepDoesNotMoveAVisibleCaret(t *testing.T) {
	const full, avail = 1000, 200
	// Caret comfortably inside the window at every scroll offset we try.
	for _, cur := range []int32{0, 137, 400, 800} {
		caret := cur + avail/2
		if got := scrollKeep(cur, full, caret, avail); got != cur {
			t.Errorf("a visible caret moved the view from %d to %d — click-to-caret must not re-centre", cur, got)
		}
	}
	// It all fits: head-aligned, no scroll state to keep.
	if got := scrollKeep(500, 150, 100, avail); got != 0 {
		t.Errorf("a value that fits left the view at %d, want 0", got)
	}
}

// TestScrollKeepMovesTheMinimumWhenTheCaretLeaves pins the other half: when the
// caret DOES leave the window the view moves the smallest distance that brings it
// back, plus a small lead so the next keystroke does not scroll again.
func TestScrollKeepMovesTheMinimumWhenTheCaretLeaves(t *testing.T) {
	const full, avail = 1000, 200
	// Off the right edge: the caret ends up fieldCaretMarginPx short of it.
	got := scrollKeep(0, full, 260, avail)
	if want := int32(260) - (avail - fieldCaretMarginPx); got != want {
		t.Errorf("caret past the right edge → scroll %d, want %d", got, want)
	}
	// Off the left edge: fieldCaretMarginPx of lead in front of it.
	got = scrollKeep(500, full, 480, avail)
	if want := int32(480) - fieldCaretMarginPx; got != want {
		t.Errorf("caret past the left edge → scroll %d, want %d", got, want)
	}
	// Always clamped: a value that shrank underneath the field can never leave a
	// gap on the right, and the view can never go negative.
	if got := scrollKeep(5000, full, 990, avail); got != full-avail {
		t.Errorf("an over-large stored scroll clamped to %d, want %d", got, full-avail)
	}
	if got := scrollKeep(-50, full, 0, avail); got != 0 {
		t.Errorf("a negative stored scroll clamped to %d, want 0", got)
	}
	// A box narrower than twice the margin must not oscillate between the two
	// clamps: the margin is capped at half the interior.
	for _, caret := range []int32{0, 5, 12, 19} {
		s := scrollKeep(0, 100, caret, 20)
		if s != scrollKeep(s, 100, caret, 20) {
			t.Errorf("a narrow box oscillated at caret %d (%d → %d)", caret, s, scrollKeep(s, 100, caret, 20))
		}
	}
}

// TestReserveFieldTailIsOneSlotAndSelfClearing pins the counter-overdraw fix's
// seam. It is a single slot on purpose: exactly one field per frame needs it, a map
// would allocate on the draw path, and a reservation declared for a field that then
// does not draw must not mis-size an unrelated one.
func TestReserveFieldTailIsOneSlotAndSelfClearing(t *testing.T) {
	c := &Ctx{}
	c.ReserveFieldTail(icFieldID, 48)
	if got := c.takeFieldTail("areasearch"); got != 0 {
		t.Errorf("another field consumed %d px of the IC input's reservation", got)
	}
	if got := c.takeFieldTail(icFieldID); got != 48 {
		t.Errorf("the declared field got %d px, want 48", got)
	}
	if got := c.takeFieldTail(icFieldID); got != 0 {
		t.Errorf("the reservation was consumed twice (%d px the second time)", got)
	}
	// Degenerate declarations are no-ops, so a caller whose overlay is hidden
	// simply skips the call.
	c.ReserveFieldTail("", 20)
	c.ReserveFieldTail(icFieldID, 0)
	c.ReserveFieldTail(icFieldID, -5)
	if got := c.takeFieldTail(icFieldID); got != 0 {
		t.Errorf("a degenerate reservation stuck (%d px)", got)
	}
}

// TestBothICBarsReserveTheCounterBand is the encapsulation gate for the overdraw
// fix. The reservation only works because the draw site declares it BEFORE the
// field draws; a bar that stopped declaring it would type straight through the
// counter again, exactly as reported, with the unit gate above still green. Both
// IC bars are pinned because the themed one is a separate draw.
func TestBothICBarsReserveTheCounterBand(t *testing.T) {
	for _, h := range []struct{ file, fn string }{
		{"screens.go", "drawICInputRow"},
		{"theme_layout.go", "drawCourtroomThemed"},
	} {
		body := funcBodySource(t, h.file, h.fn)
		if !containsCall(body, "ReserveFieldTail") {
			t.Errorf("%s does not reserve the counter band — typed text runs under the counter again", h.fn)
		}
		if !containsCall(body, "icFieldTailReserve") {
			t.Errorf("%s reserves a width of its own instead of icFieldTailReserve — a second estimate re-opens the overlap", h.fn)
		}
	}
}

// TestEscTogglesExtrasRatherThanLeavingTheServer is GH #51. Esc used to call
// requestDisconnect: one reflexive keypress from leaving the server, behind nothing
// but a confirm. It toggles the Extras box now, and the disconnect action lives in
// that box — still one click away, never one keystroke.
func TestEscTogglesExtrasRatherThanLeavingTheServer(t *testing.T) {
	// The COURTROOM arm specifically. Char-select's Esc still leaves the server
	// through the confirm and must keep doing so — a fresh char-select has no other
	// exit — so the gate reads that one case clause rather than the whole method.
	arms := screenCaseClauses(t, "ScreenCourtroom")
	if len(arms) == 0 {
		t.Fatal("App.Frame has no `case ScreenCourtroom:` arm — the dispatch was restructured and this gate checks nothing")
	}
	toggles := 0
	for _, arm := range arms {
		if containsCall(arm, "requestDisconnect") {
			t.Error("a courtroom key arm still calls requestDisconnect — one reflexive keypress from leaving the server (GH #51)")
		}
		if containsCall(arm, "toggleExtrasBox") {
			toggles++
		}
	}
	if toggles == 0 {
		t.Fatal("no courtroom key arm toggles the Extras box — GH #51's replacement action is gone")
	}
	// The action did not disappear: the Extras box still offers it.
	found := false
	for _, w := range (&App{}).extrasWidgets() {
		if w.label == "Disconnect" {
			found = true
		}
	}
	if !found {
		t.Error("the Extras box has no Disconnect entry — moving the action off Esc deleted it instead of relocating it")
	}
}

// screenCaseClauses returns every `case <screen>:` arm inside App.Frame's
// pre-screen key dispatch. Plural because Frame switches on a.screen more than
// once; reading them all is what keeps the gate honest about WHICH arm leaves the
// server, without it having to know the order they appear in.
func screenCaseClauses(t *testing.T, screen string) []ast.Node {
	t.Helper()
	body := funcBodySource(t, "app.go", "Frame")
	var out []ast.Node
	ast.Inspect(body, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok || len(cc.List) != 1 {
			return true
		}
		if id, ok := cc.List[0].(*ast.Ident); ok && id.Name == screen {
			out = append(out, cc)
		}
		return true
	})
	return out
}

// TestToggleExtrasBoxLeavesTheaterFirst pins setTheater's invariant: theater is
// stage-only, so summoning the box from there means "give me the UI back" rather
// than floating chrome over a bare stage.
func TestToggleExtrasBoxLeavesTheaterFirst(t *testing.T) {
	a := &App{ctx: &Ctx{}, theaterOn: true}
	a.toggleExtrasBox()
	if a.theaterOn {
		t.Error("summoning the Extras box from theater must exit theater")
	}
	if !a.showWidgets {
		t.Error("…and must SHOW the box, never toggle it off")
	}
	a.toggleExtrasBox()
	if a.showWidgets {
		t.Error("a second press outside theater must hide it again")
	}
}
