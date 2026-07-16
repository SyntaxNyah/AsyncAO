package render

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	// offsetPercentDivisor converts AO offsets (percent) into pixels.
	offsetPercentDivisor = 100
	// shakeAmplitudeDivisor scales the screenshake wobble to the viewport
	// (vp.W / divisor at full strength — a few pixels at 4:3 sizes).
	shakeAmplitudeDivisor = 48
	// shakePeriod is the wobble oscillation period (fast enough to read as
	// a shake, slow enough that 60 Hz sampling doesn't alias it away).
	shakePeriod = 90 * time.Millisecond

	// Rainbow-sprite hue speed maps the SpriteFX.Speed slider [1,100] to a
	// hue-rotation period. minRainbowCycle is a HARD positive floor: the phase
	// clock does phase%cycle and rainbowMod divides by cycle, so a zero period
	// would panic the render loop every frame — the period must never reach 0.
	// (~2.6 s at the default speed 70 reads as a rainbow without strobing.)
	minRainbowCycle = 300 * time.Millisecond  // fastest (Speed=100)
	maxRainbowCycle = 8000 * time.Millisecond // slowest (Speed=1)

	// spriteFloorMax is the channel floor at Vividness=0 (subtlest tint). The
	// floor lifts every channel of the colour-mod off zero so a saturated hue
	// tints the sprite instead of crushing two channels to a flat silhouette;
	// SetColorMod multiplies (out = texel*mod/255), so a higher floor keeps
	// more of the art's own brightness. Vividness=100 drops the floor to 0.
	spriteFloorMax = 200
)

// animState tracks playback of one sprite layer without allocations: the
// loop bumps an index and swaps texture pointers (spec §12). It also caches
// the resolved TexturePage against the store generation, so steady-state
// frames perform zero LRU lookups — pure pointer math.
type animState struct {
	base     string
	frame    int
	elapsed  time.Duration
	finished bool
	// startReported guards the one-shot OnPreanimStart duration report: set once
	// the decoded preanim page first plays, cleared by reset on a base change.
	startReported bool
	// loopReported guards the ONE OnPreanimDone report when the loop-preanim pref
	// is ON: the clip's first natural completion fires OnPreanimDone once and sets
	// this, then subsequent wraps (the pref keeps the preanim looping) never
	// re-fire it — so the courtroom's message lifecycle is byte-identical to
	// loop-off. Cleared by reset on a base change (a fresh preanim re-arms).
	loopReported bool
	// shownSrc is the SOURCE (un-decimated) frame index last reported through
	// OnFrameShown for this base — the #17 frame-effect trigger uses it to fire
	// only when the drawn frame moves to a new source frame. -1 = nothing
	// reported yet for this base (reset on a base change); frame 0 IS a valid
	// trigger point, so the sentinel can't be 0.
	shownSrc int

	page    *TexturePage
	pageGen uint64

	// coldFor accumulates how long this layer's base has been unresolved (ticked
	// in Update, reset on a hit) — the hold-previous max-age knob reads it.
	coldFor time.Duration
	// fadeLeft counts down a speaker-swap crossfade: armed on a base change
	// (syncAnim) when the knob is on, ticked only while the NEW sprite is
	// resident (a cold load doesn't consume the fade), and while > 0 the draw
	// blends lastGood under the alpha-ramped new sprite.
	fadeLeft time.Duration
	// thumbKey is this base's precomputed thumb:// T1 key ("" when no base) —
	// built once in reset so the cold-load miss path stays allocation-free.
	thumbKey string
	// heldKey is this base's precomputed held:// pinned key — the held-frame
	// bridge the store parks a stolen scenery frame under when the LRU must
	// evict the ON-SCREEN background/desk (the black-flash fix). Precomputed
	// like thumbKey so the scenery miss path stays allocation-free.
	heldKey string

	// lastGood is the base of the last sprite this layer actually DREW (set in
	// drawSprite on a hit). It is deliberately NOT cleared by reset, so when the
	// layer swaps to a still-loading base the renderer can keep the previous
	// sprite on screen (SpriteLoadHoldPrev) instead of flashing empty. Held by
	// STRING, re-Get through the store each frame — never a cached page pointer,
	// which could be evicted underfoot.
	lastGood string
}

// reset rebinds the state to a new asset base. lastGood survives on purpose (see
// the field comment) — the cold-load hold reads it after a base change. thumbKey
// is precomputed HERE (a base change is a rare, event-driven moment) so the
// per-frame miss path never builds a string.
func (a *animState) reset(base string) {
	a.base = base
	a.frame = 0
	a.elapsed = 0
	a.finished = false
	a.startReported = false
	a.loopReported = false
	a.shownSrc = -1 // nothing reported for the new base yet (frame 0 is a valid trigger)
	a.page = nil
	a.pageGen = 0
	a.thumbKey = ""
	a.heldKey = ""
	if base != "" {
		a.thumbKey = ThumbKeyPrefix + base
		a.heldKey = HeldKeyPrefix + base
	}
}

// resolve returns the cached page, re-querying the store only when its
// generation moved (upload/eviction/purge) or the base changed.
func (a *animState) resolve(store *TextureStore) (*TexturePage, bool) {
	if a.base == "" {
		return nil, false
	}
	gen := store.Generation()
	if a.page != nil && a.pageGen == gen {
		return a.page, true
	}
	page, ok := store.Get(a.base)
	if !ok {
		a.page = nil
		a.pageGen = gen
		return nil, false
	}
	a.page = page
	a.pageGen = gen
	return page, true
}

// advance steps frame timing. One-shot animations stop on the last frame
// and report completion exactly once.
func (a *animState) advance(page *TexturePage, dt time.Duration, playOnce bool) (justFinished bool) {
	if page == nil || len(page.Frames) <= 1 {
		if playOnce && !a.finished {
			// Static "animation": a single frame finishes immediately.
			// INVARIANT: finished is set ONLY under playOnce (here and at the
			// last-frame case below). The Update speaker block's "told-to-loop
			// restart" guard depends on this — a finished layer is always a
			// one-shot that already completed, never a live idle/talk loop.
			a.finished = true
			return true
		}
		return false
	}
	if a.finished {
		return false
	}
	a.elapsed += dt
	for {
		delay := page.Delays[a.frame]
		if delay <= 0 || a.elapsed < delay {
			return false
		}
		a.elapsed -= delay
		if a.frame == len(page.Frames)-1 {
			if playOnce {
				// INVARIANT: finished is set ONLY under playOnce (here and the
				// single-frame case above). The Update speaker block's told-to-loop
				// restart guard relies on this — never latch finished for a layer
				// that could later be a looping idle/talk sprite.
				a.finished = true
				return true
			}
			a.frame = 0
		} else {
			a.frame++
		}
	}
}

// advanceSpeaker steps the SPEAKER layer's frame timing for this Update and
// reports whether the courtroom's OnPreanimDone should fire (a one-shot preanim
// just completed). It wraps the raw advance() with two coordinated behaviours:
//
//	(1) Told-to-loop restart guard (the preanim-loop BUG fix). A finished one-shot
//	    (a preanim, finished latched on its last frame) that is now told to LOOP
//	    (playOnce=false) restarts as a clean loop instead of freezing on that held
//	    frame. This only ever triggers on a base COLLISION: the courtroom cleared
//	    PlayOnce but left Active on the SAME string (PreanimBase == TalkBase/
//	    IdleBase, reachable per courtroom/frametriggers.go:167-172), so syncAnim
//	    never reset() and finished still held — the resident preanim page would
//	    otherwise loop (advance :155 wraps) or stay frozen. On a NON-collided base
//	    the courtroom changes Active, syncAnim resets finished BEFORE this runs, so
//	    the guard is a no-op. INVARIANT it relies on: finished is set ONLY under
//	    playOnce (advance :133-135 and :150-153; see the cross-reference comment at
//	    each setter) — a genuine idle/talk layer never reaches finished, so it is
//	    never restarted here and keeps looping normally.
//
//	(2) preanimLoop (opt-in "loop preanimations", default OFF, non-canonical). When
//	    ON and a preanim is playing, phase 1 advances as a one-shot (playOnce=true)
//	    so it completes ONCE — firing OnPreanimDone exactly once (loopReported
//	    latches it) so the message lifecycle is byte-identical to loop-off — then
//	    phase 2 advances as a genuine LOOP (playOnce=false) so the SAME clip keeps
//	    wrapping (finished stays cleared, so NextAnimDue keeps scheduling the wraps
//	    and OnPreanimDone can never re-fire) for as long as the courtroom keeps the
//	    preanim active. With preanimLoop OFF this whole branch is skipped and the
//	    call is byte-identical to the original advance(page, dt, PlayOnce).
func (v *Viewport) advanceSpeaker(page *TexturePage, dt time.Duration, playOnce bool) (firePreanimDone bool) {
	a := &v.speakerAnim
	if !playOnce && a.finished {
		// (1) collision handoff: a finished one-shot is now a loop — restart clean.
		a.finished = false
		a.frame = 0
		a.elapsed = 0
	}
	if playOnce && v.preanimLoop {
		if a.loopReported {
			// (2) phase 2: the first completion already fired OnPreanimDone; keep the
			// same clip wrapping as a plain loop (finished stays cleared, so
			// NextAnimDue keeps scheduling the wraps and completion never re-fires).
			a.advance(page, dt, false)
			return false
		}
		// (2) phase 1: run as a one-shot until it completes, then report ONCE. On the
		// completing frame, immediately clear the finished latch (and wrap to frame 0)
		// so the layer is already in loop form for the next Update — leaving finished
		// latched even one frame would make NextAnimDue stop scheduling and stall the
		// loop. The frame jump to 0 IS the loop wrapping, which is exactly right.
		if a.advance(page, dt, true) {
			a.loopReported = true
			a.finished = false
			a.frame = 0
			a.elapsed = 0
			return true
		}
		return false
	}
	// Default (loop OFF): plain one-shot / loop advance, unchanged.
	return a.advance(page, dt, playOnce) && playOnce
}

// reportSpeakerFrame fires OnFrameShown with the speaker layer's currently-drawn
// frame in the SENDER's raw frame space (#17). The playback cursor (a.frame) is a
// KEPT-frame ordinal after decimation; FrameKeepIndex maps it back to the source
// index the sender authored networked frame effects against. Fires only when that
// source index changes since the last report for this base (a.shownSrc), so a
// static sprite or a paused frame costs nothing, and a nil callback short-circuits
// before any work. Called once per Update from the speaker-advance site.
func (v *Viewport) reportSpeakerFrame(page *TexturePage) {
	if v.OnFrameShown == nil || page == nil || len(page.Frames) == 0 {
		return
	}
	a := &v.speakerAnim
	src := assets.FrameKeepIndex(a.frame, page.SourceFrames, len(page.Frames))
	if src == a.shownSrc {
		return // same source frame still on screen — nothing new to fire
	}
	a.shownSrc = src
	v.OnFrameShown(src)
}

// pageDuration is a one-shot animation's total playback time (the sum of every
// frame's delay). Zero for a static / single-frame page.
func pageDuration(page *TexturePage) time.Duration {
	var total time.Duration
	for _, d := range page.Delays {
		total += d
	}
	return total
}

// SpriteFX is the optional colour wash applied to character layers (all OFF /
// neutral by default). It's a plain value struct with no pointers, so the App
// can rebuild and hand it to SetSpriteFX every frame with zero allocation. The
// look is render-side only (a SetColorMod/SetBlendMode bracket around the
// existing blit): nothing leaves the client and nobody else sees it.
type SpriteFX struct {
	Rainbow    bool // cycling-hue wash
	Solid      bool // fixed-colour wash (used only when !Rainbow)
	Glow       bool // additive blend (neon) instead of multiplying tint
	PairDesync bool // offset the pair layer's hue half a period
	PerCharHue bool // rainbow: offset each character's hue by a hash of its name
	Wobble     bool // gentle continuous position sway
	Spin       bool // slow continuous rotation
	ShoutPunch bool // #12: quick scale-pop of the whole stage when a shout appears
	Entrance   bool // #9: slide the speaker in when a new character takes the stage
	DoF        bool // #11: soft-focus + dim the background behind the sharp speaker
	Spotlight  bool // #121: dim the non-speaker layers (pair partner + desk) so the talker pops
	// #122 idle breathing (AsyncAO-only local life): a gentle continuous bob + breathing-scale
	// on every sprite so static art feels alive. IdleBreath is the master; Bob / Scale toggle
	// the two components (both default on). Suppressed by ReduceMotion like wobble/spin.
	IdleBreath  bool
	BreathBob   bool // the vertical-bob component
	BreathScale bool // the breathing scale-pulse component
	Reflection  bool // #123: a flipped, faded "glass floor" mirror of the sprites below the floor line

	Speed           int // rainbow hue speed slider [1,100] (higher = faster)
	Vividness       int // rainbow saturation slider [0,100] (higher = more neon)
	SpotlightLevel  int // spotlight dim intensity [0,100] (higher = darker non-speakers)
	BreathAmp       int // idle-breathing amplitude [1,100] (higher = more motion)
	BreathSpeed     int // idle-breathing speed [1,100] (higher = faster)
	ReflectStrength int // reflection opacity [0,100] (higher = more visible)

	SolidR, SolidG, SolidB uint8 // fixed tint colour (Solid)
}

