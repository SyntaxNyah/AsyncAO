package render

import (
	"bytes"
	"image"
	"image/png"
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

// buildPumpManager wires a minimal asset manager over a local source folder —
// the same shape the app uses, enough to fetch + decode + emit on Decoded().
func buildPumpManager(t *testing.T, source assets.Fetcher) (*assets.Manager, *assets.Resolver) {
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
		Source: source, LocalMode: true, Pool: pool, Decoder: decoder,
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

	mgr, resolver := buildPumpManager(t, local)
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
