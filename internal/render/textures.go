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

	// The small-UI texture shield: emote buttons and char icons live in their
	// own byte-budgeted LRU CARVED OUT of the configured T1 budget, so a burst
	// of multi-MiB sprite uploads can never evict them. They are decode-capped
	// tiny (40/64 px — KBs each), they back ALWAYS-VISIBLE grids, and their
	// recency in the LRU is structurally stale (the UI's generation-keyed page
	// caches skip Get() on steady frames — the whole point of that cache), so
	// under the old single tier every big streaming burst swept them out and
	// the next drawn frame flashed the grid back to fallbacks while they
	// re-streamed: the "emote buttons visibly refresh/redraw" playtest report.
	// The same churn in the other direction is also gone: a 4000-icon
	// char-select scroll now competes only with other small textures, never
	// evicting the sprites on stage. (The stage layers and theme chrome grew
	// their own protections earlier — keepSceneAssetsWarm, the pinned tier;
	// this closes the gap for the button/icon grids.)
	smallTexBudgetDiv = 8       // shield budget = T1 budget / 8 (8 MiB at the 64 MiB default ≈ >1000 buttons)
	smallTexMinBudget = 4 << 20 // floor so tiny power-user T1 budgets keep a useful shield
)

// smallTexTier reports whether an asset type belongs in the small-UI shield.
// Exactly the decode-thumbnailed types (decoder.go decodeTargetPx): both are
// guaranteed small per frame, so the shield can never be flooded by full-size
// art wearing the wrong label.
func smallTexTier(t assets.AssetType) bool {
	return t == assets.AssetTypeCharIcon || t == assets.AssetTypeEmoteButton
}

// splitT1Budget carves the small-texture shield out of the configured T1
// budget (total residency stays exactly the configured budget). The floor
// keeps a useful shield under tiny power-user budgets; the half-cap keeps the
// floor from starving the main tier in the same situation.
func splitT1Budget(budget int64) (main, small int64) {
	small = budget / smallTexBudgetDiv
	if small < smallTexMinBudget {
		small = smallTexMinBudget
	}
	if small > budget/2 {
		small = budget / 2
	}
	return budget - small, small
}

