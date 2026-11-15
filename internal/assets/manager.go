package assets

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
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
	// Stream is a music track, opened where it lies or still downloading, in place of Data; the receiver closes it.
	Stream media.Stream
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
	bridge  *mediaBridge
	prefs   *config.AssetPreferences
	t2      *cache.ByteBudgetLRU[string, []byte]
	disk    *cache.DiskCache
	client  Fetcher
	decoder *Decoder
	// learnedPath is where the media system's learned formats and winners are kept between sessions.
	learnedPath string

	// t1Contains reports whether a texture is uploaded; pass the upload's key: base if probed, full URL if exact.
	t1Contains func(url string) bool
	// t1Failed reports a recent decode failure of base, so one bad asset cannot storm the network.
	t1Failed func(base string) bool

	audioCh     chan AudioAsset
	warningCh   chan Warning
	musicFailCh chan MusicFailure

	// deliveryNotify wakes the event-driven render loop; on workers, so cheap, non-blocking and SDL-free.
	deliveryNotify atomic.Pointer[func()]

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
	// failed counts requests that ended in a fetch failure rather than a missing file.
	failed atomic.Int64
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

// ManagerDeps wires a Manager; every field is required except T1Contains and T1Failed.
type ManagerDeps struct {
	Prefs *config.AssetPreferences
	T2    *cache.ByteBudgetLRU[string, []byte]
	Disk  *cache.DiskCache
	// Source is the network client, or a fake in tests.
	Source     Fetcher
	Decoder    *Decoder
	T1Contains func(url string) bool
	T1Failed   func(base string) bool
	// LearnedPath is the learned.json the media system's learned formats and winners are kept in; "" keeps none.
	LearnedPath string
}

// NewManager builds the Manager and starts its media system; CloseMedia stops it.
func NewManager(deps ManagerDeps) *Manager {
	m := &Manager{
		prefs:       deps.Prefs,
		t2:          deps.T2,
		disk:        deps.Disk,
		client:      deps.Source,
		decoder:     deps.Decoder,
		t1Contains:  deps.T1Contains,
		t1Failed:    deps.T1Failed,
		learnedPath: deps.LearnedPath,
		audioCh:     make(chan AudioAsset, audioChanCap),
		warningCh:   make(chan Warning, warningChanCap),
		musicFailCh: make(chan MusicFailure, musicFailChanCap),
	}
	m.bridge = newMediaBridge(m)
	return m
}

// WorkStats shows what the decoders and handoffs hold, for diagnostics.
type WorkStats struct {
	Refused               int64
	DecodeWorkers         int
	AudioQueued, AudioCap int
	Decoding              []network.WorkItem
}

// WorkStats snapshots the decoders and handoffs; it allocates, so call it only while the debug panel shows it.
func (m *Manager) WorkStats() WorkStats {
	return WorkStats{
		Refused:       m.bridge.media.Refused(),
		DecodeWorkers: DecodeWorkers(),
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

// Media is the media system callers ask directly, with keys from ChainKey and ExactKey.
func (m *Manager) Media() *media.Manager {
	if m == nil {
		return nil
	}
	return m.bridge.media
}

// Delivery is one decoded page for the render thread, and the request it belongs to.
type Delivery struct {
	DecodedAsset
	asset *media.Asset
	final bool
}

// TakeDeliveries hands the render thread every decoded page waiting, in order; it calls Done on each once through.
func (m *Manager) TakeDeliveries(use func(Delivery)) {
	m.bridge.media.TakeResults(func(a *media.Asset, v any, final bool) {
		d, ok := v.(DecodedAsset)
		if !ok {
			// Only images need the render thread; anything else is finished here so its claim is freed.
			m.Done(Delivery{asset: a, final: final})
			return
		}
		use(Delivery{DecodedAsset: d, asset: a, final: final})
	})
}

// Done tells the media system the render thread is through with d: the final page finishes its request and forgets it,
// so an evicted texture can ask again.
func (m *Manager) Done(d Delivery) {
	if !d.final {
		return
	}
	m.bridge.media.Finish(d.asset, nil)
	m.bridge.media.ForgetAsset(d.asset)
}

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
	if data, ok := m.cachedRaw(url); ok {
		return data, false, nil
	}
	if data, ok := m.bridge.folders.read(url); ok {
		return data, true, nil // counted by the folder source
	}
	data, err = m.remoteRaw(ctx, url)
	return data, false, err
}

// cachedRaw reads a URL from memory or the disk cache.
func (m *Manager) cachedRaw(url string) ([]byte, bool) {
	if data, ok := m.t2.Get(url); ok {
		m.t2Hits.Add(1)
		return data, true
	}
	if skipDisk(url) {
		return nil, false
	}
	data, ok := m.disk.Get(url)
	if ok {
		m.diskHits.Add(1)
		m.t2.Add(url, data, int64(len(data)))
	}
	return data, ok
}

// remoteRaw fetches a URL from the web and keeps it in memory and the disk cache.
func (m *Manager) remoteRaw(ctx context.Context, url string) ([]byte, error) {
	data, err := m.netFetch(ctx, url)
	if err != nil {
		return nil, err
	}
	m.netFetches.Add(1)
	m.t2.Add(url, data, int64(len(data)))
	if !skipDisk(url) {
		m.disk.Put(url, data)
	}
	return data, nil
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
	return m.resolveIn(base, t, m.bridge.candidates(base, t))
}

// ResolveRawFull is ResolveRawLayered for report and export passes, which also ask the formats the host's list leaves
// out.
func (m *Manager) ResolveRawFull(base string, t AssetType) (string, []byte, bool) {
	if base == "" || !t.Valid() {
		return "", nil, false
	}
	urls := m.bridge.candidates(base, t)
	for _, f := range media.EveryFormat(media.Key{Class: classOf(t)}, base) {
		if !slices.Contains(urls, string(f)) {
			urls = append(urls, string(f))
		}
	}
	url, data, _, ok := m.resolveIn(base, t, urls)
	return url, data, ok
}

// resolveIn finds one name in the live chain's order: memory and the disk cache, then the player's folders in every
// format, then the web. Server hits teach the host's format; a folder hit teaches nothing, as the files are the player's.
func (m *Manager) resolveIn(base string, t AssetType, urls []string) (url string, data []byte, fromPack, ok bool) {
	for _, u := range urls {
		if data, ok := m.cachedRaw(u); ok {
			m.bridge.learn(base, t, u)
			return u, data, false, true
		}
	}
	for _, f := range media.EveryFormat(media.Key{Class: classOf(t)}, base) {
		if data, ok := m.bridge.folders.read(string(f)); ok {
			return string(f), data, true, true
		}
	}
	for _, u := range urls {
		if data, err := m.remoteRaw(context.Background(), u); err == nil && len(data) > 0 {
			m.bridge.learn(base, t, u)
			return u, data, false, true
		}
	}
	return "", nil, false, false
}

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
	// Failed counts requests that ended in a fetch failure rather than a missing file.
	Failed int64
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
		Failed:          m.failed.Load(),
	}
}
