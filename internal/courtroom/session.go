package courtroom

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

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

// String names the phase for diagnostics (debug overlay health line).
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

// CharacterSlot is one server character list entry.
type CharacterSlot struct {
	Name        string
	Description string
	Taken       bool
}

// AreaInfo is the live ARUP state for one area (AO2-Client arup_players/
// statuses/cms/locks, courtroom.cpp arup_modify).
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

// EvidenceItem is one LE list entry (packet_distribution.cpp: each field
// splits on '&' into name & description & image, each sub-element decoded
// again, exactly like SC).
type EvidenceItem struct {
	Name        string
	Description string
	Image       string
}

// TimerState is one TI server clock. The 2.8 protocol defines up to five
// (AO2-Client max_clocks); time is computed client-side from a deadline so
// rendering never depends on packet cadence.
type TimerState struct {
	Visible bool
	Running bool
	// Deadline is the wall-clock zero point while Running.
	Deadline time.Time
	// Left is the frozen remainder while paused.
	Left time.Duration
}

// Remaining reports the time the clock should display right now.
func (t *TimerState) Remaining(now time.Time) time.Duration {
	if t.Running {
		if d := t.Deadline.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return t.Left
}

// Judge-control states from the JD packet (AO2-Client Courtroom::JudgeState).
const (
	// JudgePosDependent (-1): fall back to client-side behavior — controls
	// show while our pos is the judge stand.
	JudgePosDependent = -1
	// JudgeHide (0): server says hide the judge controls.
	JudgeHide = 0
	// JudgeShow (1): server granted the judge controls.
	JudgeShow = 1
)

// Casing role bits for CASEA alerts (wire order def#pro#judge#jury#steno;
// legacy 2.6–2.9 casing_alerts, removed upstream but still served by
// tsuserver-family servers).
const (
	CaseRoleDef = 1 << iota
	CaseRolePro
	CaseRoleJudge
	CaseRoleJury
	CaseRoleSteno
)

const (
	// TimerCount caps TI clocks (AO2-Client max_clocks = 5).
	TimerCount = 5
	// evidenceCap bounds the LE list — a hostile server cannot balloon
	// memory (rule §17.4).
	evidenceCap = 512
	// posListCap bounds the SD dropdown for the same reason.
	posListCap = 64
	// HPBarMax is the top HP pip count (AO2-Client set_hp_bar guards 0..10;
	// exported because the UI draws defensebar0..defensebar<HPBarMax>).
	HPBarMax = 10
)

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
	// EventMusic carries a music change (MC): Text=track, Int=charID,
	// Name=showname (field 2, may be ""). charID/showname name who played it
	// for the IC "has played a song" log line (AO2 handle_song).
	EventMusic
	// EventOOC carries server/OOC chat: Name + Text.
	EventOOC
	// EventCharPicked confirms our character (PV): Int=char id.
	EventCharPicked
	// EventAssetURL announces the server's asset repository (ASS).
	EventAssetURL
	// EventDisconnect carries kick/ban notices.
	EventDisconnect
	// EventDebug carries protocol-level diagnostics (unhandled packet
	// headers, dropped malformed messages) for the UI's debug overlay.
	// Consumers may ignore it freely; Courtroom.HandleEvent does.
	EventDebug
	// EventHP is a penalty-bar change: Int = bar (1 def, 2 pro), Int2 = 0..10.
	EventHP
	// EventWTCE is a testimony/verdict splash: Text = wtce id
	// (testimony1/testimony2/judgeruling/custom), Int = variant.
	EventWTCE
	// EventModcall carries the server's modcall broadcast line (ZZ).
	EventModcall
	// EventAreasUpdated signals ARUP state changed (UI re-reads AreaInfo).
	EventAreasUpdated
	// EventJudge carries the JD judge-controls state in Int.
	EventJudge
	// EventAuth carries the AUTH state in Int (1+ mod, 0 failed, <0 logout).
	EventAuth
	// EventSetPos carries the side the server forced (SP) in Text.
	EventSetPos
	// EventPosList signals the SD dropdown list arrived (read PosList).
	EventPosList
	// EventCase is a CASEA announcement: Text = message, Int = needed-role
	// bits (CaseRole*).
	EventCase
	// EventNotice is a BB popup notice from the server.
	EventNotice
	// EventEvidence signals the LE list was replaced (read Evidence).
	EventEvidence
	// EventTimer signals TI clock Int changed (read Timers).
	EventTimer
	// EventPlayersUpdated signals the live player list changed (PR/PU from the
	// server's PlayerStateObserver); the UI re-reads Players().
	EventPlayersUpdated
	// EventVoiceCaps signals the server's voice capability advert arrived
	// (VS_CAPS); the UI re-reads VoiceCaps() to decide whether to offer voice.
	EventVoiceCaps
	// EventVoicePeers signals the voice peer set changed (VS_PEERS/JOIN/LEAVE);
	// the UI re-reads VoicePeers().
	EventVoicePeers
	// EventVoiceSpeak is a peer speaking-state toggle (VS_SPEAK): Int = uid,
	// Int2 = 1 speaking / 0 stopped.
	EventVoiceSpeak
	// EventVoiceAudio is one inbound opus frame (VS_AUDIO): Int = from-uid,
	// Text = base64 opus payload. Consumed by the audio layer (decode + play).
	EventVoiceAudio
)

