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

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/veandco/go-sdl2/sdl"
)

// pairPartnersCap bounds the per-tab pair-tracking map (#20; hard rule #4: no unbounded maps).
const pairPartnersCap = 256

// notePairPartner records (or clears) a speaker's current pair partner from an IC message, for the
// opt-in player-list pair chip (#20). A paired message stores "char → partner char"; a solo message
// drops the entry, so the map always reflects each player's pairing as of their latest line. Keyed
// by lowercased character (the MS wire identity — the same spoofable key friends/ignore use).
func (a *App) notePairPartner(m *protocol.ChatMessage) {
	if m == nil {
		return
	}
	char := strings.ToLower(strings.TrimSpace(m.CharName))
	if char == "" {
		return
	}
	if m.Pair.Active() {
		if a.pairPartners == nil {
			a.pairPartners = make(map[string]string)
		}
		if _, ok := a.pairPartners[char]; !ok && len(a.pairPartners) >= pairPartnersCap {
			return // bounded: don't grow past the cap with new speakers
		}
		a.pairPartners[char] = strings.TrimSpace(m.Pair.Name)
	} else if a.pairPartners != nil {
		delete(a.pairPartners, char) // a solo line clears their pairing
	}
}

// pairPartnerOf returns the partner a roster player is currently paired with ("" = none), for the
// opt-in player-list chip. Matched by lowercased character.
func (a *App) pairPartnerOf(p *areaPlayer) string {
	if len(a.pairPartners) == 0 || p.name == "" {
		return ""
	}
	return a.pairPartners[strings.ToLower(p.name)]
}

// areaPlayer is one parsed /getarea player. One row per UID (never deduped by
// name — two "Spectator" rows are two different people). ipid is mod-only data
// from /getarea: shown in-session, never persisted or logged. area is the area
// the player is in (from a /gas multi-area block; "" for a header-less /ga) —
// it groups the player list and drives click-an-area-to-jump.
type areaPlayer struct{ uid, name, showname, ooc, ipid, area string }

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

// addAreaPlayer records a parsed /getarea player: one roster row PER UID (so
// same-named players don't collapse), plus a lossy name→uid entry for the
// double-click auto-fill (last writer wins — that path always has a manual box).
func (a *App) addAreaPlayer(name, uid string) {
	// Akashi's mod /getarea packs everything onto one line:
	//   "<char> (<showname>) (<ipid>): <ooc name>"   (showname/ipid/ooc optional).
	// Peel the trailing ": ooc" and "(ipid)", then a leading "(showname)/(pos)",
	// leaving the BARE character — so it matches the CharsCheck name (the live-
	// roster merge key) AND the IPID/OOC buttons get their values. tsuserver's
	// separate Showname:/OOC:/IPID: lines still fill in via setAreaField (they
	// leave these empty here).
	var showname, ipid, ooc string
	if i := strings.LastIndex(name, "): "); i >= 0 { // "(ipid): ooc" tail (mod view)
		ooc = strings.TrimSpace(name[i+3:])
		name = name[:i+1] // keep through the ")" of "(ipid)"
		if j := strings.LastIndexByte(name, '('); j >= 0 {
			ipid = strings.TrimSpace(strings.TrimRight(name[j+1:], ")"))
			name = name[:j]
		}
	}
	if i := strings.LastIndexByte(name, '('); i > 0 { // a remaining "(showname)" / "(pos)"
		showname = strings.TrimSpace(strings.Trim(name[i:], "()"))
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if a.pairAreaReset { // a fresh /getarea: start the roster over (replace, never accumulate)
		a.areaUIDs = nil
		a.areaPlayers = a.areaPlayers[:0]
		a.playerIconPages = nil // re-resolve icons: a same-length new roster reuses indices (cachedPage reorder invariant)
		a.playerIconAsk = nil   // and re-arm the demand timers so the new faces fetch immediately
		a.pairAreaReset = false
		a.areaListAt = a.now()
	}
	if a.areaUIDs == nil {
		a.areaUIDs = map[string]string{}
	}
	a.areaUIDs[strings.ToLower(name)] = uid
	if showname != "" {
		a.areaUIDs[strings.ToLower(showname)] = uid // IC name → UID for the pair auto-fill
	}
	if len(a.areaPlayers) < areaPlayersCap {
		a.areaPlayers = append(a.areaPlayers, areaPlayer{uid: uid, name: name, showname: showname, ooc: ooc, ipid: ipid, area: a.areaCurName})
	}
}

// setAreaField attaches a Showname/OOC/IPID value to the most recent player row
// (the one whose "[uid]" line preceded it).
func (a *App) setAreaField(field, val string) {
	val = strings.TrimSpace(val)
	n := len(a.areaPlayers)
	if val == "" || n == 0 || a.areaPlayers[n-1].uid != a.areaLastUID {
		return
	}
	switch field {
	case "showname":
		a.areaPlayers[n-1].showname = val
		a.areaUIDs[strings.ToLower(val)] = a.areaLastUID // the IC name → UID, for auto-fill
	case "ooc":
		a.areaPlayers[n-1].ooc = val
	case "ipid":
		a.areaPlayers[n-1].ipid = val
	}
}

// parseAreaBlock parses a /getarea payload line by line. A "----" divider opens
// an area block; the FIRST one in a payload REPLACES the roster (any fetch — the
// buttons OR a hand-typed /ga — starts clean, and servers recycle UIDs so
// accumulating would mispair), while LATER dividers in the same payload are a
// /gas's extra areas and ACCUMULATE as new groups. The line after a divider is
// the area name ("Lobby:"), then "N players online." (skipped), then "[uid]"
// rows whose Showname/OOC/IPID lines attach to them. Fast-rejects ordinary chat.
func (a *App) parseAreaBlock(text string) {
	low := strings.ToLower(text)
	if !strings.ContainsRune(text, '[') && !strings.Contains(low, "showname") && !strings.Contains(low, "players online") {
		return
	}
	wantShowname := false
	firstArea := true       // first block REPLACES; a /gas's later blocks accumulate
	expectAreaName := false // the line right after a divider names the area
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "----") { // area divider (/ga: one; /gas: one per non-empty area)
			if firstArea {
				a.pairAreaReset = true // the next "[uid]" starts a clean roster + stamps the time
				firstArea = false
			}
			a.areaCurName = "" // the name line below sets it ("" = a nameless block)
			expectAreaName = true
			wantShowname = false
			continue
		}
		if expectAreaName {
			expectAreaName = false
			if isAreaNameLine(t) { // "Lobby:" / "Pizza Room 3:" — tags the players that follow
				a.areaCurName = strings.TrimSpace(strings.TrimSuffix(t, ":"))
				continue
			}
			// else fall through: a nameless block, or the "N empty area(s) hidden." footer
		}
		if isPlayerCountLine(t) { // "13 players online." — informational, not a player
			continue
		}
		if wantShowname { // the line right after a bare "Showname:" is the name
			wantShowname = false
			if t != "" {
				a.setAreaField("showname", t)
				continue
			}
		}
		switch lt := strings.ToLower(t); {
		case strings.HasPrefix(lt, "showname:"):
			if name := strings.TrimSpace(t[len("showname:"):]); name != "" {
				a.setAreaField("showname", name) // inline "Showname: X"
			} else {
				wantShowname = true // bare "Showname:" → the next line
			}
		case strings.HasPrefix(lt, "ooc:"):
			a.setAreaField("ooc", t[len("ooc:"):])
		case strings.HasPrefix(lt, "ipid:"):
			a.setAreaField("ipid", t[len("ipid:"):])
		default:
			a.parseAreaLine(line) // "[uid] name" sets areaLastUID + adds the roster row
		}
	}
}

