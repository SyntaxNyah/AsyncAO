package protocol

import (
	"fmt"
	"strconv"
	"strings"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
)

// MS field indices mirror AO2-Client's CHAT_MESSAGE enum (src/datatypes.h); count and order are fixed by the protocol.
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

	// MSMinimum is the pre-2.6 field count; shorter packets are dropped (AO2-Client MS_MINIMUM).
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

	// Deprecated wire values, normalized at parse (AO2-Client handle_emote_mod); 3 has no meaning at all.
	legacyEmoteModObjection = 2
	legacyEmoteModUnknown   = 3
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

	// Additional is the group roster (additional_chars), set only when a group member speaks.
	Additional []GroupPairMember
}

// ParseMS mirrors AO2-Client chatmessage_enqueue/unpack_chatmessage: fields past MSMinimum need
// cccc_ic_support, absent fields read as "", and charListSize 0 skips the CHAR_ID upper bound.
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

	// getBlips applies AO2-Client's CUSTOM_BLIPS check (courtroom.cpp:4249-4255, serverdata.h:64): KFO-Server
	// reuses indices 30/31 for third_charid ("-1" when idle) and third_folder, which aren't a blip name or slide.
	getBlips := func(i int) string {
		if !features.Has(FeatureCustomBlips) {
			return ""
		}
		return get(i)
	}

	charID := parse.AtoiOr(get(MSCharID), UnpairedCharID)
	if charID < UnpairedCharID || (charListSize > 0 && charID >= charListSize) {
		return nil, fmt.Errorf("protocol: MS char id %d out of range", charID)
	}

	objection, customShout := parseObjection(get(MSObjectionMod))
	selfX, selfY := ParseOffset(get(MSSelfOffset))

	msg := &ChatMessage{
		// Mirrors QString::toInt: non-numeric legacy values ("chat") read as 0 (DeskHide), as in AO2-Client.
		DeskMod:      parse.AtoiOr(get(MSDeskMod), DeskHide),
		PreEmote:     get(MSPreEmote),
		CharName:     get(MSCharName),
		Emote:        get(MSEmote),
		Message:      get(MSMessage),
		Side:         get(MSSide),
		SFXName:      get(MSSFXName),
		EmoteMod:     normalizeEmoteMod(parse.AtoiOr(get(MSEmoteMod), EmoteModIdle)),
		CharID:       charID,
		SFXDelay:     parse.AtoiOr(get(MSSFXDelay), 0),
		Objection:    objection,
		CustomShout:  customShout,
		EvidenceID:   parse.AtoiOr(get(MSEvidenceID), 0),
		Flip:         get(MSFlip) == "1",
		Realization:  get(MSRealization) == "1",
		TextColor:    parse.AtoiOr(get(MSTextColor), 0),
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
		Blipname:     getBlips(MSBlipname),
		Slide:        getBlips(MSSlide) == "1",
	}
	return msg, nil
}

// normalizeEmoteMod mirrors AO2-Client handle_emote_mod: 4 to preanim-zoom, 2 to preanim, anything unknown to idle.
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
	value := parse.AtoiOr(mod, ShoutNone)
	if !found {
		return value, ""
	}
	return value, custom
}

// IsShout reports whether the objection value plays a shout bubble.
func (m *ChatMessage) IsShout() bool {
	return m.Objection >= ShoutHoldIt && m.Objection <= ShoutCustom
}

// OutgoingMS is a message to send; Fields applies the same feature checks as AO2-Client's on_chat_return_pressed.
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

	// KFOCompat fills frame and effect fields as AO2-Client does; KFO-Server's validator rejects empty STR values.
	KFOCompat bool
}

// kfoFrameTemplate is AO2-Client's per-emote frame field (courtroom.cpp ~2272), non-empty for KFO-Server's
// validator; AsyncAO tracks no per-frame SFX data.
func kfoFrameTemplate(pre, emote string) string {
	return pre + "^(b)" + emote + "^(a)" + emote + "^"
}

