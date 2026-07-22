package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// Involuntary-disconnect dialog (BACKGROUND/parked tabs only).
//
// An ACTIVE tab's connection ending without the user asking goes straight to the
// lobby with the reason shown and auto-reconnect armed there (the v1.70.0 behaviour
// restored by user request — see handleInvoluntaryDrop). This dialog now serves
// only a DIFFERENT case: a PARKED tab that died in the background can't boot the
// user off whatever tab they're actually looking at, so it latches its reason and
// surfaces this modal when the user later reactivates that tab (see tabs.go,
// activateTab). It keeps that tab's courtroom on screen — FROZEN, the IC/OOC log
// TAIL still readable around the modal (the whole frozen pass is pointer-fenced, so
// scrolling back through history is not available while it's up) — under a modal
// that says what happened and offers one-click Reconnect / Back to lobby.
//
// "Frozen" means precisely: the network session is torn down (conn closed and
// nilled, music/voice stopped) so no more packets arrive, but a.sess and a.room
// are DELIBERATELY kept alive so drawCourtroom keeps rendering the last scene and
// the IC/OOC logs stay drawn. The pointer fence (like every confirm modal) makes
// the COURTROOM PASS behind the dialog click-proof, and handleHotkeys short-circuits
// while the dialog is up, so no keyboard shout or IC send reaches the dead socket.
// (The non-blocking floating panels — Extras / Pair / the pinned second-server
// client — draw AFTER the courtroom's fence lifts, so they stay live; a send there
// into a closed conn is harmless, just recorded in sendErr, and the pinned client is
// a DIFFERENT server that must keep working.) handleTabBar is also inert while the
// dialog is up, so a chip click can't park the frozen-but-not-dead session into a
// zombie tab (tabs.go). Back to lobby then runs the real Disconnect() (today's exact
// post-disconnect path); Reconnect tears the frozen session down and redials — the
// same server + name, so the once-per-join auto-login machinery takes over on the
// fresh join.
//
// This is one more member of the confirm-modal family (drawDisconnectConfirm /
// drawCloseTabConfirm / drawQuitConfirm): same dimmed backdrop, same fence
// discipline (fenced with the family in app.go's frame tail, pointer restored
// just before it draws), same Esc rung via closeTopOverlay. Copy that shape,
// don't invent one.

// disconnectDialog is the modal's state. The zero value is closed/inert (open ==
// false), so an App with no drop showing costs a single bool check per frame.
// It lives on sessionState (see the field doc in app.go for why it's per-tab, not
// on App): it parks/restores with its own tab, so an active-tab drop shows THIS
// dialog directly. A tab that drops while BACKGROUNDED can't pop a modal then (it
// would cover whatever tab the user is looking at), so it arms its own deadReason
// instead and surfaces this same dialog on reactivation (see tabs.go, activateTab).
// Nothing relies on it surviving resetSessionState — the close paths
// (closeDisconnectDialogToLobby / reconnectFromDisconnectDialog) snapshot name/url
// and clear the dialog BEFORE any teardown/reset runs.
type disconnectDialog struct {
	open   bool
	reason disconnectReason
	// name/url are the server we dropped from, captured at freeze time so
	// Reconnect redials exactly it even though the teardown that runs first would
	// otherwise re-point lastConn* (they're global). Mirrors Disconnect's
	// lastConn* re-capture rationale.
	name string
	url  string
	// hiddenUntil defers actually DRAWING the modal (open stays true the whole
	// time — the pointer-fence and pollAutoReconnect's frozen-retry trigger are
	// both keyed on open, not visibility) until this time; the zero value shows
	// immediately. A fresh drop gets a grace window here so a blip that heals
	// itself on the very first retry never interrupts the user at all — only a
	// drop that survives that attempt earns the modal.
	hiddenUntil time.Time
}

// disconnectReason is what the dialog tells the user: an optional friendly line
// (empty when the cause is unknown) and ALWAYS the raw close code / error
// underneath, so a server restart and a local failure never look identical and
// no information is hidden behind a guess.
type disconnectReason struct {
	friendly string // "" when we can't name the cause — only the raw line shows then
	raw      string
}

