package ui

// Click-to-pair: a shortcut for the OOC command `/pair <UID>` that some servers
// (tsuserver family) implement as a server-side pair sync. AO's IC packets only
// carry the CHARACTER, not the player's client UID — so the UID is sourced from
// the /getarea player list (parsed passively in pushOOC), with manual entry as
// the always-available fallback. The popup is honest about what it resolved:
// the UID is pre-filled ONLY on a confident match, never a guess.

import (
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// areaPlayer is one parsed /getarea player: client UID, character name, and the
// showname (the name their IC lines display) when /getarea lists one.
type areaPlayer struct{ uid, name, showname string }

// areaPlayersCap bounds the parsed /getarea picker (a busy area's roster).
const areaPlayersCap = 200

// parseAreaLine best-effort-parses a /getarea player row "[<uid>] <name>"
// (optionally a trailing "(showname)") into the UID map + picker list. Server
// formats vary, so a miss is fine — the popup's manual UID box covers it.
// Called per incoming OOC line; allocation-light (no regexp).
func (a *App) parseAreaLine(line string) {
	s := strings.TrimSpace(line)
	// Find the first "[<digits>]". A server may prefix a "[Title]" tag before the
	// UID bracket (e.g. "[Mario Kart Queen] [24] tlaloc"), so scan PAST any
	// non-numeric brackets rather than only checking the first one.
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		rel := strings.IndexByte(s[i+1:], ']')
		if rel < 1 {
			continue
		}
		end := i + 1 + rel
		uid := s[i+1 : end]
		if _, err := strconv.Atoi(uid); err != nil {
			continue // "[Title]" etc. — keep looking for "[<digits>]"
		}
		name := strings.TrimSpace(s[end+1:])
		if name == "" {
			return
		}
		a.areaLastUID = uid // a following "Showname:" line aliases to this player
		a.addAreaPlayer(name, uid)
		return
	}
}

// addAreaPlayer records a parsed /getarea player (UID + display name) into the
// match map AND the clickable roster (one row per player, dedup by name).
func (a *App) addAreaPlayer(name, uid string) {
	if i := strings.LastIndexByte(name, '('); i > 0 { // drop a trailing "(showname)"
		name = strings.TrimSpace(name[:i])
	}
	if name == "" {
		return
	}
	if a.pairAreaReset { // a fresh /getarea (after Refresh): start the roster over
		a.areaUIDs = nil
		a.areaPlayers = a.areaPlayers[:0]
		a.pairAreaReset = false
	}
	if a.areaUIDs == nil {
		a.areaUIDs = map[string]string{}
	}
	key := strings.ToLower(name)
	if _, seen := a.areaUIDs[key]; !seen && len(a.areaPlayers) < areaPlayersCap {
		a.areaPlayers = append(a.areaPlayers, areaPlayer{uid: uid, name: name})
	}
	a.areaUIDs[key] = uid
}

// aliasAreaName maps an EXTRA name (a "Showname:" line) to a UID for matching —
// no roster row, since it's the same player as its "[uid] char" line. This is
// what lets a double-clicked IC line (which shows the SHOWNAME) auto-fill the UID.
func (a *App) aliasAreaName(name, uid string) {
	name = strings.TrimSpace(name)
	if name == "" || uid == "" || a.areaUIDs == nil {
		return
	}
	a.areaUIDs[strings.ToLower(name)] = uid
	// Record the showname on its roster row (the most recent player) so the
	// picker can show it next to the character — that's the name you recognise.
	if n := len(a.areaPlayers); n > 0 && a.areaPlayers[n-1].uid == uid && a.areaPlayers[n-1].showname == "" {
		a.areaPlayers[n-1].showname = name
	}
}

// parseAreaBlock feeds each newline-separated row of an OOC payload to the parser
// (/getarea usually arrives as one multi-line CT). Tracks the verbose format too:
// a "Showname:" line (inline "Showname: X" or the name on the next line) aliases
// to the last "[uid]" seen. Fast-rejects ordinary chat so it costs nothing.
func (a *App) parseAreaBlock(text string) {
	if !strings.ContainsRune(text, '[') && !strings.Contains(strings.ToLower(text), "showname") {
		return
	}
	wantShowname := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if wantShowname { // the line right after a bare "Showname:" is the name
			wantShowname = false
			if t != "" && a.areaLastUID != "" {
				a.aliasAreaName(t, a.areaLastUID)
				continue
			}
		}
		if strings.HasPrefix(strings.ToLower(t), "showname:") {
			if name := strings.TrimSpace(t[len("showname:"):]); name != "" {
				a.aliasAreaName(name, a.areaLastUID) // inline "Showname: X"
			} else {
				wantShowname = true // bare "Showname:" → the next line
			}
			continue
		}
		a.parseAreaLine(line) // "[uid] name" sets areaLastUID + adds the roster row
	}
}

