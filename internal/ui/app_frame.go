package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

func (a *App) RefreshServers() {
	if a.lobbyFetching {
		return
	}
	a.lobbyFetching = true
	// Stamped here so every fetch start pushes out the on-open auto-refresh cap, or entering the lobby after a manual
	// refresh fires a second request. Below the dedup check: a coalesced call started nothing.
	a.lastLobbyRefreshAt = a.now()
	a.lobbyStatus = "Fetching server list..."
	// Ping state survives a refresh so a ping-sorted lobby keeps its order on re-entry; a.pings is keyed by URL,
	// and pollLobbyFetch prunes it when a fresh list lands (hard rule 4).
	urls := a.masterListURLs()
	ch := a.lobbyResult
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lobbyRefreshTimeout)
		defer cancel()
		entries, err := network.FetchServerLists(ctx, urls)
		ch <- lobbyFetch{entries: entries, err: err}
	}()
}

// masterListURLs resolves the endpoints to poll: a non-default --master flag, else Settings, else built-in defaults.
func (a *App) masterListURLs() []string {
	if a.d.MasterURL != "" && a.d.MasterURL != network.DefaultMasterServerURL {
		return []string{a.d.MasterURL}
	}
	if alt := a.d.Prefs.Load().MasterListURL; alt != "" {
		return []string{alt}
	}
	return network.DefaultMasterServerURLs
}

// lobbyAutoRefreshDue reports whether the last fetch started lobbyAutoRefreshMinInterval ago; a zero last is due.
func lobbyAutoRefreshDue(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= lobbyAutoRefreshMinInterval
}

func (a *App) pollLobbyFetch() {
	select {
	case res := <-a.lobbyResult:
		a.lobbyFetching = false
		if res.err != nil {
			// A failed fetch keeps the list: a master hiccup with no user action must not erase a joinable list
			// or lose pings by pruning against it. Only a boot failure rebuilds, so favorites still show.
			a.lobbyStatus = "Master list error: " + res.err.Error()
			if len(a.servers) == 0 {
				a.servers = a.mergedFavorites()
			}
			return
		}
		// Carry the selection by WebSocketURL: mergedFavorites re-sorts, so an index could join another server.
		prevSel := ""
		if a.selServer >= 0 && a.selServer < len(a.servers) {
			prevSel = a.servers[a.selServer].WebSocketURL()
		}
		a.lobbyStatus = fmt.Sprintf("%d servers", len(res.entries))
		a.masterEntries = res.entries
		a.servers = a.mergedFavorites()
		// Each landing re-prunes to the current list (hard rule 4).
		a.pruneStalePings()
		a.reselectServer(prevSel)
	default:
	}
}

// reselectServer restores the selection to the row matching url, or clears it if gone, and rebuilds the description.
func (a *App) reselectServer(url string) {
	a.selServer, a.descLines, a.descLinks = -1, nil, nil
	if url == "" {
		return
	}
	for i := range a.servers {
		if a.servers[i].WebSocketURL() == url {
			a.selectServerRow(i, a.winW)
			return
		}
	}
}

// pruneStalePings drops connect times for servers no longer in the merged list (hard rule 4).
func (a *App) pruneStalePings() {
	if len(a.pings) == 0 {
		return
	}
	live := make(map[string]struct{}, len(a.servers))
	for i := range a.servers {
		live[a.servers[i].WebSocketURL()] = struct{}{}
	}
	for url := range a.pings {
		if _, ok := live[url]; !ok {
			delete(a.pings, url)
		}
	}
}

// mergedFavorites merges the phone book into a copy of the master list, sorted favorites first and legacy last.
func (a *App) mergedFavorites() []network.ServerEntry {
	entries := make([]network.ServerEntry, len(a.masterEntries))
	copy(entries, a.masterEntries)
	entries = network.MergeFavorites(entries, a.d.Prefs.Load().FavoriteServers())
	if a.pingMode {
		sortByPing(entries, a.pings) // lobby "Ping" sort: connect-time ascending
	} else {
		network.SortServers(entries)
	}
	return entries
}

