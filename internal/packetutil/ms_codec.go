package packetutil

// MS (in-character chat) both-wire codec. aolib models the canonical 26-field
// MSToServer (client→server) and 30-field MSToClient (server→client), which omit
// the AO2-Client Fanta-only extensions AsyncAO carries on the wire: the custom
// shout name ("4&name"), the pair z-order ("id^order"), and the 2.9 blip name +
// slide flag. So FantaCode stays positional (lossless), while JSON is encoded
// through aolib's typed MS — dropping those Fanta-only extensions, exactly as
// Nyathena does.

import (
	"encoding/json"
	"fmt"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// msFields is the raw positional MS body (AsyncAO's feature-gated form).
type msFields struct {
	args []string
}

// RegisterMSCodec registers the MS packet as a both-wire codec.
func RegisterMSCodec() {
	RegisterCodec("MS", aolib.Codec{
		EncodeFanta: func(p any) ([]string, error) {
			switch v := p.(type) {
			case *msFields:
				return v.args, nil
			case *aolib.MSToClient:
				return v.Args(), nil
			}
			return nil, fmt.Errorf("packetutil: bad MS payload %T", p)
		},
		DecodeFanta: func(args []string) (any, error) { return &msFields{args: args}, nil },
		EncodeJSON: func(p any) (string, error) {
			mf, ok := p.(*msFields)
			if !ok {
				return "", fmt.Errorf("packetutil: bad MS payload %T", p)
			}
			typed, err := aolib.ParseMSToServer(mf.args)
			if err != nil {
				return "", err
			}
			b, err := aolib.Encode(typed, aolib.WireJSON)
			return string(b), err
		},
		DecodeJSON: func(raw string) (any, error) {
			v := aolib.MSToClient{
				DeskModifier:  aolib.DeskModifier("shown"),
				EmoteModifier: aolib.EmoteModifier("no_preanim"),
				ShoutModifier: aolib.ShoutModifier("none"),
				Flip:          aolib.Flip("none"),
				TextColor:     aolib.TextColor("white"),
				PairedCharID:  -1,
				PairedFlip:    aolib.Flip("none"),
			}
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return nil, err
			}
			return &v, nil
		},
	})
}
