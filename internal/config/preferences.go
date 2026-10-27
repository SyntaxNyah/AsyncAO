// Package config persists user-tunable asset preferences.
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

	"github.com/SyntaxNyah/AsyncAO/internal/helper/text"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
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
// shaped window.
// Native is the shipped default because stock AO2 never scales a theme at all —
// setFixedSize makes the window BE the canvas (AO2-Client charselect.cpp:83, and
// set_courtroom_size() for the courtroom), so a theme's pixels land on screen
// pixels untouched. Our window is resizable, so Native reproduces that by drawing
// the design canvas at scale exactly 1.0, centred, with bars, and scaling DOWN
// uniformly only when the window is smaller than the canvas so nothing is ever
// clipped. Stretch (the old default — webAO-style) fills edge-to-edge with a
// slight aspect distortion; Letterbox keeps the exact proportions with bars;
// Crop scales up to fill and lets the overflow run off-screen.
// These values are PERSISTED, so the list is APPEND-ONLY: renumbering would
// silently reinterpret every saved choice. Native therefore sits at 4, which
// means the zero value is no longer the default — a file written before Native
// existed carries a baked themeFit:0 (Stretch) that is indistinguishable from a
// deliberate Stretch, so only the one-shot ThemeFitDefaultMigrated stamp (see
// load) can move an existing user onto the new default.
const (
	ThemeFitStretch   = 0
	ThemeFitLetterbox = 1
	ThemeFitCrop      = 2
	ThemeFitCustom    = 3 // manual zoom + pan (crop to taste)
	ThemeFitNative    = 4 // 1:1, downscale-only: stock-AO2 pixels, never upscaled
	// themeFitModeMax is the highest valid mode. Every clamp goes through it so a
	// future mode can never be silently clamped away by a bound still pinned to the
	// previous last value — which is exactly how the load overlay would have eaten
	// Native had it kept clamping to ThemeFitCustom.
	themeFitModeMax = ThemeFitNative
	defaultThemeFit = ThemeFitNative
	// DefaultThemeFit exports that default for the UI surfaces that have to RETURN
	// to it rather than to a remembered previous choice — the menu bar's "Keep
	// aspect ratio" tick, which un-ticks to Stretch and re-ticks to whichever mode
	// currently holds the default job. An alias, so there is still exactly ONE
	// definition: hard-coding ThemeFitNative at a call site would quietly stop
	// tracking the default the day it moves again.
	DefaultThemeFit = defaultThemeFit
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

// defaultThemeFonts ships ON: an AO2 theme's courtroom_fonts.ini names a font
// family, point size and weight PER courtroom element (showname, message,
// ic_chatlog, server_chatlog, music_list, music_name, area_list) and AO2-Client
// applies all of it (Courtroom::set_fonts, courtroom.cpp:1188) — a theme simply
// does not look like itself without them (#39). Untick it to keep the client's
// own font and per-panel zoom everywhere, whatever the theme asks for. A manual
// font override / the dyslexia toggle already outranks the theme's families.
const defaultThemeFonts = true

// defaultCatchUpWhenBehind ships ON: in a packed room the IC stage otherwise
// crawls through every queued preanim/shout, falling minutes behind real-time.
// Catch-up fast-forwards the backlog; the IC log still keeps every message.
const defaultCatchUpWhenBehind = true

// defaultShoutBypassQueue ships ON: shouts skip the queue and play immediately,
// exactly as AO2 does. Toggle OFF to queue shouts like any other message.
const defaultShoutBypassQueue = true

// Catch-up queue-depth threshold: engage once this many messages (or more) are
// waiting behind the one starting. The default is the floor (1) so the IC stage
// stays real-time — in a busy room the newest message types out in full (with
// its sprite, name and effects) while any backlog behind it flashes past, so
// the textbox always tracks the latest line instead of crawling seconds behind.
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
	// MaxMultiTabCap is maxMultiTabCap exported for callers that must size a
	// per-tab structure against the same ceiling (the mount layer's origin list),
	// so the two can never drift apart.
	MaxMultiTabCap = maxMultiTabCap
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
// buttons. Default-ON per the request: dragging works without opting in.
const defaultDragLayout = true

// defaultSmoothScaling turns on linear texture filtering (SDL render
// scale quality): sprites stretched to the viewport stop shimmering. This is
// the CLIENT-WIDE hint, which still governs backgrounds, desks and chrome;
// sprites are filtered per-character by SpriteScaling below.
const defaultSmoothScaling = true

// Per-sprite texture filter (issue #21 label 15), mirroring courtroom.ScalingMode
// as an int so it round-trips through JSON. Auto is the shipped default AND the
// value every existing user is migrated to: it honours each character's char.ini
// scaling= request and otherwise point-samples art that is being ENLARGED, which
// is what stops hand-pixelled characters rendering as a blur. Smooth and Pixel
// override both.
const (
	SpriteScalingAuto   = 0
	SpriteScalingSmooth = 1
	SpriteScalingPixel  = 2

	defaultSpriteScaling = SpriteScalingAuto
)

// ClampSpriteScaling keeps a hand-edited or future-version prefs file from
// selecting a mode this build doesn't have: anything unrecognised reads as Auto,
// the safe default, rather than indexing off the end of the UI's label table.
// Exported because the settings UI indexes its own label tables by this value.
func ClampSpriteScaling(v int) int {
	if v < SpriteScalingAuto || v > SpriteScalingPixel {
		return defaultSpriteScaling
	}
	return v
}

// defaultUpdateCheck enables the one-shot GitHub-Releases update check at
// launch (M13). On by default per the user's intent; the check is a single
// async probe fired off the boot path, and a dev build never hits the network.
const defaultUpdateCheck = true

// defaultShowAssetWarnings ships OFF: surfacing every 404 is constant noise on
// servers with sparse packs. The failures still reach the debug overlay (the
// dedicated failure log); this flag only governs the on-screen report — the
// menu-bar count chip. Settings → Assets turns it back on.
const defaultShowAssetWarnings = false

// defaultSpriteMove ships OFF: click-drag sprite repositioning is a power
// feature that surprised players who grabbed a sprite by accident. Settings
// re-enables it (and offers a one-click reset of every moved sprite).
const defaultSpriteMove = false

// defaultDeskFollowManifest ships ON: desks follow the server's
// extensions.json like every other class (auto-detect). It originally shipped
// OFF — pinning desks to WebP — because desks share the manifest's background
// class and a PNG-background declaration dragged desks with it; the pin is now
// the opt-OUT for servers whose manifests are wrong. Existing installs are
// moved once by DeskManifestDefaultMigrated — see load().
const defaultDeskFollowManifest = true

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

// Emote-grid icon spacing (LOGICAL px), the gap on BOTH axes between cells in the
// AsyncAO courtroom emote grid. Default is the value that used to be hardcoded, so
// nobody's grid moves on upgrade.
// Min is 0 ON PURPOSE (butt the icons together — AO2 themes ship "0, 0" spacings
// for exactly that look), which is why the on-disk DTO carries this as a *int: with
// a plain int, "0" and "this config predates the setting" are the same bytes, and
// every existing user's grid would silently collapse to a zero gap on upgrade. Max
// is bounded so a slider can never starve the grid down to one column — the column
// arithmetic in drawEmoteRow floors at 1 either way.
const (
	DefaultEmoteGridGapPx = 6
	MinEmoteGridGapPx     = 0
	MaxEmoteGridGapPx     = 48
)

// defaultEmoteHoverNames ships ON: resting the cursor on an emote button in the
// grid shows that emote's name (its char.ini [Emotions] comment) as a tooltip.
// OFF disables the hint entirely. Distinct from the sprite hover-PREVIEW (which
// pops the full-size art): this is the lightweight name-only tooltip, so people
// who keep the heavier preview off can still see which emote a cell is.
const defaultEmoteHoverNames = true

// Emote-name tooltip hover dwell: how long the cursor must rest on an emote cell
// before its name shows. Default 5 s (matches the sprite-preview default); bounded
// so it can't be set to fire instantly-on-graze or effectively never.
const (
	DefaultEmoteHoverNamesMs = 5000
	minEmoteHoverNamesMs     = 500
	maxEmoteHoverNamesMs     = 10000
)

// defaultAutoLoginToast ships ON: when a saved auto-login fires on join, a
// toast + desktop notification confirms it, so a mod never has to guess whether
// their session is authenticated. Toggleable off in Settings.
const defaultAutoLoginToast = true

// defaultFontCensus ships ON: text in a script the bundled chain can't draw finds
// a font already installed on the machine instead of rendering as empty boxes.
// It is a preference at all because it is the one font path whose INPUT is the
// user's operating system rather than anything AsyncAO ships — it indexes the
// system font directories and installs what it finds into the live chat chain.
const defaultFontCensus = true

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

// defaultRecordingsKeepAssets ships ON: when an in-app scene recording (.aorec)
// stops, its assets are ALREADY warm in the cache (the user was just connected,
// rendering them live), so packaging them into a self-contained bundle folder is
// a near-free cache drain that costs the user nothing — and the recording then
// survives its CDN going dark, exactly like a hand-triggered "Package this RP"
// bundle. A dedicated Settings toggle turns the auto-bundle off (the flat .aorec
// still lands either way; the bundle is purely additive).
const defaultRecordingsKeepAssets = true

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

// The three AO2 "sticky" post-send options, shipped with AsyncAO's defaults
// (FALSE: every pick resets after a send). AO2's own options.cpp implements all
// three as config.value("sticky…", true) — :420-423, :430-433, :440-443 — so
// stock AO2 keeps the picks. AsyncAO ships them OFF instead so the SFX pick, the
// effects pick and the Pre checkbox clear after every send, matching the one-shot
// Screenshake/Realization arms. Courtroom::reset_ui
// (../AO2-Client/src/courtroom.cpp:2340, :2348, :2356) clears each only when its
// option is FALSE. (The accessor names upstream read backwards —
// clearXOnPlayEnabled() returns the stickiness — which is why the constants here
// are named for the config KEY, not for the function.)
const (
	defaultStickySounds   = false // AO2 "stickysounds"  (aooptionsdialog.cpp:416, "Sticky Sounds")
	defaultStickyEffects  = false // AO2 "stickyeffects" (:417, "Sticky Effects")
	defaultStickyPreanims = false // AO2 "stickypres"    (:418, "Sticky Preanims")
)

// defaultSFXOnIdle ships OFF, matching AO2's own sfx_on_idle
// (options.cpp:569-572, `config.value("sfx_on_idle", false)`; the options dialog
// calls it "Always Send SFX", aooptionsdialog.cpp:423). With it ON a hand-picked
// SFX is promoted onto an IDLE/ZOOM message — AO2 rewrites the emote mod to
// PREANIM/PREANIM_ZOOM with an empty pre and zero delay so the sound plays
// anyway (courtroom.cpp:2107-2115).
const defaultSFXOnIdle = false

// defaultUnfocusedFullRate ships OFF: an unfocused window throttles to the
// background cap, which is the whole point of that cap. ON makes the pacer treat
// an unfocused window exactly like a focused one — for a second monitor, or for
// capture software that records the client while you work in another app.
const defaultUnfocusedFullRate = false

// defaultDetailedLog ships ON as of the 2026-08-09 batch (user order: every other
// AO client writes its chat log to disk by default, and the transcript is the
// thing people wish they had AFTER the session). Existing configs are moved once
// by DetailedLogDefaultMigrated — see load(). Turning it back off sticks.
const defaultDetailedLog = true

// defaultGhostCrawlText ships ON: while the typewriter is still revealing a line,
// the not-yet-revealed tail is drawn TRANSPARENT in the log instead of being
// absent, so the message keeps its final shape and the log stops reflowing under
// the reader on every rune. No migration stamp: it is a brand-new preference, so
// an absent value has never meant anything else.
const defaultGhostCrawlText = true

// defaultICTimestamps ships OFF (playtest): when ON, each IC log line is prefixed
// with the local time it arrived, so you can see when people spoke. Toggleable in
// Settings → Chat.
const defaultICTimestamps = false

// defaultAutoReconnect ships OFF (user request): after an unexpected drop, AsyncAO
// returns to the lobby with the reason shown and leaves you there — the manual
// Reconnect button re-dials the last server when you're ready. Auto-retry is
// opt-in because reconnecting always re-runs the AO handshake, which drops you at
// character select (the server re-picks your character on any fresh connection);
// staying at the lobby avoids that surprise, especially after a drop taken while
// minimized. Turn it back ON in Settings to auto-retry the last server with backoff.
const defaultAutoReconnect = false

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
// grace — less GPU/power on a moving cursor over static UI. Default ON; the
// Settings toggle turns it off for the old hold-full-rate behaviour.
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

// defaultICCustomColor seeds the free IC hex colour picker before
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
	if v > percentMax {
		return percentMax
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
	p.Name = text.Truncate(p.Name, profileNameMax)
	p.Pronouns = text.Truncate(p.Pronouns, profilePronounsMax)
	p.Tag = text.Truncate(p.Tag, profileTagMax)
	p.Bio = text.Truncate(p.Bio, profileBioMax)
	p.ThemeSong = text.Truncate(p.ThemeSong, profileURLMax)
	p.ArtURL = text.Truncate(p.ArtURL, profileURLMax)
	return p
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
	a.ClearWords = text.Truncate(a.ClearWords, autoStatusWordsMax)
	a.AFKWords = text.Truncate(a.AFKWords, autoStatusWordsMax)
	a.BusyWords = text.Truncate(a.BusyWords, autoStatusWordsMax)
	a.WritingWords = text.Truncate(a.WritingWords, autoStatusWordsMax)
	a.LFRPWords = text.Truncate(a.LFRPWords, autoStatusWordsMax)
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

// Values shared by many settings.
const (
	percentMax       = 100      // the top of every 0..100 percentage setting
	rgbMask          = 0xFFFFFF // a packed 0xRRGGBB colour
	prefetchAggroMax = 4        // most predicted sprites the prefetcher warms per message
	rotationSteps    = 256      // a rotation byte covers one full turn
	degreesPerTurn   = 360
)

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

// LocalMountCap bounds the ordered local mount list (rule §17.4). Every mount is
// walked and indexed on a rescan and searched on a miss, so the cost of the list
// is linear in its length; sixteen folders/zips is far past any real content
// pack layout and keeps a fat-fingered "add" loop from unbounded work.
const LocalMountCap = 16

// FavBackgroundCap bounds one SERVER's starred-background list (rule §17.4).
// Favorites are a curated subset the user pins in the background picker; a
// few hundred is already generous, so this is well under WardrobeCap.
const FavBackgroundCap = 512

// Message timing knobs (milliseconds), AO2-Client options.ini parity.
const (
	// DefaultTextCrawlMs is AO2-Client's own `text_crawl` default — 40 ms per
	// revealed letter (options.cpp:182 `config.value("text_crawl", 40)`, mirrored by
	// courtroom.h:333 `int text_crawl = 40`). It is ALSO the blip clock: chat_tick
	// fires one blip every BlipRate letters, so the crawl interval is the "blip
	// speed" a user actually hears.
	DefaultTextCrawlMs = 40
	MinTextCrawlMs     = 5
	MaxTextCrawlMs     = 100
	// preMoveTextCrawlMs is the default this preference shipped with BEFORE the
	// move to DefaultTextCrawlMs — i.e. the exact value every pre-move file has
	// baked in, because the saver always wrote textCrawlMs. It is the ONLY saved
	// value the one-shot in load() is allowed to overwrite: any other value in
	// [MinTextCrawlMs, MaxTextCrawlMs] is provably a deliberate pick and survives.
	preMoveTextCrawlMs = 18
	// DefaultTextStayMs mirrors courtroom.DefaultTextStayTime.
	DefaultTextStayMs = 200
	MaxTextStayMs     = 3000
	// Chat rate limit: 0 = off (our historical behavior; AO2-Client
	// defaults to 300 ms — users opt in).
	DefaultChatRateLimitMs = 0
	MaxChatRateLimitMs     = 5000
)

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
	pct := int(math.Round(dpi / BaselineDPI * percentMax))
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

	// themeFitMovedToNative records that THIS load performed the one-shot
	// Stretch→Native theme-fit move — i.e. that the stamp was absent AND the file
	// was still sitting on the old Stretch default, so the move actually changed
	// what the user sees. Never serialized (unexported): it describes this
	// process's load, not the file, and the persisted record is the stamp itself.
	// It exists because the mode flip alone leaves an upgrading user staring at a
	// small centred canvas ringed by bars where an edge-to-edge Stretch courtroom
	// used to be — an outcome that can only be undone by hunting through Settings.
	themeFitMovedToNative bool

	// themeFitStampPending records that THIS load wrote the one-shot
	// ThemeFitDefaultMigrated stamp that the file did not carry. Never serialized
	// (unexported): it describes this load, and newWithDebounce clears it by
	// flushing the file synchronously before the app can run.
	themeFitStampPending bool

	// formatGen increments on every mutation that changes any effective
	// probe list (format orders, fallback toggles). Consumers cache derived
	// format tables keyed by this generation — see Resolver's miss path.
	formatGen atomic.Uint64

	// wardrobeGen increments on every change to any server's wardrobe
	// membership. The char-select star grid caches a lowercased membership
	// set keyed by this generation, so the per-cell lookup stays lock-free.
	wardrobeGen atomic.Uint64

	// settings is the snapshot of the data fields; readers call Load() and
	// writers call Update(). It is swapped wholesale on a real change.
	settings atomic.Pointer[Settings]
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
	// user-editable.
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

// Macro bounds (rule §17.4): as many as wanted, within named caps.
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
	// Durable one-shot stamp. It runs HERE and not inside load() for two reasons:
	// load() is only half-overlaid at the point the migration decides, so writing
	// there would persist defaults for every field it has not reached yet; and
	// bare load() is the white-box test entry point, while this is the one the app
	// actually boots through. Boot path, one small file, and only on the single
	// launch that migrates — see themeFitStampPending.
	if p.themeFitStampPending {
		p.themeFitStampPending = false
		if serr := p.SaveNow(); serr != nil {
			// SaveNow cleared `pending` before failing; re-mark so the saver and Close
			// still retry rather than the stamp being silently dropped.
			p.markDirty()
			log.Printf("config: could not persist the theme-fit migration stamp: %v", serr)
		}
	}
	go p.saverLoop()
	return p, err
}

// defaultPrefs builds a fresh, all-defaults AssetPreferences (no disk read, no
// saver goroutine). Both load() (before overlaying the file) and the reset
// methods use it, so the default state lives in exactly one place.
func defaultPrefs(path string) *AssetPreferences {
	p := &AssetPreferences{path: path}
	p.settings.Store(defaultSettings())
	return p
}

// defaultSettings builds the all-defaults Settings snapshot. Both load() (before
// overlaying the file) and the reset methods use it, so the default state lives in
// exactly one place.
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
		// All FIVE one-shot default stamps. A fresh install has nothing to move — it
		// already defaults to auto-reconnect OFF, theme fit Native, a 40 ms message
		// crawl, manifest-following desks and transcript logging ON — so it counts as
		// already-migrated. Only a file written by an OLDER build lacks these stamps
		// (absent → nil on load) and triggers the one-shot force-OFF / Stretch→Native /
		// 18→40 ms / desks-follow-ON / transcript-ON move in the load overlay.
		AutoReconnectDefaultMigrated: true,
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
		// The OOC log's own zoom. It MUST be seeded here and not left to the load
		// overlay's legacy-inherit rule: load() returns these defaults verbatim when
		// the file does not exist (first run) or was quarantined as corrupt, so the
		// overlay never runs on those paths and OOCScalePct would stay 0 — which is
		// not "absent" once it leaves config, it is a 0% scale that buildSet floors
		// at a 1 px font, i.e. an unreadable OOC log on a brand-new install.
		OOCScalePct:    DefaultScalePercent,
		InputHeightPct: DefaultScalePercent,
		UIScalePct:     DefaultScalePercent,
		// OOC gets its OWN BOX on a fresh install. Every call site already
		// documents the box as "the new default" — only this seed still said otherwise,
		// so a first-run user got the Legacy-style hybrid (a log tab PLUS a second
		// always-visible OOC bar at the bottom) the rest of the UI treats as opt-in.
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
		// A malformed EXISTING file: quarantine it (rename aside) BEFORE
		// returning defaults, so the debounced saver can never clobber the
		// only copy of the user's favourites/logins/macros/learned formats.
		// The rename runs on the startup load path (no saver exists yet, no
		// render/decode path) — cheap synchronous I/O is fine here (spec §2
		// bars sync I/O on hot paths only). See item #3.
		perr = fmt.Errorf("config: parsing %s (using defaults): %w", path, perr)
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
// regardless of how many signals are already queued. Callers hold the write lock.
func (p *AssetPreferences) markDirty() {
	markDirtyCalls.Add(1)
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
// preserves nothing. Keyed by Go field name so a rename can't silently break it.
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

// resetLocked overwrites every exported field with a fresh-defaults value,
// skipping unexported machinery (mu/saver/path — PkgPath != "") and any name in
// keep. Caller holds the write lock. Reflection (a rare, cold user action) keeps
// this robust: a newly-added setting resets automatically, no enumeration to
// forget.
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

	data, err := json.MarshalIndent(p.Load(), jsonMarshalPrefix, jsonMarshalIndent)
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

// FormatOrder returns the configured (pre-fallback) probe order for a type.
func (s *Settings) FormatOrder(typeName string) []string {

	if tp, ok := s.AssetTypes[typeName]; ok && len(tp.FormatOrder) > 0 {
		return cloneStrings(tp.FormatOrder)
	}
	return DefaultFormatOrder(typeName)
}

// SetFormatOrder replaces the probe order for a type and invalidates learned
// formats for that type on every host (the learned format may no longer be
// first preference). No-op when the order is unchanged.
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

// ResetFormatOrder restores the built-in default order for a type and
// invalidates learned formats for that type.
func (p *AssetPreferences) ResetFormatOrder(typeName string) {
	p.SetFormatOrder(typeName, DefaultFormatOrder(typeName))
}

// TypeFallbacksEnabled reports whether the legacy chain is enabled for the
// type specifically (the global toggle is separate).
func (s *Settings) TypeFallbacksEnabled(typeName string) bool {

	return s.AssetTypes[typeName].FallbacksEnabled
}

// SetTypeFallbacks toggles the legacy chain for one type and invalidates
// learned formats for that type. No-op when unchanged.
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

// SetGlobalFallbacks toggles fallbacks for every type and invalidates all
// learned formats (every type's effective probe list changed).
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

// --- Animation toggle -------------------------------------------------------

// --- Emote button images ----------------------------------------------------

// --- Smooth scaling -----------------------------------------------------------

// SpriteScalingMode reports the per-sprite texture filter (SpriteScalingAuto /
// Smooth / Pixel). Clamped on read as well as on load, so a value written by a
// newer build can never index past this build's options.
func (p *AssetPreferences) SpriteScalingMode() int {
	snap := p.Load()

	return ClampSpriteScaling(snap.SpriteScaling)
}

// --- Update check ------------------------------------------------------------

// --- Missing-asset report ----------------------------------------------------

// --- Sprite repositioning ---------------------------------------------------

// --- Desk format policy -----------------------------------------------------

// --- Sprite hover preview ---------------------------------------------------

// PreviewHoverMillis returns the configured hover dwell in milliseconds
// (clamped), for the Settings readout.
func (s *Settings) PreviewHoverMillis() int {
	return value.Clamp(orDefault(s.PreviewHoverMs, DefaultPreviewHoverMs), minPreviewHoverMs, maxPreviewHoverMs)
}

// PreviewHoverDelay returns the hover dwell as a duration for the UI.
func (p *AssetPreferences) PreviewHoverDelay() time.Duration {
	return time.Duration(p.Load().PreviewHoverMillis()) * time.Millisecond
}

// PreviewHeightPx returns the preview box's default height in px (the
// shipped default when unset; clamped to the grip's own bounds).
func (s *Settings) PreviewHeightPx() int {
	return value.Clamp(orDefault(s.PreviewHeightPxVal, DefaultPreviewHeightPx), MinPreviewHeightPx, MaxPreviewHeightPx)
}

// SetPreviewHeightPx clamps and persists the preview box's default height.
func (p *AssetPreferences) SetPreviewHeightPx(n int) {
	n = value.Clamp(n, MinPreviewHeightPx, MaxPreviewHeightPx)
	p.Update(func(sn *Settings) {
		sn.PreviewHeightPxVal = n
	})
}

// PreviewWindowRect returns the detached emote-preview window's last saved
// desktop position + client size, and ok=true only if one was ever saved
// (see PreviewWinRectValid's doc for why a bare zero-value rect can't double
// as "never popped out" here the way PreviewHeightPx's sentinel can).
func (s *Settings) PreviewWindowRect() (x, y, w, h int, ok bool) {

	if !s.PreviewWinRectValid {
		return 0, 0, 0, 0, false
	}
	return s.PreviewWinRectX, s.PreviewWinRectY, s.PreviewWinRectW, s.PreviewWinRectH, true
}

// SetPreviewWindowRect persists the detached preview window's position and
// size. Called ONLY at a natural boundary — the window closing (its own X,
// or the pop-out toggle re-attaching) — never per-frame or per-drag/-resize
// tick (CLAUDE.md hard rules 2/3: learning marks dirty, the debounced saver
// flushes; there is no per-move disk write here at all).
func (p *AssetPreferences) SetPreviewWindowRect(x, y, w, h int) {
	p.Update(func(sn *Settings) {
		sn.PreviewWinRectValid = true
		sn.PreviewWinRectX = x
		sn.PreviewWinRectY = y
		sn.PreviewWinRectW = w
		sn.PreviewWinRectH = h
	})
}

// EmoteHoverNamesMillis returns the configured emote-name hover dwell in
// milliseconds (clamped), for the Settings readout.
func (s *Settings) EmoteHoverNamesMillis() int {
	return value.Clamp(orDefault(s.EmoteHoverNamesMs, DefaultEmoteHoverNamesMs), minEmoteHoverNamesMs, maxEmoteHoverNamesMs)
}

// EmoteHoverNamesDelay returns the emote-name hover dwell as a duration.
func (p *AssetPreferences) EmoteHoverNamesDelay() time.Duration {
	return time.Duration(p.Load().EmoteHoverNamesMillis()) * time.Millisecond
}

// EmoteGridGap returns the gap between emote icons on the AsyncAO grid, in
// LOGICAL px. Read once per grid draw (never per cell) — it takes the pref lock.
// No "<= 0 means default" rescue here, unlike PreviewHeightPx: zero is a value the
// user can pick (icons flush against each other), so the only way to get the
// default back is the slider. defaultPrefs seeds it and the load overlay only
// overwrites a value the file actually carried, so it can never arrive unset.
func (p *AssetPreferences) EmoteGridGap() int {
	snap := p.Load()

	return value.Clamp(snap.EmoteGridGapPx, MinEmoteGridGapPx, MaxEmoteGridGapPx)
}

// ClearLearnedType drops every host's learned format for one asset type (e.g.
// after changing the desk-format policy) so the next probe re-derives it.
func (p *AssetPreferences) ClearLearnedType(typeName string) {
	p.Update(func(sn *Settings) {
		dropLearnedType(sn, typeName)
	})
	p.formatGen.Add(1)
}

// ClearLearnedHost drops every learned format belonging to ONE host and reports
// how many entries went, so the next probe of each asset class on that server
// re-derives from the configured order. It is the scoped counterpart to
// ClearLearned: a server that repacked its art is one server's problem, and
// wiping the whole table to fix it throws away every OTHER server's warm state
// — each of which then pays a full cold re-probe on the next visit. The learned
// key is "<host><LearnedKeySeparator><type name>" and the separator cannot occur
// in a host, so matching the prefix is exact.
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

// HighlightColorRGB returns the packed 0xRRGGBB log-selection highlight colour.
func (s *Settings) HighlightColorRGB() int {

	return s.HighlightColor & rgbMask
}

// ICCustomColorRGB returns the last free IC hex pick, packed 0xRRGGBB
// (seeds the colour wheel; defaultICCustomColor until first use).
func (s *Settings) ICCustomColorRGB() int {

	return s.ICCustomColor & rgbMask
}

// SetNameColorSatVal clamps and stores the saturation + brightness together.
func (p *AssetPreferences) SetNameColorSatVal(sat, val int) {
	p.Update(func(sn *Settings) {
		sn.NameSat = sat
		sn.NameVal = val
	})
}

// BgSlideshowSeconds is the per-background dwell time, clamped to its bounds
// (an unset/zero value falls back to the default).
func (s *Settings) BgSlideshowSeconds() int {
	return value.Clamp(orDefault(s.BgSlideshowSecs, defaultBgSlideshowSecs), minBgSlideshowSecs, maxBgSlideshowSecs)
}

// DownloadCapKBps reports the downloader bandwidth cap in KiB/s (0 = unlimited).
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
func (s *Settings) TimerSecondsValue() int {
	return clampTimerSeconds(s.TimerSeconds)
}

// SetTimerSeconds stores the countdown duration, clamped to [min,max].
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

// InstantReplaySecondsValue returns the clamped capture-window length in seconds
// (the value the slider shows); 0/unset reads as the default.
func (s *Settings) InstantReplaySecondsValue() int {
	return clampInstantReplaySeconds(s.InstantReplaySeconds)
}

// LastServer returns the last server connected to (name, ws URL) — the
// auto-connect / quick-connect target. Empty url means none yet.
func (s *Settings) LastServer() (name, url string) {

	return s.LastServerName, s.LastServerURL
}

// SetLastServer records the last server connected to (persisted for auto-connect
// + quick-connect). Called on every connect.
func (p *AssetPreferences) SetLastServer(name, url string) {
	p.Update(func(sn *Settings) {
		sn.LastServerName = name
		sn.LastServerURL = url
	})
}

// SetExportOpts stores the export options (each field clamped to its range).
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

// SetSpriteStyle stores the user's sprite style (opacity clamped to [0,100]).
func (p *AssetPreferences) SetSpriteStyle(s SpriteStylePref) {
	s.Opacity = clampOpacity(s.Opacity)
	p.Update(func(sn *Settings) {
		sn.MySpriteStyle = s
	})
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
func (s *Settings) StylePresetCount() int {

	return len(s.SavedStyles)
}

// StylePresets returns a copy of the saved presets (so a caller can't mutate the live slice).
func (s *Settings) StylePresets() []StylePreset {

	if len(s.SavedStyles) == 0 {
		return nil
	}
	return append([]StylePreset(nil), s.SavedStyles...)
}

// AddStylePreset appends a preset (its opacity clamped), ignoring a blank name or a list at
// the cap. A duplicate name overwrites the existing one (so "save" updates a mood in place).
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

// DeleteStylePreset removes the preset at i (bounds-checked).
func (p *AssetPreferences) DeleteStylePreset(i int) {
	p.Update(func(sn *Settings) {
		sn.DeleteStylePreset(i)
	})
}

// SetStylePresetKey binds (or clears, key=="") a bare key to preset i, removing that key from
// any other preset first so a key maps to exactly one mood.
func (p *AssetPreferences) SetStylePresetKey(i int, key string) {
	key = strings.ToLower(strings.TrimSpace(key))
	p.Update(func(sn *Settings) {
		sn.SetStylePresetKey(i, key)
	})
}

// StylePresetForKey returns the preset bound to a bare key (ok=false when none).
func (s *Settings) StylePresetForKey(key string) (StylePreset, bool) {

	key = strings.ToLower(strings.TrimSpace(key))

	for _, pr := range s.SavedStyles {
		if pr.Key != "" && pr.Key == key {
			return pr, true
		}
	}
	return StylePreset{}, false
}

// SetPingChip toggles the connection-quality chip.

// SetValidateTLSCerts toggles strict wss certificate verification.

// SetAssetOriginHeader sets (or clears, when blank) the asset-fetch Origin override.

func (p *AssetPreferences) SetAssetOriginHeader(s string) {
	s = strings.TrimSpace(s)
	if p.Load().AssetOrigin == s {
		return
	}
	p.Update(func(sn *Settings) { sn.AssetOrigin = s })
}

// SetWSOriginHeader sets (or clears, when blank) the WS-handshake Origin override.
// Applies on the NEXT connect (an open socket keeps its handshake).
func (p *AssetPreferences) SetWSOriginHeader(s string) {
	s = strings.TrimSpace(s)
	if p.Load().WSOrigin == s {
		return
	}
	p.Update(func(sn *Settings) { sn.WSOrigin = s })
}

// PanelFont is one panel's user-chosen font. Both fields are optional and
// independent: a user may want their own SIZE while keeping the theme's family,
// or the reverse.
type PanelFont struct {
	// Family is a font family name ("Arial") or a path to a font file. Resolved
	// off-thread against the same ladder a theme's own families use, so typing a
	// family name works on Windows; a path is the route everywhere else.
	Family string `json:"family,omitempty"`
	// SizePx is the size in PIXELS, 0 = keep whatever the theme or the client
	// would have used. Pixels rather than points because it is what the user is
	// actually looking at — a theme declares points because Qt does, and the
	// point-to-pixel conversion is an implementation detail nobody should have to
	// do in their head to pick a readable size.
	SizePx int `json:"px,omitempty"`
}

// Panel-font bounds. The map is keyed by AO2 element identifier and comes off
// disk, so both its size and its values need named caps (hard rule 4).
const (
	// PanelFontMaxEntries bounds the map. There are 8 dressable elements today
	// and the ceiling exists only so a hand-edited or corrupt file cannot grow an
	// unbounded map; 32 leaves room for every element AO2 might add.
	PanelFontMaxEntries = 32
	// PanelFontMinPx / PanelFontMaxPx bound a chosen size. Below 6 px text is not
	// legible at any DPI; above 96 one line fills a panel. These are the USER's
	// bounds and are deliberately wider than the theme's own clamp, because a
	// user asking for very large text is usually asking for it on purpose.
	PanelFontMinPx = 6
	PanelFontMaxPx = 96
	// PanelFontFamilyMaxLen bounds one stored family/path. A long value is a
	// corrupt file, not a real font name.
	PanelFontFamilyMaxLen = 260
)

// sanitizePanelFonts bounds and normalises a map read off disk. Entries that
// survive are exactly the ones the resolver will act on, so what the Settings
// screen shows and what actually applies cannot drift.
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

// FontWarnThemesCap bounds the remembered "already warned" set. It grows by one
// per THEME with missing fonts — a handful in practice — and the cap keeps the
// preferences file from growing without limit (hard rule 4). Oldest first out, so
// someone who cycles through many themes is eventually re-warned about one they
// saw long ago, which is the right way round.
const FontWarnThemesCap = 64

// clampStrings trims a list read off disk to the newest n entries, dropping
// blanks. Shared by the bounded string lists loaded from preferences.
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

// FontWarnShownFor reports whether the missing-font warning has already been
// shown for this theme.
func (s *Settings) FontWarnShownFor(theme string) bool {

	theme = strings.TrimSpace(theme)
	if theme == "" {
		return true // nothing to key on: never warn rather than warn every apply
	}

	return containsFold(s.FontWarnThemes, theme)
}

// MarkFontWarnShown records that this theme has been warned about. Idempotent.
func (p *AssetPreferences) MarkFontWarnShown(theme string) {
	theme = strings.TrimSpace(theme)
	if theme == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.MarkFontWarnShown(theme)
	})
}

// ClearFontWarnThemes forgets every warned theme, so the warnings come back. The
// way out for someone who dismissed one by reflex and wants it again.
func (p *AssetPreferences) ClearFontWarnThemes() {
	p.Update(func(sn *Settings) {
		sn.ClearFontWarnThemes()
	})
}

// PanelFonts returns a COPY of the per-panel overrides. A copy because the
// caller reads it off the render thread and the saver marshals the original
// under its own lock — handing out the live map would be a data race the
// -race build would only catch on an unlucky interleaving.
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

// PanelFontFor returns one element's override (the zero value = none).
func (s *Settings) PanelFontFor(id string) PanelFont {

	return s.PanelFontsVal[strings.ToLower(id)]
}

// SetPanelFont stores (or, for an empty override, REMOVES) one element's
// override. Removing rather than storing a zero value keeps the saved file
// honest: an element the user reset should look identical to one they never
// touched, so a later default change reaches them.
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

// ClearPanelFonts drops every per-panel override at once — the "put it all back"
// button, which matters because these are eight separate settings and undoing
// them one at a time is how a user ends up stuck with a font they cannot read.
func (p *AssetPreferences) ClearPanelFonts() {
	p.Update(func(sn *Settings) {
		sn.ClearPanelFonts()
	})
}

// Proxy modes. The values are persisted, so their ORDER is load-bearing and new
// modes must be appended.
// The mirror of netproxy.Mode, spelled out here rather than imported, because
// internal/config sits below internal/netproxy and a preference package that
// depends on a network package would invert the layering. A test in internal/ui,
// which imports both, pins the two together.
const (
	// ProxyModeSystem takes the machine's own configuration — the default,
	// because a client that ignores it is the defect this exists to fix.
	ProxyModeSystem = 0
	// ProxyModeDirect never proxies, whatever the machine says. This is the
	// escape hatch, and it has to be one click: a detected proxy that cannot
	// carry AO traffic would otherwise leave a user with no way back.
	ProxyModeDirect = 1
	// ProxyModeManual uses ProxyURL and nothing else.
	ProxyModeManual = 2
	// ProxyModeCount bounds the Settings cycle button and both clamps.
	ProxyModeCount = 3
)

// SetProxyURL stores the operator's proxy address. Validation deliberately does
// NOT happen here: an address that cannot be parsed has to reach netproxy, which
// fails closed and says so in the readout. Rejecting it at the setter would put
// the user back to whatever was there before with no explanation, which is how a
// field silently refuses to hold what you typed.
func (p *AssetPreferences) SetProxyURL(s string) {
	s = strings.TrimSpace(s)
	if p.Load().ProxyURL == s {
		return
	}
	p.Update(func(sn *Settings) { sn.ProxyURL = s })
}

// AssetCharCaseMax is the highest valid character-folder casing mode. It mirrors
// courtroom.CharCaseCount-1 (lowercase / first-cap / title / auto) and is spelled
// out here rather than imported, because internal/config sits BELOW
// internal/courtroom and must not depend on it. A test in internal/ui — which
// imports both — pins the two together.
const AssetCharCaseMax uint8 = 3

// SetAssetCharCasing sets the character-folder casing mode. Out-of-range falls
// back to lowercase, which is the safe answer for almost every server — the same
// thing URLBuilder.WithCharCase does with a value it does not recognise.
func (p *AssetPreferences) SetAssetCharCasing(c uint8) {
	if c > AssetCharCaseMax {
		c = 0
	}
	p.Update(func(sn *Settings) {
		sn.AssetCharCase = c
	})
}

// VoiceOutVol reports the voice output volume 0..100 (default 100 when unset).
func (s *Settings) VoiceOutVol() int {
	return value.Clamp(orDefault(s.VoiceOutVolume, defaultVoiceOutVol), 0, percentMax)
}

// PrefetchAggressiveness reports how many of the top predicted next sprites the
// Markov prefetcher warms per message, 1..4 (default 1 = conservative). #100.
func (s *Settings) PrefetchAggressiveness() int {
	return value.Clamp(s.PrefetchAggro, 1, prefetchAggroMax)
}

// SetPrefetchAggressiveness clamps (1..4) and persists the prefetch aggressiveness.
func (p *AssetPreferences) SetPrefetchAggressiveness(n int) {
	n = value.Clamp(n, 1, prefetchAggroMax)
	p.Update(func(sn *Settings) {
		sn.PrefetchAggro = n
	})
}

// SetProfile stores the user's character profile (every field length-clamped).
func (p *AssetPreferences) SetProfile(pr ProfilePref) {
	pr = clampProfile(pr)
	p.Update(func(sn *Settings) {
		sn.MyProfile = pr
	})
}

// Spotlight dim-strength bounds + default. The slider range is kept off 0/100 so "on" always
// reads as a spotlight (a usable shadow, never full black, never invisible).

const (
	minSpotlightStrength     = 10
	maxSpotlightStrength     = 90
	defaultSpotlightStrength = 55
)

// SpotlightLevel reports the dim intensity [10,90] (higher = darker non-speakers); 0 (unset)
// resolves to the default so enabling the toggle always shows a visible effect.

func (s *Settings) SpotlightLevel() int {
	return orDefault(s.SpotlightStrength, defaultSpotlightStrength)
}

// SetSpotlightLevel stores the dim intensity (clamped to [10,90]).
func (p *AssetPreferences) SetSpotlightLevel(v int) {
	v = value.Clamp(v, minSpotlightStrength, maxSpotlightStrength)
	p.Update(func(sn *Settings) {
		sn.SpotlightStrength = v
	})
}

// Idle-breathing (#122) defaults. The two components default ON (stored inverted so the
// zero-value pref breathes both ways); amplitude/speed default to a gentle middle.
const (
	defaultBreathAmp   = 40
	defaultBreathSpeed = 50
)

// BreathBobOn / BreathScaleOn report whether each component is enabled (both default ON; the
// pref stores the inverted "disabled" flag so the zero value means on).

func (s *Settings) BreathBobOn() bool {

	return !s.BreathNoBob
}
func (s *Settings) BreathScaleOn() bool {

	return !s.BreathNoScale
}

// BreathAmp / BreathSpeed report the sliders [1,100]; 0 (unset) resolves to the default.
func (s *Settings) BreathAmp() int {
	return orDefault(s.BreathAmount, defaultBreathAmp)
}

func (s *Settings) BreathSpeed() int {
	return orDefault(s.BreathRate, defaultBreathSpeed)
}

// defaultReflectStrength is the #123 reflection opacity when unset (so the slider shows a
// sensible value and enabling the toggle is visible at once).
const defaultReflectStrength = 30

// ReflectStrength reports the reflection opacity [0,100]; 0 (unset) resolves to the default.

func (s *Settings) ReflectStrength() int {
	return orDefault(s.ReflectOpacity, defaultReflectStrength)
}

// SetReflectStrength stores the reflection opacity (clamped to [0,100]).
func (p *AssetPreferences) SetReflectStrength(v int) {
	v = value.Clamp(v, 0, percentMax)
	p.Update(func(sn *Settings) {
		sn.ReflectOpacity = v
	})
}

// weatherKindMax is the highest valid weather index (render.WeatherCount-1 = embers); the
// config can't import render, so it's pinned here and a ui test keeps the two equal.
const (
	weatherKindMax        = 4 // None=0, Snow, Rain, Sakura, Embers
	defaultWeatherDensity = 60
)

// WeatherType reports the ambient-weather index (#124); 0 = None (off). Clamped to the valid
// range so a hand-edited pref can't select a non-existent weather.
func (p *AssetPreferences) WeatherType() int {
	snap := p.Load()

	if snap.WeatherKind < 0 || snap.WeatherKind > weatherKindMax {
		return 0
	}
	return snap.WeatherKind
}

// SetWeatherType stores the weather index (clamped to [0,weatherKindMax]).
func (p *AssetPreferences) SetWeatherType(v int) {
	v = value.Clamp(v, 0, weatherKindMax)
	p.Update(func(sn *Settings) {
		sn.WeatherKind = v
	})
}

// stageFrameKindMax is the highest valid stage-frame style index (#56). The style
// list lives in ui (stageFrameNames); a ui contract test keeps the two equal, the
// same arrangement as weatherKindMax above.
const stageFrameKindMax = 7

// StageFrame reports the decorative viewport-frame style (#56); 0 = Off (the
// default). Clamped so a hand-edited pref can't select a non-existent style.
func (p *AssetPreferences) StageFrame() int {
	snap := p.Load()

	if snap.StageFrameKind < 0 || snap.StageFrameKind > stageFrameKindMax {
		return 0
	}
	return snap.StageFrameKind
}

// WeatherIntensity reports the density slider [1,100]; 0 (unset) resolves to the default.
func (s *Settings) WeatherIntensity() int {
	return orDefault(s.WeatherDensity, defaultWeatherDensity)
}

// SetWeatherIntensity stores the density (clamped to [1,100]).
func (p *AssetPreferences) SetWeatherIntensity(v int) {
	v = value.Clamp(v, 1, percentMax)
	p.Update(func(sn *Settings) {
		sn.WeatherDensity = v
	})
}

// SpriteTintColorRGB returns the packed 0xRRGGBB solid-tint colour.
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

// --- Theme -------------------------------------------------------------------

// Theme reports the selected theme name ("" = default) and the custom theme
// root directory ("" = no custom root configured).
func (s *Settings) Theme() (name, dir string) {

	return s.ThemeName, s.ThemeDir
}

// SetTheme stores the selected theme name and custom theme root.
func (p *AssetPreferences) SetTheme(name, dir string) {
	p.Update(func(sn *Settings) {
		sn.ThemeName = name
		sn.ThemeDir = dir
	})
}

// SetSubtheme stores the AO2 subtheme folder ("" = none). The value is
// sanitized here as well as on load: it is joined onto a filesystem path, so a
// separator or a ".." must never survive the setter either.
// An EMPTY argument clears the pick; a non-empty one that cannot be a folder
// name is REFUSED rather than folded into a clear. The two are different user
// intents ("no subtheme" vs "this subtheme"), and silently turning the second
// into the first would let one bad value wipe a good one.
func (p *AssetPreferences) SetSubtheme(sub string) {
	clean := theme.SanitizeSubtheme(sub)
	if clean == "" && strings.TrimSpace(sub) != "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SubthemeName = clean
	})
}

