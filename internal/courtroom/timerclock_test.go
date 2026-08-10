package courtroom

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The three-client TI survey these tests pin (citations in latency.go):
//
//	                    AO2-Client        KFO-Client        DRO-Client
//	anchor              now + ms          now + ms          step accumulator
//	resync (type 0)     re-anchors        re-anchors        n/a (TST/TR/TP)
//	pause  (type 1)     freeze at ms      freeze at ms      firing timer stops
//	ms <= 0             stop, show zero   stop, show zero   n/a
//	flight correction   -latency/2        -latency/2        none
//	render rounding     truncate          truncate          truncate
//
// AsyncAO already matched every row but the flight correction, so that row is
// the whole of what this file adds — for CANON PARITY, not as the explanation of
// any field report. The server floors the value it sends
// (../KFO-Server/server/area.py:797 `int(current_time.total_seconds()) * 1000`),
// so two clients differ by at most half an RTT (~0.1 s at the fleet's worst)
// inside a value already coarse to the second. That cannot be a visible desync,
// and the diff found nothing else that could be.
//
// The correction is also INERT in production: nothing measures a round trip
// today. Every test below except TestTimerFlightCorrectionIsInertInProduction
// reaches the reducer through Session.Ping, a door production does not use.
// latency.go, "the missing call site", has the hand-over.

// newTimerSession returns a Session with nothing but the wire attached: these
// tests drive TI/CHECK packets only.
func newTimerSession(t *testing.T) *Session {
	t.Helper()
	rec := &sentRecorder{}
	return NewSession(rec.send, "timer-hdid")
}

// measureRTT walks the CH → CHECK exchange with a controlled gap, so the session
// ends up holding a known latency without a clock seam: the stamp is taken when
// the session sends CH and closed when CHECK arrives, both on this goroutine.
func measureRTT(t *testing.T, s *Session, rtt time.Duration) {
	t.Helper()
	s.Ping()
	s.pingSentAt = s.pingSentAt.Add(-rtt) // backdate the stamp: the reply is "rtt late"
	feed(t, s, "CHECK#%")
	if got := s.Latency(); got < rtt || got > rtt+time.Second {
		t.Fatalf("measured latency = %v, want ≈ %v", got, rtt)
	}
}

// TestTimerStartSubtractsHalfTheMeasuredRoundTrip pins canon's correction and
// its scope: AO2 and KFO apply `timer_value -= latency / 2` to TI type 0 and to
// nothing else.
func TestTimerStartSubtractsHalfTheMeasuredRoundTrip(t *testing.T) {
	const (
		rtt      = 400 * time.Millisecond
		serverMs = 60000
	)
	s := newTimerSession(t)
	measureRTT(t, s, rtt)

	before := time.Now()
	feed(t, s, fmt.Sprintf("TI#0#0#%d#%%", serverMs))
	after := time.Now()

	// The reducer subtracts half the latency the session MEASURED, and that
	// measurement is necessarily a hair ABOVE the nominal rtt: measureRTT backdates
	// the stamp and then closes the round trip on this goroutine, so everything
	// between the two — the packet parse, an allocation, a scheduler preemption —
	// lands inside the sample (~0.9 µs on an idle machine, ~15-23 µs on the first,
	// cold exchange, unbounded under load). Anchoring want to the NOMINAL rtt made
	// the lower bound demand a deadline half that overhead later than the reducer
	// can possibly mint, and the suite failed by exactly that half.
	//
	// Half of the value the reducer itself used makes the window exact by
	// construction — no tolerance to guess, and none that could ever be right,
	// since the overhead is a scheduling delay with no bound. It keeps every tooth:
	// dropping the correction lands the deadline rtt/2 (200 ms) late and applying
	// the whole latency lands it rtt/2 early, both hundreds of milliseconds outside
	// a window this narrow.
	want := serverMs*time.Millisecond - s.Latency()/2
	got := s.Timers[0].Deadline
	if got.Before(before.Add(want)) || got.After(after.Add(want)) {
		t.Errorf("deadline is %v out from the corrected anchor (want now+%v)", got.Sub(before.Add(want)), want)
	}
	// The uncorrected client reads half an RTT high — that is the whole defect.
	if rem := s.Timers[0].Remaining(after); rem > serverMs*time.Millisecond-rtt/2 {
		t.Errorf("remaining %v is not below the uncorrected %v", rem, serverMs*time.Millisecond)
	}
}

