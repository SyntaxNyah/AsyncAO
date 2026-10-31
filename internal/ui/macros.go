package ui

// Macro engine and built-in server account login, for any /login account system, not just staff auth.
// Macro lines go out as paced OOC sends under the user's OOC name, else a sticky random AsyncAO<N>, because servers
// reject empty OOC names.
// The login flow picks itself from the software announced in the ID handshake (loginLines).

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// macroLineDelay paces multi-line macros: fast enough to feel instant, slow enough for a server prompt between
	// lines.
	macroLineDelay = 300 * time.Millisecond
	// macroQueueCap bounds pending macro lines (hard rule 4), so spamming a macro key can't build a backlog.
	macroQueueCap = 32
	// oocSendMinGap is the hard floor between automated OOC sends, enforced at drain time so a backlog can't leave as a
	// burst.
	// Servers kick on an OOC flood, as a bare close with no reason (Nyathena: ooc_rate_limit, and message_rate_limit
	// commonly 10 per 7 s). One second stays under the tighter limit; deliberately slower than macroLineDelay.
	oocSendMinGap = time.Second
	// defaultOOCNameRange is the N in the "AsyncAO<N>" fallback name.
	defaultOOCNameRange = 200
)

// oocSend is one queued automated OOC line, shared by login flows and macros.
type oocSend struct {
	line string
	due  time.Time
}

// defaultOOCName is a fresh tab's OOC name: the saved default, else the saved showname.
func (a *App) defaultOOCName() string {
	if s := a.d.Prefs.Load().OOCName; s != "" {
		return s
	}
	return a.d.Prefs.Load().Showname
}

// oocNameOrDefault is the OOC name for command and macro sends: the user's, else a random AsyncAO<N> kept for the run.
func (a *App) oocNameOrDefault() string {
	if n := strings.TrimSpace(a.oocName); n != "" {
		return n
	}
	if a.defaultOOC == "" {
		a.defaultOOC = fmt.Sprintf("AsyncAO%d", 1+rand.IntN(defaultOOCNameRange))
	}
	return a.defaultOOC
}

// queueOOCLines schedules lines for paced automated OOC sending.
func (a *App) queueOOCLines(lines []string) {
	if a.sess == nil {
		return
	}
	now := a.now()
	for i, line := range lines {
		if len(a.oocQueue) >= macroQueueCap {
			a.pushDebug("automation queue full — dropping the rest")
			return
		}
		a.oocQueue = append(a.oocQueue, oocSend{
			line: line,
			due:  now.Add(time.Duration(i) * macroLineDelay),
		})
	}
}

// processOOCQueue sends at most one due line per oocSendMinGap. Called from both Frame and Background so the drain rate
// doesn't depend on the render loop; sending every due entry at once would get us kicked for flooding.
func (a *App) processOOCQueue() {
	if len(a.oocQueue) == 0 || a.sess == nil {
		return
	}
	now := a.now()
	// A zero stamp sends immediately: the floor is between sends, not before the first.
	if !a.lastAutoOOC.IsZero() && now.Sub(a.lastAutoOOC) < oocSendMinGap {
		return
	}
	if a.oocQueue[0].due.After(now) {
		return // the entry's own schedule hasn't come round yet
	}
	a.sess.SendOOC(a.oocNameOrDefault(), a.oocQueue[0].line)
	a.lastAutoOOC = now
	a.oocQueue = a.oocQueue[:copy(a.oocQueue, a.oocQueue[1:])]
}

// oocQueuePending reports whether line is already queued, so a stalled drain can't stack duplicates from pollers.
func (a *App) oocQueuePending(line string) bool {
	for _, m := range a.oocQueue {
		if m.line == line {
			return true
		}
	}
	return false
}

// runMacro queues one macro and logs it; lines may hold credentials, so never echo their content.
func (a *App) runMacro(m config.MacroSpec) {
	a.queueOOCLines(m.Lines)
	a.pushDebug(fmt.Sprintf("macro %q: %d line(s) queued", m.Name, len(m.Lines)))
}

