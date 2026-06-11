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

	// maxDecodedAssetBytes caps ONE asset's decoded payload (Σ w×h×4 across
	// frames) at half the T1 texture budget. Pages above the whole budget
	// can never become resident (ByteBudgetLRU rejects them) and one page
	// near it would evict everything else — long community animations
	// (hundreds of full-canvas frames) hit exactly that and rendered as
	// invisible characters. Decoders truncate to the frames that fit: a
	// shorter loop beats a sprite that never shows up.
	maxDecodedAssetBytes = cache.DefaultT1BudgetBytes / 2
)

// boundedFrameCount truncates an animation to the frames whose decoded
// bytes fit maxDecodedAssetBytes — never below one frame (a single canvas
// larger than the budget fails at upload with a clear error instead).
func boundedFrameCount(width, height, frames int) int {
	canvasBytes := width * height * rgbaBytesPerPixel
	if canvasBytes <= 0 {
		return frames
	}
	maxFrames := maxDecodedAssetBytes / canvasBytes
	if maxFrames < 1 {
		maxFrames = 1
	}
	if frames > maxFrames {
		return maxFrames
	}
	return frames
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
			d, err := DecodeImage(req.Data, req.PlayAnimations)
			if err != nil {
				p.failed.Add(1)
			} else {
				p.decoded.Add(1)
			}
			req.OnDone(req.URL, d, err)
		case <-p.stop:
			return
		}
	}
}

// DecoderStats is a point-in-time counter snapshot.
type DecoderStats struct {
	Decoded int64
	Failed  int64
}

// Stats snapshots the pool's counters.
func (p *DecoderPool) Stats() DecoderStats {
	return DecoderStats{Decoded: p.decoded.Load(), Failed: p.failed.Load()}
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

	animated := len(g.Image) > 1
	frameCount := boundedFrameCount(width, height, len(g.Image))
	if !playAnimations {
		frameCount = 1
	}

	d := &Decoded{
		Animated: animated,
		Width:    width,
		Height:   height,
		Frames:   make([]*image.RGBA, 0, frameCount),
		Delays:   make([]time.Duration, 0, frameCount),
	}

	// canvas accumulates composition; the backdrop snapshot supports
	// DisposalPrevious. Both come from the pixel pool and go back at the
	// end — animated decodes allocate only their output frames.
	canvas, canvasTok := newPooledRGBA(width, height)
	defer putPixBuf(canvasTok)
	var prevSnapshot *image.RGBA
	var snapTok *[]byte
	defer func() { putPixBuf(snapTok) }()

	for i := 0; i < frameCount; i++ {
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

		out, token := newPooledRGBA(width, height)
		copy(out.Pix, canvas.Pix)
		d.Frames = append(d.Frames, out)
		if token != nil {
			d.pooledPix = append(d.pooledPix, token)
		}
		d.Delays = append(d.Delays, gifFrameDelay(g, i))

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

	animated := len(animFrames) > 1
	frameCount := boundedFrameCount(width, height, len(animFrames))
	if !playAnimations {
		frameCount = 1
	}

	d := &Decoded{
		Animated: animated,
		Width:    width,
		Height:   height,
		Frames:   make([]*image.RGBA, 0, frameCount),
		Delays:   make([]time.Duration, 0, frameCount),
	}

	canvas, canvasTok := newPooledRGBA(width, height)
	defer putPixBuf(canvasTok)
	var prevSnapshot *image.RGBA
	var snapTok *[]byte
	defer func() { putPixBuf(snapTok) }()

	for i := 0; i < frameCount; i++ {
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

		out, token := newPooledRGBA(width, height)
		copy(out.Pix, canvas.Pix)
		d.Frames = append(d.Frames, out)
		if token != nil {
			d.pooledPix = append(d.pooledPix, token)
		}
		d.Delays = append(d.Delays, apngFrameDelay(frame))

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
