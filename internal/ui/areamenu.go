package ui

// The area list's overflow (kebab) menu. The Areas view had no ⋮ button, so area-scoped actions lived elsewhere and
// none was reachable from the list they act on.
//
// It adds rows to the one shared menu (the three views share one rect, popup and pane; see musicheader.go) rather than
// a rival: same click-away, clamped panel and check-mark gutter. Everything area-specific lives here; musicheader.go
// only forwards.
//
// No duplicate controls: lock / unlock / area-kick live in the CM panel, so the menu routes there, as the themed chip
// strip does when a theme places AO2's own switch.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// areaMenuKindBase offsets the area rows well clear of the music/roster iota run: the blocks are in different files,
// and continuing the iota would silently renumber the area rows when a music row is inserted, mis-dispatching them.
const areaMenuKindBase musicMenuKind = 1000

const (
	areaMenuBackground musicMenuKind = areaMenuKindBase + iota // open the background picker for this area
	areaMenuRefresh                                            // re-ask the server for the area table
	areaMenuPerAreaLog                                         // toggle: each area keeps its own IC scrollback
	areaMenuCMPanel                                            // route to the CM panel (lock / unlock / area-kick live there)
)

// areaMenuRows is the area block of the shared overflow menu, in draw order. Package-level so opening the menu
// allocates nothing.
//
// The leading divider uses the shared musicMenuSeparator kind so the menu's every-row invariants hold (a separator is
// the one row allowed an empty label). It goes dark with the block via the pane's trailing-separator rule, not
// areaMenuRowLive; see musicMenuRowDrawn (musicheader.go).
var areaMenuRows = [...]musicMenuRow{
	{musicMenuSeparator, ""},
	{areaMenuBackground, "Change the background…"},
	{areaMenuRefresh, "Refresh the area list"},
	{areaMenuPerAreaLog, "Each area keeps its own chat log"},
	{areaMenuCMPanel, "CM area controls (lock, kick)…"},
}

// isAreaMenuKind reports whether k belongs to this block, the test musicheader.go uses to forward a row here.
func isAreaMenuKind(k musicMenuKind) bool { return k >= areaMenuKindBase }

// areaMenuRowLive reports whether the list pane is showing its Areas view. Unlike the roster block, every area row acts
// on the area list in front of you, and the same popup opens from three triggers.
//
// It re-derives the answer from the screen's own dispatch, as rosterMenuThemed does (playerlist.go), because the menu
// paints at the frame tail: logTab == logTabAreas gates the classic tab (screens.go) and the themed music_list rect
// (theme_layout.go); a torn Areas panel draws whatever logTab says (torntabs.go); a hidden Areas tab draws in none
// (tabHidden). The kind is ignored so the forward in musicMenuRowLive keeps the shape of the other blocks' predicates.
func (a *App) areaMenuRowLive(_ musicMenuKind) bool {
	if a.tabHidden(logTabAreas) {
		return false
	}
	return a.logTab == logTabAreas || a.tabTorn(logTabAreas)
}

// areaMenuRowEnabled greys the rows that cannot act. Greyed, not hidden, as on the roster block: a vanished row reads
// as a removed feature.
func (a *App) areaMenuRowEnabled(k musicMenuKind) bool {
	switch k {
	case areaMenuBackground, areaMenuRefresh:
		return a.sess != nil // both talk to the server (a /bg, a /getareas)
	case areaMenuCMPanel:
		// drawCMPanel closes itself unless amICMNow (cmpanel.go), so without CM the route would be a dead click. Grey
		// it on the gate the menu bar's copy uses (`enabled: menuAmICM`, menubar.go); the mod dashboard says "Type /cm
		// to claim CM" (moddashpanel.go).
		return a.amICMNow
	}
	return true
}

// areaMenuRowChecked reports the block's one toggle.
func (a *App) areaMenuRowChecked(k musicMenuKind) bool {
	return k == areaMenuPerAreaLog && a.d.Prefs.Load().PerAreaScroll
}

// areaMenuRowIsToggle keeps the menu open for the row that flips a switch and closes it for the others (the shared
// menu's rule).
func areaMenuRowIsToggle(k musicMenuKind) bool { return k == areaMenuPerAreaLog }

