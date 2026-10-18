package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

func (a *App) RefreshServers() {
	if a.lobbyFetching {
		return
	}
	a.lobbyFetching = true
	// Arm the on-open auto-refresh cap. Stamped here — not at the auto-trigger —
	// so EVERY fetch start (boot, the manual Refresh button, a master-URL change,
	// the on-open auto) pushes the cap out: entering the lobby right after a
	// manual refresh must not fire a second request. Below the dedup gate above
	// because a coalesced call started nothing and must not extend the cap.
	a.lastLobbyRefreshAt = a.now()
	a.lobbyStatus = "Fetching server list..."
	// Ping state deliberately survives a refresh: the on-open auto-refresh
	// funnels through here too, and the old wipe (pings/pingMode/pinging reset
	// at fetch START) snapped a ping-sorted lobby back to player order the
	// instant the user re-entered — before any new data even existed. a.pings
	// is keyed by URL (not row index), so it stays valid across a list swap
	// structurally; pollLobbyFetch prunes entries for servers that left the
	// list when a fresh list lands, which is the boundedness (rule §17.4) the
	// wipe used to provide. Ping mode itself only exits via its toggle button
	// (explicit user intent), and an in-flight sweep's URL-keyed results stay
	// correct for whichever list is showing, so neither needs abandoning.
	// An explicit --master flag (anything non-default in Deps) wins over the
	// Settings override, which wins over the built-in defaults.
	urls := a.masterListURLs()
	ch := a.lobbyResult
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lobbyRefreshTimeout)
		defer cancel()
		entries, err := network.FetchServerLists(ctx, urls)
		ch <- lobbyFetch{entries: entries, err: err}
	}()
}

// masterListURLs resolves the master server endpoints to poll. An explicit
// --master flag (a non-empty, non-default Deps.MasterURL) is used verbatim;
// otherwise the Settings override is used when set; otherwise the built-in
// defaults (aceattorneyonline + umineko) are polled and merged.
func (a *App) masterListURLs() []string {
	if a.d.MasterURL != "" && a.d.MasterURL != network.DefaultMasterServerURL {
		return []string{a.d.MasterURL}
	}
	if alt := a.d.Prefs.Load().MasterListURL; alt != "" {
		return []string{alt}
	}
	return network.DefaultMasterServerURLs
}

// lobbyAutoRefreshDue reports whether an on-open lobby auto-refresh should
// fire: the last master-list fetch started at least lobbyAutoRefreshMinInterval
// ago (a zero last — no fetch ever started — is due immediately). Pure so the
// cap decision is headlessly unit-testable, this package's established idiom
// (applyResumeDuck / resumeSeek).
func lobbyAutoRefreshDue(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= lobbyAutoRefreshMinInterval
}

func (a *App) pollLobbyFetch() {
	select {
	case res := <-a.lobbyResult:
		a.lobbyFetching = false
		if res.err != nil {
			// A failed fetch KEEPS the list on screen. Refreshes now fire with
			// zero user action (the on-open auto-refresh), so a transient master
			// hiccup must not erase a stale-but-fully-joinable list — including
			// the row the user is mid-click on. The status line reports the
			// failure; the next entry edge (or the manual button) retries. Pings
			// stay un-pruned too: pruning against a degraded list would throw
			// away every connect time over a hiccup. Only a boot-time failure
			// (nothing merged yet) rebuilds, so phone-book favorites still show.
			a.lobbyStatus = "Master list error: " + res.err.Error()
			if len(a.servers) == 0 {
				a.servers = a.mergedFavorites()
			}
			return
		}
		// The selection carries over by IDENTITY (WebSocketURL), never by raw
		// index: mergedFavorites re-sorts, so an index into the new list could
		// silently point Enter-to-join at a DIFFERENT server (the wrong-join
		// hazard the old blanket -1 reset guarded against). Capture the URL
		// before the swap, re-match after — so a background auto-refresh landing
		// mid-glance no longer deselects the row the user is about to join.
		prevSel := ""
		if a.selServer >= 0 && a.selServer < len(a.servers) {
			prevSel = a.servers[a.selServer].WebSocketURL()
		}
		a.lobbyStatus = fmt.Sprintf("%d servers", len(res.entries))
		a.masterEntries = res.entries
		a.servers = a.mergedFavorites()
		// Prune on the successful land: the map was sized by the last good list
		// and each land re-prunes to the current one (boundedness, rule §17.4).
		a.pruneStalePings()
		a.reselectServer(prevSel)
	default:
	}
}

// reselectServer restores the lobby selection onto the row whose WebSocketURL
// matches url in the freshly rebuilt a.servers, clearing it when the server
// vanished from the list (or url is empty). Re-running selectServerRow
// rebuilds the cached description lines/links against the NEW entry — the
// master list may have changed the description text the cache was built from.
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

// pruneStalePings drops connect-time entries for servers no longer in the
// merged list. This is the boundedness (rule §17.4) the old wipe-at-fetch-start
// in RefreshServers provided, moved to the result landing so a refresh no
// longer stomps the user's ping sort with zero user action. Off the hot path:
// runs once per fetch landing, never per frame.
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

// mergedFavorites merges the phone book into a fresh copy of the master
// list and sorts for display (favorites pinned top, legacy pinned bottom).
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

// --- shared frame ------------------------------------------------------------------

