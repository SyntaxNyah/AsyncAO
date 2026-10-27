package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// folderMenuOpt is one row of the right-click character menu.
type folderMenuOpt struct {
	label, folder string
	remove        bool // remove-from-wardrobe action (not a folder move)
}

const iniFolderMenuW = 200

// iniFolderMenuOpts builds the character menu rows: remove from wardrobe, unfile,
// each existing folder, then a "+ new: X" row when the new-folder field has text.
func (a *App) iniFolderMenuOpts() []folderMenuOpt {
	opts := []folderMenuOpt{{label: "× Remove from wardrobe", remove: true}, {label: "(unsorted)"}}
	for _, f := range a.wardrobeFolders() {
		opts = append(opts, folderMenuOpt{label: f, folder: f})
	}
	if nf := strings.TrimSpace(a.iniNewFold); nf != "" {
		opts = append(opts, folderMenuOpt{label: "+ new: " + nf, folder: nf})
	}
	return opts
}

// iniFolderMenuRect positions the menu at the cursor, clamped on-screen.
func iniFolderMenuRect(at [2]int32, nOpts int, w, h int32) sdl.Rect {
	r := sdl.Rect{X: at[0], Y: at[1], W: iniFolderMenuW, H: int32(nOpts)*iniMenuRowH + 4}
	if r.X+r.W > w {
		r.X = w - r.W
	}
	if r.Y+r.H > h {
		r.Y = h - r.H
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r
}

const iniMenuRowH = 22

// iniDragThreshold is the cursor travel (logical px) past which a press on a
// wardrobe cell becomes a drag-to-file instead of a wear-click.
const iniDragThreshold = 6

// handleIniFolderMenu resolves a click on the open move-to-folder menu BEFORE
// the grid draws (so the click never leaks through to a cell). A click on a
// row files the character; a click anywhere else closes it; Esc closes it.
// Either way the click is consumed.
func (a *App) handleIniFolderMenu(rect sdl.Rect, opts []folderMenuOpt) {
	c := a.ctx
	if c.escPressed {
		a.iniMenuChar = ""
		return
	}
	if !c.clicked {
		return
	}
	for i, opt := range opts {
		row := sdl.Rect{X: rect.X, Y: rect.Y + 2 + int32(i)*iniMenuRowH, W: rect.W, H: iniMenuRowH}
		if c.hovering(row) {
			if opt.remove {
				a.d.Prefs.RemoveWardrobe(a.serverKey, a.iniMenuChar) // unfavourite (also drops its folder)
			} else {
				a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniMenuChar, opt.folder)
			}
			a.rebuildIniMenu()
			a.iniMenuChar = ""
			c.clicked = false
			return
		}
	}
	a.iniMenuChar = "" // clicked outside → close
	c.clicked = false
}

// drawIniFolderMenu paints the move-to-folder menu on top, highlighting the
// character's current folder.
func (a *App) drawIniFolderMenu(rect sdl.Rect, opts []folderMenuOpt) {
	c := a.ctx
	c.Fill(rect, sdl.Color{R: 18, G: 18, B: 26, A: 245})
	c.Border(rect, ColAccent)
	cur := a.d.Prefs.Load().WardrobeFolderMap(a.serverKey)[strings.ToLower(a.iniMenuChar)]
	for i, opt := range opts {
		row := sdl.Rect{X: rect.X, Y: rect.Y + 2 + int32(i)*iniMenuRowH, W: rect.W, H: iniMenuRowH}
		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
		}
		col := ColText
		if opt.remove {
			col = ColDanger // remove-from-wardrobe
		} else if opt.folder == cur {
			col = ColAccent // the character's current folder
		}
		c.LabelClipped(row.X+8, row.Y+4, row.W-12, opt.label, col)
	}
}

// Wardrobe sections: your curated characters (folders), your favourite
// backgrounds, and a flat browse of the server's full iniswap list (★ to add
// one to your characters).
const (
	wardSectionCharacters = iota
	wardSectionBackgrounds
	wardSectionIniswaps
	wardSectionJukebox
)

// drawIniswapPanel is the wardrobe modal shell. It owns the state SHARED by
// both sections — the drag press-edge and the end-of-frame drag cleanup, which
// must run exactly once per frame so a drag can't arm twice or fail to clear —
// and the section tabs, then dispatches to the active section's body for the
// grid region. A tab is a mouseup, so a section never switches mid-drag.
func (a *App) drawIniswapPanel(w, h int32) {
	c := a.ctx
	a.iniPressed = c.mouseDown && !a.iniPrevDown // mouse went down this frame (drag arm; shared)
	// Kept clear of the app-chrome band (#14, chrometop.go): the menu bar and the
	// docked server-tab strip paint AFTER the screens, so a panel starting at the raw
	// pad*3 would have its top edge and title covered by the strip.
	top := a.topChromeH()
	panel := sdl.Rect{X: pad * 3, Y: top + pad*3, W: w - pad*6, H: h - top - pad*6}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+8, "Wardrobe", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: panel.Y + 8, W: 90, H: btnH}, "Close") {
		a.showIni = false
		a.iniMenuChar = ""
		a.wardDelFolder = ""
		a.iniPrevDown = c.mouseDown
		return
	}
	// Section tabs on the header row.
	tabX := panel.X + 110
	for _, t := range [...]struct {
		id    int
		label string
	}{{wardSectionCharacters, "Characters"}, {wardSectionBackgrounds, "Backgrounds"}, {wardSectionIniswaps, "Iniswaps"}, {wardSectionJukebox, "Jukebox"}} {
		r := sdl.Rect{X: tabX, Y: panel.Y + 8, W: 120, H: btnH}
		bg := ColPanel
		if a.wardSection == t.id {
			bg = ColPanelHi
		}
		c.Fill(r, bg)
		c.Border(r, ColAccent)
		c.Label(r.X+12, r.Y+5, t.label, ColText)
		if c.hovering(r) && c.clicked && a.wardSection != t.id {
			a.wardSection = t.id
			// Each section owns its own preview — and its own dwell, so this goes
			// through closeSpritePreview rather than blanking previewBase.
			a.closeSpritePreview()
			a.iniMenuChar = ""   // close any open character move-to-folder menu
			a.wardDelFolder = "" // close any open folder-delete confirmation
			a.jukeBindFor = ""   // cancel any armed jukebox key-capture
			if t.id == wardSectionBackgrounds {
				a.rebuildBgFav()
			}
		}
		tabX += 126
	}
	// Armed key-capture hint: while a wardrobe bind waits for a key, say so loudly
	// in the header AND that Esc or a click cancels — the per-cell "press..." badge
	// alone was too easy to miss after an accidental arm.
	if a.bindingFor != "" {
		hintX := tabX + 8
		hintW := (panel.X + panel.W - 90 - pad) - hintX - 8
		if hintW > 60 {
			c.LabelClipped(hintX, panel.Y+13, hintW, "Binding "+a.bindingFor+": press a key — or Esc / click to cancel", ColAccent)
		}
	}
	switch a.wardSection {
	case wardSectionBackgrounds:
		a.drawWardrobeBgsBody(panel, w, h)
	case wardSectionIniswaps:
		a.drawWardrobeIniswapsBody(panel, w, h)
	case wardSectionJukebox:
		a.drawWardrobeJukeboxBody(panel, w, h)
	default:
		a.drawWardrobeCharsBody(panel, w, h)
	}
	a.applyPendingFav() // a ★ toggle a cell deferred: rebuild AFTER the grid loop, never during it
	// Shared drag ghost: a label trailing the cursor shows what's being dragged
	// (a character or a background — whichever section armed the drag).
	if a.iniDragging && a.iniDragChar != "" {
		g := sdl.Rect{X: c.mouseX + 12, Y: c.mouseY + 8, W: c.TextWidth(a.iniDragChar) + 16, H: 22}
		c.Fill(g, sdl.Color{R: 0, G: 0, B: 0, A: 225})
		c.Border(g, ColAccent)
		c.Label(g.X+6, g.Y+3, a.iniDragChar, ColAccent)
	}
	// Shared end-of-frame drag bookkeeping (once per frame, both sections).
	if !c.mouseDown {
		a.iniDragChar = ""
		a.iniDragging = false
	}
	a.iniPrevDown = c.mouseDown
}

