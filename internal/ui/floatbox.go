package ui

// The floating Extras box: a non-invasive, on-top panel hosting every AsyncAO
// feature an AO2 theme has no button for. Unlike the other courtroom popups it
// does NOT block — the scene, chat and logs stay live underneath. The kit has no
// z-aware input, so instead of fencing the whole screen (a modal) the courtroom
// pass runs pointer-blind only while the cursor sits over a box footprint
// (boxFencesPointer + fencePointer), then the boxes draw last with real input.
// Opened by the Extras button or the 'x' hotkey (toggles a.showWidgets).
//
// Widgets live in the main grid but TEAR OUT: drag one past a small threshold
// and it pops into its own little floating box you move and close independently
// (closing returns it to the grid). Every box shares one per-frame mouse-press
// edge so exactly one of them grabs a given press.

import (
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	extrasBoxW    = int32(380) // main box default width
	extrasBoxH    = int32(452) // main box default height
	extrasMinW    = int32(300) // resize floor: 2 columns stay readable
	extrasMinH    = int32(430) // resize floor: 4 volume sliders + all 13 widgets' rows + hint still fit
	extrasVolRowH = int32(21)  // one volume-slider row at the top of the box
	extrasTitleH  = int32(26)  // title bar / drag handle height (main + torn boxes)
	extrasGripSz  = int32(16)  // bottom-right resize grip

	detachedBoxW   = int32(176) // a torn-off widget's own little box (default)
	detachedBoxH   = int32(66)
	detachedMinW   = int32(120) // resize floor: the label + close still fit
	detachedMinH   = int32(54)
	detachedGripSz = int32(12) // smaller resize grip for the little torn-off boxes
	extrasTearPx   = int32(8)  // drag a grid cell this far to tear it loose
)

// extrasWidget is one entry in the Extras box: a labelled action you click to
// run or drag out into its own floating box.
type extrasWidget struct {
	label, desc, key string // key = hotkey id ("" = none), surfaced in the tooltip
	run              func()
}

// detachedWidget is a widget torn out into its own box at (x,y), sized w×h. id
// indexes the canonical extrasWidgets table; (x,y) is the raw (pre-clamp)
// top-left; w/h are 0 until the box is resized (then its user size).
type detachedWidget struct {
	id   int
	x, y int32
	w, h int32
}

// panelSlot is one row of panelSlotTable: a persistable floatWin panel's canonical
// slot key, an accessor to its per-App floatWin geometry, and its nominal default /
// minimum size. The fw accessor is a plain function (NOT a *floatWin) because the
// floatWin lives on the *App instance, not the package — the table itself must stay
// package-level (zero-alloc rule) so it cannot hold instance pointers. This mirrors
// the compactChip.run method-value idiom. defW/defH/minW/minH are the nominal sizes
// (a few panels compute a dynamic default at draw time; the table carries the
// canonical value for the magnetism census).
type panelSlot struct {
	slot                   string
	fw                     func(*App) *floatWin
	defW, defH, minW, minH int32
}

// panelSlotTable is the CANONICAL enumeration of the persistable floatWin panels
// (every floatWin except msgWin, which keeps its historical slotMessages key and
// its own seed/persist wrappers). It is the single source of truth for:
//   - applyProfile's .placed reset (so open panels re-seed from an applied profile),
//   - a later magnetism milestone's panel census (it derives its candidate set here).
//
// It is iterated ONLY on cold paths (applyProfile; future magnetism) — never in a
// draw fn. Each panel's draw path calls seedPanelFromSlot / persistPanelSlot
// directly with &a.<panel>Win, so no table iteration and no App-capturing closure
// ever touches a settled frame. Adding a floatWin panel? Add its slot const
// (classiclayout.go), a row here, and the two-line seed/persist wiring in its
// draw/rect fns. NOTE: this is a 4th independent panel list — do NOT try to unify
// it with floatbox.go's three deliberately-non-identical fence/suppression lists.
//
// Nominal heights for the panels whose real default height is computed at draw
// time (ban box: by kind; hotkey sheet + pairing: window-height responsive). Only
// the census below uses these — the live seed still passes each panel's own
// dynamic default — so they need only be representative, not exact.
const (
	panelNomBanH    = int32(600) // banBoxDims default (ban kind) height
	panelNomBanMinH = int32(556) // banBoxDims default (ban kind) minimum height
	panelNomHKH     = int32(520) // hotkey sheet's usual opened height
	panelNomPairH   = int32(560) // pairPanelRect's default-height clamp ceiling
)

