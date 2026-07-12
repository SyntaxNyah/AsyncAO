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
	// CacheDirName is the directory under os.UserCacheDir holding all
	// AsyncAO cache data.
	CacheDirName = "AsyncAO"
	// AssetsSubdir holds the asset blobs inside CacheDirName.
	AssetsSubdir = "assets"

	// shardPrefixLen is how many hex characters of the key name the shard
	// subdirectory uses (spec §9: assets/<xx>/<xxhash64-hex>).
	shardPrefixLen = 2

	// writeQueueCap bounds the async writer queue. When the queue is full,
	// new writes are dropped (cache writes are best-effort; hot paths never
	// block on disk — spec §17.2, §17.4).
	writeQueueCap = 256

	// pruneReqCap bounds the coalescing budget-change wake channel. Buffered
	// cap 1 is load-bearing: a wake sent by SetBudget before the writer reaches
	// its select must be RETAINED (not dropped) so the request survives the
	// race — a non-blocking send into a cap-1 buffer keeps at most one pending
	// prune and coalesces the rest. A larger cap would be pointless (extra
	// wakes ask for the same sweep); an unbuffered channel would drop wakes
	// whenever the writer is mid-write. #34: this is what lets a live Settings
	// budget change — or a change on a read-only session whose writer is parked
	// on an empty queue — actually enforce the cap instead of waiting for
	// diskPruneEvery more stores.
	pruneReqCap = 1

	diskDirPerm    = 0o755
	diskTmpPattern = "w-*.tmp"
	hashHexLen     = 16 // xxhash64 → 8 bytes → 16 hex chars

	// diskPruneEvery is how many stored blobs pass between byte-budget sweeps
	// (the sweep also runs once at open). A directory walk of a multi-GB shard
	// tree is an occasional background scan on the single writer goroutine —
	// off every hot path — never a per-write cost. Mirrors the ThumbCache's
	// thumbPruneEvery idiom (internal/assets/thumbcache.go).
	diskPruneEvery = 64
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

// diskWrite is one queued blob operation. del=true removes the blob at path
// (a corrupt-payload purge); otherwise it writes data. Ordering is preserved
// by the single FIFO writer, so a write followed by a delete of the same key
// lands in that order.
type diskWrite struct {
	path string
	data []byte
	del  bool
}

// DiskCache is tier 3: an unbounded (user-clearable) on-disk blob store with
// a single async writer goroutine. Get is synchronous and intended for
// network/manager goroutines only — never the render, decode, or resolver
// paths. Put never blocks and never performs I/O on the caller's goroutine.
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

	// compress turns on zstd for NEW blobs (Settings toggle, default off).
	// Reads always sniff the zstd magic, so mixed caches — and toggling
	// at any time — work forever without migration.
	compress atomic.Bool

	// budget is the auto-prune byte cap (Settings slider). 0 = UNLIMITED,
	// the DEFAULT — T3's unboundedness is a deliberate spec exception
	// (docs/FEATURES.md), so no user's cache is silently deleted by an
	// update. A positive value makes the writer goroutine sweep the OLDEST
	// (mtime) blobs past the cap. Read on the writer goroutine only.
	budget atomic.Int64

	onError atomic.Pointer[func(error)]
}

// zstd compression for the T3 tier: level-1 ("fastest") in the single
// writer goroutine, and a compressed blob is kept only when it actually
// shrank — WebP/AVIF sprites are pre-compressed and would only pay the
// decompress cost on every hit for nothing. INI/JSON/PNG payloads shrink
// 2–4×. Encoder/decoder with nil writers are documented safe for
// concurrent EncodeAll/DecodeAll use.
var (
	zstdEnc, _ = zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	zstdDec, _ = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
)

// zstdMagic is the standard zstd frame header (little-endian 0xFD2FB528);
// sniffing it makes the on-disk format self-describing.
var zstdMagic = [4]byte{0x28, 0xB5, 0x2F, 0xFD}

func isZstdBlob(data []byte) bool {
	return len(data) > len(zstdMagic) &&
		data[0] == zstdMagic[0] && data[1] == zstdMagic[1] &&
		data[2] == zstdMagic[2] && data[3] == zstdMagic[3]
}

// SetCompression toggles zstd for new writes (live-safe: reads sniff).
func (d *DiskCache) SetCompression(on bool) { d.compress.Store(on) }

