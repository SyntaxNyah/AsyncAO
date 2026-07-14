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
	"unicode"
	"unicode/utf8"

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
	hotkeyFavEmotes     = "fav_emote_box"  // toggle the floating favourite-emotes box
	hotkeyClipReplay    = "clip_replay"    // save the last window of conversation (instant replay)
	// Viewer-FX toggles (#121+ roadmap). The Ctrl+letter space is full, so these default to the
	// free symbol keys (and are rebindable in Settings → Controls like every hotkey).
	hotkeySpotlight  = "spotlight"   // toggle speaker spotlight (#121)
	hotkeyIdleBreath = "idle_breath" // toggle idle breathing (#122)
	hotkeyReflection = "reflection"  // toggle glass-floor reflection (#123)
	hotkeyWeather    = "weather"     // cycle ambient weather (#124)
	hotkeyCharBundle = "char_bundle" // toggle full-character sprite prefetch (#127)
	hotkeyPingChip   = "ping_chip"   // toggle the connection-quality chip (#128)
	hotkeyModDash    = "mod_dash"    // open the CM / mod dashboard (#130)
	hotkeyPalette    = "palette"     // command palette (#39) — fuzzy search every action + server command
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
	{hotkeyVolDown, "Master volume down", "-"},                             // Ctrl+-  (quieter)
	{hotkeyVolUp, "Master volume up", "="},                                 // Ctrl+=  (louder)
	{hotkeyShownameRand, "Random showname preset", "h"},                    // Ctrl+H (rebindable)
	{hotkeyShownameCycle, "Cycle showname preset", "b"},                    // Ctrl+B (rebindable)
	{hotkeyDND, "Do Not Disturb (mute pings)", "d"},                        // Ctrl+D — session-only, rebindable
	{hotkeyReshowSprites, "Reshow hidden sprites", "y"},                    // un-hide all right-click-hidden sprites
	{hotkeyHideDesk, "Hide / show the desk", "v"},                          // toggle desk rendering
	{hotkeyQuickConnect, "Connect to last server", "q"},                    // dial the saved server (lobby)
	{hotkeyFavEmotes, "Favourite-emotes box", "a"},                         // toggle the floating box of starred emotes
	{hotkeyClipReplay, "Clip the last conversation (Instant Replay)", "."}, // Ctrl+. — every letter is taken; pairs with settings (Ctrl+,)
	{hotkeySpotlight, "Toggle speaker spotlight", "["},                     // Ctrl+[ — letters are exhausted, FX toggles use the free symbol keys
	{hotkeyIdleBreath, "Toggle idle breathing", "]"},                       // Ctrl+]
	{hotkeyReflection, "Toggle glass-floor reflection", ";"},               // Ctrl+;
	{hotkeyWeather, "Cycle ambient weather", "'"},                          // Ctrl+'
	{hotkeyCharBundle, "Toggle full-character sprite preload", "\\"},       // Ctrl+\
	{hotkeyPingChip, "Toggle connection ping chip", "`"},                   // Ctrl+`
	{hotkeyModDash, "Open the CM / mod dashboard", "/"},                    // Ctrl+/ — mnemonic for a slash-command panel
	{hotkeyPalette, "Command palette (search every action)", "space"},      // Ctrl+Space — the one shortcut that finds the rest
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

