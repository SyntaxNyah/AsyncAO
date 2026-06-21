package ui

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// drawFriendsTab is the dedicated Friends window the playtesters asked for — a tab
// beside Log / OOC / Notes listing THIS server's friends (each in their saved
// colour / nickname), with Remove and a click-to-PM. Picking PM selects the
// friend into a little composer at the bottom of the tab: type a message, Send,
// and it goes out as "/pm <their id> <message>" over OOC (the only private channel
// AO has; the reply comes back in the OOC log). Friends are stored per-server by
// showname (added via the player list / IC-log "+ Friend"), so no new storage.
func (a *App) drawFriendsTab(r sdl.Rect) {
	c := a.ctx
	if a.serverKey == "" {
		c.Label(r.X+4, r.Y+6, "Connect to a server to see its friends.", ColTextDim)
		return
	}
	names := a.d.Prefs.ServerFriends(a.serverKey)

	// Reserve a bottom strip for the PM composer once a friend is picked.
	listH := r.H
	composerH := int32(0)
	if a.pmTarget != "" {
		composerH = fieldH + 22
		listH = r.H - composerH
	}

	c.Label(r.X+4, r.Y+4, fmt.Sprintf("Friends here (%d) — add with \"+ Friend\" on a player:", len(names)), ColTextDim)
	if len(names) == 0 {
		c.Label(r.X+4, r.Y+30, "No friends yet.", ColTextDim)
	} else {
		a.drawFriendRows(r, listH, names)
	}

	if a.pmTarget != "" {
		a.drawPMComposer(sdl.Rect{X: r.X, Y: r.Y + listH, W: r.W, H: composerH})
	}
}

// drawFriendRows lists the friends within the top listH of r.
func (a *App) drawFriendRows(r sdl.Rect, listH int32, names []string) {
	c := a.ctx
	sat := float64(a.d.Prefs.NameColorSat()) / 100
	val := float64(a.d.Prefs.NameColorVal()) / 100
	const rowH = int32(28)
	y := r.Y + 26
	for _, name := range names {
		if y > r.Y+listH-rowH {
			c.Label(r.X+6, y+4, "… widen / heighten the panel to see the rest.", ColTextDim)
			break
		}
		row := sdl.Rect{X: r.X, Y: y, W: r.W, H: rowH - 2}
		if c.hovering(row) {
			c.Fill(row, ColPanel)
		}
		_, fcol, nick := a.d.Prefs.ServerFriendInfo(a.serverKey, name)
		nameCol := ColText
		if fcol >= 0 {
			nameCol = readableOnPanel(fcol)
		} else if a.d.Prefs.NameColorsOn() {
			nameCol = nameColor(name, sat, val)
		}
		disp := name
		if nick != "" {
			disp = nick + "  ·  " + name
		}

		bx := r.X + r.W - 4
		rmW := c.TextWidth("Remove") + 12
		bx -= rmW
		if c.Button(sdl.Rect{X: bx, Y: y + 1, W: rmW, H: rowH - 6}, "Remove") {
			a.toggleServerFriend(name) // a friended name toggles OFF = removed
			if strings.EqualFold(a.pmTarget, name) {
				a.pmTarget, a.pmInput = "", ""
			}
		}
		pmW := c.TextWidth("PM") + 16
		bx -= pmW + 4
		pmR := sdl.Rect{X: bx, Y: y + 1, W: pmW, H: rowH - 6}
		if c.Button(pmR, "PM") {
			if !strings.EqualFold(a.pmTarget, name) {
				a.pmInput = "" // new conversation, fresh box
			}
			a.pmTarget = name
			c.focusID = "pmmsg"
		}
		if strings.EqualFold(a.pmTarget, name) {
			c.Border(pmR, ColAccent) // active-conversation cue
		}
		c.LabelClipped(r.X+6, y+5, bx-r.X-12, disp, nameCol)
		y += rowH
	}
}

// drawPMComposer is the "lil menu you can type on": a one-line message box that
// sends /pm <their id> over OOC. Drawn at the bottom of the Friends tab once a
// friend is selected.
func (a *App) drawPMComposer(box sdl.Rect) {
	c := a.ctx
	c.Fill(box, ColPanelHi)
	c.LabelClipped(box.X+4, box.Y+3, box.W-30, "PM "+a.pmTarget+":", ColAccent)
	if c.Button(sdl.Rect{X: box.X + box.W - 22, Y: box.Y + 2, W: 18, H: 16}, "x") {
		a.pmTarget, a.pmInput = "", ""
		return
	}
	fy := box.Y + 20
	sendW := c.TextWidth("Send") + 16
	var send bool
	a.pmInput, send = c.TextField("pmmsg", sdl.Rect{X: box.X + 4, Y: fy, W: box.W - sendW - 14, H: fieldH}, a.pmInput, "private message…")
	if c.Button(sdl.Rect{X: box.X + box.W - sendW - 4, Y: fy, W: sendW, H: fieldH}, "Send") || send {
		a.sendPM()
	}
}

// sendPM fires the composed message as "/pm <id> <message>" over OOC, resolving
// the friend's current client id from the roster (server /pm wants the id; falls
// back to the name when we don't have a live id). The reply lands in the OOC log.
func (a *App) sendPM() {
	msg := strings.TrimSpace(a.pmInput)
	if msg == "" || a.pmTarget == "" || a.sess == nil {
		return
	}
	target := a.friendUID(a.pmTarget)
	if target == "" {
		target = a.pmTarget // no live id — try the name (some servers accept it)
	}
	a.sess.SendOOC(a.oocNameOrDefault(), "/pm "+target+" "+msg)
	a.pmInput = ""
	a.logTab = logTabOOC // surface the OOC log so the reply is visible
}

// friendUID looks up a friend's current client UID by matching their saved name
// against the live roster (showname or character). "" when they're not currently
// visible to us (no /getarea snapshot / not in our area).
func (a *App) friendUID(name string) string {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, p := range a.rosterView() {
		if strings.ToLower(p.showname) == want || strings.ToLower(p.name) == want {
			return p.uid
		}
	}
	return ""
}
