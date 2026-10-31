package ui

// The Players tab, built from the /getarea snapshot the click-to-pair parser harvests.
// IPIDs are mod-only data shown in-session, never persisted.

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	playerRowH    = int32(44) // a char icon flanked by two text rows (IC identity, OOC) — actions live in the … menu, so rows never grow past the icon
	playerIconSz  = int32(38)
	playerHeaderH = int32(26) // a /gas area-group header row (name + count, click to jump)
	// currentAreaStatusStripH is the fixed height of the flat roster's current-area ARUP status line.
	currentAreaStatusStripH = int32(20)
)

// areaJumpFollowWindow is how long a jump keeps re-nudging the target area header into view.
// It must outlive the server round-trip that reorders the list, yet expire so a lost transfer cannot pin the scroll.
const areaJumpFollowWindow = 3 * time.Second

// Roster sort modes, cycled by the Sort button; they order players, flat or within each /gas area group.
const (
	playerSortUID      = iota // by client UID, numeric
	playerSortName            // by IC name (showname, else character)
	playerSortSpeaking        // whoever spoke most recently, first
	playerSortModes           // count, for the cycle
)

// Area-group sort modes, cycled by the Rooms button; the default keeps the server's /gas order.
const (
	areaSortGas   = iota // our current area first, then the server's /gas order — default
	areaSortName         // area name, A→Z
	areaSortPop          // most players first
	areaSortModes        // count, for the cycle
)

// clampMode keeps a remembered sort index inside [0, n), so a stale pref cannot select an out-of-range sort.
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

// sortAreaOrder orders area-group names in place for the A-Z and most-populated modes; ties keep the incoming order.
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

// applyGasAreaOrder orders headers for the default mode: our area, then the server's /gas order, then live-only areas.
// Runs only on a grouped-rows rebuild, so it adds nothing to the per-frame draw.
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

// rosterMenuThemed reports whether a themed music list is the list the roster's overflow menu acts on.
// The Volume-sliders row draws nothing there (drawMusicList) yet would persist the flip, so it must not be offered.
// It re-derives the answer from a.themeLay because drawPlayerList has three callers, two outside this panel.
// Not based on a.logTab: the canvas music list is the row's target whichever view is up.
func (a *App) rosterMenuThemed() bool {
	if !a.themeLay.valid || !a.d.Prefs.Load().ThemeLayoutOn {
		return false // classic geometry: this panel is chrome, and so is any torn panel
	}
	if a.panelHidden(panelLog) {
		return false // the themed panel that hosts the music list is not on screen
	}
	_, ok := a.themeLay.rect("music_list")
	return ok
}

