package ui

import (
	"errors"
	"slices"
	"testing"
)

func TestMountPickLabelMatchesThePlatform(t *testing.T) {
	if got, want := mountPickLabel(true), "Add folders…"; got != want {
		t.Errorf("many: got %q, want %q", got, want)
	}
	if got, want := mountPickLabel(false), "Add folder…"; got != want {
		t.Errorf("one: got %q, want %q", got, want)
	}
}

func TestMergeMountsSkipsDuplicatesAndStopsAtTheLimit(t *testing.T) {
	const limit = 3
	next, added, refused := mergeMounts([]string{"a", "b"}, []string{"b", "c", "d"}, limit)
	if want := []string{"a", "b", "c"}; !slices.Equal(next, want) {
		t.Errorf("next = %v, want %v", next, want)
	}
	if added != 1 || refused != 1 {
		t.Errorf("added %d, refused %d; want 1 and 1", added, refused)
	}
}

func TestPollMountPickAddsTheChosenFolders(t *testing.T) {
	a := expApp(t)
	a.d.Prefs.SetLocalAssets(false, nil)
	t.Cleanup(func() { settings.mountPickBusy = false })

	settings.mountPickBusy = true
	settings.mountPickRes <- mountPick{paths: []string{"one", "two"}}
	a.pollMountPick()
	if settings.mountPickBusy {
		t.Error("the picker still counts as open")
	}
	if _, got := a.d.Prefs.LocalAssets(); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("mounts = %v", got)
	}

	for _, pick := range []mountPick{{}, {err: errors.New("no portal")}} {
		settings.mountPickRes <- pick
		a.pollMountPick()
		if _, got := a.d.Prefs.LocalAssets(); !slices.Equal(got, []string{"one", "two"}) {
			t.Errorf("%+v changed the mounts to %v", pick, got)
		}
	}
}