// handleMacroKeys fires macros on Alt+<bound key>; macro binds win over character binds on conflict.
func (a *App) handleMacroKeys() bool {
	k := a.bareBindKey()
	if k == 0 {
		return false
	}
	key := strings.ToLower(sdl.GetKeyName(k))
	for _, m := range a.d.Prefs.Load().Macros() {
		if m.Key != "" && m.Key == key {
			a.runMacro(m)
			return true
		}
	}
	return false
}

// loginLines builds the login flow: Akashi "/login" then "<user> <pass>" answering its prompt; KFO "/login <pass>";
// everything else "/login <user> <pass>".
func (a *App) loginLines(user, pass string) []string {
	soft := ""
	if a.sess != nil {
		soft = strings.ToLower(a.sess.Software)
	}
	switch {
	case strings.Contains(soft, "akashi"):
		// The server does not echo the answer to Akashi's prompt into OOC, so nothing leaks.
		return []string{"/login", user + " " + pass}
	case strings.Contains(soft, "kfo"):
		return []string{"/login " + pass}
	}
	return []string{"/login " + user + " " + pass}
}

// loginNow queues the login flow from the saved credentials.
func (a *App) loginNow() {
	info := a.d.Prefs.Load().ServerWarmInfoFor(a.serverKey)
	if info.LoginUser == "" {
		a.pushDebug("login: no saved credentials for this server (Login... dialog)")
		return
	}
	a.queueOOCLines(a.loginLines(info.LoginUser, info.LoginPass))
	a.loginAt = a.now() // open the callword grace: login replies routinely echo your handle/name
	a.pushDebug("login: flow queued as " + a.oocNameOrDefault())
}

// loginFlowPreview names the wire flow the dialog will use, before you trust it with credentials.
func (a *App) loginFlowPreview() string {
	soft := "unknown software"
	if a.sess != nil && a.sess.Software != "" {
		soft = a.sess.Software
	}
	lower := ""
	if a.sess != nil {
		lower = strings.ToLower(a.sess.Software)
	}
	switch {
	case strings.Contains(lower, "akashi"):
		return soft + `: sends "/login" ⏎ … then "username password" ⏎ (the prompt answer is not echoed)`
	case strings.Contains(lower, "kfo"):
		return soft + `: sends "/login password" ⏎ (KFO has no usernames — the box is ignored)`
	}
	return soft + `: sends "/login username password" ⏎`
}

// autoLoginOnReady sends the saved login once per session when enabled, with a toast to confirm it.
func (a *App) autoLoginOnReady() {
	// At most once per session: some servers re-send DONE mid-session, which would re-queue the login. The flag clears
	// on a fresh connection, so a real reconnect logs in again; a manual loginNow is unaffected.
	if a.autoLoginTried {
		return
	}
	a.autoLoginTried = true
	info := a.d.Prefs.Load().ServerWarmInfoFor(a.serverKey)
	if !info.AutoLogin || info.LoginUser == "" {
		return
	}
	a.loginNow()
	if !a.d.Prefs.Load().AutoLoginToast {
		return // the mod opted out of the notification (Settings → General)
	}
	// Streamer mode masks the username.
	line := "Auto-logging in"
	if !a.d.Prefs.Load().StreamerModeOn {
		line += " as " + info.LoginUser
	}
	if a.serverName != "" {
		line += " on " + a.serverName
	}
	a.warnLine = clampLine(line)
	a.warnAt = time.Now()
	// Shares the friend-toast throttle so a multi-tab restore can't burst one popup per reconnecting server.
	if time.Since(a.lastOSToast) >= osToastMinInterval {
		a.lastOSToast = time.Now()
		a.d.Desktop.Notify("AsyncAO — auto-login", line)
	}
}

// Login panel geometry.
const (
	loginPanelDefW = int32(460)
	loginPanelDefH = int32(262)
	// The button row needs pad+140+90 before Cancel's own 90+pad can start.
	loginPanelMinW = int32(360)
	loginPanelMinH = int32(240) // flow line + both fields + the checkbox + the note + the button row
)

// loginPanelRect is the panel's rect, seeded from its saved layout on first open and clamped on-screen after.
func (a *App) loginPanelRect(w, h int32) sdl.Rect {
	if r, ok := a.seedPanelFromSlot(&a.loginWin, slotPanelLogin, loginPanelDefW, loginPanelDefH, loginPanelMinW, loginPanelMinH, w, h); ok {
		return r
	}
	return a.loginWin.rect(loginPanelDefW, loginPanelDefH, loginPanelMinW, loginPanelMinH, w, h)
}

