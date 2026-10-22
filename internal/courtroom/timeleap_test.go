package courtroom

// AsyncAO fork addition: TL (TimeLeap) reducer tests. TL mirrors GP: a typed
// custom packet arrives through the aolib session, lands as a positional Packet
// with a typed payload, and the reducer converts it into a render model plus an
// event. The UI pulls Session.TimeLeap when it sees EventTimeLeap.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

func tlTestPacket() protocol.Packet {
	return protocol.NewTypedPacket(&packetutil.TLPacket{
		Op:         "restore",
		SnapshotID: "wr-1",
		Label:      "Radio Tower",
		Divergence: "1.048596",
		Mode:       "authoritative",
		IssuedAt:   1761177600,
		State: packetutil.TLState{
			Area: packetutil.TLArea{
				Name:       "Radio Tower",
				Background: "sg_rt",
				Side:       "def",
				Music:      packetutil.TLMusic{Name: "Gate of Steiner", Looping: true},
				Status:     1,
				Evidence:   []string{"Divergence Meter"},
			},
			Cast: []packetutil.TLCastMember{{
				UID: 1, CharID: 3, Name: "okabe", Emote: "normal", Side: "def", Order: 0,
			}},
			Log: []packetutil.TLLogLine{{
				TS: 1761177500, UID: 1, CharID: 3, Name: "okabe", Emote: "normal",
				Side: "def", Text: "El Psy Kongroo.", Color: 0,
			}},
		},
	})
}

func TestHandlePacketTL(t *testing.T) {
	s, _ := captureSession()

	ev := s.HandlePacket(tlTestPacket())

	if len(ev) != 1 || ev[0].Kind != EventTimeLeap {
		t.Fatalf("events = %+v, want one EventTimeLeap", ev)
	}
	if s.TimeLeap == nil {
		t.Fatal("Session.TimeLeap is nil after a TL packet")
	}
	tl := s.TimeLeap
	if tl.SnapshotID != "wr-1" || tl.Mode != "authoritative" || tl.Divergence != "1.048596" {
		t.Errorf("snapshot meta = %+v", tl)
	}
	if tl.Area.Background != "sg_rt" || tl.Area.MusicName != "Gate of Steiner" || !tl.Area.MusicLooping {
		t.Errorf("area = %+v", tl.Area)
	}
	if len(tl.Area.Evidence) != 1 || tl.Area.Evidence[0] != "Divergence Meter" {
		t.Errorf("evidence = %+v", tl.Area.Evidence)
	}
	if len(tl.Members) != 1 || tl.Members[0].CharID != 3 || tl.Members[0].Order != 0 {
		t.Errorf("members = %+v", tl.Members)
	}
	if len(tl.Log) != 1 || tl.Log[0].Text != "El Psy Kongroo." {
		t.Errorf("log = %+v", tl.Log)
	}
}

// TestHandlePacketTLClearsOnUntyped verifies an empty TL (no typed payload) drops
// the snapshot instead of leaving a stale room on screen.
func TestHandlePacketTLClearsOnUntyped(t *testing.T) {
	s, _ := captureSession()
	s.TimeLeap = &protocol.TimeLeap{SnapshotID: "stale"}

	feed(t, s, "TL#%")

	if s.TimeLeap != nil {
		t.Fatalf("untyped TL left a stale snapshot: %+v", s.TimeLeap)
	}
}

// TestTimeLeapFromNonTLPacket verifies the accessor is type-safe.
func TestTimeLeapFromNonTLPacket(t *testing.T) {
	p, err := protocol.ParsePacket("GP#100#%")
	if err != nil {
		t.Fatal(err)
	}
	if protocol.TimeLeapFromPacket(p) != nil {
		t.Fatal("non-TL packet produced a TimeLeap snapshot")
	}
}
