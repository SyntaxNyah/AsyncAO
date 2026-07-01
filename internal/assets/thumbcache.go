package assets

import (
	"image"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	xdraw "golang.org/x/image/draw"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/webpenc"
)

// ThumbCache is the OPT-IN persistent low-quality sprite thumbnail store
// (power user, default OFF): a second, independent DiskCache holding a tiny
// (~1 KB) heavily-compressed WebP still of every character sprite that ever
// decodes, so a COLD sprite can show a low-q stand-in of the RIGHT character
// instantly while the full-quality one streams (the "shitty compressed 1 KB
// sprite" playtest idea). Separate from T3 by design — thumbs are ~100×
// smaller than the sprites they stand in for, so they stay useful long after
// the full asset was evicted ("more permanent, allows more stuff, REALLY low
// quality").
//
// Every path is bounded and off the hot loop (§17.4 / spec §2): Store and
// RequestLoad are non-blocking enqueues that DROP when their queue is full
// (thumbs are speculative — the paced heal retry re-asks), one goroutine
// encodes, one loads+decodes, and the render thread only drains Results()
// (upload is the caller's, on the render thread). A webpenc-less build
// (fallback) just never stores; loads of an empty cache miss harmlessly.
type ThumbCache struct {
	disk *cache.DiskCache

	jobs    chan thumbJob
	loads   chan string
	results chan ThumbLoaded

	enabled atomic.Bool
	height  atomic.Int32 // target thumb height px (ThumbHeightDefault when 0)
	quality atomic.Int32 // webp lossy quality (ThumbQualityDefault when 0)
	budget  atomic.Int64 // store byte budget (thumbBudgetDefaultBytes when 0); prune drops oldest past it

	stored  atomic.Int64 // encodes written (diagnostics)
	dropped atomic.Int64 // enqueues shed on a full queue (diagnostics)

	closeOnce sync.Once
	stop      chan struct{}
	done      sync.WaitGroup
}

// thumbJob carries one ALREADY-SHRUNK thumbnail frame to the encode worker.
// The shrink happens synchronously in Store (on the decode worker) because the
// source *Decoded's pixel buffers are POOLED — TextureStore.Upload releases
// them right after the GPU upload, so holding the original frame across a
// queue would read recycled memory. The small copy here is independently
// owned, so the encode worker can take its time.
type thumbJob struct {
	base  string
	small *image.RGBA
}

// ThumbLoaded is one loaded + decoded thumbnail, ready for the render thread
// to upload under its thumb:// T1 key.
type ThumbLoaded struct {
	Base  string
	Asset *Decoded
}

const (
	// thumbJobQueueCap / thumbLoadQueueCap / thumbResultCap bound the three
	// hand-off queues (full = drop; thumbs are speculative).
	thumbJobQueueCap  = 16
	thumbLoadQueueCap = 8
	thumbResultCap    = 8
	// ThumbHeightDefault / ThumbQualityDefault are the shipped knob defaults:
	// 64 px tall at webp q20 lands ~1 KB per sprite — visibly low-q (that's
	// the deal) but instantly recognisable.
	ThumbHeightDefault  = 64
	ThumbQualityDefault = 20
	// thumbFrameMs is the still frame's nominal duration inside the 1-frame
	// animated-WebP container webpenc produces (never played as animation).
	thumbFrameMs = 1000
	// thumbBudgetDefaultBytes bounds the store when the knob is unset: 64 MiB
	// ≈ ~60k thumbnails at the default params — effectively unlimited in
	// practice, but a hard ceiling by construction (§17.4).
	thumbBudgetDefaultBytes = 64 << 20
	// thumbPruneEvery is how many stores pass between prune sweeps (the sweep
	// also runs once at open). A dir walk every store would be wasteful; one
	// every N bounds the overshoot to N × ~1 KB.
	thumbPruneEvery = 64
)

// NewThumbCache opens (creating if needed) the thumbnail store rooted at root
// and starts its two workers. Call Close to drain and stop.
func NewThumbCache(root string) (*ThumbCache, error) {
	disk, err := cache.NewDiskCache(root)
	if err != nil {
		return nil, err
	}
	t := &ThumbCache{
		disk:    disk,
		jobs:    make(chan thumbJob, thumbJobQueueCap),
		loads:   make(chan string, thumbLoadQueueCap),
		results: make(chan ThumbLoaded, thumbResultCap),
		stop:    make(chan struct{}),
	}
	t.done.Add(2)
	go t.encodeWorker()
	go t.loadWorker()
	return t, nil
}

// SetEnabled flips the whole feature (mirrored from the pref). Off = every
// entry point returns immediately; nothing is read, encoded or written.
func (t *ThumbCache) SetEnabled(on bool) { t.enabled.Store(on) }

// Enabled reports the live toggle.
func (t *ThumbCache) Enabled() bool { return t.enabled.Load() }

// SetParams tunes the thumb size + quality (mirrored from the prefs; 0 keeps
// the shipped default). Applies to NEW encodes; existing thumbs stay as
// written until re-encoded by a fresh decode.
func (t *ThumbCache) SetParams(heightPx, quality int) {
	t.height.Store(int32(heightPx))
	t.quality.Store(int32(quality))
}

// SetBudget caps the store's on-disk bytes (0 = the default 64 MiB). The next
// prune sweep (at open, then every thumbPruneEvery stores) drops the OLDEST
// thumbnails until under budget.
func (t *ThumbCache) SetBudget(bytes int64) { t.budget.Store(bytes) }

