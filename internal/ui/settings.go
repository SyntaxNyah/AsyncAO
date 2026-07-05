package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/winexec"
)

// settingsState lives on App lazily (kept here for file cohesion).
type settingsState struct {
	mountInput string
	loaded     bool
	statusLine string
	// fpsBufs backs the three frame-rate rows' exact-number entries (active,
	// idle, unfocused) — reseeded from the pref while unfocused, applied on
	// Enter (fpsSettingRow).
	fpsBufs   [3]string
	tab       int                    // active settings tab (index into settingsTabNames)
	tabScroll [numSettingsTabs]int32 // per-tab page scroll (each tab remembers its position)
	search    string                 // settings search query (jumps to the matching tab)
	// scrollToSection holds a lowercased query for one frame after a search jump: the
	// first section card whose title contains it scrolls itself to the top, so search
	// lands on the SECTION (e.g. "scene maker"), not just the tab. Cleared each frame.
	scrollToSection string
	// #26 gather-search: the collected all-tabs row index (label + tab + the row's
	// offset within its tab at zero scroll), built once per search session by a
	// fenced, clipped-away draw of every tab. flash* highlight the landed row.
	index      []settingsIndexRow
	indexBuilt bool
	flashTab   int
	flashY     int32
	flashUntil time.Time

	// callword manager add-field buffer: a fresh empty field (NOT preloaded with
	// the word list — the words render as ×-removable rows below it).
	callAddInput string

	// friends edit buffer — reloaded when the server (friendKey) changes,
	// since the friend list is per server.
	friendInput string
	friendKey   string

	// ignore edit buffer — same per-server reload, so you can un-ignore a
	// player who's left (the player-row button is gone once they're offline).
	ignoreInput string
	ignoreKey   string

	// font override edit buffer (semicolon-separated chain).
	fontInput  string
	fontLoaded bool

	// custom window-size edit buffers (W, H), seeded from the live size and
	// re-seeded after a preset/fit so they track the window.
	winWInput, winHInput string
	winLoaded            bool

	// importArmed routes the next dropped .json into ImportSettings.
	importArmed bool
	// presetName is the "Save as preset" name buffer (setting presets).
	presetName string

	// macro editor buffers (name, captured key, |-separated lines).
	macroName  string
	macroKey   string
	macroLines string
	// IC quick-phrase editor buffer (the line to bind a key to).
	icPhrase string

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
const numSettingsTabs = 12

var settingsTabNames = [numSettingsTabs]string{
	"General", "Theme", "Assets", "Audio", "Chat", "Account", "Hotkeys", "Studio", "Data", "Voice", "Power user", "Reset",
}

// Tab indices (order matches settingsTabNames).
const (
	tabGeneral = iota
	tabTheme
	tabAssets
	tabAudio
	tabChat
	tabAccount
	tabHotkeys
	tabStudio
	tabData
	tabVoice
	tabPowerUser
	tabReset
)

// settingsSearchKeywords maps each tab to terms the search box matches. It folds
// in every SECTION TITLE on the tab (so "friends", "cache", "callwords", "window"
// etc. all resolve) plus the individual setting terms, since the old curated list
// missed most sections — the "search doesn't work that much" report (#102). Keep
// terms lowercase; when adding a settings section, add its title here.
var settingsSearchKeywords = [numSettingsTabs][]string{
	tabGeneral: {
		// sections: Identity, Display & behaviour, Application, Log colours, Stage,
		// Scale & text size, Window, Extras box, Fonts.
		"identity", "showname", "ooc name", "default showname", "force char names", "anti-impersonation",
		"your profile", "profile", "character profile", "pronouns", "bio", "tagline", "theme song", "card",
		"display", "behaviour", "behavior", "animation", "reduce motion", "accessibility",
		"sprite style", "recolour", "recolor", "tint", "glow", "opacity", "hide sprite styles", "hide other",
		"emote button", "favourite emotes", "favorite emotes", "fav emotes",
		"application", "streamer mode", "debug", "performance", "perf hud", "fps", "notify ooc", "unread badge",
		"log colours", "log colors", "selection highlight", "highlight colour", "name colour", "name colours",
		"stage", "desk", "hide desk", "scale", "text size", "ui scale", "dpi", "zoom",
		"window", "fullscreen", "window size", "resolution", "extras box", "extras", "tear off",
		"fonts", "font", "cjk", "dyslexia", "dyslexic", "emoji", "smooth scaling", "tabs", "server tabs", "max tabs",
	},
	tabTheme: {
		// sections: Theme, Layout & fit, Layout presets, Lobby, Preview & binding.
		"theme", "theme picker", "chatbox", "skin", "default theme",
		"layout", "fit", "courtroom design", "lobby", "preview", "bind", "binding",
		"layout presets", "preset", "presets", "save layout", "stage preset", "theater", "theatre",
	},
	tabAssets: {
		// sections: Server format profile, Predictive prefetch, Audio formats, Local
		// assets, Downloader, Cache. (Image formats moved to Power user.)
		"server format profile", "format profile", "profile",
		"predictive prefetch", "prefetch", "aggressiveness", "speculative", "preload",
		"audio format", "opus", "ogg", "mp3",
		"local assets", "local", "mount", "downloader", "download",
		"cache", "disk cache", "disk", "zstd", "learned formats", "learned", "clear cache",
	},
	tabAudio: {
		// sections: Volume (master / music / SFX / blip / alert), music ducking.
		"audio", "volume", "master volume", "music volume", "sfx volume", "blip volume", "blip",
		"alert volume", "music ducking", "duck",
	},
	tabChat: {
		// sections: Text & typing, Chat log, Case alerts, Callwords, Do Not Disturb,
		// Messages & connection, Sound effects, Music history, Friends, Ignored
		// players, Mod tools.
		"text", "typing", "text crawl", "text stay", "text speed", "chat limit", "catch up",
		"chat log", "ic log", "timestamps", "log", "song url", "song link", "full link", "music url",
		"case alerts", "casing", "case", "callword", "callwords", "ping", "alert",
		"do not disturb", "dnd", "messages", "connection", "auto reconnect", "reconnect", "disconnect confirm",
		"sound effects", "sfx", "mute sfx", "music history", "jukebox history",
		"friends", "friend", "nickname", "friend colour", "ignored players", "ignore", "block",
		"mod tools", "moderator", "modcall", "ipid",
	},
	tabAccount: {
		// sections: Login, Master list, Discord. (TLS / Asset Origin moved to Power user.)
		"login", "password", "username", "credential", "auto login",
		"master list", "server list", "discord", "presence", "rich presence",
	},
	tabHotkeys: {
		// sections: Hotkeys, Macros, IC quick-phrases.
		"hotkey", "hotkeys", "keybind", "keybinding", "shortcut",
		"macro", "macros", "ic quick", "quick phrase", "phrase",
	},
	tabData: {
		// sections: Your settings file, Back up / move, Other data.
		"data", "settings file", "config folder", "config location", "open config", "open settings file",
		"asset_preferences", "where are my settings", "appdata", "export", "import", "backup", "portable", "usb", "move to another pc", "json",
	},
	tabVoice: {
		// sections: Microphone, Test microphone, Output, Push-to-talk.
		"voice", "voice chat", "microphone", "mic", "input device", "recording device", "capture",
		"test microphone", "mic test", "sidetone", "hear myself", "level meter", "meter",
		"output volume", "speaker", "nyathena", "vc", "ptt", "talk", "push-to-talk", "push to talk",
	},
	tabStudio: {
		// sections: Scene recording, Instant replay, Scene maker, Recordings, Replay
		// playback, Export to GIF / WebP.
		"studio", "scene recording", "record", "recording", "instant replay", "clip", "rolling buffer",
		"scene maker", "maker", "aorec", "recordings", "replay", "replay playback", "playback speed",
		"export", "gif", "webp", "video", "mp4", "webm", "movie", "frame rate", "quality", "scene", "capture", "archive",
	},
	tabReset: {
		// sections: Reset / clear data.
		"reset", "factory reset", "reset to defaults", "restore defaults", "wipe", "fresh install",
		"clear settings", "clear data", "defaults",
	},
	tabPowerUser: {
		// sections: TLS, Asset Origin, character-folder casing, Image formats.
		"power user", "advanced", "expert",
		"tls", "ssl", "certificate", "cert", "validate certificate", "self-signed", "wss", "verify", "security",
		"origin", "cors", "referer", "asset origin", "origin header", "stream from base",
		"casing", "capital", "capitalize", "capitalise", "uppercase", "lowercase", "character folder", "folder case",
		"image format", "format", "fallback", "autodetect", "auto-detect", "extensions.json", "extensions", "webp", "png", "apng", "avif", "desk format",
	},
}

// settingsSearchMatch returns the first tab whose name or keywords contain the
// (lowercased, trimmed) query, or -1 for none/empty. Matching is forward only
// (the query is a substring of a setting term) — the comprehensive keyword list
// above is what makes the search actually find things; a reverse match would let
// a longer query like "keybind" wrongly hit the short "bind" (Theme preview).
func settingsSearchMatch(query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return -1
	}
	// A tab-name match wins (you typed the destination directly).
	for i, name := range settingsTabNames {
		if !voiceBuilt && i == tabVoice {
			continue // no Voice tab in the -tags novoice build
		}
		if strings.Contains(strings.ToLower(name), q) {
			return i
		}
	}
	for i, kws := range settingsSearchKeywords {
		if !voiceBuilt && i == tabVoice {
			continue
		}
		for _, kw := range kws {
			if strings.Contains(kw, q) {
				return i
			}
		}
	}
	return -1
}

// settingsIndexRow is one collected settings row for the gather-search (#26):
// its (trimmed) label, the tab it lives on, and its y offset within that tab's
// content at zero scroll — enough to list it and to jump straight to it.
type settingsIndexRow struct {
	label string
	tab   int
	y     int32
}

// settingsIndexCap bounds the collected index (hard rule §17.4) — ~600 rows is
// several times the real settings count; past it the collector just stops.
const settingsIndexCap = 600

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

	// --- header band: title, search, Back -----------------------------------
	c.Heading(pad, pad, "Settings", ColText)
	// Search (#26 gather): type a term and the page becomes a live list of EVERY
	// matching setting across every tab — click (or Enter for the top hit) jumps
	// straight to that row. The old jump-to-tab hint is superseded.
	q, committed := c.TextField("settsearch", sdl.Rect{X: pad + 130, Y: pad + 2, W: 240, H: fieldH}, settings.search, "Search settings…")
	settings.search = q
	if strings.TrimSpace(q) != "" {
		c.LabelClipped(pad+382, pad+6, w-pad-382-110, "showing every match — click one (or Enter for the top hit)", ColTextDim)
	}
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: pad, W: 90, H: btnH}, "Back") {
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		_ = a.d.Prefs.SaveNow() // Settings-Apply synchronous flush
		a.screen = a.prevScreen
		return
	}
	contentTop := pad + settContentTop
	c.Fill(sdl.Rect{X: 0, Y: contentTop - 10, W: w, H: 1}, ColPanelHi) // hairline under the header

	// --- left sidebar: a vertical category list (replaces the old chip row) --
	navY := contentTop + 4
	for i, name := range settingsTabNames {
		if !voiceBuilt && i == tabVoice {
			continue // voice chat is compiled out (-tags novoice): no Voice tab
		}
		r := sdl.Rect{X: pad, Y: navY, W: settSidebarW, H: settNavItemH}
		active := i == settings.tab
		switch {
		case active:
			c.Fill(r, ColPanelHi)
			c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: 3, H: r.H}, ColAccent) // selected accent rail
		case c.hovering(r):
			c.Fill(r, cardColor())
		}
		col := ColTextDim
		if active {
			col = ColAccent
		}
		c.LabelClipped(r.X+14, r.Y+8, r.W-20, name, col)
		if c.hovering(r) && c.clicked {
			settings.tab = i
		}
		navY += settNavItemH + 2
	}

	// --- content card region (right of the sidebar) -------------------------
	// formX/formW define where the section + row helpers draw; they rebase their
	// pad-relative layout onto formX so every box lands inside this card.
	cardX := pad + settSidebarW + settSidebarGap
	cardW := (w - scrollBarW - pad) - cardX
	if cardW > settMaxCardW {
		cardW = settMaxCardW
	}
	if cardW < settMinCardW {
		cardW = settMinCardW
	}
	a.formX = cardX + settCardPadX
	a.formW = cardW - 2*settCardPadX

	viewH := h - contentTop - pad
	scroll := &settings.tabScroll[settings.tab]

	// Clip everything below the header; fill the card surface (a step between the
	// page background and panels) so each section reads as a card, separated by
	// page-coloured gap bands punched in by settingsSection.
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: cardX, Y: contentTop, W: cardW, H: viewH})
	c.Fill(sdl.Rect{X: cardX, Y: contentTop, W: cardW, H: viewH}, cardColor())

	// #26 gather-search: with a query typed, the card region becomes a RESULTS
	// list of every matching setting across every tab (click → jump + flash).
	if q := strings.ToLower(strings.TrimSpace(settings.search)); q != "" {
		if !settings.indexBuilt {
			a.buildSettingsIndex(w, contentTop)
			settings.indexBuilt = true
		}
		a.drawSettingsSearchResults(q, committed, cardX, cardW, contentTop, viewH)
		c.popClip(clipPrev, clipHad)
		return
	}
	settings.indexBuilt = false // stale the index when the search closes (rows move as prefs change)

	y := contentTop - *scroll
	y = a.drawSettingsTabBody(settings.tab, y, w, h)
	// Landed-row flash: after a search jump, pulse a band around the row so the
	// eye finds it (the scroll already put it near the top).
	if settings.flashUntil.After(time.Now()) && settings.flashTab == settings.tab {
		fy := contentTop - *scroll + settings.flashY
		c.Border(sdl.Rect{X: cardX + 4, Y: fy - 5, W: cardW - 8, H: 30}, ColAccent)
		c.Border(sdl.Rect{X: cardX + 5, Y: fy - 4, W: cardW - 10, H: 28}, ColAccent)
	}
	if settings.statusLine != "" {
		c.Label(a.formX, y+6, settings.statusLine, ColAccent)
		y += 28
	}
	// Page-coloured fill below the last card so the surface base doesn't run on.
	if y < contentTop+viewH {
		fy := y
		if fy < contentTop {
			fy = contentTop
		}
		c.Fill(sdl.Rect{X: cardX, Y: fy, W: cardW, H: contentTop + viewH - fy}, ColBackground)
	}
	c.popClip(clipPrev, clipHad)

	contentH := (y + *scroll) - contentTop + pad
	if !c.ctrlHeld && !c.wheelTaken {
		*scroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: w - scrollBarW - 2, Y: contentTop, W: scrollBarW, H: viewH}
	*scroll = c.VScrollbar("settscroll", track, *scroll, contentH, viewH)
	settings.scrollToSection = "" // one-shot: consumed by the matching section this frame, else dropped
}

// drawSettingsTabBody dispatches one tab's rows starting at y — shared by the
// normal page draw and the search collector's fenced all-tabs pass (#26).
func (a *App) drawSettingsTabBody(tab int, y, w, h int32) int32 {
	switch tab {
	case tabGeneral:
		y = a.drawSettingsGeneral(y, w)
	case tabTheme:
		y = a.drawSettingsTheme(y, w, h)
	case tabAssets:
		y = a.drawSettingsAssets(y, w)
	case tabAudio:
		y = a.drawSettingsAudio(y, w)
	case tabChat:
		y = a.drawSettingsChat(y, w)
	case tabAccount:
		y = a.drawSettingsAccount(y, w)
	case tabHotkeys:
		y = a.drawSettingsHotkeys(y, w)
	case tabStudio:
		y = a.drawSettingsStudio(y, w)
	case tabData:
		y = a.drawSettingsData(y, w)
	case tabVoice:
		y = a.drawSettingsVoice(y, w)
	case tabPowerUser:
		y = a.drawSettingsPowerUser(y, w)
	case tabReset:
		y = a.drawSettingsReset(y, w)
	}
	return y
}

// buildSettingsIndex runs every tab's draw once with the input FENCED and all
// drawing CLIPPED AWAY, purely to collect each row's label/tab/offset through
// the Ctx.onRow hook (checkboxes; sections + slider rows record directly).
// A few milliseconds, once per search session — never on a normal frame.
func (a *App) buildSettingsIndex(w, contentTop int32) {
	c := a.ctx
	settings.index = settings.index[:0]
	savedClicked, savedRight, savedDown := c.clicked, c.rightClicked, c.mouseDown
	savedKey, savedWheel, savedModal := c.keyPressed, c.wheelY, c.modalOn
	c.clicked, c.rightClicked, c.mouseDown, c.keyPressed, c.wheelY, c.modalOn = false, false, false, 0, 0, true
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: -1 << 14, Y: -1 << 14, W: 1, H: 1}) // off-screen: nothing lands
	curTab := 0
	c.onRow = func(label string, y int32) {
		label = strings.TrimSpace(label)
		if label == "" || len(settings.index) >= settingsIndexCap {
			return
		}
		settings.index = append(settings.index, settingsIndexRow{label: label, tab: curTab, y: y - contentTop})
	}
	for t := 0; t < numSettingsTabs; t++ {
		if (!voiceBuilt && t == tabVoice) || t == tabReset {
			continue // no Voice tab in lean builds; the Reset tab is all danger buttons — don't index it
		}
		curTab = t
		a.drawSettingsTabBody(t, contentTop, w, contentTop) // h only matters for the Theme grid's own scroll — harmless here
	}
	c.onRow = nil
	c.popClip(clipPrev, clipHad)
	c.clicked, c.rightClicked, c.mouseDown = savedClicked, savedRight, savedDown
	c.keyPressed, c.wheelY, c.modalOn = savedKey, savedWheel, savedModal
}

// drawSettingsSearchResults lists every collected row matching q (substring,
// case-blind) in the card region; clicking one — or Enter for the first —
// switches to its tab, scrolls the row to the top, and arms the flash band.
func (a *App) drawSettingsSearchResults(q string, enter bool, cardX, cardW, contentTop, viewH int32) {
	c := a.ctx
	const rowH = int32(26)
	y := contentTop + 8
	shown, total := 0, 0
	maxRows := int((viewH - 40) / rowH)
	jump := func(m settingsIndexRow) {
		settings.tab = m.tab
		sc := m.y - 8
		if sc < 0 {
			sc = 0
		}
		settings.tabScroll[m.tab] = sc // the page scrollbar clamps overshoot next frame
		settings.flashTab, settings.flashY, settings.flashUntil = m.tab, m.y, time.Now().Add(2*time.Second)
		settings.search = ""
		settings.indexBuilt = false
	}
	for _, m := range settings.index {
		if !strings.Contains(strings.ToLower(m.label), q) {
			continue
		}
		total++
		if shown >= maxRows {
			continue // keep counting for the "+N more" line
		}
		if enter && shown == 0 {
			jump(m)
			return
		}
		r := sdl.Rect{X: cardX + 6, Y: y, W: cardW - 12, H: rowH - 2}
		if c.hovering(r) {
			c.Fill(r, ColPanelHi)
			c.Border(r, ColAccent)
		}
		tabName := "[" + settingsTabNames[m.tab] + "]"
		tw := c.TextWidth(tabName)
		c.Label(r.X+6, r.Y+4, tabName, ColAccent)
		c.LabelClipped(r.X+6+tw+10, r.Y+4, r.W-tw-24, m.label, ColText)
		if c.clicked && c.hovering(r) {
			jump(m)
			c.clicked = false
			return
		}
		y += rowH
		shown++
	}
	switch {
	case total == 0:
		// Keyword fallback: no row label matched, but the per-tab keyword table
		// (settingsSearchMatch) may still know the CONCEPT ("volume" → Audio).
		if mt := settingsSearchMatch(q); mt >= 0 {
			r := sdl.Rect{X: cardX + 6, Y: y, W: cardW - 12, H: rowH - 2}
			if c.hovering(r) {
				c.Fill(r, ColPanelHi)
				c.Border(r, ColAccent)
			}
			c.LabelClipped(r.X+6, r.Y+4, r.W-12, "→ the "+settingsTabNames[mt]+" tab covers this — open it", ColAccent)
			if enter || (c.clicked && c.hovering(r)) {
				settings.tab = mt
				settings.search = ""
				settings.indexBuilt = false
				c.clicked = false
			}
			return
		}
		c.LabelClipped(cardX+8, y+4, cardW-16, "No settings match — try a shorter word (matches are by label text).", ColTextDim)
	case total > shown:
		c.LabelClipped(cardX+8, y+4, cardW-16, fmt.Sprintf("+ %d more — narrow the search to see the rest.", total-shown), ColTextDim)
	}
}

