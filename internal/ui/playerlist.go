package ui

// The Players tab: AsyncAO's player list (Akashi/Nyathena-style), built from the
// /getarea snapshot the click-to-pair parser already harvests. AO has no
// per-player packet (only ARUP area COUNTS), so it's refresh-driven and stamped
// "as of HH:MM", not live. IPIDs are mod-only data shown in-session — never
// persisted. The foundation for future mod/user tools.
//
// On top of the bare roster this draws: a char-icon per row (the char grid's
// demand/cache pipeline), role highlights (you / current speaker / friends),
// Spectator + CM chips, a sort toggle, and — before any /ga — a live ARUP count
// for the area you're in.

import (
	"sort"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	playerRowH    = int32(44) // a char icon flanked by two text rows (IC identity, OOC)
	playerIconSz  = int32(38)
	playerHeaderH = int32(26) // a /gas area-group header row (name + count, click to jump)
)

// Roster sort modes, cycled by the Sort button.
const (
	playerSortUID      = iota // by client UID, numeric
	playerSortName            // by IC name (showname, else character)
	playerSortSpeaking        // whoever spoke most recently, first
	playerSortModes           // count, for the cycle
)

// Role chip colors (Spectator / case master), drawn as small filled pills.
var (
	chipSpecColor = sdl.Color{R: 96, G: 96, B: 104, A: 255}  // spectator: muted grey
	chipCMColor   = sdl.Color{R: 235, G: 195, B: 70, A: 255} // case master: gold
)

func playerSortLabel(mode int) string {
	switch mode {
	case playerSortName:
		return "Name"
	case playerSortSpeaking:
		return "Speaking"
	default:
		return "UID"
	}
}

