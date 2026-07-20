package ui

// The wardrobe's Backgrounds section: your favourite backgrounds (the ★ set
// from the Background picker) organized into the same navigable folders as
// characters. Clicking a favourite asks the server to change the room to it.
// Favourites live in config.FavBackgrounds; folders are a per-server
// background→folder map (config.FavBackgroundFolder). bgFavList is the
// favourites in ONE stable order so the index-keyed bgFavPages thumbnail cache
// survives folder navigation (navigation filters via a predicate; see the
// cachedPage reorder invariant).

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// rebuildBgFav refreshes the Backgrounds-section list from prefs. Called when
// the section opens and after any favourite/folder change — NOT per frame and
// NOT on folder navigation. Nulls the thumbnail cache because the set/order
// changed (the cache is keyed by index without re-checking the URL).
func (a *App) rebuildBgFav() {
	favs := a.d.Prefs.FavBackgroundList(a.serverKey)
	folders := a.d.Prefs.FavBackgroundFolderMap(a.serverKey)
	a.bgFavList = favs
	a.bgFavLower = make([]string, len(favs))
	a.bgFavFolders = make([]string, len(favs))
	for i, n := range favs {
		lower := strings.ToLower(n)
		a.bgFavLower[i] = lower
		a.bgFavFolders[i] = folders[lower] // "" = unfiled
	}
	a.bgFavAsk = nil
	a.bgFavPages = nil // set/order changed → drop the index-keyed thumbnail cache
}

