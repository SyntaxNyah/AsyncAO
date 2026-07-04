package ui

// The player-row action menu (playtest redesign): the per-row button cluster
// (Pair / UID / IPID / Follow / Friend / Ignore / Profile) crowded the rows —
// at higher text zoom it landed ON the OOC/IPID line — so every single-player
// action now lives in one anchored popup, opened by the row's "…" button or by
// right-clicking the row (the shortcut; the button keeps it discoverable).
// Rows go back to clean two-lane height. The item list is data-driven so
// future single-target actions (mod/CM commands) slot in as new kinds.
//
// Input discipline: while open the menu holds the kit's modal fence (modalOn),
// so everything underneath — rows, lists, wheel — is pointer-blind on the
// whole screen; the menu itself hit-tests with raw pointIn, exactly like the
// emoji picker and the open-dropdown list. Esc closes via closeTopOverlay.

import (
	"github.com/veandco/go-sdl2/sdl"
)

// rosterMenuKind enumerates the single-player actions the menu can carry.
type rosterMenuKind int

const (
	rosterActMessage    rosterMenuKind = iota // open a DM thread in the Group Chat panel
	rosterActPair                             // direct "/pair <uid>"
	rosterActPairManual                       // no live UID → the manual-UID pair popup
	rosterActFollow                           // start/stop trailing this player's area moves
	rosterActCopyUID
	rosterActCopyIPID
	rosterActFriend // add/remove friend (showname-else-char key)
	rosterActIgnore // ignore/unignore (same key)
	rosterActProfile
)

// rosterMenuItem is one row of the open menu: a kind plus its resolved label
// (state-dependent labels — Unfollow/Unfriend/Unignore — resolve at OPEN; any
// action closes the menu, so a label can never go stale on screen).
type rosterMenuItem struct {
	kind  rosterMenuKind
	label string
}

const (
	rosterMenuBtnW  = int32(28) // the per-row "…" trigger
	rosterMenuItemH = int32(26)
	rosterMenuPadX  = int32(10)
	rosterMenuMinW  = int32(150)
)

// hasRosterMenu reports whether a row gets the "…" trigger at all. Deliberately
// loose (any identified row): the open call builds the real item list, and
// every identified row has at least the ignore/friend pair or a copyable UID.
func hasRosterMenu(p *areaPlayer) bool {
	return p.uid != "" || p.name != ""
}

// openRosterMenu snapshots the row's identity, builds the applicable items,
// and opens the menu with its top-left preferred at `at` (the draw clamps and
// flips to stay on screen). The snapshot copies the areaPlayer VALUE: the
// roster can refresh/reorder while the menu is open, so acting through a live
// index could hit the wrong player.
func (a *App) openRosterMenu(p *areaPlayer, isMe, isSpec bool, at sdl.Point) {
	items := a.rosterMenuItems[:0]
	fk := p.showname
	if fk == "" {
		fk = p.name
	}
	// Message (PM): anything that targets one player belongs here (playtest) —
	// no friending required. Spectators are excluded: DM threads key on the
	// character name, and "Spectator" is ambiguous across clients.
	if !isSpec && p.name != "" && a.sess != nil {
		items = append(items, rosterMenuItem{rosterActMessage, "Message (PM)"})
	}
	if !a.panelHidden(rosterBtnPair) {
		switch {
		case p.uid != "":
			items = append(items, rosterMenuItem{rosterActPair, "Pair"})
		case !isSpec && p.name != "":
			items = append(items, rosterMenuItem{rosterActPairManual, "Pair… (enter UID)"})
		}
	}
	// Follow rides the header's Follow toggle exactly like the old per-row
	// button (OFF default: no follow affordances anywhere).
	if a.followShow && p.uid != "" {
		fl := "Follow"
		if a.followUID == p.uid {
			fl = "Unfollow"
		}
		items = append(items, rosterMenuItem{rosterActFollow, fl})
	}
	if p.uid != "" && !a.panelHidden(rosterBtnUID) {
		items = append(items, rosterMenuItem{rosterActCopyUID, "Copy UID"})
	}
	// IPID is mod-only server data — its presence IS the authorization.
	if p.ipid != "" && !a.panelHidden(rosterBtnIPID) {
		items = append(items, rosterMenuItem{rosterActCopyIPID, "Copy IPID"})
	}
	if fk != "" && a.serverKey != "" && a.d.Prefs.FriendButtonShown() {
		fl := "+ Friend"
		if isF, _, _ := a.d.Prefs.ServerFriendInfo(a.serverKey, fk); isF {
			fl = "Unfriend"
		}
		items = append(items, rosterMenuItem{rosterActFriend, fl})
	}
	if fk != "" && a.serverKey != "" && !a.panelHidden(rosterBtnIgnore) {
		il := "Ignore"
		if a.d.Prefs.ServerIgnoreMatch(a.serverKey, fk) {
			il = "Unignore"
		}
		items = append(items, rosterMenuItem{rosterActIgnore, il})
	}
	if _, ok := a.profileFor(p, isMe); ok && !a.panelHidden(rosterBtnProfile) {
		items = append(items, rosterMenuItem{rosterActProfile, "Profile"})
	}
	a.rosterMenuItems = items
	if len(items) == 0 {
		return // nothing applicable (every action hidden in the UI… popup)
	}
	a.rosterMenuP = *p
	a.rosterMenuMe = isMe
	a.rosterMenuAt = at
	a.rosterMenuTab = a.activeTab
	a.rosterMenuOpen = true
}

