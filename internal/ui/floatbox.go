package ui

// The floating Extras box: a non-blocking panel for AsyncAO features an AO2 theme has no button for.
// The kit has no z-aware input, so the courtroom pass runs pointer-blind only while the cursor is over a box
// (boxFencesPointer), then the boxes draw last with real input. Every box shares one per-frame press edge.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	extrasBoxW = int32(380) // main box default width
	extrasBoxH = int32(520) // main box default height
	extrasMinW = int32(300) // resize floor: 2 columns stay readable
	// extrasMinH is the resize floor: title, 4 volume rows, 2 cells and the hint; the grid scrolls the rest.
	extrasMinH    = int32(240)
	extrasVolRowH = int32(21) // one volume-slider row at the top of the box
	extrasTitleH  = int32(26) // title bar / drag handle height (main + torn boxes)
	extrasGripSz  = int32(16) // bottom-right resize grip
	// extrasHintH reserves the bottom hint line so the scrolling grid stops clear of it.
	extrasHintH = int32(24)
	// extrasScrollW is the scrollbar width; extrasScrollStep is one wheel notch, under a cell plus gap.
	extrasScrollW    = int32(10)
	extrasScrollStep = int32(38)

	detachedBoxW   = int32(176) // a torn-off widget's own little box (default)
	detachedBoxH   = int32(66)
	detachedMinW   = int32(120) // resize floor: the label + close still fit
	detachedMinH   = int32(54)
	detachedGripSz = int32(12) // smaller resize grip for the little torn-off boxes
	extrasTearPx   = int32(8)  // drag a grid cell this far to tear it loose
)

// extrasWidget is one entry in the Extras box: a labelled action to click or drag out into its own box.
type extrasWidget struct {
	label, desc, key string // key = hotkey id ("" = none), surfaced in the tooltip
	run              func()
}

// detachedWidget is a widget in its own box; id indexes extrasWidgets, w/h are 0 until resized.
type detachedWidget struct {
	id   int
	x, y int32
	w, h int32
}

// panelSlot is one row of panelSlotTable; fw and open are plain functions because the table is package-level
// (zero-alloc rule) and the state is per App. open gates magnetism: a closed floatWin keeps a real rect().
type panelSlot struct {
	slot                   string
	fw                     func(*App) *floatWin
	defW, defH, minW, minH int32
	open                   func(*App) bool
}

// Nominal heights for panels whose real default is computed at draw time; only the magnetism list uses them.
const (
	panelNomBanH    = int32(600) // banBoxDims default (ban kind) height
	panelNomBanMinH = int32(556) // banBoxDims default (ban kind) minimum height
	panelNomHKH     = int32(520) // hotkey sheet's usual opened height
	panelNomPairH   = int32(560) // pairPanelRect's default-height clamp ceiling
)

// Voice panel geometry lives here because voicepanel.go is compiled out under -tags novoice but this table is not.
const (
	voicePanelDefW = 320
	voicePanelDefH = 300
	voicePanelMinW = 240
	voicePanelMinH = 180
)

// panelSlotTable lists the persistable floatWin panels except msgWin, for applyProfile's .placed reset and the live
// magnetism list; it is iterated only on cold paths and during a drag, so no settled frame touches it.
// Adding a panel needs a slotPanel const, a row here and seed/persist wiring in its draw.
var panelSlotTable = []panelSlot{
	{slotPanelPair, func(a *App) *floatWin { return &a.pairWin }, pairPanelDefW, panelNomPairH, pairPanelMinW, pairPanelMinH, func(a *App) bool { return a.showPair }},
	// The mod dashboard hides while its ban box is open, so the magnetism list must skip it too.
	{slotPanelMod, func(a *App) *floatWin { return &a.modWin }, modDashW, modDashH, modDashMinW, modDashMinH, func(a *App) bool { return a.showModDash && a.banBoxKind == 0 }},
	{slotPanelCM, func(a *App) *floatWin { return &a.cmWin }, cmPanelDefW, cmPanelDefH, cmPanelMinW, cmPanelMinH, func(a *App) bool { return a.showCMPanel }},
	{slotPanelHK, func(a *App) *floatWin { return &a.hkWin }, hkSheetDefW, panelNomHKH, hkSheetMinW, hkSheetMinH, func(a *App) bool { return a.showHotkeys }},
	{slotPanelEvid, func(a *App) *floatWin { return &a.evidWin }, evidPanelDefW, evidPanelDefH, evidPanelMinW, evidPanelMinH, func(a *App) bool { return a.showEvid }},
	{slotPanelModcall, func(a *App) *floatWin { return &a.modcallWin }, modcallPanelDefW, modcallPanelDefH, modcallPanelMinW, modcallPanelMinH, func(a *App) bool { return a.showModcall }},
	{slotPanelVoice, func(a *App) *floatWin { return &a.voiceWin }, voicePanelDefW, voicePanelDefH, voicePanelMinW, voicePanelMinH, func(a *App) bool { return a.showVoice }},
	{slotPanelBan, func(a *App) *floatWin { return &a.banWin }, banBoxDefW, panelNomBanH, banBoxMinW, panelNomBanMinH, func(a *App) bool { return a.banBoxKind != 0 }},
	{slotPanelDebug, func(a *App) *floatWin { return &a.debugWin }, debugPanelW, debugPanelH, debugPanelMinW, debugPanelMinH, func(a *App) bool { return a.showDebugPanel }},
	{slotPanelClient, func(a *App) *floatWin { return &a.clientWin }, clientWinDefW, clientWinDefH, clientWinMinW, clientWinMinH, func(a *App) bool { return a.splitActive() }},
	{slotPanelPieces, func(a *App) *floatWin { return &a.piecesWin }, toolboxPiecesW, toolboxPiecesMaxH, toolboxPiecesMinW, toolboxPiecesMinH, func(a *App) bool { return a.toolboxPinned && a.toolboxPieces }},
	{slotPanelTimer, func(a *App) *floatWin { return &a.timerWin }, timerPanelDefW, timerPanelDefH, timerPanelMinW, timerPanelMinH, func(a *App) bool { return a.showTimer }},
	{slotPanelLogin, func(a *App) *floatWin { return &a.loginWin }, loginPanelDefW, loginPanelDefH, loginPanelMinW, loginPanelMinH, func(a *App) bool { return a.showLogin }},
	{slotPanelPairPop, func(a *App) *floatWin { return &a.pairPopWin }, pairPopPanelDefW, pairPopPanelDefH, pairPopPanelMinW, pairPopPanelMinH, func(a *App) bool { return a.pairPopupOpen }},
}

