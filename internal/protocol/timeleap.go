package protocol

import (
	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
)

// TimeLeapMember is one participant's render state at snapshot time. Order is
// the z-order captured on the server; it is preserved verbatim so a restore is
// not re-sorted by UID.
type TimeLeapMember struct {
	UID      int
	CharID   int
	Name     string
	Emote    string
	Showname string
	Side     string
	OffsetX  int
	OffsetY  int
	Flip     aolib.Flip
	Order    int
}

// TimeLeapLine is one IC message to replay, oldest first.
type TimeLeapLine struct {
	TS       int64
	UID      int
	CharID   int
	Name     string
	Emote    string
	Showname string
	Side     string
	Text     string
	Color    int
}

// TimeLeapArea is the room-level half of a snapshot.
type TimeLeapArea struct {
	Name          string
	Background    string
	Side          string
	MusicName     string
	MusicCharID   int
	MusicShowname string
	MusicLooping  bool
	Status        int
	Evidence      []string
	Testimony     []string
}

// TimeLeap is the render model of a TL snapshot. Mode is "authoritative" (the
// server rolled the room back, so every peer already agrees) or "overlay" (only
// TL-capable peers rewind).
type TimeLeap struct {
	Op         string
	SnapshotID string
	Label      string
	Divergence string
	Mode       string
	IssuedAt   int64
	Area       TimeLeapArea
	Members    []TimeLeapMember
	Log        []TimeLeapLine
}

// FromTL converts the wire TL packet into the render model.
func FromTL(tl *packetutil.TLPacket) *TimeLeap {
	if tl == nil {
		return nil
	}
	out := &TimeLeap{
		Op:         tl.Op,
		SnapshotID: tl.SnapshotID,
		Label:      tl.Label,
		Divergence: tl.Divergence,
		Mode:       tl.Mode,
		IssuedAt:   tl.IssuedAt,
		Area: TimeLeapArea{
			Name:          tl.State.Area.Name,
			Background:    tl.State.Area.Background,
			Side:          tl.State.Area.Side,
			MusicName:     tl.State.Area.Music.Name,
			MusicCharID:   tl.State.Area.Music.CharID,
			MusicShowname: tl.State.Area.Music.Showname,
			MusicLooping:  tl.State.Area.Music.Looping,
			Status:        tl.State.Area.Status,
			Evidence:      append([]string(nil), tl.State.Area.Evidence...),
			Testimony:     append([]string(nil), tl.State.Area.Testimony...),
		},
		Members: make([]TimeLeapMember, 0, len(tl.State.Cast)),
		Log:     make([]TimeLeapLine, 0, len(tl.State.Log)),
	}
	for _, m := range tl.State.Cast {
		out.Members = append(out.Members, TimeLeapMember{
			UID:      m.UID,
			CharID:   m.CharID,
			Name:     m.Name,
			Emote:    m.Emote,
			Showname: m.Showname,
			Side:     m.Side,
			OffsetX:  m.Offset.X,
			OffsetY:  m.Offset.Y,
			Flip:     m.Flip,
			Order:    m.Order,
		})
	}
	for _, l := range tl.State.Log {
		out.Log = append(out.Log, TimeLeapLine{
			TS:       l.TS,
			UID:      l.UID,
			CharID:   l.CharID,
			Name:     l.Name,
			Emote:    l.Emote,
			Showname: l.Showname,
			Side:     l.Side,
			Text:     l.Text,
			Color:    l.Color,
		})
	}
	return out
}

// TimeLeapFromPacket extracts the snapshot from a TL Packet's typed value (nil
// for non-TL or untyped packets).
func TimeLeapFromPacket(p Packet) *TimeLeap {
	if tl, ok := p.Typed().(*packetutil.TLPacket); ok {
		return FromTL(tl)
	}
	return nil
}