// friendlyDisconnectReason maps a raw close reason to a disconnectReason. The
// friendly line is set only for the obviously-known cases; everything else keeps
// friendly == "" and shows the raw string alone. Extensible by design: a future
// known cause is one more case here, one line, no call-site changes — the raw
// reason strings are exactly what the drop paths already build (pumpConnection's
// "connection lost: …" / conn.Err(), the EventDisconnect "Kicked: …" / "Banned: …"
// prefixes, and dial-failure errors). We deliberately do NOT guess at server
// families or parse close codes we don't recognise: an unknown reason is honestly
// shown raw rather than mislabelled.
func friendlyDisconnectReason(raw string) disconnectReason {
	r := disconnectReason{raw: raw}
	low := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(raw, "Kicked"):
		// KK/KB surface as "Kicked: <reason>" from EventDisconnect — the server
		// removed us on purpose; auto-reconnect stays off (shouldAutoReconnect).
		r.friendly = "The server kicked you."
	case strings.HasPrefix(raw, "Banned"):
		// BD surfaces as "Banned: <reason>" — same source, worse optics; never
		// auto-reconnects (ban evasion).
		r.friendly = "The server banned you."
	case strings.Contains(low, "network is unreachable") || strings.Contains(low, "no route"):
		// The local link is down — distinct from the server going away, so the
		// user knows to check their own connection.
		r.friendly = "Your network is unreachable — check your internet connection."
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline exceeded") ||
		strings.Contains(low, "stale") || strings.Contains(low, "i/o timeout"):
		// A write/keepalive timed out: the connection went quiet rather than
		// closing cleanly.
		r.friendly = "The connection timed out — the server stopped responding."
	case strings.Contains(low, "connection closed") || strings.Contains(low, "eof") ||
		strings.Contains(low, "connection lost") || strings.Contains(low, "closed") ||
		strings.Contains(low, "reset by peer") || strings.Contains(low, "going away"):
		// The socket closed under us — most often a server restart or a clean
		// server-side close. Phrased as a likely cause, never as certainty.
		r.friendly = "The server closed the connection (it may have restarted)."
	}
	return r
}

// handleInvoluntaryDrop is the SHARED tail every active-tab drop path runs once its
// reason is known: pumpConnection's SendErr (half-dead write) and closed-Incoming
// (transport drop) branches, and handleSessionEvents' EventDisconnect (kick/ban).
// It always runs the plain teardown to the lobby with the reason shown, then arms
// auto-reconnect for a genuine transport drop (shouldAutoReconnect suppresses
// ban/kick and deliberate closes). The involuntary-disconnect dialog is no longer
// reached from here — it now serves only a background/parked tab's death, surfaced
// on reactivation via activateTab.
func (a *App) handleInvoluntaryDrop(reason string) {
	deliberate := a.deliberateClose
	// v1.70.0 behaviour (restored by user request): a drop tears down to the LOBBY
	// with the reason shown, then arms auto-reconnect for a genuine transport drop.
	// No frozen-courtroom dialog and no background reconnect — the v1.80.0 rework
	// that added those is what made a drop-while-minimized silently reconnect and
	// dump the user at char-select before they even restored the window. Now a drop
	// returns to the phone book (reason + "Auto-reconnecting…" countdown visible),
	// and pollAutoReconnect fires only from the foreground Frame loop, so a drop
	// taken while minimized waits at the lobby until the user comes back rather than
	// churning reconnects in the background. A kick/ban (shouldAutoReconnect false)
	// lands on the lobby with its reason and never retries.
	a.Disconnect()     // → lobby; nils conn/sess and cancels any pending retry
	a.connErr = reason // App-level, survives Disconnect — the lobby's reason line
	if shouldAutoReconnect(reason, deliberate) {
		a.scheduleAutoReconnect() // re-arm the countdown the teardown just cancelled
	}
}

// openDisconnectDialog is the single place the dialog's open state is set, so the
// two entry paths (ACTIVATING a tab that died in the background via activateTab,
// and a failed retry re-surfacing it) can't diverge in what the modal shows. It
// is now reached only for a background/parked tab's death — an active-tab drop
// goes straight to the lobby (see handleInvoluntaryDrop). name/url are the redial
// target captured for Reconnect (see the disconnectDialog field doc for why
// they're frozen here rather than read from lastConn* at click time). hiddenUntil
// is the zero value for immediate display.
func (a *App) openDisconnectDialog(name, url, raw string, hiddenUntil time.Time) {
	a.disconnectDlg = disconnectDialog{
		open:        true,
		reason:      friendlyDisconnectReason(raw),
		name:        name,
		url:         url,
		hiddenUntil: hiddenUntil,
	}
	if hiddenUntil.IsZero() {
		// Only flash for a drop we're actually SHOWING right now — a hidden
		// grace-period drop that's about to self-heal shouldn't flash the
		// taskbar for nothing. The exact FlashWindow idiom modcalls and
		// callwords already use (ui.go FlashWindow); FLASH_UNTIL_FOCUSED keeps
		// the taskbar icon flashing until the user actually returns to the
		// window, however long that takes.
		a.ctx.FlashWindow()
	}
}

