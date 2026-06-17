package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// settingsState lives on App lazily (kept here for file cohesion).
type settingsState struct {
	mountInput string
	loaded     bool
	statusLine string
	tab        int                    // active settings tab (index into settingsTabNames)
	tabScroll  [numSettingsTabs]int32 // per-tab page scroll (each tab remembers its position)
	search     string                 // settings search query (jumps to the matching tab)

	// callwords edit buffer (loaded once per settings entry).
	callInput  string
	callLoaded bool

	// friends edit buffer — reloaded when the server (friendKey) changes,
	// since the friend list is per server.
	friendInput string
	friendKey   string

	// font override edit buffer (semicolon-separated chain).
	fontInput  string
	fontLoaded bool

	// custom window-size edit buffers (W, H), seeded from the live size and
	// re-seeded after a preset/fit so they track the window.
	winWInput, winHInput string
	winLoaded            bool

	// importArmed routes the next dropped .json into ImportSettings.
	importArmed bool

	// macro editor buffers (name, captured key, |-separated lines).
	macroName  string
	macroKey   string
	macroLines string

	// theme-binding picker (shares the login section's server list cache).
	themeBindKey string

	// login section: the picked server + its credential edit buffers
	// (configurable for ANY known server, connected or not).
	loginKey      string
	loginUser     string
	loginPass     string
	loginAuto     bool
	loginLoaded   bool
	loginNames    []string // picker cache (WebSocketURL allocates)
	loginKeys     []string
	loginSrvCount int
	loginSrvFor   string

	// theme picker state: list scanning runs on a goroutine (directory
	// I/O stays off the render thread — §17.2) and lands on themeRes.
	themeName string
	themeDir  string
	themeList []string
	themeRes  chan themeScan
	themeBusy bool

	// folder picking: native dialog output / resolved drag-drops land
	// here from goroutines (never block or stat on the render thread).
	folderRes  chan string
	browseBusy bool

	// ioRes carries one-line results of off-thread file ops (learned
	// format export/import) back to the status line.
	ioRes chan string
}

// themeScan is one scan result: the theme names found, the NORMALIZED
// root (users paste the themes folder itself, or a single theme inside
// it — both resolve to the root theme.Load expects), and an optional
// auto-pick when the pasted path WAS a single theme.
type themeScan struct {
	names    []string
	root     string
	pickName string
}

var settings = settingsState{
	themeRes:  make(chan themeScan, 1),
	folderRes: make(chan string, 1),
	ioRes:     make(chan string, 1),
}

// Settings tabs: the screen is split into these categories so it's
// navigable instead of one long scroll. numSettingsTabs sizes the per-tab
// scroll array (keep it == len(settingsTabNames)).
const numSettingsTabs = 6

var settingsTabNames = [numSettingsTabs]string{
	"General", "Theme", "Assets", "Audio & Chat", "Account", "Hotkeys",
}

// Tab indices (order matches settingsTabNames).
const (
	tabGeneral = iota
	tabTheme
	tabAssets
	tabAudioChat
	tabAccount
	tabHotkeys
)

// settingsSearchKeywords maps each tab to terms the search box matches, so
// "blip" jumps to Audio & Chat, "password" to Account, and so on.
var settingsSearchKeywords = [numSettingsTabs][]string{
	tabGeneral:   {"showname", "ooc name", "animation", "reduce motion", "emote button", "debug", "streamer", "smooth", "scaling", "ui scale", "dpi", "font", "cjk", "tabs", "server tabs", "max tabs"},
	tabTheme:     {"theme", "chatbox", "skin", "layout", "courtroom design", "bind", "preview"},
	tabAssets:    {"fallback", "format", "webp", "png", "avif", "extensions", "audio format", "local", "mount", "download", "cache", "disk", "zstd", "learned"},
	tabAudioChat: {"music", "sfx", "sound", "blip", "volume", "text crawl", "text stay", "text speed", "chat limit", "catch up", "callword", "casing", "case"},
	tabAccount:   {"login", "password", "credential", "master list", "discord", "presence"},
	tabHotkeys:   {"hotkey", "keybind", "macro", "shortcut", "export", "import", "backup"},
}

// settingsSearchMatch returns the first tab whose name or keywords contain the
// (lowercased, trimmed) query, or -1 for none/empty.
func settingsSearchMatch(query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return -1
	}
	for i, name := range settingsTabNames {
		if strings.Contains(strings.ToLower(name), q) {
			return i
		}
	}
	for i, kws := range settingsSearchKeywords {
		for _, kw := range kws {
			if strings.Contains(kw, q) {
				return i
			}
		}
	}
	return -1
}

// imageTypes get the per-format toggle treatment.
var imageTypeNames = []string{
	config.TypeCharIcon,
	config.TypeCharSprite,
	config.TypeBackground,
	config.TypeDeskOverlay,
	config.TypeShoutBubble,
	config.TypeEmoteButton,
	config.TypeMisc,
}

func (a *App) drawSettings(w, h int32) {
	c := a.ctx
	if a.showReset { // factory-reset pop-up owns the screen + input this frame
		a.drawResetConfirm(w, h)
		return
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	c.Heading(pad, pad, "Settings", ColText)
	// Search: type a term, press Enter to jump to the tab that has it.
	q, committed := c.TextField("settsearch", sdl.Rect{X: pad + 110, Y: pad, W: 230, H: fieldH}, settings.search, "Search settings...")
	settings.search = q
	if mt := settingsSearchMatch(q); mt >= 0 {
		c.LabelClipped(pad+350, pad+4, w-pad-350-110, "→ "+settingsTabNames[mt]+"  (Enter)", ColAccent)
		if committed {
			settings.tab = mt
			settings.search = ""
		}
	}
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		_ = a.d.Prefs.SaveNow() // Settings-Apply synchronous flush
		a.screen = a.prevScreen
		return
	}

	if !settings.loaded {
		settings.themeName, settings.themeDir = a.d.Prefs.Theme()
		if settings.themeName == "" {
			settings.themeName = theme.DefaultThemeName
		}
		settings.loaded = true
		a.scanThemes()
	}

	// These run regardless of the active tab, so async results land and
	// dropped files are honored from any tab (theme scan, folder pick,
	// drag-drop, and the off-thread IO status line).
	a.pollThemeScan()
	a.pollFolderPick()
	if c.dropped != "" {
		// Drag-and-drop anywhere on this screen: a .json points an armed
		// settings import; anything else points the theme folder.
		if settings.importArmed && strings.EqualFold(filepath.Ext(c.dropped), ".json") {
			settings.importArmed = false
			importSettingsAsync(a, c.dropped)
		} else {
			resolveDroppedFolder(c.dropped)
		}
	}
	select {
	case line := <-settings.ioRes:
		settings.statusLine = line
	default:
	}

	// Tab strip: one row of category chips, the active one highlighted.
	tabY := pad + 38
	tabW := (w - 2*pad) / int32(len(settingsTabNames))
	for i, name := range settingsTabNames {
		r := sdl.Rect{X: pad + int32(i)*tabW, Y: tabY, W: tabW, H: btnH}
		bg := ColPanel
		if i == settings.tab {
			bg = ColPanelHi
		}
		c.Fill(r, bg)
		c.Border(r, ColPanelHi)
		col := ColTextDim
		if i == settings.tab {
			col = ColText
		}
		c.LabelClipped(r.X+8, r.Y+5, r.W-12, name, col)
		if c.hovering(r) && c.clicked {
			settings.tab = i
		}
	}

	// Content scrolls per-tab (each tab remembers its position). The wheel
	// handler + bar live at the end, where the content height is known.
	top := tabY + btnH + 10
	scroll := &settings.tabScroll[settings.tab]
	y := top - *scroll
	// Clip the scrolled content to below the tab strip: a scrolled-up row
	// would otherwise draw over the tab chips (the strip is full-width, so
	// it's glaring — same overspill class as the log-list fix).
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: 0, Y: top, W: w, H: h - top})
	switch settings.tab {
	case tabGeneral:
		y = a.drawSettingsGeneral(y, w)
	case tabTheme:
		y = a.drawSettingsTheme(y, w, h)
	case tabAssets:
		y = a.drawSettingsAssets(y, w)
	case tabAudioChat:
		y = a.drawSettingsAudioChat(y, w)
	case tabAccount:
		y = a.drawSettingsAccount(y, w)
	case tabHotkeys:
		y = a.drawSettingsHotkeys(y, w)
	}
	if settings.statusLine != "" {
		c.Label(pad, y, settings.statusLine, ColAccent)
		y += 24
	}
	c.popClip(clipPrev, clipHad)

	contentH := (y + *scroll) - top + pad
	visibleH := h - top - pad
	if !c.ctrlHeld && !c.wheelTaken {
		*scroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: w - scrollBarW - 2, Y: top, W: scrollBarW, H: visibleH}
	*scroll = c.VScrollbar("settscroll", track, *scroll, contentH, visibleH)
}

