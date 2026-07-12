package cache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const diskSettleWait = 3 * time.Second

// --- ByteBudgetLRU -----------------------------------------------------------

func mustLRU[K comparable, V any](t *testing.T, maxEntries int, budget int64, onEvict EvictFunc[K, V]) *ByteBudgetLRU[K, V] {
	t.Helper()
	c, err := NewByteBudgetLRU(maxEntries, budget, onEvict)
	if err != nil {
		t.Fatalf("NewByteBudgetLRU: %v", err)
	}
	return c
}

func TestByteBudgetEvictsUntilUnderBudget(t *testing.T) {
	const budget = 100
	var evicted []string
	c := mustLRU(t, DefaultMaxEntries, int64(budget), func(key string, _ []byte, _ int64) {
		evicted = append(evicted, key)
	})

	c.Add("a", make([]byte, 40), 40)
	c.Add("b", make([]byte, 40), 40)
	if got := c.Bytes(); got != 80 {
		t.Fatalf("Bytes = %d, want 80", got)
	}

	// 50 more puts us at 130 > 100: "a" (oldest) must go, leaving 90.
	c.Add("c", make([]byte, 50), 50)
	if got := c.Bytes(); got != 90 {
		t.Errorf("Bytes after eviction = %d, want 90", got)
	}
	if len(evicted) != 1 || evicted[0] != "a" {
		t.Errorf("evicted = %v, want [a] (LRU order)", evicted)
	}
	if _, ok := c.Get("a"); ok {
		t.Error("evicted key still retrievable")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("key b lost")
	}
}

func TestByteBudgetGetBumpsRecency(t *testing.T) {
	c := mustLRU[string, int](t, DefaultMaxEntries, 100, nil)
	c.Add("a", 1, 40)
	c.Add("b", 2, 40)
	// Touch "a" so "b" becomes the eviction victim.
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a missing")
	}
	c.Add("c", 3, 40)
	if _, ok := c.Peek("a"); !ok {
		t.Error("recently-used a was evicted; recency bump broken")
	}
	if _, ok := c.Peek("b"); ok {
		t.Error("LRU b survived; eviction order broken")
	}
}

func TestByteBudgetReplaceAccountsExactly(t *testing.T) {
	var evictions int
	c := mustLRU(t, DefaultMaxEntries, int64(1000), func(string, []byte, int64) { evictions++ })
	c.Add("k", make([]byte, 100), 100)
	c.Add("k", make([]byte, 300), 300) // replace: old 100 must be released
	if got := c.Bytes(); got != 300 {
		t.Errorf("Bytes after replace = %d, want 300", got)
	}
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	if evictions != 1 {
		t.Errorf("evictions = %d, want 1 (replaced value must pass through eviction callback for texture destroy)", evictions)
	}
}

func TestByteBudgetRejectsOversized(t *testing.T) {
	c := mustLRU[string, []byte](t, DefaultMaxEntries, 100, nil)
	if c.Add("huge", make([]byte, 101), 101) {
		t.Error("Add accepted a value larger than the whole budget")
	}
	if c.Add("neg", nil, -1) {
		t.Error("Add accepted negative size")
	}
	if got := c.Bytes(); got != 0 {
		t.Errorf("Bytes = %d after rejected adds, want 0", got)
	}
}

func TestByteBudgetPurgeReleasesEverything(t *testing.T) {
	var evicted int
	c := mustLRU(t, DefaultMaxEntries, int64(1000), func(string, int, int64) { evicted++ })
	for i := 0; i < 5; i++ {
		c.Add(fmt.Sprintf("k%d", i), i, 10)
	}
	c.Purge()
	if c.Bytes() != 0 || c.Len() != 0 {
		t.Errorf("after Purge: bytes=%d len=%d, want 0/0", c.Bytes(), c.Len())
	}
	if evicted != 5 {
		t.Errorf("eviction callbacks = %d, want 5", evicted)
	}
}

func TestByteBudgetStats(t *testing.T) {
	c := mustLRU[string, int](t, DefaultMaxEntries, 100, nil)
	c.Add("a", 1, 10)
	c.Get("a")
	c.Get("missing")
	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 || s.Entries != 1 || s.Bytes != 10 || s.Budget != 100 {
		t.Errorf("Stats = %+v", s)
	}
}

