package ui

// Quality-of-life: configurable Ctrl-chord hotkeys, callword alerts, IC
// log search/copy/export, screenshots, music stop. Render thread except
// the explicitly off-thread file writes.

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Hotkey actions (persisted overrides in Prefs.Hotkeys; all fire on
// Ctrl+<key>).
const (
	hotkeyHoldIt      = "holdit"
	hotkeyObjection   = "objection"
	hotkeyTakeThat    = "takethat"
	hotkeyCustom      = "custom"
	hotkeyPosCycle    = "pos_cycle"
	hotkeyMusicStop   = "music_stop"
	hotkeyLogJump     = "log_jump"
	hotkeyScreenshot  = "screenshot"
	hotkeyRecordScene = "record_scene"
	hotkeyReplayLast  = "replay_last"
	hotkeyTheater     = "theater"
	hotkeyLogin       = "login"
	hotkeyMuteSFX     = "mute_sfx"
	hotkeyQuickSwap   = "quick_swap"
	hotkeyEmoteCycle  = "emote_cycle"
	hotkeyPinNote     = "pin_note"
	hotkeyFriendHi    = "friend_toggle"
	hotkeyExtras      = "extras"
	// Per-menu shortcuts: jump straight to an Extras menu (a legacy AO2 theme has
	// no button for these), skipping the Extras box if you know the key.
	hotkeyCharMenu      = "char_menu"
	hotkeyWardrobe      = "wardrobe"
	hotkeyJukebox       = "jukebox"
	hotkeyBackground    = "background"
	hotkeyEvidence      = "evidence"
	hotkeyPairMenu      = "pair_menu"
	hotkeyModcall       = "modcall"
	hotkeyUIChrome      = "ui_chrome"
	hotkeySettings      = "settings_menu"
	hotkeyRandomChar    = "random_char"
	hotkeyVolDown       = "vol_down" // master volume −/+ a step (quick volume from the keyboard)
	hotkeyVolUp         = "vol_up"
	hotkeyShownameRand  = "showname_rand"  // swap to a random saved showname preset (M6)
	hotkeyShownameCycle = "showname_cycle" // cycle to the next saved showname preset
	hotkeyDND           = "dnd"            // toggle Do Not Disturb (mute callword + friend pings) (M15)
	hotkeyReshowSprites = "reshow_sprites" // un-hide all sprites hidden this session (right-click-to-hide)
	hotkeyHideDesk      = "hide_desk"      // toggle hiding the courtroom desk
	hotkeyQuickConnect  = "quick_connect"  // dial the saved last server (works offline / in the lobby)
)

// volumeKeyStep is how much the master-volume hotkeys nudge per press (percent).
const volumeKeyStep = 5

// hotkeyDefs drives both dispatch and the Settings rows: id, label, and
// the default key (per the original request: shouts on Ctrl+1..4).
var hotkeyDefs = []struct {
	id, label, def string
}{
	{hotkeyHoldIt, "Hold It! shout", "1"},
	{hotkeyObjection, "Objection! shout", "2"},
	{hotkeyTakeThat, "Take That! shout", "3"},
	{hotkeyCustom, "Custom shout", "4"},
	{hotkeyPosCycle, "Cycle position", "p"},
	{hotkeyMusicStop, "Stop music", "m"},
	{hotkeyLogJump, "Jump logs to newest", "l"},
	{hotkeyScreenshot, "Screenshot", "s"},
	{hotkeyRecordScene, "Record scene to a replay file (start/stop)", "w"},
	{hotkeyReplayLast, "Replay the last recording (start/stop)", "i"},
	{hotkeyTheater, "Theater mode", "t"},
	{hotkeyLogin, "Server login (saved creds)", "g"},
	{hotkeyMuteSFX, "Mute sound effects", "k"},
	{hotkeyQuickSwap, "Quick-swap character (cycle wardrobe)", "j"},
	{hotkeyEmoteCycle, "Cycle emote (next)", "e"},
	{hotkeyPinNote, "Pin hovered log line to notes", "n"},
	{hotkeyFriendHi, "Toggle friend highlights", "u"},
	{hotkeyExtras, "Open the Extras menu (AsyncAO features box)", "x"},
	// Direct menu shortcuts (skip the Extras box). Common menus on Ctrl+5..0.
	{hotkeyCharMenu, "Menu: Characters", "5"},
	{hotkeyWardrobe, "Menu: Wardrobe", "6"},
	{hotkeyJukebox, "Menu: Jukebox", "7"},
	{hotkeyBackground, "Menu: Background", "8"},
	{hotkeyEvidence, "Menu: Evidence", "9"},
	{hotkeyPairMenu, "Menu: Pairing", "0"},
	{hotkeyModcall, "Menu: Call mod", "o"},
	{hotkeyUIChrome, "Menu: UI chrome", "f"},
	{hotkeySettings, "Menu: Settings", ","}, // Ctrl+, (prefs convention); NOT z — that's the layout-editor undo
	{hotkeyRandomChar, "Random character", "r"},
	{hotkeyVolDown, "Master volume down", "-"},          // Ctrl+-  (quieter)
	{hotkeyVolUp, "Master volume up", "="},              // Ctrl+=  (louder)
	{hotkeyShownameRand, "Random showname preset", "h"}, // Ctrl+H (rebindable)
	{hotkeyShownameCycle, "Cycle showname preset", "b"}, // Ctrl+B (rebindable)
	{hotkeyDND, "Do Not Disturb (mute pings)", "d"},     // Ctrl+D — session-only, rebindable
	{hotkeyReshowSprites, "Reshow hidden sprites", "y"}, // un-hide all right-click-hidden sprites
	{hotkeyHideDesk, "Hide / show the desk", "v"},       // toggle desk rendering
	{hotkeyQuickConnect, "Connect to last server", "q"}, // dial the saved server (lobby)
}

// hotkeyFor resolves an action's key name (pref override or default).
func (a *App) hotkeyFor(action string) string {
	if key := a.d.Prefs.Hotkey(action); key != "" {
		return strings.ToLower(key)
	}
	for _, def := range hotkeyDefs {
		if def.id == action {
			return def.def
		}
	}
	return ""
}

// hotkeyConflictKeys returns the set of keys bound to more than one action. The
// dispatch switch in handleHotkeys matches the first case for a key, so a clash
// silently dead-ends the later action(s) — the Controls tab flags these keys so
// a rebind collision is visible, not a mystery.
func (a *App) hotkeyConflictKeys() map[string]bool {
	count := make(map[string]int, len(hotkeyDefs))
	for _, def := range hotkeyDefs {
		if k := a.hotkeyFor(def.id); k != "" {
			count[k]++
		}
	}
	out := map[string]bool{}
	for k, n := range count {
		if n > 1 {
			out[k] = true
		}
	}
	return out
}

// refreshCharKeys re-reads this server's character keybinds into the
// per-frame lookup caches (connect + bind edits only — never per frame).
func (a *App) refreshCharKeys() {
	a.charKeys = a.d.Prefs.CharKeyBinds(a.serverKey)
	if len(a.charKeys) == 0 {
		a.charKeysRev = nil
		return
	}
	a.charKeysRev = make(map[string]string, len(a.charKeys))
	for k, ch := range a.charKeys {
		a.charKeysRev[ch] = k
	}
}

// charKeyFor reports the key bound to a character on this server ("" =
// none) — the wardrobe badge lookup.
func (a *App) charKeyFor(char string) string { return a.charKeysRev[char] }

// pollCharBind completes an armed wardrobe key-capture: the next plain
// keypress binds key → character on this server; Esc cancels.
func (a *App) pollCharBind() {
	if a.bindingFor == "" {
		return
	}
	c := a.ctx
	if c.escPressed {
		a.bindingFor = ""
		return
	}
	if c.keyPressed == 0 {
		return
	}
	key := strings.ToLower(sdl.GetKeyName(c.keyPressed))
	a.d.Prefs.SetCharKeyBind(a.serverKey, key, a.bindingFor)
	a.pushDebug(fmt.Sprintf("key %q now wears %s (this server only)", key, a.bindingFor))
	a.bindingFor = ""
	a.refreshCharKeys()
}

// handleCharKeys wears a bound character on a bare keypress — only with
// no text field focused, no capture armed, and no Ctrl chord in flight,
// so typing can never trigger a swap.
func (a *App) handleCharKeys() {
	c := a.ctx
	if c.keyPressed == 0 || c.focusID != "" || a.bindingFor != "" || c.ctrlHeld {
		return
	}
	if name := a.charKeys[strings.ToLower(sdl.GetKeyName(c.keyPressed))]; name != "" {
		a.wearFromMenu(name)
	}
}

// handleEmoteKeys picks the emote in number-key position N on the CURRENT
// page (keys 1-9), but only with no text field focused (so typing a number
// never switches emotes), no Ctrl chord in flight, and only when the digit
// isn't a deliberate character keybind — the same fence as handleCharKeys.
// Picking focuses the IC input, matching a click.
func (a *App) handleEmoteKeys() {
	c := a.ctx
	if c.keyPressed < sdl.K_1 || c.keyPressed > sdl.K_9 || c.focusID != "" || c.ctrlHeld || a.emotePerPage <= 0 {
		return
	}
	if a.charKeys[strings.ToLower(sdl.GetKeyName(c.keyPressed))] != "" {
		return // a character keybind owns this digit
	}
	// Map the on-screen slot through the visible list (favs-only filters it),
	// so the number keys pick exactly what that grid cell shows.
	a.refreshEmoteView()
	slot := a.emotePage*a.emotePerPage + int(c.keyPressed-sdl.K_1)
	if slot >= 0 && slot < len(a.emoteVisible) {
		a.selectEmote(a.emoteVisible[slot])
	}
}

// handleHotkeys consumes this frame's Ctrl chord on the courtroom screen
// (and dispatches macro keybinds, then character keybinds — macros win
// a key conflict since they were bound deliberately).
func (a *App) handleHotkeys() {
	if !a.handleMacroKeys() && !a.handleJukeboxKeys() && !a.handleShownameKeys() {
		a.handleCharKeys()
		a.handleEmoteKeys()
	}
	key := a.ctx.hotkey
	if key == 0 || a.sess == nil {
		return
	}
	name := strings.ToLower(sdl.GetKeyName(key))
	switch name {
	case a.hotkeyFor(hotkeyHoldIt):
		a.sendIC(protocol.ShoutHoldIt)
	case a.hotkeyFor(hotkeyObjection):
		a.sendIC(protocol.ShoutObjection)
	case a.hotkeyFor(hotkeyTakeThat):
		a.sendIC(protocol.ShoutTakeThat)
	case a.hotkeyFor(hotkeyCustom):
		a.sendIC(protocol.ShoutCustom)
	case a.hotkeyFor(hotkeyPosCycle):
		a.cyclePos()
	case a.hotkeyFor(hotkeyMusicStop):
		a.stopMusic()
	case a.hotkeyFor(hotkeyVolDown):
		a.nudgeMasterVolume(-volumeKeyStep)
	case a.hotkeyFor(hotkeyVolUp):
		a.nudgeMasterVolume(volumeKeyStep)
	case a.hotkeyFor(hotkeyLogJump):
		a.jumpLogs()
	case a.hotkeyFor(hotkeyScreenshot):
		a.captureScreenshot()
	case a.hotkeyFor(hotkeyRecordScene):
		a.toggleRecording()
	case a.hotkeyFor(hotkeyReplayLast):
		a.toggleReplay()
	case a.hotkeyFor(hotkeyTheater):
		a.setTheater(!a.theaterOn)
	case a.hotkeyFor(hotkeyLogin):
		a.loginNow()
	case a.hotkeyFor(hotkeyMuteSFX):
		a.toggleSFXMute()
	case a.hotkeyFor(hotkeyQuickSwap):
		a.quickSwapNext()
	case a.hotkeyFor(hotkeyEmoteCycle):
		a.cycleEmote(1)
	case a.hotkeyFor(hotkeyFriendHi):
		on := !a.d.Prefs.FriendHighlightOn()
		a.d.Prefs.SetFriendHighlight(on)
		a.warnLine = "Friend highlights off"
		if on {
			a.warnLine = "Friend highlights on"
		}
		a.warnAt = time.Now()
	case a.hotkeyFor(hotkeyExtras):
		a.showWidgets = !a.showWidgets
	case a.hotkeyFor(hotkeyCharMenu):
		a.screen = ScreenCharSelect
	case a.hotkeyFor(hotkeyWardrobe):
		a.openIniswap()
	case a.hotkeyFor(hotkeyJukebox):
		a.openIniswap()
		a.wardSection = wardSectionJukebox
	case a.hotkeyFor(hotkeyBackground):
		a.openBgPicker()
	case a.hotkeyFor(hotkeyEvidence):
		a.showEvid = true
	case a.hotkeyFor(hotkeyPairMenu):
		a.showPair = true
	case a.hotkeyFor(hotkeyModcall):
		a.showModcall = true
	case a.hotkeyFor(hotkeyUIChrome):
		a.showUICfg = true
	case a.hotkeyFor(hotkeySettings):
		a.prevScreen = ScreenCourtroom
		a.screen = ScreenSettings
	case a.hotkeyFor(hotkeyRandomChar):
		a.randomChar()
	case a.hotkeyFor(hotkeyShownameRand):
		a.randomShowname()
	case a.hotkeyFor(hotkeyShownameCycle):
		a.cycleShowname()
	case a.hotkeyFor(hotkeyDND):
		a.setDND(!a.dndOn)
		a.warnLine = "Do Not Disturb off"
		if a.dndOn {
			a.warnLine = "Do Not Disturb ON — callword + friend pings muted"
		}
		a.warnAt = time.Now()
	case a.hotkeyFor(hotkeyReshowSprites):
		a.reshowSprites()
	case a.hotkeyFor(hotkeyHideDesk):
		a.toggleHideDesk()
	}
}

// toggleHideDesk flips the hide-desk option (Settings + the Hide/show desk key).
func (a *App) toggleHideDesk() {
	on := !a.d.Prefs.HideDeskOn()
	a.d.Prefs.SetHideDesk(on)
	if on {
		a.warnLine = "Desk hidden"
	} else {
		a.warnLine = "Desk showing"
	}
	a.warnAt = time.Now()
}

// quickSwapNext wears the next character in this server's wardrobe ring (the
// starred set, in the same alphabetical order the wardrobe menu shows). It
// advances from whatever's worn now — so the cycle stays intuitive even after
// a manual pick — and wraps. Empty wardrobe = a hint, no swap. The wardrobe is
// per server, so the ring is too.
func (a *App) quickSwapNext() {
	ring, _, _ := mergeWardrobe(a.d.Prefs.WardrobeList(a.serverKey), nil)
	if len(ring) == 0 {
		a.warnLine = "Quick-swap: no wardrobe characters yet — star some with ★ in the Wardrobe"
		a.warnAt = time.Now()
		return
	}
	next := 0 // current char not in the ring (or nothing worn) → start at the first
	for i, n := range ring {
		if strings.EqualFold(n, a.iniChar) {
			next = (i + 1) % len(ring)
			break
		}
	}
	a.wearFromMenu(ring[next])
	a.warnLine = fmt.Sprintf("Quick-swap: %s (%d/%d)", ring[next], next+1, len(ring))
	a.warnAt = time.Now()
}

// toggleSFXMute flips a session-only SFX mute and re-applies volumes. The
// music/blip channels are untouched; the saved SFX volume is preserved (mute
// is not persisted — it's a quick "shush" for one session).
func (a *App) toggleSFXMute() {
	a.sfxMuted = !a.sfxMuted
	a.applyAudioVolumes()
	a.warnLine = "SFX unmuted"
	if a.sfxMuted {
		a.warnLine = "SFX muted (press the Mute SFX hotkey again to restore)"
	}
	a.warnAt = time.Now()
}

// duckMusicPercent is how loud music plays (as a % of its set volume) while a
// message is on stage, when music ducking is on.
const duckMusicPercent = 35

// applyAudioVolumes pushes the saved volumes to the audio system, applying the
// session SFX mute (SFX → 0) and music ducking (music → duckMusicPercent)
// when active. Call it wherever those states change so they're always honored.
func (a *App) applyAudioVolumes() {
	music, sfx, blip := a.d.Prefs.AudioVolumes()
	alert := a.d.Prefs.AlertVolume() // callword/friend ping — independent of SFX
	if a.sfxMuted {
		sfx = 0
	}
	if a.musicDucked {
		music = music * duckMusicPercent / 100
	}
	// Master multiplier scales everything (composes over mute/duck) — the one
	// "too loud / too quiet" knob.
	if m := a.d.Prefs.MasterVolume(); m != 100 {
		music, sfx, blip = music*m/100, sfx*m/100, blip*m/100
		alert = alert * m / 100
	}
	a.d.Audio.SetVolumes(music, sfx, blip)
	a.d.Audio.SetAlertVolume(alert)
}

// nudgeMasterVolume steps the master volume from the keyboard (Ctrl+-/Ctrl+=),
// clamps it, applies it, and toasts the new level. Master scales every channel,
// so this is the quick "turn it down/up" without reaching for a slider.
func (a *App) nudgeMasterVolume(delta int) {
	v := clampInt(a.d.Prefs.MasterVolume()+delta, 0, 100)
	a.d.Prefs.SetMasterVolume(v)
	a.applyAudioVolumes()
	a.warnLine = "Master volume: " + strconv.Itoa(v) + "%"
	a.warnAt = a.now()
}

// drawHotkeyCheatSheet overlays every binding (F1 or the Extras "Hotkeys" entry
// opens it). It lists the configurable Ctrl-chord actions resolved to the user's
// keys AND their own custom bindings — macros, character keys, showname keys —
// with section headers, in two columns so a long list fits the window. Your own
// bindings (a remapped action or anything you created) show their key in gold so
// they stand out from the defaults. Rows are cached per open (hkCache); drawing
// only happens while open, so it costs nothing closed.
func (a *App) drawHotkeyCheatSheet(w, h int32) {
	c := a.ctx
	if a.hkCache == nil { // opened without openHotkeyCheatSheet (defensive)
		a.hkCache = a.hotkeyCheatEntries()
	}
	entries := a.hkCache
	const (
		rowH   = int32(20)
		colW   = int32(360)
		keyGap = int32(116) // label x-offset within a column
	)
	rowsPerCol := (int32(len(entries)) + 1) / 2
	if rowsPerCol < 1 {
		rowsPerCol = 1
	}
	pw := colW*2 + pad*3
	ph := rowsPerCol*rowH + 48
	if max := h - 20; ph > max { // never taller than the window
		ph = max
	}
	panel := sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	c.Fill(panel, sdl.Color{R: 12, G: 12, B: 18, A: 245})
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+6, "Hotkeys — your shortcuts", ColText)
	if c.Button(sdl.Rect{X: panel.X + pw - 28, Y: panel.Y + 6, W: 20, H: 20}, "x") {
		a.showHotkeys = false
		a.hkCache = nil
	}
	c.Label(panel.X+pw-150, panel.Y+10, "F1 to close", ColTextDim)

	colTop := panel.Y + 34
	for i, e := range entries {
		col := int32(i) / rowsPerCol
		row := int32(i) % rowsPerCol
		x := panel.X + pad + col*(colW+pad)
		y := colTop + row*rowH
		if y+rowH > panel.Y+panel.H-4 {
			continue // clip rather than overflow (the height cap keeps this rare)
		}
		if e.header {
			c.Label(x, y, e.label, ColStar)
			continue
		}
		keyCol := ColAccent
		if e.custom {
			keyCol = ColStar // a binding you remapped or created
		}
		c.Label(x, y, e.key, keyCol)
		c.LabelClipped(x+keyGap, y, colW-keyGap, e.label, ColText)
	}
}

