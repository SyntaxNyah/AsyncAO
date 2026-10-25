package ui

// THE 294-COMBINATION COHERENCE MATRIX (v1.90.0 W9) — design §6.5 acceptance
// clause 4, risk R6's mitigation, and the only gate in the wave that can tell
// whether the two preset axes are actually orthogonal.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// anchorMinOverlapPct is design §4's own number: at least half of an anchored
// element's area must sit within reach of the widget it anchors to.
const anchorMinOverlapPct = 50

// anchorHaloPct is what "within reach" means, and it is the correction the first
// run of this matrix forced.
// So the anchor is inflated by this percentage of its OWN extent on every side
// TestPresetMatrixWouldCatchADriftedElement drives THIS expression — measured
// against the raw anchor instead, as it was, the constant could be dialled to
// 2000 (a 41x41 = 1681x-area halo) with all 588 runs and both drift arms still
// For scale, of the 19,188 anchored placements the matrix measures, 84 have ZERO
// raw overlap with their anchor and 218 more are between 1% and 49% — mostly the
// tab-above and drip-below idioms above. That is the reason to inflate; it is not
const anchorHaloPct = 100

// anchorDriftProofTenths and anchorGrowthProofTimes are the drift arm's two
// shoves, in TENTHS of the anchor's own design width and in MULTIPLES of its own
// design size. Written against the anchor rather than in pixels because the
// shipped anchors run from 640x480 to 23x19 and a pixel figure would be a
// different test on each; written as constants rather than derived from
// anchorHaloPct because a shove that grew with the halo would prove nothing about
// it. See the arithmetic in TestPresetMatrixWouldCatchADriftedElement.
const (
	anchorDriftProofTenths  = 16
	anchorGrowthProofTimes  = 4
	anchorPlacedProofTenths = 4
)

// canvasMinOverlapPct bounds the OTHER kind of style element: the declared,
// unanchored canvas-space ornament (a gilt edge stripe, a ripple on the void
// floor, a corner caption). It has no widget to be attached to, so the only
// coherence question left is whether it is on the canvas at all — which is
// exactly what a layout swap can take away from it, since its rect is absolute
// in a canvas whose size the layout chooses.
const canvasMinOverlapPct = 50

// presetMatrixCanvases are the two extremes ruling R7 names. The first is the
// widest shipped design canvas at 1:1; the second is smaller than every layout
// but `retro_lowres`, so every fragment is downscaled in it.
var presetMatrixCanvases = [...]struct {
	name string
	w, h int32
}{
	{"1512x648", 1512, 648},
	{"640x480", 640, 480},
}

// stagePresetCombination applies one (layout, style) pair to a themed App the
// way the client does: the layout's own design canvas, the AO2 backstop under
// its [overrides], the merged sidecar, and one cold rebuild.
func stagePresetCombination(t testing.TB, a *App, l, s *theme.Preset, w, h int32) *themeLayoutCache {
	t.Helper()
	merged, err := theme.NewThemeFromPresets("Matrix", theme.PresetPair{Layout: l, Style: s})
	if err != nil {
		t.Fatalf("%s x %s: merge: %v", l.ID, s.ID, err)
	}

	// The AO2 backstop, then the LAYOUT's canvas. `courtroom` is deliberately not
	// an editable key, so no layout preset declares it in [overrides] — its canvas
	// arrives through the [preset] header instead, and every design-space number in
	// the fragment is relative to exactly this box.
	rects := make(map[string]theme.Rect, len(ao2DefaultDesignRects)+1)
	for k, r := range ao2DefaultDesignRects {
		rects[k] = r
	}
	rects["courtroom"] = theme.Rect{W: l.DesignW, H: l.DesignH}
	a.skin.themeRects = rects
	a.elemBake.themeSidecar = merged
	foldSidecarOverrides(a.skin.themeRects, merged)
	a.skin.themeRectsOrig = cloneRects(a.skin.themeRects)

	a.skin.themeLay.valid = false
	lay := a.themeLayout(w, h)
	if !lay.valid {
		t.Fatalf("%s x %s at %dx%d: the layout cache did not validate", l.ID, s.ID, w, h)
	}
	return lay
}

