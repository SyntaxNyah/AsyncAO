package ui

// The dedicated Iniswap button.
// WHERE IT LIVES, AND WHY. The menu-bar band (#14, chrometop.go). That band is the
// WIDTH-DROP CONTRACT. Chrome must never lie about what is clickable, so the chip is
// dropped whole for the frame the moment it cannot sit clear of both the menu titles
// on its left and the band's right-hand reserve. Its function is not lost when it

import "github.com/veandco/go-sdl2/sdl"

const (
	// iniswapChipLabel is the word people search for. Deliberately "Iniswap" and not
	// "Wardrobe": the report is that players cannot find INISWAP, and the modal's own
	// title already says Wardrobe once it is open.
	iniswapChipLabel = "Iniswap"
	// iniswapChipTip is the one-line explanation. One line because it is a chrome
	// tooltip, not documentation.
	iniswapChipTip = "Iniswap — wear another character's sprites while keeping your own slot. Opens the Wardrobe's browse tab."
	// iniswapChipPadX is the horizontal padding inside the chip's plate (the
	// missing-asset chip's assetMissChipPadX, for one band with one rhythm).
	iniswapChipPadX = int32(8)
	// iniswapChipInsetY keeps the plate clear of the band's top edge and of the
	// hairline along its bottom — the sibling chip's assetMissChipInsetY.
	iniswapChipInsetY = int32(2)
	// iniswapChipGapX is the clearance the chip keeps from the last menu title, so it
	// reads as a separate control rather than a sixth menu.
	iniswapChipGapX = int32(14)
	// iniswapChipRightReservePx is the right-hand strip of the band the chip refuses
	// to enter, so the two chips are not permanently squabbling over one row: the
	// missing-asset chip is right-aligned there. Sized for that chip's realistic
	// longest label ("! NN assets missing") plus its plate padding, its margin and
	// its own minimum gap. On a window too narrow to hold the menu titles, this
	// reserve AND the chip, the chip is what drops — the width-drop contract, the
	// same rule the IC bar's Flip segment follows at 720p.
	// It is belt to assetMissChipRect's braces: that chip already yields to
	// menuBarLeftContentRight, so a collision is impossible either way. Preferring to
	// drop the Iniswap chip here keeps the yielding one-directional, which is what
	iniswapChipRightReservePx = int32(200)
)

// iniswapChipActive reports whether the chip should draw and take input this frame.
// Courtroom only: iniswapping needs a character and a session, and the modal itself
// is a courtroom modal. Suppressed while the Wardrobe is already open, so the chip
// never sits on top of the thing it opens.
func (a *App) iniswapChipActive() bool {
	return a.screen == ScreenCourtroom && a.link.sess != nil && !a.inibrowse.showIni &&
		!a.gifExporting && !a.replaying && !a.makerOpen
}

// iniswapChipRect is the chip's rect in the menu-bar band, left-anchored after the
// last menu title. ok=false means DRAW NOTHING this frame — see the width-drop
// contract in the file header.
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
	// The reserve is taken off menuBarRightContentLeft, not off `w`: the band grew a
	// third chip (themeerrchip.go) which is right-anchored OUTSIDE the missing-asset
	// one, so the strip this chip must stay clear of starts where that chip starts.
	// With no theme error on screen menuBarRightContentLeft is exactly `w` and this
	// line is byte-identical to what it was.
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

// menuBarLeftContentRight is the right edge of everything LEFT-anchored in the band:
// the menu titles, plus the Iniswap chip when it drew. The right-aligned
// missing-asset chip measures its clearance against this, so the two chips can never
// be drawn on top of each other — the one thing the band's "drop rather than
// overlap" rule cannot express with a single anchor.
func (a *App) menuBarLeftContentRight(w int32) int32 {
	right := a.menuBarTitlesRight()
	if r, ok := a.iniswapChipRect(w); ok {
		if e := r.X + r.W; e > right {
			right = e
		}
	}
	return right
}

// handleIniswapChipInput claims a click on the chip. Called from menuBarFrame (phase
// Exactly the ordering handleAssetMissChipInput gets one call site earlier.
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

// drawIniswapChip paints the chip. Called from drawMenuBar (phase 2), so it lands on
// top of the band it shares with the menu titles.
func (a *App) drawIniswapChip(w int32) {
	r, ok := a.iniswapChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	col := menuBarColors()
	// A filled, accent-bordered plate: the same "this is a control, not a label"
	// treatment the classic layout's own Wardrobe button wears.
	c.Fill(r, col.hi)
	c.Border(r, ColAccent)
	c.Label(r.X+iniswapChipPadX, r.Y+a.menuBarTextY(r.H), iniswapChipLabel, ColAccent)
}

// openIniswapBrowse opens the Wardrobe on its BROWSE tab — the one that lists what is
// actually on the base — rather than on the saved-favourites tab a first-time user
// finds empty. Everything else (the list fetch, the local-mount scan) is openIniswap's
// and the tab's own.
func (a *App) openIniswapBrowse() {
	a.wardrobe.wardSection = wardSectionIniswaps
	a.openIniswap()
}
