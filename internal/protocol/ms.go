package protocol

import (
	"fmt"
	"strconv"
	"strings"
)

// MS packet field indices, mirroring AO2-Client's CHAT_MESSAGE enum
// (src/datatypes.h) exactly. Field count and order are protocol law.
const (
	MSDeskMod          = 0
	MSPreEmote         = 1
	MSCharName         = 2
	MSEmote            = 3
	MSMessage          = 4
	MSSide             = 5
	MSSFXName          = 6
	MSEmoteMod         = 7
	MSCharID           = 8
	MSSFXDelay         = 9
	MSObjectionMod     = 10
	MSEvidenceID       = 11
	MSFlip             = 12
	MSRealization      = 13
	MSTextColor        = 14
	MSShowname         = 15
	MSOtherCharID      = 16
	MSOtherName        = 17
	MSOtherEmote       = 18
	MSSelfOffset       = 19
	MSOtherOffset      = 20
	MSOtherFlip        = 21
	MSImmediate        = 22
	MSLoopingSFX       = 23
	MSScreenshake      = 24
	MSFrameScreenshake = 25
	MSFrameRealization = 26
	MSFrameSFX         = 27
	MSAdditive         = 28
	MSEffects          = 29
	MSBlipname         = 30
	MSSlide            = 31

	// MSMinimum is the pre-2.6 field count; shorter packets are dropped
	// (AO2-Client MS_MINIMUM).
	MSMinimum = 15
	// MSMaximum is the full 2.8+ field count (AO2-Client MS_MAXIMUM).
	MSMaximum = 32
)

// Emote modifiers (AO2-Client EMOTE_MOD_TYPE).
const (
	EmoteModIdle        = 0
	EmoteModPreanim     = 1
	EmoteModZoom        = 5
	EmoteModPreanimZoom = 6

	// legacyEmoteModObjection (2) and legacyEmoteModZoomPre (4) are
	// deprecated wire values normalized at parse (AO2-Client
	// handle_emote_mod).
	legacyEmoteModObjection = 2
	legacyEmoteModZoomPre   = 4
)

// Desk modifiers (AO2-Client DESK_MOD_TYPE).
const (
	DeskHide        = 0
	DeskShow        = 1
	DeskEmoteOnly   = 2
	DeskPreOnly     = 3
	DeskEmoteOnlyEx = 4
	DeskPreOnlyEx   = 5
)

// Objection modifiers (shout values on the wire).
const (
	ShoutNone      = 0
	ShoutHoldIt    = 1
	ShoutObjection = 2
	ShoutTakeThat  = 3
	ShoutCustom    = 4
)

// UnpairedCharID is the OTHER_CHARID value meaning "no pair".
const UnpairedCharID = -1

// ChatMessage is a fully parsed incoming MS packet.
type ChatMessage struct {
	DeskMod      int
	PreEmote     string
	CharName     string
	Emote        string
	Message      string
	Side         string
	SFXName      string
	EmoteMod     int
	CharID       int
	SFXDelay     int
	Objection    int    // shout value (custom name split off)
	CustomShout  string // 2.8: "4&<name>" custom objection name
	EvidenceID   int
	Flip         bool
	Realization  bool
	TextColor    int
	Showname     string
	Pair         PairInfo
	SelfOffsetX  int
	SelfOffsetY  int
	Immediate    bool
	LoopingSFX   bool
	Screenshake  bool
	FrameShake   string
	FrameRealize string
	FrameSFX     string
	Additive     bool
	Effects      string
	Blipname     string
	Slide        bool
}

