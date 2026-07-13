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

// learnedTable is the immutable snapshot behind the resolver's atomic
// pointer: host → fixed per-type extension array. Lookups are one atomic
// load, one map access, one array index — no locks (spec §17.5).
type learnedTable struct {
	hosts map[string]*[AssetTypeCount]string
}

var emptyLearnedTable = &learnedTable{hosts: map[string]*[AssetTypeCount]string{}}

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
	hosts := map[string]*[AssetTypeCount]string{}
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
			arr = new([AssetTypeCount]string)
			hosts[host] = arr
		}
		arr[t] = exts[0]
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

// Learned returns the learned extension for (host, t), if any.
func (r *Resolver) Learned(host string, t AssetType) (string, bool) {
	tbl := r.table.Load()
	arr := tbl.hosts[host]
	if arr == nil {
		return "", false
	}
	ext := arr[t]
	return ext, ext != ""
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
		if ext := arr[t]; ext != "" {
			r.learnedHits.Add(1)
			c.Learned = true
			c.URLs = append(c.URLs, base+ext)
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
func (r *Resolver) BuildFullListCandidates(base string, t AssetType) *Candidates {
	c := r.pool.Get().(*Candidates)
	c.URLs = c.URLs[:0]
	c.Learned = false
	for _, ext := range r.formatList(t) {
		c.URLs = append(c.URLs, base+ext)
	}
	return c
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
		if arr := old.hosts[host]; arr != nil && arr[t] == ext {
			return // already learned; no write, no churn
		}
		next := cloneTableForHost(old, host)
		next.hosts[host][t] = ext
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
		if arr == nil || arr[t] == "" {
			return
		}
		next := cloneTableForHost(old, host)
		next.hosts[host][t] = ""
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
func cloneTableForHost(old *learnedTable, host string) *learnedTable {
	hosts := make(map[string]*[AssetTypeCount]string, len(old.hosts)+1)
	for h, arr := range old.hosts {
		hosts[h] = arr
	}
	fresh := new([AssetTypeCount]string)
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