// --- modernized settings layout: sidebar nav + content cards -----------------

const (
	settContentTop = 44   // header band height (title + search row)
	settSidebarW   = 140  // left category-nav width (slim: the labels are short)
	settSidebarGap = 12   // gap between the sidebar and the content card
	settNavItemH   = 32   // height of one sidebar nav row
	settCardPadX   = 14   // content card horizontal padding (row inset)
	settMaxCardW   = 1400 // cap the card width on ultrawide; high enough that the
	// widest multi-column rows (offsets up to ~pad+700) never clip on normal monitors.
	settMinCardW   = 320 // floor so rows never collapse on a narrow window
	settCardGap    = 14  // page-coloured gap band separating cards
	settCardTopPad = 14  // padding above a card title
	settSectionMid = 22  // title baseline → hairline
	settSectionBot = 12  // hairline → the section's first row
)

// settingsSection delimits one card: it punches a page-coloured gap above the
// card (separating it from the previous one over the cardColor surface base),
// then draws the card's uppercase accent title and a hairline, returning the y
// of the card's first row. Pure draw (no widget ids), so it never disturbs
// hit-testing, per-tab scroll, or search. The w param is unused (the region is
// taken from a.formX/a.formW) but kept so existing call sites need no change.
func (a *App) settingsSection(y, w int32, title string) int32 {
	c := a.ctx
	if c.onRow != nil {
		c.onRow("§ "+title, y+settCardTopPad+2) // gather-search: sections are jumpable headers
	}
	// Search jump (#26): the FIRST section card whose title contains the pending query
	// scrolls itself to the top, so a search lands on the SECTION (e.g. "scene maker"),
	// not just the tab. One-shot; takes effect next frame and is clamped by the page
	// scrollbar (so a bottom section just sits as low as it can).
	if settings.scrollToSection != "" && strings.Contains(strings.ToLower(title), settings.scrollToSection) {
		settings.tabScroll[settings.tab] += y - (pad + settContentTop)
		if settings.tabScroll[settings.tab] < 0 {
			settings.tabScroll[settings.tab] = 0
		}
		settings.scrollToSection = ""
	}
	cardX := a.formX - settCardPadX
	cardW := a.formW + 2*settCardPadX
	c.Fill(sdl.Rect{X: cardX, Y: y, W: cardW, H: settCardGap}, ColBackground)
	y += settCardGap + settCardTopPad
	c.Label(a.formX, y, strings.ToUpper(title), ColAccent)
	y += settSectionMid
	c.Fill(sdl.Rect{X: a.formX, Y: y, W: a.formW, H: 1}, ColPanelHi)
	y += settSectionBot
	return y
}

// formW2 returns the shadow value for a section/helper's `w` parameter so its
// existing `w - pad - K - scrollBarW` width math and `w - scrollBarW` right edge
// resolve inside the content card. Paired with `pad := a.formX` at the top of
// every settings draw helper. (See drawSettingsGeneral for the pattern.)
func (a *App) formW2() int32 { return a.formX + a.formW + scrollBarW }

// fpsSettingRow draws one frame-rate row: the slider, an exact-number entry
// (Enter applies), and the ∞ no-limit toggle. get/set wrap the pref — the
// setters are sentinel-aware (config.FPSUnlimited / config.FPSOff) and clamp
// typed values to [min, max]. zeroValue is what the slider bottom or a typed 0
// stores: config.FPSOff for the Idle/Background rows (0 = never redraw in that
// state), or the min cap for Active (which has no "off"). buf backs the number
// field (settingsState), reseeded from the pref while unfocused so live typing
// never fights the stored value. Returns the y past the row.
func (a *App) fpsSettingRow(y int32, id, label string, min, max, def, zeroValue int, get func() int, set func(int), buf *string, tip string) int32 {
	c := a.ctx
	pad := a.formX // settings helpers must shadow the form origin (drawSettings rebase)
	c.Label(pad, y+4, label, ColText)
	cur := get()
	// Slider: compare against the value we PASSED (unlimited shows as the
	// bottom position), so merely displaying an unlimited pref can't write a
	// concrete value back — only a real drag/wheel move does.
	passed := 0
	if cur > 0 {
		passed = cur
	}
	track := sdl.Rect{X: pad + 210, Y: y + 2, W: 170, H: 16}
	raw := int(clampI32(c.Slider(id, track, int32(passed), int32(max)), 0, int32(max)))
	nv := raw
	if raw == 0 {
		nv = zeroValue // bottom of the track: off (Idle/Background) or the min cap (Active)
	}
	c.Tooltip(track, tip)
	if raw != passed && nv != cur {
		set(nv)
		cur = get()
	}
	// Exact-number entry (the power-user ask): type any value, Enter applies.
	fieldID := id + "-num"
	fr := sdl.Rect{X: track.X + track.W + 8, Y: y, W: 52, H: btnH}
	// The getter maps 0 to the shipped default, so cur is FPSUnlimited, FPSOff,
	// or a concrete rate here. "off" shows as 0 in the field (the sentinel is
	// internal); ∞ shows blank.
	if c.focusID != fieldID {
		switch cur {
		case config.FPSUnlimited:
			*buf = ""
		case config.FPSOff:
			*buf = "0"
		default:
			*buf = strconv.Itoa(cur)
		}
	}
	var entered bool
	*buf, entered = c.TextField(fieldID, fr, *buf, "fps")
	if entered {
		if n, err := strconv.Atoi(strings.TrimSpace(*buf)); err == nil {
			if n <= 0 {
				set(zeroValue) // typed 0 (or blank/negative) = the row's zero action
			} else {
				set(n) // the setter clamps to [min, max]
			}
			cur = get()
		}
		c.focusID = "" // commit releases the field so it reseeds from the pref
	}
	// ∞ toggle: no limit on / off (off returns to the default).
	ub := sdl.Rect{X: fr.X + fr.W + 6, Y: y, W: 28, H: btnH}
	if cur == config.FPSUnlimited {
		c.Fill(sdl.Rect{X: ub.X - 2, Y: ub.Y - 2, W: ub.W + 4, H: ub.H + 4}, ColAccent)
	}
	if c.Button(ub, "∞") {
		if cur == config.FPSUnlimited {
			set(def) // unlimited off → the row's default
		} else {
			set(config.FPSUnlimited)
		}
		cur = get()
	}
	c.Tooltip(ub, "No limit. Active: uncapped (vsync paces the frames). Idle/Background: render as fast as the active cap in that state.")
	var lbl string
	switch {
	case cur == config.FPSUnlimited:
		lbl = "uncapped"
	case cur == config.FPSOff:
		lbl = "off"
	case cur == def:
		lbl = "default (" + strconv.Itoa(def) + " fps)"
	default:
		lbl = strconv.Itoa(cur) + " fps"
	}
	c.Label(ub.X+ub.W+10, y+4, lbl, ColTextDim)
	return y + 26
}

// settingsDesc draws a WORD-WRAPPED description block at (pad, y) inside the settings card and
// returns the y past it. Plain c.Label doesn't wrap, so long help text ran off the card; route it
// through here (wraps to the card content width, 16 px per line — matching the hand-rolled blocks).
func (a *App) settingsDesc(pad, y int32, text string, col sdl.Color) int32 {
	for _, ln := range a.ctx.WrapText(text, a.formW-8, 0) {
		a.ctx.Label(pad, y, ln, col)
		y += 16
	}
	return y
}

// drawSettingsGeneral: identity + display toggles + UI scale + font chain.
func (a *App) drawSettingsGeneral(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX // rebase every pad-relative box into the content card
	w := a.formW2()
	y = a.settingsSection(y, w, "Identity")
	// Showname: write-through to prefs. A stale once-per-session copy here
	// used to overwrite names typed in the courtroom on Back.
	c.Label(pad, y+4, "Showname:", ColText)
	shown := a.d.Prefs.SavedShowname()
	if next, _ := c.TextField("showname", sdl.Rect{X: pad + 130, Y: y, W: 240, H: fieldH}, shown, "your saved showname"); next != shown {
		a.d.Prefs.SetShowname(next)
	}
	// Default OOC name: write-through to prefs like the showname; it seeds
	// every NEW tab (blank sends a sticky AsyncAO<n>). The courtroom OOC-name
	// fields are tab-local and never write this back.
	c.Label(pad+390, y+4, "OOC name:", ColText)
	savedOOC := a.d.Prefs.SavedOOCName()
	if next, _ := c.TextField("oocdefault", sdl.Rect{X: pad + 480, Y: y, W: 200, H: fieldH}, savedOOC, "blank = AsyncAO<n>"); next != savedOOC {
		a.d.Prefs.SetOOCName(next)
		a.oocName = next // the ACTIVE tab adopts the new default right away
	}
	y += 38

	// Showname presets (M6): a saved, global list — quick-swap them in-game with
	// keybinds (random or a specific one). Cleared only by a factory reset.
	c.Label(pad, y+4, "Presets:", ColText)
	var addNow bool
	a.shownameAdd, addNow = c.TextField("shownameadd", sdl.Rect{X: pad + 130, Y: y, W: 240, H: fieldH}, a.shownameAdd, "type a name to save…")
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

	y = a.settingsSection(y, w, "Your profile")
	y = a.drawProfileSettings(y, w)
	y += 8

	y = a.settingsSection(y, w, "Auto-status")
	y = a.drawAutoStatusSettings(y, w)
	y += 8

	y = a.settingsSection(y, w, "AsyncAO appearance")
	y = a.drawChromeSettings(y, w)
	y = a.drawPartColorSettings(y) // per-part layout tints (v1.52.0, Tifera)
	y += 8

	y = a.settingsSection(y, w, "Display & behaviour")
	// Streamer mode leads the section (playtest: it's the one people need to
	// find FAST, right before going live).
	streamer := a.d.Prefs.StreamerMode()
	if next := c.Checkbox(pad, y, "Streamer mode (masks OOC names + IPs in the log display, silences callword pings)", streamer); next != streamer {
		a.d.Prefs.SetStreamerMode(next)
	}
	y += 26
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
	// #103: viewer opt-out of OTHER players' transmitted sprite styles (your own
	// is set in the Extras → Sprite Style picker). Reduce-motion already drops a
	// received style's wobble/spin; this hides the whole thing.
	hideStyles := a.d.Prefs.HideSpriteStylesOn()
	if next := c.Checkbox(pad, y, "Hide other players' sprite styles: show every character normally (ignore transmitted recolour / glow)", hideStyles); next != hideStyles {
		a.d.Prefs.SetHideSpriteStyles(next)
		if a.room != nil {
			a.room.HideSpriteStyles = next
		}
	}
	y += 26
	// #2: viewer opt-out of OTHER players' emoji reactions (the floating emoji over the
	// stage). Sending your own reactions is unaffected — this only hides incoming floats.
	hideReact := a.d.Prefs.HideReactionsOn()
	if next := c.Checkbox(pad, y, "Hide other players' emoji reactions: don't float their reaction emoji over the stage (#2)", hideReact); next != hideReact {
		a.d.Prefs.SetHideReactions(next)
	}
	y += 26
	emoteImgs := a.d.Prefs.EmoteButtonImagesEnabled()
	if next := c.Checkbox(pad, y, "Image emote buttons (characters/<char>/emotions/button art — WebP by default, formats in Assets)", emoteImgs); next != emoteImgs {
		a.d.Prefs.SetEmoteButtonImages(next)
	}
	y += 26
	emoteCaps := a.d.Prefs.EmoteCaptionsOn()
	if next := c.Checkbox(pad, y, "Emote-name captions (OFF by default): when a character has no button art, overlay the emote name on the fallback icon. Off keeps the buttons clean (just icons); on helps tell identical fallback icons apart.", emoteCaps); next != emoteCaps {
		a.d.Prefs.SetEmoteCaptions(next)
	}
	y += 26
	favStars := a.d.Prefs.EmoteFavStarsOn()
	if next := c.Checkbox(pad, y, "Emote favourites (OFF by default): show a ★ on every emote button so you can star the ones you use, plus the ★ Favs filter. Off keeps the grid clean if you don't use it.", favStars); next != favStars {
		a.d.Prefs.SetEmoteFavStars(next)
		a.emoteFavRev++ // rebuild the view for the new state
	}
	y += 26
	favOnly := a.d.Prefs.EmoteFavOnlyOn()
	if next := c.Checkbox(pad, y, "Show favourite emotes only (OFF by default): hides everything but the emotes you've starred. Click the ★ on an emote button to favourite it (per character). The classic grid also has a ★ Favs button.", favOnly); next != favOnly {
		a.d.Prefs.SetEmoteFavOnly(next)
		a.emoteFavRev++ // rebuild the visible list for the new filter state
	}
	y += 26
	favBox := a.d.Prefs.FavEmoteBoxOn()
	if next := c.Checkbox(pad, y, "Favourite-emotes box (OFF by default): a small movable box of just your starred emotes as clickable buttons — press one to use that emote. Also opens with Ctrl+A (rebindable in the Controls tab).", favBox); next != favBox {
		a.d.Prefs.SetFavEmoteBox(next)
	}
	y += 30
	dbg := a.d.Prefs.DebugOverlayEnabled()
	if next := c.Checkbox(pad, y, "Debug overlay (live log of failures: missing assets, theme problems, unhandled server packets)", dbg); next != dbg {
		a.d.Prefs.SetDebugOverlay(next)
	}
	y += 26
	notifOOC := a.d.Prefs.NotifyOnOOCOn()
	if next := c.Checkbox(pad, y, "Count OOC in the unread tab badge (OFF by default): otherwise only IC chat lights up a background tab's \"(N)\" — so server auto-messages (hourly reminders, etc.) don't ping you", notifOOC); next != notifOOC {
		a.d.Prefs.SetNotifyOnOOC(next)
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
	// Sprite hover-previews: rest the cursor on a character/emote button to pop a
	// full-size preview. ON by default; the dwell before it shows is tunable.
	// Gates ONLY the hover dwell — right-click previews always work.
	prev := a.d.Prefs.SpritePreviewsOn()
	if next := c.Checkbox(pad, y, "Sprite hover-previews (ON by default): hovering a character or emote button shows the full-size sprite. Off only disables the hover pop-up — right-click still previews", prev); next != prev {
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
	// Preview box default height: the pop-up's size before any corner-drag
	// (playtest: the old 192 px default read tiny and re-dragging it every
	// session got old). Applies to hover AND right-click previews, so it
	// shows regardless of the toggle above.
	ph := a.d.Prefs.PreviewHeightPx()
	c.Label(pad, y+4, "Preview box height:", ColText)
	phTrack := sdl.Rect{X: pad + 170, Y: y + 5, W: 120, H: 16}
	nph := config.MinPreviewHeightPx + int(c.Slider("previewheight", phTrack,
		int32(ph-config.MinPreviewHeightPx), int32(config.MaxPreviewHeightPx-config.MinPreviewHeightPx)))
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 300, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		nph += int(c.wheelY) * 16
	}
	if nph < config.MinPreviewHeightPx {
		nph = config.MinPreviewHeightPx
	}
	if nph > config.MaxPreviewHeightPx {
		nph = config.MaxPreviewHeightPx
	}
	c.Label(pad+298, y+4, strconv.Itoa(nph)+" px", ColTextDim)
	c.Label(pad+360, y+4, "default size of the sprite preview pop-up (384 px default; the corner grip still resizes per session)", ColTextDim)
	if nph != ph {
		a.d.Prefs.SetPreviewHeightPx(nph)
	}
	y += 30
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
	// Hide-sprite ("Missingno"): right-click a sprite → confirm → hidden for the
	// session (for players who'd rather not see certain art). ON by default.
	hideRC := a.d.Prefs.RightClickHideSpriteOn()
	if next := c.Checkbox(pad, y, "Right-click a sprite to hide it from the viewport (ON by default): confirms, then hides it for the session. Reshow all with the Reshow-hidden-sprites key (Controls tab).", hideRC); next != hideRC {
		a.d.Prefs.SetRightClickHideSprite(next)
	}
	y += 26
	if len(a.hiddenSprites) > 0 {
		if c.Button(sdl.Rect{X: pad + 20, Y: y, W: 200, H: btnH}, "Reshow hidden sprites") {
			a.reshowSprites()
		}
		c.Label(pad+232, y+5, fmt.Sprintf("%d sprite(s) hidden this session — un-hide them all", len(a.hiddenSprites)), ColTextDim)
		y += 32
	}
	// Hide the desk (the foreground table the character stands behind).
	hideDesk := a.d.Prefs.HideDeskOn()
	if next := c.Checkbox(pad, y, "Hide the courtroom desk (OFF by default): suppresses the foreground desk so the full character shows. Toggle live with the Hide/show-desk key (Controls tab).", hideDesk); next != hideDesk {
		a.d.Prefs.SetHideDesk(next)
	}
	y += 30
	y = a.settingsSection(y, w, "Application")
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
	if next := a.numberRow(y, "Max server tabs", tabCap, 1, 1, 99,
		"How many servers you can have open at once — each Join opens a tab (up to 99)."); next != tabCap {
		a.d.Prefs.SetTabCap(next)
	}
	c.Label(pad+270, y+4, "servers you can keep open at once — each is a live connection (default 6)", ColTextDim)
	y += 30
	restoreTabs := a.d.Prefs.RestoreTabsOn()
	if next := c.Checkbox(pad, y, "Reopen my server tabs on launch (OFF by default): remembers open servers on exit and reconnects them next time", restoreTabs); next != restoreTabs {
		a.d.Prefs.SetRestoreTabs(next)
	}
	y += 30
	y = a.settingsSection(y, w, "Log colours")
	// Log-selection highlight colour: a hue/saturation wheel + brightness
	// slider + hex field (drag-select in IC/OOC shows it).
	y = a.drawHighlightPicker(y, w)
	// Per-speaker name colours: tint each speaker's name by a stable hash, with
	// saturation/brightness sliders + a live preview. OFF by default.
	y = a.drawNameColorPicker(y, w)
	boldNames := a.d.Prefs.BoldNamesOn()
	if next := c.Checkbox(pad, y, "Bold speaker names + timestamps (ON by default): renders the name prefix (and the IC-log time stamp) in the log and chatbox in bold for readability.", boldNames); next != boldNames {
		a.d.Prefs.SetBoldNames(next)
	}
	y += 28
	y = a.settingsSection(y, w, "Stage")
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
	dragLay := a.d.Prefs.DragLayoutOn()
	if next := c.Checkbox(pad, y, "Drag panel edges to resize the courtroom (ON by default): grab the viewport's right edge to make it bigger / the log smaller — uncheck for the +/− View/Text/MsgBox/Log/Input knob buttons instead", dragLay); next != dragLay {
		a.d.Prefs.SetDragLayout(next)
	}
	y += 26
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
	y = a.settingsSection(y, w, "Scale & text size")
	// Global scale: DPI-driven by default, manual spinbox when auto is off.
	scaleAuto := a.d.Prefs.UIScaleAuto()
	scaleAutoLabel := "Auto UI scale (fits the window + display DPI)"
	if a.detectedScalePct > 0 {
		scaleAutoLabel = fmt.Sprintf("Auto UI scale — fits the window + display DPI (now: %d%%)", a.detectedScalePct)
	}
	if next := c.Checkbox(pad, y, scaleAutoLabel, scaleAuto); next != scaleAuto {
		a.d.Prefs.SetUIScaleAuto(next)
		a.ctx.SetUIScale(a.UIScale())
	}
	y += 26
	if scaleAuto {
		c.Label(pad, y+4, fmt.Sprintf("UI scale %%:  %d (auto)", a.UIScale()), ColTextDim)
		y += 34
	} else {
		y = a.drawManualUIScale(y)
	}
	y = a.drawViewportSizeRow(y)

	// Independent text size: scale the IC/OOC log + chatbox message text WITHOUT
	// zooming the courtroom art (that's the UI scale above — it's a whole-frame
	// zoom). These map to the same persisted layout scales the in-courtroom
	// Ctrl+wheel zoom tunes, so a change here shows at once and survives restart.
	if v := a.sliderRow(y, "Chat log text size %", a.logPct, config.ScaleStepPercent, config.MinLogScalePercent, config.MaxLogScalePercent,
		"Size of the IC/OOC log text only — not the courtroom art. Ctrl+wheel over the log does the same."); v != a.logPct {
		a.logPct = v
		a.saveLayout()
	}
	y += 30
	if v := a.sliderRow(y, "OOC text size %", a.oocPct, config.ScaleStepPercent, config.MinLogScalePercent, config.MaxLogScalePercent,
		"Size of the OOC log text. Ctrl+wheel over the OOC area does the same."); v != a.oocPct {
		a.oocPct = v
		a.saveLayout()
	}
	y += 30
	if v := a.sliderRow(y, "Chatbox text size %", a.chatPct, config.ScaleStepPercent, config.MinChatScalePercent, config.MaxChatScalePercent,
		"Size of the message text in the chatbox (the line over the sprite) — not the log."); v != a.chatPct {
		a.chatPct = v
		a.saveLayout()
	}
	y += 30
	// See-through chatbox: panel opacity (0 = fully transparent, 100 = solid).
	// Only affects the flat fallback skin; a theme chatbox keeps its own art.
	op := a.d.Prefs.ChatboxOpacityPct()
	if v := a.sliderRow(y, "Chatbox opacity %", op, 5, config.MinChatboxOpacity, config.MaxChatboxOpacity,
		"How see-through the chatbox panel is: 0 = text only, 100 = solid. Only the flat fallback panel — a theme's own skin keeps its art."); v != op {
		a.d.Prefs.SetChatboxOpacity(v)
	}
	y += 34
	tint := a.d.Prefs.ChatboxTintOn()
	if next := c.Checkbox(pad, y, "Per-character chatbox tint (OFF by default): the chatbox takes a hint of each speaker's colour", tint); next != tint {
		a.d.Prefs.SetChatboxTint(next)
	}
	y += 26

	y = a.settingsSection(y, w, "Window")
	// Window size / fullscreen: pick your own client dimensions (a window bigger
	// than the monitor can't be dragged smaller; F11 + Fit to screen are the
	// escapes). All window ops run here on the render thread.
	full := a.d.Prefs.WindowFullscreen()
	if next := c.Checkbox(pad, y, "Fullscreen (borderless) · F11 toggles", full); next != full {
		a.applyFullscreen(next)
	}
	y += 28
	if a.d.Prefs.WindowFullscreen() {
		c.LabelClipped(pad, y+4, a.formW, "Press F11 or untick Fullscreen to return to a window.", ColTextDim)
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

	y = a.settingsSection(y, w, "Extras box")
	// Extras box appearance: a hex colour per element (blank = the stock colour),
	// a live swatch, and a Background → Gradient↓ fade. Applies to the floating
	// Extras box and its torn-off boxes; default (all blank) is byte-identical.
	c.Label(pad, y+4, "Colours:", ColTextDim)
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

	y = a.settingsSection(y, w, "Fonts")
	// Dyslexia-friendly font: a persisted one-click toggle backed by the bundled
	// OpenDyslexic (no install needed). Drives the IC/OOC chat + log text and
	// takes precedence over the manual override below.
	dys := a.d.Prefs.DyslexiaFontOn()
	if next := c.Checkbox(pad, y, "Dyslexia-friendly font (bundled OpenDyslexic) — applies to the chat & log text", dys); next != dys {
		a.d.Prefs.SetDyslexiaFont(next)
		a.applyFontConfig()
		if next {
			settings.statusLine = "Dyslexia-friendly font on (OpenDyslexic)."
		} else {
			settings.statusLine = "Dyslexia-friendly font off."
		}
		dys = next
	}
	y += 28

	// Custom font override: a chain of TTF/TTC paths, first covering font per
	// line wins (put a CJK-capable font later in the chain). Drives the IC/OOC
	// chat + log text; the "everywhere" checkbox below extends it to the whole
	// UI. Saved even while the dyslexia font is on (which overrides it), so it
	// returns when you switch back.
	c.Label(pad, y+4, "Custom font:", ColText)
	if !settings.fontLoaded {
		settings.fontInput = a.d.Prefs.FontPaths()
		settings.fontLoaded = true
	}
	var fontCommit bool
	settings.fontInput, fontCommit = c.TextField("fontpaths", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH},
		settings.fontInput, `C:\Windows\Fonts\meiryo.ttc; more fallbacks... (blank = built-in)`)
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Apply") || fontCommit {
		raw := strings.TrimSpace(settings.fontInput)
		a.d.Prefs.SetFontPaths(raw)
		a.applyFontConfig()
		switch {
		case dys:
			settings.statusLine = "Saved — turn off the dyslexia font to use this custom font."
		case raw == "":
			settings.statusLine = "Font override cleared — built-in font."
		}
	}
	if dys {
		c.LabelClipped(pad+620, y+4, w-pad-620-scrollBarW, "(dyslexia font active — overrides this)", ColTextDim)
	} else if names := a.ctx.FontChainNames(); len(names) > 0 {
		c.LabelClipped(pad+620, y+4, w-pad-620-scrollBarW, "chain: "+strings.Join(names, " → "), ColTextDim)
	}
	y += 34

	// Font-everywhere: extend the ACTIVE override (dyslexia toggle or the chain
	// above) to all chrome text too. Opt-in — the chrome's fixed row/button
	// metrics are tuned for the embedded face, so an unusual font can sit tight
	// in buttons; chat/log stay the reading-optimized surface either way.
	few := a.d.Prefs.FontEverywhereOn()
	if next := c.Checkbox(pad, y, "Use the font everywhere — menus, buttons, lists & tabs too (not just chat)", few); next != few {
		a.d.Prefs.SetFontEverywhere(next)
		a.applyFontConfig()
		switch {
		case !next:
			settings.statusLine = "Whole-UI font off — chrome back on the built-in font."
		case dys:
			settings.statusLine = "OpenDyslexic now drives the whole UI."
		case strings.TrimSpace(a.d.Prefs.FontPaths()) != "":
			settings.statusLine = "Custom font now drives the whole UI."
		default:
			settings.statusLine = "Whole-UI font armed — set a font above (or the dyslexia toggle) to see it."
		}
	}
	y += 28
	return y
}