// drawWardrobeCharsBody is the Characters section: the navigable wardrobe grid.
func (a *App) drawWardrobeCharsBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	a.pollIniswap()
	a.pollPreviewEmotes() // try-before-wear: drain a previewed char's emote list
	a.iniHoverChar = ""   // recomputed by the cells this frame (quick-file target)
	// Move-to-folder menu: handle its clicks FIRST so they can't leak to the
	// folder icons/grid underneath (it's painted last, on top).
	if a.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.handleIniFolderMenu(iniFolderMenuRect(a.iniMenuAt, len(opts), w, h), opts)
	}

	y := panel.Y + 44
	active := "none (using " + a.myCharName() + ")"
	if a.iniChar != "" {
		active = a.iniChar
	}
	c.LabelClipped(panel.X+pad, y+4, 340, "Wearing: "+active, ColAccent)
	if a.iniChar != "" {
		if c.Button(sdl.Rect{X: panel.X + 360, Y: y, W: 130, H: btnH}, "Take off") {
			a.setIniswap("")
		}
	}
	y += 32
	// Default: clicking a favourite SWITCHES you to that character (claims its
	// slot — the CC → PV flow). Tick this to iniswap it instead: wear the look,
	// keep your slot. Per session (resets on restart). Iniswap-by-name now lives
	// on the Iniswaps tab's search box.
	const swapLabel = "Iniswap instead of switching character"
	a.iniSwapMode = c.Checkbox(panel.X+pad, y, swapLabel, a.iniSwapMode)
	c.Tooltip(sdl.Rect{X: panel.X + pad, Y: y, W: 22 + c.TextWidth(swapLabel), H: 16},
		"Off (default): clicking a character switches you to them (takes a slot). On: wears them as an iniswap — no slot taken.")
	y += 32
	a.iniSearch, _ = c.TextField("iniswapsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.iniSearch, "Search...")
	// Add any folder name on the current asset base to the wardrobe —
	// no server list required (★ marks saved entries; ★ persists
	// across sessions and servers).
	var addNow bool
	a.iniAdd, addNow = c.TextField("iniswapadd", sdl.Rect{X: panel.X + pad + 240, Y: y, W: 230, H: fieldH}, a.iniAdd, "Add char (or folder/char) to wardrobe...")
	if c.Button(sdl.Rect{X: panel.X + pad + 476, Y: y, W: 60, H: btnH}, "Add") || addNow {
		// "folder/char" files it explicitly; otherwise it joins the folder
		// you're currently viewing ("" at the top level = unfiled).
		raw := strings.TrimSpace(a.iniAdd)
		folder, char := a.iniFolder, raw
		if i := strings.IndexByte(raw, '/'); i >= 0 {
			folder, char = strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1:])
		}
		if a.d.Prefs.AddWardrobe(a.serverKey, char) {
			if folder != "" {
				a.d.Prefs.SetWardrobeFolder(a.serverKey, char, folder)
			}
			a.iniAdd = ""
			a.rebuildIniMenu()
		}
	}
	statusX := panel.X + pad + 550
	switch {
	case a.iniBusy:
		c.Label(statusX, y+4, "Fetching "+iniswapFileName+"...", ColTextDim)
	case a.iniListErr != "":
		c.LabelClipped(statusX, y+4, panel.X+panel.W-statusX-pad, a.iniListErr, ColTextDim)
	default:
		c.Label(statusX, y+4, fmt.Sprintf("%d entries", len(a.iniList)), ColTextDim)
	}
	y += 30
	// One-line orientation — the Wardrobe confused some newcomers. Names the two
	// ways in (★ in Character Select, or the Add box) and the folder/wear flow.
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad,
		"Your saved characters, kept per server. ★ a character in Character Select (or use Add above) to save it; drag a card onto a folder to organise; click a card to wear it.", ColTextDim)
	y += 22

	// --- Folder navigation -----------------------------------------------------
	// Folders are real objects you open. At the top level the grid leads with a
	// folder icon per category; click one to open it (the grid then shows just
	// that folder's characters) and drag a character onto one to file it. Inside
	// a folder, a back button returns to the top (and is itself a drop target
	// that removes a character from the folder). Folders are membership-derived;
	// typing a name below makes a transient folder cell so the FIRST character
	// has something to drop onto. A search spans every folder.
	query := a.iniQ.get(a.iniSearch)
	searching := query != ""

	folderCells := a.wardrobeFolders()
	if nf := strings.TrimSpace(a.iniNewFold); nf != "" { // a just-typed folder is a drop target before it has members
		seen := false
		for _, f := range folderCells {
			if strings.EqualFold(f, nf) {
				seen = true
				break
			}
		}
		if !seen {
			folderCells = append(folderCells, nf)
		}
	}
	charVisible := func(i int) bool {
		if i >= len(a.iniWardrobe) || i >= len(a.iniFolders) || i >= len(a.iniLower) {
			return false // never index past the parallel slices if they ever shrink mid-frame
		}
		// Characters is YOUR wardrobe only — starred or filed into a folder. The
		// full server list lives on the Iniswaps tab (★ there adds it here).
		if !a.iniWardrobe[i] && a.iniFolders[i] == "" {
			return false
		}
		if searching { // search ignores the folder you're in — find anyone
			return strings.Contains(a.iniLower[i], query)
		}
		return strings.EqualFold(a.iniFolders[i], a.iniFolder) // "" = top level = unfiled
	}
	countInFolder := func(name string) int {
		n := 0
		for _, f := range a.iniFolders {
			if strings.EqualFold(f, name) {
				n++
			}
		}
		return n
	}

	if a.wardDelFolder != "" {
		// A folder delete awaits confirmation: the bar replaces the nav row.
		choice := a.drawFolderDeleteConfirm(panel.X+pad, y, panel.W-2*pad, countInFolder(a.wardDelFolder), "characters")
		if choice == folderDeleteWithItems || choice == folderDeleteKeepItems {
			a.d.Prefs.DeleteWardrobeFolder(a.serverKey, a.wardDelFolder, choice == folderDeleteKeepItems)
			if strings.EqualFold(a.iniNewFold, a.wardDelFolder) {
				a.iniNewFold = "" // also drop the transient "new folder" chip if it named this one
			}
			if strings.EqualFold(a.iniFolder, a.wardDelFolder) { // we were inside the deleted folder
				a.iniFolder = ""
				a.iniScroll = 0
			}
			a.wardDelFolder = ""
			a.rebuildIniMenu()
		} else if choice == folderDeleteCancel {
			a.wardDelFolder = ""
		}
	} else {
		switch {
		case searching:
			c.Label(panel.X+pad, y+4, "Search spans every folder — clear it to browse folders", ColTextDim)
		case a.iniFolder == "":
			a.iniNewFold, _ = c.TextField("ininewfold", sdl.Rect{X: panel.X + pad, Y: y, W: 240, H: fieldH}, a.iniNewFold, "New folder name, then drag chars onto it")
			c.LabelClipped(panel.X+pad+250, y+4, panel.X+panel.W-(panel.X+pad+250)-pad, "Drag a character onto a folder to file it · open a folder to see inside · × on a folder deletes it", ColTextDim)
		default:
			back := sdl.Rect{X: panel.X + pad, Y: y, W: 150, H: btnH}
			c.Fill(back, ColPanel)
			if a.iniDragging && c.hovering(back) {
				c.Border(back, ColAccent) // drop here to take the character out of the folder
			} else {
				c.Border(back, ColPanelHi)
			}
			c.Label(back.X+8, back.Y+5, "‹ All folders", ColText)
			c.Tooltip(back, "Back to all folders — or drop a character here to take it out of this folder")
			if c.hovering(back) && c.clicked {
				if a.iniDragging && a.iniDragChar != "" {
					a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniDragChar, "")
					a.rebuildIniMenu()
					c.clicked = false // consume the drop
				} else {
					a.iniFolder = ""
					a.iniScroll = 0
				}
			}
			c.LabelClipped(back.X+back.W+12, y+5, panel.X+panel.W-(back.X+back.W+12)-pad, fmt.Sprintf("%s — %d character(s) · right-click a character to move it", a.iniFolder, countInFolder(a.iniFolder)), ColAccent)
		}
	}
	y += btnH + 8

	// Grid: folder icons (top level only) then character cells. The grid slot
	// drives layout ONLY — character cells always pass their iniList index to
	// drawIniswapCell so the index-keyed cachedPage stays correct (see the
	// cachedPage reorder invariant).
	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}
	showFolders := !searching && a.iniFolder == ""
	slots := int32(0)
	if showFolders {
		slots += int32(len(folderCells))
	}
	for i := range a.iniList {
		if charVisible(i) {
			slots++
		}
	}
	cellH := iconCell + iconGap + charCellCaptionH
	contentH := (slots + cols - 1) / cols * cellH
	visibleH := panel.Y + panel.H - gridTop - pad

	a.iniScroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniScroll = c.VScrollbar("iniscroll", track, a.iniScroll, contentH, visibleH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	slot := int32(0)
	place := func() (sdl.Rect, bool) {
		x := panel.X + pad + (slot%cols)*(iconCell+iconGap)
		yy := gridTop + (slot/cols)*cellH - a.iniScroll
		slot++
		return sdl.Rect{X: x, Y: yy, W: iconCell, H: iconCell}, yy > gridTop-iconCell && yy < panel.Y+panel.H-charCellCaptionH
	}
	if showFolders {
		for _, f := range folderCells {
			if cell, vis := place(); vis {
				a.drawIniFolderCell(f, countInFolder(f), cell)
			}
		}
	}
	for i := range a.iniList {
		if !charVisible(i) {
			continue
		}
		if cell, vis := place(); vis {
			a.drawIniswapCell(i, cell, cellClickChar, true)
		}
	}
	c.popClip(clipPrev, clipHad)

	// Number-key quick-file: hover a character (no field focused, no menu open)
	// and press a digit to file it — 1-9 = that-numbered folder, 0 = unsorted.
	if a.iniHoverChar != "" && c.focusID == "" && a.iniMenuChar == "" {
		switch {
		case c.keyPressed == sdl.K_0:
			a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniHoverChar, "")
			a.rebuildIniMenu()
		case c.keyPressed >= sdl.K_1 && c.keyPressed <= sdl.K_9:
			if folders := a.wardrobeFolders(); int(c.keyPressed-sdl.K_1) < len(folders) {
				a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniHoverChar, folders[c.keyPressed-sdl.K_1])
				a.rebuildIniMenu()
			}
		}
	}

	if a.previewBase != "" {
		a.drawSpritePreview(w, h, true, "") // wardrobe: try-before-wear emote cycle (the nav bar captions the name)
		a.dismissPreviewOnClick()           // pin-aware, and disarms the trigger's dwell id
	}

	// Move-to-folder menu paints last (above the grid + preview). The shared
	// drag ghost and end-of-frame drag cleanup live in the drawIniswapPanel
	// wrapper — one place for both sections.
	if a.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.drawIniFolderMenu(iniFolderMenuRect(a.iniMenuAt, len(opts), w, h), opts)
	}
}