// Frame runs one UI frame: connection pump, screen logic, drawing.
func (a *App) Frame(dt time.Duration, winW, winH int32) {
	defer a.frameCrashLog() // last-resort: capture the stack of any unrecovered panic before it kills the app
	a.frameNow = time.Now()
	a.lastFrameDrawn = a.frameNow // SkipFrame's heartbeat: a real frame was drawn

	// Drain debug logs from render package (loop points diagnostics, etc.)
	if a.d.Audio != nil {
		for _, line := range render.PollDebugLogs() {
			a.pushDebug(line)
		}
	}

	// Damage bookkeeping (experimental loop): this frame absorbs everything
	// pending. Snapshot the store generation at frame START — uploads during
	// THIS frame (the pump below) bump it and earn at most one follow-up
	// frame, whose own snapshot then matches (self-limiting, never misses).
	a.uiDirty = false
	if a.d.Store != nil {
		a.drawnGen = a.d.Store.Generation()
	}
	// Chrome SHAPE (A5): resolve the active shape + its mask textures into the
	// Ctx once per frame so every shaped widget draw is a plain field read. On
	// the default "sharp" preset this is a cheap early exit and every draw site
	// stays byte-identical; a non-sharp preset (lazily) builds/heals the pinned
	// masks here. Runs before the screen draws below consume the resolved state.
	a.refreshShapeMasks()
	// ReduceMotion, latched ONCE for the whole frame (themeclock.go's
	// reduceMotionNow). themeFrame reads it at ~10 theme-chrome draw sites, most
	// of them on screens that never open an element pass, so the pass-level latch
	// alone would have left them reading a stale answer — or, before this, no
	// answer at all: animated chrome kept moving under the accessibility option
	// AND kept the pacer at the animation cadence.
	a.themeFXFrozen = a.reduceMotionNow()
	a.drawnCaretOn = a.ctx.caretOn
	// Animated-chrome census: the draw sites below mark the flag; promote it
	// at frame end so SkipFrame reads what THIS frame actually put on screen.
	a.frameAnimChrome = false
	// Demand-pump census (experimental loop): a draw that leaves a streaming grid
	// cell blank marks frameDemandPending so NextWakeDelay keeps re-rendering at
	// the demand cadence until the cell fills. Promoted at frame end beside the
	// anim-chrome flag.
	a.frameDemandPending = false
	defer func() {
		a.drawnAnimChrome = a.frameAnimChrome
		a.drawnDemandPending = a.frameDemandPending
	}()
	a.ctx.BeginDraw()           // the visible-field order rebuilds as this frame's TextFields register
	a.winW, a.winH = winW, winH // cached for deep draw helpers (preview clamp)
	a.frameDtMs = float32(dt.Seconds() * 1000)
	a.recordFrameDt(a.frameDtMs)
	// One-time: push the initial Discord presence once the app is up, so "Playing
	// AsyncAO — In the lobby" appears immediately on launch (not only after you join
	// a room). updatePresence no-ops when presence is unset or the toggle is off.
	if !a.presenceInit {
		a.presenceInit = true
		a.updatePresence()
	}
	// Stamp the sprite-preview config onto the context once per frame so every
	// HoverPreview call this frame reads a consistent, lock-free value.
	a.ctx.SetHoverPreview(a.d.Prefs.Load().SpritePreviewOn, a.d.Prefs.PreviewHoverDelay())
	// Hold-to-clear: stamp the bound key + threshold so the kit's text fields can
	// wipe on a long hold (the timer runs in BeginFrame).
	if on, keyName, ms := a.d.Prefs.Load().HoldClear(); on {
		a.ctx.SetHoldClear(true, sdl.GetKeyFromName(keyName), time.Duration(ms)*time.Millisecond)
	} else {
		a.ctx.SetHoldClear(false, sdl.K_UNKNOWN, 0)
	}
	// Ctrl+Backspace word-delete: stamp the pref so the kit's text fields can gate
	// the captured chord at consumption (the flag is set in HandleEvent when focused).
	a.ctx.SetWordDelete(a.d.Prefs.Load().WordDelete)
	// F3 toggles the perf HUD on any screen; consumed so plain-key
	// macro/character binds named "f3" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F3 {
		a.perfHUD = !a.perfHUD
		a.ctx.keyPressed = 0
	}
	// F1 toggles the hotkey cheat-sheet on any screen (consumed so a plain-key
	// macro/char bind named "f1" can't double-fire).
	if a.ctx.keyPressed == sdl.K_F1 {
		if a.showHotkeys {
			a.showHotkeys = false
			a.hkCache = nil
		} else {
			a.openHotkeyCheatSheet() // (re)builds the rows so it reflects current bindings
		}
		a.ctx.keyPressed = 0
	}
	// F8 toggles the interactive Debug panel on any screen — the superset of the F3
	// perf HUD, also reachable from Extras → Debug and Settings → Power user.
	// Consumed so a plain-key macro/char bind named "f8" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F8 {
		a.toggleDebugPanel()
		a.ctx.keyPressed = 0
	}
	// ESC leaves a full-screen menu back to where it opened — the instinctive
	// keystroke (the Back button still works). Two-step so it ALWAYS makes
	// progress out: a focused text field (a search / callword box, the common
	// case) releases on the first ESC, then the second ESC leaves — instead of
	// the field silently swallowing the key ("ESC did nothing in Settings").
	// Scoped to the menu screens so the courtroom's own ESC behaviour is
	// untouched; skipped while a key-bind capture or an ESC-owning overlay
	// (hotkey sheet / update modal) is up so it never steals their key.
	// Esc backs out of whatever's open — one layer per press. On a full-screen MENU
	// it closes a dropdown / field then leaves; on the courtroom or lobby it closes
	// the topmost popup or floating panel (Group Chat, Voice, Evidence, Pair, Mod,
	// CM, Call Mod, Timer, …) via closeTopOverlay. Skipped while a key-bind capture
	// owns Esc — and ONLY that; the layout editors are handled by closeTopOverlay,
	// never by a guard here (see the NOTE below, which is load-bearing).
	// NOTE: Esc arrives as ctx.escPressed (HandleEvent maps K_ESCAPE there, NOT to
	// keyPressed) — the old keyPressed==K_ESCAPE test never fired, which is why "Esc
	// did nothing". Gate on escPressed.
	// NO EDITOR GUARD HERE. Esc must ALWAYS have somewhere to go, and a bare guard is
	// the one shape that cannot promise that: it swallows the key on the assumption
	// that a draw-time handler will answer, which is false on any frame the editor's
	// draw does not run (a return-to-top modal used to end the themed pass before it —
	// a hard lock with no Done chip, no banner and every kit widget fenced dead).
	// closeTopOverlay owns the editors instead, at the priority their pixels sit at,
	// so the key is consumed by whoever is actually on top and the editors' own
	// draw-time escPressed checks become a second guarantee rather than the only one.
	if a.ctx.escPressed && !a.capturingKey() {
		// Close the topmost popup / panel first (works on any screen — e.g. the
		// reset-confirm or a dropdown over Settings); only if nothing was open do we
		// fall back to leaving a full-screen menu (drop a focused field, then exit).
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
				// The editor's own ladder, in the order the design specifies: drop a focused
				// field, then close the top panel, then clear the selection, then leave.
				// Leaving goes through closeThemeEditor so the return target is the screen the
				// editor was opened FROM — a.prevScreen belongs to the menu screens and would
				// send a courtroom-opened editor to whatever menu was last visited.
				//
				// THE PANEL IS A RUNG (design §Q7's Esc row, "close top panel → deselect →
				// leave"). It was missing while the Fonts panel was cosmetic and became a real
				// hole the moment it became an input mode that fences BOTH rails: Esc walked
				// straight past the thing on top of the screen and armed the discard chip
				// instead. It sits above the selection because while a panel is up the
				// selection cannot be changed at all (editorPanelUp fences the canvas and the
				// rails), so the topmost surface is the only one Esc can honestly be about.
				// (The keyboard map at design line 1891 writes "clear, then close panel"; its
				// "clear" is this ladder's FOCUS rung, which is the reading that leaves both
				// lines true.)
				//
				// PREVIEW IS THE TOP RUNG (field batch 9), above the focused field and
				// above the panels. While the demo plays there IS no focused field and
				// there ARE no panels — enterEditorPreview closes them — so a rung placed
				// any lower would still be reached today; it sits here because "Esc leaves
				// the thing that is covering the screen" must not depend on that staying
				// true. It CONSUMES the press: an Esc that fell through would arm the
				// discard chip on a document the user is not even looking at.
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
					// The SAME door the Back button uses, so Esc cannot become the one way
					// out that discards unsaved work without asking.
					a.requestEditorExit()
				}
				handled = true
			case ScreenLobby:
				// Lobby with nothing open: Esc offers to quit — the escape hatch when
				// you're fullscreen and can't reach the window's close button.
				a.requestQuit()
				handled = true
			case ScreenCourtroom:
				// GH #51: Esc in the bare courtroom TOGGLES THE EXTRAS BOX. It used to
				// call requestDisconnect — one keypress from "leave the server", behind
				// nothing but a confirm dialog, on the key people press reflexively to
				// mean "get me out of this menu". Leaving is still one click away and now
				// lives where every other session action already does: the Extras box's
				// own Disconnect entry (extrasWidgets, floatbox.go), which goes through
				// the same confirm. So the action did not disappear, it stopped being a
				// keystroke.
				//
				// A focused field (the IC input) still eats the FIRST Esc, exactly like
				// every menu screen — and under the Discord focus model (chatfocus.go)
				// that rung is what lets you step out of the chat box at all.
				if a.ctx.focusID != "" {
					a.ctx.focusID = ""
				} else {
					a.toggleExtrasBox()
				}
				handled = true
			case ScreenCharSelect:
				// Same fullscreen keyboard exit on char-select. Re-picking from the courtroom
				// backs out to it (matches the top-right Back, non-destructive); a fresh
				// char-select's only exit is leaving the server, through the confirm.
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
	// F11 toggles fullscreen on any screen — the keyboard escape when a too-big
	// window has dragged the Settings controls off the edge of the monitor.
	if a.ctx.fullscreenReq {
		a.toggleFullscreen()
	}
	// Log-selection press edge, computed once so both logs (which may both be
	// on screen) read the same value — whichever draws first can't steal it.
	a.logSelPressed = a.ctx.mouseDown && !a.logSelPrevDown
	a.logSelPrevDown = a.ctx.mouseDown
	// Ctrl+Z / Ctrl+Y with a text field focused become the FIELD's undo/redo
	// (fieldhistory.go): consumed here, pre-screen, so no later chord consumer
	// — the courtroom dispatcher, quick-connect, a user hotkey bound to z/y —
	// can double-fire while typing. Ctrl+Shift+Z is redo too. The armed layout
	// editors keep their own Ctrl+Z (editorUndoChord); they never run with a
	// focused field, but the gate makes that explicit.
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
	// The THEME EDITOR (a SCREEN, not an overlay) claims its own Ctrl+Z /
	// Ctrl+Y here, and the site is the fix rather than a tidy-up.
	//
	// editorUndoChord's only other caller is handleHotkeys, which runs from the three
	// COURTROOM draw passes — so the chord reached the editor only while a live room
	// was painting behind its canvas. The editor's headline entry point is the
	// Settings row, which is reachable with NO SERVER ATTACHED (design §3): there
	// drawEditorGeometryCanvas draws instead, no courtroom pass runs, and the chord
	// sat in c.hotkey untouched for the whole frame. Present but unreachable, on the
	// one screen whose entire contract is that every edit is one Ctrl+Z away.
	//
	// AFTER the focused-field arm above, deliberately: a focused inspector row (a hex
	// value, an id) keeps the FIELD's own undo, which is the house rule every other
	// editor already follows. BEFORE quick-connect and every other pre-screen chord
	// consumer, because editorUndoChord's promise is that a key an armed editor owns
	// can never also fire whatever else it happens to be bound to.
	//
	// Gated on themeEditorOpen() rather than called bare, so the two LEGACY overlay
	// editors keep claiming their chord exactly where they always have — inside their
	// own courtroom pass, via handleHotkeys — and nothing about their dispatch moves.
	if a.themeEditorOpen() {
		a.editorUndoChord()
	}
	a.pumpTabRestore()  // restore-on-launch: one reconnect/frame, then idle
	a.fireAutoConnect() // one-shot: auto-connect to the last server on launch (opt-in)
	// Quick-connect key: the courtroom hotkey handler is sess-gated and never runs
	// in the lobby, so dispatch this one here, only while offline.
	// Gated on the notice box: it is the one blocking modal that is routinely up on
	// the LOBBY with no session, which is precisely when this dispatch is live. Its
	// whole reason for being there is that the server just removed us, so letting a
	// keypress redial past it would be the client arguing with the ban. (The sibling
	// confirms are NOT gated here; that gap is pre-existing and untouched.)
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
		// controlPinnedClient → activateTab → parkActive is a PRIMARY-session switch,
		// the one park surface handleTabBar's guard (tabs.go) doesn't cover. It must be
		// inert while the involuntary-disconnect dialog owns the frame, for the same
		// reason the tab strip is: parkActive would copy the open dialog into the frozen
		// tab and reset the live copy, resurfacing a stale modal cross-tab. The click
		// lands on the pinned client because it draws AFTER drawCourtroom's fence lifts
		// (screens.go), so this is the only place to stop it — and we consume the flag
		// unconditionally (never leave it armed to fire once the dialog closes), gating
		// only the action. Resolve the dialog (Reconnect / Back to lobby / Esc) first,
		// THEN the swap works.
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
	if a.ctx.TakeWantsCJK() { // a CJK letter was drawn → kick the (independent) CJK-font read
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
	// The Discord focus model (chatfocus.go): typing with nothing focused claims the
	// IC input. AFTER every bind-capture poll above — an armed capture owns the next
	// keypress and must not have it stolen — and BEFORE the screen draws, which is
	// what makes the FIRST character land in the box instead of being eaten.
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
		// M16 GIF export drives the viewport itself in the draw-phase tick — skip
		// the normal scene driving so they don't fight over the shared viewport.
	case a.replaying && a.replayRoom != nil:
		// M16 replay: drive the recorded scene instead of the live one (feed the
		// next event when the room goes idle so the courtroom's own pacing times
		// it; the viewport reads the replay scene via renderScene too). Wrapped
		// so a panic in a bad/edge recording stops the replay, not the app.
		a.driveReplay(dt)
	case a.makerOpen:
		// M16 scene maker: drive the preview-pane room (NOT the live room) so the
		// shared viewport's anim state tracks the previewed line, not whatever the
		// live courtroom is doing behind the overlay.
		a.driveMakerPreview(dt)
	case a.editorPreviewOn():
		// The theme editor's canned demo (themeeditorpreview.go), on the same terms
		// as the maker's pane above and for the same reason: the shared viewport must
		// track the room that is on screen. The live room is deliberately NOT advanced
		// — it is not being drawn, and a preview that let a live message play out
		// behind it would drop the frames that message needed.
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
		a.d.Viewport.SetSpriteLoadMode(a.vpSpriteLoadMode())                                               // cold-load flash mitigation (default hold-previous = webAO-style bridge; Blank = the original byte-identical gap)
		a.d.Viewport.SetClipSprites(a.d.Prefs.Load().ClipSpritesToStage)                                   // viewport sprite mask (default ON): offsets can't spill past the stage
		a.d.Viewport.SetHoldMaxAge(time.Duration(a.d.Prefs.Load().HoldPrevMaxAgeMsVal) * time.Millisecond) // hold-previous stand-in cap (0 = forever)
		a.d.Viewport.SetHoldDebugTint(a.d.Prefs.Load().HoldDebugTint)                                      // amber-tint stand-ins (diagnostics)
		a.d.Viewport.SetThumbSprites(a.d.Prefs.Load().ThumbCache)                                          // opt-in low-q thumbnail stand-ins on the cold miss path
		a.d.Viewport.SetMissingno(a.d.Prefs.Load().ShowMissingPlaceholder)                                 // AO2 placeholder for conclusively-missing sprites (default ON)
		a.d.Viewport.SetCrossfade(a.crossfadeDur())                                                        // speaker-swap blend (0 = off; zeroed under Reduce motion)
		a.d.Viewport.SetPreanimLoop(a.d.Prefs.Load().LoopPreanim)                                          // opt-in: keep preanims wrapping (default OFF; AO2 plays them once)
		a.d.Viewport.SetPostFX(a.postFX())                                                                 // #10 retro overlays
		a.d.Viewport.SetWeather(render.Weather(a.d.Prefs.WeatherType()), a.d.Prefs.WeatherIntensity())     // #124 ambient weather
		a.d.Viewport.Update(&a.room.Scene, dt)
		a.logSpeakerAnimDiag() // #110: log the speaker sprite's frame/delay stats once per base change
		// This frame IS the draw the doorbell exists to force, and the viewport is
		// now bound to the new overlay, so the draw-site censuses take over from
		// here — sync the latch instead of ringing (noteOverlayStart).
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
		// Music ducking: dip music while a message is on stage (shout/preanim/
		// talking), restore at idle/linger. Transition-driven — SetVolumes is
		// touched only when the duck state flips, and the prefs read is
		// short-circuited so it costs nothing between messages.
		a.rederiveMessageDuck()
		a.updateSlideshow(a.room.Phase())
	}
	a.d.Audio.Frame()
	a.settleAwaitedMusic() // delivery-time cross-tab un-duck: lift once the awaited track is what's playing
	a.d.Pump.Frame()
	// Touch AFTER the pump so the live stage wins recency over this tick's
	// upload burst — an eviction of the picture on screen was the "window
	// randomly redraws" blink. Map probes only; see keepSceneAssetsWarm.
	a.keepSceneAssetsWarm()
	a.d.Store.DrainDestroyQueue()

	// Screen switch: tear down any hover-preview riding across it. Every close
	// path is a per-screen draw tail, so a preview still open when the screen
	// changes under it (char pick → PV lands, a pointer-fenced confirm-modal
	// click, a hotkey / server-driven jump) has NO surviving owner: invisible on
	// the new screen, its stale box rect still claimed wheel/press below, and
	// its stale trigger id pinned close-on-leave open forever. Must run before
	// handlePreviewInput so the ghost box never eats the new screen's input.
	a.noteScreenTransition()
	// Sprite-preview wheel zoom + drag, claimed before any screen draws so it
	// wins the wheel/press over the grid scroll and icon clicks under the box.
	a.handlePreviewInput()

	// While a confirm modal (Disconnect / hide-sprite) OR the involuntary-disconnect
	// dialog is up, the modal OWNS the pointer: fence it so the screen + overlays
	// behind draw click-proof (no fat-finger underneath — for the disconnect dialog,
	// no stray IC send can reach the dead socket in the frozen courtroom). Restored
	// just before the modal draws, below.
	//
	// serverNoticeDlg joins them: it is blocking, and unlike the rest it can be up
	// with NO session at all (a server that refuses during the handshake), so the
	// screen underneath is the lobby — where an unfenced click lands on Reconnect and
	// redials the server that just banned you.
	//
	// fontWarnDlg was MISSING from this list and was therefore click-through: it
	// drew its own modal at the frame tail and unfenced for its buttons, but nothing
	// ever fenced the screen for it, so a click that looked like it hit the dialog
	// also hit whatever was behind. Pre-existing, same family, one term.
	if a.confirmDisconnect || a.pendingCloseTab != nil || a.hidePrompt != "" || a.showQuitConfirm || a.makerExportPack > 0 || a.disconnectDlg.open || a.serverNoticeDlg.open || a.fontWarnDlg.open || a.evidDiscardConfirm || a.evidServerPickerOpen || demoBrowser.open {
		a.ctx.fencePointer()
	} else if a.hkSheetFencesPointer(winW, winH) {
		// The hotkey sheet floats over EVERY screen and draws at the frame tail:
		// while the cursor sits on it (or its drag/resize is in flight), the
		// screens draw pointer-blind and the sheet unfences for its own pass
		// below — without this, scrolling the sheet also scrolled the lobby /
		// settings list underneath it (the kit has no z-aware input; the fence
		// IS the layering). The courtroom pass already applies the same rule
		// via boxFencesPointer, so this only changes the other screens.
		a.ctx.fencePointer()
	}

	// #M2 S1: set/RELEASE the emoji-picker modal fence before any screen draws. modalOn
	// persists across frames, so an un-released fence freezes the whole UI (the reported
	// open-then-close bug).
	a.emojiPickerFence(a.ctx)
	a.reactPickerFence(a.ctx) // #2: same modal-fence discipline as the emoji picker
	a.rosterMenuFence(a.ctx)  // player-row … menu: same discipline (rostermenu.go)
	a.musicMenuFence(a.ctx)   // music panel ⋮ menu: same discipline (musicheader.go)
	a.updateModalFence(a.ctx) // What's New modal: modal fence on ANY screen (raw pointIn hit tests inside)

	// The band's two right-anchored chips: claimed just BEFORE the bar publishes its
	// strip fence, because that fence covers their rects (they share the band) and
	// would blank hovering() for them. Same pre-screen discipline as the update chip.
	//
	// OUTERMOST FIRST. The theme-sidecar refusal chip is right-anchored at the window
	// edge and the missing-asset chip is anchored to ITS left edge, so claiming them
	// in this order is claiming them right-to-left, which is the order their rects
	// resolve in (themeerrchip.go).
	a.handleThemeErrChipInput(winW)
	a.handleAssetMissChipInput(winW)

	// App-wide menu bar (#14, menubar.go), phase 1: publish its overlay fences and
	// resolve its own input. THIS CALL MUST STAY ABOVE THE SCREEN DISPATCH BELOW.
	// drawCourtroom takes an overlayFenceMark at the top of its pass and releases
	// back to it, and the compact toolbox's owner-suspend is MARK-SCOPED — so a
	// fence published after (or inside) a pass cannot occlude that pass, and an open
	// menu pane published late would be suspended by the toolbox along with its own
	// strip: issue #26's click-through, one level up. The paint half runs after the
	// screens (single-pass kit: last painted is on top).
	a.menuBarFrame(winW, winH)

	// Reset the server-tab strip's paint-site latch (chrometop.go). Only a courtroom
	// pass takes the decision; every other frame — lobby, char select, and the three
	// full-window modes below — leaves it clear, which reads as "the over-everything
	// site owns the paint". Same reset discipline as the toolbox latch, one level up,
	// because this one is decided inside a pass that does not always run.
	a.tabStripPaint = tabStripPaintLatch{}
	// Same discipline for "a layout editor's banner reached the screen this frame"
	// (menubar.go). It is published by the editors' own banner paints, below, and read
	// once by drawMenuBar to settle who owns the top band — so it must be cleared AFTER
	// phase 1 and BEFORE the dispatch, or the bar would replay a stale answer.
	a.editorBannerPainted = false
	// Arrow-key nudging for an armed layout editor (layoutnudge.go). PRE-SCREEN, and
	// for the same reason the editors' Ctrl+Z lives in editorUndoChord rather than in
	// their draw bodies: both editors draw LAST in their pass, by which point a focused
	// text field's caret, the IC/OOC recall rings, the pair-ghost offset nudge and the
	// emote-preview cycler have all already looked at this frame's arrow key. It claims
	// nothing at all unless an editor's own draw actually reaches the screen.
	a.editorNudgeKeys(winW, winH)

	if a.gifExporting {
		// M16 GIF export: owns the viewport (renders the scene offscreen) — tick a
		// batch of frames on the render thread, behind a progress overlay, instead
		// of any screen. Highest precedence: gif > replay > maker > screen.
		a.tickGifExport()
		a.drawGifProgress(winW, winH)
	} else if a.replaying && a.replayRoom != nil {
		// M16: a replay takes over the whole window via the guarded overlay,
		// drawn INSTEAD of any screen — so its controls own the input AND every
		// replay render path is the recover-wrapped one (a themed courtroom or a
		// missing theme can't crash a replay this way). Precedence is
		// replaying > makerOpen > screen: a Preview launched from the maker shows
		// the replay; on ■ Stop the maker (still makerOpen) reappears intact.
		a.drawReplayOverlay(winW, winH)
	} else if a.makerOpen {
		// M16 scene maker: a full-window editor overlay (scenemaker.go), drawn
		// instead of any screen so it owns input.
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
			// The pinned per-piece toolbox panel draws HERE (not inside drawCourtroom):
			// its checkboxes/scrollbar/Close need real input, but boxFencesPointer runs
			// the whole courtroom pass pointer-blind while the cursor is over the panel,
			// so drawing it inside that pass left it dead (the recon BLOCKER). Post-court
			// the fence is already lifted (drawCourtroom's deferred unfencePointer ran on
			// return). Drawn AFTER drawFloatingPanels = above the other floating panels:
			// it's a small utility popover the user just opened, so it should sit on top.
			// It draws in BOTH layouts (this site is layout-agnostic) and self-gates on
			// toolboxPinned && toolboxPieces (+ extrasSurfaceLive, matching its fence), so
			// theater — which clears both flags in setTheater — and blocking modals both
			// suppress it without a special case here.
			//
			// A1 Phase 1: the compact toolbox (grip + chips) and the pieces panel now
			// survive INTO edit mode too — but the classic/themed editor fence sets
			// c.modalOn for the whole courtroom pass (classicEditFence/layoutEditFence),
			// and modalOn (unlike ptrFenced) is NOT cleared by the deferred unfence, so
			// hovering() would read false here and the toolbox/panel would draw click-dead.
			// The editor itself uses raw pointIn (it doesn't need modalOn), so release
			// modalOn just for this toolbox draw, then restore it so downstream
			// fence assumptions (FinishFrame, palette, tooltips) are undisturbed. Draw
			// the compact grip/chips HERE while editing (its in-pass draw at
			// screens.go/theme_layout.go is gated off during edit for the same
			// fence reason).
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
			// The pieces panel draws AFTER the torn-off tabs, and that ordering is the
			// fix for a reported fault rather than a preference. The Players tab can be
			// TORN OFF into a floating panel (torntabs.go), and it used to draw after
			// this one — so a torn roster sat squarely on top of the panel, which is
			// the surface you reach for when the layout has gone wrong. Being unable
			// to see the rescue control because of the thing you were trying to rescue
			// yourself from is the worst possible ordering, and the comment above
			// already claimed this should "sit on top"; it simply was not true against
			// torn tabs.
			if editingLayout {
				// modalOn (unlike ptrFenced) is NOT cleared by the deferred unfence, so
				// hovering() would read false and the panel would draw click-dead while
				// an editor fence is up. Released just for this draw, then restored so
				// FinishFrame, the palette and the tooltips see what they expect.
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
	// The tab strip floats over every screen (input was consumed at the
	// top of the frame; this is just paint) — EXCEPT while a layout editor owns the
	// courtroom: there it paints inside the courtroom pass, under the editor overlay,
	// so its chips can't cover the editor's banner and controls (the "layering mess"
	// playtest shot: a server chip parked top-right sat on the Snap/Done buttons).
	// It REPLAYS the per-frame latch the courtroom pass took (chrometop.go) rather
	// than re-deriving the predicate here: this site runs after the whole screen
	// dispatch, so a re-test saw an editor that the pass had already dismissed (Done,
	// Esc, a lost theme) and painted the strip a SECOND time over the banner that was
	// still up. The two sites are exact complements of one latched bool, so exactly
	// one of them paints per frame.
	if a.tabStripPaintsOverEverything() {
		a.drawTabBar(winW, winH)
	}
	// Download progress chip floats under the strip while a grab runs.
	a.drawDownloadIndicator(winW)
	a.drawPingChip(8, winH-16) // #128 connection-quality chip (bottom-left; off by default)
	// Menu bar (#14), phase 2 — the PAINT half of a.menuBarFrame above. It draws
	// after the screens because the kit is single-pass, and before the diagnostics
	// overlays / floating sheet / blocking modals below, which legitimately sit on
	// top of client chrome. It paints over EMPTY pixels: the strip owns the top
	// a.menuBarHeight() px of the window, the server-tab strip docks directly under
	// it, and every screen starts its content below the pair (chrometop.go).
	a.drawMenuBar(winW, winH)
	// Perf HUD (F3) and the debug overlay paint over every screen
	// (allocs acceptable: opt-in diagnostics paths, never on by default).
	if a.perfHUD {
		a.drawPerfHUD(winW, winH)
	}
	if a.d.Prefs.Load().DebugOverlay {
		a.drawDebugOverlay(winW, winH)
	}
	// The menu bar's OPEN pane paints here, above the two diagnostics overlays: a
	// dropdown is a popup and belongs on the popup layer with the kit's deferred
	// lists (FinishFrame, below), while the F3 graph is client-level diagnostics.
	// Painted with the strip it drew FIRST, and the performance overlay covered the
	// menu the user had just opened. Nothing is re-decided here — drawMenuPanes
	// replays the same phase-1 latch drawMenuBar does.
	a.drawMenuPanes()
	if a.showHotkeys {
		// Restore the pointer for the sheet's own pass — it was fenced above
		// while hovered/dragged so the screens beneath drew pointer-blind.
		// Skipped while a confirm modal is up: that fence belongs to the modal
		// (drawn after), and the sheet must stay inert under it.
		if !a.confirmDisconnect && a.pendingCloseTab == nil && a.hidePrompt == "" && !a.showQuitConfirm && a.makerExportPack == 0 && !a.disconnectDlg.open && !a.serverNoticeDlg.open && !a.fontWarnDlg.open && !a.evidDiscardConfirm && !a.evidServerPickerOpen && !demoBrowser.open {
			a.ctx.unfencePointer()
		}
		a.drawHotkeyCheatSheet(winW, winH)
	}
	// M13: a found update shows a persistent chip (reopen) and, the first time,
	// the What's New patch-notes modal. Both no-op when no update was found.
	a.drawUpdateAvailable(winW, winH)
	// Confirm modals: restore the pointer (fenced above) for the modal's own
	// buttons, then paint it over everything. One at a time.
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
			// Explicit case BEFORE default: a lone pending close must not fall into
			// the hide-sprite branch below (it would draw the wrong modal).
			a.drawCloseTabConfirm(winW, winH)
		case a.evidDiscardConfirm:
			a.drawEvidenceDiscardConfirm(winW, winH)
		case a.evidServerPickerOpen:
			a.drawServerEvidencePicker(winW, winH)
		default:
			a.drawHideSpriteConfirm(winW, winH)
		}
	}
	// The involuntary-disconnect dialog (now reached only for a background/parked
	// tab's death, surfaced on reactivation) draws LAST of the blocking modals, over
	// the already-painted frozen courtroom (its scene + logs stayed drawn under it).
	// Own block, not folded into the switch above: drawing it unconditionally-when-open
	// guarantees the fence set for it (7068) is always released here — never a
	// fenced-but-invisible dialog (the emoji-picker freeze class). Restore the pointer
	// for its own buttons first.
	if a.disconnectDlg.open {
		a.ctx.unfencePointer()
		a.drawDisconnectDialog(winW, winH)
	}
	// The server's-own-words box (servernotice.go), same rule: drawn
	// unconditionally-while-open so its fence is always released here. BELOW the
	// disconnect dialog because that dialog is about the session and owns the front
	// when both are somehow up — though they cannot be: a removal notice and the
	// dialog are mutually exclusive branches of handleInvoluntaryDrop, and
	// openDisconnectDialog dismisses a live INFO notice for exactly this reason.
	if a.serverNoticeDlg.open {
		a.ctx.unfencePointer()
		a.drawServerNotice(winW, winH)
	}
	// The missing-theme-fonts notice, same family and the same rule: drawn
	// unconditionally-while-open so its fence is always released here. Last of the
	// modals because it is the least urgent — a disconnect dialog up at the same
	// time is about the session, and must be the one in front.
	if a.fontWarnDlg.open {
		a.ctx.unfencePointer()
		a.drawFontWarnDialog(winW, winH)
	}
	// The in-app file browser (the evidence editor's Browse button and the Studio
	// flows) also draws over the courtroom, not just over Settings. It shares the
	// pointer fence every modal above uses: fenced while the screen draws, released
	// here for its own buttons, then painted last so it tops the evidence panel it
	// was opened from.
	if demoBrowser.open {
		a.ctx.unfencePointer()
		a.drawDemoBrowser(winW, winH)
	}
	// Deferred kit overlays (open dropdown lists) stack above everything.
	a.ctx.FinishFrame()
	// Hover hints paint last so they sit above every cell/overlay.
	a.ctx.drawTooltip(winW, winH)
}

