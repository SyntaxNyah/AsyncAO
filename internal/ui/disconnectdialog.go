package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Involuntary-disconnect dialog. Both drop arms reach it through freezeSessionUnderDialog. An active tab whose
// link dies freezes in place; a parked tab that died in the background keeps its reason and surfaces the same
// modal when reactivated (tabs.go, activateTab), so it can't boot the user off the tab they're viewing.
// The tab's courtroom stays on screen frozen, with the IC/OOC log tail readable (the frozen pass is pointer-fenced,
// so scrolling history is unavailable), under a modal offering Reconnect or Back to lobby.
// Frozen means the network session is torn down (conn closed and nilled, music/voice stopped) but a.sess and
// a.room are kept so drawCourtroom keeps rendering the last scene. The pointer fence makes the courtroom pass
// click-proof and handleHotkeys short-circuits while the dialog is up. The non-blocking floating panels (Extras,
// Pair, the pinned second-server client) draw after the fence lifts and stay live; a send into the closed conn
// is harmless (recorded in sendErr), and the pinned client is a different server. handleTabBar is inert while the
// dialog is up so a chip click can't park the session into a zombie tab (tabs.go). Back to lobby runs the real
// Disconnect(); Reconnect tears the frozen session down and reconnects to the same server and name.
// It follows the confirm-modal family (drawDisconnectConfirm / drawCloseTabConfirm / drawQuitConfirm): dimmed
// backdrop, fenced in app.go's frame tail with the pointer restored just before it draws, Esc via closeTopOverlay.

// disconnectDialog is the modal's state. The zero value is closed, so an idle App pays one bool check per frame.
// It lives on sessionState (see the field doc in app.go) and parks with its tab. A tab that drops while
// backgrounded can't pop a modal over another tab, so it arms its own deadReason and surfaces this dialog on
// reactivation (tabs.go, activateTab). The close paths (closeDisconnectDialogToLobby /
// reconnectFromDisconnectDialog) snapshot name/url and clear the dialog before any teardown.
type disconnectDialog struct {
	open   bool
	reason disconnectReason
	// name/url are the server we dropped from, captured at freeze time so Reconnect reconnects to exactly it; the teardown
	// would otherwise re-point the global lastConn*.
	name string
	url  string
	// wrap memoizes the laid-out raw reason. A kick/ban reason is the server's multi-line prose, and an access code
	// or
	// appeal link may sit on a later line (see paraWrapCache, qol.go).
	wrap paraWrapCache
	// hiddenUntil defers drawing the modal until this time (open stays true; the pointer fence and
	// pollAutoReconnect's
	// frozen-retry trigger key on open). The zero value shows it immediately.
	hiddenUntil time.Time
}

// disconnectReason is what the dialog tells the user: an optional friendly line (empty when the cause is unknown)
// and always the raw close code or error, so a server restart and a local failure never look identical.
type disconnectReason struct {
	friendly string // "" when we can't name the cause — only the raw line shows then
	raw      string
}

// friendlyDisconnectReason maps a raw close reason to a disconnectReason. The friendly line is set only for known
// cases; otherwise it stays "" and the raw string shows alone. The raw strings are what the drop paths build
// (pumpConnection's "connection lost: …" / conn.Err(), EventDisconnect's "Kicked: …" / "Banned: …", dial errors).
// Unrecognised reasons are shown raw rather than guessed at.
func friendlyDisconnectReason(raw string) disconnectReason {
	r := disconnectReason{raw: raw}
	low := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(raw, "Kicked"):
		// KK/KB surface as "Kicked: <reason>" from EventDisconnect; auto-reconnect stays off (shouldAutoReconnect).
		r.friendly = "The server kicked you."
	case strings.HasPrefix(raw, "Banned"):
		// BD surfaces as "Banned: <reason>"; never auto-reconnects.
		r.friendly = "The server banned you."
	case strings.Contains(low, "network is unreachable") || strings.Contains(low, "no route"):
		// The local link is down, distinct from the server going away.
		r.friendly = "Your network is unreachable — check your internet connection."
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline exceeded") ||
		strings.Contains(low, "stale") || strings.Contains(low, "i/o timeout"):
		// A write/keepalive timed out: the connection went quiet rather than closing cleanly.
		r.friendly = "The connection timed out — the server stopped responding."
	case strings.Contains(low, "connection closed") || strings.Contains(low, "eof") ||
		strings.Contains(low, "connection lost") || strings.Contains(low, "closed") ||
		strings.Contains(low, "reset by peer") || strings.Contains(low, "going away"):
		// The socket closed under us, often a server restart or clean close. Phrased as a likely cause.
		r.friendly = "The server closed the connection (it may have restarted)."
	}
	return r
}