// drawWardrobeIniswapsBody is the Iniswaps section: a flat browse of every
// character folder this client knows exists on the base, from TWO sources the
// chip row switches between.
//
//   - "Server list" is the server's published iniswap.txt: art cells, ★ to add one
//     to your wardrobe (it then appears under Characters), click to try it on. It
//     renders from the SAME a.iniList with the SAME indices as the Characters grid,
//     so the index-keyed thumbnail cache (a.iniPages) stays valid across a tab
//     switch (the cachedPage reorder invariant — a different list under the same
//     index keys would paint wrong art).
//   - "From your base" is the server's character ROSTER plus the characters/ folders
//     in your local mounts — the answer for the many servers that publish no
//     iniswap.txt at all. It keeps its OWN list and draws name buttons rather than
//     index-keyed art cells, for exactly the invariant above; see iniswapbase.go.
//
// No folders in either — organizing lives in Characters.
func (a *App) drawWardrobeIniswapsBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	a.pollIniswap()            // drain the server list (fetched on open)
	a.pollLocalCharFolders()   // ...and the local-mount folder scan (iniswapbase.go)
	a.ensureLocalCharFolders() // starts one only when the mount list actually changed
	a.iniHoverChar = ""        // recomputed by the cells (quick-file target; unused here)
	serverN, baseN := a.baseSourceCounts()
	// Memoized: this body draws every frame the tab is up (iniswapbase.go).
	serverLabel, baseLabel, statusLabel := a.iniBase.sourceLabels(serverN, baseN)

	y := panel.Y + 44
	var iniswapNow bool
	a.iniSearch, iniswapNow = c.TextField("iniswapsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.iniSearch, "Iniswap a folder by name, or search the list…")
	c.Tooltip(sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH},
		"Type any character folder on the base and press Enter to iniswap into it — or just type to filter the list below.")
	if iniswapNow { // Enter → legacy iniswap into the typed folder (whether or not it's in the list)
		if name := strings.TrimSpace(a.iniSearch); name != "" {
			a.iniSearch = ""
			a.wearFromMenu(name)
		}
	}
	statusX := panel.X + pad + 250
	switch {
	case a.iniBusy:
		c.Label(statusX, y+4, "Fetching "+iniswapFileName+"...", ColTextDim)
	case serverN == 0:
		// No iniswap.txt on this server (404 or none published): say so calmly — the
		// base browser below is the answer, and the favourites on the Characters tab
		// are untouched.
		c.LabelClipped(statusX, y+4, panel.X+panel.W-statusX-pad, "This server doesn't publish an "+iniswapFileName+" list.", ColTextDim)
	default:
		c.Label(statusX, y+4, statusLabel, ColTextDim)
	}
	y += 30

	// TWO SOURCES (iniswapbase.go). The published iniswap.txt is one list of folders
	// on the base; the server's own character roster plus the user's local mounts are
	// two more, and on the many servers that publish no iniswap.txt they are the ONLY
	// ones. A server with no published list opens on the base browser once per server
	// (the autoDone/autoKey latch) rather than on the empty state this replaces.
	if !a.iniBusy && (!a.iniBase.autoDone || a.iniBase.autoKey != a.serverKey) {
		a.iniBase.autoDone, a.iniBase.autoKey = true, a.serverKey
		a.iniBase.show = serverN == 0 && baseN > 0
	}
	for i, src := range [...]struct {
		show  bool
		label string
	}{
		{false, serverLabel},
		{true, baseLabel},
	} {
		r := sdl.Rect{X: panel.X + pad + int32(i)*(iniBaseSourceChipW+6), Y: y, W: iniBaseSourceChipW, H: btnH}
		if c.Button(r, src.label) && a.iniBase.show != src.show {
			a.iniBase.show = src.show
			a.iniBase.scroll = 0
			a.iniBrowseScroll = 0
		}
		if a.iniBase.show == src.show {
			c.Border(r, ColAccent) // active source
		}
	}
	c.Tooltip(sdl.Rect{X: panel.X + pad + iniBaseSourceChipW + 6, Y: y, W: iniBaseSourceChipW, H: btnH},
		"Every character folder this client knows exists on the base: the server's own character roster, plus the characters/ folders in your local mounts.")
	y += btnH + 6

	if a.iniBase.show {
		a.drawIniswapBaseBody(panel, y, a.iniQ.get(a.iniSearch))
		return
	}

	// No server list yet → a mini guide for owners (where to put iniswap.txt) and
	// players (they can still wear any folder by name).
	if !a.iniBusy && serverN == 0 {
		base := a.urls.Origin()
		if base == "" {
			base = "https://your-server.com/base/"
		}
		gx, gy := panel.X+pad, y+4
		for _, line := range []string{
			"No iniswap list for this server yet.",
			"",
			"Server owners: drop an " + iniswapFileName + " at your asset base and it'll",
			"be fetched here automatically — one character folder name per line:",
			"",
			base + iniswapFileName,
			"",
			"Players: type any folder name in the search box above and press Enter to",
			"iniswap into it instantly — no published list required.",
		} {
			col := ColTextDim
			if strings.Contains(line, "://") {
				col = ColAccent // the example URL
			}
			c.LabelClipped(gx, gy, panel.W-2*pad, line, col)
			gy += 20
		}
		return
	}

	query := a.iniQ.get(a.iniSearch)
	// The Iniswaps tab is the server's iniswap.txt ONLY — a favourite that isn't
	// a server iniswap (or any favourite on a server with no list) stays out.
	visible := func(i int) bool {
		if i >= len(a.iniServerMem) || !a.iniServerMem[i] {
			return false
		}
		return query == "" || strings.Contains(a.iniLower[i], query)
	}

	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}
	slots := int32(0)
	for i := range a.iniList {
		if visible(i) {
			slots++
		}
	}
	cellH := iconCell + iconGap + charCellCaptionH
	contentH := (slots + cols - 1) / cols * cellH
	visibleH := panel.Y + panel.H - gridTop - pad

	a.iniBrowseScroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniBrowseScroll = c.VScrollbar("inibrowsescroll", track, a.iniBrowseScroll, contentH, visibleH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	slot := int32(0)
	for i := range a.iniList {
		if !visible(i) {
			continue
		}
		x := panel.X + pad + (slot%cols)*(iconCell+iconGap)
		yy := gridTop + (slot/cols)*cellH - a.iniBrowseScroll
		slot++
		if yy > gridTop-iconCell && yy < panel.Y+panel.H-charCellCaptionH {
			a.drawIniswapCell(i, sdl.Rect{X: x, Y: yy, W: iconCell, H: iconCell}, cellClickIniswap, true)
		}
	}
	c.popClip(clipPrev, clipHad)

	if a.previewBase != "" {
		a.drawSpritePreview(w, h, true, "") // try-before-wear preview, same as Characters (nav bar captions the name)
		a.dismissPreviewOnClick()           // pin-aware, and disarms the trigger's dwell id
	}
}