// ParseMS validates and parses an incoming MS packet's fields. Mirrors
// AO2-Client chatmessage_enqueue/unpack_chatmessage: packets shorter than
// MSMinimum are rejected; fields at index ≥ MSMinimum are honored only when
// the server advertised cccc_ic_support; absent fields read as "".
// charListSize bounds CHAR_ID validation (pass 0 to skip the upper bound).
func ParseMS(fields []string, features FeatureSet, charListSize int) (*ChatMessage, error) {
	if len(fields) < MSMinimum {
		return nil, fmt.Errorf("protocol: MS packet has %d fields, minimum %d", len(fields), MSMinimum)
	}

	get := func(i int) string {
		if i >= len(fields) {
			return ""
		}
		if i >= MSMinimum && !features.Has(FeatureCCCCIC) {
			return "" // vanilla servers can't be trusted past index 14
		}
		return fields[i]
	}

	charID := atoiDefault(get(MSCharID), UnpairedCharID)
	if charID < UnpairedCharID || (charListSize > 0 && charID >= charListSize) {
		return nil, fmt.Errorf("protocol: MS char id %d out of range", charID)
	}

	objection, customShout := parseObjection(get(MSObjectionMod))
	selfX, selfY := ParseOffset(get(MSSelfOffset))

	msg := &ChatMessage{
		// Mirrors QString::toInt: non-numeric legacy values ("chat") read
		// as 0 (DeskHide), exactly like AO2-Client.
		DeskMod:      atoiDefault(get(MSDeskMod), DeskHide),
		PreEmote:     get(MSPreEmote),
		CharName:     get(MSCharName),
		Emote:        get(MSEmote),
		Message:      get(MSMessage),
		Side:         get(MSSide),
		SFXName:      get(MSSFXName),
		EmoteMod:     normalizeEmoteMod(atoiDefault(get(MSEmoteMod), EmoteModIdle)),
		CharID:       charID,
		SFXDelay:     atoiDefault(get(MSSFXDelay), 0),
		Objection:    objection,
		CustomShout:  customShout,
		EvidenceID:   atoiDefault(get(MSEvidenceID), 0),
		Flip:         get(MSFlip) == "1",
		Realization:  get(MSRealization) == "1",
		TextColor:    atoiDefault(get(MSTextColor), 0),
		Showname:     get(MSShowname),
		Pair:         ParsePair(get(MSOtherCharID), get(MSOtherName), get(MSOtherEmote), get(MSOtherOffset), get(MSOtherFlip)),
		SelfOffsetX:  selfX,
		SelfOffsetY:  selfY,
		Immediate:    get(MSImmediate) == "1",
		LoopingSFX:   get(MSLoopingSFX) == "1",
		Screenshake:  get(MSScreenshake) == "1",
		FrameShake:   get(MSFrameScreenshake),
		FrameRealize: get(MSFrameRealization),
		FrameSFX:     get(MSFrameSFX),
		Additive:     get(MSAdditive) == "1",
		Effects:      get(MSEffects),
		Blipname:     get(MSBlipname),
		Slide:        get(MSSlide) == "1",
	}
	return msg, nil
}

// normalizeEmoteMod maps deprecated wire values onto live ones, mirroring
// AO2-Client handle_emote_mod: 4 → preanim-zoom (old zoompre bug), 2 →
// preanim (deprecated objection-preanim), anything unknown → idle.
func normalizeEmoteMod(mod int) int {
	switch mod {
	case EmoteModIdle, EmoteModPreanim, EmoteModZoom, EmoteModPreanimZoom:
		return mod
	case legacyEmoteModZoomPre:
		return EmoteModPreanimZoom
	case legacyEmoteModObjection:
		return EmoteModPreanim
	default:
		return EmoteModIdle
	}
}

// parseObjection splits "mod" or "mod&customname" (2.8 custom objections).
func parseObjection(raw string) (int, string) {
	mod, custom, found := strings.Cut(raw, "&")
	value := atoiDefault(mod, ShoutNone)
	if !found {
		return value, ""
	}
	return value, custom
}

// IsShout reports whether the objection value plays a shout bubble.
func (m *ChatMessage) IsShout() bool {
	return m.Objection >= ShoutHoldIt && m.Objection <= ShoutCustom
}

// atoiDefault parses an int with a fallback, AO-style (garbage reads as the
// fallback, never an error mid-courtroom).
func atoiDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return v
}

// --- Outgoing MS ----------------------------------------------------------------

// OutgoingMS describes a message to send; Fields applies the same
// feature-gating ladder as AO2-Client's on_chat_return_pressed, so the
// packet shape matches what the connected server expects.
type OutgoingMS struct {
	DeskMod     int
	PreEmote    string
	CharName    string
	Emote       string
	Message     string
	Side        string
	SFXName     string
	EmoteMod    int
	CharID      int
	SFXDelay    int
	Objection   int
	CustomShout string
	EvidenceID  int
	Flip        bool
	Realization bool
	TextColor   int

	// 2.6 CCCC extensions.
	Showname  string
	PairWith  int // UnpairedCharID when unpaired
	PairOrder int // 0 = our character in front, 1 = behind (needs effects)
	OffsetX   int
	OffsetY   int
	Immediate bool

	// 2.8 extensions.
	LoopingSFX   bool
	Screenshake  bool
	FrameShake   string
	FrameRealize string
	FrameSFX     string
	Additive     bool
	Effects      string

	// 2.9+ extensions.
	Blipname string
	Slide    bool
}

