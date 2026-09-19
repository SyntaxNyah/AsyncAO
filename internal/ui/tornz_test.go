package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestBringTornToFront pins the tear-off panel z-order: the default draw order
// is the table order, and a click inside a panel's rect promotes it to the front
// so it draws over the others (the Players-tab-always-on-top fix).
func TestBringTornToFront(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{d: Deps{Prefs: prefs}, ctx: &Ctx{}}
	a.classicOv = map[string][4]float64{
		"tab:music":   {0.1, 0.1, 0.2, 0.2},
		"tab:players": {0.3, 0.1, 0.2, 0.2},
	}

	if got := a.tornTabOrder(); got != [len(tornTabTable)]int{0, 1, 2, 3, 4} {
		t.Fatalf("default order = %v, want table order", got)
	}

	// A click inside the Players panel (index 2) promotes it to front.
	a.ctx.clicked = true
	a.ctx.mouseX, a.ctx.mouseY = 350, 120 // inside {300,80,200,160}
	a.bringTornToFront(1000, 800)

	got := a.tornTabOrder()
	if got[len(got)-1] != 2 {
		t.Errorf("after clicking Players the front panel is %d, want Players (2): %v", got[len(got)-1], got)
	}
}
