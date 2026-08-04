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
