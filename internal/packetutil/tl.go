package packetutil

import (
	"encoding/json"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// TLPacket is the server→client TimeLeap snapshot: a JSON-only custom packet
// (header "TL") carrying a full room snapshot so the client can rewind its view.
// The shape mirrors Nyathena's athena.TLPacket field for field; it is not part
// of the spec and has no FantaCode form.
type TLPacket struct {
	// Op is "restore" (broadcast) or "save" (private ack to the issuer).
	Op string `json:"op"`
	// SnapshotID is the id /tl <id> accepts; Label is display-only.
	SnapshotID string `json:"snapshot_id"`
	Label      string `json:"label"`
	// Divergence is presentation-only (the meter's value).
	Divergence string `json:"divergence"`
	IssuedBy   int    `json:"issued_by"`
	IssuedAt   int64  `json:"issued_at"`
	TTLSeconds int    `json:"ttl_s"`
	// Mode is "authoritative" (the room itself was rolled back, so every peer
	// already agrees) or "overlay" (only TL-capable peers rewind).
	Mode  string  `json:"mode"`
	State TLState `json:"state"`
}

// TLState is the restorable scene.
type TLState struct {
	Area TLArea         `json:"area"`
	Cast []TLCastMember `json:"cast"`
	Log  []TLLogLine    `json:"log"`
}

// TLArea is the room-level half of a snapshot (everything the server stores).
type TLArea struct {
	Name       string   `json:"name"`
	Background string   `json:"background"`
	Side       string   `json:"side"`
	Music      TLMusic  `json:"music"`
	Status     int      `json:"status"`
	Evidence   []string `json:"evidence"`
	Testimony  []string `json:"testimony"`
}

// TLMusic mirrors the MC fields needed to re-stage the track.
type TLMusic struct {
	Name     string `json:"name"`
	CharID   int    `json:"char_id"`
	Showname string `json:"showname"`
	Looping  bool   `json:"looping"`
}

// TLCastMember is one participant's render state at capture time.
type TLCastMember struct {
	UID      int          `json:"uid"`
	CharID   int          `json:"char_id"`
	Name     string       `json:"name"`
	Emote    string       `json:"emote"`
	Showname string       `json:"showname"`
	Side     string       `json:"side"`
	Offset   aolib.Offset `json:"offset"`
	Flip     aolib.Flip   `json:"flip"`
	Order    int          `json:"order"`
}

// TLLogLine is one replayed IC message.
type TLLogLine struct {
	TS       int64  `json:"ts"`
	UID      int    `json:"uid"`
	CharID   int    `json:"char_id"`
	Name     string `json:"name"`
	Emote    string `json:"emote"`
	Showname string `json:"showname"`
	Side     string `json:"side"`
	Text     string `json:"text"`
	Color    int    `json:"color"`
}

// Header returns the wire header "TL".
func (p *TLPacket) Header() string { return "TL" }

// Args is unused: TL is JSON-only and never encoded as FantaCode.
func (p *TLPacket) Args() []string { return nil }

// RegisterTimeLeapCodec installs the JSON-only TL codec — the exact shape of
// RegisterGroupPairCodec. There is no FantaCode form: the server only sends TL
// to JSON peers that advertised "timeleap" in their FL.
func RegisterTimeLeapCodec() {
	aolib.RegisterPacket("TL", aolib.PacketOptions[*TLPacket]{
		JSON: &aolib.JSONForm[*TLPacket]{
			Encode: func(t *TLPacket) ([]byte, error) { return json.Marshal(t) },
			Decode: func(raw []byte) (*TLPacket, error) {
				var v TLPacket
				if err := json.Unmarshal(raw, &v); err != nil {
					return nil, err
				}
				return &v, nil
			},
		},
	})
}
