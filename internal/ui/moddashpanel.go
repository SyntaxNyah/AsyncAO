package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// modRosterRowH is the height of a rich (two-line) roster row in the mod / CM panels.
const modRosterRowH = int32(40)

// drawModRosterIdentity paints the rich two-line identity for one roster row, shared by the mod
// dashboard and the CM panel: line 1 "[uid] showname · character", line 2 "OOC: … · IPID: …"
// (dimmer). nameCol recolours line 1 (e.g. a selected row); textW caps the width so a caller can
// leave room for a per-row button. Off the hot path (opt-in panels), so the string building is fine.
func (a *App) drawModRosterIdentity(p areaPlayer, x, rowY, textW int32, nameCol sdl.Color) {
	c := a.ctx
	display := rosterDisplayName(p)
	ic := "[" + p.uid + "] " + display
	if p.name != "" && !strings.EqualFold(display, p.name) {
		ic = "[" + p.uid + "] " + display + " · " + p.name // showname (IC name) then the character
	}
	c.LabelClipped(x, rowY+3, textW, ic, nameCol)
	sub := ""
	if p.ooc != "" && p.showname != "" { // skip OOC when it was promoted to the identity line above
		sub = "OOC: " + p.ooc
	}
	if p.ipid != "" { // mod-only; show whenever we actually have it
		if sub != "" {
			sub += "   ·   "
		}
		sub += "IPID: " + p.ipid
	}
	if sub != "" {
		c.LabelClipped(x, rowY+21, textW, sub, ColTextDim)
	}
}

// #130 CM / mod dashboard — a STANDALONE panel (its own thing; it never bloats the player list)
// for server-software-aware moderation + area (CM) control. Opened from the Extras "Mod / CM"
// entry or its hotkey (Ctrl+/). Closed by default, so it costs nothing until you open it;
// detection + command building run only while it draws (never per frame). The OOC slash-command
// syntax differs per server software, so every Ban / Kick goes through a box with a LIVE PREVIEW
// of the exact command before it sends — a wrong-syntax ban silently fails on the server, so the
// user sees precisely what will go out. The per-software command builders live in
// internal/courtroom/modcommands.go (unit-tested); this file is only their UI.

const (
	modDashW    = int32(680)
	modDashH    = int32(520)
	modDashIn   = int32(18)  // inner padding
	modDashMinW = int32(560) // floating-box size floors (resizable down to here)
	modDashMinH = int32(420)
)

// The ban/kick box is a non-blocking floating box too (its own floatWin: banWin),
// so a mod can drag it aside and keep chatting while it's open — it never blanks
// the courtroom. Width is shared; the HEIGHT defaults + floors are per-kind
// (banBoxDims) because the four boxes carry different content (kick has no
// duration row; the bulk boxes carry a frozen target list).
const (
	banBoxDefW = int32(560)
	banBoxMinW = int32(440)
)

// banBoxDims gives the default + minimum HEIGHT for the ban/kick box of a given
// kind (1 ban, 2 kick, 3 bulk ban, 4 bulk kick). The min floors each fit that
// kind's content so a resize-to-minimum never overlaps the bottom Send/Cancel row.
func banBoxDims(kind int) (defH, minH int32) {
	switch kind {
	case 2: // kick — no duration row
		return 416, 384
	case 3: // bulk ban — frozen list + duration + reason + summary + readiness
		return 584, 524
	case 4: // bulk kick — frozen list + reason + summary + readiness
		return 476, 436
	default: // ban (kind 1)
		return 536, 494
	}
}

// banBoxRect is the ban/kick box's floating-window rect (floatwin.go), sized for
// the open kind. Shared banWin geometry: a position/size the mod set carries over.
func (a *App) banBoxRect(w, h int32) sdl.Rect {
	defH, minH := banBoxDims(a.banBoxKind)
	return a.banWin.rect(banBoxDefW, defH, banBoxMinW, minH, w, h)
}

// modDashChipOn is the "active" status-chip colour (green; matches the ping chip's "good").
var modDashChipOn = sdl.Color{R: 70, G: 200, B: 90, A: 255}