// drawPlayerList renders the parsed area roster — one row per UID — with the
// /ga · /gas · /getarea fetch buttons, a sort toggle, a snapshot time, and
// per-row icon + highlights + Pair / Copy actions.
func (a *App) drawPlayerList(r sdl.Rect) {
	c := a.ctx
	if a.playerPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.playerPct = a.logPct
	}
	// Row 1: live indicator (default) OR the legacy fetch buttons, plus the
	// "Legacy snapshot" tick box that switches the two. Live = no traffic; legacy
	// = a /getarea snapshot whose hand fetch REPLACES the roster (clean restart).
	if a.rosterLegacy {
		c.Label(r.X, r.Y+5, "Fetch:", ColTextDim)
		bx := r.X + 48
		for _, cmd := range []string{"/ga", "/gas", "/getarea"} {
			bw := c.TextWidth(cmd) + 14
			if c.Button(sdl.Rect{X: bx, Y: r.Y, W: bw, H: 22}, cmd) {
				a.pairAreaReset = true
				a.queueOOCLines([]string{cmd})
				a.warnLine = clampLine("Sent " + cmd + " — the list fills from the reply.")
				a.warnAt = a.now()
			}
			bx += bw + 5
		}
	} else {
		c.Label(r.X, r.Y+5, "● LIVE", ColTierGreen)
		rb := sdl.Rect{X: r.X + 52, Y: r.Y, W: 116, H: 22}
		if c.Button(rb, "Refresh details") {
			a.pairAreaReset = true
			a.queueOOCLines([]string{"/getarea"})
			a.warnLine = clampLine("Fetching UIDs / IPIDs for this area…")
			a.warnAt = a.now()
		}
		c.Tooltip(rb, "Pull UIDs, IPIDs, OOC names + Pair/Copy onto the live rows (one /getarea). The list stays live — refresh again to fill in new joiners.")
	}
	const legLabel = "Legacy snapshot"
	legW := int32(22) + c.TextWidth(legLabel)
	legX := r.X + r.W - legW - 4
	if next := c.Checkbox(legX, r.Y+3, legLabel, a.rosterLegacy); next != a.rosterLegacy {
		a.setRosterLegacy(next)
	}
	c.Tooltip(sdl.Rect{X: legX, Y: r.Y + 3, W: legW, H: 16},
		"Off (default): live roster from the server's join/leave signals — no commands sent, spectators come & go by head-count. On: the classic /getarea snapshot with names, UIDs & IPIDs (Pair/Copy), fetched on demand.")
	r.Y += 26
	r.H -= 26
	// Row 2: sort toggle + status (live head-count vs. legacy snapshot time).
	sortBtn := "Sort: " + playerSortLabel(a.playerSort)
	sw := c.TextWidth(sortBtn) + 16
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: sw, H: 22}, sortBtn) {
		a.playerSort = (a.playerSort + 1) % playerSortModes
	}
	status := strconv.Itoa(len(a.rosterView())) + " here · live"
	if a.rosterLegacy {
		status = strconv.Itoa(len(a.rosterView())) + " players"
		if !a.areaListAt.IsZero() {
			status += "  ·  as of " + a.areaListAt.Format("15:04") // a snapshot, not live
		}
	}
	c.LabelClipped(r.X+sw+10, r.Y+5, r.X+r.W-(r.X+sw+10)-4, status, ColTextDim)
	r.Y += 28
	r.H -= 28

	if len(a.rosterView()) == 0 {
		hint := "Run /ga (or /gas, /getarea) to list who's in this area."
		if !a.rosterLegacy {
			hint = "Nobody with a character here yet — the list fills live as people join."
		}
		if n, ok := a.curAreaPlayers(); ok {
			hint = "This area has " + strconv.Itoa(n) + " player(s) right now."
			if a.rosterLegacy {
				hint += " Run /ga for names."
			}
		}
		c.LabelClipped(r.X, r.Y+4, r.W, hint, ColTextDim)
		return
	}

	speaker := a.currentSpeakerName()
	myUID := ""
	if a.sess != nil && a.sess.PlayerID > 0 {
		myUID = strconv.Itoa(a.sess.PlayerID)
	}
	cmSet := a.cmNameSet()
	rows := a.playerRosterRows(speaker) // flat for a /ga, area-grouped for a /gas

	a.zoomWheel(r, &a.playerPct, config.MinLogScalePercent, config.MaxLogScalePercent) // Ctrl+wheel zooms text
	if !c.ctrlHeld {
		a.playerScroll -= c.WheelIn(r) * scrollStepPx
	}
	contentH := int32(0)
	for i := range rows {
		contentH += a.rowHeight(rows[i])
	}
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.playerScroll = c.VScrollbar("playerlist", track, a.playerScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowW := r.W - scrollBarW - 6
	y := r.Y - a.playerScroll
	for i := range rows {
		rh := a.rowHeight(rows[i])
		if y > r.Y+r.H {
			break
		}
		if y >= r.Y-rh {
			if rows[i].header {
				a.drawAreaHeaderRow(rows[i], sdl.Rect{X: r.X, Y: y, W: rowW, H: rh - 2})
			} else {
				a.drawPlayerRow(rows[i].idx, sdl.Rect{X: r.X, Y: y, W: rowW, H: playerRowH - 4}, myUID, speaker, cmSet)
			}
		}
		y += rh
	}
}

// rowHeight is the display height of one roster row (area headers are shorter);
// player rows scale with the Players-tab text zoom (Ctrl+wheel).
func (a *App) rowHeight(row rosterRow) int32 {
	if row.header {
		return playerHeaderH
	}
	return playerRowH * int32(a.playerPct) / 100
}

