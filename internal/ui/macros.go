package ui

// Macro engine + built-in server account login. The login is for ANY
// /login account system — member perks, donator ranks, mod powers alike
// — not just staff auth.
//
// A macro is a sequence of OOC lines sent in order with a fixed pace
// (macroLineDelay) — enough for prompt-style flows like Akashi's
// two-step login ("/login" → server prompts → "user pass"). Lines go
// out through the normal OOC send with the user's OOC name, falling
// back to a sticky random "AsyncAO<1-200>" when none is set (servers
// reject empty OOC names; commands and macros must always be sendable).
//
// The built-in login knows the two wire shapes in the wild:
//   - Akashi:                       "/login" then "<user> <pass>"
//     (the credential line answers Akashi's prompt and is NOT echoed
//     into OOC by the server, so nothing leaks)
//   - Nyathena/KFO/Athena/Whisker:  "/login <user> <pass>"
// The flow picks itself from the server software announced in the ID
// handshake; unknown servers get the one-line form (the common wire).

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

const (
	// macroLineDelay paces multi-line macros: fast enough to feel
	// instant, slow enough for a server prompt round-trip between lines.
	macroLineDelay = 300 * time.Millisecond
	// macroQueueCap bounds pending macro lines (rule §17.4) — spamming a
	// macro key can't build an unbounded send backlog.
	macroQueueCap = 32
	// defaultOOCNameRange is the N in the "AsyncAO<N>" fallback name.
	defaultOOCNameRange = 200
)

// oocSend is one queued automated OOC line (login flows and macros share
// the pipeline; the login itself is NOT a macro — it has its own
// per-server credential store, dialog, and auto-on-join trigger).
type oocSend struct {
	line string
	due  time.Time
}

// oocNameOrDefault is the OOC identity every command/macro send uses:
// the user's name when set, else a sticky random AsyncAO<1-200> minted
// once per run (so one session keeps one identity).
func (a *App) oocNameOrDefault() string {
	if n := strings.TrimSpace(a.oocName); n != "" {
		return n
	}
	if a.defaultOOC == "" {
		a.defaultOOC = fmt.Sprintf("AsyncAO%d", 1+rand.IntN(defaultOOCNameRange))
	}
	return a.defaultOOC
}

// queueOOCLines schedules lines for paced automated OOC sending (the
// shared pipeline under the login automation and user macros).
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

// processOOCQueue sends due lines (called once per frame; the queue
// belongs to the active session and pauses while its tab is parked).
func (a *App) processOOCQueue() {
	if len(a.oocQueue) == 0 || a.sess == nil {
		return
	}
	now := a.now()
	sent := 0
	for _, m := range a.oocQueue {
		if m.due.After(now) {
			break
		}
		a.sess.SendOOC(a.oocNameOrDefault(), m.line)
		sent++
	}
	if sent > 0 {
		a.oocQueue = a.oocQueue[:copy(a.oocQueue, a.oocQueue[sent:])]
	}
}

// runMacro queues one macro and reports it on the debug lane (lines may
// hold credentials — never echo their content).
func (a *App) runMacro(m config.MacroSpec) {
	a.queueOOCLines(m.Lines)
	a.pushDebug(fmt.Sprintf("macro %q: %d line(s) queued", m.Name, len(m.Lines)))
}

// handleMacroKeys fires key-bound macros on a bare keypress — same
// guards as character keybinds: no focused field, no capture armed, no
// Ctrl chord. Macro binds win over character binds on conflict (they
// were bound deliberately; the wardrobe badge shows char binds).
func (a *App) handleMacroKeys() bool {
	c := a.ctx
	if c.keyPressed == 0 || c.focusID != "" || a.bindingFor != "" || a.macroBind >= 0 || c.ctrlHeld {
		return false
	}
	key := strings.ToLower(sdl.GetKeyName(c.keyPressed))
	for _, m := range a.d.Prefs.Macros() {
		if m.Key != "" && m.Key == key {
			a.runMacro(m)
			return true
		}
	}
	return false
}

// --- built-in login ----------------------------------------------------------

// loginLines builds the wire flow for this server's software. Three
// shapes exist in the wild:
//   - Akashi: "/login" then "<user> <pass>" answering its prompt
//   - KFO-Server: "/login <pass>" — password only, no username
//   - Athena/Nyathena/Whisker/unknown: "/login <user> <pass>"
func (a *App) loginLines(user, pass string) []string {
	soft := ""
	if a.sess != nil {
		soft = strings.ToLower(a.sess.Software)
	}
	switch {
	case strings.Contains(soft, "akashi"):
		// Two-step: the bare command, then the credential line answering
		// Akashi's prompt (which the server does not echo into OOC).
		return []string{"/login", user + " " + pass}
	case strings.Contains(soft, "kfo"):
		return []string{"/login " + pass}
	}
	return []string{"/login " + user + " " + pass}
}

// loginNow queues the login flow from the saved credentials.
func (a *App) loginNow() {
	info := a.d.Prefs.ServerWarmInfoFor(a.serverKey)
	if info.LoginUser == "" {
		a.pushDebug("login: no saved credentials for this server (Login... dialog)")
		return
	}
	a.queueOOCLines(a.loginLines(info.LoginUser, info.LoginPass))
	a.pushDebug("login: flow queued as " + a.oocNameOrDefault())
}

