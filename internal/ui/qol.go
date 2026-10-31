package ui

// Quality-of-life: hotkeys, callword alerts, IC log search and export, screenshots, music stop.
// Render thread except the explicitly off-thread file writes.

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

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// Hotkey actions, with persisted overrides in Prefs.Hotkeys; all fire on Ctrl+<key>.
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
	// Jump straight to an Extras menu, which a legacy AO2 theme has no button for.
	hotkeyCharMenu      = "char_menu"
	hotkeyWardrobe      = "wardrobe"
	hotkeyJukebox       = "jukebox"
	hotkeyBackground    = "background"
	hotkeyEvidence      = "evidence"
	hotkeyPairMenu      = "pair_menu"
	hotkeyModcall       = "modcall"
	hotkeyUIChrome      = "ui_chrome"
	hotkeyEditLayout    = "edit_layout"
	hotkeySettings      = "settings_menu"
	hotkeyRandomChar    = "random_char"
	hotkeyVolDown       = "vol_down" // master volume −/+ a step (quick volume from the keyboard)
	hotkeyVolUp         = "vol_up"
	hotkeyShownameRand  = "showname_rand"  // swap to a random saved showname preset (M6)
	hotkeyShownameCycle = "showname_cycle" // cycle to the next saved showname preset
	hotkeyDND           = "dnd"            // toggle Do Not Disturb (mute callword + friend pings) (M15)
	hotkeyReshowSprites = "reshow_sprites" // un-hide all sprites hidden this session (right-click-to-hide)
	hotkeyQuickConnect  = "quick_connect"  // dial the saved last server (works offline / in the lobby)
	hotkeyFavEmotes     = "fav_emote_box"  // toggle the floating favourite-emotes box
	hotkeyClipReplay    = "clip_replay"    // save the last window of conversation (instant replay)
	// Viewer-FX toggles; the Ctrl+letter space is full, so these default to free symbol keys.
	hotkeySpotlight  = "spotlight"   // toggle speaker spotlight (#121)
	hotkeyIdleBreath = "idle_breath" // toggle idle breathing (#122)
	hotkeyReflection = "reflection"  // toggle glass-floor reflection (#123)
	hotkeyWeather    = "weather"     // cycle ambient weather (#124)
	hotkeyCharBundle = "char_bundle" // toggle full-character sprite prefetch (#127)
	hotkeyPingChip   = "ping_chip"   // toggle the connection-quality chip (#128)
	hotkeyModDash    = "mod_dash"    // open the CM / mod dashboard (#130)
	hotkeyPalette    = "palette"     // command palette (#39) — fuzzy search every action + server command
	hotkeySearchLog  = "search_log"  // focus the log find field (Ctrl+F, the standard find chord)
	// Hide or show the two chrome bars by key, as the UI-chrome panel that controls them can itself be covered.
	hotkeyToggleMenuBar = "toggle_menu_bar"
	hotkeyToggleToolbox = "toggle_toolbox"
)

// volumeKeyStep is how much the master-volume hotkeys nudge per press (percent).
const volumeKeyStep = 5

// hotkeyDefs drives both dispatch and the Settings rows: id, label and default key.
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
	{hotkeyMuteSFX, "Mute sound effects", "{"},
	{hotkeyQuickSwap, "Quick-swap character (cycle wardrobe)", "j"},
	{hotkeyEmoteCycle, "Cycle emote (next)", "e"},
	{hotkeyPinNote, "Pin hovered log line to notes", "n"},
	{hotkeyFriendHi, "Toggle friend highlights", "u"},
	{hotkeyExtras, "Open the Extras menu (AsyncAO features box)", "x"},
	// Direct menu shortcuts on Ctrl+5..0.
	{hotkeyCharMenu, "Menu: Characters", "5"},
	{hotkeyWardrobe, "Menu: Wardrobe", "6"},
	{hotkeyJukebox, "Menu: Jukebox", "7"},
	{hotkeyBackground, "Menu: Background", "8"},
	{hotkeyEvidence, "Menu: Evidence", "9"},
	{hotkeyPairMenu, "Menu: Pairing", "0"},
	{hotkeyModcall, "Menu: Call mod", "o"},
	{hotkeyUIChrome, "Menu: UI chrome", "f4"},
	// Ctrl+F2: every Ctrl+letter, digit and symbol is taken by a row above or by the clipboard and editor undo chords.
	// Plain F2 is a style-preset bind that requires !ctrlHeld, so it does not clash.
	{hotkeyEditLayout, "Edit layout (open the live editor)", "f2"},
	{hotkeySettings, "Menu: Settings", ","}, // Ctrl+, (prefs convention); NOT z — that's the layout-editor undo
	{hotkeyRandomChar, "Random character", "r"},
	{hotkeyVolDown, "Master volume down", "-"},                             // Ctrl+-  (quieter)
	{hotkeyVolUp, "Master volume up", "="},                                 // Ctrl+=  (louder)
	{hotkeyShownameRand, "Random showname preset", "h"},                    // Ctrl+H (rebindable)
	{hotkeyShownameCycle, "Cycle showname preset", "b"},                    // Ctrl+B (rebindable)
	{hotkeyDND, "Do Not Disturb (mute pings)", "d"},                        // Ctrl+D — session-only, rebindable
	{hotkeyReshowSprites, "Reshow hidden sprites", "y"},                    // un-hide all right-click-hidden sprites
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
	{hotkeySearchLog, "Focus the log search (find)", "f"},                  // Ctrl+F: the standard find chord
	// Function keys, for the same reason as Edit Layout. The bar toggles use f5 and f6 rather than f3 and f4,
	// because the cheat sheet lists plain F1, F3 and F8 as fixed, and Ctrl+F3 beside F3 reads as a mistake.
	{hotkeyToggleMenuBar, "Show / hide the menu bar", "f5"},
	{hotkeyToggleToolbox, "Show / hide the toolbox", "f6"},
}

// hotkeyFor resolves an action's key name (pref override or default).
func (a *App) hotkeyFor(action string) string {
	if key := a.d.Prefs.Load().Hotkey(action); key != "" {
		return strings.ToLower(key)
	}
	for _, def := range hotkeyDefs {
		if def.id == action {
			return def.def
		}
	}
	return ""
}

// toggleFXPref flips a bool viewer-FX pref via its setter and shows a brief on/off toast.
func (a *App) toggleFXPref(cur bool, set func(bool), label string) {
	set(!cur)
	a.warnLine = label + " off"
	if !cur {
		a.warnLine = label + " on"
	}
	a.warnAt = time.Now()
}

// hotkeyConflictKeys returns keys bound to several actions; handleHotkeys matches only the first,
// so the Controls tab flags them.
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

// refreshCharKeys re-reads this server's character keybinds into the lookup caches; only on connect and bind edits.
func (a *App) refreshCharKeys() {
	a.charKeys = a.d.Prefs.Load().CharKeyBinds(a.serverKey)
	if len(a.charKeys) == 0 {
		a.charKeysRev = nil
		return
	}
	a.charKeysRev = make(map[string]string, len(a.charKeys))
	for k, ch := range a.charKeys {
		a.charKeysRev[ch] = k
	}
}

// charKeyFor reports the key bound to a character on this server; "" is none.
func (a *App) charKeyFor(char string) string { return a.charKeysRev[char] }