// --- Debug overlay -------------------------------------------------------------

// --- Casing alerts ---------------------------------------------------------------

// Casing reports the CASEA subscription: enabled + a CaseRole* bitmask
// (the bit layout is internal/courtroom's; config just persists it).
func (s *Settings) Casing() (enabled bool, roles int) {

	return s.CasingEnabled, s.CasingRoles
}

// SetCasing stores the CASEA subscription.
func (p *AssetPreferences) SetCasing(enabled bool, roles int) {
	p.Update(func(sn *Settings) {
		sn.CasingEnabled = enabled
		sn.CasingRoles = roles
	})
}

// --- Discord Rich Presence ------------------------------------------------------------

// SetAutoStatus stores the auto-status configuration (word fields bounded).
func (p *AssetPreferences) SetAutoStatus(a AutoStatusPref) {
	a = sanitizeAutoStatus(a)
	p.Update(func(sn *Settings) {
		sn.MyAutoStatus = a
	})
}

// ChromeTheme reports the AsyncAO chrome theme preset key (#M3; "dark" default).
func (s *Settings) ChromeTheme() string {
	return orDefault(s.ChromeThemeKey, defaultChromeTheme)
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
	snap := p.Load()

	return sanitizeChromeShape(snap.ChromeShapeKey)
}