// drawSettingsStudio: the "Studio" tab — scene recording, the recordings
// library/replay picker, and (soon) GIF/video export. See replay.go.
func (a *App) drawSettingsStudio(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Scene recording")
	c.Label(pad, y, "Record the courtroom to a tiny .aorec replay file — it stores the scene EVENTS (who spoke,", ColTextDim)
	y += 18
	c.Label(pad, y, "emote, text, timing), not video — so it's near-free and replays at perfect quality. All off by default.", ColTextDim)
	y += 18
	c.Label(pad, y, "Record: Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyRecordScene))+"   ·   Replay last: Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyReplayLast))+"   (rebind in Controls). Files save under recordings\\.", ColTextDim)
	y += 26
	srb := a.d.Prefs.ShowRecordButtonOn()
	if next := c.Checkbox(pad, y, "Show a small ● Record button on the courtroom stage (OFF by default)", srb); next != srb {
		a.d.Prefs.SetShowRecordButton(next)
	}
	y += 32

	y = a.settingsSection(y, w, "Instant replay (clip what just happened)")
	c.Label(pad, y, "Optionally keep a rolling buffer of the recent conversation, so the clip key can save the last minute or", ColTextDim)
	y += 18
	c.Label(pad, y, "two as a .aorec — WITHOUT starting a recording first. Off by default; nothing is kept until you tick it.", ColTextDim)
	y += 24
	ir := a.d.Prefs.InstantReplayOn()
	if next := c.Checkbox(pad, y, "Pre-record recent conversation (Instant Replay)", ir); next != ir {
		a.d.Prefs.SetInstantReplay(next)
	}
	y += 30
	if ir {
		secs := a.d.Prefs.InstantReplaySecondsValue()
		c.Label(pad+16, y+4, "Capture window:", ColTextDim)
		track := sdl.Rect{X: pad + 140, Y: y + 5, W: 240, H: 16}
		lo, hi := config.InstantReplayMinSeconds, config.InstantReplayMaxSeconds
		if nv := int(c.Slider("ir_window", track, int32(secs-lo), int32(hi-lo))) + lo; nv != secs {
			a.d.Prefs.SetInstantReplaySeconds(nv)
			secs = nv
		}
		c.Label(track.X+track.W+10, y+4, formatReplayWindow(time.Duration(secs)*time.Second)+"  (10s … 1 hour)", ColAccent)
		y += 26
		c.Label(pad+16, y, "Clip the last window: Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyClipReplay))+"  (rebind in Controls). Saves to recordings\\ — open it in the Scene Maker to trim/export.", ColTextDim)
		y += 22
	}
	y += 6

	y = a.settingsSection(y, w, "Scene maker")
	c.Label(pad, y, "Build a scene from scratch — or edit a recording — line by line: pick the character, emote, text,", ColTextDim)
	y += 18
	c.Label(pad, y, "background and music, set the Origin/CDN the assets load from, then Preview and Save a .aorec.", ColTextDim)
	y += 18
	c.Label(pad, y, ".aorec files are plain text (JSON) — you can also open one in any text editor to tweak it by hand.", ColTextDim)
	y += 18
	c.Label(pad, y, "AO2 .demo files work here too: drop them in recordings\\ to Play / Edit / export them, and the", ColTextDim)
	y += 18
	c.Label(pad, y, "Scene Maker's ⇄ .demo button writes scenes AO2's own demo player can watch.", ColTextDim)
	y += 26
	if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "🎬 New scene") {
		a.newScene()
	}
	c.Label(pad+162, y+5, "opens the in-app Scene Maker (works offline — no server needed)", ColTextDim)
	y += 36

	y = a.settingsSection(y, w, "Recordings")
	if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "📁 Open folder") {
		a.openRecordingsFolder()
	}
	c.Label(pad+162, y+5, "the default recordings\\ folder (next to AsyncAO) where .aorec files are saved", ColTextDim)
	y += 32
	// listRecordings does a dir read, but this is the Settings menu (never the
	// courtroom render path), so it stays fresh with no caching.
	recs := listRecordings()
	if len(recs) == 0 {
		c.Label(pad, y+4, "No recordings yet — press the Record key (or the on-stage button) during a scene.", ColTextDim)
		y += 26
	} else {
		c.Label(pad, y+4, "Newest first — Play watches it back (it plays over the screen; ■ Stop to end):", ColText)
		y += 26
		const maxShow = 12
		for i, r := range recs {
			if i >= maxShow {
				c.Label(pad+16, y+4, "… and "+strconv.Itoa(len(recs)-maxShow)+" more in the recordings\\ folder.", ColTextDim)
				y += 24
				break
			}
			if c.Button(sdl.Rect{X: pad + 16, Y: y, W: 64, H: btnH}, "▶ Play") {
				a.replayFromPath(r.path)
			}
			if c.Button(sdl.Rect{X: pad + 84, Y: y, W: 58, H: btnH}, "✎ Edit") {
				a.editRecordingInMaker(r.path)
			}
			if c.Button(sdl.Rect{X: pad + 146, Y: y, W: 54, H: btnH}, "🎞 GIF") {
				a.sceneExportFromPath(r.path, exportGIF)
			}
			if c.Button(sdl.Rect{X: pad + 204, Y: y, W: 74, H: btnH}, "🎬 WebP") {
				a.sceneExportFromPath(r.path, exportWebP) // higher-quality animated WebP
			}
			if c.Button(sdl.Rect{X: pad + 282, Y: y, W: 78, H: btnH}, "🎥 Video") {
				a.sceneExportFromPath(r.path, exportVideo) // MP4/WebM via ffmpeg (Export-options format)
			}
			if c.Button(sdl.Rect{X: pad + 364, Y: y, W: 82, H: btnH}, "🖼 Comic") {
				a.sceneExportFromPath(r.path, exportComic) // single PNG storyboard page (pure Go)
			}
			c.LabelClipped(pad+450, y+4, w-pad-450-scrollBarW, r.name, ColText)
			y += 28
		}
	}
	y += 10

	y = a.settingsSection(y, w, "Replay playback")
	c.Label(pad, y, "How fast a replay plays. Lower = slower, so the whole message types out and lingers long enough", ColTextDim)
	y += 18
	c.Label(pad, y, "to read; higher = a quick recap. 100% is the readable default — adjusts live while a replay runs.", ColTextDim)
	y += 26
	// Bounds mirror the config clamp; SetReplaySpeed is authoritative.
	const (
		minReplayPct  = 25
		maxReplayPct  = 200
		replayPctStep = 5
	)
	rspd := a.d.Prefs.ReplaySpeed()
	if next := a.sliderRow(y, "  Playback speed %", rspd, replayPctStep, minReplayPct, maxReplayPct); next != rspd {
		a.d.Prefs.SetReplaySpeed(next)
	}
	y += 32

	y = a.settingsSection(y, w, "Export to GIF / WebP")
	c.Label(pad, y, "Turn a recording into a shareable animation. Use 🎞 GIF (works everywhere) or 🎬 WebP (true-colour, smaller)", ColTextDim)
	y += 18
	c.Label(pad, y, "on a recording above, or build one in the Scene Maker. These settings apply to every export:", ColTextDim)
	y += 26
	y = a.drawExportOptions(a.formX, y, false) // speed lives in the Replay-playback section above
	return y
}

// drawSettingsTheme: theme picker/folder, layout toggle, live preview, bind.
func (a *App) drawSettingsTheme(y, w, h int32) int32 {
	c := a.ctx
	pad := a.formX
	winW := w // real window width, kept for the aspect-true fit preview
	w = a.formW2()
	y = a.settingsSection(y, w, "Theme")
	c.Label(pad, y+4, "Theme:", ColText)
	// Direct-jump dropdown so a big theme collection is one click + scroll away,
	// not dozens of < > presses (#86). The < > buttons stay for fine stepping.
	selIdx := 0
	for i, n := range settings.themeList {
		if n == settings.themeName {
			selIdx = i
			break
		}
	}
	if len(settings.themeList) > 0 {
		if next, changed := c.Dropdown("themedd", sdl.Rect{X: pad + 60, Y: y, W: 240, H: btnH}, settings.themeList, selIdx); changed {
			settings.themeName = settings.themeList[next]
			a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
			a.applyThemeAsync()
		}
	} else {
		c.Label(pad+60, y+6, settings.themeName, ColAccent)
	}
	if c.Button(sdl.Rect{X: pad + 308, Y: y, W: 26, H: btnH}, "<") {
		a.cycleTheme(-1)
	}
	if c.Button(sdl.Rect{X: pad + 338, Y: y, W: 26, H: btnH}, ">") {
		a.cycleTheme(1)
	}
	if settings.themeBusy {
		c.Label(pad+372, y+6, "scanning...", ColTextDim)
	} else {
		c.Label(pad+372, y+6, fmt.Sprintf("(%d found)", len(settings.themeList)), ColTextDim)
	}
	y += 32
	c.Label(pad, y+4, "Theme folder:", ColText)
	settings.themeDir, _ = c.TextField("themedir", sdl.Rect{X: pad + 130, Y: y, W: 320, H: fieldH}, settings.themeDir, `optional root holding themes\<name> — or drop a folder anywhere`)
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

	y = a.settingsSection(y, w, "Layout & fit")
	// Built-in look: the new optimal layout (OOC as its own box, etc.) is the main theme; ticking
	// "Legacy Developer" reverts to the old developer layout exactly (OOC back in a tab).
	legacy := a.d.Prefs.LegacyDevThemeOn()
	if next := c.Checkbox(pad, y, "Legacy Developer theme (revert to the old built-in look — OOC back in a tab; off = the new optimal layout)", legacy); next != legacy {
		a.d.Prefs.SetLegacyDevTheme(next)
	}
	y += 28
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
		if avail := a.formW; boxW > avail {
			boxW = avail
		}
		boxH := boxW * h / winW // match the real window aspect so the crop is true
		if boxH > 340 {
			boxH, boxW = 340, 340*winW/h
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

	// Layout presets (#34): save the whole default-courtroom arrangement under a name
	// and flip between setups. Presets are window fractions, so they travel across sizes.
	// applyLayoutPreset/applyStagePreset (layoutpresets.go) take effect the same frame.
	y = a.settingsSection(y, w, "Layout presets")
	for _, ln := range c.WrapText("Save the default-courtroom layout (Extras → Edit Layout lets you drag every box) under a name and switch between arrangements — a big stage for watching, a wide log for moderating. Stored as window fractions, so a preset looks right at any window size.", a.formW-8, 0) {
		c.Label(pad, y, ln, ColTextDim)
		y += 16
	}
	y += 8
	// Quick stage premades — overlay just the stage; every other box keeps its spot.
	c.Label(pad, y+4, "Quick stage:", ColText)
	bx := pad + c.TextWidth("Quick stage:") + 12
	for _, pm := range []struct {
		label string
		vp    [4]float64
	}{
		{"Theater 4:3", a.theaterStageFrac(winW, h)},
		{"Wide stage", [4]float64{0, 0, 0.74, 0.66}},
		{"Compact", [4]float64{0, 0, 0.50, 0.46}},
	} {
		bw := c.TextWidth(pm.label) + 16
		if c.Button(sdl.Rect{X: bx, Y: y, W: bw, H: btnH}, pm.label) {
			a.applyStagePreset(pm.vp)
			a.pushDebug("layout: stage preset applied")
		}
		bx += bw + 6
	}
	y += btnH + 8
	if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "Reset to stock") {
		a.applyLayoutPreset(nil)
		a.pushDebug("layout: reset to stock")
	}
	y += btnH + 12

	// Save the CURRENT arrangement under a name.
	c.Label(pad, y+4, "Save current as:", ColText)
	nameX := pad + c.TextWidth("Save current as:") + 10
	saveW := int32(64)
	fieldW := int32(200)
	if maxW := w - nameX - saveW - 10; fieldW > maxW {
		fieldW = maxW
	}
	if fieldW < 80 {
		fieldW = 80
	}
	a.layoutPresetName, _ = c.TextField("layoutpreset", sdl.Rect{X: nameX, Y: y, W: fieldW, H: fieldH}, a.layoutPresetName, "preset name")
	if c.Button(sdl.Rect{X: nameX + fieldW + 8, Y: y, W: saveW, H: fieldH}, "Save") {
		if name := strings.TrimSpace(a.layoutPresetName); name != "" {
			a.d.Prefs.SaveLayoutPreset(name, a.d.Prefs.ClassicLayoutOverrides())
			a.layoutPresetName = ""
		}
	}
	y += fieldH + 10

	// Saved presets: load / delete each.
	if names := a.d.Prefs.LayoutPresetNames(); len(names) == 0 {
		c.Label(pad, y, "No saved presets yet — arrange your layout, then Save it above.", ColTextDim)
		y += 20
	} else {
		sort.Strings(names)
		for _, name := range names {
			if c.Button(sdl.Rect{X: pad, Y: y, W: 60, H: btnH}, "Load") {
				a.applyLayoutPreset(a.d.Prefs.LayoutPreset(name))
				a.pushDebug("layout: preset loaded")
			}
			if c.Button(sdl.Rect{X: pad + 66, Y: y, W: 64, H: btnH}, "Delete") {
				a.d.Prefs.DeleteLayoutPreset(name)
			}
			c.LabelClipped(pad+140, y+4, w-(pad+140)-8, name, ColText)
			y += btnH + 6
		}
	}
	y += 4

	y = a.settingsSection(y, w, "Lobby")
	// Plain lobby: the server list keeps the readable client backdrop instead of
	// the theme's lobbybackground (which is built for AO2's own list and often
	// makes ours unreadable). The courtroom still uses the theme either way.
	plain := a.d.Prefs.PlainLobbyOn()
	if next := c.Checkbox(pad, y, "Plain lobby — keep my readable server-list backdrop, ignore the theme's lobby image (ON by default; the courtroom still uses the theme)", plain); next != plain {
		a.d.Prefs.SetPlainLobby(next)
	}
	y += 28

	y = a.settingsSection(y, w, "Preview & binding")
	// Live preview of the applied chatbox skin + theme text colors.
	y = a.drawThemePreview(y)
	// Per-server theme binding: "this server always uses that theme".
	y = a.drawThemeBindRow(y, w)
	y = a.drawSettingsStageFX(y)
	return y
}

