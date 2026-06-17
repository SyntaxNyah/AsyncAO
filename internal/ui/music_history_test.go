package ui

import (
	"fmt"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestNoteMusicHistory pins the session "recently played" ring with the domain
// allowlist: only /play links from allowlisted "unique" hosts are recorded
// (newest-first, deduped, with a precomputed "<name> — <by>" label). Server
// songs (bare names) and non-allowlisted hosts (e.g. the server's own host)
// still play but are NOT recorded; Discord records only audio files.
func TestNoteMusicHistory(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix", "Maya"})

	// A catbox link (allowlisted): recorded, attributed via the MC showname.
	cat := "https://files.catbox.moe/ab12cd.mp3"
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: cat, Int: 1, Name: "Mystic Maya"})
	if len(a.musicHist) != 1 {
		t.Fatalf("history = %d, want 1", len(a.musicHist))
	}
	if e := a.musicHist[0]; e.track != cat || e.name != "ab12cd" || e.display != "ab12cd — Mystic Maya" || !e.isURL {
		t.Errorf("entry = %+v, want catbox song by Mystic Maya", e)
	}

	// A bare server-song name (no host) and a non-allowlisted server host both
	// play but are not recorded.
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "Trial.opus", Int: 0})
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "https://miku.pizza/base/sounds/music/Cross.opus?ex=1", Int: 0})
	if len(a.musicHist) != 1 {
		t.Errorf("bare name + non-allowlisted host must not record, history = %d, want 1", len(a.musicHist))
	}

	// Discord: an audio file records (front), a non-audio attachment does not.
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "https://cdn.discordapp.com/attachments/1/2/song.opus", Int: 0})
	if len(a.musicHist) != 2 || a.musicHist[0].name != "song" {
		t.Fatalf("discord audio file should record at front, history = %+v", a.musicHist)
	}
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "https://cdn.discordapp.com/attachments/1/2/pic.png", Int: 0})
	if len(a.musicHist) != 2 {
		t.Errorf("discord non-audio link must not record, history = %d, want 2", len(a.musicHist))
	}

	// Replaying an existing track moves it to the front (MRU), no duplicate.
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: cat, Int: 1, Name: "Mystic Maya"})
	if len(a.musicHist) != 2 || a.musicHist[0].track != cat {
		t.Errorf("MRU: history = %d, front = %q, want catbox at front with no dupe", len(a.musicHist), a.musicHist[0].track)
	}
}

// TestNoteMusicHistoryCap pins the bound: the ring never exceeds the cap and
// keeps the newest entries (oldest fall off the end).
func TestNoteMusicHistoryCap(t *testing.T) {
	a := testTabApp(t)
	for i := 0; i < musicHistoryCap+8; i++ {
		a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: fmt.Sprintf("https://files.catbox.moe/s%d.mp3", i)})
	}
	if len(a.musicHist) != musicHistoryCap {
		t.Fatalf("history = %d, want cap %d", len(a.musicHist), musicHistoryCap)
	}
	if want := fmt.Sprintf("s%d", musicHistoryCap+7); a.musicHist[0].name != want {
		t.Errorf("front = %q, want newest %q", a.musicHist[0].name, want)
	}
}

// TestNoteMusicHistoryDisabled pins the Settings toggle: with the history pref
// off, an otherwise-allowlisted link is not captured.
func TestNoteMusicHistoryDisabled(t *testing.T) {
	a := testTabApp(t)
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	a.d.Prefs.SetMusicHistory(false)
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "https://files.catbox.moe/x.mp3", Int: 0})
	if len(a.musicHist) != 0 {
		t.Errorf("history disabled but captured %d entries", len(a.musicHist))
	}
}

// TestNoteMusicHistoryNoSession pins crash-safety: no session + an out-of-range
// charID must not panic, and "by" is simply left blank.
func TestNoteMusicHistoryNoSession(t *testing.T) {
	a := testTabApp(t)
	a.sess = nil
	a.noteMusicHistory(courtroom.Event{Kind: courtroom.EventMusic, Text: "https://files.catbox.moe/solo.mp3", Int: 5})
	if len(a.musicHist) != 1 || a.musicHist[0].display != "solo" {
		t.Fatalf("entry = %+v, want a single entry with display 'solo'", a.musicHist)
	}
}

// TestRefreshJukeGroups pins the M12 domain grouping for the Music-history
// playlist: a header per domain (sorted), each domain's songs under it in
// original order, subdomains collapsed onto the listed domain.
func TestRefreshJukeGroups(t *testing.T) {
	a := testTabApp(t)
	entries := []config.JukeboxEntry{
		{Title: "a", URL: "https://files.catbox.moe/a.mp3"}, // catbox.moe (subdomain)
		{Title: "y", URL: "https://youtu.be/y"},             // youtu.be
		{Title: "b", URL: "https://catbox.moe/b.mp3"},       // catbox.moe
	}
	a.jukeCacheRev = 1
	a.refreshJukeGroups(0, entries)
	want := []jukeGroupRow{
		{domain: "catbox.moe"},
		{entry: 0},
		{entry: 2},
		{domain: "youtu.be"},
		{entry: 1},
	}
	if len(a.jukeGroupRows) != len(want) {
		t.Fatalf("rows = %+v, want %+v", a.jukeGroupRows, want)
	}
	for i, w := range want {
		if a.jukeGroupRows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, a.jukeGroupRows[i], w)
		}
	}

	// Memoized: same (playlist, rev) doesn't rebuild; a rev bump does.
	a.refreshJukeGroups(0, entries[:1])
	if len(a.jukeGroupRows) != len(want) {
		t.Errorf("unchanged key must not rebuild (got %d rows)", len(a.jukeGroupRows))
	}
	a.jukeCacheRev = 2
	a.refreshJukeGroups(0, entries[:1])
	if len(a.jukeGroupRows) != 2 { // header + one entry
		t.Errorf("rev bump should rebuild, got %+v", a.jukeGroupRows)
	}
}
