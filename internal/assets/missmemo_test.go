package assets

import (
	"context"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// These are the tests the rule-6 negative memo shipped WITHOUT, which is exactly
// how it shipped on top of golang-lru v2.0.7's expirable.LRU — a container that
// starts a permanent janitor goroutine per instance (a ttl/100 ticker; Close() is
// commented out in that release) and allocates 100 bucket maps with it. Nothing
// in the suite looked at what CONSTRUCTING a LocalFetcher costs, so a 300 ms
// wakeup per construction was invisible — and LocalFetcher is constructed on
// every mounts change, on every archive replay and, through NewMountLayer, on
// every tab activation.

// goroutineSettleSlack is the growth TestLocalFetcherConstructionIsGoroutineFree
// tolerates. Not zero, because the runtime can be finishing unrelated work (a GC
// worker, a finalizer) across the measurement; small enough that the defect this
// pins — one goroutine per construction, i.e. +goroutineFreeConstructions —
// cannot hide under it.
const goroutineSettleSlack = 4

// goroutineFreeConstructions is how many fetchers/layers the gate builds: well
// above the slack and above the tab ceiling, so a per-construction goroutine is
// unmissable.
const goroutineFreeConstructions = 64

// TestLocalFetcherConstructionIsGoroutineFree is the hard-rule-4 gate: building
// fetchers — and the mount layers that used to mint one just to read a label off
// it — must not grow the goroutine count. Measured, not assumed: the same shape
// of scratch test reported before=2 after50Fetchers=52 after50MountLayers=102
// against the expirable.LRU memo.
func TestLocalFetcherConstructionIsGoroutineFree(t *testing.T) {
	root := t.TempDir()
	mountSets := make([][]string, goroutineFreeConstructions)
	for i := range mountSets {
		// Distinct mount sets: a shared origin must not be what saves us. The
		// folders need not exist — construction never touches the disk.
		mountSets[i] = []string{root, filepath.Join(root, "pack"+strconv.Itoa(i))}
	}

	before := settledGoroutines()
	fetchers := make([]*LocalFetcher, 0, goroutineFreeConstructions)
	layers := make([]*MountLayer, 0, goroutineFreeConstructions)
	for _, mounts := range mountSets {
		fetchers = append(fetchers, NewLocalFetcher(mounts))
		layers = append(layers, NewMountLayer(nil, mounts, []string{"https://example.test/base/"}))
	}
	after := settledGoroutines()

	if grew := after - before; grew > goroutineSettleSlack {
		t.Fatalf("%d fetchers + %d mount layers grew the goroutine count by %d (before=%d after=%d); "+
			"the negative memo must stay janitor-free (hard rule 4)",
			len(fetchers), len(layers), grew, before, after)
	}
	// Keep both alive past the measurement so nothing can be collected early and
	// flatter the count.
	runtime.KeepAlive(fetchers)
	runtime.KeepAlive(layers)
}

// TestMountLayerLabelCostsNoFetcher is the companion pin for the specific
// regression: NewMountLayer wants the ORIGIN, not a byte source. Re-introducing
// NewLocalFetcher(mounts).BaseURL() there keeps the origin correct, so no other
// test would notice — this one notices, because building a layer must cost no
// more than building the string it wraps.
func TestMountLayerLabelCostsNoFetcher(t *testing.T) {
	mounts := []string{t.TempDir(), t.TempDir()}
	origin := LocalOriginFor(mounts)
	if got := NewMountLayer(nil, mounts, nil).LocalOrigin(); got != origin {
		t.Fatalf("layer origin %q, want %q", got, origin)
	}

	const runs = 64
	layerAllocs := testing.AllocsPerRun(runs, func() { _ = NewMountLayer(nil, mounts, nil) })
	labelAllocs := testing.AllocsPerRun(runs, func() { _ = LocalOriginFor(mounts) })
	// The layer's own overhead over the bare label: the MountLayer struct and the
	// dedup map NewMountLayer always builds. A LocalFetcher would add its struct,
	// its mount-slice copy, the memo struct and the memo's (2048-slot) map on top
	// of that — far outside this margin — so the gate catches the regression
	// without pinning the hash function's own allocation churn.
	const layerOverheadAllocs = 3
	if layerAllocs > labelAllocs+layerOverheadAllocs {
		t.Fatalf("NewMountLayer allocates %.0f vs %.0f for the label alone — it is minting a fetcher for a string",
			layerAllocs, labelAllocs)
	}
}

// TestLocalOriginForMatchesTheFetcher pins the two spellings of the origin
// formula as ONE formula. The helper exists so label call sites stop building
// fetchers; the moment it drifts, a pack's transport URLs and the fetcher's
// keyspace part company and the pack goes silently inert — the failure mode
// TestMountLayerUnnormalizedOriginIsTheInertTrap already documents.
func TestLocalOriginForMatchesTheFetcher(t *testing.T) {
	dir := t.TempDir()
	for _, mounts := range [][]string{
		nil,
		{},
		{""},
		{dir},
		{dir + "/sub/"}, // a spelling filepath.Clean rewrites
		{dir, "", dir + "/other"},
		{dir + "/other", dir}, // order is part of the identity
	} {
		want := NewLocalFetcher(mounts).BaseURL()
		if got := LocalOriginFor(mounts); got != want {
			t.Fatalf("LocalOriginFor(%q) = %q, fetcher says %q", mounts, got, want)
		}
	}
}

// TestMissMemoIsBoundedByItsCap drives the cap directly (rule 4). Every insert
// lands inside one TTL window, so the expiry sweep can free nothing and overflow
// eviction is the only thing between this and an unbounded map.
func TestMissMemoIsBoundedByItsCap(t *testing.T) {
	const capacity = 64
	m := newMissMemo(capacity, time.Hour)
	for i := 0; i < capacity*8; i++ {
		m.add(missKey(i))
		if got := m.size(); got > capacity {
			t.Fatalf("after %d inserts the memo holds %d entries, cap is %d", i+1, got, capacity)
		}
	}
	if m.size() == 0 {
		t.Fatal("the memo evicted itself empty; overflow must drop a batch, not everything")
	}
	// FIFO: the newest key survives overflow, the very first does not.
	if !m.has(missKey(capacity*8 - 1)) {
		t.Fatal("the most recent insert must still be remembered")
	}
	if m.has(missKey(0)) {
		t.Fatal("the oldest insert must have been evicted first")
	}
}

// TestMissMemoExpiresLazily pins the property that replaces the janitor: an entry
// stops answering once its TTL passes, with no timer having run.
func TestMissMemoExpiresLazily(t *testing.T) {
	const ttl = 20 * time.Millisecond
	m := newMissMemo(8, ttl)
	m.add("gone")
	if !m.has("gone") {
		t.Fatal("a fresh miss must be remembered")
	}
	time.Sleep(3 * ttl)
	if m.has("gone") {
		t.Fatal("an expired miss must stop answering without a janitor")
	}
	if m.size() != 0 {
		t.Fatalf("the read that saw the expiry must drop the entry; size=%d", m.size())
	}
}

// TestMissMemoIsConcurrentlySafe is the -race pin: Fetch runs on pool workers, so
// the memo replaces a container that was mutex-guarded internally and has to keep
// that contract.
func TestMissMemoIsConcurrentlySafe(t *testing.T) {
	const workers = 8
	const perWorker = 200
	const capacity = 128
	m := newMissMemo(capacity, time.Second)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				k := missKey(w*perWorker + i)
				m.add(k)
				_ = m.has(k)
				_ = m.has(missKey(i))
			}
		}(w)
	}
	wg.Wait()
	if m.size() > capacity {
		t.Fatalf("concurrent inserts overflowed the cap: %d", m.size())
	}
}

