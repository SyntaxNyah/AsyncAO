package ui

// Multi-server tabs. The active session lives in App.sessionState; a parked tab holds its sessionState by value, so
// switching is struct moves with no deep copies.
// Background tabs stay connected and drain into their own reducer and logs, but keep no room: nothing animates off
// screen, and activation rebuilds it through buildRoom from the reducer.
// The shared log caches key on the per-session seq plus a per-view epoch (logViewEpoch, splitPinEpoch), because seqs
// start at 0 in every tab and would collide across a switch.
// Asset caches need nothing: their keys are full URLs.

import (
	"fmt"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// tabPumpBudget bounds packets drained per background tab per frame, so a busy room can't starve the active tab.
	tabPumpBudget = 64
	// tabChipMaxName clips server names on tab chips.
	tabChipMaxName = 14
	// tabBarH is the server-tab strip's height, part of the app-chrome band every screen offsets by.
	tabBarH = 22
	// addTabChipW is the width of the "+" new-session chip at the end of the strip.
	addTabChipW = 26
	// tabDragThreshold is the cursor travel (logical px) past which a press on a chip becomes a reorder drag.
	tabDragThreshold = 6
	// tabTearOffY is the Y (logical px) below which a chip drag stops reordering and pops the tab out on release.
	// Sized for the docked strip; with the menu bar hidden the only effect is a slightly longer pull.
	tabTearOffY = menuBarH + tabBarH + 34
	// themeTabBarKey is the courtroom_design.ini rect that parks the server-tab strip inside a theme's design canvas.
	// Its X/Y are canvas-relative client px, not scaled design px (see tabStripCacheRect). No shipped theme declares
	// it, so seedTabBarDesignRect synthesizes it.
	themeTabBarKey = "asyncao_tabbar"
	// tabBarDesignSeedW is the synthesized rect's width in design px; nothing paints at it, the strip sizes from its
	// chips.
	tabBarDesignSeedW = 240
	// tabChipTextPadX is a chip's padding around its label, including the trailing "✕" on background chips; tabChipGapX
	// is the gap between chips.
	tabChipTextPadX = 28
	tabChipGapX     = 4
)

// courtTab is one parked server session; while a tab is active its state field is zero.
type courtTab struct {
	state  sessionState
	unread int  // IC+OOC lines landed while backgrounded
	dead   bool // connection ended while backgrounded
	// deadReason is the close reason, held until the tab is activated: a background drop must not open a dialog over
	// the tab in view.
	deadReason string
	inCourt    bool // a room existed when parked (activation re-enters it)
	// chipLabel memoizes the strip label so the always-on tab bar allocates nothing while chipKey is stable.
	chipLabel string
	chipKey   tabChipKey
}

// tabChipKey is everything a chip's text depends on.
type tabChipKey struct {
	name   string
	active bool
	dead   bool
	unread int
}

// notebookLoad routes an off-thread notebook read to the right tab.
type notebookLoad struct {
	key string
	nb  *config.Notebook
}

func (a *App) tabName(i int) string {
	if i == a.activeTab {
		return a.serverName
	}
	return a.tabs[i].state.serverName
}

// tabKey returns a tab's serverKey, so per-tab colour follows the server; "" for an unkeyed tab.
func (a *App) tabKey(i int) string {
	if i == a.activeTab {
		return a.serverKey
	}
	if i >= 0 && i < len(a.tabs) {
		return a.tabs[i].state.serverKey
	}
	return ""
}

// tabColorsCap bounds the per-tab colour map (hard rule 4).
const tabColorsCap = 64

// tabPalette is the cycle of tab-chip tints; index 0 is no tint.
var tabPalette = []sdl.Color{
	{},                               // 0 = none
	{R: 200, G: 70, B: 70, A: 255},   // red
	{R: 210, G: 140, B: 50, A: 255},  // amber
	{R: 90, G: 170, B: 90, A: 255},   // green
	{R: 70, G: 140, B: 210, A: 255},  // blue
	{R: 160, G: 110, B: 210, A: 255}, // violet
	{R: 200, G: 110, B: 170, A: 255}, // pink
}

// cycleTabColor advances a tab's chip colour, wrapping to none; the map only holds coloured tabs.
func (a *App) cycleTabColor(i int) {
	key := a.tabKey(i)
	if key == "" {
		return
	}
	next := (a.tabColors[key] + 1) % len(tabPalette)
	if next == 0 {
		delete(a.tabColors, key)
		return
	}
	if _, ok := a.tabColors[key]; !ok && len(a.tabColors) >= tabColorsCap {
		return // bounded
	}
	if a.tabColors == nil {
		a.tabColors = make(map[string]int)
	}
	a.tabColors[key] = next
}

// tabChipTint returns a tab chip's tint and whether one is set.
func (a *App) tabChipTint(i int) (sdl.Color, bool) {
	if len(a.tabColors) == 0 {
		return sdl.Color{}, false
	}
	idx := a.tabColors[a.tabKey(i)]
	if idx <= 0 || idx >= len(tabPalette) {
		return sdl.Color{}, false
	}
	return tabPalette[idx], true
}