// serverRemovalMessage pulls the server's own words out of a drop reason, the only thing the notice box is for.
// EventDisconnect builds "Kicked: " / "Banned: " plus the raw wire field (internal/courtroom/session.go, KK/KB/BD).
// Anything else (transport drop, timeout, our dial error) is our own prose, already said by the lobby line and
// friendly sentence, and must not raise a modal. A removal with no reason returns false too.
func serverRemovalMessage(raw string) (string, bool) {
	rest, cut := strings.CutPrefix(raw, "Kicked: ")
	if !cut {
		rest, cut = strings.CutPrefix(raw, "Banned: ")
	}
	if !cut {
		return "", false
	}
	// Trailing newlines are common in server reasons and would waste a body row; interior newlines stay.
	if rest = strings.TrimSpace(rest); rest == "" {
		return "", false
	}
	return rest, true
}

// serverNoticeRemovedFallbackTitle heads a removal we could not name. It is unreachable today
// (friendlyDisconnectReason
// names both prefixes serverRemovalMessage matches) and keeps the box from drawing a blank heading.
const serverNoticeRemovedFallbackTitle = "The server closed your connection"

// freezeSessionUnderDialog is the one freeze both drop arms use. It performs the network and audio part of
// Disconnect() that must happen the instant the link dies, and leaves the session reset, tab close and navigation
// for Back to lobby / Reconnect so the room stays drawable. Its guards make it a no-op for what a parked tab
// already did (its socket was nilled), so both arms call it unconditionally.
func (a *App) freezeSessionUnderDialog(raw string) {
	// Re-capture the reconnect target before nilling the conn, as Disconnect does: lastConn* is global and in a
	// multi-tab session may hold whichever server connected most recently.
	if a.conn != nil {
		a.lastConnName, a.lastConnURL = a.serverName, a.serverKey
	}
	// Close any courtroom-owned confirm open at freeze time so it doesn't stack with this modal. confirmDisconnect
	// and
	// hidePrompt are moot after the drop. Not cleared: showQuitConfirm (a global choice) and pendingCloseTab (another
	// tab's confirm).
	a.confirmDisconnect = false
	a.hidePrompt = ""
	if a.conn != nil {
		a.conn.Close() // idempotent: the socket is already dead on every drop path
	}
	a.conn = nil // pumpConnection early-returns on a nil conn, so the room freezes
	if a.d.Audio != nil {
		// The server's area music must not outlive the connection (as Disconnect does).
		a.d.Audio.StopMusic()
		a.musicTabDucked = false
		a.musicAwaitURL = ""
		a.musicAwaitSince = time.Time{}
	}
	a.stopVoiceAudio() // free the voice devices with the connection
	a.voiceJoined, a.voiceMicOn = false, false
	// hiddenUntil is the zero value: the dialog is always visible immediately, so a drop never self-heals silently.
	a.openDisconnectDialog(a.serverName, a.serverKey, raw, time.Time{})
}

