// Package config persists user-tunable asset preferences.
//
// Concurrency model (spec §5): mutators take the write lock, mutate in
// memory, and non-blockingly signal a single saver goroutine — they never
// touch the disk. The saver debounces (DefaultSaveDebounce after the last
// signal), marshals under the read lock, writes a temp file, and renames it
// over the real file so a crash never corrupts preferences. SaveNow is the
// only synchronous flush and exists for shutdown and Settings-Apply.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultSaveDebounce is how long the saver waits after the most recent
	// mutation before flushing preferences to disk.
	DefaultSaveDebounce = 250 * time.Millisecond

	// PrefsDirName is the directory under os.UserConfigDir holding all
	// AsyncAO configuration.
	PrefsDirName = "AsyncAO"
	// PrefsFileName is the preferences file name inside PrefsDirName.
	PrefsFileName = "asset_preferences.json"

	// PairOffsetMin and PairOffsetMax bound pair offsets, in percent of the
	// viewport dimension (mirrors AO2-Client's slider range).
	PairOffsetMin = -100
	PairOffsetMax = 100

	// LearnedKeySeparator joins host and asset-type name in learned-format
	// map keys: "<host>|<type name>".
	LearnedKeySeparator = "|"

	prefsFilePerm   = 0o644
	prefsDirPerm    = 0o755
	prefsTmpPattern = PrefsFileName + ".*.tmp"

	jsonMarshalPrefix = ""
	jsonMarshalIndent = "  "
)

// defaultPreferAnimated is the out-of-the-box value for PreferAnimated.
// PreferAnimated is a decode/render toggle (play animation frames vs. first
// frame only) — never an extra network probe (spec §4).
const defaultPreferAnimated = true

// defaultFormatAutoDetect ships ON: servers publish their own format mix
// in extensions.json (webAO convention) and seeding from it beats blind
// probing; manual per-type tuning still governs everything the manifest
// doesn't cover.
const defaultFormatAutoDetect = true

// defaultUIScaleAuto ships ON: the UI scale follows the display DPI
// (HiDPI screens render readable out of the box); unticking it re-arms
// the manual UI scale row in Settings.
const defaultUIScaleAuto = true

// defaultThemeLayout ships ON: when the active theme defines the AO2
// courtroom_design.ini geometry, the courtroom adopts it wholesale —
// that IS what picking a theme means to AO players.
const defaultThemeLayout = true

// defaultCatchUpWhenBehind ships ON: in a packed room the IC stage otherwise
// crawls through every queued preanim/shout, falling minutes behind real-time.
// Catch-up fast-forwards the backlog; the IC log still keeps every message.
const defaultCatchUpWhenBehind = true

// Catch-up queue-depth threshold: engage once MORE than this many messages are
// waiting behind the one starting. The default is the floor (1) so the IC stage
// stays real-time — in a busy room the newest message types out in full (with
// its sprite, name and effects) while any backlog behind it flashes past, so
// the textbox always tracks the latest line instead of crawling seconds behind.
// Normal back-and-forth (≤1 waiting) still plays every message in full. Raise it
// to watch more of a backlog animate. DefaultCatchUpThreshold is exported so the
// UI can show it; the value is clamped to [catchUpThresholdMin, catchUpThresholdMax].
const (
	DefaultCatchUpThreshold = 1
	catchUpThresholdMin     = 1
	catchUpThresholdMax     = 50
)

// Multi-server tab cap: how many servers you can keep open at once. Default 6;
// raise it to lurk as many as you like. Each tab costs a websocket + reducer +
// two bounded logs, so it stays bounded (rule §17.4) — the ceiling is a high,
// effectively-unlimited 99 rather than truly unbounded (hundreds of live
// connections would exhaust the budget; nobody runs that many AO tabs anyway).
const (
	DefaultMultiTabCap = 6
	minMultiTabCap     = 1
	maxMultiTabCap     = 99
)

// defaultEmoteButtonImages ships the courtroom emote picker as image
// buttons (characters/<char>/emotions/button<N>) rather than text chips.
const defaultEmoteButtonImages = true

// defaultSmoothScaling turns on linear texture filtering (SDL render
// scale quality): sprites stretched to the viewport stop shimmering.
const defaultSmoothScaling = true

// defaultUpdateCheck enables the one-shot GitHub-Releases update check at
// launch (M13). On by default per the user's intent; the check is a single
// async probe fired off the boot path, and a dev build never hits the network.
const defaultUpdateCheck = true

// defaultHighlightColor is the IC/OOC log text-selection highlight, packed
// 0xRRGGBB — defaults to the accent (120,170,255) so the look is unchanged
// until the user customizes it in Settings.
const defaultHighlightColor = 0x78AAFF

// Background slideshow (M5, OFF by default): while the courtroom is idle (no
// message on stage), cycle through the server's backgrounds every N seconds as
// ambiance. Bounded so the timer can't be set pathologically fast or slow.
const (
	defaultBgSlideshowSecs = 15
	minBgSlideshowSecs     = 3
	maxBgSlideshowSecs     = 600
)

// Layout scale bounds (percent). Defaults preserve the original layout:
// viewport 66 ≈ the old fixed 2/3 width; the text/height scales at 100.
const (
	DefaultViewportPercent = 66
	MinViewportPercent     = 40
	MaxViewportPercent     = 85

	// Chat = the IC message TEXT size (independent of its box); ChatBox =
	// the message box height; Log = log/OOC list text; Input = the IC/OOC
	// entry field height.
	DefaultScalePercent = 100
	MinChatScalePercent = 50
	MaxChatScalePercent = 250
	MinChatBoxPercent   = 50
	MaxChatBoxPercent   = 200
	MinLogScalePercent  = 75
	MaxLogScalePercent  = 200
	MinInputPercent     = 75
	MaxInputPercent     = 200

	// ScaleStepPercent is the −/+ button increment shared by the UI.
	ScaleStepPercent = 25
	// ViewportStepPercent is the viewport −/+ increment.
	ViewportStepPercent = 5

	// Global UI scale (renderer-level: every element, font, and grid
	// scales together; the mouse unprojects through the same factor).
	MinUIScalePercent  = 75
	MaxUIScalePercent  = 200
	UIScaleStepPercent = 5
)

// defaultAudioVolume is full volume (the pre-settings behavior).
const defaultAudioVolume = 100

// WardrobeCap bounds one SERVER's custom character list. Wardrobes were
// originally global; they are per-server now (playtest: a wardrobe
// carrying between unrelated servers was wrong), with a one-time legacy
// migration on the first post-update connect.
const WardrobeCap = 1024

// FavBackgroundCap bounds one SERVER's starred-background list (rule §17.4).
// Favorites are a curated subset the user pins in the background picker; a
// few hundred is already generous, so this is well under WardrobeCap.
const FavBackgroundCap = 512

// Message timing knobs (milliseconds), AO2-Client options.ini parity.
const (
	// DefaultTextCrawlMs mirrors courtroom.DefaultCharInterval.
	DefaultTextCrawlMs = 18
	MinTextCrawlMs     = 5
	MaxTextCrawlMs     = 100
	// DefaultTextStayMs mirrors courtroom.DefaultTextStayTime.
	DefaultTextStayMs = 200
	MaxTextStayMs     = 3000
	// Chat rate limit: 0 = off (our historical behavior; AO2-Client
	// defaults to 300 ms — users opt in).
	DefaultChatRateLimitMs = 0
	MaxChatRateLimitMs     = 5000
)

