package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"log"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kettek/apng"
	xdraw "golang.org/x/image/draw"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
)

const (
	// decodeQueueCap bounds the decode job queue; Submit blocks briefly when
	// full (decode work is never speculative enough to shed — the pool ahead
	// of it already filtered).
	decodeQueueCap = 64

	// minDecodeWorkers is the floor for the worker count formula
	// max(2, NumCPU/2) from spec §8.
	minDecodeWorkers = 2

	// gifDelayUnit is the GIF frame-delay resolution.
	gifDelayUnit = 10 * time.Millisecond

	// defaultZeroFrameDelay replaces zero/negative frame delays, matching
	// browser & webAO handling of broken assets.
	defaultZeroFrameDelay = 100 * time.Millisecond

	// rgbaBytesPerPixel is the decoded pixel size (image.RGBA).
	rgbaBytesPerPixel = 4
)

// defaultMaxAnimatedDecodedAssetBytes bounds ONE animated asset's decoded
// payload (Σ w×h×4 across frames) shipped as the default. Animated sprites are
// no longer DECIMATED to fit the T1 tier (the #110 choppy-animation fix): the
// decoder DOWNSCALES the frames far enough that every authored frame fits this
// budget (budgetFitHeight), so a long clip stays smooth at a smaller on-screen
// size instead of dropping frames (slideshow) or loading full-size (memory
// hog). The decimation path (frameDecimator) stays only as a safety net.
var defaultMaxAnimatedDecodedAssetBytes = int64(cache.DefaultMaxAnimatedDecodedAssetBytes)

// maxAnimatedDecodedAssetBytes is the LIVE per-animated-asset frame budget. It
// defaults to defaultMaxAnimatedDecodedAssetBytes (128 MiB). Atomic: decode
// workers read it live. Tests shrink it directly to exercise the budget-fit
// downscale deterministically.
var maxAnimatedDecodedAssetBytes atomic.Int64

func init() { maxAnimatedDecodedAssetBytes.Store(defaultMaxAnimatedDecodedAssetBytes) }

// SetAnimatedDecodedAssetBytes sets the LIVE per-animated-asset frame budget
// (bytes); <= 0 resets to the default. Atomic: decode workers read it live, so
// newly decoded animations pick it up immediately.
func SetAnimatedDecodedAssetBytes(bytes int64) {
	if bytes <= 0 {
		bytes = defaultMaxAnimatedDecodedAssetBytes
	}
	maxAnimatedDecodedAssetBytes.Store(bytes)
}

const (
	// charIconDecodePx / emoteButtonDecodePx are the post-decode thumbnail
	// edges for the two asset types drawn at fixed small cells (they mirror
	// ui.iconCell and ui.emoteBtnCell — assets cannot import ui). Packs ship
	// char icons at arbitrary sizes; a 500×500 icon decoded natively costs
	// ~1 MB of T1 for a 64 px cell (~60× waste), capping how many icons stay
	// resident and churning the cache while scrolling. Thumbnailing in the
	// decode pool makes an icon ~16 KB: a 4000-char roster fits T1 whole.
	charIconDecodePx    = 64
	emoteButtonDecodePx = 40
)

// decodeTargetPx returns the thumbnail edge for fixed-cell asset types;
// 0 means keep the native size.
func decodeTargetPx(t AssetType) int {
	switch t {
	case AssetTypeCharIcon:
		return charIconDecodePx
	case AssetTypeEmoteButton:
		return emoteButtonDecodePx
	default:
		return 0
	}
}

// downscaleDecoded rescales every frame to target×target (the same stretch
// the GPU performed when drawing the native texture into a square cell, so
// visuals are unchanged) and releases the source buffers. No-op when the
// canvas already fits.
func downscaleDecoded(d *Decoded, target int) *Decoded {
	if d.Width <= target && d.Height <= target {
		return d
	}
	out := &Decoded{
		Animated: d.Animated,
		Width:    target,
		Height:   target,
		Frames:   make([]*image.RGBA, 0, len(d.Frames)),
		Delays:   d.Delays,
		// Carried, not dropped: a downscale resizes PIXELS and never removes a
		// frame, so the source frame space is unchanged. Rebuilding the struct
		// without it left SourceFrames at 0, and FrameKeepIndex reads a
		// non-positive sourceTotal as "no decimation happened" and returns the
		// identity — silently mis-mapping #17 networked frame effects on any
		// animation that was BOTH decimated and downscaled.
		SourceFrames: d.SourceFrames,
	}
	for _, frame := range d.Frames {
		small, token := newPooledRGBA(target, target)
		xdraw.ApproxBiLinear.Scale(small, small.Rect, frame, frame.Rect, xdraw.Src, nil)
		out.Frames = append(out.Frames, small)
		if token != nil {
			out.pooledPix = append(out.pooledPix, token)
		}
	}
	d.Release()
	return out
}

// downscaleDecodedAspect shrinks every frame so its HEIGHT fits within maxH,
// preserving aspect. Sprites/backgrounds are drawn scaled to the viewport
// height, so a texture taller than the display can never show more detail — it
// only costs memory and forces SDL to single-pass-shrink the full ~2000px
// source every frame (the visible quality loss vs a browser's mipmapped
// downsample). Doing one HIGH-QUALITY CatmullRom downscale here — once, off the
// render thread — lands a near-display-size texture, so the per-frame CopyEx
// then has a far gentler ratio and the result is sharper AND cheaper to draw.
// Downscale-only: a no-op when the asset already fits.
func downscaleDecodedAspect(d *Decoded, maxH int) *Decoded {
	if maxH <= 0 || d.Height <= maxH {
		return d
	}
	newW := d.Width * maxH / d.Height
	if newW < 1 {
		newW = 1
	}
	out := &Decoded{
		Animated:     d.Animated,
		Width:        newW,
		Height:       maxH,
		Frames:       make([]*image.RGBA, 0, len(d.Frames)),
		Delays:       d.Delays,
		SourceFrames: d.SourceFrames, // see downscaleDecoded: pixels shrink, the frame space does not
	}
	for _, frame := range d.Frames {
		small, token := newPooledRGBA(newW, maxH)
		xdraw.CatmullRom.Scale(small, small.Rect, frame, frame.Rect, xdraw.Src, nil)
		out.Frames = append(out.Frames, small)
		if token != nil {
			out.pooledPix = append(out.pooledPix, token)
		}
	}
	d.Release()
	return out
}