// rectArea is int64 because a 1512x648 rect inflated by a bad delta can overflow
// int32 in the multiply long before it overflows in the rect.
func rectArea(r sdl.Rect) int64 { return int64(r.W) * int64(r.H) }

// overlapPctOfElement is assertion 1's arithmetic, isolated so the failure
// message and the threshold read the same number.
func overlapPctOfElement(el, within sdl.Rect) int64 {
	area := rectArea(el)
	if area <= 0 {
		return 0
	}
	sect, hits := el.Intersect(&within)
	if !hits {
		return 0
	}
	return rectArea(sect) * 100 / area
}

// anchorHalo inflates an anchor by anchorHaloPct of its own extent on each side.
func anchorHalo(a sdl.Rect) sdl.Rect {
	dx := int64(a.W) * anchorHaloPct / 100
	dy := int64(a.H) * anchorHaloPct / 100
	return sdl.Rect{
		X: int32(int64(a.X) - dx),
		Y: int32(int64(a.Y) - dy),
		W: int32(int64(a.W) + 2*dx),
		H: int32(int64(a.H) + 2*dy),
	}
}

// ornamentKey / ornamentSite are the invariance ledger's entry: one declared
// canvas-space element, under one design canvas, and where the first layout with
// that canvas put it. Keyed by the CANVAS rather than by the layout because the
// canvas is the one thing an ornament is allowed to depend on.
type ornamentKey struct {
	id     string
	dw, dh int
}

type ornamentSite struct {
	box    sdl.Rect
	layout string
}