func clampPercent(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// AssetTypePrefs holds the per-asset-type format preferences.
type AssetTypePrefs struct {
	// FormatOrder is the ordered probe list for this type. An empty or
	// missing list means "use the built-in default order".
	FormatOrder []string `json:"formatOrder"`
	// FallbacksEnabled appends this type's legacy chain to FormatOrder.
	FallbacksEnabled bool `json:"fallbacksEnabled"`
}

// AssetPreferences is the persisted user configuration for asset resolution
// and pairing. All exported methods are safe for concurrent use.
type AssetPreferences struct {
	GlobalFallbacksEnabled bool                         `json:"globalFallbacksEnabled"`
	PreferAnimated         bool                         `json:"preferAnimated"`
	EmoteButtonImages      bool                         `json:"emoteButtonImages"`
	SmoothScaling          bool                         `json:"smoothScaling"`
	UpdateCheck            bool                         `json:"updateCheck"`
	HighlightColor         int                          `json:"highlightColor"`
	BgSlideshow            bool                         `json:"bgSlideshow"`
	BgSlideshowSecs        int                          `json:"bgSlideshowSecs"`
	DebugOverlay           bool                         `json:"debugOverlay"`
	AutoDetectFormats      bool                         `json:"formatAutoDetect"`
	ThemeLayoutOn          bool                         `json:"themeLayout"`
	UIScaleAutoOn          bool                         `json:"uiScaleAuto"`
	CatchUpOn              bool                         `json:"catchUpWhenBehind"`
	CatchUpThreshold       int                          `json:"catchUpThreshold"`
	MultiTabCap            int                          `json:"multiTabCap"`
	ReduceMotionOn         bool                         `json:"reduceMotion"`
	MusicDuckingOn         bool                         `json:"musicDucking"`
	FontOverridePaths      string                       `json:"fontPaths"`
	UserMacros             []MacroSpec                  `json:"macros,omitempty"`
	ThemeRectOv            map[string]map[string][4]int `json:"themeRectOverrides,omitempty"`
	DiskZstd               bool                         `json:"diskZstd"`
	StreamerModeOn         bool                         `json:"streamerMode"`
	ThemeName              string                       `json:"themeName"`
	ThemeDir               string                       `json:"themeDir"`
	OOCName                string                       `json:"oocName"`
	ViewportPct            int                          `json:"viewportPercent"`
	ChatScalePct           int                          `json:"chatScalePercent"`
	ChatBoxPct             int                          `json:"chatBoxPercent"`
	LogScalePct            int                          `json:"logScalePercent"`
	InputHeightPct         int                          `json:"inputHeightPercent"`
	UIScalePct             int                          `json:"uiScalePercent"`
	MusicVol               int                          `json:"musicVolume"`
	SFXVol                 int                          `json:"sfxVolume"`
	BlipVol                int                          `json:"blipVolume"`
	TextCrawlMs            int                          `json:"textCrawlMs"`
	TextStayMs             int                          `json:"textStayMs"`
	ChatRateLimitMs        int                          `json:"chatRateLimitMs"`
	MasterListURL          string                       `json:"masterListUrl"`
	AssetTypes             map[string]AssetTypePrefs    `json:"assetTypes"`
	LearnedFormats         map[string][]string          `json:"learnedFormats"`
	PairOffsetX            int                          `json:"pairOffsetX"`
	PairOffsetY            int                          `json:"pairOffsetY"`
	PairFlip               bool                         `json:"pairFlip"`
	Showname               string                       `json:"showname"`
	LocalAssetsEnabled     bool                         `json:"localAssetsEnabled"`
	LocalAssetsPaths       []string                     `json:"localAssetsPaths"`
	Favorites              []FavoriteServer             `json:"favorites"`
	Wardrobe               []string                     `json:"wardrobe"`
	CasingEnabled          bool                         `json:"casingEnabled"`
	CasingRoles            int                          `json:"casingRoles"`
	HiddenPanelIDs         []string                     `json:"hiddenPanels"`
	ServerWarm             map[string]ServerWarmInfo    `json:"serverWarm"`
	CallWordList           []string                     `json:"callWords"`
	HotkeyMap              map[string]string            `json:"hotkeys"`
	DiscordRPC             DiscordPrefs                 `json:"discord"`
	// CharDownloaderOn enables the opt-in single-character/background
	// downloader (off by default — it writes files to disk on demand).
	CharDownloaderOn bool `json:"charDownloader"`

	mu        sync.RWMutex
	path      string
	dirty     chan struct{} // buffered 1: wake-up signal for the saver
	stop      chan struct{}
	done      chan struct{}
	pending   atomic.Bool // a mutation is awaiting flush
	saveDelay time.Duration
	closeOnce sync.Once
	onSaveErr atomic.Pointer[func(error)]
	// frozen blocks every further write after ImportSettings replaced the
	// file on disk — the live session's saves would clobber the import.
	frozen atomic.Bool

	// formatGen increments on every mutation that changes any effective
	// probe list (format orders, fallback toggles). Consumers cache derived
	// format tables keyed by this generation — see Resolver's miss path.
	formatGen atomic.Uint64
}

// prefsJSON mirrors the on-disk shape for loading. Pointer fields distinguish
// "absent" from the zero value where the default is not the zero value.
type prefsJSON struct {
	GlobalFallbacksEnabled bool  `json:"globalFallbacksEnabled"`
	PreferAnimated         *bool `json:"preferAnimated"`
	EmoteButtonImages      *bool `json:"emoteButtonImages"`
	SmoothScaling          *bool `json:"smoothScaling"`
	UpdateCheck            *bool `json:"updateCheck"`    // absent = default ON
	HighlightColor         *int  `json:"highlightColor"` // absent = default accent
	BgSlideshow            bool  `json:"bgSlideshow"`    // default OFF (zero value)
	BgSlideshowSecs        int   `json:"bgSlideshowSecs"`
	DebugOverlay           bool  `json:"debugOverlay"`
	FormatAutoDetect       *bool `json:"formatAutoDetect"`  // absent = default ON
	ThemeLayout            *bool `json:"themeLayout"`       // absent = default ON
	UIScaleAuto            *bool `json:"uiScaleAuto"`       // absent = default ON (HiDPI)
	CatchUpWhenBehind      *bool `json:"catchUpWhenBehind"` // absent = default ON
	CatchUpThreshold       *int  `json:"catchUpThreshold"`  // absent = default
	MultiTabCap            *int  `json:"multiTabCap"`       // absent = default
	ReduceMotion           bool  `json:"reduceMotion"`      // default OFF (zero value)
	MusicDucking           bool  `json:"musicDucking"`      // default OFF (zero value)

	FontPaths          string                       `json:"fontPaths"` // ""=embedded font
	Macros             []MacroSpec                  `json:"macros"`
	ThemeRectOverrides map[string]map[string][4]int `json:"themeRectOverrides"`
	DiskZstd           bool                         `json:"diskZstd"`     // default OFF (measured trade)
	StreamerMode       bool                         `json:"streamerMode"` // default OFF
	ThemeName          string                       `json:"themeName"`
	ThemeDir           string                       `json:"themeDir"`
	OOCName            string                       `json:"oocName"`
	ViewportPct        int                          `json:"viewportPercent"`
	ChatScalePct       int                          `json:"chatScalePercent"`
	ChatBoxPct         int                          `json:"chatBoxPercent"`
	LogScalePct        int                          `json:"logScalePercent"`
	InputHeightPct     int                          `json:"inputHeightPercent"`
	UIScalePct         int                          `json:"uiScalePercent"`
	// Volumes use pointers: 0 is a real value (mute), absent means 100.
	MusicVol *int `json:"musicVolume"`
	SFXVol   *int `json:"sfxVolume"`
	BlipVol  *int `json:"blipVolume"`
	// Stay/ratelimit use pointers too: 0 means "no linger" / "off".
	TextCrawlMs        int                       `json:"textCrawlMs"`
	TextStayMs         *int                      `json:"textStayMs"`
	ChatRateLimitMs    *int                      `json:"chatRateLimitMs"`
	MasterListURL      string                    `json:"masterListUrl"`
	AssetTypes         map[string]AssetTypePrefs `json:"assetTypes"`
	LearnedFormats     map[string][]string       `json:"learnedFormats"`
	PairOffsetX        int                       `json:"pairOffsetX"`
	PairOffsetY        int                       `json:"pairOffsetY"`
	PairFlip           bool                      `json:"pairFlip"`
	Showname           string                    `json:"showname"`
	LocalAssetsEnabled bool                      `json:"localAssetsEnabled"`
	LocalAssetsPaths   []string                  `json:"localAssetsPaths"`
	Favorites          []FavoriteServer          `json:"favorites"`
	Wardrobe           []string                  `json:"wardrobe"`
	CasingEnabled      bool                      `json:"casingEnabled"`
	CasingRoles        int                       `json:"casingRoles"`
	HiddenPanels       []string                  `json:"hiddenPanels"`
	ServerWarm         map[string]ServerWarmInfo `json:"serverWarm"`
	CallWords          []string                  `json:"callWords"`
	Hotkeys            map[string]string         `json:"hotkeys"`
	Discord            *DiscordPrefs             `json:"discord"`
	CharDownloader     bool                      `json:"charDownloader"`
}

// DiscordPrefs configures the OPTIONAL Rich Presence integration.
// Disabled by default; when first enabled, showname + character + server
// display and the area stays hidden (some players don't want their
// location broadcast — every field is the user's choice).
type DiscordPrefs struct {
	Enabled    bool `json:"enabled"`
	ShowServer bool `json:"showServer"`
	ShowChar   bool `json:"showChar"`
	ShowName   bool `json:"showName"`
	ShowArea   bool `json:"showArea"`
	// AppID is the user's Discord application ID (developer portal app
	// named AsyncAO with the icon uploaded as asset "appicon").
	AppID string `json:"appId"`
}

// defaultDiscordPrefs: off, with the tick-on defaults pre-set.
func defaultDiscordPrefs() DiscordPrefs {
	return DiscordPrefs{ShowServer: true, ShowChar: true, ShowName: true}
}

// ServerWarmInfo remembers what a server looked like last visit, so a
// reconnect can pre-warm it (last character used, last background seen).
// It also owns the server-scoped player state: the wardrobe (custom
// character list — per server since wardrobes carrying between servers
// was a playtest bug) and the character keybinds.
type ServerWarmInfo struct {
	Char       string `json:"char,omitempty"`
	Background string `json:"background,omitempty"`
	// Wardrobe is this server's custom character list (≤ WardrobeCap).
	Wardrobe []string `json:"wardrobe,omitempty"`
	// FavBackgrounds are this server's starred backgrounds (≤ FavBackgroundCap)
	// — pinned in the background picker so they float to the top and can be
	// filtered to on their own, even on hosts with no directory listing.
	FavBackgrounds []string `json:"favBackgrounds,omitempty"`
	// FavBackgroundFolder maps a lowercased favourite background to its folder
	// (category) for the wardrobe's Backgrounds section; absent = unfiled.
	// Mirrors WardrobeFolder for characters.
	FavBackgroundFolder map[string]string `json:"favBackgroundFolder,omitempty"`
	// CharKeys maps a plain key name ("a", "5", "f3") to a character to
	// wear when pressed in the courtroom with no text field focused.
	CharKeys map[string]string `json:"charKeys,omitempty"`
	// WardrobeFolder maps a lowercased wardrobe character to its folder
	// (category) for organizing the wardrobe; absent = unsorted.
	WardrobeFolder map[string]string `json:"wardrobeFolder,omitempty"`
	// Origin is the server's asset URL from the last visit — rehearsal
	// mode rebuilds asset paths from it without connecting.
	Origin string `json:"origin,omitempty"`
	// Chars is the server's character list from the last visit
	// (≤ WarmCharsCap) — the rehearsal char select.
	Chars []string `json:"chars,omitempty"`
	// Account login for this server — NOT mod-only: servers with user
	// account systems (Akashi and tsuserver-family forks) hang member
	// perks off /login too, and the same flow signs either in. PLAINTEXT
	// in the prefs file — the same trust level as a saved browser
	// password without an OS keychain; the settings UI says so out loud.
	// AutoLogin sends the flow on every join.
	LoginUser string `json:"loginUser,omitempty"`
	LoginPass string `json:"loginPass,omitempty"`
	AutoLogin bool   `json:"autoLogin,omitempty"`
	// Theme binds a theme to this server ("" = the global pick): joining
	// applies it, disconnecting restores the global theme.
	Theme string `json:"theme,omitempty"`
}

// charKeyCap bounds one server's character keybind table.
const charKeyCap = 36

// MacroSpec is one user macro: a name, an optional plain-key bind, and
// the OOC lines it sends in order (paced by the UI so prompt-style
// flows like Akashi's two-step login work).
type MacroSpec struct {
	Name  string   `json:"name"`
	Key   string   `json:"key,omitempty"`
	Lines []string `json:"lines"`
}

// Macro bounds (rule §17.4): "as many as you want" within named caps.
const (
	MacroCap      = 64  // macros per user
	MacroLinesCap = 8   // lines per macro
	MacroLineMax  = 256 // chars per line
)

// sanitizeMacros enforces the macro caps and drops empty entries.
func sanitizeMacros(in []MacroSpec) []MacroSpec {
	if len(in) > MacroCap {
		in = in[:MacroCap]
	}
	out := make([]MacroSpec, 0, len(in))
	for _, m := range in {
		m.Name = strings.TrimSpace(m.Name)
		m.Key = strings.ToLower(strings.TrimSpace(m.Key))
		if len(m.Lines) > MacroLinesCap {
			m.Lines = m.Lines[:MacroLinesCap]
		}
		lines := make([]string, 0, len(m.Lines))
		for _, l := range m.Lines {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			if len(l) > MacroLineMax {
				l = l[:MacroLineMax]
			}
			lines = append(lines, l)
		}
		m.Lines = lines
		if m.Name == "" || len(m.Lines) == 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

// WarmCharsCap bounds one server's remembered character list. 4096 names
// ≈ 60 KiB of JSON — covers the 4000-char megaservers rehearsal exists
// for, while the serverWarmCap (64) keeps the whole table bounded.
const WarmCharsCap = 4096

// serverWarmCap bounds the per-server warm table (rule §17.4); when full,
// new servers simply don't record until old entries are cleared.
const serverWarmCap = 64

// FavoriteServer is a starred or direct-connect server entry (the server
// phone book). URL is the full ws:// or wss:// connection address, which
// also works for private servers that never appear on the master list. The
// description is kept so starred servers stay informative even when the
// master list is unreachable.
type FavoriteServer struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// DefaultPath returns the standard preferences file location:
// <os.UserConfigDir>/AsyncAO/asset_preferences.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating user config dir: %w", err)
	}
	return filepath.Join(dir, PrefsDirName, PrefsFileName), nil
}

// New loads preferences from path (built-in defaults when the file is absent
// or unreadable) and starts the debounced saver goroutine. The returned
// preferences are always usable; a non-nil error reports a malformed or
// unreadable existing file that was replaced by defaults in memory.
// Call Close to flush pending changes and stop the saver.
func New(path string) (*AssetPreferences, error) {
	return newWithDebounce(path, DefaultSaveDebounce)
}

func newWithDebounce(path string, debounce time.Duration) (*AssetPreferences, error) {
	p, err := load(path)
	p.saveDelay = debounce
	p.dirty = make(chan struct{}, 1)
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	go p.saverLoop()
	return p, err
}

// load reads and normalizes the preferences file without starting the saver.
func load(path string) (*AssetPreferences, error) {
	p := &AssetPreferences{
		PreferAnimated:    defaultPreferAnimated,
		EmoteButtonImages: defaultEmoteButtonImages,
		SmoothScaling:     defaultSmoothScaling,
		UpdateCheck:       defaultUpdateCheck,
		HighlightColor:    defaultHighlightColor,
		BgSlideshowSecs:   defaultBgSlideshowSecs,
		AutoDetectFormats: defaultFormatAutoDetect,
		ThemeLayoutOn:     defaultThemeLayout,
		UIScaleAutoOn:     defaultUIScaleAuto,
		CatchUpOn:         defaultCatchUpWhenBehind,
		CatchUpThreshold:  DefaultCatchUpThreshold,
		MultiTabCap:       DefaultMultiTabCap,
		DiscordRPC:        defaultDiscordPrefs(),
		ViewportPct:       DefaultViewportPercent,
		ChatScalePct:      DefaultScalePercent,
		ChatBoxPct:        DefaultScalePercent,
		LogScalePct:       DefaultScalePercent,
		InputHeightPct:    DefaultScalePercent,
		UIScalePct:        DefaultScalePercent,
		MusicVol:          defaultAudioVolume,
		SFXVol:            defaultAudioVolume,
		BlipVol:           defaultAudioVolume,
		TextCrawlMs:       DefaultTextCrawlMs,
		TextStayMs:        DefaultTextStayMs,
		ChatRateLimitMs:   DefaultChatRateLimitMs,
		AssetTypes:        defaultAssetTypes(),
		LearnedFormats:    map[string][]string{},
		path:              path,
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil // first run
	}
	if err != nil {
		return p, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var onDisk prefsJSON
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return p, fmt.Errorf("config: parsing %s (using defaults): %w", path, err)
	}

	p.GlobalFallbacksEnabled = onDisk.GlobalFallbacksEnabled
	if onDisk.PreferAnimated != nil {
		p.PreferAnimated = *onDisk.PreferAnimated
	}
	if onDisk.EmoteButtonImages != nil {
		p.EmoteButtonImages = *onDisk.EmoteButtonImages
	}
	if onDisk.SmoothScaling != nil {
		p.SmoothScaling = *onDisk.SmoothScaling
	}
	if onDisk.UpdateCheck != nil {
		p.UpdateCheck = *onDisk.UpdateCheck
	}
	if onDisk.HighlightColor != nil {
		p.HighlightColor = *onDisk.HighlightColor & 0xFFFFFF
	}
	p.BgSlideshow = onDisk.BgSlideshow
	if onDisk.BgSlideshowSecs > 0 { // 0 (absent) keeps the New() default
		p.BgSlideshowSecs = onDisk.BgSlideshowSecs
	}
	p.DebugOverlay = onDisk.DebugOverlay
	p.CharDownloaderOn = onDisk.CharDownloader
	if onDisk.FormatAutoDetect != nil {
		p.AutoDetectFormats = *onDisk.FormatAutoDetect
	}
	if onDisk.ThemeLayout != nil {
		p.ThemeLayoutOn = *onDisk.ThemeLayout
	}
	if onDisk.UIScaleAuto != nil {
		p.UIScaleAutoOn = *onDisk.UIScaleAuto
	}
	if onDisk.CatchUpWhenBehind != nil {
		p.CatchUpOn = *onDisk.CatchUpWhenBehind
	}
	if onDisk.CatchUpThreshold != nil {
		p.CatchUpThreshold = clampPercent(*onDisk.CatchUpThreshold, catchUpThresholdMin, catchUpThresholdMax)
	}
	if onDisk.MultiTabCap != nil {
		p.MultiTabCap = clampPercent(*onDisk.MultiTabCap, minMultiTabCap, maxMultiTabCap)
	}
	p.ReduceMotionOn = onDisk.ReduceMotion
	p.MusicDuckingOn = onDisk.MusicDucking
	p.FontOverridePaths = onDisk.FontPaths
	p.UserMacros = sanitizeMacros(onDisk.Macros)
	p.ThemeRectOv = onDisk.ThemeRectOverrides
	p.DiskZstd = onDisk.DiskZstd
	p.StreamerModeOn = onDisk.StreamerMode
	p.ThemeName = onDisk.ThemeName
	p.ThemeDir = onDisk.ThemeDir
	p.OOCName = onDisk.OOCName
	// Zero percents mean "absent" (no scale is validly 0); volumes use
	// pointers because 0 = mute is real.
	if onDisk.ViewportPct != 0 {
		p.ViewportPct = clampPercent(onDisk.ViewportPct, MinViewportPercent, MaxViewportPercent)
	}
	if onDisk.ChatScalePct != 0 {
		p.ChatScalePct = clampPercent(onDisk.ChatScalePct, MinChatScalePercent, MaxChatScalePercent)
	}
	if onDisk.ChatBoxPct != 0 {
		p.ChatBoxPct = clampPercent(onDisk.ChatBoxPct, MinChatBoxPercent, MaxChatBoxPercent)
	}
	if onDisk.LogScalePct != 0 {
		p.LogScalePct = clampPercent(onDisk.LogScalePct, MinLogScalePercent, MaxLogScalePercent)
	}
	if onDisk.InputHeightPct != 0 {
		p.InputHeightPct = clampPercent(onDisk.InputHeightPct, MinInputPercent, MaxInputPercent)
	}
	if onDisk.UIScalePct != 0 {
		p.UIScalePct = clampPercent(onDisk.UIScalePct, MinUIScalePercent, MaxUIScalePercent)
	}
	if onDisk.MusicVol != nil {
		p.MusicVol = clampPercent(*onDisk.MusicVol, 0, defaultAudioVolume)
	}
	if onDisk.SFXVol != nil {
		p.SFXVol = clampPercent(*onDisk.SFXVol, 0, defaultAudioVolume)
	}
	if onDisk.BlipVol != nil {
		p.BlipVol = clampPercent(*onDisk.BlipVol, 0, defaultAudioVolume)
	}
	if onDisk.TextCrawlMs != 0 {
		p.TextCrawlMs = clampPercent(onDisk.TextCrawlMs, MinTextCrawlMs, MaxTextCrawlMs)
	}
	if onDisk.TextStayMs != nil {
		p.TextStayMs = clampPercent(*onDisk.TextStayMs, 0, MaxTextStayMs)
	}
	if onDisk.ChatRateLimitMs != nil {
		p.ChatRateLimitMs = clampPercent(*onDisk.ChatRateLimitMs, 0, MaxChatRateLimitMs)
	}
	p.MasterListURL = onDisk.MasterListURL
	for name, tp := range onDisk.AssetTypes {
		if len(tp.FormatOrder) == 0 {
			tp.FormatOrder = DefaultFormatOrder(name)
		}
		p.AssetTypes[name] = tp
	}
	if onDisk.LearnedFormats != nil {
		p.LearnedFormats = onDisk.LearnedFormats
	}
	p.PairOffsetX = clampPairOffset(onDisk.PairOffsetX)
	p.PairOffsetY = clampPairOffset(onDisk.PairOffsetY)
	p.PairFlip = onDisk.PairFlip
	p.Showname = onDisk.Showname
	p.LocalAssetsEnabled = onDisk.LocalAssetsEnabled
	p.LocalAssetsPaths = onDisk.LocalAssetsPaths
	p.Favorites = onDisk.Favorites
	if len(onDisk.Wardrobe) > WardrobeCap {
		onDisk.Wardrobe = onDisk.Wardrobe[:WardrobeCap]
	}
	p.Wardrobe = onDisk.Wardrobe
	p.CasingEnabled = onDisk.CasingEnabled
	p.CasingRoles = onDisk.CasingRoles
	p.HiddenPanelIDs = onDisk.HiddenPanels
	if onDisk.ServerWarm != nil {
		p.ServerWarm = onDisk.ServerWarm
	}
	p.CallWordList = onDisk.CallWords
	if onDisk.Hotkeys != nil {
		p.HotkeyMap = onDisk.Hotkeys
	}
	if onDisk.Discord != nil {
		p.DiscordRPC = *onDisk.Discord
	}
	return p, nil
}

// SetSaveErrorHook installs fn to receive asynchronous save failures from the
// saver goroutine. The default hook logs via the standard logger.
func (p *AssetPreferences) SetSaveErrorHook(fn func(error)) {
	p.onSaveErr.Store(&fn)
}

func (p *AssetPreferences) reportSaveError(err error) {
	if fn := p.onSaveErr.Load(); fn != nil {
		(*fn)(err)
		return
	}
	log.Printf("config: async save failed: %v", err)
}

// markDirty records a pending change and wakes the saver without blocking,
// regardless of how many signals are already queued.
func (p *AssetPreferences) markDirty() {
	p.pending.Store(true)
	select {
	case p.dirty <- struct{}{}:
	default:
	}
}

// saverLoop debounces dirty signals and flushes preferences to disk. It owns
// no locks while idle and never blocks mutators.
func (p *AssetPreferences) saverLoop() {
	defer close(p.done)
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case <-p.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-p.dirty:
			// Restart the debounce window on every new mutation.
			if timer == nil {
				timer = time.NewTimer(p.saveDelay)
				timerC = timer.C
			} else {
				timer.Reset(p.saveDelay)
			}
		case <-timerC:
			if err := p.SaveNow(); err != nil {
				p.reportSaveError(err)
			}
		}
	}
}

