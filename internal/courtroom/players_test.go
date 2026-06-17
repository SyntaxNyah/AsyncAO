package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestLivePlayersPRPU pins the live player list reduced from the server's
// PlayerStateObserver stream (Akashi/Nyathena): PR adds/removes a UID, PU sets a
// field, the roster is order-robust (a field-first PU still creates the row),
// updates mutate in place, and Players() comes back sorted by UID.
func TestLivePlayersPRPU(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "hdid")

	// A join then its field dump (the per-connect snapshot order).
	if ev := feed(t, s, "PR#7#0#%"); len(ev) != 1 || ev[0].Kind != EventPlayersUpdated {
		t.Fatalf("PR join should emit EventPlayersUpdated, got %+v", ev)
	}
	feed(t, s, "PU#7#0#Alice#%")       // OOC name
	feed(t, s, "PU#7#1#Phoenix#%")     // character folder
	feed(t, s, "PU#7#2#Nick Wright#%") // showname
	feed(t, s, "PU#7#3#3#%")           // area id

	got := s.Players()
	if len(got) != 1 {
		t.Fatalf("want 1 player, got %d", len(got))
	}
	if p := got[0]; p.ID != 7 || p.OOCName != "Alice" || p.Char != "Phoenix" ||
		p.Showname != "Nick Wright" || p.AreaID != 3 {
		t.Fatalf("bad player: %+v", p)
	}

	// A second player added field-first (PU before PR must still register it).
	feed(t, s, "PU#9#1#Edgeworth#%")
	feed(t, s, "PR#9#0#%")
	got = s.Players()
	if len(got) != 2 || got[0].ID != 7 || got[1].ID != 9 { // sorted by UID
		t.Fatalf("want sorted [7,9], got %+v", got)
	}

	// Leave removes only that UID.
	feed(t, s, "PR#7#1#%")
	if got = s.Players(); len(got) != 1 || got[0].ID != 9 {
		t.Fatalf("after 7 leaves want [9], got %+v", got)
	}

	// An area move mutates the existing row in place (no duplicate).
	feed(t, s, "PU#9#3#5#%")
	if got = s.Players(); len(got) != 1 || got[0].AreaID != 5 {
		t.Fatalf("want area 5 after move, got %+v", got)
	}
}
