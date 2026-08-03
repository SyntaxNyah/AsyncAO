package ui

// Multi-server tabs (bounded maxTabs). Design:
//
//   - The ACTIVE session's state is App.sessionState (embedded; promoted
//     names — the rest of the package reads `a.icInput` exactly as it did
//     before tabs existed). A parked tab holds its sessionState by value:
//     switching is two struct moves (slice headers + pointers), no deep
//     copies, no per-field plumbing.
//   - Background tabs stay CONNECTED: each frame their packets drain on a
//     budget into their OWN session reducer and logs (IC/OOC + unread
//     counter + callword flash). The room (scene, typewriter, raster) is
//     deliberately NOT kept for background tabs — nothing animates off
//     screen; activation rebuilds it via buildRoom, which re-seeds the
//     background, song, and last IC message (settled — Session.LastIC) from
//     the reducer, so tabbing back shows the stage a live watcher would have
//     ended on even when messages landed off screen.
//   - The log caches (IC filter/wrap, OOC wrap, split wrap) stay App-global,
//     keyed by the per-session log seq PLUS a per-view epoch (logViewEpoch /
//     splitPinEpoch, bumped on every session swap). The seq alone was NOT
//     enough: it starts at 0 in every tab, so two tabs at the same seq
//     collided on the shared cache and a switch served one tab's wrapped/
//     coloured lines under another (the log-crossover bug). The epoch makes a
//     switch a guaranteed miss; the chat-message raster instead parks in
//     sessionState (msRaster), so it rides the tab move directly.
//   - Per-server caches (T1/T2/T3) need nothing: keys are full URLs, so
//     three servers' assets already live in disjoint key space.

import (
	"fmt"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// maxTabs is the DEFAULT concurrent-session cap; the live cap is
	// configurable via prefs (config.TabCap, up to config.maxMultiTabCap). Kept
	// in sync with the config default so it can't drift. Each tab costs a
	// websocket, a session reducer, and two bounded logs (rule §17.4).
	maxTabs = config.DefaultMultiTabCap
	// tabPumpBudget bounds packets drained per BACKGROUND tab per frame —
	// a busy room can't starve the active tab's frame time.
	tabPumpBudget = 64
	// tabChipMaxName clips server names on tab chips.
	tabChipMaxName = 14
	// tabBarH is the server-tab strip's height — and, docked under the menu bar, the
	// second half of the app-chrome band every screen offsets by (chrometop.go).
	tabBarH = 22
	// addTabChipW is the "+" new-session chip at the end of the strip — the
	// discoverable way to open another server (the active-chip-park gesture
	// wasn't obvious to new players).
	addTabChipW = 26
	// tabDragThreshold is the cursor travel (logical px) past which a press on
	// a chip becomes a reorder drag instead of a switch/close click — mirrors
	// the wardrobe's iniDragThreshold.
	tabDragThreshold = 6
	// tabTearOffY is the Y (logical px) below which an in-progress chip drag stops
	// reordering and becomes a TEAR-OFF: releasing there pops a background tab out
	// as a floating client window at the cursor. Comfortably below the DOCKED strip
	// (menu bar + strip, #14) so a horizontal reorder never trips it; pulling the
	// chip down is the gesture. Sized for the docked case rather than measured per
	// frame: when the menu bar is hidden (gif export / replay / scene maker /
	// theater) the strip sits menuBarH higher, so the only effect of the constant
	// being generous there is a slightly longer pull — never a misfire.
	tabTearOffY = menuBarH + tabBarH + 34
	// themeTabBarKey is the courtroom_design.ini rect that parks the server-tab strip
	// inside a theme's design canvas — the twin of "asyncao_toolbox" for the toolbox,
	// registered in themeLayoutKeys (app.go).
	//
	// UNITS. Unlike every other key in the map, this rect's X/Y are CANVAS-RELATIVE
	// CLIENT px — an offset from the canvas origin in the pixels the strip is drawn
	// in, NOT design px scaled by the layout. tabStripCacheRect carries the full
	// argument; the short version is that the strip is intrinsically sized client
	// chrome (its W/H are already unscaled) and that an unscaled origin is the only
	// map an integer preimage can invert exactly, which is what a hand-placed widget
	// needs. At the shipped default fit — Native, scale 1 — the two readings coincide.
	//
	// It is an AsyncAO invention, so NO shipped theme declares it (the 74-theme
	// reference corpus has zero). Registering the key therefore bought nothing on its
	// own: the apply path only copies keys the theme actually declares, the layout
	// cache is built by ranging that map, and the THEMED editor's editable set is
	// built from the layout cache — so under every real theme the strip still had no
	// drag box, which is issue #14's literal complaint. seedTabBarDesignRect closes
	// that by SYNTHESIZING the key whenever the theme is silent (see it for the
	// default's geometry and for why the strip nevertheless stays in the chrome band
	// until it is moved).
	themeTabBarKey = "asyncao_tabbar"
	// tabBarDesignSeedW is the width the synthesized entry is BORN with, in design px.
	//
	// It is deliberately inert: the strip is INTRINSICALLY SIZED (its width comes from
	// the chips — tab count, names — and changes as tabs open, close and get renamed),
	// so nothing ever paints or hit-tests at this number. Every consumer re-derives the
	// size live: tabStripCacheRect builds the editor's box and the layout cache entry
	// from tabStripTotalW, and tabStripOrigin keeps only X/Y from the stored rect. The
	// value survives purely so the seeded rect is a plausible-looking record (and so
	// the magnet's sibling list and the editor's readout have a sane number), sized for
	// a couple of server chips on the 714 px stock AO2 canvas and clamped to a smaller
	// one.
	//
	// It used to be the width the editor's drag box was drawn at, which is what made
	// the box disagree with the strip the moment either changed.
	tabBarDesignSeedW = 240
	// tabChipTextPadX is a chip's padding around its label — room for the text plus
	// the trailing "✕" the active chip draws. tabChipGapX is the gap between chips.
	// Named per hard rule 9 so tabBarRects and tabStripTotalW cannot drift.
	tabChipTextPadX = 28
	tabChipGapX     = 4
)

// courtTab is one parked server session. While a tab is ACTIVE its state
// field is zero — the live copy is App.sessionState.
type courtTab struct {
	state  sessionState
	unread int  // IC+OOC lines landed while backgrounded
	dead   bool // connection ended while backgrounded
	// deadReason is the raw close reason recorded at background-death (socket close
	// or a kick/ban EventDisconnect). It rides the tab so that ACTIVATING a dead tab
	// can surface the disconnect dialog with the right cause instead of silently
	// booting to the lobby — a background drop must not pop a modal over the tab the
	// user is actually looking at, so the reason waits, latched, until they switch to
	// it. "" only for an already-torn-down slot (belt-and-suspenders).
	deadReason string
	inCourt    bool // a room existed when parked (activation re-enters it)
	// chipLabel memoizes the tab-strip label so the always-on tab bar (which
	// asks for it ~3×/tab/frame) allocates nothing while the tab's
	// name/state/unread are stable; rebuilt only when chipKey changes.
	chipLabel string
	chipKey   tabChipKey
}