// drawReasonTemplateChips draws the editable ban/kick reason chips (from prefs). Clicking a chip
// fills banBoxReason; the active one is outlined. When manage is set (the single box, which has the
// room — the bulk box passes false to stay compact) it also shows "+ Save" (store the current
// reason as a chip) and an Edit toggle whose chips show a × that removes them. Returns the y past
// the block. Shared by the single and bulk boxes so the list and behaviour can't drift apart.
func (a *App) drawReasonTemplateChips(x, y, maxW int32, manage bool) int32 {
	c := a.ctx
	tpls := a.d.Prefs.ModReasonTemplatesList()
	editing := manage && a.modTemplatesEdit
	tx := x
	for _, tpl := range tpls {
		label := tpl
		if editing {
			label = "× " + tpl
		}
		tw := c.TextWidth(label) + 16
		if tx+tw > x+maxW {
			tx = x
			y += btnH + 6
		}
		tr := sdl.Rect{X: tx, Y: y, W: tw, H: btnH}
		if c.Button(tr, label) {
			if editing {
				a.d.Prefs.RemoveModReasonTemplate(tpl)
			} else {
				a.banBoxReason = tpl
			}
		}
		if !editing && a.banBoxReason == tpl {
			c.Border(tr, ColAccent) // outline the active template
		}
		tx += tw + 6
	}
	y += btnH + 6
	if manage {
		if !editing && strings.TrimSpace(a.banBoxReason) != "" {
			if c.Button(sdl.Rect{X: x, Y: y, W: 150, H: btnH}, "+ Save reason") {
				a.d.Prefs.AddModReasonTemplate(a.banBoxReason)
			}
		}
		editLabel := "Edit"
		if editing {
			editLabel = "Done"
		}
		if c.Button(sdl.Rect{X: x + maxW - 64, Y: y, W: 64, H: btnH}, editLabel) {
			a.modTemplatesEdit = !a.modTemplatesEdit
		}
		y += btnH + 6
	}
	return y + 6
}

// formatModAudit renders the session audit log as plain text (one tab-separated row per entry) for
// the clipboard export. Pure — testable without SDL.
func (a *App) formatModAudit() string {
	var b strings.Builder
	b.WriteString("AsyncAO mod audit")
	if a.serverName != "" {
		b.WriteString(" — " + a.serverName)
	}
	b.WriteByte('\n')
	for _, e := range a.modAudit {
		b.WriteString(e.at.Format("2006-01-02 15:04:05"))
		b.WriteByte('\t')
		b.WriteString(e.action)
		b.WriteByte('\t')
		b.WriteString(e.target)
		b.WriteByte('\t')
		b.WriteString(e.cmd)
		b.WriteByte('\n')
	}
	return b.String()
}

// copyModAudit puts the audit log on the clipboard — the "export" path. Clipboard, not a file, so
// there's no disk I/O on the render thread (the phone book / jukebox share the same way, §17.2).
func (a *App) copyModAudit() {
	if len(a.modAudit) == 0 {
		return
	}
	_ = sdl.SetClipboardText(a.formatModAudit())
	a.warnLine = clampLine("Copied " + strconv.Itoa(len(a.modAudit)) + " audit entries to the clipboard.")
	a.warnAt = a.now()
}

// modAuditCap bounds the session audit log (hard rule #4 / spec §17.4: every buffer is capped).
// Oldest entries drop once it's full — the dashboard only needs "what did I just do this session",
// not a permanent record.
const modAuditCap = 100

// modAuditEntry is one logged dashboard command: when it was sent, the action label ("Ban" / "Kick"),
// the frozen target identity, and the exact OOC command that went out.
type modAuditEntry struct {
	at     time.Time
	action string
	target string
	cmd    string
}

// recordModAudit appends one sent command to the session audit log, dropping the oldest entry when
// the log is full. It's the only writer; the dashboard's Send paths call it just before sending.
func (a *App) recordModAudit(action, target, cmd string) {
	if cmd == "" {
		return
	}
	a.modAudit = append(a.modAudit, modAuditEntry{at: a.now(), action: action, target: target, cmd: cmd})
	if len(a.modAudit) > modAuditCap {
		a.modAudit = a.modAudit[len(a.modAudit)-modAuditCap:] // keep the newest modAuditCap entries
	}
}

// modBulkCap caps how many players can be ticked for a bulk ban / kick at once (#13). It bounds the
// selection map (hard rule #4) and is a guard rail against a fat-fingered "ban the whole room".
const modBulkCap = 50

// toggleModSelected ticks / unticks a UID for the bulk action, lazily creating the set and pruning
// the entry on untick so the map only ever holds ticked UIDs. Refuses to grow past modBulkCap.
func (a *App) toggleModSelected(uid string) {
	if uid == "" {
		return
	}
	if a.modDashSelected[uid] {
		delete(a.modDashSelected, uid)
		return
	}
	if len(a.modDashSelected) >= modBulkCap {
		a.warnLine = clampLine("Bulk selection is capped at " + strconv.Itoa(modBulkCap) + " players.")
		a.warnAt = a.now()
		return
	}
	if a.modDashSelected == nil {
		a.modDashSelected = make(map[string]bool)
	}
	a.modDashSelected[uid] = true
}