// TestEveryLayoutTimesEveryStyleIsCoherent is acceptance clause 4.
func TestEveryLayoutTimesEveryStyleIsCoherent(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	var combos, resolved, ornaments, inert, invariants int
	for _, canvas := range presetMatrixCanvases {
		for _, s := range lib.Styles() {
			// THE INVARIANCE LEDGER, per style and per canvas: where each declared
			// canvas-space ornament landed, keyed by the LAYOUT'S DESIGN CANVAS.
			seen := map[ornamentKey]ornamentSite{}
			for _, l := range lib.Layouts() {
				combos++
				lay := stagePresetCombination(t, a, l, s, canvas.w, canvas.h)
				sc := a.elemBake.themeSidecar
				for i := range sc.Elements {
					el := &sc.Elements[i]
					box, ok := a.resolveElementRect(lay, sc, el)

					// The declared canvas-space ornaments: no widget, so the only
					// question is whether the layout's canvas still contains them.
					if el.Anchor == "" {
						if !s.IsFullCanvas(el.ID) {
							t.Fatalf("%s x %s: element %q has no anchor and is not declared — "+
								"internal/theme should have refused this fragment", l.ID, s.ID, el.ID)
						}
						if !ok {
							t.Errorf("[%s] %s x %s: declared ornament %q resolves to nothing",
								canvas.name, l.ID, s.ID, el.ID)
							continue
						}
						ornaments++
						if box.W <= 0 || box.H <= 0 {
							t.Errorf("[%s] %s x %s: ornament %q resolves to %v — a non-positive box never paints",
								canvas.name, l.ID, s.ID, el.ID, box)
							continue
						}
						if pct := overlapPctOfElement(box, lay.area); pct < canvasMinOverlapPct {
							t.Errorf("[%s] %s x %s: ornament %q is %d%% on the canvas %v, want >= %d%% (element %v)",
								canvas.name, l.ID, s.ID, el.ID, pct, lay.area, canvasMinOverlapPct, box)
						}
						key := ornamentKey{id: el.ID, dw: l.DesignW, dh: l.DesignH}
						prev, had := seen[key]
						if had {
							invariants++
						}
						if had && prev.box != box {
							t.Errorf("[%s] %s: ornament %q lands at %v under layout %q and at %v under %q — "+
								"both declare a %dx%d design canvas, so the only thing that differs is where "+
								"the WIDGETS are, and a declared canvas-space element may not depend on that "+
								"(THEME-FORMAT.md section 9, clause 4)",
								canvas.name, s.ID, el.ID, prev.box, prev.layout, box, l.ID, l.DesignW, l.DesignH)
						}
						if !had {
							seen[key] = ornamentSite{box: box, layout: l.ID}
						}
						continue
					}

					anchor, present := a.elementAnchorRect(lay, sc, el)
					if !present {
						inert++ // ruling R10: absent anchor = inert, not an error
						continue
					}
					if !ok {
						t.Errorf("[%s] %s x %s: element %q has a resolved anchor %v but no rect",
							canvas.name, l.ID, s.ID, el.ID, anchor)
						continue
					}
					resolved++
					if box.W <= 0 || box.H <= 0 {
						t.Errorf("[%s] %s x %s: element %q resolves to %v — a non-positive box never paints, "+
							"and its anchor was %v (the delta inverted it)",
							canvas.name, l.ID, s.ID, el.ID, box, anchor)
						continue
					}
					if pct := overlapPctOfElement(box, anchorHalo(anchor)); pct < anchorMinOverlapPct {
						t.Errorf("[%s] %s x %s: element %q is %d%% within reach of its anchor %q, want >= %d%% "+
							"(element %v, anchor %v, halo %v) — the decoration has drifted off the widget it dresses",
							canvas.name, l.ID, s.ID, el.ID, pct, el.Anchor, anchorMinOverlapPct,
							box, anchor, anchorHalo(anchor))
					}
				}
			}
		}
	}
	if combos != lib.Combinations()*len(presetMatrixCanvases) {
		t.Fatalf("ran %d combinations, want %d", combos, lib.Combinations()*len(presetMatrixCanvases))
	}
	if resolved == 0 || ornaments == 0 {
		t.Fatalf("measured %d anchored placements and %d ornaments — one arm never ran", resolved, ornaments)
	}
	// The invariance ledger only fires when two layouts DECLARE THE SAME design
	// canvas, so a library in which every canvas were unique would make clause 4
	// unenforceable while the test stayed green. Count the comparisons.
	if invariants == 0 {
		t.Fatal("the ornament invariance check never compared two placements — no two shipped layouts " +
			"share a design canvas, so THEME-FORMAT.md section 9 clause 4 is unenforced")
	}
	t.Logf("%d combinations, %d anchored placements, %d canvas ornaments (%d invariance comparisons), "+
		"%d inert (anchor absent from the layout)", combos, resolved, ornaments, invariants, inert)
}

