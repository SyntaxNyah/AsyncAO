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
	// decodedChanCap bounds the decoded-asset handoff to the render thread.
	// When full, decoder workers block briefly (the render thread drains
	// every frame); results are never dropped (spec §17.7).
	decodedChanCap = 64
	// audioChanCap bounds the raw-audio handoff to the audio system.
	audioChanCap = 64
	// warningChanCap bounds missing-asset warnings to the UI; warnings are
	// droppable advisories, results are not.
	warningChanCap = 32
	// musicFailChanCap bounds the music-fetch-failure advisory lane to the UI.
	// A transient music failure (timeout / 5xx / host backoff) is otherwise
	// completely invisible — counted in the pump, never surfaced (§1.1) — so
	// the jukebox warn line needs a signal. Droppable like warnings (newest
	// wins on flood); scoped strictly to AssetTypeMusic so sprite backoff
	// bursts never reach it. Small because at most one track plays at a time.
	musicFailChanCap = 8
)

// DecodedAsset is the manager's handoff to the render thread: decoded frames
// ready for texture upload, or the error that ended the attempt.
type DecodedAsset struct {
	URL   string
	Base  string
	Type  AssetType
	Asset *Decoded
	Err   error
	// Transient marks a NETWORK-stage failure (5xx, timeout, host backoff):
	// the bytes were never seen, so nothing is known about the asset itself
	// and it must stay re-demandable. Only non-transient errors (the bytes
	// arrived and failed to DECODE) may enter the texture store's negative
	// cache — conflating the two pinned every asset touched during a flaky
	// origin's backoff window as "failed" for decodeFailTTL (the
	// "whole server's files go missing in waves" report).
	Transient bool
}

// AudioAsset is the manager's handoff to the audio system: raw bytes for
// SDL_mixer (decode happens in C at native speed — spec §8).
type AudioAsset struct {
	URL  string
	Base string
	Type AssetType
	Data []byte
}

// Warning reports an asset that 404'd in every probed format, for the
// visible in-client warning (spec §4).
type Warning struct {
	Base  string
	Type  AssetType
	Tried []string
}

// MusicFailure reports a TRANSIENT music-fetch failure (timeout / 5xx / host
// backoff — never a 404, which is a definitive miss surfaced elsewhere) so the
// jukebox can tell the user a track silently didn't load (§1.1). Scoped to
// AssetTypeMusic only: sprite/icon transient bursts must not surface here.
type MusicFailure struct {
	URL string
	Err error
}

// Fetcher is the byte source the manager probes: network.Client for asset
// streaming, LocalFetcher for the no-streaming legacy mode.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Manager walks the tiers per spec §8: T1 texture hit → done; T2 raw
// hit → decode; T3 disk hit → promote + decode + learn; else network probe
// by candidate order, then T2 + async T3 + learn + decode; all-404 → warning.
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

	// t1Contains asks the render side whether a texture is already uploaded;
	// nil means "no T1 yet" (headless tests). The TextureStore keys probed
	// assets by BASE (extension-free) and exact assets by their full URL —
	// callers must pass whichever key the upload used.
	t1Contains func(url string) bool
	// t1Failed asks the render side whether base recently failed to DECODE (a
	// corrupt/truncated payload); the prefetch gate backs off so one bad asset
	// can't storm the network + log every retry. nil in headless tests.
	t1Failed func(base string) bool

	decodedCh   chan DecodedAsset
	audioCh     chan AudioAsset
	warningCh   chan Warning
	musicFailCh chan MusicFailure

	// deliveryNotify, when set, fires after each decodedCh/audioCh send — the
	// experimental event-driven render loop's wake hook, so a finished decode
	// uploads (and an audio payload plays) on the next pass instead of waiting
	// out an idle tick. Called from pool workers: must be cheap, non-blocking,
	// and SDL-free here (the UI injects an SDL wake-event push).
	deliveryNotify atomic.Pointer[func()]

	inflight sync.Map // base|type → struct{}: one pipeline pass per asset

	// offline gates every network egress (rehearsal mode: a server's
	// already-cached assets browse, nothing probes). Cache-tier reads
	// keep working; misses report as not-found without touching the
	// client, so no 404s get learned from being offline.
	offline atomic.Bool

	// archiveSrc, when set, is consulted BEFORE the normal client in netFetch —
	// a bundled-scene replay points it at the archive folder so the same shared
	// Manager (whose Decoded channel the render Pump drains) serves the archive's
	// local:// URLs. A miss falls through to the normal source, so non-archive
	// fetches (theme/UI chrome) during a replay are unaffected. atomic: the UI
	// thread sets/clears it while pool workers read it.
	archiveSrc atomic.Pointer[Fetcher]

	t1Hits     atomic.Int64
	t2Hits     atomic.Int64
	diskHits   atomic.Int64
	netFetches atomic.Int64
	missing    atomic.Int64
}

