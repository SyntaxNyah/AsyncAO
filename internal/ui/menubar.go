package ui

// The app-wide menu bar (#14) — a persistent top strip with REAL dropdown menus
// (Servers / View / Window / Extras / Help), the way an art program's File / Edit /
// View bar works. This file is the WIDGET — the strip, the panes, the input
// discipline and the chrome colours — AND the model: the package-level tables that
// say which rows exist and what each one runs. Reserving the strip's height at the
// top of every screen is chrometop.go's job (topChromeH).
//
// The tables are pure ENTRY POINTS. Every handler calls the function the on-screen
// affordance it replaces already called, so the two surfaces cannot drift: the
// migrated lobby buttons (#14), the theme-fit modes (#29), the window sizes (#35) and
// the compact toolbox's chips (#26) are all one implementation each, reached from
// two places.
//
// ============================ FENCE ORDERING PRECONDITION ============================
// The two halves of this widget run at DIFFERENT points of App.Frame, and the split is
// load-bearing — do not collapse it:
//
//   menuBarFrame(w, h)  MUST run ABOVE the screen dispatch (before drawLobby /
//                       drawCharSelect / drawCourtroom / drawSettings / …).
//   drawMenuBar(w, h)   runs AFTER the screens, because the kit is single-pass and
//                       whatever paints last is on top.
//
// The reason menuBarFrame cannot be moved below the dispatch: drawCourtroom takes
// c.overlayFenceMark() at the TOP of its pass and releases back to that mark on the
// way out (screens.go), and the compact toolbox's owner-suspend is MARK-SCOPED —
// pushOverlayOwner(mark) only ignores fences published at index >= mark
// (overlayfence.go). Registry order IS paint order. So:
//
//   - A fence published AFTER a pass cannot occlude anything that pass drew: the
//     widgets already hit-tested.
//   - A fence published INSIDE the courtroom pass is released when the pass returns,
//     so it would not survive to the post-courtroom overlays either.
//   - A menu pane published LATE would land at a registry index >= the toolbox's own
//     mark, so the toolbox would suspend it along with its own strip and keep
//     hit-testing its chips straight through an open pane — the exact click-through
//     bug #26 fixed, reproduced one level up with the roles swapped.
//
// Publishing App-level, before the dispatch, puts the strip (always) and the open
// pane (when open) at registry indices BELOW every in-pass publisher, which is
// precisely "painted above everything the screen draws".
//
// The corollary: painting last means painting over ANYTHING a screen put in the top
// band, so the one surface that legitimately owns that band — either layout editor,
// whose banner and Done button live at Y=0..26 — makes the strip stand down for the
// frame. menuBarPaints is that rule as phase 1 can see it; because the screens run
// BETWEEN the two halves and can arm or dismiss an editor either way, the rule is
// re-taken once after menuBarInput and settled at the paint site by menuBarPaintsNow.
// menuBarHeight deliberately follows none of it.
// =====================================================================================
//
// INPUT DISCIPLINE (all four are non-negotiable — see the sibling popups):
//   - While a pane is open the bar holds the kit's modal fence (c.modalOn) and
//     RELEASES it the frame it closes, the rostermenu.go shape. app.go warns
//     explicitly that an un-released modalOn FREEZES the whole UI. That flag is
//     SHARED with four sibling popups that all run earlier in App.Frame, so the
//     release also stands down while one of them is holding it — see
//     menuBarModalFence for the concrete one-frame leak that costs.
//   - The bar hit-tests ITSELF with raw pointIn(), exactly like the open dropdown's
//     list rows (ui.go) and the roster menu: its own modal fence would otherwise
//     blank its own titles and items.
//   - A click outside the strip and the open pane closes it, and the dismissing
//     click is SWALLOWED — modalOn has already made every hovering()-based widget
//     inert for the frame, so leaving c.clicked set would only feed the raw-pointIn
//     sites (the layout editors, an open dropdown list) a click the user meant as
//     "put the menu away". A click the bar did NOT consume is never touched.
//   - Escape closes the pane through closeTopOverlay (floatbox.go), whose header
//     documents what happens to a popup that skips it: Esc falls through to the
//     screen arm, leaves the screen, and the popup silently reappears — still
//     fenced — the next time that screen opens.
//
// CHROME COLOURS. applyThemePalette (ui.go) overwrites the PACKAGE-GLOBAL Col* vars
// the whole kit draws from, so an AO2 theme's stylesheet bleeds into every widget.
// The menu bar is CLIENT furniture, not theme furniture, and it has to stay readable
// over an arbitrary lobbybackground / courtroombackground, so it resolves its palette
// from activeKitColors — the user's own chrome preset (applyChromePreset, chrome.go)
// — instead of the theme-overwritten globals. Same idea as extrasPalette (floatbox.go)
// resolving the Extras box's own colours. No new preference.
//
// PERF. The bar draws on every screen, under TestDrawLobbyZeroAlloc and the
// courtroom gates, so nothing here may allocate per frame: the menu model is a set of
// package-level tables with plain (non-capturing) function values — the courtroomModals
// precedent (screens.go) — geometry is integer arithmetic over memoized c.TextWidth
// probes, and the latch is a value struct on App.
//
// API (exact signatures):
//
//	const menuBarH int32                       // the strip's height when shown
//	func (a *App) menuBarHeight() int32        // menuBarH, or 0 when the bar is not shown
//	func (a *App) menuBarShows() bool          // does the strip RESERVE its band at all
//	func (a *App) menuBarPaints() bool         // phase 1's paint answer (band reserved either way)
//	func (a *App) menuBarPaintsNow() bool      // the paint site's FINAL answer
//	func (a *App) noteEditorBanner()           // a layout editor painted its banner this frame
//	func (a *App) menuBarOpen() bool           // is a pane open right now
//	func (a *App) menuBarSuppressed() bool     // paints, but inert (blocking modal)
//	func (a *App) closeMenuBar()               // close the pane + any submenu (fence released next frame)
//	func (a *App) menuBarFrame(w, h int32)     // phase 1: fences + input. ABOVE the screen dispatch.
//	func (a *App) drawMenuBar(w, h int32)      // phase 2: paint. AFTER the screens.
//	func (a *App) menuGoto(s Screen)           // enter a full-screen menu, remembering prevScreen
//	func (a *App) fireMenuItem(it *menuItem)   // run one row (disabled/separator no-op)
//	var menuBarMenus []menu                    // the model, in left-to-right order
//	type menu struct{ title string; items []menuItem }
//	type menuItem struct{ kind menuItemKind; label, shortcut string; arg int;
//	                      checked, enabled func(*App, int) bool; act func(*App, int); sub []menuItem }

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// --- geometry ---------------------------------------------------------------------