// Event is one session occurrence. Fields are populated per Kind.
type Event struct {
	Kind    EventKind
	Message *protocol.ChatMessage
	Name    string
	Text    string
	Int     int
	// Int2 is the second integer payload (EventHP's bar value).
	Int2 int
}

// LivePlayer is one entry of the server-pushed live player list. Akashi and
// Nyathena run a PlayerStateObserver that streams PR (join/leave) and PU (field
// update) packets to every client from connect — no /getarea, no opt-in — so the
// roster is live and carries the server UID (the id pairing and /getarea target).
// Char is "" for a spectator / a player still at character select. AreaID indexes
// Areas. (../akashi/src/playerstateobserver.cpp.)
type LivePlayer struct {
	ID       int
	OOCName  string
	Char     string
	Showname string
	AreaID   int
	// IPID is mod-only, and only some server families surface it in the live
	// list: the witches/wizards Akashi-party forks append it to a mod's PU NAME
	// field as a trailing "(<hex>)" token (../witches-akashi-party/src/
	// playerstateobserver.cpp). Empty on every other server / for non-mods; the
	// UI still falls back to a /getarea snapshot when it's blank. Never persisted.
	IPID string
}

// livePlayerCap bounds the live roster map (hard rule #4: no unbounded caches).
// Far above any real area population; a reconnect re-dumps the full roster, so a
// hit here self-heals rather than corrupting state.
const livePlayerCap = 1024

// PR roster-change types — PR#<id>#<type>#% (akashi PacketPR::UPDATE_TYPE).
const (
	prJoin  = 0 // ADD
	prLeave = 1 // REMOVE
)

// PU field types — PU#<id>#<type>#<data>#% (akashi PacketPU::DATA_TYPE).
const (
	puOOCName  = 0
	puChar     = 1
	puShowname = 2
	puAreaID   = 3
)

// ipidHexMin / ipidHexMax bound the parenthesised IPID token the witches-akashi-
// party fork appends to a mod's PU name. Akashi IPIDs are 8 hex chars (the last
// 4 bytes of a SHA hash — akashi/src/aoclient.cpp calculateIpid: toHex().right(8));
// the slack tolerates a fork widening it while staying clear of the short hex
// words a real OOC name might end in ("cafe", "beef").
const (
	ipidHexMin = 6
	ipidHexMax = 12
)

// embedsIPIDInName reports whether the connected server's family streams the
// mod-only IPID inside the PU name field. ONLY the witches-akashi-party (WAP)
// fork does (SoftwareWitches, announced as "WAP-Akashi") — the fork author draws
// exactly this line: stock Akashi has no PlayerStateObserver and never puts IPIDs
// in the player list, so it's deliberately kept out. This also excludes
// Athena/Nyathena/KFO/Whisker — they push PU packets too, so gating stops a
// parenthesised OOC name on those servers being mis-read as an IPID (which, via
// BanCommand's -i flag, could otherwise build a wrong-target ban).
func (s *Session) embedsIPIDInName() bool {
	return DetectSoftware(s.Software) == SoftwareWitches
}

