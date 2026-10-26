package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestEvidencePanelNonBlocking pins issue #5: the evidence panel is a non-blocking
// floating box now, so opening it does NOT fence the whole courtroom (you can keep
// chatting / follow the log) — only the cursor being OVER the panel fences.
func TestEvidencePanelNonBlocking(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	const w, h = int32(1000), int32(800)

	a.showEvid = true
	if a.courtModalOpen() {
		t.Error("evidence open must NOT be a blocking modal — chat stays live (#5)")
	}
	r := a.evidPanelRect(w, h)
	a.ctx.mouseX, a.ctx.mouseY = r.X+5, r.Y+5
	if !a.boxFencesPointer(w, h) {
		t.Error("cursor over the evidence panel must fence the courtroom (clicks can't leak)")
	}
	a.ctx.mouseX, a.ctx.mouseY = r.X+r.W+50, r.Y+r.H+50
	if a.boxFencesPointer(w, h) {
		t.Error("cursor clear of the evidence panel must NOT fence — the courtroom stays live")
	}
	a.showEvid = false
	a.ctx.mouseX, a.ctx.mouseY = r.X+5, r.Y+5
	if a.boxFencesPointer(w, h) {
		t.Error("a closed evidence panel must never fence")
	}
}

// TestEvidencePanelDefaultClearsInput pins that the FIRST-OPEN default tucks top-left
// (not centred) so it can't blanket the IC input / log — the practical point of #5
// (keep talking while browsing evidence). Once dragged (placed) the floatWin wins.
func TestEvidencePanelDefaultClearsInput(t *testing.T) {
	a := testTabApp(t)
	const w, h = int32(1000), int32(800)
	if r := a.evidPanelRect(w, h); r.X != floatWinMargin || r.Y != floatTitleH {
		t.Errorf("first-open evidence rect = %+v, want top-left (X=%d, Y=%d) so the IC input/log stay clear", r, floatWinMargin, floatTitleH)
	}
	a.evidWin = floatWin{x: 300, y: 250, w: 500, h: 400, placed: true}
	if got := a.evidPanelRect(w, h); got.X != 300 || got.Y != 250 {
		t.Errorf("placed evidence rect = %+v, want the dragged spot (300,250)", got)
	}
}
