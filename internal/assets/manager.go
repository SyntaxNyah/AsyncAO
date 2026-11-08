package assets

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// decodedChanCap bounds the render-thread handoff; when full, workers block, so results never drop.
	decodedChanCap = 64
	// audioChanCap bounds the raw-audio handoff to the audio system.
	audioChanCap = 64
	// warningChanCap bounds missing-asset warnings; these may be dropped, results may not.
	warningChanCap = 32
	// musicFailChanCap bounds music-fetch-failure notices, which may drop; sprite failures never report here.
	musicFailChanCap = 8
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
	// FromPack marks bytes from the player's folders; Base is still the server's URL, so never MarkFailed(Base) on
	// them, or a corrupt local file takes the server's good copy offline.
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

// Warning reports an asset that was found nowhere.
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

// Fetcher is a byte source: the network client, or a replay archive's folder.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Manager answers asset requests through internal/media, in the order memory, disk cache, the player's folders,
// then the web, and hands results to the render thread and the audio system.
type Manager struct {
	bridge   *mediaBridge
	resolver *Resolver
	prefs    *config.AssetPreferences
	t2       *cache.ByteBudgetLRU[string, []byte]
	disk     *cache.DiskCache
	client   Fetcher
	decoder  *DecoderPool
	thumbs   *ThumbCache // optional persistent low-q sprite thumbnails (nil = feature absent)

	// t1Contains reports whether a texture is uploaded; pass the upload's key: base if probed, full URL if exact.
	t1Contains func(url string) bool
	// t1Failed reports a recent decode failure of base, so one bad asset cannot storm the network.
	t1Failed func(base string) bool

	decodedCh   chan DecodedAsset
	audioCh     chan AudioAsset
	warningCh   chan Warning
	musicFailCh chan MusicFailure

	// deliveryNotify wakes the event-driven render loop; on workers, so cheap, non-blocking and SDL-free.
	deliveryNotify atomic.Pointer[func()]

	// refused counts requests the media system turned away at its cap.
	refused atomic.Int64

	// offline blocks every network fetch; caches and folders still read, and misses learn no 404s.
	offline atomic.Bool

	// archiveSrc answers a bundled replay's own local:// origin; set by UI, read by workers.
	archiveSrc atomic.Pointer[Fetcher]

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

// netFetch is the manager's only network egress, so every path respects the offline block.
func (m *Manager) netFetch(ctx context.Context, url string) ([]byte, error) {
	// Above the offline check on purpose: archive reads are disk, not network.
	if ov := m.archiveSrc.Load(); ov != nil {
		if data, err := (*ov).Fetch(ctx, url); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	// A local:// URL the folders and the archive could not answer is absent, never a network request.
	if strings.HasPrefix(url, LocalScheme) {
		return nil, network.ErrAssetNotFound
	}
	if m.offline.Load() {
		return nil, network.ErrAssetNotFound
	}
	return m.client.Fetch(ctx, url)
}

// skipDisk reports whether url bypasses the disk cache: local:// URLs are already on disk.
func skipDisk(url string) bool { return strings.HasPrefix(url, LocalScheme) }

// SetArchiveSource routes fetches through f first during a bundled-scene replay.
func (m *Manager) SetArchiveSource(f Fetcher) { m.archiveSrc.Store(&f) }

// ClearArchiveSource removes the bundled-archive source.
func (m *Manager) ClearArchiveSource() { m.archiveSrc.Store(nil) }

// archiveServes reports whether a bundled replay's folder owns url's origin.
func (m *Manager) archiveServes(url string) bool {
	ov := m.archiveSrc.Load()
	if ov == nil {
		return false
	}
	lf, ok := (*ov).(*LocalFetcher)
	return ok && strings.HasPrefix(url, lf.BaseURL())
}

// SetAssetOrigin sets the Origin / CORS header override on the network source.
func (m *Manager) SetAssetOrigin(origin string) {
	if c, ok := m.client.(*network.Client); ok {
		c.SetAssetOrigin(origin)
	}
}

// SetAdaptiveLatencyMultiple sets the per-host deadline multiple (0 = default).
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

// ColdLoadStats reports the fetch and decode EWMAs; zero until samples exist.
func (m *Manager) ColdLoadStats() (fetch, decode time.Duration) {
	if c, ok := m.client.(*network.Client); ok {
		fetch = c.AvgTTFB()
	}
	return fetch, m.decoder.Stats().AvgDecode
}

// ManagerDeps wires a Manager; every field is required except T1Contains, T1Failed and Thumbs.
type ManagerDeps struct {
	Resolver *Resolver
	Prefs    *config.AssetPreferences
	T2       *cache.ByteBudgetLRU[string, []byte]
	Disk     *cache.DiskCache
	// Source is the network client, or a fake in tests.
	Source     Fetcher
	Decoder    *DecoderPool
	T1Contains func(url string) bool
	T1Failed   func(base string) bool
	// Thumbs, when set, receives every decoded character sprite for thumbnailing.
	Thumbs *ThumbCache
}

// NewManager builds the Manager and starts its media system; CloseMedia stops it.
func NewManager(deps ManagerDeps) *Manager {
	m := &Manager{
		resolver:    deps.Resolver,
		prefs:       deps.Prefs,
		t2:          deps.T2,
		disk:        deps.Disk,
		client:      deps.Source,
		decoder:     deps.Decoder,
		thumbs:      deps.Thumbs,
		t1Contains:  deps.T1Contains,
		t1Failed:    deps.T1Failed,
		decodedCh:   make(chan DecodedAsset, decodedChanCap),
		audioCh:     make(chan AudioAsset, audioChanCap),
		warningCh:   make(chan Warning, warningChanCap),
		musicFailCh: make(chan MusicFailure, musicFailChanCap),
	}
	m.bridge = newMediaBridge(m)
	return m
}

// WorkStats shows what the decoders and handoffs hold, for diagnostics.
type WorkStats struct {
	Refused                   int64
	DecodeQueued, DecodeCap   int
	DecodeWorkers             int
	DecodedQueued, DecodedCap int
	AudioQueued, AudioCap     int
	Decoding                  []network.WorkItem
}

// WorkStats snapshots the decoders and handoffs; it allocates, so call it only while the debug panel shows it.
func (m *Manager) WorkStats() WorkStats {
	return WorkStats{
		Refused:       m.refused.Load(),
		DecodeQueued:  len(m.decoder.jobs),
		DecodeCap:     cap(m.decoder.jobs),
		DecodeWorkers: m.decoder.workers,
		DecodedQueued: len(m.decodedCh),
		DecodedCap:    cap(m.decodedCh),
		AudioQueued:   len(m.audioCh),
		AudioCap:      cap(m.audioCh),
		Decoding:      m.decoder.running.Snapshot(),
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

// Prefetch asks for base (URL without extension), with no other spelling.
func (m *Manager) Prefetch(base string, t AssetType, prio network.Priority) {
	m.PrefetchChain(base, nil, t, prio)
}

// PrefetchChain asks for base, then each alt while earlier names miss, like CharLayer::load_image ("(a)X" then "X");
// call sites bound the length (hard rule 4).
func (m *Manager) PrefetchChain(base string, alts []string, t AssetType, prio network.Priority) {
	m.prefetchChain(base, alts, t, prio, false)
}

// PrefetchChainSpeculative is PrefetchChain with no warning on a total miss; pass PriorityLow so it sheds.
func (m *Manager) PrefetchChainSpeculative(base string, alts []string, t AssetType, prio network.Priority) {
	m.prefetchChain(base, alts, t, prio, true)
}

func (m *Manager) prefetchChain(base string, alts []string, t AssetType, prio network.Priority, quiet bool) {
	if base == "" || !t.Valid() {
		return
	}
	if m.t1Failed != nil && m.t1Failed(base) {
		return // recently failed to decode — back off (the negative cache absorbs retries)
	}
	m.bridge.askChain(base, alts, t, prio, quiet, false)
}

// PrefetchSticky is Prefetch for assets that survive room changes, such as UI chrome.
func (m *Manager) PrefetchSticky(base string, t AssetType, prio network.Priority) {
	if base == "" || !t.Valid() {
		return
	}
	m.bridge.askChain(base, nil, t, prio, false, true)
}

// PrefetchExact asks for a complete URL with no candidate probing, such as a music track.
func (m *Manager) PrefetchExact(url string, t AssetType, prio network.Priority) {
	if url == "" || !t.Valid() {
		return
	}
	if m.t1Failed != nil && m.t1Failed(url) {
		return // recently failed to decode — back off
	}
	m.bridge.askExact(url, t, prio)
}

// PrefetchRaw warms the caches for a complete URL without decoding (char.ini on hover); silent on 404.
func (m *Manager) PrefetchRaw(url string, prio network.Priority) {
	if url != "" {
		m.bridge.askRaw(url, prio)
	}
}

// IsMissing reports whether base is remembered as found nowhere, so asking again now costs no request; nil-safe.
func (m *Manager) IsMissing(base string, t AssetType) bool {
	if m == nil {
		return false
	}
	return m.bridge.isMissing(base, t)
}

// ForgetMissing asks again for everything remembered as missing, including the web's 404 memory, and reports how
// many assets that was: the "Retry missing assets" button, a deliberate re-ask.
func (m *Manager) ForgetMissing() int {
	if m == nil {
		return 0
	}
	n := m.bridge.forgetMissing("")
	if c, ok := m.client.(*network.Client); ok {
		c.ClearNotFound()
	}
	return n
}

// ForgetMissingUnder asks again for one origin's missing assets on (re)connect, leaving other tabs' servers alone.
func (m *Manager) ForgetMissingUnder(origin string) {
	if m == nil {
		return
	}
	m.bridge.forgetMissing(origin)
}

// skipExhausted replaces a request for a missing asset: no fetch, and the warning still fires.
func (m *Manager) skipExhausted(base string, t AssetType, quiet bool) {
	if quiet {
		return // speculative: no one asked for this, so no one hears about it
	}
	select {
	case m.warningCh <- Warning{Base: base, Type: t}:
	default:
	}
}

// FetchRaw fetches a complete URL through memory, the disk cache, the player's folders and the web without decoding;
// call it off the render thread.
func (m *Manager) FetchRaw(ctx context.Context, url string) ([]byte, error) {
	data, _, err := m.fetchRaw(ctx, url)
	return data, err
}

// fetchRaw is FetchRaw that also says whether the bytes came from the player's folders.
func (m *Manager) fetchRaw(ctx context.Context, url string) (data []byte, fromPack bool, err error) {
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		return data, false, nil
	}
	if !skipDisk(url) {
		if data, ok := m.disk.Get(url); ok {
			m.diskHits.Add(1)
			m.t2.Add(url, data, int64(len(data)))
			return data, false, nil
		}
	}
	if data, ok := m.bridge.folders.read(url); ok {
		return data, true, nil // counted by the folder source
	}
	data, err = m.netFetch(ctx, url)
	if err != nil {
		return nil, false, err
	}
	m.netFetches.Add(1)
	m.t2.Add(url, data, int64(len(data)))
	if !skipDisk(url) {
		m.disk.Put(url, data)
	}
	return data, false, nil
}

// ResolveRaw returns the first candidate whose bytes fetch and learns its format; export and replay share this order.
func (m *Manager) ResolveRaw(base string, t AssetType) (string, []byte, bool) {
	url, data, _, ok := m.ResolveRawLayered(base, t)
	return url, data, ok
}

// ResolveRawLayered is ResolveRaw that also says whether the bytes came from the player's folders, for the content
// report and scene exporter.
func (m *Manager) ResolveRawLayered(base string, t AssetType) (url string, data []byte, fromPack, ok bool) {
	if base == "" || !t.Valid() {
		return "", nil, false, false
	}
	host := hostOf(base)
	cands := m.resolver.BuildCandidates(base, t, host)
	defer m.resolver.PutCandidates(cands)
	for _, url := range cands.URLs {
		data, fromPack, err := m.fetchRaw(context.Background(), url)
		if err == nil && len(data) > 0 {
			m.resolver.RecordSuccess(host, t, url[len(base):]) // learn the winning ext
			return url, data, fromPack, true
		}
	}
	return "", nil, false, false
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

// onDecoded hands one decoded page to the render thread.
func (m *Manager) onDecoded(url, base string, t AssetType, fromPack bool, d *Decoded, err error) {
	// Never for folder bytes: this disk cache is keyed by the server URL, so user art would pose as its copy.
	if m.thumbs != nil && err == nil && t == AssetTypeCharSprite && !fromPack &&
		d != nil && len(d.Frames) > 0 && d.FrameOffset == 0 {
		// Store only frame 0: later appends and the finalize would overwrite it with a mid-clip or empty frame.
		m.thumbs.Store(base, d)
	}
	m.decodedCh <- DecodedAsset{URL: url, Base: base, Type: t, Asset: d, Err: err, FromPack: fromPack}
	m.NotifyDelivery()
}

// Thumbs returns the optional thumbnail store, nil when not wired.
func (m *Manager) Thumbs() *ThumbCache { return m.thumbs }

// PurgeCorrupt evicts url from memory and the disk cache so the next demand refetches; url is the full fetch URL.
func (m *Manager) PurgeCorrupt(url string) {
	if url == "" {
		return
	}
	if m.t2 != nil {
		m.t2.Remove(url)
	}
	if !skipDisk(url) && m.disk != nil {
		m.disk.Delete(url)
	}
}

// ClearDisk wipes the disk cache.
func (m *Manager) ClearDisk() error {
	return m.disk.Clear()
}

// DiskRoot returns the disk cache's directory.
func (m *Manager) DiskRoot() string {
	if m.disk == nil {
		return ""
	}
	return m.disk.Root()
}

// SetDiskCompression toggles zstd for new disk-cache writes.
func (m *Manager) SetDiskCompression(on bool) {
	if m.disk != nil {
		m.disk.SetCompression(on)
	}
}

// SetDiskBudget sets the disk cache's auto-prune byte cap; 0 = unlimited, so no cache is silently deleted.
func (m *Manager) SetDiskBudget(bytes int64) {
	if m.disk != nil {
		m.disk.SetBudget(bytes)
	}
}

// T2Stats snapshots the memory cache's counters.
func (m *Manager) T2Stats() cache.MemoryStats {
	if m.t2 == nil {
		return cache.MemoryStats{}
	}
	return m.t2.Stats()
}

// DiskStats snapshots the disk cache's counters from cached atomics, without a directory walk, so it is safe per frame.
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
	// MountFetches counts assets served from the player's folders.
	MountFetches int64
	// PackQuarantined counts folder files withdrawn after a decode failure.
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
