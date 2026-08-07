package ui

// Element clocks and element effects (v1.90.0 W5) — docs/wip/THEME-EDITOR-DESIGN.md
// §Q3 and §6.6 R2, docs/THEME-FORMAT.md §3.
//
// This file is the whole of "pick an element, pick an effect". Three layers, in
// increasing cost, and MOST MOTION COSTS NOTHING:
//
//  1. Per-element `phase_ms` and a clock group's `speed_pct` are PURE ARITHMETIC on
//     the one themeAt anchor the client already keeps. Six butterflies out of phase
//     consume zero slots and zero state.
//  2. A fixed [theme.ClockCap]themeClock pool carries authoring GROUPS: a speed, plus
//     the paused/frozen pair the editor's scrub needs. Clock 0 is the shared anchor
//     and is byte-identical to themeFrame at speed 100 — every existing animated
//     chrome page is unchanged.
//  3. A fixed [themeFXCap]fxState pool for STATEFUL effects only: the three decaying
//     one-shots need an origin ("when did this start?"), and the other five are pure
//     functions of elapsed and take no slot at all.
//
// NO GOROUTINE, NO UPDATE TICK, NO PER-ELEMENT TIMER. Frame advance is owned by the
// DRAW SITE, exactly as themeFrame (app.go) has always done it — so there is nothing
// new to race and nothing new for -race to find.
//
// WHY PHASE ARITHMETIC AND NOT INDEPENDENT WALL CLOCKS. The argument is a product
// property, not purity: the offscreen export path (themeLayout(w,h), used by
// gifexport.go) and the editor's preset miniatures must reproduce ANY FRAME FROM
// `elapsed` ALONE. Independent wall clocks make an exported GIF non-reproducible and
// a preset thumbnail non-deterministic. Gate: TestElementFrameIsAFunctionOfElapsed.
//
// THE PACING DOCTRINE, WHICH THIS FILE IS THE NEWEST CITIZEN OF:
//
//	NoteAnimating() is called ONLY from a draw site that produced a different pixel
//	this frame — NEVER from state.
//
// This is the v1.55/v1.56 census arc generalized. An idle static element that pins
// the frame rate is the idle-CPU-burn defect; a live element that fails to pin it is
// the worse one (frozen mid-animation). There are exactly three ways an element stops
// moving, and each one has its own self-clear here:
//
//	1. a one-shot page finishes    → elementFrameIndex reports live=false
//	2. an effect's ENVELOPE decays → resolveElementEffect reports static
//	3. it is not visible at all    → drawElement returns before touching the page
//
// It is also on THIS file that TestElementDrawUsesNoStringBuilding runs (it is listed
// in elementDrawFiles beside themeelements.go): everything here is frame path.

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named caps and constants (hard rule 9: no magic numbers)
// ---------------------------------------------------------------------------

const (
	// themeSpeedNominal is a clock group running at the shared theme anchor's rate.
	// An ALIAS of the format's own constant, never a second 100: two copies of a
	// number are two numbers.
	themeSpeedNominal = theme.SpeedNominalPct

	// themeFXCap bounds the STATEFUL-effect pool. It is theme.EffectBindCap by
	// alias, which is itself theme.MediaCap — the format's own constant says in as
	// many words that internal/ui's pool "must alias THIS constant rather than
	// restate the value". One cap, one resource: a stateful effect always dresses
	// art or a generator, and both are bounded there.
	themeFXCap = theme.EffectBindCap

	// themeFXIdleFrames is how many themed passes a slot survives without being
	// asked for before the sweep takes it back. 120 = 2 s at 60 Hz. It is the reason
	// a fired one-shot cannot pin the pacer forever: an element that stopped being
	// drawn (its condition closed, the band emptied, the theme changed) gives its
	// slot back on its own, with no owner left to notice.
	themeFXIdleFrames = 120

	// effectIdleEpsilon is the amplitude below which an effect is PROVABLY
	// invisible: 1/255 is the alpha quantum, and a sub-pixel offset or scale rounds
	// to the same integer rect. At or under it the element draws its neutral form
	// and NOTHING is noted to the pacing census.
	effectIdleEpsilon = 1.0 / 255.0

	// effectAmpFull converts theme.AmpMaxPct into the 0..1 amplitude the resolver
	// works in.
	effectAmpFull = float64(theme.AmpMaxPct)

	// effectDefaultPeriodMs is the period of an effect whose author gave none. A
	// touch over a second: slow enough to read as deliberate on a pulse or a
	// breathe, fast enough that a slam does not hang. `period_ms = 0` is the format's
	// "absent" spelling for every millisecond field, so this is what absent MEANS
	// rather than a fallback for a bad value.
	effectDefaultPeriodMs = 1200

	// effectMinPeriodMs floors the period at one 60 Hz frame. Below it every frame
	// samples a different phase and the effect reads as noise rather than as motion —
	// and it is also what keeps the period out of the denominator's danger zone.
	effectMinPeriodMs = 16

	// The per-effect spans, all expressed at amplitude 100 so `amp_pct` reads as a
	// straight percentage of the effect's own full-strength look. Percentages of the
	// element's SHORT side (offsets) or of its own size (scales), never absolute
	// pixels: a drift authored on a badge must not throw a full-canvas backdrop
	// across the screen.
	effectBreatheSpanPct = 12 // breathe: ±12% of the element's size at full amplitude
	effectGlowSpanPct    = 8  // glow: the halo's swell
	effectGlowDimPct     = 35 // glow: how far its alpha rides down at the trough
	effectDriftSpanPct   = 6  // drift: the ellipse's radius, as % of the short side
	effectDriftMinorPct  = 50 // drift: the minor axis, as % of the major — an ellipse, not a circle
	effectShakeSpanPct   = 10 // shake: the judder's reach, as % of the short side
	effectSlamSpanPct    = 40 // slam: the impact's initial overshoot

	// effectShakeStepMs quantises the shake's noise. Sampling a fresh value every
	// FRAME would make the same elapsed look different at a different frame rate,
	// which breaks the export/thumbnail determinism this whole file is built on —
	// and 25 Hz is what a judder reads as anyway.
	effectShakeStepMs = 40

	// effectShakeHashA / effectShakeHashB are the odd multipliers of the shake's
	// integer hash. A hash rather than math/rand for the same reason as above: the
	// offset has to be a pure function of elapsed. Two different constants so the X
	// and Y streams do not move in lockstep (which would read as a diagonal slide,
	// not a shake).
	effectShakeHashA = 2654435761 // Knuth's 32-bit golden-ratio multiplier
	effectShakeHashB = 40503      // the 16-bit one, so the two streams decorrelate
)

