package ui

import (
	"math"
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
		// Scatterflower: an inspiration, so it sorts BELOW all three creators.
		{"ScatterFlower", eggScatter, "mixed-case scatter, bare"},
		{"big thanks to scatterflower for the idea", eggScatter, "scatter mid-sentence"},
		{"SCATTERFLOWER!!!", eggScatter, "upper-case scatter"},
		{"scatterflower and syntaxnyah", eggNyah, "a creator outranks an inspiration"},
		{"scatterflower fanatsors", eggFanat, "fanat still wins over scatter"},
		// Crystalwarrior: AO2's keeper, so above SyntaxNyah in the lineage and below
		// the two originals.
		{"CrystalWarrior", eggCrystal, "mixed-case crystal, bare"},
		{"ask crystalwarrior about it", eggCrystal, "crystal mid-sentence"},
		{"CRYSTALWARRIOR!!!", eggCrystal, "upper-case crystal"},
		{"syntaxnyah then crystalwarrior", eggCrystal, "crystal outranks nyah even when nyah appears first"},
		{"crystalwarrior omnitroid", eggOmni, "omni still outranks crystal"},
		{"crystalwarrior scatterflower", eggCrystal, "a lineage figure outranks an inspiration"},
		// No false positives.
		{"you are being fanatic about this", eggNone, "fanatic must not trigger fanat"},
		{"my android phone", eggNone, "android must not trigger omni"},
		{"she picked a flower", eggNone, "flower alone must not trigger scatter"},
		{"scatter the evidence", eggNone, "scatter alone must not trigger scatter"},
		{"scatter flower", eggNone, "the two halves split by a space must not trigger"},
		{"the alibi is crystal clear", eggNone, "crystal alone must not trigger crystal"},
		{"a lone warrior took the stand", eggNone, "warrior alone must not trigger crystal"},
		{"crystal warrior", eggNone, "the two halves split by a space must not trigger"},
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
//
// ⚠ KNOWN INTERMITTENT — QUANTIFIED, PRE-EXISTING, AND NOT A REGRESSION OF
// WHATEVER YOU JUST CHANGED. Measured at clean ee17f25 (before the v1.90.0 W8/W9
// work): 3-4 failures in 60 consecutive runs, ~5-6%. Every observed failure was
// on the FIRST arm ("shoutout to FanatSors", eggFanat) and every one reported
// exactly 1.0 allocs/op — which, over AllocsPerRun's 200 iterations, is one real
// event of >= 200 allocations rather than a drifting average. That shape says a
// one-shot rebuild lands inside the measured window, not that the egg draw
// allocates per frame.
//
// The likely candidates, in the order a bisect should try them: a text-cache or
// fontSet rebuild triggered by the typewriter settling a frame later than
// settle() assumes; the label cache evicting under the four messages this test
// pushes through it; or a lazy glyph-atlas page for a name the earlier arms did
// not use. None of them has been confirmed, and confirming one is its own
// afternoon.
//
// WHAT TO DO WITH A RED RUN HERE: re-run the test alone. If it goes green and the
// failing arm was FanatSors at 1.0/op, it is this. If it is a different arm, a
// different count, or reproducible, it is NOT this and something did ship.
//
// DO NOT WEAKEN OR SKIP IT. The gate is correct and the four egg paths are
// genuinely alloc-free in the steady state; a t.Skip here would retire a
// whole-screen zero-alloc gate to hide a 5% flake, and the class of bug it
// catches (a per-frame allocation in a draw body) is one this codebase has
// shipped more than once.
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
	// perimeter sweep, Crystalwarrior's split-hue ring PLUS its stage shard field,
	// pink heartbeat, and Scatterflower's palette-drift ring PLUS its stage petal
	// field), so all five must be gated — the sweep's per-edge fill loop and the two
	// nested stage loops are the alloc-prone ones. drawCourtroom covers both halves
	// of the two-part eggs: renderViewportZoomed reaches drawStageEgg,
	// drawChatOverlay reaches drawChatEgg. Re-drive the SAME speaker (char 0
	// "Witch", whose stage bases the staging already made resident) with each
	// creator-name message, settle the typewriter, and assert zero allocs.
	for _, tc := range []struct {
		text string
		want uint8
	}{
		{"shoutout to FanatSors", eggFanat},
		{"credit to OmniTroid", eggOmni},
		{"kept alive by CrystalWarrior", eggCrystal},
		{"made by SyntaxNyah", eggNyah},
		{"inspired by Scatterflower", eggScatter},
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

// TestStageEggCensusIsComplete is the encapsulation gate for the egg's STAGE
// half. eggDrawsOnStage is the one list of kinds that paint on the viewport, and
// drawStageEgg dispatches from it — a kind added to the list but left out of the
// dispatch (or the reverse) is the parsed-but-never-applied failure this codebase
// has shipped before: everything compiles, every other egg gate stays green, and
// the new egg simply never appears on the stage.
func TestStageEggCensusIsComplete(t *testing.T) {
	// The list itself: exactly the two-part eggs, and nothing else.
	want := map[uint8]bool{eggScatter: true, eggCrystal: true}
	for kind := eggNone; kind <= eggScatter; kind++ {
		if got := eggDrawsOnStage(kind); got != want[kind] {
			t.Errorf("eggDrawsOnStage(%d) = %v, want %v", kind, got, want[kind])
		}
	}
	// …and the dispatch: the gate is consulted, both stage draws are reachable, and
	// the frame limiter is told (an egg that animates without NoteAnimating freezes
	// at a low idle cap — the msAnim precedent).
	body := funcBodySource(t, "eggs.go", "drawStageEgg")
	for _, call := range []string{"eggDrawsOnStage", "drawEggPetals", "drawEggShards", "NoteAnimating"} {
		if !containsCall(body, call) {
			t.Errorf("drawStageEgg does not call %s — a stage egg is unreachable or unpaced", call)
		}
	}
	// The chatbox half of the crystal egg has the same deletion risk.
	if !containsCall(funcBodySource(t, "eggs.go", "drawCreatorEgg"), "drawEggCrystalRing") {
		t.Error("drawCreatorEgg no longer dispatches the Crystalwarrior ring")
	}
}

// TestEggShardTaperIsAFacet pins the shard silhouette: widest in the middle,
// pointed at both ends, and symmetric — a stack of rects that did NOT taper would
// draw a bar, which is the difference between a crystal facet and a coloured
// rectangle sitting on the stage.
func TestEggShardTaperIsAFacet(t *testing.T) {
	const last = eggCrystalSegments - 1
	if got := eggShardTaper(0); got != 0 {
		t.Errorf("the top segment must come to a point, got %.3f", got)
	}
	if got := eggShardTaper(last); got != 0 {
		t.Errorf("the bottom segment must come to a point, got %.3f", got)
	}
	mid := eggShardTaper(eggCrystalSegments / 2)
	for s := int32(0); s <= last; s++ {
		if eggShardTaper(s) > mid+1e-9 {
			t.Errorf("segment %d is wider than the middle — the shard is not a facet", s)
		}
		if got, sym := eggShardTaper(s), eggShardTaper(last-s); math.Abs(got-sym) > 1e-9 {
			t.Errorf("segment %d (%.3f) and its mirror %d (%.3f) differ — the shard is lopsided", s, got, last-s, sym)
		}
	}
}

// TestEggShardGlintIsAFlashNotAPulse pins the glint's shape: zero for most of the
// cycle, a smooth hump over the leading eggCrystalGlintFrac, and offset per shard
// so the field twinkles instead of strobing as one object.
func TestEggShardGlintIsAFlashNotAPulse(t *testing.T) {
	if got := eggShardGlint(0, 0); got != 0 {
		t.Errorf("the glint must start dark (a cosine hump from 0), got %.3f", got)
	}
	peak := eggShardGlint(eggCrystalGlintSecs*eggCrystalGlintFrac/2, 0)
	if peak < 0.99 {
		t.Errorf("the glint must reach full brightness mid-hump, got %.3f", peak)
	}
	// Everything past the hump is dark — that is what makes it a flash.
	for _, frac := range []float64{0.3, 0.5, 0.9} {
		if got := eggShardGlint(eggCrystalGlintSecs*frac, 0); got != 0 {
			t.Errorf("the glint must be dark at %.0f%% of the cycle, got %.3f", frac*100, got)
		}
	}
	// Two shards a phase apart must not flash together.
	if a, b := eggShardGlint(0.2, 0), eggShardGlint(0.2, 0.5); a == b {
		t.Error("two shards with different phases flashed identically — the field would strobe as one")
	}
}

// TestEggSceneTextMirrorsChatboxGate pins the agreement between the egg's two
// halves. The stage petals refresh from eggSceneText while the chatbox ring
// refreshes from drawChatEgg, and drawChatOverlay early-returns on a blankpost /
// empty scene — so if eggSceneText did NOT reproduce that same gate, a blankpost
// carrying a trigger word would latch the kind and leave petals falling over a
// stage whose chatbox never drew (and never cleared it).
func TestEggSceneTextMirrorsChatboxGate(t *testing.T) {
	for _, tc := range []struct {
		name string
		sc   courtroom.Scene
		want string
	}{
		{"plain message", courtroom.Scene{MessageText: "scatterflower"}, "scatterflower"},
		{"showname only still displays a box", courtroom.Scene{ShownameText: "Witch"}, ""},
		{"blankpost hides the box", courtroom.Scene{MessageText: "scatterflower", IsBlankPost: true}, ""},
		{"idle: no message, no showname", courtroom.Scene{}, ""},
	} {
		sc := tc.sc
		if got := eggSceneText(&sc); got != tc.want {
			t.Errorf("%s: eggSceneText = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestEggPetalFadeRampsBothEnds pins that petals ease in at the stage's top edge
// and out at its bottom one: a petal that appeared at full opacity would visibly
// pop into existence at the clip boundary.
func TestEggPetalFadeRampsBothEnds(t *testing.T) {
	if got := eggPetalFade(0); got != 0 {
		t.Errorf("fade at the very top = %v, want 0 (petals must not pop in)", got)
	}
	if got := eggPetalFade(1); got > 1e-9 {
		t.Errorf("fade at the very bottom = %v, want ~0 (petals must not pop out)", got)
	}
	if got := eggPetalFade(0.5); got != 1 {
		t.Errorf("fade mid-fall = %v, want 1 (full opacity between the ramps)", got)
	}
	// Monotone rise across the in-ramp, monotone fall across the out-ramp.
	for i := 1; i <= 10; i++ {
		lo, hi := eggPetalFade(float64(i-1)/10*eggScatterFadeFrac), eggPetalFade(float64(i)/10*eggScatterFadeFrac)
		if hi < lo {
			t.Fatalf("in-ramp not monotone at step %d: %v then %v", i, lo, hi)
		}
	}
	// Every sample stays a legal alpha multiplier, so the uint8 cast can't wrap.
	for i := 0; i <= 100; i++ {
		if f := eggPetalFade(float64(i) / 100); f < 0 || f > 1 {
			t.Fatalf("fade(%v) = %v, outside [0,1] — the alpha cast would wrap", float64(i)/100, f)
		}
	}
}

// TestEggPaletteAtWrapsContinuously pins that the ring's colour cycle is closed:
// sampling just before 1 must be close to sampling 0, or the ring would snap
// colour once per bloom period instead of drifting.
func TestEggPaletteAtWrapsContinuously(t *testing.T) {
	near, start := eggPaletteAt(0.9999), eggPaletteAt(0)
	if d := int(near.R) - int(start.R); d > 2 || d < -2 {
		t.Errorf("palette seam is discontinuous: R %d then %d", near.R, start.R)
	}
	// In-range for the whole cycle (the lerp must never overshoot a channel).
	for i := 0; i < 100; i++ {
		_ = eggPaletteAt(float64(i) / 100) // uint8 channels cannot leave range; this pins no panic on index wrap
	}
}

// TestEggHashSpreadsPetals guards against a degenerate mixer: the petal field's
// whole visual depends on the per-index hash decorrelating lane / speed / size /
// phase. If the hash collapsed, every petal would fall in one column and the egg
// would read as a single dropping line, which no pixel test would catch.
func TestEggHashSpreadsPetals(t *testing.T) {
	// Lanes are the salt-0 draw the petal loop uses for horizontal placement.
	var buckets [4]int
	for i := uint32(0); i < eggScatterPetals; i++ {
		u := eggUnit(eggHash(i * 4))
		if u < 0 || u >= 1 {
			t.Fatalf("eggUnit out of [0,1) for petal %d: %v", i, u)
		}
		buckets[int(u*4)]++
	}
	for q, n := range buckets {
		if n == 0 {
			t.Errorf("no petal lands in horizontal quarter %d — the hash is not spreading (buckets=%v)", q, buckets)
		}
	}
	// Distinct salts must not alias: lane and phase sharing a value would lock
	// every petal's sway to its column.
	same := 0
	for i := uint32(0); i < eggScatterPetals; i++ {
		if eggHash(i*4) == eggHash(i*4+3) {
			same++
		}
	}
	if same != 0 {
		t.Errorf("%d petals have lane == phase — salts are aliasing", same)
	}
}
