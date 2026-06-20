package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestRefreshEmoteView pins the favourites view (#77): every emote is visible by
// default, favs-only filters to the starred subset (in order), the fav set gives
// O(1) membership, and a steady-state rebuild allocates nothing (the per-frame
// guard that keeps the emote grid off the alloc budget).
func TestRefreshEmoteView(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{d: Deps{Prefs: prefs}}
	a.iniChar = "Apollo" // activeCharName() returns this with no session
	a.emotes = make([]courtroom.Emote, 5)

	// Default (favs-only OFF): every emote visible, in list order.
	a.refreshEmoteView()
	if len(a.emoteVisible) != 5 {
		t.Fatalf("default visible = %d, want 5", len(a.emoteVisible))
	}
	for i, v := range a.emoteVisible {
		if v != i {
			t.Fatalf("visible[%d] = %d, want %d", i, v, i)
		}
	}

	// Favourite emotes 1 and 3 and switch on favs-only: only those two show.
	prefs.ToggleEmoteFav("Apollo", 1)
	prefs.ToggleEmoteFav("Apollo", 3)
	prefs.SetEmoteFavOnly(true)
	a.emoteFavRev++ // mirror the UI's invalidation when a favourite/filter changes
	a.refreshEmoteView()
	if got := a.emoteVisible; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("favs-only visible = %v, want [1 3]", got)
	}
	if got := a.favBoxList; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("favBoxList = %v, want [1 3]", got)
	}
	if _, ok := a.emoteFavSet[1]; !ok {
		t.Error("emoteFavSet should contain emote 1")
	}
	if _, ok := a.emoteFavSet[0]; ok {
		t.Error("emoteFavSet should not contain emote 0")
	}

	// Steady state: nothing changed, so the guard short-circuits with no work.
	if n := testing.AllocsPerRun(100, func() { a.refreshEmoteView() }); n != 0 {
		t.Errorf("steady-state refreshEmoteView allocs/op = %v, want 0", n)
	}

	// emotePageOf maps a real index to its page within the visible list (-1 when
	// the index isn't visible — e.g. a non-favourite while favs-only is on).
	a.emotePerPage = 10
	if p := a.emotePageOf(3); p != 0 {
		t.Errorf("emotePageOf(3) = %d, want 0", p)
	}
	if p := a.emotePageOf(0); p != -1 {
		t.Errorf("emotePageOf(0) = %d, want -1 (emote 0 is filtered out)", p)
	}

	// favBoxList holds the favourites regardless of the grid filter: with
	// favs-only OFF the grid shows everything but the box still shows just [1 3].
	prefs.SetEmoteFavOnly(false)
	a.emoteFavRev++
	a.refreshEmoteView()
	if len(a.emoteVisible) != 5 {
		t.Fatalf("favs-only off visible = %d, want 5 (all)", len(a.emoteVisible))
	}
	if got := a.favBoxList; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("favBoxList with filter off = %v, want [1 3]", got)
	}
}
