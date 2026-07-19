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

	// LearnedKeySeparator joins host and asset-type name in learned-format
	// map keys: "<host>|<type name>".
	LearnedKeySeparator = "|"

	prefsFilePerm   = 0o644
	prefsDirPerm    = 0o755
	prefsTmpPattern = PrefsFileName + ".*.tmp"

	// corruptSuffixPrefix is appended (with a timestamp) to the preferences
	// file name when an existing file fails to parse, so the unparseable copy
	// is preserved for recovery instead of being silently overwritten with
	// defaults by the first debounced save. e.g. asset_preferences.json.corrupt-20260711-150405
	corruptSuffixPrefix = ".corrupt-"
	// corruptStampLayout timestamps the quarantine backup name. It is
	// deliberately colon-free: ':' is illegal in Windows file names, so an
	// RFC3339-style stamp would make os.Rename fail on this platform. Second
	// precision suffices — there is exactly one load per launch and the
	// rename removes the original, so a relaunch never re-collides.
	corruptStampLayout = "20060102-150405"

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

// Blip cadence: one chat blip per N revealed letters. defaultBlipRate ships at 2
// (Ace Attorney style — the cadence most players expect); 1 = a blip every
// letter. MaxBlipRate caps the slider. Mirrors courtroom.DefaultBlipRate (config
// can't import courtroom).
const (
	defaultBlipRate = 2
	MinBlipRate     = 1
	MaxBlipRate     = 10
)

// defaultUIScaleAuto ships ON: the UI scale follows the display DPI
// (HiDPI screens render readable out of the box); unticking it re-arms
// the manual UI scale row in Settings.
const defaultUIScaleAuto = true

// defaultThemeLayout ships ON: when the active theme defines the AO2
// courtroom_design.ini geometry, the courtroom adopts it wholesale —
// that IS what picking a theme means to AO players.
const defaultThemeLayout = true

// Theme-fit modes: how an AO2 theme's FIXED design size fills a differently
// shaped window. Stretch (default — webAO-style) fills edge-to-edge with a
// slight aspect distortion; Letterbox keeps the exact proportions with bars;
// Crop scales up to fill and lets the overflow run off-screen. Stretch is 0 so
// the zero value (and every pre-existing prefs file) lands on the new default.
const (
	ThemeFitStretch   = 0
	ThemeFitLetterbox = 1
	ThemeFitCrop      = 2
	ThemeFitCustom    = 3 // manual zoom + pan (crop to taste)
	defaultThemeFit   = ThemeFitStretch
)

// Custom theme-fit knobs: a manual zoom (percent of the letterbox-fit scale,
// so 100 = exactly fits) and a pan (percent of the window, ±), for cropping a
// theme to taste. Bounded so it can't be scaled or panned into nothing.
const (
	DefaultThemeZoom = 100
	MinThemeZoom     = 50
	MaxThemeZoom     = 300
	MaxThemePan      = 50
)

// defaultPlainLobby ships ON: the lobby/server list uses the plain client
// backdrop, not the theme's lobbybackground — a busy AO2 lobby image (made for
// AO2's own list) often renders our server list unreadable. Untick it to get
// the theme's lobby; the courtroom always uses the theme regardless.
const defaultPlainLobby = true

// defaultCatchUpWhenBehind ships ON: in a packed room the IC stage otherwise
// crawls through every queued preanim/shout, falling minutes behind real-time.
// Catch-up fast-forwards the backlog; the IC log still keeps every message.
const defaultCatchUpWhenBehind = true

// Catch-up queue-depth threshold: engage once this many messages (or more) are
// waiting behind the one starting. The default is the floor (1) so the IC stage
// stays real-time — in a busy room the newest message types out in full (with
// its sprite, name and effects) while any backlog behind it flashes past, so
// the textbox always tracks the latest line instead of crawling seconds behind.
// At the default of 1, a message plays in full only when nothing is queued
// behind it (calm back-and-forth still plays every line; only a genuine pile-up
// catches up). Raise it to let a deeper backlog animate. DefaultCatchUpThreshold
// is exported so the UI can show it; clamped to [catchUpThresholdMin, catchUpThresholdMax].
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

// defaultShowFriendButton shows the per-row "+ Friend" button in the player
// list out of the box (it can be hidden in Settings). Default-ON so the
// feature is discoverable; the toggle is for people who find it clutters
// the panel.
const defaultShowFriendButton = true

// defaultAutoClipModcall saves a small text clip of the recent IC log whenever a
// modcall fires (sent or received), so there's always a frozen record of what was
// happening — useful evidence for mods/CMs. Default-ON (it only writes on the rare
// modcall event); a Settings toggle disables it.
const defaultAutoClipModcall = true

// defaultGroupChatButton shows the Group Chat / DMs launcher as a main on-screen
// button in the courtroom control row (not just Extras). Default-ON for
// discoverability; a Settings → Chat toggle hides it.
const defaultGroupChatButton = true

// defaultCharChatbox draws a speaking character's own chatbox skin (char.ini
// chat=<misc>) like AO2/webAO — canonical behaviour, so default-ON; a
// Settings → Chat toggle disables it (which also stops the misc fetches).
const defaultCharChatbox = true

// defaultRightClickHideSprite makes right-clicking a character sprite offer to
// hide it from the viewport (default ON; a Settings toggle disables it).
const defaultRightClickHideSprite = true

// defaultDragLayout makes the courtroom resize by dragging panel edges (the
// viewport↔log divider) the default; unchecking it brings back the +/− knob
// buttons. Default-ON per the request ("mouse is draggable by default").
const defaultDragLayout = true

// defaultSmoothScaling turns on linear texture filtering (SDL render
// scale quality): sprites stretched to the viewport stop shimmering.
const defaultSmoothScaling = true

// defaultUpdateCheck enables the one-shot GitHub-Releases update check at
// launch (M13). On by default per the user's intent; the check is a single
// async probe fired off the boot path, and a dev build never hits the network.
const defaultUpdateCheck = true

// defaultShowAssetWarnings ships OFF: the red "Missing asset" banner naming
// every 404 was a constant annoyance on servers with sparse packs. The
// failures still reach the debug overlay (the dedicated failure log); this
// flag only governs the on-screen banner. Settings → Assets turns it back on.
const defaultShowAssetWarnings = false

// defaultSpriteMove ships OFF: click-drag sprite repositioning is a power
// feature that surprised players who grabbed a sprite by accident. Settings
// re-enables it (and offers a one-click reset of every moved sprite).
const defaultSpriteMove = false

// defaultDeskFollowManifest ships OFF: desks default to WebP and STAY WebP
// even when a server's extensions.json declares another format for its
// background class (which desk overlays share). ON lets desks follow the
// manifest like backgrounds do.
const defaultDeskFollowManifest = false

// defaultSpritePreview ships ON: hovering a character/emote button pops a
// full-size sprite preview. OFF disables the pop-up entirely.
const defaultSpritePreview = true

// Sprite-preview hover dwell: how long the cursor must rest on a button before
// the preview shows. Default 5 s (the user's pick); bounded so it can't be set
// to fire instantly-on-graze or effectively never.
const (
	DefaultPreviewHoverMs = 5000
	minPreviewHoverMs     = 500
	maxPreviewHoverMs     = 15000
)

// Sprite-preview box height (px). The old fixed default was AO's native
// 192 px stage height, which read "really tiny" and made people corner-drag
// it every time (playtest) — the default is now double that, and Settings
// exposes it so the per-session grip resize is a tweak, not a ritual. Bounds
// match the grip clamp in the UI.
const (
	DefaultPreviewHeightPx = 384
	MinPreviewHeightPx     = 96
	MaxPreviewHeightPx     = 720
)

// defaultAutoLoginToast ships ON: when a saved auto-login fires on join, a
// toast + desktop notification confirms it ("am I logged in rn?") so a mod
// knows their session is authenticated. Toggleable off in Settings.
const defaultAutoLoginToast = true

// defaultScreenEffects ships ON: the AO2 screenshake (\s) and realization flash
// (\f) — both the codes typed into a message and the field-based shake/realization
// — render by default like the AO2 client. A dedicated Settings toggle turns them
// off (separate from the accessibility "Reduce motion", which also suppresses them).
const defaultScreenEffects = true

// defaultWordDelete ships ON: Ctrl+Backspace in any focused text field deletes
// the preceding word (trailing whitespace + the run before it) instead of one
// rune, matching the near-universal editor convention. A dedicated Settings toggle
// restores plain-key behavior (with the pref off, a focused Ctrl+Backspace is a
// consumed no-op — it never falls through to a Ctrl+Backspace hotkey mid-typing).
const defaultWordDelete = true

// defaultAdditiveText ships ON: the 2.8 ADDITIVE flag is honored — an ADDITIVE=1
// line appends to the previous one (narration-style RP; AO2 appends on any additive
// line with no char-id gate), and the outgoing Additive checkbox shows when the
// server advertises the feature. OFF falls back to the pre-2.8 replace behavior (no
// checkbox, incoming additive replaces).
const defaultAdditiveText = true

// defaultCallwordToast ships ON: when a callword is heard, an in-app toast
// names it (alongside the flash + ping), like the modcall/friend toasts.
// Toggleable off in Settings (streamer mode suppresses it regardless).
const defaultCallwordToast = true

// defaultMessageCounter ships ON: a live IC character count so you can see how
// long a message is getting (servers truncate very long lines).
const defaultMessageCounter = true

// defaultICTimestamps ships OFF (playtest): when ON, each IC log line is prefixed
// with the local time it arrived, so you can see when people spoke. Toggleable in
// Settings → Chat.
const defaultICTimestamps = false

// defaultAutoReconnect ships ON: after an unexpected drop, AsyncAO auto-retries
// the last server with backoff (the manual Reconnect button still works, and a
// deliberate Disconnect never auto-retries).
const defaultAutoReconnect = true

// defaultClipSpritesToStage clips the character sprites to the stage rect so a
// large pair / reposition OFFSET can't spill a sprite over the chatbox or the log.
// Default ON (a power-user Settings toggle turns it off for freeform placement).
const defaultClipSpritesToStage = true

// defaultEventDrivenLoop enables the EXPERIMENTAL event-driven render loop
// (test-branch trial): static screens stop rendering entirely between real
// signals — input wakes the loop instantly (an OS-level event wait instead of
// a blind sleep), network packets / finished decodes push a wake event, and
// blinking carets / ticking clocks redraw on their own schedule instead of
// holding the idle frame rate. Default ON so testers exercise it; the Settings
// toggle is the kill switch back to the classic pacing loop.
const defaultEventDrivenLoop = true

// defaultMotionRedrawPerEvent makes bare pointer motion render one frame per motion
// event (then re-park) instead of holding the full frame rate through the motion
// grace — less GPU/power on a moving cursor over static UI. Default ON as of
// v1.55.1; the Settings toggle turns it off for the old hold-full-rate behaviour.
const defaultMotionRedrawPerEvent = true

// defaultMusicHistory ships ON: AsyncAO keeps a session list of the songs played
// in the room (the Jukebox "Recently played" view) so you can grab a link
// someone /played. Toggleable off in Settings.
const defaultMusicHistory = true

// defaultMusicStreaming ships ON: AsyncAO fetches and plays custom /play tracks
// (Discord/catbox links etc.) like any other AO2 client. Turning it off in
// Settings → Audio (or the quick volume popover) stops AsyncAO from downloading
// any /play music at all — including the user's own plays, which round-trip
// through the server, so they go silent locally too. Now-Playing still tracks
// what the room intends to play; it just isn't audible on this client.
const defaultMusicStreaming = true

// defaultShowMissingPlaceholder ships ON: like AO2-Client, AsyncAO draws a
// "placeholder" (the missingno glitch sprite) for a character emote sprite whose
// whole fallback chain 404'd, instead of holding the previous (different)
// character on stage. AO2 shows it unconditionally; we make it opt-OUT for players
// who prefer a blank stage or the last-sprite hold. NOT omitempty (see the *bool
// load DTO): an explicit OFF must persist, not read back as "absent → default ON".
const defaultShowMissingPlaceholder = true

// defaultICCustomColor seeds the free IC hex colour picker (v1.52.0) before
// the user's first pick: a readable sky-blue on the dark chatbox, packed
// 0xRRGGBB like the highlight colour below.
const defaultICCustomColor = 0x7ADCF0

// defaultHighlightColor is the IC/OOC log text-selection highlight, packed
// 0xRRGGBB — defaults to the accent (120,170,255) so the look is unchanged
// until the user customizes it in Settings.
const defaultHighlightColor = 0x78AAFF

// Sprite colour-FX knobs (all optional, all OFF/neutral by default). The
// rainbow Speed maps to the hue-rotation period and Vividness to the colour-mod
// channel floor (render side); both are plain 0..100 sliders. The solid tint is
// a packed 0xRRGGBB wash. Ranges are clamped on load and by the setters.
const (
	minRainbowSpeed        = 1   // slowest hue rotation
	maxRainbowSpeed        = 100 // fastest
	defaultRainbowSpeed    = 70  // ~2.6 s per cycle (close to the original fixed look)
	minRainbowVivid        = 0   // subtlest tint
	maxRainbowVivid        = 100 // most saturated / neon
	defaultRainbowVivid    = 65
	defaultSpriteTintColor = 0xFF44CC // hot pink — only used once Solid tint is enabled
)

// Scene-replay playback speed (M16): a percent where 100 = the comfortable
// readable base, lower = slower (longer typing + linger so the whole line can
// be read), higher = faster. Default-ON to a slower-than-live pace because the
// live chat crawl is tuned for typing, not watching back.
const (
	minReplaySpeed     = 25
	maxReplaySpeed     = 200
	defaultReplaySpeed = 100
)

// Scene GIF/WebP export options (the studio) — sticky so a chosen look persists.
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
	// Video export container/codec (the 🎥 Video button). Only "mp4" (H.264) and
	// "webm" (VP9) are valid; anything else normalizes to the MP4 default.
	defaultVideoFormat = "mp4"
)

// ExportOptions is the persisted scene-export configuration (GIF + animated
// WebP). Output is 4:3 at HeightPx; FPS is the capture/playback cadence; Quality
// is the lossy WebP quality (GIF is always 256-colour); Loop loops the animation;
// TextScale sizes the chatbox text (percent of a base fitted to the output size,
// so the live chat zoom doesn't blow the text up in the small capture).
type ExportOptions struct {
	HeightPx  int  `json:"heightPx"`
	FPS       int  `json:"fps"`
	Quality   int  `json:"quality"`
	Loop      bool `json:"loop"`
	TextScale int  `json:"textScale"`
	// VideoFormat picks the 🎥 Video container/codec ("mp4" H.264 or "webm" VP9);
	// it has no effect on the GIF/WebP buttons. Empty = the MP4 default.
	VideoFormat string `json:"videoFormat,omitempty"`
	// Watermark (#74) stamps the export's top-right corner with WatermarkText, or —
	// when that's blank — the recording's server + date. Off by default.
	Watermark     bool   `json:"watermark,omitempty"`
	WatermarkText string `json:"watermarkText,omitempty"`
	// Subtitles (#69) writes .srt + .vtt sidecars beside a 🎥 Video export,
	// cue-timed to the exported frames. Off by default.
	Subtitles bool `json:"subtitles,omitempty"`
	// CopyToClipboard (#71) puts the finished export FILE on the OS clipboard
	// (Windows) so it pastes straight into Discord. Off by default.
	CopyToClipboard bool `json:"copyToClipboard,omitempty"`
}

// maxWatermarkLen bounds the custom watermark stamp — it's a corner credit, not a
// caption (and the label raster keys a texture per distinct string).
const maxWatermarkLen = 64

// defaultExportOptions is the out-of-box export look.
func defaultExportOptions() ExportOptions {
	return ExportOptions{HeightPx: defaultExportHeight, FPS: defaultExportFPS, Quality: defaultExportQuality, Loop: true, TextScale: defaultExportText, VideoFormat: defaultVideoFormat}
}

// normalizeVideoFormat maps any stored/edited value to a supported one ("mp4" or
// "webm"), defaulting to MP4 — kept here (not in videoenc) so config has no UI
// dependency.
func normalizeVideoFormat(s string) string {
	if strings.EqualFold(s, "webm") {
		return "webm"
	}
	return defaultVideoFormat
}

// SpriteStylePref persists the user's own transmitted sprite customization (the
// Sprite Style picker): a recolour, opacity, glow, and gentle motion that rides
// invisibly in their messages so other AsyncAO clients render it. It mirrors
// courtroom.SpriteStyle field-for-field (config can't import courtroom); the App
// converts between them. The all-zero value means "no style".
type SpriteStylePref struct {
	Tint    bool  `json:"tint,omitempty"`
	R       uint8 `json:"r,omitempty"`
	G       uint8 `json:"g,omitempty"`
	B       uint8 `json:"b,omitempty"`
	Opacity uint8 `json:"opacity,omitempty"` // percent 1..100; 0 = unset = opaque
	Glow    bool  `json:"glow,omitempty"`
	Wobble  bool  `json:"wobble,omitempty"`
	Spin    bool  `json:"spin,omitempty"`
	// Richer effects (#103): hue-cycle rainbow, mirror, and percent
	// brightness/scale + a tilt (0 = unset/neutral). Clamped at render.
	HueCycle bool `json:"hueCycle,omitempty"`
	FlipH    bool `json:"flipH,omitempty"`
	// Per-pixel effects (#103 slice 2): negate / desaturate. The renderer builds a
	// cached variant texture for these (SetColorMod can't do either).
	Invert    bool      `json:"invert,omitempty"`
	Grayscale bool      `json:"grayscale,omitempty"`
	Sepia     bool      `json:"sepia,omitempty"`         // #34 warm brown-tone per-pixel variant
	Posterize bool      `json:"posterize,omitempty"`     // #34 channel-quantise (poster look)
	Restyle   uint8     `json:"restyle,omitempty"`       // extra per-pixel look picker (0 none, else a courtroom.Variant* redscale/solarize/neon…)
	Motion    uint8     `json:"motion,omitempty"`        // #34 transmitted movement path (0 none / orbit / bounce / sway / drift)
	Path      [16]uint8 `json:"motionPath,omitempty"`    // #34 custom drawn path waypoints (packed 4-bit X/Y); a fixed array so SpriteStylePref stays == comparable (size MUST match courtroom maxPathPoints)
	PathLen   uint8     `json:"motionPathLen,omitempty"` // # active waypoints (0 = none, else 2..16)
	// Silhouette effects (#8): a white outline and a dark drop-shadow drawn behind the
	// sprite. Transmitted via a backward-compatible flags2 byte on the style wire.
	Outline    bool `json:"outline,omitempty"`
	DropShadow bool `json:"dropShadow,omitempty"`
	Glitch     bool `json:"glitch,omitempty"` // #13 chromatic-aberration glitch
	// OutlineR/G/B colour the #8 outline (0,0,0 = default white). Transmitted with the outline.
	OutlineR   uint8 `json:"outlineR,omitempty"`
	OutlineG   uint8 `json:"outlineG,omitempty"`
	OutlineB   uint8 `json:"outlineB,omitempty"`
	Brightness uint8 `json:"brightness,omitempty"`
	Scale      uint8 `json:"scale,omitempty"`
	Rotation   uint8 `json:"rotation,omitempty"`
	// Two-tone hue paint: split row (percent of sprite height from the top; 0 = one
	// colour) + the lower band's colour ("head red, rest blue"). Only meaningful while
	// hue paint (Tint+Grayscale) is on — styleFromPref (ui) keeps it off the wire
	// otherwise, so a stale value can't fatten the frame.
	PaintSplit uint8 `json:"paintSplit,omitempty"`
	Paint2R    uint8 `json:"paint2R,omitempty"`
	Paint2G    uint8 `json:"paint2G,omitempty"`
	Paint2B    uint8 `json:"paint2B,omitempty"`
	// Glitch options: the look (a courtroom.Glitch* mode byte) and the two fringe
	// ghost colours (all-zero = the classic red/blue). Only meaningful while Glitch
	// is on — styleFromPref (ui) keeps them off the wire otherwise.
	GlitchMode uint8 `json:"glitchMode,omitempty"`
	GlitchAR   uint8 `json:"glitchAR,omitempty"`
	GlitchAG   uint8 `json:"glitchAG,omitempty"`
	GlitchAB   uint8 `json:"glitchAB,omitempty"`
	GlitchBR   uint8 `json:"glitchBR,omitempty"`
	GlitchBG   uint8 `json:"glitchBG,omitempty"`
	GlitchBB   uint8 `json:"glitchBB,omitempty"`
}

// clampOpacity bounds a persisted/edited opacity to [0,100] (0 = unset/opaque).
func clampOpacity(v uint8) uint8 {
	if v > 100 {
		return 100
	}
	return v
}

// StylePreset (#126) is a named, saved bundle of the user's own visual identity — sprite
// style + text colour, plus an optional emote applied by NAME when the current character has
// one by that name (so a preset stays useful across characters). Applied in one click, or
// bound to a bare key for hands-free swapping (Key; the bind-capture flow mirrors the M6
// showname keybinds). All local — applying a preset just sets your existing style/colour/emote.
type StylePreset struct {
	Name  string          `json:"name"`
	Style SpriteStylePref `json:"style"`
	Color int             `json:"color"`           // icColor text-colour index
	Emote string          `json:"emote,omitempty"` // best-effort emote anim name
	Key   string          `json:"key,omitempty"`   // bound bare key ("" = none)
}

// stylePresetCap bounds the saved presets (hard rule §17.4 — a pref list can't grow without
// bound); a real user keeps a handful of moods.
const stylePresetCap = 48

// Character profile (#101) field length caps — bounded so the pref file (and, in
// a later slice, anything transmitted) can't balloon. The wire slice will send a
// tiny fingerprint, not these bytes, so art/song are URLs, never data.
const (
	profileNameMax     = 40
	profilePronounsMax = 32
	profileTagMax      = 80
	profileBioMax      = 400
	profileURLMax      = 300
)

// ProfilePref is the user's character profile (#101): a small card — pronouns, a
// one-line tag, a short bio, and URLs for art / theme-song — shown on the player
// list to other AsyncAO clients (a later slice transmits it; standard clients are
// unaffected). Configurable: Enabled is the master switch, ShowOnList controls
// whether it appears on the list.
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

// clampProfile bounds every field length (the setter and the load-merge both run
// it, so a hand-edited prefs file can't smuggle in an oversized field).
func clampProfile(p ProfilePref) ProfilePref {
	p.Name = clampLen(p.Name, profileNameMax)
	p.Pronouns = clampLen(p.Pronouns, profilePronounsMax)
	p.Tag = clampLen(p.Tag, profileTagMax)
	p.Bio = clampLen(p.Bio, profileBioMax)
	p.ThemeSong = clampLen(p.ThemeSong, profileURLMax)
	p.ArtURL = clampLen(p.ArtURL, profileURLMax)
	return p
}