// SaveNow synchronously marshals current preferences and atomically replaces
// the preferences file (temp file + rename). It is intended for shutdown and
// Settings-Apply; routine mutations rely on the debounced saver instead.
func (p *AssetPreferences) SaveNow() error {
	if p.frozen.Load() {
		return nil // an import owns the file now; restart applies it
	}
	// Clear before marshaling: a concurrent mutation re-marks pending and is
	// picked up by the next flush even if this marshal misses it.
	p.pending.Store(false)

	p.mu.RLock()
	data, err := json.MarshalIndent(p, jsonMarshalPrefix, jsonMarshalIndent)
	p.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("config: marshaling preferences: %w", err)
	}
	return atomicWriteFile(p.path, data, prefsFilePerm)
}

// Close stops the saver goroutine and flushes any pending change. It is safe
// to call multiple times; only the first call does work.
func (p *AssetPreferences) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.stop)
		<-p.done
		if p.pending.Load() {
			err = p.SaveNow()
		}
	})
	return err
}

// atomicWriteFile writes data to a temp file in path's directory, syncs it,
// and renames it over path so readers never observe a partial file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, prefsDirPerm); err != nil {
		return fmt.Errorf("config: creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, prefsTmpPattern)
	if err != nil {
		return fmt.Errorf("config: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(fmt.Errorf("config: writing %s: %w", tmpName, err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("config: syncing %s: %w", tmpName, err))
	}
	if err := tmp.Close(); err != nil {
		return cleanup(fmt.Errorf("config: closing %s: %w", tmpName, err))
	}
	// Best effort: CreateTemp uses 0600; widen to perm where supported.
	_ = os.Chmod(tmpName, perm)
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// --- Format lists -----------------------------------------------------------

// FormatGeneration returns the probe-list generation: it changes whenever
// any SetFormatOrder/SetTypeFallbacks/SetGlobalFallbacks mutation lands, so
// derived caches know when to rebuild without holding locks.
func (p *AssetPreferences) FormatGeneration() uint64 {
	return p.formatGen.Load()
}

// FormatList implements spec §4: with fallbacks OFF it returns exactly
// the configured probe list for the type; with fallbacks ON (globally or for
// this type) it returns the configured list plus the type's legacy chain,
// deduplicated, order preserved. The result is a fresh slice.
func (p *AssetPreferences) FormatList(typeName string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	tp, ok := p.AssetTypes[typeName]
	order := tp.FormatOrder
	if !ok || len(order) == 0 {
		order = defaultFormatOrders[typeName]
	}

	withFallbacks := p.GlobalFallbacksEnabled || tp.FallbacksEnabled
	capacity := len(order)
	if withFallbacks {
		capacity += len(legacyFallbackChains[typeName])
	}
	list := make([]string, 0, capacity)
	for _, ext := range order {
		if !slices.Contains(list, ext) {
			list = append(list, ext)
		}
	}
	if withFallbacks {
		for _, ext := range legacyFallbackChains[typeName] {
			if !slices.Contains(list, ext) {
				list = append(list, ext)
			}
		}
	}
	return list
}

// FormatOrder returns the configured (pre-fallback) probe order for a type.
func (p *AssetPreferences) FormatOrder(typeName string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if tp, ok := p.AssetTypes[typeName]; ok && len(tp.FormatOrder) > 0 {
		return cloneStrings(tp.FormatOrder)
	}
	return DefaultFormatOrder(typeName)
}

// SetFormatOrder replaces the probe order for a type and invalidates learned
// formats for that type on every host (the learned format may no longer be
// first preference). No-op when the order is unchanged.
func (p *AssetPreferences) SetFormatOrder(typeName string, order []string) {
	p.mu.Lock()
	tp := p.AssetTypes[typeName]
	if slices.Equal(tp.FormatOrder, order) {
		p.mu.Unlock()
		return
	}
	tp.FormatOrder = cloneStrings(order)
	if p.AssetTypes == nil {
		p.AssetTypes = map[string]AssetTypePrefs{}
	}
	p.AssetTypes[typeName] = tp
	p.dropLearnedTypeLocked(typeName)
	p.mu.Unlock()
	p.formatGen.Add(1)
	p.markDirty()
}

// ResetFormatOrder restores the built-in default order for a type and
// invalidates learned formats for that type.
func (p *AssetPreferences) ResetFormatOrder(typeName string) {
	p.SetFormatOrder(typeName, DefaultFormatOrder(typeName))
}

// TypeFallbacksEnabled reports whether the legacy chain is enabled for the
// type specifically (the global toggle is separate).
func (p *AssetPreferences) TypeFallbacksEnabled(typeName string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AssetTypes[typeName].FallbacksEnabled
}

// SetTypeFallbacks toggles the legacy chain for one type and invalidates
// learned formats for that type. No-op when unchanged.
func (p *AssetPreferences) SetTypeFallbacks(typeName string, enabled bool) {
	p.mu.Lock()
	tp := p.AssetTypes[typeName]
	if tp.FallbacksEnabled == enabled {
		p.mu.Unlock()
		return
	}
	tp.FallbacksEnabled = enabled
	if len(tp.FormatOrder) == 0 {
		tp.FormatOrder = DefaultFormatOrder(typeName)
	}
	if p.AssetTypes == nil {
		p.AssetTypes = map[string]AssetTypePrefs{}
	}
	p.AssetTypes[typeName] = tp
	p.dropLearnedTypeLocked(typeName)
	p.mu.Unlock()
	p.formatGen.Add(1)
	p.markDirty()
}

// GlobalFallbacks reports the global fallback toggle.
func (p *AssetPreferences) GlobalFallbacks() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.GlobalFallbacksEnabled
}

// SetGlobalFallbacks toggles fallbacks for every type and invalidates all
// learned formats (every type's effective probe list changed).
func (p *AssetPreferences) SetGlobalFallbacks(enabled bool) {
	p.mu.Lock()
	if p.GlobalFallbacksEnabled == enabled {
		p.mu.Unlock()
		return
	}
	p.GlobalFallbacksEnabled = enabled
	p.LearnedFormats = map[string][]string{}
	p.mu.Unlock()
	p.formatGen.Add(1)
	p.markDirty()
}

// --- Animation toggle -------------------------------------------------------

// AnimationsEnabled reports the PreferAnimated decode/render toggle.
func (p *AssetPreferences) AnimationsEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PreferAnimated
}

