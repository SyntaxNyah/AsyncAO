// Package cache implements AsyncAO's three storage tiers (spec §9):
//
//	T1 — decoded textures, byte-budgeted LRU (64 MiB default)
//	T2 — raw fetched bytes, byte-budgeted LRU (128 MiB default)
//	T3 — on-disk asset cache with a single async writer
//
// T1/T2 are ByteBudgetLRU instantiations owned by internal/assets and the
// render side; this package is SDL-free so every tier is testable headless.
// Cache keys embed the full asset URL (host included), so two servers can
// never collide — per-server separation is structural, not best-effort.
package cache

import (
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	// DefaultT1BudgetBytes bounds decoded texture payloads (Σ w×h×4 per
	// frame) held in memory.
	DefaultT1BudgetBytes = 64 << 20
	// DefaultT2BudgetBytes bounds raw fetched asset bytes held in memory.
	DefaultT2BudgetBytes = 128 << 20
	// DefaultMaxEntries is the entry-count ceiling for either memory tier;
	// the byte budget is expected to trip first for sprite-sized assets.
	DefaultMaxEntries = 4096

	// decodeCapBudgetDiv is the STILL-asset cap: how big one decoded STILL asset
	// may get, relative to the whole T1 budget. It was written independently in
	// three places (the decoder default, main's live override, and — by ratio —
	// the render tier split it must stay compatible with), and a page near the
	// old budget/2 cap was ~57% of the MAIN tier, so ONE landing page evicted
	// the majority of the on-screen working set: the confirmed root arithmetic
	// of the stage-flash class the held:// bridge only patches downstream. The
	// render main tier is always >= budget/2 (splitT1Budget caps the small-UI
	// shield at budget/2), so a budget/4 decode cap is provably <= main AND <=
	// main/2 for EVERY budget — one page can never evict even half the main
	// tier's live working set. ANIMATED assets get a larger cap (see
	// MaxAnimatedDecodedAssetBytes = main/2); stills keep this tighter one.
	// Cross-package invariant pinned by render.TestDecodeCapFitsMainTier.
	decodeCapBudgetDiv = 4
)

// MaxDecodedAssetBytes is the per-asset decoded-payload cap (Σ w×h×4 across
// frames) for a STILL asset at a given T1 texture budget: the SINGLE source of
// truth both the decoder default and main's live override derive from (see
// decodeCapBudgetDiv for the arithmetic and why budget/4). Animated assets use
// MaxAnimatedDecodedAssetBytes instead. A non-positive budget yields 0 so
// callers can substitute their own default.
func MaxDecodedAssetBytes(budget int64) int64 {
	if budget <= 0 {
		return 0
	}
	return budget / decodeCapBudgetDiv
}

// Small-texture shield carve-out (the byte budget reserved for icon/button
// thumbnails). These constants are the SINGLE source of truth for the render
// tier split — render.splitT1Budget delegates here — so the main tier and the
// animated decode cap derive from the same arithmetic and can never drift
// apart.
const (
	smallTexBudgetDiv = 8
	smallTexMinBudget = 4 << 20
)

// SmallTierBytes is the small-UI shield's share of the T1 budget: budget/8,
// floored at smallTexMinBudget so tiny power-user budgets keep a useful shield,
// and capped at budget/2 so the floor can't starve the main tier.
func SmallTierBytes(budget int64) int64 {
	small := budget / smallTexBudgetDiv
	if small < smallTexMinBudget {
		small = smallTexMinBudget
	}
	if small > budget/2 {
		small = budget / 2
	}
	return small
}

// MainTierBytes is the remainder of the T1 budget after the small shield.
func MainTierBytes(budget int64) int64 {
	return budget - SmallTierBytes(budget)
}

// MaxAnimatedDecodedAssetBytes is the per-asset decoded-payload cap (Σ w×h×4
// across frames) for ANIMATED assets: half the main render tier. It is larger
// than the still cap (MaxDecodedAssetBytes = budget/4) so a long animated
// sprite keeps more frames, and it is provably <= main/2 for EVERY budget —
// one animated page can never evict even half the main tier's live working set
// (the stage-flash invariant, pinned by render.TestDecodeCapFitsMainTier).
func MaxAnimatedDecodedAssetBytes(budget int64) int64 {
	return MainTierBytes(budget) / 2
}

// sized pairs a cached value with the payload size it was accounted at.
type sized[V any] struct {
	value V
	size  int64
}

// EvictFunc receives every value leaving the cache — capacity eviction,
// byte-budget eviction, replacement, Remove, and Purge alike. T1 wires this
// to the render thread's texture destroy queue; it must not call back into
// the cache and must not block.
type EvictFunc[K comparable, V any] func(key K, value V, size int64)