// SetChromeShape stores the chrome shape preset key (sanitised: an unknown key
// is stored as "sharp"). Debounced saver; never writes synchronously.
func (p *AssetPreferences) SetChromeShape(key string) {
	key = sanitizeChromeShape(key)
	p.Update(func(sn *Settings) {
		sn.ChromeShapeKey = key
	})
}

// ChromeShapeTier reports the corner-radius size class for the "rounded" preset
// (0..shapeRadiusTiers-1), always clamped in range.
func (p *AssetPreferences) ChromeShapeTier() int {
	snap := p.Load()

	return clampChromeShapeTier(snap.ChromeShapeTierIdx)
}

// SetChromeShapeTier stores the corner-radius size class (clamped in range).
func (p *AssetPreferences) SetChromeShapeTier(tier int) {
	tier = clampChromeShapeTier(tier)
	p.Update(func(sn *Settings) {
		sn.ChromeShapeTierIdx = tier
	})
}

// --- Callwords + hotkeys -------------------------------------------------------------

// callWordCap bounds the highlight list (rule §17.4).
const callWordCap = 32

// CallWords reports the highlight words (lowercased on save).
func (s *Settings) CallWords() []string {

	return cloneStrings(s.CallWordList)
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
	p.Update(func(sn *Settings) {
		sn.CallWordList = clean
	})
}

