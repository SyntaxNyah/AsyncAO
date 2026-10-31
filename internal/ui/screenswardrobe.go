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

// iniFolderMenuOpts builds the character menu rows: remove, unfile, each folder, and "+ new: X" when the new-folder
// field has text.
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

// iniDragThreshold is the cursor travel (logical px) past which a press on a wardrobe cell becomes a drag-to-file.
const iniDragThreshold = 6

// handleIniFolderMenu resolves a click on the open menu before the grid draws, so it never leaks to a cell. The click
// is always consumed.
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

// drawIniFolderMenu paints the menu on top, highlighting the character's current folder.
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

// Wardrobe sections.
const (
	wardSectionCharacters = iota
	wardSectionBackgrounds
	wardSectionIniswaps
	wardSectionJukebox
)

// drawIniswapPanel is the wardrobe modal shell. It owns the drag press edge and end-of-frame cleanup, which must run
// exactly once per frame so a drag can't arm twice or fail to clear.
func (a *App) drawIniswapPanel(w, h int32) {
	c := a.ctx
	a.iniPressed = c.mouseDown && !a.iniPrevDown // mouse went down this frame (drag arm; shared)
	// Clear of the app-chrome band: the menu bar and tab strip paint after the screens and would cover the panel's top.
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
			// Each section owns its own preview and dwell, so go through closeSpritePreview rather than blanking
			// previewBase.
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
	// While a bind waits for a key, say so in the header, including that Esc or a click cancels.
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
	// Drag ghost: a label trailing the cursor, for whichever section armed the drag.
	if a.iniDragging && a.iniDragChar != "" {
		g := sdl.Rect{X: c.mouseX + 12, Y: c.mouseY + 8, W: c.TextWidth(a.iniDragChar) + 16, H: 22}
		c.Fill(g, sdl.Color{R: 0, G: 0, B: 0, A: 225})
		c.Border(g, ColAccent)
		c.Label(g.X+6, g.Y+3, a.iniDragChar, ColAccent)
	}
	// End-of-frame drag cleanup, once per frame for all sections.
	if !c.mouseDown {
		a.iniDragChar = ""
		a.iniDragging = false
	}
	a.iniPrevDown = c.mouseDown
}

func (a *App) drawWardrobeCharsBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	a.pollIniswap()
	a.pollPreviewEmotes() // try-before-wear: drain a previewed char's emote list
	a.iniHoverChar = ""   // recomputed by the cells this frame (quick-file target)
	// Handle the menu's clicks first so they can't reach the grid underneath; it paints last.
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
	// Clicking a favourite switches character (the CC → PV flow); ticking this wears it as an iniswap instead. Per
	// session.
	const swapLabel = "Iniswap instead of switching character"
	a.iniSwapMode = c.Checkbox(panel.X+pad, y, swapLabel, a.iniSwapMode)
	c.Tooltip(sdl.Rect{X: panel.X + pad, Y: y, W: 22 + c.TextWidth(swapLabel), H: 16},
		"Off (default): clicking a character switches you to them (takes a slot). On: wears them as an iniswap — no slot taken.")
	y += 32
	a.iniSearch, _ = c.TextField("iniswapsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.iniSearch, "Search...")
	// Add any folder name on the current asset base; no server list required.
	var addNow bool
	a.iniAdd, addNow = c.TextField("iniswapadd", sdl.Rect{X: panel.X + pad + 240, Y: y, W: 230, H: fieldH}, a.iniAdd, "Add char (or folder/char) to wardrobe...")
	if c.Button(sdl.Rect{X: panel.X + pad + 476, Y: y, W: 60, H: btnH}, "Add") || addNow {
		// "folder/char" files it explicitly; otherwise it joins the folder in view ("" at the top level = unfiled).
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
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad,
		"Your saved characters, kept per server. ★ a character in Character Select (or use Add above) to save it; drag a card onto a folder to organise; click a card to wear it.", ColTextDim)
	y += 22

	// Folders are membership-derived; a name typed below makes a transient folder cell so the first character has
	// something to drop onto. A search spans every folder.
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
		// Characters is the user's wardrobe only: starred or filed. The full server list lives on the Iniswaps tab.
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

	// Grid: folder icons (top level only), then character cells. Cells always pass their iniList index to
	// drawIniswapCell, not the grid position, so the index-keyed cachedPage stays correct.
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

	// Number-key quick-file: hover a character and press 1-9 for that folder, 0 for unsorted.
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

	// The menu paints last, above the grid and preview.
	if a.iniMenuChar != "" {
		opts := a.iniFolderMenuOpts()
		a.drawIniFolderMenu(iniFolderMenuRect(a.iniMenuAt, len(opts), w, h), opts)
	}
}