// ---------------------------------------------------------------------------
// The clock pool
// ---------------------------------------------------------------------------

// themeClock is ONE authoring clock group. The zero value is the shared anchor at
// nominal speed, which is why a stock AO2 theme — and a hand-built App in a test —
// needs no initialisation at all: speedPct 0 reads as themeSpeedNominal.
type themeClock struct {
	// frozen is the elapsed captured when the group was paused. It is a value, not a
	// timestamp, precisely so that resuming does not have to reconstruct anything.
	frozen time.Duration
	// speedPct is 10..400; 0 means "never declared" and runs at themeSpeedNominal.
	speedPct int16
	// paused is the editor's scrub and "hold to inspect a frame".
	paused bool
}

// applyThemeClocks loads a theme's [clock.N] groups into the pool.
//
// Called from pollThemeApply beside `a.themeAt = time.Now()` (app.go), so a theme
// reload restarts every phase together — the design's own placement, and the reason
// a swap between two animated themes does not leave the incoming one mid-cycle.
//
// A nil sidecar (every stock AO2 theme) ZEROES the pool rather than leaving it: the
// outgoing theme's speeds are its own, and a stock theme must run at the shared
// anchor exactly as it did before this feature existed.
//
// The STATEFUL-effect pool is dropped here too, and for a stronger reason than
// tidiness. Every fxState is stamped with the bake generation of the theme that
// claimed it, and a theme apply invalidates every one of those at once — so without
// this the incoming theme's first two seconds run against a pool the outgoing theme
// still nominally holds, and its own one-shots are refused (freezing at frame 0)
// until themeFXIdleFrames expires. The sweep would get there; a swap should not have
// to wait for it. Correct rather than merely tidy: no live slot can be lost, because
// every owner's generation is already stale by the time this line runs.
func (a *App) applyThemeClocks(sc *theme.Sidecar) {
	for i := range a.themeClocks {
		a.themeClocks[i] = themeClock{speedPct: clampClockSpeed(sc.ClockSpeedPct(i))}
	}
	a.themeFX = [themeFXCap]fxState{}
	a.themeFXOver = false
}

// clampClockSpeed bounds a group's speed to the format's own range.
//
// DEFENCE IN DEPTH, not duplication: the reader already clamps what it parses, but
// the editor writes this field directly (W7) and elementElapsed divides by it. A
// speed of zero here would be an absent marker in the FILE and a divide-by-nothing in
// the pool, so the two readings are reconciled once, here.
func clampClockSpeed(pct int16) int16 {
	switch {
	case pct <= 0:
		return themeSpeedNominal // the format's "absent" spelling
	case pct < theme.SpeedMinPct:
		return theme.SpeedMinPct
	case pct > theme.SpeedMaxPct:
		return theme.SpeedMaxPct
	}
	return pct
}

// reduceMotionNow is THE read of the accessibility pref — one spelling, shared by
// the two places that latch it (App.Frame and beginThemeFXPass). Nil-safe on
// prefs so a hand-built App in a test behaves like the real one: no preferences
// means nothing was asked for, which is "motion allowed".
func (a *App) reduceMotionNow() bool { return a.d.Prefs != nil && a.d.Prefs.ReduceMotion() }