// SetAnimationsEnabled toggles animation playback (ON plays animation frames,
// OFF renders first frames only). Purely decode/render-level: it never
// changes the probe list.
func (p *AssetPreferences) SetAnimationsEnabled(enabled bool) {
	p.mu.Lock()
	if p.PreferAnimated == enabled {
		p.mu.Unlock()
		return
	}
	p.PreferAnimated = enabled
	p.mu.Unlock()
	p.markDirty()
}

// --- Emote button images ----------------------------------------------------

// EmoteButtonImagesEnabled reports whether the courtroom emote picker draws
// the character's emotions/button<N> art (text chips when off).
func (p *AssetPreferences) EmoteButtonImagesEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.EmoteButtonImages
}

// SetEmoteButtonImages toggles image emote buttons. Render-level only: the
// probe list for the EmoteButton type is configured separately.
func (p *AssetPreferences) SetEmoteButtonImages(enabled bool) {
	p.mu.Lock()
	if p.EmoteButtonImages == enabled {
		p.mu.Unlock()
		return
	}
	p.EmoteButtonImages = enabled
	p.mu.Unlock()
	p.markDirty()
}

// --- Smooth scaling -----------------------------------------------------------

// SmoothScalingEnabled reports the linear texture-filtering toggle.
func (p *AssetPreferences) SmoothScalingEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SmoothScaling
}

// SetSmoothScaling toggles linear texture filtering. The UI re-streams
// resident textures so it applies live (the SDL hint only affects
// textures created after it changes).
func (p *AssetPreferences) SetSmoothScaling(enabled bool) {
	p.mu.Lock()
	if p.SmoothScaling == enabled {
		p.mu.Unlock()
		return
	}
	p.SmoothScaling = enabled
	p.mu.Unlock()
	p.markDirty()
}

// --- Update check ------------------------------------------------------------

// UpdateCheckEnabled reports whether the one-shot launch update check (M13)
// runs. On by default; disabling it stops the outbound GitHub Releases call.
func (p *AssetPreferences) UpdateCheckEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.UpdateCheck
}