// isAreaNameLine spots a /gas area-name line ("Lobby:", "Pizza Room 3:") — a line
// ending in ":" that isn't one of the per-player field labels.
func isAreaNameLine(t string) bool {
	if t == "" || !strings.HasSuffix(t, ":") {
		return false
	}
	low := strings.ToLower(t)
	return !strings.HasPrefix(low, "showname:") && !strings.HasPrefix(low, "ooc:") && !strings.HasPrefix(low, "ipid:")
}

// isPlayerCountLine spots the "N players online." line under each area name —
// informational, so it neither resets the roster nor adds a player.
func isPlayerCountLine(t string) bool {
	low := strings.ToLower(t)
	return strings.Contains(low, "players online") || strings.Contains(low, "player online")
}

// looksLikeAreaList reports whether an OOC payload is a /getarea (/ga, /gas)
// reply rather than chat — so running it never fires YOUR OWN callword (your name
// is right there in the roster). Matches the header or any verbose roster line.
func looksLikeAreaList(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(t, "players online") ||
		strings.HasPrefix(t, "showname:") || strings.HasPrefix(t, "ooc:") || strings.HasPrefix(t, "ipid:") {
		return true
	}
	for i := 0; i < len(text); i++ { // a "[<digits>] name" roster line (past any "[Title]")
		if text[i] != '[' {
			continue
		}
		if j := strings.IndexByte(text[i+1:], ']'); j >= 1 {
			if _, err := strconv.Atoi(strings.TrimSpace(text[i+1 : i+1+j])); err == nil {
				return true
			}
		}
	}
	return false
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
	if a.playerPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.playerPct = a.logPct
	}
	lineH := int32(24) * int32(a.playerPct) / 100
	a.zoomWheel(r, &a.playerPct, config.MinLogScalePercent, config.MaxLogScalePercent) // Ctrl+wheel zooms text
	if !c.ctrlHeld {
		a.pairListScroll -= c.WheelIn(r) * scrollStepPx
	}
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
			c.LabelClippedFont(c.LogFontFor(a.playerPct, label), row.X+6, row.Y+4, row.W-12, label, ColText)
		}
		rowY += lineH
	}
}
