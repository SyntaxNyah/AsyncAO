package protocol

import (
	"strconv"
	"strings"
)

// Pair z-order, from the 2.8 "<id>^<order>" extension. Semantics mirror
// AO2-Client display_pair_character exactly:
//
//	^0 → the SPEAKER renders in front (pair behind) — also the default
//	^1 → the speaker renders behind (pair in front)
//
// (PROMPT.md §11's table had these inverted; AO2-Client wins on protocol
// behavior per its own ground rule.)
const (
	PairSpeakerInFront = 0
	PairSpeakerBehind  = 1
)

// PairInfo is the pairing state of one chat message (AO2 ≥ 2.6, with 2.8
// order/vertical-offset extensions).
type PairInfo struct {
	// CharID is the partner's character list index; UnpairedCharID means no
	// pair.
	CharID int
	// Order is PairSpeakerInFront or PairSpeakerBehind.
	Order int
	// HasOrder reports whether the wire carried an explicit ^order.
	HasOrder bool
	// Name is the partner's character folder; empty disables pairing even
	// with a valid CharID (AO2-Client is_pairing checks both).
	Name string
	// Emote is the partner's idle emote: the pair renders the looping
	// (a)<Emote> animation.
	Emote string
	// OffsetX and OffsetY shift the partner sprite, in percent of viewport
	// width/height (−100..100).
	OffsetX, OffsetY int
	// Flip mirrors the partner sprite horizontally (server feature
	// "flipping" gates whether the renderer honors it).
	Flip bool
}

// ParsePair assembles PairInfo from the raw MS fields.
func ParsePair(otherCharID, otherName, otherEmote, otherOffset, otherFlip string) PairInfo {
	id, order, hasOrder := ParsePairID(otherCharID)
	x, y := ParseOffset(otherOffset)
	return PairInfo{
		CharID:   id,
		Order:    order,
		HasOrder: hasOrder,
		Name:     otherName,
		Emote:    otherEmote,
		OffsetX:  x,
		OffsetY:  y,
		Flip:     otherFlip == "1",
	}
}

// Active reports whether the message actually renders a pair partner —
// AO2-Client semantics: a valid char id AND a non-empty pair folder.
func (p PairInfo) Active() bool {
	return p.CharID > UnpairedCharID && p.Name != ""
}

// SpeakerInFront resolves z-order: an explicit ^order wins, otherwise the
// speaker renders in front.
func (p PairInfo) SpeakerInFront() bool {
	if p.HasOrder && p.Order == PairSpeakerBehind {
		return false
	}
	return true
}

// ParsePairID splits the 2.6 "<id>" / 2.8 "<id>^<order>" forms. A
// non-numeric id reads as unpaired (AO2-Client toInt(&ok) gating).
func ParsePairID(raw string) (id, order int, hasOrder bool) {
	idPart, orderPart, found := strings.Cut(raw, "^")
	id = UnpairedCharID
	if v, err := strconv.Atoi(strings.TrimSpace(idPart)); err == nil {
		id = v
	}
	if !found {
		return id, PairSpeakerInFront, false
	}
	switch atoiDefault(orderPart, PairSpeakerInFront) {
	case PairSpeakerBehind:
		return id, PairSpeakerBehind, true
	default:
		return id, PairSpeakerInFront, true
	}
}

// ParseOffset splits the 2.6 "<x>" / 2.9 "<x>&<y>" offset forms, in percent
// of the viewport dimensions. One component means X only, Y = 0 — exactly
// AO2-Client's split("&") handling.
func ParseOffset(raw string) (x, y int) {
	if raw == "" {
		return 0, 0
	}
	xPart, yPart, found := strings.Cut(raw, "&")
	x = atoiDefault(xPart, 0)
	if !found {
		return x, 0
	}
	return x, atoiDefault(yPart, 0)
}