// tinted reports whether any wash is active (so drawSprite skips all colour-mod
// work — and stays byte-identical to the original blit — when nothing's on).
func (f SpriteFX) tinted() bool { return f.Rainbow || f.Solid }

// SpriteLoadMode selects what a character layer draws while its NEW sprite is
// still streaming + decoding (an uncached emote/character). Cold-load only —
// once the sprite is in T1 every frame is byte-identical, whatever the mode.
// Values MUST match config.SpriteLoad* (the UI mirrors the pref straight in).
const (
	// SpriteLoadBlank draws nothing until the sprite lands — the original
	// behaviour, and the ~¼-second cold-sprite "flash" from the playtests.
	SpriteLoadBlank = 0
	// SpriteLoadHoldPrev keeps the layer's LAST drawn sprite on screen until the
	// new one lands (webAO-style), so the stage never blanks between speakers.
	SpriteLoadHoldPrev = 1
	// A third "wait" mode (client-AO: hold the whole message off-stage until its
	// sprite resolves) is NOT a renderer concern — it lives in the message
	// lifecycle and fights packed-room catch-up. Tracked in ROADMAP.
)

// Viewport renders a courtroom.Scene into a destination rect. Steady-state
// Render performs zero heap allocations: all rects and states are reused.
type Viewport struct {
	store *TextureStore

	speakerAnim animState
	pairAnim    animState
	shoutAnim   animState
	bgAnim      animState
	deskAnim    animState

	// OnPreanimDone forwards one-shot completion to the courtroom state
	// machine.
	OnPreanimDone func()

	// OnPreanimStart forwards a decoded, multi-frame preanim's REAL total
	// duration to the courtroom the first frame it plays, so the courtroom can
	// extend its fallback timeout to cover a preanim longer than the default
	// (else it's cut short — the "long preanims skip to the end" report). Fires
	// once per bound preanim (startReported); nil = not wired (no extension).
	OnPreanimStart func(time.Duration)

	// OnFrameShown forwards the SPEAKER layer's newly-displayed frame index —
	// mapped back through the decimation to the sender's raw frame space
	// (assets.FrameKeepIndex) — so the courtroom can fire networked frame-synced
	// SFX / realization / screenshake (#17: AO2-Client FRAME_* fields) as the
	// sprite reaches an authored trigger frame. Fires only when the drawn kept
	// frame actually changes (not every render), so a nil callback and a static
	// sprite both cost nothing. Wired to Courtroom.NotifyFrameShown by the App.
	OnFrameShown func(src int)

	// fx is the colour wash (App mirrors the user prefs here once per frame via
	// SetSpriteFX). rainbowPhase is the accumulated, cycle-bounded hue clock and
	// curCycle is the period derived from fx.Speed this frame (kept > 0 so the
	// modulo/divide can never panic). All three live on the Viewport so the
	// steady-state loop stays alloc-free.
	fx           SpriteFX
	rainbowPhase time.Duration
	curCycle     time.Duration
	// fxClock free-runs (accumulates dt; int64 ns won't overflow in any
	// session), so the motion effects (wobble, spin) each take it modulo their
	// own period and stay continuous without a shared wrap glitch.
	fxClock time.Duration

	// spriteLoadMode chooses what a character layer draws while its NEW sprite is
	// still streaming + decoding — the cold-load flash mitigation. The zero value is
	// SpriteLoadBlank, but the App ships hold-previous (webAO-style) and mirrors the
	// user pref here once per frame
	// (SetSpriteLoadMode), exactly like SetSpriteFX. It only ever affects the miss
	// path in drawSprite, so a cached scene is byte-identical whatever it's set to.
	spriteLoadMode int

	// clipSprites (ON by default via the pref) masks the character sprites to the
	// stage rect so a big pair / reposition OFFSET can't spill a sprite over the
	// chatbox or the log. Only the sprite draws are clipped (the bg/desk already
	// fill the stage), and only when no clip is already active, so screenshake and
	// the reflection's own clip are untouched. The App mirrors the pref here.
	clipSprites bool

	// holdMaxAge caps how long SpriteLoadHoldPrev may keep showing the previous
	// sprite (0 = forever, the default): past it the layer goes blank rather than
	// showing an ever-staler stand-in. Each char layer's animState.coldFor ticks
	// in Update while its base is unresolved. tintHeld (power-user diagnostics)
	// washes stand-in sprites amber so the mitigation is VISIBLE while tuning.
	holdMaxAge time.Duration
	tintHeld   bool

	// thumbSprites (opt-in pref, default OFF) lets the cold-load miss path show
	// the RIGHT character's persistent low-quality thumbnail (uploaded under a
	// thumb:// T1 key by the ui) while the full sprite streams. Independent of
	// spriteLoadMode and checked FIRST — the correct character at low quality
	// beats the previous character at full quality.
	thumbSprites bool

	// missingnoEnabled (pref-backed, default ON) draws the AO2 "placeholder"
	// (missingno) for a CONCLUSIVELY-MISSING char sprite — one whose whole
	// fallback chain 404'd (store.IsMissing). Probed FIRST in the miss branch and
	// gated on this bool: a confirmed-missing base was never uploaded so it has no
	// held://P/thumb/lastGood of its own, and hold-previous would otherwise keep a
	// DIFFERENT character on stage forever. Still-LOADING sprites (not in the
	// missing set) keep the full unchanged hold-previous/thumb chain — the two
	// states are disjoint. The App mirrors the pref here once per frame
	// (SetMissingno), like SetSpriteLoadMode.
	missingnoEnabled bool

	// preanimLoop (opt-in pref, default OFF) keeps a one-shot preanimation
	// WRAPPING for as long as the courtroom keeps it the active speaker layer,
	// instead of holding its last frame once. This is DELIBERATELY NON-CANONICAL:
	// AO2-Client plays preanims strictly play-once (animationlayer.cpp forces
	// setPlayOnce(true) for PreEmote), which is why it defaults OFF. OnPreanimDone
	// still fires EXACTLY ONCE at the first natural completion (loopReported
	// latches it), so the message lifecycle — text start, wait gates, phase
	// transitions — is byte-identical to loop-off. The App mirrors the pref here
	// once per frame (SetPreanimLoop), like the other viewport knobs.
	preanimLoop bool

	// crossfade (0 = off, the default hard swap) blends a sprite swap: the new
	// sprite alpha-ramps in over this duration while lastGood draws underneath.
	// The App mirrors the pref here (and zeroes it under Reduce motion).
	crossfade time.Duration

	// punchLeft counts down a #12 shout screen-punch; prevShoutOn edge-detects the
	// shout's appearance in Update so the pop fires once per shout. Viewer-local.
	punchLeft   time.Duration
	prevShoutOn bool

	// #9 entrance: entranceLeft counts down a speaker slide-in; prevSpeaker edge-detects a
	// NEW character taking the stage (name change) so the slide fires once per entrance.
	entranceLeft time.Duration
	prevSpeaker  string

	// scratch rects reused every frame (no allocs in the loop): taking the
	// address of a stack value for a cgo call would force a heap escape
	// per draw, so every Copy destination lives on the Viewport.
	dstRect  sdl.Rect
	fillRect sdl.Rect
	fxRect   sdl.Rect // #8 outline/shadow offset-blit destination (kept off dstRect)
	srcRect  sdl.Rect // torn-glitch band source (texture coords; kept off the dst scratches)
	reflRect sdl.Rect // #123 reflection blit destination
	reflClip sdl.Rect // #123 reflection clip rect (confine to the stage when not already clipped)
	maskClip sdl.Rect // viewport sprite mask: clip character sprites to the stage so an offset can't spill out

	// #10 post-processing overlays: cached, size-stable textures blended over the stage.
	postFX               PostFX
	vignetteTex          *sdl.Texture
	scanlineTex          *sdl.Texture
	scanlineW, scanlineH int32
	grainTex             [grainFrames]*sdl.Texture
	grainIdx             int
	postRect             sdl.Rect // post-FX blit destination scratch
	// #77 CRT preset: a cached aperture-grille (RGB phosphor stripe) mask blended
	// MOD over the stage, rebuilt only on a resize like the scanline texture.
	crtMaskTex         *sdl.Texture
	crtMaskW, crtMaskH int32

	// #124 particle weather: a bounded pool + one cached dot texture (snow/rain/sakura/embers).
	particles particleField

	// Sprite Style box live preview (RenderStylePreview): its own animState (so the
	// preview loops independently of the stage), a scratch layer + clip rects (a stack
	// value's address escaping into a cgo call would allocate per frame), and the
	// fxClock mark that turns the free-running clock into this path's dt.
	prevAnim      animState
	prevLayer     courtroom.SpriteLayer
	prevClockMark time.Duration
	prevVPClip    sdl.Rect
	prevClipSave  sdl.Rect
}

// previewMaxStep caps the style preview's animation step: the box may not have drawn
// for a while (closed, another screen), and fast-forwarding that gap through advance()
// would spin its frame loop for nothing — a fresh open just resumes the loop instead.
const previewMaxStep = 250 * time.Millisecond

// RenderStylePreview draws one character sprite styled by st into vp through the SAME
// drawSprite path the live stage uses — paint/restyle variants, outline, glitch,
// motion and all — so the Sprite Style box's preview can never drift from what a
// message actually renders. Render thread only; clipped to vp regardless of the
// clip-sprites pref (a wide sprite or a glitch jolt must not spill over the box).
func (v *Viewport) RenderStylePreview(ren *sdl.Renderer, base string, st courtroom.SpriteStyle, vp sdl.Rect) {
	dt := v.fxClock - v.prevClockMark
	v.prevClockMark = v.fxClock
	if dt < 0 || dt > previewMaxStep {
		dt = 0
	}
	v.syncAnim(&v.prevAnim, base)
	if page, ok := v.prevAnim.resolve(v.store); ok {
		v.prevAnim.advance(page, dt, false)
	}
	v.prevClipSave = ren.GetClipRect()
	v.prevVPClip = vp
	_ = ren.SetClipRect(&v.prevVPClip)
	v.prevLayer = courtroom.SpriteLayer{Visible: true, Active: base, Style: st}
	v.drawSprite(ren, &v.prevLayer, &v.prevAnim, vp, 0, 100 /* dimPct 100 = no spotlight dim */)
	if v.prevClipSave.W > 0 || v.prevClipSave.H > 0 {
		_ = ren.SetClipRect(&v.prevClipSave)
	} else {
		_ = ren.SetClipRect(nil)
	}
}

// Sprite outline / drop-shadow tuning (#8). The outline is drawn as 8 offset silhouette
// blits (cardinals + diagonals) around the sprite; the shadow is one offset down-right.
var (
	outlineDirs = [8][2]int32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	shadowDir   = [1][2]int32{{1, 1}} // down-right
)

const (
	outlineAlpha    = 235 // near-solid white outline
	shadowAlpha     = 110 // soft dark shadow
	outlineDivisor  = 170 // outline width = vp.H / divisor (scales with the stage), floored
	outlineWidthMin = 2
	shadowMultiple  = 3 // shadow offset = outline width * this
)

// NewViewport builds a viewport over the texture store.
func NewViewport(store *TextureStore) *Viewport {
	return &Viewport{store: store}
}

