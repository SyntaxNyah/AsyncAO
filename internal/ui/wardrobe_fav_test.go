package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestApplyPendingFavKeepsSlicesConsistent pins the wardrobe ★ crash fix. The bug:
func TestApplyPendingFavKeepsSlicesConsistent(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{env: env{d: Deps{Prefs: prefs}}}
	a.srvMeta.serverKey = "s"
	for _, n := range []string{"Phoenix", "Edgeworth", "Maya"} {
		prefs.AddWardrobe("s", n)
	}
	a.rebuildIniMenu()

	assertParallel := func(where string, wantLen int) {
		t.Helper()
		n := len(a.inibrowse.iniList)
		if n != wantLen {
			t.Fatalf("%s: iniList = %d, want %d", where, n, wantLen)
		}
		if len(a.inibrowse.iniWardrobe) != n || len(a.inibrowse.iniFolders) != n || len(a.inibrowse.iniLower) != n {
			t.Fatalf("%s: parallel slices out of sync — list=%d wardrobe=%d folders=%d lower=%d",
				where, n, len(a.inibrowse.iniWardrobe), len(a.inibrowse.iniFolders), len(a.inibrowse.iniLower))
		}
	}
	assertParallel("after build", 3)

	// Defer-and-apply a REMOVE of the LAST entry — the index that used to walk past
	// the end of the shrunk slices.
	a.inibrowse.iniFavPending, a.inibrowse.iniFavPendingAdd = a.inibrowse.iniList[len(a.inibrowse.iniList)-1], false
	a.applyPendingFav()
	assertParallel("after remove", 2)
	if a.inibrowse.iniFavPending != "" {
		t.Error("pending fav must be cleared after apply")
	}

	// An ADD grows it back, still consistent; an empty pending is a no-op.
	a.inibrowse.iniFavPending, a.inibrowse.iniFavPendingAdd = "Franziska", true
	a.applyPendingFav()
	assertParallel("after add", 3)
	a.applyPendingFav()
	assertParallel("after no-op", 3)
}
