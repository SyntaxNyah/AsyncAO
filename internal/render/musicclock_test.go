package render

import (
	"fmt"
	"testing"
	"time"
)

// TestSwapSnapBookkeeping pins the takeover-snapshot map (wave 13): record →
// lookup round-trips the exact values, a re-record of an existing URL updates in
// place WITHOUT growing the insertion-order slice (so order can't desync from the
// map), and overflow past musicSwapSnapCap evicts the OLDEST entry. Pure map
// logic — the Audio is constructed directly (no SDL device), like
// TestCurrentMusicURLReflectsLiveStream.
func TestSwapSnapBookkeeping(t *testing.T) {
	a := &Audio{}
	at := time.Unix(1_700_000_000, 0)

	// Record → lookup round-trips the values.
	a.recordSwapSnap("http://cdn/a.opus", 12.5, 180, at)
	pos, dur, gotAt, ok := a.SwappedOutSnap("http://cdn/a.opus")
	if !ok {
		t.Fatal("recorded snapshot must be found")
	}
	if pos != 12.5 || dur != 180 || !gotAt.Equal(at) {
		t.Errorf("snap = (%v,%v,%v), want (12.5,180,%v)", pos, dur, gotAt, at)
	}

	// A miss reports ok=false and zero values.
	if _, _, _, ok := a.SwappedOutSnap("http://cdn/nope.opus"); ok {
		t.Error("an unrecorded URL must report ok=false")
	}

	// Empty URL is ignored (never recorded).
	a.recordSwapSnap("", 1, 2, at)
	if _, _, _, ok := a.SwappedOutSnap(""); ok {
		t.Error("an empty URL must never be recorded")
	}

	// Re-record of an existing URL updates in place and does NOT grow the order
	// slice (a duplicate order entry would evict the wrong key at cap).
	orderBefore := len(a.swapSnapOrder)
	a.recordSwapSnap("http://cdn/a.opus", 99, 200, at.Add(time.Minute))
	if len(a.swapSnapOrder) != orderBefore {
		t.Errorf("re-record grew swapSnapOrder to %d, want %d (update-in-place)", len(a.swapSnapOrder), orderBefore)
	}
	pos, dur, _, _ = a.SwappedOutSnap("http://cdn/a.opus")
	if pos != 99 || dur != 200 {
		t.Errorf("re-record didn't update value: got (%v,%v), want (99,200)", pos, dur)
	}
}

// TestSwapSnapEvictsOldestAtCap fills the map past musicSwapSnapCap and asserts
// the FIRST-inserted URL is the one evicted, and the map never exceeds the cap.
func TestSwapSnapEvictsOldestAtCap(t *testing.T) {
	a := &Audio{}
	at := time.Unix(1_700_000_000, 0)
	// Insert cap+1 distinct URLs; url0 is the oldest and must be evicted.
	for i := 0; i <= musicSwapSnapCap; i++ {
		a.recordSwapSnap(fmt.Sprintf("http://cdn/%d.opus", i), float64(i), 180, at)
	}
	if len(a.swapSnaps) != musicSwapSnapCap {
		t.Errorf("map size = %d, want cap %d", len(a.swapSnaps), musicSwapSnapCap)
	}
	if len(a.swapSnapOrder) != musicSwapSnapCap {
		t.Errorf("order size = %d, want cap %d", len(a.swapSnapOrder), musicSwapSnapCap)
	}
	if _, _, _, ok := a.SwappedOutSnap("http://cdn/0.opus"); ok {
		t.Error("oldest URL (0) must have been evicted at cap")
	}
	// The most-recently inserted survives.
	if _, _, _, ok := a.SwappedOutSnap(fmt.Sprintf("http://cdn/%d.opus", musicSwapSnapCap)); !ok {
		t.Error("newest URL must survive")
	}
}
