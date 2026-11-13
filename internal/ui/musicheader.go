package ui

// The Music panel's header row: the planner and the overflow menu.
// One left-to-right cursor, so controls can't overlap: Ctx.Button doesn't consume clicks, so one click on two
// overlapped buttons fires both.
// AO2-Client keeps these actions and its playback flags in the music list's right-click menu (courtroom.cpp:5821-5875),
// so here the common actions are icon buttons and the rest moves to an overflow menu that right-clicking the list also
// opens.
// Icons are vector (drawToolIcon), not font glyphs: LogFont's primary face isn't guaranteed to cover Geometric Shapes.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

const (
	// musicIconPx is one square icon button, equal to plStripRowH so controls on the row share a baseline.
	musicIconPx = plStripRowH
	// musicNameMinPx is the narrowest the now-playing readout is drawn at; below it the planner drops it.
	musicNameMinPx = int32(56)
	// musicSearchMinPx is the narrowest the search field stays usable at.
	musicSearchMinPx = int32(70)
	// musicNothingPlaying is AsyncAO's own empty-state text (AO2 leaves ui_music_name blank), so music_name_color
	// doesn't apply.
	musicNothingPlaying = "Nothing playing"
	// musicNowPrefix fronts the readout when something is playing.
	musicNowPrefix = "Now playing: "

	// musicHeaderMaxLines bounds the header's wrap: the action row and search row. A third would eat the track list.
	musicHeaderMaxLines = 2
)

// The header's drop order, highest first; see planMusicHeader.
const (
	// musicDropOnlyRoute: dropping this control removes the ONLY route to its action.
	musicDropOnlyRoute uint8 = iota + 1
	// musicDropShortcut: the action is also a row of the ⋮ menu.
	musicDropShortcut
	// musicDropSearch: filtering is a convenience over a list that is still readable.
	musicDropSearch
	// musicDropReadout: pure information, no action lost.
	musicDropReadout
)

// fontLineH is a face's line height, or 0 with no face: a *ttf.Font in this package can be nil, as on a headless Ctx.
func fontLineH(f *ttf.Font) int32 {
	if f == nil {
		return 0
	}
	return int32(f.Height())
}

// Header control ids. Ids rather than closures keep a plan a plain value with no allocation.
const (
	musicItemStop = iota + 1
	musicItemRandom
	musicItemExpand
	musicItemCollapse
	musicItemMenu
	musicItemNowPlaying
	musicItemSearch
	musicItemCount
)

// musicHeaderMaxItems is the longest the header can get; fixed so the plan is a stack value.
const musicHeaderMaxItems = 8

// musicHeaderPlan is one frame's header; h is the vertical room it took.
type musicHeaderPlan struct {
	items [musicHeaderMaxItems]plToolbarItem
	n     int
	h     int32
}

func (p *musicHeaderPlan) add(id int, label string, r sdl.Rect) {
	if p.n >= musicHeaderMaxItems {
		return
	}
	p.items[p.n] = plToolbarItem{id: id, label: label, r: r}
	p.n++
}

// rect finds a placed control; ok=false means the panel was too small and the draw must skip it.
func (p *musicHeaderPlan) rect(id int) (sdl.Rect, bool) {
	for i := 0; i < p.n; i++ {
		if p.items[i].id == id {
			return p.items[i].r, true
		}
	}
	return sdl.Rect{}, false
}

