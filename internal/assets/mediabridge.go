package assets

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// mediaDiskWorkers serve the disk cache and the player's folders; mediaDiskKept of them take only Now work, so
	// background folder lookups can't hold up the live message.
	mediaDiskWorkers    = 6
	mediaDiskKept       = 2
	mediaNetworkWorkers = 32
	mediaComputeKept    = 1
)

// mediaBridge runs the Manager's requests on internal/media, so every caller moves at once; it goes when callers use media directly.
type mediaBridge struct {
	m       *Manager
	media   *media.Manager
	builder *media.CandidateBuilder
	remote  *media.RemoteSource
	disk    *media.DiskSource
	folders *bridgeFolders
	diskP   *media.Pool
	network *media.Pool
	compute *media.Pool
	cancel  context.CancelFunc
	closing sync.Once
	// hostLists maps each extra host in use to its media.ManifestState; written on the Manager's goroutine.
	// hostListsGen counts its changes, so a reader can skip it while nothing changed.
	hostLists    sync.Map
	hostListsGen atomic.Uint64
	pin          *sceneryPin
}

func newMediaBridge(m *Manager) *mediaBridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &mediaBridge{m: m, cancel: cancel, builder: media.NewCandidateBuilder(),
		diskP:   media.NewPool("disk", mediaDiskWorkers, mediaDiskKept),
		network: media.NewPool("network", mediaNetworkWorkers, 0),
		compute: media.NewPool("compute", DecodeWorkers(), mediaComputeKept),
		disk:    media.NewDiskSource(m.disk),
		pin:     newSceneryPin(),
	}
	b.folders = newBridgeFolders(m)
	cfg := media.Config{Candidates: b.builder.Build, Found: b.builder.Found, Finished: b.finished, Compute: b.compute}
	b.remote = media.NewRemoteSource(ctx, bridgeFetcher{m}, media.DefaultMissingTTL, time.Now)
	cfg.Chains = map[media.Place]*media.Chain{media.PlaceServer: media.NewChain(
		media.Entry{Source: bridgeMemory{m.t2, b.pin, m}},
		media.Entry{Source: bridgeDisk{b.disk, m.t2, b.pin, m}, Pool: b.diskP},
		media.Entry{Source: b.folders, Pool: b.diskP},
		media.Entry{Source: bridgeRemote{b.remote, b}, Pool: b.network},
	)}
	cfg.Formats = map[media.Kind]media.Format{media.KindImage: bridgeImage{b}, media.KindSound: bridgeSound{b}}
	b.media = media.NewManager(cfg)
	b.scanDisk()
	return b
}

// scanDisk fills the disk index; until it lands, a cached file is fetched again, which is only slower.
func (b *mediaBridge) scanDisk() {
	b.media.Run(b.diskP, media.Later, func() any {
		keys, _ := media.ScanDiskIndex(b.m.disk.Root())
		return keys
	}, func(v any) { b.disk.Adopt(v.([]string)) })
}

// RescanDisk rebuilds the disk index, after something other than the media system has written the cache.
func (m *Manager) RescanDisk() {
	if m != nil {
		m.bridge.scanDisk()
	}
}

func urgencyOf(p network.Priority) media.Urgency {
	if p == network.PriorityHigh {
		return media.Now
	}
	return media.Later
}

func classOf(t AssetType) media.Class {
	switch t {
	case AssetTypeCharIcon:
		return media.ClassCharIcon
	case AssetTypeCharSprite, AssetTypeShoutBubble:
		return media.ClassEmote
	case AssetTypeEmoteButton:
		return media.ClassEmotions
	case AssetTypeBackground, AssetTypeDeskOverlay:
		return media.ClassBackground
	case AssetTypeSFX:
		return media.ClassSFX
	case AssetTypeBlip:
		return media.ClassBlips
	case AssetTypeMusic:
		return media.ClassExact
	default:
		return media.ClassMisc
	}
}

func kindOf(t AssetType) media.Kind {
	if t.IsAudio() {
		return media.KindSound
	}
	return media.KindImage
}

