package ui

// The Music panel's header row — the planner and the overflow menu.
// RIGHT-CLICK context menu (courtroom.cpp:5821-5875), and its four playback flags
// Icons are VECTOR (drawToolIcon), never font glyphs — the compact toolbox's rule,
// and the same reason the fold markers are "[-]"/"[+]": LogFont's primary face is
// not guaranteed to cover Geometric Shapes, so a "■" is a tofu risk.

import (
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

const (
	// musicIconPx is one square icon button in the header row. Equal to
	// plStripRowH so the icons and any text control on the row share a baseline.
	musicIconPx = plStripRowH
	// musicNameMinPx is the narrowest the now-playing readout is still worth
	// drawing at — enough for a couple of words before the clip. Below it the
	// planner drops the readout rather than paint a sliver.
	musicNameMinPx = int32(56)
	// musicSearchMinPx is the narrowest the search field stays usable at.
	musicSearchMinPx = int32(70)
	// musicNothingPlaying is the readout's empty state. AsyncAO's own text (AO2
	// leaves ui_music_name blank), so the theme's music_name_color has no claim
	// on it — see drawMusicList.
	musicNothingPlaying = "Nothing playing"
	// musicNowPrefix fronts the readout when something IS playing.
	musicNowPrefix = "Now playing: "

	// musicHeaderMaxLines bounds the header's WRAP however tall the panel is. Two: the
	// action row and the search row are the header's whole design, and a third line
	// would be the header eating the track list it is a header for — the same judgement
	// chromeRowMinBodyPx makes from the other end.
	musicHeaderMaxLines = 2
)

// The header's DROP ORDER, highest first (see planMusicHeader for the argument).
// Named constants rather than bare 1/2/3 so the ranking reads as the reachability
// decision it is (hard rule 9).
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

// fontLineH is a face's line height, or 0 when there is no face. Every *ttf.Font
// in this package can be nil — the chrome face is loaded from a TTF at runtime and
// a headless Ctx has none — and Label*Font already tolerates that, so a caller
// doing its own vertical centring must too.
func fontLineH(f *ttf.Font) int32 {
	if f == nil {
		return 0
	}
	return int32(f.Height())
}

// Header control ids. Ids rather than closures, so a plan stays a plain value:
// no allocation, no captured App, and a test can assert on it with no renderer.
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

// musicHeaderMaxItems is the longest the header can get: five row-one controls
// plus the readout, then the search field and its shown/total count. Fixed, so
// musicHeaderPlan is a stack value the per-frame draw builds without allocating.
const musicHeaderMaxItems = 8

// musicHeaderPlan is one frame's header. h is the vertical room it took, which
// the caller subtracts before drawing the track list.
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

// rect finds a placed control. ok=false means the panel was too small to hold it
// and the draw must skip it — the same degradation contract plStrip.placeFlex
// documents: a missing control is recoverable, one drawn over its neighbour is not.
func (p *musicHeaderPlan) rect(id int) (sdl.Rect, bool) {
	for i := 0; i < p.n; i++ {
		if p.items[i].id == id {
			return p.items[i].r, true
		}
	}
	return sdl.Rect{}, false
}

// planMusicHeader lays out the Music panel's header for one frame.
// Placement order is the cursor's order, so it is also the on-screen order: the
// courtroom.cpp:219-222) and the volume view, which has no track list to filter.
// (AO2 parents ui_music_name inside that plate, courtroom.cpp:171), so the readout
func planMusicHeader(r sdl.Rect, now string, count string, hideSearchRow, nowPlayingExternal bool, measure func(string) int32) musicHeaderPlan {
	// A FIXED ARRAY AND AN INDEX, never an `add` closure: this runs on every frame the
	// music panel draws, and a closure capturing the array and the counter is one heap
	// allocation per frame on the themed courtroom — the class
	// TestDrawCourtroomThemedZeroAlloc exists to catch.
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

// musicHeaderItemLabel is the text a placed item carries. Only two items have any —
// the icon buttons draw vector glyphs (see the file header) and the search field draws
// its own contents.
func musicHeaderItemLabel(id int, now, count string) string {
	switch id {
	case musicItemNowPlaying:
		return now
	case musicItemCount:
		return count
	}
	return ""
}

// --- the overflow ("kebab") menu ------------------------------------------------

// musicMenuKind enumerates the rows of the LIST PANEL's overflow menu.
// It is named for the Music header it started on, but the menu belongs to the whole
// right-hand list panel — Music, Areas and the roster share one rect, one ⋮ and one
// pane. The roster rows below are the four controls the Players toolbar debloated out
// of its three header rows (playerlisttoolbar.go); they have no other home in the UI,
// so they moved here rather than being deleted.
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
	// musicMenuGutterW reserves the check-mark column so every label in the menu
	// shares a left edge — the menu-bar pane's rule (menuPaneGutterW).
	musicMenuGutterW = int32(18)
)

// musicMenuRow is one row: what it is and the label it draws.
type musicMenuRow struct {
	kind  musicMenuKind
	label string
}

// musicMenuRows is the fixed row model, package-level so opening the menu never
// allocates a table. Order follows AO2's own context menu
// (courtroom.cpp:5822-5875): the view toggle, the four playback flags, then the
// two immediate actions.
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
	// Labels are self-describing sentences, not the toolbar's abbreviations
	// ("Pairs", "Follow", "Legacy"): Ctx.Tooltip no-ops under modalOn, so a row that
	// needed a tooltip to be understood would be unexplained here.
	{musicMenuRosterRefresh, "Refresh roster details"},
	{musicMenuRosterLegacy, "Legacy roster snapshot"},
	{musicMenuPairStatus, "Show each player's pair"},
	{musicMenuFollow, "Follow players"},
}, areaMenuRows[:]...)