// TestPresetMatrixWouldCatchADriftedElement is the matrix's own gate: the same
// arithmetic driven with elements the shipped set does not contain. It measures
// anchorHalo(anchor) — what the matrix measures — because an earlier arm
// measured the raw anchor and stayed green with anchorHaloPct mutated to 2000.
func TestPresetMatrixWouldCatchADriftedElement(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	l, s := lib.Layouts()[0], lib.Styles()[0]
	lay := stagePresetCombination(t, a, l, s, 1512, 648)
	sc := a.elemBake.themeSidecar

	const anchorKey = "emotes"
	base, ok := absSlotRect(lay, anchorKey)
	if !ok {
		t.Skipf("%s does not place %q; the drift arm needs a present anchor", l.ID, anchorKey)
	}
	// The shoves are DESIGN-space, like every fragment rect, so they are derived
	// from the anchor's design rect; the device rects then scale together and the
	// ratios above hold exactly.
	design, placed := a.skin.themeRects[anchorKey]
	if !placed || design.W <= 0 || design.H <= 0 {
		t.Skipf("%q has no design rect to size the arms against", anchorKey)
	}
	halo := anchorHalo(base)
	probe := func(id string, r theme.Rect) (sdl.Rect, int64) {
		t.Helper()
		el := theme.Element{ID: id, Kind: theme.ElemShape, Shape: "sharp", Anchor: anchorKey,
			Rect: r, Fill: theme.RGBA{R: 255, A: 255}}
		box, ok := a.resolveElementRect(lay, sc, &el)
		if !ok {
			t.Fatalf("the %s probe did not resolve — the arm cannot run", id)
		}
		return box, overlapPctOfElement(box, halo)
	}

	// Arm 0, the non-vacuity one: a decoration exactly on its anchor must PASS.
	if box, pct := probe("placed", theme.Rect{}); pct < anchorMinOverlapPct {
		t.Fatalf("an element sitting exactly on its anchor measures %d%% within reach (box %v, halo %v) — "+
			"the threshold has been dialled past what a correct placement can reach, and the matrix's "+
			"294 combinations are passing on nothing", pct, box, halo)
	}
	// ...and so must one nudged a little, which is what real decoration does.
	if box, pct := probe("nudged",
		theme.Rect{X: design.W * anchorPlacedProofTenths / 10}); pct < anchorMinOverlapPct {
		t.Fatalf("a badge nudged %d/10 of its anchor's width measures %d%% within reach (box %v) — "+
			"the tab-above and drip-below idioms this halo exists for would fail",
			anchorPlacedProofTenths, pct, box)
	}

	// Arm 1, DRIFT, just past the threshold: this is the arm anchorHaloPct cannot
	// be raised past.
	if box, pct := probe("drift",
		theme.Rect{X: design.W * anchorDriftProofTenths / 10}); pct >= anchorMinOverlapPct {
		t.Fatalf("a badge shoved %d/10 of its anchor's width off it measures %d%% within reach of "+
			"anchorHalo (box %v, halo %v) — at anchorHaloPct = %d it must be under %d%%, so either the "+
			"metric cannot tell placement from drift or the halo has been inflated past the point of "+
			"being a gate", anchorDriftProofTenths, pct, box, halo, anchorHaloPct, anchorMinOverlapPct)
	}

	// Arm 2, GROWTH — the clause the constant's comment claimed and no arm tested.
	if box, pct := probe("grown", theme.Rect{
		W: design.W * anchorGrowthProofTimes, H: design.H * anchorGrowthProofTimes,
	}); pct >= anchorMinOverlapPct {
		t.Fatalf("a decoration grown to %dx the widget it dresses measures %d%% within reach "+
			"(box %v, halo %v) — a frame that has swallowed its own anchor is drift by another name",
			anchorGrowthProofTimes+1, pct, box, halo)
	}

	// Arm 3, INVERSION: a frame that subtracts more width than its anchor has.
	inverted := theme.Element{
		ID: "inverted", Kind: theme.ElemShape, Shape: "sharp", Anchor: anchorKey,
		Rect: theme.Rect{W: -9999, H: -9999}, Fill: theme.RGBA{R: 255, A: 255},
	}
	if box, ok := a.resolveElementRect(lay, sc, &inverted); ok && (box.W > 0 && box.H > 0) {
		t.Fatalf("an inverted delta produced a paintable box %v — the positive-rect arm cannot fire", box)
	}
}

// TestAnAbsentAnchorIsInertRatherThanMisplaced pins ruling R10 through the real
// resolver, which is the behaviour the matrix's `inert` count depends on being
// true. An element anchored to a key no layout places must produce NOTHING —
// not a box at the origin, and not a box on some other widget.
func TestAnAbsentAnchorIsInertRatherThanMisplaced(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	lay := stagePresetCombination(t, a, lib.Layouts()[0], lib.Styles()[0], 1512, 648)
	ghost := theme.Element{
		ID: "ghost", Kind: theme.ElemShape, Shape: "sharp", Anchor: "no_such_slot_key",
		Fill: theme.RGBA{R: 255, A: 255},
	}
	if r, ok := a.resolveElementRect(lay, a.elemBake.themeSidecar, &ghost); ok {
		t.Fatalf("an element anchored to a key nothing places resolved to %v — R10 says it is inert", r)
	}
}