func chainKey(base string, t AssetType) media.Key {
	return media.Key{Place: media.PlaceServer, Path: base, Kind: kindOf(t), Class: classOf(t), Tag: uint8(t)}
}

func flagsOf(quiet, sticky bool) media.Flags {
	var f media.Flags
	if quiet {
		f |= media.Quiet
	}
	if sticky {
		f |= media.Sticky
	}
	return f
}

func (b *mediaBridge) askChain(base string, alts []string, t AssetType, p network.Priority, sticky bool) {
	if b.m.t1Contains != nil && b.m.t1Contains(base) {
		b.m.t1Hits.Add(1)
		return
	}
	b.ask(chainKey(base, t), flagsOf(false, sticky), p, alts...)
}

func (b *mediaBridge) askExact(url string, t AssetType, p network.Priority) {
	if b.m.t1Contains != nil && b.m.t1Contains(url) {
		b.m.t1Hits.Add(1)
		return
	}
	k := chainKey(url, t)
	k.Class = media.ClassExact
	b.ask(k, 0, p)
}

func (b *mediaBridge) askRaw(url string, p network.Priority) {
	b.media.Get(media.Key{Place: media.PlaceServer, Path: url, Kind: media.KindRaw, Class: media.ClassExact}, urgencyOf(p), media.Quiet)
}

func (b *mediaBridge) ask(k media.Key, f media.Flags, p network.Priority, alts ...string) {
	a := b.media.Get(k, urgencyOf(p), f, alts...)
	switch a.State() {
	case media.Missing:
		// Asked again inside the file's missing time: no request, and the warning still fires.
		b.m.skipExhausted(k.Path, AssetType(k.Tag), f&media.Quiet != 0)
	case media.Refused:
		b.m.refused.Add(1)
	}
}

func (b *mediaBridge) isMissing(base string, t AssetType) bool {
	a := b.media.Peek(chainKey(base, t))
	return a != nil && a.State() == media.Missing
}

// forgetMissing asks again for the missing assets under prefix, including the remote's 404 memory, and reports how
// many it forgot.
func (b *mediaBridge) forgetMissing(prefix string) int {
	n := 0
	b.media.ForgetIf(func(a *media.Asset) bool {
		match := a.State() == media.Missing && strings.HasPrefix(a.Key().Path, prefix)
		if match {
			n++
		}
		return match
	})
	b.media.Do(func() { b.remote.ForgetMissing(prefix) })
	return n
}

// forgetMissingAssets asks again for every missing asset, keeping the remote's 404 memory (hard rule 6), for when
// the player's folders may now hold them.
func (b *mediaBridge) forgetMissingAssets() {
	b.media.ForgetIf(func(a *media.Asset) bool { return a.State() == media.Missing })
}

// finished ends a request on the Manager's goroutine, so anything that can block goes to a worker.
func (b *mediaBridge) finished(a *media.Asset) {
	k := a.Key()
	t := AssetType(k.Tag)
	switch a.State() {
	case media.Missing:
		if b.m.offline.Load() {
			b.media.ForgetAsset(a) // offline says nothing about the file, so it isn't remembered as missing
		}
		if k.Kind == media.KindRaw {
			return
		}
		if a.Quiet() {
			b.m.missing.Add(1)
			return
		}
		b.m.reportMissing(k.Path, t, nil)
	case media.Failed:
		if k.Kind == media.KindRaw {
			return
		}
		err := a.Err()
		if t == AssetTypeMusic {
			b.m.reportMusicFailure(k.Path, err)
		}
		// Tagged transient so it never enters the decode negative cache.
		b.media.Run(b.compute, media.Later, func() any {
			b.m.decodedCh <- DecodedAsset{URL: k.Path, Base: k.Path, Type: t, Err: err, Transient: true}
			b.m.NotifyDelivery()
			return nil
		}, func(any) {})
	default:
		// Delivered: from here T1 answers, and an evicted texture has to be able to ask again.
		b.media.ForgetAsset(a)
	}
}