// drawSettingsGeneral: identity + display toggles + UI scale + font chain.
func (a *App) drawSettingsGeneral(y, w int32) int32 {
	c := a.ctx
	// Showname: write-through to prefs. A stale once-per-session copy here
	// used to overwrite names typed in the courtroom on Back.
	c.Label(pad, y+4, "Showname (saved):", ColText)
	shown := a.d.Prefs.SavedShowname()
	if next, _ := c.TextField("showname", sdl.Rect{X: pad + 150, Y: y, W: 220, H: fieldH}, shown, "Your showname"); next != shown {
		a.d.Prefs.SetShowname(next)
	}
	// Default OOC name: applied on every join; blank sends a sticky AsyncAO<n>.
	c.Label(pad+390, y+4, "OOC name:", ColText)
	if next, _ := c.TextField("oocdefault", sdl.Rect{X: pad + 480, Y: y, W: 200, H: fieldH}, a.oocName, "blank = AsyncAO<n>"); next != a.oocName {
		a.oocName = next
		a.d.Prefs.SetOOCName(next)
	}
	y += 38

	// Showname presets (M6): a saved, global list — quick-swap them in-game with
	// keybinds (random or a specific one). Cleared only by a factory reset.
	c.Label(pad, y+4, "Showname presets:", ColText)
	var addNow bool
	a.shownameAdd, addNow = c.TextField("shownameadd", sdl.Rect{X: pad + 150, Y: y, W: 220, H: fieldH}, a.shownameAdd, "type a name to save…")
	if c.Button(sdl.Rect{X: pad + 378, Y: y, W: 60, H: btnH}, "Save") || addNow {
		if a.d.Prefs.AddShownamePreset(a.shownameAdd) {
			a.shownameAdd = ""
		}
	}
	if c.Button(sdl.Rect{X: pad + 444, Y: y, W: 110, H: btnH}, "Save current") {
		a.d.Prefs.AddShownamePreset(a.effectiveShowname())
	}
	c.Label(pad+564, y+4, "global · Ctrl+H random · Ctrl+B cycle · bind a key per preset · resets on a factory wipe", ColTextDim)
	y += 32
	if presets := a.d.Prefs.ShownameList(); len(presets) == 0 {
		c.Label(pad+12, y+2, "(none yet — Save a name above; bind keys to swap them)", ColTextDim)
		y += 24
	} else {
		active := a.effectiveShowname()
		for _, name := range presets {
			if c.Button(sdl.Rect{X: pad + 12, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.RemoveShownamePreset(name)
				a.refreshShownameKeys() // its keybind (if any) was cleared too
			}
			if c.Button(sdl.Rect{X: pad + 38, Y: y, W: 46, H: 18}, "Use") {
				a.shownameOverride = name // apply now (the in-courtroom override)
			}
			// Per-preset keybind: arm a capture (pollShownameBind binds the next
			// key to this showname); right-click the button clears the bind.
			bound := a.shownameKeyFor(name)
			keyLbl := "Bind key"
			switch {
			case a.shownameBindFor == name:
				keyLbl = "press a key…"
			case bound != "":
				keyLbl = "Key: " + bound
			}
			kr := sdl.Rect{X: pad + 90, Y: y, W: 96, H: 18}
			if c.Button(kr, keyLbl) {
				a.bindingFor = "" // don't also arm a character keybind
				a.shownameBindFor = name
				c.focusID = "" // the capture owns the next keypress
			}
			if bound != "" && c.rightClicked && c.hovering(kr) {
				a.d.Prefs.SetShownameKeyBind(bound, "")
				a.refreshShownameKeys()
			}
			c.Tooltip(kr, "Bind a key to this showname — press it in the courtroom to swap. Right-click to clear.")
			lbl, col := name, ColText
			if strings.EqualFold(active, name) {
				lbl, col = name+"   ← active", ColTierGreen
			}
			c.LabelClipped(pad+194, y+1, 280, lbl, col)
			y += 24
		}
	}
	y += 8

	anims := a.d.Prefs.AnimationsEnabled()
	if next := c.Checkbox(pad, y, "Play animations (off = render first frames only; never affects network probes)", anims); next != anims {
		a.d.Prefs.SetAnimationsEnabled(next)
	}
	y += 26
	reduce := a.d.Prefs.ReduceMotion()
	if next := c.Checkbox(pad, y, "Reduce motion (accessibility): stop the screen shake / realization flash (effect sounds still play)", reduce); next != reduce {
		a.d.Prefs.SetReduceMotion(next)
		a.applyTimingToRoom() // push the flag to the live room
	}
	y += 26
	emoteImgs := a.d.Prefs.EmoteButtonImagesEnabled()
	if next := c.Checkbox(pad, y, "Image emote buttons (characters/<char>/emotions/button art — WebP by default, formats in Assets)", emoteImgs); next != emoteImgs {
		a.d.Prefs.SetEmoteButtonImages(next)
	}
	y += 26
	dbg := a.d.Prefs.DebugOverlayEnabled()
	if next := c.Checkbox(pad, y, "Debug overlay (live log of failures: missing assets, theme problems, unhandled server packets)", dbg); next != dbg {
		a.d.Prefs.SetDebugOverlay(next)
	}
	y += 26
	streamer := a.d.Prefs.StreamerMode()
	if next := c.Checkbox(pad, y, "Streamer mode (masks OOC names + IPs in the log display, silences callword pings)", streamer); next != streamer {
		a.d.Prefs.SetStreamerMode(next)
	}
	y += 26
	fcn := a.d.Prefs.ForceCharNamesOn()
	if next := c.Checkbox(pad, y, "Force character names (OFF by default): show everyone's CHARACTER name, not custom shownames — true-roleplay / anti-impersonation (casing)", fcn); next != fcn {
		a.d.Prefs.SetForceCharNames(next)
		if a.room != nil {
			a.room.ForceCharNames = next // apply to the running session immediately
		}
	}
	y += 26
	re := a.d.Prefs.RandomEmoteOn()
	if next := c.Checkbox(pad, y, "Auto-random emote (OFF by default): every message picks a different emote from your character's set — for the lazy, and to show off more sprites", re); next != re {
		a.d.Prefs.SetRandomEmote(next)
	}
	y += 26
	// Sprite hover-previews: rest the cursor on a character/emote button to pop a
	// full-size preview. ON by default; the dwell before it shows is tunable.
	prev := a.d.Prefs.SpritePreviewsOn()
	if next := c.Checkbox(pad, y, "Sprite hover-previews (ON by default): hovering a character or emote button shows the full-size sprite", prev); next != prev {
		a.d.Prefs.SetSpritePreviews(next)
	}
	y += 26
	if prev {
		ms := a.d.Prefs.PreviewHoverMillis()
		if next := a.previewDelayRow(y, ms); next != ms {
			a.d.Prefs.SetPreviewHoverMs(next)
		}
		c.Label(pad+340, y+4, "how long to hover before the preview pops (default 5 s)", ColTextDim)
		y += 30
	}
	// Sprite repositioning: drag a character in the viewport to move them (the
	// override sticks per character until reset). OFF by default so a stray click
	// can't nudge a sprite; right-clicking a sprite resets just that one.
	move := a.d.Prefs.SpriteMoveEnabled()
	if next := c.Checkbox(pad, y, "Let me drag character sprites to reposition them (OFF by default; right-click a sprite to reset it)", move); next != move {
		a.d.Prefs.SetSpriteMove(next)
	}
	y += 26
	if move {
		if c.Button(sdl.Rect{X: pad + 20, Y: y, W: 200, H: btnH}, "Reset all moved sprites") {
			clear(a.spriteOv)
			settings.statusLine = "Cleared every sprite reposition."
		}
		c.Label(pad+232, y+5, "drop every drag override back to the server's placement", ColTextDim)
		y += 32
	}
	upd := a.d.Prefs.UpdateCheckEnabled()
	if next := c.Checkbox(pad, y, "Check for updates on launch (one async check of GitHub Releases; shows the patch notes — off = no outbound call)", upd); next != upd {
		a.d.Prefs.SetUpdateCheck(next)
	}
	y += 26
	alt := a.d.Prefs.AutoLoginToastOn()
	if next := c.Checkbox(pad, y, "Notify me when auto-login signs me in (toast + desktop notification — ON by default, so a mod knows they're logged in)", alt); next != alt {
		a.d.Prefs.SetAutoLoginToast(next)
	}
	y += 26
	tabCap := a.d.Prefs.TabCap()
	if next := a.numberRow(y, "Max server tabs", tabCap, 1, 1, 99); next != tabCap {
		a.d.Prefs.SetTabCap(next)
	}
	c.Label(pad+270, y+4, "servers you can keep open at once — each is a live connection (default 6)", ColTextDim)
	y += 30
	restoreTabs := a.d.Prefs.RestoreTabsOn()
	if next := c.Checkbox(pad, y, "Reopen my server tabs on launch (OFF by default): remembers open servers on exit and reconnects them next time", restoreTabs); next != restoreTabs {
		a.d.Prefs.SetRestoreTabs(next)
	}
	y += 30
	// Log-selection highlight colour: a hue/saturation wheel + brightness
	// slider + hex field (drag-select in IC/OOC shows it).
	y = a.drawHighlightPicker(y, w)
	// Per-speaker name colours: tint each speaker's name by a stable hash, with
	// saturation/brightness sliders + a live preview. OFF by default.
	y = a.drawNameColorPicker(y, w)
	slideOn := a.d.Prefs.BgSlideshowEnabled()
	if next := c.Checkbox(pad, y, "Background slideshow (OFF by default): when the courtroom is idle, cycle the stage through this server's backgrounds as ambiance", slideOn); next != slideOn {
		a.d.Prefs.SetBgSlideshow(next)
	}
	y += 26
	if slideOn {
		secs := a.d.Prefs.BgSlideshowSeconds()
		// Bounds match the config clamp (3..600s); SetBgSlideshowSeconds is authoritative.
		if next := a.numberRow(y, "  Seconds per background", secs, 1, 3, 600); next != secs {
			a.d.Prefs.SetBgSlideshowSeconds(next)
		}
		c.Label(pad+270, y+4, "only while idle — a message instantly shows the real area background again", ColTextDim)
		y += 30
	}
	smooth := a.d.Prefs.SmoothScalingEnabled()
	if next := c.Checkbox(pad, y, "Smooth texture scaling (linear filtering; re-streams loaded images when toggled)", smooth); next != smooth {
		a.d.Prefs.SetSmoothScaling(next)
		hint := "1"
		if !next {
			hint = "0"
		}
		// The hint applies at texture CREATION; purge so everything
		// re-streams (demand pipeline + scenery heal repopulate live).
		sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, hint)
		a.d.Store.Purge()
		c.purgeTextCache()
		a.themeChatbox = false
		a.applyThemeAsync()
		settings.statusLine = "Re-streaming textures with new filtering."
	}
	y += 26
	// Global scale: DPI-driven by default, manual spinbox when auto is off.
	scaleAuto := a.d.Prefs.UIScaleAuto()
	scaleAutoLabel := "Auto UI scale from display DPI"
	if a.detectedScalePct > 0 {
		scaleAutoLabel = fmt.Sprintf("Auto UI scale from display DPI (this display: %d%%)", a.detectedScalePct)
	}
	if next := c.Checkbox(pad, y, scaleAutoLabel, scaleAuto); next != scaleAuto {
		a.d.Prefs.SetUIScaleAuto(next)
		a.ctx.SetUIScale(a.UIScale())
	}
	y += 26
	if scaleAuto {
		c.Label(pad, y+4, fmt.Sprintf("UI scale %%:  %d (auto)", a.UIScale()), ColTextDim)
	} else {
		uiPct := a.numberRow(y, "UI scale %", a.uiScalePct, config.UIScaleStepPercent, config.MinUIScalePercent, config.MaxUIScalePercent)
		if uiPct != a.uiScalePct {
			a.uiScalePct = uiPct
			a.ctx.SetUIScale(uiPct)
			a.d.Prefs.SetUIScale(uiPct)
		}
	}
	y += 34

	// --- Window size / fullscreen: pick your own client dimensions (a window
	// bigger than the monitor can't be dragged smaller; F11 + Fit to screen are
	// the escapes). All window ops run here on the render thread.
	c.Label(pad, y+4, "Window:", ColText)
	full := a.d.Prefs.WindowFullscreen()
	if next := c.Checkbox(pad+86, y, "Fullscreen (borderless) · F11 toggles", full); next != full {
		a.applyFullscreen(next)
	}
	y += 28
	if a.d.Prefs.WindowFullscreen() {
		c.LabelClipped(pad, y+4, w-2*pad-scrollBarW, "Press F11 or untick Fullscreen to return to a window.", ColTextDim)
		y += 28
	} else {
		cw, ch := a.ctx.WindowSize()
		c.Label(pad, y+4, fmt.Sprintf("Current: %d×%d", cw, ch), ColTextDim)
		bx := pad + 150
		for _, p := range []struct {
			label string
			w, h  int
		}{
			{"1280×720", 1280, 720},
			{"1600×900", 1600, 900},
			{"1920×1080", 1920, 1080},
			{"Default", config.DefaultWindowW, config.DefaultWindowH},
		} {
			bw := c.TextWidth(p.label) + 14
			if c.Button(sdl.Rect{X: bx, Y: y, W: bw, H: btnH}, p.label) {
				a.applyWindowSize(p.w, p.h)
				settings.winLoaded = false
			}
			bx += bw + 6
		}
		y += btnH + 8
		if c.Button(sdl.Rect{X: pad, Y: y, W: 110, H: btnH}, "Fit to screen") {
			a.fitWindowToScreen()
			settings.winLoaded = false
		}
		if !settings.winLoaded { // seed/refresh the custom fields from the live size
			sw, sh := a.ctx.WindowSize()
			if sw <= 0 {
				sw, sh = config.DefaultWindowW, config.DefaultWindowH
			}
			settings.winWInput, settings.winHInput = strconv.Itoa(sw), strconv.Itoa(sh)
			settings.winLoaded = true
		}
		c.Label(pad+126, y+4, "Custom:", ColTextDim)
		var wCommit, hCommit bool
		settings.winWInput, wCommit = c.TextField("winw", sdl.Rect{X: pad + 190, Y: y, W: 66, H: fieldH}, settings.winWInput, "W")
		c.Label(pad+262, y+4, "×", ColTextDim)
		settings.winHInput, hCommit = c.TextField("winh", sdl.Rect{X: pad + 276, Y: y, W: 66, H: fieldH}, settings.winHInput, "H")
		if c.Button(sdl.Rect{X: pad + 350, Y: y, W: 70, H: btnH}, "Apply") || wCommit || hCommit {
			if iw, ew := strconv.Atoi(strings.TrimSpace(settings.winWInput)); ew == nil {
				if ih, eh := strconv.Atoi(strings.TrimSpace(settings.winHInput)); eh == nil && iw > 0 && ih > 0 {
					a.applyWindowSize(iw, ih)
					settings.winLoaded = false
				}
			}
		}
		y += btnH + 10
	}

	// Extras box appearance: a hex colour per element (blank = the stock colour),
	// a live swatch, and a Background → Gradient↓ fade. Applies to the floating
	// Extras box and its torn-off boxes; default (all blank) is byte-identical.
	c.Label(pad, y+4, "Extras box colours:", ColText)
	c.LabelClipped(pad+150, y+4, w-pad-150-scrollBarW, "hex like 78aaff — blank = default · live on the open box", ColTextDim)
	y += 24
	exBg, exBg2, exBorder, exTitle, exText, exGrad := a.d.Prefs.ExtrasBoxStyle()
	cur := [5]string{exBg, exBg2, exBorder, exTitle, exText}
	next := cur
	for i, label := range [5]string{"Background", "Gradient ↓", "Border", "Title bar", "Text"} {
		c.Label(pad+16, y+4, label, ColTextDim)
		swatch := ColPanel
		if col, ok := parseHexColor(next[i]); ok {
			swatch = col
		}
		swR := sdl.Rect{X: pad + 120, Y: y + 1, W: 18, H: 18}
		c.Fill(swR, swatch)
		c.Border(swR, ColTextDim)
		next[i], _ = c.TextField("excol"+strconv.Itoa(i), sdl.Rect{X: pad + 146, Y: y, W: 110, H: fieldH}, next[i], "rrggbb")
		y += 26
	}
	nextGrad := exGrad
	if v := c.Checkbox(pad+16, y, "Background gradient (Background → Gradient ↓)", exGrad); v != exGrad {
		nextGrad = v
	}
	y += 30
	if next != cur || nextGrad != exGrad {
		a.d.Prefs.SetExtrasBoxStyle(next[0], next[1], next[2], next[3], next[4], nextGrad)
	}

	// IC/OOC font override: a chain of TTF/TTC paths, first covering font
	// per line wins (put a CJK-capable font later in the chain).
	c.Label(pad, y+4, "IC/OOC font:", ColText)
	if !settings.fontLoaded {
		settings.fontInput = a.d.Prefs.FontPaths()
		settings.fontLoaded = true
	}
	var fontCommit bool
	settings.fontInput, fontCommit = c.TextField("fontpaths", sdl.Rect{X: pad + 110, Y: y, W: 420, H: fieldH},
		settings.fontInput, `C:\Windows\Fonts\meiryo.ttc; more fallbacks... (blank = built-in)`)
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Apply") || fontCommit {
		raw := strings.TrimSpace(settings.fontInput)
		a.d.Prefs.SetFontPaths(raw)
		a.loadFontChainAsync(raw)
		if raw == "" {
			settings.statusLine = "Font override cleared — built-in font."
		}
	}
	if names := a.ctx.FontChainNames(); len(names) > 0 {
		c.LabelClipped(pad+620, y+4, w-pad-620-scrollBarW, "chain: "+strings.Join(names, " → "), ColTextDim)
	}
	y += 30
	// Dyslexia-friendly preset: one click fills + applies a high-readability
	// font chain (OpenDyslexic if installed, else Verdana). Edit the field to
	// point at any TTF you prefer.
	if c.Button(sdl.Rect{X: pad + 110, Y: y, W: 180, H: btnH}, "Dyslexia-friendly font") {
		settings.fontInput = dyslexiaFontPreset
		a.d.Prefs.SetFontPaths(dyslexiaFontPreset)
		a.loadFontChainAsync(dyslexiaFontPreset)
		settings.statusLine = "Applied a high-readability font (edit the path above for OpenDyslexic)."
	}
	c.LabelClipped(pad+300, y+4, w-pad-300-scrollBarW, "high-readability preset; edit the path to use OpenDyslexic", ColTextDim)
	y += 34
	return y
}