// theaterHint is the only chrome theater mode draws.
const theaterHint = "Esc exits theater"

// drawTheater is the borderless viewport-only courtroom: the stage
// letterboxed to AO's 4:3 in a black surround, the chat overlay (it IS
// the show), hotkeys, and nothing else. Esc or the theater hotkey exits.
func (a *App) drawTheater(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{A: 255})
	vpW, vpH := w, w*3/4
	if vpH > h {
		vpH = h
		vpW = vpH * 4 / 3
	}
	vp := sdl.Rect{X: (w - vpW) / 2, Y: (h - vpH) / 2, W: vpW, H: vpH}
	a.renderViewportZoomed(vp)
	chatBandH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	a.handleViewportZoom(vp, c.mouseY >= vp.Y+vp.H-chatBandH)
	if a.vpZoom <= 1 {
		a.handleSpriteDrag(vp)
		a.handleSpriteHide(vp) // right-click → hide-sprite confirm (default ON)
	}
	a.handleHotkeys()
	a.drawChatOverlay(vp)
	a.drawCourtOverlays(vp, nil) // splashes/HP still play — part of the show
	c.Label(w-c.TextWidth(theaterHint)-8, 6, theaterHint, ColTextDim)
	if c.escPressed {
		a.setTheater(false)
	}
}

