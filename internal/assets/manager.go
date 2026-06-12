package assets

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

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
)

// DecodedAsset is the manager's handoff to the render thread: decoded frames
// ready for texture upload, or the error that ended the attempt.
type DecodedAsset struct {
	URL   string
	Base  string
	Type  AssetType
	Asset *Decoded
	Err   error
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

	// localMode skips the T3 disk cache (assets already live on disk).
	localMode bool

	// t1Contains asks the render side whether a texture is already uploaded;
	// nil means "no T1 yet" (headless tests). The TextureStore keys probed
	// assets by BASE (extension-free) and exact assets by their full URL —
	// callers must pass whichever key the upload used.
	t1Contains func(url string) bool

	decodedCh chan DecodedAsset
	audioCh   chan AudioAsset
	warningCh chan Warning

	inflight sync.Map // base|type → struct{}: one pipeline pass per asset

	t1Hits     atomic.Int64
	t2Hits     atomic.Int64
	diskHits   atomic.Int64
	netFetches atomic.Int64
	missing    atomic.Int64
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
}

// NewManager builds the pipeline orchestrator.
func NewManager(deps ManagerDeps) *Manager {
	return &Manager{
		resolver:   deps.Resolver,
		prefs:      deps.Prefs,
		t2:         deps.T2,
		disk:       deps.Disk,
		client:     deps.Source,
		localMode:  deps.LocalMode,
		pool:       deps.Pool,
		decoder:    deps.Decoder,
		t1Contains: deps.T1Contains,
		decodedCh:  make(chan DecodedAsset, decodedChanCap),
		audioCh:    make(chan AudioAsset, audioChanCap),
		warningCh:  make(chan Warning, warningChanCap),
	}
}

// Decoded returns the channel the render thread drains for texture uploads.
func (m *Manager) Decoded() <-chan DecodedAsset { return m.decodedCh }

// Audio returns the channel the audio system drains for SDL_mixer loads.
func (m *Manager) Audio() <-chan AudioAsset { return m.audioCh }

// Warnings returns the channel the UI drains for missing-asset banners.
func (m *Manager) Warnings() <-chan Warning { return m.warningCh }

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
	if base == "" || !t.Valid() {
		return
	}
	m.pool.Submit(prio, network.Job{
		ID:    m.pool.NextID(),
		Epoch: m.pool.Epoch(),
		Run: func(stale bool) {
			if stale {
				return
			}
			m.resolveChain(base, alt, t)
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
	data, err := m.client.Fetch(context.Background(), url)
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
		m.decodedCh <- DecodedAsset{URL: url, Base: url, Type: t, Err: err}
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
			if data, err := m.client.Fetch(context.Background(), url); err == nil {
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
	data, err := m.client.Fetch(ctx, url)
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
			m.resolveChain(base, "", t)
		},
	})
}

// resolveChain runs the pipeline for primary, then — only when the primary
// name is missing in every format — for alt, delivering under primary so
// the asset's identity never changes.
func (m *Manager) resolveChain(primary, alt string, t AssetType) {
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
	if alt != "" && alt != primary {
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

	// Every learned-first candidate 404'd on a learned format: the server
	// may have repacked. Invalidate and probe the full list once, skipping
	// extensions already tried.
	m.resolver.Invalidate(host, t)
	cands = m.resolver.BuildCandidates(base, t, host)
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
		data, err := m.client.Fetch(context.Background(), url)
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
			// Transport trouble: the render side hears the error.
			m.decodedCh <- DecodedAsset{URL: url, Base: deliverBase, Type: t, Err: err}
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
		return
	}
	m.decoder.Submit(DecodeRequest{
		URL:            url,
		Data:           data,
		Type:           t,
		PlayAnimations: m.prefs.AnimationsEnabled(),
		OnDone: func(doneURL string, d *Decoded, err error) {
			m.decodedCh <- DecodedAsset{URL: doneURL, Base: base, Type: t, Asset: d, Err: err}
		},
	})
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

// T2Stats snapshots the byte-tier counters (Settings cache browser).
func (m *Manager) T2Stats() cache.MemoryStats {
	if m.t2 == nil {
		return cache.MemoryStats{}
	}
	return m.t2.Stats()
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
