package assets

import (
	"context"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const managerWait = 5 * time.Second

// testRig wires a full pipeline against an httptest server.
type testRig struct {
	prefs    *config.AssetPreferences
	resolver *Resolver
	manager  *Manager
	pool     *network.Pool
	decoder  *DecoderPool
	disk     *cache.DiskCache
	t2       *cache.ByteBudgetLRU[string, []byte]
}

func newRig(t *testing.T, source Fetcher, localMode bool) *testRig {
	t.Helper()
	prefs := newTestPrefs(t)
	resolver := NewResolver(prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := NewDecoderPool(2)
	t.Cleanup(decoder.Close)

	m := NewManager(ManagerDeps{
		Resolver:  resolver,
		Prefs:     prefs,
		T2:        t2,
		Disk:      disk,
		Source:    source,
		LocalMode: localMode,
		Pool:      pool,
		Decoder:   decoder,
	})
	return &testRig{prefs: prefs, resolver: resolver, manager: m, pool: pool, decoder: decoder, disk: disk, t2: t2}
}

// countingServer serves payloads by exact path and counts every request.
type countingServer struct {
	mu       sync.Mutex
	requests map[string]int
	payloads map[string][]byte
	srv      *httptest.Server
}

func newCountingServer(t *testing.T, payloads map[string][]byte) *countingServer {
	t.Helper()
	cs := &countingServer{requests: map[string]int{}, payloads: payloads}
	cs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.requests[r.URL.Path]++
		cs.mu.Unlock()
		if data, ok := cs.payloads[r.URL.Path]; ok {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(cs.srv.Close)
	return cs
}

func (cs *countingServer) total() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	n := 0
	for _, c := range cs.requests {
		n += c
	}
	return n
}

func waitDecoded(t *testing.T, m *Manager) DecodedAsset {
	t.Helper()
	select {
	case d := <-m.Decoded():
		return d
	case <-time.After(managerWait):
		t.Fatal("no decoded asset delivered")
		return DecodedAsset{}
	}
}

func waitWarning(t *testing.T, m *Manager) Warning {
	t.Helper()
	select {
	case w := <-m.Warnings():
		return w
	case <-time.After(managerWait):
		t.Fatal("no warning delivered")
		return Warning{}
	}
}

// TestManagerWebPOnlyServerOneProbePerAsset is integration scenario §15.1:
// fallbacks OFF against a server shipping the preferred format — probe count
// must equal asset count exactly. (Payloads carry PNG magic so the test
// runs without CGO; the manager sniffs payloads, never extensions.)
func TestManagerWebPOnlyServerOneProbePerAsset(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{G: 255, A: 255})
	cs := newCountingServer(t, map[string][]byte{
		"/characters/phoenix/(a)normal.webp":  sprite,
		"/characters/phoenix/(b)normal.webp":  sprite,
		"/background/court/defenseempty.webp": sprite,
	})
	rig := newRig(t, network.NewClient(), false)

	bases := []struct {
		base string
		typ  AssetType
	}{
		{cs.srv.URL + "/characters/phoenix/(a)normal", AssetTypeCharSprite},
		{cs.srv.URL + "/characters/phoenix/(b)normal", AssetTypeCharSprite},
		{cs.srv.URL + "/background/court/defenseempty", AssetTypeBackground},
	}
	for _, b := range bases {
		rig.manager.Prefetch(b.base, b.typ, network.PriorityHigh) // AssetType: mixed (test)
	}
	for range bases {
		d := waitDecoded(t, rig.manager)
		if d.Err != nil {
			t.Fatalf("decode error: %v", d.Err)
		}
		d.Asset.Release()
	}

	if got := cs.total(); got != len(bases) {
		t.Errorf("probes = %d, want %d (exactly one per asset, zero fallbacks)", got, len(bases))
	}
}

// TestManagerPNGOnlyServerWarnsThenFallbacksRecover is integration scenario
// §15.2: sprites fail with fallbacks OFF (visible warning naming the formats
// tried), then enabling fallbacks at runtime loads them without restart.
func TestManagerPNGOnlyServerWarnsThenFallbacksRecover(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{B: 255, A: 255})
	cs := newCountingServer(t, map[string][]byte{
		"/characters/edgeworth/(a)normal.png": sprite,
	})
	rig := newRig(t, network.NewClient(), false)
	base := cs.srv.URL + "/characters/edgeworth/(a)normal"

	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	w := waitWarning(t, rig.manager)
	if w.Base != base || len(w.Tried) != 1 || w.Tried[0] != config.ExtWebP {
		t.Errorf("warning = %+v, want tried exactly [.webp]", w)
	}

	// User flips the global fallback toggle; the .png candidate was never
	// 404-cached, so the retry succeeds immediately.
	rig.prefs.SetGlobalFallbacks(true)
	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	d := waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatalf("post-fallback decode error: %v", d.Err)
	}
	d.Asset.Release()
	if ext, ok := rig.resolver.Learned(hostOf(base), AssetTypeCharSprite); !ok || ext != config.ExtPNG {
		t.Errorf("learned after fallback = %q,%v, want .png", ext, ok)
	}
}