// drawPlayerList renders the area roster, one row per UID, under a one-row header with per-row icon and actions.
func (a *App) drawPlayerList(r sdl.Rect) {
	// The user's font for this panel, if set; every control below follows it, so the panel changes as a whole.
	defer a.ctx.popPanelFont(a.ctx.pushPanelFont(a.elemChromeFont(elemPlayerList, a.playerPct)))
	c := a.ctx
	if a.playerPct < config.MinLogScalePercent { // uninit / stale → match the log
		a.playerPct = a.logPct
	}
	// Pull the /getarea snapshot when the live tab opens and on each area change; join/leave does not refresh it.
	if !a.rosterLegacy && a.sess != nil && a.liveDetailsArea != a.curArea {
		a.liveDetailsArea = a.curArea
		a.fetchRoster()
	}
	// The toolbar is one wrapping strip (playerlisttoolbar.go), which cannot overlap.
	// Labels are built here because they come from App state, keeping the planner pure.
	labels := plToolbarLabels{
		sort: "Sort: " + playerSortLabel(a.playerSort),
		// The status transmits on your next IC message; AsyncAO players see a chip on your row, other clients nothing.
		status: playerStatusButtonLabel(a.myStatus),
	}
	// Built only when the roster spans areas: a single-area list has nothing to order and would waste a concatenation.
	multiArea := a.rosterMultiArea()
	if multiArea {
		labels.rooms = "Rooms: " + areaSortLabel(a.playerAreaSort)
	}
	// Read here, not at the menu-side switches: drawPlayerRow needs them per row and the menu is usually closed.
	a.followShow, a.pairStatusShow = a.d.Prefs.Load().FollowEnabled, a.d.Prefs.Load().ShowPairStatus
	plan := planPlayerToolbar(r, labels, multiArea, c.TextWidth)
	for i := 0; i < plan.n; i++ {
		it := &plan.items[i]
		switch it.id {
		case plItemSort:
			if c.Button(it.r, it.label) {
				a.playerSort = (a.playerSort + 1) % playerSortModes
				{
					pv1 := a.playerSort
					a.d.Prefs.Update(func(sn *config.Settings) {
						sn. // remember it across sessions
							PlayerListSort = pv1
					})
				}
			}
		case plItemRooms:
			if c.Button(it.r, it.label) {
				a.playerAreaSort = (a.playerAreaSort + 1) % areaSortModes
				{
					pv2 := a.playerAreaSort
					a.d.Prefs.Update(func(sn *config.Settings) {
						sn.PlayerListAreaSort = // remember it across sessions
							pv2
					})
				}
			}
			c.Tooltip(it.r, "Order the area groups: /gas (the server's own order), A–Z, or most players first.")
		case plItemStatus:
			if c.Button(it.r, it.label) {
				a.myStatus = (a.myStatus + 1) % courtroom.StatusCount
			}
			c.Tooltip(it.r, "Set your status (AFK / Busy / Writing / LFRP). Other AsyncAO players see a chip on your row after your next message; standard clients are unaffected.")
		case plItemMenu:
			// Same helper as the Music header, click consumption included, or click-away closes the menu at once.
			// themed is derived: inside the theme's music_list, drawMusicList discards the Volume-sliders row.
			if a.musicIconButton(it.r, iconKebab, "Roster & playback options — refresh details, legacy snapshot, pair status, follow") {
				a.openMusicMenuFromButton(sdl.Point{X: it.r.X, Y: it.r.Y + it.r.H}, a.rosterMenuThemed())
			}
		}
	}
	r.Y += plan.h
	r.H -= plan.h

	// The flat single-area roster has no area header row to carry the ARUP status, so give it one fixed line.
	if !multiArea {
		a.drawCurrentAreaStatusStrip(sdl.Rect{X: r.X, Y: r.Y, W: r.W - scrollBarW - 6, H: currentAreaStatusStripH})
		r.Y += currentAreaStatusStripH
		r.H -= currentAreaStatusStripH
	}

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
		c.LabelClippedFont(a.elemFont(elemPlayerList, a.playerPct), r.X, r.Y+4, r.W, hint, ColTextDim)
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
		// WheelIn consumes the event, so read the delta once; a plain-wheel scroll disarms the area-jump follow.
		// Ctrl+wheel is text zoom, which changes row heights, so it does not disarm.
		if dy := c.WheelIn(r); dy != 0 {
			a.playerScroll -= dy * scrollStepPx
			a.areaJumpFollow = "" // user scrolled — stop pinning
		}
	}
	rowW := r.W - scrollBarW - 6
	contentH := int32(0)
	for i := range rows {
		contentH += a.rowHeight(rows[i])
	}
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.playerScroll = c.VScrollbar("playerlist", track, a.playerScroll, contentH, r.H)
	// Disarm on a thumb drag, not a value change: VScrollbar also clamps with no drag when the roster shrinks,
	// which would wrongly end the follow on the very reorder frame it must track.
	if c.dragID == "playerlist" {
		a.areaJumpFollow = "" // the user is dragging the scrollbar — user scroll wins over the follow
	}
	// Placed after the wheel and scrollbar disarms so a user scroll wins, after VScrollbar so the nudge sets the final
	// scroll, and before the draw loop reads playerScroll.
	a.applyAreaJumpFollow(rows, r.H)
	// While the profile popover is hovered, rows beneath ignore the pointer: a click must not press what is behind it.
	cardFenced := false
	if a.profileCardShow && pointIn(c.mouseX, c.mouseY, profileCardRect(r)) {
		c.fencePointer()
		cardFenced = true
	}
	clipPrev, clipHad := c.pushClip(r)
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
	c.popClip(clipPrev, clipHad)
	if cardFenced {
		c.unfencePointer() // the card's own pass (Close button) gets the real pointer
	}
	a.drawProfileCardOverlay(r) // #101: the profile popover sits on top of the list
}

// rowHeight is the display height of one roster row; player rows scale with the Players-tab text zoom.
func (a *App) rowHeight(row rosterRow) int32 {
	if row.header {
		return playerHeaderH
	}
	return playerRowH * int32(a.playerPct) / 100
}

