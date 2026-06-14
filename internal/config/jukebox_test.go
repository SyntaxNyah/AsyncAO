package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func newTestJukebox(t *testing.T) *Jukebox {
	t.Helper()
	j := &Jukebox{path: filepath.Join(t.TempDir(), JukeboxFileName)}
	t.Cleanup(func() { _ = j.Close() }) // stop the debounce timer before TempDir cleanup
	return j
}

func TestJukeboxRoundTrip(t *testing.T) {
	j := newTestJukebox(t)
	if !j.AddPlaylist("Battle Themes") {
		t.Fatal("AddPlaylist failed")
	}
	if !j.AddEntry(0, "Pursuit", "https://youtu.be/abc") {
		t.Fatal("AddEntry failed")
	}
	j.AddEntry(0, "", "https://cdn.discord/xyz.mp3") // title optional
	j.SetEntryKey(0, 0, "X")                         // normalizes to lowercase
	j.SetPlaylistKey(0, "P")
	if err := j.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	got := loadJukeboxFile(j.path).Playlists()
	if len(got) != 1 || got[0].Name != "Battle Themes" || len(got[0].Entries) != 2 {
		t.Fatalf("reloaded = %+v", got)
	}
	if got[0].Key != "p" || got[0].Entries[0].Key != "x" {
		t.Errorf("keys not persisted/normalized: %+v", got[0])
	}
	if got[0].Entries[0].URL != "https://youtu.be/abc" || got[0].Entries[0].Title != "Pursuit" {
		t.Errorf("entry 0 = %+v", got[0].Entries[0])
	}
}

func TestJukeboxRejectsEmptyURL(t *testing.T) {
	j := newTestJukebox(t)
	j.AddPlaylist("P")
	if j.AddEntry(0, "title", "   ") {
		t.Error("empty URL must be rejected")
	}
	if j.AddEntry(5, "title", "https://x") {
		t.Error("out-of-range playlist index must be rejected")
	}
	if got := j.TotalEntries(); got != 0 {
		t.Errorf("nothing should have been added, got %d", got)
	}
}

func TestJukeboxPlaylistCap(t *testing.T) {
	j := newTestJukebox(t)
	for i := 0; i < jukeboxMaxPlaylists; i++ {
		if !j.AddPlaylist("p") {
			t.Fatalf("AddPlaylist %d failed before cap", i)
		}
	}
	if j.AddPlaylist("over") {
		t.Errorf("AddPlaylist exceeded cap %d", jukeboxMaxPlaylists)
	}
}

func TestJukeboxSanitizeCapsAndClamps(t *testing.T) {
	// Total-entry cap across playlists, plus length clamps.
	in := []Playlist{
		{Name: strings.Repeat("n", jukeboxMaxNameLen+50), Entries: []JukeboxEntry{
			{URL: "https://ok", Title: strings.Repeat("t", jukeboxMaxTitleLen+50)},
			{URL: "   "}, // dropped
		}},
	}
	for i := 0; i < jukeboxMaxEntries+5; i++ {
		in[0].Entries = append(in[0].Entries, JukeboxEntry{URL: "https://x"})
	}
	out := sanitizePlaylists(in)
	total := 0
	for _, pl := range out {
		total += len(pl.Entries)
		if len(pl.Name) > jukeboxMaxNameLen {
			t.Errorf("name not clamped: %d", len(pl.Name))
		}
		if len(pl.Entries[0].Title) > jukeboxMaxTitleLen {
			t.Errorf("title not clamped: %d", len(pl.Entries[0].Title))
		}
	}
	if total > jukeboxMaxEntries {
		t.Errorf("total entries %d exceeded cap %d", total, jukeboxMaxEntries)
	}
}

func TestJukeboxResolveKeyEntryWinsOverPlaylist(t *testing.T) {
	j := newTestJukebox(t)
	j.AddPlaylist("Shuffle me") // index 0
	j.AddEntry(0, "s0", "https://shuffle-only")
	j.SetPlaylistKey(0, "k") // playlist shuffle bind on "k"

	j.AddPlaylist("Has a bound song") // index 1
	j.AddEntry(1, "specific", "https://specific")
	j.SetEntryKey(1, 0, "k") // entry bind ALSO on "k" — must win

	if url, ok := j.ResolveKey("k"); !ok || url != "https://specific" {
		t.Errorf("entry bind must win: got %q ok=%v", url, ok)
	}
	if _, ok := j.ResolveKey("nope"); ok {
		t.Error("unbound key must not resolve")
	}
}

func TestJukeboxShuffleInRange(t *testing.T) {
	j := newTestJukebox(t)
	j.AddPlaylist("P")
	urls := map[string]bool{}
	for i := 0; i < 5; i++ {
		u := "https://song" + string(rune('a'+i))
		j.AddEntry(0, "", u)
		urls[u] = true
	}
	for i := 0; i < 50; i++ {
		if u, ok := j.Shuffle(0); !ok || !urls[u] {
			t.Fatalf("Shuffle returned out-of-set url %q ok=%v", u, ok)
		}
		if u, ok := j.ShuffleAll(); !ok || !urls[u] {
			t.Fatalf("ShuffleAll returned out-of-set url %q ok=%v", u, ok)
		}
	}
	if _, ok := j.Shuffle(9); ok {
		t.Error("Shuffle on a bad index must report ok=false")
	}
}

func TestJukeboxClear(t *testing.T) {
	j := newTestJukebox(t)
	j.AddPlaylist("P")
	j.AddEntry(0, "", "https://x")
	j.Clear()
	if j.PlaylistCount() != 0 || j.TotalEntries() != 0 {
		t.Errorf("Clear left data: %d playlists, %d entries", j.PlaylistCount(), j.TotalEntries())
	}
}
