package render

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	// offsetPercentDivisor converts AO offsets (percent) into pixels.
	offsetPercentDivisor = 100
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

// Update advances animation clocks against the active scene.
func (v *Viewport) Update(scene *courtroom.Scene, dt time.Duration) {
	v.syncAnim(&v.bgAnim, scene.BackgroundBase)
	v.syncAnim(&v.deskAnim, scene.DeskBase)
	v.syncAnim(&v.shoutAnim, scene.ShoutBase)
	v.syncAnim(&v.speakerAnim, scene.Speaker.Active)
	v.syncAnim(&v.pairAnim, scene.Pair.Active)

	if page, ok := v.bgAnim.resolve(v.store); ok {
		v.bgAnim.advance(page, dt, false)
	}
	if page, ok := v.deskAnim.resolve(v.store); ok {
		v.deskAnim.advance(page, dt, false)
	}
	if scene.ShoutBase != "" {
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

// Render draws the scene layers in AO order: background → characters (pair
// z-order) → desk → shout bubble. Chat box and text render UI-side.
func (v *Viewport) Render(ren *sdl.Renderer, scene *courtroom.Scene, vp sdl.Rect) {
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
	if scene.ShoutBase != "" {
		v.drawFill(ren, scene.ShoutBase, &v.shoutAnim, vp)
	}
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
	_ = ren.CopyEx(page.Frames[frame], nil, &v.dstRect, 0, nil, flip)
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