// TestPausedTimerIsNotFlightCorrected pins the scope. Canon pauses at the value
// sent verbatim (pause_clock then set_clock with the raw timer_value): a paused
// clock is a DISPLAY value, not an anchor, so shaving flight time off it would
// make every pause read low.
func TestPausedTimerIsNotFlightCorrected(t *testing.T) {
	s := newTimerSession(t)
	measureRTT(t, s, 400*time.Millisecond)

	feed(t, s, "TI#1#1#90000#%")
	if s.Timers[1].Running {
		t.Error("type 1 must not leave the clock running")
	}
	if got := s.Timers[1].Left; got != 90*time.Second {
		t.Errorf("paused value = %v, want the 90s sent verbatim", got)
	}
}

// TestUnmeasuredLatencyLeavesTheAnchorAlone pins the pre-measurement behavior,
// which is also canon's (AO2 starts with latency == 0): no ping yet means no
// correction, never a guess.
func TestUnmeasuredLatencyLeavesTheAnchorAlone(t *testing.T) {
	s := newTimerSession(t)
	if s.Latency() != 0 {
		t.Fatalf("fresh session latency = %v, want 0", s.Latency())
	}
	before := time.Now()
	feed(t, s, "TI#0#0#30000#%")
	if d := s.Timers[0].Deadline.Sub(before); d < 29*time.Second || d > 31*time.Second {
		t.Errorf("uncorrected anchor = now+%v, want ≈ now+30s", d)
	}
}

// TestAbsurdRoundTripIsNotAMeasurement pins the one DELIBERATE divergence from
// canon. Our CH stamp is taken on the game thread and the CHECK is read on a
// later drained frame, so a stall (minimize, sleep/resume, a breakpoint) can
// manufacture a multi-minute "round trip"; halving that would zero every clock
// the server subsequently sets. Canon has no such stall because Qt reads the
// socket on its own event loop.
func TestAbsurdRoundTripIsNotAMeasurement(t *testing.T) {
	s := newTimerSession(t)
	measureRTT(t, s, 200*time.Millisecond)
	sane := s.Latency()

	s.Ping()
	s.pingSentAt = s.pingSentAt.Add(-(maxLatencySample + time.Second))
	feed(t, s, "CHECK#%")
	if got := s.Latency(); got != sane {
		t.Errorf("latency after a stalled round trip = %v, want the last sane %v", got, sane)
	}

	s.NoteLatency(maxLatencySample + time.Second)
	if got := s.Latency(); got != sane {
		t.Errorf("NoteLatency accepted an implausible sample: %v", got)
	}
	s.NoteLatency(-time.Second)
	if got := s.Latency(); got != sane {
		t.Errorf("NoteLatency accepted a negative sample: %v", got)
	}
}

// TestCheckWithoutAnOutstandingPingIsIgnored mirrors AO2's pong(): with
// is_pinging false it returns −1 and latency is left alone, so a server
// heartbeating CHECK on its own schedule can never invent a measurement out of
// the time since some unrelated earlier ping.
func TestCheckWithoutAnOutstandingPingIsIgnored(t *testing.T) {
	s := newTimerSession(t)
	feed(t, s, "CHECK#%")
	if s.Latency() != 0 {
		t.Errorf("unsolicited CHECK produced a latency of %v", s.Latency())
	}

	measureRTT(t, s, 150*time.Millisecond)
	sane := s.Latency()
	feed(t, s, "CHECK#%") // duplicate reply, no stamp outstanding
	if got := s.Latency(); got != sane {
		t.Errorf("duplicate CHECK re-timed the round trip: %v (want %v)", got, sane)
	}
}

// TestServerClockDoesNotDrift is the fake-clock drift pin. AsyncAO derives the
// display from an ANCHOR (deadline − now) exactly like AOClockLabel's
// target_time, rather than accumulating a per-tick decrement the way DRO's step
// timer does, so a jittery, irregular, hours-long frame cadence must not move the
// reading by even a nanosecond.
//
// It also pins the render rounding both AO2 clients use: truncation toward zero
// (QTime::addMSecs on the whole remainder), which is what the courtroom overlay's
// int(seconds) does — never rounding, which would show one second too many for
// half of every second.
func TestServerClockDoesNotDrift(t *testing.T) {
	s := newTimerSession(t)
	const total = 2 * time.Hour
	before := time.Now()
	feed(t, s, fmt.Sprintf("TI#2#0#%d#%%", total.Milliseconds()))
	anchor := s.Timers[2].Deadline
	if d := anchor.Sub(before); d < total-time.Second || d > total+time.Second {
		t.Fatalf("anchor = now+%v, want ≈ now+%v", d, total)
	}

	// A deliberately ugly cadence: 16.6667 ms frames with a prime-number jitter,
	// stepped across the whole two hours. An accumulating clock would be seconds
	// out by the end; an anchored one is exact by construction, and this is the
	// property the test exists to keep true.
	var now time.Time = before
	for i := 0; i < 60000; i++ {
		now = now.Add(16*time.Millisecond + time.Duration(667+(i%7)*13)*time.Microsecond)
		want := anchor.Sub(now)
		if want < 0 {
			want = 0
		}
		if got := s.Timers[2].Remaining(now); got != want {
			t.Fatalf("tick %d: Remaining = %v, want %v (drift %v)", i, got, want, got-want)
		}
	}
	if now.Before(before.Add(16 * time.Minute)) {
		t.Fatalf("the drift walk only covered %v — too short to be evidence", now.Sub(before))
	}

	// Truncation, not rounding: 59.999 s left displays as 59, never 60.
	almost := anchor.Add(-59999 * time.Millisecond)
	if got := int(s.Timers[2].Remaining(almost).Seconds()); got != 59 {
		t.Errorf("59.999s left renders as %d, want 59 (canon truncates)", got)
	}
}