// AddCallWord adds one or more highlight words from a single input, split on
// commas so a pasted "a, b, c" adds them all at once. Each is lowercased +
// trimmed; blanks and case-insensitive duplicates are skipped, up to
// callWordCap. Returns how many were newly added (0 = nothing new / at the cap),
// mirroring AddMusicHost for the Settings list manager.
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

// RemoveCallWord drops a highlight word (case-insensitive). Reports a change.
func (p *AssetPreferences) RemoveCallWord(word string) bool {
	word = strings.ToLower(strings.TrimSpace(word))
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveCallWord(word)
	})
	return changed
}

// Hotkey reports the configured key name for an action ("" = unset; the
// UI layer owns defaults — config just persists overrides).
func (s *Settings) Hotkey(action string) string {

	return s.HotkeyMap[action]
}

// SetHotkey stores one action's key name.
func (p *AssetPreferences) SetHotkey(action, key string) {
	p.Update(func(sn *Settings) {
		sn.SetHotkey(action, key)
	})
}

// --- Per-server warm state -----------------------------------------------------------

// ServerWarmInfoFor reports the remembered state for a server key.
func (s *Settings) ServerWarmInfoFor(key string) ServerWarmInfo {

	return snapCloneServerWarmInfo(s.ServerWarm[key])
}

// RememberServerChar records the character last used on a server.
func (p *AssetPreferences) RememberServerChar(key, char string) {
	p.rememberServer(key, func(w *ServerWarmInfo) { w.Char = char })
}

// ServerFriends returns a server's highlighted shownames (a clone — the
// settings editor mutates it; callers must not alias the stored slice).
func (s *Settings) ServerFriends(key string) []string {

	return cloneStrings(s.ServerWarm[key].Friends)
}

// friendParts splits a friend entry "name[=RRGGBB[=nick]]" into its three fields
// (#82). SplitN with 3 keeps the colour parse robust even if the nickname
// itself contains an "=". Trimmed; missing fields come back "".
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

// ServerFriendInfo reports whether name (case-insensitive) is a highlighted
// friend on the server, plus that friend's custom glow colour (packed 0xRRGGBB,
// or -1 for the default tint) and personal nickname ("" if none) — #82. ONE scan
// under the lock with no allocation, so the player list (per row) and friend
// matching (per message) read all three from a single call.
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