// SetOffline flips rehearsal mode's network gate.
func (m *Manager) SetOffline(on bool) { m.offline.Store(on) }

// netFetch is the manager's ONLY network egress: the offline gate lives
// here so every pipeline path (probe chains, raw text, sync fetch)
// respects rehearsal mode structurally.
func (m *Manager) netFetch(ctx context.Context, url string) ([]byte, error) {
	if m.offline.Load() {
		return nil, network.ErrAssetNotFound
	}
	// Bundled-archive replay: serve the archive folder first; a miss falls
	// through so concurrent non-archive fetches (theme/UI) still hit the network.
	if ov := m.archiveSrc.Load(); ov != nil {
		if data, err := (*ov).Fetch(ctx, url); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	return m.client.Fetch(ctx, url)
}

// SetArchiveSource routes fetches through f (an archive folder's LocalFetcher)
// before the normal source, for the duration of a bundled-scene replay.
// ClearArchiveSource restores normal fetching.
func (m *Manager) SetArchiveSource(f Fetcher) { m.archiveSrc.Store(&f) }

// ClearArchiveSource removes the bundled-archive source override.
func (m *Manager) ClearArchiveSource() { m.archiveSrc.Store(nil) }

// SetAssetOrigin forwards a power-user Origin / CORS header override to the network
// source. No-op in local / no-streaming mode (the source isn't a network client).
func (m *Manager) SetAssetOrigin(origin string) {
	if c, ok := m.client.(*network.Client); ok {
		c.SetAssetOrigin(origin)
	}
}

// SetAdaptiveLatencyMultiple forwards the power-user per-host deadline multiple
// to the network source (0 = the built-in default). No-op in local mode.
func (m *Manager) SetAdaptiveLatencyMultiple(n int) {
	if c, ok := m.client.(*network.Client); ok {
		c.SetAdaptiveLatencyMultiple(n)
	}
}

// SetSpriteCap forwards the decode-downscale height cap to the decoder pool
// (0 = no cap). Live-safe; applies to NEW decodes.
func (m *Manager) SetSpriteCap(px int) { m.decoder.SetSpriteCap(px) }

// ColdLoadStats reports the fetch (all-hosts TTFB) and decode (+fit) EWMAs for
// the debug overlay's cold-load profiling line; the upload stage comes from the
// render-side TextureStore. Zeroes until samples exist / in local mode.
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
	// Source is the byte origin: network.Client when streaming from the
	// server's asset URL, LocalFetcher in no-streaming mode.
	Source Fetcher
	// LocalMode skips the T3 disk cache (assets already live on disk).
	LocalMode  bool
	Pool       *network.Pool
	Decoder    *DecoderPool
	T1Contains func(url string) bool
	T1Failed   func(base string) bool
	// Thumbs, when set, receives every successfully-decoded character sprite for
	// low-q thumbnailing (the opt-in cold-load stand-in cache). nil = absent.
	Thumbs *ThumbCache
}