// planMusicHeader lays out the header for one frame, in on-screen order: the four actions, the menu, the flexible
// readout, then the search row.
// hideSearchRow drops row two and its height, for a theme's own music_search rect (courtroom.cpp:219-222) or the volume
// view. nowPlayingExternal means a theme's music_display plate painted the name (courtroom.cpp:171), so only the
// readout goes.
// planChromeRow shrinks, wraps, then drops by priority, since a theme chooses this rect and a squeezed one is normal.
// Priorities follow reachability: ⋮ never drops, its rows have no other route inside a theme canvas; Stop and Random go
// first, also in the menu; Expand and Collapse go last of the four, in no menu; the readout loses no action.
func planMusicHeader(r sdl.Rect, now string, count string, hideSearchRow, nowPlayingExternal bool, measure func(string) int32) musicHeaderPlan {
	// A fixed array and an index, never a closure: this runs every frame and a capturing closure allocates.
	var want [musicHeaderMaxItems]chromeCtl
	n := 0
	want[n] = chromeCtl{id: musicItemStop, w: musicIconPx, min: musicIconPx, prio: musicDropShortcut}
	n++
	want[n] = chromeCtl{id: musicItemRandom, w: musicIconPx, min: musicIconPx, prio: musicDropShortcut}
	n++
	want[n] = chromeCtl{id: musicItemExpand, w: musicIconPx, min: musicIconPx, prio: musicDropOnlyRoute}
	n++
	want[n] = chromeCtl{id: musicItemCollapse, w: musicIconPx, min: musicIconPx, prio: musicDropOnlyRoute}
	n++
	want[n] = chromeCtl{id: musicItemMenu, w: musicIconPx, min: musicIconPx, prio: chromeCtlNeverDrop}
	n++
	if !nowPlayingExternal {
		want[n] = chromeCtl{id: musicItemNowPlaying, w: measure(now), min: musicNameMinPx, prio: musicDropReadout}
		n++
	}
	if !hideSearchRow {
		countW := measure(count)
		want[n] = chromeCtl{
			id: musicItemSearch, w: r.W - countW - plStripItemGapPx*2, min: musicSearchMinPx,
			prio: musicDropSearch, brk: true,
		}
		n++
		want[n] = chromeCtl{id: musicItemCount, w: countW, min: countW, prio: musicDropReadout}
		n++
	}
	plan := planChromeRow(r, want[:n], musicHeaderMaxLines)

	var p musicHeaderPlan
	for i := 0; i < plan.placed(); i++ {
		p.add(plan.slot[i].id, musicHeaderItemLabel(plan.slot[i].id, now, count), plan.slot[i].r)
	}
	p.h = plan.h
	return p
}

// musicHeaderItemLabel is a placed item's text; only the readout and count have any.
func musicHeaderItemLabel(id int, now, count string) string {
	switch id {
	case musicItemNowPlaying:
		return now
	case musicItemCount:
		return count
	}
	return ""
}

// musicMenuKind enumerates the rows of the list panel's overflow menu, shared by Music, Areas and the roster.
type musicMenuKind int

const (
	musicMenuSeparator  musicMenuKind = iota
	musicMenuVolumeView               // swap the track list for the volume sliders
	musicMenuFadeIn                   // MUSIC_EFFECT bits — see config.MusicFlag*
	musicMenuFadeOut
	musicMenuSyncPos
	musicMenuLoop // the INVERSE of NO_REPEAT (KFO spells it "Loop")
	musicMenuStop
	musicMenuRandom
	// The roster half.
	musicMenuRosterRefresh // one area-detail pull (rosterDetailCmd): UIDs, IPIDs, OOC names + Pair/Copy onto the rows
	musicMenuRosterLegacy  // the classic /getarea snapshot instead of the live PR/PU roster
	musicMenuPairStatus    // #20: show each player's current pair (⇄) on their row
	musicMenuFollow        // M3: give each row a Follow button
)

const (
	musicMenuItemH = int32(24)
	musicMenuSepH  = int32(7)
	musicMenuPadX  = int32(10)
	musicMenuMinW  = int32(200)
	// musicMenuGutterW reserves the check-mark column so every label shares a left edge.
	musicMenuGutterW = int32(18)
)

type musicMenuRow struct {
	kind  musicMenuKind
	label string
}

