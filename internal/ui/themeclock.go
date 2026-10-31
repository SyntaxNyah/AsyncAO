package ui

// Element clocks and effects. Phase and speed are arithmetic on the one themeAt anchor; a fixed clock pool holds
// groups; a fixed fxState pool holds only the three decaying one-shots' origins. No goroutine, tick or timer: the draw
// site advances. Any frame must be reproducible from `elapsed` alone, or exported GIFs and preset thumbnails would not
// be deterministic. NoteAnimating is called only from a draw site that produced a different pixel this frame, never
// from state.

import (
	"math"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeSpeedNominal is a clock group running at the shared anchor's rate.
	themeSpeedNominal = theme.SpeedNominalPct

	// themeFXCap bounds the stateful-effect pool; the format says this pool must alias theme.EffectBindCap, not restate
	// it.
	themeFXCap = theme.EffectBindCap

	// themeFXIdleFrames is how many themed passes an entry survives unasked before the sweep frees it (2 s at 60 Hz),
	// so a fired one-shot on an element no longer drawn cannot pin the pacer forever.
	themeFXIdleFrames = 120

	// effectIdleEpsilon is the amplitude below which an effect is invisible: 1/255 is the alpha quantum.
	effectIdleEpsilon = 1.0 / 255.0

	// effectAmpFull converts theme.AmpMaxPct into the resolver's 0..1 amplitude.
	effectAmpFull = float64(theme.AmpMaxPct)

	// effectDefaultPeriodMs is the period when `period_ms = 0`, the format's "absent" spelling, not a bad-value
	// fallback.
	effectDefaultPeriodMs = 1200

	// effectMinPeriodMs floors the period at one 60 Hz frame; below it each frame samples a different phase and reads
	// as noise.
	effectMinPeriodMs = 16

	// Spans at amplitude 100, as percentages of the element's short side (offsets) or size (scales), never absolute
	// pixels, so a drift authored on a badge does not throw a full-canvas backdrop across the screen.
	effectBreatheSpanPct = 12 // breathe: ±12% of the element's size at full amplitude
	effectGlowSpanPct    = 8  // glow: the halo's swell
	effectGlowDimPct     = 35 // glow: how far its alpha rides down at the trough
	effectDriftSpanPct   = 6  // drift: the ellipse's radius, as % of the short side
	effectDriftMinorPct  = 50 // drift: the minor axis, as % of the major — an ellipse, not a circle
	effectShakeSpanPct   = 10 // shake: the judder's reach, as % of the short side
	effectSlamSpanPct    = 40 // slam: the impact's initial overshoot

	// effectShakeStepMs quantises the shake's noise: a per-frame sample would differ by frame rate and break export
	// determinism.
	effectShakeStepMs = 40

	// effectShakeHashA / effectShakeHashB are the odd multipliers of the shake's integer hash, a pure function of
	// elapsed. They differ so the X and Y streams do not move in lockstep.
	effectShakeHashA = 2654435761 // Knuth's 32-bit golden-ratio multiplier
	effectShakeHashB = 40503      // the 16-bit one, so the two streams decorrelate
)

// themeClock is one authoring clock group; the zero value is the shared anchor at nominal speed.
type themeClock struct {
	// frozen is the elapsed captured when the group was paused.
	frozen time.Duration
	// speedPct is 10..400; 0 means "never declared" and runs at themeSpeedNominal.
	speedPct int16
	// paused is the editor's scrub and "hold to inspect a frame".
	paused bool
}

// applyThemeClocks loads a theme's [clock.N] groups, called beside `a.themeAt = time.Now()` so a reload restarts every
// phase. A nil sidecar zeroes the pool. The effect pool is dropped too: its generation stamps are already stale, and
// leaving it would refuse the incoming theme's one-shots (frozen at frame 0) until themeFXIdleFrames expires.
func (a *App) applyThemeClocks(sc *theme.Sidecar) {
	for i := range a.themeClocks {
		a.themeClocks[i] = themeClock{speedPct: clampClockSpeed(sc.ClockSpeedPct(i))}
	}
	a.themeFX = [themeFXCap]fxState{}
	a.themeFXOver = false
}

