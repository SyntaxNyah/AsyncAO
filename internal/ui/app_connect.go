package ui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/hwid"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/presence"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

func (a *App) Connect(name, wsURL string) {
	a.cancelAutoReconnect() // a deliberate Join/Reconnect takes over from any pending auto-retry
	a.connectWith(name, wsURL, context.Background())
}

// friendlyConnError turns a raw dial error into plain lobby guidance; the raw error still goes to the debug log.
func friendlyConnError(wsURL string, err error) string {
	// A server that answered and refused comes first: the library's "expected handshake response status code 101 but
	// got 503" would otherwise match the " status" arm below and wrongly say the server isn't WebSocket. The full text
	// goes to the notice box (connectWith).
	var de *protocol.DialError
	if errors.As(err, &de) && de.StatusCode != 0 {
		switch de.StatusCode {
		case http.StatusTooManyRequests:
			return "The server is rate-limiting connections — wait a moment and try again."
		case http.StatusForbidden, http.StatusUnauthorized:
			return "The server refused your connection. It may be whitelisting who can join."
		case http.StatusServiceUnavailable:
			return "The server is not accepting connections right now (it may be locked down)."
		default:
			return "The server refused your connection (" + de.Status + ")."
		}
	}
	s := strings.ToLower(err.Error())
	switch {
	// Proxy cases come first: the websocket library wraps every dial failure with its own name, so a proxy error would
	// fall into the "not a WebSocket server" arm below.
	case errors.Is(err, netproxy.ErrProxyUnresolvable):
		return "This computer is set to find its proxy from a script AsyncAO can't run, so connections are blocked rather than sent around it. Settings → Power user → Proxy: enter the proxy yourself, or choose \"never use one\"."
	case strings.Contains(s, "proxyconnect") || strings.Contains(s, "proxy"):
		return "Couldn't reach the proxy this computer is configured to use. Settings → Power user → Proxy shows which one that is; \"never use one\" connects directly instead."
	case strings.Contains(s, "no such host") || strings.Contains(s, "lookup"):
		return "Couldn't find that server — double-check the address."
	case strings.Contains(s, "refused"):
		return "Connection refused — the server may be offline, or the port is wrong."
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded") || strings.Contains(s, "timed out"):
		return "Timed out — the server didn't respond (it may be down or unreachable)."
	case strings.Contains(s, "certificate") || strings.Contains(s, "x509") || strings.Contains(s, "tls"):
		msg := "Secure (wss://) connection failed — the server's certificate looks invalid or expired."
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(wsURL)), "wss://") {
			msg += " If the server isn't actually secure, try ws:// instead."
		}
		return msg
	case strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route"):
		return "Network unreachable — check your internet connection."
	case strings.Contains(s, "bad handshake") || strings.Contains(s, "unexpected") || strings.Contains(s, " status") || strings.Contains(s, "426") || strings.Contains(s, "websocket"):
		return "The server didn't accept a WebSocket connection — it may not be a WebSocket (AO2 2.11) server. AsyncAO can't use legacy raw-TCP servers."
	case strings.Contains(s, "unsupported") || strings.Contains(s, "invalid") || strings.Contains(s, "missing") || strings.Contains(s, "parse"):
		return "That address doesn't look right — use ws:// or wss:// (or host:port)."
	default:
		return "Couldn't connect: " + err.Error()
	}
}

// connectWith is Connect with a caller-chosen dial context; restore-on-launch passes a short timeout so a dead
// remembered server can't freeze boot.
func (a *App) connectWith(name, wsURL string, dialCtx context.Context) {
	a.connErr = ""                              // a fresh attempt clears any stale lobby error/reason
	a.deliberateClose = false                   // a fresh connection starts with no pending deliberate-close intent
	a.lastConnName, a.lastConnURL = name, wsURL // remember for one-click Reconnect on a drop/failure
	a.d.Prefs.SetLastServer(name, wsURL)        // persist for auto-connect / quick-connect on next launch
	a.pinging = false                           // leaving the lobby: don't let a dropped done-sentinel wedge re-ping
	a.parkActive()
	if !a.allocateTab() {
		return // connErr set; lobby shows it
	}
	a.resetSessionState()
	a.serverName = name
	a.serverKey = wsURL
	a.connAt = time.Now()
	// The first server joined after the per-server wardrobe split inherits the old flat collection.
	a.d.Prefs.ClaimLegacyWardrobe(wsURL)
	a.refreshCharKeys()
	a.refreshShownameKeys()
	a.refreshICPhraseKeys()
	a.syncLoginBuffers() // settings/dialog boxes show this server's creds
	a.themeBound = a.d.Prefs.Load().ServerWarmInfoFor(wsURL).Theme
	a.ensureThemeForSession()
	// Case notebook: per-server pins load off-thread; the payload carries the key so it routes even after a tab switch.
	ch := a.notebookRes
	go func(key string) {
		if nb, err := config.LoadNotebook(key); err == nil {
			select {
			case ch <- notebookLoad{key: key, nb: nb}:
			default:
			}
		}
	}(wsURL)
	// TLS: default off accepts self-signed wss certs (most community AO servers); the Security toggle enables strict
	// verification.
	conn, err := protocol.Dial(dialCtx, wsURL, protocol.DialOptions{
		SkipTLSVerify: !a.d.Prefs.Load().ValidateTLSCerts,
		Origin:        a.d.Prefs.Load().WSOrigin, // power-user: servers that allowlist their web client's origin ("" = none)
	})
	if err != nil {
		a.connErr = friendlyConnError(wsURL, err) // #9: human guidance, not a raw Go error
		a.pushDebug("connect failed: " + err.Error())
		// A refusal the server explained gets the notice box (the explanation is how you get in; the lobby line can't
		// hold it). Snapshot the name before the teardown: serverName is on sessionState.
		noticeServer := a.serverName
		var de *protocol.DialError
		refused := errors.As(err, &de) && de.Body != ""
		a.closeActiveTab()
		a.screen = ScreenLobby
		if refused {
			a.openServerNotice(serverNoticeRemoved, a.connErr, noticeServer, de.Body)
		}
		return
	}
	a.conn = conn
	// Rejoin a pending carry from this server's own drop only after the dial succeeds, so a failed auto-reconnect
	// attempt doesn't burn the one-shot carry. No-ops unless wsURL matches the server the carry was captured from.
	a.applySessionCarryIfPending(wsURL)
	// Wake the render loop the instant a packet lands (event-driven loop); the wake is never treated as user input.
	conn.SetNotify(PushWake)
	a.pkts.reset() // a fresh connection starts a clean packet history
	a.pktConn = conn
	a.sess = courtroom.NewSession(func(p protocol.Packet) error {
		if conn == a.conn { // record only the ACTIVE connection's sends (a parked tab's Ping won't match)
			a.recordPacket(p, true)
		}
		return conn.Send(context.Background(), p)
	}, hdid(wsURL))
	// Prime the keepalive goroutine immediately so the CH ping flows even if the first frame is delayed or the window
	// is minimized; pumpConnection refreshes it as MyCharID changes.
	conn.SetKeepalive(a.sess.KeepalivePacket())
	a.screen = ScreenCharSelect
}

// pumpTabRestore reconnects one remembered tab per frame, each on a short dial budget, so blocking dials don't pile
// into one boot freeze.
func (a *App) pumpTabRestore() {
	if len(a.restoreQueue) == 0 {
		return
	}
	tab := a.restoreQueue[0]
	a.restoreQueue = a.restoreQueue[1:]
	ctx, cancel := context.WithTimeout(context.Background(), restoreDialTimeout)
	defer cancel()
	a.connectWith(tab.Name, tab.URL, ctx)
}

