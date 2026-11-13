package ui

// The Wardrobe's Add window: type folder names, or pick from the server's iniswaps.ini.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ui/wardrobe"
)

// wardAddIconPx is a name's initials box in the Add list.
const wardAddIconPx int32 = 28

// openWardrobeAdd opens the Add window on its server tab when asked and the server has a list.
func (a *App) openWardrobeAdd(server bool) {
	ward := &a.ward
	ward.addOpen = true
	ward.addServer = server && len(ward.serverList) > 0
	ward.addText, ward.addScroll = "", 0
	if ward.addPick == nil {
		ward.addPick = map[string]bool{}
	}
	clear(ward.addPick)
	ward.addTarget = 0
	if i := a.wardrobeFolderIndex(ward.folder); i >= 0 {
		ward.addTarget = i + 1
	}
	ward.moveMenu = false
	if !ward.addServer {
		a.ctx.FocusField("wardaddtext")
	}
}

// wardrobeAddNames is what Add would save now: the new typed names, or the ticked server names.
func (a *App) wardrobeAddNames() []string {
	ward := &a.ward
	if ward.addServer {
		var out []string
		for _, n := range ward.serverList {
			if ward.addPick[n] && !ward.store.Has(n) {
				out = append(out, n)
			}
		}
		return out
	}
	if ward.addParsedFor != ward.addText || ward.addParsed == nil {
		ward.addParsed = wardrobe.ParseNames(ward.addText, wardrobeAddLimit)
		ward.addParsedFor = ward.addText
	}
	out := make([]string, 0, len(ward.addParsed))
	for _, n := range ward.addParsed {
		if !ward.store.Has(n) {
			out = append(out, n)
		}
	}
	return out
}

// drawWardrobeAdd draws the dialog over the window.
func (a *App) drawWardrobeAdd(panel sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	c.FillRound(panel, wardRadPanel, wcOverlay)
	h := min(wardAddH, panel.H-2*pad)
	dw := min(wardAddW, panel.W-2*pad) // the dialog narrows with a small window
	d := sdl.Rect{X: panel.X + (panel.W-dw)/2, Y: panel.Y + (panel.H-h)/2, W: dw, H: h}
	c.RoundRect(d, wardRadDlg, 1, wcPanel, wcLineHi)
	head := sdl.Rect{X: d.X + 2*wardPanelPad, Y: d.Y + 2*wardPanelPad, W: d.W - 4*wardPanelPad, H: wardSegH + 2*wardOutline + wardRadSeg}
	c.LabelClippedFontWeight(c.chromeFace(), head.X, head.Y+(head.H-a.wardLineH())/2, head.W/2, "Add characters", wcText, true)
	// Close.
	closeR := sdl.Rect{X: head.X + head.W - wardBtnH + wardPanelPad/2, Y: head.Y + (head.H-wardBtnH+wardPanelPad)/2, W: wardBtnH - wardPanelPad/2, H: wardBtnH - wardPanelPad/2}
	if a.wardButton(closeR, "", wbGhost) {
		ward.addOpen = false
		return
	}
	c.Icon(iconShapeCross, sdl.Rect{X: closeR.X + (closeR.W-wardCheckIcon)/2, Y: closeR.Y + (closeR.H-wardCheckIcon)/2, W: wardCheckIcon, H: wardCheckIcon}, wcDim)
	// Tabs.
	newOnServer := 0
	for _, n := range ward.serverList {
		if !ward.store.Has(n) {
			newOnServer++
		}
	}
	srvLabel := "From server (" + strconv.Itoa(newOnServer) + " new)"
	srvW, typW := a.wardButtonW(srvLabel), a.wardButtonW("Type names")
	track := sdl.Rect{X: closeR.X - wardPanelPad - srvW - typW - 3*wardOutline, Y: head.Y, W: srvW + typW + 3*wardOutline, H: head.H}
	a.wardPillGroup(track)
	typR := sdl.Rect{X: track.X + wardOutline, Y: track.Y + (track.H-wardSegH)/2, W: typW, H: wardSegH}
	srvR := sdl.Rect{X: typR.X + typW + wardOutline, Y: typR.Y, W: srvW, H: wardSegH}
	if a.wardSegment(typR, "Type names", !ward.addServer, true) {
		ward.addServer = false
	}
	if a.wardSegment(srvR, srvLabel, ward.addServer, len(ward.serverList) > 0) {
		ward.addServer = true
	}
	if len(ward.serverList) == 0 {
		c.Tooltip(srvR, "This server publishes no iniswaps.ini")
	}
	bodyY := head.Y + head.H + wardPanelPad
	body := sdl.Rect{X: d.X + 2*wardPanelPad, Y: bodyY, W: d.W - 4*wardPanelPad, H: d.Y + d.H - wardBarH - wardPanelPad - bodyY}
	if ward.addServer {
		a.drawWardrobeAddServer(body)
	} else {
		a.drawWardrobeAddTyped(body)
	}
	a.drawWardrobeAddFooter(sdl.Rect{X: d.X + 1, Y: d.Y + d.H - wardBarH, W: d.W - 2, H: wardBarH - 1})
}

