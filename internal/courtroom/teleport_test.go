package courtroom

// AsyncAO fork addition: TP (Teleport) reducer tests. TP mirrors GP: a typed
// custom packet arrives through the aolib session, lands as a positional Packet
// with a typed payload, and the reducer converts it into a render model plus an
// event. The UI pulls Session.Teleport when it sees EventTeleport.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

func tpTestPacket() protocol.Packet {
	return protocol.NewTypedPacket(&packetutil.TPPacket{
		Op:         "restore",
		SnapshotID: "wr-1",
		Label:      "Radio Tower",
		Divergence: "1.048596",
		Mode:       "authoritative",
		IssuedAt:   1761177600,
		State: packetutil.TPState{
			Area: packetutil.TPArea{
				Name:       "Radio Tower",
				Background: "sg_rt",
				Side:       "def",
				Music:      packetutil.TPMusic{Name: "Gate of Steiner", Looping: true},
				Status:     1,
				Evidence:   []string{"Divergence Meter"},
			},
			Cast: []packetutil.TPCastMember{{
				UID: 1, CharID: 3, Name: "okabe", Emote: "normal", Side: "def", Order: 0,
			}},
			Log: []packetutil.TPLogLine{{
				TS: 1761177500, UID: 1, CharID: 3, Name: "okabe", Emote: "normal",
				Side: "def", Text: "El Psy Kongroo.", Color: 0,
			}},
		},
	})
}

func TestHandlePacketTP(t *testing.T) {
	s, _ := captureSession()

	ev := s.HandlePacket(tpTestPacket())

	if len(ev) != 1 || ev[0].Kind != EventTeleport {
		t.Fatalf("events = %+v, want one EventTeleport", ev)
	}
	if s.Teleport == nil {
		t.Fatal("Session.Teleport is nil after a TP packet")
	}
	tp := s.Teleport
	if tp.SnapshotID != "wr-1" || tp.Mode != "authoritative" || tp.Divergence != "1.048596" {
		t.Errorf("snapshot meta = %+v", tp)
	}
	if tp.Area.Background != "sg_rt" || tp.Area.MusicName != "Gate of Steiner" || !tp.Area.MusicLooping {
		t.Errorf("area = %+v", tp.Area)
	}
	if len(tp.Area.Evidence) != 1 || tp.Area.Evidence[0] != "Divergence Meter" {
		t.Errorf("evidence = %+v", tp.Area.Evidence)
	}
	if len(tp.Members) != 1 || tp.Members[0].CharID != 3 || tp.Members[0].Order != 0 {
		t.Errorf("members = %+v", tp.Members)
	}
	if len(tp.Log) != 1 || tp.Log[0].Text != "El Psy Kongroo." {
		t.Errorf("log = %+v", tp.Log)
	}
}

// TestHandlePacketTPClearsOnUntyped verifies an empty TP (no typed payload) drops
// the snapshot instead of leaving a stale room on screen.
func TestHandlePacketTPClearsOnUntyped(t *testing.T) {
	s, _ := captureSession()
	s.Teleport = &protocol.Teleport{SnapshotID: "stale"}

	feed(t, s, "TP#%")

	if s.Teleport != nil {
		t.Fatalf("untyped TP left a stale snapshot: %+v", s.Teleport)
	}
}

// TestTeleportFromNonTPPacket verifies the accessor is type-safe.
func TestTeleportFromNonTPPacket(t *testing.T) {
	p, err := protocol.ParsePacket("GP#100#%")
	if err != nil {
		t.Fatal(err)
	}
	if protocol.TeleportFromPacket(p) != nil {
		t.Fatal("non-TP packet produced a Teleport snapshot")
	}
}
