// Package cache implements the storage tiers: ByteBudgetLRU for T1/T2 and the T3 disk cache, SDL-free.
package cache

import (
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	// DefaultT1BudgetBytes bounds decoded texture payloads held in memory.
	DefaultT1BudgetBytes = 64 << 20
	// DefaultT2BudgetBytes bounds raw fetched asset bytes held in memory.
	DefaultT2BudgetBytes = 128 << 20
	// DefaultMaxEntries is the entry-count ceiling for either memory tier; the byte budget should trip first.
	DefaultMaxEntries = 4096

	// decodeCapBudgetDiv caps a decoded still at budget/4: the main tier is >= budget/2 (splitT1Budget),
	// so one page can never evict half its working set.
	decodeCapBudgetDiv = 4
)

// DefaultMaxAnimatedDecodedAssetBytes bounds one animated asset; frames downscale to fit, so long clips stay smooth.
const DefaultMaxAnimatedDecodedAssetBytes = 128 << 20

// DefaultOversizedTextureBytes caps the eviction-exempt overflow tier, for animated pages over the main tier.
const DefaultOversizedTextureBytes = 500 << 20

// MaxDecodedAssetBytes is a still asset's cap at a T1 budget; a non-positive budget yields 0 for a caller's default.
func MaxDecodedAssetBytes(budget int64) int64 {
	if budget <= 0 {
		return 0
	}
	return budget / decodeCapBudgetDiv
}

// Byte budget reserved for icons and buttons; render.splitT1Budget delegates here, the one source of truth.
const (
	smallTexBudgetDiv = 8
	smallTexMinBudget = 4 << 20
)

// SmallTierBytes is budget/8, floored at smallTexMinBudget and capped at budget/2 so it cannot starve the main tier.
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

// MainTierBytes is the T1 budget left after the small tier.
func MainTierBytes(budget int64) int64 {
	return budget - SmallTierBytes(budget)
}

// sized pairs a cached value with the payload size it was accounted at.
type sized[V any] struct {
	value V
	size  int64
}

// EvictFunc receives every value leaving the cache; it must not call back into the cache and must not block.
type EvictFunc[K comparable, V any] func(key K, value V, size int64)

// ByteBudgetLRU adds byte accounting to hashicorp/golang-lru v2, already thread-safe, using only atomics.
type ByteBudgetLRU[K comparable, V any] struct {
	budget    int64
	bytes     atomic.Int64
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
	onEvict   EvictFunc[K, V]
	lru       *lru.Cache[K, *sized[V]]
}

// NewByteBudgetLRU builds a tier bounded by maxEntries and budgetBytes; onEvict may be nil.
func NewByteBudgetLRU[K comparable, V any](maxEntries int, budgetBytes int64, onEvict EvictFunc[K, V]) (*ByteBudgetLRU[K, V], error) {
	c := &ByteBudgetLRU[K, V]{
		budget:  budgetBytes,
		onEvict: onEvict,
	}
	inner, err := lru.NewWithEvict(maxEntries, func(key K, entry *sized[V]) {
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

// Add stores value and evicts until under budget; a value over the whole budget returns false, not a flush.
func (c *ByteBudgetLRU[K, V]) Add(key K, value V, size int64) bool {
	if size < 0 || size > c.budget {
		return false
	}
	// Remove-then-add keeps byte accounting exact and sends the old value through the eviction callback.
	c.lru.Remove(key)
	c.lru.Add(key, &sized[V]{value: value, size: size})
	c.bytes.Add(size)
	for c.bytes.Load() > c.budget {
		if _, _, ok := c.lru.RemoveOldest(); !ok {
			break
		}
	}
	return true
}

// Get returns the cached value and bumps its recency, with zero heap allocations.
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
	if !ok {
		var zero V
		return zero, false
	}
	return entry.value, true
}

// Resize updates an entry's size without the eviction callback, for an animated page reduced to a still frame.
func (c *ByteBudgetLRU[K, V]) Resize(key K, size int64) bool {
	if size < 0 {
		return false
	}
	entry, ok := c.lru.Peek(key)
	if !ok {
		return false
	}
	c.bytes.Add(size - entry.size)
	entry.size = size
	return true
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

// Keys returns a snapshot, oldest first; removing while iterating re-enters the cache through the eviction callback.
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

// MemoryStats is a point-in-time counter snapshot.
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
