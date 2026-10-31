package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
)

type settingsState struct {
	mountInput string
	loaded     bool
	statusLine string
	// fpsBufs backs the active, idle and unfocused rows' number entries; reseeded while unfocused, applied on Enter.
	fpsBufs [3]string
	// memBufs backs the number fields of the memory and scale slider rows; reseeded while unfocused, applied on Enter.
	memBufs [5]string
	// logDepthBuf backs the "IC log depth" number entry; reseeded while unfocused, applied on Enter.
	logDepthBuf string
	tab         int                    // active settings tab (index into settingsTabNames)
	tabScroll   [numSettingsTabs]int32 // per-tab page scroll (each tab remembers its position)
	search      string                 // settings search query (jumps to the matching tab)
	// scrollToSection is a lowercased query held one frame after a search jump, for the first matching section card.
	scrollToSection string
	// index is the all-tabs row list for search, built once per search session by a clipped-away, input-blocked draw.
	index      []settingsIndexRow
	indexBuilt bool
	flashTab   int
	flashY     int32
	flashUntil time.Time

	// callAddInput is an empty field, not preloaded: the words render as removable rows below it.
	callAddInput string

	// friendInput is reloaded when friendKey changes, since the friend list is per server.
	friendInput string
	friendKey   string

	// ignoreInput is reloaded per server likewise, so you can un-ignore a player who has left.
	ignoreInput string
	ignoreKey   string

	// fontInput is the font override chain, semicolon-separated.
	fontInput  string
	fontLoaded bool

	// errorSpriteInput is the custom missing-sprite path, seeded once from the pref (errorSpriteLoaded).
	errorSpriteInput  string
	errorSpriteLoaded bool

	// winWInput and winHInput are seeded from the live size and again after a preset or fit.
	winWInput, winHInput string
	winLoaded            bool

	// importArmed routes the next dropped .json into ImportSettings.
	importArmed bool
	// presetName is the "Save as preset" name buffer (setting presets).
	presetName string

	// Macro editor buffers: name, captured key, |-separated lines.
	macroName  string
	macroKey   string
	macroLines string
	// icPhrase is the IC quick-phrase line to bind a key to.
	icPhrase string

	// themeBindKey is the theme-binding picker, sharing the login section's server list cache.
	themeBindKey string

	// Login section: the picked server and its credential buffers, for any known server, connected or not.
	loginKey      string
	loginUser     string
	loginPass     string
	loginAuto     bool
	loginLoaded   bool
	loginNames    []string // picker cache (WebSocketURL allocates)
	loginKeys     []string
	loginSrvCount int
	loginSrvFor   string

	// Theme picker: scanning runs on a goroutine, landing on themeRes (hard rule 2).
	themeName string
	themeDir  string
	themeList []string
	themeRes  chan themeScan
	themeBusy bool
	// themeScanUserWant remembers a user-asked rescan across a scan in flight, which scanThemes would drop.
	// Set on every user call, cleared only by the landing that acts on it. Render thread only (hard rules 1 and 8).
	themeScanUserWant bool
	// themeDDWasOpen detects the dropdown opening, so the catalog rescans once per open, not while it stays open.
	themeDDWasOpen bool
	// themeCountText memoizes the "(N found)" line on its count; see themeFoundText.
	themeCountText string
	themeCountFor  int
	// themeRows memoizes the strings drawThemeCatalogRows draws.
	themeRows themeRowText
	// themeArt memoizes the theme-art budget bar's label.
	themeArt themeArtBarText

	// Theme editor tab state, read once by the act (settingsthemeeditor.go). Render thread only.
	//
	// newThemeName is the name field's live text; empty uses the placeholder.
	newThemeName string
	// newThemeTemplate is the picked starting point; the zero value, templateBlank, has no prerequisite.
	newThemeTemplate themeCreateTemplate
	// presetLayout and presetStyle index the two preset axes, in the same order as the editor's gallery.
	presetLayout, presetStyle int
	// presetAxisNames memoizes the dropdowns' two label slices on the library pointer, not rebuilt per frame.
	presetAxisNames presetAxisNameCache

	// Theme folder picker: dialog output and resolved drops land here from goroutines, never stat on the render thread.
	folderRes  chan string
	browseBusy bool
	// mountPickRes carries the Asset source folder dialog's outcome; mountPickBusy keeps it to one dialog (hard rule 4).
	mountPickRes  chan mountPick
	mountPickBusy bool

	// ioRes carries one-line results of off-thread file ops back to the status line.
	ioRes chan string

	// fmtOrderText caches each asset type's probe list, parallel to config.TypeNames (assets.AssetType order).
	// Rebuilt only when the format generation moves; fmtOrderOK separates never built from generation 0.
	fmtOrderText []string
	fmtOrderGen  uint64
	fmtOrderOK   bool
}

// themeScan is one result: theme names found, the normalized root, and an auto-pick when the path was a single theme.
type themeScan struct {
	names    []string
	root     string
	pickName string
	// in is the folder this scan was issued for, before normalizing, so pollThemeScan can recognise a stale result.
	// Comparing res.root would misfire: root and the configured folder legitimately differ after normalizing.
	in string
	// fromUser marks a scan the user asked for; only it may auto-resize, or opening Settings would resize the window.
	fromUser bool
}

var settings = settingsState{
	themeRes:     make(chan themeScan, 1),
	folderRes:    make(chan string, 1),
	mountPickRes: make(chan mountPick, 1),
	ioRes:        make(chan string, 1),
}

// numSettingsTabs sizes the per-tab scroll array; keep it equal to len(settingsTabNames).
const numSettingsTabs = 14

var settingsTabNames = [numSettingsTabs]string{
	"General", "Theme", "Theme editor", "Assets", "Formats", "Audio", "Chat", "Account", "Hotkeys", "Studio", "Data", "Voice", "Power user", "Reset",
}

