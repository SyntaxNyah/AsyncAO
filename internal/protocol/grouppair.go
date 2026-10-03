package protocol

import (
	"encoding/json"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
)

// GroupPairMember is one ordered member of a group roster (render model). Order
// is the z-order: 0 front-most, increasing toward the back.
type GroupPairMember struct {
	UID              int
	CharID           int
	Name             string
	Emote            string
	Side             string
	OffsetX, OffsetY int
	Flip             aolib.Flip
	Order            int
}

// GroupPair is the current group roster, ordered front→back with the speaker
// included (the single source of truth for on-screen ordering).
type GroupPair struct {
	GroupID string
	Members []GroupPairMember
}

// Empty reports whether no renderable group is active (nil or fewer than two
// members).
func (g *GroupPair) Empty() bool {
	return g == nil || len(g.Members) < 2
}

// FromGP converts the wire GP packet into the render model.
func FromGP(gp *packetutil.GP) *GroupPair {
	if gp == nil {
		return nil
	}
	out := &GroupPair{GroupID: gp.GroupID, Members: make([]GroupPairMember, 0, len(gp.Members))}
	for _, m := range gp.Members {
		out.Members = append(out.Members, GroupPairMember{
			UID:     m.UID,
			CharID:  m.CharID,
			Name:    m.Name,
			Emote:   m.Emote,
			Side:    m.Side,
			OffsetX: m.Offset.X,
			OffsetY: m.Offset.Y,
			Flip:    m.Flip,
			Order:   m.Order,
		})
	}
	return out
}

// GroupPairFromPacket extracts the group roster from a GP Packet's typed value
// (nil for non-GP or untyped packets).
func GroupPairFromPacket(p Packet) *GroupPair {
	if gp, ok := p.Typed().(*packetutil.GP); ok {
		return FromGP(gp)
	}
	return nil
}

// FlipH reports whether the member is horizontally mirrored (AO's pair flip).
func (m GroupPairMember) FlipH() bool {
	return m.Flip == aolib.FlipHorizontal || m.Flip == aolib.FlipHorizontalAndVertical
}

// ParseAdditionalChars converts the JSON-only `additional_chars` MS field
// (aolib's MSToClient.Extras["additional_chars"]) into the render model. The
// server broadcasts it on every MS from a group member, so non-members (a new
// entrant) can render the group without holding the GP roster.
func ParseAdditionalChars(v any) []GroupPairMember {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	type wireMember struct {
		CharID int          `json:"charid"`
		Name   string       `json:"name"`
		Emote  string       `json:"emote"`
		Side   string       `json:"side"`
		Offset aolib.Offset `json:"offset"`
		Flip   aolib.Flip   `json:"flip"`
		Order  int          `json:"order"`
	}
	var wire []wireMember
	if err := json.Unmarshal(b, &wire); err != nil {
		return nil
	}
	out := make([]GroupPairMember, 0, len(wire))
	for _, w := range wire {
		out = append(out, GroupPairMember{
			CharID:  w.CharID,
			Name:    w.Name,
			Emote:   w.Emote,
			Side:    w.Side,
			OffsetX: w.Offset.X,
			OffsetY: w.Offset.Y,
			Flip:    w.Flip,
			Order:   w.Order,
		})
	}
	return out
}
