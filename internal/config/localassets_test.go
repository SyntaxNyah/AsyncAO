package config

import (
	"path/filepath"
	"strconv"
	"testing"
)

// v1.89.0 stores the asset-source mode as TWO independent bools rather than a
// tri-state enum, so that localAssetsEnabled keeps its exact pre-1.89 meaning and
// no saved prefs.json changes meaning on upgrade. That buys zero migration at the
// cost of one reachable ambiguous combination, so these tests pin the
// normalization, the cap policy, and the reset behaviour.

// TestLayeredAssetsIsInertWithoutMounts pins that the mode is only "on" when it
// can actually do something: layering with an empty mount list must report false,
// or the UI would claim a pack is active when there is nothing to read.
func TestLayeredAssetsIsInertWithoutMounts(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	p.SetLayeredAssets(true)
	if on, _ := p.LayeredAssets(); on {
		t.Error("layering reports ON with no mounts configured")
	}
	if !p.SetLocalAssets(false, []string{t.TempDir()}) {
		t.Fatal("adding one mount was refused")
	}
	if on, mounts := p.LayeredAssets(); !on || len(mounts) != 1 {
		t.Errorf("layering with a mount = %v (%d mounts), want true (1)", on, len(mounts))
	}
}

// TestLayeredAndLocalOnlyAreMutuallyExclusive pins the resolution of the
// reachable fourth state. Local-only builds a local-mode Manager whose URLs
// already carry the local:// origin; layering a mount origin OVER a mount origin
// would double-serve, so exactly one of them may be live and Local-only wins.
func TestLayeredAndLocalOnlyAreMutuallyExclusive(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	mount := t.TempDir()

	// Selecting layered clears the exclusive flag.
	p.SetLocalAssets(true, []string{mount})
	p.SetLayeredAssets(true)
	if enabled, _ := p.LocalAssets(); enabled {
		t.Error("selecting layered left the exclusive no-streaming flag set")
	}
	if on, _ := p.LayeredAssets(); !on {
		t.Error("layered did not take effect")
	}

	// And if both somehow end up set (a hand-edited prefs.json), Local-only wins
	// and layering reports inert — one defined meaning, no crash.
	p.mu.Lock()
	p.LocalAssetsEnabled, p.LocalAssetsLayered = true, true
	p.mu.Unlock()
	if on, _ := p.LayeredAssets(); on {
		t.Error("both bools set: layering must be inert, Local-only wins")
	}
	if enabled, _ := p.LocalAssets(); !enabled {
		t.Error("both bools set: Local-only must stay in force")
	}
}

// TestSetLocalAssetsRefusesGrowthButAllowsShrink pins the cap policy. A flat
// length rejection would brick any user who already has more mounts saved than
// the cap: the mode toggle, the Remove button and the downloader all push the
// WHOLE list through this setter, so they could never prune back under it.
func TestSetLocalAssetsRefusesGrowthButAllowsShrink(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	full := make([]string, LocalMountCap)
	for i := range full {
		full[i] = "/mnt/pack" + strconv.Itoa(i)
	}
	if !p.SetLocalAssets(false, full) {
		t.Fatal("a list exactly at the cap was refused")
	}
	if p.SetLocalAssets(false, append(append([]string{}, full...), "/mnt/one-too-many")) {
		t.Error("growing past the cap was accepted")
	}
	if _, got := p.LocalAssets(); len(got) != LocalMountCap {
		t.Errorf("the refused add mutated the list: %d entries", len(got))
	}

	// An over-cap list already on disk (or from an older build) must stay
	// editable: same-length calls and shrinks always succeed.
	over := append(append([]string{}, full...), "/mnt/legacy-extra")
	p.mu.Lock()
	p.LocalAssetsPaths = over
	p.mu.Unlock()
	if !p.SetLocalAssets(true, over) {
		t.Error("toggling the mode on an over-cap saved list was refused — the user is locked out")
	}
	if !p.SetLocalAssets(true, over[:len(over)-1]) {
		t.Error("REMOVING a mount from an over-cap list was refused — the user can never prune")
	}
}

// TestLayeredSurvivesSettingsReset pins that a Reset does not silently change
// which source a user's assets come from. Without LocalAssetsLayered in the
// preserved set, a reset would keep the mounts but drop the mode that reads
// them, which presents as "my pack stopped working" with the folders still
// listed in Settings.
func TestLayeredSurvivesSettingsReset(t *testing.T) {
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	mount := t.TempDir()
	p.SetLocalAssets(false, []string{mount})
	p.SetLayeredAssets(true)

	p.ResetSettings()

	on, mounts := p.LayeredAssets()
	if len(mounts) != 1 {
		t.Fatalf("the mount list did not survive the reset: %v", mounts)
	}
	if !on {
		t.Error("the mounts survived the reset but the mode that reads them did not")
	}
}

// TestLocalAssetsLayeredRoundTripsToDisk is the belt-and-braces companion to the
// reflective TestEverySavedPreferenceIsAlsoLoaded: it proves the field actually
// survives a real save/reload cycle, not just that both structs declare it.
func TestLocalAssetsLayeredRoundTripsToDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PrefsFileName)
	mount := t.TempDir()

	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	p.SetLocalAssets(false, []string{mount})
	p.SetLayeredAssets(true)
	if err := p.Close(); err != nil { // Close flushes the debounced saver
		t.Fatal(err)
	}

	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if on, mounts := reloaded.LayeredAssets(); !on || len(mounts) != 1 {
		t.Errorf("after reload: layered=%v mounts=%v, want true with 1 mount", on, mounts)
	}
}