// drawFolderShape paints a folder icon — a tab over a body, the member count
// centered, the name beneath — sized to the given cell. The shared VISUAL for
// both wardrobe sections; interaction (open/drop) is the caller's, since
// characters and backgrounds file into different prefs. Pure graphics — no
// cachedPage — so a folder cell is safe to draw at any grid slot (unlike the
// index-keyed character/background cells).
func (a *App) drawFolderShape(cell sdl.Rect, count int, name string, hover, dropTarget bool) {
	c := a.ctx
	tab := sdl.Rect{X: cell.X + 4, Y: cell.Y + 5, W: (cell.W - 8) / 2, H: 9}
	body := sdl.Rect{X: cell.X + 2, Y: cell.Y + 12, W: cell.W - 4, H: cell.H - 14}
	col := ColFolder
	if hover {
		col = ColFolderHi
	}
	c.Fill(tab, col)
	c.Fill(body, col)
	if dropTarget { // a drag is hovering this folder
		c.Border(tab, ColAccent)
		c.Border(body, ColAccent)
	} else {
		c.Border(body, ColPanelHi)
	}
	cnt := fmt.Sprintf("%d", count)
	c.Label(body.X+body.W/2-c.TextWidth(cnt)/2, body.Y+body.H/2-8, cnt, ColText)
	c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, name, ColText)
}

// folderDeleteHit draws the folder's delete (×) button when the icon is hovered
// (and no drag is in flight — during a drag the folder is a drop target) and
// reports a click on it. Shared by both wardrobe sections; the caller opens the
// delete confirmation. The × claims its own click so the folder doesn't open.
func (a *App) folderDeleteHit(cell sdl.Rect, hover bool) bool {
	if !hover || a.iniDragging {
		return false
	}
	c := a.ctx
	x := sdl.Rect{X: cell.X + cell.W - 18, Y: cell.Y + 2, W: 16, H: 16}
	c.Fill(x, sdl.Color{R: 0, G: 0, B: 0, A: 205})
	c.Label(x.X+4, x.Y+1, "×", ColDanger)
	c.Tooltip(x, "Delete this folder")
	return c.hovering(x) && c.clicked
}

// folderDeleteChoice is the outcome of the delete-folder confirmation bar.
type folderDeleteChoice int

const (
	folderDeleteNone folderDeleteChoice = iota
	folderDeleteWithItems
	folderDeleteKeepItems
	folderDeleteCancel
)

// drawFolderDeleteConfirm draws the "delete folder?" bar (replacing the nav row
// while a.wardDelFolder is set) and returns the user's choice this frame. noun
// is "characters"/"backgrounds" for the copy. Esc cancels.
func (a *App) drawFolderDeleteConfirm(x, y, maxW int32, count int, noun string) folderDeleteChoice {
	c := a.ctx
	if c.escPressed {
		return folderDeleteCancel
	}
	label := fmt.Sprintf("Delete folder \"%s\" (%d %s)?", a.wardDelFolder, count, noun)
	c.LabelClipped(x, y+5, 320, label, ColDanger)
	bx := x + 332
	if c.Button(sdl.Rect{X: bx, Y: y, W: 150, H: btnH}, fmt.Sprintf("Delete + %d items", count)) {
		return folderDeleteWithItems
	}
	bx += 156
	if c.Button(sdl.Rect{X: bx, Y: y, W: 130, H: btnH}, "Keep items") {
		return folderDeleteKeepItems
	}
	bx += 136
	if c.Button(sdl.Rect{X: bx, Y: y, W: 80, H: btnH}, "Cancel") {
		return folderDeleteCancel
	}
	return folderDeleteNone
}

// drawIniFolderCell draws one Characters-section folder. Clicking opens it (the
// grid then shows only its characters); dropping a dragged character files it;
// the hover × deletes it.
func (a *App) drawIniFolderCell(name string, count int, cell sdl.Rect) {
	c := a.ctx
	hover := c.hovering(cell)
	a.drawFolderShape(cell, count, name, hover, a.iniDragging && hover)
	if a.folderDeleteHit(cell, hover) {
		a.wardDelFolder = name
		return // the × claimed the click; don't open the folder
	}
	c.Tooltip(cell, "Open the "+name+" folder — or drop a character here to file it")
	if hover && c.clicked {
		if a.iniDragging && a.iniDragChar != "" {
			a.d.Prefs.SetWardrobeFolder(a.serverKey, a.iniDragChar, name)
			a.rebuildIniMenu()
			c.clicked = false // consume the drop
		} else {
			a.iniFolder = name
			a.iniScroll = 0
		}
	}
}

// cellClick is what clicking a wardrobe cell does: always iniswap (the Iniswaps
// tab), or run the Characters-tab logic — switch to the character (claim its
// slot), falling back to iniswap when it's taken / not a server char / the box
// is ticked. cellClickChar drives the courtroom Characters tab AND the
// char-select Wardrobe tab, so a favourite switches in both.
type cellClick int

const (
	cellClickIniswap cellClick = iota
	cellClickChar
)