// Tab indices, in settingsTabNames order.
const (
	tabGeneral = iota
	tabTheme
	// tabThemeEditor owns authoring: making a theme, opening the editor, taking a bundle.
	// It sits right after Theme, which keeps selection, because the two read as a pair in the sidebar.
	tabThemeEditor
	tabAssets
	// tabFormats owns every control that decides which file type is asked for; beside Assets, where people look.
	tabFormats
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

// settingsSearchKeywords maps each tab to terms the search box matches, including every section title.
// Keep terms lowercase; when adding a settings section, add its title here.
var settingsSearchKeywords = [numSettingsTabs][]string{
	tabGeneral: {
		// Identity, Display & behaviour, Application, Log colours, Stage, Scale & text size, Window, Extras box, Fonts.
		"identity", "showname", "ooc name", "default showname", "force char names", "anti-impersonation",
		"your profile", "profile", "character profile", "pronouns", "bio", "tagline", "theme song", "card",
		"display", "behaviour", "behavior", "animation", "reduce motion", "accessibility",
		"sprite style", "recolour", "recolor", "tint", "glow", "opacity", "hide sprite styles", "hide other",
		"emote button", "favourite emotes", "favorite emotes", "fav emotes",
		// The table is scanned in tab order with substring matching, so this tab, with four preview settings,
		// takes bare "preview"; Theme keeps the qualified "theme preview".
		"preview", "sprite preview", "hover preview", "preview box", "pin preview", "pinned preview", "preview pin",
		"application", "streamer mode", "debug", "performance", "perf hud", "fps", "notify ooc", "unread badge",
		"log colours", "log colors", "selection highlight", "highlight colour", "name colour", "name colours",
		"stage", "desk", "hide desk", "scale", "text size", "ui scale", "dpi", "zoom",
		"window", "fullscreen", "window size", "resolution", "extras box", "extras", "tear off",
		"fonts", "font", "cjk", "dyslexia", "dyslexic", "emoji", "smooth scaling", "tabs", "server tabs", "max tabs",
		// The system-font toggle is on this tab, under Fonts.
		"system fonts", "installed fonts", "use fonts installed",
	},
	tabTheme: {
		// Theme, Layout & fit, Layout presets, Lobby, Preview & binding.
		"emote icon spacing", "emote spacing", "icon spacing", "emote gap", "spacing", "gap",
		"emote grid", "emote icons",
		"theme", "theme picker", "chatbox", "skin", "default theme",
		// "theme preview", not bare "preview", which General takes above.
		"layout", "fit", "courtroom design", "lobby", "theme preview", "bind", "binding",
		"layout presets", "preset", "presets", "save layout", "stage preset", "theater", "theatre",
		// The table matches forward in tab order, so a term here takes every later tab's query inside it: not "rescan"
		// (Assets) nor anything containing "import" or "portable" (Data).
		"subtheme", "subthemes", "theme variant", "variant",
		"themes folder", "my themes", "where are my themes", "open themes folder",
		"reload themes", "find themes", "theme badge",
		// Anchored on "themes" or a word no later tab claims. A long term containing a later tab's short query
		// steals it: nothing with "download" (Assets), "source repository" ("source" is Assets') or
		// "new themes" ("new" is Chat's).
		"get themes", "more themes", "free themes", "extra themes",
		"where to get themes", "drop-in themes", "franchise themes",
		"github", "theme repository",
		// The theme editor's terms are on tabThemeEditor below.
	},
	tabThemeEditor: {
		// New theme, The editor, Bring a theme in. Excluded because a later tab owns the word:
		// "export" and "import" (Data, Studio), "layer" (Assets; hence the noun "element"), "custom" (Audio, Assets),
		// "maker" (Studio), "scan" (Assets' "rescan"), "order" and "list" (Formats, Account), "new" (Chat),
		// "download" and "source" (Assets), "grid" and "glow" (General).
		"theme editor", "editor", "make a theme", "build a theme", "create a theme",
		"theme builder", "theme creator", "edit theme", "edit a theme", "edit my theme",
		"start a theme", "from scratch", "blank theme", "template", "templates",
		"theme template", "copy a theme", "copy for editing", "duplicate a theme",
		"edit the layout", "move things around", "design my own theme", "authoring",
		"decorate", "decoration", "element", "elements", "stacking", "inspector",
		"undo", "shapes", "masks", "animated background", "background art",
		"split screen", "procedural", "halftone", "generator",
		// "upload" is what people type. Not "webp" or anything containing it: reserved for Studio's video export.
		"add an image", "upload an image", "upload image", "add art", "picture",
		"animated image", "theme art", "theme image",
		"live preview", "live", "freeze the preview", "preview while editing",
		// Taking in what a friend sent, and handing one back from the editor's header.
		"aotheme", "theme bundle", "share a theme", "send a theme", "give a theme",
	},
	tabAssets: {
		// Predictive prefetch, Missing sprites, Asset source, Downloader, Cache.
		"predictive prefetch", "prefetch", "aggressiveness", "speculative", "preload",
		"local assets", "local", "mount", "downloader", "download",
		// Keep terms specific, since a broad word would take the query; not bare "stream" (General's "streamer mode").
		"asset source", "layered", "layer", "pack", "content pack", "zip", "override",
		"rescan", "folders", "my folders",
		"cache", "disk cache", "disk", "zstd", "clear cache",
		"loop preanim", "preanimation", "preanim loop", "loop preanimations",
		"placeholder", "missing sprite", "missingno", "missing character", "error sprite", "custom error sprite", "404",
	},
	tabFormats: {
		// How formats work, This server, Automatic detection, Formats per asset type, Fallbacks, Learned formats.
		// Absent because a later tab owns the word: "blip", "volume" (Audio); "ignore", "callword" (Chat);
		// "password" (Account); "webp", "gif" (Studio); "export", "import" (Studio, Data);
		// "extensions.json" (its ".json" would take Data's "json").
		"image format", "file type", "file types", "extension", "extensions", "manifest",
		"format profile", "server format profile", "per-server format", "pin format",
		"probe", "probing", "one probe", "probe order", "format order", "which format",
		"fallback", "fallbacks", "legacy format", "legacy audio", "audio format", "sound format",
		"autodetect", "auto-detect", "detect formats", "desk format",
		"png", "apng", "avif", "jpg", "jpeg", "opus", "ogg", "mp3", "wav",
		"learned format", "learned formats", "learned", "clear learned",
		"emotes not loading", "assets not loading", "images not loading", "art not loading",
		"stuck images", "repeated images", "same image", "nothing loads",
	},
	tabAudio: {
		// Volume, music ducking, custom music streaming.
		"audio", "volume", "master volume", "music volume", "sfx volume", "blip volume", "blip",
		"alert volume", "music ducking", "duck",
		"stream music", "disable music", "stop fetching music", "custom music",
		"music across tabs", "tab music", "keep music playing", "music continuity", "keep music",
	},
	tabChat: {
		// Text & typing, Chat log, Case alerts, Callwords, Do Not Disturb, Messages & connection, Sound effects,
		// Music history, Friends, Ignored players, Mod tools.
		"text", "typing", "text crawl", "text stay", "text speed", "chat limit", "catch up",
		"chat log", "ic log", "timestamps", "log", "song url", "song link", "full link", "music url",
		// Not bare "smooth", which General's "smooth scaling" owns, nor "scrolling", which contains Studio's "rolling".
		"log scroll", "smooth log scroll", "scroll", "jump to newest", "auto scroll", "autoscroll",
		"case alerts", "casing", "case", "callword", "callwords", "ping", "alert",
		"do not disturb", "dnd", "messages", "connection", "auto reconnect", "reconnect", "disconnect confirm",
		"sound effects", "sfx", "mute sfx", "music history", "jukebox history",
		"friends", "friend", "nickname", "friend colour", "ignored players", "ignore", "block",
		"mod tools", "moderator", "modcall", "ipid",
	},
	tabAccount: {
		// Login, Master list, Discord; TLS and Asset Origin are on Power user.
		"login", "password", "username", "credential", "auto login",
		"master list", "server list", "discord", "presence", "rich presence",
	},
	tabHotkeys: {
		// Hotkeys, Macros, IC quick-phrases.
		"hotkey", "hotkeys", "keybind", "keybinding", "shortcut",
		"macro", "macros", "ic quick", "quick phrase", "phrase",
	},
	tabData: {
		// Your settings file, Back up / move, Other data.
		"data", "settings file", "config folder", "config location", "open config", "open settings file",
		"asset_preferences", "where are my settings", "appdata", "export", "import", "backup", "portable", "usb", "move to another pc", "json",
	},
	tabVoice: {
		// Microphone, Test microphone, Output, Push-to-talk.
		"voice", "voice chat", "microphone", "mic", "input device", "recording device", "capture",
		"test microphone", "mic test", "sidetone", "hear myself", "level meter", "meter",
		"output volume", "speaker", "nyathena", "vc", "ptt", "talk", "push-to-talk", "push to talk",
	},
	tabStudio: {
		// Scene recording, Instant replay, Scene maker, Recordings, Replay playback, Export to GIF / WebP.
		"studio", "scene recording", "record", "recording", "instant replay", "clip", "rolling buffer",
		"scene maker", "maker", "aorec", "recordings", "replay", "replay playback", "playback speed",
		"export", "gif", "webp", "video", "mp4", "webm", "movie", "frame rate", "quality", "scene", "capture", "archive",
	},
	tabReset: {
		// Reset / clear data.
		"reset", "factory reset", "reset to defaults", "restore defaults", "wipe", "fresh install",
		"clear settings", "clear data", "defaults",
	},
	tabPowerUser: {
		// TLS, Asset Origin, character-folder casing, Proxy, and the render and GPU knobs.
		"power user", "advanced", "expert",
		"tls", "ssl", "certificate", "cert", "validate certificate", "self-signed", "wss", "verify", "security",
		"origin", "cors", "referer", "asset origin", "origin header", "stream from base",
		"casing", "capital", "capitalize", "capitalise", "uppercase", "lowercase", "character folder", "folder case",
		// Someone looking for this usually cannot connect, so include the terms they might type.
		"proxy", "socks", "socks5", "vpn", "tunnel", "corporate network", "work network",
		"wpad", "proxy script", "pac file", "system proxy", "no proxy", "bypass",
		// Not bare "pac": it is a substring of General's "opacity" and would take that query.
	},
}

// settingsSearchMatch returns the first tab whose name or keywords contain the query, or -1.
// Forward only: a reverse match would let "keybind" hit the short "bind".
func settingsSearchMatch(query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return -1
	}
	// A tab-name match wins.
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

// settingsIndexRow is one collected row for search: its trimmed label, tab, and y offset at zero scroll.
type settingsIndexRow struct {
	label string
	tab   int
	y     int32
}

// settingsIndexCap bounds the collected index (hard rule 4); past it the collector stops.
const settingsIndexCap = 600

// diskCacheBudgetStepMiB snaps the disk-cache-limit slider to a grid; exact bytes do not matter at this range.
const diskCacheBudgetStepMiB = 128

// diskCacheBudgetLabel formats a positive cap in MiB as "N MiB" below 1 GiB and "N.N GiB" above; 0 is the caller's.
func diskCacheBudgetLabel(mib int) string {
	if mib >= 1024 {
		return fmt.Sprintf("%.1f GiB", float64(mib)/1024)
	}
	return strconv.Itoa(mib) + " MiB"
}

func (a *App) drawSettings(w, h int32) {
	c := a.ctx
	if a.showReset { // factory-reset pop-up owns the screen + input this frame
		a.drawResetConfirm(w, h)
		return
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)

	if !settings.loaded {
		settings.themeName, settings.themeDir = a.d.Prefs.Load().Theme()
		if settings.themeName == "" {
			settings.themeName = theme.DefaultThemeName
		}
		settings.loaded = true
		a.scanThemes(false) // automatic: opening Settings is not a theme pick (#35)
		// Opening the screen asks for a catalog, floored by themeScanMinInterval so bouncing does not rescan.
		a.requestThemeCatalog(themeScanOnOpen)
	}
	// The window regained focus: one off-thread os.Stat of the write root, rescanning only if its mtime moved.
	if a.consumeThemeFocusStat() {
		a.requestThemeCatalog(themeScanOnFocus)
	}

	// While a blocking modal is open the whole screen behind it takes no clicks. Set before the header widgets draw and
	// released right before the modals draw, so their own widgets work.
	// The wizard's step 1 opens the browser on top of itself.
	if demoBrowser.open || contentPanel.open || baseWizard.open {
		c.modalOn = true
	}

	// These run regardless of the active tab, so async results land and drops are honored from any tab.
	a.pollThemeScan()
	a.pollFolderPick()
	a.pollMountPick()
	// baseWizardTakesDrop consumes the drop: an open wizard gets first refusal, or the folder becomes the theme root.
	if c.dropped != "" && !a.baseWizardTakesDrop(c.dropped) {
		// The classification is shared with HandleFileDrop (dropclaim.go): a recording or bundle must not also repoint
		// the theme folder at its parent. Anything unclaimed still points the theme folder.
		switch settingsDropAction(claimDroppedFile(c.dropped, settings.importArmed)) {
		case settingsDropImportSettings:
			settings.importArmed = false
			importSettingsAsync(a.d, c.dropped)
		case settingsDropIgnore:
			// HandleFileDrop is the single owner of dropped recordings, theme bundles and theme fonts.
		case settingsDropRepointThemeRoot:
			resolveDroppedFolder(c.dropped)
		}
	}
	select {
	case line := <-settings.ioRes:
		settings.statusLine = line
	default:
	}

	// Header band: content starts below the app-chrome band (chrometop.go).
	hdrY := a.topChromeH() + pad
	c.Heading(pad, hdrY, "Settings", ColText)
	// With a term typed, the page lists every matching setting across tabs; click or Enter jumps to that row.
	q, committed := c.TextField("settsearch", sdl.Rect{X: pad + 130, Y: hdrY + 2, W: 240, H: fieldH}, settings.search, "Search settings…")
	settings.search = q
	if strings.TrimSpace(q) != "" {
		c.LabelClipped(pad+382, hdrY+6, w-pad-382-110, "showing every match — click one (or Enter for the top hit)", ColTextDim)
	}
	if c.Button(sdl.Rect{X: w - 90 - pad, Y: hdrY, W: 90, H: btnH}, "Back") {
		a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
		_ = a.d.Prefs.SaveNow() // Settings-Apply synchronous flush
		a.screen = a.prevScreen
		return
	}
	contentTop := hdrY + settContentTop
	c.Fill(sdl.Rect{X: 0, Y: contentTop - 10, W: w, H: 1}, ColPanelHi) // hairline under the header

	// Left sidebar.
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

	// formX and formW define where the section and row helpers draw, so every box lands inside this card.
	cardX := pad + settSidebarW + settSidebarGap
	cardW := (w - scrollBarW - pad) - cardX
	cardW = value.Clamp(cardW, settMinCardW, settMaxCardW)
	a.formX = cardX + settCardPadX
	a.formW = cardW - 2*settCardPadX
	// No chrome label may draw past the card: Label, LabelClipped and Checkbox all consult this limit.
	// Popped by defer so early returns cannot truncate the next screen's labels, and again when the card's clip closes.
	// Popping twice with the same saved value is idempotent.
	cardLabelLimit := c.pushRowLabelLimit(a.formX + a.formW)
	defer c.popRowLabelLimit(cardLabelLimit)

	viewH := h - contentTop - pad
	scroll := &settings.tabScroll[settings.tab]

	// Clip below the header and fill the card surface; settingsSection punches page-coloured gaps between cards.
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: cardX, Y: contentTop, W: cardW, H: viewH})
	c.Fill(sdl.Rect{X: cardX, Y: contentTop, W: cardW, H: viewH}, cardColor())

	// With a query, the card becomes a list of matching settings. Skipped while the .demo browser is open: this branch
	// returns early, past the modal release at the end, which would leave c.modalOn set and freeze the UI.
	if q := strings.ToLower(strings.TrimSpace(settings.search)); q != "" && !demoBrowser.open {
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
	// Pulse a band around the landed row after a search jump.
	if settings.flashUntil.After(time.Now()) && settings.flashTab == settings.tab {
		fy := contentTop - *scroll + settings.flashY
		c.Border(sdl.Rect{X: cardX + 4, Y: fy - 5, W: cardW - 8, H: 30}, ColAccent)
		c.Border(sdl.Rect{X: cardX + 5, Y: fy - 4, W: cardW - 10, H: 28}, ColAccent)
	}
	if settings.statusLine != "" {
		c.Label(a.formX, y+6, settings.statusLine, ColAccent)
		y += 28
	}
	if y < contentTop+viewH {
		fy := y
		if fy < contentTop {
			fy = contentTop
		}
		c.Fill(sdl.Rect{X: cardX, Y: fy, W: cardW, H: contentTop + viewH - fy}, ColBackground)
	}
	c.popClip(clipPrev, clipHad)
	// Everything below is full-window chrome, so release the card's right edge rather than shorten their labels.
	c.popRowLabelLimit(cardLabelLimit)

	contentH := (y + *scroll) - contentTop + pad
	// !c.modalOn: this wheel consume is unconditional, so the page must not scroll behind the .demo browser.
	if !c.ctrlHeld && !c.wheelTaken && !c.modalOn {
		*scroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: w - scrollBarW - 2, Y: contentTop, W: scrollBarW, H: viewH}
	*scroll = c.VScrollbar("settscroll", track, *scroll, contentH, viewH)
	settings.scrollToSection = "" // one-shot: consumed by the matching section this frame, else dropped
	// Fixed position outside the scrolled clip, so it cannot scroll away.
	if a.warnActive() {
		c.LabelClipped(pad, h-24, w-2*pad, a.warnLine, ColDanger)
	}

	// These draw last with input released, so their widgets work; the content panel follows the browser,
	// because a pick closes the browser and opens the panel in the same click.
	if demoBrowser.open || contentPanel.open || baseWizard.open {
		c.modalOn = false
	}
	// Before the browser: step 1 opens one on top of itself, and the topmost must draw last.
	a.drawBaseWizard(w, h)
	a.drawDemoBrowser(w, h)
	a.drawContentPanel(w, h)
}

