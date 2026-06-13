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
//     screen; activation rebuilds it via enterCourtroom.
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
	// maxTabs is the DEFAULT concurrent-session cap (rule §17.4); the live cap
	// is configurable via prefs (config.TabCap, clamped to config.maxMultiTabCap).
	// Three hosts one server and lurks two others; each costs a websocket, a
	// session reducer, and two bounded logs.
	maxTabs = 3
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
)

// courtTab is one parked server session. While a tab is ACTIVE its state
// field is zero — the live copy is App.sessionState.
type courtTab struct {
	state   sessionState
	unread  int  // IC+OOC lines landed while backgrounded
	dead    bool // connection ended while backgrounded
	inCourt bool // a room existed when parked (activation re-enters it)
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
		a.Disconnect() // rehearsal never parks (global offline gate)
		return
	}
	if a.msRaster != nil {
		a.msRaster.Destroy()
		a.msRaster = nil
	}
	a.tabs[a.activeTab].inCourt = a.room != nil
	a.room = nil
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = nil
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
	a.parkActive()
	t := a.tabs[i]
	a.sessionState = t.state
	t.state = sessionState{}
	t.unread = 0
	a.activeTab = i
	a.warnLine = "" // warnings are per-session context
	if t.dead || a.sess == nil {
		// The connection died in the background: tear the tab down fully
		// (notebook flush, slot removed) and say why on the lobby.
		name := a.serverName
		a.Disconnect()
		a.connErr = name + " disconnected while backgrounded"
		return
	}
	if t.inCourt {
		a.enterCourtroom() // rebuilds room/prefetcher from session state
	} else {
		a.screen = ScreenCharSelect
	}
	a.ensureThemeForSession() // the tab's theme binding follows it in
	a.updatePresence()
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
				if !ok {
					t.dead = true
					s.conn = nil
					a.pushDebug("tab " + s.serverName + ": connection closed in background")
					break
				}
				for _, ev := range s.sess.HandlePacket(p) {
					a.routeBackgroundEvent(t, ev)
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
			s.icLog = append(s.icLog, icEntry{text: clampLine(icLogLine(ev.Message)), color: ev.Message.TextColor})
			if len(s.icLog) > icLogCap {
				copy(s.icLog, s.icLog[len(s.icLog)-icLogCap:])
				s.icLog = s.icLog[:icLogCap]
			}
			s.icLogSeq++
			t.unread++
			a.checkCallwords(ev.Message.Message)
		}
	case courtroom.EventOOC:
		line := ev.Name + ": " + ev.Text
		if len(line) > oocLineCap {
			line = line[:oocLineCap] + "…"
		}
		s.oocLog = appendCapped(s.oocLog, line, icLogCap)
		s.oocSeq++
		t.unread++
		a.checkCallwords(ev.Text)
	case courtroom.EventBackground:
		a.d.Prefs.RememberServerBackground(s.serverKey, ev.Text)
	case courtroom.EventDisconnect:
		t.dead = true
		s.oocLog = appendCapped(s.oocLog, "SERVER: disconnected: "+ev.Text, icLogCap)
		s.oocSeq++
	}
}

// --- tab bar (floating strip, drawn over every screen) ----------------------

// tabBarRects computes the chip rects for the current frame plus the "+"
// new-session chip (add.W == 0 when at the tab cap); the strip floats
// top-center so no screen layout has to move.
func (a *App) tabBarRects(w int32) (rects []sdl.Rect, add sdl.Rect) {
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
	x := (w - total) / 2
	if x < 0 {
		x = 0
	}
	for i := range rects {
		rects[i].X, rects[i].Y, rects[i].H = x, 0, tabBarH
		x += rects[i].W + 4
	}
	if showAdd {
		add = sdl.Rect{X: x, Y: 0, W: addTabChipW, H: tabBarH}
	}
	return rects, add
}

// tabChipLabel is "name (unread)" with the name clipped.
func (a *App) tabChipLabel(i int) string {
	name := a.tabName(i)
	if name == "" {
		name = "tab"
	}
	if len(name) > tabChipMaxName {
		name = name[:tabChipMaxName] + "…"
	}
	if i != a.activeTab {
		if a.tabs[i].dead {
			return name + " ✕"
		}
		if n := a.tabs[i].unread; n > 0 {
			return fmt.Sprintf("%s (%d)", name, n)
		}
	}
	return name
}

// handleTabBar consumes clicks on the strip BEFORE the screens draw, so
// chips can never double-act with widgets underneath; drawTabBar paints
// the same rects after the screens (so chips stack on top visually).
func (a *App) handleTabBar(w int32) {
	rects, add := a.tabBarRects(w)
	if rects == nil || !a.ctx.clicked {
		return
	}
	if add.W > 0 && a.ctx.hovering(add) {
		// "+" — open another server: park the active session (it keeps
		// running in the background) and show the lobby, where connecting
		// opens the new tab. The explicit, discoverable form of the
		// active-chip-park gesture.
		a.parkActive()
		a.ensureThemeForSession()
		a.screen = ScreenLobby
		a.updatePresence()
		a.ctx.clicked = false
		return
	}
	for i, r := range rects {
		if !a.ctx.hovering(r) {
			continue
		}
		// Right third of a chip = close; rest = switch.
		if a.ctx.mouseX > r.X+r.W-16 && i != a.activeTab {
			a.closeParkedTab(i)
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
		a.ctx.clicked = false // swallowed: nothing underneath reacts
		return
	}
}

// drawTabBar paints the strip (after the screens, before overlays).
func (a *App) drawTabBar(w int32) {
	rects, add := a.tabBarRects(w)
	c := a.ctx
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
		c.Border(r, ColPanelHi)
		c.LabelClipped(r.X+6, r.Y+3, r.W-24, a.tabChipLabel(i), col)
		if i != a.activeTab {
			c.Label(r.X+r.W-14, r.Y+3, "✕", ColTextDim)
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
		pairWith: protocol.UnpairedCharID,
		spriteOv: map[string][2]int{},
		evidIdx:  -1,
		// Full bars so the first HP packets don't fire penalty sfx.
		hpPrev: [2]int{courtroom.HPBarMax, courtroom.HPBarMax},
		// Logs follow the tail until the user scrolls up.
		icStick:  true,
		oocStick: true,
	}
}
