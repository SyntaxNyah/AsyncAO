// Package config persists user preferences; mutators never touch the disk and one saver debounces writes.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/text"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// DefaultSaveDebounce is the saver's wait after the latest mutation.
	DefaultSaveDebounce = 250 * time.Millisecond

	// PrefsDirName is the directory under os.UserConfigDir.
	PrefsDirName = "AsyncAO"

	PrefsFileName = "asset_preferences.json"

	// LearnedKeySeparator joins learned-format keys: "<host>|<type name>".
	LearnedKeySeparator = "|"

	prefsFilePerm   = 0o644
	prefsDirPerm    = 0o755
	prefsTmpPattern = PrefsFileName + ".*.tmp"

	// corruptSuffixPrefix renames an unparseable prefs file so the first save can't overwrite it with defaults.
	corruptSuffixPrefix = ".corrupt-"
	// corruptStampLayout is colon-free because ':' is illegal in Windows file names and os.Rename would fail.
	corruptStampLayout = "20060102-150405"

	jsonMarshalPrefix = ""
	jsonMarshalIndent = "  "
)

// defaultPreferAnimated is a decode and render toggle, never an extra network probe.
const defaultPreferAnimated = true

// defaultFormatAutoDetect is on because seeding from a server's extensions.json beats blind probing.
const defaultFormatAutoDetect = true

// Blip cadence: one blip per N revealed letters; mirrors courtroom.DefaultBlipRate, which config can't import.
const (
	defaultBlipRate = 2
	MinBlipRate     = 1
	MaxBlipRate     = 10
)

// defaultUIScaleAuto makes the UI scale follow display DPI; unticking re-arms the manual scale.
const defaultUIScaleAuto = true

// defaultThemeLayout makes the courtroom adopt a theme's courtroom_design.ini geometry wholesale.
const defaultThemeLayout = true

// Theme-fit modes, persisted and so append-only: renumbering would reinterpret saved choices.
// Native is the default as stock AO2 never scales a theme (setFixedSize, charselect.cpp:83).
// A file's themeFit:0 can't be told from a deliberate Stretch; only the ThemeFitDefaultMigrated stamp moves it.
const (
	ThemeFitStretch   = 0
	ThemeFitLetterbox = 1
	ThemeFitCrop      = 2
	ThemeFitCustom    = 3 // manual zoom + pan (crop to taste)
	ThemeFitNative    = 4 // 1:1, downscale-only: stock-AO2 pixels, never upscaled
	// themeFitModeMax is the highest valid mode; every clamp uses it so a new mode can't be clamped away.
	themeFitModeMax = ThemeFitNative
	defaultThemeFit = ThemeFitNative
	// DefaultThemeFit is for UI that returns to the default; an alias so it tracks the default.
	DefaultThemeFit = defaultThemeFit
)

// Custom fit: zoom is a percent of the letterbox scale (100 = fits), pan a percent of the window.
const (
	DefaultThemeZoom = 100
	MinThemeZoom     = 50
	MaxThemeZoom     = 300
	MaxThemePan      = 50
)

// defaultPlainLobby uses the plain backdrop because a busy AO2 lobby image often makes our server list unreadable.
const defaultPlainLobby = true

// defaultThemeFonts applies courtroom_fonts.ini as AO2-Client does (Courtroom::set_fonts, courtroom.cpp:1188).
const defaultThemeFonts = true

// defaultCatchUpWhenBehind matches the original AO2 queue: every IC message plays in full and in order.
const defaultCatchUpWhenBehind = false

// defaultShoutBypassQueue makes shouts skip the queue, exactly as AO2 does.
const defaultShoutBypassQueue = true

// Catch-up threshold: messages waiting behind the one starting; the floor default keeps the textbox on the latest line.
const (
	DefaultCatchUpThreshold = 1
	catchUpThresholdMin     = 1
	catchUpThresholdMax     = 50
)

// Multi-server tab cap; each tab costs a websocket, a reducer and two bounded logs (hard rule 4).
const (
	DefaultMultiTabCap = 6
	minMultiTabCap     = 1
	maxMultiTabCap     = 99
	// MaxMultiTabCap lets per-tab structures size against the same ceiling.
	MaxMultiTabCap = maxMultiTabCap
)

// defaultEmoteButtonImages uses image buttons (characters/<char>/emotions/button<N>) rather than text chips.
const defaultEmoteButtonImages = true

// defaultShowFriendButton shows the per-row "+ Friend" button in the player list.
const defaultShowFriendButton = true

// defaultAutoClipModcall saves a text clip of the recent IC log on any modcall, as a record for mods.
const defaultAutoClipModcall = true

// defaultGroupChatButton shows the Group Chat launcher in the courtroom control row, not just Extras.
const defaultGroupChatButton = true

// defaultCharChatbox draws a speaker's char.ini chat= skin like AO2 and webAO; off also stops the misc fetches.
const defaultCharChatbox = true

// defaultRightClickHideSprite makes right-clicking a sprite offer to hide it.
const defaultRightClickHideSprite = true

// defaultDragLayout resizes the courtroom by dragging panel edges; off restores the +/- buttons.
const defaultDragLayout = true

// defaultSmoothScaling is the linear filter for backgrounds, desks and chrome; sprites use SpriteScaling.
const defaultSmoothScaling = true

// Per-sprite filter, mirroring courtroom.ScalingMode: Auto honours scaling= and point-samples enlarged art.
const (
	SpriteScalingAuto   = 0
	SpriteScalingSmooth = 1
	SpriteScalingPixel  = 2

	defaultSpriteScaling = SpriteScalingAuto
)

// ClampSpriteScaling reads anything unrecognised as Auto, because the settings UI indexes label tables by this value.
func ClampSpriteScaling(v int) int {
	if v < SpriteScalingAuto || v > SpriteScalingPixel {
		return defaultSpriteScaling
	}
	return v
}

// defaultUpdateCheck enables a one-shot async release check at launch; a dev build never hits the network.
const defaultUpdateCheck = true

// defaultShowAssetWarnings is off because every 404 is noise on sparse packs; failures still reach the debug overlay.
const defaultShowAssetWarnings = false

// defaultSpriteMove is off because click-drag repositioning surprises players who grab a sprite by accident.
const defaultSpriteMove = false

// defaultDeskFollowManifest follows extensions.json; the desk pin is the opt-out for wrong manifests.
const defaultDeskFollowManifest = true

// defaultSpritePreview pops a full-size sprite on hovering a character or emote button.
const defaultSpritePreview = true

// Sprite-preview hover dwell, bounded so it can't fire on a graze or effectively never.
const (
	DefaultPreviewHoverMs = 5000
	minPreviewHoverMs     = 500
	maxPreviewHoverMs     = 15000
)

// Sprite-preview box height in px; bounds match the grip clamp in the UI.
const (
	DefaultPreviewHeightPx = 384
	MinPreviewHeightPx     = 96
	MaxPreviewHeightPx     = 720
)

// Emote-grid gap in logical px; 0 is valid (AO2 themes ship "0, 0"), so the DTO is *int to tell it from absent.
const (
	DefaultEmoteGridGapPx = 6
	MinEmoteGridGapPx     = 0
	MaxEmoteGridGapPx     = 48
)

// defaultEmoteHoverNames shows an emote's char.ini name as a tooltip, separate from the heavier sprite preview.
const defaultEmoteHoverNames = true

// Emote-name hover dwell, bounded so it can't fire on a graze or effectively never.
const (
	DefaultEmoteHoverNamesMs = 5000
	minEmoteHoverNamesMs     = 500
	maxEmoteHoverNamesMs     = 10000
)

// defaultAutoLoginToast confirms a saved auto-login so a mod never has to guess whether the session is authenticated.
const defaultAutoLoginToast = true

// defaultFontCensus lets unsupported scripts use installed fonts; optional as its input is the user's OS.
const defaultFontCensus = true

// defaultScreenEffects renders AO2 screenshake and realization flash; Reduce motion also suppresses them.
const defaultScreenEffects = true

// defaultWordDelete makes Ctrl+Backspace delete a word; off is a consumed no-op, so it never reaches a hotkey.
const defaultWordDelete = true

// defaultRecordingsKeepAssets bundles a stopped recording's cached assets so it survives its CDN going dark.
const defaultRecordingsKeepAssets = true

// defaultAdditiveText honours the 2.8 ADDITIVE flag; AO2 appends on any additive line with no char-id gate.
const defaultAdditiveText = true

// defaultCallwordToast names a heard callword in a toast; streamer mode suppresses it regardless.
const defaultCallwordToast = true

// defaultMessageCounter shows a live IC character count, since servers truncate very long lines.
const defaultMessageCounter = true

// AO2's sticky options, off here so picks clear after a send; options.cpp:420-443 defaults them true.
// Courtroom::reset_ui (AO2-Client/src/courtroom.cpp:2340, :2348, :2356) clears each only when false.
// Named for the config key because upstream's clearXOnPlayEnabled() accessors read backwards.
const (
	defaultStickySounds   = false // AO2 "stickysounds"  (aooptionsdialog.cpp:416, "Sticky Sounds")
	defaultStickyEffects  = false // AO2 "stickyeffects" (:417, "Sticky Effects")
	defaultStickyPreanims = false // AO2 "stickypres"    (:418, "Sticky Preanims")
)

// defaultSFXOnIdle matches AO2's sfx_on_idle (options.cpp:569-572, aooptionsdialog.cpp:423).
// On, a picked SFX turns IDLE/ZOOM into PREANIM/PREANIM_ZOOM with empty pre, zero delay (courtroom.cpp:2107-2115).
const defaultSFXOnIdle = false

// defaultUnfocusedFullRate is off so an unfocused window throttles; on serves second monitors and capture software.
const defaultUnfocusedFullRate = false

// defaultDetailedLog is on because the transcript is what people want after a session; turning it off sticks.
const defaultDetailedLog = true

// defaultGhostCrawlText draws the unrevealed tail transparent so the log doesn't reflow on every rune.
const defaultGhostCrawlText = true

// defaultICTimestamps prefixes each IC log line with its local arrival time.
const defaultICTimestamps = false

// defaultAutoReconnect is off because reconnecting re-runs the handshake and drops you at character select.
const defaultAutoReconnect = false

// defaultClipSpritesToStage stops a large pair or reposition offset spilling a sprite over the chatbox or log.
const defaultClipSpritesToStage = true

// defaultEventDrivenLoop stops rendering static screens between real signals; off restores the pacing loop.
const defaultEventDrivenLoop = true

// defaultMotionRedrawPerEvent renders one frame per pointer event instead of holding full rate, saving GPU.
const defaultMotionRedrawPerEvent = true

// defaultMusicHistory keeps a session list of played songs for the Jukebox "Recently played" view.
const defaultMusicHistory = true

// defaultMusicStreaming plays /play tracks; off downloads none, even your own, which round-trip the server.
const defaultMusicStreaming = true

// defaultShowMissingPlaceholder draws missingno when a fallback chain 404s, as AO2 does.
// Not omitempty, so an explicit off persists.
const defaultShowMissingPlaceholder = true

// defaultICCustomColor seeds the IC colour picker, packed 0xRRGGBB.
const defaultICCustomColor = 0x7ADCF0

// defaultHighlightColor is the log selection highlight, packed 0xRRGGBB.
const defaultHighlightColor = 0x78AAFF

// Sprite colour FX: Speed maps to hue period, Vividness to the colour-mod floor; the tint is packed 0xRRGGBB.
const (
	minRainbowSpeed        = 1   // slowest hue rotation
	maxRainbowSpeed        = 100 // fastest
	defaultRainbowSpeed    = 70  // ~2.6 s per cycle (close to the original fixed look)
	minRainbowVivid        = 0   // subtlest tint
	maxRainbowVivid        = 100 // most saturated / neon
	defaultRainbowVivid    = 65
	defaultSpriteTintColor = 0xFF44CC // hot pink — only used once Solid tint is enabled
)

// Scene-replay speed in percent; defaults slower than live, whose crawl is tuned for typing, not watching.
const (
	minReplaySpeed     = 25
	maxReplaySpeed     = 200
	defaultReplaySpeed = 100
)

// Sticky scene GIF and WebP export options.
const (
	minExportHeight      = 240
	maxExportHeight      = 720
	defaultExportHeight  = 360
	minExportFPS         = 6
	maxExportFPS         = 30
	defaultExportFPS     = 12
	minExportQuality     = 20
	maxExportQuality     = 100
	defaultExportQuality = 80
	minExportText        = 50 // chat-text size in the export, percent of the fitted base
	maxExportText        = 200
	defaultExportText    = 100
	// Video export format: only "mp4" (H.264) and "webm" (VP9) are valid; anything else normalizes to MP4.
	defaultVideoFormat = "mp4"
)

// ExportOptions: output is 4:3; TextScale is a percent of a base fitted to the output, so chat zoom can't inflate it.
type ExportOptions struct {
	HeightPx  int  `json:"heightPx"`
	FPS       int  `json:"fps"`
	Quality   int  `json:"quality"`
	Loop      bool `json:"loop"`
	TextScale int  `json:"textScale"`
	// VideoFormat is "mp4" or "webm" and doesn't affect GIF or WebP; empty means MP4.
	VideoFormat string `json:"videoFormat,omitempty"`
	// Watermark stamps the top-right corner with WatermarkText, or the recording's server and date when blank.
	Watermark     bool   `json:"watermark,omitempty"`
	WatermarkText string `json:"watermarkText,omitempty"`
	// Subtitles writes .srt and .vtt sidecars beside a video, timed to the exported frames.
	Subtitles bool `json:"subtitles,omitempty"`
	// CopyToClipboard puts the finished file on the OS clipboard (Windows).
	CopyToClipboard bool `json:"copyToClipboard,omitempty"`
}

// maxWatermarkLen bounds the stamp, because the label raster keys a texture per distinct string.
const maxWatermarkLen = 64

func defaultExportOptions() ExportOptions {
	return ExportOptions{HeightPx: defaultExportHeight, FPS: defaultExportFPS, Quality: defaultExportQuality, Loop: true, TextScale: defaultExportText, VideoFormat: defaultVideoFormat}
}

// normalizeVideoFormat lives here, not in videoenc, so config has no UI dependency.
func normalizeVideoFormat(s string) string {
	if strings.EqualFold(s, "webm") {
		return "webm"
	}
	return defaultVideoFormat
}

// SpriteStylePref mirrors courtroom.SpriteStyle, which config can't import; all zero means no style.
type SpriteStylePref struct {
	Tint    bool  `json:"tint,omitempty"`
	R       uint8 `json:"r,omitempty"`
	G       uint8 `json:"g,omitempty"`
	B       uint8 `json:"b,omitempty"`
	Opacity uint8 `json:"opacity,omitempty"` // percent 1..100; 0 = unset = opaque
	Glow    bool  `json:"glow,omitempty"`
	Wobble  bool  `json:"wobble,omitempty"`
	Spin    bool  `json:"spin,omitempty"`
	// Hue-cycle, mirror, percent brightness and scale, and tilt (0 = neutral); clamped at render.
	HueCycle bool `json:"hueCycle,omitempty"`
	FlipH    bool `json:"flipH,omitempty"`
	// Per-pixel effects need a cached variant texture, since SetColorMod can't do them.
	Invert    bool      `json:"invert,omitempty"`
	Grayscale bool      `json:"grayscale,omitempty"`
	Sepia     bool      `json:"sepia,omitempty"`         // #34 warm brown-tone per-pixel variant
	Posterize bool      `json:"posterize,omitempty"`     // #34 channel-quantise (poster look)
	Restyle   uint8     `json:"restyle,omitempty"`       // extra per-pixel look picker (0 none, else a courtroom.Variant* redscale/solarize/neon…)
	Motion    uint8     `json:"motion,omitempty"`        // #34 transmitted movement path (0 none / orbit / bounce / sway / drift)
	Path      [16]uint8 `json:"motionPath,omitempty"`    // #34 custom drawn path waypoints (packed 4-bit X/Y); a fixed array so SpriteStylePref stays == comparable (size MUST match courtroom maxPathPoints)
	PathLen   uint8     `json:"motionPathLen,omitempty"` // # active waypoints (0 = none, else 2..16)
	// Silhouette effects, sent in a backward-compatible flags2 byte on the style wire.
	Outline    bool `json:"outline,omitempty"`
	DropShadow bool `json:"dropShadow,omitempty"`
	Glitch     bool `json:"glitch,omitempty"` // #13 chromatic-aberration glitch
	// OutlineR/G/B colour the outline; 0,0,0 means default white.
	OutlineR   uint8 `json:"outlineR,omitempty"`
	OutlineG   uint8 `json:"outlineG,omitempty"`
	OutlineB   uint8 `json:"outlineB,omitempty"`
	Brightness uint8 `json:"brightness,omitempty"`
	Scale      uint8 `json:"scale,omitempty"`
	Rotation   uint8 `json:"rotation,omitempty"`
	// Two-tone hue paint: split row in percent from the top (0 = one colour) and the lower colour.
	// Only sent while Tint+Grayscale is on, so a stale value can't fatten the frame.
	PaintSplit uint8 `json:"paintSplit,omitempty"`
	Paint2R    uint8 `json:"paint2R,omitempty"`
	Paint2G    uint8 `json:"paint2G,omitempty"`
	Paint2B    uint8 `json:"paint2B,omitempty"`
	// Glitch look (a courtroom.Glitch* byte) and fringe colours (all zero = red/blue); only sent while Glitch is on.
	GlitchMode uint8 `json:"glitchMode,omitempty"`
	GlitchAR   uint8 `json:"glitchAR,omitempty"`
	GlitchAG   uint8 `json:"glitchAG,omitempty"`
	GlitchAB   uint8 `json:"glitchAB,omitempty"`
	GlitchBR   uint8 `json:"glitchBR,omitempty"`
	GlitchBG   uint8 `json:"glitchBG,omitempty"`
	GlitchBB   uint8 `json:"glitchBB,omitempty"`
}

// clampOpacity bounds opacity to [0,100]; 0 means unset, so opaque.
func clampOpacity(v uint8) uint8 {
	if v > percentMax {
		return percentMax
	}
	return v
}

// StylePreset is a named local bundle of style and text colour; its emote is by name to work across characters.
type StylePreset struct {
	Name  string          `json:"name"`
	Style SpriteStylePref `json:"style"`
	Color int             `json:"color"`           // icColor text-colour index
	Emote string          `json:"emote,omitempty"` // best-effort emote anim name
	Key   string          `json:"key,omitempty"`   // bound bare key ("" = none)
}

const stylePresetCap = 48

// Character profile length caps; art and song are URLs, never data.
const (
	profileNameMax     = 40
	profilePronounsMax = 32
	profileTagMax      = 80
	profileBioMax      = 400
	profileURLMax      = 300
)