// extrasWidgets returns the widget table, built once; the closures capture the stable *App, so caching is safe.
func (a *App) extrasWidgets() []extrasWidget {
	if a.extrasWidgetCache == nil {
		a.extrasWidgetCache = []extrasWidget{
			{"Character", "Open character select", hotkeyCharMenu, func() { a.screen = ScreenCharSelect }},
			{"Random char", "Swap to a random free character", hotkeyRandomChar, func() { a.randomChar() }},
			{"Wardrobe", "Iniswap — borrow another character's look", hotkeyWardrobe, func() { a.openIniswap() }},
			{"Jukebox", "Your saved music playlists", hotkeyJukebox, func() { a.openIniswap(); a.wardSection = wardSectionJukebox }},
			{"Background", "Change the courtroom background", hotkeyBackground, func() { a.openBgPicker() }},
			{"Evidence", "Add / view case evidence", hotkeyEvidence, func() { a.toggleEvidence() }},
			{"Call Mod", "Call a moderator to this room", hotkeyModcall, func() { a.showModcall = true }},
			{"Mod / CM", "Server-aware moderation + room (CM) controls — ban/kick with a live command preview", hotkeyModDash, func() { a.toggleModDash() }},
			{"Pair", "Pair up — share the stage with another character", hotkeyPairMenu, func() { a.showPair = true }},
			{"Login", "Log in with saved credentials", hotkeyLogin, func() { a.openLoginDialog() }},
			{"★ Fav Emotes", "Floating box of just your starred emotes (Ctrl+A)", hotkeyFavEmotes, func() {
				a.d.Prefs.Update(func(
					sn *config.
						Settings) {

					sn.
						FavEmoteBox =

						true

				})

			}},
			{"Hotkeys", "Show every keyboard shortcut, including your custom ones (F1)", "", func() { a.openHotkeyCheatSheet() }},
			{"Debug", "Diagnostics: server software, live packet inspector, performance + asset/prefetch stats, and the failure log (F8, or Settings → Power user)", "", func() { a.toggleDebugPanel() }},
			{"Timer", "A personal countdown timer + alarm (for RP / casing pace)", "", func() { a.openTimer() }},
			{"Sprite Style", "Recolour / glow / warp your character — other AsyncAO players see it", "", func() { a.openSpriteStyle() }},
			{"SFX Browser", "Browse, preview (▶) and favourite (★) sounds for your next IC message — incl. any sound by name", "", func() { a.toggleSfxBrowser() }},
			{"Screenshot", "Save a PNG of the current frame to the screenshots\\ folder next to AsyncAO — for sharing a moment (Ctrl+S)", hotkeyScreenshot, func() { a.captureScreenshot() }},
			{"Logs", "Search your saved chat transcripts — any server, any session, filter by text", "", func() { a.prevScreen = ScreenCourtroom; a.openLogBrowser(); a.screen = ScreenLogs }},
			{"Help", "Glossary of AO terms + a privacy explainer — what IC / OOC / CM / WTCE / HDID mean, and what a server can see", "", func() { a.prevScreen = ScreenCourtroom; a.openHelp(0) }},
			{"Group Chat", "Private DMs & group chats with other AsyncAO players — over the server's /pm, never the room", "", func() { a.toggleMessages() }},
			{voiceExtraLabel, "Voice chat (Nyathena): join the voice channel, see who's talking. Live mic audio is coming; for now it shares presence + speaking state", "", func() { a.toggleVoice() }},
			{"Settings", "Open settings", hotkeySettings, func() { a.prevScreen = ScreenCourtroom; a.screen = ScreenSettings }},
			{"Disconnect", "Leave this server", "", func() { a.requestDisconnect() }},
		}
	}
	return a.extrasWidgetCache
}

// toggleExtrasBox shows or hides the box (hotkeyExtras, or Esc in the bare courtroom);
// from theater it exits theater instead of drawing over the stage-only view.
func (a *App) toggleExtrasBox() {
	if a.theaterOn {
		a.setTheater(false)
		a.showWidgets = true
		return
	}
	a.showWidgets = !a.showWidgets
}

// extrasPalette is the resolved Extras-box theme for a frame; empty prefs keep the stock kit colours.
type extrasPalette struct {
	bg, bg2, border, title, text sdl.Color
	gradient                     bool
}

// extrasPalette resolves the hex prefs over the stock palette; blank or invalid entries keep the stock colour.
func (a *App) extrasPalette() extrasPalette {
	bgH, bg2H, brH, tiH, txH, grad := a.d.Prefs.Load().ExtrasBoxStyle()
	p := extrasPalette{bg: ColPanel, bg2: ColPanel, border: ColAccent, title: ColPanelHi, text: ColText, gradient: grad}
	if c, ok := parse.HexColor(bgH); ok {
		col := sdl.Color(c)
		p.bg, p.bg2 = col, col // gradient bottom defaults to the top until set
	}
	if c, ok := parse.HexColor(bg2H); ok {
		col := sdl.Color(c)
		p.bg2 = col
	}
	if c, ok := parse.HexColor(brH); ok {
		col := sdl.Color(c)
		p.border = col
	}
	if c, ok := parse.HexColor(tiH); ok {
		col := sdl.Color(c)
		p.title = col
	}
	if c, ok := parse.HexColor(txH); ok {
		col := sdl.Color(c)
		p.text = col
	}
	return p
}

// blockingCourtPopup reports a popup that takes the whole screen, so the Extras box and torn-off panels yield to it.
func (a *App) blockingCourtPopup() bool {
	// Floating panels (pair, mod, CM, evidence, ban box, timer, login, pair popup, pinned pieces) stay out: they fence
	// by footprint, and adding one would make extrasSurfaceLive false and disable its own fence.
	// Not courtroomModalUp's set: this adds classicEdit and omits showSfxBrowser, which would hide the panels too.
	// Ask courtroomModalUp whether drawCourtroomModals will take the screen.
	return a.showIni || a.bgPick.show ||
		a.classicEdit
}

// courtModalOpen reports whether any pointer-fencing popup is up, the blocking set plus the emoji picker;
// input paths use this, while panel visibility uses blockingCourtPopup so the emoji picker does not hide panels.
func (a *App) courtModalOpen() bool {
	return a.blockingCourtPopup() || a.showEmojiPicker
}

// capturingKey reports whether any key-bind capture is armed; they use Esc to cancel.
func (a *App) capturingKey() bool {
	return a.bindingFor != "" || a.shownameBindFor != "" || a.jukeBindFor != "" ||
		a.icPhraseBindFor != "" || a.stylePresetBindFor != "" || a.holdKeyArmed ||
		a.voicePTTBindArmed || a.macroBind >= 0
}