// musicMenuRows is the fixed row model, so opening the menu allocates nothing. Order follows AO2's context menu
// (courtroom.cpp:5822-5875). The roster block sits behind its own separator, and the area block is appended from
// areamenu.go, so adding a music row can't renumber it.
var musicMenuRows = append([]musicMenuRow{
	{musicMenuVolumeView, "Volume sliders"},
	{musicMenuSeparator, ""},
	{musicMenuFadeIn, "Fade in"},
	{musicMenuFadeOut, "Fade out previous"},
	{musicMenuSyncPos, "Synchronize position"},
	{musicMenuLoop, "Loop"},
	{musicMenuSeparator, ""},
	{musicMenuStop, "Stop current song"},
	{musicMenuRandom, "Play random song"},
	{musicMenuSeparator, ""},
	// Labels must explain themselves: Ctx.Tooltip no-ops under modalOn.
	{musicMenuRosterRefresh, "Refresh roster details"},
	{musicMenuRosterLegacy, "Legacy roster snapshot"},
	{musicMenuPairStatus, "Show each player's pair"},
	{musicMenuFollow, "Follow players"},
}, areaMenuRows[:]...)

// musicMenuRowLive reports whether a row is offered this frame. The volume view isn't offered in a theme's canvas,
// whose slider rects win. Roster rows are live everywhere; area rows only from the Areas view (areamenu.go).
func (a *App) musicMenuRowLive(k musicMenuKind, themed bool) bool {
	if isAreaMenuKind(k) {
		return a.areaMenuRowLive(k) // areamenu.go owns the AREA block
	}
	if k == musicMenuVolumeView {
		return !themed
	}
	return true
}

// musicMenuRowDrawn reports whether row i paints: it is live and, if a separator, some live item follows. A separator
// has no kind of its own, so this takes a block's divider down with the block.
func (a *App) musicMenuRowDrawn(i int, themed bool) bool {
	row := musicMenuRows[i]
	if !a.musicMenuRowLive(row.kind, themed) {
		return false
	}
	if row.kind != musicMenuSeparator {
		return true
	}
	for _, next := range musicMenuRows[i+1:] {
		if next.kind == musicMenuSeparator {
			return false // the run to this divider's right is empty; the next one owns it
		}
		if a.musicMenuRowLive(next.kind, themed) {
			return true
		}
	}
	return false
}

// musicMenuRowEnabled reports whether a live row can be acted on. The playback flags ride an outgoing MC's last field,
// which AO2 appends only if the server advertised `effects` (courtroom.cpp:5806-5819), so they grey out otherwise.
func (a *App) musicMenuRowEnabled(k musicMenuKind) bool {
	if isAreaMenuKind(k) {
		return a.areaMenuRowEnabled(k) // areamenu.go owns the AREA block
	}
	switch k {
	case musicMenuFadeIn, musicMenuFadeOut, musicMenuSyncPos, musicMenuLoop:
		return a.musicEffectsSupported()
	case musicMenuRosterRefresh:
		// The pull is an OOC command, so it needs a session.
		return a.sess != nil
	}
	return true
}

// musicMenuRowChecked reports a toggle row's state. Loop is the inverse of NO_REPEAT.
func (a *App) musicMenuRowChecked(k musicMenuKind) bool {
	if isAreaMenuKind(k) {
		return a.areaMenuRowChecked(k) // areamenu.go owns the AREA block
	}
	switch k {
	case musicMenuVolumeView:
		return a.musicVolMode
	case musicMenuRosterLegacy:
		return a.rosterLegacy
	case musicMenuPairStatus:
		return a.d.Prefs.Load().ShowPairStatus
	case musicMenuFollow:
		return a.d.Prefs.Load().FollowEnabled
	}
	flags := a.d.Prefs.Load().MusicFlags()
	switch k {
	case musicMenuFadeIn:
		return flags&config.MusicFlagFadeIn != 0
	case musicMenuFadeOut:
		return flags&config.MusicFlagFadeOut != 0
	case musicMenuSyncPos:
		return flags&config.MusicFlagSyncPos != 0
	case musicMenuLoop:
		return flags&config.MusicFlagNoRepeat == 0
	}
	return false
}

