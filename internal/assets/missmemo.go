package assets

import (
	"slices"
	"sync"
	"time"
)

// missMemo is a bounded, TTL'd negative memo — "this key was conclusively
// missing at time T" — with NO background goroutine and nothing to close.
//
// WHY NOT expirable.LRU (what network.Client uses). golang-lru v2.0.7's
// expirable.NewLRU starts a permanent janitor goroutine per instance
// (expirable/expirable_lru.go:77-92: `go func(done){ ticker :=
// time.NewTicker(res.ttl / numBuckets) … }`, numBuckets = 100) and Close() is
// COMMENTED OUT in that release with the note "done channel is never closed, so
// deleteExpired() goroutine will never exit". It also allocates 100 bucket maps
// per instance.
//
// network.Client can afford that: exactly one long-lived client per process and
// a five-minute TTL, i.e. one 3 s ticker for the life of the app. LocalFetcher
// cannot. It is NOT a once-per-process constructor — the local:// overlay is
// re-minted on every mounts/preference change (ui/app.go rebuildAssetOrigin) and
// an archive replay mints its own (ui/replay.go beginBundledReplay) — so at
// localNotFoundCacheTTL = 30 s each construction would pin a 300 ms ticker
// forever: an unbounded goroutine (hard rule 4) and a permanent wakeup source
// against the idle-CPU doctrine.
//
// Expiry is therefore LAZY: a read drops its own stale entry, and an insert
// sweeps when the memo is full. Correctness does not depend on any timer.
type missMemo struct {
	mu       sync.Mutex
	entries  map[string]missEntry
	scratch  []uint64 // reused by evictOldestLocked so overflow allocates nothing
	nextSeq  uint64
	capacity int
	ttl      time.Duration
}

// missEntry carries the expiry AND an insertion sequence. Eviction orders by
// seq, not by deadline: a uniform TTL makes the two equivalent in principle, but
// the wall clock's resolution is coarse enough that a burst of inserts can share
// a deadline, and ties would evict an unpredictable number of entries.
type missEntry struct {
	deadline time.Time
	seq      uint64
}

// missMemoOverflowEvictShift sizes the batch dropped when the memo is full and
// nothing in it has expired yet: capacity>>shift entries, i.e. an eighth
// (256 of localNotFoundCacheSize's 2048). Batching amortizes the O(n log n)
// overflow scan over that many inserts instead of paying it on every insert past
// the cap, and dropping an eighth costs at most that many extra disk walks —
// a negative memo has no correctness stake in what it forgets.
const missMemoOverflowEvictShift = 3

// newMissMemo returns a memo holding at most capacity keys for ttl each.
// A non-positive capacity or ttl yields a memo that never remembers anything,
// which keeps a misconfigured caller correct (just slower) rather than unbounded.
func newMissMemo(capacity int, ttl time.Duration) *missMemo {
	return &missMemo{
		entries:  make(map[string]missEntry), // no size hint: the cap is enforced in add(); a hint preallocates ~224 KiB per memo for a map that normally holds a handful of keys
		capacity: capacity,
		ttl:      ttl,
	}
}

// has reports whether key is a live remembered miss. An expired entry is deleted
// here — the read pays for its own staleness, which is what removes the need for
// a janitor. Nil-receiver safe: a zero-value holder degrades to "never memoized".
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

// add remembers key for the memo's TTL, evicting first if that would exceed the
// cap (rule 4). Re-adding a live key refreshes its deadline.
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

// sweepLocked drops every entry whose TTL has run out. Deleting during a range
// is defined behaviour in Go and does not restart the iteration.
func (m *missMemo) sweepLocked(now time.Time) {
	for k, e := range m.entries {
		if !now.Before(e.deadline) {
			delete(m.entries, k)
		}
	}
}

// evictOldestLocked drops the n earliest-inserted entries (exact FIFO — seqs are
// unique, so there are no ties to spill over).
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

// size is the live entry count, expired-but-unswept included (test/diagnostic).
func (m *missMemo) size() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