// drawSettingsTabBody dispatches one tab's rows, for the page draw and the search collector.
func (a *App) drawSettingsTabBody(tab int, y, w, h int32) int32 {
	switch tab {
	case tabGeneral:
		y = a.drawSettingsGeneral(y, w)
	case tabTheme:
		y = a.drawSettingsTheme(y, w, h)
	case tabThemeEditor:
		y = a.drawSettingsThemeEditor(y, w)
	case tabAssets:
		y = a.drawSettingsAssets(y, w)
	case tabFormats:
		y = a.drawSettingsFormats(y, w)
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

// buildSettingsIndex draws every tab once, input blocked and clipped away, to collect rows through Ctx.onRow.
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

// drawSettingsSearchResults lists rows matching q; a click, or Enter for the first, jumps there and arms the flash.
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
		// No row label matched, but the keyword table may know the concept.
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
	// labelBtnPadX is the horizontal padding a label-measured button adds around its text, half each side.
	labelBtnPadX = 14
	// designSizeHintGap separates the design-size button from its hint; the hint's x and clip width derive from it.
	designSizeHintGap = 10
)

// settingsSection punches a gap above the card and draws its title and hairline, returning the first row's y.
// The w param is unused; the region comes from a.formX and a.formW.
func (a *App) settingsSection(y, w int32, title string) int32 {
	c := a.ctx
	if c.onRow != nil {
		c.onRow("§ "+title, y+settCardTopPad+2) // gather-search: sections are jumpable headers
	}
	// Search jump: the first section titled with the query scrolls to the top, one-shot, clamped next frame.
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

// formW2 returns the value for a helper's w parameter, so its width math resolves inside the content card.
func (a *App) formW2() int32 { return a.formX + a.formW + scrollBarW }

// fpsSettingRow draws a frame-rate row: slider, exact-number entry (Enter applies) and no-limit toggle.
// zeroValue is what a slider bottom or typed 0 stores: config.FPSOff for Idle and Background, the min cap for Active.
func (a *App) fpsSettingRow(y int32, id, label string, min, max, def, zeroValue int, get func() int, set func(int), buf *string, tip string) int32 {
	c := a.ctx
	pad := a.formX // settings helpers must shadow the form origin (drawSettings rebase)
	c.Label(pad, y+4, label, ColText)
	cur := get()
	// Compare against the value passed, so displaying an unlimited pref cannot write a concrete value back.
	passed := 0
	if cur > 0 {
		passed = cur
	}
	track := sdl.Rect{X: pad + 210, Y: y + 2, W: 170, H: 16}
	raw := int(value.Clamp(c.Slider(id, track, int32(passed), int32(max)), 0, int32(max)))
	nv := raw
	if raw == 0 {
		nv = zeroValue // bottom of the track: off (Idle/Background) or the min cap (Active)
	}
	c.Tooltip(track, tip)
	if raw != passed && nv != cur {
		set(nv)
		cur = get()
	}
	fieldID := id + "-num"
	fr := sdl.Rect{X: track.X + track.W + 8, Y: y, W: 52, H: btnH}
	// The getter maps 0 to the default, so cur is FPSUnlimited, FPSOff or a rate; "off" shows as 0, unlimited as blank.
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
	// No limit on or off; off returns to the default.
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

// numSliderRow renders a slider with -/+ nudges and an exact-value field; 0 is the off or default sentinel.
func (a *App) numSliderRow(y int32, id, label string, min, max, step, def int, get func() int, set func(int), buf *string, suffix, tip string) int32 {
	c := a.ctx
	pad := a.formX
	c.Label(pad, y+4, label, ColText)

	cur := get()
	track := sdl.Rect{X: pad + 210, Y: y + 2, W: 110, H: 16}
	raw := int(value.Clamp(c.Slider(id, track, int32(cur), int32(max)), 0, int32(max)))
	nv := raw
	if nv != 0 && nv < min {
		nv = 0 // below the floor -> the off/default sentinel
	}
	c.Tooltip(track, tip)
	if nv != cur {
		set(nv)
		cur = get()
	}

	minus := sdl.Rect{X: track.X + track.W + 6, Y: y, W: 20, H: btnH}
	if c.Button(minus, "-") {
		n := cur - step
		if n < min {
			n = 0 // below the floor -> the off/default sentinel
		}
		set(n)
		cur = get()
	}
	c.Tooltip(minus, "Decrease by "+strconv.Itoa(step)+suffix)

	plus := sdl.Rect{X: minus.X + minus.W + 4, Y: y, W: 20, H: btnH}
	if c.Button(plus, "+") {
		n := cur
		if n == 0 {
			n = min // sentinel -> the first real value
		} else {
			n += step
			if n > max {
				n = max
			}
		}
		set(n)
		cur = get()
	}
	c.Tooltip(plus, "Increase by "+strconv.Itoa(step)+suffix)

	fieldID := id + "-num"
	fr := sdl.Rect{X: plus.X + plus.W + 6, Y: y, W: 46, H: btnH}
	if c.focusID != fieldID {
		*buf = strconv.Itoa(cur)
	}
	var entered bool
	*buf, entered = c.TextField(fieldID, fr, *buf, suffix)
	if entered {
		if n, err := strconv.Atoi(strings.TrimSpace(*buf)); err == nil {
			if n < min {
				n = 0 // below the floor -> the off/default sentinel
			} else if n > max {
				n = max
			}
			set(n)
			cur = get()
		}
		c.focusID = "" // commit releases the field so it reseeds from the pref
	}

	var lbl string
	switch {
	case cur == 0:
		lbl = "off"
	case def > 0 && cur == def:
		lbl = "default (" + strconv.Itoa(def) + suffix + ")"
	default:
		lbl = strconv.Itoa(cur) + suffix
	}
	c.Label(fr.X+fr.W+8, y+4, lbl, ColTextDim)
	return y + 26
}

// settingsDesc draws a word-wrapped description at (pad, y) inside the card and returns the y past it.
func (a *App) settingsDesc(pad, y int32, text string, col sdl.Color) int32 {
	for _, ln := range a.ctx.WrapText(text, a.formW-8, 0) {
		a.ctx.Label(pad, y, ln, col)
		y += 16
	}
	return y
}

func (a *App) drawSettingsGeneral(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX // rebase every pad-relative box into the content card
	w := a.formW2()
	y = a.settingsSection(y, w, "Identity")
	// Write through to prefs, so a stale copy cannot overwrite names typed in the courtroom.
	c.Label(pad, y+4, "Showname:", ColText)
	shown := a.d.Prefs.Load().Showname
	// A non-Latin showname must render as real glyphs while typed; plain ASCII keeps the single-font path.
	snPrimary, snEmoji := a.icFieldFonts(shown)
	if next, _ := c.TextFieldEmoji("showname", sdl.Rect{X: pad + 130, Y: y, W: 240, H: fieldH}, shown, "your saved showname", snPrimary, snEmoji); next != shown {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.Showname = next })
	}
	// Seeds every new tab (blank sends a sticky AsyncAO<n>); the courtroom's OOC-name fields are tab-local.
	c.Label(pad+390, y+4, "OOC name:", ColText)
	savedOOC := a.d.Prefs.Load().OOCName
	onPrimary, onEmoji := a.icFieldFonts(savedOOC) // CJK-safe OOC name (ASCII stays fast path)
	if next, _ := c.TextFieldEmoji("oocdefault", sdl.Rect{X: pad + 480, Y: y, W: 200, H: fieldH}, savedOOC, "blank = AsyncAO<n>", onPrimary, onEmoji); next != savedOOC {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.OOCName = next })
		a.oocName = next // the ACTIVE tab adopts the new default right away
	}
	y += 38

	// A saved, global list, swapped in-game with keybinds; cleared only by a factory reset.
	c.Label(pad, y+4, "Presets:", ColText)
	var addNow bool
	saP, saE := a.icFieldFonts(a.shownameAdd) // CJK-safe preset entry (ASCII stays fast path)
	a.shownameAdd, addNow = c.TextFieldEmoji("shownameadd", sdl.Rect{X: pad + 130, Y: y, W: 240, H: fieldH}, a.shownameAdd, "type a name to save…", saP, saE)
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
	if presets := a.d.Prefs.Load().ShownameList(); len(presets) == 0 {
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
			// Arm a capture (pollShownameBind binds the next key); right-click clears the bind.
			bound := a.shownameKeyFor(name)
			// bindChipLabel, not "Key: "+bound: this namespace fires on Alt+<key> (bareBindKey, qol.go).
			keyLbl := bindChipLabel(bound, a.shownameBindFor == name, "press a key…", "Bind key")
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
			c.Tooltip(kr, "Bind a key to this showname — press "+bareBindChordPrefix+"that key in the courtroom to swap. Right-click to clear.")
			lbl, col := name, ColText
			if strings.EqualFold(active, name) {
				lbl, col = name+"   ← active", ColTierGreen
			}
			a.labelName(pad+194, y+1, 280, lbl, col) // CJK-safe saved preset name
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
	y = a.drawShapeSettings(y)     // A5 chrome SHAPE presets (sharp/rounded/pill)
	y = a.drawPartColorSettings(y) // per-part layout tints
	y += 8

	y = a.settingsSection(y, w, "Display & behaviour")
	// Streamer mode leads the section, because people need it fast before going live.
	streamer := a.d.Prefs.Load().StreamerModeOn
	if next := c.Checkbox(pad, y, "Streamer mode (masks OOC names + IPs in the log display, silences callword pings)", streamer); next != streamer {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.StreamerModeOn = next })
	}
	y += 26
	anims := a.d.Prefs.Load().PreferAnimated
	if next := c.Checkbox(pad, y, "Play animations (off = render first frames only; never affects network probes)", anims); next != anims {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				PreferAnimated =

				next

		})

	}
	y += 26
	reduce := a.d.Prefs.Load().ReduceMotionOn
	if next := c.Checkbox(pad, y, "Reduce motion (accessibility): stop the screen shake / realization flash (effect sounds still play)", reduce); next != reduce {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ReduceMotionOn = next })
		a.applyTimingToRoom() // push the flag to the live room
	}
	y += 26
	// Viewer opt-out of other players' sprite styles; reduce-motion only drops a received style's wobble and spin.
	hideStyles := a.d.Prefs.Load().HideSpriteStyles
	if next := c.Checkbox(pad, y, "Hide other players' sprite styles: show every character normally (ignore transmitted recolour, glow, spinning, wobble, motion paths and glitch)", hideStyles); next != hideStyles {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				HideSpriteStyles =

				next

		})

		if a.room != nil {
			a.room.HideSpriteStyles = next
		}
	}
	y += 26
	// Viewer opt-out of incoming reaction floats; sending your own is unaffected.
	hideReact := a.d.Prefs.Load().HideReactions
	if next := c.Checkbox(pad, y, "Hide other players' emoji reactions: don't float their reaction emoji over the stage (#2)", hideReact); next != hideReact {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				HideReactions =

				next

		})

	}
	y += 26
	emoteImgs := a.d.Prefs.Load().EmoteButtonImages
	if next := c.Checkbox(pad, y, "Image emote buttons (characters/<char>/emotions/button art — WebP by default, formats in Assets)", emoteImgs); next != emoteImgs {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EmoteButtonImages =

				next

		})

	}
	y += 26
	emoteCaps := a.d.Prefs.Load().EmoteCaptions
	if next := c.Checkbox(pad, y, "Emote-name captions (OFF by default): when a character has no button art, overlay the emote name on the fallback icon. Off keeps the buttons clean (just icons); on helps tell identical fallback icons apart.", emoteCaps); next != emoteCaps {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EmoteCaptions =

				next

		})

	}
	y += 26
	favStars := a.d.Prefs.Load().EmoteFavStars
	if next := c.Checkbox(pad, y, "Emote favourites (OFF by default): show a ★ on every emote button so you can star the ones you use, plus the ★ Favs filter. Off keeps the grid clean if you don't use it.", favStars); next != favStars {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EmoteFavStars =

				next

		})

		a.emoteFavRev++ // rebuild the view for the new state
	}
	y += 26
	favOnly := a.d.Prefs.Load().EmoteFavOnly
	if next := c.Checkbox(pad, y, "Show favourite emotes only (OFF by default): hides everything but the emotes you've starred. Click the ★ on an emote button to favourite it (per character). The classic grid also has a ★ Favs button.", favOnly); next != favOnly {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EmoteFavOnly =

				next

		})

		a.emoteFavRev++ // rebuild the visible list for the new filter state
	}
	y += 26
	favBox := a.d.Prefs.Load().FavEmoteBox
	if next := c.Checkbox(pad, y, "Favourite-emotes box (OFF by default): a small movable box of just your starred emotes as clickable buttons — press one to use that emote. Also opens with Ctrl+A (rebindable in the Controls tab).", favBox); next != favBox {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FavEmoteBox =

				next

		})

	}
	y += 30
	dbg := a.d.Prefs.Load().DebugOverlay
	if next := c.Checkbox(pad, y, "Debug overlay (live log of failures: missing assets, theme problems, unhandled server packets)", dbg); next != dbg {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DebugOverlay =

				next

		})

	}
	y += 26
	notifOOC := a.d.Prefs.Load().NotifyOnOOC
	if next := c.Checkbox(pad, y, "Count OOC in the unread tab badge (OFF by default): otherwise only IC chat lights up a background tab's \"(N)\" — so server auto-messages (hourly reminders, etc.) don't ping you", notifOOC); next != notifOOC {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				NotifyOnOOC =

				next

		})

	}
	y += 26
	fcn := a.d.Prefs.Load().ForceCharNames
	if next := c.Checkbox(pad, y, "Force character names (OFF by default): show everyone's CHARACTER name, not custom shownames — true-roleplay / anti-impersonation (casing)", fcn); next != fcn {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ForceCharNames =

				next

		})

		if a.room != nil {
			a.room.ForceCharNames = next // apply to the running session immediately
		}
	}
	y += 26
	// Hover preview of a character or emote button; this controls only the dwell, right-click previews always work.
	prev := a.d.Prefs.Load().SpritePreviewOn
	if next := c.Checkbox(pad, y, "Sprite hover-previews (ON by default): hovering a character or emote button shows the full-size sprite. Off only disables the hover pop-up — right-click still previews", prev); next != prev {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpritePreviewOn = next })
	}
	y += 26
	if prev {
		ms := a.d.Prefs.Load().PreviewHoverMillis()
		if next := a.previewDelayRow(y, ms); next != ms {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.PreviewHoverMs = next })
		}
		c.Label(pad+340, y+4, "how long to hover before the preview pops (default 5 s)", ColTextDim)
		y += 30
	}
	// Applies to hover and right-click previews, so it shows regardless of the toggle above.
	ph := a.d.Prefs.Load().PreviewHeightPx()
	c.Label(pad, y+4, "Preview box height:", ColText)
	phTrack := sdl.Rect{X: pad + 170, Y: y + 5, W: 120, H: 16}
	nph := config.MinPreviewHeightPx + int(c.Slider("previewheight", phTrack,
		int32(ph-config.MinPreviewHeightPx), int32(config.MaxPreviewHeightPx-config.MinPreviewHeightPx)))
	if c.hovering(sdl.Rect{X: pad, Y: y, W: 300, H: 26}) && c.wheelY != 0 {
		c.wheelTaken = true // a hovered control owns the wheel — no page scroll
		nph += int(c.wheelY) * 16
	}
	nph = value.Clamp(nph, config.MinPreviewHeightPx, config.MaxPreviewHeightPx)
	c.Label(pad+298, y+4, strconv.Itoa(nph)+" px", ColTextDim)
	c.Label(pad+360, y+4, "default size of the sprite preview pop-up (384 px default; the corner grip still resizes per session)", ColTextDim)
	if nph != ph {
		a.d.Prefs.SetPreviewHeightPx(nph)
	}
	y += 30
	// The box's pin button writes this same pref. A pinned animated preview holds the pacer at the active cap,
	// hence the note. The label says "pin button", not the emoji, which many chrome faces lack (U+1F4CC).
	pinned := a.d.Prefs.Load().PreviewPinned
	if next := c.Checkbox(pad, y, "Keep the sprite preview pinned: it stays open until you close it (the pin button on the box toggles this too; an animated preview keeps the client at full frame rate while it is up)", pinned); next != pinned {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PreviewPinned = next })
	}
	y += 26
	// Name-only tooltip on an emote button, independent of the sprite pop-up above.
	names := a.d.Prefs.Load().EmoteHoverNamesOn
	if next := c.Checkbox(pad, y, "Emote name tooltips (ON by default): hovering an emote button shows its name after a short delay", names); next != names {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EmoteHoverNamesOn =

				next

		})

	}
	y += 26
	altRow := a.d.Prefs.Load().DisableAltEmoteRow
	if next := c.Checkbox(pad, y, "Disable the Alt+1..9 emote number row (OFF by default): the digits pick the emote shown on the current page. Disable to make Alt+1..9 type digits instead.", altRow); next != altRow {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DisableAltEmoteRow =

				next

		})

	}
	y += 26
	if names {
		ems := a.d.Prefs.Load().EmoteHoverNamesMillis()
		if next := a.emoteNameDelayRow(y, ems); next != ems {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					EmoteHoverNamesMs =

					next

			})

		}
		c.Label(pad+340, y+4, "how long to hover before the name shows (default 5 s)", ColTextDim)
		y += 30
	}
	// Drag a character to move them, sticking per character until reset; off by default so a stray click cannot nudge.
	move := a.d.Prefs.Load().SpriteMoveOn
	if next := c.Checkbox(pad, y, "Let me drag character sprites to reposition them (OFF by default; right-click a sprite to reset it)", move); next != move {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteMoveOn = next })
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
	// Right-click a sprite to hide it for the session.
	hideRC := a.d.Prefs.Load().RightClickHideSprite
	if next := c.Checkbox(pad, y, "Right-click a sprite to hide it from the viewport (ON by default): confirms, then hides it for the session. Reshow all with the Reshow-hidden-sprites key (Controls tab).", hideRC); next != hideRC {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RightClickHideSprite = next })
	}
	y += 26
	if len(a.hiddenSprites) > 0 {
		if c.Button(sdl.Rect{X: pad + 20, Y: y, W: 200, H: btnH}, "Reshow hidden sprites") {
			a.reshowSprites()
		}
		c.Label(pad+232, y+5, fmt.Sprintf("%d sprite(s) hidden this session — un-hide them all", len(a.hiddenSprites)), ColTextDim)
		y += 32
	}
	hideDesk := a.d.Prefs.Load().HideDesk
	if next := c.Checkbox(pad, y, "Hide the courtroom desk (OFF by default): suppresses the foreground desk so the full character shows.", hideDesk); next != hideDesk {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				HideDesk =

				next

		})

	}
	y += 30
	y = a.settingsSection(y, w, "Application")
	upd := a.d.Prefs.Load().UpdateCheck
	if next := c.Checkbox(pad, y, "Check for updates on launch (one async check of GitHub Releases; shows the patch notes — off = no outbound call)", upd); next != upd {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.UpdateCheck = next })
	}
	y += 26
	alt := a.d.Prefs.Load().AutoLoginToast
	if next := c.Checkbox(pad, y, "Notify me when auto-login signs me in (toast + desktop notification — ON by default, so a mod knows they're logged in)", alt); next != alt {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AutoLoginToast =

				next

		})

	}
	y += 26
	tabCap := a.d.Prefs.Load().TabCap()
	if next := a.numberRow(y, "Max server tabs", tabCap, 1, 1, 99,
		"How many servers you can have open at once — each Join opens a tab (up to 99)."); next != tabCap {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.MultiTabCap = next })
	}
	c.Label(pad+270, y+4, "servers you can keep open at once — each is a live connection (default 6)", ColTextDim)
	y += 30
	restoreTabs := a.d.Prefs.Load().RestoreTabs
	if next := c.Checkbox(pad, y, "Reopen my server tabs on launch (OFF by default): remembers open servers on exit and reconnects them next time", restoreTabs); next != restoreTabs {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.RestoreTabs = next })
	}
	y += 30
	y = a.settingsSection(y, w, "Log colours")
	// Log-selection highlight colour: hue/saturation wheel, brightness slider and hex field.
	y = a.drawHighlightPicker(y, w)
	// Per-speaker name colours: a stable hash per name, with sliders and a preview.
	y = a.drawNameColorPicker(y, w)
	boldNames := a.d.Prefs.Load().BoldNamesOn()
	if next := c.Checkbox(pad, y, "Bold speaker names + timestamps (ON by default): renders the name prefix (and the IC-log time stamp) in the log and chatbox in bold for readability.", boldNames); next != boldNames {
		a.d.Prefs.SetBoldNames(next)
	}
	y += 28
	y = a.settingsSection(y, w, "Stage")
	slideOn := a.d.Prefs.Load().BgSlideshow
	if next := c.Checkbox(pad, y, "Background slideshow (OFF by default): when the courtroom is idle, cycle the stage through this server's backgrounds as ambiance", slideOn); next != slideOn {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				BgSlideshow =

				next

		})

	}
	y += 26
	if slideOn {
		secs := a.d.Prefs.Load().BgSlideshowSeconds()
		// Bounds match the config clamp; SetBgSlideshowSeconds is authoritative.
		if next := a.numberRow(y, "  Seconds per background", secs, 1, 3, 600); next != secs {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					BgSlideshowSecs =

					next

			})

		}
		c.Label(pad+270, y+4, "only while idle — a message instantly shows the real area background again", ColTextDim)
		y += 30
	}
	dragLay := a.d.Prefs.Load().DragLayout
	if next := c.Checkbox(pad, y, "Drag panel edges to resize the courtroom (ON by default): grab the viewport's right edge to make it bigger / the log smaller — uncheck for the +/− View/Text/MsgBox/Log/Input knob buttons instead", dragLay); next != dragLay {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DragLayout =

				next

		})

	}
	y += 26
	smooth := a.d.Prefs.Load().SmoothScaling
	if next := c.Checkbox(pad, y, "Smooth texture scaling (linear filtering; re-streams loaded images when toggled)", smooth); next != smooth {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SmoothScaling = next })
		// The filter applies at texture creation, so purge and let everything re-stream.
		_ = c.Ren.SetDefaultTextureScaleMode(TextureScaleMode(next))
		a.d.Store.Purge()
		// Purge destroyed the pinned shape masks synchronously; drop the Ctx pointers before anything shaped draws.
		a.invalidateShapeMasks()
		c.purgeTextCache()
		a.applyThemeAsync()
		// Purge also destroyed the pinned missingno placeholder; re-pin the default and re-kick a custom image
		// so the missing-sprite fallback survives a filtering swap (missingno.go).
		a.uploadEmbeddedMissingno()
		if p := a.d.Prefs.Load().ErrorSpriteFile; p != "" {
			a.applyErrorSprite(p)
		}
		settings.statusLine = "Re-streaming textures with new filtering."
	}
	y += 26
	// Separate from the client-wide toggle above; resolved at draw time from the page memo, so it needs no purge.
	spriteScale := a.d.Prefs.SpriteScalingMode()
	c.Label(pad, y+4, "Sprite filtering:", ColText)
	if next, changed := c.Dropdown("spritescale",
		sdl.Rect{X: pad + spriteScaleDDX, Y: y, W: spriteScaleDDW, H: fieldH},
		spriteScalingLabels[:], spriteScale); changed {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteScaling = next })
		spriteScale = config.ClampSpriteScaling(next)
		settings.statusLine = "Sprite filtering: " + spriteScalingLabels[spriteScale]
	}
	y += 26
	c.Label(pad, y+2, spriteScalingHints[spriteScale], ColTextDim)
	y += 24
	y = a.settingsSection(y, w, "Scale & text size")
	scaleAuto := a.d.Prefs.Load().UIScaleAutoOn
	scaleAutoLabel := "Auto UI scale (fits the window + display DPI)"
	if a.detectedScalePct > 0 {
		scaleAutoLabel = fmt.Sprintf("Auto UI scale — fits the window + display DPI (now: %d%%)", a.detectedScalePct)
	}
	if next := c.Checkbox(pad, y, scaleAutoLabel, scaleAuto); next != scaleAuto {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.UIScaleAutoOn = next })
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

	// Scales log and chatbox text without zooming the art; same persisted scales as the in-courtroom Ctrl+wheel zoom.
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
	// Only affects the flat fallback skin; a theme chatbox keeps its own art.
	op := a.d.Prefs.Load().ChatboxOpacity
	if v := a.sliderRow(y, "Chatbox opacity %", op, 5, config.MinChatboxOpacity, config.MaxChatboxOpacity,
		"How see-through the chatbox panel is: 0 = text only, 100 = solid. Only the flat fallback panel — a theme's own skin keeps its art."); v != op {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ChatboxOpacity =

				v

		})

	}
	y += 34
	tint := a.d.Prefs.Load().ChatboxTint
	if next := c.Checkbox(pad, y, "Per-character chatbox tint (OFF by default): the chatbox takes a hint of each speaker's colour", tint); next != tint {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ChatboxTint =

				next

		})

	}
	y += 26

	y = a.settingsSection(y, w, "Window")
	// A window bigger than the monitor cannot be dragged smaller; F11 and Fit to screen are the escapes. Render thread.
	full := a.d.Prefs.Load().WindowFull
	if next := c.Checkbox(pad, y, "Fullscreen (borderless) · F11 toggles", full); next != full {
		a.applyFullscreen(next)
	}
	y += 28
	if a.d.Prefs.Load().WindowFull {
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
			bw := c.TextWidth(p.label) + labelBtnPadX
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
		// Snap to the size the active theme was drawn for; themeDesignSizeOffer also needs the themed layout drawn.
		// The label shows the applied size: canvas + themeDesignWindowInset, as the canvas sits below the chrome band.
		if _, _, ok := a.themeDesignSizeOffer(); ok {
			tw, th, _ := a.themeDesignWindowTarget()
			label := fmt.Sprintf("Theme's design size (%d×%d)", tw, th)
			bw := c.TextWidth(label) + labelBtnPadX
			if c.Button(sdl.Rect{X: pad, Y: y, W: bw, H: btnH}, label) {
				a.applyThemeDesignWindowSize()
				settings.winLoaded = false
			}
			// The label carries the size, so drop the hint rather than draw it into a negative width.
			if hintW := w - pad - bw - designSizeHintGap - scrollBarW; hintW > 0 {
				c.LabelClipped(pad+bw+designSizeHintGap, y+4, hintW,
					"this theme's canvas plus the menu bar and tab strip above it — the courtroom then draws 1:1, like the stock AO2 client", ColTextDim)
			}
			y += btnH + 10
		}
	}

	y = a.settingsSection(y, w, "Extras box")
	// Extras box appearance: a hex colour per element (blank is stock), a swatch, and a Background to Gradient fade.
	c.Label(pad, y+4, "Colours:", ColTextDim)
	c.LabelClipped(pad+150, y+4, w-pad-150-scrollBarW, "hex like 78aaff — blank = default · live on the open box", ColTextDim)
	y += 24
	exBg, exBg2, exBorder, exTitle, exText, exGrad := a.d.Prefs.Load().ExtrasBoxStyle()
	cur := [5]string{exBg, exBg2, exBorder, exTitle, exText}
	next := cur
	for i, label := range [5]string{"Background", "Gradient ↓", "Border", "Title bar", "Text"} {
		c.Label(pad+16, y+4, label, ColTextDim)
		swatch := ColPanel
		if c, ok := parse.HexColor(next[i]); ok {
			col := sdl.Color(c)
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
	// The scan reads the machine's own fonts, the only text path differing per OS,
	// so a player seeing wrong text can take it out of the picture in one click.
	census := a.d.Prefs.Load().FontCensus
	if next := c.Checkbox(pad, y, "Use fonts installed on this computer (ON by default): lets text in scripts the bundled fonts don't cover borrow a font from your system. Turn off if text renders wrongly or goes missing — the client then uses only the fonts it ships with.", census); next != census {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FontCensus =

				next

		})

	}
	y += 26
	dys := a.d.Prefs.Load().DyslexiaFont
	if next := c.Checkbox(pad, y, "Dyslexia-friendly font (bundled OpenDyslexic) — applies to the chat & log text", dys); next != dys {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DyslexiaFont =

				next

		})

		a.applyFontConfig()
		if next {
			settings.statusLine = "Dyslexia-friendly font on (OpenDyslexic)."
		} else {
			settings.statusLine = "Dyslexia-friendly font off."
		}
		dys = next
	}
	y += 28

	// A chain of TTF/TTC paths; first covering font per line wins. Saved even while the dyslexia font overrides it.
	c.Label(pad, y+4, "Custom font:", ColText)
	if !settings.fontLoaded {
		settings.fontInput = a.d.Prefs.Load().FontOverridePaths
		settings.fontLoaded = true
	}
	var fontCommit bool
	settings.fontInput, fontCommit = c.TextField("fontpaths", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH},
		settings.fontInput, `C:\Windows\Fonts\meiryo.ttc; more fallbacks... (blank = built-in)`)
	if c.Button(sdl.Rect{X: pad + 540, Y: y, W: 70, H: btnH}, "Apply") || fontCommit {
		raw := strings.TrimSpace(settings.fontInput)
		a.d.Prefs.Update(func(sn *config.Settings) { sn.FontOverridePaths = raw })
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

	// Opt-in: chrome's row and button metrics are tuned for the embedded face, so an unusual font can sit tight.
	few := a.d.Prefs.Load().FontEverywhere
	if next := c.Checkbox(pad, y, "Use the font everywhere — menus, buttons, lists & tabs too (not just chat)", few); next != few {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				FontEverywhere =

				next

		})

		a.applyFontConfig()
		switch {
		case !next:
			settings.statusLine = "Whole-UI font off — chrome back on the built-in font."
		case dys:
			settings.statusLine = "OpenDyslexic now drives the whole UI."
		case strings.TrimSpace(a.d.Prefs.Load().FontOverridePaths) != "":
			settings.statusLine = "Custom font now drives the whole UI."
		default:
			settings.statusLine = "Whole-UI font armed — set a font above (or the dyslexia toggle) to see it."
		}
	}
	y += 28
	return y
}

