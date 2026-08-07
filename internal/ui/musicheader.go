package ui

// The Music panel's header row — the planner and the overflow menu.
//
// WHY a planner, and what it fixes.
//
// drawMusicList used to lay its header out with UNCONDITIONAL draws interleaved
// with CONDITIONAL row advances: "Stop music" drew at the panel body's origin and
// advanced nothing, and the 28 px that covered it belonged to the now-playing
// label beside it — which a theme that declares `music_display` takes over. With
// that label gone, and `music_search` external too, the whole header collapsed
// onto one origin: Expand all was painted ON TOP of Stop music, and since
// Ctx.Button ends in `hover && c.clicked` with no click consumption, ONE click
// fired BOTH — expanding the categories also stopped the music. With only the
// plate external, the search TextField landed on the button instead. It also
// meant the panel body shifted 28 px the moment the plate's art streamed into T1
// (and back on eviction), because the advance was gated on texture residency.
//
// The cure is the one plStrip already applies to the Players toolbar: a single
// left-to-right cursor. One cursor cannot overlap itself, so the whole class is
// gone by construction rather than by careful arithmetic — and the plan is a pure
// value built from an injected measure, so the geometry is testable headless.
//
// WHY the controls shrank. The header cost 83 px of chrome (28+27+28) in a panel
// that is 212x243 usable inside aceattorney2x's music_list — about a third of it,
// for three buttons AO2 does not put on screen at all. AO2-Client keeps Stop
// Current Song, Play Random Song, Expand All and Collapse All in the music list's
// RIGHT-CLICK context menu (courtroom.cpp:5821-5875), and its four playback flags
// (Fade In / Fade Out Previous / Synchronize / No Repeat) with them. So: the
// four common actions become square icon buttons with tooltips, everything modal
// moves into an overflow ("kebab") menu that right-clicking the track list also
// opens, and the header is one 22 px row plus the search row.
//
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
//
// Placement order is the cursor's order, so it is also the on-screen order: the
// four actions first (stop, random, expand, collapse), the overflow menu, then
// the now-playing readout LAST because it is the only flexible control — a
// clipped label would rather shrink into the tail of the line than push a real
// button onto a second one. Row two is the search field and its count.
//
// hideSearchRow drops row two WHOLE — its draws and its height — so the caller
// gets those pixels back rather than a gap. Two callers set it: a theme that
// declared its own music_search rect (AO2 puts the field outside music_list,
// courtroom.cpp:219-222) and the volume view, which has no track list to filter.
//
// nowPlayingExternal means a theme's music_display plate already painted the name
// (AO2 parents ui_music_name inside that plate, courtroom.cpp:171), so the readout
// is dropped — but NOT the row, which belongs to the buttons.
// THE REFLOW GUARD (v1.90.0 W7b). The placement runs through planChromeRow
// (chromerow.go) rather than through a strip of its own, because a THEME chooses this
// panel's rect and a squeezed rect is data, not a corner case: a tester's imported AO2
// theme stacked Expand all and Collapse all into the same pixels, which is this file's
// own opening defect returning through a narrower door. planChromeRow places every
// control through ONE cursor and, when the cursor runs out, degrades in a stated order
// — shrink to each control's own minimum, wrap, then DROP BY PRIORITY — so "two
// controls on top of each other" is not a state it can produce.
//
// THE PRIORITIES ARE A REACHABILITY ARGUMENT, not a taste ranking:
//   - the ⋮ menu is chromeCtlNeverDrop, because every row it hosts (the volume view,
//     the four MC playback flags, the four roster switches) is reachable ONLY through
//     it inside a theme's canvas;
//   - Stop and Random go FIRST, because both are also rows of that menu — dropping
//     them hides a shortcut, not a feature;
//   - Expand all / Collapse all go last of the four, because they are in NO menu
//     (musicMenuRows) and the fold markers are the only other way to reach them;
//   - the now-playing readout is pure information and goes before any button.
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
//
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
// The roster block is appended AFTER the music block, behind its own separator, so
// the menu reads as two named halves rather than one mixed list — and so the music
// rows keep the exact order (and therefore the exact muscle memory) they had before
// the roster moved in.
var musicMenuRows = [...]musicMenuRow{
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
}

// musicMenuRowLive reports whether a row is offered at all this frame. The volume
// view is not offered inside a theme's design canvas: the theme declares its own
// music_slider / sfx_slider / blip_slider rects and those win (#21), so offering
// both would put two sets of volume controls on one screen.
//
// The four ROSTER rows are deliberately live in every branch, including from the
// Music view's own ⋮. They are panel-level settings for a list that shares this rect,
// and gating them on "the roster happens to be the visible view" would only mean the
// user has to switch views to reach a switch that is already one menu deep.
func (a *App) musicMenuRowLive(k musicMenuKind, themed bool) bool {
	if k == musicMenuVolumeView {
		return !themed
	}
	return true
}

// musicMenuRowEnabled reports whether a live row can be acted on. The four
// playback flags ride an outgoing MC's LAST field, which AO2 appends only when the
// server advertised `effects` (courtroom.cpp:5806-5819) — on a server without it
// the bits would never leave the client, so the rows are shown greyed rather than
// hidden (the setting is real, this server just can't carry it).
func (a *App) musicMenuRowEnabled(k musicMenuKind) bool {
	switch k {
	case musicMenuFadeIn, musicMenuFadeOut, musicMenuSyncPos, musicMenuLoop:
		return a.musicEffectsSupported()
	case musicMenuRosterRefresh:
		// The pull is an OOC command, so it needs a session to send it down.
		return a.sess != nil
	}
	return true
}