// scrollAreaIntoView nudges playerScroll just enough to show area's header row; a missing header is a no-op.
func (a *App) scrollAreaIntoView(area string, rows []rosterRow, viewH int32) {
	if area == "" {
		return
	}
	top := int32(0) // content-space offset (pre-scroll) of the target header's top
	for i := range rows {
		if rows[i].header && rows[i].area == area {
			headerH := a.rowHeight(rows[i])
			if top < a.playerScroll {
				a.playerScroll = top // header is above the viewport — pull up to its top
			} else if top+headerH > a.playerScroll+viewH {
				a.playerScroll = top + headerH - viewH // below — pull down just enough to reveal it
			}
			if a.playerScroll < 0 {
				a.playerScroll = 0
			}
			return
		}
		top += a.rowHeight(rows[i])
	}
	// Not present as a header (flat roster or gone): leave the scroll alone.
}

// applyAreaJumpFollow re-nudges the target header each frame while armed, so the position after the reorder wins.
func (a *App) applyAreaJumpFollow(rows []rosterRow, viewH int32) {
	if a.areaJumpFollow == "" {
		return
	}
	if !a.now().Before(a.areaJumpFollowUntil) { // window elapsed (or a lost/rejected transfer) → stop pinning
		a.areaJumpFollow = ""
		a.areaJumpFollowUntil = time.Time{}
		return
	}
	a.scrollAreaIntoView(a.areaJumpFollow, rows, viewH)
}

// joinFlashColor is the amber "just joined" row tint, faded out over joinFlashWindow.
var joinFlashColor = sdl.Color{R: 240, G: 200, B: 90, A: 255}