// SetSpriteFX mirrors the user's sprite colour-FX preferences onto the
// viewport. The App rebuilds the value struct and calls this once per frame
// before Update; it only feeds a SetColorMod/SetBlendMode bracket around the
// existing blit, so updating it costs nothing and never allocates.
func (v *Viewport) SetSpriteFX(fx SpriteFX) { v.fx = fx }

// AnimClock returns the free-running motion clock advanced by Update (the same clock the
// sprite Wobble/Spin read). The chatbox uses it to drive #M5 animated text, so text and
// sprite motion stay in step in every mode (live / replay / maker) — each path calls
// Update, so the clock tracks whatever scene is on stage.
func (v *Viewport) AnimClock() time.Duration { return v.fxClock }

// Update advances animation clocks against the active scene.
func (v *Viewport) Update(scene *courtroom.Scene, dt time.Duration) {
	// Derive this frame's hue period from the speed slider and advance the
	// rainbow clock unconditionally (cheap; the modulo keeps the phase bounded
	// so it never overflows however long the client runs). cycleForSpeed is
	// floored above zero, so the modulo below can never panic. The phase is
	// only *read* when a wash is enabled.
	v.curCycle = cycleForSpeed(v.fx.Speed)
	v.rainbowPhase = (v.rainbowPhase + dt) % v.curCycle
	v.fxClock += dt        // free-running clock for the motion effects (wobble/spin)
	v.particles.update(dt) // #124 advance the weather pool (no-op when off)

	// #12 shout punch: fire the pop once on the frame a shout appears (ShoutBase goes
	// non-empty), then count it down. Tracked unconditionally (cheap, no allocation); the
	// scale is only *applied* in Render when fx.ShoutPunch is on, so it's free when off.
	shoutOn := scene.ShoutBase != ""
	if shoutOn && !v.prevShoutOn {
		v.punchLeft = shoutPunchDuration
	}
	v.prevShoutOn = shoutOn
	if v.punchLeft -= dt; v.punchLeft < 0 {
		v.punchLeft = 0
	}

	// #9 entrance: a NEW character on stage (the speaker's name changed) arms a slide-in.
	// Tracked unconditionally; only *applied* in Render when fx.Entrance is on, so it's free
	// when off. Same-speaker consecutive messages (or an empty speaker) never re-trigger.
	if sp := scene.Speaker.Name; sp != "" && sp != v.prevSpeaker {
		if v.prevSpeaker != "" { // don't slide the very first speaker of a fresh session
			v.entranceLeft = entranceDuration
		}
		v.prevSpeaker = sp
	}
	if v.entranceLeft -= dt; v.entranceLeft < 0 {
		v.entranceLeft = 0
	}

	shoutBase := effectiveShoutBase(scene, v.store)
	v.syncAnimSticky(&v.bgAnim, scene.BackgroundBase)
	v.syncAnimSticky(&v.deskAnim, scene.DeskBase)
	v.syncAnim(&v.shoutAnim, shoutBase)
	v.syncAnim(&v.speakerAnim, scene.Speaker.Active)
	v.syncAnim(&v.pairAnim, scene.Pair.Active)
	// Hold-previous max-age clock: how long each char layer has been cold
	// (resolve is generation-cached — steady state is pointer math).
	v.tickCold(&v.speakerAnim, dt)
	v.tickCold(&v.pairAnim, dt)

	if page, ok := v.bgAnim.resolve(v.store); ok {
		v.bgAnim.advance(page, dt, false)
	}
	if page, ok := v.deskAnim.resolve(v.store); ok {
		v.deskAnim.advance(page, dt, false)
	}
	if shoutBase != "" {
		if page, ok := v.shoutAnim.resolve(v.store); ok {
			v.shoutAnim.advance(page, dt, true)
		}
	}
	if scene.Speaker.Visible {
		if page, ok := v.speakerAnim.resolve(v.store); ok {
			// First frame of a decoded, multi-frame preanim: report its real total
			// duration so the courtroom's fallback timeout can't cut a long one
			// short. Single-frame "preanims" finish instantly below — no report.
			if scene.Speaker.PlayOnce && !v.speakerAnim.startReported && v.OnPreanimStart != nil && len(page.Frames) > 1 {
				v.speakerAnim.startReported = true
				v.OnPreanimStart(pageDuration(page))
			}
			if v.advanceSpeaker(page, dt, scene.Speaker.PlayOnce) {
				if v.OnPreanimDone != nil {
					v.OnPreanimDone()
				}
			}
			// #17 networked frame effects: report the drawn frame's SOURCE index
			// (mapped back through decimation) whenever it moves, so the courtroom
			// can fire an authored trigger. After advance so the report reflects
			// this step's landing frame; guarded on a wired callback so a plain
			// render allocates nothing.
			v.reportSpeakerFrame(page)
		}
	}
	if scene.PairActive {
		if page, ok := v.pairAnim.resolve(v.store); ok {
			v.pairAnim.advance(page, dt, false)
		}
	}
}

// NextAnimDue reports the stage's next scheduled animation deadline: 0 while a
// continuous ramp runs (speaker-swap crossfade, shout punch, entrance slide),
// else the time until the earliest frame flip of any LIVE multi-frame layer
// (bg / desk / shout / speaker / pair). ok=false means the stage is visually
// static — nothing on it is scheduled to change. The frame pacer uses this to
// redraw exactly when the content needs it (a 100 ms-per-frame idle loop gets
// ~10 fps redraws; a static stage gets none), fixing both playtest reports:
// choppy idle animations AND wasted full-rate redraws. Reads only the states
// Update just advanced (cached page pointers — no store queries, no allocs);
// render thread only, call after Update with the same scene.
func (v *Viewport) NextAnimDue(scene *courtroom.Scene) (time.Duration, bool) {
	// Continuous ramps: sub-frame-smooth motion → full rate while they run.
	// Each is gated on its enabling knob, so an armed-but-invisible countdown
	// (tracked unconditionally in Update) never holds the rate up.
	if v.fx.ShoutPunch && v.punchLeft > 0 {
		return 0, true
	}
	if v.fx.Entrance && v.entranceLeft > 0 {
		return 0, true
	}
	if v.crossfade > 0 && (fadeRunning(&v.speakerAnim) || fadeRunning(&v.pairAnim)) {
		return 0, true
	}
	due := time.Duration(-1)
	consider := func(a *animState, visible bool) {
		if !visible || a.finished || a.page == nil || len(a.page.Frames) <= 1 {
			return
		}
		delay := a.page.Delays[a.frame]
		if delay <= 0 {
			return // advance() treats a non-positive delay as frozen — not animating
		}
		d := delay - a.elapsed
		if d < 0 {
			d = 0
		}
		if due < 0 || d < due {
			due = d
		}
	}
	consider(&v.bgAnim, true)
	consider(&v.deskAnim, true)
	consider(&v.shoutAnim, v.shoutAnim.base != "")
	consider(&v.speakerAnim, scene.Speaker.Visible)
	consider(&v.pairAnim, scene.PairActive)
	if due < 0 {
		return 0, false
	}
	// Schedule floor: decoders honor any positive authored delay verbatim (only
	// <=0 is replaced), so a wild asset authored at 1-10 ms/frame would schedule
	// redraws faster than a frame costs — with the ∞ default cap the pacing
	// sleep then never fires and the loop free-runs (the idle-CPU-burn class).
	// Flooring the SCHEDULE (not the playback: advance() folds every elapsed
	// frame per step, so speed is unchanged — the viewer sees every Nth frame,
	// exactly what a 60 Hz panel shows anyway) bounds any asset to ~60 redraws/s.
	// Normal ≥60 ms assets pace byte-identically; a mid-cycle wake's remainder
	// lands at most one floor late, sub-frame and self-correcting.
	if due < minAnimFrameDelay {
		due = minAnimFrameDelay
	}
	return due, true
}

// minAnimFrameDelay floors NextAnimDue's scheduled deadline — the fastest
// cadence an animated ASSET may demand of the render loop (~60 fps, matching
// cmd/asyncao's frameCap fallback). Transitions (shout punch / entrance /
// crossfade) keep their unfloored full-rate path above.
const minAnimFrameDelay = 16667 * time.Microsecond

// fadeRunning reports a speaker-swap crossfade that is actually ADVANCING:
// fadeLeft only ticks down while the incoming sprite is resident (tickCold —
// a cold load deliberately doesn't consume the blend), so a fade armed toward
// a permanently-404 sprite is frozen, not running. Gating the full-rate ramp
// on this (state, not the armed countdown) stops that frozen fade from
// holding uncapped redraws forever — the blend still plays, at full rate,
// from the frame the sprite first lands (coldFor resets on residency).
func fadeRunning(a *animState) bool {
	return a.fadeLeft > 0 && a.coldFor == 0
}

// AmbientAnimating reports whether the DRAWN stage animates on the free-running
// FX clock this frame: the viewer's local always-on washes (rainbow / wobble /
// spin / idle-breathing), a transmitted per-sprite style with motion (hue-cycle
// / wobble / spin / glitch / motion / path), or live weather particles. These
// are pure per-frame math with no scheduled deadline — NextAnimDue can't see
// them — so the courtroom stage draw site feeds this into the NoteAnimating
// census instead. State-gated by construction: it is consulted only where the
// stage actually draws, and reports true only while a sprite layer (or weather)
// is genuinely on screen — replacing wantsFullRate's old pref-knob checks,
// which held full rate forever on every screen, lobby and Settings included
// (the "knob not state" anti-pattern its own doc comment condemns).
func (v *Viewport) AmbientAnimating(scene *courtroom.Scene) bool {
	if scene == nil {
		return false
	}
	if v.particles.weather != WeatherNone && v.particles.n > 0 {
		return true // live weather rides the clock across the whole stage
	}
	breath := v.fx.IdleBreath && (v.fx.BreathBob || v.fx.BreathScale)
	layerAnimates := func(layer *courtroom.SpriteLayer) bool {
		if breath {
			return true // breathing applies to every drawn sprite, styled or not
		}
		if st := layer.Style; st.Active() {
			// A transmitted style wins over the local wash (drawSprite's own
			// precedence); path motion (#34) animates on the clock too.
			return st.HueCycle || st.Wobble || st.Spin || st.Glitch ||
				st.Motion != 0 || st.PathLen >= 2
		}
		return v.fx.Rainbow || v.fx.Wobble || v.fx.Spin
	}
	if scene.Speaker.Visible && layerAnimates(&scene.Speaker) {
		return true
	}
	if scene.PairActive && layerAnimates(&scene.Pair) {
		return true
	}
	return false
}

func (v *Viewport) syncAnim(a *animState, base string) {
	if a.base != base {
		a.reset(base)
		// Speaker-swap crossfade: arm on the swap when the knob is on and there
		// is a previous sprite to blend from. lastGood is only ever set by
		// drawSprite, so shout-bubble layers (drawFill) can never arm a fade.
		if v.crossfade > 0 && a.lastGood != "" && a.lastGood != base {
			a.fadeLeft = v.crossfade
		} else {
			a.fadeLeft = 0
		}
	}
}

// syncAnimSticky rebinds scenery layers (background, desk) only once the
// incoming base is resident: a position flip to a still-loading background
// must keep the last good scenery on screen instead of blanking the
// viewport to the clear color. An empty base still clears immediately, and
// the courtroom's HIGH-priority prefetch makes the swap a frame after the
// texture lands. Contains is a plain map probe — no I/O, no allocation.
func (v *Viewport) syncAnimSticky(a *animState, base string) {
	if a.base == base {
		return
	}
	if base == "" || v.store.Contains(base) {
		a.reset(base)
	}
}

// RebindScenery force-binds the background/desk layers to a new scene's bases,
// BYPASSING syncAnimSticky's residency gate. The sticky gate exists to hold the
// last good scenery through a position flip WITHIN one session (the new base is
// still streaming) — but that same "keep the old base until the new one is
// resident" behaviour leaks across a TAB SWITCH: the shared viewport would keep
// painting the previous server's background under the newly-activated tab until
// its own bg happened to load (the reported "both tabs show the same background"
// bug). buildRoom calls this on a room rebuild so the swap shows the CORRECT new
// scenery at once — a brief blank of the right base (the held:// bridge / the
// HIGH-priority prefetch fill it a frame later) beats the WRONG base persisting.
// Character layers already reset unconditionally (syncAnim, not sticky), which is
// why speakers restored correctly while scenery did not. Cheap, allocation-free
// (reset only precomputes the thumb/held keys); a no-op when the bases already
// match, so re-entering the same room never thrashes.
func (v *Viewport) RebindScenery(bgBase, deskBase string) {
	if v.bgAnim.base != bgBase {
		v.bgAnim.reset(bgBase)
	}
	if v.deskAnim.base != deskBase {
		v.deskAnim.reset(deskBase)
	}
}