// openPairPopup opens the click-to-pair popup targeting char, pre-filling the
// UID only when /getarea gave a confident name match (else blank — never a guess).
func (a *App) openPairPopup(char string) {
	a.pairPopupOpen = true
	a.pairPopupChar = char
	a.pairPopupUID = a.areaUIDs[strings.ToLower(strings.TrimSpace(char))]
	a.pairListScroll = 0
}

// drawPairPopup is the click-to-pair modal: the clicked target, an editable UID
// box (the reliable core) + Send, a Refresh that runs /getarea, and a clickable
// roster of parsed players (real UID per row — picking one needs no name match).
func (a *App) drawPairPopup(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 215})
	if c.escPressed {
		a.pairPopupOpen = false
		return
	}
	pw, ph := int32(470), int32(384)
	panel := sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+18, panel.Y+12, "Pair via /pair <UID>", ColText)
	if c.Button(sdl.Rect{X: panel.X + pw - 86, Y: panel.Y + 10, W: 74, H: btnH}, "Close") {
		a.pairPopupOpen = false
		return
	}
	x := panel.X + 18
	y := panel.Y + 48
	if a.pairPopupChar != "" {
		c.LabelClipped(x, y, pw-36, "Clicked: "+a.pairPopupChar, ColTextDim)
		y += 22
	}
	// Reliable core: an editable UID + Send. Only pre-filled on a confident match.
	c.Label(x, y+5, "UID:", ColTextDim)
	a.pairPopupUID, _ = c.TextField("pairuid", sdl.Rect{X: x + 40, Y: y, W: 150, H: fieldH}, a.pairPopupUID, "client id")
	uid := strings.TrimSpace(a.pairPopupUID)
	if c.Button(sdl.Rect{X: x + 200, Y: y, W: 120, H: btnH}, "Send /pair") && uid != "" {
		a.queueOOCLines([]string{"/pair " + uid})
		a.warnLine = clampLine("Sent /pair " + uid + " (pairs on servers that support it).")
		a.warnAt = a.now()
		a.pairPopupOpen = false
		return
	}
	y += fieldH + 10
	// Fetch the area roster. Servers differ on the command — some only have the
	// /ga alias, not /getarea — so offer all three; whichever your server answers
	// fills the list (the reply is parsed no matter how you ask).
	c.Label(x, y+5, "Fetch list:", ColTextDim)
	bx := x + 78
	for _, cmd := range []string{"/ga", "/gas", "/getarea"} {
		bw := c.TextWidth(cmd) + 16
		if c.Button(sdl.Rect{X: bx, Y: y, W: bw, H: btnH}, cmd) {
			a.pairAreaReset = true
			a.queueOOCLines([]string{cmd})
			a.warnLine = clampLine("Sent " + cmd + " — the roster fills from the reply.")
			a.warnAt = a.now()
		}
		bx += bw + 6
	}
	y += btnH + 10
	c.Label(x, y, "Players parsed from the area list: "+strconv.Itoa(len(a.areaPlayers)), ColTextDim)
	y += 20
	a.drawPairPlayerList(sdl.Rect{X: x, Y: y, W: pw - 36, H: panel.Y + ph - y - 14})
}

// drawPairPlayerList renders the parsed /getarea roster as a clickable list —
// each row carries the player's REAL UID, so picking one sidesteps name-matching
// entirely (the reliable path for a quiet scene partner you can't click on stage).
func (a *App) drawPairPlayerList(r sdl.Rect) {
	c := a.ctx
	if len(a.areaPlayers) == 0 {
		c.LabelClipped(r.X+2, r.Y+4, r.W-4, "No area data yet — run /ga, /gas, or hit Refresh; or type the UID above.", ColTextDim)
		return
	}
	const lineH = int32(24)
	a.pairListScroll -= c.WheelIn(r) * scrollStepPx
	contentH := int32(len(a.areaPlayers)) * lineH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.pairListScroll = c.VScrollbar("pairlist", track, a.pairListScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowY := r.Y - a.pairListScroll
	rowW := r.W - scrollBarW - 6
	for _, p := range a.areaPlayers {
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-lineH {
			row := sdl.Rect{X: r.X, Y: rowY, W: rowW, H: lineH - 3}
			if c.hovering(row) {
				c.Fill(row, ColPanelHi)
				if c.clicked {
					a.pairPopupUID = p.uid
					a.pairPopupChar = p.name
					if p.showname != "" {
						a.pairPopupChar = p.showname // recognise them by the IC name
					}
				}
			}
			label := "[" + p.uid + "]  " + p.name
			if p.showname != "" && !strings.EqualFold(p.showname, p.name) {
				label = "[" + p.uid + "]  " + p.showname + "  ·  " + p.name // showname first (the IC name)
			}
			c.LabelClipped(row.X+6, row.Y+4, row.W-12, label, ColText)
		}
		rowY += lineH
	}
}
