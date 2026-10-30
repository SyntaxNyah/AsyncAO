package packetutil

import (
	"encoding/json"
	"fmt"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// voiceCodec builds a both-wire codec; T is the inbound payload, and outbound takes what EncodeFanta is handed.
func voiceCodec[T aolib.Outgoing](parse func([]string) (T, error)) Codec {
	return Codec{
		EncodeFanta: func(p any) ([]string, error) {
			if o, ok := p.(aolib.Outgoing); ok {
				return o.Args(), nil
			}
			return nil, fmt.Errorf("packetutil: not an Outgoing: %T", p)
		},
		DecodeFanta: func(args []string) (any, error) { return parse(args) },
		EncodeJSON:  func(p any) ([]byte, error) { return json.Marshal(p) },
		DecodeJSON: func(raw []byte) (any, error) {
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			return v, nil
		},
	}
}

// RegisterVoiceCodecs installs every VS_* codec; bidirectional headers decode to the server-to-client shape.
func RegisterVoiceCodecs() {
	Register[*VS_CAPS]("VS_CAPS", voiceCodec(ParseVS_CAPS))
	Register[*VS_PEERS]("VS_PEERS", voiceCodec(ParseVS_PEERS))
	Register[*VS_AUDIO]("VS_AUDIO", voiceCodec(ParseVS_AUDIO))
	Register[*VS_FRAME]("VS_FRAME", voiceCodec(ParseVS_FRAME))
	Register[*VS_JOINToClient]("VS_JOIN", voiceCodec(ParseVS_JOINToClient))
	Register[*VS_LEAVEToClient]("VS_LEAVE", voiceCodec(ParseVS_LEAVEToClient))
	Register[*VS_SPEAKToClient]("VS_SPEAK", voiceCodec(ParseVS_SPEAKToClient))
}
