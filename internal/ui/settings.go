package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
)

// settingsState lives on App lazily (kept here for file cohesion).
type settingsState struct {
	mountInput string
	loaded     bool
	statusLine string
	// fpsBufs backs the three frame-rate rows' exact-number entries (active,
	// idle, unfocused) — reseeded from the pref while unfocused, applied on
	// Enter (fpsSettingRow).
	fpsBufs [3]string
	// memBufs backs the exact-number fields of the memory/scale slider rows
	// (downscale %, animated height cap, animated budget, decode concurrency,
	// texture budget) - reseeded while unfocused, applied on Enter.
	memBufs [5]string
	// logDepthBuf backs the "IC log depth" exact-number entry in the Chat tab's
	// Chat log section — reseeded while unfocused, applied on Enter.
	logDepthBuf string
	tab         int                    // active settings tab (index into settingsTabNames)
	tabScroll   [numSettingsTabs]int32 // per-tab page scroll (each tab remembers its position)
	search      string                 // settings search query (jumps to the matching tab)
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

	// custom missing-sprite (missingno) image path edit buffer, seeded once from
	// the pref. errorSpriteLoaded gates the one-time seed like fontLoaded.
	errorSpriteInput  string
	errorSpriteLoaded bool

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
	// themeScanUserWant latches "a user asked for a rescan" across a scan that was
	// already in flight. scanThemes single-flights on themeBusy, and it used to
	// drop the whole request — including the fromUser bit — so clicking "Apply &
	// rescan" (or picking a folder) while the Settings screen's automatic first-open
	// scan was still running silently lost the #35 auto-snap for that action. The
	// latch is OR'd on every user-initiated call and cleared only by the landing
	// that actually acts on it, so neither a stale result nor a landing that
	// resolved nothing can swallow the intent. Render thread only, like the rest of
	// this struct: the scan goroutine never touches it (hard rules 1 and 8).
	themeScanUserWant bool
	// themeDDWasOpen edge-detects the theme dropdown so the catalog is rescanned
	// once per OPEN rather than once per themeScanMinInterval for as long as the
	// list is held open. The floor alone made a held-open picker poll the disk
	// every 5 s, which is exactly what the no-watcher decision (themecatalog.go's
	// header) exists to avoid; the floor stays on top of this edge, so opening the
	// picker twice in a second is still one scan. Render thread only.
	themeDDWasOpen bool
	// themeCountText memoizes the "(N found)" line beside the picker on the count
	// it was built from — see themeFoundText.
	themeCountText string
	themeCountFor  int
	// themeRows memoizes every string drawThemeCatalogRows draws. See themeRowText.
	themeRows themeRowText
	// themeArt memoizes the theme-art budget bar's label. See themeArtBarText.
	themeArt themeArtBarText

	// --- Settings ▸ Theme editor, the authoring tab ------------
	//
	// The CREATOR's four pieces of UI state. On settingsState rather than on App
	// because they are exactly that — what the tab's controls are showing — and they
	// die with nothing: the act reads them once and hands values to the funnel
	// (settingsthemeeditor.go). Render thread only, like the rest of this struct.
	//
	// newThemeName is the name field's live text (empty = use the placeholder).
	newThemeName string
	// newThemeTemplate is the picked starting point. The zero value is templateBlank,
	// which is the right default: it is the one template with no prerequisite.
	newThemeTemplate themeCreateTemplate
	// presetLayout / presetStyle index the two shipped preset axes, in the library's
	// own order — the same order the editor's gallery lists them in, so a pair picked
	// here and then opened in the gallery is the pair the gallery shows.
	presetLayout, presetStyle int
	// presetAxisNames memoizes the two label slices those dropdowns draw from, keyed
	// on the library pointer. Without it the tab would rebuild two 20-odd-entry
	// string slices every frame.
	presetAxisNames presetAxisNameCache

	// folder picking: native dialog output / resolved drag-drops land
	// here from goroutines (never block or stat on the render thread). Still used
	// by the THEME folder picker (browseForFolder) — the .demo picker moved fully
	// in-app (demobrowser.go), so this is the theme picker's alone.
	folderRes  chan string
	browseBusy bool

	// ioRes carries one-line results of off-thread file ops (learned
	// format export/import) back to the status line.
	ioRes chan string

	// Formats tab: fmtOrderText caches each asset type's EFFECTIVE probe list as
	// ready-to-draw text, parallel to config.TypeNames (whose order matches the
	// assets.AssetType enum). It is rebuilt only when the preferences' format
	// generation moves — one atomic load per frame — so drawing the tab joins no
	// strings and clones no slices per frame. That matters here because the
	// settings screen has no allocation gate: a per-frame regression on this page
	// would not be caught by the benchmark suite, so the cache is the guard.
	// fmtOrderOK distinguishes "never built" from "built at generation 0".
	fmtOrderText []string
	fmtOrderGen  uint64
	fmtOrderOK   bool
}