// FetchManifest reads a server's extensions.json straight from its host, so a change is seen and the answer proves it is alive.
func (m *Manager) FetchManifest(ctx context.Context, origin string) ([]byte, error) {
	file := origin + ManifestFileName
	data, err := m.netFetch(ctx, file)
	m.bridge.noteManifest(origin, file, data, err)
	return data, err
}

func (b *mediaBridge) noteManifest(origin, file string, data []byte, err error) {
	answered := err == nil || errors.Is(err, network.ErrAssetNotFound) || errors.Is(err, network.ErrUnexpectedStatus)
	var allowed map[media.Class][]string
	if err == nil {
		allowed, _ = media.ParseManifest(data)
	}
	b.media.Do(func() {
		if answered {
			b.remote.Confirm(HostOf(file))
		}
		if allowed != nil {
			b.builder.Allow(origin, allowed)
		}
	})
}

// SetAssetHosts sets the hosts asked, in order, after origin misses. A changed list reads each host's
// extensions.json and lets files already missing be searched again.
func (m *Manager) SetAssetHosts(origin string, hosts []string) {
	if m == nil || origin == "" {
		return
	}
	b := m.bridge
	clean := slices.DeleteFunc(config.CleanAssetHosts(hosts), func(h string) bool { return h == origin })
	b.media.Do(func() {
		if !b.builder.SetHosts(origin, clean) {
			return
		}
		// Only hosts still in some list keep a state, so the table stays as small as the lists.
		b.hostLists.Range(func(k, _ any) bool {
			if !b.builder.Uses(k.(string)) {
				b.hostLists.Delete(k)
			}
			return true
		})
		for _, host := range clean {
			b.hostLists.Store(host, media.ManifestUnchecked)
			media.Join(b.media, b.network, b.remote, bridgeFetcher{m}, b.builder, host, func(s media.ManifestState) {
				if b.builder.Uses(host) {
					b.hostLists.Store(host, s)
					b.hostListsGen.Add(1)
				}
			})
		}
		b.media.ForgetIf(func(a *media.Asset) bool {
			return a.State() == media.Missing && strings.HasPrefix(a.Key().Path, origin)
		})
		b.media.ForgetWinners(func(k media.Key, _ media.File) bool { return strings.HasPrefix(k.Path, origin) })
		b.remote.ForgetMissing(origin)
	})
}

// HostListsChanged counts changes to the extra hosts' format-list states.
func (m *Manager) HostListsChanged() uint64 {
	if m == nil {
		return 0
	}
	return m.bridge.hostListsGen.Load()
}

// HostListState says what reading an extra host's extensions.json found; safe from any thread.
func (m *Manager) HostListState(host string) media.ManifestState {
	if m == nil {
		return media.ManifestUnchecked
	}
	if v, ok := m.bridge.hostLists.Load(host); ok {
		return v.(media.ManifestState)
	}
	return media.ManifestUnchecked
}

// CancelRoomWork withdraws everything not started that isn't sticky, when leaving a server.
func (m *Manager) CancelRoomWork() {
	if m == nil {
		return
	}
	m.bridge.media.WithdrawIf(func(a *media.Asset) bool { return !a.Sticky() })
}

// CloseMedia stops the media system and its workers.
func (m *Manager) CloseMedia() {
	if m == nil {
		return
	}
	b := m.bridge
	b.closing.Do(func() {
		b.cancel()
		b.media.Close()
		// A worker blocked delivering to a channel nobody drains at exit must not hold the process open.
		go func() {
			b.diskP.Close()
			b.network.Close()
			b.compute.Close()
		}()
	})
}

type bridgeFetcher struct{ m *Manager }

func (f bridgeFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	data, err := f.m.netFetch(ctx, url)
	if err == nil {
		f.m.netFetches.Add(1)
	}
	return data, err
}

// bridgeMemory is T2 as a source, shared with the raw fetch paths, with the pinned room scenery in front.
type bridgeMemory struct {
	t2  *cache.ByteBudgetLRU[string, []byte]
	pin *sceneryPin
	m   *Manager
}

