package protocol

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
)

// Pair z-order from the 2.8 "<id>^<order>" extension, mirroring AO2-Client display_pair_character:
// ^0 (the default) renders the speaker in front, ^1 renders the speaker behind.
const (
	PairSpeakerInFront = 0
	PairSpeakerBehind  = 1
)

// PairInfo is the pairing state of one chat message (AO2 ≥ 2.6, with 2.8
// order/vertical-offset extensions).
type PairInfo struct {
	// CharID is the partner's character list index; UnpairedCharID means no pair.
	CharID int
	// Order is PairSpeakerInFront or PairSpeakerBehind.
	Order int
	// HasOrder reports whether the wire carried an explicit ^order.
	HasOrder bool
	// Name is the partner's folder; empty disables pairing even with a valid CharID (AO2-Client is_pairing).
	Name string
	// Emote is the partner's idle emote, rendered as the looping (a)<Emote> animation.
	Emote string
	// OffsetX and OffsetY shift the partner sprite, in percent of viewport size (-100..100).
	OffsetX, OffsetY int
	// Flip mirrors the partner sprite; the server feature "flipping" decides whether the renderer honors it.
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

// Active reports whether a partner renders, per AO2-Client: a valid char id and a non-empty pair folder.
func (p PairInfo) Active() bool {
	return p.CharID > UnpairedCharID && p.Name != ""
}

// SpeakerInFront resolves z-order: an explicit ^order wins, otherwise the speaker is in front.
func (p PairInfo) SpeakerInFront() bool {
	if p.HasOrder && p.Order == PairSpeakerBehind {
		return false
	}
	return true
}

// ParsePairID splits "<id>" and "<id>^<order>"; a non-numeric id reads as unpaired (AO2-Client toInt(&ok)).
func ParsePairID(raw string) (id, order int, hasOrder bool) {
	idPart, orderPart, found := strings.Cut(raw, "^")
	id = UnpairedCharID
	if v, err := strconv.Atoi(strings.TrimSpace(idPart)); err == nil {
		id = v
	}
	if !found {
		return id, PairSpeakerInFront, false
	}
	switch parse.AtoiOr(orderPart, PairSpeakerInFront) {
	case PairSpeakerBehind:
		return id, PairSpeakerBehind, true
	default:
		return id, PairSpeakerInFront, true
	}
}

// ParseOffset splits "<x>" and "<x>&<y>" in viewport percent; X alone means Y = 0, as AO2-Client's split("&").
func ParseOffset(raw string) (x, y int) {
	if raw == "" {
		return 0, 0
	}
	xPart, yPart, found := strings.Cut(raw, "&")
	x = parse.AtoiOr(xPart, 0)
	if !found {
		return x, 0
	}
	return x, parse.AtoiOr(yPart, 0)
}
