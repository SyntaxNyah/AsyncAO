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
	a.jukebox.jukeCache = []config.Playlist{
		{Name: "A", Entries: []config.JukeboxEntry{a0, a1}},
		{Name: "B", Entries: []config.JukeboxEntry{b0}},
	}
	a.jukebox.jukeCacheRev = 1
	a.jukebox.refreshJukeFavs()

	want := []favRef{{pl: 0, e: 1}, {pl: 1, e: 0}}
	if len(a.jukebox.jukeFavs) != len(want) {
		t.Fatalf("favs = %+v, want %+v", a.jukebox.jukeFavs, want)
	}
	for i, w := range want {
		if a.jukebox.jukeFavs[i] != w {
			t.Errorf("fav %d = %+v, want %+v", i, a.jukebox.jukeFavs[i], w)
		}
	}
	if a.jukebox.jukeFavLbl != "★ Favorites (2)" {
		t.Errorf("label = %q, want \"★ Favorites (2)\"", a.jukebox.jukeFavLbl)
	}

	// Memoized: an unchanged revision doesn't rebuild even though the data changed.
	a.jukebox.jukeCache[0].Entries[0].Fav = true
	a.jukebox.refreshJukeFavs()
	if len(a.jukebox.jukeFavs) != 2 {
		t.Errorf("unchanged rev must not rebuild, got %d", len(a.jukebox.jukeFavs))
	}
	// A revision bump rebuilds and picks up the new star.
	a.jukebox.jukeCacheRev = 2
	a.jukebox.refreshJukeFavs()
	if len(a.jukebox.jukeFavs) != 3 {
		t.Errorf("rev bump should rebuild, got %d", len(a.jukebox.jukeFavs))
	}
}