// The wardrobe cell's fixed-size corner chrome, in pixels (hard rule 9 — these were
// bare literals, and they have to be legible now that cellFitsCornerChrome decides
// whether they are drawn at all).
const (
	// iniCellStarPx is the ★ box's edge, top-right, inset one pixel.
	iniCellStarPx int32 = 17
	// iniCellStarLaneW is how much of the cell's right edge the folder tag leaves
	// clear for that star: the star's own width plus its inset and a few px of gap,
	// so a long folder name never prints under the glyph.
	iniCellStarLaneW int32 = 22
	// iniCellTagH is the folder tag's height, top-left.
	iniCellTagH int32 = 15
	// iniCellKeyBadgeH is the key badge's height, bottom-left, inset one pixel.
	iniCellKeyBadgeH int32 = 16
)

// caption draws the folder name UNDER the cell. The char-select Wardrobe tab turns
// it off on a theme canvas, where the row gutter is the theme's and a caption would
// print across the next row's slot frame (charGridPlan) — the cell's own tooltip
// above already names it there.
func (a *App) drawIniswapCell(idx int, cell sdl.Rect, click cellClick, caption bool) {
	c := a.ctx
	name := a.iniList[idx]

	// Hover hint for what a click does here. Registered before the star/key-badge
	// tooltips below, which override it on their own sub-rects (Tooltip = last
	// write wins, and the cursor sits on those rects when it's over them).
	switch {
	case !caption:
		// No caption strip under the cell (the char-select Wardrobe tab on a theme
		// canvas), so the tooltip carries the name instead — same as AO2, which puts
		// the character's name on the button as its tooltip and nowhere else. Naming
		// the cell beats explaining the click: the tab this grid lives on already says
		// what a click does, and with no caption there is otherwise nothing to tell two
		// un-iconed folders apart.
		c.Tooltip(cell, name)
	case click == cellClickChar:
		c.Tooltip(cell, "Click to switch to this character (take its slot). Tick \"Iniswap instead\" above to wear it without a slot.")
	default:
		c.Tooltip(cell, "Click to iniswap into this folder — wear its look, no slot taken.")
	}

	// App-drawer drag: a press on the cell arms this character as the drag
	// candidate; once the cursor travels past iniDragThreshold it becomes a
	// floating ghost that drops onto a folder chip to file it (the chip drop
	// handler and ghost live in drawIniswapPanel). A press that never moves is
	// just a click and still wears/toggles below — the click actions are gated
	// on !a.iniDragging so a drag never also fires them.
	if a.iniPressed && a.iniMenuChar == "" && c.hovering(cell) {
		a.iniDragChar = name
		a.iniDragStart = [2]int32{c.mouseX, c.mouseY}
		a.iniDragging = false
	}
	if a.iniDragChar == name && c.mouseDown {
		dx, dy := c.mouseX-a.iniDragStart[0], c.mouseY-a.iniDragStart[1]
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx+dy > iniDragThreshold {
			a.iniDragging = true
		}
	}

	c.Fill(cell, ColBackground)
	base := a.urls.CharIcon(name)
	if page, ok := a.cachedPage(&a.iniPages, &a.iniPagesGen, len(a.iniList), idx, base); ok && len(page.Frames) > 0 {
		// Shared scratch, not &cell: cell is a PARAMETER, and escape analysis is
		// static, so a cgo pointer taken from it moves it to the heap on every call —
		// including the placeholder branch below, which never touches SDL. One
		// allocation per visible cell per frame, on three grids (the char-select
		// Wardrobe tab and both wardrobe-panel bodies). Same fix, same reason, as
		// drawCharCell's; SDL copies the rect during the call and never retains the
		// pointer (the ui.go cgoRect contract), and the cachedPage lookup above is
		// already done, so nothing can interleave.
		c.cgoRect = cell
		_ = c.Ren.Copy(page.Frames[0], nil, &c.cgoRect)
	} else {
		a.demandAsset(&a.iniAsk, len(a.iniList), idx, base, assets.AssetTypeCharIcon) // AssetType: CharIcon (wardrobe)
		initial := name
		if len(initial) > 2 {
			initial = initial[:2]
		}
		c.Label(cell.X+cell.W/2-8, cell.Y+cell.H/2-8, initial, ColTextDim)
	}
	if caption {
		c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, name, ColTextDim)
	}

	// FIXED-SIZE CORNER CHROME — the folder tag, the ★ and the key badge — behind the
	// SAME gate drawCharCell puts its own star and download badge behind
	// (cellFitsCornerChrome). Since #20 the char-select Wardrobe tab draws these cells
	// on the Characters tab's own charGridPlan, so a cell here can be as small as
	// charCellMinPx while all three of these are drawn at the client font's size
	// whatever the cell. On the measured 39 px cell (the 640x480 floor with the stock
	// table) they covered three-quarters of the character art AND ate the click aimed
	// at it. Above the floor nothing changes; the courtroom's own wardrobe panels draw
	// 64 px cells and never reach it.
	if cellFitsCornerChrome(cell) {
		// Folder tag (top-left): the category this character is filed under
		// (right-click the cell to file it into the active folder).
		if idx < len(a.iniFolders) && a.iniFolders[idx] != "" {
			ft := a.iniFolders[idx]
			tw := c.TextWidth(ft) + 6
			if maxW := cell.W - iniCellStarLaneW; tw > maxW { // leave the top-right for the star
				tw = maxW
			}
			tag := sdl.Rect{X: cell.X + 1, Y: cell.Y + 1, W: tw, H: iniCellTagH}
			c.Fill(tag, sdl.Color{R: 0, G: 0, B: 0, A: 185})
			c.LabelClipped(tag.X+3, tag.Y+1, tag.W-5, ft, ColAccent)
		}

		// Wardrobe star (top-right of the cell): toggle membership without
		// wearing — the favourites list itself, exactly like lobby stars.
		star := sdl.Rect{X: cell.X + cell.W - iniCellStarPx - 1, Y: cell.Y + 1, W: iniCellStarPx, H: iniCellStarPx}
		starCol := ColTextDim
		if idx < len(a.iniWardrobe) && a.iniWardrobe[idx] {
			starCol = ColStar
		}
		c.Label(star.X+2, star.Y, "★", starCol)
		c.Tooltip(star, "★ add to / remove from your wardrobe (right-click the cell for more)")
		if c.hovering(star) && c.clicked && !a.iniDragging {
			// DEFER the toggle: rebuilding the wardrobe here shrinks the iniList/
			// iniWardrobe/iniFolders slices the grid loop is currently ranging, which
			// panicked on a REMOVE (the reported crash). The HOST applies it after its
			// loop — drawIniswapPanel for the wardrobe modal, drawWardrobeGrid for the
			// char-select Wardrobe tab.
			a.iniFavPending = name
			a.iniFavPendingAdd = idx >= len(a.iniWardrobe) || !a.iniWardrobe[idx]
			c.clicked = false // consumed; don't also wear it
			return
		}

		// Key badge (bottom-left): the CHORD that wears this character on this
		// server, or "+key" on hover. Click arms capture (next plain keypress
		// binds); right-click clears the binding. The badge goes through
		// bindChipLabel, not the bare letter: handleCharKeys fires on Alt+<key>
		// (bareBindKey, qol.go), so a badge reading "M" would advertise a press
		// that now just types an "m" into the IC box.
		bound := a.charKeyFor(name)
		unbound := "" // an unbound cell shows nothing until the pointer is on it
		if c.hovering(cell) {
			unbound = "+key"
		}
		badgeLabel := bindChipLabel(bound, a.bindingFor == name, "press...", unbound)
		if badgeLabel != "" {
			bw := c.TextWidth(badgeLabel) + 8
			badge := sdl.Rect{X: cell.X + 1, Y: cell.Y + cell.H - iniCellKeyBadgeH - 1, W: bw, H: iniCellKeyBadgeH}
			c.Fill(badge, sdl.Color{R: 0, G: 0, B: 0, A: 190})
			col := ColAccent
			if bound == "" {
				col = ColTextDim
			}
			c.Label(badge.X+4, badge.Y+1, badgeLabel, col)
			if c.hovering(badge) {
				if c.clicked && !a.iniDragging {
					a.bindingFor = name
					c.focusID = "" // capture owns the next keypress outright
					return         // don't also wear it
				}
				if c.rightClicked && bound != "" {
					a.d.Prefs.SetCharKeyBind(a.serverKey, bound, "")
					a.refreshCharKeys()
					return
				}
			}
		}
	}

	// Right-click the cell opens a "move to folder" menu (pick any destination,
	// including a brand-new one). The key badge's own right-click ran first.
	if c.rightClicked && c.hovering(cell) {
		a.iniMenuChar = name
		a.iniMenuAt = [2]int32{c.mouseX, c.mouseY}
		return
	}

	if c.HoverPreview(a.iniHoverID(idx), cell) {
		a.previewBase = a.urls.Emote(name, "normal", courtroom.EmoteIdle)
		a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(name, "normal", courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
		a.ensurePreviewEmotes(name, previewEmoteNav)                                                                                                      // try-before-wear: load this character's emotes to cycle
	}
	if c.hovering(cell) {
		a.iniHoverChar = name // number keys 0-9 quick-file the hovered character
		a.warmCharINI(name)   // wearing it = memory hit, not an RTT
		if c.clicked && !a.iniDragging {
			if click == cellClickChar {
				a.wardrobeClick(name) // Characters tab: switch to the char (or iniswap if ticked)
			} else {
				a.wearFromMenu(name) // Iniswaps tab / char-select drawer: iniswap
			}
		}
	}
}

