package config

import (
	"path/filepath"
	"testing"
)

// TestMusicHostDefaults pins the out-of-the-box allowlist.
func TestMusicHostDefaults(t *testing.T) {
	p, _ := newTestPrefs(t)
	got := map[string]bool{}
	for _, h := range p.MusicHostList() {
		got[h] = true
	}
	for _, want := range []string{"catbox.moe", "file.garden", "youtube.com", "youtu.be", "discordapp.com", "cdn.discordapp.com"} {
		if !got[want] {
			t.Errorf("default allowlist missing %q (have %v)", want, p.MusicHostList())
		}
	}
}

// TestMusicURLAllowed pins the record gate: allowlisted hosts (incl. subdomains)
// pass, Discord needs an audio file, bare names and non-listed hosts fail.
func TestMusicURLAllowed(t *testing.T) {
	p, _ := newTestPrefs(t)
	cases := []struct {
		url  string
		want bool
	}{
		{"https://files.catbox.moe/ab12.mp3", true}, // subdomain of catbox.moe
		{"https://catbox.moe/x", true},              // bare allowlisted host, no ext needed
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://cdn.discordapp.com/attachments/1/2/song.opus", true}, // discord audio file
		// A real signed Discord CDN link: the long ?ex=&is=&hm=& query must be
		// stripped before the .opus extension check (and the dotted filename kept).
		{"https://cdn.discordapp.com/attachments/1343925472320557056/1509516980174983249/Reolill.bell_Cover.opus?ex=6a332bfd&is=6a31da7d&hm=063896bd960c28649460d0d9ce68a8a1062d2fe070d0d9b190c63f380e3751eb&", true},
		{"https://cdn.discordapp.com/attachments/1/2/pic.png", false},    // discord non-audio
		{"https://media.discordapp.net/attachments/1/2/clip.mp3", false}, // discordapp.net not in default list
		{"https://miku.pizza/base/sounds/music/Trial.opus?ex=1", false},  // server host, not listed
		{"Trial.opus", false}, // bare server-song name (no host)
		{"", false},
		{"not a url", false},
	}
	for _, c := range cases {
		if got := p.MusicURLAllowed(c.url); got != c.want {
			t.Errorf("MusicURLAllowed(%q) = %v, want %v", c.url, got, c.want)
		}
	}

	// Adding a discord host applies the audio-file rule to it too (not just the
	// defaults): an audio link records, a non-audio one doesn't.
	p.AddMusicHost("discordapp.net")
	if !p.MusicURLAllowed("https://media.discordapp.net/attachments/1/2/clip.mp3") {
		t.Error("after adding discordapp.net, a discord audio file should record")
	}
	if p.MusicURLAllowed("https://media.discordapp.net/attachments/1/2/clip.png") {
		t.Error("after adding discordapp.net, a discord non-audio link must still be rejected")
	}
}

// TestMusicHostAddRemove pins normalization (paste a full URL → bare host, www
// stripped), dedup, and removal.
func TestMusicHostAddRemove(t *testing.T) {
	p, _ := newTestPrefs(t)
	if !p.AddMusicHost("https://www.Example.com/songs/") {
		t.Fatal("AddMusicHost should report a change")
	}
	if !p.MusicURLAllowed("https://example.com/track") {
		t.Error("example.com should be allowed after adding it (normalized from the pasted URL)")
	}
	if p.AddMusicHost("example.com") {
		t.Error("re-adding the same host must be a no-op")
	}
	if !p.RemoveMusicHost("example.com") || p.MusicURLAllowed("https://example.com/track") {
		t.Error("RemoveMusicHost should drop it")
	}
}

// TestMusicURLDomain pins the grouping label: subdomains collapse onto the
// listed domain; an unlisted host returns its bare host; non-URLs return "".
func TestMusicURLDomain(t *testing.T) {
	p, _ := newTestPrefs(t)
	cases := map[string]string{
		"https://files.catbox.moe/x.mp3": "catbox.moe",
		"https://youtu.be/abc":           "youtu.be",
		"https://other.example/x":        "other.example",
		"Trial.opus":                     "",
	}
	for url, want := range cases {
		if got := p.MusicURLDomain(url); got != want {
			t.Errorf("MusicURLDomain(%q) = %q, want %q", url, got, want)
		}
	}
}

// TestMusicHostRoundTrip pins that a customized allowlist survives save→load and
// that clearing it isn't clobbered back to the default by the absent-default.
func TestMusicHostRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PrefsFileName)
	p, err := newWithDebounce(path, testDebounce)
	if err != nil {
		t.Fatalf("newWithDebounce: %v", err)
	}
	p.AddMusicHost("pomf.cat")
	p.RemoveMusicHost("youtube.com")
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	q, err := load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !q.MusicURLAllowed("https://pomf.cat/a.mp3") {
		t.Error("added host lost across save/load")
	}
	if q.MusicURLAllowed("https://youtube.com/watch?v=x") {
		t.Error("removed host came back (absent-default must not clobber an explicit list)")
	}
}