// clampLen truncates s to at most n runes (rune-safe so a multibyte tail can't be
// split mid-character).
func clampLen(s string, n int) string {
	if len(s) <= n { // fast path: byte length ≤ n ⇒ rune count ≤ n
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// AutoStatusPref (#M1) auto-sets your presence status from words you type in IC. Each
// field is a comma-separated list of trigger words; when an outgoing message contains
// one (whole-word, case-insensitive), your status flips — so announcing "brb" can flip
// you to AFK on that very message. OFF by default; ClearWords (e.g. "back") returns you
// to no status. The status indices map to courtroom.Status (None/AFK/Busy/Writing/LFRP);
// config stays courtroom-free by using one field per status.
type AutoStatusPref struct {
	Enabled      bool   `json:"enabled,omitempty"`
	ClearWords   string `json:"clearWords,omitempty"` // → no status (e.g. "back")
	AFKWords     string `json:"afkWords,omitempty"`
	BusyWords    string `json:"busyWords,omitempty"`
	WritingWords string `json:"writingWords,omitempty"`
	LFRPWords    string `json:"lfrpWords,omitempty"`
}

// autoStatusWordsMax bounds each trigger-word field so a hand-edited prefs file can't
// smuggle in a huge string.
const autoStatusWordsMax = 200

// defaultAutoStatusPref: OFF, with sensible word lists pre-filled so enabling it just
// works (and they're editable / removable).
func defaultAutoStatusPref() AutoStatusPref {
	return AutoStatusPref{
		ClearWords: "back",
		AFKWords:   "brb, afk, away",
		BusyWords:  "busy, dnd",
	}
}

// sanitizeAutoStatus bounds every word field (the setter and the load-merge both run it).
func sanitizeAutoStatus(a AutoStatusPref) AutoStatusPref {
	a.ClearWords = clampLen(a.ClearWords, autoStatusWordsMax)
	a.AFKWords = clampLen(a.AFKWords, autoStatusWordsMax)
	a.BusyWords = clampLen(a.BusyWords, autoStatusWordsMax)
	a.WritingWords = clampLen(a.WritingWords, autoStatusWordsMax)
	a.LFRPWords = clampLen(a.LFRPWords, autoStatusWordsMax)
	return a
}

// Per-speaker name colours (OFF by default): each speaker's name is tinted by a
// stable hash of the name. Saturation/value are user-tunable; value has a floor
// so a name can't go unreadable-dark on the chat panel.
const (
	defaultNameColorSat = 60
	defaultNameColorVal = 90
	minNameColorVal     = 50
)

// Background slideshow (M5, OFF by default): while the courtroom is idle (no
// message on stage), cycle through the server's backgrounds every N seconds as
// ambiance. Bounded so the timer can't be set pathologically fast or slow.
const (
	defaultBgSlideshowSecs = 15
	minBgSlideshowSecs     = 3
	maxBgSlideshowSecs     = 600
)

// maxDownloadKBps bounds the optional downloader bandwidth cap (KiB/s). 0 =
// unlimited (the DEFAULT — a throttle on by default would slow grabs out of the
// box, a perf regression). The 1 GiB/s ceiling is effectively unlimited but
// keeps the value finite (rule §17.4).
const maxDownloadKBps = 1 << 20

// Layout scale bounds (percent). Defaults preserve the original layout:
// viewport 66 ≈ the old fixed 2/3 width; the text/height scales at 100.
const (
	DefaultViewportPercent = 66
	MinViewportPercent     = 40
	MaxViewportPercent     = 85

	// Exact viewport sizing (the "precise size" control): the stage's native art
	// is 256×192, so width is measured in px and crisp at integer multiples of
	// ViewportArtW. Min = one full multiple; max caps it at 4K-ish so a typo can't
	// produce an off-screen stage. 0 (not in [Min,Max]) means "off — size by %".
	ViewportArtW        = 256  // native AO stage width (height = ×3/4 = 192)
	ViewportArtH        = 192  // native AO stage height
	MinViewportExactPx  = 256  // 1× — anything smaller isn't worth a fixed size
	MaxViewportExactPx  = 4096 // 16× — sanity cap (no off-screen typos)
	ViewportExactStepPx = 256  // wheel / slider step = one crisp art-multiple

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

	// Chatbox panel opacity (flat fallback skin only): percent, 0 = fully
	// see-through (text only), 100 = solid. Default ≈ the previous hardcoded
	// 215/255 panel alpha.
	MinChatboxOpacity     = 0
	MaxChatboxOpacity     = 100
	DefaultChatboxOpacity = 84

	// ScaleStepPercent is the −/+ button increment shared by the UI.
	ScaleStepPercent = 25
	// ViewportStepPercent is the viewport −/+ increment.
	ViewportStepPercent = 5

	// Global UI scale (renderer-level: every element, font, and grid
	// scales together; the mouse unprojects through the same factor).
	MinUIScalePercent  = 75
	MaxUIScalePercent  = 200
	UIScaleStepPercent = 5

	// DPI seeding (#77 Part B). BaselineDPI is the "100%" logical DPI Windows
	// and SDL report at standard (unscaled) desktop scaling; the queried DPI
	// divided by it gives the OS scale factor (144 dpi → 150%). The auto UI
	// scale is floored at MinAutoUIScalePercent so an unreliable / low DPI
	// reading can never auto-SHRINK the UI below native — the never-below-100
	// floor from #6 (kept in force for Part B). This is DELIBERATELY above
	// MinUIScalePercent (75, the MANUAL slider floor): a user may pick 75%
	// explicitly, but auto-detection never shrinks.
	BaselineDPI           = 96.0
	MinAutoUIScalePercent = 100

	// Window sizing (Settings → Window): the default windowed size, and the
	// floor a request can't go below. A saved size of 0 means "use the default".
	DefaultWindowW = 1152
	DefaultWindowH = 864
	MinWindowW     = 640
	MinWindowH     = 480
)

// ClampWindowSize fits a requested window size into [min, display-usable].
// usableW/H ≤ 0 means the display size is unknown (skip the ceiling). Pure, so
// the live SDL resize AND the startup clamp share it (a saved oversize on a
// smaller monitor can't re-strand the window off-screen) — and it's testable
// without SDL.
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

// defaultAudioVolume is full volume (the pre-settings behavior) AND the slider
// ceiling (the load-clamp max), so it must stay 100.
const defaultAudioVolume = 100

// defaultStartVolume is the INITIAL per-channel level on a fresh install — a little
// under full so a first launch isn't a blast (#9, ZeitHeld). Master stays at 100 so
// the effective level is ~70% (master scales the channels) and the ceiling is intact;
// existing users keep their saved levels.
const defaultStartVolume = 70

// Hold-to-clear: hold a key (default Backspace, rebindable) this long to wipe a
// focused text field at once, instead of deleting char-by-char.
const (
	defaultHoldClearOn  = true
	defaultHoldClearKey = "Backspace"
	DefaultHoldClearMs  = 1500
	MinHoldClearMs      = 300
	MaxHoldClearMs      = 5000
)

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

// DPIScalePercent maps a display DPI to the auto UI-scale percent (#77 Part B):
// dpi / BaselineDPI, as a percent, rounded half-up, floored at
// MinAutoUIScalePercent. 96 dpi → 100%, 144 dpi → 150%, 168 dpi → 175%. It is
// the pure seam the DPI-seeding path funnels through so a HiDPI monitor's
// DEFAULT size is correct without the user finding the slider; the floor keeps
// the never-auto-shrink rule (#6). A non-positive or sub-baseline dpi returns
// the floor (an unreliable reading must not shrink us). It does NOT snap to the
// UI-scale step — SetAutoScaleFromWindow does that once after combining this
// with the window-size factor, so snapping here would double-round.
func DPIScalePercent(dpi float64) int {
	if dpi <= 0 {
		return MinAutoUIScalePercent
	}
	pct := int(dpi/BaselineDPI*100 + 0.5) // round half up
	if pct < MinAutoUIScalePercent {
		return MinAutoUIScalePercent
	}
	return pct
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
	UpdateExperimental     bool                         `json:"updateExperimental"` // follow the prerelease/test-branch feed (Power user; default false = stable)
	HighlightColor         int                          `json:"highlightColor"`
	ICCustomColor          int                          `json:"icCustomColor"` // last free IC hex pick (packed 0xRRGGBB; v1.52.0)
	NameColors             bool                         `json:"nameColors"`
	NameSat                int                          `json:"nameColorSat"`
	NameVal                int                          `json:"nameColorVal"`
	BgSlideshow            bool                         `json:"bgSlideshow"`
	BgSlideshowSecs        int                          `json:"bgSlideshowSecs"`
	DownloadKBps           int                          `json:"downloadKBps"`
	ForceCharNames         bool                         `json:"forceCharNames"`
	RandomEmote            bool                         `json:"randomEmote"`
	FriendHighlight        bool                         `json:"friendHighlight"`
	ShowFriendButton       bool                         `json:"showFriendButton"`
	ClipSpritesToStage     bool                         `json:"clipSpritesToStage"` // clip character sprites to the stage so an offset can't spill over the chatbox/log (default ON)
	RightClickHideSprite   bool                         `json:"rightClickHideSprite"`
	DragLayout             bool                         `json:"dragLayout"`
	FollowEnabled          bool                         `json:"followEnabled"`
	ShowPairStatus         bool                         `json:"showPairStatus"`
	PlayerListSort         int                          `json:"playerListSort"`     // remembered Players-tab player sort
	PlayerListAreaSort     int                          `json:"playerListAreaSort"` // remembered Players-tab /gas area-group order
	DyslexiaFont           bool                         `json:"dyslexiaFont"`
	FontEverywhere         bool                         `json:"fontEverywhere"` // active font override also drives the chrome (whole UI), not just chat/log
	DNDPersist             bool                         `json:"dndPersist"`
	DNDSaved               bool                         `json:"dndSaved"`
	RainbowMessages        bool                         `json:"rainbowMessages"`
	RandomMessageColor     bool                         `json:"randomMessageColor"`
	RainbowSprites         bool                         `json:"rainbowSprites"`
	ShowRecordButton       bool                         `json:"showRecordButton"`
	InstantDisconnect      bool                         `json:"instantDisconnect"`
	HideDesk               bool                         `json:"hideDesk"`
	FavEmoteBox            bool                         `json:"favEmoteBox"`          // floating box of starred emotes (default OFF)
	InstantReplay          bool                         `json:"instantReplay"`        // always-on rolling clip buffer (default OFF)
	InstantReplaySeconds   int                          `json:"instantReplaySeconds"` // clip capture window; 0 = default
	TimerSeconds           int                          `json:"timerSeconds"`         // local alarm/timer remembered duration; 0 = default (#97)
	TimerRepeat            bool                         `json:"timerRepeat"`          // local alarm/timer auto-restart (default OFF) (#97)
	NotifyOnOOC            bool                         `json:"notifyOnOOC"`          // OOC bumps the unread tab badge (default OFF = IC only)
	ShowSongURL            bool                         `json:"showSongURL"`          // show the full song URL in the music log line (default OFF)
	AutoConnectOnLaunch    bool                         `json:"autoConnectOnLaunch"`
	LastServerName         string                       `json:"lastServerName"`
	LastServerURL          string                       `json:"lastServerURL"`
	RainbowSpriteSpeed     int                          `json:"rainbowSpriteSpeed"`
	ReplayPlaybackSpeed    int                          `json:"replaySpeed"`
	Export                 ExportOptions                `json:"export"`
	MySpriteStyle          SpriteStylePref              `json:"mySpriteStyle"`              // the user's own transmitted sprite style (#103)
	SavedStyles            []StylePreset                `json:"stylePresets,omitempty"`     // #126 saved style+colour+emote moods
	HideSpriteStyles       bool                         `json:"hideSpriteStyles"`           // ignore others' transmitted styles (default OFF = show)
	HideReactions          bool                         `json:"hideReactions"`              // ignore others' transmitted emoji reactions (#2) (default OFF = show)
	CharBundlePrefetch     bool                         `json:"charBundlePrefetch"`         // #127 pre-grab a char's FULL sprite set on load (default OFF)
	PingChip               bool                         `json:"pingChip"`                   // #128 show the connection-quality chip (default OFF)
	ValidateTLSCerts       bool                         `json:"validateTLSCerts"`           // power-user Security toggle: strictly verify wss:// TLS certs. Default OFF = accept self-signed (most community AO servers use them)
	AssetOrigin            string                       `json:"assetOrigin,omitempty"`      // power-user: Origin/Referer header sent on asset fetches (servers that gate their base by CORS); empty = none
	WSOrigin               string                       `json:"wsOrigin,omitempty"`         // power-user: Origin header sent on the WebSocket HANDSHAKE (servers that allowlist their own web client's origin); empty = none
	AssetCharCase          uint8                        `json:"assetCharCase,omitempty"`    // POWER-USER: character-folder casing for the rare capitalised-folder server (0 lowercase default / 1 first-cap / 2 title). A wrong value 404s every character.
	VoiceInputDevice       string                       `json:"voiceInputDevice,omitempty"` // voice chat mic device name; empty = system default
	VoiceOutVolume         int                          `json:"voiceOutVolume,omitempty"`   // voice chat output volume 0..100 (0/absent = default 100)
	PrefetchAggro          int                          `json:"prefetchAggro,omitempty"`    // predictive-prefetch aggressiveness 1..4 (0/absent = 1, conservative) (#100)
	VoicePTTKey            string                       `json:"voicePttKey,omitempty"`      // push-to-talk key name that toggles the mic; empty = unbound
	QuitConfirmSkip        bool                         `json:"quitConfirmSkip,omitempty"`  // "don't ask again" on the quit dialog
	LegacyDevTheme         bool                         `json:"legacyDevTheme"`             // tickbox: revert to the old "developer" look. Default OFF = the new optimal layout is the main theme
	OOCInLogTab            bool                         `json:"oocInLogTab"`                // OOC as a log tab + bottom OOC bar (Legacy-style hybrid); default ON. Off = OOC gets its own box.
	MyProfile              ProfilePref                  `json:"profile"`                    // the user's character profile (#101)
	ChatboxOpacity         int                          `json:"chatboxOpacity"`
	RainbowSpriteVividness int                          `json:"rainbowSpriteVividness"`
	RainbowSpriteGlow      bool                         `json:"rainbowSpriteGlow"`
	RainbowPairDesync      bool                         `json:"rainbowPairDesync"`
	RainbowPerChar         bool                         `json:"rainbowPerChar"`
	SpriteWobble           bool                         `json:"spriteWobble"`
	SpriteSpin             bool                         `json:"spriteSpin"`
	SpriteSolidTint        bool                         `json:"spriteSolidTint"`
	SpriteTintColor        int                          `json:"spriteTintColor"`
	ShoutPunch             bool                         `json:"shoutPunch"`
	ChatboxTint            bool                         `json:"chatboxTint"`
	PostVignette           bool                         `json:"postVignette"`
	PostScanlines          bool                         `json:"postScanlines"`
	PostGrain              bool                         `json:"postGrain"`
	PostCRT                bool                         `json:"postCRT"`
	AnimateEntrances       bool                         `json:"animateEntrances"`
	DepthOfField           bool                         `json:"depthOfField"`
	Spotlight              bool                         `json:"spotlight"`         // #121 dim non-speakers (default OFF)
	SpotlightStrength      int                          `json:"spotlightStrength"` // dim intensity [10,90], 0 = unset → default
	IdleBreath             bool                         `json:"idleBreath"`        // #122 idle breathing (default OFF)
	BreathNoBob            bool                         `json:"breathNoBob"`       // inverted: false = bob component ON (default)
	BreathNoScale          bool                         `json:"breathNoScale"`     // inverted: false = scale component ON (default)
	BreathAmount           int                          `json:"breathAmp"`         // amplitude [1,100], 0 = unset → default
	BreathRate             int                          `json:"breathSpeed"`       // speed [1,100], 0 = unset → default
	Reflection             bool                         `json:"reflection"`        // #123 glass-floor reflection (default OFF)
	ReflectOpacity         int                          `json:"reflectStrength"`   // opacity [0,100], 0 = unset → default
	WeatherKind            int                          `json:"weatherType"`       // #124 ambient weather (0 = None/off)
	WeatherDensity         int                          `json:"weatherIntensity"`  // intensity [1,100], 0 = unset → default
	StageFrameKind         int                          `json:"stageFrame"`        // #56 decorative viewport frame (0 = Off)
	FriendNotify           bool                         `json:"friendNotify"`
	FriendOSToast          bool                         `json:"friendOSToast"`
	CallwordOSToast        bool                         `json:"callwordOSToast"` // #M4 desktop toast on callword
	FriendGlowPulse        bool                         `json:"friendGlowPulse"`
	FriendSound            bool                         `json:"friendSound"`
	FriendSoundFile        string                       `json:"friendSoundFile"`
	ModBanSFX              bool                         `json:"modBanSFX"`
	ModKickSFX             bool                         `json:"modKickSFX"`
	ModMuteSFX             bool                         `json:"modMuteSFX"`
	ModBanSoundFile        string                       `json:"modBanSoundFile"`
	ModKickSoundFile       string                       `json:"modKickSoundFile"`
	ModMuteSoundFile       string                       `json:"modMuteSoundFile"`
	ModcallToast           bool                         `json:"modcallToast"`
	CallwordSoundFile      string                       `json:"callwordSoundFile"`
	DebugOverlay           bool                         `json:"debugOverlay"`
	AutoDetectFormats      bool                         `json:"formatAutoDetect"`
	ThemeLayoutOn          bool                         `json:"themeLayout"`
	ThemeFit               int                          `json:"themeFit"`
	ThemeFitZoom           int                          `json:"themeFitZoom"`
	ThemeFitPanX           int                          `json:"themeFitPanX"`
	ThemeFitPanY           int                          `json:"themeFitPanY"`
	PlainLobby             bool                         `json:"plainLobby"`
	UIScaleAutoOn          bool                         `json:"uiScaleAuto"`
	CatchUpOn              bool                         `json:"catchUpWhenBehind"`
	CatchUpThreshold       int                          `json:"catchUpThreshold"`
	MultiTabCap            int                          `json:"multiTabCap"`
	RestoreTabs            bool                         `json:"restoreTabs"`
	VolStripOn             bool                         `json:"volStripOn"`                     // on-screen volume strip toggle (default OFF)
	ChangelogSeen          string                       `json:"changelogSeenVersion,omitempty"` // last What's New version opened (#23 unread dot)
	SpriteLoadModeVal      int                          `json:"spriteLoadMode"`                 // cold-load sprite behaviour: 0 blank, 1 hold-previous (default), 2 wait (see SpriteLoad*). NOT omitempty: an explicit Blank(0) must persist, not read back as "absent → default".
	SpriteWaitMsVal        int                          `json:"spriteWaitMs,omitempty"`         // wait-mode hold cap in ms (0/absent = SpriteWaitDefaultMs)
	SpriteWaitPair         bool                         `json:"spriteWaitPair,omitempty"`       // wait mode also gates on the PAIR partner's idle sprite (default OFF)
	SpriteWaitPreanim      bool                         `json:"spriteWaitPreanim,omitempty"`    // wait mode also gates on the message's PREANIM (default OFF)
	HoldPrevMaxAgeMsVal    int                          `json:"holdPrevMaxAgeMs,omitempty"`     // hold-previous stand-in cap in ms (0/absent = bridge forever)
	HoldDebugTint          bool                         `json:"holdDebugTint,omitempty"`        // amber-tint stand-in sprites (power-user diagnostics, default OFF)
	ShoutDurationMsVal     int                          `json:"shoutDurationMs,omitempty"`      // shout-bubble hold in ms (0/absent = the canonical default)
	PreanimTimeoutMsVal    int                          `json:"preanimTimeoutMs,omitempty"`     // preanim wait cap in ms (0/absent = the canonical default)
	ICQueueCapVal          int                          `json:"icQueueCap,omitempty"`           // IC backlog queue depth (0/absent = the canonical default 64)
	CatchUpLingerMsVal     int                          `json:"catchUpLingerMs,omitempty"`      // per-message linger while catching up, ms (default 0 = one per frame)
	ThumbCache             bool                         `json:"thumbCache,omitempty"`           // opt-in persistent low-q sprite thumbnail cache (default OFF)
	ThumbHeightPxVal       int                          `json:"thumbHeightPx,omitempty"`        // thumbnail height px (0/absent = 64)
	ThumbQualityVal        int                          `json:"thumbQuality,omitempty"`         // thumbnail webp quality (0/absent = 20)
	ThumbBudgetMiBVal      int                          `json:"thumbBudgetMiB,omitempty"`       // thumbnail store byte budget, MiB (0/absent = 64; auto-prunes oldest)
	DiskCacheBudgetMiBVal  int                          `json:"diskCacheBudgetMiB,omitempty"`   // T3 disk-cache auto-prune cap, MiB (0/absent = UNLIMITED, the default — never silently deletes)
	NotFoundTTLSecVal      int                          `json:"notFoundTTLSec,omitempty"`       // negative-cache (404) TTL in seconds (0/absent = 5 min); applies on RESTART
	AdaptiveLatMultipleVal int                          `json:"adaptiveLatMultiple,omitempty"`  // per-host deadline = N × TTFB EWMA (0/absent = 8)
	SpriteDownscaleOff     bool                         `json:"spriteDownscaleOff,omitempty"`   // disable the automatic decode downscale entirely (default OFF = downscale on)
	FPSCapVal              int                          `json:"fpsCap,omitempty"`               // foreground frame cap (0/absent = ∞/vsync default; -1 = uncapped; positive = a cap)
	IdleFPSVal             int                          `json:"idleFps,omitempty"`              // idle frame rate (0/absent = off default; -1 = uncapped; -2 = never redraw when idle)
	UnfocusedFPSVal        int                          `json:"unfocusedFps,omitempty"`         // unfocused-window frame rate (0/absent = 5 default; -1 = uncapped; -2 = never redraw when unfocused)
	InputGraceFramesVal    int                          `json:"inputGraceFrames,omitempty"`     // full-rate hold after a click/key, in frames (0/absent = default 1)
	EventDrivenLoop        bool                         `json:"eventDrivenLoop"`                // EXPERIMENTAL event-driven render loop (default ON; the kill switch back to classic pacing)
	DisableFrameLimiter    bool                         `json:"disableFrameLimiter,omitempty"`  // #5 bypass: render every frame, NO pacing/skip (vsync only). Default OFF, high GPU. Fresh key.
	MotionRedrawPerEvent   bool                         `json:"motionRedrawPerEvent"`           // event-driven loop: pointer motion renders ONE frame per motion event instead of holding full rate. Default ON (v1.55.1). NOT omitempty: an explicit OFF must persist, not read back as "absent → default ON".
	SpriteDownscalePctVal  int                          `json:"spriteDownscalePct,omitempty"`   // decode downscale target as % of display height (0/absent = 100)
	TexBudgetMiBVal        int                          `json:"texBudgetMiB,omitempty"`         // T1 texture byte budget, MiB (0/absent = 64); applies on RESTART
	CrossfadeMsVal         int                          `json:"crossfadeMs,omitempty"`          // speaker-swap crossfade duration ms (0/absent = off)
	MusicVolMode           bool                         `json:"musicVolMode,omitempty"`         // Music menu shows the volume sliders instead of the track list (persisted)
	OpenTabs               []OpenTab                    `json:"openTabs"`
	ReduceMotionOn         bool                         `json:"reduceMotion"`
	ScreenEffects          bool                         `json:"screenEffects"` // AO2 \s/\f + field shake/flash; default ON
	WordDelete             bool                         `json:"wordDelete"`    // Ctrl+Backspace deletes a word in any text field; default ON
	AdditiveText           bool                         `json:"additiveText"`  // 2.8 additive: honor incoming ADDITIVE=1 append + offer the checkbox; default ON
	MusicDuckingOn         bool                         `json:"musicDucking"`
	PerAreaScroll          bool                         `json:"perAreaScrollback"`
	DetailedLog            bool                         `json:"detailedLog"`
	AutoClipModcall        bool                         `json:"autoClipModcall"`
	GroupChatButton        bool                         `json:"groupChatButton"`
	CharChatbox            bool                         `json:"charChatbox"` // per-character chatbox skins (default ON)
	FontOverridePaths      string                       `json:"fontPaths"`
	UserMacros             []MacroSpec                  `json:"macros,omitempty"`
	ThemeRectOv            map[string]map[string][4]int `json:"themeRectOverrides,omitempty"`
	// ThemeRectRotations holds per-theme, per-widget ROTATION angles for
	// texture-backed themed chrome (A4 — the cheap-tier rotation revamp). Keyed
	// theme → widget key → angle byte (the 360/256 encoding shared with
	// SpriteStyle.Rotation: 0=0°, 64=90°, 128=180°, 192=270°). It mirrors
	// ThemeRectOv's lifecycle exactly (per-theme outer map, bounded by the same
	// caps, dropped alongside the override in ClearThemeRectOverride) — but,
	// unlike ThemeRectOv, its load path IS sanitized (a hand-edited pref can't
	// smuggle in a junk theme/key past the caps). An absent entry means angle 0,
	// which routes through the plain Copy path (byte-identical to the unrotated
	// draw), so this is purely additive.
	ThemeRectRotations map[string]map[string]uint8 `json:"themeRectRotations,omitempty"`
	// ClassicLayout holds per-slot position/size overrides for the DEFAULT
	// (non-themed) courtroom, as window fractions [x,y,w,h] in 0..1 — the live
	// classic-layout editor writes them, slotRect reads them. Fractions keep the
	// drag resolution-independent (unlike the themed editor's design-space ints).
	ClassicLayout map[string][4]float64 `json:"classicLayout,omitempty"`
	// LayoutProfiles are named FULL-STATE snapshots of the courtroom layout the
	// user can save and flip between (A6 — superseded the old LayoutPresets,
	// which only carried the classic slots). Each profile bundles the classic
	// slot overrides, their window anchors, the hidden-chrome set, and the
	// editor snap-grid step, so switching profiles restores the whole
	// arrangement at once. Bounded by layoutProfileCap. Legacy LayoutPresets are
	// migrated in on load (one-way) and never re-saved — a downgrade loses
	// profiles, which is acceptable for this superset.
	LayoutProfiles map[string]LayoutProfile `json:"layoutProfiles,omitempty"`
	// LayoutGridPx is the layout editor's snap-grid step in logical px
	// (playtest, Tifera: "allow us to edit the snap grid"); 0 = the default 8.
	LayoutGridPx int `json:"layoutGridPx,omitempty"`
	// ClassicAnchors pins classic-layout slots to a window corner/edge/centre
	// (playtest, Tifera: "anchor layout items to corners and center of the
	// entire screen"). The slot's ClassicLayout FRACTION override stays the
	// single geometry source; the anchor re-bases it to PIXEL offsets from the
	// anchored reference at resolve time, using the window size the override
	// was saved at (WinW/WinH) — so the box stays glued through window
	// resizes instead of drifting proportionally. Only meaningful alongside a
	// ClassicLayout override; bounded by classicSlotCap like it.
	ClassicAnchors map[string]ClassicAnchor `json:"classicAnchors,omitempty"`
	// ClassicRotations holds per-slot ROTATION angles for texture-backed classic
	// chrome (A4). Keyed slot name → angle byte (the 360/256 encoding shared with
	// SpriteStyle.Rotation). Like ClassicAnchors it is only meaningful alongside a
	// ClassicLayout override for the same slot, so ClearClassicSlot drops it with
	// the override; bounded by classicSlotCap. Absent = angle 0 = the plain,
	// byte-identical Copy path. It also travels inside LayoutProfiles.
	ClassicRotations map[string]uint8 `json:"classicRotations,omitempty"`
	DiskZstd         bool             `json:"diskZstd"`
	StreamerModeOn   bool             `json:"streamerMode"`
	ThemeName        string           `json:"themeName"`
	ThemeDir         string           `json:"themeDir"`
	OOCName          string           `json:"oocName"`
	ViewportPct      int              `json:"viewportPercent"`
	ChatScalePct     int              `json:"chatScalePercent"`
	ChatBoxPct       int              `json:"chatBoxPercent"`
	LogScalePct      int              `json:"logScalePercent"`
	InputHeightPct   int              `json:"inputHeightPercent"`
	UIScalePct       int              `json:"uiScalePercent"`
	WindowW          int              `json:"windowWidth"`  // 0 = default
	WindowH          int              `json:"windowHeight"` // 0 = default
	WindowFull       bool             `json:"windowFullscreen"`
	MusicVol         int              `json:"musicVolume"`
	SFXVol           int              `json:"sfxVolume"`
	BlipVol          int              `json:"blipVolume"`
	AlertVol         int              `json:"alertVolume"`
	MasterVol        int              `json:"masterVolume"` // scales all three (default 100)
	HoldClearOn      bool             `json:"holdClearOn"`  // hold a key to wipe a text field (default on)
	HoldClearKey     string           `json:"holdClearKey"` // which key (default "Backspace"), rebindable
	HoldClearMs      int              `json:"holdClearMs"`  // hold duration to clear (default 1500)
	// Extras-box theming (all hex like "78aaff"; "" = the stock kit colour).
	ExtrasBg           string                    `json:"extrasBg"`
	ExtrasBg2          string                    `json:"extrasBg2"` // gradient bottom colour
	ExtrasBorder       string                    `json:"extrasBorder"`
	ExtrasTitle        string                    `json:"extrasTitle"`
	ExtrasText         string                    `json:"extrasText"`
	ExtrasGradient     bool                      `json:"extrasGradient"`
	AreaHighlightHex   string                    `json:"areaHighlightHex,omitempty"` // current-area row colour ("" = the stock green)
	TextCrawlMs        int                       `json:"textCrawlMs"`
	TextStayMs         int                       `json:"textStayMs"`
	ChatRateLimitMs    int                       `json:"chatRateLimitMs"`
	MasterListURL      string                    `json:"masterListUrl"`
	AssetTypes         map[string]AssetTypePrefs `json:"assetTypes"`
	LearnedFormats     map[string][]string       `json:"learnedFormats"`
	Showname           string                    `json:"showname"`
	ShownamePresets    []string                  `json:"shownamePresets,omitempty"`
	ShownameKeys       map[string]string         `json:"shownameKeys,omitempty"`
	ICPhraseKeys       map[string]string         `json:"icPhraseKeys,omitempty"` // key name → canned IC line (hotkeyed)
	MutedSFX           []string                  `json:"mutedSFX,omitempty"`
	SfxFavorites       []string                  `json:"sfxFavorites,omitempty"`       // #12 starred SFX names (global, bare; preview/use in the SFX Browser)
	ModReasonTemplates []string                  `json:"modReasonTemplates,omitempty"` // editable ban/kick reason chips (seed defaults when empty)
	ModDurations       []string                  `json:"modDurations,omitempty"`       // saved CUSTOM ban-duration chips (canonical short tokens: "45m", "2d"); the enum presets are the defaults
	BlipVols           map[string]int            `json:"blipVolumes,omitempty"`
	EmoteFavs          map[string][]int          `json:"emoteFavorites,omitempty"` // lowercased char -> favourited emote slice indices
	EmoteFavOnly       bool                      `json:"emoteFavOnly"`             // grid shows only favourited emotes (default OFF)
	EmoteFavStars      bool                      `json:"emoteFavStars"`            // show the ★ favourite badge on every emote cell (default OFF — opt-in)
	LocalAssetsEnabled bool                      `json:"localAssetsEnabled"`
	EmoteCaptions      bool                      `json:"emoteCaptions"`         // overlay the emote-name caption on icon-fallback emote buttons (default OFF — clean icons)
	ViewportExactW     int                       `json:"viewportExactW"`        // exact viewport WIDTH in px (0 = size by the View % knob / divider); height derived 4:3. Integer multiples of 256 stay crisp.
	OOCScalePct        int                       `json:"oocScalePercent"`       // OOC log text size, INDEPENDENT of the IC log (logScalePercent); 0 = inherit the IC log size once (legacy configs), then diverges
	CustomChromeHex    [7]string                 `json:"customChrome"`          // user "Custom" chrome scheme: hex rrggbb per kit colour (bg,panel,panelHi,accent,text,textDim,danger); blank slot = stock dark. Active only when ChromeTheme=="custom".
	LayoutPartHex      [4]string                 `json:"layoutPartColors"`      // per-layout-part panel tints (v1.52.0): hex rrggbb for log/OOC/emotes/chatbox, blank = chrome default (count pinned by LayoutPartColorCount)
	BoldNamesOff       bool                      `json:"boldNamesOff"`          // speaker names in the IC/OOC log + chatbox are BOLD by default (readability); set to opt OUT (stored inverted so absent = bold on)
	BlipRate           int                       `json:"blipRate"`              // play one chat blip per N revealed letters (default 2 = Ace Attorney style; 1 = every letter)
	BlipOnSpaces       bool                      `json:"blipOnSpaces"`          // also blip on spaces (default OFF = skip whitespace)
	CallwordsOOC       bool                      `json:"callwordsOOC"`          // also alert on callwords in OOC messages (default OFF — IC only, avoids /ga & chatter pings)
	ExtProfiles        map[string]string         `json:"extProfiles,omitempty"` // per asset-host extensions.json override (format profile); seeded instantly on connect, takes precedence over the fetched server manifest + the global default
	LocalAssetsPaths   []string                  `json:"localAssetsPaths"`
	Favorites          []FavoriteServer          `json:"favorites"`
	Wardrobe           []string                  `json:"wardrobe"`
	CasingEnabled      bool                      `json:"casingEnabled"`
	CasingRoles        int                       `json:"casingRoles"`
	HiddenPanelIDs     []string                  `json:"hiddenPanels"`
	ServerWarm         map[string]ServerWarmInfo `json:"serverWarm"`
	CallWordList       []string                  `json:"callWords"`
	HotkeyMap          map[string]string         `json:"hotkeys"`
	DiscordRPC         DiscordPrefs              `json:"discord"`
	MyAutoStatus       AutoStatusPref            `json:"autoStatus"`            // #M1 auto-status from typed words
	ChromeThemeKey     string                    `json:"chromeTheme,omitempty"` // #M3 AsyncAO chrome theme preset
	// ChromeShapeKey selects the CHROME SHAPE preset (A5 — parallel to
	// ChromeThemeKey's colour presets, orthogonal to it): "sharp" (default =
	// today's flat Fill+Border silhouette, byte-identical), "rounded", "pill".
	// It reshapes AsyncAO's own kit chrome (buttons/chips/panels) via
	// procedural alpha masks drawn 9-slice; hit-testing stays the same rect. An
	// unknown key sanitises to "sharp", so an absent/garbage value renders
	// exactly like today. ChromeShapeTierIdx picks the corner-radius size class
	// (0..shapeRadiusTiers-1) for the "rounded" preset; "pill"/"sharp" ignore it.
	ChromeShapeKey     string `json:"chromeShape,omitempty"`
	ChromeShapeTierIdx int    `json:"chromeShapeTier,omitempty"`
	// CharDownloaderOn enables the opt-in single-character/background
	// downloader (off by default — it writes files to disk on demand).
	CharDownloaderOn bool `json:"charDownloader"`

	// ToolboxSeen latches once the user first expands the compact bottom-right
	// toolbox (A1). While false, the collapsed grip wears a faint accent
	// discoverability ring; the first expand sets it true and the ring stops.
	// Default false (zero value) = "not seen yet", so a brand-new config shows
	// the ring exactly once. NOT in resetContentFields, so ResetSettings clears it
	// back to false — the ring simply re-shows once after a settings reset, which
	// is fine (a settings reset is a "fresh start", so re-teaching the toolbox fits).
	ToolboxSeen bool `json:"toolboxSeen"`

	// ShowAssetWarnings governs the red on-screen "Missing asset" banner
	// (OFF by default — the failures still reach the debug overlay).
	ShowAssetWarnings bool `json:"showAssetWarnings"`
	// SpriteMoveOn enables click-drag sprite repositioning (OFF by default).
	SpriteMoveOn bool `json:"spriteMove"`
	// DeskFollowManifest lets desks adopt the server extensions.json format;
	// OFF by default keeps desks on WebP regardless of the manifest.
	DeskFollowManifest bool `json:"deskFollowManifest"`
	// SpritePreviewOn shows the hover-preview pop-up (ON by default).
	SpritePreviewOn bool `json:"spritePreview"`
	// PreviewHoverMs is the hover dwell before the preview shows (ms).
	PreviewHoverMs int `json:"previewHoverMs"`
	// PreviewHeightPxVal is the preview box's DEFAULT height in px (0 = the
	// shipped DefaultPreviewHeightPx); the corner grip still resizes per
	// session on top of it.
	PreviewHeightPxVal int `json:"previewHeightPx"`
	// AutoLoginToast notifies (in-app toast + desktop notification) when a
	// saved auto-login fires on join (ON by default).
	AutoLoginToast bool `json:"autoLoginToast"`
	// CallwordToast shows an in-app toast when a callword is heard (ON by
	// default), alongside the existing flash + ping.
	CallwordToast bool `json:"callwordToast"`
	// MessageCounter shows a live character count by the IC input (ON by default).
	MessageCounter bool `json:"messageCounter"`
	// MentionSelf treats your character name / showname as a callword (#203); OFF
	// by default. Powers the "alert me when someone says my name" behaviour.
	MentionSelf bool `json:"mentionSelf"`
	// LoopPreanim keeps a preanimation WRAPPING while it stays on stage instead of
	// playing once and settling (OFF by default). This is DELIBERATELY
	// NON-CANONICAL — AO2-Client plays preanims strictly play-once
	// (animationlayer.cpp forces setPlayOnce(true) for PreEmote) — so it defaults
	// OFF and the shipped behaviour matches AO2. Message timing is unchanged either
	// way (the render fires preanim-done exactly once regardless).
	LoopPreanim bool `json:"loopPreanim"`
	// ICTimestamps prefixes each IC log line with its local arrival time (OFF by default).
	ICTimestamps bool `json:"icTimestamps"`
	// AutoReconnect auto-retries the last server after an unexpected drop (ON by default).
	AutoReconnect bool `json:"autoReconnect"`
	// MusicHistory keeps the session "recently played" jukebox list (ON by default).
	MusicHistory bool `json:"musicHistory"`
	// MusicStreaming fetches and plays custom /play tracks (ON by default). OFF =
	// AsyncAO never downloads a /play song (the user's own plays go silent locally
	// too — they round-trip through the server). NOT omitempty: an explicit OFF
	// must persist, not read back as "absent → default ON".
	MusicStreaming bool `json:"musicStreaming"`
	// MusicAcrossTabs keeps a backgrounded tab's music AUDIBLE across a server-tab
	// switch instead of ducking it to silence (default OFF). The single music
	// stream always follows the active tab; with this OFF a backgrounded tab's
	// still-rolling stream is ducked to volume 0 (tabs stay acoustically isolated,
	// position preserved). ON = you keep hearing the previous tab's music while
	// browsing another that has none. Default OFF (plain bool, no omitempty).
	MusicAcrossTabs bool `json:"musicAcrossTabs"`
	// ShowMissingPlaceholder draws the AO2 "placeholder" (missingno) for a
	// conclusively-missing character sprite (ON by default). NOT omitempty: an
	// explicit OFF must persist, not read back as "absent → default ON".
	ShowMissingPlaceholder bool `json:"showMissingPlaceholder"`
	// ErrorSpriteFile is an optional user-chosen image used INSTEAD of the embedded
	// missingno placeholder ("" = the embedded default). Loaded off-thread and pinned
	// over the placeholder key; a bad path falls back to the default with an inline
	// Settings error. (The accessor is ErrorSpritePath(); the field name differs to
	// avoid the Go method/field name clash, mirroring FontOverridePaths/FontPaths().)
	ErrorSpriteFile string `json:"errorSpritePath,omitempty"`
	// MusicHosts allowlists the domains whose /play links the music history
	// records — for "unique" user-hosted song domains (catbox/file.garden/etc.).
	// Server-hosted music (bare names, the server's own host) still plays but is
	// never recorded. Editable in Settings; seeded with a sensible default.
	MusicHosts []string `json:"musicHosts,omitempty"`

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

	// quarantine, when non-nil, records that an EXISTING preferences file
	// failed to parse at load and was renamed aside (to BackupPath) BEFORE
	// the saver could overwrite the only copy with defaults. It is never
	// serialized (unexported) and exists solely so the UI can surface a
	// one-time startup notice via Quarantine(). See load().
	quarantine *Quarantine

	// formatGen increments on every mutation that changes any effective
	// probe list (format orders, fallback toggles). Consumers cache derived
	// format tables keyed by this generation — see Resolver's miss path.
	formatGen atomic.Uint64

	// wardrobeGen increments on every change to any server's wardrobe
	// membership. The char-select star grid caches a lowercased membership
	// set keyed by this generation, so the per-cell lookup stays lock-free.
	wardrobeGen atomic.Uint64
}

// prefsJSON mirrors the on-disk shape for loading. Pointer fields distinguish
// "absent" from the zero value where the default is not the zero value.
type prefsJSON struct {
	GlobalFallbacksEnabled bool             `json:"globalFallbacksEnabled"`
	PreferAnimated         *bool            `json:"preferAnimated"`
	EmoteButtonImages      *bool            `json:"emoteButtonImages"`
	SmoothScaling          *bool            `json:"smoothScaling"`
	UpdateCheck            *bool            `json:"updateCheck"`        // absent = default ON
	UpdateExperimental     bool             `json:"updateExperimental"` // default OFF (opt-in test channel)
	HighlightColor         *int             `json:"highlightColor"`     // absent = default accent
	ICCustomColor          *int             `json:"icCustomColor"`      // absent = defaultICCustomColor
	BgSlideshow            bool             `json:"bgSlideshow"`        // default OFF (zero value)
	BgSlideshowSecs        int              `json:"bgSlideshowSecs"`
	DownloadKBps           int              `json:"downloadKBps"`         // 0 = unlimited (default)
	ForceCharNames         bool             `json:"forceCharNames"`       // default OFF
	RandomEmote            bool             `json:"randomEmote"`          // default OFF
	FriendHighlight        bool             `json:"friendHighlight"`      // default OFF
	ShowFriendButton       *bool            `json:"showFriendButton"`     // default ON (pointer: absent != off)
	ClipSpritesToStage     *bool            `json:"clipSpritesToStage"`   // clip sprites to the stage (default ON; pointer: absent != off)
	RightClickHideSprite   *bool            `json:"rightClickHideSprite"` // default ON (pointer: absent != off)
	DragLayout             *bool            `json:"dragLayout"`           // default ON (pointer: absent != off)
	FollowEnabled          bool             `json:"followEnabled"`        // default OFF (opt-in)
	ShowPairStatus         bool             `json:"showPairStatus"`       // #20 default OFF (opt-in)
	PlayerListSort         int              `json:"playerListSort"`       // default 0 (UID)
	PlayerListAreaSort     int              `json:"playerListAreaSort"`   // default 0 (/gas order)
	DyslexiaFont           bool             `json:"dyslexiaFont"`         // default OFF
	FontEverywhere         bool             `json:"fontEverywhere"`       // default OFF (chat/log only)
	DNDPersist             bool             `json:"dndPersist"`           // default OFF (DND clears each launch)
	DNDSaved               bool             `json:"dndSaved"`             // persisted DND state (restored only when DNDPersist)
	RainbowMessages        bool             `json:"rainbowMessages"`      // default OFF
	RandomMessageColor     bool             `json:"randomMessageColor"`   // default OFF
	RainbowSprites         bool             `json:"rainbowSprites"`       // default OFF
	ShowRecordButton       bool             `json:"showRecordButton"`     // default OFF
	InstantDisconnect      bool             `json:"instantDisconnect"`    // default OFF (confirm first)
	HideDesk               bool             `json:"hideDesk"`             // default OFF
	FavEmoteBox            bool             `json:"favEmoteBox"`          // default OFF
	InstantReplay          bool             `json:"instantReplay"`        // default OFF
	InstantReplaySeconds   int              `json:"instantReplaySeconds"` // 0 = default window
	TimerSeconds           int              `json:"timerSeconds"`         // 0 = default (#97)
	TimerRepeat            bool             `json:"timerRepeat"`          // default OFF (#97)
	NotifyOnOOC            bool             `json:"notifyOnOOC"`          // default OFF (IC-only badge)
	MusicAcrossTabs        bool             `json:"musicAcrossTabs"`      // default OFF (background tab music ducks to 0)
	ShowSongURL            bool             `json:"showSongURL"`          // default OFF (song name only)
	AutoConnectOnLaunch    bool             `json:"autoConnectOnLaunch"`  // default OFF
	LastServerName         string           `json:"lastServerName"`
	LastServerURL          string           `json:"lastServerURL"`
	RainbowSpriteSpeed     *int             `json:"rainbowSpriteSpeed"`         // absent = default
	ReplayPlaybackSpeed    *int             `json:"replaySpeed"`                // absent = default
	Export                 *ExportOptions   `json:"export"`                     // absent = default
	MySpriteStyle          *SpriteStylePref `json:"mySpriteStyle"`              // absent = no style (#103)
	SavedStyles            []StylePreset    `json:"stylePresets,omitempty"`     // #126 saved moods
	HideSpriteStyles       bool             `json:"hideSpriteStyles"`           // default OFF (show others' styles)
	HideReactions          bool             `json:"hideReactions"`              // default OFF (show others' reactions, #2)
	CharBundlePrefetch     bool             `json:"charBundlePrefetch"`         // #127 default OFF
	PingChip               bool             `json:"pingChip"`                   // #128 default OFF
	ValidateTLSCerts       bool             `json:"validateTLSCerts"`           // Security: strict wss cert check; default OFF (accept self-signed)
	AssetOrigin            string           `json:"assetOrigin,omitempty"`      // Security: Origin/Referer override for asset fetches
	WSOrigin               string           `json:"wsOrigin,omitempty"`         // Security: Origin override for the WS handshake
	VoiceInputDevice       string           `json:"voiceInputDevice,omitempty"` // voice mic device ("" = default)
	VoiceOutVolume         int              `json:"voiceOutVolume,omitempty"`   // voice output volume (0 = default 100)
	PrefetchAggro          int              `json:"prefetchAggro,omitempty"`    // predictive-prefetch aggressiveness 1..4 (#100)
	VoicePTTKey            string           `json:"voicePttKey,omitempty"`      // push-to-talk toggle key
	QuitConfirmSkip        bool             `json:"quitConfirmSkip,omitempty"`  // "don't ask again" on quit
	LegacyDevTheme         bool             `json:"legacyDevTheme"`             // tickbox revert to the old look; default OFF = new layout
	OOCInLogTab            bool             `json:"oocInLogTab"`                // OOC as a log tab + bottom OOC bar; default ON (Off = OOC box)
	Profile                *ProfilePref     `json:"profile"`                    // absent = no profile (#101)
	ChatboxOpacity         *int             `json:"chatboxOpacity"`             // absent = default (0 is valid → pointer)
	RainbowSpriteVividness *int             `json:"rainbowSpriteVividness"`     // absent = default (0 is valid → pointer)
	RainbowSpriteGlow      bool             `json:"rainbowSpriteGlow"`          // default OFF
	RainbowPairDesync      bool             `json:"rainbowPairDesync"`          // default OFF
	RainbowPerChar         bool             `json:"rainbowPerChar"`             // default OFF
	SpriteWobble           bool             `json:"spriteWobble"`               // default OFF
	SpriteSpin             bool             `json:"spriteSpin"`                 // default OFF
	SpriteSolidTint        bool             `json:"spriteSolidTint"`            // default OFF
	ShoutPunch             bool             `json:"shoutPunch"`                 // default OFF
	ChatboxTint            bool             `json:"chatboxTint"`                // default OFF
	PostVignette           bool             `json:"postVignette"`               // default OFF
	PostScanlines          bool             `json:"postScanlines"`              // default OFF
	PostGrain              bool             `json:"postGrain"`                  // default OFF
	PostCRT                bool             `json:"postCRT"`                    // #77 default OFF
	AnimateEntrances       bool             `json:"animateEntrances"`           // default OFF
	DepthOfField           bool             `json:"depthOfField"`               // default OFF
	Spotlight              bool             `json:"spotlight"`                  // #121 default OFF
	SpotlightStrength      int              `json:"spotlightStrength"`          // 0 = unset → default
	IdleBreath             bool             `json:"idleBreath"`                 // #122 default OFF
	BreathNoBob            bool             `json:"breathNoBob"`                // false = bob ON
	BreathNoScale          bool             `json:"breathNoScale"`              // false = scale ON
	BreathAmount           int              `json:"breathAmp"`                  // 0 = unset → default
	BreathRate             int              `json:"breathSpeed"`                // 0 = unset → default
	Reflection             bool             `json:"reflection"`                 // #123 default OFF
	ReflectOpacity         int              `json:"reflectStrength"`            // 0 = unset → default
	WeatherKind            int              `json:"weatherType"`                // #124 0 = None/off
	WeatherDensity         int              `json:"weatherIntensity"`           // 0 = unset → default
	StageFrameKind         int              `json:"stageFrame"`                 // #56 0 = Off
	SpriteTintColor        *int             `json:"spriteTintColor"`            // absent = default
	FriendNotify           bool             `json:"friendNotify"`               // default OFF
	FriendOSToast          bool             `json:"friendOSToast"`              // default OFF
	CallwordOSToast        bool             `json:"callwordOSToast"`            // #M4 default OFF
	MentionSelf            bool             `json:"mentionSelf"`                // #203 default OFF
	LoopPreanim            bool             `json:"loopPreanim"`                // default OFF (non-canonical)
	FriendGlowPulse        bool             `json:"friendGlowPulse"`            // default OFF
	FriendSound            bool             `json:"friendSound"`                // default OFF
	FriendSoundFile        string           `json:"friendSoundFile"`
	ModBanSFX              bool             `json:"modBanSFX"`        // default OFF
	ModKickSFX             bool             `json:"modKickSFX"`       // default OFF
	ModMuteSFX             bool             `json:"modMuteSFX"`       // default OFF
	ModBanSoundFile        string           `json:"modBanSoundFile"`  // "" = built-in default
	ModKickSoundFile       string           `json:"modKickSoundFile"` // "" = built-in default
	ModMuteSoundFile       string           `json:"modMuteSoundFile"` // "" = built-in default
	ModcallToast           bool             `json:"modcallToast"`     // default OFF
	CallwordSoundFile      string           `json:"callwordSoundFile"`
	DebugOverlay           bool             `json:"debugOverlay"`
	FormatAutoDetect       *bool            `json:"formatAutoDetect"` // absent = default ON
	ThemeLayout            *bool            `json:"themeLayout"`      // absent = default ON
	ThemeFit               int              `json:"themeFit"`         // 0 = Stretch (default)
	ThemeFitZoom           int              `json:"themeFitZoom"`     // 0 (absent) = default 100
	ThemeFitPanX           int              `json:"themeFitPanX"`
	ThemeFitPanY           int              `json:"themeFitPanY"`
	PlainLobby             *bool            `json:"plainLobby"`           // absent = default ON
	UIScaleAuto            *bool            `json:"uiScaleAuto"`          // absent = default ON (HiDPI)
	CatchUpWhenBehind      *bool            `json:"catchUpWhenBehind"`    // absent = default ON
	CatchUpThreshold       *int             `json:"catchUpThreshold"`     // absent = default
	MultiTabCap            *int             `json:"multiTabCap"`          // absent = default
	NameColors             bool             `json:"nameColors"`           // default OFF (zero value)
	NameColorSat           *int             `json:"nameColorSat"`         // absent = default
	NameColorVal           *int             `json:"nameColorVal"`         // absent = default
	RestoreTabs            bool             `json:"restoreTabs"`          // default OFF (zero value)
	VolStripOn             bool             `json:"volStripOn"`           // on-screen volume strip toggle (default OFF)
	ChangelogSeen          string           `json:"changelogSeenVersion"` // last What's New version opened (#23)
	SpriteLoadMode         *int             `json:"spriteLoadMode"`       // cold-load sprite behaviour (0 blank, 1 hold-previous, 2 wait); absent = default hold-previous (pointer distinguishes "unset" from an explicit Blank(0))
	SpriteWaitMs           int              `json:"spriteWaitMs"`         // wait-mode hold cap in ms (0 = default)
	SpriteWaitPair         bool             `json:"spriteWaitPair"`       // wait mode gates on the pair too (default OFF)
	SpriteWaitPreanim      bool             `json:"spriteWaitPreanim"`    // wait mode gates on the preanim too (default OFF)
	HoldPrevMaxAgeMs       int              `json:"holdPrevMaxAgeMs"`     // hold-previous cap in ms (0 = forever)
	HoldDebugTint          bool             `json:"holdDebugTint"`        // tint stand-in sprites (default OFF)
	ShoutDurationMs        int              `json:"shoutDurationMs"`      // shout hold in ms (0 = default)
	PreanimTimeoutMs       int              `json:"preanimTimeoutMs"`     // preanim cap in ms (0 = default)
	ICQueueCap             int              `json:"icQueueCap"`           // IC queue depth (0 = default 64)
	CatchUpLingerMs        int              `json:"catchUpLingerMs"`      // catch-up per-message linger ms (default 0)
	ThumbCache             bool             `json:"thumbCache"`           // low-q sprite thumbnail cache (default OFF)
	ThumbHeightPx          int              `json:"thumbHeightPx"`        // thumb height px (0 = 64)
	ThumbQuality           int              `json:"thumbQuality"`         // thumb webp quality (0 = 20)
	ThumbBudgetMiB         int              `json:"thumbBudgetMiB"`       // thumb store budget MiB (0 = 64)
	DiskCacheBudgetMiB     int              `json:"diskCacheBudgetMiB"`   // T3 disk-cache prune cap MiB (0 = unlimited, default)
	NotFoundTTLSec         int              `json:"notFoundTTLSec"`       // 404 TTL seconds (0 = default; restart)
	AdaptiveLatMultiple    int              `json:"adaptiveLatMultiple"`  // deadline multiple (0 = 8)
	SpriteDownscaleOff     bool             `json:"spriteDownscaleOff"`   // disable decode downscale (default OFF)
	FPSCap                 int              `json:"fpsCap"`               // foreground frame cap (0 = ∞/vsync default; -1 = uncapped)
	IdleFPS                int              `json:"idleFps"`              // idle frame rate (0 = off default; -1 = uncapped; -2 = off)
	UnfocusedFPS           int              `json:"unfocusedFps"`         // unfocused frame rate (0 = 5 default; -1 = uncapped; -2 = off)
	InputGraceFrames       int              `json:"inputGraceFrames"`     // full-rate hold after input, in frames (0 = default 1)
	EventDrivenLoop        *bool            `json:"eventDrivenLoop"`      // experimental event-driven loop (default ON; pointer: absent != off)
	DisableFrameLimiter    bool             `json:"disableFrameLimiter"`  // #5 bypass: no pacing/skip at all (default OFF)
	MotionRedrawPerEvent   *bool            `json:"motionRedrawPerEvent"` // per-event motion redraw (default ON as of v1.55.1; pointer: absent → the default, distinct from an explicit OFF)
	SpriteDownscalePct     int              `json:"spriteDownscalePct"`   // downscale % of display height (0 = 100)
	TexBudgetMiB           int              `json:"texBudgetMiB"`         // T1 budget MiB (0 = 64; restart)
	CrossfadeMs            int              `json:"crossfadeMs"`          // speaker-swap crossfade ms (0 = off)
	MusicVolMode           bool             `json:"musicVolMode"`         // Music menu volume-sliders view (persisted)
	OpenTabs               []OpenTab        `json:"openTabs"`             // remembered tabs for restore-on-launch
	ReduceMotion           bool             `json:"reduceMotion"`         // default OFF (zero value)
	ScreenEffects          *bool            `json:"screenEffects"`        // absent = default ON
	WordDelete             *bool            `json:"wordDelete"`           // absent = default ON (pointer: an explicit OFF must persist)
	AdditiveText           *bool            `json:"additiveText"`         // absent = default ON (pointer: an explicit OFF must persist)
	MusicDucking           bool             `json:"musicDucking"`         // default OFF (zero value)
	PerAreaScrollback      bool             `json:"perAreaScrollback"`    // default OFF (zero value)
	DetailedLog            bool             `json:"detailedLog"`          // default OFF (zero value)
	AutoClipModcall        *bool            `json:"autoClipModcall"`      // default ON (pointer: absent != off)
	GroupChatButton        *bool            `json:"groupChatButton"`      // default ON (pointer: absent != off)
	CharChatbox            *bool            `json:"charChatbox"`          // default ON (pointer: absent != off)

	FontPaths          string                           `json:"fontPaths"` // ""=embedded font
	Macros             []MacroSpec                      `json:"macros"`
	ThemeRectOverrides map[string]map[string][4]int     `json:"themeRectOverrides"`
	ThemeRectRotations map[string]map[string]uint8      `json:"themeRectRotations"` // per-theme, per-widget rotation angles (A4) — sanitized on load
	ClassicLayout      map[string][4]float64            `json:"classicLayout"`      // default-courtroom slot overrides (window fractions)
	LayoutPresets      map[string]map[string][4]float64 `json:"layoutPresets"`      // LEGACY named layout snapshots (#34) — read-only migration source into LayoutProfiles; never written back
	LayoutProfiles     map[string]LayoutProfile         `json:"layoutProfiles"`     // full-state named layout profiles (A6)
	LayoutGridPx       int                              `json:"layoutGridPx"`       // editor snap-grid step (0 = default 8)
	ClassicAnchors     map[string]ClassicAnchor         `json:"classicAnchors"`     // per-slot window pins (mode + saved window size)
	ClassicRotations   map[string]uint8                 `json:"classicRotations"`   // per-slot rotation angles (A4)
	DiskZstd           bool                             `json:"diskZstd"`           // default OFF (measured trade)
	StreamerMode       bool                             `json:"streamerMode"`       // default OFF
	ThemeName          string                           `json:"themeName"`
	ThemeDir           string                           `json:"themeDir"`
	OOCName            string                           `json:"oocName"`
	ViewportPct        int                              `json:"viewportPercent"`
	ChatScalePct       int                              `json:"chatScalePercent"`
	ChatBoxPct         int                              `json:"chatBoxPercent"`
	LogScalePct        int                              `json:"logScalePercent"`
	InputHeightPct     int                              `json:"inputHeightPercent"`
	UIScalePct         int                              `json:"uiScalePercent"`
	WindowW            int                              `json:"windowWidth"`
	WindowH            int                              `json:"windowHeight"`
	WindowFull         *bool                            `json:"windowFullscreen"` // absent = default OFF
	// Volumes use pointers: 0 is a real value (mute), absent means 100.
	MusicVol     *int   `json:"musicVolume"`
	SFXVol       *int   `json:"sfxVolume"`
	BlipVol      *int   `json:"blipVolume"`
	AlertVol     *int   `json:"alertVolume"`
	MasterVol    *int   `json:"masterVolume"`
	HoldClearOn  *bool  `json:"holdClearOn"` // absent = default ON
	HoldClearKey string `json:"holdClearKey"`
	HoldClearMs  int    `json:"holdClearMs"`
	// Extras-box theming (hex; "" = stock colour). Gradient: absent = default OFF.
	ExtrasBg         string `json:"extrasBg"`
	ExtrasBg2        string `json:"extrasBg2"`
	ExtrasBorder     string `json:"extrasBorder"`
	ExtrasTitle      string `json:"extrasTitle"`
	ExtrasText       string `json:"extrasText"`
	ExtrasGradient   *bool  `json:"extrasGradient"`
	AreaHighlightHex string `json:"areaHighlightHex"` // "" = the stock green
	// Stay/ratelimit use pointers too: 0 means "no linger" / "off".
	TextCrawlMs        int                       `json:"textCrawlMs"`
	TextStayMs         *int                      `json:"textStayMs"`
	ChatRateLimitMs    *int                      `json:"chatRateLimitMs"`
	MasterListURL      string                    `json:"masterListUrl"`
	AssetTypes         map[string]AssetTypePrefs `json:"assetTypes"`
	LearnedFormats     map[string][]string       `json:"learnedFormats"`
	Showname           string                    `json:"showname"`
	ShownamePresets    []string                  `json:"shownamePresets,omitempty"`
	ShownameKeys       map[string]string         `json:"shownameKeys,omitempty"`
	ICPhraseKeys       map[string]string         `json:"icPhraseKeys,omitempty"` // key name → canned IC line (hotkeyed)
	MutedSFX           []string                  `json:"mutedSFX,omitempty"`
	SfxFavorites       []string                  `json:"sfxFavorites,omitempty"`       // #12 starred SFX names (global, bare; preview/use in the SFX Browser)
	ModReasonTemplates []string                  `json:"modReasonTemplates,omitempty"` // editable ban/kick reason chips (seed defaults when empty)
	ModDurations       []string                  `json:"modDurations,omitempty"`       // saved CUSTOM ban-duration chips (canonical short tokens: "45m", "2d"); the enum presets are the defaults
	BlipVols           map[string]int            `json:"blipVolumes,omitempty"`
	EmoteFavs          map[string][]int          `json:"emoteFavorites,omitempty"` // lowercased char -> favourited emote slice indices
	EmoteFavOnly       bool                      `json:"emoteFavOnly"`             // grid shows only favourited emotes (default OFF)
	EmoteFavStars      bool                      `json:"emoteFavStars"`            // show the ★ favourite badge on every emote cell (default OFF — opt-in)
	LocalAssetsEnabled bool                      `json:"localAssetsEnabled"`
	EmoteCaptions      bool                      `json:"emoteCaptions"`         // overlay the emote-name caption on icon-fallback emote buttons (default OFF — clean icons)
	ViewportExactW     int                       `json:"viewportExactW"`        // exact viewport WIDTH in px (0 = size by the View % knob / divider); height derived 4:3. Integer multiples of 256 stay crisp.
	OOCScalePct        int                       `json:"oocScalePercent"`       // OOC log text size, INDEPENDENT of the IC log (logScalePercent); 0 = inherit the IC log size once (legacy configs), then diverges
	CustomChromeHex    [7]string                 `json:"customChrome"`          // user "Custom" chrome scheme: hex rrggbb per kit colour (bg,panel,panelHi,accent,text,textDim,danger); blank slot = stock dark. Active only when ChromeTheme=="custom".
	LayoutPartHex      [4]string                 `json:"layoutPartColors"`      // per-layout-part panel tints (v1.52.0): hex rrggbb for log/OOC/emotes/chatbox, blank = chrome default (count pinned by LayoutPartColorCount)
	BoldNamesOff       bool                      `json:"boldNamesOff"`          // speaker names in the IC/OOC log + chatbox are BOLD by default (readability); set to opt OUT (stored inverted so absent = bold on)
	BlipRate           int                       `json:"blipRate"`              // play one chat blip per N revealed letters (default 2 = Ace Attorney style; 1 = every letter)
	BlipOnSpaces       bool                      `json:"blipOnSpaces"`          // also blip on spaces (default OFF = skip whitespace)
	CallwordsOOC       bool                      `json:"callwordsOOC"`          // also alert on callwords in OOC messages (default OFF — IC only, avoids /ga & chatter pings)
	ExtProfiles        map[string]string         `json:"extProfiles,omitempty"` // per asset-host extensions.json override (format profile); seeded instantly on connect, takes precedence over the fetched server manifest + the global default
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
	AutoStatus         *AutoStatusPref           `json:"autoStatus,omitempty"`
	ChromeTheme        *string                   `json:"chromeTheme,omitempty"`
	ChromeShape        *string                   `json:"chromeShape,omitempty"`     // A5: nil = absent (defaults to "sharp"); non-nil sanitised on load
	ChromeShapeTier    *int                      `json:"chromeShapeTier,omitempty"` // A5: nil = absent (defaults to tier 0); clamped [0,shapeRadiusTiers-1] on load
	CharDownloader     bool                      `json:"charDownloader"`
	ToolboxSeen        bool                      `json:"toolboxSeen"` // A1: default OFF (zero value) = show the toolbox discoverability ring until first expand

	ShowAssetWarnings  bool     `json:"showAssetWarnings"`    // default OFF (zero value)
	SpriteMove         bool     `json:"spriteMove"`           // default OFF (zero value)
	DeskFollowManifest bool     `json:"deskFollowManifest"`   // default OFF (zero value)
	SpritePreview      *bool    `json:"spritePreview"`        // absent = default ON
	PreviewHoverMs     *int     `json:"previewHoverMs"`       // absent = default 5 s
	PreviewHeightPx    int      `json:"previewHeightPx"`      // 0/absent = shipped default (384)
	AutoLoginToast     *bool    `json:"autoLoginToast"`       // absent = default ON
	CallwordToast      *bool    `json:"callwordToast"`        // absent = default ON
	MessageCounter     *bool    `json:"messageCounter"`       // absent = default ON
	ICTimestamps       *bool    `json:"icTimestamps"`         // absent = default OFF
	AutoReconnect      *bool    `json:"autoReconnect"`        // absent = default ON
	MusicHistory       *bool    `json:"musicHistory"`         // absent = default ON
	MusicStreaming     *bool    `json:"musicStreaming"`       // absent = default ON
	MusicHosts         []string `json:"musicHosts,omitempty"` // absent = default list

	ShowMissingPlaceholder *bool  `json:"showMissingPlaceholder"`    // absent = default ON
	ErrorSpritePath        string `json:"errorSpritePath,omitempty"` // "" = embedded default
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
	// AppID is retained for back-compat with older saved prefs only; the dial
	// now uses the baked-in DefaultDiscordAppID and the field is no longer
	// user-editable (the Settings box was removed).
	AppID string `json:"appId,omitempty"`
}

// DefaultDiscordAppID is the official AsyncAO Discord application (registered
// under the maintainer's account; icon asset "appicon"). It is baked in so Rich
// Presence works out of the box — users toggle presence on/off in Settings but
// never the app identity. Dialed directly (see cmd/asyncao), so it applies even
// to existing saved prefs whose AppID predates the bake.
const DefaultDiscordAppID = "1519625107188744222"

// defaultDiscordPrefs: ON by default on a normal (Discord-capable) build, with
// the detail toggles pre-set. Rich Presence is pure-stdlib IPC (no DLL); it
// silently no-ops when Discord isn't running, and users who don't want it can
// flip it off in Settings or run a `-tags nodiscord` build. A saved prefs file
// keeps whatever the user last chose (this default only seeds fresh installs).
func defaultDiscordPrefs() DiscordPrefs {
	return DiscordPrefs{Enabled: true, ShowServer: true, ShowChar: true, ShowName: true, AppID: DefaultDiscordAppID}
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
	// Per-server audio override — the "sandbox each tab's sound" option. When AudioOn,
	// these 0–100 levels replace the global mixer volumes while this server's tab is
	// active (so you can mute blips on one server and keep them on another); off = the
	// shared global volumes. AudioMaster scales the rest. The channels are POINTERS so
	// "never set on this server" (nil) is distinct from "muted" (explicit 0): a nil
	// channel falls back to the global default (so a channel like SFX can't be silently
	// muted just because a profile exists — only an explicit 0 mutes). omitempty omits a
	// nil pointer; a non-nil &0 (a deliberate mute) is kept.
	AudioOn     bool `json:"audioOn,omitempty"`
	AudioMaster *int `json:"audioMaster,omitempty"`
	AudioMusic  *int `json:"audioMusic,omitempty"`
	AudioSFX    *int `json:"audioSFX,omitempty"`
	AudioBlip   *int `json:"audioBlip,omitempty"`
	// Chars is the server's character list from the last visit
	// (≤ WarmCharsCap) — the rehearsal char select.
	Chars []string `json:"chars,omitempty"`
	// Backgrounds is the server's discovered background list from the last
	// visit (≤ WarmBgsCap) — seeds the picker/slideshow INSTANTLY on the next
	// connect (like the cached char list), then a fresh autoindex fetch
	// refreshes it in the background.
	Backgrounds []string `json:"backgrounds,omitempty"`
	// Friends are this server's highlighted shownames (≤ WarmFriendsCap):
	// their IC messages glow, and (later slices) can ping/notify. Per server,
	// so your friend list on one server doesn't bleed into another.
	Friends []string `json:"friends,omitempty"`
	// Ignored is this server's blocked shownames (≤ WarmIgnoredCap): their IC
	// AND OOC messages are dropped at ingest — no log line, no sprite, no blip
	// (#81). Matched by showname-else-character, the only identity the MS wire
	// carries (UID isn't in the packet). Per server, like Friends.
	Ignored []string `json:"ignored,omitempty"`
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

// WarmBgsCap bounds one server's remembered background list (same budget as
// the char list — the megaservers have comparable background counts).
const WarmBgsCap = 4096

// WarmFriendsCap bounds one server's highlighted-showname (friend) list.
const WarmFriendsCap = 256

// WarmIgnoredCap bounds one server's ignored-showname (block) list.
const WarmIgnoredCap = 256

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

// OpenTab is one remembered server tab for restore-on-launch (M7): the display
// name and ws URL, enough to reconnect via App.Connect.
type OpenTab struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// DefaultPath returns the preferences file location. It is portable-first: a
// config set beside the executable (<exeDir>/config/) wins, otherwise the
// classic OS config dir (<os.UserConfigDir>/AsyncAO/) — see ConfigBaseDir.
func DefaultPath() (string, error) {
	dir, err := ConfigBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, PrefsFileName), nil
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

// defaultPrefs builds a fresh, all-defaults AssetPreferences (no disk read, no
// saver goroutine). Both load() (before overlaying the file) and the reset
// methods use it, so the default state lives in exactly one place.
func defaultPrefs(path string) *AssetPreferences {
	return &AssetPreferences{
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
		UpdateCheck:            defaultUpdateCheck,
		ShowAssetWarnings:      defaultShowAssetWarnings,
		SpriteMoveOn:           defaultSpriteMove,
		DeskFollowManifest:     defaultDeskFollowManifest,
		SpritePreviewOn:        defaultSpritePreview,
		PreviewHoverMs:         DefaultPreviewHoverMs,
		AutoLoginToast:         defaultAutoLoginToast,
		ScreenEffects:          defaultScreenEffects,
		WordDelete:             defaultWordDelete,
		AdditiveText:           defaultAdditiveText,
		CallwordToast:          defaultCallwordToast,
		MessageCounter:         defaultMessageCounter,
		ICTimestamps:           defaultICTimestamps,
		AutoReconnect:          defaultAutoReconnect,
		MusicHistory:           defaultMusicHistory,
		MusicStreaming:         defaultMusicStreaming,
		ShowMissingPlaceholder: defaultShowMissingPlaceholder,
		MusicHosts:             defaultMusicHostList(),
		HighlightColor:         defaultHighlightColor,
		ICCustomColor:          defaultICCustomColor,
		NameSat:                defaultNameColorSat,
		NameVal:                defaultNameColorVal,
		BgSlideshowSecs:        defaultBgSlideshowSecs,
		AutoDetectFormats:      defaultFormatAutoDetect,
		BlipRate:               defaultBlipRate,
		ThemeLayoutOn:          defaultThemeLayout,
		ThemeFit:               defaultThemeFit,
		ThemeFitZoom:           DefaultThemeZoom,
		PlainLobby:             defaultPlainLobby,
		UIScaleAutoOn:          defaultUIScaleAuto,
		CatchUpOn:              defaultCatchUpWhenBehind,
		CatchUpThreshold:       DefaultCatchUpThreshold,
		MultiTabCap:            DefaultMultiTabCap,
		SpriteLoadModeVal:      defaultSpriteLoadMode,       // webAO-style hold-previous by default (kills the cold-load flash)
		MotionRedrawPerEvent:   defaultMotionRedrawPerEvent, // per-event motion redraw ON by default (less GPU on a moving cursor)
		DiscordRPC:             defaultDiscordPrefs(),
		MyAutoStatus:           defaultAutoStatusPref(),
		ChromeThemeKey:         "dark",
		ChromeShapeKey:         defaultChromeShape, // A5: "sharp" — byte-identical to today's flat chrome
		ViewportPct:            DefaultViewportPercent,
		ChatScalePct:           DefaultScalePercent,
		ChatBoxPct:             DefaultScalePercent,
		LogScalePct:            DefaultScalePercent,
		InputHeightPct:         DefaultScalePercent,
		UIScalePct:             DefaultScalePercent,
		OOCInLogTab:            true, // default OOC = a log tab + bottom OOC bar (Legacy-style, hybrid); the OOC-box layout is opt-out

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
		path:            path,
	}
}

// Quarantine records that an existing preferences file was unparseable at load
// and was renamed aside (BackupPath) before defaults could overwrite it. Err is
// the underlying parse error. The caller (cmd/asyncao + UI) surfaces a one-time
// startup notice from it; internal/config stays UI-free. Nil unless a corrupt
// file was successfully quarantined.
type Quarantine struct {
	// BackupPath is the full path the corrupt file was renamed to, or "" if
	// the rename itself failed (the app still boots on defaults, but no backup
	// name can be shown).
	BackupPath string
	// Err is the parse error that triggered the quarantine.
	Err error
}

// Quarantine returns the corrupt-file quarantine record from load, or nil if the
// preferences file parsed cleanly (or was absent). Read once at startup.
func (p *AssetPreferences) Quarantine() *Quarantine { return p.quarantine }

// load reads and normalizes the preferences file without starting the saver.
func load(path string) (*AssetPreferences, error) {
	p := defaultPrefs(path)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil // first run
	}
	if err != nil {
		return p, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var onDisk prefsJSON
	if err := json.Unmarshal(data, &onDisk); err != nil {
		// A malformed EXISTING file: quarantine it (rename aside) BEFORE
		// returning defaults, so the debounced saver can never clobber the
		// only copy of the user's favourites/logins/macros/learned formats.
		// The rename runs on the startup load path (no saver exists yet, no
		// render/decode path) — cheap synchronous I/O is fine here (spec §2
		// bars sync I/O on hot paths only). See item #3.
		perr := fmt.Errorf("config: parsing %s (using defaults): %w", path, err)
		backup := path + corruptSuffixPrefix + time.Now().Format(corruptStampLayout)
		if renameErr := os.Rename(path, backup); renameErr == nil {
			p.quarantine = &Quarantine{BackupPath: backup, Err: perr}
		} else {
			// Rename failed (locked/permissions): don't advertise a backup
			// path that doesn't exist. The app still boots on defaults; the
			// saver will eventually overwrite, but that is no worse than the
			// pre-fix behaviour and rare.
			log.Printf("config: could not quarantine corrupt %s: %v", path, renameErr)
			p.quarantine = &Quarantine{Err: perr}
		}
		return p, perr
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
	p.UpdateExperimental = onDisk.UpdateExperimental
	if onDisk.HighlightColor != nil {
		p.HighlightColor = *onDisk.HighlightColor & 0xFFFFFF
	}
	if onDisk.ICCustomColor != nil {
		p.ICCustomColor = *onDisk.ICCustomColor & 0xFFFFFF
	}
	p.BgSlideshow = onDisk.BgSlideshow
	if onDisk.BgSlideshowSecs > 0 { // 0 (absent) keeps the New() default
		p.BgSlideshowSecs = onDisk.BgSlideshowSecs
	}
	p.DownloadKBps = onDisk.DownloadKBps // 0 = unlimited
	p.ForceCharNames = onDisk.ForceCharNames
	p.RandomEmote = onDisk.RandomEmote
	p.FriendHighlight = onDisk.FriendHighlight
	if onDisk.RightClickHideSprite != nil { // pointer: absent keeps the default-ON
		p.RightClickHideSprite = *onDisk.RightClickHideSprite
	}
	if onDisk.ShowFriendButton != nil { // pointer: absent keeps the default-ON
		p.ShowFriendButton = *onDisk.ShowFriendButton
	}
	if onDisk.ClipSpritesToStage != nil { // pointer: absent keeps the default-ON
		p.ClipSpritesToStage = *onDisk.ClipSpritesToStage
	}
	if onDisk.DragLayout != nil { // pointer: absent keeps the default-ON
		p.DragLayout = *onDisk.DragLayout
	}
	p.FollowEnabled = onDisk.FollowEnabled
	p.ShowPairStatus = onDisk.ShowPairStatus
	p.PlayerListSort = onDisk.PlayerListSort
	p.PlayerListAreaSort = onDisk.PlayerListAreaSort
	p.DyslexiaFont = onDisk.DyslexiaFont
	p.FontEverywhere = onDisk.FontEverywhere
	p.DNDPersist = onDisk.DNDPersist
	p.DNDSaved = onDisk.DNDSaved
	p.RainbowMessages = onDisk.RainbowMessages
	p.RandomMessageColor = onDisk.RandomMessageColor
	p.RainbowSprites = onDisk.RainbowSprites
	p.ShowRecordButton = onDisk.ShowRecordButton
	p.InstantDisconnect = onDisk.InstantDisconnect
	p.HideDesk = onDisk.HideDesk
	p.FavEmoteBox = onDisk.FavEmoteBox
	p.InstantReplay = onDisk.InstantReplay
	p.InstantReplaySeconds = onDisk.InstantReplaySeconds
	p.TimerSeconds = onDisk.TimerSeconds
	p.TimerRepeat = onDisk.TimerRepeat
	p.NotifyOnOOC = onDisk.NotifyOnOOC
	p.MusicAcrossTabs = onDisk.MusicAcrossTabs
	p.ShowSongURL = onDisk.ShowSongURL
	p.AutoConnectOnLaunch = onDisk.AutoConnectOnLaunch
	p.LastServerName = onDisk.LastServerName
	p.LastServerURL = onDisk.LastServerURL
	if onDisk.RainbowSpriteSpeed != nil {
		p.RainbowSpriteSpeed = clampPercent(*onDisk.RainbowSpriteSpeed, minRainbowSpeed, maxRainbowSpeed)
	}
	if onDisk.ReplayPlaybackSpeed != nil {
		p.ReplayPlaybackSpeed = clampPercent(*onDisk.ReplayPlaybackSpeed, minReplaySpeed, maxReplaySpeed)
	}
	if onDisk.Export != nil {
		// Start from defaults and overlay valid on-disk fields, so a partial
		// hand-edited export object can't zero-out the unspecified knobs.
		e := defaultExportOptions()
		if onDisk.Export.HeightPx > 0 {
			e.HeightPx = clampPercent(onDisk.Export.HeightPx, minExportHeight, maxExportHeight)
		}
		if onDisk.Export.FPS > 0 {
			e.FPS = clampPercent(onDisk.Export.FPS, minExportFPS, maxExportFPS)
		}
		if onDisk.Export.Quality > 0 {
			e.Quality = clampPercent(onDisk.Export.Quality, minExportQuality, maxExportQuality)
		}
		if onDisk.Export.TextScale > 0 {
			e.TextScale = clampPercent(onDisk.Export.TextScale, minExportText, maxExportText)
		}
		if onDisk.Export.VideoFormat != "" {
			e.VideoFormat = normalizeVideoFormat(onDisk.Export.VideoFormat)
		}
		e.Loop = onDisk.Export.Loop
		p.Export = e
	}
	if onDisk.MySpriteStyle != nil {
		s := *onDisk.MySpriteStyle
		s.Opacity = clampOpacity(s.Opacity)
		p.MySpriteStyle = s
	}
	p.SavedStyles = sanitizeStylePresets(onDisk.SavedStyles) // #126 (bounded + opacity-clamped)
	p.HideSpriteStyles = onDisk.HideSpriteStyles
	p.HideReactions = onDisk.HideReactions
	p.CharBundlePrefetch = onDisk.CharBundlePrefetch
	p.PingChip = onDisk.PingChip
	p.ValidateTLSCerts = onDisk.ValidateTLSCerts
	p.AssetOrigin = strings.TrimSpace(onDisk.AssetOrigin)
	p.WSOrigin = strings.TrimSpace(onDisk.WSOrigin)
	p.VoiceInputDevice = onDisk.VoiceInputDevice
	p.VoiceOutVolume = onDisk.VoiceOutVolume
	p.PrefetchAggro = onDisk.PrefetchAggro
	p.VoicePTTKey = onDisk.VoicePTTKey
	p.QuitConfirmSkip = onDisk.QuitConfirmSkip
	p.LegacyDevTheme = onDisk.LegacyDevTheme
	p.OOCInLogTab = onDisk.OOCInLogTab
	if onDisk.Profile != nil {
		p.MyProfile = clampProfile(*onDisk.Profile)
	}
	if onDisk.ChatboxOpacity != nil {
		p.ChatboxOpacity = clampPercent(*onDisk.ChatboxOpacity, MinChatboxOpacity, MaxChatboxOpacity)
	}
	if onDisk.RainbowSpriteVividness != nil {
		p.RainbowSpriteVividness = clampPercent(*onDisk.RainbowSpriteVividness, minRainbowVivid, maxRainbowVivid)
	}
	p.RainbowSpriteGlow = onDisk.RainbowSpriteGlow
	p.RainbowPairDesync = onDisk.RainbowPairDesync
	p.RainbowPerChar = onDisk.RainbowPerChar
	p.SpriteWobble = onDisk.SpriteWobble
	p.SpriteSpin = onDisk.SpriteSpin
	p.SpriteSolidTint = onDisk.SpriteSolidTint
	p.ShoutPunch = onDisk.ShoutPunch
	p.ChatboxTint = onDisk.ChatboxTint
	p.PostVignette = onDisk.PostVignette
	p.PostScanlines = onDisk.PostScanlines
	p.PostGrain = onDisk.PostGrain
	p.PostCRT = onDisk.PostCRT
	p.AnimateEntrances = onDisk.AnimateEntrances
	p.DepthOfField = onDisk.DepthOfField
	p.Spotlight = onDisk.Spotlight
	p.SpotlightStrength = onDisk.SpotlightStrength
	p.IdleBreath = onDisk.IdleBreath
	p.BreathNoBob = onDisk.BreathNoBob
	p.BreathNoScale = onDisk.BreathNoScale
	p.BreathAmount = onDisk.BreathAmount
	p.BreathRate = onDisk.BreathRate
	p.Reflection = onDisk.Reflection
	p.ReflectOpacity = onDisk.ReflectOpacity
	p.WeatherKind = onDisk.WeatherKind
	p.WeatherDensity = onDisk.WeatherDensity
	p.StageFrameKind = onDisk.StageFrameKind
	if onDisk.SpriteTintColor != nil {
		p.SpriteTintColor = *onDisk.SpriteTintColor & 0xFFFFFF
	}
	p.FriendNotify = onDisk.FriendNotify
	p.FriendOSToast = onDisk.FriendOSToast
	p.CallwordOSToast = onDisk.CallwordOSToast
	p.MentionSelf = onDisk.MentionSelf
	p.LoopPreanim = onDisk.LoopPreanim
	p.FriendGlowPulse = onDisk.FriendGlowPulse
	p.FriendSound = onDisk.FriendSound
	p.FriendSoundFile = onDisk.FriendSoundFile
	p.ModBanSFX = onDisk.ModBanSFX
	p.ModKickSFX = onDisk.ModKickSFX
	p.ModMuteSFX = onDisk.ModMuteSFX
	p.ModBanSoundFile = onDisk.ModBanSoundFile
	p.ModKickSoundFile = onDisk.ModKickSoundFile
	p.ModMuteSoundFile = onDisk.ModMuteSoundFile
	p.ModcallToast = onDisk.ModcallToast
	p.CallwordSoundFile = onDisk.CallwordSoundFile
	p.DebugOverlay = onDisk.DebugOverlay
	p.CharDownloaderOn = onDisk.CharDownloader
	p.ToolboxSeen = onDisk.ToolboxSeen // A1 discoverability latch; absent (old config) = false = show the ring once
	p.ShowAssetWarnings = onDisk.ShowAssetWarnings
	p.SpriteMoveOn = onDisk.SpriteMove
	p.DeskFollowManifest = onDisk.DeskFollowManifest
	if onDisk.SpritePreview != nil {
		p.SpritePreviewOn = *onDisk.SpritePreview
	}
	if onDisk.PreviewHoverMs != nil {
		p.PreviewHoverMs = clampPercent(*onDisk.PreviewHoverMs, minPreviewHoverMs, maxPreviewHoverMs)
	}
	if onDisk.PreviewHeightPx > 0 { // 0 (absent) keeps the shipped default
		p.PreviewHeightPxVal = clampPercent(onDisk.PreviewHeightPx, MinPreviewHeightPx, MaxPreviewHeightPx)
	}
	if onDisk.AutoLoginToast != nil {
		p.AutoLoginToast = *onDisk.AutoLoginToast
	}
	if onDisk.ScreenEffects != nil {
		p.ScreenEffects = *onDisk.ScreenEffects
	}
	if onDisk.WordDelete != nil {
		p.WordDelete = *onDisk.WordDelete
	}
	if onDisk.AdditiveText != nil {
		p.AdditiveText = *onDisk.AdditiveText
	}
	if onDisk.CallwordToast != nil {
		p.CallwordToast = *onDisk.CallwordToast
	}
	if onDisk.MessageCounter != nil {
		p.MessageCounter = *onDisk.MessageCounter
	}
	if onDisk.ICTimestamps != nil {
		p.ICTimestamps = *onDisk.ICTimestamps
	}
	if onDisk.AutoReconnect != nil {
		p.AutoReconnect = *onDisk.AutoReconnect
	}
	if onDisk.MusicHistory != nil {
		p.MusicHistory = *onDisk.MusicHistory
	}
	if onDisk.MusicStreaming != nil {
		p.MusicStreaming = *onDisk.MusicStreaming
	}
	if onDisk.ShowMissingPlaceholder != nil {
		p.ShowMissingPlaceholder = *onDisk.ShowMissingPlaceholder
	}
	p.ErrorSpriteFile = strings.TrimSpace(onDisk.ErrorSpritePath) // "" = embedded default
	if onDisk.MusicHosts != nil {
		p.MusicHosts = sanitizeMusicHosts(onDisk.MusicHosts)
	}
	if onDisk.FormatAutoDetect != nil {
		p.AutoDetectFormats = *onDisk.FormatAutoDetect
	}
	if onDisk.BlipRate != 0 { // 0 (absent) keeps the default-2 cadence
		p.BlipRate = clampPercent(onDisk.BlipRate, MinBlipRate, MaxBlipRate)
	}
	p.BlipOnSpaces = onDisk.BlipOnSpaces
	p.CallwordsOOC = onDisk.CallwordsOOC
	if onDisk.ExtProfiles != nil {
		p.ExtProfiles = onDisk.ExtProfiles
	}
	if onDisk.ThemeLayout != nil {
		p.ThemeLayoutOn = *onDisk.ThemeLayout
	}
	p.ThemeFit = clampPercent(onDisk.ThemeFit, ThemeFitStretch, ThemeFitCustom)
	if onDisk.ThemeFitZoom != 0 { // 0 (absent) keeps the default
		p.ThemeFitZoom = clampPercent(onDisk.ThemeFitZoom, MinThemeZoom, MaxThemeZoom)
	}
	p.ThemeFitPanX = clampPercent(onDisk.ThemeFitPanX, -MaxThemePan, MaxThemePan)
	p.ThemeFitPanY = clampPercent(onDisk.ThemeFitPanY, -MaxThemePan, MaxThemePan)
	if onDisk.PlainLobby != nil {
		p.PlainLobby = *onDisk.PlainLobby
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
	p.NameColors = onDisk.NameColors
	if onDisk.NameColorSat != nil {
		p.NameSat = clampPercent(*onDisk.NameColorSat, 0, 100)
	}
	if onDisk.NameColorVal != nil {
		p.NameVal = clampPercent(*onDisk.NameColorVal, minNameColorVal, 100)
	}
	p.RestoreTabs = onDisk.RestoreTabs
	p.VolStripOn = onDisk.VolStripOn
	if onDisk.SpriteLoadMode != nil { // absent = the defaultPrefs default (hold-previous); a present value is the user's explicit pick (incl. Blank)
		p.SpriteLoadModeVal = *onDisk.SpriteLoadMode
		if p.SpriteLoadModeVal < SpriteLoadBlank || p.SpriteLoadModeVal >= SpriteLoadModeCount {
			p.SpriteLoadModeVal = defaultSpriteLoadMode // a garbage / newer value falls back to the current default
		}
	}
	p.SpriteWaitMsVal = onDisk.SpriteWaitMs
	if p.SpriteWaitMsVal != 0 { // 0 = "use the default"; a set value clamps into range
		p.SpriteWaitMsVal = clampPercent(p.SpriteWaitMsVal, SpriteWaitMinMs, SpriteWaitMaxMs)
	}
	p.SpriteWaitPair = onDisk.SpriteWaitPair
	p.SpriteWaitPreanim = onDisk.SpriteWaitPreanim
	p.HoldPrevMaxAgeMsVal = onDisk.HoldPrevMaxAgeMs
	if p.HoldPrevMaxAgeMsVal != 0 { // 0 = bridge forever; a set value clamps into range
		p.HoldPrevMaxAgeMsVal = clampPercent(p.HoldPrevMaxAgeMsVal, HoldPrevMaxAgeMinMs, HoldPrevMaxAgeMaxMs)
	}
	p.HoldDebugTint = onDisk.HoldDebugTint
	p.ShoutDurationMsVal = onDisk.ShoutDurationMs
	if p.ShoutDurationMsVal != 0 { // 0 = the canonical default
		p.ShoutDurationMsVal = clampPercent(p.ShoutDurationMsVal, ShoutDurationMinMs, ShoutDurationMaxMs)
	}
	p.PreanimTimeoutMsVal = onDisk.PreanimTimeoutMs
	if p.PreanimTimeoutMsVal != 0 { // 0 = the canonical default
		p.PreanimTimeoutMsVal = clampPercent(p.PreanimTimeoutMsVal, PreanimTimeoutMinMs, PreanimTimeoutMaxMs)
	}
	p.ICQueueCapVal = onDisk.ICQueueCap
	if p.ICQueueCapVal != 0 { // 0 = the canonical default (64)
		p.ICQueueCapVal = clampPercent(p.ICQueueCapVal, ICQueueCapMin, ICQueueCapMax)
	}
	p.CatchUpLingerMsVal = clampPercent(onDisk.CatchUpLingerMs, 0, CatchUpLingerMaxMs) // default IS zero, so no sentinel
	p.ThumbCache = onDisk.ThumbCache
	p.ThumbHeightPxVal = onDisk.ThumbHeightPx
	if p.ThumbHeightPxVal != 0 { // 0 = the shipped default
		p.ThumbHeightPxVal = clampPercent(p.ThumbHeightPxVal, ThumbHeightMinPx, ThumbHeightMaxPx)
	}
	p.ThumbQualityVal = onDisk.ThumbQuality
	if p.ThumbQualityVal != 0 {
		p.ThumbQualityVal = clampPercent(p.ThumbQualityVal, ThumbQualityMin, ThumbQualityMax)
	}
	p.ThumbBudgetMiBVal = onDisk.ThumbBudgetMiB
	if p.ThumbBudgetMiBVal != 0 {
		p.ThumbBudgetMiBVal = clampPercent(p.ThumbBudgetMiBVal, ThumbBudgetMinMiB, ThumbBudgetMaxMiB)
	}
	p.DiskCacheBudgetMiBVal = onDisk.DiskCacheBudgetMiB
	if p.DiskCacheBudgetMiBVal != 0 { // 0 = unlimited (the default); only a positive cap is clamped
		p.DiskCacheBudgetMiBVal = clampPercent(p.DiskCacheBudgetMiBVal, DiskCacheBudgetMinMiB, DiskCacheBudgetMaxMiB)
	}
	p.NotFoundTTLSecVal = onDisk.NotFoundTTLSec
	if p.NotFoundTTLSecVal != 0 {
		p.NotFoundTTLSecVal = clampPercent(p.NotFoundTTLSecVal, NotFoundTTLMinSec, NotFoundTTLMaxSec)
	}
	p.AdaptiveLatMultipleVal = onDisk.AdaptiveLatMultiple
	if p.AdaptiveLatMultipleVal != 0 {
		p.AdaptiveLatMultipleVal = clampPercent(p.AdaptiveLatMultipleVal, AdaptiveLatMultipleMin, AdaptiveLatMultipleMax)
	}
	p.SpriteDownscaleOff = onDisk.SpriteDownscaleOff
	p.SpriteDownscalePctVal = onDisk.SpriteDownscalePct
	if p.SpriteDownscalePctVal != 0 {
		p.SpriteDownscalePctVal = clampPercent(p.SpriteDownscalePctVal, SpriteDownscaleMinPct, SpriteDownscaleMaxPct)
	}
	p.FPSCapVal = normalizeFPSPref(onDisk.FPSCap, FPSCapMin, FPSCapMax)
	p.IdleFPSVal = normalizeFPSPref(onDisk.IdleFPS, IdleFPSMin, IdleFPSMax)
	p.UnfocusedFPSVal = normalizeFPSPref(onDisk.UnfocusedFPS, UnfocusedFPSMin, UnfocusedFPSMax)
	p.InputGraceFramesVal = onDisk.InputGraceFrames
	if p.InputGraceFramesVal > 0 { // 0 = the default, <0 = the off sentinel; a positive value clamps
		p.InputGraceFramesVal = clampPercent(p.InputGraceFramesVal, InputGraceFramesMin, InputGraceFramesMax)
	} else if p.InputGraceFramesVal < 0 {
		p.InputGraceFramesVal = InputGraceOff // normalize any stored negative to the canonical off
	}
	if onDisk.EventDrivenLoop != nil { // pointer: absent keeps the default-ON
		p.EventDrivenLoop = *onDisk.EventDrivenLoop
	}
	p.DisableFrameLimiter = onDisk.DisableFrameLimiter // default-OFF plain bool (absent = off)
	if onDisk.MotionRedrawPerEvent != nil {            // absent = the defaultPrefs default (ON); a present value is the user's explicit pick (incl. OFF)
		p.MotionRedrawPerEvent = *onDisk.MotionRedrawPerEvent
	}
	p.TexBudgetMiBVal = onDisk.TexBudgetMiB
	if p.TexBudgetMiBVal != 0 {
		p.TexBudgetMiBVal = clampPercent(p.TexBudgetMiBVal, TexBudgetMinMiB, TexBudgetMaxMiB)
	}
	p.CrossfadeMsVal = onDisk.CrossfadeMs
	if p.CrossfadeMsVal != 0 {
		p.CrossfadeMsVal = clampPercent(p.CrossfadeMsVal, CrossfadeMinMs, CrossfadeMaxMs)
	}
	p.MusicVolMode = onDisk.MusicVolMode
	p.ChangelogSeen = onDisk.ChangelogSeen
	p.OpenTabs = onDisk.OpenTabs
	p.ReduceMotionOn = onDisk.ReduceMotion
	p.MusicDuckingOn = onDisk.MusicDucking
	p.PerAreaScroll = onDisk.PerAreaScrollback
	p.DetailedLog = onDisk.DetailedLog
	if onDisk.AutoClipModcall != nil { // pointer: absent keeps the default-ON
		p.AutoClipModcall = *onDisk.AutoClipModcall
	}
	if onDisk.GroupChatButton != nil { // pointer: absent keeps the default-ON
		p.GroupChatButton = *onDisk.GroupChatButton
	}
	if onDisk.CharChatbox != nil { // pointer: absent keeps the default-ON
		p.CharChatbox = *onDisk.CharChatbox
	}
	p.FontOverridePaths = onDisk.FontPaths
	p.UserMacros = sanitizeMacros(onDisk.Macros)
	p.ThemeRectOv = onDisk.ThemeRectOverrides
	// Unlike ThemeRectOv (the one historically-unsanitized layout axis), the
	// rotation side-map IS sanitized on load (A4) so a hand-edited pref can't
	// smuggle in an over-cap theme/key.
	p.ThemeRectRotations = sanitizeThemeRectRotations(onDisk.ThemeRectRotations)
	p.ClassicLayout = sanitizeClassicLayout(onDisk.ClassicLayout)
	p.LayoutProfiles = sanitizeLayoutProfiles(onDisk.LayoutProfiles)
	// One-way migration: a file written by an older build carries the legacy
	// LayoutPresets (classic slots only) but no LayoutProfiles. Wrap each
	// sanitized preset into a Classic-only profile so nothing is lost. Skipped
	// once any profile exists (a newer file already migrated / the user saved
	// one) — profiles are never written back to the presets key.
	if len(p.LayoutProfiles) == 0 && len(onDisk.LayoutPresets) > 0 {
		if legacy := sanitizeLayoutPresets(onDisk.LayoutPresets); len(legacy) > 0 {
			p.LayoutProfiles = make(map[string]LayoutProfile, len(legacy))
			for name, m := range legacy {
				// Trim the legacy key so the migrated profile is keyed
				// consistently with SaveLayoutProfile (which trims too); a
				// preset whose name was only whitespace is dropped rather than
				// creating an un-nameable, un-deletable profile.
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				p.LayoutProfiles[name] = LayoutProfile{Classic: m}
			}
		}
	}
	p.LayoutGridPx = clampLayoutGridPx(onDisk.LayoutGridPx)
	p.ClassicAnchors = sanitizeClassicAnchors(onDisk.ClassicAnchors)
	p.ClassicRotations = sanitizeClassicRotations(onDisk.ClassicRotations)
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
	// OOC log text size is independent of the IC log. Legacy configs (no value)
	// inherit the IC log size once so the OOC box doesn't visibly jump on upgrade;
	// thereafter the two diverge as each is zoomed on its own.
	if onDisk.OOCScalePct != 0 {
		p.OOCScalePct = clampPercent(onDisk.OOCScalePct, MinLogScalePercent, MaxLogScalePercent)
	} else {
		p.OOCScalePct = p.LogScalePct
	}
	p.CustomChromeHex = onDisk.CustomChromeHex // hex strings; validated (parsed/ignored) at apply time
	p.LayoutPartHex = onDisk.LayoutPartHex     // same contract: parsed/ignored at apply time, blank = default
	p.BoldNamesOff = onDisk.BoldNamesOff       // inverted: absent (false) = bold names ON
	if onDisk.InputHeightPct != 0 {
		p.InputHeightPct = clampPercent(onDisk.InputHeightPct, MinInputPercent, MaxInputPercent)
	}
	if onDisk.UIScalePct != 0 {
		p.UIScalePct = clampPercent(onDisk.UIScalePct, MinUIScalePercent, MaxUIScalePercent)
	}
	if onDisk.WindowW > 0 {
		p.WindowW = onDisk.WindowW
	}
	if onDisk.WindowH > 0 {
		p.WindowH = onDisk.WindowH
	}
	if onDisk.WindowFull != nil {
		p.WindowFull = *onDisk.WindowFull
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
	if onDisk.AlertVol != nil {
		p.AlertVol = clampPercent(*onDisk.AlertVol, 0, defaultAudioVolume)
	}
	if onDisk.MasterVol != nil {
		p.MasterVol = clampPercent(*onDisk.MasterVol, 0, defaultAudioVolume)
	}
	if onDisk.HoldClearOn != nil {
		p.HoldClearOn = *onDisk.HoldClearOn
	}
	if onDisk.HoldClearKey != "" {
		p.HoldClearKey = onDisk.HoldClearKey
	}
	if onDisk.HoldClearMs != 0 {
		p.HoldClearMs = clampPercent(onDisk.HoldClearMs, MinHoldClearMs, MaxHoldClearMs)
	}
	p.ExtrasBg, p.ExtrasBg2 = onDisk.ExtrasBg, onDisk.ExtrasBg2
	p.ExtrasBorder, p.ExtrasTitle, p.ExtrasText = onDisk.ExtrasBorder, onDisk.ExtrasTitle, onDisk.ExtrasText
	p.AreaHighlightHex = onDisk.AreaHighlightHex
	if onDisk.ExtrasGradient != nil {
		p.ExtrasGradient = *onDisk.ExtrasGradient
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
		// One-shot default migration: Misc's default grew from [webp] to
		// [png, webp] (chatbox skins mix both in the wild — one mirror
		// serves chat.png and chatbox.webp pack by pack; the webp-only
		// default was the emote class bleeding over). A stored order that
		// still equals the OLD default was never user-customized — advance
		// it. Anything else is a deliberate choice and stays.
		if name == TypeMisc && len(tp.FormatOrder) == 1 && tp.FormatOrder[0] == ExtWebP {
			tp.FormatOrder = DefaultFormatOrder(name)
		}
		p.AssetTypes[name] = tp
	}
	if onDisk.LearnedFormats != nil {
		p.LearnedFormats = onDisk.LearnedFormats
	}
	p.Showname = onDisk.Showname
	p.ShownamePresets = onDisk.ShownamePresets
	p.ShownameKeys = onDisk.ShownameKeys
	p.ICPhraseKeys = onDisk.ICPhraseKeys
	p.MutedSFX = onDisk.MutedSFX
	p.SfxFavorites = onDisk.SfxFavorites
	p.ModReasonTemplates = onDisk.ModReasonTemplates
	p.ModDurations = onDisk.ModDurations
	p.BlipVols = onDisk.BlipVols
	p.EmoteFavs = onDisk.EmoteFavs
	p.EmoteFavOnly = onDisk.EmoteFavOnly
	p.EmoteFavStars = onDisk.EmoteFavStars
	p.EmoteCaptions = onDisk.EmoteCaptions
	p.ViewportExactW = clampViewportExactPx(onDisk.ViewportExactW)
	p.LocalAssetsEnabled = onDisk.LocalAssetsEnabled
	p.LocalAssetsPaths = onDisk.LocalAssetsPaths
	p.Favorites = onDisk.Favorites
	if len(onDisk.Wardrobe) > WardrobeCap {
		onDisk.Wardrobe = onDisk.Wardrobe[:WardrobeCap]
	}
	p.Wardrobe = onDisk.Wardrobe
	p.CasingEnabled = onDisk.CasingEnabled
	p.CasingRoles = onDisk.CasingRoles
	p.HiddenPanelIDs = sanitizeHiddenPanels(onDisk.HiddenPanels)
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
	if onDisk.AutoStatus != nil {
		p.MyAutoStatus = sanitizeAutoStatus(*onDisk.AutoStatus)
	}
	if onDisk.ChromeTheme != nil {
		p.ChromeThemeKey = *onDisk.ChromeTheme
	}
	// A5 chrome shape: an unknown/garbage key sanitises to "sharp" and the tier
	// clamps to a valid size class, so a hand-edited or downgraded pref always
	// resolves to a coherent (worst case: the byte-identical default) shape.
	if onDisk.ChromeShape != nil {
		p.ChromeShapeKey = sanitizeChromeShape(*onDisk.ChromeShape)
	}
	if onDisk.ChromeShapeTier != nil {
		p.ChromeShapeTierIdx = clampChromeShapeTier(*onDisk.ChromeShapeTier)
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

// resetContentFields names the user-CONTENT fields ResetSettings PRESERVES
// (everything else — the tunable settings — reverts to default). ResetAll
// preserves nothing. Keyed by Go field name (checked against the struct by
// TestResetFieldNames so a rename can't silently break it).
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
	"Showname":           true, // your identity
	"OOCName":            true,
}

// resetLocked overwrites every exported field with a fresh-defaults value,
// skipping unexported machinery (mu/saver/path — PkgPath != "") and any name in
// keep. Caller holds the write lock. Reflection (a rare, cold user action) keeps
// this robust: a newly-added setting resets automatically, no enumeration to
// forget.
func (p *AssetPreferences) resetLocked(keep map[string]bool) {
	fresh := defaultPrefs(p.path)
	pv, fv := reflect.ValueOf(p).Elem(), reflect.ValueOf(fresh).Elem()
	t := pv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || keep[f.Name] { // unexported machinery / preserved content
			continue
		}
		pv.Field(i).Set(fv.Field(i))
	}
	p.formatGen.Add(1)   // format/fallback prefs may have changed; invalidate resolver caches
	p.wardrobeGen.Add(1) // a reset can wipe ServerWarm (and its wardrobes)
}

// ResetSettings reverts the tunable settings to defaults but KEEPS your content
// (favourites, wardrobes, servers & logins, macros, callwords, learned formats,
// layout overrides, local mounts, names) and the disk cache. Persisted now.
func (p *AssetPreferences) ResetSettings() {
	p.mu.Lock()
	p.resetLocked(resetContentFields)
	p.mu.Unlock()
	_ = p.SaveNow()
}

// ResetAll wipes EVERYTHING to a fresh-install state — settings AND content
// (favourites, wardrobes, servers, logins/passwords, macros, callwords, learned
// formats, ...). The disk cache is the caller's to clear. Persisted now.
func (p *AssetPreferences) ResetAll() {
	p.mu.Lock()
	p.resetLocked(nil)
	p.mu.Unlock()
	_ = p.SaveNow()
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

// UpdateChannelExperimentalOn reports whether the launch check follows the
// experimental channel — the full releases feed INCLUDING prereleases (test-
// branch builds) — instead of stable-only. OFF by default; a Power-user
// toggle for people who want riskier builds for extensive debugging.
func (p *AssetPreferences) UpdateChannelExperimentalOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.UpdateExperimental
}

// SetUpdateChannelExperimental swaps the update channel (true = experimental).
func (p *AssetPreferences) SetUpdateChannelExperimental(on bool) {
	p.mu.Lock()
	if p.UpdateExperimental == on {
		p.mu.Unlock()
		return
	}
	p.UpdateExperimental = on
	p.mu.Unlock()
	p.markDirty()
}

// --- Missing-asset warning banner -------------------------------------------

// AssetWarningsOn reports whether the red on-screen "Missing asset" banner
// shows (OFF by default — the failures still go to the debug overlay).
func (p *AssetPreferences) AssetWarningsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShowAssetWarnings
}

// SetAssetWarnings toggles the missing-asset banner.
func (p *AssetPreferences) SetAssetWarnings(on bool) {
	p.mu.Lock()
	if p.ShowAssetWarnings == on {
		p.mu.Unlock()
		return
	}
	p.ShowAssetWarnings = on
	p.mu.Unlock()
	p.markDirty()
}

// --- Sprite repositioning ---------------------------------------------------

// SpriteMoveEnabled reports the click-drag sprite repositioning toggle
// (OFF by default).
func (p *AssetPreferences) SpriteMoveEnabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteMoveOn
}

// SetSpriteMove toggles click-drag sprite repositioning.
func (p *AssetPreferences) SetSpriteMove(on bool) {
	p.mu.Lock()
	if p.SpriteMoveOn == on {
		p.mu.Unlock()
		return
	}
	p.SpriteMoveOn = on
	p.mu.Unlock()
	p.markDirty()
}

// --- Desk format policy -----------------------------------------------------

// DeskFollowsManifest reports whether desks adopt the server extensions.json
// format (OFF by default — desks stay WebP regardless of the manifest).
func (p *AssetPreferences) DeskFollowsManifest() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DeskFollowManifest
}

// SetDeskFollowManifest toggles whether desks follow the server manifest.
func (p *AssetPreferences) SetDeskFollowManifest(on bool) {
	p.mu.Lock()
	if p.DeskFollowManifest == on {
		p.mu.Unlock()
		return
	}
	p.DeskFollowManifest = on
	p.mu.Unlock()
	p.markDirty()
}

// --- Sprite hover preview ---------------------------------------------------

// SpritePreviewsOn reports the hover-preview toggle (ON by default).
func (p *AssetPreferences) SpritePreviewsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpritePreviewOn
}

// SetSpritePreviews toggles the hover-preview pop-up.
func (p *AssetPreferences) SetSpritePreviews(on bool) {
	p.mu.Lock()
	if p.SpritePreviewOn == on {
		p.mu.Unlock()
		return
	}
	p.SpritePreviewOn = on
	p.mu.Unlock()
	p.markDirty()
}

// PreviewHoverMillis returns the configured hover dwell in milliseconds
// (clamped), for the Settings readout.
func (p *AssetPreferences) PreviewHoverMillis() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.PreviewHoverMs <= 0 {
		return DefaultPreviewHoverMs
	}
	return clampPercent(p.PreviewHoverMs, minPreviewHoverMs, maxPreviewHoverMs)
}

// PreviewHoverDelay returns the hover dwell as a duration for the UI.
func (p *AssetPreferences) PreviewHoverDelay() time.Duration {
	return time.Duration(p.PreviewHoverMillis()) * time.Millisecond
}

// PreviewHeightPx returns the preview box's default height in px (the
// shipped default when unset; clamped to the grip's own bounds).
func (p *AssetPreferences) PreviewHeightPx() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.PreviewHeightPxVal <= 0 {
		return DefaultPreviewHeightPx
	}
	return clampPercent(p.PreviewHeightPxVal, MinPreviewHeightPx, MaxPreviewHeightPx)
}

