package ui

// Theme-sidecar refusal chip in the menu-bar band. A refused sidecar (a cap exceeded, or not INI) is swallowed on
// load, so this chip is the only sign the theme applied without its AsyncAO tier.
// Geometry and input/paint split follow assetmisschip.go and iniswapchip.go; the chip drops rather than overlaps.
// Not time-limited: a refusal is a state, so the chip clears on click (opens the log) and re-arms on the next refusal.

import (
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeErrChipLabel names the consequence (the theme looks wrong), not the fault.
	themeErrChipLabel = "! theme extras skipped"
	// themeErrChipPadX / themeErrChipInsetY repeat assetmisschip.go's values under this file's names.
	themeErrChipPadX   = int32(8)
	themeErrChipInsetY = int32(2)
	// themeErrChipMarginX insets the chip from the window's right edge (mirrors menuBarPadX).
	themeErrChipMarginX = int32(4)
	// themeErrChipMinGapX is the clearance from the menu titles; below it the chip is dropped for the frame.
	themeErrChipMinGapX = int32(12)
)

// themeErrChipActive reports whether the chip should draw and take input.
// Not gated on a screen: a theme is picked in Settings but judged in the courtroom.
func (a *App) themeErrChipActive() bool {
	return a.themeSidecarErr != "" && !a.themeSidecarErrAck
}

// themeErrChipRect is the chip's rect in the menu-bar band, right-aligned; ok=false means draw nothing this frame.
// It is the outermost item, so the other chips measure against menuBarRightContentLeft, not the window.
// Clearance is measured against menuBarTitlesRight, not menuBarLeftContentRight: that chain asks this function,
// so using it here would recurse forever.
func (a *App) themeErrChipRect(w int32) (sdl.Rect, bool) {
	if !a.themeErrChipActive() || !a.menuBarPaints() {
		return sdl.Rect{}, false
	}
	cw := a.ctx.TextWidth(themeErrChipLabel) + 2*themeErrChipPadX
	r := sdl.Rect{
		X: w - cw - themeErrChipMarginX,
		Y: themeErrChipInsetY,
		W: cw,
		H: menuBarH - menuBarHairlineH - 2*themeErrChipInsetY,
	}
	if r.X < a.menuBarTitlesRight()+themeErrChipMinGapX {
		return sdl.Rect{}, false
	}
	return r, true
}

// menuBarRightContentLeft is the left edge of everything right-anchored in the band outside the caller;
// it is the window width when nothing is.
func (a *App) menuBarRightContentLeft(w int32) int32 {
	if r, ok := a.themeErrChipRect(w); ok {
		return r.X
	}
	return w
}

// menuBarTitlesRight is the right edge of the menu titles alone, shared by menuBarLeftContentRight and
// themeErrChipRect.
func (a *App) menuBarTitlesRight() int32 {
	if len(menuBarMenus) == 0 {
		return 0
	}
	last := a.menuBarTitleRect(len(menuBarMenus) - 1)
	return last.X + last.W
}

// handleThemeErrChipInput claims a click pre-screen, as handleAssetMissChipInput does, before the screens see it.
func (a *App) handleThemeErrChipInput(w int32) {
	r, ok := a.themeErrChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	if !c.hovering(r) {
		return
	}
	c.Tooltip(r, a.themeSidecarTip)
	if !c.clicked {
		return
	}
	c.clicked = false
	a.showDebugPanel = true
	a.debugSection = assetMissDebugLogTab()
	// Acknowledged: the log holds the detail, and the next apply that still refuses re-arms the chip (pollThemeApply).
	a.themeSidecarErrAck = true
}

// drawThemeErrChip paints the chip from drawMenuBar, over the band.
func (a *App) drawThemeErrChip(w int32) {
	r, ok := a.themeErrChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	// Filled plate with a danger border, like the missing-asset chip.
	c.Fill(r, ColPanelHi)
	c.Border(r, ColDanger)
	c.Label(r.X+themeErrChipPadX, r.Y+a.menuBarTextY(r.H), themeErrChipLabel, ColDanger)
}

// themeSidecarRefusalLine renders one refused sidecar as the line for the debug log and the Settings status.
// It runs on the theme-apply goroutine; dir is "" when the loader could not say.
func themeSidecarRefusalLine(name, dir string, err error) string {
	if err == nil {
		return ""
	}
	line := fmt.Sprintf("theme %q: %s was NOT loaded — %v. The AO2 half of the theme applied; every "+
		"AsyncAO element, override, palette colour and pane in that file was skipped.",
		name, theme.SidecarFileName, err)
	if dir != "" {
		line += " (" + dir + ")"
	}
	return line
}

// themeSidecarRefusalTip is the chip's tooltip, built once per apply because the chip draws every frame.
func themeSidecarRefusalTip(name string, err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%s in theme %q could not be read:\n%v\n\n"+
		"The theme's AO2 files applied normally. Everything in the AsyncAO file — elements, "+
		"[overrides], [palette], [pane.*] — was skipped.\nClick for the full line (Debug ▸ Log).",
		theme.SidecarFileName, name, err)
}