// cyclePos advances our side through posChoices (the SD list when sent).
func (a *App) cyclePos() {
	choices := a.posChoices()
	cur := a.mySide()
	next := choices[0]
	for i, p := range choices {
		if p == cur {
			next = choices[(i+1)%len(choices)]
			break
		}
	}
	a.sidePref = next
}

// stopMusic stops the music. It HALTS OUR playback immediately, then also asks
// the server to stop the area music (AO has no stop packet, so AO2-Client
// requests a fake track: "~stop.mp3", or the first extension-less list entry a
// server recognizes — courtroom.cpp music_stop). The local halt is what makes
// the button reliable: the server-side request often fails (the server may not
// have the fake track), and a listener should be able to silence music in their
// own client regardless of DJ rights.
func (a *App) stopMusic() {
	a.d.Audio.StopMusic()
	if a.room != nil {
		a.room.Scene.MusicTrack = "" // clear the Now-Playing display
	}
	if a.sess == nil {
		return
	}
	const fakeStopSong = "~stop.mp3"
	track := fakeStopSong
	found := false
	for _, song := range a.sess.Music {
		if song == fakeStopSong {
			found = true
			break
		}
	}
	if !found {
		for _, song := range a.sess.Music {
			if !strings.Contains(song, ".") {
				track = song
				break
			}
		}
	}
	a.sess.RequestMusic(track)
}