// Package-level strings because these draw every frame.
const (
	demoVideoCalloutLead = "Have an AO2 .demo session file? Turn it into a shareable video."
	demoVideoCalloutDrag = "…or drag the .demo file anywhere onto this window."
	// So the MP4 promise does not dead-end: GIF/WebP still work, and the import still lands.
	demoVideoCalloutNoFF = "🎥 Video needs ffmpeg on PATH — GIF/WebP work without it, and the import still lands in Recordings."
)

// drawDemoToVideoCallout draws the .demo to video entry point at the top of the Studio tab.
func (a *App) drawDemoToVideoCallout(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, ".demo → video")

	// The Import button shows on every OS, so the panel is always the full two-row height.
	panelH := int32(60)
	if !videoenc.Available() {
		panelH += 18 // room for the ffmpeg fallback line
	}
	panel := sdl.Rect{X: pad, Y: y, W: w - pad - scrollBarW, H: panelH}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)

	iy := y + 8
	c.Label(pad+12, iy, demoVideoCalloutLead, ColText)
	iy += 22
	if c.Button(sdl.Rect{X: pad + 12, Y: iy, W: 170, H: btnH}, "📥 Import .demo…") {
		a.openDemoBrowser() // in-app file browser — pick any .demo/.aorec from your PC
	}
	c.Label(pad+192, iy+5, demoVideoCalloutDrag, ColTextDim)
	iy += btnH + 6
	if !videoenc.Available() {
		c.Label(pad+12, iy, demoVideoCalloutNoFF, ColDanger)
	}
	return y + panelH + 12
}

