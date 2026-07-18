package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// TestCreatorEgg pins the detection scan: each creator name in mixed case and
// mid-sentence resolves to its egg, multiple-name messages honour the documented
// fanat > omni > nyah priority, and near-miss words / plain text / empty never
// false-trigger.
func TestCreatorEgg(t *testing.T) {
	cases := []struct {
		text string
		want uint8
		why  string
	}{
		// Each name, mixed case, embedded mid-sentence.
		{"thanks FanatSors for AO", eggFanat, "mixed-case fanat mid-sentence"},
		{"all hail fanatsors!!!", eggFanat, "lower-case fanat"},
		{"OMNITROID made AO2", eggOmni, "upper-case omni"},
		{"shoutout to OmniTroid here", eggOmni, "mixed-case omni mid-sentence"},
		{"written by SyntaxNyah btw", eggNyah, "mixed-case nyah mid-sentence"},
		{"syntaxnyah", eggNyah, "bare lower-case nyah"},
		// Multiple names in one message → first in fanat>omni>nyah wins,
		// regardless of textual position (lineage order AO→AO2→AsyncAO).
		{"omnitroid and fanatsors", eggFanat, "fanat outranks omni even when omni appears first"},
		{"syntaxnyah then omnitroid", eggOmni, "omni outranks nyah even when nyah appears first"},
		{"fanatsors omnitroid syntaxnyah", eggFanat, "all three → fanat"},
		// No false positives.
		{"you are being fanatic about this", eggNone, "fanatic must not trigger fanat"},
		{"my android phone", eggNone, "android must not trigger omni"},
		{"plain courtroom banter", eggNone, "ordinary text is inert"},
		{"", eggNone, "empty text is inert"},
	}
	for _, tc := range cases {
		if got := creatorEgg(tc.text); got != tc.want {
			t.Errorf("creatorEgg(%q) = %d, want %d (%s)", tc.text, got, tc.want, tc.why)
		}
	}
}

// TestRefreshEggKindCachesScan pins the compare-and-store guard: a new message
// rescans and stores; the SAME text again does NOT rescan (returns false); a
// changed text rescans anew. This is the once-per-message contract that keeps a
// settled frame at one string compare — tested via the pure guard, no SDL.
func TestRefreshEggKindCachesScan(t *testing.T) {
	a := &App{}

	if scanned := a.refreshEggKind("hello fanatsors"); !scanned {
		t.Fatal("first sight of a message must rescan")
	}
	if a.eggKind != eggFanat {
		t.Fatalf("stored egg = %d, want eggFanat", a.eggKind)
	}
	if scanned := a.refreshEggKind("hello fanatsors"); scanned {
		t.Error("the same text must NOT rescan (one scan per message)")
	}
	if a.eggKind != eggFanat {
		t.Errorf("cached egg changed on a no-op refresh: %d", a.eggKind)
	}
	// A changed message rescans and updates the kind.
	if scanned := a.refreshEggKind("now with syntaxnyah"); !scanned {
		t.Error("a changed message must rescan")
	}
	if a.eggKind != eggNyah {
		t.Errorf("stored egg = %d, want eggNyah after text change", a.eggKind)
	}
	// A plain message clears the egg (self-clearing when the trigger leaves the box).
	if scanned := a.refreshEggKind("plain line"); !scanned {
		t.Error("clearing text must rescan")
	}
	if a.eggKind != eggNone {
		t.Errorf("egg not cleared: %d", a.eggKind)
	}
}

// TestRingVisibleSkipsOnlyOffScreen pins the artifact fix: the OUTLINE rings use
// a skip-ONLY test (never clamp), so a ring that runs off a window edge is drawn
// in full and clipped by SDL — clamping its rect would make DrawRect paint a
// spurious colored line along the window edge. A fully-off-window ring is skipped;
// a partially-visible one is kept (drawn, then SDL-clipped). The sweep FILLS keep
// the in-place clamp, which is correct (a clamped fill has no spurious edge).
func TestRingVisibleSkipsOnlyOffScreen(t *testing.T) {
	win := sdl.Rect{X: 0, Y: 0, W: 1280, H: 720}
	// A ring straddling the left edge (negative X) must be KEPT — not clamped away.
	edge := sdl.Rect{X: -6, Y: 100, W: 200, H: 60}
	if !ringVisible(edge, win) {
		t.Error("a ring straddling the window edge must be kept (drawn in full, SDL-clipped)")
	}
	// A ring entirely off-window is skipped.
	if ringVisible(sdl.Rect{X: 2000, Y: 100, W: 50, H: 50}, win) {
		t.Error("a fully off-window ring must be skipped")
	}
	// A ring flush to the top-left touching (0,0) is visible.
	if !ringVisible(sdl.Rect{X: 0, Y: 0, W: 40, H: 40}, win) {
		t.Error("a ring at the origin must be visible")
	}
	// clampRingToWindow (sweep fills) DOES clamp in place and reports visibility.
	fill := edge
	if !clampRingToWindow(&fill, win) {
		t.Fatal("a partly-visible fill must clamp to something visible")
	}
	if fill.X != 0 || fill.W != 194 { // -6..194 clamps to 0..194
		t.Errorf("clamp math wrong: got X=%d W=%d, want X=0 W=194", fill.X, fill.W)
	}
	off := sdl.Rect{X: -100, Y: 100, W: 50, H: 50} // fully left of the window
	if clampRingToWindow(&off, win) {
		t.Error("a fully off-window fill must report not-visible")
	}
}