// collectOpenTabs snapshots the open server tabs (name + ws URL) for restore-on-launch: the active tab from the session
// fields, parked tabs from their stored state. Skips rehearsal, dead and blank-URL tabs and dedups by URL.
func (a *App) collectOpenTabs() []config.OpenTab {
	out := make([]config.OpenTab, 0, len(a.tabs))
	seen := make(map[string]struct{}, len(a.tabs))
	add := func(name, url string, dead, rehearse bool) {
		if dead || rehearse || url == "" {
			return
		}
		if _, dup := seen[url]; dup {
			return
		}
		seen[url] = struct{}{}
		out = append(out, config.OpenTab{Name: name, URL: url})
	}
	for i, t := range a.tabs {
		if i == a.activeTab {
			add(a.serverName, a.serverKey, false, a.rehearsal)
		} else {
			add(t.state.serverName, t.state.serverKey, t.dead, t.state.rehearsal)
		}
	}
	return out
}

// RememberOpenTabs persists the open tabs at shutdown; no-op unless restore-on-launch is on or a relaunch asked to keep
// them.
func (a *App) RememberOpenTabs() {
	if !a.d.Prefs.Load().RestoreTabs && !a.relaunchKeepTabs {
		return
	}
	a.d.Prefs.SetOpenTabs(a.collectOpenTabs())
}

// Disconnect tears the active session down (its tab closes, other tabs keep running) and returns to the lobby.
func (a *App) Disconnect() {
	// A full teardown ends any frozen-courtroom dialog: it only makes sense while a session is drawn-but-dead, and a
	// modal must not be stranded over the empty lobby. Invariant: disconnectDlg.open implies screen == Courtroom.
	if a.disconnectDlg.open {
		a.disconnectDlg = disconnectDialog{}
	}
	a.cancelAutoReconnect() // teardown cancels any pending retry; the EventDisconnect path re-arms after
	if a.conn != nil {
		a.conn.Close()
	}
	// The server's area music must not outlive the connection.
	if a.d.Audio != nil {
		a.d.Audio.StopMusic()
		a.musicTabDucked = false        // a fresh session's music must start audible, not ducked
		a.musicAwaitURL = ""            // stream stopped: drop any pending un-duck (invariant: stopped ⇒ no await)
		a.musicAwaitSince = time.Time{} // and its timeout stamp
	}
	a.stopVoiceAudio() // free the voice capture/playback devices with the connection
	a.voiceJoined, a.voiceMicOn = false, false
	if a.rehearsal {
		a.d.Manager.SetOffline(false)
	}
	if a.notebook != nil {
		go func(nb *config.Notebook) { _ = nb.Flush() }(a.notebook)
	}
	if a.msRaster != nil {
		a.msRaster.Destroy()
	}
	if a.glyphCache != nil {
		a.glyphCache.Purge() // free the #M5 white-glyph textures
	}
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = nil
		a.d.Viewport.OnFrameShown = nil // #17: no live room owns the viewport now
		a.d.Viewport.PurgePostFX()      // #10: free the cached retro-overlay textures
	}
	// Remember which server dropped so the lobby's Reconnect and auto-reconnect target it even with other tabs
	// connected (lastConn* is global). Guarded on a live socket so rehearsal or a dead tab can't claim a phantom
	// target.
	if a.conn != nil {
		a.lastConnName, a.lastConnURL = a.serverName, a.serverKey
	}
	a.closeActiveTab()
	a.resetSessionState()
	a.selServer, a.descLines = -1, nil
	// Drain in-flight fetches so stale payloads can't land later.
	select {
	case <-a.iniRes:
	default:
	}
	a.bgPick.show = false
	select {
	case <-a.bgPick.res:
	default:
	}
	a.cancelDownload()   // a download targets this server's assets; stop it
	if a.d.Pool != nil { // nil in headless tests
		a.d.Pool.BumpEpoch() // cancel queued speculation for the old server
	}
	a.updatePresence() // sess is nil now → clears the Discord activity
	// A bound theme leaves with its server; the global pick returns.
	a.ensureThemeForSession()
	a.screen = ScreenLobby
	// Consume the deliberate-close intent: it applied only to this teardown.
	a.deliberateClose = false
}

// applyWindowSize resizes the window to a clamped, recentered size and persists it; it leaves fullscreen first.
func (a *App) applyWindowSize(w, h int) {
	if a.d.Prefs.Load().WindowFull {
		a.ctx.SetWindowFullscreen(false)
		a.d.Prefs.Update(func(sn *config.Settings) { sn.WindowFull = false })
	}
	// A maximized window swallows the resize (SDL_SetWindowSize has no maximized guard, and on Windows SetWindowPos
	// runs against an HWND still carrying WS_MAXIMIZE), so unmaximize first. Not in Ctx.ResizeWindow: applyFullscreen
	// shares it and F11 must not un-maximize. See Ctx.UnmaximizeWindow for the SDL sources.
	a.ctx.UnmaximizeWindow()
	uw, uh := a.ctx.WindowDisplayUsable()
	w, h = config.ClampWindowSize(w, h, uw, uh)
	a.ctx.ResizeWindow(w, h)
	a.d.Prefs.SetWindowSize(w, h)
}

// themeDesignWindowSize reports the window size the active theme was drawn for (its courtroom_design.ini `courtroom`
// rect) and whether it declares one. Stock AO2 sizes its window to that rect, so themes render 1:1 (AO2-Client
// charselect.cpp:83). `courtroom` is the one rect every reference theme has; char_select is often absent or differs.
// Reads the pristine geometry, so a layout-editor override can't drag the window.
//
// `viewport` must be declared too: themeLayout() needs both, and a theme without a viewport draws through the classic
// layout, so its canvas isn't on screen.
func (a *App) themeDesignWindowSize() (int, int, bool) {
	r, ok := a.themeRectsOrig["courtroom"]
	if !ok || r.W <= 0 || r.H <= 0 {
		return 0, 0, false
	}
	if _, ok := a.themeRectsOrig["viewport"]; !ok {
		return 0, 0, false
	}
	return r.W, r.H, true
}

// themeDesignSizeOffer reports the theme's design size and whether the manual "Theme's design size" affordance is
// offered: not while the themed courtroom layout is off, since the classic layout is drawn and the canvas isn't on
// screen.
func (a *App) themeDesignSizeOffer() (int, int, bool) {
	w, h, ok := a.themeDesignWindowSize()
	return w, h, ok && a.d.Prefs.Load().ThemeLayoutOn
}

// themeDesignWindowInset is the extra window height a snap-to-canvas needs so the canvas still lands 1:1: the
// app-chrome band (chrometop.go) sits above it. Deliberately the full band (menu bar + tab strip), not a.topChromeH():
// the live band varies with open sessions, and a constant keeps the snap the same from any screen.
const themeDesignWindowInset = menuBarH + tabBarH

// themeDesignWindowTarget is the window size a snap-to-canvas asks for: the theme's canvas plus the chrome band.
func (a *App) themeDesignWindowTarget() (int, int, bool) {
	w, h, ok := a.themeDesignWindowSize()
	if !ok {
		return 0, 0, false
	}
	return w, h + int(themeDesignWindowInset), true
}

// applyThemeDesignWindowSize snaps the window to the active theme's design size, reporting whether there was one. This
// is the manual path: an explicit command that doesn't second-guess a maximized or fullscreen window.
func (a *App) applyThemeDesignWindowSize() bool {
	w, h, ok := a.themeDesignWindowTarget()
	if !ok {
		return false
	}
	a.applyWindowSize(w, h)
	return true
}

// maybeResizeToThemeDesign is the automatic snap: it consumes the one-shot armed at the user-initiated theme-pick sites
// and resizes to the new theme's canvas. gen is the themeGen of the apply that just landed.
//
// One-shot because pollThemeApply also runs at boot, on healTheme, on the texture-filter Purge recovery and when a
// server binds a theme, where a jumping window would be worse. Only the apply the arming named may spend it: an older
// landing carries the previous theme's geometry, a newer one may be a server's own theme. A newer landing also disarms,
// since the armed apply can then never arrive (themeRes is a single slot and generations only increase).
//
// Skipped under fullscreen, maximized, the themed-layout opt-out, and a no-op resize (ResizeWindow recenters).
func (a *App) maybeResizeToThemeDesign(gen uint64) {
	if a.themeResizeArmGen == 0 {
		return
	}
	if gen != a.themeResizeArmGen {
		if gen > a.themeResizeArmGen {
			// Outraced: a later apply landed, so the armed one can never land. Disarm; the snap is lost, which is the
			// safe failure.
			a.themeResizeArmGen = 0
		}
		return
	}
	a.themeResizeArmGen = 0
	if a.d.Prefs.Load().WindowFull || a.ctx.WindowMaximized() || !a.d.Prefs.Load().ThemeLayoutOn {
		return
	}
	w, h, ok := a.themeDesignWindowTarget()
	if !ok {
		return
	}
	// ClampWindowSize is pure, so clamping here to compare and again inside applyWindowSize gives the same target.
	uw, uh := a.ctx.WindowDisplayUsable()
	tw, th := config.ClampWindowSize(w, h, uw, uh)
	if cw, ch := a.ctx.WindowSize(); cw == tw && ch == th {
		return
	}
	a.applyWindowSize(w, h)
}

