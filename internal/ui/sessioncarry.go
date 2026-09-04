package ui

// sessionCarry is the one-shot "we are coming back" snapshot of exactly the
// log-content fields a resumed session should see again: the IC/OOC
// scrollback and the per-area log the user was reading when the link
// dropped. Every OTHER sessionState field (conn, sess, room, roster,
// pairing, evidence, voice flags, the ban dashboard...) is connection-scoped
// and must come back fresh on a redial — resetSessionState (tabs.go) still
// wipes those unconditionally and is unchanged by this file.
//
// This is the ONE place the "what survives a same-server resume" field list
// is allowed to appear: snapshotSessionCarry is the only writer,
// applySessionCarryIfPending the only reader, and both live here so a future
// change to either side can't drift from the other. See
// TestReconnectToADifferentServerNeverCarriesTheLog for the seam this buys —
// nothing outside this file can make a carry apply to a mismatched key.
type sessionCarry struct {
	icLog        []icEntry
	icLogSeq     uint64
	icReadMark   int
	oocLog       []string
	oocSpeakers  []string
	oocSeq       uint64
	areaLogs     map[string][]icEntry
	areaLogOrder []string
}

// snapshotSessionCarry copies the current session's log-content fields into
// the one-shot carry slot, stamped with the server it came from
// (a.serverKey — the dialled address, matching the v1.91.0 per-server-
// identity convention, not the display name).
//
// Called ONLY at the two teardown points that mean "we are coming back": the
// disconnect dialog's Reconnect click (disconnectdialog.go) and an
// involuntary drop about to auto-retry (disconnectdialog.go,
// handleInvoluntaryDrop's plain-teardown branch, gated on
// shouldAutoReconnect). Every deliberate leave — the Disconnect button, Back
// to lobby, a tab close, quit — never calls this, so those keep today's
// clean-slate behavior exactly (TestDeliberateLeavePathsNeverCarryTheLog).
//
// A second call before the first is consumed simply overwrites the slot —
// never accumulates — so this is a single named slot, not an unbounded
// history (rule 4). Its contents are already bounded by icLogCap/oocLineCap
// before the copy (app.go), so carrying adds no new growth path.
func (a *App) snapshotSessionCarry() {
	a.pendingSessionCarry = &sessionCarry{
		icLog:        a.icLog,
		icLogSeq:     a.icLogSeq,
		icReadMark:   a.icReadMark,
		oocLog:       a.oocLog,
		oocSpeakers:  a.oocSpeakers,
		oocSeq:       a.oocSeq,
		areaLogs:     a.areaLogs,
		areaLogOrder: a.areaLogOrder,
	}
	a.pendingSessionCarryKey = a.serverKey
}

// applySessionCarryIfPending consumes the pending carry onto the just-dialed
// session if, and only if, wsURL matches the server it was captured from —
// the encapsulation guarantee that a carried log can never resurrect one
// server's content under a different one. ALWAYS clears the slot (one-shot):
// a mismatch discards the stale carry rather than leaving it to leak onto a
// later same-server redial that never asked for it.
//
// Called from connectWith AFTER a successful dial (a.conn = conn), never
// right after resetSessionState — so a FAILED auto-reconnect attempt never
// burns the one-shot slot; the next retry still has it
// (TestAutoReconnectCarriesLogAcrossAFailedThenSuccessfulRetry).
func (a *App) applySessionCarryIfPending(wsURL string) {
	carry, key := a.pendingSessionCarry, a.pendingSessionCarryKey
	a.pendingSessionCarry, a.pendingSessionCarryKey = nil, "" // one-shot: consumed either way
	if carry == nil || wsURL == "" || wsURL != key {
		return // no pending carry, or it belongs to a DIFFERENT server: the fresh session's empty log stands
	}
	a.icLog = carry.icLog
	a.icLogSeq = carry.icLogSeq
	a.icReadMark = carry.icReadMark
	a.oocLog = carry.oocLog
	a.oocSpeakers = carry.oocSpeakers
	a.oocSeq = carry.oocSeq
	a.areaLogs = carry.areaLogs
	a.areaLogOrder = carry.areaLogOrder
}