// pollVoicePTT captures the push-to-talk key when the rebind is armed, otherwise toggles the mic on it while in voice.
func (a *App) pollVoicePTT() {
	c := a.ctx
	if a.voicePTTBindArmed {
		if c.escPressed {
			a.voicePTTBindArmed = false // Esc cancels the capture
			return
		}
		if c.keyPressed != 0 {
			{
				pv1 := sdl.GetKeyName(c.keyPressed)
				a.d.Prefs.Update(func(sn *config.Settings) {
					sn.VoicePTTKey = pv1
				})
			}
			a.voicePTTBindArmed = false
			c.keyPressed = 0
		}
		return
	}
	k := a.bareBindKey() // Alt+<bound key>, like every other plain-key namespace (qol.go)
	if !a.voiceJoined || k == 0 {
		return
	}
	if key := a.d.Prefs.Load().VoicePTTKey; key != "" && sdl.GetKeyName(k) == key {
		a.voiceSetMic(!a.voiceMicOn)
		c.keyPressed = 0
	}
}

// closeTopOverlay closes the topmost open popup or panel, most modal first, and reports whether it closed anything;
// one Esc press peels one layer.
func (a *App) closeTopOverlay() bool {
	c := a.ctx
	switch {
	// The menu bar pane is the topmost chrome and holds c.modalOn; an Esc falling through to the screen arms in app.go
	// would leave the screen with the pane still open.
	case a.menuBarOpen():
		a.closeMenuBar()
	case c.ddOpen != "":
		c.ddOpen = "" // an open dropdown first
	case a.paletteOpen: // #39: the palette sits above everything it launches
		a.paletteOpen = false
		if c.focusID == paletteInputID {
			c.focusID = ""
		}
	// The .demo browser is a blocking modal behind c.modalOn; else Esc leaves Settings.
	case demoBrowser.open:
		demoBrowser.open = false
	// Same for the content-report panel; closeContentPanel also cancels the running job, or the next report is refused.
	case contentPanel.open:
		a.closeContentPanel()
	// Blocking modals. The disconnect dialog comes first and cannot just close: the session under it is dead but drawn,
	// so Esc means Back to lobby, which tears down and releases the dialog's fence.
	case a.disconnectDlg.open:
		a.closeDisconnectDialogToLobby()
	// Server notice: without this, Esc reaches the screen arm and on the lobby stacks the Quit confirm over the box.
	case a.serverNoticeDlg.open:
		a.closeServerNotice()
	case a.pendingCloseTab != nil:
		// Reachable on the lobby; the pointer fence does not cover Esc, which would stack a Quit modal.
		a.pendingCloseTab = nil
	case a.showQuitConfirm:
		a.showQuitConfirm = false
	case a.banBoxKind != 0:
		a.banBoxKind = 0
	case a.showReset:
		a.showReset = false
	// Layout editors, at the height of their pixels: the modals above paint over the banner, everything below under it.
	// Answered here: draw-time handling is dead when the editor does not draw.
	// Each runs its own teardown, which releases the kit's modal fence.
	case a.layoutEdit:
		a.stopLayoutEdit()
	case a.classicEdit:
		a.stopClassicEdit()
	case a.updateShow:
		a.updateShow = false
	case a.showLogin:
		a.showLogin = false
	case a.rosterMenuOpen:
		a.rosterMenuOpen = false // the player-row … menu (anchored popup, like the pickers)
	case a.musicMenuOpen:
		a.musicMenuOpen = false // the music panel's ⋮ menu (same anchored-popup contract)
	case a.showEmojiPicker:
		a.showEmojiPicker = false
	case a.showReactPicker:
		a.showReactPicker = false
	case a.bgPick.show:
		a.bgPick.show = false
	case a.showIni:
		a.showIni = false
	case a.showTimer:
		a.showTimer = false
	case a.toolboxPinned && a.toolboxPieces:
		a.toolboxPieces = false // A1: Esc closes the pinned per-piece panel first
	case a.pairPopupOpen:
		a.pairPopupOpen = false
	case a.showHotkeys:
		a.showHotkeys, a.hkCache = false, nil
	// Non-blocking floating panels.
	case a.showVoice:
		a.showVoice = false
	case a.showMessages:
		a.showMessages = false
	case a.showEvid:
		a.showEvid = false
	case a.showModcall:
		a.showModcall = false
	case a.showModDash:
		a.showModDash = false
	case a.showDebugPanel:
		a.showDebugPanel = false
	case a.showICColorWheel:
		a.showICColorWheel = false
	case a.showFxPicker:
		a.showFxPicker = false
	case a.showCMPanel:
		a.showCMPanel = false
	case a.showPair:
		a.showPair = false
	case a.showSfxBrowser:
		a.showSfxBrowser = false
	case a.showStyleBox:
		a.showStyleBox = false
	// Extras box last: a picker opened from it closes first.
	case a.showWidgets:
		a.showWidgets = false
	// A pinned sprite preview paints inside the courtroom pass, under the panels above; unpinned boxes close on leave.
	case a.previewBase != "" && a.previewIsPinned():
		a.closeSpritePreview()
	case a.theaterOn: // theater mode's "Esc exits" — handled here so it beats the leave-server shortcut
		a.setTheater(false)
	// Char-select wardrobe popups close before that screen's own Esc can fire.
	case a.wardDelFolder != "":
		a.wardDelFolder = ""
	case a.iniMenuChar != "":
		a.iniMenuChar = ""
	default:
		return false
	}
	return true
}

// extrasSurfaceLive reports whether the Extras surface may show: a live courtroom with no blocking popup;
// torn-off boxes ride on this alone, so they stay when the main box closes.
func (a *App) extrasSurfaceLive() bool {
	return a.room != nil && a.sess != nil && !a.blockingCourtPopup()
}

// extrasBoxRect is the main box's rect, clamped on-screen: dragged position once placed, else centred near the top.
func (a *App) extrasBoxRect(w, h int32) sdl.Rect {
	// Seed here so boxFencesPointer and the draw agree on frame one.
	a.seedExtrasFromSlot(w, h)
	bw, bh := extrasBoxW, extrasBoxH
	if a.extrasUserW > 0 {
		bw = a.extrasUserW
	}
	if a.extrasUserH > 0 {
		bh = a.extrasUserH
	}
	hiW, hiH := w-16, h-16 // never wider/taller than the window
	if hiW < extrasMinW {
		hiW = extrasMinW
	}
	if hiH < extrasMinH {
		hiH = extrasMinH
	}
	bw, bh = value.Clamp(bw, extrasMinW, hiW), value.Clamp(bh, extrasMinH, hiH)
	x, y := a.extrasBoxX, a.extrasBoxY
	if !a.extrasPlaced {
		x, y = (w-bw)/2, 76
	}
	maxX, maxY := w-bw-8, h-bh-8
	if maxX < 8 {
		maxX = 8
	}
	if maxY < 8 {
		maxY = 8
	}
	return sdl.Rect{X: value.Clamp(x, 8, maxX), Y: value.Clamp(y, 8, maxY), W: bw, H: bh}
}