// tabChipKey is everything a chip's text depends on — the memo key for
// tabChipLabel. Comparable (all value fields) so a changed field invalidates.
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

// tabName names a tab for the chip (parked state or the live one).
func (a *App) tabName(i int) string {
	if i == a.activeTab {
		return a.serverName
	}
	return a.tabs[i].state.serverName
}

// tabKey returns a tab's serverKey (its ws URL) — stable across park/activate, so per-tab colour
// (#22) sticks to the server, not the slot. "" for an unkeyed tab (it then never colours).
func (a *App) tabKey(i int) string {
	if i == a.activeTab {
		return a.serverKey
	}
	if i >= 0 && i < len(a.tabs) {
		return a.tabs[i].state.serverKey
	}
	return ""
}

// tabColorsCap bounds the per-tab colour map (#22; hard rule #4: no unbounded maps).
const tabColorsCap = 64

// tabPalette is the cycle of tab-chip tints (#22): index 0 is "no tint" (a fresh chip is unchanged),
// then a handful of distinct hues.
var tabPalette = []sdl.Color{
	{},                               // 0 = none
	{R: 200, G: 70, B: 70, A: 255},   // red
	{R: 210, G: 140, B: 50, A: 255},  // amber
	{R: 90, G: 170, B: 90, A: 255},   // green
	{R: 70, G: 140, B: 210, A: 255},  // blue
	{R: 160, G: 110, B: 210, A: 255}, // violet
	{R: 200, G: 110, B: 170, A: 255}, // pink
}

// cycleTabColor advances a tab's chip colour to the next palette entry (Ctrl+click), wrapping back
// to none. Keyed by serverKey so it follows the server across switches; drops the entry at 0 so the
// map only ever holds coloured tabs.
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

// tabChipTint returns a tab chip's colour-coding tint and whether one is set (#22).
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

// allocateTab claims a slot for a NEW session: dead tabs are reaped
// first; at the cap it fails with a visible reason. The caller becomes
// the active tab with a fresh sessionState.
func (a *App) allocateTab() bool {
	for i := 0; i < len(a.tabs); i++ {
		if a.tabs[i].dead && a.tabs[i].state.conn == nil {
			a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
			if a.activeTab > i {
				a.activeTab--
			}
			i--
		}
	}
	if lim := a.d.Prefs.TabCap(); len(a.tabs) >= lim {
		a.connErr = fmt.Sprintf("%d tabs max — close one first (click its ✕)", lim)
		return false
	}
	a.tabs = append(a.tabs, &courtTab{})
	a.activeTab = len(a.tabs) - 1
	return true
}

// parkActive moves the live session into its tab slot. Render-coupled
// pieces are dropped first: the room stops existing (no background
// animation by design) and the message raster's textures are freed.
// Rehearsal sessions never park — backgrounding one would hold the
// manager's global offline gate closed under a live tab.
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
	// Music CONTINUITY across a tab switch (the agreed exception to ecd6d9e's
	// log/scenery isolation): DON'T stop the single music stream when a tab goes
	// to the background. It keeps rolling — so its position is preserved perfectly
	// — but is DUCKED to volume 0 (musicTabDucked) so tabs stay acoustically
	// isolated. Switching back finds the same stream still playing this exact URL,
	// so buildRoom's resume is an idempotent no-op that just un-ducks: the track
	// picks up right where it was, in sync. If the INCOMING tab has its own song,
	// buildRoom swaps the stream (audible) and clears the duck there; the parked
	// tab's musicStartedAt lets a later return seek back near where it would be.
	// The MusicAcrossTabs pref (default OFF) instead keeps the backgrounded stream
	// AUDIBLE (no duck). We never StopMusic here anymore — tab CLOSE / disconnect
	// still do (closeParkedTab / Disconnect), so an orphaned stream can't leak.
	a.musicTabDucked = !a.d.Prefs.MusicAcrossTabsOn()
	// The outgoing tab's stream (whatever it is) is now the backgrounded one, freshly
	// (re-)ducked; any await the just-parked tab held is void — the INCOMING tab's
	// activateTab→buildRoom→resumeActiveTabMusic re-derives the correct await (or
	// none) off ITS own track. Clearing here is the switch-back-mid-await guard: a
	// stale await can't un-duck the new backgrounded context.
	a.musicAwaitURL = ""
	a.musicAwaitSince = time.Time{} // drop the timeout stamp with the void await
	// Clear the message duck BEFORE composing volumes. musicDucked is the ACTIVE,
	// drawn room's "a message is on stage → dip music to 35%" flag; a backgrounded
	// stream has no message on stage from the mixer's view (it's either ducked to 0
	// for isolation, or — MusicAcrossTabs ON — an audible continuity stream). If we
	// left musicDucked=true here, applyAudioVolumes below would push the cross-tab
	// stream to 35% and, because resetSessionState clears the flag WITHOUT re-applying
	// volumes and a char-select/lobby activate never re-composes them, the mixer would
	// stay stuck at 35% with nothing on stage until the next volume-affecting action.
	// On return, buildRoom's rederiveMessageDuck recomputes the flag from the live
	// phase, so clearing it here loses nothing (the parked slot's value is overwritten
	// on activate anyway).
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