var panelSlotTable = []panelSlot{
	{slotPanelPair, func(a *App) *floatWin { return &a.pairWin }, pairPanelDefW, panelNomPairH, pairPanelMinW, pairPanelMinH},
	{slotPanelMod, func(a *App) *floatWin { return &a.modWin }, modDashW, modDashH, modDashMinW, modDashMinH},
	{slotPanelCM, func(a *App) *floatWin { return &a.cmWin }, cmPanelDefW, cmPanelDefH, cmPanelMinW, cmPanelMinH},
	{slotPanelHK, func(a *App) *floatWin { return &a.hkWin }, hkSheetDefW, panelNomHKH, hkSheetMinW, hkSheetMinH},
	{slotPanelEvid, func(a *App) *floatWin { return &a.evidWin }, evidPanelDefW, evidPanelDefH, evidPanelMinW, evidPanelMinH},
	{slotPanelModcall, func(a *App) *floatWin { return &a.modcallWin }, modcallPanelDefW, modcallPanelDefH, modcallPanelMinW, modcallPanelMinH},
	{slotPanelVoice, func(a *App) *floatWin { return &a.voiceWin }, voicePanelDefW, voicePanelDefH, voicePanelMinW, voicePanelMinH},
	{slotPanelBan, func(a *App) *floatWin { return &a.banWin }, banBoxDefW, panelNomBanH, banBoxMinW, panelNomBanMinH},
	{slotPanelDebug, func(a *App) *floatWin { return &a.debugWin }, debugPanelW, debugPanelH, debugPanelMinW, debugPanelMinH},
	{slotPanelClient, func(a *App) *floatWin { return &a.clientWin }, clientWinDefW, clientWinDefH, clientWinMinW, clientWinMinH},
}

// extrasWidgets returns the canonical widget table, built once and cached. The
// closures capture the stable *App receiver, so caching them is safe and drops
// the per-frame slice/closure allocations the inline build used to cost.
func (a *App) extrasWidgets() []extrasWidget {
	if a.extrasWidgetCache == nil {
		a.extrasWidgetCache = []extrasWidget{
			{"Character", "Open character select", hotkeyCharMenu, func() { a.screen = ScreenCharSelect }},
			{"Random char", "Swap to a random free character", hotkeyRandomChar, func() { a.randomChar() }},
			{"Wardrobe", "Iniswap — borrow another character's look", hotkeyWardrobe, func() { a.openIniswap() }},
			{"Jukebox", "Your saved music playlists", hotkeyJukebox, func() { a.openIniswap(); a.wardSection = wardSectionJukebox }},
			{"Background", "Change the courtroom background", hotkeyBackground, func() { a.openBgPicker() }},
			{"Edit Layout", "Drag & resize EVERY box live — move the log / OOC / stage, and pop tabs (Music, Players…) out into their own panels; saved across sessions", "", func() { a.openLayoutEditor() }},
			{"Evidence", "Add / view case evidence", hotkeyEvidence, func() { a.showEvid = true }},
			{"Call Mod", "Call a moderator to this room", hotkeyModcall, func() { a.showModcall = true }},
			{"Mod / CM", "Server-aware moderation + room (CM) controls — ban/kick with a live command preview", hotkeyModDash, func() { a.toggleModDash() }},
			{"Pair", "Pair up — share the stage with another character", hotkeyPairMenu, func() { a.showPair = true }},
			{"Login", "Log in with saved credentials", hotkeyLogin, func() { a.openLoginDialog() }},
			{"★ Fav Emotes", "Floating box of just your starred emotes (Ctrl+A)", hotkeyFavEmotes, func() { a.d.Prefs.SetFavEmoteBox(true) }},
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
			{"Hide chrome", "Hide/show AsyncAO's on-screen widgets", hotkeyUIChrome, func() { a.showUICfg = true }},
			{"Theater", "Theater mode — stage only, Esc exits", hotkeyTheater, func() { a.setTheater(!a.theaterOn) }},
			{"Settings", "Open settings", hotkeySettings, func() { a.prevScreen = ScreenCourtroom; a.screen = ScreenSettings }},
			{"Disconnect", "Leave this server", "", func() { a.requestDisconnect() }},
		}
	}
	return a.extrasWidgetCache
}