// jumpLogs snaps every scrollback to its newest line.
func (a *App) jumpLogs() {
	const snapToTail = 1 << 30 // clamped by each scrollbar
	a.icScroll = snapToTail
	a.oocScroll = snapToTail
	a.icStick, a.oocStick = true, true // jumping to newest re-arms follow
}

// --- callwords ----------------------------------------------------------------------

// loginCallwordGrace is how long after a login flow we suppress callword alerts,
// covering the paced login lines plus the server's (name-echoing) confirmation.
// Short on purpose: a genuine callword a few seconds after join still pings.
const loginCallwordGrace = 5 * time.Second

// inLoginGrace reports whether we're inside the post-login callword grace window.
func (a *App) inLoginGrace() bool {
	return !a.loginAt.IsZero() && a.now().Sub(a.loginAt) < loginCallwordGrace
}

// checkCallwords flashes + pings when a configured highlight word appears
// in IC/OOC traffic (AO2-Client callwords: get_court_sfx("word_call")).
// Streamer mode suppresses both — an on-stream name ping is exactly the
// leak the toggle exists to prevent.
func (a *App) checkCallwords(text string) {
	if a.d.Prefs.StreamerMode() || a.dndOn {
		return
	}
	// Brief grace after a login flow: the server's login replies (and the Akashi
	// prompt) routinely echo your handle/name, which would self-ping your callword
	// the instant you join. The window is short so a real callword seconds later
	// still alerts.
	if a.inLoginGrace() {
		return
	}
	words := a.d.Prefs.CallWords()
	if len(words) == 0 || text == "" {
		return
	}
	lower := strings.ToLower(text)
	for _, w := range words {
		if strings.Contains(lower, w) {
			// Custom sound if set, else the built-in ping — ALWAYS audible. We
			// deliberately do NOT route through the theme's word_call: a theme
			// that names word_call but ships no (loadable) file would play
			// nothing, silencing the alert — the exact "callwords don't work"
			// report. The built-in ping is the reliable default.
			if f := a.d.Prefs.CallwordSoundPath(); f != "" {
				a.d.Audio.PlayFile(f)
			} else {
				a.d.Audio.PlayAlert()
			}
			// Optional in-app toast naming the word (like the modcall/friend
			// toasts) so you can see WHY it pinged, not just hear it.
			if a.d.Prefs.CallwordToastOn() {
				a.warnLine = clampLine("Heard your callword: " + w)
				a.warnAt = time.Now()
			}
			a.ctx.FlashWindow()
			return
		}
	}
}

