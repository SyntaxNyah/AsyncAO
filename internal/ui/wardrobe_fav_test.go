package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestApplyPendingFavKeepsSlicesConsistent pins the wardrobe ★ crash fix. The bug:
// toggling a ★ inside the grid cell rebuilt (and SHRANK) the parallel iniList /
// iniWardrobe / iniFolders / iniLower slices the loop was ranging, so a REMOVE walked
// off the end of the now-shorter slices and panicked. The toggle is now DEFERRED to
// applyPendingFav (run after the loop, in drawIniswapPanel); this verifies that path
// keeps the four parallel slices the same length through a remove, an add, and a
// no-op.
func TestApplyPendingFavKeepsSlicesConsistent(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}
	a.serverKey = "s"
	for _, n := range []string{"Phoenix", "Edgeworth", "Maya"} {
		prefs.AddWardrobe("s", n)
	}
	a.rebuildIniMenu()

	assertParallel := func(where string, wantLen int) {
		t.Helper()
		n := len(a.iniList)
		if n != wantLen {
			t.Fatalf("%s: iniList = %d, want %d", where, n, wantLen)
		}
		if len(a.iniWardrobe) != n || len(a.iniFolders) != n || len(a.iniLower) != n {
			t.Fatalf("%s: parallel slices out of sync — list=%d wardrobe=%d folders=%d lower=%d",
				where, n, len(a.iniWardrobe), len(a.iniFolders), len(a.iniLower))
		}
	}
	assertParallel("after build", 3)

	// Defer-and-apply a REMOVE of the LAST entry — the index that used to walk past
	// the end of the shrunk slices.
	a.iniFavPending, a.iniFavPendingAdd = a.iniList[len(a.iniList)-1], false
	a.applyPendingFav()
	assertParallel("after remove", 2)
	if a.iniFavPending != "" {
		t.Error("pending fav must be cleared after apply")
	}

	// An ADD grows it back, still consistent; an empty pending is a no-op.
	a.iniFavPending, a.iniFavPendingAdd = "Franziska", true
	a.applyPendingFav()
	assertParallel("after add", 3)
	a.applyPendingFav()
	assertParallel("after no-op", 3)
}
