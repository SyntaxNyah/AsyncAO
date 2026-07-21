package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// Involuntary-disconnect dialog.
//
// When a server connection ends WITHOUT the user asking (a wire error, a
// server-side close, a kick/ban), the old flow tore the session down and booted
// straight to the lobby — so the last IC/OOC lines vanished mid-read, the cause
// was buried in the lobby's easy-to-miss connErr line, and getting back on meant
// re-picking the server by hand. This dialog keeps the courtroom on screen —
// FROZEN, the IC/OOC log TAIL still readable around the modal (the whole frozen
// pass is pointer-fenced, so scrolling back through history is not available while
// it's up) — under a modal that says what happened and offers one-click Reconnect /
// Back to lobby.
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
		// The stale-link watchdog gave up, or a write/keepalive timed out: the
		// connection went quiet rather than closing cleanly.
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
// It is THE load-bearing voluntary/involuntary switch — the one place that decides,
// for a connection ending, whether to freeze the courtroom under the dialog or run
// the plain teardown, and whether to arm auto-reconnect:
//
//   - deliberate (a.deliberateClose set by the user paths — Disconnect button, tab
//     close, quit, rehearsal end): NEVER freeze — the user meant to leave, so it's
//     the plain teardown, byte-identical to today. (In practice a deliberate close
//     nils a.conn before the pump runs, so these branches aren't even reached then;
//     the guard is defense-in-depth, matching deliberateClose's own doc.)
//   - an involuntary drop WITH a live courtroom to freeze: beginInvoluntaryDisconnect
//     keeps the room drawable under the dialog.
//   - an involuntary drop with NO room (char-select, already torn down):
//     beginInvoluntaryDisconnect falls back to Disconnect() internally, so we still
//     land on the lobby with connErr — today's exact behavior.
//
// The caller has already set a.connErr (the lobby reason line) and pushed the debug
// line; auto-reconnect is armed here, AFTER the freeze/teardown, exactly as the
// pre-dialog code did (shouldAutoReconnect suppresses ban/kick and deliberate).
//
// willRetry mirrors exactly what scheduleAutoReconnect will decide (computed
// BEFORE the freeze so beginInvoluntaryDisconnect can grant the grace window —
// see initialDropGracePeriod): a drop nothing will auto-heal (kick/ban/pref
// off) has no reason to hide, so it shows immediately.
func (a *App) handleInvoluntaryDrop(reason string) {
	deliberate := a.deliberateClose
	willRetry := !deliberate && shouldAutoReconnect(reason, deliberate) && a.d.Prefs.AutoReconnectOn()
	if !deliberate {
		a.beginInvoluntaryDisconnect(reason, willRetry) // freezes if there's a room, else Disconnect() to the lobby
	} else {
		a.Disconnect() // a deliberate close raced in mid-pump: today's path, no dialog
	}
	if shouldAutoReconnect(reason, deliberate) {
		a.scheduleAutoReconnect() // the teardown just cancelled any pending retry; re-arm for a genuine drop
	}
}

// initialDropGracePeriod is how long a fresh drop stays frozen-but-invisible
// before the dialog actually draws — deliberately generous: it spans many
// retry cycles (2s/4s/8s/16s/30s/30s/... backoff), not just the first, so
// anything short of a real outage self-heals without ever interrupting the
// user. pollAutoReconnect re-arms the SAME deadline on every failed retry
// (never resetting it), so the whole window has to elapse — only a drop that
// outlasts it earns the modal.
const initialDropGracePeriod = 5 * time.Minute

// openDisconnectDialog is the single place the dialog's open state is set, so the
// three entry paths (an ACTIVE-tab drop via beginInvoluntaryDisconnect, a failed
// frozen retry re-surfacing it via pollAutoReconnect, and ACTIVATING a tab that
// died in the background via activateTab) can't diverge in what the modal shows.
// name/url are the redial target captured for Reconnect (see the disconnectDialog
// field doc for why they're frozen here rather than read from lastConn* at click
// time). hiddenUntil is the zero value for immediate display, or a future time to
// grant a grace window (see initialDropGracePeriod) — only the fresh-drop path
// ever passes a non-zero value.
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