const (
	// menuBarH is the strip's height in LOGICAL px. Sized to the kit's own row
	// pitch (rowH / btnH are both 22 in screens.go) so the bar reads as one more
	// band of client chrome rather than a foreign element, and so the layout stage
	// can offset a screen by exactly one standard row.
	menuBarH = int32(22)
	// menuBarPadX insets the FIRST title from the window's left edge. Small: a menu
	// bar's first item traditionally sits almost flush with the corner.
	menuBarPadX = int32(4)
	// menuBarTitlePadX is the horizontal padding either side of a top-level title,
	// so the title's clickable rect is comfortably wider than its glyphs.
	menuBarTitlePadX = int32(9)
	// menuPaneRowH is one ordinary pane row's height — a touch taller than the
	// strip so a list of rows does not read as cramped next to the bar itself.
	menuPaneRowH = int32(23)
	// menuPaneSepH is a separator row's height: the rule is one pixel, the rest is
	// the breathing room that makes it read as a group break.
	menuPaneSepH = int32(9)
	// menuPanePadY is the pane's inner top/bottom margin, clear of its border.
	menuPanePadY = int32(3)
	// menuPaneGutterW reserves the left column every row's LABEL starts after —
	// the check-mark gutter. Fixed for every row (checkable or not) so labels in
	// one pane share a left edge, which is what makes a menu scannable.
	menuPaneGutterW = int32(20)
	// menuPaneLabelPadX is the padding on the pane's right edge (and the inset the
	// separator rule is drawn with).
	menuPaneLabelPadX = int32(10)
	// menuPaneTailGapW is the minimum gap between a row's label and its right-hand
	// tail — the shortcut hint or the submenu arrow — so the two never touch on the
	// widest row, which is the row that sets the pane's width.
	menuPaneTailGapW = int32(28)
	// menuPaneMinW keeps a pane of very short labels from collapsing into a sliver.
	menuPaneMinW = int32(160)
	// menuPaneEdgePad is how far a pane stays clear of the window's right/bottom
	// edge once it has been clamped on-screen.
	menuPaneEdgePad = int32(4)
	// menuPaneBorderPx is the pane's own 1 px frame (c.Border draws exactly that),
	// which every row insets by on BOTH sides so a hovered row's highlight fill
	// stops at the border instead of painting over it.
	menuPaneBorderPx = int32(1)
	// menuSubmenuOverlapX tucks a submenu one pixel back under its parent pane's
	// border, so the two panes read as joined rather than as two floating boxes.
	menuSubmenuOverlapX = int32(1)
	// menuBarCheckGlyph marks a checked row, menuBarSubmenuGlyph marks a row that
	// opens a child pane. Drawn as text (the kit has no icon primitive for these);
	// both glyphs are already used elsewhere in the client's chrome.
	menuBarCheckGlyph   = "✓"
	menuBarSubmenuGlyph = "▸"
	// menuBarHairlineH is the 1px rule along the strip's bottom edge that separates
	// the bar from whatever the screen draws under it.
	menuBarHairlineH = int32(1)
	// menuBarBadgeDropY / menuBarBadgeInsetX place an unread badge (drawUnreadDot,
	// changelog.go) inside the strip. That helper hangs a 10 px dot off its anchor's
	// TOP-RIGHT corner and two pixels ABOVE it — right for a button in the middle of
	// a screen, but on a strip whose Y is 0 it would put half the badge off the top
	// of the window. The drop keeps the dot inside the 22 px band; the inset keeps it
	// off the neighbouring title's padding.
	menuBarBadgeDropY  = int32(5)
	menuBarBadgeInsetX = int32(4)
)

// Named indices into activeKitColors / defaultKitColors (ui.go), which is a fixed
// [7]sdl.Color in the order Background, Panel, PanelHi, Accent, Text, TextDim,
// Danger. The array is indexed positionally elsewhere; naming the slots here keeps
// this file free of bare subscripts (hard rule 9).
const (
	kitColBackground = iota
	kitColPanel
	kitColPanelHi
	kitColAccent
	kitColText
	kitColTextDim
	kitColDanger
)

// menuBarChrome is the bar's resolved palette for a frame: the CLIENT chrome
// colours, never the theme-overwritten Col* globals (see the header). A plain value
// struct, so resolving it costs nothing and allocates nothing.
type menuBarChrome struct {
	bg     sdl.Color // strip + pane fill
	hi     sdl.Color // hovered / open title, hovered row, the strip's hairline
	border sdl.Color // pane border, check mark
	text   sdl.Color // enabled label
	dim    sdl.Color // disabled label, shortcut hint, separator rule
}

// menuBarColors reads the user's chrome preset (or the stock dark palette when no
// preset is set). Deliberately NOT the package-global Col* vars: applyThemePalette
// rewrites those from an AO2 theme's stylesheet, and this strip has to stay legible
// over any theme's backgrounds.
func menuBarColors() menuBarChrome {
	return menuBarChrome{
		bg:     activeKitColors[kitColPanel],
		hi:     activeKitColors[kitColPanelHi],
		border: activeKitColors[kitColAccent],
		text:   activeKitColors[kitColText],
		dim:    activeKitColors[kitColTextDim],
	}
}

// --- model ------------------------------------------------------------------------

// menuItemKind classifies one row of an open pane.
type menuItemKind int

const (
	menuItemAction    menuItemKind = iota // a plain command row
	menuItemSeparator                     // a horizontal rule; never hovers, never fires
	menuItemCheck                         // a toggle; draws a check mark when checked() is true
	menuItemSubmenu                       // opens a child pane beside its parent row
)

// menuItem is one row of a menu.
//
// checked / enabled / act take the item's own `arg` so every table entry can stay a
// NON-CAPTURING package-level function value: a closure per row would be built at
// package init and — more importantly — would tempt future rows into capturing
// per-frame state. arg is whatever the handlers agree on (a mode constant, a slot
// index); it is ignored by handlers that do not need it.
//
//	kind      which of the four shapes this row is
//	label     the row text (and, for a submenu, the key menuSub is remembered by)
//	shortcut  right-aligned hint text, e.g. "F11"; "" draws nothing
//	arg       handler payload (see above)
//	checked   menuItemCheck only; nil = never checked
//	enabled   nil = always enabled; a false result dims the row and refuses the click
//	act       nil = inert; fired on click, after which the menu closes
//	sub       menuItemSubmenu only: the child pane's rows
type menuItem struct {
	kind     menuItemKind
	label    string
	shortcut string
	arg      int
	checked  func(*App, int) bool
	enabled  func(*App, int) bool
	act      func(*App, int)
	sub      []menuItem
}

// menu is one top-level title plus the rows its pane shows.
type menu struct {
	title string
	items []menuItem
}

// menuBarMenus is the bar's model, in left-to-right order.
//
// EVERY ROW IS AN ENTRY POINT ONTO SHIPPED MACHINERY. Nothing here implements
// behaviour: each handler calls the exact function the on-screen affordance it
// replaces already called (RefreshServers, applyWindowSize, SetThemeFit,
// openLayoutEditor, openHelp …), so the two surfaces cannot drift and a fix in one
// place fixes both. Where a shipped affordance did more than one thing on a press,
// that sequence was factored into a named App method (setPhoneBookPage,
// toggleConnectTimeSort) rather than copied.
//
// DISABLED RATHER THAN ABSENT. A row whose action cannot do anything right now —
// Disconnect with no session, Edit layout off the courtroom, "Theme's design size"
// under a theme that declares no canvas — is drawn DIMMED and refuses the click. The
// alternative (dropping the row) makes a menu whose shape changes under the cursor
// and hides the fact that the feature exists at all. This rule is applied to every
// menu; the only rows without an `enabled` predicate are the ones that genuinely
// always work.
//
// Package level with plain function values (the courtroomModals precedent,
// screens.go), so walking the model costs nothing per frame.
var menuBarMenus = []menu{
	{menuServersTitle, menuServersItems},
	{"View", menuViewItems},
	{"Window", menuWindowItems},
	{menuExtrasTitle, menuExtrasItems},
	{menuHelpTitle, menuHelpItems},
}

const (
	// menuServersTitle / menuExtrasTitle / menuHelpTitle are named because code
	// outside the table reaches for those panes: the unread-badge draw finds Help,
	// and the tests that pin the toolbox migration (#26) find Extras. The open pane
	// is remembered BY TITLE, so these strings are load-bearing.
	menuServersTitle = "Servers"
	menuExtrasTitle  = "Extras"
	menuHelpTitle    = "Help"
)

