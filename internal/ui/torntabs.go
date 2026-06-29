package ui

// Tear-off log tabs. The right-column tabs (Music, Areas, Players, Notes,
// Friends) can be popped OUT of the docked tab strip into their own movable,
// resizable floating panels — each rendering the tab's real content. A torn tab
// is just a classic-layout SLOT (classiclayout.go) under a "tab:<name>" key:
// presence in classicOv means "torn off, and here's where". That reuse buys drag,
// 8-handle resize, right-click-to-redock and cross-session persistence for free —
// the generic drawClassicEditor already does all of it over any registered slot.
//
// Reset-all (the editor's "Reset all") clears the whole map, so every tab redocks
// and the default is plain buttons again — exactly the user's "by default it's
// buttons" ask. Log and OOC are deliberately NOT tear-offable: Log is the home /
// fallback tab, and OOC is already its own box in the new default.

import "github.com/veandco/go-sdl2/sdl"

const (
	// tornTabHeaderH is a floating panel's title strip (drag handle in the editor).
	tornTabHeaderH = int32(22)
	// tornTabDefaultW/H size a freshly popped-out panel before the user resizes it.
	tornTabDefaultW = int32(320)
	tornTabDefaultH = int32(320)
	// tornTabCascade offsets successive panels so they don't stack exactly.
	tornTabCascade = int32(26)
)

// dockTab is one entry in the docked tab strip (id + button label). Kept here so
// dockedLogTabs (the 0-alloc strip builder) is unit-testable without SDL.
type dockTab struct {
	id    int
	label string
}

// tornTabTable is the fixed set of tear-offable tabs, in strip order. A package
// array (built once at init) so iterating it on the render path allocates nothing.
// The keys are string literals — slotRect must never format a key per frame.
var tornTabTable = [...]struct {
	id    int
	key   string
	title string
}{
	{logTabMusic, "tab:music", "Music"},
	{logTabAreas, "tab:areas", "Areas"},
	{logTabPlayers, "tab:players", "Players"},
	{logTabNotes, "tab:notes", "Notes"},
	{logTabFriends, "tab:friends", "Friends"},
}

// tornKeyFor returns the classicOv key for a tear-offable tab id, or "" if the tab
// can't be torn off (Log / OOC).
func tornKeyFor(id int) string {
	for i := range tornTabTable {
		if tornTabTable[i].id == id {
			return tornTabTable[i].key
		}
	}
	return ""
}

// tornTabIndex returns a tab's position in the table (for the cascade default), or 0.
func tornTabIndex(id int) int {
	for i := range tornTabTable {
		if tornTabTable[i].id == id {
			return i
		}
	}
	return 0
}

// tabTorn reports whether tab id is currently torn out into a floating panel.
// A nil/empty classicOv (the common case) makes this a cheap, alloc-free miss.
func (a *App) tabTorn(id int) bool {
	k := tornKeyFor(id)
	if k == "" {
		return false
	}
	_, ok := a.classicOv[k]
	return ok
}

// tabHideID maps a log tab to its hide pref id, or "" for the always-present
// Log / OOC tabs (which aren't individually hideable here).
func tabHideID(tab int) string {
	switch tab {
	case logTabMusic:
		return panelTabMusic
	case logTabAreas:
		return panelTabAreas
	case logTabPlayers:
		return panelTabPlayers
	case logTabNotes:
		return panelTabNotes
	case logTabFriends:
		return panelTabFriends
	}
	return ""
}

// tabHidden reports whether the user fully hid a right-column tab (UI… popup):
// gone from the docked strip AND not drawn even if it was torn out.
func (a *App) tabHidden(tab int) bool {
	id := tabHideID(tab)
	return id != "" && a.panelHidden(id)
}

// dockedLogTabs builds the docked tab strip, skipping any tab torn into a floating
// panel OR fully hidden. Returns a fixed-size array (by value → stack, 0-alloc) and
// the live count. Log is always present (the home/fallback tab); OOC appears only
// when it's a tab (oocAsTab = the Legacy theme, or the opt-in "OOC in the log tab").
func (a *App) dockedLogTabs(oocAsTab bool) ([7]dockTab, int32) {
	var d [7]dockTab
	n := int32(0)
	d[n] = dockTab{logTabLog, "Log"} // never tear-offable
	n++
	if !a.tabTorn(logTabMusic) && !a.tabHidden(logTabMusic) {
		d[n] = dockTab{logTabMusic, "Music"}
		n++
	}
	if !a.tabTorn(logTabAreas) && !a.tabHidden(logTabAreas) {
		d[n] = dockTab{logTabAreas, "Areas"}
		n++
	}
	if !a.tabTorn(logTabPlayers) && !a.tabHidden(logTabPlayers) {
		d[n] = dockTab{logTabPlayers, "Players"}
		n++
	}
	if oocAsTab { // OOC in the strip (Legacy or the opt-in toggle); not tear-offable
		d[n] = dockTab{logTabOOC, "OOC"}
		n++
	}
	if !a.tabTorn(logTabNotes) && !a.tabHidden(logTabNotes) {
		d[n] = dockTab{logTabNotes, "Notes"}
		n++
	}
	if !a.tabTorn(logTabFriends) && !a.tabHidden(logTabFriends) {
		d[n] = dockTab{logTabFriends, "Friends"}
		n++
	}
	return d, n
}

