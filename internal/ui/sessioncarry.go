package ui

// sessionCarry is the one-shot "we are coming back" snapshot of exactly the
// log-content fields a resumed session should see again: the IC/OOC
// scrollback and the per-area log the user was reading when the link
// dropped. Every OTHER sessionState field (conn, sess, room, roster,
// pairing, evidence, voice flags, the ban dashboard...) is connection-scoped
// and must come back fresh on a redial — resetSessionState (tabs.go) still
// wipes those unconditionally and is unchanged by this file.
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
func (a *App) snapshotSessionCarry() {
	a.conn.pendingSessionCarry = &sessionCarry{
		icLog:        a.chatlog.icLog,
		icLogSeq:     a.chatlog.icLogSeq,
		icReadMark:   a.compose.icReadMark,
		oocLog:       a.chatlog.oocLog,
		oocSpeakers:  a.chatlog.oocSpeakers,
		oocSeq:       a.chatlog.oocSeq,
		areaLogs:     a.srvMeta.areaLogs,
		areaLogOrder: a.srvMeta.areaLogOrder,
	}
	a.conn.pendingSessionCarryKey = a.srvMeta.serverKey
}

// applySessionCarryIfPending consumes the pending carry onto the just-dialed
// session if, and only if, wsURL matches the server it was captured from —
// the encapsulation guarantee that a carried log can never resurrect one
// server's content under a different one. ALWAYS clears the slot (one-shot):
func (a *App) applySessionCarryIfPending(wsURL string) {
	carry, key := a.conn.pendingSessionCarry, a.conn.pendingSessionCarryKey
	a.conn.pendingSessionCarry, a.conn.pendingSessionCarryKey = nil, "" // one-shot: consumed either way
	if carry == nil || wsURL == "" || wsURL != key {
		return // no pending carry, or it belongs to a DIFFERENT server: the fresh session's empty log stands
	}
	a.chatlog.icLog = carry.icLog
	a.chatlog.icLogSeq = carry.icLogSeq
	a.compose.icReadMark = carry.icReadMark
	a.chatlog.oocLog = carry.oocLog
	a.chatlog.oocSpeakers = carry.oocSpeakers
	a.chatlog.oocSeq = carry.oocSeq
	a.srvMeta.areaLogs = carry.areaLogs
	a.srvMeta.areaLogOrder = carry.areaLogOrder
}