// ProfilePref is the profile card shown on the player list to AsyncAO clients; standard clients are unaffected.
type ProfilePref struct {
	Enabled    bool   `json:"enabled,omitempty"`
	ShowOnList bool   `json:"showOnList,omitempty"`
	Name       string `json:"name,omitempty"` // card title (blank = use showname/character)
	Pronouns   string `json:"pronouns,omitempty"`
	Tag        string `json:"tag,omitempty"`       // one-line status / tagline
	Bio        string `json:"bio,omitempty"`       // short free text
	ThemeSong  string `json:"themeSong,omitempty"` // URL
	ArtURL     string `json:"artURL,omitempty"`    // URL to a profile picture
}

// clampProfile bounds field lengths so a hand-edited prefs file can't smuggle in an oversized field.
func clampProfile(p ProfilePref) ProfilePref {
	p.Name = text.Truncate(p.Name, profileNameMax)
	p.Pronouns = text.Truncate(p.Pronouns, profilePronounsMax)
	p.Tag = text.Truncate(p.Tag, profileTagMax)
	p.Bio = text.Truncate(p.Bio, profileBioMax)
	p.ThemeSong = text.Truncate(p.ThemeSong, profileURLMax)
	p.ArtURL = text.Truncate(p.ArtURL, profileURLMax)
	return p
}

// AutoStatusPref sets presence from typed IC words; one field per courtroom.Status as config can't import it.
type AutoStatusPref struct {
	Enabled      bool   `json:"enabled,omitempty"`
	ClearWords   string `json:"clearWords,omitempty"` // → no status (e.g. "back")
	AFKWords     string `json:"afkWords,omitempty"`
	BusyWords    string `json:"busyWords,omitempty"`
	WritingWords string `json:"writingWords,omitempty"`
	LFRPWords    string `json:"lfrpWords,omitempty"`
}

// autoStatusWordsMax bounds each field against a hand-edited prefs file.
const autoStatusWordsMax = 200

// defaultAutoStatusPref is off with word lists pre-filled, so enabling just works.
func defaultAutoStatusPref() AutoStatusPref {
	return AutoStatusPref{
		ClearWords: "back",
		AFKWords:   "brb, afk, away",
		BusyWords:  "busy, dnd",
	}
}

func sanitizeAutoStatus(a AutoStatusPref) AutoStatusPref {
	a.ClearWords = text.Truncate(a.ClearWords, autoStatusWordsMax)
	a.AFKWords = text.Truncate(a.AFKWords, autoStatusWordsMax)
	a.BusyWords = text.Truncate(a.BusyWords, autoStatusWordsMax)
	a.WritingWords = text.Truncate(a.WritingWords, autoStatusWordsMax)
	a.LFRPWords = text.Truncate(a.LFRPWords, autoStatusWordsMax)
	return a
}

// Per-speaker name colours by stable name hash; value has a floor so a name can't go unreadably dark.
const (
	defaultNameColorSat = 60
	defaultNameColorVal = 90
	minNameColorVal     = 50
)

// Idle background slideshow, bounded so the timer can't be pathologically fast or slow.
const (
	defaultBgSlideshowSecs = 15
	minBgSlideshowSecs     = 3
	maxBgSlideshowSecs     = 600
)

// maxDownloadKBps bounds the optional download cap; 0 means unlimited and is the default.
const maxDownloadKBps = 1 << 20

// Layout scale bounds in percent.
const (
	DefaultViewportPercent = 66
	MinViewportPercent     = 40
	MaxViewportPercent     = 85

	// Exact viewport width in px, crisp at multiples of ViewportArtW (art is 256x192); 0 means size by percent.
	ViewportArtW        = 256  // native AO stage width (height = ×3/4 = 192)
	ViewportArtH        = 192  // native AO stage height
	MinViewportExactPx  = 256  // 1× — anything smaller isn't worth a fixed size
	MaxViewportExactPx  = 4096 // 16× — sanity cap (no off-screen typos)
	ViewportExactStepPx = 256  // wheel / slider step = one crisp art-multiple

	// Chat = message text size; ChatBox = box height; Log = log and OOC text; Input = entry field height.
	DefaultScalePercent = 100
	MinChatScalePercent = 50
	MaxChatScalePercent = 250
	MinChatBoxPercent   = 50
	MaxChatBoxPercent   = 200
	MinLogScalePercent  = 75
	MaxLogScalePercent  = 200
	MinInputPercent     = 75
	MaxInputPercent     = 200

	// Chatbox opacity for the flat fallback skin only: 0 = text only, 100 = solid.
	MinChatboxOpacity     = 0
	MaxChatboxOpacity     = 100
	DefaultChatboxOpacity = 84

	ScaleStepPercent = 25

	ViewportStepPercent = 5

	// Global UI scale: everything scales together and the mouse unprojects through the same factor.
	MinUIScalePercent  = 75
	MaxUIScalePercent  = 200
	UIScaleStepPercent = 5

	// DPI seeding: MinAutoUIScalePercent is above the manual floor so a bad reading can never auto-shrink the UI.
	BaselineDPI           = 96.0
	MinAutoUIScalePercent = 100

	// Default windowed size and floor; a saved size of 0 means use the default.
	DefaultWindowW = 1152
	DefaultWindowH = 864
	MinWindowW     = 640
	MinWindowH     = 480
)

// ClampWindowSize fits a request into [min, display]; usableW/H <= 0 means unknown, so no ceiling.
func ClampWindowSize(reqW, reqH, usableW, usableH int) (int, int) {
	w, h := reqW, reqH
	if w < MinWindowW {
		w = MinWindowW
	}
	if h < MinWindowH {
		h = MinWindowH
	}
	if usableW > 0 && w > usableW {
		w = usableW
	}
	if usableH > 0 && h > usableH {
		h = usableH
	}
	return w, h
}

const (
	percentMax       = 100      // the top of every 0..100 percentage setting
	rgbMask          = 0xFFFFFF // a packed 0xRRGGBB colour
	prefetchAggroMax = 4        // most predicted sprites the prefetcher warms per message
	rotationSteps    = 256      // a rotation byte covers one full turn
	degreesPerTurn   = 360
)

// defaultAudioVolume is also the slider ceiling and load-clamp max, so it must stay 100.
const defaultAudioVolume = 100

// defaultStartVolume is a fresh install's channel level, under full so a first launch isn't a blast.
const defaultStartVolume = 70

// Hold-to-clear: hold a key this long to wipe a focused text field.
const (
	defaultHoldClearOn  = true
	defaultHoldClearKey = "Backspace"
	DefaultHoldClearMs  = 1500
	MinHoldClearMs      = 300
	MaxHoldClearMs      = 5000
)

// WardrobeCap bounds one server's custom character list.
const WardrobeCap = 1024

// LocalMountCap bounds the list; each mount is walked on rescan and searched on a miss.
const LocalMountCap = 16

// FavBackgroundCap bounds one server's starred backgrounds.
const FavBackgroundCap = 512

// Message timing in milliseconds, AO2-Client options.ini parity.
const (
	// DefaultTextCrawlMs is AO2's text_crawl (options.cpp:182, courtroom.h:333) and also the blip clock.
	DefaultTextCrawlMs = 40
	MinTextCrawlMs     = 5
	MaxTextCrawlMs     = 100
	// preMoveTextCrawlMs is the only saved value load's one-shot may overwrite; others are deliberate picks.
	preMoveTextCrawlMs = 18
	// DefaultTextStayMs mirrors courtroom.DefaultTextStayTime.
	DefaultTextStayMs = 200
	MaxTextStayMs     = 3000
	// Chat rate limit: 0 = off; AO2-Client defaults to 300 ms, users opt in here.
	DefaultChatRateLimitMs = 0
	MaxChatRateLimitMs     = 5000
)

// DPIScalePercent maps DPI to the auto UI scale, floored so it never shrinks.
// It doesn't snap to the step: SetAutoScaleFromWindow does, and snapping here would double-round.
func DPIScalePercent(dpi float64) int {
	if dpi <= 0 {
		return MinAutoUIScalePercent
	}
	pct := int(math.Round(dpi / BaselineDPI * percentMax))
	if pct < MinAutoUIScalePercent {
		return MinAutoUIScalePercent
	}
	return pct
}

type AssetTypePrefs struct {
	// FormatOrder is the ordered probe list; empty means the built-in default.
	FormatOrder []string `json:"formatOrder"`
	// FallbacksEnabled appends this type's legacy chain to FormatOrder.
	FallbacksEnabled bool `json:"fallbacksEnabled"`
}

// AssetPreferences is the persisted configuration; exported methods are safe for concurrent use.
type AssetPreferences struct {
	mu        sync.RWMutex
	path      string
	dirty     chan struct{} // buffered 1: wake-up signal for the saver
	stop      chan struct{}
	done      chan struct{}
	pending   atomic.Bool // a mutation is awaiting flush
	saveDelay time.Duration
	closeOnce sync.Once
	onSaveErr atomic.Pointer[func(error)]
	// frozen blocks writes after ImportSettings replaced the file; the live session's saves would clobber the import.
	frozen atomic.Bool

	// quarantine, when non-nil, records that a corrupt file was renamed aside at load, for the UI's startup notice.
	quarantine *Quarantine

	// themeFitMovedToNative records this load's Stretch to Native move, so the caller can resize once.
	themeFitMovedToNative bool

	// themeFitStampPending records a stamp this load wrote; newWithDebounce clears it by flushing synchronously.
	themeFitStampPending bool

	// formatGen increments on any change to an effective probe list; consumers key derived format tables by it.
	formatGen atomic.Uint64

	// wardrobeGen increments on any wardrobe change; char select keys its set by it for lock-free lookups.
	wardrobeGen atomic.Uint64

	// settings is the snapshot; readers call Load, writers call Update, and it is swapped wholesale on a real change.
	settings atomic.Pointer[Settings]
}

// DiscordPrefs configures Rich Presence; the area stays hidden as some players don't want it broadcast.
type DiscordPrefs struct {
	Enabled    bool `json:"enabled"`
	ShowServer bool `json:"showServer"`
	ShowChar   bool `json:"showChar"`
	ShowName   bool `json:"showName"`
	ShowArea   bool `json:"showArea"`
	// AppID is kept only so older prefs parse; the dial uses DefaultDiscordAppID.
	AppID string `json:"appId,omitempty"`
}

// DefaultDiscordAppID is the AsyncAO Discord application, dialed directly, so it overrides any saved AppID.
const DefaultDiscordAppID = "1519625107188744222"

// defaultDiscordPrefs is on for fresh installs: stdlib IPC that no-ops without Discord (`-tags nodiscord` excludes).
func defaultDiscordPrefs() DiscordPrefs {
	return DiscordPrefs{Enabled: true, ShowServer: true, ShowChar: true, ShowName: true, AppID: DefaultDiscordAppID}
}

// ServerWarmInfo remembers a server's last visit for pre-warming and owns server-scoped player state.
type ServerWarmInfo struct {
	Char       string `json:"char,omitempty"`
	Background string `json:"background,omitempty"`

	Wardrobe []string `json:"wardrobe,omitempty"`
	// FavBackgrounds are starred backgrounds, usable even on hosts with no directory listing.
	FavBackgrounds []string `json:"favBackgrounds,omitempty"`
	// FavBackgroundFolder maps a lowercased favourite to its folder; absent = unfiled.
	FavBackgroundFolder map[string]string `json:"favBackgroundFolder,omitempty"`
	// CharKeys maps a plain key name to a character worn on press with no text field focused.
	CharKeys map[string]string `json:"charKeys,omitempty"`
	// WardrobeFolder maps a lowercased wardrobe character to its folder; absent = unsorted.
	WardrobeFolder map[string]string `json:"wardrobeFolder,omitempty"`
	// Origin is the last asset URL; rehearsal mode rebuilds asset paths from it without connecting.
	Origin string `json:"origin,omitempty"`
	// Per-server audio override; channels are pointers so nil (use the global) differs from 0 (mute).
	AudioOn     bool `json:"audioOn,omitempty"`
	AudioMaster *int `json:"audioMaster,omitempty"`
	AudioMusic  *int `json:"audioMusic,omitempty"`
	AudioSFX    *int `json:"audioSFX,omitempty"`
	AudioBlip   *int `json:"audioBlip,omitempty"`
	// Chars is the last character list, for rehearsal char select.
	Chars []string `json:"chars,omitempty"`
	// Backgrounds seeds the picker and slideshow instantly on connect, until a fresh autoindex fetch refreshes it.
	Backgrounds []string `json:"backgrounds,omitempty"`
	// Friends are highlighted shownames, per server.
	Friends []string `json:"friends,omitempty"`
	// Ignored shownames are dropped at ingest; showname else character is the only identity on the MS wire.
	Ignored []string `json:"ignored,omitempty"`
	// Account login, not mod-only (Akashi, tsuserver forks). Plaintext in the prefs file; the settings UI says so.
	LoginUser string `json:"loginUser,omitempty"`
	LoginPass string `json:"loginPass,omitempty"`
	AutoLogin bool   `json:"autoLogin,omitempty"`
	// Theme binds a theme to this server, "" = the global pick; disconnecting restores the global theme.
	Theme string `json:"theme,omitempty"`
}

const charKeyCap = 36

// MacroSpec is a name, optional key bind and OOC lines, paced by the UI for flows like Akashi's two-step login.
type MacroSpec struct {
	Name  string   `json:"name"`
	Key   string   `json:"key,omitempty"`
	Lines []string `json:"lines"`
}

const (
	MacroCap      = 64  // macros per user
	MacroLinesCap = 8   // lines per macro
	MacroLineMax  = 256 // chars per line
)

// sanitizeMacros enforces the caps and drops empty entries.
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

// WarmCharsCap bounds one server's character list, covering 4000-char servers at about 60 KiB of JSON.
const WarmCharsCap = 4096

// WarmBgsCap matches WarmCharsCap; big servers have comparable background counts.
const WarmBgsCap = 4096

const WarmFriendsCap = 256

const WarmIgnoredCap = 256

// serverWarmCap bounds the per-server table; when full, new servers don't record until entries are cleared.
const serverWarmCap = 64

// FavoriteServer is a phone-book entry; Description keeps it informative when the master list is unreachable.
type FavoriteServer struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// OpenTab is a remembered tab for restore-on-launch.
type OpenTab struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// DefaultPath returns the preferences file location, resolved by ConfigBaseDir.
func DefaultPath() (string, error) {
	dir, err := ConfigBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, PrefsFileName), nil
}

// New loads preferences and starts the saver; an error reports a bad file replaced by defaults. Close flushes.
func New(path string) (*AssetPreferences, error) {
	return newWithDebounce(path, DefaultSaveDebounce)
}

func newWithDebounce(path string, debounce time.Duration) (*AssetPreferences, error) {
	p, err := load(path)
	p.saveDelay = debounce
	p.dirty = make(chan struct{}, 1)
	p.stop = make(chan struct{})
	p.done = make(chan struct{})
	// Runs here, not in load: load is half-overlaid at this decision, so writing there would persist defaults.
	if p.themeFitStampPending {
		p.themeFitStampPending = false
		if serr := p.SaveNow(); serr != nil {
			// SaveNow cleared pending before failing; re-mark so the saver and Close still retry.
			p.markDirty()
			log.Printf("config: could not persist the theme-fit migration stamp: %v", serr)
		}
	}
	go p.saverLoop()
	return p, err
}

// defaultPrefs builds an all-defaults AssetPreferences with no disk read and no saver.
func defaultPrefs(path string) *AssetPreferences {
	p := &AssetPreferences{path: path}
	p.settings.Store(defaultSettings())
	return p
}

// defaultSettings is the one place the default state lives; load and the resets both use it.
func defaultSettings() *Settings {
	return &Settings{
		PreferAnimated:       defaultPreferAnimated,
		EmoteButtonImages:    defaultEmoteButtonImages,
		ShowFriendButton:     defaultShowFriendButton,
		ClipSpritesToStage:   defaultClipSpritesToStage,
		EventDrivenLoop:      defaultEventDrivenLoop,
		AutoClipModcall:      defaultAutoClipModcall,
		GroupChatButton:      defaultGroupChatButton,
		CharChatbox:          defaultCharChatbox,
		RightClickHideSprite: defaultRightClickHideSprite,
		DragLayout:           defaultDragLayout,

		RainbowSpriteSpeed:     defaultRainbowSpeed,
		ReplayPlaybackSpeed:    defaultReplaySpeed,
		Export:                 defaultExportOptions(),
		ChatboxOpacity:         DefaultChatboxOpacity,
		RainbowSpriteVividness: defaultRainbowVivid,
		SpriteTintColor:        defaultSpriteTintColor,
		SmoothScaling:          defaultSmoothScaling,
		SpriteScaling:          defaultSpriteScaling,
		SpriteScalingMigrated:  true,
		UpdateCheck:            defaultUpdateCheck,
		ShowAssetWarnings:      defaultShowAssetWarnings,
		SpriteMoveOn:           defaultSpriteMove,
		DeskFollowManifest:     defaultDeskFollowManifest,
		SpritePreviewOn:        defaultSpritePreview,
		PreviewHoverMs:         DefaultPreviewHoverMs,
		EmoteHoverNamesOn:      defaultEmoteHoverNames,
		EmoteHoverNamesMs:      DefaultEmoteHoverNamesMs,
		EmoteGridGapPx:         DefaultEmoteGridGapPx,
		AutoLoginToast:         defaultAutoLoginToast,
		ScreenEffects:          defaultScreenEffects,
		FontCensus:             defaultFontCensus,
		WordDelete:             defaultWordDelete,
		RecordingsKeepAssets:   defaultRecordingsKeepAssets,
		AdditiveText:           defaultAdditiveText,
		CallwordToast:          defaultCallwordToast,
		MessageCounter:         defaultMessageCounter,
		ICTimestamps:           defaultICTimestamps,
		AutoReconnect:          defaultAutoReconnect,
		StickySounds:           defaultStickySounds,
		StickyEffects:          defaultStickyEffects,
		StickyPreanims:         defaultStickyPreanims,
		SFXOnIdle:              defaultSFXOnIdle,
		UnfocusedFullRate:      defaultUnfocusedFullRate,
		GhostCrawlText:         defaultGhostCrawlText,
		DetailedLog:            defaultDetailedLog,
		// One-shot stamps: a fresh install has nothing to move; only a file lacking them triggers the moves.
		AutoReconnectDefaultMigrated: true,
		CatchUpDefaultMigrated:       true,
		ThemeFitDefaultMigrated:      true,
		BlipSpeedDefaultMigrated:     true,
		DeskManifestDefaultMigrated:  true,
		DetailedLogDefaultMigrated:   true,
		MusicHistory:                 defaultMusicHistory,
		MusicStreaming:               defaultMusicStreaming,
		ShowMissingPlaceholder:       defaultShowMissingPlaceholder,
		MusicHosts:                   defaultMusicHostList(),
		HighlightColor:               defaultHighlightColor,
		ICCustomColor:                defaultICCustomColor,
		NameSat:                      defaultNameColorSat,
		NameVal:                      defaultNameColorVal,
		BgSlideshowSecs:              defaultBgSlideshowSecs,
		AutoDetectFormats:            defaultFormatAutoDetect,
		BlipRate:                     defaultBlipRate,
		ThemeLayoutOn:                defaultThemeLayout,
		ThemeFit:                     defaultThemeFit,
		ThemeFitZoom:                 DefaultThemeZoom,
		PlainLobby:                   defaultPlainLobby,
		ThemeFonts:                   defaultThemeFonts,
		UIScaleAutoOn:                defaultUIScaleAuto,
		CatchUpOn:                    defaultCatchUpWhenBehind,
		ShoutBypassQueue:             defaultShoutBypassQueue,
		CatchUpThreshold:             DefaultCatchUpThreshold,
		MultiTabCap:                  DefaultMultiTabCap,
		MusicFlagsVal:                DefaultMusicFlags,           // AO2's own default (courtroom.h:551 music_flags = FADE_OUT)
		SpriteLoadModeVal:            defaultSpriteLoadMode,       // Default mode (strict sequencing) by default
		MotionRedrawPerEvent:         defaultMotionRedrawPerEvent, // per-event motion redraw ON by default (less GPU on a moving cursor)
		DiscordRPC:                   defaultDiscordPrefs(),
		MyAutoStatus:                 defaultAutoStatusPref(),
		ChromeThemeKey:               "dark",
		ChromeShapeKey:               defaultChromeShape, // A5: "sharp" — byte-identical to today's flat chrome
		ViewportPct:                  DefaultViewportPercent,
		ChatScalePct:                 DefaultScalePercent,
		ChatBoxPct:                   DefaultScalePercent,
		LogScalePct:                  DefaultScalePercent,
		// Seeded here: load returns defaults verbatim for a missing or quarantined file; 0 floors to a 1 px font.
		OOCScalePct:    DefaultScalePercent,
		InputHeightPct: DefaultScalePercent,
		UIScalePct:     DefaultScalePercent,
		// OOC gets its own box on a fresh install; the log-tab hybrid is opt-in.
		OOCInLogTab: false,

		MusicVol:        defaultStartVolume,
		SFXVol:          defaultStartVolume,
		BlipVol:         defaultStartVolume,
		AlertVol:        defaultAudioVolume, // alerts/pings stay loud — they should reach you
		MasterVol:       defaultAudioVolume, // master at 100 (scales the channels; ceiling intact)
		HoldClearOn:     defaultHoldClearOn,
		HoldClearKey:    defaultHoldClearKey,
		HoldClearMs:     DefaultHoldClearMs,
		TextCrawlMs:     DefaultTextCrawlMs,
		TextStayMs:      DefaultTextStayMs,
		ChatRateLimitMs: DefaultChatRateLimitMs,
		AssetTypes:      defaultAssetTypes(),
		LearnedFormats:  map[string][]string{},
	}
}