// drawLoginDialog edits this server's credentials (stored in plain text, as it says) and runs the flow.
// A floatWin, not a modal, so the courtroom stays visible behind the flow preview.
func (a *App) drawLoginDialog(w, h int32, pressed *bool) {
	c := a.ctx
	wasActive := a.loginWin.dragging || a.loginWin.resizing // detect the drag/resize-end frame for slot persistence
	panel := a.loginPanelRect(w, h)
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: floatTitleH}, ColPanelHi)
	c.Heading(panel.X+pad, panel.Y+6, "Auto-login", ColText) // the server name moves into the body — a narrow panel would clip it off the title bar
	closeB := sdl.Rect{X: panel.X + panel.W - 30, Y: panel.Y + 3, W: 24, H: btnH}
	if c.Button(closeB, "X") {
		a.showLogin = false
		return
	}
	a.floatWinDrag(&a.loginWin, sdl.Rect{X: panel.X, Y: panel.Y, W: closeB.X - panel.X - 4, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: panel.X + panel.W - floatGripSz, Y: panel.Y + panel.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.loginWin, grip, panel, loginPanelMinW, loginPanelMinH, pressed)
	a.drawResizeGrip(grip)
	if wasActive && !a.loginWin.dragging && !a.loginWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelLogin, panel, w, h)
	}
	y := panel.Y + floatTitleH + 8
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad, a.serverName, ColText)
	y += 20
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad, a.loginFlowPreview(), ColAccent)
	y += 22
	userField := c.TextField
	if a.d.Prefs.Load().StreamerModeOn {
		userField = c.PasswordField // names mask on stream too
	}
	a.loginUser, _ = userField("loginuser", sdl.Rect{X: panel.X + pad, Y: y, W: panel.W - 2*pad, H: fieldH}, a.loginUser, "username")
	y += fieldH + 6
	a.loginPass, _ = c.PasswordField("loginpass", sdl.Rect{X: panel.X + pad, Y: y, W: panel.W - 2*pad, H: fieldH}, a.loginPass, "password")
	y += fieldH + 6
	a.loginAuto = c.Checkbox(panel.X+pad, y, "Auto-login every time I join this server", a.loginAuto)
	y += 24
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad, "Saved per server, in PLAIN TEXT in your prefs file.", ColTextDim)
	// Clear of the resize grip in the corner (it would otherwise sit on Cancel).
	by := panel.Y + panel.H - btnH - 18
	if c.Button(sdl.Rect{X: panel.X + pad, Y: by, W: 130, H: btnH}, "Save & login") {
		a.d.Prefs.SetServerLogin(a.serverKey, strings.TrimSpace(a.loginUser), a.loginPass, a.loginAuto)
		a.loginNow()
		a.showLogin = false
	}
	if c.Button(sdl.Rect{X: panel.X + pad + 140, Y: by, W: 90, H: btnH}, "Save") {
		a.d.Prefs.SetServerLogin(a.serverKey, strings.TrimSpace(a.loginUser), a.loginPass, a.loginAuto)
		a.showLogin = false
	}
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: by, W: 90, H: btnH}, "Cancel") {
		a.showLogin = false
	}
}

// openLoginDialog loads the saved credentials into the edit buffers.
func (a *App) openLoginDialog() {
	info := a.d.Prefs.Load().ServerWarmInfoFor(a.serverKey)
	a.loginUser, a.loginPass, a.loginAuto = info.LoginUser, info.LoginPass, info.AutoLogin
	a.showLogin = true
}

// macroLineSeparator splits the editor's one-field line entry; "|" is very rare in OOC commands.
const macroLineSeparator = "|"