// clearModSelected drops the whole bulk selection (the "Clear" button) — also the escape hatch for
// ticked players who have since left, since their rows are gone and can't be unticked individually.
func (a *App) clearModSelected() {
	for k := range a.modDashSelected {
		delete(a.modDashSelected, k)
	}
}

// selectedPresentUIDs returns the ticked UIDs that are STILL in the roster, in roster order. A
// player who left is silently dropped, so a bulk command never targets a stale slot. Order matches
// the roster so the bulk box lists people the way the mod sees them.
func (a *App) selectedPresentUIDs() []string {
	if len(a.modDashSelected) == 0 {
		return nil
	}
	roster := a.rosterView()
	out := make([]string, 0, len(a.modDashSelected))
	for i := range roster {
		if uid := roster[i].uid; uid != "" && a.modDashSelected[uid] {
			out = append(out, uid)
		}
	}
	return out
}

// countSelectedPresent is the allocation-free count of ticked-and-still-present players, for the
// per-frame button labels (selectedPresentUIDs allocates, so it's reserved for the one-shot freeze).
func (a *App) countSelectedPresent() int {
	if len(a.modDashSelected) == 0 {
		return 0
	}
	roster := a.rosterView()
	n := 0
	for i := range roster {
		if uid := roster[i].uid; uid != "" && a.modDashSelected[uid] {
			n++
		}
	}
	return n
}

// openModBulkBox freezes the currently-ticked, present UIDs into the bulk box and opens it
// (banBoxKind 3 = bulk ban, 4 = bulk kick). Freezing by UID matches the single box's safety: a
// roster rebuild while a reason is typed can't repoint the batch. A reused reason/duration carries
// over from the single box, so default the reason empty and the duration to a sane value.
func (a *App) openModBulkBox(kind int) {
	uids := a.selectedPresentUIDs()
	if len(uids) == 0 {
		return
	}
	a.banBoxKind = kind
	a.bulkBoxUIDs = uids
	a.banBoxReason = ""
	a.banBoxDur = courtroom.Ban1Day
}

// bulkCommandFor builds the exact OOC command for one bulk target UID, re-resolving its IPID by UID
// (from the enriched roster, else the raw /getarea snapshot) — same per-person resolution the single
// box does. Returns "" when the server needs an identifier we don't have yet (e.g. an un-fetched
// IPID), so the caller can count and report those instead of sending a broken command.
func (a *App) bulkCommandFor(uid string, isBan bool) string {
	ipid := ""
	if row, ok := a.rosterByUID(uid); ok && row.ipid != "" {
		ipid = row.ipid
	} else if ip := a.ipidByUID()[uid]; ip != "" {
		ipid = ip
	}
	sw := a.detectedSoftware()
	if isBan {
		return courtroom.BanCommand(sw, ipid, uid, a.banBoxDur, a.banBoxReason)
	}
	return courtroom.KickCommand(sw, ipid, uid, a.banBoxReason)
}

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
	a.modTemplatesEdit = false      // start in normal (fill) mode, not the template editor
}

// fetchAreaForBan asks the server for the area roster — the mod-only reply that carries IPIDs — so
// the ban box re-resolves the frozen target's IPID from it (by UID). It sends /getareas (ALL areas),
// not /getarea (current area only): the target may be in another area, and Akashi surfaces the
// mod-only IPID via /getareas (the user confirmed plain /getarea doesn't). pairAreaReset starts a
// clean roster (Akashi's "=== area ===" blocks carry no "----" reset marker), and the reply burst is
// kept out of OOC like the other roster pulls. (WAP doesn't reach here — its IPID arrives live.)
func (a *App) fetchAreaForBan() {
	if a.sess == nil {
		return
	}
	a.pairAreaReset = true
	a.suppressAreaEchoUntil = a.now().Add(areaEchoSuppressWindow)
	a.queueOOCLines([]string{"/getareas"})
	a.warnLine = "Fetching area info (/getareas) — IPID fills in when the server replies."
	a.warnAt = a.now()
}

// drawModDashPanel paints the dashboard (or the ban/kick box on top of it) and handles its input.
// modDashRect is the Mod dashboard's floating-window rect (floatwin.go).
func (a *App) modDashRect(w, h int32) sdl.Rect {
	return a.modWin.rect(modDashW, modDashH, modDashMinW, modDashMinH, w, h)
}

