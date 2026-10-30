// Package protocol implements the AO2 wire protocol as AO2-Client 2.11 speaks it, over WebSocket only.
package protocol

import (
	"fmt"
	"strings"
	"unicode"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

const (
	// FieldSeparator separates the header and fields on the wire.
	FieldSeparator = "#"
	// PacketTerminator ends every packet.
	PacketTerminator = "#%"
)

// EncodeField escapes one field for the wire; aolib owns the escaping.
func EncodeField(field string) string {
	return aolib.EscapeFanta(field)
}

// DecodeField unescapes one field from the wire; aolib owns the escaping.
func DecodeField(field string) string {
	return aolib.UnescapeFanta(field)
}

// SanitizeText drops invalid UTF-8 and control runes except newline and tab: SDL_ttf and the clipboard use
// NUL-terminated C strings, so an embedded NUL would silently hide the rest of a server's message.
// Both stdlib calls return the original string when nothing changes, so a clean payload doesn't allocate.
func SanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

// Packet is one AO protocol message.
type Packet struct {
	Header string
	Fields []string

	// typed is the optional aolib form Conn.Send prefers; Fields stays set (unescaped) for the Fanta fallback.
	typed aolib.Outgoing
}

// NewPacket builds a packet from a header and raw (unescaped) fields.
func NewPacket(header string, fields ...string) Packet {
	return Packet{Header: header, Fields: fields}
}

// NewTypedPacket builds an outbound packet from an aolib typed value or a custom value with a registered codec.
func NewTypedPacket(o aolib.Outgoing) Packet {
	return Packet{Header: o.Header(), Fields: unescapeArgs(o.Args()), typed: o}
}

// unescapeArgs turns aolib's already-escaped Fanta Args back into unescaped fields.
func unescapeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = aolib.UnescapeFanta(a)
	}
	return out
}

// String serializes the packet with field escaping: HEADER#field1#field2#%.
func (p Packet) String() string {
	var b strings.Builder
	size := len(p.Header) + len(PacketTerminator)
	for _, f := range p.Fields {
		size += len(f) + len(FieldSeparator)
	}
	b.Grow(size)
	b.WriteString(p.Header)
	for _, f := range p.Fields {
		b.WriteString(FieldSeparator)
		b.WriteString(EncodeField(f))
	}
	b.WriteString(PacketTerminator)
	return b.String()
}

// Field returns the decoded field at index i, or "" when absent, mirroring AO2-Client's tolerance of short packets.
func (p Packet) Field(i int) string {
	if i < 0 || i >= len(p.Fields) {
		return ""
	}
	return p.Fields[i]
}

// Typed returns the packet's aolib form, or nil for positional-only packets.
func (p Packet) Typed() aolib.Outgoing { return p.typed }

// ParsePacket parses one text frame as AO2-Client's websocketconnection.cpp does: it must end with #%.
func ParsePacket(message string) (Packet, error) {
	if !strings.HasSuffix(message, PacketTerminator) {
		return Packet{}, fmt.Errorf("protocol: message missing %q terminator: %.40q", PacketTerminator, message)
	}
	message = message[:len(message)-len(PacketTerminator)]
	parts := strings.Split(message, FieldSeparator)
	header := parts[0]
	fields := parts[1:]
	for i, f := range fields {
		fields[i] = DecodeField(f)
	}
	return Packet{Header: header, Fields: fields}, nil
}