// drawPlayerRow is one player: char icon + "[uid] showname · character" with the
// OOC/IPID detail beneath, role highlight (you / speaker / friend), Spectator/CM
// chips, and right-aligned Pair / Copy-UID / Copy-IPID actions. idx is the index
// into areaPlayers (sort-stable) — the key for the icon cache.
func (a *App) drawPlayerRow(idx int, row sdl.Rect, myUID, speaker string, cmSet map[string]bool) {
	c := a.ctx
	p := &a.rosterView()[idx]
	isMod := a.sess != nil && a.sess.ModGranted
	isSpec := strings.EqualFold(p.name, "Spectator")
	isMe := myUID != "" && p.uid == myUID
	isSpeaker := speaker != "" && (strings.EqualFold(p.showname, speaker) || strings.EqualFold(p.name, speaker))
	isCM := cmSet != nil && (cmSet[strings.ToLower(p.showname)] || cmSet[strings.ToLower(p.name)])
	friend := false
	if a.d.Prefs.FriendHighlightOn() {
		nm := p.showname
		if nm == "" {
			nm = p.name
		}
		friend, _ = a.d.Prefs.ServerFriendMatch(a.serverKey, nm)
	}
	display := rosterName(p) // showname → OOC → character

	// Background + role tint (you > speaking > friend) + a left accent bar.
	c.Fill(row, ColPanel)
	switch {
	case isMe:
		c.Fill(row, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 46})
	case isSpeaker:
		c.Fill(row, sdl.Color{R: ColTierGreen.R, G: ColTierGreen.G, B: ColTierGreen.B, A: 46})
	case friend:
		c.Fill(row, friendTintColor)
	}
	if bar, ok := rosterBarColor(isMe, isSpeaker, friend); ok {
		c.Fill(sdl.Rect{X: row.X, Y: row.Y, W: 3, H: row.H}, bar)
	}
	if c.hovering(row) {
		c.Border(row, ColPanelHi)
	}

	// Char icon (left). Spectators have no character art, so don't demand it.
	iconR := sdl.Rect{X: row.X + 6, Y: row.Y + (row.H-playerIconSz)/2, W: playerIconSz, H: playerIconSz}
	a.drawPlayerIcon(p, idx, iconR, isSpec)

	// Right cluster: Pair + compact Copy buttons (mod also gets Copy-IPID).
	btnY := row.Y + (row.H-22)/2
	bx := row.X + row.W - 4
	// Pair / Copy-UID need a UID, which only the /getarea snapshot carries — live
	// rows (CharsCheck) have none, so these appear only in legacy mode.
	if p.uid != "" {
		pw := c.TextWidth("Pair") + 14
		bx -= pw
		if c.Button(sdl.Rect{X: bx, Y: btnY, W: pw, H: 22}, "Pair") {
			a.queueOOCLines([]string{"/pair " + p.uid}) // we have the UID — no popup needed
			a.warnLine = clampLine("Sent /pair " + p.uid + " — " + display)
			a.warnAt = a.now()
		}
		uw := c.TextWidth("UID") + 12
		bx -= uw + 4
		if c.Button(sdl.Rect{X: bx, Y: btnY, W: uw, H: 22}, "UID") {
			_ = sdl.SetClipboardText(p.uid)
			a.warnLine = clampLine("Copied UID " + p.uid)
			a.warnAt = a.now()
		}
	}
	if p.ipid != "" && isMod {
		iw := c.TextWidth("IPID") + 12
		bx -= iw + 4
		if c.Button(sdl.Rect{X: bx, Y: btnY, W: iw, H: 22}, "IPID") {
			_ = sdl.SetClipboardText(p.ipid)
			a.warnLine = clampLine("Copied IPID for " + display)
			a.warnAt = a.now()
		}
	}

	// Text column between the icon and the button cluster.
	textX := row.X + 6 + playerIconSz + 8
	textW := bx - textX - 8
	if textW < 40 {
		textW = 40
	}
	// Line 1 — role chips, then "[uid] showname · character".
	cx := textX
	if isCM {
		cx += a.drawRosterChip(cx, row.Y+4, "CM", chipCMColor, ColBackground) + 5
	}
	if isSpec {
		cx += a.drawRosterChip(cx, row.Y+4, "SPEC", chipSpecColor, ColText) + 5
	}
	ic := "[" + p.uid + "]  " + p.name
	if !strings.EqualFold(display, p.name) {
		ic = "[" + p.uid + "]  " + display + "  ·  " + p.name
	}
	c.LabelClippedFont(c.LogFontFor(a.playerPct, ic), cx, row.Y+5, textW-(cx-textX), ic, ColText)
	// Line 2 — OOC name (+ IPID for mods), dimmer. Skip OOC when it's already the
	// display name above (no showname → OOC was promoted to the identity line).
	sub := ""
	if p.ooc != "" && p.showname != "" {
		sub = "OOC: " + p.ooc
	}
	if p.ipid != "" && isMod {
		if sub != "" {
			sub += "   ·   "
		}
		sub += "IPID: " + p.ipid
	}
	if sub != "" {
		c.LabelClippedFont(c.LogFontFor(a.playerPct, sub), textX, row.Y+row.H-int32(16*a.playerPct/100), textW, sub, ColTextDim)
	}
}

