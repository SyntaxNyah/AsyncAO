package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestRecordSentOOC pins the OOC recall ring (mirrors IC #8): newest-last, blanks skipped,
// consecutive duplicates collapsed, capped, and any send resets the cursor to the live draft.
func TestRecordSentOOC(t *testing.T) {
	a := testTabApp(t)
	a.oocRecallIdx = 5 // pretend we were browsing history
	a.recordSentOOC("hi")
	a.recordSentOOC("   ") // blank — skipped
	a.recordSentOOC("hi")  // consecutive duplicate — skipped
	a.recordSentOOC("there")
	if len(a.oocSentHist) != 2 || a.oocSentHist[0] != "hi" || a.oocSentHist[1] != "there" {
		t.Errorf("oocSentHist = %v, want [hi there]", a.oocSentHist)
	}
	if a.oocRecallIdx != -1 {
		t.Errorf("a send must reset oocRecallIdx to -1, got %d", a.oocRecallIdx)
	}
	for i := 0; i < sentHistCap+10; i++ {
		a.recordSentOOC("m" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
	}
	if len(a.oocSentHist) > sentHistCap {
		t.Errorf("history len = %d, want <= %d", len(a.oocSentHist), sentHistCap)
	}
}

// TestRecallOOC pins the Up/Down walk driven by BOTH OOC fields (the "oocmsg" box and the "ooc"
// bar — they share oocInput and one ring), and that IC focus never touches the OOC ring.
func TestRecallOOC(t *testing.T) {
	a := testTabApp(t)
	a.oocSentHist = []string{"first", "second", "third"} // oldest..newest
	a.oocRecallIdx = -1
	a.oocInput = "draft"
	press := func(focus string, k sdl.Keycode) {
		a.ctx.focusID = focus
		a.ctx.keyPressed = k
		a.recallOOC()
	}

	press("oocmsg", sdl.K_UP) // the OOC box: stash draft, load newest
	if a.oocInput != "third" || a.ctx.keyPressed != 0 {
		t.Fatalf("first Up (oocmsg): input=%q key=%d, want third + consumed", a.oocInput, a.ctx.keyPressed)
	}
	press("ooc", sdl.K_UP) // the bottom bar drives the SAME ring
	if a.oocInput != "second" {
		t.Errorf("Up from the 'ooc' bar walked to %q, want second", a.oocInput)
	}
	press("ooc", sdl.K_DOWN)
	if a.oocInput != "third" {
		t.Errorf("Down walked to %q, want third", a.oocInput)
	}
	press("oocmsg", sdl.K_DOWN) // past newest -> restore the draft
	if a.oocInput != "draft" || a.oocRecallIdx != -1 {
		t.Errorf("past newest: input=%q idx=%d, want draft + -1", a.oocInput, a.oocRecallIdx)
	}

	// IC focus must NOT drive the OOC ring (each input owns its own history).
	a.oocInput = "untouched"
	press("ic", sdl.K_UP)
	if a.oocInput != "untouched" || a.ctx.keyPressed != sdl.K_UP {
		t.Errorf("IC focus fired OOC recall: input=%q key=%d", a.oocInput, a.ctx.keyPressed)
	}
}