// splitTrailingIPID peels a trailing "(<hex>)" IPID token off a witches-akashi-
// party PU name: "web71 (eea20f10)" → "web71", "eea20f10"; a bare "(eea20f10)"
// → "", "eea20f10". Returns the input unchanged with an empty ipid when there is
// no IPID-looking token, so an ordinary parenthesised name ("Bob (cafe)") — or
// any name on a server that doesn't do this — is left intact. Callers gate on
// embedsIPIDInName; the hex + length test is a second guard.
// (../witches-akashi-party/src/playerstateobserver.cpp.)
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

// looksLikeIPID reports whether tok is an Akashi-style hex IPID (all hex digits,
// within the length bounds), so splitTrailingIPID never mistakes a word-in-parens
// for one.
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

// Players returns the live roster (PR/PU) as a slice sorted by UID for a stable
// display order. Empty when the server doesn't push the player-list packets (an
// older tsuserver) — the UI then falls back to its CharsCheck-derived roster.
// Allocates a fresh slice; call it on EventPlayersUpdated, not per frame.
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

// PlayerArea returns the live (PR/PU) area id of player id and whether that
// player is currently known — the basis for follow-a-player (M3). O(1).
func (s *Session) PlayerArea(id int) (int, bool) {
	if pl := s.players[id]; pl != nil {
		return pl.AreaID, true
	}
	return 0, false
}

// touchPlayer returns player id's live entry, creating it (bounded by
// livePlayerCap) when absent. Returns nil only at the cap. A PR join and a
// field-first PU both route through here, so the roster survives either order.
func (s *Session) touchPlayer(id int) *LivePlayer {
	if pl := s.players[id]; pl != nil {
		return pl
	}
	if s.players == nil {
		s.players = make(map[int]*LivePlayer, 16)
	}
	if len(s.players) >= livePlayerCap {
		return nil
	}
	pl := &LivePlayer{ID: id}
	s.players[id] = pl
	return pl
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
	// MusicTrack is the area's currently-playing song (the last real MC track; "" =
	// nothing / stopped). Persisted on the session like Background so a room rebuilt
	// LATER — entering the courtroom after the join handshake already announced the
	// song, or a tab reactivation — can resume it (buildRoom re-seeds it). Without this
	// the track lived only on the throwaway Scene, so a song playing when the room is
	// (re)built fell silent while the background, tracked here, survived.
	MusicTrack string
	// LastIC is the most recent well-formed IC chat message (MS) this session
	// parsed — persisted like Background/MusicTrack so a room rebuilt LATER (a
	// tab reactivation, re-entering court, pinning a background tab) can
	// re-stage it settled (Courtroom.RestoreMessage) instead of coming back to
	// a blank stage. One replaced pointer, bounded by construction; cleared on
	// a fresh SI handshake like MusicTrack (no stale stage across a rejoin).
	//
	// Two known fidelity gaps, both degrading to the pre-restore status quo
	// (a blank / plainer stage, never a wrong render): it records EVERY
	// speaker — the #81 ignore list is active-tab UI state, so an ignored
	// LAST speaker shadows the previous non-ignored line and the restore
	// stages nothing; and the send-on-change memories (sprite style, status,
	// profile) live on the throwaway room, so a restored message that carried
	// no style marker re-stages its speaker unstyled until they next change.
	LastIC   *protocol.ChatMessage
	MyCharID int

	// Live court state (AO2-Client parity; all mutated only by HandlePacket
	// on the caller's loop — same single-threaded discipline as the rest).
	HPDef    int                    // defense penalty bar, 0..10
	HPPro    int                    // prosecution penalty bar, 0..10
	AreaInfo []AreaInfo             // ARUP columns, parallel to Areas
	players  map[int]*LivePlayer    // live roster (PR/PU), keyed by server UID; ≤ livePlayerCap
	Evidence []EvidenceItem         // LE list (≤ evidenceCap)
	Timers   [TimerCount]TimerState // TI server clocks
	PosList  []string               // SD dropdown entries (≤ posListCap)
	// Judge is the JD state (JudgePosDependent until the server says).
	Judge int
	// ModGranted reports mod authentication (AUTH 1, or the legacy OOC
	// confirmation line on servers without auth_packet).
	ModGranted bool

	// Voice (Nyathena/LemmyAO VS_* relay): the caps the server advertised, the
	// live voice peer set, and who's currently transmitting. All mutated only by
	// HandlePacket on the caller's loop, like the rest of the live state.
	voiceCaps     VoiceCaps
	voicePeers    map[int]bool // uids currently in voice (≤ voicePeerCap)
	voiceSpeaking map[int]bool // uids currently transmitting (subset of voicePeers)

	// Rehearsal marks an offline session (NewRehearsalSession): picks
	// resolve locally, sends are swallowed, nothing network exists.
	Rehearsal bool

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
		// Full bars until the server's join-time HP packets land (servers
		// send both immediately; 10/10 is the AO resting state).
		HPDef: HPBarMax,
		HPPro: HPBarMax,
		// Judge controls are pos-dependent until a JD packet overrides
		// (Courtroom::judge_state = POS_DEPENDENT initial).
		Judge: JudgePosDependent,
	}
}

