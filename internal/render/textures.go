// Package render owns every SDL resource; everything in it must run on the render thread (hard rule 1).
package render

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// T1BudgetBytes bounds decoded texture payload bytes.
	T1BudgetBytes = cache.DefaultT1BudgetBytes
	t1MaxEntries  = cache.DefaultMaxEntries

	// Speculative uploads per frame: bytes protect 16 ms, the count bounds tiny bursts; live assets bypass both.
	speculativeUploadMaxTextures = 16
	speculativeUploadMaxBytes    = 4 << 20

	destroyQueueCap = 256
	// destroyBudgetPerFrame caps destroys per frame to keep 16 ms.
	destroyBudgetPerFrame = 16

	// Small UI textures get their own LRU: the UI's page caches skip Get, so their recency goes stale.
)

// smallTexTier reports decode-thumbnailed types (decodeTargetPx), so full-size art can't flood the tier.
func smallTexTier(t assets.AssetType) bool {
	return t == assets.AssetTypeCharIcon || t == assets.AssetTypeEmoteButton
}

// splitT1Budget carves the small tier using the cache package's arithmetic, shared with the decode cap.
func splitT1Budget(budget int64) (main, small int64) {
	return cache.MainTierBytes(budget), cache.SmallTierBytes(budget)
}

const (
	// decodeFailTTL backs off a base that failed to decode, so one bad asset isn't re-fetched every retry.
	decodeFailTTL = 30 * time.Second
	// decodeFailCap bounds the negative cache; pruned on insert.
	decodeFailCap = 2048
)

// TexturePage is one decoded asset on the GPU: all frames plus timing.
type TexturePage struct {
	Frames   []*sdl.Texture
	Delays   []time.Duration
	Animated bool
	// Partial marks a first frame the full set will replace: playOnce must not complete on it, and swaps keep holding.
	Partial bool
	// SourceFrames is the pre-decimation frame count, the space networked frame effects index; 0 means len(Frames).
	SourceFrames int
	// Stilled marks a page reduced to frame 0: ContainsAnimated treats it as absent, Get still draws it.
	Stilled bool
	W, H    int32
	bytes   int64
	// scaleMode/scaleModeSet memoise the filter, as the cgo call and flush must not run per frame per draw.
	scaleMode    sdl.ScaleMode
	scaleModeSet bool
	// variants holds lazily built per-pixel transforms, destroyed with the page and bounded by maxVariantPages.
	variants map[variantKey]*TexturePage
}

// variantKey identifies a cached transform: the effect, plus the paint parameters for variantPaint (zero otherwise).
type variantKey struct {
	effect         uint8
	paintA, paintB uint32 // packed 0xRRGGBB, pre-quantised (variantPaint only)
	split          uint8  // two-tone split row percent (variantPaint only; 0 = one colour)
}

// destroy releases every frame texture and variant; render thread only.
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

// reduceToStill destroys all frames but the first; the caller owns the byte accounting. Render thread only.
func (p *TexturePage) reduceToStill() {
	n := len(p.Frames)
	if n <= 1 {
		return
	}
	// Frames of one animation are equal-size, so this holds for both raw RGBA and DXT.
	frameBytes := p.bytes / int64(n)
	if frameBytes <= 0 {
		frameBytes = int64(p.W) * int64(p.H) * texBytesPerPixel
	}
	for _, t := range p.Frames[1:] {
		if t != nil {
			_ = t.Destroy()
		}
	}
	p.Frames = p.Frames[:1]
	if len(p.Delays) > 1 {
		p.Delays = p.Delays[:1]
	}
	p.Stilled = true
	p.bytes = frameBytes
}

