package ui

// sessionCarry is the one-shot snapshot of the logs a resumed session should see again; every other sessionState
// field is connection-scoped and comes back fresh. This file is the only place that field list may appear.
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

// snapshotSessionCarry copies the log fields into the carry, keyed by a.serverKey (the dialled address, not the name).
// Called only when coming back: the disconnect dialog's Reconnect and an involuntary drop about to retry.
// A second call overwrites; the contents are already capped by icLogCap/oocLineCap.
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

// applySessionCarryIfPending applies the carry only if wsURL matches its server, so one server's log can never appear
// under another, and always clears it. Called after a successful dial, so a failed retry does not use up the carry.
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
