package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// mkCharPack builds a mount-shaped folder tree: <root>/characters/<name>/ for each
// name, plus one stray FILE in characters/ that must never be listed as a character.
func mkCharPack(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	chars := filepath.Join(root, iniBaseCharsDir)
	if err := os.MkdirAll(chars, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(chars, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(chars, "readme.txt"), []byte("not a character"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestScanLocalCharFoldersListsMountedCharacters is the whole point of the "From
// your base" source: a user with a content pack mounted can browse and wear what is
// IN it, without the server publishing anything.
//
// It also pins the three rules the scan is built on — files are not characters,
// mounts are first-hit-wins (so an earlier pack shadows a later one, exactly as
// BuildMountIndex resolves asset reads), and the result is name-sorted so the grid
// does not reshuffle between scans.
func TestScanLocalCharFoldersListsMountedCharacters(t *testing.T) {
	first := mkCharPack(t, "Phoenix", "edgeworth")
	second := mkCharPack(t, "EDGEWORTH", "Maya") // duplicate in a different case + one new

	got := scanLocalCharFolders([]string{first, second})
	if got.err != "" {
		t.Fatalf("a good pair of mounts reported %q", got.err)
	}
	if got.truncated {
		t.Error("three folders must not trip the cap")
	}
	want := []string{"edgeworth", "Maya", "Phoenix"} // case-insensitive sort, first spelling kept
	if len(got.names) != len(want) {
		t.Fatalf("scan = %v, want %v", got.names, want)
	}
	for i := range want {
		if got.names[i] != want[i] {
			t.Errorf("scan[%d] = %q, want %q (full result %v)", i, got.names[i], want[i], got.names)
		}
	}
}

// TestScanLocalCharFoldersDegradesOnAnUnusableMount pins the degradation rule: a
// mount that is missing, is a zip, or simply has no characters/ folder is SKIPPED,
// never fatal — mounts go missing (an unplugged drive, a deleted folder) and
// whatever else resolved is still worth browsing.
func TestScanLocalCharFoldersDegradesOnAnUnusableMount(t *testing.T) {
	good := mkCharPack(t, "Phoenix")
	missing := filepath.Join(t.TempDir(), "not-there")
	bare := t.TempDir() // exists, but has no characters/ subfolder

	got := scanLocalCharFolders([]string{missing, good, bare})
	if len(got.names) != 1 || got.names[0] != "Phoenix" {
		t.Fatalf("a good mount beside two unusable ones gave %v, want [Phoenix]", got.names)
	}
	if got.err != "" {
		t.Errorf("a scan that FOUND something must not report an error, got %q", got.err)
	}

	// Every mount unusable: nothing to show, and a line that says why rather than an
	// unexplained empty grid.
	none := scanLocalCharFolders([]string{missing, bare})
	if len(none.names) != 0 {
		t.Errorf("unusable mounts produced names: %v", none.names)
	}
	if none.err == "" {
		t.Error("a scan that found nothing because every mount was unusable must say so")
	}

	// No mounts at all is not an error — it is the ordinary streaming setup.
	if empty := scanLocalCharFolders(nil); empty.err != "" || len(empty.names) != 0 {
		t.Errorf("no mounts = %+v, want a silent empty result", empty)
	}
}

// TestScanLocalCharFoldersCarriesItsStalenessKey pins that the result names the
// mount list it came from. ensureLocalCharFolders compares that key to decide
// whether to spawn another scan, so a result that lost it would either re-scan the
// disk every frame or never re-scan at all.
func TestScanLocalCharFoldersCarriesItsStalenessKey(t *testing.T) {
	mounts := []string{mkCharPack(t, "Phoenix")}
	got := scanLocalCharFolders(mounts)
	if !slices.Equal(got.mounts, mounts) {
		t.Fatalf("scan.mounts = %v, want the %v it was asked for", got.mounts, mounts)
	}
	// A COPY, not the caller's backing array: the preferences accessor hands out a
	// fresh slice each call, but the scan runs on a goroutine and must not alias
	// anything the render thread may reuse.
	mounts[0] = "mutated"
	if got.mounts[0] == "mutated" {
		t.Error("scan.mounts aliases the caller's slice — a goroutine result must own its key")
	}
}

// TestBaseMountsEqualMatchesBaseMounts is the anti-drift pin for the staleness
// check. ensureLocalCharFolders no longer compares against baseMounts() — that
// copies the list out of preferences on EVERY frame the tab is open — it asks
// config.BaseMountsEqual, which answers the same question without a copy. Two
// implementations of one rule drift, so this walks the whole mode matrix and
// demands they agree, including on the states where the answer is "no mounts".
func TestBaseMountsEqualMatchesBaseMounts(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}

	paths := []string{"C:\\packs\\one", "C:\\packs\\two"}
	for _, tc := range []struct {
		name            string
		local, layered  bool
		mounts          []string
		wantScannedList bool // true = the two mounts, false = nothing to scan
	}{
		{"streaming, no mounts saved", false, false, nil, false},
		{"streaming with mounts saved (mode is OFF, so nothing is scanned)", false, false, paths, false},
		{"local-only", true, false, paths, true},
		{"layered", false, true, paths, true},
		{"layered with no mounts is inert", false, true, nil, false},
		{"both set: local-only wins, and it yields the same list anyway", true, true, paths, true},
	} {
		if !prefs.SetLocalAssets(tc.local, tc.mounts) {
			t.Fatalf("%s: SetLocalAssets refused", tc.name)
		}
		prefs.SetLayeredAssets(tc.layered)
		if tc.local && tc.layered { // SetLayeredAssets clears the exclusive flag; put it back
			if !prefs.SetLocalAssets(true, tc.mounts) {
				t.Fatalf("%s: SetLocalAssets refused", tc.name)
			}
		}
		got := a.baseMounts()
		want := []string(nil)
		if tc.wantScannedList {
			want = tc.mounts
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: baseMounts() = %v, want %v", tc.name, got, want)
		}
		if !prefs.BaseMountsEqual(got) {
			t.Errorf("%s: BaseMountsEqual disagrees with baseMounts() = %v", tc.name, got)
		}
		// The empty case must accept BOTH spellings of "nothing": the scan settles
		// on []string{} (non-nil, so the memo counts as taken), baseMounts returns nil.
		if !tc.wantScannedList && !prefs.BaseMountsEqual([]string{}) {
			t.Errorf("%s: BaseMountsEqual([]string{}) = false, so a mountless client would re-scan every frame", tc.name)
		}
		// And a genuine change must still read as changed, or the browser would never
		// re-scan after a mount edit.
		if prefs.BaseMountsEqual([]string{"C:\\packs\\other"}) {
			t.Errorf("%s: a different mount list compared equal", tc.name)
		}
	}
}

// TestEnsureLocalCharFoldersIsAllocationFreeWhenUnchanged pins the claim that
// licenses calling this from a draw path. The measured subject is a client WITH
// mounts configured — a mountless one escapes trivially because make([]string, 0)
// returns no allocation at all, which is exactly how the per-frame copy hid.
func TestEnsureLocalCharFoldersIsAllocationFreeWhenUnchanged(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	mounts := []string{"C:\\packs\\one", "C:\\packs\\two"}
	if !prefs.SetLocalAssets(true, mounts) {
		t.Fatal("SetLocalAssets refused")
	}
	a := &App{d: Deps{Prefs: prefs}}
	a.iniBase.scanned = mounts // the state after a completed scan: nothing has changed

	if n := testing.AllocsPerRun(200, a.ensureLocalCharFolders); n != 0 {
		t.Errorf("ensureLocalCharFolders allocates %v time(s) per frame with mounts configured, want 0", n)
	}
	if a.iniBase.scanning {
		t.Error("the unchanged path started a scan")
	}
}

// TestIniswapChipDropsRatherThanOverlapping pins the width-drop contract with plain
// arithmetic — no renderer needed. The chip's reserve has to be at least as wide as
// the right-aligned missing-asset chip it must never sit under, or the two would
// collide on a narrow window instead of one of them standing down.
func TestIniswapChipDropsRatherThanOverlapping(t *testing.T) {
	// A realistic longest label for the sibling chip, at a generous 8 px/rune (the
	// 14 px chrome face averages nearer 7), plus its plate padding, its margin and
	// its own minimum gap. The reserve must cover it.
	const worstCaseLabel = "! 12 assets missing"
	const glyphPx = int32(8)
	need := int32(len(worstCaseLabel))*glyphPx + 2*assetMissChipPadX + assetMissChipMarginX + assetMissChipMinGapX
	if iniswapChipRightReservePx < need {
		t.Errorf("iniswapChipRightReservePx = %d, want at least %d so the Iniswap chip never lands under the missing-asset chip",
			iniswapChipRightReservePx, need)
	}
	// And the plate must fit inside the band, hairline included, or it would paint
	// over the separator the band draws to divide itself from the screen below.
	if h := menuBarH - menuBarHairlineH - 2*iniswapChipInsetY; h <= 0 || h > menuBarH-menuBarHairlineH {
		t.Errorf("the chip's plate height resolves to %d in a %d px band", h, menuBarH)
	}
}