// dyslexiaFontPreset is the one-click readability chain: OpenDyslexic if the
// user installed it, else Verdana (present on every Windows install) — both
// far more legible for dyslexic readers than the default. First covering font
// per line wins, so the missing entries are simply skipped.
const dyslexiaFontPreset = `C:\Windows\Fonts\OpenDyslexic-Regular.ttf; C:\Windows\Fonts\verdana.ttf`

// drawSettingsTheme: theme picker/folder, layout toggle, live preview, bind.
func (a *App) drawSettingsTheme(y, w, h int32) int32 {
	c := a.ctx
	c.Label(pad, y+4, "Theme:", ColText)
	if c.Button(sdl.Rect{X: pad + 60, Y: y, W: 26, H: btnH}, "<") {
		a.cycleTheme(-1)
	}
	nameW := c.TextWidth(settings.themeName)
	c.Label(pad+96, y+6, settings.themeName, ColAccent)
	if c.Button(sdl.Rect{X: pad + 104 + nameW, Y: y, W: 26, H: btnH}, ">") {
		a.cycleTheme(1)
	}
	if settings.themeBusy {
		c.Label(pad+140+nameW, y+6, "scanning...", ColTextDim)
	} else {
		c.Label(pad+140+nameW, y+6, fmt.Sprintf("(%d found)", len(settings.themeList)), ColTextDim)
	}
	y += 32
	c.Label(pad, y+4, "Theme folder:", ColText)
	settings.themeDir, _ = c.TextField("themedir", sdl.Rect{X: pad + 110, Y: y, W: 340, H: fieldH}, settings.themeDir, `optional root holding themes\<name> — or drop a folder anywhere`)
	if c.Button(sdl.Rect{X: pad + 460, Y: y, W: 130, H: btnH}, "Apply & rescan") {
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		a.scanThemes()
		a.applyThemeAsync()
	}
	if runtime.GOOS == "windows" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Browse...") {
			browseForFolder()
		}
	}
	y += 36

	// Theme-driven courtroom geometry (courtroom_design.ini).
	tlay := a.d.Prefs.ThemeLayoutEnabled()
	if next := c.Checkbox(pad, y, "Use the theme's courtroom layout (courtroom_design.ini positions every widget; off = classic layout)", tlay); next != tlay {
		a.d.Prefs.SetThemeLayout(next)
		a.themeLay.valid = false
	}
	y += 28

	// Theme fit: how the theme's FIXED design size fills your (differently
	// shaped) window — the cause of those borders.
	c.Label(pad, y+4, "Theme fit:", ColText)
	fitOpts := []string{"Stretch — fill, no bars", "Letterbox — keep shape (bars)", "Crop — fill, trim edges", "Custom — zoom + pan"}
	fit := a.d.Prefs.ThemeFitMode()
	if next, changed := c.Dropdown("themefit", sdl.Rect{X: pad + 90, Y: y, W: 230, H: fieldH}, fitOpts, fit); changed {
		a.d.Prefs.SetThemeFit(next)
		a.themeLay.valid = false // rebuild the layout cache with the new fit
		fit = next
	}
	c.LabelClipped(pad+330, y+4, w-pad-330-scrollBarW, "applies under a theme that drives the courtroom layout (above)", ColTextDim)
	y += 30
	if fit == config.ThemeFitCustom {
		// Big interactive preview (window-shaped): drag to pan, scroll to zoom.
		boxW := int32(560)
		if avail := w - 2*pad - scrollBarW; boxW > avail {
			boxW = avail
		}
		boxH := boxW * h / w // match the real window aspect so the crop is true
		if boxH > 340 {
			boxH, boxW = 340, 340*w/h
		}
		if boxH < 160 {
			boxH = 160
		}
		a.drawThemeFitPreview(sdl.Rect{X: pad, Y: y, W: boxW, H: boxH})
		y += boxH + 10
		zoom := a.d.Prefs.ThemeZoom()
		if next := a.sliderRow(y, "  Zoom %", zoom, 5, config.MinThemeZoom, config.MaxThemeZoom); next != zoom {
			a.d.Prefs.SetThemeFitZoom(next)
			a.themeLay.valid = false
		}
		y += 28
		px, py := a.d.Prefs.ThemePan()
		if next := a.sliderRow(y, "  Pan X %", px, 1, -config.MaxThemePan, config.MaxThemePan); next != px {
			a.d.Prefs.SetThemeFitPan(next, py)
			a.themeLay.valid = false
		}
		y += 28
		if next := a.sliderRow(y, "  Pan Y %", py, 1, -config.MaxThemePan, config.MaxThemePan); next != py {
			a.d.Prefs.SetThemeFitPan(px, next)
			a.themeLay.valid = false
		}
		y += 30
	}

	// Plain lobby: the server list keeps the readable client backdrop instead of
	// the theme's lobbybackground (which is built for AO2's own list and often
	// makes ours unreadable). The courtroom still uses the theme either way.
	plain := a.d.Prefs.PlainLobbyOn()
	if next := c.Checkbox(pad, y, "Plain lobby — keep my readable server-list backdrop, ignore the theme's lobby image (ON by default; the courtroom still uses the theme)", plain); next != plain {
		a.d.Prefs.SetPlainLobby(next)
	}
	y += 28

	// Live preview of the applied chatbox skin + theme text colors.
	y = a.drawThemePreview(y)
	// Per-server theme binding: "this server always uses that theme".
	y = a.drawThemeBindRow(y, w)
	return y
}