// musicMenuRowIsToggle reports whether acting on a row leaves the menu open. Qt closes on every click; we don't,
// because setting several playback flags in a row is what this menu is for.
func musicMenuRowIsToggle(k musicMenuKind) bool {
	if isAreaMenuKind(k) {
		return areaMenuRowIsToggle(k) // areamenu.go owns the AREA block
	}
	switch k {
	case musicMenuVolumeView, musicMenuFadeIn, musicMenuFadeOut, musicMenuSyncPos, musicMenuLoop,
		musicMenuRosterLegacy, musicMenuPairStatus, musicMenuFollow:
		return true
	}
	return false
}

// musicEffectsSupported reports whether this server can carry the MUSIC_EFFECT bitmask.
func (a *App) musicEffectsSupported() bool {
	return a.sess != nil && a.sess.Features.Has(protocol.FeatureEffects)
}

// openMusicMenu opens the menu at `at` (the draw clamps it). The tab is stored so a popup can't survive a tab switch,
// and themed because the menu paints after the panel has returned.
func (a *App) openMusicMenu(at sdl.Point, themed bool) {
	a.musicMenuAt = at
	a.musicMenuTab = a.activeTab
	a.musicMenuThemed = themed
	a.musicMenuOpen = true
}

// openMusicMenuFromButton opens the menu and consumes the press: musicIconButton never clears c.clicked, and the panel
// anchors below the button, so drawMusicMenu's click-away would close it the frame it opened.
func (a *App) openMusicMenuFromButton(at sdl.Point, themed bool) {
	a.openMusicMenu(at, themed)
	a.ctx.clicked = false // the opening click is the menu's, not the click-away's
}

// musicMenuFence holds and releases c.modalOn for the open menu, and closes it when its surface is gone. A stranded
// modalOn freezes the UI.
func (a *App) musicMenuFence(c *Ctx) {
	live := a.screen == ScreenCourtroom && !a.gifExporting && !a.replaying &&
		!a.makerOpen && a.activeTab == a.musicMenuTab
	if a.musicMenuOpen && !live {
		a.musicMenuOpen = false
	}
	if a.musicMenuOpen {
		c.modalOn = true
		a.musicMenuFenceOn = true
	} else if a.musicMenuFenceOn {
		c.modalOn = false
		a.musicMenuFenceOn = false
	}
}

func (a *App) musicMenuHeight(themed bool) int32 {
	h := int32(8)
	for i, row := range musicMenuRows {
		if !a.musicMenuRowDrawn(i, themed) {
			continue
		}
		if row.kind == musicMenuSeparator {
			h += musicMenuSepH
			continue
		}
		h += musicMenuItemH
	}
	return h
}

func (a *App) musicMenuRect(w, h int32, themed bool) sdl.Rect {
	c := a.ctx
	mw := musicMenuMinW
	for i, row := range musicMenuRows {
		if row.kind == musicMenuSeparator || !a.musicMenuRowDrawn(i, themed) {
			continue
		}
		if lw := c.TextWidth(row.label) + musicMenuGutterW + 2*musicMenuPadX; lw > mw {
			mw = lw
		}
	}
	mh := a.musicMenuHeight(themed)
	x := value.Clamp(a.musicMenuAt.X, pad, max(pad, w-mw-pad))
	y := value.Clamp(a.musicMenuAt.Y, pad, max(pad, h-mh-pad))
	return sdl.Rect{X: x, Y: y, W: mw, H: mh}
}

