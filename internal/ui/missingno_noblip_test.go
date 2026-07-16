package ui

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// wireLocalManager builds a real streaming-shaped Manager over a LocalFetcher
// rooted at dir, plus a headless TextureStore, and installs both on the App —
// enough to drive keepSceneAssetsWarm + drainWarnings against on-disk fixtures.
// LocalMode is true (assets already on disk); resolution is synchronous file I/O
// so the pipeline settles deterministically (poll with a deadline like the
// manager tests). Returns the LocalFetcher for building bases and a settle helper.
func wireLocalManager(t *testing.T, a *App, dir string) (*assets.LocalFetcher, func()) {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	store, err := render.NewTextureStore(ren)
	if err != nil {
		cleanup()
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store

	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)

	local := assets.NewLocalFetcher([]string{dir})
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    local,
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})
	return local, cleanup
}

// writeBareSprite writes a bare-spelling (unprefixed) idle sprite PNG at the webAO
// relpath characters/<char>/<emote>.png under dir — a mixed-naming pack that ships
// ONLY the bare file, never the "(a)<emote>" prefixed one.
func writeBareSprite(t *testing.T, dir, char, emote string) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("characters", char, emote+".png")
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSceneWarmNoMissingnoForBareOnlySprite is the no-blip guard: it exercises the
// exact path that produced the false-positive flash — keepSceneAssetsWarm's
// char-sprite re-demand for a mixed-naming pack whose sprite exists ONLY under the
// bare spelling. Because that re-demand now walks the prefix→bare fallback (like
// healSpriteLayer), the bare file resolves, NO Warning fires, and the base is
// NEVER marked missing — so the placeholder can never flash over a sprite that
// exists. A single-spelling warm (the pre-fix bug) would 404 the prefixed base,
// report it missing, and fail this test.
func TestSceneWarmNoMissingnoForBareOnlySprite(t *testing.T) {
	a := testTabApp(t)
	dir := t.TempDir()
	// The pack ships characters/hobo/normal.png (bare) — but NOT (a)normal.
	writeBareSprite(t, dir, "hobo", "normal")
	local, cleanup := wireLocalManager(t, a, dir)
	defer cleanup()

	// The live speaker's idle base is the PREFIXED spelling (the sprite's identity),
	// which does not exist on disk — only its bare twin does.
	prefixed := local.BaseURL() + "characters/hobo/(a)normal"
	// Seed the format learn to .png for this host so LocalMode resolves the bare
	// PNG (the default probe list is webp-only; a real server would have learned or
	// the manifest seeded it — see TestResolveRawFromLocalArchive).
	seedPNGFormat(t, a)

	a.room = &courtroom.Courtroom{}
	a.room.Scene.Speaker.Visible = true
	a.room.Scene.Speaker.Active = prefixed
	a.room.Scene.Speaker.IdleBase = prefixed

	// Throttle open so the warm re-demand actually fires this pass.
	a.sceneWarmLastDemand = time.Time{}
	a.keepSceneAssetsWarm()

	// Let the synchronous LocalFetcher pipeline settle: the decode of the resolved
	// BARE file must land (proving the fallback resolved it), and no Warning fires.
	settleDeadline := time.Now().Add(5 * time.Second)
	gotDecode := false
	for !gotDecode && time.Now().Before(settleDeadline) {
		select {
		case d := <-a.d.Manager.Decoded():
			if d.Err == nil && d.Base == prefixed {
				gotDecode = true
			}
			if d.Asset != nil {
				d.Asset.Release()
			}
		case w := <-a.d.Manager.Warnings():
			t.Fatalf("a bare-only sprite must NOT report missing (no-blip guarantee); got Warning for %q", w.Base)
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
	if !gotDecode {
		t.Fatal("the bare-spelling sprite never resolved through the fallback — the warm re-demand is not walking prefix→bare")
	}

	// Drain any (there should be none) warnings and assert the render-side missing
	// flag was never set for the displayed base.
	a.drainWarnings()
	if a.d.Store.IsMissing(prefixed) {
		t.Error("a bare-only sprite must never be flagged missing — missingno would flash over a sprite that exists")
	}
}

// TestSceneWarmMarksTrulyMissingSprite is the positive control: a base with
// NEITHER spelling present exhausts the whole prefix→bare chain, fires a Warning,
// and drainWarnings marks it missing — so the placeholder DOES show for a
// genuinely-absent sprite. This proves the guard above tests a real distinction,
// not a dead assertion.
func TestSceneWarmMarksTrulyMissingSprite(t *testing.T) {
	a := testTabApp(t)
	dir := t.TempDir() // empty: no sprite files at all
	local, cleanup := wireLocalManager(t, a, dir)
	defer cleanup()

	prefixed := local.BaseURL() + "characters/ghost/(a)normal"
	seedPNGFormat(t, a)

	a.room = &courtroom.Courtroom{}
	a.room.Scene.Speaker.Visible = true
	a.room.Scene.Speaker.Active = prefixed
	a.room.Scene.Speaker.IdleBase = prefixed

	a.sceneWarmLastDemand = time.Time{}
	a.keepSceneAssetsWarm()

	// Wait for the Warning (whole chain 404'd) to land, then relay it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("a sprite absent under EVERY spelling must report missing")
		}
		a.drainWarnings() // relays Warnings() → Store.MarkMissing
		if a.d.Store.IsMissing(prefixed) {
			break
		}
		// Drain any transient decode results so the pipeline keeps moving.
		select {
		case d := <-a.d.Manager.Decoded():
			if d.Asset != nil {
				d.Asset.Release()
			}
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// seedPNGFormat forces the char-sprite probe list to .png so the LocalMode
// resolver finds the on-disk PNG fixtures (the default candidate list is
// webp-only). Mirrors what a real server learns / the archive manifest seeds.
func seedPNGFormat(t *testing.T, a *App) {
	t.Helper()
	a.d.Prefs.SetFormatOrder(assets.AssetTypeCharSprite.Name(), []string{config.ExtPNG})
}
