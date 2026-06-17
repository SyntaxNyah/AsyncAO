package ui

import (
	"fmt"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestNoteMusicHistory pins the session "recently played" ring: real songs are
// captured newest-first with a precomputed "<name> — <by>" label, a DJ URL is
// flagged isURL, stop/area-transfer events are skipped, and replaying a track
// moves it to the front without duplicating (MRU keyed by raw track).
func TestNoteMusicHistory(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix", "Maya"})

	// A plain server song: attributed via the MC showname, name cleaned, not a URL.
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "Trial.opus", Int: 1, Name: "Mystic Maya"})
	if len(a.musicHist) != 1 {
		t.Fatalf("history = %d, want 1", len(a.musicHist))
	}
	if e := a.musicHist[0]; e.track != "Trial.opus" || e.name != "Trial" || e.display != "Trial — Mystic Maya" || e.isURL {
		t.Errorf("entry = %+v, want {track:Trial.opus name:Trial display:'Trial — Mystic Maya' isURL:false}", e)
	}

	// A DJ /play URL: newest first, flagged isURL, "by" falls back to the char.
	url := "https://miku.pizza/base/sounds/music/Cross.opus?ex=1&is=2&hm=3&"
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: url, Int: 0})
	if len(a.musicHist) != 2 || a.musicHist[0].track != url {
		t.Fatalf("after URL: %d entries, front=%q", len(a.musicHist), a.musicHist[0].track)
	}
	if e := a.musicHist[0]; !e.isURL || e.name != "Cross" || e.display != "Cross — Phoenix" {
		t.Errorf("url entry = %+v, want isURL with name Cross by Phoenix", e)
	}

	// Stop sentinel + area-name transfer are not songs.
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "~stop.mp3", Int: 1})
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "Pizza Room 3", Int: 1})
	if len(a.musicHist) != 2 {
		t.Errorf("stop/area must not record, history = %d, want 2", len(a.musicHist))
	}

	// Replaying an existing track moves it to the front (MRU), no duplicate.
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "Trial.opus", Int: 1, Name: "Mystic Maya"})
	if len(a.musicHist) != 2 || a.musicHist[0].track != "Trial.opus" {
		t.Errorf("MRU: history = %d, front = %q, want Trial.opus at front with no dupe", len(a.musicHist), a.musicHist[0].track)
	}
}

// TestNoteMusicHistoryCap pins the bound: the ring never exceeds the cap and
// keeps the newest entries (oldest fall off the end).
func TestNoteMusicHistoryCap(t *testing.T) {
	a := testTabApp(t)
	for i := 0; i < musicHistoryCap+8; i++ {
		a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: fmt.Sprintf("Song%d.opus", i)})
	}
	if len(a.musicHist) != musicHistoryCap {
		t.Fatalf("history = %d, want cap %d", len(a.musicHist), musicHistoryCap)
	}
	if want := fmt.Sprintf("Song%d", musicHistoryCap+7); a.musicHist[0].name != want {
		t.Errorf("front = %q, want newest %q", a.musicHist[0].name, want)
	}
}

// TestNoteMusicHistoryDisabled pins the Settings toggle: with the history pref
// off, nothing is captured (the default is ON, so it normally records).
func TestNoteMusicHistoryDisabled(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.d.Prefs.SetMusicHistory(false)
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "Trial.opus", Int: 0})
	if len(a.musicHist) != 0 {
		t.Errorf("history disabled but captured %d entries", len(a.musicHist))
	}
}

// TestNoteMusicHistoryNoSession pins crash-safety: no session + an out-of-range
// charID must not panic, and "by" is simply left blank.
func TestNoteMusicHistoryNoSession(t *testing.T) {
	a := testTabApp(t)
	a.sess = nil
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "Solo.opus", Int: 5})
	if len(a.musicHist) != 1 || a.musicHist[0].display != "Solo" {
		t.Fatalf("entry = %+v, want a single entry with display 'Solo'", a.musicHist)
	}
}
