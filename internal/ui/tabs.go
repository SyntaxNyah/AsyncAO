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
//   - The caches keyed by per-session sequence numbers (IC filter/wrap,
//     OOC wrap, chat raster) stay App-global: a tab switch changes the
//     keys, so they self-heal as ordinary misses.
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
	// tabBarH is the floating tab strip's height.
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
	// as a floating client window at the cursor. Comfortably below the tabBarH strip
	// so a horizontal reorder never trips it; pulling the chip down is the gesture.
	tabTearOffY = tabBarH + 34
)

// courtTab is one parked server session. While a tab is ACTIVE its state
// field is zero — the live copy is App.sessionState.
type courtTab struct {
	state   sessionState
	unread  int  // IC+OOC lines landed while backgrounded
	dead    bool // connection ended while backgrounded
	inCourt bool // a room existed when parked (activation re-enters it)
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
	// A parked server falls SILENT. Music is a single looping stream, so without
	// this the parked tab's song keeps playing under the next tab — and the
	// activated tab's buildRoom only replays its OWN track (and not at all if its
	// area has no song), so you'd hear the wrong server. buildRoom re-seeds the
	// foreground tab's music on activation, so audio follows the active server.
	if a.d.Audio != nil {
		a.d.Audio.StopMusic()
	}
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
	a.warnLine = "" // warnings are per-session context
	// Field undo histories are keyed by field ID, not by tab — wipe them on a
	// switch so one session's drafts can't resurface (or read as "external
	// changes") in another (the multi-tab isolation rule).
	a.ctx.ClearFieldHistories()
	if t.dead || a.sess == nil {
		// The connection died in the background: tear the tab down fully
		// (notebook flush, slot removed) and say why on the lobby. This is a
		// deliberate teardown of an already-dead tab — background-tab drops are
		// their own concern (#46), so suppress auto-reconnect here (#1).
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
		if time.Since(s.lastPing) >= keepalivePeriod {
			s.lastPing = time.Now()
			s.sess.Ping()
		}
		for drained := 0; drained < tabPumpBudget; drained++ {
			select {
			case p, ok := <-s.conn.Incoming():
				a.uiDirty = true // unread badges / tab chips / the pinned pane change with parked-tab traffic
				if !ok {
					t.dead = true
					s.conn = nil
					if t == a.splitTab {
						a.clearSplit() // the pinned right-pane server dropped
					}
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
	case courtroom.EventDisconnect:
		t.dead = true
		s.oocLog = appendCapped(s.oocLog, "SERVER: disconnected: "+ev.Text, icLogCap)
		s.oocSpeakers = appendCapped(s.oocSpeakers, "", icLogCap) // system line
		s.oocSeq++
	}
}

// --- tab bar (floating strip, drawn over every screen) ----------------------

// tabBarRects computes the chip rects for the current frame plus the "+"
// new-session chip (add.W == 0 when at the tab cap). The strip floats over the
// stage (a move-only "tabbar" slot — see tabStripDefaultX) so no screen layout
// has to move; it used to float dead-centre, on top of the dock tabs (issue #2).
func (a *App) tabBarRects(w, h int32) (rects []sdl.Rect, add sdl.Rect) {
	if len(a.tabs) == 0 {
		return nil, sdl.Rect{}
	}
	c := a.ctx
	rects = make([]sdl.Rect, len(a.tabs))
	total := int32(0)
	for i := range a.tabs {
		rects[i].W = c.TextWidth(a.tabChipLabel(i)) + 28 // label + ✕ pad
		total += rects[i].W + 4
	}
	showAdd := len(a.tabs) < a.d.Prefs.TabCap() // no "+" once every slot is full
	if showAdd {
		total += addTabChipW + 4
	}
	// Default origin: centred in the space LEFT of the dock tabs (over the stage), so
	// the strip no longer sits ON the Log/Music/Areas tabs (issue #2). The whole strip
	// is a MOVE-ONLY layout slot — drag it anywhere in the Edit Layout editor; un-edited
	// it uses this default. ensureClassicOv loads the override map slotRect reads.
	a.ensureClassicOv()
	defX := tabStripDefaultX(total, a.dockLeftX, w)
	origin := a.slotRect(slotTabBar, sdl.Rect{X: defX, Y: 0, W: total, H: tabBarH}, w, h)
	x, y := origin.X, origin.Y
	for i := range rects {
		rects[i].X, rects[i].Y, rects[i].H = x, y, tabBarH
		x += rects[i].W + 4
	}
	if showAdd {
		add = sdl.Rect{X: x, Y: y, W: addTabChipW, H: tabBarH}
	}
	return rects, add
}

// tabStripDefaultX centres the server-tab strip in the gap LEFT of the dock tabs
// (dockLeftX = the docked log strip's left edge), so its default no longer overlaps
// the Log/Music/Areas tabs (issue #2). dockLeftX<=0 (pre-courtroom) or a hidden log
// (dockLeftX>=w) falls back to the original window-centre. Clamped to the left edge so
// a very narrow stage can't push it off-screen — it stays clear of the dock tabs (the
// actual bug) even then. Pure + alloc-free so the non-overlap invariant is unit-pinnable.
func tabStripDefaultX(total, dockLeftX, w int32) int32 {
	right := dockLeftX
	if right <= 0 || right > w {
		right = w
	}
	x := (right - total) / 2
	if x < 0 {
		x = 0
	}
	return x
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
		// hidden (playtest: "the show/hide volume option still doesn't
		// persist"). Seed them on every fresh session instead.
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
	}
	// The IC/OOC log text selection lives on App (not sessionState) but is anchored
	// into the ACTIVE log's wrapped lines — leaving it set across a session change
	// (park/disconnect/connect) would highlight stale lines in a different tab's log.
	a.logSelActive, a.logSelDragging = false, false
	// Same for the field undo histories (Ctx, keyed by field ID): a fresh
	// session must not inherit — or Ctrl+Z back into — another session's drafts.
	if a.ctx != nil {
		a.ctx.ClearFieldHistories()
	}
}