// SetPreviewHeightPx clamps and persists the preview box's default height.
func (p *AssetPreferences) SetPreviewHeightPx(n int) {
	n = clampPercent(n, MinPreviewHeightPx, MaxPreviewHeightPx)
	p.mu.Lock()
	if p.PreviewHeightPxVal == n {
		p.mu.Unlock()
		return
	}
	p.PreviewHeightPxVal = n
	p.mu.Unlock()
	p.markDirty()
}

// SetPreviewHoverMs clamps and persists the hover dwell (milliseconds).
func (p *AssetPreferences) SetPreviewHoverMs(n int) {
	n = clampPercent(n, minPreviewHoverMs, maxPreviewHoverMs)
	p.mu.Lock()
	if p.PreviewHoverMs == n {
		p.mu.Unlock()
		return
	}
	p.PreviewHoverMs = n
	p.mu.Unlock()
	p.markDirty()
}

// AutoLoginToastOn reports the auto-login notification toggle (ON by default):
// a toast + desktop notification when a saved auto-login fires on join.
func (p *AssetPreferences) AutoLoginToastOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AutoLoginToast
}

// SetAutoLoginToast toggles the auto-login notification.
func (p *AssetPreferences) SetAutoLoginToast(on bool) {
	p.mu.Lock()
	if p.AutoLoginToast == on {
		p.mu.Unlock()
		return
	}
	p.AutoLoginToast = on
	p.mu.Unlock()
	p.markDirty()
}

