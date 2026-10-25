package courtroom

import (
	"strconv"
	"strings"
)

// AsyncAO messaging: private DMs and group chats between AsyncAO users, carried
// over the SERVER's private-message command (/pm) — never the broadcast IC/OOC
// channel. So normal players in the room see nothing and no area is spammed; the
// server relays each message only to its recipients (the owner's logs see it, like
// all traffic — other players don't). The server attributes every PM to its real
// sender, so group roles (owner-only kick) can't be forged.
//
// The PM body is the human-readable text PLUS a tiny invisible control frame
// (magic 0x74) carrying the KIND and, for groups, the group id / control fields. A
// non-AsyncAO recipient just reads the text; AsyncAO threads it and acts on the
// frame. Group state (members, owner) is reconstructed client-side from these
// control messages — there is NO server-side group.
//
// Nyathena / Athena (and tsuserver) take "/pm <uid[,uid...]> <message>": the comma
// list fans the message out to every recipient in ONE command, the server
// delivering it privately to each. Received PMs arrive as a server CT whose Name is
// "[PM] [UID <n>] <ooc name>" — the UID there is the server's attribution, the
// trustworthy sender identity for roles.

const (
	// msgFrameMagic is this frame's leading byte, distinct from sprite-style (1),
	// profile (0x70), status (0x71), effects (0x72) and reaction (0x73) so the shared
	// zero-width scanner tells the codecs apart.
	msgFrameMagic  = 0x74
	msgWireVersion = 1

	// maxGroupNameBytes bounds an invite's group name on the wire.
	maxGroupNameBytes = 48
)

// MsgKind is the control kind of a messaging PM.
type MsgKind uint8

const (
	MsgDM        MsgKind = 0 // a 1:1 direct message (no group)
	MsgGroupText MsgKind = 1 // a message to a group (GroupID set)
	MsgInvite    MsgKind = 2 // owner invites you to a group (GroupID + GroupName)
	MsgJoin      MsgKind = 3 // a member announces they joined (GroupID)
	MsgLeave     MsgKind = 4 // a member left a group (GroupID)
	MsgKick      MsgKind = 5 // owner removed a member (GroupID + TargetUID)
)

// WireMessage is a decoded messaging control frame (SDL-free plain data). The
// human-readable text travels as the plain PM body, NOT here — this is only the
// metadata that threads / controls it. The sender is taken from the server's PM
// attribution (ParsePMSender), never from this frame, so it can't be forged.
type WireMessage struct {
	Kind      MsgKind
	GroupID   uint32 // 0 for a plain DM
	TargetUID int    // MsgKick: the UID being removed
	GroupName string // MsgInvite: the group's display name
}

// EncodeMarker returns the invisible zero-width run carrying this control frame, to
// append to the PM body after the human-readable text.
func (m WireMessage) EncodeMarker() string {
	buf := []byte{
		msgFrameMagic, msgWireVersion, byte(m.Kind),
		byte(m.GroupID >> 24), byte(m.GroupID >> 16), byte(m.GroupID >> 8), byte(m.GroupID),
	}
	switch m.Kind {
	case MsgKick:
		u := uint32(m.TargetUID)
		buf = append(buf, byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
	case MsgInvite:
		n := m.GroupName
		if len(n) > maxGroupNameBytes {
			n = n[:maxGroupNameBytes]
		}
		buf = append(buf, byte(len(n)))
		buf = append(buf, n...)
	}
	return packZeroWidth(buf)
}

// DecodeMessageFrame pulls a WireMessage out of a PM body if a well-formed messaging
// frame is present, and returns the body with all zero-width runes stripped (the
// human-readable text). ok=false (with the body unchanged) means no valid frame — a
// short / corrupt frame is benign. Coexists with any other frame on the same text.
func DecodeMessageFrame(body string) (WireMessage, string, bool) {
	if !hasMarker(body) {
		return WireMessage{}, body, false
	}
	for _, fr := range scanZeroWidthFrames(body) {
		if len(fr) < 7 || fr[0] != msgFrameMagic || fr[1] != msgWireVersion {
			continue
		}
		m := WireMessage{
			Kind:    MsgKind(fr[2]),
			GroupID: uint32(fr[3])<<24 | uint32(fr[4])<<16 | uint32(fr[5])<<8 | uint32(fr[6]),
		}
		rest := fr[7:]
		switch m.Kind {
		case MsgKick:
			if len(rest) < 4 {
				return WireMessage{}, body, false
			}
			m.TargetUID = int(uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3]))
		case MsgInvite:
			if len(rest) < 1 || len(rest) < 1+int(rest[0]) {
				return WireMessage{}, body, false
			}
			m.GroupName = string(rest[1 : 1+int(rest[0])])
		}
		return m, stripZeroWidth(body), true
	}
	return WireMessage{}, body, false
}

// PMCommand builds the OOC command that privately sends body to every uid via the
// server's /pm (the comma list fans out in one command). Empty uids → "".
func PMCommand(uids []int, body string) string {
	if len(uids) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("/pm ")
	for i, u := range uids {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(u))
	}
	sb.WriteByte(' ')
	sb.WriteString(body)
	return sb.String()
}

// ParsePMSender reads the server's PM attribution from an incoming CT Name field —
// "[PM] [UID <n>] <ooc name>" (Nyathena / Athena). Returns the sender UID and OOC
// name; ok=false when the Name isn't a received PM (e.g. the sender's own echo,
// which starts "[PM →"). The UID is the server's attribution — trust it for roles.
func ParsePMSender(name string) (uid int, oocName string, ok bool) {
	const prefix = "[PM] [UID "
	if !strings.HasPrefix(name, prefix) {
		return 0, "", false
	}
	rest := name[len(prefix):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return 0, "", false
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil {
		return 0, "", false
	}
	return n, strings.TrimSpace(rest[end+1:]), true
}