// Fields serializes the outgoing message honoring the server's features.
func (o OutgoingMS) Fields(features FeatureSet) []string {
	fields := make([]string, 0, MSMaximum)
	fields = append(fields,
		strconv.Itoa(o.DeskMod),
		o.PreEmote,
		o.CharName,
		o.Emote,
		o.Message,
		o.Side,
		o.SFXName,
		strconv.Itoa(o.EmoteMod),
		strconv.Itoa(o.CharID),
		strconv.Itoa(o.SFXDelay),
		formatObjection(o.Objection, o.CustomShout, features),
		strconv.Itoa(o.EvidenceID),
		boolField(o.Flip),
		boolField(o.Realization),
		strconv.Itoa(o.TextColor),
	)

	if features.Has(FeatureCCCCIC) {
		fields = append(fields, o.Showname)
		fields = append(fields, formatPairID(o.PairWith, o.PairOrder, features))
		if features.Has(FeatureYOffset) {
			fields = append(fields, strconv.Itoa(o.OffsetX)+"&"+strconv.Itoa(o.OffsetY))
		} else {
			fields = append(fields, strconv.Itoa(o.OffsetX))
		}
		fields = append(fields, boolField(o.Immediate))
	}

	if features.Has(FeatureLoopingSFX) {
		fields = append(fields,
			boolField(o.LoopingSFX),
			boolField(o.Screenshake),
			o.FrameShake,
			o.FrameRealize,
			o.FrameSFX,
		)
	}
	if features.Has(FeatureAdditive) {
		fields = append(fields, boolField(o.Additive))
	}
	if features.Has(FeatureEffects) {
		fields = append(fields, o.Effects)
	}
	if features.Has(FeatureCustomBlips) {
		fields = append(fields, o.Blipname, boolField(o.Slide))
	}
	return fields
}

// Packet wraps Fields into the MS packet.
func (o OutgoingMS) Packet(features FeatureSet) Packet {
	return NewPacket("MS", o.Fields(features)...)
}

// NormalizeOutgoingEmoteMod applies AO2-Client's on_chat_return_pressed
// emote-mod overrides (courtroom.cpp "EMOTE MOD OVERRIDES", 2.11): char.ini
// files ship legacy values that must never reach the wire — 2 is
// objection-internal, 3 is meaningless, 4 aliases zoom — and an emote with
// a preanimation upgrades idle→preanim and zoom→preanim-zoom (the latter
// only when the server advertises prezoom). Strict receivers
// (LemmyAO schema-validates MS broadcasts) DROP messages whose
// emote_modifier carries a raw legacy value, so sending ini values
// verbatim made us invisible to them.
func NormalizeOutgoingEmoteMod(mod int, hasPreanim, immediate bool, features FeatureSet) int {
	switch mod {
	case legacyEmoteModObjection:
		mod = EmoteModPreanim
	case 3: // "No clue what emote_mod 3 is even supposed to be." — AO2-Client
		mod = EmoteModIdle
	case legacyEmoteModZoomPre:
		mod = EmoteModZoom
	}
	if hasPreanim && !immediate {
		switch {
		case mod == EmoteModIdle:
			mod = EmoteModPreanim
		case mod == EmoteModZoom && features.Has(FeaturePrezoom):
			mod = EmoteModPreanimZoom
		}
	}
	return mod
}

// formatObjection emits "mod" or "mod&custom" (custom objections need the
// server feature).
func formatObjection(mod int, custom string, features FeatureSet) string {
	if mod == ShoutCustom && custom != "" && features.Has(FeatureCustomObjections) {
		return strconv.Itoa(mod) + "&" + custom
	}
	return strconv.Itoa(mod)
}

// formatPairID emits "-1", "<id>", or "<id>^<order>" — pair reordering only
// on servers with the effects feature (AO2-Client gates it the same way).
func formatPairID(pairWith, order int, features FeatureSet) string {
	if pairWith <= UnpairedCharID {
		return strconv.Itoa(UnpairedCharID)
	}
	if features.Has(FeatureEffects) {
		return strconv.Itoa(pairWith) + "^" + strconv.Itoa(order)
	}
	return strconv.Itoa(pairWith)
}

func boolField(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