// drawPlayerIcon paints one roster row's character icon, reusing the char grid's
// demand/cache pipeline (paced asks, 404 cache, generation-aware page cache).
// Spectators have no character, so they get a plain placeholder and no demand.
func (a *App) drawPlayerIcon(p *areaPlayer, idx int, cell sdl.Rect, isSpec bool) {
	c := a.ctx
	c.Fill(cell, ColPanelHi)
	if isSpec {
		return
	}
	base := a.urls.CharIcon(p.name)
	if page, ok := a.cachedPage(&a.playerIconPages, &a.playerIconPagesGen, len(a.rosterView()), idx, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
		return
	}
	a.demandAsset(&a.playerIconAsk, len(a.rosterView()), idx, base, assets.AssetTypeCharIcon) // AssetType: CharIcon
	initial := p.name
	if len(initial) > 2 {
		initial = initial[:2]
	}
	c.Label(cell.X+4, cell.Y+cell.H/2-8, initial, ColTextDim)
}

// drawRosterChip draws a small filled pill (e.g. "CM", "SPEC") and returns its
// width so the caller can advance the cursor.
func (a *App) drawRosterChip(x, y int32, text string, bg, fg sdl.Color) int32 {
	c := a.ctx
	w := c.TextWidth(text) + 10
	c.Fill(sdl.Rect{X: x, Y: y, W: w, H: 16}, bg)
	c.Label(x+5, y+1, text, fg)
	return w
}

// rosterBarColor picks the left-edge accent for a row's most salient role.
func rosterBarColor(isMe, isSpeaker, friend bool) (sdl.Color, bool) {
	switch {
	case isMe:
		return ColAccent, true
	case isSpeaker:
		return ColTierGreen, true
	case friend:
		return sdl.Color{R: 255, G: 210, B: 90, A: 255}, true
	}
	return sdl.Color{}, false
}

// currentSpeakerName is the lowercased displayed name of the most recent IC line
// — the "who's talking now" signal for the speaker highlight and the
// speakers-first sort. "" when nobody has spoken or the last line was a system
// line.
func (a *App) currentSpeakerName() string {
	if n := len(a.icLog); n > 0 {
		return strings.ToLower(strings.TrimSpace(a.icLog[n-1].speaker))
	}
	return ""
}

// curAreaPlayers returns the live ARUP head-count for the area we're in (matched
// by name; area 0 on a fresh join before any area click). ok=false when unknown.
func (a *App) curAreaPlayers() (int, bool) {
	if a.sess == nil || len(a.sess.AreaInfo) == 0 {
		return 0, false
	}
	idx := -1
	for i, name := range a.sess.Areas {
		if name == a.curArea {
			idx = i
			break
		}
	}
	if idx < 0 {
		if a.curArea != "" { // we've navigated, but the name didn't match the list
			return 0, false
		}
		idx = 0 // fresh join: assume the spawn area
	}
	if idx >= len(a.sess.AreaInfo) {
		return 0, false
	}
	n := a.sess.AreaInfo[idx].Players
	if n < 0 { // -1 = server hasn't reported it
		return 0, false
	}
	return n, true
}

