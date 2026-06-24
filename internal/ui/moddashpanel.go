package ui

import (
	"strconv"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// #130 CM / mod dashboard — a STANDALONE panel (its own thing; it never bloats the player list)
// for server-software-aware moderation + area (CM) control. Opened from the Extras "Mod / CM"
// entry or its hotkey (Ctrl+/). Closed by default, so it costs nothing until you open it;
// detection + command building run only while it draws (never per frame). The OOC slash-command
// syntax differs per server software, so every Ban / Kick goes through a box with a LIVE PREVIEW
// of the exact command before it sends — a wrong-syntax ban silently fails on the server, so the
// user sees precisely what will go out. The per-software command builders live in
// internal/courtroom/modcommands.go (unit-tested); this file is only their UI.

const (
	modDashW  = int32(680)
	modDashH  = int32(520)
	modDashIn = int32(18) // inner padding
)

// modDashChipOn is the "active" status-chip colour (green; matches the ping chip's "good").
var modDashChipOn = sdl.Color{R: 70, G: 200, B: 90, A: 255}

// toggleModDash opens / closes the dashboard (the Extras entry + the hotkey). Closing it also
// drops any half-filled ban/kick box, so it can't reappear out of context next open.
func (a *App) toggleModDash() {
	a.showModDash = !a.showModDash
	if !a.showModDash {
		a.banBoxKind = 0
	}
}

// cycleModDashSoftware advances the manual software override one step (… → KFO → Athena → Akashi
// → Whisker → Nyathena → auto). SoftwareUnknown means "auto-detect from the ID packet".
func (a *App) cycleModDashSoftware() {
	a.cmSoftwareOverride = (a.cmSoftwareOverride + 1) % courtroom.ServerSoftwareCount
}

// rosterByUID resolves a live-roster row by UID — the STABLE identity key. The roster slice is
// replaced wholesale on every join/leave (rebuildLiveRoster), so a destructive command must never
// be keyed by a row index; UID survives the rebuild. 0-alloc slice scan.
func (a *App) rosterByUID(uid string) (areaPlayer, bool) {
	if uid == "" {
		return areaPlayer{}, false
	}
	for i := range a.liveRoster {
		if a.liveRoster[i].uid == uid {
			return a.liveRoster[i], true
		}
	}
	return areaPlayer{}, false
}

// rosterDisplayName is the human label for a roster row: showname, else character, else a generic.
func rosterDisplayName(p areaPlayer) string {
	if p.showname != "" {
		return p.showname
	}
	if p.name != "" {
		return p.name
	}
	return "Spectator"
}

// sendModCommand fires one already-built OOC command (the only send path for the dashboard) and
// flashes a confirming toast of exactly what went out. Refuses an empty command defensively.
func (a *App) sendModCommand(cmd string) {
	if cmd == "" || a.sess == nil {
		return
	}
	a.sess.SendOOC(a.oocNameOrDefault(), cmd)
	a.warnLine = clampLine("Sent: " + cmd)
	a.warnAt = a.now()
}

// openModDashBox opens the Ban (kind 1) / Kick (kind 2) box for the selected target, FREEZING the
// target's identity into the box state. A roster rebuild while the reason is being typed then
// can't repoint the command at whoever shifted into that slot — only the IPID is allowed to fill
// in later (re-resolved by the frozen UID, i.e. the same person).
func (a *App) openModDashBox(kind int) {
	row, ok := a.rosterByUID(a.modDashTargetUID)
	if !ok {
		return
	}
	a.banBoxKind = kind
	a.banBoxUID = row.uid
	a.banBoxIPID = row.ipid
	a.banBoxName = rosterDisplayName(row)
	a.banBoxReason = ""
	a.banBoxDur = courtroom.Ban1Day // a sane default duration
}

// fetchAreaForBan asks the server for the area roster (/getarea), the mod-only reply that carries
// IPIDs. The ban box re-resolves the frozen target's IPID from that reply (by UID), so an
// IPID-only server's ban preview fills in once it lands.
func (a *App) fetchAreaForBan() {
	if a.sess == nil {
		return
	}
	a.queueOOCLines([]string{"/getarea"})
	a.warnLine = "Fetching area info (/getarea) — IPID fills in when the server replies."
	a.warnAt = a.now()
}

// drawModDashPanel paints the dashboard (or the ban/kick box on top of it) and handles its input.
func (a *App) drawModDashPanel(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 210}) // backdrop dim
	if a.banBoxKind != 0 {
		a.drawModDashBanBox(w, h) // the box owns all input while open
		return
	}
	if c.escPressed {
		a.showModDash = false
		return
	}
	pw, ph := modDashW, modDashH
	if pw > w-16 {
		pw = w - 16
	}
	if ph > h-16 {
		ph = h - 16
	}
	panel := sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	x := panel.X + modDashIn

	c.Heading(x, panel.Y+12, "Mod / CM Dashboard", ColText)
	if c.Button(sdl.Rect{X: panel.X + pw - modDashIn - 74, Y: panel.Y + 12, W: 74, H: btnH}, "Close") {
		a.showModDash = false
		return
	}

	// Software row: detected (or overridden) family + a Change button that cycles the override.
	sw := a.detectedSoftware()
	sy := panel.Y + 48
	c.Label(x, sy+5, "Server:", ColTextDim)
	vx := x + c.TextWidth("Server:") + 8
	c.Label(vx, sy+5, sw.String(), ColText)
	mode := "auto-detected"
	if a.cmSoftwareOverride != courtroom.SoftwareUnknown {
		mode = "manual override"
	}
	c.Label(vx+c.TextWidth(sw.String())+10, sy+5, "("+mode+")", ColTextDim)
	if c.Button(sdl.Rect{X: panel.X + pw - modDashIn - 90, Y: sy, W: 90, H: btnH}, "Change") {
		a.cycleModDashSoftware()
	}

	// Status chips + a one-line situational hint.
	cy := panel.Y + 80
	nx := a.drawModDashChip(x, cy, "MOD", a.amIMod())
	nx = a.drawModDashChip(nx, cy, "CM", a.amICM())
	hintW := panel.X + pw - modDashIn - nx - 4
	switch {
	case !a.dashSoftwareKnown():
		c.LabelClipped(nx+4, cy+3, hintW, "Unknown software — pick one with Change to enable commands.", ColDanger)
	case !a.amIMod() && !a.amICM():
		c.LabelClipped(nx+4, cy+3, hintW, "Neither mod nor CM here — commands stay reference-only.", ColTextDim)
	}

	c.Fill(sdl.Rect{X: x, Y: panel.Y + 108, W: pw - 2*modDashIn, H: 1}, ColPanelHi) // divider

	// Body: roster picker (left) + actions (right). Footer: the server's command reference.
	bodyTop := panel.Y + 118
	bodyBottom := panel.Y + ph - 100 - 14
	leftW := int32(322)
	rightX := x + leftW + 16
	rightW := panel.X + pw - modDashIn - rightX
	c.Label(x, bodyTop, "Players in area ("+strconv.Itoa(len(a.liveRoster))+")", ColTextDim)
	a.drawModDashRoster(sdl.Rect{X: x, Y: bodyTop + 22, W: leftW, H: bodyBottom - (bodyTop + 22)})
	a.drawModDashActions(rightX, bodyTop, rightW)

	fy := bodyBottom + 12
	c.Label(x, fy, "This server's commands:", ColTextDim)
	fy += 18
	for _, line := range courtroom.CommandReference(sw) {
		c.LabelClipped(x, fy, pw-2*modDashIn, line, ColTextDim)
		fy += 15
	}
}

