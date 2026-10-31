package ui

// The app-wide menu bar: a persistent top strip with dropdown menus. Row handlers call the function the on-screen
// control they replace calls, so the two can't drift.
//
// menuBarFrame must run above the screen dispatch; drawMenuBar runs after the screens because the kit is single-pass
// and the last paint is on top.
// Registry order is paint order, and the compact toolbox's owner-suspend ignores rects published at or after its mark
// (overlayfence.go). A pane published late would be suspended with the toolbox strip, and the toolbox would hit-test
// straight through it.
// A layout editor owns the top band, so the strip stands down (menuBarPaints); that is re-decided after menuBarInput
// and settled at the paint site by menuBarPaintsNow. menuBarHeight follows none of it.
//
// While a pane is open the bar holds c.modalOn and must release it the frame it closes: an unreleased modalOn freezes
// the whole UI.
// The bar hit-tests with raw pointIn because its own modal state blanks hovering(). A click outside the strip and pane
// closes it and is swallowed.
// Escape closes through closeTopOverlay; a popup that skips it lets Esc leave the screen and reappears on return.
//
// Colours come from activeKitColors (the user's chrome preset), not the Col* globals an AO2 theme overwrites, so the
// bar stays readable over any background.
// The bar draws on every screen, so nothing here may allocate per frame: the tables hold non-capturing function values.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// menuBarH is the strip's height in logical px, matching the kit's row pitch so a screen offsets by one standard
	// row.
	menuBarH = int32(22)
	// menuBarPadX insets the first title from the window's left edge.
	menuBarPadX = int32(4)
	// menuBarTitlePadX is the horizontal padding either side of a top-level title.
	menuBarTitlePadX = int32(9)
	// menuPaneRowH is one ordinary pane row's height.
	menuPaneRowH = int32(23)
	// menuPaneSepH is a separator row's height: a one-pixel rule plus spacing.
	menuPaneSepH = int32(9)
	// menuPanePadY is the pane's inner top/bottom margin.
	menuPanePadY = int32(3)
	// menuPaneGutterW is the check-mark column; fixed for every row so labels in one pane share a left edge.
	menuPaneGutterW = int32(20)
	// menuPaneLabelPadX is the padding on the pane's right edge and the separator rule's inset.
	menuPaneLabelPadX = int32(10)
	// menuPaneTailGapW is the minimum gap between a row's label and its shortcut hint or submenu arrow.
	menuPaneTailGapW = int32(28)
	// menuPaneMinW keeps a pane of short labels from collapsing.
	menuPaneMinW = int32(160)
	// menuPaneEdgePad is how far a pane stays clear of the window's right and bottom edges.
	menuPaneEdgePad = int32(4)
	// menuPaneBorderPx is the pane's frame, which rows inset by on both sides so a hover fill stops at the border.
	menuPaneBorderPx = int32(1)
	// menuSubmenuOverlapX tucks a submenu back under its parent's border so the panes read as joined.
	menuSubmenuOverlapX = int32(1)
	// menuBarCheckGlyph marks a checked row, menuBarSubmenuGlyph a row that opens a child pane.
	menuBarCheckGlyph   = "✓"
	menuBarSubmenuGlyph = "▸"
	// menuBarHairlineH is the rule along the strip's bottom edge.
	menuBarHairlineH = int32(1)
)

// Named indices into activeKitColors / defaultKitColors (ui.go), a fixed [7]sdl.Color (hard rule 9).
const (
	kitColBackground = iota
	kitColPanel
	kitColPanelHi
	kitColAccent
	kitColText
	kitColTextDim
	kitColDanger
)

// menuBarChrome is the bar's resolved palette for a frame: client chrome colours, never the Col* globals.
type menuBarChrome struct {
	bg     sdl.Color // strip + pane fill
	hi     sdl.Color // hovered / open title, hovered row, the strip's hairline
	border sdl.Color // pane border, check mark
	text   sdl.Color // enabled label
	dim    sdl.Color // disabled label, shortcut hint, separator rule
}

