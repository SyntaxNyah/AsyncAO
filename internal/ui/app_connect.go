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

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/hwid"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/presence"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

func (a *App) Connect(name, wsURL string) {
	a.cancelAutoReconnect() // a deliberate Join/Reconnect takes over from any pending auto-retry
	a.connectWith(name, wsURL, context.Background())
}

// friendlyConnError turns a raw dial error into plain lobby guidance (#9) — the raw
// error still goes to the debug log. Matches substrings of the lowercased error (Go's
// net / crypto-tls / websocket wording is stable enough); falls back to the raw text.
func friendlyConnError(wsURL string, err error) string {
	// A server that ANSWERED and refused comes first, ahead of every string sniff
	// below. It has to: the library's "expected handshake response status code 101
	// but got 503" matches the " status" arm further down, which tells the user the
	// server "may not be a WebSocket (AO2 2.11) server" — false, and it sends people
	// away from a server that would let them in if they read its message. The full
	// text goes to the notice box (connectWith); this is the one-line lobby view.
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
	// Proxy cases come FIRST and it matters. The websocket library wraps every
	// dial failure with its own name, so a proxy error falls into the "not a
	// WebSocket server" arm below and tells the user to blame a server that is
	// working perfectly.
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

// connectWith is Connect with a caller-chosen dial context. Manual joins pass
// context.Background() (Dial's full 10s budget); restore-on-launch passes a
// short timeout so a dead remembered server can't freeze boot for long.
func (a *App) connectWith(name, wsURL string, dialCtx context.Context) {
	a.connErr = ""                              // a fresh attempt clears any stale lobby error/reason
	a.deliberateClose = false                   // a fresh connection starts with no pending deliberate-close intent
	a.lastConnName, a.lastConnURL = name, wsURL // remember for one-click Reconnect on a drop/failure
	a.d.Prefs.SetLastServer(name, wsURL)        // persist for auto-connect / quick-connect on next launch
	a.lobby.pinging = false                     // leaving the lobby: don't let a dropped done-sentinel wedge re-ping
	a.parkActive()
	if !a.allocateTab() {
		return // connErr set; lobby shows it
	}
	a.resetSessionState()
	a.srvMeta.serverName = name
	a.srvMeta.serverKey = wsURL
	a.srvMeta.connAt = time.Now()
	// One-time wardrobe migration: the first server joined after the
	// per-server split inherits the old flat collection.
	a.d.Prefs.ClaimLegacyWardrobe(wsURL)
	a.refreshCharKeys()
	a.refreshShownameKeys()
	a.refreshICPhraseKeys()
	a.syncLoginBuffers() // settings/dialog boxes show this server's creds
	// Per-server theme binding: this server always uses that theme.
	a.srvMeta.themeBound = a.d.Prefs.Load().ServerWarmInfoFor(wsURL).Theme
	a.ensureThemeForSession()
	// Case notebook: per-server pins load off-thread, land via the poll
	// (the payload carries the key so it routes even after a tab switch).
	ch := a.notebookRes
	go func(key string) {
		if nb, err := config.LoadNotebook(key); err == nil {
			select {
			case ch <- notebookLoad{key: key, nb: nb}:
			default:
			}
		}
	}(wsURL)
	// TLS: default OFF accepts self-signed wss certs (most community AO servers);
	// the power-user Security toggle flips on strict verification.
	conn, err := protocol.Dial(dialCtx, wsURL, protocol.DialOptions{
		SkipTLSVerify: !a.d.Prefs.Load().ValidateTLSCerts,
		Origin:        a.d.Prefs.Load().WSOrigin, // power-user: servers that allowlist their web client's origin ("" = none)
	})
	if err != nil {
		a.connErr = friendlyConnError(wsURL, err) // #9: human guidance, not a raw Go error
		a.debug.push("connect failed: " + err.Error())
		// A refusal the server EXPLAINED gets the notice box, same as a BD reason:
		// the explanation is how you get in (a Discord invite to ask for a whitelist
		// entry, an access code), and the lobby line is one row that cannot hold it.
		// Snapshot the name before the teardown below — serverName is on sessionState.
		noticeServer := a.srvMeta.serverName
		var de *protocol.DialError
		refused := errors.As(err, &de) && de.Body != ""
		a.closeActiveTab()
		a.screen = ScreenLobby
		if refused {
			a.openServerNotice(serverNoticeRemoved, a.connErr, noticeServer, de.Body)
		}
		return
	}
	a.link.conn = conn
	// A pending carry from THIS server's own drop (a dialog Reconnect click or
	// an auto-retry) rejoins the fresh session now that the dial actually
	// succeeded — placed AFTER the dial, not right after resetSessionState
	// above, so a failed attempt (a retried auto-reconnect) never burns the
	// one-shot slot before the connection that earns it. No-ops (and clears
	// the slot) unless wsURL matches the server the carry was captured from.
	a.applySessionCarryIfPending(wsURL)
	// Wake the render loop the instant a packet lands (experimental
	// event-driven loop; a queued no-op event otherwise — the wake is never
	// treated as user input in either mode).
	conn.SetNotify(PushWake)
	a.pkts.reset() // a fresh connection starts a clean packet history
	a.pktConn = conn
	a.link.sess = courtroom.NewSession(func(p protocol.Packet) error {
		if conn == a.link.conn { // record only the ACTIVE connection's sends (a parked tab's Ping won't match)
			a.recordPacket(p, true)
		}
		return conn.Send(context.Background(), p)
	}, hdid(wsURL))
	// Prime the background keepalive goroutine immediately, so the CH ping flows from
	// the moment we connect even if the first render frame is delayed (or the window
	// is already minimized). pumpConnection refreshes it as MyCharID changes.
	conn.SetKeepalive(a.link.sess.KeepalivePacket())
	a.screen = ScreenCharSelect
}

// pumpTabRestore reconnects one remembered tab per frame (restore-on-launch),
// spreading the blocking dials so they never pile into one boot freeze, each on
// a short dial budget. A no-op (single length check) once the queue is empty,
// so it costs nothing after startup and nothing at all when the feature is off.
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

// collectOpenTabs snapshots the currently open server tabs (name + ws URL) for
// restore-on-launch: the live active tab from the session fields, parked tabs
// from their stored state. Skips rehearsal/dead/blank-URL slots and dedups by
// URL. Pure over App state (no prefs/IO) so it unit-tests directly. activeTab
// == -1 (sitting on the lobby) simply contributes no "active" entry.
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
			add(a.srvMeta.serverName, a.srvMeta.serverKey, false, a.srvMeta.rehearsal)
		} else {
			add(t.state.srvMeta.serverName, t.state.srvMeta.serverKey, t.dead, t.state.srvMeta.rehearsal)
		}
	}
	return out
}

// RememberOpenTabs persists the open tabs for next launch's restore — called
// once at shutdown (before the final SaveNow). No-op unless restore-on-launch
// is enabled, so the feature is truly zero-cost when off.
func (a *App) RememberOpenTabs() {
	if !a.d.Prefs.Load().RestoreTabs {
		return
	}
	a.d.Prefs.SetOpenTabs(a.collectOpenTabs())
}