// decodeTargetDims returns the on-screen dimensions for a sprite after the
// aspect-preserving height cap maxH, plus whether any downscale is needed.
func decodeTargetDims(width, height, maxH int) (tw, th int, downscale bool) {
	if maxH <= 0 || height <= maxH {
		return width, height, false
	}
	th = maxH
	tw = width * maxH / height
	if tw < 1 {
		tw = 1
	}
	return tw, th, true
}

// budgetFitHeight returns the max HEIGHT (px) at which `frames` frames of a
// `width`×`height` canvas still fit within maxAnimatedDecodedAssetBytes,
// preserving aspect. It is the #110 memory bound: an over-budget animation is
// DOWNSCALED so every authored frame fits, rather than decimated (dropping
// frames — the choppy/slideshow behaviour) or loaded at full size (the memory
// hog). Returns `height` when the native canvas already fits (or the inputs
// are degenerate, so the caller's height cap still applies).
func budgetFitHeight(width, height, frames int) int {
	budget := maxAnimatedDecodedAssetBytes.Load()
	if budget <= 0 || width <= 0 || height <= 0 || frames <= 1 {
		return height
	}
	// frames × (width×th/height) × th × 4 ≤ budget
	//   =>  th² ≤ budget × height / (frames × width × 4)
	th2 := budget * int64(height) / (int64(frames) * int64(width) * rgbaBytesPerPixel)
	th := int(math.Sqrt(float64(th2)))
	if th <= 0 {
		th = 1
	}
	if th >= height {
		return height // the native canvas already fits the budget
	}
	return th
}

// decodeTargetDimsBudgeted is decodeTargetDims with the animated frame budget
// folded in: it downscales far enough that `frames` frames fit
// maxAnimatedDecodedAssetBytes, so a long high-res clip keeps every authored
// frame at a smaller on-screen size instead of being decimated (#110).
func decodeTargetDimsBudgeted(width, height, maxH, frames int) (tw, th int, downscale bool) {
	if frames <= 0 {
		frames = 1
	}
	if fitH := budgetFitHeight(width, height, frames); maxH <= 0 || fitH < maxH {
		maxH = fitH
	}
	return decodeTargetDims(width, height, maxH)
}

// downscaleFrame shrinks one full-size frame to tw×th with an area-average
// (box) filter. Every source pixel is visited exactly once — unlike
// ApproxBiLinear, which skips source pixels when shrinking and produces the
// aliased "low-res" look — so the result is smooth and anti-aliased. Alpha is
// averaged in premultiplied space (weighted by A) so transparent pixels can't
// darken a sprite's edge. Integer-only, so it stays fast (a fraction of
// CatmullRom's float64 kernel).
func downscaleFrame(src *image.RGBA, tw, th int) (*image.RGBA, *[]byte) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	out, token := newPooledRGBA(tw, th)
	spix, dpix := src.Pix, out.Pix
	sstride, dstride := src.Stride, out.Stride

	for dy := 0; dy < th; dy++ {
		y0 := dy * sh / th
		y1 := (dy + 1) * sh / th
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < tw; dx++ {
			x0 := dx * sw / tw
			x1 := (dx + 1) * sw / tw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sumR, sumG, sumB, sumA uint32
			for sy := y0; sy < y1; sy++ {
				p := sy*sstride + x0*4
				for sx := x0; sx < x1; sx++ {
					a := uint32(spix[p+3])
					sumR += uint32(spix[p]) * a
					sumG += uint32(spix[p+1]) * a
					sumB += uint32(spix[p+2]) * a
					sumA += a
					p += 4
				}
			}
			q := dy*dstride + dx*4
			if sumA == 0 {
				dpix[q], dpix[q+1], dpix[q+2], dpix[q+3] = 0, 0, 0, 0
				continue
			}
			dpix[q] = uint8(sumR / sumA)
			dpix[q+1] = uint8(sumG / sumA)
			dpix[q+2] = uint8(sumB / sumA)
			dpix[q+3] = uint8(sumA / uint32((x1-x0)*(y1-y0)))
		}
	}
	return out, token
}

// boundedFrameCount reports how many frames of an animation may stay resident:
// the count whose decoded bytes fit maxAnimatedDecodedAssetBytes — never below
// one frame (a single canvas larger than the budget fails at upload with a
// clear error instead). The decoders honour this budget by DECIMATION, not
// truncation (see frameDecimator): the returned count is how many evenly-spaced
// frames to keep across the whole clip, not a prefix length.
func boundedFrameCount(width, height, frames int) int {
	canvasBytes := width * height * rgbaBytesPerPixel
	if canvasBytes <= 0 {
		return frames
	}
	maxFrames := int(maxAnimatedDecodedAssetBytes.Load()) / canvasBytes
	if maxFrames < 1 {
		maxFrames = 1
	}
	if frames > maxFrames {
		return maxFrames
	}
	return frames
}

// --- animation decimation -----------------------------------------------------
//
// boundedFrameCount caps how many frames of one animation may stay resident (the
// T1 memory budget). The decoders used to honour it by TRUNCATION — materialise
// the first N frames, drop the rest — but a long one-shot preanimation (Great-
// Ace-Attorney-style sprites ship 60–150 full-canvas frames) then played only
// its first quarter and SNAPPED to the talking pose: the "animations break / cut
// off / flash" report. Decimation instead keeps N frames EVENLY SPACED across
// the whole clip and folds every skipped frame's delay into the next kept one,
// so the clip plays start→end (final pose intact) at a lower frame rate inside
// the same byte budget — and, because it now plays for its real duration, the
// courtroom's preanim timeout matches and the next sprite has the full window to
// stream in. Compositing decoders must still walk EVERY source frame (each frame
// composes onto the running canvas); only the kept frames are copied out, so the
// resident cost stays bounded while decode CPU scales with the source length —
// an acceptable trade for a one-shot preanim decoded off the render thread.
// Short animations (frames ≤ budget) decimate to themselves: idles and talk
// loops are never touched.

// frameKeepIndex maps kept-frame ordinal j∈[0,keep) to its SOURCE frame index
// when sampling `keep` frames out of `total` (1 ≤ keep ≤ total), spanning both
// endpoints: j=0→0, j=keep-1→total-1, evenly spaced (round-to-nearest). The
// indices are strictly increasing for total ≥ keep, so exactly `keep` distinct
// frames are kept.
func frameKeepIndex(j, total, keep int) int {
	if keep <= 1 {
		return 0
	}
	return (j*(total-1) + (keep-1)/2) / (keep - 1) // round(j*(total-1)/(keep-1))
}

