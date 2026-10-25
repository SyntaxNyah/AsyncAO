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
	if !a.dialogs.autoConnectPending {
		return
	}
	a.dialogs.autoConnectPending = false
	if a.link.sess != nil || a.conn.lastConnURL != "" {
		return // a tab restore (or anything) already started connecting
	}
	if name, url := a.d.Prefs.Load().LastServer(); url != "" {
		a.Connect(name, url)
	}
}

// quickConnect dials the saved last server now (the quick-connect keybind) — to
// hop back on after a disconnect without going through the lobby.
func (a *App) quickConnect() {
	if a.link.sess != nil {
		a.warnLine = "Already connected — Disconnect first."
		a.warnAt = time.Now()
		return
	}
	name, url := a.d.Prefs.Load().LastServer()
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
// server with exponential backoff until it returns or we connect. A deliberate
// Disconnect (the Extras button) never auto-retries, and a manual Reconnect /
// fresh Join takes over (Connect cancels any pending retry). The dial happens on
// the frame loop — the same blocking connect the manual button and restore-on-
// launch use — so there's no off-thread session setup; idle, this is a single
// time compare per frame.

const (
	// autoReconnectBase is the first backoff delay; it doubles each failed
	// attempt up to autoReconnectMax.
	autoReconnectBase = 2 * time.Second
	autoReconnectMax  = 30 * time.Second
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
	if a.conn.lastConnURL == "" || !a.d.Prefs.Load().AutoReconnect {
		return
	}
	a.conn.autoReconnectTries = 0
	a.conn.autoReconnectAt = a.now().Add(autoReconnectDelay(0))
	a.conn.autoReconnectMsg = "Auto-reconnecting to " + a.conn.lastConnName + "…"
}

// cancelAutoReconnect stops any pending retry — a deliberate Disconnect, a manual
// Reconnect, or a fresh Join all take over.
func (a *App) cancelAutoReconnect() {
	a.conn.autoReconnectAt = time.Time{}
	a.conn.autoReconnectTries = 0
	a.conn.autoReconnectMsg = ""
}

// pollAutoReconnect fires a due retry from the frame loop. No-op unless a retry is
// scheduled and its time has come — so connected or idle it costs one comparison.
func (a *App) pollAutoReconnect() {
	if a.conn.autoReconnectAt.IsZero() || a.now().Before(a.conn.autoReconnectAt) || a.screen != ScreenLobby {
		return
	}
	a.conn.autoReconnectTries++
	a.conn.autoReconnectMsg = "Auto-reconnecting to " + a.conn.lastConnName + "… (attempt " +
		strconv.Itoa(a.conn.autoReconnectTries) + ")"
	ctx, cancel := context.WithTimeout(context.Background(), restoreDialTimeout)
	a.connectWith(a.conn.lastConnName, a.conn.lastConnURL, ctx)
	cancel()
	if a.screen != ScreenLobby { // left the lobby = the dial connected; stop retrying
		a.cancelAutoReconnect()
		return
	}
	a.conn.autoReconnectAt = a.now().Add(autoReconnectDelay(a.conn.autoReconnectTries)) // failed: back off
}