// drawThemeFitPreview draws a big interactive preview of the Custom theme fit:
// the theme's courtroom background scaled + panned exactly as the live courtroom
// would (the box carries the window aspect, so the crop you see is the crop you
// get). Drag to pan, scroll to zoom — both write the prefs and re-fit live.
// Settings-screen only; never the courtroom hot path.
func (a *App) drawThemeFitPreview(box sdl.Rect) {
	c := a.ctx
	c.Fill(box, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	court, ok := a.themeRects["courtroom"]
	if ok && court.W > 0 && court.H > 0 {
		base := float64(box.W) / float64(court.W) // min-fit within the box
		if by := float64(box.H) / float64(court.H); by < base {
			base = by
		}
		s := base * float64(a.d.Prefs.ThemeZoom()) / 100
		artW, artH := int32(float64(court.W)*s), int32(float64(court.H)*s)
		px, py := a.d.Prefs.ThemePan()
		art := sdl.Rect{
			X: box.X + (box.W-artW)/2 + int32(px)*box.W/100,
			Y: box.Y + (box.H-artH)/2 + int32(py)*box.H/100,
			W: artW, H: artH,
		}
		clipPrev, clipHad := c.pushClip(box)
		if page, pok := a.themePage("courtroombackground"); pok {
			_ = c.Ren.Copy(a.themeFrame(page), nil, &art)
		} else {
			c.Fill(art, ColPanel)
			c.Border(art, ColAccent)
			c.LabelClipped(art.X+6, art.Y+6, art.W-12, "(theme ships no courtroombackground — this outline is its design area)", ColTextDim)
		}
		c.popClip(clipPrev, clipHad)
	} else {
		c.LabelClipped(box.X+8, box.Y+8, box.W-16, "This theme has no courtroom_design.ini — the fit modes don't apply to it.", ColTextDim)
	}
	c.Border(box, ColAccent)
	c.Label(box.X+6, box.Y+4, "Drag to pan · scroll to zoom", ColTextDim)

	// Interaction: wheel zooms, drag pans; both clamp via the setters and re-fit
	// the live courtroom by invalidating the geometry cache.
	if c.hovering(box) && c.wheelY != 0 {
		c.wheelTaken = true
		a.d.Prefs.SetThemeFitZoom(a.d.Prefs.ThemeZoom() + int(c.wheelY)*5)
		a.themeLay.valid = false
	}
	if c.hovering(box) && c.mouseDown && !a.themeFitDrag {
		a.themeFitDrag = true
		a.themeFitDragStart = [2]int32{c.mouseX, c.mouseY}
		px, py := a.d.Prefs.ThemePan()
		a.themeFitDragBase = [2]int{px, py}
	}
	if a.themeFitDrag {
		if !c.mouseDown {
			a.themeFitDrag = false
		} else if box.W > 0 && box.H > 0 {
			dx := int(c.mouseX-a.themeFitDragStart[0]) * 100 / int(box.W)
			dy := int(c.mouseY-a.themeFitDragStart[1]) * 100 / int(box.H)
			a.d.Prefs.SetThemeFitPan(a.themeFitDragBase[0]+dx, a.themeFitDragBase[1]+dy)
			a.themeLay.valid = false
		}
	}
}

// drawSettingsAssets: format probing, audio fallbacks, local mounts, the
// opt-in downloader, and the cache browser/actions.
func (a *App) drawSettingsAssets(y, w int32) int32 {
	c := a.ctx
	global := a.d.Prefs.GlobalFallbacks()
	if next := c.Checkbox(pad, y, "Enable format fallbacks globally (probe legacy formats after the preferred one)", global); next != global {
		a.d.Prefs.SetGlobalFallbacks(next)
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
	}
	y += 26

	// Format detection mode: the server manifest by default, manual per-type
	// probing when off. While auto is ON the manual rows are read-only.
	auto := a.d.Prefs.FormatAutoDetect()
	if next := c.Checkbox(pad, y, "Auto-detect formats from the server's extensions.json on connect (recommended)", auto); next != auto {
		auto = next
		a.d.Prefs.SetFormatAutoDetect(next)
		if next {
			a.manifestFor = "" // re-check the current server right away
			a.fetchManifestAsync()
		}
	}
	y += 26
	if auto {
		c.Label(pad, y, "Manual tuning disabled — formats come from each server's extensions.json (untick above to tune by hand):", ColTextDim)
		y += 22
		for _, typeName := range imageTypeNames {
			c.Label(pad, y+2, typeName+":", ColTextDim)
			c.Label(pad+110, y+2, strings.Join(a.d.Prefs.FormatOrder(typeName), "  "), ColTextDim)
			y += 26
		}
	} else {
		c.Label(pad, y, "Image formats probed per asset type (defaults: char_icon=PNG only, everything else=WebP only):", ColTextDim)
		y += 22
		for _, typeName := range imageTypeNames {
			y = a.drawTypeFormatRow(typeName, y)
		}
	}
	y += 8

	// Desk format policy: desks default to WebP and stay WebP even when a
	// server's extensions.json declares another format for backgrounds (which
	// desks share) — unless the player opts in here. Reachable in either mode.
	deskWebP := !a.d.Prefs.DeskFollowsManifest()
	if next := c.Checkbox(pad, y, "Always use WebP for desks, ignoring the server's extensions.json (recommended)", deskWebP); next != deskWebP {
		a.d.Prefs.SetDeskFollowManifest(!next)
		a.d.Prefs.ClearLearnedType(config.TypeDeskOverlay) // re-derive on next probe
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
		if !next { // now following the manifest: re-seed from the current server
			a.manifestFor = ""
			a.fetchManifestAsync()
		}
	}
	y += 28

	// Missing-asset banner: opt-in (default OFF). The failures always reach the
	// debug overlay; this only governs the red on-screen banner.
	showWarn := a.d.Prefs.AssetWarningsOn()
	if next := c.Checkbox(pad, y, "Show missing-asset warnings (red banner naming assets that failed to load — off by default)", showWarn); next != showWarn {
		a.d.Prefs.SetAssetWarnings(next)
	}
	y += 28

	// Audio fallbacks.
	for _, typeName := range []string{config.TypeSFX, config.TypeMusic, config.TypeBlip} {
		enabled := a.d.Prefs.TypeFallbacksEnabled(typeName)
		if next := c.Checkbox(pad, y, typeName+": probe legacy audio formats (.ogg/.wav/.mp3) after .opus", enabled); next != enabled {
			a.d.Prefs.SetTypeFallbacks(typeName, next)
			a.d.Resolver.InvalidateAll()
			a.d.Resolver.WarmFromPrefs()
		}
		y += 24
	}
	y += 10

	// Local assets (no-streaming legacy mode).
	enabled, mounts := a.d.Prefs.LocalAssets()
	if next := c.Checkbox(pad, y, "Read assets from local folders instead of streaming (legacy servers without an asset URL)", enabled); next != enabled {
		a.d.Prefs.SetLocalAssets(next, mounts)
		a.rebuildAssetOrigin()
	}
	y += 28
	c.Label(pad, y+4, "Mount folder:", ColText)
	settings.mountInput, _ = c.TextField("mount", sdl.Rect{X: pad + 110, Y: y, W: 340, H: fieldH}, settings.mountInput, `C:\AO2\base or /home/you/ao2/base`)
	if c.Button(sdl.Rect{X: pad + 460, Y: y, W: 80, H: btnH}, "Add") && strings.TrimSpace(settings.mountInput) != "" {
		a.d.Prefs.SetLocalAssets(enabled, append(mounts, strings.TrimSpace(settings.mountInput)))
		settings.mountInput = ""
		a.rebuildAssetOrigin()
	}
	y += 32
	for i, m := range mounts {
		c.LabelClipped(pad+20, y+4, w-220, fmt.Sprintf("%d. %s", i+1, m), ColText)
		if c.Button(sdl.Rect{X: w - 180, Y: y, W: 90, H: 24}, "Remove") {
			next := append(append([]string{}, mounts[:i]...), mounts[i+1:]...)
			a.d.Prefs.SetLocalAssets(enabled, next)
			a.rebuildAssetOrigin()
			break
		}
		y += 28
	}
	y += 10

	// Built-in single-asset downloader (opt-in).
	y = a.drawDownloaderSettings(y, w)

	// Cache browser: live tier stats, T3 size on demand, open-in-Explorer.
	t2 := a.d.Manager.T2Stats()
	hitPct := 0.0
	if total := t2.Hits + t2.Misses; total > 0 {
		hitPct = float64(t2.Hits) / float64(total) * 100
	}
	c.Label(pad, y, fmt.Sprintf("Memory cache (T2): %d entries · %.1f / %.0f MiB · %.0f%% hit rate · %d evictions",
		t2.Entries, float64(t2.Bytes)/(1<<20), float64(t2.Budget)/(1<<20), hitPct, t2.Evictions), ColTextDim)
	y += 24
	zstdOn := a.d.Prefs.DiskZstdEnabled()
	if next := c.Checkbox(pad, y, "Compress disk cache with zstd (new writes only; smaller T3, tiny CPU on hits — old blobs always read fine)", zstdOn); next != zstdOn {
		a.d.Prefs.SetDiskZstd(next)
		a.d.Manager.SetDiskCompression(next)
	}
	y += 26
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Measure disk cache") {
		measureDiskCacheAsync(a.d.Manager.DiskRoot())
	}
	if c.Button(sdl.Rect{X: pad + 180, Y: y, W: 170, H: btnH}, "Open cache folder") {
		if root := a.d.Manager.DiskRoot(); root != "" {
			// Fire-and-forget Explorer launch; never blocks the frame.
			_ = exec.Command("explorer.exe", root).Start()
		}
	}
	y += 32

	// Cache actions.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Clear disk cache") {
		if err := a.d.Manager.ClearDisk(); err != nil {
			settings.statusLine = "Clear failed: " + err.Error()
		} else {
			settings.statusLine = "Disk cache cleared."
		}
	}
	if c.Button(sdl.Rect{X: pad + 180, Y: y, W: 190, H: btnH}, "Clear learned formats") {
		a.d.Prefs.ClearLearned()
		a.d.Resolver.InvalidateAll()
		settings.statusLine = "Learned formats cleared."
	}
	// Learned-format portability: one player's warm state seeds another's.
	if c.Button(sdl.Rect{X: pad + 380, Y: y, W: 150, H: btnH}, "Export learned") {
		exportLearnedAsync(a)
	}
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 150, H: btnH}, "Import learned") {
		importLearnedAsync(a)
	}
	y += 30
	c.Label(pad, y, "\"Clear disk cache\" wipes the on-disk asset cache (T3); assets re-download fresh on next use.", ColTextDim)
	y += 18
	c.Label(pad, y, "Recommended after a server that's behind Cloudflare / a CDN updates its assets: otherwise the CDN —", ColTextDim)
	y += 18
	c.Label(pad, y, "or your cache — can keep serving the OLD file, so you'd see the wrong (outdated) version. Worth keeping", ColTextDim)
	y += 18
	c.Label(pad, y, "in mind if a character or background looks stale right after a server update.", ColTextDim)
	y += 30

	// Factory reset: opens a pop-up offering settings-only or a full wipe.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 220, H: btnH}, "Reset to defaults…") {
		a.showReset = true
	}
	c.Label(pad+232, y+5, "Reset the settings page, or wipe everything (favourites, logins, data, cache).", ColTextDim)
	y += 30
	return y
}