// elementElapsed is the element's own animation position: the shared anchor, scaled
// by its clock group's speed, offset by its own free phase.
//
// The wave plan calls this "elapsed-for" (TestElapsedForIsAllocFree); the function
// keeps design §Q3's own spelling. ALLOC-FREE and pointer-free: two array indexes,
// one subtraction and at most one multiply-divide.
func (a *App) elementElapsed(e *bakedElement) time.Duration {
	return a.clockElapsed(e.clock) + e.phase
}

// clockElapsed is one clock group's position. Split from elementElapsed because the
// [effect.*] binds and the editor's scrub want the group's own time without an
// element to read it through.
//
// AN OUT-OF-RANGE INDEX FALLS BACK TO CLOCK 0 rather than panicking. The reader
// already clamps `clock = 99` to 0 (parseClockIndex), so this can only fire on a bug
// — but it would fire on the render thread, and one predictable compare is cheaper
// than that risk. TestClockPoolIsFixed pins both halves.
func (a *App) clockElapsed(i uint8) time.Duration {
	if int(i) >= len(a.themeClocks) {
		i = 0
	}
	c := &a.themeClocks[i]
	if c.paused {
		return c.frozen
	}
	el := a.themeElapsed()
	// speedPct 0 is the zero value of an un-applied pool and means nominal, so the
	// stock path takes neither branch and is byte-identical to themeFrame's.
	if c.speedPct > 0 && c.speedPct != themeSpeedNominal {
		el = el * time.Duration(c.speedPct) / themeSpeedNominal
	}
	return el
}

// clockPaused reports that a clock group is HELD — the editor's scrub, and
// "hold to inspect a frame".
//
// Same out-of-range fallback as clockElapsed, and for the same reason: the pair
// must answer for the SAME group, or a bad index would read time from clock 0 and
// pausedness from nothing.
func (a *App) clockPaused(i uint8) bool {
	if int(i) >= len(a.themeClocks) {
		i = 0
	}
	return a.themeClocks[i].paused
}

// noteElementAnimating is the ONE census call on the element path — the funnel
// that makes "a paused clock never pins the frame rate" one line instead of a
// rule every future draw site has to remember.
//
// THE DEFECT IT CLOSES (W5 left it recorded at drawElement's census line, as a
// TODO, because W5 had no writer for `paused`; W7's scrub is the first). A paused
// clock returns themeClock.frozen from clockElapsed, so `el` stops advancing while
// the frame keeps drawing. Everything downstream then reports motion forever with
// nothing moving:
//
//   - a CONDITIONAL one-shot acquires its slot at the frozen elapsed, so
//     startedAt == el permanently: local 0, t 0, env 1, static = false;
//   - the five PERIODIC kinds return static=false unconditionally, by design;
//   - a LOOPING page's elementFrameIndex returns live=true unconditionally, by the
//     same design.
//
// Each of those is correct in isolation and each of them is the idle-CPU-burn
// defect (the v1.55/v1.56 arc) once the clock stops. Scrubbing to a frame would
// pin the client at the animation cadence, on a still picture, until the user
// resumed — and there would be nothing on screen to explain it.
//
// GATE THE CENSUS, NOT THE TERMS. The resolver is untouched and the terms are
// still applied: holding the frame is the entire point of a scrub, and a wash's
// envelope still has to paint its held state. Clamping elapsed, or making a paused
// clock resolve neutral, would blank the very frame the scrub exists to inspect.
//
// One bounds-checked array read, no allocation, no prefs probe: it is inside both
// AllocsPerRun gates over the element path.
func (a *App) noteElementAnimating(clock uint8) {
	if a.clockPaused(clock) {
		return
	}
	a.NoteAnimating()
}

// ---------------------------------------------------------------------------
// Frame advance — themeFrame's twin, with the census contract intact
// ---------------------------------------------------------------------------

// elementFrame picks an element's current animation frame.
//
// It honours themeFrame's contract exactly (app.go): a STATIC page costs one len
// check and never notes the census, an animated one notes it so the pacer keeps
// stepping. The two differences are the element's own clock (elementElapsed rather
// than themeElapsed) and `loop`, which themeFrame has no spelling for — a one-shot
// stops noting the moment pageFrameAt reports done, which is self-clear path 1.
func (a *App) elementFrame(e *bakedElement) *sdl.Texture {
	if e.page == nil || len(e.page.Frames) == 0 {
		return nil
	}
	if len(e.page.Frames) == 1 {
		return e.page.Frames[0]
	}
	// ReduceMotion, latched once per pass rather than read per element: frame 0,
	// nothing noted. The accessibility branch and the pacing census agree by
	// construction, because they are the same return.
	if a.themeFXFrozen {
		return e.page.Frames[0]
	}
	idx, live := elementFrameIndex(e.page, a.elementElapsed(e), e.loop)
	if live {
		// Through the funnel, never bare: a LOOPING page on a paused clock holds one
		// frame and elementFrameIndex reports live=true for it unconditionally (that is
		// its contract — a loop never ends), so this is the second site the scrub would
		// have pinned the frame rate from. See noteElementAnimating.
		a.noteElementAnimating(e.clock)
	}
	return e.page.Frames[idx]
}

