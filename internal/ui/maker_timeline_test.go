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

// TestReindexAfterMove pins the pure index remap a reorder applies to the
// selection + crop endpoints, in both directions.
func TestReindexAfterMove(t *testing.T) {
	// Move src→dst, then every original index should map to its new slot.
	cases := []struct {
		src, dst int
		want     map[int]int // old index → new index over [0,1,2,3]
	}{
		{0, 2, map[int]int{0: 2, 1: 0, 2: 1, 3: 3}}, // forward
		{3, 1, map[int]int{0: 0, 1: 2, 2: 3, 3: 1}}, // backward
		{2, 2, map[int]int{0: 0, 1: 1, 2: 2, 3: 3}}, // no-op
	}
	for _, tc := range cases {
		for old, want := range tc.want {
			if got := reindexAfterMove(old, tc.src, tc.dst); got != want {
				t.Errorf("reindexAfterMove(%d, src=%d, dst=%d) = %d, want %d", old, tc.src, tc.dst, got, want)
			}
		}
	}
}

// TestMakerMoveEvent pins the end-to-end reorder: the slice is reordered and the
// selection + crop In/Out follow their events (not their old slots).
func TestMakerMoveEvent(t *testing.T) {
	a := &App{makerScene: &sceneRecording{Events: []recEvent{
		{Text: "0"}, {Text: "1"}, {Text: "2"}, {Text: "3"},
	}}}
	a.makerSel = 0
	a.makerTrimStart, a.makerTrimEnd = 1, 2 // crop covers events "1","2"

	a.makerMoveEvent(0, 2) // move event "0" to index 2 → order 1,2,0,3

	got := make([]string, len(a.makerScene.Events))
	for i, e := range a.makerScene.Events {
		got[i] = e.Text
	}
	if want := []string{"1", "2", "0", "3"}; !equalStrings(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if a.makerSel != 2 { // the moved event stays selected
		t.Errorf("makerSel = %d, want 2 (follows the moved event)", a.makerSel)
	}
	if a.makerTrimStart != 0 || a.makerTrimEnd != 1 { // crop still covers events "1","2", now at 0..1
		t.Errorf("crop = [%d,%d], want [0,1] (follows the cropped events)", a.makerTrimStart, a.makerTrimEnd)
	}

	// An out-of-range destination is a no-op (the ▲/▼ buttons at an end).
	before := a.makerSel
	a.makerMoveEvent(0, -1)
	a.makerMoveEvent(0, 99)
	if a.makerSel != before || a.makerScene.Events[2].Text != "0" {
		t.Errorf("out-of-range move must be a no-op")
	}
}

// TestMakerTLGapAt pins the drop-gap hit-test: cursor x → insertion gap 0..n.
func TestMakerTLGapAt(t *testing.T) {
	a := &App{}
	a.makerTLLayout([]recEvent{{}, {}, {}, {}}, 400) // 4 equal segments, no scroll
	n := len(a.makerSegW)

	if g := a.makerTLGapAt(0, 0); g != 0 {
		t.Errorf("far-left gap = %d, want 0", g)
	}
	mid0 := a.makerSegX[0] + a.makerSegW[0]/2
	if g := a.makerTLGapAt(mid0, 0); g != 1 { // at/after seg0's midpoint → drop after seg0
		t.Errorf("gap at seg0 midpoint = %d, want 1", g)
	}
	if g := a.makerTLGapAt(mid0-1, 0); g != 0 { // before it → drop before seg0
		t.Errorf("gap before seg0 midpoint = %d, want 0", g)
	}
	far := a.makerSegX[n-1] + a.makerSegW[n-1] + 50
	if g := a.makerTLGapAt(far, 0); g != n {
		t.Errorf("far-right gap = %d, want %d", g, n)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