// seedExtrasFromSlot adopts the saved position and size once, before any drag.
func (a *App) seedExtrasFromSlot(w, h int32) {
	if a.extrasPlaced {
		return
	}
	ov, ok := a.classicOv[slotExtras]
	if !ok {
		return
	}
	r := a.anchoredRect(slotExtras, ov, w, h)
	a.extrasBoxX, a.extrasBoxY = r.X, r.Y
	if r.W > 0 && r.H > 0 {
		a.extrasUserW, a.extrasUserH = value.Clamp(r.W, extrasMinW, w), value.Clamp(r.H, extrasMinH, h)
	}
	a.extrasPlaced = true
}

// persistExtrasSlot saves the box's rect on a drag or resize end frame, never per frame.
func (a *App) persistExtrasSlot(w, h int32) {
	a.persistPanelSlot(slotExtras, a.extrasBoxRect(w, h), w, h)
}

// detachedBoxRect is the i-th torn-off box's rect, clamped to the window.
func (a *App) detachedBoxRect(i int, w, h int32) sdl.Rect {
	d := a.extrasDetached[i]
	bw, bh := detachedBoxW, detachedBoxH
	if d.w > 0 {
		bw = d.w
	}
	if d.h > 0 {
		bh = d.h
	}
	hiW, hiH := w-8, h-8
	if hiW < detachedMinW {
		hiW = detachedMinW
	}
	if hiH < detachedMinH {
		hiH = detachedMinH
	}
	bw, bh = value.Clamp(bw, detachedMinW, hiW), value.Clamp(bh, detachedMinH, hiH)
	maxX, maxY := w-bw-4, h-bh-4
	if maxX < 4 {
		maxX = 4
	}
	if maxY < 4 {
		maxY = 4
	}
	return sdl.Rect{X: value.Clamp(d.x, 4, maxX), Y: value.Clamp(d.y, 4, maxY), W: bw, H: bh}
}

// widgetDetached reports whether widget id is torn out, so the grid skips it.
func (a *App) widgetDetached(id int) bool {
	for _, d := range a.extrasDetached {
		if d.id == id {
			return true
		}
	}
	return false
}

// boxFencesPointer reports whether the courtroom pass should run pointer-blind: a box is under the cursor or a drag or
// resize is in flight. Gated on extrasSurfaceLive so torn-off boxes fence with the main box closed.
func (a *App) boxFencesPointer(w, h int32) bool {
	if !a.extrasSurfaceLive() {
		return false
	}
	// An open dropdown's list paints above the panels and resolves clicks with raw pointIn, so with the cursor over it
	// the courtroom pass must not run pointer-blind, or the list's rows go dead.
	if a.ctx.ddOpen != "" && pointIn(a.ctx.mouseX, a.ctx.mouseY, a.ctx.ddOpenList) {
		return false
	}
	// The sprite preview fences during its gesture, because blanking mouseDown ends a log drag-selection underneath;
	// not while merely hovered, since it paints inside the courtroom pass and a fence would kill its own zoom buttons.
	if a.previewDrag || a.previewResize {
		return true
	}
	if a.extrasDragging || a.extrasDetachDragging || a.extrasPressing || a.extrasResizing || a.extrasDetachResizing || a.favBoxDragging || a.styleBoxDragging || a.styleBoxResizing ||
		a.pairWin.dragging || a.pairWin.resizing || a.modWin.dragging || a.modWin.resizing || a.cmWin.dragging || a.cmWin.resizing ||
		a.evidWin.dragging || a.evidWin.resizing || a.modcallWin.dragging || a.modcallWin.resizing || a.msgWin.dragging || a.msgWin.resizing ||
		a.voiceWin.dragging || a.voiceWin.resizing || a.banWin.dragging || a.banWin.resizing || a.debugWin.dragging || a.debugWin.resizing ||
		a.hkWin.dragging || a.hkWin.resizing ||
		a.timerWin.dragging || a.timerWin.resizing || a.loginWin.dragging || a.loginWin.resizing ||
		a.pairPopWin.dragging || a.pairPopWin.resizing ||
		a.clientWin.dragging || a.clientWin.resizing || a.clientPanning {
		return true
	}
	mx, my := a.ctx.mouseX, a.ctx.mouseY
	if a.splitActive() && pointIn(mx, my, a.clientWinRect(w, h)) { // the floating client window fences too
		return true
	}
	if a.showWidgets && pointIn(mx, my, a.extrasBoxRect(w, h)) {
		return true
	}
	if a.showPair && pointIn(mx, my, a.pairPanelRect(w, h)) { // the Pair / Mod / CM boxes fence too
		return true
	}
	if a.showModDash && a.banBoxKind == 0 && pointIn(mx, my, a.modDashRect(w, h)) { // dashboard hides while its ban box is open
		return true
	}
	if a.showDebugPanel && pointIn(mx, my, a.debugPanelRect(w, h)) { // the Debug panel fences too
		return true
	}
	if a.showFxPicker && pointIn(mx, my, a.fxPickerRect(w, h)) { // the Text FX picker fences too
		return true
	}
	if a.showICColorWheel && pointIn(mx, my, a.icColorWheelRect(w, h)) { // the free-hex colour wheel fences too (v1.52.0)
		return true
	}
	if a.banBoxKind != 0 && pointIn(mx, my, a.banBoxRect(w, h)) { // the ban/kick box fences too
		return true
	}
	if a.showCMPanel && pointIn(mx, my, a.cmPanelRect(w, h)) {
		return true
	}
	if a.showEvid && pointIn(mx, my, a.evidPanelRect(w, h)) { // the floating evidence box fences too (#5)
		return true
	}
	if a.showEvid && a.evidInspectorDetached && pointIn(mx, my, a.evidInspectorRect(w, h)) { // popped-out inspector
		return true
	}
	if a.showModcall && pointIn(mx, my, a.modcallPanelRect(w, h)) { // Call Mod box
		return true
	}
	if a.showMessages && pointIn(mx, my, a.msgPanelRect(w, h)) { // Group Chat / DMs box
		return true
	}
	if a.showVoice && pointIn(mx, my, a.voicePanelRect(w, h)) { // Voice box — clicks must not fall through to the area list (was swapping rooms)
		return true
	}
	if a.showHotkeys && pointIn(mx, my, a.hkSheetRect(w, h)) { // the floating hotkey sheet fences too
		return true
	}
	// Timer, login and pair popup fence by footprint so a click cannot also land on the scene.
	if a.showTimer && pointIn(mx, my, a.timerPanelRect(w, h)) {
		return true
	}
	if a.showLogin && pointIn(mx, my, a.loginPanelRect(w, h)) {
		return true
	}
	if a.pairPopupOpen && pointIn(mx, my, a.pairPopPanelRect(w, h)) {
		return true
	}
	// Pinned pieces panel: same geometry as its draw; !paletteOpen matches the draw's stand-down, else a dead zone.
	if a.toolboxPinned && a.toolboxPieces && !a.paletteOpen && pointIn(mx, my, a.toolboxPiecesRect(w, h)) {
		return true
	}
	for i := range a.extrasDetached {
		if pointIn(mx, my, a.detachedBoxRect(i, w, h)) {
			return true
		}
	}
	if a.d.Prefs.Load().FavEmoteBox && pointIn(mx, my, a.favBoxRect(w, h)) {
		return true
	}
	if a.showStyleBox {
		if sr, _ := a.styleBoxRect(w, h); pointIn(mx, my, sr) {
			return true
		}
	}
	// Torn-off tab panels have clickable lists, so they fence too.
	if len(a.classicOv) > 0 {
		for i := range tornTabTable {
			if r, ok := a.tornTabRect(tornTabTable[i].key, w, h); ok && pointIn(mx, my, r) {
				return true
			}
		}
	}
	return false
}