// armBootThemeResize gives the one boot apply named by gen the same window snap as a theme pick, only on the launch
// where the theme-fit default move (Stretch to Native) ran for this preferences file: Native draws the canvas 1:1 and
// centred, so without a matching window the upgrade shows a small canvas ringed by bars.
//
// It is driven by the stamp being newly written (persisted synchronously on the migrating launch, so a crash inside the
// saver's debounce can't resurrect it), never by "is this boot". A file with a deliberate Letterbox/Crop/Custom pick
// reports false. Naming one generation keeps it off healTheme, the purge recovery and server-bound themes.
func (a *App) armBootThemeResize(gen uint64) {
	if a.d.Prefs.ThemeFitDefaultJustMoved() {
		a.themeResizeArmGen = gen
	}
}

// fitWindowToScreen sizes the window to the display's usable area and recenters: the rescue for a window bigger than
// the monitor.
func (a *App) fitWindowToScreen() {
	uw, uh := a.ctx.WindowDisplayUsable()
	if uw <= 0 || uh <= 0 {
		a.applyWindowSize(config.DefaultWindowW, config.DefaultWindowH)
		return
	}
	a.applyWindowSize(uw, uh) // ClampWindowSize keeps it within the usable bounds
}

// applyFullscreen toggles borderless fullscreen and persists it; leaving snaps back to a clamped windowed size.
func (a *App) applyFullscreen(on bool) {
	a.ctx.SetWindowFullscreen(on)
	a.d.Prefs.Update(func(sn *config.Settings) { sn.WindowFull = on })
	if !on {
		w, h := a.d.Prefs.Load().WindowSize()
		if w <= 0 || h <= 0 {
			w, h = config.DefaultWindowW, config.DefaultWindowH
		}
		uw, uh := a.ctx.WindowDisplayUsable()
		w, h = config.ClampWindowSize(w, h, uw, uh)
		a.ctx.ResizeWindow(w, h)
	}
}

// toggleFullscreen flips fullscreen, shared by F11 and the Settings checkbox.
func (a *App) toggleFullscreen() { a.applyFullscreen(!a.d.Prefs.Load().WindowFull) }

// effectiveShowname is the name outgoing messages carry: the courtroom override when filled, else the Settings
// showname.
func (a *App) effectiveShowname() string {
	if s := strings.TrimSpace(a.shownameOverride); s != "" {
		return s
	}
	return a.d.Prefs.Load().Showname
}

// ensureNameOpts rebuilds the cached name-picker options (a leading "▾", then the saved shownames) only when the preset
// count changes.
func (a *App) ensureNameOpts() {
	if n := a.d.Prefs.Load().ShownameCount(); a.nameOpts == nil || n != a.nameOptsLen {
		a.nameOptsLen = n
		a.nameOpts = append([]string{"▾"}, a.d.Prefs.Load().ShownameList()...)
	}
}

// pickNameDropdown draws the ▾ showname picker at r and returns the chosen preset, or "" if none; shown only when
// presets exist.
func (a *App) pickNameDropdown(id string, r sdl.Rect) string {
	if len(a.nameOpts) <= 1 {
		return ""
	}
	if next, changed := a.ctx.Dropdown(id, r, a.nameOpts, 0); changed && next > 0 {
		return a.nameOpts[next]
	}
	return ""
}

// updatePresence pushes (or clears) the Discord activity from the user's per-field choices; the presence worker owns
// all I/O.
func (a *App) updatePresence() {
	if a.d.Presence == nil {
		return
	}
	dp := a.d.Prefs.Load().DiscordRPC
	if !dp.Enabled {
		a.d.Presence.Clear()
		return
	}
	if a.sess == nil {
		// In the lobby, keep "Playing AsyncAO" up; no server/character applies and no elapsed timer (it would reset on
		// every connect).
		a.d.Presence.Set(presence.Activity{Details: "In the lobby"})
		return
	}
	act := presence.Activity{Start: a.connAt, Details: "In court"}
	if dp.ShowServer && a.serverName != "" {
		act.Details = "On " + a.serverName
	}
	var parts []string
	if dp.ShowName {
		if n := strings.TrimSpace(a.effectiveShowname()); n != "" {
			parts = append(parts, n)
		}
	}
	if dp.ShowChar {
		if ch := a.myCharName(); ch != "" {
			if len(parts) > 0 {
				parts = append(parts, "as "+ch)
			} else {
				parts = append(parts, ch)
			}
		}
	}
	if dp.ShowArea && a.curArea != "" {
		parts = append(parts, "— "+a.curArea)
	}
	act.State = strings.Join(parts, " ")
	a.d.Presence.Set(act)
}

// hdid is the device ID AO servers key bans on, for the server at wsURL: derived from stable per-machine roots and
// bound to that one server (hwid.For), so the id a server receives can't be replayed against another. See
// internal/hwid.
func hdid(wsURL string) string { return hwid.For(wsURL) }

// pumpConnection drains incoming packets into the session each frame.
func (a *App) pumpConnection() {
	if a.conn == nil || a.sess == nil {
		return
	}
	// Keep the keepalive goroutine primed with the current CH payload (it carries MyCharID, which changes only on the
	// render thread). The goroutine sends the ping, so it keeps flowing while the window is minimized and this loop is
	// stalled.
	a.conn.SetKeepalive(a.sess.KeepalivePacket())
	// Half-dead write side: Session.reply records the first failed write in sendErr and swallows later packets, so a
	// non-nil SendErr means we are writing into a dead socket. Tear down and auto-reconnect; a write failure is a
	// transport drop, never a ban/kick.
	if err := a.sess.SendErr(); err != nil {
		reason := "connection lost: " + err.Error()
		a.connErr = reason
		a.pushDebug("disconnected: " + reason)
		a.handleInvoluntaryDrop(reason) // freeze under the dialog (or plain teardown) + arm auto-reconnect
		return
	}
	for {
		// A handled packet can tear the session down mid-drain (KK/KB/BD -> EventDisconnect -> Disconnect() zeroes
		// conn/sess); re-check before touching a.conn or a.sess again.
		if a.conn == nil || a.sess == nil {
			return
		}
		select {
		case p, ok := <-a.conn.Incoming():
			a.uiDirty = true // packets (or the drop below) change UI-visible state — redraw-worthy damage
			if !ok {
				// The Incoming channel closed: a transport drop (network blip, server restart, read error). A
				// deliberate Disconnect nils a.conn first and never reaches here; this is the case auto-reconnect
				// exists for.
				reason := "connection closed"
				if err := a.conn.Err(); err != nil {
					reason = err.Error()
				}
				a.connErr = reason
				a.pushDebug("disconnected: " + reason)
				a.handleInvoluntaryDrop(reason) // freeze under the dialog (or plain teardown) + arm auto-reconnect
				return
			}
			a.lastPktHdr, a.lastPktAt = p.Header, time.Now()
			a.notePktConn() // reset the packet ring if the active conn changed (tab switch / reconnect)
			a.recordPacket(p, false)
			a.handleSessionEvents(a.sess.HandlePacket(p))
		default:
			return
		}
	}
}

