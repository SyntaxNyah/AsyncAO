package render

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// buildPumpManager wires a minimal asset manager over the given source —
// the same shape the app uses, enough to fetch + decode + emit on Decoded().
// localMode true = a mount-folder source; false = a real network client.
func buildPumpManager(t *testing.T, source assets.Fetcher, localMode bool) (*assets.Manager, *assets.Resolver) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	resolver := assets.NewResolver(prefs)
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
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver: resolver, Prefs: prefs, T2: t2, Disk: disk,
		Source: source, LocalMode: localMode, Pool: pool, Decoder: decoder,
	})
	return mgr, resolver
}

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	im := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range im.Pix {
		im.Pix[i] = 0xFF // opaque white
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPumpUploadsPrefetchByBase pins the assumption the scene export's asset
// pre-warm rides on: Manager.Prefetch(base) → (fetch+decode) → Pump.Frame() makes
// the texture resident in T1 under the SAME base key the warm gate (and the
// viewport) later query with Store.Contains(base). If that key ever drifted, the
// GIF/WebP export would wait out its whole timeout and capture an empty stage —
// the exact "no characters" failure this gate exists to prevent.
func TestPumpUploadsPrefetchByBase(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	// A local "server" with one real PNG sprite. Derive the on-disk path FROM the
	// URL builder (seg() lowercases/encodes — hand-built paths only match on a
	// case-insensitive FS), exactly like the archive round-trip test.
	dir := t.TempDir()
	local := assets.NewLocalFetcher([]string{dir})
	origin := local.BaseURL()
	urls := courtroom.NewURLBuilder(origin)
	base := urls.Emote("phoenix", "normal", courtroom.EmoteIdle) // the prefixed-spelling identity base
	rel, _ := strings.CutPrefix(base, origin)
	writeTestPNG(t, filepath.Join(dir, filepath.FromSlash(rel+config.ExtPNG)))

	mgr, resolver := buildPumpManager(t, local, true)
	// The default probe list is webp-only; this fixture is PNG, so seed the host's
	// learned format (what a real extensions.json / a prior learned hit provides).
	resolver.RecordSuccess(assets.HostOf(base), assets.AssetTypeCharSprite, config.ExtPNG)

	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	pump := NewPump(store, mgr, nil)

	mgr.Prefetch(base, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (warm-gate prefetch)

	// Pump every "frame" until the async decode lands in T1 — the same loop the
	// export's warm phase runs (Pump.Frame() drains Manager.Decoded() each frame).
	deadline := time.Now().Add(3 * time.Second)
	for !store.Contains(base) && time.Now().Before(deadline) {
		pump.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if !store.Contains(base) {
		t.Fatalf("sprite never became resident under base %q — the export pre-warm would time out on an empty stage", base)
	}
}

// TestPumpTransientErrorsDontPin pins the negative-cache split (live report:
// a flaky mirror behind Cloudflare 5xx'd in bursts and every touched asset
// vanished for decodeFailTTL): a NETWORK-stage failure reaching the pump must
// stay out of the decode negative cache — the asset remains re-demandable the
// moment the origin recovers — while genuinely corrupt bytes still pin.
func TestPumpTransientErrorsDontPin(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	// Two servers = two hosts, so the sick origin's host backoff can never
	// couple into the corrupt-bytes assertion.
	srvSick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway) // network-stage failure (sick origin)
	}))
	defer srvSick.Close()
	srvCorrupt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "these bytes are not an image") // 200 → a real DECODE failure
	}))
	defer srvCorrupt.Close()

	mgr, _ := buildPumpManager(t, network.NewClient(), false)
	store, err := NewTextureStore(ren)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()
	pump := NewPump(store, mgr, nil)

	// Sick origin: the fetch (with its bounded retry) fails at the network
	// stage → transient → counted, NOT pinned.
	sick := srvSick.URL + "/characters/witch/(a)sick"
	mgr.Prefetch(sick, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (test)
	deadline := time.Now().Add(5 * time.Second)
	for pump.transientErrs == 0 && time.Now().Before(deadline) {
		pump.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if pump.transientErrs == 0 {
		t.Fatal("the network failure never reached the pump as transient")
	}
	if store.FailedRecently(sick) {
		t.Error("a transient network failure entered the decode negative cache — the asset would stay blank for decodeFailTTL after the origin recovers")
	}

	// Corrupt bytes: a genuine decode failure still pins (the cache's job).
	corrupt := srvCorrupt.URL + "/characters/witch/(a)corrupt"
	mgr.Prefetch(corrupt, assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (test)
	deadline = time.Now().Add(5 * time.Second)
	for !store.FailedRecently(corrupt) && time.Now().Before(deadline) {
		pump.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	if !store.FailedRecently(corrupt) {
		t.Error("corrupt bytes never entered the decode negative cache")
	}
}
