package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSessionTracksCurrentMusic pins the new session state behind the "music goes silent on
// rejoin / room rebuild" fix: the session remembers the area's current REAL song (like it
// remembers Background), a ~stop clears it, an area-name transfer leaves the playing song
// alone, and a fresh handshake (SI) resets it so a reconnect can't resume a stale track the
// server never re-announced.
func TestSessionTracksCurrentMusic(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")

	s.HandlePacket(protocol.NewPacket("MC", "Trial.opus", "0"))
	if s.MusicTrack != "Trial.opus" {
		t.Fatalf("real song not captured on the session: %q", s.MusicTrack)
	}
	s.HandlePacket(protocol.NewPacket("MC", "Basement", "0")) // area name, no audio ext
	if s.MusicTrack != "Trial.opus" {
		t.Errorf("an area transfer changed the remembered song: %q", s.MusicTrack)
	}
	s.HandlePacket(protocol.NewPacket("MC", "~stop.mp3", "0")) // stop sentinel
	if s.MusicTrack != "" {
		t.Errorf("a stop didn't clear the remembered song: %q", s.MusicTrack)
	}
	s.HandlePacket(protocol.NewPacket("MC", "Cornered.opus", "0"))
	s.HandlePacket(protocol.NewPacket("SI", "1", "1", "1")) // fresh handshake
	if s.MusicTrack != "" {
		t.Errorf("an SI handshake didn't reset the remembered song: %q", s.MusicTrack)
	}
}

// TestAmbienceChannelNeverTouchesMusic pins the MC channel split (field 4;
// AO2-Client courtroom.cpp handle_song: channel 0 = master music, others =
// ambience). WAP-family servers stream area ambience on channel 1 — one on
// every join — and treating those as the song stopped/replaced the area's
// real music (playtest: rejoining a server stayed silent).
func TestAmbienceChannelNeverTouchesMusic(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")

	s.HandlePacket(protocol.NewPacket("MC", "Trial.opus", "0"))
	if s.MusicTrack != "Trial.opus" {
		t.Fatalf("real song not captured: %q", s.MusicTrack)
	}

	// The WAP join burst: ambience on channel 1, then the ambience ~stop
	// default on the wire — neither may emit events or touch the song.
	if ev := s.HandlePacket(protocol.NewPacket("MC", "rain.opus", "-1", "Server", "1", "1", "0")); len(ev) != 0 {
		t.Errorf("ambience MC emitted events: %+v", ev)
	}
	if ev := s.HandlePacket(protocol.NewPacket("MC", "~stop.mp3", "-1", "Server", "0", "1", "0")); len(ev) != 0 {
		t.Errorf("ambience stop emitted events: %+v", ev)
	}
	if s.MusicTrack != "Trial.opus" {
		t.Errorf("ambience touched the remembered song: %q", s.MusicTrack)
	}

	// Channel 0 (or absent, the legacy short packet) stays the music path.
	s.HandlePacket(protocol.NewPacket("MC", "Cornered.opus", "0", "DJ", "1", "0", "0"))
	if s.MusicTrack != "Cornered.opus" {
		t.Errorf("explicit channel 0 must still be music, got %q", s.MusicTrack)
	}
}

// TestMusicResumesAcrossRoomRebuild proves the whole "rejoin keeps the music" chain end to
// end without buildRoom's full dependency graph: the session captures the playing song from
// an MC (the join handshake announces it before the room exists), and re-seeding that
// session track into a FRESHLY built room — exactly what buildRoom does after the rebuild —
// plays it. Today the second half had nothing to re-seed, so the song fell silent.
func TestMusicResumesAcrossRoomRebuild(t *testing.T) {
	// Half 1: the session remembers what's playing.
	s := NewSession(func(protocol.Packet) error { return nil }, "h")
	s.HandlePacket(protocol.NewPacket("MC", "Investigation.opus", "0"))
	if s.MusicTrack != "Investigation.opus" {
		t.Fatalf("session didn't capture the playing track: %q", s.MusicTrack)
	}

	// Half 2: a fresh room (a rebuild) re-seeded with the session track plays it.
	room, _, _, audio := newCourtroomRig(t)
	if room.Scene.MusicTrack != "" {
		t.Fatalf("a fresh room should start with no Now-Playing, got %q", room.Scene.MusicTrack)
	}
	room.HandleEvent(Event{Kind: EventMusic, Text: s.MusicTrack}) // what buildRoom now does
	if room.Scene.MusicTrack != "Investigation.opus" {
		t.Errorf("re-seeded track isn't Now-Playing: %q", room.Scene.MusicTrack)
	}
	if len(audio.music) != 1 || audio.music[0] == "" {
		t.Errorf("re-seeded track didn't actually start playing: plays=%v", audio.music)
	}
}