// loginFlowPreview names the exact wire flow the dialog will use, so the
// automation explains itself before you trust it with credentials.
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

// autoLoginOnReady fires the saved flow once per join when enabled.
func (a *App) autoLoginOnReady() {
	if info := a.d.Prefs.ServerWarmInfoFor(a.serverKey); info.AutoLogin && info.LoginUser != "" {
		a.loginNow()
	}
}

// drawLoginDialog edits this server's credentials (plaintext storage —
// the dialog says so) and fires the flow.
func (a *App) drawLoginDialog(w, h int32) {
	c := a.ctx
	panel := sdl.Rect{X: w/2 - 230, Y: h/2 - 122, W: 460, H: 244}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+10, "Auto-login — "+a.serverName, ColText)
	y := panel.Y + 40
	// The dialog names the exact flow it will run for THIS server's
	// software — the automation explains itself.
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad, a.loginFlowPreview(), ColAccent)
	y += 22
	userField := c.TextField
	if a.d.Prefs.StreamerMode() {
		userField = c.PasswordField // names mask on stream too
	}
	a.loginUser, _ = userField("loginuser", sdl.Rect{X: panel.X + pad, Y: y, W: panel.W - 2*pad, H: fieldH}, a.loginUser, "username")
	y += fieldH + 6
	a.loginPass, _ = c.PasswordField("loginpass", sdl.Rect{X: panel.X + pad, Y: y, W: panel.W - 2*pad, H: fieldH}, a.loginPass, "password")
	y += fieldH + 6
	a.loginAuto = c.Checkbox(panel.X+pad, y, "Auto-login every time I join this server", a.loginAuto)
	y += 24
	c.Label(panel.X+pad, y, "Saved per server, in PLAIN TEXT in your prefs file.", ColTextDim)
	by := panel.Y + panel.H - btnH - 10
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
	info := a.d.Prefs.ServerWarmInfoFor(a.serverKey)
	a.loginUser, a.loginPass, a.loginAuto = info.LoginUser, info.LoginPass, info.AutoLogin
	a.showLogin = true
}

// --- settings: macro editor --------------------------------------------------

// macroLineSeparator splits the editor's one-field line entry ("|" is
// vanishingly rare in OOC commands; documented in the placeholder).
const macroLineSeparator = "|"

// drawMacroSettings renders the macro list + editor. Returns the next y.
func (a *App) drawMacroSettings(y int32, w int32) int32 {
	c := a.ctx
	macros := a.d.Prefs.Macros()
	c.Label(pad, y+4, fmt.Sprintf("Macros (%d/%d) — chain OOC commands with %q and fire the whole chain from one key:", len(macros), config.MacroCap, macroLineSeparator), ColText)
	y += 26

	removeIdx := -1
	for i, m := range macros {
		key := m.Key
		if key == "" {
			key = "—"
		}
		line := fmt.Sprintf("[%s] %s: %s", key, m.Name, strings.Join(m.Lines, " "+macroLineSeparator+" "))
		c.LabelClipped(pad+12, y+3, w-pad*2-80, clampLine(line), ColTextDim)
		if c.Button(sdl.Rect{X: w - pad - scrollBarW - 56, Y: y, W: 50, H: 22}, "✕") {
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
	keyLabel := "key: " + settings.macroKey
	if settings.macroKey == "" {
		keyLabel = "set key"
	}
	if a.macroBind >= 0 {
		keyLabel = "press..."
	}
	if c.Button(sdl.Rect{X: pad + 136, Y: y, W: 86, H: btnH}, keyLabel) {
		a.macroBind = 0 // arm capture (Esc cancels)
		a.ctx.focusID = ""
	}
	settings.macroLines, _ = c.TextField("macrolines", sdl.Rect{X: pad + 228, Y: y, W: w - pad*2 - 228 - 130 - scrollBarW, H: fieldH},
		settings.macroLines, `/cm `+macroLineSeparator+` /area 2 `+macroLineSeparator+` /tsundere 1   (each `+macroLineSeparator+` step sends as its own ENTER)`)
	if c.Button(sdl.Rect{X: w - pad - scrollBarW - 124, Y: y, W: 118, H: btnH}, "Add macro") {
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
	c.Label(pad, y, "Sends go to OOC as your OOC name (or AsyncAO<n> when unset); key fires with no text box focused.", ColTextDim)
	return y + 24
}

// drawLoginSettings is the Settings section for the built-in login:
// pick ANY known server (connected or not) and configure its account
// ahead of time — the credentials wait in its per-server slot and fire
// (auto or manual) the next time you join. Returns the next y.
func (a *App) drawLoginSettings(y, w int32) int32 {
	c := a.ctx
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

	// Load the picked server's saved slots once per pick.
	if !settings.loginLoaded {
		info := a.d.Prefs.ServerWarmInfoFor(settings.loginKey)
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
	if a.d.Prefs.StreamerMode() {
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

// loginTargets lists the login picker's servers: the connected one
// first, then every joinable lobby/favorite entry. Cached against the
// (server count, active key) pair — WebSocketURL allocates per entry.
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

// syncLoginBuffers loads this server's saved credentials into the edit
// buffers (connect time, so the settings boxes show the truth).
func (a *App) syncLoginBuffers() {
	info := a.d.Prefs.ServerWarmInfoFor(a.serverKey)
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