// handleExtrasDrag moves the main box by its title bar; pressed is zeroed when this handle grabs it, one press one box.
func (a *App) handleExtrasDrag(handle sdl.Rect, w, h int32, pressed *bool) {
	c := a.ctx
	wasDragging := a.extrasDragging
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
		r := a.extrasBoxRect(w, h)
		a.extrasDragging = true
		a.extrasGrabDX, a.extrasGrabDY = c.mouseX-r.X, c.mouseY-r.Y
	}
	if !c.mouseDown {
		a.extrasDragging = false
	}
	if a.extrasDragging {
		a.extrasBoxX, a.extrasBoxY = c.mouseX-a.extrasGrabDX, c.mouseY-a.extrasGrabDY
		a.extrasPlaced = true
		// Piece-to-piece magnet, before the persist below so the snapped spot is saved; extrasBoxRect re-clamps it.
		if !a.siblingMagnetOff() && len(a.panelMagnetRects) > 0 {
			r := a.extrasBoxRect(w, h)
			a.extrasBoxX, a.extrasBoxY, a.alignGuides = snapRectToSiblings(r, a.panelMagnetRects, w, h, a.alignGuides)
		}
	}
	if wasDragging && !a.extrasDragging { // drag just ended → remember where (slot:panel:extras)
		a.persistExtrasSlot(w, h)
	}
}

// handleDetachedDrag moves the i-th torn-off box by its title bar, sharing the press edge and the single grab offset.
func (a *App) handleDetachedDrag(i int, handle sdl.Rect, w, h int32, pressed *bool) {
	c := a.ctx
	wasDragging := a.extrasDetachDragging && a.extrasDetachIdx == i // detect this box's drag-end frame
	if *pressed && pointIn(c.mouseX, c.mouseY, handle) {
		*pressed = false
		a.extrasDetachDragging = true
		a.extrasDetachIdx = i
		a.extrasGrabDX, a.extrasGrabDY = c.mouseX-a.extrasDetached[i].x, c.mouseY-a.extrasDetached[i].y
	}
	if a.extrasDetachDragging && a.extrasDetachIdx == i {
		if !c.mouseDown {
			a.extrasDetachDragging = false
		} else {
			a.extrasDetached[i].x = c.mouseX - a.extrasGrabDX
			a.extrasDetached[i].y = c.mouseY - a.extrasGrabDY
			// Magnet, before the drag-end persist: snap the clamped rect, write the top-left back to the raw fields.
			if !a.siblingMagnetOff() && len(a.panelMagnetRects) > 0 {
				r := a.detachedBoxRect(i, w, h)
				a.extrasDetached[i].x, a.extrasDetached[i].y, a.alignGuides = snapRectToSiblings(r, a.panelMagnetRects, w, h, a.alignGuides)
			}
		}
	}
	if wasDragging && !a.extrasDetachDragging { // drag just ended → remember where (torn:widget:<id>)
		a.persistTornWidgetSlot(i, w, h)
	}
}

// detachWidget tears widget id into a new box under the cursor and starts dragging it in the same gesture.
func (a *App) detachWidget(id int, mx, my int32) {
	if a.widgetDetached(id) {
		return // defensive: the grid already hides detached ids
	}
	x, y := mx-detachedBoxW/2, my-extrasTitleH/2
	a.extrasDetached = append(a.extrasDetached, detachedWidget{id: id, x: x, y: y})
	a.extrasDetachDragging = true
	a.extrasDetachIdx = len(a.extrasDetached) - 1
	a.extrasGrabDX, a.extrasGrabDY = mx-x, my-y
}

// tornWidgetSlotKey is the saved key "torn:widget:<id>"; formatted off the hot path only.
func tornWidgetSlotKey(id int) string {
	return tornWidgetSlotPrefix + strconv.Itoa(id)
}

// persistTornWidgetSlot saves the i-th torn-off box's rect as a plain frac, never window-pinned.
func (a *App) persistTornWidgetSlot(i int, w, h int32) {
	if i < 0 || i >= len(a.extrasDetached) {
		return
	}
	key := tornWidgetSlotKey(a.extrasDetached[i].id)
	frac := rectToFrac(a.detachedBoxRect(i, w, h), w, h)
	if a.classicOv == nil {
		a.classicOv = make(map[string][4]float64, classicSlotRegCap)
	}
	a.classicOv[key] = frac
	a.d.Prefs.SetClassicSlot(key, frac)
}

// reattachWidget closes the i-th torn-off box and clears its saved key, so the widget does not re-tear next launch.
func (a *App) reattachWidget(i int) {
	if i < 0 || i >= len(a.extrasDetached) {
		return
	}
	a.clearClassicSlot(tornWidgetSlotKey(a.extrasDetached[i].id)) // capture id BEFORE the splice
	a.extrasDetached = append(a.extrasDetached[:i], a.extrasDetached[i+1:]...)
	a.extrasDetachDragging = false
}

// reconstructTornWidgets re-tears widgets a prior session left torn, once per session, before boxFencesPointer runs.
// It runs from drawCourtroom because a detachedWidget stores pixels and needs the window size.
// Unknown ids are ignored, not deleted, so a downgrade round-trips.
func (a *App) reconstructTornWidgets(w, h int32) {
	if a.extrasTornRebuilt {
		return
	}
	a.extrasTornRebuilt = true
	if len(a.classicOv) == 0 {
		return
	}
	widgets := a.extrasWidgets()
	for key, frac := range a.classicOv {
		rest, ok := strings.CutPrefix(key, tornWidgetSlotPrefix)
		if !ok {
			continue
		}
		id, err := strconv.Atoi(rest)
		if err != nil || id < 0 || id >= len(widgets) {
			continue // unknown / malformed id: leave the slot persisted, don't re-tear it
		}
		if a.widgetDetached(id) {
			continue // dedup: already torn (e.g. re-entry after a mid-session detach)
		}
		r := fracToRect(frac, w, h) // torn widgets are unanchored → plain frac→px
		a.extrasDetached = append(a.extrasDetached, detachedWidget{id: id, x: r.X, y: r.Y, w: r.W, h: r.H})
	}
}

