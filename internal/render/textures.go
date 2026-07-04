// Package render owns every SDL resource: window, renderer, textures, fonts,
// and the audio device. Everything in this package must run on the render
// thread (runtime.LockOSThread in main) — spec §17.1. It consumes plain
// data (assets.Decoded, courtroom.Scene) produced by the SDL-free packages.
package render

import (
	"fmt"
	"sync"
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

	// decodeFailTTL backs off a base whose bytes downloaded but failed to
	// DECODE (a corrupt/truncated payload — distinct from a 404, which the
	// network tier already caches). Without it a single bad asset is re-fetched
	// + re-decoded every retry interval, storming the log and the network; the
	// asset can't render regardless, so a long backoff costs nothing and a
	// transient failure still recovers after the window.
	decodeFailTTL = 30 * time.Second
	// decodeFailCap bounds the negative cache (rule §17.4); pruned on insert.
	decodeFailCap = 2048
)

// TexturePage is one decoded asset uploaded to the GPU: all frames plus
// timing metadata.
type TexturePage struct {
	Frames   []*sdl.Texture
	Delays   []time.Duration
	Animated bool
	W, H     int32
	bytes    int64
	// variants holds lazily-built per-pixel transforms of this page (invert /
	// grayscale / the hue-paint colorize) — a transmitted sprite style needs a
	// genuinely transformed texture (SetColorMod can't invert/desaturate). They live
	// HERE so they're destroyed with the base page (eviction frees them too), keeping
	// the cache from leaking or going stale. Bounded by maxVariantPages (variant.go).
	variants map[variantKey]*TexturePage
}

// variantKey identifies one cached per-pixel transform of a page: the effect id plus
// the paint parameters when the effect is parameterised. The classic effects (invert,
// grayscale, the restyles, the silhouette) leave the paint fields zero — their key is
// the effect alone, exactly as before; the hue-paint colorize (variantPaint) keys by
// its pre-quantised colour(s) + split so each painted look is its own cached page.
type variantKey struct {
	effect         uint8
	paintA, paintB uint32 // packed 0xRRGGBB, pre-quantised (variantPaint only)
	split          uint8  // two-tone split row percent (variantPaint only; 0 = one colour)
}

// destroy releases every frame texture and any built variants. Render thread only.
func (p *TexturePage) destroy() {
	for _, t := range p.Frames {
		if t != nil {
			_ = t.Destroy()
		}
	}
	p.Frames = nil
	for k, v := range p.variants {
		v.destroy()
		delete(p.variants, k)
	}
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
	ren *sdl.Renderer
	t1  *cache.ByteBudgetLRU[string, *TexturePage]
	// pinned holds pages exempt from LRU eviction: the active theme's
	// chrome (skin, splashes, bars, buttons, full-screen backdrops).
	// Bounded by the theme stem tables (≈40 pages), render-thread only.
	// Without pinning, themed backdrops churned against streaming
	// sprites in the LRU and the stage flashed black every time an
	// eviction caught them (the gen-keyed UI cache stopped refreshing
	// their recency, making it constant).
	pinned      map[string]*TexturePage
	pinnedBytes int64
	destroy     chan *TexturePage
	generation  atomic.Uint64

	// failed is the decode-failure negative cache: base → last failure time.
	// Written on the render thread (the upload pump) but read off-thread by the
	// manager's prefetch gate, so it carries its own lock.
	failedMu sync.Mutex
	failed   map[string]time.Time

	// budget is the T1 byte budget this store was built with (the default, or
	// the power-user override); error messages and Settings read it.
	budget int64
	// uploadNsEWMA tracks Upload wall time (cold-load profiling; the debug
	// overlay's per-stage line reads it via AvgUpload).
	uploadNsEWMA atomic.Int64
}

// AvgUpload reports the texture-upload wall-time EWMA (zero until the first
// upload) — the upload stage of the cold-load profiling line.
func (s *TextureStore) AvgUpload() time.Duration { return time.Duration(s.uploadNsEWMA.Load()) }

// uploadEWMAWeightDen is the upload EWMA weight (1/4, matching the decode and
// TTFB EWMAs so the three profiling stages average alike).
const uploadEWMAWeightDen = 4

// foldUploadEWMA folds one duration sample into an atomic nanosecond EWMA.
func foldUploadEWMA(dst *atomic.Int64, sample time.Duration) {
	if sample <= 0 {
		return
	}
	old := dst.Load()
	if old == 0 {
		dst.Store(int64(sample))
		return
	}
	dst.Store(old + (int64(sample)-old)/uploadEWMAWeightDen)
}

// MarkFailed records that base failed to DECODE and reports whether this is a
// NEW failure (so the caller logs once per decodeFailTTL, not on every retry).
func (s *TextureStore) MarkFailed(base string) bool {
	now := time.Now()
	s.failedMu.Lock()
	defer s.failedMu.Unlock()
	if s.failed == nil {
		s.failed = make(map[string]time.Time)
	}
	prev, ok := s.failed[base]
	fresh := !ok || now.Sub(prev) >= decodeFailTTL
	if len(s.failed) >= decodeFailCap {
		for k, t := range s.failed { // prune expired first
			if now.Sub(t) >= decodeFailTTL {
				delete(s.failed, k)
			}
		}
		if len(s.failed) >= decodeFailCap {
			clear(s.failed) // pathological flood of distinct bad assets
		}
	}
	s.failed[base] = now
	return fresh
}

