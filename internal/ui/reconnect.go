package ui

import (
	"context"
	"strconv"
	"time"
)

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

// scheduleAutoReconnect arms the first retry after an unexpected drop, when the
// feature is on and we know which server to redial. Called from the
// EventDisconnect handler (after Disconnect tore the session down).
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
// scheduled, we're sitting in the lobby, and its time has come — so connected or
// idle it costs one comparison. The dial blocks the frame (like the manual
// button); on failure it backs off and reschedules, on success it clears. It
// calls connectWith directly (not Connect) so the backoff counter survives.
func (a *App) pollAutoReconnect() {
	if a.autoReconnectAt.IsZero() || a.screen != ScreenLobby || a.now().Before(a.autoReconnectAt) {
		return
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
