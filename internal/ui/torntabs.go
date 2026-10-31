package ui

// Tear-off log tabs: the right-column tabs can be popped out of the docked strip into movable, resizable floating
// panels. A torn tab is a classic-layout slot (classiclayout.go) under a "tab:<name>" key, so presence in classicOv
// means torn off and the generic drawClassicEditor supplies drag, resize, redock and persistence.
// Log and OOC are not tear-offable: Log is the fallback tab and OOC is its own box by default.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// tornTabHeaderH is a floating panel's title strip (drag handle in the editor).
	tornTabHeaderH = int32(22)
	// tornTabDefaultW/H size a freshly popped-out panel before the user resizes it.
	tornTabDefaultW = int32(320)
	tornTabDefaultH = int32(320)
	// tornTabCascade offsets successive panels so they don't stack exactly.
	tornTabCascade = int32(26)
)

// dockTab is one entry in the docked tab strip (id + button label).
type dockTab struct {
	id    int
	label string
}

// tornTabTable is the fixed set of tear-offable tabs, in strip order; an array so the render path allocates nothing.
// The keys are string literals so slotRect never formats a key per frame.
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

// tornKeyFor returns the classicOv key for a tear-offable tab id, or "" for Log / OOC.
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

// tabTorn reports whether tab id is currently torn out into a floating panel; a nil classicOv is a cheap miss.
func (a *App) tabTorn(id int) bool {
	k := tornKeyFor(id)
	if k == "" {
		return false
	}
	_, ok := a.classicOv[k]
	return ok
}

// tabHideID maps a log tab to its hide pref id, or "" for Log / OOC, which are not individually hideable.
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

// tabHidden reports whether the user fully hid a right-column tab: gone from the strip and not drawn even if torn out.
func (a *App) tabHidden(tab int) bool {
	id := tabHideID(tab)
	return id != "" && a.panelHidden(id)
}

// dockedLogTabs builds the docked tab strip, skipping torn and hidden tabs, as a fixed-size array and the live count.
// Log is always present; OOC appears only when it is a tab (oocAsTab).
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

// drawTabContent renders a tab's body into rect with the renderers the docked strip uses.
func (a *App) drawTabContent(id int, inner sdl.Rect) {
	switch id {
	case logTabMusic:
		a.drawMusicList(inner, false, false, false)
	case logTabAreas:
		a.drawAreaList(inner, false)
	case logTabPlayers:
		a.drawPlayerList(inner)
	case logTabNotes:
		a.drawNotesTab(inner)
	case logTabFriends:
		a.drawFriendsTab(inner)
	}
}

// tornTabRect returns a torn panel's screen rect, or ok=false when the tab isn't torn. It writes no slotReg, so the
// pointer-blocking pass can call it.
func (a *App) tornTabRect(key string, w, h int32) (sdl.Rect, bool) {
	ov, ok := a.classicOv[key]
	if !ok {
		return sdl.Rect{}, false
	}
	return a.anchoredRect(key, ov, w, h), true
}

// tornTabDefaultRect is where a tab first lands when popped out: a cascade below the editor banner.
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

// tearOffTab pops tab id out of the docked strip into a floating panel at its cascade default and persists the slot.
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

// tornTabOrder returns the back-to-front draw order of the tear-off tabs as tornTabTable indices; tornZ records the
// bring-to-front clicks and empty means table order. A fixed-size value so the draw loop allocates nothing.
func (a *App) tornTabOrder() [len(tornTabTable)]int {
	var order [len(tornTabTable)]int
	if len(a.tornZ) == 0 {
		for i := range tornTabTable {
			order[i] = i
		}
		return order
	}
	copy(order[:], a.tornZ)
	return order
}