// NewRehearsalSession builds an OFFLINE session over a server's
// remembered state: the character list browses, emotes play from cache,
// and every outgoing packet is swallowed — rehearse a character without
// connecting (or even a network). The UI resolves picks locally (no PV
// will ever arrive) and labels itself from the Rehearsal flag.
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

// KFOCompat reports whether the connected server is KFO-Server (from the ID packet's
// software name; tsuserver.py sets self.software = "KFO-Server"). KFO's MS validator
// (validate_net_cmd) rejects EMPTY strings for the frame/effect fields it types STR —
// where AO2-Client always sends a non-empty value — so the outgoing MS must fill them
// the same way. Scoped to KFO so every other server's wire is unchanged.
func (s *Session) KFOCompat() bool {
	return strings.Contains(strings.ToLower(s.Software), "kfo")
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
		s.MusicTrack = "" // fresh handshake: let the server's join MC repopulate (no stale resume)
		s.LastIC = nil    // ...and don't re-stage another room's message after a rejoin
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

	case "PR":
		// Player roster change (PR#<id>#<type>#%): the Akashi/Nyathena
		// PlayerStateObserver streams these to every client from connect — no
		// /getarea, no opt-in — so the live list reacts to joins/leaves and learns
		// each player's server UID. (../akashi/src/playerstateobserver.cpp.)
		id := atoiOr(p.Field(0), -1)
		if id < 0 {
			return nil
		}
		if atoiOr(p.Field(1), prJoin) == prLeave {
			delete(s.players, id)
		} else {
			s.touchPlayer(id) // join (or a benign re-add); fields follow via PU
		}
		return []Event{{Kind: EventPlayersUpdated}}

	case "PU":
		// Player field update (PU#<id>#<type>#<data>#%): type 0 OOC name, 1
		// character folder, 2 showname, 3 area id (an index into Areas). Servers
		// send PR before PU, but touchPlayer keeps it order-robust. Name/showname
		// ship wire-escaped like every other text field — decode them.
		id := atoiOr(p.Field(0), -1)
		if id < 0 {
			return nil
		}
		pl := s.touchPlayer(id)
		if pl == nil {
			return nil // at the cap; reconnect re-dumps the roster
		}
		switch atoiOr(p.Field(1), -1) {
		case puOOCName:
			// The witches/wizards Akashi-party forks stream a mod's target IPID
			// as a trailing "(<hex>)" token on the PU name ("web71 (eea20f10)"),
			// and as a bare "(eea20f10)" ADD follow-up. Peel it into pl.IPID so
			// the ban box fills without a /getarea. Gated to that family so a
			// normal server's parenthesised OOC name is never touched.
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
			pl.AreaID = atoiOr(p.Field(2), 0)
		}
		return []Event{{Kind: EventPlayersUpdated}}

	case "SM":
		s.Areas, s.Music = splitAreasAndMusic(p.Fields)
		// Fresh ARUP table parallel to the area list; -1 players = "the
		// server hasn't reported yet" (servers without arup never will).
		s.AreaInfo = make([]AreaInfo, len(s.Areas))
		for i := range s.AreaInfo {
			s.AreaInfo[i].Players = -1
		}
		s.reply(protocol.NewPacket("RD"))

	case "FM":
		// Fetch-music refresh (AO2-Client packet_distribution "FM"): the
		// MUSIC list alone is replaced, live — tsuserver-family servers push
		// a fresh list on area moves (per-area jukeboxes; a live server sent
		// 2106 fields of it and we dropped them on the floor). Every field
		// is a track verbatim (AO2 appends without the SM area/music split);
		// the music tab's first/last/len memo picks the swap up next frame.
		s.Music = append(s.Music[:0], p.Fields...)

	case "FA":
		// Fetch-areas refresh (packet_distribution "FA"): the AREA list is
		// replaced and the ARUP table resets to unknown until the next ARUP
		// lands (AO2 clear_areas + arup_clear with "Unknown" rows).
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
			// Malformed/japing message: drop it like AO2-Client does, but
			// surface the reason on the debug lane — a server emitting
			// broken MS packets is exactly what the overlay exists for.
			return []Event{{Kind: EventDebug, Text: "MS dropped: " + err.Error()}}
		}
		s.LastIC = msg // the stage-restore seed for rooms rebuilt later (see field doc)
		return []Event{{Kind: EventMessage, Message: msg}}

	case "MC":
		// Field 2 (showname) and field 1 (charID) name who played it for the IC
		// log line; both are optional on the wire (short legacy MC packets).
		track := p.Field(0)
		// Field 4 is the 2.9+ CHANNEL (AO2-Client courtroom.cpp handle_song:
		// "Channel 0 is 'master music', other for ambient"; absent = 0). An
		// ambience MC must never reach the music path: WAP-family servers
		// stream area ambience on channel 1 — including one on EVERY join —
		// and playing it as the song stopped/replaced the area's real music
		// (playtest: rejoining a server stayed silent). We don't render
		// ambience channels (yet), so a non-zero channel is a no-op.
		if atoiOr(p.Field(4), 0) != 0 {
			return nil
		}
		// Persist the current REAL song on the session (like Background), so a room
		// rebuilt later can resume it. Mirror the courtroom's play classification
		// (courtroom.go EventMusic): a stop clears it; an area-name transfer leaves the
		// playing song alone (the song continues across an area move until a new MC).
		switch {
		case track == "" || isAreaTransfer(track):
			// not a song — leave s.MusicTrack as-is
		case isMusicStop(track):
			s.MusicTrack = ""
		default:
			s.MusicTrack = track
		}
		return []Event{{Kind: EventMusic, Text: track, Int: atoiOr(p.Field(1), protocol.UnpairedCharID), Name: p.Field(2)}}

	case "BN":
		s.Background = p.Field(0)
		return []Event{{Kind: EventBackground, Text: s.Background}}

	case "PV":
		s.MyCharID = atoiOr(p.Field(2), protocol.UnpairedCharID)
		return []Event{{Kind: EventCharPicked, Int: s.MyCharID}}

	case "CT":
		// Legacy mod-login detection (courtroom.cpp
		// append_server_chatmessage): servers without auth_packet confirm
		// a /login with this exact OOC line — emulate AUTH 1.
		if !s.Features.Has(protocol.FeatureAuthPacket) && p.Field(1) == "Logged in as a moderator." {
			s.ModGranted = true
			return []Event{
				{Kind: EventOOC, Name: p.Field(0), Text: p.Field(1)},
				{Kind: EventAuth, Int: 1},
			}
		}
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

	case "HP":
		// HP#<bar 1=def|2=pro>#<0..10>#% — judge penalty bars
		// (packet_distribution.cpp → courtroom.cpp set_hp_bar, which
		// rejects out-of-range states).
		bar, val := atoiOr(p.Field(0), 0), atoiOr(p.Field(1), -1)
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
		// Witness Testimony / Cross Examination splashes, 2.8 judgeruling
		// verdicts, and the 2.9 "testimony1#1" end-of-recording marker —
		// variant semantics live in handle_wtce (courtroom.cpp:4846).
		if len(p.Fields) == 0 {
			return nil
		}
		return []Event{{Kind: EventWTCE, Text: p.Field(0), Int: atoiOr(p.Field(1), 0)}}

	case "ZZ":
		// Incoming modcall broadcast: the body is the pre-formatted notice
		// line (courtroom.cpp mod_called appends it to the server chatlog).
		if len(p.Fields) == 0 {
			return nil
		}
		return []Event{{Kind: EventModcall, Text: p.Field(0)}}

	case "VS_CAPS", "VS_PEERS", "VS_JOIN", "VS_LEAVE", "VS_SPEAK", "VS_AUDIO":
		return s.handleVoicePacket(p)

	case "ARUP":
		// ARUP#<type>#<area0>#<area1>…: type 0 players, 1 status, 2 CM,
		// 3 lock; field n applies to area n−1, out-of-range entries drop
		// (courtroom.cpp arup_modify bounds-checks the same way).
		typ := atoiOr(p.Field(0), -1)
		if typ < 0 || typ > 3 {
			return nil
		}
		for i := 1; i < len(p.Fields); i++ {
			n := i - 1
			if n >= len(s.AreaInfo) {
				break
			}
			switch typ {
			case 0:
				s.AreaInfo[n].Players = atoiOr(p.Fields[i], -1)
			case 1:
				s.AreaInfo[n].Status = p.Fields[i]
			case 2:
				s.AreaInfo[n].CM = p.Fields[i]
			case 3:
				s.AreaInfo[n].Lock = p.Fields[i]
			}
		}
		return []Event{{Kind: EventAreasUpdated}}

	case "TI":
		// TI#<id>#<type>#[<ms>]: type 0 start/resume countdown at ms,
		// 1 pause at ms, 2 show, 3 hide; ms ≤ 0 stops the clock
		// (packet_distribution.cpp). The canonical client also shaves its
		// measured latency/2 off type 0; we don't measure ping, so clocks
		// run at most one half-RTT behind the server's intent.
		id := atoiOr(p.Field(0), -1)
		if id < 0 || id >= TimerCount || len(p.Fields) < 2 {
			return nil
		}
		t := &s.Timers[id]
		switch atoiOr(p.Field(1), -1) {
		case 0:
			if len(p.Fields) < 3 {
				return nil
			}
			if ms := atoiOr(p.Field(2), -1); ms > 0 {
				t.Running = true
				t.Deadline = time.Now().Add(time.Duration(ms) * time.Millisecond)
			} else {
				t.Running, t.Left = false, 0 // negative value = stop
			}
		case 1:
			if len(p.Fields) < 3 {
				return nil
			}
			if ms := atoiOr(p.Field(2), -1); ms > 0 {
				t.Running, t.Left = false, time.Duration(ms)*time.Millisecond
			} else {
				t.Running, t.Left = false, 0
			}
		case 2:
			t.Visible = true
		case 3:
			t.Visible = false
		default:
			return nil
		}
		return []Event{{Kind: EventTimer, Int: id}}

	case "JD":
		// JD#<state>: −1 fall back to client-side judge buttons (pos ==
		// judge stand), 0 hide, 1 show; malformed packets are ignored
		// (packet_distribution.cpp JD).
		n, err := strconv.Atoi(strings.TrimSpace(p.Field(0)))
		if err != nil {
			return nil
		}
		s.Judge = n
		return []Event{{Kind: EventJudge, Int: n}}

	case "AUTH":
		// AUTH#<state>: 1+ mod granted, 0 login failed, <0 logged out
		// (on_authentication_state_received). Honored only with the
		// auth_packet feature, exactly like packet_distribution.cpp.
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
		// SD#<pos1*pos2*…>: the server's position dropdown
		// (set_pos_dropdown splits on '*').
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
		// Evidence list replace. Same nested decode as SC: split each field
		// on '&', percent-decode every sub-element again
		// (packet_distribution.cpp LE "decoding has to be done here").
		s.Evidence = s.Evidence[:0]
		for _, field := range p.Fields {
			if len(s.Evidence) >= evidenceCap {
				break
			}
			parts := strings.Split(field, "&")
			if len(parts) < 3 {
				continue
			}
			s.Evidence = append(s.Evidence, EvidenceItem{
				Name:        protocol.DecodeField(parts[0]),
				Description: protocol.DecodeField(parts[1]),
				Image:       protocol.DecodeField(parts[2]),
			})
		}
		return []Event{{Kind: EventEvidence}}

	case "BB":
		// Server popup notice (call_notice) — surfaced like OOC + a flash.
		if len(p.Fields) == 0 {
			return nil
		}
		return []Event{{Kind: EventNotice, Text: p.Field(0)}}

	case "CASEA":
		// Case announcement: CASEA#<msg>#<def>#<pro>#<judge>#<jury>#<steno>.
		// Legacy 2.6–2.9 casing_alerts — removed upstream ("No longer
		// used", serverdata.h) but tsuserver-family servers still send it;
		// role gating happens UI-side against the user's SETCASE prefs.
		if len(p.Fields) < 6 {
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
		// Headers this client doesn't implement (vendor extensions, not-yet
		// -built features): ignoring them is harmless, but "the server sends
		// X and nothing happens" must be diagnosable — debug lane only.
		return []Event{{Kind: EventDebug,
			Text: fmt.Sprintf("unhandled packet %q (%d fields)", p.Header, len(p.Fields))}}
	}
	return nil
}

