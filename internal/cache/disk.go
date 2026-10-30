package cache

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/cespare/xxhash/v2"
	"github.com/klauspost/compress/zstd"
)

const (
	// CacheDirName is the directory under os.UserCacheDir holding AsyncAO's cache data.
	CacheDirName = "AsyncAO"
	AssetsSubdir = "assets"

	// shardPrefixLen is how many hex characters name the shard directory: assets/<xx>/<xxhash64-hex>.
	shardPrefixLen = 2

	// writeQueueCap bounds the writer queue; when full, writes drop because hot paths never block on disk.
	writeQueueCap = 256

	// pruneReqCap must be 1: a wake before the writer's select must be kept, and extra wakes ask for the same sweep.
	pruneReqCap = 1

	diskDirPerm    = 0o755
	diskTmpPattern = "w-*.tmp"
	hashHexLen     = 16 // xxhash64 → 8 bytes → 16 hex chars

	// diskPruneEvery is how many stored blobs pass between sweeps; the walk runs only on the writer goroutine.
	diskPruneEvery = 64
)

// Key hashes a full URL to its on-disk name, so two servers can never serve each other stale files.
func Key(url string) string {
	var buf [hashHexLen / 2]byte
	sum := xxhash.Sum64String(url)
	for i := range buf {
		buf[i] = byte(sum >> (8 * (len(buf) - 1 - i)))
	}
	return hex.EncodeToString(buf[:])
}

// diskWrite is one queued blob operation; del=true removes the blob, and the single FIFO writer preserves order.
type diskWrite struct {
	path string
	data []byte
	del  bool
}

// DiskCache is tier 3: a user-clearable blob store with one writer goroutine.
// Get is synchronous, never for render, decode or resolver paths; Put never blocks or does I/O.
type DiskCache struct {
	root string

	queue     chan diskWrite
	pruneReq  chan struct{} // coalescing wake: SetBudget signals the writer to sweep now
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	stopped   atomic.Bool

	hits        atomic.Int64
	misses      atomic.Int64
	writes      atomic.Int64
	writeErrors atomic.Int64
	dropped     atomic.Int64

	// compress turns on zstd for new blobs; reads always sniff the magic, so mixed caches work without migration.
	compress atomic.Bool

	// budget is the auto-prune byte cap; 0 = unlimited, the default, so no cache is silently deleted.
	budget atomic.Int64

	onError atomic.Pointer[func(error)]
}

// Level-1 zstd on the writer goroutine, kept only if it shrank: WebP/AVIF would pay decompression for nothing.
// Nil-writer encoder and decoder are safe for concurrent EncodeAll/DecodeAll.
var (
	zstdEnc, _ = zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	zstdDec, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
)

// zstdMagic is the standard zstd frame header (little-endian 0xFD2FB528), which makes blobs self-describing.
var zstdMagic = [4]byte{0x28, 0xB5, 0x2F, 0xFD}

func isZstdBlob(data []byte) bool {
	return len(data) > len(zstdMagic) &&
		data[0] == zstdMagic[0] && data[1] == zstdMagic[1] &&
		data[2] == zstdMagic[2] && data[3] == zstdMagic[3]
}

// SetCompression toggles zstd for new writes.
func (d *DiskCache) SetCompression(on bool) { d.compress.Store(on) }

// SetBudget sets the byte cap (0 = unlimited) and wakes the writer so it applies even in a read-only session.
func (d *DiskCache) SetBudget(bytes int64) {
	d.budget.Store(bytes)
	select {
	case d.pruneReq <- struct{}{}:
	default: // a sweep is already pending; it will read the just-stored value
	}
}

// DefaultDiskRoot returns <os.UserCacheDir>/AsyncAO/assets.
func DefaultDiskRoot() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cache: locating user cache dir: %w", err)
	}
	return filepath.Join(dir, CacheDirName, AssetsSubdir), nil
}

// NewDiskCache opens the tier and starts its writer; initialBudget is set first so the at-open sweep sees it.
func NewDiskCache(root string, initialBudget int64) (*DiskCache, error) {
	if err := os.MkdirAll(root, diskDirPerm); err != nil {
		return nil, fmt.Errorf("cache: creating disk cache root %s: %w", root, err)
	}
	d := &DiskCache{
		root:     root,
		queue:    make(chan diskWrite, writeQueueCap),
		pruneReq: make(chan struct{}, pruneReqCap),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	d.budget.Store(initialBudget) // read by the at-open prune below, before any race
	go d.writerLoop()
	return d, nil
}

// SetErrorHook installs fn to receive asynchronous write failures; the default logs.
func (d *DiskCache) SetErrorHook(fn func(error)) {
	d.onError.Store(&fn)
}

func (d *DiskCache) reportError(err error) {
	d.writeErrors.Add(1)
	if fn := d.onError.Load(); fn != nil {
		(*fn)(err)
		return
	}
	log.Printf("cache: async disk write failed: %v", err)
}

// Root returns the cache directory, for read-only use.
func (d *DiskCache) Root() string { return d.root }

// pathFor maps a URL to its blob path: <root>/<xx>/<xxhash64-hex>.
func (d *DiskCache) pathFor(url string) string {
	key := Key(url)
	return filepath.Join(d.root, key[:shardPrefixLen], key)
}

// Get reads the blob for url; a zero-length blob is a torn write from a crash, so it is deleted and treated as a miss.
func (d *DiskCache) Get(url string) ([]byte, bool) {
	data, err := os.ReadFile(d.pathFor(url))
	if err != nil || len(data) == 0 {
		if err == nil {
			_ = os.Remove(d.pathFor(url))
		}
		d.misses.Add(1)
		return nil, false
	}
	if isZstdBlob(data) {
		raw, derr := zstdDec.DecodeAll(data, nil)
		if derr != nil {
			// Corrupt frame: treat as a miss so the pipeline refetches over this path.
			_ = os.Remove(d.pathFor(url))
			d.misses.Add(1)
			return nil, false
		}
		data = raw
	}
	d.hits.Add(1)
	return data, true
}

// Put queues data and takes ownership, as T2 shares the payload; a full queue or closed cache drops this copy.
func (d *DiskCache) Put(url string, data []byte) {
	if len(data) == 0 || d.stopped.Load() {
		d.dropped.Add(1)
		return
	}
	select {
	case d.queue <- diskWrite{path: d.pathFor(url), data: data}:
	default:
		d.dropped.Add(1)
	}
}

// Delete queues removal on the writer goroutine (hard rule 2); a full queue drops it, and the 404 cache paces retries.
func (d *DiskCache) Delete(url string) {
	if d.stopped.Load() {
		d.dropped.Add(1)
		return
	}
	select {
	case d.queue <- diskWrite{path: d.pathFor(url), del: true}:
	default:
		d.dropped.Add(1)
	}
}

// Clear removes every cached blob; the cache remains usable.
func (d *DiskCache) Clear() error {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return fmt.Errorf("cache: listing %s: %w", d.root, err)
	}
	var firstErr error
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(d.root, entry.Name())); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close stops accepting writes, drains the queue and waits for the writer; safe to call repeatedly.
func (d *DiskCache) Close() {
	d.closeOnce.Do(func() {
		d.stopped.Store(true)
		close(d.stop)
		<-d.done
	})
}

