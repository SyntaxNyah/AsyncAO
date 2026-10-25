package assets

import (
	"slices"
	"strings"
	"sync"
)

// missChain identifies one resolution CHAIN, not one file: the base, the asset
// type, and the alternate spellings probed after it.
// The chain has to be part of the identity because the SAME base is prefetched
// with different alt lists from different call sites — ui/screens.go asks a
// preview sprite for its bare base (drawPreanimPreview), then asks the same
// base again with the (a)/(b)/bare ladder (drawCharPreview) — and a bare pass
// that came up empty says nothing about whether the ladder would. Keying on
// base alone would let the narrower pass silence the wider one, which is the
// same class of bug as blanking a shared learned-format slot: one asset's
// answer applied to a question it never asked.
// A struct key rather than a joined string because the alt-less lookup is the
// hot one — every non-resident grid cell consults it on the way to a demand —
// and a struct key hashes its fields in place, allocating nothing.
type missChain struct {
	base  string
	typ   AssetType
	chain string // alts joined by missChainSeparator; "" when there are none
}

// missChainSeparator joins alt spellings into missChain.chain. NUL cannot appear
// in a URL (RFC 3986 percent-encodes it), so no pair of distinct alt lists can
// collide on the joined form.
const missChainSeparator = "\x00"

// missTypeRaw is the miss identity for the decode-free raw lane
// (Manager.PrefetchRaw: char.ini and other text). Those URLs carry no AssetType
// at all, so they need a bucket of their own, and AssetTypeCount is the enum's
// sentinel — never a valid asset type — so a raw entry can never collide with a
// typed one that happens to share a URL.
const missTypeRaw = AssetTypeCount

// missSetOverflowEvictShift sizes the batch dropped when the set is full:
const missSetOverflowEvictShift = 3

// missSet remembers, for the LIFE OF THE SESSION, that a prefetch chain was
// probed to exhaustion and every spelling of it 404'd.
// Without that, turning fallbacks on — or adding .png to the format order in
// Settings, which is exactly what a user does when sprites look missing — would
// change nothing on screen, because the gate sits above the resolver and would
// never let the new list be tried.
type missSet struct {
	mu       sync.Mutex
	keys     map[missChain]uint64 // key → insertion sequence (eviction order)
	scratch  []uint64             // reused by evictOldestLocked so overflow allocates nothing
	nextSeq  uint64
	gen      uint64 // config.FormatGeneration these chains were established under
	capacity int
}

// newMissSet returns a set holding at most capacity chains. A non-positive
// capacity yields a set that never remembers anything, which keeps a
// misconfigured caller correct (just chattier) rather than unbounded.
func newMissSet(capacity int) *missSet {
	return &missSet{
		keys:     make(map[missChain]uint64), // no size hint: the cap is enforced in add(), and most sessions hold a handful of keys
		capacity: capacity,
	}
}

// retireStaleLocked drops everything recorded under a different probe-list
// generation. Called by every operation that observes or extends the set, so
// the staleness check cannot be forgotten at a call site and there is no window
// in which an entry from the old configuration can be read.
func (s *missSet) retireStaleLocked(gen uint64) {
	if s.gen == gen {
		return
	}
	clear(s.keys)
	s.gen = gen
}

// has reports whether this chain was already probed to exhaustion under the
// current probe-list generation.
// Nil-receiver safe: a Manager built without one degrades to "never memoized".
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

// add records an exhausted chain, evicting first if that would exceed the cap
// (rule §17.4). Re-adding a known key refreshes its position in the eviction
// order — it was asked for again, so it is the opposite of stale.
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

// evictOldestLocked drops the n earliest-recorded chains (exact FIFO — seqs are
// unique, so there are no ties to spill over).
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

// forget empties the set: every remembered chain becomes probeable again. The
// map is cleared rather than replaced so the buckets already paid for stay
// available for the re-discovery that follows.
func (s *missSet) forget() {
	if s == nil || s.keys == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.keys)
}

// forgetUnder empties only the chains whose base sits under prefix — one asset
// origin's worth.
// Scoping matters because one Manager serves every open server tab, and the
// events that invalidate this memory are mostly per-SERVER: reconnecting to one
// server says nothing about what another still-connected tab has already
// established about its own. A blanket flush there would send every other tab
// back to the wire for assets it had settled, which is the traffic this whole
// type exists to remove. An empty prefix is a no-op, not a full flush, so a
// caller with no origin to name cannot wipe the set by accident.
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

// size is the remembered-chain count (the Settings button's label, and tests).
func (s *missSet) size(gen uint64) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retireStaleLocked(gen)
	return len(s.keys)
}