func (a *App) drawWardrobeAddTyped(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	lh := a.wardLineH()
	c.Label(r.X, r.Y, "Character folder names, one per line or separated by commas", wcSoft)
	y := r.Y + lh + wardPanelPad/2
	ward.addText = c.TextArea("wardaddtext", sdl.Rect{X: r.X, Y: y, W: r.W, H: wardAddTextH}, ward.addText, "Phoenix, Phoenix (AJ), Kristoph")
	y += wardAddTextH + wardPanelPad/2
	c.Label(r.X, y, "A folder the server has no character slot for is saved as an iniswap.", wcDim)
	y += lh + wardPanelPad
	a.wardrobeAddNames() // refreshes the parse
	a.drawWardrobeAddRows(sdl.Rect{X: r.X, Y: y, W: r.W, H: r.Y + r.H - y}, ward.addParsed, false)
}

func (a *App) drawWardrobeAddServer(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	allNew, anyNew := true, false
	for _, n := range ward.serverList {
		if !ward.store.Has(n) {
			anyNew = true
			allNew = allNew && ward.addPick[n]
		}
	}
	if next := c.Checkbox(r.X, r.Y, "Select all new", anyNew && allNew); next != (anyNew && allNew) {
		for _, n := range ward.serverList {
			if !ward.store.Has(n) {
				ward.addPick[n] = next
			}
		}
	}
	note := "From the server's iniswaps.ini"
	c.Label(r.X+r.W-c.TextWidth(note), r.Y, note, wcDim)
	y := r.Y + a.wardLineH() + wardPanelPad
	a.drawWardrobeAddRows(sdl.Rect{X: r.X, Y: y, W: r.W, H: r.Y + r.H - y}, ward.serverList, true)
}

// drawWardrobeAddRows lists names with their iniswap mark; with ticks, each new one has a checkbox.
func (a *App) drawWardrobeAddRows(r sdl.Rect, names []string, ticks bool) {
	c := a.ctx
	ward := &a.ward
	if len(names) == 0 {
		return
	}
	c.RoundRect(r, wardRadRow, 1, wcInset, wcLine)
	in := sdl.Rect{X: r.X + wardPanelPad/2, Y: r.Y + wardPanelPad/2, W: r.W - wardPanelPad, H: r.H - wardPanelPad}
	ward.addScroll -= c.WheelIn(in) * scrollStepPx
	track := sdl.Rect{X: in.X + in.W - scrollBarW, Y: in.Y, W: scrollBarW, H: in.H}
	ward.addScroll = c.VScrollbar("wardaddscroll", track, ward.addScroll, int32(len(names))*wardAddRowH, in.H)
	prev, had := c.pushClip(in)
	for i, n := range names {
		row := sdl.Rect{X: in.X, Y: in.Y + int32(i)*wardAddRowH - ward.addScroll, W: in.W - scrollBarW - scrollBarGap, H: wardAddRowH - wardRowGap}
		if row.Y+row.H < in.Y || row.Y > in.Y+in.H {
			continue
		}
		saved := ward.store.Has(n)
		if c.hovering(row) && !saved {
			c.FillRound(row, wardRadSeg, wcHover)
		}
		x := row.X + wardPanelPad
		if ticks {
			box := sdl.Rect{X: x, Y: row.Y + (row.H-checkboxBoxPx)/2, W: checkboxBoxPx, H: checkboxBoxPx}
			on := saved || ward.addPick[n]
			if c.CheckboxIn(box, "", on) != on && !saved {
				ward.addPick[n] = !on
			}
			x += checkboxBoxPx + wardPanelPad
		}
		icon := sdl.Rect{X: x, Y: row.Y + (row.H-wardAddIconPx)/2, W: wardAddIconPx, H: wardAddIconPx}
		a.drawWardrobeAddIcon(n, icon)
		ink := wcText
		if saved {
			ink = wcFaint
		}
		c.LabelClipped(icon.X+icon.W+wardPanelPad+wardRowGap, row.Y+(row.H-a.wardLineH())/2, row.W/2, n, ink)
		if saved {
			note := "Already saved"
			c.Label(row.X+row.W-wardPanelPad-c.TextWidth(note), row.Y+(row.H-a.wardLineH())/2, note, wcFaint)
		}
	}
	c.popClip(prev, had)
}