// musicFilterKey memoizes the server-music-list filter so a list of thousands
// isn't re-scanned (and re-lowercased — that allocates) every frame. Keyed by
// the query plus a cheap identity of the list (len + first/last track), which
// changes whenever the server resends SM.
type musicFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshMusicFilter recomputes the matching track indices for a non-empty
// query, memoized against the query + the list identity. Only this O(N) scan
// (and its ToLower allocs) is gated here; the per-frame call is an O(1) key
// compare and early-return when neither the query nor the list changed.
//
// It matches the RAW list entry, which covers the displayed short name too:
// courtroom.MusicDisplayName only ever slices, so the display is always a
// substring of the raw name (#43 shortened the rows, not what you can search).
// Searching the raw text additionally keeps a directory ("songs/") findable.
func (a *App) refreshMusicFilter(query string) {
	n := len(a.sess.Music)
	key := musicFilterKey{q: query, n: n}
	if n > 0 {
		key.first, key.last = a.sess.Music[0], a.sess.Music[n-1]
	}
	if key == a.musicFilterMemo {
		return
	}
	a.musicFilterMemo = key
	a.musicFiltered = a.musicFiltered[:0]
	for i, track := range a.sess.Music {
		if strings.Contains(strings.ToLower(track), query) {
			a.musicFiltered = append(a.musicFiltered, i)
		}
	}
}

// musicRow is one displayed row of the Music tab's track list: either a
// category header (Issue #17: AO2/KFO group songs under a "==Category=="
// entry with no audio extension — see courtroom.HasAudioExt) or a track,
// both identified by their ORIGINAL index into a.sess.Music.
type musicRow struct {
	ti     int
	header bool
}

// musicGroupKey memoizes the grouped/collapsed row layout, mirroring
// musicFilterKey: the query plus a cheap list identity, PLUS
// musicCollapseGen so toggling a category (or Expand/Collapse All)
// invalidates it without re-keying on the whole collapse map every frame.
type musicGroupKey struct {
	q           string
	n           int
	first, last string
	collapseGen uint64
}

// refreshMusicGroups (re)builds the visible row layout for the Music tab:
// every category header, plus its tracks unless the header is collapsed.
// Memoized against (query, list identity, collapse generation) so folding a
// category — or typing in the search box — costs an O(N) rescan only on the
// frame that actually changed something (the icWrapped/refreshMusicFilter
// idiom), never per frame.
//
// While a search is active, collapse state is bypassed entirely: matching
// tracks always show (auto-expanded, VSCode-style), with their category
// header pulled in for context even if that header didn't itself match.
// Manual collapse state is untouched and resumes the moment the query is
// cleared.
func (a *App) refreshMusicGroups(query string) {
	n := len(a.sess.Music)
	key := musicGroupKey{q: query, n: n, collapseGen: a.musicCollapseGen}
	if n > 0 {
		key.first, key.last = a.sess.Music[0], a.sess.Music[n-1]
	}
	if key == a.musicGroupMemo && a.musicRows != nil {
		return
	}
	a.musicGroupMemo = key
	a.musicRows = a.musicRows[:0]

	if query == "" {
		collapsed := false
		for i, entry := range a.sess.Music {
			if !courtroom.HasAudioExt(entry) {
				collapsed = a.musicCollapsed[i]
				a.musicRows = append(a.musicRows, musicRow{ti: i, header: true})
				continue
			}
			if collapsed {
				continue
			}
			a.musicRows = append(a.musicRows, musicRow{ti: i})
		}
		return
	}

	pendingHeader, headerShown := -1, false
	for i, entry := range a.sess.Music {
		if !courtroom.HasAudioExt(entry) {
			pendingHeader = i
			headerShown = strings.Contains(strings.ToLower(entry), query)
			if headerShown {
				a.musicRows = append(a.musicRows, musicRow{ti: i, header: true})
			}
			continue
		}
		if !strings.Contains(strings.ToLower(entry), query) {
			continue
		}
		if pendingHeader >= 0 && !headerShown {
			a.musicRows = append(a.musicRows, musicRow{ti: pendingHeader, header: true})
			headerShown = true
		}
		a.musicRows = append(a.musicRows, musicRow{ti: i})
	}
}

// setAllMusicCollapsed (Issue #17's Expand All / Collapse All button) bulk-sets
// every category header's fold state in one pass and bumps musicCollapseGen
// once, instead of one toggle (and one memo-invalidating bump) per category.
func (a *App) setAllMusicCollapsed(collapsed bool) {
	if !collapsed {
		if len(a.musicCollapsed) == 0 {
			return // already all-expanded (the default, empty-map state) — no-op
		}
		a.musicCollapsed = nil
		a.musicCollapseGen++
		return
	}
	if a.musicCollapsed == nil {
		a.musicCollapsed = make(map[int]bool)
	}
	for i, entry := range a.sess.Music {
		if !courtroom.HasAudioExt(entry) {
			a.musicCollapsed[i] = true
		}
	}
	a.musicCollapseGen++
}

// areaFilterKey memoizes the areas-list filter (#20), mirroring musicFilterKey.
// Keyed by the query plus a cheap identity of the Areas list (len +
// first/last name), which changes whenever the server resends the area list.
// It deliberately does NOT key on AreaInfo (player counts / status / lock):
// those change on every area packet, and the name filter doesn't depend on
// them, so including them would defeat the memo and re-scan constantly.
type areaFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshAreaFilter returns the matching area indices for a non-empty query,
// memoized against the query + the list identity — same O(1)-per-frame guard as
// refreshMusicFilter. The indices are ORIGINAL indices into a.sess.Areas, which
// parallels a.sess.AreaInfo, so a caller uses ti (not the visible index) to look
// up either.
//
// slot names the query SOURCE (areaQuerySlotOwn/Themed), never the drawer: two
// panels reading the same box share a slot and hit the same memo entry, while the
// two boxes cannot evict each other.
func (a *App) refreshAreaFilter(query string, slot int) []int {
	n := len(a.sess.Areas)
	key := areaFilterKey{q: query, n: n}
	if n > 0 {
		key.first, key.last = a.sess.Areas[0], a.sess.Areas[n-1]
	}
	if key == a.areaFilterMemo[slot] {
		return a.areaFiltered[slot]
	}
	a.areaFilterMemo[slot] = key
	out := a.areaFiltered[slot][:0]
	for i, area := range a.sess.Areas {
		if strings.Contains(strings.ToLower(area), query) {
			out = append(out, i)
		}
	}
	a.areaFiltered[slot] = out
	return out
}