func (a *App) drawPlayerRow(idx int, row sdl.Rect, myUID, speaker string, cmSet map[string]bool) {
	c := a.ctx
	p := &a.rosterView()[idx]
	isSpec := strings.EqualFold(p.name, "Spectator")
	isMe := myUID != "" && p.uid == myUID
	isSpeaker := speaker != "" && (strings.EqualFold(p.showname, speaker) || strings.EqualFold(p.name, speaker))
	isCM := cmSet != nil && (cmSet[strings.ToLower(p.showname)] || cmSet[strings.ToLower(p.name)])
	// Friend lookup, once: drives the tint, nickname and name colour regardless of the toggle; reused by the buttons.
	fk := p.showname
	if fk == "" {
		fk = p.name
	}
	isFriend, fColor, fNick := false, -1, ""
	if a.serverKey != "" && fk != "" {
		isFriend, fColor, fNick = a.d.Prefs.Load().ServerFriendInfo(a.serverKey, fk)
	}
	friend := isFriend && a.d.Prefs.Load().FriendHighlight // row tint gate (unchanged)
	display := rosterName(p)                               // showname → OOC → character
	if isFriend && fNick != "" {
		display = fNick // your personal label for them (the character still shows after "·")
	}
	nameCol := ColText
	if isFriend && fColor >= 0 {
		nameCol = readableOnPanel(fColor) // custom colour, lifted so it stays legible on the dark panel
	}

	// Background, role tint (you > speaking > friend) and a left accent bar.
	c.Fill(row, ColPanel)
	switch {
	case isMe:
		c.Fill(row, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 46})
	case isSpeaker:
		c.Fill(row, sdl.Color{R: ColTierGreen.R, G: ColTierGreen.G, B: ColTierGreen.B, A: 46})
	case friend:
		c.Fill(row, friendTintColor)
	}
	// A brief, fading amber tint when this UID just appeared in the live roster.
	if p.uid != "" {
		if t, ok := a.joinFlash[p.uid]; ok && !t.IsZero() {
			if el := a.now().Sub(t); el >= 0 && el < joinFlashWindow {
				fa := uint8(70 * int64(joinFlashWindow-el) / int64(joinFlashWindow))
				c.Fill(row, sdl.Color{R: joinFlashColor.R, G: joinFlashColor.G, B: joinFlashColor.B, A: fa})
				a.NoteAnimating() // the tint fades on the wall clock: keep frames coming so it eases out instead of freezing at idle=0
			}
		}
	}
	if bar, ok := rosterBarColor(isMe, isSpeaker, friend); ok {
		c.Fill(sdl.Rect{X: row.X, Y: row.Y, W: 3, H: row.H}, bar)
	}
	if c.hovering(row) {
		c.Border(row, ColPanelHi)
	}

	// Scaled with the zoom so the icon never overflows a short row.
	iconSz := playerIconSz * int32(a.playerPct) / 100
	iconR := sdl.Rect{X: row.X + 6, Y: row.Y + (row.H-iconSz)/2, W: iconSz, H: iconSz}
	a.drawPlayerIcon(p, idx, iconR, isSpec)

	// One trigger: single-player actions live in its popup menu (rostermenu.go);
	// right-clicking the row is the shortcut.
	bx := row.X + row.W - 4
	if hasRosterMenu(p) {
		bx -= rosterMenuBtnW
		mb := sdl.Rect{X: bx, Y: row.Y + (row.H-22)/2, W: rosterMenuBtnW, H: 22}
		if c.Button(mb, "…") {
			a.openRosterMenu(p, isMe, isSpec, sdl.Point{X: mb.X + mb.W, Y: mb.Y + mb.H + 2})
			c.clicked = false // the opening click is the menu's, not a later widget's
		}
		c.Tooltip(mb, "Actions for this player (Message, Pair, Copy, Friend, Ignore…) — right-clicking the row works too")
		if c.rightClicked && c.hovering(row) {
			a.openRosterMenu(p, isMe, isSpec, sdl.Point{X: c.mouseX, Y: c.mouseY})
			c.rightClicked = false
		}
	}

	textX := row.X + 6 + iconSz + 8
	textW := bx - textX - 8
	if textW < 40 {
		textW = 40
	}
	cx := textX
	if isCM {
		cx += a.drawRosterChip(cx, row.Y+4, "CM", chipCMColor, ColBackground) + 5
	}
	if isSpec {
		cx += a.drawRosterChip(cx, row.Y+4, "SPEC", chipSpecColor, ColText) + 5
	}
	// This player sent the zero-width channel, which AO2 and webAO never emit; a hint, not proof (it can be faked).
	if a.room != nil && (isMe || a.room.RemoteIsAsyncAO(p.name)) {
		cx += a.drawRosterChip(cx, row.Y+4, "AO", ColAccent, ColBackground) + 5
	}
	// Yours from a.myStatus, others from the room's per-character memory; only a set status draws a chip.
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
	// Pair chip: who this player is paired with, from their IC messages.
	if a.pairStatusShow {
		if partner := a.pairPartnerOf(p); partner != "" {
			cx += a.drawRosterChip(cx, row.Y+4, "⇄ "+partner, chipPairColor, ColBackground) + 5
		}
	}
	ic := "[" + p.uid + "]  " + p.name
	if !strings.EqualFold(display, p.name) {
		ic = "[" + p.uid + "]  " + display + "  ·  " + p.name
	}
	// a.playerPct is the per-panel zoom on top of the per-element font.
	// labelEmoji, not LabelClippedFont: a server-supplied name may need more than one face, or it renders as tofu.
	rosterEmoji := a.elemEmoji(elemPlayerList, a.playerPct)
	a.labelEmoji(a.elemFontFor(elemPlayerList, a.playerPct, ic), rosterEmoji, cx, row.Y+5, textW-(cx-textX), ic, nameCol)
	// OOC name, plus IPID for mods; skip OOC when it was promoted to the display name above.
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
		// As on the identity line: the OOC name is the player's own text.
		a.labelEmoji(a.elemFontFor(elemPlayerList, a.playerPct, sub), rosterEmoji, textX, row.Y+row.H-int32(16*a.playerPct/100), textW, sub, ColTextDim)
	}
}

// drawPlayerIcon paints a row's character icon through the char grid's demand pipeline; spectators get a placeholder.
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

// chipPairColor is the pair-status chip's fill, distinct from the CM, SPEC and status chips.
var chipPairColor = sdl.Color{R: 168, G: 140, B: 224, A: 255}

// drawRosterChip draws a small filled pill and returns its width so the caller can advance the cursor.
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

// currentSpeakerName is the lowercased name of the latest IC line; "" when there is none or it was a system line.
func (a *App) currentSpeakerName() string {
	if n := len(a.icLog); n > 0 {
		return strings.ToLower(strings.TrimSpace(a.icLog[n-1].speaker))
	}
	return ""
}

