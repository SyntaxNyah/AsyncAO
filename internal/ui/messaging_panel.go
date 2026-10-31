package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The Group Chat / Messages panel: a non-blocking floating window (Extras > Group Chat) for AsyncAO-to-AsyncAO private
// messages over the server's /pm. The left column lists conversations and AsyncAO players in the room; the right shows
// the partner's profile, the thread and a compose box. It draws only while open.

const (
	msgPanelDefW = 560
	msgPanelDefH = 420
	msgPanelMinW = 360
	msgPanelMinH = 240
)

func (a *App) toggleMessages() { a.showMessages = !a.showMessages }

// seedPanelFromSlot is the first-open seed shared by every persistable floatWin panel. If the layout editor positioned
// the panel (classicOv[slot]) and it hasn't been dragged this session, it adopts that rect via anchoredRect and
// returns (rect, true); otherwise (zero, false) and the caller keeps its own default. panelSlotTable lists the panels.
func (a *App) seedPanelFromSlot(fw *floatWin, slot string, defW, defH, minW, minH, w, h int32) (sdl.Rect, bool) {
	if fw.placed {
		return sdl.Rect{}, false
	}
	ov, ok := a.classicOv[slot]
	if !ok {
		return sdl.Rect{}, false
	}
	r := a.anchoredRect(slot, ov, w, h) // positioned via Edit Layout → start there
	fw.x, fw.y, fw.w, fw.h, fw.placed = r.X, r.Y, r.W, r.H, true
	return fw.rect(defW, defH, minW, minH, w, h), true
}

// persistPanelSlot writes a floatWin panel's current rect r (already computed this frame) to its classic slot, in
// memory and through the debounced saver, so a drag/resize survives relaunch. Call it only on the drag/resize-end
// frame.
func (a *App) persistPanelSlot(slot string, r sdl.Rect, w, h int32) {
	frac := rectToFrac(r, w, h)
	if a.classicOv == nil {
		a.classicOv = make(map[string][4]float64, classicSlotRegCap)
	}
	a.classicOv[slot] = frac
	a.syncAnchorWindow(slot, w, h) // a pinned slot's override now describes THIS window
	a.d.Prefs.SetClassicSlot(slot, frac)
	if m := a.slotAnchorMode(slot); m != "" {
		a.d.Prefs.SetClassicAnchor(slot, config.ClassicAnchor{Mode: m, WinW: int(w), WinH: int(h)})
	}
}

// msgPanelRect is the panel's screen rect: seeded from the persisted layout slot on first open, else tucked top-left;
// after that the live floatWin geometry wins.
func (a *App) msgPanelRect(w, h int32) sdl.Rect {
	if r, ok := a.seedPanelFromSlot(&a.msgWin, slotMessages, msgPanelDefW, msgPanelDefH, msgPanelMinW, msgPanelMinH, w, h); ok {
		return r
	}
	if !a.msgWin.placed { // historical default: tuck top-left (not centred)
		dw := value.Clamp(msgPanelDefW, msgPanelMinW, w-2*floatWinMargin)
		dh := value.Clamp(msgPanelDefH, msgPanelMinH, h-2*floatWinMargin)
		return sdl.Rect{X: floatWinMargin, Y: floatTitleH, W: dw, H: dh}
	}
	return a.msgWin.rect(msgPanelDefW, msgPanelDefH, msgPanelMinW, msgPanelMinH, w, h)
}

// msgSlotDefaultRect is where the Group Chat slot lands when first added from the layout editor: centred, clamped.
func (a *App) msgSlotDefaultRect(w, h int32) sdl.Rect {
	dw := value.Clamp(msgPanelDefW, msgPanelMinW, w-2*floatWinMargin)
	dh := value.Clamp(msgPanelDefH, msgPanelMinH, h-2*floatWinMargin)
	return sdl.Rect{X: (w - dw) / 2, Y: (h - dh) / 2, W: dw, H: dh}
}