// drawSettingsStageFX: every render-side stage/viewport look — the sprite
// colour washes, motion FX, the retro post-process overlays (CRT / vignette /
// scanlines / grain), chatbox skins, the stage frame and the ambient extras
// (spotlight, breathing, reflection, weather). Lived in General → Display &
// behaviour; moved to the Theme tab on playtest feedback (Tifera: settings
// were overwhelming — these edit how the VIEWPORT looks, which is theme
// territory). Rows are unchanged, only re-homed.
func (a *App) drawSettingsStageFX(y int32) int32 {
	c := a.ctx
	pad := a.formX // rebase every pad-relative box into the content card
	w := a.formW2()
	y = a.settingsSection(y, w, "Stage & viewport effects")
	// Sprite colour FX (all OFF by default): a render-side wash over the
	// on-stage characters. Local eye-candy only — nothing on the wire, nobody
	// else sees it, and zero render cost when everything's off.
	rbs := a.d.Prefs.RainbowSpritesOn()
	if next := c.Checkbox(pad, y, "Rainbow character sprites (OFF by default): washes every on-stage sprite through a hue cycle — local eye-candy only, nobody else sees it, zero render cost when off", rbs); next != rbs {
		a.d.Prefs.SetRainbowSprites(next)
	}
	y += 26
	if rbs {
		sp := a.d.Prefs.RainbowSpeed()
		c.Label(pad+16, y+4, "Speed:", ColTextDim)
		if next := int(c.Slider("rbspeed", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(sp), 100)); next != sp {
			a.d.Prefs.SetRainbowSpriteSpeed(next)
			sp = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", sp), ColAccent)
		y += 26
		vv := a.d.Prefs.RainbowVividness()
		c.Label(pad+16, y+4, "Vividness:", ColTextDim)
		if next := int(c.Slider("rbvivid", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(vv), 100)); next != vv {
			a.d.Prefs.SetRainbowSpriteVividness(next)
			vv = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", vv), ColAccent)
		y += 26
		dsy := a.d.Prefs.RainbowPairDesyncOn()
		if next := c.Checkbox(pad+16, y, "Desync the pair's colour (the two characters show different hues at once)", dsy); next != dsy {
			a.d.Prefs.SetRainbowPairDesync(next)
		}
		y += 26
		pch := a.d.Prefs.RainbowPerCharOn()
		if next := c.Checkbox(pad+16, y, "Different hue per character (each on-stage character cycles to its own colour)", pch); next != pch {
			a.d.Prefs.SetRainbowPerChar(next)
		}
		y += 26
	}
	sst := a.d.Prefs.SpriteSolidTintOn()
	if next := c.Checkbox(pad, y, "Solid colour sprite tint (OFF by default): wash sprites in one fixed colour instead of a cycle (rainbow takes priority if both are on)", sst); next != sst {
		a.d.Prefs.SetSpriteSolidTint(next)
	}
	y += 26
	if sst {
		c.Label(pad+16, y+4, "Tint colour:", ColTextDim)
		cur := a.d.Prefs.SpriteTintColorRGB()
		sw := sdl.Rect{X: pad + 130, Y: y, W: 28, H: fieldH}
		c.Fill(sw, sdl.Color{R: uint8(cur >> 16 & 0xFF), G: uint8(cur >> 8 & 0xFF), B: uint8(cur & 0xFF), A: 255})
		c.Border(sw, ColPanelHi)
		if c.focusID != "spritetinthex" {
			a.spriteTintHex = fmt.Sprintf("%06x", cur) // reflect the pref when not typing
		}
		if next, _ := c.TextField("spritetinthex", sdl.Rect{X: pad + 166, Y: y, W: 100, H: fieldH}, a.spriteTintHex, "RRGGBB"); next != a.spriteTintHex {
			a.spriteTintHex = next
			if rgb, ok := parseHex6(next); ok {
				a.d.Prefs.SetSpriteTintColor(rgb)
			}
		}
		y += 28
	}
	if rbs || sst { // glow applies to whichever wash is on
		glw := a.d.Prefs.RainbowSpriteGlowOn()
		if next := c.Checkbox(pad+16, y, "Neon glow (additive): the tint adds light so sprites glow — they become translucent neon ghosts (you can see the room through them)", glw); next != glw {
			a.d.Prefs.SetRainbowSpriteGlow(next)
		}
		y += 26
	}
	// Motion FX (independent of the colour wash, OFF by default).
	wob := a.d.Prefs.SpriteWobbleOn()
	if next := c.Checkbox(pad, y, "Wobble sprites (OFF by default): a gentle, continuous sway", wob); next != wob {
		a.d.Prefs.SetSpriteWobble(next)
	}
	y += 26
	spn := a.d.Prefs.SpriteSpinOn()
	if next := c.Checkbox(pad, y, "Spin sprites (OFF by default): the on-stage characters rotate slowly — maximum chaos", spn); next != spn {
		a.d.Prefs.SetSpriteSpin(next)
	}
	y += 26
	punch := a.d.Prefs.ShoutPunchOn()
	if next := c.Checkbox(pad, y, "Shout punch (OFF by default): a quick zoom-pop of the whole stage when an objection/shout appears", punch); next != punch {
		a.d.Prefs.SetShoutPunch(next)
	}
	y += 26
	// Post-processing overlays (#10): retro looks blended over the whole stage. All OFF.
	// #77 CRT preset leads the group: one toggle for the whole old-TV look.
	crt := a.d.Prefs.PostCRTOn()
	if next := c.Checkbox(pad, y, "CRT / retro TV (OFF by default): scanlines + an RGB phosphor grille + vignette — the whole old-TV look in one toggle", crt); next != crt {
		a.d.Prefs.SetPostCRT(next)
	}
	y += 26
	vig := a.d.Prefs.PostVignetteOn()
	if next := c.Checkbox(pad, y, "Vignette (OFF by default): darken the stage edges for a cinematic frame", vig); next != vig {
		a.d.Prefs.SetPostVignette(next)
	}
	y += 26
	scan := a.d.Prefs.PostScanlinesOn()
	if next := c.Checkbox(pad, y, "Scanlines (OFF by default): faint CRT scan lines over the stage", scan); next != scan {
		a.d.Prefs.SetPostScanlines(next)
	}
	y += 26
	grain := a.d.Prefs.PostGrainOn()
	if next := c.Checkbox(pad, y, "Film grain (OFF by default): subtle animated noise over the stage", grain); next != grain {
		a.d.Prefs.SetPostGrain(next)
	}
	y += 26
	// Per-character chatbox skins (char.ini chat=<misc>): canonical AO2/webAO
	// behaviour, default ON; off also stops the misc art fetches.
	ccb := a.d.Prefs.CharChatboxOn()
	if next := c.Checkbox(pad, y, "Per-character chatbox skins (ON by default): a speaker with their own chatbox art (char.ini chat=) shows it, like AO2/webAO", ccb); next != ccb {
		a.d.Prefs.SetCharChatbox(next)
	}
	y += 26
	// #56 stage frame: a decorative border around the viewport (Off by default).
	c.Label(pad, y+4, "Stage frame:", ColText)
	sf := a.d.Prefs.StageFrame()
	if next, changed := c.Dropdown("stageframe", sdl.Rect{X: pad + 130, Y: y, W: 170, H: fieldH}, stageFrameNames, sf); changed {
		a.d.Prefs.SetStageFrame(next)
	}
	c.Label(pad+312, y+4, "a decorative border on the stage — pure looks, zero cost when Off", ColTextDim)
	y += 30
	ent := a.d.Prefs.AnimateEntrancesOn()
	if next := c.Checkbox(pad, y, "Animate entrances (OFF by default): a new speaker slides in when they take the stage", ent); next != ent {
		a.d.Prefs.SetAnimateEntrances(next)
	}
	y += 26
	dof := a.d.Prefs.DepthOfFieldOn()
	if next := c.Checkbox(pad, y, "Depth of field (OFF by default): soft-focus + dim the background behind the speaker", dof); next != dof {
		a.d.Prefs.SetDepthOfField(next)
	}
	y += 26
	// #121 speaker spotlight: dim the non-speaker characters + the desk. The Dim slider only
	// shows while it's on (like the rainbow knobs above).
	spot := a.d.Prefs.SpotlightOn()
	if next := c.Checkbox(pad, y, "Speaker spotlight (OFF by default): dim the other characters + the desk so the talking character pops", spot); next != spot {
		a.d.Prefs.SetSpotlight(next)
		spot = next
	}
	y += 26
	if spot {
		lv := a.d.Prefs.SpotlightLevel()
		c.Label(pad+16, y+4, "Dim:", ColTextDim)
		if next := int(c.Slider("spotdim", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(lv), 100)); next != lv {
			a.d.Prefs.SetSpotlightLevel(next)
			lv = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", lv), ColAccent)
		y += 26
	}
	// #122 idle breathing: a gentle bob + breathing-scale on static sprites (AsyncAO-only).
	// The amplitude/speed sliders + the two component toggles show only while it's on. The
	// keybind (Settings → Keybinds, or the default below) toggles it hands-free.
	brth := a.d.Prefs.IdleBreathOn()
	if next := c.Checkbox(pad, y, "Idle breathing (OFF by default): static sprites gently bob + breathe so they feel alive — only other AsyncAO users won't see your local view, this is your viewer", brth); next != brth {
		a.d.Prefs.SetIdleBreath(next)
		brth = next
	}
	y += 26
	if brth {
		amp := a.d.Prefs.BreathAmp()
		c.Label(pad+16, y+4, "Amount:", ColTextDim)
		if next := int(c.Slider("breathamp", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(amp), 100)); next != amp {
			a.d.Prefs.SetBreathAmp(next)
			amp = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", amp), ColAccent)
		y += 26
		spd := a.d.Prefs.BreathSpeed()
		c.Label(pad+16, y+4, "Speed:", ColTextDim)
		if next := int(c.Slider("breathspd", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(spd), 100)); next != spd {
			a.d.Prefs.SetBreathSpeed(next)
			spd = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", spd), ColAccent)
		y += 26
		bob := a.d.Prefs.BreathBobOn()
		if next := c.Checkbox(pad+16, y, "Vertical bob", bob); next != bob {
			a.d.Prefs.SetBreathBob(next)
		}
		scl := a.d.Prefs.BreathScaleOn()
		if next := c.Checkbox(pad+180, y, "Breathing scale", scl); next != scl {
			a.d.Prefs.SetBreathScale(next)
		}
		y += 26
	}
	// #123 glass-floor reflection: a flipped, faded mirror of the sprites below the floor line.
	refl := a.d.Prefs.ReflectionOn()
	if next := c.Checkbox(pad, y, "Glass-floor reflection (OFF by default): a flipped, faded mirror of the characters on the floor below them", refl); next != refl {
		a.d.Prefs.SetReflection(next)
		refl = next
	}
	y += 26
	if refl {
		op := a.d.Prefs.ReflectStrength()
		c.Label(pad+16, y+4, "Opacity:", ColTextDim)
		if next := int(c.Slider("reflop", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(op), 100)); next != op {
			a.d.Prefs.SetReflectStrength(next)
			op = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", op), ColAccent)
		y += 26
	}
	// #124 ambient weather: a cycle picker (None → Snow → Rain → Sakura → Embers) + an
	// Intensity slider while a weather is on. The keybind cycles it hands-free.
	wk := a.d.Prefs.WeatherType()
	c.Label(pad, y+4, "Weather (OFF by default):", ColTextDim)
	if c.Button(sdl.Rect{X: pad + 210, Y: y, W: 90, H: 22}, render.WeatherName(render.Weather(wk))) {
		a.d.Prefs.SetWeatherType((wk + 1) % int(render.WeatherCount))
	}
	y += 26
	if wk != 0 {
		wi := a.d.Prefs.WeatherIntensity()
		c.Label(pad+16, y+4, "Intensity:", ColTextDim)
		if next := int(c.Slider("wxint", sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}, int32(wi), 100)); next != wi {
			a.d.Prefs.SetWeatherIntensity(next)
			wi = next
		}
		c.Label(pad+312, y+4, fmt.Sprintf("%3d%%", wi), ColAccent)
		y += 26
	}
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
// prefetchAggroLabel names the predictive-prefetch level (#100) for the slider readout.
func prefetchAggroLabel(n int) string {
	switch n {
	case 1:
		return "Conservative (1 sprite)"
	case 2:
		return "Balanced (2 sprites)"
	case 3:
		return "Eager (3 sprites)"
	default:
		return "Aggressive (4 sprites)"
	}
}

func (a *App) drawSettingsAssets(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	// Per-server format profile: probe exactly the formats a given server uses,
	// seeded instantly so the very first probe is right (no webp-first waste). The
	// official-vanilla servers carry a built-in example; apply or clear it per server.
	y = a.settingsSection(y, w, "Server format profile")
	host := hostOfURL(a.urls.Origin())
	if host == "" {
		c.Label(pad, y, "Connect to a server to set its format profile.", ColTextDim)
		y += 26
	} else {
		hasCustom := a.d.Prefs.ExtProfile(host) != ""
		status := "no profile — fetches this server's extensions.json, else your global default"
		switch {
		case hasCustom:
			status = "custom profile active"
		case a.extProfileFor(host) != "":
			status = "built-in official-vanilla profile active"
		}
		c.LabelClipped(pad, y, w-pad-scrollBarW, "This server ("+host+"): "+status, ColTextDim)
		y += 24
		if c.Button(sdl.Rect{X: pad, Y: y, W: 300, H: btnH}, "Apply official-vanilla profile here") {
			a.d.Prefs.SetExtProfile(host, assets.BundledVanillaManifestJSON)
			a.manifestFor = ""     // allow a re-seed
			a.fetchManifestAsync() // apply instantly
		}
		if hasCustom {
			if c.Button(sdl.Rect{X: pad + 310, Y: y, W: 130, H: btnH}, "Clear profile") {
				a.d.Prefs.SetExtProfile(host, "")
				a.manifestFor = ""
				a.fetchManifestAsync()
			}
		}
		y += btnH + 6
		c.LabelClipped(pad, y, w-pad-scrollBarW, "A profile probes exactly the formats it lists (per server, instant) and overrides both the server's manifest and your global default — for this server only.", ColTextDim)
		y += 22
	}

	y = a.settingsSection(y, w, "Predictive prefetch")
	c.LabelClipped(pad, y, w-pad-scrollBarW, "How many of the next sprites AsyncAO guesses ahead and warms while you chat — higher means snappier sprite swaps but more speculative downloading.", ColTextDim)
	y += 22
	aggro := int32(a.d.Prefs.PrefetchAggressiveness())
	c.Label(pad, y+4, "Level:", ColText)
	if nv := c.Slider("prefetchaggro", sdl.Rect{X: pad + 70, Y: y, W: 200, H: btnH}, aggro, 4); nv != aggro {
		if nv < 1 {
			nv = 1
		}
		a.d.Prefs.SetPrefetchAggressiveness(int(nv))
		if a.room != nil && a.room.Predictor != nil {
			a.room.Predictor.SetAggressiveness(int(nv)) // apply live to the running predictor
		}
	}
	c.Label(pad+285, y+4, prefetchAggroLabel(a.d.Prefs.PrefetchAggressiveness()), ColAccent)
	y += 30

	// (Image formats + desk-format policy moved to the Power user tab.)

	// #127 full-character bundle prefetch (default OFF): pre-grab a character's whole sprite
	// set on load so emote switches are instant — speculative + low priority, so it sheds
	// under load and never blocks live fetches (it uses more bandwidth + cache up front).
	bundle := a.d.Prefs.CharBundlePrefetchOn()
	if next := c.Checkbox(pad, y, "Preload a character's whole sprite set (OFF by default): grabs every emote up front so switching is instant — speculative, low-priority, more bandwidth", bundle); next != bundle {
		a.d.Prefs.SetCharBundlePrefetch(next)
	}
	y += 28

	// #128 connection-quality chip: a tiny signal-bar icon (bottom-left) showing the server
	// round-trip time, with the exact ms on hover. Off by default → no ping loop runs.
	pingChip := a.d.Prefs.PingChipOn()
	if next := c.Checkbox(pad, y, "Connection ping chip (OFF by default): a tiny signal-bar icon (bottom-left); hover it for the exact ms", pingChip); next != pingChip {
		a.d.Prefs.SetPingChip(next)
	}
	y += 28

	// Missing-asset banner: opt-in (default OFF). The failures always reach the
	// debug overlay; this only governs the red on-screen banner.
	showWarn := a.d.Prefs.AssetWarningsOn()
	if next := c.Checkbox(pad, y, "Show missing-asset warnings (red banner naming assets that failed to load — off by default)", showWarn); next != showWarn {
		a.d.Prefs.SetAssetWarnings(next)
	}
	y += 28

	y = a.settingsSection(y, w, "Audio formats")
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

	y = a.settingsSection(y, w, "Local assets")
	// Local assets (no-streaming legacy mode).
	enabled, mounts := a.d.Prefs.LocalAssets()
	if next := c.Checkbox(pad, y, "Read assets from local folders instead of streaming (legacy servers without an asset URL)", enabled); next != enabled {
		a.d.Prefs.SetLocalAssets(next, mounts)
		a.rebuildAssetOrigin()
	}
	y += 28
	c.Label(pad, y+4, "Mount folder:", ColText)
	settings.mountInput, _ = c.TextField("mount", sdl.Rect{X: pad + 130, Y: y, W: 320, H: fieldH}, settings.mountInput, `C:\AO2\base or /home/you/ao2/base`)
	if c.Button(sdl.Rect{X: pad + 460, Y: y, W: 80, H: btnH}, "Add") && strings.TrimSpace(settings.mountInput) != "" {
		a.d.Prefs.SetLocalAssets(enabled, append(mounts, strings.TrimSpace(settings.mountInput)))
		settings.mountInput = ""
		a.rebuildAssetOrigin()
	}
	y += 32
	for i, m := range mounts {
		c.LabelClipped(pad+20, y+4, a.formW-130, fmt.Sprintf("%d. %s", i+1, m), ColText)
		if c.Button(sdl.Rect{X: a.formX + a.formW - 90, Y: y, W: 90, H: 24}, "Remove") {
			next := append(append([]string{}, mounts[:i]...), mounts[i+1:]...)
			a.d.Prefs.SetLocalAssets(enabled, next)
			a.rebuildAssetOrigin()
			break
		}
		y += 28
	}
	y += 10

	y = a.settingsSection(y, w, "Downloader")
	// Built-in single-asset downloader (opt-in).
	y = a.drawDownloaderSettings(y, w)

	y = a.settingsSection(y, w, "Cache")
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

	// One-click "fix stuck images" (#36, Dag): when a character's emote-button art (or any
	// asset) gets stuck on a wrong-format / cached 404, the emote grid falls back to the
	// SAME character icon for every cell ("all the images are the same"). Neither clear
	// button alone fixes it — you need the learned format AND the cached 404 gone together —
	// so this does both in one click; the next probe re-derives the right format from scratch.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 300, H: btnH}, "Fix stuck / repeated images") {
		a.d.Prefs.ClearLearned()
		a.d.Resolver.InvalidateAll()
		settings.statusLine = "Cleared learned formats + disk cache — images re-derive on next use."
		if err := a.d.Manager.ClearDisk(); err != nil {
			settings.statusLine = "Cleared learned formats; disk clear failed: " + err.Error()
		}
	}
	y += 22
	c.Label(pad, y, "Use if emote buttons (or other art) all show the SAME image: clears the learned format AND the", ColTextDim)
	y += 18
	c.Label(pad, y, "cached 404 together, so a wrongly-probed format re-derives from scratch.", ColTextDim)
	y += 30

	return y
}

// drawSettingsReset is the dedicated Reset tab: the factory-reset launcher, moved
// out of the Assets tab into its own section so it stands alone and isn't a
// misclick risk among other controls (playtest: "make reset all settings its own
// section. Split the settings.").
func (a *App) drawSettingsReset(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Reset")
	c.Label(pad, y, "Reset the settings page, or wipe everything (favourites, logins, data, cache).", ColTextDim)
	y += 26
	// Factory reset: opens a pop-up offering settings-only or a full wipe.
	if c.Button(sdl.Rect{X: pad, Y: y, W: 220, H: btnH}, "Reset to defaults…") {
		a.showReset = true
	}
	y += 34
	return y
}

// drawResetConfirm is the factory-reset pop-up: settings-only (keeps your data,
// logins and cache) or a full wipe (erases everything, fresh-install state). It
// owns the screen + input while open, so its buttons can't double-fire with the
// settings widgets underneath.
func (a *App) drawResetConfirm(w, h int32) {
	c := a.ctx
	if c.escPressed { // ESC cancels the confirm (the safe default), like Cancel
		a.showReset = false
		return
	}
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
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = a.d.Prefs.LayoutScales()
	a.oocPct = a.d.Prefs.OOCScale()
	a.uiScalePct = a.d.Prefs.UIScale()
	a.ctx.SetUIScale(a.UIScale())
	a.oocName = a.defaultOOCName() // the active tab re-seeds; parked tabs keep theirs
	a.refreshCharKeys()
	a.applyThemeAsync()
	a.applyAudioVolumes()
	if a.room != nil {
		a.applyTimingToRoom()
		a.room.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
		a.room.ReduceMotion = a.d.Prefs.ReduceMotion()
	}
}

// drawSettingsAudio: per-channel volumes (master/music/SFX/blip/alert) and music
// ducking. Split out of the old combined Audio & Chat tab so audio settings stand
// on their own (playtest: "why is audio and chat in the same group").
func (a *App) drawSettingsAudio(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Volume")
	// Volumes save PER SERVER while you're connected to one, so muting blips (or
	// anything) on one server leaves the others untouched — each tab keeps its own.
	// Edited from the lobby they set the global default new servers start from.
	if a.serverName != "" {
		c.Label(pad, y, "Saved for this server ("+a.serverName+") — each tab keeps its own volumes; a channel you never touch follows the global default.", ColTextDim)
		y += 22
	}
	em, emu, es, eb := a.effectiveVolumes()
	nm := a.volumeRow(y, "Master volume", em) // scales everything; also on the Extras box
	y += 30
	nmu := a.volumeRow(y, "Music volume", emu)
	y += 26
	ns := a.volumeRow(y, "SFX volume", es)
	y += 26
	nb := a.volumeRow(y, "Blip volume", eb)
	y += 26
	if nm != em || nmu != emu || ns != es || nb != eb {
		a.setEffectiveVolumes(nm, nmu, ns, nb) // per-server when connected, else global
	}
	// Callword/friend ping volume — its OWN control, independent of SFX (so a quiet
	// SFX mix or the SFX mute never silences your name-pings) and global (alerts
	// should reach you the same on every server).
	if av := a.volumeRow(y, "Callword/alert volume", a.d.Prefs.AlertVolume()); av != a.d.Prefs.AlertVolume() {
		a.d.Prefs.SetAlertVolume(av)
		a.applyAudioVolumes()
	}
	y += 32
	// Music ducking (off by default): dip the music while a message plays.
	duck := a.d.Prefs.MusicDucking()
	if next := c.Checkbox(pad, y, "Duck music while someone talks (lower music during a message so dialogue stays clear)", duck); next != duck {
		a.d.Prefs.SetMusicDucking(next)
	}
	y += 28

	y = a.settingsSection(y, w, "Blips")
	rate, onSpaces := a.d.Prefs.BlipTyping()
	if v := a.sliderRow(y, "Blip rate (1 blip / N letters)", rate, 1, config.MinBlipRate, config.MaxBlipRate); v != rate {
		a.d.Prefs.SetBlipTyping(v, onSpaces)
		a.applyTimingToRoom() // applies from the next message
	}
	c.LabelClipped(pad+300, y+4, w-pad-300-scrollBarW, "2 = Ace Attorney style · 1 = every letter", ColTextDim)
	y += 30
	if next := c.Checkbox(pad, y, "Blip on spaces too (off = skip whitespace, like Ace Attorney)", onSpaces); next != onSpaces {
		a.d.Prefs.SetBlipTyping(rate, next)
		a.applyTimingToRoom()
	}
	y += 28
	return y
}

// drawSettingsChat: message timing, typing, casing alerts, callwords, plus the
// friends / ignore / DND / music-history / mod sections — everything from the old
// Audio & Chat tab except the volumes (now their own Audio tab).
func (a *App) drawSettingsChat(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	y = a.settingsSection(y, w, "Group chat")
	gcBtn := a.d.Prefs.GroupChatButtonOn()
	if next := c.Checkbox(pad, y, "Show the Group Chat button in the courtroom (ON by default): a main button to open DMs & group chats with other AsyncAO players. It's always in Extras → Group Chat too.", gcBtn); next != gcBtn {
		a.d.Prefs.SetGroupChatButton(next)
	}
	y += 30

	y = a.settingsSection(y, w, "Text & typing")
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
	// Message-send extras — lived in General → Display & behaviour; moved here
	// on playtest feedback (Tifera: "move messaging settings to Chat"). They
	// shape the messages you TYPE, so they belong with text & typing.
	re := a.d.Prefs.RandomEmoteOn()
	if next := c.Checkbox(pad, y, "Auto-random emote (OFF by default): every message picks a different emote from your character's set — for the lazy, and to show off more sprites", re); next != re {
		a.d.Prefs.SetRandomEmote(next)
	}
	y += 26
	rmc := a.d.Prefs.RandomMessageColorOn()
	if next := c.Checkbox(pad, y, "Random colour for each IC message (OFF by default): every message you send picks a random text colour — everyone sees it (standard colour field)", rmc); next != rmc {
		a.d.Prefs.SetRandomMessageColor(next)
	}
	y += 26
	rbw := a.d.Prefs.RainbowMessagesOn()
	if next := c.Checkbox(pad, y, "Rainbow IC messages (OFF by default): your text cycles the palette per letter (takes priority over random; renders on clients that read inline \\cr colour)", rbw); next != rbw {
		a.d.Prefs.SetRainbowMessages(next)
	}
	y += 26

	y = a.settingsSection(y, w, "Chat log")
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
	// Auto-clip on modcall (opt-out): save the recent IC log when a modcall fires.
	clipMod := a.d.Prefs.AutoClipModcallOn()
	if next := c.Checkbox(pad, y, "Auto-clip on modcall (ON by default): when a mod is called, save the last IC lines to logs/<server>/modcalls/ as evidence", clipMod); next != clipMod {
		a.d.Prefs.SetAutoClipModcall(next)
	}
	y += 26
	// Show the full song URL in the "has played a song" log line instead of just
	// the name (a song's link, on request).
	songURL := a.d.Prefs.ShowSongURLOn()
	if next := c.Checkbox(pad, y, "Show the full song URL in the music log line (OFF by default): the whole link instead of just the song name", songURL); next != songURL {
		a.d.Prefs.SetShowSongURL(next)
	}
	y += 26

	y = a.settingsSection(y, w, "Area list")
	// Current-area highlight (playtest ask): the row you're in reads coloured
	// at a glance in the player list + mod dashboard; green by default,
	// recolourable here (the Extras-box hex convention: blank = default).
	c.Label(pad, y+4, "Your area's colour:", ColTextDim)
	areaHex := a.d.Prefs.AreaHighlightColorHex()
	sw := sdl.Rect{X: pad + 160, Y: y + 1, W: 18, H: 18}
	c.Fill(sw, a.areaHighlightCol())
	c.Border(sw, ColTextDim)
	if next, _ := c.TextField("areahicol", sdl.Rect{X: pad + 186, Y: y, W: 110, H: fieldH}, areaHex, "rrggbb"); next != areaHex {
		a.d.Prefs.SetAreaHighlightColorHex(next)
	}
	wheelLbl := "Wheel…"
	if a.showAreaWheel {
		wheelLbl = "Close"
	}
	if c.Button(sdl.Rect{X: pad + 306, Y: y - 1, W: 64, H: 22}, wheelLbl) {
		a.showAreaWheel = !a.showAreaWheel
	}
	c.LabelClipped(pad+378, y+4, w-pad-378-scrollBarW, "hex like 4ac96c — blank = the stock green; marks the area you're in", ColTextDim)
	y += 32
	if a.showAreaWheel {
		// The shared inline colour wheel (drawWheelPicker), writing back as hex —
		// the same value the field above edits. The extra button restores the
		// stock green (the blank-pref default).
		hi := a.areaHighlightCol()
		wheelY := y
		ny, hx := a.drawWheelPicker(pad, wheelY, int(hi.R)<<16|int(hi.G)<<8|int(hi.B), func(rgb int) {
			a.d.Prefs.SetAreaHighlightColorHex(fmt.Sprintf("%06x", rgb))
		})
		if c.Button(sdl.Rect{X: hx, Y: wheelY + 42, W: 100, H: btnH}, "Stock green") {
			a.d.Prefs.SetAreaHighlightColorHex("")
		}
		y = ny
	}

	y = a.settingsSection(y, w, "Case alerts")
	// Case announcements (CASEA, tsuserver-family): subscribe by role.
	y = a.drawCasingRow(y)

	y = a.settingsSection(y, w, "Callwords")
	// #203 self-mention: auto-treat your own showname / character name as a
	// callword (whole-word match, never your own messages) so you're pinged when
	// addressed by name without adding it manually. Honours the sound/toast/OS
	// options below and the streamer / DND / login-grace suppressions.
	ms := a.d.Prefs.MentionSelfOn()
	if next := c.Checkbox(pad, y, "Alert me when someone says my name (OFF by default): treats your showname + character as callwords, matched as whole words and never on your own lines", ms); next != ms {
		a.d.Prefs.SetMentionSelf(next)
	}
	y += 30
	// Callwords manager: type a word (or paste "a, b, c") + Add, and each shows
	// below with a × to remove. Flash + sound + toast fire on an IC/OOC match.
	c.Label(pad, y+4, "Add word(s):", ColTextDim)
	var callCommit bool
	settings.callAddInput, callCommit = c.TextField("callwordadd", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, settings.callAddInput, "your name, nickname… (comma-separates; flash + sound when seen in IC/OOC)")
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "+ Add") || callCommit {
		if n := a.d.Prefs.AddCallWord(settings.callAddInput); n > 0 {
			settings.callAddInput = ""
			settings.statusLine = fmt.Sprintf("Added %d callword(s).", n)
		} else {
			settings.statusLine = "Nothing added (blank, already listed, or at the 32-word cap)."
		}
	}
	y += 30
	if words := a.d.Prefs.CallWords(); len(words) > 0 {
		for _, w := range words {
			if c.Button(sdl.Rect{X: pad + 28, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.RemoveCallWord(w)
			}
			c.LabelClipped(pad+56, y+1, 320, w, ColText)
			y += 24
		}
	} else {
		c.Label(pad+28, y+1, "No callwords yet — add your name to get pinged when it's said.", ColTextDim)
		y += 24
	}
	y += 6
	c.Label(pad, y+4, "Callword sound:", ColTextDim)
	if next, _ := c.TextField("cwsound", sdl.Rect{X: pad + 130, Y: y, W: 480, H: fieldH}, a.d.Prefs.CallwordSoundPath(), "custom .wav/.ogg/.mp3/.opus path (blank = built-in ping)"); next != a.d.Prefs.CallwordSoundPath() {
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
	y += 26
	cot := a.d.Prefs.CallwordOSToastOn()
	if next := c.Checkbox(pad+16, y, "Also pop a DESKTOP (OS) notification when AsyncAO is in the background (OFF by default; Windows; rate-limited)", cot); next != cot {
		a.d.Prefs.SetCallwordOSToast(next)
	}
	y += 26
	cooc := a.d.Prefs.CallwordsOOCOn()
	if next := c.Checkbox(pad, y, "Check OOC messages for callwords too (OFF by default — IC only; on = also ping on OOC, e.g. someone typing your room ID)", cooc); next != cooc {
		a.d.Prefs.SetCallwordsOOC(next)
	}
	y += 30
	y = a.settingsSection(y, w, "Do Not Disturb")
	// Do Not Disturb: session-only by default (clears every launch so it can't
	// silently kill your callwords days later) — mutes the personal pings only.
	// The keybind (default Ctrl+D, rebindable on the Controls tab) toggles it too.
	dndPersist := a.d.Prefs.DNDPersistOn()
	dndTail := "Clears on restart."
	if dndPersist {
		dndTail = "Remembered across restarts (option below)."
	}
	dndLabel := "Do Not Disturb (Ctrl+" + strings.ToUpper(a.hotkeyFor(hotkeyDND)) + ") — mute callword + friend pings (sound/toast/window flash). Modcalls + case alerts still come through. " + dndTail
	if next := c.Checkbox(pad, y, dndLabel, a.dndOn); next != a.dndOn {
		a.setDND(next)
		if next {
			settings.statusLine = "Do Not Disturb ON — callword + friend pings muted."
		} else {
			settings.statusLine = "Do Not Disturb off."
		}
	}
	y += 26
	if next := c.Checkbox(pad+16, y, "Remember Do Not Disturb across restarts (default off — DND normally clears every launch)", dndPersist); next != dndPersist {
		a.d.Prefs.SetDNDPersist(next)
		if next {
			a.d.Prefs.SetDNDSaved(a.dndOn) // snapshot the current state so it restores correctly
		}
	}
	y += 30

	y = a.settingsSection(y, w, "Messages & connection")
	mc := a.d.Prefs.MessageCounterOn()
	if next := c.Checkbox(pad, y, "Show a character count by the IC box (ON by default): turns red past ~256 chars, where many servers truncate.", mc); next != mc {
		a.d.Prefs.SetMessageCounter(next)
	}
	y += 26

	ts := a.d.Prefs.ICTimestampsOn()
	if next := c.Checkbox(pad, y, "Show local timestamps in the IC log (OFF by default): prefixes each line with the time it arrived, so you can see when people spoke.", ts); next != ts {
		a.d.Prefs.SetICTimestamps(next)
	}
	y += 26

	ti := a.d.Prefs.TypingIndicatorOn()
	if next := c.Checkbox(pad, y, "Show \"X is typing…\" between AsyncAO users (OFF by default): a live caption when other AsyncAO players are composing. Uses a hidden, throttled OOC signal only AsyncAO reads; off = your client sends nothing.", ti); next != ti {
		a.d.Prefs.SetTypingIndicator(next)
	}
	y += 26

	ar := a.d.Prefs.AutoReconnectOn()
	if next := c.Checkbox(pad, y, "Auto-reconnect after a dropped connection (ON by default): retries the last server with backoff. A deliberate Disconnect never reconnects; the manual Reconnect button always works.", ar); next != ar {
		a.d.Prefs.SetAutoReconnect(next)
	}
	y += 26

	idc := a.d.Prefs.InstantDisconnectOn()
	if next := c.Checkbox(pad, y, "Instant disconnect (OFF by default): the Disconnect button asks for confirmation first, since it's easy to hit by accident. Turn this on to disconnect immediately with no prompt.", idc); next != idc {
		a.d.Prefs.SetInstantDisconnect(next)
	}
	y += 26

	acl := a.d.Prefs.AutoConnectOnLaunchOn()
	lastName, lastURL := a.d.Prefs.LastServer()
	label := "Auto-connect to my last server on launch (OFF by default): opens straight onto the server you last used, even after a disconnect. Also bound to a Connect-to-last-server key (Controls tab)."
	if lastURL != "" {
		label = "Auto-connect on launch to \"" + lastName + "\" (OFF by default): your last server. Reconnect there with the Connect-to-last-server key (Controls tab) too."
	}
	if next := c.Checkbox(pad, y, label, acl); next != acl {
		a.d.Prefs.SetAutoConnectOnLaunch(next)
	}
	y += 30

	y = a.settingsSection(y, w, "Sound effects")
	// M11 per-SFX mute: silence an annoying emote sound effect by name. The
	// last one you heard gets a one-click toggle; the muted list is below.
	if a.lastSFXName != "" {
		on := a.d.Prefs.IsSFXMuted(a.lastSFXName)
		lbl := "Mute last SFX: " + a.lastSFXName
		if on {
			lbl = "Unmute last SFX: " + a.lastSFXName
		}
		if c.Button(sdl.Rect{X: pad, Y: y, W: c.TextWidth(lbl) + 20, H: btnH}, lbl) {
			if on {
				a.d.Prefs.UnmuteSFX(a.lastSFXName)
			} else {
				a.d.Prefs.MuteSFX(a.lastSFXName)
			}
		}
		y += 30
	} else {
		c.Label(pad, y+2, "Per-SFX mute: the last emote sound effect you hear gets a one-click Mute button here.", ColTextDim)
		y += 26
	}
	if list := a.d.Prefs.MutedSFXList(); len(list) > 0 {
		c.Label(pad, y+2, "Muted sound effects (× to unmute):", ColTextDim)
		y += 22
		for _, name := range list {
			if c.Button(sdl.Rect{X: pad + 12, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.UnmuteSFX(name)
			}
			c.LabelClipped(pad+40, y+1, 360, name, ColText)
			y += 24
		}
		y += 6
	}

	// M11 per-character blip volume: quiet a character whose typing blips are
	// too loud (their scale multiplies the global blip volume; 100% = no
	// change). The last speaker gets a quick slider; everyone you've adjusted
	// is listed below with their own slider and a × that resets to 100%.
	c.Label(pad, y+2, "Per-character blip volume (typing-sound loudness, 100% = unchanged):", ColTextDim)
	y += 22
	if a.lastBlipChar != "" {
		cur := a.d.Prefs.BlipVolumeFor(a.lastBlipChar)
		if nv := a.volumeRow(y, "Last speaker — "+a.lastBlipChar, cur); nv != cur {
			a.d.Prefs.SetBlipVolume(a.lastBlipChar, nv)
		}
		y += 28
	} else {
		c.Label(pad+12, y+1, "The last character to speak gets a quick slider here.", ColTextDim)
		y += 24
	}
	if vols := a.d.Prefs.BlipVolumes(); len(vols) > 0 {
		names := make([]string, 0, len(vols))
		for name := range vols {
			names = append(names, name)
		}
		sort.Strings(names) // stable row order (map iteration is random)
		for _, name := range names {
			if c.Button(sdl.Rect{X: pad + 12, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.SetBlipVolume(name, 100) // reset to default (100% = unchanged)
			}
			c.LabelClipped(pad+40, y+1, 124, name, ColText)
			track := sdl.Rect{X: pad + 170, Y: y + 1, W: 110, H: 16}
			if nv := int(c.Slider("blipvol:"+name, track, int32(vols[name]), 100)); nv != vols[name] {
				a.d.Prefs.SetBlipVolume(name, nv)
			}
			c.Label(track.X+track.W+8, y+1, fmt.Sprintf("%3d%%", vols[name]), ColAccent)
			y += 24
		}
		y += 6
	}

	y = a.settingsSection(y, w, "Music history")
	// M12: keep a session "recently played" jukebox history (ON by default).
	mh := a.d.Prefs.MusicHistoryOn()
	if next := c.Checkbox(pad, y, "Keep a \"recently played\" music history (ON by default): the Jukebox tab lists songs played in the room so you can Save a link (into the \"Music history\" playlist), Play, or Share. Off = don't record.", mh); next != mh {
		a.d.Prefs.SetMusicHistory(next)
	}
	y += 30

	// Domain allowlist for the history: only songs from these "unique" user-hosted
	// domains are recorded (the server's own music still plays, it's just not
	// saved). Discord records audio files only. Add/remove like the muted-SFX list.
	if mh {
		c.Label(pad+16, y, "Only record songs from these hosts — others still play, just aren't saved. Add a host (catbox.moe), or a host/folder", ColTextDim)
		y += 18
		c.Label(pad+16, y, "for a server's user-rip path (e.g. miku.pizza/base/youtube → only songs under it). Discord: audio files only.", ColTextDim)
		y += 22
		a.musicHostInput, _ = c.TextField("musichost", sdl.Rect{X: pad + 16, Y: y, W: 240, H: fieldH}, a.musicHostInput, "Host or host/folder (e.g. catbox.moe)…")
		if c.Button(sdl.Rect{X: pad + 262, Y: y, W: 80, H: btnH}, "+ Add") {
			if a.d.Prefs.AddMusicHost(a.musicHostInput) {
				a.musicHostInput = ""
			} else {
				a.jukeWarn("Enter a host or host/folder (or it's already listed / at the cap).")
			}
		}
		y += fieldH + 6
		for _, h := range a.d.Prefs.MusicHostList() {
			if c.Button(sdl.Rect{X: pad + 28, Y: y, W: 20, H: 18}, "×") {
				a.d.Prefs.RemoveMusicHost(h)
			}
			c.LabelClipped(pad+56, y+1, 320, h, ColText)
			y += 24
		}
		y += 6
	}

	y = a.settingsSection(y, w, "Friends")
	// Player-list friend button (ON by default): the per-row "+ Friend" /
	// "Unfriend" button. Hide it if it clutters the panel.
	sfb := a.d.Prefs.FriendButtonShown()
	if next := c.Checkbox(pad, y, "Show the \"+ Friend\" / \"Unfriend\" button on player-list rows (ON by default): one click adds or removes a friend.", sfb); next != sfb {
		a.d.Prefs.SetShowFriendButton(next)
	}
	y += 26
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
		settings.friendInput, friendCommit = c.TextField("friends", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, settings.friendInput, "showname, showname=ffcc00, showname=ffcc00=Nickname, ... (per server)")
		if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Save") || friendCommit {
			a.d.Prefs.SetServerFriends(a.serverKey, strings.Split(settings.friendInput, ","))
			settings.statusLine = "Friends saved for this server."
		}
		y += 28
		c.Label(pad+110, y, "name=RRGGBB sets a custom glow + name colour; name=RRGGBB=Nick adds a personal nickname (shown in the player list). Use name==Nick for a nickname with no colour. No commas in nicknames.", ColTextDim)
		y += 24
	}

	y = a.settingsSection(y, w, "Ignored players")
	if a.serverKey == "" {
		c.Label(pad, y+4, "Ignored: connect to a server to manage its ignore list.", ColTextDim)
		y += 30
	} else {
		c.Label(pad, y+4, "Ignored:", ColText)
		if settings.ignoreKey != a.serverKey { // reload buffer per server
			settings.ignoreInput = strings.Join(a.d.Prefs.ServerIgnored(a.serverKey), ", ")
			settings.ignoreKey = a.serverKey
		}
		var ignoreCommit bool
		settings.ignoreInput, ignoreCommit = c.TextField("ignored", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, settings.ignoreInput, "showname1, showname2, ... (saved per server)")
		if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Save") || ignoreCommit {
			a.d.Prefs.SetServerIgnored(a.serverKey, strings.Split(settings.ignoreInput, ","))
			settings.statusLine = "Ignore list saved for this server."
		}
		y += 28
		c.Label(pad+110, y, "Ignored players' IC + OOC are hidden entirely (no log, no sprite, no blip). Edit here to un-ignore someone who's left. Use the Ignore button on player rows too.", ColTextDim)
		y += 24
	}

	y = a.settingsSection(y, w, "Mod tools")
	// Mod-call desktop toast (for moderators).
	mct := a.d.Prefs.ModcallToastOn()
	if next := c.Checkbox(pad, y, "Desktop notification on mod-call (OFF by default): pop a Windows toast when a modcall comes in — for mods who alt-tabbed away", mct); next != mct {
		a.d.Prefs.SetModcallToast(next)
	}
	y += 28

	// Mod-command feedback sounds (#60): a distinct sound on ban/kick/mute —
	// fires when your own /ban /kick /mute lands in OOC, on mod actions you can
	// see, and when you personally get kicked/banned. Each action has its own
	// toggle + optional custom file (blank = the built-in default). A duty
	// signal, so it plays through Do Not Disturb. Test plays it right now.
	c.Label(pad, y, "Mod-command SFX — play a sound when a ban / kick / mute happens (blank file = built-in default):", ColTextDim)
	y += 24
	type modSFXRow struct {
		label  string
		on     bool
		path   string
		action render.ModAction
		setOn  func(bool)
		setPth func(string)
	}
	for _, r := range []modSFXRow{
		{"Ban", a.d.Prefs.ModBanSFXOn(), a.d.Prefs.ModBanSoundPath(), render.ModBan, a.d.Prefs.SetModBanSFX, a.d.Prefs.SetModBanSoundPath},
		{"Kick", a.d.Prefs.ModKickSFXOn(), a.d.Prefs.ModKickSoundPath(), render.ModKick, a.d.Prefs.SetModKickSFX, a.d.Prefs.SetModKickSoundPath},
		{"Mute", a.d.Prefs.ModMuteSFXOn(), a.d.Prefs.ModMuteSoundPath(), render.ModMute, a.d.Prefs.SetModMuteSFX, a.d.Prefs.SetModMuteSoundPath},
	} {
		if next := c.Checkbox(pad+16, y, r.label, r.on); next != r.on {
			r.setOn(next)
		}
		if next, _ := c.TextField("modsfx:"+r.label, sdl.Rect{X: pad + 130, Y: y, W: 380, H: fieldH}, r.path, "custom .wav/.ogg/.mp3/.opus (blank = built-in)"); next != r.path {
			r.setPth(next)
		}
		if c.Button(sdl.Rect{X: pad + 520, Y: y, W: 60, H: btnH}, "Test") {
			a.d.Audio.PlayModAction(r.action, r.path)
		}
		y += 28
	}
	return y
}

// drawSettingsAccount: per-server login, the master-list override, Discord.
// formatWaitMs renders a millisecond knob compactly ("850 ms", "1.5 s").
func formatWaitMs(ms int) string {
	if ms < 1000 {
		return strconv.Itoa(ms) + " ms"
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', -1, 64) + " s"
}

// spriteLoadModeLabel is the cycle-button label for the power-user cold-load sprite
// behaviour: what a character layer shows while its NEW (uncached) sprite streams.
func spriteLoadModeLabel(mode int) string {
	switch mode {
	case config.SpriteLoadHoldPrev:
		return "Uncached sprite: keep the previous one (webAO-style)"
	case config.SpriteLoadWait:
		return "Uncached sprite: hold the message until it loads (client-AO-style)"
	}
	return "Uncached sprite: show nothing until it loads (default)"
}

// powerUserToggleLabel is the reveal-button label for the advanced (power-user) settings.
func powerUserToggleLabel(on bool) string {
	if on {
		return "Hide advanced options"
	}
	return "Show advanced (power-user) options"
}

// charCaseLabel is the cycle-button label for the character-folder asset casing (power-user only).
func charCaseLabel(c uint8) string {
	switch c {
	case courtroom.CharCaseFirstCap:
		return "Casing: First cap (Phoenix wright)"
	case courtroom.CharCaseTitle:
		return "Casing: Title (Phoenix Wright)"
	case courtroom.CharCaseAuto:
		return "Casing: Auto-detect (learn per server)"
	default:
		return "Casing: lowercase (default)"
	}
}

// drawSettingsPowerUser is the advanced Settings card (its own left-sidebar tab): TLS validation,
// the Asset Origin header, character-folder casing, and image-format tuning — options that can
// BREAK connections or asset fetching if set wrong. The whole card hides behind a reveal button and
// its warnings are red. Moved here out of Account / Assets so everyday settings stay uncluttered.
func (a *App) drawSettingsPowerUser(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	// Diagnostics — the Debug panel only READS state, so it sits ABOVE the
	// dangerous reveal gate below. Opening it returns to the courtroom, where the
	// floating panel draws. Also on F8 anywhere, and Extras → Debug.
	y = a.settingsSection(y, w, "Diagnostics")
	if c.Button(sdl.Rect{X: pad, Y: y, W: 260, H: btnH}, "Open Debug panel (F8)") {
		a.showDebugPanel = true
		a.screen = a.prevScreen // back to the courtroom, where the panel is drawn
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "Server software, live packet inspector, performance (frame graph, heap vs budget, GC), the three-tier asset cache, and the failure log — one floating panel. Also opens with F8 anywhere, or Extras → Debug.", ColTextDim)
	y += 10

	y = a.settingsSection(y, w, "Power user (advanced)")
	y = a.settingsDesc(pad, y, "⚠ Everything here is for people who KNOW what their server needs. The wrong value can make characters fetch 0 assets or stop you connecting. If unsure, leave it all at the defaults.", ColDanger)
	y += 4
	if c.Button(sdl.Rect{X: pad, Y: y, W: 270, H: btnH}, powerUserToggleLabel(a.showPowerUser)) {
		a.showPowerUser = !a.showPowerUser
		a.powerNukeArm = false // never carry an armed nuke across a hide/reveal
	}
	y += btnH + 8
	if !a.showPowerUser {
		return y
	}

	// The nuke: reset EVERY option on this tab to its shipped default (two-click
	// confirm — arming flips the label). Scoped to this tab; the image-format block
	// keeps its own controls (its learned state is per-server, not a "knob").
	nukeLabel := "⟲ Reset ALL power-user options to defaults"
	if a.powerNukeArm {
		nukeLabel = "⚠ Click again to confirm the reset"
	}
	if c.Button(sdl.Rect{X: pad, Y: y, W: 340, H: btnH}, nukeLabel) {
		if a.powerNukeArm {
			a.d.Prefs.ResetPowerUser()
			a.d.Manager.SetAssetOrigin("") // the live network override too, not just the pref
			a.rebuildAssetOrigin()         // re-derive the URL builder (character casing reset)
			a.applyTimingToRoom()          // push the reset renderer/timing knobs to the live room
			if th := a.d.Manager.Thumbs(); th != nil {
				th.SetEnabled(false) // the thumbnail store follows its reset pref (stored thumbs are kept — Clear is separate)
				th.SetParams(0, 0)
				th.SetBudget(0)
			}
			a.d.Manager.SetAdaptiveLatencyMultiple(0) // back to the ×8 default, live
			a.applySpriteCap()                        // re-derive the downscale cap from defaults
			a.warnLine = "Power-user options reset to defaults."
			a.warnAt = a.now()
		}
		a.powerNukeArm = !a.powerNukeArm
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "Puts every option on this tab back to its default. Image-format probing keeps its own controls below; saved presets and mod chips are untouched.", ColTextDim)
	y += 10

	// Update channel: stable releases vs the experimental test-branch feed.
	// Power-user by design — test builds exist for extensive debugging and may
	// move sideways or DOWN in version; the strict per-platform asset match
	// still applies, and flipping back rejoins stable on its next release.
	y = a.settingsSection(y, w, "Update channel")
	expCh := a.d.Prefs.UpdateChannelExperimentalOn()
	if next := c.Checkbox(pad, y, "Get experimental test builds (the MayAO-Test branch) instead of stable releases", expCh); next != expCh {
		a.d.Prefs.SetUpdateChannelExperimental(next)
		// Re-arm the one-shot launch check so the swap takes effect NOW (next
		// frame), not on the next restart — and drop a stale offer from the
		// other channel so its modal can't push the wrong build.
		a.updateChecked = false
		a.updateRel = nil
		a.updateShow = false
	}
	y += 26
	y = a.settingsDesc(pad, y, "OFF (default): the launch check follows stable releases and only ever moves forward. ON: it follows the newest published build INCLUDING prereleases cut from the test branch — riskier, less tested, and it may offer a lower version than you run (that's also how you get back: turn this off and take the next stable offer). Toggling re-checks immediately.", ColTextDim)
	y += 10

	// Renderer — what a character layer shows while a NEW, uncached sprite is still
	// streaming + decoding (the playtest cold-load flash report). Purely cosmetic and
	// fully isolated (it can't break a connection or an asset fetch), but it touches
	// the render path, so it sits behind the reveal with the rest of the advanced kit.
	y = a.settingsSection(y, w, "Renderer — uncached sprite loading")
	slm := a.d.Prefs.SpriteLoadMode()
	if c.Button(sdl.Rect{X: pad, Y: y, W: 460, H: btnH}, spriteLoadModeLabel(slm)) {
		a.d.Prefs.SetSpriteLoadMode((slm + 1) % config.SpriteLoadModeCount)
		a.applyTimingToRoom() // the wait gate lives on the live room — flip it now
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "What shows while an uncached sprite is still downloading. Show nothing = blank until it lands. Keep the previous one = the last sprite stays until the new one is ready (webAO-style). Hold the message = the message waits for its sprite, capped by the timeout below (a broken sprite only ever delays it; on timeout the previous sprite is kept). Cosmetic only; no cost once a sprite is cached.", ColTextDim)
	y += 6
	if slm == config.SpriteLoadWait {
		c.Label(pad, y+4, "Max hold per message:", ColText)
		waitMs := a.d.Prefs.SpriteWaitMs()
		wtrack := sdl.Rect{X: pad + 170, Y: y + 2, W: 260, H: 16}
		nw := int(clampI32(c.Slider("spritewaitms", wtrack, int32(waitMs), config.SpriteWaitMaxMs), config.SpriteWaitMinMs, config.SpriteWaitMaxMs))
		c.Tooltip(wtrack, "How long one message may wait for its sprite before playing anyway (50 ms – 30 s).")
		c.Label(pad+170+266, y+4, formatWaitMs(nw), ColTextDim)
		if nw != waitMs {
			a.d.Prefs.SetSpriteWaitMs(nw)
			a.applyTimingToRoom()
		}
		y += 26
		wp := a.d.Prefs.SpriteWaitPairOn()
		if next := c.Checkbox(pad, y, "Also wait for the pair partner's sprite", wp); next != wp {
			a.d.Prefs.SetSpriteWaitPair(next)
			a.applyTimingToRoom()
		}
		y += 24
		wpre := a.d.Prefs.SpriteWaitPreanimOn()
		if next := c.Checkbox(pad, y, "Also wait for the pre-animation", wpre); next != wpre {
			a.d.Prefs.SetSpriteWaitPreanim(next)
			a.applyTimingToRoom()
		}
		y += 26
	}
	if slm != config.SpriteLoadBlank { // hold-previous applies in Hold mode AND as Wait's timeout fallback
		c.Label(pad, y+4, "Keep the previous sprite for:", ColText)
		ageMs := a.d.Prefs.HoldPrevMaxAgeMs()
		atrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
		na := int(clampI32(c.Slider("holdmaxage", atrack, int32(ageMs), config.HoldPrevMaxAgeMaxMs), 0, config.HoldPrevMaxAgeMaxMs))
		if na != 0 && na < config.HoldPrevMaxAgeMinMs {
			na = 0 // the bottom of the track snaps to "forever" (0)
		}
		c.Tooltip(atrack, "How long the stand-in may bridge a still-loading sprite before giving up to blank. Far left = forever.")
		ageLabel := "forever"
		if na != 0 {
			ageLabel = formatWaitMs(na)
		}
		c.Label(pad+200+236, y+4, ageLabel, ColTextDim)
		if na != ageMs {
			a.d.Prefs.SetHoldPrevMaxAgeMs(na)
		}
		y += 26
		tint := a.d.Prefs.HoldDebugTintOn()
		if next := c.Checkbox(pad, y, "Tint stand-in sprites amber (diagnostic: shows when a stand-in is bridging a load)", tint); next != tint {
			a.d.Prefs.SetHoldDebugTint(next)
		}
		y += 26
	}
	c.Label(pad, y+4, "Speaker-swap crossfade:", ColText)
	cfMs := a.d.Prefs.CrossfadeMs()
	cftrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	ncf := int(clampI32(c.Slider("crossfadems", cftrack, int32(cfMs), config.CrossfadeMaxMs), 0, config.CrossfadeMaxMs))
	if ncf != 0 && ncf < config.CrossfadeMinMs {
		ncf = 0 // bottom of the track = off (the default hard swap)
	}
	c.Tooltip(cftrack, "Blend a sprite change: the new sprite fades in over this long instead of snapping (0 = off). Suppressed by Reduce motion.")
	cfLabel := "off (hard swap)"
	if ncf != 0 {
		cfLabel = formatWaitMs(ncf)
	}
	c.Label(pad+200+236, y+4, cfLabel, ColTextDim)
	if ncf != cfMs {
		a.d.Prefs.SetCrossfadeMs(ncf)
	}
	y += 26
	y = a.settingsDesc(pad, y, "Fades the new sprite in over the old on every character/emote change instead of hard-swapping. Local only; a still-loading sprite starts its fade when it arrives. With a crossfade set, the idle frame-rate cap stands down so fades stay smooth.", ColTextDim)
	y += 10

	// Frame rate & GPU — the adaptive pacing that fixed the idle GPU burn.
	y = a.settingsSection(y, w, "Frame rate & GPU")
	y = a.settingsDesc(pad, y, "AsyncAO renders adaptively: the active rate while you interact or anything animates, the idle rate when nothing needs redrawing, the background rate when another window has focus (minimized draws nothing). Any change returns to the active rate instantly. Each rate takes a typed exact number; 0 on Idle/Background means never redraw in that state (0 GPU when nothing moves), and ∞ removes the cap — for Active that means vsync paces the frames.", ColTextDim)
	y += 6
	y = a.fpsSettingRow(y, "fpscap", "Active frame rate:",
		config.FPSCapMin, config.FPSCapMax, config.FPSCapDefault, config.FPSCapMin,
		a.d.Prefs.FPSCap, a.d.Prefs.SetFPSCap, &settings.fpsBufs[0],
		"The ceiling while interacting or animating (1–240, type an exact number, or ∞ = uncapped/vsync). 60 is plenty for AO.")
	y = a.fpsSettingRow(y, "idlefps", "Idle frame rate:",
		config.IdleFPSMin, config.IdleFPSMax, config.IdleFPSDefault, config.FPSOff,
		a.d.Prefs.IdleFPS, a.d.Prefs.SetIdleFPS, &settings.fpsBufs[1],
		"The rate when nothing needs redrawing (1–120, 0 = never redraw when idle, or ∞ = uncapped). Any change returns to the active rate instantly.")
	y = a.fpsSettingRow(y, "unfocusedfps", "Background frame rate:",
		config.UnfocusedFPSMin, config.UnfocusedFPSMax, config.UnfocusedFPSDefault, config.FPSOff,
		a.d.Prefs.UnfocusedFPS, a.d.Prefs.SetUnfocusedFPS, &settings.fpsBufs[2],
		"The rate while another window has focus (1–60, 0 = never redraw when unfocused, or ∞ = uncapped). Minimized draws nothing.")
	y = a.settingsDesc(pad, y, "\"Animating\" = messages typing or queued, shouts/preanims, shakes/flashes, replays, the Scene Maker, exports, voice, reactions, toasts, the pinned second courtroom, the F3 graph, and any always-moving effect you enable. Sprite loops and theme art run at their own ≤15 fps timings, so the idle default never visibly slows them; raise it if something looks choppy while idle.", ColTextDim)
	y += 10
	edl := a.d.Prefs.EventDrivenLoopOn()
	if next := c.Checkbox(pad, y, "Event-driven renderer (EXPERIMENTAL, default ON on this test build)", edl); next != edl {
		a.d.Prefs.SetEventDrivenLoop(next)
	}
	y += 26
	y = a.settingsDesc(pad, y, "Static screens stop redrawing entirely: input wakes the renderer instantly, incoming messages and finished loads push their own wake, and a blinking caret or ticking clock redraws one frame exactly when it's due. Sounds and the connection never slow down — only wasted redraws go. Applies live. If anything looks frozen or choppy, turn this off (the classic pacing loop) and report what it was.", ColTextDim)
	y += 10
	nolimit := a.d.Prefs.FrameLimiterDisabled()
	if next := c.Checkbox(pad, y, "Disable the frame limiter entirely (render every frame — vsync only, HIGH GPU)", nolimit); next != nolimit {
		a.d.Prefs.SetFrameLimiterDisabled(next)
	}
	y += 26
	y = a.settingsDesc(pad, y, "Off by default. On = no idle skipping and no pacing at all: the client redraws continuously, paced only by vsync (or a 60 fps floor with -vsync=false). The opposite of everything above — it trades GPU for the last drop of responsiveness. Leave it off unless you have a specific reason.", ColTextDim)
	y += 10

	// Sprite thumbnail cache — the persistent low-q stand-in store.
	y = a.settingsSection(y, w, "Sprite thumbnail cache")
	th := a.d.Manager.Thumbs()
	if th == nil {
		y = a.settingsDesc(pad, y, "Unavailable in this build (the thumbnail store could not open).", ColTextDim)
		y += 10
	} else {
		tcOn := a.d.Prefs.ThumbCacheOn()
		if next := c.Checkbox(pad, y, "Keep tiny low-quality thumbnails of every sprite (default OFF)", tcOn); next != tcOn {
			a.d.Prefs.SetThumbCache(next)
			th.SetEnabled(next)
		}
		y += 26
		y = a.settingsDesc(pad, y, "Every sprite that finishes loading leaves a ~1 KB low-quality thumbnail on disk (its own folder, kept after the full sprite is evicted). The next cold load shows the thumbnail instantly while the real one streams, then swaps — covering the case where there's no previous sprite to hold. Costs a little disk and a moment of visibly crunchy art.", ColTextDim)
		y += 6
		if a.d.Prefs.ThumbCacheOn() {
			c.Label(pad, y+4, "Thumbnail height:", ColText)
			tHp := a.d.Prefs.ThumbHeightPx()
			ttrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
			nth := int(clampI32(c.Slider("thumbheight", ttrack, int32(tHp), config.ThumbHeightMaxPx), 0, config.ThumbHeightMaxPx))
			if nth != 0 && nth < config.ThumbHeightMinPx {
				nth = 0 // bottom of the track = the default (64 px)
			}
			c.Tooltip(ttrack, "How tall a stored thumbnail is (32–160 px). Bigger = sharper stand-in, bigger files. Applies to NEWLY stored thumbnails.")
			thLabel := "default (64 px)"
			if nth != 0 {
				thLabel = strconv.Itoa(nth) + " px"
			}
			c.Label(pad+200+236, y+4, thLabel, ColTextDim)
			if nth != tHp {
				a.d.Prefs.SetThumbHeightPx(nth)
				th.SetParams(a.d.Prefs.ThumbHeightPx(), a.d.Prefs.ThumbQuality())
			}
			y += 26
			c.Label(pad, y+4, "Thumbnail quality:", ColText)
			tq := a.d.Prefs.ThumbQuality()
			qtrack2 := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
			ntq := int(clampI32(c.Slider("thumbquality", qtrack2, int32(tq), config.ThumbQualityMax), 0, config.ThumbQualityMax))
			if ntq != 0 && ntq < config.ThumbQualityMin {
				ntq = 0
			}
			c.Tooltip(qtrack2, "WebP quality of a stored thumbnail (5–60). Lower = smaller + crunchier; the default 20 lands ~1 KB.")
			tqLabel := "default (20)"
			if ntq != 0 {
				tqLabel = strconv.Itoa(ntq)
			}
			c.Label(pad+200+236, y+4, tqLabel, ColTextDim)
			if ntq != tq {
				a.d.Prefs.SetThumbQuality(ntq)
				th.SetParams(a.d.Prefs.ThumbHeightPx(), a.d.Prefs.ThumbQuality())
			}
			y += 26
			c.Label(pad, y+4, "Thumbnail store budget:", ColText)
			tb := a.d.Prefs.ThumbBudgetMiB()
			btrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
			ntb := int(clampI32(c.Slider("thumbbudget", btrack, int32(tb), config.ThumbBudgetMaxMiB), 0, config.ThumbBudgetMaxMiB))
			if ntb != 0 && ntb < config.ThumbBudgetMinMiB {
				ntb = 0 // bottom of the track = the default (64 MiB)
			}
			c.Tooltip(btrack, "Hard cap on the thumbnail folder (8–512 MiB): past it the OLDEST thumbnails auto-delete. 64 MiB ≈ ~60,000 thumbnails.")
			tbLabel := "default (64 MiB)"
			if ntb != 0 {
				tbLabel = strconv.Itoa(ntb) + " MiB"
			}
			c.Label(pad+200+236, y+4, tbLabel, ColTextDim)
			if ntb != tb {
				a.d.Prefs.SetThumbBudgetMiB(ntb)
				th.SetBudget(int64(a.d.Prefs.ThumbBudgetMiB()) << 20)
			}
			y += 26
			if c.Button(sdl.Rect{X: pad, Y: y, W: 200, H: btnH}, "Clear stored thumbnails") {
				if err := th.Clear(); err != nil {
					a.warnLine = clampLine("Clear thumbnails: " + err.Error())
				} else {
					a.warnLine = "Stored thumbnails cleared."
				}
				a.warnAt = a.now()
			}
			y += btnH + 6
		}
		y += 10
	}

	// Core message timings — the courtroom ceremony's hard-coded spans, exposed.
	y = a.settingsSection(y, w, "Core message timings")
	c.Label(pad, y+4, "Shout bubble duration:", ColText)
	shoutMs := a.d.Prefs.ShoutDurationMs()
	strack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	ns := int(clampI32(c.Slider("shoutdurms", strack, int32(shoutMs), config.ShoutDurationMaxMs), 0, config.ShoutDurationMaxMs))
	if ns != 0 && ns < config.ShoutDurationMinMs {
		ns = 0 // bottom of the track = the canonical default
	}
	c.Tooltip(strack, "How long an Objection!/Hold It!/Take That! bubble holds the stage. Far left = the canonical AO2 timing (~0.72 s).")
	shoutLabel := "default (0.72 s)"
	if ns != 0 {
		shoutLabel = formatWaitMs(ns)
	}
	c.Label(pad+200+236, y+4, shoutLabel, ColTextDim)
	if ns != shoutMs {
		a.d.Prefs.SetShoutDurationMs(ns)
		a.applyTimingToRoom()
	}
	y += 26
	c.Label(pad, y+4, "Pre-animation wait cap:", ColText)
	preMs := a.d.Prefs.PreanimTimeoutMs()
	ptrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	np := int(clampI32(c.Slider("preanimms", ptrack, int32(preMs), config.PreanimTimeoutMaxMs), 0, config.PreanimTimeoutMaxMs))
	if np != 0 && np < config.PreanimTimeoutMinMs {
		np = 0
	}
	c.Tooltip(ptrack, "How long a pre-animation may play before the text starts anyway when its real length is unknown (still decoding). Far left = the canonical default (2.5 s).")
	preLabel := "default (2.5 s)"
	if np != 0 {
		preLabel = formatWaitMs(np)
	}
	c.Label(pad+200+236, y+4, preLabel, ColTextDim)
	if np != preMs {
		a.d.Prefs.SetPreanimTimeoutMs(np)
		a.applyTimingToRoom()
	}
	y += 26
	c.Label(pad, y+4, "IC backlog queue depth:", ColText)
	qcap := a.d.Prefs.ICQueueCap()
	qtrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	nq := int(clampI32(c.Slider("icqueuecap", qtrack, int32(qcap), config.ICQueueCapMax), 0, config.ICQueueCapMax))
	if nq != 0 && nq < config.ICQueueCapMin {
		nq = 0 // bottom of the track = the canonical default (64)
	}
	c.Tooltip(qtrack, "How many unplayed messages a packed room may stack before the OLDEST drops (the IC log records everything regardless). Far left = the default (64).")
	qLabel := "default (64)"
	if nq != 0 {
		qLabel = strconv.Itoa(nq) + " messages"
	}
	c.Label(pad+200+236, y+4, qLabel, ColTextDim)
	if nq != qcap {
		a.d.Prefs.SetICQueueCap(nq)
		a.applyTimingToRoom()
	}
	y += 26
	c.Label(pad, y+4, "Catch-up flash linger:", ColText)
	cul := a.d.Prefs.CatchUpLingerMs()
	ctrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	nc := int(clampI32(c.Slider("catchuplinger", ctrack, int32(cul), config.CatchUpLingerMaxMs), 0, config.CatchUpLingerMaxMs))
	c.Tooltip(ctrack, "How long each fast-forwarded backlog message stays up while catch-up drains a pile-up. 0 (default) = one per frame — fastest; a little linger makes the backlog readable as it flashes past.")
	culLabel := "default (one per frame)"
	if nc != 0 {
		culLabel = formatWaitMs(nc)
	}
	c.Label(pad+200+236, y+4, culLabel, ColTextDim)
	if nc != cul {
		a.d.Prefs.SetCatchUpLingerMs(nc)
		a.applyTimingToRoom()
	}
	y += 26
	y = a.settingsDesc(pad, y, "Shout and preanim pace the message ceremony; queue depth bounds how much backlog is kept; catch-up linger holds each fast-forwarded backlog message a touch longer. Far left on every slider = the canonical AO2 default.", ColTextDim)
	y += 10

	// Viewport sprite mask — confine offset sprites to the stage.
	y = a.settingsSection(y, w, "Viewport sprite mask")
	clipS := a.d.Prefs.ClipSpritesToStageOn()
	if next := c.Checkbox(pad, y, "Clip sprites to the stage (default ON)", clipS); next != clipS {
		a.d.Prefs.SetClipSpritesToStage(next)
	}
	y += 24
	y = a.settingsDesc(pad, y, "Masks character sprites to the stage so a large pair/reposition offset can't spill over the chatbox or log. Off = free placement past the stage edges.", ColTextDim)
	y += 10

	// TLS certificate validation.
	y = a.settingsSection(y, w, "TLS certificate")
	validate := a.d.Prefs.ValidateTLSCertsOn()
	if next := c.Checkbox(pad, y, "Validate server certificates (wss) — OFF by default", validate); next != validate {
		a.d.Prefs.SetValidateTLSCerts(next)
	}
	y += 24
	y = a.settingsDesc(pad, y, "Strictly verify the TLS certificate when connecting over wss://. Most community AO servers use self-signed certs, so turning this ON can make them UNREACHABLE — it's only for confirming the encrypted connection is to the real server.", ColDanger)
	y += 10

	// Origin overrides (asset fetches + the WS handshake).
	y = a.settingsSection(y, w, "Origin overrides")
	c.Label(pad, y+4, "Assets Origin/Referer:", ColText)
	origin := a.d.Prefs.AssetOriginHeader()
	if next, _ := c.TextField("assetorigin", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, origin, "https://webao.example  (blank = off)"); next != origin {
		a.d.Prefs.SetAssetOriginHeader(next)
		a.d.Manager.SetAssetOrigin(next)
	}
	y += 24
	y = a.settingsDesc(pad, y, "Sends this Origin/Referer header on asset downloads, so a server that only serves its base to its own web client still streams to AsyncAO. Wrong value = assets stop loading — leave it blank unless you know you need it.", ColDanger)
	y += 8
	c.Label(pad, y+4, "Connection Origin:", ColText)
	wsOrigin := a.d.Prefs.WSOriginHeader()
	if next, _ := c.TextField("wsorigin", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, wsOrigin, "https://webao.example  (blank = off)"); next != wsOrigin {
		a.d.Prefs.SetWSOriginHeader(next)
	}
	y += 24
	y = a.settingsDesc(pad, y, "Sends this Origin header on the SERVER CONNECTION itself (the WebSocket handshake), for the rare server that only accepts its own web client — e.g. one that requires \"webao.<its domain>\". Applies on the next connect. Wrong value can get the connection refused — leave it blank unless a server demands it.", ColDanger)
	y += 10

	// Network tuning — the streaming pipeline's self-tuning bounds, exposed.
	y = a.settingsSection(y, w, "Network tuning")
	c.Label(pad, y+4, "Missing-asset (404) memory:", ColText)
	ttl := a.d.Prefs.NotFoundTTLSec()
	ntrack := sdl.Rect{X: pad + 210, Y: y + 2, W: 220, H: 16}
	nttl := int(clampI32(c.Slider("notfoundttl", ntrack, int32(ttl), config.NotFoundTTLMaxSec), 0, config.NotFoundTTLMaxSec))
	if nttl != 0 && nttl < config.NotFoundTTLMinSec {
		nttl = 0 // bottom of the track = the default (5 min)
	}
	c.Tooltip(ntrack, "How long a 404'd asset stays \"missing\" before AsyncAO will probe it again (30 s – 60 min). Applies on RESTART.")
	ttlLabel := "default (5 min)"
	if nttl != 0 {
		ttlLabel = formatWaitMs(nttl * 1000)
	}
	c.Label(pad+210+226, y+4, ttlLabel, ColTextDim)
	if nttl != ttl {
		a.d.Prefs.SetNotFoundTTLSec(nttl)
	}
	y += 26
	y = a.settingsDesc(pad, y, "How long a missing (404) asset is remembered before it may be probed again. Shorter picks up newly-uploaded art sooner; longer wastes fewer probes on sparse packs. Applies on restart.", ColTextDim)
	y += 8
	c.Label(pad, y+4, "Slow-host patience:", ColText)
	lm := a.d.Prefs.AdaptiveLatMultiple()
	ltrack := sdl.Rect{X: pad + 210, Y: y + 2, W: 220, H: 16}
	nlm := int(clampI32(c.Slider("latmultiple", ltrack, int32(lm), config.AdaptiveLatMultipleMax), 0, config.AdaptiveLatMultipleMax))
	if nlm != 0 && nlm < config.AdaptiveLatMultipleMin {
		nlm = 0 // bottom of the track = the default (×8)
	}
	c.Tooltip(ltrack, "Each asset host's request deadline = THIS × its observed response-time average (2–32). Applies immediately.")
	lmLabel := "default (×8)"
	if nlm != 0 {
		lmLabel = "×" + strconv.Itoa(nlm)
	}
	c.Label(pad+210+226, y+4, lmLabel, ColTextDim)
	if nlm != lm {
		a.d.Prefs.SetAdaptiveLatMultiple(nlm)
		a.d.Manager.SetAdaptiveLatencyMultiple(a.d.Prefs.AdaptiveLatMultiple())
	}
	y += 26
	y = a.settingsDesc(pad, y, "Each asset host's request deadline is N × its average response time, so one dying mirror can't stall the download lane. Lower gives up sooner (jittery links may see spurious timeouts); higher is more patient.", ColTextDim)
	y += 10

	// Decode downscale + texture memory — how big decoded art lives on the GPU.
	y = a.settingsSection(y, w, "Decode downscale & texture memory")
	dsOff := a.d.Prefs.SpriteDownscaleOffOn()
	if next := c.Checkbox(pad, y, "Disable the automatic sprite downscale entirely (keep EXACT source art)", dsOff); next != dsOff {
		a.d.Prefs.SetSpriteDownscaleOff(next)
		a.applySpriteCap()
	}
	y += 24
	c.Label(pad, y+4, "Downscale target:", ColText)
	dsPct := a.d.Prefs.SpriteDownscalePct()
	dtrack := sdl.Rect{X: pad + 210, Y: y + 2, W: 220, H: 16}
	nds := int(clampI32(c.Slider("downscalepct", dtrack, int32(dsPct), config.SpriteDownscaleMaxPct), 0, config.SpriteDownscaleMaxPct))
	if nds != 0 && nds < config.SpriteDownscaleMinPct {
		nds = 0 // bottom of the track = the default (100 % of the display height)
	}
	c.Tooltip(dtrack, "The decode-time downscale shrinks huge art toward your display height; this scales that target (50–200 %). Applies to NEWLY loaded art.")
	dsLabel := "default (100% of display)"
	if nds != 0 {
		dsLabel = strconv.Itoa(nds) + "%"
	}
	c.Label(pad+210+226, y+4, dsLabel, ColTextDim)
	if nds != dsPct {
		a.d.Prefs.SetSpriteDownscalePct(nds)
		a.applySpriteCap()
	}
	y += 26
	y = a.settingsDesc(pad, y, "Art taller than your display is downscaled once at load (high quality) instead of every frame on the GPU. Lower % = smaller textures, less memory; higher % or the off-switch = more source detail for heavy zooming, at memory cost. Applies to newly loaded art.", ColTextDim)
	y += 8
	c.Label(pad, y+4, "Texture memory budget:", ColText)
	txb := a.d.Prefs.TexBudgetMiB()
	xtrack := sdl.Rect{X: pad + 210, Y: y + 2, W: 220, H: 16}
	ntx := int(clampI32(c.Slider("texbudget", xtrack, int32(txb), config.TexBudgetMaxMiB), 0, config.TexBudgetMaxMiB))
	if ntx != 0 && ntx < config.TexBudgetMinMiB {
		ntx = 0 // bottom of the track = the default (64 MiB)
	}
	c.Tooltip(xtrack, "How much GPU-texture memory decoded art may occupy before the least-recently-used is evicted (32–128 MiB). Applies on RESTART.")
	txLabel := "default (64 MiB)"
	if ntx != 0 {
		txLabel = strconv.Itoa(ntx) + " MiB"
	}
	c.Label(pad+210+226, y+4, txLabel, ColTextDim)
	if ntx != txb {
		a.d.Prefs.SetTexBudgetMiB(ntx)
	}
	y += 26
	y = a.settingsDesc(pad, y, "GPU memory for decoded art; past it the least-recently-used is evicted (and re-fetched if needed again). Bigger = fewer evictions in packed rooms; smaller = lighter footprint. Capped to fit the 256 MiB memory budget. Applies on restart.", ColTextDim)
	y += 10

	// Character-folder casing.
	y = a.settingsSection(y, w, "Character folder casing")
	cs := a.d.Prefs.AssetCharCasing()
	if c.Button(sdl.Rect{X: pad, Y: y, W: 260, H: btnH}, charCaseLabel(cs)) {
		a.d.Prefs.SetAssetCharCasing((cs + 1) % courtroom.CharCaseCount)
		a.rebuildAssetOrigin() // apply the new casing to the URL builder immediately
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "How the character FOLDER is capitalised in asset URLs. The VAST MAJORITY of servers are lowercase (the default) — CHECK YOUR SERVER FIRST: the wrong choice makes EVERY character fetch 0 assets. \"First cap\" = Phoenix wright · \"Title\" = Phoenix Wright. \"Auto-detect\" (OFF unless you pick it) probes one character per server once and learns the casing, staying on lowercase unless lowercase actually fails.", ColDanger)
	y += 10

	// Image formats (moved from Assets — it decides the one probe per asset).
	y = a.settingsSection(y, w, "Image formats")
	y = a.settingsDesc(pad, y, "Controls WHICH image file types AsyncAO asks a server for, per asset kind (sprites, icons, backgrounds…). AsyncAO streams with exactly one network probe per asset, so this list decides that probe — get it wrong and assets 404 (0 fetched), or extra formats are probed and the cold load slows down.", ColTextDim)
	y += 6
	global := a.d.Prefs.GlobalFallbacks()
	if next := c.Checkbox(pad, y, "Enable format fallbacks globally (probe legacy formats after the preferred one)", global); next != global {
		a.d.Prefs.SetGlobalFallbacks(next)
		a.d.Resolver.InvalidateAll()
		a.d.Resolver.WarmFromPrefs()
	}
	y += 24
	y = a.settingsDesc(pad, y, "When ON, if the preferred format 404s the client tries older ones (WebP → PNG → APNG → GIF): safer (finds more art) but can cost extra probes. When OFF, exactly one format is tried per asset.", ColTextDim)
	y += 8
	auto := a.d.Prefs.FormatAutoDetect()
	if next := c.Checkbox(pad, y, "Auto-detect formats from the server's extensions.json on connect (recommended)", auto); next != auto {
		auto = next
		a.d.Prefs.SetFormatAutoDetect(next)
		if next {
			a.manifestFor = "" // re-check the current server right away
			a.fetchManifestAsync()
		}
	}
	y += 24
	y = a.settingsDesc(pad, y, "Recommended ON: reads the server's extensions.json to learn which formats it actually ships, so the single probe per asset is correct. Turn it OFF only to force the formats by hand below.", ColTextDim)
	y += 6
	if auto {
		y = a.settingsDesc(pad, y, "Manual tuning is disabled while auto-detect is on — the rows below show what each server's extensions.json resolved to. Untick auto-detect above to force them by hand. Exception: misc (chatbox skins) has no extensions.json key, so its probes stay hand-tunable here.", ColTextDim)
		for _, typeName := range imageTypeNames {
			if typeName == config.TypeMisc {
				// Chatbox-skin art is never declared by extensions.json and
				// mixes formats pack-by-pack on real servers, so the misc
				// probes stay adjustable even with auto-detect on.
				y = a.drawTypeFormatRow(typeName, y)
				continue
			}
			c.Label(pad, y+2, typeName+":", ColTextDim)
			c.Label(pad+110, y+2, strings.Join(a.d.Prefs.FormatOrder(typeName), "  "), ColTextDim)
			y += 26
		}
	} else {
		y = a.settingsDesc(pad, y, "Formats probed per asset type (defaults: char_icon = PNG only, misc = PNG then WebP — that's the chatbox skins — everything else = WebP only). Add only the formats your server actually uses — extra ones just waste probes.", ColTextDim)
		for _, typeName := range imageTypeNames {
			y = a.drawTypeFormatRow(typeName, y)
		}
	}
	y += 8
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
	return y
}

func (a *App) drawSettingsAccount(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Login")
	// Auto-login: ITS OWN automation, not a macro — per-server creds,
	// software-detected wire flow, fires on join (or via hotkey/button).
	y = a.drawLoginSettings(y, w)
	y += 8

	y = a.settingsSection(y, w, "Master list")
	// Master list override (blank = official). Refresh in the lobby applies.
	c.Label(pad, y+4, "URL:", ColTextDim)
	master := a.d.Prefs.MasterList()
	if next, _ := c.TextField("masterurl", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, master, network.DefaultMasterServerURL); next != master {
		a.d.Prefs.SetMasterList(next)
	}
	y += 34

	// Discord Rich Presence: the whole section lives in a build-tagged file, so a
	// -tags nodiscord binary compiles it out entirely (no section, no code).
	y = a.drawDiscordSection(y, w)
	return y
}

// drawSettingsHotkeys: hotkey rebinds, macros, and the whole-settings bundle.
func (a *App) drawSettingsHotkeys(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Hotkeys")
	y = a.settingsDesc(pad, y, "Click a binding, then press a key (Ctrl + that key triggers it). Esc cancels · right-click resets to default.", ColTextDim)
	y += 8
	// Conflict scan: two actions resolving to the same key clash — only the first
	// in the dispatch switch fires — so flag both rather than fail silently.
	hkConflicts := a.hotkeyConflictKeys()
	hx := pad
	for _, def := range hotkeyDefs {
		c.LabelClipped(hx, y+4, 190, def.label, ColText)
		key := a.hotkeyFor(def.id)
		lbl := "Ctrl+" + strings.ToUpper(key)
		switch {
		case a.hkCapture == def.id:
			lbl = "press a key…"
		case key == "":
			lbl = "(unset)"
		}
		br := sdl.Rect{X: hx + 198, Y: y, W: 110, H: btnH}
		if c.Button(br, lbl) {
			a.hkCapture = def.id // arm capture for this action
			a.bindingFor = ""    // don't also arm a character keybind
			c.focusID = ""       // the capture owns the next keypress
		}
		if c.rightClicked && c.hovering(br) {
			a.d.Prefs.SetHotkey(def.id, "") // reset to the built-in default
			if a.hkCapture == def.id {
				a.hkCapture = ""
			}
		}
		if a.hkCapture != def.id && hkConflicts[key] {
			c.Border(br, ColDanger) // clash: another action shares this key
			c.Tooltip(br, "Ctrl+"+strings.ToUpper(key)+" is bound to more than one action — only the first fires. Rebind one.")
		}
		hx += 340
		if hx > 560 {
			hx = pad
			y += 30
		}
	}
	if hx != pad {
		y += 30 // finish a partial row
	}
	// Capture: the next keypress binds to the armed action; Esc cancels. Consume
	// the key so it can't also act elsewhere (mirrors the hold-to-clear capture).
	if a.hkCapture != "" && c.keyPressed != 0 {
		if c.keyPressed != sdl.K_ESCAPE {
			a.d.Prefs.SetHotkey(a.hkCapture, strings.ToLower(sdl.GetKeyName(c.keyPressed)))
		}
		a.hkCapture = ""
		c.keyPressed = 0
	}
	y += 8
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Reset all to defaults") {
		for _, def := range hotkeyDefs {
			a.d.Prefs.SetHotkey(def.id, "")
		}
		settings.statusLine = "All hotkeys reset to their defaults."
	}
	y += 36

	y = a.settingsSection(y, w, "Macros")
	// Macros: user-defined OOC command sequences with optional keybinds.
	y = a.drawMacroSettings(y, w)
	y += 8

	y = a.settingsSection(y, w, "IC quick-phrases")
	// Bind a key to a canned IC line your CHARACTER says (the IC counterpart to macros).
	y = a.drawICPhraseSettings(y, w)
	y += 8
	// (Export / Import / Open config moved to their own "Data" tab.)
	return y
}

// drawSettingsData is the dedicated "where's my stuff / back it up" tab — one place
// for the settings file, its folder, and export/import, so nobody hunts through
// %AppData% (the recurring confusion).
func (a *App) drawSettingsData(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	y = a.settingsSection(y, w, "Your settings file")
	y = a.settingsDesc(pad, y, "Every setting is ONE editable JSON file (asset_preferences.json). Close AsyncAO before hand-editing — it autosaves.", ColTextDim)
	y += 6
	c.LabelClipped(pad, y, w-pad, configDir(), ColAccent) // the actual path
	y += 24
	if config.ConfigIsPortable() {
		y = a.settingsDesc(pad, y, "Storage: PORTABLE — this folder sits next to AsyncAO, so it travels with a copied install / USB stick.", ColAccent)
	} else {
		y = a.settingsDesc(pad, y, "Storage: OS config folder. Use \"Make portable\" to keep settings beside AsyncAO (USB stick / copy).", ColTextDim)
	}
	y += 6
	if c.Button(sdl.Rect{X: pad, Y: y, W: 180, H: btnH}, "Open config folder") {
		openConfigFolder()
	}
	if c.Button(sdl.Rect{X: pad + 190, Y: y, W: 180, H: btnH}, "Open settings file") {
		openSettingsFile()
	}
	y += 38
	if !config.ConfigIsPortable() {
		if c.Button(sdl.Rect{X: pad, Y: y, W: 260, H: btnH}, "Make portable (copy beside AsyncAO)") {
			makePortableAsync(a)
		}
		c.Label(pad+270, y+6, "Copies settings next to the program; takes effect on restart.", ColTextDim)
		y += 38
	}

	y = a.settingsSection(y, w, "Back up / move to another PC")
	y = a.settingsDesc(pad, y, "Export everything (favourites, layout, hotkeys, wardrobes, learned formats — NOT passwords) to a portable JSON; import it elsewhere.", ColTextDim)
	y += 6
	if c.Button(sdl.Rect{X: pad, Y: y, W: 180, H: btnH}, "Export settings") {
		exportSettingsAsync(a)
	}
	importLabel := "Import settings..."
	if settings.importArmed {
		importLabel = "Drop the .json here"
	}
	if c.Button(sdl.Rect{X: pad + 190, Y: y, W: 200, H: btnH}, importLabel) {
		settings.importArmed = !settings.importArmed
		if settings.importArmed {
			settings.statusLine = "Drop an exported asyncao-settings .json anywhere on this window."
		}
	}
	y += 38

	// Setting presets (Nightingale): named bundles of the WHOLE settings file.
	y = a.settingsSection(y, w, "Setting presets")
	y = a.settingsDesc(pad, y, "Save your current settings under a name and switch between bundles. A preset is a full snapshot (passwords excluded); applying one replaces your settings on the next restart, like an import. Save your current setup as its own preset first if you'll want it back.", ColTextDim)
	y += 6
	settings.presetName, _ = c.TextField("presetname", sdl.Rect{X: pad, Y: y, W: 220, H: fieldH}, settings.presetName, "preset name (e.g. casing)")
	if c.Button(sdl.Rect{X: pad + 228, Y: y, W: 170, H: btnH}, "💾 Save as preset") {
		savePresetAsync(a, settings.presetName)
		settings.presetName = ""
	}
	y += btnH + 8
	for _, name := range a.d.Prefs.ListPresets() {
		c.Label(pad, y+4, name, ColText)
		if c.Button(sdl.Rect{X: pad + 240, Y: y, W: 150, H: btnH}, "Apply (restart)") {
			applyPresetAsync(a, name)
		}
		if c.Button(sdl.Rect{X: pad + 396, Y: y, W: 30, H: btnH}, "×") {
			_ = a.d.Prefs.DeletePreset(name)
		}
		y += btnH + 4
	}
	y += 6

	y = a.settingsSection(y, w, "Other data")
	y = a.settingsDesc(pad, y, "logs / recordings / screenshots sit next to the AsyncAO program (portable).", ColTextDim)
	y = a.settingsDesc(pad, y, "The streamed-asset cache is separate — view or clear it under Assets → Cache.", ColTextDim)
	y += 8
	return y
}

// savePresetAsync snapshots the current settings under presets/<name>.json
// (the export machinery: flush, strip passwords, write) off-thread.
func savePresetAsync(a *App, name string) {
	go func() {
		line := "Preset saved — pick it from the list any time."
		if err := a.d.Prefs.SavePreset(name); err != nil {
			line = "Save preset failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
}

// applyPresetAsync stages a preset as the live settings (the import machinery:
// validate + atomic replace, applied on the next start) off-thread.
func applyPresetAsync(a *App, name string) {
	go func() {
		line := "Preset \"" + name + "\" staged — RESTART AsyncAO to apply (changes made this session won't save)."
		if err := a.d.Prefs.ApplyPreset(name); err != nil {
			line = "Apply preset failed: " + err.Error()
		}
		select {
		case settings.ioRes <- line:
		default:
		}
	}()
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

// makePortableAsync copies the active config set (settings + notebooks + jukebox)
// into a config/ folder beside the executable so it travels with a portable copy
// or USB stick. Off-thread (§17.2: no sync disk I/O on the render thread); the
// move takes effect on the next launch.
func makePortableAsync(a *App) {
	go func() {
		dest, err := a.d.Prefs.MigrateToPortable()
		var line string
		switch {
		case err == config.ErrAlreadyPortable:
			line = "Config is already portable (" + dest + ")."
		case err != nil:
			line = "Make portable failed: " + err.Error()
		default:
			line = "Copied your settings to " + dest + " — restart AsyncAO to start using the portable copy."
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
	pad := a.formX
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

// drawDownloaderSettings renders the opt-in single-asset downloader section:
// the master toggle (OFF by default), the benefit explainer, the downloads
// folder with open / add-to-mounts actions, and the live job status + Cancel.
// Returns the next y. (Folds into its own tab when the settings screen is
// tabbed.)
func (a *App) drawDownloaderSettings(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
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
		if c.Button(sdl.Rect{X: a.formX + a.formW - 340, Y: y, W: 150, H: btnH}, "Open folder") {
			_ = exec.Command("explorer.exe", root).Start()
		}
		if c.Button(sdl.Rect{X: a.formX + a.formW - 180, Y: y, W: 180, H: btnH}, "Add to local mounts") {
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
			if c.Button(sdl.Rect{X: a.formX + a.formW - 218, Y: y, W: 100, H: btnH}, pauseLabel) {
				a.dlPaused.Store(!a.dlPaused.Load())
			}
			if c.Button(sdl.Rect{X: a.formX + a.formW - 110, Y: y, W: 110, H: btnH}, "Cancel") {
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
	pad := a.formX
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
	pad := a.formX // rebase into the settings content card
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
func (a *App) drawThemeBindRow(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
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
	pad := a.formX
	c.Label(pad, y+4, name+":", ColText)
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 170, H: 16}
	value = int(c.Slider(name, track, int32(value), 100))
	c.Label(track.X+track.W+12, y+4, fmt.Sprintf("%3d%%", value), ColAccent)
	return value
}

// numberRow is volumeRow for arbitrary units/steps/bounds (spinbox-style:
// −/+ plus mousewheel over the row).
func (a *App) numberRow(y int32, label string, value, step, min, max int, tip ...string) int {
	c := a.ctx
	pad := a.formX
	c.Label(pad, y+4, label+":", ColText)
	if c.Button(sdl.Rect{X: pad + 130, Y: y, W: 24, H: 24}, "-") && value-step >= min {
		value -= step
	}
	c.Label(pad+162, y+4, fmt.Sprintf("%5d", value), ColAccent)
	if c.Button(sdl.Rect{X: pad + 224, Y: y, W: 24, H: 24}, "+") && value+step <= max {
		value += step
	}
	row := sdl.Rect{X: pad, Y: y, W: 252, H: 26}
	if c.hovering(row) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered spinbox owns the wheel — no page scroll
		next := value + int(c.wheelY)*step
		if next >= min && next <= max {
			value = next
		}
	}
	settingTip(c, row, tip) // #19: optional "what's this" hover explanation
	return value
}

// settingTip shows a value row's "what's this" tooltip (#19) when one was passed — a hover
// explanation for the terse slider / number controls that can't carry a full inline label like the
// (already self-documenting) checkboxes do.
func settingTip(c *Ctx, row sdl.Rect, tip []string) {
	if len(tip) > 0 && tip[0] != "" {
		c.Tooltip(row, tip[0])
	}
}

// sliderRow is numberRow drawn as a draggable slider (same signature, so it's
// a drop-in): label, a slider track, then the live value — all left of the
// pad+270 help text the settings rows use. Drag for coarse; mousewheel over
// the row still fine-tunes by ±step (numberRow parity). The result snaps to
// the step grid and clamps to [min, max].
func (a *App) sliderRow(y int32, label string, value, step, min, max int, tip ...string) int {
	c := a.ctx
	pad := a.formX
	if c.onRow != nil {
		c.onRow(label, y) // gather-search: slider rows are findable like checkboxes
	}
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
	settingTip(c, sdl.Rect{X: pad, Y: y, W: 252, H: 26}, tip) // #19: optional "what's this" hover explanation
	return value
}

// uiScaleSliderID is the manual UI-scale slider's drag id (== the label passed to
// sliderRow), used to detect the drag for commit-on-release.
const uiScaleSliderID = "UI scale %"

// drawManualUIScale draws the manual UI-scale control. Applying the scale LIVE
// rescales the whole UI — this very slider included — so a live drag chases the
// cursor (the "super hard to use" report). Fix: the slider COMMITS ON RELEASE
// (preview the number while dragging, apply on mouse-up), and a row of preset
// chips gives a one-click path that never fights the cursor. Returns the new y.
func (a *App) drawManualUIScale(y int32) int32 {
	c := a.ctx
	pad := a.formX
	// While a drag is armed, feed the PENDING value back in: on the release
	// frame the Slider no longer tracks the mouse and echoes whatever it was
	// passed, so passing uiScalePct made the thumb (and worse, `next`) revert.
	shown := a.uiScalePct
	if a.uiScaleDragging {
		shown = a.uiScalePending
	}
	next := a.sliderRow(y, uiScaleSliderID, shown, config.UIScaleStepPercent,
		config.MinUIScalePercent, config.MaxUIScalePercent,
		"Zooms the WHOLE interface. Drag to preview, release to apply (applying mid-drag would rescale this slider under your cursor); or click a preset below.")
	switch {
	// The mouseDown guard is load-bearing: BeginFrame clears dragID only on a
	// frame that STARTS with the button up, so on the release frame itself
	// dragID still names this slider while mouseDown is already false. Without
	// the guard this case fired one extra frame and clobbered uiScalePending
	// with the un-applied base value — the release then committed a no-op and
	// the slider "snapped back to 100%" (the playtest report).
	case c.dragID == uiScaleSliderID && c.mouseDown:
		// Dragging: remember the value but DON'T rescale yet (that's the feedback loop).
		a.uiScaleDragging = true
		a.uiScalePending = next
	case a.uiScaleDragging:
		// Released → apply once, on the release frame.
		a.uiScaleDragging = false
		a.applyManualUIScale(a.uiScalePending)
	case next != a.uiScalePct:
		// Wheel or click-to-set (no drag) — discrete, no feedback loop.
		a.applyManualUIScale(next)
	}
	y += 30

	// Preset chips: a no-drag, one-click path (the easy way to land 150/175% on a
	// big screen). A click is instantaneous, so it can't chase the cursor.
	cx := pad + 130
	for _, p := range []int{100, 125, 150, 175, 200} {
		r := sdl.Rect{X: cx, Y: y, W: 46, H: btnH}
		if c.Button(r, fmt.Sprintf("%d%%", p)) {
			a.applyManualUIScale(p)
		}
		if a.uiScalePct == p {
			c.Border(r, ColAccent) // mark the active preset
		}
		cx += 52
	}
	return y + 34
}

// applyManualUIScale commits a manual UI scale: clamp, store, push to the kit
// (mouse unprojection) and persist. The single chokepoint for the slider's
// release, the wheel, and the preset chips.
func (a *App) applyManualUIScale(pct int) {
	pct = clampInt(pct, config.MinUIScalePercent, config.MaxUIScalePercent)
	if pct == a.uiScalePct {
		return
	}
	a.uiScalePct = pct
	a.ctx.SetUIScale(pct)
	a.d.Prefs.SetUIScale(pct)
}

// drawViewportSizeRow is the precise stage-size control. The AO stage art is
// 256×192, so the stage stays sharp at integer multiples and softens at the
// %-of-window sizes that fall between them. Preset chips pick the crisp
// multiples, a slider / mouse-wheel step through them, and an entry box takes an
// exact px width for power users; 0 px = "Fit" (size by the View % knob / edge
// drag, the prior behaviour). The entry buffer is written only on edit / commit
// / chip / slider — never reseeded per frame — so it stays freely editable.
// Settings screen only; off the courtroom hot path.
func (a *App) drawViewportSizeRow(y int32) int32 {
	c := a.ctx
	pad := a.formX
	cur := a.d.Prefs.ViewportExactWidth()
	set := func(px int) { // apply + resync the entry buffer to the clamped result
		a.d.Prefs.SetViewportExactWidth(px)
		cur = a.d.Prefs.ViewportExactWidth()
		if cur == 0 {
			a.vpExactBuf = ""
		} else {
			a.vpExactBuf = strconv.Itoa(cur)
		}
	}

	c.Label(pad, y+4, "Stage size:", ColText)
	x := pad + 130
	chip := func(label string, px int) {
		bw := c.TextWidth(label) + 16
		r := sdl.Rect{X: x, Y: y, W: bw, H: btnH}
		if c.Button(r, label) {
			set(px)
		}
		if cur == px {
			c.Border(r, ColAccent) // the active size
		}
		x += bw + 5
	}
	chip("Fit", 0)
	for m := 1; m <= 4; m++ {
		chip(fmt.Sprintf("%d×", m), m*config.ViewportArtW)
	}
	y += 30

	// Slider + wheel step in art-multiples (so every slider stop is a crisp size);
	// 0 = the leftmost stop = Fit.
	maxM := config.MaxViewportExactPx / config.ViewportArtW
	curM := cur / config.ViewportArtW
	track := sdl.Rect{X: pad + 130, Y: y + 5, W: 160, H: 16}
	newM := int(c.Slider("vpexact", track, int32(curM), int32(maxM)))
	if c.hovering(sdl.Rect{X: track.X - 6, Y: y, W: track.W + 12, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // hovered control owns the wheel — no page scroll
		newM = curM + int(c.wheelY)
	}
	if newM < 0 {
		newM = 0
	}
	if newM > maxM {
		newM = maxM
	}
	if newM != curM {
		set(newM * config.ViewportArtW) // 0 ⇒ Fit
	}

	// Exact-px entry for "specific numbers" (commit on Enter; mirrors the
	// window-size inputs — no live filtering, Atoi just ignores junk on submit).
	box := sdl.Rect{X: track.X + track.W + 14, Y: y, W: 64, H: fieldH}
	next, submitted := c.TextField("vpexactpx", box, a.vpExactBuf, "px")
	a.vpExactBuf = next
	if submitted {
		if v, err := strconv.Atoi(strings.TrimSpace(a.vpExactBuf)); err == nil {
			set(v)
		}
	}

	rx := box.X + box.W + 12
	if cur == 0 {
		c.LabelClipped(rx, y+4, a.formX+a.formW-rx, "Fit window (sized by the View % knob / edge drag)", ColTextDim)
	} else {
		hh := cur * config.ViewportArtH / config.ViewportArtW
		if cur%config.ViewportArtW == 0 {
			c.Label(rx, y+4, fmt.Sprintf("%d×%d  ·  %d× — sharp", cur, hh, cur/config.ViewportArtW), ColAccent)
		} else {
			c.Label(rx, y+4, fmt.Sprintf("%d×%d  ·  off-grid — may blur", cur, hh), ColTierYellow)
		}
	}
	y += 28
	c.LabelClipped(pad, y, a.formW, "Integer multiples of 256×192 stay sharp; for pixel-perfect scaling also turn Smooth texture scaling off.", ColTextDim)
	return y + 20
}

// previewDelayRow draws the sprite-preview hover dwell as a draggable slider
// with a seconds readout (the value is stored in milliseconds; a raw "5000"
// would be opaque). Bounds mirror the config clamp — SetPreviewHoverMs is
// authoritative — and the result snaps to the half-second grid.
func (a *App) previewDelayRow(y int32, ms int) int {
	c := a.ctx
	pad := a.formX
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
		cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", dialog)
		winexec.Hide(cmd) // GUI-subsystem build: no empty PowerShell window (the dialog still shows)
		out, err := cmd.Output()
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