// handleInvoluntaryDrop is the shared tail every active-tab drop path runs once its reason is known:
// pumpConnection's SendErr and closed-Incoming branches, and handleSessionEvents' EventDisconnect (kick/ban).
func (a *App) handleInvoluntaryDrop(reason string) {
	deliberate := a.deliberateClose
	a.connErr = reason // set FIRST: it must survive whichever branch runs below
	// A live courtroom freezes under the dialog instead of being torn down, the same end state a parked tab reaches
	// via activateTab. No grace window (see freezeSessionUnderDialog), and no auto-reconnect is armed here: its
	// countdown could never fire because pollAutoReconnect returns early off the lobby, and a background retry would
	// dump users at char-select while minimized. The dialog's Reconnect button is the action.
	if !deliberate && a.room != nil && a.sess != nil && a.screen == ScreenCourtroom {
		a.freezeSessionUnderDialog(reason)
		return
	}
	// No room to freeze (char-select, already torn down) or a deliberate close: plain teardown to the lobby, which
	// still arms auto-reconnect for a genuine transport drop (shouldAutoReconnect suppresses ban/kick).
	// A server that refuses during the handshake (locked down, IP whitelist) sends its instructions as the BD reason
	// and closes with a.room == nil, so there is no frozen courtroom to sit on. The lobby's single clipped label
	// could not show a later line, where a lockdown puts the access code. Snapshot before the teardown:
	// serverName is on sessionState and resetSessionState rebuilds it, so reading it after Disconnect names nobody.
	// The two surfaces are exclusive: the branch above returns, so a drop either freezes under the disconnect dialog
	// or lands here and raises the notice box. The a.disconnectDlg.open implies screen == Courtroom invariant holds.
	noticeBody, showNotice := "", false
	if !deliberate {
		noticeBody, showNotice = serverRemovalMessage(reason)
	}
	noticeTitle, noticeServer := friendlyDisconnectReason(reason).friendly, a.serverName
	if noticeTitle == "" {
		noticeTitle = serverNoticeRemovedFallbackTitle
	}
	// A genuine transport drop about to auto-retry is "coming back": snapshot today's log before Disconnect wipes it
	// so
	// a successful retry can restore it (applySessionCarryIfPending, same server). Same condition as the re-arm
	// below,
	// so a kick/ban/deliberate close starts clean.
	if shouldAutoReconnect(reason, deliberate) {
		a.snapshotSessionCarry()
	}
	a.Disconnect() // → lobby; nils conn/sess and cancels any pending retry
	if showNotice {
		// Open the notice after the teardown, so no teardown step runs with a modal already fenced over the screen.
		// The
		// box is App-level and survives resetSessionState.
		a.openServerNotice(serverNoticeRemoved, noticeTitle, noticeServer, noticeBody)
	}
	if shouldAutoReconnect(reason, deliberate) {
		a.scheduleAutoReconnect() // re-arm the countdown the teardown just cancelled
	}
}

// openDisconnectDialog is the single place the dialog's open state is set, so both entry paths (activating a tab
// that died in the background, and an active-tab drop through the shared freeze) show the same modal. name/url
// are the reconnect target captured for Reconnect (see the disconnectDialog field doc). hiddenUntil is the zero value
// for immediate display.
func (a *App) openDisconnectDialog(name, url, raw string, hiddenUntil time.Time) {
	// A BB popup notice can be on screen when the link dies, and two blocking modals would stack. Only an info notice
	// is dismissed: a serverNoticeRemoved box is the only copy of the appeal instructions. (It can't be open here,
	// since a removal takes the other branch of handleInvoluntaryDrop.)
	if a.serverNoticeDlg.open && a.serverNoticeDlg.kind == serverNoticeInfo {
		a.closeServerNotice()
	}
	a.disconnectDlg = disconnectDialog{
		open:        true,
		reason:      friendlyDisconnectReason(raw),
		name:        name,
		url:         url,
		hiddenUntil: hiddenUntil,
	}
	if hiddenUntil.IsZero() {
		// Flash only for a drop we're showing now, using the same FlashWindow idiom as modcalls and callwords;
		// FLASH_UNTIL_FOCUSED keeps the taskbar icon flashing until the user returns.
		a.ctx.FlashWindow()
	}
}