// areaInfoByName returns the live ARUP record for the named area, matched by index against a.sess.Areas.
// ok is false with no session, no such name, or an index out of range for AreaInfo (partial snapshot mid-connect).
func (a *App) areaInfoByName(name string) (*courtroom.AreaInfo, bool) {
	if a.sess == nil || len(a.sess.AreaInfo) == 0 {
		return nil, false
	}
	for i, nm := range a.sess.Areas {
		if nm == name {
			if i >= len(a.sess.AreaInfo) {
				return nil, false
			}
			return &a.sess.AreaInfo[i], true
		}
	}
	return nil, false
}

// curAreaPlayers returns the live ARUP head-count for the area we are in; ok=false when unknown.
func (a *App) curAreaPlayers() (int, bool) {
	if a.sess == nil || len(a.sess.AreaInfo) == 0 {
		return 0, false
	}
	var info *courtroom.AreaInfo
	if a.curArea == "" {
		info = &a.sess.AreaInfo[0] // fresh join, no area click yet: assume the spawn area (index 0)
	} else {
		var ok bool
		if info, ok = a.areaInfoByName(a.curArea); !ok { // navigated, but the name didn't match the list
			return 0, false
		}
	}
	if info.Players < 0 { // -1 = server hasn't reported it
		return 0, false
	}
	return info.Players, true
}

// cmNameSet is the lowercased set of case-master names across all areas, excluding ""/FREE; nil when there are none.
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

// playerRosterOrder returns the display order (indices into areaPlayers), recomputed on roster, sort or speaker change.
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

// sortRosterIdxs orders indices into areaPlayers in place by the current sort mode.
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

// rosterRow is one display row: a /gas area-group header (name and count) or a player (index into areaPlayers).
type rosterRow struct {
	header bool
	area   string // header: the area name
	count  int    // header: players in the group
	idx    int    // player: index into areaPlayers
}

// rosterMultiArea reports whether the roster spans two or more named areas, so the list groups by area.
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

// playerRosterRows is the memoized display: flat for a single area, an area header before each group for a /gas.
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

// areaCurrentDefault is the stock current-area highlight; Settings recolours it.
var areaCurrentDefault = sdl.Color{R: 74, G: 201, B: 108, A: 255}

// areaHighlightWashAlpha tints the whole current-area row with the highlight colour at this alpha.
const areaHighlightWashAlpha = 56

// areaHighlightCol resolves the current-area colour: the user's hex pref, or the stock green when unset or malformed.
func (a *App) areaHighlightCol() sdl.Color {
	if c, ok := parse.HexColor(a.d.Prefs.Load().AreaHighlightHex); ok {
		col := sdl.Color(c)
		return col
	}
	return areaCurrentDefault
}

// drawAreaHeaderRow draws a /gas area-group header, clickable to jump there; inert with no live session.
func (a *App) drawAreaHeaderRow(hr rosterRow, r sdl.Rect) {
	c := a.ctx
	c.Fill(r, ColPanelHi)
	name := hr.area
	if name == "" {
		name = "(unnamed area)"
	}
	// The current area floats to the top of the /gas order, so mark it with a row wash, edge bar and "(current)".
	current := a.sess != nil && hr.area != "" && hr.area == a.myAreaName()
	nameCol := ColText
	switch {
	case current:
		hi := a.areaHighlightCol()
		c.Fill(r, sdl.Color{R: hi.R, G: hi.G, B: hi.B, A: areaHighlightWashAlpha})
		nameCol = hi
		c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: 3, H: r.H}, hi)
		c.LabelClipped(r.X+r.W-120, r.Y+6, 116, "(current)", hi)
	case a.sess != nil && hr.area != "":
		if c.hovering(r) {
			c.Border(r, ColAccent)
		}
		if c.ClickedIn(r) { // press+release in-row, so a drag-in release can't jump areas
			a.jumpToArea(hr.area)
		}
		c.LabelClipped(r.X+r.W-120, r.Y+6, 116, "click to jump →", ColTextDim)
	}
	// The header is part of the roster, so it takes the roster's font.
	font := a.elemFont(elemPlayerList, a.playerPct)
	base := name + "  ·  " + strconv.Itoa(hr.count) + " player(s)"
	maxW := r.W - 132
	c.LabelClippedFont(font, r.X+8, r.Y+6, maxW, base, nameCol)

	// Looked up every frame, never memoized with playerRosterRows: its key misses areaInfoSeq, so an ARUP-only change
	// such as a lock would leave a stale string. The status text and colours are the Areas tab's.
	if info, ok := a.areaInfoByName(hr.area); ok {
		if extra := arupStatusText(info); extra != "" {
			extraCol := ColTextDim
			switch {
			case strings.EqualFold(info.Lock, "LOCKED"):
				extraCol = areaLockedBorder
			case info.Status != "" && !strings.EqualFold(info.Status, "IDLE"):
				extraCol = areaStatusColor(info.Status)
			}
			if usedW, ok := c.fontTextWidth(font, base); ok && usedW < maxW {
				c.LabelClippedFont(font, r.X+8+usedW, r.Y+6, maxW-usedW, extra, extraCol)
			}
		}
	}
}