// TestTimerResyncReAnchors pins the resync row: every type 0 re-anchors from the
// value just received (AOClockLabel::start → set → target_time = now + msecs),
// so a server correcting a drifting clock actually corrects it, and a resume
// after a pause starts from the resumed value rather than the frozen one.
func TestTimerResyncReAnchors(t *testing.T) {
	s := newTimerSession(t)
	feed(t, s, "TI#3#0#300000#%")
	first := s.Timers[3].Deadline

	feed(t, s, "TI#3#1#120000#%") // pause at 2 minutes
	if s.Timers[3].Running || s.Timers[3].Left != 2*time.Minute {
		t.Fatalf("pause: running=%v left=%v", s.Timers[3].Running, s.Timers[3].Left)
	}

	feed(t, s, "TI#3#0#120000#%") // resume at the paused value
	if !s.Timers[3].Running {
		t.Fatal("type 0 must resume the clock")
	}
	if !s.Timers[3].Deadline.Before(first) {
		t.Error("resume did not re-anchor: the deadline still reflects the original 5 minutes")
	}
	if rem := s.Timers[3].Remaining(time.Now()); rem > 2*time.Minute || rem < 2*time.Minute-time.Second {
		t.Errorf("resumed remaining = %v, want ≈ 2m", rem)
	}
}

// TestTimerStopsOnANonPositiveValue pins the ms <= 0 arm for BOTH types, which
// canon routes to the same stop_clock (packet_distribution.cpp:587-590) — a
// stopped clock reads zero, it does not keep the previous remainder.
func TestTimerStopsOnANonPositiveValue(t *testing.T) {
	for _, typ := range []string{"0", "1"} {
		s := newTimerSession(t)
		feed(t, s, "TI#4#0#45000#%")
		feed(t, s, "TI#4#"+typ+"#0#%")
		if s.Timers[4].Running || s.Timers[4].Left != 0 {
			t.Errorf("type %s with ms=0: running=%v left=%v, want stopped at zero", typ, s.Timers[4].Running, s.Timers[4].Left)
		}
		if got := s.Timers[4].Remaining(time.Now()); got != 0 {
			t.Errorf("type %s with ms=0: Remaining = %v, want 0", typ, got)
		}
	}
}

// TestTimerIDsPastTheClockCapAreIgnored pins the bound canon shares
// (`id >= 0 && id < max_clocks`) against a server that offers more: KFO-Server's
// /timer accepts ids up to 20 (../KFO-Server/server/commands/roleplay.py:783)
// and sends TI for every one of them.
func TestTimerIDsPastTheClockCapAreIgnored(t *testing.T) {
	s := newTimerSession(t)
	for _, id := range []int{-1, TimerCount, TimerCount + 15} {
		if ev := feed(t, s, fmt.Sprintf("TI#%d#0#30000#%%", id)); len(ev) != 0 {
			t.Errorf("TI for out-of-range clock %d produced %d events", id, len(ev))
		}
	}
	for i := range s.Timers {
		if s.Timers[i].Running {
			t.Errorf("clock %d started from an out-of-range packet", i)
		}
	}
}