// themeScan is one scan result: the theme names found, the NORMALIZED
// root (users paste the themes folder itself, or a single theme inside
// it — both resolve to the root theme.Load expects), and an optional
// auto-pick when the pasted path WAS a single theme.
type themeScan struct {
	names    []string
	root     string
	pickName string
	// in is the theme folder this scan was ISSUED for — settings.themeDir as it
	// read at scanThemes time, before normalizeThemeRoot touched it. It is what
	// makes a STALE result recognisable: the folder can change (a picker landing,
	// a drop, a typed path) while a scan is in flight, and this result then
	// answers a question nobody is asking any more. Comparing `in` against the
	// current folder is the only sound test — comparing res.root would fire on
	// every normalized path too, because normalizing is exactly when root and the
	// configured folder legitimately differ. See pollThemeScan.
	in string
	// fromUser marks a scan the user actually asked for (Apply & rescan, the
	// folder picker, a dropped folder) as opposed to the automatic one the
	// Settings screen fires the first time it opens. Only the former may arm the
	// #35 auto-resize: the automatic scan re-applies the theme for anyone who has
	// a custom theme folder saved, so treating it as a pick would resize the
	// window merely for opening Settings.
	fromUser bool
}

var settings = settingsState{
	themeRes:  make(chan themeScan, 1),
	folderRes: make(chan string, 1),
	ioRes:     make(chan string, 1),
}

// Settings tabs: the screen is split into these categories so it's
// navigable instead of one long scroll. numSettingsTabs sizes the per-tab
// scroll array (keep it == len(settingsTabNames)).
const numSettingsTabs = 14

var settingsTabNames = [numSettingsTabs]string{
	"General", "Theme", "Theme editor", "Assets", "Formats", "Audio", "Chat", "Account", "Hotkeys", "Studio", "Data", "Voice", "Power user", "Reset",
}

