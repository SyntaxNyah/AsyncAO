package protocol

import (
	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
)

// TeleportMember is one participant's render state at snapshot time. Order is
// the z-order captured on the server; it is preserved verbatim so a restore is
// not re-sorted by UID.
type TeleportMember struct {
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

// TeleportLine is one IC message to replay, oldest first.
type TeleportLine struct {
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

// TeleportArea is the room-level half of a snapshot.
type TeleportArea struct {
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

// Teleport is the render model of a TP snapshot. Mode is "authoritative" (the
// server rolled the room back, so every peer already agrees) or "overlay" (only
// TP-capable peers rewind).
type Teleport struct {
	Op         string
	SnapshotID string
	Label      string
	Divergence string
	Mode       string
	IssuedAt   int64
	Area       TeleportArea
	Members    []TeleportMember
	Log        []TeleportLine
}

// FromTP converts the wire TP packet into the render model.
func FromTP(tp *packetutil.TPPacket) *Teleport {
	if tp == nil {
		return nil
	}
	out := &Teleport{
		Op:         tp.Op,
		SnapshotID: tp.SnapshotID,
		Label:      tp.Label,
		Divergence: tp.Divergence,
		Mode:       tp.Mode,
		IssuedAt:   tp.IssuedAt,
		Area: TeleportArea{
			Name:          tp.State.Area.Name,
			Background:    tp.State.Area.Background,
			Side:          tp.State.Area.Side,
			MusicName:     tp.State.Area.Music.Name,
			MusicCharID:   tp.State.Area.Music.CharID,
			MusicShowname: tp.State.Area.Music.Showname,
			MusicLooping:  tp.State.Area.Music.Looping,
			Status:        tp.State.Area.Status,
			Evidence:      append([]string(nil), tp.State.Area.Evidence...),
			Testimony:     append([]string(nil), tp.State.Area.Testimony...),
		},
		Members: make([]TeleportMember, 0, len(tp.State.Cast)),
		Log:     make([]TeleportLine, 0, len(tp.State.Log)),
	}
	for _, m := range tp.State.Cast {
		out.Members = append(out.Members, TeleportMember{
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
	for _, l := range tp.State.Log {
		out.Log = append(out.Log, TeleportLine{
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

// TeleportFromPacket extracts the snapshot from a TP Packet's typed value (nil
// for non-TP or untyped packets).
func TeleportFromPacket(p Packet) *Teleport {
	if tp, ok := p.Typed().(*packetutil.TPPacket); ok {
		return FromTP(tp)
	}
	return nil
}