// beginInvoluntaryDisconnect freezes the ACTIVE tab's courtroom under the
// disconnect dialog: it performs the NETWORK teardown (close+nil the conn, stop
// music/voice) but KEEPS a.sess/a.room so the last scene and logs stay drawn,
// then opens the dialog (visible immediately, or after willRetry's grace window —
// see initialDropGracePeriod) with a friendly+raw reason. It is the involuntary
// counterpart to Disconnect() — the load-bearing voluntary/involuntary switch is
// which one the drop path calls (deliberateClose gates it; see pumpConnection and
// handleSessionEvents). Called ONLY when the drop was NOT deliberate and there is
// a live room to freeze; the caller has already vetted intent.
func (a *App) beginInvoluntaryDisconnect(raw string, willRetry bool) {
	// No room to freeze (char-select, or already torn down): fall back to the
	// plain teardown so we still land on the lobby with connErr set. Keeps this
	// safe if a drop lands before a courtroom exists.
	if a.room == nil || a.sess == nil || a.screen != ScreenCourtroom {
		a.Disconnect()
		return
	}
	// Re-capture the redial target BEFORE we nil the conn — exactly as Disconnect
	// does (app.go:3177), and for the same reason: lastConn* is GLOBAL and, in a
	// multi-tab session, may hold whatever server connected most recently rather
	// than THIS dropped one. scheduleAutoReconnect (armed by the caller) reads
	// lastConn*, so without this the countdown would redial the wrong server. The
	// dialog's own Reconnect uses the name/url snapshot below, which is likewise
	// this server, not the global.
	if a.conn != nil {
		a.lastConnName, a.lastConnURL = a.serverName, a.serverKey
	}
	// Close any courtroom-owned confirm popup that could be open at freeze time — it
	// would otherwise stack with the disconnect dialog for a frame (both draw at the
	// frame tail in independent blocks — see app.go). confirmDisconnect: the user
	// clicked Disconnect with instant-disconnect off, then the link died before they
	// answered — the involuntary dialog now owns the teardown, the pending answer is
	// moot. hidePrompt: a sprite-hide confirm; clearing it just cancels a moot confirm
	// (hiddenSprites is untouched). We deliberately do NOT clear showQuitConfirm (a
	// GLOBAL quit choice, unrelated to this server) or pendingCloseTab (a DIFFERENT
	// tab's confirm) — those aren't ours to cancel on a drop.
	a.confirmDisconnect = false
	a.hidePrompt = ""
	var hiddenUntil time.Time
	if willRetry {
		hiddenUntil = a.now().Add(initialDropGracePeriod)
	}
	a.openDisconnectDialog(a.serverName, a.serverKey, raw, hiddenUntil)
	// Network teardown — the subset of Disconnect() that must happen the instant
	// the link dies, mirroring its cleanup, but WITHOUT the session reset /
	// tab-close / navigation (those wait for Back to lobby / Reconnect so the room
	// stays drawable). The socket is already dead on the drop paths; closing it is
	// idempotent.
	if a.conn != nil {
		a.conn.Close()
	}
	a.conn = nil // stop the pump: pumpConnection early-returns on a nil conn (the room now receives no events — "frozen")
	if a.d.Audio != nil {
		// The server's area music must not outlive the connection (as Disconnect does).
		a.d.Audio.StopMusic()
		a.musicTabDucked = false
		a.musicAwaitURL = ""
		a.musicAwaitSince = time.Time{}
	}
	a.stopVoiceAudio() // free the voice devices with the connection
	a.voiceJoined, a.voiceMicOn = false, false
	// connErr is still set by the caller (the lobby reason line) so that Back to
	// lobby lands in exactly today's post-disconnect state — the dialog is an
	// addition on top of that path, never a replacement for it.
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
