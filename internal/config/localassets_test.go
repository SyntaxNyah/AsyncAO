package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The asset source is three independent switches: localAssetsLayered is "Use my folders and packs",
// localAssetsEnabled is "Stream from the web" switched off, and packagesOff leaves the packages folder out. These
// tests pin their independence, the cap policy, the reset behaviour and the migration from the three old modes.

func newLocalPrefs(t *testing.T) *AssetPreferences {
	t.Helper()
	p, err := New(filepath.Join(t.TempDir(), PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// TestFolderAndStreamSwitchesAreIndependent: switching folders on or off never touches streaming, and back.
func TestFolderAndStreamSwitchesAreIndependent(t *testing.T) {
	p := newLocalPrefs(t)
	mount := t.TempDir()

	p.SetLocalAssets(true, []string{mount}) // streaming off
	p.SetLayeredAssets(false)               // folders off
	if streamOff, _ := p.LocalAssets(); !streamOff {
		t.Error("switching folders off turned streaming back on")
	}
	p.SetLayeredAssets(true)
	if streamOff, _ := p.LocalAssets(); !streamOff {
		t.Error("switching folders on turned streaming back on")
	}
	if !p.SetLocalMounts([]string{mount, t.TempDir()}) {
		t.Fatal("adding a folder was refused")
	}
	if streamOff, mounts := p.LocalAssets(); !streamOff || len(mounts) != 2 || !p.Load().LocalAssetsLayered {
		t.Errorf("SetLocalMounts changed a switch: streamOff=%v folders=%v mounts=%d", streamOff, p.Load().LocalAssetsLayered, len(mounts))
	}
}

// TestSetLocalAssetsRefusesGrowthButAllowsShrink pins the cap policy. A flat length rejection would brick any user
// who already has more mounts saved than the cap, since every edit pushes the whole list through this setter.
func TestSetLocalAssetsRefusesGrowthButAllowsShrink(t *testing.T) {
	p := newLocalPrefs(t)
	full := make([]string, LocalMountCap)
	for i := range full {
		full[i] = "/mnt/pack" + strconv.Itoa(i)
	}
	if !p.SetLocalAssets(false, full) {
		t.Fatal("a list exactly at the cap was refused")
	}
	if p.SetLocalMounts(append(append([]string{}, full...), "/mnt/one-too-many")) {
		t.Error("growing past the cap was accepted")
	}
	if _, got := p.LocalAssets(); len(got) != LocalMountCap {
		t.Errorf("the refused add mutated the list: %d entries", len(got))
	}

	// An over-cap list already on disk must stay editable: same-length calls and shrinks always succeed.
	over := append(append([]string{}, full...), "/mnt/legacy-extra")
	p.Update(func(sn *Settings) { sn.LocalAssetsPaths = over })
	if !p.SetLocalAssets(true, over) {
		t.Error("switching streaming on an over-cap saved list was refused, which locks the user out")
	}
	if !p.SetLocalMounts(over[:len(over)-1]) {
		t.Error("removing a mount from an over-cap list was refused, so the user can never prune")
	}
}

// TestSwitchesSurviveSettingsReset: a Reset keeps the folders and all three switches, or the folders stay listed
// while nothing reads them, which reads as "my pack stopped working".
func TestSwitchesSurviveSettingsReset(t *testing.T) {
	p := newLocalPrefs(t)
	p.SetLocalAssets(true, []string{t.TempDir()})
	p.SetLayeredAssets(true)
	p.Update(func(sn *Settings) { sn.PackagesOff = true })

	p.ResetSettings()

	snap := p.Load()
	if len(snap.LocalAssetsPaths) != 1 || !snap.LocalAssetsLayered || !snap.LocalAssetsEnabled || !snap.PackagesOff {
		t.Errorf("after reset: mounts=%v folders=%v streamOff=%v packagesOff=%v, want all kept",
			snap.LocalAssetsPaths, snap.LocalAssetsLayered, snap.LocalAssetsEnabled, snap.PackagesOff)
	}
}

// TestFolderSwitchRoundTripsToDisk: the folder switch survives a real save and reload.
func TestFolderSwitchRoundTripsToDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	p.SetLocalAssets(false, []string{t.TempDir()})
	p.SetLayeredAssets(true)
	if err := p.Close(); err != nil { // Close flushes the debounced saver
		t.Fatal(err)
	}
	reloaded, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if !reloaded.Load().LocalAssetsLayered {
		t.Error("the folder switch did not survive a reload")
	}
}

// TestOldLocalOnlyModeKeepsItsFolders: "Only use my folders" stored the no-streaming flag alone, so the migration
// switches folders on for it once; a file that already has the stamp is taken as it is.
func TestOldLocalOnlyModeKeepsItsFolders(t *testing.T) {
	const fileMode = 0o600
	for _, tc := range []struct {
		name, file  string
		wantFolders bool
	}{
		{"old local-only mode", `{"localAssetsEnabled": true, "localAssetsPaths": ["x"]}`, true},
		{"old stream mode", `{"localAssetsPaths": ["x"]}`, false},
		{"new: packages only, no stream", `{"assetSwitchesMigrated": true, "localAssetsEnabled": true, "localAssetsPaths": ["x"]}`, false},
	} {
		path := filepath.Join(t.TempDir(), PrefsFileName)
		if err := os.WriteFile(path, []byte(tc.file), fileMode); err != nil {
			t.Fatal(err)
		}
		p, err := load(path)
		if err != nil {
			t.Fatalf("%s: load: %v", tc.name, err)
		}
		if got := p.Load().LocalAssetsLayered; got != tc.wantFolders {
			t.Errorf("%s: folders switch = %v, want %v", tc.name, got, tc.wantFolders)
		}
	}
}
