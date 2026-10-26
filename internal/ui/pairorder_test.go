package ui

// Pair-order reorder rides the server's OOC /pairorder command (JSON group
// reorder). This pins that sendPairOrder emits a CT packet whose body is the
// exact "/pairorder <uid> <op>" the Nyathena server expects.

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

func pairOrderSendApp(t *testing.T) (*App, *[]protocol.Packet) {
	t.Helper()
	a := testTabApp(t)
	var sent []protocol.Packet
	a.sess = courtroom.NewSession(func(p protocol.Packet) error { sent = append(sent, p); return nil }, "")
	a.sess.Chars = []courtroom.CharacterSlot{{Name: "Phoenix"}, {Name: "Maya"}}
	a.sess.MyCharID = 0
	a.sess.PlayerID = 7
	return a, &sent
}

func TestSendPairOrderRidesOOC(t *testing.T) {
	a, sent := pairOrderSendApp(t)

	a.sendPairOrder(7, "front")
	if len(*sent) != 1 {
		t.Fatalf("want 1 packet, got %d", len(*sent))
	}
	if p := (*sent)[0]; p.Header != "CT" || p.Field(1) != "/pairorder 7 front" {
		t.Fatalf("front send = %q %q, want CT \"/pairorder 7 front\"", p.Header, p.Field(1))
	}

	a.sendPairOrder(7, "back")
	a.sendPairOrder(3, "up")
	a.sendPairOrder(3, "down")
	want := []string{"/pairorder 7 front", "/pairorder 7 back", "/pairorder 3 up", "/pairorder 3 down"}
	for i, w := range want {
		if got := (*sent)[i].Field(1); got != w {
			t.Fatalf("packet %d body = %q, want %q", i, got, w)
		}
	}
}