// Render draws the scene layers in AO order: background → characters (pair
// z-order) → desk → shout bubble. Chat box and text render UI-side.
// Screenshake jitters the whole stage rect; the realization flash paints
// over everything (do_screenshake / do_flash parity, zero allocations).
func (v *Viewport) Render(ren *sdl.Renderer, scene *courtroom.Scene, vp sdl.Rect) {
	stage := vp // the unmodified stage frame, for the #10 post-FX overlays (not the punched art)
	// #12 shout punch: inflate the stage rect around its centre by a decaying factor, so
	// every blit below scales up together (a zoom pop that settles). Off → vp untouched →
	// byte-identical to before. Applied before the shake so the two compose.
	if v.fx.ShoutPunch && v.punchLeft > 0 {
		if s := punchScale(v.punchLeft); s > 0 {
			dw := int32(float64(vp.W) * s)
			dh := int32(float64(vp.H) * s)
			vp.X -= dw / 2
			vp.Y -= dh / 2
			vp.W += dw
			vp.H += dh
		}
	}
	if scene.ShakeLeft > 0 {
		// Decaying sinusoid: amplitude follows the remaining time, so the
		// shake settles instead of cutting off.
		strength := float64(scene.ShakeLeft) / float64(courtroom.ScreenshakeDuration)
		amp := float64(vp.W) / shakeAmplitudeDivisor * strength
		phase := 2 * math.Pi * float64(scene.ShakeLeft) / float64(shakePeriod)
		vp.X += int32(amp * math.Sin(phase))
		vp.Y += int32(amp / 2 * math.Cos(phase*1.3))
	}

	if v.fx.DoF {
		v.drawBackgroundDoF(ren, scene.BackgroundBase, &v.bgAnim, vp) // #11 soft-focus + dim
	} else {
		v.drawFill(ren, scene.BackgroundBase, &v.bgAnim, vp, 100) // bg full-bright (spotlight dims pair+desk, not bg)
	}

	// Pair hue offset for the "desync" effect: half a period so the two
	// characters show opposite hues. Zero unless rainbow + desync are both on,
	// and the speaker always passes 0 — so with the effect off the speaker blit
	// is byte-identical to before.
	var pairShift time.Duration
	if v.fx.Rainbow && v.fx.PairDesync {
		pairShift = v.curCycle / 2
	}

	// #121 speaker spotlight: the non-speaker layers (the pair partner + the desk overlay)
	// draw dimmed toward shadow so the talking character pops; the speaker and the shout
	// bubble stay at full brightness. noDim (100%) leaves a layer byte-identical, so with the
	// effect off every draw below is unchanged. The background is left alone here (the DoF
	// effect owns the background dim), so the two compose without double-dimming.
	const noDim = 100
	spotPct := noDim
	if v.fx.Spotlight {
		spotPct = spotlightBrightness(v.fx.SpotlightLevel)
	}
	// #9 entrance: a new speaker slides in from the left, easing to position (the slide rect
	// is the speaker's only; the pair stays put). Off / settled → spkVP == vp → unchanged.
	spkVP := vp
	if v.fx.Entrance && v.entranceLeft > 0 {
		spkVP.X += entranceSlide(v.entranceLeft, vp.W)
	}
	// Viewport sprite mask (default ON): clip the character sprites to the ORIGINAL
	// stage rect so a big pair / reposition OFFSET can't spill a sprite over the
	// chatbox or the log. Only the sprites are clipped (the bg/desk already fill the
	// stage), and only when nothing else already owns a clip — so we restore to
	// exactly the prior state and never fight the reflection's own clip (which runs
	// just after this). Off → no SetClipRect at all → byte-identical to before.
	spriteClip := v.clipSprites && !ren.IsClipEnabled()
	if spriteClip {
		v.maskClip = stage
		_ = ren.SetClipRect(&v.maskClip)
	}
	if scene.PairActive && !scene.SpeakerInFront {
		// Speaker behind: draw speaker first, pair over it.
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, spkVP, 0, noDim)
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp, pairShift, spotPct)
	} else if scene.PairActive {
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp, pairShift, spotPct)
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, spkVP, 0, noDim)
	} else {
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, spkVP, 0, noDim)
	}
	if spriteClip {
		_ = ren.SetClipRect(nil)
	}

	// #123 glass-floor reflection: a flipped, faded mirror of the sprites below the floor line,
	// drawn AFTER the sprites and BEFORE the desk so the desk occludes it naturally. Off →
	// skipped entirely (byte-identical).
	if v.fx.Reflection {
		v.drawReflections(ren, scene, vp, spkVP)
	}
	if scene.ShowDesk {
		v.drawFill(ren, scene.DeskBase, &v.deskAnim, vp, spotPct)
	}
	if shoutBase := effectiveShoutBase(scene, v.store); shoutBase != "" {
		v.drawFill(ren, shoutBase, &v.shoutAnim, vp, noDim)
	}

	if scene.FlashLeft > 0 {
		// Realization flash: white overlay fading with the countdown.
		frac := float64(scene.FlashLeft) / float64(courtroom.RealizationFlashDuration)
		if frac > 1 {
			frac = 1
		}
		v.fillRect = vp
		_ = ren.SetDrawColor(255, 255, 255, uint8(255*frac))
		_ = ren.FillRect(&v.fillRect)
	}

	v.particles.draw(ren, stage) // #124 ambient weather over the stage (free when off), under the post-FX
	v.applyPostFX(ren, stage)    // #10 retro overlays over the whole stage frame (free when off)
}

// effectiveShoutBase picks which shout bubble to draw: the character's own
// bubble when it's resident, else the default (misc/default) bubble — mirroring
// AO2-Client, since most characters ship NO custom interjection art, so the
// char-specific base 404s and only the default ever loads. Both are prefetched
// in Courtroom.begin. The Contains probe only runs during a shout (ShoutBase
// set), so it costs nothing the rest of the time.
func effectiveShoutBase(scene *courtroom.Scene, store *TextureStore) string {
	if scene.ShoutBase == "" {
		return ""
	}
	if scene.ShoutFallbackBase != "" && !store.Contains(scene.ShoutBase) {
		return scene.ShoutFallbackBase
	}
	return scene.ShoutBase
}

// drawFill stretches an asset across the whole viewport (backgrounds, desks,
// shout bubbles — AO renders all of them viewport-sized).
// drawFill blits a full-viewport layer (background / desk / shout bubble). dimPct < 100
// darkens it via a grey ColorMod (the #121 spotlight dims the desk) — set→copy→RESTORE on
// the shared T1 page so nothing bleeds onto the next user; dimPct == 100 is byte-identical.
func (v *Viewport) drawFill(ren *sdl.Renderer, base string, anim *animState, vp sdl.Rect, dimPct int) {
	if base == "" {
		return
	}
	frame := 0
	page, ok := anim.resolve(v.store)
	if ok && len(page.Frames) > 0 {
		frame = clampFrame(anim.frame, len(page.Frames))
	} else if page, ok = v.resolveHeld(anim); !ok {
		// No page and no held bridge frame: nothing to draw (the pre-filled
		// black stage shows — the state the bridge exists to avoid).
		return
	}
	tex := page.Frames[frame]
	v.fillRect = vp
	if dimPct < 100 {
		d := scaleChannel(255, dimPct)
		_ = tex.SetColorMod(d, d, d)
	}
	_ = ren.Copy(tex, nil, &v.fillRect)
	if dimPct < 100 {
		_ = tex.SetColorMod(255, 255, 255) // restore the shared page
	}
}

// resolveHeld probes the held-frame bridge for a scenery layer whose CURRENT
// base is missing (evicted mid-scene by a cap-sized incoming upload — the
// black-flash class). Store.Get on a pinned key is a plain map probe, and
// heldKey is precomputed in reset, so this miss path stays allocation-free.
// The bridge page is a single stolen frame; callers draw frame 0.
func (v *Viewport) resolveHeld(a *animState) (*TexturePage, bool) {
	if a.heldKey == "" {
		return nil, false
	}
	page, ok := v.store.Get(a.heldKey)
	if !ok || len(page.Frames) == 0 || page.Frames[0] == nil {
		return nil, false
	}
	return page, true
}

// Reflection tuning (#123): the mirror line and how strong the default reflection is.
const (
	reflectFloorPct   = 85  // mirror axis at vp.H * pct/100 down (≈ where feet land)
	reflectMinAlpha   = 10  // floor so "on" always shows something
	reflectMaxAlpha   = 200 // ceiling so a reflection never reads as a second solid sprite
	reflectDefaultPct = 30  // default opacity when unset
)

// drawReflections mirrors the speaker (+ pair) below the floor line as a flipped, faded glass
// reflection, confined to the stage. Called from Render between the sprites and the desk.
func (v *Viewport) drawReflections(ren *sdl.Renderer, scene *courtroom.Scene, vp, spkVP sdl.Rect) {
	floorY := vp.Y + vp.H*reflectFloorPct/100
	alpha := reflectAlpha(v.fx.ReflectStrength)
	// Confine the reflection to the stage so it can't bleed past the viewport bottom — but
	// only when a clip isn't already active (the zoom path clips to the stage itself), so we
	// never stomp the camera-zoom clip.
	needClip := !ren.IsClipEnabled()
	if needClip {
		v.reflClip = vp
		_ = ren.SetClipRect(&v.reflClip)
	}
	v.drawReflection(ren, &scene.Speaker, &v.speakerAnim, spkVP, floorY, alpha)
	if scene.PairActive {
		v.drawReflection(ren, &scene.Pair, &v.pairAnim, vp, floorY, alpha)
	}
	if needClip {
		_ = ren.SetClipRect(nil)
	}
}

// drawReflection blits one sprite vertically flipped with its mirror axis at floorY, at the
// given alpha (restored after — shared T1 page). The clip in drawReflections keeps it inside
// the stage; the desk drawn after occludes its lower part. 0-alloc (reused scratch rect).
func (v *Viewport) drawReflection(ren *sdl.Renderer, layer *courtroom.SpriteLayer, anim *animState, vp sdl.Rect, floorY int32, alpha uint8) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	page, ok := anim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		return
	}
	frame := clampFrame(anim.frame, len(page.Frames))
	scaledW := vp.H * page.W / page.H
	v.reflRect.W = scaledW
	v.reflRect.H = vp.H
	v.reflRect.X = vp.X + (vp.W-scaledW)/2 + vp.W*int32(layer.OffsetX)/offsetPercentDivisor
	v.reflRect.Y = floorY // flipped: the texture's bottom (feet) lands at the mirror axis
	var flip sdl.RendererFlip = sdl.FLIP_VERTICAL
	if layer.Flip {
		flip |= sdl.FLIP_HORIZONTAL
	}
	tex := page.Frames[frame]
	_ = tex.SetAlphaMod(alpha)
	_ = ren.CopyEx(tex, nil, &v.reflRect, 0, nil, flip)
	_ = tex.SetAlphaMod(255) // restore the shared page
}

// reflectAlpha maps the ReflectStrength slider [0,100] to an 0..255 opacity, clamped to a
// visible floor and a ceiling so a reflection never reads as a second solid sprite.
func reflectAlpha(strength int) uint8 {
	if strength <= 0 {
		strength = reflectDefaultPct
	}
	a := strength * 255 / 100
	if a < reflectMinAlpha {
		a = reflectMinAlpha
	}
	if a > reflectMaxAlpha {
		a = reflectMaxAlpha
	}
	return uint8(a)
}

