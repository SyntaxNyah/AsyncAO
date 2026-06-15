package ui

// The Players tab: AsyncAO's player list (Akashi/Nyathena-style), built from the
// /getarea snapshot the click-to-pair parser already harvests. AO has no
// per-player packet (only ARUP area COUNTS), so it's refresh-driven and stamped
// "as of HH:MM", not live. IPIDs are mod-only data shown in-session — never
// persisted. The foundation for future mod/user tools.

import (
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// drawPlayerList renders the parsed area roster — one row per UID — with the
// /ga · /gas · /getarea fetch buttons, a snapshot time, and per-row Pair /
// Copy-UID (and Copy-IPID when present) actions.
func (a *App) drawPlayerList(r sdl.Rect) {
	c := a.ctx
	c.Label(r.X, r.Y+5, "Fetch:", ColTextDim)
	bx := r.X + 48
	for _, cmd := range []string{"/ga", "/gas", "/getarea"} {
		bw := c.TextWidth(cmd) + 14
		if c.Button(sdl.Rect{X: bx, Y: r.Y, W: bw, H: 22}, cmd) {
			a.queueOOCLines([]string{cmd})
			a.warnLine = clampLine("Sent " + cmd + " — the list fills from the reply.")
			a.warnAt = a.now()
		}
		bx += bw + 5
	}
	status := strconv.Itoa(len(a.areaPlayers)) + " players"
	if !a.areaListAt.IsZero() {
		status += "  ·  as of " + a.areaListAt.Format("15:04") // a snapshot, not live
	}
	c.LabelClipped(bx+10, r.Y+5, r.X+r.W-bx-12, status, ColTextDim)
	r.Y += 28
	r.H -= 28

	if len(a.areaPlayers) == 0 {
		c.LabelClipped(r.X, r.Y+4, r.W, "Run /ga (or /gas, /getarea) to list who's in this area.", ColTextDim)
		return
	}
	const lineH = int32(40) // two text rows: IC identity + OOC
	if !c.ctrlHeld {
		a.playerScroll -= c.WheelIn(r) * scrollStepPx
	}
	contentH := int32(len(a.areaPlayers)) * lineH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.playerScroll = c.VScrollbar("playerlist", track, a.playerScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowW := r.W - scrollBarW - 6
	y := r.Y - a.playerScroll
	for i := range a.areaPlayers {
		p := &a.areaPlayers[i]
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-lineH {
			a.drawPlayerRow(p, sdl.Rect{X: r.X, Y: y, W: rowW, H: lineH - 4})
		}
		y += lineH
	}
}

// drawPlayerRow is one player: "[uid] showname · character" + right-aligned
// Pair / Copy-UID / Copy-IPID, with the OOC/IPID detail on a hover tooltip.
func (a *App) drawPlayerRow(p *areaPlayer, row sdl.Rect) {
	c := a.ctx
	c.Fill(row, ColPanel)
	hover := c.hovering(row)
	if hover {
		c.Border(row, ColPanelHi)
	}
	display := p.name
	if p.showname != "" {
		display = p.showname
	}
	// IPID is mod-only data: show it ONLY while logged in as a mod (non-mods never
	// get it from /getarea anyway; this also hides it the moment a mod logs out).
	isMod := a.sess != nil && a.sess.ModGranted
	btnY := row.Y + (row.H-22)/2
	bx := row.X + row.W - 52
	if c.Button(sdl.Rect{X: bx, Y: btnY, W: 48, H: 22}, "Pair") {
		a.queueOOCLines([]string{"/pair " + p.uid}) // we have the UID — no popup needed
		a.warnLine = clampLine("Sent /pair " + p.uid + " — " + display)
		a.warnAt = a.now()
	}
	bx -= 84
	if c.Button(sdl.Rect{X: bx, Y: btnY, W: 80, H: 22}, "Copy UID") {
		_ = sdl.SetClipboardText(p.uid)
		a.warnLine = clampLine("Copied UID " + p.uid)
		a.warnAt = a.now()
	}
	if p.ipid != "" && isMod {
		bx -= 88
		if c.Button(sdl.Rect{X: bx, Y: btnY, W: 84, H: 22}, "Copy IPID") {
			_ = sdl.SetClipboardText(p.ipid)
			a.warnLine = clampLine("Copied IPID for " + display)
			a.warnAt = a.now()
		}
	}
	textW := bx - row.X - 12
	// Line 1 — IC identity: [uid] showname · character.
	ic := "[" + p.uid + "]  " + p.name
	if p.showname != "" && !strings.EqualFold(p.showname, p.name) {
		ic = "[" + p.uid + "]  " + p.showname + "  ·  " + p.name
	}
	c.LabelClipped(row.X+8, row.Y+4, textW, ic, ColText)
	// Line 2 — OOC name (+ IPID for mods), dimmer.
	sub := ""
	if p.ooc != "" {
		sub = "OOC: " + p.ooc
	}
	if p.ipid != "" && isMod {
		if sub != "" {
			sub += "   ·   "
		}
		sub += "IPID: " + p.ipid
	}
	if sub != "" {
		c.LabelClipped(row.X+8, row.Y+row.H-16, textW, sub, ColTextDim)
	}
}