// drawCurrentAreaStatusStrip draws the flat roster's ARUP status line, read live as drawAreaHeaderRow explains.
// Blank with no session, a name mismatch mid-transfer, or nothing reported yet.
func (a *App) drawCurrentAreaStatusStrip(r sdl.Rect) {
	c := a.ctx
	info, ok := a.areaInfoByName(a.myAreaName())
	if !ok {
		return
	}
	extra := arupStatusText(info)
	if extra == "" {
		return
	}
	col := ColTextDim
	switch {
	case strings.EqualFold(info.Lock, "LOCKED"):
		col = areaLockedBorder
	case info.Status != "" && !strings.EqualFold(info.Status, "IDLE"):
		col = areaStatusColor(info.Status)
	}
	// arupStatusText's leading separator follows a headline; alone on this strip it would be an orphaned dot.
	text := strings.TrimPrefix(extra, "  ·  ")
	c.LabelClippedFont(a.elemFont(elemPlayerList, a.playerPct), r.X, r.Y+3, r.W, text, col)
}

// jumpToArea transfers us to area by name through the music list (MC#<area name>#<char id>).
// The optimistic local updates live here so that every jump entry point moves the local selection identically.
func (a *App) jumpToArea(area string) {
	if a.sess == nil || area == "" {
		return
	}
	// Keep the header in view until expiry, so the Players tab follows the row after the server reorders the roster.
	a.areaJumpFollow = area
	a.areaJumpFollowUntil = a.now().Add(areaJumpFollowWindow)
	a.switchAreaScrollback(area) // per-area IC log (opt-in) — before curArea moves (it keys the outgoing log on the OLD curArea)
	a.curArea = area             // optimistic local selection (Rich Presence, best-effort — myAreaName still prefers the server echo)
	a.updatePresence()
	a.sess.RequestMusic(area)
	// Wipe the stage on our own jump: Athena/Nyathena send a one-field BN, which the reducer's arity test would
	// not wipe on, leaving the old sprite and chatbox up. Server-pushed moves still rely on BN arity, like AO2.
	if a.room != nil {
		a.room.WipeStage()
	}
	a.wtceName, a.testimonyOn, a.evShowImg = "", false, ""
	a.sess.LastIC = nil // ...so tabbing back can't re-stage the area we just left
	a.warnLine = clampLine("Jumping to " + area + "…")
	a.warnAt = a.now()
}

// toggleServerFriend adds or removes a name from this server's friends, matched by showname, else character.
func (a *App) toggleServerFriend(name string) {
	if a.serverKey == "" || strings.TrimSpace(name) == "" {
		return
	}
	friends := a.d.Prefs.Load().ServerFriends(a.serverKey)
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

// toggleServerIgnore adds or removes a name from this server's ignore list, matched by showname, else character.
func (a *App) toggleServerIgnore(name string) {
	if a.serverKey == "" || strings.TrimSpace(name) == "" {
		return
	}
	ignored := a.d.Prefs.Load().ServerIgnored(a.serverKey)
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

// readableOnPanel returns a packed 0xRRGGBB colour lifted to a minimum brightness, hue kept; rgb must be >= 0.
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

// rosterName is the recognisable display name: showname, else OOC name, else character.
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

// uidLess orders two UID strings numerically, falling back to lexical for a non-numeric id.
func uidLess(x, y string) bool {
	nx, ex := strconv.Atoi(x)
	ny, ey := strconv.Atoi(y)
	if ex == nil && ey == nil {
		return nx < ny
	}
	return x < y
}