// menuBarColors reads the user's chrome preset, not the Col* globals, which an AO2 theme's stylesheet rewrites.
func menuBarColors() menuBarChrome {
	return menuBarChrome{
		bg:     activeKitColors[kitColPanel],
		hi:     activeKitColors[kitColPanelHi],
		border: activeKitColors[kitColAccent],
		text:   activeKitColors[kitColText],
		dim:    activeKitColors[kitColTextDim],
	}
}

type menuItemKind int

const (
	menuItemAction    menuItemKind = iota // a plain command row
	menuItemSeparator                     // a horizontal rule; never hovers, never fires
	menuItemCheck                         // a toggle; draws a check mark when checked() is true
	menuItemSubmenu                       // opens a child pane beside its parent row
)

// menuItem is one row of a menu. checked, enabled and act take arg so table entries stay non-capturing functions.
// label is also the key menuSub remembers a submenu by. A nil checked is never checked, a nil enabled is always
// enabled, a nil act is inert; act fires on click, then the menu closes.
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

type menu struct {
	title string
	items []menuItem
}

// menuBarMenus is the bar's model, in left-to-right order. A row that can't act right now is dimmed rather than
// removed, so a menu's shape doesn't change under the cursor.
var menuBarMenus = []menu{
	{menuServersTitle, menuServersItems},
	{"View", menuViewItems},
	{"Window", menuWindowItems},
	{menuExtrasTitle, menuExtrasItems},
	{menuHelpTitle, menuHelpItems},
}

const (
	// Named because code outside the table looks these panes up, and the open pane is remembered by title.
	menuServersTitle = "Servers"
	menuExtrasTitle  = "Extras"
	menuHelpTitle    = "Help"
)

// menuServersItems is the lobby's server plumbing. "Direct connect…" focuses the field the lobby draws.
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
	// F3 is hard-coded in App.Frame, not a rebindable hotkey.
	{kind: menuItemCheck, label: "Performance overlay", shortcut: "F3", checked: menuPerfHUDOn, act: menuTogglePerfHUD},
}

// menuWindowItems all route through App.applyWindowSize, which leaves fullscreen and maximized, clamps and persists.
var menuWindowItems = []menuItem{
	{kind: menuItemAction, label: "Restore default size", act: menuActDefaultWindowSize},
	{kind: menuItemAction, label: "Theme's design size", enabled: menuThemeDesignSizeOffered, act: menuActThemeDesignSize},
	{kind: menuItemAction, label: "Fit to screen", act: menuActFitToScreen},
	{kind: menuItemSeparator},
	// F11 is likewise hard-coded (App.Frame's fullscreenReq arm).
	{kind: menuItemCheck, label: "Fullscreen", shortcut: "F11", checked: menuFullscreenOn, act: menuToggleFullscreen},
}

// menuExtrasItems is the chrome home for AsyncAO's own controls. The floating toolbox survives as a movable,
// theme-placeable box and the layout editor's drag handle.
var menuExtrasItems = []menuItem{
	// A written Wardrobe path from every screen: the band's Iniswap chip is dropped when the band gets tight.
	{kind: menuItemAction, label: "Wardrobe / iniswap…", enabled: menuInCourtroom, act: menuActWardrobe},
	{kind: menuItemCheck, label: "AsyncAO Extras box", checked: menuExtrasBoxOpen, enabled: menuInCourtroom, act: menuToggleExtrasBox},
	{kind: menuItemCheck, label: "Restyle character…", checked: menuSpriteStyleOpen, enabled: menuInCourtroom, act: menuToggleSpriteStyle},
	// Text FX and the emoji picker have no AO2 design key. The themed IC bar draws them only at override rects no stock
	// theme declares, and icEffect is sticky, so without these rows a themed player couldn't clear an armed effect.
	{kind: menuItemCheck, label: "Text FX…", checked: menuFxPickerOpen, enabled: menuInCourtroom, act: menuToggleFxPicker},
	{kind: menuItemCheck, label: "Emoji picker…", checked: menuEmojiPickerOpen, enabled: menuInCourtroom, act: menuToggleEmojiPicker},
	{kind: menuItemSeparator},
	{kind: menuItemCheck, label: "Mod dashboard", checked: menuModDashOpen, enabled: menuAmIMod, act: menuActModDash},
	{kind: menuItemCheck, label: "CM area controls", checked: menuCMPanelOpen, enabled: menuAmICM, act: menuActCMPanel},
	{kind: menuItemSeparator},
	{kind: menuItemAction, label: "Theater mode", enabled: menuInCourtroom, act: menuActTheater},
	{kind: menuItemAction, label: "Edit layout…", enabled: menuInCourtroom, act: menuActEditLayout},
	{kind: menuItemCheck, label: "Hide UI pieces…", checked: menuToolboxPiecesOpen, enabled: menuInCourtroom, act: menuActHideUIPieces},
	{kind: menuItemCheck, label: "Floating toolbox", checked: menuToolboxShown, act: menuToggleToolbox},
	{kind: menuItemSeparator},
	{kind: menuItemAction, label: "Settings", act: menuOpenSettings},
}