// drawWardrobeIniswapsBody is the Iniswaps section, a flat browse from two sources.
// "Server list" is the published iniswap.txt; it renders from a.iniList with the same indices as the Characters grid,
// so the index-keyed thumbnail cache stays valid across a tab switch.
// "From your base" is the server roster plus local mounts' characters/ folders, for servers that publish no list. It
// keeps its own list and draws name buttons for that same cache reason (iniswapbase.go).
func (a *App) drawWardrobeIniswapsBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	a.pollIniswap()            // drain the server list (fetched on open)
	a.pollLocalCharFolders()   // ...and the local-mount folder scan (iniswapbase.go)
	a.ensureLocalCharFolders() // starts one only when the mount list actually changed
	a.iniHoverChar = ""        // recomputed by the cells (quick-file target; unused here)
	serverN, baseN := a.baseSourceCounts()
	// Memoized, because this body draws every frame the tab is up (iniswapbase.go).
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
		// No iniswap.txt on this server is not an error; the base browser below is the answer.
		c.LabelClipped(statusX, y+4, panel.X+panel.W-statusX-pad, "This server doesn't publish an "+iniswapFileName+" list.", ColTextDim)
	default:
		c.Label(statusX, y+4, statusLabel, ColTextDim)
	}
	y += 30

	// A server with no published list opens on the base browser, once per server (autoDone/autoKey).
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

	// No server list: a short guide for owners (where to put iniswap.txt) and players (any folder works by name).
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
	// This tab is the server's iniswap.txt only; a favourite that isn't a server iniswap stays out.
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

// drawFolderShape paints a folder icon sized to the cell, shared by both wardrobe sections. Pure graphics with no
// cachedPage, so it is safe at any grid position.
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

// folderDeleteHit draws the delete (×) button on a hovered folder when no drag is in flight, and reports a click on it.
// The × claims its click so the folder doesn't open.
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

// drawFolderDeleteConfirm draws the "delete folder?" bar and returns this frame's choice. noun is "characters" or
// "backgrounds". Esc cancels.
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

// drawIniFolderCell draws one Characters-section folder: click opens, a drop files, the hover × deletes.
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

// cellClick is what clicking a wardrobe cell does: always iniswap, or switch to the character, falling back to iniswap
// when it's taken, not a server char, or the box is ticked.
type cellClick int

const (
	cellClickIniswap cellClick = iota
	cellClickChar
)

// The wardrobe cell's fixed-size corner chrome, in pixels.
const (
	// iniCellStarPx is the ★ box's edge, top-right, inset one pixel.
	iniCellStarPx int32 = 17
	// iniCellStarLaneW is how much of the right edge the folder tag leaves clear for the star.
	iniCellStarLaneW int32 = 22
	// iniCellTagH is the folder tag's height, top-left.
	iniCellTagH int32 = 15
	// iniCellKeyBadgeH is the key badge's height, bottom-left, inset one pixel.
	iniCellKeyBadgeH int32 = 16
)