// areaMenuAct performs one area row.
func (a *App) areaMenuAct(k musicMenuKind) {
	switch k {
	case areaMenuBackground:
		a.openBgPicker()
	case areaMenuRefresh:
		// The area table arrives on ARUP, which servers push on change, but a client that joined mid-session or missed
		// a burst is stale. /getareas is the re-ask the mod dashboard already uses.
		a.queueOOCLines([]string{areaRefreshCommand})
		a.warnLine = clampLine("Re-asking the server for the area list (" + areaRefreshCommand + ")…")
		a.warnAt = a.now()
	case areaMenuPerAreaLog:
		perAreaScroll := !a.d.Prefs.Load().PerAreaScroll
		a.d.Prefs.Update(func(sn *config.Settings) {
			sn.PerAreaScroll = perAreaScroll
		})

	case areaMenuCMPanel:
		a.toggleCMPanel()
	}
}

// areaRefreshCommand is the OOC spelling that re-lists areas, shared with the toast so a server answering "unknown
// command" shows what was tried.
const areaRefreshCommand = "/getareas"

// areaMenuButtonW is the ⋮ button's side on the area list's header row, matching the Music header's icon buttons.
// areaMenuButtonGap is the clearance to whatever lies to its left.
const (
	areaMenuButtonW   = int32(22)
	areaMenuButtonGap = int32(4)
)

// The area header row's named parts (the ⋮ made the row a split; see splitAreaHeaderRow). areaHeaderFieldInsetX insets
// the search field to the cards' left edge (cards draw at r.X+2). areaHeaderCountW is the width the "shown / total"
// counter may use at the right end. areaHeaderCountGap is the clearance between field and counter. areaHeaderCountOffY
// centres the counter optically against the taller field.
const (
	areaHeaderFieldInsetX = int32(2)
	areaHeaderCountW      = int32(142)
	areaHeaderCountGap    = int32(8)
	areaHeaderCountOffY   = int32(5)
)

// areaHeaderLayout is the header row split into what draws on it, left to right: the search field, the "shown / total"
// counter and the ⋮.
//
// Rects, not widths, so the rule that nothing on the row may reach under the button lives in one place.
type areaHeaderLayout struct {
	field  sdl.Rect // the "Search areas…" box (only drawn when the theme has no music_search)
	count  sdl.Rect // the counter's label origin and the room it may use
	button sdl.Rect // the ⋮
}

// splitAreaHeaderRow divides one header row between the field, the counter and the ⋮, each taking its room out of the
// row instead of drawing over its neighbour (a theme's narrow music_list has no slack). Pure and the only place the
// row's arithmetic exists: the button is placed here and the others are measured back from its left edge. row is the
// header row alone; the cards and scrollbar below keep the whole panel.
func splitAreaHeaderRow(row sdl.Rect) areaHeaderLayout {
	button := sdl.Rect{X: row.X + row.W - areaMenuButtonW, Y: row.Y, W: areaMenuButtonW, H: row.H}
	// Everything else shares what is left once the button and its clearance are taken off the right end.
	headRight := button.X - areaMenuButtonGap
	count := sdl.Rect{
		X: headRight - areaHeaderCountW,
		Y: row.Y + areaHeaderCountOffY,
		W: areaHeaderCountW,
		H: row.H - areaHeaderCountOffY,
	}
	fieldX := row.X + areaHeaderFieldInsetX
	return areaHeaderLayout{
		field:  sdl.Rect{X: fieldX, Y: row.Y, W: count.X - areaHeaderCountGap - fieldX, H: row.H},
		count:  count,
		button: button,
	}
}

// drawAreaMenuButton draws the Areas view's ⋮ in the rect splitAreaHeaderRow reserved. It opens the same popup as the
// Music header and roster toolbar, including openMusicMenuFromButton's click consumption: without it the menu's
// click-away closes it on the frame it opened (musicheader.go).
func (a *App) drawAreaMenuButton(btn sdl.Rect, themed bool) {
	if a.musicIconButton(btn, iconKebab, "Area options — background, refresh the list, per-area chat log, CM controls") {
		a.openMusicMenuFromButton(sdl.Point{X: btn.X, Y: btn.Y + btn.H}, themed)
	}
}