// FrameKeepIndex is the exported forward map kept-frame ordinal → SOURCE frame
// index for the same (total, keep) decimation the decoders applied. The render
// side needs it to convert a decimated (kept) frame ordinal back into the
// sender's raw frame space when firing networked frame-synced effects (#17):
// wire frame indices are authored against the un-decimated file, so a trigger
// must be tested against the SOURCE index a kept frame stands in for. Guards
// keep>total / non-positive args to a safe identity so a malformed page never
// panics. Reuses the private helper so the rounding formula lives in one place
// (a duplicated ratio was a shipped bug — see decoder.go's page-cap history).
func FrameKeepIndex(keptOrdinal, sourceTotal, keptCount int) int {
	if keptCount <= 0 || sourceTotal <= 0 || keptCount >= sourceTotal {
		return keptOrdinal // identity: no decimation happened (or bad args)
	}
	if keptOrdinal < 0 {
		return 0
	}
	if keptOrdinal >= keptCount {
		keptOrdinal = keptCount - 1
	}
	return frameKeepIndex(keptOrdinal, sourceTotal, keptCount)
}

// frameDecimator walks an animation's source frames in order and decides which
// to materialise, folding skipped-frame delays into the kept frames (see the
// block comment above). The zero value is unusable; build with newFrameDecimator
// and call step once per source frame, in index order.
type frameDecimator struct {
	total, keep, next, kept int
	pending                 time.Duration
}

// newFrameDecimator prepares to keep `keep` frames (clamped to [1,total]) out of
// `total`, starting with frame 0.
func newFrameDecimator(total, keep int) frameDecimator {
	if keep < 1 {
		keep = 1
	}
	if keep > total {
		keep = total
	}
	return frameDecimator{total: total, keep: keep} // next == 0 keeps frame 0
}

// step records source frame i's display delay and reports whether that frame
// should be materialised. When keep is true, dur is the kept frame's folded
// delay (every skipped frame since the previous kept one, plus this one), so the
// kept sequence's total playback time equals the original.
func (fd *frameDecimator) step(i int, delay time.Duration) (dur time.Duration, keep bool) {
	fd.pending += delay
	if i != fd.next {
		return 0, false
	}
	dur = fd.pending
	fd.pending = 0
	fd.kept++
	if fd.kept < fd.keep {
		fd.next = frameKeepIndex(fd.kept, fd.total, fd.keep)
	} else {
		fd.next = fd.total // sentinel past the final index: nothing more is kept
	}
	return dur, true
}

// spreadLoopDelays evens out a decimated animation's kept-frame delays when the
// SOURCE delays were uniform (a looping idle/talk cycle, e.g. a trotting
// horse): the fold that packs skipped-frame delays forward would otherwise play
// such a loop as [D, 2D, 2D, …] — every kept frame a different duration — so
// the loop judders. Evenly spreading the total across the kept frames restores
// a constant frame rate at the SAME total playback time. Non-uniform sources (a
// one-shot preanim whose final frame holds) stay folded so the hold remains on
// the final kept frame. sourceDelays is the full source-order delay sequence;
// GIF/APNG build it up front, AVIF/WebP collect it during their walk. No-op
// unless the clip was actually decimated.
func spreadLoopDelays(d *Decoded, sourceDelays []time.Duration) {
	if len(d.Delays) < 2 || len(sourceDelays) <= len(d.Delays) {
		return
	}
	uniform := true
	var total time.Duration
	for _, s := range sourceDelays {
		total += s
		if s != sourceDelays[0] {
			uniform = false
		}
	}
	if !uniform {
		return
	}
	keep := len(d.Delays)
	base := total / time.Duration(keep)
	rem := int(total % time.Duration(keep))
	for i := range d.Delays {
		d.Delays[i] = base
		if i < rem {
			d.Delays[i]++
		}
	}
}

// DecodeWorkers returns the §8 worker-count formula.
func DecodeWorkers() int {
	n := runtime.NumCPU() / 2
	if n < minDecodeWorkers {
		return minDecodeWorkers
	}
	return n
}

// defaultAnimatedDecodeConcurrency bounds how many FULL animated decodes run at
// once. The per-frame decoded RGBA of a long clip is the memory spike (60+
// native frames held until upload), so the decode worker pool may be wider than
// this gate without the heap ballooning.
const defaultAnimatedDecodeConcurrency = 2

// animGate is a live-adjustable semaphore bounding concurrent animated decodes.
// A buffered channel cannot be resized, so the limit is an atomic the condition
// variable re-checks each time a worker waits for a free slot. limit <= 0 means
// "unlimited".
type animGate struct {
	mu     sync.Mutex
	cond   *sync.Cond
	limit  atomic.Int64
	active atomic.Int64
}

func newAnimGate(limit int) *animGate {
	g := &animGate{}
	g.cond = sync.NewCond(&g.mu)
	g.limit.Store(int64(limit))
	return g
}

// SetLimit adjusts the concurrency bound live; a raised limit wakes waiters.
func (g *animGate) SetLimit(n int) {
	g.mu.Lock()
	g.limit.Store(int64(n))
	g.cond.Broadcast()
	g.mu.Unlock()
}

// acquire blocks until an animated-decode slot is free.
func (g *animGate) acquire() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.limit.Load() > 0 && g.active.Load() >= g.limit.Load() {
		g.cond.Wait()
	}
	g.active.Add(1)
}

func (g *animGate) release() {
	g.mu.Lock()
	g.active.Add(-1)
	g.cond.Broadcast()
	g.mu.Unlock()
}

// DecodeRequest is one decode job.
type DecodeRequest struct {
	// URL identifies the asset (cache key); carried through to OnDone.
	URL string
	// Data is the raw payload. Treated as immutable.
	Data []byte
	// Type hints budget accounting; routing is by sniffed magic, never
	// extension.
	Type AssetType
	// PlayAnimations decodes all frames when true; only the first frame
	// when false (the "Play Animations" toggle — a decode-level switch,
	// never a network-level one).
	PlayAnimations bool
	// OnDone receives the result on a decoder goroutine. It must be cheap
	// (hand off to a channel) and must not touch SDL (spec §17.1).
	OnDone func(url string, d *Decoded, err error)
}