// extrasPalette is the resolved Extras-box theme for a frame. Empty prefs leave
// every field at the stock kit colour, so the default look (and cost) is unchanged.
type extrasPalette struct {
	bg, bg2, border, title, text sdl.Color
	gradient                     bool
}

// extrasPalette resolves the user's Extras-box colours (hex prefs) over the stock
// kit palette. Blank/invalid entries keep the stock colour — so an untouched box
// is byte-identical to before.
func (a *App) extrasPalette() extrasPalette {
	bgH, bg2H, brH, tiH, txH, grad := a.d.Prefs.ExtrasBoxStyle()
	p := extrasPalette{bg: ColPanel, bg2: ColPanel, border: ColAccent, title: ColPanelHi, text: ColText, gradient: grad}
	if col, ok := parseHexColor(bgH); ok {
		p.bg, p.bg2 = col, col // gradient bottom defaults to the top until set
	}
	if col, ok := parseHexColor(bg2H); ok {
		p.bg2 = col
	}
	if col, ok := parseHexColor(brH); ok {
		p.border = col
	}
	if col, ok := parseHexColor(tiH); ok {
		p.title = col
	}
	if col, ok := parseHexColor(txH); ok {
		p.text = col
	}
	return p
}

// parseHexColor parses "rrggbb" (optionally "#rrggbb") into an opaque colour.
// ok=false on empty/invalid input — no allocation, so it's cheap per frame.
func parseHexColor(s string) (sdl.Color, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return sdl.Color{}, false
	}
	var rgb [3]uint8
	for i := 0; i < 3; i++ {
		hi, ok1 := hexNibble(s[i*2])
		lo, ok2 := hexNibble(s[i*2+1])
		if !ok1 || !ok2 {
			return sdl.Color{}, false
		}
		rgb[i] = hi<<4 | lo
	}
	return sdl.Color{R: rgb[0], G: rgb[1], B: rgb[2], A: 255}, true
}

