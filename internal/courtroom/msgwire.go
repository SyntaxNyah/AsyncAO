package courtroom

import (
	"encoding/binary"
	"strconv"
	"strings"
)

// DMs and group chats over the server's /pm, never broadcast; group state is rebuilt client-side.
// Received PMs arrive as a CT named "[PM] [UID <n>] <ooc name>"; that UID is the identity trusted for roles.

const (
	// msgFrameMagic is the first byte, distinct from the other frames' (1, 0x70, 0x71, 0x72, 0x73).
	msgFrameMagic  = 0x74
	msgWireVersion = 1

	maxGroupNameBytes = 48
	// msgUIDBytes is a kick target's UID: a big-endian uint32.
	msgUIDBytes = 4
)

type MsgKind uint8

const (
	MsgDM        MsgKind = 0 // a 1:1 direct message (no group)
	MsgGroupText MsgKind = 1 // a message to a group (GroupID set)
	MsgInvite    MsgKind = 2 // owner invites you to a group (GroupID + GroupName)
	MsgJoin      MsgKind = 3 // a member announces they joined (GroupID)
	MsgLeave     MsgKind = 4 // a member left a group (GroupID)
	MsgKick      MsgKind = 5 // owner removed a member (GroupID + TargetUID)
)

// WireMessage is a control frame; the sender comes from the server's PM attribution, so it can't be forged.
type WireMessage struct {
	Kind      MsgKind
	GroupID   uint32 // 0 for a plain DM
	TargetUID int    // MsgKick: the UID being removed
	GroupName string // MsgInvite: the group's display name
}

// EncodeMarker returns the invisible run to append after the PM's readable text.
func (m WireMessage) EncodeMarker() string {
	buf := binary.BigEndian.AppendUint32([]byte{msgFrameMagic, msgWireVersion, byte(m.Kind)}, m.GroupID)
	switch m.Kind {
	case MsgKick:
		buf = binary.BigEndian.AppendUint32(buf, uint32(m.TargetUID))
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

// DecodeMessageFrame returns the frame and the stripped body; ok=false with the body unchanged means no valid frame.
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
			GroupID: binary.BigEndian.Uint32(fr[3:]),
		}
		rest := fr[7:]
		switch m.Kind {
		case MsgKick:
			if len(rest) < msgUIDBytes {
				return WireMessage{}, body, false
			}
			m.TargetUID = int(binary.BigEndian.Uint32(rest))
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

// PMCommand builds the /pm command for every uid (the comma list fans out in one command); empty uids return "".
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

// ParsePMSender reads a CT Name's attribution; ok=false if not a received PM (the sender's echo starts "[PM →").
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