// drawTabContent renders a tab's body into rect — the exact renderers the docked
// tab strip uses, so a torn panel and a docked tab show identical content.
func (a *App) drawTabContent(id int, inner sdl.Rect) {
	switch id {
	case logTabMusic:
		a.drawMusicList(inner)
	case logTabAreas:
		a.drawAreaList(inner)
	case logTabPlayers:
		a.drawPlayerList(inner)
	case logTabNotes:
		a.drawNotesTab(inner)
	case logTabFriends:
		a.drawFriendsTab(inner)
	}
}

// tornTabRect returns a torn panel's screen rect (override → px), or ok=false when
// the tab isn't torn. Pure (no slotReg write) so the pointer-fence pass can call it.
func (a *App) tornTabRect(key string, w, h int32) (sdl.Rect, bool) {
	ov, ok := a.classicOv[key]
	if !ok {
		return sdl.Rect{}, false
	}
	return fracToRect(ov, w, h), true
}

// tornTabDefaultRect is where a tab lands the first time it's popped out: a
// cascade near the upper-middle, clamped on-screen below the editor banner.
func (a *App) tornTabDefaultRect(i int, w, h int32) sdl.Rect {
	dw, dh := tornTabDefaultW, tornTabDefaultH
	if dw > w-16 {
		dw = w - 16
	}
	if dh > h-16 {
		dh = h - 16
	}
	x := (w-dw)/2 + int32(i)*tornTabCascade
	y := h/5 + int32(i)*tornTabCascade
	if x+dw > w-8 {
		x = w - 8 - dw
	}
	if x < 8 {
		x = 8
	}
	if y+dh > h-8 {
		y = h - 8 - dh
	}
	if y < classicBannerH+4 {
		y = classicBannerH + 4
	}
	return sdl.Rect{X: x, Y: y, W: dw, H: dh}
}

// tearOffTab pops tab id out of the docked strip into a floating panel at its
// cascade default, persisting the slot (the debounced saver flushes it).
func (a *App) tearOffTab(id int, w, h int32) {
	key := tornKeyFor(id)
	if key == "" {
		return
	}
	if _, exists := a.classicOv[key]; exists {
		return // already torn
	}
	frac := rectToFrac(a.tornTabDefaultRect(tornTabIndex(id), w, h), w, h)
	if a.classicOv == nil {
		a.classicOv = make(map[string][4]float64, classicSlotRegCap)
	}
	a.classicOv[key] = frac
	a.d.Prefs.SetClassicSlot(key, frac)
}

// drawTornTabs paints every torn-off tab as its own floating panel: a titled
// header (the editor's drag handle) over the tab's real content. slotRect returns
// the override and — only while editing — registers the slot, so the editor draws
// its move/resize handles. Called post-courtroom in normal mode (interactive,
// fenced by boxFencesPointer); in the courtroom pass during edit (inert, so you
// see what you're arranging). Alloc-free when nothing is torn.
func (a *App) drawTornTabs(w, h int32) {
	if len(a.classicOv) == 0 {
		return
	}
	c := a.ctx
	for i := range tornTabTable {
		t := tornTabTable[i]
		if _, torn := a.classicOv[t.key]; !torn {
			continue
		}
		if a.tabHidden(t.id) {
			continue // fully hidden: don't draw it even though it has a torn-out override
		}
		r := a.slotRect(t.key, a.tornTabDefaultRect(i, w, h), w, h)
		if r.W < classicMinPx || r.H < classicMinPx {
			continue
		}
		c.Fill(r, ColPanel)
		c.Border(r, ColAccent)
		hdr := sdl.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: tornTabHeaderH}
		c.Fill(hdr, ColPanelHi)
		c.Label(hdr.X+7, hdr.Y+3, t.title, ColText)
		// Redock: RIGHT-CLICK the header — a deliberate gesture. The old one-click "x"
		// sat in the corner and was far too easy to hit by accident during normal play
		// (playtest: "really easy to accidentally click, I just did it"), so it's gone.
		// In edit mode the tray chip / right-click-reset handle redocking instead.
		if !a.classicEdit {
			c.TooltipAfter("redock:"+t.key, hdr, "Right-click to redock this panel")
			if c.rightClicked && c.hovering(hdr) {
				a.clearClassicSlot(t.key)
				continue // redocked — skip its body; other panels still draw
			}
		}
		inner := sdl.Rect{X: r.X + 5, Y: r.Y + tornTabHeaderH + 4, W: r.W - 10, H: r.H - tornTabHeaderH - 9}
		if inner.W > 8 && inner.H > 8 {
			a.drawTabContent(t.id, inner)
		}
	}
}