// splitAreasAndMusic mirrors AO2-Client's SM scan (packet_distribution.cpp
// "SM" handler): entries before the first audio-extension entry are areas,
// the rest (inclusive) are music. The one subtlety is fix_last_area
// (courtroom.cpp:613): the entry immediately preceding the first song is a
// music *category header* (e.g. "==Cave Story OST=="), NOT an area — AO music
// lists always wrap their songs in a category, so that last "area" is
// malplaced and gets moved to the front of the music list. Without this a
// category header leaks into the Areas tab (and leaves a trailing area row
// with no ARUP column).
func splitAreasAndMusic(fields []string) (areas, music []string) {
	musicStart := len(fields)
	for i, f := range fields {
		if hasAudioExt(f) {
			musicStart = i
			break
		}
	}
	// fix_last_area: when at least one song exists and an entry precedes it,
	// that preceding entry is the music category header — shift the boundary
	// back one so it lands in music instead of areas.
	if musicStart > 0 && musicStart < len(fields) {
		musicStart--
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

// Ping sends the CH keepalive AO2-Client fires every 45 s
// (courtroom.cpp keepalive_timer → ping_server). Servers idle-kick
// silent clients; without this, sitting minimized (no chat traffic)
// got the connection dropped.
func (s *Session) Ping() {
	s.reply(protocol.NewPacket("CH", strconv.Itoa(s.MyCharID)))
}

// CallMod sends a mod call with an optional reason. The second field is
// the target player id, −1 = whole area (courtroom.cpp:6530
// on_call_mod_clicked sends {reason, "-1"}).
func (s *Session) CallMod(reason string) {
	if s.Features.Has(protocol.FeatureModcallReason) {
		s.reply(protocol.NewPacket("ZZ", reason, "-1"))
		return
	}
	s.reply(protocol.NewPacket("ZZ"))
}

// SendHP submits a judge penalty-bar change: HP#<bar 1=def|2=pro>#<0..10>
// (courtroom.cpp on_defense_minus_clicked et al.).
func (s *Session) SendHP(bar, state int) {
	if state < 0 || state > HPBarMax || (bar != 1 && bar != 2) {
		return
	}
	s.reply(protocol.NewPacket("HP", strconv.Itoa(bar), strconv.Itoa(state)))
}

// SendWTCE fires a judge splash: testimony1/testimony2 plain, judgeruling
// with the verdict variant (courtroom.cpp judge button handlers).
func (s *Session) SendWTCE(name string, variant int) {
	if name == "judgeruling" {
		s.reply(protocol.NewPacket("RT", name, strconv.Itoa(variant)))
		return
	}
	s.reply(protocol.NewPacket("RT", name))
}

// AddEvidence appends a global evidence item (PE#name#desc#image).
func (s *Session) AddEvidence(name, desc, image string) {
	s.reply(protocol.NewPacket("PE", name, desc, image))
}

// DeleteEvidence removes the item at index (DE#id).
func (s *Session) DeleteEvidence(id int) {
	s.reply(protocol.NewPacket("DE", strconv.Itoa(id)))
}

// EditEvidence replaces the item at index (EE#id#name#desc#image).
func (s *Session) EditEvidence(id int, name, desc, image string) {
	s.reply(protocol.NewPacket("EE", strconv.Itoa(id), name, desc, image))
}

// SetCasingPrefs subscribes to CASEA announcements by role. The leading
// field is the legacy case-list blob no server reads (tsuserver
// net_cmd_setcase skips args[0]).
func (s *Session) SetCasingPrefs(def, pro, judge, jury, steno bool) {
	s.reply(protocol.NewPacket("SETCASE", "",
		boolWire(def), boolWire(pro), boolWire(judge), boolWire(jury), boolWire(steno)))
}

// boolWire is the AO wire encoding for booleans.
func boolWire(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func atoiOr(s string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return v
}
