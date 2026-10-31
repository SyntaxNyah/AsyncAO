package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The missing-asset chip is a right-aligned count inside the menu-bar band: no reflow, and outside every design
// canvas. The full detail is in the debug log, and clicking the chip goes there.

const (
	// assetMissDedupCap bounds the recently-warned ring, a burst suppressor covering one room's churn.
	assetMissDedupCap = 16
	// assetMissRepeatCooldown is how long a base stays reported: repeats reach the debug log but don't re-arm the chip.
	assetMissRepeatCooldown = 60 * time.Second
	// assetMissChipShowDuration is how long the chip lingers after the latest new miss.
	assetMissChipShowDuration = 30 * time.Second
	// assetMissChipPadX is the horizontal padding inside the chip's plate.
	assetMissChipPadX = int32(8)
	// assetMissChipMarginX insets the chip from the right edge, matching menuBarPadX on the left.
	assetMissChipMarginX = int32(4)
	// assetMissChipInsetY clears the strip's top edge and the hairline along its bottom.
	assetMissChipInsetY = int32(2)
	// assetMissChipMinGapX is the clearance from left-anchored content; below it the chip is dropped for the frame.
	assetMissChipMinGapX = int32(12)
)

// assetMissRing is the fixed-size set of recently reported bases (hard rule 4).
type assetMissRing struct {
	base [assetMissDedupCap]string
	at   [assetMissDedupCap]time.Time
	next int
}

// seen reports whether base was reported within cooldown, recording it when it was not.
func (r *assetMissRing) seen(base string, now time.Time, cooldown time.Duration) bool {
	for i := range r.base {
		if r.base[i] == base {
			if now.Sub(r.at[i]) < cooldown {
				return true
			}
			r.at[i] = now // stale entry: refresh in place, keep the slot
			return false
		}
	}
	r.base[r.next] = base
	r.at[r.next] = now
	r.next = (r.next + 1) % assetMissDedupCap
	return false
}

// noteAssetMiss counts a miss, dedups it and arms the chip; it never touches warnLine. A desk-only miss isn't counted:
// a background with no desk image is expected AO2 behaviour (set_scene hides the layer, courtroom.cpp:4628-4634).
func (a *App) noteAssetMiss(base string, deskOnly bool, now time.Time) {
	if deskOnly {
		return
	}
	if a.assetMissSeen.seen(base, now, assetMissRepeatCooldown) {
		return // still inside the cooldown: the debug log already has it
	}
	a.assetMissCount++
	a.assetMissAt = now
	// Built here, not in the draw: a warning arrives at most once per base per cooldown; the chip draws every frame.
	a.assetMissTip = "Missing: " + assetMissTail(base) +
		"\nClick for the full list, with the extensions tried (Debug ▸ Log)."
}

// assetMissChipActive reports whether the chip draws and takes input; ShowAssetWarnings (default off) silences it.
func (a *App) assetMissChipActive() bool {
	return a.assetMissCount > 0 && a.d.Prefs.Load().ShowAssetWarnings &&
		a.now().Sub(a.assetMissAt) < assetMissChipShowDuration
}

// assetMissChipLabel rebuilds only when the count changed, so a settled frame stays allocation-free.
func (a *App) assetMissChipLabel() string {
	if a.assetMissLabelN != a.assetMissCount {
		n := strconv.Itoa(a.assetMissCount)
		if a.assetMissCount == 1 {
			a.assetMissLabel = "! 1 asset missing"
		} else {
			a.assetMissLabel = "! " + n + " assets missing"
		}
		a.assetMissLabelN = a.assetMissCount
	}
	return a.assetMissLabel
}

// assetMissChipRect is the chip's right-aligned rect in the band; ok is false when the band isn't painting or the
// chip would sit on a title. Input and draw call it at different points, so answers can differ for one frame.
func (a *App) assetMissChipRect(w int32) (sdl.Rect, bool) {
	// Active first: siblings in the band's geometry chain expect no rect for a chip not on screen, or they keep clear
	// of it.
	if !a.assetMissChipActive() || !a.menuBarPaints() {
		return sdl.Rect{}, false
	}
	cw := a.ctx.TextWidth(a.assetMissChipLabel()) + 2*assetMissChipPadX
	r := sdl.Rect{
		// Anchored to menuBarRightContentLeft, not w, because the theme refusal chip is right-anchored outside this
		// one.
		X: a.menuBarRightContentLeft(w) - cw - assetMissChipMarginX,
		Y: assetMissChipInsetY,
		W: cw,
		H: menuBarH - menuBarHairlineH - 2*assetMissChipInsetY,
	}
	// Measured against everything left-anchored: a transient badge gives way to the permanent Iniswap chip.
	if r.X < a.menuBarLeftContentRight(w)+assetMissChipMinGapX {
		return sdl.Rect{}, false
	}
	return r, true
}

// handleAssetMissChipInput claims a click on the chip before the screens, which offset below the band it paints over.
func (a *App) handleAssetMissChipInput(w int32) {
	if !a.assetMissChipActive() {
		return
	}
	r, ok := a.assetMissChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	if !c.hovering(r) {
		return
	}
	c.Tooltip(r, a.assetMissTip)
	if !c.clicked {
		return
	}
	c.clicked = false
	a.showDebugPanel = true
	a.debugSection = assetMissDebugLogTab()
	a.assetMissCount = 0 // acknowledged: the log owns the detail from here
}

// assetMissDebugLogTab finds the Log tab by name, so reordering debugSections can't send the chip to the wrong tab.
func assetMissDebugLogTab() int {
	for i, name := range debugSections {
		if name == "Log" {
			return i
		}
	}
	return 0
}

// drawAssetMissChip paints the chip; called from drawMenuBar phase 2, so it lands on top of the band.
func (a *App) drawAssetMissChip(w int32) {
	if !a.assetMissChipActive() {
		return
	}
	r, ok := a.assetMissChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	c.Fill(r, ColPanelHi)
	c.Border(r, ColDanger)
	c.Label(r.X+assetMissChipPadX, r.Y+a.menuBarTextY(r.H), a.assetMissChipLabel(), ColDanger)
}

// assetMissTail trims a base to its last two path segments; the full URL is in the debug log.
func assetMissTail(base string) string {
	trimmed := strings.TrimSuffix(base, "/")
	cut := strings.LastIndexByte(trimmed, '/')
	if cut <= 0 {
		return trimmed
	}
	if head := strings.LastIndexByte(trimmed[:cut], '/'); head >= 0 {
		return trimmed[head+1:]
	}
	return trimmed[cut+1:]
}