// Depth-of-field tuning (#11). A real Gaussian needs per-texture linear filtering that
// go-sdl2 doesn't expose, so the background is soft-focused by smearing it: the sharp bg
// plus eight low-alpha offset ghosts (the outlineDirs ring), then a dim overlay to push it
// behind the sharp speaker. All blits + a scratch rect → 0 alloc.
const (
	dofBlurDivisor = 90  // ghost offset = vp.H / divisor (floored)
	dofBlurMin     = 3   //
	dofAlphaCard   = 120 // cardinal ghost strength
	dofAlphaDiag   = 80  // diagonal ghost strength
	dofDimAlpha    = 95  // darkening over the blurred bg
)

// drawBackgroundDoF draws the background soft-focused + dimmed (#11). The bg store page's
// alpha is set per ghost and RESTORED to opaque after (the page is shared T1). 0-alloc.
func (v *Viewport) drawBackgroundDoF(ren *sdl.Renderer, base string, anim *animState, vp sdl.Rect) {
	if base == "" {
		return
	}
	frame := 0
	page, ok := anim.resolve(v.store)
	if ok && len(page.Frames) > 0 {
		frame = clampFrame(anim.frame, len(page.Frames))
	} else if page, ok = v.resolveHeld(anim); !ok {
		return // no page, no held bridge frame — see drawFill
	}
	tex := page.Frames[frame]
	r := vp.H / dofBlurDivisor
	if r < dofBlurMin {
		r = dofBlurMin
	}
	v.fillRect = vp // the sharp base
	_ = ren.Copy(tex, nil, &v.fillRect)
	for i := range outlineDirs { // eight offset ghosts → a smear blur
		a := uint8(dofAlphaCard)
		if i >= 4 { // outlineDirs[4:] are the diagonals
			a = dofAlphaDiag
		}
		_ = tex.SetAlphaMod(a)
		v.fillRect = vp
		v.fillRect.X += outlineDirs[i][0] * r
		v.fillRect.Y += outlineDirs[i][1] * r
		_ = ren.Copy(tex, nil, &v.fillRect)
	}
	_ = tex.SetAlphaMod(255)
	v.fillRect = vp // dim overlay, pushing the bg behind the sharp speaker
	_ = ren.SetDrawColor(0, 0, 0, dofDimAlpha)
	_ = ren.FillRect(&v.fillRect)
}

// SetSpriteLoadMode picks what a character layer draws while its new sprite is
// still streaming + decoding (the App ships SpriteLoadHoldPrev; SpriteLoadBlank is
// the original byte-identical gap). The App mirrors the user pref here once per
// frame, like SetSpriteFX.
func (v *Viewport) SetSpriteLoadMode(mode int) { v.spriteLoadMode = mode }

// SetClipSprites toggles the viewport sprite mask (ON by default): confine the
// character sprites to the stage so an offset can't spill them out. The App
// mirrors the user pref here once per frame.
func (v *Viewport) SetClipSprites(on bool) { v.clipSprites = on }

// SetHoldMaxAge caps how long hold-previous may bridge a cold sprite (0 = forever).
func (v *Viewport) SetHoldMaxAge(d time.Duration) { v.holdMaxAge = d }

// SetHoldDebugTint washes stand-in (held) sprites amber — a power-user diagnostic
// so the cold-load mitigation is visible while tuning it.
func (v *Viewport) SetHoldDebugTint(on bool) { v.tintHeld = on }

// SetThumbSprites toggles the low-q thumbnail stand-in on the cold-load miss
// path (opt-in pref, default OFF). The App mirrors the pref here per frame.
func (v *Viewport) SetThumbSprites(on bool) { v.thumbSprites = on }

// SetMissingno toggles the AO2 "placeholder" (missingno) for a conclusively-
// missing char sprite (pref ShowMissingPlaceholder, default ON). The App mirrors
// the pref here once per frame, like SetSpriteLoadMode. A plain bool assign — no
// per-frame pref read on the hot path.
func (v *Viewport) SetMissingno(on bool) { v.missingnoEnabled = on }

// SetCrossfade sets the speaker-swap blend duration (0 = off, the default hard
// swap). The App mirrors the pref here per frame and zeroes it under Reduce
// motion (a fade is motion).
func (v *Viewport) SetCrossfade(d time.Duration) { v.crossfade = d }

// SetPreanimLoop toggles the opt-in "loop preanimations" behaviour (default OFF,
// deliberately non-canonical — AO2 plays preanims once). ON keeps a one-shot
// preanim wrapping while it stays the active layer; OnPreanimDone still fires
// exactly once so the message lifecycle is unchanged. The App mirrors the pref
// here once per frame.
func (v *Viewport) SetPreanimLoop(on bool) { v.preanimLoop = on }

// ThumbKeyPrefix namespaces thumbnail uploads in T1 (like theme:// — a scheme
// prefix can never collide with an asset URL base). The ui uploads loaded
// thumbs under ThumbKeyPrefix+base; animState precomputes the same key.
const ThumbKeyPrefix = "thumb://"

// tickCold advances a char layer's cold-time (base set but not resolved) and
// resets it on residency — the hold-previous max-age clock. It also ticks a
// running crossfade, but ONLY while resident: a cold load never consumes the
// fade, so the blend plays from the frame the new sprite first draws. resolve
// is generation-cached, so the steady-state cost is pointer math.
func (v *Viewport) tickCold(a *animState, dt time.Duration) {
	if a.base == "" {
		a.coldFor = 0
		return
	}
	if _, ok := a.resolve(v.store); ok {
		a.coldFor = 0
		if a.fadeLeft > 0 {
			a.fadeLeft -= dt
		}
		return
	}
	a.coldFor += dt
}

// drawHeldSprite blits a layer's previously-resolved sprite as a plain stand-in
// during the cold-load gap (SpriteLoadHoldPrev). Deliberately minimal — no style,
// variant, wash, spotlight or breathing — it's a transient bridge, sized to the
// HELD page's own aspect and bottom-centered with the incoming layer's offset +
// flip so it sits where the new character will. Frame 0 is fine for a sub-second
// hold. Alloc-free (reuses v.dstRect) and only ever on the miss path, so the
// cached steady state is untouched.
func (v *Viewport) drawHeldSprite(ren *sdl.Renderer, layer *courtroom.SpriteLayer, page *TexturePage, vp sdl.Rect) {
	if page.H == 0 {
		return
	}
	scaledW := vp.H * page.W / page.H
	v.dstRect.W = scaledW
	v.dstRect.H = vp.H
	v.dstRect.X = vp.X + (vp.W-scaledW)/2 + vp.W*int32(layer.OffsetX)/offsetPercentDivisor
	v.dstRect.Y = vp.Y + vp.H*int32(layer.OffsetY)/offsetPercentDivisor
	flip := sdl.FLIP_NONE
	if layer.Flip {
		flip = sdl.FLIP_HORIZONTAL
	}
	tex := page.Frames[0]
	if v.tintHeld {
		// Diagnostic amber wash so a stand-in is visibly a stand-in. Restored to
		// neutral right after — the shared T1 page rule (see the drawSprite
		// INVARIANT; this is the one other MOD-bracket, same restore discipline).
		_ = tex.SetColorMod(heldTintR, heldTintG, heldTintB)
	}
	_ = ren.CopyEx(tex, nil, &v.dstRect, 0, nil, flip)
	if v.tintHeld {
		_ = tex.SetColorMod(255, 255, 255)
	}
}

// heldTint* is the debug wash for stand-in sprites (a warm amber: R full, G/B
// cut — obvious on any art without hiding it).
const (
	heldTintR = 255
	heldTintG = 190
	heldTintB = 120
)