// TestZeroValueLocalFetcherDoesNotPanic pins the nil-memo path. LocalFetcher is
// exported with unexported fields, so `assets.LocalFetcher{}` compiles outside
// this package; the memo's methods are nil-receiver safe so that bypass degrades
// to "never memoized" instead of taking down a pool worker.
func TestZeroValueLocalFetcherDoesNotPanic(t *testing.T) {
	var lf LocalFetcher
	if _, err := lf.Fetch(context.Background(), "local://m-x/whatever.png"); err == nil {
		t.Fatal("a zero-value fetcher has no mounts; the fetch must fail, not succeed")
	}
	if lf.notFound.has("anything") {
		t.Fatal("a nil memo must remember nothing")
	}
	lf.notFound.add("anything") // must not panic
	if lf.notFound.size() != 0 {
		t.Fatal("a nil memo must stay empty")
	}
}

// settledGoroutines samples runtime.NumGoroutine() until it stops moving, so an
// unrelated goroutine winding down mid-measurement cannot decide the gate.
func settledGoroutines() int {
	const stableSamples = 3
	const maxSamples = 200
	const samplePause = 2 * time.Millisecond
	runtime.GC()
	last, stable := -1, 0
	for i := 0; i < maxSamples; i++ {
		n := runtime.NumGoroutine()
		if n == last {
			if stable++; stable >= stableSamples {
				return n
			}
		} else {
			last, stable = n, 1
		}
		time.Sleep(samplePause)
	}
	return runtime.NumGoroutine()
}

// missKey builds a distinct memo key per index, shaped like the real ones.
func missKey(i int) string {
	return "local://m-test/characters/phoenix/(a)normal" + strconv.Itoa(i) + ".png"
}