// drawMacroSettings renders the macro list + editor. Returns the next y.
func (a *App) drawMacroSettings(y int32, w int32) int32 {
	c := a.ctx
	pad := a.formX
	_ = w // laid out by formX/formW; w param kept for the call signature
	macros := a.d.Prefs.Load().Macros()
	c.Label(pad, y+4, fmt.Sprintf("Macros (%d/%d) — chain OOC commands with %q and fire the whole chain from one key:", len(macros), config.MacroCap, macroLineSeparator), ColText)
	y += 26

	removeIdx := -1
	for i, m := range macros {
		// The chord, not m.Key: the chain fires on Alt+<key>, and a bare letter would advertise a press that just
		// types.
		key := bindChipLabel(m.Key, false, "", "—")
		line := fmt.Sprintf("[%s] %s: %s", key, m.Name, strings.Join(m.Lines, " "+macroLineSeparator+" "))
		c.LabelClipped(pad+12, y+3, a.formW-90, clampLine(line), ColTextDim)
		if c.Button(sdl.Rect{X: a.formX + a.formW - 56, Y: y, W: 50, H: 22}, "✕") {
			removeIdx = i
		}
		y += 24
	}
	if removeIdx >= 0 {
		macros = append(macros[:removeIdx], macros[removeIdx+1:]...)
		a.d.Prefs.SetMacros(macros)
	}

	// Editor row: name, key capture, lines, add.
	settings.macroName, _ = c.TextField("macroname", sdl.Rect{X: pad, Y: y, W: 130, H: fieldH}, settings.macroName, "name")
	keyLabel := bindChipLabel(settings.macroKey, a.macroBind >= 0, "press...", "set key")
	if c.Button(sdl.Rect{X: pad + 136, Y: y, W: 86, H: btnH}, keyLabel) {
		a.macroBind = 0 // arm capture (Esc cancels)
		a.ctx.focusID = ""
	}
	settings.macroLines, _ = c.TextField("macrolines", sdl.Rect{X: pad + 228, Y: y, W: a.formW - 358, H: fieldH},
		settings.macroLines, `/cm `+macroLineSeparator+` /area 2 `+macroLineSeparator+` /tsundere 1   (each `+macroLineSeparator+` step sends as its own ENTER)`)
	if c.Button(sdl.Rect{X: a.formX + a.formW - 124, Y: y, W: 118, H: btnH}, "Add macro") {
		name := strings.TrimSpace(settings.macroName)
		var lines []string
		for _, l := range strings.Split(settings.macroLines, macroLineSeparator) {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		if name != "" && len(lines) > 0 && len(macros) < config.MacroCap {
			macros = append(macros, config.MacroSpec{Name: name, Key: settings.macroKey, Lines: lines})
			a.d.Prefs.SetMacros(macros)
			settings.macroName, settings.macroKey, settings.macroLines = "", "", ""
			settings.statusLine = "Macro added."
		}
	}
	y += 30
	// An Alt chord is never text, so the chain fires even mid-sentence in the IC box.
	c.Label(pad, y, "Sends go to OOC as your OOC name (or AsyncAO<n> when unset); the chain fires on "+bareBindChordPrefix+"<key>, even while you are typing.", ColTextDim)
	return y + 24
}

// drawLoginSettings configures any known server's login ahead of time and returns the next y.
func (a *App) drawLoginSettings(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	c.Label(pad, y+4, "Auto-login — for ANY server account (member perks, donator ranks, mod powers). Credentials per server; the wire flow picks itself:", ColText)
	y += 20
	c.Label(pad+12, y, "Athena/Nyathena/Whisker: /login username password   ·   KFO: /login password   ·   Akashi: /login ⏎ then username password ⏎", ColTextDim)
	y += 22

	// Server picker: defaults to the connected server, works without one.
	names, keys := a.loginTargets()
	if len(names) == 0 {
		c.Label(pad+12, y, "No servers known yet — Refresh the lobby once, then set up logins here ahead of time.", ColTextDim)
		return y + 26
	}
	cur := 0
	if settings.loginKey == "" && a.serverKey != "" {
		settings.loginKey = a.serverKey
	}
	for i, k := range keys {
		if k == settings.loginKey {
			cur = i
			break
		}
	}
	if settings.loginKey != keys[cur] {
		settings.loginKey, settings.loginLoaded = keys[cur], false // stale pick: normalize
	}
	c.Label(pad+12, y+4, "Server:", ColText)
	if next, changed := c.Dropdown("loginsrv", sdl.Rect{X: pad + 80, Y: y, W: 280, H: btnH}, names, cur); changed {
		settings.loginKey, settings.loginLoaded = keys[next], false
	}
	if settings.loginKey == a.serverKey && a.sess != nil {
		c.LabelClipped(pad+372, y+4, w-pad-372-scrollBarW, a.loginFlowPreview(), ColAccent)
	} else {
		c.LabelClipped(pad+372, y+4, w-pad-372-scrollBarW, "flow auto-detects from the server software when you join", ColTextDim)
	}
	y += btnH + 8

	// Load the picked server's saved login once per pick.
	if !settings.loginLoaded {
		info := a.d.Prefs.Load().ServerWarmInfoFor(settings.loginKey)
		settings.loginUser, settings.loginPass, settings.loginAuto = info.LoginUser, info.LoginPass, info.AutoLogin
		settings.loginLoaded = true
	}

	saveTarget := func() {
		a.d.Prefs.SetServerLogin(settings.loginKey, strings.TrimSpace(settings.loginUser), settings.loginPass, settings.loginAuto)
		if settings.loginKey == a.serverKey {
			a.syncLoginBuffers() // the courtroom Login... dialog shows the same
		}
	}

	c.Label(pad+12, y+4, "Username:", ColText)
	// Streamer mode treats the username as a name too — masked on stream.
	userField := c.TextField
	if a.d.Prefs.Load().StreamerModeOn {
		userField = c.PasswordField
	}
	settings.loginUser, _ = userField("loginuser", sdl.Rect{X: pad + 100, Y: y, W: 180, H: fieldH}, settings.loginUser, "username")
	c.Label(pad+292, y+4, "Password:", ColText)
	settings.loginPass, _ = c.PasswordField("loginpass", sdl.Rect{X: pad + 380, Y: y, W: 180, H: fieldH}, settings.loginPass, "password")
	if c.Button(sdl.Rect{X: pad + 572, Y: y, W: 70, H: btnH}, "Save") {
		saveTarget()
		settings.statusLine = "Login saved (plain text in the prefs file) — it fires on your next join."
	}
	// Login now only makes sense against the live session.
	if settings.loginKey == a.serverKey && a.sess != nil {
		if c.Button(sdl.Rect{X: pad + 648, Y: y, W: 100, H: btnH}, "Login now") {
			saveTarget()
			a.loginNow()
		}
	}
	y += fieldH + 6
	next := c.Checkbox(pad+12, y, "Auto-login on join: OFF = log in only when YOU trigger it (Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyLogin))+" or the courtroom Login... button); ON = instantly on every join", settings.loginAuto)
	if next != settings.loginAuto {
		settings.loginAuto = next
		saveTarget()
	}
	return y + 28
}

// loginTargets lists the picker's servers, connected one first; cached because WebSocketURL allocates per entry.
func (a *App) loginTargets() ([]string, []string) {
	if settings.loginSrvCount == len(a.servers) && settings.loginSrvFor == a.serverKey && settings.loginNames != nil {
		return settings.loginNames, settings.loginKeys
	}
	var names, keys []string
	if a.sess != nil && a.serverKey != "" {
		names = append(names, a.serverName+" (connected)")
		keys = append(keys, a.serverKey)
	}
	for i := range a.servers {
		e := &a.servers[i]
		if !e.Joinable() {
			continue
		}
		url := e.WebSocketURL()
		if url == a.serverKey {
			continue
		}
		names = append(names, e.Name)
		keys = append(keys, url)
	}
	settings.loginNames, settings.loginKeys = names, keys
	settings.loginSrvCount, settings.loginSrvFor = len(a.servers), a.serverKey
	return names, keys
}

// syncLoginBuffers loads this server's saved credentials into the edit buffers at connect time.
func (a *App) syncLoginBuffers() {
	info := a.d.Prefs.Load().ServerWarmInfoFor(a.serverKey)
	a.loginUser, a.loginPass, a.loginAuto = info.LoginUser, info.LoginPass, info.AutoLogin
}

// pollMacroBind completes an armed macro key capture.
func (a *App) pollMacroBind() {
	if a.macroBind < 0 {
		return
	}
	c := a.ctx
	if c.escPressed {
		a.macroBind = -1
		return
	}
	if c.keyPressed == 0 {
		return
	}
	settings.macroKey = strings.ToLower(sdl.GetKeyName(c.keyPressed))
	a.macroBind = -1
}