func (a *App) handleSessionEvents(events []courtroom.Event) {
	for _, ev := range events {
		switch ev.Kind {
		case courtroom.EventAssetURL:
			a.rebuildAssetOrigin()
		case courtroom.EventReady:
			a.rebuildAssetOrigin()
			a.prefetchCharIcons()
			a.sendCasingPrefs()
			a.prewarmServer()
			// Remember enough to rehearse this server offline later.
			a.d.Prefs.RememberServerOrigin(a.serverKey, a.sess.AssetURL)
			names := make([]string, len(a.sess.Chars))
			for i := range a.sess.Chars {
				names[i] = a.sess.Chars[i].Name
			}
			a.d.Prefs.RememberServerChars(a.serverKey, names)
			a.autoLoginOnReady()
		case courtroom.EventBackground:
			// Remember it for next visit's pre-warm; the room still consumes the event below.
			a.d.Prefs.RememberServerBackground(a.serverKey, ev.Text)
			if ev.Wipe {
				// Also drop the overlays AO2's set_background teardown drops that live on the App, not the Scene: the
				// WT/CE splash and testimony badge (ui_vp_testimony->stopPlayback) and the evidence popup
				// (ui_vp_evidence_display->reset). Deliberate deviation: AO2 stops testimony on every BN; we do it only
				// in the wipe branch so a plain /bg is unchanged.
				a.wtceName, a.testimonyOn, a.evShowImg = "", false, ""
			}
		case courtroom.EventMusic:
			// Log "<name> has played a song: <song>" in the IC log like webAO and AO2-Client (handle_song). The room
			// still plays the track below.
			a.logMusicChange(ev)
			a.noteMusicHistory(ev) // M12 slice: session "recently played" history
			// Stamp when this track began, for the cross-tab resume seek. A ~stop clears MusicTrack, so the stamp is
			// harmless then. See musicStartedAt.
			a.musicStartedAt = a.now()
			// A live music change on the active tab (a DJ /play) makes this tab the audible owner of the stream, but
			// only once the requested track is what the mixer plays. If the stream is still ducked-foreign (a
			// backgrounded tab's song), un-ducking now would make that foreign track audible while the replacement
			// downloads. So keep the duck and record the awaited URL; settleAwaitedMusic lifts it when the new track
			// lands. Skip the ~stop sentinel. Same URL builder as the room's PlayMusic (a.urls), so the await compares
			// equal to Audio.CurrentMusicURL.
			if a.sess.MusicTrack != "" {
				// This tab is now the audible owner of the stream: stamp the owner so its per-server music volume
				// governs it, even if the tab is later backgrounded under MusicAcrossTabs. See musicOwnerKey.
				a.musicOwnerKey = a.serverKey
				if a.musicTabDucked {
					a.musicAwaitURL = a.urls.MusicURL(a.sess.MusicTrack)
					a.musicAwaitSince = a.now() // stamp for the never-arrives timeout release
				}
			} else {
				// A ~stop: the room's handler stops the stream, so any await armed from a prior resume or /play is
				// orphaned (the awaited bytes are purged); clear it to keep the invariant "stopped => no await". The
				// tab-duck stays: ~stop doesn't change tab ownership, and the next real track re-arms the await.
				a.clearMusicAwait()
				// The stream is being stopped: drop the owner stamp so a later volume compose can't use a stale owner's
				// per-server level.
				a.musicOwnerKey = ""
			}
		// These arms only rebuild the roster from what the server pushed; there is no automatic /gas refetch (see
		// liveroster.go).
		case courtroom.EventCharsUpdated:
			a.charLower = nil     // names may have changed; rebuild lazily
			a.rebuildLiveRoster() // pre-snapshot fallback only
		case courtroom.EventAreasUpdated:
			a.rebuildLiveRoster()
			a.amICMNow = a.amICM() // the ARUP CM column may have changed — refresh the cached flag
			a.areaInfoSeq++        // invalidate the area-list word-wrap cache (screens.go areaWrapped)
		case courtroom.EventPlayersUpdated:
			a.rebuildLiveRoster()
			a.amICMNow = a.amICM() // a PU may have moved us to another area — refresh the cached flag  // server-pushed PR/PU: the live roster's primary source
			a.maybeFollowJump()    // follow-a-player (M3): trail the followed UID across areas
			a.recordAreaHistory()  // area history (M3): note our own area into the MRU list
		case courtroom.EventCharPicked:
			// Diagnostic: the server's PV echo, the char_id it actually assigned us. Compare with the CC char_id logged
			// on pick to tell a wire/server desync from a roster-display problem.
			if ev.Int >= 0 && ev.Int < len(a.sess.Chars) {
				a.pushDebug(fmt.Sprintf("server assigned char_id=%d (%s)", ev.Int, a.sess.Chars[ev.Int].Name))
			}
			a.enterCourtroom()
		case courtroom.EventOOC:
			if a.serverKey != "" && a.d.Prefs.Load().ServerIgnoreMatch(a.serverKey, ev.Name) {
				continue // #81: ignored player's OOC — drop it (room ignores OOC, so this is a clean skip)
			}
			// Received private message (Nyathena / Athena attribute the sender in the CT name as "[PM] [UID n]
			// <name>"): file it in its DM thread; it also stays in the OOC log.
			if uid, sender, ok := courtroom.ParsePMSender(ev.Name); ok {
				a.routeIncomingPM(uid, sender, ev.Text)
			}
			a.pushOOC(ev.Name+": "+ev.Text, ev.Name)
			if a.d.Prefs.Load().CallwordsOOC && !looksLikeAreaList(ev.Text) { // OOC callwords opt-in (default OFF); /ga roster never self-pings
				names := a.mentionNames()
				a.checkCallwords(ev.Text, names, isSelfName(ev.Name, names))
			}
			a.scanModActionOOC(ev.Name, ev.Text) // #60: optional ban/kick/mute feedback sound
		case courtroom.EventMessage:
			if ev.Message != nil {
				// Our own message echoed back: the server accepted it, so now the input clears (see noteOwnICEcho).
				// Above the ignore filter: it's our line.
				if a.sess != nil && ev.Message.CharID == a.sess.MyCharID {
					a.noteOwnICEcho()
				}
				if a.serverKey != "" && a.ignoreSpeaker(ev.Message) {
					continue // #81: ignored player — drop IC entirely (no log, no sprite/blip via room, no recording)
				}
				fr, fc := a.friendMessage(a.serverKey, ev.Message)
				force := a.d.Prefs.Load().ForceCharNames
				// The log's name resolves through the same char.ini rung as the plate (remoteIniShownameFor).
				line, speaker, bodyRuneStart, styles := icLogEntry(ev.Message, force, a.friendNick(ev.Message), a.remoteIniShownameFor)
				a.pushICStyled(line, ev.Message.TextColor, fr, fc, speaker, styles, bodyRuneStart)
				// Store the speaker's position (wire Side) per entry for the advanced pos filter.
				a.icLog[len(a.icLog)-1].pos = icPos(ev.Message)
				// This line has a message in the play queue behind it: the log is written on arrival, the courtroom
				// speaks it later, and ghost text (ghosttext.go) pairs the two by counting. Stamped only here because
				// the EventMessage branch is the only push that enqueues.
				a.icLog[len(a.icLog)-1].speech = true
				// Reactions: stamp the entry with its content-stable ref, computed from the raw wire fields (CharName +
				// marker-free text), not the display line, which bakes in client-local showname, nick and timestamp and
				// wouldn't match a peer's ref. A speaker line becomes a reaction target and the "last message"; float
				// any reaction this message carries.
				if speaker != "" {
					ref := courtroom.MakeReactionRef(ev.Message.CharName, courtroom.StripSpriteStyle(ev.Message.Message))
					a.icLog[len(a.icLog)-1].ref = ref
					a.lastReactRef, a.lastReactName = ref, speaker
				}
				if r, ok := courtroom.DecodeReactionMarker(ev.Message.Message); ok {
					a.onIncomingReaction(r)
				}
				a.noteShowname(ev.Message.CharName, ev.Message.Showname) // live-roster name cache
				a.notePairPartner(ev.Message)                            // #20: track each player's current pair
				if sn := ev.Message.SFXName; sn != "" && sn != "0" && sn != "1" {
					a.lastSFXName = sn // M11: remember the most-recent SFX for one-click "Mute last SFX"
				}
				if cn := ev.Message.CharName; cn != "" {
					a.lastBlipChar = cn // M11: remember the most-recent speaker for one-click per-char blip volume
				}
				if fr {
					a.signalFriend(a.serverName, ev.Message)
				}
				a.logDetailed(a.serverName, a.logSession, ev.Message) // detailed transcript (opt-in), this tab's own file
				a.noteEvidencePresented(ev.Message)
				names := a.mentionNames()
				a.checkCallwords(ev.Message.Message, names, isSelfName(ev.Message.CharName, names))
			}
		case courtroom.EventHP:
			// Direction decides the penalty sfx (set_hp_bar compares the new state with the previous one).
			if idx := ev.Int - 1; idx >= 0 && idx < len(a.hpPrev) {
				if ev.Int2 > a.hpPrev[idx] {
					a.playThemeSFX("hp_increased_sfx")
				} else if ev.Int2 < a.hpPrev[idx] {
					a.playThemeSFX("hp_decreased_sfx")
				}
				a.hpPrev[idx] = ev.Int2
			}
		case courtroom.EventWTCE:
			a.handleWTCE(ev.Text, ev.Int)
		case courtroom.EventVoiceCaps:
			// Voice just became available (VS_CAPS): point the user at the Voice button once per session.
			if a.sess != nil && a.sess.VoiceAvailable() && !a.voiceCapsAnnounced {
				a.voiceCapsAnnounced = true
				a.pushOOC("[Voice] This area supports voice chat — use the Voice button (bottom row / Extras) to join.", "")
			}
		case courtroom.EventVoiceAudio:
			a.voiceOnAudio(ev.Int, ev.Text) // decode + queue a peer's frame for the mixer
		case courtroom.EventVoicePeers:
			a.voiceReconcilePeers() // someone joined/left voice — sync the decoder set
		case courtroom.EventModcall:
			a.pushOOC("[MOD CALL] "+ev.Text, "")
			// AO2's ui_guard silences only the modcall alert (courtroom.cpp:4997-5001: sound and window flash); the OOC
			// line and auto-clip still land, so a guarded mod loses the interruption, never the report.
			if !a.modcallGuard {
				a.playThemeSFX("mod_call")
				a.ctx.FlashWindow()
			}
			a.signalModcall(a.serverName, ev.Text)            // desktop toast (opt-in)
			a.autoClipModcall(a.serverName, a.icLog, ev.Text) // freeze IC context for mods (opt-out)
		case courtroom.EventAuth:
			// AO2 surfaces auth transitions as CLIENT lines in the OOC log (on_authentication_state_received).
			switch {
			case ev.Int >= 1:
				a.pushOOC("CLIENT: Logged in as a moderator.", "")
				// Mod now: re-pull /getarea so IPIDs (mod-only, absent from PR/PU) reach the roster; the first
				// auto-pull can fire before this auto-login lands.
				if a.sess != nil {
					a.fetchRoster()
				}
			case ev.Int == 0:
				a.pushOOC("CLIENT: Login unsuccessful.", "")
			default:
				a.pushOOC("CLIENT: You were logged out.", "")
			}
		case courtroom.EventSetPos:
			a.sidePref = ev.Text // SP: the server moved us
		case courtroom.EventCase:
			a.pushOOC("[CASE] "+ev.Text, "")
			if enabled, roles := a.d.Prefs.Load().Casing(); enabled && ev.Int&roles != 0 {
				a.playThemeSFX("case_call")
				a.ctx.FlashWindow()
			}
		case courtroom.EventNotice:
			// BB is AO2's call_notice, shown in a modal (courtroom.cpp). Do both: the OOC log keeps it searchable, the
			// box makes it copyable and unmissable. serverNoticeInfo, so dismissing touches nothing; the connection is
			// fine.
			a.pushOOC("[SERVER] "+ev.Text, "")
			if strings.TrimSpace(ev.Text) != "" {
				a.openServerNotice(serverNoticeInfo, serverNoticeInfoTitle, a.serverName, ev.Text)
			}
			a.ctx.FlashWindow()
		case courtroom.EventMuted:
			// The server (un)muted us: log it and flash the window; the IC input shows a muted chip and refuses sends
			// while Session.Muted is set (AO2 set_mute disables ui_ic_chat_message).
			if ev.Int == 1 {
				a.pushOOC("[SERVER] You have been muted — you can't speak in-character.", "")
			} else {
				a.pushOOC("[SERVER] You have been unmuted.", "")
			}
			a.ctx.FlashWindow()
		case courtroom.EventEvidence:
			a.evidAsk = nil  // list replaced; thumbnail pacing resets
			a.evidScroll = 0 // a fresh list scrolls from the top
			if a.evidIdx >= len(a.sess.Evidence) {
				a.evidIdx = -1
			}
		case courtroom.EventEvidenceChanged:
			// One item changed (PE/EE/DE relay): keep the selection and scroll, only fix the cursor when a delete
			// removed or shifted it.
			if ev.Int2 == courtroom.EvidenceOpDelete {
				if a.evidIdx == ev.Int {
					a.evidIdx = -1
					a.evidEditing = false
					a.evidConflict = false
				} else if a.evidIdx > ev.Int {
					a.evidIdx--
				}
			} else if ev.Int2 == courtroom.EvidenceOpEdit && a.evidEditing && a.evidIdx == ev.Int {
				// Someone else edited the item we're mid-edit on: snapshot the incoming values and offer Keep mine /
				// Accept theirs instead of clobbering unsaved work.
				a.evidIncoming = a.sess.Evidence[ev.Int]
				a.evidConflict = true
			}
		case courtroom.EventDisconnect:
			a.connErr = ev.Text
			a.pushDebug("disconnected: " + ev.Text)
			// KK/KB/BD surface as "Kicked: ..." / "Banned: ...": play the matching sound (cooldown-gated) before the
			// session tears down; the audio device is app-level and keeps playing.
			switch {
			case strings.HasPrefix(ev.Text, "Kicked"):
				a.playModActionSFX(render.ModKick)
			case strings.HasPrefix(ev.Text, "Banned"):
				a.playModActionSFX(render.ModBan)
			}
			// Freeze the courtroom under the dialog (or tear down to the lobby with no room), then arm auto-reconnect.
			// EventDisconnect is only ever a KK/KB/BD kick/ban (session.go:696-701), so shouldAutoReconnect returns
			// false for both and no retry arms. Transport drops rearm via the pumpConnection paths. Return, not
			// continue: the freeze keeps a.sess/a.room alive, so further events in this batch would apply to the frozen
			// room.
			a.handleInvoluntaryDrop(ev.Text)
			return
		case courtroom.EventDebug:
			// Protocol-level diagnostics (unhandled headers, dropped MS): debug overlay only.
			a.pushDebug("server: " + ev.Text)
			continue
		}
		if a.recActive { // M16: tap the scene event stream for a replay recording
			a.recordEvent(ev)
		}
		a.bufferReplayEvent(ev) // instant-replay rolling buffer (no-op unless the opt-in pref is on)
		if a.room != nil {
			a.room.HandleEvent(ev)
		}
	}
}