// SetBudget sets the auto-prune byte cap. 0 = UNLIMITED (the default: T3 is a
// deliberate spec exception, so no cache is silently deleted). A positive cap
// makes the writer goroutine sweep the OLDEST blobs past it. Live-safe: the
// writer reads the value each sweep.
//
// The store is followed by a coalescing wake so the change is enforced NOW —
// not only after diskPruneEvery more stores land (and not never, on a
// read-only session whose writer is parked on the empty queue). The
// non-blocking send into the cap-1 buffer means SetBudget never blocks and
// duplicate wakes collapse to one pending sweep.
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

// NewDiskCache opens (creating if needed) the disk tier rooted at root and
// starts its writer goroutine. Call Close to drain pending writes.
//
// initialBudget is the auto-prune byte cap the at-open sweep enforces (#34):
// it is stored BEFORE the writer starts, so writerLoop's first d.prune() reads
// the real value instead of the atomic's default 0 — this is what makes the
// "at open" enforcement required by the spec actually fire on whatever a
// previous session left. 0 = unlimited (the deliberate T3 default). Pass 0
// from callers that will only ever set the budget live via SetBudget.
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

// Root exposes the cache directory (Settings: open-in-file-manager and
// size measurement — read-only use).
func (d *DiskCache) Root() string { return d.root }

// pathFor maps a URL to its blob path: <root>/<xx>/<xxhash64-hex>.
func (d *DiskCache) pathFor(url string) string {
	key := Key(url)
	return filepath.Join(d.root, key[:shardPrefixLen], key)
}

// Get reads the cached blob for url. A zero-length blob is treated as a miss
// and deleted (defensive: a torn write that survived a crash). Compressed
// blobs self-identify by the zstd magic and decompress transparently.
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
			// Corrupt frame: treat as a miss so the pipeline refetches a
			// clean copy over this path.
			_ = os.Remove(d.pathFor(url))
			d.misses.Add(1)
			return nil, false
		}
		data = raw
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
// corrupt and the manager wants a clean refetch). The removal is routed
// through the single async writer goroutine — like Put, it never performs
// disk I/O on the caller's goroutine, so a render/decode-path caller stays
// off the disk (spec §17.2). A full queue drops the delete (best-effort,
// like a dropped write); the negative cache still paces the refetch, so the
// worst case is one extra 30s window before the corrupt blob is retried.
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

// writerLoop is the single goroutine allowed to write blobs. It also owns the
// byte-budget prune (#34): a sweep at open, then every diskPruneEvery stores
// and on every SetBudget wake — all on THIS goroutine, so no new sync I/O path
// is introduced (spec §17.2).
func (d *DiskCache) writerLoop() {
	defer close(d.done)
	d.prune() // enforce the initial budget on whatever a previous session left
	sincePrune := 0
	for {
		select {
		case <-d.pruneReq:
			// A live budget change (SetBudget): enforce it now instead of
			// waiting for diskPruneEvery more stores — or forever, if this
			// session only reads. The store counter is unaffected.
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
// partial blob under the final name — or, for a delete op, removes the blob.
// It returns true when a blob was actually stored (a completed non-delete
// write), which writerLoop counts toward the next byte-budget sweep.
func (d *DiskCache) write(w diskWrite) bool {
	if w.del {
		// A missing file is a no-op success (nothing to purge); other errors
		// (permissions, locked file) surface through the error hook.
		if err := os.Remove(w.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			d.reportError(fmt.Errorf("cache: deleting blob %s: %w", w.path, err))
		}
		return false
	}
	if d.compress.Load() {
		// Compress on the writer goroutine (never the caller's); keep the
		// zstd frame only when it actually shrank the blob.
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

// diskFileAge is one blob's path, size, and mtime — the prune sweep's working
// record.
type diskFileAge struct {
	path string
	size int64
	mod  int64 // ModTime UnixNano; oldest sweeps out first
}

// prune enforces the byte budget (#34): when stored blobs sum past it, the
// OLDEST (mtime) are deleted until back under. budget <= 0 = UNLIMITED (the
// default), so the common case returns immediately with no walk. Runs ONLY on
// the writer goroutine (at open + every diskPruneEvery stores) so it never
// races another deleter and never touches disk on a hot-path caller. Errors
// are ignored per-file (a locked/racing file just survives to the next sweep);
// in-flight temp files are skipped so a partial write is never counted or
// deleted. Mirrors ThumbCache.prune, adapted to T3's sharded <xx>/<hash> tree.
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