// Quarantine records a corrupt file renamed aside before defaults could overwrite it; the caller shows the notice.
type Quarantine struct {
	// BackupPath is "" if the rename failed.
	BackupPath string

	Err error
}

// Quarantine returns the record from load, or nil if the file was clean or absent.
func (p *AssetPreferences) Quarantine() *Quarantine { return p.quarantine }

func load(path string) (*AssetPreferences, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return defaultPrefs(path), nil // first run
	}
	if err != nil {
		return defaultPrefs(path), fmt.Errorf("config: reading %s: %w", path, err)
	}

	p, perr := snapshotLoad(data)
	p.path = path
	if perr != nil {
		// Quarantine first so the saver can't overwrite the only copy; a sync rename is fine at startup (hard rule 2).
		perr = fmt.Errorf("config: parsing %s (using defaults): %w", path, perr)
		backup := path + corruptSuffixPrefix + time.Now().Format(corruptStampLayout)
		if renameErr := os.Rename(path, backup); renameErr == nil {
			p.quarantine = &Quarantine{BackupPath: backup, Err: perr}
		} else {
			// Rename failed: don't advertise a backup path that doesn't exist; the app still boots on defaults.
			log.Printf("config: could not quarantine corrupt %s: %v", path, renameErr)
			p.quarantine = &Quarantine{Err: perr}
		}
		return p, perr
	}
	return p, nil
}

// SetSaveErrorHook receives the saver's asynchronous failures; the default logs.
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

// markDirty records a change and wakes the saver without blocking; callers hold the write lock.
func (p *AssetPreferences) markDirty() {
	markDirtyCalls.Add(1)
	p.pending.Store(true)
	select {
	case p.dirty <- struct{}{}:
	default:
	}
}

// saverLoop debounces dirty signals; it holds no locks while idle and never blocks mutators.
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

// resetContentFields names the content fields ResetSettings keeps, keyed by Go field name.
var resetContentFields = map[string]bool{
	"Favorites":          true, // saved servers (phone book)
	"Wardrobe":           true, // legacy flat wardrobe
	"EmoteFavs":          true, // per-character favourited emotes
	"ServerWarm":         true, // per-server: char/bg/wardrobe/folders/LOGINS/friends
	"UserMacros":         true,
	"CallWordList":       true,
	"LearnedFormats":     true, // learned per-host formats (cache-adjacent)
	"ThemeRectOv":        true, // layout-editor overrides
	"ThemeRectRotations": true, // layout-editor rotation overrides (A4) — user content, survives a settings reset like ThemeRectOv (ClassicRotations is NOT preserved, mirroring ClassicLayout/ClassicAnchors)
	"LayoutProfiles":     true, // saved full-state layout profiles (A6) — user content, survives a settings reset like ThemeRectOv
	"OpenTabs":           true,
	"LocalAssetsPaths":   true, // local mount config
	"LocalAssetsEnabled": true,
	"LocalAssetsLayered": true, // ...and the mode that reads them; without this a Reset drops a layered user to stream-only while KEEPING their mounts, which reads as "my pack stopped working"
	"Showname":           true, // your identity
	"OOCName":            true,
}

// resetLocked resets exported fields not in keep, by reflection so new settings reset; caller holds the write lock.
func (p *AssetPreferences) resetLocked(keep map[string]bool) {
	fresh := defaultSettings()
	cur := p.Load()
	if cur != nil {
		cv, fv := reflect.ValueOf(cur).Elem(), reflect.ValueOf(fresh).Elem()
		t := cv.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" || !keep[f.Name] { // unexported machinery / reset-to-default content
				continue
			}
			fv.Field(i).Set(cv.Field(i)) // preserved content rides over the defaults
		}
	}
	p.settings.Store(fresh)
	p.formatGen.Add(1)   // format/fallback prefs may have changed; invalidate resolver caches
	p.wardrobeGen.Add(1) // a reset can wipe ServerWarm (and its wardrobes)
	p.markDirty()
}

// ResetSettings reverts tunable settings but keeps content and the disk cache; persisted at once.
func (p *AssetPreferences) ResetSettings() {
	p.mu.Lock()
	p.resetLocked(resetContentFields)
	p.mu.Unlock()
	_ = p.SaveNow()
}

// ResetAll restores a fresh-install state, content included; the caller clears the disk cache. Persisted at once.
func (p *AssetPreferences) ResetAll() {
	p.mu.Lock()
	p.resetLocked(nil)
	p.mu.Unlock()
	_ = p.SaveNow()
}

// SaveNow synchronously replaces the file, for shutdown and Settings-Apply; routine mutations use the saver.
func (p *AssetPreferences) SaveNow() error {
	if p.frozen.Load() {
		return nil // an import owns the file now; restart applies it
	}
	// Clear before marshaling: a concurrent mutation re-marks pending for the next flush if this marshal misses it.
	p.pending.Store(false)

	data, err := json.MarshalIndent(p.Load(), jsonMarshalPrefix, jsonMarshalIndent)
	if err != nil {
		return fmt.Errorf("config: marshaling preferences: %w", err)
	}
	return atomicWriteFile(p.path, data, prefsFilePerm)
}

// Close stops the saver and flushes; only the first call does work.
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

// atomicWriteFile writes a synced temp file and renames it over path, so readers never see a partial file.
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

// FormatGeneration changes on any probe-list mutation, so derived caches rebuild without holding locks.
func (p *AssetPreferences) FormatGeneration() uint64 {
	return p.formatGen.Load()
}

// FormatList returns a fresh slice: the configured list plus, when fallbacks are on, the legacy chain deduplicated.
func (s *Settings) FormatList(typeName string) []string {

	tp, ok := s.AssetTypes[typeName]
	order := tp.FormatOrder
	if !ok || len(order) == 0 {
		order = defaultFormatOrders[typeName]
	}

	withFallbacks := s.GlobalFallbacksEnabled || tp.FallbacksEnabled
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

// FormatOrder returns the configured probe order, before fallbacks.
func (s *Settings) FormatOrder(typeName string) []string {

	if tp, ok := s.AssetTypes[typeName]; ok && len(tp.FormatOrder) > 0 {
		return cloneStrings(tp.FormatOrder)
	}
	return DefaultFormatOrder(typeName)
}

// SetFormatOrder replaces a type's order and invalidates its learned formats, which may no longer lead.
func (p *AssetPreferences) SetFormatOrder(typeName string, order []string) {
	changed := false
	p.Update(func(sn *Settings) {
		tp := sn.AssetTypes[typeName]
		if slices.Equal(tp.FormatOrder, order) {
			return
		}
		tp.FormatOrder = cloneStrings(order)
		if sn.AssetTypes == nil {
			sn.AssetTypes = map[string]AssetTypePrefs{}
		}
		sn.AssetTypes[typeName] = tp
		suffix := LearnedKeySeparator + typeName
		for key := range sn.LearnedFormats {
			if strings.HasSuffix(key, suffix) {
				delete(sn.LearnedFormats, key)
			}
		}
		changed = true
	})
	if changed {
		p.formatGen.Add(1)
	}
}

// ResetFormatOrder restores the default order and invalidates the type's learned formats.
func (p *AssetPreferences) ResetFormatOrder(typeName string) {
	p.SetFormatOrder(typeName, DefaultFormatOrder(typeName))
}

// TypeFallbacksEnabled reports the per-type toggle, not the global one.
func (s *Settings) TypeFallbacksEnabled(typeName string) bool {

	return s.AssetTypes[typeName].FallbacksEnabled
}

// SetTypeFallbacks toggles a type's chain and invalidates its learned formats.
func (p *AssetPreferences) SetTypeFallbacks(typeName string, enabled bool) {
	changed := false
	p.Update(func(sn *Settings) {
		tp := sn.AssetTypes[typeName]
		if tp.FallbacksEnabled == enabled {
			return
		}
		tp.FallbacksEnabled = enabled
		if len(tp.FormatOrder) == 0 {
			tp.FormatOrder = DefaultFormatOrder(typeName)
		}
		if sn.AssetTypes == nil {
			sn.AssetTypes = map[string]AssetTypePrefs{}
		}
		sn.AssetTypes[typeName] = tp
		suffix := LearnedKeySeparator + typeName
		for key := range sn.LearnedFormats {
			if strings.HasSuffix(key, suffix) {
				delete(sn.LearnedFormats, key)
			}
		}
		changed = true
	})
	if changed {
		p.formatGen.Add(1)
	}
}

// SetGlobalFallbacks toggles all fallbacks and invalidates all learned formats, since every probe list changed.
func (p *AssetPreferences) SetGlobalFallbacks(enabled bool) {
	changed := false
	p.Update(func(sn *Settings) {
		if sn.GlobalFallbacksEnabled == enabled {
			return
		}
		sn.GlobalFallbacksEnabled = enabled
		sn.LearnedFormats = map[string][]string{}
		changed = true
	})
	if changed {
		p.formatGen.Add(1)
	}
}

// SpriteScalingMode is clamped on read too, so a newer build's value can't index past this build's options.
func (p *AssetPreferences) SpriteScalingMode() int {
	snap := p.Load()

	return ClampSpriteScaling(snap.SpriteScaling)
}

// PreviewHoverMillis returns the clamped dwell for the Settings readout.
func (s *Settings) PreviewHoverMillis() int {
	return value.Clamp(orDefault(s.PreviewHoverMs, DefaultPreviewHoverMs), minPreviewHoverMs, maxPreviewHoverMs)
}

func (p *AssetPreferences) PreviewHoverDelay() time.Duration {
	return time.Duration(p.Load().PreviewHoverMillis()) * time.Millisecond
}

// PreviewHeightPx returns the default box height, clamped to the grip's bounds.
func (s *Settings) PreviewHeightPx() int {
	return value.Clamp(orDefault(s.PreviewHeightPxVal, DefaultPreviewHeightPx), MinPreviewHeightPx, MaxPreviewHeightPx)
}

func (p *AssetPreferences) SetPreviewHeightPx(n int) {
	n = value.Clamp(n, MinPreviewHeightPx, MaxPreviewHeightPx)
	p.Update(func(sn *Settings) {
		sn.PreviewHeightPxVal = n
	})
}

// PreviewWindowRect returns the saved rect; ok is false if never saved, as a zero rect can't mean that.
func (s *Settings) PreviewWindowRect() (x, y, w, h int, ok bool) {

	if !s.PreviewWinRectValid {
		return 0, 0, 0, 0, false
	}
	return s.PreviewWinRectX, s.PreviewWinRectY, s.PreviewWinRectW, s.PreviewWinRectH, true
}

// SetPreviewWindowRect is called only when the window closes, never per move or resize (hard rules 2 and 3).
func (p *AssetPreferences) SetPreviewWindowRect(x, y, w, h int) {
	p.Update(func(sn *Settings) {
		sn.PreviewWinRectValid = true
		sn.PreviewWinRectX = x
		sn.PreviewWinRectY = y
		sn.PreviewWinRectW = w
		sn.PreviewWinRectH = h
	})
}

// EmoteHoverNamesMillis returns the clamped dwell for the Settings readout.
func (s *Settings) EmoteHoverNamesMillis() int {
	return value.Clamp(orDefault(s.EmoteHoverNamesMs, DefaultEmoteHoverNamesMs), minEmoteHoverNamesMs, maxEmoteHoverNamesMs)
}

func (p *AssetPreferences) EmoteHoverNamesDelay() time.Duration {
	return time.Duration(p.Load().EmoteHoverNamesMillis()) * time.Millisecond
}

// EmoteGridGap takes the pref lock, so read it once per grid draw; 0 is a valid pick, with no default rescue.
func (p *AssetPreferences) EmoteGridGap() int {
	snap := p.Load()

	return value.Clamp(snap.EmoteGridGapPx, MinEmoteGridGapPx, MaxEmoteGridGapPx)
}

// ClearLearnedType drops every host's learned format for one type, so the next probe re-derives it.
func (p *AssetPreferences) ClearLearnedType(typeName string) {
	p.Update(func(sn *Settings) {
		dropLearnedType(sn, typeName)
	})
	p.formatGen.Add(1)
}

// ClearLearnedHost drops one host's formats, sparing other servers a re-probe, and reports how many.
// The separator can't occur in a host, so the prefix match is exact.
func (p *AssetPreferences) ClearLearnedHost(host string) int {
	if host == "" {
		return 0
	}
	prefix := host + LearnedKeySeparator
	n := 0
	p.Update(func(sn *Settings) {
		for key := range sn.LearnedFormats {
			if strings.HasPrefix(key, prefix) {
				delete(sn.LearnedFormats, key)
				n++
			}
		}
	})
	return n
}

func (s *Settings) HighlightColorRGB() int {

	return s.HighlightColor & rgbMask
}

// ICCustomColorRGB returns the last free IC pick, packed 0xRRGGBB, seeding the colour wheel.
func (s *Settings) ICCustomColorRGB() int {

	return s.ICCustomColor & rgbMask
}

func (p *AssetPreferences) SetNameColorSatVal(sat, val int) {
	p.Update(func(sn *Settings) {
		sn.NameSat = sat
		sn.NameVal = val
	})
}

// BgSlideshowSeconds is the clamped dwell per background; unset falls back to the default.
func (s *Settings) BgSlideshowSeconds() int {
	return value.Clamp(orDefault(s.BgSlideshowSecs, defaultBgSlideshowSecs), minBgSlideshowSecs, maxBgSlideshowSecs)
}

// DownloadCapKBps reports the bandwidth cap in KiB/s, 0 = unlimited.
func (p *AssetPreferences) DownloadCapKBps() int {
	snap := p.Load()

	if snap.DownloadKBps < 0 {
		return 0
	}
	if snap.DownloadKBps > maxDownloadKBps {
		return maxDownloadKBps
	}
	return snap.DownloadKBps
}

// Instant-replay window bounds: a floor so a clip has context, a 1-hour ceiling; 0 reads as default.
const (
	InstantReplayMinSeconds     = 10
	InstantReplayMaxSeconds     = 3600
	InstantReplayDefaultSeconds = 60
)

// Local timer bounds; only the remembered duration persists, running state is session-only.
const (
	TimerMinSeconds     = 1
	TimerMaxSeconds     = 99 * 60
	TimerDefaultSeconds = 5 * 60
)

func clampTimerSeconds(s int) int {
	switch {
	case s <= 0:
		return TimerDefaultSeconds
	case s < TimerMinSeconds:
		return TimerMinSeconds
	case s > TimerMaxSeconds:
		return TimerMaxSeconds
	default:
		return s
	}
}

// TimerSecondsValue returns the clamped remembered duration; 0 reads as default.
func (s *Settings) TimerSecondsValue() int {
	return clampTimerSeconds(s.TimerSeconds)
}

func (p *AssetPreferences) SetTimerSeconds(s int) {
	s = clampTimerSeconds(s)
	p.Update(func(sn *Settings) {
		sn.TimerSeconds = s
	})
}

func clampInstantReplaySeconds(s int) int {
	switch {
	case s <= 0:
		return InstantReplayDefaultSeconds
	case s < InstantReplayMinSeconds:
		return InstantReplayMinSeconds
	case s > InstantReplayMaxSeconds:
		return InstantReplayMaxSeconds
	default:
		return s
	}
}

// InstantReplaySecondsValue returns the clamped window in seconds; 0 reads as default.
func (s *Settings) InstantReplaySecondsValue() int {
	return clampInstantReplaySeconds(s.InstantReplaySeconds)
}

// LastServer returns the auto-connect target; empty url means none yet.
func (s *Settings) LastServer() (name, url string) {

	return s.LastServerName, s.LastServerURL
}

// SetLastServer is called on every connect.
func (p *AssetPreferences) SetLastServer(name, url string) {
	p.Update(func(sn *Settings) {
		sn.LastServerName = name
		sn.LastServerURL = url
	})
}

func (p *AssetPreferences) SetExportOpts(o ExportOptions) {
	o.HeightPx = value.Clamp(o.HeightPx, minExportHeight, maxExportHeight)
	o.FPS = value.Clamp(o.FPS, minExportFPS, maxExportFPS)
	o.Quality = value.Clamp(o.Quality, minExportQuality, maxExportQuality)
	o.TextScale = value.Clamp(o.TextScale, minExportText, maxExportText)
	o.VideoFormat = normalizeVideoFormat(o.VideoFormat)
	if runes := []rune(o.WatermarkText); len(runes) > maxWatermarkLen {
		o.WatermarkText = string(runes[:maxWatermarkLen]) // rune-safe: never split a glyph
	}
	p.Update(func(sn *Settings) {
		sn.Export = o
	})
}

func (p *AssetPreferences) SetSpriteStyle(s SpriteStylePref) {
	s.Opacity = clampOpacity(s.Opacity)
	p.Update(func(sn *Settings) {
		sn.MySpriteStyle = s
	})
}

// sanitizeStylePresets bounds, drops nameless entries and clamps, so a hand-edited file can't corrupt the list.
func sanitizeStylePresets(in []StylePreset) []StylePreset {
	if len(in) == 0 {
		return nil
	}
	out := make([]StylePreset, 0, len(in))
	for _, pr := range in {
		if strings.TrimSpace(pr.Name) == "" || len(out) >= stylePresetCap {
			continue
		}
		pr.Style.Opacity = clampOpacity(pr.Style.Opacity)
		pr.Key = strings.ToLower(strings.TrimSpace(pr.Key))
		out = append(out, pr)
	}
	return out
}

// StylePresetCount doesn't allocate, for the Style box's per-frame height calculation.
func (s *Settings) StylePresetCount() int {

	return len(s.SavedStyles)
}

// StylePresets returns a copy so a caller can't mutate the live slice.
func (s *Settings) StylePresets() []StylePreset {

	if len(s.SavedStyles) == 0 {
		return nil
	}
	return append([]StylePreset(nil), s.SavedStyles...)
}

// AddStylePreset overwrites a duplicate name or appends, ignoring a blank name or a full list.
func (p *AssetPreferences) AddStylePreset(pr StylePreset) {
	if strings.TrimSpace(pr.Name) == "" {
		return
	}
	pr.Style.Opacity = clampOpacity(pr.Style.Opacity)
	pr.Key = strings.ToLower(strings.TrimSpace(pr.Key))
	p.Update(func(sn *Settings) {
		sn.AddStylePreset(pr)
	})
}

func (p *AssetPreferences) DeleteStylePreset(i int) {
	p.Update(func(sn *Settings) {
		sn.DeleteStylePreset(i)
	})
}

// SetStylePresetKey binds or, with "", clears a key, removing it from other presets so it maps to one.
func (p *AssetPreferences) SetStylePresetKey(i int, key string) {
	key = strings.ToLower(strings.TrimSpace(key))
	p.Update(func(sn *Settings) {
		sn.SetStylePresetKey(i, key)
	})
}

// StylePresetForKey returns the preset bound to a bare key.
func (s *Settings) StylePresetForKey(key string) (StylePreset, bool) {

	key = strings.ToLower(strings.TrimSpace(key))

	for _, pr := range s.SavedStyles {
		if pr.Key != "" && pr.Key == key {
			return pr, true
		}
	}
	return StylePreset{}, false
}

// SetAssetOriginHeader sets or, when blank, clears the asset-fetch Origin override.

func (p *AssetPreferences) SetAssetOriginHeader(s string) {
	s = strings.TrimSpace(s)
	if p.Load().AssetOrigin == s {
		return
	}
	p.Update(func(sn *Settings) { sn.AssetOrigin = s })
}

// SetWSOriginHeader sets or clears the override; applies on the next connect, as open sockets keep their handshake.
func (p *AssetPreferences) SetWSOriginHeader(s string) {
	s = strings.TrimSpace(s)
	if p.Load().WSOrigin == s {
		return
	}
	p.Update(func(sn *Settings) { sn.WSOrigin = s })
}

// PanelFont is one panel's font; family and size are independently optional.
type PanelFont struct {
	// Family is a name or file path, resolved off-thread; names work on Windows, a path everywhere.
	Family string `json:"family,omitempty"`
	// SizePx is in pixels, 0 = keep the theme's or client's size.
	SizePx int `json:"px,omitempty"`
}

// Panel-font bounds: the map comes off disk, so size and values need caps (hard rule 4).
const (
	// PanelFontMaxEntries exists only so a hand-edited or corrupt file can't grow the map.
	PanelFontMaxEntries = 32
	// Size bounds: below 6 px isn't legible, above 96 one line fills a panel; wider than the theme clamp on purpose.
	PanelFontMinPx = 6
	PanelFontMaxPx = 96
	// PanelFontFamilyMaxLen bounds a family or path; longer is a corrupt file.
	PanelFontFamilyMaxLen = 260
)

// sanitizePanelFonts keeps only entries the resolver will act on, so Settings and what applies can't drift.
func sanitizePanelFonts(in map[string]PanelFont) map[string]PanelFont {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]PanelFont, len(in))
	for id, pf := range in {
		if len(out) >= PanelFontMaxEntries {
			break
		}
		id = strings.TrimSpace(strings.ToLower(id))
		if id == "" {
			continue
		}
		pf.Family = strings.TrimSpace(pf.Family)
		if len(pf.Family) > PanelFontFamilyMaxLen {
			pf.Family = ""
		}
		if pf.SizePx != 0 && (pf.SizePx < PanelFontMinPx || pf.SizePx > PanelFontMaxPx) {
			pf.SizePx = 0
		}
		if pf.Family == "" && pf.SizePx == 0 {
			continue // an empty override is the absence of one
		}
		out[id] = pf
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FontWarnThemesCap bounds the warned set, oldest first out, so a long-ago theme is eventually warned about again.
const FontWarnThemesCap = 64

// clampStrings trims a list read off disk to the newest n entries, dropping blanks.
func clampStrings(in []string, n int) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// FontWarnShownFor reports whether this theme's missing-font warning was shown.
func (s *Settings) FontWarnShownFor(theme string) bool {

	theme = strings.TrimSpace(theme)
	if theme == "" {
		return true // nothing to key on: never warn rather than warn every apply
	}

	return containsFold(s.FontWarnThemes, theme)
}

// MarkFontWarnShown records a warned theme; idempotent.
func (p *AssetPreferences) MarkFontWarnShown(theme string) {
	theme = strings.TrimSpace(theme)
	if theme == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.MarkFontWarnShown(theme)
	})
}

