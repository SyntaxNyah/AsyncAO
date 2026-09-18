package render

import "testing"

// TestLogicalFromDeviceRounding pins the #77 "round half up" rule (add half the
// divisor before dividing) at the non-integer scales the roadmap flags as the
// classic off-by-one failure mode. This rule is DUPLICATED in ui.uiLogicalFromDevice
// (and inverted in Ctx.devToLogical); if this changes, that MUST change in lockstep
// or a kit label and a message raster of the same string disagree by a pixel.
func TestLogicalFromDeviceRounding(t *testing.T) {
	cases := []struct {
		device, devScale, want int32
	}{
		// Identity fast paths.
		{100, 100, 100},
		{37, 100, 37},
		{50, 0, 50},  // devScale 0 → identity (headless MessageRaster{})
		{50, -5, 50}, // negative → identity
		// 200%: device is exactly 2× logical, halves cleanly.
		{200, 200, 100},
		{201, 200, 101}, // (201*100 + 100) / 200 = 20200/200 = 101 (rounds up from 100.5)
		// 150%: the odd-scale case. logical = round(device/1.5).
		{150, 150, 100}, // 15000/150 = 100
		{151, 150, 101}, // (15100+75)/150 = 15175/150 = 101 (100.67 → 101)
		{149, 150, 99},  // (14900+75)/150 = 14975/150 = 99 (99.3 → 99)
		// 125%: logical = round(device/1.25).
		{125, 125, 100}, // 12500/125 = 100
		{100, 125, 80},  // (10000+62)/125 = 10062/125 = 80 (80.5 rounds up? 80.496 → 80)
		// 175%: logical = round(device/1.75).
		{175, 175, 100}, // 17500/175 = 100
		{88, 175, 50},   // (8800+87)/175 = 8887/175 = 50 (50.3 → 50)
	}
	for _, c := range cases {
		if got := logicalFromDevice(c.device, c.devScale); got != c.want {
			t.Errorf("logicalFromDevice(%d, %d) = %d, want %d", c.device, c.devScale, got, c.want)
		}
	}
}

// TestDeviceFromLogicalRoundTripBounded pins the ONE property a
// device→logical→device round trip can actually guarantee once the forward
// projection uses the shared "round half up" rule: the drift stays within a pixel
// and the projection never reverses direction. logicalFromDevice folds the scale
// into the font point size and divides the result back down, so the round trip is
// lossy by construction — demanding exactness here would force the forward
// direction to round some OTHER way, reintroducing exactly the disagreement with
// uiDeviceFromLogical this fix removes.
func TestDeviceFromLogicalRoundTripBounded(t *testing.T) {
	for _, dev := range []int32{0, 100, 125, 150, 175, 200} {
		for _, lineH := range []int32{0, 1, 4, 10, 16, 20, 22, 35, 48} {
			m := &MessageRaster{lineH: lineH, devScale: dev}
			logical := logicalFromDevice(m.lineH, m.devScale)
			if back := m.deviceFromLogical(logical); back < lineH-1 || back > lineH+1 {
				t.Errorf("round-trip %d device at %d%% → %d logical → %d device drifted more than 1px",
					lineH, dev, logical, back)
			}
			// Monotonic non-decreasing in the logical input: a crossing would let a
			// caret/selection edge land left of the glyph edge it belongs at.
			if d2 := m.deviceFromLogical(logical + 1); d2 < m.deviceFromLogical(logical) {
				t.Errorf("deviceFromLogical not monotonic at dev=%d logical=%d", dev, logical)
			}
		}
	}
}

// TestDeviceFromLogicalRounding pins the forward projection's "round half up"
// rule at the odd scales the roadmap flags as the off-by-one failure mode, and
// pins it EQUAL to the inverse rule rather than merely consistent with its own
// history. uiDeviceFromLogical rounds half up too; if this file ever rounds the
// other way again, a chatbox line and the chrome around it separate by a pixel.
func TestDeviceFromLogicalRounding(t *testing.T) {
	cases := []struct {
		logical, devScale, want int32
	}{
		// Identity fast paths.
		{37, 100, 37},
		{50, 0, 50},  // devScale 0 → identity (headless MessageRaster{})
		{50, -5, 50}, // negative → identity
		// 200%: halves cleanly.
		{100, 200, 200},
		{101, 200, 202},
		// 150%: 3 → (3*150+50)/100 = 5 (the row that used to truncate to 4).
		{3, 150, 5},
		{5, 150, 8}, // (750+50)/100 = 8
		{7, 150, 11},
		// 125%: 3 → (375+50)/100 = 4 (the row that used to truncate to 3).
		{3, 125, 4},
		{7, 125, 9}, // (875+50)/100 = 9
		// 175%: 1 → (175+50)/100 = 2.
		{1, 175, 2},
	}
	for _, c := range cases {
		if got := deviceFromLogicalAt(c.logical, c.devScale); got != c.want {
			t.Errorf("deviceFromLogicalAt(%d, %d) = %d, want %d", c.logical, c.devScale, got, c.want)
		}
	}
}