// menuHelpItems is reference material useful from any screen. What's New lives on the lobby header instead
// (screens.go).
var menuHelpItems = []menuItem{
	{kind: menuItemAction, label: "Hotkeys", shortcut: "F1", act: menuToggleHotkeys},
	{kind: menuItemSeparator},
	// arg is openHelp's section index.
	{kind: menuItemAction, label: "Privacy", arg: helpTabPrivacy, act: menuOpenHelpTab},
	{kind: menuItemAction, label: "Glossary", arg: helpTabGlossary, act: menuOpenHelpTab},
	{kind: menuItemAction, label: "Chat logs", act: menuOpenLogs},
	{kind: menuItemSeparator},
	{kind: menuItemAction, label: "For server owners…", act: menuOpenServerHelp},
	{kind: menuItemAction, label: "About AsyncAO", act: menuOpenAbout},
}

const (
	// menuThemeFitLabel is the submenu row's label and also the key menuSub remembers the open child by.
	menuThemeFitLabel = "Theme fit"
	// helpTabGlossary / helpTabPrivacy are openHelp's section indexes, positions in helpSectionNames (help.go).
	helpTabGlossary = 0
	helpTabPrivacy  = 1
)

// menuThemeFitItems is the "Theme fit ▸" submenu, derived from themeFitOptions so the two can't drift. The mode
// constants are persisted, so they stay independent of row order.
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

// The Phone Book and Direct-connect rows jump to the lobby first: they are reachable from the courtroom, and on a
// screen that draws neither they would be a silent no-op.

func menuCanRefreshServers(a *App, _ int) bool { return !a.lobbyFetching }
func menuActRefreshServers(a *App, _ int)      { a.RefreshServers() }

func menuPhoneBookOn(a *App, _ int) bool { return a.phoneBookPage }
func menuTogglePhoneBook(a *App, _ int) {
	a.menuGoto(ScreenLobby)
	a.setPhoneBookPage(!a.phoneBookPage)
}

// menuCanPingSort dims the row during a sweep: startPinging refuses a second one, which would race its own results.
func menuCanPingSort(a *App, _ int) bool { return !a.pinging }
func menuPingSortOn(a *App, _ int) bool  { return a.pingMode }
func menuTogglePingSort(a *App, _ int) {
	a.menuGoto(ScreenLobby)
	a.toggleConnectTimeSort()
}

// menuActDirectConnect focuses the lobby's direct-connect field. The field only draws on the all-servers page, and
// FocusField silently drops an id nothing draws.
func menuActDirectConnect(a *App, _ int) {
	a.menuGoto(ScreenLobby)
	a.setPhoneBookPage(false)
	a.ctx.FocusField(directConnectFieldID)
}

func menuInSession(a *App, _ int) bool { return a.sess != nil }

func menuActDisconnect(a *App, _ int) { a.requestDisconnect() } // routed through the confirm, like every other exit
func menuActQuit(a *App, _ int)       { a.requestQuit() }

// menuAspectKept reports whether the fit mode preserves the canvas's aspect ratio; only Stretch doesn't.
func menuAspectKept(a *App, _ int) bool { return a.d.Prefs.Load().ThemeFit != config.ThemeFitStretch }