// DecoderPool decodes image payloads into plain RGBA memory. It performs
// zero SDL calls: texture upload happens on the render thread, which drains
// the manager's decoded channel (spec §8).
type DecoderPool struct {
	jobs      chan DecodeRequest
	stop      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once

	decoded atomic.Int64
	failed  atomic.Int64

	// spriteCap is the max HEIGHT in px for full-size assets (character
	// sprites, backgrounds, …); 0 = no cap. Set once at startup from the
	// display height: a texture taller than the screen can never show more
	// detail, only cost memory and force the GPU to single-pass-shrink a huge
	// source every frame. atomic because workers read it while the SDL thread
	// stores it at startup.
	spriteCap atomic.Int64

	// animatedSpriteCap is an OPTIONAL tighter height cap for ANIMATED assets
	// only (0 = off, the default): a power-user memory knob that downscales
	// animated sprites harder than stills, so a long full-canvas preanim costs
	// less VRAM without touching still-art sharpness. Applied post-decode in
	// fit, which already knows whether the asset animated.
	animatedSpriteCap atomic.Int64

	// animGate bounds concurrent FULL animated decodes (the per-frame RGBA
	// spike). The worker pool may be wider; the gate keeps the decode burst
	// from holding several whole clips in flight at once.
	animGate *animGate

	// texCompress is the live texture-compression FOURCC (0 = off, DXT1/DXT5).
	// Set once at startup from the power-user setting intersected with the
	// renderer's supported formats; decode workers read it while compressing.
	texCompress atomic.Uint32

	// decodeNsEWMA tracks decode+fit wall time (cold-load profiling; the debug
	// overlay's per-stage line reads it via Stats).
	decodeNsEWMA atomic.Int64
}

// ewmaFoldWeightDen is the EWMA weight (1/4 — matches the network TTFB EWMA)
// for the cold-load profiling averages.
const ewmaFoldWeightDen = 4

// foldEWMA folds one duration sample into an atomic nanosecond EWMA.
func foldEWMA(dst *atomic.Int64, sample time.Duration) {
	if sample <= 0 {
		return
	}
	old := dst.Load()
	if old == 0 {
		dst.Store(int64(sample))
		return
	}
	dst.Store(old + (int64(sample)-old)/ewmaFoldWeightDen)
}

// --- animated-decode heap reclaim ---------------------------------------------
//
// An animated decode burst (a character's dozens of emotion animations decoded
// in quick succession) frees a large amount of heap the moment its frames
// upload and their raw RGBA buffers are released. The Go runtime's background
// scavenger returns those pages to the OS over a few seconds, so process RSS
// can transiently read far above the GOMEMLIMIT budget. maybeReclaimHeap forces
// a single GC from a decode worker once a burst has left a lot of idle heap, so
// the heap stops re-growing while the scavenger catches up. It deliberately does
// NOT call debug.FreeOSMemory: that stop-the-world scavenge would stall the
// render thread and drop frames.

const (
	// reclaimHeapIdleThreshold is the minimum unused heap (HeapIdle, bytes)
	// that triggers a forced GC. Below it the runtime's own pacing suffices.
	reclaimHeapIdleThreshold = 128 << 20
	// reclaimHeapMinInterval bounds how often a decode worker may force a GC so
	// a burst of small animations cannot thrash the collector.
	reclaimHeapMinInterval = time.Second
)

var (
	reclaimMu       sync.Mutex
	lastReclaimTime time.Time
)

func maybeReclaimHeap() {
	reclaimMu.Lock()
	if time.Since(lastReclaimTime) < reclaimHeapMinInterval {
		reclaimMu.Unlock()
		return
	}
	lastReclaimTime = time.Now()
	reclaimMu.Unlock()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapIdle < reclaimHeapIdleThreshold {
		return
	}
	runtime.GC()
}

// heapSysMiB reports the Go runtime's total memory obtained from the OS
// (runtime.MemStats.Sys) in MiB — the Go-heap contribution to process RSS,
// including freed-but-not-yet-returned pages. Logged on every animated decode
// so the memory footprint of a large-animated-sprite burst is visible in an
// exported console log without a profiler.
func heapSysMiB() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys >> 20
}

// NewDecoderPool starts workers decode goroutines (DecodeWorkers() when
// workers <= 0).
func NewDecoderPool(workers int) *DecoderPool {
	if workers <= 0 {
		workers = DecodeWorkers()
	}
	p := &DecoderPool{
		jobs:     make(chan DecodeRequest, decodeQueueCap),
		stop:     make(chan struct{}),
		animGate: newAnimGate(defaultAnimatedDecodeConcurrency),
	}
	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go p.worker()
	}
	return p
}

// SetSpriteCap sets the display-height ceiling (px) for full-size assets; 0
// disables it. Call once at startup (from the SDL display height) before
// decodes begin. Safe to call concurrently with workers (atomic store).
func (p *DecoderPool) SetSpriteCap(px int) {
	if px < 0 {
		px = 0
	}
	p.spriteCap.Store(int64(px))
}

// SetAnimatedSpriteCap sets the optional animated-only height cap (px); 0
// disables it (animated sprites then use the same sprite cap as stills). Like
// SetSpriteCap it is safe to call concurrently with workers (atomic store).
func (p *DecoderPool) SetAnimatedSpriteCap(px int) {
	if px < 0 {
		px = 0
	}
	p.animatedSpriteCap.Store(int64(px))
}

// SetAnimatedBudgetMiB sets the live per-animated-asset frame budget (MiB);
// <= 0 resets to the default. Decode workers read the budget atomically.
func (p *DecoderPool) SetAnimatedBudgetMiB(mib int) {
	SetAnimatedDecodedAssetBytes(int64(mib) << 20)
}

// SetAnimatedDecodeConcurrency sets the live concurrent-animated-decode bound.
func (p *DecoderPool) SetAnimatedDecodeConcurrency(n int) {
	p.animGate.SetLimit(n)
}

// SetTextureCompression sets the decode-pool texture-compression mode
// (CompressOff / CompressDXT5 / CompressDXT1). New decodes compress their
// frames to the chosen DXT format before upload.
func (p *DecoderPool) SetTextureCompression(mode int) {
	switch mode {
	case CompressDXT5:
		p.texCompress.Store(DXT5FourCC)
	case CompressDXT1:
		p.texCompress.Store(DXT1FourCC)
	default:
		p.texCompress.Store(0)
	}
}