// pollCharBind completes an armed wardrobe key-capture: the next plain keypress binds; Esc or any click cancels.
// The arming click cannot self-cancel: this runs in the update phase, before the wardrobe arms bindingFor in render.
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

// bareBindKey returns the keycode the user-assignable key namespaces may act on this frame (Alt+<key>), or 0.
// Typing claims the IC input on the first character (chatfocus.go), so these cannot fire on a bare letter.
// Alt because Ctrl is fully claimed (hotkeyDefs) and Shift is text.
// !ctrlHeld is required: AltGr on Windows reports as Ctrl+Alt and types real text, which altHeld alone would swallow.
// The bind-capture paths keep reading the plain key: you press "m" to bind m, and it fires as Alt+M.
func (a *App) bareBindKey() sdl.Keycode {
	c := a.ctx
	if c.keyPressed == 0 || !c.altHeld || c.ctrlHeld || a.capturingKey() {
		return 0
	}
	return c.keyPressed
}

// handleCharKeys wears a bound character on Alt+<bound key>; a capture in flight still owns the key.
func (a *App) handleCharKeys() {
	key := a.bareBindKey()
	if key == 0 {
		return
	}
	if name := a.charKeys[strings.ToLower(sdl.GetKeyName(key))]; name != "" {
		a.wearFromMenu(name)
	}
}

// handleEmoteKeys picks the emote in number-key position N on Alt+1..9, unless the digit is a character keybind.
func (a *App) handleEmoteKeys() {
	if a.d.Prefs.Load().DisableAltEmoteRow {
		return // the user removed the Alt+1..9 number row; the digits are plain text again
	}
	key := a.bareBindKey()
	if key < sdl.K_1 || key > sdl.K_9 || a.emotePerPage <= 0 {
		return
	}
	if a.charKeys[strings.ToLower(sdl.GetKeyName(key))] != "" {
		return // a character keybind owns this digit
	}
	// Map through the visible list (favs-only filters it), so the keys pick what that grid cell shows.
	a.refreshEmoteView()
	slot := a.emoteFirstSlot() + int(key-sdl.K_1)
	if slot >= 0 && slot < len(a.emoteVisible) {
		a.selectEmote(a.emoteVisible[slot])
	}
}

// editorUndoChord routes Ctrl+Z and Ctrl+Y to the armed layout editor ahead of other chords, since HandleEvent puts
// them in c.hotkey where the editors' own c.keyPressed checks cannot see them. It consumes the chord regardless.
// Two call sites: handleHotkeys for the overlay editors, which exist only during the courtroom pass, and App.Frame
// for the theme editor screen, which has no courtroom pass when opened from Settings with no server.
func (a *App) editorUndoChord() bool {
	c := a.ctx
	// The theme editor is checked first because it owns a whole screen; a stale overlay flag must not steal the chord.
	if a.themeEditorOpen() {
		if c.hotkey != sdl.K_z && c.hotkey != sdl.K_y {
			return false
		}
		// Ctrl+Shift+Z is redo too, as in app.go's focused-field path. HandleEvent folds every non-clipboard Ctrl
		// combination into one c.hotkey keysym with modifier flags beside it, so the shift test reads c.shiftHeld.
		if c.hotkey == sdl.K_z && !c.shiftHeld {
			a.editorUndo()
		} else {
			a.editorRedo()
		}
		c.hotkey = 0
		return true
	}
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

// handleHotkeys consumes this frame's Ctrl chord on the courtroom screen, then macro and character keybinds;
// macros win a key conflict.
func (a *App) handleHotkeys() {
	if a.serverNoticeDlg.open {
		// A BB notice owns the frame, so a chord must not fire blind beneath it; Esc is handled earlier.
		return
	}
	if a.disconnectDlg.open {
		// The courtroom is frozen under the disconnect dialog with its socket gone, and keyboard chords are not blocked
		// by the modal, so stop them here before they send into the dead connection. Esc is handled earlier.
		return
	}
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
	// Hotkeys are opt-in: no Ctrl chord below fires until "Enable hotkeys" is ticked.
	if !a.d.Prefs.Load().HotkeysEnabled {
		return
	}
	name := strings.ToLower(sdl.GetKeyName(key))
	switch name {
	// The shout chords arm the interjection, like the buttons (courtroom.cpp:6048-6127); pressing again disarms.
	case a.hotkeyFor(hotkeyHoldIt):
		a.toggleShout(protocol.ShoutHoldIt)
	case a.hotkeyFor(hotkeyObjection):
		a.toggleShout(protocol.ShoutObjection)
	case a.hotkeyFor(hotkeyTakeThat):
		a.toggleShout(protocol.ShoutTakeThat)
	case a.hotkeyFor(hotkeyCustom):
		a.toggleShout(protocol.ShoutCustom)
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
		on := !a.d.Prefs.Load().FriendHighlight
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FriendHighlight =

				on

		})

		a.warnLine = "Friend highlights off"
		if on {
			a.warnLine = "Friend highlights on"
		}
		a.warnAt = time.Now()
	case a.hotkeyFor(hotkeyExtras):
		a.toggleExtrasBox()
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
		a.toggleEvidence()
	case a.hotkeyFor(hotkeyPairMenu):
		a.showPair = true
	case a.hotkeyFor(hotkeyModcall):
		a.showModcall = true
	case a.hotkeyFor(hotkeyUIChrome):
		// Opens the per-piece panel even when the grip is hidden, so a user who hid the toolbox can recover it.
		// Theater mode exits first: re-arming these flags under theater would float the panel over the bare stage.
		if a.theaterOn {
			a.setTheater(false)
		}
		a.toolboxPinned, a.toolboxPieces = true, true
	case a.hotkeyFor(hotkeyToggleMenuBar):
		a.toggleChromePanel(panelMenuBar, "Menu bar")
	case a.hotkeyFor(hotkeyToggleToolbox):
		a.toggleChromePanel(panelToolbox, "Toolbox")
	case a.hotkeyFor(hotkeyEditLayout):
		// Keyboard way into the layout editor; openLayoutEditor picks classic or themed itself.
		a.openLayoutEditor()
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
	case a.hotkeyFor(hotkeySpotlight):
		a.toggleFXPref(a.d.Prefs.Load().Spotlight, func(v bool) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.Spotlight = v
			})
		}, "Speaker spotlight")
	case a.hotkeyFor(hotkeyIdleBreath):
		a.toggleFXPref(a.d.Prefs.Load().IdleBreath, func(v bool) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.IdleBreath = v
			})
		}, "Idle breathing")
	case a.hotkeyFor(hotkeyReflection):
		a.toggleFXPref(a.d.Prefs.Load().Reflection, func(v bool) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.Reflection = v
			})
		}, "Glass-floor reflection")
	case a.hotkeyFor(hotkeyWeather):
		a.cycleWeather()
	case a.hotkeyFor(hotkeyCharBundle):
		a.toggleFXPref(a.d.Prefs.Load().CharBundlePrefetch, func(v bool) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.CharBundlePrefetch = v
			})
		}, "Full-character preload")
	case a.hotkeyFor(hotkeyPingChip):
		a.toggleFXPref(a.d.Prefs.Load().PingChip, func(v bool) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.PingChip = v
			})
		}, "Ping chip")
	case a.hotkeyFor(hotkeyModDash):
		a.toggleModDash()
	case a.hotkeyFor(hotkeyFavEmotes):
		a.toggleFavEmoteBox()
	case a.hotkeyFor(hotkeySearchLog):
		a.focusLogSearch()
	}
}

