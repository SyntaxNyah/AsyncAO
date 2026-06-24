package ui

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// #130 — the CM (area control) tools live in their OWN panel, separate from the mod (ban/kick)
// dashboard, and surface only while you actually hold CM. Two pieces:
//
//   - drawModCornerButtons: the dedicated, always-on launcher. A "Mod" chip appears only when
//     you're mod-authed, a "CM" chip only when you hold CM, tucked in the bottom-right corner —
//     so a regular player sees no new chrome at all. 0-alloc per frame: amIMod() is a plain bool
//     read and amICMNow is the event-cached CM flag (refreshed on ARUP/PU, never per frame).
//   - drawCMPanel: claim is via /cm (the chip's appearance IS "it showed up"); this panel is the
//     room controls once you're CM — lock/unlock, release CM, and a per-player area-kick. It
//     self-closes the instant you stop holding CM.

const (
	modCornerMargin = int32(12) // gap from the screen edges
	modCornerChipH  = int32(26)
	modCornerGap    = int32(6)
)

// toggleCMPanel opens / closes the CM panel (corner chip + hotkey).
func (a *App) toggleCMPanel() { a.showCMPanel = !a.showCMPanel }

// drawModCornerButtons paints the context-aware Mod / CM launcher chips, bottom-right, on top of
// the live scene. Skipped while a blocking popup is up (so it can't eat that popup's clicks).
func (a *App) drawModCornerButtons(w, h int32) {
	if a.sess == nil || a.courtModalOpen() {
		return
	}
	isMod, isCM := a.amIMod(), a.amICMNow
	if !isMod && !isCM {
		return // a regular player gets no new chrome
	}
	rightX := w - modCornerMargin
	y := h - modCornerMargin - modCornerChipH
	if isCM {
		rightX = a.drawCornerChip(rightX, y, "CM", a.showCMPanel, a.toggleCMPanel)
	}
	if isMod {
		a.drawCornerChip(rightX, y, "Mod", a.showModDash, a.toggleModDash)
	}
}

// drawCornerChip draws one right-aligned launcher pill ending at rightX, highlighted while its
// panel is open. A click runs onClick and is CONSUMED so it can't also land on the chrome behind.
// Returns the next chip's right edge.
func (a *App) drawCornerChip(rightX, y int32, label string, active bool, onClick func()) int32 {
	c := a.ctx
	cw := c.TextWidth(label) + 24
	r := sdl.Rect{X: rightX - cw, Y: y, W: cw, H: modCornerChipH}
	bg, fg := ColPanel, ColText
	switch {
	case active:
		bg, fg = ColAccent, ColBackground
	case c.hovering(r):
		bg = ColPanelHi
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	c.Label(r.X+(cw-c.TextWidth(label))/2, y+5, label, fg)
	if c.clicked && c.hovering(r) {
		c.clicked = false // consume so the click can't leak to the scene / chrome behind
		onClick()
	}
	return rightX - cw - modCornerGap
}

// drawCMPanel is the standalone CM (area control) panel: lock/unlock the area, release CM, and a
// per-player kick-from-area. Gold-bordered (CM colour). Self-closes the moment amICMNow drops.
func (a *App) drawCMPanel(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 210}) // backdrop dim
	if c.escPressed || !a.amICMNow {
		a.showCMPanel = false // Esc, or we no longer hold CM
		return
	}
	sw := a.detectedSoftware()
	pw, ph := int32(460), int32(404)
	if pw > w-16 {
		pw = w - 16
	}
	if ph > h-16 {
		ph = h - 16
	}
	panel := sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	c.Fill(panel, ColPanel)
	c.Border(panel, chipCMColor) // gold = CM
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