func TestByteBudgetConcurrent(t *testing.T) {
	c := mustLRU[int, []byte](t, 64, 1<<20, func(int, []byte, int64) {})
	var wg sync.WaitGroup
	const goroutines = 8
	const ops = 500
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			payload := make([]byte, 1024)
			for i := 0; i < ops; i++ {
				key := (seed*ops + i) % 100
				switch i % 3 {
				case 0:
					c.Add(key, payload, int64(len(payload)))
				case 1:
					c.Get(key)
				case 2:
					c.Remove(key)
				}
			}
		}(g)
	}
	wg.Wait()
	if c.Bytes() < 0 {
		t.Errorf("byte accounting went negative: %d", c.Bytes())
	}
	if c.Bytes() > c.Budget() {
		t.Errorf("bytes %d exceed budget %d after settle", c.Bytes(), c.Budget())
	}
}

// TestGetZeroAllocs asserts the §15 alloc gate for cache hits outside of
// benchmarks so `go test` alone catches regressions.
func TestGetZeroAllocs(t *testing.T) {
	c := mustLRU[string, []byte](t, DefaultMaxEntries, 1<<20, nil)
	c.Add("k", make([]byte, 4096), 4096)
	allocs := testing.AllocsPerRun(1000, func() {
		if _, ok := c.Get("k"); !ok {
			t.Fatal("lost key")
		}
	})
	if allocs != 0 {
		t.Errorf("Get allocates %.1f objects per op, want 0", allocs)
	}
}

func BenchmarkCacheHit_T2(b *testing.B) {
	c, err := NewByteBudgetLRU[string, []byte](DefaultMaxEntries, DefaultT2BudgetBytes, nil)
	if err != nil {
		b.Fatal(err)
	}
	c.Add("k", make([]byte, 64<<10), 64<<10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.Get("k"); !ok {
			b.Fatal("miss")
		}
	}
}

// texturePage stands in for the render side's decoded-texture bundle: T1
// stores pointers, so hits must also be alloc-free.
type texturePage struct {
	frames []uintptr
	bytes  int64
}

func BenchmarkCacheHit_T1(b *testing.B) {
	c, err := NewByteBudgetLRU[string, *texturePage](DefaultMaxEntries, DefaultT1BudgetBytes, nil)
	if err != nil {
		b.Fatal(err)
	}
	page := &texturePage{frames: make([]uintptr, 4), bytes: 256 * 192 * 4}
	c.Add("char/sprite", page, page.bytes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.Get("char/sprite"); !ok {
			b.Fatal("miss")
		}
	}
}

// --- DiskCache ---------------------------------------------------------------