// TestManagerLearnedWarmStart is integration scenario §15.4: with learned
// formats, N assets cost exactly N probes, all first-try.
func TestManagerLearnedWarmStart(t *testing.T) {
	icon := encodePNG(t, 4, 4, color.White)
	payloads := map[string][]byte{}
	const n = 5
	for _, c := range []string{"a", "b", "c", "d", "e"} {
		payloads["/characters/"+c+"/char_icon.png"] = icon
	}
	cs := newCountingServer(t, payloads)
	rig := newRig(t, network.NewClient(), false)

	host := hostOf(cs.srv.URL)
	rig.resolver.RecordSuccess(host, AssetTypeCharIcon, config.ExtPNG)

	for _, c := range []string{"a", "b", "c", "d", "e"} {
		rig.manager.Prefetch(cs.srv.URL+"/characters/"+c+"/char_icon", AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
	}
	for i := 0; i < n; i++ {
		d := waitDecoded(t, rig.manager)
		if d.Err != nil {
			t.Fatalf("decode error: %v", d.Err)
		}
		d.Asset.Release()
	}
	if got := cs.total(); got != n {
		t.Errorf("probes = %d, want exactly %d (learned warm start)", got, n)
	}
}

// TestManagerT1ShortCircuitUsesBaseKey pins the tier-1 fast path: probed
// assets are uploaded under their BASE key (TextureStore.Upload(d.Base, …)),
// so resolve must consult T1 by base — a resident texture costs zero probes
// and zero decodes (spec §8: T1 hit → done). This was once checked per
// candidate URL, extension included, which can never match the store key:
// every re-Prefetch of a visible asset silently re-decoded and re-uploaded.
func TestManagerT1ShortCircuitUsesBaseKey(t *testing.T) {
	icon := encodePNG(t, 4, 4, color.White)
	cs := newCountingServer(t, map[string][]byte{
		"/characters/phoenix/char_icon.png": icon,
	})
	rig := newRig(t, network.NewClient(), false)
	base := cs.srv.URL + "/characters/phoenix/char_icon"

	// Simulate the render side: textures land keyed by base. Safe to set
	// post-construction (same package, before any Prefetch).
	resident := map[string]bool{}
	rig.manager.t1Contains = func(key string) bool { return resident[key] }

	rig.manager.Prefetch(base, AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
	d := waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatalf("decode error: %v", d.Err)
	}
	d.Asset.Release()
	if got := cs.total(); got != 1 {
		t.Fatalf("probes after first load = %d, want 1", got)
	}

	resident[base] = true // "uploaded" — keyed by base, like the pump does

	// Re-ask while waiting: a Prefetch submitted during the FIRST pass's
	// tiny deliver→deferred-inflight-Delete window dedupes away WITHOUT
	// consulting T1 (correct — an identical pass is mid-flight). Slow CI
	// runners hit that window (the Linux 2-core runner did); retrying
	// keeps the test deterministic while the probe/decode assertions
	// below still pin the actual contract.
	deadline := time.Now().Add(managerWait)
	for rig.manager.Stats().T1Hits == 0 {
		if time.Now().After(deadline) {
			t.Fatal("T1 short-circuit never hit for a resident base")
		}
		rig.manager.Prefetch(base, AssetTypeCharIcon, network.PriorityHigh) // AssetType: CharIcon
		time.Sleep(5 * time.Millisecond)
	}
	if got := cs.total(); got != 1 {
		t.Errorf("probes after T1 hit = %d, want still 1 (no re-fetch)", got)
	}
	select {
	case d := <-rig.manager.Decoded():
		t.Errorf("unexpected re-decode of resident asset: %+v", d)
	default:
	}
}

// TestManagerUnprefixedSpriteFallback pins the AO sprite-name chain: packs
// ship "(a)<emote>" files OR bare "<emote>" files (AO2-Client probes the
// prefixed path, then the unprefixed one — CharLayer::load_image). When
// every format of the prefixed base 404s, the same formats probe under the
// bare base, and the asset keeps the PRIMARY (prefixed) identity so scene
// layers and T1 keys never change. Cost: exactly one extra probe, 404-cached.
func TestManagerUnprefixedSpriteFallback(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{R: 200, G: 80, A: 255})
	cs := newCountingServer(t, map[string][]byte{
		"/characters/tung/1.webp": sprite, // bare spelling only — no (a)/(b)
	})
	rig := newRig(t, network.NewClient(), false)
	primary := cs.srv.URL + "/characters/tung/(a)1"
	bare := cs.srv.URL + "/characters/tung/1"

	resident := map[string]bool{}
	rig.manager.t1Contains = func(key string) bool { return resident[key] }

	rig.manager.PrefetchWithFallback(primary, bare, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	d := waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatalf("decode error: %v", d.Err)
	}
	d.Asset.Release()
	if d.Base != primary {
		t.Errorf("delivered base = %q, want the primary identity %q", d.Base, primary)
	}
	if got := cs.total(); got != 2 {
		t.Errorf("probes = %d, want exactly 2 ((a)1.webp 404 + 1.webp hit)", got)
	}
	if ext, ok := rig.resolver.Learned(hostOf(primary), AssetTypeCharSprite); !ok || ext != config.ExtWebP {
		t.Errorf("learned = %q,%v, want .webp recorded from the bare hit", ext, ok)
	}

	// Resident under the primary key: the next pass is a T1 no-op.
	resident[primary] = true
	rig.manager.PrefetchWithFallback(primary, bare, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	deadline := time.Now().Add(managerWait)
	for rig.manager.Stats().T1Hits == 0 {
		if time.Now().After(deadline) {
			t.Fatal("T1 short-circuit never hit for the resident primary base")
		}
		time.Sleep(time.Millisecond)
	}
	if got := cs.total(); got != 2 {
		t.Errorf("probes after residency = %d, want still 2", got)
	}
}

// TestManagerStaleLearnedFormatRecovers covers the server-repack case: the
// learned format starts 404ing, the manager invalidates it and re-probes the
// full list once, landing on the new format.
func TestManagerStaleLearnedFormatRecovers(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{R: 128, A: 255})
	cs := newCountingServer(t, map[string][]byte{
		"/characters/maya/(a)normal.png": sprite, // only PNG exists now
	})
	rig := newRig(t, network.NewClient(), false)
	rig.prefs.SetGlobalFallbacks(true) // full list: webp, apng, gif, png
	host := hostOf(cs.srv.URL)
	rig.resolver.RecordSuccess(host, AssetTypeCharSprite, config.ExtWebP) // stale learn

	base := cs.srv.URL + "/characters/maya/(a)normal"
	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	d := waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatalf("decode error: %v", d.Err)
	}
	d.Asset.Release()

	if ext, ok := rig.resolver.Learned(host, AssetTypeCharSprite); !ok || ext != config.ExtPNG {
		t.Errorf("re-learned format = %q,%v, want .png", ext, ok)
	}
}

