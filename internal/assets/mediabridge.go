package assets

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	mediaDiskWorkers    = 4
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
	pack    *bridgePack
	diskP   *media.Pool
	network *media.Pool
	compute *media.Pool
	cancel  context.CancelFunc
	closing sync.Once
}

func newMediaBridge(m *Manager) *mediaBridge {
	ctx, cancel := context.WithCancel(context.Background())
	b := &mediaBridge{m: m, cancel: cancel, builder: media.NewCandidateBuilder(),
		diskP:   media.NewPool("disk", mediaDiskWorkers, 0),
		network: media.NewPool("network", mediaNetworkWorkers, 0),
		compute: media.NewPool("compute", DecodeWorkers(), mediaComputeKept),
		disk:    media.NewDiskSource(m.disk),
		pack:    &bridgePack{m: m},
	}
	cfg := media.Config{Candidates: b.builder.Build, Found: b.builder.Found, Finished: b.finished, Compute: b.compute}
	b.remote = media.NewRemoteSource(ctx, bridgeFetcher{m}, media.DefaultMissingTTL, time.Now)
	cfg.Chains = map[media.Place]*media.Chain{media.PlaceServer: media.NewChain(
		media.Entry{Source: bridgeMemory{m.t2}},
		media.Entry{Source: bridgeDisk{b.disk, m.t2}, Pool: b.diskP},
		media.Entry{Source: b.pack, Pool: b.diskP},
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
	if m != nil && m.bridge != nil {
		m.bridge.scanDisk()
	}
}

// handles reports whether a URL runs on the media system; a local:// URL is already on disk and keeps its own path.
func (b *mediaBridge) handles(url string) bool {
	return b != nil && !strings.HasPrefix(url, LocalScheme)
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

func chainKey(base string, alts []string, t AssetType, quiet, sticky bool) media.Key {
	k := media.Key{Place: media.PlaceServer, Path: base, Kind: kindOf(t), Class: classOf(t), Tag: uint8(t), Quiet: quiet, Sticky: sticky}
	i := 0
	for _, alt := range alts {
		if alt != "" && alt != base && i < len(k.Alts) {
			k.Alts[i] = alt
			i++
		}
	}
	return k
}

func (b *mediaBridge) askChain(base string, alts []string, t AssetType, p network.Priority, quiet, sticky bool) {
	if b.m.t1Contains != nil && b.m.t1Contains(base) {
		b.m.t1Hits.Add(1)
		return
	}
	b.ask(chainKey(base, alts, t, quiet, sticky), p)
}

func (b *mediaBridge) askExact(url string, t AssetType, p network.Priority) {
	if b.m.t1Contains != nil && b.m.t1Contains(url) {
		b.m.t1Hits.Add(1)
		return
	}
	k := chainKey(url, nil, t, false, false)
	k.Class = media.ClassExact
	b.ask(k, p)
}

func (b *mediaBridge) askRaw(url string, p network.Priority) {
	b.media.Get(media.Key{Place: media.PlaceServer, Path: url, Kind: media.KindRaw, Class: media.ClassExact, Quiet: true}, urgencyOf(p))
}

func (b *mediaBridge) ask(k media.Key, p network.Priority) {
	a := b.media.Get(k, urgencyOf(p))
	switch a.State() {
	case media.Missing:
		// Asked again inside the file's missing time: no request, and the warning still fires.
		b.m.skipExhausted(k.Path, AssetType(k.Tag), k.Quiet)
	case media.Refused:
		b.m.refused.Add(1)
	}
}

func (b *mediaBridge) isMissing(base string, t AssetType) bool {
	a := b.media.Peek(chainKey(base, nil, t, false, false))
	return a != nil && a.State() == media.Missing
}

func (b *mediaBridge) forgetMissing(prefix string) {
	if b == nil {
		return
	}
	b.media.ForgetIf(func(a *media.Asset) bool {
		return a.State() == media.Missing && strings.HasPrefix(a.Key().Path, prefix)
	})
	b.media.Do(func() { b.remote.ForgetMissing(prefix) })
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
		if k.Quiet {
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
	if m.bridge == nil {
		return m.FetchRaw(ctx, origin+ManifestFileName)
	}
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
// extensions.json and lets files already missing be searched again. The old pipeline ignores them.
func (m *Manager) SetAssetHosts(origin string, hosts []string) {
	if m == nil || m.bridge == nil || origin == "" {
		return
	}
	b := m.bridge
	clean := slices.DeleteFunc(config.CleanAssetHosts(hosts), func(h string) bool { return h == origin })
	b.media.Do(func() {
		if !b.builder.SetHosts(origin, clean) {
			return
		}
		for _, host := range clean {
			media.Join(b.media, b.network, b.remote, bridgeFetcher{m}, b.builder, host, nil)
		}
		b.media.ForgetIf(func(a *media.Asset) bool {
			return a.State() == media.Missing && strings.HasPrefix(a.Key().Path, origin)
		})
		b.remote.ForgetMissing(origin)
	})
}

// CancelRoomWork withdraws everything not started that isn't sticky, when leaving a server.
func (m *Manager) CancelRoomWork() {
	if m == nil || m.bridge == nil {
		return
	}
	m.bridge.media.WithdrawIf(func(k media.Key) bool { return !k.Sticky })
}

// CloseMedia stops the media system and its workers.
func (m *Manager) CloseMedia() {
	if m == nil || m.bridge == nil {
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

// bridgeMemory is T2 as a source, shared with the raw fetch paths.
type bridgeMemory struct {
	t2 *cache.ByteBudgetLRU[string, []byte]
}

func (s bridgeMemory) Has(f media.File) media.Answer {
	if s.t2.Contains(string(f)) {
		return media.Yes
	}
	return media.No
}

func (s bridgeMemory) Data(f media.File) ([]byte, error) {
	if d, ok := s.t2.Get(string(f)); ok {
		return d, nil
	}
	return nil, media.ErrAbsent
}

func (s bridgeMemory) Size() media.Contents {
	return media.Contents{Name: "memory", Entries: s.t2.Len()}
}

// bridgeDisk promotes what it reads into T2.
type bridgeDisk struct {
	*media.DiskSource
	t2 *cache.ByteBudgetLRU[string, []byte]
}

func (s bridgeDisk) Observe(f media.File, data []byte, err error) {
	s.DiskSource.Observe(f, data, err)
	if err == nil {
		s.t2.Add(string(f), data, int64(len(data)))
	}
}

// bridgeRemote keeps what it fetches, and records nothing offline, where every answer is a pretend 404.
type bridgeRemote struct {
	*media.RemoteSource
	b *mediaBridge
}

func (s bridgeRemote) Observe(f media.File, data []byte, err error) {
	if s.b.m.offline.Load() {
		return
	}
	s.RemoteSource.Observe(f, data, err)
	if err == nil {
		s.b.m.t2.Add(string(f), data, int64(len(data)))
		s.b.disk.Keep(f, data)
	}
}

// bridgePack is the local packs as a source: a pack can hold any format, whatever the server's list says.
type bridgePack struct {
	m     *Manager
	local sync.Map // media.File to the pack's own URL, for quarantine
}

func (s *bridgePack) stem(f media.File) (string, bool) {
	dot := strings.LastIndexByte(string(f), '.')
	slash := strings.LastIndexByte(string(f), '/')
	if dot <= slash {
		return string(f), true
	}
	return string(f[:dot]), true
}

func (s *bridgePack) Has(f media.File) media.Answer {
	l := s.m.activeMountLayer()
	if l == nil {
		return media.No
	}
	idx := l.Index()
	if idx == nil || !idx.acquire() {
		return media.No
	}
	defer idx.release()
	if rel, ok := l.RelOf(string(f)); ok && !mountLayerExcluded(rel) {
		if _, hit := idx.LookupExact(foldRel(rel)); hit {
			return media.Yes
		}
	}
	base, _ := s.stem(f)
	rel, ok := l.RelOf(base)
	if !ok || mountLayerExcluded(rel) {
		return media.No
	}
	if _, have := idx.LookupStem(foldRel(rel)); have {
		return media.Yes
	}
	return media.No
}

func (s *bridgePack) Data(f media.File) ([]byte, error) {
	l := s.m.activeMountLayer()
	if l == nil {
		return nil, media.ErrAbsent
	}
	if b, ok := s.m.packExactBytes(l, string(f)); ok {
		s.local.Store(f, string(f))
		s.m.mountFetches.Add(1)
		return b, nil
	}
	base, _ := s.stem(f)
	// Sprites, then sounds: the source doesn't know which a file is, and a stem holds one or the other.
	for _, t := range []AssetType{AssetTypeCharSprite, AssetTypeSFX} {
		if key, b, ok := s.m.packBytes(l, base, t); ok {
			s.local.Store(f, l.LocalURLFor(key))
			s.m.mountFetches.Add(1)
			return b, nil
		}
	}
	return nil, media.ErrAbsent
}

func (s *bridgePack) Size() media.Contents { return media.Contents{Name: "packs"} }

// hitOf returns the URL and pack flag to deliver a hit under.
func (b *mediaBridge) hitOf(hit media.Hit) (string, bool) {
	if hit.Source == media.Source(b.pack) {
		if v, ok := b.pack.local.Load(hit.File); ok {
			return v.(string), true
		}
		return string(hit.File), true
	}
	return string(hit.File), false
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
