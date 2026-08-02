package assets

import (
	"sync"
	"sync/atomic"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// candidatePoolMaxCap caps the slice capacity worth pooling; anything bigger
// is left to the GC (spec §6).
const candidatePoolMaxCap = 8

// Candidates is a pooled probe list. Acquire via Resolver.BuildCandidates,
// release via Resolver.PutCandidates as soon as probing finishes. Returning
// a pooled struct (instead of a bare []string) keeps the learned fast path
// at exactly one allocation — the joined URL itself — including the return
// trip to the pool.
type Candidates struct {
	// URLs are the full candidate URLs in probe order. With fallbacks off
	// and a learned format this is exactly one entry.
	URLs []string
	// Learned reports whether the list came from the learned table.
	Learned bool
}

// learnedExtCap bounds one (host, type) learned list (rule §17.4). A server
// declares at most a handful of formats per class; the cap keeps a hostile or
// buggy extensions.json from growing the probe list without limit.
const learnedExtCap = 4

// learnedTable is the immutable snapshot behind the resolver's atomic
// pointer: host → fixed per-type extension list. Lookups are one atomic
// load, one map access, one array index — no locks (spec §17.5).
//
// Each entry is an ORDERED list, front = the format to probe first, rather
// than the single string it used to be. A host can genuinely serve more than
// one format for the same class — umineko.online declares
// "emotions_extensions": [".png", ".webp"] and really does ship ~560 PNG-button
// and ~490 WebP-button characters — and a one-slot table could only ever hold
// one of them. Worse, it held whichever loaded LAST: the first WebP character
// flipped the slot for the whole host, and every PNG character then 404'd its
// only candidate and had nothing left to probe, because the type's configured
// list (config.defaultFormatOrders) is itself the single entry .webp. That is
// the "every emote button shows the same icon" report — the grid falls back to
// the character icon in every cell (internal/ui/screens.go, drawEmoteImageButton).
//
// The list is read-only by contract: writers copy-on-write (see promoteExt).
type learnedTable struct {
	hosts map[string]*[AssetTypeCount][]string
}

var emptyLearnedTable = &learnedTable{hosts: map[string]*[AssetTypeCount][]string{}}

// promoteExt returns prev with ext moved to the FRONT, as a fresh slice — the
// published table is immutable, so this never writes through to prev.
//
// Promote, not overwrite. Overwriting is what made the old single-slot table an
// absorbing state: once it held .webp, a PNG-only character probed .webp, 404'd,
// found nothing else to try, and could never record the .png success that would
// have corrected the slot. Keeping the loser in the list means both halves of a
// mixed fleet stay reachable no matter which one loaded most recently; only the
// ORDER churns, and the front is always the last format that actually worked.
//
// Returns prev unchanged (same backing array) when ext is already the front, so
// the steady-state hit does no allocation and RecordSuccess can skip its CAS.
func promoteExt(prev []string, ext string) []string {
	if len(prev) > 0 && prev[0] == ext {
		return prev
	}
	next := make([]string, 0, min(len(prev)+1, learnedExtCap))
	next = append(next, ext)
	for _, e := range prev {
		if e == ext { // already at the front
			continue
		}
		if len(next) == learnedExtCap {
			break
		}
		next = append(next, e)
	}
	return next
}

// formatListCache is an immutable per-generation snapshot of every type's
// effective probe list, so the unlearned (miss) path costs one atomic load
// instead of a preferences RLock + slice rebuild per asset. Cold-loading a
// 200-character icon wall hits this path 200 times back-to-back.
type formatListCache struct {
	gen   uint64
	lists [AssetTypeCount][]string
}

// Resolver is the AssetResolutionEngine: it turns (base, type, host) into
// the list of URLs to probe, learning the first working format per
// (host, type) so steady-state resolution costs a single candidate.
type Resolver struct {
	table   atomic.Pointer[learnedTable]
	formats atomic.Pointer[formatListCache]
	prefs   *config.AssetPreferences
	pool    sync.Pool // *Candidates

	learnedHits   atomic.Int64
	learnedMisses atomic.Int64
}

// NewResolver builds a resolver reading format policy from prefs and warms
// the learned table from the persisted snapshot.
func NewResolver(prefs *config.AssetPreferences) *Resolver {
	r := &Resolver{prefs: prefs}
	r.table.Store(emptyLearnedTable)
	r.pool.New = func() any {
		return &Candidates{URLs: make([]string, 0, candidatePoolMaxCap)}
	}
	r.WarmFromPrefs()
	return r
}

// WarmFromPrefs seeds the learned table from preferences (startup and server
// connect), so the second session's cold load probes once per asset.
func (r *Resolver) WarmFromPrefs() {
	if r.prefs == nil {
		return
	}
	snapshot := r.prefs.LearnedSnapshot()
	if len(snapshot) == 0 {
		return
	}
	hosts := map[string]*[AssetTypeCount][]string{}
	for key, exts := range snapshot {
		host, typeName, ok := splitLearnedKey(key)
		if !ok || len(exts) == 0 {
			continue
		}
		t, ok := TypeFromName(typeName)
		if !ok {
			continue
		}
		arr := hosts[host]
		if arr == nil {
			arr = new([AssetTypeCount][]string)
			hosts[host] = arr
		}
		// The whole persisted list, not just exts[0]: a host that learned two
		// formats for a class must come back with both, or the second session
		// re-enters the single-candidate dead end the list exists to prevent.
		// The snapshot is already a copy, but clamp it to the cap in case an
		// older / hand-edited prefs file carries a longer one.
		if len(exts) > learnedExtCap {
			exts = exts[:learnedExtCap]
		}
		arr[t] = exts
	}
	r.table.Store(&learnedTable{hosts: hosts})
}

// splitLearnedKey splits "<host>|<type name>" on the LAST separator so
// host:port and IPv6-ish hosts survive.
func splitLearnedKey(key string) (host, typeName string, ok bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i:i+1] == config.LearnedKeySeparator {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// Learned returns the PREFERRED learned extension for (host, t), if any — the
// front of the list, i.e. the format that most recently worked. Callers that
// need the whole list (the stale-learned re-probe) use LearnedList.
func (r *Resolver) Learned(host string, t AssetType) (string, bool) {
	exts := r.LearnedList(host, t)
	if len(exts) == 0 {
		return "", false
	}
	return exts[0], true
}

// LearnedList returns every format learned for (host, t), preferred first. The
// returned slice is the published, immutable one — callers must not write to it.
func (r *Resolver) LearnedList(host string, t AssetType) []string {
	arr := r.table.Load().hosts[host]
	if arr == nil {
		return nil
	}
	return arr[t]
}

// BuildCandidates returns the URLs to probe for base (the full URL without
// extension) of type t served by host. Learned hit → exactly one candidate.
// Miss → the preference FormatList for the type (zero-fallback defaults are
// a single format, spec §4). Callers must hand the result back via
// PutCandidates.
//
// Fast path budget: < 100 ns, ≤ 1 alloc (the joined URL string) — gated by
// BenchmarkBuildCandidates_Learned and TestBuildCandidatesLearnedAllocGate.
func (r *Resolver) BuildCandidates(base string, t AssetType, host string) *Candidates {
	c := r.pool.Get().(*Candidates)
	c.URLs = c.URLs[:0]

	tbl := r.table.Load()
	if arr := tbl.hosts[host]; arr != nil {
		// Deliberately the FRONT of the learned list only, never the whole list:
		// the zero-fallback pillar is "exactly one network probe per asset by
		// default", and the front is the format that most recently worked on this
		// host. The rest of the list is the recovery ladder, spent only after this
		// one actually 404s — see BuildFullListCandidates.
		if exts := arr[t]; len(exts) > 0 {
			r.learnedHits.Add(1)
			c.Learned = true
			c.URLs = append(c.URLs, base+exts[0])
			return c
		}
	}

	r.learnedMisses.Add(1)
	c.Learned = false
	for _, ext := range r.formatList(t) {
		c.URLs = append(c.URLs, base+ext)
	}
	return c
}

// BuildFullListCandidates returns the type's full configured probe list for
// base WITHOUT reading or writing the shared learned table. It exists to close
// the learned-format empty-window race: the manager's stale-learned re-probe
// (a learned-first candidate 404'd) used to blank the shared per-(host, type)
// slot with Invalidate before restoring it, so a DIFFERENT concurrent asset of
// the same type could read the empty slot, fall back to the type default, and
// spuriously report a file that exists in the learned format as missing (the
// "every emote button renders the same icon" report). By probing the remaining
// formats through this table-free path, the re-probe never touches the slot:
// the only table write left is walkCandidates' RecordSuccess CAS, so a
// concurrent reader always sees one valid value or another, never nothing.
//
// It does NOT bump learnedMisses: that counter tracks learned-TABLE lookups
// that missed, and this method performs no table lookup — counting it would
// double-count the same asset (BuildCandidates already recorded the miss that
// sent the manager here) and skew the hit-rate metric.
func (r *Resolver) BuildFullListCandidates(base string, t AssetType, host string) *Candidates {
	c := r.pool.Get().(*Candidates)
	c.URLs = c.URLs[:0]
	c.Learned = false
	// The host's OTHER learned formats come first, then the type's configured
	// list. Without the learned half this method could only offer what the client
	// already believed globally, and for a type whose configured list is a single
	// entry (config.defaultFormatOrders: EmoteButton, CharSprite, Background,
	// DeskOverlay, ShoutBubble are all one format) that entry is precisely the one
	// that just 404'd — the caller filters it out as already-tried and is left
	// with an EMPTY list, so a format the server openly declares in its
	// extensions.json is never requested even once. That empty-rest dead end is
	// the umineko.online emote-button bug.
	for _, ext := range r.LearnedList(host, t) {
		c.URLs = append(c.URLs, base+ext)
	}
	for _, ext := range r.formatList(t) {
		if containsExt(r.LearnedList(host, t), ext) {
			continue // already queued above; probing it twice wastes a round trip
		}
		c.URLs = append(c.URLs, base+ext)
	}
	return c
}

// containsExt reports whether exts holds ext. A linear scan over a list capped
// at learnedExtCap, on the recovery path only — no map, no allocation.
func containsExt(exts []string, ext string) bool {
	for _, e := range exts {
		if e == ext {
			return true
		}
	}
	return false
}

// formatList returns the cached probe list for t, rebuilding the snapshot
// only when the preferences' format generation moved. The cached slices are
// read-only by contract. A racing rebuild publishes an identical snapshot —
// last write wins harmlessly.
func (r *Resolver) formatList(t AssetType) []string {
	gen := r.prefs.FormatGeneration()
	if cached := r.formats.Load(); cached != nil && cached.gen == gen {
		return cached.lists[t]
	}
	fresh := &formatListCache{gen: gen}
	for tt := AssetType(0); tt < AssetTypeCount; tt++ {
		fresh.lists[tt] = r.prefs.FormatList(tt.Name())
	}
	r.formats.Store(fresh)
	return fresh.lists[t]
}

// PutCandidates returns a Candidates to the pool. The caller must not touch
// it afterwards.
func (r *Resolver) PutCandidates(c *Candidates) {
	if c == nil || cap(c.URLs) > candidatePoolMaxCap {
		return
	}
	c.URLs = c.URLs[:0]
	c.Learned = false
	r.pool.Put(c)
}

// RecordSuccess learns ext as the working format for (host, t). The write is
// a copy-on-write CompareAndSwap loop — readers never block — and a change
// marks preferences dirty for lazy persistence (spec §6, §17.3).
func (r *Resolver) RecordSuccess(host string, t AssetType, ext string) {
	if !t.Valid() || host == "" || ext == "" {
		return
	}
	for {
		old := r.table.Load()
		var prev []string
		if arr := old.hosts[host]; arr != nil {
			prev = arr[t]
		}
		// Already the preferred format: the steady state, and by far the common
		// case (every asset after the first). No write, no churn, no CAS.
		if len(prev) > 0 && prev[0] == ext {
			return
		}
		next := cloneTableForHost(old, host)
		next.hosts[host][t] = promoteExt(prev, ext)
		if r.table.CompareAndSwap(old, next) {
			break
		}
		// Lost the race with another learn — retry against the new table.
	}
	if r.prefs != nil {
		r.prefs.RecordLearned(host, t.Name(), ext)
	}
}

// Invalidate forgets the learned format for (host, t) — e.g. the learned
// extension started 404ing after a server-side repack.
func (r *Resolver) Invalidate(host string, t AssetType) {
	if !t.Valid() {
		return
	}
	for {
		old := r.table.Load()
		arr := old.hosts[host]
		if arr == nil || len(arr[t]) == 0 {
			return
		}
		next := cloneTableForHost(old, host)
		next.hosts[host][t] = nil
		if r.table.CompareAndSwap(old, next) {
			return
		}
	}
}

// InvalidateAll wipes the in-memory learned table (settings changes, "Clear
// Learned Formats"). Preferences-side invalidation is handled by the
// config mutators themselves.
func (r *Resolver) InvalidateAll() {
	r.table.Store(emptyLearnedTable)
}

// cloneTableForHost shallow-copies the host map and deep-copies only the
// touched host's array, guaranteeing the published table is immutable.
// The per-type element is a slice header, so the array copy shares the LISTS
// with the old table. That is safe and deliberate: a published list is never
// written in place — promoteExt always builds a fresh slice — so sharing costs
// one header copy per type and keeps concurrent readers on a valid snapshot.
func cloneTableForHost(old *learnedTable, host string) *learnedTable {
	hosts := make(map[string]*[AssetTypeCount][]string, len(old.hosts)+1)
	for h, arr := range old.hosts {
		hosts[h] = arr
	}
	fresh := new([AssetTypeCount][]string)
	if prev := old.hosts[host]; prev != nil {
		*fresh = *prev
	}
	hosts[host] = fresh
	return &learnedTable{hosts: hosts}
}

// ResolverStats is a point-in-time counter snapshot.
type ResolverStats struct {
	LearnedHits   int64
	LearnedMisses int64
}

// Stats snapshots the resolver's counters.
func (r *Resolver) Stats() ResolverStats {
	return ResolverStats{
		LearnedHits:   r.learnedHits.Load(),
		LearnedMisses: r.learnedMisses.Load(),
	}
}