// NewManager builds the pipeline orchestrator.
func NewManager(deps ManagerDeps) *Manager {
	return &Manager{
		resolver:    deps.Resolver,
		prefs:       deps.Prefs,
		t2:          deps.T2,
		disk:        deps.Disk,
		client:      deps.Source,
		localMode:   deps.LocalMode,
		pool:        deps.Pool,
		decoder:     deps.Decoder,
		thumbs:      deps.Thumbs,
		t1Contains:  deps.T1Contains,
		t1Failed:    deps.T1Failed,
		decodedCh:   make(chan DecodedAsset, decodedChanCap),
		audioCh:     make(chan AudioAsset, audioChanCap),
		warningCh:   make(chan Warning, warningChanCap),
		musicFailCh: make(chan MusicFailure, musicFailChanCap),
	}
}

// SetDeliveryNotify installs (or clears, with nil) the callback fired after
// each Decoded/Audio channel delivery. Safe at any time from any goroutine.
func (m *Manager) SetDeliveryNotify(f func()) {
	if f == nil {
		m.deliveryNotify.Store(nil)
		return
	}
	m.deliveryNotify.Store(&f)
}

// notifyDelivery invokes the delivery wake callback if one is installed.
func (m *Manager) notifyDelivery() {
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

// MusicFailures returns the channel the UI drains for transient music-fetch
// failures — the jukebox warn line's source (§1.1). Advisory: drained on the
// render thread, never blocks a pool worker.
func (m *Manager) MusicFailures() <-chan MusicFailure { return m.musicFailCh }

// reportMusicFailure surfaces a transient music-fetch failure to the jukebox,
// scoped strictly to AssetTypeMusic (callers must check). Non-blocking like
// reportMissing: the lane is a droppable advisory (rule 4), never a result
// (rule 7 — the fetch's own delivery/error path is unchanged; this is an
// EXTRA signal). Fires the delivery wake so an idle event-loop redraws the
// warn line promptly.
func (m *Manager) reportMusicFailure(url string, err error) {
	select {
	case m.musicFailCh <- MusicFailure{URL: url, Err: err}:
		m.notifyDelivery()
	default:
	}
}

// Prefetch queues a full pipeline pass for base (URL without extension) at
// the given priority, tagged with the pool's current epoch so room changes
// cancel stale speculation.
func (m *Manager) Prefetch(base string, t AssetType, prio network.Priority) {
	m.PrefetchWithFallback(base, "", t, prio)
}

// PrefetchWithFallback is Prefetch with a second URL base probed only when
// every format of the first one 404s. AO sprite naming requires it: packs
// ship "(a)<emote>"/"(b)<emote>" files OR bare "<emote>" files, and
// AO2-Client probes the prefixed path then the unprefixed one
// (CharLayer::load_image's pathlist). The asset keeps the PRIMARY base as
// its identity everywhere (T1 key, scene layers); alt is just a second
// spelling of the same asset. The client's 404 cache keeps the extra probe
// from repeating inside its TTL, and once the texture is resident the T1
// short-circuit costs zero probes.
func (m *Manager) PrefetchWithFallback(base, alt string, t AssetType, prio network.Priority) {
	if alt == "" {
		m.PrefetchChain(base, nil, t, prio)
		return
	}
	m.PrefetchChain(base, []string{alt}, t, prio)
}

// PrefetchChain is the N-spelling generalization of PrefetchWithFallback:
// alts are further spellings of the SAME asset, probed in order only while
// every format of each earlier base 404s. Born for chatbox skins, which need
// two stems × two casings (AO2's chat→chatbox order on a case-insensitive
// filesystem, spoken over case-sensitive HTTP mirrors). base stays the
// identity whichever link answers; the chain is bounded by its call sites
// (rule §17.4) and every miss is 404-cached, so a settled chain costs zero
// probes inside the TTL.
func (m *Manager) PrefetchChain(base string, alts []string, t AssetType, prio network.Priority) {
	m.prefetchChain(base, alts, t, prio, false)
}

// PrefetchChainSpeculative is PrefetchChain for a PREDICTED (not yet demanded)
// asset: identical probing, but a total miss does NOT surface the §4
// missing-asset warning. The Markov prefetcher warms guesses that may 404 in
// every format on bare-named packs — reporting those would spam the debug log
// and the on-screen banner with warnings for assets no one asked for, and feed
// NotifyAssetMissing for a base that isn't on screen. The 404 cache and
// singleflight are untouched, so rule §17.6 (no re-probe inside the TTL) still
// holds. Callers pass PriorityLow so the speculation sheds under backpressure.
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

// PrefetchExact queues a pipeline pass for a COMPLETE URL (extension
// included) — AO music tracks ship their extension in the track name, and
// direct http(s) tracks are full URLs already. No candidate probing.
func (m *Manager) PrefetchExact(url string, t AssetType, prio network.Priority) {
	if url == "" || !t.Valid() {
		return
	}
	if m.t1Failed != nil && m.t1Failed(url) {
		return // recently failed to decode — back off
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

// resolveExact is the candidate-free pipeline pass behind PrefetchExact.
func (m *Manager) resolveExact(url string, t AssetType) {
	if _, loaded := m.inflight.LoadOrStore(url, struct{}{}); loaded {
		return
	}
	defer m.inflight.Delete(url)

	if m.t1Contains != nil && m.t1Contains(url) {
		m.t1Hits.Add(1)
		return
	}
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		m.deliver(url, url, t, data)
		return
	}
	if !m.localMode {
		if data, ok := m.disk.Get(url); ok {
			m.diskHits.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			m.deliver(url, url, t, data)
			return
		}
	}
	data, err := m.netFetch(context.Background(), url)
	switch {
	case err == nil:
		m.netFetches.Add(1)
		m.t2.Add(url, data, int64(len(data)))
		if !m.localMode {
			m.disk.Put(url, data)
		}
		m.deliver(url, url, t, data)
	case errors.Is(err, network.ErrAssetNotFound):
		m.reportMissing(url, t, nil)
	default:
		// Transient failure (timeout / 5xx / host backoff). Music is the one
		// exact-fetch type whose failure is otherwise silent (audio never
		// reaches the pump's decode path, so the "counted, not logged" pump
		// branch doesn't even see it) — surface it to the jukebox. Sprite/icon
		// exact fetches stay unlogged (scoped by type).
		if t == AssetTypeMusic {
			m.reportMusicFailure(url, err)
		}
		m.decodedCh <- DecodedAsset{URL: url, Base: url, Type: t, Err: err, Transient: true}
		m.notifyDelivery()
	}
}

// PrefetchRaw warms T2/T3 for a COMPLETE URL without decoding — text
// assets like char.ini. Hover-driven: picking a character then costs a
// memory hit instead of an RTT. Pool-bounded, inflight-deduped, silent
// on 404 (the negative cache absorbs retries).
func (m *Manager) PrefetchRaw(url string, prio network.Priority) {
	if url == "" {
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
			if !m.localMode {
				if data, ok := m.disk.Get(url); ok {
					m.diskHits.Add(1)
					m.t2.Add(url, data, int64(len(data)))
					return
				}
			}
			if data, err := m.netFetch(context.Background(), url); err == nil {
				m.netFetches.Add(1)
				m.t2.Add(url, data, int64(len(data)))
				if !m.localMode {
					m.disk.Put(url, data)
				}
			}
		},
	})
}