// SetUpdateCheck toggles the launch update check.
func (p *AssetPreferences) SetUpdateCheck(enabled bool) {
	p.mu.Lock()
	if p.UpdateCheck == enabled {
		p.mu.Unlock()
		return
	}
	p.UpdateCheck = enabled
	p.mu.Unlock()
	p.markDirty()
}

// HighlightColorRGB returns the packed 0xRRGGBB log-selection highlight colour.
func (p *AssetPreferences) HighlightColorRGB() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HighlightColor & 0xFFFFFF
}

// SetHighlightColor stores a packed 0xRRGGBB log-selection highlight colour.
func (p *AssetPreferences) SetHighlightColor(rgb int) {
	rgb &= 0xFFFFFF
	p.mu.Lock()
	if p.HighlightColor == rgb {
		p.mu.Unlock()
		return
	}
	p.HighlightColor = rgb
	p.mu.Unlock()
	p.markDirty()
}

// BgSlideshowEnabled reports the idle background-slideshow toggle (OFF default).
func (p *AssetPreferences) BgSlideshowEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.BgSlideshow
}

// SetBgSlideshow toggles the idle background slideshow.
func (p *AssetPreferences) SetBgSlideshow(on bool) {
	p.mu.Lock()
	if p.BgSlideshow == on {
		p.mu.Unlock()
		return
	}
	p.BgSlideshow = on
	p.mu.Unlock()
	p.markDirty()
}

// BgSlideshowSeconds is the per-background dwell time, clamped to its bounds
// (an unset/zero value falls back to the default).
func (p *AssetPreferences) BgSlideshowSeconds() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.BgSlideshowSecs <= 0 {
		return defaultBgSlideshowSecs
	}
	return clampPercent(p.BgSlideshowSecs, minBgSlideshowSecs, maxBgSlideshowSecs)
}

// SetBgSlideshowSeconds clamps and persists the slideshow dwell time.
func (p *AssetPreferences) SetBgSlideshowSeconds(n int) {
	n = clampPercent(n, minBgSlideshowSecs, maxBgSlideshowSecs)
	p.mu.Lock()
	if p.BgSlideshowSecs == n {
		p.mu.Unlock()
		return
	}
	p.BgSlideshowSecs = n
	p.mu.Unlock()
	p.markDirty()
}

// --- Theme -------------------------------------------------------------------

// Theme reports the selected theme name ("" = default) and the custom theme
// root directory ("" = no custom root configured).
func (p *AssetPreferences) Theme() (name, dir string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ThemeName, p.ThemeDir
}

// SetTheme stores the selected theme name and custom theme root.
func (p *AssetPreferences) SetTheme(name, dir string) {
	p.mu.Lock()
	if p.ThemeName == name && p.ThemeDir == dir {
		p.mu.Unlock()
		return
	}
	p.ThemeName = name
	p.ThemeDir = dir
	p.mu.Unlock()
	p.markDirty()
}

// --- Debug overlay -------------------------------------------------------------

// DebugOverlayEnabled reports whether the on-screen failure log draws.
func (p *AssetPreferences) DebugOverlayEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DebugOverlay
}

// SetDebugOverlay toggles the on-screen failure log.
func (p *AssetPreferences) SetDebugOverlay(enabled bool) {
	p.mu.Lock()
	if p.DebugOverlay == enabled {
		p.mu.Unlock()
		return
	}
	p.DebugOverlay = enabled
	p.mu.Unlock()
	p.markDirty()
}

// CharDownloaderEnabled reports whether the opt-in single-asset downloader
// is on (off by default — it writes files to disk).
func (p *AssetPreferences) CharDownloaderEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CharDownloaderOn
}

// SetCharDownloader toggles the opt-in single-asset downloader.
func (p *AssetPreferences) SetCharDownloader(enabled bool) {
	p.mu.Lock()
	if p.CharDownloaderOn == enabled {
		p.mu.Unlock()
		return
	}
	p.CharDownloaderOn = enabled
	p.mu.Unlock()
	p.markDirty()
}

// --- Casing alerts ---------------------------------------------------------------

// Casing reports the CASEA subscription: enabled + a CaseRole* bitmask
// (the bit layout is internal/courtroom's; config just persists it).
func (p *AssetPreferences) Casing() (enabled bool, roles int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CasingEnabled, p.CasingRoles
}

// SetCasing stores the CASEA subscription.
func (p *AssetPreferences) SetCasing(enabled bool, roles int) {
	p.mu.Lock()
	if p.CasingEnabled == enabled && p.CasingRoles == roles {
		p.mu.Unlock()
		return
	}
	p.CasingEnabled = enabled
	p.CasingRoles = roles
	p.mu.Unlock()
	p.markDirty()
}

// --- Discord Rich Presence ------------------------------------------------------------

// Discord reports the Rich Presence configuration.
func (p *AssetPreferences) Discord() DiscordPrefs {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DiscordRPC
}

// SetDiscord stores the Rich Presence configuration.
func (p *AssetPreferences) SetDiscord(dp DiscordPrefs) {
	p.mu.Lock()
	if p.DiscordRPC == dp {
		p.mu.Unlock()
		return
	}
	p.DiscordRPC = dp
	p.mu.Unlock()
	p.markDirty()
}

// --- Callwords + hotkeys -------------------------------------------------------------

// callWordCap bounds the highlight list (rule §17.4).
const callWordCap = 32

// CallWords reports the highlight words (lowercased on save).
func (p *AssetPreferences) CallWords() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneStrings(p.CallWordList)
}

// SetCallWords replaces the highlight list (entries lowercased, capped).
func (p *AssetPreferences) SetCallWords(words []string) {
	clean := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		clean = append(clean, w)
		if len(clean) >= callWordCap {
			break
		}
	}
	p.mu.Lock()
	p.CallWordList = clean
	p.mu.Unlock()
	p.markDirty()
}

// Hotkey reports the configured key name for an action ("" = unset; the
// UI layer owns defaults — config just persists overrides).
func (p *AssetPreferences) Hotkey(action string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HotkeyMap[action]
}

// SetHotkey stores one action's key name.
func (p *AssetPreferences) SetHotkey(action, key string) {
	p.mu.Lock()
	if p.HotkeyMap == nil {
		p.HotkeyMap = map[string]string{}
	}
	if p.HotkeyMap[action] == key {
		p.mu.Unlock()
		return
	}
	p.HotkeyMap[action] = key
	p.mu.Unlock()
	p.markDirty()
}

// --- Per-server warm state -----------------------------------------------------------

// ServerWarmInfoFor reports the remembered state for a server key.
func (p *AssetPreferences) ServerWarmInfoFor(key string) ServerWarmInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ServerWarm[key]
}

// RememberServerChar records the character last used on a server.
func (p *AssetPreferences) RememberServerChar(key, char string) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Char = char })
}

// RememberServerBackground records the background last seen on a server.
func (p *AssetPreferences) RememberServerBackground(key, bg string) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Background = bg })
}

// RememberServerOrigin records the asset origin for rehearsal mode.
func (p *AssetPreferences) RememberServerOrigin(key, origin string) {
	if origin == "" {
		return
	}
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Origin = origin })
}

// SetServerLogin stores a server's mod credentials (plaintext — see
// ServerWarmInfo) and the auto-login-on-join choice. Empty user+pass
// clears them.
func (p *AssetPreferences) SetServerLogin(key, user, pass string, auto bool) {
	p.rememberServer(key, func(w *ServerWarmInfo) {
		w.LoginUser, w.LoginPass = user, pass
		w.AutoLogin = auto && user != ""
	})
}

// SetServerTheme binds a theme to one server ("" unbinds): joining the
// server applies it, disconnecting restores the global pick.
func (p *AssetPreferences) SetServerTheme(key, themeName string) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Theme = themeName })
}

// RememberServerChars records the character list for rehearsal mode
// (capped at WarmCharsCap).
func (p *AssetPreferences) RememberServerChars(key string, chars []string) {
	if len(chars) == 0 {
		return
	}
	if len(chars) > WarmCharsCap {
		chars = chars[:WarmCharsCap]
	}
	copied := cloneStrings(chars)
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Chars = copied })
}

func (p *AssetPreferences) rememberServer(key string, set func(*ServerWarmInfo)) {
	if key == "" {
		return
	}
	p.mu.Lock()
	if p.ServerWarm == nil {
		p.ServerWarm = map[string]ServerWarmInfo{}
	}
	w, exists := p.ServerWarm[key]
	if !exists && len(p.ServerWarm) >= serverWarmCap {
		p.mu.Unlock()
		return // table full; never unbounded
	}
	// ServerWarmInfo carries slices/maps now, so no cheap == skip: every
	// remember marks dirty and the debounced saver coalesces the writes.
	set(&w)
	p.ServerWarm[key] = w
	p.mu.Unlock()
	p.markDirty()
}

// --- Hidden UI panels ---------------------------------------------------------------

// HiddenPanels reports the courtroom chrome regions the user hid.
func (p *AssetPreferences) HiddenPanels() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.HiddenPanelIDs))
	copy(out, p.HiddenPanelIDs)
	return out
}

// SetHiddenPanels replaces the hidden-chrome set.
func (p *AssetPreferences) SetHiddenPanels(ids []string) {
	p.mu.Lock()
	p.HiddenPanelIDs = append([]string(nil), ids...)
	p.mu.Unlock()
	p.markDirty()
}

// --- OOC name -----------------------------------------------------------------

// SavedOOCName reports the persisted OOC chat name.
func (p *AssetPreferences) SavedOOCName() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.OOCName
}

// SetOOCName persists the OOC chat name (debounced saver flushes).
func (p *AssetPreferences) SetOOCName(name string) {
	p.mu.Lock()
	if p.OOCName == name {
		p.mu.Unlock()
		return
	}
	p.OOCName = name
	p.mu.Unlock()
	p.markDirty()
}

// --- Layout scales --------------------------------------------------------------

// LayoutScales reports the courtroom layout knobs: viewport width percent,
// chat TEXT percent, chat BOX height percent, log/OOC list text percent,
// input field height percent. All clamped at load and set time.
func (p *AssetPreferences) LayoutScales() (viewport, chatText, chatBox, logText, input int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ViewportPct, p.ChatScalePct, p.ChatBoxPct, p.LogScalePct, p.InputHeightPct
}