func (a *App) Frame(dt time.Duration, winW, winH int32) {
	defer a.frameCrashLog() // last-resort: capture the stack of any unrecovered panic before it kills the app
	a.frameNow = time.Now()
	a.lastFrameDrawn = a.frameNow // SkipFrame's heartbeat: a real frame was drawn

	if a.d.Audio != nil {
		for _, line := range render.PollDebugLogs() {
			a.pushDebug(line)
		}
	}

	// Snapshot the store generation at frame start: uploads during this frame earn at most one follow-up frame.
	a.uiDirty = false
	if a.d.Store != nil {
		a.drawnGen = a.d.Store.Generation()
	}
	// Resolve the shape and its masks once so every widget draw is a field read; the "sharp" preset exits early.
	a.refreshShapeMasks()
	// Read once per frame: themeFrame reads it at theme-chrome sites on screens that never open an element pass.
	a.themeFXFrozen = a.reduceMotionNow()
	a.drawnCaretOn = a.ctx.caretOn
	// The draw sites below set this; it is promoted at frame end so SkipFrame reads what this frame drew.
	a.frameAnimChrome = false
	// A draw leaving a streaming cell blank sets this, so NextWakeDelay keeps re-rendering until it fills.
	a.frameDemandPending = false
	defer func() {
		a.drawnAnimChrome = a.frameAnimChrome
		a.drawnDemandPending = a.frameDemandPending
	}()
	a.ctx.BeginDraw()           // the visible-field order rebuilds as this frame's TextFields register
	a.winW, a.winH = winW, winH // cached for deep draw helpers (preview clamp)
	a.frameDtMs = float32(dt.Seconds() * 1000)
	a.recordFrameDt(a.frameDtMs)
	// Push the initial Discord presence so it appears on launch; updatePresence no-ops when unset or toggled off.
	if !a.presenceInit {
		a.presenceInit = true
		a.updatePresence()
	}
	// Once per frame, so every HoverPreview call this frame reads a consistent value without locks.
	a.ctx.SetHoverPreview(a.d.Prefs.Load().SpritePreviewOn, a.d.Prefs.PreviewHoverDelay())
	// The hold timer runs in BeginFrame.
	if on, keyName, ms := a.d.Prefs.Load().HoldClear(); on {
		a.ctx.SetHoldClear(true, sdl.GetKeyFromName(keyName), time.Duration(ms)*time.Millisecond)
	} else {
		a.ctx.SetHoldClear(false, sdl.K_UNKNOWN, 0)
	}
	// HandleEvent sets the chord flag when focused; the kit's text fields check this pref on consumption.
	a.ctx.SetWordDelete(a.d.Prefs.Load().WordDelete)
	// Consumed so plain-key macro or character binds named "f3" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F3 {
		a.perfHUD = !a.perfHUD
		a.ctx.keyPressed = 0
	}
	// Consumed so plain-key macro or character binds named "f1" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F1 {
		if a.showHotkeys {
			a.showHotkeys = false
			a.hkCache = nil
		} else {
			a.openHotkeyCheatSheet() // (re)builds the rows so it reflects current bindings
		}
		a.ctx.keyPressed = 0
	}
	// Consumed so plain-key macro or character binds named "f8" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F8 {
		a.toggleDebugPanel()
		a.ctx.keyPressed = 0
	}
	// Esc backs out one layer per press. Test escPressed: HandleEvent maps K_ESCAPE there, not to keyPressed.
	// No editor guard here: a guard assumes a draw-time handler answers, which fails on frames the editor's draw does
	// not run, a hard lock. closeTopOverlay owns the editors, so whoever is on top consumes the key.
	if a.ctx.escPressed && !a.capturingKey() {
		// The topmost popup or panel first; only if nothing was open, drop a focused field or leave a full-screen menu.
		handled := a.closeTopOverlay()
		if !handled {
			switch a.screen {
			case ScreenSettings, ScreenAbout, ScreenChangelog, ScreenServerHelp, ScreenLogs, ScreenHelp:
				if a.ctx.focusID != "" {
					a.ctx.focusID = "" // first ESC: drop a focused field
				} else {
					a.screen = a.prevScreen // then leave the menu
				}
				handled = true
			case ScreenThemeEditor:
				// The editor's order: preview, focused field, top panel, selection, then leave.
				// Preview consumes the press: falling through would arm the discard chip on an unseen document.
				// The panel is above the selection because editorPanelUp blocks selection changes.
				// Leave through closeThemeEditor: a.prevScreen would send a courtroom-opened editor to the last menu.
				switch {
				case a.editorPreviewOn():
					a.exitEditorPreview()
				case a.ctx.focusID != "":
					a.ctx.focusID = ""
				case a.editorPanelUp():
					a.closeEditorPanels()
				case a.te != nil && a.te.selN > 0:
					a.te.selectClear()
				default:
					// The same exit as the Back button, so Esc cannot discard unsaved work without asking.
					a.requestEditorExit()
				}
				handled = true
			case ScreenLobby:
				// Esc offers to quit, the way out when fullscreen hides the window's close button.
				a.requestQuit()
				handled = true
			case ScreenCourtroom:
				// Esc toggles the Extras box, whose Disconnect entry confirms; leaving must not be a reflex key.
				// A focused field eats the first Esc, which is how you step out of the chat box (chatfocus.go).
				if a.ctx.focusID != "" {
					a.ctx.focusID = ""
				} else {
					a.toggleExtrasBox()
				}
				handled = true
			case ScreenCharSelect:
				// From the courtroom this backs out like Back; a fresh char select can only leave, with a confirm.
				if a.ctx.focusID != "" {
					a.ctx.focusID = ""
				} else if a.room != nil {
					a.screen = ScreenCourtroom
				} else {
					a.requestDisconnect()
				}
				handled = true
			}
		}
		if handled {
			a.ctx.escPressed = false // consume it so a draw-time Esc handler can't double-fire
		}
	}
	// The keyboard way out when a too-big window drags the Settings controls off the monitor.
	if a.ctx.fullscreenReq {
		a.toggleFullscreen()
	}
	// Computed once so both on-screen logs read the same edge; whichever draws first can't steal it.
	a.logSelPressed = a.ctx.mouseDown && !a.logSelPrevDown
	a.logSelPrevDown = a.ctx.mouseDown
	// Consumed pre-screen so no later chord consumer (dispatcher, quick-connect, hotkey) double-fires.
	// Layout editors keep their own Ctrl+Z (editorUndoChord).
	if a.ctx.focusID != "" && !a.classicEdit && !a.layoutEdit {
		switch a.ctx.hotkey {
		case sdl.K_z:
			a.ctx.undoReq = !a.ctx.shiftHeld
			a.ctx.redoReq = a.ctx.shiftHeld
			a.ctx.hotkey = 0
		case sdl.K_y:
			a.ctx.redoReq = true
			a.ctx.hotkey = 0
		}
	}
	// The theme editor claims its chord here: with no server no courtroom pass runs, so c.hotkey goes unread.
	// After the focused-field branch so a focused row keeps its own undo; before quick-connect
	// so an owned key fires nothing else. The two overlay editors keep claiming theirs via handleHotkeys.
	if a.themeEditorOpen() {
		a.editorUndoChord()
	}
	a.pumpTabRestore()  // restore-on-launch: one reconnect/frame, then idle
	a.fireAutoConnect() // one-shot: auto-connect to the last server on launch (opt-in)
	// The courtroom hotkey handler never runs in the lobby, so dispatch here while offline.
	// Not under the notice box: the server just removed us, and a keypress must not reconnect past it.
	if a.sess == nil && !a.serverNoticeDlg.open && a.ctx.hotkey != 0 && strings.ToLower(sdl.GetKeyName(a.ctx.hotkey)) == a.hotkeyFor(hotkeyQuickConnect) {
		a.quickConnect()
	}
	a.maybeKickUpdateCheck()        // one-shot, off the boot path (fires on frame 1)
	a.pollUpdate()                  // drain a found release
	a.handleUpdateInput(winW, winH) // modal/chip clicks resolve before screens
	a.pumpConnection()
	a.pumpBackgroundTabs()
	a.voicePump()              // live voice: drain mic→encode→send + decode→mix→play (no-op unless joined)
	a.handleTabBar(winW, winH) // chip clicks resolve BEFORE screens see them
	if a.pendingControlSwap {  // a click on the floating client last frame → take control now (before any draw)
		a.pendingControlSwap = false
		// activateTab's parkActive would copy an open disconnect dialog into the frozen tab and resurface it cross-tab.
		// The pinned client draws after drawCourtroom's pointer block lifts, so stop it here; always consume the flag.
		if !a.disconnectDlg.open {
			a.controlPinnedClient()
		}
	}
	a.drainWarnings()
	a.drainMusicFailures() // transient music-fetch failures → jukebox warn line (§1.1)
	a.drainThumbs()        // loaded low-q stand-ins → T1 (render thread; bounded per frame)
	a.pollThemeApply()
	a.pollThemeBundle() // lands a finished .aotheme inspect/extract (themeimport.go)
	a.pollThemeCopy()   // lands a finished copy-for-editing (themecopy.go)
	a.pollThemeCreate() // lands a finished theme mint (themecreate.go)
	a.pollManifest()
	a.maybeProbeCasing() // OFF unless the user picked Auto casing (cheap no-op otherwise)
	a.pollCasingProbe()
	a.pollMountIndex() // lands a finished local-pack index (no-op when no mounts)
	a.pollFontChain()
	a.pollLogBrowser()
	a.pollEmojiFont()
	a.updatePingLoop()             // #128: keep the ping loop targeting the active conn (no-op/no goroutine when the chip is off)
	if a.ctx.TakeWantsFallback() { // a non-ASCII rune was drawn → kick the one broad-font read
		a.ensureFallbackFontLoad()
	}
	a.pollFallbackFont()
	if a.ctx.TakeWantsCJK() { // a CJK rune was drawn → kick the (independent) CJK-font read
		a.ensureCJKFontLoad()
	}
	a.pollCJKFont()
	a.drainMissingRunes() // a rune NOTHING covered → ask the census for an installed face
	a.pollCensusFont()
	a.pollErrorSprite()    // land an off-thread custom missingno image → UploadPinned over MissingKey
	a.pollOverlayRosters() // land resolved effects rosters + theme-tier overlay art (effectspicker.go)
	a.pollServerEvidencePicker()
	a.pollNotebook()
	a.pollJukebox()
	a.pollCharBind()
	a.pollJukeBind()
	a.pollMacroBind()
	a.pollShownameBind()
	a.pollICPhraseBind()
	a.pollVoicePTT()        // voice push-to-talk: capture the rebind, or toggle the mic on the bound key
	a.pollStylePresetBind() // #126
	// After every bind-capture poll, which owns the next keypress; before the draws, so the first character lands.
	a.claimTypingForIC()
	a.pollAutoReconnect() // M2: due auto-retry fires from the lobby; a single time-compare otherwise
	a.pollTimer()         // #97 local alarm: one compare while running, zero cost when idle
	a.pollDownload()
	a.pollMakerExport() // M16: deliver the self-contained archive export result
	a.tickContentJob()  // content report / package: drain probe results + poll the package goroutine (no-op when idle)
	a.pollGifExport()   // M16: deliver the off-thread GIF encode result
	a.pollCharMeta()    // land remote char.ini fetches (per-character blips + chatbox skins)
	a.pollBgList()      // drain bg discovery even when the picker is closed (slideshow)
	a.processOOCQueue()
	a.iconAskBudget = charIconAskPerFrame // shared demand budget (icons, emote buttons)
	switch {
	case a.gifExporting:
		// GIF export drives the viewport in its draw-phase tick; driving the scene too would fight over it.
	case a.replaying && a.replayRoom != nil:
		// Feed the next event when the room goes idle; wrapped so a panic stops only the replay.
		a.driveReplay(dt)
	case a.makerOpen:
		// Drive the preview room, so the shared viewport's animation state tracks the previewed line.
		a.driveMakerPreview(dt)
	case a.editorPreviewOn():
		// As with the maker: the viewport must track the room on screen. The live room is not advanced, or a message
		// playing out behind the preview would lose the frames it needed.
		a.driveEditorPreview(dt)
	case a.room != nil:
		a.healScenery()
		if a.roomPreAdvanced {
			a.roomPreAdvanced = false // the audio-paced loop already advanced the room this present cycle; just draw it
		} else {
			a.room.Update(dt)
		}
		a.applySpriteOverrides()
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.SetSpriteLoadMode(a.vpSpriteLoadMode())                                                  // cold-load flash mitigation (default hold-previous = webAO-style bridge; Blank = the original byte-identical gap)
		a.d.Viewport.SetClipSprites(a.d.Prefs.Load().ClipSpritesToStage)                                      // viewport sprite mask (default ON): offsets can't spill past the stage
		a.d.Viewport.SetHoldMaxAge(time.Duration(a.d.Prefs.Load().HoldPrevMaxAgeMsVal) * time.Millisecond)    // hold-previous stand-in cap (0 = forever)
		a.d.Viewport.SetHoldDebugTint(a.d.Prefs.Load().HoldDebugTint)                                         // amber-tint stand-ins (diagnostics)
		a.d.Viewport.SetThumbSprites(a.d.Prefs.Load().ThumbCache)                                             // opt-in low-q thumbnail stand-ins on the cold miss path
		a.d.Viewport.SetMissingno(a.d.Prefs.Load().ShowMissingPlaceholder)                                    // AO2 placeholder for conclusively-missing sprites (default ON)
		a.d.Viewport.SetCrossfade(a.crossfadeDur())                                                           // speaker-swap blend (0 = off; zeroed under Reduce motion)
		a.d.Viewport.SetPreanimLoop(a.d.Prefs.Load().LoopPreanim)                                             // opt-in: keep preanims wrapping (default OFF; AO2 plays them once)
		a.d.Viewport.SetPostFX(a.postFX())                                                                    // #10 retro overlays
		a.d.Viewport.SetWeather(render.Weather(a.d.Prefs.WeatherType()), a.d.Prefs.Load().WeatherIntensity()) // #124 ambient weather
		a.d.Viewport.Update(&a.room.Scene, dt)
		a.logSpeakerAnimDiag() // #110: log the speaker sprite's frame/delay stats once per base change
		// This frame is the forced draw and the viewport is bound to the new overlay, so just sync (noteOverlayStart).
		a.drawnOverlayGen = a.room.Scene.Overlay.Gen
		if a.splitActive() { // drive the pinned right-pane stage on its OWN viewport
			a.splitRoom.Update(dt)
			a.applySpriteOverridesTo(a.splitRoom) // Hide-desk / Missingno / offsets are user choices, not per-room ones
			a.splitVP.SetSpriteFX(a.spriteFX())
			a.splitVP.SetSpriteLoadMode(a.vpSpriteLoadMode())
			a.splitVP.SetClipSprites(a.d.Prefs.Load().ClipSpritesToStage)
			a.splitVP.SetHoldMaxAge(time.Duration(a.d.Prefs.Load().HoldPrevMaxAgeMsVal) * time.Millisecond)
			a.splitVP.SetHoldDebugTint(a.d.Prefs.Load().HoldDebugTint)
			a.splitVP.SetThumbSprites(a.d.Prefs.Load().ThumbCache)
			a.splitVP.SetMissingno(a.d.Prefs.Load().ShowMissingPlaceholder)
			a.splitVP.SetCrossfade(a.crossfadeDur())
			a.splitVP.SetPreanimLoop(a.d.Prefs.Load().LoopPreanim)
			a.splitVP.Update(&a.splitRoom.Scene, dt)
		}
		// Changes volumes only when the duck state flips, so it costs nothing between messages.
		a.rederiveMessageDuck()
		a.updateSlideshow(a.room.Phase())
	}
	a.d.Audio.Frame()
	a.settleAwaitedMusic() // delivery-time cross-tab un-duck: lift once the awaited track is what's playing
	a.d.Pump.Frame()
	// After the pump so the live stage wins recency over this tick's uploads and eviction cannot blank the picture.
	a.keepSceneAssetsWarm()
	a.d.Store.DrainDestroyQueue()

	// Close a hover preview on a screen switch: every close path is a per-screen draw tail, so it would have no owner
	// and keep claiming wheel and press. Must precede handlePreviewInput.
	a.noteScreenTransition()
	// Before any screen draws, so it wins the wheel and press over what is under the box.
	a.handlePreviewInput()

	// A blocking modal owns the pointer, so the screen behind draws click-proof. The notice box can show with no
	// session, where a click on the lobby's Reconnect would reach the server that just banned you.
	if a.confirmDisconnect || a.pendingCloseTab != nil || a.hidePrompt != "" || a.showQuitConfirm || a.makerExportPack > 0 || a.disconnectDlg.open || a.serverNoticeDlg.open || a.fontWarnDlg.open || a.evidDiscardConfirm || a.evidServerPickerOpen || demoBrowser.open {
		a.ctx.fencePointer()
	} else if a.hkSheetFencesPointer(winW, winH) {
		// The kit has no z-aware input, so with the cursor on the sheet the screens draw pointer-blind.
		// The courtroom pass applies the same rule via boxFencesPointer.
		a.ctx.fencePointer()
	}

	// Set or release before any screen draws: modalOn persists across frames, so an unreleased block freezes the UI.
	a.emojiPickerFence(a.ctx)
	a.reactPickerFence(a.ctx) // #2: same modal-fence discipline as the emoji picker
	a.rosterMenuFence(a.ctx)  // player-row … menu: same discipline (rostermenu.go)
	a.musicMenuFence(a.ctx)   // music panel ⋮ menu: same discipline (musicheader.go)
	a.updateModalFence(a.ctx) // What's New modal: modal fence on ANY screen (raw pointIn hit tests inside)

	// Claimed before the bar publishes its strip block, which covers these rects and blanks hovering() for them.
	// Outermost first: the missing-asset chip anchors to the theme-error chip's left edge.
	a.handleThemeErrChipInput(winW)
	a.handleAssetMissChipInput(winW)

	// Must stay above the screen dispatch: drawCourtroom's pointer blocks and the compact toolbox's suspend are scoped
	// by overlayFenceMark, so one published later cannot cover that pass. The paint half runs after the screens.
	a.menuBarFrame(winW, winH)

	// Only a courtroom pass decides this; clear on any other frame means the over-everything site paints.
	a.tabStripPaint = tabStripPaintLatch{}
	// Editors set this by painting and drawMenuBar reads it, so clear it after phase 1 and before the dispatch.
	a.editorBannerPainted = false
	// Pre-screen: editors draw last in their pass, after every other arrow-key consumer has seen the key.
	// It claims nothing unless an editor's draw reaches the screen.
	a.editorNudgeKeys(winW, winH)

	if a.gifExporting {
		// Owns the viewport and renders offscreen. Precedence: gif, replay, maker, screen.
		a.tickGifExport()
		a.drawGifProgress(winW, winH)
	} else if a.replaying && a.replayRoom != nil {
		// Drawn instead of any screen, so its controls own input and every render path is recover-wrapped.
		// On Stop the maker, still makerOpen, reappears intact.
		a.drawReplayOverlay(winW, winH)
	} else if a.makerOpen {
		// Drawn instead of any screen so it owns input.
		a.drawSceneMaker(winW, winH)
	} else {
		switch a.screen {
		case ScreenLobby:
			a.drawLobby(winW, winH)
		case ScreenCharSelect:
			a.drawCharSelect(winW, winH)
		case ScreenCourtroom:
			a.drawCourtroom(winW, winH)      // primary always full-screen; the pinned server floats on top
			a.drawFloatingPanels(winW, winH) // non-blocking Extras + Pair + the floating client window, on top of the live courtroom (input already restored)
			// Here because boxFencesPointer runs the courtroom pass pointer-blind while the cursor is over the panel.
			// It checks toolboxPinned and toolboxPieces itself, so theater and modals suppress it.
			// The editor block sets c.modalOn for the whole pass, and unblocking leaves it: release it for this draw.
			editingLayout := a.classicEdit || a.layoutEdit
			if editingLayout {
				savedModal := a.ctx.modalOn
				a.ctx.modalOn = false
				a.drawCompactToolboxOverEditor(winW, winH)
				a.ctx.modalOn = savedModal
			}
			if a.extrasSurfaceLive() { // torn-off tab panels: live court, no modal, not editing (edit-mode draws them inside drawCourtroom)
				a.drawTornTabs(winW, winH) // interactive content, fenced by boxFencesPointer (torntabs.go)
			}
			// After the torn-off tabs: a torn roster must not cover the panel used to rescue a broken layout.
			if editingLayout {
				// Unblocking does not clear modalOn, so hovering() would be false; restored for later draws.
				savedModal := a.ctx.modalOn
				a.ctx.modalOn = false
				a.drawToolboxPieces(winW, winH)
				a.ctx.modalOn = savedModal
			} else {
				a.drawToolboxPieces(winW, winH)
			}
			a.drawPalette(winW, winH)          // #39: command palette (Ctrl+Space), above panels, below pickers
			a.drawEmojiPicker(winW, winH)      // #M2 S1: emoji picker overlay (modal-fenced in drawCourtroom)
			a.drawReactPalette(winW, winH)     // #2: reaction palette overlay (modal-fenced)
			a.drawRosterMenu(winW, winH)       // player-row … menu (modal-fenced; docked AND torn-off players tabs)
			a.drawMusicMenu(winW, winH)        // music panel ⋮ menu (modal-fenced; same contract)
			a.drawGroupInviteToast(winW, winH) // group invite Accept/Decline banner (only when one is pending)
		case ScreenSettings:
			a.drawSettings(winW, winH)
		case ScreenAbout:
			a.drawAbout(winW, winH)
		case ScreenChangelog:
			a.drawChangelog(winW, winH)
		case ScreenServerHelp:
			a.drawServerHelp(winW, winH)
		case ScreenLogs:
			a.drawLogBrowser(winW, winH)
		case ScreenHelp:
			a.drawHelp(winW, winH)
		case ScreenThemeEditor:
			a.drawThemeEditor(winW, winH)
		}
	}
	// Over every screen, except while a layout editor owns the courtroom. Replays the courtroom pass's decision:
	// a re-test here sees an editor the pass already dismissed and paints twice. Exactly one site paints per frame.
	if a.tabStripPaintsOverEverything() {
		a.drawTabBar(winW, winH)
	}
	a.drawDownloadIndicator(winW)
	a.drawPingChip(8, winH-16) // #128 connection-quality chip (bottom-left; off by default)
	// The paint half of a.menuBarFrame: after the screens because the kit is single-pass, below diagnostics and modals.
	a.drawMenuBar(winW, winH)
	// Allocation is acceptable: opt-in diagnostics paths.
	if a.perfHUD {
		a.drawPerfHUD(winW, winH)
	}
	if a.d.Prefs.Load().DebugOverlay {
		a.drawDebugOverlay(winW, winH)
	}
	// A dropdown belongs above the F3 graph, so this paints separately from the strip, replaying phase 1's decision.
	a.drawMenuPanes()
	if a.showHotkeys {
		// Restore the pointer for the sheet's own pass, except under a confirm modal, which owns it.
		if !a.confirmDisconnect && a.pendingCloseTab == nil && a.hidePrompt == "" && !a.showQuitConfirm && a.makerExportPack == 0 && !a.disconnectDlg.open && !a.serverNoticeDlg.open && !a.fontWarnDlg.open && !a.evidDiscardConfirm && !a.evidServerPickerOpen && !demoBrowser.open {
			a.ctx.unfencePointer()
		}
		a.drawHotkeyCheatSheet(winW, winH)
	}
	// A found update shows a persistent chip and, the first time, the patch-notes modal.
	a.drawUpdateAvailable(winW, winH)
	// Restore the pointer for the modal's own buttons, then paint it over everything. One at a time.
	if a.confirmDisconnect || a.pendingCloseTab != nil || a.hidePrompt != "" || a.showQuitConfirm || a.makerExportPack > 0 || a.evidDiscardConfirm || a.evidServerPickerOpen {
		a.ctx.unfencePointer()
		switch {
		case a.makerExportPack > 0:
			a.drawExportPackConfirm(winW, winH)
		case a.showQuitConfirm:
			a.drawQuitConfirm(winW, winH)
		case a.confirmDisconnect:
			a.drawDisconnectConfirm(winW, winH)
		case a.pendingCloseTab != nil:
			// Before default: a lone pending close must not fall into the hide-sprite branch.
			a.drawCloseTabConfirm(winW, winH)
		case a.evidDiscardConfirm:
			a.drawEvidenceDiscardConfirm(winW, winH)
		case a.evidServerPickerOpen:
			a.drawServerEvidencePicker(winW, winH)
		default:
			a.drawHideSpriteConfirm(winW, winH)
		}
	}
	// Own block, drawn whenever open, so its pointer block is always released here and never left invisible.
	if a.disconnectDlg.open {
		a.ctx.unfencePointer()
		a.drawDisconnectDialog(winW, winH)
	}
	// Same rule. Below the disconnect dialog, though handleInvoluntaryDrop's branches make the two mutually exclusive.
	if a.serverNoticeDlg.open {
		a.ctx.unfencePointer()
		a.drawServerNotice(winW, winH)
	}
	// Same rule; last because least urgent, with any disconnect dialog in front.
	if a.fontWarnDlg.open {
		a.ctx.unfencePointer()
		a.drawFontWarnDialog(winW, winH)
	}
	// Also draws over the courtroom. Same pointer block as the modals above, painted last to top the evidence panel.
	if demoBrowser.open {
		a.ctx.unfencePointer()
		a.drawDemoBrowser(winW, winH)
	}
	a.ctx.FinishFrame()
	a.ctx.drawTooltip(winW, winH)
}

