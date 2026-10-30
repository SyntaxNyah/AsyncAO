package assets

import (
	"slices"
	"sync"
	"time"
)

// missMemo is a bounded, TTL'd negative memo without expirable.LRU's unstoppable janitor goroutine.
// Expiry is lazy: a read drops its own stale entry and an insert sweeps when full.
type missMemo struct {
	mu       sync.Mutex
	entries  map[string]missEntry
	scratch  []uint64 // reused by evictOldestLocked so overflow allocates nothing
	nextSeq  uint64
	capacity int
	ttl      time.Duration
}

// missEntry has an insertion seq for eviction: a burst can share a deadline, and ties would evict unpredictably.
type missEntry struct {
	deadline time.Time
	seq      uint64
}

// missMemoOverflowEvictShift drops an eighth when full with nothing expired, amortizing the O(n log n) scan.
const missMemoOverflowEvictShift = 3

// newMissMemo holds at most capacity keys for ttl each; non-positive values remember nothing.
func newMissMemo(capacity int, ttl time.Duration) *missMemo {
	return &missMemo{
		entries:  make(map[string]missEntry), // no size hint: the cap is enforced in add(); a hint preallocates ~224 KiB per memo for a map that normally holds a handful of keys
		capacity: capacity,
		ttl:      ttl,
	}
}

// has reports whether key is a live miss, deleting an expired entry; safe on a nil receiver.
func (m *missMemo) has(key string) bool {
	if m == nil || m.entries == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return false
	}
	if !time.Now().Before(e.deadline) {
		delete(m.entries, key)
		return false
	}
	return true
}

// add remembers key for the TTL, evicting at the cap (hard rule 4); re-adding refreshes the deadline.
func (m *missMemo) add(key string) {
	if m == nil || m.entries == nil || m.capacity <= 0 || m.ttl <= 0 {
		return
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) >= m.capacity {
		m.sweepLocked(now)
	}
	if len(m.entries) >= m.capacity {
		batch := m.capacity >> missMemoOverflowEvictShift
		if batch < 1 {
			batch = 1 // a tiny cap still has to make room for the incoming key
		}
		m.evictOldestLocked(batch)
	}
	m.entries[key] = missEntry{deadline: now.Add(m.ttl), seq: m.nextSeq}
	m.nextSeq++
}

// sweepLocked drops every expired entry.
func (m *missMemo) sweepLocked(now time.Time) {
	for k, e := range m.entries {
		if !now.Before(e.deadline) {
			delete(m.entries, k)
		}
	}
}

// evictOldestLocked drops the n earliest-inserted entries; seqs are unique, so there are no ties.
func (m *missMemo) evictOldestLocked(n int) {
	if n <= 0 || len(m.entries) == 0 {
		return
	}
	if n >= len(m.entries) {
		clear(m.entries)
		return
	}
	m.scratch = m.scratch[:0]
	for _, e := range m.entries {
		m.scratch = append(m.scratch, e.seq)
	}
	slices.Sort(m.scratch) // not sort.Slice: no reflect swapper, no closure, no alloc
	cutoff := m.scratch[n-1]
	for k, e := range m.entries {
		if e.seq <= cutoff {
			delete(m.entries, k)
		}
	}
}

// size is the entry count, expired-but-unswept included.
func (m *missMemo) size() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// clear forgets every miss, so the next lookup re-walks the mounts.
func (m *missMemo) clear() {
	if m == nil || m.entries == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.entries)
}
