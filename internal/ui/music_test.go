package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestLogMusicChange pins the IC "has played a song" line (webAO/AO2 parity):
// the packet showname names the player, an absent showname falls back to the
// character, system/area changes (no valid charID) and area-name transfers log
// nothing, and ~stop reads as "has stopped the music".
func TestLogMusicChange(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix", "Maya"})

	a.logMusicChange(courtroom.Event{Kind: courtroom.EventMusic, Text: "Trial.opus", Int: 1, Name: "Mystic Maya"})
	if len(a.icLog) != 1 {
		t.Fatalf("icLog = %d, want 1 music line", len(a.icLog))
	}
	if a.icLog[0].text != "Mystic Maya has played a song: Trial" {
		t.Errorf("music line = %q, want 'Mystic Maya has played a song: Trial'", a.icLog[0].text)
	}

	a.logMusicChange(courtroom.Event{Kind: courtroom.EventMusic, Text: "Cross.opus", Int: 0}) // no showname → char name
	if a.icLog[1].text != "Phoenix has played a song: Cross" {
		t.Errorf("fallback line = %q, want 'Phoenix has played a song: Cross'", a.icLog[1].text)
	}

	a.logMusicChange(courtroom.Event{Kind: courtroom.EventMusic, Text: "Theme.opus", Int: -1})  // system: no charID
	a.logMusicChange(courtroom.Event{Kind: courtroom.EventMusic, Text: "Pizza Room 3", Int: 1}) // area transfer
	if len(a.icLog) != 2 {
		t.Errorf("system music + area transfer must not log, icLog = %d, want 2", len(a.icLog))
	}

	a.logMusicChange(courtroom.Event{Kind: courtroom.EventMusic, Text: "~stop.mp3", Int: 1})
	if a.icLog[2].text != "Maya has stopped the music" {
		t.Errorf("stop line = %q, want 'Maya has stopped the music'", a.icLog[2].text)
	}
}
