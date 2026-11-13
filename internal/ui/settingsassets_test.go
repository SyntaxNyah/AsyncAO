package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestSwitchesAddUpToTheMode: the folder, packages and stream switches are independent, and the mode is what they
// add up to.
func TestSwitchesAddUpToTheMode(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	mounts := []string{t.TempDir()}
	a.d.AutoMounts = []string{t.TempDir()}

	for _, tc := range []struct {
		folders, packages, stream bool
		want                      int
	}{
		{true, false, true, assetSrcLayered},
		{false, true, true, assetSrcLayered},
		{false, true, false, assetSrcLocal},
		{true, true, false, assetSrcLocal},
		{false, false, true, assetSrcStream},
	} {
		a.setPackagesInUse(tc.packages)
		a.setSources(tc.folders, tc.stream, mounts)
		if got := a.assetSourceMode(); got != tc.want {
			t.Errorf("folders=%v packages=%v stream=%v: mode %d, want %d", tc.folders, tc.packages, tc.stream, got, tc.want)
		}
		if got := a.d.Prefs.Load().LocalAssetsLayered; got != tc.folders {
			t.Errorf("folders=%v packages=%v stream=%v: the folder switch read back %v", tc.folders, tc.packages, tc.stream, got)
		}
	}

	// Nothing local and streaming off would read nothing, so streaming comes back on.
	a.setPackagesInUse(false)
	a.setSources(false, false, mounts)
	if streamOff, _ := a.d.Prefs.LocalAssets(); streamOff {
		t.Error("streaming stayed off with nothing local to read")
	}
}

// TestStreamOffSwitchesLocalSourcesOn: switching streaming off with folders and packages off switches both on, and
// switching both off again brings streaming back.
func TestStreamOffSwitchesLocalSourcesOn(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	mounts := []string{t.TempDir()}
	a.d.AutoMounts = []string{t.TempDir()}
	a.setPackagesInUse(false)
	a.setSources(false, true, mounts)

	a.setStreaming(false, mounts)
	snap := a.d.Prefs.Load()
	if !snap.LocalAssetsLayered || snap.PackagesOff {
		t.Errorf("streaming off left folders=%v packages=%v, want both on", snap.LocalAssetsLayered, !snap.PackagesOff)
	}
	if got := a.assetSourceMode(); got != assetSrcLocal {
		t.Errorf("mode %d after streaming off, want local only (%d)", got, assetSrcLocal)
	}

	a.setPackagesInUse(false)
	if got := a.assetSourceMode(); got != assetSrcLocal {
		t.Errorf("packages off with folders on: mode %d, want local only (%d)", got, assetSrcLocal)
	}
	a.setSources(false, false, mounts)
	if streamOff, _ := a.d.Prefs.LocalAssets(); streamOff {
		t.Error("folders and packages both off, but streaming stayed off")
	}
}

// TestPackagesAreSearchedWithFoldersOff: the packages switch works on its own, and folders on their own.
func TestPackagesAreSearchedWithFoldersOff(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	mount, pkg := t.TempDir(), t.TempDir()
	a.d.AutoMounts = []string{pkg}

	a.setSources(false, true, []string{mount})
	if _, got := a.layeredAssets(); !slices.Equal(got, []string{pkg}) {
		t.Errorf("folders off, packages on: searched %v, want only the package", got)
	}
	a.setPackagesInUse(false)
	a.setSources(true, true, []string{mount})
	if _, got := a.layeredAssets(); !slices.Equal(got, []string{mount}) {
		t.Errorf("folders on, packages off: searched %v, want only the folder", got)
	}
}

