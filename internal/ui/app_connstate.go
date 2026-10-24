package ui

import (
	"time"
)

// connState is App's "connection lifecycle" banner group lifted into a named component: the fields
// used to sit flat on App, readable at depth 1 from every ui file. They are
// reached as a.conn.<field> now.
type connState struct {
	// These outlive any single session: the lobby shows the last disconnect
	// reason and a one-click Reconnect, and auto-reconnect retries ACROSS the
	// very resetSessionState() that Disconnect runs. They must live on App, not
	// sessionState — a past refactor swept them into sessionState, so Disconnect
	// wiped them mid-teardown and silently killed the reason text, the Reconnect
	// button and the backoff counter. Keep them here.
	connErr string // last lobby error/notice (disconnect reason, dial failure, …)
	// connErrLine is the drawable one-line view of connErr, memoized against
	// connErrSrc. Memoized because building it clamps runes and so ALLOCATES, and
	// this is read from the lobby's per-frame draw — an allocation there is what the
	// lobby alloc gate exists to catch. See connErrLabel.
	connErrLine  string
	connErrSrc   string
	lastConnName string // M2: the server we were dropped from, for one-click Reconnect
	lastConnURL  string // its ws URL (serverKey); set on connect, re-captured at Disconnect
	// M2 auto-reconnect: after an unexpected drop, retry lastConnURL with backoff.
	// autoReconnectAt is the next attempt (zero = not retrying); pollAutoReconnect
	// fires it from the frame loop (a single time compare when idle — 0 per-frame
	// cost). autoReconnectMsg is the cached lobby status (rebuilt per attempt only).
	autoReconnectAt    time.Time
	autoReconnectTries int
	autoReconnectMsg   string
	// deliberateClose marks the CURRENT teardown as user-initiated (Disconnect
	// button, tab close, quit, rehearsal end) so the closed-channel/SendErr drop
	// paths suppress auto-reconnect. Today Disconnect() nils a.conn synchronously,
	// so the socket-close branch is only reached on genuinely unexpected drops —
	// this flag is defense-in-depth (belt-and-suspenders against a future refactor
	// that pumps between conn.Close() and the nil-out) and the explicit intent
	// signal shouldAutoReconnect reads. Set by the deliberate paths, cleared on a
	// fresh connect.
	deliberateClose bool
	// pendingSessionCarry is the one-shot "we are coming back" snapshot of the
	// log-content fields (sessioncarry.go) captured at a teardown that means
	// exactly that — the disconnect dialog's Reconnect, or an involuntary drop
	// about to auto-retry — and reapplied by connectWith only when the redial
	// lands on the SAME server. Lives here, not in sessionState, for the same
	// reason lastConnURL does two fields up: it must outlive the very
	// resetSessionState() call (Disconnect, then connectWith's own reset) that
	// it exists to protect content from — sessionState is what that call
	// replaces wholesale. pendingSessionCarryKey is the serverKey (dialled
	// address, not display name) it was captured from; nil/"" together mean
	// nothing is pending.
	pendingSessionCarry    *sessionCarry
	pendingSessionCarryKey string
	lobby                  lobbyState
	pbName, pbURL          string // phone-book "add server" form
	// Inline row edit (phone book only): pbEditURL is the identity (stored
	// Favorites URL) of the row currently open for editing — empty = none; the
	// two working buffers hold the in-progress name/address until Save.
	pbEditURL, pbEditName, pbEditAddr string
}
