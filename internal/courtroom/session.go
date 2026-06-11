package courtroom

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Handshake phases, driving the fast-loading flow AO2-Client 2.11 uses
// exclusively: decryptor→HI, ID→ID, PN→askchaa, SI→RC, SC→RM, SM→RD,
// DONE. Loading is *client-initiated*: askchaa is what makes the server
// send SI — without it every server waits forever. (Only the legacy
// askchar2 paging is dead.)
type SessionPhase int

const (
	PhaseGreeting SessionPhase = iota
	PhaseLoading
	PhaseReady
)

// CharacterSlot is one server character list entry.
type CharacterSlot struct {
	Name        string
	Description string
	Taken       bool
}

// EventKind tags session events handed to the UI/courtroom layer.
type EventKind int

const (
	EventNone EventKind = iota
	// EventReady fires on DONE: lists are loaded, joining is possible.
	EventReady
	// EventCharsUpdated fires when the character list or taken flags
	// change (rebuild char select).
	EventCharsUpdated
	// EventMessage carries a parsed IC chat message.
	EventMessage
	// EventBackground carries a background change (BN).
	EventBackground
	// EventMusic carries a music change (MC): Text=track, Int=charID.
	EventMusic
	// EventOOC carries server/OOC chat: Name + Text.
	EventOOC
	// EventCharPicked confirms our character (PV): Int=char id.
	EventCharPicked
	// EventAssetURL announces the server's asset repository (ASS).
	EventAssetURL
	// EventDisconnect carries kick/ban notices.
	EventDisconnect
)

// Event is one session occurrence. Fields are populated per Kind.
type Event struct {
	Kind    EventKind
	Message *protocol.ChatMessage
	Name    string
	Text    string
	Int     int
}

// Session is a synchronous reducer over server packets: HandlePacket
// mutates state, sends protocol replies through send, and returns UI events.
// It owns no goroutines; the caller's loop feeds it (spec §17.4).
type Session struct {
	send func(protocol.Packet) error

	HDID     string
	PlayerID int
	Software string
	Features protocol.FeatureSet
	Chars    []CharacterSlot
	Music    []string
	Areas    []string
	AssetURL string

	Background string
	MyCharID   int

	phase   SessionPhase
	sendErr error
}

// NewSession builds a session that writes packets via send.
func NewSession(send func(protocol.Packet) error, hdid string) *Session {
	return &Session{
		send:     send,
		HDID:     hdid,
		Features: protocol.ParseFeatures(nil),
		MyCharID: protocol.UnpairedCharID,
		phase:    PhaseGreeting,
	}
}

// Phase reports the handshake phase.
func (s *Session) Phase() SessionPhase { return s.phase }

// SendErr reports the first failed reply write (connection teardown signal).
func (s *Session) SendErr() error { return s.sendErr }

func (s *Session) reply(p protocol.Packet) {
	if s.sendErr != nil {
		return
	}
	if err := s.send(p); err != nil {
		s.sendErr = fmt.Errorf("courtroom: sending %s: %w", p.Header, err)
	}
}