// musicMenuRowLive reports whether a row is offered at all this frame. The volume
// view is not offered inside a theme's design canvas: the theme declares its own
// music_slider / sfx_slider / blip_slider rects and those win (#21), so offering
// both would put two sets of volume controls on one screen.
func (a *App) musicMenuRowLive(k musicMenuKind, themed bool) bool {
	if isAreaMenuKind(k) {
		return a.areaMenuRowLive(k) // areamenu.go owns the AREA block
	}
	if k == musicMenuVolumeView {
		return !themed
	}
	return true
}

// musicMenuRowDrawn reports whether row i of musicMenuRows paints this frame: it is
// live, and — if it is a SEPARATOR — some live item still follows it.
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

// musicMenuRowEnabled reports whether a live row can be acted on. The four
// playback flags ride an outgoing MC's LAST field, which AO2 appends only when the
// server advertised `effects` (courtroom.cpp:5806-5819) — on a server without it
// the bits would never leave the client, so the rows are shown greyed rather than
// hidden (the setting is real, this server just can't carry it).
func (a *App) musicMenuRowEnabled(k musicMenuKind) bool {
	if isAreaMenuKind(k) {
		return a.areaMenuRowEnabled(k) // areamenu.go owns the AREA block
	}
	switch k {
	case musicMenuFadeIn, musicMenuFadeOut, musicMenuSyncPos, musicMenuLoop:
		return a.musicEffectsSupported()
	case musicMenuRosterRefresh:
		// The pull is an OOC command, so it needs a session to send it down.
		return a.link.sess != nil
	}
	return true
}