// cmNameSet is the lowercased set of case-master names across all areas (ARUP
// cms), so a roster row whose name is a CM gets the chip. Excludes ""/FREE and
// splits multi-CM lists. nil when no area has a CM.
func (a *App) cmNameSet() map[string]bool {
	if a.sess == nil {
		return nil
	}
	var set map[string]bool
	for i := range a.sess.AreaInfo {
		cm := strings.TrimSpace(a.sess.AreaInfo[i].CM)
		if cm == "" || strings.EqualFold(cm, "FREE") {
			continue
		}
		for _, nm := range strings.Split(cm, ",") {
			nm = strings.ToLower(strings.TrimSpace(nm))
			if nm == "" {
				continue
			}
			if set == nil {
				set = map[string]bool{}
			}
			set[nm] = true
		}
	}
	return set
}

// playerRosterOrder returns the display order (indices into areaPlayers) for the
// current sort. Memoized: it recomputes only when the roster, sort mode, or
// current speaker change — never per frame.
func (a *App) playerRosterOrder(speaker string) []int {
	spk := ""
	if a.playerSort == playerSortSpeaking {
		spk = speaker // only this mode depends on who's talking
	}
	if a.playerOrder != nil && a.playerOrderLen == len(a.rosterView()) &&
		a.playerOrderSort == a.playerSort && a.playerOrderSpk == spk &&
		a.playerOrderAt.Equal(a.rosterStamp()) {
		return a.playerOrder
	}
	ord := a.playerOrder[:0]
	if cap(ord) < len(a.rosterView()) {
		ord = make([]int, 0, len(a.rosterView()))
	}
	for i := range a.rosterView() {
		ord = append(ord, i)
	}
	a.sortRosterIdxs(ord, spk)
	a.playerOrder = ord
	a.playerOrderLen = len(a.rosterView())
	a.playerOrderSort = a.playerSort
	a.playerOrderSpk = spk
	a.playerOrderAt = a.rosterStamp()
	return ord
}

// sortRosterIdxs orders indices into areaPlayers in place by the current sort
// mode (shared by the flat list and each /gas area group).
func (a *App) sortRosterIdxs(ord []int, spk string) {
	switch a.playerSort {
	case playerSortName:
		sort.SliceStable(ord, func(i, j int) bool {
			return strings.ToLower(rosterName(&a.rosterView()[ord[i]])) <
				strings.ToLower(rosterName(&a.rosterView()[ord[j]]))
		})
	case playerSortSpeaking:
		sort.SliceStable(ord, func(i, j int) bool { // speakers first; stable keeps parse order otherwise
			return rosterIsSpeaker(&a.rosterView()[ord[i]], spk) && !rosterIsSpeaker(&a.rosterView()[ord[j]], spk)
		})
	default: // playerSortUID
		sort.SliceStable(ord, func(i, j int) bool {
			return uidLess(a.rosterView()[ord[i]].uid, a.rosterView()[ord[j]].uid)
		})
	}
}

// rosterRow is one display row of the player list: a /gas area-group header
// (name + count, click to jump) or a player (index into areaPlayers).
type rosterRow struct {
	header bool
	area   string // header: the area name
	count  int    // header: players in the group
	idx    int    // player: index into areaPlayers
}

// rosterMultiArea reports whether the roster spans ≥2 distinct named areas (a
// /gas), so the list groups by area instead of showing one flat run (a /ga).
func (a *App) rosterMultiArea() bool {
	first, seen := "", false
	for i := range a.rosterView() {
		ar := a.rosterView()[i].area
		if ar == "" {
			continue
		}
		if !seen {
			first, seen = ar, true
		} else if ar != first {
			return true
		}
	}
	return false
}