func (a *App) drawModDashPanel(w, h int32, pressed *bool) {
	c := a.ctx
	if c.escPressed {
		a.showModDash = false
		return
	}
	panel := a.modDashRect(w, h) // floating box: movable / resizable, non-blocking
	pw, ph := panel.W, panel.H
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: floatTitleH}, ColPanelHi) // title bar / drag handle
	a.floatWinDrag(&a.modWin, sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W - 84 - modDashIn, H: floatTitleH}, pressed)
	mgrip := sdl.Rect{X: panel.X + panel.W - floatGripSz, Y: panel.Y + panel.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.modWin, mgrip, panel, modDashMinW, modDashMinH, pressed)
	a.drawResizeGrip(mgrip)
	x := panel.X + modDashIn

	c.Heading(x, panel.Y+12, "Moderation — Ban / Kick", ColText)
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
	// Left column: the roster picker OR the session audit log (#13), switched by a two-button toggle.
	rosterLabel := "Players (" + strconv.Itoa(len(a.rosterView())) + ")"
	auditLabel := "Audit (" + strconv.Itoa(len(a.modAudit)) + ")"
	rosterBtn := sdl.Rect{X: x, Y: bodyTop, W: c.TextWidth(rosterLabel) + 22, H: btnH}
	auditBtn := sdl.Rect{X: rosterBtn.X + rosterBtn.W + 8, Y: bodyTop, W: c.TextWidth(auditLabel) + 22, H: btnH}
	if c.Button(rosterBtn, rosterLabel) {
		a.modDashShowAudit = false
	}
	if c.Button(auditBtn, auditLabel) {
		a.modDashShowAudit = true
	}
	if a.modDashShowAudit {
		c.Border(auditBtn, ColAccent)
	} else {
		c.Border(rosterBtn, ColAccent)
	}
	if a.modDashShowAudit && len(a.modAudit) > 0 { // export the audit log to the clipboard
		if c.Button(sdl.Rect{X: auditBtn.X + auditBtn.W + 8, Y: bodyTop, W: 60, H: btnH}, "Copy") {
			a.copyModAudit()
		}
	}
	leftRect := sdl.Rect{X: x, Y: bodyTop + btnH + 6, W: leftW, H: bodyBottom - (bodyTop + btnH + 6)}
	if a.modDashShowAudit {
		a.drawModDashAudit(leftRect)
	} else {
		a.drawModDashRoster(leftRect)
	}
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

	// Bulk ban / kick (#13): acts on the ticked set, INDEPENDENT of the single target above. The
	// section only appears once at least one present player is ticked. nStr drives the button labels.
	if n := a.countSelectedPresent(); n > 0 {
		y += 2
		nStr := strconv.Itoa(n)
		c.Label(x, y, "Bulk — "+nStr+" ticked", modDashChipOn)
		if c.Button(sdl.Rect{X: x + w - 64, Y: y - 4, W: 64, H: btnH}, "Clear") {
			a.clearModSelected()
		}
		y += 22
		if a.amIMod() && a.dashSoftwareKnown() {
			if c.Button(sdl.Rect{X: x, Y: y, W: 110, H: btnH}, "Ban "+nStr+"…") {
				a.openModBulkBox(3)
			}
			if c.Button(sdl.Rect{X: x + 120, Y: y, W: 110, H: btnH}, "Kick "+nStr+"…") {
				a.openModBulkBox(4)
			}
		} else {
			c.LabelClipped(x, y, w, "Log in as mod (known software) to bulk-act.", ColTextDim)
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

// drawModDashRoster renders the live roster as a clickable, scrollable list with a character icon
// and rich two-line rows (showname · IC · OOC · UID · IPID). It reuses the player list's grouped,
// memoized row layout (playerRosterRows), so a /gas spanning areas shows the SAME area-grouped
// headers the player list does — organised by the server software's own area order — and shares
// the player list's index-keyed icon cache (same rosterView indices), so faces stay warm in both.
// Selecting a row sets modDashTargetUID (by UID, never index, so a roster rebuild can't repoint it).
func (a *App) drawModDashRoster(r sdl.Rect) {
	c := a.ctx
	c.Border(r, ColPanelHi)
	if len(a.rosterView()) == 0 {
		c.LabelClipped(r.X+6, r.Y+6, r.W-12, "No players yet (or no live list on this server).", ColTextDim)
		return
	}
	// Pass the SAME speaker arg the player list passes: playerRosterRows memoizes on it over one
	// shared backing slice, so a mismatch would rebuild that slice every frame while both panels
	// are open (a per-frame allocation). currentSpeakerName is what drawPlayerList feeds it too.
	speaker := a.currentSpeakerName()
	rows := a.playerRosterRows(speaker) // flat for a /ga, area-grouped (with headers) for a /gas
	if !c.ctrlHeld {
		a.modDashScroll -= c.WheelIn(r) * scrollStepPx
	}
	contentH := int32(0)
	for i := range rows {
		contentH += modRowHeight(rows[i])
	}
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.modDashScroll = c.VScrollbar("moddashroster", track, a.modDashScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowW := r.W - scrollBarW - 4
	rowY := r.Y - a.modDashScroll
	for i := range rows {
		rh := modRowHeight(rows[i])
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-rh {
			if rows[i].header {
				a.drawAreaHeaderRow(rows[i], sdl.Rect{X: r.X, Y: rowY, W: rowW, H: rh - 2})
			} else {
				a.drawModRosterRow(rows[i].idx, sdl.Rect{X: r.X, Y: rowY, W: rowW, H: rh - 2})
			}
		}
		rowY += rh
	}
}

// modRowHeight is the display height of one mod-dashboard roster row. Area-group headers are short
// (playerHeaderH, shared with the player list); player rows keep the full two-line identity height
// (modRosterRowH). Unlike the player list it does NOT scale with the Players-tab zoom — the
// dashboard wants a stable target row a mod can reliably click.
func modRowHeight(row rosterRow) int32 {
	if row.header {
		return playerHeaderH
	}
	return modRosterRowH
}

// drawModRosterRow is one mod-dashboard player row: a character icon (shared player-list cache),
// the rich two-line identity, and click-to-target (by UID). It mirrors the player list's row so a
// mod sees the same faces, but the click selects a ban / kick target instead of the pair / copy
// actions. idx is the rosterView index — the icon-cache key, valid because it came from
// playerRosterRows iterating that same rosterView.
func (a *App) drawModRosterRow(idx int, rrow sdl.Rect) {
	c := a.ctx
	p := &a.rosterView()[idx]
	selected := p.uid != "" && p.uid == a.modDashTargetUID // single ban/kick target (accent row)
	checked := p.uid != "" && a.modDashSelected[p.uid]     // ticked for the bulk batch (green box)
	switch {
	case selected:
		c.Fill(rrow, ColAccent)
	case checked:
		c.Fill(rrow, sdl.Color{R: modDashChipOn.R, G: modDashChipOn.G, B: modDashChipOn.B, A: 50}) // subtle "ticked" tint
	case c.hovering(rrow):
		c.Fill(rrow, ColPanelHi)
	}

	// Bulk tick-box on the right edge. Toggling it CONSUMES the click so the row-body select below
	// doesn't also fire (the box sits inside the row rect, so both would otherwise see the click).
	cbSz := int32(18)
	cb := sdl.Rect{X: rrow.X + rrow.W - cbSz - 6, Y: rrow.Y + (rrow.H-cbSz)/2, W: cbSz, H: cbSz}
	clickedCB := false
	if p.uid != "" {
		c.Border(cb, ColPanelHi)
		if checked {
			c.Fill(sdl.Rect{X: cb.X + 4, Y: cb.Y + 4, W: cbSz - 8, H: cbSz - 8}, modDashChipOn)
		}
		if c.hovering(cb) && c.clicked {
			a.toggleModSelected(p.uid)
			clickedCB = true
		}
	}
	if !clickedCB && c.hovering(rrow) && c.clicked && p.uid != "" {
		a.modDashTargetUID = p.uid
	}

	isSpec := strings.EqualFold(p.name, "Spectator")
	iconSz := modRosterRowH - 12
	iconR := sdl.Rect{X: rrow.X + 6, Y: rrow.Y + (rrow.H-iconSz)/2, W: iconSz, H: iconSz}
	a.drawPlayerIcon(p, idx, iconR, isSpec)
	nameCol := ColText
	if selected {
		nameCol = ColBackground
	}
	textX := rrow.X + 6 + iconSz + 8
	a.drawModRosterIdentity(*p, textX, rrow.Y, cb.X-8-textX, nameCol) // stop the text short of the tick-box
}

// drawModDashAudit renders the session audit log (newest first): a bounded, scrollable list of the
// ban / kick commands sent from this dashboard, each with its timestamp, action, target and the
// exact command. Read-only — a record of what went out, not a re-send path. Opt-in (only when the
// Audit view is selected), so its per-row string building stays off the render hot path.
func (a *App) drawModDashAudit(r sdl.Rect) {
	c := a.ctx
	c.Border(r, ColPanelHi)
	if len(a.modAudit) == 0 {
		c.LabelClipped(r.X+6, r.Y+6, r.W-12, "No ban / kick actions sent yet this session.", ColTextDim)
		return
	}
	if !c.ctrlHeld {
		a.modAuditScroll -= c.WheelIn(r) * scrollStepPx
	}
	const auditRowH = int32(38)
	contentH := int32(len(a.modAudit)) * auditRowH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.modAuditScroll = c.VScrollbar("moddashaudit", track, a.modAuditScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowW := r.W - scrollBarW - 8
	rowY := r.Y - a.modAuditScroll
	for i := len(a.modAudit) - 1; i >= 0; i-- { // newest first
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-auditRowH {
			e := a.modAudit[i]
			c.LabelClipped(r.X+6, rowY+3, rowW, e.at.Format("15:04:05")+"   ·   "+e.action+"   ·   "+e.target, ColText)
			c.LabelClipped(r.X+6, rowY+20, rowW, e.cmd, ColTextDim)
		}
		rowY += auditRowH
	}
}

// banActionSummary is the plain-English "who · how long · why" line shown in the ban/kick box, so a
// mod sees exactly what they're about to do — even before the server-specific command can be built
// (e.g. an IPID-only server hasn't surfaced the IPID yet). Pure; unit-tested.
func banActionSummary(isBan bool, who string, dur courtroom.BanDuration, reason string) string {
	s := "Kick " + who
	if isBan {
		s = "Ban " + who + " for " + courtroom.BanDurationLabel(dur)
	}
	if r := strings.TrimSpace(reason); r != "" {
		return s + " — reason: " + r
	}
	return s + " — (no reason given)"
}

// drawModDashBanBox is the Ban (kind 1) / Kick (kind 2) box: the frozen target, a duration
// picker (ban only), a reason field, and a LIVE PREVIEW of the exact command. Send refuses an
// empty command; when the preview is empty because an IPID-only server hasn't surfaced the IPID
// yet, it explains and offers a one-click fetch instead of silently disabling the button. It's a
// NON-BLOCKING floating box now (floatWin: drag the title bar, resize the bottom-right grip), so a
// mod can drag it aside and keep chatting — the live preview + the explicit Send keep the
// don't-accidentally-ban safety the old blocking confirm had.
func (a *App) drawModDashBanBox(w, h int32, pressed *bool) {
	if a.banBoxKind >= 3 { // kinds 3/4 are the BULK ban/kick — a different (frozen-list) box
		a.drawModBulkBox(w, h, pressed)
		return
	}
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
	_, minH := banBoxDims(a.banBoxKind)
	panel := a.banBoxRect(w, h) // floating box: movable / resizable, non-blocking
	pw, ph := panel.W, panel.H
	c.Fill(panel, ColPanel)
	c.Border(panel, ColDanger)
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: floatTitleH}, ColPanelHi) // title bar / drag handle
	a.floatWinDrag(&a.banWin, sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W - 84 - modDashIn, H: floatTitleH}, pressed)
	bgrip := sdl.Rect{X: panel.X + pw - floatGripSz, Y: panel.Y + ph - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.banWin, bgrip, panel, banBoxMinW, minH, pressed)
	a.drawResizeGrip(bgrip)
	x := panel.X + modDashIn
	maxW := pw - 2*modDashIn

	title := "Kick"
	if isBan {
		title = "Ban"
	}
	c.Heading(x, panel.Y+12, title+"  ["+a.banBoxUID+"] "+a.banBoxName, ColText)
	if c.Button(sdl.Rect{X: panel.X + pw - modDashIn - 74, Y: panel.Y + 12, W: 74, H: btnH}, "Close") {
		a.banBoxKind = 0
		return
	}
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
	y += fieldH + 8

	// Quick-reason templates: editable chips fill the field; the single box gets the manage row.
	y = a.drawReasonTemplateChips(x, y, maxW, true)

	// At-a-glance summary of the action (always shown, even before the exact command below can be
	// built) so the mod sees who, how long and why — the "ban someone 6 hours for disrespect" feedback.
	c.LabelClipped(x, y, maxW, banActionSummary(isBan, "["+a.banBoxUID+"] "+a.banBoxName, a.banBoxDur, a.banBoxReason), ColText)
	y += 24

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
		needIPID := a.banBoxIPID == "" && (sw == courtroom.SoftwareTsuserver || sw == courtroom.SoftwareAkashi || sw == courtroom.SoftwareWitches)
		switch {
		case !a.dashSoftwareKnown():
			c.LabelClipped(x, y, maxW, "Pick the server software first (Close, then Change).", ColDanger)
		case needIPID && sw == courtroom.SoftwareWitches:
			// WAP streams the IPID to authenticated mods inside the live player list — nothing to
			// fetch; the mod just has to be logged in for it to arrive on the target's PU name.
			c.LabelClipped(x, y, maxW, "WAP sends the IPID to mods in the player list. Log in as a server mod (Extras → Login) and it fills in.", ColDanger)
		case needIPID:
			c.LabelClipped(x, y, maxW, "This server bans by IPID (mod-only). Fetch it, then it fills in:", ColDanger)
			if c.Button(sdl.Rect{X: x, Y: y + 22, W: 240, H: btnH}, "Fetch area info (/getareas)") {
				a.fetchAreaForBan()
			}
		default:
			c.LabelClipped(x, y, maxW, "Missing the identifier this server needs to "+title+".", ColDanger)
		}
	}

	// Send (only when a real command exists) + Cancel, anchored to the box bottom.
	by := panel.Y + ph - btnH - 14
	if cmd != "" {
		send := title + " (send)"
		if c.Button(sdl.Rect{X: x, Y: by, W: 160, H: btnH}, send) {
			a.recordModAudit(title, "["+a.banBoxUID+"] "+a.banBoxName, cmd) // #13 audit log: record before it goes out
			a.sendModCommand(cmd)
			a.banBoxKind = 0
			return
		}
	}
	if c.Button(sdl.Rect{X: x + 172, Y: by, W: 100, H: btnH}, "Cancel") {
		a.banBoxKind = 0
	}
}