// elementFrameIndex is elementFrame's PURE core: elapsed in, (frame, still-moving)
// out, with no App and no clock behind it.
//
// Pure on purpose. It is what makes TestElementFrameIsAFunctionOfElapsed a real test
// rather than a tautology, and it is the contract the offscreen export and the preset
// thumbnails rest on — the same elapsed must always produce the same frame, on any
// machine, at any frame rate, months apart.
//
// `live` means "this page will look different next frame", which is exactly the
// question NoteAnimating answers. A finished one-shot is NOT live even though it is
// still showing its last frame.
func elementFrameIndex(page *render.TexturePage, el time.Duration, loop bool) (idx int, live bool) {
	if page == nil || len(page.Frames) < 2 {
		return 0, false
	}
	if el < 0 {
		// NOT YET STARTED — the same state resolveElementEffect answers neutral for, and
		// the same trap in the same shape. pageFrameAt reads a negative elapsed as
		// "before the first delay", so a one-shot page would report frame 0 with
		// done=false on every frame and note the census forever without ever advancing;
		// pageFrameLoop's negative remainder holds frame 0 just as motionlessly. A page
		// whose clock has not reached it is a STILL PICTURE, and a still picture that
		// pins the frame rate is the defect this file's doctrine exists to prevent.
		//
		// It self-starts: the client keeps drawing at the idle cadence, so elapsed goes
		// on advancing and the page notes again on the first frame it crosses zero.
		return 0, false
	}
	if !loop {
		i, done := pageFrameAt(page, el) // court_extras.go
		return i, !done
	}
	return pageFrameLoop(page, el), true
}

// ---------------------------------------------------------------------------
// The stateful-effect pool
// ---------------------------------------------------------------------------

// fxOwnerNone is a free slot. The owner is stored as the baked index PLUS ONE so
// that the ZERO VALUE of the array is a pool of free slots — which means App needs
// no constructor for it, a hand-built test App behaves like the real one, and there
// is no "did anyone initialise this?" failure mode waiting on the render thread.
const fxOwnerNone = int16(0)

// fxState is one STATEFUL effect's hysteresis: where its one-shot began, and whether
// anybody is still asking for it.
//
// Only the three DECAYING kinds (fade / shake / slam) take a slot. The five periodic
// ones are pure functions of elapsed and would gain nothing from state — which is the
// whole reason the pool can be 24 entries for a format that allows 96 elements.
type fxState struct {
	// startedAt is the element's OWN elapsed at the moment this effect began, so its
	// one-shot plays from when the element appeared rather than from when the theme
	// applied. That is what makes `visible_when = shout:objection` + `effect = slam`
	// a stamp that lands when the shout does, with no trigger syntax (design §6.6 R5
	// cut event triggers for v1.90.0, and this needs none).
	startedAt time.Duration
	// gen is the bake generation the owner index belongs to. A cold rebuild (a resize,
	// a theme apply, a fit change) re-numbers the baked array, so without this a slot
	// would keep answering for whatever element inherited its index. Every slot goes
	// stale together on a rebuild and the sweep collects them.
	gen uint64
	// lastFrame is the themeFXFrame stamp of the last pass that asked for this slot.
	// The sweep's only input.
	lastFrame uint64
	// owner is the baked element index PLUS ONE; fxOwnerNone (0) is free.
	owner int16
}

// beginThemeFXPass advances the pool's frame stamp, sweeps the slots nobody asked
// for, and latches this pass's ReduceMotion answer.
//
// ONCE PER THEMED PASS, at the top, from refreshElementConditionsFrom — the one hook
// both the live courtroom and the offscreen export already share.
//
// SWEEPING AT THE TOP OF THE NEXT PASS rather than at the bottom of this one is a
// deliberate deviation from the design's wording, and the reason is the themed pass's
// MID-SEQUENCE ABORT: drawCourtroomThemed returns early when a modal owns the screen
// (theme_layout.go), so code at the bottom of that function does not run on every
// frame. A sweep that skipped the frames a popup was open would leak slots for
// exactly as long as the popup stayed open. Same set, one frame later, and it cannot
// be skipped.
func (a *App) beginThemeFXPass() {
	a.themeFXFrame++
	a.themeFXOver = false
	// ReduceMotion is the ONLY accessibility gate in this feature, and it is read
	// ONCE here rather than per element: the two AllocsPerRun gates forbid a prefs
	// read on the per-element path, and a pass that changed its mind halfway would
	// freeze half a theme. App.Frame latches the same answer for the theme-chrome
	// draw sites, which run outside any pass; both go through reduceMotionNow.
	a.themeFXFrozen = a.reduceMotionNow()
	for i := range a.themeFX {
		s := &a.themeFX[i]
		if s.owner == fxOwnerNone {
			continue
		}
		if a.themeFXFrame-s.lastFrame > themeFXIdleFrames {
			*s = fxState{}
		}
	}
}