// ClearFontWarnThemes forgets warned themes so the warnings come back.
func (p *AssetPreferences) ClearFontWarnThemes() {
	p.Update(func(sn *Settings) {
		sn.ClearFontWarnThemes()
	})
}

// PanelFonts returns a copy: the saver marshals the original, so handing out the live map would be a data race.
func (s *Settings) PanelFonts() map[string]PanelFont {

	if len(s.PanelFontsVal) == 0 {
		return nil
	}
	out := make(map[string]PanelFont, len(s.PanelFontsVal))
	for k, v := range s.PanelFontsVal {
		out[k] = v
	}
	return out
}

// PanelFontFor returns one element's override; the zero value means none.
func (s *Settings) PanelFontFor(id string) PanelFont {

	return s.PanelFontsVal[strings.ToLower(id)]
}

// SetPanelFont removes an empty override rather than storing it, so a later default change reaches a reset element.
func (p *AssetPreferences) SetPanelFont(id string, pf PanelFont) {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return
	}
	pf.Family = strings.TrimSpace(pf.Family)
	if len(pf.Family) > PanelFontFamilyMaxLen {
		pf.Family = pf.Family[:PanelFontFamilyMaxLen]
	}
	if pf.SizePx != 0 {
		pf.SizePx = value.Clamp(pf.SizePx, PanelFontMinPx, PanelFontMaxPx)
	}
	p.Update(func(sn *Settings) {
		sn.SetPanelFont(id, pf)
	})
}

// ClearPanelFonts drops every per-panel override at once.
func (p *AssetPreferences) ClearPanelFonts() {
	p.Update(func(sn *Settings) {
		sn.ClearPanelFonts()
	})
}

// Proxy modes are persisted, so append only; they mirror netproxy.Mode, as config sits below internal/netproxy.
const (
	// ProxyModeSystem takes the machine's own proxy configuration.
	ProxyModeSystem = 0
	// ProxyModeDirect never proxies: the one-click escape when a detected proxy can't carry AO traffic.
	ProxyModeDirect = 1

	ProxyModeManual = 2
	// ProxyModeCount bounds the Settings cycle button and both clamps.
	ProxyModeCount = 3
)

// SetProxyURL doesn't validate: a bad address must reach netproxy, which fails closed and says so in the readout.
func (p *AssetPreferences) SetProxyURL(s string) {
	s = strings.TrimSpace(s)
	if p.Load().ProxyURL == s {
		return
	}
	p.Update(func(sn *Settings) { sn.ProxyURL = s })
}

// AssetCharCaseMax mirrors courtroom.CharCaseCount-1, spelled out because config sits below internal/courtroom.
const AssetCharCaseMax uint8 = 3

// SetAssetCharCasing falls back to lowercase when out of range, as URLBuilder.WithCharCase does.
func (p *AssetPreferences) SetAssetCharCasing(c uint8) {
	if c > AssetCharCaseMax {
		c = 0
	}
	p.Update(func(sn *Settings) {
		sn.AssetCharCase = c
	})
}

// VoiceOutVol reports the volume 0..100, 100 when unset.
func (s *Settings) VoiceOutVol() int {
	return value.Clamp(orDefault(s.VoiceOutVolume, defaultVoiceOutVol), 0, percentMax)
}

// PrefetchAggressiveness is how many predicted sprites the Markov prefetcher warms per message, 1..4.
func (s *Settings) PrefetchAggressiveness() int {
	return value.Clamp(s.PrefetchAggro, 1, prefetchAggroMax)
}

func (p *AssetPreferences) SetPrefetchAggressiveness(n int) {
	n = value.Clamp(n, 1, prefetchAggroMax)
	p.Update(func(sn *Settings) {
		sn.PrefetchAggro = n
	})
}

func (p *AssetPreferences) SetProfile(pr ProfilePref) {
	pr = clampProfile(pr)
	p.Update(func(sn *Settings) {
		sn.MyProfile = pr
	})
}

// Spotlight dim bounds stay off 0 and 100 so on always reads as a usable shadow.

const (
	minSpotlightStrength     = 10
	maxSpotlightStrength     = 90
	defaultSpotlightStrength = 55
)

// SpotlightLevel reports the dim, higher = darker; 0 resolves to the default so enabling always shows.

func (s *Settings) SpotlightLevel() int {
	return orDefault(s.SpotlightStrength, defaultSpotlightStrength)
}

func (p *AssetPreferences) SetSpotlightLevel(v int) {
	v = value.Clamp(v, minSpotlightStrength, maxSpotlightStrength)
	p.Update(func(sn *Settings) {
		sn.SpotlightStrength = v
	})
}

// Idle-breathing defaults; components are stored inverted so the zero value breathes both ways.
const (
	defaultBreathAmp   = 40
	defaultBreathSpeed = 50
)

// BreathBobOn and BreathScaleOn invert the stored disabled flags.

func (s *Settings) BreathBobOn() bool {

	return !s.BreathNoBob
}
func (s *Settings) BreathScaleOn() bool {

	return !s.BreathNoScale
}

// BreathAmp and BreathSpeed report sliders [1,100]; 0 resolves to the default.
func (s *Settings) BreathAmp() int {
	return orDefault(s.BreathAmount, defaultBreathAmp)
}

func (s *Settings) BreathSpeed() int {
	return orDefault(s.BreathRate, defaultBreathSpeed)
}

// defaultReflectStrength applies when unset so enabling is visible at once.
const defaultReflectStrength = 30

// ReflectStrength reports opacity [0,100]; 0 resolves to the default.

func (s *Settings) ReflectStrength() int {
	return orDefault(s.ReflectOpacity, defaultReflectStrength)
}

func (p *AssetPreferences) SetReflectStrength(v int) {
	v = value.Clamp(v, 0, percentMax)
	p.Update(func(sn *Settings) {
		sn.ReflectOpacity = v
	})
}

// weatherKindMax is render.WeatherCount-1, spelled out because config can't import render.
const (
	weatherKindMax        = 4 // None=0, Snow, Rain, Sakura, Embers
	defaultWeatherDensity = 60
)

// WeatherType reports the weather index, 0 = off, clamped against a hand-edited pref.
func (p *AssetPreferences) WeatherType() int {
	snap := p.Load()

	if snap.WeatherKind < 0 || snap.WeatherKind > weatherKindMax {
		return 0
	}
	return snap.WeatherKind
}

func (p *AssetPreferences) SetWeatherType(v int) {
	v = value.Clamp(v, 0, weatherKindMax)
	p.Update(func(sn *Settings) {
		sn.WeatherKind = v
	})
}

// stageFrameKindMax must match ui's stageFrameNames list.
const stageFrameKindMax = 7

// StageFrame reports the frame style, 0 = off, clamped against a hand-edited pref.
func (p *AssetPreferences) StageFrame() int {
	snap := p.Load()

	if snap.StageFrameKind < 0 || snap.StageFrameKind > stageFrameKindMax {
		return 0
	}
	return snap.StageFrameKind
}

// WeatherIntensity reports density [1,100]; 0 resolves to the default.
func (s *Settings) WeatherIntensity() int {
	return orDefault(s.WeatherDensity, defaultWeatherDensity)
}

func (p *AssetPreferences) SetWeatherIntensity(v int) {
	v = value.Clamp(v, 1, percentMax)
	p.Update(func(sn *Settings) {
		sn.WeatherDensity = v
	})
}

func (s *Settings) SpriteTintColorRGB() int {

	return s.SpriteTintColor & rgbMask
}

func (p *AssetPreferences) SetFriendSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.Update(func(sn *Settings) { sn.FriendSoundFile = path })
}

func (p *AssetPreferences) SetModBanSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.Update(func(sn *Settings) { sn.ModBanSoundFile = path })
}

func (p *AssetPreferences) SetModKickSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.Update(func(sn *Settings) { sn.ModKickSoundFile = path })
}

func (p *AssetPreferences) SetModMuteSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.Update(func(sn *Settings) { sn.ModMuteSoundFile = path })
}

func (p *AssetPreferences) SetCallwordSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.Update(func(sn *Settings) { sn.CallwordSoundFile = path })
}

// Theme reports the theme name ("" = default) and custom root ("" = none).
func (s *Settings) Theme() (name, dir string) {

	return s.ThemeName, s.ThemeDir
}

func (p *AssetPreferences) SetTheme(name, dir string) {
	p.Update(func(sn *Settings) {
		sn.ThemeName = name
		sn.ThemeDir = dir
	})
}

// SetSubtheme sanitizes as the value joins a path; "" clears, but an invalid name is refused, keeping a good pick.
func (p *AssetPreferences) SetSubtheme(sub string) {
	clean := theme.SanitizeSubtheme(sub)
	if clean == "" && strings.TrimSpace(sub) != "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SubthemeName = clean
	})
}

// Casing reports the CASEA subscription; the role bitmask layout is internal/courtroom's.
func (s *Settings) Casing() (enabled bool, roles int) {

	return s.CasingEnabled, s.CasingRoles
}

func (p *AssetPreferences) SetCasing(enabled bool, roles int) {
	p.Update(func(sn *Settings) {
		sn.CasingEnabled = enabled
		sn.CasingRoles = roles
	})
}

func (p *AssetPreferences) SetAutoStatus(a AutoStatusPref) {
	a = sanitizeAutoStatus(a)
	p.Update(func(sn *Settings) {
		sn.MyAutoStatus = a
	})
}

// ChromeTheme reports the chrome theme preset key, "dark" by default.
func (s *Settings) ChromeTheme() string {
	return orDefault(s.ChromeThemeKey, defaultChromeTheme)
}

// Chrome shape keys, the persisted vocabulary: sanitized here and trusted by the UI, whose shape* keys mirror them.
const (
	defaultChromeShape = "sharp"   // flat rectangle — the byte-identical default
	chromeShapeRounded = "rounded" // rounded-rectangle corners (radius by tier)
	chromeShapePill    = "pill"    // full-radius pill (corner = min(w,h)/2)
)

// shapeRadiusTiers bounds "rounded" radius classes and the UI's mask cache (hard rule 4); must equal the UI's.
const shapeRadiusTiers = 4

// sanitizeChromeShape collapses unknown keys to the default, so a corrupt pref can't leave chrome undrawable.
func sanitizeChromeShape(key string) string {
	switch key {
	case chromeShapeRounded, chromeShapePill, defaultChromeShape:
		return key
	default:
		return defaultChromeShape
	}
}

// clampChromeShapeTier snaps a stored tier to the nearest valid class.
func clampChromeShapeTier(tier int) int {
	if tier < 0 {
		return 0
	}
	if tier >= shapeRadiusTiers {
		return shapeRadiusTiers - 1
	}
	return tier
}

// ChromeShape always returns a sanitised, drawable key.
func (p *AssetPreferences) ChromeShape() string {
	snap := p.Load()

	return sanitizeChromeShape(snap.ChromeShapeKey)
}

// SetChromeShape stores an unknown key as "sharp".
func (p *AssetPreferences) SetChromeShape(key string) {
	key = sanitizeChromeShape(key)
	p.Update(func(sn *Settings) {
		sn.ChromeShapeKey = key
	})
}

// ChromeShapeTier reports the "rounded" radius class, always in range.
func (p *AssetPreferences) ChromeShapeTier() int {
	snap := p.Load()

	return clampChromeShapeTier(snap.ChromeShapeTierIdx)
}

func (p *AssetPreferences) SetChromeShapeTier(tier int) {
	tier = clampChromeShapeTier(tier)
	p.Update(func(sn *Settings) {
		sn.ChromeShapeTierIdx = tier
	})
}

const callWordCap = 32

func (s *Settings) CallWords() []string {

	return cloneStrings(s.CallWordList)
}

// SetCallWords replaces the list, lowercased and capped.
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
	p.Update(func(sn *Settings) {
		sn.CallWordList = clean
	})
}

// AddCallWord splits input on commas, lowercases and dedups up to the cap, and returns how many were added.
func (p *AssetPreferences) AddCallWord(input string) int {
	added := 0
	p.Update(func(sn *Settings) {
		for _, raw := range strings.Split(input, ",") {
			w := strings.ToLower(strings.TrimSpace(raw))
			if w == "" {
				continue
			}
			if sn.AddCallWord(w) {
				added++
			}
		}
	})
	return added
}

// RemoveCallWord drops a word, ignoring case; reports a change.
func (p *AssetPreferences) RemoveCallWord(word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveCallWord(word)
	})
	return changed
}

// Hotkey reports an action's key name, "" = unset; the UI owns defaults.
func (s *Settings) Hotkey(action string) string {

	return s.HotkeyMap[action]
}

func (p *AssetPreferences) SetHotkey(action, key string) {
	p.Update(func(sn *Settings) {
		sn.SetHotkey(action, key)
	})
}

func (s *Settings) ServerWarmInfoFor(key string) ServerWarmInfo {

	return snapCloneServerWarmInfo(s.ServerWarm[key])
}