// ServerFriendMatch reports whether name is a highlighted friend and its custom
// glow colour — a thin wrapper over ServerFriendInfo (one scan, no nested lock)
// kept for the per-message/glow callers that don't need the nickname.
func (s *Settings) ServerFriendMatch(key, name string) (friend bool, color int) {
	f, c, _ := s.ServerFriendInfo(key, name)
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
func (s *Settings) ServerIgnored(key string) []string {

	return cloneStrings(s.ServerWarm[key].Ignored)
}

// ServerIgnoreMatch reports whether name (case-insensitive) is on the server's
// ignore list. Scans under the lock with NO allocation — safe to call per
// incoming IC/OOC message (an empty list, the default, costs one RLock + zero
// iterations).
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
	snap := p.Load()

	w := snap.ServerWarm[key]
	// Each channel: this server's explicit level if it set one, else the global default
	// (so an unset channel — e.g. SFX a prior build never wrote — is never silently 0).
	return w.AudioOn,
		ptrOr(w.AudioMaster, snap.MasterVol),
		ptrOr(w.AudioMusic, snap.MusicVol),
		ptrOr(w.AudioSFX, snap.SFXVol),
		ptrOr(w.AudioBlip, snap.BlipVol)
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
	m := value.Clamp(master, 0, defaultAudioVolume)
	mu := value.Clamp(music, 0, defaultAudioVolume)
	s := value.Clamp(sfx, 0, defaultAudioVolume)
	b := value.Clamp(blip, 0, defaultAudioVolume)
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

// --- Hidden UI panels ---------------------------------------------------------------

// HiddenPanels reports the courtroom chrome regions the user hid.
func (s *Settings) HiddenPanels() []string {

	out := make([]string, len(s.HiddenPanelIDs))
	copy(out, s.HiddenPanelIDs)
	return out
}

// SetHiddenPanels replaces the hidden-chrome set. Dedups blank/duplicate ids and
// bounds the slice at maxHiddenPanels (rule §17.4).
func (p *AssetPreferences) SetHiddenPanels(ids []string) {
	clean := sanitizeHiddenPanels(ids)
	p.Update(func(sn *Settings) {
		sn.HiddenPanelIDs = clean
	})
}

// --- OOC name -----------------------------------------------------------------

// --- Layout scales --------------------------------------------------------------

// LayoutScales reports the courtroom layout knobs: viewport width percent,
// chat TEXT percent, chat BOX height percent, log/OOC list text percent,
// input field height percent. All clamped at load and set time.
func (s *Settings) LayoutScales() (viewport, chatText, chatBox, logText, input int) {

	return s.ViewportPct, s.ChatScalePct, s.ChatBoxPct, s.LogScalePct, s.InputHeightPct
}

// SetLayoutScales clamps and persists the layout knobs.
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

// LayoutPartColorCount is the number of individually-tintable layout parts
// (requested by Tifera: colour individual parts of the layout). The ui package
// owns the index meanings (log / OOC / emote grid / chatbox); this package
// just stores the hex slots, blank = use the chrome default.
const LayoutPartColorCount = 4

// SetLayoutPartColor stores one part's hex override ("" clears it back to the
// chrome default). Out-of-range indices are ignored.
func (p *AssetPreferences) SetLayoutPartColor(i int, hex string) {
	if i < 0 || i >= LayoutPartColorCount {
		return
	}
	p.Update(func(sn *Settings) {
		sn.LayoutPartHex[i] = hex
	})
}

// BoldNamesOn reports whether speaker names render bold (IC/OOC log + chatbox).
// Default ON — the pref is stored inverted (BoldNamesOff) so an absent value
// means bold, matching the on-by-default behaviour.
func (s *Settings) BoldNamesOn() bool {

	return !s.BoldNamesOff
}

// SetBoldNames toggles bold speaker names.
func (p *AssetPreferences) SetBoldNames(on bool) {
	p.Update(func(sn *Settings) {
		sn.BoldNamesOff = !on
	})
}

// SetBreathBob toggles the idle-breath vertical bob. The stored field is the
// inverted BreathNoBob, so the closure assigns the inverse.
func (p *AssetPreferences) SetBreathBob(on bool) {
	p.Update(func(sn *Settings) { sn.BreathNoBob = !on })
}

// SetBreathScale toggles the idle-breath scale pulse (inverted BreathNoScale).
func (p *AssetPreferences) SetBreathScale(on bool) {
	p.Update(func(sn *Settings) { sn.BreathNoScale = !on })
}

// The following six setters stay explicit because their clamp is not a plain range the
// normalize can reproduce through a single field assignment: zero resolves to a default
// or "off" sentinel, or below-floor collapses to off instead of the floor value.
// The setter golden adapter (TestSetterGoldenViaUpdate) would flag them otherwise.

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

// SetInputGraceFrames maps non-positive n to InputGraceOff (the one-shot "off" stamp)
// and clamps positive n into the valid range.
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

// SetAnimatedSpriteCap sets the animated-sprite height cap; a value below the floor
// disables the cap (0), and 0 itself is the unset sentinel.
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

// below the floor = off, not a forced floor value

// SetUIScale clamps and persists the global UI scale.
func (p *AssetPreferences) SetUIScale(pct int) {
	pct = value.Clamp(pct, MinUIScalePercent, MaxUIScalePercent)
	p.Update(func(sn *Settings) {
		sn.UIScalePct = pct
	})
}

// WindowSize reports the saved windowed size (0,0 = use the default). Clamp to
// the display at apply (ClampWindowSize) — a raw saved value may be stale.
func (s *Settings) WindowSize() (int, int) {

	return s.WindowW, s.WindowH
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
	p.Update(func(sn *Settings) {
		sn.WindowW = w
		sn.WindowH = h
	})
}

// --- Audio volumes ----------------------------------------------------------------

// AudioVolumes reports the music/SFX/blip volumes (0–100).
func (s *Settings) AudioVolumes() (music, sfx, blip int) {

	return s.MusicVol, s.SFXVol, s.BlipVol
}

// SetAudioVolumes clamps and persists the mixer volumes.
func (p *AssetPreferences) SetAudioVolumes(music, sfx, blip int) {
	p.Update(func(sn *Settings) {
		sn.MusicVol = music
		sn.SFXVol = sfx
		sn.BlipVol = blip
	})
}

// The master/music/blip channel-mute accessors below persist the volume
// strip's (#10) click-to-mute state, so quitting muted stays muted on the next
// launch — they default OFF (unmuted), same as the zero value. SFXVolMuted is
// the deliberate exception: the SFX mute (the Mute-SFX hotkey AND the volume
// strip's SFX cell) is session-only (see ui.toggleSFXMute / ui.resetSessionState),
// so its field and accessors are retained only for schema compatibility and are
// never read by the app — an accidental Mute-SFX press must not survive a restart.

// HoldClear reports the hold-to-clear settings: whether it's on, the key name
// (SDL key name, e.g. "Backspace"), and the hold duration in ms.
func (s *Settings) HoldClear() (on bool, key string, ms int) {

	return s.HoldClearOn, s.HoldClearKey, s.HoldClearMs
}

// SetHoldClearKey rebinds the hold-to-clear key (an SDL key name; "" ignored).
func (p *AssetPreferences) SetHoldClearKey(key string) {
	if key == "" {
		return
	}
	if p.Load().HoldClearKey == key {
		return
	}
	p.Update(func(sn *Settings) { sn.HoldClearKey = key })
}

// ExtrasBoxStyle reports the Extras-box theme: background / gradient-bottom /
// border / title / text colours (hex, "" = stock) and whether the background is
// a gradient.
func (s *Settings) ExtrasBoxStyle() (bg, bg2, border, title, text string, gradient bool) {

	return s.ExtrasBg, s.ExtrasBg2, s.ExtrasBorder, s.ExtrasTitle, s.ExtrasText, s.ExtrasGradient
}

// SetExtrasBoxStyle persists the Extras-box theme.
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

// --- Message timing & master list ----------------------------------------------

// Timing reports the message timing knobs in milliseconds: typewriter
// crawl per character, post-message stay, and the minimum gap between
// outgoing chats (0 = no limit).
func (s *Settings) Timing() (crawlMs, stayMs, rateLimitMs int) {

	return s.TextCrawlMs, s.TextStayMs, s.ChatRateLimitMs
}

// SetTiming clamps and persists the timing knobs.
func (p *AssetPreferences) SetTiming(crawlMs, stayMs, rateLimitMs int) {
	crawlMs = value.Clamp(crawlMs, MinTextCrawlMs, MaxTextCrawlMs)
	rateLimitMs = value.Clamp(rateLimitMs, 0, MaxChatRateLimitMs)
	p.Update(func(sn *Settings) {
		sn.TextCrawlMs = crawlMs
		sn.TextStayMs = stayMs
		sn.ChatRateLimitMs = rateLimitMs
	})
}

// CatchUp reports the packed-room catch-up toggle and its queue-depth
// threshold (fast-forward the IC stage once more than threshold messages are
// queued).
func (s *Settings) CatchUp() (on bool, threshold int) {

	return s.CatchUpOn, s.CatchUpThreshold
}

// SetCatchUp clamps and persists the catch-up toggle + threshold.
func (p *AssetPreferences) SetCatchUp(on bool, threshold int) {
	p.Update(func(sn *Settings) {
		sn.CatchUpOn = on
		sn.CatchUpThreshold = threshold
	})
}

// TabCap is how many concurrent server tabs are allowed (clamped to
// [minMultiTabCap, maxMultiTabCap]; an unset/zero value falls back to the
// default, so older prefs files and tests behave as before).
func (s *Settings) TabCap() int {
	return value.Clamp(orDefault(s.MultiTabCap, DefaultMultiTabCap), minMultiTabCap, maxMultiTabCap)
}

// Cold-load sprite behaviour (SpriteLoadMode): what a character layer shows while
// an uncached sprite is still streaming + decoding. The values MUST equal
// render.SpriteLoad* (the UI mirrors the pref straight into the viewport); kept as
// plain ints here to avoid a config→render import.
const (
	SpriteLoadBlank     = 0 // Show nothing — draw nothing until the sprite lands (the original behaviour; the cold-load flash)
	SpriteLoadHoldPrev  = 1 // webAO — keep the previous sprite until the new one lands (webAO-style; the old default)
	SpriteLoadWait      = 2 // Hold the message — hold the MESSAGE off-stage until its speaker sprite decodes (client-AO-style; courtroom wait gate, timeout-capped)
	SpriteLoadDefault   = 3 // Default — strict sequencing: wait for EVERY asset a message presents before it plays (the courtroom SequentialWait gate)
	SpriteLoadModeCount = 4 // number of valid modes (for the Settings mode picker + load clamp)

	// defaultSpriteLoadMode is what a fresh install (and a power-user reset) uses:
	// Default (strict sequencing) — wait for every sprite, the background, desk,
	// chatbox, speedlines, SFX and blip before a message plays, so nothing ever
	// pops in mid-ceremony.
	defaultSpriteLoadMode = SpriteLoadDefault
)

// Wait-mode timeout bounds (SpriteLoadWait): how long one message may be held for
// its sprite before playing anyway. User-tunable within [min,max] — deliberately a
// WIDE range (power user: from a near-instant 50 ms nudge to a patient 30 s on a
// dial-up-grade link); the default is 0.5 s — a fast desktop client shouldn't
// stall a busy room for longer than that waiting on a first-load sprite.
const (
	SpriteWaitDefaultMs = 500
	SpriteWaitMinMs     = 50
	SpriteWaitMaxMs     = 30000
)

// SetSpriteLoadMode persists the cold-load sprite behaviour (clamped to a known mode).
func (p *AssetPreferences) SetSpriteLoadMode(mode int) {
	if mode < SpriteLoadBlank || mode >= SpriteLoadModeCount {
		mode = SpriteLoadBlank
	}
	p.Update(func(sn *Settings) {
		sn.SpriteLoadModeVal = mode
	})
}

// SpriteWaitMs reports the wait-mode hold cap in milliseconds (SpriteWaitDefaultMs
// when unset): how long SpriteLoadWait may hold one message for its sprite before
// playing anyway.
func (s *Settings) SpriteWaitMs() int {
	return orDefault(s.SpriteWaitMsVal, SpriteWaitDefaultMs)
}

// SetSpriteWaitMs persists the wait-mode hold cap (clamped to the tunable range).
func (p *AssetPreferences) SetSpriteWaitMs(ms int) {
	ms = value.Clamp(ms, SpriteWaitMinMs, SpriteWaitMaxMs)
	p.Update(func(sn *Settings) {
		sn.SpriteWaitMsVal = ms
	})
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

	// IC scrollback depth (the on-screen IC chat log). 0 = the shipped default
	// (the client's own icLogCap, 1024); ICLogDepthUnlimited = never cap it.
	ICLogDepthUnlimited = -1    // "don't cap at all" — the log grows for the whole session
	ICLogDepthMin       = 1     // shallowest log worth keeping
	ICLogDepthMax       = 65536 // deepest a typed number may set (still bounded; ∞ is the true uncap)

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

	// Animated-only height cap (px): an OPTIONAL, default-off memory knob that
	// downscales ANIMATED sprites harder than stills, so a long full-canvas
	// preanim costs less VRAM. 0 = off (animated sprites use the still cap);
	// anything below the floor snaps back to off.
	AnimatedSpriteCapMinPx = 240
	AnimatedSpriteCapMaxPx = 2160

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

	// Per-animated-asset decode budget, MiB (the #110 downscale-to-fit budget).
	// LIVE-applied: the decoder reads it atomically, so changing it reshapes NEW
	// animated decodes without a restart. 128 = the shipped default (downscales
	// very large clips to fit; lower = softer, far less RAM). The oversized GPU
	// overflow cap stays large enough to hold many compressed clips resident.
	AnimatedBudgetDefaultMiB = 128
	AnimatedBudgetMinMiB     = 64
	AnimatedBudgetMaxMiB     = 512

	// Concurrent animated decodes: how many long clips may decode at once. The
	// per-frame decoded RGBA of a full clip is the RAM spike, so this gates the
	// decode burst. 1 = slowest loads, least RAM; higher = faster, more RAM.
	AnimDecodeConcurrencyDefault = 2
	AnimDecodeConcurrencyMin     = 1
	AnimDecodeConcurrencyMax     = 8

	// Texture compression (restart-applied): how decoded frames are stored on
	// the GPU. Off = raw RGBA (32 bpp, lossless); DXT5 = BC3 (8 bpp, smooth
	// alpha, ~4x smaller); DXT1 = BC1 (4 bpp, 1-bit alpha, ~8x smaller). Lossy
	// (4x4 blocks); applies only to canvases whose width/height are multiples of
	// 4 and only when the renderer advertises the format.
	TexCompressOff  = 1
	TexCompressDXT5 = 2
	TexCompressDXT1 = 3

	// Speaker-swap crossfade: the new sprite fades in over N ms (0 = off, the
	// default hard swap). Suppressed by Reduce motion.
	CrossfadeMinMs = 50
	CrossfadeMaxMs = 1000

	// Frame pacing (the GPU-burn fix): the loop used to re-render the whole UI
	// every monitor refresh — on a 144/165 Hz laptop panel that's a full-screen
	// GPU composite 165×/sec while IDLE (fans, +10 °C — the playtest report).
	// Three caps, all sleep-based (vsync stays on for tear-free presents):
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
	return value.Clamp(fps, min, max)
}

// SetSpriteWaitPreanim persists the wait-covers-preanim knob.

// SetShoutBypassQueue persists the shout-bypass toggle.

// SetShoutDurationMs persists the shout hold (0 = default; else clamped).

// ThumbHeightPx reports the thumbnail height (ThumbHeightDefaultPx when unset).

func (s *Settings) ThumbHeightPx() int {
	return orDefault(s.ThumbHeightPxVal, ThumbHeightDefaultPx)
}

// ThumbQuality reports the thumbnail WebP quality (ThumbQualityDefault when unset).
func (s *Settings) ThumbQuality() int {
	return orDefault(s.ThumbQualityVal, ThumbQualityDefault)
}

// ThumbBudgetMiB reports the thumbnail store's byte budget in MiB
// (ThumbBudgetDefaultMiB when unset); the store auto-prunes oldest past it.
func (s *Settings) ThumbBudgetMiB() int {
	return orDefault(s.ThumbBudgetMiBVal, ThumbBudgetDefaultMiB)
}

// SetSpriteDownscalePct persists the downscale percent (0 = default; clamped).

// TexBudgetMiB reports the T1 texture byte budget in MiB (TexBudgetDefaultMiB
// when unset; applies on restart).
func (s *Settings) TexBudgetMiB() int {
	return orDefault(s.TexBudgetMiBVal, TexBudgetDefaultMiB)
}

// AnimatedBudgetMiB reports the per-animated-asset decode budget in MiB
// (AnimatedBudgetDefaultMiB when unset; LIVE-applied - new decodes pick it up).
func (s *Settings) AnimatedBudgetMiB() int {
	return orDefault(s.AnimatedBudgetMiBVal, AnimatedBudgetDefaultMiB)
}

// AnimDecodeConcurrency reports the concurrent animated-decode bound
// (AnimDecodeConcurrencyDefault when unset; LIVE-applied).
func (s *Settings) AnimDecodeConcurrency() int {
	return orDefault(s.AnimDecodeConcurrencyVal, AnimDecodeConcurrencyDefault)
}

// TexCompression reports the texture-compression mode (TexCompressOff /
// TexCompressDXT5 / TexCompressDXT1; restart-applied).
func (p *AssetPreferences) TexCompression() int {
	snap := p.Load()

	if snap.TexCompressionVal < TexCompressOff || snap.TexCompressionVal > TexCompressDXT1 {
		return TexCompressDXT5 // 0/absent -> the shipped default
	}
	return snap.TexCompressionVal
}

// SetTexCompression persists the texture-compression mode (clamped to 0/1/2).
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

// unknown -> the shipped default

// FPSCap / IdleFPS / UnfocusedFPS report the three frame-pacing rates
// (defaults when unset): the foreground ceiling, the nothing-is-animating
// idle rate, and the another-window-has-focus rate.
func (s *Settings) FPSCap() int {
	return orDefault(s.FPSCapVal, FPSCapDefault)
}

// IdleFPS reports the idle frame rate (IdleFPSDefault when unset).
func (s *Settings) IdleFPS() int {
	return orDefault(s.IdleFPSVal, IdleFPSDefault)
}

// UnfocusedFPS reports the unfocused frame rate (UnfocusedFPSDefault when unset).
func (s *Settings) UnfocusedFPS() int {
	return orDefault(s.UnfocusedFPSVal, UnfocusedFPSDefault)
}

// InputGraceFrames reports the post-input full-rate hold in frames
// (InputGraceFramesDefault when unset).
func (s *Settings) InputGraceFrames() int {
	if s.InputGraceFramesVal < 0 {
		return 0 // InputGraceOff: no hold
	}
	return orDefault(s.InputGraceFramesVal, InputGraceFramesDefault)
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
		pct = percentMax
	}
	return base * pct / percentMax
}

