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

// TestDeviceLogicalRoundTrip pins that a MessageRaster's logical line-height metric
// stays close to the un-scaled value across scales (the raster rasterizes the SAME
// font at pt×scale, then Draw/Height divide back) — proving the scale folds into the
// point size and unfolds in the geometry, not into a doubled on-screen size.
func TestDeviceFromLogicalInverts(t *testing.T) {
	m := &MessageRaster{lineH: 20, devScale: 150}
	// logical(device) then device(logical) should land within 1px of the original
	// (round-trip through integer rounding at an odd scale).
	logical := logicalFromDevice(m.lineH, m.devScale)
	back := m.deviceFromLogical(logical)
	if d := back - m.lineH; d < -1 || d > 1 {
		t.Errorf("round-trip 20 device → %d logical → %d device drifted by %d (>1px)", logical, back, d)
	}
}