// HandlePacket reduces one server packet into state + events.
func (s *Session) HandlePacket(p protocol.Packet) []Event {
	switch p.Header {
	case "decryptor":
		// Modern servers still open with this; FantaCrypt itself is dead —
		// HI goes out plain (noencryption era).
		s.reply(protocol.NewPacket("HI", s.HDID))

	case "ID":
		s.PlayerID = atoiOr(p.Field(0), 0)
		s.Software = p.Field(1)
		s.reply(protocol.NewPacket("ID", protocol.ClientName, protocol.Version))

	case "FL":
		s.Features = protocol.ParseFeatures(p.Fields)

	case "PN":
		// Population marker, the tail of every server family's greeting.
		// Joining is client-initiated from here: webAO sends askchaa on PN
		// (handshake.ts applyServerInfo), AO2-Client at join time
		// (networkmanager.cpp join_to_server); the server answers with SI.
		// Phase guard: population re-broadcasts must not reload the lists.
		if s.phase == PhaseGreeting {
			s.reply(protocol.NewPacket("askchaa"))
		}

	case "ASS":
		if decoded, err := url.PathUnescape(p.Field(0)); err == nil && decoded != "" {
			s.AssetURL = decoded
		} else {
			s.AssetURL = p.Field(0)
		}
		return []Event{{Kind: EventAssetURL, Text: s.AssetURL}}

	case "SI":
		s.phase = PhaseLoading
		s.Chars = s.Chars[:0]
		s.Music = s.Music[:0]
		s.Areas = s.Areas[:0]
		s.reply(protocol.NewPacket("RC"))

	case "SC":
		for _, field := range p.Fields {
			// AO2-Client splits on & and percent-decodes each sub-element
			// again (packet_distribution.cpp) — mirror it.
			parts := strings.Split(field, "&")
			slot := CharacterSlot{Name: protocol.DecodeField(parts[0])}
			if len(parts) > 1 {
				slot.Description = protocol.DecodeField(parts[1])
			}
			s.Chars = append(s.Chars, slot)
		}
		s.reply(protocol.NewPacket("RM"))
		return []Event{{Kind: EventCharsUpdated}}

	case "CharsCheck":
		for i := 0; i < len(p.Fields) && i < len(s.Chars); i++ {
			s.Chars[i].Taken = p.Fields[i] == "-1"
		}
		return []Event{{Kind: EventCharsUpdated}}

	case "SM":
		s.Areas, s.Music = splitAreasAndMusic(p.Fields)
		s.reply(protocol.NewPacket("RD"))

	case "DONE":
		s.phase = PhaseReady
		return []Event{{Kind: EventReady}}

	case "MS":
		msg, err := protocol.ParseMS(p.Fields, s.Features, len(s.Chars))
		if err != nil {
			return nil // malformed/japing message: drop like AO2-Client
		}
		return []Event{{Kind: EventMessage, Message: msg}}

	case "MC":
		return []Event{{Kind: EventMusic, Text: p.Field(0), Int: atoiOr(p.Field(1), protocol.UnpairedCharID)}}

	case "BN":
		s.Background = p.Field(0)
		return []Event{{Kind: EventBackground, Text: s.Background}}

	case "PV":
		s.MyCharID = atoiOr(p.Field(2), protocol.UnpairedCharID)
		return []Event{{Kind: EventCharPicked, Int: s.MyCharID}}

	case "CT":
		return []Event{{Kind: EventOOC, Name: p.Field(0), Text: p.Field(1)}}

	case "KK":
		return []Event{{Kind: EventDisconnect, Text: "Kicked: " + p.Field(0)}}
	case "KB":
		return []Event{{Kind: EventDisconnect, Text: "Banned: " + p.Field(0)}}
	case "BD":
		return []Event{{Kind: EventDisconnect, Text: "Banned: " + p.Field(0)}}

	case "checkconnection":
		// Keepalive: AO2-Client answers CH with our char id.
		s.reply(protocol.NewPacket("CH", strconv.Itoa(s.MyCharID)))
	}
	return nil
}

// splitAreasAndMusic mirrors AO2-Client's SM scan: entries before the first
// audio-extension entry are areas, the rest (inclusive) are music.
func splitAreasAndMusic(fields []string) (areas, music []string) {
	musicStart := len(fields)
	for i, f := range fields {
		if hasAudioExt(f) {
			musicStart = i
			break
		}
	}
	areas = append(areas, fields[:musicStart]...)
	music = append(music, fields[musicStart:]...)
	return areas, music
}

var audioExts = []string{".wav", ".mp3", ".mp4", ".ogg", ".opus"}

func hasAudioExt(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range audioExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// --- Outgoing actions -------------------------------------------------------

// PickCharacter requests a character (CC packet).
func (s *Session) PickCharacter(charID int) {
	s.reply(protocol.NewPacket("CC", strconv.Itoa(s.PlayerID), strconv.Itoa(charID), s.HDID))
}

// SendChat sends an IC message shaped for this server's features.
func (s *Session) SendChat(msg protocol.OutgoingMS) {
	s.reply(msg.Packet(s.Features))
}

// SendOOC sends an out-of-character line.
func (s *Session) SendOOC(name, text string) {
	s.reply(protocol.NewPacket("CT", name, text))
}

// RequestMusic asks the server to play a track (or area transfer by name).
func (s *Session) RequestMusic(track string) {
	s.reply(protocol.NewPacket("MC", track, strconv.Itoa(s.MyCharID)))
}

// CallMod sends a mod call with an optional reason.
func (s *Session) CallMod(reason string) {
	if s.Features.Has(protocol.FeatureModcallReason) {
		s.reply(protocol.NewPacket("ZZ", reason))
		return
	}
	s.reply(protocol.NewPacket("ZZ"))
}

func atoiOr(s string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return v
}
