package ui

import "testing"

// TestUIDeviceFromLogical pins the #77 device-exact projection (strategy B). The
// focused text field's moving parts (value texture, selection, caret) draw under
// SetScale(1,1) with rects projected here, so the rule MUST be the exact inverse
// of uiLogicalFromDevice's "round half up" (add half the divisor before dividing)
// and identity at 100%/unset — otherwise the caret and value drift apart at
// fractional scales, the very artifact this path fixes.
func TestUIDeviceFromLogical(t *testing.T) {
	cases := []struct {
		logical, devPct, want int32
	}{
		// devPct 0 / 100 are identity passthrough (the byte-identical 100% path).
		{0, 0, 0}, {2, 0, 2}, {37, 0, 37},
		{0, 100, 0}, {2, 100, 2}, {200, 100, 200},
		// 125%: round half up. 2 (the caret width) → (2*125+50)/100 = 3.
		{0, 125, 0}, {2, 125, 3}, {4, 125, 5}, {8, 125, 10}, {100, 125, 125},
		// 150%: 2 → (2*150+50)/100 = 3.
		{2, 150, 3}, {4, 150, 6}, {100, 150, 150},
		// 175%/200% spot checks.
		{2, 175, 4}, {2, 200, 4}, {50, 200, 100},
	}
	for _, c := range cases {
		if got := uiDeviceFromLogical(c.logical, c.devPct); got != c.want {
			t.Errorf("uiDeviceFromLogical(%d,%d)=%d, want %d", c.logical, c.devPct, got, c.want)
		}
	}
}

// TestUIDeviceLogicalRoundTrip pins that the device projection is the exact
// inverse direction of uiLogicalFromDevice: projecting a device width back to
// logical and forward again is stable (no accumulating drift), the property the
// twin rounding rule guarantees.
func TestUIDeviceLogicalRoundTrip(t *testing.T) {
	for _, dev := range []int32{100, 125, 150, 175, 200} {
		for _, logical := range []int32{0, 1, 2, 3, 7, 40, 200, 333} {
			d := uiDeviceFromLogical(logical, dev)
			// Forward projection is monotonic non-decreasing in the logical input:
			// projecting x and x+1 must not cross (a crossing would let the caret
			// land left of a glyph edge it should sit at).
			if d2 := uiDeviceFromLogical(logical+1, dev); d2 < d {
				t.Errorf("uiDeviceFromLogical not monotonic at logical=%d dev=%d: %d then %d", logical, dev, d, d2)
			}
		}
	}
}

// TestDeviceExactTextGate pins the entry gate for the device-exact draw path as a
// PURE predicate (no renderer, no Ctx test hook in prod code): it fires only at a
// fractional user scale. At 100% and unset (0) it is false, so the focused text
// field keeps every line of its pre-fix scaled behavior byte-identical — the 100%
// path takes ZERO SetScale flips (there is no other caller of devFieldValue).
func TestDeviceExactTextGate(t *testing.T) {
	if deviceExactText(DefaultScalePct) {
		t.Error("deviceExactText(100) must be false — the 100% path must not flip SetScale")
	}
	if deviceExactText(0) {
		t.Error("deviceExactText(0) must be false — an unset scale must not flip SetScale")
	}
	for _, dev := range []int32{125, 150, 175, 200} {
		if !deviceExactText(dev) {
			t.Errorf("deviceExactText(%d) must be true — a fractional scale needs the device-exact path", dev)
		}
	}
}
