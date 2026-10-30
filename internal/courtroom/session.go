package courtroom

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Handshake phases, as AO2-Client 2.11: decryptor→HI, ID→ID, PN→askchaa, SI→RC, SC→RM, SM→RD, DONE.
// askchaa is what makes the server send SI; without it every server waits forever.
type SessionPhase int

const (
	PhaseGreeting SessionPhase = iota
	PhaseLoading
	PhaseReady
)

func (p SessionPhase) String() string {
	switch p {
	case PhaseGreeting:
		return "greeting"
	case PhaseLoading:
		return "loading"
	case PhaseReady:
		return "ready"
	}
	return "unknown"
}

type CharacterSlot struct {
	Name        string
	Description string
	Taken       bool
}

// AreaInfo is the live ARUP state for one area (courtroom.cpp arup_modify).
type AreaInfo struct {
	// Players is the live player count (-1 until the server reports it).
	Players int
	// Status is the area status string (LOOKING-FOR-PLAYERS, CASING, ...).
	Status string
	// CM is the case master name(s) ("" or "FREE" = none).
	CM string
	// Lock is the lock state (FREE, SPECTATABLE, LOCKED).
	Lock string
}

// EvidenceItem is one LE entry; each field splits on '&' and each part is decoded again (packet_distribution.cpp).
type EvidenceItem struct {
	Name        string
	Description string
	Image       string
}

// TimerState is one TI clock, computed from a deadline so rendering never depends on packet cadence.
type TimerState struct {
	Visible bool
	Running bool
	// Deadline is the wall-clock zero point while Running.
	Deadline time.Time
	// Left is the frozen remainder while paused.
	Left time.Duration
}

