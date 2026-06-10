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

	// t1Contains asks the render side whether a texture for url is already
	// uploaded; nil means "no T1 yet" (headless tests).
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
			m.resolve(base, t)
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
			m.resolve(base, t)
		},
	})
}

// resolve runs one pipeline pass on a pool worker goroutine.
func (m *Manager) resolve(base string, t AssetType) {
	key := base + config.LearnedKeySeparator + t.Name()
	if _, loaded := m.inflight.LoadOrStore(key, struct{}{}); loaded {
		return // an identical pass is already in flight
	}
	defer m.inflight.Delete(key)

	host := hostOf(base)
	cands := m.resolver.BuildCandidates(base, t, host)
	defer m.resolver.PutCandidates(cands)

	tried := make([]string, 0, len(cands.URLs))
	usedLearned := cands.Learned

	for _, url := range cands.URLs {
		ext := url[len(base):]
		tried = append(tried, ext)

		// T1: already a texture — nothing to do.
		if m.t1Contains != nil && m.t1Contains(url) {
			m.t1Hits.Add(1)
			return
		}
		// T2: raw bytes in memory — straight to decode.
		if data, ok := m.t2.Get(url); ok {
			m.t2Hits.Add(1)
			m.deliver(url, base, t, data)
			return
		}
		// T3: disk — promote to T2, learn, decode (spec §8). Skipped in
		// local mode: the mounts ARE disk.
		if !m.localMode {
			if data, ok := m.disk.Get(url); ok {
				m.diskHits.Add(1)
				m.t2.Add(url, data, int64(len(data)))
				m.resolver.RecordSuccess(host, t, ext)
				m.deliver(url, base, t, data)
				return
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
			m.deliver(url, base, t, data)
			return
		case errors.Is(err, network.ErrAssetNotFound):
			continue // probe the next candidate
		default:
			// Transport trouble: the render side hears the error; remaining
			// candidates on the same ailing host would only stack timeouts.
			m.decodedCh <- DecodedAsset{URL: url, Base: base, Type: t, Err: err}
			return
		}
	}

	// Every candidate 404'd. If we trusted a learned format, the server may
	// have repacked: invalidate and re-probe the full list once.
	if usedLearned {
		m.resolver.Invalidate(host, t)
		m.resolveFullList(base, t, host, tried)
		return
	}
	m.reportMissing(base, t, tried)
}

// resolveFullList is the one-shot retry after a stale learned format.
func (m *Manager) resolveFullList(base string, t AssetType, host string, alreadyTried []string) {
	cands := m.resolver.BuildCandidates(base, t, host)
	defer m.resolver.PutCandidates(cands)

	tried := alreadyTried
	for _, url := range cands.URLs {
		ext := url[len(base):]
		if containsString(alreadyTried, ext) {
			continue // the learned ext we just 404'd
		}
		tried = append(tried, ext)
		data, err := m.client.Fetch(context.Background(), url)
		switch {
		case err == nil:
			m.netFetches.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			if !m.localMode {
				m.disk.Put(url, data)
			}
			m.resolver.RecordSuccess(host, t, ext)
			m.deliver(url, base, t, data)
			return
		case errors.Is(err, network.ErrAssetNotFound):
			continue
		default:
			m.decodedCh <- DecodedAsset{URL: url, Base: base, Type: t, Err: err}
			return
		}
	}
	m.reportMissing(base, t, tried)
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