// menuToggleAspect goes to Stretch when unticked and back to the default fit when re-ticked: a remembered previous mode
// would need state that survives a restart.
func menuToggleAspect(a *App, _ int) {
	if menuAspectKept(a, 0) {
		menuSetThemeFit(a, config.ThemeFitStretch)
		return
	}
	menuSetThemeFit(a, config.DefaultThemeFit)
}

func menuPlainBackdropsOn(a *App, _ int) bool { return a.d.Prefs.Load().PlainLobby }
func menuTogglePlainBackdrops(a *App, _ int) {
	{
		pv1 := !a.d.Prefs.Load().PlainLobby
		a.d.Prefs.Update(func(sn *config.Settings) {
			sn.PlainLobby = pv1
		})
	}
}

func menuPerfHUDOn(a *App, _ int) bool { return a.perfHUD }
func menuTogglePerfHUD(a *App, _ int)  { a.perfHUD = !a.perfHUD }

func menuActDefaultWindowSize(a *App, _ int) {
	a.applyWindowSize(config.DefaultWindowW, config.DefaultWindowH)
}

// menuThemeDesignSizeOffered reuses the predicate the Settings row and snap-on-import consult.
func menuThemeDesignSizeOffered(a *App, _ int) bool {
	_, _, ok := a.themeDesignSizeOffer()
	return ok
}
func menuActThemeDesignSize(a *App, _ int) { a.applyThemeDesignWindowSize() }

func menuActFitToScreen(a *App, _ int) { a.fitWindowToScreen() }

func menuFullscreenOn(a *App, _ int) bool { return a.d.Prefs.Load().WindowFull }
func menuToggleFullscreen(a *App, _ int)  { a.toggleFullscreen() }

// menuInCourtroom enables the courtroom-only rows. Arming an editor from the lobby is worse than a no-op: the strip
// stops painting on a screen that never draws the editor's way out.
func menuInCourtroom(a *App, _ int) bool { return a.screen == ScreenCourtroom }

// These call the toolbox's own methods, so the menu, chips, command palette and hotkeys are one implementation each.
func menuActTheater(a *App, _ int)      { a.compactTheater() }
func menuActEditLayout(a *App, _ int)   { a.compactEditLayout() }
func menuActHideUIPieces(a *App, _ int) { a.compactHideUI() }

func menuToolboxPiecesOpen(a *App, _ int) bool { return a.toolboxPinned && a.toolboxPieces }

// Package-level funcs, never method values: a method value in the menu table allocates a closure, and the themed frame
// must not allocate.
func menuExtrasBoxOpen(a *App, _ int) bool { return a.showWidgets }
func menuToggleExtrasBox(a *App, _ int)    { a.showWidgets = !a.showWidgets }

// menuActWardrobe opens the Wardrobe on its browse tab, the same entry the Iniswap chip uses (iniswapchip.go).
func menuActWardrobe(a *App, _ int) { a.openIniswapBrowse() }

func menuSpriteStyleOpen(a *App, _ int) bool { return a.showStyleBox }
func menuToggleSpriteStyle(a *App, _ int)    { a.openSpriteStyle() }

// Text FX and the emoji picker have no AO2 design key, so this menu is the only place a themed player reaches them.
func menuFxPickerOpen(a *App, _ int) bool { return a.showFxPicker }
func menuToggleFxPicker(a *App, _ int)    { a.showFxPicker = !a.showFxPicker }

func menuEmojiPickerOpen(a *App, _ int) bool { return a.showEmojiPicker }
func menuToggleEmojiPicker(a *App, _ int)    { a.showEmojiPicker = !a.showEmojiPicker }

// Mod and CM rows grey out rather than hide, so the row still says the command exists until the server grants it.
func menuAmIMod(a *App, _ int) bool      { return a.amIMod() }
func menuModDashOpen(a *App, _ int) bool { return a.showModDash }
func menuActModDash(a *App, _ int)       { a.toggleModDash() }

func menuAmICM(a *App, _ int) bool       { return a.amICMNow }
func menuCMPanelOpen(a *App, _ int) bool { return a.showCMPanel }
func menuActCMPanel(a *App, _ int)       { a.toggleCMPanel() }