// drawSprite draws a character layer: scaled to viewport height preserving
// aspect, bottom-centered, shifted by percent offsets, optionally mirrored.
// hueShift offsets this layer's rainbow phase (used to desync the pair).
func (v *Viewport) drawSprite(ren *sdl.Renderer, layer *courtroom.SpriteLayer, anim *animState, vp sdl.Rect, hueShift time.Duration, dimPct int) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	page, ok := anim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		// missingno FIRST (AO2 fidelity): a CONCLUSIVELY-MISSING base (its whole
		// fallback chain 404'd — store.IsMissing) draws the shared placeholder
		// instead of holding a DIFFERENT (stale) character. Deliberately ahead of
		// the held/thumb/hold-previous chain below: a confirmed-missing base
		// TYPICALLY was never uploaded, so it has no held://P/thumb page of its own,
		// and lastGood SURVIVES base swaps — so hold-previous would otherwise pin
		// the previous character on stage forever (holdprev_test.go:75-77). The one
		// case where a held frame DOES exist: a base displayed → LRU-evicted (the
		// held-frame steal parks its last frame) → re-demanded → NOW 404s (asset
		// pulled mid-session). Probing missingno first means the glitch supersedes
		// that stale held frame — intentional (AO2 shows its placeholder regardless
		// of the prior frame). This does not disturb the scenery bridge: a base that
		// was never marked missing still takes the held branch unchanged
		// (heldscenery_test.go stays green). The probe is a plain const-key map read
		// (MissingKey is const, anim.base already in hand) + drawHeldSprite (reuses
		// v.dstRect) — 0-alloc, mirroring resolveHeld/thumb. Only STILL-LOADING
		// sprites (not in the missing set) keep the full chain below; still-loading
		// and confirmed-missing are disjoint. Structurally unreachable on the found
		// path (resolved sprites exit via the ok-path).
		if v.missingnoEnabled && v.store.IsMissing(anim.base) {
			if mp, ok2 := v.store.Get(MissingKey); ok2 && len(mp.Frames) > 0 {
				v.drawHeldSprite(ren, layer, mp, vp)
				return
			}
		}
		// Held-frame bridge FIRST: if THIS sprite's page was evicted mid-display
		// (a cap-sized incoming upload forced it out — the last black-flash hole),
		// the store parked its first frame under held://base. That is the exact
		// same character at FULL quality, so it beats both the low-res thumbnail
		// and holding the PREVIOUS (different) character. Zero decode, zero copy;
		// releases the instant the real page re-uploads (store.releaseHeld).
		if held, ok2 := v.resolveHeld(anim); ok2 {
			v.drawHeldSprite(ren, layer, held, vp)
			return
		}
		// Cold-load gap: the incoming sprite hasn't finished streaming + decoding.
		// The opt-in THUMBNAIL wins next — the right character at low quality
		// (uploaded by the ui under the precomputed thumb:// key) beats the
		// previous character at full quality.
		if v.thumbSprites && anim.thumbKey != "" {
			if tp, ok2 := v.store.Get(anim.thumbKey); ok2 && len(tp.Frames) > 0 {
				v.drawHeldSprite(ren, layer, tp, vp)
				return
			}
		}
		// Else SpriteLoadHoldPrev keeps the layer's LAST drawn sprite on screen
		// until the new one lands (webAO-style) instead of flashing empty. We
		// resolve the HELD base through the store (never a stashed page pointer —
		// the LRU owns lifetime, so an eviction just falls back to blank +
		// self-heals). This lives ONLY on the draw path: resolve()/Update never see
		// the held page, so the preanim lifecycle and packed-room catch-up pacing
		// are untouched. SpriteLoadBlank → the original byte-identical early return.
		if v.spriteLoadMode == SpriteLoadHoldPrev && anim.lastGood != "" && anim.lastGood != anim.base &&
			(v.holdMaxAge <= 0 || anim.coldFor <= v.holdMaxAge) { // max-age knob: 0 = bridge forever
			if held, ok2 := v.store.Get(anim.lastGood); ok2 && len(held.Frames) > 0 {
				v.drawHeldSprite(ren, layer, held, vp)
			}
		}
		return
	}
	// Remember the sprite we're about to draw so a later cold swap can hold it.
	// (A plain assign is alloc-free; guard it so steady state doesn't even copy the
	// string header when nothing changed.) During a crossfade the update WAITS —
	// lastGood must keep naming the OLD sprite while it draws under the ramp.
	if anim.fadeLeft <= 0 && anim.lastGood != anim.base {
		anim.lastGood = anim.base
	}
	frame := clampFrame(anim.frame, len(page.Frames))

	scaledW := vp.H * page.W / page.H
	v.dstRect.W = scaledW
	v.dstRect.H = vp.H
	v.dstRect.X = vp.X + (vp.W-scaledW)/2 + vp.W*int32(layer.OffsetX)/offsetPercentDivisor
	v.dstRect.Y = vp.Y + vp.H*int32(layer.OffsetY)/offsetPercentDivisor

	flip := sdl.FLIP_NONE
	if layer.Flip {
		flip = sdl.FLIP_HORIZONTAL
	}
	tex := page.Frames[frame]
	// Hue paint (Tint+Grayscale, the v1.53.5 composition) draws from a dedicated
	// luma-preserving colorize variant instead of multiplying the tint over the
	// grayscale variant — a multiply can only remove light, so the old composition
	// darkened every saturated hue (playtest: turning hue paint on still looked
	// like the plain dark recolour). The tint colormod is SKIPPED for this layer
	// (huePainted below) — the colour is baked into the variant's pixels.
	// Conditions mirror what the old path composed: HueCycle keeps the rainbow-
	// over-grayscale look (rebuilding a paint page per rainbow step would churn
	// readback+upload every frame), and Variant() == Grayscale means no Invert /
	// Restyle override is active (those still win, exactly as before).
	huePainted := false
	if st := layer.Style; st.Tint && st.Grayscale && !st.HueCycle &&
		st.Variant() == courtroom.VariantGrayscale {
		if pp, ok := v.store.PaintPage(layer.Active, st.R, st.G, st.B,
			st.Paint2R, st.Paint2G, st.Paint2B, st.PaintSplit); ok && frame < len(pp.Frames) {
			tex = pp.Frames[frame]
			huePainted = true
		}
	}
	// A transmitted PER-PIXEL effect (invert / grayscale) swaps in a cached variant
	// texture built from the base's transformed pixels — SetColorMod can't do either.
	// The colour-mod bracket below still applies ON TOP (so invert + glow composes).
	// Built once per (base, effect) and cached on the page; this is a 0-alloc map hit
	// every frame after the first. (Also the hue-paint fallback: if the paint page
	// couldn't build, the old grayscale×tint composition still renders.)
	if eff := layer.Style.Variant(); eff != courtroom.VariantNone && !huePainted {
		if vpg, ok := v.store.VariantPage(layer.Active, eff); ok && frame < len(vpg.Frames) {
			tex = vpg.Frames[frame]
		}
	}

	// Resolve the effective per-layer effects into plain locals (no allocation; a
	// no-FX layer leaves them all neutral and the blit byte-identical). A
	// TRANSMITTED style — the speaker's own choice, decoded from their message —
	// takes precedence over the viewer's local wash for that layer; otherwise the
	// local wash (rainbow / solid) applies exactly as before.
	var (
		modR, modG, modB                    uint8 = 255, 255, 255
		alphaMod                            uint8 = 255
		doColorMod, glow, wob, spin, glitch bool
		motion                              uint8     // #34 transmitted movement path (0 = none)
		pathLen                             uint8     // #34 custom-path point count (0 = none, else 2..16)
		pathPts                             [16]uint8 // #34 custom-path waypoints (size MUST match courtroom maxPathPoints)
		scalePct                            = 100
		rotDeg                              float64
		glitchMode                          uint8 // transmitted glitch look (courtroom.Glitch*; 0 = classic)
		gAR, gAG, gAB, gBR, gBG, gBB        uint8 // fringe ghost colour pair (all-zero = classic red/blue)
	)
	if st := layer.Style; st.Active() {
		// Colour: a transmitted hue-cycle rainbow, else the tint colour, then
		// optionally dimmed by brightness — all collapse into one ColorMod.
		if st.HueCycle {
			phase := v.rainbowPhase + hueShift
			if v.curCycle > 0 {
				phase %= v.curCycle
			}
			modR, modG, modB = rainbowMod(phase, v.curCycle, floorForVividness(styleHueVividness))
			doColorMod = true
		} else if st.Tint && !huePainted {
			// Plain multiply tint. Skipped when the hue-paint variant is on stage:
			// its pixels already carry the colour, and multiplying it in again
			// would re-darken exactly what the paint variant exists to fix.
			modR, modG, modB, doColorMod = st.R, st.G, st.B, true
		}
		if b := st.BrightnessPct(); b != 100 {
			if !doColorMod {
				modR, modG, modB, doColorMod = 255, 255, 255, true
			}
			modR, modG, modB = scaleChannel(modR, b), scaleChannel(modG, b), scaleChannel(modB, b)
		}
		alphaMod = st.AlphaMod() // opacity (floored so a received style can't go invisible)
		glow, wob, spin = st.Glow, st.Wobble, st.Spin
		motion = st.Motion
		pathPts, pathLen = st.Path, st.PathLen // #34 custom path (a cheap fixed-size array copy)
		glitch = st.Glitch
		glitchMode = st.GlitchMode
		gAR, gAG, gAB = st.GlitchAR, st.GlitchAG, st.GlitchAB
		gBR, gBG, gBB = st.GlitchBR, st.GlitchBG, st.GlitchBB
		scalePct = st.ScalePct()
		rotDeg = st.RotationDeg()
		if st.FlipH { // transmitted mirror toggles the layer's own flip
			if flip == sdl.FLIP_HORIZONTAL {
				flip = sdl.FLIP_NONE
			} else {
				flip = sdl.FLIP_HORIZONTAL
			}
		}
	} else {
		fx := v.fx
		if fx.tinted() {
			doColorMod = true
			if fx.Rainbow {
				phase := v.rainbowPhase + hueShift
				if fx.PerCharHue { // each character a different hue at once
					phase += charHueOffset(layer.Active, v.curCycle)
				}
				if v.curCycle > 0 { // defensive: curCycle is floored > 0 in Update
					phase %= v.curCycle
				}
				modR, modG, modB = rainbowMod(phase, v.curCycle, floorForVividness(fx.Vividness))
			} else {
				modR, modG, modB = fx.SolidR, fx.SolidG, fx.SolidB
			}
			glow = fx.Glow // local glow rides the local tint (unchanged)
		}
		wob, spin = fx.Wobble, fx.Spin
	}

	// #121 spotlight: dim this non-speaker layer by scaling its colour toward black, ON TOP of
	// whatever tint / brightness / wash resolved above (a dimmed pair keeps its own style, just
	// darker). Forces a ColorMod; the speaker passes dimPct 100 → no-op, byte-identical.
	if dimPct < 100 {
		modR, modG, modB = scaleChannel(modR, dimPct), scaleChannel(modG, dimPct), scaleChannel(modB, dimPct)
		doColorMod = true
	}

	// Speaker-swap crossfade (power-user, 0 = off): while fadeLeft runs, the
	// PREVIOUS sprite draws underneath at full strength and this (new) sprite
	// alpha-ramps in on top — lastGood still names the old sprite here by
	// design (its update above waits for the fade to end). Resolved by string
	// through the store like every stand-in; an evicted under-layer just means
	// the ramp plays over the background.
	if anim.fadeLeft > 0 && v.crossfade > 0 && anim.lastGood != "" && anim.lastGood != anim.base {
		if under, okU := v.store.Get(anim.lastGood); okU && len(under.Frames) > 0 {
			v.drawHeldSprite(ren, layer, under, vp)
		}
		ramp := 255 - int(255*anim.fadeLeft/v.crossfade) // 0 → 255 across the fade
		if ramp < 0 {
			ramp = 0
		}
		alphaMod = uint8(int(alphaMod) * ramp / 255)
	}

	// #122 idle breathing: fold the breathing scale-pulse into scalePct (applied below) and
	// stash the bob to add after the wobble. Local + AsyncAO-only, pure math off the free-running
	// clock; v.fx already has it cleared under ReduceMotion. Composes with a transmitted scale +
	// the wobble; off → breathBobY 0 and scalePct unchanged, so the blit is byte-identical.
	var breathBobY int32
	if v.fx.IdleBreath {
		bobY, addScale := breathTransform(v.fxClock, vp, v.fx.BreathAmp, v.fx.BreathSpeed)
		if v.fx.BreathBob {
			breathBobY = bobY
		}
		if v.fx.BreathScale {
			scalePct += addScale
		}
	}

	// Static glitch: fold the signal-loss flicker into the alpha BEFORE the mods are
	// applied (the jolt/fringe below runs after them). Pure hash math per time bucket;
	// the other modes leave alphaMod untouched.
	if glitch && glitchMode == courtroom.GlitchStatic {
		alphaMod = uint8(int(alphaMod) * glitchFlickerPct(v.fxClock) / 100)
	}

	// Apply each modulation, then restore EACH to neutral after the blit so it
	// never bleeds onto the next user of this SHARED T1 page (next frame, emote
	// preview, wardrobe grid). ColorMod leaves alpha alone so the transparent
	// cutout stays cut out; AlphaMod handles opacity; ADD blend makes a glow.
	//
	// INVARIANT: drawSprite is the *only* place the renderer MOD-brackets a texture
	// for drawing — SetColorMod / SetBlendMode / SetAlphaMod (besides the upload
	// default BLENDMODE_BLEND at textures.go, the variant readback in variant.go,
	// which flips a frame to BLENDMODE_NONE for an exact copy and restores BLEND,
	// and drawHeldSprite's debug-tint bracket, which restores neutral the same way).
	// Any future effect that mods a texture elsewhere MUST restore it too, or art bleeds.
	if doColorMod {
		_ = tex.SetColorMod(modR, modG, modB)
	}
	if alphaMod != 255 {
		_ = tex.SetAlphaMod(alphaMod)
	}
	if glow {
		_ = tex.SetBlendMode(sdl.BLENDMODE_ADD)
	}
	// Transmitted scale: resize around the sprite's CENTRE so it grows/shrinks in
	// place rather than from a corner.
	if scalePct != 100 {
		nw := v.dstRect.W * int32(scalePct) / 100
		nh := v.dstRect.H * int32(scalePct) / 100
		v.dstRect.X += (v.dstRect.W - nw) / 2
		v.dstRect.Y += (v.dstRect.H - nh) / 2
		v.dstRect.W, v.dstRect.H = nw, nh
	}
	// Optional motion FX: a gentle sway and/or slow spin, pure math off the
	// free-running fxClock (no allocation; off = zero offset / zero angle).
	if wob {
		dx, dy := wobbleOffset(v.fxClock, vp)
		v.dstRect.X += dx
		v.dstRect.Y += dy
	}
	// #34 movement: a custom drawn PATH wins over a predefined motion; both are pure math
	// off the free-running clock, so when neither is set this whole block is skipped.
	switch {
	case pathLen >= 2:
		mdx, mdy := pathOffset(pathPts[:], pathLen, v.fxClock, vp)
		v.dstRect.X += mdx
		v.dstRect.Y += mdy
	case motion != 0:
		mdx, mdy := motionOffset(motion, v.fxClock, vp)
		v.dstRect.X += mdx
		v.dstRect.Y += mdy
	}
	if breathBobY != 0 { // #122 idle breathing's vertical bob (folded scale was applied above)
		v.dstRect.Y += breathBobY
	}
	// #13 glitch: an occasional horizontal jolt moves the WHOLE sprite (solid + outline +
	// the chromatic fringe below) together; glitchSplit is the chromatic offset for the
	// fringe. The mode picks its own split/jolt tuning (glitchParamsMode).
	var glitchSplit int32
	if glitch {
		var jolt int32
		glitchSplit, jolt = glitchParamsMode(glitchMode, v.fxClock, vp.H)
		v.dstRect.X += jolt
	}
	angle := rotDeg // transmitted fixed tilt (spin adds to it)
	if spin {
		angle += spinDegrees(v.fxClock)
	}
	// #8 outline + drop-shadow: draw a tinted silhouette BEHIND the sprite, using the FINAL
	// dstRect/angle/flip so it tracks scale + wobble + rotation. Gated so a normal sprite is
	// byte-identical. The silhouette page is a cached white-filled variant; drawSilhouette
	// set→blit→restores ColorMod AND AlphaMod each call so nothing bleeds onto the next user.
	if st := layer.Style; st.Outline || st.DropShadow {
		if sil, ok := v.store.VariantPage(layer.Active, courtroom.VariantSilhouette); ok && frame < len(sil.Frames) {
			silTex := sil.Frames[frame]
			ow := vp.H / outlineDivisor
			if ow < outlineWidthMin {
				ow = outlineWidthMin
			}
			if st.DropShadow {
				v.drawSilhouette(ren, silTex, angle, flip, shadowDir[:], ow*shadowMultiple, 0, 0, 0, shadowAlpha)
			}
			if st.Outline {
				or, og, ob := uint8(255), uint8(255), uint8(255) // white by default
				if st.OutlineR != 0 || st.OutlineG != 0 || st.OutlineB != 0 {
					or, og, ob = st.OutlineR, st.OutlineG, st.OutlineB // #M5+ custom outline colour
				}
				v.drawSilhouette(ren, silTex, angle, flip, outlineDirs[:], ow, or, og, ob, outlineAlpha)
			}
		}
	}
	// Torn glitch: during its tear windows the sprite draws as horizontal bands, some
	// shoved sideways (VHS tracking-error style). Angle-rotated sprites keep the plain
	// blit — CopyEx rotates each band around its own centre, which shreds the art
	// rather than tearing it. Everything else (mods, fringe) composes unchanged.
	if glitch && glitchMode == courtroom.GlitchTorn && angle == 0 &&
		v.fxClock%glitchTornPeriod < glitchTornWindow {
		v.drawTornSprite(ren, tex, page, flip, glitchSplit)
	} else {
		_ = ren.CopyEx(tex, nil, &v.dstRect, angle, nil, flip)
	}
	if doColorMod {
		_ = tex.SetColorMod(255, 255, 255)
	}
	if alphaMod != 255 {
		_ = tex.SetAlphaMod(255)
	}
	if glow {
		_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	}
	// #13 chromatic fringe: a ghost pair over the solid sprite — classic red/blue, or
	// the style's own colour pair (the mods above are already restored, so these set +
	// restore their own). 0-alloc.
	if glitch {
		v.drawGlitchFringe(ren, tex, angle, flip, glitchSplit, glitchMode, gAR, gAG, gAB, gBR, gBG, gBB)
	}
}

