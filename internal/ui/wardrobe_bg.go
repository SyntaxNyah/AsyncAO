package ui

// The wardrobe's Backgrounds section: your favourite backgrounds (the ★ set
// from the Background picker) organized into the same navigable folders as
// characters. Clicking a favourite asks the server to change the room to it.

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
	favs := a.d.Prefs.Load().FavBackgroundList(a.srvMeta.serverKey)
	folders := a.d.Prefs.Load().FavBackgroundFolderMap(a.srvMeta.serverKey)
	a.wardrobe.bgFavList = favs
	a.wardrobe.bgFavLower = make([]string, len(favs))
	a.wardrobe.bgFavFolders = make([]string, len(favs))
	for i, n := range favs {
		lower := strings.ToLower(n)
		a.wardrobe.bgFavLower[i] = lower
		a.wardrobe.bgFavFolders[i] = folders[lower] // "" = unfiled
	}
	a.wardrobe.bgFavAsk = nil
	a.wardrobe.bgFavPages = nil // set/order changed → drop the index-keyed thumbnail cache
}

// bgFavFolderNames lists the distinct background folders in first-seen order.
func (a *App) bgFavFolderNames() []string {
	var out []string
	seen := map[string]struct{}{}
	for _, f := range a.wardrobe.bgFavFolders {
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
	a.wardrobe.bgFavSearch, _ = c.TextField("bgfavsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.wardrobe.bgFavSearch, "Search favourites...")
	c.LabelClipped(panel.X+pad+250, y+4, panel.X+panel.W-(panel.X+pad+250)-pad, fmt.Sprintf("%d favourite backgrounds — click one to change the room to it", len(a.wardrobe.bgFavList)), ColTextDim)
	y += 36

	// Empty state: favourites are added by starring in the Background picker.
	if len(a.wardrobe.bgFavList) == 0 {
		c.LabelClipped(panel.X+pad, y+4, panel.W-2*pad, "No favourite backgrounds yet — open the Background picker (the courtroom Background button), press ★ on the ones you like, and they show up here to organize into folders.", ColTextDim)
		return
	}

	query := strings.ToLower(strings.TrimSpace(a.wardrobe.bgFavSearch))
	searching := query != ""
	folderCells := a.bgFavFolderNames()
	if nf := strings.TrimSpace(a.wardrobe.bgFavNewFold); nf != "" { // a just-typed folder is a drop target before it has members
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
			return strings.Contains(a.wardrobe.bgFavLower[i], query)
		}
		return strings.EqualFold(a.wardrobe.bgFavFolders[i], a.wardrobe.bgFavFolder) // "" = top level = unfiled
	}
	countIn := func(name string) int {
		n := 0
		for _, f := range a.wardrobe.bgFavFolders {
			if strings.EqualFold(f, name) {
				n++
			}
		}
		return n
	}

	if a.wardrobe.wardDelFolder != "" {
		// A folder delete awaits confirmation: the bar replaces the nav row.
		choice := a.drawFolderDeleteConfirm(panel.X+pad, y, panel.W-2*pad, countIn(a.wardrobe.wardDelFolder), "backgrounds")
		if choice == folderDeleteWithItems || choice == folderDeleteKeepItems {
			a.d.Prefs.DeleteFavBackgroundFolder(a.srvMeta.serverKey, a.wardrobe.wardDelFolder, choice == folderDeleteKeepItems)
			if strings.EqualFold(a.wardrobe.bgFavNewFold, a.wardrobe.wardDelFolder) {
				a.wardrobe.bgFavNewFold = "" // also drop the transient "new folder" chip if it named this one
			}
			if strings.EqualFold(a.wardrobe.bgFavFolder, a.wardrobe.wardDelFolder) { // we were inside the deleted folder
				a.wardrobe.bgFavFolder = ""
				a.wardrobe.bgFavScroll = 0
			}
			a.wardrobe.wardDelFolder = ""
			a.rebuildBgFav()
		} else if choice == folderDeleteCancel {
			a.wardrobe.wardDelFolder = ""
		}
	} else {
		switch {
		case searching:
			c.Label(panel.X+pad, y+4, "Search spans every folder — clear it to browse folders", ColTextDim)
		case a.wardrobe.bgFavFolder == "":
			a.wardrobe.bgFavNewFold, _ = c.TextField("bgfavnewfold", sdl.Rect{X: panel.X + pad, Y: y, W: 240, H: fieldH}, a.wardrobe.bgFavNewFold, "New folder name, then drag backgrounds onto it")
			c.LabelClipped(panel.X+pad+250, y+4, panel.X+panel.W-(panel.X+pad+250)-pad, "Drag a background onto a folder to file it · open a folder to see inside · × on a folder deletes it", ColTextDim)
		default:
			back := sdl.Rect{X: panel.X + pad, Y: y, W: 150, H: btnH}
			c.Fill(back, ColPanel)
			if a.inibrowse.iniDragging && c.hovering(back) {
				c.Border(back, ColAccent) // drop here to take the background out of the folder
			} else {
				c.Border(back, ColPanelHi)
			}
			c.Label(back.X+8, back.Y+5, "‹ All folders", ColText)
			c.Tooltip(back, "Back to all folders — or drop a background here to take it out of this folder")
			if c.hovering(back) && c.clicked {
				if a.inibrowse.iniDragging && a.inibrowse.iniDragChar != "" {
					a.d.Prefs.SetFavBackgroundFolder(a.srvMeta.serverKey, a.inibrowse.iniDragChar, "")
					a.rebuildBgFav()
					c.clicked = false // consume the drop
				} else {
					a.wardrobe.bgFavFolder = ""
					a.wardrobe.bgFavScroll = 0
				}
			}
			c.LabelClipped(back.X+back.W+12, y+5, panel.X+panel.W-(back.X+back.W+12)-pad, fmt.Sprintf("%s — %d background(s)", a.wardrobe.bgFavFolder, countIn(a.wardrobe.bgFavFolder)), ColAccent)
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
	showFolders := !searching && a.wardrobe.bgFavFolder == ""
	slots := int32(0)
	if showFolders {
		slots += int32(len(folderCells))
	}
	for i := range a.wardrobe.bgFavList {
		if visible(i) {
			slots++
		}
	}
	cellStride := bgCellH + bgCellGap + bgCellLabelH
	contentH := (slots + cols - 1) / cols * cellStride
	visibleH := panel.Y + panel.H - gridTop - pad

	a.wardrobe.bgFavScroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.wardrobe.bgFavScroll = c.VScrollbar("bgfavscroll", track, a.wardrobe.bgFavScroll, contentH, visibleH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	slot := int32(0)
	place := func() (sdl.Rect, bool) {
		x := panel.X + pad + (slot%cols)*(bgCellW+bgCellGap)
		yy := gridTop + (slot/cols)*cellStride - a.wardrobe.bgFavScroll
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
	for i := range a.wardrobe.bgFavList {
		if !visible(i) {
			continue
		}
		if cell, vis := place(); vis {
			a.drawBgFavCell(i, cell)
		}
	}
	c.popClip(clipPrev, clipHad)

	if a.budget.previewBase != "" {
		a.drawSpritePreview(w, h, false, "") // background preview: no emote-name caption
		a.dismissPreviewOnClick()            // pin-aware, and disarms the trigger's dwell id
	}
}

// drawBgFavFolderCell draws one Backgrounds-section folder. Clicking opens it;
// dropping a dragged background files it (SetFavBackgroundFolder).
func (a *App) drawBgFavFolderCell(name string, count int, cell sdl.Rect) {
	c := a.ctx
	hover := c.hovering(cell)
	a.drawFolderShape(cell, count, name, hover, a.inibrowse.iniDragging && hover)
	if a.folderDeleteHit(cell, hover) {
		a.wardrobe.wardDelFolder = name
		return // the × claimed the click; don't open the folder
	}
	c.Tooltip(cell, "Open the "+name+" folder — or drop a background here to file it")
	if hover && c.clicked {
		if a.inibrowse.iniDragging && a.inibrowse.iniDragChar != "" {
			a.d.Prefs.SetFavBackgroundFolder(a.srvMeta.serverKey, a.inibrowse.iniDragChar, name)
			a.rebuildBgFav()
			c.clicked = false // consume the drop
		} else {
			a.wardrobe.bgFavFolder = name
			a.wardrobe.bgFavScroll = 0
		}
	}
}

// drawBgFavCell draws one favourite background: landscape thumbnail, name, an
// unstar ★, and (on a plain click) a request to change the room to it. Drag it
// onto a folder to file it — the drag uses the shared modal drag state, so the
// "dragged thing" is this background's name.
func (a *App) drawBgFavCell(idx int, cell sdl.Rect) {
	c := a.ctx
	name := a.wardrobe.bgFavList[idx]
	c.Fill(cell, ColBackground)
	base := a.link.urls.Background(name, bgThumbPart)
	if page, ok := a.cachedPage(&a.wardrobe.bgFavPages, &a.wardrobe.bgFavPagesGen, len(a.wardrobe.bgFavList), idx, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
	} else {
		a.demandAsset(&a.wardrobe.bgFavAsk, len(a.wardrobe.bgFavList), idx, base, assets.AssetTypeBackground) // AssetType: Background (wardrobe favourite thumb)
		c.LabelClipped(cell.X+4, cell.Y+cell.H/2-8, cell.W-8, name, ColTextDim)
	}
	c.Border(cell, ColPanelHi)
	if a.link.sess != nil && strings.EqualFold(name, a.link.sess.Background) {
		tag := sdl.Rect{X: cell.X + 1, Y: cell.Y + 1, W: c.TextWidth("current") + 8, H: 16}
		c.Fill(tag, sdl.Color{R: 0, G: 0, B: 0, A: 190})
		c.Label(tag.X+4, tag.Y+1, "current", ColAccent)
	}
	c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, name, ColTextDim)

	// Drag-arm (shared modal drag state; the dragged item is this background).
	if a.inibrowse.iniPressed && c.hovering(cell) {
		a.inibrowse.iniDragChar = name
		a.inibrowse.iniDragStart = [2]int32{c.mouseX, c.mouseY}
		a.inibrowse.iniDragging = false
	}
	if a.inibrowse.iniDragChar == name && c.mouseDown {
		dx, dy := c.mouseX-a.inibrowse.iniDragStart[0], c.mouseY-a.inibrowse.iniDragStart[1]
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx+dy > iniDragThreshold {
			a.inibrowse.iniDragging = true
		}
	}

	// Unstar (top-right): leaves favourites (and this section).
	star := sdl.Rect{X: cell.X + cell.W - 20, Y: cell.Y + 2, W: 18, H: 18}
	c.Fill(star, sdl.Color{R: 0, G: 0, B: 0, A: 150}) // backing: the star must read over busy thumbnails
	c.Label(star.X+3, star.Y+1, "★", ColStar)
	c.Tooltip(star, "Remove from favourites")
	if c.hovering(star) && c.clicked && !a.inibrowse.iniDragging {
		a.d.Prefs.RemoveFavBackground(a.srvMeta.serverKey, name)
		a.rebuildBgFav()
		return
	}

	// Hover → large preview; a plain click (not a drag) changes the room to it.
	if c.HoverPreview("bgfav:"+name, cell) {
		a.budget.previewBase = base
		a.d.Manager.Prefetch(base, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background (favourite preview)
	}
	if c.hovering(cell) && c.clicked && !a.inibrowse.iniDragging {
		a.requestBackground(name)
	}
}
