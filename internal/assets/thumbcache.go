package assets

import (
	"errors"
	"image"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	xdraw "golang.org/x/image/draw"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/webpenc"
)

// ThumbCache is the opt-in persistent store of tiny WebP sprite stills, shown while a cold sprite streams.
// Store and RequestLoad never block and drop when full, since thumbnails are speculative.
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

// thumbJob carries an already-shrunk frame, because source buffers are pooled and released after upload.
type thumbJob struct {
	base  string
	small *image.RGBA
}

// ThumbLoaded is one decoded thumbnail, ready to upload under its thumb:// T1 key.
type ThumbLoaded struct {
	Base  string
	Asset *Decoded
}

const (
	// thumbJobQueueCap, thumbLoadQueueCap and thumbResultCap bound the hand-off queues; a full queue drops.
	thumbJobQueueCap  = 16
	thumbLoadQueueCap = 8
	thumbResultCap    = 8
	// ThumbHeightDefault and ThumbQualityDefault give about 1 KB per sprite: visibly low quality but recognisable.
	ThumbHeightDefault  = 64
	ThumbQualityDefault = 20
	// thumbFrameMs is the still's nominal duration in webpenc's 1-frame animated container; it never plays.
	thumbFrameMs = 1000
	// thumbBudgetDefaultBytes bounds the store when unset (hard rule 4), about 60k thumbnails.
	thumbBudgetDefaultBytes = 64 << 20
	// thumbPruneEvery is how many stores pass between prune sweeps, bounding overshoot to N x about 1 KB.
	thumbPruneEvery = 64
)

// NewThumbCache opens the store at root and starts its two workers; call Close to stop.
func NewThumbCache(root string) (*ThumbCache, error) {
	// 0 disables DiskCache's own prune, because ThumbCache runs its own over this store.
	disk, err := cache.NewDiskCache(root, 0)
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

// SetEnabled flips the whole feature; when off, every entry point returns immediately.
func (t *ThumbCache) SetEnabled(on bool) { t.enabled.Store(on) }

// Enabled reports the live toggle.
func (t *ThumbCache) Enabled() bool { return t.enabled.Load() }

// SetParams sets thumbnail size and quality for new encodes (0 keeps the default).
func (t *ThumbCache) SetParams(heightPx, quality int) {
	t.height.Store(int32(heightPx))
	t.quality.Store(int32(quality))
}

// SetBudget caps the store's on-disk bytes (0 = the default); the next sweep drops the oldest.
func (t *ThumbCache) SetBudget(bytes int64) { t.budget.Store(bytes) }

// Store shrinks frame 0 on the decode worker, as d's buffers are pooled, then enqueues the copy without blocking.
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

// RequestLoad asks for base's thumbnail without blocking; any result lands on Results, and callers pace retries.
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

// Results delivers loaded thumbnails to the render thread.
func (t *ThumbCache) Results() <-chan ThumbLoaded { return t.results }

// Stored and Dropped are diagnostics counters.
func (t *ThumbCache) Stored() int64  { return t.stored.Load() }
func (t *ThumbCache) Dropped() int64 { return t.dropped.Load() }

// Root returns the thumbnail directory.
func (t *ThumbCache) Root() string { return t.disk.Root() }

// Clear wipes every stored thumbnail.
func (t *ThumbCache) Clear() error { return t.disk.Clear() }

// Close stops the workers and the underlying disk writer.
func (t *ThumbCache) Close() {
	t.closeOnce.Do(func() {
		close(t.stop)
		t.done.Wait()
		t.disk.Close()
	})
}

// params reads the live settings with defaults applied.
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

// encodeWorker encodes each job into a WebP still for the disk writer; a build without webpenc skips the job.
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

// thumbFile is one entry of prune's directory snapshot.
type thumbFile struct {
	path string
	size int64
	mod  int64 // mtime, UnixNano — the prune order
}

// prune deletes the oldest files until under budget, on the encode worker; per-file errors are ignored.
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
	var total int64
	files := make([]thumbFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		files = append(files, thumbFile{path: filepath.Join(root, e.Name()), size: info.Size(), mod: info.ModTime().UnixNano()})
	}
	pruneToBudget(files, total, budget, os.Remove)
}

// pruneToBudget deletes oldest-first; split out so a file that vanished can be driven through the remove hook.
func pruneToBudget(files []thumbFile, total, budget int64, remove func(string) error) {
	if total <= budget {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod }) // oldest first
	for _, f := range files {
		if total <= budget {
			return
		}
		if err := remove(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
			total -= f.size
		}
	}
}

// loadWorker reads and decodes requested thumbnails off-thread; a full results queue drops.
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