// compressIfEnabled compresses a freshly decoded asset in place when a DXT mode
// is active (and the canvas is 4-aligned). Idempotent.
func (p *DecoderPool) compressIfEnabled(d *Decoded) {
	if d == nil {
		return
	}
	if f := p.texCompress.Load(); f != 0 {
		d.compress(f)
	}
}

// fit shrinks a freshly decoded asset to its on-screen ceiling: fixed-cell
// types (char icons / emote buttons) to a small square thumbnail, every other
// (full-size) type to the display-height sprite cap — or, for animated assets,
// the tighter animated-only cap when one is set (the opt-in memory knob).
// Both are downscale-only, so already-small assets pass through untouched.
func (p *DecoderPool) fit(t AssetType, d *Decoded) *Decoded {
	if d == nil {
		return d
	}
	if target := decodeTargetPx(t); target > 0 {
		return downscaleDecoded(d, target) // square thumbnail (fixed cells)
	}
	cap := int(p.spriteCap.Load())
	if d.Animated {
		if ac := int(p.animatedSpriteCap.Load()); ac > 0 && (cap <= 0 || ac < cap) {
			cap = ac // animated-only cap, tighter than the still sprite cap
		}
	}
	if cap > 0 {
		return downscaleDecodedAspect(d, cap) // aspect-preserving, height-bound
	}
	return d
}

// fullSizeMaxH is the decode-time height cap for a full-size asset: the sprite
// cap when the type is not a fixed-cell thumbnail (those downscale in fit to a
// square), 0 otherwise. Animated decoders use it to downscale per-frame DURING
// decode and to budget their frame count against the on-screen bytes (#110).
func (p *DecoderPool) fullSizeMaxH(t AssetType) int {
	if decodeTargetPx(t) > 0 {
		return 0
	}
	return int(p.spriteCap.Load())
}

// Submit queues a decode. Returns false when the pool is closed (OnDone is
// then invoked inline with an error so callers always hear back).
func (p *DecoderPool) Submit(req DecodeRequest) bool {
	if req.OnDone == nil {
		return false
	}
	select {
	case <-p.stop:
		req.OnDone(req.URL, nil, fmt.Errorf("assets: decoder pool closed"))
		return false
	default:
	}
	select {
	case p.jobs <- req:
		return true
	case <-p.stop:
		req.OnDone(req.URL, nil, fmt.Errorf("assets: decoder pool closed"))
		return false
	}
}

// Close stops the workers and fails any queued jobs so nobody waits forever.
func (p *DecoderPool) Close() {
	p.closeOnce.Do(func() {
		close(p.stop)
		p.wg.Wait()
		for {
			select {
			case req := <-p.jobs:
				req.OnDone(req.URL, nil, fmt.Errorf("assets: decoder pool closed"))
			default:
				return
			}
		}
	})
}

func (p *DecoderPool) worker() {
	defer p.wg.Done()
	// A decode runs on a background goroutine the render thread's crash guard
	// cannot see; log any panic to disk (then re-panic so the crash behaviour
	// is unchanged — it just becomes diagnosable).
	defer func() {
		if r := recover(); r != nil {
			writeDecodeCrash(r)
			panic(r)
		}
	}()
	for {
		select {
		case req := <-p.jobs:
			p.runJob(req)
		case <-p.stop:
			return
		}
	}
}

// runJob decodes one payload. Animated payloads with full playback
// requested deliver progressively: frame 0 first (the cheap first-frame
// path — one frame decoded instead of N), then the full set replaces it
// at upload. A 5 MB preanim starts on screen after one frame-decode
// instead of after the whole sequence.
func (p *DecoderPool) runJob(req DecodeRequest) {
	animated := req.PlayAnimations && sniffMaybeAnimated(req.Data)
	streamable := animated && streamableFormat(req.Data)

	// Animated decodes hold an animGate slot for their WHOLE run — both the
	// establishing frame-0 decode and the full/streaming decode. The frame-0
	// prefix used to run un-gated (up to NumCPU/2 at once), which multiplied the
	// transient native-frame allocations into the multi-GiB RSS spike the gate
	// exists to cap (docs/PERFORMANCE.md). Taking the slot up front bounds the
	// entire animated decode to the configured concurrency without touching the
	// render/playback path.
	if animated {
		p.animGate.acquire()
		defer p.animGate.release()
	}

	if animated {
		if first, err := DecodeImage(req.Data, false); err == nil {
			// GIF/APNG can sniff "maybe" but decode static — only a real
			// animation benefits from the early frame (statics would just
			// upload the same texture twice).
			if first.Animated && len(first.Frames) > 0 {
				if streamable {
					// Match the stream's frame size: downscale frame 0 to the
					// same budget-fit dimensions the appends use (fit below still
					// applies any tighter animated-only cap after).
					first = fitEstablishingPrefix(first, req.Data, p.fullSizeMaxH(req.Type))
				}
				first = p.fit(req.Type, first)
				p.compressIfEnabled(first)
				first.Partial = true
				if streamable {
					// Streaming formats park the page in the oversized map so it
					// can grow frame-by-frame; frame 0 is the establishing prefix.
					first.Stream = true
				}
				req.OnDone(req.URL, first, nil)
			} else {
				first.Release()
			}
		}
	}

	// Streaming decode for the definitely-animated CGO formats: frames land one
	// at a time and the page plays the growing prefix (see streamAnimated).
	if streamable {
		p.streamAnimated(req)
		return
	}

	start := time.Now()
	d, err := DecodeImageSized(req.Data, req.PlayAnimations, p.fullSizeMaxH(req.Type))
	if err != nil {
		p.failed.Add(1)
	} else {
		d = p.fit(req.Type, d)
		p.compressIfEnabled(d)
		p.decoded.Add(1)
		// Cold-load profiling: fold decode+fit wall time into the EWMA (weight
		// 1/4, same as the network TTFB) — the debug overlay's per-stage line.
		foldEWMA(&p.decodeNsEWMA, time.Since(start))
	}
	req.OnDone(req.URL, d, err)

	// A completed full animated decode (GIF/APNG — the streaming path reclaims
	// in streamAnimated) just freed its burst; nudge the heap now.
	if animated {
		maybeReclaimHeap()
	}
}

