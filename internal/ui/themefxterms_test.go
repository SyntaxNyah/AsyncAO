package ui

// EVERY EFFECT TERM MUST REACH THE PIXEL (ledger L9 + L10).
//
// applyElementFX writes five terms onto the baked element, the painter draws, and
// restoreElementFX puts the row back — so the only moment the terms EXIST is the
// instant the painter is called. Every gate in this package read the element AFTER
// that, through a recorded *bakedElement, and therefore saw the restored row. The
// consequence was total: the wash arm, the offset arm, the scale arm, the alpha arm
// and elemFXSave's restore were all deletable with the suite green, and the one gate
// that looked relevant recomputed its own expected alpha with fxAlpha(...) three
// times instead of reading what the painter actually got.
//
// captureElementPaintValues (themeelements_test.go) is the five-character fix: it
// snapshots the element BY VALUE at the call. Everything here drives the real
// drawElement and reads that snapshot, so each arm is pinned by the picture rather
// than by a second copy of the arithmetic.
//
// The other half of the same contract is here too: a painter MUST NOT MUTATE its
// *bakedElement (ledger L37). On the no-effect fast path drawElement does no
// save/restore at all, so a mutating painter corrupts the shared cache permanently
// and compounds every frame.

import (
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// fxTermsAt drives ONE drawElement at a chosen elapsed and returns what the painter
// received, by value, plus the baked row afterwards.
//
// The elapsed is set the way every other gate in this package sets it — by moving
// a.themeAt back — so the clock the element reads is the one the client reads.
func fxTermsAt(t *testing.T, a *App, lay *themeLayoutCache, e bakedElement, elapsed time.Duration) (got, after bakedElement) {
	t.Helper()
	var seen []bakedElement
	restore := captureElementPaintValues(t, &seen)
	a.themeAt = time.Now().Add(-elapsed)
	a.beginThemeFXPass()
	a.drawElement(lay, &e, 0)
	restore()
	if len(seen) != 1 {
		t.Fatalf("drawElement dispatched %d painter calls, want exactly 1 — the element is not "+
			"visible, or the band loop is not reaching a painter at all", len(seen))
	}
	return seen[0], e
}

// TestEveryFXTermReachesThePixel is the arm-by-arm gate. Each row makes exactly one
// term non-neutral and asserts the PAINTER saw it — and that the baked row came back
// unchanged, because a left-applied offset integrates into the cache every frame.
func TestEveryFXTermReachesThePixel(t *testing.T) {
	a, _ := newEffectsRig(t)
	lay := &themeLayoutCache{condGen: 11}

	// --- the OFFSET arm (dx/dy) -------------------------------------------------
	// Drift at elapsed 0 is a pure +X offset (cos 0 = 1, sin 0 = 0), which is the one
	// sample point where the term is both maximal and exactly determined.
	{
		base := fxTestElement(theme.FXDrift)
		got, after := fxTermsAt(t, a, lay, base, 0)
		if got.r.X <= base.r.X {
			t.Errorf("a drift at cycle 0 painted at X=%d, want > the baked %d — applyElementFX's "+
				"offset arm never reached the painter", got.r.X, base.r.X)
		}
		if got.r.W != base.r.W || got.r.H != base.r.H {
			t.Errorf("the offset arm resized the element (%dx%d → %dx%d) — a drift moves, it does not scale",
				base.r.W, base.r.H, got.r.W, got.r.H)
		}
		if after.r != base.r {
			t.Errorf("the baked rect was left at %+v, want the authored %+v — an un-restored offset "+
				"integrates into the cache on EVERY frame, so the element walks off the screen",
				after.r, base.r)
		}
	}

	// --- the SCALE arm ----------------------------------------------------------
	// Breathe at half a period is at its positive extreme (wave = 1).
	{
		base := fxTestElement(theme.FXBreathe)
		got, after := fxTermsAt(t, a, lay, base, time.Duration(fxTestPeriodMs)*time.Millisecond/2)
		if got.r.W <= base.r.W || got.r.H <= base.r.H {
			t.Errorf("a breathe at its peak painted %dx%d, want larger than the baked %dx%d — the "+
				"scale arm never reached the painter", got.r.W, got.r.H, base.r.W, base.r.H)
		}
		// Scaled about the CENTRE: an element that grew from its top-left would slide,
		// which is the one thing a breathe must not do.
		if gotCx, wantCx := got.r.X+got.r.W/2, base.r.X+base.r.W/2; absInt32(gotCx-wantCx) > 1 {
			t.Errorf("the scaled element's centre moved %d → %d — the scale is not centred", wantCx, gotCx)
		}
		if after.r != base.r {
			t.Errorf("the baked rect was left scaled (%+v, want %+v) — one frame's growth would "+
				"compound into the next", after.r, base.r)
		}
	}

	// --- the ALPHA arm, on the four colours -------------------------------------
	// Pulse at half a period rides down to (1 - amp), which at full amplitude is 0.
	{
		base := fxTestElement(theme.FXPulse)
		base.col2 = sdl.Color{R: 10, G: 20, B: 30, A: 200}
		base.stroke = sdl.Color{R: 40, G: 50, B: 60, A: 180}
		got, after := fxTermsAt(t, a, lay, base, time.Duration(fxTestPeriodMs)*time.Millisecond/2)
		for _, c := range []struct {
			what      string
			got, want uint8
		}{
			{"col", got.col.A, base.col.A},
			{"col2", got.col2.A, base.col2.A},
			{"stroke", got.stroke.A, base.stroke.A},
			{"tint", got.tint.A, base.tint.A},
		} {
			if c.got >= c.want {
				t.Errorf("a pulse at its trough painted %s at alpha %d, want below the baked %d — the "+
					"kinds do not share one alpha knob (the procedural painters read col/col2/stroke, "+
					"the art painter reads only tint.A), so an arm that dims three of four is an "+
					"effect that works on some kinds and not others", c.what, c.got, c.want)
			}
		}
		if after.col != base.col || after.col2 != base.col2 || after.stroke != base.stroke || after.tint != base.tint {
			t.Error("the baked colours were left dimmed — the fade would compound to black over a few frames")
		}
	}

	// --- the ALPHA arm on TEXT, which spends it through fxDim --------------------
	// Text ink must NOT be dimmed in place: the label cache keys on the ink colour,
	// so a per-frame colour would rasterise a fresh texture every frame a fade moved.
	{
		base := fxTestElement(theme.FXPulse)
		base.kind, base.label = theme.ElemText, "verdict"
		got, after := fxTermsAt(t, a, lay, base, time.Duration(fxTestPeriodMs)*time.Millisecond/2)
		if got.fxDim == 0 {
			t.Error("a pulse on an ElemText left fxDim at 0 — the text alpha arm never reached the painter")
		}
		if got.col != base.col {
			t.Errorf("a pulse dimmed an ElemText's INK (%v → %v) — the label cache keys on colour, so "+
				"this rasterises a new texture on every frame the fade moves", base.col, got.col)
		}
		if after.fxDim != base.fxDim {
			t.Errorf("fxDim was left at %d, want the baked %d", after.fxDim, base.fxDim)
		}
	}

	// --- the ROTATION arm -------------------------------------------------------
	// Spin at a quarter period, at full amplitude, is a quarter turn.
	{
		base := fxTestElement(theme.FXSpin)
		got, after := fxTermsAt(t, a, lay, base, time.Duration(fxTestPeriodMs)*time.Millisecond/4)
		if got.ang == base.ang {
			t.Errorf("a spin painted at the baked angle %d — the rotation arm never reached the painter", base.ang)
		}
		if after.ang != base.ang {
			t.Errorf("the baked angle was left at %d, want %d — an un-restored rotation accumulates "+
				"one quarter-turn per frame", after.ang, base.ang)
		}
	}

	// --- the WASH arm (env), which is a bind's whole reason for existing ---------
	{
		base := fxTestElement(theme.FXFade)
		base.wash = true
		mid, after := fxTermsAt(t, a, lay, base, time.Duration(fxTestPeriodMs)*time.Millisecond/2)
		if mid.col.A == 0 || mid.col.A >= base.col.A {
			t.Errorf("a mid-fade wash painted at alpha %d, want 0 < a < the authored peak %d — the "+
				"envelope arm never reached the painter, so a bind is either invisible or an opaque "+
				"sheet over the widget it decorates", mid.col.A, base.col.A)
		}
		if after.col != base.col {
			t.Errorf("the wash's baked colour was left at %v, want %v — one frame's envelope would "+
				"compound into the next", after.col, base.col)
		}
		// A FINISHED wash is GONE. It still reaches its painter — the effect is live,
		// it has simply decayed — so the proof is the alpha the painter received, which
		// only a value snapshot can see. This is the whole reason the arm exists: a
		// wash that settles at its authored peak is a theme that has permanently
		// covered the widget it decorates.
		done, _ := fxTermsAt(t, a, lay, base, time.Duration(fxTestPeriodMs)*time.Millisecond*5)
		if done.col.A != 0 {
			t.Fatalf("a FINISHED fade wash painted at alpha %d, want 0 — the curtain never clears",
				done.col.A)
		}
	}
}

// TestReduceMotionPaintsTheAuthoredFormNotTheFrozenPeak is the accessibility half of
// the same reading, and it is the one an "obvious" implementation gets backwards.
//
// Under ReduceMotion every effect resolves neutral — so a FREE element paints its
// authored form (still), and a WASH paints NOTHING (env 0). Freezing a wash at its
// baked peak instead would make the accessibility option the thing that put an opaque
// sheet over the stage.
func TestReduceMotionPaintsTheAuthoredFormNotTheFrozenPeak(t *testing.T) {
	a, prefs := newEffectsRig(t)
	prefs.SetReduceMotion(true)
	lay := &themeLayoutCache{condGen: 12}

	free := fxTestElement(theme.FXDrift)
	got, after := fxTermsAt(t, a, lay, free, time.Duration(fxTestPeriodMs)*time.Millisecond/2)
	if got.r != free.r || got.col != free.col || got.ang != free.ang {
		t.Errorf("under ReduceMotion a free element painted %+v/%v/%d, want its authored "+
			"%+v/%v/%d — the freeze must be neutral, not 'whatever the effect said'",
			got.r, got.col, got.ang, free.r, free.col, free.ang)
	}
	if after.r != free.r {
		t.Errorf("the baked rect was mutated on the frozen path: %+v, want %+v", after.r, free.r)
	}

	wash := fxTestElement(theme.FXFade)
	wash.wash = true
	var seen []bakedElement
	restore := captureElementPaintValues(t, &seen)
	a.themeAt = time.Now()
	a.beginThemeFXPass()
	a.drawElement(lay, &wash, 0)
	restore()
	if len(seen) != 0 {
		t.Fatalf("under ReduceMotion a wash still painted (alpha %d) — a wash IS its effect, so a "+
			"frozen one must paint nothing; painting it at the baked peak is the accessibility "+
			"option covering the stage", seen[0].col.A)
	}
}

// TestNoPainterMutatesItsBakedElement is ledger L37: the painter contract's
// undocumented, untested clause.
//
// On the no-effect fast path — which every stock element takes — drawElement does NO
// save/restore, so a painter that writes through its *bakedElement corrupts the cache
// permanently and compounds every frame. Two shipped painters already mutate SHARED
// state (paintElementArt's colour/alpha mod on a page texture) and it is only the
// row itself that must come back untouched.
//
// It runs the REAL painter table, unlike every other element gate in the package —
// which is precisely why the clause had no coverage: the one gate that looked
// relevant runs with the recorder swapped in, so it pins drawElement and no painter.
func TestNoPainterMutatesItsBakedElement(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	a.drawCourtroom(w, h) // cold rebuild first: themeLayoutIn replaces the whole cache
	lay := &a.themeLay
	seedBakedElements(a, lay, theme.ElementCap, seedElementArt(t, a))
	if lay.elN != theme.ElementCap {
		t.Fatalf("seeded %d elements, want %d", lay.elN, theme.ElementCap)
	}

	before := make([]bakedElement, lay.elN)
	copy(before, lay.el[:lay.elN])
	a.drawCourtroom(w, h) // the REAL painters
	for i := 0; i < lay.elN; i++ {
		if lay.el[i] != before[i] {
			t.Fatalf("element %d (kind %v) came back from its painter mutated:\n got %+v\nwant %+v\n"+
				"On the no-effect fast path drawElement does no save/restore, so this corrupts the "+
				"layout cache permanently and compounds on every frame.",
				i, lay.el[i].kind, lay.el[i], before[i])
		}
	}
}

// absInt32 is |v|. Test-local: the package has no such helper and two comparisons do
// not earn a shared one.
func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
