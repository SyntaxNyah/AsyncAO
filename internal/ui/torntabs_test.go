package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// A zero App is enough: the tear-off logic touches only classicOv.

func TestTornKeyFor(t *testing.T) {
	cases := map[int]string{
		logTabMusic:   "tab:music",
		logTabAreas:   "tab:areas",
		logTabPlayers: "tab:players",
		logTabNotes:   "tab:notes",
		logTabFriends: "tab:friends",
		logTabLog:     "", // home / fallback tab — never tear-offable
		logTabOOC:     "", // its own box (default) / legacy tab — not tear-offable
	}
	for id, want := range cases {
		if got := tornKeyFor(id); got != want {
			t.Errorf("tornKeyFor(%d) = %q, want %q", id, got, want)
		}
	}
}

func TestTabTorn(t *testing.T) {
	var a App
	if a.tabTorn(logTabMusic) {
		t.Fatal("no overrides: music must not read as torn")
	}
	a.classicOv = map[string][4]float64{"tab:music": {0.1, 0.1, 0.3, 0.3}}
	if !a.tabTorn(logTabMusic) {
		t.Fatal("tab:music present: music must read as torn")
	}
	if a.tabTorn(logTabAreas) {
		t.Fatal("only music torn: areas must not read as torn")
	}
	if a.tabTorn(logTabLog) {
		t.Fatal("Log is never tear-offable")
	}
}

func TestDockedLogTabs(t *testing.T) {
	// Nothing torn, new default (OOC is its own box): Log+Music+Areas+Players+Notes+Friends.
	var a App
	d, n := a.dockedLogTabs(false)
	if n != 6 {
		t.Fatalf("default docked count = %d, want 6", n)
	}
	if d[0].id != logTabLog {
		t.Fatalf("first docked tab = %d, want Log", d[0].id)
	}

	// Legacy keeps the OOC tab in the strip.
	if _, n := a.dockedLogTabs(true); n != 7 {
		t.Fatalf("legacy docked count = %d, want 7", n)
	}

	// Tear Music + Players out → they leave the strip and the rest compact.
	a.classicOv = map[string][4]float64{
		"tab:music":   {0.1, 0.1, 0.3, 0.3},
		"tab:players": {0.4, 0.1, 0.3, 0.3},
	}
	d, n = a.dockedLogTabs(false)
	if n != 4 {
		t.Fatalf("two torn: docked count = %d, want 4", n)
	}
	for i := int32(0); i < n; i++ {
		if d[i].id == logTabMusic || d[i].id == logTabPlayers {
			t.Fatalf("torn tab %d still in the docked strip", d[i].id)
		}
	}

	// Every tear-offable tab torn → only Log remains (new default).
	a.classicOv = map[string][4]float64{
		"tab:music": {0, 0, .1, .1}, "tab:areas": {0, 0, .1, .1},
		"tab:players": {0, 0, .1, .1}, "tab:notes": {0, 0, .1, .1},
		"tab:friends": {0, 0, .1, .1},
	}
	if d, n := a.dockedLogTabs(false); n != 1 || d[0].id != logTabLog {
		t.Fatalf("all torn: got count=%d first=%d, want 1 / Log", n, d[0].id)
	}
}

// TestDockedLogTabsNoAlloc pins the hard rule: building the docked strip on the
// render path allocates nothing, even with a tab torn out (the worst case).
func TestDockedLogTabsNoAlloc(t *testing.T) {
	var a App
	a.classicOv = map[string][4]float64{"tab:music": {0.1, 0.1, 0.3, 0.3}}
	if n := testing.AllocsPerRun(200, func() {
		_, _ = a.dockedLogTabs(false)
		_, _ = a.dockedLogTabs(true)
	}); n != 0 {
		t.Fatalf("dockedLogTabs allocates %v/op on the render path; want 0", n)
	}
}

func TestTornTabRect(t *testing.T) {
	var a App
	const w, h = 1000, 800
	if _, ok := a.tornTabRect("tab:music", w, h); ok {
		t.Fatal("no override: tornTabRect must report not-torn")
	}
	a.classicOv = map[string][4]float64{"tab:music": {0.5, 0.25, 0.2, 0.5}}
	r, ok := a.tornTabRect("tab:music", w, h)
	if !ok {
		t.Fatal("override present: tornTabRect must report torn")
	}
	want := sdl.Rect{X: 500, Y: 200, W: 200, H: 400} // 0.5*1000, 0.25*800, 0.2*1000, 0.5*800
	if r != want {
		t.Fatalf("tornTabRect = %+v, want %+v", r, want)
	}
}