// clampClockSpeed bounds a group's speed: the editor writes this field directly and elementElapsed divides by it.
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

// reduceMotionNow is the one read of the accessibility pref; nil prefs mean motion is allowed.
func (a *App) reduceMotionNow() bool { return a.d.Prefs != nil && a.d.Prefs.Load().ReduceMotionOn }

// elementElapsed is the element's animation position: the anchor scaled by its group's speed, offset by its phase.
func (a *App) elementElapsed(e *bakedElement) time.Duration {
	return a.clockElapsed(e.clock) + e.phase
}

// clockElapsed is one group's position, for [effect.*] binds and the editor's scrub. An out-of-range index falls back
// to clock 0: the reader clamps it already, but a bug here must not panic on the render thread.
func (a *App) clockElapsed(i uint8) time.Duration {
	if int(i) >= len(a.themeClocks) {
		i = 0
	}
	c := &a.themeClocks[i]
	if c.paused {
		return c.frozen
	}
	el := a.themeElapsed()
	// speedPct 0 is an un-applied pool and means nominal, so the stock path matches themeFrame's.
	if c.speedPct > 0 && c.speedPct != themeSpeedNominal {
		el = el * time.Duration(c.speedPct) / themeSpeedNominal
	}
	return el
}

// clockPaused reports that a clock group is held; same out-of-range fallback, so both answer for the same group.
func (a *App) clockPaused(i uint8) bool {
	if int(i) >= len(a.themeClocks) {
		i = 0
	}
	return a.themeClocks[i].paused
}

// noteElementAnimating is the one NoteAnimating call on the element path, so a paused clock never pins the frame rate.
// A paused clock freezes `el`, and downstream one-shots, periodic kinds and looping pages all still report motion.
// Skip the note, not the terms: the scrub must still paint its held frame. Alloc-free and no prefs probe.
func (a *App) noteElementAnimating(clock uint8) {
	if a.clockPaused(clock) {
		return
	}
	a.NoteAnimating()
}

// elementFrame picks an element's frame as themeFrame does: a static page never notes, an animated one does,
// but on the element's own clock, and a one-shot stops noting when pageFrameAt reports done.
func (a *App) elementFrame(e *bakedElement) *sdl.Texture {
	if e.page == nil || len(e.page.Frames) == 0 {
		return nil
	}
	if len(e.page.Frames) == 1 {
		return e.page.Frames[0]
	}
	// ReduceMotion, read once per pass: frame 0, nothing noted.
	if a.themeFXFrozen {
		return e.page.Frames[0]
	}
	idx, live := elementFrameIndex(e.page, a.elementElapsed(e), e.loop)
	if live {
		// Through noteElementAnimating: a looping page on a paused clock reports live=true unconditionally.
		a.noteElementAnimating(e.clock)
	}
	return e.page.Frames[idx]
}

// elementFrameIndex is elementFrame's pure core, so the same elapsed gives the same frame for export and thumbnails.
// `live` means the page will look different next frame; a finished one-shot is not live.
func elementFrameIndex(page *render.TexturePage, el time.Duration, loop bool) (idx int, live bool) {
	if page == nil || len(page.Frames) < 2 {
		return 0, false
	}
	if el < 0 {
		// Not started: pageFrameAt would report frame 0 with done=false on every frame and note forever without
		// advancing. It starts on its own, since drawing continues at the idle cadence and notes again once elapsed
		// crosses zero.
		return 0, false
	}
	if !loop {
		i, done := pageFrameAt(page, el) // court_extras.go
		return i, !done
	}
	return pageFrameLoop(page, el), true
}

// fxOwnerNone is a free entry. The owner is stored as the baked index plus one so the array's zero value is all free.
const fxOwnerNone = int16(0)