// bgFavFolderNames lists the distinct background folders in first-seen order.
func (a *App) bgFavFolderNames() []string {
	var out []string
	seen := map[string]struct{}{}
	for _, f := range a.bgFavFolders {
		if f == "" {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

// drawWardrobeBgsBody draws the Backgrounds section grid (favourite backgrounds
// + folder icons). Mirrors drawWardrobeCharsBody; the shared drag bookkeeping
// is in the drawIniswapPanel wrapper.
func (a *App) drawWardrobeBgsBody(panel sdl.Rect, w, h int32) {
	c := a.ctx
	y := panel.Y + 44
	a.bgFavSearch, _ = c.TextField("bgfavsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.bgFavSearch, "Search favourites...")
	c.LabelClipped(panel.X+pad+250, y+4, panel.X+panel.W-(panel.X+pad+250)-pad, fmt.Sprintf("%d favourite backgrounds — click one to change the room to it", len(a.bgFavList)), ColTextDim)
	y += 36

	// Empty state: favourites are added by starring in the Background picker.
	if len(a.bgFavList) == 0 {
		c.LabelClipped(panel.X+pad, y+4, panel.W-2*pad, "No favourite backgrounds yet — open the Background picker (the courtroom Background button), press ★ on the ones you like, and they show up here to organize into folders.", ColTextDim)
		return
	}

	query := strings.ToLower(strings.TrimSpace(a.bgFavSearch))
	searching := query != ""
	folderCells := a.bgFavFolderNames()
	if nf := strings.TrimSpace(a.bgFavNewFold); nf != "" { // a just-typed folder is a drop target before it has members
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
	visible := func(i int) bool {
		if searching { // search spans every folder
			return strings.Contains(a.bgFavLower[i], query)
		}
		return strings.EqualFold(a.bgFavFolders[i], a.bgFavFolder) // "" = top level = unfiled
	}
	countIn := func(name string) int {
		n := 0
		for _, f := range a.bgFavFolders {
			if strings.EqualFold(f, name) {
				n++
			}
		}
		return n
	}

	if a.wardDelFolder != "" {
		// A folder delete awaits confirmation: the bar replaces the nav row.
		choice := a.drawFolderDeleteConfirm(panel.X+pad, y, panel.W-2*pad, countIn(a.wardDelFolder), "backgrounds")
		if choice == folderDeleteWithItems || choice == folderDeleteKeepItems {
			a.d.Prefs.DeleteFavBackgroundFolder(a.serverKey, a.wardDelFolder, choice == folderDeleteKeepItems)
			if strings.EqualFold(a.bgFavNewFold, a.wardDelFolder) {
				a.bgFavNewFold = "" // also drop the transient "new folder" chip if it named this one
			}
			if strings.EqualFold(a.bgFavFolder, a.wardDelFolder) { // we were inside the deleted folder
				a.bgFavFolder = ""
				a.bgFavScroll = 0
			}
			a.wardDelFolder = ""
			a.rebuildBgFav()
		} else if choice == folderDeleteCancel {
			a.wardDelFolder = ""
		}
	} else {
		switch {
		case searching:
			c.Label(panel.X+pad, y+4, "Search spans every folder — clear it to browse folders", ColTextDim)
		case a.bgFavFolder == "":
			a.bgFavNewFold, _ = c.TextField("bgfavnewfold", sdl.Rect{X: panel.X + pad, Y: y, W: 240, H: fieldH}, a.bgFavNewFold, "New folder name, then drag backgrounds onto it")
			c.LabelClipped(panel.X+pad+250, y+4, panel.X+panel.W-(panel.X+pad+250)-pad, "Drag a background onto a folder to file it · open a folder to see inside · × on a folder deletes it", ColTextDim)
		default:
			back := sdl.Rect{X: panel.X + pad, Y: y, W: 150, H: btnH}
			c.Fill(back, ColPanel)
			if a.iniDragging && c.hovering(back) {
				c.Border(back, ColAccent) // drop here to take the background out of the folder
			} else {
				c.Border(back, ColPanelHi)
			}
			c.Label(back.X+8, back.Y+5, "‹ All folders", ColText)
			c.Tooltip(back, "Back to all folders — or drop a background here to take it out of this folder")
			if c.hovering(back) && c.clicked {
				if a.iniDragging && a.iniDragChar != "" {
					a.d.Prefs.SetFavBackgroundFolder(a.serverKey, a.iniDragChar, "")
					a.rebuildBgFav()
					c.clicked = false // consume the drop
				} else {
					a.bgFavFolder = ""
					a.bgFavScroll = 0
				}
			}
			c.LabelClipped(back.X+back.W+12, y+5, panel.X+panel.W-(back.X+back.W+12)-pad, fmt.Sprintf("%s — %d background(s)", a.bgFavFolder, countIn(a.bgFavFolder)), ColAccent)
		}
	}
	y += btnH + 8

	// Grid: landscape thumbnails (like the picker) with folder icons at the top
	// level. Slot drives layout only; background cells pass their bgFavList
	// index to drawBgFavCell so the index-keyed cache stays correct.
	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (bgCellW + bgCellGap)
	if cols < 1 {
		cols = 1
	}
	showFolders := !searching && a.bgFavFolder == ""
	slots := int32(0)
	if showFolders {
		slots += int32(len(folderCells))
	}
	for i := range a.bgFavList {
		if visible(i) {
			slots++
		}
	}
	cellStride := bgCellH + bgCellGap + bgCellLabelH
	contentH := (slots + cols - 1) / cols * cellStride
	visibleH := panel.Y + panel.H - gridTop - pad

	a.bgFavScroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.bgFavScroll = c.VScrollbar("bgfavscroll", track, a.bgFavScroll, contentH, visibleH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	slot := int32(0)
	place := func() (sdl.Rect, bool) {
		x := panel.X + pad + (slot%cols)*(bgCellW+bgCellGap)
		yy := gridTop + (slot/cols)*cellStride - a.bgFavScroll
		slot++
		return sdl.Rect{X: x, Y: yy, W: bgCellW, H: bgCellH}, yy > gridTop-bgCellH && yy < panel.Y+panel.H
	}
	if showFolders {
		for _, f := range folderCells {
			if cell, vis := place(); vis {
				a.drawBgFavFolderCell(f, countIn(f), cell)
			}
		}
	}
	for i := range a.bgFavList {
		if !visible(i) {
			continue
		}
		if cell, vis := place(); vis {
			a.drawBgFavCell(i, cell)
		}
	}
	c.popClip(clipPrev, clipHad)

	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false, "") // background preview: no emote-name caption
		if c.clicked {
			a.previewBase = ""
		}
	}
}

// drawBgFavFolderCell draws one Backgrounds-section folder. Clicking opens it;
// dropping a dragged background files it (SetFavBackgroundFolder).
func (a *App) drawBgFavFolderCell(name string, count int, cell sdl.Rect) {
	c := a.ctx
	hover := c.hovering(cell)
	a.drawFolderShape(cell, count, name, hover, a.iniDragging && hover)
	if a.folderDeleteHit(cell, hover) {
		a.wardDelFolder = name
		return // the × claimed the click; don't open the folder
	}
	c.Tooltip(cell, "Open the "+name+" folder — or drop a background here to file it")
	if hover && c.clicked {
		if a.iniDragging && a.iniDragChar != "" {
			a.d.Prefs.SetFavBackgroundFolder(a.serverKey, a.iniDragChar, name)
			a.rebuildBgFav()
			c.clicked = false // consume the drop
		} else {
			a.bgFavFolder = name
			a.bgFavScroll = 0
		}
	}
}

// drawBgFavCell draws one favourite background: landscape thumbnail, name, an
// unstar ★, and (on a plain click) a request to change the room to it. Drag it
// onto a folder to file it — the drag uses the shared modal drag state, so the
// "dragged thing" is this background's name.
func (a *App) drawBgFavCell(idx int, cell sdl.Rect) {
	c := a.ctx
	name := a.bgFavList[idx]
	c.Fill(cell, ColBackground)
	base := a.urls.Background(name, bgThumbPart)
	if page, ok := a.cachedPage(&a.bgFavPages, &a.bgFavPagesGen, len(a.bgFavList), idx, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
	} else {
		a.demandAsset(&a.bgFavAsk, len(a.bgFavList), idx, base, assets.AssetTypeBackground) // AssetType: Background (wardrobe favourite thumb)
		c.LabelClipped(cell.X+4, cell.Y+cell.H/2-8, cell.W-8, name, ColTextDim)
	}
	c.Border(cell, ColPanelHi)
	if a.sess != nil && strings.EqualFold(name, a.sess.Background) {
		tag := sdl.Rect{X: cell.X + 1, Y: cell.Y + 1, W: c.TextWidth("current") + 8, H: 16}
		c.Fill(tag, sdl.Color{R: 0, G: 0, B: 0, A: 190})
		c.Label(tag.X+4, tag.Y+1, "current", ColAccent)
	}
	c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, name, ColTextDim)

	// Drag-arm (shared modal drag state; the dragged item is this background).
	if a.iniPressed && c.hovering(cell) {
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

	// Unstar (top-right): leaves favourites (and this section).
	star := sdl.Rect{X: cell.X + cell.W - 20, Y: cell.Y + 2, W: 18, H: 18}
	c.Fill(star, sdl.Color{R: 0, G: 0, B: 0, A: 150}) // backing: the star must read over busy thumbnails
	c.Label(star.X+3, star.Y+1, "★", ColStar)
	c.Tooltip(star, "Remove from favourites")
	if c.hovering(star) && c.clicked && !a.iniDragging {
		a.d.Prefs.RemoveFavBackground(a.serverKey, name)
		a.rebuildBgFav()
		return
	}

	// Hover → large preview; a plain click (not a drag) changes the room to it.
	if c.HoverPreview("bgfav:"+name, cell) {
		a.previewBase = base
		a.d.Manager.Prefetch(base, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background (favourite preview)
	}
	if c.hovering(cell) && c.clicked && !a.iniDragging {
		a.requestBackground(name)
	}
}
