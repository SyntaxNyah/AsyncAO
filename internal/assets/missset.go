package assets

import (
	"slices"
	"strings"
	"sync"
)

// missChain identifies a chain, not a file: bases are prefetched with different alts, and a narrower miss
// says nothing about a wider one. A struct key allocates nothing on the hot alt-less lookup.
type missChain struct {
	base  string
	typ   AssetType
	chain string // alts joined by missChainSeparator; "" when there are none
}

// missChainSeparator joins alt spellings; a URL cannot contain NUL, so distinct alt lists cannot collide.
const missChainSeparator = "\x00"

// missTypeRaw is the type for PrefetchRaw's URLs; the enum's sentinel cannot collide with a typed entry.
const missTypeRaw = AssetTypeCount

// missSetOverflowEvictShift drops an eighth when full, amortizing the O(n log n) scan.
const missSetOverflowEvictShift = 3

// missSet remembers for the session that a chain was probed to exhaustion, per probe-list generation.
// Mutex-guarded: written by pool workers, read by the render thread.
type missSet struct {
	mu       sync.Mutex
	keys     map[missChain]uint64 // key → insertion sequence (eviction order)
	scratch  []uint64             // reused by evictOldestLocked so overflow allocates nothing
	nextSeq  uint64
	gen      uint64 // config.FormatGeneration these chains were established under
	capacity int
}

// newMissSet holds at most capacity chains; non-positive capacity remembers nothing.
func newMissSet(capacity int) *missSet {
	return &missSet{
		keys:     make(map[missChain]uint64), // no size hint: the cap is enforced in add(), and most sessions hold a handful of keys
		capacity: capacity,
	}
}

// retireStaleLocked drops other generations' entries; every operation calls it, so none can be read.
func (s *missSet) retireStaleLocked(gen uint64) {
	if s.gen == gen {
		return
	}
	clear(s.keys)
	s.gen = gen
}

// has reports whether this chain is exhausted under the current generation; safe on a nil receiver.
func (s *missSet) has(k missChain, gen uint64) bool {
	if s == nil || s.keys == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retireStaleLocked(gen)
	_, ok := s.keys[k]
	return ok
}

// add records an exhausted chain, evicting at the cap (hard rule 4); re-adding refreshes its eviction position.
func (s *missSet) add(k missChain, gen uint64) {
	if s == nil || s.keys == nil || s.capacity <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retireStaleLocked(gen)
	if _, known := s.keys[k]; !known && len(s.keys) >= s.capacity {
		batch := s.capacity >> missSetOverflowEvictShift
		if batch < 1 {
			batch = 1 // a tiny cap still has to make room for the incoming key
		}
		s.evictOldestLocked(batch)
	}
	s.keys[k] = s.nextSeq
	s.nextSeq++
}

// evictOldestLocked drops the n earliest-recorded chains; seqs are unique, so there are no ties.
func (s *missSet) evictOldestLocked(n int) {
	if n <= 0 || len(s.keys) == 0 {
		return
	}
	if n >= len(s.keys) {
		clear(s.keys)
		return
	}
	s.scratch = s.scratch[:0]
	for _, seq := range s.keys {
		s.scratch = append(s.scratch, seq)
	}
	slices.Sort(s.scratch) // not sort.Slice: no reflect swapper, no closure, no alloc
	cutoff := s.scratch[n-1]
	for k, seq := range s.keys {
		if seq <= cutoff {
			delete(s.keys, k)
		}
	}
}

// forget empties the set, clearing rather than replacing the map to keep its buckets.
func (s *missSet) forget() {
	if s == nil || s.keys == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.keys)
}

// forgetUnder empties chains under prefix, since one Manager serves every tab; an empty prefix is a no-op.
func (s *missSet) forgetUnder(prefix string) {
	if s == nil || s.keys == nil || prefix == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.keys {
		if strings.HasPrefix(k.base, prefix) {
			delete(s.keys, k)
		}
	}
}

// size counts chains under the current generation, so the Settings label excludes ones no probe would consult.
func (s *missSet) size(gen uint64) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retireStaleLocked(gen)
	return len(s.keys)
}