// sniffMaybeAnimated reports payloads worth a progressive first frame:
// definitely-animated containers plus GIF/APNG (frame count unknowable
// without decoding; the cheap first-frame decode settles it).
func sniffMaybeAnimated(data []byte) bool {
	switch Sniff(data) {
	case FormatWebPAnim, FormatAVIFAnim, FormatAPNG, FormatGIF:
		return true
	default:
		return false
	}
}

// streamableFormat reports the definitely-animated CGO containers whose
// decoders can stream frames incrementally (WebP ANMF / AV1 image sequence).
// GIF and APNG are excluded: their DecodeAll decodes every frame up front, so
// there is no per-frame decode to overlap and they keep the classic
// progressive+full path.
func streamableFormat(data []byte) bool {
	switch Sniff(data) {
	case FormatWebPAnim, FormatAVIFAnim:
		return true
	default:
		return false
	}
}

// peekAnimatedDims returns an animated payload's canvas size and frame count
// without decoding pixels, dispatching to the format's header reader. Used to
// make the progressive first frame match the stream's budget-fit dimensions.
func peekAnimatedDims(data []byte) (width, height, frames int, ok bool) {
	switch Sniff(data) {
	case FormatWebPAnim:
		return peekWebPAnimDims(data)
	case FormatAVIFAnim:
		return peekAVIFAnimDims(data)
	default:
		return 0, 0, 0, false
	}
}

// fitEstablishingPrefix downscales a streaming asset's frame-0 prefix to the
// same budget-fit dimensions the incremental appends will use (downscaleFrame,
// the same box filter), so the whole clip is one consistent resolution and the
// establishing frame doesn't pay a full-size downscale + upload. No-op when the
// prefix already matches or the header can't be peeked.
func fitEstablishingPrefix(first *Decoded, data []byte, maxH int) *Decoded {
	w, h, frames, ok := peekAnimatedDims(data)
	if !ok {
		return first
	}
	tw, th, _ := decodeTargetDimsBudgeted(w, h, maxH, frames)
	if first.Width == tw && first.Height == th {
		return first
	}
	out := &Decoded{
		Animated:     first.Animated,
		Width:        tw,
		Height:       th,
		Frames:       make([]*image.RGBA, 0, len(first.Frames)),
		Delays:       first.Delays,
		SourceFrames: first.SourceFrames, // pixels shrink, the frame space does not
	}
	for _, frame := range first.Frames {
		small, token := downscaleFrame(frame, tw, th)
		out.Frames = append(out.Frames, small)
		if token != nil {
			out.pooledPix = append(out.pooledPix, token)
		}
	}
	first.Release()
	return out
}

// streamAnimated drives the incremental decode of a WebP/AVIF animation. The
// progressive first frame above already delivered the establishing chunk; this
// walks the remaining frames and appends each as it decodes, so the resident
// page grows and the renderer plays the prefix instead of holding frame 0
// until the whole clip decodes (docs/ANIMATION-COLD-LOAD-INVESTIGATION.md gap
// #3).
//
// A decoder may refuse to stream (when the frame budget forces decimation —
// rare, since downscale-to-fit keeps everything in budget); in that case this
// falls back to the classic full decode so spreadLoopDelays keeps working.
func (p *DecoderPool) streamAnimated(req DecodeRequest) {
	format := Sniff(req.Data)
	maxH := p.fullSizeMaxH(req.Type)
	start := time.Now()
	// Reclaim the heap this burst frees once the stream (or its full-decode
	// fallback) completes — every return path below runs it.
	defer maybeReclaimHeap()

	emit := func(chunk *Decoded) {
		chunk = p.fit(req.Type, chunk)
		p.compressIfEnabled(chunk)
		req.OnDone(req.URL, chunk, nil)
	}

	var streamed bool
	var total int
	var err error
	switch format {
	case FormatWebPAnim:
		streamed, total, err = decodeWebPAnimStream(req.Data, maxH, emit)
	case FormatAVIFAnim:
		streamed, total, err = decodeAVIFAnimStream(req.Data, maxH, emit)
	default:
		streamed = false
		err = fmt.Errorf("assets: streamAnimated on non-streamable format %s", format)
	}

	if !streamed {
		// Decimation needed (rare): the classic full decode replaces the
		// establishing prefix and keeps spreadLoopDelays correct.
		start = time.Now()
		d, derr := DecodeImageSized(req.Data, req.PlayAnimations, maxH)
		if derr != nil {
			p.failed.Add(1)
		} else {
			d = p.fit(req.Type, d)
			p.compressIfEnabled(d)
			p.decoded.Add(1)
			foldEWMA(&p.decodeNsEWMA, time.Since(start))
		}
		req.OnDone(req.URL, d, derr)
		return
	}

	// Finalize: clear Partial on the resident page so the renderer stops
	// treating it as a growing prefix (finish a playOnce layer, report the
	// true duration). Sent even after a mid-stream decode failure — the frames
	// that decoded form the final page.
	req.OnDone(req.URL, &Decoded{
		Stream:       true,
		Partial:      false,
		FrameOffset:  total,
		SourceFrames: total,
	}, nil)

	if err != nil {
		p.failed.Add(1)
		log.Printf("[anim-decode] %s playAnim=%v maxH=%d ERR=%v sys=%dMiB took=%s",
			format, req.PlayAnimations, maxH, err, heapSysMiB(), time.Since(start).Round(time.Millisecond))
		return
	}
	p.decoded.Add(1)
	foldEWMA(&p.decodeNsEWMA, time.Since(start))
	log.Printf("[anim-decode] %s playAnim=%v maxH=%d -> streamed source=%d budget=%dMiB sys=%dMiB took=%s",
		format, req.PlayAnimations, maxH, total, maxAnimatedDecodedAssetBytes.Load()>>20, heapSysMiB(), time.Since(start).Round(time.Millisecond))
}

// DecoderStats is a point-in-time counter snapshot.
type DecoderStats struct {
	Decoded int64
	Failed  int64
	// AvgDecode is the decode+fit wall-time EWMA (cold-load profiling; zero
	// until the first successful decode).
	AvgDecode time.Duration
}

// Stats snapshots the pool's counters.
func (p *DecoderPool) Stats() DecoderStats {
	return DecoderStats{
		Decoded:   p.decoded.Load(),
		Failed:    p.failed.Load(),
		AvgDecode: time.Duration(p.decodeNsEWMA.Load()),
	}
}

