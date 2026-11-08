package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
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

// TestAssetSourceLayeredCoversTheServer: picking the layered mode puts the folder over the server without a restart
// (GitHub #19).
func TestAssetSourceLayeredCoversTheServer(t *testing.T) {
	const origin = "http://h/base/"
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	a.urls = courtroom.NewURLBuilder(origin)

	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	a.setAssetSourceMode(assetSrcLayered, []string{dir})

	if !a.d.Manager.PackCovers(origin + "characters/witch/(a)normal") {
		t.Error("the layered folder does not answer for the server's asset")
	}
}

// TestAssetSourceStreamModeNeverLayers: in Stream mode the folders are remembered but never answer for a server.
func TestAssetSourceStreamModeNeverLayers(t *testing.T) {
	const origin = "http://h/base/"
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	a.urls = courtroom.NewURLBuilder(origin)

	dir := t.TempDir()
	writeUIPack(t, dir, "characters/witch/(a)normal.png")
	a.setAssetSourceMode(assetSrcStream, []string{dir})

	if a.d.Manager.PackCovers(origin + "characters/witch/(a)normal") {
		t.Error("Stream mode layered the folder over the server")
	}
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