// fxState is one decaying effect's origin and last use; the periodic kinds are pure functions of elapsed and take none.
type fxState struct {
	// startedAt is the element's own elapsed when this effect began, so `visible_when = shout:objection` + `effect =
	// slam` lands with the shout.
	startedAt time.Duration
	// gen is the bake generation of the owner index: a rebuild re-numbers the baked array, so entries go stale
	// together.
	gen uint64
	// lastFrame is the themeFXFrame stamp of the last pass that asked for this entry; the sweep's only input.
	lastFrame uint64
	// owner is the baked element index PLUS ONE; fxOwnerNone (0) is free.
	owner int16
}

// beginThemeFXPass advances the frame stamp, sweeps unrequested entries, and records this pass's ReduceMotion answer.
// It sweeps at the top of the next pass because drawCourtroomThemed returns early under a modal, which would leak
// entries.
func (a *App) beginThemeFXPass() {
	a.themeFXFrame++
	a.themeFXOver = false
	// Read once here: the alloc tests forbid a prefs read per element, and a mid-pass change would freeze half a theme.
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

// acquireElementFX finds or claims the pool entry for one baked element, by linear scan.
// ok=false is pool exhaustion: the caller resolves the first frame statically, so an author can see the pool ran out.
func (a *App) acquireElementFX(idx int16, gen uint64, el time.Duration) (*fxState, bool) {
	want := idx + 1 // see fxOwnerNone
	free := -1
	for i := range a.themeFX {
		s := &a.themeFX[i]
		if s.owner == want && s.gen == gen {
			// A gap in the stamps is a re-appearance and restarts the one-shot; otherwise a second shout inside the
			// sweep's window would find it still finished. A same-pass re-ask must not restart anything.
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

// themeFXInUse counts the claimed entries, for diagnostics; the draw path never asks.
func (a *App) themeFXInUse() int {
	n := 0
	for i := range a.themeFX {
		if a.themeFX[i].owner != fxOwnerNone {
			n++
		}
	}
	return n
}

// elemFX is one effect resolved for one frame. Every term is dimensionless, so one epsilon governs all of them:
// offsets are fractions of the short side, rotation is in angle-byte units (theme.AngleCount = one turn).
type elemFX struct {
	dx, dy float64 // offset, as a fraction of the element's short side
	scale  float64 // 1 = neutral
	alpha  float64 // a MULTIPLIER in [0, 1]; 1 = neutral
	rot    float64 // ADDITIONAL rotation, in angle-byte units
	// env is the effect's strength this frame, in [0, 1]. A wash bind's alpha is this value, so `[effect.viewport]
	// effect = fade` clears rather than settling fully opaque over the stage.
	env float64
	// static means no visible change this frame or later without an outside event; if so, NoteAnimating is not called.
	static bool
}

// elemFXNeutral is the identity transform; env 0 means a wash with no live effect paints nothing.
var elemFXNeutral = elemFX{scale: 1, alpha: 1, static: true}

// fxAlwaysOrigin is the shared, never-written origin for a one-shot on an always-visible element: elapsed zero.
// It keeps the pool for conditional elements, so decoration with no state to keep cannot exhaust it.
var fxAlwaysOrigin = &fxState{}

// effectIsStateful reports whether a kind needs a pool entry; a table indexed by the enum so new kinds get a considered
// answer.
var effectIsStateful = [theme.EffectKindCount]bool{
	theme.FXFade:  true,
	theme.FXShake: true,
	theme.FXSlam:  true,
}

// resolveElementEffect is a pure function turning a baked effect into this frame's terms, so exported GIFs reproduce.
// st == nil means no pool entry: a stateful kind resolves at local elapsed 0 and reports static. Static is decided on
// the decay, not the instantaneous value: a sine crosses zero twice a cycle, and testing that value would drop to idle
// and stutter. Periodic kinds are never idle above the epsilon; decaying kinds test their monotonic decay.
func resolveElementEffect(fx bakedEffect, el time.Duration, st *fxState) elemFX {
	if fx.kind == theme.FXNone || fx.kind >= theme.EffectKindCount {
		return elemFXNeutral
	}
	amp := float64(fx.ampPct) / effectAmpFull
	if amp <= effectIdleEpsilon {
		// Amplitude zero is a static element that happens to name an effect.
		return elemFXNeutral
	}
	period := effectPeriodMs(fx.periodMs)
	if !effectIsStateful[fx.kind] {
		return resolvePeriodicEffect(fx.kind, amp, elapsedMs(el)/period)
	}
	// local is elapsed since this one-shot began; with no pool entry it stays 0 and frozen, showing its first frame.
	local, frozen := 0.0, true
	if st != nil {
		local, frozen = elapsedMs(el)-elapsedMs(st.startedAt), false
		if local < 0 {
			// Not started: `phase_ms` may be negative, a backwards clock step makes themeElapsed negative, and the
			// scrub writes frozen. Must be neutral and static: clamping local to 0 would hold the peak and pin the
			// pacer with nothing moving, and env 0 keeps a wash bound to it from painting. It starts by itself when
			// elapsed reaches the origin.
			return elemFXNeutral
		}
	}
	// t walks 0 to 1 over the period and stops; the decay is full at t=0 and monotonic to nothing at t=1.
	t := local / period
	if t > 1 {
		t = 1
	}
	out := resolveDecayEffect(fx.kind, amp, t, local)
	out.static = frozen || amp*(1-t) <= effectIdleEpsilon
	return out
}

// resolvePeriodicEffect is the kinds that are pure functions of the cycle; turns is unwrapped because spin integrates
// it.
func resolvePeriodicEffect(kind theme.EffectKind, amp, turns float64) elemFX {
	out := elemFX{scale: 1, alpha: 1}
	// wave rides 0 to 1 to 0 over one period, so a periodic effect starts neutral instead of snapping to full strength.
	wave := 0.5 - 0.5*math.Cos(2*math.Pi*turns)
	// Drift and spin set env to 1 below: a moving wash has no trough to be dimmer at.
	out.env = wave
	switch kind {
	case theme.FXPulse:
		// Rides down to (1 - amp) at the trough and back.
		out.alpha = 1 - amp*wave
	case theme.FXBreathe:
		// Centred on neutral so the element does not appear to grow permanently.
		out.scale = 1 + amp*float64(effectBreatheSpanPct)/100*(2*wave-1)
	case theme.FXGlow:
		// A swell, not a true additive halo despite the enum's comment: additive needs a second blit in a blend mode
		// the painters don't have.
		out.scale = 1 + amp*float64(effectGlowSpanPct)/100*wave
		out.alpha = 1 - amp*float64(effectGlowDimPct)/100*(1-wave)
	case theme.FXDrift:
		// An ellipse: a linear drift reads as a glitch at the turn.
		ang := 2 * math.Pi * turns
		out.dx = amp * float64(effectDriftSpanPct) / 100 * math.Cos(ang)
		out.dy = amp * float64(effectDriftSpanPct) / 100 * float64(effectDriftMinorPct) / 100 * math.Sin(ang)
		out.env = 1
	case theme.FXSpin:
		// Amplitude scales the rate, so the angle never jumps; clipping the arc would snap back to zero every cycle.
		out.rot = math.Mod(amp*turns, 1) * theme.AngleCount
		out.env = 1
	}
	// Never idle above the epsilon: a static test on the instantaneous value would stutter twice a cycle.
	out.static = false
	return out
}

// resolveDecayEffect is the three one-shots: t is 0 to 1 over the period; local is milliseconds, for the shake's noise.
func resolveDecayEffect(kind theme.EffectKind, amp, t, local float64) elemFX {
	env := 1 - t // the decay envelope, and the static test's input
	out := elemFX{scale: 1, alpha: 1, env: env}
	switch kind {
	case theme.FXFade:
		// A fade in: alpha climbs from (1 - amp) to 1, so a finished one-shot leaves nothing behind.
		out.alpha = 1 - amp*env
	case theme.FXShake:
		// Hashed off a quantised step so the same elapsed always produces the same offset.
		step := int64(local / effectShakeStepMs)
		out.dx = amp * env * float64(effectShakeSpanPct) / 100 * shakeNoise(step, effectShakeHashA)
		out.dy = amp * env * float64(effectShakeSpanPct) / 100 * shakeNoise(step, effectShakeHashB)
	case theme.FXSlam:
		// Lands big and settles, squared so the first part of the travel is the hit.
		out.scale = 1 + amp*float64(effectSlamSpanPct)/100*env*env
		out.env = env * env // a wash driven by a slam flashes and is gone
	}
	return out
}

// shakeNoise is a deterministic [-1, 1] sample for one step: an integer hash so exported GIFs match the screen.
func shakeNoise(step int64, mul int64) float64 {
	h := uint32(step*mul) ^ uint32(step>>16)
	h ^= h >> 13
	h *= 0x5bd1e995 // the MurmurHash2 mixing constant
	h ^= h >> 15
	// [0, 1) to [-1, 1): the divisor is half the uint32 range, so the sample never quite reaches 1.
	return float64(h)/float64(1<<31) - 1
}

// effectPeriodMs resolves an effect's period: the author's, the default when absent, and never below one frame.
func effectPeriodMs(ms int32) float64 {
	if ms <= 0 {
		return effectDefaultPeriodMs // 0 is the format's "absent" spelling
	}
	if ms < effectMinPeriodMs {
		return effectMinPeriodMs
	}
	return float64(ms)
}

// elapsedMs converts a duration to float milliseconds.
func elapsedMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// elemFXSave holds the baked fields applyElementFX overwrites, so drawElement can put them back.
// Mutate and restore, not a scratch copy: painters must get a pointer into the baked array, as the alloc tests assert.
type elemFXSave struct {
	col, col2, stroke, tint sdl.Color
	r                       sdl.Rect
	ang, fxDim              uint8
}

// applyElementFX writes one frame's terms onto the baked element and returns what it overwrote.
func applyElementFX(e *bakedElement, fx elemFX) elemFXSave {
	saved := elemFXSave{r: e.r, col: e.col, col2: e.col2, stroke: e.stroke, tint: e.tint, ang: e.ang, fxDim: e.fxDim}
	if fx.dx != 0 || fx.dy != 0 {
		// Fractions of the short side, so the same amplitude reads the same on a badge and a banner.
		short := float64(min(e.r.W, e.r.H))
		e.r.X += int32(math.Round(fx.dx * short))
		e.r.Y += int32(math.Round(fx.dy * short))
	}
	if fx.scale != 1 {
		// About the centre: growing from the top-left corner would appear to slide.
		w := int32(math.Round(float64(e.r.W) * fx.scale))
		h := int32(math.Round(float64(e.r.H) * fx.scale))
		e.r.X += (e.r.W - w) / 2
		e.r.Y += (e.r.H - h) / 2
		e.r.W, e.r.H = w, h
	}
	if e.wash {
		// A bind: its plate is the effect, so the decay is its alpha, and env 0 lets a `fade` bind clear completely.
		e.col = fxAlpha(e.col, fx.env)
	} else if fx.alpha != 1 {
		// Every colour on the row: procedural painters read col/col2/stroke, the art painter only tint.A. Except text:
		// the label cache keys on ink colour, so darkening would rasterise every frame. fxDim becomes a texture alpha
		// mod in paintElementText.
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
		// Adds to the baked angle, wrapped into the 360/256 byte, so a spin starts where the author put it.
		e.ang = uint8((int32(e.ang) + int32(math.Round(fx.rot))) & (theme.AngleCount - 1))
	}
	return saved
}

// restoreElementFX puts the baked row back on every path out of drawElement that applied anything.
func restoreElementFX(e *bakedElement, s elemFXSave) {
	e.r, e.col, e.col2, e.stroke, e.tint = s.r, s.col, s.col2, s.stroke, s.tint
	e.ang, e.fxDim = s.ang, s.fxDim
}

// fxAlpha multiplies one colour's alpha by a [0, 1] factor; an integer result so unchanged frames cannot differ by
// rounding.
func fxAlpha(c sdl.Color, f float64) sdl.Color {
	if f <= 0 {
		c.A = 0
		return c
	}
	c.A = uint8(math.Round(float64(c.A) * f))
	return c
}