// clampDeskMod collapses 2.9 desk mods (2-5) to hide/show without expanded_desk_mods, as AO2-Client does
// (courtroom.cpp:2021-2031); strict servers reject the whole MS otherwise.
func clampDeskMod(mod int, features FeatureSet) int {
	if features.Has(FeatureExpandedDeskMods) {
		return mod
	}
	switch mod {
	case DeskPreOnly, DeskPreOnlyEx:
		return DeskHide
	case DeskEmoteOnly, DeskEmoteOnlyEx:
		return DeskShow
	default:
		return mod
	}
}

// Fields serializes the outgoing message honoring the server's features.
func (o OutgoingMS) Fields(features FeatureSet) []string {
	fields := make([]string, 0, MSMaximum)
	fields = append(fields,
		strconv.Itoa(clampDeskMod(o.DeskMod, features)),
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
		shake, realize, sfx := o.FrameShake, o.FrameRealize, o.FrameSFX
		if o.KFOCompat { // KFO rejects empty STR frame fields; AO2-Client sends the frame template
			t := kfoFrameTemplate(o.PreEmote, o.Emote)
			if shake == "" {
				shake = t
			}
			if realize == "" {
				realize = t
			}
			if sfx == "" {
				sfx = t
			}
		}
		fields = append(fields,
			boolField(o.LoopingSFX),
			boolField(o.Screenshake),
			shake,
			realize,
			sfx,
		)
	}
	if features.Has(FeatureAdditive) {
		fields = append(fields, boolField(o.Additive))
	}
	if features.Has(FeatureEffects) {
		eff := o.Effects
		if o.KFOCompat && eff == "" {
			eff = "||" // KFO rejects an empty effect (STR); AO2-Client sends "<effect>|<folder>|<sound>", "||" when none
		}
		fields = append(fields, eff)
	}
	if features.Has(FeatureCustomBlips) {
		fields = append(fields, o.Blipname, boolField(o.Slide))
	}
	return fields
}

// Packet wraps Fields and carries aolib's typed form, so the JSON wire is built from the typed struct.
func (o OutgoingMS) Packet(features FeatureSet) Packet {
	p := NewPacket("MS", o.Fields(features)...)
	typed := o.ToAolibMSToServer(features)
	p.typed = &typed
	return p
}

// ToAolibMSToServer applies the checks of Fields, and drops Fanta-only extensions aolib doesn't model
// (custom shout name, pair z-order, blip name and slide).
func (o OutgoingMS) ToAolibMSToServer(features FeatureSet) aolib.MSToServer {
	return aolib.MSToServer{
		DeskModifier:           deskModifierSafe(clampDeskMod(o.DeskMod, features)),
		Preanim:                o.PreEmote,
		Character:              o.CharName,
		Emote:                  o.Emote,
		Message:                o.Message,
		Side:                   aolib.Side(o.Side),
		SfxName:                o.SFXName,
		EmoteModifier:          emoteModifierSafe(o.EmoteMod),
		CharID:                 o.CharID,
		SfxDelay:               o.SFXDelay,
		ShoutModifier:          shoutModifierSafe(o.Objection),
		EvidenceID:             o.EvidenceID,
		Flip:                   flipSafe(o.Flip),
		Realization:            o.Realization,
		TextColor:              textColorSafe(o.TextColor),
		Showname:               o.Showname,
		PairedCharID:           o.PairWith,
		Offset:                 aolib.Offset{X: o.OffsetX, Y: o.OffsetY},
		NoninterruptingPreanim: o.Immediate,
		SfxLooping:             o.LoopingSFX,
		Screenshake:            o.Screenshake,
		FramesShake:            o.FrameShake,
		FramesRealization:      o.FrameRealize,
		FramesSfx:              o.FrameSFX,
		Additive:               o.Additive,
		Effect:                 parseEffect(o.Effects),
	}
}