// closeDisconnectDialogToLobby is Back to lobby: it runs the real Disconnect() (session reset, tab closed, lobby,
// connErr showing the reason). The freeze already nilled a.conn, so Disconnect's conn-guarded steps skip. The
// auto-reconnect decision was made on the drop path, so mark this teardown deliberate to avoid re-arming. The
// dialog is cleared last so its fence releases this frame.
func (a *App) closeDisconnectDialogToLobby() {
	a.disconnectDlg = disconnectDialog{}
	a.deliberateClose = true // Back to lobby is a user choice — this teardown must not auto-reconnect
	a.cancelAutoReconnect()  // and the user opted out of any pending countdown
	a.Disconnect()
}

// reconnectFromDisconnectDialog is Reconnect: tear the frozen session down (deliberate, so the teardown doesn't
// auto-retry) and reconnect the same server and name through Connect, so the normal join flow takes over. The dialog
// is cleared first so its fence releases; a failed dial lands on the lobby with connErr.
func (a *App) reconnectFromDisconnectDialog() {
	name, url := a.disconnectDlg.name, a.disconnectDlg.url
	a.disconnectDlg = disconnectDialog{}
	a.deliberateClose = true // reconnecting on purpose — the frozen-tab teardown must not auto-retry
	if url != "" {
		// Reconnect means "we're coming back": snapshot today's log before the teardown so the reconnect to the same
		// server
		// can restore it. applySessionCarryIfPending checks the key match.
		a.snapshotSessionCarry()
	}
	a.Disconnect() // full teardown of the frozen session → lobby
	if url != "" {
		a.Connect(name, url) // Connect cancels any pending auto-retry and redials
	}
}

