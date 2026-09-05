package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestICLogDrawAllocationIsUnaffectedByTheFontDebugReadout is the "zero cost
// when the panel is closed" gate CLAUDE.md rule 11 calls for on this
// diagnostic, modeled on internal/assets/mountserve_test.go's
// TestNoMountsIsExactlyOneAtomicLoad: measure the REAL per-frame call site
// directly (drawICLogList — the function screens.go's courtroom draw calls
// every frame) and assert the Fonts-tab readout added nothing to it.
//
// allocsPerFrame (not a bare testing.AllocsPerRun) is this package's own
// established idiom for a draw-path gate — see allocgate_test.go's header for
// why a bare AllocsPerRun on a fully-loaded package flakes on an unrelated
// warm-up burst.
func TestICLogDrawAllocationIsUnaffectedByTheFontDebugReadout(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	list := sdl.Rect{X: 0, Y: 0, W: 420, H: 300}
	draw := func() { a.drawICLogList(list, false) }

	if n := allocsPerFrame(allocGateFrames, 0, draw); n != 0 {
		t.Fatalf("drawICLogList allocates %.1f/op with the Fonts debug readout in the tree, want 0 — "+
			"the readout must never run on the real per-frame draw path (fix the alloc, don't loosen the gate)", n)
	}
}

// TestICFontPickRowsReadsTheRealCoverageDecisionNotAGuess is the encapsulation
// test for the Fonts-tab seam (CLAUDE.md rule 11): it proves icFontPickRows
// reports whatever the REAL pick machinery (elemFontFor -> pickIn ->
// Ctx.pickMemo, read back by coversFace) decided, rather than re-deriving
// "plain ASCII is always covered" on its own — which is exactly the failure
// mode that would make a readout report "correct" while the real draw goes
// wrong, per the task brief this diagnostic shipped to satisfy.
//
// It dresses ic_chatlog with its own face at a non-100% scale so pickIn is
// off the single-font fast path (buildSet only shares the embedded pointer at
// exactly 100%/100%) and onto the real per-(text,set,pct) memo, drives one
// real IC line through it, then reaches into that SAME memo entry — the one
// pickIn itself wrote — and flips its covered bit, exactly as pickIn would
// have written it had the theme's face genuinely not covered the line. Only a
// readout that calls the real coversFace/elemFontFor pair notices.
func TestICFontPickRowsReadsTheRealCoverageDecisionNotAGuess(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()
	c := a.ctx

	c.SetThemeFaces([][]byte{openDyslexicOTF})
	a.themeFonts.e[elemICChatlog] = themeElemFont{face: 1, pct: themeFontPct(11)}
	a.themeFaceNames = []string{"OpenDyslexic-Regular.otf"}

	a.icLog = a.icLog[:0]
	a.pushIC("Celes: rip", 0, false, -1, "Celes")
	const text = "Celes: rip" // ICTimestampsOn defaults off, so the row text is exactly the pushed line

	rows := a.icFontPickRows(nil)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !rows[0].covered {
		t.Fatalf("the theme's own Latin face reported uncovered for plain ASCII before any tampering")
	}
	if rows[0].shared {
		t.Fatalf("a DRESSED ic_chatlog face at a non-100%% scale reported the shared embedded-face pointer")
	}
	if rows[0].setName != "ic_chatlog" {
		t.Fatalf("setName = %q, want \"ic_chatlog\" — setIndexOf resolved the row to the wrong element", rows[0].setName)
	}

	// Reach into the exact memo entry pickIn wrote for (text, ic_chatlog's own
	// set, its resolved pct) and flip covered to false.
	pct := a.elemPct(elemICChatlog, a.logPct)
	font := a.elemFontFor(elemICChatlog, a.logPct, text)
	key := pickKey{text: text, set: &c.themeElemSets[elemICChatlog], pct: int32(pct)}
	if _, ok := c.pickMemo[key]; !ok {
		t.Fatalf("pickIn did not memoize (text,set,pct) — this test's key has drifted from ui.go's pickIn")
	}
	c.pickMemo[key] = pickResult{font: font, covered: false}
	c.coverHintFont = nil // defensive: force the next coversFace call to consult the memo, not a stale hint

	rows = a.icFontPickRows(nil)
	if len(rows) != 1 {
		t.Fatalf("got %d rows after tampering, want 1", len(rows))
	}
	if rows[0].covered {
		t.Fatalf("icFontPickRows reported covered=true after the REAL pick memo was flipped to false — " +
			"it is not reading coversFace, it is guessing")
	}
}
