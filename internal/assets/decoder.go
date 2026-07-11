package assets

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
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

// defaultMaxDecodedAssetBytes caps ONE asset's decoded payload (Σ w×h×4 across
// frames) at the default T1 texture budget's per-asset share. The cap fraction
// (and WHY it must stay well under the render main tier) is the single source of
// truth cache.MaxDecodedAssetBytes — see cache.decodeCapBudgetDiv: a page above
// the whole budget can never become resident (ByteBudgetLRU rejects it) and one
// page near the main tier evicts most of the on-screen working set (the
// stage-flash class). Long community animations (hundreds of full-canvas frames)
// hit exactly that and rendered as invisible characters; the decoders now
// DECIMATE to the frames that fit (frameDecimator) so a longer clip still spans
// its whole duration at a lower frame rate rather than truncating.
var defaultMaxDecodedAssetBytes = cache.MaxDecodedAssetBytes(cache.DefaultT1BudgetBytes)

// maxDecodedAssetBytes is the LIVE per-asset decode cap. It defaults to
// defaultMaxDecodedAssetBytes; SetMaxDecodedAssetBytes scales it off the user's
// actual texture budget (TexBudgetMiB) at startup via cache.MaxDecodedAssetBytes
// — the SAME fraction, so a bigger budget lets a longer animation decode in full
// (the "long animations skip to the end past ~5 s" report) without moving the
// shipped default or the eviction safety margin. Atomic: decode workers read it
// live.
var maxDecodedAssetBytes atomic.Int64

func init() { maxDecodedAssetBytes.Store(defaultMaxDecodedAssetBytes) }

// SetMaxDecodedAssetBytes sets the per-asset decode cap in bytes (<= 0 restores
// the default). Called once at startup from the texture budget.
func SetMaxDecodedAssetBytes(n int64) {
	if n <= 0 {
		n = defaultMaxDecodedAssetBytes
	}
	maxDecodedAssetBytes.Store(n)
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
		Animated: d.Animated,
		Width:    newW,
		Height:   maxH,
		Frames:   make([]*image.RGBA, 0, len(d.Frames)),
		Delays:   d.Delays,
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

// boundedFrameCount reports how many frames of an animation may stay resident:
// the count whose decoded bytes fit maxDecodedAssetBytes — never below one
// frame (a single canvas larger than the budget fails at upload with a clear
// error instead). The decoders honour this budget by DECIMATION, not
// truncation (see frameDecimator): the returned count is how many evenly-spaced
// frames to keep across the whole clip, not a prefix length.
func boundedFrameCount(width, height, frames int) int {
	canvasBytes := width * height * rgbaBytesPerPixel
	if canvasBytes <= 0 {
		return frames
	}
	maxFrames := int(maxDecodedAssetBytes.Load()) / canvasBytes
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

// DecodeWorkers returns the §8 worker-count formula.
func DecodeWorkers() int {
	n := runtime.NumCPU() / 2
	if n < minDecodeWorkers {
		return minDecodeWorkers
	}
	return n
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

// NewDecoderPool starts workers decode goroutines (DecodeWorkers() when
// workers <= 0).
func NewDecoderPool(workers int) *DecoderPool {
	if workers <= 0 {
		workers = DecodeWorkers()
	}
	p := &DecoderPool{
		jobs: make(chan DecodeRequest, decodeQueueCap),
		stop: make(chan struct{}),
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

// fit shrinks a freshly decoded asset to its on-screen ceiling: fixed-cell
// types (char icons / emote buttons) to a small square thumbnail, every other
// (full-size) type to the display-height sprite cap. Both are downscale-only,
// so already-small assets pass through untouched.
func (p *DecoderPool) fit(t AssetType, d *Decoded) *Decoded {
	if d == nil {
		return d
	}
	if target := decodeTargetPx(t); target > 0 {
		return downscaleDecoded(d, target) // square thumbnail (fixed cells)
	}
	if cap := int(p.spriteCap.Load()); cap > 0 {
		return downscaleDecodedAspect(d, cap) // aspect-preserving, height-bound
	}
	return d
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
	if req.PlayAnimations && sniffMaybeAnimated(req.Data) {
		if first, err := DecodeImage(req.Data, false); err == nil {
			// GIF/APNG can sniff "maybe" but decode static — only a real
			// animation benefits from the early frame (statics would just
			// upload the same texture twice).
			if first.Animated && len(first.Frames) > 0 {
				first = p.fit(req.Type, first)
				first.Partial = true
				req.OnDone(req.URL, first, nil)
			} else {
				first.Release()
			}
		}
	}

	start := time.Now()
	d, err := DecodeImage(req.Data, req.PlayAnimations)
	if err != nil {
		p.failed.Add(1)
	} else {
		d = p.fit(req.Type, d)
		p.decoded.Add(1)
		// Cold-load profiling: fold decode+fit wall time into the EWMA (weight
		// 1/4, same as the network TTFB) — the debug overlay's per-stage line.
		foldEWMA(&p.decodeNsEWMA, time.Since(start))
	}
	req.OnDone(req.URL, d, err)
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
func DecodeImage(data []byte, playAnimations bool) (*Decoded, error) {
	switch Sniff(data) {
	case FormatPNG:
		return decodePNG(data)
	case FormatAPNG:
		return decodeAPNG(data, playAnimations)
	case FormatGIF:
		return decodeGIF(data, playAnimations)
	case FormatJPEG:
		return decodeJPEG(data)
	case FormatWebP, FormatWebPAnim:
		return decodeWebP(data, playAnimations)
	case FormatAVIF, FormatAVIFAnim:
		return decodeAVIF(data, playAnimations)
	default:
		return nil, fmt.Errorf("assets: unrecognized image payload (%d bytes, magic %s)", len(data), Sniff(data))
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
func decodeGIF(data []byte, playAnimations bool) (*Decoded, error) {
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
	keep := boundedFrameCount(width, height, total)
	walk := total // frames to composite: the whole clip (decimation keeps a subset)
	if !playAnimations {
		walk, keep = 1, 1
	}
	dec := newFrameDecimator(walk, keep)

	d := &Decoded{
		Animated: animated,
		Width:    width,
		Height:   height,
		Frames:   make([]*image.RGBA, 0, keep),
		Delays:   make([]time.Duration, 0, keep),
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
			d.Frames = append(d.Frames, out)
			if token != nil {
				d.pooledPix = append(d.pooledPix, token)
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
func decodeAPNG(data []byte, playAnimations bool) (*Decoded, error) {
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
	keep := boundedFrameCount(width, height, total)
	walk := total // compose the whole clip; decimation keeps an evenly-spaced subset
	if !playAnimations {
		walk, keep = 1, 1
	}
	dec := newFrameDecimator(walk, keep)

	d := &Decoded{
		Animated: animated,
		Width:    width,
		Height:   height,
		Frames:   make([]*image.RGBA, 0, keep),
		Delays:   make([]time.Duration, 0, keep),
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
			d.Frames = append(d.Frames, out)
			if token != nil {
				d.pooledPix = append(d.pooledPix, token)
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
