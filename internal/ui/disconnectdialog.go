package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// Involuntary-disconnect dialog.
// This is one more member of the confirm-modal family (drawDisconnectConfirm /
// drawCloseTabConfirm / drawQuitConfirm): same dimmed backdrop, same fence
// discipline (fenced with the family in app.go's frame tail, pointer restored

// disconnectDialog is the modal's state. The zero value is closed/inert (open ==
// false), so an App with no drop showing costs a single bool check per frame.
type disconnectDialog struct {
	open   bool
	reason disconnectReason
	// name/url are the server we dropped from, captured at freeze time so
	// Reconnect redials exactly it even though the teardown that runs first would
	// otherwise re-point lastConn* (they're global). Mirrors Disconnect's
	// lastConn* re-capture rationale.
	name string
	url  string
	// wrap memoizes the laid-out raw reason. The raw line used to be ONE clipped
	// label, which for a kick/ban is the server's own multi-line prose — so an
	// access code or an appeal link on its second line was unreachable here too,
	// and past some length the label drew nothing at all (LabelClipped rasterizes
	// the whole string and clips only the blit). See paraWrapCache (qol.go).
	wrap paraWrapCache
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

// serverRemovalMessage pulls the SERVER'S OWN words out of a drop reason, which is
// the only thing the notice box is for.
// A removal with no reason attached returns false too. "The server banned you."
// is already on screen; a modal whose body is "no reason given" adds a click and
// no information.
func serverRemovalMessage(raw string) (string, bool) {
	rest, cut := strings.CutPrefix(raw, "Kicked: ")
	if !cut {
		rest, cut = strings.CutPrefix(raw, "Banned: ")
	}
	if !cut {
		return "", false
	}
	// Trailing newlines are common in server-formatted reasons and would otherwise
	// spend a body row on nothing. Interior newlines are the whole point and stay.
	if rest = strings.TrimSpace(rest); rest == "" {
		return "", false
	}
	return rest, true
}

// serverNoticeRemovedFallbackTitle heads a removal we could not name. Unreachable
// from today's single caller (friendlyDisconnectReason names both prefixes that
// serverRemovalMessage matches), and kept anyway so the box can never draw a blank
// heading if either side of that pairing changes.
const serverNoticeRemovedFallbackTitle = "The server closed your connection"

// freezeSessionUnderDialog is the ONE freeze both drop arms use: the active tab's
// link dying under the user, and a parked tab's death surfaced later by
// activateTab. It performs the NETWORK + AUDIO subset of Disconnect() — the part
// that must happen the instant the link dies — and deliberately not the session
// reset, tab close or navigation, which wait for the dialog's Back to lobby /
// Reconnect so the room stays drawable underneath.
func (a *App) freezeSessionUnderDialog(raw string) {
	// Re-capture the redial target BEFORE nilling the conn, exactly as Disconnect
	// does and for the same reason: lastConn* is GLOBAL and in a multi-tab session
	// may hold whatever server connected most recently rather than THIS one.
	if a.link.conn != nil {
		a.conn.lastConnName, a.conn.lastConnURL = a.srvMeta.serverName, a.srvMeta.serverKey
	}
	// Close any courtroom-owned confirm that could be open at freeze time — it
	// would stack with this modal for a frame. confirmDisconnect: the user clicked
	// Disconnect with instant-disconnect off and the link died before they
	// answered, so the pending question is moot. hidePrompt: a sprite-hide confirm;
	// clearing it cancels a moot confirm and leaves hiddenSprites alone. We do NOT
	// clear showQuitConfirm (a GLOBAL quit choice) or pendingCloseTab (a DIFFERENT
	// tab's confirm) — neither is ours to cancel on a drop.
	a.dialogs.confirmDisconnect = false
	a.dialogs.hidePrompt = ""
	if a.link.conn != nil {
		a.link.conn.Close() // idempotent: the socket is already dead on every drop path
	}
	a.link.conn = nil // pumpConnection early-returns on a nil conn, so the room freezes
	if a.d.Audio != nil {
		// The server's area music must not outlive the connection (as Disconnect does).
		a.d.Audio.StopMusic()
		a.budget.musicTabDucked = false
		a.budget.musicAwaitURL = ""
		a.budget.musicAwaitSince = time.Time{}
	}
	a.stopVoiceAudio() // free the voice devices with the connection
	a.link.voiceJoined, a.link.voiceMicOn = false, false
	// hiddenUntil is deliberately the zero value: ALWAYS visible immediately. The
	// 5-minute grace window this function used to take is half of what made
	// v1.81.4 rip the freeze out — a drop hid itself, self-healed in the
	// background, and dumped the user at char-select without ever saying why.
	a.openDisconnectDialog(a.srvMeta.serverName, a.srvMeta.serverKey, raw, time.Time{})
}

// handleInvoluntaryDrop is the SHARED tail every active-tab drop path runs once its
// reason is known: pumpConnection's SendErr (half-dead write) and closed-Incoming
// (transport drop) branches, and handleSessionEvents' EventDisconnect (kick/ban).
func (a *App) handleInvoluntaryDrop(reason string) {
	deliberate := a.conn.deliberateClose
	a.conn.connErr = reason // set FIRST: it must survive whichever branch runs below
	// A live courtroom FREEZES under the dialog instead of being torn down: the
	// logs and the last frame stay readable, the reason is named, and Reconnect is
	// one click. That is the same end state a PARKED tab reaches via activateTab,
	// and playtesters reported the inconsistency directly — a background tab showed
	// the box while the tab they were looking at was slammed to the server list.
	if !deliberate && a.link.room != nil && a.link.sess != nil && a.screen == ScreenCourtroom {
		a.freezeSessionUnderDialog(reason)
		return
	}
	// No room to freeze (char-select, already torn down) or a deliberate close:
	noticeBody, showNotice := "", false
	if !deliberate {
		noticeBody, showNotice = serverRemovalMessage(reason)
	}
	noticeTitle, noticeServer := friendlyDisconnectReason(reason).friendly, a.srvMeta.serverName
	if noticeTitle == "" {
		noticeTitle = serverNoticeRemovedFallbackTitle
	}
	//
	// A genuine transport drop about to auto-retry is "we're coming back" just
	// as much as a dialog Reconnect click is: snapshot today's log before
	// Disconnect wipes it, so a successful retry can restore it
	// (applySessionCarryIfPending, gated on the SAME server). Gated on the same
	// shouldAutoReconnect check as the re-arm below, so a kick/ban/deliberate
	// close starts clean — nothing here for those cases to ever consume.
	if shouldAutoReconnect(reason, deliberate) {
		a.snapshotSessionCarry()
	}
	a.Disconnect() // → lobby; nils conn/sess and cancels any pending retry
	if showNotice {
		// AFTER the teardown, deliberately. The box is App-level so it survives
		// resetSessionState either way, but opening it last means no teardown step
		// can run with a modal already fenced over the screen.
		a.openServerNotice(serverNoticeRemoved, noticeTitle, noticeServer, noticeBody)
	}
	if shouldAutoReconnect(reason, deliberate) {
		a.scheduleAutoReconnect() // re-arm the countdown the teardown just cancelled
	}
}

// openDisconnectDialog is the single place the dialog's open state is set, so the
// two entry paths (ACTIVATING a tab that died in the background via activateTab,
// and a failed retry re-surfacing it) can't diverge in what the modal shows. It
// is reached for BOTH a background/parked tab's death and an active-tab drop,
// through the one shared freeze (see freezeSessionUnderDialog). name/url are the
// redial target captured for Reconnect (see the disconnectDialog field doc for why
// they're frozen here rather than read from lastConn* at click time). hiddenUntil
// is the zero value for immediate display, which is what both arms now pass.
func (a *App) openDisconnectDialog(name, url, raw string, hiddenUntil time.Time) {
	// A BB popup notice can be on screen when the link dies — a server that is about
	// to close the socket is exactly the kind that just sent one. Two blocking modals
	// would then stack, and the server's MOTD is moot the instant the session is gone.
	if a.font.serverNoticeDlg.open && a.font.serverNoticeDlg.kind == serverNoticeInfo {
		a.closeServerNotice()
	}
	a.dialogs.disconnectDlg = disconnectDialog{
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
	a.dialogs.disconnectDlg = disconnectDialog{}
	a.conn.deliberateClose = true // Back to lobby is a user choice — this teardown must not auto-reconnect
	a.cancelAutoReconnect()       // and the user opted out of any pending countdown
	a.Disconnect()
}

// reconnectFromDisconnectDialog is the dialog's Reconnect: tear the frozen
// session fully down (deliberate, so the teardown itself doesn't auto-retry) and
// redial the SAME server + name — the exact lobby-Reconnect path (Connect), so
// the normal join flow (fresh courtroom entry, once-per-join auto-login) takes
// over. Clears the dialog first so its fence releases; a failed dial lands on the
// lobby with connErr, just like the lobby button.
func (a *App) reconnectFromDisconnectDialog() {
	name, url := a.dialogs.disconnectDlg.name, a.dialogs.disconnectDlg.url
	a.dialogs.disconnectDlg = disconnectDialog{}
	a.conn.deliberateClose = true // reconnecting on purpose — the frozen-tab teardown must not auto-retry
	if url != "" {
		// Clicking Reconnect IS "we're coming back": snapshot today's log before
		// the teardown below wipes it, so the redial to this SAME server (below)
		// can restore it — applySessionCarryIfPending checks the key match, so
		// this is a no-op harvest if url somehow changes before the dial lands.
		a.snapshotSessionCarry()
	}
	a.Disconnect() // full teardown of the frozen session → lobby
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
	if !a.dialogs.disconnectDlg.open {
		return // defensive: the outer guard only draws this while open
	}
	if a.now().Before(a.dialogs.disconnectDlg.hiddenUntil) {
		return // still within the grace window — a quick self-heal must stay invisible
	}
	raw := a.dialogs.disconnectDlg.reason.raw
	if raw == "" {
		raw = "connection ended"
	}
	friendly := a.dialogs.disconnectDlg.reason.friendly
	friendlyH := int32(0)
	if friendly != "" {
		friendlyH = discDlgFriendlyH
	}

	// The raw reason wraps now instead of being one clipped label. For a kick or a
	// ban it IS the server's prose, newlines and all, and the tail is the part that
	// matters (a lockdown puts its access code on the last line). Copy below hands
	// over the whole untruncated string either way.
	textW := int32(discDlgW) - 2*pad
	rows := discDlgRawRows(h, friendlyH)
	lines, _ := a.dialogs.disconnectDlg.wrap.get(a.chromeWrapMeasure(), a.ctx.fontChainGen,
		raw, textW, rows, serverNoticeTruncMark)

	mh := discDlgChromeH() + friendlyH + int32(len(lines))*discDlgLineH
	if mh < discDlgMinH {
		mh = discDlgMinH // the common one-line drop keeps exactly today's proportions
	}
	if lim := h - 2*serverNoticeMargin; mh > lim {
		mh = lim
	}
	// Same absolute floor and same edge clamps as the notice box, for the same reason:
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

	// The friendly line (when we could name the cause) in the normal text colour,
	// then the raw reason ALWAYS below it, dimmer — a server restart and a local
	// failure can't look identical, and no cause is hidden behind a guess. Both are
	// server-supplied / error-derived and variable length, so clip to the panel
	// (the §3.4 spill-past-the-border class of bug).
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

	// Auto-reconnect status: if a retry is armed and counting down (the pref is on
	// and this was a genuine drop), show it — the buttons still work, and a
	// successful auto-reconnect closes the dialog on its own (pollAutoReconnect
	// clears disconnectDlg on success; see reconnect.go). Reads the EXISTING
	// autoReconnect* state, no new timer.
	if !a.conn.autoReconnectAt.IsZero() {
		secs := int(a.conn.autoReconnectAt.Sub(a.now()).Round(time.Second) / time.Second)
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
	// Copy, between the two actions: the raw reason is the server's own text, and for
	// a kick or a ban it carries the thing you have to act on — an appeal link, a
	// Discord invite, a whitelist code. Reading it off the screen and retyping it was
	// the only option before. Sends the WHOLE raw string, never the wrapped view: a
	// re-flowed or truncated code is worse than none.
	if c.Button(sdl.Rect{X: m.X + (mw-discDlgCopyW)/2, Y: by, W: discDlgCopyW, H: btnH}, "Copy") {
		_ = sdl.SetClipboardText(raw)
		a.warnLine = clampLine("Copied the disconnect reason.")
		a.warnAt = a.now()
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 150, Y: by, W: 150, H: btnH}, "Back to lobby") {
		a.closeDisconnectDialogToLobby()
	}
}

// Involuntary-disconnect dialog geometry. Named per rule §17.9; the panel now GROWS
// with the wrapped reason instead of being a fixed 520x220, so the arithmetic has to
// be stated once and shared with discDlgRawRows rather than inlined twice.
const (
	// discDlgW is unchanged from the fixed-size version.
	discDlgW = 520
	// discDlgMinH is the old fixed height, kept as a FLOOR so the overwhelmingly
	// common case — a one-line transport drop — looks exactly as it did.
	discDlgMinH = 220
	// discDlgHeadH is the heading band, the m.Y+48 the whole modal family uses.
	discDlgHeadH = 48
	// discDlgFriendlyH is the row the named-cause sentence occupies when there is
	// one. Zero when friendly == "", which is why it is added separately.
	discDlgFriendlyH = 24
	// discDlgLineH is one wrapped raw-reason row.
	discDlgLineH = 20
	// discDlgCountdownH is the strip above the buttons the auto-reconnect countdown
	// draws in. RESERVED ALWAYS, even for a kick or ban that can never count down,
	// so the panel does not resize under the user's eyes when a retry arms.
	discDlgCountdownH = 26
	// discDlgRawMaxRows caps the wrapped reason. Lower than the notice box's budget
	// because this panel also carries a heading, the friendly line, the countdown
	// strip and three buttons — and Copy is the escape hatch for anything longer.
	discDlgRawMaxRows = 8
	// discDlgCopyW is the middle button. It has to fit between two 150 px buttons in
	// a 520 px panel, which leaves 204 px of gap.
	discDlgCopyW = 110
)

// discDlgChromeH is every vertical pixel the panel spends on something other than
// the friendly line and the wrapped body.
func discDlgChromeH() int32 {
	return discDlgHeadH + discDlgCountdownH + btnH + pad
}

// discDlgRawRows is how many wrapped reason rows fit in a window h tall. Derived
// from the same arithmetic drawDisconnectDialog lays out with, so the wrap budget and
// the drawn area cannot drift apart — the duplicated-layout-constant trap the lobby
// row helpers call out (screens.go).
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