// ResetPowerUser reverts EVERY power-user option to its shipped default — the
// "nuke" button on the Power user tab. Scoped strictly to that tab's knobs: TLS
// validation, both Origin overrides, character-folder casing, the whole renderer
// block (cold-load mode + wait/hold knobs + tint), the sprite mask, and the core
// timings. The image-format block is deliberately NOT touched (probe orders +
// learned formats are per-server warm state with their own controls, and the full
// "Reset settings" already covers them). A guard test pins this field list.
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

// AO2 MUSIC_EFFECT bits, appended to an outgoing MC when the server advertises
// `effects` — LAST field, whose index depends on whether the conditional showname
// preceded it (courtroom.Session.RequestMusicWithFlags); the server's echo carries
// them at field 5. AO2-Client src/datatypes.h:95-101.
// Duplicated here as plain ints rather than imported from internal/courtroom for
// the same reason SpriteLoad* is: config must not depend on the packages that
// consume it.
const (
	MusicFlagFadeIn   = 1 // ramp the NEW track up
	MusicFlagFadeOut  = 2 // ramp the PREVIOUS track down
	MusicFlagSyncPos  = 4 // start the new track at the previous track's position
	MusicFlagNoRepeat = 8 // play once regardless of the looping field (KFO's "Loop" off)
	// MusicFlagsMask is every bit AO2 defines; anything else in a stored value is
	// dropped on load so a hand-edited or future file can't put junk on the wire.
	MusicFlagsMask = MusicFlagFadeIn | MusicFlagFadeOut | MusicFlagSyncPos | MusicFlagNoRepeat
	// DefaultMusicFlags matches AO2-Client's own initial value —
	// `int music_flags = MUSIC_EFFECT::FADE_OUT;` (src/courtroom.h:551).
	DefaultMusicFlags = MusicFlagFadeOut
)

// MusicFlags reports the MUSIC_EFFECT bitmask a track play carries.
func (s *Settings) MusicFlags() int {

	return s.MusicFlagsVal & MusicFlagsMask
}

// OpenTabList returns a copy of the remembered open tabs (restore-on-launch).
func (s *Settings) OpenTabList() []OpenTab {

	if len(s.OpenTabs) == 0 {
		return nil
	}
	out := make([]OpenTab, len(s.OpenTabs))
	copy(out, s.OpenTabs)
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
	p.Update(func(sn *Settings) {
		sn.OpenTabs = cp
	})
}

// --- AO2 post-send "sticky" options + sfx_on_idle ------------------------------
//
// All four are transcriptions of AO2 options with AO2's own shipped defaults; see
// the defaultSticky* / defaultSFXOnIdle constants for the canon citations. The
// getters read "does the pick SURVIVE the send", which is the config key's own
// sense — NOT the upstream accessor's backwards name.

// SetMasterList persists the master-list override.
func (p *AssetPreferences) SetMasterList(url string) {
	url = strings.TrimSpace(url)
	if p.Load().MasterListURL == url {
		return
	}
	p.Update(func(sn *Settings) { sn.MasterListURL = url })
}

// --- Wardrobe (per server) ----------------------------------------------------

// WardrobeList returns a copy of one server's custom character list.
func (s *Settings) WardrobeList(serverKey string) []string {

	return cloneStrings(s.ServerWarm[serverKey].Wardrobe)
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

// AddWardrobe stores a character folder in one server's wardrobe
// (case-insensitive dedupe, capped at WardrobeCap). Reports change.
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

// RemoveWardrobe drops a character folder from one server's wardrobe.
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

// WardrobeGeneration returns a counter that bumps whenever any server's
// wardrobe membership changes; the char-select star grid caches its
// membership set keyed by it (mirrors FormatGeneration).
func (p *AssetPreferences) WardrobeGeneration() uint64 {
	return p.wardrobeGen.Load()
}

// WardrobeFolderMap returns a copy of one server's lowercased char → folder
// map (wardrobe organization). nil when nothing is filed.
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

// fileFolder files one key under a per-server folder map (or clears it when
// folder is empty), bounded by cap. get/set read/write the map field on the
// warm server info; the wardrobe and favourite-background folders share the
// body so a fix to one cannot miss the other.
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

// deleteFolder ungroups one folder (or, when keepMembers is false, also drops
// each member from the flat list). list/setList read/write the member list the
// same way fileFolder's accessors do the folder map.
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

// SetWardrobeFolder files a wardrobe character under a folder (category) on one
// server; an empty folder clears it. Bounded by WardrobeCap.
func (p *AssetPreferences) SetWardrobeFolder(serverKey, char, folder string) {
	p.fileFolder(serverKey, char, folder, WardrobeCap,
		func(w *ServerWarmInfo) map[string]string { return w.WardrobeFolder },
		func(w *ServerWarmInfo, m map[string]string) { w.WardrobeFolder = m })
}

// DeleteWardrobeFolder removes a whole wardrobe folder on one server in a single
// op. keepMembers true just ungroups (clears the folder tag, the characters
// stay in the wardrobe, unfiled); false removes those characters from the
// wardrobe entirely. Either way the folder — being membership-derived — ceases
// to exist once nothing is filed under it.
func (p *AssetPreferences) DeleteWardrobeFolder(serverKey, folder string, keepMembers bool) {
	p.deleteFolder(serverKey, folder, keepMembers,
		func(w *ServerWarmInfo) map[string]string { return w.WardrobeFolder },
		func(w *ServerWarmInfo) []string { return w.Wardrobe },
		func(w *ServerWarmInfo, l []string) { w.Wardrobe = l })
}

// --- Favorite backgrounds (per server) ----------------------------------------

// FavBackgroundList returns a copy of one server's starred-background list.
func (s *Settings) FavBackgroundList(serverKey string) []string {

	return cloneStrings(s.ServerWarm[serverKey].FavBackgrounds)
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
		w.FavBackgrounds, changed = addFoldUnique(w.FavBackgrounds, name, FavBackgroundCap)
	})
	return changed
}

// RemoveFavBackground unstars a background on one server (and drops its folder
// entry, like RemoveWardrobe). Reports change.
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

// FavBackgroundFolderMap returns a copy of one server's lowercased background →
// folder map (Backgrounds-section organization). nil when nothing is filed.
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

// SetFavBackgroundFolder files a favourite background under a folder on one
// server; an empty folder clears it. Bounded by FavBackgroundCap. Mirrors
// SetWardrobeFolder.
func (p *AssetPreferences) SetFavBackgroundFolder(serverKey, bg, folder string) {
	p.fileFolder(serverKey, bg, folder, FavBackgroundCap,
		func(w *ServerWarmInfo) map[string]string { return w.FavBackgroundFolder },
		func(w *ServerWarmInfo, m map[string]string) { w.FavBackgroundFolder = m })
}