// Store shrinks a freshly-decoded sprite's first frame to thumb size and
// enqueues the small copy for encoding. The shrink runs HERE, synchronously on
// the calling decode worker, because d's pixel buffers are pooled and released
// after the texture upload — only an independently-owned copy may cross the
// queue (see thumbJob). At ~64 px the CatmullRom pass is a small fraction of
// the decode that just ran, and the whole path is opt-in. Non-blocking enqueue:
// a full queue drops the job (the next decode re-offers).
func (t *ThumbCache) Store(base string, d *Decoded) {
	if !t.enabled.Load() || d == nil || len(d.Frames) == 0 || d.Frames[0] == nil {
		return
	}
	src := d.Frames[0]
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw <= 0 || sh <= 0 {
		return
	}
	h, _ := t.params()
	w := sw * h / sh
	if w < 1 {
		w = 1
	}
	if sh < h { // never upscale a tiny sprite — store it as-is
		w, h = sw, sh
	}
	small := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(small, small.Rect, src, src.Rect, xdraw.Src, nil)
	select {
	case t.jobs <- thumbJob{base: base, small: small}:
	default:
		t.dropped.Add(1)
	}
}

// RequestLoad asks for base's thumbnail to be read + decoded; the result (if
// the thumb exists) lands on Results. Non-blocking; callers pace retries.
func (t *ThumbCache) RequestLoad(base string) {
	if !t.enabled.Load() || base == "" {
		return
	}
	select {
	case t.loads <- base:
	default:
		t.dropped.Add(1)
	}
}

// Results delivers loaded thumbnails; the render thread drains it and uploads
// each under its thumb:// key.
func (t *ThumbCache) Results() <-chan ThumbLoaded { return t.results }

// Stored / Dropped are diagnostics counters (debug overlay material).
func (t *ThumbCache) Stored() int64  { return t.stored.Load() }
func (t *ThumbCache) Dropped() int64 { return t.dropped.Load() }

// Root exposes the thumbnail directory (Settings: open / size).
func (t *ThumbCache) Root() string { return t.disk.Root() }

// Clear wipes every stored thumbnail (the Settings button).
func (t *ThumbCache) Clear() error { return t.disk.Clear() }

// Close stops the workers and the underlying disk writer.
func (t *ThumbCache) Close() {
	t.closeOnce.Do(func() {
		close(t.stop)
		t.done.Wait()
		t.disk.Close()
	})
}

// params reads the live knobs with their defaults applied.
func (t *ThumbCache) params() (h, q int) {
	h, q = int(t.height.Load()), int(t.quality.Load())
	if h <= 0 {
		h = ThumbHeightDefault
	}
	if q <= 0 {
		q = ThumbQualityDefault
	}
	return h, q
}

// encodeWorker compresses each pre-shrunk job frame into one low-quality WebP
// still through webpenc, then hands the bytes to the disk tier's own async
// writer. A webpenc-less (fallback) build errors on New and the job is skipped
// — the feature degrades to "no thumbs", never a fault.
func (t *ThumbCache) encodeWorker() {
	defer t.done.Done()
	t.prune() // enforce the byte budget on whatever a previous session left
	sincePrune := 0
	for {
		select {
		case <-t.stop:
			return
		case job := <-t.jobs:
			sincePrune++
			if sincePrune >= thumbPruneEvery {
				sincePrune = 0
				t.prune()
			}
			_, q := t.params()
			w, h := job.small.Rect.Dx(), job.small.Rect.Dy()
			enc, err := webpenc.New(w, h, q, thumbFrameMs, false)
			if err != nil {
				continue // encoder unavailable (fallback build): no thumbs, no fault
			}
			if err := enc.AddFrame(job.small); err != nil {
				enc.Close()
				continue
			}
			blob, err := enc.Assemble()
			enc.Close()
			if err != nil || len(blob) == 0 {
				continue
			}
			t.disk.Put(job.base, blob) // the disk tier's own bounded async writer
			t.stored.Add(1)
		}
	}
}

// prune enforces the byte budget: when the store's files sum past it, the
// OLDEST (mtime) are deleted until under. Runs on the encode worker (off every
// hot path) at open and every thumbPruneEvery stores; a walk of even a full
// 64 MiB store (~60k tiny files) is an occasional background dir scan, not a
// per-frame cost. Errors are ignored per-file (a locked file just survives to
// the next sweep).
func (t *ThumbCache) prune() {
	budget := t.budget.Load()
	if budget <= 0 {
		budget = thumbBudgetDefaultBytes
	}
	root := t.disk.Root()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type fileAge struct {
		path string
		size int64
		mod  int64
	}
	var total int64
	files := make([]fileAge, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		files = append(files, fileAge{path: filepath.Join(root, e.Name()), size: info.Size(), mod: info.ModTime().UnixNano()})
	}
	if total <= budget {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod }) // oldest first
	for _, f := range files {
		if total <= budget {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// loadWorker reads + decodes requested thumbnails off-thread and offers them
// on results (non-blocking: a full results queue drops — the paced heal retry
// asks again while the sprite is still cold).
func (t *ThumbCache) loadWorker() {
	defer t.done.Done()
	for {
		select {
		case <-t.stop:
			return
		case base := <-t.loads:
			data, ok := t.disk.Get(base)
			if !ok {
				continue // no thumb yet — the full sprite has simply never decoded here
			}
			d, err := DecodeImage(data, false)
			if err != nil || d == nil || len(d.Frames) == 0 {
				continue
			}
			select {
			case t.results <- ThumbLoaded{Base: base, Asset: d}:
			default:
				t.dropped.Add(1)
			}
		}
	}
}
