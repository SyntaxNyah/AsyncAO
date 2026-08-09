package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// The field defect these pin (2026-08-09 debug panel, credit Crystalwarrior for
// the find): the ×N collapse only ever folded CONSECUTIVE duplicates, which two
// interleaved chatty sources defeat completely. The panel showed the KFO packet
// family this client deliberately does not implement (TT / TIN / TF / CHECK)
// alternating with one absent desk overlay re-reported per IC message, so
// neither was ever the immediately previous line: 25 of the ring's rows carried
// two distinct messages and every genuine one-off failure aged out behind them.
//
// The deferred-packet DECISION is untouched — this is presentation only.

// icPacketLine / assetMissLine are the two field classes, verbatim in shape.
const (
	icPacketLine  = `server: unhandled packet "TT" (3 fields)`
	assetMissLine = "Missing asset: local://m-71fad2ce5db0f270/background/botc-townsquare/evening_overlay"
)

// TestInterleavedRepeatsFoldIntoOneRow is the pin: the exact field pattern
// (A B A B A B …) must collapse to one row per distinct message, each carrying
// its own counter.
func TestInterleavedRepeatsFoldIntoOneRow(t *testing.T) {
	a := testTabApp(t)
	now := time.Now()
	const rounds = 12
	for i := 0; i < rounds; i++ {
		now = now.Add(2 * time.Second) // the observed cadence: seconds apart
		a.pushDebugAt(icPacketLine, now)
		now = now.Add(time.Second)
		a.pushDebugAt(assetMissLine, now)
	}
	if len(a.debugLog) != 2 {
		t.Fatalf("interleaved repeats must fold to 2 rows, got %d:\n%s", len(a.debugLog), dumpDebug(a))
	}
	for _, e := range a.debugLog {
		if e.count != rounds {
			t.Errorf("row %q folded %d sightings, want %d", e.raw, e.count, rounds)
		}
		if !strings.HasSuffix(e.text, "×12") {
			t.Errorf("row text must carry the counter, got %q", e.text)
		}
	}
	// Newest-last ordering survives folding (the panel draws the ring in order,
	// and pushDebugAt's early exit depends on it).
	if a.debugLog[0].at.After(a.debugLog[1].at) {
		t.Error("folded rows must stay ordered by latest sighting")
	}
}

// TestFoldedFloodCannotEvictRealFailures is the consequence that matters: a
// one-off failure logged before a long flood is still in the ring afterwards.
// Before the fix, debugLogCap rows of two repeated messages flushed it out.
func TestFoldedFloodCannotEvictRealFailures(t *testing.T) {
	a := testTabApp(t)
	now := time.Now()
	const realFailure = "connect failed: dial tcp: connection refused"
	a.pushDebugAt(realFailure, now)
	for i := 0; i < 4*debugLogCap; i++ {
		now = now.Add(100 * time.Millisecond)
		a.pushDebugAt(icPacketLine, now)
		a.pushDebugAt(assetMissLine, now)
	}
	for _, e := range a.debugLog {
		if e.raw == realFailure {
			return
		}
	}
	t.Fatalf("the flood evicted the real failure:\n%s", dumpDebug(a))
}

// TestRepeatOutsideTheWindowStartsANewRow pins the fold as a WINDOW, not a
// permanent counter: the same failure recurring much later is a new event and
// must read as one, with its own stamp.
func TestRepeatOutsideTheWindowStartsANewRow(t *testing.T) {
	a := testTabApp(t)
	now := time.Now()
	a.pushDebugAt(assetMissLine, now)
	a.pushDebugAt(assetMissLine, now.Add(debugFoldWindow/2)) // inside: folds
	if len(a.debugLog) != 1 || a.debugLog[0].count != 2 {
		t.Fatalf("inside the window must fold:\n%s", dumpDebug(a))
	}
	a.pushDebugAt(assetMissLine, now.Add(debugFoldWindow/2).Add(debugFoldWindow+time.Second))
	if len(a.debugLog) != 2 {
		t.Fatalf("outside the window must start a new row:\n%s", dumpDebug(a))
	}
	if a.debugLog[1].count != 1 {
		t.Errorf("the new row starts at one sighting, got %d", a.debugLog[1].count)
	}
}

// TestDebugRingStaysBounded pins rule 4 across the rewrite: distinct messages
// still fall off at debugLogCap, oldest first.
func TestDebugRingStaysBounded(t *testing.T) {
	a := testTabApp(t)
	now := time.Now()
	for i := 0; i < 3*debugLogCap; i++ {
		now = now.Add(time.Millisecond)
		a.pushDebugAt(distinctLine(i), now)
	}
	if len(a.debugLog) != debugLogCap {
		t.Fatalf("ring holds %d rows, want the %d cap", len(a.debugLog), debugLogCap)
	}
	if a.debugLog[0].raw != distinctLine(3*debugLogCap-debugLogCap) {
		t.Errorf("oldest surviving row is %q, want the cap-th newest", a.debugLog[0].raw)
	}
}

// TestPushDebugStampsAndCounts pins the rendered row's shape — the format the
// panel and the overlay both draw, and the one the field screenshot shows.
func TestPushDebugStampsAndCounts(t *testing.T) {
	a := testTabApp(t)
	at := time.Date(2026, 8, 9, 2, 5, 32, 0, time.Local)
	a.pushDebugAt(icPacketLine, at)
	if want := "02:05:32 " + icPacketLine; a.debugLog[0].text != want {
		t.Fatalf("single sighting = %q, want %q", a.debugLog[0].text, want)
	}
	a.pushDebugAt(icPacketLine, at.Add(time.Second))
	if want := "02:05:33 " + icPacketLine + "  ×2"; a.debugLog[0].text != want {
		t.Fatalf("folded sighting = %q, want %q", a.debugLog[0].text, want)
	}
}

// distinctLine is a unique message per index (never foldable).
func distinctLine(i int) string { return "failure #" + strconv.Itoa(i) }

// dumpDebug renders the ring for a failure message.
func dumpDebug(a *App) string {
	var b strings.Builder
	for _, e := range a.debugLog {
		b.WriteString("  " + e.text + "\n")
	}
	return b.String()
}