// drawModDashChip paints one status pill (green when active) and returns the x past it.
func (a *App) drawModDashChip(x, y int32, label string, on bool) int32 {
	c := a.ctx
	cw := c.TextWidth(label) + 16
	r := sdl.Rect{X: x, Y: y, W: cw, H: 20}
	col, txt := ColPanelHi, ColTextDim
	if on {
		col, txt = modDashChipOn, ColBackground
	}
	c.Fill(r, col)
	c.Label(x+8, y+3, label, txt)
	return x + cw + 8
}

// drawModDashActions draws the right column: the selected target, the Moderation buttons (gated on
// being mod), and the Room (CM) controls. Claim-CM sits OUTSIDE the amICM() gate (you can't be CM
// yet when you claim it); the uncm / area-kick / lock controls are gated on actually being CM.
func (a *App) drawModDashActions(x, y, w int32) {
	c := a.ctx
	row, hasTarget := a.rosterByUID(a.modDashTargetUID)

	if hasTarget {
		c.LabelClipped(x, y, w, "Target: ["+row.uid+"] "+rosterDisplayName(row), ColText)
		y += 18
		ip := row.ipid
		if ip == "" {
			ip = "— (mod-only; fetch in the ban box)"
		}
		c.LabelClipped(x, y, w, "IPID: "+ip, ColTextDim)
		y += 24
	} else {
		c.LabelClipped(x, y, w, "Pick a player on the left.", ColTextDim)
		y += 26
	}

	// Moderation (ban / kick) — needs mod auth, a known software, and a target.
	c.Label(x, y, "Moderation", ColAccent)
	y += 20
	switch {
	case !a.amIMod():
		c.LabelClipped(x, y, w, "Log in as mod to ban / kick (Extras → Login).", ColTextDim)
		y += 24
	case !a.dashSoftwareKnown():
		c.LabelClipped(x, y, w, "Pick the server software (Change) first.", ColDanger)
		y += 24
	case !hasTarget:
		c.LabelClipped(x, y, w, "Select a player to ban / kick.", ColTextDim)
		y += 24
	default:
		if c.Button(sdl.Rect{X: x, Y: y, W: 110, H: btnH}, "Ban…") {
			a.openModDashBox(1)
		}
		if c.Button(sdl.Rect{X: x + 120, Y: y, W: 110, H: btnH}, "Kick…") {
			a.openModDashBox(2)
		}
		y += btnH + 10
	}

	// CM (area control) is its OWN panel now (#130) — it opens from the corner "CM" chip the
	// moment you hold CM, so the mod dashboard stays focused on ban / kick. Claim CM with /cm.
	y += 6
	c.Label(x, y, "Room (CM)", ColAccent)
	y += 20
	if a.amICMNow {
		c.LabelClipped(x, y, w, "You hold CM — open the CM panel from the corner chip.", ColTextDim)
	} else {
		c.LabelClipped(x, y, w, "Type /cm to claim CM; its controls open from the corner chip.", ColTextDim)
	}
}

