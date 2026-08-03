package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// TestAssetSourceModeIsExclusive pins that the three-way control can never
// persist the ambiguous both-bools state, whichever order the user clicks in.
func TestAssetSourceModeIsExclusive(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	mounts := []string{t.TempDir()}

	for _, want := range []int{assetSrcLayered, assetSrcLocal, assetSrcStream, assetSrcLocal, assetSrcLayered} {
		a.setAssetSourceMode(want, mounts)
		if got := a.assetSourceMode(); got != want {
			t.Fatalf("set mode %d, read back %d", want, got)
		}
		enabled, _ := a.d.Prefs.LocalAssets()
		layered, _ := a.d.Prefs.LayeredAssets()
		if enabled && layered {
			t.Fatalf("mode %d persisted BOTH bools — the ambiguous state is reachable", want)
		}
	}
}

// TestAssetSourceLayeredPublishesALayer is the end-to-end gesture GitHub #19 is
// actually about: the user adds a folder and picks the layered mode, and the pack
// must become live without a restart.
func TestAssetSourceLayeredPublishesALayer(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)

	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	if !a.d.Prefs.SetLocalAssets(false, []string{dir}) {
		t.Fatal("mount rejected")
	}
	a.setAssetSourceMode(assetSrcLayered, []string{dir})

	layer := waitForIndex(t, a)
	if layer.Index() == nil {
		t.Fatal("no index behind the published layer")
	}
	if files, _, _ := layer.Index().Stats(); files != 1 {
		t.Errorf("indexed %d files, want the 1 in the pack", files)
	}
}

// TestAssetSourceStreamModeNeverLayers pins the default: mounts may be configured
// and remembered, but in Stream mode nothing is layered. This is the state the
// #19 reporter was in — which is why the panel shows an explicit nudge.
func TestAssetSourceStreamModeNeverLayers(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)

	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	a.setAssetSourceMode(assetSrcStream, []string{dir})

	if got := a.wantMountLayer(); len(got) != 0 {
		t.Errorf("Stream mode wants a layer over %v", got)
	}
	if a.d.Manager.MountLayer() != nil {
		t.Error("Stream mode published a layer")
	}
	// ...but the folders are REMEMBERED, so switching modes needs no re-typing.
	if _, mounts := a.d.Prefs.LocalAssets(); len(mounts) != 1 {
		t.Errorf("Stream mode dropped the configured mounts: %v", mounts)
	}
}

// TestAssetSourceSettingsSearchFindsTheNewTerms pins that the panel is reachable
// from the settings search by the words a user would actually type. The rows are
// drawn muted rather than hidden precisely so they stay registered.
func TestAssetSourceSettingsSearchFindsTheNewTerms(t *testing.T) {
	for _, q := range []string{"asset source", "layered", "pack", "zip", "rescan", "override"} {
		found := false
		for _, kw := range settingsSearchKeywords[tabAssets] {
			if strings.Contains(kw, q) || strings.Contains(q, kw) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("settings search has no keyword matching %q — the panel is unreachable by search", q)
		}
	}
}

// TestSettingsCopyExplainsZipAndPriority is a copy gate, not a logic one. The
// product owner asked specifically that the UI — not just the docs — say that
// .zip packs work and that the user's own files take priority over the stream.
// Both are things people otherwise only discover by being confused.
func TestSettingsCopyExplainsZipAndPriority(t *testing.T) {
	src := readSourceFile(t, "settingsassets.go")
	for _, want := range []struct{ what, needle string }{
		{"zip packs are accepted as mounts", ".zip pack"},
		{"the user's files replace the server's", "REPLACE the server's copies"},
		{"first mount wins", "the first one with the file wins"},
		{"anything not held still streams", "still streams from the server"},
	} {
		if !strings.Contains(src, want.needle) {
			t.Errorf("the Asset source panel no longer explains %s (looked for %q)", want.what, want.needle)
		}
	}
}