// ByteBudgetLRU wraps hashicorp/golang-lru v2 with byte accounting
// (evict-until-under-budget). The inner LRU is already thread-safe; per
// spec §9 this wrapper adds no locking of its own — only atomics.
type ByteBudgetLRU[K comparable, V any] struct {
	budget    int64
	bytes     atomic.Int64
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
	onEvict   EvictFunc[K, V]
	lru       *lru.Cache[K, sized[V]]
}

// NewByteBudgetLRU builds a tier holding at most maxEntries values and at
// most budgetBytes accounted payload bytes. onEvict may be nil.
func NewByteBudgetLRU[K comparable, V any](maxEntries int, budgetBytes int64, onEvict EvictFunc[K, V]) (*ByteBudgetLRU[K, V], error) {
	c := &ByteBudgetLRU[K, V]{
		budget:  budgetBytes,
		onEvict: onEvict,
	}
	inner, err := lru.NewWithEvict(maxEntries, func(key K, entry sized[V]) {
		c.bytes.Add(-entry.size)
		c.evictions.Add(1)
		if c.onEvict != nil {
			c.onEvict(key, entry.value, entry.size)
		}
	})
	if err != nil {
		return nil, err
	}
	c.lru = inner
	return c, nil
}

// Add stores value under key, accounting size payload bytes, then evicts
// least-recently-used entries until the tier is back under budget. Values
// larger than the whole budget are rejected (return false) instead of
// flushing the entire tier. Replacing an existing key first evicts the old
// value (its eviction callback fires, e.g. to destroy the old texture).
func (c *ByteBudgetLRU[K, V]) Add(key K, value V, size int64) bool {
	if size < 0 || size > c.budget {
		return false
	}
	// Remove-then-add keeps byte accounting exact on replacement and routes
	// the displaced value through the eviction callback.
	c.lru.Remove(key)
	c.lru.Add(key, sized[V]{value: value, size: size})
	c.bytes.Add(size)
	for c.bytes.Load() > c.budget {
		if _, _, ok := c.lru.RemoveOldest(); !ok {
			break
		}
	}
	return true
}

// Get returns the cached value, bumping its recency. The hot path performs
// one map lookup and two atomic adds: zero heap allocations (benchmarked by
// BenchmarkCacheHit_T2 and asserted by TestGetZeroAllocs).
func (c *ByteBudgetLRU[K, V]) Get(key K) (V, bool) {
	entry, ok := c.lru.Get(key)
	if !ok {
		c.misses.Add(1)
		var zero V
		return zero, false
	}
	c.hits.Add(1)
	return entry.value, true
}

// Peek returns the cached value without bumping recency or counters.
func (c *ByteBudgetLRU[K, V]) Peek(key K) (V, bool) {
	entry, ok := c.lru.Peek(key)
	return entry.value, ok
}

// Contains reports presence without recency or counter effects.
func (c *ByteBudgetLRU[K, V]) Contains(key K) bool {
	return c.lru.Contains(key)
}

// Remove drops key, routing the value through the eviction callback.
func (c *ByteBudgetLRU[K, V]) Remove(key K) {
	c.lru.Remove(key)
}

// Purge empties the tier; every value passes through the eviction callback.
func (c *ByteBudgetLRU[K, V]) Purge() {
	c.lru.Purge()
}

// Keys returns a SNAPSHOT of the current keys, oldest first.
//
// It allocates the whole slice, so it is deliberately NOT a hot-path API: the
// only callers are user-initiated sweeps (dropping exactly the textures a newly
// mounted local pack can re-answer), never per frame and never per asset.
//
// Snapshotting is also what makes those sweeps safe. Removing while iterating
// re-enters the cache through the eviction callback, which the texture store
// documents as forbidden — so a caller must collect the keys it wants first and
// only then call Remove on each.
func (c *ByteBudgetLRU[K, V]) Keys() []K {
	return c.lru.Keys()
}

// Len returns the current entry count.
func (c *ByteBudgetLRU[K, V]) Len() int {
	return c.lru.Len()
}

// Bytes returns the currently accounted payload bytes.
func (c *ByteBudgetLRU[K, V]) Bytes() int64 {
	return c.bytes.Load()
}

// Budget returns the configured byte budget.
func (c *ByteBudgetLRU[K, V]) Budget() int64 {
	return c.budget
}

// MemoryStats is a point-in-time counter snapshot for the debug HUD and the
// 1 Hz metrics sampler.
type MemoryStats struct {
	Hits      int64
	Misses    int64
	Evictions int64
	Entries   int
	Bytes     int64
	Budget    int64
}

// Stats snapshots the tier's counters.
func (c *ByteBudgetLRU[K, V]) Stats() MemoryStats {
	return MemoryStats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
		Entries:   c.lru.Len(),
		Bytes:     c.bytes.Load(),
		Budget:    c.budget,
	}
}