// DeleteFavBackgroundFolder removes a whole background folder on one server.
// keepMembers true ungroups (the backgrounds stay favourited, unfiled); false
// unstars those backgrounds entirely. Mirrors DeleteWardrobeFolder.
func (p *AssetPreferences) DeleteFavBackgroundFolder(serverKey, folder string, keepMembers bool) {
	p.deleteFolder(serverKey, folder, keepMembers,
		func(w *ServerWarmInfo) map[string]string { return w.FavBackgroundFolder },
		func(w *ServerWarmInfo) []string { return w.FavBackgrounds },
		func(w *ServerWarmInfo, l []string) { w.FavBackgrounds = l })
}

// --- Character keybinds (per server) -------------------------------------------

// CharKeyBinds returns a copy of one server's key → character map.
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

// ImportSettings validates that srcPath parses as a preferences file and
// atomically replaces the live one. Applied on the NEXT start: the live
// state has too many mirrors (resolver snapshots, UI caches) to hot-swap
// safely mid-session.
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

// LearnedExtCap bounds one (host, type) persisted learned list (rule §17.4).
// It mirrors the resolver's in-memory cap; the two must agree or a reload would
// silently drop formats the running session had learned.
const LearnedExtCap = 4

// RecordLearned persists ext as the known-working format for (host, type),
// moving it to the FRONT of whatever was already learned instead of replacing
// the list. The resolver calls this on every successful probe; persistence is
// lazy via the debounced saver.
// Promote, not overwrite: a host can serve more than one format for the same
// class, and dropping the others is what stranded ~560 PNG-button characters on
// umineko.online behind a lone .webp candidate. Mirrors assets.promoteExt — the
// two must stay in step or a restart would resurrect the truncated list.
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

// RecordLearnedList persists the full ordered format list for (host, type) —
// the extensions.json seed path, where the SERVER is the authority on what it
// serves and in what preference order. Unlike RecordLearned this replaces the
// list rather than promoting into it, because a re-read manifest supersedes
// whatever earlier probing inferred. A no-op when exts is empty, so a manifest
// that omits a class leaves what was already learned alone.
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

// equalStrings reports whether two string slices are element-wise equal.
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

// ExtProfile returns the per-host extensions.json format profile (raw JSON), or
// "" if none. A profile is seeded synchronously on connect and overrides both the
// fetched server manifest and the global default — for that host only.
func (s *Settings) ExtProfile(host string) string {

	return s.ExtProfiles[host]
}

// SetExtProfile sets (or clears, with "") a host's format profile.
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

// BlipTyping reports the chat-blip cadence: one blip per `rate` revealed letters
// (default 2), and whether spaces also blip (default false = skip whitespace).
func (s *Settings) BlipTyping() (rate int, onSpaces bool) {

	r := s.BlipRate
	if r < MinBlipRate {
		r = defaultBlipRate
	}
	return r, s.BlipOnSpaces
}

// SetBlipTyping clamps and persists the blip cadence + whitespace toggle.
func (p *AssetPreferences) SetBlipTyping(rate int, onSpaces bool) {
	rate = value.Clamp(rate, MinBlipRate, MaxBlipRate)
	p.Update(func(sn *Settings) {
		sn.BlipRate = rate
		sn.BlipOnSpaces = onSpaces
	})
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
	return float64(int(b)) * degreesPerTurn / rotationSteps
}

// RotationDegToByte is the inverse (rounded to the nearest byte, wrapping at
// 360°) — used when the Shift+R fine step advances by RotStepFineDeg.
func RotationDegToByte(deg int) uint8 {
	deg %= degreesPerTurn
	if deg < 0 {
		deg += degreesPerTurn
	}
	// Round to nearest byte: add half a turn-step before the integer divide.
	return uint8((deg*rotationSteps + degreesPerTurn/2) / degreesPerTurn % rotationSteps)
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

// HasThemeRectOverride reports whether one widget carries a persisted layout-editor
// override under a theme — the allocation-free probe ThemeRectOverrides cannot be
// (it copies the whole map, so a per-frame caller would allocate every frame).
// It exists because for one key the override's PRESENCE is itself the state: the
// server-tab strip is client chrome whose "the user has placed this on the theme's
// canvas" flag cannot be recovered from its geometry (a legitimate drag can land
// exactly on the pristine seed position). The strip's placement is therefore read
// back from the same record that persists it, once per frame.
func (s *Settings) HasThemeRectOverride(theme, key string) bool {

	if theme == "" || key == "" {
		return false
	}

	_, ok := s.ThemeRectOv[theme][key]
	return ok
}

// SetThemeRectOverride stores one widget's edited design rect.
func (p *AssetPreferences) SetThemeRectOverride(theme, key string, r [4]int) {
	if theme == "" || key == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetThemeRectOverride(theme, key, r)
	})
}

// ClearThemeRectOverride drops one widget's edit (key "" = the whole
// theme's edits).
func (p *AssetPreferences) ClearThemeRectOverride(theme, key string) {
	p.Update(func(sn *Settings) {
		sn.ClearThemeRectOverride(theme, key)
	})
}

// ThemeRectRotationSnapshot returns a copy of one theme's per-widget rotation
// angles (A4). The themed editor bakes this into themeLayoutCache.ang at cache
// rebuild time so the draw path resolves angles lock-free (a plain map probe on
// the pre-baked cache, never a prefs read per frame).
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

// SetThemeRectRotation stores one widget's rotation angle byte for a theme
// (bounded by the same themeOvThemesCap/themeOvRectsCap as the overrides). A
// zero angle is stored like any other so it round-trips; the draw path treats an
// ABSENT entry as angle 0, so a caller that wants "no rotation" should
// ClearThemeRectRotation rather than set 0 (keeps the map minimal).
func (p *AssetPreferences) SetThemeRectRotation(theme, key string, deg uint8) {
	if theme == "" || key == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetThemeRectRotation(theme, key, deg)
	})
}

// ClearThemeRectRotation drops one widget's rotation (key "" = the whole theme's
// rotations).
func (p *AssetPreferences) ClearThemeRectRotation(theme, key string) {
	p.Update(func(sn *Settings) {
		sn.ClearThemeRectRotation(theme, key)
	})
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

// LayoutProfile returns a deep copy of one saved profile (zero value if the name
// is unknown) (A6). Every axis is copied so the caller can't alias the live map.
func (s *Settings) LayoutProfile(name string) LayoutProfile {

	prof, ok := s.LayoutProfiles[name]
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
	p.Update(func(sn *Settings) {
		sn.SaveLayoutProfile(name, clean)
	})
}

// DeleteLayoutProfile removes a saved profile by name (A6).
func (p *AssetPreferences) DeleteLayoutProfile(name string) {
	p.Update(func(sn *Settings) {
		sn.DeleteLayoutProfile(name)
	})
}

// ClassicLayoutOverrides returns a sanitized copy of the default-courtroom slot
// overrides (window fractions). The classic-layout editor snapshots this once
// into an App-local map so the render path reads it lock-free.
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

// SetClassicSlot stores one default-courtroom slot's override (window fraction
// [x,y,w,h]). Bounded by classicSlotCap.
func (p *AssetPreferences) SetClassicSlot(name string, frac [4]float64) {
	if name == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetClassicSlot(name, frac)
	})
}

// ClearClassicSlot drops one slot's override (name "" clears every slot, the
// editor's "Reset all"). The slot's window anchor goes with it — an anchor
// without an override is meaningless (it only re-bases the override).
func (p *AssetPreferences) ClearClassicSlot(name string) {
	p.Update(func(sn *Settings) {
		sn.ClearClassicSlot(name)
	})
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

// SetClassicAnchor stores one slot's window pin (rejects malformed modes /
// window sizes; bounded by classicSlotCap alongside the overrides).
func (p *AssetPreferences) SetClassicAnchor(name string, a ClassicAnchor) {
	if name == "" || !validAnchorMode(a.Mode) || a.WinW <= 0 || a.WinH <= 0 {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetClassicAnchor(name, a)
	})
}

// ClearClassicAnchor drops one slot's window pin (the override stays; the
// slot goes back to plain fraction scaling).
func (p *AssetPreferences) ClearClassicAnchor(name string) {
	p.Update(func(sn *Settings) {
		sn.ClearClassicAnchor(name)
	})
}

// Layout-editor snap grid (playtest request, Tifera: a user-editable snap
// grid). The step is clamped to [MinLayoutGridPx, MaxLayoutGridPx]; the
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
func (s *Settings) LayoutGridSize() int {
	return orDefault(s.LayoutGridPx, DefaultLayoutGridPx)
}

// SetClassicLayout REPLACES every default-courtroom slot override at once — the
// classic editor's undo/redo restores a whole snapshot, so it can't go slot-by-slot.
// The map is copied (never aliased), empty keys skipped, and bounded by classicSlotCap;
// a nil/empty map clears all overrides.
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

// SetClassicAnchors REPLACES every slot's window pin at once (symmetric to
// SetClassicLayout) — applyProfile restores a whole anchor snapshot, so it can't
// go slot-by-slot. The map is sanitized (bad mode / non-positive window dropped)
// and copied (never aliased); a nil/empty map clears all pins. Unlike
// ClearClassicSlot this does NOT touch ClassicLayout — a profile carries both
// axes independently, and the caller sets them together.
func (p *AssetPreferences) SetClassicAnchors(m map[string]ClassicAnchor) {
	clean := sanitizeClassicAnchors(m) // drops junk + bounds at classicSlotCap
	p.Update(func(sn *Settings) {
		sn.ClassicAnchors = clean
	})
}

// ClassicRotationSnapshot returns a copy of the per-slot rotation angles (A4) —
// the classic editor snapshots it once into an App-local map (beside the anchor
// snapshot) so the render path resolves angles lock-free.
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

// SetClassicRotation stores one slot's rotation angle byte (bounded by
// classicSlotCap alongside the overrides). A zero angle is stored like any
// other; the draw path treats an ABSENT entry as angle 0, so a caller that wants
// "no rotation" should ClearClassicRotation rather than set 0.
func (p *AssetPreferences) SetClassicRotation(name string, deg uint8) {
	if name == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetClassicRotation(name, deg)
	})
}

// ClearClassicRotation drops one slot's rotation (the override stays; the slot
// draws unrotated).
func (p *AssetPreferences) ClearClassicRotation(name string) {
	p.Update(func(sn *Settings) {
		sn.ClearClassicRotation(name)
	})
}

// SetClassicRotations REPLACES every slot's rotation at once (symmetric to
// SetClassicAnchors) — applyProfile restores a whole rotation snapshot. The map
// is sanitized (blank keys dropped, bounded by classicSlotCap) and copied (never
// aliased); a nil/empty map clears all rotations.
func (p *AssetPreferences) SetClassicRotations(m map[string]uint8) {
	clean := sanitizeClassicRotations(m) // drops blanks + bounds at classicSlotCap
	p.Update(func(sn *Settings) {
		sn.ClassicRotations = clean
	})
}

// Macros returns a copy of the user macro list.
func (s *Settings) Macros() []MacroSpec {

	out := make([]MacroSpec, len(s.UserMacros))
	for i := range s.UserMacros {
		out[i] = snapCloneMacroSpec(s.UserMacros[i])
	}
	return out
}

// SetMacros replaces the macro list (caps enforced).
func (p *AssetPreferences) SetMacros(in []MacroSpec) {
	clean := sanitizeMacros(in)
	p.Update(func(sn *Settings) {
		sn.UserMacros = clean
	})
}

// ThemeFitDefaultJustMoved reports whether THIS load performed the one-shot
// Stretch→Native theme-fit move: the stamp was absent (so this is the first run
// after updating for this file) AND the file was still on the old Stretch
// default, so the mode actually changed under the user.
// It is the upgrade signal for the #35 window snap — the caller uses it to arm
// exactly one automatic resize to the theme's design size, so an existing user
// does not go from an edge-to-edge courtroom to a small canvas ringed by bars
// with no way back except the settings screen. Persisting the stamp is what
// makes it fire once and only once: the next launch finds the stamp and this
// reports false forever after. That is why the stamp is flushed SYNCHRONOUSLY on
// the migrating launch (newWithDebounce) rather than left to the debounced saver
// — a stamp that can be lost to a crash inside the debounce window would only
// buy "once per persisted stamp", and the move would re-run and re-snap.
// Pure read (no consume), so it is safe to call from anywhere; the caller owns
// the "act on it once" decision. A file that kept a deliberate Letterbox/Crop/
// Custom pick reports FALSE — its look did not change, so nothing should move.
func (p *AssetPreferences) ThemeFitDefaultJustMoved() bool {

	return p.themeFitMovedToNative
}

// SetThemeFit sets the theme-fit mode (clamped to a valid mode).
func (p *AssetPreferences) SetThemeFit(mode int) {
	mode = value.Clamp(mode, ThemeFitStretch, themeFitModeMax)
	p.Update(func(sn *Settings) {
		sn.ThemeFit = mode
	})
}