// menuServersItems is the lobby's server plumbing. Refresh and the connect-time sort
// USED to be buttons in drawLobby's right-hand utility cluster; that cluster ran seven
// buttons deep and pushed "Logs" clean off the left edge of a 640 px window, so
// everything that is not a lobby *page* affordance moved in here (screens.go).
// "Direct connect…" is new only as a row — it focuses the field the lobby has always
// drawn, which is otherwise a mouse-only target you have to find first.
var menuServersItems = []menuItem{
	{kind: menuItemAction, label: "Refresh server list", enabled: menuCanRefreshServers, act: menuActRefreshServers},
	{kind: menuItemCheck, label: "Sort by connect time", checked: menuPingSortOn, enabled: menuCanPingSort, act: menuTogglePingSort},
	{kind: menuItemCheck, label: "Phone Book", checked: menuPhoneBookOn, act: menuTogglePhoneBook},
	{kind: menuItemAction, label: "Direct connect…", act: menuActDirectConnect},
	{kind: menuItemSeparator},
	{kind: menuItemAction, label: "Disconnect", enabled: menuInSession, act: menuActDisconnect},
	{kind: menuItemSeparator},
	{kind: menuItemAction, label: "Quit AsyncAO", act: menuActQuit},
}

var menuViewItems = []menuItem{
	{kind: menuItemCheck, label: "Keep aspect ratio", checked: menuAspectKept, act: menuToggleAspect},
	{kind: menuItemSubmenu, label: menuThemeFitLabel, sub: menuThemeFitItems},
	{kind: menuItemSeparator},
	{kind: menuItemCheck, label: "Plain backdrops", checked: menuPlainBackdropsOn, act: menuTogglePlainBackdrops},
	// F3 is hard-coded in App.Frame (not a rebindable hotkey), so the hint is honest.
	{kind: menuItemCheck, label: "Performance overlay", shortcut: "F3", checked: menuPerfHUDOn, act: menuTogglePerfHUD},
}

// menuWindowItems is #35. Every row routes through App.applyWindowSize, which is the
// layer that leaves fullscreen, drops out of MAXIMIZED (a maximized window swallowed
// the resize whole — that was the reported bug), clamps to the display's usable area
// and PERSISTS. None of that is repeated here.
var menuWindowItems = []menuItem{
	{kind: menuItemAction, label: "Restore default size", act: menuActDefaultWindowSize},
	{kind: menuItemAction, label: "Theme's design size", enabled: menuThemeDesignSizeOffered, act: menuActThemeDesignSize},
	{kind: menuItemAction, label: "Fit to screen", act: menuActFitToScreen},
	{kind: menuItemSeparator},
	// F11 is likewise hard-coded (App.Frame's fullscreenReq arm).
	{kind: menuItemCheck, label: "Fullscreen", shortcut: "F11", checked: menuFullscreenOn, act: menuToggleFullscreen},
}

// menuExtrasItems is issue #26: the compact bottom-right toolbox's chips, migrated.
//
// THE FLOATING TOOLBOX SURVIVES AS A SECONDARY SURFACE, and this menu is its primary
// home. It is not deleted because it is also a MOVABLE, theme-placeable box (the
// slotToolbox classic override and the asyncao_toolbox theme rect), it is the layout
// editor's own drag handle while editing, and it carries the overlay fence that
// stopped the reported click-through. What #26 actually needed was a way to get those
// three commands WITHOUT a strip parked on top of an AO2 theme's control buttons —
// which is exactly what the rows below are, plus the "Floating toolbox" row that
// turns the strip off from a surface that is reachable on every screen. Before this
// menu existed the only ways to hide it were the very chips it was covering, a
// hotkey, or the Settings screen.
var menuExtrasItems = []menuItem{
	{kind: menuItemAction, label: "Theater mode", enabled: menuInCourtroom, act: menuActTheater},
	{kind: menuItemAction, label: "Edit layout…", enabled: menuInCourtroom, act: menuActEditLayout},
	{kind: menuItemCheck, label: "Hide UI pieces…", checked: menuToolboxPiecesOpen, enabled: menuInCourtroom, act: menuActHideUIPieces},
	{kind: menuItemCheck, label: "Floating toolbox", checked: menuToolboxShown, act: menuToggleToolbox},
	{kind: menuItemSeparator},
	{kind: menuItemAction, label: "Settings", act: menuOpenSettings},
}

var menuHelpItems = []menuItem{
	{kind: menuItemAction, label: "Hotkeys", shortcut: "F1", act: menuToggleHotkeys},
	{kind: menuItemAction, label: menuWhatsNewLabel, act: menuOpenChangelog},
	{kind: menuItemSeparator},
	// arg is openHelp's section index — the Help screen's own tab order.
	{kind: menuItemAction, label: "Privacy", arg: helpTabPrivacy, act: menuOpenHelpTab},
	{kind: menuItemAction, label: "Glossary", arg: helpTabGlossary, act: menuOpenHelpTab},
	{kind: menuItemAction, label: "Chat logs", act: menuOpenLogs},
	{kind: menuItemSeparator},
	{kind: menuItemAction, label: "For server owners…", act: menuOpenServerHelp},
	{kind: menuItemAction, label: "About AsyncAO", act: menuOpenAbout},
}

const (
	// menuThemeFitLabel is the submenu row's label, which is ALSO the key menuSub
	// remembers the open child by (menuItemIndexOf). Named so the two uses agree.
	menuThemeFitLabel = "Theme fit"
	// menuWhatsNewLabel names the row the unread badge belongs to (#23). The badge
	// itself rides the Help TITLE — see drawMenuBar — because a nag inside a closed
	// pane is not a nag; the label is named so the two can never name different rows.
	menuWhatsNewLabel = "What's New"
	// helpTabGlossary / helpTabPrivacy are openHelp's section indexes, i.e. positions
	// in helpSectionNames (help.go). Named per hard rule 9: openHelp(1) at a call
	// site says nothing about which document opens.
	helpTabGlossary = 0
	helpTabPrivacy  = 1
)

// menuThemeFitItems is the "Theme fit ▸" submenu, derived ONCE at package init from
// themeFitOptions (settings.go) so the two surfaces can never drift and the mode
// constants — which are persisted on disk and therefore append-only — stay
// independent of row order. Reuses SetThemeFit rather than forking the logic.
var menuThemeFitItems = buildThemeFitMenu()

func buildThemeFitMenu() []menuItem {
	out := make([]menuItem, len(themeFitOptions))
	for i := range themeFitOptions {
		out[i] = menuItem{
			kind:    menuItemCheck,
			label:   themeFitOptions[i].label,
			arg:     themeFitOptions[i].mode,
			checked: menuThemeFitIs,
			act:     menuSetThemeFit,
		}
	}
	return out
}

// --- handlers (non-capturing package-level functions; see menuBarMenus) --------------

// --- Servers ---
//
// The four lobby rows all jump to the lobby first. They are reachable from the
// courtroom (the tab strip's own tooltip advertises "click to browse the lobby"), and
// a Phone Book toggle or a focused Direct-connect field on a screen that draws
// neither would be a silent no-op — the failure mode the disabled-not-absent rule
// above exists to avoid.

func menuCanRefreshServers(a *App, _ int) bool { return !a.lobbyFetching }
func menuActRefreshServers(a *App, _ int)      { a.RefreshServers() }

func menuPhoneBookOn(a *App, _ int) bool { return a.phoneBookPage }
func menuTogglePhoneBook(a *App, _ int) {
	a.menuGoto(ScreenLobby)
	a.setPhoneBookPage(!a.phoneBookPage)
}

// menuCanPingSort dims the row while a sweep is in flight: startPinging refuses a
// second one (it would race its own results), so a live row there would do nothing.
// Dimming is also the only progress feedback the menu can give — the on-screen button
// this replaced said "Pinging…" in its label, which a static menu row cannot.
func menuCanPingSort(a *App, _ int) bool { return !a.pinging }
func menuPingSortOn(a *App, _ int) bool  { return a.pingMode }
func menuTogglePingSort(a *App, _ int) {
	a.menuGoto(ScreenLobby)
	a.toggleConnectTimeSort()
}