// playerRosterRows is the memoized GROUPED display. A single-area roster (/ga) is
// the flat sorted list (no headers); a /gas spanning areas emits an area header
// before each group, players sorted within. Same invalidation keys as
// playerRosterOrder, so it rebuilds only on roster/sort/speaker change.
func (a *App) playerRosterRows(speaker string) []rosterRow {
	spk := ""
	if a.playerSort == playerSortSpeaking {
		spk = speaker
	}
	if a.playerRows != nil && a.playerRowsLen == len(a.rosterView()) &&
		a.playerRowsSort == a.playerSort && a.playerRowsSpk == spk &&
		a.playerRowsAt.Equal(a.rosterStamp()) {
		return a.playerRows
	}
	rows := a.playerRows[:0]
	if !a.rosterMultiArea() {
		for _, idx := range a.playerRosterOrder(speaker) {
			rows = append(rows, rosterRow{idx: idx})
		}
	} else {
		order := make([]string, 0, 8) // areas in first-seen (parse) order
		groups := map[string][]int{}
		for i := range a.rosterView() {
			ar := a.rosterView()[i].area
			if _, ok := groups[ar]; !ok {
				order = append(order, ar)
			}
			groups[ar] = append(groups[ar], i)
		}
		for _, ar := range order {
			idxs := groups[ar]
			a.sortRosterIdxs(idxs, spk)
			rows = append(rows, rosterRow{header: true, area: ar, count: len(idxs)})
			for _, idx := range idxs {
				rows = append(rows, rosterRow{idx: idx})
			}
		}
	}
	a.playerRows = rows
	a.playerRowsLen, a.playerRowsSort, a.playerRowsSpk, a.playerRowsAt =
		len(a.rosterView()), a.playerSort, spk, a.rosterStamp()
	return rows
}

// drawAreaHeaderRow draws a /gas area-group header: the area name + headcount,
// the whole row clickable to jump there (an area transfer by name via the music
// list). Inert when there's no live session.
func (a *App) drawAreaHeaderRow(hr rosterRow, r sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanelHi)
	name := hr.area
	if name == "" {
		name = "(unnamed area)"
	}
	if a.sess != nil && hr.area != "" {
		if c.hovering(r) {
			c.Border(r, ColAccent)
			if c.clicked {
				a.jumpToArea(hr.area)
			}
		}
		c.LabelClipped(r.X+r.W-120, r.Y+6, 116, "click to jump →", ColTextDim)
	}
	c.LabelClipped(r.X+8, r.Y+6, r.W-132, name+"  ·  "+strconv.Itoa(hr.count)+" player(s)", ColText)
}

// jumpToArea transfers us to area by name (AO switches areas through the music
// list: MC#<area name>#<char id>). The name comes from the same server, so it
// matches the area list.
func (a *App) jumpToArea(area string) {
	if a.sess == nil || area == "" {
		return
	}
	a.sess.RequestMusic(area)
	a.warnLine = clampLine("Jumping to " + area + "…")
	a.warnAt = a.now()
}

// rosterName is the recognisable display name: showname, else the OOC name (an
// iniswapper with no showname is known by their OOC handle), else the character.
func rosterName(p *areaPlayer) string {
	if p.showname != "" {
		return p.showname
	}
	if p.ooc != "" {
		return p.ooc
	}
	return p.name
}

// rosterIsSpeaker reports whether p matches the lowercased current speaker name.
func rosterIsSpeaker(p *areaPlayer, spk string) bool {
	return spk != "" && (strings.EqualFold(p.showname, spk) || strings.EqualFold(p.name, spk))
}

// uidLess orders two UID strings numerically (falling back to lexical for the
// odd non-numeric id).
func uidLess(x, y string) bool {
	nx, ex := strconv.Atoi(x)
	ny, ey := strconv.Atoi(y)
	if ex == nil && ey == nil {
		return nx < ny
	}
	return x < y
}
