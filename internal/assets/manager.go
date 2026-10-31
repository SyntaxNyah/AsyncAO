package assets

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// decodedChanCap bounds the render-thread handoff; when full, workers block: results never drop (hard rule 7).
	decodedChanCap = 64
	// audioChanCap bounds the raw-audio handoff to the audio system.
	audioChanCap = 64
	// warningChanCap bounds missing-asset warnings; these may be dropped, results may not.
	warningChanCap = 32
	// musicFailChanCap bounds music-fetch-failure notices, which may drop; sprite failures never report here.
	musicFailChanCap = 8
	// localOriginHistoryCap bounds how many retired mount-set origins stay answerable.
	localOriginHistoryCap = 4
	// conclusiveMissCap bounds the exhausted-chain set; overflow drops the oldest eighth.
	conclusiveMissCap = 8192
)

// DecodedAsset is the manager's handoff to the render thread: decoded frames, or the error that ended the attempt.
type DecodedAsset struct {
	URL   string
	Base  string
	Type  AssetType
	Asset *Decoded
	Err   error
	// Transient marks a network failure: bytes were never seen, so keep it out of the negative cache.
	Transient bool
	// FromPack marks local mount bytes; Base is still the server's URL, so never MarkFailed(Base) on them,
	// or a corrupt pack file takes the server's good copy offline.
	FromPack bool
}

// AudioAsset is the manager's handoff to the audio system: raw bytes that SDL_mixer decodes.
type AudioAsset struct {
	URL  string
	Base string
	Type AssetType
	Data []byte
	// FromPack is as on DecodedAsset; audio skips the upload pump, so the audio loader quarantines itself.
	FromPack bool
}

// Warning reports an asset that 404'd in every probed format.
type Warning struct {
	Base  string
	Type  AssetType
	Tried []string
}

// MusicFailure reports a transient music-fetch failure, never a 404, so the jukebox can say a track did not load.
type MusicFailure struct {
	URL string
	Err error
}

// Fetcher is the byte source the manager probes: network.Client when streaming, LocalFetcher otherwise.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Manager walks the tiers: T1 texture, T2 raw bytes, T3 disk, then a network probe in candidate order.
type Manager struct {
	resolver *Resolver
	prefs    *config.AssetPreferences
	t2       *cache.ByteBudgetLRU[string, []byte]
	disk     *cache.DiskCache
	client   Fetcher
	pool     *network.Pool
	decoder  *DecoderPool
	thumbs   *ThumbCache // optional persistent low-q sprite thumbnails (nil = feature absent)

	// localMode skips the T3 disk cache (assets already live on disk).
	localMode bool

	// t1Contains reports whether a texture is uploaded; pass the upload's key: base if probed, full URL if exact.
	t1Contains func(url string) bool
	// t1Failed reports a recent decode failure of base, so one bad asset cannot storm the network.
	t1Failed func(base string) bool

	decodedCh   chan DecodedAsset
	audioCh     chan AudioAsset
	warningCh   chan Warning
	musicFailCh chan MusicFailure

	// deliveryNotify wakes the event-driven render loop; on pool workers, so cheap, non-blocking and SDL-free.
	deliveryNotify atomic.Pointer[func()]

	inflight sync.Map // base|type → struct{}: one pipeline pass per asset

	// conclusiveMiss holds chains probed to exhaustion, checked before the pool submit; no TTL (see missSet).
	conclusiveMiss *missSet

	// offline blocks every network fetch; cache tiers still read, and misses learn no 404s.
	offline atomic.Bool

	// archiveSrc is consulted first during a scene replay, and a miss falls through; set by UI, read by workers.
	archiveSrc atomic.Pointer[Fetcher]

	// localOverlay serves local:// URLs in a streaming manager, whose network client would error on them.
	localOverlay atomic.Pointer[LocalFetcher]

	// localRetired holds previous overlays, newest first, so their URLs resolve; copy-on-write (hard rule 5).
	localRetired atomic.Pointer[[]*LocalFetcher]

	// mountLayer answers from local folders and .zip packs before the network; nil costs one atomic load.
	mountLayer atomic.Pointer[MountLayer]

	t1Hits          atomic.Int64
	t2Hits          atomic.Int64
	diskHits        atomic.Int64
	netFetches      atomic.Int64
	missing         atomic.Int64
	mountFetches    atomic.Int64
	packQuarantined atomic.Int64
}

// SetOffline opens or closes the network block.
func (m *Manager) SetOffline(on bool) { m.offline.Store(on) }

// Offline reports the network block's state, so a temporary closer can restore it, not end rehearsal mode.
func (m *Manager) Offline() bool { return m.offline.Load() }

// ErrLocalOverlayUnavailable is not ErrAssetNotFound: "cannot serve local://" must not mark every asset missing.
var ErrLocalOverlayUnavailable = errors.New("assets: no local mount overlay configured")