// TestManagerAudioBypassesDecode pins §8: audio bytes go to the audio
// channel untouched, never through the image decode pool.
func TestManagerAudioBypassesDecode(t *testing.T) {
	opus := []byte("OggS\x00fake-opus-payload")
	cs := newCountingServer(t, map[string][]byte{
		"/sounds/blips/male.opus": opus,
	})
	rig := newRig(t, network.NewClient(), false)

	rig.manager.Prefetch(cs.srv.URL+"/sounds/blips/male", AssetTypeBlip, network.PriorityHigh) // AssetType: Blip
	select {
	case a := <-rig.manager.Audio():
		if string(a.Data) != string(opus) {
			t.Error("audio payload mangled")
		}
		if a.Type != AssetTypeBlip {
			t.Errorf("audio type = %v", a.Type)
		}
	case <-time.After(managerWait):
		t.Fatal("no audio asset delivered")
	}
	if s := rig.decoder.Stats(); s.Decoded+s.Failed != 0 {
		t.Error("audio payload went through the image decoder")
	}
}

// TestManagerDiskPromotion pins §8's T3 → T2 promotion: a disk hit decodes
// without touching the network and records the learned format.
func TestManagerDiskPromotion(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	cs := newCountingServer(t, map[string][]byte{}) // network would 404 everything
	rig := newRig(t, network.NewClient(), false)

	base := cs.srv.URL + "/characters/franziska/(a)normal"
	url := base + config.ExtWebP
	rig.disk.Put(url, sprite)
	waitForDiskBlob(t, rig.disk, url)

	rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	d := waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatalf("decode error: %v", d.Err)
	}
	d.Asset.Release()

	if got := cs.total(); got != 0 {
		t.Errorf("network probes = %d, want 0 (disk hit)", got)
	}
	if _, ok := rig.t2.Get(url); !ok {
		t.Error("disk hit not promoted to T2")
	}
	if ext, ok := rig.resolver.Learned(hostOf(base), AssetTypeCharSprite); !ok || ext != config.ExtWebP {
		t.Errorf("disk hit did not record learned format: %q,%v", ext, ok)
	}
}

