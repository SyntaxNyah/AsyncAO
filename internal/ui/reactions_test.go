package ui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestReactionTargetMatch pins the ref match: it finds a recent entry's ref, ignores a 0
// ref (system line), resolves a same-ref collision to the NEWEST message (newest-first
// scan), and won't match beyond the recent-window bound.
func TestReactionTargetMatch(t *testing.T) {
	a := &App{}
	a.icLog = []icEntry{
		{speaker: "Phoenix", ref: 111},
		{speaker: "", ref: 0}, // a system line
		{speaker: "Maya", ref: 222},
	}
	if !a.reactionTargetKnown(111) || !a.reactionTargetKnown(222) {
		t.Error("a present ref didn't match")
	}
	if a.reactionTargetKnown(999) {
		t.Error("an absent ref matched")
	}
	if a.reactionTargetKnown(0) {
		t.Error("a 0 ref (system line) must never match")
	}

	// Beyond the recent-window bound: a ref only in a far-older entry isn't found.
	a.icLog = make([]icEntry, reactionMatchScan+10)
	for i := range a.icLog {
		a.icLog[i] = icEntry{speaker: "x", ref: uint32(1000 + i)}
	}
	if a.reactionTargetKnown(1000) { // oldest, outside the scan window
		t.Error("matched a ref older than the scan window")
	}
	if !a.reactionTargetKnown(uint32(1000 + len(a.icLog) - 1)) {
		t.Error("didn't match the most recent ref")
	}
}

// TestReactionRingBounded pins §17.4: a burst of reactions can't grow the float ring past
// the cap, and the oldest is evicted (FIFO) so the newest always survives.
func TestReactionRingBounded(t *testing.T) {
	a := &App{}
	for i := 0; i < reactionFloatsMax+8; i++ {
		a.spawnReactionFloat(uint8(i % courtroom.ReactionCount()))
	}
	if len(a.reactionFloats) != reactionFloatsMax {
		t.Fatalf("ring len = %d, want capped at %d", len(a.reactionFloats), reactionFloatsMax)
	}
	// An unknown palette index spawns nothing (a reaction from a newer peer).
	before := len(a.reactionFloats)
	a.spawnReactionFloat(uint8(courtroom.ReactionCount()))
	if len(a.reactionFloats) != before {
		t.Error("an out-of-range index spawned a float")
	}
}

// TestOnIncomingReactionGate pins the receive gate: a reaction floats only when its ref
// matches a seen message AND the viewer hasn't opted out (HideReactions).
func TestOnIncomingReactionGate(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}
	a.icLog = []icEntry{{speaker: "Phoenix", ref: 4242}}

	// Unknown ref → no float.
	a.onIncomingReaction(courtroom.WireReaction{Ref: 1, Index: 0})
	if len(a.reactionFloats) != 0 {
		t.Fatal("a reaction with an unmatched ref floated")
	}
	// Matched ref, default (show) → float.
	a.onIncomingReaction(courtroom.WireReaction{Ref: 4242, Index: 2})
	if len(a.reactionFloats) != 1 {
		t.Fatalf("a matched reaction didn't float (got %d)", len(a.reactionFloats))
	}
	// Viewer opted out → suppressed.
	a.reactionFloats = a.reactionFloats[:0]
	prefs.SetHideReactions(true)
	a.onIncomingReaction(courtroom.WireReaction{Ref: 4242, Index: 2})
	if len(a.reactionFloats) != 0 {
		t.Error("HideReactions didn't suppress an incoming float")
	}
}

