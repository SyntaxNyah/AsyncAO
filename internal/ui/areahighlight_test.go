package ui

import (
	"fmt"
	"testing"
)

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

// TestAreaWheelHexRoundTrip pins the area colour wheel's write format: the wheel
// stores its pick as "%06x", and that string must resolve back to the same colour
// through areaHighlightCol — including a leading-zero channel (a naive "%x" would
// shorten 0x00ff40 to "ff40" and fall back to green).
func TestAreaWheelHexRoundTrip(t *testing.T) {
	a := testTabApp(t)
	for _, rgb := range []int{0x00ff40, 0xff0080, 0x000001, 0xffffff} {
		a.d.Prefs.SetAreaHighlightColorHex(fmt.Sprintf("%06x", rgb))
		got := a.areaHighlightCol()
		if int(got.R)<<16|int(got.G)<<8|int(got.B) != rgb {
			t.Errorf("wheel write %06x resolved to %+v", rgb, got)
		}
	}
}