// drawWardrobeAddIcon is a small initials box with the iniswap tag; the Add list shows names before any icon loads.
func (a *App) drawWardrobeAddIcon(name string, r sdl.Rect) {
	c := a.ctx
	c.FillRound(r, wardRadBadge, wcCard)
	initials := name
	if len(initials) > 2 {
		initials = initials[:2]
	}
	c.LabelClippedFontWeight(c.chromeFace(), r.X+(r.W-c.TextWidth(initials))/2, r.Y+(r.H-a.wardLineH())/2, r.W, initials, wcFaint, true)
	if a.wardrobeSlot(name) < 0 {
		tag := sdl.Rect{X: r.X + r.W - wardTagPx + wardOutline + 1, Y: r.Y + r.H - wardTagPx + wardOutline + 1, W: wardTagPx, H: wardTagPx}
		c.FillRound(tag, wardOutline+1, wcYellow)
		c.Icon(iconShapeSwap, sdl.Rect{X: tag.X + (tag.W-wardTagIcon)/2, Y: tag.Y + (tag.H-wardTagIcon)/2, W: wardTagIcon, H: wardTagIcon}, wcYellowInk)
	}
}

func (a *App) drawWardrobeAddFooter(r sdl.Rect) {
	c := a.ctx
	ward := &a.ward
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, wcLine)
	y := r.Y + (r.H-wardBtnH)/2
	x := r.X + 2*wardPanelPad
	c.Label(x, r.Y+(r.H-a.wardLineH())/2, "Add to", wcSoft)
	x += c.TextWidth("Add to") + wardPanelPad
	if next, changed := c.Dropdown("wardaddtarget", sdl.Rect{X: x, Y: r.Y + (r.H-btnH)/2, W: wardMenuW, H: btnH}, ward.choices, ward.addTarget); changed {
		ward.addTarget = next
	}
	names := a.wardrobeAddNames()
	label := "Add"
	if n := len(names); n > 0 {
		label = "Add " + strconv.Itoa(n) + " characters"
		if n == 1 {
			label = "Add 1 character"
		}
	}
	addR := sdl.Rect{X: r.X + r.W - 2*wardPanelPad - a.wardButtonW(label), Y: y, W: a.wardButtonW(label), H: wardBtnH}
	cancelR := sdl.Rect{X: addR.X - wardPanelPad - wardCloseW, Y: y, W: wardCloseW, H: wardBtnH}
	if a.wardButton(cancelR, "Cancel", wbNormal) {
		ward.addOpen = false
		return
	}
	look := wbPrimary
	if len(names) == 0 {
		look = wbDisabled
	}
	if a.wardButton(addR, label, look) {
		folder := a.wardrobeChoiceFolder(ward.addTarget)
		added := ward.store.Add(names, folder)
		ward.addOpen = false
		a.setWardrobeSource(wardrobe.SourceUser)
		ward.folder, ward.sel, ward.search, ward.scroll = folder, "", "", 0
		a.wardWarn("Added " + strconv.Itoa(added) + " to " + strings.ToLower(a.wardrobeFolderName(folder)))
	}
}