// toggleFavEmoteBox flips the floating favourite-emotes box; a short toast points at how to fill it.
func (a *App) toggleFavEmoteBox() {
	on := !a.d.Prefs.Load().FavEmoteBox
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			FavEmoteBox =

			on

	})

	if on {
		a.warnLine = "Favourite-emotes box shown — ★ emotes in the grid to fill it"
	} else {
		a.warnLine = "Favourite-emotes box hidden"
	}
	a.warnAt = time.Now()
}

// quickSwapNext wears the next starred character on this server, from whatever is worn now, wrapping.
func (a *App) quickSwapNext() {
	ring, _, _ := mergeWardrobe(a.d.Prefs.Load().WardrobeList(a.serverKey), nil)
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

// toggleSFXMute flips the SFX mute and re-applies volumes; an overlay, not a volume write.
// Session-only, so an accidental press cannot leave SFX muted across a restart.
func (a *App) toggleSFXMute() {
	a.sfxMuted = !a.sfxMuted
	a.applyAudioVolumes()
	a.warnLine = "SFX unmuted"
	if a.sfxMuted {
		a.warnLine = "SFX muted (press the Mute SFX hotkey again to restore)"
	}
	a.warnAt = time.Now()
}

// mutedChannelsBootNotice names the persisted channel mutes just restored ("" for none);
// without it a user who quit muted gets silence, with the explaining volume strip hidden by default.
func mutedChannelsBootNotice(master, music, sfx, blip bool) string {
	var names []string
	if master {
		names = append(names, "Master")
	}
	if music {
		names = append(names, "Music")
	}
	if sfx {
		names = append(names, "SFX")
	}
	if blip {
		names = append(names, "Blips")
	}
	if len(names) == 0 {
		return ""
	}
	return "Audio is muted (" + strings.Join(names, ", ") + ") — click a label in the volume strip to unmute."
}

// duckMusicPercent is how loud music plays, as a % of its set volume, while a message is on stage with ducking on.
const duckMusicPercent = 35

// applyAudioVolumes pushes saved volumes to the audio system, with SFX mute, message ducking and cross-tab duck.
// Call it wherever those states change; a nil Audio is a no-op.
func (a *App) applyAudioVolumes() {
	if a.d.Audio == nil {
		return
	}
	music, sfx, blip := a.d.Prefs.Load().AudioVolumes()
	master := a.d.Prefs.Load().MasterVol
	// The active server's override, when enabled, replaces global levels; ServerAudio falls back per channel
	// to the global, so only an explicit 0 mutes. Re-applied on courtroom entry and every tab switch.
	if on, m, mu, s, b := a.d.Prefs.ServerAudio(a.serverKey); on {
		master, music, sfx, blip = m, mu, s, b
	}
	// The single music stream may belong to a backgrounded owner tab, so the music channel follows the owner's levels,
	// or an active tab with music at 0 would silence the owner's stream. See musicOwnerKey.
	musicMaster, musicVol := a.ownerMusicInputs(master, music)
	alert := a.d.Prefs.Load().AlertVol // callword/friend ping — independent of SFX, always global
	// Music rides the owner's levels and sfx, blip and alert the active tab's;
	// call mixChannels once per master domain and take only that domain's channel from each result.
	mMusic, _, _, _ := mixChannels(musicMaster, musicVol, sfx, blip, alert,
		a.masterMuted, a.musicMuted, a.sfxMuted, a.blipMuted, a.musicDucked, a.musicTabDucked)
	_, mSFX, mBlip, mAlert := mixChannels(master, music, sfx, blip, alert,
		a.masterMuted, a.musicMuted, a.sfxMuted, a.blipMuted, a.musicDucked, a.musicTabDucked)
	a.d.Audio.SetVolumes(mMusic, mSFX, mBlip)
	a.d.Audio.SetAlertVolume(mAlert)
}

// ownerMusicInputs resolves the (master, music) governing the music stream: the owner's when it is another tab with
// an audio profile, else the active tab's. It reads ServerAudio only for a distinct owner.
func (a *App) ownerMusicInputs(activeMaster, activeMusic int) (master, music int) {
	if a.musicOwnerKey == "" || a.musicOwnerKey == a.serverKey {
		return activeMaster, activeMusic
	}
	if on, m, mu, _, _ := a.d.Prefs.ServerAudio(a.musicOwnerKey); on {
		return m, mu
	}
	return activeMaster, activeMusic
}

// mixChannels applies per-channel mutes, music duck, cross-tab duck and master scale to the effective levels.
// Master mute zeroes everything; a channel mute leaves its stored level. tabDuck silences music after the other ducks,
// keeping the stream alive under a backgrounded tab so its position survives a switch.
func mixChannels(master, music, sfx, blip, alert int, masterMute, musicMute, sfxMute, blipMute, ducked, tabDuck bool) (mMusic, mSFX, mBlip, mAlert int) {
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
	if tabDuck {
		music = 0 // backgrounded tab's still-rolling stream: silent, but not stopped
	}
	// Master scales everything, over mute and duck.
	if master != 100 {
		music, sfx, blip = music*master/100, sfx*master/100, blip*master/100
		alert = alert * master / 100
	}
	return music, sfx, blip, alert
}

// settleAwaitedMusic lifts the cross-tab duck once the track the active tab requested is what the mixer is playing.
// Runs once per frame right after a.d.Audio.Frame() drains delivered audio, in both the Frame and Background loops.
func (a *App) settleAwaitedMusic() {
	// The usual case is nothing awaited: one field read, no Audio call.
	if a.musicAwaitURL == "" || a.d.Audio == nil {
		return
	}
	cur := a.d.Audio.CurrentMusicURL()
	if a.settleAwaitedDuck(cur) {
		return // the awaited track landed — normal delivery-time un-duck
	}
	// Only silent failures reach here (a 404, unsupported format, stall, or URL never equal to CurrentMusicURL),
	// which fire no event, so without a deadline the duck would hold forever. See settleAwaitTimeout for the release.
	switch a.settleAwaitTimeout(cur, a.now()) {
	case awaitTimeoutStop:
		a.d.Audio.StopMusic() // foreign stream loaded: silence it rather than reveal the wrong song
		a.clearMusicAwait()
		a.musicTabDucked = false
		a.applyAudioVolumes()
	case awaitTimeoutUnduck:
		a.clearMusicAwait()
		a.musicTabDucked = false
		a.applyAudioVolumes()
	}
}

// awaitTimeoutAction is settleAwaitTimeout's decision.
type awaitTimeoutAction int

const (
	// awaitTimeoutNone keeps waiting; a slow but real download may still arrive.
	awaitTimeoutNone awaitTimeoutAction = iota
	// awaitTimeoutUnduck: deadline reached, nothing foreign loaded, so lift the duck.
	awaitTimeoutUnduck
	// awaitTimeoutStop: deadline reached, a foreign stream is loaded, so stop rather than reveal the wrong song.
	awaitTimeoutStop
)

// settleAwaitTimeout decides how to release an await past its deadline, given the mixer's current URL.
// cur != "" here is a foreign stream, since settleAwaitedDuck already returned on a match: a wrong audible track
// is worse than silence, so stop it (StopMusic also purges the pending fetch, so it cannot land late).
func (a *App) settleAwaitTimeout(cur string, now time.Time) awaitTimeoutAction {
	if a.musicAwaitSince.IsZero() || now.Sub(a.musicAwaitSince) < musicAwaitTimeout {
		return awaitTimeoutNone
	}
	if cur == "" {
		return awaitTimeoutUnduck
	}
	return awaitTimeoutStop
}

// clearMusicAwait drops the await and its timeout stamp as a pair; callers clear the duck flag separately,
// because some also stop the stream at that point.
func (a *App) clearMusicAwait() {
	a.musicAwaitURL = ""
	a.musicAwaitSince = time.Time{}
}

// musicAwaitTimeout bounds how long the cross-tab duck waits for a track, for silent failures that fire no event;
// long enough not to clip a slow download.
const musicAwaitTimeout = 10 * time.Second

// settleAwaitedDuck clears the tab duck and await and re-applies volumes if the mixer now plays the awaited track.
func (a *App) settleAwaitedDuck(cur string) bool {
	if a.musicAwaitURL == "" || cur != a.musicAwaitURL {
		return false // nothing awaited, or the awaited track hasn't landed yet — keep the duck
	}
	a.musicAwaitURL = ""
	a.musicAwaitSince = time.Time{} // await satisfied: drop the timeout stamp with it
	a.musicTabDucked = false
	a.applyAudioVolumes()
	return true
}

// effectiveVolumes reads the active server's per-server profile if it has one, else the global defaults.
func (a *App) effectiveVolumes() (master, music, sfx, blip int) {
	master = a.d.Prefs.Load().MasterVol
	music, sfx, blip = a.d.Prefs.Load().AudioVolumes()
	if a.serverKey != "" {
		if on, m, mu, s, b := a.d.Prefs.ServerAudio(a.serverKey); on {
			master, music, sfx, blip = m, mu, s, b // each falls back to global if this server never set it
		}
	}
	return
}

// setEffectiveVolumes writes to the active server's own profile when connected, else the global defaults; applies live.
func (a *App) setEffectiveVolumes(master, music, sfx, blip int) {
	if a.serverKey != "" {
		a.d.Prefs.SetServerAudioVolumes(a.serverKey, master, music, sfx, blip)
	} else {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MasterVol =

				master

		})

		a.d.Prefs.SetAudioVolumes(music, sfx, blip)
	}
	a.applyAudioVolumes()
}

