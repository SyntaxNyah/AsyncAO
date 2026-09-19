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
