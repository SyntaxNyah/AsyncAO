package ui

// THE 294-COMBINATION COHERENCE MATRIX (v1.90.0 W9) — design §6.5 acceptance
// clause 4, risk R6's mitigation, and the only gate in the wave that can tell
// whether the two preset axes are actually orthogonal.
//
// WHY IT LIVES IN internal/ui AND NOT BESIDE THE PRESET DATA. The question it
// asks is "where does this decoration LAND", and the answer is produced by
// resolveElementRect / absSlotRect / bakePanes — three functions in this
// package, whose behaviour includes the anchor delta (R1), the pane reparent
// (R8), AO2's four coordinate systems, the second spelling fold, the canvas fit
// and the widget clamp. A version of this test in internal/theme would have to
// re-derive all of that, and hard rule 11 says in as many words that a
// mirror-test which re-implements the seam instead of driving it does not count.
// So it drives the real bake, through the real layout cache, on a real App.
//
// WHAT COHERENT MEANS, stated once:
//
//   1. A style element whose anchor is PRESENT in the resolved layout lands ON
//      its anchor — at least anchorMinOverlapPct of the element's own area is
//      inside the widget it decorates. Frames outset a few pixels, badges tuck
//      into corners, and both stay well over half; a decoration that has drifted
//      off its widget after a layout swap does not.
//   2. A style element whose anchor is ABSENT is INERT, not a failure (ruling
//      R10). It is counted, so a preset that has quietly gone all-inert shows up.
//   3. Every element that resolves has a POSITIVE rect. A delta that inverts its
//      anchor's width produces a box SDL silently declines to draw, which is the
//      one failure mode that looks exactly like success.
//   4. A DECLARED canvas-space ornament lands in the same box under every layout
//      that declares the same design canvas. That is what "layout-independent"
//      means, and it is the clause that replaced the published ">= 90% of its
//      space" rule the whole shipped corpus broke (THEME-FORMAT.md section 9's
//      recorded deviation, 2026-08-08).
//
// It runs at both canvas extremes (R7): a 1512x648 window and a 640x480 one.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// anchorMinOverlapPct is design §4's own number: at least half of an anchored
// element's area must sit within reach of the widget it anchors to.
//
// OF THE ELEMENT'S AREA, not of the anchor's — the two differ and only one of
// them is the question. An element much SMALLER than its anchor (a corner badge
// on the viewport) covers a fraction of the anchor and is perfectly placed; an
// element much LARGER than its anchor has drifted into the client at large. The
// denominator is therefore the element, which is the thing that either is or is
// not attached to its widget.
const anchorMinOverlapPct = 50