// Tab indices (order matches settingsTabNames).
const (
	tabGeneral = iota
	tabTheme
	// tabThemeEditor owns AUTHORING: making a theme, opening the editor over one,
	// and taking a bundle somebody sent you. It exists because the editor — the
	// flagship — deserves a dedicated tab: a feature you have to scroll to
	// discover is a feature nobody discovers.
	//
	// IMMEDIATELY AFTER Theme, not at the end: the two are read as a pair ("pick
	// one" / "make one"), and the sidebar is scanned top to bottom, so the
	// authoring tab has to be where the eye already is when it fails to find the
	// editor on Theme. The Theme tab keeps SELECTION — the catalog, the badge, the
	// subtheme variant, the write root — and points here (drawThemeAuthoringHint).
	tabThemeEditor
	tabAssets
	// tabFormats owns EVERY control that decides which image/audio file type is
	// asked for. It sits next to Assets because that's the neighbourhood people
	// browse when something didn't load.
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
		// The sprite hover-preview cluster had NO keyword on this tab at all. The
		// bare "preview" is listed DELIBERATELY: the table is scanned in tab order
		// with substring matching, so any phrase here would swallow that query
		// anyway (it is a substring of "sprite preview"), and a silent theft is
		// exactly what the shadow test forbids. This tab owns four preview settings
		// to Theme's one section, so it takes the bare word and Theme keeps the
		// qualified "theme preview".
		"preview", "sprite preview", "hover preview", "preview box", "pin preview", "pinned preview", "preview pin",
		"application", "streamer mode", "debug", "performance", "perf hud", "fps", "notify ooc", "unread badge",
		"log colours", "log colors", "selection highlight", "highlight colour", "name colour", "name colours",
		"stage", "desk", "hide desk", "scale", "text size", "ui scale", "dpi", "zoom",
		"window", "fullscreen", "window size", "resolution", "extras box", "extras", "tear off",
		"fonts", "font", "cjk", "dyslexia", "dyslexic", "emoji", "smooth scaling", "tabs", "server tabs", "max tabs",
		// The system-font toggle's written path was wrong too ("Interface"); it is
		// on this tab, under Fonts.
		"system fonts", "installed fonts", "use fonts installed",
	},
	tabTheme: {
		// sections: Theme, Layout & fit, Layout presets, Lobby, Preview & binding.
		// Emote-grid spacing had NO keyword at all and its only written path named
		// a tab that does not exist, so it was effectively unreachable. Every word
		// someone would actually type for it lives here, on the tab that owns it.
		"emote icon spacing", "emote spacing", "icon spacing", "emote gap", "spacing", "gap",
		"emote grid", "emote icons",
		"theme", "theme picker", "chatbox", "skin", "default theme",
		// "theme preview", not a bare "preview": General's sprite-preview cluster
		// takes that word (see its block above). "bind"/"binding" still reach this
		// section, and the gather-search matches its ROW LABELS first regardless.
		"layout", "fit", "courtroom design", "lobby", "theme preview", "bind", "binding",
		"layout presets", "preset", "presets", "save layout", "stage preset", "theater", "theatre",
		// the themes folder, its badges and AO2 subthemes. Terms are
		// picked to avoid the shadow trap (the table is scanned in TAB ORDER and
		// matches forward, so a term here swallows every later tab's query hidden
		// inside it): NOT "rescan" (Assets owns it), NOT anything containing
		// "import"/"portable" (Data), hence "reload themes" and "themes folder".
		"subtheme", "subthemes", "theme variant", "variant",
		"themes folder", "my themes", "where are my themes", "open themes folder",
		"reload themes", "find themes", "theme badge",
		// The "Get themes" discovery row. Same shadow discipline as the block above
		// (the table is scanned in TAB ORDER and matches forward, so a term here
		// swallows every later tab's query hidden inside it): every entry is anchored
		// on "themes" or on a word no later tab claims. Note the trap the shadow test
		// CANNOT see: TestSettingsSearchKeywordsDoNotShadowLaterTabs compares WHOLE
		// keywords, so a long term here that merely CONTAINS a later tab's short query
		// passes the gate while stealing that query for real. Hence three exclusions,
		// each a word a later tab owns outright:
		//   - anything containing "download" — Assets owns "download"/"downloader".
		//   - "source repository" — the bare "source" belongs to Assets' "asset
		//     source", the only other "source" term and the owner of the real
		//     "Asset source" section.
		//   - "new themes" — the bare "new" belongs to Chat's "jump to newest".
		// "github" and "theme repository" stay: no later tab claims either word.
		"get themes", "more themes", "free themes", "extra themes",
		"where to get themes", "drop-in themes", "franchise themes",
		"github", "theme repository",
		// The theme EDITOR's terms are on tabThemeEditor below — search terms stay
		// with the row they describe.
	},
	tabThemeEditor: {
		// sections: New theme, The editor, Bring a theme in.
		//
		// THE SHADOW DISCIPLINE IS UNCHANGED and it still governs every line here.
		// The table is scanned in TAB ORDER and matches FORWARD (the query must be a
		// substring of a term), so a term on this tab swallows every later tab's
		// query hidden inside it. This tab is index 2, so "later" is Assets onwards —
		// the same set the theme-editor block was vetted against at index 1, minus Theme
		// itself, which sits BEFORE us and therefore still wins its own words.
		//
		// Each exclusion below is a word a later tab owns outright:
		//   - nothing containing "export" or "import" (tabData, tabStudio) — hence
		//     no "export theme" and no "import theme" either. "aotheme" and "theme
		//     bundle" carry the transport rows instead; they contain neither word.
		//   - nothing containing "layer" (tabAssets owns "layer"/"layered", the
		//     layered assets). THIS is why the editor's noun is "element".
		//   - nothing containing "custom" (tabAudio's "custom music", tabAssets'
		//     "custom error sprite"). This is why the brief's suggested "customize
		//     layout" is NOT here: it would steal the bare "custom" from both. The
		//     feature is not lost — "edit the layout" and "move things around" below
		//     answer the same question with words nobody else claims, and the
		//     gather-search matches ROW LABELS first regardless.
		//   - not "maker" on its own and nothing containing it (tabStudio's "scene
		//     maker"): "theme maker" would steal the bare "maker". "theme builder"
		//     and "theme creator" say the same thing and collide with nothing.
		//   - nothing containing "scan" ("scanlines" → tabAssets' "rescan"),
		//     "order"/"list" (tabFormats' "probe order", tabAccount's "master
		//     list"), "new" (tabChat's "jump to newest"), "download" (tabAssets),
		//     "source" (tabAssets' "asset source") or "grid"/"glow" (tabGeneral).
		//     Hence "start a theme from scratch", never "new theme".
		"theme editor", "editor", "make a theme", "build a theme", "create a theme",
		"theme builder", "theme creator", "edit theme", "edit a theme", "edit my theme",
		"start a theme", "from scratch", "blank theme", "template", "templates",
		"theme template", "copy a theme", "copy for editing", "duplicate a theme",
		"edit the layout", "move things around", "design my own theme", "authoring",
		"decorate", "decoration", "element", "elements", "stacking", "inspector",
		"undo", "shapes", "masks", "animated background", "background art",
		"split screen", "procedural", "halftone", "generator",
		// The image/animated-webp intake (this wave). "upload" is the word a person
		// actually types for it even though nothing is uploaded anywhere.
		// NOT "webp" and NOT anything containing it ("animated webp"): tabFormats'
		// own note reserves "webp"/"gif" for tabStudio's video export, and this tab
		// sits ahead of both. "animated image" carries the same question.
		"add an image", "upload an image", "upload image", "add art", "picture",
		"animated image", "theme art", "theme image",
		// Live preview, the editor rail's own toggle.
		"live preview", "live", "freeze the preview", "preview while editing",
		// The transport rows: taking in what a friend sent, and (in the editor's
		// header) handing one back.
		"aotheme", "theme bundle", "share a theme", "send a theme", "give a theme",
	},
	tabAssets: {
		// sections: Predictive prefetch, Missing sprites, Asset source, Downloader,
		// Cache. (Everything format-related lives on the Formats tab.)
		"predictive prefetch", "prefetch", "aggressiveness", "speculative", "preload",
		"local assets", "local", "mount", "downloader", "download",
		// The layered source. Terms stay specific — the table is scanned in
		// tab order with substring matching, so a broad word would hijack the query.
		// NB: NOT a bare "stream" — the table is scanned in tab order with substring
		// matching, and General's "streamer mode" already owns that prefix.
		"asset source", "layered", "layer", "pack", "content pack", "zip", "override",
		"rescan", "folders", "my folders",
		"cache", "disk cache", "disk", "zstd", "clear cache",
		"loop preanim", "preanimation", "preanim loop", "loop preanimations",
		"placeholder", "missing sprite", "missingno", "missing character", "error sprite", "custom error sprite", "404",
	},
	tabFormats: {
		// sections: How formats work, This server, Automatic detection, Formats per
		// asset type, Fallbacks, Learned formats.
		// NOTE the table is scanned in TAB ORDER and matches forward (the query is a
		// SUBSTRING of a term), so a term here silently steals every query a LATER
		// tab answers better — including queries hidden inside a longer term. Absent
		// on purpose, each because a later tab owns the word: "blip"/"volume"
		// (Audio); "ignore"/"callword" (Chat); "password" (Account); "webp"/"gif"
		// (Studio's video export — so no "webp format" either, which would still
		// contain "webp"); "export"/"import" (Studio, Data); "extensions.json"
		// (its ".json" tail would swallow Data's "json").
		// None of those are lost: the gather-search matches ROW LABELS first, and
		// this tab's rows literally read ".webp", "extensions.json", "Export
		// learned". This table is only the concept-level fallback. Bare
		// "audio"/"format" need no entry at all — they match a TAB NAME, and the
		// name pass runs before this one.
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
		// sections: Volume (master / music / SFX / blip / alert), music ducking,
		// custom music streaming.
		"audio", "volume", "master volume", "music volume", "sfx volume", "blip volume", "blip",
		"alert volume", "music ducking", "duck",
		"stream music", "disable music", "stop fetching music", "custom music",
		"music across tabs", "tab music", "keep music playing", "music continuity", "keep music",
	},
	tabChat: {
		// sections: Text & typing, Chat log, Case alerts, Callwords, Do Not Disturb,
		// Messages & connection, Sound effects, Music history, Friends, Ignored
		// players, Mod tools.
		"text", "typing", "text crawl", "text stay", "text speed", "chat limit", "catch up",
		"chat log", "ic log", "timestamps", "log", "song url", "song link", "full link", "music url",
		// The follow-jump toggle. NOT a bare "smooth" — the table is scanned in TAB
		// ORDER and matches forward, and General's "smooth scaling" already owns that
		// prefix. Also NOT the "-ing" forms: "scrolling" literally CONTAINS "rolling",
		// which would hand Studio's rolling-buffer queries to this tab.
		"log scroll", "smooth log scroll", "scroll", "jump to newest", "auto scroll", "autoscroll",
		"case alerts", "casing", "case", "callword", "callwords", "ping", "alert",
		"do not disturb", "dnd", "messages", "connection", "auto reconnect", "reconnect", "disconnect confirm",
		"sound effects", "sfx", "mute sfx", "music history", "jukebox history",
		"friends", "friend", "nickname", "friend colour", "ignored players", "ignore", "block",
		"mod tools", "moderator", "modcall", "ipid",
	},
	tabAccount: {
		// sections: Login, Master list, Discord. (TLS / Asset Origin are on Power user.)
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
		// sections: TLS, Asset Origin, character-folder casing, Proxy, and the
		// render/GPU knobs. (Image formats are on the Formats tab — see tabFormats.)
		"power user", "advanced", "expert",
		"tls", "ssl", "certificate", "cert", "validate certificate", "self-signed", "wss", "verify", "security",
		"origin", "cors", "referer", "asset origin", "origin header", "stream from base",
		"casing", "capital", "capitalize", "capitalise", "uppercase", "lowercase", "character folder", "folder case",
		// Someone looking for this is usually looking for it because nothing will
		// connect, so the terms they might actually type all belong here.
		"proxy", "socks", "socks5", "vpn", "tunnel", "corporate network", "work network",
		"wpad", "proxy script", "pac file", "system proxy", "no proxy", "bypass",
		// NOT the bare "pac": the search matches forward, so it is a substring of
		// General's "opacity" and would silently steal the query for that tab.
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

// diskCacheBudgetStepMiB snaps the #34 disk-cache-limit slider to a round grid
// (128 MiB): the cap covers 128 MiB … 64 GiB, where exact bytes don't matter,
// so a coarse grid keeps the displayed value clean.
const diskCacheBudgetStepMiB = 128

// diskCacheBudgetLabel formats a positive T3 cap in MiB as "N MiB" below 1 GiB
// and "N.N GiB" at or above it (0 = unlimited is handled by the caller).
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
		settings.themeName, settings.themeDir = a.d.Prefs.Theme()
		if settings.themeName == "" {
			settings.themeName = theme.DefaultThemeName
		}
		settings.loaded = true
		a.scanThemes(false) // automatic: opening Settings is not a theme pick (#35)
		// Trigger 1 of four (§Q6): opening the screen asks for a catalog. Floored
		// by themeScanMinInterval, so bouncing in and out of Settings does not
		// walk the disk each time.
		a.requestThemeCatalog(themeScanOnOpen)
	}
	// Trigger 3: the window regained focus while this screen is up — the
	// "I dropped a theme into the folder in Explorer and came back" story. Costs
	// exactly one os.Stat of the write root, off-thread, and only rescans if the
	// directory's mtime actually moved.
	if a.consumeThemeFocusStat() {
		a.requestThemeCatalog(themeScanOnFocus)
	}

	// In-app .demo browser fence: while it's open the WHOLE settings screen behind
	// it must go click-dead (header Back/search, tab strip, tab body), so a click on
	// the modal can't also fire a widget beneath it. Recomputed fresh each frame from
	// the open flag — no cross-frame persist-and-release bug (unlike emojiPickerFence).
	// It's set HERE (before the header widgets draw) and RELEASED at the very end,
	// right before the browser itself draws, so the browser's own c.Button/VScrollbar
	// (which read hovering()) work while everything behind stays fenced. Mirrors the
	// app.go toolbox release-and-restore idiom. The Studio content-report panel
	// (contentpanel.go) is the SAME kind of blocking modal and rides the same fence —
	// the two are mutually exclusive (picking in the browser closes it and opens the
	// panel), so one flag OR the other is set, never contended.
	//
	// The base-folder wizard (basewizard.go) is the same kind of blocking modal and
	// joins the same fence. It is NOT exclusive with the browser — its step 1 opens
	// one on top of itself — so the wizard fences ITSELF while the browser is up;
	// see drawBaseWizard.
	if demoBrowser.open || contentPanel.open || baseWizard.open {
		c.modalOn = true
	}

	// These run regardless of the active tab, so async results land and
	// dropped files are honored from any tab (theme scan, folder pick,
	// drag-drop, and the off-thread IO status line).
	a.pollThemeScan()
	a.pollFolderPick()
	// baseWizardTakesDrop CONSUMES the drop when it returns true: the OPEN
	// base-folder wizard gets first refusal, because it says "or drag the folder
	// onto this window" and has to mean it. Without that arm the drop falls through
	// to the default below, which points the user's THEME ROOT at the folder they
	// were setting up as an asset base — the silent-wrong-action class dropclaim.go
	// exists to end, arriving through a different door. It refuses anything a
	// global owner already claimed, so it cannot resurrect that bug from the other
	// side either. Pinned by TestOpenBaseWizardTakesTheDropBeforeTheThemeRoot.
	if c.dropped != "" && !a.baseWizardTakesDrop(c.dropped) {
		// Drag-and-drop anywhere on this screen. The classification is shared
		// with HandleFileDrop (dropclaim.go) precisely because this arm and that
		// one used to disagree: a recording, or a theme BUNDLE, must not fall
		// through to the default here, or the same drop would ALSO repoint the
		// theme folder at the file's parent directory — silently, since the
		// global handler was meanwhile warning about the very same file.
		// Anything unclaimed still points the theme folder, which is the
		// documented folder-import path (#21).
		// The routing itself is settingsDropAction (dropclaim.go) — a named
		// function with a table test on it, so the arm that stops a .ttf or a
		// .aotheme from repointing the theme root cannot be deleted green.
		switch settingsDropAction(claimDroppedFile(c.dropped, settings.importArmed)) {
		case settingsDropImportSettings:
			settings.importArmed = false
			importSettingsAsync(a.d, c.dropped)
		case settingsDropIgnore:
			// HandleFileDrop is the single owner of dropped recordings, theme
			// bundles and theme FONTS.
		case settingsDropRepointThemeRoot:
			resolveDroppedFolder(c.dropped)
		}
	}
	select {
	case line := <-settings.ioRes:
		settings.statusLine = line
	default:
	}

	// --- header band: title, search, Back -----------------------------------
	// Content starts below the app-chrome band (#14, chrometop.go).
	hdrY := a.topChromeH() + pad
	c.Heading(pad, hdrY, "Settings", ColText)
	// Search (#26 gather): type a term and the page becomes a live list of EVERY
	// matching setting across every tab — click (or Enter for the top hit) jumps
	// straight to that row. The old jump-to-tab hint is superseded.
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
	// F2: no chrome label may draw past the card. The settings rows carry sentence-
	// long help text and the form draws it two ways, both of which overflowed:
	//
	//   - Checkbox grew its hit box and its label to whatever the string measured,
	//     with nothing bounding it, so on a narrow window the row ran off the card
	//     and out of the WINDOW entirely. That is the FONTS section's system-fonts
	//     line and about forty siblings.
	//   - The bare help lines under the rows — roughly a hundred c.Label calls, some
	//     190 characters, plus the FONTS chain line's c.LabelClipped, whose own maxW
	//     is the window and not the card — stayed inside the window but were cut dead
	//     at the card's clip edge, with no ellipsis to say the sentence continued.
	//     A hard cut reads as a rendering fault; that is the "cut off at the right
	//     border" half of the report.
	//
	// One bracket answers both: Ctx.Label / LabelClipped / Checkbox all consult it,
	// the card knows its own right edge and the rows do not need to be told. Popped
	// with defer so the several early returns below cannot strand the limit on the
	// shared Ctx and truncate the courtroom's labels on the next screen — and popped
	// AGAIN, explicitly, the moment the card's clip closes, because the chrome after
	// it (the full-window warn banner, the .demo browser, the content panel) is not
	// inside the card and must keep its own width. Popping twice with the same saved
	// value is idempotent.
	cardLabelLimit := c.pushRowLabelLimit(a.formX + a.formW)
	defer c.popRowLabelLimit(cardLabelLimit)

	viewH := h - contentTop - pad
	scroll := &settings.tabScroll[settings.tab]

	// Clip everything below the header; fill the card surface (a step between the
	// page background and panels) so each section reads as a card, separated by
	// page-coloured gap bands punched in by settingsSection.
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: cardX, Y: contentTop, W: cardW, H: viewH})
	c.Fill(sdl.Rect{X: cardX, Y: contentTop, W: cardW, H: viewH}, cardColor())

	// #26 gather-search: with a query typed, the card region becomes a RESULTS
	// list of every matching setting across every tab (click → jump + flash).
	// Skipped while the .demo browser is open (!demoBrowser.open): this branch
	// returns EARLY, which would jump over the end-of-function fence release +
	// browser draw — stranding c.modalOn=true and freezing the whole UI (the
	// un-released-fence failure the emoji picker documents). Falling through draws
	// the fenced (click-dead, covered) tab body instead, so the release always runs.
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
	// The card is drawn: everything below is full-window chrome (the warn banner
	// spans w-2*pad, the .demo browser and content panel are their own overlays), so
	// release the card's right edge here rather than letting it shorten their labels.
	c.popRowLabelLimit(cardLabelLimit)

	contentH := (y + *scroll) - contentTop + pad
	// !c.modalOn: with the in-app .demo browser open the page must not scroll behind
	// it (this wheel consume is unconditional — the VScrollbar drag below already
	// self-gates on hovering(), which reads false under modalOn). The browser reads
	// the wheel for its own list after the fence is released.
	if !c.ctrlHeld && !c.wheelTaken && !c.modalOn {
		*scroll -= c.wheelY * scrollStepPx
	}
	track := sdl.Rect{X: w - scrollBarW - 2, Y: contentTop, W: scrollBarW, H: viewH}
	*scroll = c.VScrollbar("settscroll", track, *scroll, contentH, viewH)
	settings.scrollToSection = "" // one-shot: consumed by the matching section this frame, else dropped
	// Warn banner, mirroring the lobby/char-select/courtroom draw sites: Settings
	// was the ONE screen without it, so every Studio-picker message (import
	// confirmation, extension rejection, picker failure) went invisible exactly
	// where the user stood (a hotfix review find). Fixed position, outside
	// the scrolled clip, so it can't scroll away.
	if a.warnActive() {
		c.LabelClipped(pad, h-24, w-2*pad, a.warnLine, ColDanger)
	}

	// The in-app .demo browser and the Studio content-report panel draw LAST
	// (topmost) with the page fence RELEASED, so their own widgets work while
	// everything behind stayed click-dead all frame. Both no-op when closed; they're
	// mutually exclusive, so releasing once for either is correct. The content panel
	// draws after the browser (a pick closes the browser and opens the panel in the
	// same click — the panel must own this frame's release).
	if demoBrowser.open || contentPanel.open || baseWizard.open {
		c.modalOn = false
	}
	// The wizard draws BEFORE the browser: its step 1 opens one on top of itself,
	// and the topmost panel has to be the one drawn last. drawBaseWizard re-fences
	// itself for exactly that overlap.
	a.drawBaseWizard(w, h)
	a.drawDemoBrowser(w, h)
	a.drawContentPanel(w, h)
}