func menuToolboxShown(a *App, _ int) bool { return !a.panelHidden(panelToolbox) }
func menuToggleToolbox(a *App, _ int) {
	a.setPanelHiddenGuarded(panelToolbox, !a.panelHidden(panelToolbox))
}

func menuOpenSettings(a *App, _ int) { a.menuGoto(ScreenSettings) }

func menuOpenAbout(a *App, _ int) { a.menuGoto(ScreenAbout) }

// menuOpenHelpTab opens the Help screen on one section; re-picking while on Help must not overwrite prevScreen.
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

// menuToggleHotkeys mirrors App.Frame's F1 arm, including dropping the cached rows.
func menuToggleHotkeys(a *App, _ int) {
	if a.showHotkeys {
		a.showHotkeys = false
		a.hkCache = nil
		return
	}
	a.openHotkeyCheatSheet()
}

// menuGoto enters a full-screen menu, remembering prevScreen. Re-selecting the current screen must not overwrite it, or
// Esc would bounce between it and itself.
func (a *App) menuGoto(s Screen) {
	if a.screen == s {
		return
	}
	a.prevScreen = a.screen
	a.screen = s
}

func menuThemeFitIs(a *App, mode int) bool { return a.d.Prefs.Load().ThemeFit == mode }

func menuSetThemeFit(a *App, mode int) {
	a.d.Prefs.SetThemeFit(mode)
	a.invalidateThemeCanvases() // both canvases re-fit under the new mode (the settings.go idiom)
}

// menuBarLatch is this frame's single menu-bar decision: phase 1 computes it, phase 2 replays it. A screen draw sits
// between the two and can change the state they read, and a pointer block that disagrees with the pixels eats clicks
// over nothing.
type menuBarLatch struct {
	// draws is whether the strip paints at all this frame.
	draws bool
	// live means the bar published its pointer blocks and may take input; false while a later overlay owns the pointer,
	// though the strip still paints.
	live bool
	// inert is menuBarSuppressed() as of phase 1: the strip paints but takes no input and offers no hover feedback.
	inert bool
	// editorHeld means phase 1 stood down for a layout editor while the band is still the bar's; the strip rect is kept
	// so menuBarPaintsNow can restore the bar if the editor never painted.
	editorHeld bool
	// strip, pane and sub are the exact rects published and painted.
	strip sdl.Rect
	pane  sdl.Rect
	sub   sdl.Rect
	// open indexes menuBarMenus (-1 closed); subRow indexes that menu's items (-1 none).
	open   int
	subRow int
	// mark is the overlay registry depth from just before our own publications.
	mark int
}

// newMenuBarLatch is the only way to build one: the zero value's open/subRow would read as menu 0 / row 0 open.
func newMenuBarLatch(mark int) menuBarLatch {
	return menuBarLatch{open: -1, subRow: -1, mark: mark}
}

// menuBarShows reports whether the strip reserves its band at all: false for full-window modes, theater mode, and when
// the user hid it, in which case they want the pixels back.
func (a *App) menuBarShows() bool {
	return !a.screenDispatchPreempted() && !a.theaterOn && !a.panelHidden(panelMenuBar)
}

// menuBarPaints is phase 1's answer to whether the strip paints: menuBarShows minus any layout editor or the theme
// editor, whose banners sit at the top of the band.
// Only the paint stands down. menuBarHeight still reports menuBarH: giving up the band would move the very widgets
// being placed.
// The theme editor's header uses kit buttons, so a strip that took input would also make them hit-test dead.
// It is not final, because a screen drawn later can flip the flag; see menuBarFrame and menuBarPaintsNow.
func (a *App) menuBarPaints() bool {
	return a.menuBarShows() && !a.layoutEditorArmed() && a.screen != ScreenThemeEditor
}

