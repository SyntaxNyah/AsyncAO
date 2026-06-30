package render

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"

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

	page    *TexturePage
	pageGen uint64
}

// reset rebinds the state to a new asset base.
func (a *animState) reset(base string) {
	a.base = base
	a.frame = 0
	a.elapsed = 0
	a.finished = false
	a.page = nil
	a.pageGen = 0
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
				a.finished = true
				return true
			}
			a.frame = 0
		} else {
			a.frame++
		}
	}
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
	reflRect sdl.Rect // #123 reflection blit destination
	reflClip sdl.Rect // #123 reflection clip rect (confine to the stage when not already clipped)

	// #10 post-processing overlays: cached, size-stable textures blended over the stage.
	postFX               PostFX
	vignetteTex          *sdl.Texture
	scanlineTex          *sdl.Texture
	scanlineW, scanlineH int32
	grainTex             [grainFrames]*sdl.Texture
	grainIdx             int
	postRect             sdl.Rect // post-FX blit destination scratch

	// #124 particle weather: a bounded pool + one cached dot texture (snow/rain/sakura/embers).
	particles particleField
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
			if v.speakerAnim.advance(page, dt, scene.Speaker.PlayOnce) && scene.Speaker.PlayOnce {
				if v.OnPreanimDone != nil {
					v.OnPreanimDone()
				}
			}
		}
	}
	if scene.PairActive {
		if page, ok := v.pairAnim.resolve(v.store); ok {
			v.pairAnim.advance(page, dt, false)
		}
	}
}

func (v *Viewport) syncAnim(a *animState, base string) {
	if a.base != base {
		a.reset(base)
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
	page, ok := anim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		return
	}
	frame := clampFrame(anim.frame, len(page.Frames))
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
	page, ok := anim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		return
	}
	tex := page.Frames[clampFrame(anim.frame, len(page.Frames))]
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

// drawSprite draws a character layer: scaled to viewport height preserving
// aspect, bottom-centered, shifted by percent offsets, optionally mirrored.
// hueShift offsets this layer's rainbow phase (used to desync the pair).
func (v *Viewport) drawSprite(ren *sdl.Renderer, layer *courtroom.SpriteLayer, anim *animState, vp sdl.Rect, hueShift time.Duration, dimPct int) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	page, ok := anim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		return
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
	// A transmitted PER-PIXEL effect (invert / grayscale) swaps in a cached variant
	// texture built from the base's transformed pixels — SetColorMod can't do either.
	// The colour-mod bracket below still applies ON TOP (so invert + glow composes).
	// Built once per (base, effect) and cached on the page; this is a 0-alloc map hit
	// every frame after the first.
	if eff := layer.Style.Variant(); eff != courtroom.VariantNone {
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
		motion                              uint8    // #34 transmitted movement path (0 = none)
		pathLen                             uint8    // #34 custom-path point count (0 = none, else 2..6)
		pathPts                             [6]uint8 // #34 custom-path waypoints (matches courtroom maxPathPoints)
		scalePct                            = 100
		rotDeg                              float64
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
		} else if st.Tint {
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
		pathPts, pathLen = st.Path, st.PathLen // #34 custom path (a cheap 6-byte array copy)
		glitch = st.Glitch
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

	// Apply each modulation, then restore EACH to neutral after the blit so it
	// never bleeds onto the next user of this SHARED T1 page (next frame, emote
	// preview, wardrobe grid). ColorMod leaves alpha alone so the transparent
	// cutout stays cut out; AlphaMod handles opacity; ADD blend makes a glow.
	//
	// INVARIANT: drawSprite is the *only* place the renderer MOD-brackets a texture
	// for drawing — SetColorMod / SetBlendMode / SetAlphaMod (besides the upload
	// default BLENDMODE_BLEND at textures.go, and the variant readback in variant.go,
	// which flips a frame to BLENDMODE_NONE for an exact copy and restores BLEND).
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
	// the chromatic fringe below) together; glitchSplit is the chromatic offset for the fringe.
	var glitchSplit int32
	if glitch {
		var jolt int32
		glitchSplit, jolt = glitchParams(v.fxClock, vp.H)
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
				v.drawSilhouette(ren, silTex, angle, flip, outlineDirs[:], ow, 255, 255, 255, outlineAlpha)
			}
		}
	}
	_ = ren.CopyEx(tex, nil, &v.dstRect, angle, nil, flip)
	if doColorMod {
		_ = tex.SetColorMod(255, 255, 255)
	}
	if alphaMod != 255 {
		_ = tex.SetAlphaMod(255)
	}
	if glow {
		_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	}
	// #13 chromatic fringe: a red ghost left + blue ghost right over the solid sprite (the
	// mods above are already restored, so these set + restore their own). 0-alloc.
	if glitch {
		v.drawGlitchFringe(ren, tex, angle, flip, glitchSplit)
	}
}

// drawGlitchFringe overlays a red and a blue offset ghost of the sprite (chromatic
// aberration). Shared tex: set→blit→RESTORE ColorMod + AlphaMod so nothing bleeds. 0-alloc.
func (v *Viewport) drawGlitchFringe(ren *sdl.Renderer, tex *sdl.Texture, angle float64, flip sdl.RendererFlip, split int32) {
	_ = tex.SetAlphaMod(glitchFringeAlpha)
	_ = tex.SetColorMod(255, 0, 0) // red ghost, offset left
	v.fxRect = v.dstRect
	v.fxRect.X -= split
	_ = ren.CopyEx(tex, nil, &v.fxRect, angle, nil, flip)
	_ = tex.SetColorMod(0, 0, 255) // blue ghost, offset right
	v.fxRect = v.dstRect
	v.fxRect.X += split
	_ = ren.CopyEx(tex, nil, &v.fxRect, angle, nil, flip)
	_ = tex.SetColorMod(255, 255, 255)
	_ = tex.SetAlphaMod(255)
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