// CallwordToastOn reports the callword-toast toggle (ON by default): an in-app
// toast naming the heard callword, alongside the flash + ping.
func (p *AssetPreferences) CallwordToastOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CallwordToast
}

// SetCallwordToast toggles the callword toast.
func (p *AssetPreferences) SetCallwordToast(on bool) {
	p.mu.Lock()
	if p.CallwordToast == on {
		p.mu.Unlock()
		return
	}
	p.CallwordToast = on
	p.mu.Unlock()
	p.markDirty()
}

// MessageCounterOn reports the IC character-counter toggle (ON by default).
func (p *AssetPreferences) MessageCounterOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MessageCounter
}

// SetMessageCounter toggles the IC character counter.
func (p *AssetPreferences) SetMessageCounter(on bool) {
	p.mu.Lock()
	if p.MessageCounter == on {
		p.mu.Unlock()
		return
	}
	p.MessageCounter = on
	p.mu.Unlock()
	p.markDirty()
}

// MentionSelfOn reports the self-mention toggle (OFF by default): treat your own
// character name / showname as a callword so being addressed by name alerts you.
func (p *AssetPreferences) MentionSelfOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MentionSelf
}

// SetMentionSelf toggles self-mention alerts.
func (p *AssetPreferences) SetMentionSelf(on bool) {
	p.mu.Lock()
	if p.MentionSelf == on {
		p.mu.Unlock()
		return
	}
	p.MentionSelf = on
	p.mu.Unlock()
	p.markDirty()
}

// LoopPreanimOn reports the "loop preanimations" toggle (OFF by default). ON =
// preanims wrap while on stage; OFF (canonical, matching AO2) plays them once.
func (p *AssetPreferences) LoopPreanimOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.LoopPreanim
}

// SetLoopPreanim toggles the non-canonical looping-preanimation behaviour.
func (p *AssetPreferences) SetLoopPreanim(on bool) {
	p.mu.Lock()
	if p.LoopPreanim == on {
		p.mu.Unlock()
		return
	}
	p.LoopPreanim = on
	p.mu.Unlock()
	p.markDirty()
}

// ICTimestampsOn reports the IC-log local-time prefix toggle (OFF by default).
func (p *AssetPreferences) ICTimestampsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ICTimestamps
}

// SetICTimestamps toggles the IC-log local-time prefix.
func (p *AssetPreferences) SetICTimestamps(on bool) {
	p.mu.Lock()
	if p.ICTimestamps == on {
		p.mu.Unlock()
		return
	}
	p.ICTimestamps = on
	p.mu.Unlock()
	p.markDirty()
}

// AutoReconnectOn reports the auto-reconnect toggle (ON by default): retry the
// last server with backoff after an unexpected drop.
func (p *AssetPreferences) AutoReconnectOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AutoReconnect
}

// SetAutoReconnect toggles auto-reconnect on a dropped connection.
func (p *AssetPreferences) SetAutoReconnect(on bool) {
	p.mu.Lock()
	if p.AutoReconnect == on {
		p.mu.Unlock()
		return
	}
	p.AutoReconnect = on
	p.mu.Unlock()
	p.markDirty()
}

// MusicHistoryOn reports the jukebox "recently played" history toggle (ON by
// default): capture the songs played in the room into the session list.
func (p *AssetPreferences) MusicHistoryOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MusicHistory
}

// SetMusicHistory toggles the jukebox "recently played" session history.
func (p *AssetPreferences) SetMusicHistory(on bool) {
	p.mu.Lock()
	if p.MusicHistory == on {
		p.mu.Unlock()
		return
	}
	p.MusicHistory = on
	p.mu.Unlock()
	p.markDirty()
}

// MusicStreamingOn reports whether AsyncAO fetches and plays custom /play tracks
// (ON by default). OFF = no /play song is ever downloaded on this client.
func (p *AssetPreferences) MusicStreamingOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MusicStreaming
}

// SetMusicStreaming toggles custom /play music streaming.
func (p *AssetPreferences) SetMusicStreaming(on bool) {
	p.mu.Lock()
	if p.MusicStreaming == on {
		p.mu.Unlock()
		return
	}
	p.MusicStreaming = on
	p.mu.Unlock()
	p.markDirty()
}

// ShowMissingPlaceholderOn reports whether the AO2 placeholder (missingno) draws
// for a conclusively-missing character sprite (ON by default).
func (p *AssetPreferences) ShowMissingPlaceholderOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShowMissingPlaceholder
}

// SetShowMissingPlaceholder toggles the missing-sprite placeholder.
func (p *AssetPreferences) SetShowMissingPlaceholder(on bool) {
	p.mu.Lock()
	if p.ShowMissingPlaceholder == on {
		p.mu.Unlock()
		return
	}
	p.ShowMissingPlaceholder = on
	p.mu.Unlock()
	p.markDirty()
}

// ErrorSpritePath reports the user-chosen custom placeholder image path ("" =
// the embedded default missingno).
func (p *AssetPreferences) ErrorSpritePath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ErrorSpriteFile
}

// SetErrorSpritePath persists a custom placeholder image path ("" clears it back
// to the embedded default). The App reloads the art off-thread on change.
func (p *AssetPreferences) SetErrorSpritePath(path string) {
	p.mu.Lock()
	if p.ErrorSpriteFile == path {
		p.mu.Unlock()
		return
	}
	p.ErrorSpriteFile = path
	p.mu.Unlock()
	p.markDirty()
}

// ClearLearnedType drops every host's learned format for one asset type (e.g.
// after changing the desk-format policy) so the next probe re-derives it.
func (p *AssetPreferences) ClearLearnedType(typeName string) {
	p.mu.Lock()
	p.dropLearnedTypeLocked(typeName)
	p.mu.Unlock()
	p.formatGen.Add(1)
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

// ICCustomColorRGB returns the last free IC hex pick, packed 0xRRGGBB
// (v1.52.0 — seeds the colour wheel; defaultICCustomColor until first use).
func (p *AssetPreferences) ICCustomColorRGB() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ICCustomColor & 0xFFFFFF
}

// SetICCustomColor persists the free IC hex pick (packed 0xRRGGBB).
func (p *AssetPreferences) SetICCustomColor(rgb int) {
	rgb &= 0xFFFFFF
	p.mu.Lock()
	if p.ICCustomColor == rgb {
		p.mu.Unlock()
		return
	}
	p.ICCustomColor = rgb
	p.mu.Unlock()
	p.markDirty()
}

// NameColorsOn reports the per-speaker name-colour toggle (OFF by default).
func (p *AssetPreferences) NameColorsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.NameColors
}

// SetNameColors toggles per-speaker name colours.
func (p *AssetPreferences) SetNameColors(on bool) {
	p.mu.Lock()
	if p.NameColors == on {
		p.mu.Unlock()
		return
	}
	p.NameColors = on
	p.mu.Unlock()
	p.markDirty()
}

// NameColorSat / NameColorVal report the name-colour saturation and brightness
// (0..100; value floored at minNameColorVal so names stay readable).
func (p *AssetPreferences) NameColorSat() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.NameSat
}

func (p *AssetPreferences) NameColorVal() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.NameVal
}

// SetNameColorSatVal clamps and stores the saturation + brightness together.
func (p *AssetPreferences) SetNameColorSatVal(sat, val int) {
	sat = clampPercent(sat, 0, 100)
	val = clampPercent(val, minNameColorVal, 100)
	p.mu.Lock()
	if p.NameSat == sat && p.NameVal == val {
		p.mu.Unlock()
		return
	}
	p.NameSat, p.NameVal = sat, val
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

// DownloadCapKBps reports the downloader bandwidth cap in KiB/s (0 = unlimited).
func (p *AssetPreferences) DownloadCapKBps() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.DownloadKBps < 0 {
		return 0
	}
	if p.DownloadKBps > maxDownloadKBps {
		return maxDownloadKBps
	}
	return p.DownloadKBps
}

// SetDownloadCapKBps clamps and persists the bandwidth cap (0 = unlimited).
func (p *AssetPreferences) SetDownloadCapKBps(n int) {
	if n < 0 {
		n = 0
	}
	if n > maxDownloadKBps {
		n = maxDownloadKBps
	}
	p.mu.Lock()
	if p.DownloadKBps == n {
		p.mu.Unlock()
		return
	}
	p.DownloadKBps = n
	p.mu.Unlock()
	p.markDirty()
}

// ForceCharNames reports the "show character names, not custom shownames"
// toggle (OFF by default) — applies to everyone's messages, in the chatbox and
// the IC log, for true-roleplay immersion / anti-impersonation in casing.
func (p *AssetPreferences) ForceCharNamesOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ForceCharNames
}

// SetForceCharNames toggles forcing character names over custom shownames.
func (p *AssetPreferences) SetForceCharNames(on bool) {
	p.mu.Lock()
	if p.ForceCharNames == on {
		p.mu.Unlock()
		return
	}
	p.ForceCharNames = on
	p.mu.Unlock()
	p.markDirty()
}

// RandomEmote reports the "auto-random emote" toggle (OFF by default): when on,
// every IC send rolls a fresh emote from the current character's set so the
// sprite changes each message without manual clicking.
func (p *AssetPreferences) RandomEmoteOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RandomEmote
}

// SetRandomEmote toggles auto-random emote selection on send.
func (p *AssetPreferences) SetRandomEmote(on bool) {
	p.mu.Lock()
	if p.RandomEmote == on {
		p.mu.Unlock()
		return
	}
	p.RandomEmote = on
	p.mu.Unlock()
	p.markDirty()
}

// FriendHighlightOn reports the master toggle for friend (highlighted-showname)
// IC-log highlighting (OFF by default).
func (p *AssetPreferences) FriendHighlightOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FriendHighlight
}

// SetFriendHighlight toggles friend IC-log highlighting.
func (p *AssetPreferences) SetFriendHighlight(on bool) {
	p.mu.Lock()
	if p.FriendHighlight == on {
		p.mu.Unlock()
		return
	}
	p.FriendHighlight = on
	p.mu.Unlock()
	p.markDirty()
}

// FriendButtonShown reports whether the player-list row menu offers the
// "+ Friend" / "Unfriend" action (ON by default; Settings can hide it).
func (p *AssetPreferences) FriendButtonShown() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShowFriendButton
}

// DragLayoutOn reports whether the courtroom resizes by dragging panel edges
// (ON by default) — when on, the viewport↔log divider is draggable and the +/−
// layout-knob panel is hidden; off brings the knobs back.
func (p *AssetPreferences) DragLayoutOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DragLayout
}

// SetDragLayout toggles drag-to-resize layout mode.
func (p *AssetPreferences) SetDragLayout(on bool) {
	p.mu.Lock()
	if p.DragLayout == on {
		p.mu.Unlock()
		return
	}
	p.DragLayout = on
	p.mu.Unlock()
	p.markDirty()
}

// SetShowFriendButton toggles the player-list friend button.
func (p *AssetPreferences) SetShowFriendButton(on bool) {
	p.mu.Lock()
	if p.ShowFriendButton == on {
		p.mu.Unlock()
		return
	}
	p.ShowFriendButton = on
	p.mu.Unlock()
	p.markDirty()
}

// ClipSpritesToStageOn reports the viewport sprite mask (ON by default): character
// sprites are clipped to the stage so a big pair / reposition offset can't spill
// over the chatbox or the log.
func (p *AssetPreferences) ClipSpritesToStageOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ClipSpritesToStage
}

// SetClipSpritesToStage toggles the viewport sprite mask.
func (p *AssetPreferences) SetClipSpritesToStage(on bool) {
	p.mu.Lock()
	if p.ClipSpritesToStage == on {
		p.mu.Unlock()
		return
	}
	p.ClipSpritesToStage = on
	p.mu.Unlock()
	p.markDirty()
}

// RightClickHideSpriteOn reports whether right-clicking a sprite offers to hide
// it from the viewport (ON by default; a Settings toggle disables it).
func (p *AssetPreferences) RightClickHideSpriteOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RightClickHideSprite
}

// SetRightClickHideSprite toggles right-click-to-hide-sprite.
func (p *AssetPreferences) SetRightClickHideSprite(on bool) {
	p.mu.Lock()
	if p.RightClickHideSprite == on {
		p.mu.Unlock()
		return
	}
	p.RightClickHideSprite = on
	p.mu.Unlock()
	p.markDirty()
}

// DyslexiaFontOn reports the dyslexia-friendly font toggle (OFF by default):
// when on, the bundled OpenDyslexic font drives the IC/OOC chat + log text and
// takes precedence over any manual font-path override.
func (p *AssetPreferences) DyslexiaFontOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DyslexiaFont
}

// SetDyslexiaFont toggles the embedded dyslexia-friendly font.
func (p *AssetPreferences) SetDyslexiaFont(on bool) {
	p.mu.Lock()
	if p.DyslexiaFont == on {
		p.mu.Unlock()
		return
	}
	p.DyslexiaFont = on
	p.mu.Unlock()
	p.markDirty()
}

// FontEverywhereOn reports whether the active font override (the dyslexia
// toggle or the manual font chain) also drives the CHROME — every menu,
// button, list and tab — instead of just the IC/OOC chat + log text. OFF by
// default: the chrome's fixed row/button metrics are tuned for the embedded
// face, so extending an override to the whole UI is an explicit opt-in.
func (p *AssetPreferences) FontEverywhereOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FontEverywhere
}

// SetFontEverywhere toggles extending the font override to the whole UI.
func (p *AssetPreferences) SetFontEverywhere(on bool) {
	p.mu.Lock()
	if p.FontEverywhere == on {
		p.mu.Unlock()
		return
	}
	p.FontEverywhere = on
	p.mu.Unlock()
	p.markDirty()
}

// DNDPersistOn reports the "remember Do Not Disturb across restarts" option (OFF
// by default — DND normally clears every launch so it can't silently mute you
// forever; the on-screen badge + opt-in are the guard when it's on).
func (p *AssetPreferences) DNDPersistOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DNDPersist
}

// SetDNDPersist toggles whether the DND state survives a restart.
func (p *AssetPreferences) SetDNDPersist(on bool) {
	p.mu.Lock()
	if p.DNDPersist == on {
		p.mu.Unlock()
		return
	}
	p.DNDPersist = on
	p.mu.Unlock()
	p.markDirty()
}

// DNDSavedOn reports the persisted Do Not Disturb state, restored at launch only
// when DNDPersistOn.
func (p *AssetPreferences) DNDSavedOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DNDSaved
}

// SetDNDSaved records the current DND state for next launch (called only while
// persistence is on).
func (p *AssetPreferences) SetDNDSaved(on bool) {
	p.mu.Lock()
	if p.DNDSaved == on {
		p.mu.Unlock()
		return
	}
	p.DNDSaved = on
	p.mu.Unlock()
	p.markDirty()
}

// RainbowMessagesOn reports the "rainbow my IC messages" toggle (OFF by default):
// when on, sendIC prefixes \cr so the message renders as a per-rune colour cycle
// (on clients that parse the inline-colour markup).
func (p *AssetPreferences) RainbowMessagesOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RainbowMessages
}

// SetRainbowMessages toggles rainbow IC messages.
func (p *AssetPreferences) SetRainbowMessages(on bool) {
	p.mu.Lock()
	if p.RainbowMessages == on {
		p.mu.Unlock()
		return
	}
	p.RainbowMessages = on
	p.mu.Unlock()
	p.markDirty()
}

// RandomMessageColorOn reports the "random colour per IC message" toggle (OFF by
// default): each message picks a random palette colour (the standard TextColor
// field, so every client shows it).
func (p *AssetPreferences) RandomMessageColorOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RandomMessageColor
}

// SetRandomMessageColor toggles random per-message colour.
func (p *AssetPreferences) SetRandomMessageColor(on bool) {
	p.mu.Lock()
	if p.RandomMessageColor == on {
		p.mu.Unlock()
		return
	}
	p.RandomMessageColor = on
	p.mu.Unlock()
	p.markDirty()
}

// RainbowSpritesOn reports the "rainbow character sprites" toggle (OFF by
// default): when on, the viewport washes every character layer through a
// slow hue cycle (local-only eye-candy — purely a render-side colour mod, it
// changes nothing on the wire and never touches other clients).
func (p *AssetPreferences) RainbowSpritesOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RainbowSprites
}

// SetRainbowSprites toggles the rainbow character-sprite wash.
func (p *AssetPreferences) SetRainbowSprites(on bool) {
	p.mu.Lock()
	if p.RainbowSprites == on {
		p.mu.Unlock()
		return
	}
	p.RainbowSprites = on
	p.mu.Unlock()
	p.markDirty()
}

// ShowRecordButtonOn reports the "show a small Record button on the courtroom"
// toggle (OFF by default — the feature is otherwise keyboard-only, Ctrl+W).
func (p *AssetPreferences) ShowRecordButtonOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShowRecordButton
}

// SetShowRecordButton toggles the on-courtroom Record button.
func (p *AssetPreferences) SetShowRecordButton(on bool) {
	p.mu.Lock()
	if p.ShowRecordButton == on {
		p.mu.Unlock()
		return
	}
	p.ShowRecordButton = on
	p.mu.Unlock()
	p.markDirty()
}

// InstantDisconnectOn reports whether Disconnect acts immediately (true) or asks
// for confirmation first (false, the default — the button is easy to hit by
// accident).
func (p *AssetPreferences) InstantDisconnectOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.InstantDisconnect
}

// SetInstantDisconnect toggles skip-the-confirm on the Disconnect button.
func (p *AssetPreferences) SetInstantDisconnect(on bool) {
	p.mu.Lock()
	if p.InstantDisconnect == on {
		p.mu.Unlock()
		return
	}
	p.InstantDisconnect = on
	p.mu.Unlock()
	p.markDirty()
}

// HideDeskOn reports whether the courtroom desk is hidden (default OFF). When on,
// the desk layer is suppressed so the character isn't grounded behind it.
func (p *AssetPreferences) HideDeskOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HideDesk
}

// SetHideDesk toggles hiding the desk.
func (p *AssetPreferences) SetHideDesk(on bool) {
	p.mu.Lock()
	if p.HideDesk == on {
		p.mu.Unlock()
		return
	}
	p.HideDesk = on
	p.mu.Unlock()
	p.markDirty()
}

// Instant-replay capture-window bounds: a sane floor so a clip has context, and
// a 1-hour ceiling (the requested "up to one hour"). 0/unset reads as default.
const (
	InstantReplayMinSeconds     = 10
	InstantReplayMaxSeconds     = 3600
	InstantReplayDefaultSeconds = 60
)

// Local alarm/timer (#97) duration bounds: 1 second … 99 minutes, default 5 min.
// Only the remembered duration persists; running state is session-only.
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

// TimerSecondsValue returns the clamped remembered countdown duration (#97); the
// running state itself lives in the UI (session-only). 0/unset reads as default.
func (p *AssetPreferences) TimerSecondsValue() int {
	p.mu.RLock()
	s := p.TimerSeconds
	p.mu.RUnlock()
	return clampTimerSeconds(s)
}

// SetTimerSeconds stores the countdown duration, clamped to [min,max].
func (p *AssetPreferences) SetTimerSeconds(s int) {
	s = clampTimerSeconds(s)
	p.mu.Lock()
	if p.TimerSeconds == s {
		p.mu.Unlock()
		return
	}
	p.TimerSeconds = s
	p.mu.Unlock()
	p.markDirty()
}

// TimerRepeatOn reports whether the local timer restarts itself on each fire
// (#97, default OFF).
func (p *AssetPreferences) TimerRepeatOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.TimerRepeat
}

// NotifyOnOOCOn reports whether OOC messages count toward a background tab's
// unread badge. Default OFF: server auto-messages (hourly reminders, etc.) live
// in OOC and shouldn't light up a "(1)" when nobody actually chatted — so only IC
// counts unless the user opts in.
func (p *AssetPreferences) NotifyOnOOCOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.NotifyOnOOC
}