// menuBarPaintsNow is the final word on the strip's paint, because the answer can't change after the screens have run.
// It asks whether a banner reached the screen, not whether an editor is armed: the menu row, a hotkey or the toolbox
// can arm an editor after phase 1 and still paint the banner; the command palette arms after the courtroom pass, so no
// banner paints; drawClassicEditor returns before painting on the frame Done is pressed, so editorHeld puts the bar
// back, inert.
func (a *App) menuBarPaintsNow() bool {
	if a.editorBannerPainted {
		return false
	}
	return a.menuBar.draws || a.menuBar.editorHeld
}

// noteEditorBanner marks the band as painted by a layout editor; called from the statement that fills the banner, never
// the arm path. App.Frame clears it every frame.
func (a *App) noteEditorBanner() { a.editorBannerPainted = true }

// layoutEditorArmed reports that the classic editor (classiclayout.go) or the themed editor (layoutedit.go) owns the
// screen.
func (a *App) layoutEditorArmed() bool { return a.classicEdit || a.layoutEdit }

// menuBarHeight is the vertical space the bar occupies, zero when not shown. menuBarShows, not menuBarPaints: a layout
// edit must not move the band it edits against.
func (a *App) menuBarHeight() int32 {
	if !a.menuBarShows() {
		return 0
	}
	return menuBarH
}

func (a *App) menuBarOpen() bool { return menuBarIndexOf(a.menuOpen) >= 0 }

// closeMenuBar shuts the pane and any submenu. menuBarModalFence releases modalOn this frame; a caller outside the
// frame (closeTopOverlay) gets it released next frame.
func (a *App) closeMenuBar() { a.menuOpen, a.menuSub = "", "" }

// menuBarSuppressed reports when the strip must be inert though it paints: a blocking modal owns the frame, and the
// bar's modalOn would blank its buttons.
// Layout editors stand down earlier, at menuBarPaints; they are included so every caller gets one answer.
func (a *App) menuBarSuppressed() bool {
	return a.layoutEditorArmed() ||
		a.confirmDisconnect || a.pendingCloseTab != nil || a.hidePrompt != "" ||
		a.showQuitConfirm || a.disconnectDlg.open || a.serverNoticeDlg.open
}

// menuBarIndexOf maps a stored menu title to its index, or -1. Stored by title so the &App{} zero value is closed and
// reordering the model can't reinterpret the open pane.
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

// menuItemIndexOf maps a stored item label to its row index, or -1, for the same reasons as menuBarIndexOf.
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

// menuItemEnabled resolves a row's enabled predicate; separators are never enabled.
func (a *App) menuItemEnabled(it *menuItem) bool {
	if it.kind == menuItemSeparator {
		return false
	}
	if it.enabled == nil {
		return true
	}
	return it.enabled(a, it.arg)
}

func (a *App) menuBarTitleW(i int) int32 {
	return a.ctx.TextWidth(menuBarMenus[i].title) + 2*menuBarTitlePadX
}

// menuBarTitleRect is the i-th title's rect, walked rather than cached because c.TextWidth is memoized and allocates
// nothing.
func (a *App) menuBarTitleRect(i int) sdl.Rect {
	x := menuBarPadX
	for j := 0; j < i; j++ {
		x += a.menuBarTitleW(j)
	}
	return sdl.Rect{X: x, Y: 0, W: a.menuBarTitleW(i), H: menuBarH}
}

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

func menuItemHeight(k menuItemKind) int32 {
	if k == menuItemSeparator {
		return menuPaneSepH
	}
	return menuPaneRowH
}

// menuPaneSize measures a pane: the widest row (at least menuPaneMinW) and every row's height plus margins.
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

// menuPaneRect places the mi-th menu's pane under its title, kept on screen horizontally.
func (a *App) menuPaneRect(mi int, w, h int32) sdl.Rect {
	paneW, paneH := a.menuPaneSize(menuBarMenus[mi].items)
	x := a.menuBarTitleRect(mi).X
	if x+paneW > w-menuPaneEdgePad {
		x = w - menuPaneEdgePad - paneW
	}
	if x < menuPaneEdgePad {
		x = menuPaneEdgePad
	}
	// A pane too tall is clamped, never moved up: it must not cover the bar that owns it. Rows past the clamp go
	// through menuPaneRowFits.
	y := menuBarH
	if room := h - menuPaneEdgePad - y; paneH > room && room > 0 {
		paneH = room
	}
	return sdl.Rect{X: x, Y: y, W: paneW, H: paneH}
}

