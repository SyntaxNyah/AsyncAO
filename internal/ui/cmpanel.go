package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// #130 — the CM (area control) tools live in their OWN panel, separate from the mod (ban/kick)
// dashboard, and surface only while you actually hold CM. Both panels are launched from compact
// "Mod" / "CM" buttons in the courtroom's button row (classic drawICControls / themed
// drawThemedExtrasButton), shown only when you hold the role — in the row, so they never float
// over the emote sprites. Claiming CM is via /cm; this panel is the room controls once you're CM.

// toggleCMPanel opens / closes the CM panel (its row button + the mod dashboard's pointer).
func (a *App) toggleCMPanel() { a.showCMPanel = !a.showCMPanel }

// cmPanelRect is the CM panel's floating-window rect (floatwin.go).
func (a *App) cmPanelRect(w, h int32) sdl.Rect {
	return a.cmWin.rect(cmPanelDefW, cmPanelDefH, cmPanelMinW, cmPanelMinH, w, h)
}

const (
	cmPanelDefW = 460 // default size
	cmPanelDefH = 404
	cmPanelMinW = 380 // floating-box floors
	cmPanelMinH = 320
)

// drawCMPanel is the standalone CM (area control) panel: lock/unlock the area, release CM, and a
// per-player kick-from-area. Gold-bordered (CM colour). Self-closes the moment amICMNow drops.
// Now a movable/resizable, non-blocking floating box (chat stays live behind it).
func (a *App) drawCMPanel(w, h int32, pressed *bool) {
	c := a.ctx
	if c.escPressed || !a.amICMNow {
		a.showCMPanel = false // Esc, or we no longer hold CM
		return
	}
	sw := a.detectedSoftware()
	panel := a.cmPanelRect(w, h) // floating box: movable / resizable, non-blocking
	pw, ph := panel.W, panel.H
	c.Fill(panel, ColPanel)
	c.Border(panel, chipCMColor)                                                     // gold = CM
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: floatTitleH}, ColPanelHi) // title bar / drag handle
	a.floatWinDrag(&a.cmWin, sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W - 84 - modDashIn, H: floatTitleH}, pressed)
	cgrip := sdl.Rect{X: panel.X + panel.W - floatGripSz, Y: panel.Y + panel.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.cmWin, cgrip, panel, cmPanelMinW, cmPanelMinH, pressed)
	a.drawResizeGrip(cgrip)
	x := panel.X + modDashIn

	c.Heading(x, panel.Y+12, "CM — Area Control", ColText)
	if c.Button(sdl.Rect{X: panel.X + pw - modDashIn - 74, Y: panel.Y + 12, W: 74, H: btnH}, "Close") {
		a.showCMPanel = false
		return
	}
	c.LabelClipped(x, panel.Y+46, pw-2*modDashIn, "Area: "+a.myAreaName(), ColTextDim)

	// Global area controls (no target needed). Flow + wrap.
	y := panel.Y + 72
	bx := x
	mk := func(label, cmd string) {
		if cmd == "" {
			return
		}
		bw := c.TextWidth(label) + 18
		if bx+bw > panel.X+pw-modDashIn {
			bx = x
			y += btnH + 6
		}
		if c.Button(sdl.Rect{X: bx, Y: y, W: bw, H: btnH}, label) {
			a.sendModCommand(cmd)
		}
		bx += bw + 6
	}
	mk("Lock area", courtroom.LockArea(sw))
	mk("Unlock area", courtroom.UnlockArea(sw))
	mk("Release CM", courtroom.CMRelease(sw))
	y += btnH + 14

	// Room roster with a per-row area-kick (the "room controls like kick" the user asked for).
	c.Label(x, y, "Players in this area:", ColTextDim)
	y += 22
	footerH := int32(36)
	a.drawCMRoster(sdl.Rect{X: x, Y: y, W: pw - 2*modDashIn, H: panel.Y + ph - footerH - 12 - y}, sw)

	fy := panel.Y + ph - footerH
	c.LabelClipped(x, fy, pw-2*modDashIn, "Claim / leave CM with /cm · /uncm. Lock seals the area to newcomers.", ColTextDim)
}

// drawCMRoster lists the area's players with a per-row "Kick" (area-kick) for the detected
// software. Opt-in panel, so allocation here is off the hot path.
func (a *App) drawCMRoster(r sdl.Rect, sw courtroom.ServerSoftware) {
	c := a.ctx
	c.Border(r, ColPanelHi)
	roster := a.rosterView()
	if len(roster) == 0 {
		c.LabelClipped(r.X+6, r.Y+6, r.W-12, "No players in the live list yet.", ColTextDim)
		return
	}
	if !c.ctrlHeld {
		a.cmRosterScroll -= c.WheelIn(r) * scrollStepPx
	}
	contentH := int32(len(roster)) * modRosterRowH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.cmRosterScroll = c.VScrollbar("cmroster", track, a.cmRosterScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowY := r.Y - a.cmRosterScroll
	rowW := r.W - scrollBarW - 4
	for i := range roster {
		p := roster[i]
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-modRosterRowH {
			rrow := sdl.Rect{X: r.X, Y: rowY, W: rowW, H: modRosterRowH - 2}
			if c.hovering(rrow) {
				c.Fill(rrow, ColPanelHi)
			}
			textW := rowW - 12
			// Per-row area-kick when the software supports it and the row has a UID.
			if cmd := courtroom.AreaKick(sw, p.uid); cmd != "" {
				kw := c.TextWidth("Kick") + 14
				kr := sdl.Rect{X: rrow.X + rowW - kw - 2, Y: rowY + (modRosterRowH-btnH)/2, W: kw, H: btnH}
				if c.Button(kr, "Kick") {
					a.sendModCommand(cmd)
				}
				textW = rowW - kw - 16
			}
			a.drawModRosterIdentity(p, rrow.X+6, rowY, textW, ColText)
		}
		rowY += modRosterRowH
	}
}
