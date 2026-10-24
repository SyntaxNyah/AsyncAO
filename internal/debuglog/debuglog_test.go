package debuglog

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
	var s Sink
	now := time.Now()
	const rounds = 12
	for i := 0; i < rounds; i++ {
		now = now.Add(2 * time.Second) // the observed cadence: seconds apart
		s.PushAt(icPacketLine, now)
		now = now.Add(time.Second)
		s.PushAt(assetMissLine, now)
	}
	if s.Len() != 2 {
		t.Fatalf("interleaved repeats must fold to 2 rows, got %d:\n%s", s.Len(), dump(&s))
	}
	for _, e := range s.Lines() {
		if e.Count != rounds {
			t.Errorf("row %q folded %d sightings, want %d", e.Raw, e.Count, rounds)
		}
		if !strings.HasSuffix(e.Text, "×12") {
			t.Errorf("row text must carry the counter, got %q", e.Text)
		}
	}
	// Newest-last ordering survives folding (the panel draws the ring in order,
	// and the early exit in PushAt depends on it).
	if s.Lines()[0].At.After(s.Lines()[1].At) {
		t.Error("folded rows must stay ordered by latest sighting")
	}
}

// TestFoldedFloodCannotEvictRealFailures is the consequence that matters: a
// one-off failure logged before a long flood is still in the ring afterwards.
// Before the fix, Cap rows of two repeated messages flushed it out.
func TestFoldedFloodCannotEvictRealFailures(t *testing.T) {
	var s Sink
	now := time.Now()
	const realFailure = "connect failed: dial tcp: connection refused"
	s.PushAt(realFailure, now)
	for i := 0; i < 4*Cap; i++ {
		now = now.Add(100 * time.Millisecond)
		s.PushAt(icPacketLine, now)
		s.PushAt(assetMissLine, now)
	}
	for _, e := range s.Lines() {
		if e.Raw == realFailure {
			return
		}
	}
	t.Fatalf("the flood evicted the real failure:\n%s", dump(&s))
}

// TestRepeatOutsideTheWindowStartsANewRow pins the fold as a WINDOW, not a
// permanent counter: the same failure recurring much later is a new event and
// must read as one, with its own stamp.
func TestRepeatOutsideTheWindowStartsANewRow(t *testing.T) {
	var s Sink
	now := time.Now()
	s.PushAt(assetMissLine, now)
	s.PushAt(assetMissLine, now.Add(FoldWindow/2)) // inside: folds
	if s.Len() != 1 || s.Lines()[0].Count != 2 {
		t.Fatalf("inside the window must fold:\n%s", dump(&s))
	}
	s.PushAt(assetMissLine, now.Add(FoldWindow/2).Add(FoldWindow+time.Second))
	if s.Len() != 2 {
		t.Fatalf("outside the window must start a new row:\n%s", dump(&s))
	}
	if s.Lines()[1].Count != 1 {
		t.Errorf("the new row starts at one sighting, got %d", s.Lines()[1].Count)
	}
}

// TestDebugRingStaysBounded pins rule 4 across the rewrite: distinct messages
// still fall off at Cap, oldest first.
func TestDebugRingStaysBounded(t *testing.T) {
	var s Sink
	now := time.Now()
	for i := 0; i < 3*Cap; i++ {
		now = now.Add(time.Millisecond)
		s.PushAt(distinctLine(i), now)
	}
	if s.Len() != Cap {
		t.Fatalf("ring holds %d rows, want the %d cap", s.Len(), Cap)
	}
	if s.Lines()[0].Raw != distinctLine(3*Cap-Cap) {
		t.Errorf("oldest surviving row is %q, want the cap-th newest", s.Lines()[0].Raw)
	}
}

// TestPushStampsAndCounts pins the rendered row's shape — the format the panel
// and the overlay both draw, and the one the field screenshot shows.
func TestPushStampsAndCounts(t *testing.T) {
	var s Sink
	at := time.Date(2026, 8, 9, 2, 5, 32, 0, time.Local)
	s.PushAt(icPacketLine, at)
	if want := "02:05:32 " + icPacketLine; s.Lines()[0].Text != want {
		t.Fatalf("single sighting = %q, want %q", s.Lines()[0].Text, want)
	}
	s.PushAt(icPacketLine, at.Add(time.Second))
	if want := "02:05:33 " + icPacketLine + "  ×2"; s.Lines()[0].Text != want {
		t.Fatalf("folded sighting = %q, want %q", s.Lines()[0].Text, want)
	}
}

// distinctLine is a unique message per index (never foldable).
func distinctLine(i int) string { return "failure #" + strconv.Itoa(i) }

// dump renders the ring for a failure message.
func dump(s *Sink) string {
	var b strings.Builder
	for _, e := range s.Lines() {
		b.WriteString("  " + e.Text + "\n")
	}
	return b.String()
}