// FailedRecently reports whether base failed to decode within decodeFailTTL —
// the manager's prefetch gate skips re-fetching it. Safe from any goroutine.
func (s *TextureStore) FailedRecently(base string) bool {
	if base == "" {
		return false
	}
	s.failedMu.Lock()
	defer s.failedMu.Unlock()
	t, ok := s.failed[base]
	return ok && time.Since(t) < decodeFailTTL
}

// clearFailed drops base from the negative cache (it just uploaded fine, so a
// transient failure recovered).
func (s *TextureStore) clearFailed(base string) {
	s.failedMu.Lock()
	if s.failed != nil {
		delete(s.failed, base)
	}
	s.failedMu.Unlock()
}

// NewTextureStore builds T1 over the given renderer at the default budget.
func NewTextureStore(ren *sdl.Renderer) (*TextureStore, error) {
	return NewTextureStoreBudget(ren, int64(T1BudgetBytes))
}

// NewTextureStoreBudget is NewTextureStore with a power-user T1 byte budget
// (≤ 0 = the default). BOOT-applied by design: resizing a live LRU would be an
// eviction storm mid-session — the Settings row says "applies on restart".
func NewTextureStoreBudget(ren *sdl.Renderer, budgetBytes int64) (*TextureStore, error) {
	if budgetBytes <= 0 {
		budgetBytes = int64(T1BudgetBytes)
	}
	s := &TextureStore{
		ren:     ren,
		pinned:  map[string]*TexturePage{},
		destroy: make(chan *TexturePage, destroyQueueCap),
		budget:  budgetBytes,
	}
	t1, err := cache.NewByteBudgetLRU(t1MaxEntries, budgetBytes, func(_ string, page *TexturePage, _ int64) {
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

// Get returns the page for base (pinned pages first), bumping recency for
// LRU-resident ones. Render thread only.
func (s *TextureStore) Get(base string) (*TexturePage, bool) {
	if page, ok := s.pinned[base]; ok {
		return page, true
	}
	return s.t1.Get(base)
}

// buildPage turns a decoded asset into a texture page (shared by the LRU
// and pinned upload paths). The Decoded is NOT released here.
func (s *TextureStore) buildPage(d *assets.Decoded) (*TexturePage, error) {
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
			return nil, err
		}
		if err := tex.Update(nil, unsafe.Pointer(&frame.Pix[0]), frame.Stride); err != nil {
			_ = tex.Destroy()
			page.destroy()
			return nil, err
		}
		_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
		page.Frames = append(page.Frames, tex)
		page.bytes += int64(len(frame.Pix))
	}
	return page, nil
}

// Upload turns a decoded asset into textures under the asset's base key.
// Render thread only. The Decoded is released here.
func (s *TextureStore) Upload(base string, d *assets.Decoded) error {
	defer d.Release()
	start := time.Now()
	page, err := s.buildPage(d)
	if err != nil {
		return err
	}
	foldUploadEWMA(&s.uploadNsEWMA, time.Since(start)) // cold-load profiling: GPU-upload stage
	if !s.t1.Add(base, page, page.bytes) {
		// Bigger than the entire T1 budget: the LRU refuses it, and before
		// this check the freshly created textures leaked silently — sprites
		// of that size simply never appeared. The decode-side cap
		// (assets.maxDecodedAssetBytes) keeps this branch unreachable for
		// well-formed assets; pathological ones get a loud error instead.
		page.destroy()
		return fmt.Errorf("render: %s decoded to %d bytes, above the %d-byte T1 budget", base, page.bytes, s.budget)
	}
	s.generation.Add(1)
	s.clearFailed(base) // it decoded fine — drop any stale negative-cache entry
	return nil
}

// UploadPinned is Upload into the eviction-exempt tier (theme chrome:
// losing the courtroom backdrop to LRU pressure flashed the stage black).
// Replacing an existing pinned page routes the old one through the
// destroy queue. Render thread only.
func (s *TextureStore) UploadPinned(base string, d *assets.Decoded) error {
	defer d.Release()
	page, err := s.buildPage(d)
	if err != nil {
		return err
	}
	if old, ok := s.pinned[base]; ok {
		s.pinnedBytes -= old.bytes
		s.queueDestroy(old)
	}
	s.pinned[base] = page
	s.pinnedBytes += page.bytes
	s.generation.Add(1)
	return nil
}

// Remove evicts one page from whichever tier holds it (theme swaps
// replace skin textures). Render thread only.
func (s *TextureStore) Remove(base string) {
	if page, ok := s.pinned[base]; ok {
		delete(s.pinned, base)
		s.pinnedBytes -= page.bytes
		s.queueDestroy(page)
		s.generation.Add(1)
		return
	}
	s.t1.Remove(base)
}

// queueDestroy hands a page to the bounded destroy queue (inline when
// full — render thread, legal and bounded).
func (s *TextureStore) queueDestroy(page *TexturePage) {
	select {
	case s.destroy <- page:
	default:
		page.destroy()
	}
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

// Purge destroys everything — pinned pages included (server switch /
// shutdown / filtering swap; the theme re-applies after). Render thread
// only.
func (s *TextureStore) Purge() {
	s.generation.Add(1)
	for base, page := range s.pinned {
		page.destroy()
		delete(s.pinned, base)
	}
	s.pinnedBytes = 0
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