// menuPaneRowRect is the idx-th row's rect, inset by the pane's border on both sides.
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

// menuPaneRowFits reports whether a row is wholly inside a clamped pane. Draw and hit test share it, or the undrawn
// last row would be clickable through the pixels above the pane's bottom edge.
func menuPaneRowFits(pane, row sdl.Rect) bool { return row.Y+row.H <= pane.Y+pane.H }

// menuPaneRowAt returns the row under (x, y), or -1, also for a separator or a row past a clamped pane's bottom.
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

// menuSubRect places the child pane against the parent's right border, flipping left at the window edge, first row
// lined up with the parent row.
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

// menuBarFrame resolves the bar for this frame. It must be called above the screen dispatch; see the top of this file.
func (a *App) menuBarFrame(w, h int32) {
	c := a.ctx
	a.menuBar = newMenuBarLatch(c.overlayFenceMark())
	// menuBarPaints, not menuBarShows: a layout editor owns the band's pixels and input, so the bar stands down whole
	// while its height stays reserved.
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
	// Publishing follows the pixels, input follows ownership. c.ptrFenced means a later overlay owns the pointer, and a
	// rect left in the registry would blank its widgets, so publish nothing. Otherwise the strip is painted over the
	// band, so screens must not hit-test under it.
	a.menuBar.live = !c.ptrFenced
	// The Iniswap chip shares this band, so claim its click before the strip's rect covers it.
	if a.menuBar.live && !a.menuBar.inert {
		a.handleIniswapChipInput(w)
	}
	if a.menuBar.live {
		c.fenceOverlay(a.menuBar.strip)
	}
	if a.menuBar.live && !a.menuBar.inert {
		a.menuBarInput(w, h)
	}
	// menuBarInput may have just armed a layout editor, leaving the decision above stale, so re-decide and drop what
	// was published.
	if !a.menuBarPaints() {
		a.closeMenuBar()
		c.overlayFenceRelease(a.menuBar.mark) // a strip that paints nothing fences nothing
		a.menuBar = newMenuBarLatch(a.menuBar.mark)
		a.menuBarStandDown(w)
		a.menuBarModalFence(c)
		return
	}
	// Resolve the open pane after the input, which may have opened or closed one, so the published rect is the one
	// painted.
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

// menuBarStandDown records a stand-down caused by a layout editor, keeping the strip rect for menuBarPaintsNow.
// Full-window modes have no band, so they record nothing.
func (a *App) menuBarStandDown(w int32) {
	if !a.menuBarShows() {
		return
	}
	a.menuBar.editorHeld = true
	a.menuBar.strip = sdl.Rect{X: 0, Y: 0, W: w, H: menuBarH}
}

// menuBarModalFence holds c.modalOn while a pane is open and releases it the frame it closes; an unreleased one freezes
// the UI.
// c.modalOn is shared with sibling popups, so the release stands down if one holds it; otherwise the courtroom under an
// open roster menu goes live for a frame.
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

// otherModalFenceHeld reports whether a sibling popup holds c.modalOn. They all run above the bar in App.Frame, so
// their flags are this frame's answer.
func (a *App) otherModalFenceHeld() bool {
	return a.emojiFenceOn || a.reactFenceOn || a.rosterMenuFenceOn || a.musicMenuFenceOn || a.updateFenceOn
}

// menuBarInput resolves the bar's own interaction, with raw pointIn because the modal state blanks hovering().
func (a *App) menuBarInput(w, h int32) {
	c := a.ctx
	mx, my := c.mouseX, c.mouseY
	title := a.menuBarTitleAt(mx, my)
	open := menuBarIndexOf(a.menuOpen)

	switch {
	case title >= 0 && c.clicked:
		if open == title {
			a.closeMenuBar()
		} else {
			a.menuOpen, a.menuSub = menuBarMenus[title].title, ""
		}
		c.clicked = false
	case title >= 0 && open >= 0 && title != open:
		// Once a pane is open, sliding across the strip moves it with the cursor.
		a.menuOpen, a.menuSub = menuBarMenus[title].title, ""
	}

	open = menuBarIndexOf(a.menuOpen)
	if open < 0 {
		// A click that missed the strip is not ours.
		return
	}
	items := menuBarMenus[open].items
	pane := a.menuPaneRect(open, w, h)
	row := menuPaneRowAt(pane, items, mx, my)

	// Hovering the parent pane drives the submenu: a submenu row opens it, any other row closes it. Off the pane
	// changes nothing, for travel into the submenu.
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
		// The strip's empty stretch: swallow, but a press on the bar's background is not a dismissal.
	default:
		a.closeMenuBar()
	}
	// Swallow the click: raw pointIn sites (layout editors, an open dropdown's rows) would otherwise see the press
	// meant to put the menu away.
	c.clicked, c.rightClicked = false, false
}

