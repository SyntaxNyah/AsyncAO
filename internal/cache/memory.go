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
)

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