// drawResetConfirm is the factory-reset pop-up: settings-only (keeps your data,
// logins and cache) or a full wipe (erases everything, fresh-install state). It
// owns the screen + input while open, so its buttons can't double-fire with the
// settings widgets underneath.
func (a *App) drawResetConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 235})
	pw, ph := int32(620), int32(300)
	panel := sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	x, y := panel.X+24, panel.Y+18
	c.Heading(x, y, "Reset AsyncAO", ColText)
	y += 40

	c.LabelClipped(x, y, pw-48, "Reset settings — restores the settings page to defaults (scales, volumes,", ColText)
	y += 18
	c.LabelClipped(x, y, pw-48, "theme, hotkeys, colours, toggles). KEEPS favourites, wardrobes, servers &", ColTextDim)
	y += 18
	c.LabelClipped(x, y, pw-48, "logins, callwords, learned formats, and your disk cache.", ColTextDim)
	y += 26
	if c.Button(sdl.Rect{X: x, Y: y, W: pw - 48, H: btnH}, "Reset settings (keep my data, logins & cache)") {
		a.applyFactoryReset(false)
	}
	y += 42

	c.LabelClipped(x, y, pw-48, "Wipe everything — a brand-new install: ALSO erases favourites, wardrobes,", ColDanger)
	y += 18
	c.LabelClipped(x, y, pw-48, "servers, logins, callwords, jukebox, notes, and the disk cache. No undo.", ColDanger)
	y += 26
	if c.Button(sdl.Rect{X: x, Y: y, W: pw - 48, H: btnH}, "WIPE EVERYTHING — logins, data, cache (no undo)") {
		a.applyFactoryReset(true)
	}
	y += 42
	if c.Button(sdl.Rect{X: x, Y: y, W: 120, H: btnH}, "Cancel") {
		a.showReset = false
	}
}

// applyFactoryReset resets preferences (settings-only or full wipe), clears the
// disk cache on a full wipe, then re-applies the derived UI state and refreshes
// the lobby so the change is visible immediately.
func (a *App) applyFactoryReset(wipeAll bool) {
	if wipeAll {
		a.d.Prefs.ResetAll()
		_ = a.d.Manager.ClearDisk()
		a.d.Resolver.InvalidateAll()
		if a.juke != nil {
			a.juke.Clear() // the jukebox library lives in its own file — wipe it too
		}
		if a.notebook != nil {
			a.notebook.Clear() // empty + stop its flush before deleting the files
		}
		_ = config.WipeNotebooks() // per-server case notes live in their own dir
		a.warnLine = "Everything wiped — fresh-install state."
	} else {
		a.d.Prefs.ResetSettings()
		a.warnLine = "Settings reset to defaults."
	}
	a.applyPrefsToState()
	a.RefreshServers() // favourites/master re-merge (also clears ping state)
	a.showReset = false
	a.warnAt = time.Now()
}

// applyPrefsToState re-pulls the App-cached values derived from preferences —
// the subset NewApp seeds — so a reset (or import) takes effect without a
// restart. Not a hot path.
func (a *App) applyPrefsToState() {
	a.hidden = map[string]bool{}
	for _, id := range a.d.Prefs.HiddenPanels() {
		a.hidden[id] = true
	}
	a.pairOffX, a.pairOffY = a.d.Prefs.PairOffsets()
	a.pairFlip = a.d.Prefs.PairFlipped()
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = a.d.Prefs.LayoutScales()
	a.uiScalePct = a.d.Prefs.UIScale()
	a.ctx.SetUIScale(a.UIScale())
	a.oocName = a.d.Prefs.SavedShowname()
	if s := a.d.Prefs.SavedOOCName(); s != "" {
		a.oocName = s
	}
	a.refreshCharKeys()
	a.applyThemeAsync()
	a.applyAudioVolumes()
	if a.room != nil {
		a.applyTimingToRoom()
		a.room.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
		a.room.ReduceMotion = a.d.Prefs.ReduceMotion()
	}
}

// drawSettingsAudioChat: volumes, message timing, casing alerts, callwords.
func (a *App) drawSettingsAudioChat(y, w int32) int32 {
	c := a.ctx
	// Master volume — scales everything; also on the Extras box for quick access.
	if mv := a.volumeRow(y, "Master volume", a.d.Prefs.MasterVolume()); mv != a.d.Prefs.MasterVolume() {
		a.d.Prefs.SetMasterVolume(mv)
		a.applyAudioVolumes()
	}
	y += 30
	music, sfx, blip := a.d.Prefs.AudioVolumes()
	music = a.volumeRow(y, "Music volume", music)
	y += 26
	sfx = a.volumeRow(y, "SFX volume", sfx)
	y += 26
	blip = a.volumeRow(y, "Blip volume", blip)
	y += 32
	if m0, s0, b0 := a.d.Prefs.AudioVolumes(); m0 != music || s0 != sfx || b0 != blip {
		a.d.Prefs.SetAudioVolumes(music, sfx, blip)
		a.applyAudioVolumes() // honors the session SFX mute
	}
	// Music ducking (off by default): dip the music while a message plays.
	duck := a.d.Prefs.MusicDucking()
	if next := c.Checkbox(pad, y, "Duck music while someone talks (lower music during a message so dialogue stays clear)", duck); next != duck {
		a.d.Prefs.SetMusicDucking(next)
	}
	y += 28

	// Hold-to-clear: hold a key (default Backspace, rebindable) to wipe a text
	// box at once instead of deleting char-by-char.
	hcOn, hcKey, hcMs := a.d.Prefs.HoldClear()
	if next := c.Checkbox(pad, y, "Hold a key to clear a text box (a fast wipe — no holding backspace char-by-char)", hcOn); next != hcOn {
		a.d.Prefs.SetHoldClearOn(next)
	}
	y += 28
	if hcOn {
		c.Label(pad+16, y+4, "Clear key:", ColTextDim)
		keyLabel := hcKey
		if a.holdKeyArmed {
			keyLabel = "press a key…  (Esc cancels)"
		}
		if c.Button(sdl.Rect{X: pad + 96, Y: y, W: 200, H: btnH}, keyLabel) {
			a.holdKeyArmed = !a.holdKeyArmed
		}
		if a.holdKeyArmed && c.keyPressed != 0 {
			if c.keyPressed != sdl.K_ESCAPE {
				a.d.Prefs.SetHoldClearKey(sdl.GetKeyName(c.keyPressed))
			}
			a.holdKeyArmed = false
			c.keyPressed = 0 // consume — don't let the captured key act elsewhere
		}
		y += btnH + 6
		if next := a.sliderRow(y, "  Hold time (ms)", hcMs, 100, config.MinHoldClearMs, config.MaxHoldClearMs); next != hcMs {
			a.d.Prefs.SetHoldClearMs(next)
		}
		c.Label(pad+270, y+4, "how long to hold before it clears (default 1.5 s)", ColTextDim)
		y += 30
	}

	// Message timing (AO2-Client options.ini parity); applies live. Plain
	// descriptions beside each knob so they're self-explanatory.
	c.Label(pad, y, "Text speed — how the IC chatbox plays messages:", ColTextDim)
	y += 20
	crawl, stay, rate := a.d.Prefs.Timing()
	crawl = a.sliderRow(y, "Text crawl ms", crawl, 5, config.MinTextCrawlMs, config.MaxTextCrawlMs)
	c.Label(pad+270, y+4, "delay between letters (higher = slower, easier to read)", ColTextDim)
	y += 26
	stay = a.sliderRow(y, "Text stay ms", stay, 100, 0, config.MaxTextStayMs)
	c.Label(pad+270, y+4, "pause after a message finishes before the next plays", ColTextDim)
	y += 26
	rate = a.sliderRow(y, "Chat limit ms", rate, 100, 0, config.MaxChatRateLimitMs)
	c.Label(pad+270, y+4, "smallest gap between YOUR sent messages (anti-spam)", ColTextDim)
	y += 30
	if c0, s0, r0 := a.d.Prefs.Timing(); c0 != crawl || s0 != stay || r0 != rate {
		a.d.Prefs.SetTiming(crawl, stay, rate)
		a.applyTimingToRoom()
	}

	// Packed-room catch-up vs. the OG-client queue. ON (default) = webAO pacing:
	// when 20 people talk at once, backlog messages skip their animations so the
	// stage tracks real-time. OFF = the original AO2 client's queue — every line
	// plays in full and in order, nothing cut off, but a room full of webAO users
	// (who skip the queue) can leave you behind. The log keeps every line either
	// way. (This is the "message queue" toggle — it was a setting, not removed.)
	cuOn, cuThresh := a.d.Prefs.CatchUp()
	const cuLabel = "Catch up in packed rooms — uncheck for the OG-client queue (every line plays in full, nothing cut off)"
	if next := c.Checkbox(pad, y, cuLabel, cuOn); next != cuOn {
		a.d.Prefs.SetCatchUp(next, cuThresh)
		a.applyTimingToRoom()
	}
	c.Tooltip(sdl.Rect{X: pad, Y: y, W: 22 + c.TextWidth(cuLabel), H: 16},
		"On (default): in a packed room, queued messages skip their animations so chat stays at real-time — like webAO. Off: the original AO2 client's queue — every IC message plays in full and in order (nothing cut off), but a roomful of webAO users can leave you minutes behind. The IC log keeps every line either way.")
	y += 26
	c.Label(pad+22, y, "Checked (default): webAO pacing — in a busy room, backlog messages skip their animation to keep chat current (they can flash past).", ColTextDim)
	y += 18
	c.Label(pad+22, y, "Unchecked: the original AO2 client's queue — every message plays out in full and in order, nothing skipped (busy webAO rooms may lag you).", ColTextDim)
	y += 22
	if cuOn {
		nt := a.numberRow(y, "Catch up after", cuThresh, 1, 1, 50)
		c.Label(pad+270, y+4, "fast-forward once at least this many messages are waiting (1 = stay on the newest)", ColTextDim)
		if nt != cuThresh {
			a.d.Prefs.SetCatchUp(cuOn, nt)
			a.applyTimingToRoom()
		}
		y += 30
	}

	// Per-area IC scrollback (opt-in): each visited area keeps its own log.
	areaScroll := a.d.Prefs.PerAreaScrollbackOn()
	if next := c.Checkbox(pad, y, "Per-area chat scrollback (OFF by default): each area keeps its own IC log; switches when you click an area in the Areas list", areaScroll); next != areaScroll {
		a.d.Prefs.SetPerAreaScrollback(next)
	}
	y += 26
	// Detailed transcript logging (opt-in): full IC record to a file.
	detLog := a.d.Prefs.DetailedLogOn()
	if next := c.Checkbox(pad, y, "Detailed logging (OFF by default): append every IC line to logs/transcript.log with timestamp, server, area, character + showname", detLog); next != detLog {
		a.d.Prefs.SetDetailedLog(next)
	}
	y += 26

	// Case announcements (CASEA, tsuserver-family): subscribe by role.
	y = a.drawCasingRow(y)

	// Callwords: comma-separated highlight words (flash + sound on match).
	c.Label(pad, y+4, "Callwords:", ColText)
	if !settings.callLoaded {
		settings.callInput = strings.Join(a.d.Prefs.CallWords(), ", ")
		settings.callLoaded = true
	}
	var callCommit bool
	settings.callInput, callCommit = c.TextField("callwords", sdl.Rect{X: pad + 110, Y: y, W: 420, H: fieldH}, settings.callInput, "your name, nickname, ... (flash + sound when seen in IC/OOC)")
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Save") || callCommit {
		a.d.Prefs.SetCallWords(strings.Split(settings.callInput, ","))
		settings.statusLine = "Callwords saved."
	}
	y += 30
	c.Label(pad, y+4, "Callword sound:", ColTextDim)
	if next, _ := c.TextField("cwsound", sdl.Rect{X: pad + 120, Y: y, W: 490, H: fieldH}, a.d.Prefs.CallwordSoundPath(), "custom .wav/.ogg/.mp3/.opus path (blank = built-in ping)"); next != a.d.Prefs.CallwordSoundPath() {
		a.d.Prefs.SetCallwordSoundPath(next)
	}
	y += 32
	if c.Button(sdl.Rect{X: pad + 120, Y: y, W: 130, H: btnH}, "Test sound") {
		// Play exactly what a callword/friend alert fires: the custom file if set,
		// else the built-in ping — so people can confirm it's actually audible.
		if f := a.d.Prefs.CallwordSoundPath(); f != "" {
			a.d.Audio.PlayFile(f)
		} else {
			a.d.Audio.PlayAlert()
		}
	}
	c.Label(pad+260, y+6, "play the alert sound now to check it works", ColTextDim)
	y += 34
	ct := a.d.Prefs.CallwordToastOn()
	if next := c.Checkbox(pad, y, "Toast when a callword is heard (ON by default): a popup names the word, like the modcall/friend toasts.", ct); next != ct {
		a.d.Prefs.SetCallwordToast(next)
	}
	y += 30

	mc := a.d.Prefs.MessageCounterOn()
	if next := c.Checkbox(pad, y, "Show a character count by the IC box (ON by default): turns red past ~256 chars, where many servers truncate.", mc); next != mc {
		a.d.Prefs.SetMessageCounter(next)
	}
	y += 26

	ts := a.d.Prefs.ICTimestampsOn()
	if next := c.Checkbox(pad, y, "Show local timestamps in the IC log (ON by default): each line is prefixed with the time it arrived, so you can see when people spoke.", ts); next != ts {
		a.d.Prefs.SetICTimestamps(next)
	}
	y += 30

	// Highlighted friends (per server): shownames whose IC messages glow.
	fh := a.d.Prefs.FriendHighlightOn()
	if next := c.Checkbox(pad, y, "Highlight friends in the IC log (OFF by default): their messages glow. Matches the DISPLAYED name, so it can be spoofed.", fh); next != fh {
		a.d.Prefs.SetFriendHighlight(next)
	}
	y += 26
	fgp := a.d.Prefs.FriendGlowPulseOn()
	if next := c.Checkbox(pad+16, y, "Pulse the friend glow (gentle breathing animation; obeys reduce-motion)", fgp); next != fgp {
		a.d.Prefs.SetFriendGlowPulse(next)
	}
	y += 26
	fn := a.d.Prefs.FriendNotifyOn()
	if next := c.Checkbox(pad+16, y, "Notify + flash the taskbar when a friend speaks (fires even from a backgrounded server tab)", fn); next != fn {
		a.d.Prefs.SetFriendNotify(next)
	}
	y += 26
	fot := a.d.Prefs.FriendOSToastOn()
	if next := c.Checkbox(pad+16, y, "Also pop a DESKTOP (OS) notification — a real Windows toast (rate-limited so it can't storm)", fot); next != fot {
		a.d.Prefs.SetFriendOSToast(next)
	}
	y += 26
	fsnd := a.d.Prefs.FriendSoundOn()
	if next := c.Checkbox(pad+16, y, "Play a sound when a friend speaks", fsnd); next != fsnd {
		a.d.Prefs.SetFriendSound(next)
	}
	y += 26
	c.Label(pad+16, y+4, "Friend sound:", ColTextDim)
	if next, _ := c.TextField("friendsound", sdl.Rect{X: pad + 130, Y: y, W: 480, H: fieldH}, a.d.Prefs.FriendSoundPath(), "custom .wav/.ogg/.mp3/.opus path (blank = built-in ping)"); next != a.d.Prefs.FriendSoundPath() {
		a.d.Prefs.SetFriendSoundPath(next)
	}
	y += 32
	if a.serverKey == "" {
		c.Label(pad, y+4, "Friends: connect to a server to set its highlighted shownames.", ColTextDim)
		y += 30
	} else {
		c.Label(pad, y+4, "Friends:", ColText)
		if settings.friendKey != a.serverKey { // reload buffer per server
			settings.friendInput = strings.Join(a.d.Prefs.ServerFriends(a.serverKey), ", ")
			settings.friendKey = a.serverKey
		}
		var friendCommit bool
		settings.friendInput, friendCommit = c.TextField("friends", sdl.Rect{X: pad + 110, Y: y, W: 420, H: fieldH}, settings.friendInput, "showname1, showname2=ffcc00, ... (saved per server)")
		if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Save") || friendCommit {
			a.d.Prefs.SetServerFriends(a.serverKey, strings.Split(settings.friendInput, ","))
			settings.statusLine = "Friends saved for this server."
		}
		y += 28
		c.Label(pad+110, y, "Append =RRGGBB to a name to give that friend a custom glow colour (e.g. blank=ff4488).", ColTextDim)
		y += 24
	}
	// Mod-call desktop toast (for moderators): not friend-related, so it sits at
	// the section's top level.
	y += 6
	mct := a.d.Prefs.ModcallToastOn()
	if next := c.Checkbox(pad, y, "Desktop notification on mod-call (OFF by default): pop a Windows toast when a modcall comes in — for mods who alt-tabbed away", mct); next != mct {
		a.d.Prefs.SetModcallToast(next)
	}
	y += 28
	return y
}