// TestPerimSegment pins the sweep geometry: perimSegment maps a 1-D perimeter
// offset (clockwise from the ring's top-left, going right along the top) to an
// edge-aligned rect on the edge that offset sits on, and reports how much
// perimeter length it consumed. TestDrawCourtroomEggZeroAlloc only COUNTS
// allocations, so a sweep drawn on the wrong edge, off-by-N, or with a spurious
// corner rect would fail no other test — this is the correctness gate for the
// most intricate math in the eggs change.
//
// The ring is {100,200,400,300}: w=400, h=300, so the edge boundaries along the
// perimeter are top=400, right=700, bottom=1100, and the full perimeter is 1400.
// Each row starts an integer offset on a known edge so `consumed` compares
// exactly. Rather than assert opaque rect literals, we check the geometric
// INVARIANT per edge (a top/bottom bar is th tall and hugs the top/bottom side;
// a left/right bar is th wide and hugs the left/right side), so a future
// edge-flip or off-by-one fails with a legible message instead of a bare diff.
func TestPerimSegment(t *testing.T) {
	ring := sdl.Rect{X: 100, Y: 200, W: 400, H: 300}
	const th = int32(3)
	// Edge classification for the message: which side the bar must hug.
	const (
		edgeTop = iota
		edgeRight
		edgeBottom
		edgeLeft
	)
	cases := []struct {
		name         string
		start        float64
		length       float64
		edge         int
		wantConsumed float64
	}{
		// Full-length runs, one starting at the head of each edge.
		{"top head", 0, 50, edgeTop, 50},
		{"right head", 400, 50, edgeRight, 50},
		{"bottom head", 700, 50, edgeBottom, 50},
		{"left head", 1100, 50, edgeLeft, 50},
		// A run that starts late on the TOP edge and would overrun the corner is
		// clamped to the rest of that edge (consumed=20, the caller then continues
		// onto the right edge) — this is the per-edge split that keeps a sweep from
		// painting a spurious corner rect.
		{"top clamps at corner", 380, 50, edgeTop, 20},
		// The LEFT edge clamps at the perimeter/seam boundary (2*(w+h)=1400): a run
		// from 1380 consumes only the last 20. The actual wrap-around back onto the
		// top edge is the caller's math.Mod in drawEggSweep (needs SDL, out of unit
		// scope); here we pin that perimSegment itself stops exactly at the seam.
		{"left clamps at seam", 1380, 50, edgeLeft, 20},
	}
	for _, tc := range cases {
		r, consumed := perimSegment(ring, tc.start, tc.length, th)
		if consumed != tc.wantConsumed {
			t.Errorf("%s: consumed = %v, want %v", tc.name, consumed, tc.wantConsumed)
		}
		// The visible bar length is `consumed`; the code adds +1 px so adjacent
		// segments overlap by a pixel and leave no seam gap.
		wantSpan := int32(tc.wantConsumed) + 1
		switch tc.edge {
		case edgeTop:
			if r.H != th || r.Y != ring.Y {
				t.Errorf("%s: not on the TOP edge (H=%d Y=%d, want H=%d Y=%d)", tc.name, r.H, r.Y, th, ring.Y)
			}
			if r.W != wantSpan {
				t.Errorf("%s: top bar span W=%d, want %d", tc.name, r.W, wantSpan)
			}
			if r.X != ring.X+int32(tc.start) {
				t.Errorf("%s: top bar X=%d, want %d", tc.name, r.X, ring.X+int32(tc.start))
			}
		case edgeBottom:
			if r.H != th || r.Y != ring.Y+ring.H-th {
				t.Errorf("%s: not on the BOTTOM edge (H=%d Y=%d, want H=%d Y=%d)", tc.name, r.H, r.Y, th, ring.Y+ring.H-th)
			}
			if r.W != wantSpan {
				t.Errorf("%s: bottom bar span W=%d, want %d", tc.name, r.W, wantSpan)
			}
			// Travel axis: the bottom edge runs right→left, so the bar's X sits at
			// the edge head (perimeter offset 700 = the ring's right end) minus how
			// far in the run starts and the span it covers.
			if r.X != ring.X+ring.W-int32(tc.start-700)-int32(tc.wantConsumed) {
				t.Errorf("%s: bottom bar X=%d, want %d", tc.name, r.X, ring.X+ring.W-int32(tc.start-700)-int32(tc.wantConsumed))
			}
		case edgeRight:
			if r.W != th || r.X != ring.X+ring.W-th {
				t.Errorf("%s: not on the RIGHT edge (W=%d X=%d, want W=%d X=%d)", tc.name, r.W, r.X, th, ring.X+ring.W-th)
			}
			if r.H != wantSpan {
				t.Errorf("%s: right bar span H=%d, want %d", tc.name, r.H, wantSpan)
			}
			if r.Y != ring.Y+int32(tc.start-400) {
				t.Errorf("%s: right bar Y=%d, want %d", tc.name, r.Y, ring.Y+int32(tc.start-400))
			}
		case edgeLeft:
			if r.W != th || r.X != ring.X {
				t.Errorf("%s: not on the LEFT edge (W=%d X=%d, want W=%d X=%d)", tc.name, r.W, r.X, th, ring.X)
			}
			if r.H != wantSpan {
				t.Errorf("%s: left bar span H=%d, want %d", tc.name, r.H, wantSpan)
			}
			// Travel axis: the left edge runs bottom→top, so the bar's Y sits at
			// the edge head (perimeter offset 1100 = the ring's bottom end) minus
			// how far in the run starts and the span it covers.
			if r.Y != ring.Y+ring.H-int32(tc.start-1100)-int32(tc.wantConsumed) {
				t.Errorf("%s: left bar Y=%d, want %d", tc.name, r.Y, ring.Y+ring.H-int32(tc.start-1100)-int32(tc.wantConsumed))
			}
		}
	}
}