// DecodeImage decodes a payload by sniffed format. Exported for benchmarks
// and the emote preview path; the client itself goes through the pool.
// DecodeImage decodes a payload by sniffed format with no decode-time downscale
// (maxH = 0). Exported for benchmarks and the emote preview path; the client
// goes through the pool, which passes its sprite cap via DecodeImageSized.
func DecodeImage(data []byte, playAnimations bool) (*Decoded, error) {
	return DecodeImageSized(data, playAnimations, 0)
}

// DecodeImageSized is DecodeImage with an optional aspect-preserving height cap
// (maxH). Animated decoders honour it by downscaling each frame DURING decode
// and by budgeting their frame count against the DOWNSCALED bytes, so a
// high-res clip keeps its full authored frame rate while its resident memory
// stays bounded by its on-screen size (#110). maxH <= 0 = keep native size.
func DecodeImageSized(data []byte, playAnimations bool, maxH int) (d *Decoded, err error) {
	format := Sniff(data)
	start := time.Now()
	if format == FormatWebPAnim || format == FormatAVIFAnim || format == FormatAPNG || format == FormatGIF {
		// #110 diagnostic: log every animated decode (native→decoded size,
		// frame counts, budget, timing, error) so a choppy/static sprite can be
		// triaged from the console log alone.
		defer func() {
			if err != nil {
				log.Printf("[anim-decode] %s playAnim=%v maxH=%d ERR=%v sys=%dMiB took=%s",
					format, playAnimations, maxH, err, heapSysMiB(), time.Since(start).Round(time.Millisecond))
				return
			}
			if d != nil {
				log.Printf("[anim-decode] %s playAnim=%v maxH=%d -> %dx%d frames=%d source=%d animated=%v budget=%dMiB sys=%dMiB took=%s",
					format, playAnimations, maxH, d.Width, d.Height, len(d.Frames), d.SourceFrames, d.Animated,
					maxAnimatedDecodedAssetBytes.Load()>>20, heapSysMiB(), time.Since(start).Round(time.Millisecond))
			}
		}()
	}
	switch format {
	case FormatPNG:
		return decodePNG(data)
	case FormatAPNG:
		return decodeAPNG(data, playAnimations, maxH)
	case FormatGIF:
		return decodeGIF(data, playAnimations, maxH)
	case FormatJPEG:
		return decodeJPEG(data)
	case FormatWebP, FormatWebPAnim:
		return decodeWebP(data, playAnimations, maxH)
	case FormatAVIF, FormatAVIFAnim:
		return decodeAVIF(data, playAnimations, maxH)
	default:
		return nil, fmt.Errorf("assets: unrecognized image payload (%d bytes, magic %s)", len(data), format)
	}
}

// --- Static stdlib formats ----------------------------------------------------

func decodePNG(data []byte) (*Decoded, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("assets: png decode: %w", err)
	}
	return staticDecoded(img), nil
}

func decodeJPEG(data []byte) (*Decoded, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("assets: jpeg decode: %w", err)
	}
	return staticDecoded(img), nil
}

// staticDecoded wraps a single still image, converting to RGBA when the
// decoder produced another color model.
func staticDecoded(img image.Image) *Decoded {
	rgba, token := toRGBA(img)
	d := &Decoded{
		Frames:   []*image.RGBA{rgba},
		Delays:   []time.Duration{0},
		Animated: false,
		Width:    rgba.Rect.Dx(),
		Height:   rgba.Rect.Dy(),
	}
	if token != nil {
		d.pooledPix = append(d.pooledPix, token)
	}
	return d
}

// toRGBA returns img as *image.RGBA, drawing into a pooled buffer when a
// conversion is needed. The second return is the pool token (nil when img
// was already RGBA and is used as-is).
func toRGBA(img image.Image) (*image.RGBA, *[]byte) {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, nil
	}
	bounds := img.Bounds()
	rgba, token := newPooledRGBA(bounds.Dx(), bounds.Dy())
	draw.Draw(rgba, rgba.Rect, img, bounds.Min, draw.Src)
	return rgba, token
}

// newPooledRGBA builds a w×h RGBA image whose Pix comes from the pixel pool.
func newPooledRGBA(w, h int) (*image.RGBA, *[]byte) {
	const bytesPerPixel = 4
	n := w * h * bytesPerPixel
	buf, token := getPixBuf(n)
	return &image.RGBA{
		Pix:    buf,
		Stride: w * bytesPerPixel,
		Rect:   image.Rect(0, 0, w, h),
	}, token
}

// --- GIF -----------------------------------------------------------------------

// decodeGIF composes a multi-frame GIF onto a persistent canvas, honoring
// per-frame disposal, producing full-canvas RGBA frames the render loop can
// flip between with zero work.
func decodeGIF(data []byte, playAnimations bool, maxH int) (*Decoded, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("assets: gif decode: %w", err)
	}
	if len(g.Image) == 0 {
		return nil, fmt.Errorf("assets: gif has no frames")
	}

	width, height := g.Config.Width, g.Config.Height
	if width == 0 || height == 0 {
		b := g.Image[0].Bounds()
		width, height = b.Max.X, b.Max.Y
	}

	total := len(g.Image)
	animated := total > 1
	walk := total // frames to composite: every authored frame (downscale-to-fit, no dropping)
	if !playAnimations {
		walk = 1
	}
	tw, th, down := decodeTargetDimsBudgeted(width, height, maxH, walk)
	keep := boundedFrameCount(tw, th, walk) // safety net; == walk after downscale-to-fit
	dec := newFrameDecimator(walk, keep)
	sourceDelays := make([]time.Duration, walk)
	for i := 0; i < walk; i++ {
		sourceDelays[i] = gifFrameDelay(g, i)
	}

	d := &Decoded{
		Animated:     animated,
		Width:        tw,
		Height:       th,
		SourceFrames: walk, // frame space the sender's networked frame effects index into (#17)
		Frames:       make([]*image.RGBA, 0, keep),
		Delays:       make([]time.Duration, 0, keep),
	}

	// canvas accumulates composition; the backdrop snapshot supports
	// DisposalPrevious. Both come from the pixel pool and go back at the
	// end — animated decodes allocate only their output frames.
	canvas, canvasTok := newPooledRGBA(width, height)
	defer putPixBuf(canvasTok)
	var prevSnapshot *image.RGBA
	var snapTok *[]byte
	defer func() { putPixBuf(snapTok) }()

	// Compose EVERY source frame onto the canvas (a kept frame's pixels depend
	// on the disposal of the ones before it); copy out only the decimated subset.
	for i := 0; i < walk; i++ {
		frame := g.Image[i]
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}

		if disposal == gif.DisposalPrevious {
			if prevSnapshot == nil {
				prevSnapshot, snapTok = newPooledRGBA(width, height)
			}
			copy(prevSnapshot.Pix, canvas.Pix)
		}

		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)

		if folded, keepIt := dec.step(i, gifFrameDelay(g, i)); keepIt {
			out, token := newPooledRGBA(width, height)
			copy(out.Pix, canvas.Pix)
			if down {
				small, smallTok := downscaleFrame(out, tw, th)
				putPixBuf(token)
				d.Frames = append(d.Frames, small)
				if smallTok != nil {
					d.pooledPix = append(d.pooledPix, smallTok)
				}
			} else {
				d.Frames = append(d.Frames, out)
				if token != nil {
					d.pooledPix = append(d.pooledPix, token)
				}
			}
			d.Delays = append(d.Delays, folded)
		}

		switch disposal {
		case gif.DisposalBackground:
			clearRect(canvas, frame.Bounds())
		case gif.DisposalPrevious:
			if prevSnapshot != nil {
				copy(canvas.Pix, prevSnapshot.Pix)
			}
		}
	}
	spreadLoopDelays(d, sourceDelays)
	return d, nil
}

