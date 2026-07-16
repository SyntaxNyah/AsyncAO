package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestRectsNearlyCover pins the "near-total overlap" predicate for the panel
// de-overlap pass (A1 Phase 2): only an intersection covering at least
// panelDeOverlapFrac (82%) of the SMALLER rect counts, so intentional partial
// stacks are left alone and the two-panels-on-identical-defaults case trips it.
func TestRectsNearlyCover(t *testing.T) {
	a := sdl.Rect{X: 0, Y: 0, W: 100, H: 100}

	// Disjoint → false.
	if rectsNearlyCover(a, sdl.Rect{X: 200, Y: 200, W: 100, H: 100}) {
		t.Error("disjoint rects must not count as covering")
	}
	// Identical → true (the seed-collision root case).
	if !rectsNearlyCover(a, a) {
		t.Error("identical rects must count as covering")
	}
	// Smaller rect fully inside a larger one → true (inter == smaller area).
	if !rectsNearlyCover(sdl.Rect{X: 10, Y: 10, W: 20, H: 20}, sdl.Rect{X: 0, Y: 0, W: 500, H: 500}) {
		t.Error("fully-contained rect must count as covering")
	}
	// Exactly at the 82% threshold (inter 82x100 of a 100x100) → true; one
	// pixel-column under (81x100) → false. Integer-exact on purpose.
	if !rectsNearlyCover(a, sdl.Rect{X: 18, Y: 0, W: 100, H: 100}) {
		t.Error("82%% overlap must count as covering (threshold is >=)")
	}
	if rectsNearlyCover(a, sdl.Rect{X: 19, Y: 0, W: 100, H: 100}) {
		t.Error("81%% overlap must NOT count as covering")
	}
	// Degenerate zero-area rect → false, never a divide/overflow.
	if rectsNearlyCover(a, sdl.Rect{X: 0, Y: 0, W: 0, H: 0}) {
		t.Error("zero-area rect must not count as covering")
	}
}

// TestDeOverlapRect pins the bounded cascade: an unobstructed rect is untouched,
// a fully-stacked rect is nudged diagonally by panelDeOverlapStep until clear,
// the loop terminates at panelDeOverlapCap even when nowhere is clear, and an
// off-window nudge wraps back to the top-left inset (floatWinMargin/floatTitleH).
func TestDeOverlapRect(t *testing.T) {
	const w, h = int32(1920), int32(1080)

	// No overlap → returned unchanged.
	r := sdl.Rect{X: 100, Y: 100, W: 200, H: 150}
	if got := deOverlapRect(r, []sdl.Rect{{X: 900, Y: 600, W: 200, H: 150}}, w, h); got != r {
		t.Errorf("clear rect moved: got %+v, want %+v", got, r)
	}

	// Seeded exactly on a sibling → one diagonal nudge clears it (a 28px offset
	// of a 200x150 rect leaves ~70%% intersection, under the 82%% threshold).
	census := []sdl.Rect{{X: 100, Y: 100, W: 200, H: 150}}
	got := deOverlapRect(r, census, w, h)
	want := sdl.Rect{X: 100 + panelDeOverlapStep, Y: 100 + panelDeOverlapStep, W: 200, H: 150}
	if got != want {
		t.Errorf("stacked rect: got %+v, want one-step cascade %+v", got, want)
	}

	// Nowhere clear (census covers the whole window) → the cascade must stop at
	// panelDeOverlapCap nudges, not loop forever (§17.4). Window large enough
	// that no wrap fires, so the landing spot is exactly cap diagonal steps.
	everything := []sdl.Rect{{X: 0, Y: 0, W: w, H: h}}
	got = deOverlapRect(r, everything, w, h)
	want = sdl.Rect{X: 100 + panelDeOverlapCap*panelDeOverlapStep, Y: 100 + panelDeOverlapCap*panelDeOverlapStep, W: 200, H: 150}
	if got != want {
		t.Errorf("capped cascade: got %+v, want %+v after %d steps", got, want, panelDeOverlapCap)
	}

	// A nudge that would run off the right/bottom wraps back to the top-left
	// inset so the cascade stays on-screen.
	small := sdl.Rect{X: 150, Y: 180, W: 100, H: 80}
	got = deOverlapRect(small, []sdl.Rect{{X: 150, Y: 180, W: 100, H: 80}}, 200, 250)
	if got.X != floatWinMargin || got.Y != floatTitleH {
		t.Errorf("off-window nudge: got (%d,%d), want wrap to (%d,%d)", got.X, got.Y, floatWinMargin, floatTitleH)
	}
}
