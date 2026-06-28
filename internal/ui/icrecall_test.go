package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestRecordSentIC pins the recall ring (#8): newest-last, blanks skipped, consecutive
// duplicates collapsed, capped, and any send resets the cursor to the live draft.
func TestRecordSentIC(t *testing.T) {
	a := testTabApp(t)
	a.icRecallIdx = 5 // pretend we were browsing history
	a.recordSentIC("hello")
	a.recordSentIC("   ")   // blank — skipped
	a.recordSentIC("hello") // consecutive duplicate — skipped
	a.recordSentIC("world")
	if len(a.icSentHist) != 2 || a.icSentHist[0] != "hello" || a.icSentHist[1] != "world" {
		t.Errorf("icSentHist = %v, want [hello world]", a.icSentHist)
	}
	if a.icRecallIdx != -1 {
		t.Errorf("a send must reset icRecallIdx to -1, got %d", a.icRecallIdx)
	}
	for i := 0; i < sentHistCap+10; i++ {
		a.recordSentIC("m" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
	}
	if len(a.icSentHist) > sentHistCap {
		t.Errorf("history len = %d, want <= %d", len(a.icSentHist), sentHistCap)
	}
}

// TestRecallIC pins the shell-style Up/Down walk: Up stashes the live draft and goes
// older, Down goes newer and restores the draft; both consume the key; no-op unless the
// "ic" field is focused.
func TestRecallIC(t *testing.T) {
	a := testTabApp(t)
	a.icSentHist = []string{"first", "second", "third"} // oldest..newest
	a.icRecallIdx = -1
	a.icInput = "draft"
	press := func(k sdl.Keycode) {
		a.ctx.focusID = "ic"
		a.ctx.keyPressed = k
		a.recallIC()
	}

	press(sdl.K_UP) // stash draft, load newest
	if a.icInput != "third" || a.ctx.keyPressed != 0 {
		t.Fatalf("first Up: input=%q key=%d, want third + consumed", a.icInput, a.ctx.keyPressed)
	}
	press(sdl.K_UP)
	press(sdl.K_UP)
	if a.icInput != "first" {
		t.Errorf("walked back to %q, want first", a.icInput)
	}
	press(sdl.K_UP) // at oldest — stays put
	if a.icInput != "first" {
		t.Errorf("Up past oldest changed it to %q", a.icInput)
	}
	press(sdl.K_DOWN)
	press(sdl.K_DOWN)
	if a.icInput != "third" {
		t.Errorf("walked forward to %q, want third", a.icInput)
	}
	press(sdl.K_DOWN) // past newest → restore the draft
	if a.icInput != "draft" || a.icRecallIdx != -1 {
		t.Errorf("past newest: input=%q idx=%d, want draft + -1", a.icInput, a.icRecallIdx)
	}

	a.ctx.focusID = "oocmsg" // a different field focused → recall must not fire
	a.ctx.keyPressed = sdl.K_UP
	a.recallIC()
	if a.icInput != "draft" || a.ctx.keyPressed != sdl.K_UP {
		t.Errorf("recall fired when ic wasn't focused: input=%q key=%d", a.icInput, a.ctx.keyPressed)
	}
}