// --- screenshots ----------------------------------------------------------------------

// captureScreenshot reads the back buffer (render thread — required for
// ReadPixels) and writes a PNG off-thread next to the exe under screenshots\
// (§17.2: file I/O never blocks the render thread). PNG over BMP: ~10× smaller
// and it previews inline everywhere (Discord, etc.) — the point is to share it.
func (a *App) captureScreenshot() {
	w, h, err := a.ctx.Ren.GetOutputSize()
	if err != nil {
		a.pushDebug("screenshot: " + err.Error())
		return
	}
	surf, err := sdl.CreateRGBSurfaceWithFormat(0, w, h, 32, uint32(sdl.PIXELFORMAT_ABGR8888))
	if err != nil {
		a.pushDebug("screenshot: " + err.Error())
		return
	}
	if err := a.ctx.Ren.ReadPixels(nil, surf.Format.Format, surf.Data(), int(surf.Pitch)); err != nil {
		surf.Free()
		a.pushDebug("screenshot: " + err.Error())
		return
	}
	stamp := time.Now().Format("20060102-150405")
	rel := "screenshots\\asyncao-" + stamp + ".png"
	pitch := int(surf.Pitch)
	go func() {
		defer surf.Free()
		// Copy the surface bytes into Go memory so the encode is decoupled from
		// the SDL surface's lifetime (ABGR8888 == image.RGBA byte order).
		pix := make([]byte, len(surf.Pixels()))
		copy(pix, surf.Pixels())
		exe, err := os.Executable()
		if err != nil {
			return
		}
		dir := filepath.Join(filepath.Dir(exe), "screenshots")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = writeScreenshotPNG(filepath.Join(dir, "asyncao-"+stamp+".png"), pix, int(w), int(h), pitch)
	}()
	a.warnLine = "Screenshot saved: " + rel
	a.warnAt = time.Now()
	a.pushDebug("screenshot saved: " + rel)
}

