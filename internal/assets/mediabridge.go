package assets

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/saver"
)

const (
	// mediaDiskWorkers serve the disk cache and the player's folders; mediaDiskKept of them take only Now work, so
	// background folder lookups can't hold up the live message.
	mediaDiskWorkers    = 6
	mediaDiskKept       = 2
	mediaNetworkWorkers = 32
	mediaComputeKept    = 1
	// learnedSaveDelay is how long after a find learned.json is written, so a busy room writes it once.
	learnedSaveDelay = 10 * time.Second
)

// errLearnedNotRecalled refuses a write before the saved file was read, which would replace it with this session alone.
var errLearnedNotRecalled = errors.New("assets: learned.json not read yet")

// errMediaStopped refuses a write after the media system stopped.
var errMediaStopped = errors.New("assets: media system stopped")

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
	// learned keeps learned.json. recalled and foundEarly are the Manager's goroutine's: whether the saved file was
	// read, and whether something was found before it was.
	learned    *saver.Saver
	recalled   bool
	foundEarly bool
	// learnedHosts mirrors builder.LearnedOrigins for other threads.
	learnedHosts atomic.Int64
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
	b.learned = saver.New(m.learnedPath, learnedSaveDelay, b.encodeLearned)
	cfg := media.Config{Candidates: b.builder.Build, AllFormats: media.EveryFormat, Found: b.found,
		Finished: b.finished, Delivered: m.NotifyDelivery, Skip: b.skip, Asked: b.asked, MissingAgain: b.missingAgain,
		Compute: b.compute, ClaimLimit: imageClaimLimit}
	b.remote = media.NewRemoteSource(ctx, bridgeFetcher{m}, media.DefaultMissingTTL, time.Now)
	cfg.Chains = map[media.Place]*media.Chain{media.PlaceServer: media.NewChain(
		media.Entry{Source: bridgeMemory{m.t2, b.pin, m}},
		media.Entry{Source: bridgeDisk{b.disk, m.t2, b.pin, m}, Pool: b.diskP},
		media.Entry{Source: b.folders, Pool: b.diskP, AllFormats: true},
		media.Entry{Source: bridgeRemote{b.remote, b}, Pool: b.network},
	)}
	cfg.Formats = map[media.Kind]media.Format{media.KindImage: bridgeImage{b}, media.KindSound: bridgeSound{b}}
	b.media = media.NewManager(cfg)
	b.scanDisk()
	b.recallLearned()
	return b
}

// found learns the format a key turned up in, and schedules a save.
func (b *mediaBridge) found(k media.Key, f media.File) {
	b.builder.Found(k, f)
	if !b.recalled {
		b.foundEarly = true
	}
	b.learnedHosts.Store(int64(b.builder.LearnedOrigins()))
	b.learned.Changed()
}

// learn is found for a raw walk on a worker: url turned up for base.
// AllowFormats gives origin a format list per asset type, as its extensions.json would, such as a replay archive's
// recorded formats.
func (m *Manager) AllowFormats(origin string, byType map[AssetType]string) {
	allowed := map[media.Class][]string{}
	for t, ext := range byType {
		if c := classOf(t); c != media.ClassExact && !slices.Contains(allowed[c], ext) {
			allowed[c] = append(allowed[c], ext)
		}
	}
	m.bridge.media.Do(func() { m.bridge.builder.Allow(origin, allowed) })
}

// AllowManifest gives origin the formats an extensions.json lists, for a host known to publish none; false when it
// doesn't parse.
func (m *Manager) AllowManifest(origin string, data []byte) bool {
	allowed, err := media.ParseManifest(data)
	if err != nil {
		return false
	}
	m.bridge.media.Do(func() { m.bridge.builder.Allow(origin, allowed) })
	return true
}

// ForgetLearned drops every learned format and remembered file, here and in learned.json.
func (m *Manager) ForgetLearned() {
	b := m.bridge
	b.media.Do(func() {
		b.builder.ForgetLearned()
		b.media.ForgetWinners(func(media.Key, media.File) bool { return true })
		b.learnedHosts.Store(0)
		b.learned.Changed()
	})
}

// LearnedHosts counts the hosts a format was learned for; safe from any thread.
func (m *Manager) LearnedHosts() int {
	return int(m.bridge.learnedHosts.Load())
}