func (p *AssetPreferences) RememberServerChar(key, char string) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Char = char })
}

// ServerFriends returns a clone because the settings editor mutates it.
func (s *Settings) ServerFriends(key string) []string {

	return cloneStrings(s.ServerWarm[key].Friends)
}

// friendParts splits "name[=RRGGBB[=nick]]"; SplitN with 3 survives an "=" in the nickname.
func friendParts(entry string) (name, hex, nick string) {
	const friendFields = 3 // name=hex=nick
	parts := strings.SplitN(entry, "=", friendFields)
	name = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		hex = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		nick = strings.TrimSpace(parts[2])
	}
	return
}

// ServerFriendInfo returns friend state, glow colour (-1 = default) and nick in one allocation-free scan.
func (s *Settings) ServerFriendInfo(key, name string) (friend bool, color int, nick string) {

	if name == "" {
		return false, -1, ""
	}

	for _, f := range s.ServerWarm[key].Friends {
		fname, hex, nk := friendParts(f)
		if !strings.EqualFold(fname, name) {
			continue
		}
		color = -1
		if hex != "" {
			if v, err := strconv.ParseInt(strings.TrimPrefix(hex, "#"), 16, 32); err == nil {
				color = int(v) & rgbMask
			}
		}
		return true, color, nk
	}
	return false, -1, ""
}

// ServerFriendMatch is ServerFriendInfo without the nickname, for per-message glow callers.
func (s *Settings) ServerFriendMatch(key, name string) (friend bool, color int) {
	f, c, _ := s.ServerFriendInfo(key, name)
	return f, c
}

// SetServerFriends canonicalises "name[=RRGGBB[=nick]]", dedups by name and caps.
// Commas are stripped from nicks because the editor stores the list comma-separated.
func (p *AssetPreferences) SetServerFriends(key string, names []string) {
	seen := make(map[string]struct{}, len(names))
	clean := make([]string, 0, len(names))
	for _, n := range names {
		nm, hex, nick := friendParts(n)
		if nm == "" {
			continue
		}
		k := strings.ToLower(nm)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		nick = strings.ReplaceAll(nick, ",", "") // can't survive the comma-separated store
		entry := nm
		if hex != "" || nick != "" {
			entry += "=" + hex
		}
		if nick != "" {
			entry += "=" + nick
		}
		clean = append(clean, entry)
		if len(clean) >= WarmFriendsCap {
			break
		}
	}
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Friends = clean })
}

// ServerIgnored returns a clone because the settings editor mutates it.
func (s *Settings) ServerIgnored(key string) []string {

	return cloneStrings(s.ServerWarm[key].Ignored)
}

// ServerIgnoreMatch scans without allocating, safe per incoming message.
func (s *Settings) ServerIgnoreMatch(key, name string) bool {

	if name == "" {
		return false
	}

	for _, n := range s.ServerWarm[key].Ignored {
		if strings.EqualFold(strings.TrimSpace(n), name) {
			return true
		}
	}
	return false
}

// SetServerIgnored replaces the list, trimmed, fold-deduped and capped.
func (p *AssetPreferences) SetServerIgnored(key string, names []string) {
	seen := make(map[string]struct{}, len(names))
	clean := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		k := strings.ToLower(n)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		clean = append(clean, n)
		if len(clean) >= WarmIgnoredCap {
			break
		}
	}
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Ignored = clean })
}

func (p *AssetPreferences) RememberServerBackground(key, bg string) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Background = bg })
}

func (p *AssetPreferences) RememberServerOrigin(key, origin string) {
	if origin == "" {
		return
	}
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Origin = origin })
}

// SetServerLogin stores credentials, in plaintext, and the auto-login choice; empty user and pass clear them.
func (p *AssetPreferences) SetServerLogin(key, user, pass string, auto bool) {
	p.rememberServer(key, func(w *ServerWarmInfo) {
		w.LoginUser, w.LoginPass = user, pass
		w.AutoLogin = auto && user != ""
	})
}

// SetServerTheme binds a theme to one server, "" unbinds.
func (p *AssetPreferences) SetServerTheme(key, themeName string) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Theme = themeName })
}

// ServerAudio returns a server's override; on=false means the global volumes apply; master scales the rest.
func (p *AssetPreferences) ServerAudio(key string) (on bool, master, music, sfx, blip int) {
	snap := p.Load()

	w := snap.ServerWarm[key]
	// An unset channel uses the global default, so it's never silently 0.
	return w.AudioOn,
		ptrOr(w.AudioMaster, snap.MasterVol),
		ptrOr(w.AudioMusic, snap.MusicVol),
		ptrOr(w.AudioSFX, snap.SFXVol),
		ptrOr(w.AudioBlip, snap.BlipVol)
}

// ptrOr returns *v when set, else def.
func ptrOr(v *int, def int) int {
	if v != nil {
		return *v
	}
	return def
}

func (p *AssetPreferences) SetServerAudioOn(key string, on bool) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.AudioOn = on })
}

// SetServerAudioVolumes writes all channels explicitly, so none silently defaults to a muted 0.
func (p *AssetPreferences) SetServerAudioVolumes(key string, master, music, sfx, blip int) {
	m := value.Clamp(master, 0, defaultAudioVolume)
	mu := value.Clamp(music, 0, defaultAudioVolume)
	s := value.Clamp(sfx, 0, defaultAudioVolume)
	b := value.Clamp(blip, 0, defaultAudioVolume)
	p.rememberServer(key, func(w *ServerWarmInfo) {
		w.AudioOn = true // adjusting volumes makes this server's own profile "exist"
		w.AudioMaster, w.AudioMusic, w.AudioSFX, w.AudioBlip = &m, &mu, &s, &b
	})
}

// RememberServerChars records the list for rehearsal mode, capped.
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

// RememberServerBackgrounds caches the list so the next session's picker seeds instantly, capped.
func (p *AssetPreferences) RememberServerBackgrounds(key string, bgs []string) {
	if len(bgs) == 0 {
		return
	}
	if len(bgs) > WarmBgsCap {
		bgs = bgs[:WarmBgsCap]
	}
	copied := cloneStrings(bgs)
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Backgrounds = copied })
}

func (p *AssetPreferences) rememberServer(key string, set func(*ServerWarmInfo)) {
	if key == "" {
		return
	}
	p.Update(func(sn *Settings) {
		if sn.ServerWarm == nil {
			sn.ServerWarm = map[string]ServerWarmInfo{}
		}
		w, exists := sn.ServerWarm[key]
		if !exists && len(sn.ServerWarm) >= serverWarmCap {
			return // table full; never unbounded
		}
		set(&w)
		sn.ServerWarm[key] = w
	})
}

func (s *Settings) HiddenPanels() []string {

	out := make([]string, len(s.HiddenPanelIDs))
	copy(out, s.HiddenPanelIDs)
	return out
}

// SetHiddenPanels replaces the set, deduped and capped.
func (p *AssetPreferences) SetHiddenPanels(ids []string) {
	clean := sanitizeHiddenPanels(ids)
	p.Update(func(sn *Settings) {
		sn.HiddenPanelIDs = clean
	})
}

// LayoutScales reports viewport width, chat text, chat box, log text and input height percents.
func (s *Settings) LayoutScales() (viewport, chatText, chatBox, logText, input int) {

	return s.ViewportPct, s.ChatScalePct, s.ChatBoxPct, s.LogScalePct, s.InputHeightPct
}

func (p *AssetPreferences) SetLayoutScales(viewport, chatText, chatBox, logText, input int) {
	viewport = value.Clamp(viewport, MinViewportPercent, MaxViewportPercent)
	chatText = value.Clamp(chatText, MinChatScalePercent, MaxChatScalePercent)
	chatBox = value.Clamp(chatBox, MinChatBoxPercent, MaxChatBoxPercent)
	logText = value.Clamp(logText, MinLogScalePercent, MaxLogScalePercent)
	input = value.Clamp(input, MinInputPercent, MaxInputPercent)
	p.Update(func(sn *Settings) {
		sn.ViewportPct = viewport
		sn.ChatScalePct = chatText
		sn.ChatBoxPct = chatBox
		sn.LogScalePct = logText
		sn.InputHeightPct = input
	})
}

// LayoutPartColorCount is the tintable part count; ui owns index meanings and blank means the chrome default.
const LayoutPartColorCount = 4

// SetLayoutPartColor stores a part's hex, "" clears; out-of-range indices are ignored.
func (p *AssetPreferences) SetLayoutPartColor(i int, hex string) {
	if i < 0 || i >= LayoutPartColorCount {
		return
	}
	p.Update(func(sn *Settings) {
		sn.LayoutPartHex[i] = hex
	})
}

// BoldNamesOn is stored inverted so an absent value means bold.
func (s *Settings) BoldNamesOn() bool {

	return !s.BoldNamesOff
}

func (p *AssetPreferences) SetBoldNames(on bool) {
	p.Update(func(sn *Settings) {
		sn.BoldNamesOff = !on
	})
}

// SetBreathBob assigns the inverse, as the field is the inverted BreathNoBob.
func (p *AssetPreferences) SetBreathBob(on bool) {
	p.Update(func(sn *Settings) { sn.BreathNoBob = !on })
}

// SetBreathScale assigns the inverse of BreathNoScale.
func (p *AssetPreferences) SetBreathScale(on bool) {
	p.Update(func(sn *Settings) { sn.BreathNoScale = !on })
}

// These setters stay explicit: zero resolves to a default or off, which normalize can't do through one field.

// SetBreathAmp clamps the idle-breath amplitude slider to [1,100] (0 -> 1).
func (p *AssetPreferences) SetBreathAmp(v int) {
	v = value.Clamp(v, 1, percentMax)
	p.Update(func(sn *Settings) {
		sn.BreathAmount = v
	})
}

// SetBreathSpeed clamps the idle-breath speed slider to [1,100] (0 -> 1).
func (p *AssetPreferences) SetBreathSpeed(v int) {
	v = value.Clamp(v, 1, percentMax)
	p.Update(func(sn *Settings) {
		sn.BreathRate = v
	})
}

// SetHoldClearMs clamps the hold-to-clear hold time (0 -> MinHoldClearMs).
func (p *AssetPreferences) SetHoldClearMs(ms int) {
	ms = value.Clamp(ms, MinHoldClearMs, MaxHoldClearMs)
	p.Update(func(sn *Settings) {
		sn.HoldClearMs = ms
	})
}

// SetInputGraceFrames maps non-positive n to InputGraceOff and clamps positive n.
func (p *AssetPreferences) SetInputGraceFrames(n int) {
	if n <= 0 {
		n = InputGraceOff
	}
	p.Update(func(sn *Settings) {
		sn.InputGraceFramesVal = n
	})
}

// SetInstantReplaySeconds clamps the replay window, resolving 0 to the default.
func (p *AssetPreferences) SetInstantReplaySeconds(s int) {
	s = clampInstantReplaySeconds(s)
	p.Update(func(sn *Settings) {
		sn.InstantReplaySeconds = s
	})
}

// SetAnimatedSpriteCap disables the cap (0) below the floor, rather than forcing the floor.
func (p *AssetPreferences) SetAnimatedSpriteCap(px int) {
	if px != 0 {
		if px < AnimatedSpriteCapMinPx {
			px = 0
		} else {
			px = value.Clamp(px, AnimatedSpriteCapMinPx, AnimatedSpriteCapMaxPx)
		}
	}
	p.Update(func(sn *Settings) {
		sn.AnimatedSpriteCapPxVal = px
	})
}

func (p *AssetPreferences) SetUIScale(pct int) {
	pct = value.Clamp(pct, MinUIScalePercent, MaxUIScalePercent)
	p.Update(func(sn *Settings) {
		sn.UIScalePct = pct
	})
}

// WindowSize reports the saved size, 0,0 = default; apply through ClampWindowSize, as a saved value may be stale.
func (s *Settings) WindowSize() (int, int) {

	return s.WindowW, s.WindowH
}

// SetWindowSize floors at the minimum; the display ceiling is applied where used, since prefs are SDL-free.
func (p *AssetPreferences) SetWindowSize(w, h int) {
	if w < MinWindowW {
		w = MinWindowW
	}
	if h < MinWindowH {
		h = MinWindowH
	}
	p.Update(func(sn *Settings) {
		sn.WindowW = w
		sn.WindowH = h
	})
}

// AudioVolumes reports the volumes, 0-100.
func (s *Settings) AudioVolumes() (music, sfx, blip int) {

	return s.MusicVol, s.SFXVol, s.BlipVol
}

func (p *AssetPreferences) SetAudioVolumes(music, sfx, blip int) {
	p.Update(func(sn *Settings) {
		sn.MusicVol = music
		sn.SFXVol = sfx
		sn.BlipVol = blip
	})
}

// Master, music and blip mutes persist; the SFX mute is session-only (ui.toggleSFXMute).
// An accidental press must not survive a restart; its field stays for schema compatibility only.

// HoldClear reports on, the SDL key name and the hold in ms.
func (s *Settings) HoldClear() (on bool, key string, ms int) {

	return s.HoldClearOn, s.HoldClearKey, s.HoldClearMs
}

// SetHoldClearKey rebinds the key; "" is ignored.
func (p *AssetPreferences) SetHoldClearKey(key string) {
	if key == "" {
		return
	}
	if p.Load().HoldClearKey == key {
		return
	}
	p.Update(func(sn *Settings) { sn.HoldClearKey = key })
}

// ExtrasBoxStyle reports the colours (hex, "" = stock) and whether the background is a gradient.
func (s *Settings) ExtrasBoxStyle() (bg, bg2, border, title, text string, gradient bool) {

	return s.ExtrasBg, s.ExtrasBg2, s.ExtrasBorder, s.ExtrasTitle, s.ExtrasText, s.ExtrasGradient
}

func (p *AssetPreferences) SetExtrasBoxStyle(bg, bg2, border, title, text string, gradient bool) {
	p.Update(func(sn *Settings) {
		sn.ExtrasBg = bg
		sn.ExtrasBg2 = bg2
		sn.ExtrasBorder = border
		sn.ExtrasTitle = title
		sn.ExtrasText = text
		sn.ExtrasGradient = gradient
	})
}

// Timing reports crawl per character, post-message stay and minimum chat gap (0 = none), in ms.
func (s *Settings) Timing() (crawlMs, stayMs, rateLimitMs int) {

	return s.TextCrawlMs, s.TextStayMs, s.ChatRateLimitMs
}

func (p *AssetPreferences) SetTiming(crawlMs, stayMs, rateLimitMs int) {
	crawlMs = value.Clamp(crawlMs, MinTextCrawlMs, MaxTextCrawlMs)
	rateLimitMs = value.Clamp(rateLimitMs, 0, MaxChatRateLimitMs)
	p.Update(func(sn *Settings) {
		sn.TextCrawlMs = crawlMs
		sn.TextStayMs = stayMs
		sn.ChatRateLimitMs = rateLimitMs
	})
}

// CatchUp reports the toggle and the queue depth at which the IC stage fast-forwards.
func (s *Settings) CatchUp() (on bool, threshold int) {

	return s.CatchUpOn, s.CatchUpThreshold
}

func (p *AssetPreferences) SetCatchUp(on bool, threshold int) {
	p.Update(func(sn *Settings) {
		sn.CatchUpOn = on
		sn.CatchUpThreshold = threshold
	})
}

// TabCap is the clamped concurrent tab limit; unset falls back to the default.
func (s *Settings) TabCap() int {
	return value.Clamp(orDefault(s.MultiTabCap, DefaultMultiTabCap), minMultiTabCap, maxMultiTabCap)
}

// Cold-load sprite modes: values must equal render.SpriteLoad*; plain ints to avoid a config to render import.
const (
	SpriteLoadBlank     = 0 // Show nothing — draw nothing until the sprite lands (the original behaviour; the cold-load flash)
	SpriteLoadHoldPrev  = 1 // webAO — keep the previous sprite until the new one lands (webAO-style; the old default)
	SpriteLoadWait      = 2 // Hold the message — hold the MESSAGE off-stage until its speaker sprite decodes (client-AO-style; courtroom wait gate, timeout-capped)
	SpriteLoadDefault   = 3 // Default — strict sequencing: wait for EVERY asset a message presents before it plays (the courtroom SequentialWait gate)
	SpriteLoadModeCount = 4 // number of valid modes (for the Settings mode picker + load clamp)

	// defaultSpriteLoadMode waits for every asset before a message plays, so nothing pops in mid-ceremony.
	defaultSpriteLoadMode = SpriteLoadDefault
)

// SpriteLoadWait timeout; the 0.5 s default keeps a busy room from stalling on a first-load sprite.
const (
	SpriteWaitDefaultMs = 500
	SpriteWaitMinMs     = 50
	SpriteWaitMaxMs     = 30000
)

// SetSpriteLoadMode clamps to a known mode.
func (p *AssetPreferences) SetSpriteLoadMode(mode int) {
	if mode < SpriteLoadBlank || mode >= SpriteLoadModeCount {
		mode = SpriteLoadBlank
	}
	p.Update(func(sn *Settings) {
		sn.SpriteLoadModeVal = mode
	})
}

// SpriteWaitMs reports how long SpriteLoadWait may hold a message, in ms.
func (s *Settings) SpriteWaitMs() int {
	return orDefault(s.SpriteWaitMsVal, SpriteWaitDefaultMs)
}

func (p *AssetPreferences) SetSpriteWaitMs(ms int) {
	ms = value.Clamp(ms, SpriteWaitMinMs, SpriteWaitMaxMs)
	p.Update(func(sn *Settings) {
		sn.SpriteWaitMsVal = ms
	})
}