// drawMusicMenu paints the open menu and resolves its input, with raw pointIn because the modal state blanks
// hovering().
func (a *App) drawMusicMenu(w, h int32) {
	if !a.musicMenuOpen {
		return
	}
	c := a.ctx
	themed := a.musicMenuThemed
	panel := a.musicMenuRect(w, h, themed)
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)
	y := panel.Y + 4
	for i, row := range musicMenuRows {
		if !a.musicMenuRowDrawn(i, themed) {
			continue
		}
		if row.kind == musicMenuSeparator {
			c.Fill(sdl.Rect{X: panel.X + musicMenuPadX, Y: y + musicMenuSepH/2, W: panel.W - 2*musicMenuPadX, H: 1}, ColPanelHi)
			y += musicMenuSepH
			continue
		}
		r := sdl.Rect{X: panel.X + 2, Y: y, W: panel.W - 4, H: musicMenuItemH - 2}
		enabled := a.musicMenuRowEnabled(row.kind)
		ink := ColText
		if !enabled {
			ink = ColTextDim
		}
		if enabled && pointIn(c.mouseX, c.mouseY, r) {
			c.Fill(r, ColPanelHi)
			if c.clicked {
				a.musicMenuAct(row.kind)
				c.clicked = false
				if !musicMenuRowIsToggle(row.kind) {
					a.musicMenuOpen = false
					return
				}
			}
		}
		if a.musicMenuRowChecked(row.kind) {
			c.Label(r.X+musicMenuPadX-4, r.Y+3, menuBarCheckGlyph, ink)
		}
		c.LabelClipped(r.X+musicMenuPadX-4+musicMenuGutterW, r.Y+3, r.W-musicMenuGutterW-2*musicMenuPadX+8, row.label, ink)
		y += musicMenuItemH
	}
	// Click-away or a fresh right-click closes.
	if (c.clicked || c.rightClicked) && !pointIn(c.mouseX, c.mouseY, panel) {
		a.musicMenuOpen = false
		c.clicked = false
		c.rightClicked = false
	}
}

func (a *App) musicMenuAct(k musicMenuKind) {
	if isAreaMenuKind(k) {
		a.areaMenuAct(k) // areamenu.go owns the AREA block
		return
	}
	switch k {
	case musicMenuVolumeView:
		a.musicVolMode = !a.musicVolMode
		mode := a.musicVolMode
		a.d.Prefs.Update(func(sn *config.Settings) {
			sn.MusicVolMode = mode
		})

	case musicMenuFadeIn:
		a.toggleMusicFlag(config.MusicFlagFadeIn)
	case musicMenuFadeOut:
		a.toggleMusicFlag(config.MusicFlagFadeOut)
	case musicMenuSyncPos:
		a.toggleMusicFlag(config.MusicFlagSyncPos)
	case musicMenuLoop:
		// "Loop" is the inverse of NO_REPEAT. It drives the outgoing bit only; a UI toggle must not reach local
		// playback, whose default is Loop=true.
		a.toggleMusicFlag(config.MusicFlagNoRepeat)
	case musicMenuStop:
		a.stopMusic()
	case musicMenuRandom:
		a.playRandomMusic()
	case musicMenuRosterRefresh:
		// rosterDetailCmd, not a hardcoded /getarea: that spelling is unknown on Athena, Nyathena and Whisker
		// (liveroster.go). The toast names the command so a mis-detected server can be driven by hand.
		cmd := a.rosterDetailCmd()
		a.pairAreaReset = true
		a.queueOOCLines([]string{cmd})
		a.warnLine = clampLine("Fetching UIDs / IPIDs for this area (" + cmd + ")…")
		a.warnAt = a.now()
	case musicMenuRosterLegacy:
		a.setRosterLegacy(!a.rosterLegacy) // drops the index-keyed icon cache, rebuilds live
	case musicMenuPairStatus:
		next := !a.d.Prefs.Load().ShowPairStatus
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShowPairStatus = next })
		a.pairStatusShow = next
	case musicMenuFollow:
		next := !a.d.Prefs.Load().FollowEnabled
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FollowEnabled =

				next

		})

		a.followShow = next
		if !next {
			a.followUID = "" // turning it off stops any active trailing
		}
	}
}

func (a *App) toggleMusicFlag(bit int) {
	flags := a.d.Prefs.Load().MusicFlags() ^ bit
	a.d.Prefs.Update(func(sn *config.Settings) {
		sn.MusicFlagsVal = flags
	})

}