// allocateTab claims a tab for a new session, reaping dead tabs first, and reports failure at the cap.
func (a *App) allocateTab() bool {
	reaped := false
	for i := 0; i < len(a.tabs); i++ {
		if a.tabs[i].dead && a.tabs[i].state.conn == nil {
			a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
			if a.activeTab > i {
				a.activeTab--
			}
			i--
			reaped = true
		}
	}
	if reaped {
		a.reapTranscripts() // a reaped dead tab's transcript writer goes with it (F4)
	}
	if lim := a.d.Prefs.Load().TabCap(); len(a.tabs) >= lim {
		a.connErr = fmt.Sprintf("%d tabs max — close one first (click its ✕)", lim)
		return false
	}
	a.tabs = append(a.tabs, &courtTab{})
	a.activeTab = len(a.tabs) - 1
	return true
}

// parkActive moves the live session into its tab, dropping the room and message raster first.
// Rehearsal sessions never park: backgrounding one would keep the manager's global offline mode on under a live tab.
func (a *App) parkActive() {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return
	}
	if a.rehearsal {
		a.deliberateClose = true // ending rehearsal mode is deliberate — never auto-reconnect (#1)
		a.Disconnect()           // rehearsal never parks (global offline gate)
		return
	}
	if a.msRaster != nil {
		a.msRaster.Destroy()
		a.msRaster = nil
	}
	a.msAnim, a.msAnimFont = nil, nil // #M5: drop the parked tab's animated message too
	a.tabs[a.activeTab].inCourt = a.room != nil
	a.room = nil
	// Keep the single music stream rolling in the background, ducked to 0 unless MusicAcrossTabs, so its position
	// survives and a switch back just un-ducks. Tab close and Disconnect stop it, so it can't leak.
	a.musicTabDucked = !a.d.Prefs.Load().MusicAcrossTabs
	// Any await the parked tab held is void, and a stale one could un-duck the new background stream; the incoming
	// tab's buildRoom derives its own.
	a.musicAwaitURL = ""
	a.musicAwaitSince = time.Time{} // drop the timeout stamp with the void await
	// Clear the message duck before composing volumes, or a cross-tab stream sticks at 35% with nothing on stage:
	// resetSessionState clears the flag without re-applying volumes. buildRoom recomputes it on return.
	a.musicDucked = false
	a.applyAudioVolumes() // push the duck (or its absence) to the mixer (no-op if no Audio)
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = nil
		a.d.Viewport.OnFrameShown = nil // #17: parked tab's room no longer owns the viewport
	}
	a.tabs[a.activeTab].state = a.sessionState
	a.resetSessionState()
	a.activeTab = -1
}

// activateTab restores a parked session, parking whatever is active and landing on the right screen.
func (a *App) activateTab(i int) {
	if i < 0 || i >= len(a.tabs) || i == a.activeTab {
		return
	}
	// The URL builder is per-tab and rebuildAssetOrigin doesn't run on a switch, so the local-pack layer must
	// re-register this tab's origin here. Deferred because the dead-tab arms return early.
	defer a.applyMountLayer()
	if a.tabs[i] == a.splitTab {
		a.clearSplit() // the pinned tab is becoming the primary
	}
	a.parkActive()
	t := a.tabs[i]
	a.sessionState = t.state
	t.state = sessionState{}
	t.unread = 0
	a.activeTab = i
	// Bump the epoch so the shared log caches can't serve the parked tab's lines, even on a rapid A→B→A round trip.
	a.logViewEpoch++
	a.warnLine = "" // warnings are per-session context
	// Field undo histories are keyed by field ID, not tab: wipe them so one session's drafts can't resurface in
	// another.
	a.ctx.ClearFieldHistories()
	if t.dead && a.sess != nil && t.inCourt {
		// Died in the background while in court: the drop couldn't open a dialog then, so show the stored reason now,
		// over the restored courtroom. The socket is already gone.
		a.buildRoom() // rebuild WITHOUT fresh-entry resets (parked iniswap/pos survive)
		// freezeSessionUnderDialog stops the audio: buildRoom re-seeds the area's music assuming a live session, and a
		// dead tab must not play.
		a.freezeSessionUnderDialog(t.deadReason)
		t.dead = false    // consumed: the dialog now owns this drop (Back to lobby / Reconnect finish it)
		t.deadReason = "" // and the latched reason has been shown
		a.ensureThemeForSession()
		a.updatePresence()
		return
	}
	if t.dead || a.sess == nil {
		// Died in the background with no court to freeze: tear the tab down and say why on the lobby. Background drops
		// don't auto-reconnect.
		name := a.serverName
		a.deliberateClose = true
		a.Disconnect()
		a.connErr = name + " disconnected while backgrounded"
		return
	}
	if t.inCourt {
		a.buildRoom() // rebuild the room WITHOUT the fresh-entry resets, so the
		// parked iniswap and /pos override survive the tab switch (enterCourtroom is for a fresh char-select entry
		// only)
	} else {
		a.screen = ScreenCharSelect
	}
	a.ensureThemeForSession() // the tab's theme binding follows it in
	a.updatePresence()
}

// moveTab reorders the strip, keeping activeTab on the same session.
func (a *App) moveTab(from, to int) {
	n := len(a.tabs)
	if from == to || from < 0 || to < 0 || from >= n || to >= n {
		return
	}
	t := a.tabs[from]
	a.tabs = append(a.tabs[:from], a.tabs[from+1:]...) // remove
	a.tabs = append(a.tabs, nil)                       // grow by one
	copy(a.tabs[to+1:], a.tabs[to:])                   // open a gap at `to`
	a.tabs[to] = t                                     // drop it in
	// Track the active tab through the remove (indices past from shift down) and insert (indices at or after to shift
	// up).
	act := a.activeTab
	switch {
	case act == from:
		act = to
	default:
		if act > from {
			act--
		}
		if act >= to {
			act++
		}
	}
	a.activeTab = act
}

