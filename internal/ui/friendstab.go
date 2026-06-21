package ui

import (
	"fmt"

	"github.com/veandco/go-sdl2/sdl"
)

// drawFriendsTab is the dedicated Friends window the playtesters asked for — a tab
// beside Log / OOC / Notes listing THIS server's friends, each in their saved
// colour / nickname, with a PM button (pre-fills the OOC input with "/pm <name> "
// and focuses it, so you message them with the server's /pm if it supports one)
// and a Remove button. Friends are stored per-server by showname (added via the
// player list or the IC-log "+ Friend"), so no roster snapshot is needed.
func (a *App) drawFriendsTab(r sdl.Rect) {
	c := a.ctx
	if a.serverKey == "" {
		c.Label(r.X+4, r.Y+6, "Connect to a server to see its friends.", ColTextDim)
		return
	}
	names := a.d.Prefs.ServerFriends(a.serverKey)
	c.Label(r.X+4, r.Y+4, fmt.Sprintf("Friends here (%d) — add with \"+ Friend\" on a player:", len(names)), ColTextDim)
	if len(names) == 0 {
		c.Label(r.X+4, r.Y+30, "No friends yet.", ColTextDim)
		return
	}
	sat := float64(a.d.Prefs.NameColorSat()) / 100
	val := float64(a.d.Prefs.NameColorVal()) / 100
	const rowH = int32(28)
	y := r.Y + 26
	for _, name := range names {
		if y > r.Y+r.H-rowH {
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
			nameCol = readableOnPanel(fcol) // their custom colour, lifted to stay legible
		} else if a.d.Prefs.NameColorsOn() {
			nameCol = nameColor(name, sat, val)
		}
		disp := name
		if nick != "" {
			disp = nick + "  ·  " + name
		}

		// Right cluster: Remove, then PM.
		bx := r.X + r.W - 4
		rmW := c.TextWidth("Remove") + 12
		bx -= rmW
		if c.Button(sdl.Rect{X: bx, Y: y + 1, W: rmW, H: rowH - 6}, "Remove") {
			a.toggleServerFriend(name) // a friended name toggles OFF = removed
		}
		pmW := c.TextWidth("PM") + 16
		bx -= pmW + 4
		pmR := sdl.Rect{X: bx, Y: y + 1, W: pmW, H: rowH - 6}
		if c.Button(pmR, "PM") {
			a.oocInput = "/pm " + name + " "
			c.focusID = "ooc" // jump the cursor into the OOC input, message pre-filled
			a.logTab = logTabOOC
			a.warnLine = "Type your PM after /pm and press Enter (needs a server that supports /pm)."
			a.warnAt = a.now()
		}
		c.Tooltip(pmR, "Pre-fill the OOC box with /pm "+name+" — send if your server supports private messages")

		c.LabelClipped(r.X+6, y+5, bx-r.X-12, disp, nameCol)
		y += rowH
	}
}