// drawSettingsAccount: per-server login, the master-list override, Discord.
func (a *App) drawSettingsAccount(y, w int32) int32 {
	c := a.ctx
	// Auto-login: ITS OWN automation, not a macro — per-server creds,
	// software-detected wire flow, fires on join (or via hotkey/button).
	y = a.drawLoginSettings(y, w)
	y += 8

	// Master list override (blank = official). Refresh in the lobby applies.
	c.Label(pad, y+4, "Master list:", ColText)
	master := a.d.Prefs.MasterList()
	if next, _ := c.TextField("masterurl", sdl.Rect{X: pad + 110, Y: y, W: 420, H: fieldH}, master, network.DefaultMasterServerURL); next != master {
		a.d.Prefs.SetMasterList(next)
	}
	y += 34

	// Discord Rich Presence (optional — never required to build or run).
	y = a.drawDiscordRow(y, w)
	return y
}

// drawSettingsHotkeys: hotkey rebinds, macros, and the whole-settings bundle.
func (a *App) drawSettingsHotkeys(y, w int32) int32 {
	c := a.ctx
	c.Label(pad, y, "Hotkeys (Ctrl + key — single letters/digits; blank uses the default):", ColTextDim)
	y += 22
	hx := pad
	for _, def := range hotkeyDefs {
		c.Label(hx, y+4, def.label+":", ColText)
		cur := a.d.Prefs.Hotkey(def.id)
		placeholder := def.def
		next, _ := c.TextField("hk_"+def.id, sdl.Rect{X: hx + 150, Y: y, W: 44, H: fieldH}, cur, placeholder)
		if next != cur {
			a.d.Prefs.SetHotkey(def.id, strings.ToLower(strings.TrimSpace(next)))
		}
		hx += 210
		if hx > 700 {
			hx = pad
			y += 30
		}
	}
	y += 36

	// Macros: user-defined OOC command sequences with optional keybinds.
	y = a.drawMacroSettings(y, w)
	y += 8

	// Whole-settings portability: the new-PC bundle (every knob, favorites,
	// per-server wardrobes/keybinds, learned formats — minus passwords).
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Export settings") {
		exportSettingsAsync(a)
	}
	importLabel := "Import settings..."
	if settings.importArmed {
		importLabel = "Drop the .json here"
	}
	if c.Button(sdl.Rect{X: pad + 180, Y: y, W: 190, H: btnH}, importLabel) {
		settings.importArmed = !settings.importArmed
		if settings.importArmed {
			settings.statusLine = "Drop an exported asyncao-settings .json anywhere on this window."
		}
	}
	y += 36
	return y
}

// measureDiskCacheAsync walks the T3 directory off-thread and reports the
// blob count + total size on the status line.
func measureDiskCacheAsync(root string) {
	if root == "" {
		return
	}
	go func() {
		var files int
		var bytes int64
		_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // best-effort measurement
			}
			if info, ierr := d.Info(); ierr == nil {
				files++
				bytes += info.Size()
			}
			return nil
		})
		line := fmt.Sprintf("Disk cache (T3): %d blobs · %.1f MiB at %s", files, float64(bytes)/(1<<20), root)
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// learnedExportFileName sits next to the executable — easy to hand to a
// friend, easy to find.
const learnedExportFileName = "learned-formats.json"

func learnedExportPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), learnedExportFileName), nil
}

