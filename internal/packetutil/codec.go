package packetutil

import (
	"encoding/json"
	"fmt"
	"strings"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// IsJSON reports whether raw is a JSON wire frame (it starts with '{').
func IsJSON(raw []byte) bool {
	return len(raw) > 0 && raw[0] == '{'
}

// EscapeAll escapes every positional field for the Fanta wire.
func EscapeAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = aolib.EscapeFanta(a)
	}
	return out
}

// UnescapeAll unescapes every positional field from the Fanta wire.
func UnescapeAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = aolib.UnescapeFanta(a)
	}
	return out
}

// customCodecs holds the registered codecs for AsyncAO's nonstandard headers.
// aolib keeps its own registry private and only dispatches custom packets via
// the session API, so AsyncAO tracks them here for its own wire dispatch.
var customCodecs = map[string]aolib.Codec{}

// RegisterCodec registers a both-wire codec for a custom header, both with
// aolib and in AsyncAO's dispatch table.
func RegisterCodec(header string, c aolib.Codec) {
	aolib.RegisterCodec(header, c)
	customCodecs[header] = c
}

// IsCustom reports whether a header has an AsyncAO custom codec.
func IsCustom(header string) bool {
	_, ok := customCodecs[header]
	return ok
}

// encodeCustom serializes a typed custom packet through its registered codec in
// the given wire mode, applying framing (header + trailing "%" for Fanta, the
// "$header" key for JSON).
func encodeCustom(header string, p any, c aolib.Codec, mode aolib.WireMode) ([]byte, error) {
	switch mode {
	case aolib.WireFanta:
		args, err := c.EncodeFanta(p)
		if err != nil {
			return nil, err
		}
		return frameFanta(header, args), nil
	case aolib.WireJSON:
		raw, err := c.EncodeJSON(p)
		if err != nil {
			return nil, err
		}
		return ensureHeader([]byte(raw), header)
	default:
		return nil, fmt.Errorf("packetutil: unknown wire mode %d", mode)
	}
}

// EncodeCustom encodes a typed value for a header that has a registered codec
// (the VS_* voice headers) in the given wire mode. It is the both-wire path for
// custom packets; canonical packets go through aolib.Encode instead.
func EncodeCustom(header string, p any, mode aolib.WireMode) ([]byte, error) {
	c, ok := customCodecs[header]
	if !ok {
		return nil, fmt.Errorf("packetutil: no codec for %q", header)
	}
	return encodeCustom(header, p, c, mode)
}

// frameFanta frames header + positional args into HEADER#a#b#...#%. The args
// are expected to already be escaped (aolib's EncodeFanta contract).
func frameFanta(header string, args []string) []byte {
	var b strings.Builder
	b.WriteString(header)
	for _, a := range args {
		b.WriteByte('#')
		b.WriteString(a)
	}
	b.WriteString("#%")
	return []byte(b.String())
}

// ensureHeader guarantees the JSON object carries a matching "$header" key.
func ensureHeader(raw []byte, header string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("packetutil: codec JSON for %q is not an object: %w", header, err)
	}
	h, _ := json.Marshal(header)
	obj["$header"] = h
	return json.Marshal(obj)
}