func hexNibble(b byte) (uint8, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// blockingCourtPopup reports a truly BLOCKING courtroom popup — one that takes
// the whole screen's attention, so the Extras box and torn-off tab panels yield
// to it (and reappear when it closes).
func (a *App) blockingCourtPopup() bool {
	// NOTE: showPair / showModDash / showCMPanel / showEvid / banBoxKind are deliberately
	// NOT here — they're non-blocking floating boxes now (floatwin.go), so the courtroom
	// stays live behind them (you can keep chatting / follow the log while one is open). The
	// ban/kick box used to be a blocking modal; it's a draggable floating box now too, with
	// the live preview + the explicit Send button keeping the destructive-action safety.
	return a.showIni || a.bgPick.show ||
		a.showTimer || a.showUICfg || a.showLogin || a.pairPopupOpen ||
		a.classicEdit
}

// courtModalOpen reports whether ANY pointer-fencing courtroom popup is up —
// the blocking set plus the emoji picker. Input paths (stage zoom, sprite drag,
// the viewport divider) key off this; panel VISIBILITY keys off
// blockingCourtPopup instead, because the emoji picker is a tiny anchored popup
// whose fence already stops click-through — hiding the Extras box and every
// torn-off tab for it read as "the emoji button makes all the areas disappear"
// in the playtest.
func (a *App) courtModalOpen() bool {
	return a.blockingCourtPopup() || a.showEmojiPicker
}

// capturingKey reports whether ANY key-bind capture is armed (hotkey, showname,
// jukebox, macro, IC-phrase, style-preset, hold-to-clear). Those use Esc to
// CANCEL, so the global Esc handler must stand down while one is active.
func (a *App) capturingKey() bool {
	return a.bindingFor != "" || a.shownameBindFor != "" || a.jukeBindFor != "" ||
		a.icPhraseBindFor != "" || a.stylePresetBindFor != "" || a.holdKeyArmed ||
		a.voicePTTBindArmed || a.macroBind >= 0
}

// pollVoicePTT handles the push-to-talk key: captures it when the Voice-settings
// rebind is armed, and otherwise — while in voice — toggles the mic when the bound
// key is pressed (gated like the other plain-key binds: no field focused, no Ctrl,
// no other capture).
func (a *App) pollVoicePTT() {
	c := a.ctx
	if a.voicePTTBindArmed {
		if c.escPressed {
			a.voicePTTBindArmed = false // Esc cancels the capture
			return
		}
		if c.keyPressed != 0 {
			a.d.Prefs.SetVoicePTT(sdl.GetKeyName(c.keyPressed))
			a.voicePTTBindArmed = false
			c.keyPressed = 0
		}
		return
	}
	if !a.voiceJoined || c.keyPressed == 0 || c.focusID != "" || c.ctrlHeld || a.capturingKey() {
		return
	}
	if key := a.d.Prefs.VoicePTT(); key != "" && sdl.GetKeyName(c.keyPressed) == key {
		a.voiceSetMic(!a.voiceMicOn)
		c.keyPressed = 0
	}
}

// closeTopOverlay closes the single topmost open popup / floating panel (most
// modal first), returning whether it closed anything. This is the Esc "back out
// of whatever's open" handler for the courtroom & lobby — one press, one layer,
// so repeated Esc peels overlays off in order. The layout editor (classicEdit)
// is excluded: it owns Esc itself (Done + save). Menu SCREENS are handled by the
// caller; this is only the overlay flags.
func (a *App) closeTopOverlay() bool {
	c := a.ctx
	switch {
	case c.ddOpen != "":
		c.ddOpen = "" // an open dropdown first
	case a.paletteOpen: // #39: the palette sits above everything it launches
		a.paletteOpen = false
		if c.focusID == paletteInputID {
			c.focusID = ""
		}
	// Blocking / confirm modals (highest priority).
	case a.pendingCloseTab != nil:
		// The tab-close confirm CAN be open on the lobby (park a server via "+",
		// then click a background chip's ✕): without this, Esc falls through
		// closeTopOverlay to ScreenLobby's requestQuit(), stacking a Quit modal over
		// this one. The pointer fence doesn't cover Esc (keyboard, handled before the
		// fence) — the modal must answer Esc itself (floatbox.go doctrine: Esc can't
		// fall through while a popup is open). Cancel == drop the pending target.
		a.pendingCloseTab = nil
	case a.showQuitConfirm:
		a.showQuitConfirm = false
	case a.banBoxKind != 0:
		a.banBoxKind = 0
	case a.showReset:
		a.showReset = false
	case a.updateShow:
		a.updateShow = false
	case a.showLogin:
		a.showLogin = false
	case a.rosterMenuOpen:
		a.rosterMenuOpen = false // the player-row … menu (anchored popup, like the pickers)
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
	case a.showUICfg:
		a.showUICfg = false
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
	// The Extras box LAST of the panels: a picker opened from it (SFX / style) closes
	// first, then the box itself. (#28: every courtroom popup answers Esc — and so Esc
	// can't fall through to the courtroom's leave-the-server shortcut while one is open.)
	case a.showWidgets:
		a.showWidgets = false
	case a.theaterOn: // theater mode's "Esc exits" — handled here so it beats the leave-server shortcut
		a.setTheater(false)
	// Char-select wardrobe popups: close them on Esc before the char-select screen's own
	// Esc (back / leave) can fire (#28).
	case a.wardDelFolder != "":
		a.wardDelFolder = ""
	case a.iniMenuChar != "":
		a.iniMenuChar = ""
	default:
		return false
	}
	return true
}

// extrasSurfaceLive reports whether the Extras surface (the MAIN box and/or any
// torn-off boxes) may show at all: a live courtroom with no BLOCKING popup over
// it (the emoji picker doesn't count — see blockingCourtPopup). Torn-off boxes
// ride on this alone, so they persist when the main box is closed — closing the
// main box must not yank the widgets you dragged out.
func (a *App) extrasSurfaceLive() bool {
	return a.room != nil && a.sess != nil && !a.blockingCourtPopup()
}

// extrasBoxVisible reports whether the MAIN box should draw: opened (showWidgets)
// on a live surface. (Torn-off boxes are gated only by extrasSurfaceLive.)
func (a *App) extrasBoxVisible() bool {
	return a.showWidgets && a.extrasSurfaceLive()
}

// extrasBoxRect is the main box's screen rect: the (possibly user-resized) size
// at the dragged position once placed, else a centered-near-the-top default.
// Size clamps to [min, window] and the position clamps fully on-screen, so a
// resize or a moved-then-shrunk window can't strand it off-edge or oversize it.
func (a *App) extrasBoxRect(w, h int32) sdl.Rect {
	// Seed from the persisted slot HERE (not in drawExtrasMainBox) so boxFencesPointer
	// — which calls this rect fn — and the draw agree on frame one: the fence used to
	// read the un-seeded (centred) rect the frame before the draw adopted the saved
	// spot, leaking a click through the box's real footprint. seedExtrasFromSlot is
	// self-guarded (!extrasPlaced) and sets extrasPlaced, so this never double-seeds.
	// Mirrors the floatWin panels' seed-in-rect-fn idiom (seedPanelFromSlot).
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
	bw, bh = clampI32(bw, extrasMinW, hiW), clampI32(bh, extrasMinH, hiH)
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
	return sdl.Rect{X: clampI32(x, 8, maxX), Y: clampI32(y, 8, maxY), W: bw, H: bh}
}

// seedExtrasFromSlot places the Extras box from its persisted classic slot on the
// first frame it's shown this session (before any drag): if the layout editor /
// last session saved a position, adopt it (position + size), marking it placed so
// extrasBoxRect stops re-centring. Called at the top of extrasBoxRect (so the
// fence and the draw see the same seeded rect on frame one). The Extras box is NOT
// a floatWin, so this is bespoke, mirroring seedPanelFromSlot.
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
		a.extrasUserW, a.extrasUserH = clampI32(r.W, extrasMinW, w), clampI32(r.H, extrasMinH, h)
	}
	a.extrasPlaced = true
}