// SetNotifyOnOOC toggles whether OOC bumps the unread tab badge.
func (p *AssetPreferences) SetNotifyOnOOC(on bool) {
	p.mu.Lock()
	if p.NotifyOnOOC == on {
		p.mu.Unlock()
		return
	}
	p.NotifyOnOOC = on
	p.mu.Unlock()
	p.markDirty()
}

// MusicAcrossTabsOn reports whether a backgrounded tab's music stays audible
// across a server-tab switch (default OFF). OFF ducks the backgrounded stream to
// volume 0 so tabs are acoustically isolated (its position is preserved, not
// stopped); ON keeps it playing at normal volume.
func (p *AssetPreferences) MusicAcrossTabsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MusicAcrossTabs
}

// SetMusicAcrossTabs toggles keeping a backgrounded tab's music audible across a
// tab switch (default OFF = duck to 0).
func (p *AssetPreferences) SetMusicAcrossTabs(on bool) {
	p.mu.Lock()
	if p.MusicAcrossTabs == on {
		p.mu.Unlock()
		return
	}
	p.MusicAcrossTabs = on
	p.mu.Unlock()
	p.markDirty()
}

// ShowSongURLOn reports whether the music log line shows the full song URL
// instead of just the song name (default OFF).
func (p *AssetPreferences) ShowSongURLOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShowSongURL
}

// SetShowSongURL toggles showing the full song URL in the music log line.
func (p *AssetPreferences) SetShowSongURL(on bool) {
	p.mu.Lock()
	if p.ShowSongURL == on {
		p.mu.Unlock()
		return
	}
	p.ShowSongURL = on
	p.mu.Unlock()
	p.markDirty()
}

// SetTimerRepeat stores the timer's repeat toggle.
func (p *AssetPreferences) SetTimerRepeat(on bool) {
	p.mu.Lock()
	if p.TimerRepeat == on {
		p.mu.Unlock()
		return
	}
	p.TimerRepeat = on
	p.mu.Unlock()
	p.markDirty()
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

// InstantReplayOn reports whether the always-on rolling clip buffer is enabled
// (default OFF). When on, recent scene events are kept so the clip key can save
// the last window WITHOUT a recording started in advance.
func (p *AssetPreferences) InstantReplayOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.InstantReplay
}

// SetInstantReplay toggles the rolling clip buffer.
func (p *AssetPreferences) SetInstantReplay(on bool) {
	p.mu.Lock()
	if p.InstantReplay == on {
		p.mu.Unlock()
		return
	}
	p.InstantReplay = on
	p.mu.Unlock()
	p.markDirty()
}

// InstantReplaySecondsValue returns the clamped capture-window length in seconds
// (the value the slider shows); 0/unset reads as the default.
func (p *AssetPreferences) InstantReplaySecondsValue() int {
	p.mu.RLock()
	s := p.InstantReplaySeconds
	p.mu.RUnlock()
	return clampInstantReplaySeconds(s)
}

// SetInstantReplaySeconds stores the capture window, clamped to [min,max].
func (p *AssetPreferences) SetInstantReplaySeconds(s int) {
	s = clampInstantReplaySeconds(s)
	p.mu.Lock()
	if p.InstantReplaySeconds == s {
		p.mu.Unlock()
		return
	}
	p.InstantReplaySeconds = s
	p.mu.Unlock()
	p.markDirty()
}

// FavEmoteBoxOn reports whether the floating favourite-emotes box is shown.
func (p *AssetPreferences) FavEmoteBoxOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FavEmoteBox
}

// SetFavEmoteBox toggles the floating favourite-emotes box.
func (p *AssetPreferences) SetFavEmoteBox(on bool) {
	p.mu.Lock()
	if p.FavEmoteBox == on {
		p.mu.Unlock()
		return
	}
	p.FavEmoteBox = on
	p.mu.Unlock()
	p.markDirty()
}

// AutoConnectOnLaunchOn reports whether the client auto-connects to the last
// server on launch (default OFF). Distinct from M7 tab-restore: this fires even
// when no tab was open at shutdown, so it always lands on your chosen server.
func (p *AssetPreferences) AutoConnectOnLaunchOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AutoConnectOnLaunch
}

// SetAutoConnectOnLaunch toggles auto-connect-on-launch.
func (p *AssetPreferences) SetAutoConnectOnLaunch(on bool) {
	p.mu.Lock()
	if p.AutoConnectOnLaunch == on {
		p.mu.Unlock()
		return
	}
	p.AutoConnectOnLaunch = on
	p.mu.Unlock()
	p.markDirty()
}

// LastServer returns the last server connected to (name, ws URL) — the
// auto-connect / quick-connect target. Empty url means none yet.
func (p *AssetPreferences) LastServer() (name, url string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.LastServerName, p.LastServerURL
}

// SetLastServer records the last server connected to (persisted for auto-connect
// + quick-connect). Called on every connect.
func (p *AssetPreferences) SetLastServer(name, url string) {
	p.mu.Lock()
	if p.LastServerName == name && p.LastServerURL == url {
		p.mu.Unlock()
		return
	}
	p.LastServerName, p.LastServerURL = name, url
	p.mu.Unlock()
	p.markDirty()
}

// RainbowSpeed reports the rainbow hue-rotation speed slider [1,100]
// (higher = faster); render maps it to the cycle period.
func (p *AssetPreferences) RainbowSpeed() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RainbowSpriteSpeed
}

// ReplaySpeed reports the scene-replay playback speed percent [25,200] (100 =
// the readable base, lower = slower). The UI maps it to the typewriter crawl +
// linger when driving a replay.
func (p *AssetPreferences) ReplaySpeed() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ReplayPlaybackSpeed
}

// SetReplaySpeed stores the replay playback speed (clamped to [25,200]).
func (p *AssetPreferences) SetReplaySpeed(v int) {
	v = clampPercent(v, minReplaySpeed, maxReplaySpeed)
	p.mu.Lock()
	if p.ReplayPlaybackSpeed == v {
		p.mu.Unlock()
		return
	}
	p.ReplayPlaybackSpeed = v
	p.mu.Unlock()
	p.markDirty()
}

// ExportOpts reports the sticky scene-export options (GIF/WebP size/fps/quality/loop).
func (p *AssetPreferences) ExportOpts() ExportOptions {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Export
}

// SetExportOpts stores the export options (each field clamped to its range).
func (p *AssetPreferences) SetExportOpts(o ExportOptions) {
	o.HeightPx = clampPercent(o.HeightPx, minExportHeight, maxExportHeight)
	o.FPS = clampPercent(o.FPS, minExportFPS, maxExportFPS)
	o.Quality = clampPercent(o.Quality, minExportQuality, maxExportQuality)
	o.TextScale = clampPercent(o.TextScale, minExportText, maxExportText)
	o.VideoFormat = normalizeVideoFormat(o.VideoFormat)
	if runes := []rune(o.WatermarkText); len(runes) > maxWatermarkLen {
		o.WatermarkText = string(runes[:maxWatermarkLen]) // rune-safe: never split a glyph
	}
	p.mu.Lock()
	if p.Export == o {
		p.mu.Unlock()
		return
	}
	p.Export = o
	p.mu.Unlock()
	p.markDirty()
}

// SpriteStyle reports the user's own transmitted sprite customization (#103).
func (p *AssetPreferences) SpriteStyle() SpriteStylePref {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MySpriteStyle
}

// SetSpriteStyle stores the user's sprite style (opacity clamped to [0,100]).
func (p *AssetPreferences) SetSpriteStyle(s SpriteStylePref) {
	s.Opacity = clampOpacity(s.Opacity)
	p.mu.Lock()
	if p.MySpriteStyle == s {
		p.mu.Unlock()
		return
	}
	p.MySpriteStyle = s
	p.mu.Unlock()
	p.markDirty()
}

// --- #126 style presets (saved style+colour+emote moods) -----------------------------------

// sanitizeStylePresets bounds the list, drops nameless entries, and clamps each style's
// opacity — applied on load so a hand-edited pref file can't grow or corrupt the list.
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

// StylePresetCount returns how many presets are saved (no allocation — for the Style box's
// per-frame height calc).
func (p *AssetPreferences) StylePresetCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.SavedStyles)
}

// StylePresets returns a copy of the saved presets (so a caller can't mutate the live slice).
func (p *AssetPreferences) StylePresets() []StylePreset {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.SavedStyles) == 0 {
		return nil
	}
	return append([]StylePreset(nil), p.SavedStyles...)
}

// AddStylePreset appends a preset (its opacity clamped), ignoring a blank name or a list at
// the cap. A duplicate name overwrites the existing one (so "save" updates a mood in place).
func (p *AssetPreferences) AddStylePreset(pr StylePreset) {
	if strings.TrimSpace(pr.Name) == "" {
		return
	}
	pr.Style.Opacity = clampOpacity(pr.Style.Opacity)
	pr.Key = strings.ToLower(strings.TrimSpace(pr.Key))
	p.mu.Lock()
	for i := range p.SavedStyles {
		if p.SavedStyles[i].Name == pr.Name {
			pr.Key = p.SavedStyles[i].Key // keep an existing key binding across an overwrite
			p.SavedStyles[i] = pr
			p.mu.Unlock()
			p.markDirty()
			return
		}
	}
	if len(p.SavedStyles) >= stylePresetCap {
		p.mu.Unlock()
		return
	}
	p.SavedStyles = append(p.SavedStyles, pr)
	p.mu.Unlock()
	p.markDirty()
}

// DeleteStylePreset removes the preset at i (bounds-checked).
func (p *AssetPreferences) DeleteStylePreset(i int) {
	p.mu.Lock()
	if i >= 0 && i < len(p.SavedStyles) {
		p.SavedStyles = append(p.SavedStyles[:i], p.SavedStyles[i+1:]...)
		p.markDirty()
	}
	p.mu.Unlock()
}

// SetStylePresetKey binds (or clears, key=="") a bare key to preset i, removing that key from
// any other preset first so a key maps to exactly one mood.
func (p *AssetPreferences) SetStylePresetKey(i int, key string) {
	key = strings.ToLower(strings.TrimSpace(key))
	p.mu.Lock()
	if i < 0 || i >= len(p.SavedStyles) {
		p.mu.Unlock()
		return
	}
	if key != "" {
		for j := range p.SavedStyles {
			if j != i && p.SavedStyles[j].Key == key {
				p.SavedStyles[j].Key = ""
			}
		}
	}
	p.SavedStyles[i].Key = key
	p.mu.Unlock()
	p.markDirty()
}

// StylePresetForKey returns the preset bound to a bare key (ok=false when none).
func (p *AssetPreferences) StylePresetForKey(key string) (StylePreset, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, pr := range p.SavedStyles {
		if pr.Key != "" && pr.Key == key {
			return pr, true
		}
	}
	return StylePreset{}, false
}

// HideSpriteStylesOn reports whether the viewer ignores others' transmitted
// sprite styles (default false = show them).
func (p *AssetPreferences) HideSpriteStylesOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HideSpriteStyles
}

// SetHideSpriteStyles toggles ignoring others' transmitted sprite styles.
func (p *AssetPreferences) SetHideSpriteStyles(b bool) {
	p.mu.Lock()
	if p.HideSpriteStyles == b {
		p.mu.Unlock()
		return
	}
	p.HideSpriteStyles = b
	p.mu.Unlock()
	p.markDirty()
}

// HideReactionsOn reports whether the viewer ignores others' transmitted emoji
// reactions (#2) (default false = show the floating emoji).
func (p *AssetPreferences) HideReactionsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HideReactions
}

// SetHideReactions toggles ignoring others' transmitted emoji reactions.
func (p *AssetPreferences) SetHideReactions(b bool) {
	p.mu.Lock()
	if p.HideReactions == b {
		p.mu.Unlock()
		return
	}
	p.HideReactions = b
	p.mu.Unlock()
	p.markDirty()
}

// CharBundlePrefetchOn reports the #127 toggle (OFF by default): when on, loading a character
// pre-grabs its FULL emote set (every idle + talk) at low priority, so emote switches are
// instant. Speculative + sheddable, so it never blocks live fetches; off keeps the lightweight
// "first few emotes" default.
func (p *AssetPreferences) CharBundlePrefetchOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CharBundlePrefetch
}

// SetCharBundlePrefetch toggles full-character bundle prefetch.
func (p *AssetPreferences) SetCharBundlePrefetch(b bool) { p.setBoolPref(&p.CharBundlePrefetch, b) }

// PingChipOn reports the #128 toggle (OFF by default): show the connection-quality signal-bar
// chip (and run the background ping loop that measures latency).
func (p *AssetPreferences) PingChipOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PingChip
}

// SetPingChip toggles the connection-quality chip.
func (p *AssetPreferences) SetPingChip(b bool) { p.setBoolPref(&p.PingChip, b) }

// ValidateTLSCertsOn reports the power-user Security toggle (OFF by default):
// when ON, wss:// connections strictly verify the server's TLS certificate
// chain. The default is OFF — most community AO servers run self-signed certs,
// and rejecting them would leave them unreachable. Turning it ON trades that
// reach for a guarantee the wss endpoint is who it claims to be.
func (p *AssetPreferences) ValidateTLSCertsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ValidateTLSCerts
}

// SetValidateTLSCerts toggles strict wss certificate verification.
func (p *AssetPreferences) SetValidateTLSCerts(b bool) { p.setBoolPref(&p.ValidateTLSCerts, b) }

// AssetOriginHeader reports the power-user Origin/Referer override sent on asset
// fetches (empty = none). For servers that gate their asset base by Origin/CORS.
func (p *AssetPreferences) AssetOriginHeader() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AssetOrigin
}

// SetAssetOriginHeader sets (or clears, when blank) the asset-fetch Origin override.
func (p *AssetPreferences) SetAssetOriginHeader(s string) {
	s = strings.TrimSpace(s)
	p.mu.Lock()
	if p.AssetOrigin == s {
		p.mu.Unlock()
		return
	}
	p.AssetOrigin = s
	p.mu.Unlock()
	p.markDirty()
}

// WSOriginHeader reports the power-user Origin override sent on the WebSocket
// HANDSHAKE (empty = none, the default). For servers that allowlist only their
// own web client's origin on the socket — the connection-side sibling of
// AssetOriginHeader (which covers asset fetches).
func (p *AssetPreferences) WSOriginHeader() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.WSOrigin
}

// SetWSOriginHeader sets (or clears, when blank) the WS-handshake Origin override.
// Applies on the NEXT connect (an open socket keeps its handshake).
func (p *AssetPreferences) SetWSOriginHeader(s string) {
	s = strings.TrimSpace(s)
	p.mu.Lock()
	if p.WSOrigin == s {
		p.mu.Unlock()
		return
	}
	p.WSOrigin = s
	p.mu.Unlock()
	p.markDirty()
}

// AssetCharCasing returns the character-folder casing mode (0 lowercase default / 1 first-cap /
// 2 title). A POWER-USER setting: the wrong value 404s every character asset.
func (p *AssetPreferences) AssetCharCasing() uint8 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AssetCharCase
}

// SetAssetCharCasing sets the character-folder casing mode.
func (p *AssetPreferences) SetAssetCharCasing(c uint8) {
	p.mu.Lock()
	if p.AssetCharCase == c {
		p.mu.Unlock()
		return
	}
	p.AssetCharCase = c
	p.mu.Unlock()
	p.markDirty()
}

// VoiceInput reports the chosen voice mic device name ("" = system default).
func (p *AssetPreferences) VoiceInput() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.VoiceInputDevice
}

// SetVoiceInput sets (or clears, for system default) the voice mic device.
func (p *AssetPreferences) SetVoiceInput(s string) {
	p.mu.Lock()
	if p.VoiceInputDevice == s {
		p.mu.Unlock()
		return
	}
	p.VoiceInputDevice = s
	p.mu.Unlock()
	p.markDirty()
}

// VoiceOutVol reports the voice output volume 0..100 (default 100 when unset).
func (p *AssetPreferences) VoiceOutVol() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.VoiceOutVolume <= 0 {
		return 100
	}
	if p.VoiceOutVolume > 100 {
		return 100
	}
	return p.VoiceOutVolume
}

// SetVoiceOutVol clamps and persists the voice output volume.
func (p *AssetPreferences) SetVoiceOutVol(v int) {
	if v < 0 {
		v = 0
	} else if v > 100 {
		v = 100
	}
	p.mu.Lock()
	if p.VoiceOutVolume == v {
		p.mu.Unlock()
		return
	}
	p.VoiceOutVolume = v
	p.mu.Unlock()
	p.markDirty()
}

// PrefetchAggressiveness reports how many of the top predicted next sprites the
// Markov prefetcher warms per message, 1..4 (default 1 = conservative). #100.
func (p *AssetPreferences) PrefetchAggressiveness() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.PrefetchAggro < 1 {
		return 1
	}
	if p.PrefetchAggro > 4 {
		return 4
	}
	return p.PrefetchAggro
}

// SetPrefetchAggressiveness clamps (1..4) and persists the prefetch aggressiveness.
func (p *AssetPreferences) SetPrefetchAggressiveness(n int) {
	if n < 1 {
		n = 1
	} else if n > 4 {
		n = 4
	}
	p.mu.Lock()
	if p.PrefetchAggro == n {
		p.mu.Unlock()
		return
	}
	p.PrefetchAggro = n
	p.mu.Unlock()
	p.markDirty()
}

// VoicePTT reports the push-to-talk toggle key name ("" = unbound).
func (p *AssetPreferences) VoicePTT() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.VoicePTTKey
}

// SetVoicePTT sets (or clears) the push-to-talk toggle key.
func (p *AssetPreferences) SetVoicePTT(k string) {
	p.mu.Lock()
	if p.VoicePTTKey == k {
		p.mu.Unlock()
		return
	}
	p.VoicePTTKey = k
	p.mu.Unlock()
	p.markDirty()
}

// QuitConfirmSkipOn reports whether the user ticked "don't ask again" on the quit
// dialog (so the quit hotkey exits immediately). Default OFF.
func (p *AssetPreferences) QuitConfirmSkipOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.QuitConfirmSkip
}

// SetQuitConfirmSkip persists the quit-confirm "don't ask again" choice.
func (p *AssetPreferences) SetQuitConfirmSkip(on bool) {
	p.mu.Lock()
	if p.QuitConfirmSkip == on {
		p.mu.Unlock()
		return
	}
	p.QuitConfirmSkip = on
	p.mu.Unlock()
	p.markDirty()
}

// LegacyDevThemeOn reports whether the user ticked the built-in "Legacy Developer" theme (the old
// look). Default OFF — the new optimal layout (OOC-as-a-box, grouped buttons) is the main theme.
func (p *AssetPreferences) LegacyDevThemeOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.LegacyDevTheme
}

// SetLegacyDevTheme toggles the Legacy Developer theme tickbox.
func (p *AssetPreferences) SetLegacyDevTheme(b bool) { p.setBoolPref(&p.LegacyDevTheme, b) }

// OOCInLogTabOn reports whether OOC should render as a tab in the log panel (the old
// layout) instead of its own box. Independent of the Legacy theme, which always tabs
// OOC — so the effective "OOC is a tab" condition is LegacyDevThemeOn() || OOCInLogTabOn().
func (p *AssetPreferences) OOCInLogTabOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.OOCInLogTab
}

// SetOOCInLogTab toggles OOC-as-a-log-tab (the old layout).
func (p *AssetPreferences) SetOOCInLogTab(b bool) { p.setBoolPref(&p.OOCInLogTab, b) }

// Profile reports the user's character profile (#101).
func (p *AssetPreferences) Profile() ProfilePref {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MyProfile
}

// SetProfile stores the user's character profile (every field length-clamped).
func (p *AssetPreferences) SetProfile(pr ProfilePref) {
	pr = clampProfile(pr)
	p.mu.Lock()
	if p.MyProfile == pr {
		p.mu.Unlock()
		return
	}
	p.MyProfile = pr
	p.mu.Unlock()
	p.markDirty()
}

// ChatboxOpacityPct reports the IC chatbox panel opacity (0–100, default 84) for
// the flat fallback skin (0 = fully see-through, 100 = solid).
func (p *AssetPreferences) ChatboxOpacityPct() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ChatboxOpacity
}

// SetChatboxOpacity clamps and persists the chatbox panel opacity percent.
func (p *AssetPreferences) SetChatboxOpacity(v int) {
	v = clampPercent(v, MinChatboxOpacity, MaxChatboxOpacity)
	p.mu.Lock()
	if p.ChatboxOpacity == v {
		p.mu.Unlock()
		return
	}
	p.ChatboxOpacity = v
	p.mu.Unlock()
	p.markDirty()
}

// SetRainbowSpriteSpeed stores the hue speed (clamped to [1,100]).
func (p *AssetPreferences) SetRainbowSpriteSpeed(v int) {
	v = clampPercent(v, minRainbowSpeed, maxRainbowSpeed)
	p.mu.Lock()
	if p.RainbowSpriteSpeed == v {
		p.mu.Unlock()
		return
	}
	p.RainbowSpriteSpeed = v
	p.mu.Unlock()
	p.markDirty()
}

// RainbowVividness reports the rainbow saturation slider [0,100] (higher =
// more vivid/neon); render maps it to the colour-mod channel floor.
func (p *AssetPreferences) RainbowVividness() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RainbowSpriteVividness
}

// SetRainbowSpriteVividness stores the saturation (clamped to [0,100]).
func (p *AssetPreferences) SetRainbowSpriteVividness(v int) {
	v = clampPercent(v, minRainbowVivid, maxRainbowVivid)
	p.mu.Lock()
	if p.RainbowSpriteVividness == v {
		p.mu.Unlock()
		return
	}
	p.RainbowSpriteVividness = v
	p.mu.Unlock()
	p.markDirty()
}

// RainbowSpriteGlowOn reports the additive-blend "neon glow" toggle (OFF by
// default): the tint adds light instead of multiplying, so sprites glow.
func (p *AssetPreferences) RainbowSpriteGlowOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RainbowSpriteGlow
}

// SetRainbowSpriteGlow toggles the additive neon-glow blend.
func (p *AssetPreferences) SetRainbowSpriteGlow(on bool) {
	p.mu.Lock()
	if p.RainbowSpriteGlow == on {
		p.mu.Unlock()
		return
	}
	p.RainbowSpriteGlow = on
	p.mu.Unlock()
	p.markDirty()
}

// RainbowPairDesyncOn reports the "desync pair colour" toggle (OFF by default):
// the speaker and pair sprites cycle half a period apart, so they show
// different hues at once.
func (p *AssetPreferences) RainbowPairDesyncOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RainbowPairDesync
}

// SetRainbowPairDesync toggles the pair hue offset.
func (p *AssetPreferences) SetRainbowPairDesync(on bool) {
	p.mu.Lock()
	if p.RainbowPairDesync == on {
		p.mu.Unlock()
		return
	}
	p.RainbowPairDesync = on
	p.mu.Unlock()
	p.markDirty()
}

// RainbowPerCharOn reports the "different hue per character" toggle (OFF by
// default): with rainbow on, each character's hue is offset by a hash of its
// name, so several on-stage characters show different colours at once.
func (p *AssetPreferences) RainbowPerCharOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RainbowPerChar
}

// SetRainbowPerChar toggles the per-character hue offset.
func (p *AssetPreferences) SetRainbowPerChar(on bool) {
	p.mu.Lock()
	if p.RainbowPerChar == on {
		p.mu.Unlock()
		return
	}
	p.RainbowPerChar = on
	p.mu.Unlock()
	p.markDirty()
}

// SpriteWobbleOn reports the "wobble" toggle (OFF by default): a gentle
// continuous position sway over the on-stage sprites.
func (p *AssetPreferences) SpriteWobbleOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteWobble
}

// SetSpriteWobble toggles the sprite wobble.
func (p *AssetPreferences) SetSpriteWobble(on bool) {
	p.mu.Lock()
	if p.SpriteWobble == on {
		p.mu.Unlock()
		return
	}
	p.SpriteWobble = on
	p.mu.Unlock()
	p.markDirty()
}

// SpriteSpinOn reports the "spin" toggle (OFF by default): the on-stage sprites
// rotate slowly and continuously.
func (p *AssetPreferences) SpriteSpinOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteSpin
}

// SetSpriteSpin toggles the sprite spin.
func (p *AssetPreferences) SetSpriteSpin(on bool) {
	p.mu.Lock()
	if p.SpriteSpin == on {
		p.mu.Unlock()
		return
	}
	p.SpriteSpin = on
	p.mu.Unlock()
	p.markDirty()
}

// SpriteSolidTintOn reports the "solid colour tint" toggle (OFF by default): a
// single fixed-colour wash over sprites (rainbow takes priority if both are on).
func (p *AssetPreferences) SpriteSolidTintOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteSolidTint
}

// SetSpriteSolidTint toggles the fixed-colour sprite wash.
func (p *AssetPreferences) SetSpriteSolidTint(on bool) {
	p.mu.Lock()
	if p.SpriteSolidTint == on {
		p.mu.Unlock()
		return
	}
	p.SpriteSolidTint = on
	p.mu.Unlock()
	p.markDirty()
}

// ShoutPunchOn reports the "shout punch" toggle (OFF by default): a quick scale-pop of the
// whole stage when a shout/objection appears, for extra impact (#12). Viewer-local.
func (p *AssetPreferences) ShoutPunchOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShoutPunch
}

// SetShoutPunch toggles the shout screen-punch.
func (p *AssetPreferences) SetShoutPunch(on bool) {
	p.mu.Lock()
	if p.ShoutPunch == on {
		p.mu.Unlock()
		return
	}
	p.ShoutPunch = on
	p.mu.Unlock()
	p.markDirty()
}

// ChatboxTintOn reports the "per-character chatbox tint" toggle (OFF by default): the
// chatbox panel takes a hint of each speaker's stable hue (#14). Viewer-local.
func (p *AssetPreferences) ChatboxTintOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ChatboxTint
}

// SetChatboxTint toggles the per-character chatbox tint.
func (p *AssetPreferences) SetChatboxTint(on bool) {
	p.mu.Lock()
	if p.ChatboxTint == on {
		p.mu.Unlock()
		return
	}
	p.ChatboxTint = on
	p.mu.Unlock()
	p.markDirty()
}

// Post-processing overlay toggles (#10, all OFF by default): retro looks blended over the
// stage — a vignette, scanlines, and film grain.
func (p *AssetPreferences) PostVignetteOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PostVignette
}
func (p *AssetPreferences) PostScanlinesOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PostScanlines
}
func (p *AssetPreferences) PostGrainOn() bool { p.mu.RLock(); defer p.mu.RUnlock(); return p.PostGrain }
func (p *AssetPreferences) PostCRTOn() bool   { p.mu.RLock(); defer p.mu.RUnlock(); return p.PostCRT }

// SetPostVignette / SetPostScanlines / SetPostGrain / SetPostCRT toggle the overlays.
func (p *AssetPreferences) SetPostVignette(on bool)  { p.setBoolPref(&p.PostVignette, on) }
func (p *AssetPreferences) SetPostScanlines(on bool) { p.setBoolPref(&p.PostScanlines, on) }
func (p *AssetPreferences) SetPostGrain(on bool)     { p.setBoolPref(&p.PostGrain, on) }
func (p *AssetPreferences) SetPostCRT(on bool)       { p.setBoolPref(&p.PostCRT, on) }

// AnimateEntrancesOn reports the #9 entrance-slide toggle (OFF by default): a new speaker
// slides in when they take the stage.
func (p *AssetPreferences) AnimateEntrancesOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AnimateEntrances
}

// SetAnimateEntrances toggles the entrance slide-in.
func (p *AssetPreferences) SetAnimateEntrances(on bool) { p.setBoolPref(&p.AnimateEntrances, on) }

// DepthOfFieldOn reports the #11 toggle (OFF by default): soft-focus + dim the background
// behind the sharp speaker.
func (p *AssetPreferences) DepthOfFieldOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DepthOfField
}

// SetDepthOfField toggles the background depth-of-field.
func (p *AssetPreferences) SetDepthOfField(on bool) { p.setBoolPref(&p.DepthOfField, on) }

// Spotlight dim-strength bounds + default. The slider range is kept off 0/100 so "on" always
// reads as a spotlight (a usable shadow, never full black, never invisible).
const (
	minSpotlightStrength     = 10
	maxSpotlightStrength     = 90
	defaultSpotlightStrength = 55
)

// SpotlightOn reports the #121 toggle (OFF by default): dim the non-speaker layers (the pair
// partner + the desk) so the talking character pops.
func (p *AssetPreferences) SpotlightOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Spotlight
}

// SetSpotlight toggles the speaker spotlight.
func (p *AssetPreferences) SetSpotlight(on bool) { p.setBoolPref(&p.Spotlight, on) }

// SpotlightLevel reports the dim intensity [10,90] (higher = darker non-speakers); 0 (unset)
// resolves to the default so enabling the toggle always shows a visible effect.
func (p *AssetPreferences) SpotlightLevel() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.SpotlightStrength == 0 {
		return defaultSpotlightStrength
	}
	return p.SpotlightStrength
}

// SetSpotlightLevel stores the dim intensity (clamped to [10,90]).
func (p *AssetPreferences) SetSpotlightLevel(v int) {
	v = clampPercent(v, minSpotlightStrength, maxSpotlightStrength)
	p.mu.Lock()
	if p.SpotlightStrength == v {
		p.mu.Unlock()
		return
	}
	p.SpotlightStrength = v
	p.mu.Unlock()
	p.markDirty()
}

// Idle-breathing (#122) defaults. The two components default ON (stored inverted so the
// zero-value pref breathes both ways); amplitude/speed default to a gentle middle.
const (
	defaultBreathAmp   = 40
	defaultBreathSpeed = 50
)

// IdleBreathOn reports the #122 master toggle (OFF by default): a gentle bob + breathing-scale
// so static sprites feel alive (AsyncAO-only, viewer-local).
func (p *AssetPreferences) IdleBreathOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.IdleBreath
}

// SetIdleBreath toggles idle breathing.
func (p *AssetPreferences) SetIdleBreath(on bool) { p.setBoolPref(&p.IdleBreath, on) }

// BreathBobOn / BreathScaleOn report whether each component is enabled (both default ON; the
// pref stores the inverted "disabled" flag so the zero value means on).
func (p *AssetPreferences) BreathBobOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.BreathNoBob
}
func (p *AssetPreferences) BreathScaleOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.BreathNoScale
}

// SetBreathBob / SetBreathScale toggle each component (stored inverted).
func (p *AssetPreferences) SetBreathBob(on bool)   { p.setBoolPref(&p.BreathNoBob, !on) }
func (p *AssetPreferences) SetBreathScale(on bool) { p.setBoolPref(&p.BreathNoScale, !on) }

// BreathAmp / BreathSpeed report the sliders [1,100]; 0 (unset) resolves to the default.
func (p *AssetPreferences) BreathAmp() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.BreathAmount == 0 {
		return defaultBreathAmp
	}
	return p.BreathAmount
}

func (p *AssetPreferences) BreathSpeed() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.BreathRate == 0 {
		return defaultBreathSpeed
	}
	return p.BreathRate
}

// SetBreathAmp / SetBreathSpeed store the sliders (clamped to [1,100]).
func (p *AssetPreferences) SetBreathAmp(v int)   { p.setIntPref(&p.BreathAmount, v, 1, 100) }
func (p *AssetPreferences) SetBreathSpeed(v int) { p.setIntPref(&p.BreathRate, v, 1, 100) }

// defaultReflectStrength is the #123 reflection opacity when unset (so the slider shows a
// sensible value and enabling the toggle is visible at once).
const defaultReflectStrength = 30

// ReflectionOn reports the #123 toggle (OFF by default): a flipped, faded glass-floor mirror
// of the sprites.
func (p *AssetPreferences) ReflectionOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Reflection
}

// SetReflection toggles the glass-floor reflection.
func (p *AssetPreferences) SetReflection(on bool) { p.setBoolPref(&p.Reflection, on) }

// ReflectStrength reports the reflection opacity [0,100]; 0 (unset) resolves to the default.
func (p *AssetPreferences) ReflectStrength() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ReflectOpacity == 0 {
		return defaultReflectStrength
	}
	return p.ReflectOpacity
}

// SetReflectStrength stores the reflection opacity (clamped to [0,100]).
func (p *AssetPreferences) SetReflectStrength(v int) { p.setIntPref(&p.ReflectOpacity, v, 0, 100) }

// weatherKindMax is the highest valid weather index (render.WeatherCount-1 = embers); the
// config can't import render, so it's pinned here and a ui test keeps the two equal.
const (
	weatherKindMax        = 4 // None=0, Snow, Rain, Sakura, Embers
	defaultWeatherDensity = 60
)

// WeatherType reports the ambient-weather index (#124); 0 = None (off). Clamped to the valid
// range so a hand-edited pref can't select a non-existent weather.
func (p *AssetPreferences) WeatherType() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.WeatherKind < 0 || p.WeatherKind > weatherKindMax {
		return 0
	}
	return p.WeatherKind
}

// SetWeatherType stores the weather index (clamped to [0,weatherKindMax]).
func (p *AssetPreferences) SetWeatherType(v int) { p.setIntPref(&p.WeatherKind, v, 0, weatherKindMax) }

// stageFrameKindMax is the highest valid stage-frame style index (#56). The style
// list lives in ui (stageFrameNames); a ui contract test keeps the two equal, the
// same arrangement as weatherKindMax above.
const stageFrameKindMax = 7

// StageFrame reports the decorative viewport-frame style (#56); 0 = Off (the
// default). Clamped so a hand-edited pref can't select a non-existent style.
func (p *AssetPreferences) StageFrame() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.StageFrameKind < 0 || p.StageFrameKind > stageFrameKindMax {
		return 0
	}
	return p.StageFrameKind
}

// SetStageFrame stores the stage-frame style (clamped to [0,stageFrameKindMax]).
func (p *AssetPreferences) SetStageFrame(v int) {
	p.setIntPref(&p.StageFrameKind, v, 0, stageFrameKindMax)
}

// WeatherIntensity reports the density slider [1,100]; 0 (unset) resolves to the default.
func (p *AssetPreferences) WeatherIntensity() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.WeatherDensity == 0 {
		return defaultWeatherDensity
	}
	return p.WeatherDensity
}

// SetWeatherIntensity stores the density (clamped to [1,100]).
func (p *AssetPreferences) SetWeatherIntensity(v int) { p.setIntPref(&p.WeatherDensity, v, 1, 100) }

// setBoolPref sets *field to on under the lock, marking dirty only on a real change.
func (p *AssetPreferences) setBoolPref(field *bool, on bool) {
	p.mu.Lock()
	if *field == on {
		p.mu.Unlock()
		return
	}
	*field = on
	p.mu.Unlock()
	p.markDirty()
}

// setIntPref clamps v to [lo,hi] and stores it under the lock, marking dirty only on a real
// change. Shared by the slider-backed prefs.
func (p *AssetPreferences) setIntPref(field *int, v, lo, hi int) {
	v = clampPercent(v, lo, hi)
	p.mu.Lock()
	if *field == v {
		p.mu.Unlock()
		return
	}
	*field = v
	p.mu.Unlock()
	p.markDirty()
}

// SpriteTintColorRGB returns the packed 0xRRGGBB solid-tint colour.
func (p *AssetPreferences) SpriteTintColorRGB() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteTintColor & 0xFFFFFF
}

// SetSpriteTintColor stores a packed 0xRRGGBB solid-tint colour.
func (p *AssetPreferences) SetSpriteTintColor(rgb int) {
	rgb &= 0xFFFFFF
	p.mu.Lock()
	if p.SpriteTintColor == rgb {
		p.mu.Unlock()
		return
	}
	p.SpriteTintColor = rgb
	p.mu.Unlock()
	p.markDirty()
}

// FollowEnabledOn reports the opt-in player-follow toggle (OFF by default): when
// on, each player-list row shows a Follow button that trails them across areas.
func (p *AssetPreferences) FollowEnabledOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FollowEnabled
}

// PlayerListSortMode / PlayerListAreaSortMode return the remembered Players-tab
// sort choices (the player sort, and the /gas area-group order). Stored raw; the
// UI clamps to its current mode count when it seeds a session.
func (p *AssetPreferences) PlayerListSortMode() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PlayerListSort
}

// SetPlayerListSort remembers the Players-tab player sort.
func (p *AssetPreferences) SetPlayerListSort(v int) {
	p.mu.Lock()
	if p.PlayerListSort == v {
		p.mu.Unlock()
		return
	}
	p.PlayerListSort = v
	p.mu.Unlock()
	p.markDirty()
}

// PlayerListAreaSortMode returns the remembered Players-tab area-group order.
func (p *AssetPreferences) PlayerListAreaSortMode() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PlayerListAreaSort
}

// SetPlayerListAreaSort remembers the Players-tab area-group order.
func (p *AssetPreferences) SetPlayerListAreaSort(v int) {
	p.mu.Lock()
	if p.PlayerListAreaSort == v {
		p.mu.Unlock()
		return
	}
	p.PlayerListAreaSort = v
	p.mu.Unlock()
	p.markDirty()
}

// SetFollowEnabled toggles the opt-in player-follow feature.
func (p *AssetPreferences) SetFollowEnabled(on bool) {
	p.mu.Lock()
	if p.FollowEnabled == on {
		p.mu.Unlock()
		return
	}
	p.FollowEnabled = on
	p.mu.Unlock()
	p.markDirty()
}

// ShowPairStatusOn reports whether the player list shows each player's current pair (#20, opt-in).
func (p *AssetPreferences) ShowPairStatusOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShowPairStatus
}

// SetShowPairStatus toggles the opt-in pair-status chip in the player list.
func (p *AssetPreferences) SetShowPairStatus(on bool) {
	p.mu.Lock()
	if p.ShowPairStatus == on {
		p.mu.Unlock()
		return
	}
	p.ShowPairStatus = on
	p.mu.Unlock()
	p.markDirty()
}

// FriendNotifyOn / SetFriendNotify: pop an in-app toast + flash the window when
// a friend speaks (OFF by default).
func (p *AssetPreferences) FriendNotifyOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FriendNotify
}

