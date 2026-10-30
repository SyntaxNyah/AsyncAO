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

// Codec is a both-wire codec for a custom header, type-erased so the encode path needs no type switch.
type Codec struct {
	EncodeFanta func(p any) ([]string, error)
	DecodeFanta func(args []string) (any, error)
	EncodeJSON  func(p any) ([]byte, error)
	DecodeJSON  func(raw []byte) (any, error)
}

// customCodecs holds AsyncAO's nonstandard headers, because aolib keeps its registry private.
var customCodecs = map[string]Codec{}

// Register installs a codec in aolib (inbound decode) and customCodecs (outbound encode); T is the inbound
// type, and bidirectional headers encode through EncodeCustom, so the aolib Encode closures are unused.
func Register[T any](header string, c Codec) {
	customCodecs[header] = c
	aolib.RegisterPacket(header, aolib.PacketOptions[T]{
		Fanta: &aolib.Fanta[T]{
			Encode: func(t T) ([]string, error) { return c.EncodeFanta(t) },
			Decode: func(args []string) (T, error) {
				v, err := c.DecodeFanta(args)
				if err != nil {
					var zero T
					return zero, err
				}
				return v.(T), nil
			},
		},
		JSON: &aolib.JSONForm[T]{
			Encode: func(t T) ([]byte, error) { return c.EncodeJSON(t) },
			Decode: func(raw []byte) (T, error) {
				v, err := c.DecodeJSON(raw)
				if err != nil {
					var zero T
					return zero, err
				}
				return v.(T), nil
			},
		},
	})
}

// IsCustom reports whether a header has an AsyncAO custom codec.
func IsCustom(header string) bool {
	_, ok := customCodecs[header]
	return ok
}

// encodeCustom applies framing: header and trailing "%" for Fanta, the "$header" key for JSON.
func encodeCustom(header string, p any, c Codec, mode aolib.WireMode) ([]byte, error) {
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
		return ensureHeader(raw, header)
	default:
		return nil, fmt.Errorf("packetutil: unknown wire mode %d", mode)
	}
}

// EncodeCustom encodes a value for a header with a registered codec; canonical packets go through aolib.Encode.
func EncodeCustom(header string, p any, mode aolib.WireMode) ([]byte, error) {
	c, ok := customCodecs[header]
	if !ok {
		return nil, fmt.Errorf("packetutil: no codec for %q", header)
	}
	return encodeCustom(header, p, c, mode)
}

// frameFanta frames HEADER#a#b#...#%; args must already be escaped (aolib's EncodeFanta contract).
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

// ensureHeader makes sure the JSON object has a matching "$header" key.
func ensureHeader(raw []byte, header string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("packetutil: codec JSON for %q is not an object: %w", header, err)
	}
	h, _ := json.Marshal(header)
	obj["$header"] = h
	return json.Marshal(obj)
}