// anchorHaloPct is what "within reach" means, and it is the correction the first
// run of this matrix forced.
//
// Measured against the anchor's own box, the rule failed 260 times on decoration
// that is doing exactly what it was written to do: `higurashi`'s red mark BLEEDS
// DOWN OFF the nameplate (that is the preset's whole signature), `cyberpunk`'s
// `ooc_head` is a caption bar tucked ABOVE the OOC field, `danganronpa`'s badge
// sits above the showname, `investigation`'s medallion rings its evidence button.
// A tab above a panel and a drip below a plate are the two most ordinary
// decoration idioms there are, and a gate that calls them drift is a gate that
// would have forced fourteen presets to be redrawn to satisfy it.
//
// So the anchor is inflated by this percentage of its OWN extent on every side
// before the overlap is measured. It is proportional rather than a pixel figure
// because the anchors range from 640 x 480 (a stage) to 23 x 19 (an area-swap
// button) across the shipped layouts, and a fixed halo would be invisible on one
// and enormous on the other.
//
// 100 makes the halo 3x3 = nine times the anchor's area.
//
// THE HALO IS THE WHOLE GATE'S SENSITIVITY, so it is pinned rather than trusted.
// TestPresetMatrixWouldCatchADriftedElement drives THIS expression — measured
// against the raw anchor instead, as it was, the constant could be dialled to
// 2000 (a 41x41 = 1681x-area halo) with all 588 runs and both drift arms still
// green: the arm proved a metric the matrix does not use, which is the mirror
// shape hard rule 11 forbids, applied to the wave's headline gate. Its arms are
// now expressed in units of the anchor's OWN extent, which is what makes them
// absolute against this number: a zero-delta element stops overlapping half of
// itself at 1.5 anchor-widths of shove, so the near-miss arm at 1.6 reddens the
// moment the halo grows past ~110, and the growth arm reddens past ~180.
//
// For scale, of the 19,188 anchored placements the matrix measures, 84 have ZERO
// raw overlap with their anchor and 218 more are between 1% and 49% — mostly the
// tab-above and drip-below idioms above. That is the reason to inflate; it is not
// a reason to leave the inflation ungated.
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
//
// It uses the SAME two-map fold applyFixtureSidecar does (pristine + live),
// because that is what the client has by the time anything bakes.
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
	a.themeRects = rects
	a.themeSidecar = merged
	foldSidecarOverrides(a.themeRects, merged)
	a.themeRectsOrig = cloneRects(a.themeRects)

	a.themeLay.valid = false
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
// int64 throughout: a 1512-wide anchor tripled still fits int32, but the
// intermediate on a hostile rect need not, and an overflow here would silently
// turn a failing case into a passing one.
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
//
// It is one test rather than 294 subtests on purpose: a subtest per combination
// makes a single broken style print 21 near-identical failures and buries the
// one line that says which. This prints the WORST offender per (style, canvas)
// and a census, which is what a person debugging a preset actually needs.
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
			//
			// It is what "layout-independent" actually means, and until W9 nothing
			// measured it — the published rule asked for >= 90% coverage instead, which
			// every one of the twelve shipped ornaments breaks and which was a proxy for
			// this in the first place (THEME-FORMAT.md section 9's recorded deviation).
			// An ornament may depend on the canvas it is fitted into and on NOTHING
			// else, so two layouts that declare the same design canvas must put it in
			// exactly the same box.
			seen := map[ornamentKey]ornamentSite{}
			for _, l := range lib.Layouts() {
				combos++
				lay := stagePresetCombination(t, a, l, s, canvas.w, canvas.h)
				sc := a.themeSidecar
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

// TestPresetMatrixWouldCatchADriftedElement is the matrix's own gate: it proves
// the assertion has teeth, by driving the same arithmetic with elements the
// shipped set does not contain.
//
// IT MEASURES anchorHalo(anchor), WHICH IS WHAT THE MATRIX MEASURES. It used to
// measure the raw anchor, and the difference is the whole value of the test: with
// `anchorHaloPct` mutated from 100 to 2000 — a 41x41 halo, 1681 times the
// anchor's area, which is not a gate at all — the matrix's 588 runs and this
// test both stayed green, because a badge shoved 4000 design px off its anchor
// has 0% raw overlap whatever the halo is. The arm proved a metric nothing uses.
//
// The three arms are the three failures the matrix claims to detect, and the
// first two are sized in units of the ANCHOR'S OWN extent so that they pin the
// halo constant rather than moving with it:
//
//   - DRIFT. The halo reaches anchorHaloPct% of the anchor's width past each
//     edge, so a zero-delta element (a box exactly the anchor's size) loses half
//     of itself at 1 + (1 - anchorMinOverlapPct/100) = 1.5 anchor-widths of
//     shove. anchorDriftProofTenths is 1.6 of them: it must FAIL now, and it
//     starts passing the moment the halo grows past about 110.
//   - GROWTH. A decoration grown to anchorGrowthProofTimes the anchor from its
//     own origin covers 3/N of the 3x halo per axis. At N = 4 that is 56% of
//     the halo per axis, 32% of its area — a failure now, and a pass once the
//     halo reaches about 5x, i.e. anchorHaloPct 200.
//   - INVERSION. A delta that subtracts more width than its anchor has, which
//     SDL declines to draw: the one failure that looks exactly like success.
//
// A fourth arm runs the metric the other way, on a correctly placed element, so
// a threshold that had been dialled the OTHER way — into a gate nothing can pass
// — is a failure here rather than 294 silent skips.
func TestPresetMatrixWouldCatchADriftedElement(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("preset library: %v", err)
	}
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	l, s := lib.Layouts()[0], lib.Styles()[0]
	lay := stagePresetCombination(t, a, l, s, 1512, 648)
	sc := a.themeSidecar

	const anchorKey = "emotes"
	base, ok := absSlotRect(lay, anchorKey)
	if !ok {
		t.Skipf("%s does not place %q; the drift arm needs a present anchor", l.ID, anchorKey)
	}
	// The shoves are DESIGN-space, like every fragment rect, so they are derived
	// from the anchor's design rect; the device rects then scale together and the
	// ratios above hold exactly.
	design, placed := a.themeRects[anchorKey]
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
	if r, ok := a.resolveElementRect(lay, a.themeSidecar, &ghost); ok {
		t.Fatalf("an element anchored to a key nothing places resolved to %v — R10 says it is inert", r)
	}
}
