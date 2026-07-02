package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSessionFMFARefresh pins the live list-refresh packets (AO2-Client
// packet_distribution "FM"/"FA" — a live server pushed a 2106-field FM per
// area move and we dropped it unhandled): FM replaces the MUSIC list alone,
// verbatim (no SM area/music split); FA replaces the AREA list and resets
// the ARUP table to unknown, emitting EventAreasUpdated so the UI rebuilds.
func TestSessionFMFARefresh(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "hdid")
	feed(t, s, "SM#Lobby#==OST==#trial.opus#%") // login-time lists: 1 area, 2 music entries

	if ev := feed(t, s, "FM#==New OST==#turnabout.opus#suspense.opus#%"); len(ev) != 0 {
		t.Fatalf("FM should refresh silently (music memo self-detects), got %+v", ev)
	}
	if len(s.Music) != 3 || s.Music[0] != "==New OST==" || s.Music[2] != "suspense.opus" {
		t.Errorf("FM music = %v, want the 3 pushed entries verbatim", s.Music)
	}
	if len(s.Areas) != 1 || s.Areas[0] != "Lobby" {
		t.Errorf("FM must not touch areas, got %v", s.Areas)
	}

	ev := feed(t, s, "FA#Courtroom 1#Courtroom 2#Cafeteria#%")
	if len(ev) != 1 || ev[0].Kind != EventAreasUpdated {
		t.Fatalf("FA should emit EventAreasUpdated, got %+v", ev)
	}
	if len(s.Areas) != 3 || s.Areas[0] != "Courtroom 1" || s.Areas[2] != "Cafeteria" {
		t.Errorf("FA areas = %v, want the 3 pushed entries", s.Areas)
	}
	if len(s.AreaInfo) != 3 || s.AreaInfo[0].Players != -1 {
		t.Errorf("FA must reset ARUP to unknown: %+v", s.AreaInfo)
	}
	if len(s.Music) != 3 {
		t.Errorf("FA must not touch music, got %d entries", len(s.Music))
	}
}

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

	// PlayerArea (follow-a-player, M3) reads the same live area; unknown ids !ok.
	if area, ok := s.PlayerArea(9); !ok || area != 5 {
		t.Fatalf("PlayerArea(9) = %d,%v want 5,true", area, ok)
	}
	if _, ok := s.PlayerArea(999); ok {
		t.Error("PlayerArea(unknown) should report !ok")
	}
}
