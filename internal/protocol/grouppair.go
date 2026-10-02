package protocol

import (
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