// drawSettingsTabBody dispatches one tab's rows starting at y — shared by the
// normal page draw and the search collector's fenced all-tabs pass (#26).
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
	// labelBtnPadX is the horizontal padding a label-measured button adds around
	// its text (half each side). Buttons whose width is measured from the label
	// rather than fixed — the Window section's size presets and the "Theme's
	// design size (W×H)" row, which both size themselves because their labels
	// carry live numbers — share it so a wider label can never look tighter than
	// a narrow one in the same column.
	labelBtnPadX = 14
	// designSizeHintGap separates the "Theme's design size (W×H)" button from its
	// explanatory hint. Its own constant because that button's width is measured
	// from the label (the size is in it), so the hint's x AND its clip width are
	// both derived from this one number — they must not drift apart.
	designSizeHintGap = 10
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

// numSliderRow renders a labelled slider with -/+ nudge buttons and an
// exact-value entry field (the power-user ask: nudge with arrows or type a
// value). 0 is the off/default sentinel (the bottom of the track); the getter
// maps it to the shipped default. buf backs the number field (settingsState),
// reseeded from the pref while unfocused. Returns y past the row.
func (a *App) numSliderRow(y int32, id, label string, min, max, step, def int, get func() int, set func(int), buf *string, suffix, tip string) int32 {
	c := a.ctx
	pad := a.formX
	c.Label(pad, y+4, label, ColText)

	cur := get()
	track := sdl.Rect{X: pad + 210, Y: y + 2, W: 110, H: 16}
	raw := int(clampI32(c.Slider(id, track, int32(cur), int32(max)), 0, int32(max)))
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
	shown := a.d.Prefs.Load().Showname
	// CJK / non-Latin shownames must render as real glyphs while typed here too
	// (a saved Japanese showname); plain ASCII keeps the single-font fast path.
	snPrimary, snEmoji := a.icFieldFonts(shown)
	if next, _ := c.TextFieldEmoji("showname", sdl.Rect{X: pad + 130, Y: y, W: 240, H: fieldH}, shown, "your saved showname", snPrimary, snEmoji); next != shown {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.Showname = next })
	}
	// Default OOC name: write-through to prefs like the showname; it seeds
	// every NEW tab (blank sends a sticky AsyncAO<n>). The courtroom OOC-name
	// fields are tab-local and never write this back.
	c.Label(pad+390, y+4, "OOC name:", ColText)
	savedOOC := a.d.Prefs.Load().OOCName
	onPrimary, onEmoji := a.icFieldFonts(savedOOC) // CJK-safe OOC name (ASCII stays fast path)
	if next, _ := c.TextFieldEmoji("oocdefault", sdl.Rect{X: pad + 480, Y: y, W: 200, H: fieldH}, savedOOC, "blank = AsyncAO<n>", onPrimary, onEmoji); next != savedOOC {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.OOCName = next })
		a.oocName = next // the ACTIVE tab adopts the new default right away
	}
	y += 38

	// Showname presets (M6): a saved, global list — quick-swap them in-game with
	// keybinds (random or a specific one). Cleared only by a factory reset.
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
			// Per-preset keybind: arm a capture (pollShownameBind binds the next
			// key to this showname); right-click the button clears the bind.
			bound := a.shownameKeyFor(name)
			// bindChipLabel, not "Key: "+bound: this namespace fires on Alt+<key>
			// (bareBindKey, qol.go) and a row printing the bare letter advertises
			// a press that no longer does anything.
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
	// Streamer mode leads the section (playtest: it's the one people need to
	// find FAST, right before going live).
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
	// #103: viewer opt-out of OTHER players' transmitted sprite styles (your own
	// is set in the Extras → Sprite Style picker). Reduce-motion already drops a
	// received style's wobble/spin; this hides the whole thing.
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
	// #2: viewer opt-out of OTHER players' emoji reactions (the floating emoji over the
	// stage). Sending your own reactions is unaffected — this only hides incoming floats.
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
	// Sprite hover-previews: rest the cursor on a character/emote button to pop a
	// full-size preview. ON by default; the dwell before it shows is tunable.
	// Gates ONLY the hover dwell — right-click previews always work.
	prev := a.d.Prefs.Load().SpritePreviewOn
	if next := c.Checkbox(pad, y, "Sprite hover-previews (ON by default): hovering a character or emote button shows the full-size sprite. Off only disables the hover pop-up — right-click still previews", prev); next != prev {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpritePreviewOn = next })
	}
	y += 26
	if prev {
		ms := a.d.Prefs.PreviewHoverMillis()
		if next := a.previewDelayRow(y, ms); next != ms {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.PreviewHoverMs = next })
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
	// Keep the preview pinned: the box's own pin button writes this same preference,
	// so a player who pins one box discovers the setting and vice versa. A pinned
	// ANIMATED preview holds the frame pacer at the active cap for as long as it is
	// up — true of the right-click pin already, but a sticky default makes it the
	// steady state, hence the note in the label.
	//
	// The row says "pin button", never a 📌. This label is drawn through
	// chromeFaceFor like every other settings string, and U+1F4CC is outside the
	// chrome face's coverage on plenty of installs — a tofu box in the one sentence
	// whose whole job is to point at a control. It would also be a lie: the box
	// draws a VECTOR pin (drawToolIcon/iconPin), not that glyph.
	pinned := a.d.Prefs.Load().PreviewPinned
	if next := c.Checkbox(pad, y, "Keep the sprite preview pinned: it stays open until you close it (the pin button on the box toggles this too; an animated preview keeps the client at full frame rate while it is up)", pinned); next != pinned {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PreviewPinned = next })
	}
	y += 26
	// Emote-name hover tooltips: rest the cursor on an emote button to see its
	// name (independent of the sprite pop-up above). ON by default; the dwell is
	// tunable. Lightweight — a name-only tooltip, not the full-size art.
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
		ems := a.d.Prefs.EmoteHoverNamesMillis()
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
	// (Emote-grid icon spacing lives under Theme > Layout & fit — it is a layout
	// measurement and AO2 themes override it, which is where people look for it.)
	// Sprite repositioning: drag a character in the viewport to move them (the
	// override sticks per character until reset). OFF by default so a stray click
	// can't nudge a sprite; right-clicking a sprite resets just that one.
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
	// Hide-sprite ("Missingno"): right-click a sprite → confirm → hidden for the
	// session (for players who'd rather not see certain art). ON by default.
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
	// Hide the desk (the foreground table the character stands behind).
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
	tabCap := a.d.Prefs.TabCap()
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
		secs := a.d.Prefs.BgSlideshowSeconds()
		// Bounds match the config clamp (3..600s); SetBgSlideshowSeconds is authoritative.
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
		hint := "1"
		if !next {
			hint = "0"
		}
		// The hint applies at texture CREATION; purge so everything
		// re-streams (demand pipeline + scenery heal repopulate live).
		sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, hint)
		a.d.Store.Purge()
		// Purge destroyed the pinned shape masks SYNCHRONOUSLY — drop the
		// frame-resolved Ctx pointers before anything shaped draws this frame.
		a.invalidateShapeMasks()
		c.purgeTextCache()
		a.applyThemeAsync()
		// Purge also destroyed the pinned missingno placeholder — re-pin the
		// embedded default (render thread) and re-kick a custom image if set, so
		// the missing-sprite fallback survives a filtering swap (missingno.go).
		a.uploadEmbeddedMissingno()
		if p := a.d.Prefs.Load().ErrorSpriteFile; p != "" {
			a.applyErrorSprite(p)
		}
		settings.statusLine = "Re-streaming textures with new filtering."
	}
	y += 26
	// Per-character sprite filtering (#21). Separate from the client-wide toggle
	// above, which still governs backgrounds, desks and chrome. This one applies
	// live — the filter is resolved at draw time from the page memo — so unlike
	// the hint it needs no purge and no re-stream.
	spriteScale := config.ClampSpriteScaling(a.d.Prefs.SpriteScalingMode())
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
	// Global scale: DPI-driven by default, manual spinbox when auto is off.
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
	// Window size / fullscreen: pick your own client dimensions (a window bigger
	// than the monitor can't be dragged smaller; F11 + Fit to screen are the
	// escapes). All window ops run here on the render thread.
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
		// #35, manual half: snap to the size the ACTIVE theme was drawn for. Only
		// offered when the theme declares a courtroom rect — the automatic snap on a
		// theme pick covers the common case, this is the way back to 1:1 after any
		// manual resize. Same applyWindowSize route as every button above, so the
		// clamp, the un-maximize and the persist are shared, and it stays a one-line
		// call a future Window menu can reuse.
		//
		// themeDesignSizeOffer, not themeDesignWindowSize: it also gates on the
		// themed courtroom layout being the one actually drawn, so this row and the
		// automatic snap agree about when a theme canvas is meaningful.
		// The label carries the size the button APPLIES, not the theme's raw canvas.
		// They are not the same number: the canvas sits below the app-chrome band, so
		// the snap asks for canvas + themeDesignWindowInset (44 px) — the stock theme's
		// 714×579 canvas needs a 714×623 window. Printing the canvas here while
		// applying the window size made the button describe a resize it never performed.
		if _, _, ok := a.themeDesignSizeOffer(); ok {
			tw, th, _ := a.themeDesignWindowTarget()
			label := fmt.Sprintf("Theme's design size (%d×%d)", tw, th)
			bw := c.TextWidth(label) + labelBtnPadX
			if c.Button(sdl.Rect{X: pad, Y: y, W: bw, H: btnH}, label) {
				a.applyThemeDesignWindowSize()
				settings.winLoaded = false
			}
			// The button label carries the size, so the hint is pure explanation —
			// drop it rather than draw it into a negative width at a narrow window.
			if hintW := w - pad - bw - designSizeHintGap - scrollBarW; hintW > 0 {
				c.LabelClipped(pad+bw+designSizeHintGap, y+4, hintW,
					"this theme's canvas plus the menu bar and tab strip above it — the courtroom then draws 1:1, like the stock AO2 client", ColTextDim)
			}
			y += btnH + 10
		}
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
	// The census reads the machine's OWN fonts, so it is the only text path whose
	// behaviour differs per operating system. Exposed so a player seeing wrong or
	// missing text can take the whole subsystem out of the picture in one click
	// instead of waiting for a build.
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

	// Custom font override: a chain of TTF/TTC paths, first covering font per
	// line wins (put a CJK-capable font later in the chain). Drives the IC/OOC
	// chat + log text; the "everywhere" checkbox below extends it to the whole
	// UI. Saved even while the dyslexia font is on (which overrides it), so it
	// returns when you switch back.
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

	// Font-everywhere: extend the ACTIVE override (dyslexia toggle or the chain
	// above) to all chrome text too. Opt-in — the chrome's fixed row/button
	// metrics are tuned for the embedded face, so an unusual font can sit tight
	// in buttons; chat/log stay the reading-optimized surface either way.
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

