package ui

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// fireAutoConnect dials the last server once on launch; autoConnectPending is armed in NewApp only with no tab restore.
func (a *App) fireAutoConnect() {
	if !a.autoConnectPending {
		return
	}
	a.autoConnectPending = false
	if a.sess != nil || a.lastConnURL != "" {
		return // a tab restore (or anything) already started connecting
	}
	if name, url := a.d.Prefs.Load().LastServer(); url != "" {
		a.Connect(name, url)
	}
}

// quickConnect dials the saved last server now (the quick-connect keybind).
func (a *App) quickConnect() {
	if a.sess != nil {
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

// Auto-reconnect retries the last server with backoff after an unexpected drop, on the frame loop. It never gives up:
// an AFK session has nobody to click Reconnect, and once backed off it costs one dial per autoReconnectMax.

const (
	// autoReconnectBase is the first backoff delay; it doubles up to autoReconnectMax.
	autoReconnectBase = 2 * time.Second
	autoReconnectMax  = 30 * time.Second
)

// autoReconnectDelay is the backoff before attempt `tries`: base·2^tries, capped and guarded against shift overflow.
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

// shouldAutoReconnect is true only for a genuine transport drop. A user-initiated close never reconnects, and neither
// does a kick or ban (matched by the EventDisconnect reason prefixes): reconnecting after a ban reads as ban evasion.
func shouldAutoReconnect(reason string, deliberate bool) bool {
	if deliberate {
		return false
	}
	if strings.HasPrefix(reason, "Banned") || strings.HasPrefix(reason, "Kicked") {
		return false
	}
	return true
}

// scheduleAutoReconnect arms the first retry after a drop; the caller has already vetted intent via
// shouldAutoReconnect.
func (a *App) scheduleAutoReconnect() {
	if a.lastConnURL == "" || !a.d.Prefs.Load().AutoReconnect {
		return
	}
	a.autoReconnectTries = 0
	a.autoReconnectAt = a.now().Add(autoReconnectDelay(0))
	a.autoReconnectMsg = "Auto-reconnecting to " + a.lastConnName + "…"
}

// cancelAutoReconnect stops any pending retry: a Disconnect, a manual Reconnect or a fresh Join takes over.
func (a *App) cancelAutoReconnect() {
	a.autoReconnectAt = time.Time{}
	a.autoReconnectTries = 0
	a.autoReconnectMsg = ""
}

// pollAutoReconnect fires a due retry, blocking the frame like the manual button. It calls connectWith, not Connect,
// so the backoff counter survives. Called only from the foreground Frame loop, and retries only from the lobby, so a
// drop while minimized waits there instead of landing the user at char-select before they restore.
func (a *App) pollAutoReconnect() {
	if a.autoReconnectAt.IsZero() || a.now().Before(a.autoReconnectAt) || a.screen != ScreenLobby {
		return
	}
	a.autoReconnectTries++
	a.autoReconnectMsg = "Auto-reconnecting to " + a.lastConnName + "… (attempt " +
		strconv.Itoa(a.autoReconnectTries) + ")"
	ctx, cancel := context.WithTimeout(context.Background(), restoreDialTimeout)
	a.connectWith(a.lastConnName, a.lastConnURL, ctx)
	cancel()
	if a.screen != ScreenLobby { // left the lobby = the dial connected; stop retrying
		a.cancelAutoReconnect()
		return
	}
	a.autoReconnectAt = a.now().Add(autoReconnectDelay(a.autoReconnectTries)) // failed: back off
}