// Disconnect tears the ACTIVE session down (its tab closes; other tabs
// keep running) and returns to the lobby.
func (a *App) Disconnect() {
	// A full teardown always ends any frozen-courtroom dialog: the dialog only makes
	// sense while a session is drawn-but-dead, and Disconnect is about to remove it
	// (→ lobby). The dialog's own Back-to-lobby / Reconnect already clear it before
	// calling here, but a FUTURE path that reaches Disconnect with the dialog still
	// up (a quit-while-frozen, say) must not strand a fenced modal over an empty
	// lobby (the emoji-picker freeze class). Cheap: a struct assignment on a rarely-
	// set field. Guards the invariant disconnectDlg.open ⇒ screen == Courtroom.
	if a.dialogs.disconnectDlg.open {
		a.dialogs.disconnectDlg = disconnectDialog{}
	}
	a.cancelAutoReconnect() // teardown cancels any pending retry; the EventDisconnect path re-arms after
	if a.link.conn != nil {
		a.link.conn.Close()
	}
	// The server's area music shouldn't outlive the connection (manual
	// disconnect, kick, or a dropped socket all land here).
	if a.d.Audio != nil {
		a.d.Audio.StopMusic()
		a.musicTabDucked = false        // a fresh session's music must start audible, not ducked
		a.musicAwaitURL = ""            // stream stopped: drop any pending un-duck (invariant: stopped ⇒ no await)
		a.musicAwaitSince = time.Time{} // and its timeout stamp
	}
	a.stopVoiceAudio() // free the voice capture/playback devices with the connection
	a.link.voiceJoined, a.link.voiceMicOn = false, false
	// Rehearsal mode ends with the session: reopen the network gate.
	if a.srvMeta.rehearsal {
		a.d.Manager.SetOffline(false)
	}
	// Notebook: flush pending pins off-thread.
	if a.notebook != nil {
		go func(nb *config.Notebook) { _ = nb.Flush() }(a.notebook)
	}
	if a.raster.msRaster != nil {
		a.raster.msRaster.Destroy()
	}
	if a.raster.glyphCache != nil {
		a.raster.glyphCache.Purge() // free the #M5 white-glyph textures
	}
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = nil
		a.d.Viewport.OnFrameShown = nil // #17: no live room owns the viewport now
		a.d.Viewport.PurgePostFX()      // #10: free the cached retro-overlay textures
	}
	// Remember which server this was so the lobby's Reconnect button and
	// auto-reconnect target the session that actually dropped — correct even
	// with other tabs still connected (lastConn* is global, so set-at-connect
	// alone would hold whatever was connected most recently). Guarded on a live
	// socket so rehearsal / an already-dead tab can't claim a phantom target;
	// connectWith seeds lastConn* for the dial-FAILURE case (no session here).
	if a.link.conn != nil {
		a.lastConnName, a.lastConnURL = a.srvMeta.serverName, a.srvMeta.serverKey
	}
	a.closeActiveTab()
	a.resetSessionState()
	a.lobby.selServer, a.lobby.descLines = -1, nil
	// Drain in-flight fetches so stale payloads can't land later (polls
	// also key-guard, this just frees the slots).
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
	// Consume the deliberate-close intent: it applied only to THIS teardown. A
	// later unexpected drop on a fresh connection must be treated as unexpected.
	a.deliberateClose = false
}

// applyWindowSize resizes the window to a clamped, recentered size and persists
// it, so a too-big or off-screen window snaps back into view. Leaves fullscreen
// first (a windowed resize is meaningless while fullscreen).
func (a *App) applyWindowSize(w, h int) {
	if a.d.Prefs.Load().WindowFull {
		a.ctx.SetWindowFullscreen(false)
		a.d.Prefs.Update(func(sn *config.Settings) { sn.WindowFull = false })
	}
	// A MAXIMIZED window swallows the resize whole (#35): SDL_SetWindowSize has no
	// maximized guard, and on Windows the backend's SetWindowPos runs against an
	// HWND that still carries WS_MAXIMIZE, so Default / Fit to screen / Custom /
	// Theme's design size all appeared to do nothing. Drop out of maximized first.
	a.ctx.UnmaximizeWindow()
	uw, uh := a.ctx.WindowDisplayUsable()
	w, h = config.ClampWindowSize(w, h, uw, uh)
	a.ctx.ResizeWindow(w, h)
	a.d.Prefs.SetWindowSize(w, h)
}

// themeDesignWindowSize reports the window size the ACTIVE theme was drawn for —
// its courtroom_design.ini `courtroom` rect — and whether it declares one.
// AO2-Client charselect.cpp:83 on the char-select screen). Matching the window to
// `viewport` must be declared too, because that is the SECOND half of
// themeLayout()'s own validity test: a theme with a courtroom rect but no
// viewport draws through the CLASSIC layout, so its canvas is not what is on
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

// themeDesignSizeOffer reports the theme's design size and whether the MANUAL
// "Theme's design size" affordance should be offered at all.
// It exists so both halves of #35 agree about when a theme canvas is meaningful.
func (a *App) themeDesignSizeOffer() (int, int, bool) {
	w, h, ok := a.themeDesignWindowSize()
	return w, h, ok && a.d.Prefs.Load().ThemeLayoutOn
}

// themeDesignWindowInset is the extra window HEIGHT a snap-to-canvas has to ask for
// so the canvas itself still lands 1:1: the app-chrome band (#14, chrometop.go) sits
// above the design canvas, so a window exactly designH tall would leave the canvas
// designH-band and force it to scale down — quietly breaking the one promise this
// affordance makes.
// So it costs no display anything it would otherwise have had, and it is the only
// version that can deliver the 1:1 this affordance promises.
const themeDesignWindowInset = menuBarH + tabBarH

// themeDesignWindowTarget is the window size a snap-to-canvas asks for: the theme's
// declared canvas plus the chrome band above it.
func (a *App) themeDesignWindowTarget() (int, int, bool) {
	w, h, ok := a.themeDesignWindowSize()
	if !ok {
		return 0, 0, false
	}
	return w, h + int(themeDesignWindowInset), true
}

// applyThemeDesignWindowSize snaps the window to the active theme's design size,
// reporting whether there was one to snap to. The MANUAL half of #35 (Settings →
// Window, and any future menu entry): an explicit user command, so unlike the
// automatic path it deliberately does not second-guess a maximized or fullscreen
// window — applyWindowSize leaves both states, clamps and persists.
func (a *App) applyThemeDesignWindowSize() bool {
	w, h, ok := a.themeDesignWindowTarget()
	if !ok {
		return false
	}
	a.applyWindowSize(w, h)
	return true
}

