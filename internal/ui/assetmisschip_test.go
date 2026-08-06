package ui

import (
	"testing"
	"time"
)

// TestAssetMissRingDedupsWithinTheCooldown pins the burst suppressor behind GH #27.
// The old banner re-stamped its 12 s timer on EVERY warning with no dedup, and the
// desk-heal loop re-demanded a conclusively-missing base up to 8 times per message
// at a 2 s cadence — so a background that legitimately ships no desk made the red
// line permanent, which also pinned the frame pacer at full rate (warnActive is in
// the full-rate gate).
func TestAssetMissRingDedupsWithinTheCooldown(t *testing.T) {
	var r assetMissRing
	t0 := time.Now()
	const base = "local://m-1/background/ocartgallery/stand"
	if r.seen(base, t0, assetMissRepeatCooldown) {
		t.Fatal("the FIRST report of a base must not be deduped")
	}
	for i := 1; i <= 9; i++ { // the observed x5..x9 runs
		at := t0.Add(time.Duration(i) * 2 * time.Second)
		if !r.seen(base, at, assetMissRepeatCooldown) {
			t.Fatalf("repeat %d at +%v escaped the cooldown", i, at.Sub(t0))
		}
	}
	if r.seen(base, t0.Add(assetMissRepeatCooldown+time.Second), assetMissRepeatCooldown) {
		t.Error("past the cooldown the base must report again")
	}
	// A different base is never suppressed by its neighbour.
	if r.seen("local://m-1/sounds/blips/typewriter", t0, assetMissRepeatCooldown) {
		t.Error("a DIFFERENT base must not be deduped")
	}
}

// TestAssetMissRingIsBounded pins hard rule 4: the ring is a fixed array, so a
// server that 404s hundreds of distinct bases cannot grow it. Eviction is
// acceptable (the debug log is the history); unbounded growth is not.
func TestAssetMissRingIsBounded(t *testing.T) {
	var r assetMissRing
	now := time.Now()
	for i := 0; i < assetMissDedupCap*4; i++ {
		r.seen(string(rune('a'+i%26))+"/base", now, assetMissRepeatCooldown)
	}
	if len(r.base) != assetMissDedupCap || len(r.at) != assetMissDedupCap {
		t.Fatalf("the ring grew: %d/%d slots", len(r.base), len(r.at))
	}
	if r.next < 0 || r.next >= assetMissDedupCap {
		t.Errorf("write cursor out of range: %d", r.next)
	}
}

// TestAssetMissTailDropsTheOrigin pins the readability half. The old banner
// carried the whole base including the mount pseudo-origin
// ("local://m-73c0c5c0e1b49612/background/...") plus the full tried-extension
// tail, which is what made it unreadable. The chip's tooltip names the last two
// path segments; the Debug > Log tab keeps every byte.
func TestAssetMissTailDropsTheOrigin(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"local://m-73c0c5c0e1b49612/background/ocartgallery/stand", "ocartgallery/stand"},
		{"https://cdn.example.com/base/sounds/blips/typewriter", "blips/typewriter"},
		{"https://cdn.example.com/base/", "cdn.example.com/base"},
		{"stand", "stand"},
	} {
		if got := assetMissTail(tc.in); got != tc.want {
			t.Errorf("assetMissTail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDebugLogTabIsResolvedByName pins the chip's click target against a
// reordering of the Debug panel's tab table — an index literal would silently send
// the user to Perf.
func TestDebugLogTabIsResolvedByName(t *testing.T) {
	i := assetMissDebugLogTab()
	if i < 0 || i >= len(debugSections) || debugSections[i] != "Log" {
		t.Fatalf("assetMissDebugLogTab() = %d, which is %v, want the Log tab", i, debugSections)
	}
}

// TestInvisibleAssetMissChipReportsNoRect pins the shared-geometry helper's honesty
// (ledger L64, under L20).
//
// assetMissChipRect gated on menuBarPaints() ALONE, so a chip that was not on screen
// — no misses, the preference off, or the 30 s badge expired — still reported a plate
// roughly its label's width. The band's geometry resolves as a CHAIN
// (menuBarLeftContentRight → the Iniswap chip → menuBarRightContentLeft → the refusal
// chip) and every link expects "no rect" from a chip that is not drawing; a phantom
// answer reserves pixels for nothing. The two shipped callers check Active()
// themselves, so this changes no painted frame — it makes the helper answerable by a
// third caller, which is the whole point of a SHARED geometry helper.
//
// It does NOT touch the ruled two-answers-per-frame skew between the input pass and
// drawMenuBar's phase 2; that is a different, deliberate property.
func TestInvisibleAssetMissChipReportsNoRect(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()
	a.drawCourtroom(1280, 720) // one frame so the band's own latches are live
	const w = int32(1280)

	// A FAILURE, never a skip: the frame-driven harness proves a plain courtroom
	// frame paints the band (frameharness_test.go,
	// TestSidecarRefusalChipReachesTheScreenAndTakesItsClick), so a bar that is
	// not painting here means the fixture stopped staging a courtroom — and a
	// skip would retire every assertion below it in silence.
	if !a.menuBarPaints() {
		t.Fatal("the menu bar is not painting in this fixture — the chip's geometry chain has no home, " +
			"so nothing below measures anything")
	}
	// Nothing missing: inactive, so no rect.
	a.assetMissCount = 0
	if r, ok := a.assetMissChipRect(w); ok {
		t.Errorf("an INACTIVE missing-asset chip reported the rect %+v — the band's geometry chain "+
			"would reserve those pixels for a chip nobody can see", r)
	}
	// Armed and inside its window: a real rect, so the gate filters rather than
	// standing the helper down.
	a.d.Prefs.SetAssetWarnings(true)
	a.assetMissCount, a.assetMissAt = 3, a.now()
	if !a.assetMissChipActive() {
		t.Fatal("the fixture did not arm the chip — the positive half would be vacuous")
	}
	live, ok := a.assetMissChipRect(w)
	if !ok || live.W <= 0 {
		t.Fatalf("an ACTIVE chip reported no rect (%+v, ok=%v)", live, ok)
	}
	// The preference is the user's own silence switch, and it must reach the GEOMETRY
	// as well as the paint.
	a.d.Prefs.SetAssetWarnings(false)
	if r, ok := a.assetMissChipRect(w); ok {
		t.Errorf("with missing-asset reporting turned OFF the chip still reported %+v", r)
	}
	// And so must the 30 s expiry.
	a.d.Prefs.SetAssetWarnings(true)
	a.assetMissAt = a.now().Add(-assetMissChipShowDuration - time.Second)
	if r, ok := a.assetMissChipRect(w); ok {
		t.Errorf("an EXPIRED chip still reported %+v", r)
	}
}
