package courtroom

import "time"

// Latency stays 0 in production and clockLead returns 0: Conn.keepaliveLoop writes the CH off this thread
// (so it fires while minimized), and nothing stamps it for us.
// Half an RTT matters little: the server truncates TI values to seconds (KFO-Server area.py:797,809).
// Canon: courtroom.cpp:6637-6653 ping_server/pong, with ping_timer.start() at :6639.

const (
	// maxLatencySample diverges from canon, which clamps nothing: a stall, sleep or debugger can fake a long RTT,
	// and half of that would zero every server clock.
	maxLatencySample = 2 * time.Second
)

// noteLatencySample folds one CH to CHECK round trip into the latency, ignoring implausible samples.
func (s *Session) noteLatencySample(rtt time.Duration) {
	if rtt <= 0 || rtt > maxLatencySample {
		return
	}
	s.latency = rtt
}

// stampPing records a CH leaving on the game thread; a CHECK with no outstanding stamp is ignored, not mistimed.
func (s *Session) stampPing() { s.pingSentAt = time.Now() }

// notePong closes a round trip like AO2's pong(): with no stamp, an unsolicited CHECK never invents a measurement.
func (s *Session) notePong() {
	if s.pingSentAt.IsZero() {
		return
	}
	s.noteLatencySample(time.Since(s.pingSentAt))
	s.pingSentAt = time.Time{}
}

// clockLead is half the round trip, subtracted from a TI type-0 value; zero until measured, as in AO2.
func (s *Session) clockLead() time.Duration { return s.latency / 2 }