// extrasTearDetect starts a tear-off when cell id is dragged past the threshold; on true the caller must stop the grid.
func (a *App) extrasTearDetect(id int, cell sdl.Rect, pressed *bool) bool {
	c := a.ctx
	if *pressed && pointIn(c.mouseX, c.mouseY, cell) {
		*pressed = false
		a.extrasPressing = true
		a.extrasPressID = id
		a.extrasPressX, a.extrasPressY = c.mouseX, c.mouseY
	}
	if a.extrasPressing && a.extrasPressID == id && c.mouseDown &&
		(absInt(int(c.mouseX-a.extrasPressX)) > int(extrasTearPx) ||
			absInt(int(c.mouseY-a.extrasPressY)) > int(extrasTearPx)) {
		a.extrasPressing = false
		a.detachWidget(id, c.mouseX, c.mouseY)
		return true
	}
	return false
}

// drawFloatingPanels draws every non-blocking floating panel on the live courtroom, with real input and one shared
// press edge so exactly one panel grabs a press.
func (a *App) drawFloatingPanels(w, h int32) {
	if !a.extrasSurfaceLive() { // live courtroom, no blocking modal
		return
	}
	c := a.ctx
	pressed := c.mouseDown && !a.extrasPrevDown
	a.extrasPrevDown = c.mouseDown
	// Rebuild the magnet list and clear guides before any drag handler reads them; no-ops on a settled frame.
	a.rebuildPanelMagnetRects(w, h)
	a.alignGuides = a.alignGuides[:0]
	// De-overlap: an open never-placed panel is cascaded off any sibling it nearly covers, once per panel.
	if !a.anyPanelDragging() {
		seeded := false
		for i := range panelSlotTable {
			row := &panelSlotTable[i]
			fw := row.fw(a)
			if !row.open(a) || fw.placed || fw.deSeeded {
				continue
			}
			r := fw.rect(row.defW, row.defH, row.minW, row.minH, w, h)
			if nr := deOverlapRect(r, a.panelDeOverlapCensus(r, w, h), w, h); nr != r {
				fw.x, fw.y, fw.placed = nr.X, nr.Y, true
			}
			fw.deSeeded, seeded = true, true
		}
		if seeded {
			// Leave the shared buffer empty on settled frames, as rebuildPanelMagnetRects requires.
			a.panelMagnetRects = a.panelMagnetRects[:0]
		}
	}
	if !c.mouseDown {
		a.extrasPressing = false // a cell press can't outlive the button
		if a.extrasDragging || a.extrasDetachDragging || a.extrasResizing || a.extrasDetachResizing ||
			a.favBoxDragging || a.styleBoxDragging || a.styleBoxResizing ||
			a.pairWin.dragging || a.pairWin.resizing || a.modWin.dragging || a.modWin.resizing || a.cmWin.dragging || a.cmWin.resizing ||
			a.evidWin.dragging || a.evidWin.resizing || a.modcallWin.dragging || a.modcallWin.resizing || a.msgWin.dragging || a.msgWin.resizing ||
			a.voiceWin.dragging || a.voiceWin.resizing || a.banWin.dragging || a.banWin.resizing || a.debugWin.dragging || a.debugWin.resizing ||
			a.timerWin.dragging || a.timerWin.resizing || a.loginWin.dragging || a.loginWin.resizing ||
			a.pairPopWin.dragging || a.pairPopWin.resizing ||
			a.clientWin.dragging || a.clientWin.resizing || a.clientPanning {
			c.clicked = false // a finished drag/resize isn't a click on whatever's now underneath
		}
	}
	// The client window draws first, at the bottom of the float stack; drawing first gives input priority.
	if a.splitActive() {
		a.drawFloatClient(w, h, &pressed)
	}
	a.drawFloatingExtras(w, h, &pressed)
	if a.showPair {
		a.drawPairPanel(w, h, &pressed)
	}
	if a.showModDash && a.banBoxKind == 0 { // the dashboard hides while its ban/kick box is open (below)
		a.drawModDashPanel(w, h, &pressed)
	}
	if a.showDebugPanel {
		a.drawDebugPanel(w, h, &pressed)
	}
	if a.showFxPicker {
		a.drawFxPicker(w, h)
	}
	if a.showICColorWheel {
		a.drawICColorWheel(w, h)
	}
	if a.showCMPanel {
		a.drawCMPanel(w, h, &pressed)
	}
	if a.showEvid { // evidence is a floating box now (#5) — chat stays live behind it
		a.drawEvidencePanel(w, h, &pressed)
		if a.evidInspectorDetached { // the inspector popped out into its own window
			a.drawEvidenceInspectorWindow(w, h, &pressed)
		}
	}
	if a.showTimer {
		a.drawTimerPanel(w, h, &pressed)
	}
	if a.showLogin {
		a.drawLoginDialog(w, h, &pressed)
	}
	if a.pairPopupOpen {
		a.drawPairPopup(w, h, &pressed)
	}
	if a.showModcall { // call-mod is a floating box now — chat stays live behind it
		a.drawModcallPanel(w, h, &pressed)
	}
	if a.showMessages { // Group Chat / DMs — non-blocking floating panel
		a.drawMessagesPanel(w, h, &pressed)
	}
	if a.showVoice { // Voice (Nyathena) — non-blocking floating panel
		a.drawVoicePanel(w, h, &pressed)
	}
	// The ban box draws last, topmost, instead of the dashboard.
	if a.banBoxKind != 0 {
		a.drawModDashBanBox(w, h, &pressed)
	}
	// Alignment guides last, over every panel; a no-op on a settled frame.
	a.drawPanelAlignGuides(w, h)
}

// drawFloatingExtras draws the Extras, favourite-emotes and Sprite Style boxes, sharing the press edge.
func (a *App) drawFloatingExtras(w, h int32, pressed *bool) {
	favOpen := a.d.Prefs.Load().FavEmoteBox
	if !a.showWidgets && len(a.extrasDetached) == 0 && !favOpen && !a.showStyleBox {
		return // every Extras box closed — nothing to draw
	}
	if a.showWidgets {
		a.drawExtrasMainBox(w, h, pressed)
	}
	a.drawExtrasDetached(w, h, pressed) // torn-off widgets persist even with the main box closed
	if favOpen {
		a.drawFavEmoteBox(w, h, pressed)
	}
	if a.showStyleBox {
		a.drawSpriteStyleBox(w, h, pressed)
	}
}