// netFetch is the manager's only network egress, so every path respects the offline block.
func (m *Manager) netFetch(ctx context.Context, url string) ([]byte, error) {
	// Above the offline check on purpose: archive reads are disk, not network.
	if ov := m.archiveSrc.Load(); ov != nil {
		if data, err := (*ov).Fetch(ctx, url); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	// The origin hashes the mount list the URL was minted under, so route to its owner. Above the offline check
	// because it is disk, and below archiveSrc so a replay archive's own local:// origin wins.
	if strings.HasPrefix(url, LocalScheme) {
		if src := m.localSourceFor(url); src != nil {
			// A missing mount file returns ErrAssetNotFound, matching a streaming 404.
			return src.Fetch(ctx, url)
		}
		if m.localMode {
			// No source claims this origin; let the LocalFetcher answer so the error names the origin it does serve.
			return m.client.Fetch(ctx, url)
		}
		return nil, ErrLocalOverlayUnavailable // cannot serve — NOT a 404 (see above)
	}
	if m.offline.Load() {
		return nil, network.ErrAssetNotFound
	}
	return m.client.Fetch(ctx, url)
}

// localSourceFor picks the fetcher owning a local:// origin: overlay, then retired overlays, then the client; or nil.
func (m *Manager) localSourceFor(url string) Fetcher {
	if ov := m.localOverlay.Load(); ov != nil && strings.HasPrefix(url, ov.BaseURL()) {
		return ov
	}
	if retired := m.localRetired.Load(); retired != nil {
		for _, lf := range *retired { // newest first, ≤ localOriginHistoryCap entries
			if strings.HasPrefix(url, lf.BaseURL()) {
				return lf
			}
		}
	}
	// A streaming manager's client is the network client, which claims no local:// origin.
	if lf, ok := m.client.(*LocalFetcher); ok && strings.HasPrefix(url, lf.BaseURL()) {
		return lf
	}
	return nil
}

// SetLocalOverlay installs or clears the overlay, render thread only; pass nil for an empty mount set,
// because an empty LocalFetcher answers every URL with a conclusive 404.
func (m *Manager) SetLocalOverlay(f *LocalFetcher) {
	prev := m.localOverlay.Swap(f)
	if sameMountSet(prev, f) {
		return // the same mount set re-pushed: nothing retired, nothing changed
	}
	// A new source changes which assets exist; a stale entry would silently hide a just-attached folder's files.
	// Guarded on a real change because rebuildAssetOrigin re-pushes on every connect and settings edit.
	m.ForgetConclusiveMisses()
	if prev == nil {
		return // nothing to retire
	}
	old := m.localRetired.Load()
	next := make([]*LocalFetcher, 0, localOriginHistoryCap)
	next = append(next, prev) // newest first
	if old != nil {
		for _, lf := range *old {
			if len(next) == localOriginHistoryCap {
				break // oldest generations fall off the end
			}
			if lf.BaseURL() == prev.BaseURL() {
				continue // it is back at the head; never list a generation twice
			}
			next = append(next, lf)
		}
	}
	m.localRetired.Store(&next)
}

// sameMountSet compares origins, not pointers: rebuildAssetOrigin mints a fetcher each call, and BaseURL hashes mounts.
func sameMountSet(a, b *LocalFetcher) bool {
	if a == nil || b == nil {
		return a == b // nil→nil is the no-mounts steady state; nil↔set is a real change
	}
	return a.BaseURL() == b.BaseURL()
}

// skipDisk reports whether url bypasses T3: local:// URLs read from mounts that are already on disk.
func (m *Manager) skipDisk(url string) bool {
	return m.localMode || strings.HasPrefix(url, LocalScheme)
}

// SetArchiveSource routes fetches through f before the normal source during a bundled-scene replay.
func (m *Manager) SetArchiveSource(f Fetcher) { m.archiveSrc.Store(&f) }

// ClearArchiveSource removes the bundled-archive source.
func (m *Manager) ClearArchiveSource() { m.archiveSrc.Store(nil) }

// LocalMode is fixed at construction, so key URL-scheme decisions off this, not the pref, which can invert.
func (m *Manager) LocalMode() bool { return m.localMode }

// SetAssetOrigin sets the Origin / CORS header override on the network source; no-op in local mode.
func (m *Manager) SetAssetOrigin(origin string) {
	if c, ok := m.client.(*network.Client); ok {
		c.SetAssetOrigin(origin)
	}
}

// SetAdaptiveLatencyMultiple sets the per-host deadline multiple (0 = default); no-op in local mode.
func (m *Manager) SetAdaptiveLatencyMultiple(n int) {
	if c, ok := m.client.(*network.Client); ok {
		c.SetAdaptiveLatencyMultiple(n)
	}
}

// SetSpriteCap sets the decode-downscale height cap (0 = no cap) for new decodes.
func (m *Manager) SetSpriteCap(px int) { m.decoder.SetSpriteCap(px) }

// SetAnimatedSpriteCap sets the animated-only height cap (0 = off) for new decodes.
func (m *Manager) SetAnimatedSpriteCap(px int) { m.decoder.SetAnimatedSpriteCap(px) }

// SetAnimatedBudgetMiB sets the per-animated-asset decode budget for new decodes.
func (m *Manager) SetAnimatedBudgetMiB(mib int) { m.decoder.SetAnimatedBudgetMiB(mib) }

// SetAnimatedDecodeConcurrency sets the concurrent-animated-decode bound.
func (m *Manager) SetAnimatedDecodeConcurrency(n int) { m.decoder.SetAnimatedDecodeConcurrency(n) }

// SetTextureCompression sets the texture-compression mode (CompressOff, CompressDXT5, CompressDXT1).
func (m *Manager) SetTextureCompression(mode int) { m.decoder.SetTextureCompression(mode) }

// ColdLoadStats reports the fetch and decode EWMAs; zero until samples exist and in local mode.
func (m *Manager) ColdLoadStats() (fetch, decode time.Duration) {
	if c, ok := m.client.(*network.Client); ok {
		fetch = c.AvgTTFB()
	}
	return fetch, m.decoder.Stats().AvgDecode
}

// ManagerDeps wires a Manager; every field is required except T1Contains.
type ManagerDeps struct {
	Resolver *Resolver
	Prefs    *config.AssetPreferences
	T2       *cache.ByteBudgetLRU[string, []byte]
	Disk     *cache.DiskCache
	// Source is the byte origin: network.Client when streaming, LocalFetcher otherwise.
	Source Fetcher
	// LocalMode skips the T3 disk cache (assets already live on disk).
	LocalMode  bool
	Pool       *network.Pool
	Decoder    *DecoderPool
	T1Contains func(url string) bool
	T1Failed   func(base string) bool
	// Thumbs, when set, receives every decoded character sprite for thumbnailing.
	Thumbs *ThumbCache
}

// NewManager builds the Manager from its dependencies.
func NewManager(deps ManagerDeps) *Manager {
	return &Manager{
		resolver:       deps.Resolver,
		prefs:          deps.Prefs,
		t2:             deps.T2,
		disk:           deps.Disk,
		client:         deps.Source,
		localMode:      deps.LocalMode,
		pool:           deps.Pool,
		decoder:        deps.Decoder,
		thumbs:         deps.Thumbs,
		t1Contains:     deps.T1Contains,
		t1Failed:       deps.T1Failed,
		decodedCh:      make(chan DecodedAsset, decodedChanCap),
		audioCh:        make(chan AudioAsset, audioChanCap),
		warningCh:      make(chan Warning, warningChanCap),
		musicFailCh:    make(chan MusicFailure, musicFailChanCap),
		conclusiveMiss: newMissSet(conclusiveMissCap),
	}
}

// SetDeliveryNotify installs or clears the callback fired after each Decoded/Audio delivery, from any goroutine.
func (m *Manager) SetDeliveryNotify(f func()) {
	if f == nil {
		m.deliveryNotify.Store(nil)
		return
	}
	m.deliveryNotify.Store(&f)
}

// NotifyDelivery runs the delivery callback, which wakes a parked frame loop; safe from any goroutine.
func (m *Manager) NotifyDelivery() {
	if f := m.deliveryNotify.Load(); f != nil {
		(*f)()
	}
}

// Decoded returns the channel the render thread drains for texture uploads.
func (m *Manager) Decoded() <-chan DecodedAsset { return m.decodedCh }

// Audio returns the channel the audio system drains for SDL_mixer loads.
func (m *Manager) Audio() <-chan AudioAsset { return m.audioCh }

// Warnings returns the channel the UI drains for missing-asset banners.
func (m *Manager) Warnings() <-chan Warning { return m.warningCh }

// MusicFailures returns the channel of transient music-fetch failures, drained on the render thread.
func (m *Manager) MusicFailures() <-chan MusicFailure { return m.musicFailCh }

// reportMusicFailure is non-blocking and may drop: an extra signal, not a result; callers check for music.
func (m *Manager) reportMusicFailure(url string, err error) {
	select {
	case m.musicFailCh <- MusicFailure{URL: url, Err: err}:
		m.NotifyDelivery()
	default:
	}
}

// Prefetch queues a pipeline pass for base (URL without extension), tagged with the epoch so room changes cancel it.
func (m *Manager) Prefetch(base string, t AssetType, prio network.Priority) {
	m.PrefetchWithFallback(base, "", t, prio)
}

// PrefetchWithFallback probes alt only when every format of base 404s, like CharLayer::load_image ("(a)X" then "X").
func (m *Manager) PrefetchWithFallback(base, alt string, t AssetType, prio network.Priority) {
	if alt == "" {
		m.PrefetchChain(base, nil, t, prio)
		return
	}
	m.PrefetchChain(base, []string{alt}, t, prio)
}

// PrefetchChain is PrefetchWithFallback with several alts, in order; call sites bound the length (hard rule 4).
func (m *Manager) PrefetchChain(base string, alts []string, t AssetType, prio network.Priority) {
	m.prefetchChain(base, alts, t, prio, false)
}

// PrefetchChainSpeculative is PrefetchChain with no warning on a total miss; pass PriorityLow so it sheds.
func (m *Manager) PrefetchChainSpeculative(base string, alts []string, t AssetType, prio network.Priority) {
	m.prefetchChain(base, alts, t, prio, true)
}

func (m *Manager) prefetchChain(base string, alts []string, t AssetType, prio network.Priority, suppressMissing bool) {
	if base == "" || !t.Valid() {
		return
	}
	if m.t1Failed != nil && m.t1Failed(base) {
		return // recently failed to decode — back off (the negative cache absorbs retries)
	}
	if m.conclusiveMiss.has(missChainFor(base, alts, t), m.probeListGen()) {
		m.skipExhausted(base, t, suppressMissing)
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: m.pool.Epoch(),
		Run: func(stale bool) {
			if stale {
				return
			}
			m.resolveChain(base, alts, t, suppressMissing)
		},
	})
}