// TestTimerFlightCorrectionIsInertInProduction is the honest label on the flight
// correction, enforced by the suite instead of by a comment. Every other test in
// this file reaches the reducer through Session.Ping — a door the app does not
// use. This one drives the session the way the app actually does: the keepalive
// is BUILT here and written by the connection's own goroutine
// (Session.KeepalivePacket → Conn.SetKeepalive), so nothing stamps a send time,
// and the server's CHECK replies (../KFO-Server/server/network/aoprotocol.py:248
// net_cmd_ch answers every CH) arrive with no round trip outstanding.
//
// It is a TRIPWIRE, not a wish. The day internal/protocol or internal/ui calls
// Session.NoteLatency (latency.go, "the missing call site"), this test fails,
// and whoever lands that wiring deletes it and re-labels the item CLOSED.
// Deleting it WITHOUT landing the wiring puts back the claim that this client
// corrects for flight time when it does not.
func TestTimerFlightCorrectionIsInertInProduction(t *testing.T) {
	s := newTimerSession(t)
	s.MyCharID = 7

	// Production's keepalive path in full: one immutable string, handed off.
	if pkt := s.KeepalivePacket(); !strings.HasPrefix(pkt, "CH#") {
		t.Fatalf("precondition: production sends %q, which is not the CH keepalive", pkt)
	}
	// Assert the CAUSE, not only the symptom. Building the production keepalive
	// leaves no round trip open, so notePong has nothing to close. Checking this
	// separately matters because the symptom alone can hide the wiring: Go's
	// monotonic clock on Windows advances in ~0.5 ms ticks, so a stamp taken and
	// closed inside one tick measures exactly 0 and noteLatencySample discards it
	// as implausible. A real wiring's RTT is milliseconds and would show up in
	// Latency() below — but a same-tick unit-test exchange would not, and this
	// tripwire must not depend on which of those it is looking at.
	if !s.pingSentAt.IsZero() {
		t.Fatalf("the production keepalive stamped a send time — a latency source IS wired now; " +
			"see the Latency() failure below for what to do")
	}
	// The server answers every one of them. Several, so a "the first reply is
	// free" bug could not hide behind a single exchange.
	const keepaliveRoundTrips = 3
	for i := 0; i < keepaliveRoundTrips; i++ {
		feed(t, s, "CHECK#%")
	}

	if got := s.Latency(); got != 0 {
		t.Fatalf("latency = %v after %d production keepalive round trips — a latency source IS wired now, "+
			"so delete this tripwire, drop the INERT IN PRODUCTION notes in latency.go/session.go, and "+
			"re-label the item CLOSED", got, keepaliveRoundTrips)
	}
	if got := s.clockLead(); got != 0 {
		t.Fatalf("clockLead = %v with nothing measured", got)
	}

	// And therefore the TI anchor is the raw server value, exactly as it was
	// before latency.go existed.
	const serverMs = 60000
	before := time.Now()
	feed(t, s, fmt.Sprintf("TI#0#0#%d#%%", serverMs))
	after := time.Now()
	want := serverMs * time.Millisecond
	if got := s.Timers[0].Deadline; got.Before(before.Add(want)) || got.After(after.Add(want)) {
		t.Errorf("the anchor moved %v off the raw server value; with no measurement it must be uncorrected",
			got.Sub(before.Add(want)))
	}
}

// latencyFieldOwner is the ONE non-test file allowed to assign the timing
// fields. maxLatencySample is a DELIBERATE divergence from canon (canon clamps
// nothing), and it is worth exactly as much as the guarantee that every write
// goes through the clamp. Both sanctioned doors — the CHECK arm and NoteLatency
// — funnel into noteLatencySample, which lives here.
const latencyFieldOwner = "latency.go"

// latencyGuardedFields are the Session fields noteLatencySample owns.
var latencyGuardedFields = []string{"latency", "pingSentAt"}

// TestLatencyHasExactlyOneWriter is the encapsulation test on that seam. Go's
// package scope is the bypass: any file in internal/courtroom can assign
// s.latency directly and skip the clamp, and no compiler will object. This
// census does. (Test files are exempt — measureRTT backdates pingSentAt on
// purpose, which is how a round trip is simulated without a clock seam.)
func TestLatencyHasExactlyOneWriter(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	var offenders []string
	ownerWrites := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range as.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || !isGuardedLatencyField(sel.Sel.Name) {
					continue
				}
				if name == latencyFieldOwner {
					ownerWrites++
					continue
				}
				offenders = append(offenders, fmt.Sprintf("%s:%d assigns .%s", name,
					fset.Position(sel.Pos()).Line, sel.Sel.Name))
			}
			return true
		})
	}

	if len(offenders) > 0 {
		t.Errorf("the latency clamp is bypassed:\n  %s\n\nEvery write to %v goes through "+
			"noteLatencySample in %s, which rejects anything that is not plausibly a network "+
			"measurement (maxLatencySample). Call NoteLatency, or the CHECK arm.",
			strings.Join(offenders, "\n  "), latencyGuardedFields, latencyFieldOwner)
	}
	// Deletion catcher: a census that finds nothing because the code it guards
	// moved out from under it is not a census.
	if ownerWrites == 0 {
		t.Errorf("no writes to %v found in %s — the seam moved; re-point this census at its new home",
			latencyGuardedFields, latencyFieldOwner)
	}
}

func isGuardedLatencyField(name string) bool {
	for _, f := range latencyGuardedFields {
		if f == name {
			return true
		}
	}
	return false
}
