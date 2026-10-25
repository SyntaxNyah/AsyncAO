//go:build novoice

package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/protocol"

// Voice chat is compiled out in the -tags novoice build: the LemmyAO/Nyathena
// VS_* relay (voice.go) is excluded, and these stubs keep the session reducer
// and the UI's single VoiceAvailable() gate compiling. handleVoicePacket
// produces no events, so an incoming VS_* packet is silently ignored;
// VoiceAvailable() is always false, so the UI never offers any voice surface.
//
// Opus MUSIC is unaffected — it decodes through SDL_mixer (internal/render),
// not this package. See internal/render/audio.go.

// VoiceCaps mirrors the real type so Session's inert voiceCaps field keeps a
// type in the voice-free build.
type VoiceCaps struct {
	Enabled       bool
	PTTOnly       bool
	MaxPeers      int
	Codec         string
	SampleRate    int
	FrameMs       int
	MaxFrameBytes int
}

// handleVoicePacket is a no-op here: the VS_* dispatch in session.go still calls
// it, but voice chat is not built, so it yields no events. It reads the inert
// voice-state fields so they don't register as unused in the voice-free build —
// they back the reducer in voice.go for the voice-enabled build.
func (s *Session) handleVoicePacket(protocol.Packet) []Event {
	_, _, _ = s.voiceCaps, s.voicePeers, s.voiceSpeaking
	return nil
}

// VoiceAvailable always reports false — there is no voice chat in this build.
func (s *Session) VoiceAvailable() bool { return false }
