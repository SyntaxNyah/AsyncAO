package ui

import "testing"

// TestAreaHighlightColor pins the current-area highlight resolution: stock
// green when unset, the user's hex when valid, and the green fallback on a
// malformed value (never a black/zero colour on a typo mid-edit).
func TestAreaHighlightColor(t *testing.T) {
	a := testTabApp(t)
	if got := a.areaHighlightCol(); got != areaCurrentDefault {
		t.Errorf("unset highlight = %+v, want the stock green %+v", got, areaCurrentDefault)
	}
	a.d.Prefs.SetAreaHighlightColorHex("ff0080")
	if got := a.areaHighlightCol(); got.R != 0xff || got.G != 0 || got.B != 0x80 {
		t.Errorf("custom highlight = %+v, want ff0080", got)
	}
	a.d.Prefs.SetAreaHighlightColorHex("not-a-colour")
	if got := a.areaHighlightCol(); got != areaCurrentDefault {
		t.Errorf("malformed hex = %+v, want the green fallback", got)
	}
	a.d.Prefs.SetAreaHighlightColorHex("")
	if got := a.areaHighlightCol(); got != areaCurrentDefault {
		t.Errorf("cleared hex = %+v, want the stock green", got)
	}
}