// drawDisconnectDialog paints the modal over the frozen courtroom: dimmed backdrop, the friendly line (when
// known), the raw reason, an optional auto-reconnect countdown, and Reconnect / Back to lobby buttons. Drawn at
// the frame tail with its family (pointer restored just before, fenced with them). Only while open.
func (a *App) drawDisconnectDialog(w, h int32) {
	c := a.ctx
	if !a.disconnectDlg.open {
		return // defensive: the outer guard only draws this while open
	}
	if a.now().Before(a.disconnectDlg.hiddenUntil) {
		return // still within the grace window — a quick self-heal must stay invisible
	}
	raw := a.disconnectDlg.reason.raw
	if raw == "" {
		raw = "connection ended"
	}
	friendly := a.disconnectDlg.reason.friendly
	friendlyH := int32(0)
	if friendly != "" {
		friendlyH = discDlgFriendlyH
	}

	// The raw reason wraps instead of being one clipped label. For a kick or ban it is the server's prose, and the
	// tail matters (a lockdown puts its access code on the last line). Copy hands over the whole string.
	textW := int32(discDlgW) - 2*pad
	rows := discDlgRawRows(h, friendlyH)
	lines, _ := a.disconnectDlg.wrap.get(a.chromeWrapMeasure(), a.ctx.fontChainGen,
		raw, textW, rows, serverNoticeTruncMark)

	mh := discDlgChromeH() + friendlyH + int32(len(lines))*discDlgLineH
	if mh < discDlgMinH {
		mh = discDlgMinH // the common one-line drop keeps exactly today's proportions
	}
	if lim := h - 2*serverNoticeMargin; mh > lim {
		mh = lim
	}
	// Same floor and edge clamps as the notice box: the window clamp can return a height smaller than the chrome, and
	// the buttons' y is measured from the panel's bottom, so a collapsed panel would stack Reconnect on the heading.
	// friendlyH is inside the floor so one row of the server's own reason is always visible.
	if floor := discDlgChromeH() + friendlyH + discDlgLineH; mh < floor {
		mh = floor
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw = discDlgW
	mx, my := (w-mw)/2, (h-mh)/2
	if mx < 0 {
		mx = 0 // narrower than the panel: Reconnect is left-anchored, keep it clickable
	}
	if my < 0 {
		my = 0 // taller than the window: heading and first reason rows stay on screen
	}
	m := sdl.Rect{X: mx, Y: my, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Disconnected from the server", ColText)

	// The friendly line (when we could name the cause) in the normal colour, then the raw reason always below it,
	// dimmer. Both are variable length, so clip to the panel.
	y := m.Y + discDlgHeadH
	if friendly != "" {
		c.LabelClipped(m.X+pad, y, mw-2*pad, friendly, ColText)
		y += friendlyH
	}
	bodyLimit := m.Y + mh - btnH - pad - discDlgCountdownH
	for _, line := range lines {
		if y+discDlgLineH > bodyLimit {
			break // the height clamp already told the wrap its budget; belt and braces
		}
		c.LabelClipped(m.X+pad, y, mw-2*pad, line, ColTextDim)
		y += discDlgLineH
	}

	// Auto-reconnect status: if a retry is armed and counting down, show it. A successful auto-reconnect closes the
	// dialog itself (pollAutoReconnect clears disconnectDlg; see reconnect.go). Reads the existing autoReconnect*
	// state.
	if !a.autoReconnectAt.IsZero() {
		secs := int(a.autoReconnectAt.Sub(a.now()).Round(time.Second) / time.Second)
		if secs < 0 {
			secs = 0
		}
		c.Label(m.X+pad, m.Y+mh-btnH-pad-discDlgCountdownH, "Reconnecting automatically in "+strconv.Itoa(secs)+"s…", ColAccent)
	}

	by := m.Y + mh - btnH - pad
	if c.Button(sdl.Rect{X: m.X + pad, Y: by, W: 150, H: btnH}, "Reconnect") {
		a.reconnectFromDisconnectDialog()
		return
	}
	// Copy, between the two actions: the raw reason is the server's own text and for a kick or ban may carry an
	// appeal
	// link, invite or whitelist code. It sends the whole raw string, never the wrapped view.
	if c.Button(sdl.Rect{X: m.X + (mw-discDlgCopyW)/2, Y: by, W: discDlgCopyW, H: btnH}, "Copy") {
		_ = sdl.SetClipboardText(raw)
		a.warnLine = clampLine("Copied the disconnect reason.")
		a.warnAt = a.now()
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 150, Y: by, W: 150, H: btnH}, "Back to lobby") {
		a.closeDisconnectDialogToLobby()
	}
}

// Involuntary-disconnect dialog geometry (hard rule 9). The panel grows with the wrapped reason, so the
// arithmetic is shared with discDlgRawRows.
const (
	// discDlgW is the panel width.
	discDlgW = 520
	// discDlgMinH is a floor, so a one-line transport drop keeps the standard size.
	discDlgMinH = 220
	// discDlgHeadH is the heading band, the m.Y+48 the whole modal family uses.
	discDlgHeadH = 48
	// discDlgFriendlyH is the row the named-cause sentence occupies; zero when friendly == "".
	discDlgFriendlyH = 24
	// discDlgLineH is one wrapped raw-reason row.
	discDlgLineH = 20
	// discDlgCountdownH is the strip above the buttons for the auto-reconnect countdown. Always reserved, so the
	// panel
	// doesn't resize when a retry arms.
	discDlgCountdownH = 26
	// discDlgRawMaxRows caps the wrapped reason. Lower than the notice box's budget because this panel also carries a
	// heading, the friendly line, the countdown strip and three buttons; Copy covers anything longer.
	discDlgRawMaxRows = 8
	// discDlgCopyW is the middle button; it fits in the 204 px gap between two 150 px buttons in a 520 px panel.
	discDlgCopyW = 110
)

// discDlgChromeH is every vertical pixel the panel spends other than on the friendly line and wrapped body.
func discDlgChromeH() int32 {
	return discDlgHeadH + discDlgCountdownH + btnH + pad
}

// discDlgRawRows is how many wrapped reason rows fit in a window h tall, from the same arithmetic as
// drawDisconnectDialog so the wrap budget and drawn area can't drift.
func discDlgRawRows(h, friendlyH int32) int {
	avail := h - 2*serverNoticeMargin - discDlgChromeH() - friendlyH
	rows := int(avail / discDlgLineH)
	if rows > discDlgRawMaxRows {
		rows = discDlgRawMaxRows
	}
	if rows < 1 {
		rows = 1 // unreachable above config.MinWindowH; never wrap to nothing
	}
	return rows
}
