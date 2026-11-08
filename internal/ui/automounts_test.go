package ui

import (
	"reflect"
	"testing"
)

// TestLocalAssetsAppendsAutoMounts pins that the effective local mount list is
// manual-then-auto, so a user's explicit folder always wins over a packages/
// folder (issue #128).
func TestLocalAssetsAppendsAutoMounts(t *testing.T) {
	a := testTabApp(t)
	mount := t.TempDir()
	pkg := t.TempDir()
	if !a.d.Prefs.SetLocalAssets(true, []string{mount}) {
		t.Fatal("mount rejected")
	}
	a.d.AutoMounts = []string{pkg}

	enabled, mounts := a.localAssets()
	if !enabled {
		t.Error("local mode must stay enabled")
	}
	if want := []string{mount, pkg}; !reflect.DeepEqual(mounts, want) {
		t.Fatalf("localAssets = %v, want %v", mounts, want)
	}
}

// TestLayeredAssetsAppendsAutoMounts pins the layered-mode twin of the above.
func TestLayeredAssetsAppendsAutoMounts(t *testing.T) {
	a := testTabApp(t)
	mount := t.TempDir()
	pkg := t.TempDir()
	if !a.d.Prefs.SetLocalAssets(false, []string{mount}) {
		t.Fatal("mount rejected")
	}
	a.d.Prefs.SetLayeredAssets(true)
	a.d.AutoMounts = []string{pkg}

	on, mounts := a.layeredAssets()
	if !on {
		t.Fatal("layered mode must stay on")
	}
	if want := []string{mount, pkg}; !reflect.DeepEqual(mounts, want) {
		t.Fatalf("layeredAssets = %v, want %v", mounts, want)
	}
}

// TestPackagesAloneCanBeUsed: with no folders of your own, packages still count as something local to read, so the
// folder and stream switches can be turned on.
func TestPackagesAloneCanBeUsed(t *testing.T) {
	a := testTabApp(t)
	if a.haveLocalSources() {
		t.Fatal("nothing local, but haveLocalSources said yes")
	}
	a.d.AutoMounts = []string{t.TempDir()}
	if !a.haveLocalSources() {
		t.Error("packages alone don't count as a local source, so their switches stay greyed out")
	}
}

// TestPackagesSwitchLeavesThemOut: switched off, packages leave the search order and your folders stay.
func TestPackagesSwitchLeavesThemOut(t *testing.T) {
	a := testTabApp(t)
	mount, pkg := t.TempDir(), t.TempDir()
	if !a.d.Prefs.SetLocalAssets(false, []string{mount}) {
		t.Fatal("mount rejected")
	}
	a.d.Prefs.SetLayeredAssets(true)
	a.d.AutoMounts = []string{pkg}

	a.setPackagesInUse(false, []string{mount})
	if _, mounts := a.layeredAssets(); !reflect.DeepEqual(mounts, []string{mount}) {
		t.Errorf("with packages off, layeredAssets = %v, want only %v", mounts, []string{mount})
	}
	a.setPackagesInUse(true, []string{mount})
	if _, mounts := a.layeredAssets(); !reflect.DeepEqual(mounts, []string{mount, pkg}) {
		t.Errorf("with packages back on, layeredAssets = %v", mounts)
	}
}

// TestPackagesOffNeverLeavesNothingToRead: in "only my folders" mode with packages as the only source, switching
// them off turns streaming back on.
func TestPackagesOffNeverLeavesNothingToRead(t *testing.T) {
	a := testTabApp(t)
	a.d.AutoMounts = []string{t.TempDir()}
	a.setAssetSourceMode(assetSrcLocal, nil)

	a.setPackagesInUse(false, nil)
	if got := a.assetSourceMode(); got != assetSrcStream {
		t.Errorf("mode = %d after the last local source went away, want streaming (%d)", got, assetSrcStream)
	}
}
