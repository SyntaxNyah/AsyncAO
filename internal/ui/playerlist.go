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
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	playerRowH    = int32(44) // a char icon flanked by two text rows (IC identity, OOC)
	playerIconSz  = int32(38)
	playerHeaderH = int32(26) // a /gas area-group header row (name + count, click to jump)
)

// Roster sort modes, cycled by the Sort button (orders PLAYERS, flat or within
// each /gas area group).
const (
	playerSortUID      = iota // by client UID, numeric
	playerSortName            // by IC name (showname, else character)
	playerSortSpeaking        // whoever spoke most recently, first
	playerSortModes           // count, for the cycle
)

// Area-group sort modes, cycled by the Rooms button — orders the /gas area
// HEADERS (a second axis from playerSort, which orders players). Default keeps
// the server's /gas order so room 1 stays on top.
const (
	areaSortGas   = iota // our current area first, then the server's /gas order — default
	areaSortName         // area name, A→Z
	areaSortPop          // most players first
	areaSortModes        // count, for the cycle
)

// clampMode keeps a remembered sort index inside [0, n) — a stale pref or a future
// change to the mode count can't select an out-of-range sort.
func clampMode(v, n int) int {
	if v < 0 || v >= n {
		return 0
	}
	return v
}

func areaSortLabel(mode int) string {
	switch mode {
	case areaSortName:
		return "A–Z"
	case areaSortPop:
		return "Most"
	default:
		return "/gas"
	}
}

// sortAreaOrder orders the area-group names in place for the A→Z and most-populated
// modes (the default /gas mode is handled by applyGasAreaOrder, which needs our
// current area + the snapshot order). Stable, so a tie keeps the incoming order.
// Pure (groups supplies each area's size) — tested directly.
func sortAreaOrder(order []string, groups map[string][]int, mode int) {
	switch mode {
	case areaSortName:
		sort.SliceStable(order, func(i, j int) bool {
			return strings.ToLower(order[i]) < strings.ToLower(order[j])
		})
	case areaSortPop:
		sort.SliceStable(order, func(i, j int) bool {
			return len(groups[order[i]]) > len(groups[order[j]])
		})
	}
}