// SetLayoutScales clamps and persists the layout knobs.
func (p *AssetPreferences) SetLayoutScales(viewport, chatText, chatBox, logText, input int) {
	viewport = clampPercent(viewport, MinViewportPercent, MaxViewportPercent)
	chatText = clampPercent(chatText, MinChatScalePercent, MaxChatScalePercent)
	chatBox = clampPercent(chatBox, MinChatBoxPercent, MaxChatBoxPercent)
	logText = clampPercent(logText, MinLogScalePercent, MaxLogScalePercent)
	input = clampPercent(input, MinInputPercent, MaxInputPercent)
	p.mu.Lock()
	if p.ViewportPct == viewport && p.ChatScalePct == chatText && p.ChatBoxPct == chatBox &&
		p.LogScalePct == logText && p.InputHeightPct == input {
		p.mu.Unlock()
		return
	}
	p.ViewportPct, p.ChatScalePct, p.ChatBoxPct, p.LogScalePct, p.InputHeightPct =
		viewport, chatText, chatBox, logText, input
	p.mu.Unlock()
	p.markDirty()
}

// UIScale reports the global UI scale percent.
func (p *AssetPreferences) UIScale() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.UIScalePct
}

// SetUIScale clamps and persists the global UI scale.
func (p *AssetPreferences) SetUIScale(pct int) {
	pct = clampPercent(pct, MinUIScalePercent, MaxUIScalePercent)
	p.mu.Lock()
	if p.UIScalePct == pct {
		p.mu.Unlock()
		return
	}
	p.UIScalePct = pct
	p.mu.Unlock()
	p.markDirty()
}

// --- Audio volumes ----------------------------------------------------------------

// AudioVolumes reports the music/SFX/blip volumes (0–100).
func (p *AssetPreferences) AudioVolumes() (music, sfx, blip int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MusicVol, p.SFXVol, p.BlipVol
}

// SetAudioVolumes clamps and persists the mixer volumes.
func (p *AssetPreferences) SetAudioVolumes(music, sfx, blip int) {
	music = clampPercent(music, 0, defaultAudioVolume)
	sfx = clampPercent(sfx, 0, defaultAudioVolume)
	blip = clampPercent(blip, 0, defaultAudioVolume)
	p.mu.Lock()
	if p.MusicVol == music && p.SFXVol == sfx && p.BlipVol == blip {
		p.mu.Unlock()
		return
	}
	p.MusicVol, p.SFXVol, p.BlipVol = music, sfx, blip
	p.mu.Unlock()
	p.markDirty()
}

// --- Message timing & master list ----------------------------------------------

// Timing reports the message timing knobs in milliseconds: typewriter
// crawl per character, post-message stay, and the minimum gap between
// outgoing chats (0 = no limit).
func (p *AssetPreferences) Timing() (crawlMs, stayMs, rateLimitMs int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.TextCrawlMs, p.TextStayMs, p.ChatRateLimitMs
}

// SetTiming clamps and persists the timing knobs.
func (p *AssetPreferences) SetTiming(crawlMs, stayMs, rateLimitMs int) {
	crawlMs = clampPercent(crawlMs, MinTextCrawlMs, MaxTextCrawlMs)
	stayMs = clampPercent(stayMs, 0, MaxTextStayMs)
	rateLimitMs = clampPercent(rateLimitMs, 0, MaxChatRateLimitMs)
	p.mu.Lock()
	if p.TextCrawlMs == crawlMs && p.TextStayMs == stayMs && p.ChatRateLimitMs == rateLimitMs {
		p.mu.Unlock()
		return
	}
	p.TextCrawlMs, p.TextStayMs, p.ChatRateLimitMs = crawlMs, stayMs, rateLimitMs
	p.mu.Unlock()
	p.markDirty()
}

// CatchUp reports the packed-room catch-up toggle and its queue-depth
// threshold (fast-forward the IC stage once more than threshold messages are
// queued).
func (p *AssetPreferences) CatchUp() (on bool, threshold int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CatchUpOn, p.CatchUpThreshold
}

// SetCatchUp clamps and persists the catch-up toggle + threshold.
func (p *AssetPreferences) SetCatchUp(on bool, threshold int) {
	threshold = clampPercent(threshold, catchUpThresholdMin, catchUpThresholdMax)
	p.mu.Lock()
	if p.CatchUpOn == on && p.CatchUpThreshold == threshold {
		p.mu.Unlock()
		return
	}
	p.CatchUpOn, p.CatchUpThreshold = on, threshold
	p.mu.Unlock()
	p.markDirty()
}

// TabCap is how many concurrent server tabs are allowed (clamped to
// [minMultiTabCap, maxMultiTabCap]; an unset/zero value falls back to the
// default, so older prefs files and tests behave as before).
func (p *AssetPreferences) TabCap() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.MultiTabCap <= 0 {
		return DefaultMultiTabCap
	}
	return clampPercent(p.MultiTabCap, minMultiTabCap, maxMultiTabCap)
}

// SetTabCap clamps and persists the multi-server tab cap.
func (p *AssetPreferences) SetTabCap(n int) {
	n = clampPercent(n, minMultiTabCap, maxMultiTabCap)
	p.mu.Lock()
	if p.MultiTabCap == n {
		p.mu.Unlock()
		return
	}
	p.MultiTabCap = n
	p.mu.Unlock()
	p.markDirty()
}

// ReduceMotion reports the reduce-motion accessibility toggle: when on, the
// jarring courtroom effects (screen shake, realization flash, and the M2 text
// effects) are suppressed.
func (p *AssetPreferences) ReduceMotion() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ReduceMotionOn
}

// SetReduceMotion toggles reduce-motion.
func (p *AssetPreferences) SetReduceMotion(on bool) {
	p.mu.Lock()
	if p.ReduceMotionOn == on {
		p.mu.Unlock()
		return
	}
	p.ReduceMotionOn = on
	p.mu.Unlock()
	p.markDirty()
}

// MusicDucking reports the music-ducking toggle: lower the music while a
// message is on stage so dialogue isn't drowned out.
func (p *AssetPreferences) MusicDucking() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MusicDuckingOn
}

// SetMusicDucking toggles music ducking.
func (p *AssetPreferences) SetMusicDucking(on bool) {
	p.mu.Lock()
	if p.MusicDuckingOn == on {
		p.mu.Unlock()
		return
	}
	p.MusicDuckingOn = on
	p.mu.Unlock()
	p.markDirty()
}

// MasterList reports the user's master-list override ("" = the default).
func (p *AssetPreferences) MasterList() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MasterListURL
}

// SetMasterList persists the master-list override.
func (p *AssetPreferences) SetMasterList(url string) {
	url = strings.TrimSpace(url)
	p.mu.Lock()
	if p.MasterListURL == url {
		p.mu.Unlock()
		return
	}
	p.MasterListURL = url
	p.mu.Unlock()
	p.markDirty()
}

// --- Wardrobe (per server) ----------------------------------------------------

// WardrobeList returns a copy of one server's custom character list.
func (p *AssetPreferences) WardrobeList(serverKey string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneStrings(p.ServerWarm[serverKey].Wardrobe)
}

// ClaimLegacyWardrobe performs the one-time migration from the flat
// pre-per-server wardrobe: the FIRST server connected to after the
// update inherits the collection (in practice the user's main server),
// then the legacy list clears and every other server starts clean.
// Connect calls this; it is a no-op once claimed.
func (p *AssetPreferences) ClaimLegacyWardrobe(serverKey string) {
	if serverKey == "" {
		return
	}
	p.mu.RLock()
	parked := len(p.Wardrobe) > 0
	p.mu.RUnlock()
	if !parked {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		if len(w.Wardrobe) == 0 && len(p.Wardrobe) > 0 {
			w.Wardrobe = p.Wardrobe
			p.Wardrobe = nil
		}
	})
}

// AddWardrobe stores a character folder in one server's wardrobe
// (case-insensitive dedupe, capped at WardrobeCap). Reports change.
func (p *AssetPreferences) AddWardrobe(serverKey, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || serverKey == "" {
		return false
	}
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		if len(w.Wardrobe) >= WardrobeCap {
			return
		}
		for _, have := range w.Wardrobe {
			if strings.EqualFold(have, name) {
				return
			}
		}
		w.Wardrobe = append(w.Wardrobe, name)
		changed = true
	})
	return changed
}

// RemoveWardrobe drops a character folder from one server's wardrobe.
func (p *AssetPreferences) RemoveWardrobe(serverKey, name string) bool {
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		for i, have := range w.Wardrobe {
			if strings.EqualFold(have, name) {
				w.Wardrobe = append(w.Wardrobe[:i], w.Wardrobe[i+1:]...)
				delete(w.WardrobeFolder, strings.ToLower(name)) // drop its folder too
				changed = true
				return
			}
		}
	})
	return changed
}

// WardrobeFolderMap returns a copy of one server's lowercased char → folder
// map (wardrobe organization). nil when nothing is filed.
func (p *AssetPreferences) WardrobeFolderMap(serverKey string) map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	src := p.ServerWarm[serverKey].WardrobeFolder
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetWardrobeFolder files a wardrobe character under a folder (category) on one
// server; an empty folder clears it. Bounded by WardrobeCap.
func (p *AssetPreferences) SetWardrobeFolder(serverKey, char, folder string) {
	char = strings.ToLower(strings.TrimSpace(char))
	folder = strings.TrimSpace(folder)
	if char == "" || serverKey == "" {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		if folder == "" {
			delete(w.WardrobeFolder, char)
			return
		}
		if w.WardrobeFolder == nil {
			w.WardrobeFolder = map[string]string{}
		}
		if _, exists := w.WardrobeFolder[char]; !exists && len(w.WardrobeFolder) >= WardrobeCap {
			return
		}
		w.WardrobeFolder[char] = folder
	})
}

// DeleteWardrobeFolder removes a whole wardrobe folder on one server in a single
// op. keepMembers true just ungroups (clears the folder tag, the characters
// stay in the wardrobe, unfiled); false removes those characters from the
// wardrobe entirely. Either way the folder — being membership-derived — ceases
// to exist once nothing is filed under it.
func (p *AssetPreferences) DeleteWardrobeFolder(serverKey, folder string, keepMembers bool) {
	folder = strings.TrimSpace(folder)
	if folder == "" || serverKey == "" {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		for lc, f := range w.WardrobeFolder {
			if !strings.EqualFold(f, folder) {
				continue
			}
			delete(w.WardrobeFolder, lc)
			if keepMembers {
				continue
			}
			for i, have := range w.Wardrobe { // also drop the character from the wardrobe
				if strings.ToLower(have) == lc {
					w.Wardrobe = append(w.Wardrobe[:i], w.Wardrobe[i+1:]...)
					break
				}
			}
		}
	})
}

