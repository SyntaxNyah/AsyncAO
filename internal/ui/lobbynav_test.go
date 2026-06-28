package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// TestNextJoinableServer pins lobby keyboard nav (#18): arrows skip legacy (non-joinable) rows,
// clamp at the ends (no wrap), and the Phone Book filter restricts movement to favourites.
func TestNextJoinableServer(t *testing.T) {
	a := testTabApp(t)
	a.servers = []network.ServerEntry{
		{Name: "A", WSPort: 1},                  // 0 joinable
		{Name: "Legacy"},                        // 1 legacy (no ports) — skipped
		{Name: "B", WSPort: 1, Favorite: true},  // 2 joinable + favourite
		{Name: "C", WSSPort: 1, Favorite: true}, // 3 joinable + favourite
	}

	// All servers: skip the legacy row, clamp at the ends.
	if got := a.nextJoinableServer(-1, 1); got != 0 {
		t.Errorf("down from none = %d, want 0", got)
	}
	if got := a.nextJoinableServer(0, 1); got != 2 {
		t.Errorf("down from 0 = %d, want 2 (skips legacy)", got)
	}
	if got := a.nextJoinableServer(3, 1); got != -1 {
		t.Errorf("down from last = %d, want -1 (no wrap)", got)
	}
	if got := a.nextJoinableServer(2, -1); got != 0 {
		t.Errorf("up from 2 = %d, want 0 (skips legacy)", got)
	}
	if got := a.nextJoinableServer(0, -1); got != -1 {
		t.Errorf("up from first = %d, want -1", got)
	}

	// Phone Book page: only favourites (idx 2, 3) are reachable.
	a.phoneBookPage = true
	if got := a.nextJoinableServer(-1, 1); got != 2 {
		t.Errorf("phonebook down from none = %d, want 2", got)
	}
	if got := a.nextJoinableServer(2, 1); got != 3 {
		t.Errorf("phonebook down from 2 = %d, want 3", got)
	}
	if got := a.nextJoinableServer(3, 1); got != -1 {
		t.Errorf("phonebook down from last fav = %d, want -1", got)
	}
}

// TestScrollServerIntoView pins the invariant: after scrolling to a row, that row is fully inside
// the [listTop, h] viewport (so keyboard nav never parks the selection off-screen).
func TestScrollServerIntoView(t *testing.T) {
	a := testTabApp(t)
	a.servers = []network.ServerEntry{
		{Name: "A", WSPort: 1}, {Name: "Legacy"}, {Name: "B", WSPort: 1},
		{Name: "C", WSPort: 1}, {Name: "D", WSPort: 1}, {Name: "E", WSPort: 1},
	}
	const listTop, h = int32(100), int32(220) // a short viewport so most rows are off-screen
	for _, idx := range []int{5, 0, 3} {      // jump around
		a.scrollServerIntoView(idx, listTop, h)
		// Recompute the row's content-space top the same way the helper does.
		top, legacy := int32(0), false
		for i := range a.servers {
			if e := &a.servers[i]; !e.Joinable() && !legacy {
				top += rowH
				legacy = true
			}
			if i == idx {
				break
			}
			top += rowH
		}
		onScreen := listTop + top - a.lobbyScroll
		if onScreen < listTop || onScreen+rowH > h {
			t.Errorf("row %d at on-screen y=%d not within [%d,%d] (scroll=%d)", idx, onScreen, listTop, h, a.lobbyScroll)
		}
	}
}