// maybeResizeToThemeDesign is the AUTOMATIC half of #35: it consumes the one-shot
// armed at the user-initiated theme-pick call sites and snaps the window to the
// freshly applied theme's canvas, so importing a theme delivers stock-AO2 1:1
// with zero user action. `gen` is the themeGen of the apply that just landed.
// The one-shot is why this can't key off "the theme name changed": pollThemeApply
func (a *App) maybeResizeToThemeDesign(gen uint64) {
	if a.themeResizeArmGen == 0 {
		return
	}
	if gen != a.themeResizeArmGen {
		if gen > a.themeResizeArmGen {
			// Outraced: a later apply has landed, so the armed one is now barred from
			// ever landing (see the newest-wins guards above). Disarm — the snap is
			// lost, which is the safe failure, but the arming must not linger.
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
	// ClampWindowSize is pure, so clamping here to compare and again inside
	// applyWindowSize yields the identical target — a 1918×982 theme on a 1366×768
	// laptop lands on the same downscaled size either way.
	uw, uh := a.ctx.WindowDisplayUsable()
	tw, th := config.ClampWindowSize(w, h, uw, uh)
	if cw, ch := a.ctx.WindowSize(); cw == tw && ch == th {
		return
	}
	a.applyWindowSize(w, h)
}

// armBootThemeResize gives the ONE boot apply named by gen the same window snap
// a deliberate theme pick gets — but only on the single launch where the #29
// theme-fit default move actually ran for this preferences file.
func (a *App) armBootThemeResize(gen uint64) {
	if a.d.Prefs.ThemeFitDefaultJustMoved() {
		a.themeResizeArmGen = gen
	}
}

// fitWindowToScreen sizes the window to (most of) the display's usable area and
// recenters — the one-click rescue for a window bigger than the monitor.
func (a *App) fitWindowToScreen() {
	uw, uh := a.ctx.WindowDisplayUsable()
	if uw <= 0 || uh <= 0 {
		a.applyWindowSize(config.DefaultWindowW, config.DefaultWindowH)
		return
	}
	a.applyWindowSize(uw, uh) // ClampWindowSize keeps it within the usable bounds
}

// applyFullscreen toggles borderless fullscreen and persists it. Leaving
// fullscreen snaps back to a clamped windowed size so a stranded one can't return.
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

// toggleFullscreen flips fullscreen — shared by the F11 escape and the Settings
// checkbox.
func (a *App) toggleFullscreen() { a.applyFullscreen(!a.d.Prefs.Load().WindowFull) }

// effectiveShowname is the name outgoing messages carry: the courtroom
// override box when filled, the persisted Settings showname otherwise.
func (a *App) effectiveShowname() string {
	if s := strings.TrimSpace(a.compose.shownameOverride); s != "" {
		return s
	}
	return a.d.Prefs.Load().Showname
}

// ensureNameOpts (re)builds the cached name-picker dropdown options — a leading
// "▾" (the no-op the closed control shows) then the saved showname presets —
// rebuilt only when the preset count changes, so the per-frame chat row reads a
// cached slice instead of copying the preset list every draw.
func (a *App) ensureNameOpts() {
	if n := a.d.Prefs.Load().ShownameCount(); a.nameOpts == nil || n != a.nameOptsLen {
		a.nameOptsLen = n
		a.nameOpts = append([]string{"▾"}, a.d.Prefs.Load().ShownameList()...)
	}
}

// pickNameDropdown draws the tiny ▾ showname picker at r and returns the chosen
// preset (or "" if nothing was picked). Shown only when presets exist; reused by
// the IC showname box and the OOC name box.
func (a *App) pickNameDropdown(id string, r sdl.Rect) string {
	if len(a.nameOpts) <= 1 {
		return ""
	}
	if next, changed := a.ctx.Dropdown(id, r, a.nameOpts, 0); changed && next > 0 {
		return a.nameOpts[next]
	}
	return ""
}

// updatePresence pushes (or clears) the Discord activity from the user's
// per-field choices. Cheap — a channel swap; the presence worker owns all
// I/O and silently idles when Discord isn't running.
func (a *App) updatePresence() {
	if a.d.Presence == nil {
		return
	}
	dp := a.d.Prefs.Load().DiscordRPC
	if !dp.Enabled {
		a.d.Presence.Clear()
		return
	}
	if a.link.sess == nil {
		// In the lobby / server list — keep "Playing AsyncAO" up (the user asked for
		// presence to persist there too, not vanish until you join a room). No
		// server/character/showname applies yet, and no elapsed timer (it'd reset on
		// every connect); just a neutral line.
		a.d.Presence.Set(presence.Activity{Details: "In the lobby"})
		return
	}
	act := presence.Activity{Start: a.srvMeta.connAt, Details: "In court"}
	if dp.ShowServer && a.srvMeta.serverName != "" {
		act.Details = "On " + a.srvMeta.serverName
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
	if dp.ShowArea && a.srvMeta.curArea != "" {
		parts = append(parts, "— "+a.srvMeta.curArea)
	}
	act.State = strings.Join(parts, " ")
	a.d.Presence.Set(act)
}

// hdid is the device ID AO servers key bans on, for the server at wsURL. It
// derives from stable per-machine/account roots rather than the hostname, and is
// bound to that ONE server (hwid.For), so the id a server receives cannot be
// replayed against another — see internal/hwid for the roots, hashing, address
// normalisation and graceful fallback.
func hdid(wsURL string) string { return hwid.For(wsURL) }

// pumpConnection drains incoming packets into the session each frame.
func (a *App) pumpConnection() {
	if a.link.conn == nil || a.link.sess == nil {
		return
	}
	// Keep the connection's background keepalive goroutine primed with the current
	// CH payload (it carries MyCharID, which only changes on the render thread). The
	// goroutine — NOT this loop — actually sends the ping every keepaliveInterval, so
	// it keeps flowing even while the window is minimized and Windows has stalled this
	// render loop (the old per-frame send died there, and the server idle-dropped us).
	a.link.conn.SetKeepalive(a.link.sess.KeepalivePacket())
	// Half-dead write side (#7a): Session.reply records the FIRST failed outgoing
	// write into sendErr and then silently swallows every later packet. A non-nil
	// SendErr means our writes are going into a dead socket (the keepalive above,
	// or any IC send) — surface it and tear down, then auto-reconnect (a write
	// failure is a transport drop, never a ban/kick, so it's not deliberate).
	if err := a.link.sess.SendErr(); err != nil {
		reason := "connection lost: " + err.Error()
		a.connErr = reason
		a.debug.push("disconnected: " + reason)
		a.handleInvoluntaryDrop(reason) // freeze under the dialog (or plain teardown) + arm auto-reconnect
		return
	}
	for {
		// A handled packet can tear the session down mid-drain: a server
		// kick/ban (KK/KB/BD → EventDisconnect) calls Disconnect(), whose
		// resetSessionState() zeroes conn/sess. Re-check before the select
		// touches a.conn.Incoming()/a.sess.HandlePacket again, or the next
		// iteration nil-derefs (conn.go:73). The plain socket-close path
		// returns immediately below, so only the kick/ban packet hit this.
		if a.link.conn == nil || a.link.sess == nil {
			return
		}
		select {
		case p, ok := <-a.link.conn.Incoming():
			a.uiDirty = true // packets (or the drop below) change UI-visible state — redraw-worthy damage
			if !ok {
				// The Incoming channel closed: a genuine transport drop (Wi-Fi
				// blip, server restart, or a read error). This branch is NOT
				// reached on a deliberate
				// Disconnect (that nils a.conn first, so pumpConnection early-
				// returns) — so unless deliberateClose was set by a refactor
				// mid-teardown, this is exactly the case auto-reconnect exists
				// for (#1: previously this path only Disconnected and never
				// rearmed a retry).
				reason := "connection closed"
				if err := a.link.conn.Err(); err != nil {
					reason = err.Error()
				}
				a.connErr = reason
				a.debug.push("disconnected: " + reason)
				a.handleInvoluntaryDrop(reason) // freeze under the dialog (or plain teardown) + arm auto-reconnect
				return
			}
			a.lastPktHdr, a.lastPktAt = p.Header, time.Now()
			a.notePktConn() // reset the packet ring if the active conn changed (tab switch / reconnect)
			a.recordPacket(p, false)
			a.handleSessionEvents(a.link.sess.HandlePacket(p))
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
			a.d.Prefs.RememberServerOrigin(a.srvMeta.serverKey, a.link.sess.AssetURL)
			names := make([]string, len(a.link.sess.Chars))
			for i := range a.link.sess.Chars {
				names[i] = a.link.sess.Chars[i].Name
			}
			a.d.Prefs.RememberServerChars(a.srvMeta.serverKey, names)
			a.autoLoginOnReady()
		case courtroom.EventBackground:
			// Remember it for next visit's pre-warm; the room still
			// consumes the event below (no continue).
			a.d.Prefs.RememberServerBackground(a.srvMeta.serverKey, ev.Text)
			if ev.Wipe {
				// The overlays AO2's set_background teardown also drops, which live on
				// the App rather than in the Scene: the WT/CE splash and testimony badge
				// (ui_vp_testimony->stopPlayback) and the evidence popup
				// (ui_vp_evidence_display->reset). Deliberate deviation: AO2 stops the
				// testimony overlay on EVERY BN, including the no-pos form; we keep it
				// inside the wipe branch so a plain /bg behaves exactly as it does today.
				a.courtxtra.wtceName, a.courtxtra.testimonyOn, a.evid.evShowImg = "", false, ""
			}
		case courtroom.EventMusic:
			// Log "<name> has played a song: <song>" in the IC log like webAO and
			// AO2-Client (handle_song). The room still plays the track below.
			a.logMusicChange(ev)
			a.noteMusicHistory(ev) // M12 slice: session "recently played" history
			// Stamp when this track began, for a cross-tab resume seek (a later
			// tab switch computes elapsed = now - start). A ~stop clears MusicTrack,
			// so the stamp is harmless then (resume only reads it when a track
			// remains). See musicStartedAt.
			a.musicTab.musicStartedAt = a.now()
			// A LIVE music change on the ACTIVE tab (a DJ /play) makes this tab the
			// audible owner of the single stream — but only ONCE the requested track is
			// actually what the mixer is playing. If the stream is still ducked-foreign
			// (a BACKGROUNDED tab's previously-ducked song), un-ducking NOW — before the
			// room's EventMusic handler (line ~3527) even issues PlayMusic→PrefetchExact —
			// would make that FOREIGN track audible for the seconds its replacement takes
			// to download. So KEEP the duck and record the awaited URL instead;
			// settleAwaitedMusic lifts it the frame the new track's bytes land (the mixer
			// swaps in startMusic, CurrentMusicURL catches up, ~16ms, imperceptible).
			if a.link.sess.MusicTrack != "" {
				// This active tab is now the audible owner of the single stream (its DJ
				// /play is what the room's handler plays below). Stamp the owner so its
				// per-server music volume governs the stream — including when the user later
				// backgrounds THIS tab under MusicAcrossTabs ON and the stream keeps playing
				// its song under a different active tab. See musicOwnerKey.
				a.musicOwnerKey = a.srvMeta.serverKey
				if a.musicTabDucked {
					a.musicAwaitURL = a.link.urls.MusicURL(a.link.sess.MusicTrack)
					a.musicAwaitSince = a.now() // stamp for the never-arrives timeout release
				}
			} else {
				// A ~stop (MusicTrack cleared): the room's handler will StopMusic the
				// render stream below (courtroom ~stop), so any await armed from a prior
				// resume or /play is now orphaned — the awaited bytes are purged and can
				// never become current, so settleAwaitedDuck could never match and only
				// the 10s timeout (frozen while minimized) would release it. Clear it
				// here so this stop site honors the same "stopped ⇒ no await" invariant
				// the local stop button, park, close, disconnect and streaming-off sites
				// already enforce. The tab-duck stays as-is: an incoming ~stop doesn't
				// change tab ownership of the (now-silent) stream, and the next real
				// track re-arms the await through the branch above.
				a.clearMusicAwait()
				// The stream is being stopped (silent): drop the owner stamp so a later
				// volume compose can't scale a (now-nonexistent) stream by a stale owner's
				// per-server level. Harmless if already "" ; the next real track re-stamps.
				a.musicOwnerKey = ""
			}
		// NOTE: these three arms used to call maybeRefetchRoster, which
		// sent a /gas back at the server on a 3 s debounce. That automatic tier is
		// deleted — see the header comment in liveroster.go for the field report, the
		// per-site verdict and the trade-off. All three now do nothing but rebuild the
		// roster from what the server PUSHED.
		case courtroom.EventCharsUpdated:
			a.charsel.charLower = nil // names may have changed; rebuild lazily
			a.rebuildLiveRoster()     // pre-snapshot fallback only
		case courtroom.EventAreasUpdated:
			a.rebuildLiveRoster()
			a.srvMeta.amICMNow = a.amICM() // the ARUP CM column may have changed — refresh the cached flag
			a.areaTab.areaInfoSeq++        // invalidate the area-list word-wrap cache (screens.go areaWrapped)
		case courtroom.EventPlayersUpdated:
			a.rebuildLiveRoster()
			a.srvMeta.amICMNow = a.amICM() // a PU may have moved us to another area — refresh the cached flag  // server-pushed PR/PU: the live roster's primary source
			a.maybeFollowJump()            // follow-a-player (M3): trail the followed UID across areas
			a.recordAreaHistory()          // area history (M3): note our own area into the MRU list
		case courtroom.EventCharPicked:
			// #88 diagnostic: the server's PV echo — the char_id it actually
			// assigned us. If this differs from the CC char_id logged on pick, the
			// desync is on the wire/server; if it matches but the player list still
			// shows the wrong char, it's in how the roster reports it.
			if ev.Int >= 0 && ev.Int < len(a.link.sess.Chars) {
				a.debug.push(fmt.Sprintf("server assigned char_id=%d (%s)", ev.Int, a.link.sess.Chars[ev.Int].Name))
			}
			a.enterCourtroom()
		case courtroom.EventOOC:
			if a.srvMeta.serverKey != "" && a.d.Prefs.Load().ServerIgnoreMatch(a.srvMeta.serverKey, ev.Name) {
				continue // #81: ignored player's OOC — drop it (room ignores OOC, so this is a clean skip)
			}
			// Received private message (Nyathena / Athena attribute the sender in the
			// CT name as "[PM] [UID n] <name>"): file it in its DM thread. It also
			// stays in the OOC log below, so a miss loses nothing.
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
				// Our own message echoed back — the server accepted it, so NOW
				// the input clears (keep-until-echo, see noteOwnICEcho). Above
				// the ignore filter: it's our line, filtering is display-only.
				if a.link.sess != nil && ev.Message.CharID == a.link.sess.MyCharID {
					a.noteOwnICEcho()
				}
				if a.srvMeta.serverKey != "" && a.ignoreSpeaker(ev.Message) {
					continue // #81: ignored player — drop IC entirely (no log, no sprite/blip via room, no recording)
				}
				fr, fc := a.friendMessage(a.srvMeta.serverKey, ev.Message)
				force := a.d.Prefs.Load().ForceCharNames
				// The log's name resolves through the SAME char.ini rung the plate
				// uses (remoteIniShownameFor) — one chain, two surfaces.
				line, speaker, bodyRuneStart, styles := icLogEntry(ev.Message, force, a.friendNick(ev.Message), a.remoteIniShownameFor)
				a.pushICStyled(line, ev.Message.TextColor, fr, fc, speaker, styles, bodyRuneStart)
				// Store the speaker's courtroom position (wire Side) per entry so the
				// advanced pos filter (#129) can narrow the log to one position.
				a.chatlog.icLog[len(a.chatlog.icLog)-1].pos = icPos(ev.Message)
				// This line has a message in the play queue behind it: the log is written
				// on ARRIVAL, the courtroom speaks it later, and the ghost-text option
				// (ghosttext.go) pairs the two by counting. Stamped here, next to the
				// reaction ref, because the EventMessage branch is the ONLY push that
				// enqueues — every other pushIC call site is a system line that is true
				// the instant it appears.
				a.chatlog.icLog[len(a.chatlog.icLog)-1].speech = true
				// #2 reactions: stamp this entry with its CONTENT-STABLE ref, computed from the
				// RAW wire fields (CharName + marker-free text) — NOT the display line, which
				// bakes in client-local showname / nick / timestamp and so wouldn't match a
				// peer's ref. A real speaker line becomes a reaction target; track it as the
				// "last message" the React button snapshots, and float any reaction this very
				// message carries (the reactor piggybacks on their next line).
				if speaker != "" {
					ref := courtroom.MakeReactionRef(ev.Message.CharName, courtroom.StripSpriteStyle(ev.Message.Message))
					a.chatlog.icLog[len(a.chatlog.icLog)-1].ref = ref
					a.reactUI.lastReactRef, a.reactUI.lastReactName = ref, speaker
				}
				if r, ok := courtroom.DecodeReactionMarker(ev.Message.Message); ok {
					a.onIncomingReaction(r)
				}
				a.noteShowname(ev.Message.CharName, ev.Message.Showname) // live-roster name cache
				a.notePairPartner(ev.Message)                            // #20: track each player's current pair
				if sn := ev.Message.SFXName; sn != "" && sn != "0" && sn != "1" {
					a.roster.lastSFXName = sn // M11: remember the most-recent SFX for one-click "Mute last SFX"
				}
				if cn := ev.Message.CharName; cn != "" {
					a.roster.lastBlipChar = cn // M11: remember the most-recent speaker for one-click per-char blip volume
				}
				if fr {
					a.signalFriend(a.srvMeta.serverName, ev.Message)
				}
				a.logDetailed(a.srvMeta.serverName, a.srvMeta.logSession, ev.Message) // detailed transcript (opt-in), this tab's own file
				a.noteEvidencePresented(ev.Message)
				names := a.mentionNames()
				a.checkCallwords(ev.Message.Message, names, isSelfName(ev.Message.CharName, names))
			}
		case courtroom.EventHP:
			// Direction decides the penalty sfx (set_hp_bar compares the
			// new state against the previous one).
			if idx := ev.Int - 1; idx >= 0 && idx < len(a.courtxtra.hpPrev) {
				if ev.Int2 > a.courtxtra.hpPrev[idx] {
					a.playThemeSFX("hp_increased_sfx")
				} else if ev.Int2 < a.courtxtra.hpPrev[idx] {
					a.playThemeSFX("hp_decreased_sfx")
				}
				a.courtxtra.hpPrev[idx] = ev.Int2
			}
		case courtroom.EventWTCE:
			a.handleWTCE(ev.Text, ev.Int)
		case courtroom.EventVoiceCaps:
			// Voice just became available here (VS_CAPS) — point the user at the
			// Voice button once per session (also confirms VS_CAPS reached us).
			if a.link.sess != nil && a.link.sess.VoiceAvailable() && !a.link.voiceCapsAnnounced {
				a.link.voiceCapsAnnounced = true
				a.pushOOC("[Voice] This area supports voice chat — use the Voice button (bottom row / Extras) to join.", "")
			}
		case courtroom.EventVoiceAudio:
			a.voiceOnAudio(ev.Int, ev.Text) // decode + queue a peer's frame for the mixer
		case courtroom.EventVoicePeers:
			a.voiceReconcilePeers() // someone joined/left voice — sync the decoder set
		case courtroom.EventModcall:
			a.pushOOC("[MOD CALL] "+ev.Text, "")
			// AO2's ui_guard silences the modcall ALERT only (courtroom.cpp:4997-5001
			// gates the sound and the window flash on !ui_guard->isChecked()). The OOC
			// line above and the auto-clip below still land, so a guarded mod loses the
			// interruption, never the report.
			if !a.compose.modcallGuard {
				a.playThemeSFX("mod_call")
				a.ctx.FlashWindow()
			}
			a.signalModcall(a.srvMeta.serverName, ev.Text)                    // desktop toast (opt-in)
			a.autoClipModcall(a.srvMeta.serverName, a.chatlog.icLog, ev.Text) // freeze IC context for mods (opt-out)
		case courtroom.EventAuth:
			// AO2 surfaces auth transitions as CLIENT lines in the OOC log
			// (on_authentication_state_received).
			switch {
			case ev.Int >= 1:
				a.pushOOC("CLIENT: Logged in as a moderator.", "")
				// Mod now: re-pull /getarea so IPIDs (mod-only, absent from PR/PU)
				// grab onto the roster. The first auto-pull on tab-open can fire
				// BEFORE this auto-login lands, so its reply carried no IPIDs.
				if a.link.sess != nil {
					a.fetchRoster()
				}
			case ev.Int == 0:
				a.pushOOC("CLIENT: Login unsuccessful.", "")
			default:
				a.pushOOC("CLIENT: You were logged out.", "")
			}
		case courtroom.EventSetPos:
			a.compose.sidePref = ev.Text // SP: the server moved us
		case courtroom.EventCase:
			a.pushOOC("[CASE] "+ev.Text, "")
			if enabled, roles := a.d.Prefs.Load().Casing(); enabled && ev.Int&roles != 0 {
				a.playThemeSFX("case_call")
				a.ctx.FlashWindow()
			}
		case courtroom.EventNotice:
			// BB is AO2's call_notice, and AO2 shows it in a real modal
			// (courtroom.cpp). We kept only the OOC mirror, so a whitelist code or a
			// lockdown explanation scrolled away behind the next line of chat. Do
			// BOTH: the log keeps it searchable, the box makes it copyable and
			// impossible to miss. serverNoticeInfo, so dismissing touches nothing —
			// the connection is fine and the server just said something.
			a.pushOOC("[SERVER] "+ev.Text, "")
			if strings.TrimSpace(ev.Text) != "" {
				a.openServerNotice(serverNoticeInfo, serverNoticeInfoTitle, a.srvMeta.serverName, ev.Text)
			}
			a.ctx.FlashWindow()
		case courtroom.EventMuted:
			// The server (un)muted us. Surface it in the OOC log + flash the window;
			// the IC input draws a persistent "muted" chip and refuses sends while
			// Session.Muted is set (AO2 set_mute disables ui_ic_chat_message).
			if ev.Int == 1 {
				a.pushOOC("[SERVER] You have been muted — you can't speak in-character.", "")
			} else {
				a.pushOOC("[SERVER] You have been unmuted.", "")
			}
			a.ctx.FlashWindow()
		case courtroom.EventEvidence:
			a.evid.evidAsk = nil  // list replaced; thumbnail pacing resets
			a.evid.evidScroll = 0 // a fresh list scrolls from the top
			if a.evid.evidIdx >= len(a.link.sess.Evidence) {
				a.evid.evidIdx = -1
			}
		case courtroom.EventEvidenceChanged:
			// One item changed (PE/EE/DE relay from another player or the
			// server): keep the selection and scroll, just fix the cursor when
			// a delete removed or shifted the selection.
			if ev.Int2 == courtroom.EvidenceOpDelete {
				if a.evid.evidIdx == ev.Int {
					a.evid.evidIdx = -1
					a.evid.evidEditing = false
					a.evid.evidConflict = false
				} else if a.evid.evidIdx > ev.Int {
					a.evid.evidIdx--
				}
			} else if ev.Int2 == courtroom.EvidenceOpEdit && a.evid.evidEditing && a.evid.evidIdx == ev.Int {
				// Someone else edited the very item we're mid-edit on: snapshot
				// the incoming values and surface the Keep mine / Accept theirs
				// compare instead of silently clobbering our unsaved work.
				a.evid.evidIncoming = a.link.sess.Evidence[ev.Int]
				a.evid.evidConflict = true
			}
		case courtroom.EventDisconnect:
			a.connErr = ev.Text
			a.debug.push("disconnected: " + ev.Text)
			// #60: KK/KB/BD surface here as "Kicked: …" / "Banned: …" — play the
			// matching feedback sound (cooldown-gated, so an auto-reconnect retry
			// loop can't machine-gun it) before the session tears down. The audio
			// device is app-level, so it keeps playing across the disconnect.
			switch {
			case strings.HasPrefix(ev.Text, "Kicked"):
				a.playModActionSFX(render.ModKick)
			case strings.HasPrefix(ev.Text, "Banned"):
				a.playModActionSFX(render.ModBan)
			}
			// Freeze the courtroom under the dialog (or plain-teardown to the lobby if
			// there's no room), then arm auto-reconnect. #1: EventDisconnect is ONLY
			// ever a KK/KB/BD kick/ban (session.go:696-701) — the server removing us on
			// purpose — so shouldAutoReconnect returns false for both prefixes and no
			// retry arms; the dialog shows the reason and its buttons still work.
			a.handleInvoluntaryDrop(ev.Text)
			return
		case courtroom.EventDebug:
			// Protocol-level diagnostics (unhandled headers, dropped MS):
			// the room has no use for these — debug overlay only.
			a.debug.push("server: " + ev.Text)
			continue
		}
		if a.recActive { // M16: tap the scene event stream for a replay recording
			a.recordEvent(ev)
		}
		a.bufferReplayEvent(ev) // instant-replay rolling buffer (no-op unless the opt-in pref is on)
		if a.link.room != nil {
			a.link.room.HandleEvent(ev)
		}
	}
}

// musicLogColor is the IC colour of the "has played a song" line — AO default
// white (0), matching AO2-Client handle_song's p_color.
const musicLogColor = 0

// logMusicChange adds AO2/webAO's "<name> has played a song: <song>" (or "has
// stopped the music") line to the IC log when a real player changed the music.
// System/area music (no valid charID) and area-name transfers don't log. The
// room still plays the track — this only mirrors the on-screen notice.
func (a *App) logMusicChange(ev courtroom.Event) {
	cid := ev.Int
	if a.link.sess == nil || cid < 0 || cid >= len(a.link.sess.Chars) {
		return // no player to attribute it to (system/area change or bad id)
	}
	action, song, ok := courtroom.MusicAction(ev.Text)
	if !ok {
		return // an area-name transfer, not a song
	}
	name := ev.Name // the MC packet's showname (field 2)
	if name == "" {
		name = a.link.sess.Chars[cid].Name // fall back to the character name
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

// icLogEntry builds everything the IC log stores for an incoming message: the
// display line, the real speaker (for name colours / pairing), the rune offset of
// the body within the line, and the body's inline-colour runs. The head is
// resolved ONCE here so the body offset and the two legacy line formatters can
// never disagree about where the message text starts.
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

// icBodyStyled returns an IC message's display body and its inline-colour style
// runs over that body. Emoji are expanded first
// (the transform commutes with markup stripping: emoji colons are not markup runes
// and vice versa), then the typewriter strips the markup and records the colour
// runs in one tested pass (the SAME parser the chatbox uses). The clean text is
// identical to the unstyled body's; styles is a single default run for a plain message.
func icBodyStyled(m *protocol.ChatMessage) (string, []courtroom.StyleRun) {
	raw := courtroom.ExpandInlineEmotes(courtroom.StripSpriteStyle(m.Message), inlineEmoteFor)
	tw := courtroom.NewTypewriter()
	tw.Start(raw)
	return tw.Text(), tw.Styles()
}

// icSpeakerName is the displayed name an IC log line is prefixed with. It is
// the SAME chain the name plate draws (courtroom.DisplayName): wire showname →
// char.ini showname → character folder, with force-char-names suppressing only
// the first rung. Canon draws the log through get_showname too
// (log_chatmessage, courtroom.cpp:2528-2538), so a second local rule here is
// how the two surfaces drift — which they had, both missing the middle rung.
// Stored on the entry so per-speaker name colours tint exactly that prefix.
func icSpeakerName(m *protocol.ChatMessage, forceChar bool, ini courtroom.IniShowname) string {
	return courtroom.DisplayName(m, forceChar, ini)
}

// icPos is an IC message's normalized courtroom position (the wire Side field):
// lowercased and trimmed, so the pos filter and the stored icEntry.pos always
// agree regardless of how a server or a custom SD list spells it. "" for a
// system line (no speaker).
func icPos(m *protocol.ChatMessage) string {
	if m == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(m.Side))
}

// friendNick returns the personal nickname set for m's speaker if they're a
// friend with one (#82 follow-up) — independent of the friend-highlight signals,
// since a nickname is a label, not an alert. Showname-else-character match, the
// identity the MS wire carries.
func (a *App) friendNick(m *protocol.ChatMessage) string {
	if m == nil || a.srvMeta.serverKey == "" {
		return ""
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	_, _, nick := a.d.Prefs.Load().ServerFriendInfo(a.srvMeta.serverKey, name)
	return nick
}

// installAssetOrigin points this session's URL builder at origin — the ONE
// place rebuildAssetOrigin mints one, mounts branch and server branch alike.
// Both jobs live here so they cannot drift apart: an origin installed without
// the flush would silently serve a previous visit's verdict.
func (a *App) installAssetOrigin(origin string) {
	a.link.urls = courtroom.NewURLBuilder(origin).WithCharCase(a.charCasingFor(origin))
	a.d.Manager.ForgetConclusiveMissesUnder(origin) // nil-safe: a headless App has no Manager and nothing to forget
}

// rebuildAssetOrigin wires the URL builder to local mounts or the server's
// asset URL, in that priority (the no-streaming checkbox wins).
func (a *App) rebuildAssetOrigin() {
	// DEFERRED, and first: this function has four exits (the local-mode branch, the
	// no-asset-URL error branch, the rehearsal branch and the normal tail). A call
	// at the tail would miss three of them — including rehearsal, which is exactly
	// where a local pack matters most. The layer needs the origin this function is
	// about to install, so it runs after the body either way.
	defer a.applyMountLayer()
	// Push the configured mounts into the streaming Manager's local:// overlay on
	// every mounts/pref change (this is the one funnel settings.go routes through),
	// regardless of the legacy enabled flag: mounts are the recording-resolution
	// base even mid-streaming-session (the content report's "Local base" source).
	if a.d.Manager != nil {
		if _, mounts := a.localAssets(); len(mounts) > 0 {
			a.d.Manager.SetLocalOverlay(assets.NewLocalFetcher(mounts))
		} else {
			a.d.Manager.SetLocalOverlay(nil)
		}
	}
	// Cache the local:// origin the evidence panel's "stream from server" toggle
	// reads local images from — the same mounts the overlay above serves ("" when
	// none, so the toggle falls back to streaming). Rebuilt on every mounts/pref
	// change because this is the one funnel they all route through.
	if _, mounts := a.localAssets(); len(mounts) > 0 {
		a.evid.evidLocalOrigin = assets.LocalOriginFor(mounts)
	} else {
		a.evid.evidLocalOrigin = ""
	}
	if enabled, mounts := a.localAssets(); enabled && len(mounts) > 0 {
		// The ORIGIN helper, not a fetcher: this branch only ever read BaseURL off
		// one, and the fetcher that actually serves these URLs was already built
		// above (SetLocalOverlay) or at boot. Building a second byte source — memo
		// and all — to obtain a string is the pattern LocalOriginFor exists to end.
		localOrigin := assets.LocalOriginFor(mounts)
		a.installAssetOrigin(localOrigin)
		log.Printf("ui: local asset mode over %d mounts", len(mounts))
		return
	}
	origin := ""
	if a.link.sess != nil {
		origin = a.link.sess.AssetURL
	}
	if origin == "" {
		a.connErr = `server provided no asset URL — set Settings > Assets > Asset source to "Only use my folders"`
		return
	}
	a.installAssetOrigin(origin)
	if a.srvMeta.rehearsal {
		return // offline: no DNS warm, no manifest fetch
	}
	if host := hostOfURL(origin); host != "" {
		a.d.Client.PreResolve(context.Background(), strings.Split(host, ":")[0])
	}
	a.fetchManifestAsync()
}

// rehearsalBadge labels the offline mode in the courtroom viewport.
const rehearsalBadge = "REHEARSAL — offline, nothing sends"

// startRehearsal enters the offline cached-asset browser for a server
// visited before: the remembered character list + asset origin, with the
// manager's network gate closed — emotes and sprites play from T2/T3,
// misses just say so. Disconnect (or any connect) exits the mode.
func (a *App) startRehearsal(name, key string, info config.ServerWarmInfo) {
	a.parkActive()
	if !a.allocateTab() {
		return
	}
	a.resetSessionState()
	a.srvMeta.serverName = name + " (rehearsal)"
	a.srvMeta.serverKey = key
	a.srvMeta.connAt = time.Now()
	a.srvMeta.rehearsal = true
	a.d.Manager.SetOffline(true)
	a.link.sess = courtroom.NewRehearsalSession(info.Origin, info.Chars)
	a.rebuildAssetOrigin()
	a.refreshCharKeys()
	a.refreshShownameKeys()
	a.refreshICPhraseKeys()
	a.srvMeta.themeBound = info.Theme // rehearsal wears the server's bound theme too
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
	a.debug.push("rehearsal: offline browse of " + name + " (cached assets only)")
}

// pickCharacter routes a char-select pick: live sessions ask the server
// (CC → PV → EventCharPicked); rehearsal resolves locally — no PV will
// ever arrive offline.
func (a *App) pickCharacter(idx int) {
	// Diagnostic for the "player list shows the wrong character" report (#88):
	// log the exact char_id we put on the wire in CC, named from our own list, so
	// a playtest with the Debug overlay on shows whether the index we SEND matches
	// the character picked (vs the server-assigned id logged at EventCharPicked).
	if idx >= 0 && idx < len(a.link.sess.Chars) {
		a.debug.push(fmt.Sprintf("char pick → CC char_id=%d (%s)", idx, a.link.sess.Chars[idx].Name))
	}
	if a.link.sess.Rehearsal {
		a.link.sess.MyCharID = idx
		a.enterCourtroom()
		return
	}
	// Warm this character's char.ini the instant we send CC, so the fetch
	// overlaps the CC→PV round-trip instead of only starting once PV lands
	// (Cluster E: /randomchar & Ctrl+R never pre-warm on hover the way a
	// char-select click does, adding one full RTT of visible button-load
	// delay). warmCharINI's iniWarmed guard makes the hover-then-click path a
	// no-op. Same bounds guard shape as the pushDebug above.
	if idx >= 0 && idx < len(a.link.sess.Chars) {
		a.warmCharINI(a.link.sess.Chars[idx].Name)
	}
	a.link.sess.PickCharacter(idx)
}

// fetchManifestAsync grabs <origin>/extensions.json (webAO convention —
// every server ships its own format mix) and seeds this host's learned
// formats so the first asset of each class costs exactly one probe even
// stone cold. Default ON (Settings → auto-detect); switching it off keeps
// the manual per-type probing in full control, and manual orders still
// govern servers without a manifest either way.
func (a *App) extProfileFor(host string) string {
	if p := a.d.Prefs.Load().ExtProfile(host); p != "" {
		return p
	}
	if a.isOfficialVanilla(host) {
		return assets.BundledVanillaManifestJSON
	}
	return ""
}

// isOfficialVanilla matches the AO official vanilla base by asset host, or the
// official vanilla server by the connected server key, so joining it uses the
// vanilla format profile (the example).
func (a *App) isOfficialVanilla(assetHost string) bool {
	h := strings.ToLower(assetHost)
	return strings.Contains(h, "attorneyoffline.de") ||
		strings.Contains(strings.ToLower(a.srvMeta.serverKey), "vanilla.aceattorneyonline.com")
}

// casingProbeResult carries an asset-casing probe's outcome back to the render thread.
type casingProbeResult struct {
	host   string
	casing uint8
}

// charCasingFor resolves the character-folder casing for origin's host: the manual pref as-is, or —
// only when the user selected CharCaseAuto — the LEARNED casing for that host (lowercase until it's
// learned). Resolving Auto here keeps it out of the SDL-free URLBuilder.
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

// maybeProbeCasing kicks off a ONE-TIME casing probe for the current server, but ONLY when the user
// has selected Auto casing, the char list is known, and this host hasn't been probed yet. A cheap
// per-frame no-op otherwise, so it costs nothing unless Auto is on.
func (a *App) maybeProbeCasing() {
	if a.d.Prefs.Load().AssetCharCase != courtroom.CharCaseAuto || a.casingProbing || a.link.sess == nil {
		return
	}
	origin := a.link.urls.Origin()
	host := hostOfURL(origin)
	if host == "" || !strings.HasPrefix(origin, "http") || a.casingProbedHost == host {
		return // not a network origin, or already handled this host this session
	}
	name := ""
	for i := range a.link.sess.Chars {
		if n := strings.TrimSpace(a.link.sess.Chars[i].Name); n != "" {
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

// probeCasing fetches character name's icon in each casing (lowercase → first-cap → title) and
// reports the FIRST that exists — so Auto stays on lowercase unless lowercase actually 404s. Two
// common icon formats are tried per casing; the whole burst is one-time per server (the sanctioned
// learning exception to one-probe-per-asset). Background goroutine — touches only the result channel.
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

// pollCasingProbe lands a casing probe: record the learned casing for its host and, if we're still
// on that server, apply it to the URL builder so subsequent fetches use it (a no-op when it stayed
// lowercase). Render thread.
func (a *App) pollCasingProbe() {
	select {
	case res := <-a.casingRes:
		a.casingProbing = false
		if a.charCaseLearned == nil {
			a.charCaseLearned = map[string]uint8{}
		}
		a.charCaseLearned[res.host] = res.casing
		if hostOfURL(a.link.urls.Origin()) == res.host { // still on this server → apply now
			a.link.urls = a.link.urls.WithCharCase(res.casing)
		}
		a.debug.push("asset casing auto-detect: " + res.host + " → " + charCaseName(res.casing))
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

func (a *App) fetchManifestAsync() {
	origin := a.link.urls.Origin()
	// Settings' re-seed hooks blank manifestFor to force a fresh fetch of the
	// current server after a profile / auto-detect change; honor that by dropping
	// the current origin from the dedupe set so the guard below re-runs it.
	if a.srvMeta.manifestFor == "" {
		delete(a.srvMeta.manifestSeeded, origin)
	}
	a.srvMeta.manifestFor = origin
	if a.originSeeded(origin) || !strings.HasPrefix(origin, "http") {
		return
	}
	a.markOriginSeeded(origin)
	host := hostOfURL(origin)
	// A per-host format profile (user-set, or the built-in official-vanilla example)
	// applies even when auto-detect is OFF — it's an explicit per-server override, not
	// the autodetect fetch. It seeds INSTANTLY (no network race) so the very first probe
	// already uses the right formats; it wins over the global default for THIS host only.
	if profile := a.extProfileFor(host); profile != "" {
		if m, err := assets.ParseManifest([]byte(profile)); err == nil {
			n := m.SeedLearned(a.d.Prefs, host)
			a.d.Resolver.WarmFromPrefs()
			a.debug.push(fmt.Sprintf("format profile: seeded %d classes for %s (instant)", n, host))
			return
		}
	}
	// The NETWORK fetch of the server's own extensions.json is gated on auto-detect
	// (the per-server profile above is not — it always applies).
	if !a.d.Prefs.Load().AutoDetectFormats {
		return
	}
	mgr := a.d.Manager
	prefs := a.d.Prefs
	ch := a.manifestRes
	go func() {
		res := seedOriginFormats(mgr, prefs, origin)
		mf := manifestFetch{host: host, seeded: res.seeded, status: res.status, err: res.err}
		// Preserve the session path's prior semantics: pollManifest treats a nil
		// err as "seeded, republish + log N classes". A host with no manifest
		// (seedAbsent) or an unreadable one (seedFailed with no wrapped err) is NOT
		// a seed, so surface it as an error so pollManifest logs "learn per probe
		// instead" rather than republishing a no-op and claiming 0 classes seeded.
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

// originSeeded reports whether origin's extensions.json has already been fetched
// + seeded this session (shared dedupe for the session connect path and the two
// diagnostic paths). The empty origin is never "seeded" so callers still gate on
// their own http(s) check.
func (a *App) originSeeded(origin string) bool {
	if origin == "" {
		return false
	}
	_, ok := a.srvMeta.manifestSeeded[origin]
	return ok
}

// markOriginSeeded records origin as seeded this session. Render thread only
// (the map is not synchronized — every caller marks on the render thread before
// spawning its off-thread fetch).
func (a *App) markOriginSeeded(origin string) {
	if origin == "" {
		return
	}
	if a.srvMeta.manifestSeeded == nil {
		a.srvMeta.manifestSeeded = map[string]struct{}{}
	}
	a.srvMeta.manifestSeeded[origin] = struct{}{}
}

// seedStatus is the outcome of a diagnostic origin-seeding pass, noted on a
// content report so the panel/bundle text can say WHY a host's formats were (or
// weren't) learned before probing.
type seedStatus int

const (
	seedSkipped seedStatus = iota // not attempted (non-http origin, or autodetect off)
	seedApplied                   // extensions.json fetched + parsed; N classes seeded
	seedAbsent                    // no reachable extensions.json (host publishes none)
	seedFailed                    // reachable but unparseable / transport fault
)

// seedResult carries a shared seed core's outcome back to its caller. seeded is
// the number of format classes written to the learned table (0 unless status is
// seedApplied). It is COSMETIC for probing correctness — probeRef reads the
// learned table directly after WarmFromPrefs, not this value — and feeds only
// the report's status line.
type seedResult struct {
	status seedStatus
	seeded int
	err    error
}

// seedOriginFormats is the shared, blocking core both the session connect path
// (fetchManifestAsync, fire-and-forget) and the diagnostic paths (content
// report, export loader) use to seed a host's learned formats from its
// <origin>/extensions.json. It runs OFF the render thread (network fetch +
// prefs writes, both goroutine-safe: FetchRaw is a blocking network call,
// RecordLearned/SeedLearned take the prefs mutex). The CALLER republishes the
// resolver table (WarmFromPrefs) on the render thread once this returns, so a
// candidate built after that sees the seeds. absent (a clean 404) is DISTINCT
// from failed (transport/parse fault) so the panel can say "server publishes no
// format manifest" honestly rather than "couldn't reach it".
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

// pollManifest lands autodetect results: republish the resolver snapshot
// (now containing the seeds) and report on the debug lane.
func (a *App) pollManifest() {
	select {
	case res := <-a.manifestRes:
		a.noteManifestStatus(res.host, res.status)
		if res.err != nil {
			a.debug.push("extensions.json: " + res.err.Error() + " — formats learn per probe instead")
			return
		}
		a.d.Resolver.WarmFromPrefs()
		a.debug.push(fmt.Sprintf("extensions.json: seeded %d format classes for %s", res.seeded, res.host))
	default:
	}
}

// noteManifestStatus records whether a host published a usable extensions.json,
// so Settings → Formats can state the fact instead of leaving the player to
// guess. Render thread only (same as manifestSeeded, and for the same reason:
// the map is not synchronized). Bounded by the number of DISTINCT asset hosts
// visited in one session — a handful in practice (rule §17.4).
func (a *App) noteManifestStatus(host string, st seedStatus) {
	if host == "" {
		return
	}
	if a.srvMeta.manifestStatus == nil {
		a.srvMeta.manifestStatus = map[string]seedStatus{}
	}
	a.srvMeta.manifestStatus[host] = st
}

// fontLoad is one font-override read result: file bytes in chain order
// plus a one-line verdict for the settings status.
type fontLoad struct {
	names []string
	data  [][]byte
	note  string
}

// fontFileMaxBytes bounds one override font file (CJK TTCs run ~20 MiB;
// past 64 MiB it's not a font, it's a mistake).
const fontFileMaxBytes = 64 << 20

// applyFontConfig installs the IC/OOC override font chain from prefs through one
// resolver, so the launch path and the live Settings toggles can never disagree:
// the dyslexia toggle wins (embedded OpenDyslexic, set synchronously — no disk),
// else the manual font-path chain (read off-thread), else the built-in font.
// Called at launch and on every change to either source.
func (a *App) applyFontConfig() {
	// The themed layout bakes each AO2 toggle's label truncated against the CHROME
	// face (themeLayoutCache.toggleLabel), and the cache key knows nothing about
	// fonts. Changing the face without this leaves every label measured against the
	// old one until an unrelated resize or theme apply: LabelClipped then hard-cuts
	// mid-glyph where the new face is wider, or shows a needless ellipsis where it
	// is narrower. Theme applies already invalidate; this covers the font sources.
	defer a.invalidateThemeCanvases()

	switch fontChainSource(a.d.Prefs.Load().DyslexiaFont, a.d.Prefs.Load().FontOverridePaths) {
	case fontSourceDyslexia:
		a.ctx.SetFontChain([]string{dyslexiaFontName}, [][]byte{openDyslexicOTF})
		// Font-everywhere (opt-in): the same face drives the chrome too;
		// otherwise make sure the chrome is back on the embedded font.
		if a.d.Prefs.Load().FontEverywhere {
			a.ctx.SetChromeFont(openDyslexicOTF)
		} else {
			a.ctx.SetChromeFont(nil)
		}
		a.raster.rasterText = "" // re-raster the visible message in the new font
	default:
		// Manual font paths win; otherwise the active theme's own bundled font
		// (an AO theme that ships a .ttf — #6, Crystalwarrior); otherwise the
		// embedded font (loadFontChainAsync clears on an empty string).
		paths := strings.TrimSpace(a.d.Prefs.Load().FontOverridePaths)
		if paths == "" {
			paths = a.themeFontFile
		}
		a.loadFontChainAsync(paths)
	}
}

// loadFontChainAsync reads the override font files off-thread (semicolon-
// or comma-separated paths, chain order, ≤ fontChainCap) and lands them on
// fontRes. An empty list clears the override immediately.
func (a *App) loadFontChainAsync(raw string) {
	paths := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	if len(paths) == 0 {
		a.ctx.SetFontChain(nil, nil)
		a.ctx.SetChromeFont(nil) // no override chain → the chrome is embedded again
		a.raster.rasterText = "" // re-raster the visible message with the embedded font
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

// pollFontChain lands override font bytes on the render thread: install
// the chain (fonts build lazily per scale) and force a chat re-raster.
func (a *App) pollFontChain() {
	select {
	case res := <-a.fontRes:
		// Precedence guard: if the dyslexia toggle flipped on while this manual
		// chain was loading off-thread, the embedded font already won — don't
		// let the stale result clobber it.
		if len(res.data) > 0 && !a.d.Prefs.Load().DyslexiaFont {
			a.ctx.SetFontChain(res.names, res.data)
			// Font-everywhere (opt-in): the chain's PRIMARY face drives the
			// chrome too; otherwise restore the embedded chrome (the toggle
			// may have just been switched off). On a failed open the chrome
			// keeps the embedded font — say so instead of silently ignoring.
			if !a.d.Prefs.Load().FontEverywhere {
				a.ctx.SetChromeFont(nil)
			} else if !a.ctx.SetChromeFont(res.data[0]) {
				res.note += " — chrome font failed to open, whole-UI font stays embedded"
			}
		}
		a.raster.rasterText = ""
		settings.statusLine = clampLine(res.note)
		a.debug.push(res.note)
	default:
	}
}

// prefetchCharIcons warms the first screenfuls of icons speculatively.
// Deliberately capped: the rest load on demand from drawCharCell, because
// a 4000-char burst would only shed itself out of the low lane.
func (a *App) prefetchCharIcons() {
	if a.link.sess == nil || a.link.urls.Origin() == "" {
		return
	}
	for i, c := range a.link.sess.Chars {
		if i >= charIconWarmup {
			break
		}
		a.d.Manager.Prefetch(a.link.urls.CharIcon(c.Name), assets.AssetTypeCharIcon, network.PriorityLow) // AssetType: CharIcon
	}
}

func (a *App) enterCourtroom() {
	if a.link.sess == nil {
		return
	}
	// FRESH entry from char select: a wardrobe pick rides in as pendingIni (the
	// slot was auto-claimed); a plain pick starts clean, and the side waits on the
	// new char.ini. A tab REACTIVATION must NOT run these — activateTab calls
	// buildRoom directly so the parked iniswap + /pos override survive (they were
	// leaking across tabs: re-entering a tab un-iniswapped you and reset your /pos).
	a.inibrowse.iniChar = a.inibrowse.pendingIni
	a.inibrowse.pendingIni = ""
	a.compose.sidePref = ""
	a.compose.charDefaultSide = "" // a fresh character declares its own default; the old one must not outlive it
	// FRESH entry / character switch starts at the first emote (page 0). The
	// reactivation path (activateTab → buildRoom) deliberately skips this so each
	// tab keeps its own emote selection; pollCharINI then clamps rather than
	// resets, so the parked index survives the reload.
	a.emotepick.emoteIdx, a.emotepick.emoteRow = 0, 0
	// A fresh character must not inherit the previous character's last staged
	// message — buildRoom's stageRestoreMsg would otherwise re-stage the OLD
	// speaker's sprite (the "swapped to Armstrong, Phoenix's emote on the witness
	// desk" report). Tab reactivation (activateTab → buildRoom directly) keeps it.
	a.link.sess.LastIC = nil
	a.buildRoom()
}

// buildRoom (re)constructs the render-coupled courtroom from the CURRENT session
// state WITHOUT touching the per-entry bits (iniChar / sidePref) — so a tab
// reactivation rebuilds the room while the parked iniswap and /pos ride along
// (loadCharINI below re-applies the active, possibly iniswapped, character).
