// Package render owns every SDL resource: window, renderer, textures, fonts,
// and the audio device. Everything in this package must run on the render
// thread (runtime.LockOSThread in main) — spec §17.1. It consumes plain
// data (assets.Decoded, courtroom.Scene) produced by the SDL-free packages.
package render

import (
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
)

const (
	// T1BudgetBytes bounds decoded texture payload bytes (spec §9).
	T1BudgetBytes = cache.DefaultT1BudgetBytes
	t1MaxEntries  = cache.DefaultMaxEntries

	// Speculative upload budget per frame: live-message assets bypass it,
	// prefetch-ahead uploads stop after this many textures or bytes
	// (spec §8). The BYTE cap is what protects 16 ms; the texture count
	// exists so a burst of tiny uploads stays bounded too. At the old
	// count of 2, a 600-icon char-select viewport took ~5 s to fill even
	// with every payload already decoded (icons are ~16 KB thumbnails);
	// 16 small Updates cost tens of µs while big sprites still gate on
	// bytes long before the count.
	speculativeUploadMaxTextures = 16
	speculativeUploadMaxBytes    = 4 << 20

	// destroyQueueCap bounds the texture destroy queue drained each frame.
	destroyQueueCap = 256
	// destroyBudgetPerFrame caps destroys per frame to keep 16 ms.
	destroyBudgetPerFrame = 16
)

// TexturePage is one decoded asset uploaded to the GPU: all frames plus
// timing metadata.
type TexturePage struct {
	Frames   []*sdl.Texture
	Delays   []time.Duration
	Animated bool
	W, H     int32
	bytes    int64
}

// destroy releases every frame texture. Render thread only.
func (p *TexturePage) destroy() {
	for _, t := range p.Frames {
		if t != nil {
			_ = t.Destroy()
		}
	}
	p.Frames = nil
}

// TextureStore is T1: a byte-budgeted texture cache keyed by asset BASE
// (extension-less URL base — unique per asset). Evicted pages drain through
// a bounded destroy queue on the render thread.
//
// generation increments on every mutation (upload, eviction, purge). The
// viewport caches *TexturePage pointers against it, so steady-state frames
// reuse pages with zero LRU lookups; any mutation forces a re-lookup before
// a cached pointer can dangle (destroys only happen later in the same frame,
// in DrainDestroyQueue, after rendering consulted the generation).
type TextureStore struct {
	ren        *sdl.Renderer
	t1         *cache.ByteBudgetLRU[string, *TexturePage]
	destroy    chan *TexturePage
	generation atomic.Uint64
}

// NewTextureStore builds T1 over the given renderer.
func NewTextureStore(ren *sdl.Renderer) (*TextureStore, error) {
	s := &TextureStore{
		ren:     ren,
		destroy: make(chan *TexturePage, destroyQueueCap),
	}
	t1, err := cache.NewByteBudgetLRU(t1MaxEntries, int64(T1BudgetBytes), func(_ string, page *TexturePage, _ int64) {
		s.generation.Add(1) // cached page pointers must re-resolve
		select {
		case s.destroy <- page:
		default:
			// Queue full: we are on the render thread (every T1 mutation
			// happens here), destroying inline is legal and bounded.
			page.destroy()
		}
	})
	if err != nil {
		return nil, err
	}
	s.t1 = t1
	return s, nil
}

// Contains reports whether a texture page exists for the asset base. Safe
// to call from any goroutine (the inner LRU is thread-safe) — wired as the
// manager's T1 probe.
func (s *TextureStore) Contains(base string) bool {
	return s.t1.Contains(base)
}

// Get returns the page for base, bumping recency.
func (s *TextureStore) Get(base string) (*TexturePage, bool) {
	return s.t1.Get(base)
}

// Upload turns a decoded asset into textures under the asset's base key.
// Render thread only. The Decoded is released here.
func (s *TextureStore) Upload(base string, d *assets.Decoded) error {
	defer d.Release()
	page := &TexturePage{
		Delays:   append([]time.Duration(nil), d.Delays...),
		Animated: d.Animated,
		W:        int32(d.Width),
		H:        int32(d.Height),
	}
	for _, frame := range d.Frames {
		tex, err := s.ren.CreateTexture(
			uint32(sdl.PIXELFORMAT_ABGR8888), // image.RGBA byte order
			sdl.TEXTUREACCESS_STATIC,
			int32(d.Width), int32(d.Height),
		)
		if err != nil {
			page.destroy()
			return err
		}
		if err := tex.Update(nil, unsafe.Pointer(&frame.Pix[0]), frame.Stride); err != nil {
			_ = tex.Destroy()
			page.destroy()
			return err
		}
		_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
		page.Frames = append(page.Frames, tex)
		page.bytes += int64(len(frame.Pix))
	}
	if !s.t1.Add(base, page, page.bytes) {
		// Bigger than the entire T1 budget: the LRU refuses it, and before
		// this check the freshly created textures leaked silently — sprites
		// of that size simply never appeared. The decode-side cap
		// (assets.maxDecodedAssetBytes) keeps this branch unreachable for
		// well-formed assets; pathological ones get a loud error instead.
		page.destroy()
		return fmt.Errorf("render: %s decoded to %d bytes, above the %d-byte T1 budget", base, page.bytes, int64(T1BudgetBytes))
	}
	s.generation.Add(1)
	return nil
}

// DrainDestroyQueue destroys up to destroyBudgetPerFrame evicted pages.
// Render thread only; call once per frame (spec §12).
func (s *TextureStore) DrainDestroyQueue() {
	for i := 0; i < destroyBudgetPerFrame; i++ {
		select {
		case page := <-s.destroy:
			page.destroy()
		default:
			return
		}
	}
}

// Generation reports the mutation counter backing cached page pointers.
func (s *TextureStore) Generation() uint64 {
	return s.generation.Load()
}

// Stats exposes T1 counters for the HUD.
func (s *TextureStore) Stats() cache.MemoryStats {
	return s.t1.Stats()
}

// Purge destroys everything (server switch / shutdown). Render thread only.
func (s *TextureStore) Purge() {
	s.generation.Add(1)
	s.t1.Purge()
	for {
		select {
		case page := <-s.destroy:
			page.destroy()
		default:
			return
		}
	}
}
