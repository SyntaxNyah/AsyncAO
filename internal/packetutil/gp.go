package packetutil

import (
	"encoding/json"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// GP is the JSON-only server-to-client group-pair roster; list position is the z-order (front first), speaker included.
type GP struct {
	GroupID string     `json:"group_id"`
	Members []GPMember `json:"members"`
}

// GPMember is one ordered member of a group roster.
type GPMember struct {
	UID    int          `json:"uid"`
	CharID int          `json:"char_id"`
	Name   string       `json:"name"`
	Emote  string       `json:"emote"`
	Side   string       `json:"side"`
	Offset aolib.Offset `json:"offset"`
	Flip   aolib.Flip   `json:"flip"`
	Order  int          `json:"order"`
}

// Header returns the wire header "GP".
func (p *GP) Header() string { return "GP" }

// Args is unused: GP is JSON-only and never encoded as FantaCode.
func (p *GP) Args() []string { return nil }

// RegisterGroupPairCodec has no FantaCode form: the server sends GP only to JSON peers advertising "grouppair".
func RegisterGroupPairCodec() {
	aolib.RegisterPacket("GP", aolib.PacketOptions[*GP]{
		JSON: &aolib.JSONForm[*GP]{
			Encode: func(t *GP) ([]byte, error) { return json.Marshal(t) },
			Decode: func(raw []byte) (*GP, error) {
				var v GP
				if err := json.Unmarshal(raw, &v); err != nil {
					return nil, err
				}
				return &v, nil
			},
		},
	})
}