// nudgeMasterVolume steps the master volume from the keyboard, clamps it, applies it and toasts the level.
func (a *App) nudgeMasterVolume(delta int) {
	v := value.Clamp(a.d.Prefs.Load().MasterVol+delta, 0, 100)
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			MasterVol =

			v

	})

	a.applyAudioVolumes()
	a.warnLine = "Master volume: " + strconv.Itoa(v) + "%"
	a.warnAt = a.now()
}

const (
	hkRowH      = int32(20)
	hkKeyGap    = int32(116) // label x-offset (the key column width)
	hkSheetDefW = int32(470) // default width — a single scrollable column
	hkSheetMinW = int32(300)
	hkSheetMinH = int32(160)
)

// hkSheetRect is the hotkey cheat sheet's movable, resizable floating-window rect, default-sized for its shortcuts.
func (a *App) hkSheetRect(w, h int32) sdl.Rect {
	if a.hkCache == nil {
		a.hkCache = a.hotkeyCheatEntries()
	}
	// The list scrolls, so the exact fit does not matter and shrinking never drops rows.
	defH := int32(len(a.hkCache))*hkRowH + floatTitleH + 14
	if defH > 540 {
		defH = 540
	}
	if r, ok := a.seedPanelFromSlot(&a.hkWin, slotPanelHK, hkSheetDefW, defH, hkSheetMinW, hkSheetMinH, w, h); ok {
		return r
	}
	return a.hkWin.rect(hkSheetDefW, defH, hkSheetMinW, hkSheetMinH, w, h)
}

// hkSheetFencesPointer reports whether the screen pass should ignore the pointer because the sheet owns it: open and
// hovered, or mid drag or resize, so a fast drag cannot leak presses beneath. boxFencesPointer covers the courtroom.
func (a *App) hkSheetFencesPointer(w, h int32) bool {
	if !a.showHotkeys {
		return false
	}
	return a.hkWin.dragging || a.hkWin.resizing ||
		pointIn(a.ctx.mouseX, a.ctx.mouseY, a.hkSheetRect(w, h))
}

// drawHotkeyCheatSheet overlays every binding in a movable, resizable floating box; rows are cached per open.
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
	// The sheet does its own capture, or a rebind started here would wait until Settings opened.
	a.consumeHotkeyCapture()
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

	// Single-column and scrollable: shrinking never drops rows, and each label gets the full window width.
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
				// A row naming an action is rebindable here, through the same a.hkCapture as the Settings rows.
				row := sdl.Rect{X: x, Y: y, W: labelW + hkKeyGap, H: hkRowH}
				if e.action != "" {
					if c.hovering(row) {
						c.Fill(row, ColPanelHi)
						c.Tooltip(row, "Click to rebind · right-click to reset to the default")
					}
					if c.ClickedIn(row) {
						a.hkCapture = e.action // the next keypress binds this action
						a.bindingFor = ""      // don't also arm a character keybind
						c.focusID = ""         // the capture owns that keypress
					}
					if c.rightClicked && c.hovering(row) {
						a.d.Prefs.SetHotkey(e.action, "")
						if a.hkCapture == e.action {
							a.hkCapture = ""
						}
						a.hkCache = nil // the row's key changed; rebuild on the next frame
					}
				}
				shown := e.key
				if e.action != "" && a.hkCapture == e.action {
					shown = "press a key…"
					keyCol = ColStar
				}
				c.Label(x, y, shown, keyCol)
				c.LabelClipped(x+hkKeyGap, y, labelW, e.label, ColText)
			}
		}
		y += hkRowH
	}
}

const theaterHint = "Esc exits theater"

// drawTheater is the borderless viewport-only courtroom: the stage letterboxed to 4:3, chat overlay and hotkeys.
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