// persistExtrasSlot writes the Extras box's current rect back to its classic slot
// on a drag/resize-END frame (never per-frame) so its position + size survive a
// relaunch. Mirrors persistPanelSlot for the non-floatWin Extras box.
func (a *App) persistExtrasSlot(w, h int32) {
	a.persistPanelSlot(slotExtras, a.extrasBoxRect(w, h), w, h)
}

// detachedBoxRect is the i-th torn-off widget's screen rect: its (possibly
// resized) size clamped to [min, window], placed at its clamped-on-screen top-left.
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
	bw, bh = clampI32(bw, detachedMinW, hiW), clampI32(bh, detachedMinH, hiH)
	maxX, maxY := w-bw-4, h-bh-4
	if maxX < 4 {
		maxX = 4
	}
	if maxY < 4 {
		maxY = 4
	}
	return sdl.Rect{X: clampI32(d.x, 4, maxX), Y: clampI32(d.y, 4, maxY), W: bw, H: bh}
}

// widgetDetached reports whether widget id is currently torn out (so the grid
// skips it).
func (a *App) widgetDetached(id int) bool {
	for _, d := range a.extrasDetached {
		if d.id == id {
			return true
		}
	}
	return false
}

// boxFencesPointer reports whether the courtroom pass should run pointer-blind
// this frame: any Extras box is up under the cursor, or a box drag/resize is in
// flight (so a fast drag can't leak a click to the scene between frames). Gated
// on extrasSurfaceLive — NOT extrasBoxVisible — so torn-off boxes still fence
// the scene when the main box is closed (else clicks would leak through them).
func (a *App) boxFencesPointer(w, h int32) bool {
	if !a.extrasSurfaceLive() {
		return false
	}
	// An OPEN dropdown owns the pointer modally: hovering() is already blanked for
	// every other widget, and its list PAINTS ABOVE the floating panels (deferred to
	// FinishFrame) — so while the cursor is over that list, input must follow the
	// visuals and the courtroom pass must NOT run pointer-blind, or the dropdown's
	// own click resolution (raw pointIn in dropdownEx) goes blind with it. Without
	// this, a list flipped up over a torn-tab panel had dead rows exactly where the
	// two overlapped (custom-layout playtest: "can't select higher than Gray").
	if a.ctx.ddOpen != "" && pointIn(a.ctx.mouseX, a.ctx.mouseY, a.ctx.ddOpenList) {
		return false
	}
	if a.extrasDragging || a.extrasDetachDragging || a.extrasPressing || a.extrasResizing || a.extrasDetachResizing || a.favBoxDragging || a.styleBoxDragging || a.styleBoxResizing ||
		a.pairWin.dragging || a.pairWin.resizing || a.modWin.dragging || a.modWin.resizing || a.cmWin.dragging || a.cmWin.resizing ||
		a.evidWin.dragging || a.evidWin.resizing || a.modcallWin.dragging || a.modcallWin.resizing || a.msgWin.dragging || a.msgWin.resizing ||
		a.voiceWin.dragging || a.voiceWin.resizing || a.banWin.dragging || a.banWin.resizing || a.debugWin.dragging || a.debugWin.resizing ||
		a.hkWin.dragging || a.hkWin.resizing || a.clientWin.dragging || a.clientWin.resizing || a.clientPanning {
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
	for i := range a.extrasDetached {
		if pointIn(mx, my, a.detachedBoxRect(i, w, h)) {
			return true
		}
	}
	if a.d.Prefs.FavEmoteBoxOn() && pointIn(mx, my, a.favBoxRect(w, h)) {
		return true
	}
	if a.showStyleBox && pointIn(mx, my, a.styleBoxRect(w, h)) {
		return true
	}
	// Torn-off tab panels (torntabs.go) fence the scene too — their list content is
	// clickable, so a click in one must not also land on the stage behind it.
	if len(a.classicOv) > 0 {
		for i := range tornTabTable {
			if r, ok := a.tornTabRect(tornTabTable[i].key, w, h); ok && pointIn(mx, my, r) {
				return true
			}
		}
	}
	return false
}

// handleExtrasDrag moves the main box by its title bar. pressed is this frame's
// shared, unconsumed mouse-press edge — zeroed when this handle grabs it, so one
// press moves one box. Runs in the box's own pass (real pointer).
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
	}
	if wasDragging && !a.extrasDragging { // drag just ended → remember where (slot:panel:extras)
		a.persistExtrasSlot(w, h)
	}
}

