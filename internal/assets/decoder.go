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
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

const (
	// decodeQueueCap bounds the decode job queue; Submit blocks briefly when full, since decode work is never shed.
	decodeQueueCap = 64

	// minDecodeWorkers is the floor of the worker count, max(2, NumCPU/2).
	minDecodeWorkers = 2

	// gifDelayUnit is the GIF frame-delay resolution.
	gifDelayUnit = 10 * time.Millisecond

	// defaultZeroFrameDelay replaces zero or negative frame delays, as browsers and webAO do.
	defaultZeroFrameDelay = 100 * time.Millisecond

	rgbaBytesPerPixel = 4
)

// defaultMaxAnimatedDecodedAssetBytes bounds one animated asset; frames downscale to fit, so long clips stay smooth.
var defaultMaxAnimatedDecodedAssetBytes = int64(cache.DefaultMaxAnimatedDecodedAssetBytes)

// maxAnimatedDecodedAssetBytes is the live per-animated-asset frame budget, read by decode workers.
var maxAnimatedDecodedAssetBytes atomic.Int64

func init() { maxAnimatedDecodedAssetBytes.Store(defaultMaxAnimatedDecodedAssetBytes) }

// SetAnimatedDecodedAssetBytes sets the live per-animated-asset frame budget; <= 0 resets to the default.
func SetAnimatedDecodedAssetBytes(bytes int64) {
	if bytes <= 0 {
		bytes = defaultMaxAnimatedDecodedAssetBytes
	}
	maxAnimatedDecodedAssetBytes.Store(bytes)
}

const (
	// charIconDecodePx and emoteButtonDecodePx mirror ui.iconCell and ui.emoteBtnCell (assets cannot import ui).
	// Packs ship icons at any size, and a 500x500 icon costs about 60x its 64 px cell in T1.
	charIconDecodePx    = 64
	emoteButtonDecodePx = 40
)

// decodeTargetPx returns the thumbnail edge for fixed-cell types; 0 keeps the native size.
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

// downscaleDecoded rescales every frame to target x target, the stretch the GPU did, and frees the source buffers.
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
		// Without SourceFrames, FrameKeepIndex reads "no decimation" and mis-maps networked frame effects.
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

// downscaleDecodedAspect shrinks every frame's height to maxH, off the render thread, once.
// A gentler per-frame draw ratio looks sharper, and a texture taller than the display only costs memory.
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

// decodeTargetDims returns a sprite's size after the height cap maxH, and whether a downscale is needed.
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

// budgetFitHeight returns the max height at which all frames fit the animated budget, or height if already fitting.
func budgetFitHeight(width, height, frames int) int {
	budget := maxAnimatedDecodedAssetBytes.Load()
	if budget <= 0 || width <= 0 || height <= 0 || frames <= 1 {
		return height
	}
	// frames * (width*th/height) * th * 4 <= budget, so th^2 <= budget * height / (frames * width * 4)
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

// decodeTargetDimsBudgeted is decodeTargetDims that also downscales until all frames fit the animated budget.
func decodeTargetDimsBudgeted(width, height, maxH, frames int) (tw, th int, downscale bool) {
	if frames <= 0 {
		frames = 1
	}
	if fitH := budgetFitHeight(width, height, frames); maxH <= 0 || fitH < maxH {
		maxH = fitH
	}
	return decodeTargetDims(width, height, maxH)
}

// downscaleFrame uses an area-average filter, as ApproxBiLinear skips source pixels and aliases.
// Alpha is averaged premultiplied so transparent pixels cannot darken an edge.
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

// boundedFrameCount reports how many evenly spaced frames fit the budget, at least one.
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

// Decimation keeps frames evenly spaced and folds skipped delays into the next kept one, so a preanim plays in full.
// Compositing decoders must still walk every source frame.

// frameKeepIndex maps kept-frame ordinal j to its source index, spanning both endpoints, strictly increasing.
func frameKeepIndex(j, total, keep int) int {
	if keep <= 1 {
		return 0
	}
	return (j*(total-1) + (keep-1)/2) / (keep - 1) // round(j*(total-1)/(keep-1))
}

// FrameKeepIndex maps a kept frame to its source index, as wire frame effects use the undecimated file.
// Non-positive args or keep > total return the identity so a malformed page cannot panic.
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

// frameDecimator picks source frames to keep, folding skipped delays; call step once per source frame, in order.
type frameDecimator struct {
	total, keep, next, kept int
	pending                 time.Duration
}

// newFrameDecimator keeps keep frames (clamped to [1,total]) out of total, starting with frame 0.
func newFrameDecimator(total, keep int) frameDecimator {
	keep = value.Clamp(keep, 1, total)
	return frameDecimator{total: total, keep: keep} // next == 0 keeps frame 0
}

// step reports whether frame i is kept; dur is then its folded delay, so total playback time equals the original.
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

// spreadLoopDelays evens kept delays when source delays were uniform, because the fold would make a loop judder.
// Non-uniform sources stay folded so a preanim's final hold stays on the last frame.
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

// DecodeWorkers returns the worker count, max(minDecodeWorkers, NumCPU/2).
func DecodeWorkers() int {
	n := runtime.NumCPU() / 2
	if n < minDecodeWorkers {
		return minDecodeWorkers
	}
	return n
}

// defaultAnimatedDecodeConcurrency bounds full animated decodes, since their per-frame RGBA is the memory spike.
const defaultAnimatedDecodeConcurrency = 2

// animGate is a resizable semaphore: a buffered channel cannot resize, so waiters re-check an atomic limit.
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

// SetLimit changes the bound live; a raised limit wakes waiters.
func (g *animGate) SetLimit(n int) {
	g.mu.Lock()
	g.limit.Store(int64(n))
	g.cond.Broadcast()
	g.mu.Unlock()
}

// acquire blocks until an animated decode may start.
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
	URL string
	// Data is the raw payload, treated as immutable.
	Data []byte
	// Type hints budget accounting; routing is by sniffed magic, never extension.
	Type AssetType
	// PlayAnimations decodes all frames when true, only the first when false.
	PlayAnimations bool
	// OnDone runs on a decoder goroutine: it must be cheap and must not touch SDL (hard rule 1).
	OnDone func(url string, d *Decoded, err error)
}

