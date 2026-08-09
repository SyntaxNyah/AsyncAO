package ui

// THE AREA LIST'S OVERFLOW ("kebab") MENU — one concern, one file.
//
// The right-hand list panel has three views and, until now, two ⋮ buttons: the
// Music header's and the roster toolbar's. The Areas view had none, so every
// area-scoped action lived somewhere else entirely — the background picker behind
// a hotkey and an Extras-box row, the per-area log behind a Settings checkbox, the
// lock/kick controls behind the CM panel — and none of them was reachable from the
// list of areas they act on.
//
// PRECEDENT, NOT A SECOND MENU. The three views share one rect, one popup and one
// pane (musicheader.go says so in as many words), so this adds ROWS to that menu
// rather than a rival implementation of it: same fence, same click-away, same
// clamped panel, same check-mark gutter. Everything area-specific — the kinds, the
// labels, when a row is live, what it does — lives here; musicheader.go only
// forwards.
//
// NO DUPLICATE CONTROLS. Lock / unlock / area-kick already exist, together, in the
// CM panel, so the menu ROUTES to that panel instead of growing its own copies —
// the same rule the themed chip strip follows when a theme places AO2's own switch.

import "github.com/veandco/go-sdl2/sdl"

// areaMenuKindBase offsets the area rows well clear of the music/roster block's
// iota run. Deliberate: the two blocks are declared in DIFFERENT files, and
// continuing the iota here would silently renumber every area row the moment
// someone inserts a music row — a kind mismatch that compiles and mis-dispatches.
const areaMenuKindBase musicMenuKind = 1000

const (
	areaMenuBackground musicMenuKind = areaMenuKindBase + iota // open the background picker for this area
	areaMenuRefresh                                            // re-ask the server for the area table
	areaMenuPerAreaLog                                         // toggle: each area keeps its own IC scrollback
	areaMenuCMPanel                                            // route to the CM panel (lock / unlock / area-kick live there)
)

// areaMenuRows is the AREA block of the shared overflow menu, in the order it
// draws: the action people came for first, the list-maintenance row next, then the
// panel-level setting, then the route out to CM. Package-level so opening the menu
// allocates nothing.
//
// The leading divider carries the SHARED musicMenuSeparator kind on purpose, so the
// menu's every-row invariants (a separator is the one row allowed an empty label)
// hold for this block too. It goes dark with the block via the pane's
// trailing-separator rule rather than via areaMenuRowLive — see
// musicMenuRowDrawn (musicheader.go), which is what no per-KIND predicate could do
// for a row whose kind is shared with the music block's own dividers.
var areaMenuRows = [...]musicMenuRow{
	{musicMenuSeparator, ""},
	{areaMenuBackground, "Change the background…"},
	{areaMenuRefresh, "Refresh the area list"},
	{areaMenuPerAreaLog, "Each area keeps its own chat log"},
	{areaMenuCMPanel, "CM area controls (lock, kick)…"},
}

// isAreaMenuKind reports whether k belongs to this block — the one test
// musicheader.go needs to forward a row here.
func isAreaMenuKind(k musicMenuKind) bool { return k >= areaMenuKindBase }

// areaMenuRowLive answers the single question the shared pane must ask before it
// offers an area row at all: is the list pane showing its Areas view?
//
// The ROSTER block next door is deliberately live in every branch, because its rows
// are panel-level settings for a list that merely shares this rect. The AREA block is
// the opposite case and must not copy that choice: every row here acts on THE AREA
// LIST in front of you — this area's background, a re-ask for the area table, a
// per-area chat log, the CM controls for the area — while the same popup is opened
// from three triggers, only one of which is the Areas view. Ungated, the Music
// header's ⋮ and the roster toolbar's ⋮ would each grow five rows about a list
// neither of them is showing.
//
// It RE-DERIVES the answer from the screen's own dispatch rather than being handed
// one, exactly as rosterMenuThemed does (playerlist.go) and for the same reason: the
// menu paints at the frame tail, long after the panel that knows the answer has
// returned, and drawAreaList is reached from three places that would each have to
// thread a parameter through. The clauses are those three call sites verbatim:
//   - logTab == logTabAreas is the gate on the classic docked tab (screens.go) and on
//     the themed music_list rect (theme_layout.go).
//   - a TORN Areas panel draws whatever logTab says (torntabs.go) and carries its own
//     ⋮, so its rows have to be live or that button is inert.
//   - a fully hidden Areas tab draws in none of the three (tabHidden), torn or not.
//
// The kind is taken and ignored: every row in the block shares one answer, and the
// parameter is what keeps the forward in musicMenuRowLive the same shape as the other
// four — a per-row predicate, so a future row that needs a narrower condition has
// somewhere to put it.
func (a *App) areaMenuRowLive(_ musicMenuKind) bool {
	if a.tabHidden(logTabAreas) {
		return false
	}
	return a.logTab == logTabAreas || a.tabTorn(logTabAreas)
}

// areaMenuRowEnabled greys the rows that cannot act. Greyed rather than hidden, on
// the roster block's precedent: the setting is real, this session just cannot carry
// it yet, and a row that vanishes reads as a feature that was removed.
func (a *App) areaMenuRowEnabled(k musicMenuKind) bool {
	switch k {
	case areaMenuBackground, areaMenuRefresh:
		return a.sess != nil // both talk to the server (a /bg, a /getareas)
	case areaMenuCMPanel:
		// The panel does NOT say it for us. drawCMPanel opens with
		// `if c.escPressed || !a.amICMNow { a.showCMPanel = false; return }`
		// (cmpanel.go), and floatbox.go only calls it while showCMPanel — so without CM
		// the route closes the menu, paints nothing and says nothing: a dead click.
		// Grey it on exactly the gate the menu bar's own copy of this action already
		// uses (`enabled: menuAmICM`, menubar.go), which is the mod/CM doctrine spelled
		// out beside it. The words a greyed row cannot carry are in the mod dashboard
		// ("Type /cm to claim CM", moddashpanel.go).
		return a.amICMNow
	}
	return true
}