// drawClassicTabTray is the editor's tray strip (just under the banner, at the TOP):
// one chip per tear-offable tab, clicked to pop it out (or redock it), plus the OOC
// box/tab chip. Returns whether the cursor is over the tray, so the editor suppresses a
// slot-move when you press a chip. It lives at the top, not the bottom, so it doesn't
// cover bottom-anchored bars (the OOC bar / emote grid) — a tester couldn't drag the OOC
// bar to the bottom because the old bottom tray hid it and blocked the grab. Big boxes
// (stage / right column) are grabbed by their bodies, so a top strip costs them nothing.
// Edit-only.
func (a *App) drawClassicTabTray(w, h int32) bool {
	c := a.ctx
	trayY := int32(classicBannerH + 8)
	// A dark backing strip so the tray reads as chrome even if a slot's outline
	// crosses it (the slot overlay draws after this). The WHOLE strip suppresses a
	// slot-move (overTray), not just the chips — else pressing a gap would grab a
	// box parked under it.
	trayStrip := sdl.Rect{X: 0, Y: trayY - 4, W: w, H: 40}
	c.Fill(trayStrip, sdl.Color{R: 0, G: 0, B: 0, A: 205})
	c.Label(pad, trayY-2, "Pop a tab out into its own movable panel (click again to redock):", ColTierYellow)
	overTray := pointIn(c.mouseX, c.mouseY, trayStrip)
	tx := int32(pad)
	for i := range tornTabTable {
		t := tornTabTable[i]
		_, torn := a.classicOv[t.key]
		cw := c.TextWidth(t.title) + 18
		chip := sdl.Rect{X: tx, Y: trayY + 14, W: cw, H: 22}
		bg := ColPanel
		switch {
		case torn:
			bg = ColAccent // already out → highlighted
		case pointIn(c.mouseX, c.mouseY, chip):
			bg = ColPanelHi
		}
		c.Fill(chip, bg)
		c.Border(chip, ColAccent)
		c.LabelClipped(chip.X+6, chip.Y+3, chip.W-12, t.title, ColText)
		if c.clicked && pointIn(c.mouseX, c.mouseY, chip) {
			if torn {
				a.clearClassicSlot(t.key) // redock
			} else {
				a.tearOffTab(t.id, w, h)
			}
		}
		tx += cw + 6
	}
	// OOC placement chip (yellow, set apart): switch OOC between its own box (new
	// default) and a tab in the log panel (the old layout). Hidden on Legacy, where
	// OOC is always a tab — toggling there would be a no-op. Lives here because this
	// strip is "where tabs get arranged", which is what the maintainer asked for.
	if !a.d.Prefs.LegacyDevThemeOn() {
		tx += 14 // separate it from the tear-off chips
		inTab := a.d.Prefs.OOCInLogTabOn()
		label := "OOC: own box"
		if inTab {
			label = "OOC: log tab"
		}
		ow := c.TextWidth(label) + 18
		ooChip := sdl.Rect{X: tx, Y: trayY + 14, W: ow, H: 22}
		bg := ColPanel
		if inTab {
			bg = ColAccent
		} else if pointIn(c.mouseX, c.mouseY, ooChip) {
			bg = ColPanelHi
		}
		c.Fill(ooChip, bg)
		c.Border(ooChip, ColTierYellow)
		c.LabelClipped(ooChip.X+6, ooChip.Y+3, ooChip.W-12, label, ColText)
		c.Tooltip(ooChip, "OOC chat: its own box (new) or back as a tab in the log panel (the old way)")
		if c.clicked && pointIn(c.mouseX, c.mouseY, ooChip) {
			a.d.Prefs.SetOOCInLogTab(!inTab)
		}
	}
	// Group Chat panel placement chip (both themes): give the Group Chat / DMs panel a
	// fixed home in this layout you can drag here (click again to remove it). It only
	// owns WHERE the panel sits — whether it's shown stays the Extras → Group Chat toggle.
	tx += 14
	_, gcPlaced := a.classicOv[slotMessages]
	gcLabel := "Group Chat"
	gw := c.TextWidth(gcLabel) + 18
	gcChip := sdl.Rect{X: tx, Y: trayY + 14, W: gw, H: 22}
	bg := ColPanel
	if gcPlaced {
		bg = ColAccent // has a home → highlighted
	} else if pointIn(c.mouseX, c.mouseY, gcChip) {
		bg = ColPanelHi
	}
	c.Fill(gcChip, bg)
	c.Border(gcChip, ColAccent)
	c.LabelClipped(gcChip.X+6, gcChip.Y+3, gcChip.W-12, gcLabel, ColText)
	c.Tooltip(gcChip, "Give the Group Chat / DMs panel a fixed home here (then drag it). Open it with Extras → Group Chat.")
	if c.clicked && pointIn(c.mouseX, c.mouseY, gcChip) {
		if gcPlaced {
			a.clearClassicSlot(slotMessages)
		} else {
			frac := rectToFrac(a.msgSlotDefaultRect(w, h), w, h)
			if a.classicOv == nil {
				a.classicOv = make(map[string][4]float64, classicSlotRegCap)
			}
			a.classicOv[slotMessages] = frac
			a.d.Prefs.SetClassicSlot(slotMessages, frac)
		}
	}
	return overTray
}