// Package-level literals because these draw every frame.
const (
	contentToolsLead = "Check what a recording needs — or bundle it so it plays anywhere."
	contentToolsBody = "Enumerates every sprite/background/music/SFX/blip a .demo/.aorec references, probes the origin, and lists what's missing."
	contentToolsPkg  = "Package writes the found assets + a self-contained .aorec into recordings\\<name>-bundle\\ — it replays offline forever."
)

// drawContentToolsCallout draws the content entry points, which reuse the demo browser with a pick purpose.
func (a *App) drawContentToolsCallout(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, "Content report & packaging")

	panelH := int32(96)
	panel := sdl.Rect{X: pad, Y: y, W: w - pad - scrollBarW, H: panelH}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)

	iy := y + 8
	c.Label(pad+12, iy, contentToolsLead, ColText)
	iy += 20
	c.LabelClipped(pad+12, iy, panel.W-24, contentToolsBody, ColTextDim)
	iy += 18
	c.LabelClipped(pad+12, iy, panel.W-24, contentToolsPkg, ColTextDim)
	iy += 22
	if c.Button(sdl.Rect{X: pad + 12, Y: iy, W: 184, H: btnH}, "🔍 Check demo content") {
		a.openDemoBrowserFor(purposeCheck) // pick a recording → probe → report panel
	}
	if c.Button(sdl.Rect{X: pad + 204, Y: iy, W: 168, H: btnH}, "📦 Package this RP") {
		a.openDemoBrowserFor(purposePackage) // pick → probe → package once ready
	}
	return y + panelH + 12
}

// TextureScaleMode is the texture filter for "Smooth texture scaling": linear when on, nearest when off.
func TextureScaleMode(smooth bool) sdl.ScaleMode {
	if smooth {
		return sdl.ScaleModeLinear
	}
	return sdl.ScaleModeNearest
}