// persistMsgSlot writes the panel's rect back to its layout slot on the drag/resize-end frame.
func (a *App) persistMsgSlot(w, h int32) {
	a.persistPanelSlot(slotMessages, a.msgWin.rect(msgPanelDefW, msgPanelDefH, msgPanelMinW, msgPanelMinH, w, h), w, h)
}

// drawMessagesSlotGhost draws an inert placeholder while the layout editor is open, registered via slotRect so it gets
// move/resize handles. Only when the slot exists.
func (a *App) drawMessagesSlotGhost(w, h int32) {
	if _, ok := a.classicOv[slotMessages]; !ok {
		return
	}
	c := a.ctx
	r := a.slotRect(slotMessages, a.msgSlotDefaultRect(w, h), w, h)
	if r.W < classicMinPx || r.H < classicMinPx {
		return
	}
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Label(r.X+pad, r.Y+6, "Group Chat", ColText)
	c.LabelClipped(r.X+pad, r.Y+floatTitleH+8, r.W-2*pad, "(opens here when you click Extras → Group Chat)", ColTextDim)
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
			out = append(out, msgEntry{label: groupUnreadLabel(g.name, g.unread), gid: g.id})
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

// drawMessagesPanel paints the floating Group Chat window; pressed is the shared floatWin press edge.
func (a *App) drawMessagesPanel(w, h int32, pressed *bool) {
	c := a.ctx
	wasActive := a.msgWin.dragging || a.msgWin.resizing // detect the drag/resize-end frame for slot persistence
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
	if wasActive && !a.msgWin.dragging && !a.msgWin.resizing { // drag/resize just ended → remember where
		a.persistMsgSlot(w, h)
	}

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
			if g := a.msgGroups[e.gid]; g != nil {
				g.unread = 0 // opening the group reads it
			}
		} else {
			a.msgSel, a.msgSelGroup = e.name, 0
		}
	}
	if c.Button(sdl.Rect{X: listR.X, Y: bottom - btnH, W: leftW, H: btnH}, "+ New Group") {
		a.createGroup("New group")
		a.msgGroupManage = true // jump straight to the members / invite picker
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
	partnerChar := a.rosterCharByName(a.msgSel)
	a.drawMsgCharIcon(partnerChar, a.msgSel, sdl.Rect{X: rx, Y: hy - 2, W: 22, H: 22})
	a.labelName(rx+28, hy, rw-28, a.msgSel, nameColor(a.msgSel, msgNameSat, msgNameVal)) // CJK-safe DM peer
	hy += 22
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
	g.unread = 0 // on screen = read (the chat-list badge clears)
	chip := groupChipColor(g.id)
	c.Fill(sdl.Rect{X: rx, Y: top + 1, W: 10, H: 14}, chip)
	head := "# " + g.name + "  ·  " + strconv.Itoa(len(g.members)) + " members"
	if g.ownerUID == a.myUID() {
		head += "  ★"
	}
	c.LabelClipped(rx+14, top, rw-184, head, ColAccent)
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
		// Char icon from the live roster (offline members keep the initial disc) and the member's stable name colour.
		a.drawMsgCharIcon(a.rosterCharByUID(m.uid), m.name, sdl.Rect{X: box.X + 6, Y: y, W: rowH - 4, H: rowH - 4})
		label := m.name
		if m.uid == g.ownerUID {
			label += "  ★ owner"
		}
		if m.uid == a.myUID() {
			label += "  (you)"
		}
		a.labelName(box.X+8+rowH, y+2, box.W-98-rowH, label, nameColor(m.name, msgNameSat, msgNameVal)) // CJK-safe member
		if owner && m.uid != a.myUID() {
			if c.Button(sdl.Rect{X: box.X + box.W - 68, Y: y, W: 62, H: rowH - 2}, "Kick") {
				a.kickMember(g, m.uid)
			}
		}
		y += rowH
	}
	y = box.Y + box.H/2
	c.Label(box.X, y, "Invite players in this area:", ColTextDim)
	y += 16
	c.LabelClipped(box.X, y, box.W-8, "Green = on AsyncAO (gets the full group chat). Others can still be invited — they get a one-off PM.", ColTierYellow)
	y += 18
	if a.room == nil {
		return
	}
	// Show everyone in the room: AsyncAO detection needs a marked IC message, so it usually showed nobody.
	// Inviting rides /pm; AsyncAO clients get the rich invite, others a readable PM.
	roster := a.rosterView()
	me := a.myUID()
	shown := 0
	for i := range roster {
		if y > box.Y+box.H-rowH {
			break
		}
		p := &roster[i]
		name := strings.TrimSpace(p.name)
		uid, _ := strconv.Atoi(p.uid)
		if name == "" || p.uid == "" || uid == me || g.hasMember(uid) {
			continue
		}
		isAO := a.room.RemoteIsAsyncAO(name)
		// AsyncAO users get the full group chat, so their row is green; others get a one-off PM.
		if isAO {
			c.Fill(sdl.Rect{X: box.X, Y: y, W: box.W, H: rowH - 2}, sdl.Color{R: ColTierGreen.R, G: ColTierGreen.G, B: ColTierGreen.B, A: 46})
		}
		tx := box.X + 8
		if isAO { // blue "AO" chip, same as the player list
			tx += a.drawRosterChip(tx, y+2, "AO", ColAccent, ColBackground) + 5
		}
		disp := strings.TrimSpace(p.showname)
		if disp == "" {
			disp = name
		}
		if oc := strings.TrimSpace(p.ooc); oc != "" && !strings.EqualFold(oc, disp) {
			disp += "   ·   OOC: " + oc
		}
		a.labelName(tx, y+2, box.X+box.W-80-tx, disp, ColText) // CJK-safe invite name
		if c.Button(sdl.Rect{X: box.X + box.W - 80, Y: y, W: 74, H: rowH - 2}, "Invite") {
			a.inviteToGroup(g, uid, name)
		}
		y += rowH
		shown++
	}
	if shown == 0 {
		c.LabelClipped(box.X+8, y+2, box.W-12, "No other players in this area. Open the player list to see who's around.", ColTextDim)
	}
}