// missChainFor builds a chain's miss key, allocation-free when there are no alts.
func missChainFor(base string, alts []string, t AssetType) missChain {
	k := missChain{base: base, typ: t}
	switch len(alts) {
	case 0:
	case 1:
		k.chain = alts[0] // the overwhelmingly common chain (prefixed → bare sprite): no join, no alloc
	default:
		k.chain = strings.Join(alts, missChainSeparator)
	}
	return k
}

// IsConclusiveMiss reports whether an alt-less prefetch of base would be skipped as exhausted; nil-safe.
func (m *Manager) IsConclusiveMiss(base string, t AssetType) bool {
	if m == nil {
		return false
	}
	return m.conclusiveMiss.has(missChain{base: base, typ: t}, m.probeListGen())
}

// probeListGen is the format generation miss records are valid for; read per consult, as it is one atomic load.
func (m *Manager) probeListGen() uint64 {
	if m.prefs == nil {
		return 0
	}
	return m.prefs.FormatGeneration()
}

// ForgetConclusiveMisses empties the exhausted-chain memory, for source changes not confined to one server.
func (m *Manager) ForgetConclusiveMisses() {
	if m == nil {
		return
	}
	m.conclusiveMiss.forget()
}

// ForgetNotFound clears the 404 cache: the network client's LRU or, in local mode, the fetcher's miss memo.
func (m *Manager) ForgetNotFound() {
	if m == nil {
		return
	}
	if c, ok := m.client.(*network.Client); ok {
		c.ClearNotFound()
	}
	if lf, ok := m.client.(*LocalFetcher); ok {
		lf.notFound.clear()
	}
}

