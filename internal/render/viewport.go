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

	// rainbowSpriteCycle is the period of one full hue rotation for the
	// optional rainbow-sprites wash (off by default). ~2.5 s reads clearly as
	// a rainbow without strobing — far slower than shakePeriod, so it neither
	// aliases at 60 Hz nor trips photosensitivity the way a fast flash would.
	rainbowSpriteCycle = 2500 * time.Millisecond

	// rainbowSpriteFloor lifts every channel of the hue colour-mod off zero so
	// a saturated hue tints the sprite instead of crushing two channels to a
	// flat silhouette — the character art stays readable under the wash.
	// SetColorMod multiplies (out = texel*mod/255); a floor of 64 keeps each
	// channel in [64,255], i.e. ≥25% of the original survives everywhere.
	rainbowSpriteFloor = 64
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

	// rainbowSprites washes character layers through a cycling hue when set
	// (App mirrors the user pref here once per frame via SetRainbowSprites).
	// rainbowPhase is the accumulated, cycle-bounded clock that drives the
	// hue; both live on the Viewport so the steady-state loop stays alloc-free.
	rainbowSprites bool
	rainbowPhase   time.Duration

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

// SetRainbowSprites toggles the optional rainbow wash over character layers.
// The App mirrors the user preference here once per frame before Update; the
// flag only gates a SetColorMod around the existing blit, so flipping it costs
// nothing and never allocates.
func (v *Viewport) SetRainbowSprites(on bool) { v.rainbowSprites = on }

// Update advances animation clocks against the active scene.
func (v *Viewport) Update(scene *courtroom.Scene, dt time.Duration) {
	// Advance the rainbow-sprite hue clock unconditionally (cheap; the modulo
	// keeps the phase bounded so it never overflows however long the client
	// runs). It is only *read* when the wash is enabled.
	v.rainbowPhase = (v.rainbowPhase + dt) % rainbowSpriteCycle

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

	if scene.PairActive && !scene.SpeakerInFront {
		// Speaker behind: draw speaker first, pair over it.
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, vp)
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp)
	} else if scene.PairActive {
		v.drawSprite(ren, &scene.Pair, &v.pairAnim, vp)
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, vp)
	} else {
		v.drawSprite(ren, &scene.Speaker, &v.speakerAnim, vp)
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
func (v *Viewport) drawSprite(ren *sdl.Renderer, layer *courtroom.SpriteLayer, anim *animState, vp sdl.Rect) {
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
	if v.rainbowSprites {
		// Optional eye-candy: multiply the sprite by a cycling hue. Alpha is
		// untouched, so the transparent cutout stays transparent — it tints
		// the character, it never fills the frame.
		r, g, b := rainbowMod(v.rainbowPhase)
		_ = tex.SetColorMod(r, g, b)
	}
	_ = ren.CopyEx(tex, nil, &v.dstRect, 0, nil, flip)
	if v.rainbowSprites {
		// Restore the neutral mod on this SHARED T1 page: leaving a tint would
		// bleed onto every later user of the same texture (the next frame, the
		// emote preview, the wardrobe grid). Cheap and load-bearing.
		//
		// INVARIANT: drawSprite is the *only* SetColorMod caller in the
		// renderer. That's what actually keeps scenery/preview/wardrobe
		// untinted — this per-draw restore is the backstop. Any future effect
		// that mods a texture elsewhere MUST restore it too, or art bleeds.
		_ = tex.SetColorMod(255, 255, 255)
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

// rainbowMod maps the hue clock to an SDL colour-mod (r,g,b) for the rainbow
// sprite wash. Pure integer math — no allocation, no float, no math import —
// so it is safe on the zero-alloc render path. It walks the six edges of the
// RGB cube (the classic hue wheel) and then lifts each channel off zero by
// rainbowSpriteFloor so the tint never crushes the art to a silhouette.
func rainbowMod(phase time.Duration) (r, g, b uint8) {
	const segments = 6  // six edges of the RGB cube
	const segSpan = 256 // hue ticks per edge
	// Position on the 0..(segments*segSpan) hue ring.
	hue := int64(phase) * (segments * segSpan) / int64(rainbowSpriteCycle)
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
	return liftChannel(rr), liftChannel(gg), liftChannel(bb)
}

// liftChannel maps a 0..255 hue channel into [rainbowSpriteFloor,255] so the
// colour-mod tints the sprite rather than silhouetting it (see the floor's
// rationale at rainbowSpriteFloor).
func liftChannel(c int64) uint8 {
	return uint8(rainbowSpriteFloor + c*(255-rainbowSpriteFloor)/255)
}