// menuActDirectConnect puts the caret in the lobby's direct-connect field. The field
// only draws on the all-servers page, so the Phone Book page has to be left first —
// FocusField queues an id, and an id nothing draws is silently dropped.
func menuActDirectConnect(a *App, _ int) {
	a.menuGoto(ScreenLobby)
	a.setPhoneBookPage(false)
	a.ctx.FocusField(directConnectFieldID)
}

func menuInSession(a *App, _ int) bool { return a.sess != nil }

func menuActDisconnect(a *App, _ int) { a.requestDisconnect() } // routed through the confirm, like every other exit
func menuActQuit(a *App, _ int)       { a.requestQuit() }

// --- View ---

// menuAspectKept reports whether the active fit mode preserves the theme canvas's
// aspect ratio. Stretch is the only mode that does not: Native, Letterbox, Crop and
// Custom all scale both axes by one factor (Crop keeps the shape and trims the
// overhang; Custom is a uniform zoom plus a pan).
func menuAspectKept(a *App, _ int) bool { return a.d.Prefs.ThemeFitMode() != config.ThemeFitStretch }

// menuToggleAspect goes to Stretch when unticked and back to the SHIPPED DEFAULT when
// re-ticked — Native, i.e. 1:1 like the stock AO2 client, not whichever aspect-keeping
// mode happened to be set before. A remembered "previous mode" would need state that
// survives a restart, and the default is the answer #29 shipped for "what should this
// look like out of the box".
func menuToggleAspect(a *App, _ int) {
	if menuAspectKept(a, 0) {
		menuSetThemeFit(a, config.ThemeFitStretch)
		return
	}
	menuSetThemeFit(a, config.DefaultThemeFit)
}

func menuPlainBackdropsOn(a *App, _ int) bool { return a.d.Prefs.PlainLobbyOn() }
func menuTogglePlainBackdrops(a *App, _ int) {
	a.d.Prefs.SetPlainLobby(!a.d.Prefs.PlainLobbyOn())
}

func menuPerfHUDOn(a *App, _ int) bool { return a.perfHUD }
func menuTogglePerfHUD(a *App, _ int)  { a.perfHUD = !a.perfHUD }

// --- Window (#35) ---

func menuActDefaultWindowSize(a *App, _ int) {
	a.applyWindowSize(config.DefaultWindowW, config.DefaultWindowH)
}

// menuThemeDesignSizeOffered reuses themeDesignSizeOffer — the SAME predicate the
// Settings row and the automatic snap-on-import consult — rather than re-deriving
// "does this theme declare a canvas". It also gates on the themed courtroom layout
// being the one actually drawn, because under the classic layout the canvas is not
// what is on screen and the row's promise would be false.
func menuThemeDesignSizeOffered(a *App, _ int) bool {
	_, _, ok := a.themeDesignSizeOffer()
	return ok
}
func menuActThemeDesignSize(a *App, _ int) { a.applyThemeDesignWindowSize() }

func menuActFitToScreen(a *App, _ int) { a.fitWindowToScreen() }

func menuFullscreenOn(a *App, _ int) bool { return a.d.Prefs.WindowFullscreen() }
func menuToggleFullscreen(a *App, _ int)  { a.toggleFullscreen() }

// --- Extras (#26) ---

// menuInCourtroom gates the three migrated toolbox commands. All three only mean
// anything on the courtroom screen: theater mode is stage-only, the layout editors
// draw INSIDE drawCourtroom, and the per-piece panel is drawn from the courtroom arm
// of the screen dispatch. Arming the editor from the lobby would be worse than a
// no-op: the editor owns the top band while it is armed, so the bar would stop
// painting altogether on a screen that never draws the editor's own way out.
func menuInCourtroom(a *App, _ int) bool { return a.screen == ScreenCourtroom }

// The three actions call the toolbox's own method values, so the menu, the chips, the
// command palette and the hotkeys are one implementation each.
func menuActTheater(a *App, _ int)      { a.compactTheater() }
func menuActEditLayout(a *App, _ int)   { a.compactEditLayout() }
func menuActHideUIPieces(a *App, _ int) { a.compactHideUI() }

func menuToolboxPiecesOpen(a *App, _ int) bool { return a.toolboxPinned && a.toolboxPieces }

// menuToolboxShown / menuToggleToolbox drive the floating strip's visibility through
// the SAME hidden-chrome entry the per-piece panel's own checkbox uses, guard
// included: setPanelHiddenGuarded refuses to hide the toolbox while the toolbar's
// Settings button is also hidden, because that pair is the two mouse routes back to
// the chrome. (That guard predates this menu, which is now a third route; it is left
// in force rather than relaxed here, where relaxing it could not be tested against
// every profile/import path that also writes the hidden set.)
func menuToolboxShown(a *App, _ int) bool { return !a.panelHidden(panelToolbox) }
func menuToggleToolbox(a *App, _ int) {
	a.setPanelHiddenGuarded(panelToolbox, !a.panelHidden(panelToolbox))
}

func menuOpenSettings(a *App, _ int) { a.menuGoto(ScreenSettings) }

// --- Help ---

func menuOpenAbout(a *App, _ int) { a.menuGoto(ScreenAbout) }

// menuOpenChangelog opens the full version history AND clears the unread badge, the
// same pair the lobby's "What's New" button ran before it moved in here.
func menuOpenChangelog(a *App, _ int) {
	a.menuGoto(ScreenChangelog)
	a.markChangelogSeen()
}

// menuOpenHelpTab opens the Help screen on one of its sections. openHelp assigns the
// screen itself and documents that callers set prevScreen first, so re-picking a
// section while already on Help must NOT overwrite it (the menuGoto reasoning, which
// cannot be reused directly here because the tab has to change either way).
func menuOpenHelpTab(a *App, tab int) {
	if a.screen != ScreenHelp {
		a.prevScreen = a.screen
	}
	a.openHelp(tab)
}

func menuOpenLogs(a *App, _ int) {
	if a.screen == ScreenLogs {
		return
	}
	a.prevScreen = a.screen
	a.openLogBrowser()
	a.screen = ScreenLogs
}

func menuOpenServerHelp(a *App, _ int) { a.menuGoto(ScreenServerHelp) }

// menuToggleHotkeys mirrors App.Frame's F1 arm exactly (including dropping the
// cached rows, which are rebuilt per open).
func menuToggleHotkeys(a *App, _ int) {
	if a.showHotkeys {
		a.showHotkeys = false
		a.hkCache = nil
		return
	}
	a.openHotkeyCheatSheet()
}

// menuGoto enters a full-screen menu the way the on-screen buttons do, remembering
// where we came from for the Back button / Esc. Re-selecting the screen you are
// already on must NOT overwrite prevScreen, or Esc would bounce between it and
// itself instead of backing out.
func (a *App) menuGoto(s Screen) {
	if a.screen == s {
		return
	}
	a.prevScreen = a.screen
	a.screen = s
}

func menuThemeFitIs(a *App, mode int) bool { return a.d.Prefs.ThemeFitMode() == mode }

func menuSetThemeFit(a *App, mode int) {
	a.d.Prefs.SetThemeFit(mode)
	a.themeLay.valid = false // rebuild the layout cache with the new fit (the settings.go idiom)
}

// --- per-frame latch ----------------------------------------------------------------

