package courtroom

import "time"

// PR NOTE: latency seam inert — the stamp source (Session.Ping / the checkconnection
// arm) is unreachable in production, so latency stays 0 and clockLead returns 0.
// Kept as canon-parity instrumentation; see 0e68a06.

// This file owns ONE concern: how long a packet takes to reach us, and the half
// of that the TI server clocks have to be corrected by.
// courtroom.cpp:6637-6653 ping_server/pong — :6639 is the `ping_timer.start()`
// the reply reads back). The third client in the survey,
// DRO, has no equivalent: its TST/TR/TP clock is a step accumulator
// (../DRO-Client/src/dro/interface/widgets/aotimer.cpp update_time) that drifts
// by design and carries no anchor to correct. Two of three, and the two that
// implement the TI protocol at all, so the consensus is clear.
// whole seconds before it ever leaves (../KFO-Server/server/area.py:797,809
// `int(current_time.total_seconds()) * 1000`), so the anchor a client receives is
// already up to 999 ms coarser than the truth. Half an RTT is ~125 ms even at the
// pessimistic end (the lobby's worst quality tier begins at pingFairMs = 250 ms
// round trip, internal/ui/pingchip.go). Uncorrected, two clients watching the
// same clock therefore flip the displayed second at most ~0.125 s apart — well
// inside the coarseness the server itself introduced, under two frames at 60 Hz,
// and nothing a person reports as a desync.
// THE MISSING CALL SITE. Canon can time its own keepalive because the client
// that sends CH is the client that reads CHECK, on one event loop
// (courtroom.cpp:6637-6653 ping_server/pong). AsyncAO split those: the CH is
// written by the connection's background keepalive goroutine
// (internal/protocol Conn.keepaliveLoop, fed a pre-built string via
// Conn.SetKeepalive) precisely so it keeps firing while the window is minimized
// and the render loop is stalled, and that goroutine does not — cannot, without
// crossing a thread boundary — tell this Session when the packet left. So the
// two doors below stay shut in production:
// Wiring it needs one line in a package this one must not import. Either:

const (
	// maxLatencySample rejects an absurd CH → CHECK round trip as a measurement.
	// A DELIBERATE divergence from canon, which clamps nothing: our CH can be
	// stamped on the game thread and the CHECK read on the next drained frame, so
	// a stalled/minimized client, a laptop resuming from sleep, or a debugger
	// breakpoint can manufacture a "round trip" of minutes. Subtracting half of
	// that would zero every clock the server then sets. Two seconds is far above
	// any real AO server RTT (the lobby's own "poor" ping tier is a small
	// fraction of it) and far below anything a stall produces, so a real
	// measurement always lands and a fabricated one never does.
	maxLatencySample = 2 * time.Second
)

// noteLatencySample folds one CH → CHECK round trip into the session's latency,
// ignoring a sample that is not plausibly a network measurement (see
// maxLatencySample). Unexported: the wire path below and NoteLatency are the
// only two doors.
func (s *Session) noteLatencySample(rtt time.Duration) {
	if rtt <= 0 || rtt > maxLatencySample {
		return
	}
	s.latency = rtt
}

// stampPing records that a CH keepalive just left on the game thread, so the
// CHECK that answers it can be timed. Called from every place this Session
// itself sends a CH — which today is Session.Ping and the "checkconnection"
// arm, NEITHER of which production reaches (see "the missing call site"). The
// connection's background keepalive goroutine sends the CH that production
// actually puts on the wire, off this thread and without telling us, so an
// unanswered stamp simply expires: the next CHECK with no stamp outstanding is
// ignored, not mistimed.
func (s *Session) stampPing() { s.pingSentAt = time.Now() }

// notePong closes the round trip a stampPing opened. Mirrors AO2's pong():
// `is_pinging` false → -1 → no update, so an unsolicited CHECK (the server
// heartbeating on its own) never invents a measurement.
func (s *Session) notePong() {
	if s.pingSentAt.IsZero() {
		return
	}
	s.noteLatencySample(time.Since(s.pingSentAt))
	s.pingSentAt = time.Time{}
}

// clockLead is the correction subtracted from a TI type-0 value: half the round
// trip, i.e. the one-way flight the value spent getting here. Zero until a round
// trip has been measured, which is precisely canon's behavior before its first
// CHECK (AO2 initialises `latency` to 0).
func (s *Session) clockLead() time.Duration { return s.latency / 2 }