// handleDetachedDrag moves the i-th torn-off box by its title bar, sharing the
// per-frame press edge and the (single, one-at-a-time) grab offset. w/h size the
// window so the gesture-END frame can persist the box's rect (torn widgets survive
// relaunch — same wasActive bare-bool pattern the floatWin panels use).
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
		}
	}
	if wasDragging && !a.extrasDetachDragging { // drag just ended → remember where (torn:widget:<id>)
		a.persistTornWidgetSlot(i, w, h)
	}
}

// detachWidget tears widget id out of the grid into a new box centred under the
// cursor, and starts dragging it so it follows straight from the same gesture.
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

// tornWidgetSlotKey is the persisted classic-slot key for widget id's torn-off box
// ("torn:widget:<id>"). Formatted off the hot path only (gesture-end / reattach /
// cold reconstruction), never on a settled draw frame.
func tornWidgetSlotKey(id int) string {
	return tornWidgetSlotPrefix + strconv.Itoa(id)
}

// persistTornWidgetSlot writes the i-th torn-off box's current rect back to its
// classic slot on a drag/resize-END frame (never per-frame) so it survives a
// relaunch. Torn widgets are never window-pinned, so this is a plain frac write
// (no anchor bookkeeping) — the thin sibling of persistPanelSlot for the bespoke
// detached boxes.
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

// reattachWidget closes the i-th torn-off box, returning its widget to the grid.
// The widget's persisted torn slot is cleared (pref + live classicOv) so a
// re-grid'd widget doesn't re-tear on the next launch and stale keys don't pile up.
func (a *App) reattachWidget(i int) {
	if i < 0 || i >= len(a.extrasDetached) {
		return
	}
	a.clearClassicSlot(tornWidgetSlotKey(a.extrasDetached[i].id)) // capture id BEFORE the splice
	a.extrasDetached = append(a.extrasDetached[:i], a.extrasDetached[i+1:]...)
	a.extrasDetachDragging = false
}

