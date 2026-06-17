package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestBuildLivePlayers pins the PR/PU→row conversion: a UID-keyed row per player,
// area resolved by index, a character-less player shown as a Spectator, IPID
// merged from the snapshot by UID, and an out-of-range area id left blank (no
// panic) rather than indexing past the area list.
func TestBuildLivePlayers(t *testing.T) {
	areas := []string{"Lobby", "Courtroom 1", "Basement"}
	players := []courtroom.LivePlayer{
		{ID: 3, Char: "Phoenix", Showname: "Nick", OOCName: "alex", AreaID: 1},
		{ID: 5, Char: "", Showname: "", OOCName: "lurker", AreaID: 0}, // spectator
		{ID: 8, Char: "Edgeworth", AreaID: 99},                        // area out of range
	}
	ipid := map[string]string{"3": "1A2B"} // only a mod /getarea fills this

	rows := buildLivePlayers(players, areas, ipid)
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if r := rows[0]; r.uid != "3" || r.name != "Phoenix" || r.showname != "Nick" ||
		r.ooc != "alex" || r.ipid != "1A2B" || r.area != "Courtroom 1" {
		t.Fatalf("row0 bad: %+v", r)
	}
	if r := rows[1]; r.name != specName || r.area != "Lobby" || r.ipid != "" || r.uid != "5" {
		t.Fatalf("spectator row bad: %+v", r)
	}
	if r := rows[2]; r.area != "" { // out-of-range AreaID → blank, no panic
		t.Fatalf("row2 area should be blank, got %q", r.area)
	}

	// A nil IPID map is safe (no snapshot pulled yet).
	if rows := buildLivePlayers(players[:1], areas, nil); rows[0].ipid != "" {
		t.Fatalf("nil ipid map should leave ipid blank, got %q", rows[0].ipid)
	}
}