// musicLogColor is the IC colour of the "has played a song" line: AO default white, as AO2-Client handle_song's
// p_color.
const musicLogColor = 0

// logMusicChange adds AO2/webAO's "<name> has played a song: <song>" (or "has stopped the music") line to the IC log
// when a real player changed the music; system/area music and area-name transfers don't log.
func (a *App) logMusicChange(ev courtroom.Event) {
	cid := ev.Int
	if a.sess == nil || cid < 0 || cid >= len(a.sess.Chars) {
		return // no player to attribute it to (system/area change or bad id)
	}
	action, song, ok := courtroom.MusicAction(ev.Text)
	if !ok {
		return // an area-name transfer, not a song
	}
	name := ev.Name // the MC packet's showname (field 2)
	if name == "" {
		name = a.sess.Chars[cid].Name // fall back to the character name
	}
	if name == "" {
		name = "Someone"
	}
	line := name + " " + action
	if song != "" {
		disp := song
		if a.d.Prefs.Load().ShowSongURL && ev.Text != "" {
			disp = ev.Text // opt-in: the full song URL / raw track instead of the name
		}
		line += ": " + disp
	}
	a.pushIC(line, musicLogColor, false, -1, "") // system line: no friend tint, no name-tint/pair
}

// icLogEntry builds what the IC log stores for an incoming message: the display line, the real speaker, the rune offset
// of the body in the line, and the body's colour runs. The head is resolved once so the offset and the line formatters
// can't disagree.
func icLogEntry(m *protocol.ChatMessage, forceChar bool, nick string, ini courtroom.IniShowname) (line, speaker string, bodyRuneStart int, styles []courtroom.StyleRun) {
	speaker = icSpeakerName(m, forceChar, ini)
	body, styles := icBodyStyled(m)
	if len(styles) == 1 && styles[0].Color == courtroom.ColorDefault && !styles[0].Bold && !styles[0].Italic {
		styles = nil // a plain message: keep the cheap single-colour log path
	}
	head := speaker + ": "
	if !forceChar && nick != "" {
		head = nick + " (" + speaker + "): "
	}
	return head + body, speaker, utf8.RuneCountInString(head), styles
}