// drawMusicVolume is the Music tab's pure volume menu (toggled in place of the
// track list) — Master + the three channels, full sliders + readouts. Reachable
// on a legacy AO2 theme too, since the theme draws drawMusicList directly.
func (a *App) drawMusicVolume(r sdl.Rect) {
	c := a.ctx
	c.LabelClipped(r.X, r.Y, r.W, "Volume — the IC box stays live below, so adjust and keep chatting.", ColTextDim)
	y := r.Y + 30
	// FIX #9 parity (playtest: the Music tab's Volume control did nothing): drive the
	// EFFECTIVE (per-server when connected) volumes, exactly like the strip and the
	// Settings/Extras sliders. This menu used to read/write the GLOBAL prefs, which a
	// connected server's per-server audio profile overrides — so as soon as volume had
	// been touched anywhere else (those all write per-server), these sliders moved
	// values nothing was listening to.
	master, music, sfx, blip := a.effectiveVolumes()
	row := func(id, label string, val int) int {
		c.Label(r.X, y+6, label, ColText)
		track := sdl.Rect{X: r.X + 80, Y: y + 7, W: r.W - 80 - 56, H: 16}
		nv := int(c.Slider("musicvol:"+id, track, int32(val), 100))
		c.Label(r.X+r.W-48, y+6, strconv.Itoa(nv)+"%", ColTextDim)
		y += 40
		return nv
	}
	if nv := row("master", "Master", master); nv != master {
		a.setEffectiveVolumes(nv, music, sfx, blip)
	}
	if nv := row("music", "Music", music); nv != music {
		a.setEffectiveVolumes(master, nv, sfx, blip)
	}
	if nv := row("sfx", "SFX", sfx); nv != sfx {
		a.setEffectiveVolumes(master, music, nv, blip)
	}
	if nv := row("blip", "Blip", blip); nv != blip {
		a.setEffectiveVolumes(master, music, sfx, nv)
	}
	// Blip rate, right under the Blip volume — the same cadence control as the volume
	// strip's "Rate" column and Settings → Blips, mirrored here so it's adjustable straight
	// from the Music menu's volume view. Range is clamped like the strip (left = faster).
	rate, onSpaces := a.d.Prefs.Load().BlipTyping()
	c.Label(r.X, y+6, "Blip rate", ColText)
	rtrack := sdl.Rect{X: r.X + 80, Y: y + 7, W: r.W - 80 - 56, H: 16}
	nr := value.Clamp(c.Slider("musicvol:rate", rtrack, int32(rate), int32(config.MaxBlipRate)), int32(config.MinBlipRate), int32(config.MaxBlipRate))
	c.Tooltip(rtrack, "Blip cadence: 1 blip every N letters (left = faster)")
	c.Label(r.X+r.W-48, y+6, "1/"+strconv.Itoa(int(nr)), ColTextDim)
	if int(nr) != rate {
		a.d.Prefs.SetBlipTyping(int(nr), onSpaces)
		a.applyTimingToRoom()
	}
}

