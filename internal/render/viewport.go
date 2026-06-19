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

	Speed     int // rainbow hue speed slider [1,100] (higher = faster)
	Vividness int // rainbow saturation slider [0,100] (higher = more neon)

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

	// scratch rects reused every frame (no allocs in the loop): taking the
	// address of a stack value for a cgo call would force a heap escape
	// per draw, so every Copy destination lives on the Viewport.
	dstRect  sdl.Rect
	fillRect sdl.Rect
}

// NewViewport builds a viewport over the texture store.
func NewViewport(store *TextureStore) *Viewport {
	return &Viewport{store: store}
}

// SetSpriteFX mirrors the user's sprite colour-FX preferences onto the
// viewport. The App rebuilds the value struct and calls this once per frame
// before Update; it only feeds a SetColorMod/SetBlendMode bracket around the
// existing blit, so updating it costs nothing and never allocates.
func (v *Viewport) SetSpriteFX(fx SpriteFX) { v.fx = fx }

// Update advances animation clocks against the active scene.
func (v *Viewport) Update(scene *courtroom.Scene, dt time.Duration) {
	// Derive this frame's hue period from the speed slider and advance the
	// rainbow clock unconditionally (cheap; the modulo keeps the phase bounded
	// so it never overflows however long the client runs). cycleForSpeed is
	// floored above zero, so the modulo below can never panic. The phase is
	// only *read* when a wash is enabled.
	v.curCycle = cycleForSpeed(v.fx.Speed)
	v.rainbowPhase = (v.rainbowPhase + dt) % v.curCycle
	v.fxClock += dt // free-running clock for the motion effects (wobble/spin)

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
	if scene.ShakeLeft > 0 {
		// Decaying sinusoid: amplitude follows the remaining time, so the
		// shake settles instead of cutting off.
		strength := float64(scene.ShakeLeft) / float64(courtroom.ScreenshakeDuration)
		amp := float64(vp.W) / shakeAmplitudeDivisor * strength
		phase := 2 * math.Pi * float64(scene.ShakeLeft) / float64(shakePeriod)
		vp.X += int32(amp * math.Sin(phase))
		vp.Y += int32(amp / 2 * math.Cos(phase*1.3))
	}

	v.drawFill(ren, scene.BackgroundBase, &v.bgAnim, vp)

	// Pair hue offset for the "desync" effect: half a period so the two
	// characters show opposite hues. Zero unless rainbow + desync are both on,
	// and the speaker always passes 0 — so with the effect off the speaker blit
	// is byte-identical to before.
	var pairShift time.Duration
	if v.fx.Rainbow && v.fx.PairDesync {
		pairShift = v.curCycle / 2
	}

	if scene.PairActive && !scene.SpeakerInFront {
		// Speaker behind: draw speaker first, pair over it.
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, vp, 0)
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp, pairShift)
	} else if scene.PairActive {
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp, pairShift)
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, vp, 0)
	} else {
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, vp, 0)
	}

	if scene.ShowDesk {
		v.drawFill(ren, scene.DeskBase, &v.deskAnim, vp)
	}
	if shoutBase := effectiveShoutBase(scene, v.store); shoutBase != "" {
		v.drawFill(ren, shoutBase, &v.shoutAnim, vp)
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
func (v *Viewport) drawFill(ren *sdl.Renderer, base string, anim *animState, vp sdl.Rect) {
	if base == "" {
		return
	}
	page, ok := anim.resolve(v.store)
	if !ok || len(page.Frames) == 0 {
		return
	}
	frame := clampFrame(anim.frame, len(page.Frames))
	v.fillRect = vp
	_ = ren.Copy(page.Frames[frame], nil, &v.fillRect)
}

// drawSprite draws a character layer: scaled to viewport height preserving
// aspect, bottom-centered, shifted by percent offsets, optionally mirrored.
// hueShift offsets this layer's rainbow phase (used to desync the pair).
func (v *Viewport) drawSprite(ren *sdl.Renderer, layer *courtroom.SpriteLayer, anim *animState, vp sdl.Rect, hueShift time.Duration) {
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
	fx := v.fx
	tinted := fx.tinted()
	if tinted {
		// Optional eye-candy: modulate the sprite by a colour. Alpha is
		// untouched, so the transparent cutout stays transparent — it tints the
		// character, it never fills the frame. Rainbow (a cycling hue) wins over
		// the fixed Solid colour when both are on.
		var r, g, b uint8
		if fx.Rainbow {
			phase := v.rainbowPhase + hueShift
			if fx.PerCharHue { // each character a different hue at once
				phase += charHueOffset(layer.Active, v.curCycle)
			}
			if v.curCycle > 0 { // defensive: curCycle is floored > 0 in Update
				phase %= v.curCycle
			}
			r, g, b = rainbowMod(phase, v.curCycle, floorForVividness(fx.Vividness))
		} else {
			r, g, b = fx.SolidR, fx.SolidG, fx.SolidB
		}
		_ = tex.SetColorMod(r, g, b)
		if fx.Glow {
			// Additive: the tint adds light instead of multiplying, so the
			// sprite glows like neon (and turns translucent — the background
			// shows through, by design).
			_ = tex.SetBlendMode(sdl.BLENDMODE_ADD)
		}
	}
	// Optional motion FX (independent of the colour wash): a gentle position
	// sway and/or a slow spin. Both are pure math off the free-running fxClock,
	// so they add no allocation — and when off, the offset is 0 and the angle is
	// 0, leaving the blit byte-identical to before.
	if fx.Wobble {
		dx, dy := wobbleOffset(v.fxClock, vp)
		v.dstRect.X += dx
		v.dstRect.Y += dy
	}
	angle := 0.0
	if fx.Spin {
		angle = spinDegrees(v.fxClock)
	}
	_ = ren.CopyEx(tex, nil, &v.dstRect, angle, nil, flip)
	if tinted {
		// Restore the neutral mod/blend on this SHARED T1 page: leaving either
		// set would bleed onto every later user of the same texture (the next
		// frame, the emote preview, the wardrobe grid). Cheap and load-bearing.
		//
		// INVARIANT: drawSprite is the *only* place the renderer touches
		// SetColorMod or SetBlendMode (the sole other SetBlendMode is the
		// upload default, BLENDMODE_BLEND at textures.go) — that's what keeps
		// scenery/preview/wardrobe untouched, and why restoring to BLEND is
		// correct. Any future effect that mods a texture elsewhere MUST restore
		// it too, or art bleeds.
		_ = tex.SetColorMod(255, 255, 255)
		if fx.Glow {
			_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
		}
	}
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
