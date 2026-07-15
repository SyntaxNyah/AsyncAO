package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// newShapeTestApp builds a headless App with a real Ctx + texture store so the
// A5 shape-mask build/heal/draw paths can be exercised (mirrors the store setup
// stageSettledCourtroom uses). Skips when SDL/Ctx is unavailable headlessly.
func newShapeTestApp(t *testing.T) (*App, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	ctx, err := NewCtx(ren)
	if err != nil {
		cleanup()
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = ctx
	store, err := render.NewTextureStore(ren)
	if err != nil {
		cleanup()
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	return a, func() {
		store.Purge()
		cleanup()
	}
}

// shapeMasksResidentCount probes every shape/role/tier key for the given kind
// and returns how many are resident — a bounded enumeration (the cache has no
// key-listing API, so we probe the enumerated set the design promises).
func shapeMasksResidentCount(a *App, kind string) int {
	n := 0
	for tier := 0; tier < shapeRadiusTiers; tier++ {
		for _, role := range []string{shapeRoleFill, shapeRoleStroke} {
			if page, ok := a.d.Store.Get(shapeTexKey(kind, role, tier)); ok && page != nil {
				n++
			}
		}
	}
	return n
}

// TestChromeShapeMaskBuildAndBounded pins that building a shape uploads exactly
// the enumerated set (tiers × 2 roles) into the pinned tier and NOTHING beyond
// it — no per-frame growth, and no tier past the cap (hard rule §17.4).
func TestChromeShapeMaskBuildAndBounded(t *testing.T) {
	a, done := newShapeTestApp(t)
	defer done()

	a.d.Prefs.SetChromeShape(shapeRounded)
	a.buildShapeMasks()

	want := shapeRadiusTiers * 2 // fill + stroke per tier
	if got := shapeMasksResidentCount(a, shapeRounded); got != want {
		t.Fatalf("rounded masks resident = %d, want %d (tiers×roles)", got, want)
	}
	// A tier past the cap must never be uploaded — probing it is a miss.
	if _, ok := a.d.Store.Get(shapeTexKey(shapeRounded, shapeRoleFill, shapeRadiusTiers)); ok {
		t.Fatalf("a tier beyond shapeRadiusTiers=%d was uploaded — cache is unbounded", shapeRadiusTiers)
	}
	// Total distinct shape masks across every kind is bounded by
	// len(shapeMaskKinds) × shapeRadiusTiers × 2 — the enumerated ceiling.
	max := len(shapeMaskKinds) * shapeRadiusTiers * 2
	total := 0
	for _, k := range shapeMaskKinds {
		total += shapeMasksResidentCount(a, k)
	}
	if total > max {
		t.Fatalf("total shape masks = %d, exceeds bound %d", total, max)
	}
}

// TestBuildShapeMasksIdempotent pins that re-running buildShapeMasks replaces
// (never leaks/grows) the pinned pages — UploadPinned destroys the old page for
// a key, so the resident count is stable across rebuilds.
func TestBuildShapeMasksIdempotent(t *testing.T) {
	a, done := newShapeTestApp(t)
	defer done()

	a.d.Prefs.SetChromeShape(shapePillKey)
	a.buildShapeMasks()
	first := shapeMasksResidentCount(a, shapePillKey)
	a.buildShapeMasks()
	a.buildShapeMasks()
	second := shapeMasksResidentCount(a, shapePillKey)
	if first != second || second != shapeRadiusTiers*2 {
		t.Fatalf("rebuild changed resident count: %d then %d, want %d", first, second, shapeRadiusTiers*2)
	}
}

// TestSharpDefaultNoShapedPath pins the #1 ruling: at the default "sharp" preset
// refreshShapeMasks leaves the shaped path DISABLED (shapeMaskReady=false, nil
// mask pointers) so every draw site falls through to the byte-identical
// Fill+Border — and it uploads NO masks (sharp has none).
func TestSharpDefaultNoShapedPath(t *testing.T) {
	a, done := newShapeTestApp(t)
	defer done()

	// Default prefs are "sharp".
	if got := a.d.Prefs.ChromeShape(); got != shapeSharp {
		t.Fatalf("default shape = %q, want sharp", got)
	}
	a.refreshShapeMasks()
	if a.ctx.shapeMaskReady {
		t.Fatalf("sharp must leave shapeMaskReady=false (shaped path disabled)")
	}
	if a.ctx.shapeFillTex != nil || a.ctx.shapeStrokeTex != nil {
		t.Fatalf("sharp must leave mask pointers nil")
	}
	if n := shapeMasksResidentCount(a, shapeRounded) + shapeMasksResidentCount(a, shapePillKey); n != 0 {
		t.Fatalf("sharp uploaded %d masks, want 0 (sharp has no mask)", n)
	}
	// FillShaped/borderShaped on the sharp path must not touch the store or
	// panic — they route straight to Fill/Border. (Draw is a no-op assertion:
	// it simply must not crash and must not flip shapeMaskReady.)
	a.ctx.FillShaped(sdl.Rect{X: 0, Y: 0, W: 40, H: 20}, ColPanel)
	a.ctx.borderShaped(sdl.Rect{X: 0, Y: 0, W: 40, H: 20}, ColAccent)
	if a.ctx.shapeMaskReady {
		t.Fatalf("sharp FillShaped/borderShaped must not enable the shaped path")
	}
}

// TestChromeShapeActivates pins that picking a non-sharp preset + building masks
// makes refreshShapeMasks enable the shaped path with resident mask pointers.
func TestChromeShapeActivates(t *testing.T) {
	a, done := newShapeTestApp(t)
	defer done()

	a.d.Prefs.SetChromeShape(shapePillKey)
	a.buildShapeMasks()
	a.refreshShapeMasks()
	if !a.ctx.shapeMaskReady {
		t.Fatalf("pill + built masks must enable the shaped path")
	}
	if a.ctx.shapeFillTex == nil || a.ctx.shapeStrokeTex == nil {
		t.Fatalf("pill must resolve fill+stroke mask pointers")
	}
	if a.ctx.activeShape != shapePillKey {
		t.Fatalf("activeShape = %q, want pill", a.ctx.activeShape)
	}
	if !a.ctx.shapePill {
		t.Fatalf("pill preset must set the shapePill draw flag")
	}
}

// TestButtonColPillZeroAlloc is THE pill alloc gate: a representative ButtonCol
// draw with the pill preset ACTIVE and masks RESIDENT must allocate nothing —
// the 9-slice hot path (two mask passes, SetColorMod/SetAlphaMod, set-then-Copy
// scratch rects) is 0-alloc/op once the masks are up, which the default-sharp
// whole-screen gate never exercises. Models TestToolIconAllocFree.
func TestButtonColPillZeroAlloc(t *testing.T) {
	a, done := newShapeTestApp(t)
	defer done()

	a.d.Prefs.SetChromeShape(shapePillKey)
	a.buildShapeMasks()
	a.refreshShapeMasks()
	if !a.ctx.shapeMaskReady {
		t.Skipf("shape masks not resident headlessly; skipping the alloc gate")
	}
	c := a.ctx
	r := sdl.Rect{X: 20, Y: 20, W: 90, H: 26}
	draw := func() {
		// A representative shaped button: fill + border 9-slice + label clip.
		c.ButtonCol(r, "Play", ColPanel, ColPanelHi, ColAccent, ColText)
		// The raw shaped primitives too (panel-body adoption path).
		c.FillShaped(r, ColPanel)
		c.borderShaped(r, ColAccent)
	}
	draw() // warm the text-texture cache + any lazy renderer state
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("shaped ButtonCol/FillShaped allocates %.1f/op, want 0 — the 9-slice hot path shipped a per-frame alloc", n)
	}
}

// TestRefreshShapeMasksZeroAlloc pins the PER-FRAME hook itself, which the
// per-widget gate above never exercises: with a non-sharp preset active and a
// quiescent store, the once-per-frame refreshShapeMasks must be pure field
// reads + comparisons — the resolve memo (shapeResolved*) skips the
// shapeTexKey concat + store Gets until the shape, tier or store generation
// moves. Without the memo the always-on render loop pays two key-concat map
// lookups every frame of a shaped config.
func TestRefreshShapeMasksZeroAlloc(t *testing.T) {
	a, done := newShapeTestApp(t)
	defer done()

	a.d.Prefs.SetChromeShape(shapePillKey)
	a.buildShapeMasks()
	a.refreshShapeMasks() // prime the resolve memo
	if !a.ctx.shapeMaskReady {
		t.Skipf("shape masks not resident headlessly; skipping the alloc gate")
	}
	if n := testing.AllocsPerRun(200, a.refreshShapeMasks); n != 0 {
		t.Fatalf("settled refreshShapeMasks allocates %.1f/op, want 0 — the per-frame resolve must ride the memo", n)
	}
	// The memo must not go stale: a store mutation (generation bump) has to
	// re-resolve rather than early-return on the old pointers.
	a.buildShapeMasks() // re-upload = same keys, new pages, new generation
	a.refreshShapeMasks()
	if !a.ctx.shapeMaskReady || a.ctx.shapeFillTex == nil {
		t.Fatalf("post-rebuild refresh must re-resolve the fresh mask pointers")
	}
	if a.shapeResolvedGen != a.shapeStoreGen() {
		t.Fatalf("resolve memo generation = %d, want the store's %d — a stale memo would pin freed pointers",
			a.shapeResolvedGen, a.shapeStoreGen())
	}
}