// fireMenuItem runs one row: disabled and separator rows do nothing, a submenu opens its child, anything else fires and
// closes the menu.
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

// drawMenuBar paints the strip, replaying phase 1's decision. Only whether to paint at all is settled here, by
// menuBarPaintsNow.
func (a *App) drawMenuBar(w, _ int32) {
	if !a.menuBarPaintsNow() {
		return
	}
	c := a.ctx
	col := menuBarColors()
	c.Fill(a.menuBar.strip, col.bg)
	c.Fill(sdl.Rect{X: 0, Y: menuBarH - menuBarHairlineH, W: w, H: menuBarHairlineH}, col.hi)
	textY := a.menuBarTextY(menuBarH)
	for i := range menuBarMenus {
		r := a.menuBarTitleRect(i)
		if i == a.menuBar.open || a.menuBarHot(r) {
			c.Fill(r, col.hi)
		}
		c.Label(r.X+menuBarTitlePadX, r.Y+textY, menuBarMenus[i].title, col.text)
	}
	// The band's chips are drawn only here so they can't intrude on an AO2 design canvas or the emote grid. The
	// missing-asset chip measures its clearance against the Iniswap button and the theme-sidecar chip's anchor.
	a.drawIniswapChip(w)
	a.drawAssetMissChip(w)
	a.drawThemeErrChip(w)
}

// drawMenuPanes paints the open pane and submenu after the diagnostics overlays: the strip belongs under the F3 and
// debug overlays, a popup above them. Safe to call unconditionally.
func (a *App) drawMenuPanes() {
	if !a.menuBarPaintsNow() || a.menuBar.open < 0 {
		return
	}
	col := menuBarColors()
	items := menuBarMenus[a.menuBar.open].items
	a.drawMenuPane(a.menuBar.pane, items, col)
	if a.menuBar.subRow >= 0 {
		a.drawMenuPane(a.menuBar.sub, items[a.menuBar.subRow].sub, col)
	}
}

// menuBarHot is the hover rule for titles and rows. It reads phase 1's state: highlighting under a cursor whose click
// the bar refused advertises a control that isn't there.
func (a *App) menuBarHot(r sdl.Rect) bool {
	return a.menuBar.live && !a.menuBar.inert && pointIn(a.ctx.mouseX, a.ctx.mouseY, r)
}

// menuBarTextY is the y offset that vertically centres the chrome font in a row of the given height.
func (a *App) menuBarTextY(rowHeight int32) int32 {
	if a.ctx.font == nil {
		return 0 // headless test Ctx; the real kit always has the chrome font
	}
	return (rowHeight - int32(a.ctx.font.Height())) / 2
}

// drawMenuPane paints one pane's background, border and rows, with the same raw pointIn and cursor as the input phase.
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
		// Keep an open submenu's parent lit while the cursor is in the child pane.
		if (on && a.menuBarHot(row)) ||
			(it.kind == menuItemSubmenu && a.menuSub == it.label) {
			c.Fill(row, col.hi)
		}
		if it.kind == menuItemCheck && it.checked != nil && it.checked(a, it.arg) {
			c.Label(row.X+menuPaneLabelPadX, row.Y+textY, menuBarCheckGlyph, col.border)
		}
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
