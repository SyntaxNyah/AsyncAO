package packetutil

import (
	"encoding/json"
	"fmt"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// argsOf returns the FantaCode positional args for any VS_* value (each type
// implements aolib.Outgoing). It is the codecs' shared EncodeFanta: a custom
// packet is handed to EncodeFanta in both the outbound direction (the
// client→server shape) and, on a JSON receive, the server→client shape, and
// each type's own Args() emits its positional form.
func argsOf(p any) ([]string, error) {
	if o, ok := p.(aolib.Outgoing); ok {
		return o.Args(), nil
	}
	return nil, fmt.Errorf("packetutil: not an Outgoing: %T", p)
}

// jsonDecoder allocates a fresh typed value and unmarshals a JSON frame into it.
func jsonDecoder[T any](raw string) (any, error) {
	v := new(T)
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return nil, err
	}
	return v, nil
}

// RegisterVoiceCodecs installs the both-wire codecs for every VS_* header. The
// bidirectional headers (VS_JOIN / VS_LEAVE / VS_SPEAK) carry a different shape
// per direction, so decodeFanta reconstructs the client→server shape (what the
// outbound direction sends) while the JSON decode produces the server→client shape.
func RegisterVoiceCodecs() {
	registerVS("VS_CAPS", func(a []string) (any, error) { return ParseVS_CAPS(a) }, jsonDecoder[VS_CAPS])
	registerVS("VS_PEERS", func(a []string) (any, error) { return ParseVS_PEERS(a) }, jsonDecoder[VS_PEERS])
	registerVS("VS_AUDIO", func(a []string) (any, error) { return ParseVS_AUDIO(a) }, jsonDecoder[VS_AUDIO])
	registerVS("VS_FRAME", func(a []string) (any, error) { return ParseVS_FRAME(a) }, jsonDecoder[VS_FRAME])

	registerVS("VS_JOIN", func(a []string) (any, error) { return &VS_JOINToServer{}, nil }, jsonDecoder[VS_JOINToClient])
	registerVS("VS_LEAVE", func(a []string) (any, error) { return &VS_LEAVEToServer{}, nil }, jsonDecoder[VS_LEAVEToClient])
	registerVS("VS_SPEAK", func(a []string) (any, error) { return ParseVS_SPEAKToServer(a) }, jsonDecoder[VS_SPEAKToClient])
}

// registerVS registers one both-wire codec for a voice header.
func registerVS(header string, decodeFanta func([]string) (any, error), decodeJSON func(string) (any, error)) {
	RegisterCodec(header, aolib.Codec{
		EncodeFanta: argsOf,
		DecodeFanta: decodeFanta,
		EncodeJSON: func(p any) (string, error) {
			b, err := json.Marshal(p)
			return string(b), err
		},
		DecodeJSON: decodeJSON,
	})
}