// acquireElementFX finds (or claims) the pool slot for one baked element.
//
// A LINEAR SCAN OF 24 — cheaper than a map probe, allocation-free, and bounded by a
// constant rather than by the theme. ok=false is POOL EXHAUSTION, which is graceful
// and named: the caller then resolves the effect at its FIRST FRAME, statically. That
// is the design's own ruling, and it is the right one — a frozen-at-frame-0 effect is
// visible, so an author (and W7's editor chip) can SEE that the pool ran out, where a
// silently-neutral element would look like the effect simply did not work.
func (a *App) acquireElementFX(idx int16, gen uint64, el time.Duration) (*fxState, bool) {
	want := idx + 1 // see fxOwnerNone
	free := -1
	for i := range a.themeFX {
		s := &a.themeFX[i]
		if s.owner == want && s.gen == gen {
			// A GAP IN THE STAMPS IS A RE-APPEARANCE, and a re-appearance restarts the
			// one-shot. Without this a stamp bound to `visible_when = shout:objection`
			// would fire on the first shout and then sit finished — a second shout inside
			// the sweep's two-second window would find the slot still held and still done.
			// The pool's job is to remember WHEN this effect began, and it began again.
			// Strictly "missed a pass": a same-pass re-ask (which the draw loop cannot
			// produce, but a caller might) must not restart anything.
			if s.lastFrame+1 < a.themeFXFrame {
				s.startedAt = el
			}
			s.lastFrame = a.themeFXFrame
			return s, true
		}
		if free < 0 && s.owner == fxOwnerNone {
			free = i
		}
	}
	if free < 0 {
		a.themeFXOver = true
		return nil, false
	}
	s := &a.themeFX[free]
	*s = fxState{startedAt: el, gen: gen, lastFrame: a.themeFXFrame, owner: want}
	return s, true
}