// TestDrawCourtroomEggZeroAlloc gates the egg DRAW path (rings + sweep +
// heartbeat) at zero allocations per frame. The existing whole-screen gate
// stages a NON-triggering message, so it never exercises the egg branch; this
// sibling drives a creator-name message (ScreenEffects on, ReduceMotion off) so
// the full drawCreatorEgg runs each measured frame. A non-zero count means a
// per-frame allocation shipped in the egg draw (fix it, don't loosen the gate).
func TestDrawCourtroomEggZeroAlloc(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	// The accessibility gate must be OPEN for the egg to draw at all.
	a.d.Prefs.SetScreenEffects(true)
	a.d.Prefs.SetReduceMotion(false)

	const w, h = 1280, 720
	// drawCreatorEgg clamps its rings to (0,0,winW,winH); Frame (which normally
	// sets these) never runs in this harness, so pin them or every ring clamps to
	// nothing and the fills — the primitives the gate most needs to measure — are
	// skipped.
	a.winW, a.winH = w, h
	draw := func() { a.drawCourtroom(w, h) }

	// Each egg has a DISTINCT draw path (rainbow rings, blue<->gold pulse + the
	// perimeter sweep, pink heartbeat), so all three must be gated — the sweep's
	// per-edge fill loop is the alloc-prone one. Re-drive the SAME speaker (char 0
	// "Witch", whose stage bases the staging already made resident) with each
	// creator-name message, settle the typewriter, and assert zero allocs.
	for _, tc := range []struct {
		text string
		want uint8
	}{
		{"shoutout to FanatSors", eggFanat},
		{"credit to OmniTroid", eggOmni},
		{"made by SyntaxNyah", eggNyah},
	} {
		a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(0, "Witch", tc.text)})
		a.room.SkipToIdle()
		settle(draw)
		// Sanity: the egg must actually resolve, or this gate would pass vacuously.
		if a.eggKind != tc.want {
			t.Fatalf("egg for %q = %d, want %d — the zero-alloc gate would measure the wrong (or no) path", tc.text, a.eggKind, tc.want)
		}
		if n := testing.AllocsPerRun(200, draw); n != 0 {
			t.Fatalf("a settled %q egg frame allocates %.1f/op, want 0 — a per-frame allocation shipped in the egg draw", tc.text, n)
		}
	}
}
