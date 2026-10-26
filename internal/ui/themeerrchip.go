package ui

// The theme-sidecar refusal chip (v1.90.0 W4 — docs/wip/THEME-EDITOR-DESIGN.md §3.3,
// "cap-hit and error UX").
//
// THE SHIP-BLOCKER THIS CLOSES. theme.Theme.SidecarErr() had existed since W1 with
// ZERO consumers. A sidecar the reader refuses — a named cap exceeded
// (theme.ErrSidecarCap), or a file that is not INI at all (theme.ErrSidecarUnreadable)
// — is swallowed on purpose (format rule 1: reading never refuses, so a theme with an
// unreadable extension tier renders as the stock AO2 theme it validly is). That is
// the right LOAD behaviour and it was a catastrophic REPORTING one: seven of the
// fourteen themes this repository ships lost their entire AsyncAO tier — every
// element, override, palette role and pane — through the whole of W2 because one
// free-text line was one rune over a named cap, and absolutely nothing anywhere said
// so. The theme applied. It just quietly was not the theme.
//
// So a refusal now has a surface: a chip in the menu-bar band carrying the reason.
//
// WHERE IT LIVES, AND WHY. The same band, and for the same three reasons, as #27's
// missing-asset chip (assetmisschip.go) and the Iniswap button (iniswapchip.go):
// always on screen at a fixed place, ABOVE every theme's design canvas by
// construction (so it can never sit on an AO2 control — #21's parity rule), and
// already the home for AsyncAO chrome AO2 has no rect for. This file follows those
// two files' geometry, their two-phase input/paint split and their "drop rather than
// overlap" rule; it does not invent a third idiom for a third chip.
//
// NOT TIME-LIMITED, unlike the missing-asset chip. A missing asset is an EVENT and
// that chip is a notification of it; a refused sidecar is a STATE — it is refused
// now, it will be refused on the next apply, and it stays refused until somebody
// edits the file. A thirty-second badge for a permanent condition is a badge nobody
// sees. It clears when the user acknowledges it (the click, which opens the log
// holding the full line) and re-arms on the next apply that still refuses, so the
// state cannot be silently lost either.

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
	return a.themeSidecarErr != "" && !a.themeSidecarErrAck
}

// themeErrChipRect is the chip's rect in the menu-bar band, right-aligned.
// ok=false means DRAW NOTHING this frame.
//
// It is the OUTERMOST thing in the band, and that placement is what lets a third
// chip join without the other two moving: the missing-asset chip now measures its
// own right edge against menuBarRightContentLeft instead of against the window, and
// the Iniswap chip's reserve is taken off the same value. Two chips both anchored to
// `w` would have drawn on top of each other — the mirror of the problem
// menuBarLeftContentRight already solves on the other side of the band.
//
// This chip wins the outer slot on purpose: it reports a permanent, actionable state
// while the missing-asset chip reports a transient event that clears itself in
// thirty seconds.
//
// It measures its clearance against the menu TITLES ALONE (menuBarTitlesRight) and
// not against menuBarLeftContentRight, which is a correctness requirement rather
// than a simplification: menuBarLeftContentRight asks iniswapChipRect, iniswapChipRect
// asks menuBarRightContentLeft, and menuBarRightContentLeft asks this — so consulting
// it here would be unbounded recursion on the render thread. The band's whole
// geometry resolves strictly outside-in, and this is the outermost step.
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
	c.Tooltip(r, a.themeSidecarTip)
	if !c.clicked {
		return
	}
	c.clicked = false
	a.showDebugPanel = true
	a.debugSection = assetMissDebugLogTab()
	// Acknowledged: the log owns the detail from here, and the next apply that still
	// refuses re-arms the chip (pollThemeApply). Never cleared by a TIMER — the
	// condition it reports does not expire on its own.
	a.themeSidecarErrAck = true
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