// exportLearnedAsync writes the learned table off-thread (§17.2: no sync
// disk I/O on the render thread) and reports on the status line.
func exportLearnedAsync(a *App) {
	go func() {
		path, err := learnedExportPath()
		if err == nil {
			var data []byte
			if data, err = a.d.Prefs.ExportLearnedJSON(); err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
		}
		line := "Learned formats exported to " + path
		if err != nil {
			line = "Export failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// exportSettingsAsync writes the whole-settings bundle beside the exe
// (timestamped, so repeated exports never clobber each other).
func exportSettingsAsync(a *App) {
	go func() {
		var path string
		exe, err := os.Executable()
		if err == nil {
			path = filepath.Join(filepath.Dir(exe),
				"asyncao-settings-"+time.Now().Format("20060102-150405")+".json")
			err = a.d.Prefs.ExportSettings(path)
		}
		line := "Settings exported to " + path + " — copy it to the new PC and Import there. (Saved passwords are NOT exported; re-enter them there.)"
		if err != nil {
			line = "Settings export failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// importSettingsAsync replaces the preferences file with a dropped
// bundle; the import owns the file from then on (saver freezes) and
// applies on the next start.
func importSettingsAsync(a *App, path string) {
	go func() {
		line := "Settings imported — RESTART AsyncAO to apply (changes made this session won't save)."
		if err := a.d.Prefs.ImportSettings(path); err != nil {
			line = "Settings import failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// importLearnedAsync merges a learned-formats export and republishes the
// resolver snapshot (its table swap is atomic — safe off-thread).
func importLearnedAsync(a *App) {
	go func() {
		var line string
		path, err := learnedExportPath()
		if err == nil {
			var data []byte
			if data, err = os.ReadFile(path); err == nil {
				var n int
				if n, err = a.d.Prefs.ImportLearnedJSON(data); err == nil {
					a.d.Resolver.WarmFromPrefs()
					line = fmt.Sprintf("Imported %d learned entries from %s", n, path)
				}
			}
		}
		if err != nil {
			line = "Import failed: " + err.Error() + " (expected " + learnedExportFileName + " beside the exe)"
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// drawTypeFormatRow renders the per-type format checkboxes; ticking builds a
// new format order: the type's default first, then enabled extras in the
// OptionalImageFormats order.
func (a *App) drawTypeFormatRow(typeName string, y int32) int32 {
	c := a.ctx
	c.Label(pad, y+2, typeName+":", ColText)
	x := pad + 110

	current := a.d.Prefs.FormatOrder(typeName)
	enabled := map[string]bool{}
	for _, ext := range current {
		enabled[ext] = true
	}

	changed := false
	for _, ext := range config.OptionalImageFormats {
		on := enabled[ext]
		next := c.Checkbox(x, y, ext, on)
		if next != on {
			enabled[ext] = next
			changed = true
		}
		x += c.TextWidth(ext) + 46
	}
	if changed {
		def := config.DefaultFormatOrder(typeName)
		order := make([]string, 0, len(config.OptionalImageFormats))
		for _, ext := range def {
			if enabled[ext] {
				order = append(order, ext)
			}
		}
		for _, ext := range config.OptionalImageFormats {
			if enabled[ext] && !containsExt(order, ext) {
				order = append(order, ext)
			}
		}
		if len(order) == 0 {
			order = def // never allow zero probes
		}
		a.d.Prefs.SetFormatOrder(typeName, order)
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
	}

	// Probe-order chips: with 2+ formats ticked, clicking a chip promotes
	// it one slot toward "probed first" (zero-fallback order is the user's
	// to arrange — ticking chooses the set, chips choose the order).
	if len(current) > 1 && !changed {
		c.Label(x+12, y+2, "order:", ColTextDim)
		cx := x + 12 + c.TextWidth("order:") + 8
		for i, ext := range current {
			bw := c.TextWidth(ext) + 14
			if c.Button(sdl.Rect{X: cx, Y: y, W: bw, H: 22}, ext) && i > 0 {
				order := append([]string(nil), current...)
				order[i-1], order[i] = order[i], order[i-1]
				a.d.Prefs.SetFormatOrder(typeName, order)
				a.d.Resolver.InvalidateAll()
				a.d.Resolver.WarmFromPrefs()
			}
			cx += bw + 6
		}
	}
	return y + 26
}

func containsExt(list []string, ext string) bool {
	for _, e := range list {
		if e == ext {
			return true
		}
	}
	return false
}

// drawDiscordRow renders the optional Rich Presence section: a master
// toggle (default OFF), one checkbox per displayed field (the tick-on
// defaults show showname + character + server; the area stays private
// unless chosen), and the application-ID field. Returns the next y.
func (a *App) drawDiscordRow(y, w int32) int32 {
	c := a.ctx
	dp := a.d.Prefs.Discord()
	changed := false
	if next := c.Checkbox(pad, y, "Discord Rich Presence (\"Playing AsyncAO\" on your profile while Discord runs; fully optional)", dp.Enabled); next != dp.Enabled {
		dp.Enabled = next
		changed = true
	}
	y += 26
	if dp.Enabled {
		c.Label(pad+20, y+2, "Show:", ColTextDim)
		x := pad + 70
		fields := []struct {
			label string
			v     *bool
		}{
			{"server", &dp.ShowServer},
			{"character", &dp.ShowChar},
			{"showname", &dp.ShowName},
			{"area", &dp.ShowArea},
		}
		for _, f := range fields {
			if next := c.Checkbox(x, y, f.label, *f.v); next != *f.v {
				*f.v = next
				changed = true
			}
			x += c.TextWidth(f.label) + 52
		}
		y += 28
		c.Label(pad+20, y+4, "App ID:", ColText)
		if next, _ := c.TextField("discordappid", sdl.Rect{X: pad + 90, Y: y, W: 220, H: fieldH}, dp.AppID, "Discord application ID"); next != dp.AppID {
			dp.AppID = next
			changed = true
		}
		status := "(create an app named AsyncAO at discord.com/developers, icon asset \"appicon\"; ID changes apply on restart)"
		if a.d.Presence != nil {
			status = "status: " + a.d.Presence.Status() + " — ID changes apply on restart"
		}
		c.LabelClipped(pad+320, y+4, w-pad-330, status, ColTextDim)
		y += 32
	}
	if changed {
		a.d.Prefs.SetDiscord(dp)
		a.updatePresence()
	}
	return y + 4
}

// drawDownloaderSettings renders the opt-in single-asset downloader section:
// the master toggle (OFF by default), the benefit explainer, the downloads
// folder with open / add-to-mounts actions, and the live job status + Cancel.
// Returns the next y. (Folds into its own tab when the settings screen is
// tabbed.)
func (a *App) drawDownloaderSettings(y, w int32) int32 {
	c := a.ctx
	on := a.d.Prefs.CharDownloaderEnabled()
	if next := c.Checkbox(pad, y, "Built-in downloader (OFF by default) — grab one character or background straight from the server", on); next != on {
		a.d.Prefs.SetCharDownloader(next)
	}
	y += 24
	c.Label(pad+20, y, "Tired of a multi-GB pack download for one file? Turn this on and a Download button appears on each", ColTextDim)
	y += 18
	c.Label(pad+20, y, "character (char-select) and background (Background menu) cell. Characters also pull the sfx/blips their", ColTextDim)
	y += 18
	c.Label(pad+20, y, "char.ini references (those live outside the folder). Files save below — point \"Read assets from local", ColTextDim)
	y += 18
	c.Label(pad+20, y, "folders\" (above) at this folder to use them offline / in rehearsal.", ColTextDim)
	y += 24

	if on {
		// Bandwidth cap (KiB/s; 0 = unlimited — the default, so grabs run full
		// speed unless you throttle them). Average-rate, applied per grab.
		capKBps := a.d.Prefs.DownloadCapKBps()
		if next := a.numberRow(y, "Bandwidth cap", capKBps, 256, 0, 1<<20); next != capKBps {
			a.d.Prefs.SetDownloadCapKBps(next)
		}
		c.Label(pad+270, y+4, "KiB/s — 0 = unlimited (full speed)", ColTextDim)
		y += 30
		root := downloadsRoot()
		c.LabelClipped(pad+20, y+4, w-pad-360-scrollBarW, "Folder: "+root, ColText)
		if c.Button(sdl.Rect{X: w - pad - 340 - scrollBarW, Y: y, W: 150, H: btnH}, "Open folder") {
			_ = exec.Command("explorer.exe", root).Start()
		}
		if c.Button(sdl.Rect{X: w - pad - 180 - scrollBarW, Y: y, W: 180, H: btnH}, "Add to local mounts") {
			enabled, mounts := a.d.Prefs.LocalAssets()
			a.d.Prefs.SetLocalAssets(enabled, append(mounts, root))
			a.rebuildAssetOrigin()
			settings.statusLine = "Added the downloads folder to local mounts."
		}
		y += 30
		if a.dl.active || len(a.dl.queue) > 0 {
			status := a.dl.status
			pauseLabel := "Pause"
			if a.dlPaused.Load() {
				pauseLabel = "Resume"
				status = "Paused — " + status
			}
			c.LabelClipped(pad+20, y+4, w-pad-240-scrollBarW, status, ColAccent)
			if c.Button(sdl.Rect{X: w - pad - 218 - scrollBarW, Y: y, W: 100, H: btnH}, pauseLabel) {
				a.dlPaused.Store(!a.dlPaused.Load())
			}
			if c.Button(sdl.Rect{X: w - pad - 110 - scrollBarW, Y: y, W: 110, H: btnH}, "Cancel") {
				a.cancelDownload()
			}
			y += 30
		} else if a.dl.status != "" {
			c.LabelClipped(pad+20, y+4, w-pad-40-scrollBarW, a.dl.status, ColTextDim)
			y += 26
		}
	}
	return y + 8
}

// casingRoles drives the per-role subscription checkboxes (wire order).
var casingRoles = []struct {
	bit   int
	label string
}{
	{courtroom.CaseRoleDef, "def"},
	{courtroom.CaseRolePro, "pro"},
	{courtroom.CaseRoleJudge, "judge"},
	{courtroom.CaseRoleJury, "jury"},
	{courtroom.CaseRoleSteno, "steno"},
}

// drawCasingRow renders the case-announcement subscription (SETCASE roles);
// changes re-subscribe live when connected. Returns the next y.
func (a *App) drawCasingRow(y int32) int32 {
	c := a.ctx
	enabled, roles := a.d.Prefs.Casing()
	changed := false
	if next := c.Checkbox(pad, y, "Case announcements (get notified when someone needs your role)", enabled); next != enabled {
		enabled = next
		changed = true
	}
	y += 26
	if enabled {
		x := pad + 20
		for _, r := range casingRoles {
			on := roles&r.bit != 0
			if next := c.Checkbox(x, y, r.label, on); next != on {
				roles ^= r.bit
				changed = true
			}
			x += c.TextWidth(r.label) + 52
		}
		y += 26
	}
	if changed {
		a.d.Prefs.SetCasing(enabled, roles)
		a.sendCasingPrefs() // live re-subscribe (no-op when disconnected)
	}
	return y + 8
}

// --- theme picker -----------------------------------------------------------

// drawThemePreview renders the applied theme's chatbox skin (or the flat
// fallback panel) with sample text in the theme's colors — instant visual
// proof of what the current pick actually changes. Returns the next y.
func (a *App) drawThemePreview(y int32) int32 {
	c := a.ctx
	const prevW, prevH = 340, 70
	prev := sdl.Rect{X: pad, Y: y, W: prevW, H: prevH}
	skinned := false
	if page, ok := a.themePage(themeStemChatbox); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &prev)
		skinned = true
	}
	if !skinned {
		c.Fill(prev, sdl.Color{R: 16, G: 16, B: 24, A: 215})
		c.Border(prev, ColAccent)
	}
	// Same skin-gated color rule as the live chatbox (readability).
	nameCol := ColAccent
	if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	msgCol := ColText
	if skinned && a.themeHasMsg {
		msgCol = a.themeMsgCol
	}
	c.Label(prev.X+8, prev.Y+6, "Showname", nameCol)
	c.Label(prev.X+8, prev.Y+30, "Message text preview.", msgCol)
	label := "preview: theme chatbox skin"
	if !skinned {
		label = "preview: no chatbox skin in this theme (flat panel)"
	}
	c.Label(prev.X+prevW+12, prev.Y+6, label, ColTextDim)
	return y + prevH + 10
}

// drawThemeBindRow binds the PICKED theme to a chosen server: joining
// that server applies the bound theme, leaving restores the global one.
// Works for any known server (same picker as the login section).
func (a *App) drawThemeBindRow(y, w int32) int32 {
	c := a.ctx
	names, keys := a.loginTargets()
	if len(names) == 0 {
		return y
	}
	cur := 0
	if settings.themeBindKey == "" && a.serverKey != "" {
		settings.themeBindKey = a.serverKey
	}
	for i, k := range keys {
		if k == settings.themeBindKey {
			cur = i
			break
		}
	}
	settings.themeBindKey = keys[cur]
	c.Label(pad, y+4, "Bind theme to server:", ColText)
	if next, changed := c.Dropdown("themebindsrv", sdl.Rect{X: pad + 170, Y: y, W: 260, H: btnH}, names, cur); changed {
		settings.themeBindKey = keys[next]
	}
	bound := a.d.Prefs.ServerWarmInfoFor(settings.themeBindKey).Theme
	if c.Button(sdl.Rect{X: pad + 440, Y: y, W: 150, H: btnH}, "Bind "+clampLine(settings.themeName)) {
		a.d.Prefs.SetServerTheme(settings.themeBindKey, settings.themeName)
		if settings.themeBindKey == a.serverKey && a.sess != nil {
			a.themeBound = settings.themeName
			a.ensureThemeForSession()
		}
		settings.statusLine = "Theme " + settings.themeName + " bound — that server always uses it now."
	}
	if bound != "" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Unbind") {
			a.d.Prefs.SetServerTheme(settings.themeBindKey, "")
			if settings.themeBindKey == a.serverKey && a.sess != nil {
				a.themeBound = ""
				a.ensureThemeForSession()
			}
			settings.statusLine = "Theme binding removed."
		}
		c.LabelClipped(pad+700, y+4, w-pad-700-scrollBarW, "bound: "+bound, ColAccent)
	} else {
		c.Label(pad+600, y+4, "no binding (uses the global theme)", ColTextDim)
	}
	return y + 32
}

// cycleTheme steps through the scanned theme list and persists the pick.
func (a *App) cycleTheme(step int) {
	list := settings.themeList
	if len(list) == 0 {
		return
	}
	idx := 0
	for i, name := range list {
		if name == settings.themeName {
			idx = i
			break
		}
	}
	idx = (idx + step + len(list)) % len(list)
	settings.themeName = list[idx]
	a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
	a.applyThemeAsync() // chatbox skin + colors follow the pick live
}

// scanThemes lists themes/<name> directories under the custom root and the
// executable's directory, off-thread; pollThemeScan picks up the result.
func (a *App) scanThemes() {
	if settings.themeBusy {
		return
	}
	settings.themeBusy = true
	customRoot := strings.TrimSpace(settings.themeDir)
	go func() {
		root, pick := normalizeThemeRoot(customRoot)
		roots := make([]string, 0, 2)
		if root != "" {
			roots = append(roots, root)
		}
		if exe, err := os.Executable(); err == nil {
			roots = append(roots, filepath.Dir(exe))
		}
		settings.themeRes <- themeScan{names: scanThemeDirs(roots), root: root, pickName: pick}
	}()
}

func (a *App) pollThemeScan() {
	select {
	case res := <-settings.themeRes:
		settings.themeBusy = false
		settings.themeList = res.names
		// The scanner may have normalized the pasted path (the themes
		// folder itself, or one theme inside it) into the root
		// theme.Load expects — reflect and persist it.
		if res.root != "" && res.root != strings.TrimSpace(settings.themeDir) {
			settings.themeDir = res.root
			settings.statusLine = "Theme folder normalized to " + res.root
		}
		if res.pickName != "" {
			settings.themeName = res.pickName
		}
		if res.root != "" || res.pickName != "" {
			a.d.Prefs.SetTheme(settings.themeName, settings.themeDir)
			a.applyThemeAsync()
		}
	default:
	}
}

// themeINIFiles marks a directory as a single theme folder.
var themeINIFiles = []string{theme.DesignFileName, theme.FontsFileName, theme.SoundsFileName}

// normalizeThemeRoot turns whatever the user pasted or dropped into the
// root theme.Load expects (the folder CONTAINING themes/). Users paste
// all three shapes — the root, the themes folder itself, or a single
// theme inside it (returned as pickName and auto-selected). Runs off the
// render thread (it stats directories).
func normalizeThemeRoot(path string) (root, pickName string) {
	// Explorer's "Copy as path" wraps in quotes — strip them or every
	// stat below misses and the root never normalizes.
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return "", ""
	}
	path = filepath.Clean(path)
	// A single theme folder? Its name is the pick; the root is two up
	// (…/root/themes/<name> → …/root).
	for _, ini := range themeINIFiles {
		if _, err := os.Stat(filepath.Join(path, ini)); err == nil {
			return filepath.Dir(filepath.Dir(path)), filepath.Base(path)
		}
	}
	// The themes folder itself → its parent is the root.
	if strings.EqualFold(filepath.Base(path), theme.ThemesDirName) {
		return filepath.Dir(path), ""
	}
	return path, ""
}

// volumeRow draws one "<name>  [====slider====] NN%" control and returns the
// value. A draggable slider (click/drag anywhere on the track) instead of
// +/- buttons — far nicer to set than stepping a button. Continuous 0–100.
func (a *App) volumeRow(y int32, name string, value int) int {
	c := a.ctx
	c.Label(pad, y+4, name+":", ColText)
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}
	value = int(c.Slider(name, track, int32(value), 100))
	c.Label(track.X+track.W+12, y+4, fmt.Sprintf("%3d%%", value), ColAccent)
	return value
}

// numberRow is volumeRow for arbitrary units/steps/bounds (spinbox-style:
// −/+ plus mousewheel over the row).
func (a *App) numberRow(y int32, label string, value, step, min, max int) int {
	c := a.ctx
	c.Label(pad, y+4, label+":", ColText)
	if c.Button(sdl.Rect{X: pad + 130, Y: y, W: 24, H: 24}, "-") && value-step >= min {
		value -= step
	}
	c.Label(pad+162, y+4, fmt.Sprintf("%5d", value), ColAccent)
	if c.Button(sdl.Rect{X: pad + 224, Y: y, W: 24, H: 24}, "+") && value+step <= max {
		value += step
	}
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 252, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered spinbox owns the wheel — no page scroll
		next := value + int(c.wheelY)*step
		if next >= min && next <= max {
			value = next
		}
	}
	return value
}

// sliderRow is numberRow drawn as a draggable slider (same signature, so it's
// a drop-in): label, a slider track, then the live value — all left of the
// pad+270 help text the settings rows use. Drag for coarse; mousewheel over
// the row still fine-tunes by ±step (numberRow parity). The result snaps to
// the step grid and clamps to [min, max].
func (a *App) sliderRow(y int32, label string, value, step, min, max int) int {
	c := a.ctx
	c.Label(pad, y+4, label+":", ColText)
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 90, H: 16}
	if span := max - min; span > 0 {
		value = min + int(c.Slider(label, track, int32(value-min), int32(span)))
	}
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 252, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		value += int(c.wheelY) * step
	}
	if value < min {
		value = min
	}
	if value > max {
		value = max
	}
	if step > 1 { // snap to the step grid for clean values, then re-clamp
		value = ((value-min+step/2)/step)*step + min
		if value > max {
			value = max
		}
	}
	c.Label(track.X+track.W+8, y+4, fmt.Sprintf("%d", value), ColAccent)
	return value
}

// previewDelayRow draws the sprite-preview hover dwell as a draggable slider
// with a seconds readout (the value is stored in milliseconds; a raw "5000"
// would be opaque). Bounds mirror the config clamp — SetPreviewHoverMs is
// authoritative — and the result snaps to the half-second grid.
func (a *App) previewDelayRow(y int32, ms int) int {
	c := a.ctx
	const (
		minMs  = 500   // == config.minPreviewHoverMs (setter is authoritative)
		maxMs  = 15000 // == config.maxPreviewHoverMs
		stepMs = 500   // half-second grid
	)
	c.Label(pad, y+4, "Preview after hovering:", ColText)
	track := sdl.Rect{X: pad + 170, Y: y + 5, W: 120, H: 16}
	if span := maxMs - minMs; span > 0 {
		ms = minMs + int(c.Slider("previewdelay", track, int32(ms-minMs), int32(span)))
	}
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 300, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		ms += int(c.wheelY) * stepMs
	}
	if ms < minMs {
		ms = minMs
	}
	if ms > maxMs {
		ms = maxMs
	}
	ms = ((ms-minMs+stepMs/2)/stepMs)*stepMs + minMs // snap to the grid
	if ms > maxMs {
		ms = maxMs
	}
	c.Label(track.X+track.W+8, y+4, fmt.Sprintf("%.1f s", float64(ms)/1000), ColAccent)
	return ms
}

// browseForFolder shells the native Windows folder picker on a goroutine;
// the chosen path lands on folderRes (empty = cancelled, dropped).
func browseForFolder() {
	if settings.browseBusy {
		return
	}
	settings.browseBusy = true
	go func() {
		const dialog = `Add-Type -AssemblyName System.Windows.Forms; ` +
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
			`$d.Description = 'Pick the folder that CONTAINS themes\<name>'; ` +
			`if ($d.ShowDialog() -eq 'OK') { Write-Output $d.SelectedPath }`
		out, err := exec.Command("powershell", "-NoProfile", "-STA", "-Command", dialog).Output()
		path := strings.TrimSpace(string(out))
		if err != nil || path == "" {
			settings.folderRes <- ""
			return
		}
		settings.folderRes <- path
	}()
}

// resolveDroppedFolder turns an SDL drop path into a directory off-thread
// (a dropped file means "its folder") and feeds the same channel as Browse.
func resolveDroppedFolder(path string) {
	go func() {
		st, err := os.Stat(path)
		if err != nil {
			settings.folderRes <- ""
			return
		}
		if !st.IsDir() {
			path = filepath.Dir(path)
		}
		settings.folderRes <- path
	}()
}

func (a *App) pollFolderPick() {
	select {
	case path := <-settings.folderRes:
		settings.browseBusy = false
		if path == "" {
			return
		}
		settings.themeDir = path
		a.d.Prefs.SetTheme(settings.themeName, path)
		a.scanThemes()
		settings.statusLine = "Theme folder set: " + path
	default:
	}
}

// scanThemeDirs collects theme names across roots, "default" always first
// (the built-in fallback theme.Load uses even when no folder exists).
func scanThemeDirs(roots []string) []string {
	names := []string{theme.DefaultThemeName}
	seen := map[string]bool{theme.DefaultThemeName: true}
	for _, root := range roots {
		entries, err := os.ReadDir(filepath.Join(root, theme.ThemesDirName))
		if err != nil {
			continue // missing themes/ dir is normal
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			seen[e.Name()] = true
			names = append(names, e.Name())
		}
	}
	return names
}