func (t *TimerState) Remaining(now time.Time) time.Duration {
	if t.Running {
		if d := t.Deadline.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return t.Left
}

// Judge-control states from the JD packet (Courtroom::JudgeState).
const (
	// JudgePosDependent: controls show while our pos is the judge stand.
	JudgePosDependent = -1
	// JudgeHide: the server hides the judge controls.
	JudgeHide = 0
	// JudgeShow: the server grants the judge controls.
	JudgeShow = 1
)

// Casing role bits for CASEA (def#pro#judge#jury#steno); removed upstream but tsuserver-family servers still send them.
const (
	CaseRoleDef = 1 << iota
	CaseRolePro
	CaseRoleJudge
	CaseRoleJury
	CaseRoleSteno
)

const (
	// TimerCount caps TI clocks (AO2 max_clocks = 5).
	TimerCount = 5
	// evidenceCap stops a hostile server ballooning the LE list (hard rule 4).
	evidenceCap = 512
	// evidenceFields is an LE entry's name&desc&image.
	evidenceFields = 3
	// posListCap bounds the SD dropdown (hard rule 4).
	posListCap = 64
	// bnWipeFieldCount is the BN arity at which AO2 re-stages (packet_distribution.cpp, content.size() >= 2).
	bnWipeFieldCount = 2
	// HPBarMax is the top HP pip count (set_hp_bar guards 0..10); the UI draws defensebar0..defensebar<HPBarMax>.
	HPBarMax = 10
	// serverTextRuneCap bounds rasterized prose (hard rule 4); LabelClipped draws nothing for an oversized string.
	serverTextRuneCap = 8192
)

// capServerText clamps with an ellipsis. Apply it to the raw field before any "Kicked: " or "Banned: " prefix:
// internal/ui matches those, and a cut "Banned" would re-arm auto-reconnect against a ban.
func capServerText(s string) string {
	s = protocol.SanitizeText(s)
	if utf8.RuneCountInString(s) <= serverTextRuneCap {
		return s
	}
	return string([]rune(s)[:serverTextRuneCap]) + "…"
}

type EventKind int

const (
	EventNone EventKind = iota
	EventReady
	// EventCharsUpdated fires when the character list or taken flags change.
	EventCharsUpdated
	EventMessage
	EventBackground
	// EventMusic carries an MC: Text=track, Int=charID, Name=showname (may be ""), for the IC log (handle_song).
	EventMusic
	// EventOOC carries server or OOC chat: Name + Text.
	EventOOC
	// EventCharPicked confirms our character (PV): Int=char id.
	EventCharPicked
	EventAssetURL
	EventDisconnect
	// EventDebug carries protocol diagnostics for the debug overlay; consumers may ignore it.
	EventDebug
	// EventHP is a penalty-bar change: Int = bar (1 def, 2 pro), Int2 = 0..10.
	EventHP
	// EventWTCE is a testimony or verdict splash: Text = wtce id, Int = variant.
	EventWTCE
	EventModcall
	EventAreasUpdated
	EventJudge
	// EventAuth carries the AUTH state in Int (1+ mod, 0 failed, <0 logout).
	EventAuth
	// EventSetPos carries the side the server forced (SP) in Text.
	EventSetPos
	// EventPosList signals the SD dropdown list arrived (read PosList).
	EventPosList
	// EventCase is a CASEA announcement: Text = message, Int = needed-role bits (CaseRole*).
	EventCase
	EventNotice
	// EventMuted signals our mute state changed (MU/UM on our cid or -1): Int = 1 muted, 0 unmuted.
	EventMuted
	EventEvidence
	// EventEvidenceChanged signals one PE/EE/DE change: Int = index (-1 append), Int2 = EvidenceOp*, Text = name.
	EventEvidenceChanged
	EventTimer
	// EventPlayersUpdated signals the live player list changed (PR/PU).
	EventPlayersUpdated
	// EventVoiceCaps signals VS_CAPS arrived.
	EventVoiceCaps
	// EventVoicePeers signals the voice peer set changed (VS_PEERS/JOIN/LEAVE).
	EventVoicePeers
	// EventVoiceSpeak is a peer speaking toggle (VS_SPEAK): Int = uid, Int2 = 1 speaking, 0 stopped.
	EventVoiceSpeak
	// EventVoiceAudio is one inbound opus frame (VS_AUDIO): Int = from-uid, Text = base64 payload.
	EventVoiceAudio
	// EventGroupPair signals the group-pair roster changed (GP).
	EventGroupPair
)

const (
	EvidenceOpAdd = iota
	EvidenceOpEdit
	EvidenceOpDelete
)

// Event is one session occurrence; fields are populated per Kind.
type Event struct {
	Kind    EventKind
	Message *protocol.ChatMessage
	Name    string
	Text    string
	Int     int
	// Int2 is the second integer payload (EventHP's bar value).
	Int2 int
	// Loop and MusicEffects carry MC looping (field 3) and MUSIC_EFFECT flags (field 5); only EventMusic sets them.
	Loop         bool
	MusicEffects int
	// Wipe is set_background's display parameter (packet_distribution.cpp:352, courtroom.cpp:1427): a BN with a pos.
	// Only the reducer sets it; client-made EventBackgrounds must leave it false or they erase what they re-seed.
	Wipe bool
}

// LivePlayer is one entry of the list PlayerStateObserver streams as PR and PU (../akashi/src/playerstateobserver.cpp).
type LivePlayer struct {
	ID       int
	OOCName  string
	Char     string
	Showname string
	AreaID   int
	// IPID is mod-only: WAP forks append "(<hex>)" to a mod's PU name (playerstateobserver.cpp). Never persisted.
	IPID string
}

// livePlayerCap bounds the roster map (hard rule 4); a reconnect re-dumps it, so hitting it self-heals.
const livePlayerCap = 1024

const livePlayersInitialCap = 16

// MUSIC_EFFECT flags in MC field 5 (datatypes.h:95-101). SYNC_POS is skipped: SDL_mixer can't cheaply seek mid-fetch.
const (
	musicEffectFadeIn   = 1
	musicEffectFadeOut  = 2
	musicEffectSyncPos  = 4
	musicEffectNoRepeat = 8
)

// PR roster-change types (akashi PacketPR::UPDATE_TYPE).
const (
	prJoin  = 0 // ADD
	prLeave = 1 // REMOVE
)

// PU field types (akashi PacketPU::DATA_TYPE); whether a client may see one is the server's decision.
const (
	puOOCName  = 0
	puChar     = 1
	puShowname = 2
	puAreaID   = 3
	puIPID     = 4
)

// ipidHexMin and ipidHexMax bound the WAP token: 8 hex chars (aoclient.cpp calculateIpid), clear of words like "cafe".
const (
	ipidHexMin = 6
	ipidHexMax = 12
)

// embedsIPIDInName is true only for WAP; elsewhere a parenthesised name could become a wrong-target -i ban.
func (s *Session) embedsIPIDInName() bool {
	return DetectSoftware(s.Software) == SoftwareWitches
}

// splitTrailingIPID peels "(<hex>)" off a WAP PU name (playerstateobserver.cpp); callers gate on embedsIPIDInName.
func splitTrailingIPID(s string) (name, ipid string) {
	t := strings.TrimRight(s, " \t\r\n")
	if !strings.HasSuffix(t, ")") {
		return s, ""
	}
	open := strings.LastIndexByte(t, '(') // the LAST group — the IPID is always the final token
	if open < 0 {
		return s, ""
	}
	tok := t[open+1 : len(t)-1]
	if !looksLikeIPID(tok) {
		return s, ""
	}
	return strings.TrimRight(t[:open], " \t\r\n"), tok
}

// looksLikeIPID reports all-hex within the bounds, so a word in parens isn't mistaken for one.
func looksLikeIPID(tok string) bool {
	if len(tok) < ipidHexMin || len(tok) > ipidHexMax {
		return false
	}
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// Players returns the roster sorted by UID, empty without PR/PU; it allocates, so call on EventPlayersUpdated only.
func (s *Session) Players() []LivePlayer {
	if len(s.players) == 0 {
		return nil
	}
	out := make([]LivePlayer, 0, len(s.players))
	for _, p := range s.players {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PlayerArea returns a player's live area id and whether they are known.
func (s *Session) PlayerArea(id int) (int, bool) {
	if pl := s.players[id]; pl != nil {
		return pl.AreaID, true
	}
	return 0, false
}

// touchPlayer returns or creates an entry, nil only at the cap; PR and PU can arrive in either order.
func (s *Session) touchPlayer(id int) *LivePlayer {
	if pl := s.players[id]; pl != nil {
		return pl
	}
	if s.players == nil {
		s.players = make(map[int]*LivePlayer, livePlayersInitialCap)
	}
	if len(s.players) >= livePlayerCap {
		return nil
	}
	pl := &LivePlayer{ID: id}
	s.players[id] = pl
	return pl
}

// Session reduces server packets to state, replies and UI events; it owns no goroutines.
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
	// MusicTrack is the last real MC track ("" = stopped), kept here so a room rebuilt later can resume it.
	MusicTrack string
	// LastIC is the latest well-formed MS, for a rebuilt room's RestoreMessage; cleared on SI.
	// An ignored last speaker restores nothing, and a restored speaker is unstyled until their next change.
	LastIC   *protocol.ChatMessage
	MyCharID int

	// Muted is set by MU on our cid or -1, cleared by UM (set_mute, courtroom.cpp:4689-4711); PV resets it (per cid).
	Muted bool

	// Live court state; mutated only by HandlePacket on the caller's loop.
	HPDef    int                    // defense penalty bar, 0..10
	HPPro    int                    // prosecution penalty bar, 0..10
	AreaInfo []AreaInfo             // ARUP columns, parallel to Areas
	players  map[int]*LivePlayer    // live roster (PR/PU), keyed by server UID; ≤ livePlayerCap
	Evidence []EvidenceItem         // LE list (≤ evidenceCap)
	Timers   [TimerCount]TimerState // TI server clocks
	PosList  []string               // SD dropdown entries (≤ posListCap)
	// latency and pingSentAt time the CH to CHECK round trip for TI type-0 clocks.
	latency    time.Duration
	pingSentAt time.Time
	// Judge is the JD state (JudgePosDependent until the server says).
	Judge int
	// ModGranted reports mod authentication (AUTH 1, or the legacy OOC line without auth_packet).
	ModGranted bool

	// GroupPair is the active roster (GP), ordered front to back including the speaker; nil when not in a group.
	GroupPair *protocol.GroupPair

	// Voice state (VS_* relay); mutated only by HandlePacket on the caller's loop.
	voiceCaps     VoiceCaps
	voicePeers    map[int]bool // uids currently in voice (≤ voicePeerCap)
	voiceSpeaking map[int]bool // uids currently transmitting (subset of voicePeers)

	// Rehearsal marks an offline session: picks resolve locally and sends are swallowed.
	Rehearsal bool

	phase   SessionPhase
	sendErr error
}

func NewSession(send func(protocol.Packet) error, hdid string) *Session {
	return &Session{
		send:     send,
		HDID:     hdid,
		Features: protocol.ParseFeatures(nil),
		MyCharID: protocol.UnpairedCharID,
		phase:    PhaseGreeting,
		// Full bars until the join-time HP packets land.
		HPDef: HPBarMax,
		HPPro: HPBarMax,
		// Pos-dependent until a JD packet overrides (judge_state = POS_DEPENDENT).
		Judge: JudgePosDependent,
	}
}

// NewRehearsalSession builds an offline session over remembered state; picks resolve locally, sends are swallowed.
func NewRehearsalSession(origin string, chars []string) *Session {
	s := NewSession(func(protocol.Packet) error { return nil }, "")
	s.Rehearsal = true
	s.phase = PhaseReady
	s.AssetURL = origin
	s.Chars = make([]CharacterSlot, len(chars))
	for i, name := range chars {
		s.Chars[i] = CharacterSlot{Name: name}
	}
	return s
}

func (s *Session) Phase() SessionPhase { return s.phase }

// SendErr reports the first failed reply write, which signals teardown.
func (s *Session) SendErr() error { return s.sendErr }

func (s *Session) reply(p protocol.Packet) {
	if s.sendErr != nil {
		return
	}
	if err := s.send(p); err != nil {
		s.sendErr = fmt.Errorf("courtroom: sending %s: %w", p.Header, err)
	}
}

// KFOCompat reports a KFO-Server: validate_net_cmd rejects empty STR frame and effect fields, so MS must fill them.
func (s *Session) KFOCompat() bool {
	return strings.Contains(strings.ToLower(s.Software), "kfo")
}

// handleTyped reduces an aolib typed packet; ok=false for headers whose model isn't a faithful superset of ours.
func (s *Session) handleTyped(t aolib.Outgoing) (ev []Event, ok bool) {
	switch v := t.(type) {
	case *aolib.Decryptor:
		// FantaCrypt is dead, so HI goes out plain.
		s.reply(protocol.NewTypedPacket(&aolib.HI{HDID: s.HDID}))
		return nil, true

	case *aolib.IDToClient:
		s.PlayerID = v.PlayerID
		s.Software = v.Software
		s.reply(protocol.NewTypedPacket(&aolib.IDToServer{Software: protocol.ClientName, Version: protocol.Version}))
		return nil, true

	case *aolib.FL:
		s.Features = protocol.ParseFeatures(v.Features)
		// Advertise only features the server itself supports.
		if s.Features.Has(protocol.FeatureGroupPair) {
			s.reply(protocol.NewTypedPacket(&aolib.FL{Features: []string{protocol.FeatureGroupPair}}))
		}
		return nil, true

	case *aolib.PN:
		// Population marker: joining is client-initiated from here.
		if s.phase == PhaseGreeting {
			s.reply(protocol.NewTypedPacket(&aolib.Askchaa{}))
		}
		return nil, true

	case *aolib.ASS:
		if decoded, err := url.PathUnescape(v.AssetUrl); err == nil && decoded != "" {
			s.AssetURL = decoded
		} else {
			s.AssetURL = v.AssetUrl
		}
		return []Event{{Kind: EventAssetURL, Text: s.AssetURL}}, true

	case *aolib.SI:
		s.phase = PhaseLoading
		s.Chars = s.Chars[:0]
		s.Music = s.Music[:0]
		s.MusicTrack = "" // fresh handshake: let the server's join MC repopulate (no stale resume)
		s.LastIC = nil    // ...and don't re-stage another room's message after a rejoin
		s.Muted = false   // ...and start unmuted on a rejoin
		s.Areas = s.Areas[:0]
		s.reply(protocol.NewTypedPacket(&aolib.RC{}))
		return nil, true

	case *aolib.SC:
		for _, it := range v.CharData {
			s.Chars = append(s.Chars, CharacterSlot{Name: it.Name, Description: it.Desc})
		}
		s.reply(protocol.NewTypedPacket(&aolib.RM{}))
		return []Event{{Kind: EventCharsUpdated}}, true

	case *aolib.CharsCheck:
		for i := 0; i < len(v.Taken) && i < len(s.Chars); i++ {
			s.Chars[i].Taken = v.Taken[i] == aolib.CharAvailabilityTaken
		}
		return []Event{{Kind: EventCharsUpdated}}, true

	case *aolib.PR:
		id := v.ID
		if id < 0 {
			return nil, true
		}
		if v.Type == aolib.PlayerListUpdateRemove {
			delete(s.players, id)
		} else {
			s.touchPlayer(id) // join (or a benign re-add); fields follow via PU
		}
		return []Event{{Kind: EventPlayersUpdated}}, true

	case *aolib.FA:
		// Fetch-areas refresh: the AREA list is replaced and the ARUP table resets.
		s.Areas = append(s.Areas[:0], v.Areas...)
		s.AreaInfo = make([]AreaInfo, len(s.Areas))
		for i := range s.AreaInfo {
			s.AreaInfo[i].Players = -1
		}
		return []Event{{Kind: EventAreasUpdated}}, true

	case *aolib.FM:
		// Fetch-music refresh: the MUSIC list alone is replaced, live.
		s.Music = s.Music[:0]
		for _, it := range v.MusicList {
			s.Music = append(s.Music, it.Name)
		}
		return nil, true

	case *aolib.DONE:
		s.phase = PhaseReady
		return []Event{{Kind: EventReady}}, true

	case *aolib.PV:
		s.MyCharID = v.CharID
		// A mute is keyed to a cid; a new cid voids the old mute.
		s.Muted = false
		return []Event{{Kind: EventCharPicked, Int: s.MyCharID}}, true

	case *aolib.CTToClient:
		// Servers without auth_packet confirm a /login with this exact OOC line; treat it as AUTH 1.
		if !s.Features.Has(protocol.FeatureAuthPacket) && v.Message == "Logged in as a moderator." {
			s.ModGranted = true
			return []Event{
				{Kind: EventOOC, Name: v.Name, Text: v.Message},
				{Kind: EventAuth, Int: 1},
			}, true
		}
		return []Event{{Kind: EventOOC, Name: v.Name, Text: v.Message}}, true

	case *aolib.KK:
		return []Event{{Kind: EventDisconnect, Text: "Kicked: " + capServerText(v.Reason)}}, true
	case *aolib.KB:
		return []Event{{Kind: EventDisconnect, Text: "Banned: " + capServerText(v.Reason)}}, true
	case *aolib.BD:
		return []Event{{Kind: EventDisconnect, Text: "Banned: " + capServerText(v.Reason)}}, true

	case *aolib.CHECK:
		// The tsuserver family answers every CH with CHECK; it only dates the clock correction.
		s.notePong()
		return nil, true

	case *aolib.HPToClient:
		if v.Value < 0 || v.Value > HPBarMax {
			return nil, true
		}
		var bar int
		switch v.Bar {
		case aolib.PenaltyBarDefense:
			bar, s.HPDef = 1, v.Value
		case aolib.PenaltyBarProsecution:
			bar, s.HPPro = 2, v.Value
		default:
			return nil, true
		}
		return []Event{{Kind: EventHP, Int: bar, Int2: v.Value}}, true

	case *aolib.ZZToClient:
		// Incoming modcall broadcast: the body is the pre-formatted notice line.
		if v.Reason == "" {
			return nil, true
		}
		return []Event{{Kind: EventModcall, Text: v.Reason}}, true

	case *aolib.ARUP:
		for i, d := range v.UpdateData {
			if i >= len(s.AreaInfo) {
				break
			}
			switch v.UpdateType {
			case aolib.AreaUpdateTypePlayerCount:
				s.AreaInfo[i].Players = parse.AtoiOr(d, -1)
			case aolib.AreaUpdateTypeStatus:
				s.AreaInfo[i].Status = d
			case aolib.AreaUpdateTypeCaseManager:
				s.AreaInfo[i].CM = d
			case aolib.AreaUpdateTypeLocked:
				s.AreaInfo[i].Lock = d
			}
		}
		return []Event{{Kind: EventAreasUpdated}}, true

	case *aolib.JD:
		var n int
		switch v.State {
		case aolib.JudgeStateByPosition:
			n = JudgePosDependent
		case aolib.JudgeStateHidden:
			n = JudgeHide
		case aolib.JudgeStateShown:
			n = JudgeShow
		default:
			return nil, true
		}
		s.Judge = n
		return []Event{{Kind: EventJudge, Int: n}}, true

	case *aolib.AUTH:
		if !s.Features.Has(protocol.FeatureAuthPacket) {
			return nil, true
		}
		var n int
		switch v.AuthState {
		case aolib.AuthStateLogout:
			n = -1
		case aolib.AuthStateFailed:
			n = 0
		case aolib.AuthStateSuccess:
			n = 1
		default:
			return nil, true
		}
		s.ModGranted = n >= 1
		return []Event{{Kind: EventAuth, Int: n}}, true

	case *aolib.SP:
		// SP#<pos>: the server forces our position (set_side).
		if v.Side == "" {
			return nil, true
		}
		return []Event{{Kind: EventSetPos, Text: string(v.Side)}}, true

	case *aolib.BB:
		// Server popup notice (call_notice). Capped like the removal reasons.
		if v.Message == "" {
			return nil, true
		}
		return []Event{{Kind: EventNotice, Text: capServerText(v.Message)}}, true
	}
	return nil, false
}

// HandlePacket reduces one server packet into state + events.
func (s *Session) HandlePacket(p protocol.Packet) []Event {
	if typed := p.Typed(); typed != nil {
		if ev, ok := s.handleTyped(typed); ok {
			return ev
		}
	}
	switch p.Header {
	case "decryptor":
		// FantaCrypt is dead, so HI goes out plain.
		s.reply(protocol.NewTypedPacket(&aolib.HI{HDID: s.HDID}))

	case "ID":
		s.PlayerID = parse.AtoiOr(p.Field(0), 0)
		s.Software = p.Field(1)
		s.reply(protocol.NewTypedPacket(&aolib.IDToServer{Software: protocol.ClientName, Version: protocol.Version}))

	case "FL":
		s.Features = protocol.ParseFeatures(p.Fields)
		if s.Features.Has(protocol.FeatureGroupPair) {
			s.reply(protocol.NewTypedPacket(&aolib.FL{Features: []string{protocol.FeatureGroupPair}}))
		}

	case "GP":
		if gp := protocol.GroupPairFromPacket(p); gp != nil {
			s.GroupPair = gp
		} else {
			s.GroupPair = nil
		}
		return []Event{{Kind: EventGroupPair}}

	case "PN":
		// Client-initiated from here: webAO sends askchaa on PN, AO2 at join (networkmanager.cpp join_to_server).
		// Re-broadcasts must not reload the lists.
		if s.phase == PhaseGreeting {
			s.reply(protocol.NewTypedPacket(&aolib.Askchaa{}))
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
		s.MusicTrack = "" // fresh handshake: let the server's join MC repopulate (no stale resume)
		s.LastIC = nil    // ...and don't re-stage another room's message after a rejoin
		s.Muted = false   // ...and start unmuted on a rejoin (the server re-sends MU if still muted)
		s.Areas = s.Areas[:0]
		s.reply(protocol.NewTypedPacket(&aolib.RC{}))

	case "SC":
		for _, field := range p.Fields {
			// AO2 splits on & and decodes each part again (packet_distribution.cpp).
			parts := strings.Split(field, "&")
			slot := CharacterSlot{Name: protocol.DecodeField(parts[0])}
			if len(parts) > 1 {
				slot.Description = protocol.DecodeField(parts[1])
			}
			s.Chars = append(s.Chars, slot)
		}
		s.reply(protocol.NewTypedPacket(&aolib.RM{}))
		return []Event{{Kind: EventCharsUpdated}}

	case "CharsCheck":
		for i := 0; i < len(p.Fields) && i < len(s.Chars); i++ {
			s.Chars[i].Taken = p.Fields[i] == "-1"
		}
		return []Event{{Kind: EventCharsUpdated}}

	case "PR":
		// PR#<id>#<type>#%: streamed from connect by PlayerStateObserver (../akashi/src/playerstateobserver.cpp).
		id := parse.AtoiOr(p.Field(0), -1)
		if id < 0 {
			return nil
		}
		if parse.AtoiOr(p.Field(1), prJoin) == prLeave {
			delete(s.players, id)
		} else {
			s.touchPlayer(id) // join (or a benign re-add); fields follow via PU
		}
		return []Event{{Kind: EventPlayersUpdated}}

	case "PU":
		// PU#<id>#<type>#<data>#%: PR and PU may come in either order; name and showname ship wire-escaped.
		id := parse.AtoiOr(p.Field(0), -1)
		if id < 0 {
			return nil
		}
		pl := s.touchPlayer(id)
		if pl == nil {
			return nil // at the cap; reconnect re-dumps the roster
		}
		switch parse.AtoiOr(p.Field(1), -1) {
		case puOOCName:
			// WAP forks put a mod's target IPID on the name (playerstateobserver.cpp); gated so others stay intact.
			name := protocol.DecodeField(p.Field(2))
			if s.embedsIPIDInName() {
				if stripped, ipid := splitTrailingIPID(name); ipid != "" {
					pl.IPID = ipid      // sticky: a later clean-name update won't clear it
					if stripped == "" { // a bare "(ipid)" follow-up carries no name — keep the one we have
						break
					}
					name = stripped
				}
			}
			pl.OOCName = name
		case puChar:
			pl.Char = protocol.DecodeField(p.Field(2))
		case puShowname:
			pl.Showname = protocol.DecodeField(p.Field(2))
		case puAreaID:
			pl.AreaID = parse.AtoiOr(p.Field(2), 0)
		case puIPID:
			// Dedicated IPID field, sticky across updates. The format is opaque per server: only non-empty is checked.
			if ipid := protocol.DecodeField(p.Field(2)); ipid != "" {
				pl.IPID = ipid
			}
		}
		return []Event{{Kind: EventPlayersUpdated}}

	case "SM":
		s.Areas, s.Music = splitAreasAndMusic(p.Fields)
		// -1 players means not yet reported; servers without arup never will.
		s.AreaInfo = make([]AreaInfo, len(s.Areas))
		for i := range s.AreaInfo {
			s.AreaInfo[i].Players = -1
		}
		s.reply(protocol.NewTypedPacket(&aolib.RD{}))

	case "FM":
		// FM replaces the music list live (packet_distribution.cpp "FM"), as servers push one on area moves.
		s.Music = append(s.Music[:0], p.Fields...)

	case "FA":
		// FA replaces the area list and resets ARUP to unknown (AO2 clear_areas + arup_clear).
		s.Areas = append(s.Areas[:0], p.Fields...)
		s.AreaInfo = make([]AreaInfo, len(s.Areas))
		for i := range s.AreaInfo {
			s.AreaInfo[i].Players = -1
		}
		return []Event{{Kind: EventAreasUpdated}}

	case "DONE":
		s.phase = PhaseReady
		return []Event{{Kind: EventReady}}

	case "MS":
		msg, err := protocol.ParseMS(p.Fields, s.Features, len(s.Chars))
		if err != nil {
			// Drop a malformed message like AO2, but show the reason on the debug overlay.
			return []Event{{Kind: EventDebug, Text: "MS dropped: " + err.Error()}}
		}
		// additional_chars (JSON-only): the group roster on every MS from a member.
		if typed, ok := p.Typed().(*aolib.MSToClient); ok {
			msg.Additional = protocol.ParseAdditionalChars(typed.Extras["additional_chars"])
		}
		s.LastIC = msg // the stage-restore seed for rooms rebuilt later (see field doc)
		return []Event{{Kind: EventMessage, Message: msg}}

	case "MC":
		// MC field positions past track (0), char id (1) and showname (2).
		const (
			mcLoopingField = 3
			mcChannelField = 4
			mcEffectsField = 5
		)
		// Field 0 is the track; fields 1-2 are optional. Trim it, or a /play link's leading space misses isMusicURL.
		track := strings.TrimSpace(p.Field(0))
		// Field 4 is the channel (courtroom.cpp handle_song: 0 is music). WAP streams ambience on channel 1 at join,
		// which must not replace the song; we don't render ambience.
		if parse.AtoiOr(p.Field(mcChannelField), 0) != 0 {
			return nil
		}
		// Field 3 = looping, field 5 = MUSIC_EFFECT flags (handle_song, courtroom.cpp:4764-4787); both optional.
		looping := true
		if v := p.Field(mcLoopingField); v != "" {
			looping = v == "1"
		}
		effects := parse.AtoiOr(p.Field(mcEffectsField), 0)
		// NO_REPEAT overrides the looping field (aomusicplayer.cpp:31), so the audio sink's loop bool is authoritative.
		if effects&musicEffectNoRepeat != 0 {
			looping = false
		}
		// Keep the real song here, classified as in the courtroom: a stop clears it, an area transfer leaves it.
		switch {
		case track == "" || isAreaTransfer(track):
			// not a song — leave s.MusicTrack as-is
		case isMusicStop(track):
			s.MusicTrack = ""
		default:
			s.MusicTrack = track
		}
		return []Event{{Kind: EventMusic, Text: track, Int: parse.AtoiOr(p.Field(1), protocol.UnpairedCharID), Name: p.Field(2),
			Loop: looping, MusicEffects: effects}}

	case "BN":
		// packet_distribution.cpp:342-358. The wipe keys off arity: never soften to Field(1) != "", as akashi's side
		// empty (area_data.cpp) and KFO's remote-listen sends an empty pos (area.py), and AO2 wipes on both.
		if len(p.Fields) == 0 {
			return nil // AO2's content.isEmpty() guard; "" would burn a probe on background//
		}
		s.Background = p.Field(0)
		wipe := len(p.Fields) >= bnWipeFieldCount
		if !wipe {
			return []Event{{Kind: EventBackground, Text: s.Background}}
		}
		// A wiping BN retires the remembered message: a parked tab has no Courtroom, so only the reducer can stop it.
		s.LastIC = nil
		evs := make([]Event, 0, 2)
		if pos := p.Field(1); pos != "" {
			// set_side runs before set_background upstream so the re-stage sees the new pos; order matters.
			evs = append(evs, Event{Kind: EventSetPos, Text: pos})
		}
		// Name carries the pos so the wiped stage re-scenes at the new position.
		return append(evs, Event{Kind: EventBackground, Text: s.Background, Name: p.Field(1), Wipe: true})

	case "PV":
		s.MyCharID = parse.AtoiOr(p.Field(2), protocol.UnpairedCharID)
		// A mute is keyed to a cid (set_mute); clear it so a stale MU can't lock IC input after switching characters.
		s.Muted = false
		return []Event{{Kind: EventCharPicked, Int: s.MyCharID}}

	case "CT":
		// Servers without auth_packet confirm /login with this OOC line (courtroom.cpp append_server_chatmessage).
		if !s.Features.Has(protocol.FeatureAuthPacket) && p.Field(1) == "Logged in as a moderator." {
			s.ModGranted = true
			return []Event{
				{Kind: EventOOC, Name: p.Field(0), Text: p.Field(1)},
				{Kind: EventAuth, Int: 1},
			}
		}
		return []Event{{Kind: EventOOC, Name: p.Field(0), Text: p.Field(1)}}

	// Removal reasons are attacker-controlled, capped ahead of every consumer; cap the field (see capServerText).
	case "KK":
		return []Event{{Kind: EventDisconnect, Text: "Kicked: " + capServerText(p.Field(0))}}
	case "KB":
		return []Event{{Kind: EventDisconnect, Text: "Banned: " + capServerText(p.Field(0))}}
	case "BD":
		return []Event{{Kind: EventDisconnect, Text: "Banned: " + capServerText(p.Field(0))}}

	case "checkconnection":
		// Answer CH with our char id. Compatibility only: no maintained server sends it (packet_distribution.cpp:40).
		s.reply(protocol.NewTypedPacket(&aolib.CH{CharID: s.MyCharID}))
		s.stampPing() // the CHECK that answers this CH times the round trip

	case "CHECK":
		// The tsuserver family answers CH with CHECK (aoprotocol.py:248-253); AO2 times it (packet_distribution.cpp).
		s.notePong()

	case "HP":
		// HP#<bar 1=def|2=pro>#<0..10>#% (packet_distribution.cpp); courtroom.cpp set_hp_bar rejects bad states.
		bar, val := parse.AtoiOr(p.Field(0), 0), parse.AtoiOr(p.Field(1), -1)
		if val < 0 || val > HPBarMax {
			return nil
		}
		switch bar {
		case 1:
			s.HPDef = val
		case 2:
			s.HPPro = val
		default:
			return nil
		}
		return []Event{{Kind: EventHP, Int: bar, Int2: val}}

	case "RT":
		// Testimony splashes, verdicts and the 2.9 "testimony1#1" marker; see handle_wtce (courtroom.cpp:4846).
		if len(p.Fields) == 0 {
			return nil
		}
		return []Event{{Kind: EventWTCE, Text: p.Field(0), Int: parse.AtoiOr(p.Field(1), 0)}}

	case "ZZ":
		// The body is the pre-formatted notice line (courtroom.cpp mod_called).
		if len(p.Fields) == 0 {
			return nil
		}
		return []Event{{Kind: EventModcall, Text: p.Field(0)}}

	case "VS_CAPS", "VS_PEERS", "VS_JOIN", "VS_LEAVE", "VS_SPEAK", "VS_AUDIO":
		return s.handleVoicePacket(p)

	case "ARUP":
		// ARUP#<type>#<area0>#...: field n applies to area n-1; out-of-range entries drop (courtroom.cpp arup_modify).
		const (
			arupPlayers = iota
			arupStatus
			arupCM
			arupLock
		)
		typ := parse.AtoiOr(p.Field(0), -1)
		if typ < 0 || typ > arupLock {
			return nil
		}
		for i := 1; i < len(p.Fields); i++ {
			n := i - 1
			if n >= len(s.AreaInfo) {
				break
			}
			switch typ {
			case arupPlayers:
				s.AreaInfo[n].Players = parse.AtoiOr(p.Fields[i], -1)
			case arupStatus:
				s.AreaInfo[n].Status = p.Fields[i]
			case arupCM:
				s.AreaInfo[n].CM = p.Fields[i]
			case arupLock:
				s.AreaInfo[n].Lock = p.Fields[i]
			}
		}
		return []Event{{Kind: EventAreasUpdated}}

	case "TI":
		// TI#<id>#<type>#[<ms>]: ms <= 0 stops the clock (packet_distribution.cpp). Only type 0 gets the half-RTT fix.
		const (
			timerStart = iota
			timerPause
			timerShow
			timerHide
		)
		const timerMsField = 2
		id := parse.AtoiOr(p.Field(0), -1)
		if id < 0 || id >= TimerCount || len(p.Fields) < 2 {
			return nil
		}
		t := &s.Timers[id]
		switch parse.AtoiOr(p.Field(1), -1) {
		case timerStart:
			if len(p.Fields) <= timerMsField {
				return nil
			}
			if ms := parse.AtoiOr(p.Field(timerMsField), -1); ms > 0 {
				// Canon tests ms > 0 before subtracting, so a correction eating the remainder still starts the clock.
				t.Running = true
				t.Deadline = time.Now().Add(time.Duration(ms)*time.Millisecond - s.clockLead())
			} else {
				t.Running, t.Left = false, 0 // negative value = stop
			}
		case timerPause:
			if len(p.Fields) <= timerMsField {
				return nil
			}
			if ms := parse.AtoiOr(p.Field(timerMsField), -1); ms > 0 {
				t.Running, t.Left = false, time.Duration(ms)*time.Millisecond
			} else {
				t.Running, t.Left = false, 0
			}
		case timerShow:
			t.Visible = true
		case timerHide:
			t.Visible = false
		default:
			return nil
		}
		return []Event{{Kind: EventTimer, Int: id}}

	case "JD":
		// JD#<state>: -1 client-side buttons, 0 hide, 1 show; malformed packets are ignored (packet_distribution.cpp).
		n, err := strconv.Atoi(strings.TrimSpace(p.Field(0)))
		if err != nil {
			return nil
		}
		s.Judge = n
		return []Event{{Kind: EventJudge, Int: n}}

	case "AUTH":
		// AUTH#<state>: 1+ granted, 0 failed, <0 logged out; only with auth_packet, as in packet_distribution.cpp.
		if !s.Features.Has(protocol.FeatureAuthPacket) || len(p.Fields) == 0 {
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(p.Field(0)))
		if err != nil {
			return []Event{{Kind: EventDebug, Text: "malformed AUTH: " + p.Field(0)}}
		}
		s.ModGranted = n >= 1
		return []Event{{Kind: EventAuth, Int: n}}

	case "SD":
		// SD#<pos1*pos2*...>: position dropdown (set_pos_dropdown splits on '*').
		if p.Field(0) == "" {
			return nil
		}
		list := strings.Split(p.Field(0), "*")
		if len(list) > posListCap {
			list = list[:posListCap]
		}
		s.PosList = list
		return []Event{{Kind: EventPosList}}

	case "SP":
		// SP#<pos>: the server forces our position (set_side).
		if p.Field(0) == "" {
			return nil
		}
		return []Event{{Kind: EventSetPos, Text: p.Field(0)}}

	case "LE":
		// Same nested decode as SC (packet_distribution.cpp LE "decoding has to be done here").
		s.Evidence = s.Evidence[:0]
		for _, field := range p.Fields {
			if len(s.Evidence) >= evidenceCap {
				break
			}
			parts := strings.Split(field, "&")
			if len(parts) < evidenceFields {
				continue
			}
			s.Evidence = append(s.Evidence, EvidenceItem{
				Name:        protocol.DecodeField(parts[0]),
				Description: protocol.DecodeField(parts[1]),
				Image:       protocol.DecodeField(parts[2]),
			})
		}
		return []Event{{Kind: EventEvidence}}

	case "PE":
		// tsuserver-family servers echo PE rather than resend LE, so signal a single-item change.
		if len(p.Fields) < 3 || len(s.Evidence) >= evidenceCap {
			return nil
		}
		name := protocol.DecodeField(p.Field(0))
		s.Evidence = append(s.Evidence, EvidenceItem{
			Name:        name,
			Description: protocol.DecodeField(p.Field(1)),
			Image:       protocol.DecodeField(p.Field(2)),
		})
		return []Event{{Kind: EventEvidenceChanged, Int: len(s.Evidence) - 1, Int2: EvidenceOpAdd, Text: name}}

	case "EE":
		// EE#id#name#desc#image; out-of-range ids drop, and the server re-syncs with an LE.
		const eeImageField = 3
		id := parse.AtoiOr(p.Field(0), -1)
		if id < 0 || id >= len(s.Evidence) {
			return nil
		}
		s.Evidence[id] = EvidenceItem{
			Name:        protocol.DecodeField(p.Field(1)),
			Description: protocol.DecodeField(p.Field(2)),
			Image:       protocol.DecodeField(p.Field(eeImageField)),
		}
		return []Event{{Kind: EventEvidenceChanged, Int: id, Int2: EvidenceOpEdit, Text: s.Evidence[id].Name}}

	case "DE":
		// DE#id.
		id := parse.AtoiOr(p.Field(0), -1)
		if id < 0 || id >= len(s.Evidence) {
			return nil
		}
		name := s.Evidence[id].Name
		s.Evidence = append(s.Evidence[:id], s.Evidence[id+1:]...)
		return []Event{{Kind: EventEvidenceChanged, Int: id, Int2: EvidenceOpDelete, Text: name}}

	case "MU", "UM":
		// MU mutes, UM unmutes (packet_distribution.cpp:483-497); set_mute acts on our cid or -1 (courtroom.cpp:4691).
		if len(p.Fields) == 0 || p.Field(0) == "" {
			return nil
		}
		cid := parse.AtoiOr(p.Field(0), protocol.UnpairedCharID)
		if cid != s.MyCharID && cid != protocol.UnpairedCharID {
			return nil // not us and not mute-all
		}
		muted := p.Header == "MU"
		if s.Muted == muted {
			return nil // no change (redundant packet) — don't spam the chip
		}
		s.Muted = muted
		return []Event{{Kind: EventMuted, Int: boolToInt(muted)}}

	case "BB":
		// AO2 shows a modal (courtroom.cpp call_notice); we also mirror to OOC, as a transient line gets missed.
		if len(p.Fields) == 0 {
			return nil
		}
		return []Event{{Kind: EventNotice, Text: capServerText(p.Field(0))}}

	case "CASEA":
		// CASEA#<msg>#<def>#<pro>#<judge>#<jury>#<steno>; removed upstream (serverdata.h), still sent by tsuserver.
		const caseaFields = 6 // msg + five role flags
		if len(p.Fields) < caseaFields {
			return nil
		}
		need := 0
		for i, bit := range [...]int{CaseRoleDef, CaseRolePro, CaseRoleJudge, CaseRoleJury, CaseRoleSteno} {
			if p.Field(i+1) == "1" {
				need |= bit
			}
		}
		return []Event{{Kind: EventCase, Text: p.Field(0), Int: need}}

	default:
		// Ignoring unimplemented headers is harmless, but they must be diagnosable: debug overlay only.
		return []Event{{Kind: EventDebug,
			Text: fmt.Sprintf("unhandled packet %q (%d fields)", p.Header, len(p.Fields))}}
	}
	return nil
}

// splitAreasAndMusic mirrors AO2's SM scan (packet_distribution.cpp): before the first audio extension are areas.
// fix_last_area (courtroom.cpp:613): the entry just before the first song is a music category header.
func splitAreasAndMusic(fields []string) (areas, music []string) {
	musicStart := len(fields)
	for i, f := range fields {
		if HasAudioExt(f) {
			musicStart = i
			break
		}
	}
	// fix_last_area: shift the boundary back one so the category header lands in music.
	if musicStart > 0 && musicStart < len(fields) {
		musicStart--
	}
	areas = append(areas, fields[:musicStart]...)
	music = append(music, fields[musicStart:]...)
	return areas, music
}

var audioExts = []string{".wav", ".mp3", ".mp4", ".ogg", ".opus"}

// HasAudioExt reports a known AO audio extension; exported for the UI's music-list grouping.
func HasAudioExt(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range audioExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func (s *Session) PickCharacter(charID int) {
	s.reply(protocol.NewTypedPacket(&aolib.CC{PlayerID: s.PlayerID, CharID: charID, CharPassword: s.HDID}))
}

// SendChat sends an IC message shaped for this server's features.
func (s *Session) SendChat(msg protocol.OutgoingMS) {
	s.reply(msg.Packet(s.Features))
}

// SendOOC sends an out-of-character line.
func (s *Session) SendOOC(name, text string) {
	s.reply(protocol.NewTypedPacket(&aolib.CTToServer{Name: name, Message: text}))
}

// RequestMusic is the two-field area-jump shape (courtroom.cpp:6036-6044); a server may read field 3 as a showname.
func (s *Session) RequestMusic(track string) {
	s.reply(protocol.NewTypedPacket(&aolib.MCToServer{Name: track, CharID: s.MyCharID}))
}

// RequestMusicWithFlags is AO2's track-play shape, MC#<song>#<cid>[#<showname>][#<flags>] (courtroom.cpp:5806-5819).
func (s *Session) RequestMusicWithFlags(track, showname string, flags int) {
	const mcMaxFields = 4 // track, char id, showname, effects
	fields := make([]string, 0, mcMaxFields)
	fields = append(fields, track, strconv.Itoa(s.MyCharID))
	effects := s.Features.Has(protocol.FeatureEffects)
	if (showname != "" && s.Features.Has(protocol.FeatureCCCCIC)) || effects {
		fields = append(fields, showname)
	}
	if effects {
		fields = append(fields, strconv.Itoa(flags))
	}
	s.reply(protocol.NewPacket("MC", fields...))
}

// KeepalivePacket returns the CH string for Conn.SetKeepalive. Read on the render thread, which owns MyCharID.
func (s *Session) KeepalivePacket() string {
	return protocol.NewPacket("CH", strconv.Itoa(s.MyCharID)).String()
}

// CallMod sends a mod call; the second field is the target, -1 = whole area (on_call_mod_clicked, courtroom.cpp:6530).
func (s *Session) CallMod(reason string) {
	if s.Features.Has(protocol.FeatureModcallReason) {
		s.reply(protocol.NewTypedPacket(&aolib.ZZToServer{Reason: reason, ReportedPlayerID: -1}))
		return
	}
	s.reply(protocol.NewPacket("ZZ"))
}

// SendHP submits HP#<bar 1=def|2=pro>#<0..10> (courtroom.cpp on_defense_minus_clicked et al.).
func (s *Session) SendHP(bar, state int) {
	if state < 0 || state > HPBarMax || (bar != 1 && bar != 2) {
		return
	}
	side := aolib.PenaltyBarDefense
	if bar == 2 {
		side = aolib.PenaltyBarProsecution
	}
	s.reply(protocol.NewTypedPacket(&aolib.HPToServer{Bar: side, Value: state}))
}

// SendWTCE fires a judge splash: judgeruling takes the verdict variant (courtroom.cpp judge button handlers).
func (s *Session) SendWTCE(name string, variant int) {
	if name == "judgeruling" {
		s.reply(protocol.NewPacket("RT", name, strconv.Itoa(variant)))
		return
	}
	s.reply(protocol.NewPacket("RT", name))
}

// AddEvidence appends a global evidence item (PE#name#desc#image).
func (s *Session) AddEvidence(name, desc, image string) {
	s.reply(protocol.NewTypedPacket(&aolib.PE{Name: name, Description: desc, Image: image}))
}

// DeleteEvidence removes the item at index (DE#id).
func (s *Session) DeleteEvidence(id int) {
	s.reply(protocol.NewTypedPacket(&aolib.DE{ID: id}))
}

// EditEvidence replaces the item at index (EE#id#name#desc#image).
func (s *Session) EditEvidence(id int, name, desc, image string) {
	s.reply(protocol.NewTypedPacket(&aolib.EE{ID: id, Name: name, Description: desc, Image: image}))
}

// SetCasingPrefs subscribes to CASEA by role; the leading field is a legacy blob (tsuserver net_cmd_setcase skips it).
func (s *Session) SetCasingPrefs(def, pro, judge, jury, steno bool) {
	s.reply(protocol.NewPacket("SETCASE", "",
		boolWire(def), boolWire(pro), boolWire(judge), boolWire(jury), boolWire(steno)))
}

func boolWire(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