// Call-out copy (precomputed package-level strings — drawn every frame, so no
// per-frame fmt.Sprintf/concat: sibling settings labels use literals for the same
// reason). The Import button opens the in-app file browser on EVERY OS; the
// "drag anywhere" line is the second, cross-platform mechanism.
const (
	demoVideoCalloutLead = "Have an AO2 .demo session file? Turn it into a shareable video."
	demoVideoCalloutDrag = "…or drag the .demo file anywhere onto this window."
	// Reuses the ffmpeg wording from drawExportOptions/startSceneExport so the MP4
	// promise doesn't dead-end: GIF/WebP still work, and the import still lands.
	demoVideoCalloutNoFF = "🎥 Video needs ffmpeg on PATH — GIF/WebP work without it, and the import still lands in Recordings."
)

// drawDemoToVideoCallout draws the prominent ".demo → video" entry point at the
// top of the Studio tab: a highlighted panel (the c.Fill(ColPanel)+c.Border(ColAccent)
// idiom) with a one-line pitch, the in-app Import file browser (ALL platforms),
// the cross-platform drag hint, and — without ffmpeg — the ColDanger fallback note.
// Shadows pad/w per the mandatory settings idiom.
func (a *App) drawDemoToVideoCallout(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	y = a.settingsSection(y, w, ".demo → video")

	// Highlighted panel background + accent border spanning the section width. The
	// Import button now shows on every OS (the in-app browser is cross-platform),
	// so the panel is always the full two-row height.
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

// Content-tools call-out copy (package-level literals — drawn every frame, no
// per-frame concat, matching the demo-video call-out).
const (
	contentToolsLead = "Check what a recording needs — or bundle it so it plays anywhere."
	contentToolsBody = "Enumerates every sprite/background/music/SFX/blip a .demo/.aorec references, probes the origin, and lists what's missing."
	contentToolsPkg  = "Package writes the found assets + a self-contained .aorec into recordings\\<name>-bundle\\ — it replays offline forever."
)

// drawContentToolsCallout draws the "Check demo content" / "Package this RP"
// entry points right under the .demo → video call-out (same highlighted-panel
// idiom). Both reuse the in-app demo browser (openDemoBrowserFor) with a pick
// PURPOSE, so the file-navigation surface is shared with the video flow — the
// pick then routes to the content-report engine (openContentReportFor) and opens
// the report panel. Shadows pad/w per the mandatory settings idiom.
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