// caption draws the folder name under the cell. The char-select Wardrobe tab turns it off on a theme canvas, where it
// would print across the next row's frame (charGridPlan).
func (a *App) drawIniswapCell(idx int, cell sdl.Rect, click cellClick, caption bool) {
	c := a.ctx
	name := a.iniList[idx]

	// Registered before the star and key-badge tooltips, which override it on their own rects (last write wins).
	switch {
	case !caption:
		// No caption, so the tooltip carries the name, as in AO2; with no icon nothing else tells two folders apart.
		c.Tooltip(cell, name)
	case click == cellClickChar:
		c.Tooltip(cell, "Click to switch to this character (take its slot). Tick \"Iniswap instead\" above to wear it without a slot.")
	default:
		c.Tooltip(cell, "Click to iniswap into this folder — wear its look, no slot taken.")
	}

	// A press arms this character as the drag candidate; past iniDragThreshold it becomes a ghost that drops on a
	// folder. A press that never moves is a click, and the click actions check !a.iniDragging.
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
		// Shared scratch, not &cell: a cgo pointer taken from a parameter moves it to the heap on every call, including
		// the branch that never touches SDL. SDL copies the rect during the call and never keeps the pointer (ui.go
		// cgoRect).
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

	// Corner chrome draws at the client font's size whatever the cell, so behind cellFitsCornerChrome: on a tiny cell
	// it would cover the art and eat the click aimed at it.
	if cellFitsCornerChrome(cell) {
		// Folder tag (top-left): the category this character is filed under.
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

		// Wardrobe star (top-right): toggle membership without wearing.
		star := sdl.Rect{X: cell.X + cell.W - iniCellStarPx - 1, Y: cell.Y + 1, W: iniCellStarPx, H: iniCellStarPx}
		starCol := ColTextDim
		if idx < len(a.iniWardrobe) && a.iniWardrobe[idx] {
			starCol = ColStar
		}
		c.IconLabel(star, "★", starCol)
		c.Tooltip(star, "★ add to / remove from your wardrobe (right-click the cell for more)")
		if c.hovering(star) && c.clicked && !a.iniDragging {
			// Defer the toggle: rebuilding the wardrobe here shrinks the slices the grid loop is ranging and panics on
			// a remove. The host applies it after its loop (drawIniswapPanel, drawWardrobeGrid).
			a.iniFavPending = name
			a.iniFavPendingAdd = idx >= len(a.iniWardrobe) || !a.iniWardrobe[idx]
			c.clicked = false // consumed; don't also wear it
			return
		}

		// Key badge (bottom-left): the chord that wears this character, or "+key" on hover. Click arms capture;
		// right-click clears. It uses bindChipLabel because handleCharKeys fires on Alt+<key> (bareBindKey, qol.go), so
		// a bare letter would advertise a press that types into the IC box.
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

	// Right-click opens the move-to-folder menu. The key badge's own right-click ran first.
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

// musicFilterKey memoizes the music filter so a list of thousands isn't re-lowered every frame. Keyed by the query plus
// a cheap list identity (len, first and last track), which changes when the server resends SM.
type musicFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshMusicFilter recomputes the matching track indices when the query or list identity changes. It matches the raw
// entry: MusicDisplayName only slices, so the display is a substring, and a directory ("songs/") stays findable.
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

// musicRow is one row of the Music tab: a category header (a "==Category==" entry with no audio extension, see
// courtroom.HasAudioExt) or a track, by original index into a.sess.Music.
type musicRow struct {
	ti     int
	header bool
}

// musicGroupKey memoizes the grouped row layout like musicFilterKey, plus musicCollapseGen so a fold invalidates it
// without re-keying on the whole map.
type musicGroupKey struct {
	q           string
	n           int
	first, last string
	collapseGen uint64
}

// refreshMusicGroups rebuilds the Music tab's visible rows when its key changes. A search bypasses collapse state:
// matches always show, with their header for context, and manual state resumes when the query clears.
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

// setAllMusicCollapsed sets every header's fold state and bumps musicCollapseGen once, not once per category.
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

// areaFilterKey memoizes the areas filter like musicFilterKey. Not keyed on AreaInfo: it changes on every area packet
// and the name filter doesn't depend on it.
type areaFilterKey struct {
	q           string
	n           int
	first, last string
}

// refreshAreaFilter returns matching original indices into a.sess.Areas, which parallels a.sess.AreaInfo; memoized like
// refreshMusicFilter.
// slot names the query source (areaQuerySlotOwn/Themed), not the drawer: panels reading one box share an entry, and two
// boxes can't evict each other.
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

// drawMusicVolume is the Music tab's volume view, in place of the track list: Master, the three channels and blip rate.
func (a *App) drawMusicVolume(r sdl.Rect) {
	c := a.ctx
	c.LabelClipped(r.X, r.Y, r.W, "Volume — the IC box stays live below, so adjust and keep chatting.", ColTextDim)
	y := r.Y + 30
	// Drive the effective (per-server when connected) volumes, like the strip and Settings; a per-server profile
	// overrides the global prefs, so sliders on those would do nothing.
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
	// Blip rate, the same control as the volume strip's "Rate" column and Settings → Blips; left is faster.
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

// themed means this panel draws inside a theme's canvas, whose music_slider / sfx_slider / blip_slider rects win over
// the Volume toggle.
func (a *App) drawMusicList(r sdl.Rect, themed, searchExternal, nowPlayingExternal bool) {
	c := a.ctx
	if a.musicPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.musicPct = a.logPct
	}
	// After the zoom is settled and before the volume-mode return, so the sliders are dressed too.
	defer c.popPanelFont(c.pushPanelFont(a.elemChromeFont(elemMusicList, a.musicPct)))
	// Here as well as in the classic log panel, so it works on the themed courtroom; in classic the panel already took
	// the wheel, so no double step.
	a.zoomWheel(r, &a.musicPct, config.MinLogScalePercent, config.MaxLogScalePercent)
	// volMode is forced false, not just its control hidden: musicVolMode persists, so a user who left it on and applied
	// a theme would open to a volume view with no way out.
	volMode := a.musicVolMode && !themed
	// nowPlayingExternal means the theme's music_display plate already painted the name (courtroom.cpp:171), so only
	// the readout is dropped, not the row.
	now := ""
	if a.room != nil {
		now = a.room.Scene.MusicTrack
	}
	nowLabel := musicNothingPlaying
	if now != "" {
		// The short name, matching the list rows and the IC "has played a song" line (AO2 list_music).
		nowLabel = a.musicNowLabel.text(now)
	}
	// The search row is dropped whole when the theme declared music_search or the volume view replaced the track list.
	hideSearchRow := searchExternal || volMode
	total := len(a.sess.Music)
	// The plan needs the count's width before the field draws, but the field returns this frame's text, so measure with
	// last frame's outcome and evaluate the live query after the draw. Otherwise results trail typing by a frame.
	prevShown := total
	if strings.TrimSpace(a.musicSearch) != "" {
		prevShown = len(a.musicFiltered)
	}
	plan := planMusicHeader(r, nowLabel, a.musicCount.text(prevShown, total), hideSearchRow, nowPlayingExternal, c.TextWidth)

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
			// FromButton: the press must be consumed or drawMusicMenu's click-away closes the menu the frame it opened.
			a.openMusicMenuFromButton(sdl.Point{X: br.X, Y: br.Y + br.H}, themed)
		}
	}
	// "music_name" is AO2's id for this line (courtroom.cpp:1201). elemLabelFont keeps the chrome face when the theme
	// says nothing; real themes set music_name_bold = 1.
	if br, ok := plan.rect(musicItemNowPlaying); ok {
		nameFont := a.elemLabelFont(elemMusicName, DefaultScalePct, nowLabel)
		// "music_name_color"; themed is the canvas flag here.
		nameInk := ColTextDim
		if now != "" {
			nameInk = a.elemInkOr(elemMusicName, themed, ColAccent)
		}
		ty := br.Y + (br.H-fontLineH(nameFont))/2
		c.LabelClippedFontWeight(nameFont, br.X, ty, br.W, nowLabel, nameInk, a.elemBold(elemMusicName))
	}
	// searchExternal means the theme drew the field in its own rect, outside music_list (courtroom.cpp:219-222); the
	// plan then skips this row.
	if br, ok := plan.rect(musicItemSearch); ok {
		a.musicSearch, _ = c.TextField("musicsearch", sdl.Rect{X: br.X, Y: br.Y, W: br.W, H: fieldH}, a.musicSearch, "Search music…  (Ctrl+wheel resizes)")
	}
	// After the field, so the filter, count and rows all use the text typed this frame.
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

	if !c.ctrlHeld { // ctrl+wheel resizes text (musicPct), never scrolls
		a.musicScroll -= c.WheelIn(r) * scrollStepPx
	}
	// The theme's "music_list" size and family, folded with the music zoom.
	font := a.elemFont(elemMusicList, a.musicPct)
	musicBold := a.elemBold(elemMusicList) // music_list_bold → the row's real bold cut
	// "music_list_color" is the track rows' ink. Category headers keep ColAccent: the [-]/[+] marker is AsyncAO's, not
	// one of AO2's tree items.
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
				// ASCII fold marker: LogFont's primary face isn't guaranteed to cover Geometric Shapes.
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
				// AO2's display name (courtroom.MusicDisplayName mirrors Courtroom::list_music). The click sends the
				// raw entry: the MC packet and URL are built from it.
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
	// Right-click opens the same menu as the ⋮ button; AO2's entry point is on_music_list_context_menu_requested
	// (courtroom.cpp:5821).
	if c.rightClicked && c.hovering(r) {
		a.openMusicMenu(sdl.Point{X: c.mouseX, Y: c.mouseY}, themed)
		c.rightClicked = false
	}
}

// musicCategoryLabel strips a category header's decorative "==" wrapper for display, if the server used it.
func musicCategoryLabel(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.HasPrefix(trimmed, "==") && strings.HasSuffix(trimmed, "==") && len(trimmed) > 4 {
		return strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	}
	return name
}

// scaleControl draws one "Name − +" layout knob, stepping *value within [min, max] and saving the layout; returns the
// next x.
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