// themeLoadRoots orders the content roots theme.Load searches for theme `name`.
// A custom themes folder (customRoot) is searched FIRST for named themes — but is
// dropped for the stock "default": theme packs ship their own themes/default, and
// since the custom root wins, that custom default would otherwise SHADOW the
// built-in one, making the stock default unreachable once you've set a folder
// (#87). The app directory is always the built-in fallback. Pure, for testing.
//
// Everything else about the ordering lives in themeSearchRoots, which the theme
// PICKER and the catalog build from too: what the loader resolves and what the
// user can SEE in the dropdown must never disagree about which directories exist.
func themeLoadRoots(name, customRoot, exeDir, configBase string) []string {
	if name == theme.DefaultThemeName {
		// #87, above: the stock default is the one theme a custom root may not answer.
		customRoot = ""
	}
	return themeSearchRoots(customRoot, exeDir, configBase)
}

// themeSearchRootCap is how many content roots this client knows: the user's
// custom themes folder, the executable's directory, and the config base. It is
// the make() bound for every list built from them (hard rule 4 — a named cap,
// not a bare 3).
const themeSearchRootCap = 3

// themeSearchRoots is THE ordered list of directories that may hold a themes/
// folder. The loader (themeLoadRoots), the picker (scanThemes) and the browser
// catalog (scanThemeCatalog) all build from this one function, so none of the
// three can drift into searching a root the others don't — the bug where a
// theme resolved by name, appeared in the catalog, sat in the folder "Open
// folder" opens, and was still absent from the dropdown.
//
// First hit per theme NAME wins downstream, so this order is the precedence.
//
// configBase goes LAST on purpose. It is the portable-aware config root
// (config.UserThemesDir's parent), where a locked-down install (Program Files, an
// unwritable exe dir) puts the themes the client itself writes — imports,
// editable copies — so it must be searched; but inserting it any earlier would
// silently flip which theme wins for anyone holding the same name in two places,
// which is a live regression traded for nothing. Appended, never inserted.
//
// The samePath skip is DEFENSIVE — it is not a state production reaches.
// config.ConfigBaseDir never returns the executable's directory: it resolves to
// <exeDir>/config when portable and <osConfigDir>/AsyncAO otherwise
// (config/location.go, resolveConfigBase). In a portable install the WRITE root
// is <exeDir>/themes, which the exe root above already covers, and this third
// root then probes <exeDir>/config/themes — a sibling nothing writes to, normally
// absent, and harmless to miss. Do NOT "align" config.UserThemesDir to
// <configBase>/themes for portable to make the skip meaningful: that would move
// every portable user's write root into config/ and strand the themes they have.
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