// recallLearned reads learned.json on a disk worker and adds it to what this session knows.
func (b *mediaBridge) recallLearned() {
	path := b.m.learnedPath
	b.media.Run(b.diskP, media.Later, func() any {
		var l media.Learned
		if path != "" {
			if data, err := os.ReadFile(path); err == nil {
				l, _ = media.DecodeLearned(data) // a damaged file is a fresh start
			}
		}
		return l.Memory()
	}, func(v any) {
		media.Recall(b.media, b.builder, b.remote, v.(media.Memory))
		b.recalled = true
		b.learnedHosts.Store(int64(b.builder.LearnedOrigins()))
		if b.foundEarly {
			b.learned.Changed()
		}
	})
}

// encodeLearned snapshots the learned formats and winners on the Manager's goroutine for the saver.
func (b *mediaBridge) encodeLearned() ([]byte, error) {
	var mem media.Memory
	var err error
	ran := b.media.Call(func() {
		if !b.recalled {
			err = errLearnedNotRecalled
			return
		}
		mem = media.Remember(b.media, b.builder, b.remote)
	})
	if !ran {
		return nil, errMediaStopped
	}
	if err != nil {
		return nil, err
	}
	return media.EncodeLearned(mem.Learned())
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
	if t == AssetTypeMusic {
		return media.KindStream
	}
	if t.IsAudio() {
		return media.KindSound
	}
	return media.KindImage
}

// ChainKey is the key a base is asked under; its formats and spellings are the media system's to find.
func ChainKey(base string, t AssetType) media.Key {
	return media.Key{Place: media.PlaceServer, Path: base, Kind: kindOf(t), Class: classOf(t), Tag: uint8(t)}
}

// ExactKey is the key for a complete URL, such as a music track: one file, no formats to try.
func ExactKey(url string, t AssetType) media.Key {
	k := ChainKey(url, t)
	k.Class = media.ClassExact
	return k
}

// RawKey is the key for a complete URL kept as bytes, such as a char.ini, never decoded.
func RawKey(url string) media.Key {
	return media.Key{Place: media.PlaceServer, Path: url, Kind: media.KindRaw, Class: media.ClassExact}
}

// skip is Config.Skip: an uploaded texture is held already, and one that failed to decode lately is left alone.
func (b *mediaBridge) skip(k media.Key) bool {
	if k.Kind == media.KindRaw {
		return false
	}
	if b.m.t1Contains != nil && b.m.t1Contains(k.Path) {
		b.m.t1Hits.Add(1)
		return true
	}
	return b.m.t1Failed != nil && b.m.t1Failed(k.Path)
}

// asked is Config.Asked: a character's first sprite or emote button maps its folder, so everyone who appears is mapped
// and a roster of icons is not.
func (b *mediaBridge) asked(k media.Key) {
	t := AssetType(k.Tag)
	if k.Kind == media.KindRaw || t != AssetTypeCharSprite && t != AssetTypeEmoteButton || !strings.HasPrefix(k.Path, "http") {
		return
	}
	if root := media.CharacterRoot(k.Path); root != "" {
		media.MapCharacter(b.media, b.network, b.remote, b.builder, root)
	}
}

// missingAgain is Config.MissingAgain: asked again inside its missing time costs no request, and the warning still
// fires.
func (b *mediaBridge) missingAgain(a *media.Asset) {
	if k := a.Key(); k.Kind != media.KindRaw {
		b.m.skipExhausted(k.Path, AssetType(k.Tag), a.Quiet())
	}
}

func (b *mediaBridge) isMissing(base string, t AssetType) bool {
	a := b.media.Peek(ChainKey(base, t))
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
		// Counted, never shown as a broken asset: the bytes were never seen, so the next ask tries again.
		b.m.failed.Add(1)
	default:
		// Delivered: from here T1 answers, and an evicted texture has to be able to ask again.
		b.media.ForgetAsset(a)
		if st, ok := a.Result().(media.Stream); ok {
			b.sendStream(k, t, st)
		}
	}
}

// sendStream hands a music track to the audio side; with the hand-off full, the track is closed and dropped.
func (b *mediaBridge) sendStream(k media.Key, t AssetType, st media.Stream) {
	select {
	case b.m.audioCh <- AudioAsset{URL: k.Path, Base: k.Path, Type: t, Stream: st}:
		b.m.NotifyDelivery()
	default:
		st.Close()
	}
}

