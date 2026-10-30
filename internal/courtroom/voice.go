//go:build !novoice

package courtroom

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Server-relayed VS_* voice over the WebSocket: no P2P, so peers never learn IPs (../LemmyAO/src/voice/voice.ts).
//
//	s2c  VS_CAPS#<enabled>#<ptt_only>#<max_peers>#<codec>#<sample_rate>#<frame_ms>#<max_frame_bytes>#%
//	s2c  VS_PEERS#<csv_uids>#%
//	s2c  VS_JOIN#<uid>#%   VS_LEAVE#<uid>#%   VS_SPEAK#<uid>#<on>#%   VS_AUDIO#<from_uid>#<b64>#%
//	c2s  VS_JOIN#%         VS_LEAVE#%         VS_SPEAK#<on>#%         VS_FRAME#<b64>#%

// voicePeerCap bounds the voice maps (hard rule 4); a reconnect re-dumps VS_PEERS, so hitting it self-heals.
const voicePeerCap = 256

const voicePeersInitialCap = 8

// VS_CAPS field order, and defaults for fields an older server omits.
const (
	vsCapsEnabled = iota
	vsCapsPTTOnly
	vsCapsMaxPeers
	vsCapsCodec
	vsCapsSampleRate
	vsCapsFrameMs
	vsCapsMaxFrameBytes

	voiceDefaultSampleRate    = 48000
	voiceDefaultFrameMs       = 20
	voiceDefaultMaxFrameBytes = 4096
)

// VoiceCaps is the server's VS_CAPS advertisement; Enabled gates the whole feature.
type VoiceCaps struct {
	Enabled       bool
	PTTOnly       bool
	MaxPeers      int
	Codec         string
	SampleRate    int
	FrameMs       int
	MaxFrameBytes int
}

// voiceBool parses an AO wire bool, accepting the spellings servers vary between.
func voiceBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// handleVoicePacket reduces one VS_* packet into voice state and an event, without sending.
func (s *Session) handleVoicePacket(p protocol.Packet) []Event {
	switch p.Header {
	case "VS_CAPS":
		s.voiceCaps = VoiceCaps{
			Enabled:       voiceBool(p.Field(vsCapsEnabled)),
			PTTOnly:       voiceBool(p.Field(vsCapsPTTOnly)),
			MaxPeers:      parse.AtoiOr(p.Field(vsCapsMaxPeers), 0),
			Codec:         p.Field(vsCapsCodec),
			SampleRate:    parse.AtoiOr(p.Field(vsCapsSampleRate), voiceDefaultSampleRate),
			FrameMs:       parse.AtoiOr(p.Field(vsCapsFrameMs), voiceDefaultFrameMs),
			MaxFrameBytes: parse.AtoiOr(p.Field(vsCapsMaxFrameBytes), voiceDefaultMaxFrameBytes),
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
		uid := parse.AtoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		if s.voicePeers == nil {
			s.voicePeers = make(map[int]bool, voicePeersInitialCap)
		}
		if len(s.voicePeers) < voicePeerCap {
			s.voicePeers[uid] = true
		}
		return []Event{{Kind: EventVoicePeers}}

	case "VS_LEAVE":
		uid := parse.AtoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		delete(s.voicePeers, uid)
		delete(s.voiceSpeaking, uid)
		return []Event{{Kind: EventVoicePeers}}

	case "VS_SPEAK":
		uid := parse.AtoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		on := voiceBool(p.Field(1))
		if on {
			if s.voiceSpeaking == nil {
				s.voiceSpeaking = make(map[int]bool, voicePeersInitialCap)
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
		uid := parse.AtoiOr(p.Field(0), -1)
		if uid < 0 {
			return nil
		}
		// Text carries the base64 opus payload for the audio layer (decode + play).
		return []Event{{Kind: EventVoiceAudio, Int: uid, Text: p.Field(1)}}
	}
	return nil
}

// VoiceCapsInfo returns the advertised voice caps (zero value = none yet).
func (s *Session) VoiceCapsInfo() VoiceCaps { return s.voiceCaps }

// VoiceAvailable reports whether this server offers voice; the UI checks it before showing any voice control.
func (s *Session) VoiceAvailable() bool { return s.voiceCaps.Enabled }

// VoicePeers returns the current voice peer uids (unordered, nil if none).
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

// VoicePeerCount is the allocation-free peer count for the UI badge.
func (s *Session) VoicePeerCount() int { return len(s.voicePeers) }

func (s *Session) VoiceIsSpeaking(uid int) bool { return s.voiceSpeaking[uid] }

// VoiceJoin and VoiceLeave enter or leave the area's voice channel; the server attaches our uid.
func (s *Session) VoiceJoin()  { s.reply(protocol.NewTypedPacket(&packetutil.VS_JOINToServer{})) }
func (s *Session) VoiceLeave() { s.reply(protocol.NewTypedPacket(&packetutil.VS_LEAVEToServer{})) }

// VoiceSpeak announces a speaking-state change.
func (s *Session) VoiceSpeak(on bool) {
	s.reply(protocol.NewTypedPacket(&packetutil.VS_SPEAKToServer{On: on}))
}

// VoiceFrame sends one opus frame upstream; never call it from the SDL audio thread, or it races other writes.
func (s *Session) VoiceFrame(b64 string) {
	s.reply(protocol.NewTypedPacket(&packetutil.VS_FRAME{Payload: b64}))
}