// drawExtrasMainBox paints the main box and its 2-column widget grid, with tear-off detection per cell.
func (a *App) drawExtrasMainBox(w, h int32, pressed *bool) {
	c := a.ctx
	r := a.extrasBoxRect(w, h) // seeds from the persisted slot on first open (see extrasBoxRect)
	pal := a.extrasPalette()   // stock colours unless the user themed the box
	if pal.gradient {
		c.FillGradient(r, pal.bg, pal.bg2)
	} else {
		c.Fill(r, pal.bg)
	}
	c.Border(r, pal.border)
	// Title bar / drag handle.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: extrasTitleH}, pal.title)
	c.Label(r.X+10, r.Y+6, "AsyncAO Extras", pal.text)
	if c.ButtonCol(sdl.Rect{X: r.X + r.W - 26, Y: r.Y + 4, W: 20, H: extrasTitleH - 8}, "x", pal.bg, pal.title, pal.border, pal.text) {
		a.showWidgets = false
		if !a.extrasCloseHintShown { // tell them how to get it back — once per session
			a.extrasCloseHintShown = true
			a.warnLine = clampLine("Extras hidden — press Ctrl+" + strings.ToUpper(a.hotkeyFor(hotkeyExtras)) + " or Extras → AsyncAO Extras box to reopen")
			a.warnAt = a.now()
		}
		return
	}
	a.handleExtrasDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 30, H: extrasTitleH}, w, h, pressed)

	// Handled before the grid so a corner press resizes rather than arming a tear on a cell.
	grip := sdl.Rect{X: r.X + r.W - extrasGripSz, Y: r.Y + r.H - extrasGripSz, W: extrasGripSz, H: extrasGripSz}
	a.handleExtrasResize(grip, r, w, h, pressed)
	a.drawResizeGrip(grip)

	// Master scales the others; each channel is independent.
	master, music, sfx, blip := a.effectiveVolumes() // per-server when connected, else global
	volY := r.Y + extrasTitleH + 4
	drawVol := func(id, label string, val int) int {
		c.Label(r.X+10, volY+2, label, pal.text)
		track := sdl.Rect{X: r.X + 62, Y: volY + 4, W: r.W - 62 - 48, H: 12}
		nv := int(c.Slider("exvol:"+id, track, int32(val), 100))
		c.Label(r.X+r.W-42, volY+2, strconv.Itoa(nv)+"%", ColTextDim)
		volY += extrasVolRowH
		return nv
	}
	if nv := drawVol("master", "Master", master); nv != master {
		a.setEffectiveVolumes(nv, music, sfx, blip)
	}
	if nv := drawVol("music", "Music", music); nv != music {
		a.setEffectiveVolumes(master, nv, sfx, blip)
	}
	if nv := drawVol("sfx", "SFX", sfx); nv != sfx {
		a.setEffectiveVolumes(master, music, nv, blip)
	}
	if nv := drawVol("blip", "Blip", blip); nv != blip {
		a.setEffectiveVolumes(master, music, sfx, nv)
	}

	widgets := a.extrasWidgets()
	// Count shown cells first: content height decides the scrollbar, which takes width from the cells.
	shown := int32(0)
	for id, wd := range widgets {
		if a.widgetDetached(id) || (wd.label == voiceExtraLabel && !a.voiceOfferable()) {
			continue
		}
		shown++
	}
	g := extrasGridLayout(r, volY, shown)

	// The wheel is consumed so a sibling surface does not read it too; the fence is footprint-scoped.
	if c.wheelY != 0 && g.maxScroll > 0 && c.hovering(g.view) {
		a.extrasScroll = value.Clamp(a.extrasScroll-c.wheelY*extrasScrollStep, 0, g.maxScroll)
		c.wheelY, c.wheelTaken = 0, true
	}
	a.extrasScroll = value.Clamp(a.extrasScroll, 0, g.maxScroll) // a tear-off can shrink the content under us
	if g.barW > 0 {
		a.extrasScroll = c.VScrollbar("extrasgrid",
			sdl.Rect{X: g.view.X + g.view.W - extrasScrollW, Y: g.view.Y, W: extrasScrollW, H: g.view.H},
			a.extrasScroll, g.contentH, g.view.H)
	}

	// pushClip, not SetClipRect: hovering() honours it, so a half-scrolled cell is not clickable.
	prevClip, hadClip := c.pushClip(g.view)
	slot := int32(0) // visible cells compact past torn-off widgets
	for id, wd := range widgets {
		if a.widgetDetached(id) {
			continue
		}
		if wd.label == voiceExtraLabel && !a.voiceOfferable() {
			continue // voice option only shows on servers that advertise it (Nyathena)
		}
		br := g.cellRect(slot, a.extrasScroll)
		slot++
		if br.Y >= g.view.Y+g.view.H || br.Y+br.H <= g.view.Y {
			continue // scrolled clean out of the viewport — nothing to draw or hit-test
		}
		// Tear-off has priority over the Button. Its cell is clipped to the viewport; it hit-tests with raw pointIn,
		// which the clip does not reach; a fully clipped cell has zero width and misses.
		if a.extrasTearDetect(id, intersectRect(br, g.view), pressed) {
			c.popClip(prevClip, hadClip)
			return // grid changed — stop drawing stale cells this frame
		}
		if c.ButtonCol(br, wd.label, pal.bg, pal.title, pal.border, pal.text) {
			c.popClip(prevClip, hadClip)
			wd.run()
			return // an action can open a sub-panel / switch screen — stop here
		}
		tip := wd.desc
		if wd.key != "" {
			tip += "  ·  Ctrl+" + strings.ToUpper(a.hotkeyFor(wd.key))
		}
		c.TooltipAfter("fextra:"+wd.label, br, tip)
	}
	c.popClip(prevClip, hadClip)
	c.LabelClipped(r.X+10, r.Y+r.H-18, r.W-20-extrasGripSz,
		"Drag a widget out to pop it loose · drag the title to move · × closes", pal.text)
}

// extrasGrid is the widget grid's resolved geometry for one frame.
type extrasGrid struct {
	view      sdl.Rect // the clipped scrolling viewport, inside the box
	cellW     int32    // column width after the scrollbar takes its cut
	contentH  int32    // full laid-out height of every visible cell
	maxScroll int32    // contentH - view.H, floored at 0
	barW      int32    // scrollbar column width including its gap (0 = everything fits)
}

// Grid metrics: two columns of 34px cells.
const (
	extrasGridCols = int32(2)
	extrasCellH    = int32(34)
	extrasCellGap  = int32(8)
)