// ForgetConclusiveMissesUnder empties the misses for one origin on (re)connect, leaving other open tabs' servers alone.
func (m *Manager) ForgetConclusiveMissesUnder(origin string) {
	if m == nil {
		return
	}
	m.conclusiveMiss.forgetUnder(origin)
}

// ConclusiveMissCount reports how many chains are remembered as exhausted.
func (m *Manager) ConclusiveMissCount() int {
	if m == nil {
		return 0
	}
	return m.conclusiveMiss.size(m.probeListGen())
}

// skipExhausted replaces a pass for an exhausted chain: no probe, warning still fires, with Tried left empty.
func (m *Manager) skipExhausted(base string, t AssetType, suppressMissing bool) {
	if suppressMissing {
		return // speculative: no one asked for this, so no one hears about it (PrefetchChainSpeculative)
	}
	select {
	case m.warningCh <- Warning{Base: base, Type: t}:
	default:
	}
}

// PrefetchExact queues a pipeline pass for a complete URL, with no candidate probing, such as a music track.
func (m *Manager) PrefetchExact(url string, t AssetType, prio network.Priority) {
	if url == "" || !t.Valid() {
		return
	}
	if m.t1Failed != nil && m.t1Failed(url) {
		return // recently failed to decode — back off
	}
	// One rule for the Manager: a chain exhausted this session is not probed again.
	if m.conclusiveMiss.has(missChain{base: url, typ: t}, m.probeListGen()) {
		m.skipExhausted(url, t, false)
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: m.pool.Epoch(),
		Run: func(stale bool) {
			if stale {
				return
			}
			m.resolveExact(url, t)
		},
	})
}

func (m *Manager) resolveExact(url string, t AssetType) {
	if _, loaded := m.inflight.LoadOrStore(url, struct{}{}); loaded {
		return
	}
	defer m.inflight.Delete(url)

	if m.t1Contains != nil && m.t1Contains(url) {
		m.t1Hits.Add(1)
		return
	}
	// After T1 (a resident texture is right) but before T2, so a new pack file beats a cached server copy.
	if layer := m.activeMountLayer(); layer != nil && m.serveFromMount(layer, url, url, t) {
		return
	}
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		m.deliver(url, url, t, data, false)
		return
	}
	if !m.skipDisk(url) {
		if data, ok := m.disk.Get(url); ok {
			m.diskHits.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			m.deliver(url, url, t, data, false)
			return
		}
	}
	data, err := m.netFetch(context.Background(), url)
	switch {
	case err == nil:
		m.netFetches.Add(1)
		m.t2.Add(url, data, int64(len(data)))
		if !m.skipDisk(url) {
			m.disk.Put(url, data)
		}
		m.deliver(url, url, t, data, false)
	case errors.Is(err, network.ErrAssetNotFound):
		// The current generation, not a sampled one: this path builds no candidate list.
		m.rememberExhausted(missChain{base: url, typ: t}, m.probeListGen())
		m.reportMissing(url, t, nil)
	default:
		// Music's failure is otherwise silent: audio never reaches the pump's decode path.
		if t == AssetTypeMusic {
			m.reportMusicFailure(url, err)
		}
		m.decodedCh <- DecodedAsset{URL: url, Base: url, Type: t, Err: err, Transient: true}
		m.NotifyDelivery()
	}
}

