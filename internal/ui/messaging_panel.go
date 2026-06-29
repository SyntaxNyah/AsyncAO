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

// msgConversations builds the left-column entries: existing DM threads first
// (alphabetical), then AsyncAO-detected players in the room you don't yet have a
// thread with (prefixed "+"). Returns display labels and the parallel partner names.
func (a *App) msgConversations() (labels, names []string) {
	seen := map[string]bool{}
	threads := make([]string, 0, len(a.pmThreads))
	for k := range a.pmThreads {
		threads = append(threads, k)
	}
	sort.Strings(threads)
	for _, k := range threads {
		seen[k] = true
		names = append(names, k)
		labels = append(labels, k)
	}
	if a.room == nil {
		return labels, names
	}
	roster := a.rosterView()
	for i := range roster {
		name := strings.TrimSpace(roster[i].name)
		lk := strings.ToLower(name)
		if name == "" || seen[lk] || !a.room.RemoteIsAsyncAO(name) {
			continue
		}
		seen[lk] = true
		names = append(names, name)
		labels = append(labels, "+ "+name)
	}
	return labels, names
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

	// Left: conversations / AsyncAO users to start a chat with.
	const leftW = int32(170)
	c.Label(r.X+pad, top, "Chats", ColTextDim)
	listR := sdl.Rect{X: r.X + pad, Y: top + 18, W: leftW, H: bottom - (top + 18)}
	labels, names := a.msgConversations()
	sel := -1
	for i, n := range names {
		if strings.EqualFold(n, a.msgSel) {
			sel = i
			break
		}
	}
	if clicked := a.drawLogList("msgchats", listR, labels, sel, &a.msgListScroll); clicked >= 0 && clicked < len(names) {
		a.msgSel = names[clicked]
	}

	// Right: profile header + thread + compose.
	rx := listR.X + leftW + 10
	rw := r.X + r.W - pad - rx
	if rw < 140 {
		rw = 140
	}
	if a.msgSel == "" {
		c.LabelClipped(rx, top, rw, "Pick an AsyncAO player (AO badge) on the left to start a private chat.", ColTextDim)
		return
	}
	// Profile card: name + pronouns / tagline when the partner has transmitted one.
	hy := top
	c.LabelClipped(rx, hy, rw, a.msgSel, ColAccent)
	hy += 18
	if p, ok := a.room.RemoteProfile(a.msgSel); ok {
		if line := strings.TrimSpace(p.Pronouns + "   " + p.Tag); line != "" {
			c.LabelClipped(rx, hy, rw, line, ColTextDim)
			hy += 16
		}
	}
	// Compose row pinned to the bottom.
	composeY := bottom - fieldH
	sendW := c.TextWidth("Send") + 16
	var send bool
	a.msgInput, send = c.TextField("msgcompose", sdl.Rect{X: rx, Y: composeY, W: rw - sendW - 6, H: fieldH}, a.msgInput, "message…")
	if c.Button(sdl.Rect{X: rx + rw - sendW, Y: composeY, W: sendW, H: fieldH}, "Send") || send {
		a.sendDirectMessage(a.msgSel, a.msgInput)
		a.msgInput = ""
	}
	// Thread fills the space between the header and the compose row.
	a.drawMsgThreadBody(sdl.Rect{X: rx, Y: hy + 2, W: rw, H: composeY - (hy + 2) - 6})
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
