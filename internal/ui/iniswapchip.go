package ui

// The Iniswap chip sits in the menu-bar band: always on screen, and above every theme's design canvas,
// so it can never cover an AO2 control. It drops whole for the frame when it cannot sit clear of the menu titles
// and the right-hand reserve; Extras > "Wardrobe / iniswap…" reaches the same modal at every width.

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

const (
	// iniswapChipLabel is the word people search for; the modal's title already says Wardrobe.
	iniswapChipLabel = "Iniswap"
	// iniswapChipTip is the chrome tooltip.
	iniswapChipTip = "Iniswap — wear another character's sprites while keeping your own slot. Opens the Wardrobe's browse tab."
	// iniswapChipPadX is the plate's padding, matching assetMissChipPadX.
	iniswapChipPadX = int32(8)
	// iniswapChipInsetY keeps the plate off the band's edges, matching assetMissChipInsetY.
	iniswapChipInsetY = int32(2)
	// iniswapChipGapX is the clearance from the last menu title, so it does not read as another menu.
	iniswapChipGapX = int32(14)
	// iniswapChipRightReservePx is the strip kept for the right-aligned missing-asset chip ("! NN assets missing").
	// iniswapChipRightReservePx is the strip kept for the right-aligned missing-asset chip.
	// That chip yields to menuBarLeftContentRight; this one yielding keeps it one-way, so they do not flicker.
	iniswapChipRightReservePx = int32(200)
)

// iniswapChipActive is courtroom-only, since iniswapping needs a session, and off while the Wardrobe is open.
func (a *App) iniswapChipActive() bool {
	return a.screen == ScreenCourtroom && a.sess != nil && !a.showIni &&
		!a.gifExporting && !a.replaying && !a.makerOpen
}

// iniswapChipRect is the chip's rect after the last menu title; ok=false means draw nothing this frame.
func (a *App) iniswapChipRect(w int32) (sdl.Rect, bool) {
	if !a.iniswapChipActive() || !a.menuBarPaints() {
		return sdl.Rect{}, false
	}
	x := menuBarPadX
	if len(menuBarMenus) > 0 {
		last := a.menuBarTitleRect(len(menuBarMenus) - 1)
		x = last.X + last.W + iniswapChipGapX
	}
	cw := a.ctx.TextWidth(iniswapChipLabel) + 2*iniswapChipPadX
	// Off menuBarRightContentLeft, not w: the theme error chip is right-anchored outside the missing-asset one.
	if x+cw > a.menuBarRightContentLeft(w)-iniswapChipRightReservePx {
		return sdl.Rect{}, false
	}
	return sdl.Rect{
		X: x,
		Y: iniswapChipInsetY,
		W: cw,
		H: menuBarH - menuBarHairlineH - 2*iniswapChipInsetY,
	}, true
}

// menuBarLeftContentRight is the right edge of everything left-anchored in the band, so the two chips cannot overlap.
func (a *App) menuBarLeftContentRight(w int32) int32 {
	right := a.menuBarTitlesRight()
	if r, ok := a.iniswapChipRect(w); ok {
		if e := r.X + r.W; e > right {
			right = e
		}
	}
	return right
}

// handleIniswapChipInput claims a click, before the strip publishes its overlay block, which covers the chip.
// closeMenuBar is a precaution: an open pane holds c.modalOn, so hovering() is false and this cannot be reached.
func (a *App) handleIniswapChipInput(w int32) {
	r, ok := a.iniswapChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	if !c.hovering(r) {
		return
	}
	c.Tooltip(r, iniswapChipTip)
	if !c.clicked {
		return
	}
	c.clicked = false
	a.closeMenuBar() // no-op unless a pane is somehow open — see above
	a.openIniswapBrowse()
}

// drawIniswapChip paints the chip from drawMenuBar, on top of the band it shares with the menu titles.
func (a *App) drawIniswapChip(w int32) {
	r, ok := a.iniswapChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	col := menuBarColors()
	// The same "this is a control" treatment as the classic layout's Wardrobe button.
	c.Fill(r, col.hi)
	c.Border(r, ColAccent)
	c.Label(r.X+iniswapChipPadX, r.Y+a.menuBarTextY(r.H), iniswapChipLabel, ColAccent)
}

// openIniswapBrowse opens the Wardrobe on its Browse tab, since the favourites tab is empty for a first-time user.
func (a *App) openIniswapBrowse() {
	a.wardSection = wardSectionIniswaps
	a.openIniswap()
}