// musicMenuRowChecked reports a toggle row's state. Loop is the INVERSE of the
// NO_REPEAT bit: AO2 stores "don't repeat", KFO's UI says "Loop".
func (a *App) musicMenuRowChecked(k musicMenuKind) bool {
	switch k {
	case musicMenuVolumeView:
		return a.musicVolMode
	case musicMenuRosterLegacy:
		return a.rosterLegacy
	case musicMenuPairStatus:
		return a.d.Prefs.ShowPairStatusOn()
	case musicMenuFollow:
		return a.d.Prefs.FollowEnabledOn()
	}
	flags := a.d.Prefs.MusicFlags()
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
	return a.sess != nil && a.sess.Features.Has(protocol.FeatureEffects)
}

// openMusicMenu opens the overflow menu with its top-left preferred at `at` (the
// draw clamps it on-screen). Latching the tab is the rosterMenu discipline: a
// popup must never survive a switch to another session's tab. `themed` is latched
// too, for the same snapshot reason — the menu paints at the frame tail, long
// after the panel that owns that answer has returned.
func (a *App) openMusicMenu(at sdl.Point, themed bool) {
	a.musicMenuAt = at
	a.musicMenuTab = a.activeTab
	a.musicMenuThemed = themed
	a.musicMenuOpen = true
}

// openMusicMenuFromButton opens the overflow menu from the header's ⋮ control
// and CONSUMES the opening press.
//
// The consumption is load-bearing, not tidiness. musicIconButton ends in
// `hover && c.clicked` and never clears the flag (Ctx.Button behaves the same),
// so the press is still live when drawMusicMenu runs at the frame tail. Its
// click-away test then sees a click whose cursor is on the BUTTON — and the
// panel is anchored at the button's BOTTOM edge, so the cursor is above
// panel.Y, pointIn is false, and the menu closes on the very frame it opened.
// The result was a dead control: every row it hosts (the volume view, the four
// MC playback flags, stop, random) was reachable only by right-clicking the
// track list, which does consume its press. Same discipline as the roster
// list's "…" trigger (playerlist.go).
func (a *App) openMusicMenuFromButton(at sdl.Point, themed bool) {
	a.openMusicMenu(at, themed)
	a.ctx.clicked = false // the opening click is the menu's, not the click-away's
}

// musicMenuFence holds / RELEASES the kit's modal fence for the open menu, and
// force-closes it when its surface is gone. Same set-and-release discipline as
// rosterMenuFence: a stranded modalOn freezes the whole UI.
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

// musicMenuHeight is the open menu's height for the rows live this frame.
func (a *App) musicMenuHeight(themed bool) int32 {
	h := int32(8)
	for _, row := range musicMenuRows {
		if !a.musicMenuRowLive(row.kind, themed) {
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
	for _, row := range musicMenuRows {
		if row.kind == musicMenuSeparator || !a.musicMenuRowLive(row.kind, themed) {
			continue
		}
		if lw := c.TextWidth(row.label) + musicMenuGutterW + 2*musicMenuPadX; lw > mw {
			mw = lw
		}
	}
	mh := a.musicMenuHeight(themed)
	x := clampI32(a.musicMenuAt.X, pad, maxI32(pad, w-mw-pad))
	y := clampI32(a.musicMenuAt.Y, pad, maxI32(pad, h-mh-pad))
	return sdl.Rect{X: x, Y: y, W: mw, H: mh}
}

// drawMusicMenu paints the open menu and resolves its interaction. Raw pointIn,
// because our own modal fence blanks hovering() everywhere — including here — and
// Ctx.Tooltip no-ops under modalOn, which is why every row's LABEL has to say
// what it does on its own (tooltips belong on the icon buttons).
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
	for _, row := range musicMenuRows {
		if !a.musicMenuRowLive(row.kind, themed) {
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
	// Click-away (or a fresh right-click) closes; the fence already kept the press
	// from reaching anything underneath.
	if (c.clicked || c.rightClicked) && !pointIn(c.mouseX, c.mouseY, panel) {
		a.musicMenuOpen = false
		c.clicked = false
		c.rightClicked = false
	}
}

// musicMenuAct performs one menu row.
func (a *App) musicMenuAct(k musicMenuKind) {
	switch k {
	case musicMenuVolumeView:
		a.musicVolMode = !a.musicVolMode
		a.d.Prefs.SetMusicVolMode(a.musicVolMode)
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
		// 2.11 live list in the base server to fall back on (serverhelp.go: Athena
		// plist:false, Whisker plistPlugin), so the button's replacement would have
		// answered "unknown command" exactly where it is the last roster control —
		// the inert row playerlisttoolbar.go's degradation contract forbids. The
		// toast NAMES the command so a mis-detected server can still be driven by
		// hand.
		cmd := a.rosterDetailCmd()
		a.pairAreaReset = true
		a.queueOOCLines([]string{cmd})
		a.warnLine = clampLine("Fetching UIDs / IPIDs for this area (" + cmd + ")…")
		a.warnAt = a.now()
	case musicMenuRosterLegacy:
		a.setRosterLegacy(!a.rosterLegacy) // drops the index-keyed icon cache, rebuilds live
	case musicMenuPairStatus:
		next := !a.d.Prefs.ShowPairStatusOn()
		a.d.Prefs.SetShowPairStatus(next)
		a.pairStatusShow = next
	case musicMenuFollow:
		next := !a.d.Prefs.FollowEnabledOn()
		a.d.Prefs.SetFollowEnabled(next)
		a.followShow = next
		if !next {
			a.followUID = "" // turning it off stops any active trailing
		}
	}
}

// toggleMusicFlag flips one MUSIC_EFFECT bit in the persisted mask.
func (a *App) toggleMusicFlag(bit int) {
	a.d.Prefs.SetMusicFlags(a.d.Prefs.MusicFlags() ^ bit)
}
