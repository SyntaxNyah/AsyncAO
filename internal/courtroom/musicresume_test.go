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