// TextureStore is T1, keyed by asset base; a base lives in one of the main, small, pinned or oversized tiers.
type TextureStore struct {
	ren *sdl.Renderer
	t1  *cache.ByteBudgetLRU[string, *TexturePage]
	// small is the UI tier: sprites can't evict button and icon grids, and icon floods can't evict sprites.
	small *cache.ByteBudgetLRU[string, *TexturePage]
	// pinned holds LRU-exempt pages (theme chrome), bounded by the theme stem tables; render thread only.
	pinned      map[string]*TexturePage
	pinnedBytes int64
	// oversized holds animated pages too large for the main tier, bounded by oversizedCap against hostile servers.
	// oversizedMu: Contains runs off the render thread and must see them, or a resident animation is re-fetched.
	oversizedMu    sync.RWMutex
	oversized      map[string]*TexturePage
	oversizedBytes int64
	oversizedCap   int64
	oversizedOrder []string // insertion order, oldest first, for cap eviction
	// themeMediaBytes/Count and themeGenCount count the user's art in pinned; client chrome doesn't count.
	themeMediaBytes int64
	themeMediaCount int
	themeGenCount   int
	destroy         chan *TexturePage
	generation      atomic.Uint64

	// Held frames: when the LRU evicts on-screen scenery, onEvict pins its first frame, so the stage doesn't go black.
	// liveScenery is the "is this base drawn" probe (nil = off); heldKeys bounds the frames to heldSceneryMax.
	// purging suppresses this during Purge: tier purges fire onEvict after pinned clears, leaking held pages.
	liveScenery func(base string) bool
	heldKeys    [heldSceneryMax]string
	heldNext    int
	purging     bool
	// heldSteals/heldReleases are debug counters; atomic so the panel read can't race, and they never affect behaviour.
	heldSteals   atomic.Int64
	heldReleases atomic.Int64

	// failed is the decode-failure cache; the render thread writes it and the prefetch check reads it off-thread.
	failedMu sync.Mutex
	failed   map[string]time.Time

	// missing holds char sprite bases whose whole fallback chain 404'd; render thread only, so no lock.
	missing map[string]struct{}

	// budget is the T1 byte budget this store was built with.
	budget int64
	// uploadNsEWMA tracks Upload wall time for the debug overlay.
	uploadNsEWMA atomic.Int64
}

// AvgUpload reports the texture-upload wall-time EWMA, zero until the first upload.
func (s *TextureStore) AvgUpload() time.Duration { return time.Duration(s.uploadNsEWMA.Load()) }

// uploadEWMAWeightDen is 1/4, matching the decode and TTFB EWMAs so the profiling stages average alike.
const uploadEWMAWeightDen = 4

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

// MarkFailed records a decode failure and reports whether it is new, so the caller logs once per decodeFailTTL.
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

// FailedRecently reports whether base failed to decode within decodeFailTTL; safe from any goroutine.
func (s *TextureStore) FailedRecently(base string) bool {
	if base == "" {
		return false
	}
	s.failedMu.Lock()
	defer s.failedMu.Unlock()
	t, ok := s.failed[base]
	return ok && time.Since(t) < decodeFailTTL
}

// clearFailed drops base from the negative cache after a good upload.
func (s *TextureStore) clearFailed(base string) {
	s.failedMu.Lock()
	if s.failed != nil {
		delete(s.failed, base)
	}
	s.failedMu.Unlock()
}