// Renderer and core-timing bounds; "0 = default" keeps the pref file byte-identical for untouched knobs.
const (
	HoldPrevMaxAgeMinMs = 100   // shortest useful stand-in bridge
	HoldPrevMaxAgeMaxMs = 30000 // past this, give up to blank (0 = bridge forever)
	ShoutDurationMinMs  = 100   // a blink of a bubble
	ShoutDurationMaxMs  = 3000  // a leisurely bubble
	PreanimTimeoutMinMs = 200   // barely wait for a preanim
	PreanimTimeoutMaxMs = 10000 // very patient preanim cap
	ICQueueCapMin       = 8     // shallowest useful backlog (still bounded — courtroom floors ≥ 1 regardless)
	ICQueueCapMax       = 256   // deepest backlog before "just read the log"
	CatchUpLingerMaxMs  = 1000  // longest per-message flash while catching up (default 0 = one per frame)

	// IC scrollback depth: 0 = the client's icLogCap (1024), ICLogDepthUnlimited = never cap.
	ICLogDepthUnlimited = -1    // "don't cap at all" — the log grows for the whole session
	ICLogDepthMin       = 1     // shallowest log worth keeping
	ICLogDepthMax       = 65536 // deepest a typed number may set (still bounded; ∞ is the true uncap)

	// Thumbnail knobs: about 1 KB per sprite, low quality by design so a stand-in is never mistaken for real art.
	ThumbHeightDefaultPx = 64  // matches assets.ThumbHeightDefault (pinned by test)
	ThumbHeightMinPx     = 32  // barely a silhouette
	ThumbHeightMaxPx     = 160 // "low-q" stops meaning anything past this
	ThumbQualityDefault  = 20  // matches assets.ThumbQualityDefault (pinned by test)
	ThumbQualityMin      = 5   // maximum crunch
	ThumbQualityMax      = 60  // still clearly a placeholder

	// Thumb-store budget; prunes oldest past it, and 64 MiB is about 60k thumbnails.
	ThumbBudgetDefaultMiB = 64
	ThumbBudgetMinMiB     = 8
	ThumbBudgetMaxMiB     = 512

	// T3 auto-prune cap: 0 = unlimited, the default, so an update never silently deletes a user's cache.
	// A positive cap makes the writer sweep oldest blobs; one character can run tens of MiB.
	DiskCacheBudgetMinMiB = 128   // smallest meaningful cap
	DiskCacheBudgetMaxMiB = 65536 // 64 GiB — a very large asset library

	// 404 TTL before a re-probe; applied on restart because the LRU takes its TTL at construction.
	NotFoundTTLMinSec = 30
	NotFoundTTLMaxSec = 3600

	// Adaptive deadline = N x the host's TTFB EWMA; lower sheds a bad mirror sooner but risks spurious timeouts.
	AdaptiveLatMultipleMin = 2
	AdaptiveLatMultipleMax = 32

	// Decode-downscale override: scales the display-height target, 100 = default; SpriteDownscaleOff disables the cap.
	SpriteDownscaleMinPct = 50
	SpriteDownscaleMaxPct = 200

	// Animated-only height cap in px: 0 = off (use the still cap), and below the floor snaps to off.
	AnimatedSpriteCapMinPx = 240
	AnimatedSpriteCapMaxPx = 2160

	// T1 texture budget in MiB, applied on restart; above TexBudgetSafeMaxMiB is an experimental, warned opt-in.
	// It lets long animations decode past the ~5 s truncation at the cost of peak memory.
	TexBudgetDefaultMiB = 64
	TexBudgetMinMiB     = 32
	TexBudgetMaxMiB     = 256
	// TexBudgetSafeMaxMiB is the largest budget Settings doesn't mark experimental.
	TexBudgetSafeMaxMiB = 128

	// Per-animated-asset decode budget in MiB, read atomically so new decodes pick it up without a restart.
	AnimatedBudgetDefaultMiB = 128
	AnimatedBudgetMinMiB     = 64
	AnimatedBudgetMaxMiB     = 512

	// Concurrent animated decodes: a clip's decoded RGBA is the RAM spike, so this limits the burst.
	AnimDecodeConcurrencyDefault = 2
	AnimDecodeConcurrencyMin     = 1
	AnimDecodeConcurrencyMax     = 8

	// Texture compression, restart-applied and lossy; only for canvases sized in multiples of 4 and supported formats.
	TexCompressOff  = 1
	TexCompressDXT5 = 2
	TexCompressDXT1 = 3

	// Speaker-swap crossfade in ms: 0 = hard swap; suppressed by Reduce motion.
	CrossfadeMinMs = 50
	CrossfadeMaxMs = 1000

	// Frame pacing: full-rate rendering burns GPU while idle, so three sleep-based caps apply; vsync stays on.
	FPSCapDefault = FPSUnlimited // Active default = ∞ / vsync-paced (the playtesters' hard default; the ceiling only bites if you type a number or drag off ∞)
	FPSCapMin     = 1
	FPSCapMax     = 240
	// FPSCapUnlimitedOff is what the "unlimited" toggle drops to; the default is unlimited, so it can't be that.
	FPSCapUnlimitedOff  = 60
	IdleFPSDefault      = FPSOff // idle rendering OFF by default: a static screen redraws zero times (0 GPU) and leans on real events + scheduled deadlines
	IdleFPSMin          = 1
	IdleFPSMax          = 120
	UnfocusedFPSDefault = 5 // low unfocused ceiling: a tabbed-out window renders at most this (the background cap is a hard ceiling now)
	UnfocusedFPSMin     = 1
	UnfocusedFPSMax     = 60

	// InputGraceFrames: full-rate hold after a click or keypress, in frames at 60 fps; motion has its own grace.
	InputGraceFramesDefault = 1
	InputGraceFramesMin     = 1
	InputGraceFramesMax     = 60
	// InputGraceOff is the stored 0-frame hold, distinct from 0, which means absent and so the default.
	InputGraceOff = -1

	// FPSUnlimited is the no-limit sentinel for the three rate knobs: vsync paces; distinct from 0, the default.
	FPSUnlimited = -1

	// FPSOff is the never-redraw sentinel for Idle and Background: only damage and scheduled deadlines wake the loop.
	FPSOff = -2
)

// normalizeFPSPref: 0, FPSUnlimited and FPSOff pass through, other negatives become unlimited, the rest clamp.
func normalizeFPSPref(fps, min, max int) int {
	switch fps {
	case 0, FPSUnlimited, FPSOff:
		return fps
	}
	if fps < 0 {
		return FPSUnlimited
	}
	return value.Clamp(fps, min, max)
}

func (s *Settings) ThumbHeightPx() int {
	return orDefault(s.ThumbHeightPxVal, ThumbHeightDefaultPx)
}

func (s *Settings) ThumbQuality() int {
	return orDefault(s.ThumbQualityVal, ThumbQualityDefault)
}

// ThumbBudgetMiB reports the thumbnail store budget; the store prunes oldest past it.
func (s *Settings) ThumbBudgetMiB() int {
	return orDefault(s.ThumbBudgetMiBVal, ThumbBudgetDefaultMiB)
}

// TexBudgetMiB reports the T1 budget in MiB; applies on restart.
func (s *Settings) TexBudgetMiB() int {
	return orDefault(s.TexBudgetMiBVal, TexBudgetDefaultMiB)
}

// AnimatedBudgetMiB reports the per-asset decode budget; new decodes pick it up live.
func (s *Settings) AnimatedBudgetMiB() int {
	return orDefault(s.AnimatedBudgetMiBVal, AnimatedBudgetDefaultMiB)
}

// AnimDecodeConcurrency reports the concurrent decode bound; applied live.
func (s *Settings) AnimDecodeConcurrency() int {
	return orDefault(s.AnimDecodeConcurrencyVal, AnimDecodeConcurrencyDefault)
}

// TexCompression reports the mode; applies on restart.
func (p *AssetPreferences) TexCompression() int {
	snap := p.Load()

	if snap.TexCompressionVal < TexCompressOff || snap.TexCompressionVal > TexCompressDXT1 {
		return TexCompressDXT5 // 0/absent -> the shipped default
	}
	return snap.TexCompressionVal
}

func (p *AssetPreferences) SetTexCompression(mode int) {
	switch mode {
	case TexCompressOff, TexCompressDXT5, TexCompressDXT1:
	default:
		mode = TexCompressDXT5
	}
	p.Update(func(sn *Settings) {
		sn.TexCompressionVal = mode
	})
}

// Unknown modes get the shipped default.

// FPSCap, IdleFPS and UnfocusedFPS report the foreground ceiling, idle and other-window-focused rates.
func (s *Settings) FPSCap() int {
	return orDefault(s.FPSCapVal, FPSCapDefault)
}

func (s *Settings) IdleFPS() int {
	return orDefault(s.IdleFPSVal, IdleFPSDefault)
}

func (s *Settings) UnfocusedFPS() int {
	return orDefault(s.UnfocusedFPSVal, UnfocusedFPSDefault)
}

func (s *Settings) InputGraceFrames() int {
	if s.InputGraceFramesVal < 0 {
		return 0 // InputGraceOff: no hold
	}
	return orDefault(s.InputGraceFramesVal, InputGraceFramesDefault)
}

// EffectiveSpriteCap is shared by boot and live Settings so they can't disagree; off gives 0, else base x pct/100.
func EffectiveSpriteCap(base int, off bool, pct int) int {
	if off || base <= 0 {
		return 0
	}
	if pct == 0 {
		pct = percentMax
	}
	return base * pct / percentMax
}

// ResetPowerUser reverts the Power user tab; probe orders and learned formats have their own controls.
func (p *AssetPreferences) ResetPowerUser() {
	p.Update(func(s *Settings) {
		s.ValidateTLSCerts = false
		s.AssetOrigin = ""
		s.WSOrigin = ""
		s.UpdateExperimental = false // back to the stable update channel
		s.AssetCharCase = 0
		s.ProxyModeVal = ProxyModeSystem // back to "use the machine's setting"
		s.ProxyURL = ""
		s.SpriteLoadModeVal = defaultSpriteLoadMode
		s.SpriteWaitMsVal = 0
		s.SpriteWaitPair = false
		s.SpriteWaitPreanim = false
		s.ShoutBypassQueue = defaultShoutBypassQueue
		s.HoldPrevMaxAgeMsVal = 0
		s.HoldDebugTint = false
		s.HotkeysEnabled = false
		s.ShoutDurationMsVal = 0
		s.PreanimTimeoutMsVal = 0
		s.ICQueueCapVal = 0
		s.CatchUpLingerMsVal = 0
		s.ThumbCache = false
		s.ThumbHeightPxVal = 0
		s.ThumbQualityVal = 0
		s.ThumbBudgetMiBVal = 0
		s.DiskCacheBudgetMiBVal = 0 // back to unlimited (the default)
		s.NotFoundTTLSecVal = 0
		s.AdaptiveLatMultipleVal = 0
		s.SpriteDownscaleOff = false
		s.SpriteDownscalePctVal = 0
		s.AnimatedSpriteCapPxVal = 0
		s.TexBudgetMiBVal = 0
		s.AnimatedBudgetMiBVal = 0
		s.AnimDecodeConcurrencyVal = 0
		s.TexCompressionVal = 0

		s.CrossfadeMsVal = 0
		s.FPSCapVal = 0
		s.IdleFPSVal = 0
		s.UnfocusedFPSVal = 0
		s.InputGraceFramesVal = 0
		s.EventDrivenLoop = defaultEventDrivenLoop
		s.DisableFrameLimiter = false                        // #5 bypass resets OFF
		s.MotionRedrawPerEvent = defaultMotionRedrawPerEvent // per-event motion redraw resets to its default (ON)
		s.ClipSpritesToStage = defaultClipSpritesToStage
	})
}

// AO2 MUSIC_EFFECT bits (AO2-Client src/datatypes.h:95-101), the last field of an outgoing MC under `effects`.
// Plain ints because config must not depend on its consumers.
const (
	MusicFlagFadeIn   = 1 // ramp the NEW track up
	MusicFlagFadeOut  = 2 // ramp the PREVIOUS track down
	MusicFlagSyncPos  = 4 // start the new track at the previous track's position
	MusicFlagNoRepeat = 8 // play once regardless of the looping field (KFO's "Loop" off)
	// MusicFlagsMask drops unknown bits on load so a hand-edited file can't put junk on the wire.
	MusicFlagsMask = MusicFlagFadeIn | MusicFlagFadeOut | MusicFlagSyncPos | MusicFlagNoRepeat
	// DefaultMusicFlags matches AO2-Client's `int music_flags = MUSIC_EFFECT::FADE_OUT;` (src/courtroom.h:551).
	DefaultMusicFlags = MusicFlagFadeOut
)

func (s *Settings) MusicFlags() int {

	return s.MusicFlagsVal & MusicFlagsMask
}

// OpenTabList returns a copy of the remembered tabs.
func (s *Settings) OpenTabList() []OpenTab {

	if len(s.OpenTabs) == 0 {
		return nil
	}
	out := make([]OpenTab, len(s.OpenTabs))
	copy(out, s.OpenTabs)
	return out
}

// SetOpenTabs replaces the remembered tabs, copied and capped.
func (p *AssetPreferences) SetOpenTabs(tabs []OpenTab) {
	if len(tabs) > maxMultiTabCap {
		tabs = tabs[:maxMultiTabCap]
	}
	cp := make([]OpenTab, len(tabs))
	copy(cp, tabs)
	p.Update(func(sn *Settings) {
		sn.OpenTabs = cp
	})
}

// AO2 sticky options and sfx_on_idle; getters read "does the pick survive the send", the config key's sense.

func (p *AssetPreferences) SetMasterList(url string) {
	url = strings.TrimSpace(url)
	if p.Load().MasterListURL == url {
		return
	}
	p.Update(func(sn *Settings) { sn.MasterListURL = url })
}

func (s *Settings) WardrobeList(serverKey string) []string {

	return cloneStrings(s.ServerWarm[serverKey].Wardrobe)
}

// ClaimLegacyWardrobe gives the legacy wardrobe to the first server connected to, then clears it.
func (p *AssetPreferences) ClaimLegacyWardrobe(serverKey string) {
	if serverKey == "" {
		return
	}
	changed := false
	p.Update(func(sn *Settings) {
		if len(sn.Wardrobe) == 0 {
			return
		}
		if sn.ServerWarm == nil {
			sn.ServerWarm = map[string]ServerWarmInfo{}
		}
		w, exists := sn.ServerWarm[serverKey]
		if !exists && len(sn.ServerWarm) >= serverWarmCap {
			return
		}
		if len(w.Wardrobe) == 0 {
			w.Wardrobe = sn.Wardrobe
			sn.Wardrobe = nil
			changed = true
		}
		sn.ServerWarm[serverKey] = w
	})
	if changed {
		p.wardrobeGen.Add(1)
	}
}

// AddWardrobe stores a character folder, fold-deduped and capped; reports a change.
func (p *AssetPreferences) AddWardrobe(serverKey, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || serverKey == "" {
		return false
	}
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		w.Wardrobe, changed = addFoldUnique(w.Wardrobe, name, WardrobeCap)
	})
	if changed {
		p.wardrobeGen.Add(1)
	}
	return changed
}

func (p *AssetPreferences) RemoveWardrobe(serverKey, name string) bool {
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		w.Wardrobe, changed = removeFold(w.Wardrobe, name)
		if changed {
			delete(w.WardrobeFolder, strings.ToLower(name)) // drop its folder too
		}
	})
	if changed {
		p.wardrobeGen.Add(1)
	}
	return changed
}

// WardrobeGeneration bumps on any membership change; char select keys its star cache by it.
func (p *AssetPreferences) WardrobeGeneration() uint64 {
	return p.wardrobeGen.Load()
}

// WardrobeFolderMap returns a copy of the lowercased char to folder map, nil when empty.
func (s *Settings) WardrobeFolderMap(serverKey string) map[string]string {

	src := s.ServerWarm[serverKey].WardrobeFolder
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// fileFolder files a key under a folder map, or clears on empty; both folder kinds share it so a fix can't miss one.
func (p *AssetPreferences) fileFolder(serverKey, key, folder string, cap int,
	get func(*ServerWarmInfo) map[string]string, set func(*ServerWarmInfo, map[string]string)) {
	key = strings.ToLower(strings.TrimSpace(key))
	folder = strings.TrimSpace(folder)
	if key == "" || serverKey == "" {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		m := get(w)
		if folder == "" {
			delete(m, key) // deleting from a nil map is a no-op
			return
		}
		if m == nil {
			m = map[string]string{}
			set(w, m)
		}
		if _, exists := m[key]; !exists && len(m) >= cap {
			return
		}
		m[key] = folder
	})
}

// deleteFolder ungroups a folder or, when keepMembers is false, also drops its members.
func (p *AssetPreferences) deleteFolder(serverKey, folder string, keepMembers bool,
	get func(*ServerWarmInfo) map[string]string, list func(*ServerWarmInfo) []string, setList func(*ServerWarmInfo, []string)) {
	folder = strings.TrimSpace(folder)
	if folder == "" || serverKey == "" {
		return
	}
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		for lc, f := range get(w) {
			if !strings.EqualFold(f, folder) {
				continue
			}
			delete(get(w), lc)
			if keepMembers {
				continue
			}
			l := list(w)
			for i, have := range l {
				if strings.ToLower(have) == lc {
					setList(w, append(l[:i], l[i+1:]...))
					break
				}
			}
		}
	})
}

// SetWardrobeFolder files a character under a folder; empty clears, capped.
func (p *AssetPreferences) SetWardrobeFolder(serverKey, char, folder string) {
	p.fileFolder(serverKey, char, folder, WardrobeCap,
		func(w *ServerWarmInfo) map[string]string { return w.WardrobeFolder },
		func(w *ServerWarmInfo, m map[string]string) { w.WardrobeFolder = m })
}

// DeleteWardrobeFolder ungroups a folder or, when keepMembers is false, removes its characters from the wardrobe.
func (p *AssetPreferences) DeleteWardrobeFolder(serverKey, folder string, keepMembers bool) {
	p.deleteFolder(serverKey, folder, keepMembers,
		func(w *ServerWarmInfo) map[string]string { return w.WardrobeFolder },
		func(w *ServerWarmInfo) []string { return w.Wardrobe },
		func(w *ServerWarmInfo, l []string) { w.Wardrobe = l })
}

func (s *Settings) FavBackgroundList(serverKey string) []string {

	return cloneStrings(s.ServerWarm[serverKey].FavBackgrounds)
}

// AddFavBackground stars a background, fold-deduped and capped; reports a change.
func (p *AssetPreferences) AddFavBackground(serverKey, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || serverKey == "" {
		return false
	}
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		w.FavBackgrounds, changed = addFoldUnique(w.FavBackgrounds, name, FavBackgroundCap)
	})
	return changed
}

// RemoveFavBackground unstars a background and drops its folder entry; reports a change.
func (p *AssetPreferences) RemoveFavBackground(serverKey, name string) bool {
	changed := false
	p.rememberServer(serverKey, func(w *ServerWarmInfo) {
		w.FavBackgrounds, changed = removeFold(w.FavBackgrounds, name)
		if changed {
			delete(w.FavBackgroundFolder, strings.ToLower(name)) // leaving favourites drops the folder too
		}
	})
	return changed
}

