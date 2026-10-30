package assets

import (
	"sync"
	"sync/atomic"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// candidatePoolMaxCap caps the slice capacity worth pooling; anything bigger is left to the GC.
const candidatePoolMaxCap = 8

// Candidates is a pooled probe list; pooling the struct keeps the learned path at one allocation.
type Candidates struct {
	// URLs are the candidate URLs in probe order.
	URLs []string
	// Learned reports whether the list came from the learned table.
	Learned bool
}

// learnedExtCap bounds a learned list (hard rule 4), so a hostile extensions.json cannot grow the probes.
const learnedExtCap = 4

// learnedTable is the immutable snapshot behind the atomic pointer: lock-free reads (hard rule 5), copy-on-write.
type learnedTable struct {
	hosts map[string]*[AssetTypeCount][]string
}

var emptyLearnedTable = &learnedTable{hosts: map[string]*[AssetTypeCount][]string{}}

// promoteExt returns prev with ext moved to the front as a fresh slice, or prev itself if already front.
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

// formatListCache is a per-generation snapshot of probe lists, so a miss costs an atomic load, not an RLock.
type formatListCache struct {
	gen   uint64
	lists [AssetTypeCount][]string
}

// Resolver turns (base, type, host) into URLs to probe, learning working formats per (host, type).
type Resolver struct {
	table   atomic.Pointer[learnedTable]
	formats atomic.Pointer[formatListCache]
	// packFormats is formats for local mount folders; a separate snapshot because the contents differ.
	packFormats atomic.Pointer[formatListCache]
	prefs       *config.AssetPreferences
	pool        sync.Pool // *Candidates

	learnedHits   atomic.Int64
	learnedMisses atomic.Int64
}

// NewResolver builds a resolver reading format policy from prefs and warms the learned table.
func NewResolver(prefs *config.AssetPreferences) *Resolver {
	r := &Resolver{prefs: prefs}
	r.table.Store(emptyLearnedTable)
	r.pool.New = func() any {
		return &Candidates{URLs: make([]string, 0, candidatePoolMaxCap)}
	}
	r.WarmFromPrefs()
	return r
}

// WarmFromPrefs seeds the learned table from preferences, so the next session's cold load probes once per asset.
func (r *Resolver) WarmFromPrefs() {
	if r.prefs == nil {
		return
	}
	snapshot := r.prefs.Load().LearnedSnapshot()
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
		// Restore the whole list, or a host with two formats gets stuck on one; clamp for hand-edited prefs.
		if len(exts) > learnedExtCap {
			exts = exts[:learnedExtCap]
		}
		arr[t] = exts
	}
	r.table.Store(&learnedTable{hosts: hosts})
}

// splitLearnedKey splits "<host>|<type name>" on the last separator so host:port and IPv6-like hosts survive.
func splitLearnedKey(key string) (host, typeName string, ok bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i:i+1] == config.LearnedKeySeparator {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

// Learned returns the preferred extension for (host, t): the format that most recently worked.
func (r *Resolver) Learned(host string, t AssetType) (string, bool) {
	exts := r.LearnedList(host, t)
	if len(exts) == 0 {
		return "", false
	}
	return exts[0], true
}

// LearnedList returns every format learned for (host, t), preferred first; the slice is read-only.
func (r *Resolver) LearnedList(host string, t AssetType) []string {
	arr := r.table.Load().hosts[host]
	if arr == nil {
		return nil
	}
	return arr[t]
}

// BuildCandidates returns URLs to probe: one on a learned hit, else FormatList; return it via PutCandidates.
func (r *Resolver) BuildCandidates(base string, t AssetType, host string) *Candidates {
	c := r.pool.Get().(*Candidates)
	c.URLs = c.URLs[:0]

	tbl := r.table.Load()
	if arr := tbl.hosts[host]; arr != nil {
		// Only the front: the default is one probe per asset, and the rest wait for a 404 (BuildFullListCandidates).
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

// BuildFullListCandidates lists the learned then configured formats, never writing the learned table,
// so a stale-format re-probe cannot affect concurrent readers; BuildCandidates already counted this miss.
func (r *Resolver) BuildFullListCandidates(base string, t AssetType, host string) *Candidates {
	c := r.pool.Get().(*Candidates)
	c.URLs = c.URLs[:0]
	c.Learned = false
	// Learned first: the configured list may be the single entry that just 404'd, leaving nothing to try.
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

// containsExt is a linear scan of a list capped at learnedExtCap, on the recovery path only.
func containsExt(exts []string, ext string) bool {
	for _, e := range exts {
		if e == ext {
			return true
		}
	}
	return false
}

// formatList returns the cached list for t, rebuilt per format generation; a racing rebuild is identical.
func (r *Resolver) formatList(t AssetType) []string {
	gen := r.prefs.FormatGeneration()
	if cached := r.formats.Load(); cached != nil && cached.gen == gen {
		return cached.lists[t]
	}
	fresh := &formatListCache{gen: gen}
	for tt := AssetType(0); tt < AssetTypeCount; tt++ {
		fresh.lists[tt] = r.prefs.Load().FormatList(tt.Name())
	}
	r.formats.Store(fresh)
	return fresh.lists[t]
}

// PackFormatList returns the full chain for local mounts: a pack has no learned formats and need not
// match the server, so a stock .png pack would otherwise always miss.
func (r *Resolver) PackFormatList(t AssetType) []string {
	gen := r.prefs.FormatGeneration()
	if cached := r.packFormats.Load(); cached != nil && cached.gen == gen {
		return cached.lists[t]
	}
	fresh := &formatListCache{gen: gen}
	for tt := AssetType(0); tt < AssetTypeCount; tt++ {
		fresh.lists[tt] = fullProbeChain(r.prefs, tt)
	}
	r.packFormats.Store(fresh)
	return fresh.lists[t]
}

// PutCandidates returns c to the pool; the caller must not touch it afterwards.
func (r *Resolver) PutCandidates(c *Candidates) {
	if c == nil || cap(c.URLs) > candidatePoolMaxCap {
		return
	}
	c.URLs = c.URLs[:0]
	c.Learned = false
	r.pool.Put(c)
}

// RecordSuccess learns ext with a copy-on-write CAS loop and marks preferences dirty (hard rules 3 and 5).
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
		if len(prev) > 0 && prev[0] == ext {
			return
		}
		next := cloneTableForHost(old, host)
		next.hosts[host][t] = promoteExt(prev, ext)
		if r.table.CompareAndSwap(old, next) {
			break
		}
		// Lost the race with another learn: retry against the new table.
	}
	if r.prefs != nil {
		r.prefs.RecordLearned(host, t.Name(), ext)
	}
}

// Invalidate forgets the learned format for (host, t), for example after a server-side repack.
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

// InvalidateAll wipes the in-memory learned table; the config mutators handle the preferences side.
func (r *Resolver) InvalidateAll() {
	r.table.Store(emptyLearnedTable)
}

// cloneTableForHost copies the host map and touched array; lists stay shared, as promoteExt never writes one.
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
