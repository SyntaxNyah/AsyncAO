//go:build !novoice

package courtroom

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Voice chat — the Nyathena/LemmyAO server-relayed VS_* transport. Every opus
// frame travels client -> AO2 server -> peers over the EXISTING WebSocket: no
// WebRTC, no STUN/TURN/ICE, no P2P, so peers never learn each other's IPs (a
// structural privacy property — ../LemmyAO/src/voice/voice.ts). This file owns
// the wire + the session-side presence state only; the opus codec lives in
// internal/voice and mic capture / playback in internal/render (hard rule #1).
//
// The whole feature is gated on the server's VS_CAPS advert, so a server that
// never sends VS_CAPS has zero voice surface and an unchanged wire.
//
// Wire (canonical, '#' separator / '%' terminator; base64 needs no escaping):
//
//	s2c  VS_CAPS#<enabled>#<ptt_only>#<max_peers>#<codec>#<sample_rate>#<frame_ms>#<max_frame_bytes>#%
//	s2c  VS_PEERS#<csv_uids>#%
//	s2c  VS_JOIN#<uid>#%   VS_LEAVE#<uid>#%   VS_SPEAK#<uid>#<on>#%   VS_AUDIO#<from_uid>#<b64>#%
//	c2s  VS_JOIN#%         VS_LEAVE#%         VS_SPEAK#<on>#%         VS_FRAME#<b64>#%

// voicePeerCap bounds the voice peer / speaking maps (hard rule #4: no unbounded
// caches). Far above any real voice room; a reconnect re-dumps VS_PEERS, so a hit
// here self-heals rather than corrupting state.
const voicePeerCap = 256

// VoiceCaps is the server's voice capability advertisement (VS_CAPS). Enabled
// gates the whole feature; the rest let capture/transport match the server.
type VoiceCaps struct {
	Enabled       bool
	PTTOnly       bool
	MaxPeers      int
	Codec         string
	SampleRate    int
	FrameMs       int
	MaxFrameBytes int
}

// voiceBool parses an AO wire bool: "1"/"true"/"yes"/"on" = true, else false
// (servers vary, so accept the common spellings).
func voiceBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// handleVoicePacket reduces one VS_* packet into voice state + an event. Pure
// w.r.t. the network (no sends); mutates only the session's voice maps.
func (s *Session) handleVoicePacket(p protocol.Packet) []Event {
	switch p.Header {
	case "VS_CAPS":
		s.voiceCaps = VoiceCaps{
			Enabled:       voiceBool(p.Field(0)),
			PTTOnly:       voiceBool(p.Field(1)),
			MaxPeers:      atoiOr(p.Field(2), 0),
			Codec:         p.Field(3),
			SampleRate:    atoiOr(p.Field(4), 48000),
			FrameMs:       atoiOr(p.Field(5), 20),
			MaxFrameBytes: atoiOr(p.Field(6), 4096),
		}
		return []Event{{Kind: EventVoiceCaps}}

	case "VS_PEERS":
		s.voicePeers = make(map[int]bool, len(p.Field(0))/2+1)
		for _, f := range strings.Split(p.Field(0), ",") {
			if f = strings.TrimSpace(f); f == "" {
				continue
			}
			if uid, err := strconv.Atoi(f); err == nil && len(s.voicePeers) < voicePeerCap {
				s.voicePeers[uid] = true
			}
		}
		return []Event{{Kind: EventVoicePeers}}

	case "VS_JOIN":
		uid := atoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		if s.voicePeers == nil {
			s.voicePeers = make(map[int]bool, 8)
		}
		if len(s.voicePeers) < voicePeerCap {
			s.voicePeers[uid] = true
		}
		return []Event{{Kind: EventVoicePeers}}

	case "VS_LEAVE":
		uid := atoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		delete(s.voicePeers, uid)
		delete(s.voiceSpeaking, uid)
		return []Event{{Kind: EventVoicePeers}}

	case "VS_SPEAK":
		uid := atoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		on := voiceBool(p.Field(1))
		if on {
			if s.voiceSpeaking == nil {
				s.voiceSpeaking = make(map[int]bool, 8)
			}
			if len(s.voiceSpeaking) < voicePeerCap {
				s.voiceSpeaking[uid] = true
			}
		} else {
			delete(s.voiceSpeaking, uid)
		}
		spk := 0
		if on {
			spk = 1
		}
		return []Event{{Kind: EventVoiceSpeak, Int: uid, Int2: spk}}

	case "VS_AUDIO":
		uid := atoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		// Text carries the base64 opus payload for the audio layer (decode + play).
		return []Event{{Kind: EventVoiceAudio, Int: uid, Text: p.Field(1)}}
	}
	return nil
}

// VoiceCapsInfo returns the server's advertised voice caps (zero value = none yet).
func (s *Session) VoiceCapsInfo() VoiceCaps { return s.voiceCaps }

// VoiceAvailable reports whether this server offers voice (VS_CAPS.enabled) — the
// single gate the UI checks before showing any voice surface.
func (s *Session) VoiceAvailable() bool { return s.voiceCaps.Enabled }

// VoicePeers returns the current voice peer uids (unordered snapshot, nil if none).
func (s *Session) VoicePeers() []int {
	if len(s.voicePeers) == 0 {
		return nil
	}
	out := make([]int, 0, len(s.voicePeers))
	for uid := range s.voicePeers {
		out = append(out, uid)
	}
	return out
}

// VoicePeerCount is the live voice peer count (alloc-free; for the UI badge).
func (s *Session) VoicePeerCount() int { return len(s.voicePeers) }

// VoiceIsSpeaking reports whether uid is currently transmitting.
func (s *Session) VoiceIsSpeaking(uid int) bool { return s.voiceSpeaking[uid] }

// VoiceJoin / VoiceLeave enter or leave the area's voice channel (the server
// attaches our uid and rebroadcasts).
func (s *Session) VoiceJoin()  { s.reply(protocol.NewPacket("VS_JOIN")) }
func (s *Session) VoiceLeave() { s.reply(protocol.NewPacket("VS_LEAVE")) }

// VoiceSpeak announces our speaking-state change (PTT press/release, or open-mic
// VAD). Sent on the caller's loop.
func (s *Session) VoiceSpeak(on bool) {
	v := "0"
	if on {
		v = "1"
	}
	s.reply(protocol.NewPacket("VS_SPEAK", v))
}

// VoiceFrame sends one base64-encoded opus frame upstream (VS_FRAME). NOTE: the
// audio-capture slice must funnel frames through the session's loop (or a
// write-serialised path) — never call this directly from the SDL audio thread, or
// it races other packet writes.
func (s *Session) VoiceFrame(b64 string) { s.reply(protocol.NewPacket("VS_FRAME", b64)) }