// --- Favorite backgrounds (per server) ----------------------------------------

// FavBackgroundList returns a copy of one server's starred-background list.
func (p *AssetPreferences) FavBackgroundList(serverKey string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneStrings(p.ServerWarm[serverKey].FavBackgrounds)
}

// AddFavBackground stars a background on one server (case-insensitive dedupe,
// capped at FavBackgroundCap). Reports whether it changed.
func (p *AssetPreferences) AddFavBackground(serverKey, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || serverKey == "" {
		return false
	}
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		if len(w.FavBackgrounds) >= FavBackgroundCap {
			return
		}
		for _, have := range w.FavBackgrounds {
			if strings.EqualFold(have, name) {
				return
			}
		}
		w.FavBackgrounds = append(w.FavBackgrounds, name)
		changed = true
	})
	return changed
}

// RemoveFavBackground unstars a background on one server (and drops its folder
// entry, like RemoveWardrobe). Reports change.
func (p *AssetPreferences) RemoveFavBackground(serverKey, name string) bool {
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		for i, have := range w.FavBackgrounds {
			if strings.EqualFold(have, name) {
				w.FavBackgrounds = append(w.FavBackgrounds[:i], w.FavBackgrounds[i+1:]...)
				delete(w.FavBackgroundFolder, strings.ToLower(name)) // leaving favourites drops the folder too
				changed = true
				return
			}
		}
	})
	return changed
}

// FavBackgroundFolderMap returns a copy of one server's lowercased background →
// folder map (Backgrounds-section organization). nil when nothing is filed.
func (p *AssetPreferences) FavBackgroundFolderMap(serverKey string) map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	src := p.ServerWarm[serverKey].FavBackgroundFolder
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetFavBackgroundFolder files a favourite background under a folder on one
// server; an empty folder clears it. Bounded by FavBackgroundCap. Mirrors
// SetWardrobeFolder.
func (p *AssetPreferences) SetFavBackgroundFolder(serverKey, bg, folder string) {
	bg = strings.ToLower(strings.TrimSpace(bg))
	folder = strings.TrimSpace(folder)
	if bg == "" || serverKey == "" {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		if folder == "" {
			delete(w.FavBackgroundFolder, bg)
			return
		}
		if w.FavBackgroundFolder == nil {
			w.FavBackgroundFolder = map[string]string{}
		}
		if _, exists := w.FavBackgroundFolder[bg]; !exists && len(w.FavBackgroundFolder) >= FavBackgroundCap {
			return
		}
		w.FavBackgroundFolder[bg] = folder
	})
}

// DeleteFavBackgroundFolder removes a whole background folder on one server.
// keepMembers true ungroups (the backgrounds stay favourited, unfiled); false
// unstars those backgrounds entirely. Mirrors DeleteWardrobeFolder.
func (p *AssetPreferences) DeleteFavBackgroundFolder(serverKey, folder string, keepMembers bool) {
	folder = strings.TrimSpace(folder)
	if folder == "" || serverKey == "" {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		for lc, f := range w.FavBackgroundFolder {
			if !strings.EqualFold(f, folder) {
				continue
			}
			delete(w.FavBackgroundFolder, lc)
			if keepMembers {
				continue
			}
			for i, have := range w.FavBackgrounds {
				if strings.ToLower(have) == lc {
					w.FavBackgrounds = append(w.FavBackgrounds[:i], w.FavBackgrounds[i+1:]...)
					break
				}
			}
		}
	})
}

// --- Character keybinds (per server) -------------------------------------------

// CharKeyBinds returns a copy of one server's key → character map.
func (p *AssetPreferences) CharKeyBinds(serverKey string) map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	src := p.ServerWarm[serverKey].CharKeys
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetCharKeyBind binds key (lowercase SDL key name) to a character on one
// server; an empty char clears the binding. Bounded by charKeyCap.
func (p *AssetPreferences) SetCharKeyBind(serverKey, key, char string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" || serverKey == "" {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		if char == "" {
			delete(w.CharKeys, key)
			return
		}
		if w.CharKeys == nil {
			w.CharKeys = map[string]string{}
		}
		if _, exists := w.CharKeys[key]; !exists && len(w.CharKeys) >= charKeyCap {
			return
		}
		w.CharKeys[key] = char
	})
}

// --- Settings export / import ---------------------------------------------------

// ExportSettings flushes pending mutations and copies the preferences file
// — every knob, favorites, per-server wardrobes and keybinds, learned
// formats — to destPath: the move-to-a-new-PC bundle. Saved passwords are
// the ONE thing stripped on the way out (see stripExportPasswords).
func (p *AssetPreferences) ExportSettings(destPath string) error {
	if err := p.SaveNow(); err != nil {
		return fmt.Errorf("config: flushing before export: %w", err)
	}
	data, err := os.ReadFile(p.path)
	if err != nil {
		return fmt.Errorf("config: reading preferences for export: %w", err)
	}
	if data, err = stripExportPasswords(data); err != nil {
		return fmt.Errorf("config: redacting export passwords: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), prefsDirPerm); err != nil {
		return err
	}
	return os.WriteFile(destPath, data, prefsFilePerm)
}

// stripExportPasswords blanks every serverWarm[].loginPass in a marshaled
// preferences blob. The bundle is meant to travel to another machine, and
// plaintext credentials shouldn't ride along — the user re-enters the
// password there (the username and the auto-login choice survive, so a
// single retype restores the saved flow). loginPass is omitempty, so a
// blanked value drops out of the JSON entirely. Everything else round-trips
// through the same struct shape the saver marshals, so the export stays a
// faithful, fully-indented, loadable preferences file.
func stripExportPasswords(data []byte) ([]byte, error) {
	var snap AssetPreferences
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	if len(snap.ServerWarm) == 0 {
		return data, nil // nothing saved to redact
	}
	for key, warm := range snap.ServerWarm {
		if warm.LoginPass == "" {
			continue
		}
		warm.LoginPass = ""
		snap.ServerWarm[key] = warm
	}
	return json.MarshalIndent(&snap, jsonMarshalPrefix, jsonMarshalIndent)
}

// ImportSettings validates that srcPath parses as a preferences file and
// atomically replaces the live one. Applied on the NEXT start: the live
// state has too many mirrors (resolver snapshots, UI caches) to hot-swap
// safely mid-session.
func (p *AssetPreferences) ImportSettings(srcPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("config: reading import: %w", err)
	}
	var probe prefsJSON
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("config: not a settings file: %w", err)
	}
	dir := filepath.Dir(p.path)
	if err := os.MkdirAll(dir, prefsDirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, prefsTmpPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, p.path); err != nil {
		return err
	}
	// From here the on-disk truth is the import; freeze the saver so the
	// live session can't write its (pre-import) state back over it.
	p.frozen.Store(true)
	return nil
}

// --- Learned formats --------------------------------------------------------

// LearnedKey builds the learned-format map key for a host and type name.
func LearnedKey(host, typeName string) string {
	return host + LearnedKeySeparator + typeName
}

// RecordLearned persists ext as the known-working format for (host, type).
// The resolver calls this on the first successful probe; persistence is lazy
// via the debounced saver.
func (p *AssetPreferences) RecordLearned(host, typeName, ext string) {
	key := LearnedKey(host, typeName)
	p.mu.Lock()
	if existing, ok := p.LearnedFormats[key]; ok && len(existing) == 1 && existing[0] == ext {
		p.mu.Unlock()
		return
	}
	if p.LearnedFormats == nil {
		p.LearnedFormats = map[string][]string{}
	}
	p.LearnedFormats[key] = []string{ext}
	p.mu.Unlock()
	p.markDirty()
}

// FormatAutoDetect reports whether the client fetches extensions.json on
// connect to seed that server's formats (default ON).
func (p *AssetPreferences) FormatAutoDetect() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AutoDetectFormats
}

// SetFormatAutoDetect toggles manifest-driven format detection.
func (p *AssetPreferences) SetFormatAutoDetect(enabled bool) {
	p.mu.Lock()
	if p.AutoDetectFormats == enabled {
		p.mu.Unlock()
		return
	}
	p.AutoDetectFormats = enabled
	p.mu.Unlock()
	p.markDirty()
}

// StreamerMode reports the on-stream privacy toggle: OOC sender names
// and IP-like tokens mask in the display, callwords stay silent.
func (p *AssetPreferences) StreamerMode() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.StreamerModeOn
}

// SetStreamerMode toggles the on-stream privacy mode.
func (p *AssetPreferences) SetStreamerMode(enabled bool) {
	p.mu.Lock()
	if p.StreamerModeOn == enabled {
		p.mu.Unlock()
		return
	}
	p.StreamerModeOn = enabled
	p.mu.Unlock()
	p.markDirty()
}

// DiskZstdEnabled reports whether new T3 blobs compress with zstd
// (default OFF — the CPU-vs-disk trade is the user's to take).
func (p *AssetPreferences) DiskZstdEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DiskZstd
}

// SetDiskZstd toggles T3 compression for new writes.
func (p *AssetPreferences) SetDiskZstd(enabled bool) {
	p.mu.Lock()
	if p.DiskZstd == enabled {
		p.mu.Unlock()
		return
	}
	p.DiskZstd = enabled
	p.mu.Unlock()
	p.markDirty()
}

// Layout-editor bounds (rule §17.4).
const (
	// themeOvThemesCap bounds how many themes carry edited layouts.
	themeOvThemesCap = 32
	// themeOvRectsCap bounds edited widgets per theme (more than the
	// layout engine even defines).
	themeOvRectsCap = 64
)