// themed reports that this panel is drawing INSIDE a theme's design canvas, where
// the theme declares its own music_slider / sfx_slider / blip_slider rects and those
// win (issue #21, owner decision). The Volume toggle is AsyncAO's own affordance for
// the same job, so offering both would put two sets of volume controls on one screen.
// Master volume and blip rate have no AO2 rect and stay reachable in Settings.
func (a *App) drawMusicList(r sdl.Rect, themed, searchExternal, nowPlayingExternal bool) {
	c := a.ctx
	if a.musicPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.musicPct = a.logPct
	}
	// See drawPlayerList. AFTER the zoom is settled, and BEFORE the volume-mode
	// early return, so the sliders and their labels are dressed too.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemMusicList, a.musicPct)))
	// Ctrl+wheel (or wheel-button) resizes the track text (musicPct). Lives HERE
	// — not just the classic log panel — so it also works on the THEMED courtroom
	// (which draws drawMusicList directly). In classic the panel already took the
	// wheel (wheelTaken), so this no-ops there; no double-step.
	a.zoomWheel(r, &a.musicPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	// Inside a theme's canvas the theme's own slider rects are the volume surface,
	// so the volume view is not offered (#21). volMode is forced FALSE rather than
	// merely hiding its control: musicVolMode persists across restarts, so a user
	// who left it on and then applied a theme would otherwise open to a volume view
	// with no way out of it.
	volMode := a.musicVolMode && !themed
	// Now-Playing indicator: the current track from the server's MC (cleared on
	// stop / area transfer), so you can see and silence what's playing.
	//
	// nowPlayingExternal means the THEME declared a music_display plate and the
	// themed pass already painted the name on it (AO2 parents ui_music_name inside
	// that plate — courtroom.cpp:171), so only the READOUT is dropped. The row
	// itself belongs to the header buttons and is placed either way; that coupling
	// is exactly what used to paint Expand all on top of Stop music.
	now := ""
	if a.room != nil {
		now = a.room.Scene.MusicTrack
	}
	nowLabel := musicNothingPlaying
	if now != "" {
		// Same short name the list rows and the IC "has played a song" line use
		// (courtroom.MusicDisplayName — AO2 list_music), so the three agree.
		nowLabel = a.musicNowLabel.text(now)
	}
	// The search row is dropped WHOLE — draw and reserved height — in two cases:
	// the theme declared its own music_search rect (searchExternal, below), and the
	// volume view has replaced the track list. Filtering a list that is not on
	// screen is nothing but ~26 px stolen from the sliders, which is what the old
	// code's early return used to prevent.
	hideSearchRow := searchExternal || volMode
	total := len(a.sess.Music)
	// planMusicHeader needs the count's WIDTH to size the search field, so a label
	// has to exist BEFORE the field draws — but the count's NUMBERS must not be
	// computed up here. The TextField returns this frame's text, so a query read
	// before it runs is last frame's, and every result (count, filtered rows, the
	// empty-state hint) then trails typing by a frame. The measurement therefore
	// uses last frame's outcome — a.musicFiltered is already populated, so no extra
	// scan — and the live query/filter/label are all evaluated after the draw below.
	prevShown := total
	if strings.TrimSpace(a.musicSearch) != "" {
		prevShown = len(a.musicFiltered)
	}
	plan := planMusicHeader(r, nowLabel, a.musicCount.text(prevShown, total), hideSearchRow, nowPlayingExternal, c.TextWidth)

	// --- header row 1: the four actions, the overflow menu, the readout ---
	if br, ok := plan.rect(musicItemStop); ok {
		if a.musicIconButton(br, iconStop, "Stop the current song (also the M hotkey)") {
			a.stopMusic()
		}
	}
	if br, ok := plan.rect(musicItemRandom); ok {
		if a.musicIconButton(br, iconDice, "Play a random song from the current playlist") {
			a.playRandomMusic()
		}
	}
	if br, ok := plan.rect(musicItemExpand); ok {
		if a.musicIconButton(br, iconExpandAll, "Expand all categories") {
			a.setAllMusicCollapsed(false)
		}
	}
	if br, ok := plan.rect(musicItemCollapse); ok {
		if a.musicIconButton(br, iconCollapseAll, "Collapse all categories") {
			a.setAllMusicCollapsed(true)
		}
	}
	if br, ok := plan.rect(musicItemMenu); ok {
		if a.musicIconButton(br, iconKebab, "More: playback options, volume sliders (right-click the list opens this too)") {
			// FromButton, not openMusicMenu: the press has to be consumed here or
			// drawMusicMenu's click-away test closes the menu the same frame it
			// opened (see openMusicMenuFromButton).
			a.openMusicMenuFromButton(sdl.Point{X: br.X, Y: br.Y + br.H}, themed)
		}
	}
	// #39: "music_name" is AO2's own id for this line (Courtroom::set_fonts,
	// courtroom.cpp:1201). elemLabelFont keeps the fixed chrome face when the theme
	// says nothing, so an undressed client draws exactly what it did before; real
	// themes set music_name_bold = 1, hence the weighted draw.
	if br, ok := plan.rect(musicItemNowPlaying); ok {
		nameFont := a.elemLabelFont(elemMusicName, DefaultScalePct, nowLabel)
		// #21 label 16: "music_name_color". themed IS the canvas flag here — the two
		// mean the same thing at every call site (see the doc comment above).
		nameInk := ColTextDim
		if now != "" {
			nameInk = a.elemInkOr(elemMusicName, themed, ColAccent)
		}
		ty := br.Y + (br.H-fontLineH(nameFont))/2
		c.LabelClippedFontWeight(nameFont, br.X, ty, br.W, nowLabel, nameInk, a.elemBold(elemMusicName))
	}
	// --- header row 2: the search filter and its shown/total count ---
	//
	// searchExternal means the THEME declared its own music_search rect and the
	// themed pass already drew the field there (AO2 puts it outside music_list —
	// courtroom.cpp:219-222), so the row is skipped entirely and the track list
	// gets those pixels back. Volume mode skips it for its own reason (hideSearchRow).
	if br, ok := plan.rect(musicItemSearch); ok {
		a.musicSearch, _ = c.TextField("musicsearch", sdl.Rect{X: br.X, Y: br.Y, W: br.W, H: fieldH}, a.musicSearch, "Search music…  (Ctrl+wheel resizes)")
	}
	// Evaluated AFTER the field, so the filter, the count and the rows below all run
	// on the text typed THIS frame — see the measurement note above the plan.
	query := strings.ToLower(strings.TrimSpace(a.musicSearch))
	shown := total
	if query != "" {
		a.refreshMusicFilter(query)
		shown = len(a.musicFiltered)
	}
	if br, ok := plan.rect(musicItemCount); ok {
		c.Label(br.X, br.Y+plStripLabelOffY, a.musicCount.text(shown, total), ColTextDim)
	}
	r.Y += plan.h
	r.H -= plan.h
	if volMode { // pure volume menu in place of the track list
		a.drawMusicVolume(r)
		return
	}

	// Hover-gated (playtest: the music list scrolled from anywhere).
	if !c.ctrlHeld { // ctrl+wheel resizes text (musicPct), never scrolls
		a.musicScroll -= c.WheelIn(r) * scrollStepPx
	}
	// #39: the theme's "music_list" size/family, folded with the music zoom
	// (independent of the IC log scale).
	font := a.elemFont(elemMusicList, a.musicPct)
	musicBold := a.elemBold(elemMusicList) // music_list_bold → the row's real bold cut
	// #21 label 16: "music_list_color" is the TRACK rows' ink. Category headers keep
	// ColAccent — they are filled ColPanelHi and the [-]/[+] marker is AsyncAO's own
	// collapse affordance, not one of AO2's tree items.
	trackInk := a.elemInkOr(elemMusicList, themed, ColText)
	lineH := int32(font.Height()) + 10
	a.refreshMusicGroups(query) // grouped + (search: auto-expanded) row layout — cached, see screens.go
	rows := a.musicRows
	contentH := int32(len(rows)) * lineH
	bar := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.musicScroll = c.VScrollbar("musicscroll", bar, a.musicScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r) // partial top/bottom row stays inside the panel
	defer c.popClip(clipPrev, clipHad)
	y := r.Y - a.musicScroll
	for _, mr := range rows {
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH && mr.ti >= 0 && mr.ti < len(a.sess.Music) {
			entry := a.sess.Music[mr.ti]
			row := sdl.Rect{X: r.X, Y: y, W: r.W - scrollBarW - scrollBarGap, H: lineH - 4}
			hover := c.hovering(row)
			if mr.header {
				c.Fill(row, ColPanelHi) // section header, like drawJukeGroupHeader (music_history.go)
				// ASCII fold marker, not a ▶/▼ glyph — LogFont's primary face isn't
				// guaranteed to cover Geometric Shapes (see jukebox.go's "Play" button
				// comment: those tofu on fonts without that block).
				glyph := "[-] " // expanded
				if a.musicCollapsed[mr.ti] {
					glyph = "[+] " // collapsed
				}
				header := glyph + musicCategoryLabel(entry)
				c.LabelClippedFontWeight(font, r.X+4, y+4, row.W-8, header, ColAccent, musicBold)
				if c.ClickedIn(row) { // toggle just this category, not a track request
					if a.musicCollapsed == nil {
						a.musicCollapsed = make(map[int]bool)
					}
					if a.musicCollapsed[mr.ti] {
						delete(a.musicCollapsed, mr.ti)
					} else {
						a.musicCollapsed[mr.ti] = true
					}
					a.musicCollapseGen++
				}
			} else {
				if hover {
					c.Fill(row, ColPanelHi)
				}
				// #43: show AO2's DISPLAY name (directory + extension cut —
				// courtroom.MusicDisplayName mirrors Courtroom::list_music), not the
				// whole wire entry. The click below still sends `entry` VERBATIM: the
				// display is a pure substring, the MC packet and the music URL are
				// built from the raw name.
				display := courtroom.MusicDisplayName(entry)
				c.LabelClippedFontWeight(font, r.X+20, y+4, row.W-24, display, trackInk, musicBold) // indented under its category, tree-style
				if hover {
					c.Tooltip(row, entry) // the RAW entry on hover — the full path is still one hover away
				}
				if c.ClickedIn(row) { // press+release in-row: a scrollbar-drag release must not play a track
					a.requestMusicTrack(entry) // wire name byte-exact, plus AO2's showname/flags tail
				}
			}
		}
		y += lineH
	}
	if len(rows) == 0 {
		hint := "Server sent no music list."
		if query != "" {
			hint = "No tracks match your search."
		}
		c.Label(r.X+4, r.Y+6, hint, ColTextDim)
	}
	// Right-click anywhere in the list opens the same overflow menu the ⋮ button
	// does — AO2's real entry point for these actions is
	// on_music_list_context_menu_requested (courtroom.cpp:5821), so the button is
	// the discoverable route and this is the shortcut.
	if c.rightClicked && c.hovering(r) {
		a.openMusicMenu(sdl.Point{X: c.mouseX, Y: c.mouseY}, themed)
		c.rightClicked = false
	}
}

// musicCategoryLabel strips a category header's decorative "==" wrapper
// (the AO2 convention for marking a music-list entry as a section header,
// e.g. "==Cave Story OST==") for display now that folding gives it a real
// header row instead of relying on the punctuation to read as one. Left
// untouched if the server didn't use that convention.
func musicCategoryLabel(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "==") && strings.HasSuffix(trimmed, "==") && len(trimmed) > 4 {
		return strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	}
	return name
}

// scaleControl draws one "Name − +" layout knob; steps mutate *value
// within [min, max] and persist the layout. Returns the next x.
func (a *App) scaleControl(x, y int32, name string, value *int, step, min, max int) int32 {
	c := a.ctx
	x += c.Label(x, y+6, name, ColTextDim) + 4
	if c.Button(sdl.Rect{X: x, Y: y, W: 22, H: btnH}, "-") && *value-step >= min {
		*value -= step
		a.saveLayout()
	}
	x += 24
	if c.Button(sdl.Rect{X: x, Y: y, W: 22, H: btnH}, "+") && *value+step <= max {
		*value += step
		a.saveLayout()
	}
	return x + 30
}
