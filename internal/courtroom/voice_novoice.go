//go:build novoice

package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/protocol"

// Stubs for the -tags novoice build; Opus music is unaffected because it decodes through SDL_mixer in internal/render.

// VoiceCaps keeps Session's voiceCaps field typed in this build.
type VoiceCaps struct {
	Enabled       bool
	PTTOnly       bool
	MaxPeers      int
	Codec         string
	SampleRate    int
	FrameMs       int
	MaxFrameBytes int
}

// handleVoicePacket yields no events; it reads the voice-state fields so they don't register as unused in this build.
func (s *Session) handleVoicePacket(protocol.Packet) []Event {
	_, _, _ = s.voiceCaps, s.voicePeers, s.voiceSpeaking
	return nil
}

// VoiceAvailable is always false in this build.
func (s *Session) VoiceAvailable() bool { return false }