// closeDisconnectDialogToLobby is the dialog's Back to lobby: it runs the real
// Disconnect() so we land in EXACTLY today's post-disconnect state (session
// reset, tab closed, screen == lobby, connErr showing the reason). The freeze
// already nilled a.conn, so Disconnect's conn-guarded steps simply skip; the
// deliberate/involuntary auto-reconnect decision was already made on the drop
// path, so this teardown must not re-arm — mark it deliberate. Clears the dialog
// LAST so its fence is released this frame (the emoji-picker freeze class).
func (a *App) closeDisconnectDialogToLobby() {
	a.disconnectDlg = disconnectDialog{}
	a.deliberateClose = true // Back to lobby is a user choice — this teardown must not auto-reconnect
	a.cancelAutoReconnect()  // and the user opted out of any pending countdown
	a.Disconnect()
}

// reconnectFromDisconnectDialog is the dialog's Reconnect: tear the frozen
// session fully down (deliberate, so the teardown itself doesn't auto-retry) and
// redial the SAME server + name — the exact lobby-Reconnect path (Connect), so
// the normal join flow (fresh courtroom entry, once-per-join auto-login) takes
// over. Clears the dialog first so its fence releases; a failed dial lands on the
// lobby with connErr, just like the lobby button.
func (a *App) reconnectFromDisconnectDialog() {
	name, url := a.disconnectDlg.name, a.disconnectDlg.url
	a.disconnectDlg = disconnectDialog{}
	a.deliberateClose = true // reconnecting on purpose — the frozen-tab teardown must not auto-retry
	a.Disconnect()           // full teardown of the frozen session → lobby
	if url != "" {
		a.Connect(name, url) // Connect cancels any pending auto-retry and redials
	}
}

// drawDisconnectDialog paints the involuntary-disconnect modal over the frozen
// courtroom: dimmed backdrop, the friendly line (when known) + the raw reason
// underneath, an optional auto-reconnect countdown, and Reconnect / Back to lobby
// buttons. Drawn at the frame tail with its family (pointer restored just before,
// fenced with them) so clicks can't reach the frozen scene behind. Off the render
// hot path — only while open.
func (a *App) drawDisconnectDialog(w, h int32) {
	c := a.ctx
	if !a.disconnectDlg.open {
		return // defensive: the outer guard only draws this while open
	}
	if a.now().Before(a.disconnectDlg.hiddenUntil) {
		return // still within the grace window — a quick self-heal must stay invisible
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 520, 220
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Disconnected from the server", ColText)

	// The friendly line (when we could name the cause) in the normal text colour,
	// then the raw reason ALWAYS below it, dimmer — a server restart and a local
	// failure can't look identical, and no cause is hidden behind a guess. Both are
	// server-supplied / error-derived and variable length, so clip to the panel
	// (the §3.4 spill-past-the-border class of bug).
	y := m.Y + 48
	if fr := a.disconnectDlg.reason.friendly; fr != "" {
		c.LabelClipped(m.X+pad, y, mw-2*pad, fr, ColText)
		y += 24
	}
	raw := a.disconnectDlg.reason.raw
	if raw == "" {
		raw = "connection ended"
	}
	c.LabelClipped(m.X+pad, y, mw-2*pad, raw, ColTextDim)

	// Auto-reconnect status: if a retry is armed and counting down (the pref is on
	// and this was a genuine drop), show it — the buttons still work, and a
	// successful auto-reconnect closes the dialog on its own (pollAutoReconnect
	// clears disconnectDlg on success; see reconnect.go). Reads the EXISTING
	// autoReconnect* state, no new timer.
	if !a.autoReconnectAt.IsZero() {
		secs := int(a.autoReconnectAt.Sub(a.now()).Round(time.Second) / time.Second)
		if secs < 0 {
			secs = 0
		}
		c.Label(m.X+pad, m.Y+mh-btnH-pad-26, "Reconnecting automatically in "+strconv.Itoa(secs)+"s…", ColAccent)
	}

	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 150, H: btnH}, "Reconnect") {
		a.reconnectFromDisconnectDialog()
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 150, Y: m.Y + mh - btnH - pad, W: 150, H: btnH}, "Back to lobby") {
		a.closeDisconnectDialogToLobby()
	}
}