// writerLoop is the only blob writer and owns the prune: at open, every diskPruneEvery stores, on each SetBudget.
func (d *DiskCache) writerLoop() {
	defer close(d.done)
	d.prune() // enforce the initial budget on whatever a previous session left
	sincePrune := 0
	for {
		select {
		case <-d.pruneReq:
			d.prune()
		case w := <-d.queue:
			if d.write(w) { // write() returns true only for a completed store (never a delete)
				sincePrune++
				if sincePrune >= diskPruneEvery {
					sincePrune = 0
					d.prune()
				}
			}
		case <-d.stop:
			// Drain whatever was queued before Close.
			for {
				select {
				case w := <-d.queue:
					d.write(w)
				default:
					return
				}
			}
		}
	}
}

// write stores via temp file and rename so readers never see a partial blob; true when a blob was stored.
func (d *DiskCache) write(w diskWrite) bool {
	if w.del {
		// A missing file is success; other errors reach the error hook.
		if err := os.Remove(w.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			d.reportError(fmt.Errorf("cache: deleting blob %s: %w", w.path, err))
		}
		return false
	}
	if d.compress.Load() {
		if cz := zstdEnc.EncodeAll(w.data, nil); len(cz) < len(w.data) {
			w.data = cz
		}
	}
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, diskDirPerm); err != nil {
		d.reportError(fmt.Errorf("cache: creating shard dir %s: %w", dir, err))
		return false
	}
	tmp, err := os.CreateTemp(dir, diskTmpPattern)
	if err != nil {
		d.reportError(fmt.Errorf("cache: creating temp blob in %s: %w", dir, err))
		return false
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(w.data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		d.reportError(fmt.Errorf("cache: writing blob %s: %w", tmpName, err))
		return false
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		d.reportError(fmt.Errorf("cache: closing blob %s: %w", tmpName, err))
		return false
	}
	if err := os.Rename(tmpName, w.path); err != nil {
		os.Remove(tmpName)
		d.reportError(fmt.Errorf("cache: publishing blob %s: %w", w.path, err))
		return false
	}
	d.writes.Add(1)
	return true
}

// diskFileAge is one blob's path, size and mtime, for the prune sweep.
type diskFileAge struct {
	path string
	size int64
	mod  int64 // ModTime UnixNano; oldest sweeps out first
}

// prune deletes the oldest blobs until under budget; writer goroutine only, so it never races another deleter.
// Temp files are skipped and per-file errors ignored.
func (d *DiskCache) prune() {
	budget := d.budget.Load()
	if budget <= 0 {
		return // 0 = unlimited: the deliberate T3 default, never deletes
	}
	var total int64
	files := make([]diskFileAge, 0, 256)
	_ = filepath.WalkDir(d.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // racing a concurrent Clear is fine
			}
			return nil // an unreadable shard dir just isn't counted this sweep
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if filepath.Ext(path) == ".tmp" {
			return nil // an in-flight write (w-*.tmp): never count or evict it
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		files = append(files, diskFileAge{path: path, size: info.Size(), mod: info.ModTime().UnixNano()})
		return nil
	})
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

// DiskStats is a point-in-time counter snapshot.
type DiskStats struct {
	Hits        int64
	Misses      int64
	Writes      int64
	WriteErrors int64
	Dropped     int64
}

// Stats snapshots the disk tier's counters.
func (d *DiskCache) Stats() DiskStats {
	return DiskStats{
		Hits:        d.hits.Load(),
		Misses:      d.misses.Load(),
		Writes:      d.writes.Load(),
		WriteErrors: d.writeErrors.Load(),
		Dropped:     d.dropped.Load(),
	}
}

// SizeOnDisk walks the cache and reports total blob bytes.
func (d *DiskCache) SizeOnDisk() (int64, error) {
	var total int64
	err := filepath.WalkDir(d.root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // racing a concurrent Clear is fine
			}
			return err
		}
		if entry.Type().IsRegular() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}