// FavBackgroundFolderMap returns a copy of the lowercased background to folder map, nil when empty.
func (s *Settings) FavBackgroundFolderMap(serverKey string) map[string]string {

	src := s.ServerWarm[serverKey].FavBackgroundFolder
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetFavBackgroundFolder files a background under a folder; empty clears, capped.
func (p *AssetPreferences) SetFavBackgroundFolder(serverKey, bg, folder string) {
	p.fileFolder(serverKey, bg, folder, FavBackgroundCap,
		func(w *ServerWarmInfo) map[string]string { return w.FavBackgroundFolder },
		func(w *ServerWarmInfo, m map[string]string) { w.FavBackgroundFolder = m })
}

// DeleteFavBackgroundFolder ungroups a folder or, when keepMembers is false, unstars its backgrounds.
func (p *AssetPreferences) DeleteFavBackgroundFolder(serverKey, folder string, keepMembers bool) {
	p.deleteFolder(serverKey, folder, keepMembers,
		func(w *ServerWarmInfo) map[string]string { return w.FavBackgroundFolder },
		func(w *ServerWarmInfo) []string { return w.FavBackgrounds },
		func(w *ServerWarmInfo, l []string) { w.FavBackgrounds = l })
}

func (s *Settings) CharKeyBinds(serverKey string) map[string]string {

	src := s.ServerWarm[serverKey].CharKeys
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetCharKeyBind binds a lowercase SDL key name to a character; empty clears, capped.
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

// ExportSettings flushes and copies the preferences file to destPath, with saved passwords stripped.
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

// Presets are settings exports under presets/<name>.json, and applying one is an import.

const (
	presetsDirName = "presets"

	presetsCap = 16
	// presetNameMaxLen keeps names filesystem- and UI-friendly.
	presetNameMaxLen = 32
)

func (p *AssetPreferences) PresetsDir() string {
	return filepath.Join(filepath.Dir(p.path), presetsDirName)
}

// sanitizePresetName reduces a name to letters, digits, space, dash and underscore, length-capped; empty means invalid.
func sanitizePresetName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	s := strings.TrimSpace(b.String())
	if len(s) > presetNameMaxLen {
		s = strings.TrimSpace(s[:presetNameMaxLen])
	}
	return s
}

// SavePreset snapshots settings without passwords, overwriting by name; refuses past the cap or an invalid name.
func (p *AssetPreferences) SavePreset(name string) error {
	stem := sanitizePresetName(name)
	if stem == "" {
		return fmt.Errorf("config: preset name is empty after removing special characters")
	}
	existing := p.ListPresets()
	replacing := false
	for _, e := range existing {
		if strings.EqualFold(e, stem) {
			replacing = true
			break
		}
	}
	if !replacing && len(existing) >= presetsCap {
		return fmt.Errorf("config: preset cap reached (%d) — delete one first", presetsCap)
	}
	return p.ExportSettings(filepath.Join(p.PresetsDir(), stem+".json"))
}

// ListPresets returns sorted stems; a missing directory means none.
func (p *AssetPreferences) ListPresets() []string {
	entries, err := os.ReadDir(p.PresetsDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		if len(names) >= presetsCap {
			break // bounded even if the dir was stuffed by hand
		}
	}
	slices.Sort(names) // the package already imports slices, not sort
	return names
}

// ApplyPreset stages a preset as the import does, for the next start: the live state has too many mirrors to hot-swap.
func (p *AssetPreferences) ApplyPreset(name string) error {
	stem := sanitizePresetName(name)
	if stem == "" {
		return fmt.Errorf("config: bad preset name")
	}
	return p.ImportSettings(filepath.Join(p.PresetsDir(), stem+".json"))
}

func (p *AssetPreferences) DeletePreset(name string) error {
	stem := sanitizePresetName(name)
	if stem == "" {
		return fmt.Errorf("config: bad preset name")
	}
	return os.Remove(filepath.Join(p.PresetsDir(), stem+".json"))
}

// stripExportPasswords blanks every serverWarm[].loginPass so credentials don't travel; username survives.
func stripExportPasswords(data []byte) ([]byte, error) {
	var snap Settings
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

// ImportSettings validates and replaces the live file for the next start, as live caches can't be hot-swapped.
func (p *AssetPreferences) ImportSettings(srcPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("config: reading import: %w", err)
	}
	var probe Settings
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
	// Freeze the saver so the live session can't write its old state over the import.
	p.frozen.Store(true)
	return nil
}

func LearnedKey(host, typeName string) string {
	return host + LearnedKeySeparator + typeName
}

// LearnedExtCap mirrors the resolver's cap; they must agree or a reload drops formats the session learned.
const LearnedExtCap = 4

// RecordLearned moves ext to the front, since a host can serve several formats for a class.
// Mirrors assets.promoteExt; they must stay in step or a restart resurrects a truncated list.
func (p *AssetPreferences) RecordLearned(host, typeName, ext string) {
	if ext == "" {
		return
	}
	key := LearnedKey(host, typeName)
	p.Update(func(sn *Settings) {
		existing := sn.LearnedFormats[key]
		if len(existing) > 0 && existing[0] == ext {
			return // already preferred; no write, no dirty
		}
		if sn.LearnedFormats == nil {
			sn.LearnedFormats = map[string][]string{}
		}
		sn.LearnedFormats[key] = promoteFront(existing, ext, LearnedExtCap)
	})
}

// RecordLearnedList replaces because a re-read manifest supersedes probing; empty exts keeps what was learned.
func (p *AssetPreferences) RecordLearnedList(host, typeName string, exts []string) {
	if len(exts) == 0 {
		return
	}
	if len(exts) > LearnedExtCap {
		exts = exts[:LearnedExtCap]
	}
	key := LearnedKey(host, typeName)
	p.Update(func(sn *Settings) {
		if equalStrings(sn.LearnedFormats[key], exts) {
			return // unchanged; no write, no dirty
		}
		if sn.LearnedFormats == nil {
			sn.LearnedFormats = map[string][]string{}
		}
		sn.LearnedFormats[key] = cloneStrings(exts) // caller's slice must not alias the stored one
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ExtProfile returns a host's extensions.json profile, or ""; it overrides the manifest and global default.
func (s *Settings) ExtProfile(host string) string {

	return s.ExtProfiles[host]
}

// SetExtProfile sets or, with "", clears a host's profile.
func (p *AssetPreferences) SetExtProfile(host, profileJSON string) {
	if host == "" {
		return
	}
	p.Update(func(sn *Settings) {
		if sn.ExtProfiles == nil {
			sn.ExtProfiles = map[string]string{}
		}
		if profileJSON == "" {
			delete(sn.ExtProfiles, host)
		} else {
			sn.ExtProfiles[host] = profileJSON
		}
	})
}

// BlipTyping reports one blip per rate revealed letters and whether spaces blip too.
func (s *Settings) BlipTyping() (rate int, onSpaces bool) {

	r := s.BlipRate
	if r < MinBlipRate {
		r = defaultBlipRate
	}
	return r, s.BlipOnSpaces
}

func (p *AssetPreferences) SetBlipTyping(rate int, onSpaces bool) {
	rate = value.Clamp(rate, MinBlipRate, MaxBlipRate)
	p.Update(func(sn *Settings) {
		sn.BlipRate = rate
		sn.BlipOnSpaces = onSpaces
	})
}

const (
	themeOvThemesCap = 32

	themeOvRectsCap = 64
	// classicSlotCap bounds named slots; a profile's Classic map also carries floating surfaces, about 90 in all.
	classicSlotCap = 128
	// layoutPresetCap bounds legacy named layout snapshots.
	layoutPresetCap = 24
	// layoutProfileCap bounds saved full-state profiles.
	layoutProfileCap = 24
	// maxHiddenPanels sits above the ~45 registry ids so an all-hidden set is never truncated.
	maxHiddenPanels = 64
)

// Layout rotation: one byte in the 360/256 encoding of SpriteStyle.Rotation; all values are valid, so bound the map.
const (
	// RotStepFineDeg is the Shift+R step; bytes quantize to about 1.406 degrees, so 24 presses miss 0 (accepted).
	RotStepFineDeg = 15
)

// RotCoarseCycle is the R-key cycle: 0, 90, 180, 270 degrees land on whole bytes.
var RotCoarseCycle = [4]uint8{0, 64, 128, 192}

// RotationByteToDeg matches courtroom.SpriteStyle.RotationDeg; draw sites compare the byte to 0 first.
func RotationByteToDeg(b uint8) float64 {
	return float64(int(b)) * degreesPerTurn / rotationSteps
}

// RotationDegToByte is the inverse, rounded and wrapping at 360.
func RotationDegToByte(deg int) uint8 {
	deg %= degreesPerTurn
	if deg < 0 {
		deg += degreesPerTurn
	}
	// Round to nearest byte: add half a turn-step before the integer divide.
	return uint8((deg*rotationSteps + degreesPerTurn/2) / degreesPerTurn % rotationSteps)
}

// NextCoarseRotation snaps a fine-tilted byte up to the next coarse angle, so the key always makes visible progress.
func NextCoarseRotation(cur uint8) uint8 {
	for _, b := range RotCoarseCycle {
		if b > cur {
			return b
		}
	}
	return RotCoarseCycle[0]
}

// ThemeRectOverrides returns a copy of a theme's edited geometry in design-space [x,y,w,h].
func (s *Settings) ThemeRectOverrides(theme string) map[string][4]int {

	src := s.ThemeRectOv[theme]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string][4]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// HasThemeRectOverride avoids a per-frame copy; for the tab strip, presence is the state, as a drag can hit the seed.
func (s *Settings) HasThemeRectOverride(theme, key string) bool {

	if theme == "" || key == "" {
		return false
	}

	_, ok := s.ThemeRectOv[theme][key]
	return ok
}

func (p *AssetPreferences) SetThemeRectOverride(theme, key string, r [4]int) {
	if theme == "" || key == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetThemeRectOverride(theme, key, r)
	})
}

// ClearThemeRectOverride drops a widget's edit, "" = the whole theme's.
func (p *AssetPreferences) ClearThemeRectOverride(theme, key string) {
	p.Update(func(sn *Settings) {
		sn.ClearThemeRectOverride(theme, key)
	})
}

// ThemeRectRotationSnapshot returns a copy; the editor bakes it into its cache, so draws never read prefs.
func (s *Settings) ThemeRectRotationSnapshot(theme string) map[string]uint8 {

	src := s.ThemeRectRotations[theme]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]uint8, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetThemeRectRotation stores an angle byte, capped; absent draws as 0, so clear rather than set 0.
func (p *AssetPreferences) SetThemeRectRotation(theme, key string, deg uint8) {
	if theme == "" || key == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetThemeRectRotation(theme, key, deg)
	})
}

// ClearThemeRectRotation drops a widget's rotation, "" = the whole theme's.
func (p *AssetPreferences) ClearThemeRectRotation(theme, key string) {
	p.Update(func(sn *Settings) {
		sn.ClearThemeRectRotation(theme, key)
	})
}

// sanitizeClassicLayout drops NaN and inf, clamps and bounds; unknown names are kept for compatibility.
func sanitizeClassicLayout(in map[string][4]float64) map[string][4]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][4]float64, len(in))
	for k, v := range in {
		if k == "" || len(out) >= classicSlotCap {
			continue
		}
		ok := true
		for _, f := range v {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// X/Y may sit slightly off-stage by design; width and height must stay positive; the clamp rejects garbage.
		if v[2] <= 0 || v[3] <= 0 || v[0] < -1 || v[0] > 2 || v[1] < -1 || v[1] > 2 || v[2] > 2 || v[3] > 2 {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeLayoutPresets sanitizes each preset, drops blank names and empty presets and bounds the set.
func sanitizeLayoutPresets(in map[string]map[string][4]float64) map[string]map[string][4]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string][4]float64, len(in))
	for name, m := range in {
		if strings.TrimSpace(name) == "" || len(out) >= layoutPresetCap {
			continue
		}
		clean := sanitizeClassicLayout(m)
		if len(clean) == 0 {
			continue // an all-default preset is just the stock reset; nothing to store
		}
		out[name] = clean
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeHiddenPanels drops blanks and duplicates, preserves order and caps; nil-safe.
func sanitizeHiddenPanels(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, id := range in {
		if id == "" || seen[id] {
			continue
		}
		if len(out) >= maxHiddenPanels {
			break
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeClassicRotations drops blank names and bounds the map; any byte is valid, so no per-entry clamp. Nil-safe.
func sanitizeClassicRotations(in map[string]uint8) map[string]uint8 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]uint8, len(in))
	for k, v := range in {
		if k == "" || len(out) >= classicSlotCap {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeThemeRectRotations drops blank names and bounds both levels, unlike ThemeRectOv, which loads raw; nil-safe.
func sanitizeThemeRectRotations(in map[string]map[string]uint8) map[string]map[string]uint8 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]uint8, len(in))
	for theme, m := range in {
		if theme == "" || len(out) >= themeOvThemesCap {
			continue
		}
		if len(m) == 0 {
			continue
		}
		clean := make(map[string]uint8, len(m))
		for k, v := range m {
			if k == "" || len(clean) >= themeOvRectsCap {
				continue
			}
			clean[k] = v
		}
		if len(clean) == 0 {
			continue
		}
		out[theme] = clean
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeLayoutProfile cleans each axis with the live helpers; the caller drops a profile left with nothing.
func sanitizeLayoutProfile(in LayoutProfile) LayoutProfile {
	return LayoutProfile{
		Classic:   sanitizeClassicLayout(in.Classic),
		Anchors:   sanitizeClassicAnchors(in.Anchors),
		Hidden:    sanitizeHiddenPanels(in.Hidden),
		GridPx:    clampLayoutGridPx(in.GridPx),
		Rotations: sanitizeClassicRotations(in.Rotations),
	}
}

// layoutProfileEmpty reports whether a sanitized profile carries no state.
func layoutProfileEmpty(p LayoutProfile) bool {
	return len(p.Classic) == 0 && len(p.Anchors) == 0 && len(p.Hidden) == 0 && p.GridPx == 0 && len(p.Rotations) == 0
}

// sanitizeLayoutProfiles drops blank names and empty profiles, sanitizes each axis and bounds the set.
func sanitizeLayoutProfiles(in map[string]LayoutProfile) map[string]LayoutProfile {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]LayoutProfile, len(in))
	for name, prof := range in {
		if strings.TrimSpace(name) == "" || len(out) >= layoutProfileCap {
			continue
		}
		clean := sanitizeLayoutProfile(prof)
		if layoutProfileEmpty(clean) {
			continue
		}
		out[name] = clean
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LayoutProfileNames returns names in unspecified order; the UI sorts.
func (s *Settings) LayoutProfileNames() []string {

	if len(s.LayoutProfiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.LayoutProfiles))
	for k := range s.LayoutProfiles {
		names = append(names, k)
	}
	return names
}

// LayoutProfile returns a deep copy, zero if unknown, so the caller can't alias the live map.
func (s *Settings) LayoutProfile(name string) LayoutProfile {

	prof, ok := s.LayoutProfiles[name]
	if !ok {
		return LayoutProfile{}
	}
	return cloneLayoutProfile(prof)
}

// cloneLayoutProfile deep-copies so live and returned copies never alias.
func cloneLayoutProfile(in LayoutProfile) LayoutProfile {
	out := LayoutProfile{GridPx: in.GridPx}
	if len(in.Classic) > 0 {
		out.Classic = make(map[string][4]float64, len(in.Classic))
		for k, v := range in.Classic {
			out.Classic[k] = v
		}
	}
	if len(in.Anchors) > 0 {
		out.Anchors = make(map[string]ClassicAnchor, len(in.Anchors))
		for k, v := range in.Anchors {
			out.Anchors[k] = v
		}
	}
	if len(in.Hidden) > 0 {
		out.Hidden = append([]string(nil), in.Hidden...)
	}
	if len(in.Rotations) > 0 {
		out.Rotations = make(map[string]uint8, len(in.Rotations))
		for k, v := range in.Rotations {
			out.Rotations[k] = v
		}
	}
	return out
}

// SaveLayoutProfile replaces by name, sanitized and deep-copied, capped; a blank name or empty profile is ignored.
func (p *AssetPreferences) SaveLayoutProfile(name string, prof LayoutProfile) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	clean := sanitizeLayoutProfile(prof)
	if layoutProfileEmpty(clean) {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SaveLayoutProfile(name, clean)
	})
}

func (p *AssetPreferences) DeleteLayoutProfile(name string) {
	p.Update(func(sn *Settings) {
		sn.DeleteLayoutProfile(name)
	})
}

// ClassicLayoutOverrides returns a sanitized copy; the editor snapshots it once so the render path reads lock-free.
func (s *Settings) ClassicLayoutOverrides() map[string][4]float64 {

	if len(s.ClassicLayout) == 0 {
		return nil
	}
	out := make(map[string][4]float64, len(s.ClassicLayout))
	for k, v := range s.ClassicLayout {
		out[k] = v
	}
	return out
}

// SetClassicSlot stores a window-fraction [x,y,w,h] override, capped.
func (p *AssetPreferences) SetClassicSlot(name string, frac [4]float64) {
	if name == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetClassicSlot(name, frac)
	})
}

// ClearClassicSlot drops an override, "" = all; its anchor goes too, since an anchor only re-bases the override.
func (p *AssetPreferences) ClearClassicSlot(name string) {
	p.Update(func(sn *Settings) {
		sn.ClearClassicSlot(name)
	})
}

// ClassicAnchor pins a slot: Mode is horizontal {f,l,r,c} then vertical {f,t,b,c}; f = follow, c = centre, else edge.
// WinW/WinH are the window size at write time, to rebuild the exact pixel rect.
type ClassicAnchor struct {
	Mode string `json:"mode"`
	WinW int    `json:"winW"`
	WinH int    `json:"winH"`
}

// LayoutProfile is a named full-state layout snapshot; legacy presets migrate in as Classic-only profiles.
type LayoutProfile struct {
	Classic map[string][4]float64    `json:"classic,omitempty"`
	Anchors map[string]ClassicAnchor `json:"anchors,omitempty"`
	Hidden  []string                 `json:"hidden,omitempty"`
	GridPx  int                      `json:"gridPx,omitempty"`
	// Rotations is nil in older profiles; theme rotations stay out, as a theme-X angle on theme-Y is incoherent.
	Rotations map[string]uint8 `json:"rotations,omitempty"`
}

func validAnchorMode(m string) bool {
	if len(m) != 2 {
		return false
	}
	h, v := m[0], m[1]
	return (h == 'f' || h == 'l' || h == 'r' || h == 'c') &&
		(v == 'f' || v == 't' || v == 'b' || v == 'c')
}

// sanitizeClassicAnchors keeps well-formed entries, so the resolver needn't defend against junk per frame.
func sanitizeClassicAnchors(in map[string]ClassicAnchor) map[string]ClassicAnchor {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]ClassicAnchor, len(in))
	for k, a := range in {
		if k == "" || !validAnchorMode(a.Mode) || a.WinW <= 0 || a.WinH <= 0 {
			continue
		}
		if len(out) >= classicSlotCap {
			break
		}
		out[k] = a
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ClassicAnchorSnapshot returns a copy; the editor snapshots it once so the render path reads lock-free.
func (s *Settings) ClassicAnchorSnapshot() map[string]ClassicAnchor {

	if len(s.ClassicAnchors) == 0 {
		return nil
	}
	out := make(map[string]ClassicAnchor, len(s.ClassicAnchors))
	for k, v := range s.ClassicAnchors {
		out[k] = v
	}
	return out
}

// SetClassicAnchor rejects malformed modes and window sizes, capped.
func (p *AssetPreferences) SetClassicAnchor(name string, a ClassicAnchor) {
	if name == "" || !validAnchorMode(a.Mode) || a.WinW <= 0 || a.WinH <= 0 {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetClassicAnchor(name, a)
	})
}