// bringTornToFront promotes the topmost torn panel under a fresh left-click to the front of the z-order.
func (a *App) bringTornToFront(w, h int32) {
	c := a.ctx
	if !c.clicked {
		return
	}
	order := a.tornTabOrder()
	// Scan front-to-back: the panel already on top under the cursor wins, and
	// promoting it is a no-op when it is already front.
	for j := len(order) - 1; j >= 0; j-- {
		idx := order[j]
		t := tornTabTable[idx]
		if _, torn := a.classicOv[t.key]; !torn || a.tabHidden(t.id) {
			continue
		}
		r, ok := a.tornTabRect(t.key, w, h)
		if !ok || r.W < classicMinPx || r.H < classicMinPx {
			continue
		}
		if pointIn(c.mouseX, c.mouseY, r) {
			if j == len(order)-1 {
				return // already front
			}
			z := make([]int, 0, len(order))
			for _, o := range order {
				if o != idx {
					z = append(z, o)
				}
			}
			z = append(z, idx)
			a.tornZ = z
			return
		}
	}
}

// drawTornTabs paints every torn-off tab as a floating panel: a titled header (the editor's drag handle) over the
// tab's content. slotRect returns the override and registers the slot only while editing.
// Called post-courtroom in normal mode (blocked via boxFencesPointer); in the courtroom pass during edit (inert).
func (a *App) drawTornTabs(w, h int32) {
	if len(a.classicOv) == 0 {
		return
	}
	c := a.ctx
	a.bringTornToFront(w, h)
	for _, idx := range a.tornTabOrder() {
		t := tornTabTable[idx]
		if _, torn := a.classicOv[t.key]; !torn {
			continue
		}
		if a.tabHidden(t.id) {
			continue // fully hidden: don't draw it even though it has a torn-out override
		}
		r := a.slotRect(t.key, a.tornTabDefaultRect(idx, w, h), w, h)
		if r.W < classicMinPx || r.H < classicMinPx {
			continue
		}
		c.Fill(r, ColPanel)
		c.Border(r, ColAccent)
		hdr := sdl.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: tornTabHeaderH}
		c.Fill(hdr, ColPanelHi)
		c.Label(hdr.X+7, hdr.Y+3, t.title, ColText)
		// Redock is a right-click on the header; in edit mode the tray chip or right-click-reset does it.
		if !a.classicEdit {
			c.TooltipAfter("redock:"+t.key, hdr, "Right-click to redock this panel")
			if c.rightClicked && c.hovering(hdr) {
				a.clearClassicSlot(t.key)
				continue // redocked — skip its body; other panels still draw
			}
		}
		inner := sdl.Rect{
			X: r.X + boxBodyInsetPx,
			Y: r.Y + tornTabHeaderH + boxHeaderGapPx,
			W: r.W - 2*boxBodyInsetPx,
			H: r.H - tornTabHeaderH - boxHeaderGapPx - boxBodyInsetPx,
		}
		if inner.W > 8 && inner.H > 8 {
			a.drawTabContent(t.id, inner)
		}
	}
}

// drawClassicTabTray is the editor's tray strip under the banner: one chip per tear-offable tab to pop it out or
// redock it, plus the OOC box/tab chip. It returns whether the cursor is over the tray so a chip press doesn't
// start a slot-move. It sits at the top so it doesn't cover bottom-anchored bars. Edit-only.
func (a *App) drawClassicTabTray(w, h int32) bool {
	c := a.ctx
	trayY := int32(classicBannerH + 8)
	// A dark backing strip so the tray reads as chrome; the whole strip suppresses slot-move (overTray).
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
	// OOC placement chip: switch OOC between its own box and a log-panel tab. Hidden on Legacy (always a tab).
	if !a.d.Prefs.Load().LegacyDevTheme {
		tx += 14 // separate it from the tear-off chips
		inTab := a.d.Prefs.Load().OOCInLogTab
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
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					OOCInLogTab =

					!inTab

			})

		}
		tx += ow // advance past the OOC chip so the next chip doesn't overlap it
	}
	// Group Chat panel placement chip: gives the panel a fixed home in this layout; display stays on the Extras toggle.
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
