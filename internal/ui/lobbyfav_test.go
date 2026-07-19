package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestLobbyStarRemoveNoPanic pins the saved-server-removal crash (Task C):
// drawLobby ranged over a.servers while drawServerRow's ★ handler called
// toggleFavorite → a.servers = a.mergedFavorites(), REPLACING the slice
// mid-iteration. Removing a phone-book-only server (one NOT in the master list)
// shrinks the new slice, so the loop's next a.servers[i] indexed past the new
// length → index-out-of-range panic → the process died. The fix snapshots the
// slice once at loop top; this drives a real ★-click on row 0 through the ctx
// (headless SDL) and asserts the draw survives and the list shrank by one.
//
// Geometry (must reproduce the OLD panic, so it needs 2+ favorites and a click
// on a NON-last row): with a.lobbyScroll==0 the rows start at listTop == dcY+40
// == (pad+56)+40 == 104 and step by rowH. Row 0's ★ rect is
// {X: pad+22, Y: listTop+2, W: 22, H: rowH-6} == {30, 106, 22, 16}; its centre
// (41, 114) sits inside row 0's star only (row 1's star is at Y 128+), so
// exactly one toggle fires.
func TestLobbyStarRemoveNoPanic(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("kit unavailable: %v", err)
	}
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{ctx: ctx, d: Deps{Prefs: prefs}}
	// Favorites-only list: neither URL is in the (empty) master list, so removing
	// one genuinely SHRINKS mergedFavorites() — the length-preserving master-list
	// case is exactly why only the saved-only removal used to crash. ws:// keeps
	// both rows Joinable (no legacy-header branch to shift the row geometry).
	a.d.Prefs.AddFavorite("Alpha", "ws://a.example:1001", "")
	a.d.Prefs.AddFavorite("Beta", "ws://b.example:1002", "")
	a.servers = a.mergedFavorites()
	if len(a.servers) != 2 {
		t.Fatalf("setup: want 2 favorites, got %d", len(a.servers))
	}
	before := len(a.servers)

	// Phone Book page (faithful to the bug report) with no selection/desc box.
	a.phoneBookPage = true
	a.selServer = -1

	// Point the mouse at row 0's ★ and arm a left-click release. The ★ handler is
	// a plain `hovering(star) && c.clicked` (not ClickedIn), so setting the
	// logical mouse + clicked directly is enough — no down/up event replay needed.
	ctx.mouseX, ctx.mouseY = 41, 114
	ctx.clicked = true

	// drawLobby has no internal recover (Frame's crash-log guard is above it and
	// not on this call path), so the index panic would propagate and fail here —
	// which is exactly the regression we want to catch. A tall+wide window keeps
	// both rows on-screen and the Phone Fanat chip far from the click.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("drawLobby panicked on ★-remove (saved-server-removal crash): %v", r)
			}
		}()
		a.drawLobby(1280, 720)
	}()

	if len(a.servers) != before-1 {
		t.Fatalf("★-remove must shrink the saved list by one: got %d, want %d", len(a.servers), before-1)
	}
}
