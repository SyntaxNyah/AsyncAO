package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// feed runs one packet through the reducer and returns the events.
func feedVoice(s *Session, header string, fields ...string) []Event {
	return s.HandlePacket(protocol.NewPacket(header, fields...))
}

func TestVoiceReceive(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "hdid")

	// VS_CAPS gates the whole feature.
	if s.VoiceAvailable() {
		t.Fatal("voice must be unavailable before VS_CAPS")
	}
	evs := feedVoice(s, "VS_CAPS", "1", "1", "8", "opus", "48000", "20", "4096")
	if len(evs) != 1 || evs[0].Kind != EventVoiceCaps {
		t.Fatalf("VS_CAPS events = %+v", evs)
	}
	caps := s.VoiceCapsInfo()
	if !caps.Enabled || !caps.PTTOnly || caps.MaxPeers != 8 || caps.SampleRate != 48000 || caps.FrameMs != 20 || caps.MaxFrameBytes != 4096 {
		t.Fatalf("caps = %+v", caps)
	}
	if !s.VoiceAvailable() {
		t.Fatal("VoiceAvailable should be true after enabled VS_CAPS")
	}

	// VS_PEERS replaces the peer set.
	if evs := feedVoice(s, "VS_PEERS", "1,2,3"); len(evs) != 1 || evs[0].Kind != EventVoicePeers {
		t.Fatalf("VS_PEERS events = %+v", evs)
	}
	if s.VoicePeerCount() != 3 {
		t.Fatalf("peer count = %d, want 3", s.VoicePeerCount())
	}

	// JOIN adds, LEAVE removes (and clears any speaking flag).
	feedVoice(s, "VS_JOIN", "4")
	if s.VoicePeerCount() != 4 {
		t.Fatalf("after JOIN count = %d, want 4", s.VoicePeerCount())
	}
	feedVoice(s, "VS_SPEAK", "2", "1")
	if !s.VoiceIsSpeaking(2) {
		t.Fatal("uid 2 should be speaking")
	}
	feedVoice(s, "VS_LEAVE", "2")
	if s.VoiceIsSpeaking(2) {
		t.Fatal("LEAVE must clear the speaking flag")
	}
	if s.VoicePeerCount() != 3 {
		t.Fatalf("after LEAVE count = %d, want 3", s.VoicePeerCount())
	}

	// SPEAK toggles and emits uid + on/off.
	evs = feedVoice(s, "VS_SPEAK", "1", "1")
	if len(evs) != 1 || evs[0].Kind != EventVoiceSpeak || evs[0].Int != 1 || evs[0].Int2 != 1 {
		t.Fatalf("VS_SPEAK on event = %+v", evs)
	}
	feedVoice(s, "VS_SPEAK", "1", "0")
	if s.VoiceIsSpeaking(1) {
		t.Fatal("uid 1 should have stopped speaking")
	}

	// AUDIO carries from-uid + base64 payload to the audio layer.
	evs = feedVoice(s, "VS_AUDIO", "3", "T3B1c0RhdGE=")
	if len(evs) != 1 || evs[0].Kind != EventVoiceAudio || evs[0].Int != 3 || evs[0].Text != "T3B1c0RhdGE=" {
		t.Fatalf("VS_AUDIO event = %+v", evs)
	}
}

func TestVoiceDisabledCaps(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "hdid")
	feedVoice(s, "VS_CAPS", "0", "0", "0", "opus", "48000", "20", "4096")
	if s.VoiceAvailable() {
		t.Fatal("enabled=0 caps must leave voice unavailable")
	}
}

func TestVoiceSend(t *testing.T) {
	var sent []string
	s := NewSession(func(p protocol.Packet) error { sent = append(sent, p.String()); return nil }, "hdid")

	s.VoiceJoin()
	s.VoiceSpeak(true)
	s.VoiceSpeak(false)
	s.VoiceFrame("Zm9vYmFy")
	s.VoiceLeave()

	want := []string{
		"VS_JOIN#%",
		"VS_SPEAK#1#%",
		"VS_SPEAK#0#%",
		"VS_FRAME#Zm9vYmFy#%",
		"VS_LEAVE#%",
	}
	if len(sent) != len(want) {
		t.Fatalf("sent %d packets, want %d: %q", len(sent), len(want), sent)
	}
	for i := range want {
		if sent[i] != want[i] {
			t.Errorf("packet %d = %q, want %q", i, sent[i], want[i])
		}
	}
}