// FetchRaw synchronously fetches a complete URL through T2 → T3 → source
// without decoding — for text assets like char.ini. Call it off the render
// thread (UI screens use a goroutine).
func (m *Manager) FetchRaw(ctx context.Context, url string) ([]byte, error) {
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		return data, nil
	}
	if !m.localMode {
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
	if !m.localMode {
		m.disk.Put(url, data)
	}
	return data, nil
}

// ResolveRaw resolves an extensionless base to the first candidate URL whose
// bytes fetch — learned-first, the SAME candidate order the render path probes
// (BuildCandidates) — returning that complete URL and its bytes. Synchronous,
// for tooling (the scene-archive exporter) that needs the resolved file itself,
// not a decoded texture. It learns the winning format so a repeat call is a
// single-probe hit. ok=false when every candidate is missing.
//
// Because export and replay both resolve through this same candidate logic, the
// relative path an asset is written to (resolvedURL minus origin) is exactly the
// path replay will later request — symmetry by construction (no hand-built
// paths, no pre-seeded format table needed).
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

// ResolveRawFull is the DIAGNOSTIC sibling of ResolveRaw for a user-invoked,
// bounded packaging/report pass (contentjob.go's probe, the export loader's
// seed) — NOT the live render path. The difference is the miss policy for an
// UNLEARNED host:
//
//   - Learned host: try the learned format FIRST (one probe). A HIT returns
//     immediately — the fast single-probe path a manifest-seeded or single-format
//     host takes. A MISS falls through to the full walk rather than being honored
//     as a terminal miss: the learned entry may have been won opportunistically by
//     a sibling asset earlier in THIS pass (one shared per-(host,type) slot), and
//     honoring it would strand a mixed-format sibling at the wrong format (see the
//     WHY block on the method body). Confirming true absence across every format is
//     the diagnostic path's job; the extra probes are all 404-TTL-cached.
//   - Unlearned host (no manifest, or the manifest lacks this type): walk the
//     FULL configured chain (the type's format order + its legacy fallback
//     chain, deduped) instead of the zero-fallback single format, and
//     RecordSuccess the winner so the very next ResolveRaw/warm of the same host
//     is learned-first (a re-report or an export right after the report probes
//     once, not the whole chain again).
//
// WHY the zero-fallback pillar doesn't apply here: that pillar protects the
// LIVE render hot path — one network probe per asset, no speculative format
// storm competing with the session's traffic. This is a user-triggered
// diagnostic/packaging pass whose whole POINT is truth over probe count: a
// server that "definitely has" a .gif/.png sprite must be findable even though
// the client would never stream it that way live. The walk is still exactly one
// network probe per candidate, and every 404 is cached by the 404-TTL layer +
// collapsed by singleflight (FetchRaw → the same T2/T3/source pipeline), so a
// repeat pass over the same scene re-probes nothing. The learned-first branch
// above means a host whose learned format HITS is single-probed; only an unknown
// or learned-but-missing asset walks the chain (the latter is how a mixed-format
// sibling recovers from another sibling's opportunistically-learned format).
func (m *Manager) ResolveRawFull(base string, t AssetType) (string, []byte, bool) {
	if base == "" || !t.Valid() {
		return "", nil, false
	}
	host := hostOf(base)
	// Learned host → try the learned format FIRST (the fast, single-probe path a
	// manifest-seeded or single-format host takes). A HIT returns immediately.
	//
	// A MISS deliberately does NOT stop here — it falls through to the full walk
	// below. This is the mixed-format-honesty case: the learned entry may have
	// been won OPPORTUNISTICALLY by a sibling asset earlier in this same pass
	// (ResolveRaw/ResolveRawFull's RecordSuccess writes one shared per-(host,type)
	// slot), not authoritatively from a manifest. On a no-manifest host whose
	// assets carry MIXED per-asset formats — charA only .gif, charB only .png —
	// whichever probes first learns its format; honoring that as a terminal miss
	// for the OTHER asset would 404 it at the wrong format and falsely report a
	// real file Missing, nondeterministically by worker order. The learned table
	// can't tell manifest-authoritative from walk-opportunistic (both go through
	// RecordLearned), so the diagnostic path treats a learned MISS as "probe the
	// rest of the chain to confirm" rather than "it isn't there." Confirming true
	// absence across every format is exactly the diagnostic path's job; the
	// zero-fallback pillar (one probe, honor the miss) protects only the LIVE
	// render path, which never calls this method. Every re-probe here is one
	// network probe per candidate, cached by the 404-TTL layer + collapsed by
	// singleflight, so the walk after a learned miss adds no wire traffic once the
	// first sibling has probed the chain.
	if ext, ok := m.resolver.Learned(host, t); ok {
		url := base + ext
		if data, err := m.FetchRaw(context.Background(), url); err == nil && len(data) > 0 {
			return url, data, true
		}
		// fall through to the full walk (see WHY above)
	}
	// Unlearned host (or a learned format that missed) → walk the full configured
	// chain, learning the winner so the common SINGLE-format no-manifest server
	// (whole host is .png) is learned-first for the export warm that runs right
	// after. A subsequent sibling of a DIFFERENT format still full-walks via the
	// learned-miss fall-through above, so learning the winner never strands a
	// mixed-format sibling.
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

// fullProbeChain is the FULL diagnostic probe order for one asset type: the
// user's configured format order (which defaults to the zero-fallback single
// format) followed by the type's legacy fallback chain, deduped, order
// preserved. This is deliberately the type's OWN chain rather than one global
// image list, so audio types (SFX/Blip) walk .opus→.ogg/.wav/.mp3 while image
// types walk .webp→.apng/.gif/.png — each covering exactly the formats its
// decoder/mixer supports. Equivalent to FormatList with fallbacks forced on,
// which is the table-free FULL candidate set the diagnostic path needs.
func fullProbeChain(prefs *config.AssetPreferences, t AssetType) []string {
	name := t.Name()
	var order []string
	if prefs != nil {
		order = prefs.FormatOrder(name) // configured order (defaults to the zero-fallback list)
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

// PrefetchSticky is Prefetch for assets that survive room changes (UI
// chrome, theme bits).
func (m *Manager) PrefetchSticky(base string, t AssetType, prio network.Priority) {
	if base == "" || !t.Valid() {
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

// resolveChain runs the pipeline for primary, then — only while every
// format of each earlier name is missing — for each alt in order,
// delivering under primary so the asset's identity never changes.
// suppressMissing skips the final §4 warning for a total miss (speculative
// prefetches only — see PrefetchChainSpeculative).
func (m *Manager) resolveChain(primary string, alts []string, t AssetType, suppressMissing bool) {
	key := primary + config.LearnedKeySeparator + t.Name()
	if _, loaded := m.inflight.LoadOrStore(key, struct{}{}); loaded {
		return // an identical pass is already in flight
	}
	defer m.inflight.Delete(key)

	// T1: already a texture — nothing to do. Uploads from this path are
	// keyed by base (TextureStore.Upload(d.Base, …)), so the check must use
	// base too; checking the extension-included candidate URL can never hit.
	if m.t1Contains != nil && m.t1Contains(primary) {
		m.t1Hits.Add(1)
		return
	}

	host := hostOf(primary)
	done, tried := m.tryBase(primary, primary, t, host)
	if done {
		return
	}
	for i, alt := range alts {
		if alt == "" || alt == primary || containsString(alts[:i], alt) {
			continue // blank / duplicate spelling — nothing new to probe
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
	if suppressMissing {
		// Speculative pass: count the miss (metrics) but do not surface the
		// visible §4 warning for an asset no one demanded.
		m.missing.Add(1)
		return
	}
	m.reportMissing(primary, t, tried)
}

// tryBase walks one base's format candidates (learned-first), delivering a
// hit under deliverBase. done=true ends the whole pass: delivered, or a
// transport error already reported on the decoded channel (remaining
// probes on the same ailing host would only stack timeouts). A stale
// learned format triggers the one-shot full-list re-probe inline (spec §4).
func (m *Manager) tryBase(base, deliverBase string, t AssetType, host string) (done bool, tried404 []string) {
	cands := m.resolver.BuildCandidates(base, t, host)
	usedLearned := cands.Learned
	done, tried404 = m.walkCandidates(cands.URLs, base, deliverBase, t, host, make([]string, 0, len(cands.URLs)))
	m.resolver.PutCandidates(cands)
	if done || !usedLearned {
		return done, tried404
	}

	// Every learned-first candidate 404'd on a learned format: the server may
	// have repacked. Re-probe the full configured format list once, skipping
	// extensions already tried. We must NOT blank the shared learned slot to do
	// this: it is one slot per (host, AssetType) shared by every asset of that
	// type on the host, and clearing it opens a window in which a DIFFERENT
	// concurrent asset (resolved on another pool worker, unserialized) reads the
	// empty slot, falls back to the type default, and spuriously reports a file
	// that exists in the learned format as missing (the "every emote button
	// renders the same icon" report on a non-default-format host). Instead
	// BuildFullListCandidates returns the format list WITHOUT touching the table.
	// If a fallback format answers, walkCandidates' RecordSuccess re-learns it
	// via a single old-valid -> new-valid CAS (a genuine repack heals here). If
	// nothing answers, the asset is simply absent — and absence says nothing
	// about the host's formats, so the learned entry is left exactly as it was.
	cands = m.resolver.BuildFullListCandidates(base, t)
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

// walkCandidates probes urls in order through T2 → T3 → source. done=true
// ends the pass (delivered under deliverBase, or transport error reported);
// tried404 accumulates the extensions that 404'd.
func (m *Manager) walkCandidates(urls []string, base, deliverBase string, t AssetType, host string, tried404 []string) (bool, []string) {
	for _, url := range urls {
		ext := url[len(base):]

		// T2: raw bytes in memory — straight to decode.
		if data, ok := m.t2.Get(url); ok {
			m.t2Hits.Add(1)
			m.deliver(url, deliverBase, t, data)
			return true, tried404
		}
		// T3: disk — promote to T2, learn, decode (spec §8). Skipped in
		// local mode: the mounts ARE disk.
		if !m.localMode {
			if data, ok := m.disk.Get(url); ok {
				m.diskHits.Add(1)
				m.t2.Add(url, data, int64(len(data)))
				m.resolver.RecordSuccess(host, t, ext)
				m.deliver(url, deliverBase, t, data)
				return true, tried404
			}
		}
		// Source: network stream or local mounts.
		data, err := m.netFetch(context.Background(), url)
		switch {
		case err == nil:
			m.netFetches.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			if !m.localMode {
				m.disk.Put(url, data)
			}
			m.resolver.RecordSuccess(host, t, ext)
			m.deliver(url, deliverBase, t, data)
			return true, tried404
		case errors.Is(err, network.ErrAssetNotFound):
			tried404 = append(tried404, ext)
			continue // probe the next candidate
		default:
			// Transport trouble: the render side hears the error — tagged
			// transient, so it never enters the decode negative cache.
			m.decodedCh <- DecodedAsset{URL: url, Base: deliverBase, Type: t, Err: err, Transient: true}
			m.notifyDelivery()
			return true, tried404
		}
	}
	return false, tried404
}

// deliver routes payload bytes onward: audio types skip the decode pool
// entirely; images are decoded off-thread and land on the decoded channel.
func (m *Manager) deliver(url, base string, t AssetType, data []byte) {
	if t.IsAudio() {
		m.audioCh <- AudioAsset{URL: url, Base: base, Type: t, Data: data}
		m.notifyDelivery()
		return
	}
	m.decoder.Submit(DecodeRequest{
		URL:            url,
		Data:           data,
		Type:           t,
		PlayAnimations: m.prefs.AnimationsEnabled(),
		OnDone: func(doneURL string, d *Decoded, err error) {
			// Opt-in thumbnail store: every character sprite that decodes leaves a
			// tiny low-q stand-in behind (ThumbCache gates on its own enable and
			// Store is a non-blocking enqueue, so this is free when off).
			if m.thumbs != nil && err == nil && t == AssetTypeCharSprite {
				m.thumbs.Store(base, d)
			}
			m.decodedCh <- DecodedAsset{URL: doneURL, Base: base, Type: t, Asset: d, Err: err}
			m.notifyDelivery()
		},
	})
}

// Thumbs exposes the optional low-q thumbnail store (nil when the app was
// built/wired without one) — the ui reaches it for loads, knobs and Clear.
func (m *Manager) Thumbs() *ThumbCache { return m.thumbs }

// PurgeCorrupt evicts a URL's bytes from T2 and (unless local-mode, where the
// mounts ARE the source) queues its T3 blob for async deletion, so the next
// demand refetches clean bytes instead of re-promoting the same corrupt blob
// forever. url is the FULL fetch URL (extension included) that produced the
// corrupt payload — the exact key T2/T3 store under (never the sprite base).
//
// Called from the render thread's decode-error path: T2's onEvict is
// memory-only (byte accounting, no render callback), and disk.Delete never
// touches the disk on this goroutine — it enqueues onto the single async
// writer — so this stays off both the render-thread SDL rule and the no-sync-
// disk-I/O rule. Only NON-transient (corrupt-payload) failures may call this;
// a transient network failure never saw the bytes, so there is nothing to
// purge (see pump.go).
func (m *Manager) PurgeCorrupt(url string) {
	if url == "" {
		return
	}
	if m.t2 != nil {
		m.t2.Remove(url)
	}
	if !m.localMode && m.disk != nil {
		m.disk.Delete(url)
	}
}

// ClearDisk wipes the T3 cache (Settings "Clear Disk Cache" button).
func (m *Manager) ClearDisk() error {
	return m.disk.Clear()
}

// DiskRoot exposes the T3 directory (Settings cache browser).
func (m *Manager) DiskRoot() string {
	if m.disk == nil {
		return ""
	}
	return m.disk.Root()
}

// SetDiskCompression toggles zstd for new T3 writes (Settings, live-safe).
func (m *Manager) SetDiskCompression(on bool) {
	if m.disk != nil {
		m.disk.SetCompression(on)
	}
}

// SetDiskBudget sets the T3 auto-prune byte cap (#34; Settings slider,
// live-safe). 0 = unlimited (the default: T3 is a deliberate spec exception,
// so no cache is silently deleted). The writer goroutine sweeps oldest past it.
func (m *Manager) SetDiskBudget(bytes int64) {
	if m.disk != nil {
		m.disk.SetBudget(bytes)
	}
}

// T2Stats snapshots the byte-tier counters (Settings cache browser).
func (m *Manager) T2Stats() cache.MemoryStats {
	if m.t2 == nil {
		return cache.MemoryStats{}
	}
	return m.t2.Stats()
}

// DiskStats snapshots the T3 disk-tier counters from cached atomics (the debug
// cache inspector, #164) — no directory walk, so it is safe to read per frame.
func (m *Manager) DiskStats() cache.DiskStats {
	if m.disk == nil {
		return cache.DiskStats{}
	}
	return m.disk.Stats()
}

// reportMissing surfaces the §4 visible warning; the warning lane may drop
// under flood (advisory, not a result).
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

// hostOf extracts host:port from an absolute URL base (scheme://host/...).
// HostOf returns the host component of an asset URL/origin (the key the learned
// format table uses), exported for the archive exporter's replay seeding.
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
}

// Stats snapshots the manager's counters.
func (m *Manager) Stats() ManagerStats {
	return ManagerStats{
		T1Hits:     m.t1Hits.Load(),
		T2Hits:     m.t2Hits.Load(),
		DiskHits:   m.diskHits.Load(),
		NetFetches: m.netFetches.Load(),
		Missing:    m.missing.Load(),
	}
}