// PrefetchRaw warms T2/T3 for a complete URL without decoding (char.ini on hover); silent on 404.
func (m *Manager) PrefetchRaw(url string, prio network.Priority) {
	if url == "" {
		return
	}
	// Same session memory: warmCharINI re-fires when the hover returns to a name.
	key := missChain{base: url, typ: missTypeRaw}
	if m.conclusiveMiss.has(key, m.probeListGen()) {
		return // no warning: this lane has no AssetType to report and is silent on 404 by contract
	}
	// A pack file needs no warming, and probing would spend a request on a URL the server may not have.
	// Covers is a map lookup: on the render thread, reading the file would break hard rule 2.
	if l := m.activeMountLayer(); l != nil && l.Covers(url) {
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: m.pool.Epoch(),
		Run: func(stale bool) {
			if stale {
				return
			}
			if _, loaded := m.inflight.LoadOrStore(url, struct{}{}); loaded {
				return
			}
			defer m.inflight.Delete(url)
			if _, ok := m.t2.Get(url); ok {
				m.t2Hits.Add(1)
				return
			}
			if !m.skipDisk(url) {
				if data, ok := m.disk.Get(url); ok {
					m.diskHits.Add(1)
					m.t2.Add(url, data, int64(len(data)))
					return
				}
			}
			data, err := m.netFetch(context.Background(), url)
			if err != nil {
				// The current generation, as in resolveExact: a complete URL builds no candidate list.
				if errors.Is(err, network.ErrAssetNotFound) {
					m.rememberExhausted(key, m.probeListGen())
				}
				return // any other error is transient and leaves the URL probeable
			}
			m.netFetches.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			if !m.skipDisk(url) {
				m.disk.Put(url, data)
			}
		},
	})
}

// FetchRaw fetches a complete URL through T2, T3 and source without decoding; call it off the render thread.
func (m *Manager) FetchRaw(ctx context.Context, url string) ([]byte, error) {
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		return data, nil
	}
	if !m.skipDisk(url) {
		if data, ok := m.disk.Get(url); ok {
			m.diskHits.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			return data, nil
		}
	}
	data, err := m.netFetch(ctx, url)
	if err != nil {
		return nil, err
	}
	m.netFetches.Add(1)
	m.t2.Add(url, data, int64(len(data)))
	if !m.skipDisk(url) {
		m.disk.Put(url, data)
	}
	return data, nil
}

// ResolveRaw returns the first candidate whose bytes fetch and learns its format; export and replay share this order.
func (m *Manager) ResolveRaw(base string, t AssetType) (string, []byte, bool) {
	if base == "" || !t.Valid() {
		return "", nil, false
	}
	host := hostOf(base)
	cands := m.resolver.BuildCandidates(base, t, host)
	defer m.resolver.PutCandidates(cands)
	for _, url := range cands.URLs {
		data, err := m.FetchRaw(context.Background(), url)
		if err == nil && len(data) > 0 {
			m.resolver.RecordSuccess(host, t, url[len(base):]) // learn the winning ext
			return url, data, true
		}
	}
	return "", nil, false
}

// ResolveRawFull is ResolveRaw for report and export passes: the learned format, then the full probe chain.
func (m *Manager) ResolveRawFull(base string, t AssetType) (string, []byte, bool) {
	if base == "" || !t.Valid() {
		return "", nil, false
	}
	host := hostOf(base)
	// A learned format is the fast single-probe path.
	if ext, ok := m.resolver.Learned(host, t); ok {
		url := base + ext
		if data, err := m.FetchRaw(context.Background(), url); err == nil && len(data) > 0 {
			return url, data, true
		}
		// fall through to the full walk
	}
	// Learn the winner; a different-format sibling still reaches this walk via the fall-through above.
	for _, ext := range fullProbeChain(m.prefs, t) {
		url := base + ext
		data, err := m.FetchRaw(context.Background(), url)
		if err == nil && len(data) > 0 {
			m.resolver.RecordSuccess(host, t, ext) // learn so a repeat pass is single-probe
			return url, data, true
		}
	}
	return "", nil, false
}

// fullProbeChain is the configured order then the type's fallback chain, deduped, covering each decoder's formats.
func fullProbeChain(prefs *config.AssetPreferences, t AssetType) []string {
	name := t.Name()
	var order []string
	if prefs != nil {
		order = prefs.Load().FormatOrder(name) // configured order (defaults to the zero-fallback list)
	}
	if len(order) == 0 {
		order = config.DefaultFormatOrder(name)
	}
	out := make([]string, 0, len(order)+len(config.LegacyFallbackChain(name)))
	seen := map[string]bool{}
	appendExt := func(exts []string) {
		for _, e := range exts {
			if e == "" || seen[e] {
				continue
			}
			seen[e] = true
			out = append(out, e)
		}
	}
	appendExt(order)
	appendExt(config.LegacyFallbackChain(name))
	return out
}