func (s bridgeMemory) Has(f media.File) media.Answer {
	if s.pin.has(string(f)) || s.t2.Contains(string(f)) {
		return media.Yes
	}
	return media.No
}

func (s bridgeMemory) Data(f media.File) ([]byte, error) {
	if d, ok := s.pin.get(string(f)); ok {
		return d, nil
	}
	if d, ok := s.t2.Get(string(f)); ok {
		s.m.t2Hits.Add(1)
		s.pin.keep(string(f), d)
		return d, nil
	}
	return nil, media.ErrAbsent
}

func (s bridgeMemory) Size() media.Contents {
	return media.Contents{Name: "memory", Entries: s.t2.Len()}
}

// bridgeDisk promotes what it reads into T2; local:// files are already on disk, so it never holds them.
type bridgeDisk struct {
	*media.DiskSource
	t2  *cache.ByteBudgetLRU[string, []byte]
	pin *sceneryPin
	m   *Manager
}

func (s bridgeDisk) Has(f media.File) media.Answer {
	if skipDisk(string(f)) {
		return media.No
	}
	return s.DiskSource.Has(f)
}

func (s bridgeDisk) Observe(f media.File, data []byte, err error) {
	s.DiskSource.Observe(f, data, err)
	if err == nil {
		s.m.diskHits.Add(1)
		s.t2.Add(string(f), data, int64(len(data)))
		s.pin.keep(string(f), data)
	}
}

// bridgeRemote keeps what it fetches, and records nothing offline, where every answer is a pretend 404. A local://
// file is never on the web; only a replay archive's own origin is asked here, as its folder answers through netFetch.
type bridgeRemote struct {
	*media.RemoteSource
	b *mediaBridge
}

func (s bridgeRemote) Has(f media.File) media.Answer {
	if skipDisk(string(f)) && !s.b.m.archiveServes(string(f)) {
		return media.No
	}
	return s.RemoteSource.Has(f)
}

func (s bridgeRemote) Observe(f media.File, data []byte, err error) {
	if s.b.m.offline.Load() {
		return
	}
	s.RemoteSource.Observe(f, data, err)
	if err == nil {
		s.b.m.t2.Add(string(f), data, int64(len(data)))
		s.b.pin.keep(string(f), data)
		if !skipDisk(string(f)) {
			s.b.disk.Keep(f, data)
		}
	}
}

// hitOf returns the URL and folder flag to deliver a hit under.
func (b *mediaBridge) hitOf(hit media.Hit) (string, bool) {
	return string(hit.File), hit.Source == media.Source(b.folders)
}

// bridgeImage decodes through DecoderPool, holding a Compute worker until the last frame, so Now work keeps its place.
type bridgeImage struct{ b *mediaBridge }

func (f bridgeImage) OnMainThread() bool { return false }

func (f bridgeImage) Decode(k media.Key, hit media.Hit, data []byte) (any, error) {
	url, fromPack := f.b.hitOf(hit)
	t := AssetType(k.Tag)
	m := f.b.m
	finished := make(chan struct{})
	accepted := m.decoder.Submit(DecodeRequest{
		URL:            url,
		Data:           data,
		Type:           t,
		PlayAnimations: m.prefs.Load().PreferAnimated,
		OnDone: func(doneURL string, d *Decoded, err error) {
			m.onDecoded(doneURL, k.Path, t, fromPack, d, err)
			if d == nil || !d.Partial {
				close(finished)
			}
		},
	})
	if accepted {
		<-finished
	}
	return nil, nil
}

// bridgeSound hands the bytes to the audio side, which decodes them itself.
type bridgeSound struct{ b *mediaBridge }

func (f bridgeSound) OnMainThread() bool { return false }

func (f bridgeSound) Decode(k media.Key, hit media.Hit, data []byte) (any, error) {
	url, fromPack := f.b.hitOf(hit)
	f.b.m.audioCh <- AudioAsset{URL: url, Base: k.Path, Type: AssetType(k.Tag), Data: data, FromPack: fromPack}
	f.b.m.NotifyDelivery()
	return nil, nil
}