// themeFXInUse counts the claimed slots. Diagnostics and gates only — the draw path
// never asks.
func (a *App) themeFXInUse() int {
	n := 0
	for i := range a.themeFX {
		if a.themeFX[i].owner != fxOwnerNone {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// The resolver — ONE function, nine kinds, five terms
// ---------------------------------------------------------------------------

// elemFX is one effect resolved for one frame: design §6.6 R2's
// (offset, scale, alpha, rot, static), as a value.
//
// EVERY TERM IS DIMENSIONLESS, which is what lets one epsilon govern all of them.
// The offsets are fractions of the element's SHORT side and the rotation is in
// angle-byte units (theme.AngleCount = one turn); applyElementFX is the only place
// they meet a pixel.
//
// R2 IN ONE SENTENCE: the rot term exists because the brief NAMES spinning objection
// graphics, CopyEx + ThemeRectRotations already prove rotated draws, and one term at
// design time is free where deleting a brief-named signature is not. It has been
// carried, unread, in bakedEffect since W3 for exactly this wave.
type elemFX struct {
	dx, dy float64 // offset, as a fraction of the element's short side
	scale  float64 // 1 = neutral
	alpha  float64 // a MULTIPLIER in [0, 1]; 1 = neutral
	rot    float64 // ADDITIONAL rotation, in angle-byte units
	// env is the effect's STRENGTH this frame, in [0, 1]: the scalar every arm above
	// computes on its way to its own terms (a decay for the one-shots, the wave for
	// the periodic ones, a constant for the two that only move geometry).
	//
	// A SIXTH TERM BESIDE R2's FIVE, and it earns its place: an [effect.<widget key>]
	// bind paints a plate that EXISTS ONLY BECAUSE OF THE EFFECT, so its alpha is the
	// effect's strength itself rather than a modulation of an alpha it was authored
	// with. Without it, `[effect.viewport] effect = fade` — a shipped theme's
	// scene-transition curtain — would settle at fully opaque over the whole stage
	// instead of clearing. See bakedElement.wash and applyElementFX.
	env float64
	// static reports that this effect produced no visible change this frame AND will
	// not produce one later without something else happening. It is the pacing
	// census's whole input: static ⇒ nothing is noted.
	static bool
}

// elemFXNeutral is the identity transform — what FXNone, amplitude zero, and
// ReduceMotion all resolve to. env is 0, so a wash with no live effect paints
// nothing at all, which is the only reading of "this plate is the effect".
var elemFXNeutral = elemFX{scale: 1, alpha: 1, static: true}

// fxAlwaysOrigin is the slot-free origin for a one-shot on an element that is ALWAYS
// visible: its effect began when the theme applied, so its origin is elapsed zero and
// there is nothing to remember.
//
// It is what keeps the pool for the case it was designed for — a CONDITIONAL element
// whose one-shot has to replay each time its condition opens (a stamp that lands with
// a shout). Every [effect.*] bind and every unconditional element resolves through
// this shared, never-written value instead, so a theme cannot exhaust the pool with
// decoration that had no state to keep in the first place.
var fxAlwaysOrigin = &fxState{}

// effectIsStateful reports whether a kind needs a pool slot: the three DECAYING
// one-shots do (they need an origin), the five periodic ones do not.
//
// A table rather than a switch, and indexed by the enum, so a kind appended to
// theme.EffectKind gets a considered answer here instead of falling into whichever
// half the switch's default happened to be.
var effectIsStateful = [theme.EffectKindCount]bool{
	theme.FXFade:  true,
	theme.FXShake: true,
	theme.FXSlam:  true,
}

// resolveElementEffect turns a baked effect into this frame's five terms.
//
// PURE. No App, no prefs, no clock — elapsed and (for a stateful kind) the slot's
// origin are the entire input, which is what TestElementFrameIsAFunctionOfElapsed's
// sibling determinism check rests on and what makes an exported GIF reproducible.
//
// st == nil means NO POOL SLOT: a stateful kind then resolves at local elapsed 0 and
// reports static, which is the named, graceful exhaustion path. A periodic kind
// ignores st entirely and never asks for one.
//
// A NEGATIVE ELAPSED IS "NOT YET STARTED", and a one-shot resolves NEUTRAL for it —
// still, and painting nothing. (A periodic kind needs no such arm: a cosine of a
// negative phase is an ordinary point on the cycle, and it really is moving.) See the
// branch below for the three ways a live client reaches one.
//
// STATIC IS DECIDED ON THE ENVELOPE, NOT ON THE INSTANTANEOUS VALUE — and this is the
// single most load-bearing line in the file. A sine crosses zero twice a cycle, so a
// static test on the current offset would report "nothing moved" at the crossing,
// drop the frame rate to idle, and stutter a running animation twice per period. The
// periodic kinds are therefore NEVER idle above the epsilon (design §Q3 says so in as
// many words: "they note forever, which is correct and is what the author asked
// for"), and the decaying kinds test their DECAY, which is monotonic.
func resolveElementEffect(fx bakedEffect, el time.Duration, st *fxState) elemFX {
	if fx.kind == theme.FXNone || fx.kind >= theme.EffectKindCount {
		return elemFXNeutral
	}
	amp := float64(fx.ampPct) / effectAmpFull
	if amp <= effectIdleEpsilon {
		// Amplitude zero is a STATIC element that happens to name an effect. Every
		// kind returns here, which is half of TestEveryEffectKindIsResolvable.
		return elemFXNeutral
	}
	period := effectPeriodMs(fx.periodMs)
	if !effectIsStateful[fx.kind] {
		return resolvePeriodicEffect(fx.kind, amp, elapsedMs(el)/period)
	}
	// The decaying family. local is elapsed since THIS one-shot began.
	//
	// st == nil is POOL EXHAUSTION: local stays 0 and frozen stays true, so the effect
	// shows its first frame and reports static — the named graceful degrade.
	local, frozen := 0.0, true
	if st != nil {
		local, frozen = elapsedMs(el)-elapsedMs(st.startedAt), false
		if local < 0 {
			// NOT YET STARTED, which is a real state and not a curiosity: `phase_ms` is
			// legally NEGATIVE (theme.TimeMsMin, sidecar.go — the reader CLAMPS `phase_ms`
			// into that range, it does not refuse the sign), any backwards wall-clock
			// step (an NTP correction, DST, resume-from-sleep) makes themeElapsed negative
			// for every element at once, and W7's scrub writes themeClock.frozen directly.
			//
			// It must resolve NEUTRAL — which is to say STATIC, and env 0. Clamping local
			// to 0 instead (i.e. "replay rather than run in reverse") is right about the
			// TERMS and wrong about the CENSUS: it pins t at 0, so the envelope sits at
			// full amplitude and static is false on EVERY frame while elapsed stays
			// negative. The one-shot never advances, holds its peak, and holds the pacer
			// at the animation cadence with nothing moving on screen — for ten minutes
			// after a theme apply with `phase_ms = -600000`, or forever on a scrubbed
			// clock. That is the idle-CPU-burn defect (the v1.56.0 arc), and replay that
			// cannot end is not replay.
			//
			// env 0 is the other half: a WASH bound to a not-yet-started one-shot must
			// paint nothing, exactly as a finished one does, rather than its full authored
			// peak over the widget it decorates. The effect starts on its own the moment
			// elapsed reaches its origin — nothing has to remember to restart it.
			return elemFXNeutral
		}
	}
	// t walks 0 → 1 over the period and stops there. env is the decay envelope: full
	// strength at t=0, nothing at t=1, and monotonic in between.
	t := local / period
	if t > 1 {
		t = 1
	}
	out := resolveDecayEffect(fx.kind, amp, t, local)
	out.static = frozen || amp*(1-t) <= effectIdleEpsilon
	return out
}

// resolvePeriodicEffect is the five kinds that are pure functions of the cycle
// position. turns is the (unwrapped) number of periods elapsed — unwrapped because
// spin integrates it, and wrapping is the sine's own job for the rest.
func resolvePeriodicEffect(kind theme.EffectKind, amp, turns float64) elemFX {
	out := elemFX{scale: 1, alpha: 1}
	// wave rides 0 → 1 → 0 over one period (a raised cosine), so every periodic
	// effect starts at its neutral end rather than snapping to full strength on the
	// first frame a theme applies.
	wave := 0.5 - 0.5*math.Cos(2*math.Pi*turns)
	// The two kinds that only move GEOMETRY hold their strength constant: a drifting
	// or spinning wash is at its authored opacity the whole way round, because there
	// is no "trough" of a drift to be dimmer at.
	out.env = wave
	switch kind {
	case theme.FXPulse:
		// Periodic alpha: rides down to (1 - amp) at the trough and back.
		out.alpha = 1 - amp*wave
	case theme.FXBreathe:
		// Periodic scale, centred on neutral so the element does not appear to grow
		// permanently: ±effectBreatheSpanPct at full amplitude.
		out.scale = 1 + amp*float64(effectBreatheSpanPct)/100*(2*wave-1)
	case theme.FXGlow:
		// A SWELL, NOT A TRUE ADDITIVE HALO — stated plainly because the enum's own
		// comment says "additive". The five resolver terms are geometry and alpha; an
		// additive pass means a second blit per element in a blend mode the shape,
		// gradient and text painters have no spelling for, which is a painter-contract
		// change and not this wave's. What ships is the halo's READ: the element swells
		// and brightens back to full on the same cycle.
		out.scale = 1 + amp*float64(effectGlowSpanPct)/100*wave
		out.alpha = 1 - amp*float64(effectGlowDimPct)/100*(1-wave)
	case theme.FXDrift:
		// A slow ellipse rather than a line: a linear drift reads as a glitch at the
		// turn, where an ellipse reads as float.
		ang := 2 * math.Pi * turns
		out.dx = amp * float64(effectDriftSpanPct) / 100 * math.Cos(ang)
		out.dy = amp * float64(effectDriftSpanPct) / 100 * float64(effectDriftMinorPct) / 100 * math.Sin(ang)
		out.env = 1
	case theme.FXSpin:
		// §6.6 R2's whole point. CONTINUOUS: amplitude scales the RATE, so half
		// amplitude is half a turn per period and the angle never jumps — a spin whose
		// amplitude clipped its arc would snap back to zero every cycle.
		out.rot = math.Mod(amp*turns, 1) * theme.AngleCount
		out.env = 1
	}
	// Periodic effects are never idle above the epsilon. See the resolver's doc: a
	// static test on the instantaneous value would stutter twice a cycle.
	out.static = false
	return out
}

// resolveDecayEffect is the three one-shots. t is 0 → 1 over the period; local is the
// same position in milliseconds, which only the shake's deterministic noise needs.
func resolveDecayEffect(kind theme.EffectKind, amp, t, local float64) elemFX {
	env := 1 - t // the decay envelope, and the static test's input
	out := elemFX{scale: 1, alpha: 1, env: env}
	switch kind {
	case theme.FXFade:
		// A fade IN that settles at neutral: alpha climbs from (1 - amp) to 1. That is
		// what "decaying" means for every kind here — the EFFECT decays, converging on
		// the element's authored look, so a finished one-shot leaves nothing behind.
		out.alpha = 1 - amp*env
	case theme.FXShake:
		// Decaying offset noise. The noise is hashed off a QUANTISED step (see
		// effectShakeStepMs) so the same elapsed always produces the same offset.
		step := int64(local / effectShakeStepMs)
		out.dx = amp * env * float64(effectShakeSpanPct) / 100 * shakeNoise(step, effectShakeHashA)
		out.dy = amp * env * float64(effectShakeSpanPct) / 100 * shakeNoise(step, effectShakeHashB)
	case theme.FXSlam:
		// A one-shot scale impact: lands big and settles, squared so the last third of
		// the travel is the slow part and the first third is the hit.
		out.scale = 1 + amp*float64(effectSlamSpanPct)/100*env*env
		out.env = env * env // a wash driven by a slam flashes and is gone
	}
	return out
}

// shakeNoise is a deterministic [-1, 1] sample for one quantised shake step.
//
// An integer hash, not math/rand and not a per-frame value: the shake has to be a
// pure function of elapsed or an exported GIF stops matching the screen it was
// exported from. Two multipliers give the X and Y streams different sequences without
// a second table.
func shakeNoise(step int64, mul int64) float64 {
	h := uint32(step*mul) ^ uint32(step>>16)
	h ^= h >> 13
	h *= 0x5bd1e995 // the MurmurHash2 mixing constant
	h ^= h >> 15
	// [0, 1) → [-1, 1). The divisor is the full uint32 range, so the sample is
	// symmetric and never quite reaches ±1.
	return float64(h)/float64(1<<31) - 1
}

// effectPeriodMs resolves the period an effect actually runs at: the author's, the
// documented default when they gave none, and never below one frame.
func effectPeriodMs(ms int32) float64 {
	if ms <= 0 {
		return effectDefaultPeriodMs // 0 is the format's "absent" spelling
	}
	if ms < effectMinPeriodMs {
		return effectMinPeriodMs
	}
	return float64(ms)
}

// elapsedMs converts a duration to float milliseconds. Named because the conversion
// appears four times and because `float64(d) / 1e6` is the kind of line that gets
// "simplified" into a wrong unit.
func elapsedMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// ---------------------------------------------------------------------------
// Applying the terms
// ---------------------------------------------------------------------------

// elemFXSave holds the baked fields applyElementFX overwrites, so drawElement can put
// them back.
//
// MUTATE-AND-RESTORE, NOT A SCRATCH COPY, and the reason is the painter contract: the
// two AllocsPerRun gates assert that every painter is handed a POINTER INTO the baked
// array (&lay.el[i]) rather than a copy, and drawing an effect through a scratch
// element would quietly break that for exactly the elements that move. It is also the
// idiom this file's neighbour already uses for the same shape of problem —
// paintElementArt sets a texture's colour mod and restores it inline, because the page
// is shared. This struct is a local that does not escape, so it costs a stack slot and
// nothing else.
type elemFXSave struct {
	col, col2, stroke, tint sdl.Color
	r                       sdl.Rect
	ang, fxDim              uint8
}

// applyElementFX writes one frame's terms onto the baked element and returns what it
// overwrote. Alloc-free: integer rect arithmetic, four colour multiplies and one
// angle add.
func applyElementFX(e *bakedElement, fx elemFX) elemFXSave {
	saved := elemFXSave{r: e.r, col: e.col, col2: e.col2, stroke: e.stroke, tint: e.tint, ang: e.ang, fxDim: e.fxDim}
	if fx.dx != 0 || fx.dy != 0 {
		// The offsets are fractions of the SHORT side, so the same authored amplitude
		// reads the same on a badge and on a banner.
		short := float64(min32(e.r.W, e.r.H))
		e.r.X += int32(math.Round(fx.dx * short))
		e.r.Y += int32(math.Round(fx.dy * short))
	}
	if fx.scale != 1 {
		// Scaled about the CENTRE: an element that grew from its top-left corner would
		// appear to slide, which is the one thing a breathe must not do.
		w := int32(math.Round(float64(e.r.W) * fx.scale))
		h := int32(math.Round(float64(e.r.H) * fx.scale))
		e.r.X += (e.r.W - w) / 2
		e.r.Y += (e.r.H - h) / 2
		e.r.W, e.r.H = w, h
	}
	if e.wash {
		// A BIND. Its plate is the effect, so the effect's ENVELOPE is its alpha —
		// scaled off the peak the bake folded `amp_pct` into. env 0 paints nothing,
		// which is what lets a `fade` bind clear completely and a `glow` bind reach
		// the trough of its cycle instead of leaving a permanent tint over a widget.
		e.col = fxAlpha(e.col, fx.env)
	} else if fx.alpha != 1 {
		// EVERY colour on the row, because the kinds do not share one alpha knob: the
		// procedural painters read col / col2 / stroke and the art painter reads only
		// tint.A (SetAlphaMod). An effect that dimmed three of the four would be an
		// effect that works on some kinds and not others.
		//
		// TEXT IS THE ONE EXCEPTION, and it is a frame-cost exception rather than a
		// design one: the label cache keys on the ink COLOUR (ui.go textKey), so
		// darkening it would rasterise a fresh texture on every frame a fade moved.
		// The alpha goes to fxDim instead and paintElementText spends it as a texture
		// alpha mod over the same cached glyphs — same pixels, no raster.
		if e.kind == theme.ElemText {
			e.fxDim = 255 - uint8(math.Round(255*fx.alpha))
		} else {
			e.col = fxAlpha(e.col, fx.alpha)
		}
		e.col2 = fxAlpha(e.col2, fx.alpha)
		e.stroke = fxAlpha(e.stroke, fx.alpha)
		e.tint = fxAlpha(e.tint, fx.alpha)
	}
	if fx.rot != 0 {
		// The effect's rotation ADDS to the element's own baked angle, wrapped into the
		// 360/256 byte — so a spin on an already-rotated element spins from where the
		// author put it.
		e.ang = uint8((int32(e.ang) + int32(math.Round(fx.rot))) & (theme.AngleCount - 1))
	}
	return saved
}

// restoreElementFX puts the baked row back. Called on EVERY path out of drawElement
// that applied anything, so the cache is never left holding one frame's motion.
func restoreElementFX(e *bakedElement, s elemFXSave) {
	e.r, e.col, e.col2, e.stroke, e.tint = s.r, s.col, s.col2, s.stroke, s.tint
	e.ang, e.fxDim = s.ang, s.fxDim
}

// fxAlpha multiplies one colour's alpha by a [0, 1] factor. Integer result, so two
// frames of an unchanged window cannot disagree by a rounding step.
func fxAlpha(c sdl.Color, f float64) sdl.Color {
	if f <= 0 {
		c.A = 0
		return c
	}
	c.A = uint8(math.Round(float64(c.A) * f))
	return c
}