// drawGlitchFringe overlays the two offset ghosts of the sprite (chromatic aberration):
// colour A left, colour B right — all-zero = the classic red/blue pair. Static jitters
// the ghosts on both axes per time bucket; Echo draws a second, fainter pair further
// out first. Shared tex: set→blit→RESTORE ColorMod + AlphaMod so nothing bleeds. 0-alloc.
func (v *Viewport) drawGlitchFringe(ren *sdl.Renderer, tex *sdl.Texture, angle float64, flip sdl.RendererFlip, split int32, mode uint8, aR, aG, aB, bR, bG, bB uint8) {
	if aR == 0 && aG == 0 && aB == 0 && bR == 0 && bG == 0 && bB == 0 {
		aR, bB = 255, 255 // the classic pair: red left, blue right
	}
	adx, ady := -split, int32(0)
	bdx, bdy := split, int32(0)
	if mode == courtroom.GlitchStatic { // signal loss: the ghosts jitter around the sprite
		h := glitchHash(uint32(v.fxClock / glitchStaticBucket))
		amp := split * glitchStaticAmpMul
		adx -= int32(h&0xF) * amp / 15
		ady = int32((h>>4)&0xF)*amp/15 - amp/2
		bdx += int32((h>>8)&0xF) * amp / 15
		bdy = int32((h>>12)&0xF)*amp/15 - amp/2
	}
	if mode == courtroom.GlitchEcho { // a far, faint echo pair behind the main fringe
		_ = tex.SetAlphaMod(glitchFringeAlpha / glitchEchoFadeDiv)
		v.blitFringeGhost(ren, tex, angle, flip, adx*glitchEchoFarMul, 0, aR, aG, aB)
		v.blitFringeGhost(ren, tex, angle, flip, bdx*glitchEchoFarMul, 0, bR, bG, bB)
	}
	_ = tex.SetAlphaMod(glitchFringeAlpha)
	v.blitFringeGhost(ren, tex, angle, flip, adx, ady, aR, aG, aB)
	v.blitFringeGhost(ren, tex, angle, flip, bdx, bdy, bR, bG, bB)
	_ = tex.SetColorMod(255, 255, 255)
	_ = tex.SetAlphaMod(255)
}

// blitFringeGhost draws one tinted ghost copy at an offset from the sprite's rect.
// Caller owns the alpha bracket + the final restore.
func (v *Viewport) blitFringeGhost(ren *sdl.Renderer, tex *sdl.Texture, angle float64, flip sdl.RendererFlip, dx, dy int32, r, g, b uint8) {
	_ = tex.SetColorMod(r, g, b)
	v.fxRect = v.dstRect
	v.fxRect.X += dx
	v.fxRect.Y += dy
	_ = ren.CopyEx(tex, nil, &v.fxRect, angle, nil, flip)
}

// drawTornSprite draws the sprite as glitchTornBands horizontal bands, ~two thirds of
// them shoved sideways by a per-(band, bucket) hash — deterministic, re-rolled every
// glitchTornBucket so the tear crawls while its window lasts. Bands map 1:1 between
// texture rows and dstRect rows, so flip (horizontal) stays correct per band. 0-alloc.
func (v *Viewport) drawTornSprite(ren *sdl.Renderer, tex *sdl.Texture, page *TexturePage, flip sdl.RendererFlip, split int32) {
	bucket := glitchHash(uint32(v.fxClock / glitchTornBucket))
	maxOff := split * glitchTornOffMul
	for i := int32(0); i < glitchTornBands; i++ {
		sy0 := page.H * i / glitchTornBands
		sy1 := page.H * (i + 1) / glitchTornBands
		dy0 := v.dstRect.H * i / glitchTornBands
		dy1 := v.dstRect.H * (i + 1) / glitchTornBands
		var off int32
		if h := glitchHash(bucket + uint32(i)*glitchTornBandSalt); h%3 != 0 { // ~2/3 of the bands tear
			off = int32(h%uint32(2*maxOff+1)) - maxOff
		}
		v.srcRect = sdl.Rect{X: 0, Y: sy0, W: page.W, H: sy1 - sy0}
		v.fxRect = sdl.Rect{X: v.dstRect.X + off, Y: v.dstRect.Y + dy0, W: v.dstRect.W, H: dy1 - dy0}
		_ = ren.CopyEx(tex, &v.srcRect, &v.fxRect, 0, nil, flip)
	}
}

// Entrance tuning (#9): a quick slide-in for a newly-arrived speaker.
const (
	entranceDuration  = 320 * time.Millisecond // slide settle time
	entranceSlideFrac = 0.22                   // start offset = vp.W * this (slides from the left)
)

// entranceSlide returns the speaker's horizontal slide offset (negative → from the left) at
// the given remaining time: most offset at the start, easing to 0 (frac² snap-in). Pure,
// 0-alloc.
func entranceSlide(left time.Duration, vpW int32) int32 {
	frac := float64(left) / float64(entranceDuration)
	if frac > 1 {
		frac = 1
	}
	return -int32(float64(vpW) * entranceSlideFrac * frac * frac)
}

// Glitch tuning (#13): a chromatic split that shimmers + a periodic horizontal jolt.
const (
	glitchSplitDivisor = 220                     // base chromatic offset = vp.H / divisor (floored)
	glitchSplitMin     = 2                       //
	glitchFringeAlpha  = 150                     // chromatic ghost strength
	glitchShimmerHz    = 7.0                     // how fast the split pulses
	glitchJoltPeriod   = 1300 * time.Millisecond // a jolt every period
	glitchJoltWindow   = 90 * time.Millisecond   // …lasting this long
	glitchJoltMultiple = 3                       // jolt distance = split * this
)

// glitchParams returns the chromatic split (px) and the horizontal jolt (px) at the given
// clock for a stage of height vpH. Pure scalar math — 0-alloc, safe on the render path.
func glitchParams(clock time.Duration, vpH int32) (split, jolt int32) {
	split = vpH / glitchSplitDivisor
	if split < glitchSplitMin {
		split = glitchSplitMin
	}
	split += int32(math.Abs(math.Sin(clock.Seconds()*glitchShimmerHz)) * float64(split)) // shimmer
	if clock%glitchJoltPeriod < glitchJoltWindow {                                       // periodic jolt
		jolt = split * glitchJoltMultiple
		if int64(clock/glitchJoltPeriod)%2 == 0 {
			jolt = -jolt
		}
	}
	return split, jolt
}

// Per-mode glitch tuning (the v1.54.0 glitch options). All pure math off the
// free-running clock, like the classic parameters above.
const (
	glitchHeavySplitMul   = 2                      // Heavy: fringe distance × this
	glitchHeavyJoltPeriod = 700 * time.Millisecond // Heavy: jolts come almost twice as often
	glitchHeavyJoltWindow = 150 * time.Millisecond // …and hold longer
	glitchHeavyJoltMul    = 4                      // …and shove harder
	glitchEchoSplitMul    = 2                      // Echo: main fringe distance × this
	glitchEchoFarMul      = 3                      // Echo: the faint pair sits this × further out
	glitchEchoFadeDiv     = 3                      // Echo: the far pair's alpha = fringe / this
	glitchStaticBucket    = 50 * time.Millisecond  // Static: jitter/flicker re-roll cadence
	glitchStaticAmpMul    = 2                      // Static: ghost jitter amplitude = split × this
	glitchStaticFloorPct  = 70                     // Static: normal flicker floor (percent alpha)
	glitchStaticDropPct   = 35                     // Static: the hard dropout's alpha
	glitchStaticDropMod   = 24                     // Static: ~1 bucket in this many dips hard
	glitchTornBands       = int32(8)               // Torn: horizontal band count
	glitchTornPeriod      = 900 * time.Millisecond // Torn: a tear every period
	glitchTornWindow      = 140 * time.Millisecond // …lasting this long
	glitchTornBucket      = 45 * time.Millisecond  // …re-rolling the band offsets this often
	glitchTornOffMul      = 3                      // Torn: max band shove = split × this
	glitchTornBandSalt    = 0x9E3779B9             // decorrelates per-band hashes within a bucket
)

// glitchParamsMode is glitchParams specialised by the transmitted glitch look:
// Heavy widens the fringe and jolts harder/oftener on its own cadence, Echo only
// widens (its motion is the extra ghost pair), Torn/Static keep the classic split
// (their character lives in the band tearing / jitter+flicker). Pure, 0-alloc.
func glitchParamsMode(mode uint8, clock time.Duration, vpH int32) (split, jolt int32) {
	split, jolt = glitchParams(clock, vpH)
	switch mode {
	case courtroom.GlitchHeavy:
		split *= glitchHeavySplitMul
		jolt = 0
		if clock%glitchHeavyJoltPeriod < glitchHeavyJoltWindow {
			jolt = split * glitchHeavyJoltMul
			if int64(clock/glitchHeavyJoltPeriod)%2 == 0 {
				jolt = -jolt
			}
		}
	case courtroom.GlitchEcho:
		split *= glitchEchoSplitMul
	}
	return split, jolt
}

// glitchFlickerPct is Static's signal-loss alpha percentage for the current time
// bucket: usually glitchStaticFloorPct..99, with an occasional hard dropout. Pure.
func glitchFlickerPct(clock time.Duration) int {
	h := glitchHash(uint32(clock / glitchStaticBucket))
	if h%glitchStaticDropMod == 0 {
		return glitchStaticDropPct
	}
	return glitchStaticFloorPct + int(h>>8)%(100-glitchStaticFloorPct)
}

// glitchHash is a tiny integer scrambler (xorshift-multiply) behind the glitch modes'
// pseudo-random offsets — deterministic per (band, time bucket), stateless, 0-alloc.
func glitchHash(n uint32) uint32 {
	n ^= n >> 16
	n *= 0x7feb352d
	n ^= n >> 15
	n *= 0x846ca68b
	n ^= n >> 16
	return n
}

// drawSilhouette blits the (shared, cached) silhouette texture at each dir offset (scaled by
// width), tinted (r,g,b,a) — the #8 outline/shadow. ColorMod + AlphaMod are set once and
// restored to neutral after the group, so the shared page never carries a leftover mod into
// the next frame or another consumer. 0-alloc: a Viewport scratch rect + scalar math.
func (v *Viewport) drawSilhouette(ren *sdl.Renderer, tex *sdl.Texture, angle float64, flip sdl.RendererFlip, dirs [][2]int32, width int32, r, g, b, a uint8) {
	_ = tex.SetColorMod(r, g, b)
	_ = tex.SetAlphaMod(a)
	for _, d := range dirs {
		v.fxRect = v.dstRect
		v.fxRect.X += d[0] * width
		v.fxRect.Y += d[1] * width
		_ = ren.CopyEx(tex, nil, &v.fxRect, angle, nil, flip)
	}
	_ = tex.SetColorMod(255, 255, 255)
	_ = tex.SetAlphaMod(255)
}