func (p *AssetPreferences) SetFriendNotify(on bool) {
	p.mu.Lock()
	if p.FriendNotify != on {
		p.FriendNotify = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

// FriendOSToastOn / SetFriendOSToast: pop a DESKTOP (OS) notification when a
// friend speaks (OFF by default; Windows only, rate-limited by the UI).
func (p *AssetPreferences) FriendOSToastOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FriendOSToast
}

func (p *AssetPreferences) SetFriendOSToast(on bool) {
	p.mu.Lock()
	if p.FriendOSToast != on {
		p.FriendOSToast = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

// CallwordOSToastOn / SetCallwordOSToast: pop a DESKTOP (OS) notification when a callword
// is heard while AsyncAO is in the background (#M4; OFF by default; Windows only).
func (p *AssetPreferences) CallwordOSToastOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CallwordOSToast
}

func (p *AssetPreferences) SetCallwordOSToast(on bool) {
	p.mu.Lock()
	if p.CallwordOSToast != on {
		p.CallwordOSToast = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

// ModcallToastOn / SetModcallToast: pop a desktop notification when a modcall
// arrives (for mods who alt-tabbed away). OFF by default; Windows only.
func (p *AssetPreferences) ModcallToastOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModcallToast
}

func (p *AssetPreferences) SetModcallToast(on bool) {
	p.mu.Lock()
	if p.ModcallToast != on {
		p.ModcallToast = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

// FriendGlowPulseOn / SetFriendGlowPulse: gently pulse the friend glow instead
// of a static tint (OFF by default).
func (p *AssetPreferences) FriendGlowPulseOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FriendGlowPulse
}

func (p *AssetPreferences) SetFriendGlowPulse(on bool) {
	p.mu.Lock()
	if p.FriendGlowPulse != on {
		p.FriendGlowPulse = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

// FriendSoundOn / SetFriendSound: play a sound when a friend speaks (OFF by
// default). FriendSoundPath is the custom file ("" = the theme's word_call).
func (p *AssetPreferences) FriendSoundOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FriendSound
}

func (p *AssetPreferences) SetFriendSound(on bool) {
	p.mu.Lock()
	if p.FriendSound != on {
		p.FriendSound = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

// FriendSoundPath / CallwordSoundPath are the custom alert sound files
// ("" = fall back to the theme's word_call). Setters store the trimmed path.
func (p *AssetPreferences) FriendSoundPath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.FriendSoundFile
}

func (p *AssetPreferences) SetFriendSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.mu.Lock()
	p.FriendSoundFile = path
	p.mu.Unlock()
	p.markDirty()
}

// Mod-command feedback sounds (#60, each OFF by default): play a distinct sound
// when a ban / kick / mute happens. Fired by the OOC mod-action scan and the
// kick/ban disconnect; each action has its own toggle and an optional custom
// file ("" = the built-in synthesized default). A duty signal — deliberately
// NOT silenced by DND / streamer mode (consistent with modcalls).
func (p *AssetPreferences) ModBanSFXOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModBanSFX
}

func (p *AssetPreferences) SetModBanSFX(on bool) {
	p.mu.Lock()
	if p.ModBanSFX != on {
		p.ModBanSFX = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

func (p *AssetPreferences) ModKickSFXOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModKickSFX
}

func (p *AssetPreferences) SetModKickSFX(on bool) {
	p.mu.Lock()
	if p.ModKickSFX != on {
		p.ModKickSFX = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

func (p *AssetPreferences) ModMuteSFXOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModMuteSFX
}

func (p *AssetPreferences) SetModMuteSFX(on bool) {
	p.mu.Lock()
	if p.ModMuteSFX != on {
		p.ModMuteSFX = on
		p.mu.Unlock()
		p.markDirty()
		return
	}
	p.mu.Unlock()
}

func (p *AssetPreferences) ModBanSoundPath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModBanSoundFile
}

func (p *AssetPreferences) SetModBanSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.mu.Lock()
	p.ModBanSoundFile = path
	p.mu.Unlock()
	p.markDirty()
}

func (p *AssetPreferences) ModKickSoundPath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModKickSoundFile
}

func (p *AssetPreferences) SetModKickSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.mu.Lock()
	p.ModKickSoundFile = path
	p.mu.Unlock()
	p.markDirty()
}

func (p *AssetPreferences) ModMuteSoundPath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ModMuteSoundFile
}

func (p *AssetPreferences) SetModMuteSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.mu.Lock()
	p.ModMuteSoundFile = path
	p.mu.Unlock()
	p.markDirty()
}

func (p *AssetPreferences) CallwordSoundPath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CallwordSoundFile
}

func (p *AssetPreferences) SetCallwordSoundPath(path string) {
	path = strings.TrimSpace(path)
	p.mu.Lock()
	p.CallwordSoundFile = path
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

// ToolboxSeenOn reports whether the user has already expanded the compact
// bottom-right toolbox at least once (A1). While false the collapsed grip
// wears a faint accent discoverability ring; the first expand latches it true.
func (p *AssetPreferences) ToolboxSeenOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ToolboxSeen
}

// SetToolboxSeen latches the toolbox-discoverability flag (A1). Idempotent: a
// no-op write skips markDirty so a settled draw that re-asserts "seen" never
// spins the debounced saver.
func (p *AssetPreferences) SetToolboxSeen(seen bool) {
	p.mu.Lock()
	if p.ToolboxSeen == seen {
		p.mu.Unlock()
		return
	}
	p.ToolboxSeen = seen
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

// AutoStatus reports the auto-status configuration (#M1: typed words → presence status).
func (p *AssetPreferences) AutoStatus() AutoStatusPref {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MyAutoStatus
}

// SetAutoStatus stores the auto-status configuration (word fields bounded).
func (p *AssetPreferences) SetAutoStatus(a AutoStatusPref) {
	a = sanitizeAutoStatus(a)
	p.mu.Lock()
	if p.MyAutoStatus == a {
		p.mu.Unlock()
		return
	}
	p.MyAutoStatus = a
	p.mu.Unlock()
	p.markDirty()
}

// ChromeTheme reports the AsyncAO chrome theme preset key (#M3; "dark" default).
func (p *AssetPreferences) ChromeTheme() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ChromeThemeKey == "" {
		return "dark"
	}
	return p.ChromeThemeKey
}

// SetChromeTheme stores the chrome theme preset key.
func (p *AssetPreferences) SetChromeTheme(key string) {
	p.mu.Lock()
	if p.ChromeThemeKey == key {
		p.mu.Unlock()
		return
	}
	p.ChromeThemeKey = key
	p.mu.Unlock()
	p.markDirty()
}

// Chrome SHAPE presets (A5): the valid keys for ChromeShapeKey. "sharp" is the
// default and renders byte-identical to today's flat Fill+Border chrome; the
// others reshape kit chrome silhouettes via procedural 9-sliced alpha masks in
// the UI layer. Kept as named constants (not magic strings) and mirrored by the
// UI package's shape* keys — this is the persisted vocabulary, so sanitisation
// happens here and the UI trusts the value it reads back.
const (
	defaultChromeShape = "sharp"   // flat rectangle — the byte-identical default
	chromeShapeRounded = "rounded" // rounded-rectangle corners (radius by tier)
	chromeShapePill    = "pill"    // full-radius pill (corner = min(w,h)/2)
)

// shapeRadiusTiers bounds the corner-radius size classes for the "rounded"
// preset (0..shapeRadiusTiers-1) — a named cap so the derived-mask cache in the
// UI stays bounded (hard rule §17.4) and so ChromeShapeTier can be clamped on
// load. Must equal the UI's shapeRadiusTiers; a mismatch would only ever clamp
// tighter here, never smuggle an out-of-range tier through.
const shapeRadiusTiers = 4

// sanitizeChromeShape maps any stored chrome-shape key onto a known preset,
// collapsing unknown/blank values to the byte-identical default so a corrupt or
// downgraded pref can never leave the chrome in an undrawable state.
func sanitizeChromeShape(key string) string {
	switch key {
	case chromeShapeRounded, chromeShapePill, defaultChromeShape:
		return key
	default:
		return defaultChromeShape
	}
}

// clampChromeShapeTier bounds a stored radius tier into the valid size-class
// range (out-of-range values snap to the nearest end).
func clampChromeShapeTier(tier int) int {
	if tier < 0 {
		return 0
	}
	if tier >= shapeRadiusTiers {
		return shapeRadiusTiers - 1
	}
	return tier
}

// ChromeShape reports the AsyncAO chrome SHAPE preset key (A5; "sharp" default).
// Always returns a sanitised, drawable key.
func (p *AssetPreferences) ChromeShape() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return sanitizeChromeShape(p.ChromeShapeKey)
}

// SetChromeShape stores the chrome shape preset key (sanitised: an unknown key
// is stored as "sharp"). Debounced saver; never writes synchronously.
func (p *AssetPreferences) SetChromeShape(key string) {
	key = sanitizeChromeShape(key)
	p.mu.Lock()
	if p.ChromeShapeKey == key {
		p.mu.Unlock()
		return
	}
	p.ChromeShapeKey = key
	p.mu.Unlock()
	p.markDirty()
}

// ChromeShapeTier reports the corner-radius size class for the "rounded" preset
// (0..shapeRadiusTiers-1), always clamped in range.
func (p *AssetPreferences) ChromeShapeTier() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return clampChromeShapeTier(p.ChromeShapeTierIdx)
}

// SetChromeShapeTier stores the corner-radius size class (clamped in range).
func (p *AssetPreferences) SetChromeShapeTier(tier int) {
	tier = clampChromeShapeTier(tier)
	p.mu.Lock()
	if p.ChromeShapeTierIdx == tier {
		p.mu.Unlock()
		return
	}
	p.ChromeShapeTierIdx = tier
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

// AddCallWord adds one or more highlight words from a single input, split on
// commas so a pasted "a, b, c" adds them all at once. Each is lowercased +
// trimmed; blanks and case-insensitive duplicates are skipped, up to
// callWordCap. Returns how many were newly added (0 = nothing new / at the cap),
// mirroring AddMusicHost for the Settings list manager.
func (p *AssetPreferences) AddCallWord(input string) int {
	p.mu.Lock()
	have := make(map[string]bool, len(p.CallWordList))
	for _, w := range p.CallWordList {
		have[w] = true
	}
	added := 0
	for _, raw := range strings.Split(input, ",") {
		w := strings.ToLower(strings.TrimSpace(raw))
		if w == "" || have[w] {
			continue
		}
		if len(p.CallWordList) >= callWordCap {
			break
		}
		p.CallWordList = append(p.CallWordList, w)
		have[w] = true
		added++
	}
	p.mu.Unlock()
	if added > 0 {
		p.markDirty()
	}
	return added
}

// RemoveCallWord drops a highlight word (case-insensitive). Reports a change.
func (p *AssetPreferences) RemoveCallWord(word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	p.mu.Lock()
	changed := false
	for i, w := range p.CallWordList {
		if w == word {
			p.CallWordList = append(p.CallWordList[:i], p.CallWordList[i+1:]...)
			changed = true
			break
		}
	}
	p.mu.Unlock()
	if changed {
		p.markDirty()
	}
	return changed
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

// ServerFriends returns a server's highlighted shownames (a clone — the
// settings editor mutates it; callers must not alias the stored slice).
func (p *AssetPreferences) ServerFriends(key string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneStrings(p.ServerWarm[key].Friends)
}

// friendParts splits a friend entry "name[=RRGGBB[=nick]]" into its three fields
// (#82). SplitN with 3 keeps the colour parse robust even if the nickname
// itself contains an "=". Trimmed; missing fields come back "".
func friendParts(entry string) (name, hex, nick string) {
	parts := strings.SplitN(entry, "=", 3)
	name = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		hex = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		nick = strings.TrimSpace(parts[2])
	}
	return
}

// ServerFriendInfo reports whether name (case-insensitive) is a highlighted
// friend on the server, plus that friend's custom glow colour (packed 0xRRGGBB,
// or -1 for the default tint) and personal nickname ("" if none) — #82. ONE scan
// under the lock with no allocation, so the player list (per row) and friend
// matching (per message) read all three from a single call.
func (p *AssetPreferences) ServerFriendInfo(key, name string) (friend bool, color int, nick string) {
	if name == "" {
		return false, -1, ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, f := range p.ServerWarm[key].Friends {
		fname, hex, nk := friendParts(f)
		if !strings.EqualFold(fname, name) {
			continue
		}
		color = -1
		if hex != "" {
			if v, err := strconv.ParseInt(strings.TrimPrefix(hex, "#"), 16, 32); err == nil {
				color = int(v) & 0xFFFFFF
			}
		}
		return true, color, nk
	}
	return false, -1, ""
}

// ServerFriendMatch reports whether name is a highlighted friend and its custom
// glow colour — a thin wrapper over ServerFriendInfo (one scan, no nested lock)
// kept for the per-message/glow callers that don't need the nickname.
func (p *AssetPreferences) ServerFriendMatch(key, name string) (friend bool, color int) {
	f, c, _ := p.ServerFriendInfo(key, name)
	return f, c
}

// SetServerFriends replaces a server's highlighted-showname list — entries are
// "name", "name=RRGGBB" (per-friend glow colour), or "name=RRGGBB=nick" (#82, a
// personal nickname). Each is re-canonicalised: trimmed, blanks dropped, deduped
// by NAME (case-insensitive), capped at WarmFriendsCap. Commas are STRIPPED from
// the nickname — the editor stores the list comma-separated, so a comma in a
// nick would split into two broken entries on the next round-trip.
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

// ServerIgnored returns a server's blocked shownames (a clone — the settings
// editor mutates it; callers must not alias the stored slice).
func (p *AssetPreferences) ServerIgnored(key string) []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneStrings(p.ServerWarm[key].Ignored)
}

// ServerIgnoreMatch reports whether name (case-insensitive) is on the server's
// ignore list. Scans under the lock with NO allocation — safe to call per
// incoming IC/OOC message (an empty list, the default, costs one RLock + zero
// iterations).
func (p *AssetPreferences) ServerIgnoreMatch(key, name string) bool {
	if name == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, n := range p.ServerWarm[key].Ignored {
		if strings.EqualFold(strings.TrimSpace(n), name) {
			return true
		}
	}
	return false
}

// SetServerIgnored replaces a server's ignore list — trimmed, blanks dropped,
// deduped case-insensitively, capped at WarmIgnoredCap.
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

// ServerAudio returns a server's per-server audio override. on=false means the
// global mixer volumes apply; the four levels are 0–100 (master scales the rest).
// The "sandbox each tab's sound" feature — applied while this server's tab is active.
func (p *AssetPreferences) ServerAudio(key string) (on bool, master, music, sfx, blip int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	w := p.ServerWarm[key]
	// Each channel: this server's explicit level if it set one, else the global default
	// (so an unset channel — e.g. SFX a prior build never wrote — is never silently 0).
	return w.AudioOn,
		ptrOr(w.AudioMaster, p.MasterVol),
		ptrOr(w.AudioMusic, p.MusicVol),
		ptrOr(w.AudioSFX, p.SFXVol),
		ptrOr(w.AudioBlip, p.BlipVol)
}

// ptrOr returns *v when set, else the fallback — the per-channel "unset → global default".
func ptrOr(v *int, def int) int {
	if v != nil {
		return *v
	}
	return def
}

// SetServerAudioOn toggles one server's per-server audio override.
func (p *AssetPreferences) SetServerAudioOn(key string, on bool) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.AudioOn = on })
}

// SetServerAudioVolumes records a server's per-server mixer volumes (0–100 each). All
// four channels are written explicitly (as pointers), so once a server has a profile
// every channel is a real chosen value — none silently defaults to a muted 0.
func (p *AssetPreferences) SetServerAudioVolumes(key string, master, music, sfx, blip int) {
	m := clampPercent(master, 0, defaultAudioVolume)
	mu := clampPercent(music, 0, defaultAudioVolume)
	s := clampPercent(sfx, 0, defaultAudioVolume)
	b := clampPercent(blip, 0, defaultAudioVolume)
	p.rememberServer(key, func(w *ServerWarmInfo) {
		w.AudioOn = true // adjusting volumes makes this server's own profile "exist"
		w.AudioMaster, w.AudioMusic, w.AudioSFX, w.AudioBlip = &m, &mu, &s, &b
	})
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

// RememberServerBackgrounds caches a server's discovered background list so the
// next session's picker/slideshow seed instantly (capped at WarmBgsCap).
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

// SetHiddenPanels replaces the hidden-chrome set. Dedups blank/duplicate ids and
// bounds the slice at maxHiddenPanels (rule §17.4 — it was previously uncapped).
func (p *AssetPreferences) SetHiddenPanels(ids []string) {
	clean := sanitizeHiddenPanels(ids)
	p.mu.Lock()
	p.HiddenPanelIDs = clean
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

// OOCScale reports the OOC log text size percent (independent of the IC log).
func (p *AssetPreferences) OOCScale() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.OOCScalePct
}

// SetOOCScale clamps and persists the OOC log text size.
func (p *AssetPreferences) SetOOCScale(pct int) {
	pct = clampPercent(pct, MinLogScalePercent, MaxLogScalePercent)
	p.mu.Lock()
	if p.OOCScalePct == pct {
		p.mu.Unlock()
		return
	}
	p.OOCScalePct = pct
	p.mu.Unlock()
	p.markDirty()
}

// CustomChrome returns the user's "Custom" chrome scheme as 7 hex strings
// (bg, panel, panelHi, accent, text, textDim, danger); a blank slot means
// "use the stock dark colour". Active only while ChromeTheme is "custom".
func (p *AssetPreferences) CustomChrome() [7]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CustomChromeHex
}

// SetCustomChrome stores the 7-colour custom chrome scheme (hex strings).
func (p *AssetPreferences) SetCustomChrome(hex [7]string) {
	p.mu.Lock()
	if p.CustomChromeHex == hex {
		p.mu.Unlock()
		return
	}
	p.CustomChromeHex = hex
	p.mu.Unlock()
	p.markDirty()
}

// LayoutPartColorCount is the number of individually-tintable layout parts
// (v1.52.0, Tifera: "color individual parts of the layout"). The ui package
// owns the index meanings (log / OOC / emote grid / chatbox); this package
// just stores the hex slots, blank = use the chrome default.
const LayoutPartColorCount = 4

// LayoutPartColors returns the per-part hex overrides (rrggbb; "" = default).
func (p *AssetPreferences) LayoutPartColors() [LayoutPartColorCount]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.LayoutPartHex
}

// SetLayoutPartColor stores one part's hex override ("" clears it back to the
// chrome default). Out-of-range indices are ignored.
func (p *AssetPreferences) SetLayoutPartColor(i int, hex string) {
	if i < 0 || i >= LayoutPartColorCount {
		return
	}
	p.mu.Lock()
	if p.LayoutPartHex[i] == hex {
		p.mu.Unlock()
		return
	}
	p.LayoutPartHex[i] = hex
	p.mu.Unlock()
	p.markDirty()
}

// BoldNamesOn reports whether speaker names render bold (IC/OOC log + chatbox).
// Default ON — the pref is stored inverted (BoldNamesOff) so an absent value
// means bold, matching the on-by-default behaviour.
func (p *AssetPreferences) BoldNamesOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.BoldNamesOff
}

// SetBoldNames toggles bold speaker names.
func (p *AssetPreferences) SetBoldNames(on bool) {
	p.mu.Lock()
	if p.BoldNamesOff == !on {
		p.mu.Unlock()
		return
	}
	p.BoldNamesOff = !on
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

// WindowSize reports the saved windowed size (0,0 = use the default). Clamp to
// the display at apply (ClampWindowSize) — a raw saved value may be stale.
func (p *AssetPreferences) WindowSize() (int, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.WindowW, p.WindowH
}

// SetWindowSize persists the windowed size (floored at the minimum; the display
// ceiling is applied where the size is used, since prefs are SDL-free).
func (p *AssetPreferences) SetWindowSize(w, h int) {
	if w < MinWindowW {
		w = MinWindowW
	}
	if h < MinWindowH {
		h = MinWindowH
	}
	p.mu.Lock()
	if p.WindowW == w && p.WindowH == h {
		p.mu.Unlock()
		return
	}
	p.WindowW, p.WindowH = w, h
	p.mu.Unlock()
	p.markDirty()
}

// WindowFullscreen reports whether the client launches in borderless fullscreen.
func (p *AssetPreferences) WindowFullscreen() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.WindowFull
}

// SetWindowFullscreen persists the fullscreen choice.
func (p *AssetPreferences) SetWindowFullscreen(on bool) {
	p.mu.Lock()
	if p.WindowFull == on {
		p.mu.Unlock()
		return
	}
	p.WindowFull = on
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

// AlertVolume reports the callword/friend ping volume (0–100), independent of
// SFX so quietening or muting SFX never silences your name-pings.
func (p *AssetPreferences) AlertVolume() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AlertVol
}

// SetAlertVolume clamps and persists the alert (callword/friend) volume.
func (p *AssetPreferences) SetAlertVolume(v int) {
	v = clampPercent(v, 0, defaultAudioVolume)
	p.mu.Lock()
	if p.AlertVol == v {
		p.mu.Unlock()
		return
	}
	p.AlertVol = v
	p.mu.Unlock()
	p.markDirty()
}

// MasterVolume reports the master multiplier (0–100) that scales music/SFX/blip
// together — the single "make everything quieter/louder" control.
func (p *AssetPreferences) MasterVolume() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MasterVol
}

// SetMasterVolume clamps and persists the master volume multiplier.
func (p *AssetPreferences) SetMasterVolume(v int) {
	v = clampPercent(v, 0, defaultAudioVolume)
	p.mu.Lock()
	if p.MasterVol == v {
		p.mu.Unlock()
		return
	}
	p.MasterVol = v
	p.mu.Unlock()
	p.markDirty()
}

// HoldClear reports the hold-to-clear settings: whether it's on, the key name
// (SDL key name, e.g. "Backspace"), and the hold duration in ms.
func (p *AssetPreferences) HoldClear() (on bool, key string, ms int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HoldClearOn, p.HoldClearKey, p.HoldClearMs
}

// SetHoldClearOn toggles hold-to-clear.
func (p *AssetPreferences) SetHoldClearOn(on bool) {
	p.mu.Lock()
	if p.HoldClearOn == on {
		p.mu.Unlock()
		return
	}
	p.HoldClearOn = on
	p.mu.Unlock()
	p.markDirty()
}

// SetHoldClearKey rebinds the hold-to-clear key (an SDL key name; "" ignored).
func (p *AssetPreferences) SetHoldClearKey(key string) {
	if key == "" {
		return
	}
	p.mu.Lock()
	if p.HoldClearKey == key {
		p.mu.Unlock()
		return
	}
	p.HoldClearKey = key
	p.mu.Unlock()
	p.markDirty()
}

// SetHoldClearMs clamps and persists the hold-to-clear duration.
func (p *AssetPreferences) SetHoldClearMs(ms int) {
	ms = clampPercent(ms, MinHoldClearMs, MaxHoldClearMs)
	p.mu.Lock()
	if p.HoldClearMs == ms {
		p.mu.Unlock()
		return
	}
	p.HoldClearMs = ms
	p.mu.Unlock()
	p.markDirty()
}

// ExtrasBoxStyle reports the Extras-box theme: background / gradient-bottom /
// border / title / text colours (hex, "" = stock) and whether the background is
// a gradient.
func (p *AssetPreferences) ExtrasBoxStyle() (bg, bg2, border, title, text string, gradient bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ExtrasBg, p.ExtrasBg2, p.ExtrasBorder, p.ExtrasTitle, p.ExtrasText, p.ExtrasGradient
}

// SetExtrasBoxStyle persists the Extras-box theme.
func (p *AssetPreferences) SetExtrasBoxStyle(bg, bg2, border, title, text string, gradient bool) {
	p.mu.Lock()
	p.ExtrasBg, p.ExtrasBg2, p.ExtrasBorder, p.ExtrasTitle, p.ExtrasText, p.ExtrasGradient =
		bg, bg2, border, title, text, gradient
	p.mu.Unlock()
	p.markDirty()
}

// AreaHighlightColorHex is the current-area row highlight colour (hex like
// "4ac96c"; "" = the stock green). The ui parses it, falling back to the
// default on any malformed value.
func (p *AssetPreferences) AreaHighlightColorHex() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AreaHighlightHex
}

// SetAreaHighlightColorHex persists the current-area highlight colour ("" =
// back to the stock green).
func (p *AssetPreferences) SetAreaHighlightColorHex(hex string) {
	p.mu.Lock()
	if p.AreaHighlightHex == hex {
		p.mu.Unlock()
		return
	}
	p.AreaHighlightHex = hex
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

// RestoreTabsOn reports the "reopen my server tabs on launch" toggle (OFF by
// default). When on, the open tabs are saved on exit and reconnected next time.
func (p *AssetPreferences) RestoreTabsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.RestoreTabs
}

// SetRestoreTabs toggles restore-on-launch.
func (p *AssetPreferences) SetRestoreTabs(on bool) {
	p.mu.Lock()
	if p.RestoreTabs == on {
		p.mu.Unlock()
		return
	}
	p.RestoreTabs = on
	p.mu.Unlock()
	p.markDirty()
}

// VolStripShownOn reports the log panel's on-screen volume-strip toggle (OFF by
// default). Persisted so it survives a restart (playtest: it used to reset every launch).
func (p *AssetPreferences) VolStripShownOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.VolStripOn
}

// SetVolStripShown persists the on-screen volume-strip toggle.
func (p *AssetPreferences) SetVolStripShown(on bool) {
	p.mu.Lock()
	if p.VolStripOn == on {
		p.mu.Unlock()
		return
	}
	p.VolStripOn = on
	p.mu.Unlock()
	p.markDirty()
}

// Cold-load sprite behaviour (SpriteLoadMode): what a character layer shows while
// an uncached sprite is still streaming + decoding. The values MUST equal
// render.SpriteLoad* (the UI mirrors the pref straight into the viewport); kept as
// plain ints here to avoid a config→render import.
const (
	SpriteLoadBlank     = 0 // draw nothing until the sprite lands (the original behaviour; the cold-load flash)
	SpriteLoadHoldPrev  = 1 // keep the previous sprite until the new one lands (webAO-style; the default)
	SpriteLoadWait      = 2 // hold the MESSAGE off-stage until its sprite decodes (client-AO-style; courtroom wait gate, timeout-capped)
	SpriteLoadModeCount = 3 // number of valid modes (for the Settings cycle button + load clamp)

	// defaultSpriteLoadMode is what a fresh install (and a power-user reset) uses:
	// hold-previous, so a cold idle↔talk sprite swap bridges with the last good frame
	// instead of the ~¼-second empty flash SpriteLoadBlank leaves (the playtest report,
	// worst on packs whose idle and talk are one bare sprite — the swap should be a
	// no-op but each spelling is a separate T1 key, so the gap reads as a pure blink).
	defaultSpriteLoadMode = SpriteLoadHoldPrev
)

// Wait-mode timeout bounds (SpriteLoadWait): how long one message may be held for
// its sprite before playing anyway. User-tunable within [min,max] — deliberately a
// WIDE range (power user: from a near-instant 50 ms nudge to a patient 30 s on a
// dial-up-grade link); the default is generous enough for a big sprite on a slow
// link without stalling conversation.
const (
	SpriteWaitDefaultMs = 1500
	SpriteWaitMinMs     = 50
	SpriteWaitMaxMs     = 30000
)

// SpriteLoadMode reports the power-user cold-load sprite behaviour (SpriteLoadBlank
// default). It never affects a cached sprite — only what shows during the fetch/
// decode gap for an uncached one.
func (p *AssetPreferences) SpriteLoadMode() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteLoadModeVal
}

// SetSpriteLoadMode persists the cold-load sprite behaviour (clamped to a known mode).
func (p *AssetPreferences) SetSpriteLoadMode(mode int) {
	if mode < SpriteLoadBlank || mode >= SpriteLoadModeCount {
		mode = SpriteLoadBlank
	}
	p.mu.Lock()
	if p.SpriteLoadModeVal == mode {
		p.mu.Unlock()
		return
	}
	p.SpriteLoadModeVal = mode
	p.mu.Unlock()
	p.markDirty()
}

// SpriteWaitMs reports the wait-mode hold cap in milliseconds (SpriteWaitDefaultMs
// when unset): how long SpriteLoadWait may hold one message for its sprite before
// playing anyway.
func (p *AssetPreferences) SpriteWaitMs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.SpriteWaitMsVal == 0 {
		return SpriteWaitDefaultMs
	}
	return p.SpriteWaitMsVal
}

// SetSpriteWaitMs persists the wait-mode hold cap (clamped to the tunable range).
func (p *AssetPreferences) SetSpriteWaitMs(ms int) {
	ms = clampPercent(ms, SpriteWaitMinMs, SpriteWaitMaxMs)
	p.mu.Lock()
	if p.SpriteWaitMsVal == ms {
		p.mu.Unlock()
		return
	}
	p.SpriteWaitMsVal = ms
	p.mu.Unlock()
	p.markDirty()
}

// Bounds for the remaining renderer / core-timing power knobs. Every "0 = default"
// sentinel keeps the pref file byte-identical for anyone who never touches them.
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

	// Thumbnail-cache knobs (opt-in low-q sprite stand-ins). The defaults land
	// ~1 KB per sprite: visibly low-quality by design — instantly recognisable,
	// never mistaken for the real art.
	ThumbHeightDefaultPx = 64  // matches assets.ThumbHeightDefault (pinned by test)
	ThumbHeightMinPx     = 32  // barely a silhouette
	ThumbHeightMaxPx     = 160 // "low-q" stops meaning anything past this
	ThumbQualityDefault  = 20  // matches assets.ThumbQualityDefault (pinned by test)
	ThumbQualityMin      = 5   // maximum crunch
	ThumbQualityMax      = 60  // still clearly a placeholder

	// Thumb-store byte budget (auto-prunes oldest past it). At ~1 KB each the
	// 64 MiB default is ~60k thumbnails — effectively "everything you've seen".
	ThumbBudgetDefaultMiB = 64
	ThumbBudgetMinMiB     = 8
	ThumbBudgetMaxMiB     = 512

	// T3 disk-cache auto-prune cap (#34). 0 = UNLIMITED, the DEFAULT — T3's
	// unboundedness is a deliberate spec exception, so an update never silently
	// deletes a user's cache. A positive cap makes the writer goroutine sweep
	// the oldest blobs past it. A single character on a 4000-char server runs
	// tens of MiB, so the floor is generous; the ceiling covers a whole disk.
	DiskCacheBudgetMinMiB = 128   // smallest meaningful cap
	DiskCacheBudgetMaxMiB = 65536 // 64 GiB — a very large asset library

	// Negative-cache (404) TTL knob: how long a missing asset stays "missing"
	// before a re-probe is allowed. RESTART-applied (the LRU takes its TTL at
	// construction). Shorter = a server admin uploading a missing sprite shows
	// up sooner; longer = fewer wasted probes on genuinely-absent art.
	NotFoundTTLMinSec = 30
	NotFoundTTLMaxSec = 3600

	// Per-host adaptive-deadline multiple: request deadline = N × the host's
	// TTFB EWMA, clamped. Lower sheds a degrading mirror sooner (risking
	// spurious timeouts on jittery links); higher is more patient.
	AdaptiveLatMultipleMin = 2
	AdaptiveLatMultipleMax = 32

	// Decode-downscale override: the automatic CatmullRom cap targets the
	// display height; this scales that target (100 = default). Lower = smaller
	// textures (less VRAM, softer sprites); higher keeps more source detail
	// for heavy zoom users. SpriteDownscaleOff disables the cap entirely.
	SpriteDownscaleMinPct = 50
	SpriteDownscaleMaxPct = 200

	// T1 texture byte budget, MiB. RESTART-applied. ⚠ T1 + T2 (128 MiB) live
	// inside the 256 MiB memory budget. The default (64) and everything up to
	// TexBudgetSafeMaxMiB keep the WHOLE client under budget; the max is raised
	// to 256 only as an EXPERIMENTAL, default-off, warned opt-in so a long
	// animation can decode past the ~5 s truncation — above the safe max it
	// deliberately exceeds the budget (trades peak memory, may stutter/crash on
	// low-RAM machines). The default is unchanged: normal users never leave 64.
	TexBudgetDefaultMiB = 64
	TexBudgetMinMiB     = 32
	TexBudgetMaxMiB     = 256
	// TexBudgetSafeMaxMiB is the largest budget that still fits the 256 MiB
	// memory target; the Settings row marks everything above it experimental.
	TexBudgetSafeMaxMiB = 128

	// Speaker-swap crossfade: the new sprite fades in over N ms (0 = off, the
	// default hard swap). Suppressed by Reduce motion.
	CrossfadeMinMs = 50
	CrossfadeMaxMs = 1000

	// Frame pacing (the GPU-burn fix): the loop used to re-render the whole UI
	// every monitor refresh — on a 144/165 Hz laptop panel that's a full-screen
	// GPU composite 165×/sec while IDLE (fans, +10 °C — the playtest report).
	// Three caps, all sleep-based (vsync stays on for tear-free presents):
	// foreground (the ceiling), idle (nothing animating), unfocused (another
	// window has focus; minimized already naps).
	FPSCapDefault = FPSUnlimited // Active default = ∞ / vsync-paced (the playtesters' hard default; the ceiling only bites if you type a number or drag off ∞)
	FPSCapMin     = 1
	FPSCapMax     = 240
	// FPSCapUnlimitedOff is the concrete Active cap the Settings "∞" toggle drops
	// to — now that the default IS ∞, using the default there would just re-pick
	// unlimited and the toggle would do nothing.
	FPSCapUnlimitedOff  = 60
	IdleFPSDefault      = FPSOff // idle rendering OFF by default: a static screen redraws zero times (0 GPU) and leans on real events + scheduled deadlines
	IdleFPSMin          = 1
	IdleFPSMax          = 120
	UnfocusedFPSDefault = 5 // low unfocused ceiling: a tabbed-out window renders at most this (the background cap is a hard ceiling now)
	UnfocusedFPSMin     = 1
	UnfocusedFPSMax     = 60

	// InputGraceFrames: how long full rate is held after a click / keypress, in
	// frames (at a 60 fps reference). 1 (the default) = snappiest — the input's
	// own frame shows, then the rate falls straight back to idle; the max restores
	// the old ~1 s hold; 0 (InputGraceOff) turns the hold OFF entirely (just the
	// input's own frame). Mouse MOTION is unaffected (its own short grace).
	InputGraceFramesDefault = 1
	InputGraceFramesMin     = 1
	InputGraceFramesMax     = 60
	// InputGraceOff is the STORED sentinel for a 0-frame hold — kept distinct from
	// 0, which stays "absent → the default" so an unset pref loads as 1, not off.
	InputGraceOff = -1

	// FPSUnlimited is the "no limit" sentinel any of the three rate knobs may
	// hold (the Settings ∞ toggle): the active cap stops capping (vsync paces
	// the presents), and an unlimited idle/unfocused rate means that state is
	// never throttled below the active pacing. Distinct from 0 = "the default".
	FPSUnlimited = -1

	// FPSOff is the "never redraw in this state" sentinel for the Idle and
	// Background knobs (the Settings 0 / bottom-of-slider position). The loop
	// stops issuing periodic idle redraws in that state and waits purely on real
	// damage + scheduled deadlines (caret flip, clock tick, animation frame).
	// Distinct from FPSUnlimited (uncapped) and from 0, which stays "the shipped
	// default" on the wire — so an ABSENT key loads as the default, not off, and
	// only an explicit choice writes FPSOff. The Active cap has no "off" (0 fps
	// while interacting is meaningless), so the Active row never emits it.
	FPSOff = -2
)

// normalizeFPSPref maps a stored rate knob onto its valid domain: 0 keeps the
// default (the getter resolves it), FPSUnlimited (∞) and FPSOff (never redraw)
// pass through as-is, anything else clamps to [min, max]. Any other negative is
// treated as ∞ defensively. Shared by the setters and the disk-load overlay so
// a hand-edited file obeys the same rules as the sliders.
func normalizeFPSPref(fps, min, max int) int {
	switch fps {
	case 0, FPSUnlimited, FPSOff:
		return fps
	}
	if fps < 0 {
		return FPSUnlimited
	}
	return clampPercent(fps, min, max)
}

// SpriteWaitPairOn / SpriteWaitPreanimOn report the wait-mode strictness knobs
// (both OFF by default): also hold a message for the pair partner's idle sprite /
// its preanimation.
func (p *AssetPreferences) SpriteWaitPairOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteWaitPair
}

// SetSpriteWaitPair persists the wait-covers-pair knob.
func (p *AssetPreferences) SetSpriteWaitPair(on bool) { p.setBoolPref(&p.SpriteWaitPair, on) }

// SpriteWaitPreanimOn reports the wait-covers-preanim knob.
func (p *AssetPreferences) SpriteWaitPreanimOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteWaitPreanim
}

// SetSpriteWaitPreanim persists the wait-covers-preanim knob.
func (p *AssetPreferences) SetSpriteWaitPreanim(on bool) { p.setBoolPref(&p.SpriteWaitPreanim, on) }

// HoldPrevMaxAgeMs reports how long hold-previous may bridge a cold sprite before
// giving up to blank (0 = bridge forever, the default).
func (p *AssetPreferences) HoldPrevMaxAgeMs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HoldPrevMaxAgeMsVal
}

// SetHoldPrevMaxAgeMs persists the stand-in cap (0 = forever; else clamped).
func (p *AssetPreferences) SetHoldPrevMaxAgeMs(ms int) {
	if ms != 0 {
		ms = clampPercent(ms, HoldPrevMaxAgeMinMs, HoldPrevMaxAgeMaxMs)
	}
	p.mu.Lock()
	if p.HoldPrevMaxAgeMsVal == ms {
		p.mu.Unlock()
		return
	}
	p.HoldPrevMaxAgeMsVal = ms
	p.mu.Unlock()
	p.markDirty()
}

// HoldDebugTintOn reports the stand-in diagnostic tint (OFF by default).
func (p *AssetPreferences) HoldDebugTintOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HoldDebugTint
}

