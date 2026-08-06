package ui

// The element clock + effect gates (v1.90.0 W5) — the nine the wave plan names
// (docs/wip/THEME-EDITOR-DESIGN.md §4 W5).
//
// Two of them are the whole reason this wave is risky, and they are opposites:
//
//	TestIdleElementsDoNotPinTheFrameRate  — a still element that holds the frame
//	  rate up is the idle-CPU-burn defect (the v1.56.0 arc).
//	TestStaticElementNeverCallsNoteAnimating and its live siblings — an element
//	  that MOVES and fails to note the census freezes mid-animation, which is the
//	  worse defect, because the pacer will happily sleep through it.
//
// The rest pin determinism (an exported GIF and a preset thumbnail must reproduce
// any frame from elapsed alone), the two fixed pools, and the one accessibility gate
// the feature has.

import (
	"image"
	"strconv"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// fxTestPeriodMs is the period every effect in this file runs at. Named so the
// sample points below read as fractions of a cycle rather than as magic times.
const fxTestPeriodMs = int32(1000)

// fxTestAmp is full amplitude. Effects are measured at their strongest so a term
// that is present but tiny cannot be mistaken for one that is missing.
const fxTestAmp = int16(theme.AmpMaxPct)

// fxLiveEffect is one effect at full strength, for kind k.
func fxLiveEffect(k theme.EffectKind) bakedEffect {
	return bakedEffect{kind: k, periodMs: fxTestPeriodMs, ampPct: fxTestAmp}
}

// fxTestElement is a plain visible element carrying effect k. Big enough that a
// fractional offset resolves to whole pixels — an element smaller than 1/eps on its
// short side would round every offset to zero and make a real motion look static.
func fxTestElement(k theme.EffectKind) bakedElement {
	return bakedElement{
		r:        sdl.Rect{X: 100, Y: 100, W: 200, H: 200},
		kind:     theme.ElemShape,
		shape:    elemShapeSharp,
		opacity:  theme.OpacityOpaque,
		col:      sdl.Color{R: 200, G: 40, B: 40, A: 255},
		tint:     sdl.Color{R: 255, G: 255, B: 255, A: 255},
		cond:     condAlwaysVisible,
		condAxis: theme.CondAlways,
		fx:       fxLiveEffect(k),
	}
}

// fxTestConditional is fxTestElement on a CONDITION that currently matches — the
// only shape of element that takes a pool slot, because it is the only one whose
// one-shot has to replay when its condition re-opens. An unconditional element's
// one-shot began when the theme applied, so it needs no state (fxAlwaysOrigin).
//
// cond/condLive are wired to agree so the element is VISIBLE: a gate that closed
// would make every assertion below pass for the wrong reason.
func fxTestConditional(a *App, k theme.EffectKind) bakedElement {
	e := fxTestElement(k)
	e.condAxis, e.cond = theme.CondPos, 0
	a.condLive[theme.CondPos] = 0
	return e
}

// fxTestStore is a headless texture store for the pages below — newCaptureHarness's
// software renderer over the dummy video driver, the same one every other
// page-building test in this package runs on. SKIPPED rather than failed where SDL
// is unavailable, which is the package's standing rule for its SDL-backed gates.
//
// Only the four tests that need a real page ask for one, so the effect-only gates
// (the pool, the binds) still run with no renderer at all.
func fxTestStore(t *testing.T) *render.TextureStore {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	store, err := render.NewTextureStore(ren)
	if err != nil {
		cleanup()
		t.Skipf("texture store unavailable: %v", err)
	}
	t.Cleanup(func() {
		store.Purge()
		cleanup()
	})
	return store
}

// fxTestPageSide is the edge of a fixture page, in pixels. Tiny on purpose: what is
// being measured is the frame INDEX, never the art, and a page this size is well
// inside the theme-media per-item byte cap at any TexBudget.
const fxTestPageSide = 8

// fxTestPage is an ANIMATED page with n distinct frames and a uniform delay.
//
// It uploads a real assets.Decoded through the REAL theme-media tier, which is the
// pattern seedElementArt (themeelements_test.go) and the render package's own media
// gates already use. There is no lighter stand-in available: sdl.Texture is an
// INCOMPLETE cgo type, so `new(sdl.Texture)` does not compile, and elementFrame
// hands whatever it returns to a painter that blits it.
//
// The frames differ from each other so the page is genuinely an animation rather
// than n copies of one picture — a page whose frames were identical would make every
// "the frame advanced" assertion below pass without the index ever moving.
func fxTestPage(t testing.TB, store *render.TextureStore, n int, delay time.Duration) *render.TexturePage {
	t.Helper()
	d := &assets.Decoded{Width: fxTestPageSide, Height: fxTestPageSide, Animated: n > 1}
	for i := 0; i < n; i++ {
		img := image.NewRGBA(image.Rect(0, 0, fxTestPageSide, fxTestPageSide))
		for p := 0; p < len(img.Pix); p += 4 {
			img.Pix[p] = uint8(i * 16) // a different red per frame
			img.Pix[p+3] = 0xFF
		}
		d.Frames = append(d.Frames, img)
		d.Delays = append(d.Delays, delay)
	}
	// Keyed by the test and the frame count, so a store shared by two pages holds
	// both: an identical key is a REPLACE at this tier, not a second page.
	key := render.ThemeMediaKey("w5fixture", t.Name()+"-"+strconv.Itoa(n))
	if err := store.UploadThemeMedia(key, d, config.TexBudgetDefaultMiB); err != nil {
		t.Fatalf("seeding a %d-frame theme-media page: %v", n, err)
	}
	page, ok := store.Get(key)
	if !ok {
		t.Fatal("the seeded theme-media page did not land")
	}
	return page
}

// fxNeutralTerms reports whether an effect resolved to the identity transform.
func fxNeutralTerms(fx elemFX) bool {
	return fx.dx == 0 && fx.dy == 0 && fx.scale == 1 && fx.alpha == 1 && fx.rot == 0
}

// ---------------------------------------------------------------------------
// Gate 1 — every kind resolves, and amplitude zero is always still
// ---------------------------------------------------------------------------

// TestEveryEffectKindIsResolvable pins the resolver TOTAL over theme.EffectKind, the
// way TestEveryElementKindHasAPainter pins the painter table.
//
// Totality matters for the same reason: the kind comes out of a file a stranger
// wrote, and a kind the resolver has no arm for would be an effect that silently does
// nothing — the failure nobody reports because the theme "just looks a bit plain".
//
// The second half is the design's own rule that every effect returns static at
// amplitude 0. That is what makes `amp_pct = 0` a supported way to author a
// still element that still NAMES its effect (so the editor can turn it back on),
// rather than a way to pin the frame rate for no pixels.
func TestEveryEffectKindIsResolvable(t *testing.T) {
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if k.String() == "" {
			t.Errorf("effect kind %d has no wire name — the messages below would name nothing", k)
		}
		// Amplitude zero: neutral AND static, for every kind without exception.
		zero := resolveElementEffect(bakedEffect{kind: k, periodMs: fxTestPeriodMs}, 0, &fxState{})
		if !zero.static || !fxNeutralTerms(zero) {
			t.Errorf("%s at amp 0 resolved to %+v, want the neutral transform with static=true — "+
				"an amplitude-zero effect must cost nothing and must not pin the pacer", k, zero)
		}
		if k == theme.FXNone {
			// The zero value is the only kind that is static at FULL amplitude too.
			full := resolveElementEffect(fxLiveEffect(k), 250*time.Millisecond, &fxState{})
			if !full.static || !fxNeutralTerms(full) {
				t.Errorf("FXNone at amp 100 resolved to %+v, want neutral+static", full)
			}
			continue
		}
		// Every real kind must MOVE something somewhere in its first cycle. Sampled
		// across the period rather than at one point, because a sine is neutral at its
		// own zero crossings and a single sample would be a coin flip.
		moved := false
		for step := 1; step <= 8; step++ {
			el := time.Duration(step) * time.Duration(fxTestPeriodMs) * time.Millisecond / 8
			got := resolveElementEffect(fxLiveEffect(k), el, &fxState{})
			if !fxNeutralTerms(got) {
				moved = true
				break
			}
		}
		if !moved {
			t.Errorf("%s at amp 100 never left the neutral transform across a whole period — "+
				"the resolver has no arm for this kind", k)
		}
		// The pool-exhaustion path, for the kinds that use the pool: no slot means the
		// effect freezes at its first frame and reports static. Graceful and named
		// (design §Q3) — never a drop, never a crash, never a live effect with no slot.
		if effectIsStateful[k] {
			out := resolveElementEffect(fxLiveEffect(k), 400*time.Millisecond, nil)
			if !out.static {
				t.Errorf("%s with no pool slot reported static=false — an exhausted pool must freeze "+
					"the effect, not leave it pinning the frame rate with no state behind it", k)
			}
		}
	}
}

