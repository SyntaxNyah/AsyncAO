package courtroom

import (
	"strings"
	"testing"
)

// TestMusicDisplayName pins the music-list DISPLAY transform (issue #43: the
// list showed the whole wire entry, e.g. "999/songs/that/slap/[999]
// Tranquility.ogg"). It is AO2-Client Courtroom::list_music (courtroom.cpp:
// 1738-1782) — cut at the LAST '.', THEN take everything after the LAST '/' —
// with the QUrl::fileName() ordering for direct http(s) links (handle_song).
func TestMusicDisplayName(t *testing.T) {
	cases := []struct{ track, want string }{
		// The reporter's entry: deep nesting, spaces and brackets kept.
		{"999/songs/that/slap/[999] Tranquility.ogg", "[999] Tranquility"},
		{"track.mp3", "track"},
		{"songs/intro.opus", "intro"},
		// Category headers carry no extension, so list_music leaves them alone
		// (listname == i_song is exactly how AO2 spots a category).
		{"==Cave Story OST==", "==Cave Story OST=="},
		{"Trucy's Theme", "Trucy's Theme"},
		{"=== STOP ===", "=== STOP ==="},
		// The stop sentinel is still a track name — it shortens like any other.
		{"~stop.mp3", "~stop"},
		// Dots that are NOT the extension survive: only the LAST one is cut.
		{"Mr. X.mp3", "Mr. X"},
		{"OST vol.2/Theme.ogg", "Theme"},
		// AO2's ORDER, not basename-first: a dot before the last '/' and no
		// extension at all cuts at the dot ("vol.2/name" -> "vol"). Pinning it
		// keeps the port honest even though real lists rarely look like this.
		{"vol.2/name", "vol"},
		// Unicode is byte-sliced but never split mid-rune (the cut points are
		// ASCII '.' and '/').
		{"東方/幻想郷.opus", "幻想郷"},
		// Windows-authored entry: '\' is a separator too (superset of AO2).
		{`songs\intro.mp3`, "intro"},
		// Direct links: the query/fragment goes FIRST (a Discord CDN /play link
		// signs with ?ex=&is=&hm=&), then the directory, then the extension.
		{"https://miku.pizza/base/sounds/music/Trial.opus?ex=1&is=2&hm=3&", "Trial"},
		{"https://example.com/music/Theme.Song.v2.ogg", "Theme.Song.v2"},
		{"http://example.com/a/b/song.mp3#t=30", "song"},
		// Scheme match is case-insensitive, and the URL branch's precedence is
		// what saves this one: cutting at the last '.' first would land inside
		// the query ("q=1.5") and leave "x.OGG?q=1".
		{"HTTPS://cdn.example.com/x.OGG?q=1.5", "x"},
		// A name that is nothing but an extension would be blank under Qt's
		// left(0); we deliberately fall back to the raw name so no row is empty.
		{".mp3", ".mp3"},
		{"", ""},
	}
	for _, c := range cases {
		if got := MusicDisplayName(c.track); got != c.want {
			t.Errorf("MusicDisplayName(%q) = %q, want %q", c.track, got, c.want)
		}
	}
}

// TestMusicDisplayNameSubstring pins the property the music search relies on:
// the display name is always a slice of the raw entry, so filtering the RAW
// text necessarily matches everything the row shows.
func TestMusicDisplayNameSubstring(t *testing.T) {
	for _, track := range []string{
		"999/songs/that/slap/[999] Tranquility.ogg", "track.mp3", "==Category==",
		"Mr. X.mp3", "東方/幻想郷.opus", "https://x.example/a/b.opus?sig=1", ".mp3",
	} {
		if got := MusicDisplayName(track); !strings.Contains(track, got) {
			t.Errorf("MusicDisplayName(%q) = %q, not a substring of the raw entry", track, got)
		}
	}
}

// TestMusicDisplayNameKeepsWireName is the hazard guard: shortening is DISPLAY
// only. The MC packet and the streamed music URL must still carry the original
// entry byte-for-byte, or clicking a shortened row would request a track the
// server has never heard of.
func TestMusicDisplayNameKeepsWireName(t *testing.T) {
	const track = "999/songs/that/slap/[999] Tranquility.ogg"
	if MusicDisplayName(track) == track {
		t.Fatal("fixture no longer exercises shortening")
	}
	rec := &sentRecorder{}
	s := NewSession(rec.send, "h")
	s.RequestMusic(track)
	if len(rec.packets) != 1 || rec.packets[0].Header != "MC" {
		t.Fatalf("RequestMusic sent %v, want one MC packet", rec.headers())
	}
	if rec.packets[0].Fields[0] != track {
		t.Errorf("MC track = %q, want the raw entry %q", rec.packets[0].Fields[0], track)
	}
	// And the asset URL is built from the raw name too — extension included,
	// or the fetch 404s.
	u := NewURLBuilder("https://miku.pizza/base/")
	got := u.MusicURL(track)
	want := "https://miku.pizza/base/" + soundsMusic + "999/songs/that/slap/%5B999%5D%20tranquility.ogg"
	if got != want {
		t.Errorf("MusicURL(%q) = %q, want %q", track, got, want)
	}
}

// TestMusicDisplayNameAllocFree gates the render loop: the music list calls this
// once per VISIBLE ROW every frame, so it must only slice — never lowercase a
// copy of the track (rule §17: zero-allocation render path).
func TestMusicDisplayNameAllocFree(t *testing.T) {
	tracks := []string{
		"999/songs/that/slap/[999] Tranquility.ogg",
		"==Cave Story OST==",
		"HTTPS://cdn.example.com/x.OGG?q=1.5",
	}
	sink := ""
	if n := testing.AllocsPerRun(100, func() {
		for _, tr := range tracks {
			sink = MusicDisplayName(tr)
		}
	}); n != 0 {
		t.Errorf("MusicDisplayName allocates %v/run, want 0 (sink=%q)", n, sink)
	}
}

// TestMusicActionUsesDisplayName pins that the IC "has played a song" line and
// the music list agree — one shared helper, one name on screen.
func TestMusicActionUsesDisplayName(t *testing.T) {
	const track = "999/songs/that/slap/[999] Tranquility.ogg"
	action, song, ok := MusicAction(track)
	if !ok || action != "has played a song" {
		t.Fatalf("MusicAction(%q) = (%q, %q, %v)", track, action, song, ok)
	}
	if song != MusicDisplayName(track) {
		t.Errorf("log line song = %q, want the list's display name %q", song, MusicDisplayName(track))
	}
}