func clampFrame(frame, count int) int {
	if frame >= count {
		return count - 1
	}
	if frame < 0 {
		return 0
	}
	return frame
}

// spotlightMinBrightness floors how dark the #121 spotlight can push a non-speaker layer, so
// it reads as "in shadow" rather than a black silhouette even at the max slider.
const spotlightMinBrightness = 15

// spotlightBrightness maps the SpotlightLevel slider [0,100] (dim intensity, higher = darker)
// to the brightness percent a non-speaker layer is drawn at, floored so it never blacks out.
func spotlightBrightness(level int) int {
	b := 100 - level
	if b < spotlightMinBrightness {
		return spotlightMinBrightness
	}
	if b > 100 {
		return 100
	}
	return b
}

// cycleForSpeed maps the Speed slider [1,100] to a hue-rotation period, higher
// speed → shorter period. The result is hard-floored at minRainbowCycle so the
// caller's phase%cycle and rainbowMod's divide can never hit zero, whatever a
// hand-edited or out-of-range pref says.
func cycleForSpeed(speed int) time.Duration {
	if speed < 1 {
		speed = 1
	}
	if speed > 100 {
		speed = 100
	}
	span := maxRainbowCycle - minRainbowCycle
	cyc := maxRainbowCycle - time.Duration(int64(span)*int64(speed-1)/99)
	if cyc < minRainbowCycle {
		cyc = minRainbowCycle
	}
	return cyc
}

// styleHueVividness is the saturation for a TRANSMITTED hue-cycle rainbow (the
// wire style carries no vividness field) — vivid but not blinding.
const styleHueVividness = 70

// scaleChannel multiplies a colour channel by pct/100 (clamped to 255): pct < 100
// dims the sprite, pct > 100 washes it brighter toward white. Used by the
// transmitted brightness control.
func scaleChannel(c uint8, pct int) uint8 {
	v := int(c) * pct / 100
	if v > 255 {
		v = 255
	}
	return uint8(v)
}

// floorForVividness maps the Vividness slider [0,100] to the colour-mod channel
// floor, higher vividness → lower floor → more saturated/neon.
func floorForVividness(v int) int {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return spriteFloorMax * (100 - v) / 100
}

// Motion-FX tuning (Wobble / Spin). Slow + small so they read as playful, not
// nauseating.
const (
	wobblePeriod     = 1700 * time.Millisecond // one full sway cycle
	wobbleAmpDivisor = 40                      // sway amplitude = vp.W / divisor
	spinPeriod       = 4 * time.Second         // one full rotation
)

// Shout-punch tuning (#12): a snappy zoom pop on objections. Short so it reads as impact, not
// a zoom; the peak scale is small so the inflated art barely overspills the stage.
const (
	shoutPunchDuration = 240 * time.Millisecond // how long the pop takes to settle
	shoutPunchPeak     = 0.06                   // peak extra scale (+6%) at the instant of the shout
)

// punchScale is the extra-scale fraction for the shout punch at the given remaining time:
// an instant attack to the peak that eases out as the clock runs down (frac², so it decays
// fast then smooths to zero). Pure — no allocation, safe on the render path.
func punchScale(left time.Duration) float64 {
	if left <= 0 {
		return 0
	}
	frac := float64(left) / float64(shoutPunchDuration)
	if frac > 1 {
		frac = 1
	}
	return shoutPunchPeak * frac * frac
}

// oscFrac returns clock's phase within period as a fraction in [0,1).
func oscFrac(clock, period time.Duration) float64 {
	if period <= 0 {
		return 0
	}
	return float64(clock%period) / float64(period)
}

// wobbleOffset is a gentle Lissajous sway (x and y on different periods) scaled
// to the viewport, for the Wobble effect. Pure math — no allocation.
func wobbleOffset(clock time.Duration, vp sdl.Rect) (dx, dy int32) {
	amp := float64(vp.W) / wobbleAmpDivisor
	dx = int32(amp * math.Sin(2*math.Pi*oscFrac(clock, wobblePeriod)))
	dy = int32(amp * 0.6 * math.Sin(2*math.Pi*oscFrac(clock, wobblePeriod*3/2)))
	return dx, dy
}

// spinDegrees is the continuous rotation angle (0..360) for the Spin effect.
func spinDegrees(clock time.Duration) float64 {
	return oscFrac(clock, spinPeriod) * 360
}

// Sprite-motion tuning (#34). Amplitude is a fraction of the viewport width (clamped so a
// roaming sprite can't leave the stage); the period is one slow loop.
const (
	motionAmpDivisor = 7.0                     // motion amplitude = vp.W / divisor
	motionPeriod     = 3500 * time.Millisecond // one full loop
)

// motionOffset returns the per-frame (dx,dy) for a transmitted Motion path (#34): a clamped
// parametric loop off the free-running clock. Pure math, no allocation; MotionNone never
// reaches here (the caller skips motion==0), so the no-motion case stays byte-identical.
func motionOffset(motion uint8, clock time.Duration, vp sdl.Rect) (dx, dy int32) {
	amp := float64(vp.W) / motionAmpDivisor
	t := 2 * math.Pi * oscFrac(clock, motionPeriod)
	switch motion {
	case courtroom.MotionOrbit:
		dx = int32(amp * math.Cos(t))
		dy = int32(amp * 0.55 * math.Sin(t)) // a flattened ellipse reads as "around the box"
	case courtroom.MotionBounce:
		dy = int32(-amp * 0.8 * math.Abs(math.Sin(t))) // bob UP from the baseline
	case courtroom.MotionSway:
		dx = int32(amp * math.Sin(t))
	case courtroom.MotionDrift:
		dx = int32(amp * math.Sin(t)) // a slow figure-8 roam
		dy = int32(amp * 0.5 * math.Sin(2*t))
	case courtroom.MotionShake:
		ft := 2 * math.Pi * oscFrac(clock, motionPeriod/6) // fast jitter, small amplitude
		dx = int32(amp * 0.25 * math.Sin(ft))
		dy = int32(amp * 0.25 * math.Sin(ft*1.7)) // off-ratio so it's not a clean diagonal
	case courtroom.MotionSpiral:
		rad := 0.4 + 0.6*math.Abs(math.Sin(t/2)) // orbit whose radius pulses in and out
		dx = int32(amp * rad * math.Cos(t))
		dy = int32(amp * 0.55 * rad * math.Sin(t))
	case courtroom.MotionPendulum:
		dx = int32(amp * math.Sin(t))                   // swing side to side…
		dy = int32(-amp * 0.25 * math.Abs(math.Cos(t))) // …lifting slightly at each end (an arc)
	}
	return dx, dy
}

// pathOffset returns the per-frame (dx,dy) along a user-drawn custom path (#34): the sprite
// traverses the waypoints over one motionPeriod, lerping between them and looping back to the
// first. Each waypoint is a packed 4-bit X/Y on a 16×16 grid centred at 8,8. Pure math, no
// allocation (path is a slice of a stack array the caller owns).
func pathOffset(path []byte, pathLen uint8, clock time.Duration, vp sdl.Rect) (dx, dy int32) {
	n := int(pathLen)
	if n < 2 || n > len(path) {
		return 0, 0
	}
	amp := float64(vp.W) / motionAmpDivisor
	seg := oscFrac(clock, motionPeriod) * float64(n) // 0..n across the whole loop
	i := int(seg)
	if i >= n {
		i = n - 1
	}
	frac := seg - float64(i)
	j := (i + 1) % n // wrap to the first point so the path loops
	x0, y0 := pathPointPx(path[i], amp)
	x1, y1 := pathPointPx(path[j], amp)
	return int32(x0 + (x1-x0)*frac), int32(y0 + (y1-y0)*frac)
}

// pathPointPx maps a packed 4-bit X/Y waypoint (each 0..15, centred at 8) to a pixel offset
// scaled to amp.
func pathPointPx(p byte, amp float64) (x, y float64) {
	return float64(int(p>>4)-8) / 8.0 * amp, float64(int(p&0x0F)-8) / 8.0 * amp
}

// Idle-breathing tuning (#122). Slow + small so a static sprite reads as gently alive.
const (
	breathPeriodMin = 1500 * time.Millisecond // fastest breath (speed = 100)
	breathPeriodMax = 5000 * time.Millisecond // slowest breath (speed = 1)
	breathBobDiv    = 50                      // bob amplitude at amp=100 = vp.H / div (px)
	breathScaleMax  = 5                       // scale-pulse adds up to +5% at amp=100
)

// breathPeriod maps the BreathSpeed slider [1,100] to a breathing period (higher = faster).
func breathPeriod(speed int) time.Duration {
	if speed < 1 {
		speed = 1
	}
	if speed > 100 {
		speed = 100
	}
	span := breathPeriodMax - breathPeriodMin
	return breathPeriodMax - time.Duration(int64(span)*int64(speed-1)/99)
}

// breathTransform is the #122 idle-breathing offsets at the given clock: a vertical bob and a
// scale-pulse (an "inhale" that only expands, 0..max, via (1+sin)/2), both scaled by the
// amplitude slider. Pure math off the free-running fxClock — no allocation. The caller gates
// each component (Bob / Scale) and folds scaleAddPct into the sprite's scale.
func breathTransform(clock time.Duration, vp sdl.Rect, ampPct, speedPct int) (bobY int32, scaleAddPct int) {
	if ampPct < 1 {
		ampPct = 1
	} else if ampPct > 100 {
		ampPct = 100
	}
	s := math.Sin(2 * math.Pi * oscFrac(clock, breathPeriod(speedPct)))
	bobY = int32(float64(vp.H) * float64(ampPct) / 100 / breathBobDiv * s)
	scaleAddPct = int(float64(breathScaleMax) * float64(ampPct) / 100 * (1 + s) / 2)
	return bobY, scaleAddPct
}

// charHueOffset hashes a sprite's base name to a stable hue offset in [0,cycle)
// so PerCharHue gives each character a different rainbow colour at once. Inline
// FNV-1a — no allocation, safe on the render path.
func charHueOffset(base string, cycle time.Duration) time.Duration {
	if cycle <= 0 {
		return 0
	}
	const (
		fnvOffset = 2166136261
		fnvPrime  = 16777619
	)
	h := uint32(fnvOffset)
	for i := 0; i < len(base); i++ {
		h ^= uint32(base[i])
		h *= fnvPrime
	}
	return time.Duration(uint64(h) % uint64(cycle))
}

// rainbowMod maps the hue clock to an SDL colour-mod (r,g,b) for the rainbow
// sprite wash. Pure integer math — no allocation, no float, no math import —
// so it is safe on the zero-alloc render path. It walks the six edges of the
// RGB cube (the classic hue wheel) and then lifts each channel off zero by
// floor so the tint never crushes the art to a silhouette.
func rainbowMod(phase, cycle time.Duration, floor int) (r, g, b uint8) {
	const segments = 6  // six edges of the RGB cube
	const segSpan = 256 // hue ticks per edge
	if cycle <= 0 {     // defensive: never divide by zero (Update floors it > 0)
		cycle = minRainbowCycle
	}
	if phase < 0 {
		phase = 0
	}
	// Position on the 0..(segments*segSpan) hue ring.
	hue := int64(phase) * (segments * segSpan) / int64(cycle)
	seg := hue / segSpan % segments
	t := hue % segSpan // 0..255 within the current edge
	var rr, gg, bb int64
	switch seg {
	case 0:
		rr, gg, bb = 255, t, 0
	case 1:
		rr, gg, bb = 255-t, 255, 0
	case 2:
		rr, gg, bb = 0, 255, t
	case 3:
		rr, gg, bb = 0, 255-t, 255
	case 4:
		rr, gg, bb = t, 0, 255
	default: // 5
		rr, gg, bb = 255, 0, 255-t
	}
	return liftChannel(rr, floor), liftChannel(gg, floor), liftChannel(bb, floor)
}

// liftChannel maps a 0..255 hue channel into [floor,255] so the colour-mod
// tints the sprite rather than silhouetting it (see spriteFloorMax).
func liftChannel(c int64, floor int) uint8 {
	return uint8(int64(floor) + c*(255-int64(floor))/255)
}