// TestPeriodicEffectsNeverGoIdleMidCycle is the other half of the doctrine, and the
// one a naive implementation gets wrong.
//
// A sine crosses zero twice per period. A static test written on the INSTANTANEOUS
// term would therefore report "nothing moved" at every crossing, the pacer would drop
// to the idle rate for that frame, and a running animation would stutter twice a
// cycle — on a machine fast enough to sample the crossing, which is every machine.
// The envelope, not the value, is what decides.
func TestPeriodicEffectsNeverGoIdleMidCycle(t *testing.T) {
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if k == theme.FXNone || effectIsStateful[k] {
			continue
		}
		for step := 0; step <= 64; step++ {
			el := time.Duration(step) * time.Duration(fxTestPeriodMs) * time.Millisecond / 64
			if got := resolveElementEffect(fxLiveEffect(k), el, nil); got.static {
				t.Fatalf("%s reported static at %v (%d/64 of its period) — a periodic effect is never "+
					"idle above the epsilon. Testing the instantaneous term instead of the envelope "+
					"makes the pacer stutter at every zero crossing.", k, el, step)
			}
		}
	}
}

// TestDecayingEffectsSelfClear is self-clear path 2: the three one-shots must stop
// noting the census once their envelope has decayed, or a slam fired at boot pins the
// frame rate for the rest of the session.
func TestDecayingEffectsSelfClear(t *testing.T) {
	period := time.Duration(fxTestPeriodMs) * time.Millisecond
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if !effectIsStateful[k] {
			continue
		}
		st := &fxState{}
		if got := resolveElementEffect(fxLiveEffect(k), period/8, st); got.static {
			t.Errorf("%s reported static an eighth of the way into its one-shot — it has not run yet", k)
		}
		if got := resolveElementEffect(fxLiveEffect(k), period*2, st); !got.static {
			t.Errorf("%s is still live at twice its period — a finished one-shot must self-clear, or it "+
				"holds the frame rate at the animation cadence forever", k)
		}
		// And it must have converged on the element's authored look, not stopped
		// somewhere halfway: "decaying" means the EFFECT decays, leaving nothing behind.
		if got := resolveElementEffect(fxLiveEffect(k), period*2, st); !fxNeutralTerms(got) {
			t.Errorf("%s settled at %+v rather than the neutral transform — a finished one-shot must "+
				"leave the element exactly as it was authored", k, got)
		}
		// The BEFORE side of the same rule, and the one a naive implementation gets
		// wrong by clamping. A one-shot that has not started yet — a negative `phase_ms`
		// (theme.TimeMsMin allows it), a backwards wall-clock step, W7's scrub — must be
		// STILL and must paint nothing. Held at its first frame instead, it sits at full
		// amplitude and notes the census on every frame for as long as elapsed stays
		// negative: ten minutes after a theme apply for `phase_ms = -600000`, and forever
		// on a scrubbed clock.
		if got := resolveElementEffect(fxLiveEffect(k), -period, st); !got.static || got.env != 0 || !fxNeutralTerms(got) {
			t.Errorf("%s at a NEGATIVE elapsed resolved to %+v, want the neutral transform with env 0 and "+
				"static=true — a one-shot whose clock has not reached it yet must not pin the frame rate at "+
				"full amplitude with nothing on screen, and a wash bound to it must not draw its peak", k, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Gate 1b — §6.6 R2's rot term, applied and restored
// ---------------------------------------------------------------------------

const (
	// fxRotBakedAngle is a NON-ZERO authored angle, so the gate below measures the
	// documented behaviour ("the effect's rotation ADDS to the element's own baked
	// angle") rather than the weaker "the field ended up non-zero". Chosen so the
	// sum overflows the byte — 200 + 64 = 264 — which makes the same assertion pin
	// the 360/256 wrap as well.
	fxRotBakedAngle = uint8(200)
	// fxRotQuarterTurn is what FXSpin resolves to a quarter of the way through its
	// period at full amplitude: rot = mod(1 * 0.25, 1) turns, and one turn is
	// theme.AngleCount units. Written as the arithmetic rather than as 64 so a change
	// to the angle convention moves the expectation with it.
	fxRotQuarterTurn = theme.AngleCount / 4
	// fxRotSamples is how many points of a period the no-rotation half samples. More
	// than one, because a kind that rotated only at its peak would slip through a
	// single sample.
	fxRotSamples = 16
)

// TestSpinIsTheOnlyEffectThatRotates is the R2 rot gate: design §6.6's ratified
// escalation had ZERO coverage after W5 shipped it.
//
// That is the dangerous shape of untested code, not the harmless one. The rot term
// is FOUR LINES in two places — resolvePeriodicEffect's FXSpin arm and the one `if
// fx.rot != 0` block in applyElementFX — with no assertion anywhere pointing at
// either. A refactor that dropped the apply block would leave every other effect
// gate green, every shipped theme loading, the resolver still returning a correct
// rot, and spinning elements simply not spinning: the failure nobody reports because
// the theme "just looks a bit plain". Proved by mutation before it was written —
// disabling the branch at themeclock.go's `if fx.rot != 0` must fail this test and
// nothing else in the package.
//
// Three claims, because the term is only honoured if all three hold:
//
//  1. FXSpin ADVANCES e.ang, by the resolved amount, added to the baked angle and
//     wrapped into the byte.
//  2. restoreElementFX PUTS IT BACK. The baked array is a cache the next frame
//     re-reads, so an angle left applied would integrate: a spin would accelerate
//     every frame until it was a strobe. elemFXSave carries `ang` for this reason
//     alone, and nothing else asserts that it does.
//  3. The other seven motion kinds LEAVE IT ALONE, across a whole period. Rotation
//     is spin's identity; a pulse that also turned would be a second, undeclared
//     effect, and the enum is the theme author's only contract about what a kind
//     does.
func TestSpinIsTheOnlyEffectThatRotates(t *testing.T) {
	period := time.Duration(fxTestPeriodMs) * time.Millisecond

	// --- 1 + 2: the spin itself -------------------------------------------
	//
	// A quarter of the way in, so the expected angle is exact integer arithmetic
	// rather than a rounding of a sine — the assertion names a number, and a wrong
	// number is a wrong number rather than "close enough".
	fx := resolveElementEffect(fxLiveEffect(theme.FXSpin), period/4, nil)
	if fx.rot == 0 {
		t.Fatalf("FXSpin resolved rot = 0 a quarter of the way through its period (%+v) — the resolver "+
			"half of R2 is gone, and the apply half below cannot be measured through it", fx)
	}
	e := fxTestElement(theme.FXSpin)
	e.ang = fxRotBakedAngle
	saved := applyElementFX(&e, fx)
	want := uint8((int32(fxRotBakedAngle) + fxRotQuarterTurn) & (theme.AngleCount - 1))
	if e.ang != want {
		t.Errorf("a quarter-turn spin on an element baked at angle %d left e.ang = %d, want %d "+
			"(resolved rot = %v). §6.6 R2's rotation must ADD to the element's own baked angle and wrap "+
			"into the 360/256 byte — an effect that resolves a rotation nothing applies is a spin that "+
			"never spins.", fxRotBakedAngle, e.ang, want, fx.rot)
	}
	restoreElementFX(&e, saved)
	if e.ang != fxRotBakedAngle {
		t.Errorf("after restoreElementFX the element is at angle %d, want its baked %d — the baked array "+
			"is a CACHE the next frame re-reads, so an angle left applied integrates: the spin would "+
			"accelerate every frame until it strobed.", e.ang, fxRotBakedAngle)
	}
	// The angle must keep MOVING across the period, not stop at one applied value: a
	// spin whose amplitude clipped its arc (rather than scaling its rate) would snap
	// back to the same handful of angles every cycle.
	seen := map[uint8]bool{}
	for step := 0; step < fxRotSamples; step++ {
		el := time.Duration(step) * period / fxRotSamples
		s := fxTestElement(theme.FXSpin)
		s.ang = fxRotBakedAngle
		_ = applyElementFX(&s, resolveElementEffect(fxLiveEffect(theme.FXSpin), el, nil))
		seen[s.ang] = true
	}
	if len(seen) < fxRotSamples {
		t.Errorf("a full-amplitude spin visited %d distinct angles across %d samples of one period, want "+
			"%d — the rotation is not advancing continuously with elapsed", len(seen), fxRotSamples, fxRotSamples)
	}

	// --- 3: nothing else touches the angle --------------------------------
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if k == theme.FXNone || k == theme.FXSpin {
			continue
		}
		for step := 0; step < fxRotSamples; step++ {
			el := time.Duration(step) * period / fxRotSamples
			// A fresh slot per sample so the one-shots are measured from their own
			// origin rather than from a slot the previous kind advanced.
			got := resolveElementEffect(fxLiveEffect(k), el, &fxState{startedAt: 0})
			if got.rot != 0 {
				t.Errorf("%s resolved rot = %v at %v — rotation is FXSpin's identity, and a kind that "+
					"also turned would be a second effect the theme author never declared", k, got.rot, el)
			}
			o := fxTestElement(k)
			o.ang = fxRotBakedAngle
			s := applyElementFX(&o, got)
			if o.ang != fxRotBakedAngle {
				t.Errorf("%s moved e.ang from %d to %d at %v — only a spin rotates",
					k, fxRotBakedAngle, o.ang, el)
			}
			restoreElementFX(&o, s)
			if o.ang != fxRotBakedAngle {
				t.Errorf("%s left e.ang = %d after restore, want %d", k, o.ang, fxRotBakedAngle)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Gate 2 — determinism (the export / thumbnail contract)
// ---------------------------------------------------------------------------

// TestElementFrameIsAFunctionOfElapsed is the reason phase arithmetic beat
// independent per-element wall clocks (design §Q3).
//
// The offscreen export (gifexport.go, through themeLayout) and the editor's live
// preset miniatures must be able to reproduce ANY frame from elapsed alone.
// Independent wall clocks would make an exported GIF non-reproducible and a preset
// thumbnail non-deterministic — you could not diff two exports of the same recording,
// and a gallery would shimmer.
//
// Both halves are pinned: the page index AND the effect terms.
func TestElementFrameIsAFunctionOfElapsed(t *testing.T) {
	page := fxTestPage(t, fxTestStore(t), 5, 40*time.Millisecond)
	for _, loop := range []bool{false, true} {
		for step := 0; step <= 40; step++ {
			el := time.Duration(step) * 13 * time.Millisecond
			i1, live1 := elementFrameIndex(page, el, loop)
			i2, live2 := elementFrameIndex(page, el, loop)
			if i1 != i2 || live1 != live2 {
				t.Fatalf("elementFrameIndex(%v, loop=%v) returned (%d,%v) then (%d,%v) — the frame must be "+
					"a pure function of elapsed", el, loop, i1, live1, i2, live2)
			}
		}
	}
	// A one-shot must REACH done and stay there; a loop must never report done. If
	// the first were false an export would hang on a splash, and if the second were
	// the pacer would stop stepping a looping backdrop.
	total := 5 * 40 * time.Millisecond
	if _, live := elementFrameIndex(page, total*3, false); live {
		t.Error("a one-shot page is still live at three times its own length")
	}
	if _, live := elementFrameIndex(page, total*3, true); !live {
		t.Error("a looping page reported not-live — a looping backdrop would freeze the moment the pacer idled")
	}
	// A page whose clock has not reached it yet is a STILL PICTURE, both ways round.
	// pageFrameAt reads a negative elapsed as "before the first delay", so without the
	// guard a one-shot page reports frame 0 with done=false on every frame — noting the
	// census forever while never advancing, which is the same idle-CPU-burn defect the
	// effect resolver's negative-elapsed arm exists to prevent.
	for _, loop := range []bool{false, true} {
		if i, live := elementFrameIndex(page, -time.Second, loop); i != 0 || live {
			t.Errorf("a page at a NEGATIVE elapsed (loop=%v) reported frame %d live=%v, want frame 0 and "+
				"still", loop, i, live)
		}
	}

	// The effect terms, same rule, every kind.
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		for step := 0; step <= 20; step++ {
			el := time.Duration(step) * 57 * time.Millisecond
			a := resolveElementEffect(fxLiveEffect(k), el, &fxState{})
			b := resolveElementEffect(fxLiveEffect(k), el, &fxState{})
			if a != b {
				t.Fatalf("%s at %v resolved to %+v then %+v — an effect must be a pure function of "+
					"elapsed, or an exported GIF stops matching the screen it came from", k, el, a, b)
			}
		}
	}
}

// TestElapsedForIsAllocFree measures elementElapsed — the wave plan calls it
// "elapsed-for"; design §Q3's own code sketch spells it elementElapsed.
//
// It runs once per element per frame, at up to theme.ElementCap elements, inside the
// one frame gated at exactly zero allocations. An allocation here is 96 of them a
// frame, on every themed install that uses the feature at all.
func TestElapsedForIsAllocFree(t *testing.T) {
	a := &App{themeAt: time.Now().Add(-time.Second)}
	a.themeClocks[1] = themeClock{speedPct: 250}
	a.themeClocks[2] = themeClock{paused: true, frozen: 400 * time.Millisecond}
	// Every arm of clockElapsed in one fixture: the shared anchor, a scaled group, a
	// paused group and an out-of-range index that falls back to clock 0.
	els := [...]bakedElement{
		{clock: 0},
		{clock: 1, phase: 250 * time.Millisecond},
		{clock: 2},
		{clock: 200},
	}
	run := func() {
		for i := range els {
			_ = a.elementElapsed(&els[i])
		}
	}
	run()
	if n := testing.AllocsPerRun(500, run); n != 0 {
		t.Fatalf("elementElapsed allocates %.1f/op, want 0 — at %d elements a frame this is the whole "+
			"element model's alloc budget spent on reading a clock", n, theme.ElementCap)
	}
}

// ---------------------------------------------------------------------------
// Gate 3 — the pacing census, in both directions
// ---------------------------------------------------------------------------

// fxDrawRig is an App that can run drawElement without a renderer: prefs (for the
// ReduceMotion latch) and a painter table swapped for a recorder.
func fxDrawRig(t *testing.T) (*App, *themeLayoutCache, *[]*bakedElement, func()) {
	t.Helper()
	a, _ := newEffectsRig(t)
	lay := &themeLayoutCache{condGen: 7}
	seen := &[]*bakedElement{}
	restore := captureElementPaints(seen)
	return a, lay, seen, restore
}

// TestStaticElementNeverCallsNoteAnimating is the idle half of the doctrine at the
// per-element level: an element with no effect, an amplitude-zero effect, a finished
// one-shot or a static page must leave the census untouched.
//
// This is the v1.56.0 idle-CPU-burn arc's rule applied to a brand-new draw surface.
// A themed client sitting on a settled courtroom must fall to the idle rate, and one
// element quietly noting every frame is enough to stop that happening — with nothing
// on screen to explain why the fans are on.
func TestStaticElementNeverCallsNoteAnimating(t *testing.T) {
	a, lay, seen, restore := fxDrawRig(t)
	defer restore()
	store := fxTestStore(t)
	a.themeAt = time.Now().Add(-time.Hour) // long past every one-shot's period
	a.beginThemeFXPass()

	type stillCase struct {
		what string
		el   bakedElement
	}
	cases := []stillCase{
		{"no effect at all", fxTestElement(theme.FXNone)},
		{"a static page", func() bakedElement {
			e := fxTestElement(theme.FXNone)
			e.kind, e.page = theme.ElemImage, fxTestPage(t, store, 1, 0)
			return e
		}()},
		{"an amplitude-zero effect", func() bakedElement {
			e := fxTestElement(theme.FXPulse)
			e.fx.ampPct = 0
			return e
		}()},
	}
	// Every decaying kind, long finished.
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if effectIsStateful[k] {
			cases = append(cases, stillCase{k.String() + ", long finished", fxTestElement(k)})
		}
	}
	// Every case here is UNCONDITIONAL, which is what makes "long finished" mean
	// something without any setup: an always-visible one-shot resolves through
	// fxAlwaysOrigin, so its origin is elapsed zero and a theme clock set an hour back
	// puts it an hour past its own period.
	for i, c := range cases {
		el := c.el
		before := el
		a.frameAnimChrome = false
		a.drawElement(lay, &el, int16(i))
		if a.frameAnimChrome {
			t.Errorf("%s noted the animation census — a still element that pins the frame rate is the "+
				"idle-CPU-burn defect (NoteAnimating is for a draw site that produced a DIFFERENT pixel "+
				"this frame, never for state)", c.what)
		}
		if el != before {
			t.Errorf("%s left the baked row mutated (%+v → %+v) — every effect must be put back after "+
				"the painter, or one frame's motion accumulates into the cache", c.what, before.r, el.r)
		}
	}
	if len(*seen) != len(cases) {
		t.Fatalf("the recorder saw %d painter calls for %d cases — a still element must still DRAW; "+
			"this gate is about the census, not about skipping the paint", len(*seen), len(cases))
	}
	// And none of them took a pool slot: an always-visible one-shot has no state to
	// keep, and the pool is for the conditional ones that have to replay.
	if got := a.themeFXInUse(); got != 0 {
		t.Errorf("%d FX slots claimed by unconditional elements, want 0 — the pool is for elements "+
			"whose one-shot must replay when their condition re-opens", got)
	}
}

// TestLiveElementAlwaysNotesTheCensus is the opposite failure and the worse one: an
// element that MOVES and does not note leaves the pacer free to sleep, and the
// animation freezes mid-stride until something else happens to wake a frame.
func TestLiveElementAlwaysNotesTheCensus(t *testing.T) {
	a, lay, _, restore := fxDrawRig(t)
	defer restore()
	a.themeAt = time.Now()
	a.beginThemeFXPass()

	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if k == theme.FXNone {
			continue
		}
		el := fxTestElement(k)
		a.frameAnimChrome = false
		// A quarter of the way into the period: every kind, periodic or decaying, is
		// unambiguously live there.
		a.themeAt = time.Now().Add(-time.Duration(fxTestPeriodMs) * time.Millisecond / 4)
		a.drawElement(lay, &el, int16(k))
		if !a.frameAnimChrome {
			t.Errorf("%s did not note the animation census a quarter into its period — the pacer may "+
				"sleep, and the effect freezes mid-animation", k)
		}
	}
	// An animated PAGE is the other live source, and it must note on its own even
	// with no effect attached.
	el := fxTestElement(theme.FXNone)
	el.kind, el.page, el.loop = theme.ElemImage, fxTestPage(t, fxTestStore(t), 4, 50*time.Millisecond), true
	a.frameAnimChrome = false
	if got := a.elementFrame(&el); got == nil {
		t.Fatal("elementFrame returned nil for a four-frame page")
	}
	if !a.frameAnimChrome {
		t.Error("a looping animated page did not note the census — it would freeze on whatever frame the pacer idled at")
	}
}

// TestIdleElementsDoNotPinTheFrameRate is the same rule measured on a whole themed
// frame, which is the level the user actually experiences it at.
func TestIdleElementsDoNotPinTheFrameRate(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	draw()
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		t.Fatal("the fixture did not reach the themed branch")
	}
	// The baseline: this fixture's own chrome must already be settled, or the
	// assertion below would be measuring something else entirely.
	a.frameAnimChrome = false
	draw()
	if a.frameAnimChrome {
		t.Skip("the fixture's own chrome notes the census — this gate cannot isolate the element model here")
	}
	// Now fill it with elements that are all, in one way or another, STILL.
	lay := &a.themeLay
	seedBakedElements(a, lay, theme.ElementCap, nil)
	for i := 0; i < lay.elN; i++ {
		switch i % 3 {
		case 0: // no effect at all
		case 1: // an effect at amplitude zero
			lay.el[i].fx = bakedEffect{kind: theme.FXPulse, periodMs: fxTestPeriodMs}
		case 2: // a one-shot that finished long ago
			lay.el[i].fx = fxLiveEffect(theme.FXSlam)
		}
	}
	// An hour on the theme clock puts every one-shot far past its own period. Two
	// passes before the measured one so that anything which DOES take a pool slot (the
	// seeded conditional rows) has claimed and finished it as well.
	a.themeAt = time.Now().Add(-time.Hour)
	draw()
	a.themeAt = time.Now().Add(-time.Hour - 10*time.Second)
	draw()
	a.frameAnimChrome = false
	draw()
	if a.frameAnimChrome {
		t.Fatalf("a themed frame carrying %d STILL elements pinned the frame rate — the client would "+
			"never fall to the idle cadence, and there is nothing on screen to explain why", lay.elN)
	}
}

// TestElementEffectsAreAllocFreeAtCap extends W3's cap-full alloc gate over the code
// this wave added: theme.ElementCap elements, every effect kind in rotation, all of
// them LIVE, drawn on every frame.
//
// The W3 gate cannot reach any of it — its fixture leaves every fx zero, so it
// measures drawElement's fast path and nothing else. This one measures the resolver,
// the pool scan, the transform and the restore, at 96x amplification, which is where
// a per-frame allocation on the motion path would actually show up.
//
// It is also the gate that catches the subtle one: an alpha effect on an ElemText
// that folded its alpha into the ink colour would miss the label cache and rasterise
// a texture EVERY FRAME (ui.go textKey includes the colour). That is invisible to
// every other gate in the package.
func TestElementEffectsAreAllocFreeAtCap(t *testing.T) {
	a, cleanup := stageThemedCourtroom(t)
	defer cleanup()

	const w, h = 1280, 720
	draw := func() { a.drawCourtroom(w, h) }
	draw() // themeLayoutIn rebuilds on its cold path; seed AFTER it
	if !a.themeLay.valid || !a.toolboxThemeRectOn {
		t.Fatal("the fixture did not reach the themed branch")
	}
	lay := &a.themeLay
	seedBakedElements(a, lay, theme.ElementCap, seedElementArt(t, a))
	// Every kind in rotation, at full amplitude, on a mix of clock groups so the
	// scaled and the shared-anchor arms of clockElapsed are both inside the gate.
	live := 0
	for i := 0; i < lay.elN; i++ {
		k := theme.EffectKind(1 + i%int(theme.EffectKindCount-1)) // never FXNone
		lay.el[i].fx = fxLiveEffect(k)
		lay.el[i].clock = uint8(i % 3)
		live++
	}
	a.themeClocks[1] = themeClock{speedPct: 250}
	a.themeClocks[2] = themeClock{speedPct: theme.SpeedMinPct}
	if live != theme.ElementCap {
		t.Fatalf("seeded %d live effects, want %d", live, theme.ElementCap)
	}

	settle(draw)
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("a settled themed frame with %d LIVE effects allocates %.1f/op, want 0 — "+
			"the motion path is charging per frame (fix it, don't shrink the fixture)", theme.ElementCap, n)
	}
	if lay.elN != theme.ElementCap {
		t.Fatalf("the layout cache was rebuilt mid-gate (elN=%d) — the measured frames drew fewer than %d elements",
			lay.elN, theme.ElementCap)
	}
	// Non-vacuity: with 96 live effects the frame MUST have been noting the census
	// throughout, or the gate above measured a screen where nothing moved.
	a.frameAnimChrome = false
	draw()
	if !a.frameAnimChrome {
		t.Fatal("no element noted the animation census during the measured frames — the gate was " +
			"measuring the static path, not the motion path it is named for")
	}
	// The pool stayed inside its cap throughout — the one property that has to hold
	// however many elements a theme declares (hard rule 4).
	if got := a.themeFXInUse(); got > themeFXCap {
		t.Errorf("%d FX slots in use with %d live effects, past the cap of %d", got, theme.ElementCap, themeFXCap)
	}
}

// ---------------------------------------------------------------------------
// Gate 4 — the two fixed pools
// ---------------------------------------------------------------------------

// TestFXPoolIsFixedAndSelfClears pins hard rule 4 on the newest pool in the client:
// a named cap, no growth past it, a graceful answer at it, and a self-clear that
// needs nobody to remember to call it.
func TestFXPoolIsFixedAndSelfClears(t *testing.T) {
	a, _ := newEffectsRig(t)
	if len(a.themeFX) != themeFXCap {
		t.Fatalf("the FX pool holds %d slots, want themeFXCap (%d)", len(a.themeFX), themeFXCap)
	}
	if themeFXCap != theme.EffectBindCap {
		t.Errorf("themeFXCap is %d but theme.EffectBindCap is %d — the format's own constant says internal/ui "+
			"must ALIAS it rather than restate the value", themeFXCap, theme.EffectBindCap)
	}
	// The zero value must be a pool of FREE slots: App has no constructor for this
	// array, and a hand-built App (a test fixture, a future caller) has to behave
	// exactly like the real one.
	if got := a.themeFXInUse(); got != 0 {
		t.Fatalf("a zero-value pool reports %d slots in use, want 0 — fxState's owner must be the baked "+
			"index PLUS ONE so that 0 means free", got)
	}

	// A cold rebuild re-numbers the baked array, so a slot stamped with the PREVIOUS
	// bake generation must not answer for whatever element inherited its index. Its
	// own rig, and before the pool is full, so the check cannot pass vacuously on an
	// exhausted pool handing back nil twice.
	{
		b, _ := newEffectsRig(t)
		b.beginThemeFXPass()
		s1, ok1 := b.acquireElementFX(0, 1, 0)
		s2, ok2 := b.acquireElementFX(0, 2, 0)
		if !ok1 || !ok2 {
			t.Fatal("a two-slot request was refused by an empty pool")
		}
		if s1 == s2 {
			t.Error("index 0 of a NEW bake generation was handed the old generation's slot — after a " +
				"resize an element would be playing another element's one-shot")
		}
		if got := b.themeFXInUse(); got != 2 {
			t.Errorf("two generations of index 0 took %d slots, want 2", got)
		}
	}

	a.beginThemeFXPass()
	// Over-subscribe by eight. Every request must be ANSWERED (with a slot or with an
	// honest refusal); nothing may grow and nothing may panic.
	granted := 0
	for i := 0; i < themeFXCap+8; i++ {
		if _, ok := a.acquireElementFX(int16(i), 1, 0); ok {
			granted++
		}
	}
	if granted != themeFXCap {
		t.Errorf("the pool granted %d slots, want exactly %d", granted, themeFXCap)
	}
	if !a.themeFXOver {
		t.Error("the pool ran out and did not record it — the F8 line and the editor chip are how a user " +
			"finds out an effect froze at frame 0 instead of being broken")
	}
	if got := a.themeFXInUse(); got != themeFXCap {
		t.Errorf("%d slots in use after over-subscribing, want %d", got, themeFXCap)
	}
	// Re-asking for a slot already owned must find it, not consume a second one.
	before := a.themeFXInUse()
	if _, ok := a.acquireElementFX(0, 1, 0); !ok {
		t.Error("an owner could not re-acquire its own slot")
	}
	if got := a.themeFXInUse(); got != before {
		t.Errorf("re-acquiring an owned slot changed the in-use count %d → %d", before, got)
	}

	// Self-clear: nobody asks, and after themeFXIdleFrames passes the pool empties
	// itself. No owner has to remember to release anything.
	for i := 0; i <= themeFXIdleFrames+1; i++ {
		a.beginThemeFXPass()
	}
	if got := a.themeFXInUse(); got != 0 {
		t.Errorf("%d slots still held %d passes after the last request — without the sweep a fired "+
			"one-shot holds its slot forever", got, themeFXIdleFrames+1)
	}
	if a.themeFXOver {
		t.Error("themeFXOver survived the sweep — it describes the LAST pass, not the session")
	}

	// A THEME APPLY drops the pool outright, without waiting for the sweep. Every
	// slot is stamped with the bake generation of the theme that claimed it and an
	// apply invalidates all of them at once, so leaving them held would refuse the
	// INCOMING theme its own one-shots — freezing them at frame 0 — for the first
	// two seconds after every swap.
	{
		b, _ := newEffectsRig(t)
		b.beginThemeFXPass()
		for i := 0; i < themeFXCap+1; i++ { // +1 so the exhaustion flag is set too
			b.acquireElementFX(int16(i), 1, 0)
		}
		if got := b.themeFXInUse(); got != themeFXCap || !b.themeFXOver {
			t.Fatalf("the pre-apply state is %d slots / over=%v, want a full pool that recorded its "+
				"exhaustion", got, b.themeFXOver)
		}
		b.applyThemeClocks(nil)
		if got := b.themeFXInUse(); got != 0 {
			t.Errorf("%d slots survived a theme apply — the incoming theme would be refused its own "+
				"one-shots until the sweep caught up", got)
		}
		if b.themeFXOver {
			t.Error("themeFXOver survived a theme apply — it describes the pass, not the install")
		}
	}
}

// TestOneShotEffectReleasesItsFXSlot is the pool's whole reason for existing, seen
// from the element's side: a slam fires, finishes, the element goes away, and the
// slot comes back on its own.
//
// The slot is deliberately NOT released the instant the one-shot finishes. Releasing
// it there would let the very next pass re-acquire a fresh slot, restart the effect
// and note the census again — an infinite replay that pins the pacer forever, which
// is precisely the failure themeFXIdleFrames is named for.
func TestOneShotEffectReleasesItsFXSlot(t *testing.T) {
	a, lay, _, restore := fxDrawRig(t)
	defer restore()
	period := time.Duration(fxTestPeriodMs) * time.Millisecond

	// CONDITIONAL: that is the element shape the pool exists for. An unconditional
	// one-shot resolves through fxAlwaysOrigin and takes no slot at all.
	el := fxTestConditional(a, theme.FXSlam)
	a.themeAt = time.Now()
	a.beginThemeFXPass()
	a.frameAnimChrome = false
	a.drawElement(lay, &el, 0)
	if !a.frameAnimChrome {
		t.Fatal("a slam that has just started did not note the census")
	}
	if got := a.themeFXInUse(); got != 1 {
		t.Fatalf("a stateful effect took %d slots, want exactly 1", got)
	}

	// Past its period, still on screen: finished, still holding its slot, no longer
	// noting. Holding is correct — this is what stops the replay loop.
	a.themeAt = time.Now().Add(-period * 3)
	a.beginThemeFXPass()
	a.frameAnimChrome = false
	a.drawElement(lay, &el, 0)
	if a.frameAnimChrome {
		t.Error("a finished one-shot is still noting the census")
	}
	if got := a.themeFXInUse(); got != 1 {
		t.Errorf("a finished one-shot holds %d slots, want 1 — releasing it here would let the next pass "+
			"re-acquire and REPLAY, forever", got)
	}

	// The element stops being drawn. The sweep takes the slot back.
	for i := 0; i <= themeFXIdleFrames; i++ {
		a.beginThemeFXPass()
	}
	if got := a.themeFXInUse(); got != 0 {
		t.Errorf("%d slots held after the element stopped drawing for %d passes, want 0",
			got, themeFXIdleFrames+1)
	}
	// And it plays again when the element comes back — which is what makes
	// `visible_when = shout:objection` + `effect = slam` a stamp that lands with the
	// shout, using no trigger syntax at all (design §6.6 R5 cut triggers for v1.90.0).
	a.themeAt = time.Now()
	a.beginThemeFXPass()
	a.frameAnimChrome = false
	a.drawElement(lay, &el, 0)
	if !a.frameAnimChrome {
		t.Error("a one-shot did not replay after its slot was swept — a shout stamp would fire once per session")
	}
}

// TestClockPoolIsFixed pins the other pool: sixteen groups, an out-of-range index
// that falls back to the shared anchor rather than panicking on the render thread,
// and a speed clamped into the format's own range.
func TestClockPoolIsFixed(t *testing.T) {
	a := &App{themeAt: time.Now().Add(-2 * time.Second)}
	if len(a.themeClocks) != theme.ClockCap {
		t.Fatalf("the clock pool holds %d groups, want theme.ClockCap (%d)", len(a.themeClocks), theme.ClockCap)
	}
	// Clock 99 clamps to 0. The reader already refuses it (parseClockIndex), so this
	// is the belt — and the failure it prevents is an index panic on the render
	// thread, triggered by a file a stranger wrote.
	// Tolerant, because both sides read the wall clock: what is pinned is that the
	// out-of-range index resolves to the SHARED ANCHOR, not that two time.Now() calls
	// a nanosecond apart agree exactly.
	if got, want := a.clockElapsed(99), a.clockElapsed(0); absDuration(got-want) > time.Millisecond {
		t.Errorf("clock 99 resolved to %v, want clock 0's %v", got, want)
	}
	// The zero-value pool must be byte-identical to today's themeFrame behaviour, or
	// this wave silently changes every animated chrome page that already ships.
	if got, want := a.clockElapsed(0), a.themeElapsed(); absDuration(got-want) > time.Millisecond {
		t.Errorf("an un-applied clock 0 resolved to %v, want themeElapsed's %v", got, want)
	}

	for _, tc := range []struct{ in, want int16 }{
		{0, theme.SpeedNominalPct}, // the format's "absent" spelling
		{-50, theme.SpeedNominalPct},
		{1, theme.SpeedMinPct},
		{theme.SpeedMinPct, theme.SpeedMinPct},
		{150, 150},
		{theme.SpeedMaxPct, theme.SpeedMaxPct},
		{9999, theme.SpeedMaxPct},
	} {
		if got := clampClockSpeed(tc.in); got != tc.want {
			t.Errorf("clampClockSpeed(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}

	// applyThemeClocks takes the clamp too — the editor writes ClockSpec directly
	// (W7), so the pool cannot rely on the reader having been the only writer.
	sc := &theme.Sidecar{}
	sc.Clocks[1] = theme.ClockSpec{SpeedPct: 9999, Declared: true}
	sc.Clocks[2] = theme.ClockSpec{SpeedPct: 200, Declared: true}
	a.applyThemeClocks(sc)
	if got := a.themeClocks[1].speedPct; got != theme.SpeedMaxPct {
		t.Errorf("an out-of-range group loaded at %d, want %d", got, theme.SpeedMaxPct)
	}
	if got := a.themeClocks[2].speedPct; got != 200 {
		t.Errorf("group 2 loaded at %d, want 200", got)
	}
	// A double-speed group must actually run twice as fast as the shared anchor.
	e0, e2 := bakedElement{clock: 0}, bakedElement{clock: 2}
	if a.elementElapsed(&e2) <= a.elementElapsed(&e0) {
		t.Error("a 200% clock group did not run ahead of the shared anchor")
	}
	// Applying a STOCK theme (no sidecar) must put every group back to nominal, or
	// the outgoing theme's speeds would leak into the incoming one.
	a.applyThemeClocks(nil)
	for i := range a.themeClocks {
		if got := a.themeClocks[i].speedPct; got != theme.SpeedNominalPct {
			t.Fatalf("group %d survived a stock-theme apply at %d, want %d", i, got, theme.SpeedNominalPct)
		}
	}
	// Paused groups hold their frozen elapsed — the editor's scrub, and the reason
	// `paused`/`frozen` are designed in rather than retrofitted.
	a.themeClocks[3] = themeClock{speedPct: theme.SpeedNominalPct, paused: true, frozen: 750 * time.Millisecond}
	if got := a.clockElapsed(3); got != 750*time.Millisecond {
		t.Errorf("a paused group resolved to %v, want its frozen 750ms", got)
	}
}

// absDuration is |d|. Local to this file; the package has no such helper and one
// comparison does not earn a shared one.
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// ---------------------------------------------------------------------------
// Gate 5 — [effect.<widget key>] binds
// ---------------------------------------------------------------------------

// TestEffectBindNeverBuriesItsWidget is the gate the shipped corpus asked for.
//
// Every `[effect.*]` colour in the fourteen native themes is a bare `#rrggbb` — i.e.
// FULLY OPAQUE — at amplitudes from 12 to 100, and every one of them is bound to a
// widget somebody has to be able to read or click: the objection button, the chat
// message, the whole viewport. Under a naive reading ("paint the colour, modulate its
// alpha with the effect") the first frame of `[effect.viewport] effect = fade,
// amp_pct = 100` is an opaque sheet over the entire stage that never clears, and
// `[effect.objection]` is a solid red rectangle where the button was.
//
// So the rule this pins: a bind's plate IS its effect. `amp_pct` is the peak the wash
// reaches, and the effect's envelope scales it from there — which means it is never
// stronger than the author asked for, and a decayed one-shot is GONE.
func TestEffectBindNeverBuriesItsWidget(t *testing.T) {
	a, lay, _, restore := fxDrawRig(t)
	defer restore()
	lay.r = map[string]sdl.Rect{"viewport": {X: 0, Y: 0, W: 640, H: 360}}

	// The shipped shape, verbatim: opaque colour, full amplitude, a fade.
	bind := &theme.EffectBind{
		Target:   "viewport",
		Effect:   theme.FXFade,
		Color:    theme.RGBA{R: 0xf8, G: 0xce, B: 0x7c, A: 255},
		PeriodMs: fxTestPeriodMs,
		AmpPct:   100,
	}
	be, ok := a.bakeEffectBind(lay, bind)
	if !ok {
		t.Fatal("a bind naming a placed widget baked inert")
	}
	if !be.el.wash {
		t.Fatal("a bind did not bake as a wash — its alpha would be a modulation of an authored one")
	}
	if be.band != theme.BandOverlay {
		t.Errorf("a bind baked into band %v, want overlay — a highlight under its own widget is invisible", be.band)
	}

	el := be.el
	// Mid-fade: visible, and no stronger than the authored peak.
	a.themeAt = time.Now().Add(-time.Duration(fxTestPeriodMs) * time.Millisecond / 2)
	a.beginThemeFXPass()
	a.frameAnimChrome = false
	a.drawElement(lay, &el, 0)
	if !a.frameAnimChrome {
		t.Error("a running fade bind did not note the census")
	}
	fx := resolveElementEffect(el.fx, a.elementElapsed(&el), fxAlwaysOrigin)
	mid := fxAlpha(be.el.col, fx.env)
	if mid.A == 0 || mid.A > be.el.col.A {
		t.Errorf("mid-fade wash alpha is %d, want 0 < a <= the authored peak %d", mid.A, be.el.col.A)
	}

	// Finished: the curtain is GONE, and the pacer is free again. This is the whole
	// test — a wash that settles opaque is a theme that has covered the stage.
	a.themeAt = time.Now().Add(-time.Duration(fxTestPeriodMs) * time.Millisecond * 5)
	a.beginThemeFXPass()
	a.frameAnimChrome = false
	a.drawElement(lay, &el, 0)
	if a.frameAnimChrome {
		t.Error("a finished fade bind is still noting the census")
	}
	done := resolveElementEffect(el.fx, a.elementElapsed(&el), fxAlwaysOrigin)
	if got := fxAlpha(be.el.col, done.env); got.A != 0 {
		t.Fatalf("a FINISHED fade bind still washes at alpha %d over %q — it must clear completely, "+
			"or the theme has permanently covered the widget it decorates", got.A, bind.Target)
	}
	// The baked row is unchanged afterwards, so the next frame starts from the peak
	// again rather than from whatever the last one faded to.
	if el.col != be.el.col {
		t.Errorf("the wash's baked colour was left mutated (%v → %v) — one frame's envelope would "+
			"compound into the next", be.el.col, el.col)
	}

	// A glow at 45% never exceeds 45%: the shipped `[effect.objection]` case.
	glow := *bind
	glow.Effect, glow.AmpPct = theme.FXGlow, 45
	gb, ok := a.bakeEffectBind(lay, &glow)
	if !ok {
		t.Fatal("a glow bind baked inert")
	}
	wantPeak := uint8(255 * 45 / 100)
	if gb.el.col.A != wantPeak {
		t.Errorf("a glow bind at amp 45 baked a peak alpha of %d, want %d — amp_pct IS the wash's opacity",
			gb.el.col.A, wantPeak)
	}
	for step := 0; step <= 32; step++ {
		e := gb.el
		at := time.Duration(step) * time.Duration(fxTestPeriodMs) * time.Millisecond / 32
		f := resolveElementEffect(e.fx, at, fxAlwaysOrigin)
		if got := fxAlpha(gb.el.col, f.env); got.A > wantPeak {
			t.Fatalf("the glow wash reached alpha %d at %v, past its authored peak %d", got.A, at, wantPeak)
		}
	}

	// The four inert shapes: no effect, no amplitude, no colour, no such widget.
	for _, c := range []struct {
		what string
		b    theme.EffectBind
	}{
		{"no effect", theme.EffectBind{Target: "viewport", Color: theme.RGBA{A: 255}, AmpPct: 50}},
		{"no amplitude", theme.EffectBind{Target: "viewport", Effect: theme.FXGlow, Color: theme.RGBA{A: 255}}},
		{"no colour", theme.EffectBind{Target: "viewport", Effect: theme.FXGlow, AmpPct: 50}},
		{"absent widget", theme.EffectBind{Target: "no_such_key", Effect: theme.FXGlow, Color: theme.RGBA{A: 255}, AmpPct: 50}},
	} {
		if _, ok := a.bakeEffectBind(lay, &c.b); ok {
			t.Errorf("a bind with %s baked live — it must be inert (preserved on save, drawing nothing)", c.what)
		}
	}
}

// ---------------------------------------------------------------------------
// Gate 6 — the one accessibility gate in the feature
// ---------------------------------------------------------------------------

// TestReduceMotionFreezesEveryElement pins the accessibility branch.
//
// Reduce motion is not "most effects": a person who turns it on because motion makes
// them ill is not helped by a theme whose backdrop still breathes. Every effect
// resolves neutral, every animated page holds frame 0, and nothing is noted — so the
// screen is genuinely still AND the client falls to the idle rate while it is.
func TestReduceMotionFreezesEveryElement(t *testing.T) {
	a, prefs := newEffectsRig(t)
	lay := &themeLayoutCache{condGen: 3}
	var seen []*bakedElement
	restore := captureElementPaints(&seen)
	defer restore()

	prefs.SetReduceMotion(true)
	a.themeAt = time.Now()
	a.beginThemeFXPass()
	if !a.themeFXFrozen {
		t.Fatal("ReduceMotion did not latch on the pass — the branch is read once per pass, not per element")
	}

	page := fxTestPage(t, fxTestStore(t), 6, 30*time.Millisecond)
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		el := fxTestElement(k)
		before := el
		a.frameAnimChrome = false
		// A quarter into the period — every kind is live there when motion is allowed.
		a.themeAt = time.Now().Add(-time.Duration(fxTestPeriodMs) * time.Millisecond / 4)
		a.drawElement(lay, &el, int16(k))
		if a.frameAnimChrome {
			t.Errorf("%s noted the animation census under ReduceMotion — the screen would keep the client "+
				"at the animation cadence for motion the user asked not to see", k)
		}
		if el != before {
			t.Errorf("%s moved the element under ReduceMotion (%+v → %+v)", k, before.r, el.r)
		}
	}
	// Every animated page holds frame 0, and does not note.
	art := fxTestElement(theme.FXNone)
	art.kind, art.page, art.loop = theme.ElemImage, page, true
	a.themeAt = time.Now().Add(-time.Second)
	a.frameAnimChrome = false
	if got := a.elementFrame(&art); got != page.Frames[0] {
		t.Error("an animated page did not hold frame 0 under ReduceMotion")
	}
	if a.frameAnimChrome {
		t.Error("an animated page noted the census under ReduceMotion")
	}
	// No slot is taken either: a frozen effect has no state to keep.
	if got := a.themeFXInUse(); got != 0 {
		t.Errorf("ReduceMotion still claimed %d FX slots — a frozen effect needs no hysteresis", got)
	}

	// A WASH is the one element whose frozen form is not its baked form, and it is
	// the case that makes ReduceMotion dangerous rather than merely disappointing.
	// `[effect.viewport] effect = fade, amp_pct = 100` bakes a plate at FULL opacity
	// over the whole stage and lets the effect's envelope decide how much of it
	// shows. Painting it under the frozen branch draws the peak — a solid sheet over
	// the courtroom, put there by the accessibility option. Freezing resolves amp 0,
	// and amp 0 on a wash is nothing at all.
	seen = seen[:0]
	prefs.SetReduceMotion(true) // already on; restated so the block reads on its own
	a.beginThemeFXPass()
	plate := fxTestElement(theme.FXFade)
	plate.wash = true
	a.frameAnimChrome = false
	a.drawElement(lay, &plate, 0)
	if len(seen) != 0 {
		t.Error("a wash painted under ReduceMotion — its plate exists only because of its effect, so a " +
			"frozen one draws its full authored PEAK over the widget it decorates, forever")
	}
	if a.frameAnimChrome {
		t.Error("a frozen wash noted the animation census")
	}

	// And turning it back off restores the motion: the branch overrules, it must never
	// rewrite anything.
	prefs.SetReduceMotion(false)
	a.beginThemeFXPass()
	el := fxTestElement(theme.FXPulse)
	a.themeAt = time.Now().Add(-time.Duration(fxTestPeriodMs) * time.Millisecond / 4)
	a.frameAnimChrome = false
	a.drawElement(lay, &el, 0)
	if !a.frameAnimChrome {
		t.Error("unticking ReduceMotion did not bring the motion back")
	}
}

// ---------------------------------------------------------------------------
// Gate 7 — the hand-authored fixture, end to end
// ---------------------------------------------------------------------------

// TestHandAuthoredClockSidecarAnimatesEndToEnd is W3's "hand-authored sidecars
// render" claim carried forward to the half W5 added: a hand-authored sidecar
// MOVES, and every moving part of it survives the whole pipeline rather than only
// the unit test each part has of its own.
//
// The fixture (testdata/sidecars/w5_clocks.ini) is written the way an author
// writes, so this gate reads a FILE through the real reader, the real bake, the
// real clock pool and the real resolver — which is the only place the four of them
// are ever in the same sentence.
func TestHandAuthoredClockSidecarAnimatesEndToEnd(t *testing.T) {
	a, lay, cleanup := stageThemedWithSidecar(t, "w5_clocks.ini")
	defer cleanup()
	sc := a.themeSidecar

	// Our own file: a degrade note here means the fixture is writing something this
	// build cannot read, and every assertion below would be measuring the survivors.
	for _, note := range sc.Notes() {
		t.Errorf("w5_clocks.ini: %s", note)
	}
	// The fixture's own claim is that it exercises ALL NINE kinds. A kind that fell
	// out of it (a typo, a rename) would quietly shrink this gate to the kinds that
	// happened to survive, so the claim is checked rather than trusted.
	declared := map[theme.EffectKind]bool{}
	for i := range sc.Elements {
		declared[sc.Elements[i].Effect] = true
	}
	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if !declared[k] {
			t.Errorf("w5_clocks.ini declares no element with effect %q — the fixture is meant to carry "+
				"every kind, and the gate below only measures the ones it does", k)
		}
	}

	// The clock pool, loaded the way pollThemeApply loads it.
	a.applyThemeClocks(sc)
	for _, tc := range []struct{ group, want int }{{1, 250}, {2, 40}, {15, 400}} {
		if got := a.themeClocks[tc.group].speedPct; int(got) != tc.want {
			t.Errorf("[clock.%d] loaded at %d%%, want %d%% — group 15 is the LAST one, and an off-by-one "+
				"in the pool drops exactly that group and nothing else", tc.group, got, tc.want)
		}
	}
	// A fixed frame snapshot: the ratios below are exact facts about the arithmetic,
	// not a race between two time.Now() calls.
	a.themeAt = time.Now()
	a.frameNow = a.themeAt.Add(time.Second)
	defer func() { a.frameNow = time.Time{} }()
	if got := a.clockElapsed(15); got != 4*time.Second {
		t.Errorf("a 400%% group resolved %v at one second on the shared anchor, want 4s", got)
	}
	if got := a.clockElapsed(2); got != 400*time.Millisecond {
		t.Errorf("a 40%% group resolved %v at one second on the shared anchor, want 400ms", got)
	}

	// phase_ms is FREE: two elements on ONE clock group, a fixed interval apart, and
	// no second group and no second piece of state between them.
	pa, pb := findBakedElement(t, lay, sc, "phase_a"), findBakedElement(t, lay, sc, "phase_b")
	if pa.clock != pb.clock {
		t.Fatalf("the phase pair landed on clock groups %d and %d — the fixture puts both on group 2",
			pa.clock, pb.clock)
	}
	if got := a.elementElapsed(pb) - a.elementElapsed(pa); got != time.Second {
		t.Errorf("the phase pair is %v apart, want exactly 1s — phase_ms is arithmetic on the shared "+
			"anchor, so the interval is a fact and not a measurement", got)
	}

	// The bindings. Every one whose widget this layout PLACES must bake as a wash;
	// the one that names no widget at all must be inert (§6.6 R10, skip-if-absent).
	// The expectation is derived from the layout rather than written down, so the
	// gate cannot fail for the uninteresting reason that a default rect moved.
	want := 0
	for i := range sc.Effects {
		b := &sc.Effects[i]
		if _, placed := absSlotRect(lay, b.Target); placed {
			want++
			continue
		}
		if _, ok := a.bakeEffectBind(lay, b); ok {
			t.Errorf("[effect.%s] baked live and this layout does not place %q — an absent target must be "+
				"skip-if-absent, not a plate at the origin", b.Target, b.Target)
		}
	}
	washes := 0
	for i := 0; i < lay.elN; i++ {
		if lay.el[i].wash {
			washes++
		}
	}
	if washes != want || want < 2 {
		t.Errorf("%d of %d [effect.*] sections baked as washes, want %d — the fixture binds three widgets "+
			"the stock layout places and one it does not", washes, len(sc.Effects), want)
	}

	// And the whole frame moves. Non-vacuity for everything above: a fixture that
	// parsed, baked and then sat still would pass every assertion so far.
	draw := func() { a.drawCourtroom(1280, 720) }
	a.frameNow = time.Time{}
	a.themeAt = time.Now().Add(-time.Duration(fxTestPeriodMs) * time.Millisecond / 4)
	a.frameAnimChrome = false
	draw()
	if !a.frameAnimChrome {
		t.Fatal("a themed frame carrying the whole W5 fixture noted nothing — the file parsed and baked " +
			"and does not animate")
	}
	// ReduceMotion stills the same frame. Measured HERE rather than on a synthetic
	// element because this is the shape a user actually has: a real theme, applied.
	//
	// A SUB-TEST so its skip is its own. The census is a whole-frame signal, so the
	// fixture's own chrome can drown the element model out — the same limitation
	// TestIdleElementsDoNotPinTheFrameRate names — and a skip there must not hide
	// the assertions above, which have already run.
	t.Run("reduce motion stills the whole frame", func(t *testing.T) {
		prefs := a.d.Prefs
		if prefs == nil {
			t.Skip("the fixture has no preferences — the accessibility branch cannot be measured")
		}
		prefs.SetReduceMotion(true)
		defer prefs.SetReduceMotion(false)
		// The BASELINE first, with the elements switched off: anything still noting
		// then is the client's own chrome, and the element model cannot be isolated
		// over it. Measured rather than assumed, because the census is a whole-frame
		// signal and this fixture is shared with gates that make no such claim.
		elN := lay.elN
		lay.elN = 0
		draw()
		a.frameAnimChrome = false
		draw()
		quiet := !a.frameAnimChrome
		lay.elN = elN
		if !quiet {
			t.Skip("the fixture's own chrome notes the census under ReduceMotion — this gate cannot " +
				"isolate the element model here")
		}
		draw() // one pass to latch the branch, since it is read at the TOP of a pass
		a.frameAnimChrome = false
		draw()
		if a.frameAnimChrome {
			t.Error("the W5 fixture kept noting the animation census under ReduceMotion — a person who " +
				"turns it on because motion makes them ill is not helped by a theme that keeps moving")
		}
	})
}

// findBakedElement locates one authored element in the SORTED baked array.
//
// The bake sorts by band and z, so a fixture's declaration order is not the array's
// order and an index would be a guess. The authored id is not carried on the baked
// row (it is bake-time-only, by design — a string on the frame path is exactly what
// the element model does not have), so the lookup goes through the sidecar's own
// rect, which the bake does carry.
func findBakedElement(t *testing.T, lay *themeLayoutCache, sc *theme.Sidecar, id string) *bakedElement {
	t.Helper()
	var src *theme.Element
	for i := range sc.Elements {
		if sc.Elements[i].ID == id {
			src = &sc.Elements[i]
			break
		}
	}
	if src == nil {
		t.Fatalf("the fixture declares no [element.%s]", id)
	}
	var hit *bakedElement
	for i := 0; i < lay.elN; i++ {
		e := &lay.el[i]
		if e.kind != src.Kind || e.fx.kind != src.Effect || e.phase != time.Duration(src.PhaseMs)*time.Millisecond {
			continue
		}
		if e.clock != src.Clock || e.col != fadeColor(src.Fill, src.EffectiveOpacity()) {
			continue
		}
		if hit != nil {
			t.Fatalf("two baked rows match [element.%s] — the lookup cannot tell them apart", id)
		}
		hit = e
	}
	if hit == nil {
		t.Fatalf("[element.%s] baked nothing", id)
	}
	return hit
}
