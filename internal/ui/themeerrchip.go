package ui

// The theme-sidecar refusal chip (v1.90.0 W4 — docs/wip/THEME-EDITOR-DESIGN.md §3.3,
// "cap-hit and error UX").
// WHERE IT LIVES, AND WHY. The same band, and for the same three reasons, as #27's
// construction (so it can never sit on an AO2 control — #21's parity rule), and

import (
	"fmt"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeErrChipLabel is what the chip says. It names the CONSEQUENCE rather than
	// the fault, because the consequence is the thing the user is looking at and
	// cannot explain: their theme applied and looks wrong.
	themeErrChipLabel = "! theme extras skipped"
	// themeErrChipPadX / themeErrChipInsetY are assetmisschip.go's numbers, restated
	// under this file's own names so the three chips read as one band rhythm rather
	// than as one chip reaching into another's constants.
	themeErrChipPadX   = int32(8)
	themeErrChipInsetY = int32(2)
	// themeErrChipMarginX insets the chip from the window's right edge — the same
	// breathing room the menu titles get on the left (menuBarPadX).
	themeErrChipMarginX = int32(4)
	// themeErrChipMinGapX is the clearance the chip demands from the menu titles.
	// Below it the chip is dropped whole for the frame rather than drawn over a
	// title: chrome must never lie about what is clickable, and the F8 debug log
	// carries the same line unconditionally either way.
	themeErrChipMinGapX = int32(12)
)

// themeErrChipActive reports whether the chip should draw and take input.
//
// Deliberately NOT gated on a screen. A theme is picked in Settings and judged in
// the courtroom, and the whole failure this reports is "I applied a theme and it is
// not the theme" — gating it to the courtroom would hide it on the very screen the
// user is standing on when they change themes.
func (a *App) themeErrChipActive() bool {
	return a.fxPool.themeSidecarErr != "" && !a.fxPool.themeSidecarErrAck
}

// themeErrChipRect is the chip's rect in the menu-bar band, right-aligned.
// ok=false means DRAW NOTHING this frame.
// It measures its clearance against the menu TITLES ALONE (menuBarTitlesRight) and
// not against menuBarLeftContentRight, which is a correctness requirement rather
// than a simplification: menuBarLeftContentRight asks iniswapChipRect, iniswapChipRect
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

// menuBarRightContentLeft is the left edge of everything RIGHT-anchored in the band
// OUTSIDE the caller — the mirror of menuBarLeftContentRight (iniswapchip.go).
//
// It returns the window width when nothing is right-anchored, which is exactly the
// value the missing-asset chip and the Iniswap chip used to hardcode: with no theme
// error on screen every rect in this band is byte-identical to what it was before
// this chip existed.
func (a *App) menuBarRightContentLeft(w int32) int32 {
	if r, ok := a.themeErrChipRect(w); ok {
		return r.X
	}
	return w
}

// menuBarTitlesRight is the right edge of the menu TITLES alone — no chips. Shared
// by menuBarLeftContentRight (which then folds the Iniswap chip in) and by
// themeErrChipRect (which must not, see above), so the two cannot drift.
func (a *App) menuBarTitlesRight() int32 {
	if len(menuBarMenus) == 0 {
		return 0
	}
	last := a.menuBarTitleRect(len(menuBarMenus) - 1)
	return last.X + last.W
}

// handleThemeErrChipInput claims a click on the chip. Called pre-screen, beside
// handleAssetMissChipInput and for the same reason: the chip paints over the band
// the screens offset below, so its click must be claimed before they see it.
func (a *App) handleThemeErrChipInput(w int32) {
	r, ok := a.themeErrChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	if !c.hovering(r) {
		return
	}
	c.Tooltip(r, a.fxPool.themeSidecarTip)
	if !c.clicked {
		return
	}
	c.clicked = false
	a.debugPanel.open = true
	a.debugPanel.section = assetMissDebugLogTab()
	// Acknowledged: the log owns the detail from here, and the next apply that still
	// refuses re-arms the chip (pollThemeApply). Never cleared by a TIMER — the
	// condition it reports does not expire on its own.
	a.fxPool.themeSidecarErrAck = true
}

// drawThemeErrChip paints the chip. Called from drawMenuBar (phase 2), so it lands
// on top of the band it shares with the menu titles and the other two chips.
func (a *App) drawThemeErrChip(w int32) {
	r, ok := a.themeErrChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	// A filled plate with a danger border — the missing-asset chip's treatment,
	// because this is the same class of message: something is not there, and the
	// user cannot see that it is not there.
	c.Fill(r, ColPanelHi)
	c.Border(r, ColDanger)
	c.Label(r.X+themeErrChipPadX, r.Y+a.menuBarTextY(r.H), themeErrChipLabel, ColDanger)
}

// themeSidecarRefusalLine renders one refused sidecar as the single line that goes
// to the debug log and the Settings status line.
//
// Built on the theme-apply goroutine, never on a draw: it formats. `name` is the
// theme as that apply resolved it, `dir` is the directory the refused file was found
// in ("" when the loader could not say), and `err` is theme.Theme.SidecarErr's own
// error — already wrapped with the failing section and the numbers, so nothing here
// re-derives why.
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

// themeSidecarRefusalTip is the chip's tooltip: the same fact in the shape a hover
// can absorb, plus where to go next. Built once per apply on the apply goroutine,
// for the reason noteAssetMiss builds its own tooltip there — a warning arrives at
// most once per apply, the chip draws every frame.
func themeSidecarRefusalTip(name string, err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%s in theme %q could not be read:\n%v\n\n"+
		"The theme's AO2 files applied normally. Everything in the AsyncAO file — elements, "+
		"[overrides], [palette], [pane.*] — was skipped.\nClick for the full line (Debug ▸ Log).",
		theme.SidecarFileName, name, err)
}
