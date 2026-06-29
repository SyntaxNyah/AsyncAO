package ui

import (
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// The Group Chat / Messages panel — a NON-BLOCKING floating window (drag to move,
// grip to resize, Close to hide; Extras → "Group Chat") for AsyncAO-to-AsyncAO
// private messaging over the server's /pm. Left column = your conversations plus the
// AsyncAO players in the room (the "AO"-badged ones) to start a DM with; right =
// the selected partner's profile, the thread, and a compose box. It's confidential
// from normal players (server /pm only) and never spams an area, and it draws only
// while open, so it costs nothing on the render hot path. Group chats (create /
// invite / owner / kick) layer onto this next.

const (
	msgPanelDefW = 560
	msgPanelDefH = 420
	msgPanelMinW = 360
	msgPanelMinH = 240
)

func (a *App) toggleMessages() { a.showMessages = !a.showMessages }

// msgPanelRect is the panel's screen rect; first-open tucks top-left, then the
// floatWin geometry (drag / resize) wins.
func (a *App) msgPanelRect(w, h int32) sdl.Rect {
	if !a.msgWin.placed {
		dw := clampI32(msgPanelDefW, msgPanelMinW, w-2*floatWinMargin)
		dh := clampI32(msgPanelDefH, msgPanelMinH, h-2*floatWinMargin)
		return sdl.Rect{X: floatWinMargin, Y: floatTitleH, W: dw, H: dh}
	}
	return a.msgWin.rect(msgPanelDefW, msgPanelDefH, msgPanelMinW, msgPanelMinH, w, h)
}

// msgEntry is one left-column row: a group (gid != 0) or a DM (keyed by name).
type msgEntry struct {
	label string
	gid   uint32
	name  string
}

// msgEntries builds the left column: groups first (alphabetical, "# name"), then DM
// threads, then AsyncAO-detected players in the room without a thread yet ("+ name").
func (a *App) msgEntries() []msgEntry {
	var out []msgEntry
	if len(a.msgGroups) > 0 {
		gs := make([]*msgGroup, 0, len(a.msgGroups))
		for _, g := range a.msgGroups {
			gs = append(gs, g)
		}
		sort.Slice(gs, func(i, j int) bool { return strings.ToLower(gs[i].name) < strings.ToLower(gs[j].name) })
		for _, g := range gs {
			out = append(out, msgEntry{label: "# " + g.name, gid: g.id})
		}
	}
	seen := map[string]bool{}
	threads := make([]string, 0, len(a.pmThreads))
	for k := range a.pmThreads {
		threads = append(threads, k)
	}
	sort.Strings(threads)
	for _, k := range threads {
		seen[k] = true
		out = append(out, msgEntry{label: k, name: k})
	}
	if a.room != nil {
		roster := a.rosterView()
		for i := range roster {
			name := strings.TrimSpace(roster[i].name)
			lk := strings.ToLower(name)
			if name == "" || seen[lk] || !a.room.RemoteIsAsyncAO(name) {
				continue
			}
			seen[lk] = true
			out = append(out, msgEntry{label: "+ " + name, name: name})
		}
	}
	return out
}

// drawMessagesPanel paints the floating Group Chat window. pressed is the shared
// floatWin press edge from drawFloatingPanels.
func (a *App) drawMessagesPanel(w, h int32, pressed *bool) {
	c := a.ctx
	r := a.msgPanelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, "Group Chat", ColText)
	closeB := sdl.Rect{X: r.X + r.W - 70 - pad, Y: r.Y + 3, W: 70, H: btnH}
	if c.Button(closeB, "Close") {
		a.showMessages = false
		return
	}
	a.floatWinDrag(&a.msgWin, sdl.Rect{X: r.X, Y: r.Y, W: closeB.X - r.X - 4, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.msgWin, grip, r, msgPanelMinW, msgPanelMinH, pressed)
	a.drawResizeGrip(grip)

	top := r.Y + floatTitleH + 8
	bottom := r.Y + r.H - pad

	// Left column: a scrollable list of chats + a "+ New Group" button beneath it.
	const leftW = int32(170)
	c.Label(r.X+pad, top, "Chats", ColTextDim)
	listR := sdl.Rect{X: r.X + pad, Y: top + 18, W: leftW, H: bottom - (top + 18) - btnH - 6}
	entries := a.msgEntries()
	labels := make([]string, len(entries))
	sel := -1
	for i, e := range entries {
		labels[i] = e.label
		if e.gid != 0 {
			if e.gid == a.msgSelGroup {
				sel = i
			}
		} else if a.msgSelGroup == 0 && strings.EqualFold(e.name, a.msgSel) {
			sel = i
		}
	}
	if clicked := a.drawLogList("msgchats", listR, labels, sel, &a.msgListScroll); clicked >= 0 && clicked < len(entries) {
		if e := entries[clicked]; e.gid != 0 {
			a.msgSelGroup, a.msgSel, a.msgGroupManage = e.gid, "", false
		} else {
			a.msgSel, a.msgSelGroup = e.name, 0
		}
	}
	if c.Button(sdl.Rect{X: listR.X, Y: bottom - btnH, W: leftW, H: btnH}, "+ New Group") {
		a.createGroup("New group")
	}

	// Right column: group view, DM view, or a hint.
	rx := listR.X + leftW + 10
	rw := r.X + r.W - pad - rx
	if rw < 140 {
		rw = 140
	}
	switch {
	case a.msgSelGroup != 0:
		a.drawGroupView(rx, top, rw, bottom)
	case a.msgSel != "":
		a.drawDMView(rx, top, rw, bottom)
	default:
		c.LabelClipped(rx, top, rw, "Pick an AsyncAO player (AO badge) to DM, or + New Group to start a group chat.", ColTextDim)
	}
}

// drawDMView renders the selected 1:1 conversation: profile card, thread, compose.
func (a *App) drawDMView(rx, top, rw, bottom int32) {
	c := a.ctx
	hy := top
	c.LabelClipped(rx, hy, rw, a.msgSel, ColAccent)
	hy += 18
	if a.room != nil {
		if p, ok := a.room.RemoteProfile(a.msgSel); ok {
			if line := strings.TrimSpace(p.Pronouns + "   " + p.Tag); line != "" {
				c.LabelClipped(rx, hy, rw, line, ColTextDim)
				hy += 16
			}
		}
	}
	composeY := bottom - fieldH
	sendW := c.TextWidth("Send") + 16
	var send bool
	a.msgInput, send = c.TextField("msgcompose", sdl.Rect{X: rx, Y: composeY, W: rw - sendW - 6, H: fieldH}, a.msgInput, "message…")
	if c.Button(sdl.Rect{X: rx + rw - sendW, Y: composeY, W: sendW, H: fieldH}, "Send") || send {
		a.sendDirectMessage(a.msgSel, a.msgInput)
		a.msgInput = ""
	}
	a.drawMsgThreadBody(sdl.Rect{X: rx, Y: hy + 2, W: rw, H: composeY - (hy + 2) - 6})
}

// drawGroupView renders the selected group: header + Members / Leave, then either the
// members/invite manager or the chat (thread + compose).
func (a *App) drawGroupView(rx, top, rw, bottom int32) {
	c := a.ctx
	g := a.msgGroups[a.msgSelGroup]
	if g == nil {
		a.msgSelGroup = 0
		return
	}
	c.LabelClipped(rx, top, rw-170, "# "+g.name, ColAccent)
	if c.Button(sdl.Rect{X: rx + rw - 162, Y: top - 2, W: 86, H: btnH}, "Members") {
		a.msgGroupManage = !a.msgGroupManage
	}
	if c.Button(sdl.Rect{X: rx + rw - 70, Y: top - 2, W: 70, H: btnH}, "Leave") {
		a.leaveGroup(g)
		return
	}
	hy := top + 22
	if a.msgGroupManage {
		a.drawGroupManage(g, sdl.Rect{X: rx, Y: hy, W: rw, H: bottom - hy})
		return
	}
	composeY := bottom - fieldH
	sendW := c.TextWidth("Send") + 16
	var send bool
	a.msgInput, send = c.TextField("msgcompose", sdl.Rect{X: rx, Y: composeY, W: rw - sendW - 6, H: fieldH}, a.msgInput, "message the group…")
	if c.Button(sdl.Rect{X: rx + rw - sendW, Y: composeY, W: sendW, H: fieldH}, "Send") || send {
		a.sendGroupText(g, a.msgInput)
		a.msgInput = ""
	}
	a.drawGroupThread(g, sdl.Rect{X: rx, Y: hy, W: rw, H: composeY - hy - 6})
}

// drawGroupManage lists members (with a Kick for the owner) and the AsyncAO players
// in the room you can invite. Top half = members, bottom half = invite.
func (a *App) drawGroupManage(g *msgGroup, box sdl.Rect) {
	c := a.ctx
	owner := g.ownerUID == a.myUID()
	const rowH = int32(20)
	y := box.Y
	c.Label(box.X, y, "Members:", ColTextDim)
	y += 18
	for _, m := range g.members {
		if y > box.Y+box.H/2-rowH {
			break
		}
		label := m.name
		if m.uid == g.ownerUID {
			label += "  (owner)"
		}
		if m.uid == a.myUID() {
			label += "  (you)"
		}
		c.LabelClipped(box.X+8, y+2, box.W-90, label, ColText)
		if owner && m.uid != a.myUID() {
			if c.Button(sdl.Rect{X: box.X + box.W - 68, Y: y, W: 62, H: rowH - 2}, "Kick") {
				a.kickMember(g, m.uid)
			}
		}
		y += rowH
	}
	y = box.Y + box.H/2
	c.Label(box.X, y, "Invite AsyncAO players here:", ColTextDim)
	y += 18
	if a.room == nil {
		return
	}
	roster := a.rosterView()
	for i := range roster {
		if y > box.Y+box.H-rowH {
			break
		}
		name := strings.TrimSpace(roster[i].name)
		uid, _ := strconv.Atoi(roster[i].uid)
		if name == "" || uid == 0 || g.hasMember(uid) || !a.room.RemoteIsAsyncAO(name) {
			continue
		}
		c.LabelClipped(box.X+8, y+2, box.W-90, name, ColText)
		if c.Button(sdl.Rect{X: box.X + box.W - 80, Y: y, W: 74, H: rowH - 2}, "Invite") {
			a.inviteToGroup(g, uid, name)
		}
		y += rowH
	}
}

// drawGroupThread renders a group's messages, newest at the bottom.
func (a *App) drawGroupThread(g *msgGroup, box sdl.Rect) {
	if box.H < 24 {
		return
	}
	c := a.ctx
	c.Border(box, ColPanelHi)
	if len(g.lines) == 0 {
		c.LabelClipped(box.X+6, box.Y+6, box.W-12, "No messages yet — say hi below.", ColTextDim)
		return
	}
	const lh = int32(18)
	clipPrev, clipHad := c.pushClip(box)
	defer c.popClip(clipPrev, clipHad)
	rows := int((box.H - 8) / lh)
	if rows < 1 {
		rows = 1
	}
	start := 0
	if len(g.lines) > rows {
		start = len(g.lines) - rows
	}
	y := box.Y + 4
	for _, ln := range g.lines[start:] {
		col, who := ColText, ln.from+": "
		if ln.fromMe {
			col, who = ColAccent, "You: "
		}
		c.LabelClipped(box.X+6, y, box.W-12, who+ln.text, col)
		y += lh
	}
}

// drawMsgThreadBody renders the selected DM thread, newest at the bottom.
func (a *App) drawMsgThreadBody(box sdl.Rect) {
	if box.H < 24 {
		return
	}
	c := a.ctx
	c.Border(box, ColPanelHi)
	lines := a.pmThreads[strings.ToLower(strings.TrimSpace(a.msgSel))]
	if len(lines) == 0 {
		c.LabelClipped(box.X+6, box.Y+6, box.W-12, "No messages yet — say hi below.", ColTextDim)
		return
	}
	const lh = int32(18)
	clipPrev, clipHad := c.pushClip(box)
	defer c.popClip(clipPrev, clipHad)
	rows := int((box.H - 8) / lh)
	if rows < 1 {
		rows = 1
	}
	start := 0
	if len(lines) > rows {
		start = len(lines) - rows // anchor to the newest, like a chat
	}
	y := box.Y + 4
	for _, ln := range lines[start:] {
		col, who := ColText, a.msgSel+": "
		if ln.fromMe {
			col, who = ColAccent, "You: "
		}
		c.LabelClipped(box.X+6, y, box.W-12, who+ln.text, col)
		y += lh
	}
}

// sendDirectMessage sends text to one partner over the server's /pm, tagging it with
// an AsyncAO DM control frame (stripped on the receiver's display) and mirroring it
// into the local thread. Resolves the partner's live UID from the roster; falls back
// to the name when there's no live id (some servers accept it).
func (a *App) sendDirectMessage(target, text string) {
	text = strings.TrimSpace(text)
	if text == "" || target == "" || a.sess == nil {
		return
	}
	id := a.friendUID(target)
	if id == "" {
		id = target
	}
	body := text + courtroom.WireMessage{Kind: courtroom.MsgDM}.EncodeMarker()
	if uid, err := strconv.Atoi(id); err == nil {
		a.sess.SendOOC(a.oocNameOrDefault(), courtroom.PMCommand([]int{uid}, body))
	} else {
		a.sess.SendOOC(a.oocNameOrDefault(), "/pm "+id+" "+body)
	}
	a.pmAppend(target, true, text)
}
