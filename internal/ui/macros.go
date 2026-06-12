package ui

// Macro engine + built-in server login.
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

// macroSend is one queued OOC line.
type macroSend struct {
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

// queueMacroLines schedules lines for paced OOC sending.
func (a *App) queueMacroLines(lines []string) {
	if a.sess == nil {
		return
	}
	now := a.now()
	for i, line := range lines {
		if len(a.macroQueue) >= macroQueueCap {
			a.pushDebug("macro queue full — dropping the rest")
			return
		}
		a.macroQueue = append(a.macroQueue, macroSend{
			line: line,
			due:  now.Add(time.Duration(i) * macroLineDelay),
		})
	}
}

// processMacroQueue sends due lines (called once per frame; the queue
// belongs to the active session and pauses while its tab is parked).
func (a *App) processMacroQueue() {
	if len(a.macroQueue) == 0 || a.sess == nil {
		return
	}
	now := a.now()
	sent := 0
	for _, m := range a.macroQueue {
		if m.due.After(now) {
			break
		}
		a.sess.SendOOC(a.oocNameOrDefault(), m.line)
		sent++
	}
	if sent > 0 {
		a.macroQueue = a.macroQueue[:copy(a.macroQueue, a.macroQueue[sent:])]
	}
}

// runMacro queues one macro and reports it on the debug lane (lines may
// hold credentials — never echo their content).
func (a *App) runMacro(m config.MacroSpec) {
	a.queueMacroLines(m.Lines)
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

// loginLines builds the wire flow for this server's software.
func (a *App) loginLines(user, pass string) []string {
	soft := ""
	if a.sess != nil {
		soft = strings.ToLower(a.sess.Software)
	}
	if strings.Contains(soft, "akashi") {
		// Two-step: the bare command, then the credential line answering
		// Akashi's prompt (which the server does not echo into OOC).
		return []string{"/login", user + " " + pass}
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
	a.queueMacroLines(a.loginLines(info.LoginUser, info.LoginPass))
	a.pushDebug("login: flow queued as " + a.oocNameOrDefault())
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
	panel := sdl.Rect{X: w/2 - 230, Y: h/2 - 110, W: 460, H: 220}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+10, "Server login — "+a.serverName, ColText)
	y := panel.Y + 44
	a.loginUser, _ = c.TextField("loginuser", sdl.Rect{X: panel.X + pad, Y: y, W: panel.W - 2*pad, H: fieldH}, a.loginUser, "username")
	y += fieldH + 6
	a.loginPass, _ = c.TextField("loginpass", sdl.Rect{X: panel.X + pad, Y: y, W: panel.W - 2*pad, H: fieldH}, a.loginPass, "password")
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
	c.Label(pad, y+4, fmt.Sprintf("Macros (%d/%d) — OOC lines, optional key, fired in the courtroom:", len(macros), config.MacroCap), ColText)
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
		settings.macroLines, `/login user pass   (multi-step: /login `+macroLineSeparator+` user pass)`)
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
// per-server credentials with the auto/manual difference spelled out.
// Returns the next y.
func (a *App) drawLoginSettings(y, w int32) int32 {
	c := a.ctx
	c.Label(pad, y+4, "Server login (saved per server; flow auto-detected: Akashi = 2-step prompt, others = /login user pass):", ColText)
	y += 24
	if a.sess == nil || a.serverKey == "" {
		c.Label(pad+12, y, "Connect to a server to set its login — each server keeps its own credentials.", ColTextDim)
		return y + 26
	}
	info := a.d.Prefs.ServerWarmInfoFor(a.serverKey)
	state := "no credentials saved"
	if info.LoginUser != "" {
		state = "credentials saved for " + info.LoginUser
		if info.AutoLogin {
			state += " — AUTO-LOGIN ON (logs in instantly on every join)"
		} else {
			state += " — manual (Login... button or Ctrl+" + strings.ToUpper(a.hotkeyFor(hotkeyLogin)) + ")"
		}
	}
	c.LabelClipped(pad+12, y, w-2*pad-140, a.serverName+": "+state, ColTextDim)
	if c.Button(sdl.Rect{X: w - pad - scrollBarW - 120, Y: y - 4, W: 114, H: btnH}, "Edit login...") {
		a.openLoginDialog()
	}
	y += 26
	c.Label(pad+12, y, "Auto-login fires the flow by itself the moment a join finishes (off by default).", ColTextDim)
	y += 18
	c.Label(pad+12, y, "Manual = same saved flow, but only when YOU trigger it — hotkey or the courtroom Login... button.", ColTextDim)
	return y + 26
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
