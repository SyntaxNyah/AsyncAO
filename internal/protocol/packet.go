// Package protocol implements the AO2 wire protocol (≥ 2.6 with 2.8/2.9
// extensions) exactly as AO2-Client 2.11 speaks it: WebSocket text frames
// carrying #-delimited packets with <num>-style escaping. Legacy raw-TCP
// framing is deliberately not implemented (WebSocket-only client).
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

// EncodeField escapes one field for the wire. Delegated to aolib so the
// metacharacter escaping has a single source of truth.
func EncodeField(field string) string {
	return aolib.EscapeFanta(field)
}

// DecodeField unescapes one field from the wire. Delegated to aolib.
func DecodeField(field string) string {
	return aolib.UnescapeFanta(field)
}

// SanitizeText makes a server-authored string safe to RENDER, without changing what
// it says. Newlines and tabs survive (they carry the layout of a multi-line notice);
// every other control rune and every invalid UTF-8 sequence is dropped.
//
// It lives here, at the seam where server bytes become Go strings, because both paths
// that carry server prose to a texture need exactly this and neither may re-implement
// it: internal/courtroom's kick/ban/notice cap and this package's own refusal-body
// reader.
//
// WHY IT IS NOT COSMETIC. Every draw ends in SDL_ttf and every copy ends in
// SDL_SetClipboardText, and both marshal through C.CString — which is NUL-terminated.
// An embedded NUL therefore truncates the C string silently, so a server could put one
// byte in front of its own access code and the box would show, and Copy would hand
// over, everything up to that byte and nothing after it. That is the exact "the tail of
// the message is unreachable" failure the notice box exists to end, through a different
// door. An invalid sequence (a payload cut mid-rune upstream) draws as tofu, which is
// noise the user cannot act on either.
//
// Both stdlib calls return the original string untouched when there is nothing to do,
// so a clean payload — every real one — costs no allocation.
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

	// typed is the optional canonical aolib form of an OUTBOUND packet. When
	// set, Conn.Send encodes it through aolib (correct enum strings + field
	// mapping, valid JSON) instead of reconstructing a typed value from the
	// positional Fields. Fields is still populated (unescaped) for tests and
	// the Fanta fallback.
	typed aolib.Outgoing
}

// NewPacket builds a packet from a header and raw (unescaped) fields.
func NewPacket(header string, fields ...string) Packet {
	return Packet{Header: header, Fields: fields}
}

// NewTypedPacket builds an outbound packet from an aolib typed value (a
// canonical C2S struct, or a custom value with a registered codec). Conn.Send
// encodes it through aolib.
func NewTypedPacket(o aolib.Outgoing) Packet {
	return Packet{Header: o.Header(), Fields: unescapeArgs(o.Args()), typed: o}
}

// unescapeArgs unescapes aolib's Fanta-form Args (which are already escaped for
// the wire) back to unescaped fields, so Packet.Fields stays unescaped for any
// positional consumer (tests, fallback).
func unescapeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = aolib.UnescapeFanta(a)
	}
	return out
}

// String serializes the packet with field escaping:
// HEADER#field1#field2#%.
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

// Field returns the decoded field at index i, or "" when absent — mirroring
// AO2-Client's tolerance of short packets.
func (p Packet) Field(i int) string {
	if i < 0 || i >= len(p.Fields) {
		return ""
	}
	return p.Fields[i]
}

// Typed returns the packet's canonical aolib form, or nil for positional-only
// packets (tests, unmodeled headers). The courtroom reducer reads it to consume
// the typed value directly.
func (p Packet) Typed() aolib.Outgoing { return p.typed }

// ParsePacket parses one wire message (one WebSocket text frame): it must
// end with #%, the first #-segment is the header, and every following field
// is unescaped — exactly AO2-Client's websocketconnection.cpp.
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
