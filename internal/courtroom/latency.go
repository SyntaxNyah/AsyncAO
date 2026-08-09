package courtroom

import "time"

// This file owns ONE concern: how long a packet takes to reach us, and the half
// of that the TI server clocks have to be corrected by.
//
// CANON. Both maintained AO2-family clients apply the same correction, in the
// same place and only to the same packet type:
//
//	../AO2-Client/src/packet_distribution.cpp:573-580   (TI, type 0)
//	  qint64 timer_value = content.at(2).toLongLong();
//	  if (timer_value > 0) { if (type == 0) { timer_value -= latency / 2;
//	                                          w_courtroom->start_clock(id, timer_value); } ...
//	Crystalwarrior/KFO-Client src/packet_distribution.cpp  — byte-identical arm.
//
// and `latency` in both is the CH → CHECK round trip, recorded where the reply
// lands (AO2 packet_distribution.cpp CHECK → `latency = w_courtroom->pong()`,
// courtroom.cpp:6637-6653 ping_server/pong — :6639 is the `ping_timer.start()`
// the reply reads back). The third client in the survey,
// DRO, has no equivalent: its TST/TR/TP clock is a step accumulator
// (../DRO-Client/src/dro/interface/widgets/aotimer.cpp update_time) that drifts
// by design and carries no anchor to correct. Two of three, and the two that
// implement the TI protocol at all, so the consensus is clear.
//
// WHAT THIS IS NOT. It is not an explanation of the "the timer is desynced"
// field report, and it must not be cited as one. The server floors the value to
// whole seconds before it ever leaves (../KFO-Server/server/area.py:797,809
// `int(current_time.total_seconds()) * 1000`), so the anchor a client receives is
// already up to 999 ms coarser than the truth. Half an RTT is ~125 ms even at the
// pessimistic end (the lobby's worst quality tier begins at pingFairMs = 250 ms
// round trip, internal/ui/pingchip.go). Uncorrected, two clients watching the
// same clock therefore flip the displayed second at most ~0.125 s apart — well
// inside the coarseness the server itself introduced, under two frames at 60 Hz,
// and nothing a person reports as a desync.
//
// The three-way diff against AO2-Client, KFO-Client and DRO found NO other
// divergence in the TI path (the table below is that diff), so this file closes
// the one row that differed for CANON PARITY only. No reproducible defect was
// found behind the report.
//
// STATUS: INERT IN PRODUCTION — see "the missing call site" below. Nothing on
// the production path measures a round trip today, so latency stays 0,
// clockLead returns 0, and the TI arm behaves exactly as it did before this
// file existed. TestTimerFlightCorrectionIsInertInProduction pins that.
//
// Everything else in the TI reducer already matches both clients: the anchor is
// "now + ms" recomputed from a deadline rather than accumulated (AOClockLabel::
// set → `target_time = currentDateTime().addMSecs(msecs)`), type 0 re-anchors on
// every resync, type 1 freezes at the value sent, ms <= 0 stops the clock, and
// the display truncates toward zero. Go's monotonic-clock reading makes the
// AsyncAO anchor strictly better than Qt's wall-clock one under an NTP step.
//
// THE MISSING CALL SITE. Canon can time its own keepalive because the client
// that sends CH is the client that reads CHECK, on one event loop
// (courtroom.cpp:6637-6653 ping_server/pong). AsyncAO split those: the CH is
// written by the connection's background keepalive goroutine
// (internal/protocol Conn.keepaliveLoop, fed a pre-built string via
// Conn.SetKeepalive) precisely so it keeps firing while the window is minimized
// and the render loop is stalled, and that goroutine does not — cannot, without
// crossing a thread boundary — tell this Session when the packet left. So the
// two doors below stay shut in production:
//
//   - Session.Ping stamps, but has no production caller. internal/ui installs
//     the keepalive instead (ui/app.go:4187, ui/app.go:4669, ui/tabs.go:540 all
//     call conn.SetKeepalive(sess.KeepalivePacket())).
//   - The "checkconnection" arm stamps, but no server in the maintained fleet
//     sends that header: zero occurrences across KFO-Server, Nyathena, Athena,
//     akashi and Whisker, and AO2-Client names it only to EXCLUDE it from a
//     debug log, inside #ifdef DEBUG_NETWORK (packet_distribution.cpp:40).
//
// Wiring it needs one line in a package this one must not import. Either:
//
//	sess.NoteLatency(rtt)   // render thread, from the #128 ping loop's pingOnce
//	                        // (internal/ui/pingchip.go) — but that loop only
//	                        // exists while the opt-in chip is ON, so it is a
//	                        // partial source, not a replacement for the keepalive
//
// or a notify hook on Conn.keepaliveLoop in the shape of Conn.SetNotify, handed
// to the render thread the way packet arrival already is. Until one lands, item
// 7 is DEFERRED, and this file is instrumentation with its wire cut.

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

// NoteLatency feeds the session an externally measured round trip. This is the
// sanctioned door for the wiring item 7 is deferred on: the caller lives outside
// this package (a WebSocket-level ping, or a keepalive-sent notification), and
// this package must not reach for it. It has NO caller today, so the seam is
// open and unused by design rather than by oversight — the encapsulation test
// TestTimerFlightCorrectionIsInertInProduction states that in the suite so the
// state cannot be mistaken for "working" again. Same plausibility bound as the
// wire path. Game thread only, like the rest of Session.
func (s *Session) NoteLatency(rtt time.Duration) { s.noteLatencySample(rtt) }

// Latency is the last accepted round trip (0 = never measured). Its only
// consumer today is clockLead below — nothing outside this package reads it, and
// in production it always answers 0 (see "the missing call site"). It is
// exported so the connection chip / debug panel can show the session's own
// measurement once one exists, alongside the wiring NoteLatency is waiting for.
func (s *Session) Latency() time.Duration { return s.latency }

// clockLead is the correction subtracted from a TI type-0 value: half the round
// trip, i.e. the one-way flight the value spent getting here. Zero until a round
// trip has been measured, which is precisely canon's behavior before its first
// CHECK (AO2 initialises `latency` to 0).
func (s *Session) clockLead() time.Duration { return s.latency / 2 }