// ThemeRectOverrides returns a copy of one theme's user-dragged widget
// geometry (design-space [x,y,w,h], from the live layout editor).
func (p *AssetPreferences) ThemeRectOverrides(theme string) map[string][4]int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	src := p.ThemeRectOv[theme]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string][4]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetThemeRectOverride stores one widget's edited design rect.
func (p *AssetPreferences) SetThemeRectOverride(theme, key string, r [4]int) {
	if theme == "" || key == "" {
		return
	}
	p.mu.Lock()
	if p.ThemeRectOv == nil {
		p.ThemeRectOv = map[string]map[string][4]int{}
	}
	m, ok := p.ThemeRectOv[theme]
	if !ok {
		if len(p.ThemeRectOv) >= themeOvThemesCap {
			p.mu.Unlock()
			return
		}
		m = map[string][4]int{}
		p.ThemeRectOv[theme] = m
	}
	if _, exists := m[key]; !exists && len(m) >= themeOvRectsCap {
		p.mu.Unlock()
		return
	}
	m[key] = r
	p.mu.Unlock()
	p.markDirty()
}

// ClearThemeRectOverride drops one widget's edit (key "" = the whole
// theme's edits).
func (p *AssetPreferences) ClearThemeRectOverride(theme, key string) {
	p.mu.Lock()
	if m, ok := p.ThemeRectOv[theme]; ok {
		if key == "" {
			delete(p.ThemeRectOv, theme)
		} else {
			delete(m, key)
			if len(m) == 0 {
				delete(p.ThemeRectOv, theme)
			}
		}
	}
	p.mu.Unlock()
	p.markDirty()
}

// Macros returns a copy of the user macro list.
func (p *AssetPreferences) Macros() []MacroSpec {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]MacroSpec, len(p.UserMacros))
	copy(out, p.UserMacros)
	return out
}

// SetMacros replaces the macro list (caps enforced).
func (p *AssetPreferences) SetMacros(in []MacroSpec) {
	clean := sanitizeMacros(in)
	p.mu.Lock()
	p.UserMacros = clean
	p.mu.Unlock()
	p.markDirty()
}

// FontPaths reports the raw IC/OOC font override list (semicolon- or
// comma-separated TTF/TTC paths, chain order; "" = embedded font).
func (p *AssetPreferences) FontPaths() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FontOverridePaths
}

// SetFontPaths persists the font override list.
func (p *AssetPreferences) SetFontPaths(raw string) {
	p.mu.Lock()
	if p.FontOverridePaths == raw {
		p.mu.Unlock()
		return
	}
	p.FontOverridePaths = raw
	p.mu.Unlock()
	p.markDirty()
}

// UIScaleAuto reports whether the UI scale follows the display DPI
// (default ON); off, the manual UIScale percent governs.
func (p *AssetPreferences) UIScaleAuto() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.UIScaleAutoOn
}

// SetUIScaleAuto toggles DPI-driven UI scaling.
func (p *AssetPreferences) SetUIScaleAuto(enabled bool) {
	p.mu.Lock()
	if p.UIScaleAutoOn == enabled {
		p.mu.Unlock()
		return
	}
	p.UIScaleAutoOn = enabled
	p.mu.Unlock()
	p.markDirty()
}

// ThemeLayoutEnabled reports whether the courtroom adopts the theme's
// courtroom_design.ini geometry (default ON).
func (p *AssetPreferences) ThemeLayoutEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ThemeLayoutOn
}

// SetThemeLayout toggles theme-driven courtroom geometry.
func (p *AssetPreferences) SetThemeLayout(enabled bool) {
	p.mu.Lock()
	if p.ThemeLayoutOn == enabled {
		p.mu.Unlock()
		return
	}
	p.ThemeLayoutOn = enabled
	p.mu.Unlock()
	p.markDirty()
}

// ExportLearnedJSON snapshots the learned-format table as indented JSON —
// one player's warm state can seed another's (Settings → Export).
func (p *AssetPreferences) ExportLearnedJSON() ([]byte, error) {
	return json.MarshalIndent(p.LearnedSnapshot(), "", jsonMarshalIndent)
}

// ImportLearnedJSON merges a learned-format export into this table
// (imported entries win) and reports how many entries landed.
func (p *AssetPreferences) ImportLearnedJSON(data []byte) (int, error) {
	var in map[string][]string
	if err := json.Unmarshal(data, &in); err != nil {
		return 0, fmt.Errorf("config: parsing learned-format import: %w", err)
	}
	p.mu.Lock()
	if p.LearnedFormats == nil {
		p.LearnedFormats = map[string][]string{}
	}
	n := 0
	for k, v := range in {
		if k == "" || len(v) == 0 {
			continue
		}
		p.LearnedFormats[k] = cloneStrings(v)
		n++
	}
	p.mu.Unlock()
	if n > 0 {
		p.markDirty()
	}
	return n, nil
}

// LearnedSnapshot returns a deep copy of the learned-format table, used to
// warm the resolver's atomic snapshot at startup and on server connect.
func (p *AssetPreferences) LearnedSnapshot() map[string][]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string][]string, len(p.LearnedFormats))
	for k, v := range p.LearnedFormats {
		out[k] = cloneStrings(v)
	}
	return out
}

// ClearLearned wipes every learned format ("Clear Learned Formats" button).
func (p *AssetPreferences) ClearLearned() {
	p.mu.Lock()
	if len(p.LearnedFormats) == 0 {
		p.mu.Unlock()
		return
	}
	p.LearnedFormats = map[string][]string{}
	p.mu.Unlock()
	p.markDirty()
}

// dropLearnedTypeLocked removes learned entries for one type across all
// hosts. Caller holds the write lock.
func (p *AssetPreferences) dropLearnedTypeLocked(typeName string) {
	suffix := LearnedKeySeparator + typeName
	for key := range p.LearnedFormats {
		if strings.HasSuffix(key, suffix) {
			delete(p.LearnedFormats, key)
		}
	}
}

// --- Pairing ----------------------------------------------------------------

// PairOffsets returns the last-used pair offsets in percent (−100..100).
func (p *AssetPreferences) PairOffsets() (x, y int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PairOffsetX, p.PairOffsetY
}

// SetPairOffsets stores the last-used pair offsets, clamped to
// [PairOffsetMin, PairOffsetMax].
func (p *AssetPreferences) SetPairOffsets(x, y int) {
	x, y = clampPairOffset(x), clampPairOffset(y)
	p.mu.Lock()
	if p.PairOffsetX == x && p.PairOffsetY == y {
		p.mu.Unlock()
		return
	}
	p.PairOffsetX, p.PairOffsetY = x, y
	p.mu.Unlock()
	p.markDirty()
}

// PairFlipped reports the persisted pair flip toggle.
func (p *AssetPreferences) PairFlipped() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PairFlip
}

// SetPairFlipped stores the pair flip toggle.
func (p *AssetPreferences) SetPairFlipped(flip bool) {
	p.mu.Lock()
	if p.PairFlip == flip {
		p.mu.Unlock()
		return
	}
	p.PairFlip = flip
	p.mu.Unlock()
	p.markDirty()
}

// --- Local assets (no-streaming legacy mode) ----------------------------------

// LocalAssets reports the no-streaming mode: read assets from user-chosen
// local mount folders instead of the server's asset URL (legacy support for
// servers without an asset server). Mounts are searched in order, first hit
// wins — mirroring AO2-Client mount paths, so any folder layout works, not
// just a default /base.
func (p *AssetPreferences) LocalAssets() (enabled bool, mounts []string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.LocalAssetsPaths))
	copy(out, p.LocalAssetsPaths)
	return p.LocalAssetsEnabled, out
}

// SetLocalAssets toggles no-streaming mode and stores the ordered mount
// folder list.
func (p *AssetPreferences) SetLocalAssets(enabled bool, mounts []string) {
	p.mu.Lock()
	if p.LocalAssetsEnabled == enabled && slices.Equal(p.LocalAssetsPaths, mounts) {
		p.mu.Unlock()
		return
	}
	p.LocalAssetsEnabled = enabled
	p.LocalAssetsPaths = slices.Clone(mounts)
	p.mu.Unlock()
	p.markDirty()
}

// --- Favorites -----------------------------------------------------------------

// FavoriteServers returns the starred server list, in pin order.
func (p *AssetPreferences) FavoriteServers() []FavoriteServer {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]FavoriteServer, len(p.Favorites))
	copy(out, p.Favorites)
	return out
}

// AddFavorite stars a server (or updates an existing favorite with the same
// URL). URL must be the full ws://host:port or wss://host:port address, so
// private servers off the master list work identically; the description is
// retained for the phone book.
func (p *AssetPreferences) AddFavorite(name, url, description string) {
	if url == "" {
		return
	}
	p.mu.Lock()
	for i, f := range p.Favorites {
		if f.URL == url {
			if f.Name == name && f.Description == description {
				p.mu.Unlock()
				return
			}
			p.Favorites[i].Name = name
			p.Favorites[i].Description = description
			p.mu.Unlock()
			p.markDirty()
			return
		}
	}
	p.Favorites = append(p.Favorites, FavoriteServer{Name: name, URL: url, Description: description})
	p.mu.Unlock()
	p.markDirty()
}

// RemoveFavorite unstars the server with the given URL.
func (p *AssetPreferences) RemoveFavorite(url string) {
	p.mu.Lock()
	kept := p.Favorites[:0]
	removed := false
	for _, f := range p.Favorites {
		if f.URL == url {
			removed = true
			continue
		}
		kept = append(kept, f)
	}
	p.Favorites = kept
	p.mu.Unlock()
	if removed {
		p.markDirty()
	}
}

// IsFavorite reports whether the URL is starred.
func (p *AssetPreferences) IsFavorite(url string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, f := range p.Favorites {
		if f.URL == url {
			return true
		}
	}
	return false
}

// --- Showname ----------------------------------------------------------------

// SavedShowname returns the persisted in-character showname.
func (p *AssetPreferences) SavedShowname() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Showname
}

// SetShowname persists the in-character showname so it survives restarts and
// is prefilled on the next session.
func (p *AssetPreferences) SetShowname(name string) {
	p.mu.Lock()
	if p.Showname == name {
		p.mu.Unlock()
		return
	}
	p.Showname = name
	p.mu.Unlock()
	p.markDirty()
}

func clampPairOffset(v int) int {
	if v < PairOffsetMin {
		return PairOffsetMin
	}
	if v > PairOffsetMax {
		return PairOffsetMax
	}
	return v
}
