package assets

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/media"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	bridgeTestTimeout = 10 * time.Second
	bridgeTestOrigin  = "http://host.test/base/"
	bridgeHungOrigin  = "http://hung.test/base/"
	bridgeHungAsks    = 64
	bridgeTestWorkers = 2
)

// bridgeFakeHost plays hosts: it serves a table, answers 404 otherwise, and hangs one chosen prefix.
type bridgeFakeHost struct {
	mu     sync.Mutex
	files  map[string][]byte
	asked  []string
	hung   string
	hold   chan struct{}
	broken error
}

func (h *bridgeFakeHost) Fetch(ctx context.Context, url string) ([]byte, error) {
	h.mu.Lock()
	h.asked = append(h.asked, url)
	hung, hold, broken := h.hung, h.hold, h.broken
	data, ok := h.files[url]
	h.mu.Unlock()
	if hung != "" && len(url) >= len(hung) && url[:len(hung)] == hung {
		select {
		case <-hold:
		case <-ctx.Done():
		}
		return nil, errors.New("host hung")
	}
	if broken != nil {
		return nil, broken
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", network.ErrAssetNotFound, url)
	}
	return data, nil
}

func (h *bridgeFakeHost) askedFor() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.asked)
}

func newBridgeRig(t *testing.T, host *bridgeFakeHost) *testRig {
	t.Helper()
	prefs := newTestPrefs(t)
	resolver := NewResolver(prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(bridgeTestWorkers)
	t.Cleanup(pool.Close)
	decoder := NewDecoderPool(bridgeTestWorkers)
	t.Cleanup(decoder.Close)
	m := NewManager(ManagerDeps{Resolver: resolver, Prefs: prefs, T2: t2, Disk: disk, Source: host, Pool: pool, Decoder: decoder, MediaSystem: true})
	t.Cleanup(m.CloseMedia)
	return &testRig{prefs: prefs, resolver: resolver, manager: m, pool: pool, decoder: decoder, disk: disk, t2: t2}
}

// nextDecoded waits for the final page of base, releasing partial ones as the upload pump does.
func nextDecoded(t *testing.T, m *Manager, base string) DecodedAsset {
	t.Helper()
	deadline := time.After(bridgeTestTimeout)
	for {
		select {
		case d := <-m.Decoded():
			if d.Base != base {
				continue
			}
			if d.Asset != nil && d.Asset.Partial {
				d.Asset.Release()
				continue
			}
			return d
		case <-deadline:
			t.Fatalf("%s was never delivered", base)
		}
	}
}

func TestBridgeDeliversAndLearns(t *testing.T) {
	webp := fixture(t, "sprite_256x192.webp")
	host := &bridgeFakeHost{files: map[string][]byte{bridgeTestOrigin + "c/(a)e.webp": webp}}
	r := newBridgeRig(t, host)
	base := bridgeTestOrigin + "c/(a)e"
	r.manager.PrefetchChain(base, []string{bridgeTestOrigin + "c/e"}, AssetTypeCharSprite, network.PriorityHigh)
	d := nextDecoded(t, r.manager, base)
	if d.Err != nil || d.Asset == nil || d.URL != base+".webp" {
		t.Fatalf("url %q err %v", d.URL, d.Err)
	}
}

func TestBridgeAsksAgainAfterDelivery(t *testing.T) {
	webp := fixture(t, "sprite_256x192.webp")
	host := &bridgeFakeHost{files: map[string][]byte{bridgeTestOrigin + "e.webp": webp}}
	r := newBridgeRig(t, host)
	base := bridgeTestOrigin + "e"
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	nextDecoded(t, r.manager, base)
	deadline := time.Now().Add(bridgeTestTimeout)
	// An evicted texture asks again and comes back from memory, with no second request.
	var d DecodedAsset
	got := make(chan DecodedAsset, 1)
	go func() { got <- nextDecoded(t, r.manager, base) }()
	for time.Now().Before(deadline) {
		r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
		select {
		case d = <-got:
			if d.Err != nil {
				t.Fatal(d.Err)
			}
			if asked := host.askedFor(); len(asked) != 1 {
				t.Fatalf("asked %v", asked)
			}
			return
		case <-time.After(time.Millisecond):
		}
	}
	t.Fatal("no second delivery")
}

func TestBridgeMissingWarnsAndDoesNotAskAgain(t *testing.T) {
	host := &bridgeFakeHost{files: map[string][]byte{}}
	r := newBridgeRig(t, host)
	base := bridgeTestOrigin + "none"
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	select {
	case w := <-r.manager.Warnings():
		if w.Base != base {
			t.Fatalf("warning for %q", w.Base)
		}
	case <-time.After(bridgeTestTimeout):
		t.Fatal("no missing warning")
	}
	if !r.manager.IsConclusiveMiss(base, AssetTypeCharSprite) {
		t.Fatal("not remembered as missing")
	}
	asked := len(host.askedFor())
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	select {
	case <-r.manager.Warnings():
	case <-time.After(bridgeTestTimeout):
		t.Fatal("a repeat ask must still warn")
	}
	if got := len(host.askedFor()); got != asked {
		t.Fatalf("a repeat ask cost %d requests", got-asked)
	}
	r.manager.ForgetConclusiveMissesUnder(bridgeTestOrigin)
	if r.manager.IsConclusiveMiss(base, AssetTypeCharSprite) {
		t.Fatal("forgetting left the miss")
	}
}

func TestBridgeTransportFailureIsTransient(t *testing.T) {
	host := &bridgeFakeHost{files: map[string][]byte{}, broken: errors.New("refused")}
	r := newBridgeRig(t, host)
	base := bridgeTestOrigin + "e"
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	d := nextDecoded(t, r.manager, base)
	if d.Err == nil || !d.Transient {
		t.Fatalf("err %v transient %v", d.Err, d.Transient)
	}
}

// The failure the old pipeline showed: work on a healthy host must not wait behind a hung one.
func TestBridgeHungHostDoesNotBlockOtherWork(t *testing.T) {
	webp := fixture(t, "sprite_256x192.webp")
	host := &bridgeFakeHost{files: map[string][]byte{bridgeTestOrigin + "e.webp": webp}, hung: bridgeHungOrigin, hold: make(chan struct{})}
	t.Cleanup(func() { close(host.hold) })
	r := newBridgeRig(t, host)
	for i := range bridgeHungAsks {
		r.manager.Prefetch(fmt.Sprintf("%sc%d", bridgeHungOrigin, i), AssetTypeCharSprite, network.PriorityHigh)
	}
	base := bridgeTestOrigin + "e"
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	if d := nextDecoded(t, r.manager, base); d.Err != nil {
		t.Fatal(d.Err)
	}
}

func TestBridgeRawWarmsMemorySilently(t *testing.T) {
	host := &bridgeFakeHost{files: map[string][]byte{bridgeTestOrigin + "char.ini": []byte("[Options]")}}
	r := newBridgeRig(t, host)
	url := bridgeTestOrigin + "char.ini"
	r.manager.PrefetchRaw(url, network.PriorityLow)
	deadline := time.Now().Add(bridgeTestTimeout)
	for !r.t2.Contains(url) {
		if time.Now().After(deadline) {
			t.Fatal("raw file never reached memory")
		}
		time.Sleep(time.Millisecond)
	}
	r.manager.PrefetchRaw(bridgeTestOrigin+"none.ini", network.PriorityLow)
	select {
	case w := <-r.manager.Warnings():
		t.Fatalf("raw miss warned: %v", w)
	case <-time.After(bridgeSettle):
	}
}

const bridgeSettle = 50 * time.Millisecond

func TestBridgeManifestSetsOrderAndIsFetchedFresh(t *testing.T) {
	webp := fixture(t, "sprite_256x192.webp")
	host := &bridgeFakeHost{files: map[string][]byte{
		bridgeTestOrigin + ManifestFileName: []byte(`{"emote_extensions": [".png", ".webp"]}`),
		bridgeTestOrigin + "e.webp":         webp,
	}}
	r := newBridgeRig(t, host)
	for range bridgeTestWorkers {
		if _, err := r.manager.FetchManifest(context.Background(), bridgeTestOrigin); err != nil {
			t.Fatal(err)
		}
	}
	base := bridgeTestOrigin + "e"
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	nextDecoded(t, r.manager, base)
	want := []string{bridgeTestOrigin + ManifestFileName, bridgeTestOrigin + ManifestFileName, base + ".png", base + ".webp"}
	if asked := host.askedFor(); !slices.Equal(asked, want) {
		t.Fatalf("asked %v, want %v", asked, want)
	}
}

func TestBridgeOfflineRecordsNothing(t *testing.T) {
	webp := fixture(t, "sprite_256x192.webp")
	host := &bridgeFakeHost{files: map[string][]byte{bridgeTestOrigin + "e.webp": webp}}
	r := newBridgeRig(t, host)
	base := bridgeTestOrigin + "e"
	r.manager.SetOffline(true)
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	select {
	case <-r.manager.Warnings():
	case <-time.After(bridgeTestTimeout):
		t.Fatal("offline request never ended")
	}
	if asked := host.askedFor(); len(asked) != 0 {
		t.Fatalf("offline asked the host: %v", asked)
	}
	time.Sleep(bridgeSettle)
	r.manager.SetOffline(false)
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	if d := nextDecoded(t, r.manager, base); d.Err != nil {
		t.Fatalf("a file offline said nothing about was not fetched: %v", d.Err)
	}
}

func TestBridgeExtraHostServesWhatTheServerLacks(t *testing.T) {
	const mirror = "http://mirror.test/ao/"
	webp := fixture(t, "sprite_256x192.webp")
	host := &bridgeFakeHost{files: map[string][]byte{mirror + "c/e.webp": webp}}
	r := newBridgeRig(t, host)
	base := bridgeTestOrigin + "c/e"
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	select {
	case <-r.manager.Warnings():
	case <-time.After(bridgeTestTimeout):
		t.Fatal("no missing warning without the mirror")
	}
	r.manager.SetAssetHosts(bridgeTestOrigin, []string{mirror})
	waitUntil(t, "the miss to be forgotten", func() bool { return !r.manager.IsConclusiveMiss(base, AssetTypeCharSprite) })
	r.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh)
	d := nextDecoded(t, r.manager, base)
	if d.Err != nil || d.Asset == nil || d.URL != mirror+"c/e.webp" {
		t.Fatalf("url %q err %v", d.URL, d.Err)
	}
	if !slices.Contains(host.askedFor(), mirror+ManifestFileName) {
		t.Fatal("the mirror's extensions.json was not read")
	}
	waitUntil(t, "the mirror to report no list", func() bool { return r.manager.HostListState(mirror) == media.ManifestAbsent })
}

func TestBridgeReportsAnExtraHostsListAndForgetsARemovedHost(t *testing.T) {
	const mirror = "http://mirror.test/ao/"
	host := &bridgeFakeHost{files: map[string][]byte{mirror + ManifestFileName: []byte(`{"emote_extensions": [".png"]}`)}}
	r := newBridgeRig(t, host)
	if s := r.manager.HostListState(mirror); s != media.ManifestUnchecked {
		t.Fatalf("before joining: %d, want unchecked", s)
	}
	r.manager.SetAssetHosts(bridgeTestOrigin, []string{mirror})
	waitUntil(t, "the mirror's list to be read", func() bool { return r.manager.HostListState(mirror) == media.ManifestRead })
	r.manager.SetAssetHosts(bridgeTestOrigin, nil)
	waitUntil(t, "the removed mirror to be forgotten", func() bool { return r.manager.HostListState(mirror) == media.ManifestUnchecked })
}