// PrefetchSticky is Prefetch for assets that survive room changes, such as UI chrome.
func (m *Manager) PrefetchSticky(base string, t AssetType, prio network.Priority) {
	if base == "" || !t.Valid() {
		return
	}
	// A sticky asset outlives room changes, so an absent one would be re-demanded in every room.
	if m.conclusiveMiss.has(missChainFor(base, nil, t), m.probeListGen()) {
		m.skipExhausted(base, t, false)
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: network.EpochAny,
		Run: func(stale bool) {
			if stale {
				return
			}
			m.resolveChain(base, nil, t, false)
		},
	})
}

// resolveChain tries primary, then each alt while earlier names miss, delivering under primary.
func (m *Manager) resolveChain(primary string, alts []string, t AssetType, suppressMissing bool) {
	key := primary + config.LearnedKeySeparator + t.Name()
	if _, loaded := m.inflight.LoadOrStore(key, struct{}{}); loaded {
		return // an identical pass is already in flight
	}
	defer m.inflight.Delete(key)
	// Sampled before the walk: rememberExhausted drops the verdict if the probe list changed during the pass.
	startGen := m.probeListGen()

	// Uploads are keyed by base, so a candidate URL with its extension could never hit.
	if m.t1Contains != nil && m.t1Contains(primary) {
		m.t1Hits.Add(1)
		return
	}

	layer := m.activeMountLayer()

	host := hostOf(primary)
	// Pack-first within each spelling: the (a) and (b) chains share a bare alt, so a sweep across spellings
	// would let one bare pack file serve as both idle and talk, shadowing a server's (a)/(b) art.
	if layer != nil && m.serveFromMount(layer, primary, primary, t) {
		return
	}
	done, tried := m.tryBase(primary, primary, t, host)
	if done {
		return
	}
	for i, alt := range alts {
		if alt == "" || alt == primary || containsString(alts[:i], alt) {
			continue // blank / duplicate spelling — nothing new to probe
		}
		if layer != nil && m.serveFromMount(layer, alt, primary, t) {
			return
		}
		var altTried []string
		done, altTried = m.tryBase(alt, primary, t, host)
		if done {
			return
		}
		for _, ext := range altTried {
			if !containsString(tried, ext) {
				tried = append(tried, ext)
			}
		}
	}
	m.rememberExhausted(missChainFor(primary, alts, t), startGen)
	if suppressMissing {
		// Speculative: count the miss but raise no warning for an asset no one demanded.
		m.missing.Add(1)
		return
	}
	m.reportMissing(primary, t, tried)
}

// rememberExhausted records an empty chain, unless offline or the probe list generation changed during the pass.
func (m *Manager) rememberExhausted(k missChain, startGen uint64) {
	if m.offline.Load() {
		return
	}
	if startGen != m.probeListGen() {
		return
	}
	m.conclusiveMiss.add(k, startGen)
}

// tryBase walks base's candidates learned-first; done=true ends the pass: delivered, or transport error reported.
func (m *Manager) tryBase(base, deliverBase string, t AssetType, host string) (done bool, tried404 []string) {
	cands := m.resolver.BuildCandidates(base, t, host)
	usedLearned := cands.Learned
	done, tried404 = m.walkCandidates(cands.URLs, base, deliverBase, t, host, make([]string, 0, len(cands.URLs)))
	m.resolver.PutCandidates(cands)
	if done || !usedLearned {
		return done, tried404
	}

	// The learned format may be stale, so re-probe the full list. Do not blank the learned entry: it is
	// shared per (host, type), and a concurrent asset would read it empty and report an existing file missing.
	cands = m.resolver.BuildFullListCandidates(base, t, host)
	rest := make([]string, 0, len(cands.URLs))
	for _, url := range cands.URLs {
		if !containsString(tried404, url[len(base):]) {
			rest = append(rest, url)
		}
	}
	done, tried404 = m.walkCandidates(rest, base, deliverBase, t, host, tried404)
	m.resolver.PutCandidates(cands)
	return done, tried404
}

// walkCandidates probes urls through T2, T3 and source; done=true ends the pass; tried404 collects 404'd extensions.
func (m *Manager) walkCandidates(urls []string, base, deliverBase string, t AssetType, host string, tried404 []string) (bool, []string) {
	for _, url := range urls {
		ext := url[len(base):]

		if data, ok := m.t2.Get(url); ok {
			m.t2Hits.Add(1)
			m.deliver(url, deliverBase, t, data, false)
			return true, tried404
		}
		// T3 is skipped for local:// URLs: mounts are already on disk.
		if !m.skipDisk(url) {
			if data, ok := m.disk.Get(url); ok {
				m.diskHits.Add(1)
				m.t2.Add(url, data, int64(len(data)))
				m.resolver.RecordSuccess(host, t, ext)
				m.deliver(url, deliverBase, t, data, false)
				return true, tried404
			}
		}
		data, err := m.netFetch(context.Background(), url)
		switch {
		case err == nil:
			m.netFetches.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			if !m.skipDisk(url) {
				m.disk.Put(url, data)
			}
			m.resolver.RecordSuccess(host, t, ext)
			m.deliver(url, deliverBase, t, data, false)
			return true, tried404
		case errors.Is(err, network.ErrAssetNotFound):
			tried404 = append(tried404, ext)
			continue // probe the next candidate
		default:
			// Tagged transient so it never enters the decode negative cache.
			m.decodedCh <- DecodedAsset{URL: url, Base: deliverBase, Type: t, Err: err, Transient: true}
			m.NotifyDelivery()
			return true, tried404
		}
	}
	return false, tried404
}

