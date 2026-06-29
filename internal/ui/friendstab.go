package ui

import (
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
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
	c.Label(r.X+4, r.Y+4, fmt.Sprintf("Friends here (%d) — add with \"+ Friend\" on a player:", len(names)), ColTextDim)

	// No friend picked: the list owns the whole tab.
	if a.pmTarget == "" {
		if len(names) == 0 {
			c.Label(r.X+4, r.Y+30, "No friends yet.", ColTextDim)
		} else {
			a.drawFriendRows(r, r.H, names)
		}
		return
	}

	// A friend is picked: split into a compact list (to switch between friends), the
	// PM thread (the DM view), and the composer pinned at the bottom.
	composerH := fieldH + 22
	listH := (r.H - composerH) / 3
	if listH < 64 {
		listH = 64
	}
	threadH := r.H - composerH - listH
	if len(names) == 0 {
		c.Label(r.X+4, r.Y+30, "No friends yet.", ColTextDim)
	} else {
		a.drawFriendRows(r, listH, names)
	}
	a.drawPMThread(sdl.Rect{X: r.X, Y: r.Y + listH, W: r.W, H: threadH})
	a.drawPMComposer(sdl.Rect{X: r.X, Y: r.Y + listH + threadH, W: r.W, H: composerH})
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
	a.pmAppend(a.pmTarget, true, msg) // mirror into the DM thread (it also shows in OOC)
	a.pmInput = ""
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

// pmLine is one line of a PM conversation: who said it and the text. A tiny
// convenience mirror of the OOC log, not a store of record.
type pmLine struct {
	fromMe bool
	text   string
}

const (
	pmThreadLineCap = 100 // per-conversation history cap
	pmThreadCap     = 32  // distinct conversations kept (bounds the map)
)

// pmAppend records one PM line under the partner's canonical thread key, bounded
// per-thread and in the number of threads.
func (a *App) pmAppend(partner string, fromMe bool, text string) {
	text = strings.TrimSpace(text)
	key := a.pmThreadKey(partner)
	if key == "" || text == "" {
		return
	}
	if a.pmThreads == nil {
		a.pmThreads = map[string][]pmLine{}
	}
	if _, ok := a.pmThreads[key]; !ok && len(a.pmThreads) >= pmThreadCap {
		return // already at the conversation cap — drop the new partner, stay bounded
	}
	cur := append(a.pmThreads[key], pmLine{fromMe: fromMe, text: text})
	if len(cur) > pmThreadLineCap { // keep the newest; fresh backing so the old head frees
		cur = append([]pmLine(nil), cur[len(cur)-pmThreadLineCap:]...)
	}
	a.pmThreads[key] = cur
}

// pmThreadKey canonicalises a PM partner name to a thread key so incoming and
// outgoing share ONE conversation. Matches a known friend by their stored name,
// or via the live roster's showname / char / OOC name (a server's "PM from <X>"
// may use a different identity field than the one we filed the friend under);
// otherwise the lowercased name itself.
func (a *App) pmThreadKey(name string) string {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return ""
	}
	friends := a.d.Prefs.ServerFriends(a.serverKey)
	for _, f := range friends {
		if strings.ToLower(f) == want {
			return want
		}
	}
	roster := a.rosterView()
	for i := range roster {
		p := &roster[i]
		if strings.ToLower(p.showname) != want && strings.ToLower(p.name) != want && strings.ToLower(p.ooc) != want {
			continue
		}
		for _, f := range friends {
			if strings.EqualFold(f, p.showname) || strings.EqualFold(f, p.name) {
				return strings.ToLower(f)
			}
		}
	}
	return want
}

// detectIncomingPM best-effort parses a received PM out of an OOC line and mirrors
// it into the Friends-tab thread (the line ALSO stays in the OOC log, so a miss
// loses nothing). Matches the common "PM from <sender>: <message>" shape, dropping
// a trailing "(...)" annotation from the sender. Server formats vary across
// tsuserver forks, so this is deliberately tolerant + easy to extend once a real
// line is confirmed. Outgoing PMs are recorded in sendPM, and a server's own
// "PM sent to ..." echo never matches "PM from", so nothing double-records.
func (a *App) detectIncomingPM(text string) {
	if sender, msg, ok := parseIncomingPM(text); ok {
		a.pmAppend(sender, false, msg)
	}
}

// routeIncomingPM files a received private message into its DM thread. This is the
// ROBUST path for servers that attribute the sender in the CT NAME field — Nyathena
// / Athena deliver a PM as a server message whose name is "[PM] [UID n] <name>"
// (courtroom.ParsePMSender), which the text-shape detectIncomingPM can't see. Any
// AsyncAO control frame in the body is stripped before it's shown. Group routing
// layers onto this later.
func (a *App) routeIncomingPM(sender, body string) {
	_, clean, _ := courtroom.DecodeMessageFrame(body) // drop a hidden control frame if present
	a.pmAppend(sender, false, clean)
}

// parseIncomingPM extracts (sender, message) from a received-PM OOC line, or
// ok=false when it isn't the recognised "PM from <sender>: <message>" shape (a
// trailing "(CID 5)" / "(in Area)" annotation on the sender is dropped). Pure, so
// the format match is unit-tested — and easy to adjust — without a live App.
func parseIncomingPM(text string) (sender, msg string, ok bool) {
	const prefix = "pm from "
	t := strings.TrimSpace(text)
	if len(t) < len(prefix) || !strings.EqualFold(t[:len(prefix)], prefix) {
		return "", "", false
	}
	rest := t[len(prefix):]
	idx := strings.Index(rest, ": ")
	if idx <= 0 {
		return "", "", false
	}
	sender, msg = rest[:idx], rest[idx+2:]
	if p := strings.Index(sender, " ("); p > 0 {
		sender = sender[:p]
	}
	sender, msg = strings.TrimSpace(sender), strings.TrimSpace(msg)
	if sender == "" || msg == "" {
		return "", "", false
	}
	return sender, msg, true
}

// drawPMThread renders the selected friend's PM history as a little DM view (your
// lines in accent, theirs plain), newest at the bottom. A convenience mirror of
// the OOC log; incoming lines are best-effort parsed there.
func (a *App) drawPMThread(box sdl.Rect) {
	c := a.ctx
	c.Fill(box, ColPanel)
	c.Border(box, ColPanelHi)
	lines := a.pmThreads[strings.ToLower(strings.TrimSpace(a.pmTarget))]
	if len(lines) == 0 {
		c.Label(box.X+6, box.Y+6, "No messages yet — type below to start.", ColTextDim)
		return
	}
	const lh = int32(18)
	rows := int((box.H - 6) / lh)
	if rows < 1 {
		rows = 1
	}
	start := 0
	if len(lines) > rows {
		start = len(lines) - rows // anchor to the newest, like a chat
	}
	y := box.Y + 4
	for _, ln := range lines[start:] {
		who, col := a.pmTarget, ColText
		if ln.fromMe {
			who, col = "You", ColAccent
		}
		c.LabelClipped(box.X+6, y, box.W-12, who+": "+ln.text, col)
		y += lh
	}
}