// areaMenuRowChecked reports the block's one toggle.
func (a *App) areaMenuRowChecked(k musicMenuKind) bool {
	return k == areaMenuPerAreaLog && a.d.Prefs.PerAreaScrollbackOn()
}

// areaMenuRowIsToggle keeps the menu open for the row that flips a switch, and
// closes it for the three that DO something — the shared menu's rule.
func areaMenuRowIsToggle(k musicMenuKind) bool { return k == areaMenuPerAreaLog }

// areaMenuAct performs one area row.
func (a *App) areaMenuAct(k musicMenuKind) {
	switch k {
	case areaMenuBackground:
		a.openBgPicker()
	case areaMenuRefresh:
		// The area table arrives on ARUP, which servers push on change — but a client
		// that joined mid-session, or missed a burst, has whatever it was last told.
		// /getareas is the re-ask the mod dashboard already uses.
		a.queueOOCLines([]string{areaRefreshCommand})
		a.warnLine = clampLine("Re-asking the server for the area list (" + areaRefreshCommand + ")…")
		a.warnAt = a.now()
	case areaMenuPerAreaLog:
		a.d.Prefs.SetPerAreaScrollback(!a.d.Prefs.PerAreaScrollbackOn())
	case areaMenuCMPanel:
		a.toggleCMPanel()
	}
}

// areaRefreshCommand is the OOC spelling that re-lists areas. Named once, shared
// with the toast, so a server that answers "unknown command" tells the user exactly
// what was tried and they can adapt it by hand.
const areaRefreshCommand = "/getareas"

// areaMenuButtonW is the ⋮ control's square side on the area list's header row.
// Matches the Music header's icon buttons so the two panels' headers line up.
// areaMenuButtonGap is the clearance between it and whatever the row lays out to its
// left.
const (
	areaMenuButtonW   = int32(22)
	areaMenuButtonGap = int32(4)
)

// The rest of the area list's header row, in named parts. All three were bare
// arithmetic inside drawAreaList until the ⋮ arrived and made the row a SPLIT rather
// than a list of independent offsets — see splitAreaHeaderRow.
//
//   - areaHeaderFieldInsetX insets the search field to the area cards' left edge
//     (the cards draw at r.X+2). The box was ported verbatim from drawMusicList,
//     whose rows start at r.X with no inset, so it used to sit 2 px left of its own
//     column and clip out.
//   - areaHeaderCountW is the room the "shown / total" counter reserves at the right
//     end of the row. It is the counter's WIDTH, not a margin: the label is drawn
//     left-aligned from its X, and this is how much of the row it may use.
//   - areaHeaderCountGap is the clearance between the field's right edge and that
//     counter, so a full search string never runs into the numbers.
//   - areaHeaderCountOffY drops the counter's baseline to sit optically centred
//     against the field beside it, which is taller than the text.
const (
	areaHeaderFieldInsetX = int32(2)
	areaHeaderCountW      = int32(142)
	areaHeaderCountGap    = int32(8)
	areaHeaderCountOffY   = int32(5)
)

// areaHeaderLayout is the area list's header row split into the three things that
// draw on it, left to right: the search field, the "shown / total" counter and the ⋮.
//
// Rects, not widths. The caller used to get a width back from drawAreaMenuButton and
// re-derive the other two from it with its own arithmetic, which meant the rule this
// type exists to hold — nothing on this row may reach under the button — lived in
// three expressions that no test could drive, and one of them could drift back to the
// full panel width while the suite stayed green.
type areaHeaderLayout struct {
	field  sdl.Rect // the "Search areas…" box (only drawn when the theme has no music_search)
	count  sdl.Rect // the counter's label origin and the room it may use
	button sdl.Rect // the ⋮
}

// splitAreaHeaderRow divides one header row between the search field, the counter and
// the ⋮ so that each takes its room OUT OF THE ROW instead of drawing over its
// neighbour — the overlap class the roster toolbar was rebuilt to stop, on a theme's
// 212 px music_list, which has no slack.
//
// Pure, and the ONLY place the row's arithmetic exists: the button is placed here
// rather than by the drawer, and the field and counter are measured back from the
// button's left edge, so shrinking-to-fit is structural rather than a convention the
// caller has to remember. The row it is handed is the header row alone — the cards and
// the scrollbar below keep the whole panel.
func splitAreaHeaderRow(row sdl.Rect) areaHeaderLayout {
	button := sdl.Rect{X: row.X + row.W - areaMenuButtonW, Y: row.Y, W: areaMenuButtonW, H: row.H}
	// Everything else shares what is left of the row once the button and its clearance
	// are taken off the right end.
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

// drawAreaMenuButton draws the Areas view's ⋮ in the rect splitAreaHeaderRow reserved
// for it.
//
// It opens the SAME popup the Music header and the roster toolbar open — including
// openMusicMenuFromButton's click consumption, without which the menu's own
// click-away test closes it on the frame it opened (musicheader.go documents that
// trap, and it has bitten both the other two triggers).
func (a *App) drawAreaMenuButton(btn sdl.Rect, themed bool) {
	if a.musicIconButton(btn, iconKebab, "Area options — background, refresh the list, per-area chat log, CM controls") {
		a.openMusicMenuFromButton(sdl.Point{X: btn.X, Y: btn.Y + btn.H}, themed)
	}
}