func gifFrameDelay(g *gif.GIF, i int) time.Duration {
	if i >= len(g.Delay) {
		return defaultZeroFrameDelay
	}
	delay := time.Duration(g.Delay[i]) * gifDelayUnit
	if delay <= 0 {
		return defaultZeroFrameDelay
	}
	return delay
}

// clearRect zeroes a rectangle of canvas to transparent black.
func clearRect(canvas *image.RGBA, r image.Rectangle) {
	r = r.Intersect(canvas.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		rowStart := canvas.PixOffset(r.Min.X, y)
		rowEnd := canvas.PixOffset(r.Max.X, y)
		row := canvas.Pix[rowStart:rowEnd]
		for i := range row {
			row[i] = 0
		}
	}
}

// --- APNG ----------------------------------------------------------------------

// decodeAPNG composes APNG frames (offsets, dispose ops, blend ops) onto a
// persistent canvas, mirroring the GIF path.
func decodeAPNG(data []byte, playAnimations bool, maxH int) (*Decoded, error) {
	a, err := apng.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("assets: apng decode: %w", err)
	}
	if len(a.Frames) == 0 {
		return nil, fmt.Errorf("assets: apng has no frames")
	}

	first := a.Frames[0].Image.Bounds()
	width, height := first.Dx(), first.Dy()

	// Frames flagged IsDefault are the static fallback image, not part of
	// the animation proper.
	animFrames := make([]apng.Frame, 0, len(a.Frames))
	for _, f := range a.Frames {
		if !f.IsDefault {
			animFrames = append(animFrames, f)
		}
	}
	if len(animFrames) == 0 {
		animFrames = a.Frames
	}

	total := len(animFrames)
	animated := total > 1
	walk := total // compose every authored frame (downscale-to-fit, no dropping)
	if !playAnimations {
		walk = 1
	}
	tw, th, down := decodeTargetDimsBudgeted(width, height, maxH, walk)
	keep := boundedFrameCount(tw, th, walk) // safety net; == walk after downscale-to-fit
	dec := newFrameDecimator(walk, keep)
	sourceDelays := make([]time.Duration, walk)
	for i := 0; i < walk; i++ {
		sourceDelays[i] = apngFrameDelay(animFrames[i])
	}

	d := &Decoded{
		Animated:     animated,
		Width:        tw,
		Height:       th,
		SourceFrames: walk, // frame space the sender's networked frame effects index into (#17)
		Frames:       make([]*image.RGBA, 0, keep),
		Delays:       make([]time.Duration, 0, keep),
	}

	canvas, canvasTok := newPooledRGBA(width, height)
	defer putPixBuf(canvasTok)
	var prevSnapshot *image.RGBA
	var snapTok *[]byte
	defer func() { putPixBuf(snapTok) }()

	// Compose every source frame; copy out only the decimated subset (a kept
	// frame depends on the dispose/blend of all frames before it).
	for i := 0; i < walk; i++ {
		frame := animFrames[i]
		target := image.Rect(
			frame.XOffset,
			frame.YOffset,
			frame.XOffset+frame.Image.Bounds().Dx(),
			frame.YOffset+frame.Image.Bounds().Dy(),
		)

		if frame.DisposeOp == apng.DISPOSE_OP_PREVIOUS {
			if prevSnapshot == nil {
				prevSnapshot, snapTok = newPooledRGBA(width, height)
			}
			copy(prevSnapshot.Pix, canvas.Pix)
		}

		op := draw.Over
		if frame.BlendOp == apng.BLEND_OP_SOURCE {
			op = draw.Src
		}
		draw.Draw(canvas, target, frame.Image, frame.Image.Bounds().Min, op)

		if folded, keepIt := dec.step(i, apngFrameDelay(frame)); keepIt {
			out, token := newPooledRGBA(width, height)
			copy(out.Pix, canvas.Pix)
			if down {
				small, smallTok := downscaleFrame(out, tw, th)
				putPixBuf(token)
				d.Frames = append(d.Frames, small)
				if smallTok != nil {
					d.pooledPix = append(d.pooledPix, smallTok)
				}
			} else {
				d.Frames = append(d.Frames, out)
				if token != nil {
					d.pooledPix = append(d.pooledPix, token)
				}
			}
			d.Delays = append(d.Delays, folded)
		}

		switch frame.DisposeOp {
		case apng.DISPOSE_OP_BACKGROUND:
			clearRect(canvas, target)
		case apng.DISPOSE_OP_PREVIOUS:
			if prevSnapshot != nil {
				copy(canvas.Pix, prevSnapshot.Pix)
			}
		}
	}
	spreadLoopDelays(d, sourceDelays)
	return d, nil
}

func apngFrameDelay(f apng.Frame) time.Duration {
	num := f.DelayNumerator
	den := f.DelayDenominator
	if den == 0 {
		den = 100 // APNG spec: zero denominator means 1/100 s units
	}
	delay := time.Duration(num) * time.Second / time.Duration(den)
	if delay <= 0 {
		return defaultZeroFrameDelay
	}
	return delay
}