// SetHoldDebugTint persists the stand-in tint knob.
func (p *AssetPreferences) SetHoldDebugTint(on bool) { p.setBoolPref(&p.HoldDebugTint, on) }

// ShoutDurationMs reports the shout-bubble hold in milliseconds (0 = the
// canonical courtroom default — see courtroom.DefaultShoutDuration).
func (p *AssetPreferences) ShoutDurationMs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ShoutDurationMsVal
}

// SetShoutDurationMs persists the shout hold (0 = default; else clamped).
func (p *AssetPreferences) SetShoutDurationMs(ms int) {
	if ms != 0 {
		ms = clampPercent(ms, ShoutDurationMinMs, ShoutDurationMaxMs)
	}
	p.mu.Lock()
	if p.ShoutDurationMsVal == ms {
		p.mu.Unlock()
		return
	}
	p.ShoutDurationMsVal = ms
	p.mu.Unlock()
	p.markDirty()
}

// PreanimTimeoutMs reports the preanim wait cap in milliseconds (0 = the
// canonical courtroom default — see courtroom.DefaultPreanimTimeout).
func (p *AssetPreferences) PreanimTimeoutMs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PreanimTimeoutMsVal
}

// SetPreanimTimeoutMs persists the preanim cap (0 = default; else clamped).
func (p *AssetPreferences) SetPreanimTimeoutMs(ms int) {
	if ms != 0 {
		ms = clampPercent(ms, PreanimTimeoutMinMs, PreanimTimeoutMaxMs)
	}
	p.mu.Lock()
	if p.PreanimTimeoutMsVal == ms {
		p.mu.Unlock()
		return
	}
	p.PreanimTimeoutMsVal = ms
	p.mu.Unlock()
	p.markDirty()
}

// ICQueueCap reports the IC backlog queue depth (0 = the courtroom's canonical
// default, 64). How many unplayed messages a packed room may stack before the
// oldest drops (the IC log records everything regardless).
func (p *AssetPreferences) ICQueueCap() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ICQueueCapVal
}

// SetICQueueCap persists the queue depth (0 = default; else clamped).
func (p *AssetPreferences) SetICQueueCap(n int) {
	if n != 0 {
		n = clampPercent(n, ICQueueCapMin, ICQueueCapMax)
	}
	p.mu.Lock()
	if p.ICQueueCapVal == n {
		p.mu.Unlock()
		return
	}
	p.ICQueueCapVal = n
	p.mu.Unlock()
	p.markDirty()
}

// CatchUpLingerMs reports how long each fast-forwarded backlog message lingers
// while packed-room catch-up drains (default 0 = one per frame).
func (p *AssetPreferences) CatchUpLingerMs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CatchUpLingerMsVal
}

// SetCatchUpLingerMs persists the catch-up linger (clamped; 0 = the default).
func (p *AssetPreferences) SetCatchUpLingerMs(ms int) {
	ms = clampPercent(ms, 0, CatchUpLingerMaxMs)
	p.mu.Lock()
	if p.CatchUpLingerMsVal == ms {
		p.mu.Unlock()
		return
	}
	p.CatchUpLingerMsVal = ms
	p.mu.Unlock()
	p.markDirty()
}

// ThumbCacheOn reports the opt-in persistent low-quality sprite thumbnail cache
// (OFF by default): every character sprite that decodes leaves a tiny (~1 KB)
// heavily-compressed still behind, shown instantly as a stand-in when that
// sprite is next needed cold.
func (p *AssetPreferences) ThumbCacheOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ThumbCache
}

// SetThumbCache persists the thumbnail-cache toggle.
func (p *AssetPreferences) SetThumbCache(on bool) { p.setBoolPref(&p.ThumbCache, on) }

// ThumbHeightPx reports the thumbnail height (ThumbHeightDefaultPx when unset).
func (p *AssetPreferences) ThumbHeightPx() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ThumbHeightPxVal == 0 {
		return ThumbHeightDefaultPx
	}
	return p.ThumbHeightPxVal
}

// SetThumbHeightPx persists the thumbnail height (0 = default; else clamped).
func (p *AssetPreferences) SetThumbHeightPx(px int) {
	if px != 0 {
		px = clampPercent(px, ThumbHeightMinPx, ThumbHeightMaxPx)
	}
	p.mu.Lock()
	if p.ThumbHeightPxVal == px {
		p.mu.Unlock()
		return
	}
	p.ThumbHeightPxVal = px
	p.mu.Unlock()
	p.markDirty()
}

// ThumbQuality reports the thumbnail WebP quality (ThumbQualityDefault when unset).
func (p *AssetPreferences) ThumbQuality() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ThumbQualityVal == 0 {
		return ThumbQualityDefault
	}
	return p.ThumbQualityVal
}

// SetThumbQuality persists the thumbnail quality (0 = default; else clamped).
func (p *AssetPreferences) SetThumbQuality(q int) {
	if q != 0 {
		q = clampPercent(q, ThumbQualityMin, ThumbQualityMax)
	}
	p.mu.Lock()
	if p.ThumbQualityVal == q {
		p.mu.Unlock()
		return
	}
	p.ThumbQualityVal = q
	p.mu.Unlock()
	p.markDirty()
}

// ThumbBudgetMiB reports the thumbnail store's byte budget in MiB
// (ThumbBudgetDefaultMiB when unset); the store auto-prunes oldest past it.
func (p *AssetPreferences) ThumbBudgetMiB() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ThumbBudgetMiBVal == 0 {
		return ThumbBudgetDefaultMiB
	}
	return p.ThumbBudgetMiBVal
}

// SetThumbBudgetMiB persists the thumb-store budget (0 = default; else clamped).
func (p *AssetPreferences) SetThumbBudgetMiB(mib int) {
	if mib != 0 {
		mib = clampPercent(mib, ThumbBudgetMinMiB, ThumbBudgetMaxMiB)
	}
	p.mu.Lock()
	if p.ThumbBudgetMiBVal == mib {
		p.mu.Unlock()
		return
	}
	p.ThumbBudgetMiBVal = mib
	p.mu.Unlock()
	p.markDirty()
}

// DiskCacheBudgetMiB reports the T3 disk-cache auto-prune cap in MiB (#34).
// 0 = UNLIMITED, the default — T3's unboundedness is a deliberate spec
// exception, so an update never silently deletes a user's cache.
func (p *AssetPreferences) DiskCacheBudgetMiB() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DiskCacheBudgetMiBVal
}

// SetDiskCacheBudgetMiB persists the T3 prune cap (0 = unlimited; else clamped
// to [DiskCacheBudgetMinMiB, DiskCacheBudgetMaxMiB]). The debounced saver
// flushes the change; the caller applies it to the live cache via
// Manager.SetDiskBudget.
func (p *AssetPreferences) SetDiskCacheBudgetMiB(mib int) {
	if mib != 0 {
		mib = clampPercent(mib, DiskCacheBudgetMinMiB, DiskCacheBudgetMaxMiB)
	}
	p.mu.Lock()
	if p.DiskCacheBudgetMiBVal == mib {
		p.mu.Unlock()
		return
	}
	p.DiskCacheBudgetMiBVal = mib
	p.mu.Unlock()
	p.markDirty()
}

// NotFoundTTLSec reports the 404 negative-cache TTL override in seconds
// (0 = the network default; applies on restart).
func (p *AssetPreferences) NotFoundTTLSec() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.NotFoundTTLSecVal
}

// SetNotFoundTTLSec persists the 404 TTL (0 = default; else clamped).
func (p *AssetPreferences) SetNotFoundTTLSec(sec int) {
	if sec != 0 {
		sec = clampPercent(sec, NotFoundTTLMinSec, NotFoundTTLMaxSec)
	}
	p.mu.Lock()
	if p.NotFoundTTLSecVal == sec {
		p.mu.Unlock()
		return
	}
	p.NotFoundTTLSecVal = sec
	p.mu.Unlock()
	p.markDirty()
}

// AdaptiveLatMultiple reports the per-host deadline multiple (0 = the network
// default, ×8). Live-applied.
func (p *AssetPreferences) AdaptiveLatMultiple() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AdaptiveLatMultipleVal
}

// SetAdaptiveLatMultiple persists the deadline multiple (0 = default; clamped).
func (p *AssetPreferences) SetAdaptiveLatMultiple(n int) {
	if n != 0 {
		n = clampPercent(n, AdaptiveLatMultipleMin, AdaptiveLatMultipleMax)
	}
	p.mu.Lock()
	if p.AdaptiveLatMultipleVal == n {
		p.mu.Unlock()
		return
	}
	p.AdaptiveLatMultipleVal = n
	p.mu.Unlock()
	p.markDirty()
}

// SpriteDownscaleOffOn reports whether the automatic decode downscale is
// disabled entirely (default OFF = the downscale runs).
func (p *AssetPreferences) SpriteDownscaleOffOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteDownscaleOff
}

// SetSpriteDownscaleOff persists the downscale-disable knob.
func (p *AssetPreferences) SetSpriteDownscaleOff(off bool) {
	p.setBoolPref(&p.SpriteDownscaleOff, off)
}

// SpriteDownscalePct reports the decode-downscale target as a % of display
// height (0 = the default 100 %). Live-applied to NEW decodes.
func (p *AssetPreferences) SpriteDownscalePct() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.SpriteDownscalePctVal
}

// SetSpriteDownscalePct persists the downscale percent (0 = default; clamped).
func (p *AssetPreferences) SetSpriteDownscalePct(pct int) {
	if pct != 0 {
		pct = clampPercent(pct, SpriteDownscaleMinPct, SpriteDownscaleMaxPct)
	}
	p.mu.Lock()
	if p.SpriteDownscalePctVal == pct {
		p.mu.Unlock()
		return
	}
	p.SpriteDownscalePctVal = pct
	p.mu.Unlock()
	p.markDirty()
}

// TexBudgetMiB reports the T1 texture byte budget in MiB (TexBudgetDefaultMiB
// when unset; applies on restart).
func (p *AssetPreferences) TexBudgetMiB() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.TexBudgetMiBVal == 0 {
		return TexBudgetDefaultMiB
	}
	return p.TexBudgetMiBVal
}

// SetTexBudgetMiB persists the T1 budget (0 = default; else clamped).
func (p *AssetPreferences) SetTexBudgetMiB(mib int) {
	if mib != 0 {
		mib = clampPercent(mib, TexBudgetMinMiB, TexBudgetMaxMiB)
	}
	p.mu.Lock()
	if p.TexBudgetMiBVal == mib {
		p.mu.Unlock()
		return
	}
	p.TexBudgetMiBVal = mib
	p.mu.Unlock()
	p.markDirty()
}

// CrossfadeMs reports the speaker-swap crossfade duration (0 = off, the default).
func (p *AssetPreferences) CrossfadeMs() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CrossfadeMsVal
}

// SetCrossfadeMs persists the crossfade duration (0 = off; else clamped).
func (p *AssetPreferences) SetCrossfadeMs(ms int) {
	if ms != 0 {
		ms = clampPercent(ms, CrossfadeMinMs, CrossfadeMaxMs)
	}
	p.mu.Lock()
	if p.CrossfadeMsVal == ms {
		p.mu.Unlock()
		return
	}
	p.CrossfadeMsVal = ms
	p.mu.Unlock()
	p.markDirty()
}

// EventDrivenLoopOn reports the EXPERIMENTAL event-driven render loop toggle
// (default ON on the test channel): static screens stop rendering between real
// signals, input wakes the loop instantly, and packets/decodes push wake
// events. OFF = the classic sleep-paced loop, byte-identical to before.
func (p *AssetPreferences) EventDrivenLoopOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.EventDrivenLoop
}

// SetEventDrivenLoop flips the experimental loop (applies live — the main loop
// re-reads it every pass).
func (p *AssetPreferences) SetEventDrivenLoop(on bool) {
	p.mu.Lock()
	if p.EventDrivenLoop == on {
		p.mu.Unlock()
		return
	}
	p.EventDrivenLoop = on
	p.mu.Unlock()
	p.markDirty()
}

// MotionRedrawPerEventOn reports the "redraw once per mouse-move event" pacing
// (event-driven loop only, default ON as of v1.55.1): bare pointer motion renders
// exactly the one frame its event earns and then re-parks, instead of arming the
// short full-rate motion grace — so hovering/sweeping the cursor over nothing stops
// pinning the frame rate to the cap. Real input (click/key/wheel) keeps its normal
// full-rate hold; turn it off to restore the hold-full-rate-while-moving behaviour.
func (p *AssetPreferences) MotionRedrawPerEventOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MotionRedrawPerEvent
}

// SetMotionRedrawPerEvent flips the per-event motion pacing (applies live — the
// main loop re-reads it every pass).
func (p *AssetPreferences) SetMotionRedrawPerEvent(on bool) {
	p.mu.Lock()
	if p.MotionRedrawPerEvent == on {
		p.mu.Unlock()
		return
	}
	p.MotionRedrawPerEvent = on
	p.mu.Unlock()
	p.markDirty()
}

// FrameLimiterDisabled reports the #5 bypass: when ON the main loop renders
// every pass with no adaptive pacing and no static skip — vsync alone paces the
// presents (and -vsync=false keeps a 60 fps floor so it can't busy-spin). Default
// OFF: it's the deliberate high-GPU escape hatch for anyone who wants maximum
// responsiveness regardless of cost. A fresh json key (not the retired test-build
// "no limit" default) so a stale saved value can't silently leak the bypass on.
func (p *AssetPreferences) FrameLimiterDisabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DisableFrameLimiter
}

// SetFrameLimiterDisabled flips the bypass (applies live — the main loop
// re-reads it every pass).
func (p *AssetPreferences) SetFrameLimiterDisabled(on bool) {
	p.mu.Lock()
	if p.DisableFrameLimiter == on {
		p.mu.Unlock()
		return
	}
	p.DisableFrameLimiter = on
	p.mu.Unlock()
	p.markDirty()
}

// FPSCap / IdleFPS / UnfocusedFPS report the three frame-pacing rates
// (defaults when unset): the foreground ceiling, the nothing-is-animating
// idle rate, and the another-window-has-focus rate.
func (p *AssetPreferences) FPSCap() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.FPSCapVal == 0 {
		return FPSCapDefault
	}
	return p.FPSCapVal
}

// SetFPSCap persists the foreground frame cap (0 = default, FPSUnlimited = no
// cap; else clamped).
func (p *AssetPreferences) SetFPSCap(fps int) {
	fps = normalizeFPSPref(fps, FPSCapMin, FPSCapMax)
	p.mu.Lock()
	if p.FPSCapVal == fps {
		p.mu.Unlock()
		return
	}
	p.FPSCapVal = fps
	p.mu.Unlock()
	p.markDirty()
}

// IdleFPS reports the idle frame rate (IdleFPSDefault when unset).
func (p *AssetPreferences) IdleFPS() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.IdleFPSVal == 0 {
		return IdleFPSDefault
	}
	return p.IdleFPSVal
}

// SetIdleFPS persists the idle frame rate (0 = default, FPSUnlimited = never
// throttle when idle; else clamped).
func (p *AssetPreferences) SetIdleFPS(fps int) {
	fps = normalizeFPSPref(fps, IdleFPSMin, IdleFPSMax)
	p.mu.Lock()
	if p.IdleFPSVal == fps {
		p.mu.Unlock()
		return
	}
	p.IdleFPSVal = fps
	p.mu.Unlock()
	p.markDirty()
}

// UnfocusedFPS reports the unfocused frame rate (UnfocusedFPSDefault when unset).
func (p *AssetPreferences) UnfocusedFPS() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.UnfocusedFPSVal == 0 {
		return UnfocusedFPSDefault
	}
	return p.UnfocusedFPSVal
}

// SetUnfocusedFPS persists the unfocused frame rate (0 = default,
// FPSUnlimited = never throttle when unfocused; else clamped).
func (p *AssetPreferences) SetUnfocusedFPS(fps int) {
	fps = normalizeFPSPref(fps, UnfocusedFPSMin, UnfocusedFPSMax)
	p.mu.Lock()
	if p.UnfocusedFPSVal == fps {
		p.mu.Unlock()
		return
	}
	p.UnfocusedFPSVal = fps
	p.mu.Unlock()
	p.markDirty()
}

// InputGraceFrames reports the post-input full-rate hold in frames
// (InputGraceFramesDefault when unset).
func (p *AssetPreferences) InputGraceFrames() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.InputGraceFramesVal == 0 {
		return InputGraceFramesDefault // absent = the default hold
	}
	if p.InputGraceFramesVal < 0 {
		return 0 // InputGraceOff → no hold
	}
	return p.InputGraceFramesVal
}

// SetInputGraceFrames persists the post-input full-rate hold. 0 (or below) is
// the OFF sentinel (no hold — distinct from unset); a positive value clamps.
func (p *AssetPreferences) SetInputGraceFrames(n int) {
	if n <= 0 {
		n = InputGraceOff
	} else {
		n = clampPercent(n, InputGraceFramesMin, InputGraceFramesMax)
	}
	p.mu.Lock()
	if p.InputGraceFramesVal == n {
		p.mu.Unlock()
		return
	}
	p.InputGraceFramesVal = n
	p.mu.Unlock()
	p.markDirty()
}

// EffectiveSpriteCap derives the decode-downscale height cap from the
// display-derived base and the two power-user knobs: off → 0 (no cap, exact
// source art), else base × pct/100 (pct 0 = the default 100 %). Pure — shared
// by the boot path (cmd/asyncao) and the live Settings re-derive so the two
// can never disagree.
func EffectiveSpriteCap(base int, off bool, pct int) int {
	if off || base <= 0 {
		return 0
	}
	if pct == 0 {
		pct = 100
	}
	return base * pct / 100
}

// ResetPowerUser reverts EVERY power-user option to its shipped default — the
// "nuke" button on the Power user tab. Scoped strictly to that tab's knobs: TLS
// validation, both Origin overrides, character-folder casing, the whole renderer
// block (cold-load mode + wait/hold knobs + tint), the sprite mask, and the core
// timings. The image-format block is deliberately NOT touched (probe orders +
// learned formats are per-server warm state with their own controls, and the full
// "Reset settings" already covers them). A guard test pins this field list.
func (p *AssetPreferences) ResetPowerUser() {
	p.mu.Lock()
	p.ValidateTLSCerts = false
	p.AssetOrigin = ""
	p.WSOrigin = ""
	p.UpdateExperimental = false // back to the stable update channel
	p.AssetCharCase = 0
	p.SpriteLoadModeVal = defaultSpriteLoadMode
	p.SpriteWaitMsVal = 0
	p.SpriteWaitPair = false
	p.SpriteWaitPreanim = false
	p.HoldPrevMaxAgeMsVal = 0
	p.HoldDebugTint = false
	p.ShoutDurationMsVal = 0
	p.PreanimTimeoutMsVal = 0
	p.ICQueueCapVal = 0
	p.CatchUpLingerMsVal = 0
	p.ThumbCache = false
	p.ThumbHeightPxVal = 0
	p.ThumbQualityVal = 0
	p.ThumbBudgetMiBVal = 0
	p.DiskCacheBudgetMiBVal = 0 // back to unlimited (the default)
	p.NotFoundTTLSecVal = 0
	p.AdaptiveLatMultipleVal = 0
	p.SpriteDownscaleOff = false
	p.SpriteDownscalePctVal = 0
	p.TexBudgetMiBVal = 0
	p.CrossfadeMsVal = 0
	p.FPSCapVal = 0
	p.IdleFPSVal = 0
	p.UnfocusedFPSVal = 0
	p.InputGraceFramesVal = 0
	p.EventDrivenLoop = defaultEventDrivenLoop
	p.DisableFrameLimiter = false                        // #5 bypass resets OFF
	p.MotionRedrawPerEvent = defaultMotionRedrawPerEvent // per-event motion redraw resets to its default (ON)
	p.ClipSpritesToStage = defaultClipSpritesToStage
	p.mu.Unlock()
	p.markDirty()
}

// MusicVolModeOn reports whether the Music menu shows the volume sliders instead of
// the track list. Persisted so the choice survives a restart (playtest: it used to
// reset to the track list every launch).
func (p *AssetPreferences) MusicVolModeOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.MusicVolMode
}

// SetMusicVolMode persists the Music-menu volume-view toggle.
func (p *AssetPreferences) SetMusicVolMode(on bool) {
	p.mu.Lock()
	if p.MusicVolMode == on {
		p.mu.Unlock()
		return
	}
	p.MusicVolMode = on
	p.mu.Unlock()
	p.markDirty()
}

// ChangelogSeenVersion reports the build version whose "What's New" the user last
// opened (#23). Empty until they open it; compared to update.Version to decide the
// unread dot after an app update.
func (p *AssetPreferences) ChangelogSeenVersion() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ChangelogSeen
}

// SetChangelogSeen records that the user opened the changelog for build version v,
// clearing the unread dot until the next update changes the version.
func (p *AssetPreferences) SetChangelogSeen(v string) {
	p.mu.Lock()
	if p.ChangelogSeen == v {
		p.mu.Unlock()
		return
	}
	p.ChangelogSeen = v
	p.mu.Unlock()
	p.markDirty()
}

// OpenTabList returns a copy of the remembered open tabs (restore-on-launch).
func (p *AssetPreferences) OpenTabList() []OpenTab {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.OpenTabs) == 0 {
		return nil
	}
	out := make([]OpenTab, len(p.OpenTabs))
	copy(out, p.OpenTabs)
	return out
}

// SetOpenTabs replaces the remembered open tabs (copied, capped at
// maxMultiTabCap), persisted for the next launch's restore.
func (p *AssetPreferences) SetOpenTabs(tabs []OpenTab) {
	if len(tabs) > maxMultiTabCap {
		tabs = tabs[:maxMultiTabCap]
	}
	cp := make([]OpenTab, len(tabs))
	copy(cp, tabs)
	p.mu.Lock()
	p.OpenTabs = cp
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

// ScreenEffectsOn reports the AO2 screen-effects toggle (ON by default): the \s
// screenshake / \f flash codes typed into a message and the field-based
// shake/realization render when on. "Reduce motion" also suppresses them.
func (p *AssetPreferences) ScreenEffectsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ScreenEffects
}

// SetScreenEffects toggles the AO2 screen effects.
func (p *AssetPreferences) SetScreenEffects(on bool) {
	p.mu.Lock()
	if p.ScreenEffects == on {
		p.mu.Unlock()
		return
	}
	p.ScreenEffects = on
	p.mu.Unlock()
	p.markDirty()
}

// WordDeleteOn reports the Ctrl+Backspace word-delete toggle (ON by default):
// when on, Ctrl+Backspace in any focused text field deletes the preceding word
// instead of one rune. OFF restores plain-key behavior.
func (p *AssetPreferences) WordDeleteOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.WordDelete
}

// SetWordDelete toggles Ctrl+Backspace word deletion.
func (p *AssetPreferences) SetWordDelete(on bool) {
	p.mu.Lock()
	if p.WordDelete == on {
		p.mu.Unlock()
		return
	}
	p.WordDelete = on
	p.mu.Unlock()
	p.markDirty()
}

// AdditiveTextOn reports the 2.8 additive-text toggle (ON by default): honor an
// incoming ADDITIVE=1 append and offer the outgoing Additive checkbox. OFF falls
// back to the pre-2.8 replace behavior.
func (p *AssetPreferences) AdditiveTextOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AdditiveText
}

// SetAdditiveText toggles 2.8 additive text.
func (p *AssetPreferences) SetAdditiveText(on bool) {
	p.mu.Lock()
	if p.AdditiveText == on {
		p.mu.Unlock()
		return
	}
	p.AdditiveText = on
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

// PerAreaScrollbackOn reports the per-area IC scrollback toggle (OFF by
// default): when on, switching areas swaps the IC log to that area's own
// history instead of one continuous log.
func (p *AssetPreferences) PerAreaScrollbackOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PerAreaScroll
}

// SetPerAreaScrollback toggles per-area IC scrollback.
func (p *AssetPreferences) SetPerAreaScrollback(on bool) {
	p.mu.Lock()
	if p.PerAreaScroll == on {
		p.mu.Unlock()
		return
	}
	p.PerAreaScroll = on
	p.mu.Unlock()
	p.markDirty()
}

// DetailedLogOn reports the detailed-logging toggle (OFF by default): when on,
// IC/OOC messages are appended to a per-server transcript file with timestamps,
// area, character name and showname.
func (p *AssetPreferences) DetailedLogOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.DetailedLog
}

// SetDetailedLog toggles detailed transcript logging.
func (p *AssetPreferences) SetDetailedLog(on bool) {
	p.mu.Lock()
	if p.DetailedLog == on {
		p.mu.Unlock()
		return
	}
	p.DetailedLog = on
	p.mu.Unlock()
	p.markDirty()
}

// AutoClipModcallOn reports the auto-clip-on-modcall toggle (ON by default): when
// on, a modcall (sent or received) saves a small text clip of the recent IC log
// to logs/<server>/modcalls/ as a frozen record for mods/CMs.
func (p *AssetPreferences) AutoClipModcallOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.AutoClipModcall
}

// SetAutoClipModcall toggles saving an IC-log clip when a modcall fires.
func (p *AssetPreferences) SetAutoClipModcall(on bool) {
	p.mu.Lock()
	if p.AutoClipModcall == on {
		p.mu.Unlock()
		return
	}
	p.AutoClipModcall = on
	p.mu.Unlock()
	p.markDirty()
}

// CharChatboxOn reports the per-character chatbox-skin toggle (ON by default:
// a speaker's char.ini chat=<misc> art draws as their chatbox, AO2-parity).
func (p *AssetPreferences) CharChatboxOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CharChatbox
}

// SetCharChatbox toggles per-character chatbox skins.
func (p *AssetPreferences) SetCharChatbox(on bool) {
	p.mu.Lock()
	if p.CharChatbox == on {
		p.mu.Unlock()
		return
	}
	p.CharChatbox = on
	p.mu.Unlock()
	p.markDirty()
}

// GroupChatButtonOn reports the on-screen Group Chat button toggle (ON by default).
func (p *AssetPreferences) GroupChatButtonOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.GroupChatButton
}

// SetGroupChatButton toggles the courtroom's main Group Chat button.
func (p *AssetPreferences) SetGroupChatButton(on bool) {
	p.mu.Lock()
	if p.GroupChatButton == on {
		p.mu.Unlock()
		return
	}
	p.GroupChatButton = on
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
	p.wardrobeGen.Add(1) // the claiming server's wardrobe just gained the legacy list
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
	if changed {
		p.wardrobeGen.Add(1)
	}
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
	if changed {
		p.wardrobeGen.Add(1)
	}
	return changed
}

// WardrobeGeneration returns a counter that bumps whenever any server's
// wardrobe membership changes; the char-select star grid caches its
// membership set keyed by it (mirrors FormatGeneration).
func (p *AssetPreferences) WardrobeGeneration() uint64 {
	return p.wardrobeGen.Load()
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

// --- setting presets (Nightingale: named bundles on top of the one-JSON store) ---
// A preset IS a settings export (passwords stripped, same loadable shape) saved
// under presets/<name>.json beside the live file; applying one IS an import
// (validated, atomic replace, applied on the next start). The full file stays
// the single source of truth — presets are an opt-in convenience layer, exactly
// as scoped on the roadmap.

const (
	// presetsDirName holds the saved bundles beside the live preferences file.
	presetsDirName = "presets"
	// presetsCap bounds how many presets may exist (hard rule #4).
	presetsCap = 16
	// presetNameMaxLen keeps names filesystem- and UI-friendly.
	presetNameMaxLen = 32
)

// PresetsDir is the presets directory path (beside the preferences file).
func (p *AssetPreferences) PresetsDir() string {
	return filepath.Join(filepath.Dir(p.path), presetsDirName)
}

// sanitizePresetName reduces a user-typed preset name to a safe file stem:
// letters, digits, space, dash, underscore; trimmed; length-capped. Empty
// after sanitizing = invalid.
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

// SavePreset snapshots the CURRENT settings as a named preset (passwords
// stripped, like every export). Overwrites a same-named preset; refuses past
// presetsCap distinct names or an empty/invalid name.
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

// ListPresets names the saved presets (sorted stems; missing dir = none).
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

// ApplyPreset stages a saved preset as the live preferences file — the import
// path: validated, atomically replaced, applied on the NEXT start (the live
// state has too many mirrors to hot-swap; the caller shows the restart note).
func (p *AssetPreferences) ApplyPreset(name string) error {
	stem := sanitizePresetName(name)
	if stem == "" {
		return fmt.Errorf("config: bad preset name")
	}
	return p.ImportSettings(filepath.Join(p.PresetsDir(), stem+".json"))
}

// DeletePreset removes a saved preset file.
func (p *AssetPreferences) DeletePreset(name string) error {
	stem := sanitizePresetName(name)
	if stem == "" {
		return fmt.Errorf("config: bad preset name")
	}
	return os.Remove(filepath.Join(p.PresetsDir(), stem+".json"))
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

// CallwordsOOCOn reports whether callwords also alert on OOC messages (default
// OFF — IC only, so /ga rosters and OOC chatter don't constantly ping).
func (p *AssetPreferences) CallwordsOOCOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.CallwordsOOC
}

// SetCallwordsOOC toggles OOC callword alerts.
func (p *AssetPreferences) SetCallwordsOOC(on bool) {
	p.mu.Lock()
	if p.CallwordsOOC == on {
		p.mu.Unlock()
		return
	}
	p.CallwordsOOC = on
	p.mu.Unlock()
	p.markDirty()
}

// ExtProfile returns the per-host extensions.json format profile (raw JSON), or
// "" if none. A profile is seeded synchronously on connect and overrides both the
// fetched server manifest and the global default — for that host only.
func (p *AssetPreferences) ExtProfile(host string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ExtProfiles[host]
}

// SetExtProfile sets (or clears, with "") a host's format profile.
func (p *AssetPreferences) SetExtProfile(host, profileJSON string) {
	if host == "" {
		return
	}
	p.mu.Lock()
	if p.ExtProfiles == nil {
		p.ExtProfiles = map[string]string{}
	}
	if profileJSON == "" {
		delete(p.ExtProfiles, host)
	} else {
		p.ExtProfiles[host] = profileJSON
	}
	p.mu.Unlock()
	p.markDirty()
}

// BlipTyping reports the chat-blip cadence: one blip per `rate` revealed letters
// (default 2), and whether spaces also blip (default false = skip whitespace).
func (p *AssetPreferences) BlipTyping() (rate int, onSpaces bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	r := p.BlipRate
	if r < MinBlipRate {
		r = defaultBlipRate
	}
	return r, p.BlipOnSpaces
}

// SetBlipTyping clamps and persists the blip cadence + whitespace toggle.
func (p *AssetPreferences) SetBlipTyping(rate int, onSpaces bool) {
	rate = clampPercent(rate, MinBlipRate, MaxBlipRate)
	p.mu.Lock()
	if p.BlipRate == rate && p.BlipOnSpaces == onSpaces {
		p.mu.Unlock()
		return
	}
	p.BlipRate, p.BlipOnSpaces = rate, onSpaces
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
	// classicSlotCap bounds named slots in the default-courtroom layout
	// (rule §17.4). Raised 48→128 for full-state LayoutProfiles: a profile's
	// Classic map now carries not just the 18 classic slots but every
	// persistable floating surface routed through the classic-slot mechanism —
	// budget: 18 classic + 11 floatWin panels + 1 extras box + ~26 torn Extras
	// widgets + ~24 ctrl.* control-button slots + torn-tab headroom ≈ 90; 128
	// rounds up with comfortable margin.
	classicSlotCap = 128
	// layoutPresetCap bounds how many named layout snapshots a user can save (#34).
	// LayoutProfiles (which superseded presets) reuse it via layoutProfileCap.
	layoutPresetCap = 24
	// layoutProfileCap bounds how many named full-state layout profiles a user
	// can save (mirrors the retired layoutPresetCap). A profile snapshots the
	// classic slots, anchors, hidden set, and grid step together.
	layoutProfileCap = 24
	// maxHiddenPanels bounds the hidden-chrome slice (rule §17.4). Comfortably
	// above the ~45 registry ids (hideablePanels + hideableButtons +
	// hideableRosterButtons) so a legitimate all-hidden set is never truncated,
	// while a stale/hand-edited pref can't grow it unboundedly. Enforced in
	// SetHiddenPanels (dedup + cap) and sanitizeHiddenPanels on load.
	maxHiddenPanels = 64
)

// Layout-element rotation (A4). Angles are stored as a single byte in the
// 360/256 encoding shared with courtroom.SpriteStyle.Rotation — any of the 256
// values is valid, so there is nothing to clamp per value, only the map size to
// bound.
const (
	// RotStepFineDeg is the Shift+R fine-tilt step in degrees. The byte encoding
	// quantizes to 360/256 ≈ 1.406°/step, so a 15° request rounds to the nearest
	// byte; the visible step is ~15° ± 1° and a full 24-press circle doesn't land
	// exactly back on 0 (accepted — matches SpriteStyle's own tilt slider).
	RotStepFineDeg = 15
)

// RotCoarseCycle is the R-key coarse cycle: 0 / 90 / 180 / 270 degrees in the
// 360/256 byte encoding (90*256/360=64, 180→128, 270→192). Exact — these four
// angles land on whole bytes with no rounding.
var RotCoarseCycle = [4]uint8{0, 64, 128, 192}

// RotationByteToDeg converts a stored rotation byte to degrees (the same map
// courtroom.SpriteStyle.RotationDeg uses). Pure — the draw-path resolver and
// the editors share it, and the angle==0 fast path in the draw sites compares
// the byte directly (0 → the plain Copy path) so this is only called when a
// rotation is actually set.
func RotationByteToDeg(b uint8) float64 {
	return float64(int(b)) * 360.0 / 256.0
}

// RotationDegToByte is the inverse (rounded to the nearest byte, wrapping at
// 360°) — used when the Shift+R fine step advances by RotStepFineDeg.
func RotationDegToByte(deg int) uint8 {
	deg %= 360
	if deg < 0 {
		deg += 360
	}
	// Round to nearest byte: +180 before the integer divide by 360.
	return uint8((deg*256 + 180) / 360 % 256)
}

// NextCoarseRotation advances a rotation byte through RotCoarseCycle. A byte
// that isn't exactly one of the four coarse angles (a prior Shift+R fine tilt)
// snaps up to the next coarse angle at or above it, so the coarse key always
// makes visible progress.
func NextCoarseRotation(cur uint8) uint8 {
	for _, b := range RotCoarseCycle {
		if b > cur {
			return b
		}
	}
	return RotCoarseCycle[0]
}

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
	// The rotation dies with its override (A4), mirroring the anchor coupling on
	// the classic side.
	if rm, ok := p.ThemeRectRotations[theme]; ok {
		if key == "" {
			delete(p.ThemeRectRotations, theme)
		} else {
			delete(rm, key)
			if len(rm) == 0 {
				delete(p.ThemeRectRotations, theme)
			}
		}
	}
	p.mu.Unlock()
	p.markDirty()
}

// ThemeRectRotationSnapshot returns a copy of one theme's per-widget rotation
// angles (A4). The themed editor bakes this into themeLayoutCache.ang at cache
// rebuild time so the draw path resolves angles lock-free (a plain map probe on
// the pre-baked cache, never a prefs read per frame).
func (p *AssetPreferences) ThemeRectRotationSnapshot(theme string) map[string]uint8 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	src := p.ThemeRectRotations[theme]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]uint8, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SetThemeRectRotation stores one widget's rotation angle byte for a theme
// (bounded by the same themeOvThemesCap/themeOvRectsCap as the overrides). A
// zero angle is stored like any other so it round-trips; the draw path treats an
// ABSENT entry as angle 0, so a caller that wants "no rotation" should
// ClearThemeRectRotation rather than set 0 (keeps the map minimal).
func (p *AssetPreferences) SetThemeRectRotation(theme, key string, deg uint8) {
	if theme == "" || key == "" {
		return
	}
	p.mu.Lock()
	if p.ThemeRectRotations == nil {
		p.ThemeRectRotations = map[string]map[string]uint8{}
	}
	m, ok := p.ThemeRectRotations[theme]
	if !ok {
		if len(p.ThemeRectRotations) >= themeOvThemesCap {
			p.mu.Unlock()
			return
		}
		m = map[string]uint8{}
		p.ThemeRectRotations[theme] = m
	}
	if _, exists := m[key]; !exists && len(m) >= themeOvRectsCap {
		p.mu.Unlock()
		return
	}
	m[key] = deg
	p.mu.Unlock()
	p.markDirty()
}

// ClearThemeRectRotation drops one widget's rotation (key "" = the whole theme's
// rotations).
func (p *AssetPreferences) ClearThemeRectRotation(theme, key string) {
	p.mu.Lock()
	if m, ok := p.ThemeRectRotations[theme]; ok {
		if key == "" {
			delete(p.ThemeRectRotations, theme)
		} else {
			delete(m, key)
			if len(m) == 0 {
				delete(p.ThemeRectRotations, theme)
			}
		}
	}
	p.mu.Unlock()
	p.markDirty()
}

// sanitizeClassicLayout validates persisted default-courtroom slot overrides on
// load: it drops NaN/inf, clamps fractions into a sane on-screen range, and
// bounds the map (rule §17.4). Unknown slot names are harmless — slotRect only
// ever reads names it queries — so they're kept (forward/back compatibility).
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
		// X/Y may sit slightly off-stage (negative) by design; width/height must
		// stay positive. The wide clamp just rejects corrupt/garbage values.
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

// sanitizeLayoutPresets validates persisted named layout snapshots on load (#34):
// each preset's slot map runs through sanitizeClassicLayout (so it inherits the same
// NaN/clamp/cap guards), blank names and now-empty presets are dropped, and the whole
// set is bounded by layoutPresetCap.
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

// sanitizeHiddenPanels dedups and bounds a hidden-chrome id slice on load (rule
// §17.4). Blank ids and duplicates are dropped; the result is capped at
// maxHiddenPanels. Order is preserved for the survivors. Nil-safe (empty → nil).
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

// sanitizeClassicRotations validates persisted per-slot rotation bytes on load
// (A4): blank slot names are dropped and the map is bounded by classicSlotCap
// (mirroring the ClassicLayout/ClassicAnchors axes it rides alongside). Any byte
// value is a valid angle, so there is nothing to clamp per entry. Nil-safe.
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