func newTestDisk(t *testing.T) *DiskCache {
	t.Helper()
	d, err := NewDiskCache(filepath.Join(t.TempDir(), AssetsSubdir))
	if err != nil {
		t.Fatalf("NewDiskCache: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

func waitForBlob(t *testing.T, d *DiskCache, url string) []byte {
	t.Helper()
	deadline := time.Now().Add(diskSettleWait)
	for time.Now().Before(deadline) {
		if data, ok := d.Get(url); ok {
			return data
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("blob for %s never appeared", url)
	return nil
}

func TestDiskPutGetRoundTrip(t *testing.T) {
	d := newTestDisk(t)
	const url = "http://assets.example.com/characters/phoenix/(a)normal.webp"
	payload := []byte("RIFFxxxxWEBPVP8X-animated-sprite-bytes")

	d.Put(url, payload)
	got := waitForBlob(t, d, url)
	if string(got) != string(payload) {
		t.Errorf("round trip mismatch: got %q", got)
	}
}

// TestDiskZstdRoundTrip pins the compressed tier: compressible payloads
// land as zstd frames yet read back verbatim, incompressible payloads stay
// raw (no decompress tax for pre-compressed sprites), and blobs written
// before the toggle keep reading after it (the format self-describes).
func TestDiskZstdRoundTrip(t *testing.T) {
	d := newTestDisk(t)

	const rawURL = "http://example.com/pre-toggle.webp"
	d.Put(rawURL, []byte("written-before-compression"))
	waitForBlob(t, d, rawURL)

	d.SetCompression(true)
	compressible := bytes.Repeat([]byte("courtroom_design.ini ftw "), 200)
	const czURL = "http://example.com/design.ini"
	d.Put(czURL, append([]byte(nil), compressible...))
	if got := waitForBlob(t, d, czURL); !bytes.Equal(got, compressible) {
		t.Fatalf("compressed round trip mismatch (%d bytes)", len(got))
	}
	onDisk, err := os.ReadFile(d.pathFor(czURL))
	if err != nil || !isZstdBlob(onDisk) {
		t.Errorf("compressible blob not stored as zstd (err=%v, %d bytes)", err, len(onDisk))
	}
	if len(onDisk) >= len(compressible) {
		t.Errorf("zstd blob (%d) not smaller than payload (%d)", len(onDisk), len(compressible))
	}

	// Incompressible (already-random) data must stay raw on disk.
	random := make([]byte, 4096)
	for i := range random {
		random[i] = byte(i*7919 + i>>3) // cheap pseudo-noise
	}
	const rndURL = "http://example.com/sprite.webp"
	d.Put(rndURL, append([]byte(nil), random...))
	if got := waitForBlob(t, d, rndURL); !bytes.Equal(got, random) {
		t.Fatal("incompressible round trip mismatch")
	}

	// Pre-toggle blob still reads.
	if got, ok := d.Get(rawURL); !ok || string(got) != "written-before-compression" {
		t.Errorf("pre-toggle blob unreadable after enabling compression: %q ok=%v", got, ok)
	}
}

// TestDiskBudgetPrune pins the #34 byte-budget auto-prune: past the cap the
// OLDEST (mtime) blobs are swept while the newest survive and the total lands
// under budget — and a zero budget (the default) never deletes anything, since
// T3's unboundedness is a deliberate spec exception.
func TestDiskBudgetPrune(t *testing.T) {
	d := newTestDisk(t)

	// Five ~1 KiB blobs, staged oldest→newest by mtime so the sweep order is
	// deterministic. Written straight through the writer, then re-stamped.
	const blobSize = 1024
	urls := []string{
		"http://example.com/a.webp",
		"http://example.com/b.webp",
		"http://example.com/c.webp",
		"http://example.com/d.webp",
		"http://example.com/e.webp",
	}
	base := time.Now().Add(-time.Hour)
	for i, u := range urls {
		payload := bytes.Repeat([]byte{byte('A' + i)}, blobSize)
		d.Put(u, payload)
		waitForBlob(t, d, u)
		// urls[0] is the OLDEST, urls[len-1] the NEWEST (one minute apart).
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(d.pathFor(u), mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", u, err)
		}
	}

	// A default (zero) budget must NEVER delete — the deliberate spec exception.
	d.SetBudget(0)
	d.prune()
	for _, u := range urls {
		if _, ok := d.Get(u); !ok {
			t.Fatalf("budget 0 (unlimited) deleted %s — it must never prune", u)
		}
	}

	// Cap at 3 KiB: only the three newest (c,d,e) may survive; a,b are oldest.
	const budget = 3 * blobSize
	d.SetBudget(budget)
	d.prune()

	total, err := d.SizeOnDisk()
	if err != nil {
		t.Fatalf("SizeOnDisk: %v", err)
	}
	if total > budget {
		t.Errorf("total %d bytes still over budget %d after prune", total, budget)
	}
	// Oldest evicted.
	for _, u := range urls[:2] {
		if _, ok := d.Get(u); ok {
			t.Errorf("oldest blob %s survived the prune", u)
		}
	}
	// Newest kept.
	for _, u := range urls[2:] {
		if _, ok := d.Get(u); !ok {
			t.Errorf("newest blob %s was wrongly evicted", u)
		}
	}
}

// BenchmarkDiskZstd quantifies the CPU-vs-disk trade the setting buys:
// encode+decode cost per blob for INI-like text (the win case) vs
// pseudo-random bytes (the skip case).
func BenchmarkDiskZstd(b *testing.B) {
	text := bytes.Repeat([]byte("emote_button_spacing = 1, 1\nemotions = 454, 602, 590, 100\n"), 600)
	noise := make([]byte, len(text))
	for i := range noise {
		noise[i] = byte(i*2654435761 + i>>5)
	}
	for _, bench := range []struct {
		name string
		data []byte
	}{{"ini-text", text}, {"noise", noise}} {
		b.Run(bench.name, func(b *testing.B) {
			b.SetBytes(int64(len(bench.data)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cz := zstdEnc.EncodeAll(bench.data, nil)
				if len(cz) < len(bench.data) {
					if _, err := zstdDec.DecodeAll(cz, nil); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func TestDiskKeyShape(t *testing.T) {
	k := Key("http://example.com/a.webp")
	if len(k) != hashHexLen {
		t.Errorf("Key length = %d, want %d", len(k), hashHexLen)
	}
	if k != Key("http://example.com/a.webp") {
		t.Error("Key not deterministic")
	}
}

// TestDiskPerServerSeparation pins the "each server cached separately, no
// conflicts" requirement: identical asset paths on different hosts must map
// to different blobs.
func TestDiskPerServerSeparation(t *testing.T) {
	d := newTestDisk(t)
	const path = "/characters/phoenix/char_icon.png"
	serverA := "http://server-a.example.com" + path
	serverB := "http://server-b.example.com" + path

	if Key(serverA) == Key(serverB) {
		t.Fatal("same key for two servers: cache would conflict")
	}

	d.Put(serverA, []byte("icon-from-server-a"))
	d.Put(serverB, []byte("icon-from-server-b"))
	if got := string(waitForBlob(t, d, serverA)); got != "icon-from-server-a" {
		t.Errorf("server A blob = %q", got)
	}
	if got := string(waitForBlob(t, d, serverB)); got != "icon-from-server-b" {
		t.Errorf("server B blob = %q", got)
	}
}

func TestDiskShardLayout(t *testing.T) {
	d := newTestDisk(t)
	const url = "http://example.com/bg/court.webp"
	d.Put(url, []byte("bg"))
	waitForBlob(t, d, url)

	key := Key(url)
	want := filepath.Join(d.root, key[:shardPrefixLen], key)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("blob not at sharded path %s: %v", want, err)
	}
}

func TestDiskGetMissOnAbsent(t *testing.T) {
	d := newTestDisk(t)
	if _, ok := d.Get("http://example.com/never-stored.webp"); ok {
		t.Error("Get returned data for an absent key")
	}
	if s := d.Stats(); s.Misses != 1 {
		t.Errorf("Misses = %d, want 1", s.Misses)
	}
}

func TestDiskEmptyBlobTreatedAsMissAndRemoved(t *testing.T) {
	d := newTestDisk(t)
	const url = "http://example.com/torn.webp"
	path := d.pathFor(url)
	if err := os.MkdirAll(filepath.Dir(path), diskDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Get(url); ok {
		t.Error("zero-length blob served as a hit")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("torn blob not cleaned up")
	}
}

func TestDiskDelete(t *testing.T) {
	d := newTestDisk(t)
	const url = "http://example.com/corrupt.webp"
	d.Put(url, []byte("bad-bytes-decoder-rejected"))
	waitForBlob(t, d, url)
	blobPath := d.pathFor(url) // test is in-package: read the unexported path
	d.Delete(url)
	// Delete is routed through the async writer goroutine (it never does disk
	// I/O on the caller — a render/decode-path caller must stay off the disk),
	// so the removal lands after the queue drains, not synchronously. Close
	// drains-and-waits; check the file via Stat (not Get, which would race the
	// writer's os.Remove and, on Windows, hold a read handle that blocks it).
	d.Close()
	if _, err := os.Stat(blobPath); !os.IsNotExist(err) {
		t.Errorf("blob survived Delete: Stat err = %v (want IsNotExist)", err)
	}
}

func TestDiskNoTempLeftovers(t *testing.T) {
	d := newTestDisk(t)
	for i := 0; i < 32; i++ {
		d.Put(fmt.Sprintf("http://example.com/a%d.webp", i), []byte("payload"))
	}
	d.Close() // drains queue

	var leftovers []string
	err := filepath.Walk(d.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".tmp") {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) > 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
	if s := d.Stats(); s.Writes != 32 {
		t.Errorf("Writes = %d, want 32", s.Writes)
	}
}

func TestDiskClear(t *testing.T) {
	d := newTestDisk(t)
	const url = "http://example.com/x.webp"
	d.Put(url, []byte("x"))
	waitForBlob(t, d, url)
	if err := d.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok := d.Get(url); ok {
		t.Error("blob survived Clear")
	}
	// Cache must remain usable.
	d.Put(url, []byte("y"))
	if got := string(waitForBlob(t, d, url)); got != "y" {
		t.Errorf("blob after Clear = %q, want y", got)
	}
}

func TestDiskPutAfterCloseDoesNotBlockOrPanic(t *testing.T) {
	d := newTestDisk(t)
	d.Close()

	finished := make(chan struct{})
	go func() {
		d.Put("http://example.com/late.webp", []byte("late"))
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Put blocked after Close")
	}
	if s := d.Stats(); s.Dropped == 0 {
		t.Error("post-Close Put not counted as dropped")
	}
}

func TestDiskPutNeverBlocksWhenQueueFull(t *testing.T) {
	d := newTestDisk(t)
	payload := make([]byte, 1024)
	finished := make(chan struct{})
	go func() {
		// Far more puts than writeQueueCap; success = returning promptly,
		// dropping excess instead of blocking the caller.
		for i := 0; i < writeQueueCap*8; i++ {
			d.Put(fmt.Sprintf("http://example.com/flood/%d.webp", i), payload)
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(diskSettleWait):
		t.Fatal("Put blocked under queue pressure")
	}
}

func TestDiskConcurrent(t *testing.T) {
	d := newTestDisk(t)
	var wg sync.WaitGroup
	const goroutines = 8
	const ops = 100
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				url := fmt.Sprintf("http://example.com/c%d.webp", (seed*ops+i)%50)
				if i%2 == 0 {
					d.Put(url, []byte("concurrent-payload"))
				} else {
					d.Get(url)
				}
			}
		}(g)
	}
	wg.Wait()
}
