package ui

// Multi-server tabs (bounded maxTabs). Design:

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
	// drag box, which is issue #14's literal complaint. seedTabBarDesignRect closes
	themeTabBarKey = "asyncao_tabbar"
	// tabBarDesignSeedW is the width the synthesized entry is BORN with, in design px.
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
		return a.srvMeta.serverName
	}
	return a.tabs[i].state.srvMeta.serverName
}

// tabKey returns a tab's serverKey (its ws URL) — stable across park/activate, so per-tab colour
// (#22) sticks to the server, not the slot. "" for an unkeyed tab (it then never colours).
func (a *App) tabKey(i int) string {
	if i == a.activeTab {
		return a.srvMeta.serverKey
	}
	if i >= 0 && i < len(a.tabs) {
		return a.tabs[i].state.srvMeta.serverKey
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
	reaped := false
	for i := 0; i < len(a.tabs); i++ {
		if a.tabs[i].dead && a.tabs[i].state.link.conn == nil {
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
		a.conn.connErr = fmt.Sprintf("%d tabs max — close one first (click its ✕)", lim)
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
	if a.srvMeta.rehearsal {
		a.conn.deliberateClose = true // ending rehearsal mode is deliberate — never auto-reconnect (#1)
		a.Disconnect()                // rehearsal never parks (global offline gate)
		return
	}
	if a.raster.msRaster != nil {
		a.raster.msRaster.Destroy()
		a.raster.msRaster = nil
	}
	a.raster.msAnim, a.raster.msAnimFont = nil, nil // #M5: drop the parked tab's animated message too
	a.tabs[a.activeTab].inCourt = a.link.room != nil
	a.link.room = nil
	// Music CONTINUITY across a tab switch (the agreed exception to ecd6d9e's
	// log/scenery isolation): DON'T stop the single music stream when a tab goes
	// to the background. It keeps rolling — so its position is preserved perfectly
	// — but is DUCKED to volume 0 (musicTabDucked) so tabs stay acoustically
	// isolated. Switching back finds the same stream still playing this exact URL,
	// so buildRoom's resume is an idempotent no-op that just un-ducks: the track
	// picks up right where it was, in sync. If the INCOMING tab has its own song,
	// buildRoom swaps the stream (audible) and clears the duck there; the parked
	// tab's musicStartedAt lets a later return seek back near where it would be.
	a.budget.musicTabDucked = !a.d.Prefs.Load().MusicAcrossTabs
	// The outgoing tab's stream (whatever it is) is now the backgrounded one, freshly
	// (re-)ducked; any await the just-parked tab held is void — the INCOMING tab's
	// activateTab→buildRoom→resumeActiveTabMusic re-derives the correct await (or
	// none) off ITS own track. Clearing here is the switch-back-mid-await guard: a
	// stale await can't un-duck the new backgrounded context.
	a.budget.musicAwaitURL = ""
	a.budget.musicAwaitSince = time.Time{} // drop the timeout stamp with the void await
	// Clear the message duck BEFORE composing volumes. musicDucked is the ACTIVE,
	// drawn room's "a message is on stage → dip music to 35%" flag; a backgrounded
	// stream has no message on stage from the mixer's view (it's either ducked to 0
	// for isolation, or — MusicAcrossTabs ON — an audible continuity stream). If we
	// left musicDucked=true here, applyAudioVolumes below would push the cross-tab
	// stream to 35% and, because resetSessionState clears the flag WITHOUT re-applying
	// volumes and a char-select/lobby activate never re-composes them, the mixer would
	// stay stuck at 35% with nothing on stage until the next volume-affecting action.
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
	// The URL builder is PER-TAB session state and rebuildAssetOrigin is not called
	// on a tab switch, so the local-pack layer has to re-register the now-active
	// tab's origin here or the pack would only work on whichever tab connected
	// last. Deferred because this function has early returns in its dead-tab arms,
	// and it is the cheap origins-only transition: the index pointer is reused, so
	// nothing re-walks.
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
	if t.dead && a.link.sess != nil && t.inCourt {
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
	if t.dead || a.link.sess == nil {
		// Died in the background with no court to freeze (char-select, or a session
		// that never fully connected): tear the tab down fully (notebook flush, slot
		// removed) and say why on the lobby — today's exact fallback. Deliberate
		// teardown of an already-dead tab; background-tab drops are their own concern
		// (#46), so suppress auto-reconnect here (#1).
		name := a.srvMeta.serverName
		a.conn.deliberateClose = true
		a.Disconnect()
		a.conn.connErr = name + " disconnected while backgrounded"
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
	if t.state.link.conn != nil {
		t.state.link.conn.Close()
	}
	if t.state.notebook != nil {
		go func(nb *config.Notebook) { _ = nb.Flush() }(t.state.notebook)
	}
	// If the single music stream is currently DUCKED it's owned by a backgrounded
	// tab (cross-tab continuity kept it rolling silently — see parkActive). Closing
	// a background tab could be closing that owner, and we can't cheaply tell which
	// tab owns the stream, so stop it to avoid an orphaned stream leaking (rule §4).
	if a.d.Audio != nil && a.budget.musicTabDucked && a.budget.musicAwaitURL == "" {
		a.d.Audio.StopMusic()
		a.budget.musicTabDucked = false
	}
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if a.activeTab > i {
		a.activeTab--
	}
	a.reapTranscripts() // the closed tab's session is gone: release its transcript file + goroutine (F4)
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
	if a.d.Prefs.Load().InstantDisconnect || t.dead {
		a.closeParkedTab(i) // nothing to confirm — close now (unchanged behavior)
		return
	}
	a.dialogs.pendingCloseTab = t // stash the pointer; the modal revalidates it before acting
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
	t := a.dialogs.pendingCloseTab
	a.dialogs.pendingCloseTab = nil
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
		if s.link.conn == nil || s.link.sess == nil {
			continue
		}
		// Keep this parked tab's background keepalive goroutine primed (it sends the
		// CH ping off the render loop, so a backgrounded app doesn't idle-drop it).
		s.link.conn.SetKeepalive(s.link.sess.KeepalivePacket())
		for drained := 0; drained < tabPumpBudget; drained++ {
			select {
			case p, ok := <-s.link.conn.Incoming():
				a.uiDirty = true // unread badges / tab chips / the pinned pane change with parked-tab traffic
				if !ok {
					t.dead = true
					// Record WHY, for the disconnect dialog shown when the user
					// activates this dead tab. Same shape as pumpConnection's
					// closed-channel reason (conn.Err() when the transport left one,
					// else the plain "connection closed").
					reason := "connection closed"
					if s.link.conn != nil {
						if err := s.link.conn.Err(); err != nil {
							reason = err.Error()
						}
					}
					t.deadReason = reason
					s.link.conn = nil
					if t == a.splitTab {
						// The pinned float pane is about to VANISH (clearSplit tears it
						// down). Announce it first, or a torn-out server's death is
						// silent — the reason otherwise reaches only the debug log below,
						// so the pane just disappears with no user-visible notice. Ride
						// the existing warn-line surface (jukeWarn, same one background
						// music/asset failures use); no new UI. The dead tab's chip is
						// unchanged: it still opens the disconnect dialog on activation.
						a.jukeWarn("Pinned server " + s.srvMeta.serverName + " disconnected: " + reason)
						a.clearSplit() // the pinned right-pane server dropped
					}
					// SCOPE-OUT (deliberate, deferred): a parked/pinned tab's death is
					// surfaced but NOT auto-reconnected. Per-tab auto-reconnect is out of
					// scope for this wave — the App-singular autoReconnect* fields track a
					// single countdown, and arming them for a parked tab would FIGHT a
					// live primary's own countdown. The recovery gap is known; recovery
					// for parked tabs needs per-tab retry state (designed elsewhere).
					a.debug.push("tab " + s.srvMeta.serverName + ": connection closed in background")
					break
				}
				for _, ev := range s.link.sess.HandlePacket(p) {
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
			if s.link.sess != nil && ev.Message.CharID == s.link.sess.MyCharID {
				s.noteOwnICEcho()
			}
			fr, fc := a.friendMessage(s.srvMeta.serverKey, ev.Message)
			force := a.d.Prefs.Load().ForceCharNames
			// nil char.ini rung, deliberately: charMetaFor resolves against the
			// ACTIVE tab's asset origin, and a BACKGROUND tab's speaker is on a
			// different server. Answering from the wrong origin's cache would put
			// another server's showname on this line; the folder name is canon's
			// own unreadable-ini answer and is the honest degradation here.
			// One resolution of the name, used as BOTH the entry's speaker and
			// capLogLine's head marker — computing it twice would let the two drift
			// and quietly cost the strip its anchor.
			speaker := icSpeakerName(ev.Message, force, nil)
			line, _, bodyRuneStart, styles := icLogEntry(ev.Message, force, "", nil)
			s.chatlog.icLog = append(s.chatlog.icLog, icEntry{text: capLogLine(line, speaker), color: ev.Message.TextColor, friend: fr, friendColor: fc, speaker: speaker, pos: icPos(ev.Message), stamp: a.icStamp(), styles: styles, bodyRuneStart: bodyRuneStart})
			if cap := a.icLogCapFor(); cap > 0 && len(s.chatlog.icLog) > cap {
				copy(s.chatlog.icLog, s.chatlog.icLog[len(s.chatlog.icLog)-cap:])
				s.chatlog.icLog = s.chatlog.icLog[:cap]
			}
			s.chatlog.icLogSeq++
			t.unread++
			if fr {
				a.signalFriend(s.srvMeta.serverName, ev.Message) // alert even from a backgrounded server
			}
			a.logDetailed(s.srvMeta.serverName, s.srvMeta.logSession, ev.Message) // detailed transcript (opt-in), the PARKED tab's own file
			names := a.mentionNamesFor(s)
			a.checkCallwords(ev.Message.Message, names, isSelfName(ev.Message.CharName, names))
		}
	case courtroom.EventOOC:
		// Literally the same call pushOOC makes for the live tab. A parked tab writes its
		// OOC log here instead of through pushOOC (that one mutates ACTIVE-tab state), so
		// the rule has to be stated at both seams — TestEveryDisplayLaneStripsTheSidechannel
		// is what keeps the two from drifting apart again, and sharing one function rather
		// than two spellings of "strip the body" is what makes that gate meaningful.
		line := stripDisplayTail(ev.Name+": "+ev.Text, ev.Name)
		if len(line) > oocLineCap {
			line = line[:oocLineCap] + "…"
		}
		s.chatlog.oocLog = appendCapped(s.chatlog.oocLog, line, icLogCap)
		s.chatlog.oocSpeakers = appendCapped(s.chatlog.oocSpeakers, ev.Name, icLogCap) // parallel: for name colours
		s.chatlog.oocSeq++
		a.logDetailedOOC(s.srvMeta.serverName, s.srvMeta.logSession, line) // detailed transcript (opt-in), the PARKED tab's own file
		// OOC still LOGS, but by default it doesn't bump the unread badge: servers
		// post auto-messages in OOC (hourly "hydration" reminders, etc.) and a "(1)"
		// when nobody chatted is just noise. Opt in to count OOC in Settings.
		if a.d.Prefs.Load().NotifyOnOOC {
			t.unread++
		}
		if a.d.Prefs.Load().CallwordsOOC && !looksLikeAreaList(ev.Text) { // OOC callwords opt-in (default OFF); /ga roster never self-pings
			names := a.mentionNamesFor(s)
			a.checkCallwords(ev.Text, names, isSelfName(ev.Name, names))
		}
	case courtroom.EventModcall:
		// A modcall on a backgrounded server still alerts the mod (toast +
		// the tab's OOC log + unread), like the friend signal.
		modcallLine := "[MOD CALL] " + ev.Text
		s.chatlog.oocLog = appendCapped(s.chatlog.oocLog, modcallLine, icLogCap)
		s.chatlog.oocSpeakers = appendCapped(s.chatlog.oocSpeakers, "", icLogCap) // system line — no name tint
		s.chatlog.oocSeq++
		a.logDetailedOOC(s.srvMeta.serverName, s.srvMeta.logSession, modcallLine) // detailed transcript (opt-in), the PARKED tab's own file
		t.unread++
		a.signalModcall(s.srvMeta.serverName, ev.Text)
		a.autoClipModcall(s.srvMeta.serverName, s.chatlog.icLog, ev.Text) // freeze IC context even on a backgrounded server
	case courtroom.EventBackground:
		a.d.Prefs.RememberServerBackground(s.srvMeta.serverKey, ev.Text)
		if ev.Wipe {
			// The same overlay teardown the live tab does (#23). A parked tab has no
			// Courtroom, so this is the only place its splash / testimony badge /
			// evidence popup can be cleared before the user tabs back into it.
			s.courtxtra.wtceName, s.courtxtra.testimonyOn, s.evid.evShowImg = "", false, ""
		}
	case courtroom.EventSetPos:
		// KFO-family servers carry the pos INSIDE an area change's BN and send no SP
		// at all, so without this a parked tab keeps whatever side it had when it was
		// parked. SP had the same hole; one case closes both.
		s.compose.sidePref = ev.Text
	case courtroom.EventMusic:
		// A DJ /play on a BACKGROUNDED tab. HandlePacket already advanced this tab's
		// s.sess.MusicTrack (the return-switch reads exactly that on activation, via the
		// promoted session), so the track identity the resume path needs is bookkept for
		// free — no extra per-tab stamp is required here (the App-level musicStartedAt is
		// only a rough seek FALLBACK for the active tab; SwappedOutSnap is the drift-free
		// primary, and the audible-switch branch below makes the return a same-track no-op
		// anyway).
		// Without this case the tab's audible continuity stream (MusicAcrossTabs ON) would
		// keep playing the STALE song: the room that issues PlayMusic parks with the tab, so
		// a backgrounded change never reached the mixer. Under ON, when the single stream
		if url, ok := a.backgroundMusicFollow(s); ok {
			a.d.Audio.PlayMusic(url, ev.Loop, ev.MusicEffects) // AssetType: Music
		}
	case courtroom.EventDisconnect:
		t.dead = true
		t.deadReason = ev.Text // "Kicked: …" / "Banned: …" — the dialog names it on activation
		disconnectLine := "SERVER: disconnected: " + ev.Text
		s.chatlog.oocLog = appendCapped(s.chatlog.oocLog, disconnectLine, icLogCap)
		s.chatlog.oocSpeakers = appendCapped(s.chatlog.oocSpeakers, "", icLogCap) // system line
		s.chatlog.oocSeq++
		a.logDetailedOOC(s.srvMeta.serverName, s.srvMeta.logSession, disconnectLine) // detailed transcript (opt-in), the PARKED tab's own file
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
	if !a.d.Prefs.Load().MusicAcrossTabs || a.d.Audio == nil {
		return "", false
	}
	if a.budget.musicOwnerKey == "" || a.budget.musicOwnerKey != s.srvMeta.serverKey {
		return "", false // stream is silent-isolated, or owned by another tab: don't touch it
	}
	if s.link.sess == nil || s.link.sess.MusicTrack == "" {
		return "", false // ~stop / no track: nothing to switch to
	}
	return s.link.urls.MusicURL(s.link.sess.MusicTrack), true
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
	showAdd := len(a.tabs) < a.d.Prefs.Load().TabCap() // no "+" once every slot is full
	if showAdd {
		total += addTabChipW + tabChipGapX
	}
	// Default origin: DOCKED — window-centred, directly under the menu bar, in the
	// band chrometop.go reserves for it. The whole strip is a MOVE-ONLY layout slot,
	// so an Edit Layout drag still lifts it anywhere; un-edited it uses this default.
	// ensureClassicOv loads the override map slotRect reads.
	a.classic.ensureClassicOv()
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
// It used to centre in the gap LEFT of the docked log column instead (issue #2: the
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
	if len(a.tabs) < a.d.Prefs.Load().TabCap() {
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
// App.themeRectsOrig), after the AO2 backstop and BEFORE BOTH override tiers —
// the theme author's [overrides] fold and the player's applyRectOverrides.
// BOTH, because each only rewrites keys that ALREADY EXIST: seeding after the
// author's fold silently discarded an `[overrides] asyncao_tabbar` row, wrote a
// client-computed rect over it, and then reported the theme as silent about the
// The seeded default does NOT move the strip on its own — tabStripThemeParked keeps
// it in the client chrome band until the theme declares the key or the user drags it.
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

// seedTabBarThroughOverrides is the seed AND the theme author's [overrides] fold,
// in the one order that answers an `asyncao_tabbar` row exactly once. It returns
// what tabBarSeeded means: NOBODY BUT THIS CLIENT decided where the strip goes.
// The order is load-bearing in both directions, which is why the two steps are
// Comparing the rect rather than re-asking the sidecar is deliberate: it reads the
// [overrides] tier's OUTPUT, so there is no second copy of its policy here to
// disagree with foldSidecarOverrides about editability or AO2's second spelling.
func seedTabBarThroughOverrides(layout map[string]theme.Rect, sc *theme.Sidecar) bool {
	seeded := seedTabBarDesignRect(layout)
	before := layout[themeTabBarKey]
	foldSidecarOverrides(layout, sc)
	if seeded && layout[themeTabBarKey] != before {
		return false // the author DID place the strip — in the AsyncAO tier, not AO2's
	}
	return seeded
}

// tabStripThemeParked reports whether the THEME's design layout owns where the strip
// sits, as opposed to the client chrome band it docks in by default.
// Cheap enough for the draw path: the recorded ladder short-circuits first, so an
// ordinary frame still costs one map probe on latched state plus two RLock'd prefs
// probes and no allocation (that is what HasThemeRectOverride is for). Only a frame
func (a *App) tabStripThemeParked() bool {
	if a.tabStripPlacementRecorded() {
		return true
	}
	if !a.layoutEdit || a.editDrag == 0 || a.editTgt.designKey() != themeTabBarKey {
		return false
	}
	_, present := a.skin.themeRects[themeTabBarKey]
	return present // a placement is happening right now
}

// tabStripPlacementRecorded is tabStripThemeParked WITHOUT the in-flight arm: the
// placement as it is RECORDED right now, in the two places that outlive a frame — the
// theme's own declaration and the persisted per-key override.
// The distinction exists because the two questions have different answers exactly when
// a drag is in flight, and one caller needs the recorded one: layoutSnapshotNow. An
// undo step's job is to describe state that can be WRITTEN BACK, and restoreLayout
func (a *App) tabStripPlacementRecorded() bool {
	if _, present := a.skin.themeRects[themeTabBarKey]; !present {
		return false // no key at all ⇒ no themed home
	}
	if !a.tabBarSeeded {
		return true // the theme author placed it
	}
	name, _ := a.d.Prefs.Load().Theme()
	return a.d.Prefs.Load().HasThemeRectOverride(name, themeTabBarKey)
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
		a.classic.ensureClassicOv()
		r := a.classic.slotRect(slotTabBar, def, w, h)
		r.W, r.H = def.W, def.H
		return r, true
	}
	// THE FORWARD MAP. Its exact inverse is editBaseFor (layoutedit.go) and its clamp
	// twin is clampTabStripScreen; all three must keep the same form and the same
	// order of operations or the grab point drifts again.
	r := def
	d := a.skin.themeRects[themeTabBarKey]
	r.X = clampI32(lay.offX+int32(d.X), 0, maxI32(w-r.W, 0))
	r.Y = clampI32(lay.offY+int32(d.Y), 0, maxI32(h-r.H, 0))
	return r, true
}

// tabStripOrigin resolves the strip's top-left for this frame: a theme's
// "asyncao_tabbar" rect first, then a layout-editor override, else the docked
// default.
// It reads the layout cache directly rather than a latched flag because — unlike the
// toolbox — this strip paints on EVERY screen, and a flag armed inside
// drawCourtroomThemed would still be set on the next lobby frame and teleport the strip
func (a *App) tabStripOrigin(def sdl.Rect, w, h int32) sdl.Rect {
	if tr, ok := a.tabStripThemeRect(); ok {
		cur := def
		cur.X, cur.Y = tr.X, tr.Y
		return cur
	}
	return a.classic.slotRect(slotTabBar, def, w, h)
}

// tabStripThemeRect returns the active theme's home for the strip, when the themed
// courtroom is the thing actually on screen.
// The gate excludes gif export, replay and the scene maker as well as theater mode:
func (a *App) tabStripThemeRect() (sdl.Rect, bool) {
	if a.screen != ScreenCourtroom || a.link.room == nil || a.theaterOn || a.screenDispatchPreempted() {
		return sdl.Rect{}, false
	}
	if !a.skin.themeLay.valid || !a.d.Prefs.Load().ThemeLayoutOn {
		return sdl.Rect{}, false
	}
	if !a.tabStripThemeParked() {
		// The key exists (seedTabBarDesignRect guarantees it) but nobody has placed it:
		// the strip belongs in the chrome band, so hand the decision back to the classic
		// slot override / docked default rather than to the synthesized rect.
		return sdl.Rect{}, false
	}
	return a.skin.themeLay.rect(themeTabBarKey)
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
	if a.classic.classicEdit {
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
	if a.dialogs.pendingCloseTab != nil {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	// The involuntary-disconnect dialog freezes a tab whose network session is dead
	// but whose sessionState is STILL LIVE (conn nil, sess/room kept for viewing).
	if a.dialogs.disconnectDlg.open {
		a.tabDragFrom, a.tabDragging = -1, false
		return
	}
	// The notice box is the THIRD gate here and the only one that can be up with no
	// session at all. Same hazard as the dialog above for an INFO notice (a chip
	// click would activateTab → parkActive out from under a modal the box keeps open,
	// because the box is on App and does NOT park with the tab), and for a REMOVED
	// notice the strip must stay inert so the appeal text cannot be scrolled away
	// behind a tab switch before it has been read or copied.
	if a.font.serverNoticeDlg.open {
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
		pw, ph := a.wins.clientWin.w, a.wins.clientWin.h
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
	// A fresh live slot is a fresh transcript session (F4). Minted HERE — the one
	// place a pristine sessionState is built — so no caller can create a tab that
	// shares another tab's log file by forgetting to stamp it. Monotonic: park moves
	// the id into the tab with the rest of the state, so an id is never reused.
	a.nextLogSession++
	a.sessionState = sessionState{srvMeta: serverMetaState{logSession: a.nextLogSession}, pairbox: pairBoxState{pairWith: protocol.UnpairedCharID}, roster: rosterState{playerSort: clampMode(a.d.Prefs.Load().PlayerListSort, playerSortModes), playerAreaSort: clampMode(a.d.Prefs.Load().PlayerListAreaSort, areaSortModes), spriteOv: map[string][2]int{}}, compose: composeState{
		oocName: a.defaultOOCName(),
		// pmThreads keeps the Friends-tab DM history bounded per session.
		pmThreads: map[string][]pmLine{},
		// -1 = editing the live draft, not browsing history (#8); the OOC
		// recall ring uses the same convention.
		icRecallIdx:  -1,
		oocRecallIdx: -1,
		// Logs follow the tail until the user scrolls up.
		icStick:  true,
		oocStick: true,
		// "Pre" defaults ON so a fresh join reproduces the historical
		// always-play-the-emote's-preanim behavior; selectEmote then
		// auto-follows each subsequent emote pick (AO2-Client ui_pre).
		icPreanim: true,
	}, musicTab: musicTabState{volStripOn: a.d.Prefs.Load().VolStripOn, musicVolMode: a.d.Prefs.Load().MusicVolMode}, audio: audioLevelState{masterMuted: a.d.Prefs.Load().MasterVolMuted, musicMuted: a.d.Prefs.Load().MusicVolMuted, sfxMuted: false, blipMuted: a.d.Prefs.Load().BlipVolMuted}, evid: evidenceState{evidIdx: -1}, courtxtra: courtExtrasState{hpPrev: [2]int{courtroom.HPBarMax, courtroom.HPBarMax}}, charsel: charGridState{charShowTaken: true, charListSel: -1}}
	a.wireComponentEnvs()
	// The IC/OOC log text selection lives on App (not sessionState) but is anchored
	// into the ACTIVE log's wrapped lines — leaving it set across a session change
	// (park/disconnect/connect) would highlight stale lines in a different tab's log.
	a.sel.logSelActive, a.sel.logSelDragging = false, false
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
	// The session that was live a moment ago is over unless park just moved it into
	// a tab, so let go of its transcript file and writer goroutine (F4). Last, after
	// the new id is in place, so the reap's liveness question sees the state it is
	// about to be asked about; parkActive copies the outgoing state into its slot
	// BEFORE calling this, which is what keeps a backgrounded tab's log open.
	a.reapTranscripts()
}