// ThemeZoom reports the Custom-mode zoom (percent of the letterbox-fit scale;
// 100 = exactly fits), clamped.
func (s *Settings) ThemeZoom() int {
	return value.Clamp(orDefault(s.ThemeFitZoom, DefaultThemeZoom), MinThemeZoom, MaxThemeZoom)
}

// ThemePan reports the Custom-mode pan (percent of the window, ±).
func (s *Settings) ThemePan() (x, y int) {

	return s.ThemeFitPanX, s.ThemeFitPanY
}

// SetThemeFitPan clamps and stores the Custom-mode pan.
func (p *AssetPreferences) SetThemeFitPan(x, y int) {
	x = value.Clamp(x, -MaxThemePan, MaxThemePan)
	y = value.Clamp(y, -MaxThemePan, MaxThemePan)
	p.Update(func(sn *Settings) {
		sn.ThemeFitPanX = x
		sn.ThemeFitPanY = y
	})
}

// ExportLearnedJSON snapshots the learned-format table as indented JSON —
// one player's warm state can seed another's (Settings → Export).
func (p *AssetPreferences) ExportLearnedJSON() ([]byte, error) {
	return json.MarshalIndent(p.Load().LearnedSnapshot(), "", jsonMarshalIndent)
}

// ImportLearnedJSON merges a learned-format export into this table
// (imported entries win) and reports how many entries landed.
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

// LearnedSnapshot returns a deep copy of the learned-format table, used to
// warm the resolver's atomic snapshot at startup and on server connect.
func (s *Settings) LearnedSnapshot() map[string][]string {

	out := make(map[string][]string, len(s.LearnedFormats))
	for k, v := range s.LearnedFormats {
		out[k] = cloneStrings(v)
	}
	return out
}

// ClearLearned wipes every learned format ("Clear Learned Formats" button).
func (p *AssetPreferences) ClearLearned() {
	p.Update(func(sn *Settings) {
		sn.LearnedFormats = map[string][]string{}
	})
}

// dropLearnedType removes learned entries for one type across all hosts from a
// snapshot — the shared path for SetFormatOrder/SetTypeFallbacks/ClearLearnedType.
func dropLearnedType(sn *Settings, typeName string) {
	suffix := LearnedKeySeparator + typeName
	for key := range sn.LearnedFormats {
		if strings.HasSuffix(key, suffix) {
			delete(sn.LearnedFormats, key)
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
	snap := p.Load()

	out := make([]string, len(snap.LocalAssetsPaths))
	copy(out, snap.LocalAssetsPaths)
	return snap.LocalAssetsEnabled, out
}

// SetLocalAssets toggles no-streaming mode and stores the ordered mount
// folder list. It returns false — changing nothing — when the call would GROW
// the list past LocalMountCap.
// Refusing only GROWTH is deliberate. A flat `len(mounts) > LocalMountCap`
// rejection would brick any user who already has more mounts saved than the cap:
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

// LayeredAssets reports the mode where local mounts are read ALONGSIDE
// the server's stream, with the user's own files winning at the same path.
func (p *AssetPreferences) LayeredAssets() (layered bool, mounts []string) {
	snap := p.Load()

	out := make([]string, len(snap.LocalAssetsPaths))
	copy(out, snap.LocalAssetsPaths)
	return snap.LocalAssetsLayered && !snap.LocalAssetsEnabled && len(out) > 0, out
}

// SetLayeredAssets turns layering on or off. Selecting it clears the exclusive
// no-streaming flag (and vice versa in SetLocalAssets' callers), so the Settings
// radio's three states map onto the two bools without ever persisting the
// ambiguous both-set combination.
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

// BaseMountsEqual reports whether want is exactly the mount list the asset
// pipeline is reading right now — the SAME list LocalAssets/LayeredAssets would
// hand back, in the same order — without copying it out.
// It exists because both accessors allocate: each takes `make([]string, n)` +
// copy under the lock, which is correct for a caller that keeps the slice but is
// pure waste for a caller that only wants to know "did this change since last
// time?". That caller (internal/ui's local character-folder scan) asks once per
// FRAME while its tab is open, so with mounts configured the copy was one or two
// heap allocations per frame for an answer that is a comparison. Comparing under
// the read lock costs nothing and never escapes the stored slice.
// The mode logic mirrors ui's baseMounts and is pinned by a parity test there:
func (p *AssetPreferences) BaseMountsEqual(want []string) bool {
	snap := p.Load()

	if !snap.LocalAssetsEnabled && !(snap.LocalAssetsLayered && len(snap.LocalAssetsPaths) > 0) {
		return len(want) == 0
	}
	return slices.Equal(snap.LocalAssetsPaths, want)
}

// --- Favorites -----------------------------------------------------------------

// FavoriteServers returns the starred server list, in pin order.
func (s *Settings) FavoriteServers() []FavoriteServer {

	out := make([]FavoriteServer, len(s.Favorites))
	copy(out, s.Favorites)
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
	p.Update(func(sn *Settings) {
		sn.AddFavorite(name, url, description)
	})
}

// UpdateFavorite renames and/or re-addresses an existing phone-book entry,
// preserving its position in the list. Returns false if oldURL isn't found, or
// if newURL differs from oldURL and already belongs to ANOTHER favorite
// (rejected to avoid a silent, surprising merge onto that target). A no-op edit
// (nothing actually changed) returns true — the caller treats false purely as
// an error signal, so an unchanged Save must not report failure. markDirty()
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

// RemoveFavorite unstars the server with the given URL.
func (p *AssetPreferences) RemoveFavorite(url string) {
	p.Update(func(sn *Settings) {
		sn.RemoveFavorite(url)
	})
}

// ExportFavoritesJSON marshals the phone book (favorites) as shareable JSON.
func (p *AssetPreferences) ExportFavoritesJSON() ([]byte, error) {
	return json.MarshalIndent(p.Load().FavoriteServers(), jsonMarshalPrefix, jsonMarshalIndent)
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
		if strings.TrimSpace(f.URL) == "" || p.Load().IsFavorite(f.URL) {
			continue
		}
		p.AddFavorite(f.Name, f.URL, f.Description)
		added++
	}
	return added, nil
}

// IsFavorite reports whether the URL is starred.
func (s *Settings) IsFavorite(url string) bool {

	for _, f := range s.Favorites {
		if f.URL == url {
			return true
		}
	}
	return false
}

// --- Showname ----------------------------------------------------------------

// ShownameCap bounds the saved showname presets (M6).
const ShownameCap = 64

// ShownameList returns a copy of the saved showname presets (global, persisted —
// cleared only by a full factory reset).
func (s *Settings) ShownameList() []string {

	if len(s.ShownamePresets) == 0 {
		return nil
	}
	out := make([]string, len(s.ShownamePresets))
	copy(out, s.ShownamePresets)
	return out
}

// ShownameCount is the number of saved presets — a cheap, alloc-free length read
// so the courtroom name picker caches its option list and rebuilds only when the
// set changes (vs calling ShownameList, which copies, every frame).
func (s *Settings) ShownameCount() int {

	return len(s.ShownamePresets)
}

// AddShownamePreset appends a showname to the saved presets (trimmed, deduped
// case-insensitively, bounded). Reports whether it changed.
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

// RemoveShownamePreset drops a saved showname. Reports whether it changed.
func (p *AssetPreferences) RemoveShownamePreset(name string) bool {
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveShownamePreset(name)
	})
	return changed
}

// shownameKeyCap bounds the global per-key showname binds (hard rule #4: no
// unbounded maps).
const shownameKeyCap = 64

// ShownameKeyBinds returns a copy of the global key → showname bind map (M6:
// press a bound key in the courtroom to swap to that showname). Nil = none.
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

// SetShownameKeyBind binds key (lowercase SDL key name) to a showname; an empty
// showname clears the binding. Global + persisted; bounded by shownameKeyCap.
func (p *AssetPreferences) SetShownameKeyBind(key, showname string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return
	}
	p.Update(func(sn *Settings) {
		sn.SetShownameKeyBind(key, showname)
	})
}

// icPhraseKeyCap bounds the hotkeyed IC quick-phrases (hard rule #4: no unbounded maps).
const icPhraseKeyCap = 64

// ICPhraseBinds returns a copy of the key → IC-phrase map: press a bound key in
// the courtroom to send that line as your character in IC. Nil = none.
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

// SetICPhraseKey binds key (lowercase SDL key name) to a canned IC line; an empty
// phrase clears the binding. Global + persisted; bounded by icPhraseKeyCap.
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

// mutedSFXCap bounds the per-SFX mute list (hard rule #4: no unbounded slices).
const mutedSFXCap = 128

// IsSFXMuted reports whether an emote SFX name is muted (M11, case-insensitive).
// Called from the courtroom audio path once per played SFX (not per frame).
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

// MutedSFXList returns a copy of the muted SFX names (global, persisted).
func (s *Settings) MutedSFXList() []string {

	if len(s.MutedSFX) == 0 {
		return nil
	}
	out := make([]string, len(s.MutedSFX))
	copy(out, s.MutedSFX)
	return out
}

// MuteSFX adds an SFX name to the mute list (lowercased, deduped, bounded).
// Reports whether it changed.
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

// UnmuteSFX removes an SFX name from the mute list. Reports whether it changed.
func (p *AssetPreferences) UnmuteSFX(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.UnmuteSFX(name)
	})
	return changed
}

// sfxFavoritesCap bounds the starred-SFX list (hard rule #4: no unbounded
// lists). A generous ceiling — nobody favourites hundreds of sounds.
const sfxFavoritesCap = 128

// SfxFavoritesList returns a copy of the starred SFX names (#12, global, persisted,
// lowercased bare names resolved per-server by urls.SFX).
func (s *Settings) SfxFavoritesList() []string {

	if len(s.SfxFavorites) == 0 {
		return nil
	}
	out := make([]string, len(s.SfxFavorites))
	copy(out, s.SfxFavorites)
	return out
}

// IsSfxFavorite reports whether a sound name is starred (case-insensitive).
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

// ToggleSfxFavorite stars an SFX name if absent (lowercased, deduped, bounded) or unstars it if
// present. Reports the new state (true = now starred). A no-op (and false) when at the cap adding.
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
func (s *Settings) ModReasonTemplatesList() []string {

	if len(s.ModReasonTemplates) == 0 {
		return cloneStrings(defaultModReasonTemplates)
	}
	return cloneStrings(s.ModReasonTemplates)
}

// seedModTemplates populates the stored list with the defaults if it's empty, so a
// mutation (add/remove) starts from what the user sees rather than from nothing.
func seedModTemplates(sn *Settings) {
	if len(sn.ModReasonTemplates) == 0 {
		sn.ModReasonTemplates = cloneStrings(defaultModReasonTemplates)
	}
}

// AddModReasonTemplate appends a reason (trimmed, deduped case-insensitively, bounded). Reports
// whether it changed. Case is preserved — these are display labels, not match keys.
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

// modDurationsCap bounds the saved custom ban-duration chips (hard rule #4). The
// enum presets already cover the common spans, so 16 customs is plenty.
const modDurationsCap = 16

// ModDurationsList returns the saved custom ban-duration chips (a clone) — canonical
// short tokens ("45m", "2d"). Empty by default: the BanDuration enum presets are the
// built-ins, customs only add to them.
func (s *Settings) ModDurationsList() []string {

	return cloneStrings(s.ModDurations)
}

// AddModDuration appends a custom duration chip (deduped, bounded). The caller
// canonicalizes first (courtroom.CanonicalBanDuration) — config stays courtroom-free,
// so it stores what it's given, trimmed. Reports whether it changed.
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

// RemoveModDuration drops a custom duration chip. Reports whether it changed.
func (p *AssetPreferences) RemoveModDuration(token string) bool {
	token = strings.TrimSpace(token)
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveModDuration(token)
	})
	return changed
}

// RemoveModReasonTemplate drops a reason (case-insensitive). Reports whether it changed.
func (p *AssetPreferences) RemoveModReasonTemplate(s string) bool {
	s = strings.TrimSpace(s)
	changed := false
	p.Update(func(sn *Settings) {
		changed = sn.RemoveModReasonTemplate(s)
	})
	return changed
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

// BlipVolumes returns a copy of the per-character blip-volume overrides
// (lowercased char → scale), for the settings UI. Nil when none are set.
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

// SetBlipVolume stores a character's per-character blip scale (clamped 0–100).
// Setting the default (100) clears the override so the map only holds genuine
// adjustments (and stays well under blipVolsCap). Reports whether it changed.
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

// IsEmoteFav reports whether emote index idx is favourited for char.
func (s *Settings) IsEmoteFav(char string, idx int) bool {

	char = strings.ToLower(strings.TrimSpace(char))
	if char == "" || idx < 0 {
		return false
	}

	return containsInt(s.EmoteFavs[char], idx)
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
	nowFav := false
	p.Update(func(sn *Settings) {
		nowFav = sn.ToggleEmoteFav(char, idx)
	})
	return nowFav
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