// writeScreenshotPNG encodes ABGR8888 back-buffer bytes (already in image.RGBA
// byte order) as a PNG at path. Pure + off the render thread; pitch is the row
// stride (≥ w*4, may carry alignment padding).
func writeScreenshotPNG(path string, pix []byte, w, h, pitch int) error {
	img := &image.RGBA{Pix: pix, Stride: pitch, Rect: image.Rect(0, 0, w, h)}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// --- streamer mode ----------------------------------------------------------------------

// streamerIPRe matches IPv4-looking tokens (modcall broadcasts on some
// servers embed caller IPs).
var streamerIPRe = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// streamerMaskLine redacts one OOC display line: the sender prefix
// becomes ???, IP-like tokens block out. Display-time only — the raw log
// is untouched, so switching the mode off restores everything.
func streamerMaskLine(line string) string {
	if name, rest, found := strings.Cut(line, ": "); found && len(name) <= streamerNameMax {
		line = "???: " + rest
		_ = name
	}
	return streamerIPRe.ReplaceAllString(line, "█.█.█.█")
}

// streamerNameMax bounds what counts as a "name:" prefix — a colon deep
// inside a sentence is message text, not a sender.
const streamerNameMax = 32

// --- OOC wrapping ----------------------------------------------------------------------

// oocWrapMaxLinesPerEntry bounds one entry's wrapped output (a hostile
// MOTD cannot balloon the list).
const oocWrapMaxLinesPerEntry = 24

// oocWrapped returns the OOC log as display lines: long entries (MOTDs)
// word-wrap to the list width and embedded newlines split, instead of the
// old 120-char truncation. Streamer mode masks sender names and IPs here
// (display only). Cached against (log seq, width, font scale, mask) —
// rebuilds happen on new messages or resizes, never per frame.
func (a *App) oocWrapped(width int32) []string {
	streamer := a.d.Prefs.StreamerMode()
	if a.oocWrap != nil && a.oocWrapSeq == a.oocSeq && a.oocWrapGen == a.ctx.fontChainGen &&
		a.oocWrapW == width && a.oocWrapPct == a.logPct && a.oocWrapMask == streamer {
		return a.oocWrap
	}
	out := a.oocWrap[:0]
	name := a.oocWrapName[:0] // parallel to out: speaker on each entry's first display line
	urls := a.oocWrapURL[:0]  // parallel to out: the entry's link on each of its display lines
	for i, entry := range a.oocLog {
		sp := ""
		if i < len(a.oocSpeakers) {
			sp = a.oocSpeakers[i]
		}
		entryFirst := true // the speaker tints only the entry's FIRST display line
		for _, rawPara := range strings.Split(entry, "\n") {
			// Link PER PARAGRAPH, from the UNMASKED line: a multi-line entry with a
			// URL on each line (a server's fork/upstream description) makes each line
			// open its OWN link, not the entry's first one. A long URL the wrap
			// hard-splits below is still captured whole here — extractURLs runs on the
			// full paragraph, never a wrapped fragment.
			paraURL := ""
			if u := extractURLs(rawPara, 1); len(u) > 0 {
				paraURL = u[0]
			}
			para := rawPara
			if streamer {
				para = streamerMaskLine(para)
			}
			// Per-paragraph font pick: wraps measure with the font that will draw
			// the line (the CJK chain rule).
			font := a.ctx.LogFontFor(a.logPct, para)
			lines := wrapToWidth(font, strings.TrimRight(para, "\r"), width, oocWrapMaxLinesPerEntry)
			if len(lines) == 0 {
				out = append(out, "") // blank MOTD spacer lines survive
				name = append(name, "")
				urls = append(urls, "")
				continue
			}
			for _, ln := range lines {
				out = append(out, ln)
				urls = append(urls, paraURL) // every wrapped row of THIS line opens its link
				if entryFirst {
					name = append(name, sp)
					entryFirst = false
				} else {
					name = append(name, "")
				}
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	a.oocWrap, a.oocWrapName, a.oocWrapURL = out, name, urls
	a.oocWrapSeq, a.oocWrapW, a.oocWrapPct, a.oocWrapMask = a.oocSeq, width, a.logPct, streamer
	a.oocWrapGen = a.ctx.fontChainGen
	return out
}

// icWrapLine is one display row of the wrapped IC log: a text slice plus
// its source entry (the row inherits that entry's AO color).
type icWrapLine struct {
	text  string
	entry int // index into icLog
}

// icWrapMaxLinesPerEntry bounds one entry's wrapped rows. IC messages cap
// at 256 chars on the wire, so 16 rows survives even huge fonts in a
// narrow column.
const icWrapMaxLinesPerEntry = 16

// icWrapped returns the filtered IC log as display rows wrapped to the
// list width (playtest: lines clipped at the right edge). Cached against
// (log seq, query, width, font scale) — rebuilds on new messages,
// searches, and resizes, never per frame.
func (a *App) icWrapped(width int32, showStamps bool) []icWrapLine {
	if a.icWrap != nil && a.icWrapSeq == a.icLogSeq && a.icWrapQuery == a.logSearch &&
		a.icWrapW == width && a.icWrapPct == a.logPct && a.icWrapGen == a.ctx.fontChainGen &&
		a.icWrapStamp == showStamps {
		return a.icWrap
	}
	out := a.icWrap[:0]
	for _, i := range a.icLogFiltered() {
		// Prefix the local arrival time when enabled. The stamp was formatted once
		// on append; the only cost here is one concat per entry, and only on a wrap
		// REBUILD (new message / search / resize / toggle), never per frame.
		text := a.icLog[i].text
		if showStamps && a.icLog[i].stamp != "" {
			text = a.icLog[i].stamp + "  " + text
		}
		// Wrap with the font that will draw the entry (CJK chain rule).
		font := a.ctx.LogFontFor(a.logPct, text)
		for _, ln := range wrapToWidth(font, text, width, icWrapMaxLinesPerEntry) {
			out = append(out, icWrapLine{text: ln, entry: i})
		}
	}
	if out == nil {
		out = []icWrapLine{} // non-nil marks the cache as populated
	}
	a.icWrap, a.icWrapSeq, a.icWrapQuery, a.icWrapW, a.icWrapPct, a.icWrapGen, a.icWrapStamp =
		out, a.icLogSeq, a.logSearch, width, a.logPct, a.ctx.fontChainGen, showStamps
	return out
}

// wrapToWidth greedily word-wraps text for an arbitrary font (the kit's
// WrapText memo is chrome-font only; this measures with the actual log
// font, and only runs on cache rebuilds). Words wider than the column
// hard-split so a long URL can't force a 1-line overflow. A nil font
// (headless tests) measures at a rough 8 px/char.
func wrapToWidth(font *ttf.Font, text string, maxW int32, maxLines int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var lines []string
	var line strings.Builder
	flush := func() {
		if line.Len() > 0 {
			lines = append(lines, line.String())
			line.Reset()
		}
	}
	width := func(s string) int32 {
		if font == nil {
			return int32(len(s) * 8)
		}
		w, _, err := font.SizeUTF8(s)
		if err != nil {
			return int32(len(s) * 8)
		}
		return int32(w)
	}
	for _, word := range strings.Fields(text) {
		// Hard-split single words wider than the column.
		for width(word) > maxW && len(word) > 1 {
			cut := len(word) / 2
			for cut > 1 && width(word[:cut]) > maxW {
				cut /= 2
			}
			flush()
			lines = append(lines, word[:cut])
			word = word[cut:]
			if len(lines) >= maxLines {
				return lines
			}
		}
		candidate := word
		if line.Len() > 0 {
			candidate = line.String() + " " + word
		}
		if width(candidate) <= maxW {
			line.Reset()
			line.WriteString(candidate)
			continue
		}
		flush()
		if len(lines) >= maxLines {
			return lines
		}
		line.WriteString(word)
	}
	flush()
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}

// --- IC log export --------------------------------------------------------------------

// icLogFiltered returns the indices of log entries matching the search
// box ("" = all), cached against (log seq, query): while the log tab is
// visible the per-frame cost is two comparisons instead of a 1024-line
// scan plus a slice allocation.
func (a *App) icLogFiltered() []int {
	if a.icFilter != nil && a.icFilterSeq == a.icLogSeq && a.icFilterQuery == a.logSearch {
		return a.icFilter
	}
	out := a.icFilter[:0]
	q := strings.ToLower(strings.TrimSpace(a.logSearch))
	for i := range a.icLog {
		if q == "" || strings.Contains(strings.ToLower(a.icLog[i].text), q) {
			out = append(out, i)
		}
	}
	if out == nil {
		out = []int{} // non-nil marks the cache as populated
	}
	a.icFilter, a.icFilterSeq, a.icFilterQuery = out, a.icLogSeq, a.logSearch
	return out
}

// copyICLog puts the (filtered) log on the clipboard.
func (a *App) copyICLog() {
	idx := a.icLogFiltered()
	var b strings.Builder
	for _, i := range idx {
		b.WriteString(a.icLog[i].text)
		b.WriteString("\r\n")
	}
	_ = sdl.SetClipboardText(b.String())
	a.pushDebug(fmt.Sprintf("copied %d log lines to clipboard", len(idx)))
}

// exportICLog writes the full log next to the exe as TXT or HTML (colors
// preserved in HTML via the AO palette). Snapshot on the render thread,
// file I/O off it.
func (a *App) exportICLog(asHTML bool) {
	entries := make([]icEntry, len(a.icLog))
	copy(entries, a.icLog)
	server := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>| `, r) {
			return '-'
		}
		return r
	}, a.serverName)
	stamp := time.Now().Format("20060102-150405")
	go func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		dir := filepath.Join(filepath.Dir(exe), "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		var b strings.Builder
		ext := ".txt"
		if asHTML {
			ext = ".html"
			b.WriteString("<!doctype html><meta charset=\"utf-8\"><title>AsyncAO IC log</title>\n")
			b.WriteString("<body style=\"background:#18181c;color:#ebebeb;font-family:monospace\">\n")
			for _, e := range entries {
				col := icHTMLColor(e.color)
				b.WriteString("<div style=\"color:" + col + "\">" + htmlEscape(e.text) + "</div>\n")
			}
			b.WriteString("</body>\n")
		} else {
			for _, e := range entries {
				b.WriteString(e.text)
				b.WriteString("\r\n")
			}
		}
		path := filepath.Join(dir, "ic-"+server+"-"+stamp+ext)
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err == nil {
			select {
			case settings.ioRes <- "IC log exported: " + path:
			default:
			}
		}
	}()
	a.pushDebug("exporting IC log (" + stamp + ")")
}

// icHTMLColor maps an AO text color index onto a CSS color matching the
// in-client palette (render.TextColor).
func icHTMLColor(index int) string {
	col := render.TextColor(index)
	return fmt.Sprintf("#%02x%02x%02x", col.R, col.G, col.B)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