// icBodyStyled returns an IC message's display body and its inline-colour runs. Emoji expand first (this commutes with
// markup stripping), then the typewriter strips markup and records the runs in one pass, the same parser the chatbox
// uses; styles is a single default run for a plain message.
func icBodyStyled(m *protocol.ChatMessage) (string, []courtroom.StyleRun) {
	raw := courtroom.ExpandInlineEmotes(courtroom.StripSpriteStyle(m.Message), inlineEmoteFor)
	tw := courtroom.NewTypewriter()
	tw.Start(raw)
	return tw.Text(), tw.Styles()
}

// icSpeakerName is the name an IC log line is prefixed with: the same chain the name plate draws
// (courtroom.DisplayName). Canon's log_chatmessage goes through get_showname too (courtroom.cpp:2528-2538); a second
// local rule would let the surfaces drift. Stored on the entry so per-speaker colours tint exactly that prefix.
func icSpeakerName(m *protocol.ChatMessage, forceChar bool, ini courtroom.IniShowname) string {
	return courtroom.DisplayName(m, forceChar, ini)
}

// icPos is an IC message's normalized position (wire Side), lowercased and trimmed so the pos filter and icEntry.pos
// agree; "" for a system line.
func icPos(m *protocol.ChatMessage) string {
	if m == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m.Side))
}