// menuBarLatch is THIS FRAME's single menu-bar decision. Phase 1 (menuBarFrame, above
// the screen dispatch) computes it and publishes the fences; phase 2 (drawMenuBar,
// after the screens) REPLAYS it instead of re-deriving anything. The compact
// toolbox's compactToolboxLatch exists for the same reason and is worth reading: a
// screen draw sits between the two halves and can change the state they would each
// read, and a fence that disagrees with the pixels eats clicks over nothing.
type menuBarLatch struct {
	// draws is "the strip paints at all" this frame.
	draws bool
	// live is "the bar published its fences and may resolve input" — false while a
	// later overlay already owns the pointer. The strip still PAINTS when live is
	// false, it is simply inert (see menuBarFrame for why the fence follows the paint
	// but the input follows ownership).
	live bool
	// inert is menuBarSuppressed() as it was when phase 1 ran: the strip paints, but
	// a blocking window modal owns the frame, so it neither takes input NOR offers
	// hover feedback. LATCHED rather than re-read in phase 2 for the same reason
	// every other field here is — a screen draws between the two halves and can flip
	// the state, and a highlight that lights under a cursor whose click does nothing
	// is a lie about what is clickable.
	inert bool
	// editorHeld is "phase 1 stood the widget down because a layout editor was armed,
	// and the band is otherwise ours" — draws is false, no fence was published and no
	// input was taken, but the STRIP RECT is latched so the paint site can still put
	// the bar back if that editor turns out never to have painted anything (see
	// menuBarPaintsNow). It is never set on the frames that have no band at all.
	editorHeld bool
	// strip / pane / sub are the exact rects published AND painted — one
	// computation, so the fence and the pixels cannot drift.
	strip sdl.Rect
	pane  sdl.Rect
	sub   sdl.Rect
	// open is the index into menuBarMenus of the open pane, -1 when closed;
	// subRow is the index into that menu's items of the open submenu, -1 when none.
	open   int
	subRow int
	// mark is the fence-registry depth from just BEFORE our own publications.
	mark int
}

// newMenuBarLatch is the only way to build one: the zero value's open/subRow would
// read as "menu 0 / row 0 is open", which is the wrong default.
func newMenuBarLatch(mark int) menuBarLatch {
	return menuBarLatch{open: -1, subRow: -1, mark: mark}
}

// --- state queries ------------------------------------------------------------------

// menuBarShows reports whether the strip RESERVES its band at all. It is FALSE for
// the modes that take the whole window instead of a screen (App.Frame's
// gif > replay > maker precedence) and for theater mode, which exists precisely to
// get every piece of client chrome off the stage.
func (a *App) menuBarShows() bool {
	return !a.screenDispatchPreempted() && !a.theaterOn
}

// menuBarPaints is PHASE 1's answer to whether the strip paints. It is menuBarShows
// minus one case the RESERVED HEIGHT must not lose:
//
// While either layout editor is armed the editor owns the top band outright. Both
// draw a 26 px banner at Y=0 whose entire control row — Done / Reset all / Snap /
// Grid / Aspect / Magnet / Profile — sits at Y=2..24, and drawMenuBar runs AFTER the
// screen dispatch, so an opaque 22 px strip painted there buries ~20 of those 22 px.
// The chips still answer the mouse (both editors hit-test with raw pointIn, which no
// fence covers) but they are INVISIBLE, and Done is the editor's primary exit — one
// that the Extras menu's own "Edit layout…" row leads straight into.
//
// Only the PAINT stands down. menuBarHeight still reports menuBarH, because the band
// is what every screen — and the themed layout cache — offsets its content by: giving
// it up while editing would shift the entire courtroom up 22 px on entry and back down
// on exit, i.e. the editor would move the very widgets you are placing.
//
// IT IS NOT THE LAST WORD, and it cannot be: it reads a flag that a screen drawn later
// in the same frame can flip in either direction, and phase 1 has to run above the
// screen dispatch (the fence-ordering precondition at the top of this file). The route
// named two paragraphs up is itself one of them — the Extras row fires inside
// menuBarInput, i.e. after this predicate has already been consulted. menuBarFrame
// therefore RE-TAKES it once its own input is resolved, and menuBarPaintsNow settles it
// for good at the paint site.
func (a *App) menuBarPaints() bool {
	return a.menuBarShows() && !a.layoutEditorArmed()
}

// menuBarPaintsNow is the FINAL word on the strip's paint, taken at the paint site
// because that is the only point in a frame where the answer can no longer change.
//
// The question is not "is an editor armed" — it is "did an editor's banner reach the
// screen this frame", and the two differ in BOTH directions. Measured at 1280x720:
//
//   - ARM. The Extras → "Edit layout…" row, a hotkey and the compact toolbox's Edit
//     chip all arm an editor AFTER phase 1 has latched draws=true, and all three do it
//     early enough that the editor's own draw site still runs — so the banner is up and
//     the strip painted straight over it. (menuBar.draws=true, menuBarPaints=false, and
//     a published fence for pixels that were never ours.) The command palette also arms
//     an editor mid-frame, but from AFTER the courtroom pass, so no banner is painted
//     that frame and the strip is still the right thing to draw — which is exactly why
//     the armed flag is the wrong signal and a re-derived menuBarPaints() here would
//     open a fresh one-frame gap where it closes the other.
//   - DISMISS. drawClassicEditor lays out its chips, handles their clicks, and paints
//     its banner LAST, so the frame Done is pressed returns before painting anything —
//     as do the mid-pass force-stops (session dropped, theater, the theme went away).
//     Phase 1 had already stood the bar down, so that frame showed an EMPTY 22 px band.
//     editorHeld is what lets the paint site put the bar back, inert (no fence and no
//     input were taken for it), instead of losing it for a frame.
func (a *App) menuBarPaintsNow() bool {
	if a.editorBannerPainted {
		return false
	}
	return a.menuBar.draws || a.menuBar.editorHeld
}

// noteEditorBanner is how the two layout editors publish "the band is mine this frame".
// Called from the exact statement that fills the banner, never from the arm path, so
// the flag means what menuBarPaintsNow needs it to mean: painted, not merely armed.
// App.Frame clears it every frame, next to the tab strip's paint latch.
func (a *App) noteEditorBanner() { a.editorBannerPainted = true }

// layoutEditorArmed is "one of the two layout editors owns the screen": the classic
// slot editor (classiclayout.go) or the themed rect editor (layoutedit.go). Named
// because three separate rules key off the pair.
func (a *App) layoutEditorArmed() bool { return a.classicEdit || a.layoutEdit }

// menuBarHeight is the vertical space the bar occupies — the ONE number the layout
// stage offsets screen content by, so no caller ever hardcodes menuBarH. Reports
// ZERO whenever the bar is not shown, so the offset can be applied unconditionally.
// Deliberately menuBarShows, NOT menuBarPaints: see menuBarPaints for why a layout
// edit must not move the band it is editing against.
func (a *App) menuBarHeight() int32 {
	if !a.menuBarShows() {
		return 0
	}
	return menuBarH
}

// menuBarOpen reports whether a pane is open right now.
func (a *App) menuBarOpen() bool { return menuBarIndexOf(a.menuOpen) >= 0 }

// closeMenuBar shuts the pane and any submenu. The modal fence it holds is released
// by menuBarModalFence on this same frame (menuBarFrame calls it last), so a caller
// outside the frame — closeTopOverlay's Esc arm — is released on the next frame,
// exactly like the roster menu.
func (a *App) closeMenuBar() { a.menuOpen, a.menuSub = "", "" }

// menuBarSuppressed reports the states in which the strip must be INERT even though
// it still paints: a blocking window modal owns the frame (the same set App.Frame
// fences the pointer for). Leaving the bar live under a modal would let its own
// modalOn blank the modal's buttons.
//
// The layout editors are in here too, but they never reach the inert path — they
// stand the whole widget down one step earlier, at menuBarPaints, because they own
// the pixels as well as the input. Keeping them in this predicate means every
// "is the bar usable right now" caller gets one honest answer.
func (a *App) menuBarSuppressed() bool {
	return a.layoutEditorArmed() ||
		a.confirmDisconnect || a.pendingCloseTab != nil || a.hidePrompt != "" ||
		a.showQuitConfirm || a.disconnectDlg.open
}

// menuBarIndexOf maps a stored menu title to its index in menuBarMenus, or -1.
// The open menu is remembered BY TITLE rather than by index so that the &App{} zero
// value is "closed" — a zero index would mean "Servers is open" on every freshly
// built App, including every test fixture — and so that reordering the model cannot
// silently reinterpret the open pane.
func menuBarIndexOf(title string) int {
	if title == "" {
		return -1
	}
	for i := range menuBarMenus {
		if menuBarMenus[i].title == title {
			return i
		}
	}
	return -1
}