// NewTextureStoreBudget builds T1 (<= 0 = default); boot only, as resizing a live LRU is an eviction storm.
func NewTextureStoreBudget(ren *sdl.Renderer, budgetBytes int64) (*TextureStore, error) {
	if budgetBytes <= 0 {
		budgetBytes = int64(T1BudgetBytes)
	}
	s := &TextureStore{
		ren:          ren,
		pinned:       map[string]*TexturePage{},
		oversized:    map[string]*TexturePage{},
		oversizedCap: cache.DefaultOversizedTextureBytes,
		destroy:      make(chan *TexturePage, destroyQueueCap),
		budget:       budgetBytes,
	}
	// Eviction bumps the generation so cached pointers re-resolve; mutating s.pinned is legal, as it isn't the LRU.
	onEvict := func(base string, page *TexturePage, _ int64) {
		s.generation.Add(1)
		if !s.purging && s.liveScenery != nil && s.liveScenery(base) {
			s.holdSceneryFrame(base, page)
		}
		select {
		case s.destroy <- page:
		default:
			// Queue full: every T1 mutation is on the render thread, so destroying inline is legal and bounded.
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

// HeldKeyPrefix namespaces held pages in pinned; a scheme prefix can't collide with an asset URL.
const HeldKeyPrefix = "held://"

// MissingKey is the one shared key of the missing-sprite placeholder, so drawSprite's miss path builds no string.
const MissingKey = "missingno://"

// missingSetCap bounds the set, mirroring courtroom.missingSpritesCap; a miss when full takes the normal path.
const missingSetCap = 512

// MarkMissing records that base's whole fallback chain 404'd, stopping inserts when full; render thread only.
func (s *TextureStore) MarkMissing(base string) {
	if base == "" {
		return
	}
	if s.missing == nil {
		s.missing = make(map[string]struct{})
	}
	if _, ok := s.missing[base]; ok {
		return
	}
	if len(s.missing) >= missingSetCap {
		return // full: the un-recorded base falls through to hold-previous/blank
	}
	s.missing[base] = struct{}{}
}

// IsMissing reports whether base was conclusively 404'd this session; render thread only.
func (s *TextureStore) IsMissing(base string) bool {
	if len(s.missing) == 0 {
		return false
	}
	_, ok := s.missing[base]
	return ok
}

// clearMissing drops base from the missing set after its real page uploads; render thread only.
func (s *TextureStore) clearMissing(base string) {
	if s.missing != nil {
		delete(s.missing, base)
	}
}

// heldSceneryMax bounds held frames to background, desk, speaker and pair; pinned until the real page returns.
const heldSceneryMax = 4

// texBytesPerPixel is one ABGR8888 texel, the only format the store creates.
const texBytesPerPixel = 4

const mibShift = 20

const dxtBlockPx = 4

// SetLiveScenery sets the "is this base on screen" probe for held frames (nil = off); render thread only, once at boot.
func (s *TextureStore) SetLiveScenery(f func(base string) bool) { s.liveScenery = f }

// holdSceneryFrame pins an evicted page's first frame, reusing the texture; destroy skips the nil entry.
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
		// Same base again: replace in place.
		s.pinnedBytes -= old.bytes
		s.queueDestroy(old)
	} else {
		// Take the next ring entry, releasing its previous occupant.
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
	s.heldSteals.Add(1)
}

// releaseHeld drops base's held page once its real page is resident; a stale key can't evict another hold.
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
	s.heldReleases.Add(1)
}

// HeldStats is a snapshot of the held-frame counters; Current is the number pinned now.
type HeldStats struct {
	Steals   int64 // stage frames stolen into the pinned bridge on eviction
	Releases int64 // holds released when the real page re-uploaded
	Current  int64 // held frames pinned right now (≤ heldSceneryMax)
}

// HeldStats snapshots the counters; render thread only, because it reads the heldKeys ring.
func (s *TextureStore) HeldStats() HeldStats {
	current := int64(0)
	for i := range s.heldKeys {
		if s.heldKeys[i] != "" {
			current++
		}
	}
	return HeldStats{
		Steals:   s.heldSteals.Load(),
		Releases: s.heldReleases.Load(),
		Current:  current,
	}
}

// Contains reports whether a page exists in either LRU or the oversized map; safe from any goroutine.
func (s *TextureStore) Contains(base string) bool {
	if s.small.Contains(base) || s.t1.Contains(base) {
		return true
	}
	s.oversizedMu.RLock()
	_, ok := s.oversized[base]
	s.oversizedMu.RUnlock()
	return ok
}

// ContainsAnimated is Contains with Stilled pages absent, so a reduced page re-streams.
func (s *TextureStore) ContainsAnimated(base string) bool {
	if page, ok := s.small.Peek(base); ok {
		return !page.Stilled
	}
	if page, ok := s.t1.Peek(base); ok {
		return !page.Stilled
	}
	s.oversizedMu.RLock()
	page, ok := s.oversized[base]
	s.oversizedMu.RUnlock()
	return ok && !page.Stilled
}

// ReduceAnimatedExcept reduces animated pages not in active to one frame; they re-stream when active.
func (s *TextureStore) ReduceAnimatedExcept(active map[string]bool) int {
	reduced := 0

	s.oversizedMu.Lock()
	for base, page := range s.oversized {
		if active[base] || !page.Animated || page.Stilled || len(page.Frames) <= 1 {
			continue
		}
		before := page.bytes
		page.reduceToStill()
		s.oversizedBytes += page.bytes - before
		reduced++
	}
	s.oversizedMu.Unlock()

	for _, base := range s.t1.Keys() {
		if active[base] {
			continue
		}
		page, ok := s.t1.Peek(base)
		if !ok || !page.Animated || page.Stilled || len(page.Frames) <= 1 {
			continue
		}
		before := page.bytes
		page.reduceToStill()
		s.t1.Resize(base, page.bytes)
		if page.bytes != before {
			reduced++
		}
	}
	return reduced
}

// Get returns the page, checking pinned, oversized, small then main; render thread only.
func (s *TextureStore) Get(base string) (*TexturePage, bool) {
	if page, ok := s.pinned[base]; ok {
		return page, true
	}
	s.oversizedMu.RLock()
	page, ok := s.oversized[base]
	s.oversizedMu.RUnlock()
	if ok {
		return page, true
	}
	if page, ok := s.small.Get(base); ok {
		return page, true
	}
	return s.t1.Get(base)
}

// buildPage turns a decoded asset into a page; the Decoded is not released here.
func (s *TextureStore) buildPage(d *assets.Decoded) (*TexturePage, error) {
	page := &TexturePage{
		Delays:       append([]time.Duration(nil), d.Delays...),
		Animated:     d.Animated,
		Partial:      d.Partial,      // progressive first-frame delivery (full set follows)
		SourceFrames: d.SourceFrames, // carry the un-decimated frame count for #17 frame-effect mapping
		W:            int32(d.Width),
		H:            int32(d.Height),
	}
	frames, bytes, err := s.buildFrames(d)
	if err != nil {
		page.destroy()
		return nil, err
	}
	page.Frames = frames
	page.bytes = bytes
	return page, nil
}

// buildFrames uploads DXT or RGBA frames, keeping the Decoded; on error built textures are destroyed.
func (s *TextureStore) buildFrames(d *assets.Decoded) (frames []*sdl.Texture, bytes int64, err error) {
	fail := func() {
		for _, t := range frames {
			if t != nil {
				_ = t.Destroy()
			}
		}
	}
	if d.DXTFormat != 0 && len(d.Compressed) > 0 {
		// Decoded.compress guarantees a 4-aligned canvas, and the format was validated against the renderer at startup.
		blockBytes := 8
		if d.DXTFormat == assets.DXT5FourCC {
			blockBytes = 16
		}
		pitch := (d.Width / dxtBlockPx) * blockBytes
		for _, comp := range d.Compressed {
			tex, e := s.ren.CreateTexture(d.DXTFormat, sdl.TEXTUREACCESS_STATIC, int32(d.Width), int32(d.Height))
			if e != nil {
				fail()
				return nil, 0, e
			}
			if e := tex.Update(nil, unsafe.Pointer(&comp[0]), pitch); e != nil {
				_ = tex.Destroy()
				fail()
				return nil, 0, e
			}
			_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
			frames = append(frames, tex)
			bytes += int64(len(comp))
		}
		return frames, bytes, nil
	}
	for _, frame := range d.Frames {
		tex, e := s.ren.CreateTexture(
			uint32(sdl.PIXELFORMAT_ABGR8888), // image.RGBA byte order
			sdl.TEXTUREACCESS_STATIC,
			int32(d.Width), int32(d.Height),
		)
		if e != nil {
			fail()
			return nil, 0, e
		}
		if e := tex.Update(nil, unsafe.Pointer(&frame.Pix[0]), frame.Stride); e != nil {
			_ = tex.Destroy()
			fail()
			return nil, 0, e
		}
		_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
		frames = append(frames, tex)
		bytes += int64(len(frame.Pix))
	}
	return frames, bytes, nil
}

// Upload turns a decoded asset into textures and releases the Decoded; render thread only.
func (s *TextureStore) Upload(base string, d *assets.Decoded) error {
	return s.uploadTier(base, d, s.t1)
}

// UploadSmall is Upload into the small tier; a page too big for it goes to main. Render thread only.
func (s *TextureStore) UploadSmall(base string, d *assets.Decoded) error {
	if d.PixelBytes() > s.small.Budget() {
		return s.uploadTier(base, d, s.t1)
	}
	// The tiers are alternatives for a key, never both.
	s.t1.Remove(base)
	return s.uploadTier(base, d, s.small)
}

// uploadTier builds and registers a page in the given tier and releases the Decoded.
func (s *TextureStore) uploadTier(base string, d *assets.Decoded, tier *cache.ByteBudgetLRU[string, *TexturePage]) error {
	defer d.Release()
	start := time.Now()
	page, err := s.buildPage(d)
	if err != nil {
		return err
	}
	foldUploadEWMA(&s.uploadNsEWMA, time.Since(start)) // cold-load profiling: GPU-upload stage
	if !tier.Add(base, page, page.bytes) {
		// An animation can exceed all of T1, which the LRU refuses, so it goes to oversized undecimated.
		// UploadSmall routes too-big pages to main first, so reaching here from small is a real error.
		if tier == s.t1 {
			// Add rejects an over-budget page before removing the old entry, so drop the stale partial here.
			s.t1.Remove(base)
			s.storeOversized(base, page)
			s.generation.Add(1)
			s.clearFailed(base)  // it decoded fine
			s.releaseHeld(base)  // the real page is back
			s.clearMissing(base) // it landed after all
			return nil
		}
		page.destroy()
		return fmt.Errorf("render: %s decoded to %d bytes, above the tier's %d-byte share of the T1 budget", base, page.bytes, tier.Budget())
	}
	s.generation.Add(1)
	s.clearFailed(base)  // it decoded fine — drop any stale negative-cache entry
	s.releaseHeld(base)  // the real page is back: drop the held-frame bridge
	s.clearMissing(base) // it landed after all — drop any conclusively-missing flag
	return nil
}

// storeOversized parks a page in the oversized map, evicting the oldest down to oversizedCap; render thread only.
func (s *TextureStore) storeOversized(base string, page *TexturePage) {
	s.oversizedMu.Lock()
	defer s.oversizedMu.Unlock()
	if old, ok := s.oversized[base]; ok {
		s.oversizedBytes -= old.bytes
		s.dropOversizedOrderLocked(base)
		s.queueDestroy(old)
	}
	s.oversized[base] = page
	s.oversizedBytes += page.bytes
	s.oversizedOrder = append(s.oversizedOrder, base)
	s.enforceOversizedCapLocked(base)
}

// enforceOversizedCapLocked evicts the oldest oversized pages except keep; the caller holds oversizedMu.
func (s *TextureStore) enforceOversizedCapLocked(keep string) {
	for s.oversizedBytes > s.oversizedCap && len(s.oversizedOrder) > 0 {
		// keep's stream may be live, so never evict it; a lone page may exceed the cap.
		idx := -1
		for i, k := range s.oversizedOrder {
			if k != keep {
				idx = i
				break
			}
		}
		if idx < 0 {
			return // only `keep` remains: a single page may exceed the cap
		}
		oldest := s.oversizedOrder[idx]
		if p, ok := s.oversized[oldest]; ok {
			s.oversizedBytes -= p.bytes
			delete(s.oversized, oldest)
			s.queueDestroy(p)
		}
		s.oversizedOrder = append(s.oversizedOrder[:idx], s.oversizedOrder[idx+1:]...)
	}
}

// AppendStream grows a page in the oversized map, avoiding LRU churn; the finalize chunk clears Partial.
func (s *TextureStore) AppendStream(base string, d *assets.Decoded) error {
	defer d.Release()
	if d.FrameOffset == 0 && len(d.Frames) > 0 {
		// A re-decode can race this stream; re-establishing would shrink the page and strand the cursor, so keep it.
		s.oversizedMu.RLock()
		_, exists := s.oversized[base]
		s.oversizedMu.RUnlock()
		if exists {
			return nil
		}
		page, err := s.buildPage(d)
		if err != nil {
			return err
		}
		s.storeOversized(base, page)
		s.clearFailed(base)
		s.releaseHeld(base)
		s.clearMissing(base)
		s.generation.Add(1)
		return nil
	}
	s.oversizedMu.RLock()
	page, ok := s.oversized[base]
	s.oversizedMu.RUnlock()
	if !ok {
		return nil // stream page gone (oversized cap eviction); a re-demand re-decodes
	}
	if len(d.Frames) > 0 {
		if len(page.Frames) != d.FrameOffset {
			return nil // out-of-order append: a fuller page already won
		}
		frames, added, err := s.buildFrames(d)
		if err != nil {
			return err
		}
		// applyScaleMode skips a page already set, so appended frames must inherit its filter or stay bilinear.
		// No Flush is needed: new textures have no queued copies.
		if page.scaleModeSet {
			for _, t := range frames {
				setTextureScaleMode(t, page.scaleMode)
			}
		}
		page.Frames = append(page.Frames, frames...)
		page.Delays = append(page.Delays, d.Delays...)
		page.bytes += added
		s.oversizedMu.Lock()
		s.oversizedBytes += added
		s.enforceOversizedCapLocked(base)
		s.oversizedMu.Unlock()
	}
	if !d.Partial {
		page.Partial = false
		page.SourceFrames = d.SourceFrames
	}
	s.generation.Add(1)
	return nil
}

// dropOversizedOrderLocked removes base from the order ring; the caller holds oversizedMu.
func (s *TextureStore) dropOversizedOrderLocked(base string) {
	for i, k := range s.oversizedOrder {
		if k == base {
			s.oversizedOrder = append(s.oversizedOrder[:i], s.oversizedOrder[i+1:]...)
			return
		}
	}
}

// UploadPinned is Upload into the LRU-exempt tier; theme-media prefixes must use UploadThemeMedia's budget.
func (s *TextureStore) UploadPinned(base string, d *assets.Decoded) error {
	if IsThemeMediaKey(base) {
		d.Release()
		return fmt.Errorf("render: %q is theme media — use UploadThemeMedia, which is the only gated producer of %s / %s",
			base, ThemeMediaKeyPrefix, ThemeGenKeyPrefix)
	}
	return s.uploadPinnedTracked(base, d, false)
}

// uploadPinnedTracked is the shared body; admitted is a parameter so neither caller assumes the other checked.
func (s *TextureStore) uploadPinnedTracked(base string, d *assets.Decoded, admitted bool) error {
	defer d.Release()
	page, err := s.buildPage(d)
	if err != nil {
		return err
	}
	if old, ok := s.pinned[base]; ok {
		s.pinnedBytes -= old.bytes
		s.noteThemeMediaRemoved(base, old.bytes)
		s.queueDestroy(old)
	}
	s.pinned[base] = page
	s.pinnedBytes += page.bytes
	if admitted {
		s.noteThemeMediaAdded(base, page.bytes)
	}
	s.generation.Add(1)
	return nil
}

// Remove evicts a page from whichever tier holds it; render thread only.
func (s *TextureStore) Remove(base string) {
	if page, ok := s.pinned[base]; ok {
		delete(s.pinned, base)
		s.pinnedBytes -= page.bytes
		// The ledger drains here so the theme-swap prune returns the whole allowance; a separate path would leak it.
		s.noteThemeMediaRemoved(base, page.bytes)
		s.queueDestroy(page)
		s.generation.Add(1)
		return
	}
	s.oversizedMu.Lock()
	page, ok := s.oversized[base]
	if ok {
		s.oversizedBytes -= page.bytes
		delete(s.oversized, base)
		s.dropOversizedOrderLocked(base)
	}
	s.oversizedMu.Unlock()
	if ok {
		s.queueDestroy(page)
		s.generation.Add(1)
		return
	}
	// A key lives in at most one LRU tier; Remove on the other is a no-op.
	s.small.Remove(base)
	s.t1.Remove(base)
}

// reservedKeySchemes aren't server asset URLs; a local-pack sweep must skip them, as no mount can re-answer them.
var reservedKeySchemes = [...]string{"theme://", "held://", "thumb://", "shape://", "missingno://"}

func reservedKey(key string) bool {
	for _, s := range reservedKeySchemes {
		if strings.HasPrefix(key, s) {
			return true
		}
	}
	return false
}

// RemoveWhere drops pages whose key satisfies pred; render thread, user-initiated, as it allocates.
// Without it, resolution short-circuits on resident textures and a Rescan keeps showing old art.
func (s *TextureStore) RemoveWhere(pred func(key string) bool) int {
	if pred == nil {
		return 0
	}
	var doomed []string
	for key := range s.pinned {
		if !reservedKey(key) && pred(key) {
			doomed = append(doomed, key)
		}
	}
	s.oversizedMu.RLock()
	for key := range s.oversized {
		if !reservedKey(key) && pred(key) {
			doomed = append(doomed, key)
		}
	}
	s.oversizedMu.RUnlock()
	for _, key := range append(s.t1.Keys(), s.small.Keys()...) {
		if !reservedKey(key) && pred(key) {
			doomed = append(doomed, key)
		}
	}
	for _, key := range doomed {
		s.Remove(key) // the existing path, so byte accounting and queueDestroy stay correct
	}
	return len(doomed)
}

// ForgetMissing empties the 404'd set, as a mount change or Rescan may supply them; render thread only.
func (s *TextureStore) ForgetMissing() {
	clear(s.missing)
}

// ForgetFailed empties the decode cache; Rescan must call it, as the prefetch check runs before mounts.
func (s *TextureStore) ForgetFailed() {
	s.failedMu.Lock()
	clear(s.failed)
	s.failedMu.Unlock()
}

// queueDestroy hands a page to the destroy queue, or destroys inline when full (render thread, bounded).
func (s *TextureStore) queueDestroy(page *TexturePage) {
	select {
	case s.destroy <- page:
	default:
		page.destroy()
	}
}

// DrainDestroyQueue destroys up to destroyBudgetPerFrame pages; render thread only, once per frame.
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

func (s *TextureStore) Generation() uint64 {
	return s.generation.Load()
}

// Stats returns T1 counters aggregated over both LRU tiers.
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

// OversizedBytes reports total bytes in the oversized map; safe from any goroutine.
func (s *TextureStore) OversizedBytes() int64 {
	s.oversizedMu.RLock()
	defer s.oversizedMu.RUnlock()
	return s.oversizedBytes
}

// Purge destroys everything, pinned pages included; the theme re-applies after. Render thread only.
func (s *TextureStore) Purge() {
	// The tier purges fire onEvict after pinned is cleared; a held-frame steal then would leak past the purge.
	s.purging = true
	defer func() { s.purging = false }()
	s.generation.Add(1)
	for base, page := range s.pinned {
		page.destroy()
		delete(s.pinned, base)
	}
	s.pinnedBytes = 0
	s.oversizedMu.Lock()
	for base, page := range s.oversized {
		page.destroy()
		delete(s.oversized, base)
	}
	s.oversizedBytes = 0
	s.oversizedOrder = s.oversizedOrder[:0]
	s.oversizedMu.Unlock()
	// The ledger is a view of pinned, so it empties with it; healTheme brings the art back like the chrome.
	s.themeMediaBytes, s.themeMediaCount, s.themeGenCount = 0, 0, 0
	s.heldKeys = [heldSceneryMax]string{}
	s.heldNext = 0
	clear(s.missing) // fresh server = fresh misses; the placeholder page (pinned) was destroyed above and re-uploads via the App's post-purge heal
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