// themeSearchRootsNow resolves the live inputs behind themeSearchRoots: the
// executable's directory and the config base. BLOCKING on a cold process
// (config.ExecutableDir's first resolve, and whichever config memo is still
// empty) — goroutine only, never a draw path (hard rule 2). customRoot is the
// already-normalized custom folder, "" when there is none.
//
// config.ExecutableDir, never a local os.Executable + filepath.Dir: that raw
// spelling skips EvalSymlinks, so on a symlinked or junctioned install it names
// the same directory with a DIFFERENT string than config.UserThemesDir and the
// catalog scan (themecatalog.go) do — and every "is this the same place?" check
// downstream (samePath, the write-root backstop, the origin badge) is a string
// comparison by design. One memoized answer, shared by every caller.
func themeSearchRootsNow(customRoot string) []string {
	exeDir := config.ExecutableDir()
	configBase, _ := config.ConfigBaseDir()
	return themeSearchRoots(customRoot, exeDir, configBase)
}

// samePath compares two directory paths for "the same place" as far as this
// process is concerned: cleaned, and case-insensitively on Windows, where
// C:\App and c:\app are one directory. It is a string comparison on purpose —
// resolving links would be disk I/O on a path that must stay cheap.
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

// applyThemeAsync loads the selected theme's visible pieces off-thread — the
// chatbox skin (chatbox.webp/png in the theme dir, AO2 convention) and the
// message/showname font colors — and publishes them to the render thread via
// themeRes. Settings re-triggers it on every theme change.
//
// It returns the themeGen it allocated for this load, so a caller that means to
// act on THIS apply's landing (the #35 window snap) can name it. Every other
// caller ignores it.