// DecoderPool decodes image payloads into plain RGBA with no SDL calls; upload happens on the render thread.
type DecoderPool struct {
	jobs      chan DecodeRequest
	stop      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once

	decoded atomic.Int64
	failed  atomic.Int64

	// spriteCap is the max height for full-size assets (0 = no cap); atomic as workers read while SDL stores.
	spriteCap atomic.Int64

	// animatedSpriteCap is an optional tighter height cap for animated assets (0 = off), applied post-decode in fit.
	animatedSpriteCap atomic.Int64

	// animGate bounds concurrent full animated decodes; the worker pool may be wider.
	animGate *animGate

	// texCompress is the live compression FOURCC (0 = off): the setting intersected with the renderer's formats.
	texCompress atomic.Uint32

	// decodeNsEWMA tracks decode+fit wall time.
	decodeNsEWMA atomic.Int64
}

// ewmaFoldWeightDen is the EWMA weight denominator (1/4, as the network TTFB EWMA).
const ewmaFoldWeightDen = 4

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

// After an animated burst, maybeReclaimHeap forces one GC from a decode worker, as the scavenger returns pages slowly.
// It must not call debug.FreeOSMemory: that stop-the-world scavenge would stall the render thread.

const (
	// reclaimHeapIdleThreshold is the HeapIdle bytes that trigger a forced GC.
	reclaimHeapIdleThreshold = 128 << 20
	// reclaimHeapMinInterval stops a burst of small animations thrashing the collector.
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

// heapSysMiB reports runtime.MemStats.Sys in MiB, logged per animated decode to show a burst's memory.
func heapSysMiB() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys >> 20
}

// NewDecoderPool starts workers decode goroutines (DecodeWorkers() when workers <= 0).
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

// SetSpriteCap sets the height ceiling for full-size assets; 0 disables it.
func (p *DecoderPool) SetSpriteCap(px int) {
	if px < 0 {
		px = 0
	}
	p.spriteCap.Store(int64(px))
}

// SetAnimatedSpriteCap sets the animated-only height cap; 0 disables it.
func (p *DecoderPool) SetAnimatedSpriteCap(px int) {
	if px < 0 {
		px = 0
	}
	p.animatedSpriteCap.Store(int64(px))
}

// SetAnimatedBudgetMiB sets the per-animated-asset frame budget; <= 0 resets to the default.
func (p *DecoderPool) SetAnimatedBudgetMiB(mib int) {
	SetAnimatedDecodedAssetBytes(int64(mib) << 20)
}

// SetAnimatedDecodeConcurrency sets the concurrent animated decode bound.
func (p *DecoderPool) SetAnimatedDecodeConcurrency(n int) {
	p.animGate.SetLimit(n)
}

// SetTextureCompression sets the compression mode (CompressOff, CompressDXT5, CompressDXT1) for new decodes.
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

// compressIfEnabled compresses in place when a DXT mode is active and the canvas is 4-aligned; idempotent.
func (p *DecoderPool) compressIfEnabled(d *Decoded) {
	if d == nil {
		return
	}
	if f := p.texCompress.Load(); f != 0 {
		d.compress(f)
	}
}

// fit downscales to the ceiling: fixed-cell types to a square, others to the sprite cap or the animated cap when set.
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

// fullSizeMaxH is the decode-time height cap for a full-size asset, 0 for fixed-cell types, which downscale in fit.
func (p *DecoderPool) fullSizeMaxH(t AssetType) int {
	if decodeTargetPx(t) > 0 {
		return 0
	}
	return int(p.spriteCap.Load())
}