// TestFirstFolderSwitchesFoldersOn: a folder added to an empty list is meant to be used.
func TestFirstFolderSwitchesFoldersOn(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	if a.d.Prefs.Load().LocalAssetsLayered {
		t.Fatal("folders start on")
	}
	if _, ok := a.addSource("", t.TempDir(), nil); !ok {
		t.Fatal("the folder was refused")
	}
	if !a.d.Prefs.Load().LocalAssetsLayered {
		t.Error("adding the first folder left folders switched off")
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
	a.setSources(true, true, []string{dir})

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
	a.setSources(false, true, []string{dir})

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

// TestSettingsCopyExplainsZipAndPriority: the product owner asked that the UI, not just the docs, say that .zip packs
// work, that the user's own files beat the stream, and that the list order decides.
func TestSettingsCopyExplainsZipAndPriority(t *testing.T) {
	for _, want := range []struct{ what, text, needle string }{
		{"zip packs are accepted as mounts", mountOrderHint, ".zip pack"},
		{"first mount wins", mountOrderHint, "first one with the file wins"},
		{"the user's files replace the server's", assetModeHint(assetSrcLayered), "replace the server's"},
		{"anything not held still streams", assetModeHint(assetSrcLayered), "still streams from the server"},
	} {
		if !strings.Contains(want.text, want.needle) {
			t.Errorf("the Asset source cards no longer explain %s (looked for %q in %q)", want.what, want.needle, want.text)
		}
	}
}

// TestMoveMountChangesTheSearchOrder: moving a folder up makes the folders search it first.
func TestMoveMountChangesTheSearchOrder(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	first, second, third := t.TempDir(), t.TempDir(), t.TempDir()
	a.setSources(true, true, []string{first, second, third})

	const from, to = 2, 0
	a.moveMount(from, to)

	want := []string{third, first, second}
	if _, saved := a.d.Prefs.LocalAssets(); !slices.Equal(saved, want) {
		t.Errorf("saved order %v, want %v", saved, want)
	}
	if mounted, _, _ := a.d.Manager.FolderStatus(); !slices.Equal(mounted, want) {
		t.Errorf("search order %v, want %v", mounted, want)
	}
	if a.assetSourceMode() != assetSrcLayered {
		t.Error("moving a folder changed the asset source mode")
	}
}

// TestAddSourceRoutesByWhatWasTyped: the one add field puts a web address under the picked server's mirrors and
// anything else in the folder list.
func TestAddSourceRoutesByWhatWasTyped(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), false)
	const key, mirror = "wss://srv.test/", "https://mirror.test/base/"
	dir := t.TempDir()

	if _, ok := a.addSource(key, "  "+mirror+"  ", nil); !ok {
		t.Fatal("the mirror was refused")
	}
	if _, ok := a.addSource(key, dir, nil); !ok {
		t.Fatal("the folder was refused")
	}
	if hosts := a.d.Prefs.Load().ServerAssetHosts(key); !slices.Equal(hosts, []string{mirror}) {
		t.Errorf("mirrors %v, want [%s]", hosts, mirror)
	}
	if _, mounts := a.d.Prefs.LocalAssets(); !slices.Equal(mounts, []string{dir}) {
		t.Errorf("folders %v, want [%s]", mounts, dir)
	}
	if _, ok := a.addSource("", mirror, nil); ok {
		t.Error("a mirror was added with no server picked")
	}
	if _, ok := a.addSource(key, dir, []string{dir}); ok {
		t.Error("a folder already in the list was added again")
	}
}

func TestMoveItemKeepsTheInputAndEveryEntry(t *testing.T) {
	in := []string{"a", "b", "c", "d"}
	const last = 3
	for _, tc := range []struct {
		from, to int
		want     []string
	}{
		{0, 1, []string{"b", "a", "c", "d"}},
		{last, last - 1, []string{"a", "b", "d", "c"}},
		{0, last, []string{"b", "c", "d", "a"}},
	} {
		if got := moveItem(in, tc.from, tc.to); !slices.Equal(got, tc.want) {
			t.Errorf("moveItem(%d→%d) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
	if !slices.Equal(in, []string{"a", "b", "c", "d"}) {
		t.Errorf("moveItem changed its input: %v", in)
	}
}

func TestMountSummaryCountsFoldersPacksAndServed(t *testing.T) {
	const auto, served = 1, 412
	for _, tc := range []struct {
		mounts       []string
		auto, served int
		want         string
	}{
		{nil, 0, 0, "No folders or packs added."},
		{[]string{`C:\AO2\base`}, 0, 0, "1 folder"},
		{[]string{`C:\AO2\base`, `D:\a.ZIP`, `D:\b.zip`}, auto, served, "1 folder  ·  2 packs  ·  1 auto-mounted  ·  412 assets from them this session"},
	} {
		if got := mountSummary(tc.mounts, tc.auto, int64(tc.served)); got != tc.want {
			t.Errorf("mountSummary(%v) = %q, want %q", tc.mounts, got, tc.want)
		}
	}
}