// deskModifierSafe falls back to aolib's default (shown) so the JSON never carries an empty enum.
func deskModifierSafe(mod int) aolib.DeskModifier {
	if v, ok := packetutil.DeskModifierFromWire[mod]; ok {
		return v
	}
	return aolib.DeskModifierShown
}

// emoteModifierSafe falls back to no_preanim, aolib's default.
func emoteModifierSafe(mod int) aolib.EmoteModifier {
	if v, ok := packetutil.EmoteModifierFromWire[mod]; ok {
		return v
	}
	return aolib.EmoteModifierNoPreanim
}

// shoutModifierSafe falls back to none.
func shoutModifierSafe(mod int) aolib.ShoutModifier {
	if v, ok := packetutil.ShoutModifierFromWire[mod]; ok {
		return v
	}
	return aolib.ShoutModifierNone
}

// textColorSafe falls back to white.
func textColorSafe(c int) aolib.TextColor {
	if v, ok := packetutil.TextColorFromWire[c]; ok {
		return v
	}
	return aolib.TextColorWhite
}

func flipSafe(flip bool) aolib.Flip {
	if flip {
		return aolib.FlipHorizontal
	}
	return aolib.FlipNone
}

// parseEffect splits the "name|folder|sound" field, mirroring aolib's effectFromWire.
func parseEffect(raw string) aolib.Effect {
	const effectFields = 3 // name|folder|sound
	if raw == "" {
		return aolib.Effect{}
	}
	parts := strings.SplitN(raw, "|", effectFields)
	e := aolib.Effect{Name: parts[0]}
	if len(parts) > 1 {
		e.Folder = parts[1]
	}
	if len(parts) > 2 {
		e.Sound = parts[2]
	}
	return e
}

// NormalizeOutgoingEmoteMod applies AO2-Client's on_chat_return_pressed overrides ("EMOTE MOD OVERRIDES"):
// legacy char.ini values never reach the wire (2 is objection-internal, 3 meaningless, 4 aliases zoom),
// and the "Pre" toggle upgrades or, unchecked, downgrades preanim mods (courtroom.cpp:2080-2092).
func NormalizeOutgoingEmoteMod(mod int, hasPreanim, preanim, immediate bool, features FeatureSet) int {
	switch mod {
	case legacyEmoteModObjection:
		mod = EmoteModPreanim
	case legacyEmoteModUnknown: // "No clue what emote_mod 3 is even supposed to be." — AO2-Client
		mod = EmoteModIdle
	case legacyEmoteModZoomPre:
		mod = EmoteModZoom
	}
	if preanim && hasPreanim && !immediate {
		// "Pre" on: idle to preanim, zoom to preanim-zoom (courtroom.cpp:2065-2079).
		switch {
		case mod == EmoteModIdle:
			mod = EmoteModPreanim
		case mod == EmoteModZoom && features.Has(FeaturePrezoom):
			mod = EmoteModPreanimZoom
		}
	} else if !preanim {
		// "Pre" off: downgrade so no intro plays (courtroom.cpp:2080-2092); immediate only stops the upgrade above.
		switch mod {
		case EmoteModPreanim:
			mod = EmoteModIdle
		case EmoteModPreanimZoom:
			mod = EmoteModZoom
		}
	}
	return mod
}

// formatObjection emits "mod" or "mod&custom"; custom objections need the server feature.
func formatObjection(mod int, custom string, features FeatureSet) string {
	if mod == ShoutCustom && custom != "" && features.Has(FeatureCustomObjections) {
		return strconv.Itoa(mod) + "&" + custom
	}
	return strconv.Itoa(mod)
}

// formatPairID emits "-1" unpaired, else the id, with the 2.8 "^<order>" suffix only on effects servers
// and only for a non-default order (speaker behind).
func formatPairID(pairWith, order int, features FeatureSet) string {
	if pairWith <= UnpairedCharID {
		return strconv.Itoa(UnpairedCharID)
	}
	if features.Has(FeatureEffects) && order != PairSpeakerInFront {
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
