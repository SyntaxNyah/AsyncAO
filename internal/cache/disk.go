package cache

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/cespare/xxhash/v2"
)

const (
	// CacheDirName is the directory under os.UserCacheDir holding all
	// AsyncAO cache data.
	CacheDirName = "AsyncAO"
	// AssetsSubdir holds the asset blobs inside CacheDirName.
	AssetsSubdir = "assets"

	// shardPrefixLen is how many hex characters of the key name the shard
	// subdirectory uses (PROMPT.md §9: assets/<xx>/<xxhash64-hex>).
	shardPrefixLen = 2

	// writeQueueCap bounds the async writer queue. When the queue is full,
	// new writes are dropped (cache writes are best-effort; hot paths never
	// block on disk — PROMPT.md §17.2, §17.4).
	writeQueueCap = 256

	diskDirPerm    = 0o755
	diskTmpPattern = "w-*.tmp"
	hashHexLen     = 16 // xxhash64 → 8 bytes → 16 hex chars
)

// Key hashes a full asset URL to its on-disk name. Hashing the complete URL
// (scheme, host, and path) keeps every server's assets in disjoint key
// space — two servers can never serve each other stale files.
func Key(url string) string {
	var buf [hashHexLen / 2]byte
	sum := xxhash.Sum64String(url)
	for i := range buf {
		buf[i] = byte(sum >> (8 * (len(buf) - 1 - i)))
	}
	return hex.EncodeToString(buf[:])
}

// diskWrite is one queued blob write.
type diskWrite struct {
	path string
	data []byte
}

// DiskCache is tier 3: an unbounded (user-clearable) on-disk blob store with
// a single async writer goroutine. Get is synchronous and intended for
// network/manager goroutines only — never the render, decode, or resolver
// paths. Put never blocks and never performs I/O on the caller's goroutine.
type DiskCache struct {
	root string

	queue     chan diskWrite
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	stopped   atomic.Bool

	hits        atomic.Int64
	misses      atomic.Int64
	writes      atomic.Int64
	writeErrors atomic.Int64
	dropped     atomic.Int64

	onError atomic.Pointer[func(error)]
}

// DefaultDiskRoot returns <os.UserCacheDir>/AsyncAO/assets.
func DefaultDiskRoot() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cache: locating user cache dir: %w", err)
	}
	return filepath.Join(dir, CacheDirName, AssetsSubdir), nil
}

// NewDiskCache opens (creating if needed) the disk tier rooted at root and
// starts its writer goroutine. Call Close to drain pending writes.
func NewDiskCache(root string) (*DiskCache, error) {
	if err := os.MkdirAll(root, diskDirPerm); err != nil {
		return nil, fmt.Errorf("cache: creating disk cache root %s: %w", root, err)
	}
	d := &DiskCache{
		root:  root,
		queue: make(chan diskWrite, writeQueueCap),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go d.writerLoop()
	return d, nil
}

// SetErrorHook installs fn to receive asynchronous write failures. The
// default hook logs via the standard logger.
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

// pathFor maps a URL to its blob path: <root>/<xx>/<xxhash64-hex>.
func (d *DiskCache) pathFor(url string) string {
	key := Key(url)
	return filepath.Join(d.root, key[:shardPrefixLen], key)
}

// Get reads the cached blob for url. A zero-length blob is treated as a miss
// and deleted (defensive: a torn write that survived a crash).
func (d *DiskCache) Get(url string) ([]byte, bool) {
	data, err := os.ReadFile(d.pathFor(url))
	if err != nil || len(data) == 0 {
		if err == nil {
			_ = os.Remove(d.pathFor(url))
		}
		d.misses.Add(1)
		return nil, false
	}
	d.hits.Add(1)
	return data, true
}

// Put queues data for asynchronous storage under url's key. Put takes
// ownership of data: the caller must not mutate the slice afterwards (the
// asset pipeline shares the same immutable payload with T2). When the writer
// queue is full or the cache is closed, the write is dropped and counted —
// callers are never blocked and results are never lost (only this
// speculative disk copy is).
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

// Delete removes the blob for url (e.g. the decoder found the payload
// corrupt and the manager wants a clean refetch).
func (d *DiskCache) Delete(url string) {
	_ = os.Remove(d.pathFor(url))
}

// Clear removes every cached blob ("Clear Disk Cache" button). The cache
// remains usable afterwards.
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

// Close stops accepting writes, drains the queue, and waits for the writer
// to exit. Safe to call multiple times.
func (d *DiskCache) Close() {
	d.closeOnce.Do(func() {
		d.stopped.Store(true)
		close(d.stop)
		<-d.done
	})
}

// writerLoop is the single goroutine allowed to write blobs.
func (d *DiskCache) writerLoop() {
	defer close(d.done)
	for {
		select {
		case w := <-d.queue:
			d.write(w)
		case <-d.stop:
			// Drain whatever was queued before Close, then exit.
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

// write lands one blob via temp file + rename so readers never observe a
// partial blob under the final name.
func (d *DiskCache) write(w diskWrite) {
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, diskDirPerm); err != nil {
		d.reportError(fmt.Errorf("cache: creating shard dir %s: %w", dir, err))
		return
	}
	tmp, err := os.CreateTemp(dir, diskTmpPattern)
	if err != nil {
		d.reportError(fmt.Errorf("cache: creating temp blob in %s: %w", dir, err))
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(w.data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		d.reportError(fmt.Errorf("cache: writing blob %s: %w", tmpName, err))
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		d.reportError(fmt.Errorf("cache: closing blob %s: %w", tmpName, err))
		return
	}
	if err := os.Rename(tmpName, w.path); err != nil {
		os.Remove(tmpName)
		d.reportError(fmt.Errorf("cache: publishing blob %s: %w", w.path, err))
		return
	}
	d.writes.Add(1)
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

// SizeOnDisk walks the cache and reports total blob bytes (Settings UI).
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
