package ui

import "testing"

// TestGrownChatBoxH pins the chatbox "grow to fit its message" rule (the fix for text being cut
// off under the box after a viewport/box resize wraps it taller): a short message keeps the base
// band height, a long one grows to fit, and growth is capped at 3/5 of the stage so it can never
// swallow the viewport.
func TestGrownChatBoxH(t *testing.T) {
	const vpH, lineH = int32(400), int32(16)

	if got := grownChatBoxH(100, vpH, 3, lineH); got != 100 { // short: base band wins, box unchanged
		t.Errorf("short message: got %d, want base 100", got)
	}
	want := chatBoxTopStrip + 10*lineH + chatBoxBottomPad // 26 + 160 + 8 = 194, below the 240 cap
	if got := grownChatBoxH(100, vpH, 10, lineH); got != want {
		t.Errorf("long message: got %d, want %d (grow to fit)", got, want)
	}
	if got := grownChatBoxH(100, vpH, 100, lineH); got != vpH*3/5 { // very long: capped at 3/5 of the stage
		t.Errorf("very long message: got %d, want cap %d", got, vpH*3/5)
	}
}
