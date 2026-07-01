package ui

import (
	"strconv"
	"testing"
)

// TestPacketLogRecord pins the packet inspector's recorder: in/out totals and
// per-header counts, newest-first recent(), ring-wrap past the cap, the
// distinct-header bound, and reset.
func TestPacketLogRecord(t *testing.T) {
	var pl packetLog
	pl.record("MS", 15, 100, false) // inbound
	pl.record("CT", 3, 20, false)   // inbound
	pl.record("MS", 15, 100, true)  // outbound

	if pl.total != 3 || pl.inTotal != 2 || pl.outTotal != 1 {
		t.Fatalf("totals = %d/%d/%d, want 3/2/1", pl.total, pl.inTotal, pl.outTotal)
	}
	if pl.inCount["MS"] != 1 || pl.inCount["CT"] != 1 || pl.outCount["MS"] != 1 {
		t.Fatalf("counts wrong: in=%v out=%v", pl.inCount, pl.outCount)
	}

	// recent() is newest-first and carries direction.
	got := pl.recent(nil, 2)
	if len(got) != 2 || got[0].hdr != "MS" || !got[0].out || got[1].hdr != "CT" || got[1].out {
		t.Fatalf("recent = %+v, want [MS(out), CT(in)]", got)
	}

	// Ring wrap: overfill; total keeps climbing but the live window caps at pktLogCap.
	var pl2 packetLog
	for i := 0; i < pktLogCap+10; i++ {
		pl2.record("PU", 3, 10, false)
	}
	if pl2.total != pktLogCap+10 {
		t.Fatalf("total = %d, want %d", pl2.total, pktLogCap+10)
	}
	if got := pl2.recent(nil, pktLogCap+50); len(got) != pktLogCap {
		t.Fatalf("recent window = %d, want %d (capped)", len(got), pktLogCap)
	}

	// Distinct-header bound: floods of unique headers bucket into (other).
	var pl3 packetLog
	for i := 0; i < pktHeaderCap+20; i++ {
		pl3.record("H"+strconv.Itoa(i), 1, 5, false)
	}
	if len(pl3.inCount) > pktHeaderCap+1 { // pktHeaderCap distinct + the (other) bucket
		t.Fatalf("distinct-header map unbounded: %d entries", len(pl3.inCount))
	}
	if pl3.inCount[pktOtherHeader] == 0 {
		t.Fatal("overflow headers must bucket into (other)")
	}

	// reset clears counts/totals (ring backing array is kept but masked).
	pl.reset()
	if pl.total != 0 || pl.inTotal != 0 || pl.outTotal != 0 || len(pl.inCount) != 0 || len(pl.recent(nil, 5)) != 0 {
		t.Fatal("reset must clear totals, counts, and the live window")
	}
}