// Submit queues a decode; a closed pool returns false and calls OnDone inline, so callers always hear back.
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

// Close stops the workers and fails queued jobs so nobody waits forever.
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
	// The render thread's crash guard cannot see this goroutine: log any panic to disk, then re-panic.
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

// runJob decodes one payload; animated payloads deliver frame 0 first, then the full set replaces it at upload.
func (p *DecoderPool) runJob(req DecodeRequest) {
	animated := req.PlayAnimations && sniffMaybeAnimated(req.Data)
	streamable := animated && streamableFormat(req.Data)

	// Hold the permit for the whole run, including frame 0, or native-frame allocations spike RSS to multiple GiB.
	if animated {
		p.animGate.acquire()
		defer p.animGate.release()
	}

	if animated {
		if first, err := DecodeImage(req.Data, false); err == nil {
			// GIF/APNG can sniff "maybe" but decode static, and a static would upload the same texture twice.
			if first.Animated && len(first.Frames) > 0 {
				if streamable {
					// Match the frame size of the stream's appends; fit below still applies any tighter animated cap.
					first = fitEstablishingPrefix(first, req.Data, p.fullSizeMaxH(req.Type))
				}
				first = p.fit(req.Type, first)
				p.compressIfEnabled(first)
				first.Partial = true
				if streamable {
					// Streaming pages grow frame by frame; frame 0 is the first chunk.
					first.Stream = true
				}
				req.OnDone(req.URL, first, nil)
			} else {
				first.Release()
			}
		}
	}

	// The definitely-animated CGO formats stream: frames land one at a time and the page plays what has arrived.
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
		foldEWMA(&p.decodeNsEWMA, time.Since(start))
	}
	req.OnDone(req.URL, d, err)

	// GIF/APNG only: the streaming path reclaims in streamAnimated.
	if animated {
		maybeReclaimHeap()
	}
}

// sniffMaybeAnimated reports payloads worth a first frame: animated containers plus GIF/APNG, which need a decode.
func sniffMaybeAnimated(data []byte) bool {
	switch Sniff(data) {
	case FormatWebPAnim, FormatAVIFAnim, FormatAPNG, FormatGIF:
		return true
	default:
		return false
	}
}

// streamableFormat reports the CGO containers that decode incrementally; GIF and APNG decode all frames up front.
func streamableFormat(data []byte) bool {
	switch Sniff(data) {
	case FormatWebPAnim, FormatAVIFAnim:
		return true
	default:
		return false
	}
}

// peekAnimatedDims returns an animated payload's canvas size and frame count without decoding pixels.
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

// fitEstablishingPrefix downscales frame 0 to the appends' dimensions, so the clip has one resolution.
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

// streamAnimated appends each remaining WebP/AVIF frame; if decimation is needed it falls back to a full decode.
func (p *DecoderPool) streamAnimated(req DecodeRequest) {
	format := Sniff(req.Data)
	maxH := p.fullSizeMaxH(req.Type)
	start := time.Now()
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
		// Decimation needed: the full decode replaces frame 0 and keeps spreadLoopDelays correct.
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

	// Clear Partial so the page stops growing; sent even after a failure, as the decoded frames form the final page.
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
	// AvgDecode is the decode+fit wall-time EWMA, zero until the first success.
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

// DecodeImage decodes a payload by sniffed format with no downscale; the client itself goes through the pool.
func DecodeImage(data []byte, playAnimations bool) (*Decoded, error) {
	return DecodeImageSized(data, playAnimations, 0)
}

// DecodeImageSized is DecodeImage with a height cap (<= 0 keeps native size) that animated decoders apply per frame.
func DecodeImageSized(data []byte, playAnimations bool, maxH int) (d *Decoded, err error) {
	format := Sniff(data)
	start := time.Now()
	if format == FormatWebPAnim || format == FormatAVIFAnim || format == FormatAPNG || format == FormatGIF {
		// Log every animated decode so a choppy or static sprite can be diagnosed from the console log.
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

// staticDecoded wraps a still image, converting to RGBA when needed.
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

// toRGBA returns img as *image.RGBA; the pool token is nil when img was already RGBA.
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

// decodeGIF composes a GIF onto a persistent canvas, honoring disposal, producing full-canvas RGBA frames.
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

	// The backdrop snapshot supports DisposalPrevious.
	canvas, canvasTok := newPooledRGBA(width, height)
	defer putPixBuf(canvasTok)
	var prevSnapshot *image.RGBA
	var snapTok *[]byte
	defer func() { putPixBuf(snapTok) }()

	// Compose every source frame, because a kept frame depends on the disposal before it; copy out only the kept ones.
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

// decodeAPNG composes APNG frames onto a persistent canvas, as decodeGIF does.
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

	// Frames flagged IsDefault are the static fallback image, not part of the animation.
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

	// Compose every source frame, because a kept frame depends on the dispose and blend before it.
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