const (
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
// a bounded destroy queue on the render thread. Three tiers share the one
// configured budget: the main LRU (sprites, backgrounds, everything big),
// the small-UI shield LRU (emote buttons + char icons, immune to sprite
// churn — see smallTexBudgetDiv), and the pinned map (theme chrome). A base
// lives in exactly one tier: the pump routes uploads by asset type, and
// theme keys use their own scheme.
//
// generation increments on every mutation (upload, eviction, purge — either
// LRU). The viewport caches *TexturePage pointers against it, so steady-state
// frames reuse pages with zero LRU lookups; any mutation forces a re-lookup
// before a cached pointer can dangle (destroys only happen later in the same
// frame, in DrainDestroyQueue, after rendering consulted the generation).
type TextureStore struct {
	ren *sdl.Renderer
	t1  *cache.ByteBudgetLRU[string, *TexturePage]
	// small is the UI shield tier (emote buttons, char icons): its own byte
	// budget carved from the T1 total, so streaming sprites can never evict
	// the button/icon grids (and icon floods never evict sprites).
	small *cache.ByteBudgetLRU[string, *TexturePage]
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

	// Held-frame bridge (the black-flash fix): when the LRU must evict the
	// ON-SCREEN background/desk — a cap-sized incoming sprite page can force it,
	// since one page may be half the budget while the main tier is 7/8 of it —
	// onEvict STEALS the page's first frame into the pinned map under a
	// HeldKeyPrefix key instead of letting the stage draw the black clear color
	// until the heal re-decodes. liveScenery is the injected "is this base the
	// drawn bg/desk" probe (nil = bridge off; render thread only, like every
	// mutation here). heldKeys is a fixed ring bounding the bridge to
	// heldSceneryMax stolen frames; purging suppresses the steal during Purge
	// (the tier purges fire onEvict per entry AFTER the pinned map was cleared —
	// stealing there would leak held pages past the purge).
	liveScenery func(base string) bool
	heldKeys    [heldSceneryMax]string
	heldNext    int
	purging     bool

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
	// Both LRU tiers share one eviction path: bump the generation (cached
	// page pointers must re-resolve) and route the page through the bounded
	// destroy queue. The held-frame bridge intercepts live scenery first —
	// mutating s.pinned here is legal (it is store-owned, not the inner LRU,
	// so the "no call back into the cache" contract holds).
	onEvict := func(base string, page *TexturePage, _ int64) {
		s.generation.Add(1)
		if !s.purging && s.liveScenery != nil && s.liveScenery(base) {
			s.holdSceneryFrame(base, page)
		}
		select {
		case s.destroy <- page:
		default:
			// Queue full: we are on the render thread (every T1 mutation
			// happens here), destroying inline is legal and bounded.
			page.destroy()
		}
	}
	mainBudget, smallBudget := splitT1Budget(budgetBytes)
	t1, err := cache.NewByteBudgetLRU(t1MaxEntries, mainBudget, onEvict)
	if err != nil {
		return nil, err
	}
	small, err := cache.NewByteBudgetLRU(t1MaxEntries, smallBudget, onEvict)
	if err != nil {
		return nil, err
	}
	s.t1 = t1
	s.small = small
	return s, nil
}

// HeldKeyPrefix namespaces held-frame bridge pages in the pinned map (like
// theme:// and thumb:// — a scheme prefix can never collide with an asset URL
// base). The viewport's scenery miss path probes HeldKeyPrefix+base.
const HeldKeyPrefix = "held://"

// heldSceneryMax bounds the held-frame bridge: at most this many stolen
// scenery frames (the on-screen background + desk) live in the pinned map at
// once — a fixed ring, oldest slot reused.
const heldSceneryMax = 2

// texBytesPerPixel is the byte cost of one ABGR8888 texel (the only texture
// format the store creates) — sizes the held single-frame page.
const texBytesPerPixel = 4

// SetLiveScenery injects the "is this base the on-screen background/desk"
// probe backing the held-frame bridge (nil = bridge off). Render thread only,
// set once at boot.
func (s *TextureStore) SetLiveScenery(f func(base string) bool) { s.liveScenery = f }

// holdSceneryFrame steals an evicted live-scenery page's FIRST frame into the
// eviction-exempt pinned map under HeldKeyPrefix+base, so the stage draws a
// frozen background instead of the black clear color while the heal
// re-decodes (or forever, if the scene is genuinely over budget and the heal
// latch has gone quiet). Zero decode, zero copy, zero upload — the texture
// already exists; destroy() skips the nil'd slot. Render thread only (called
// from onEvict). Bounded by the heldSceneryMax ring; released by releaseHeld
// the moment the real page re-uploads.
func (s *TextureStore) holdSceneryFrame(base string, page *TexturePage) {
	if len(page.Frames) == 0 || page.Frames[0] == nil {
		return
	}
	held := &TexturePage{
		Frames: []*sdl.Texture{page.Frames[0]},
		Delays: []time.Duration{0}, // a single frozen frame — never scheduled
		W:      page.W,
		H:      page.H,
		bytes:  int64(page.W) * int64(page.H) * texBytesPerPixel,
	}
	page.Frames[0] = nil // stolen: the page's destroy() skips nil frames
	key := HeldKeyPrefix + base
	if old, ok := s.pinned[key]; ok {
		// Re-steal for the same base: replace in place (ring slot already ours).
		s.pinnedBytes -= old.bytes
		s.queueDestroy(old)
	} else {
		// Claim the next ring slot, releasing its previous occupant.
		if prev := s.heldKeys[s.heldNext]; prev != "" {
			if op, ok := s.pinned[prev]; ok {
				s.pinnedBytes -= op.bytes
				s.queueDestroy(op)
				delete(s.pinned, prev)
			}
		}
		s.heldKeys[s.heldNext] = key
		s.heldNext = (s.heldNext + 1) % heldSceneryMax
	}
	s.pinned[key] = held
	s.pinnedBytes += held.bytes
}

// releaseHeld drops the held-frame bridge page for base once its real page is
// resident again (called from uploadTier after a successful Add — the Add
// already bumped the generation, so cached pointers re-resolve to the real
// page). Ring slot cleared so a stale key can't evict an unrelated hold.
func (s *TextureStore) releaseHeld(base string) {
	key := HeldKeyPrefix + base
	page, ok := s.pinned[key]
	if !ok {
		return
	}
	delete(s.pinned, key)
	s.pinnedBytes -= page.bytes
	s.queueDestroy(page)
	for i := range s.heldKeys {
		if s.heldKeys[i] == key {
			s.heldKeys[i] = ""
		}
	}
}

// Contains reports whether a texture page exists for the asset base in
// either LRU tier. Safe to call from any goroutine (the inner LRUs are
// thread-safe) — wired as the manager's T1 probe.
func (s *TextureStore) Contains(base string) bool {
	return s.small.Contains(base) || s.t1.Contains(base)
}

// Get returns the page for base (pinned pages first, then the small-UI
// shield, then the main tier), bumping recency for LRU-resident ones.
// Render thread only.
func (s *TextureStore) Get(base string) (*TexturePage, bool) {
	if page, ok := s.pinned[base]; ok {
		return page, true
	}
	if page, ok := s.small.Get(base); ok {
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
	return s.uploadTier(base, d, s.t1)
}

// UploadSmall is Upload into the small-UI shield tier (emote buttons, char
// icons — see smallTexBudgetDiv). The pump routes by asset type. A page too
// big for the shield's whole budget (a pathological many-frame animated
// icon) falls through to the MAIN tier instead of being refused: it still
// shows, it just doesn't get churn immunity. Render thread only.
func (s *TextureStore) UploadSmall(base string, d *assets.Decoded) error {
	if d.PixelBytes() > s.small.Budget() {
		return s.uploadTier(base, d, s.t1)
	}
	// The tiers are alternatives for a key, never both: drop a stale main-tier
	// page from before this asset's type routed here (belt-and-braces — a
	// base's type never actually changes at runtime).
	s.t1.Remove(base)
	return s.uploadTier(base, d, s.small)
}

// uploadTier builds and registers a page in the given LRU tier (shared by
// Upload and UploadSmall). The Decoded is released here.
func (s *TextureStore) uploadTier(base string, d *assets.Decoded, tier *cache.ByteBudgetLRU[string, *TexturePage]) error {
	defer d.Release()
	start := time.Now()
	page, err := s.buildPage(d)
	if err != nil {
		return err
	}
	foldUploadEWMA(&s.uploadNsEWMA, time.Since(start)) // cold-load profiling: GPU-upload stage
	if !tier.Add(base, page, page.bytes) {
		// Bigger than the entire tier budget: the LRU refuses it, and before
		// this check the freshly created textures leaked silently — sprites
		// of that size simply never appeared. The decode-side cap
		// (assets.maxDecodedAssetBytes) keeps this branch unreachable for
		// well-formed assets; pathological ones get a loud error instead.
		page.destroy()
		return fmt.Errorf("render: %s decoded to %d bytes, above the tier's %d-byte share of the T1 budget", base, page.bytes, tier.Budget())
	}
	s.generation.Add(1)
	s.clearFailed(base) // it decoded fine — drop any stale negative-cache entry
	s.releaseHeld(base) // the real page is back: drop the held-frame bridge
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
	// A key lives in at most one LRU tier; Remove on the other is a no-op.
	s.small.Remove(base)
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

// Stats exposes T1 counters for the HUD: both LRU tiers aggregated, so the
// readout keeps describing "decoded textures vs the configured budget".
func (s *TextureStore) Stats() cache.MemoryStats {
	main, small := s.t1.Stats(), s.small.Stats()
	return cache.MemoryStats{
		Hits:      main.Hits + small.Hits,
		Misses:    main.Misses + small.Misses,
		Evictions: main.Evictions + small.Evictions,
		Entries:   main.Entries + small.Entries,
		Bytes:     main.Bytes + small.Bytes,
		Budget:    main.Budget + small.Budget, // = the configured T1 budget (splitT1Budget carves, never adds)
	}
}

// Purge destroys everything — pinned pages included (server switch /
// shutdown / filtering swap; the theme re-applies after). Render thread
// only.
func (s *TextureStore) Purge() {
	// The tier purges below fire onEvict per entry AFTER the pinned map is
	// cleared — the held-frame steal must stay out of the way or held pages
	// would be re-created mid-purge and leak past it.
	s.purging = true
	defer func() { s.purging = false }()
	s.generation.Add(1)
	for base, page := range s.pinned {
		page.destroy()
		delete(s.pinned, base)
	}
	s.pinnedBytes = 0
	s.heldKeys = [heldSceneryMax]string{}
	s.heldNext = 0
	s.small.Purge()
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