// deliver sends audio straight on and decodes images off-thread; see DecodedAsset.FromPack for fromPack.
func (m *Manager) deliver(url, base string, t AssetType, data []byte, fromPack bool) {
	if t.IsAudio() {
		m.audioCh <- AudioAsset{URL: url, Base: base, Type: t, Data: data, FromPack: fromPack}
		m.NotifyDelivery()
		return
	}
	m.decoder.Submit(DecodeRequest{
		URL:            url,
		Data:           data,
		Type:           t,
		PlayAnimations: m.prefs.Load().PreferAnimated,
		OnDone: func(doneURL string, d *Decoded, err error) {
			// Never for pack bytes: this disk cache is keyed by the server URL, so user art would pose as its copy.
			if m.thumbs != nil && err == nil && t == AssetTypeCharSprite && !fromPack &&
				d != nil && len(d.Frames) > 0 && d.FrameOffset == 0 {
				// Store only frame 0: later appends and the finalize would overwrite it with a mid-clip or empty frame.
				m.thumbs.Store(base, d)
			}
			m.decodedCh <- DecodedAsset{URL: doneURL, Base: base, Type: t, Asset: d, Err: err, FromPack: fromPack}
			m.NotifyDelivery()
		},
	})
}

// SetMountLayer installs or clears the local-pack layer; readers load the atomic pointer and never lock.
func (m *Manager) SetMountLayer(l *MountLayer) {
	m.ForgetConclusiveMisses()
	m.mountLayer.Store(l)
}

// MountLayer returns the live layer (nil when no mounts are configured).
func (m *Manager) MountLayer() *MountLayer { return m.mountLayer.Load() }

// ActiveMountLayer returns the layer to consult for this pass, or nil.
func (m *Manager) ActiveMountLayer() *MountLayer {
	return m.activeMountLayer()
}

func (m *Manager) activeMountLayer() *MountLayer {
	l := m.mountLayer.Load()
	if l == nil {
		return nil
	}
	if m.localMode {
		// Local-only mode's source is already the mounts, and the pref can invert under a fixed Manager.
		return nil
	}
	if m.archiveSrc.Load() != nil {
		return nil // a bundled replay stays hermetic: the live pack must not fill its holes
	}
	return l
}

// serveFromMount delivers base from local mounts and reports whether it did; a read error never quarantines,
// because it is usually transient: only a decode failure does.
func (m *Manager) serveFromMount(l *MountLayer, base, deliverBase string, t AssetType) bool {
	key, data, ok := m.packBytes(l, base, t)
	if !ok {
		return false
	}
	m.mountFetches.Add(1)
	// T1 evicts long before T2, and each evicted pack file would otherwise re-read multiple MiB from disk.
	m.t2.Add(l.LocalURLFor(key), data, int64(len(data)))
	m.deliver(l.LocalURLFor(key), deliverBase, t, data, true)
	return true
}

// packBytes walks the pack's format list for base and returns the winning index key and its bytes.
func (m *Manager) packBytes(l *MountLayer, base string, t AssetType) (key string, data []byte, ok bool) {
	idx := l.Index()
	if idx == nil || !idx.acquire() {
		return "", nil, false
	}
	defer idx.release()

	rel, has := l.RelOf(base)
	if !has || mountLayerExcluded(rel) {
		return "", nil, false
	}
	folded := foldRel(rel)
	mask, haveStem := idx.LookupStem(folded)

	for _, ext := range m.resolver.PackFormatList(t) {
		// A fast reject; an extension outside indexedExts has no bit, so it falls through to a direct probe.
		if bit, indexed := extBit(ext); indexed && haveStem && mask&bit == 0 {
			continue
		}
		k := folded + ext
		f, hit := idx.LookupExact(k)
		if !hit || idx.IsBad(k) {
			continue
		}
		b, err := idx.ReadFile(f, k)
		if err != nil || len(b) == 0 {
			continue // fall through to the next candidate, then to the network
		}
		return k, b, true
	}
	return "", nil, false
}

// packExactBytes is packBytes for a complete URL: a format walk would only make keys like char.ini.webp.
func (m *Manager) packExactBytes(l *MountLayer, url string) ([]byte, bool) {
	idx := l.Index()
	if idx == nil || !idx.acquire() {
		return nil, false
	}
	defer idx.release()

	rel, has := l.RelOf(url)
	if !has || mountLayerExcluded(rel) {
		return nil, false
	}
	folded := foldRel(rel)
	f, hit := idx.LookupExact(folded)
	if !hit || idx.IsBad(folded) {
		return nil, false
	}
	b, err := idx.ReadFile(f, folded)
	if err != nil || len(b) == 0 {
		return nil, false // a pack failure is never a server failure: fall through
	}
	return b, true
}

