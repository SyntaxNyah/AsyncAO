package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestRefreshJukeFavs pins the M12 favorites collection: starred songs from
// every playlist are gathered (in playlist/entry order), the count label is
// cached, and the list is memoized against the library revision.
func TestRefreshJukeFavs(t *testing.T) {
	a := testTabApp(t)
	// Build entries with field assignment for Fav (avoids the keyed-literal
	// "unknown field" build glitch noted in memory).
	a0 := config.JukeboxEntry{Title: "a0", URL: "u0"}
	a1 := config.JukeboxEntry{Title: "a1", URL: "u1"}
	a1.Fav = true
	b0 := config.JukeboxEntry{Title: "b0", URL: "u2"}
	b0.Fav = true
	a.jukeCache = []config.Playlist{
		{Name: "A", Entries: []config.JukeboxEntry{a0, a1}},
		{Name: "B", Entries: []config.JukeboxEntry{b0}},
	}
	a.jukeCacheRev = 1
	a.refreshJukeFavs()

	want := []favRef{{pl: 0, e: 1}, {pl: 1, e: 0}}
	if len(a.jukeFavs) != len(want) {
		t.Fatalf("favs = %+v, want %+v", a.jukeFavs, want)
	}
	for i, w := range want {
		if a.jukeFavs[i] != w {
			t.Errorf("fav %d = %+v, want %+v", i, a.jukeFavs[i], w)
		}
	}
	if a.jukeFavLbl != "★ Favorites (2)" {
		t.Errorf("label = %q, want \"★ Favorites (2)\"", a.jukeFavLbl)
	}

	// Memoized: an unchanged revision doesn't rebuild even though the data changed.
	a.jukeCache[0].Entries[0].Fav = true
	a.refreshJukeFavs()
	if len(a.jukeFavs) != 2 {
		t.Errorf("unchanged rev must not rebuild, got %d", len(a.jukeFavs))
	}
	// A revision bump rebuilds and picks up the new star.
	a.jukeCacheRev = 2
	a.refreshJukeFavs()
	if len(a.jukeFavs) != 3 {
		t.Errorf("rev bump should rebuild, got %d", len(a.jukeFavs))
	}
}