// drawModBulkBox is the bulk Ban (kind 3) / Kick (kind 4) box: a FROZEN list of ticked targets,
// a shared duration (ban) + reason with the same quick-reason templates, a live count of how many
// commands are ready vs. still missing an identifier, and a Send that queues one paced command per
// ready target (each audited) through the macro OOC pacing — so a 30-player batch can't flood the
// server. Frozen by UID like the single box, so a roster churn can't repoint the batch. Also a
// NON-BLOCKING floating box (floatWin: drag the title bar, resize the grip) — chat stays live.
func (a *App) drawModBulkBox(w, h int32, pressed *bool) {
	c := a.ctx
	if c.escPressed {
		a.banBoxKind = 0
		return
	}
	isBan := a.banBoxKind == 3
	_, minH := banBoxDims(a.banBoxKind)
	panel := a.banBoxRect(w, h) // floating box: movable / resizable, non-blocking
	pw, ph := panel.W, panel.H
	c.Fill(panel, ColPanel)
	c.Border(panel, ColDanger)
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: floatTitleH}, ColPanelHi) // title bar / drag handle
	a.floatWinDrag(&a.banWin, sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W - 84 - modDashIn, H: floatTitleH}, pressed)
	bgrip := sdl.Rect{X: panel.X + pw - floatGripSz, Y: panel.Y + ph - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.banWin, bgrip, panel, banBoxMinW, minH, pressed)
	a.drawResizeGrip(bgrip)
	x := panel.X + modDashIn
	maxW := pw - 2*modDashIn

	title := "Bulk Kick"
	if isBan {
		title = "Bulk Ban"
	}
	n := len(a.bulkBoxUIDs)
	c.Heading(x, panel.Y+12, title+"  —  "+strconv.Itoa(n)+" player(s)", ColText)
	if c.Button(sdl.Rect{X: panel.X + pw - modDashIn - 74, Y: panel.Y + 12, W: 74, H: btnH}, "Close") {
		a.banBoxKind = 0
		return
	}
	y := panel.Y + 44

	// Frozen target list. Capped display (modBulkCap can be 50): show the first rows, then "…and K
	// more" so the box never grows unbounded. Each line: "[uid] name" (or "(left)" if they've gone).
	const maxListRows = int32(6)
	listH := maxListRows*16 + 8
	listR := sdl.Rect{X: x, Y: y, W: maxW, H: listH}
	c.Border(listR, ColPanelHi)
	clipPrev, clipHad := c.pushClip(listR)
	ly := listR.Y + 4
	shown, more := a.bulkBoxUIDs, 0
	if int32(len(shown)) > maxListRows {
		more = len(shown) - int(maxListRows-1)
		shown = shown[:maxListRows-1]
	}
	for _, uid := range shown {
		label := "[" + uid + "] (left)"
		if row, ok := a.rosterByUID(uid); ok {
			label = "[" + uid + "] " + rosterDisplayName(row)
		}
		c.LabelClipped(listR.X+6, ly, maxW-12, label, ColTextDim)
		ly += 16
	}
	if more > 0 {
		c.LabelClipped(listR.X+6, ly, maxW-12, "…and "+strconv.Itoa(more)+" more", ColTextDim)
	}
	c.popClip(clipPrev, clipHad)
	y += listH + 12

	if isBan {
		c.Label(x, y, "Duration (applied to all):", ColTextDim)
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
				c.Border(br, ColAccent)
			}
			dx += dw + 6
		}
		y += btnH + 10
	}

	c.Label(x, y, "Reason (applied to all):", ColTextDim)
	y += 20
	a.banBoxReason, _ = c.TextField("modbulkreason", sdl.Rect{X: x, Y: y, W: maxW, H: fieldH}, a.banBoxReason, "reason (optional for kick)")
	y += fieldH + 8
	y = a.drawReasonTemplateChips(x, y, maxW, false) // compact (no manage row) in the busy bulk box

	// At-a-glance summary of the bulk action (always shown), so the mod sees how long and why.
	c.LabelClipped(x, y, maxW, banActionSummary(isBan, strconv.Itoa(n)+" player(s)", a.banBoxDur, a.banBoxReason), ColText)
	y += 22

	// Readiness: how many frozen targets have a buildable command right now (the rest need an IPID
	// this server hasn't surfaced yet). Ground truth — we just try to build each command.
	ready := 0
	for _, uid := range a.bulkBoxUIDs {
		if a.bulkCommandFor(uid, isBan) != "" {
			ready++
		}
	}
	switch {
	case !a.dashSoftwareKnown():
		c.LabelClipped(x, y, maxW, "Pick the server software first (Cancel, then Change).", ColDanger)
	case ready == 0:
		c.LabelClipped(x, y, maxW, "No targets are ready — this server needs IPIDs (mod-only). Fetch them:", ColDanger)
		if c.Button(sdl.Rect{X: x, Y: y + 22, W: 210, H: btnH}, "Fetch area info (/getarea)") {
			a.fetchAreaForBan()
		}
	default:
		msg := "Will send " + strconv.Itoa(ready) + " command(s) — one per player, paced."
		if skipped := n - ready; skipped > 0 {
			msg += "  " + strconv.Itoa(skipped) + " skipped (no IPID yet)."
		}
		c.LabelClipped(x, y, maxW, msg, ColAccent)
	}

	by := panel.Y + ph - btnH - 14
	if ready > 0 {
		if c.Button(sdl.Rect{X: x, Y: by, W: 200, H: btnH}, title+" (send "+strconv.Itoa(ready)+")") {
			a.sendBulk(isBan)
			a.banBoxKind = 0
			return
		}
	}
	if c.Button(sdl.Rect{X: x + 212, Y: by, W: 100, H: btnH}, "Cancel") {
		a.banBoxKind = 0
	}
}

// sendBulk builds and queues one paced OOC command per ready frozen target (skipping any that still
// lack an identifier), audits each individually, then clears the selection. Paced through
// queueOOCLines (the macro pipeline) so a large batch never floods the server. Returns the number
// queued (for the test / the toast).
func (a *App) sendBulk(isBan bool) int {
	action := "Kick"
	if isBan {
		action = "Ban"
	}
	cmds := make([]string, 0, len(a.bulkBoxUIDs))
	for _, uid := range a.bulkBoxUIDs {
		cmd := a.bulkCommandFor(uid, isBan)
		if cmd == "" {
			continue // not ready (no identifier yet) — never send a broken command
		}
		target := "[" + uid + "]"
		if row, ok := a.rosterByUID(uid); ok {
			target = "[" + uid + "] " + rosterDisplayName(row)
		}
		a.recordModAudit(action, target, cmd)
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return 0
	}
	a.queueOOCLines(cmds) // paced + capped (macroQueueCap)
	a.clearModSelected()
	a.bulkBoxUIDs = nil
	a.warnLine = clampLine("Queued " + strconv.Itoa(len(cmds)) + " " + action + " command(s).")
	a.warnAt = a.now()
	return len(cmds)
}