// rosterMenuFence holds/releases the kit's modal fence for the open menu, and
// force-closes it when its surface is gone (left the courtroom, switched tabs
// — the snapshot must never act on another session). Same set/RELEASE
// discipline as emojiPickerFence: an un-released modalOn freezes the UI.
func (a *App) rosterMenuFence(c *Ctx) {
	live := a.screen == ScreenCourtroom && !a.gifExporting && !a.replaying &&
		!a.makerOpen && a.activeTab == a.rosterMenuTab
	if a.rosterMenuOpen && !live {
		a.rosterMenuOpen = false
	}
	if a.rosterMenuOpen {
		c.modalOn = true
		a.rosterMenuFenceOn = true
	} else if a.rosterMenuFenceOn {
		c.modalOn = false // menu just closed → release the persistent fence
		a.rosterMenuFenceOn = false
	}
}

// rosterMenuRect is the open menu's clamped on-screen panel.
func (a *App) rosterMenuRect(w, h int32) sdl.Rect {
	c := a.ctx
	mw := rosterMenuMinW
	for _, it := range a.rosterMenuItems {
		if lw := c.TextWidth(it.label) + 2*rosterMenuPadX; lw > mw {
			mw = lw
		}
	}
	mh := int32(len(a.rosterMenuItems))*rosterMenuItemH + 8
	x := clampI32(a.rosterMenuAt.X, pad, maxI32(pad, w-mw-pad))
	y := clampI32(a.rosterMenuAt.Y, pad, maxI32(pad, h-mh-pad))
	return sdl.Rect{X: x, Y: y, W: mw, H: mh}
}

// drawRosterMenu paints the open menu and resolves its interaction (raw
// pointIn — the modal fence blanks hovering() everywhere, including here).
// Any item click acts on the snapshot and closes; a click elsewhere closes.
func (a *App) drawRosterMenu(w, h int32) {
	if !a.rosterMenuOpen {
		return
	}
	c := a.ctx
	panel := a.rosterMenuRect(w, h)
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)
	y := panel.Y + 4
	for i := range a.rosterMenuItems {
		it := &a.rosterMenuItems[i]
		row := sdl.Rect{X: panel.X + 2, Y: y, W: panel.W - 4, H: rosterMenuItemH - 2}
		if pointIn(c.mouseX, c.mouseY, row) {
			c.Fill(row, ColPanelHi)
			if c.clicked {
				a.rosterMenuAct(it.kind)
				a.rosterMenuOpen = false
				c.clicked = false
				return
			}
		}
		c.LabelClipped(row.X+rosterMenuPadX-2, row.Y+4, row.W-2*rosterMenuPadX+4, it.label, ColText)
		y += rosterMenuItemH
	}
	// Click-away (or a fresh right-click) closes; the fence already kept the
	// press from reaching anything underneath.
	if (c.clicked || c.rightClicked) && !pointIn(c.mouseX, c.mouseY, panel) {
		a.rosterMenuOpen = false
		c.clicked = false
		c.rightClicked = false
	}
}

// rosterMenuAct performs one menu action against the opened row's snapshot.
func (a *App) rosterMenuAct(kind rosterMenuKind) {
	p := &a.rosterMenuP
	fk := p.showname
	if fk == "" {
		fk = p.name
	}
	display := rosterName(p)
	switch kind {
	case rosterActMessage:
		// Open the Group Chat panel straight onto this player's DM thread
		// (threads key by character name; sendDirectMessage resolves the live
		// UID per send). Works for non-AsyncAO players too — it rides the
		// server's /pm, they just reply through OOC.
		a.showMessages = true
		a.msgSel = p.name
		a.msgSelGroup = 0
		a.msgGroupManage = false
		a.ctx.FocusField("msgcompose")
	case rosterActPair:
		a.queueOOCLines([]string{"/pair " + p.uid}) // we have the UID — no popup needed
		a.warnLine = clampLine("Sent /pair " + p.uid + " — " + display)
		a.warnAt = a.now()
	case rosterActPairManual:
		a.openPairPopup(p.name)
	case rosterActFollow:
		a.toggleFollow(p.uid)
	case rosterActCopyUID:
		_ = sdl.SetClipboardText(p.uid)
		a.warnLine = clampLine("Copied UID " + p.uid)
		a.warnAt = a.now()
	case rosterActCopyIPID:
		_ = sdl.SetClipboardText(p.ipid)
		a.warnLine = clampLine("Copied IPID for " + display)
		a.warnAt = a.now()
	case rosterActFriend:
		a.toggleServerFriend(fk)
	case rosterActIgnore:
		a.toggleServerIgnore(fk)
	case rosterActProfile:
		if prof, ok := a.profileFor(p, a.rosterMenuMe); ok {
			a.openProfileCard(prof, display)
		}
	}
}

// maxI32 is the int32 max (the kit has clampI32 but no bare max helper).
func maxI32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
