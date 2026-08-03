package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// waitForIndex pumps pollMountIndex until a layer is published, so the tests read
// like the frame loop does rather than sleeping on a goroutine.
func waitForIndex(t *testing.T, a *App) *assets.MountLayer {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.pollMountIndex()
		if l := a.d.Manager.MountLayer(); l != nil {
			return l
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no mount layer published within 5s")
	return nil
}

// TestApplyMountLayerPublishesAndTearsDown walks the three transitions the
// lifecycle exists for. The LEAVE case is the one that used to be impossible to
// express: the invalidation predicate needs the OUTGOING layer, which the live
// pointer no longer holds by the time it runs.
func TestApplyMountLayerPublishesAndTearsDown(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)

	if a.d.Manager.MountLayer() != nil {
		t.Fatal("a layer exists before layering was enabled")
	}

	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	if !a.d.Prefs.SetLocalAssets(false, []string{dir}) {
		t.Fatal("mount rejected")
	}
	a.d.Prefs.SetLayeredAssets(true)

	a.applyMountLayer()
	layer := waitForIndex(t, a)
	if layer.Index() == nil {
		t.Fatal("published a layer with no index")
	}

	// LEAVE: turning it off must publish nil and not panic on the now-nil layer.
	a.d.Prefs.SetLayeredAssets(false)
	a.applyMountLayer()
	if a.d.Manager.MountLayer() != nil {
		t.Error("the layer survived turning layering off")
	}
}

// TestApplyMountLayerSingleFlight pins rule 4 for the walk goroutine: a user
// tidying their mount list (or a reconnect loop) must never spawn a walk per
// click, and the cap-1 channel must never block a producer.
func TestApplyMountLayerSingleFlight(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	a.d.Prefs.SetLayeredAssets(true)

	for i := 0; i < 20; i++ {
		dir := t.TempDir()
		writeUIPack(t, dir, "characters/c/(a)normal.png")
		a.d.Prefs.SetLocalAssets(false, []string{dir})
		a.applyMountLayer()
		if a.mountIndexing != true && i == 0 {
			t.Fatal("the first edit did not start a walk")
		}
	}
	// At most one walk may ever be outstanding.
	if !a.mountIndexing {
		t.Log("the walk already landed; nothing outstanding")
	}
	// Drain to completion: the latch must clear and a layer must eventually land.
	waitForIndex(t, a)
	if a.mountIndexing {
		t.Error("the single-flight latch stayed set after the walk landed")
	}
}

// TestApplyMountLayerIsInertWithoutMounts is the structural half of the
// no-mounts contract at the UI level: no goroutine, no index, no layer.
func TestApplyMountLayerIsInertWithoutMounts(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)

	a.applyMountLayer()
	if a.mountIndexing {
		t.Error("a walk goroutine started with no mounts configured")
	}
	if a.mountIndex != nil || a.d.Manager.MountLayer() != nil {
		t.Error("an index or layer was built with no mounts configured")
	}
}

// TestLayeredIsInertInLocalOnlyMode pins the second half of the mode guard. The
// PREF layer already refuses the ambiguous both-flags state (config's own test
// covers that), but a Manager is mode-locked at CONSTRUCTION while the pref can
// invert under it — so a local-mode Manager must refuse to layer regardless of
// what the prefs currently say. Its source already is the mount folders, and
// layering a mount origin over a mount origin would double-serve.
func TestLayeredIsInertInLocalOnlyMode(t *testing.T) {
	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{dir}), true) // local-mode Manager

	a.d.Prefs.SetLocalAssets(false, []string{dir})
	a.d.Prefs.SetLayeredAssets(true) // the pref says layer; the Manager cannot

	if got := a.wantMountLayer(); len(got) != 0 {
		t.Errorf("wantMountLayer = %v against a local-mode Manager, want none", got)
	}
	a.applyMountLayer()
	if a.d.Manager.MountLayer() != nil {
		t.Error("a layer was published against a local-mode Manager")
	}
	if a.mountIndexing {
		t.Error("a walk goroutine started for a mode that can never use it")
	}
}

// TestPublishMountLayerUsesNormalizedOrigins pins the contract that keeps a pack
// from going silently inert: origins come from the URL BUILDER, which normalizes
// the trailing slash, never from a session's raw asset URL.
func TestPublishMountLayerUsesNormalizedOrigins(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)

	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	a.d.Prefs.SetLocalAssets(false, []string{dir})
	a.d.Prefs.SetLayeredAssets(true)

	// A server whose ASS field omits the trailing slash — the case that silently
	// breaks everything if the raw value is registered.
	a.urls = courtroom.NewURLBuilder("http://h/base")

	a.applyMountLayer()
	layer := waitForIndex(t, a)

	if !layer.Covers("http://h/base/characters/witch/(a)normal") {
		t.Error("the pack cannot answer for its own asset — the origin was not normalized")
	}
}

// --- helpers -------------------------------------------------------------------

func writeUIPack(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