// stopMusic halts our playback, then asks the server to stop the area music: AO has no stop packet, so AO2-Client
// requests a fake track, "~stop.mp3" or the first extension-less list entry (courtroom.cpp music_stop).
// The local halt is what makes this reliable, as the server request often fails.
func (a *App) stopMusic() {
	a.d.Audio.StopMusic()
	a.musicTabDucked = false        // stream stopped: a later track must start audible (invariant: stopped ⇒ not ducked)
	a.musicAwaitURL = ""            // and no pending un-duck can survive the stop (invariant: stopped ⇒ no await)
	a.musicAwaitSince = time.Time{} // and no dangling timeout stamp
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

// loginCallwordGrace is how long after a login flow callword alerts are suppressed; short, so a real one still pings.
const loginCallwordGrace = 5 * time.Second

// inLoginGrace reports whether we're inside the post-login callword grace window.
func (a *App) inLoginGrace() bool {
	return !a.loginAt.IsZero() && a.now().Sub(a.loginAt) < loginCallwordGrace
}

// checkCallwords flashes and pings when a highlight word appears in IC or OOC traffic (AO2-Client's word_call sfx).
// Streamer mode suppresses both, to prevent an on-stream name leak.
func (a *App) checkCallwords(text string, selfNames []string, mine bool) {
	if a.d.Prefs.Load().StreamerModeOn || a.dndOn {
		return
	}
	// Login replies routinely echo your handle, which would self-ping the instant you join.
	if a.inLoginGrace() || text == "" {
		return
	}
	lower := strings.ToLower(text)
	// Words match whole ("tif" misses "motif"); a trailing '*' matches a word start ("obj*" hits "objection").
	for _, w := range a.d.Prefs.Load().CallWords() {
		if callwordHit(lower, w) {
			a.callwordAlert("AsyncAO — callword", "Heard your callword: "+w)
			return
		}
	}
	// Your own names match as whole words and never on your own message, which the server echoes.
	// The caller passes this message's session identity, so multi-server play pings the right name per tab.
	if mine || !a.d.Prefs.Load().MentionSelf {
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

// callwordAlert fires the personal alert: sound, toasts and window flash; the caller checks streamer, DND and grace.
// Not the theme's word_call: a theme may name it without shipping a loadable file, silently killing the alert.
func (a *App) callwordAlert(osTitle, body string) {
	if f := a.d.Prefs.Load().CallwordSoundFile; f != "" {
		a.d.Audio.PlayFile(f)
	} else {
		a.d.Audio.PlayAlert()
	}
	if a.d.Prefs.Load().CallwordToast {
		a.warnLine = clampLine(body)
		a.warnAt = time.Now()
	}
	if a.d.Prefs.Load().CallwordOSToast && !a.ctx.WindowFocused() && time.Since(a.lastOSToast) >= osToastMinInterval {
		a.lastOSToast = time.Now()
		a.d.Desktop.Notify(osTitle, body)
	}
	a.ctx.FlashWindow()
}

// mentionMinRunes is the shortest self-name that alerts: a 1-rune name matches too much ordinary chat.
const mentionMinRunes = 2

// containsWord reports whether needle occurs in haystack as a whole word; both must already be lowercased.
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

// wordBoundary reports whether byte offset i in s is a word edge: string start or end, or a non-letter, non-digit rune.
func wordBoundary(s string, i int) bool {
	if i <= 0 || i >= len(s) {
		return true
	}
	before, _ := utf8.DecodeLastRuneInString(s[:i])
	after, _ := utf8.DecodeRuneInString(s[i:])
	return !isWordRune(before) || !isWordRune(after)
}

// isWordRune classifies letters and digits as inside a word, Unicode-aware.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// callwordHit reports whether one callword fires in lower, which must be lowercased: whole word, or prefix with a
// trailing '*'. A lone "*" never matches, as it would fire on every message.
func callwordHit(lower, word string) bool {
	if stem, ok := strings.CutSuffix(word, "*"); ok {
		return stem != "" && containsWordPrefix(lower, stem)
	}
	return containsWord(lower, word)
}

// containsWordPrefix reports whether needle occurs at a word start, with no constraint on its right; lowercased inputs.
// It keeps scanning past an interior hit, so "tif" in "motif tiffany" still matches.
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

// mentionNames returns the names that count as you in the active session: effective showname and selected character.
func (a *App) mentionNames() []string {
	return []string{a.effectiveShowname(), a.myCharName()}
}

// mentionNamesFor returns a background tab's names: its character and saved showname, as a parked tab has no override.
func (a *App) mentionNamesFor(s *sessionState) []string {
	return []string{sessionCharName(s), a.d.Prefs.Load().Showname}
}

// sessionCharName is the character a session is playing, or "" if none is selected.
func sessionCharName(s *sessionState) string {
	if s.sess == nil || s.sess.MyCharID < 0 || s.sess.MyCharID >= len(s.sess.Chars) {
		return ""
	}
	return s.sess.Chars[s.sess.MyCharID].Name
}

// isSelfName reports whether name is, case-insensitively, one of selfNames, so your own echoed lines never alert you.
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

// captureScreenshot reads the back buffer, which ReadPixels requires on the render thread, and writes a PNG off-thread
// next to the exe under screenshots\ (hard rule 2).
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
		// Copy into Go memory so the encode outlives the surface (ABGR8888 == image.RGBA byte order).
		pix := make([]byte, len(surf.Pixels()))
		copy(pix, surf.Pixels())
		root, err := exeDataDir()
		if err != nil {
			return
		}
		dir := filepath.Join(root, "screenshots")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = writeScreenshotPNG(filepath.Join(dir, "asyncao-"+stamp+".png"), pix, int(w), int(h), pitch)
	}()
	a.warnLine = "Screenshot saved: " + rel
	a.warnAt = time.Now()
	a.pushDebug("screenshot saved: " + rel)
}

// writeScreenshotPNG encodes ABGR8888 back-buffer bytes as a PNG at path; pitch is the row stride (>= w*4).
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

// streamerIPRe matches IPv4-looking tokens, which modcall broadcasts on some servers embed.
var streamerIPRe = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// streamerMaskLine redacts one OOC display line: the sender becomes ???, IP-like tokens block out; the raw log is kept.
func streamerMaskLine(line string) string {
	if name, rest, found := strings.Cut(line, ": "); found && len(name) <= streamerNameMax {
		line = "???: " + rest
		_ = name
	}
	return streamerIPRe.ReplaceAllString(line, "█.█.█.█")
}

// streamerNameMax bounds a "name:" prefix: a colon deep in a sentence is message text, not a sender.
const streamerNameMax = 32

// oocWrapMinCharsPerRow is the conservative floor for characters per row, used only to derive the row budget below.
const oocWrapMinCharsPerRow = 4

// oocWrapMaxLinesPerEntry bounds one entry's wrapped rows, and so its link spans, against a hostile MOTD.
// Derived from oocLineCap so a legitimate entry is never clipped; the "…" marker needs a synthetic width.
const oocWrapMaxLinesPerEntry = oocLineCap / oocWrapMinCharsPerRow

// oocLinkSpan is one clickable link on one wrapped OOC row: its byte range in the displayed text, pixel range and URL.
// A link the wrap hard-split contributes one span per row sharing an id, so the highlight covers the whole link.
type oocLinkSpan struct {
	row    int32  // display-row index into a.oocWrap
	lo, hi int32  // byte range inside a.oocWrap[row] (the drawn text)
	x0, x1 int32  // pixel range relative to the row's TEXT origin (hanging indent NOT included)
	link   int32  // link id; the pieces of one wrapped link share it and are contiguous in the table
	url    string // what a click opens — read from the UNMASKED paragraph (streamer mode masks display only)
}

// oocLinksPerParagraphMax bounds the clickable links of one paragraph (hard rule 4); links past it still display.
const oocLinksPerParagraphMax = 64

// appendLinkSpans records the link regions of one wrapped paragraph; only inside oocWrapped's cached rebuild.
// Positions come from masked, the drawn text, since streamer mode shifts offsets; the URL to open comes from raw.
func (a *App) appendLinkSpans(dst []oocLinkSpan, rows []string, rowBase int, raw, masked string, nextID int32) ([]oocLinkSpan, int32) {
	rawLinks := extractURLs(raw, oocLinksPerParagraphMax)
	if len(rawLinks) == 0 || len(rows) == 0 {
		return dst, nextID // the overwhelming common case: no link in this paragraph, no work
	}
	start, startID := len(dst), nextID
	rawAt := 0          // cursor into rawLinks
	rowIdx, pos := 0, 0 // walk position: row, byte offset within it
	for _, tok := range strings.Fields(masked) {
		// Masking can eat or rewrite a link, so pair by exact match scanning forward from the cursor,
		// falling back to the next unconsumed raw link when the drawn text no longer matches.
		tlo, thi := urlTokenRange(tok)
		url := ""
		if isURLToken(tok[tlo:thi]) && rawAt < len(rawLinks) {
			for k := rawAt; k < len(rawLinks); k++ {
				if rawLinks[k] == tok[tlo:thi] {
					rawAt = k
					break
				}
			}
			url = rawLinks[rawAt]
			rawAt++
		}
		// Walk every token across the rows to keep the position aligned: the wrappers only drop whitespace at a break
		// and cut mid-token when it is wider than the column, so a token's bytes stay in order.
		consumed := 0
		for consumed < len(tok) {
			for rowIdx < len(rows) {
				row := rows[rowIdx]
				if pos >= len(row) {
					rowIdx, pos = rowIdx+1, 0
					continue
				}
				// Skip whitespace only before a token: a split token resumes at the next row's start,
				// or "ab cd" would pass as "abcd".
				if consumed == 0 && (row[pos] == ' ' || row[pos] == '\t') {
					pos++
					continue
				}
				break
			}
			if rowIdx >= len(rows) {
				break // the row budget clipped the rest of the paragraph
			}
			row := rows[rowIdx]
			n := 0
			for pos+n < len(row) && consumed+n < len(tok) && row[pos+n] == tok[consumed+n] {
				n++
			}
			if n == 0 {
				// Desync (a wrapper that rewrites text): emit no spans for this paragraph rather than misplaced ones.
				return dst[:start], startID
			}
			if url != "" {
				// Intersect with the link's trimmed range; brackets and a sentence period are not part of it.
				ovLo, ovHi := max(consumed, tlo), min(consumed+n, thi)
				if ovHi > ovLo {
					dst = append(dst, oocLinkSpan{
						row:  int32(rowBase + rowIdx),
						lo:   int32(pos + ovLo - consumed),
						hi:   int32(pos + ovHi - consumed),
						link: nextID,
						url:  url,
					})
				}
			}
			pos, consumed = pos+n, consumed+n
		}
		if url != "" {
			nextID++
		}
	}
	return a.measureLinkSpans(dst, start, rows, rowBase), nextID
}

// measureLinkSpans fills the pixel range of dst[start:] in the face each row draws in, so tint and hit test match.
// A row the per-glyph raster draws has no advance to trust here, so its first link covers the whole row.
func (a *App) measureLinkSpans(dst []oocLinkSpan, start int, rows []string, rowBase int) []oocLinkSpan {
	w := start
	for k := start; k < len(dst); k++ {
		s := dst[k]
		row := rows[int(s.row)-rowBase]
		font := a.elemFontFor(elemServerChatlog, a.oocPct, row) // memoized; also the pick coversFace() reads
		if render.NeedsEmojiFallback(row) || !a.ctx.coversFace(font, row) {
			if w > start && dst[w-1].row == s.row {
				continue // this row already collapsed to a whole-row span
			}
			s.lo, s.hi = 0, int32(len(row))
			s.x0, s.x1 = 0, fontWidth(font, row)
		} else {
			s.x0 = fontWidth(font, row[:s.lo])
			s.x1 = fontWidth(font, row[:s.hi])
		}
		dst[w] = s
		w++
	}
	return dst[:w]
}

// oocRowLinks returns row li's link spans, a view into the flat table with no allocation.
func (a *App) oocRowLinks(li int) []oocLinkSpan {
	if li < 0 || li+1 >= len(a.oocWrapLinkAt) {
		return nil
	}
	return a.oocWrapLink[a.oocWrapLinkAt[li]:a.oocWrapLinkAt[li+1]]
}

// oocWrapped returns the OOC log as display lines, word-wrapped with newlines split and streamer masking applied.
// Cached against log seq, width, font scale and mask.
func (a *App) oocWrapped(width int32) []string {
	streamer := a.d.Prefs.Load().StreamerModeOn
	// Key on the resolved scale, so a theme swap that only changes the point size re-wraps.
	pct := a.elemPct(elemServerChatlog, a.oocPct)
	if a.oocWrap != nil && a.oocWrapSeq == a.oocSeq && a.oocWrapEpoch == a.logViewEpoch &&
		a.oocWrapGen == a.ctx.fontChainGen &&
		a.oocWrapW == width && a.oocWrapPct == pct && a.oocWrapMask == streamer {
		return a.oocWrap
	}
	out := a.oocWrap[:0]
	name := a.oocWrapName[:0]  // parallel to out: speaker on each entry's first display line
	cont := a.oocWrapCont[:0]  // parallel to out: wrap continuation rows (hanging indent)
	src := a.oocWrapSrc[:0]    // parallel to out: source oocLog entry index
	links := a.oocWrapLink[:0] // NOT parallel: the flat link-span table, rows index it via linkAt below
	var nextLinkID int32       // link ids are unique across the whole rebuild, so a span identifies its link alone
	// Per-candidate measure matching drawOOCLogRow: covering face, per-rune faces for emoji, and the bold name split.
	nameOn := a.d.Prefs.Load().NameColors
	boldNames := a.logRowBoldPref(logSelOOC)
	for i, entry := range a.oocLog {
		sp := ""
		if i < len(a.oocSpeakers) {
			sp = a.oocSpeakers[i]
		}
		measure := func(s string) int32 {
			return a.logDrawnWidthNamed(elemServerChatlog, a.oocPct, s, sp, nameOn, boldNames)
		}
		entryFirst := true // the speaker tints only the entry's FIRST display line
		// The budget is per entry, across all paragraphs. A visible "…" is added only if it runs out.
		remaining := oocWrapMaxLinesPerEntry
		truncated := false
		for _, rawPara := range strings.Split(entry, "\n") {
			if remaining <= 0 {
				truncated = true // out of budget with paragraphs still unrendered
				break
			}
			para := rawPara
			if streamer {
				para = streamerMaskLine(para)
			}
			// Trigger the font load off the whole paragraph: per-candidate picks see only rows within budget,
			// and a script past the cut would otherwise never load its face.
			a.ctx.noteScript(para)
			trimmed := strings.TrimRight(para, "\r")
			// Rune-granular wrap for emoji lines: the word-wrap would size emoji as tofu and split mid-rune.
			// Ask for remaining+1 rows: wrappers cap silently, so the extra row separates an exact fit from overflow.
			// That also sidesteps maxLines <= 0, where wrapToWidth yields nothing and WrapMeasured is unbounded.
			probe := remaining + 1
			var lines []string
			if ef := a.ctx.EmojiFont(pct); ef != nil && render.NeedsEmojiFallback(trimmed) {
				lines = render.WrapMeasured(measure, trimmed, width, probe)
			} else {
				lines = wrapToWidthMeasured(measure, trimmed, width, probe)
			}
			if len(lines) > remaining {
				lines = lines[:remaining] // overflowed the budget — clip and flag the marker
				truncated = true
			}
			if len(lines) == 0 {
				out = append(out, "") // blank MOTD spacer lines survive
				name = append(name, "")
				cont = append(cont, false)
				src = append(src, i)
				remaining-- // a spacer row spends budget too, so the count stays bounded
				continue
			}
			rowBase := len(out) // where this paragraph's rows land — the spans index into the WRAP, not into lines
			for row, ln := range lines {
				out = append(out, ln)
				cont = append(cont, row > 0) // rows the WRAP made are continuations (hanging indent); the paragraph's own newline isn't
				src = append(src, i)         // all rows of this entry carry its oocLog index
				if entryFirst {
					name = append(name, sp)
					entryFirst = false
				} else {
					name = append(name, "")
				}
			}
			// Each URL on the line gets its own span, positioned on the drawn rows but opening the link as it arrived.
			links, nextLinkID = a.appendLinkSpans(links, lines, rowBase, rawPara, trimmed, nextLinkID)
			remaining -= len(lines)
			if truncated {
				break // clipped this paragraph — no point wrapping later ones
			}
		}
		if truncated {
			// One marker row for the whole entry, parallel slices in step; cont=true hangs it under the entry.
			out = append(out, "…")
			name = append(name, "")
			cont = append(cont, true)
			src = append(src, i)
		}
	}
	if out == nil {
		out = []string{}
	}
	// linkAt[r] is the first span of row r, so its spans are links[linkAt[r]:linkAt[r+1]]; hence len(rows)+1 entries.
	linkAt := a.oocWrapLinkAt[:0]
	for r, k := 0, 0; r <= len(out); r++ {
		for k < len(links) && int(links[k].row) < r {
			k++
		}
		linkAt = append(linkAt, int32(k))
	}
	a.oocWrap, a.oocWrapName, a.oocWrapCont, a.oocWrapSrc = out, name, cont, src
	a.oocWrapLink, a.oocWrapLinkAt = links, linkAt
	a.oocWrapSeq, a.oocWrapW, a.oocWrapPct, a.oocWrapMask = a.oocSeq, width, pct, streamer
	a.oocWrapEpoch = a.logViewEpoch
	a.oocWrapGen = a.ctx.fontChainGen
	return out
}

// icWrapLine is one display row of the wrapped IC log: a text slice plus its source entry.
type icWrapLine struct {
	text  string
	entry int // index into icLog
	// headRunes is the leading timestamp and speaker runes; bodyOff is this row's rune offset in the entry's body.
	// Both are 0 unless the entry has inline-colour runs.
	headRunes int
	bodyOff   int
}

// logWrapIndentPx is the hanging indent of wrapped rows; drawers wrap against width-logWrapIndentPx.
const logWrapIndentPx = 14

// icRowIsEntryStart reports whether row li of a.icWrap begins a new icLog entry rather than continuing the row above.
func (a *App) icRowIsEntryStart(li int) bool {
	return li <= 0 || li >= len(a.icWrap) || a.icWrap[li-1].entry != a.icWrap[li].entry
}

// logRowIndent is row li's draw indent in log which; selection hit-testing uses the same offset.
func (a *App) logRowIndent(which, li int) int32 {
	if which == logSelIC {
		if !a.icRowIsEntryStart(li) {
			return logWrapIndentPx
		}
		return 0
	}
	if li >= 0 && li < len(a.oocWrapCont) && a.oocWrapCont[li] {
		return logWrapIndentPx
	}
	return 0
}

// icWrapMaxLinesPerEntry bounds one entry's wrapped rows; IC messages cap at 256 chars on the wire.
const icWrapMaxLinesPerEntry = 16

// icWrapped returns the filtered IC log as wrapped rows, cached against log seq, query, width and font scale.
func (a *App) icWrapped(width int32, showStamps bool) []icWrapLine {
	// Key on the resolved scale.
	pct := a.elemPct(elemICChatlog, a.logPct)
	if a.icWrap != nil && a.icWrapSeq == a.icLogSeq && a.icWrapEpoch == a.logViewEpoch &&
		a.icWrapQuery == a.logSearch && a.icWrapShowname == a.logFilterShowname && a.icWrapPos == a.logFilterPos &&
		a.icWrapColor == a.logFilterColor &&
		a.icWrapW == width && a.icWrapPct == pct && a.icWrapGen == a.ctx.fontChainGen &&
		a.icWrapStamp == showStamps {
		return a.icWrap
	}
	out := a.icWrap[:0]
	// Per-candidate measure, as for OOC, so wrap and draw agree. Prefs are nil in headless fixtures, hence the guard.
	nameOn := false
	boldNames := false
	if a.d.Prefs != nil {
		nameOn = a.d.Prefs.Load().NameColors
		boldNames = a.logRowBoldPref(logSelIC)
	}
	for _, i := range a.icLogFiltered() {
		entry := &a.icLog[i]
		measure := func(s string) int32 {
			return a.logDrawnWidthNamed(elemICChatlog, a.logPct, s, entry.speaker, nameOn, boldNames)
		}
		text := entry.text
		stampRunes := 0
		if showStamps && entry.stamp != "" {
			prefix := entry.stamp + "  "
			text = prefix + text
			stampRunes = utf8.RuneCountInString(prefix)
		}
		// Wrap against the drawn width; emoji entries use the rune-granular wrap, as the word-wrap sizes them as tofu.
		// Trigger the font load off the whole entry first: per-candidate picks see only rows within the cap.
		a.ctx.noteScript(text)
		var wrapped []string
		if ef := a.ctx.EmojiFont(pct); ef != nil && render.NeedsEmojiFallback(text) {
			wrapped = render.WrapMeasured(measure, text, width, icWrapMaxLinesPerEntry)
		} else {
			wrapped = wrapToWidthMeasured(measure, text, width, icWrapMaxLinesPerEntry)
		}
		// For inline-colour entries, track each row's rune offset and fold the head into a leading default-colour span.
		styled := len(entry.styles) > 0
		bodyStart := stampRunes + entry.bodyRuneStart
		off := 0
		for _, ln := range wrapped {
			row := icWrapLine{text: ln, entry: i}
			if styled {
				n := utf8.RuneCountInString(ln)
				if head := bodyStart - off; head > 0 {
					row.headRunes = head
				}
				if row.headRunes > n {
					row.headRunes = n
				}
				if bodyOff := off - bodyStart; bodyOff > 0 {
					row.bodyOff = bodyOff
				}
				off += n
			}
			out = append(out, row)
		}
	}
	if out == nil {
		out = []icWrapLine{} // non-nil marks the cache as populated
	}
	a.icWrap, a.icWrapSeq, a.icWrapQuery, a.icWrapW, a.icWrapPct, a.icWrapGen, a.icWrapStamp =
		out, a.icLogSeq, a.logSearch, width, pct, a.ctx.fontChainGen, showStamps
	a.icWrapShowname, a.icWrapPos, a.icWrapColor = a.logFilterShowname, a.logFilterPos, a.logFilterColor
	a.icWrapEpoch = a.logViewEpoch
	return out
}

// constFont adapts one fixed face to wrapToWidth's per-string picker.
func constFont(f *ttf.Font) func(string) *ttf.Font {
	return func(string) *ttf.Font { return f }
}

// wrapFallbackCharPx is the per-byte width assumed with no font: a headless Ctx or a measure that errored.
const wrapFallbackCharPx = 8

// fontWidth measures s in font, else by wrapFallbackCharPx; wrap and link spans share it, so they cannot drift.
func fontWidth(font *ttf.Font, s string) int32 {
	if font == nil {
		return int32(len(s) * wrapFallbackCharPx)
	}
	w, _, err := font.SizeUTF8(s)
	if err != nil {
		return int32(len(s) * wrapFallbackCharPx)
	}
	return int32(w)
}

// logDrawnWidthNamed measures a row with a bold speaker span, as drawLogLineNamed splits it: bold glyphs are wider,
// and a plain measure would let the last word overflow. speaker == "" or bold == false measures plain.
func (a *App) logDrawnWidthNamed(el themeFontElem, userPct int, s, speaker string, nameOn, bold bool) int32 {
	c := a.ctx
	primary := a.elemFontFor(el, userPct, s) // also populates the pick coversFace() reads below
	if primary == nil {
		return fontWidth(nil, s) // headless Ctx: the rough per-byte stand-in
	}
	ef := a.elemEmoji(el, userPct)
	if (ef == nil || !render.NeedsEmojiFallback(s)) && c.coversFace(primary, s) {
		if bold {
			if _, boldEndByte, ok := c.logRowSplit(primary, ef, s, speaker, nameOn, bold); ok {
				runes := []rune(s)
				boldEnd := utf8.RuneCountInString(s[:boldEndByte])
				nameW, nameOK := c.fontTextWidthWeight(primary, string(runes[:boldEnd]), true)
				restW, restOK := c.fontTextWidthWeight(primary, string(runes[boldEnd:]), false)
				if nameOK && restOK {
					return nameW + restW
				}
			}
		}
		return fontWidth(primary, s)
	}
	runes := []rune(s)
	return render.MeasureFallbackWidth(c.coverRunes(primary, runes), ef, runes)
}

func wrapToWidth(pick func(string) *ttf.Font, text string, maxW int32, maxLines int) []string {
	return wrapToWidthMeasured(func(s string) int32 { return fontWidth(pick(s), s) }, text, maxW, maxLines)
}

// wrapToWidthMeasured breaks text using measure for a candidate's drawn width, since per-glyph raster rows
// are laid out from per-rune faces that no single face's SizeUTF8 predicts.
func wrapToWidthMeasured(measure func(string) int32, text string, maxW int32, maxLines int) []string {
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
	width := measure
	for _, word := range strings.Fields(text) {
		// Hard-split words wider than the column on rune indices: CJK and Thai have no spaces,
		// so this always runs on them, and splitting by bytes would produce invalid UTF-8.
		rw := []rune(word)
		for width(word) > maxW && len(rw) > 1 {
			cut := len(rw) / 2
			for cut > 1 && width(string(rw[:cut])) > maxW {
				cut /= 2
			}
			flush()
			lines = append(lines, string(rw[:cut]))
			rw = rw[cut:]
			word = string(rw)
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

// wrapParagraphs wraps server prose with embedded newlines, which wrapToWidthMeasured's strings.Fields discards.
// maxLines is shared across paragraphs and must be >= 1, as the wrappers disagree on <= 0.
// ok=false means the caller must show a truncation marker: these payloads put the code or appeal link at the tail.
func wrapParagraphs(measure func(string) int32, s string, maxW int32, maxLines int) (lines []string, ok bool) {
	if maxLines < 1 {
		return nil, false
	}
	remaining := maxLines
	fit := true
	for _, rawPara := range strings.Split(s, "\n") {
		if remaining <= 0 {
			fit = false // out of budget with paragraphs still unrendered
			break
		}
		para := strings.TrimRight(rawPara, "\r") // tolerate CRLF payloads
		if strings.TrimSpace(para) == "" {
			// A blank line is a deliberate break, so it stays as a spacer row and spends budget.
			lines = append(lines, "")
			remaining--
			continue
		}
		// wrapToWidthMeasured caps silently, so asking for one extra row separates an exact fit from an overflow.
		got := wrapToWidthMeasured(measure, para, maxW, remaining+1)
		if len(got) > remaining {
			got = got[:remaining]
			fit = false
		}
		lines = append(lines, got...)
		remaining -= len(got)
	}
	return lines, fit
}

// paraWrapCache memoizes one wrapParagraphs result for a modal that redraws every frame,
// as the wrap allocates and the text cannot change while the box is open.
type paraWrapCache struct {
	lines []string
	fit   bool
	src   string
	width int32
	rows  int
	gen   int
	// mark is part of the key: it decides the last row on overflow, so a hit across two markers would be stale.
	mark string
}

// get returns the wrapped lines and whether everything fit, rewrapping when text, width, budget or font chain change.
// gen must be ctx.fontChainGen: a non-Latin notice is measured before its fallback face lands.
// mark, when non-empty, takes the last row on overflow, as the tail of a notice holds the access code and appeal link.
func (p *paraWrapCache) get(measure func(string) int32, gen int, s string, width int32, rows int, mark string) ([]string, bool) {
	if p.lines != nil && p.src == s && p.width == width && p.rows == rows && p.gen == gen &&
		p.mark == mark {
		return p.lines, p.fit
	}
	lines, fit := wrapParagraphs(measure, s, width, rows)
	if !fit && mark != "" {
		// Spend the last row rather than exceed the budgeted height; the caller's Copy button has the whole text.
		keep := rows - 1
		if keep < 0 {
			keep = 0
		}
		if len(lines) > keep {
			lines = lines[:keep]
		}
		lines = append(lines, mark)
	}
	if lines == nil {
		lines = []string{} // non-nil, so an empty result still reads as baked
	}
	p.lines, p.fit = lines, fit
	p.src, p.width, p.rows, p.gen, p.mark = s, width, rows, gen, mark
	return p.lines, p.fit
}

// icLogFiltered returns indices of log entries matching the search box and the showname and pos filters ("" for none).
// Case-insensitive; cached against log seq, epoch, query, showname and pos.
func (a *App) icLogFiltered() []int {
	if a.icFilter != nil && a.icFilterSeq == a.icLogSeq && a.icFilterEpoch == a.logViewEpoch &&
		a.icFilterQuery == a.logSearch && a.icFilterShowname == a.logFilterShowname && a.icFilterPos == a.logFilterPos &&
		a.icFilterColor == a.logFilterColor {
		return a.icFilter
	}
	out := a.icFilter[:0]
	q := strings.ToLower(strings.TrimSpace(a.logSearch))
	showname := strings.ToLower(strings.TrimSpace(a.logFilterShowname))
	pos := strings.ToLower(strings.TrimSpace(a.logFilterPos))
	for i := range a.icLog {
		e := &a.icLog[i]
		if q != "" && !strings.Contains(strings.ToLower(e.text), q) {
			continue
		}
		if showname != "" && !strings.Contains(strings.ToLower(e.speaker), showname) {
			continue
		}
		if pos != "" && !strings.EqualFold(e.pos, pos) {
			continue
		}
		if !icEntryUsesColor(e, a.logFilterColor) {
			continue
		}
		out = append(out, i)
	}
	if out == nil {
		out = []int{} // non-nil marks the cache as populated
	}
	a.icFilter, a.icFilterSeq, a.icFilterQuery = out, a.icLogSeq, a.logSearch
	a.icFilterShowname, a.icFilterPos, a.icFilterColor = a.logFilterShowname, a.logFilterPos, a.logFilterColor
	a.icFilterEpoch = a.logViewEpoch
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

// exportICLog writes the log next to the exe as TXT or HTML; snapshot on the render thread, file I/O off it.
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
		root, err := exeDataDir()
		if err != nil {
			return
		}
		dir := filepath.Join(root, "logs")
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

// icHTMLColor maps an AO text color index onto a CSS color matching render.TextColor.
func icHTMLColor(index int) string {
	col := render.TextColor(index)
	return fmt.Sprintf("#%02x%02x%02x", col.R, col.G, col.B)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