// activateTab restores a parked session: park whatever is active, move
// the tab's state in, rebuild the room when a character was picked, and
// land on the right screen.
func (a *App) activateTab(i int) {
	if i < 0 || i >= len(a.tabs) || i == a.activeTab {
		return
	}
	if a.tabs[i] == a.splitTab {
		a.clearSplit() // the pinned tab is becoming the primary
	}
	a.parkActive()
	t := a.tabs[i]
	a.sessionState = t.state
	t.state = sessionState{}
	t.unread = 0
	a.activeTab = i
	// The restored session promotes its OWN log seqs (which resumed from where the
	// tab parked) into App; bump the log-view epoch so the shared IC/OOC wrap+filter
	// caches key on this fresh view and can't serve the just-parked tab's lines under
	// it. parkActive already bumped once via resetSessionState; this second bump
	// makes the restored view distinct from its previous incarnation too, so a rapid
	// A→B→A round-trip never lands on a stale cache. See the logViewEpoch field doc.
	a.logViewEpoch++
	a.warnLine = "" // warnings are per-session context
	// Field undo histories are keyed by field ID, not by tab — wipe them on a
	// switch so one session's drafts can't resurface (or read as "external
	// changes") in another (the multi-tab isolation rule).
	a.ctx.ClearFieldHistories()
	if t.dead && a.sess != nil && t.inCourt {
		// The tab's connection died in the BACKGROUND while it was in court. The
		// drop couldn't pop a modal then (it would have covered whatever tab the
		// user was actually looking at — the parked-tab rule), so the reason was
		// latched on the tab (deadReason). NOW that the user has switched to it,
		// surface the SAME involuntary-disconnect dialog the active-tab drop shows,
		// over the tab's own restored courtroom — the logs and last scene are intact
		// in the promoted session, so it reads as "this tab dropped" rather than a
		// silent boot to the lobby. The socket is already gone (pumpBackgroundTabs /
		// routeBackgroundEvent nilled s.conn and marked dead), so there's no network
		// teardown to do here — just rebuild the room to draw and open the dialog.
		a.buildRoom() // rebuild WITHOUT fresh-entry resets (parked iniswap/pos survive)
		// The SAME freeze the active-tab drop runs (freezeSessionUnderDialog), so the
		// two arms cannot drift again. Its conn guards make the network half a no-op
		// here — pumpBackgroundTabs already nilled s.conn and latched the reason — and
		// its audio teardown is what this arm did by hand, because buildRoom re-seeds
		// the area's music assuming a live session and a dead tab must not start
		// playing. Sharing it additionally silences a parked tab's leaked voice
		// devices, which the hand-rolled version never did.
		a.freezeSessionUnderDialog(t.deadReason)
		t.dead = false    // consumed: the dialog now owns this drop (Back to lobby / Reconnect finish it)
		t.deadReason = "" // and the latched reason has been shown
		a.ensureThemeForSession()
		a.updatePresence()
		return
	}
	if t.dead || a.sess == nil {
		// Died in the background with no court to freeze (char-select, or a session
		// that never fully connected): tear the tab down fully (notebook flush, slot
		// removed) and say why on the lobby — today's exact fallback. Deliberate
		// teardown of an already-dead tab; background-tab drops are their own concern
		// (#46), so suppress auto-reconnect here (#1).
		name := a.serverName
		a.deliberateClose = true
		a.Disconnect()
		a.connErr = name + " disconnected while backgrounded"
		return
	}
	if t.inCourt {
		a.buildRoom() // rebuild the room WITHOUT the fresh-entry resets, so the
		// parked iniswap + /pos override survive the tab switch (enterCourtroom is
		// for a fresh char-select entry only)
	} else {
		a.screen = ScreenCharSelect
	}
	a.ensureThemeForSession() // the tab's theme binding follows it in
	a.updatePresence()
}

// moveTab reorders the strip, moving the tab at `from` to index `to` and
// keeping activeTab pointing at whatever session was active across the move
// (remove-then-insert, so the index shifts in two steps). Slot identity is by
// pointer, so a parked session's state rides along untouched. Drag-gesture
// frequency, not per frame — the small reslice alloc is fine.
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
	// Track the active slot through the same remove (indices past `from` shift
	// down) then insert (indices at/after `to` shift up).
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