// closeActiveTab disconnects the live session and removes its tab.
func (a *App) closeActiveTab() {
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) {
		a.tabs = append(a.tabs[:a.activeTab], a.tabs[a.activeTab+1:]...)
	}
	a.activeTab = -1
	a.reapTranscripts() // F4: any session this slot held is over — don't strand its writer
}

// closeParkedTab disconnects a BACKGROUND tab (chip ✕).
func (a *App) closeParkedTab(i int) {
	if i < 0 || i >= len(a.tabs) || i == a.activeTab {
		return
	}
	t := a.tabs[i]
	if t == a.splitTab {
		a.clearSplit()
	}
	if t.state.conn != nil {
		t.state.conn.Close()
	}
	if t.state.notebook != nil {
		go func(nb *config.Notebook) { _ = nb.Flush() }(t.state.notebook)
	}
	// A ducked stream is owned by some background tab, possibly this one, and we can't cheaply tell which, so stop it
	// to avoid an orphan (hard rule 4); the real owner resumes it on return.
	// An armed await means the active tab owns the in-flight fetch: stopping would cancel it and leave that tab silent.
	if a.d.Audio != nil && a.musicTabDucked && a.musicAwaitURL == "" {
		a.d.Audio.StopMusic()
		a.musicTabDucked = false
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.activeTab > i {
		a.activeTab--
	}
	a.reapTranscripts() // the closed tab's session is gone: release its transcript file + goroutine (F4)
}

// requestCloseTab confirms a manual chip-✕ click, since the ✕ abuts the switch zone on the same chip.
// Instant if the pref is set or the tab is dead. Internal teardowns call closeParkedTab directly.
func (a *App) requestCloseTab(i int) {
	if i < 0 || i >= len(a.tabs) || i == a.activeTab {
		return // the active chip has no ✕; guard anyway
	}
	t := a.tabs[i]
	if a.d.Prefs.Load().InstantDisconnect || t.dead {
		a.closeParkedTab(i) // nothing to confirm — close now (unchanged behavior)
		return
	}
	a.pendingCloseTab = t // stash the pointer; the modal revalidates it before acting
}

// confirmPendingCloseTab closes the pending tab at its current index; it may have moved or gone while the modal was up.
func (a *App) confirmPendingCloseTab() {
	t := a.pendingCloseTab
	a.pendingCloseTab = nil
	if t == nil {
		return
	}
	for i := range a.tabs {
		if a.tabs[i] == t {
			a.closeParkedTab(i)
			return
		}
	}
}

// pumpBackgroundTabs drains every parked tab's connection on a budget, on the render thread like the active pump.
func (a *App) pumpBackgroundTabs() {
	for i, t := range a.tabs {
		if i == a.activeTab || t.dead {
			continue
		}
		s := &t.state
		if s.conn == nil || s.sess == nil {
			continue
		}
		// Keep the tab's keepalive goroutine primed so a backgrounded tab isn't dropped for idling.
		s.conn.SetKeepalive(s.sess.KeepalivePacket())
		for drained := 0; drained < tabPumpBudget; drained++ {
			select {
			case p, ok := <-s.conn.Incoming():
				a.uiDirty = true // unread badges / tab chips / the pinned pane change with parked-tab traffic
				if !ok {
					t.dead = true
					// Recorded for the disconnect dialog shown when the user activates this tab; same shape as
					// pumpConnection's reason.
					reason := "connection closed"
					if s.conn != nil {
						if err := s.conn.Err(); err != nil {
							reason = err.Error()
						}
					}
					t.deadReason = reason
					s.conn = nil
					if t == a.splitTab {
						// clearSplit removes the pane, so announce the drop first or a pinned server's death is silent.
						a.jukeWarn("Pinned server " + s.serverName + " disconnected: " + reason)
						a.clearSplit() // the pinned right-pane server dropped
					}
					// Not auto-reconnected: the autoReconnect* fields track one countdown, and arming them for a parked
					// tab would fight the primary's.
					a.pushDebug("tab " + s.serverName + ": connection closed in background")
					break
				}
				for _, ev := range s.sess.HandlePacket(p) {
					a.routeBackgroundEvent(t, ev)
					if t == a.splitTab && a.splitRoom != nil {
						a.splitRoom.HandleEvent(ev) // drive the pinned right-pane stage
					}
				}
			default:
				drained = tabPumpBudget // empty: stop draining this tab
			}
			if t.dead {
				break
			}
		}
	}
}

// routeBackgroundEvent applies the events a parked tab surfaces; everything else already mutated its session in
// HandlePacket.
func (a *App) routeBackgroundEvent(t *courtTab, ev courtroom.Event) {
	s := &t.state
	switch ev.Kind {
	case courtroom.EventMessage:
		if ev.Message != nil {
			// A parked tab's own send clears on its server's echo too, so the split pane's input survives a raced send.
			if s.sess != nil && ev.Message.CharID == s.sess.MyCharID {
				s.noteOwnICEcho()
			}
			fr, fc := a.friendMessage(s.serverKey, ev.Message)
			force := a.d.Prefs.Load().ForceCharNames
			// nil char.ini on purpose: charMetaFor resolves against the active tab's origin, which would put another
			// server's showname on this line.
			// One name for both the entry's speaker and capLogLine's head marker, so the two can't drift.
			speaker := icSpeakerName(ev.Message, force, nil)
			line, _, bodyRuneStart, styles := icLogEntry(ev.Message, force, "", nil)
			s.icLog = append(s.icLog, icEntry{text: capLogLine(line, speaker), color: ev.Message.TextColor, friend: fr, friendColor: fc, speaker: speaker, pos: icPos(ev.Message), stamp: a.icStamp(), styles: styles, bodyRuneStart: bodyRuneStart})
			if cap := a.icLogCapFor(); cap > 0 && len(s.icLog) > cap {
				copy(s.icLog, s.icLog[len(s.icLog)-cap:])
				s.icLog = s.icLog[:cap]
			}
			s.icLogSeq++
			t.unread++
			if fr {
				a.signalFriend(s.serverName, ev.Message) // alert even from a backgrounded server
			}
			a.logDetailed(s.serverName, s.logSession, ev.Message) // detailed transcript (opt-in), the PARKED tab's own file
			names := a.mentionNamesFor(s)
			a.checkCallwords(ev.Message.Message, names, isSelfName(ev.Message.CharName, names))
		}
	case courtroom.EventOOC:
		// The same call pushOOC makes for the live tab; pushOOC itself can't run here because it mutates active-tab
		// state.
		line := stripDisplayTail(ev.Name+": "+ev.Text, ev.Name)
		if len(line) > oocLineCap {
			line = line[:oocLineCap] + "…"
		}
		s.oocLog = appendCapped(s.oocLog, line, icLogCap)
		s.oocSpeakers = appendCapped(s.oocSpeakers, ev.Name, icLogCap) // parallel: for name colours
		s.oocSeq++
		a.logDetailedOOC(s.serverName, s.logSession, line) // detailed transcript (opt-in), the PARKED tab's own file
		// OOC doesn't bump unread by default because servers post auto-messages there. Opt in through Settings.
		if a.d.Prefs.Load().NotifyOnOOC {
			t.unread++
		}
		if a.d.Prefs.Load().CallwordsOOC && !looksLikeAreaList(ev.Text) { // OOC callwords opt-in (default OFF); /ga roster never self-pings
			names := a.mentionNamesFor(s)
			a.checkCallwords(ev.Text, names, isSelfName(ev.Name, names))
		}
	case courtroom.EventModcall:
		// A modcall on a background server still alerts, like the friend signal.
		modcallLine := "[MOD CALL] " + ev.Text
		s.oocLog = appendCapped(s.oocLog, modcallLine, icLogCap)
		s.oocSpeakers = appendCapped(s.oocSpeakers, "", icLogCap) // system line — no name tint
		s.oocSeq++
		a.logDetailedOOC(s.serverName, s.logSession, modcallLine) // detailed transcript (opt-in), the PARKED tab's own file
		t.unread++
		a.signalModcall(s.serverName, ev.Text)
		a.autoClipModcall(s.serverName, s.icLog, ev.Text) // freeze IC context even on a backgrounded server
	case courtroom.EventBackground:
		a.d.Prefs.RememberServerBackground(s.serverKey, ev.Text)
		if ev.Wipe {
			// The same overlay teardown the live tab does; a parked tab has no Courtroom, so this is the only place to
			// clear it.
			s.wtceName, s.testimonyOn, s.evShowImg = "", false, ""
		}
	case courtroom.EventSetPos:
		// KFO-family servers carry the pos inside an area change's BN and send no SP, so a parked tab must track it
		// here.
		s.sidePref = ev.Text
	case courtroom.EventMusic:
		// HandlePacket already advanced s.sess.MusicTrack, which the return switch reads. Only an audible cross-tab
		// stream owned by this tab has to follow here, since the room that issues PlayMusic parks with the tab.
		if url, ok := a.backgroundMusicFollow(s); ok {
			a.d.Audio.PlayMusic(url, ev.Loop, ev.MusicEffects) // AssetType: Music
		}
	case courtroom.EventDisconnect:
		t.dead = true
		t.deadReason = ev.Text // "Kicked: …" / "Banned: …" — the dialog names it on activation
		disconnectLine := "SERVER: disconnected: " + ev.Text
		s.oocLog = appendCapped(s.oocLog, disconnectLine, icLogCap)
		s.oocSpeakers = appendCapped(s.oocSpeakers, "", icLogCap) // system line
		s.oocSeq++
		a.logDetailedOOC(s.serverName, s.logSession, disconnectLine) // detailed transcript (opt-in), the PARKED tab's own file
	}
}

// backgroundMusicFollow reports whether the single audible stream should follow a background tab's DJ /play, and the
// URL.
// Only with MusicAcrossTabs on, when this tab owns the stream and really has a track; the URL uses the tab's own
// builder.
func (a *App) backgroundMusicFollow(s *sessionState) (url string, ok bool) {
	if !a.d.Prefs.Load().MusicAcrossTabs || a.d.Audio == nil {
		return "", false
	}
	if a.musicOwnerKey == "" || a.musicOwnerKey != s.serverKey {
		return "", false // stream is silent-isolated, or owned by another tab: don't touch it
	}
	if s.sess == nil || s.sess.MusicTrack == "" {
		return "", false // ~stop / no track: nothing to switch to
	}
	return s.urls.MusicURL(s.sess.MusicTrack), true
}

// tabBarRects computes the chip rects plus the "+" chip (add.W == 0 at the tab cap).
func (a *App) tabBarRects(w, h int32) (rects []sdl.Rect, add sdl.Rect) {
	if len(a.tabs) == 0 {
		return nil, sdl.Rect{}
	}
	rects = make([]sdl.Rect, len(a.tabs))
	total := int32(0)
	for i := range a.tabs {
		rects[i].W = a.tabChipW(i)
		total += rects[i].W + tabChipGapX
	}
	showAdd := len(a.tabs) < a.d.Prefs.Load().TabCap() // no "+" once every slot is full
	if showAdd {
		total += addTabChipW + tabChipGapX
	}
	// ensureClassicOv loads the override map slotRect reads.
	a.ensureClassicOv()
	def := a.tabStripDockedRect(total, w)
	origin := a.tabStripOrigin(def, w, h)
	x, y := origin.X, origin.Y
	for i := range rects {
		rects[i].X, rects[i].Y, rects[i].H = x, y, tabBarH
		x += rects[i].W + tabChipGapX
	}
	if showAdd {
		add = sdl.Rect{X: x, Y: y, W: addTabChipW, H: tabBarH}
	}
	return rects, add
}

// tabStripDefaultX centres the strip in the window, clamped to the left edge so a wider strip still starts on screen.
func tabStripDefaultX(total, w int32) int32 {
	x := (w - total) / 2
	if x < 0 {
		x = 0
	}
	return x
}

// tabChipW is the i-th chip's width; alloc-free because tabChipLabel and TextWidth are memoized.
func (a *App) tabChipW(i int) int32 {
	return a.ctx.TextWidth(a.tabChipLabel(i)) + tabChipTextPadX
}

// tabStripTotalW is the strip's intrinsic width, without allocating rects or reading the layout cache being built.
func (a *App) tabStripTotalW() int32 {
	if len(a.tabs) == 0 {
		return 0
	}
	total := int32(0)
	for i := range a.tabs {
		total += a.tabChipW(i) + tabChipGapX
	}
	if len(a.tabs) < a.d.Prefs.Load().TabCap() {
		total += addTabChipW + tabChipGapX
	}
	return total
}

// tabStripDockedRect is the strip's home: window-centred under the menu bar; total is its intrinsic width.
func (a *App) tabStripDockedRect(total, w int32) sdl.Rect {
	return sdl.Rect{X: tabStripDefaultX(total, w), Y: a.menuBarHeight(), W: total, H: tabBarH}
}

// seedTabBarDesignRect adds a default "asyncao_tabbar" rect when the theme is silent, and reports whether it did.
// Call on the pristine map before both override tiers, which only rewrite existing keys; the layout cache, the themed
// editor's editable set and both reset paths all need the key present.
// The default is centred at the canvas top; W/H are inert, and the strip stays in the chrome band until parked.
func seedTabBarDesignRect(layout map[string]theme.Rect) bool {
	if layout == nil {
		return false
	}
	if _, declared := layout[themeTabBarKey]; declared {
		return false
	}
	court, ok := layout["courtroom"]
	if !ok || court.W <= 0 || court.H <= 0 {
		return false // no canvas ⇒ no themed layout at all ⇒ nothing to seed into
	}
	w := tabBarDesignSeedW
	if w > court.W {
		w = court.W
	}
	layout[themeTabBarKey] = theme.Rect{X: (court.W - w) / 2, Y: 0, W: w, H: int(tabBarH)}
	return true
}

// seedTabBarThroughOverrides seeds, folds the author's [overrides], and reports whether only this client placed the
// strip.
// Seed first because the fold only rewrites existing keys. Compare after because the seed alone reports true even when
// the author placed the strip; comparing the rect keeps one copy of the fold's policy.
func seedTabBarThroughOverrides(layout map[string]theme.Rect, sc *theme.Sidecar) bool {
	seeded := seedTabBarDesignRect(layout)
	before := layout[themeTabBarKey]
	foldSidecarOverrides(layout, sc)
	if seeded && layout[themeTabBarKey] != before {
		return false // the author DID place the strip — in the AsyncAO tier, not AO2's
	}
	return seeded
}

// tabStripThemeParked reports whether the theme's layout, not the chrome band, owns where the strip sits.
// Recorded rather than inferred from geometry: a drag landing on the seed's coordinates would look unmoved.
// A themed-editor drag in flight also counts: nothing persists until release, so the strip would otherwise sit frozen.
func (a *App) tabStripThemeParked() bool {
	if a.tabStripPlacementRecorded() {
		return true
	}
	if !a.layoutEdit || a.editDrag == 0 || a.editTgt.designKey() != themeTabBarKey {
		return false
	}
	_, present := a.themeRects[themeTabBarKey]
	return present // a placement is happening right now
}

// tabStripPlacementRecorded is tabStripThemeParked without the drag in flight: the theme's declaration or the persisted
// override.
// layoutSnapshotNow needs this one: restoreLayout writes the flag back, and drawLayoutEditor sets editDrag before
// pushing the pre-press snapshot.
func (a *App) tabStripPlacementRecorded() bool {
	if _, present := a.themeRects[themeTabBarKey]; !present {
		return false // no key at all ⇒ no themed home
	}
	if !a.tabBarSeeded {
		return true // the theme author placed it
	}
	name, _ := a.d.Prefs.Load().Theme()
	return a.d.Prefs.Load().HasThemeRectOverride(name, themeTabBarKey)
}

// tabStripIntrinsicW is tabStripTotalW with a guard, because themeLayoutIn runs in fixtures and export paths without a
// Ctx.
func (a *App) tabStripIntrinsicW() int32 {
	if a.ctx == nil || len(a.tabs) == 0 {
		return 0
	}
	return a.tabStripTotalW()
}

// tabStripCacheRect is the strip's entry in the themed layout cache: the live painted rect, parked or not, so the
// editor's box matches the widget.
// Parked X/Y are canvas-relative client px: a scaled map over integers has gaps above scale 1, so a one-pixel drag
// couldn't be expressed, and the strip's size is already unscaled.
// It runs inside themeLayoutIn before lay.valid, so it takes the half-built cache and must not recurse into the
// rebuild.
func (a *App) tabStripCacheRect(lay *themeLayoutCache, w, h, total int32, parked bool) (sdl.Rect, bool) {
	if total < minThemedElementPx {
		return sdl.Rect{}, false // no chips (or a degenerate strip): nothing to place
	}
	def := a.tabStripDockedRect(total, w)
	if !parked {
		// Unparked: same as tabStripOrigin's fallback. Only the origin survives: the persisted W/H are window fractions
		// and the strip is move-only.
		// the strip is move-only, so its size is always the chips'.
		a.ensureClassicOv()
		r := a.slotRect(slotTabBar, def, w, h)
		r.W, r.H = def.W, def.H
		return r, true
	}
	// Its exact inverse is editBaseFor and its clamp twin is clampTabStripScreen; all three must keep the same form and
	// order of operations or the grab point drifts.
	r := def
	d := a.themeRects[themeTabBarKey]
	r.X = value.Clamp(lay.offX+int32(d.X), 0, max(w-r.W, 0))
	r.Y = value.Clamp(lay.offY+int32(d.Y), 0, max(h-r.H, 0))
	return r, true
}

// tabStripOrigin resolves the strip's top-left: theme rect first, then a layout-editor override, else the docked
// default.
// Only X/Y are taken: the cache entry is already clamped and chip-sized, and re-clamping would separate the editor's
// box from the paint.
func (a *App) tabStripOrigin(def sdl.Rect, w, h int32) sdl.Rect {
	if tr, ok := a.tabStripThemeRect(); ok {
		cur := def
		cur.X, cur.Y = tr.X, tr.Y
		return cur
	}
	return a.slotRect(slotTabBar, def, w, h)
}

// tabStripThemeRect returns the theme's home for the strip when the themed courtroom is on screen.
// Excludes gif export, replay and scene maker: they keep a.screen == Courtroom but rebuild the shared layout cache at
// the export's own dimensions.
func (a *App) tabStripThemeRect() (sdl.Rect, bool) {
	if a.screen != ScreenCourtroom || a.room == nil || a.theaterOn || a.screenDispatchPreempted() {
		return sdl.Rect{}, false
	}
	if !a.themeLay.valid || !a.d.Prefs.Load().ThemeLayoutOn {
		return sdl.Rect{}, false
	}
	if !a.tabStripThemeParked() {
		// The key exists but nobody placed it: hand the decision back to the classic override or docked default.
		return sdl.Rect{}, false
	}
	return a.themeLay.rect(themeTabBarKey)
}

// buildTabChipLabel formats a chip's text: the clipped name, plus " ✕" when dead or " (N)" for unread on a background
// tab.
func buildTabChipLabel(key tabChipKey) string {
	name := key.name
	if name == "" {
		name = "tab"
	}
	if len(name) > tabChipMaxName {
		name = name[:tabChipMaxName] + "…"
	}
	if !key.active {
		if key.dead {
			return name + " ✕"
		}
		if key.unread > 0 {
			return fmt.Sprintf("%s (%d)", name, key.unread)
		}
	}
	return name
}

// tabChipLabel is the memoized chip text, because the strip asks for every chip several times per frame.
func (a *App) tabChipLabel(i int) string {
	t := a.tabs[i]
	key := tabChipKey{name: a.tabName(i), active: i == a.activeTab, dead: t.dead, unread: t.unread}
	if t.chipLabel == "" || t.chipKey != key {
		t.chipLabel = buildTabChipLabel(key)
		t.chipKey = key
	}
	return t.chipLabel
}

// handleTabBar consumes clicks on the strip before the screens draw, so chips can't double-act with widgets underneath.
func (a *App) handleTabBar(w, h int32) {
	c := a.ctx
	rects, add := a.tabBarRects(w, h) // also registers the strip's slot for the editor
	// The Edit Layout editor owns the strip while open; tabBarRects above already registered it for the editor.
	if a.classicEdit {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	// The tab-close modal overlays this strip, and this runs before the frame's pointer blocking, so keep the strip
	// inert or clicks leak through.
	if a.pendingCloseTab != nil {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	// A chip click here would park the frozen session (conn nil) into a tab pumpBackgroundTabs never drains. Resolve
	// the dialog first.
	if a.disconnectDlg.open {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	// The notice box stays on App and doesn't park with the tab, and a removal notice's appeal text must not be
	// switched away from unread.
	if a.serverNoticeDlg.open {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	pressed := c.mouseDown && !a.tabPrevDown
	a.tabPrevDown = c.mouseDown
	if rects == nil {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	if a.handleTabDrag(rects, pressed) {
		return // a reorder drag consumed this gesture; don't switch/close
	}
	// Right-click a BACKGROUND tab to pin/unpin it as the split's right pane.
	if c.rightClicked {
		for i, r := range rects {
			if i != a.activeTab && c.hovering(r) {
				a.pinToSplit(a.tabs[i])
				c.rightClicked = false
				return
			}
		}
	}
	if !c.clicked {
		return
	}
	// Ctrl+click a chip cycles its colour; consumes the click so it doesn't switch.
	if c.ctrlHeld {
		for i, r := range rects {
			if c.hovering(r) {
				a.cycleTabColor(i)
				c.clicked = false
				return
			}
		}
	}
	if add.W > 0 && c.hovering(add) {
		// Park the active session, which keeps running, and show the lobby, where connecting opens the new tab.
		a.parkActive()
		a.ensureThemeForSession()
		a.screen = ScreenLobby
		a.updatePresence()
		c.clicked = false
		return
	}
	for i, r := range rects {
		if !c.hovering(r) {
			continue
		}
		// The right end of a chip closes; the rest switches.
		if c.mouseX > r.X+r.W-16 && i != a.activeTab {
			a.requestCloseTab(i) // manual click confirms first (easy to fat-finger) — internal teardowns still call closeParkedTab direct
		} else if i == a.activeTab {
			// Clicking the active chip parks it and shows the lobby, to browse while connected.
			a.parkActive()
			a.ensureThemeForSession() // lobby shows the global theme
			a.screen = ScreenLobby
			a.updatePresence()
		} else {
			a.activateTab(i)
		}
		c.clicked = false // swallowed: nothing underneath reacts
		return
	}
}

// tabTearingOff reports a background chip dragged below the strip; the active tab is already the primary and can't tear
// off.
func (a *App) tabTearingOff() bool {
	return a.tabDragging && a.tabDragFrom >= 0 && a.tabDragFrom != a.activeTab &&
		a.ctx.mouseY > tabTearOffY
}

// handleTabDrag runs chip reorder and tear-off drags, and returns true when a release ended a drag so the caller
// swallows the click.
// Pressing the ✕ end never arms.
func (a *App) handleTabDrag(rects []sdl.Rect, pressed bool) bool {
	c := a.ctx
	if pressed && a.tabDragFrom < 0 {
		for i, r := range rects {
			if c.hovering(r) && c.mouseX <= r.X+r.W-16 { // chip body, not the ✕
				a.tabDragFrom = i
				a.tabDragStart = [2]int32{c.mouseX, c.mouseY}
				a.tabDragging = false
				break
			}
		}
	}
	if a.tabDragFrom >= 0 && c.mouseDown {
		if !a.tabDragging {
			dx, dy := c.mouseX-a.tabDragStart[0], c.mouseY-a.tabDragStart[1]
			if dx*dx+dy*dy > tabDragThreshold*tabDragThreshold {
				a.tabDragging = true
			}
		}
		if a.tabDragging && !a.tabTearingOff() { // tear-off mode: hold position, don't reorder
			target := a.tabDragFrom
			last := rects[len(rects)-1]
			switch {
			case c.mouseX < rects[0].X:
				target = 0
			case c.mouseX >= last.X+last.W:
				target = len(rects) - 1
			default:
				for i, r := range rects {
					if c.mouseX >= r.X && c.mouseX < r.X+r.W {
						target = i
						break
					}
				}
			}
			if target != a.tabDragFrom {
				a.moveTab(a.tabDragFrom, target)
				a.tabDragFrom = target // follow the slot to its new index
			}
		}
	}
	if !c.mouseDown {
		wasDragging := a.tabDragging
		tearOff := a.tabTearingOff() // capture before clearing the drag state
		from := a.tabDragFrom
		a.tabDragFrom, a.tabDragging = -1, false
		if wasDragging {
			c.clicked = false
			if tearOff && from >= 0 && from < len(a.tabs) {
				t := a.tabs[from]
				if a.splitTab != t {
					a.pinToSplit(t) // pop this background server out as a floating window
				}
				a.placeClientAt(c.mouseX, c.mouseY) // land where you dropped it (reposition if already open)
			}
			return true
		}
	}
	return false
}

// drawTabBar paints the strip (after the screens, before overlays).
func (a *App) drawTabBar(w, h int32) {
	c := a.ctx
	// Tear-off preview: a ghost of the floating window under the cursor, at the size it will appear.
	if a.tabTearingOff() {
		pw, ph := a.clientWin.w, a.clientWin.h
		if pw <= 0 {
			pw = clientWinDefW
		}
		if ph <= 0 {
			ph = clientWinDefH
		}
		ghost := sdl.Rect{X: c.mouseX - pw/2, Y: c.mouseY - floatTitleH/2, W: pw, H: ph}
		c.Fill(ghost, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 40})
		c.Border(ghost, ColAccent)
		c.Fill(sdl.Rect{X: ghost.X, Y: ghost.Y, W: ghost.W, H: floatTitleH}, sdl.Color{R: ColPanelHi.R, G: ColPanelHi.G, B: ColPanelHi.B, A: 220})
		c.LabelClipped(ghost.X+10, ghost.Y+8, ghost.W-20, "▣ "+a.tabName(a.tabDragFrom), ColAccent)
		c.LabelClipped(ghost.X+10, ghost.Y+floatTitleH+10, ghost.W-20, "Release to pop out as a floating window", ColText)
	}
	rects, add := a.tabBarRects(w, h)
	for i, r := range rects {
		bg := ColPanel
		col := ColTextDim
		switch {
		case i == a.activeTab:
			bg, col = ColPanelHi, ColText
		case a.tabs[i].dead:
			col = ColDanger
		case a.tabs[i].unread > 0:
			col = ColAccent
		}
		c.Fill(r, sdl.Color{R: bg.R, G: bg.G, B: bg.B, A: 235})
		if tint, ok := a.tabChipTint(i); ok { // #22: colour-coding stripe along the chip's top
			c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: 3}, tint)
		}
		border := ColPanelHi
		if a.tabDragging && i == a.tabDragFrom {
			border = ColAccent // lifted: this chip is mid-reorder
		}
		c.Border(r, border)
		c.LabelClipped(r.X+6, r.Y+3, r.W-24, a.tabChipLabel(i), col)
		if i != a.activeTab {
			c.Label(r.X+r.W-14, r.Y+3, "✕", ColTextDim)
		}
		if !a.tabDragging {
			hint := "Click to switch  •  drag to reorder (↓ to pop out)  •  Ctrl+click to colour  •  ✕ to close"
			if i == a.activeTab {
				hint = "Drag to reorder  •  click to browse the lobby"
			}
			c.Tooltip(r, hint)
		}
	}
	if add.W > 0 {
		c.Fill(add, sdl.Color{R: ColPanel.R, G: ColPanel.G, B: ColPanel.B, A: 235})
		c.Border(add, ColAccent)
		c.Label(add.X+9, add.Y+3, "+", ColAccent)
		c.Tooltip(add, "Open another server in a new tab")
	}
}

// loweredCache memoizes ToLower(TrimSpace(src)): search filters run per frame and re-lowering allocates.
type loweredCache struct{ src, out string }

func (l *loweredCache) get(src string) string {
	if l.src != src {
		l.src = src
		l.out = strings.ToLower(strings.TrimSpace(src))
	}
	return l.out
}

// resetSessionState replaces the live session with a pristine one (maps initialized, sentinel ids set).
func (a *App) resetSessionState() {
	// Minted here, the one place a pristine state is built, so no tab can share another's log file; ids are never
	// reused.
	a.nextLogSession++
	a.sessionState = sessionState{
		logSession: a.nextLogSession,
		// Pair placement is per-session and deliberately not persisted; a fresh session starts centered and unflipped.
		pairWith:       protocol.UnpairedCharID,
		playerSort:     clampMode(a.d.Prefs.Load().PlayerListSort, playerSortModes), // remembered Players-tab sorts
		playerAreaSort: clampMode(a.d.Prefs.Load().PlayerListAreaSort, areaSortModes),
		// OOC name is per-tab; fresh tabs start from the saved default.
		oocName: a.defaultOOCName(),
		// Per-session UI backed by persisted prefs, so seed them on every fresh session.
		volStripOn:   a.d.Prefs.Load().VolStripOn,
		musicVolMode: a.d.Prefs.Load().MusicVolMode,
		// Channel mutes persist but live on sessionState, which every connect and disconnect rebuilds, so reseed here.
		// sfxMuted is deliberately session-only so an accidental hotkey can't leave SFX muted across a restart.
		masterMuted:  a.d.Prefs.Load().MasterVolMuted,
		musicMuted:   a.d.Prefs.Load().MusicVolMuted,
		sfxMuted:     false,
		blipMuted:    a.d.Prefs.Load().BlipVolMuted,
		spriteOv:     map[string][2]int{},
		pmThreads:    map[string][]pmLine{},
		evidIdx:      -1,
		icRecallIdx:  -1, // -1 = editing the live draft, not browsing history (#8)
		oocRecallIdx: -1, // same, for the OOC recall ring
		// Full bars so the first HP packets don't fire penalty sfx.
		hpPrev: [2]int{courtroom.HPBarMax, courtroom.HPBarMax},
		// Logs follow the tail until the user scrolls up.
		icStick:  true,
		oocStick: true,
		// Pre defaults on; selectEmote then follows each emote pick (AO2-Client ui_pre).
		icPreanim: true,
		// AO2 seeds char select's "Taken" filter checked on every join and never persists it (charselect.cpp:99).
		charShowTaken: true,
		charListSel:   -1, // no name-column selection yet
	}
	// The log text selection lives on App but is anchored into the active log's lines, so it must not cross sessions.
	a.logSelActive, a.logSelDragging = false, false
	// Bump the epoch so shared log caches miss: seqs start at 0 in every tab, so a matching seq would be a cross-tab
	// hit.
	a.logViewEpoch++
	// Field undo histories too: a fresh session must not Ctrl+Z into another session's drafts.
	if a.ctx != nil {
		a.ctx.ClearFieldHistories()
	}
	// Reap last, after the new id is in place; parkActive copies the outgoing state first, which keeps a background
	// tab's log open.
	a.reapTranscripts()
}