// drawGroupThread renders a group's messages as chat bubbles, newest at the bottom.
func (a *App) drawGroupThread(g *msgGroup, box sdl.Rect) {
	if box.H < 24 {
		return
	}
	c := a.ctx
	c.Border(box, ColPanelHi)
	a.drawChatBubbles(box, len(g.lines), func(i int) (string, string, bool, string, time.Time) {
		ln := &g.lines[i]
		from := ln.from
		if ln.fromMe {
			from = "You"
		}
		return from, ln.text, ln.fromMe, ln.char, ln.at
	})
}

// drawMsgThreadBody renders the selected DM thread as chat bubbles, newest at
// the bottom. The partner's char icon resolves once from the live roster.
func (a *App) drawMsgThreadBody(box sdl.Rect) {
	if box.H < 24 {
		return
	}
	c := a.ctx
	c.Border(box, ColPanelHi)
	lines := a.pmThreads[strings.ToLower(strings.TrimSpace(a.msgSel))]
	partnerChar := a.rosterCharByName(a.msgSel)
	a.drawChatBubbles(box, len(lines), func(i int) (string, string, bool, string, time.Time) {
		ln := &lines[i]
		if ln.fromMe {
			return "You", ln.text, true, "", ln.at
		}
		return a.msgSel, ln.text, false, partnerChar, ln.at
	})
}

// sendDirectMessage sends text over the server's /pm with an AsyncAO DM control frame (stripped on display) and mirrors
// it into the local thread. The partner's live UID comes from the roster; without one it falls back to the name.
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