// friendNick returns the nickname set for m's speaker if they're a friend with one, independent of the friend-highlight
// signals (a nickname is a label, not an alert). Matches showname else character, the identity the MS wire carries.
func (a *App) friendNick(m *protocol.ChatMessage) string {
	if m == nil || a.serverKey == "" {
		return ""
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	_, _, nick := a.d.Prefs.Load().ServerFriendInfo(a.serverKey, name)
	return nick
}

// installAssetOrigin points this session's URL builder at origin, the one place rebuildAssetOrigin mints one. It also
// clears that origin's remembered misses: the manager keeps every exhausted chain for the whole session
// (assets.missSet), so reconnecting is the moment a repacked server gets a clean slate. Scoped to this origin because
// one Manager serves every open tab. Both jobs live here so an origin can't be installed without the flush.
func (a *App) installAssetOrigin(origin string) {
	a.urls = courtroom.NewURLBuilder(origin).WithCharCase(a.charCasingFor(origin))
	a.d.Manager.ForgetConclusiveMissesUnder(origin) // nil-safe: a headless App has no Manager and nothing to forget
}

// rebuildAssetOrigin wires the URL builder to local mounts or the server's
// asset URL, in that priority (the no-streaming checkbox wins).
func (a *App) rebuildAssetOrigin() {
	// Deferred and first: this function has four exits (local-mode, no-asset-URL error, rehearsal, normal tail), and
	// the mount layer needs the origin the body installs.
	defer a.applyMountLayer()
	// Push the configured mounts into the Manager's local:// overlay on every mounts/pref change (the one funnel
	// settings.go routes through), regardless of the legacy enabled flag: mounts are the recording-resolution base even
	// mid-streaming (the content report's "Local base"). A local-mode Manager ignores the overlay.
	if a.d.Manager != nil {
		if _, mounts := a.localAssets(); len(mounts) > 0 {
			a.d.Manager.SetLocalOverlay(assets.NewLocalFetcher(mounts))
		} else {
			a.d.Manager.SetLocalOverlay(nil)
		}
	}
	// Cache the local:// origin the evidence panel's "stream from server" toggle reads local images from ("" when no
	// mounts, so it falls back to streaming).
	if _, mounts := a.localAssets(); len(mounts) > 0 {
		a.evidLocalOrigin = assets.LocalOriginFor(mounts)
	} else {
		a.evidLocalOrigin = ""
	}
	if enabled, mounts := a.localAssets(); enabled && len(mounts) > 0 {
		// The origin helper, not a fetcher: this branch only read BaseURL, and the fetcher that serves these URLs is
		// already built (SetLocalOverlay or boot).
		localOrigin := assets.LocalOriginFor(mounts)
		a.installAssetOrigin(localOrigin)
		log.Printf("ui: local asset mode over %d mounts", len(mounts))
		return
	}
	origin := ""
	if a.sess != nil {
		origin = a.sess.AssetURL
	}
	if origin == "" {
		a.connErr = `server provided no asset URL — set Settings > Assets > Asset source to "Only use my folders"`
		return
	}
	a.installAssetOrigin(origin)
	if a.rehearsal {
		return // offline: no DNS warm, no manifest fetch
	}
	if host := hostOfURL(origin); host != "" {
		a.d.Client.PreResolve(context.Background(), strings.Split(host, ":")[0])
	}
	a.fetchManifestAsync()
}

// rehearsalBadge labels the offline mode in the courtroom viewport.
const rehearsalBadge = "REHEARSAL — offline, nothing sends"

// startRehearsal enters the offline cached-asset browser for a previously visited server: the remembered characters and
// asset origin, with the manager's network gate closed. Disconnect or any connect exits the mode.
func (a *App) startRehearsal(name, key string, info config.ServerWarmInfo) {
	a.parkActive()
	if !a.allocateTab() {
		return
	}
	a.resetSessionState()
	a.serverName = name + " (rehearsal)"
	a.serverKey = key
	a.connAt = time.Now()
	a.rehearsal = true
	a.d.Manager.SetOffline(true)
	a.sess = courtroom.NewRehearsalSession(info.Origin, info.Chars)
	a.rebuildAssetOrigin()
	a.refreshCharKeys()
	a.refreshShownameKeys()
	a.refreshICPhraseKeys()
	a.themeBound = info.Theme // rehearsal wears the server's bound theme too
	a.ensureThemeForSession()
	a.screen = ScreenCharSelect
	ch := a.notebookRes
	go func(k string) {
		if nb, err := config.LoadNotebook(k); err == nil {
			select {
			case ch <- notebookLoad{key: k, nb: nb}:
			default:
			}
		}
	}(key)
	a.pushDebug("rehearsal: offline browse of " + name + " (cached assets only)")
}

// pickCharacter routes a char-select pick: live sessions ask the server (CC -> PV -> EventCharPicked); rehearsal
// resolves locally, since no PV arrives offline.
func (a *App) pickCharacter(idx int) {
	// Log the char_id we put on the wire in CC, to compare with the server-assigned id logged at EventCharPicked.
	if idx >= 0 && idx < len(a.sess.Chars) {
		a.pushDebug(fmt.Sprintf("char pick → CC char_id=%d (%s)", idx, a.sess.Chars[idx].Name))
	}
	if a.sess.Rehearsal {
		a.sess.MyCharID = idx
		a.enterCourtroom()
		return
	}
	// Warm this character's char.ini as we send CC so the fetch overlaps the CC -> PV round-trip (/randomchar and
	// Ctrl+R never pre-warm on hover). warmCharINI's iniWarmed guard makes hover-then-click a no-op.
	if idx >= 0 && idx < len(a.sess.Chars) {
		a.warmCharINI(a.sess.Chars[idx].Name)
	}
	a.sess.PickCharacter(idx)
}

// extProfileFor returns the effective format profile (extensions.json text) for an asset host: a user-set per-server
// profile wins, else the official vanilla server gets the bundled example. "" means fetch the server's own
// extensions.json.
func (a *App) extProfileFor(host string) string {
	if p := a.d.Prefs.Load().ExtProfile(host); p != "" {
		return p
	}
	if a.isOfficialVanilla(host) {
		return assets.BundledVanillaManifestJSON
	}
	return ""
}

// isOfficialVanilla matches the AO official vanilla base by asset host, or the official vanilla server by connected
// server key, so joining it uses the vanilla format profile.
func (a *App) isOfficialVanilla(assetHost string) bool {
	h := strings.ToLower(assetHost)
	return strings.Contains(h, "attorneyoffline.de") ||
		strings.Contains(strings.ToLower(a.serverKey), "vanilla.aceattorneyonline.com")
}

// casingProbeResult carries an asset-casing probe's outcome back to the render thread.
type casingProbeResult struct {
	host   string
	casing uint8
}

// charCasingFor resolves the character-folder casing for origin's host: the manual pref as-is, or with CharCaseAuto the
// learned casing (lowercase until learned). Resolving Auto here keeps it out of the SDL-free URLBuilder.
func (a *App) charCasingFor(origin string) uint8 {
	c := a.d.Prefs.Load().AssetCharCase
	if c != courtroom.CharCaseAuto {
		return c // manual (incl. the lowercase default) — no probing, no learning
	}
	if host := hostOfURL(origin); host != "" {
		if learned, ok := a.charCaseLearned[host]; ok {
			return learned
		}
	}
	return courtroom.CharCaseLower // Auto, not yet learned → the safe default
}

// maybeProbeCasing starts a one-time casing probe for the current server, only with Auto casing, a known char list and
// an unprobed host.
func (a *App) maybeProbeCasing() {
	if a.d.Prefs.Load().AssetCharCase != courtroom.CharCaseAuto || a.casingProbing || a.sess == nil {
		return
	}
	origin := a.urls.Origin()
	host := hostOfURL(origin)
	if host == "" || !strings.HasPrefix(origin, "http") || a.casingProbedHost == host {
		return // not a network origin, or already handled this host this session
	}
	name := ""
	for i := range a.sess.Chars {
		if n := strings.TrimSpace(a.sess.Chars[i].Name); n != "" {
			name = n
			break
		}
	}
	if name == "" {
		return // no character names yet — try again next frame
	}
	a.casingProbing = true
	a.casingProbedHost = host
	go a.probeCasing(host, name, origin)
}

// probeCasing fetches character name's icon in each casing (lowercase, first-cap, title) and reports the first that
// exists, so Auto stays lowercase unless lowercase 404s. Two icon formats per casing; the burst is one-time per server
// (the sanctioned exception to one probe per asset). Background goroutine: touches only the result channel.
func (a *App) probeCasing(host, name, origin string) {
	ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
	defer cancel()
	winner := courtroom.CharCaseLower
	for _, cc := range []uint8{courtroom.CharCaseLower, courtroom.CharCaseFirstCap, courtroom.CharCaseTitle} {
		base := courtroom.NewURLBuilder(origin).WithCharCase(cc).CharIcon(name)
		found := false
		for _, ext := range []string{config.ExtPNG, config.ExtWebP} {
			if _, err := a.d.Manager.FetchRaw(ctx, base+ext); err == nil {
				found = true
				break
			}
		}
		if found {
			winner = cc
			break
		}
	}
	select {
	case <-a.casingRes:
	default:
	}
	a.casingRes <- casingProbeResult{host: host, casing: winner}
}

// pollCasingProbe lands a casing probe: record the casing for its host and, if still on that server, apply it to the
// URL builder. Render thread.
func (a *App) pollCasingProbe() {
	select {
	case res := <-a.casingRes:
		a.casingProbing = false
		if a.charCaseLearned == nil {
			a.charCaseLearned = map[string]uint8{}
		}
		a.charCaseLearned[res.host] = res.casing
		if hostOfURL(a.urls.Origin()) == res.host { // still on this server → apply now
			a.urls = a.urls.WithCharCase(res.casing)
		}
		a.pushDebug("asset casing auto-detect: " + res.host + " → " + charCaseName(res.casing))
	default:
	}
}

// charCaseName is a short label for a resolved casing (debug log).
func charCaseName(c uint8) string {
	switch c {
	case courtroom.CharCaseFirstCap:
		return "First cap"
	case courtroom.CharCaseTitle:
		return "Title"
	default:
		return "lowercase"
	}
}

// fetchManifestAsync seeds this host's learned formats from <origin>/extensions.json (webAO convention) so the first
// asset of each class costs one probe. Auto-detect (Settings) gates the network fetch; manual orders cover servers
// without a manifest.
func (a *App) fetchManifestAsync() {
	origin := a.urls.Origin()
	// Settings' re-seed hooks blank manifestFor to force a fresh fetch after a profile or auto-detect change; drop the
	// origin from the dedupe set so the guard below re-runs.
	if a.manifestFor == "" {
		delete(a.manifestSeeded, origin)
	}
	a.manifestFor = origin
	if a.originSeeded(origin) || !strings.HasPrefix(origin, "http") {
		return
	}
	a.markOriginSeeded(origin)
	host := hostOfURL(origin)
	// A per-host format profile (user-set, or the built-in official-vanilla example) applies even with auto-detect off;
	// it seeds instantly, so the first probe already uses the right formats, and wins over the global default for this
	// host.
	if profile := a.extProfileFor(host); profile != "" {
		if m, err := assets.ParseManifest([]byte(profile)); err == nil {
			n := m.SeedLearned(a.d.Prefs, host)
			a.d.Resolver.WarmFromPrefs()
			a.pushDebug(fmt.Sprintf("format profile: seeded %d classes for %s (instant)", n, host))
			return
		}
	}
	// The network fetch of the server's own extensions.json is gated on auto-detect; the per-server profile above is
	// not.
	if !a.d.Prefs.Load().AutoDetectFormats {
		return
	}
	mgr := a.d.Manager
	prefs := a.d.Prefs
	ch := a.manifestRes
	go func() {
		res := seedOriginFormats(mgr, prefs, origin)
		mf := manifestFetch{host: host, seeded: res.seeded, status: res.status, err: res.err}
		// pollManifest treats a nil err as "seeded". A host with no manifest or an unreadable one is not a seed, so
		// surface it as an error and let pollManifest log "learn per probe instead".
		if res.status != seedApplied && mf.err == nil {
			mf.err = network.ErrAssetNotFound
		}
		select {
		case <-ch:
		default:
		}
		ch <- mf
	}()
}

// originSeeded reports whether origin's extensions.json was already fetched and seeded this session. The empty origin
// is never seeded.
func (a *App) originSeeded(origin string) bool {
	if origin == "" {
		return false
	}
	_, ok := a.manifestSeeded[origin]
	return ok
}

// markOriginSeeded records origin as seeded this session. Render thread only (the map is unsynchronized).
func (a *App) markOriginSeeded(origin string) {
	if origin == "" {
		return
	}
	if a.manifestSeeded == nil {
		a.manifestSeeded = map[string]struct{}{}
	}
	a.manifestSeeded[origin] = struct{}{}
}

// seedStatus is the outcome of a diagnostic origin-seeding pass, so a content report can say why a host's formats were
// or weren't learned.
type seedStatus int

const (
	seedSkipped seedStatus = iota // not attempted (non-http origin, or autodetect off)
	seedApplied                   // extensions.json fetched + parsed; N classes seeded
	seedAbsent                    // no reachable extensions.json (host publishes none)
	seedFailed                    // reachable but unparseable / transport fault
)

// seedResult carries a seed pass's outcome back to its caller. seeded is the number of classes written to the learned
// table (0 unless seedApplied); it only feeds the report's status line, since probeRef reads the learned table
// directly.
type seedResult struct {
	status seedStatus
	seeded int
	err    error
}

// seedOriginFormats is the blocking core shared by the session connect path (fetchManifestAsync) and the diagnostic
// paths (content report, export loader) to seed a host's learned formats from <origin>/extensions.json. It runs off the
// render thread (FetchRaw blocks; RecordLearned/SeedLearned take the prefs mutex). The caller republishes the resolver
// table (WarmFromPrefs) on the render thread. absent (a clean 404) is distinct from failed (transport/parse fault) so
// the panel can say which.
func seedOriginFormats(mgr *assets.Manager, prefs *config.AssetPreferences, origin string) seedResult {
	if mgr == nil || prefs == nil || !strings.HasPrefix(origin, "http") {
		return seedResult{status: seedSkipped}
	}
	host := hostOfURL(origin)
	if host == "" {
		return seedResult{status: seedSkipped}
	}
	ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
	defer cancel()
	data, err := mgr.FetchRaw(ctx, origin+assets.ManifestFileName)
	if err != nil {
		if errors.Is(err, network.ErrAssetNotFound) {
			return seedResult{status: seedAbsent} // host ships no extensions.json
		}
		return seedResult{status: seedFailed, err: err}
	}
	m, perr := assets.ParseManifest(data)
	if perr != nil {
		return seedResult{status: seedFailed, err: perr}
	}
	return seedResult{status: seedApplied, seeded: m.SeedLearned(prefs, host)}
}

// pollManifest lands autodetect results: republish the resolver snapshot with the seeds and report on the debug lane.
func (a *App) pollManifest() {
	select {
	case res := <-a.manifestRes:
		a.noteManifestStatus(res.host, res.status)
		if res.err != nil {
			a.pushDebug("extensions.json: " + res.err.Error() + " — formats learn per probe instead")
			return
		}
		a.d.Resolver.WarmFromPrefs()
		a.pushDebug(fmt.Sprintf("extensions.json: seeded %d format classes for %s", res.seeded, res.host))
	default:
	}
}

// noteManifestStatus records whether a host published a usable extensions.json, for Settings -> Formats. Render thread
// only (the map is unsynchronized); bounded by the distinct asset hosts visited in a session.
func (a *App) noteManifestStatus(host string, st seedStatus) {
	if host == "" {
		return
	}
	if a.manifestStatus == nil {
		a.manifestStatus = map[string]seedStatus{}
	}
	a.manifestStatus[host] = st
}

// fontLoad is one font-override read result: file bytes in chain order plus a verdict for the settings status.
type fontLoad struct {
	names []string
	data  [][]byte
	note  string
}

// fontFileMaxBytes bounds one override font file (a CJK TTC is about 20 MiB).
const fontFileMaxBytes = 64 << 20

// applyFontConfig installs the IC/OOC override font chain from prefs through one resolver so launch and the live
// Settings toggles can't disagree: the dyslexia toggle first (embedded OpenDyslexic, no disk), else the manual
// font-path chain (read off-thread), else the built-in font.
func (a *App) applyFontConfig() {
	// The themed layout bakes each toggle label truncated against the chrome face (themeLayoutCache.toggleLabel) and
	// the cache key knows nothing about fonts, so changing the face must invalidate it or labels stay measured against
	// the old one. Theme applies already invalidate; this covers the font sources.
	defer a.invalidateThemeCanvases()

	switch fontChainSource(a.d.Prefs.Load().DyslexiaFont, a.d.Prefs.Load().FontOverridePaths) {
	case fontSourceDyslexia:
		a.ctx.SetFontChain([]string{dyslexiaFontName}, [][]byte{openDyslexicOTF})
		// Font-everywhere (opt-in): the same face drives the chrome; otherwise restore the embedded chrome.
		if a.d.Prefs.Load().FontEverywhere {
			a.ctx.SetChromeFont(openDyslexicOTF)
		} else {
			a.ctx.SetChromeFont(nil)
		}
		a.rasterText = "" // re-raster the visible message in the new font
	default:
		// Manual font paths win; else the active theme's own bundled .ttf; else the embedded font (loadFontChainAsync
		// clears on an empty string).
		paths := strings.TrimSpace(a.d.Prefs.Load().FontOverridePaths)
		if paths == "" {
			paths = a.themeFontFile
		}
		a.loadFontChainAsync(paths)
	}
}

// loadFontChainAsync reads the override font files off-thread (semicolon- or comma-separated, chain order, at most
// fontChainCap) and lands them on fontRes. An empty list clears the override immediately.
func (a *App) loadFontChainAsync(raw string) {
	paths := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	if len(paths) == 0 {
		a.ctx.SetFontChain(nil, nil)
		a.ctx.SetChromeFont(nil) // no override chain → the chrome is embedded again
		a.rasterText = ""        // re-raster the visible message with the embedded font
		return
	}
	if len(paths) > fontChainCap {
		paths = paths[:fontChainCap]
	}
	ch := a.fontRes
	go func() {
		var res fontLoad
		var failed []string
		for _, p := range paths {
			p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"`))
			if p == "" {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil || len(data) == 0 || len(data) > fontFileMaxBytes {
				failed = append(failed, filepath.Base(p))
				continue
			}
			res.names = append(res.names, filepath.Base(p))
			res.data = append(res.data, data)
		}
		switch {
		case len(res.data) == 0:
			res.note = "Font override: no readable font files — keeping the embedded font"
		case len(failed) > 0:
			res.note = fmt.Sprintf("Font chain: %s (skipped: %s)",
				strings.Join(res.names, " → "), strings.Join(failed, ", "))
		default:
			res.note = "Font chain: " + strings.Join(res.names, " → ")
		}
		select {
		case ch <- res:
		default: // a newer load superseded this one
		}
	}()
}

// pollFontChain lands override font bytes on the render thread: install the chain and force a chat re-raster.
func (a *App) pollFontChain() {
	select {
	case res := <-a.fontRes:
		// Precedence guard: if the dyslexia toggle flipped on while this chain loaded off-thread, the embedded font
		// already won.
		if len(res.data) > 0 && !a.d.Prefs.Load().DyslexiaFont {
			a.ctx.SetFontChain(res.names, res.data)
			// Font-everywhere (opt-in): the chain's primary face drives the chrome; otherwise restore the embedded
			// chrome. On a failed open the chrome keeps the embedded font and the note says so.
			if !a.d.Prefs.Load().FontEverywhere {
				a.ctx.SetChromeFont(nil)
			} else if !a.ctx.SetChromeFont(res.data[0]) {
				res.note += " — chrome font failed to open, whole-UI font stays embedded"
			}
		}
		a.rasterText = ""
		settings.statusLine = clampLine(res.note)
		a.pushDebug(res.note)
	default:
	}
}

// prefetchCharIcons warms the first screenfuls of icons; the rest load on demand from drawCharCell, since a huge burst
// would only shed itself from the low lane.
func (a *App) prefetchCharIcons() {
	if a.sess == nil || a.urls.Origin() == "" {
		return
	}
	for i, c := range a.sess.Chars {
		if i >= charIconWarmup {
			break
		}
		a.d.Manager.Prefetch(a.urls.CharIcon(c.Name), assets.AssetTypeCharIcon, network.PriorityLow) // AssetType: CharIcon
	}
}

func (a *App) enterCourtroom() {
	if a.sess == nil {
		return
	}
	// Fresh entry from char select: a wardrobe pick rides in as pendingIni; a plain pick starts clean and the side
	// waits on the new char.ini. Tab reactivation must not run these: activateTab calls buildRoom directly so the
	// parked iniswap and /pos override survive.
	a.iniChar = a.pendingIni
	a.pendingIni = ""
	a.sidePref = ""
	a.charDefaultSide = "" // a fresh character declares its own default; the old one must not outlive it
	// A fresh entry or character switch starts at the first emote. Reactivation skips this so each tab keeps its emote
	// selection; pollCharINI clamps rather than resets.
	a.emoteIdx, a.emoteRow = 0, 0
	// A fresh character must not inherit the previous character's last staged message, or buildRoom's stageRestoreMsg
	// re-stages the old speaker's sprite. Tab reactivation keeps it.
	a.sess.LastIC = nil
	a.buildRoom()
}