// sanitizeThemeRectRotations validates persisted per-theme rotation maps on load
// (A4). Unlike the sibling ThemeRectOv (which loads raw), this axis is sanitized:
// blank theme/key names are dropped and the same themeOvThemesCap (outer) /
// themeOvRectsCap (inner) bounds apply, so a hand-edited pref can't smuggle in an
// unbounded set. Any byte is a valid angle. Nil-safe.
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

// sanitizeLayoutProfile cleans one profile's four axes through the same helpers
// the live axes use: classic slots, anchors, and hidden set are validated/capped
// and the grid step is clamped. A profile with nothing left in any axis is
// dropped by the caller (its Classic/Anchors/Hidden are all nil and GridPx 0).
func sanitizeLayoutProfile(in LayoutProfile) LayoutProfile {
	return LayoutProfile{
		Classic:   sanitizeClassicLayout(in.Classic),
		Anchors:   sanitizeClassicAnchors(in.Anchors),
		Hidden:    sanitizeHiddenPanels(in.Hidden),
		GridPx:    clampLayoutGridPx(in.GridPx),
		Rotations: sanitizeClassicRotations(in.Rotations),
	}
}

// layoutProfileEmpty reports whether a sanitized profile carries no state at all
// (nothing worth persisting).
func layoutProfileEmpty(p LayoutProfile) bool {
	return len(p.Classic) == 0 && len(p.Anchors) == 0 && len(p.Hidden) == 0 && p.GridPx == 0 && len(p.Rotations) == 0
}

// sanitizeLayoutProfiles validates persisted full-state profiles on load (A6):
// blank names and empty profiles are dropped, each surviving profile's axes pass
// through their own sanitizers, and the set is bounded by layoutProfileCap.
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

// LayoutProfileNames returns the saved full-state profile names (A6). Order is
// unspecified — the UI sorts for display.
func (p *AssetPreferences) LayoutProfileNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.LayoutProfiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(p.LayoutProfiles))
	for k := range p.LayoutProfiles {
		names = append(names, k)
	}
	return names
}

// LayoutProfile returns a deep copy of one saved profile (zero value if the name
// is unknown) (A6). Every axis is copied so the caller can't alias the live map.
func (p *AssetPreferences) LayoutProfile(name string) LayoutProfile {
	p.mu.RLock()
	defer p.mu.RUnlock()
	prof, ok := p.LayoutProfiles[name]
	if !ok {
		return LayoutProfile{}
	}
	return cloneLayoutProfile(prof)
}

// cloneLayoutProfile returns a deep copy (all three maps/slices duplicated) so
// the live profile and any returned/stored copy never alias.
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

// SaveLayoutProfile stores a full-state profile under name, replacing any
// existing profile of that name (A6). A blank name or all-empty profile is
// ignored; the profile is sanitized + deep-copied (never aliased) and the set is
// bounded by layoutProfileCap.
func (p *AssetPreferences) SaveLayoutProfile(name string, prof LayoutProfile) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	clean := sanitizeLayoutProfile(prof)
	if layoutProfileEmpty(clean) {
		return
	}
	p.mu.Lock()
	if p.LayoutProfiles == nil {
		p.LayoutProfiles = map[string]LayoutProfile{}
	}
	if _, exists := p.LayoutProfiles[name]; !exists && len(p.LayoutProfiles) >= layoutProfileCap {
		p.mu.Unlock()
		return
	}
	p.LayoutProfiles[name] = clean
	p.mu.Unlock()
	p.markDirty()
}

// DeleteLayoutProfile removes a saved profile by name (A6).
func (p *AssetPreferences) DeleteLayoutProfile(name string) {
	p.mu.Lock()
	if p.LayoutProfiles != nil {
		delete(p.LayoutProfiles, name)
		if len(p.LayoutProfiles) == 0 {
			p.LayoutProfiles = nil
		}
	}
	p.mu.Unlock()
	p.markDirty()
}

// ClassicLayoutOverrides returns a sanitized copy of the default-courtroom slot
// overrides (window fractions). The classic-layout editor snapshots this once
// into an App-local map so the render path reads it lock-free.
func (p *AssetPreferences) ClassicLayoutOverrides() map[string][4]float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ClassicLayout) == 0 {
		return nil
	}
	out := make(map[string][4]float64, len(p.ClassicLayout))
	for k, v := range p.ClassicLayout {
		out[k] = v
	}
	return out
}

// SetClassicSlot stores one default-courtroom slot's override (window fraction
// [x,y,w,h]). Bounded by classicSlotCap.
func (p *AssetPreferences) SetClassicSlot(name string, frac [4]float64) {
	if name == "" {
		return
	}
	p.mu.Lock()
	if p.ClassicLayout == nil {
		p.ClassicLayout = map[string][4]float64{}
	}
	if _, exists := p.ClassicLayout[name]; !exists && len(p.ClassicLayout) >= classicSlotCap {
		p.mu.Unlock()
		return
	}
	p.ClassicLayout[name] = frac
	p.mu.Unlock()
	p.markDirty()
}

// ClearClassicSlot drops one slot's override (name "" clears every slot, the
// editor's "Reset all"). The slot's window anchor goes with it — an anchor
// without an override is meaningless (it only re-bases the override).
func (p *AssetPreferences) ClearClassicSlot(name string) {
	p.mu.Lock()
	if name == "" {
		p.ClassicLayout = nil
		p.ClassicAnchors = nil
		p.ClassicRotations = nil // rotation is meaningless without an override (like the anchor)
	} else {
		delete(p.ClassicLayout, name)
		delete(p.ClassicAnchors, name)
		delete(p.ClassicRotations, name)
	}
	p.mu.Unlock()
	p.markDirty()
}

// ClassicAnchor pins one classic-layout slot to a window reference: Mode is
// two letters — horizontal ∈ {f,l,r,c} then vertical ∈ {f,t,b,c} (f = follow
// the fraction, i.e. unpinned on that axis; l/r/t/b = pixel-glued to that
// window edge; c = pixel-glued to the window centre). WinW/WinH record the
// window size the slot's fraction override was last written at, so the
// resolver can reconstruct the exact pixel rect the user placed.
type ClassicAnchor struct {
	Mode string `json:"mode"`
	WinW int    `json:"winW"`
	WinH int    `json:"winH"`
}

// LayoutProfile is a named, full-state snapshot of the courtroom layout (A6):
// the classic slot overrides (window fractions), their window anchors, the
// hidden-chrome set, and the editor snap-grid step. Applying a profile restores
// all four axes at once. It is a strict superset of the retired LayoutPresets
// (which carried only Classic) — legacy presets migrate in as Classic-only
// profiles. Each axis is sanitized on load through the same helpers the live
// axes use, so a profile can never smuggle in geometry the resolver would have
// to defend against.
type LayoutProfile struct {
	Classic map[string][4]float64    `json:"classic,omitempty"`
	Anchors map[string]ClassicAnchor `json:"anchors,omitempty"`
	Hidden  []string                 `json:"hidden,omitempty"`
	GridPx  int                      `json:"gridPx,omitempty"`
	// Rotations carries the classic slots' rotation angles (A4). Additive: a
	// profile saved by an older build has this nil and loads fine. Theme rotations
	// stay OUT of profiles (like the theme rects — a theme-X angle applied on
	// theme-Y would be incoherent).
	Rotations map[string]uint8 `json:"rotations,omitempty"`
}

// validAnchorMode reports whether m is a well-formed two-letter anchor mode.
func validAnchorMode(m string) bool {
	if len(m) != 2 {
		return false
	}
	h, v := m[0], m[1]
	return (h == 'f' || h == 'l' || h == 'r' || h == 'c') &&
		(v == 'f' || v == 't' || v == 'b' || v == 'c')
}

// sanitizeClassicAnchors keeps only well-formed entries (valid mode, positive
// saved window) within the classicSlotCap bound — a hand-edited or stale pref
// can't smuggle in junk the resolver would have to defend against per frame.
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

// ClassicAnchorSnapshot returns a copy of the per-slot window pins — the
// classic editor snapshots it once alongside ClassicLayoutOverrides so the
// render path resolves anchors lock-free.
func (p *AssetPreferences) ClassicAnchorSnapshot() map[string]ClassicAnchor {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ClassicAnchors) == 0 {
		return nil
	}
	out := make(map[string]ClassicAnchor, len(p.ClassicAnchors))
	for k, v := range p.ClassicAnchors {
		out[k] = v
	}
	return out
}

// SetClassicAnchor stores one slot's window pin (rejects malformed modes /
// window sizes; bounded by classicSlotCap alongside the overrides).
func (p *AssetPreferences) SetClassicAnchor(name string, a ClassicAnchor) {
	if name == "" || !validAnchorMode(a.Mode) || a.WinW <= 0 || a.WinH <= 0 {
		return
	}
	p.mu.Lock()
	if p.ClassicAnchors == nil {
		p.ClassicAnchors = map[string]ClassicAnchor{}
	}
	if _, exists := p.ClassicAnchors[name]; !exists && len(p.ClassicAnchors) >= classicSlotCap {
		p.mu.Unlock()
		return
	}
	p.ClassicAnchors[name] = a
	p.mu.Unlock()
	p.markDirty()
}

// ClearClassicAnchor drops one slot's window pin (the override stays; the
// slot goes back to plain fraction scaling).
func (p *AssetPreferences) ClearClassicAnchor(name string) {
	p.mu.Lock()
	delete(p.ClassicAnchors, name)
	p.mu.Unlock()
	p.markDirty()
}

// Layout-editor snap grid (playtest, Tifera: "allow us to edit the snap
// grid"). The step is clamped to [MinLayoutGridPx, MaxLayoutGridPx]; the
// editor's Grid chip cycles a sensible subset.
const (
	// DefaultLayoutGridPx is the long-standing 8 px editor grid.
	DefaultLayoutGridPx = 8
	// MinLayoutGridPx / MaxLayoutGridPx bound a stored grid step so a stale
	// or hand-edited pref can't make snapping useless (1) or absurd (256).
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

// LayoutGridSize reports the layout editor's snap-grid step in logical px
// (DefaultLayoutGridPx when unset).
func (p *AssetPreferences) LayoutGridSize() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.LayoutGridPx == 0 {
		return DefaultLayoutGridPx
	}
	return p.LayoutGridPx
}

// SetLayoutGridSize persists the editor snap-grid step, clamped.
func (p *AssetPreferences) SetLayoutGridSize(px int) {
	px = clampLayoutGridPx(px)
	p.mu.Lock()
	if p.LayoutGridPx == px {
		p.mu.Unlock()
		return
	}
	p.LayoutGridPx = px
	p.mu.Unlock()
	p.markDirty()
}

// SetClassicLayout REPLACES every default-courtroom slot override at once — the
// classic editor's undo/redo restores a whole snapshot, so it can't go slot-by-slot.
// The map is copied (never aliased), empty keys skipped, and bounded by classicSlotCap;
// a nil/empty map clears all overrides.
func (p *AssetPreferences) SetClassicLayout(m map[string][4]float64) {
	p.mu.Lock()
	if len(m) == 0 {
		p.ClassicLayout = nil
	} else {
		cp := make(map[string][4]float64, len(m))
		for k, v := range m {
			if k == "" || len(cp) >= classicSlotCap {
				continue
			}
			cp[k] = v
		}
		p.ClassicLayout = cp
	}
	p.mu.Unlock()
	p.markDirty()
}

// SetClassicAnchors REPLACES every slot's window pin at once (symmetric to
// SetClassicLayout) — applyProfile restores a whole anchor snapshot, so it can't
// go slot-by-slot. The map is sanitized (bad mode / non-positive window dropped)
// and copied (never aliased); a nil/empty map clears all pins. Unlike
// ClearClassicSlot this does NOT touch ClassicLayout — a profile carries both
// axes independently, and the caller sets them together.
func (p *AssetPreferences) SetClassicAnchors(m map[string]ClassicAnchor) {
	clean := sanitizeClassicAnchors(m) // drops junk + bounds at classicSlotCap
	p.mu.Lock()
	p.ClassicAnchors = clean
	p.mu.Unlock()
	p.markDirty()
}

// ClassicRotationSnapshot returns a copy of the per-slot rotation angles (A4) —
// the classic editor snapshots it once into an App-local map (beside the anchor
// snapshot) so the render path resolves angles lock-free.
func (p *AssetPreferences) ClassicRotationSnapshot() map[string]uint8 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ClassicRotations) == 0 {
		return nil
	}
	out := make(map[string]uint8, len(p.ClassicRotations))
	for k, v := range p.ClassicRotations {
		out[k] = v
	}
	return out
}

// SetClassicRotation stores one slot's rotation angle byte (bounded by
// classicSlotCap alongside the overrides). A zero angle is stored like any
// other; the draw path treats an ABSENT entry as angle 0, so a caller that wants
// "no rotation" should ClearClassicRotation rather than set 0.
func (p *AssetPreferences) SetClassicRotation(name string, deg uint8) {
	if name == "" {
		return
	}
	p.mu.Lock()
	if p.ClassicRotations == nil {
		p.ClassicRotations = map[string]uint8{}
	}
	if _, exists := p.ClassicRotations[name]; !exists && len(p.ClassicRotations) >= classicSlotCap {
		p.mu.Unlock()
		return
	}
	p.ClassicRotations[name] = deg
	p.mu.Unlock()
	p.markDirty()
}

// ClearClassicRotation drops one slot's rotation (the override stays; the slot
// draws unrotated).
func (p *AssetPreferences) ClearClassicRotation(name string) {
	p.mu.Lock()
	delete(p.ClassicRotations, name)
	p.mu.Unlock()
	p.markDirty()
}

// SetClassicRotations REPLACES every slot's rotation at once (symmetric to
// SetClassicAnchors) — applyProfile restores a whole rotation snapshot. The map
// is sanitized (blank keys dropped, bounded by classicSlotCap) and copied (never
// aliased); a nil/empty map clears all rotations.
func (p *AssetPreferences) SetClassicRotations(m map[string]uint8) {
	clean := sanitizeClassicRotations(m) // drops blanks + bounds at classicSlotCap
	p.mu.Lock()
	p.ClassicRotations = clean
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

// ThemeFitMode reports how an AO2 theme fills the window: ThemeFitStretch
// (default), ThemeFitLetterbox, or ThemeFitCrop.
func (p *AssetPreferences) ThemeFitMode() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ThemeFit
}

// SetThemeFit sets the theme-fit mode (clamped to a valid mode).
func (p *AssetPreferences) SetThemeFit(mode int) {
	mode = clampPercent(mode, ThemeFitStretch, ThemeFitCustom)
	p.mu.Lock()
	if p.ThemeFit == mode {
		p.mu.Unlock()
		return
	}
	p.ThemeFit = mode
	p.mu.Unlock()
	p.markDirty()
}

// ThemeZoom reports the Custom-mode zoom (percent of the letterbox-fit scale;
// 100 = exactly fits), clamped.
func (p *AssetPreferences) ThemeZoom() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.ThemeFitZoom <= 0 {
		return DefaultThemeZoom
	}
	return clampPercent(p.ThemeFitZoom, MinThemeZoom, MaxThemeZoom)
}

// ThemePan reports the Custom-mode pan (percent of the window, ±).
func (p *AssetPreferences) ThemePan() (x, y int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ThemeFitPanX, p.ThemeFitPanY
}

// SetThemeFitZoom clamps and stores the Custom-mode zoom.
func (p *AssetPreferences) SetThemeFitZoom(z int) {
	z = clampPercent(z, MinThemeZoom, MaxThemeZoom)
	p.mu.Lock()
	if p.ThemeFitZoom == z {
		p.mu.Unlock()
		return
	}
	p.ThemeFitZoom = z
	p.mu.Unlock()
	p.markDirty()
}

// SetThemeFitPan clamps and stores the Custom-mode pan.
func (p *AssetPreferences) SetThemeFitPan(x, y int) {
	x = clampPercent(x, -MaxThemePan, MaxThemePan)
	y = clampPercent(y, -MaxThemePan, MaxThemePan)
	p.mu.Lock()
	if p.ThemeFitPanX == x && p.ThemeFitPanY == y {
		p.mu.Unlock()
		return
	}
	p.ThemeFitPanX, p.ThemeFitPanY = x, y
	p.mu.Unlock()
	p.markDirty()
}

// PlainLobbyOn reports whether the lobby/server list uses the plain client
// backdrop instead of the theme's lobbybackground (ON by default).
func (p *AssetPreferences) PlainLobbyOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.PlainLobby
}

// SetPlainLobby toggles the plain lobby backdrop.
func (p *AssetPreferences) SetPlainLobby(on bool) {
	p.mu.Lock()
	if p.PlainLobby == on {
		p.mu.Unlock()
		return
	}
	p.PlainLobby = on
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

// UpdateFavorite renames and/or re-addresses an existing phone-book entry,
// preserving its position in the list. Returns false if oldURL isn't found, or
// if newURL differs from oldURL and already belongs to ANOTHER favorite
// (rejected to avoid a silent, surprising merge onto that target). A no-op edit
// (nothing actually changed) returns true — the caller treats false purely as
// an error signal, so an unchanged Save must not report failure. markDirty()
// fires only on a real change.
func (p *AssetPreferences) UpdateFavorite(oldURL, newName, newURL, description string) bool {
	if oldURL == "" || newURL == "" {
		return false
	}
	p.mu.Lock()
	idx := -1
	for i, f := range p.Favorites {
		if f.URL == oldURL {
			idx = i
			break
		}
	}
	if idx < 0 {
		p.mu.Unlock()
		return false // oldURL not in the phone book
	}
	if newURL != oldURL {
		for i, f := range p.Favorites {
			if i != idx && f.URL == newURL {
				p.mu.Unlock()
				return false // the new address already belongs to another entry — reject
			}
		}
	}
	cur := p.Favorites[idx]
	if cur.Name == newName && cur.URL == newURL && cur.Description == description {
		p.mu.Unlock()
		return true // no-op: accepted, nothing to persist
	}
	p.Favorites[idx].Name = newName
	p.Favorites[idx].URL = newURL
	p.Favorites[idx].Description = description
	p.mu.Unlock()
	p.markDirty()
	return true
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

// ExportFavoritesJSON marshals the phone book (favorites) as shareable JSON.
func (p *AssetPreferences) ExportFavoritesJSON() ([]byte, error) {
	return json.MarshalIndent(p.FavoriteServers(), jsonMarshalPrefix, jsonMarshalIndent)
}

// MergeFavoritesJSON merges a shared phone book in (additive, dedup by URL) and
// returns the count of NEW servers added. Entries with no URL are skipped.
func (p *AssetPreferences) MergeFavoritesJSON(data []byte) (int, error) {
	var in []FavoriteServer
	if err := json.Unmarshal(data, &in); err != nil {
		return 0, err
	}
	added := 0
	for _, f := range in {
		if strings.TrimSpace(f.URL) == "" || p.IsFavorite(f.URL) {
			continue
		}
		p.AddFavorite(f.Name, f.URL, f.Description)
		added++
	}
	return added, nil
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

// ShownameCap bounds the saved showname presets (M6).
const ShownameCap = 64

// ShownameList returns a copy of the saved showname presets (global, persisted —
// cleared only by a full factory reset).
func (p *AssetPreferences) ShownameList() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ShownamePresets) == 0 {
		return nil
	}
	out := make([]string, len(p.ShownamePresets))
	copy(out, p.ShownamePresets)
	return out
}

// ShownameCount is the number of saved presets — a cheap, alloc-free length read
// so the courtroom name picker caches its option list and rebuilds only when the
// set changes (vs calling ShownameList, which copies, every frame).
func (p *AssetPreferences) ShownameCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.ShownamePresets)
}

// AddShownamePreset appends a showname to the saved presets (trimmed, deduped
// case-insensitively, bounded). Reports whether it changed.
func (p *AssetPreferences) AddShownamePreset(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	p.mu.Lock()
	if len(p.ShownamePresets) >= ShownameCap {
		p.mu.Unlock()
		return false
	}
	for _, have := range p.ShownamePresets {
		if strings.EqualFold(have, name) {
			p.mu.Unlock()
			return false
		}
	}
	p.ShownamePresets = append(p.ShownamePresets, name)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// RemoveShownamePreset drops a saved showname. Reports whether it changed.
func (p *AssetPreferences) RemoveShownamePreset(name string) bool {
	p.mu.Lock()
	for i, have := range p.ShownamePresets {
		if strings.EqualFold(have, name) {
			p.ShownamePresets = append(p.ShownamePresets[:i], p.ShownamePresets[i+1:]...)
			for k, v := range p.ShownameKeys { // drop any key bound to the removed preset
				if strings.EqualFold(v, name) {
					delete(p.ShownameKeys, k)
				}
			}
			p.mu.Unlock()
			p.markDirty()
			return true
		}
	}
	p.mu.Unlock()
	return false
}

// shownameKeyCap bounds the global per-key showname binds (hard rule #4: no
// unbounded maps).
const shownameKeyCap = 64

// ShownameKeyBinds returns a copy of the global key → showname bind map (M6:
// press a bound key in the courtroom to swap to that showname). Nil = none.
func (p *AssetPreferences) ShownameKeyBinds() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ShownameKeys) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.ShownameKeys))
	for k, v := range p.ShownameKeys {
		out[k] = v
	}
	return out
}

// SetShownameKeyBind binds key (lowercase SDL key name) to a showname; an empty
// showname clears the binding. Global + persisted; bounded by shownameKeyCap.
func (p *AssetPreferences) SetShownameKeyBind(key, showname string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return
	}
	p.mu.Lock()
	if showname == "" {
		delete(p.ShownameKeys, key)
		p.mu.Unlock()
		p.markDirty()
		return
	}
	if p.ShownameKeys == nil {
		p.ShownameKeys = map[string]string{}
	}
	if _, exists := p.ShownameKeys[key]; !exists && len(p.ShownameKeys) >= shownameKeyCap {
		p.mu.Unlock()
		return
	}
	p.ShownameKeys[key] = showname
	p.mu.Unlock()
	p.markDirty()
}

// icPhraseKeyCap bounds the hotkeyed IC quick-phrases (hard rule #4: no unbounded maps).
const icPhraseKeyCap = 64

// ICPhraseBinds returns a copy of the key → IC-phrase map: press a bound key in
// the courtroom to send that line as your character in IC. Nil = none.
func (p *AssetPreferences) ICPhraseBinds() map[string]string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ICPhraseKeys) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.ICPhraseKeys))
	for k, v := range p.ICPhraseKeys {
		out[k] = v
	}
	return out
}

// SetICPhraseKey binds key (lowercase SDL key name) to a canned IC line; an empty
// phrase clears the binding. Global + persisted; bounded by icPhraseKeyCap.
func (p *AssetPreferences) SetICPhraseKey(key, phrase string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return
	}
	phrase = strings.TrimSpace(phrase)
	p.mu.Lock()
	if phrase == "" {
		delete(p.ICPhraseKeys, key)
		p.mu.Unlock()
		p.markDirty()
		return
	}
	if p.ICPhraseKeys == nil {
		p.ICPhraseKeys = map[string]string{}
	}
	if _, exists := p.ICPhraseKeys[key]; !exists && len(p.ICPhraseKeys) >= icPhraseKeyCap {
		p.mu.Unlock()
		return
	}
	p.ICPhraseKeys[key] = phrase
	p.mu.Unlock()
	p.markDirty()
}

// mutedSFXCap bounds the per-SFX mute list (hard rule #4: no unbounded slices).
const mutedSFXCap = 128

// IsSFXMuted reports whether an emote SFX name is muted (M11, case-insensitive).
// Called from the courtroom audio path once per played SFX (not per frame).
func (p *AssetPreferences) IsSFXMuted(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, m := range p.MutedSFX {
		if m == name {
			return true
		}
	}
	return false
}

// MutedSFXList returns a copy of the muted SFX names (global, persisted).
func (p *AssetPreferences) MutedSFXList() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.MutedSFX) == 0 {
		return nil
	}
	out := make([]string, len(p.MutedSFX))
	copy(out, p.MutedSFX)
	return out
}

// MuteSFX adds an SFX name to the mute list (lowercased, deduped, bounded).
// Reports whether it changed.
func (p *AssetPreferences) MuteSFX(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	p.mu.Lock()
	if len(p.MutedSFX) >= mutedSFXCap {
		p.mu.Unlock()
		return false
	}
	for _, m := range p.MutedSFX {
		if m == name {
			p.mu.Unlock()
			return false
		}
	}
	p.MutedSFX = append(p.MutedSFX, name)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// UnmuteSFX removes an SFX name from the mute list. Reports whether it changed.
func (p *AssetPreferences) UnmuteSFX(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	p.mu.Lock()
	for i, m := range p.MutedSFX {
		if m == name {
			p.MutedSFX = append(p.MutedSFX[:i], p.MutedSFX[i+1:]...)
			p.mu.Unlock()
			p.markDirty()
			return true
		}
	}
	p.mu.Unlock()
	return false
}

// sfxFavoritesCap bounds the starred-SFX list (hard rule #4: no unbounded
// lists). A generous ceiling — nobody favourites hundreds of sounds.
const sfxFavoritesCap = 128

// SfxFavoritesList returns a copy of the starred SFX names (#12, global, persisted,
// lowercased bare names resolved per-server by urls.SFX).
func (p *AssetPreferences) SfxFavoritesList() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.SfxFavorites) == 0 {
		return nil
	}
	out := make([]string, len(p.SfxFavorites))
	copy(out, p.SfxFavorites)
	return out
}

// IsSfxFavorite reports whether a sound name is starred (case-insensitive).
func (p *AssetPreferences) IsSfxFavorite(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, s := range p.SfxFavorites {
		if s == name {
			return true
		}
	}
	return false
}

// ToggleSfxFavorite stars an SFX name if absent (lowercased, deduped, bounded) or unstars it if
// present. Reports the new state (true = now starred). A no-op (and false) when at the cap adding.
func (p *AssetPreferences) ToggleSfxFavorite(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	p.mu.Lock()
	for i, s := range p.SfxFavorites {
		if s == name {
			p.SfxFavorites = append(p.SfxFavorites[:i], p.SfxFavorites[i+1:]...)
			p.mu.Unlock()
			p.markDirty()
			return false
		}
	}
	if len(p.SfxFavorites) >= sfxFavoritesCap {
		p.mu.Unlock()
		return false
	}
	p.SfxFavorites = append(p.SfxFavorites, name)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// defaultModReasonTemplates are the built-in ban/kick reason chips shown until the user edits the
// list. Seeded (not just shown) on the first add/remove so editing starts from these.
var defaultModReasonTemplates = []string{
	"Spam", "Harassment", "NSFW", "Disruptive", "Trolling", "Ban evasion", "Disrespect",
}

// modReasonTemplatesCap bounds the editable reason-template list (hard rule #4).
const modReasonTemplatesCap = 32

// ModReasonTemplatesList returns the editable ban/kick reason chips (a clone). When the stored list
// is empty it returns the built-in defaults — so a brand-new config (and a fully-cleared one) always
// has something. Deleting every entry therefore re-seeds the defaults on next read, by design.
func (p *AssetPreferences) ModReasonTemplatesList() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.ModReasonTemplates) == 0 {
		return cloneStrings(defaultModReasonTemplates)
	}
	return cloneStrings(p.ModReasonTemplates)
}

// seedModTemplatesLocked populates the stored list with the defaults if it's empty, so a mutation
// (add/remove) starts from what the user sees rather than from nothing. Caller holds the lock.
func (p *AssetPreferences) seedModTemplatesLocked() {
	if len(p.ModReasonTemplates) == 0 {
		p.ModReasonTemplates = cloneStrings(defaultModReasonTemplates)
	}
}

// AddModReasonTemplate appends a reason (trimmed, deduped case-insensitively, bounded). Reports
// whether it changed. Case is preserved — these are display labels, not match keys.
func (p *AssetPreferences) AddModReasonTemplate(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	p.mu.Lock()
	p.seedModTemplatesLocked()
	if len(p.ModReasonTemplates) >= modReasonTemplatesCap {
		p.mu.Unlock()
		return false
	}
	for _, t := range p.ModReasonTemplates {
		if strings.EqualFold(t, s) {
			p.mu.Unlock()
			return false
		}
	}
	p.ModReasonTemplates = append(p.ModReasonTemplates, s)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// modDurationsCap bounds the saved custom ban-duration chips (hard rule #4). The
// enum presets already cover the common spans, so 16 customs is plenty.
const modDurationsCap = 16

// ModDurationsList returns the saved custom ban-duration chips (a clone) — canonical
// short tokens ("45m", "2d"). Empty by default: the BanDuration enum presets are the
// built-ins, customs only add to them.
func (p *AssetPreferences) ModDurationsList() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return cloneStrings(p.ModDurations)
}

// AddModDuration appends a custom duration chip (deduped, bounded). The caller
// canonicalizes first (courtroom.CanonicalBanDuration) — config stays courtroom-free,
// so it stores what it's given, trimmed. Reports whether it changed.
func (p *AssetPreferences) AddModDuration(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	p.mu.Lock()
	if len(p.ModDurations) >= modDurationsCap {
		p.mu.Unlock()
		return false
	}
	for _, t := range p.ModDurations {
		if strings.EqualFold(t, token) {
			p.mu.Unlock()
			return false
		}
	}
	p.ModDurations = append(p.ModDurations, token)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// RemoveModDuration drops a custom duration chip. Reports whether it changed.
func (p *AssetPreferences) RemoveModDuration(token string) bool {
	token = strings.TrimSpace(token)
	p.mu.Lock()
	for i, t := range p.ModDurations {
		if strings.EqualFold(t, token) {
			p.ModDurations = append(p.ModDurations[:i], p.ModDurations[i+1:]...)
			p.mu.Unlock()
			p.markDirty()
			return true
		}
	}
	p.mu.Unlock()
	return false
}

// RemoveModReasonTemplate drops a reason (case-insensitive). Reports whether it changed.
func (p *AssetPreferences) RemoveModReasonTemplate(s string) bool {
	s = strings.TrimSpace(s)
	p.mu.Lock()
	p.seedModTemplatesLocked()
	for i, t := range p.ModReasonTemplates {
		if strings.EqualFold(t, s) {
			p.ModReasonTemplates = append(p.ModReasonTemplates[:i], p.ModReasonTemplates[i+1:]...)
			p.mu.Unlock()
			p.markDirty()
			return true
		}
	}
	p.mu.Unlock()
	return false
}

// blipVolsCap bounds the per-character blip-volume map (hard rule #4: no
// unbounded maps). Only characters adjusted away from the default occupy a
// slot, so this is a safety ceiling, not an expected size.
const blipVolsCap = 256

// blipVolumeDefault is the per-character blip scale when none is stored: full
// (100%), i.e. no extra attenuation beyond the global blip volume (M11).
const blipVolumeDefault = 100

// BlipVolumeFor returns a character's stored per-character blip scale (0–100,
// 100 = no attenuation; M11). Called from the courtroom once per message (not
// per frame). Unknown characters get the full default.
func (p *AssetPreferences) BlipVolumeFor(char string) int {
	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" {
		return blipVolumeDefault
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if v, ok := p.BlipVols[char]; ok {
		return v
	}
	return blipVolumeDefault
}

// BlipVolumes returns a copy of the per-character blip-volume overrides
// (lowercased char → scale), for the settings UI. Nil when none are set.
func (p *AssetPreferences) BlipVolumes() map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.BlipVols) == 0 {
		return nil
	}
	out := make(map[string]int, len(p.BlipVols))
	for k, v := range p.BlipVols {
		out[k] = v
	}
	return out
}

// SetBlipVolume stores a character's per-character blip scale (clamped 0–100).
// Setting the default (100) clears the override so the map only holds genuine
// adjustments (and stays well under blipVolsCap). Reports whether it changed.
func (p *AssetPreferences) SetBlipVolume(char string, pct int) bool {
	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" {
		return false
	}
	pct = clampPercent(pct, 0, blipVolumeDefault)
	p.mu.Lock()
	cur, had := p.BlipVols[char]
	if pct == blipVolumeDefault {
		if !had {
			p.mu.Unlock()
			return false // already default
		}
		delete(p.BlipVols, char)
		p.mu.Unlock()
		p.markDirty()
		return true
	}
	if had && cur == pct {
		p.mu.Unlock()
		return false // unchanged
	}
	if !had && len(p.BlipVols) >= blipVolsCap {
		p.mu.Unlock()
		return false // bounded
	}
	if p.BlipVols == nil {
		p.BlipVols = make(map[string]int)
	}
	p.BlipVols[char] = pct
	p.mu.Unlock()
	p.markDirty()
	return true
}

// emoteFavCharsCap / emoteFavPerCharCap bound the per-character emote-favourites
// map (hard rule #4: no unbounded maps/slices). Only characters with at least
// one starred emote occupy a slot, and hundreds of favourites on one character
// is already absurd, so these are safety ceilings, not expected sizes.
const (
	emoteFavCharsCap   = 1024
	emoteFavPerCharCap = 256
)

// EmoteFavsFor returns a COPY of a character's favourited emote indices — the
// slice positions in its emote list. Indices are the key (not the emote
// name/anim) because those DUPLICATE within a character (e.g. Apollo's three
// "normal" emotes share both label and talking sprite), so a name key would
// merge distinct emotes into one star. Called from the UI's per-character view
// rebuild, which runs only on a character/filter change — never per frame. Nil
// when none are set.
func (p *AssetPreferences) EmoteFavsFor(char string) []int {
	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	src := p.EmoteFavs[char]
	if len(src) == 0 {
		return nil
	}
	out := make([]int, len(src))
	copy(out, src)
	return out
}

// IsEmoteFav reports whether emote index idx is favourited for char.
func (p *AssetPreferences) IsEmoteFav(char string, idx int) bool {
	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" || idx < 0 {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, v := range p.EmoteFavs[char] {
		if v == idx {
			return true
		}
	}
	return false
}

// ToggleEmoteFav flips emote index idx's favourite state for char and returns
// the NEW state (true = now favourited). Bounded by the per-char and char-count
// caps; marks dirty so the debounced saver persists it. Removing the last
// favourite drops the character's map entry, so the map only holds genuine
// favourites.
func (p *AssetPreferences) ToggleEmoteFav(char string, idx int) bool {
	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" || idx < 0 {
		return false
	}
	p.mu.Lock()
	list := p.EmoteFavs[char]
	for i, v := range list {
		if v == idx { // already favourited -> remove it
			list = append(list[:i], list[i+1:]...)
			if len(list) == 0 {
				delete(p.EmoteFavs, char)
			} else {
				p.EmoteFavs[char] = list
			}
			p.mu.Unlock()
			p.markDirty()
			return false
		}
	}
	if len(list) >= emoteFavPerCharCap { // bounded: keep existing favourites
		p.mu.Unlock()
		return false
	}
	if _, had := p.EmoteFavs[char]; !had && len(p.EmoteFavs) >= emoteFavCharsCap {
		p.mu.Unlock()
		return false // bounded: too many characters tracked
	}
	if p.EmoteFavs == nil {
		p.EmoteFavs = make(map[string][]int)
	}
	p.EmoteFavs[char] = append(p.EmoteFavs[char], idx)
	p.mu.Unlock()
	p.markDirty()
	return true
}

// EmoteFavOnlyOn reports whether the emote grid is filtered to favourites only.
func (p *AssetPreferences) EmoteFavOnlyOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.EmoteFavOnly
}

// SetEmoteFavOnly toggles the favourites-only emote grid filter.
func (p *AssetPreferences) SetEmoteFavOnly(on bool) {
	p.mu.Lock()
	if p.EmoteFavOnly == on {
		p.mu.Unlock()
		return
	}
	p.EmoteFavOnly = on
	p.mu.Unlock()
	p.markDirty()
}

// EmoteFavStarsOn reports whether the favourite ★ badges show on emote cells.
// The whole emote-favourites feature is opt-in (default OFF) so the badge can't
// clutter the grid for people who don't use it.
func (p *AssetPreferences) EmoteFavStarsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.EmoteFavStars
}

// SetEmoteFavStars toggles the emote-favourites feature (the per-cell stars).
func (p *AssetPreferences) SetEmoteFavStars(on bool) {
	p.mu.Lock()
	if p.EmoteFavStars == on {
		p.mu.Unlock()
		return
	}
	p.EmoteFavStars = on
	p.mu.Unlock()
	p.markDirty()
}

// EmoteCaptionsOn reports whether the emote-name caption strip is drawn over
// icon-fallback emote buttons (the buttons shown when a character has no
// emotions/button art). Default OFF: the buttons show as clean icons with no
// text overlay — players found the name strip cluttered, especially when every
// fallback cell shows the same character icon. Opt-in for those who relied on
// the captions to tell otherwise-identical fallback cells apart.
func (p *AssetPreferences) EmoteCaptionsOn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.EmoteCaptions
}

// SetEmoteCaptions toggles the icon-fallback emote-name caption overlay.
func (p *AssetPreferences) SetEmoteCaptions(on bool) {
	p.mu.Lock()
	if p.EmoteCaptions == on {
		p.mu.Unlock()
		return
	}
	p.EmoteCaptions = on
	p.mu.Unlock()
	p.markDirty()
}

// clampViewportExactPx normalizes the exact-viewport-width pref: 0 (off) passes
// through; any other value is clamped into [Min,Max] so a hand-edited or stale
// config can't size the stage off-screen.
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

// ViewportExactWidth reports the user's fixed stage width in px (0 = size by the
// View % knob / divider instead). Read once per courtroom frame; lock-light.
func (p *AssetPreferences) ViewportExactWidth() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ViewportExactW
}

// SetViewportExactWidth pins (or clears, with 0) the exact stage width. The
// courtroom snaps an over-large value DOWN to the largest crisp multiple that
// fits the window at draw time, so this only needs the coarse sanity clamp.
func (p *AssetPreferences) SetViewportExactWidth(px int) {
	px = clampViewportExactPx(px)
	p.mu.Lock()
	if p.ViewportExactW == px {
		p.mu.Unlock()
		return
	}
	p.ViewportExactW = px
	p.mu.Unlock()
	p.markDirty()
}