// themeLoadRoots orders the roots theme.Load searches, dropping the custom root for the stock "default":
// theme packs ship their own themes/default, which would otherwise shadow the built-in one.
func themeLoadRoots(name, customRoot, exeDir, configBase string) []string {
	if name == theme.DefaultThemeName {
		customRoot = ""
	}
	return themeSearchRoots(customRoot, exeDir, configBase)
}

// themeSearchRootCap is how many content roots exist: custom folder, executable directory, config base.
const themeSearchRootCap = 3

// themeSearchRoots is the one ordered list of directories that may hold themes/, shared by loader, picker and catalog.
// First hit per name wins downstream. configBase goes last: inserting it earlier flips which theme wins for anyone
// with the same name in two places. Do not align config.UserThemesDir to <configBase>/themes for portable installs:
// that moves their write root and strands their themes. The samePath skip is defensive; production never reaches it.
func themeSearchRoots(customRoot, exeDir, configBase string) []string {
	roots := make([]string, 0, themeSearchRootCap)
	if customRoot != "" {
		roots = append(roots, customRoot)
	}
	if exeDir != "" {
		roots = append(roots, exeDir)
	}
	if configBase != "" && !samePath(configBase, exeDir) {
		roots = append(roots, configBase)
	}
	return roots
}

// themeSearchRootsNow resolves the live inputs; it can block on a cold process, so goroutine only (hard rule 2).
// config.ExecutableDir, not os.Executable: only it applies EvalSymlinks, and samePath compares strings.
func themeSearchRootsNow(customRoot string) []string {
	exeDir := config.ExecutableDir()
	configBase, _ := config.ConfigBaseDir()
	return themeSearchRoots(customRoot, exeDir, configBase)
}

// samePath compares cleaned paths, case-insensitively on Windows; resolving links would be disk I/O.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