// menuItemIndexOf maps a stored item label to its row index in items, or -1. Same
// zero-value reasoning as menuBarIndexOf.
func menuItemIndexOf(items []menuItem, label string) int {
	if label == "" {
		return -1
	}
	for i := range items {
		if items[i].label == label {
			return i
		}
	}
	return -1
}

// menuItemEnabled resolves a row's enabled predicate (nil = always enabled).
// Separators are never enabled: they are decoration, not a target.
func (a *App) menuItemEnabled(it *menuItem) bool {
	if it.kind == menuItemSeparator {
		return false
	}
	if it.enabled == nil {
		return true
	}
	return it.enabled(a, it.arg)
}

// --- geometry helpers ---------------------------------------------------------------

// menuBarTitleW is the i-th top-level title's clickable width.
func (a *App) menuBarTitleW(i int) int32 {
	return a.ctx.TextWidth(menuBarMenus[i].title) + 2*menuBarTitlePadX
}

// menuBarTitleRect is the i-th title's rect on the strip. Recomputed by walking
// rather than cached: c.TextWidth is memoized, so the walk is a handful of map hits
// and allocates nothing.
func (a *App) menuBarTitleRect(i int) sdl.Rect {
	x := menuBarPadX
	for j := 0; j < i; j++ {
		x += a.menuBarTitleW(j)
	}
	return sdl.Rect{X: x, Y: 0, W: a.menuBarTitleW(i), H: menuBarH}
}

// menuBarTitleAt returns the index of the title under (x, y), or -1.
func (a *App) menuBarTitleAt(x, y int32) int {
	if y < 0 || y >= menuBarH {
		return -1
	}
	for i := range menuBarMenus {
		if pointIn(x, y, a.menuBarTitleRect(i)) {
			return i
		}
	}
	return -1
}

// menuItemHeight is one row's height by kind.
func menuItemHeight(k menuItemKind) int32 {
	if k == menuItemSeparator {
		return menuPaneSepH
	}
	return menuPaneRowH
}

// menuPaneSize measures a pane holding items: wide enough for the widest
// gutter+label+tail row (never below menuPaneMinW), tall enough for every row plus
// the inner margins.
func (a *App) menuPaneSize(items []menuItem) (int32, int32) {
	c := a.ctx
	paneW, paneH := menuPaneMinW, 2*menuPanePadY
	for i := range items {
		it := &items[i]
		paneH += menuItemHeight(it.kind)
		if it.kind == menuItemSeparator {
			continue
		}
		rowW := menuPaneGutterW + c.TextWidth(it.label) + menuPaneLabelPadX
		switch {
		case it.shortcut != "":
			rowW += menuPaneTailGapW + c.TextWidth(it.shortcut)
		case it.kind == menuItemSubmenu:
			rowW += menuPaneTailGapW + c.TextWidth(menuBarSubmenuGlyph)
		}
		if rowW > paneW {
			paneW = rowW
		}
	}
	return paneW, paneH
}

// menuPaneRect places the pane for the mi-th menu: dropped under its title, shifted
// left at the window's right edge, and clamped so it never starts above the strip.
func (a *App) menuPaneRect(mi int, w, h int32) sdl.Rect {
	paneW, paneH := a.menuPaneSize(menuBarMenus[mi].items)
	x := a.menuBarTitleRect(mi).X
	if x+paneW > w-menuPaneEdgePad {
		x = w - menuPaneEdgePad - paneW
	}
	if x < menuPaneEdgePad {
		x = menuPaneEdgePad
	}
	// A pane taller than the room under the strip is CLAMPED, never moved up: it
	// must not cover the bar that owns it. Rows past the clamp are culled by the
	// draw and refused by the hit test through the SAME predicate (menuPaneRowFits),
	// so the two agree exactly — see that helper for the sliver bug they used to
	// disagree about. A scrolling pane is out of scope: nothing shipped is remotely
	// that long, and hard rule 4 wants the cap named rather than implicit.
	y := menuBarH
	if room := h - menuPaneEdgePad - y; paneH > room && room > 0 {
		paneH = room
	}
	return sdl.Rect{X: x, Y: y, W: paneW, H: paneH}
}

// menuPaneRowRect is the idx-th row's rect inside pane, inset by the pane's own
// border on both sides.
func menuPaneRowRect(pane sdl.Rect, items []menuItem, idx int) sdl.Rect {
	y := pane.Y + menuPanePadY
	for i := 0; i < idx; i++ {
		y += menuItemHeight(items[i].kind)
	}
	return sdl.Rect{
		X: pane.X + menuPaneBorderPx, Y: y,
		W: pane.W - 2*menuPaneBorderPx, H: menuItemHeight(items[idx].kind),
	}
}

// menuPaneRowFits reports whether a row is WHOLLY inside a clamped pane (menuPaneRect
// shortens a pane that would run off the bottom of the window).
//
// It is THE rule for "does this row exist this frame", consulted by the draw and by
// the hit test alike. They used to disagree: the draw stopped at the first row whose
// bottom passed the clamp, while the hit test accepted any point inside both the pane
// and the row — so the last, undrawn row was still clickable through the few pixels
// of it that lay above the pane's bottom edge. A menu that fires a command nothing
// painted is the worst version of the input-vs-pixels drift this file exists to avoid.
func menuPaneRowFits(pane, row sdl.Rect) bool { return row.Y+row.H <= pane.Y+pane.H }

// menuPaneRowAt returns the index of the row under (x, y) inside pane, or -1 (also
// -1 for a separator, which is not a target, and for anything past a clamped pane's
// bottom edge, which is not painted).
func menuPaneRowAt(pane sdl.Rect, items []menuItem, x, y int32) int {
	if !pointIn(x, y, pane) {
		return -1
	}
	for i := range items {
		row := menuPaneRowRect(pane, items, i)
		if !menuPaneRowFits(pane, row) {
			break // rows run top-to-bottom: nothing after this one is painted either
		}
		if items[i].kind == menuItemSeparator {
			continue
		}
		if pointIn(x, y, row) {
			return i
		}
	}
	return -1
}

// menuSubRect places the child pane for the idx-th row of pane: hard against the
// parent's right border, flipping to its LEFT when that would run off the window,
// and lifted so its first row lines up with the parent row.
func (a *App) menuSubRect(pane sdl.Rect, items []menuItem, idx int, w, h int32) sdl.Rect {
	subW, subH := a.menuPaneSize(items[idx].sub)
	row := menuPaneRowRect(pane, items, idx)
	x := pane.X + pane.W - menuSubmenuOverlapX
	if x+subW > w-menuPaneEdgePad {
		x = pane.X - subW + menuSubmenuOverlapX // flip to the parent's left
	}
	if x < menuPaneEdgePad {
		x = menuPaneEdgePad
	}
	y := row.Y - menuPanePadY
	if y+subH > h-menuPaneEdgePad {
		y = h - menuPaneEdgePad - subH
	}
	if y < menuBarH {
		y = menuBarH
	}
	return sdl.Rect{X: x, Y: y, W: subW, H: subH}
}

// --- phase 1: fences + input (ABOVE the screen dispatch) -----------------------------