// FetchRawLayered is FetchRaw that checks local packs first, for text a pack ships such as char.ini.
func (m *Manager) FetchRawLayered(ctx context.Context, url string) ([]byte, error) {
	if l := m.activeMountLayer(); l != nil {
		if b, ok := m.packExactBytes(l, url); ok {
			m.mountFetches.Add(1)
			return b, nil
		}
	}
	return m.FetchRaw(ctx, url)
}

// ResolveRawLayered is ResolveRaw that sees local packs, for the content report and scene exporter.
func (m *Manager) ResolveRawLayered(base string, t AssetType) (url string, data []byte, fromPack, ok bool) {
	if base == "" || !t.Valid() {
		return "", nil, false, false
	}
	if l := m.activeMountLayer(); l != nil {
		if key, b, hit := m.packBytes(l, base, t); hit {
			return l.LocalURLFor(key), b, true, true
		}
	}
	u, b, hit := m.ResolveRaw(base, t)
	return u, b, false, hit
}

// PackCovers reports whether local packs hold anything for base, by index lookup without reading.
func (m *Manager) PackCovers(base string) bool {
	l := m.activeMountLayer()
	return l != nil && l.Covers(base)
}

// QuarantinePack skips a pack entry that failed to decode, so the server's copy is fetched;
// false means not this layer's URL or already quarantined, and the caller must then record nothing.
func (m *Manager) QuarantinePack(url string) bool {
	l := m.activeMountLayer()
	if l == nil {
		return false
	}
	rel, ok := l.PackRelOf(url)
	if !ok {
		return false
	}
	idx := l.Index()
	if idx == nil || !idx.MarkBad(rel) {
		return false
	}
	m.packQuarantined.Add(1)
	return true
}

// Thumbs returns the optional thumbnail store, nil when not wired.
func (m *Manager) Thumbs() *ThumbCache { return m.thumbs }

// PurgeCorrupt evicts url from T2 and T3 so the next demand refetches; url is the full fetch URL, never the base.
func (m *Manager) PurgeCorrupt(url string) {
	if url == "" {
		return
	}
	if m.t2 != nil {
		m.t2.Remove(url)
	}
	if !m.skipDisk(url) && m.disk != nil {
		m.disk.Delete(url)
	}
}

// ClearDisk wipes the T3 cache.
func (m *Manager) ClearDisk() error {
	return m.disk.Clear()
}

// DiskRoot returns the T3 directory.
func (m *Manager) DiskRoot() string {
	if m.disk == nil {
		return ""
	}
	return m.disk.Root()
}

// SetDiskCompression toggles zstd for new T3 writes.
func (m *Manager) SetDiskCompression(on bool) {
	if m.disk != nil {
		m.disk.SetCompression(on)
	}
}

// SetDiskBudget sets the T3 auto-prune byte cap; 0 = unlimited, so no cache is silently deleted.
func (m *Manager) SetDiskBudget(bytes int64) {
	if m.disk != nil {
		m.disk.SetBudget(bytes)
	}
}

// T2Stats snapshots the T2 counters.
func (m *Manager) T2Stats() cache.MemoryStats {
	if m.t2 == nil {
		return cache.MemoryStats{}
	}
	return m.t2.Stats()
}

// DiskStats snapshots the T3 counters from cached atomics, without a directory walk, so it is safe per frame.
func (m *Manager) DiskStats() cache.DiskStats {
	if m.disk == nil {
		return cache.DiskStats{}
	}
	return m.disk.Stats()
}

// reportMissing raises the missing-asset warning, which may be dropped under flood.
func (m *Manager) reportMissing(base string, t AssetType, tried []string) {
	m.missing.Add(1)
	w := Warning{Base: base, Type: t, Tried: tried}
	select {
	case m.warningCh <- w:
	default:
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// HostOf returns the host of an asset URL, the key of the learned-format table.
func HostOf(url string) string { return hostOf(url) }

func hostOf(base string) string {
	const sep = "://"
	i := strings.Index(base, sep)
	if i < 0 {
		return ""
	}
	rest := base[i+len(sep):]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// ManagerStats is a point-in-time counter snapshot.
type ManagerStats struct {
	T1Hits     int64
	T2Hits     int64
	DiskHits   int64
	NetFetches int64
	Missing    int64
	// MountFetches counts assets served from local mounts.
	MountFetches int64
	// PackQuarantined counts pack entries withdrawn after a decode failure.
	PackQuarantined int64
}

// Stats snapshots the manager's counters.
func (m *Manager) Stats() ManagerStats {
	return ManagerStats{
		T1Hits:          m.t1Hits.Load(),
		T2Hits:          m.t2Hits.Load(),
		DiskHits:        m.diskHits.Load(),
		NetFetches:      m.netFetches.Load(),
		Missing:         m.missing.Load(),
		MountFetches:    m.mountFetches.Load(),
		PackQuarantined: m.packQuarantined.Load(),
	}
}
