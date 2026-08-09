package courtroom

import (
	"strings"
	"testing"
)

// TestMP3TracksMintExactURLs pins the URL half of the MP3 music contract:
// whatever spelling an mp3 track arrives in, the URL handed to the audio sink is
// COMPLETE (extension included) so the manager's exact path can take it —
// PrefetchExact, never the format-probing ladder (CLAUDE.md: music tracks carry
// their extension, AO2-Client get_music_path).
//
// The direct-link cases are the ones that regress quietly: a DJ /play link is
// returned byte for byte, NOT lowercased, NOT re-escaped and NOT stripped of its
// query string — Discord CDN links carry a signed ?ex=&is=&hm= suffix and a
// mangled one 403s.
func TestMP3TracksMintExactURLs(t *testing.T) {
	u := NewURLBuilder("http://cdn.example.com/base/")
	cases := []struct {
		name  string
		track string
		want  string
	}{
		{
			name:  "server-relative mp3",
			track: "Cornered.mp3",
			want:  "http://cdn.example.com/base/sounds/music/cornered.mp3",
		},
		{
			name:  "server-relative mp3 in a category folder",
			track: "AA1/Trial (Moderate).mp3",
			want:  "http://cdn.example.com/base/sounds/music/aa1/trial%20%28moderate%29.mp3",
		},
		{
			name:  "direct http mp3 link is verbatim",
			track: "http://radio.example.com/Stream/Song.MP3",
			want:  "http://radio.example.com/Stream/Song.MP3",
		},
		{
			name:  "direct https mp3 link keeps its signed query",
			track: "https://cdn.discordapp.com/attachments/1/2/song.mp3?ex=abc&is=def&hm=012",
			want:  "https://cdn.discordapp.com/attachments/1/2/song.mp3?ex=abc&is=def&hm=012",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := u.MusicURL(tc.track); got != tc.want {
				t.Errorf("MusicURL(%q) = %q, want %q", tc.track, got, tc.want)
			}
		})
	}
}

// TestMP3TracksAreSongsNotAreaJumps pins the classification an mp3 depends on
// before it ever reaches a URL. AO splits one SM list into areas and songs by
// extension, and the same predicate decides whether an MC is a song or an area
// transfer — an mp3 misread as an area name plays nothing at all and silently
// jumps the room instead.
func TestMP3TracksAreSongsNotAreaJumps(t *testing.T) {
	for _, track := range []string{"Cornered.mp3", "CORNERED.MP3", "aa1/trial.mp3"} {
		if !HasAudioExt(track) {
			t.Errorf("HasAudioExt(%q) = false — an mp3 track would be filed as an area", track)
		}
		if isAreaTransfer(track) {
			t.Errorf("isAreaTransfer(%q) = true — the mp3 would jump the room instead of playing", track)
		}
	}
	// A direct link is always a song even though its extension sits before a
	// query string (isMusicURL short-circuits ahead of the extension test).
	link := "https://cdn.discordapp.com/attachments/1/2/song.mp3?ex=abc"
	if isAreaTransfer(link) {
		t.Errorf("a direct mp3 link with a query string was classified as an area transfer")
	}
	// And the area side still works: a bare name with no audio extension.
	if !isAreaTransfer("Courtroom 1") {
		t.Error("a plain area name must still classify as an area transfer")
	}
}

// TestMusicWireCarriesTheMP3TrackThrough pins the reducer end: an MC naming an
// mp3 lands as a playable song event and is remembered as the area's track, with
// the extension intact for the exact fetch downstream.
func TestMusicWireCarriesTheMP3TrackThrough(t *testing.T) {
	rec := &sentRecorder{}
	s := NewSession(rec.send, "hdid")
	evs := feed(t, s, "MC#Cornered.mp3#3#DJ#1#0#0#%")
	if len(evs) != 1 || evs[0].Kind != EventMusic {
		t.Fatalf("MC produced %+v, want one EventMusic", evs)
	}
	if evs[0].Text != "Cornered.mp3" {
		t.Errorf("event track = %q, want the mp3 name intact", evs[0].Text)
	}
	if s.MusicTrack != "Cornered.mp3" {
		t.Errorf("remembered track = %q, want the mp3", s.MusicTrack)
	}
	u := NewURLBuilder("http://cdn.example.com/base/")
	if got := u.MusicURL(evs[0].Text); !strings.HasSuffix(got, ".mp3") {
		t.Errorf("URL for the mp3 event = %q, want an .mp3 suffix", got)
	}
}