// ClearClassicAnchor drops a pin; the override stays and the slot returns to fraction scaling.
func (p *AssetPreferences) ClearClassicAnchor(name string) {
	p.Update(func(sn *Settings) {
		sn.ClearClassicAnchor(name)
	})
}

// Layout-editor snap grid step; the Grid chip cycles a subset.
const (
	DefaultLayoutGridPx = 8
	// Min and max stop a hand-edited pref making snapping useless (1) or absurd (256).
	MinLayoutGridPx = 2
	MaxLayoutGridPx = 64
)

func clampLayoutGridPx(v int) int {
	if v == 0 {
		return 0 // absent: LayoutGridSize resolves the default
	}
	if v < MinLayoutGridPx {
		return MinLayoutGridPx
	}
	if v > MaxLayoutGridPx {
		return MaxLayoutGridPx
	}
	return v
}

// LayoutGridSize reports the snap step in logical px.
func (s *Settings) LayoutGridSize() int {
	return orDefault(s.LayoutGridPx, DefaultLayoutGridPx)
}

// SetClassicLayout replaces all overrides at once for undo and redo; copied, capped, and nil clears.
func (p *AssetPreferences) SetClassicLayout(m map[string][4]float64) {
	var clean map[string][4]float64
	if len(m) == 0 {
		clean = nil
	} else {
		cp := make(map[string][4]float64, len(m))
		for k, v := range m {
			if k == "" || len(cp) >= classicSlotCap {
				continue
			}
			cp[k] = v
		}
		clean = cp
	}
	p.Update(func(sn *Settings) {
		sn.ClassicLayout = clean
	})
}

// SetClassicAnchors replaces all pins but leaves ClassicLayout alone; profiles carry both axes independently.
func (p *AssetPreferences) SetClassicAnchors(m map[string]ClassicAnchor) {
	clean := sanitizeClassicAnchors(m) // drops junk + bounds at classicSlotCap
	p.Update(func(sn *Settings) {
		sn.ClassicAnchors = clean
	})
}

// ClassicRotationSnapshot returns a copy; the editor snapshots it once so the render path reads lock-free.
func (s *Settings) ClassicRotationSnapshot() map[string]uint8 {

	if len(s.ClassicRotations) == 0 {
		return nil
	}
	out := make(map[string]uint8, len(s.ClassicRotations))
	for k, v := range s.ClassicRotations {
		out[k] = v
	}
	return out
}

// SetClassicRotation stores an angle byte, capped; absent draws as 0, so use ClearClassicRotation for no rotation.
func (p *AssetPreferences) SetClassicRotation(name string, deg uint8) {
	if name == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetClassicRotation(name, deg)
	})
}

// ClearClassicRotation drops a rotation; the override stays.
func (p *AssetPreferences) ClearClassicRotation(name string) {
	p.Update(func(sn *Settings) {
		sn.ClearClassicRotation(name)
	})
}

// SetClassicRotations replaces all rotations, sanitized and copied; nil clears.
func (p *AssetPreferences) SetClassicRotations(m map[string]uint8) {
	clean := sanitizeClassicRotations(m) // drops blanks + bounds at classicSlotCap
	p.Update(func(sn *Settings) {
		sn.ClassicRotations = clean
	})
}

func (s *Settings) Macros() []MacroSpec {

	out := make([]MacroSpec, len(s.UserMacros))
	for i := range s.UserMacros {
		out[i] = snapCloneMacroSpec(s.UserMacros[i])
	}
	return out
}

func (p *AssetPreferences) SetMacros(in []MacroSpec) {
	clean := sanitizeMacros(in)
	p.Update(func(sn *Settings) {
		sn.UserMacros = clean
	})
}

// ThemeFitDefaultJustMoved reports this load's Stretch to Native move, so the caller arms one resize; a pure read.
// The stamp is flushed synchronously, as one lost in the debounce window would rerun the move and resize.
func (p *AssetPreferences) ThemeFitDefaultJustMoved() bool {

	return p.themeFitMovedToNative
}

func (p *AssetPreferences) SetThemeFit(mode int) {
	mode = value.Clamp(mode, ThemeFitStretch, themeFitModeMax)
	p.Update(func(sn *Settings) {
		sn.ThemeFit = mode
	})
}

// ThemeZoom reports the clamped Custom zoom, 100 = exactly fits.
func (s *Settings) ThemeZoom() int {
	return value.Clamp(orDefault(s.ThemeFitZoom, DefaultThemeZoom), MinThemeZoom, MaxThemeZoom)
}

// ThemePan reports the Custom pan in percent of the window.
func (s *Settings) ThemePan() (x, y int) {

	return s.ThemeFitPanX, s.ThemeFitPanY
}

func (p *AssetPreferences) SetThemeFitPan(x, y int) {
	x = value.Clamp(x, -MaxThemePan, MaxThemePan)
	y = value.Clamp(y, -MaxThemePan, MaxThemePan)
	p.Update(func(sn *Settings) {
		sn.ThemeFitPanX = x
		sn.ThemeFitPanY = y
	})
}

// ExportLearnedJSON returns the table as indented JSON, so one player's warm state can seed another's.
func (p *AssetPreferences) ExportLearnedJSON() ([]byte, error) {
	return json.MarshalIndent(p.Load().LearnedSnapshot(), "", jsonMarshalIndent)
}

// ImportLearnedJSON merges an export, imported entries winning, and reports how many landed.
func (p *AssetPreferences) ImportLearnedJSON(data []byte) (int, error) {
	var in map[string][]string
	if err := json.Unmarshal(data, &in); err != nil {
		return 0, fmt.Errorf("config: parsing learned-format import: %w", err)
	}
	n := 0
	p.Update(func(sn *Settings) {
		for k, v := range in {
			if k == "" || len(v) == 0 {
				continue
			}
			if sn.LearnedFormats == nil {
				sn.LearnedFormats = map[string][]string{}
			}
			sn.LearnedFormats[k] = cloneStrings(v)
			n++
		}
	})
	return n, nil
}

// LearnedSnapshot returns a deep copy, warming the resolver's snapshot at startup and on connect.
func (s *Settings) LearnedSnapshot() map[string][]string {

	out := make(map[string][]string, len(s.LearnedFormats))
	for k, v := range s.LearnedFormats {
		out[k] = cloneStrings(v)
	}
	return out
}

func (p *AssetPreferences) ClearLearned() {
	p.Update(func(sn *Settings) {
		sn.LearnedFormats = map[string][]string{}
	})
}

// dropLearnedType removes one type's entries across hosts from a snapshot.
func dropLearnedType(sn *Settings, typeName string) {
	suffix := LearnedKeySeparator + typeName
	for key := range sn.LearnedFormats {
		if strings.HasSuffix(key, suffix) {
			delete(sn.LearnedFormats, key)
		}
	}
}

// LocalAssets reports no-streaming mode; mounts are searched in order, mirroring AO2-Client mount paths.
func (p *AssetPreferences) LocalAssets() (enabled bool, mounts []string) {
	snap := p.Load()

	out := make([]string, len(snap.LocalAssetsPaths))
	copy(out, snap.LocalAssetsPaths)
	return snap.LocalAssetsEnabled, out
}

// SetLocalAssets refuses only growth past the cap; a flat length check would brick a user already over it.
func (p *AssetPreferences) SetLocalAssets(enabled bool, mounts []string) bool {
	accepted := true
	p.Update(func(sn *Settings) {
		if len(mounts) > LocalMountCap && len(mounts) > len(sn.LocalAssetsPaths) {
			accepted = false
			return
		}
		sn.LocalAssetsEnabled = enabled
		sn.LocalAssetsPaths = slices.Clone(mounts)
	})
	return accepted
}

// LayeredAssets reports reading mounts alongside the stream, the user's files winning at the same path.
func (p *AssetPreferences) LayeredAssets() (layered bool, mounts []string) {
	snap := p.Load()

	out := make([]string, len(snap.LocalAssetsPaths))
	copy(out, snap.LocalAssetsPaths)
	return snap.LocalAssetsLayered && !snap.LocalAssetsEnabled && len(out) > 0, out
}

// SetLayeredAssets clears the no-streaming flag, so the two bools never persist the ambiguous both-set state.
func (p *AssetPreferences) SetLayeredAssets(layered bool) {
	p.Update(func(sn *Settings) {
		if sn.LocalAssetsLayered == layered && !(layered && sn.LocalAssetsEnabled) {
			return
		}
		sn.LocalAssetsLayered = layered
		if layered {
			sn.LocalAssetsEnabled = false // the radio is exclusive; keep the store unambiguous
		}
	})
}

// BaseMountsEqual compares without copying, as internal/ui asks once per frame; mirrors ui's baseMounts.
func (p *AssetPreferences) BaseMountsEqual(want []string) bool {
	snap := p.Load()

	if !snap.LocalAssetsEnabled && !(snap.LocalAssetsLayered && len(snap.LocalAssetsPaths) > 0) {
		return len(want) == 0
	}
	return slices.Equal(snap.LocalAssetsPaths, want)
}

// FavoriteServers returns the list in pin order.
func (s *Settings) FavoriteServers() []FavoriteServer {

	out := make([]FavoriteServer, len(s.Favorites))
	copy(out, s.Favorites)
	return out
}

// AddFavorite stars or updates by URL, which must be the full ws:// or wss:// address so private servers work.
func (p *AssetPreferences) AddFavorite(name, url, description string) {
	if url == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.AddFavorite(name, url, description)
	})
}

// UpdateFavorite edits in place; false if oldURL is missing or newURL is another's; a no-op edit returns true.
func (p *AssetPreferences) UpdateFavorite(oldURL, newName, newURL, description string) bool {
	if oldURL == "" || newURL == "" {
		return false
	}
	accepted := false
	p.Update(func(sn *Settings) {
		accepted = sn.UpdateFavorite(oldURL, newName, newURL, description)
	})
	return accepted
}

func (p *AssetPreferences) RemoveFavorite(url string) {
	p.Update(func(sn *Settings) {
		sn.RemoveFavorite(url)
	})
}

// ExportFavoritesJSON marshals the phone book as shareable JSON.
func (p *AssetPreferences) ExportFavoritesJSON() ([]byte, error) {
	return json.MarshalIndent(p.Load().FavoriteServers(), jsonMarshalPrefix, jsonMarshalIndent)
}

// MergeFavoritesJSON adds shared entries, deduped by URL, skipping those without one; returns the new count.
func (p *AssetPreferences) MergeFavoritesJSON(data []byte) (int, error) {
	var in []FavoriteServer
	if err := json.Unmarshal(data, &in); err != nil {
		return 0, err
	}
	added := 0
	for _, f := range in {
		if strings.TrimSpace(f.URL) == "" || p.Load().IsFavorite(f.URL) {
			continue
		}
		p.AddFavorite(f.Name, f.URL, f.Description)
		added++
	}
	return added, nil
}

func (s *Settings) IsFavorite(url string) bool {

	for _, f := range s.Favorites {
		if f.URL == url {
			return true
		}
	}
	return false
}

const ShownameCap = 64

// ShownameList returns a copy of the global presets, cleared only by a factory reset.
func (s *Settings) ShownameList() []string {

	if len(s.ShownamePresets) == 0 {
		return nil
	}
	out := make([]string, len(s.ShownamePresets))
	copy(out, s.ShownamePresets)
	return out
}

// ShownameCount is an alloc-free length read, so the name picker rebuilds its options only on change.
func (s *Settings) ShownameCount() int {

	return len(s.ShownamePresets)
}

// AddShownamePreset trims, fold-dedups and caps; reports a change.
func (p *AssetPreferences) AddShownamePreset(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.AddShownamePreset(name)
	})
	return changed
}

func (p *AssetPreferences) RemoveShownamePreset(name string) bool {
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveShownamePreset(name)
	})
	return changed
}

const shownameKeyCap = 64

// ShownameKeyBinds returns a copy of the global key to showname map, nil when empty.
func (s *Settings) ShownameKeyBinds() map[string]string {

	if len(s.ShownameKeys) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.ShownameKeys))
	for k, v := range s.ShownameKeys {
		out[k] = v
	}
	return out
}

// SetShownameKeyBind binds a lowercase SDL key name; empty clears, capped.
func (p *AssetPreferences) SetShownameKeyBind(key, showname string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetShownameKeyBind(key, showname)
	})
}

const icPhraseKeyCap = 64

// ICPhraseBinds returns a copy of the key to IC-line map, nil when empty.
func (s *Settings) ICPhraseBinds() map[string]string {

	if len(s.ICPhraseKeys) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.ICPhraseKeys))
	for k, v := range s.ICPhraseKeys {
		out[k] = v
	}
	return out
}

// SetICPhraseKey binds a lowercase SDL key name to a canned line; empty clears, capped.
func (p *AssetPreferences) SetICPhraseKey(key, phrase string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return
	}
	phrase = strings.TrimSpace(phrase)
	p.Update(func(sn *Settings) {
		sn.SetICPhraseKey(key, phrase)
	})
}

const mutedSFXCap = 128

// IsSFXMuted ignores case; called once per played SFX, not per frame.
func (s *Settings) IsSFXMuted(name string) bool {

	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}

	for _, m := range s.MutedSFX {
		if m == name {
			return true
		}
	}
	return false
}

func (s *Settings) MutedSFXList() []string {

	if len(s.MutedSFX) == 0 {
		return nil
	}
	out := make([]string, len(s.MutedSFX))
	copy(out, s.MutedSFX)
	return out
}

// MuteSFX adds a name, lowercased, deduped and capped; reports a change.
func (p *AssetPreferences) MuteSFX(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.MuteSFX(name)
	})
	return changed
}

func (p *AssetPreferences) UnmuteSFX(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.UnmuteSFX(name)
	})
	return changed
}

const sfxFavoritesCap = 128

// SfxFavoritesList returns a copy of lowercased bare names, resolved per server by urls.SFX.
func (s *Settings) SfxFavoritesList() []string {

	if len(s.SfxFavorites) == 0 {
		return nil
	}
	out := make([]string, len(s.SfxFavorites))
	copy(out, s.SfxFavorites)
	return out
}

func (s *Settings) IsSfxFavorite(name string) bool {

	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}

	for _, s := range s.SfxFavorites {
		if s == name {
			return true
		}
	}
	return false
}

// ToggleSfxFavorite flips a star and reports the new state; false when adding at the cap.
func (p *AssetPreferences) ToggleSfxFavorite(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	nowFav := false
	p.Update(func(sn *Settings) {
		nowFav = sn.ToggleSfxFavorite(name)
	})
	return nowFav
}

// defaultModReasonTemplates are seeded on the first add or remove, so editing starts from what the user sees.
var defaultModReasonTemplates = []string{
	"Spam", "Harassment", "NSFW", "Disruptive", "Trolling", "Ban evasion", "Disrespect",
}

const modReasonTemplatesCap = 32

// ModReasonTemplatesList returns a clone; an empty list reads as the defaults, so deleting all re-seeds them.
func (s *Settings) ModReasonTemplatesList() []string {

	if len(s.ModReasonTemplates) == 0 {
		return cloneStrings(defaultModReasonTemplates)
	}
	return cloneStrings(s.ModReasonTemplates)
}

// seedModTemplates fills an empty list with the defaults before a mutation.
func seedModTemplates(sn *Settings) {
	if len(sn.ModReasonTemplates) == 0 {
		sn.ModReasonTemplates = cloneStrings(defaultModReasonTemplates)
	}
}

// AddModReasonTemplate preserves case, as these are display labels; trimmed, fold-deduped and capped.
func (p *AssetPreferences) AddModReasonTemplate(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.AddModReasonTemplate(s)
	})
	return changed
}

// modDurationsCap bounds customs; the enum presets cover the common spans.
const modDurationsCap = 16

// ModDurationsList returns a clone of custom tokens ("45m", "2d"); enum presets are the built-ins.
func (s *Settings) ModDurationsList() []string {

	return cloneStrings(s.ModDurations)
}

// AddModDuration stores what it's given; the caller canonicalizes, as config stays courtroom-free.
func (p *AssetPreferences) AddModDuration(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.AddModDuration(token)
	})
	return changed
}

func (p *AssetPreferences) RemoveModDuration(token string) bool {
	token = strings.TrimSpace(token)
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveModDuration(token)
	})
	return changed
}

func (p *AssetPreferences) RemoveModReasonTemplate(s string) bool {
	s = strings.TrimSpace(s)
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveModReasonTemplate(s)
	})
	return changed
}

// blipVolsCap is a safety ceiling; only characters off the default occupy an entry.
const blipVolsCap = 256

// blipVolumeDefault means no attenuation beyond the global blip volume.
const blipVolumeDefault = 100

// BlipVolumeFor returns a character's scale, 100 for unknown; called once per message.
func (s *Settings) BlipVolumeFor(char string) int {

	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" {
		return blipVolumeDefault
	}

	if v, ok := s.BlipVols[char]; ok {
		return v
	}
	return blipVolumeDefault
}

// BlipVolumes returns a copy of the overrides, nil when empty.
func (s *Settings) BlipVolumes() map[string]int {

	if len(s.BlipVols) == 0 {
		return nil
	}
	out := make(map[string]int, len(s.BlipVols))
	for k, v := range s.BlipVols {
		out[k] = v
	}
	return out
}

// SetBlipVolume clamps; the default clears the override so the map holds only real adjustments. Reports a change.
func (p *AssetPreferences) SetBlipVolume(char string, pct int) bool {
	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" {
		return false
	}
	pct = value.Clamp(pct, 0, blipVolumeDefault)
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.SetBlipVolume(char, pct)
	})
	return changed
}

// Safety ceilings for emote favourites, not expected sizes.
const (
	emoteFavCharsCap   = 1024
	emoteFavPerCharCap = 256
)

// EmoteFavsFor returns a copy of favourite indices; names duplicate within a character, so they can't be the key.
func (s *Settings) EmoteFavsFor(char string) []int {

	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" {
		return nil
	}

	src := s.EmoteFavs[char]
	if len(src) == 0 {
		return nil
	}
	out := make([]int, len(src))
	copy(out, src)
	return out
}

func (s *Settings) IsEmoteFav(char string, idx int) bool {

	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" || idx < 0 {
		return false
	}

	return containsInt(s.EmoteFavs[char], idx)
}

// ToggleEmoteFav flips a favourite, capped, and returns the new state; removing the last drops the character's entry.
func (p *AssetPreferences) ToggleEmoteFav(char string, idx int) bool {
	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" || idx < 0 {
		return false
	}
	nowFav := false
	p.Update(func(sn *Settings) {
		nowFav = sn.ToggleEmoteFav(char, idx)
	})
	return nowFav
}

// clampViewportExactPx passes 0 (off) and clamps the rest, so a hand-edited config can't size the stage off-screen.
func clampViewportExactPx(px int) int {
	if px == 0 {
		return 0
	}
	if px < MinViewportExactPx {
		return MinViewportExactPx
	}
	if px > MaxViewportExactPx {
		return MaxViewportExactPx
	}
	return px
}
