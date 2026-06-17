package ui

import "testing"

// TestPushAreaHistory pins the MRU recent-areas list: move-to-front, dedup, a
// no-op (same backing slice — no churn) when the area is unchanged or empty, and
// the cap.
func TestPushAreaHistory(t *testing.T) {
	var h []string
	h = pushAreaHistory(h, "Lobby")
	h = pushAreaHistory(h, "Court 1")
	if len(h) != 2 || h[0] != "Court 1" || h[1] != "Lobby" {
		t.Fatalf("after two pushes = %v, want [Court 1 Lobby]", h)
	}
	// Re-entering the current area is a no-op (returns the same slice — no alloc).
	if got := pushAreaHistory(h, "Court 1"); &got[0] != &h[0] || len(got) != 2 {
		t.Error("re-pushing the current area must be a no-op")
	}
	if got := pushAreaHistory(h, ""); &got[0] != &h[0] {
		t.Error("an empty area name must be ignored (no-op)")
	}
	// Returning to a prior area move-to-fronts it (deduped, not duplicated).
	h = pushAreaHistory(h, "Lobby")
	if len(h) != 2 || h[0] != "Lobby" || h[1] != "Court 1" {
		t.Fatalf("after returning to Lobby = %v, want [Lobby Court 1]", h)
	}
	// Cap: pushing many distinct areas never grows past areaHistoryCap.
	for i := 0; i < areaHistoryCap+5; i++ {
		h = pushAreaHistory(h, "area-"+string(rune('a'+i)))
	}
	if len(h) > areaHistoryCap {
		t.Errorf("history len %d exceeds cap %d", len(h), areaHistoryCap)
	}
}