// closeActiveTab disconnects the live session and removes its slot.
func (a *App) closeActiveTab() {
	if a.activeTab >= 0 && a.activeTab < len(a.tabs) {
		a.tabs = append(a.tabs[:a.activeTab], a.tabs[a.activeTab+1:]...)
	}
	a.activeTab = -1
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
	// If the single music stream is currently DUCKED it's owned by a backgrounded
	// tab (cross-tab continuity kept it rolling silently — see parkActive). Closing
	// a background tab could be closing that owner, and we can't cheaply tell which
	// tab owns the stream, so stop it to avoid an orphaned stream leaking (rule §4).
	// It's inaudible anyway, so this is acoustically invisible; returning to the
	// real owner re-fetches and resumes it (buildRoom → resumeActiveTabMusic).
	//
	// EXCEPTION: an armed await (musicAwaitURL != "") means the ACTIVE tab is mid-
	// cross-tab-resume and OWNS the in-flight fetch — parkActive ducked, then
	// resumeActiveTabMusic armed the await and issued PlayMusicAt for THIS tab's own
	// track, which hasn't landed yet. StopMusic here would purgePendingMusic("") and
	// cancel the active tab's own pending fetch, leaving it permanently silent (no
	// re-fetch follows a background-chip close). The ducked-but-awaited stream isn't
	// an orphan — it belongs to the visible tab — so skip the stop and let the fetch
	// settle. A genuinely orphaned background stream never has an await armed against
	// it (parkActive clears the await when it re-ducks), so the leak guard still holds.
	if a.d.Audio != nil && a.musicTabDucked && a.musicAwaitURL == "" {
		a.d.Audio.StopMusic()
		a.musicTabDucked = false
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.activeTab > i {
		a.activeTab--
	}
}

// requestCloseTab gates a MANUAL chip-✕ click (tabs.go handleTabBar) behind the
// same confirm as requestDisconnect: closing a tab drops that server's live
// connection, and the ✕ hit-zone abuts the switch zone on the same chip, so it's
// easy to fat-finger (the reported bug). Instant when there's nothing to confirm
// — the escape-hatch pref is set, or the tab is already dead (its socket ended in
// the background, so there's no live session to lose) — otherwise it stashes the
// tab POINTER and opens the confirm modal. Only the manual click routes through
// here; every INTERNAL teardown (dead-tab reaping in activateTab/allocateTab, the
// split-drop in pumpBackgroundTabs, clearSplit ordering) keeps calling
// closeParkedTab directly, so those stay instant — this splits the manual case
// out of the "internal disconnects keep calling Disconnect() direct" doctrine.
func (a *App) requestCloseTab(i int) {
	if i < 0 || i >= len(a.tabs) || i == a.activeTab {
		return // the active chip has no ✕; guard anyway
	}
	t := a.tabs[i]
	if a.d.Prefs.InstantDisconnectOn() || t.dead {
		a.closeParkedTab(i) // nothing to confirm — close now (unchanged behavior)
		return
	}
	a.pendingCloseTab = t // stash the pointer; the modal revalidates it before acting
}

// confirmPendingCloseTab is the "Yes" action of the tab-close confirm: it
// re-finds the pending tab's CURRENT index (it may have reordered/reaped/torn-off
// while the modal was up, since the target is a pointer, not the click-time
// index), closes it via the unchanged closeParkedTab, and clears the pending
// state. Silently drops the close if the tab is already gone from a.tabs — the
// pointer is stale, so there's nothing left to close. Split out from the modal
// draw so it's unit-testable without the CGO/SDL button (the draw path can't run
// headlessly).
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

// pumpBackgroundTabs drains every parked tab's connection on a budget:
// the session reducer keeps its court state current, chat lands in the
// tab's own logs (unread counter, callword flash), and a closed
// connection marks the tab dead. Runs on the render thread like the
// active pump — no locks anywhere.
func (a *App) pumpBackgroundTabs() {
	for i, t := range a.tabs {
		if i == a.activeTab || t.dead {
			continue
		}
		s := &t.state
		if s.conn == nil || s.sess == nil {
			continue
		}
		// Keep this parked tab's background keepalive goroutine primed (it sends the
		// CH ping off the render loop, so a backgrounded app doesn't idle-drop it).
		s.conn.SetKeepalive(s.sess.KeepalivePacket())
		for drained := 0; drained < tabPumpBudget; drained++ {
			select {
			case p, ok := <-s.conn.Incoming():
				a.uiDirty = true // unread badges / tab chips / the pinned pane change with parked-tab traffic
				if !ok {
					t.dead = true
					// Record WHY, for the disconnect dialog shown when the user
					// activates this dead tab. Same shape as pumpConnection's
					// closed-channel reason (conn.Err() when the transport left one,
					// else the plain "connection closed").
					reason := "connection closed"
					if s.conn != nil {
						if err := s.conn.Err(); err != nil {
							reason = err.Error()
						}
					}
					t.deadReason = reason
					s.conn = nil
					if t == a.splitTab {
						// The pinned float pane is about to VANISH (clearSplit tears it
						// down). Announce it first, or a torn-out server's death is
						// silent — the reason otherwise reaches only the debug log below,
						// so the pane just disappears with no user-visible notice. Ride
						// the existing warn-line surface (jukeWarn, same one background
						// music/asset failures use); no new UI. The dead tab's chip is
						// unchanged: it still opens the disconnect dialog on activation.
						a.jukeWarn("Pinned server " + s.serverName + " disconnected: " + reason)
						a.clearSplit() // the pinned right-pane server dropped
					}
					// SCOPE-OUT (deliberate, deferred): a parked/pinned tab's death is
					// surfaced but NOT auto-reconnected. Per-tab auto-reconnect is out of
					// scope for this wave — the App-singular autoReconnect* fields track a
					// single countdown, and arming them for a parked tab would FIGHT a
					// live primary's own countdown. The recovery gap is known; recovery
					// for parked tabs needs per-tab retry state (designed elsewhere).
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

// routeBackgroundEvent applies the few events a parked tab surfaces:
// chat into its logs (+unread, callword flash), disconnects mark it
// dead. Everything else already mutated the tab's session in
// HandlePacket and re-renders from it on activation.
func (a *App) routeBackgroundEvent(t *courtTab, ev courtroom.Event) {
	s := &t.state
	switch ev.Kind {
	case courtroom.EventMessage:
		if ev.Message != nil {
			// A parked/pinned tab's own send clears on ITS server's echo too
			// (keep-until-echo — the split pane's input must not vanish when
			// that server swallows a raced send; see noteOwnICEcho).
			if s.sess != nil && ev.Message.CharID == s.sess.MyCharID {
				s.noteOwnICEcho()
			}
			fr, fc := a.friendMessage(s.serverKey, ev.Message)
			force := a.d.Prefs.ForceCharNamesOn()
			s.icLog = append(s.icLog, icEntry{text: capLogLine(icLogLine(ev.Message, force)), color: ev.Message.TextColor, friend: fr, friendColor: fc, speaker: icSpeakerName(ev.Message, force), stamp: a.icStamp()})
			if len(s.icLog) > icLogCap {
				copy(s.icLog, s.icLog[len(s.icLog)-icLogCap:])
				s.icLog = s.icLog[:icLogCap]
			}
			s.icLogSeq++
			t.unread++
			if fr {
				a.signalFriend(s.serverName, ev.Message) // alert even from a backgrounded server
			}
			a.logDetailed(s.serverName, ev.Message) // detailed transcript (opt-in)
			names := a.mentionNamesFor(s)
			a.checkCallwords(ev.Message.Message, names, isSelfName(ev.Message.CharName, names))
		}
	case courtroom.EventOOC:
		line := ev.Name + ": " + ev.Text
		if len(line) > oocLineCap {
			line = line[:oocLineCap] + "…"
		}
		s.oocLog = appendCapped(s.oocLog, line, icLogCap)
		s.oocSpeakers = appendCapped(s.oocSpeakers, ev.Name, icLogCap) // parallel: for name colours
		s.oocSeq++
		// OOC still LOGS, but by default it doesn't bump the unread badge: servers
		// post auto-messages in OOC (hourly "hydration" reminders, etc.) and a "(1)"
		// when nobody chatted is just noise. Opt in to count OOC in Settings.
		if a.d.Prefs.NotifyOnOOCOn() {
			t.unread++
		}
		if a.d.Prefs.CallwordsOOCOn() && !looksLikeAreaList(ev.Text) { // OOC callwords opt-in (default OFF); /ga roster never self-pings
			names := a.mentionNamesFor(s)
			a.checkCallwords(ev.Text, names, isSelfName(ev.Name, names))
		}
	case courtroom.EventModcall:
		// A modcall on a backgrounded server still alerts the mod (toast +
		// the tab's OOC log + unread), like the friend signal.
		s.oocLog = appendCapped(s.oocLog, "[MOD CALL] "+ev.Text, icLogCap)
		s.oocSpeakers = appendCapped(s.oocSpeakers, "", icLogCap) // system line — no name tint
		s.oocSeq++
		t.unread++
		a.signalModcall(s.serverName, ev.Text)
		a.autoClipModcall(s.serverName, s.icLog, ev.Text) // freeze IC context even on a backgrounded server
	case courtroom.EventBackground:
		a.d.Prefs.RememberServerBackground(s.serverKey, ev.Text)
		if ev.Wipe {
			// The same overlay teardown the live tab does (#23). A parked tab has no
			// Courtroom, so this is the only place its splash / testimony badge /
			// evidence popup can be cleared before the user tabs back into it.
			s.wtceName, s.testimonyOn, s.evShowImg = "", false, ""
		}
	case courtroom.EventSetPos:
		// KFO-family servers carry the pos INSIDE an area change's BN and send no SP
		// at all, so without this a parked tab keeps whatever side it had when it was
		// parked. SP had the same hole; one case closes both.
		s.sidePref = ev.Text
	case courtroom.EventMusic:
		// A DJ /play on a BACKGROUNDED tab. HandlePacket already advanced this tab's
		// s.sess.MusicTrack (the return-switch reads exactly that on activation, via the
		// promoted session), so the track identity the resume path needs is bookkept for
		// free — no extra per-tab stamp is required here (the App-level musicStartedAt is
		// only a rough seek FALLBACK for the active tab; SwappedOutSnap is the drift-free
		// primary, and the audible-switch branch below makes the return a same-track no-op
		// anyway).
		//
		// Without this case the tab's audible continuity stream (MusicAcrossTabs ON) would
		// keep playing the STALE song: the room that issues PlayMusic parks with the tab, so
		// a backgrounded change never reached the mixer. Under ON, when the single stream
		// belongs to THIS tab (musicOwnerKey — the honest owner signal set at the play
		// sites), follow the change: switch the live stream to the new track using the SAME
		// async start machinery the active-tab path bottoms out in (Audio.PlayMusic —
		// singleflight-collapsed fetch, no new goroutine). A ~stop / area-name transfer left
		// s.sess.MusicTrack "" / unchanged; only a real track switches. Under OFF (stream
		// silent) or when another tab owns the stream, this is bookkeeping only — no audible
		// change (the isolation the OFF toggle promises).
		if url, ok := a.backgroundMusicFollow(s); ok {
			a.d.Audio.PlayMusic(url, ev.Loop, ev.MusicEffects) // AssetType: Music
		}
	case courtroom.EventDisconnect:
		t.dead = true
		t.deadReason = ev.Text // "Kicked: …" / "Banned: …" — the dialog names it on activation
		s.oocLog = appendCapped(s.oocLog, "SERVER: disconnected: "+ev.Text, icLogCap)
		s.oocSpeakers = appendCapped(s.oocSpeakers, "", icLogCap) // system line
		s.oocSeq++
	}
}

// backgroundMusicFollow is the pure decision for the ARM-2 background song-change
// (unit-pinnable like settleAwaitTimeout / mixChannels): should the single audible
// stream follow a backgrounded tab's DJ /play, and to which URL? It returns a non-empty
// url + true ONLY when ALL hold: MusicAcrossTabs is ON (OFF keeps tabs acoustically
// isolated — bookkeeping only), a device exists, the stream's owner IS this backgrounded
// tab (musicOwnerKey — so the change follows the tab that is actually audible, never
// hijacks a DIFFERENT tab's stream), and the tab really has a track (a ~stop / area-name
// transfer leaves s.sess.MusicTrack ""/unchanged, so it must not switch). The URL is
// built from the tab's OWN URLBuilder (its server's format/casing), matching how its
// active-tab room would have played it. Reusing the existing Audio.PlayMusic start
// machinery (no new goroutine, singleflight-collapsed fetch) is the caller's job.
func (a *App) backgroundMusicFollow(s *sessionState) (url string, ok bool) {
	if !a.d.Prefs.MusicAcrossTabsOn() || a.d.Audio == nil {
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

// --- tab bar (docked strip, drawn over every screen) ------------------------

// tabBarRects computes the chip rects for the current frame plus the "+"
// new-session chip (add.W == 0 when at the tab cap). The strip DOCKS under the menu
// bar in the top chrome band (chrometop.go), window-centred, and every screen keeps
// its content below that band — so it can no longer sit on top of anything (#14). It
// is still a move-only "tabbar" slot, so a layout-editor drag lifts it out of the
// band, and a theme that ships "asyncao_tabbar" parks it inside its design canvas.
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
	showAdd := len(a.tabs) < a.d.Prefs.TabCap() // no "+" once every slot is full
	if showAdd {
		total += addTabChipW + tabChipGapX
	}
	// Default origin: DOCKED — window-centred, directly under the menu bar, in the
	// band chrometop.go reserves for it. The whole strip is a MOVE-ONLY layout slot,
	// so an Edit Layout drag still lifts it anywhere; un-edited it uses this default.
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

// tabStripDefaultX centres the server-tab strip in the WINDOW — issue #14's "it
// doesn't even center itself".
//
// It used to centre in the gap LEFT of the docked log column instead (issue #2: the
// strip floated over the stage and landed ON the Log/Music/Areas tabs), which is why
// the reporter's screenshot shows it parked well off to one side. That constraint is
// gone because the reason for it is gone: the strip now docks in its own reserved
// band (chrometop.go) and every screen — dock tabs included — starts below that band,
// so no X can make the two collide. Clamped to the left edge so a strip wider than
// the window still starts on-screen. Pure + alloc-free, so the centring is
// unit-pinnable.
func tabStripDefaultX(total, w int32) int32 {
	x := (w - total) / 2
	if x < 0 {
		x = 0
	}
	return x
}

// tabChipW is the i-th chip's width: its (memoized) label plus the padding that
// holds the trailing ✕. Alloc-free — tabChipLabel and TextWidth are both memoized.
func (a *App) tabChipW(i int) int32 {
	return a.ctx.TextWidth(a.tabChipLabel(i)) + tabChipTextPadX
}

// tabStripTotalW is the whole strip's intrinsic width — every chip, the gaps, and
// the "+" chip when it shows. Factored out of tabBarRects so the layout cache can
// measure the strip WITHOUT building the per-chip rect slice (which allocates) and
// without calling back into tabStripOrigin (which would read the very cache being
// built).
func (a *App) tabStripTotalW() int32 {
	if len(a.tabs) == 0 {
		return 0
	}
	total := int32(0)
	for i := range a.tabs {
		total += a.tabChipW(i) + tabChipGapX
	}
	if len(a.tabs) < a.d.Prefs.TabCap() {
		total += addTabChipW + tabChipGapX
	}
	return total
}

// tabStripDockedRect is the strip's HOME: window-centred, directly under the menu
// bar, inside the band chrometop.go reserves for it (#14). `total` is the strip's
// intrinsic width.
func (a *App) tabStripDockedRect(total, w int32) sdl.Rect {
	return sdl.Rect{X: tabStripDefaultX(total, w), Y: a.menuBarHeight(), W: total, H: tabBarH}
}

// seedTabBarDesignRect inserts a DEFAULT "asyncao_tabbar" design rect into a freshly
// loaded theme layout when the theme itself is silent about the key, and reports
// whether it had to. Call it on the PRISTINE map (the one that becomes
// App.themeRectsOrig) and BEFORE the override overlay, so all four consumers line up:
//
//   - themeLayoutIn ranges the design map, so the key finally enters the layout cache;
//   - the themed editor builds its editable key set from that cache, so the strip
//     finally gets a drag box — issue #14's undraggable strip, which was undraggable
//     under every real theme, none of which declares the key;
//   - applyRectOverrides only rewrites keys that ALREADY EXIST, so without the seed a
//     dragged position could never be re-applied after a theme reload;
//   - right-click reset and the editor's "Reset all" both restore from the pristine
//     map, so the seed is what they put the strip back to.
//
// GEOMETRY. Centred at the TOP of the canvas, which is where a strip of server tabs
// belongs on one. Only X/Y are ever consumed, and only once the strip is PARKED; W/H
// are inert (tabBarDesignSeedW). The X/Y are canvas-relative CLIENT px like every
// other value stored under this key (see themeTabBarKey), so "centred" here means
// centred at scale 1 — which is the shipped default fit, and in any case this seed's
// coordinates are overwritten by the first drag. In practice even X/Y are rarely read —
// parking happens through a drag, which overwrites them — so the seeded rect's real
// jobs are to put the key in the map (the editor's editable set and applyRectOverrides
// are both built by ranging it) and to give right-click reset / "Reset all" something
// to restore to.
//
// The seeded default does NOT move the strip on its own — tabStripThemeParked keeps
// it in the client chrome band until the theme declares the key or the user drags it.
// The band is above the canvas and has no design-space coordinates, so it cannot be
// expressed here; what CAN be guaranteed is that the box the editor hands out sits on
// the painted strip (tabStripCacheRect substitutes the live docked rect while
// unparked).
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

// tabStripThemeParked reports whether the THEME's design layout owns where the strip
// sits, as opposed to the client chrome band it docks in by default.
//
// Parked-ness is RECORDED, never inferred from geometry. It used to be "the live
// design rect differs from the pristine seed", and that quietly threw away any drag
// that happened to land on the seed's own coordinates: the override was written, the
// comparison then said "unchanged", and the strip snapped back into the chrome band as
// if the user had never moved it. Two EXPLICIT sources answer it instead:
//
//   - the theme DECLARED asyncao_tabbar (tabBarSeeded is false): that rect is the
//     theme author's own placement, so it is parked from the moment the theme loads;
//   - a per-key rect override is PERSISTED for the active theme. The editor writes one
//     on drag release and both reset paths delete it, so the override's presence IS
//     the placement flag — recorded with the placement it belongs to, which is why a
//     theme reload cannot disagree with it.
//
// Those two are the RECORDED placement — tabStripPlacementRecorded below, which is
// what a caller asking "where does this strip belong" (as opposed to "where is it
// being drawn this instant") must use.
//
// Plus one IN-FLIGHT arm: a themed-editor drag that has grabbed the strip is a
// placement in progress. Nothing is persisted until the release (a press that never
// moves must leave the strip docked, like every other widget), so without this arm the
// live design rect the drag is rewriting would be ignored for the whole gesture and the
// strip would sit frozen in the band until the mouse came up.
//
// Cheap enough for the draw path: the recorded ladder short-circuits first, so an
// ordinary frame still costs one map probe on latched state plus two RLock'd prefs
// probes and no allocation (that is what HasThemeRectOverride is for). Only a frame
// with a strip drag in flight pays the extra map probe.
func (a *App) tabStripThemeParked() bool {
	if a.tabStripPlacementRecorded() {
		return true
	}
	if !a.layoutEdit || a.editDrag == 0 || a.editKey != themeTabBarKey {
		return false
	}
	_, present := a.themeRects[themeTabBarKey]
	return present // a placement is happening right now
}

// tabStripPlacementRecorded is tabStripThemeParked WITHOUT the in-flight arm: the
// placement as it is RECORDED right now, in the two places that outlive a frame — the
// theme's own declaration and the persisted per-key override.
//
// The distinction exists because the two questions have different answers exactly when
// a drag is in flight, and one caller needs the recorded one: layoutSnapshotNow. An
// undo step's job is to describe state that can be WRITTEN BACK, and restoreLayout
// writes this flag straight to the persisted override — so a snapshot must carry what
// was recorded, never a transient "the user's hand is on it".
//
// Nothing but ordering made that visible: drawLayoutEditor assigns editKey/editDrag and
// only THEN pushes the pre-press snapshot, so the in-flight arm was already true when
// the state it was supposed to describe was still "docked, un-parked". The first drag's
// undo therefore re-placed the strip at its synthesized seed instead of sending it home
// to the chrome band. Fixed HERE rather than by moving the push above the assignment:
// the ordering fix would have to be re-established at every future pushLayoutUndo call
// site, invisibly, whereas the accessor states which question the snapshot is asking at
// the place the answer is defined.
func (a *App) tabStripPlacementRecorded() bool {
	if _, present := a.themeRects[themeTabBarKey]; !present {
		return false // no key at all ⇒ no themed home
	}
	if !a.tabBarSeeded {
		return true // the theme author placed it
	}
	name, _ := a.d.Prefs.Theme()
	return a.d.Prefs.HasThemeRectOverride(name, themeTabBarKey)
}

// tabStripIntrinsicW is the strip's chip-driven width for the layout cache: the same
// number tabBarRects lays the chips out with, with the headless guard the cache needs
// (themeLayoutIn runs in unit fixtures and export paths that may not have built a Ctx).
// Alloc-free — tabChipLabel and TextWidth are both memoized.
func (a *App) tabStripIntrinsicW() int32 {
	if a.ctx == nil || len(a.tabs) == 0 {
		return 0
	}
	return a.tabStripTotalW()
}

// tabStripCacheRect is the strip's entry in the themed layout cache: THE LIVE PAINTED
// RECT, parked or not.
//
// The strip is the one key in the cache that is not a plain transform of a design
// rect, because it is intrinsically sized CLIENT chrome:
//
//   - its W/H come from the chips, never from the stored rect — the same reason the
//     classic layout system registers the same widget move-only
//     (slotResizeEdges(slotTabBar) == 0);
//   - while nobody has parked it its home is the reserved band ABOVE the canvas, a
//     place design coordinates cannot name, so the docked rect stands in.
//
// Everything the editor does to this key reads this one rect — the drag box, the
// Tab-cycle stacking order (sorted by box AREA), the resize-grip probe, right-click
// reset — so deriving it here, live, is what keeps box == widget. The window clamp is
// applied HERE rather than in tabStripOrigin so the box and the paint clamp the same
// way and can never disagree.
//
// PLACEMENT UNITS. A parked strip's stored X/Y are CANVAS-RELATIVE CLIENT px — the
// offset from the canvas origin in the SAME pixels the strip is drawn in — not design
// px scaled by the layout. Three reasons, in order of weight:
//
//   - It makes the forward map (below) and its inverse (editBaseFor) an EXACT pair by
//     construction: one addition and one subtraction, no rounding on either side. A
//     scaled map is not invertible over integers once the scale exceeds 1 — its image
//     has gaps — so at Letterbox 1000x900 (scale 1.4006) a one-pixel drag was simply
//     not expressible, and the editor's box drifted a constant −2 px from the cursor
//     for the whole gesture. Client chrome the user is placing by hand has to land
//     where the hand put it.
//   - The strip's SIZE is already unscaled: its width comes from the chips and its
//     height is tabBarH, both client px. Scaling only the origin was half a transform.
//   - No shipped theme declares asyncao_tabbar (the 74-theme reference corpus has
//     zero), and at the shipped default fit — Native, scale exactly 1 — the two
//     readings are byte-identical anyway. A theme that does declare it gets its rect
//     read as an offset from the canvas origin, which is what it looks like it means.
//
// Takes the half-built cache, the already-measured width and the already-resolved
// parked flag explicitly: it runs inside themeLayoutIn before lay.valid, so it must
// neither recurse into the rebuild nor re-measure or re-probe what the caller has.
func (a *App) tabStripCacheRect(lay *themeLayoutCache, w, h, total int32, parked bool) (sdl.Rect, bool) {
	if total < minThemedElementPx {
		return sdl.Rect{}, false // no chips (or a degenerate strip): nothing to place
	}
	def := a.tabStripDockedRect(total, w)
	if !parked {
		// Unparked: whatever tabStripOrigin's fallback resolves, arm for arm — the
		// classic slot override when the user moved the strip in the DEFAULT-layout
		// editor (that override outlives a switch TO a theme, and it also zeroes the
		// chrome band, so the strip really is floating there), else the docked home.
		// Only the origin survives: the slot's persisted W/H are window fractions and
		// the strip is move-only, so its size is always the chips'.
		a.ensureClassicOv()
		r := a.slotRect(slotTabBar, def, w, h)
		r.W, r.H = def.W, def.H
		return r, true
	}
	// THE FORWARD MAP. Its exact inverse is editBaseFor (layoutedit.go) and its clamp
	// twin is clampTabStripScreen; all three must keep the same form and the same
	// order of operations or the grab point drifts again.
	r := def
	d := a.themeRects[themeTabBarKey]
	r.X = clampI32(lay.offX+int32(d.X), 0, maxI32(w-r.W, 0))
	r.Y = clampI32(lay.offY+int32(d.Y), 0, maxI32(h-r.H, 0))
	return r, true
}

// tabStripOrigin resolves the strip's top-left for this frame: a theme's
// "asyncao_tabbar" rect first, then a layout-editor override, else the docked
// default.
//
// The theme arm mirrors compactToolboxStripRect's asyncao_toolbox handling exactly,
// including WHY only X/Y are taken: the strip is move-only (slotResizeEdges), its
// width is chip-driven, so honouring a design W/H would smear it. The cache entry is
// already the live painted rect — origin transformed, chip-sized, window-clamped
// (tabStripCacheRect) — so this takes its X/Y verbatim rather than re-clamping against
// a second, possibly different width. Re-deriving the clamp here is exactly how the
// editor's box and the painted strip used to drift apart.
//
// It reads the layout cache directly rather than a latched flag because — unlike the
// toolbox — this strip paints on EVERY screen, and a flag armed inside
// drawCourtroomThemed would still be set on the next lobby frame and teleport the strip
// into a courtroom rect. The screen/room/theater gate makes the answer false everywhere
// the themed courtroom is not what is on screen.
func (a *App) tabStripOrigin(def sdl.Rect, w, h int32) sdl.Rect {
	if tr, ok := a.tabStripThemeRect(); ok {
		cur := def
		cur.X, cur.Y = tr.X, tr.Y
		return cur
	}
	return a.slotRect(slotTabBar, def, w, h)
}

// tabStripThemeRect returns the active theme's home for the strip, when the themed
// courtroom is the thing actually on screen.
//
// The gate excludes gif export, replay and the scene maker as well as theater mode:
// all three PREEMPT the screen dispatch while leaving a.screen == Courtroom and
// a.room non-nil, and — worse — they rebuild the SHARED layout cache at the export
// frame's own dimensions with no chrome band (themeLayout vs themeWindowLayout). The
// strip would read export-space geometry and teleport for the whole export.
func (a *App) tabStripThemeRect() (sdl.Rect, bool) {
	if a.screen != ScreenCourtroom || a.room == nil || a.theaterOn || a.screenDispatchPreempted() {
		return sdl.Rect{}, false
	}
	if !a.themeLay.valid || !a.d.Prefs.ThemeLayoutEnabled() {
		return sdl.Rect{}, false
	}
	if !a.tabStripThemeParked() {
		// The key exists (seedTabBarDesignRect guarantees it) but nobody has placed it:
		// the strip belongs in the chrome band, so hand the decision back to the classic
		// slot override / docked default rather than to the synthesized rect.
		return sdl.Rect{}, false
	}
	return a.themeLay.rect(themeTabBarKey)
}

// buildTabChipLabel formats a chip's text from its key: the (clipped) name,
// with " ✕" when dead or " (N)" when a backgrounded tab has unread. Pure — the
// memoized tabChipLabel calls it only when the key changes.
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

// tabChipLabel is "name (unread)" with the name clipped — memoized, because the
// always-on tab strip asks for every chip several times per frame (sizing in
// tabBarRects from both handleTabBar and drawTabBar, plus the draw itself).
func (a *App) tabChipLabel(i int) string {
	t := a.tabs[i]
	key := tabChipKey{name: a.tabName(i), active: i == a.activeTab, dead: t.dead, unread: t.unread}
	if t.chipLabel == "" || t.chipKey != key {
		t.chipLabel = buildTabChipLabel(key)
		t.chipKey = key
	}
	return t.chipLabel
}

// handleTabBar consumes clicks on the strip BEFORE the screens draw, so
// chips can never double-act with widgets underneath; drawTabBar paints
// the same rects after the screens (so chips stack on top visually).
func (a *App) handleTabBar(w, h int32) {
	c := a.ctx
	rects, add := a.tabBarRects(w, h) // also registers the strip's slot for the editor
	// While the Edit Layout editor is open it OWNS the strip (drag the whole slot to
	// move it); don't also switch / park-to-lobby / close on the same press. The
	// tabBarRects call above already registered the slot, so the editor can grab it.
	if a.classicEdit {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	// The tab-close confirm modal OVERLAYS this very strip, and handleTabBar runs
	// BEFORE the frame-level pointer fence is applied (it consumes clicks pre-screen
	// at Frame's top) — so the fence alone can't stop a click from leaking through to
	// the strip that spawned the modal (re-arming a new pending close, or "+"-parking
	// the modal away). Guard the strip inert here while our own modal is up. (The
	// Disconnect/Quit/hide-sprite confirms don't originate from the strip, so they
	// keep the strip's pre-existing pass-through — this fixes only the modal born on
	// it.) Keep drag state cleared so a release later doesn't act on a stale grab.
	if a.pendingCloseTab != nil {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	// The involuntary-disconnect dialog freezes a tab whose network session is dead
	// but whose sessionState is STILL LIVE (conn nil, sess/room kept for viewing).
	// handleTabBar runs in the update phase, BEFORE the frame-tail pointer fence that
	// covers the dialog — so without this guard a chip click here would activateTab
	// away, parkActive the frozen (not-dead) session into a slot that pumpBackgroundTabs
	// never drains (s.conn==nil) — a zombie tab. The dialog itself parks with the tab
	// (it's on sessionState), so it wouldn't paint over the new tab; the hazard is the
	// zombie slot plus the wipe-and-resurface-stale-modal race resetSessionState opens.
	// So the strip is inert while the dialog owns the frame: the user resolves it
	// (Reconnect / Back to lobby / Esc) first, THEN switches tabs. This is one of TWO
	// primary-session-switch surfaces gated on disconnectDlg.open; the other is the
	// pinned-client control-swap consume (app.go). (Unlike pendingCloseTab above, this
	// modal isn't born on the strip; it's gated for state-safety, not click-leak.) Drag
	// state cleared so a later release can't act on a stale grab.
	if a.disconnectDlg.open {
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
	// Ctrl+click a chip cycles its colour-coding (#22) — consumes the click so it doesn't switch.
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
		// "+" — open another server: park the active session (it keeps
		// running in the background) and show the lobby, where connecting
		// opens the new tab. The explicit, discoverable form of the
		// active-chip-park gesture.
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
		// Right third of a chip = close; rest = switch.
		if c.mouseX > r.X+r.W-16 && i != a.activeTab {
			a.requestCloseTab(i) // manual click confirms first (easy to fat-finger) — internal teardowns still call closeParkedTab direct
		} else if i == a.activeTab {
			// Clicking the active chip parks it and shows the lobby —
			// the "browse while connected" affordance.
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

// tabTearingOff reports that the in-progress chip drag has been pulled below the
// strip while holding a BACKGROUND tab — the gesture that pops it out as a
// floating client window instead of reordering. The active tab can't tear off
// (it's the primary client already), so it stays in plain reorder mode.
func (a *App) tabTearingOff() bool {
	return a.tabDragging && a.tabDragFrom >= 0 && a.tabDragFrom != a.activeTab &&
		a.ctx.mouseY > tabTearOffY
}

// handleTabDrag arms a reorder on press over a chip body, promotes it to a
// drag once the cursor passes tabDragThreshold, and reorders the strip live as
// the cursor crosses chips. Pulling a background chip below the strip switches
// to tear-off mode (no reorder); releasing there pops it out as a floating client
// window at the cursor. Returns true when a release ended a drag, so the caller
// swallows the click (a reorder/tear-off must not also switch/close the tab).
// Pressing the right ✕ third never arms — that stays a close-click target.
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
	// Tear-off drop preview: while a background chip is dragged below the strip,
	// draw a GHOST of the floating client window under the cursor so you can see
	// where it will pop out (Chrome-style), at the size it will appear. Drawn over
	// everything (the strip paints after the screens) so it reads as a drag preview.
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
		// Discoverability: hovering a chip explains it can be dragged (reorder)
		// and clicked/closed — the drag-to-reorder gesture wasn't obvious.
		if !a.tabDragging {
			hint := "Click to switch  •  drag to reorder (↓ to pop out)  •  Ctrl+click to colour  •  ✕ to close"
			if i == a.activeTab {
				hint = "Drag to reorder  •  click to browse the lobby"
			}
			c.Tooltip(r, hint)
		}
	}
	// "+" new-session chip: accent-bordered so it reads as an action, with a
	// hint spelling out what it does (multi-server wasn't discoverable).
	if add.W > 0 {
		c.Fill(add, sdl.Color{R: ColPanel.R, G: ColPanel.G, B: ColPanel.B, A: 235})
		c.Border(add, ColAccent)
		c.Label(add.X+9, add.Y+3, "+", ColAccent)
		c.Tooltip(add, "Open another server in a new tab")
	}
}

// loweredCache memoizes ToLower(TrimSpace(src)) — search filters run per
// frame and re-lowering the query allocated on every one of them.
type loweredCache struct{ src, out string }

func (l *loweredCache) get(src string) string {
	if l.src != src {
		l.src = src
		l.out = strings.ToLower(strings.TrimSpace(src))
	}
	return l.out
}

// resetSessionState replaces the live session with a pristine one (maps
// initialized, sentinel ids set) — used by NewApp, Disconnect, and the
// park path.
func (a *App) resetSessionState() {
	a.sessionState = sessionState{
		// Pair placement is per-session AND session-only: each tab keeps its
		// own, and a fresh session starts centered/unflipped — the old prefs
		// seeding made offsets "inexplicably saved" across client restarts
		// (playtest), so pairing state deliberately does not persist.
		pairWith:       protocol.UnpairedCharID,
		playerSort:     clampMode(a.d.Prefs.PlayerListSortMode(), playerSortModes), // remembered Players-tab sorts
		playerAreaSort: clampMode(a.d.Prefs.PlayerListAreaSortMode(), areaSortModes),
		// OOC name is per-tab (typed names leaked across tabs from App);
		// fresh tabs start from the saved default.
		oocName: a.defaultOOCName(),
		// The volume-view toggles are per-session UI but PERSISTED prefs:
		// NewApp restored them once and every connect wiped them back to
		// hidden (playtest: the show/hide volume option did not persist).
		// Seed them on every fresh session instead.
		volStripOn:   a.d.Prefs.VolStripShownOn(),
		musicVolMode: a.d.Prefs.MusicVolModeOn(),
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
		// "Pre" defaults ON so a fresh join reproduces the historical
		// always-play-the-emote's-preanim behavior; selectEmote then
		// auto-follows each subsequent emote pick (AO2-Client ui_pre).
		icPreanim: true,
		// AO2 seeds char select's "Taken" filter CHECKED on every join
		// (charselect.cpp:99) — the roster shows everyone, taken or not — and never
		// persists it. Seeded here for the same reason every other session toggle is:
		// the zero value would silently hide half the character list on a fresh join.
		charShowTaken: true,
		charListSel:   -1, // no name-column selection yet
	}
	// The IC/OOC log text selection lives on App (not sessionState) but is anchored
	// into the ACTIVE log's wrapped lines — leaving it set across a session change
	// (park/disconnect/connect) would highlight stale lines in a different tab's log.
	a.logSelActive, a.logSelDragging = false, false
	// Bump the log-view epoch so the shared IC/OOC wrap+filter caches can't serve
	// the outgoing session's wrapped/coloured lines to the incoming one: their keys
	// carry the per-session log seq, which both start at 0 in every tab, so without
	// a fresh epoch a matching seq would be a cross-tab false hit (the log-crossover
	// bug). Bumped on every session swap (park/disconnect/new); activateTab bumps
	// again when it installs a restored session so its view is distinct from its
	// previous incarnation too.
	a.logViewEpoch++
	// Same for the field undo histories (Ctx, keyed by field ID): a fresh
	// session must not inherit — or Ctrl+Z back into — another session's drafts.
	if a.ctx != nil {
		a.ctx.ClearFieldHistories()
	}
}
