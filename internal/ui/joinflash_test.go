package ui

import (
	"testing"
	"time"
)

// TestJoinFlash pins the new-joiner highlight bookkeeping (#107): the initial roster
// isn't flashed, a genuine new joiner is stamped, a player who leaves is dropped, and
// a rejoin flashes again.
func TestJoinFlash(t *testing.T) {
	a := &App{}
	now := time.Now()

	a.liveRoster = []areaPlayer{{uid: "1"}, {uid: "2"}}
	a.updateJoinFlash(now)
	if !a.joinFlash["1"].IsZero() || !a.joinFlash["2"].IsZero() {
		t.Error("the initial roster must not flash (timestamps stay zero)")
	}

	a.liveRoster = []areaPlayer{{uid: "1"}, {uid: "2"}, {uid: "3"}}
	a.updateJoinFlash(now.Add(time.Second))
	if a.joinFlash["3"].IsZero() {
		t.Error("a genuine new joiner must be stamped so its row flashes")
	}
	if !a.joinFlash["1"].IsZero() {
		t.Error("an already-present player must not be re-stamped")
	}

	a.liveRoster = []areaPlayer{{uid: "1"}, {uid: "3"}}
	a.updateJoinFlash(now.Add(2 * time.Second))
	if _, ok := a.joinFlash["2"]; ok {
		t.Error("a player who left must be dropped from the flash map")
	}

	a.liveRoster = []areaPlayer{{uid: "1"}, {uid: "2"}, {uid: "3"}}
	a.updateJoinFlash(now.Add(3 * time.Second))
	if a.joinFlash["2"].IsZero() {
		t.Error("a rejoining player must flash again")
	}
}