// reconstructTornWidgets re-tears the Extras widgets a prior session left torn off,
// by scanning the loaded classic overrides for tornWidgetSlotPrefix keys and
// appending a detachedWidget at each saved (frac→px) spot. COLD PATH ONLY: latched
// by extrasTornRebuilt so it runs once per SESSION (the latch is reset only by
// applyProfile). Called from drawCourtroom right after ensureClassicOv and BEFORE
// boxFencesPointer, so the fence sees the reconstructed boxes on frame one — never
// adds per-frame work.
//
// Why here (not App init): a detachedWidget stores PIXELS, so reconstruction needs
// the live window size; App init has none. drawCourtroom is the first spot that has
// both the loaded classicOv AND a real w/h, and ensureClassicOv has just run above
// it — so this is the natural one-shot home (ensureClassicOv itself takes no w/h).
//
// Unknown ids (from a newer build) are IGNORED, not deleted — their slots stay
// persisted so a downgrade/upgrade round-trips. The result is structurally bounded:
// ids must match an extrasWidgets() entry and are deduped, so at most one box per
// widget.
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

// extrasTearDetect starts a tear-off when grid cell id is press-dragged past the
// threshold; the plain click (release in place) is left to the cell's Button.
// Returns true once it tears — the caller must stop drawing the now-stale grid
// this frame.
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

// drawFloatingExtras paints the Extras surface (main box + every torn-off box)
// on top of the live courtroom. Picking a widget runs its action but LEAVES the
// box open (non-invasive); a widget that opens its own blocking panel hides the
// surface until that panel closes, then it returns.
// drawFloatingPanels draws every non-blocking floating panel on the live
// courtroom — the Extras boxes AND the Pair menu — sharing ONE mouse-press edge
// per frame so exactly one panel grabs a given press (no double-grab where two
// overlap). Called after drawCourtroom with input restored, so they draw on top
// with real input while the courtroom behind stays interactive everywhere the
// cursor isn't over a panel (you can still chat with one open).
func (a *App) drawFloatingPanels(w, h int32) {
	if !a.extrasSurfaceLive() { // live courtroom, no blocking modal
		return
	}
	c := a.ctx
	pressed := c.mouseDown && !a.extrasPrevDown
	a.extrasPrevDown = c.mouseDown
	if !c.mouseDown {
		a.extrasPressing = false // a cell press can't outlive the button
		if a.extrasDragging || a.extrasDetachDragging || a.extrasResizing || a.extrasDetachResizing ||
			a.favBoxDragging || a.styleBoxDragging || a.styleBoxResizing ||
			a.pairWin.dragging || a.pairWin.resizing || a.modWin.dragging || a.modWin.resizing || a.cmWin.dragging || a.cmWin.resizing ||
			a.evidWin.dragging || a.evidWin.resizing || a.modcallWin.dragging || a.modcallWin.resizing || a.msgWin.dragging || a.msgWin.resizing ||
			a.voiceWin.dragging || a.voiceWin.resizing || a.banWin.dragging || a.banWin.resizing || a.debugWin.dragging || a.debugWin.resizing ||
			a.clientWin.dragging || a.clientWin.resizing || a.clientPanning {
			c.clicked = false // a finished drag/resize isn't a click on whatever's now underneath
		}
	}
	// The floating client window (a pinned second server) draws FIRST — it's the
	// big interaction surface, so it sits at the bottom of the float stack (the
	// smaller Extras / Pair / Mod panels overlay it). draw-first = input priority.
	if a.splitActive() {
		a.drawFloatClient(w, h, &pressed)
	}
	a.drawFloatingExtras(w, h, &pressed)
	// Pair / Mod / CM are floating boxes now (drawn last = on top, real input).
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
	// The ban/kick box draws last = topmost (it's the focused destructive action). It's drawn
	// INSTEAD of the dashboard (above), which mirrors the old blocking modal that hid the dashboard
	// — only now the courtroom behind stays live, so the mod can drag it aside and keep chatting.
	if a.banBoxKind != 0 {
		a.drawModDashBanBox(w, h, &pressed)
	}
}