// musicMenuRowChecked reports a toggle row's state. Loop is the INVERSE of the
// NO_REPEAT bit: AO2 stores "don't repeat", KFO's UI says "Loop".
func (a *App) musicMenuRowChecked(k musicMenuKind) bool {
	if isAreaMenuKind(k) {
		return a.areaMenuRowChecked(k) // areamenu.go owns the AREA block
	}
	switch k {
	case musicMenuVolumeView:
		return a.musicTab.musicVolMode
	case musicMenuRosterLegacy:
		return a.roster.rosterLegacy
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

// musicMenuRowIsToggle reports whether acting on a row should leave the menu
// OPEN. Qt closes its context menu on every click; we do not, because setting
// three playback flags in a row is precisely what this menu exists for. Pinned by
// a test so it is not "tidied" back to Qt's behaviour later.
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

// musicEffectsSupported reports whether this server can carry the MUSIC_EFFECT
// bitmask at all.
func (a *App) musicEffectsSupported() bool {
	return a.link.sess != nil && a.link.sess.Features.Has(protocol.FeatureEffects)
}

// openMusicMenu opens the overflow menu with its top-left preferred at `at` (the
// draw clamps it on-screen). Latching the tab is the rosterMenu discipline: a
// popup must never survive a switch to another session's tab. `themed` is latched
// too, for the same snapshot reason — the menu paints at the frame tail, long
// after the panel that owns that answer has returned.
func (a *App) openMusicMenu(at sdl.Point, themed bool) {
	a.roster.musicMenuAt = at
	a.roster.musicMenuTab = a.activeTab
	a.roster.musicMenuThemed = themed
	a.roster.musicMenuOpen = true
}

// openMusicMenuFromButton opens the overflow menu from the header's ⋮ control
// and CONSUMES the opening press.
func (a *App) openMusicMenuFromButton(at sdl.Point, themed bool) {
	a.openMusicMenu(at, themed)
	a.ctx.clicked = false // the opening click is the menu's, not the click-away's
}

// musicMenuFence holds / RELEASES the kit's modal fence for the open menu, and
// force-closes it when its surface is gone. Same set-and-release discipline as
// rosterMenuFence: a stranded modalOn freezes the whole UI.
func (a *App) musicMenuFence(c *Ctx) {
	live := a.screen == ScreenCourtroom && !a.sel.gifExporting && !a.sel.replaying &&
		!a.sel.makerOpen && a.activeTab == a.roster.musicMenuTab
	if a.roster.musicMenuOpen && !live {
		a.roster.musicMenuOpen = false
	}
	if a.roster.musicMenuOpen {
		c.modalOn = true
		a.roster.musicMenuFenceOn = true
	} else if a.roster.musicMenuFenceOn {
		c.modalOn = false
		a.roster.musicMenuFenceOn = false
	}
}

// musicMenuHeight is the open menu's height for the rows live this frame.
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

// musicMenuRect is the open menu's clamped on-screen panel.
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
	x := clampI32(a.roster.musicMenuAt.X, pad, maxI32(pad, w-mw-pad))
	y := clampI32(a.roster.musicMenuAt.Y, pad, maxI32(pad, h-mh-pad))
	return sdl.Rect{X: x, Y: y, W: mw, H: mh}
}

// drawMusicMenu paints the open menu and resolves its interaction. Raw pointIn,
// because our own modal fence blanks hovering() everywhere — including here — and
// Ctx.Tooltip no-ops under modalOn, which is why every row's LABEL has to say
// what it does on its own (tooltips belong on the icon buttons).
func (a *App) drawMusicMenu(w, h int32) {
	if !a.roster.musicMenuOpen {
		return
	}
	c := a.ctx
	themed := a.roster.musicMenuThemed
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
					a.roster.musicMenuOpen = false
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
	// Click-away (or a fresh right-click) closes; the fence already kept the press
	// from reaching anything underneath.
	if (c.clicked || c.rightClicked) && !pointIn(c.mouseX, c.mouseY, panel) {
		a.roster.musicMenuOpen = false
		c.clicked = false
		c.rightClicked = false
	}
}

// musicMenuAct performs one menu row.
func (a *App) musicMenuAct(k musicMenuKind) {
	if isAreaMenuKind(k) {
		a.areaMenuAct(k) // areamenu.go owns the AREA block
		return
	}
	switch k {
	case musicMenuVolumeView:
		a.musicTab.musicVolMode = !a.musicTab.musicVolMode
		mode := a.musicTab.musicVolMode
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
		// "Loop" is the inverse of NO_REPEAT. It drives the OUTGOING bit only —
		// our own absent-`looping`-field default is deliberately Loop=true, and a
		// UI toggle must not reach into local playback.
		a.toggleMusicFlag(config.MusicFlagNoRepeat)
	case musicMenuStop:
		a.stopMusic()
	case musicMenuRandom:
		a.playRandomMusic()
	case musicMenuRosterRefresh:
		// What the retired "Refresh details" button did, minus its hardcoded
		// /getarea: that spelling is registered on neither Athena, Nyathena nor
		// Whisker (liveroster.go, rosterDetailCmd), and two of those three have no
		cmd := a.rosterDetailCmd()
		a.roster.pairAreaReset = true
		a.queueOOCLines([]string{cmd})
		a.warnLine = clampLine("Fetching UIDs / IPIDs for this area (" + cmd + ")…")
		a.warnAt = a.now()
	case musicMenuRosterLegacy:
		a.setRosterLegacy(!a.roster.rosterLegacy) // drops the index-keyed icon cache, rebuilds live
	case musicMenuPairStatus:
		next := !a.d.Prefs.Load().ShowPairStatus
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShowPairStatus = next })
		a.roster.pairStatusShow = next
	case musicMenuFollow:
		next := !a.d.Prefs.Load().FollowEnabled
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FollowEnabled =

				next

		})

		a.roster.followShow = next
		if !next {
			a.roster.followUID = "" // turning it off stops any active trailing
		}
	}
}

// toggleMusicFlag flips one MUSIC_EFFECT bit in the persisted mask.
func (a *App) toggleMusicFlag(bit int) {
	flags := a.d.Prefs.Load().MusicFlags() ^ bit
	a.d.Prefs.Update(func(sn *config.Settings) {
		sn.MusicFlagsVal = flags
	})

}