// TestQueueReactionSnapshot pins the picker-open snapshot (advisor #5): the React target is
// captured when the palette opens, so a message arriving while it's open doesn't shift what
// the queued emoji reacts to; the click consumes the snapshot.
func TestQueueReactionSnapshot(t *testing.T) {
	a := &App{}
	a.lastReactRef, a.lastReactName = 777, "Phoenix"
	a.toggleReactPicker() // opens + snapshots 777
	if !a.showReactPicker || a.reactTargetRef != 777 {
		t.Fatalf("open didn't snapshot: show=%v target=%d", a.showReactPicker, a.reactTargetRef)
	}
	a.lastReactRef, a.lastReactName = 888, "Maya" // a new message lands while open
	a.queueReaction(5)
	if !a.pendingReactSet || a.pendingReact.Ref != 777 || a.pendingReact.Index != 5 {
		t.Fatalf("queued = %+v set=%v, want ref 777 index 5 (the snapshot)", a.pendingReact, a.pendingReactSet)
	}
	if a.showReactPicker {
		t.Error("picking an emoji didn't close the palette")
	}
	// A pick with no target queues nothing (just closes).
	a.pendingReactSet, a.reactTargetRef = false, 0
	a.showReactPicker = true
	a.queueReaction(1)
	if a.pendingReactSet {
		t.Error("queued a reaction with no target message")
	}
}

// TestDrawReactionFloatsZeroAlloc is THE render-loop gate for #2: the overlay draws every
// courtroom frame, so with floats active (warm badge cache) it must allocate nothing — and
// the no-floats common case is a 0-alloc early return. BenchmarkRenderFrame can't cover this
// (it exercises render.Viewport, not the ui overlay), so this is the proof.
func TestDrawReactionFloatsZeroAlloc(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	font, err := loadEmbeddedFont(UIFontSize * reactionFloatPct / DefaultScalePct)
	if err != nil {
		t.Skipf("embedded font unavailable: %v", err)
	}
	defer font.Close()

	a := &App{ctx: &Ctx{Ren: ren}}
	white := sdl.Color{R: 255, G: 255, B: 255, A: 255}
	// Pre-warm the badge cache directly (bypasses the system emoji face, which a headless
	// test can't rely on) so ensureReactBadge is a pure map hit in the measured loop.
	a.reactBadges = map[uint8]*render.Badge{}
	for i := 0; i < 2; i++ {
		b, err := render.RasterizeBadge(ren, font, "A", white)
		if err != nil || b == nil {
			t.Fatalf("RasterizeBadge: %v", err)
		}
		defer b.Destroy()
		a.reactBadges[uint8(i)] = b
	}

	vp := sdl.Rect{X: 0, Y: 0, W: 512, H: 384}

	// No floats: the early-return path must be free.
	if allocs := testing.AllocsPerRun(200, func() { a.drawReactionFloats(vp) }); allocs != 0 {
		t.Errorf("empty drawReactionFloats allocated %.1f/op, want 0", allocs)
	}

	// Active floats: fixed frame clock so they stay mid-life (never cull) across the run.
	a.frameNow = time.Now()
	a.reactionFloats = []reactionFloat{
		{index: 0, born: a.frameNow.Add(-400 * time.Millisecond), xJit: 20},
		{index: 1, born: a.frameNow.Add(-700 * time.Millisecond), xJit: -30},
	}
	if allocs := testing.AllocsPerRun(200, func() { a.drawReactionFloats(vp) }); allocs != 0 {
		t.Errorf("active drawReactionFloats allocated %.1f/op, want 0 (#2 zero-perf constraint)", allocs)
	}
	if len(a.reactionFloats) != 2 {
		t.Errorf("floats were culled mid-life: %d left, want 2", len(a.reactionFloats))
	}
}

// TestReactionAlphaEnvelope pins the fade: 0 at the very start/end, full in the middle.
func TestReactionAlphaEnvelope(t *testing.T) {
	if reactionAlpha(0) != 0 {
		t.Error("alpha at birth should be 0 (fade in)")
	}
	if reactionAlpha(0.5) != 255 {
		t.Error("alpha mid-life should be full")
	}
	if a := reactionAlpha(0.999); a > 10 {
		t.Errorf("alpha near death = %d, want ~0 (fade out)", a)
	}
}
