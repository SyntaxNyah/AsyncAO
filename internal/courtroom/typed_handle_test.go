package courtroom

import (
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestHandleTypedConsumesTypedStructs exercises the typed reduce path: inbound
// packets that carry an aolib typed value (everything the conn's On* hooks
// enqueue) must reduce through handleTyped, producing the same events the
// positional path does. It pins the enum→int mappings (HP bar, JD state, ARUP
// type) that are the easy place for a typed migration to slip.
func TestHandleTypedConsumesTypedStructs(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")

	// HP defense → bar 1, HPDef.
	if ev := s.HandlePacket(protocol.NewTypedPacket(&aolib.HPToClient{Bar: aolib.PenaltyBarDefense, Value: 5})); len(ev) != 1 ||
		ev[0].Kind != EventHP || ev[0].Int != 1 || ev[0].Int2 != 5 || s.HPDef != 5 {
		t.Fatalf("typed HP defense mis-reduced: ev=%+v HPDef=%d", ev, s.HPDef)
	}
	// HP prosecution → bar 2, HPPro.
	if ev := s.HandlePacket(protocol.NewTypedPacket(&aolib.HPToClient{Bar: aolib.PenaltyBarProsecution, Value: 3})); len(ev) != 1 ||
		ev[0].Kind != EventHP || ev[0].Int != 2 || ev[0].Int2 != 3 || s.HPPro != 3 {
		t.Fatalf("typed HP prosecution mis-reduced: ev=%+v HPPro=%d", ev, s.HPPro)
	}

	// CT → OOC event.
	if ev := s.HandlePacket(protocol.NewTypedPacket(&aolib.CTToClient{Name: "Bob", Message: "hi"})); len(ev) != 1 ||
		ev[0].Kind != EventOOC || ev[0].Name != "Bob" || ev[0].Text != "hi" {
		t.Fatalf("typed CT mis-reduced: %+v", ev)
	}

	// JD shown → Judge=1.
	if ev := s.HandlePacket(protocol.NewTypedPacket(&aolib.JD{State: aolib.JudgeStateShown})); len(ev) != 1 ||
		ev[0].Kind != EventJudge || ev[0].Int != JudgeShow || s.Judge != JudgeShow {
		t.Fatalf("typed JD mis-reduced: ev=%+v Judge=%d", ev, s.Judge)
	}

	// ID → PlayerID + Software (no event).
	s.HandlePacket(protocol.NewTypedPacket(&aolib.IDToClient{PlayerID: 42, Software: "tsuserver"}))
	if s.PlayerID != 42 || s.Software != "tsuserver" {
		t.Fatalf("typed ID mis-reduced: PlayerID=%d Software=%q", s.PlayerID, s.Software)
	}

	// PV → EventCharPicked + MyCharID.
	if ev := s.HandlePacket(protocol.NewTypedPacket(&aolib.PV{CharID: 7})); len(ev) != 1 ||
		ev[0].Kind != EventCharPicked || ev[0].Int != 7 || s.MyCharID != 7 {
		t.Fatalf("typed PV mis-reduced: ev=%+v MyCharID=%d", ev, s.MyCharID)
	}

	// ARUP player-count → AreaInfo[0].Players.
	s.AreaInfo = []AreaInfo{{Players: -1}, {Players: -1}}
	if ev := s.HandlePacket(protocol.NewTypedPacket(&aolib.ARUP{UpdateType: aolib.AreaUpdateTypePlayerCount, UpdateData: []string{"5", "7"}})); len(ev) != 1 ||
		ev[0].Kind != EventAreasUpdated || s.AreaInfo[0].Players != 5 || s.AreaInfo[1].Players != 7 {
		t.Fatalf("typed ARUP mis-reduced: ev=%+v AreaInfo=%+v", ev, s.AreaInfo)
	}
}

// TestHandleTypedFallsBackForIncompleteModels pins that headers whose aolib
// typed model drops fields AsyncAO needs (MS, PU type-4, MC NO_REPEAT, SM)
// still reduce through the positional path even when a typed value is present.
func TestHandleTypedFallsBackForIncompleteModels(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")
	s.Chars = []CharacterSlot{{Name: "A"}, {Name: "B"}}

	// MS carries a blipname (slot 30) that aolib's MSToClient drops; the
	// positional ParseMS must still read it from the full field slice.
	fields := make([]string, 31)
	fields[0] = "0"         // desk mod
	fields[2] = "Phoenix"   // char name
	fields[4] = "hello"     // message
	fields[5] = "def"       // side
	fields[8] = "0"         // char id
	fields[11] = "0"        // evidence id
	fields[15] = ""         // showname
	fields[16] = "-1"       // other char id
	fields[30] = "blip.wav" // blipname — aolib MSToClient does not model this
	p := protocol.NewPacket("MS", fields...)
	ev := s.HandlePacket(p)
	if len(ev) != 1 || ev[0].Kind != EventMessage {
		t.Fatalf("positional MS (blipname) not reduced via EventMessage: %+v", ev)
	}
	if ev[0].Message == nil || ev[0].Message.Blipname != "blip.wav" {
		t.Fatalf("positional MS lost blipname: %+v", ev[0].Message)
	}
}