func waitForDiskBlob(t *testing.T, d *cache.DiskCache, url string) {
	t.Helper()
	deadline := time.Now().Add(managerWait)
	for time.Now().Before(deadline) {
		if _, ok := d.Get(url); ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("disk blob never landed")
}

// TestManagerLocalMounts covers the no-streaming legacy mode: assets come
// from user-chosen mount folders (first hit wins), nothing touches the
// network or the T3 cache.
func TestManagerLocalMounts(t *testing.T) {
	mountA := t.TempDir() // higher priority, sparse
	mountB := t.TempDir() // fallback with the actual content

	spriteDir := filepath.Join(mountB, "characters", "phoenix")
	if err := os.MkdirAll(spriteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sprite := encodePNG(t, 8, 8, color.RGBA{R: 9, A: 255})
	// Stored as .webp path with PNG magic — decoding goes by magic.
	if err := os.WriteFile(filepath.Join(spriteDir, "(a)normal.webp"), sprite, 0o644); err != nil {
		t.Fatal(err)
	}
	// An override in mountA must win for this other asset.
	overrideDir := filepath.Join(mountA, "characters", "phoenix")
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := encodePNG(t, 4, 4, color.RGBA{B: 9, A: 255})
	if err := os.WriteFile(filepath.Join(overrideDir, "(b)normal.webp"), override, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spriteDir, "(b)normal.webp"), sprite, 0o644); err != nil {
		t.Fatal(err)
	}

	local := NewLocalFetcher([]string{mountA, mountB})
	rig := newRig(t, local, true)
	origin := local.BaseURL()

	rig.manager.Prefetch(origin+"characters/phoenix/(a)normal", AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	d := waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatalf("local decode error: %v", d.Err)
	}
	if d.Asset.Width != 8 {
		t.Errorf("got %dx%d, want the mountB sprite", d.Asset.Width, d.Asset.Height)
	}
	d.Asset.Release()

	rig.manager.Prefetch(origin+"characters/phoenix/(b)normal", AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	d = waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatalf("local decode error: %v", d.Err)
	}
	if d.Asset.Width != 4 {
		t.Errorf("got %dx%d, want the mountA override (first mount wins)", d.Asset.Width, d.Asset.Height)
	}
	d.Asset.Release()

	// Missing asset → the same §4 warning machinery.
	rig.manager.Prefetch(origin+"characters/phoenix/(a)void", AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	w := waitWarning(t, rig.manager)
	if !strings.HasSuffix(w.Base, "(a)void") {
		t.Errorf("warning base = %q", w.Base)
	}
}

func TestLocalFetcherDistinctMountSetsDistinctOrigins(t *testing.T) {
	a := NewLocalFetcher([]string{"/ao/base"})
	b := NewLocalFetcher([]string{"/ao/other"})
	if a.BaseURL() == b.BaseURL() {
		t.Error("different mount sets share an origin: caches would collide")
	}
}

// TestManagerInflightDedup: two prefetches for the same base collapse into
// one pipeline pass (and one decode).
func TestManagerInflightDedup(t *testing.T) {
	sprite := encodePNG(t, 8, 8, color.White)
	cs := newCountingServer(t, map[string][]byte{
		"/characters/godot/(a)normal.webp": sprite,
	})
	rig := newRig(t, network.NewClient(), false)
	base := cs.srv.URL + "/characters/godot/(a)normal"

	for i := 0; i < 4; i++ {
		rig.manager.Prefetch(base, AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	}
	d := waitDecoded(t, rig.manager)
	if d.Err != nil {
		t.Fatal(d.Err)
	}
	d.Asset.Release()

	// No further deliveries expected; give stragglers a moment to misbehave.
	select {
	case extra := <-rig.manager.Decoded():
		// A second delivery is acceptable only if it was a T2 hit decode
		// (same bytes); more than one upstream probe is not.
		if extra.Err == nil && extra.Asset != nil {
			extra.Asset.Release()
		}
	case <-time.After(200 * time.Millisecond):
	}
	if got := cs.total(); got != 1 {
		t.Errorf("upstream probes = %d, want 1 (inflight dedup + singleflight)", got)
	}
}

// TestManagerPrefetchRawWarmsTiers pins the decode-free raw lane: one
// network probe lands the payload in T2 + disk, and a later synchronous
// FetchRaw is a pure memory hit (hover-warm → instant character pick).
func TestManagerPrefetchRawWarmsTiers(t *testing.T) {
	ini := []byte("[Options]\nside = def\n")
	cs := newCountingServer(t, map[string][]byte{
		"/characters/tung/char.ini": ini,
	})
	rig := newRig(t, network.NewClient(), false)
	url := cs.srv.URL + "/characters/tung/char.ini"

	rig.manager.PrefetchRaw(url, network.PriorityLow) // raw text: char.ini
	deadline := time.Now().Add(managerWait)
	for {
		if _, ok := rig.t2.Get(url); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PrefetchRaw never landed in T2")
		}
		time.Sleep(time.Millisecond)
	}
	if got := cs.total(); got != 1 {
		t.Fatalf("probes = %d, want 1", got)
	}

	data, err := rig.manager.FetchRaw(context.Background(), url)
	if err != nil || string(data) != string(ini) {
		t.Fatalf("FetchRaw after warm: %v / %q", err, data)
	}
	if got := cs.total(); got != 1 {
		t.Fatalf("probes after warmed FetchRaw = %d, want still 1 (memory hit)", got)
	}
}