// menuBarFrame resolves the bar for this frame: it latches the geometry, publishes
// the overlay fences, resolves the bar's own input, and holds/releases the modal
// fence. It MUST be called from App.Frame above the screen dispatch — read the fence
// ordering precondition at the top of this file before moving it.
func (a *App) menuBarFrame(w, h int32) {
	c := a.ctx
	a.menuBar = newMenuBarLatch(c.overlayFenceMark())
	// menuBarPaints, not menuBarShows: a layout editor owns the top band's PIXELS as
	// well as its input, so the widget stands down whole — no paint, no fence, no
	// input. The band it reserves is untouched (menuBarHeight), so nothing the editor
	// is placing moves under it.
	if !a.menuBarPaints() {
		a.closeMenuBar()
		a.menuBarStandDown(w)
		a.menuBarModalFence(c)
		return
	}
	a.menuBar.draws = true
	a.menuBar.inert = a.menuBarSuppressed()
	a.menuBar.strip = sdl.Rect{X: 0, Y: 0, W: w, H: menuBarH}
	if a.menuBar.inert {
		a.closeMenuBar() // never hold modalOn under a modal that owns the frame
	}
	// The FENCE follows the PIXELS, the INPUT follows ownership — two different
	// questions, deliberately answered separately:
	//   - c.ptrFenced means a LATER overlay (a blocking modal, the hotkey sheet)
	//     already owns the pointer for this frame and will unfence for its own pass.
	//     A fence left in the registry would then blank ITS widgets over pixels we
	//     never painted, so publish nothing at all.
	//   - Otherwise publish: the strip really is painted over the top band of every
	//     screen dispatched below, so those screens must not hit-test underneath it.
	a.menuBar.live = !c.ptrFenced
	if a.menuBar.live {
		c.fenceOverlay(a.menuBar.strip)
	}
	if a.menuBar.live && !a.menuBar.inert {
		a.menuBarInput(w, h)
	}
	// RE-TAKE THE PAINT DECISION. menuBarInput may have just fired a row that arms a
	// layout editor — Extras → "Edit layout…" is the shortest route to one, and it is
	// the very route menuBarPaints' own doc names — in which case the decision taken at
	// the top of this function is already stale and the strip would paint over a banner
	// the courtroom pass is about to draw. Retaking it here, and DROPPING what we
	// published, is the discipline latchTabStripPaint uses one level down: decide where
	// the answer is final, then replay. (The far end, an editor armed or dismissed
	// deeper in the frame than this, is settled at the paint site by menuBarPaintsNow.)
	if !a.menuBarPaints() {
		a.closeMenuBar()
		c.overlayFenceRelease(a.menuBar.mark) // a strip that paints nothing fences nothing
		a.menuBar = newMenuBarLatch(a.menuBar.mark)
		a.menuBarStandDown(w)
		a.menuBarModalFence(c)
		return
	}
	// Resolve the open pane AFTER the input (a click may have just opened or closed
	// one) so the published rect is the rect this frame paints.
	if mi := menuBarIndexOf(a.menuOpen); mi >= 0 {
		items := menuBarMenus[mi].items
		a.menuBar.open = mi
		a.menuBar.pane = a.menuPaneRect(mi, w, h)
		if a.menuBar.live {
			c.fenceOverlay(a.menuBar.pane)
		}
		if si := menuItemIndexOf(items, a.menuSub); si >= 0 && items[si].kind == menuItemSubmenu {
			a.menuBar.subRow = si
			a.menuBar.sub = a.menuSubRect(a.menuBar.pane, items, si, w, h)
			if a.menuBar.live {
				c.fenceOverlay(a.menuBar.sub)
			}
		} else {
			a.menuSub = "" // a stale label (the model changed under us) is not a submenu
		}
	}
	a.menuBarModalFence(c)
}

// menuBarStandDown records a phase-1 stand-down that a LAYOUT EDITOR caused, as
// opposed to one of the full-window modes that take the band away altogether. Both
// leave draws false, publish nothing and take no input; the difference is that the
// band is still the bar's, so the strip rect is latched and editorHeld is set — the
// two things menuBarPaintsNow needs to put the bar back on a frame where the editor
// stopped without ever painting. menuBarShows is the discriminator, so a gif export /
// replay / scene maker / theater frame records nothing and can never "recover" a strip
// that has no band to sit in.
func (a *App) menuBarStandDown(w int32) {
	if !a.menuBarShows() {
		return
	}
	a.menuBar.editorHeld = true
	a.menuBar.strip = sdl.Rect{X: 0, Y: 0, W: w, H: menuBarH}
}

// menuBarModalFence holds the kit's modal fence while a pane is open and RELEASES it
// the frame it closes — the rostermenu.go discipline, because modalOn persists
// across frames and an un-released one freezes the whole UI (app.go says so).
// menuFenceOn records that WE set it, so a bar that never took the fence never
// touches it.
//
// c.modalOn is a single SHARED flag, not a per-owner one, so "I set it" is not by
// itself enough to make clearing it safe. Four siblings drive the same flag and ALL
// of them run EARLIER in App.Frame — the emoji picker, the reaction picker, the
// roster menu and the What's New modal. The concrete leak: the roster menu is open,
// the user opens a menu pane (the bar hit-tests with raw pointIn, so modalOn does not
// stop it) and then dismisses it — on that frame the roster's fence sets modalOn and
// ours would clear it, leaving the courtroom under the open roster menu live for one
// frame (hover, wheel, scrollbar drags). So the release ALSO asks whether a sibling
// is holding it, and stands down if so; the sibling's own release then runs on the
// frame IT closes, exactly as before.
func (a *App) menuBarModalFence(c *Ctx) {
	if a.menuBarOpen() {
		c.modalOn = true
		a.menuFenceOn = true
		return
	}
	if !a.menuFenceOn {
		return
	}
	a.menuFenceOn = false
	if a.otherModalFenceHeld() {
		return // not ours to drop this frame — the holder releases it on its own close
	}
	c.modalOn = false
}

// otherModalFenceHeld reports whether one of the sibling popups that share c.modalOn
// is holding it right now. Every one of them latches its own "WE set it" flag with
// the same discipline, and every one of them runs above the menu bar in App.Frame, so
// their flags are already this frame's answer by the time the bar releases.
func (a *App) otherModalFenceHeld() bool {
	return a.emojiFenceOn || a.reactFenceOn || a.rosterMenuFenceOn || a.updateFenceOn
}

// menuBarInput resolves the bar's own interaction. Every hit test is raw pointIn:
// the bar's modal fence blanks hovering() for the whole frame, this widget included
// (the open dropdown's list rows and the roster menu solve it the same way).
func (a *App) menuBarInput(w, h int32) {
	c := a.ctx
	mx, my := c.mouseX, c.mouseY
	title := a.menuBarTitleAt(mx, my)
	open := menuBarIndexOf(a.menuOpen)

	switch {
	case title >= 0 && c.clicked:
		// Click a title: open it, or toggle the open one shut.
		if open == title {
			a.closeMenuBar()
		} else {
			a.menuOpen, a.menuSub = menuBarMenus[title].title, ""
		}
		c.clicked = false
	case title >= 0 && open >= 0 && title != open:
		// Hover-to-switch: once ANY pane is open, sliding across the strip moves the
		// open pane with the cursor — standard menu-bar behaviour, no second click.
		a.menuOpen, a.menuSub = menuBarMenus[title].title, ""
	}

	open = menuBarIndexOf(a.menuOpen)
	if open < 0 {
		// Nothing open: a click that missed the strip is NOT ours — leave it alone.
		return
	}
	items := menuBarMenus[open].items
	pane := a.menuPaneRect(open, w, h)
	row := menuPaneRowAt(pane, items, mx, my)

	// Hovering a row of the parent pane drives the submenu: a submenu row opens its
	// child, any other row closes it. Moving OFF the pane entirely (row < 0, e.g.
	// travelling into the open submenu) deliberately changes nothing.
	if row >= 0 {
		if items[row].kind == menuItemSubmenu {
			a.menuSub = items[row].label
		} else {
			a.menuSub = ""
		}
	}

	sub := menuItemIndexOf(items, a.menuSub)
	var subPane sdl.Rect
	if sub >= 0 && items[sub].kind == menuItemSubmenu {
		subPane = a.menuSubRect(pane, items, sub, w, h)
	} else {
		sub = -1
	}

	if !c.clicked && !c.rightClicked {
		return
	}
	switch {
	case sub >= 0 && pointIn(mx, my, subPane):
		if r := menuPaneRowAt(subPane, items[sub].sub, mx, my); r >= 0 {
			a.fireMenuItem(&items[sub].sub[r])
		}
	case pointIn(mx, my, pane):
		if row >= 0 {
			a.fireMenuItem(&items[row])
		}
	case pointIn(mx, my, a.menuBar.strip):
		// The strip's empty stretch: swallow, but keep the menu open (a press on
		// the bar's own background is not a dismissal).
	default:
		a.closeMenuBar()
	}
	// The click landed on the bar or dismissed it, so it is OURS: swallow it. The
	// modal fence has already made every hovering()-based widget inert, but the raw
	// pointIn sites (the layout editors, an open dropdown's rows) would otherwise
	// still see the press the user meant as "put the menu away".
	c.clicked, c.rightClicked = false, false
}

