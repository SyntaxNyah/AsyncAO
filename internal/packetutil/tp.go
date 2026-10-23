package packetutil

import (
	"encoding/json"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// TPPacket is the server→client Teleport snapshot: a JSON-only custom packet
// (header "TP") carrying a full room snapshot so the client can rewind its view.
// The shape mirrors Nyathena's athena.TPPacket field for field; it is not part
// of the spec and has no FantaCode form.
type TPPacket struct {
	// Op is "restore" (broadcast) or "save" (private ack to the issuer).
	Op string `json:"op"`
	// SnapshotID is the id /tp <id> accepts; Label is display-only.
	SnapshotID string `json:"snapshot_id"`
	Label      string `json:"label"`
	// Divergence is presentation-only (the meter's value).
	Divergence string `json:"divergence"`
	IssuedBy   int    `json:"issued_by"`
	IssuedAt   int64  `json:"issued_at"`
	TTLSeconds int    `json:"ttl_s"`
	// Mode is "authoritative" (the room itself was rolled back, so every peer
	// already agrees) or "overlay" (only TP-capable peers rewind).
	Mode  string  `json:"mode"`
	State TPState `json:"state"`
}

// TPState is the restorable scene.
type TPState struct {
	Area TPArea         `json:"area"`
	Cast []TPCastMember `json:"cast"`
	Log  []TPLogLine    `json:"log"`
}

// TPArea is the room-level half of a snapshot (everything the server stores).
type TPArea struct {
	Name       string   `json:"name"`
	Background string   `json:"background"`
	Side       string   `json:"side"`
	Music      TPMusic  `json:"music"`
	Status     int      `json:"status"`
	Evidence   []string `json:"evidence"`
	Testimony  []string `json:"testimony"`
}

// TPMusic mirrors the MC fields needed to re-stage the track.
type TPMusic struct {
	Name     string `json:"name"`
	CharID   int    `json:"char_id"`
	Showname string `json:"showname"`
	Looping  bool   `json:"looping"`
}

// TPCastMember is one participant's render state at capture time.
type TPCastMember struct {
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

// TPLogLine is one replayed IC message.
type TPLogLine struct {
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

// Header returns the wire header "TP".
func (p *TPPacket) Header() string { return "TP" }

// Args is unused: TP is JSON-only and never encoded as FantaCode.
func (p *TPPacket) Args() []string { return nil }

// RegisterTeleportCodec installs the JSON-only TP codec — the exact shape of
// RegisterGroupPairCodec. There is no FantaCode form: the server only sends TP
// to JSON peers that advertised "teleport" in their FL.
func RegisterTeleportCodec() {
	aolib.RegisterPacket("TP", aolib.PacketOptions[*TPPacket]{
		JSON: &aolib.JSONForm[*TPPacket]{
			Encode: func(t *TPPacket) ([]byte, error) { return json.Marshal(t) },
			Decode: func(raw []byte) (*TPPacket, error) {
				var v TPPacket
				if err := json.Unmarshal(raw, &v); err != nil {
					return nil, err
				}
				return &v, nil
			},
		},
	})
}