// drawFloatingExtras draws the Extras boxes (main + torn-off + favourite-emotes +
// Sprite Style), sharing the press edge from drawFloatingPanels.
func (a *App) drawFloatingExtras(w, h int32, pressed *bool) {
	favOpen := a.d.Prefs.FavEmoteBoxOn()
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

// drawExtrasMainBox paints the main box and its 2-column grid of (non-detached)
// widgets, with tear-off detection per cell.
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
			a.warnLine = clampLine("Extras hidden — press Ctrl+" + strings.ToUpper(a.hotkeyFor(hotkeyExtras)) + " or the ★ Extras button to reopen")
			a.warnAt = a.now()
		}
		return
	}
	a.handleExtrasDrag(sdl.Rect{X: r.X, Y: r.Y, W: r.W - 30, H: extrasTitleH}, w, h, pressed)

	// Bottom-right resize grip. Handled before the grid so a corner press resizes
	// rather than arming a tear on the cell beneath; it sits below the grid, so
	// drawing it here doesn't overlap any cell.
	grip := sdl.Rect{X: r.X + r.W - extrasGripSz, Y: r.Y + r.H - extrasGripSz, W: extrasGripSz, H: extrasGripSz}
	a.handleExtrasResize(grip, r, w, h, pressed)
	a.drawResizeGrip(grip)

	// Sound sliders at the top — Master + the three channels (players' top ask:
	// "the volume is in a bad place", and they liked this design). Master scales
	// the others; each channel is independent.
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
	const cols = int32(2)
	const cellH, gap = int32(34), int32(8)
	cellW := (r.W - 20 - gap) / cols
	gx, gy := r.X+10, volY+6 // grid starts below the volume sliders
	slot := int32(0)         // visible cells compact past torn-off widgets
	for id, wd := range widgets {
		if a.widgetDetached(id) {
			continue
		}
		if wd.label == voiceExtraLabel && !a.voiceOfferable() {
			continue // voice option only shows on servers that advertise it (Nyathena)
		}
		col, row := slot%cols, slot/cols
		slot++
		br := sdl.Rect{X: gx + col*(cellW+gap), Y: gy + row*(cellH+gap), W: cellW, H: cellH}
		// Tear-off takes priority: a press-drag past the threshold pops the
		// widget out; a plain click still runs it via the Button below.
		if a.extrasTearDetect(id, br, pressed) {
			return // grid changed — stop drawing stale cells this frame
		}
		if c.ButtonCol(br, wd.label, pal.bg, pal.title, pal.border, pal.text) {
			wd.run()
			return // an action can open a sub-panel / switch screen — stop here
		}
		tip := wd.desc
		if wd.key != "" {
			tip += "  ·  Ctrl+" + strings.ToUpper(a.hotkeyFor(wd.key))
		}
		c.TooltipAfter("fextra:"+wd.label, br, tip)
	}
	c.LabelClipped(r.X+10, r.Y+r.H-18, r.W-20-extrasGripSz,
		"Drag a widget out to pop it loose · drag the title to move · × closes", pal.text)
}

// handleExtrasResize resizes the main box from its bottom-right grip, pinning the
// top-left so only width/height grow. Shares the per-frame press edge and the
// (one-at-a-time) grab offset; extrasBoxRect clamps the result to [min, window].
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
		// Floor at the minimum here (so a far-inward drag can't drive the size
		// to ≤0, which extrasBoxRect would misread as "unset → default"); the
		// window ceiling is clamped there.
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

// drawResizeGrip paints a bottom-right resize handle — a small plate with accent
// nicks stepping up the diagonal — so it reads as draggable rather than blending
// into the box edge. Shared by the main box and every torn-off box.
func (a *App) drawResizeGrip(grip sdl.Rect) {
	c := a.ctx
	c.Fill(grip, ColPanelHi)
	for i := int32(0); i < 3; i++ { // dots along the bottom-right diagonal
		d := 3 + i*4
		c.Fill(sdl.Rect{X: grip.X + grip.W - d - 2, Y: grip.Y + grip.H - d - 2, W: 2, H: 2}, ColAccent)
	}
}

// handleDetachedResize resizes the i-th torn-off box from its bottom-right grip,
// pinning the top-left. Shares the per-frame press edge and the (one-at-a-time)
// grab offset; detachedBoxRect clamps the result to [min, window]. w/h let the
// resize-END frame persist the new rect (torn widgets survive relaunch).
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

// drawExtrasDetached paints every torn-off widget as its own small floating box:
// a title strip that drags + closes (closing returns the widget to the grid),
// and a body button that runs the widget.
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
		// Bottom-right resize grip — handled before the body button so a corner
		// press resizes the box instead of running the widget.
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