// applyGasAreaOrder reorders the area-group headers for the DEFAULT mode: OUR
// current area first, then the server's /gas order (the order areaPlayers — the
// /getareas snapshot — listed the areas), then any live-only areas keep their
// first-seen order. This fixes the live roster grouping by the lowest-UID player's
// area (UID 0 in "Pizza Room 1" floating above the Lobby you're standing in)
// instead of the server's order. Stable; reuses the already-built order/groups; one
// area-count-sized rank map. Runs ONLY on a grouped-rows REBUILD (memoized), so it
// adds nothing to the per-frame draw.
func (a *App) applyGasAreaOrder(order []string, groups map[string][]int) {
	mine := a.myAreaName()
	gasRank := make(map[string]int, len(order)) // area → its index in the /gas snapshot
	for i := range a.areaPlayers {
		ar := a.areaPlayers[i].area
		if _, ok := gasRank[ar]; !ok {
			gasRank[ar] = len(gasRank)
		}
	}
	after := len(gasRank) + 1 // live-only areas (no snapshot row) sort after the /gas ones
	rank := func(ar string) int {
		switch {
		case ar != "" && ar == mine:
			return -1 // our current area floats to the very top
		default:
			if r, ok := gasRank[ar]; ok {
				return r
			}
			return after
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return rank(order[i]) < rank(order[j]) })
}

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
	// Pull the rich /getarea snapshot (UIDs/IPIDs/Pair) when the live tab first
	// opens and on each area change; join/leave refreshes it via maybeRefetchRoster.
	if !a.rosterLegacy && a.sess != nil && a.liveDetailsArea != a.curArea {
		a.liveDetailsArea = a.curArea
		a.fetchRoster()
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
		a.d.Prefs.SetPlayerListSort(a.playerSort) // remember it across sessions
	}
	statusX := r.X + sw + 10
	// Rooms button: orders the /gas AREA GROUPS (a second axis from Sort, which
	// orders players). Default keeps the server's /gas order; only shown when the
	// roster spans areas — there's nothing to order in a single-area /ga list.
	if a.rosterMultiArea() {
		roomsBtn := "Rooms: " + areaSortLabel(a.playerAreaSort)
		rw := c.TextWidth(roomsBtn) + 16
		rb := sdl.Rect{X: statusX, Y: r.Y, W: rw, H: 22}
		if c.Button(rb, roomsBtn) {
			a.playerAreaSort = (a.playerAreaSort + 1) % areaSortModes
			a.d.Prefs.SetPlayerListAreaSort(a.playerAreaSort) // remember it across sessions
		}
		c.Tooltip(rb, "Order the area groups: /gas (the server's own order), A–Z, or most players first.")
		statusX += rw + 10
	}
	// Follow toggle (M3, opt-in / OFF by default): when on, every row gets a
	// Follow button that auto-jumps you to that player's area as they move.
	const followLabel = "Follow"
	follW := int32(22) + c.TextWidth(followLabel)
	follX := r.X + r.W - follW - 4
	followOn := a.d.Prefs.FollowEnabledOn()
	a.followShow = followOn // cache for the per-row Follow buttons (one read per frame, no per-row lock)
	if next := c.Checkbox(follX, r.Y+3, followLabel, followOn); next != followOn {
		a.d.Prefs.SetFollowEnabled(next)
		a.followShow = next
		if !next {
			a.followUID = "" // turning it off stops any active trailing
		}
	}
	c.Tooltip(sdl.Rect{X: follX, Y: r.Y + 3, W: follW, H: 16},
		"Off (default): no follow. On: each row gets a Follow button that auto-jumps you to that player's area whenever they move.")
	// Pair-status toggle (#20, opt-in / OFF by default): when on, a row shows who that player is
	// currently paired with (⇄), tracked from IC messages. Sits left of Follow on the right edge.
	const pairLbl = "Pairs"
	pairW := int32(22) + c.TextWidth(pairLbl)
	pairX := follX - pairW - 12
	pairOn := a.d.Prefs.ShowPairStatusOn()
	a.pairStatusShow = pairOn // cache once per frame (no per-row lock)
	if next := c.Checkbox(pairX, r.Y+3, pairLbl, pairOn); next != pairOn {
		a.d.Prefs.SetShowPairStatus(next)
		a.pairStatusShow = next
	}
	c.Tooltip(sdl.Rect{X: pairX, Y: r.Y + 3, W: pairW, H: 16},
		"Off (default). On: each row shows who that player is currently paired with (⇄), updated as they speak.")
	// #M1: your own cross-client status — a cycle button (none → AFK → Busy → Writing →
	// LFRP). Send-on-change transmits it on your next IC message; AsyncAO players see a
	// coloured chip on your row, standard clients see nothing.
	statLbl := statusLabel(a.myStatus)
	if statLbl == "" {
		statLbl = "none"
	}
	statBtnW := c.TextWidth("Status: Writing") + 18 // size to the widest label so it doesn't jump
	statBtnX := follX - statBtnW - 12
	statBtn := sdl.Rect{X: statBtnX, Y: r.Y, W: statBtnW, H: 22}
	if c.Button(statBtn, "Status: "+statLbl) {
		a.myStatus = (a.myStatus + 1) % courtroom.StatusCount
	}
	c.Tooltip(statBtn, "Set your status (AFK / Busy / Writing / LFRP). Other AsyncAO players see a chip on your row after your next message; standard clients are unaffected.")
	status := strconv.Itoa(len(a.rosterView())) + " here · live"
	if !a.rosterLegacy && !a.livePlayersOn && len(a.areaPlayers) == 0 {
		status += " · fetching details…" // CharsCheck fallback; the rich /getarea snapshot hasn't landed
	}
	if a.rosterLegacy {
		status = strconv.Itoa(len(a.rosterView())) + " players"
		if !a.areaListAt.IsZero() {
			status += "  ·  as of " + a.areaListAt.Format("15:04") // a snapshot, not live
		}
	}
	c.LabelClipped(statusX, r.Y+5, statBtnX-statusX-8, status, ColTextDim)
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
				a.drawPlayerRow(rows[i].idx, sdl.Rect{X: r.X, Y: y, W: rowW, H: rh - 4}, myUID, speaker, cmSet)
			}
		}
		y += rh
	}
	a.drawProfileCardOverlay(r) // #101: the profile popover sits on top of the list
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
	isSpec := strings.EqualFold(p.name, "Spectator")
	isMe := myUID != "" && p.uid == myUID
	isSpeaker := speaker != "" && (strings.EqualFold(p.showname, speaker) || strings.EqualFold(p.name, speaker))
	isCM := cmSet != nil && (cmSet[strings.ToLower(p.showname)] || cmSet[strings.ToLower(p.name)])
	// Friend lookup, ONCE: drives the row tint (only when highlights are on) plus
	// the per-friend nickname + name colour (#82, shown regardless of the toggle —
	// they're labels, not the glow). isFriend is reused by the Ignore/Friend
	// buttons below so the list isn't scanned twice per row.
	fk := p.showname
	if fk == "" {
		fk = p.name
	}
	isFriend, fColor, fNick := false, -1, ""
	if a.serverKey != "" && fk != "" {
		isFriend, fColor, fNick = a.d.Prefs.ServerFriendInfo(a.serverKey, fk)
	}
	friend := isFriend && a.d.Prefs.FriendHighlightOn() // row tint gate (unchanged)
	display := rosterName(p)                            // showname → OOC → character
	if isFriend && fNick != "" {
		display = fNick // your personal label for them (the character still shows after "·")
	}
	nameCol := ColText
	if isFriend && fColor >= 0 {
		nameCol = readableOnPanel(fColor) // custom colour, lifted so it stays legible on the dark panel
	}

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

	// Char icon (left), scaled with the zoom so it never overflows a short row.
	iconSz := playerIconSz * int32(a.playerPct) / 100
	iconR := sdl.Rect{X: row.X + 6, Y: row.Y + (row.H-iconSz)/2, W: iconSz, H: iconSz}
	a.drawPlayerIcon(p, idx, iconR, isSpec)

	// Right cluster: Pair + compact Copy buttons (mod also gets Copy-IPID).
	btnY := row.Y + (row.H-22)/2
	bx := row.X + row.W - 4
	// Pair / Copy-UID need a UID. The PR/PU live roster now carries it, so these
	// show on live rows too; the CharsCheck-only fallback has none (hence the gate).
	// Pair sends the server's "/pair <uid>" command — no popup, no char-slot guess.
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
		// Follow (M3, opt-in): trail this player across areas — auto-jump when they
		// move. Only shown when the player-list "Follow" toggle is on (OFF default);
		// a.followShow is read once per frame in drawPlayerList (no per-row lock).
		if a.followShow {
			fl := "Follow"
			if a.followUID == p.uid {
				fl = "Following"
			}
			fw := c.TextWidth(fl) + 14
			bx -= fw + 4
			fr := sdl.Rect{X: bx, Y: btnY, W: fw, H: 22}
			if c.Button(fr, fl) {
				a.toggleFollow(p.uid)
			}
			c.Tooltip(fr, "Follow this player: auto-jump to their area whenever they move. Click again to stop.")
		}
	} else if !isSpec && p.name != "" {
		// No live UID (CharsCheck-only roster, or a server without PR/PU UIDs): can't
		// direct-/pair, so offer the manual-UID popup instead (pre-filled from
		// /getarea when it confidently matched the character). Keeps a home for
		// click-to-pair now that the IC log's double-click selects text.
		pw := c.TextWidth("Pair…") + 14
		bx -= pw
		pr := sdl.Rect{X: bx, Y: btnY, W: pw, H: 22}
		if c.Button(pr, "Pair…") {
			a.openPairPopup(p.name)
		}
		c.Tooltip(pr, "Pair via the manual /pair UID box — this row has no live UID")
	}
	if p.ipid != "" { // IPID is mod-only server data — its presence IS the authorization (don't gate on local mod-detection)
		iw := c.TextWidth("IPID") + 12
		bx -= iw + 4
		if c.Button(sdl.Rect{X: bx, Y: btnY, W: iw, H: 22}, "IPID") {
			_ = sdl.SetClipboardText(p.ipid)
			a.warnLine = clampLine("Copied IPID for " + display)
			a.warnAt = a.now()
		}
	}
	// Add/remove friend (matches by showname, else character — the IC-log
	// highlight key, no UID needed). Shown for any named player; reuses isFriend
	// from the single lookup above, so the row isn't scanned twice.
	if fk != "" && a.serverKey != "" && a.d.Prefs.FriendButtonShown() {
		fl := "+ Friend"
		if isFriend {
			fl = "Unfriend" // friended state reads as the un-action (one click removes)
		}
		fw := c.TextWidth(fl) + 12
		bx -= fw + 4
		fr := sdl.Rect{X: bx, Y: btnY, W: fw, H: 22}
		if c.Button(fr, fl) {
			a.toggleServerFriend(fk)
		}
		c.Tooltip(fr, "Add this player to your friends — highlights their messages in the IC log. Click again to remove. Matches their showname, so it can be spoofed.")
	}
	// Ignore / Unignore (#81): block a player's IC + OOC. Same showname-else-char
	// key as friends (all the MS wire carries). Always shown for a named player —
	// it's a moderation-of-your-own-view tool, independent of the friend button.
	if fk != "" && a.serverKey != "" {
		ignored := a.d.Prefs.ServerIgnoreMatch(a.serverKey, fk)
		il := "Ignore"
		if ignored {
			il = "Unignore"
		}
		iw := c.TextWidth(il) + 12
		bx -= iw + 4
		ir := sdl.Rect{X: bx, Y: btnY, W: iw, H: 22}
		if c.Button(ir, il) {
			a.toggleServerIgnore(fk)
		}
		c.Tooltip(ir, "Ignore this player — drops their IC and OOC messages (no log line, no sprite, no blip). Click again to unignore; you can also manage the list in Settings.")
	}
	// Profile (#101): a card other AsyncAO players can set. Only rows we HAVE a
	// profile for show this (slice 1: your own; slice 2 adds remote players).
	if prof, ok := a.profileFor(p, isMe); ok {
		pw := c.TextWidth("Profile") + 12
		bx -= pw + 4
		pb := sdl.Rect{X: bx, Y: btnY, W: pw, H: 22}
		if c.Button(pb, "Profile") {
			a.openProfileCard(prof, display)
		}
		c.Tooltip(pb, "View this player's AsyncAO profile card")
	}

	// Text column between the icon and the button cluster.
	textX := row.X + 6 + iconSz + 8
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
	// Status chip (#M1): your own from a.myStatus, others from the room's per-character
	// status memory. RemoteStatus is a plain map read (0-alloc), safe per row per frame;
	// only a set status draws a chip, so the common (none) case costs nothing.
	st := courtroom.StatusNone
	if isMe {
		st = a.myStatus
	} else if a.room != nil {
		if rs, ok := a.room.RemoteStatus(p.name); ok {
			st = rs
		}
	}
	if lbl := statusLabel(st); lbl != "" {
		cx += a.drawRosterChip(cx, row.Y+4, lbl, statusColor(st), ColText) + 5
	}
	// Pair chip (#20, opt-in): who this player is currently paired with (⇄), from their IC messages.
	if a.pairStatusShow {
		if partner := a.pairPartnerOf(p); partner != "" {
			cx += a.drawRosterChip(cx, row.Y+4, "⇄ "+partner, chipPairColor, ColBackground) + 5
		}
	}
	ic := "[" + p.uid + "]  " + p.name
	if !strings.EqualFold(display, p.name) {
		ic = "[" + p.uid + "]  " + display + "  ·  " + p.name
	}
	c.LabelClippedFont(c.LogFontFor(a.playerPct, ic), cx, row.Y+5, textW-(cx-textX), ic, nameCol)
	// Line 2 — OOC name (+ IPID for mods), dimmer. Skip OOC when it's already the
	// display name above (no showname → OOC was promoted to the identity line).
	sub := ""
	if p.ooc != "" && p.showname != "" {
		sub = "OOC: " + p.ooc
	}
	if p.ipid != "" { // mod-only server data; show it whenever we actually have it
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

// chipPairColor is the pair-status chip's fill (#20) — a soft violet, distinct from the CM/SPEC/
// status chips so a paired marker reads at a glance.
var chipPairColor = sdl.Color{R: 168, G: 140, B: 224, A: 255}

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
		a.playerRowsSort == a.playerSort && a.playerRowsAreaSort == a.playerAreaSort &&
		a.playerRowsSpk == spk && a.playerRowsAt.Equal(a.rosterStamp()) &&
		a.playerRowsAreaAt.Equal(a.areaListAt) { // a /gas refresh changes the default area order
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
		if a.playerAreaSort == areaSortGas {
			a.applyGasAreaOrder(order, groups) // default: current area first, then the server's /gas order
		} else {
			sortAreaOrder(order, groups, a.playerAreaSort) // A→Z or most-populated
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
	a.playerRowsLen, a.playerRowsSort, a.playerRowsAreaSort, a.playerRowsSpk, a.playerRowsAt, a.playerRowsAreaAt =
		len(a.rosterView()), a.playerSort, a.playerAreaSort, spk, a.rosterStamp(), a.areaListAt
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
		}
		if c.ClickedIn(r) { // press+release in-row, so a drag-in release can't jump areas
			a.jumpToArea(hr.area)
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

// toggleServerFriend adds or removes a name from this server's friend list (the
// player-list "+ Friend" button). Friending matches by showname (else the
// character), the same key the IC-log highlight uses. Reuses the cloned list +
// SetServerFriends (which dedups, caps, and persists).
func (a *App) toggleServerFriend(name string) {
	if a.serverKey == "" || strings.TrimSpace(name) == "" {
		return
	}
	friends := a.d.Prefs.ServerFriends(a.serverKey)
	out := friends[:0]
	removed := false
	for _, f := range friends {
		fn := f
		if i := strings.IndexByte(f, '='); i >= 0 {
			fn = f[:i] // strip any "=RRGGBB" glow colour before matching
		}
		if strings.EqualFold(strings.TrimSpace(fn), name) {
			removed = true
			continue
		}
		out = append(out, f)
	}
	if !removed {
		out = append(out, name) // add as a plain entry (default glow)
	}
	a.d.Prefs.SetServerFriends(a.serverKey, out)
	if removed {
		a.warnLine = clampLine("Removed " + name + " from friends")
	} else {
		a.warnLine = clampLine("Added " + name + " to friends")
	}
	a.warnAt = a.now()
}

// toggleServerIgnore adds or removes a name from this server's ignore list (the
// player-list "Ignore" button). Matching mirrors friends (showname else
// character — the only identity the MS wire carries). Ignored players' IC and
// OOC messages are dropped at ingest (#81). Reuses the cloned list +
// SetServerIgnored (dedups, caps, persists).
func (a *App) toggleServerIgnore(name string) {
	if a.serverKey == "" || strings.TrimSpace(name) == "" {
		return
	}
	ignored := a.d.Prefs.ServerIgnored(a.serverKey)
	out := ignored[:0]
	removed := false
	for _, n := range ignored {
		if strings.EqualFold(strings.TrimSpace(n), name) {
			removed = true
			continue
		}
		out = append(out, n)
	}
	if !removed {
		out = append(out, name)
	}
	a.d.Prefs.SetServerIgnored(a.serverKey, out)
	if removed {
		a.warnLine = clampLine("Unignored " + name + " — their messages show again")
	} else {
		a.warnLine = clampLine("Ignoring " + name + " — hiding their IC + OOC")
	}
	a.warnAt = a.now()
}

// readableOnPanel returns a packed 0xRRGGBB colour as an sdl.Color guaranteed
// legible on the dark player panel (#82): a too-dark colour is lifted toward a
// minimum brightness with its hue preserved, so a custom friend colour can tint
// the name without it sinking into the background — the same readability concern
// the theme ink guard handles. rgb must be >= 0.
func readableOnPanel(rgb int) sdl.Color {
	const minLuma = 120
	r, g, b := int(uint8(rgb>>16)), int(uint8(rgb>>8)), int(uint8(rgb))
	luma := (299*r + 587*g + 114*b) / 1000 // Rec.601
	if luma < minLuma {
		if luma < 1 {
			return sdl.Color{R: minLuma, G: minLuma, B: minLuma, A: 255} // pure black → neutral grey
		}
		clamp := func(v int) uint8 {
			if v > 255 {
				return 255
			}
			return uint8(v)
		}
		r, g, b = int(clamp(r*minLuma/luma)), int(clamp(g*minLuma/luma)), int(clamp(b*minLuma/luma))
	}
	return sdl.Color{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
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
