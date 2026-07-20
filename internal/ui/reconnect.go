package ui

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// fireAutoConnect dials the last server once on launch when auto-connect-on-launch
// is set (one-shot via autoConnectPending, armed in NewApp only when no tab
// restore is queued). No-op when not pending — a single bool check per frame, so
// boot stays byte-identical when off.
func (a *App) fireAutoConnect() {
	if !a.autoConnectPending {
		return
	}
	a.autoConnectPending = false
	if a.sess != nil || a.lastConnURL != "" {
		return // a tab restore (or anything) already started connecting
	}
	if name, url := a.d.Prefs.LastServer(); url != "" {
		a.Connect(name, url)
	}
}

// quickConnect dials the saved last server now (the quick-connect keybind) — to
// hop back on after a disconnect without going through the lobby.
func (a *App) quickConnect() {
	if a.sess != nil {
		a.warnLine = "Already connected — Disconnect first."
		a.warnAt = time.Now()
		return
	}
	name, url := a.d.Prefs.LastServer()
	if url == "" {
		a.warnLine = "No saved server yet — connect to one first."
		a.warnAt = time.Now()
		return
	}
	a.warnLine = "Connecting to " + name + "…"
	a.warnAt = time.Now()
	a.Connect(name, url)
}

// Auto-reconnect (M2): after an UNEXPECTED drop (EventDisconnect), retry the last
// server with exponential backoff until it returns, we connect, or we give up. A
// deliberate Disconnect (the Extras button) never auto-retries, and a manual
// Reconnect / fresh Join takes over (Connect cancels any pending retry). The dial
// happens on the frame loop — the same blocking connect the manual button and
// restore-on-launch use — so there's no off-thread session setup; idle, this is a
// single time compare per frame.

const (
	// autoReconnectBase is the first backoff delay; it doubles each failed
	// attempt up to autoReconnectMax.
	autoReconnectBase = 2 * time.Second
	autoReconnectMax  = 30 * time.Second
	// autoReconnectMaxTries bounds the attempts before giving up (the manual
	// Reconnect button still works after).
	autoReconnectMaxTries = 8
)

// autoReconnectDelay is the backoff before attempt `tries`: base·2^tries, capped
// at the max (and guarded against shift overflow). Pure.
func autoReconnectDelay(tries int) time.Duration {
	if tries < 0 {
		tries = 0
	}
	if tries >= 16 { // 2s<<16 already dwarfs the cap; avoid a huge/negative shift
		return autoReconnectMax
	}
	if d := autoReconnectBase << uint(tries); d < autoReconnectMax {
		return d
	}
	return autoReconnectMax
}

// shouldAutoReconnect decides whether an ended connection should auto-retry. It
// is the single source of truth for the "drop vs ban/kick vs user-close"
// distinction (#1), kept pure so it unit-tests directly:
//
//   - deliberate: a user-initiated close (Disconnect button, tab close, quit,
//     rehearsal end) never reconnects — the user meant to leave.
//   - a server KICK or BAN never reconnects: reconnecting after a ban reads as
//     ban evasion, and a kick is the server removing you on purpose — both are
//     bad optics. Matched by the EventDisconnect reason prefixes ("Banned: " /
//     "Kicked: ", session.go:697-701).
//   - anything else is a genuine transport drop (Wi-Fi blip, server restart,
//     read error, stale-link watchdog) — the exact case auto-reconnect exists
//     for — so retry.
func shouldAutoReconnect(reason string, deliberate bool) bool {
	if deliberate {
		return false
	}
	if strings.HasPrefix(reason, "Banned") || strings.HasPrefix(reason, "Kicked") {
		return false
	}
	return true
}

// scheduleAutoReconnect arms the first retry after an unexpected drop, when the
// feature is on and we know which server to redial. Called from the
// closed-channel / SendErr drop paths (after Disconnect tore the session down);
// the caller has already vetted intent via shouldAutoReconnect.
func (a *App) scheduleAutoReconnect() {
	if a.lastConnURL == "" || !a.d.Prefs.AutoReconnectOn() {
		return
	}
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(autoReconnectDelay(0))
	a.autoReconnectMsg = "Auto-reconnecting to " + a.lastConnName + "…"
}

// cancelAutoReconnect stops any pending retry — a deliberate Disconnect, a manual
// Reconnect, or a fresh Join all take over.
func (a *App) cancelAutoReconnect() {
	a.autoReconnectAt = time.Time{}
	a.autoReconnectTries = 0
	a.autoReconnectMsg = ""
}

// pollAutoReconnect fires a due retry from the frame loop. No-op unless a retry is
// scheduled and its time has come — so connected or idle it costs one comparison.
// The dial blocks the frame (like the manual button); on failure it backs off and
// reschedules, on success it clears. It calls connectWith directly (not Connect) so
// the backoff counter survives.
//
// Two states can host a due retry:
//   - the lobby (a drop that fell through to the plain teardown, or an earlier
//     failed frozen attempt that collapsed to the lobby-with-countdown), and
//   - a FROZEN courtroom under the involuntary-disconnect dialog: the retry must
//     still fire there, or the countdown the dialog shows would tick to zero and do
//     nothing. connectWith's parkActive→allocateTab→resetSessionState would strand
//     a zombie tab over the connless frozen session, so the frozen fire first tears
//     the frozen session fully down (exactly as the manual Reconnect button does)
//     and clears the dialog, THEN redials — collapsing to the lobby if the dial
//     fails (consistent with a manual Reconnect that fails: lobby + countdown).
//
// Any OTHER screen (settings, a live second tab) suppresses the retry, as before.
func (a *App) pollAutoReconnect() {
	frozen := a.disconnectDlg.open
	if a.autoReconnectAt.IsZero() || a.now().Before(a.autoReconnectAt) ||
		(a.screen != ScreenLobby && !frozen) {
		return
	}
	if frozen {
		// Tear the frozen session down (deliberate, so the teardown itself doesn't
		// re-arm) and clear the dialog, but PRESERVE the backoff counter across it —
		// Disconnect→cancelAutoReconnect zeroes autoReconnectTries, and this is a
		// continuation of the same retry sequence, not a fresh one. connErr is left
		// as the drop set it; connectWith clears it on the redial attempt.
		tries := a.autoReconnectTries
		a.disconnectDlg = disconnectDialog{}
		a.deliberateClose = true // the frozen-tab teardown is intentional here
		a.Disconnect()           // → lobby; nils conn/sess, removes the frozen tab
		a.autoReconnectTries = tries
	}
	a.autoReconnectTries++
	if a.autoReconnectTries > autoReconnectMaxTries {
		a.cancelAutoReconnect()
		a.connErr = "Auto-reconnect gave up after " + strconv.Itoa(autoReconnectMaxTries) +
			" tries — click Reconnect to keep trying."
		return
	}
	a.autoReconnectMsg = "Auto-reconnecting to " + a.lastConnName + "… (attempt " +
		strconv.Itoa(a.autoReconnectTries) + "/" + strconv.Itoa(autoReconnectMaxTries) + ")"
	ctx, cancel := context.WithTimeout(context.Background(), restoreDialTimeout)
	a.connectWith(a.lastConnName, a.lastConnURL, ctx)
	cancel()
	if a.screen != ScreenLobby { // left the lobby = the dial connected; stop retrying
		a.cancelAutoReconnect()
		return
	}
	a.autoReconnectAt = a.now().Add(autoReconnectDelay(a.autoReconnectTries)) // failed: back off
}