// FetchManifest reads a server's extensions.json straight from its host, so a change is seen and the answer proves it is alive.
func (m *Manager) FetchManifest(ctx context.Context, origin string) ([]byte, error) {
	file := origin + media.ManifestFile
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
		_ = b.learned.Close() // before the media system stops, as the snapshot is taken on its goroutine
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

var _ media.Downloader = bridgeFetcher{}

// streamer is an HTTP client that hands a body over as it arrives, as *network.Client does.
type streamer interface {
	Open(ctx context.Context, url string) (*network.Body, error)
	ReadRange(ctx context.Context, url string, from, n int64) ([]byte, error)
}

// Open is netFetch for a body read as it arrives; a replay archive or a client that can't stream answers whole.
func (f bridgeFetcher) Open(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	s, ok := f.m.client.(streamer)
	if !ok || f.m.archiveServes(url) || f.m.offline.Load() || strings.HasPrefix(url, LocalScheme) {
		data, err := f.Fetch(ctx, url)
		if err != nil {
			return nil, 0, err
		}
		return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
	}
	body, err := s.Open(ctx, url)
	if err != nil {
		return nil, 0, err
	}
	f.m.netFetches.Add(1)
	return body, body.Size, nil
}

// ReadRange fetches one span when the client can; without it, reads of a track's end wait for its download.
func (f bridgeFetcher) ReadRange(ctx context.Context, url string, from, n int64) ([]byte, error) {
	if s, ok := f.m.client.(streamer); ok && !f.m.offline.Load() {
		return s.ReadRange(ctx, url, from, n)
	}
	return nil, network.ErrNoRanges
}

// PartialFile is where a download arrives.
func (f bridgeFetcher) PartialFile() (*os.File, error) { return f.m.disk.PartialFile() }

// Adopt gives a finished download to the disk cache.
func (f bridgeFetcher) Adopt(url, path string) {
	if skipDisk(url) {
		_ = os.Remove(path)
		return
	}
	f.m.disk.Adopt(url, path)
	b := f.m.bridge
	b.media.Call(func() { b.disk.Index(media.File(url)) })
}

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
	}
	if err == nil && len(data) > 0 { // a track opened as a stream brings no bytes
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
	if err == nil && len(data) > 0 { // a download keeps itself, through Adopt
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

// bridgeImage decodes on the Compute worker that holds the request; an animation's opening frames go to the render
// thread as early pages, and the last page is the result.
type bridgeImage struct{ b *mediaBridge }

var _ media.Measurer = bridgeImage{}

func (f bridgeImage) OnMainThread() bool { return true }

// Measure claims the decoded size, so background decodes wait while the render thread is behind on uploads.
func (f bridgeImage) Measure(data []byte) int64 {
	return decodeClaim(data, f.b.m.prefs.Load().PreferAnimated)
}

func (f bridgeImage) Decode(k media.Key, hit media.Hit, data []byte, page func(any)) (any, error) {
	url, fromPack := f.b.hitOf(hit)
	t := AssetType(k.Tag)
	m := f.b.m
	var last DecodedAsset
	m.decoder.Decode(DecodeRequest{
		URL:            url,
		Data:           data,
		Type:           t,
		PlayAnimations: m.prefs.Load().PreferAnimated,
		OnDone: func(doneURL string, d *Decoded, err error) {
			decoded := DecodedAsset{URL: doneURL, Base: k.Path, Type: t, Asset: d, Err: err, FromPack: fromPack}
			if d != nil && d.Partial {
				page(decoded)
				return
			}
			last = decoded
		},
	})
	return last, nil
}

// bridgeSound hands the bytes to the audio side, which decodes them itself.
type bridgeSound struct{ b *mediaBridge }

func (f bridgeSound) OnMainThread() bool { return false }

func (f bridgeSound) Decode(k media.Key, hit media.Hit, data []byte, _ func(any)) (any, error) {
	url, fromPack := f.b.hitOf(hit)
	f.b.m.audioCh <- AudioAsset{URL: url, Base: k.Path, Type: AssetType(k.Tag), Data: data, FromPack: fromPack}
	f.b.m.NotifyDelivery()
	return nil, nil
}
