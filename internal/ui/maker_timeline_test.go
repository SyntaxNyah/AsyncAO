package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestMakerTimelineLayout pins the #75 timeline geometry: segment widths follow
// the recorded OffsetMs pacing (clamped), every segment keeps a clickable min
// width, hit-testing maps an x to the right segment, and a scene with no recorded
// pacing falls back to equal widths instead of a degenerate bar.
func TestMakerTimelineLayout(t *testing.T) {
	a := &App{}
	// A 3s message, a 0.2s one, then a scene change (last → tail).
	evs := []recEvent{
		{OffsetMs: 0, Kind: int(courtroom.EventMessage)},
		{OffsetMs: 3000, Kind: int(courtroom.EventMessage)},
		{OffsetMs: 3200, Kind: int(courtroom.EventBackground)},
	}
	durs := makerTLDurations(evs, nil)
	if len(durs) != 3 || durs[0] != 3000 || durs[1] != 200 || durs[2] != makerTLTailMs {
		t.Fatalf("durations = %v, want [3000 200 %v]", durs, makerTLTailMs)
	}

	contentW := a.makerTLLayout(evs, 600)
	if len(a.makerSegW) != 3 || contentW <= 0 {
		t.Fatalf("layout = %v widths / contentW %d", a.makerSegW, contentW)
	}
	for i, w := range a.makerSegW {
		if w < makerSegMinPx {
			t.Errorf("segment %d width %d below the clickable floor %d", i, w, makerSegMinPx)
		}
	}
	if a.makerSegW[0] <= a.makerSegW[1] {
		t.Errorf("the 3s segment (%d) should be wider than the 0.2s one (%d)", a.makerSegW[0], a.makerSegW[1])
	}

	// Hit-test: a point in the middle of segment 1 resolves to index 1 (strip
	// left edge at x=0, no scroll).
	mid1 := a.makerSegX[1] + a.makerSegW[1]/2
	if got := a.makerTLSegAt(mid1, 0); got != 1 {
		t.Errorf("makerTLSegAt(mid of seg 1) = %d, want 1", got)
	}

	// No recorded pacing (all-zero offsets) → equal widths, none collapsed.
	flat := []recEvent{{}, {}, {}, {}}
	a.makerTLLayout(flat, 600)
	for i := 1; i < len(a.makerSegW); i++ {
		if a.makerSegW[i] != a.makerSegW[0] {
			t.Fatalf("all-zero offsets must give equal widths, got %v", a.makerSegW)
		}
	}
}