// extrasGridLayout resolves the geometry; the viewport stops extrasHintH short of the bottom and is floored at zero
// height, since a negative one would invert every containment check.
func extrasGridLayout(box sdl.Rect, volY, shown int32) extrasGrid {
	g := extrasGrid{view: sdl.Rect{
		X: box.X + 10,
		Y: volY + 6,
		W: box.W - 20,
		H: box.Y + box.H - extrasHintH - (volY + 6),
	}}
	if g.view.H < 0 {
		g.view.H = 0
	}
	if g.view.W < 0 {
		g.view.W = 0
	}
	if rows := (shown + extrasGridCols - 1) / extrasGridCols; rows > 0 {
		g.contentH = rows*(extrasCellH+extrasCellGap) - extrasCellGap
	}
	if g.contentH > g.view.H {
		g.barW = extrasScrollW + extrasCellGap
		g.maxScroll = g.contentH - g.view.H
	}
	g.cellW = (g.view.W - g.barW - extrasCellGap) / extrasGridCols
	if g.cellW < 0 {
		g.cellW = 0
	}
	return g
}

// cellRect is a cell's screen rect at a scroll offset, row-major.
func (g extrasGrid) cellRect(slot, scroll int32) sdl.Rect {
	col, row := slot%extrasGridCols, slot/extrasGridCols
	return sdl.Rect{
		X: g.view.X + col*(g.cellW+extrasCellGap),
		Y: g.view.Y - scroll + row*(extrasCellH+extrasCellGap),
		W: g.cellW,
		H: extrasCellH,
	}
}

// handleExtrasResize resizes the main box from its grip, pinning the top-left; extrasBoxRect clamps the result.
func (a *App) handleExtrasResize(grip, r sdl.Rect, w, h int32, pressed *bool) {
	c := a.ctx
	wasResizing := a.extrasResizing
	if *pressed && pointIn(c.mouseX, c.mouseY, grip) {
		*pressed = false
		a.extrasResizing = true
		a.extrasBoxX, a.extrasBoxY = r.X, r.Y // pin the corner so resizing doesn't re-center
		a.extrasPlaced = true
		a.extrasGrabDX, a.extrasGrabDY = (r.X+r.W)-c.mouseX, (r.Y+r.H)-c.mouseY
	}
	if !c.mouseDown {
		a.extrasResizing = false
	}
	if a.extrasResizing {
		// Floor here: a size <= 0 would read as unset in extrasBoxRect.
		nw, nh := (c.mouseX+a.extrasGrabDX)-r.X, (c.mouseY+a.extrasGrabDY)-r.Y
		if nw < extrasMinW {
			nw = extrasMinW
		}
		if nh < extrasMinH {
			nh = extrasMinH
		}
		a.extrasUserW, a.extrasUserH = nw, nh
	}
	if wasResizing && !a.extrasResizing { // resize just ended → remember the size (slot:panel:extras)
		a.persistExtrasSlot(w, h)
	}
}

// drawResizeGrip paints a bottom-right resize handle: a small plate with accent nicks up the diagonal.
func (a *App) drawResizeGrip(grip sdl.Rect) {
	c := a.ctx
	c.Fill(grip, ColPanelHi)
	for i := int32(0); i < 3; i++ { // dots along the bottom-right diagonal
		d := 3 + i*4
		c.Fill(sdl.Rect{X: grip.X + grip.W - d - 2, Y: grip.Y + grip.H - d - 2, W: 2, H: 2}, ColAccent)
	}
}

// handleDetachedResize resizes the i-th torn-off box from its grip, pinning the top-left; saved on the end frame.
func (a *App) handleDetachedResize(i int, grip, r sdl.Rect, w, h int32, pressed *bool) {
	c := a.ctx
	wasResizing := a.extrasDetachResizing && a.extrasDetachIdx == i // detect this box's resize-end frame
	if *pressed && pointIn(c.mouseX, c.mouseY, grip) {
		*pressed = false
		a.extrasDetachResizing = true
		a.extrasDetachIdx = i
		a.extrasDetached[i].x, a.extrasDetached[i].y = r.X, r.Y // pin the corner
		a.extrasGrabDX, a.extrasGrabDY = (r.X+r.W)-c.mouseX, (r.Y+r.H)-c.mouseY
	}
	if a.extrasDetachResizing && a.extrasDetachIdx == i {
		if !c.mouseDown {
			a.extrasDetachResizing = false
		} else {
			nw, nh := (c.mouseX+a.extrasGrabDX)-r.X, (c.mouseY+a.extrasGrabDY)-r.Y
			if nw < detachedMinW {
				nw = detachedMinW
			}
			if nh < detachedMinH {
				nh = detachedMinH
			}
			a.extrasDetached[i].w, a.extrasDetached[i].h = nw, nh
		}
	}
	if wasResizing && !a.extrasDetachResizing { // resize just ended → remember the size (torn:widget:<id>)
		a.persistTornWidgetSlot(i, w, h)
	}
}

// drawExtrasDetached paints every torn-off widget as a draggable, closable box.
func (a *App) drawExtrasDetached(w, h int32, pressed *bool) {
	c := a.ctx
	widgets := a.extrasWidgets()
	pal := a.extrasPalette() // torn-off boxes share the main box's theme
	for i := 0; i < len(a.extrasDetached); i++ {
		id := a.extrasDetached[i].id
		if id < 0 || id >= len(widgets) {
			continue
		}
		wd := widgets[id]
		if wd.label == voiceExtraLabel && !a.voiceOfferable() {
			continue // hidden on non-Nyathena servers, even if it was torn off earlier
		}
		r := a.detachedBoxRect(i, w, h)
		if pal.gradient {
			c.FillGradient(r, pal.bg, pal.bg2)
		} else {
			c.Fill(r, pal.bg)
		}
		c.Border(r, pal.border)
		// Title strip = drag handle + close. Identity lives on the body button.
		c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: extrasTitleH}, pal.title)
		if c.ButtonCol(sdl.Rect{X: r.X + r.W - 24, Y: r.Y + 4, W: 18, H: extrasTitleH - 8}, "x", pal.bg, pal.title, pal.border, pal.text) {
			a.reattachWidget(i)
			return // slice mutated — stop drawing this frame
		}
		a.handleDetachedDrag(i, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 28, H: extrasTitleH}, w, h, pressed)
		// Handled before the body button so a corner press resizes instead of running the widget.
		grip := sdl.Rect{X: r.X + r.W - detachedGripSz, Y: r.Y + r.H - detachedGripSz, W: detachedGripSz, H: detachedGripSz}
		a.handleDetachedResize(i, grip, r, w, h, pressed)
		body := sdl.Rect{X: r.X + 8, Y: r.Y + extrasTitleH + 6, W: r.W - 16, H: r.H - extrasTitleH - 12}
		if c.ButtonCol(body, wd.label, pal.bg, pal.title, pal.border, pal.text) {
			wd.run()
			return
		}
		a.drawResizeGrip(grip) // over the body's corner, so it's always visible
		c.TooltipAfter("fdetach:"+wd.label, body, wd.desc)
	}
}