// fireMenuItem runs one row: a disabled row and a separator do nothing, a submenu
// row just opens its child, and anything else fires its action and closes the menu
// (the roster menu's discipline — a resolved action never leaves the popup up).
func (a *App) fireMenuItem(it *menuItem) {
	if !a.menuItemEnabled(it) {
		return
	}
	if it.kind == menuItemSubmenu {
		a.menuSub = it.label
		return
	}
	if it.act != nil {
		it.act(a, it.arg)
	}
	a.closeMenuBar()
}

// --- phase 2: paint (AFTER the screens) ---------------------------------------------

// drawMenuBar paints the strip and any open pane, REPLAYING the latch phase 1
// computed rather than re-deriving it. Call it after the screen dispatch: the kit is
// single-pass, so the last thing painted is the thing on top.
//
// The one thing it does not replay is whether to paint at all: that is the frame's last
// undecided bit, and menuBarPaintsNow settles it here. Everything the paint uses —
// geometry, the open pane, hover eligibility — is still the latch, so a bar recovered
// on a dismiss frame paints exactly what phase 1 would have (a bare, inert strip: no
// pane, no highlight) rather than a second opinion.
func (a *App) drawMenuBar(w, _ int32) {
	if !a.menuBarPaintsNow() {
		return
	}
	c := a.ctx
	col := menuBarColors()
	c.Fill(a.menuBar.strip, col.bg)
	// Hairline along the bottom edge, so the bar reads as a separate band from
	// whatever the screen painted under it.
	c.Fill(sdl.Rect{X: 0, Y: menuBarH - menuBarHairlineH, W: w, H: menuBarHairlineH}, col.hi)
	textY := a.menuBarTextY(menuBarH)
	for i := range menuBarMenus {
		r := a.menuBarTitleRect(i)
		if i == a.menuBar.open || a.menuBarHot(r) {
			c.Fill(r, col.hi)
		}
		c.Label(r.X+menuBarTitlePadX, r.Y+textY, menuBarMenus[i].title, col.text)
	}
	// The unread "What's New" badge (#23) rides the Help TITLE, because the row it
	// belongs to now lives inside a closed pane and a nag nobody can see is not a
	// nag. Same predicate and same dot the lobby button carried before the row moved
	// in here, so opening What's New still clears it.
	if a.changelogUnread() {
		if hi := menuBarIndexOf(menuHelpTitle); hi >= 0 {
			a.drawUnreadDot(menuBarBadgeAnchor(a.menuBarTitleRect(hi)))
		}
	}
	if a.menuBar.open < 0 {
		return
	}
	items := menuBarMenus[a.menuBar.open].items
	a.drawMenuPane(a.menuBar.pane, items, col)
	if a.menuBar.subRow >= 0 {
		a.drawMenuPane(a.menuBar.sub, items[a.menuBar.subRow].sub, col)
	}
}

// menuBarHot is THE hover-feedback rule, shared by the strip's titles and the panes'
// rows so the two can never gate differently.
//
// Both halves of the gate replay the LATCH, never live state: `live` is "phase 1
// published our fences and may take input", `inert` is "a blocking window modal owned
// the frame when phase 1 ran". Highlighting under a cursor whose click the bar has
// already refused advertises a control that is not there — the same input-vs-pixels
// lie menuPaneRowFits exists to prevent one level down.
func (a *App) menuBarHot(r sdl.Rect) bool {
	return a.menuBar.live && !a.menuBar.inert && pointIn(a.ctx.mouseX, a.ctx.mouseY, r)
}

// menuBarBadgeAnchor turns a strip title's rect into the anchor drawUnreadDot wants.
// That helper hangs its dot off the anchor's top-right corner and two pixels ABOVE
// it — fine on a button mid-screen, but on a strip whose Y is 0 half the badge would
// be off the top of the window. See menuBarBadgeDropY / menuBarBadgeInsetX.
func menuBarBadgeAnchor(r sdl.Rect) sdl.Rect {
	return sdl.Rect{X: r.X, Y: r.Y + menuBarBadgeDropY, W: r.W - menuBarBadgeInsetX, H: r.H}
}

// menuBarTextY is the y OFFSET inside a rowH-tall row that vertically centres the
// chrome font's line box. Split out because the strip and the panes use different
// row heights.
func (a *App) menuBarTextY(rowHeight int32) int32 {
	if a.ctx.font == nil {
		return 0 // headless test Ctx; the real kit always has the chrome font
	}
	return (rowHeight - int32(a.ctx.font.Height())) / 2
}

// drawMenuPane paints one pane's background, border and rows. Hit-testing here is
// raw pointIn for the same reason the input phase's is, and it reads the same
// cursor, so the highlight and the click always agree.
func (a *App) drawMenuPane(pane sdl.Rect, items []menuItem, col menuBarChrome) {
	c := a.ctx
	c.Fill(pane, col.bg)
	c.Border(pane, col.border)
	textY := a.menuBarTextY(menuPaneRowH)
	for i := range items {
		it := &items[i]
		row := menuPaneRowRect(pane, items, i)
		if !menuPaneRowFits(pane, row) {
			break // past a clamped pane's bottom edge — the same rule menuPaneRowAt applies
		}
		if it.kind == menuItemSeparator {
			c.Fill(sdl.Rect{
				X: row.X + menuPaneLabelPadX, Y: row.Y + row.H/2,
				W: row.W - 2*menuPaneLabelPadX, H: menuBarHairlineH,
			}, col.dim)
			continue
		}
		on := a.menuItemEnabled(it)
		ink := col.text
		if !on {
			ink = col.dim
		}
		// Highlight the row under the cursor, and keep an open submenu's parent lit
		// while the cursor is away in the child pane.
		if (on && a.menuBarHot(row)) ||
			(it.kind == menuItemSubmenu && a.menuSub == it.label) {
			c.Fill(row, col.hi)
		}
		if it.kind == menuItemCheck && it.checked != nil && it.checked(a, it.arg) {
			c.Label(row.X+menuPaneLabelPadX, row.Y+textY, menuBarCheckGlyph, col.border)
		}
		// The label is clipped to the room left by the gutter and the right padding —
		// menuPaneSize already sized the pane for it, so this only bites when a future
		// row's label is genuinely longer than the window is wide.
		c.LabelClipped(row.X+menuPaneGutterW, row.Y+textY,
			row.W-menuPaneGutterW-menuPaneLabelPadX, it.label, ink)
		switch {
		case it.shortcut != "":
			c.Label(row.X+row.W-menuPaneLabelPadX-c.TextWidth(it.shortcut), row.Y+textY, it.shortcut, col.dim)
		case it.kind == menuItemSubmenu:
			c.Label(row.X+row.W-menuPaneLabelPadX-c.TextWidth(menuBarSubmenuGlyph), row.Y+textY, menuBarSubmenuGlyph, col.dim)
		}
	}
}