// drawModDashRoster renders the live roster as a clickable, scrollable list. Selecting a row sets
// modDashTargetUID (by UID, never index). Mod-only IPID is shown inline when enriched.
func (a *App) drawModDashRoster(r sdl.Rect) {
	c := a.ctx
	c.Border(r, ColPanelHi)
	if len(a.liveRoster) == 0 {
		c.LabelClipped(r.X+6, r.Y+6, r.W-12, "No players yet (or no live list on this server).", ColTextDim)
		return
	}
	lineH := int32(26)
	if !c.ctrlHeld {
		a.modDashScroll -= c.WheelIn(r) * scrollStepPx
	}
	contentH := int32(len(a.liveRoster)) * lineH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.modDashScroll = c.VScrollbar("moddashroster", track, a.modDashScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowY := r.Y - a.modDashScroll
	rowW := r.W - scrollBarW - 4
	for i := range a.liveRoster {
		p := a.liveRoster[i]
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-lineH {
			rrow := sdl.Rect{X: r.X, Y: rowY, W: rowW, H: lineH - 2}
			selected := p.uid != "" && p.uid == a.modDashTargetUID
			if selected {
				c.Fill(rrow, ColAccent)
			} else if c.hovering(rrow) {
				c.Fill(rrow, ColPanelHi)
			}
			if c.hovering(rrow) && c.clicked && p.uid != "" {
				a.modDashTargetUID = p.uid
			}
			label := "[" + p.uid + "] " + rosterDisplayName(p)
			if p.ipid != "" {
				label += "  · ip:" + p.ipid
			}
			col := ColText
			if selected {
				col = ColBackground
			}
			c.LabelClipped(rrow.X+6, rrow.Y+4, rowW-12, label, col)
		}
		rowY += lineH
	}
}

// drawModDashBanBox is the Ban (kind 1) / Kick (kind 2) sub-modal: the frozen target, a duration
// picker (ban only), a reason field, and a LIVE PREVIEW of the exact command. Send refuses an
// empty command; when the preview is empty because an IPID-only server hasn't surfaced the IPID
// yet, it explains and offers a one-click fetch instead of silently disabling the button.
func (a *App) drawModDashBanBox(w, h int32) {
	c := a.ctx
	// Lazy IPID fill: re-resolve the FROZEN uid's IPID (same person — safe) from the enriched
	// roster, else from the raw /getarea snapshot, so a fetch populates the preview live.
	if a.banBoxIPID == "" {
		if row, ok := a.rosterByUID(a.banBoxUID); ok && row.ipid != "" {
			a.banBoxIPID = row.ipid
		} else if ip := a.ipidByUID()[a.banBoxUID]; ip != "" {
			a.banBoxIPID = ip
		}
	}
	if c.escPressed {
		a.banBoxKind = 0
		return
	}
	isBan := a.banBoxKind == 1
	bw, bh := int32(560), int32(372) // ban: taller — the duration presets wrap to two rows
	if !isBan {
		bh = 250
	}
	panel := sdl.Rect{X: (w - bw) / 2, Y: (h - bh) / 2, W: bw, H: bh}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColDanger)
	x := panel.X + modDashIn
	maxW := bw - 2*modDashIn

	title := "Kick"
	if isBan {
		title = "Ban"
	}
	c.Heading(x, panel.Y+14, title+"  ["+a.banBoxUID+"] "+a.banBoxName, ColText)
	y := panel.Y + 44
	ipShown := a.banBoxIPID
	if ipShown == "" {
		ipShown = "not fetched yet"
	}
	c.LabelClipped(x, y, maxW, "UID "+a.banBoxUID+"    IPID "+ipShown, ColTextDim)
	y += 24

	sw := a.detectedSoftware()
	if isBan {
		c.Label(x, y, "Duration:", ColTextDim)
		y += 20
		dx := x
		for d := courtroom.BanPerma; d < courtroom.BanDurationCount; d++ {
			label := courtroom.BanDurationLabel(d)
			dw := c.TextWidth(label) + 16
			if dx+dw > x+maxW {
				dx = x
				y += btnH + 6
			}
			br := sdl.Rect{X: dx, Y: y, W: dw, H: btnH}
			if c.Button(br, label) {
				a.banBoxDur = d
			}
			if d == a.banBoxDur {
				c.Border(br, ColAccent) // highlight the chosen preset
			}
			dx += dw + 6
		}
		y += btnH + 10
	}

	c.Label(x, y, "Reason:", ColTextDim)
	y += 20
	a.banBoxReason, _ = c.TextField("moddashreason", sdl.Rect{X: x, Y: y, W: maxW, H: fieldH}, a.banBoxReason, "reason (optional for kick)")
	y += fieldH + 12

	// Live preview of the exact command.
	var cmd string
	if isBan {
		cmd = courtroom.BanCommand(sw, a.banBoxIPID, a.banBoxUID, a.banBoxDur, a.banBoxReason)
	} else {
		cmd = courtroom.KickCommand(sw, a.banBoxIPID, a.banBoxUID, a.banBoxReason)
	}
	c.Label(x, y, "Will send:", ColTextDim)
	y += 20
	if cmd != "" {
		c.LabelClipped(x, y, maxW, cmd, ColAccent)
	} else {
		needIPID := a.banBoxIPID == "" && (sw == courtroom.SoftwareTsuserver || sw == courtroom.SoftwareAkashi)
		switch {
		case !a.dashSoftwareKnown():
			c.LabelClipped(x, y, maxW, "Pick the server software first (Close, then Change).", ColDanger)
		case needIPID:
			c.LabelClipped(x, y, maxW, "This server bans by IPID (mod-only). Fetch it, then it fills in:", ColDanger)
			if c.Button(sdl.Rect{X: x, Y: y + 22, W: 210, H: btnH}, "Fetch area info (/getarea)") {
				a.fetchAreaForBan()
			}
		default:
			c.LabelClipped(x, y, maxW, "Missing the identifier this server needs to "+title+".", ColDanger)
		}
	}

	// Send (only when a real command exists) + Cancel.
	by := panel.Y + bh - btnH - 14
	if cmd != "" {
		send := title + " (send)"
		if c.Button(sdl.Rect{X: x, Y: by, W: 160, H: btnH}, send) {
			a.sendModCommand(cmd)
			a.banBoxKind = 0
			return
		}
	}
	if c.Button(sdl.Rect{X: x + 172, Y: by, W: 100, H: btnH}, "Cancel") {
		a.banBoxKind = 0
	}
}