// toggleFXPref flips a bool viewer-FX pref via its setter and flashes a brief on/off toast —
// the shared body of the FX toggle hotkeys (#121+), so every effect gets a hands-free keybind.
func (a *App) toggleFXPref(cur bool, set func(bool), label string) {
	set(!cur)
	a.warnLine = label + " off"
	if !cur {
		a.warnLine = label + " on"
	}
	a.warnAt = time.Now()
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
// keypress binds key → character on this server. Esc OR any mouse click cancels
// it — an accidental arm used to trap you into pressing a key (you only had a
// tiny per-cell "press..." cue and no obvious way out), so clicking anywhere now
// dismisses it, the natural "oops, didn't mean to" gesture. The arming click
// itself can't self-cancel: pollCharBind runs in the update phase BEFORE the
// wardrobe arms bindingFor in the render phase, and c.clicked is a one-frame edge.
func (a *App) pollCharBind() {
	if a.bindingFor == "" {
		return
	}
	c := a.ctx
	if c.escPressed || c.clicked {
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

// editorUndoChord routes Ctrl+Z / Ctrl+Y to the ARMED layout editor (classic
// or themed) ahead of every other chord consumer. The editors' own in-draw
// checks read c.keyPressed — which a Ctrl chord never reaches: HandleEvent
// puts non-clipboard chords in c.hotkey and returns (#96 configurable
// hotkeys), so editor undo was silently dead AND Ctrl+Y fell through to its
// default bind ("Reshow hidden sprites") mid-edit. Consumes the chord whether
// or not history exists, so a bound action can never fire while editing.
func (a *App) editorUndoChord() bool {
	c := a.ctx
	if !a.classicEdit && !a.layoutEdit {
		return false
	}
	if c.hotkey != sdl.K_z && c.hotkey != sdl.K_y {
		return false
	}
	switch {
	case a.classicEdit && c.hotkey == sdl.K_z:
		a.classicEditUndo()
	case a.classicEdit:
		a.classicEditRedo()
	case c.hotkey == sdl.K_z:
		a.layoutEditUndo()
	default:
		a.layoutEditRedo()
	}
	c.hotkey = 0
	return true
}

// (The old inputUndoChord one-slot IC/OOC swap is gone: text fields carry a
// real undo history now — fieldhistory.go — fed by the same clears through
// the out-of-band detector, and the Ctrl+Z/Y chords are consumed pre-screen
// in App.Frame while any field is focused.)

// handleHotkeys consumes this frame's Ctrl chord on the courtroom screen
// (and dispatches macro keybinds, then character keybinds — macros win
// a key conflict since they were bound deliberately).
func (a *App) handleHotkeys() {
	if a.editorUndoChord() {
		return // Ctrl+Z / Ctrl+Y belong to the armed layout editor
	}
	if !a.handleMacroKeys() && !a.handleJukeboxKeys() && !a.handleShownameKeys() && !a.handleICPhraseKeys() && !a.handleStylePresetKeys() {
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
	case a.hotkeyFor(hotkeyClipReplay):
		a.clipInstantReplay()
	case a.hotkeyFor(hotkeyPalette):
		a.togglePalette()
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
	case a.hotkeyFor(hotkeySpotlight):
		a.toggleFXPref(a.d.Prefs.SpotlightOn(), a.d.Prefs.SetSpotlight, "Speaker spotlight")
	case a.hotkeyFor(hotkeyIdleBreath):
		a.toggleFXPref(a.d.Prefs.IdleBreathOn(), a.d.Prefs.SetIdleBreath, "Idle breathing")
	case a.hotkeyFor(hotkeyReflection):
		a.toggleFXPref(a.d.Prefs.ReflectionOn(), a.d.Prefs.SetReflection, "Glass-floor reflection")
	case a.hotkeyFor(hotkeyWeather):
		a.cycleWeather()
	case a.hotkeyFor(hotkeyCharBundle):
		a.toggleFXPref(a.d.Prefs.CharBundlePrefetchOn(), a.d.Prefs.SetCharBundlePrefetch, "Full-character preload")
	case a.hotkeyFor(hotkeyPingChip):
		a.toggleFXPref(a.d.Prefs.PingChipOn(), a.d.Prefs.SetPingChip, "Ping chip")
	case a.hotkeyFor(hotkeyModDash):
		a.toggleModDash()
	case a.hotkeyFor(hotkeyFavEmotes):
		a.toggleFavEmoteBox()
	}
}

// toggleFavEmoteBox flips the floating favourite-emotes box (its rebindable key
// + the Settings tick). A short toast confirms, and points at how to fill it.
func (a *App) toggleFavEmoteBox() {
	on := !a.d.Prefs.FavEmoteBoxOn()
	a.d.Prefs.SetFavEmoteBox(on)
	if on {
		a.warnLine = "Favourite-emotes box shown — ★ emotes in the grid to fill it"
	} else {
		a.warnLine = "Favourite-emotes box hidden"
	}
	a.warnAt = time.Now()
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
	master := a.d.Prefs.MasterVolume()
	// Per-server audio (sandboxed tab sound): the ACTIVE server's override, when
	// enabled, replaces the global mixer levels — so each tab carries its own volumes.
	// ServerAudio falls back to the global per channel for any level this server never
	// set, so a profile can't silently mute a channel (e.g. SFX) just by existing — only
	// an explicit 0 mutes. Re-applied on courtroom entry + every tab switch (buildRoom).
	if on, m, mu, s, b := a.d.Prefs.ServerAudio(a.serverKey); on {
		master, music, sfx, blip = m, mu, s, b
	}
	alert := a.d.Prefs.AlertVolume() // callword/friend ping — independent of SFX, always global
	music, sfx, blip, alert = mixChannels(master, music, sfx, blip, alert,
		a.masterMuted, a.musicMuted, a.sfxMuted, a.blipMuted, a.musicDucked)
	a.d.Audio.SetVolumes(music, sfx, blip)
	a.d.Audio.SetAlertVolume(alert)
}

// mixChannels applies the per-channel mutes (#10), the music duck, and the master scale
// to the effective levels — pure (no audio engine), so the mute/duck/scale math is
// unit-pinnable. Master mute zeroes EVERYTHING (it scales all channels + the alert); a
// per-channel mute zeroes just that channel without touching its stored slider level.
func mixChannels(master, music, sfx, blip, alert int, masterMute, musicMute, sfxMute, blipMute, ducked bool) (mMusic, mSFX, mBlip, mAlert int) {
	if masterMute {
		master = 0
	}
	if musicMute {
		music = 0
	}
	if sfxMute {
		sfx = 0
	}
	if blipMute {
		blip = 0
	}
	if ducked {
		music = music * duckMusicPercent / 100
	}
	// Master multiplier scales everything (composes over mute/duck) — the one
	// "too loud / too quiet" knob.
	if master != 100 {
		music, sfx, blip = music*master/100, sfx*master/100, blip*master/100
		alert = alert * master / 100
	}
	return music, sfx, blip, alert
}

// effectiveVolumes reads the volumes that apply right now: the active server's own
// per-server profile if it has one, else the global defaults.
func (a *App) effectiveVolumes() (master, music, sfx, blip int) {
	master = a.d.Prefs.MasterVolume()
	music, sfx, blip = a.d.Prefs.AudioVolumes()
	if a.serverKey != "" {
		if on, m, mu, s, b := a.d.Prefs.ServerAudio(a.serverKey); on {
			master, music, sfx, blip = m, mu, s, b // each falls back to global if this server never set it
		}
	}
	return
}

// setEffectiveVolumes writes a volume change to the active server's OWN profile
// when connected — so muting blips on one server leaves another untouched (each
// tab keeps its own) — else to the global defaults. Applies it live.
func (a *App) setEffectiveVolumes(master, music, sfx, blip int) {
	if a.serverKey != "" {
		a.d.Prefs.SetServerAudioVolumes(a.serverKey, master, music, sfx, blip)
	} else {
		a.d.Prefs.SetMasterVolume(master)
		a.d.Prefs.SetAudioVolumes(music, sfx, blip)
	}
	a.applyAudioVolumes()
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
const (
	hkRowH      = int32(20)
	hkKeyGap    = int32(116) // label x-offset (the key column width)
	hkSheetDefW = int32(470) // default width — a single scrollable column
	hkSheetMinW = int32(300)
	hkSheetMinH = int32(160)
)

// hkSheetRect is the hotkey cheat sheet's floating-window rect (floatwin.go):
// movable + resizable, default-sized to fit its two columns of shortcuts.
func (a *App) hkSheetRect(w, h int32) sdl.Rect {
	if a.hkCache == nil {
		a.hkCache = a.hotkeyCheatEntries()
	}
	// Default tall enough to show a good chunk; the list scrolls, so the exact fit
	// doesn't matter and resizing smaller never drops rows.
	defH := int32(len(a.hkCache))*hkRowH + floatTitleH + 14
	if defH > 540 {
		defH = 540
	}
	if r, ok := a.seedPanelFromSlot(&a.hkWin, slotPanelHK, hkSheetDefW, defH, hkSheetMinW, hkSheetMinH, w, h); ok {
		return r
	}
	return a.hkWin.rect(hkSheetDefW, defH, hkSheetMinW, hkSheetMinH, w, h)
}

// hkSheetFencesPointer reports whether the SCREEN pass should run pointer-blind
// because the floating hotkey sheet owns the cursor: open and hovered, or its
// drag/resize is in flight (a fast drag must not leak presses underneath —
// the boxFencesPointer rule). The courtroom pass already fences the sheet via
// boxFencesPointer; this extends the same discipline to every OTHER screen
// (lobby / settings / char-select / menus), where the sheet previously let
// the wheel, hover and drags fall straight through to the list below it.
func (a *App) hkSheetFencesPointer(w, h int32) bool {
	if !a.showHotkeys {
		return false
	}
	return a.hkWin.dragging || a.hkWin.resizing ||
		pointIn(a.ctx.mouseX, a.ctx.mouseY, a.hkSheetRect(w, h))
}

// drawHotkeyCheatSheet is now a movable/resizable, non-blocking floating box —
// drag the title to park it out of the way and keep it open while you play.
func (a *App) drawHotkeyCheatSheet(w, h int32) {
	c := a.ctx
	if a.hkCache == nil { // opened without openHotkeyCheatSheet (defensive)
		a.hkCache = a.hotkeyCheatEntries()
	}
	entries := a.hkCache
	pressed := c.mouseDown && !a.hkPrevDown // own press edge (drawn over every screen)
	a.hkPrevDown = c.mouseDown
	r := a.hkSheetRect(w, h)
	c.Fill(r, sdl.Color{R: 12, G: 12, B: 18, A: 245})
	c.Border(r, ColAccent)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi) // title bar / drag handle
	c.Heading(r.X+pad, r.Y+6, "Hotkeys — your shortcuts", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 28, Y: r.Y + 5, W: 20, H: 20}, "x") {
		a.showHotkeys = false
		a.hkCache = nil
		return
	}
	c.Label(r.X+r.W-150, r.Y+10, "F1 to close", ColTextDim)
	wasManip := a.hkWin.dragging || a.hkWin.resizing
	a.floatWinDrag(&a.hkWin, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 160, H: floatTitleH}, &pressed)
	hgrip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.hkWin, hgrip, r, hkSheetMinW, hkSheetMinH, &pressed)
	a.drawResizeGrip(hgrip)
	if wasManip && !a.hkWin.dragging && !a.hkWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelHK, r, w, h)
	}
	if !c.mouseDown && wasManip {
		c.clicked = false // a finished drag/resize isn't a click underneath
	}

	// Scrollable single-column list: resizing smaller never drops rows now, and each
	// label gets the full window width instead of being cut off at a fixed column.
	body := sdl.Rect{X: r.X, Y: r.Y + floatTitleH + 2, W: r.W, H: r.Y + r.H - 4 - (r.Y + floatTitleH + 2)}
	if body.H < hkRowH {
		return
	}
	contentH := int32(len(entries)) * hkRowH
	if !c.ctrlHeld {
		a.hkScroll -= c.WheelIn(body) * scrollStepPx
	}
	track := sdl.Rect{X: r.X + r.W - scrollBarW - 2, Y: body.Y, W: scrollBarW, H: body.H}
	a.hkScroll = c.VScrollbar("hkscroll", track, a.hkScroll, contentH, body.H)
	clipPrev, clipHad := c.pushClip(body)
	defer c.popClip(clipPrev, clipHad)
	x := r.X + pad
	labelW := (r.X + r.W - scrollBarW - 8) - (x + hkKeyGap) // label fills to the scrollbar
	y := body.Y - a.hkScroll
	for _, e := range entries {
		if y+hkRowH > body.Y && y < body.Y+body.H { // only draw rows in view
			if e.header {
				c.Label(x, y, e.label, ColStar)
			} else {
				keyCol := ColAccent
				if e.custom {
					keyCol = ColStar // a binding you remapped or created
				}
				c.Label(x, y, e.key, keyCol)
				c.LabelClipped(x+hkKeyGap, y, labelW, e.label, ColText)
			}
		}
		y += hkRowH
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
	a.drawChatOverlay(vp, false, 0, 0) // theater mode keeps the clean stage-bottom chatbox
	a.drawCourtOverlays(vp, nil)       // splashes/HP still play — part of the show
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
	a.applySide(next) // also /pos the server so the cycle moves you instantly
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
func (a *App) checkCallwords(text string, selfNames []string, mine bool) {
	if a.d.Prefs.StreamerMode() || a.dndOn {
		return
	}
	// Brief grace after a login flow: the server's login replies (and the Akashi
	// prompt) routinely echo your handle/name, which would self-ping the instant
	// you join. The window is short so a real callword seconds later still alerts.
	if a.inLoginGrace() || text == "" {
		return
	}
	lower := strings.ToLower(text)
	// Configured highlight words match as WHOLE WORDS by default (reusing the
	// #203 containsWord matcher), so "tif" fires on "hi tif" but not on "motif"
	// / "artifact" — the old raw-substring match self-pinged on any three-letter
	// coincidence. A trailing '*' is an opt-in loose escape hatch: "obj*" matches
	// at a WORD START without a word-end boundary, so it still catches
	// "objection" / "objecting" (the deliberately-loose shorthand the raw match
	// used to give for free) while "tif*" still won't hit "motif" — "tif" isn't a
	// word start there. See callwordHit / containsWordPrefix below.
	for _, w := range a.d.Prefs.CallWords() {
		if callwordHit(lower, w) {
			a.callwordAlert("AsyncAO — callword", "Heard your callword: "+w)
			return
		}
	}
	// #203 name mentions: your own showname / character name, matched as a WHOLE
	// word (so "Max" doesn't fire on "maximum") and NEVER on your own message —
	// the server echoes your IC back and RP lines routinely contain your name.
	// The names are session-scoped: the caller passes THIS message's session's
	// identity, so multi-server play pings the right name per tab.
	if mine || !a.d.Prefs.MentionSelfOn() {
		return
	}
	for _, n := range selfNames {
		n = strings.TrimSpace(n)
		if utf8.RuneCountInString(n) < mentionMinRunes {
			continue // too short to match as a whole word without chat-noise pings
		}
		if containsWord(lower, strings.ToLower(n)) {
			a.callwordAlert("AsyncAO — mention", "Mentioned by name: "+n)
			return
		}
	}
}

// callwordAlert fires the shared personal-alert side effects — a sound (custom
// if set, else the built-in ping; NOT the theme's word_call, which a theme may
// name without shipping a loadable file, silently killing the alert), an optional
// in-app toast, an optional rate-limited desktop toast while tabbed away, and a
// window flash. Streamer / DND / login-grace are gated by the caller before here.
func (a *App) callwordAlert(osTitle, body string) {
	if f := a.d.Prefs.CallwordSoundPath(); f != "" {
		a.d.Audio.PlayFile(f)
	} else {
		a.d.Audio.PlayAlert()
	}
	if a.d.Prefs.CallwordToastOn() {
		a.warnLine = clampLine(body)
		a.warnAt = time.Now()
	}
	if a.d.Prefs.CallwordOSToastOn() && !a.ctx.WindowFocused() && time.Since(a.lastOSToast) >= osToastMinInterval {
		a.lastOSToast = time.Now()
		showOSToast(osTitle, body)
	}
	a.ctx.FlashWindow()
}

// mentionMinRunes is the shortest self-name that triggers a #203 mention alert:
// a 1-rune name, even as a whole word, matches far too much ordinary chat.
const mentionMinRunes = 2

// containsWord reports whether needle occurs in haystack as a whole word — not
// flanked by a letter or digit on either side (so "max" matches "hi max" and
// "max!" but not "maximum"). Both args must already be lowercased. Alloc-free
// and Unicode-safe: it scans with strings.Index and checks the rune on each
// side, so accented names work and there are no byte-boundary false splits.
func containsWord(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for from := 0; from <= len(haystack)-len(needle); {
		i := strings.Index(haystack[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		if wordBoundary(haystack, i) && wordBoundary(haystack, i+len(needle)) {
			return true
		}
		from = i + 1 // overlap-safe: step one byte past this (rejected) hit
	}
	return false
}

// wordBoundary reports whether byte offset i in s is a word edge: the start/end
// of the string, or a spot where the adjacent rune isn't a letter/digit. Used on
// both ends of a candidate match by containsWord.
func wordBoundary(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	before, _ := utf8.DecodeLastRuneInString(s[:i])
	after, _ := utf8.DecodeRuneInString(s[i:])
	return !isWordRune(before) || !isWordRune(after)
}

// isWordRune classifies letters and digits as "inside a word" for the mention
// boundary test (Unicode-aware, so accented / non-Latin names work).
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// callwordHit reports whether a single configured callword fires in lower (which
// must already be lowercased). A bare word matches as a WHOLE word (containsWord)
// so it can't self-ping on a coincidental substring. A trailing '*' opts into a
// looser prefix match (containsWordPrefix): the stem must begin a word but need
// not end one, so "obj*" catches "objection". A lone "*" (empty stem) never
// matches — it would otherwise fire on every message, defeating the whole point.
// Extracted from checkCallwords so the pure matching logic is unit-testable
// without the SDL/audio side effects of callwordAlert.
func callwordHit(lower, word string) bool {
	if stem, ok := strings.CutSuffix(word, "*"); ok {
		return stem != "" && containsWordPrefix(lower, stem)
	}
	return containsWord(lower, word)
}

// containsWordPrefix reports whether needle occurs in haystack at a WORD START —
// bounded by a non-word rune (or the string start) on its left, with NO
// constraint on its right. So "obj" matches "objection" and "obj!" but not
// "an obj-ish thing" spelled "kobj" (interior) — the classic prefix/shorthand
// rule. Both args must already be lowercased. Like containsWord it's alloc-free
// and Unicode-safe (scans with strings.Index, checks the rune to the left) and
// keeps scanning past a rejected interior hit so a later true word-start match
// still lands (e.g. "tif" in "motif tiffany" rejects at "motif", accepts at
// "tiffany").
func containsWordPrefix(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for from := 0; from <= len(haystack)-len(needle); {
		i := strings.Index(haystack[from:], needle)
		if i < 0 {
			return false
		}
		i += from
		if wordBoundary(haystack, i) { // left edge only — right edge is free
			return true
		}
		from = i + 1 // overlap-safe: step one byte past this (rejected) hit
	}
	return false
}

// mentionNames returns the names that count as "you" in the ACTIVE session for
// #203 mention alerts: your effective showname (override or saved) and your
// selected character. checkCallwords drops the blank / too-short ones.
func (a *App) mentionNames() []string {
	return []string{a.effectiveShowname(), a.myCharName()}
}

// mentionNamesFor returns the mention names for a BACKGROUND session tab: its
// selected character (session-scoped, so multi-server play pings the right name)
// and your saved showname — a parked tab can't carry the live showname override.
func (a *App) mentionNamesFor(s *sessionState) []string {
	return []string{sessionCharName(s), a.d.Prefs.SavedShowname()}
}

// sessionCharName is the character a session is playing (its wire identity), or
// "" if none is selected yet.
func sessionCharName(s *sessionState) string {
	if s.sess == nil || s.sess.MyCharID < 0 || s.sess.MyCharID >= len(s.sess.Chars) {
		return ""
	}
	return s.sess.Chars[s.sess.MyCharID].Name
}

// isSelfName reports whether name is (case-insensitively) one of selfNames — the
// #203 self-ping guard, so your own echoed IC/OOC never alerts you by name.
func isSelfName(name string, selfNames []string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, n := range selfNames {
		if strings.EqualFold(name, strings.TrimSpace(n)) {
			return true
		}
	}
	return false
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
		a.oocWrapW == width && a.oocWrapPct == a.oocPct && a.oocWrapMask == streamer {
		return a.oocWrap
	}
	out := a.oocWrap[:0]
	name := a.oocWrapName[:0] // parallel to out: speaker on each entry's first display line
	urls := a.oocWrapURL[:0]  // parallel to out: the entry's link on each of its display lines
	cont := a.oocWrapCont[:0] // parallel to out: wrap continuation rows (hanging indent)
	src := a.oocWrapSrc[:0]   // parallel to out: source oocLog entry index (link-hover boundary)
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
			// Per-paragraph font pick: wraps measure with the font that will DRAW the
			// line — the OOC log draws at oocPct (its own scale, independent of the IC
			// log's logPct), so the wrap must measure at oocPct too or a scaled-up OOC
			// message overflows instead of wrapping (#1). (Also the CJK chain rule.)
			font := a.ctx.LogFontFor(a.oocPct, para)
			trimmed := strings.TrimRight(para, "\r")
			// Emoji-aware wrap for emoji lines (the plain font sizes colour emoji as
			// narrow tofu, so an emoji-laden line overflowed instead of wrapping); plain
			// lines keep the cheap word-wrap.
			var lines []string
			if ef := a.ctx.EmojiFont(a.oocPct); ef != nil && render.NeedsEmojiFallback(trimmed) {
				lines = render.WrapEmojiAware(font, ef, trimmed, width, oocWrapMaxLinesPerEntry)
			} else {
				lines = wrapToWidth(font, trimmed, width, oocWrapMaxLinesPerEntry)
			}
			if len(lines) == 0 {
				out = append(out, "") // blank MOTD spacer lines survive
				name = append(name, "")
				urls = append(urls, "")
				cont = append(cont, false)
				src = append(src, i)
				continue
			}
			for row, ln := range lines {
				out = append(out, ln)
				urls = append(urls, paraURL) // every wrapped row of THIS line opens its link
				cont = append(cont, row > 0) // rows the WRAP made are continuations (hanging indent); the paragraph's own newline isn't
				src = append(src, i)         // all rows of this entry carry its oocLog index
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
	a.oocWrap, a.oocWrapName, a.oocWrapURL, a.oocWrapCont, a.oocWrapSrc = out, name, urls, cont, src
	a.oocWrapSeq, a.oocWrapW, a.oocWrapPct, a.oocWrapMask = a.oocSeq, width, a.oocPct, streamer
	a.oocWrapGen = a.ctx.fontChainGen
	return out
}

// icWrapLine is one display row of the wrapped IC log: a text slice plus
// its source entry (the row inherits that entry's AO color).
type icWrapLine struct {
	text  string
	entry int // index into icLog
}

// logWrapIndentPx is the hanging indent for a message's wrapped continuation
// rows (IC and OOC): rows two and up of one message start slightly right of
// its first, so a wrapped message reads as ONE message instead of a run of
// new paragraphs (playtest). The drawers wrap text against
// width−logWrapIndentPx, so an indented row can never overflow the column.
const logWrapIndentPx = 14

// logRowIndent is display row li's draw indent in log `which`: a wrap
// continuation row indents by logWrapIndentPx, a message's first row (and any
// row without wrap data) sits at the column start. The selection hit-math and
// highlight use the same offset, so clicks land exactly on what's drawn.
func (a *App) logRowIndent(which, li int) int32 {
	if which == logSelIC {
		// IC entries are single paragraphs: a row is a continuation exactly
		// when it shares its entry with the row above (the filtered wrap keeps
		// an entry's rows adjacent).
		if li > 0 && li < len(a.icWrap) && a.icWrap[li].entry == a.icWrap[li-1].entry {
			return logWrapIndentPx
		}
		return 0
	}
	if li >= 0 && li < len(a.oocWrapCont) && a.oocWrapCont[li] {
		return logWrapIndentPx
	}
	return 0
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
		// Wrap with the font that will draw the entry (CJK chain rule). An emoji entry
		// wraps with the emoji-AWARE measure: the plain font sizes colour emoji as narrow
		// tofu, so a long emoji showname overflowed + clipped instead of wrapping. Plain
		// entries keep the cheap word-wrap.
		font := a.ctx.LogFontFor(a.logPct, text)
		var wrapped []string
		if ef := a.ctx.EmojiFont(a.logPct); ef != nil && render.NeedsEmojiFallback(text) {
			wrapped = render.WrapEmojiAware(font, ef, text, width, icWrapMaxLinesPerEntry)
		} else {
			wrapped = wrapToWidth(font, text, width, icWrapMaxLinesPerEntry)
		}
		for _, ln := range wrapped {
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
