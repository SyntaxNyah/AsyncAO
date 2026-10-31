package ui

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/logbrowser"
	"github.com/SyntaxNyah/AsyncAO/internal/metrics"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/platform/host"
	"github.com/SyntaxNyah/AsyncAO/internal/presence"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

// Screen identifies the active top-level view.
type Screen int

const (
	ScreenLobby Screen = iota
	ScreenCharSelect
	ScreenCourtroom
	ScreenSettings
	ScreenAbout
	// ScreenServerHelp is the upgrade guide for owners of legacy servers.
	ScreenServerHelp
	// ScreenChangelog renders the embedded CHANGELOG.md.
	ScreenChangelog
	// ScreenLogs is the transcript browser over logs/<server>/*.log written by detailed logging.
	ScreenLogs
	// ScreenHelp is the newcomer glossary and privacy explainer.
	ScreenHelp
	// ScreenThemeEditor is the live courtroom with two floating rails. Screen is never persisted, so appending is safe.
	ScreenThemeEditor
)

const (
	lobbyRefreshTimeout = 15 * time.Second
	// lobbyAutoRefreshMinInterval limits auto-refresh on entering the lobby; the Refresh button bypasses it.
	lobbyAutoRefreshMinInterval = time.Minute
	logLines                    = 8
	logLineMax                  = 120 // single-line UI (toasts/warnings) — NOT the IC log
	// icLineCap bounds one stored IC entry against hostile servers; long lines wrap at draw time.
	icLineCap = 1024
	// icLogCap sizes the IC scrollback.
	icLogCap = 1024

	// areaLogCacheMax bounds how many visited areas keep their own IC scrollback; oldest evicted first.
	areaLogCacheMax = 64

	// charIconWarmup caps the connect-time icon burst: servers with 4000+ characters would flood the low lane.
	// Past it, drawCharCell fetches only what is on screen.
	charIconWarmup = 128
	// charIconAskPerFrame bounds grid requests per frame; the render thread must not flood or block on the pool.
	charIconAskPerFrame = 32
	// charIconRetryInterval spaces re-requests for a visible icon: the pool never re-runs shed low-lane jobs.
	charIconRetryInterval = 2 * time.Second

	// warnShowDuration is how long a transient toast stays up.
	warnShowDuration = 12 * time.Second

	// iniswapFileName is the server's custom character list at the asset origin root, one folder name per line.
	// Listed characters stream like normal ones but never occupy a server character.
	iniswapFileName = "iniswap.txt"
	// iniswapListCap bounds the parsed list against a hostile file.
	iniswapListCap = 4096
	// iniswapFetchTimeout caps the download.
	iniswapFetchTimeout = 15 * time.Second

	// restoreDialTimeout bounds each restore-on-launch reconnect so a dead server can't freeze boot.
	restoreDialTimeout = 4 * time.Second

	// maxDescLines caps the expanded lobby description box.
	maxDescLines = 6
	// spriteOvCap bounds the client-side sprite override table.
	spriteOvCap = 128

	// debugLogCap bounds the in-app failure log; the oldest lines fall off.
	debugLogCap = 120
	// debugOverlayLines is how many of the newest log lines the overlay draws.
	debugOverlayLines = 10
	// themeHealPeriod paces reloading the theme chatbox after T1 evicted it, one request per period.
	themeHealPeriod = 2 * time.Second
)

// Log panel tabs (courtroom right column).
const (
	logTabLog = iota
	logTabMusic
	logTabAreas
	logTabPlayers
	logTabOOC
	logTabNotes
	logTabFriends
)

// Char select grid tabs.
const (
	charTabServer = iota
	charTabWardrobe
)

// Deps wires the app to the engine singletons built in main.
type Deps struct {
	Prefs    *config.AssetPreferences
	Resolver *assets.Resolver
	Manager  *assets.Manager
	Pool     *network.Pool
	Client   *network.Client
	Store    *render.TextureStore
	Viewport *render.Viewport
	Pump     *render.Pump
	Audio    *render.Audio
	// AudioDevices opens the voice streams and lists microphones.
	AudioDevices host.AudioDevices
	// Capabilities says what this platform supports; the zero value supports everything.
	Capabilities platform.Capabilities
	// Desktop opens web addresses and files in the programs the user chose.
	Desktop host.Desktop
	// GraphicsName says what is drawing, for the perf HUD and the About screen.
	GraphicsName string
	// Preview is the detachable emote-preview OS window; consumers must stay nil-safe for headless tests.
	Preview *render.PreviewWindow
	// Presence is the optional Discord Rich Presence client; nil in tests.
	Presence *presence.Client
	// Profiler is the 1 Hz sampler the F3 perf HUD reads; nil in tests.
	Profiler *metrics.Profiler
	// MasterURL is the server-list endpoint.
	MasterURL string
	// RestoreTabs reopens the tabs saved at last shutdown even with restore-on-launch off (relaunch after GPU device
	// loss).
	RestoreTabs bool
	// ConfigQuarantine is non-nil when a corrupt preferences file was renamed aside; NewApp raises a one-time notice.
	ConfigQuarantine *config.Quarantine
	// AutoMounts are the auto-discovered packages/ folders, appended after the user's manual mounts.
	AutoMounts []string
}

// App is the whole client UI state machine. Render thread only.
type App struct {
	d   Deps
	ctx *Ctx

	screen     Screen
	prevScreen Screen // for settings/about back navigation

	// te is the theme editor, nil while shut so it costs nothing when closed.
	te *themeEditor

	// Connection lifecycle: these must stay on App, not sessionState. They outlive a session, and auto-reconnect
	// retries across the resetSessionState that Disconnect runs, which would wipe them.
	connErr string // last lobby error/notice (disconnect reason, dial failure, …)
	// connErrLine is the drawable one-line connErr, memoized against connErrSrc because building it allocates
	// and the lobby reads it every frame.
	connErrLine  string
	connErrSrc   string
	lastConnName string // M2: the server we were dropped from, for one-click Reconnect
	lastConnURL  string // its ws URL (serverKey); set on connect, re-captured at Disconnect
	// After an unexpected drop, retry lastConnURL with backoff. autoReconnectAt is the next attempt (zero = not
	// retrying); autoReconnectMsg is the cached lobby status, rebuilt per attempt.
	autoReconnectAt    time.Time
	autoReconnectTries int
	autoReconnectMsg   string
	// deliberateClose marks the current teardown as user-initiated so the drop paths suppress auto-reconnect;
	// cleared on a fresh connect.
	deliberateClose bool
	// pendingSessionCarry is the log content captured at a teardown that expects to return, reapplied by connectWith
	// only on the same server. It must outlive resetSessionState, so it lives here.
	// pendingSessionCarryKey is the dialled serverKey it came from; nil and "" together mean nothing pending.
	pendingSessionCarry    *sessionCarry
	pendingSessionCarryKey string

	// --- lobby state ---
	masterEntries []network.ServerEntry // raw master list (no favorites)
	servers       []network.ServerEntry
	lobbyStatus   string
	lobbyFetching bool
	lobbyResult   chan lobbyFetch
	// lastLobbyRefreshAt is when the last master-list fetch started, stamped in RefreshServers past its dedup check.
	lastLobbyRefreshAt time.Time
	directInput        string
	directSecure       bool
	directSave         bool
	lobbyScroll        int32
	// The phone book is a lobby page over the saved Favorites servers, with its own export and import.
	phoneBookPage bool
	pbName, pbURL string // phone-book "add server" form
	// pbEditURL is the stored URL of the row open for editing (empty = none); the buffers hold edits until Save.
	pbEditURL, pbEditName, pbEditAddr string
	// "Phone Fanat" chip on the phone-book page: phoneFanatLine lingers for fanatShowDuration after phoneFanatShownAt.
	phoneFanatLine    string
	phoneFanatShownAt time.Time
	// First click opens the full description under the row; a second click on the same row joins.
	selServer  int
	descLines  []string
	descLinks  []string // clickable URLs found in the description
	helpScroll int32    // ScreenServerHelp scroll position
	// Ping sort: pings holds each server's TCP-connect RTT by URL (0 = unprobed, <0 = unreachable);
	// pingGen drops stale results after a re-probe.
	pings    map[string]time.Duration
	pingRes  chan pingResult
	pingMode bool
	pinging  bool
	pingGen  int

	// Connection-quality chip: one goroutine pings the active conn, and exists only while the chip is on.
	// pingRTT is the only field it touches; the rest are render-thread-only.
	pingRTT     atomic.Int64       // round-trip nanoseconds (0 = unknown); goroutine→render
	pingConn    *protocol.Conn     // the conn the live loop targets (nil = no loop)
	pingCancel  context.CancelFunc // stops the live loop
	pingLabel   string             // cached tooltip text, rebuilt only when the ms bucket changes
	pingLabelMs int                // the ms the cached label was built for (-1 = none)

	// --- the active server session (every per-server field) ---
	// sessionState is embedded so the whole session parks and restores as one struct move on tab switch.
	sessionState

	// --- global per-frame budgets / transient UI not tied to a server ---
	iconAskBudget int // per-frame icon demand allowance, reset each Frame
	// frameNow is this frame's clock snapshot, read by animation clocks instead of the OS clock per draw site.
	frameNow time.Time
	// musicTabDucked silences the one music stream while it belongs to a background tab, keeping its position.
	// It says whether the active tab owns the stream. Suppressed when the MusicAcrossTabs pref is on.
	musicTabDucked bool
	// musicOwnerKey is the serverKey of the tab whose track is in the stream. applyAudioVolumes must scale by the
	// owner's music volume, or an active tab with music at 0 would silence the owner's continuing stream.
	// "" falls back to the active tab; a stale owner after a stop is harmless, the next play stamps it again.
	musicOwnerKey string
	// musicAwaitURL is the URL the active tab requested while the stream is still ducked: unducking before the mixer
	// plays this URL would make the old track audible during the download. "" = nothing awaited.
	// Invariant: stopped means not ducked and no await, so a stale await can't unduck a later foreign track.
	musicAwaitURL string
	// musicAwaitSince stamps musicAwaitURL at both places that set it. A silently failed fetch fires no event, so
	// settleAwaitedMusic unducks anyway past musicAwaitTimeout rather than stay silent forever.
	musicAwaitSince time.Time
	// defaultOOC is the AsyncAO<n> fallback OOC name, minted once per run on first use.
	defaultOOC string
	// nameOpts caches the name-picker dropdown options; nameOptsLen is the preset count it was built at.
	nameOpts    []string
	nameOptsLen int
	// macroBind is >= 0 while the settings macro editor captures a key.
	macroBind int
	// holdKeyArmed means the hold-to-clear rebind is capturing the next key.
	holdKeyArmed bool
	// voicePTTBindArmed means the push-to-talk rebind is capturing the next key.
	voicePTTBindArmed bool
	// F3 perf HUD: toggle plus the frame-time ring it graphs.
	perfHUD     bool
	frameDts    [perfHUDFrames]float32 // milliseconds
	frameDtIdx  int
	previewBase string
	// previewName is the display name of the emote behind previewBase, stamped by previewEmote so the caption never
	// scans a.emotes per frame. previewNameFor is that base: consumers return the name only while it equals
	// previewBase, so every other previewBase setter invalidates the caption automatically.
	previewName    string
	previewNameFor string
	previewFor     string    // base the preview clock was started for
	previewAt      time.Time // loop anchor — animated previews play, not freeze
	previewZoom    int       // magnifier factor (1 = fit; >1 shows a cursor-panned window)
	// Cycle a previewed, non-worn wardrobe character's emotes: previewChar guards a one-shot char.ini parse;
	// the capped slices drive the buttons and arrow keys.
	previewChar     string
	previewAnims    []string
	previewLabels   []string
	previewEmoteIdx int
	// previewEmoteCycle says who asked for the emote list. True (wardrobe navigator): jump to emote 0 so the caption
	// matches the sprite. False (char-select portrait): keep the "normal" probe and repoint only when the char.ini
	// has no emote "normal" (correctPortraitPreview).
	previewEmoteCycle bool

	// Async result channels: payloads carry the serverKey they were fetched for and polls drop mismatches,
	// so a tab switch can't land another server's data.
	charINIres chan charINIFetch
	// previewEmoteRes carries a previewed character's parsed emote list, guarded by key and char.
	previewEmoteRes chan previewEmoteFetch
	// logViewEpoch identifies the session promoted into App and is part of every log cache key: icLogSeq and oocSeq
	// start at 0 in every tab, so tabs would collide on the shared caches. It bumps on every session swap and is
	// stable within a tab, so steady-frame draws stay 0-alloc. Never key a log cache by seq alone.
	logViewEpoch uint64
	// icLogFiltered cache: rebuilt only when the log or query moved.
	icFilter      []int
	icFilterSeq   uint64
	icFilterEpoch uint64
	icFilterQuery string
	// Advanced filters are part of the key, so editing them rebuilds without touching the log.
	icFilterShowname string
	icFilterPos      string
	// icFilterColor is the colour filter in the cache key.
	icFilterColor int
	// IC wrapped-rows cache: rebuilt only when the log, query, width or font scale moved.
	icWrap         []icWrapLine
	icWrapSeq      uint64
	icWrapEpoch    uint64
	icWrapQuery    string
	icWrapShowname string
	icWrapPos      string
	icWrapColor    int // colour filter baked into the wrapped-rows cache key (#49)
	icWrapW        int32
	icWrapPct      int
	icWrapGen      int  // font chain generation baked into the wrap
	icWrapStamp    bool // ICTimestamps state baked into the wrap (toggling rewraps)
	// OOC wrapped-lines cache, also keyed by logViewEpoch: rebuilt only when the log, width or font scale moved.
	oocWrap     []string
	oocWrapName []string // parallel to oocWrap: speaker to tint on each entry's FIRST display line ("" otherwise)
	oocWrapCont []bool   // parallel to oocWrap: true = a wrap continuation row (hanging indent); a paragraph's own newline starts a fresh row, not a continuation
	oocWrapSrc  []int    // parallel to oocWrap: the source oocLog entry index of each display line (mirrors icWrapLine.entry)
	// Every URL in a paragraph is separately hoverable. oocWrapLink is the flat table in row order;
	// oocWrapLinkAt has len(rows)+1 entries, so row r's spans are oocWrapLink[oocWrapLinkAt[r]:oocWrapLinkAt[r+1]].
	oocWrapLink   []oocLinkSpan
	oocWrapLinkAt []int32
	oocWrapSeq    uint64
	oocWrapEpoch  uint64
	oocWrapW      int32
	oocWrapPct    int
	oocWrapMask   bool // streamer-mode masking baked into the cache
	oocWrapGen    int  // font chain generation baked into the wrap

	// warnLine is the last transient toast; missing-asset reports use the menu-bar chip instead.
	warnLine string
	warnAt   time.Time

	// Missing-assets chip: the ring suppresses bursts; label and tip are memoized so its draw allocates nothing.
	assetMissSeen   assetMissRing
	assetMissCount  int
	assetMissAt     time.Time
	assetMissLabel  string
	assetMissLabelN int // count the memoized label was built for
	assetMissTip    string

	// dndOn silences personal pings (callword, friend) but not duty signals. Session-only, so it can't silently
	// kill callwords days later.
	dndOn   bool
	loginAt time.Time // when a login flow last fired; a brief callword grace so the server's login replies don't self-ping

	// Debug overlay: deliberately client-global, unlike the per-tab transcripts. Most entries have no tab, and
	// splitting it would hide a background tab's drop. Memory-only.
	debugLog   []debugEntry // ring of stamped failure rows, debugLogCap max (see pushDebugAt)
	lastPktHdr string       // newest server packet header (health line)
	lastPktAt  time.Time

	// Packet inspector: the Debug panel's live packet ring and counts.
	pkts    packetLog      // bounded ring + per-header in/out counts (active conn)
	pktConn *protocol.Conn // the conn pkts currently reflects; a change resets it

	// Server format manifest (extensions.json autodetect).
	manifestRes chan manifestFetch

	// Asset-casing auto-detect, off unless AssetCharCasing == CharCaseAuto.
	charCaseLearned  map[string]uint8 // host → learned character-folder casing
	casingProbing    bool             // a casing probe is in flight
	casingProbedHost string           // host already probed this session (probe once per host)
	casingRes        chan casingProbeResult

	// Local-pack layer (mountlayer.go).
	mountIndex     *assets.MountIndex // the live index (nil = none built); its own Mounts() is the identity, so no separate key can drift
	mountIndexing  bool               // a walk is in flight (single-flight latch)
	mountIndexRes  chan mountIndexResult
	mountIndexErrs []error // per-mount build failures, for the Settings status line

	// Font override pipeline; file bytes are read off-thread.
	fontRes chan fontLoad
	// themeFontFile is the active theme's bundled font path, used when no manual font or dyslexia toggle is set.
	themeFontFile string
	// Settings diagnostics for per-panel fonts: the file each element resolved to, and families that resolved to
	// nothing. Set on theme apply, never read on a draw path.
	themeFaceNames   []string
	panelFontMissing []string
	// themeFontWarn is the sentence about the theme's unresolved families, kept for Settings after the toast fades.
	themeFontWarn string
	// fontWarnDlg is the missing-fonts modal, on App because a theme is global to the client, not to one tab.
	fontWarnDlg fontWarnDialog
	// serverNoticeDlg shows a kick or ban reason, lockdown message or BB popup. It is on App because
	// resetSessionState would erase a removal reason before the lobby ever drew.
	serverNoticeDlg serverNoticeDialog
	// chromeMeasure is the shared modal-wrap metric, built once because a closure allocates.
	chromeMeasure func(string) int32
	// panelSizeEdit is the raw text of each per-panel size box while typing, keyed by AO2 element id. A half-typed
	// number is not a size: clamping it mid-keystroke made values below ten impossible to enter.
	panelSizeEdit map[string]string
	// Color-emoji fallback: the system emoji font is read off-thread the first time a message needs it,
	// since the file is large and many users never type emoji.
	emojiFontRes     chan []byte
	emojiLoadStarted bool
	// Broad-Unicode text fallback: system fonts covering scripts the embedded font lacks. Read off-thread on the first
	// non-ASCII rune, so a pure-ASCII session never loads them and the single-font path stays untouched.
	fallbackFontRes     chan [][]byte
	fallbackLoadStarted bool
	// CJK fonts are 13-35 MB, read only when a CJK rune is drawn; a separate flag so the broad load can't consume it.
	cjkFontRes     chan [][]byte
	cjkLoadStarted bool
	// System-font scan, the last resort for a rune nothing in the curated chain covers: the render thread sends it on
	// censusReq and installs the face from censusRes. The scanning goroutine starts on the first miss.
	// missingBuf is the reusable drain buffer; the drain runs every frame and must not allocate.
	censusReq     chan rune
	censusRes     chan []byte
	censusStarted bool
	missingBuf    []rune

	// Custom missing-image placeholder: read and decoded off-thread (hard rule 2), landing on errorSpriteRes.
	// errorSpriteErr is the inline Settings error after a failed load.
	errorSpriteRes chan errorSpriteLoad
	errorSpriteErr string

	// Log browser over saved transcripts; global, not per-session, loaded off-thread.
	logBrowser    logBrowserState
	logBrowserRes chan logbrowser.Result

	// Case notebook loads, per-server; the payload routes by key.
	notebookRes chan notebookLoad

	// Offset ghost editor (pair panel) drag state.
	ghostDrag  bool
	ghostPrev  bool // last frame's mouseDown (edge detect)
	ghostStart [2]int32
	ghostBase  [2]int

	// Multi-server tabs: tabs holds parked sessions, the active one lives in sessionState. activeTab -1 = lobby.
	tabs      []*courtTab
	activeTab int
	// tabColors holds each tab chip's palette index by serverKey, so colours survive switching. 0 = no tint.
	// Created lazily, bounded by tabColorsCap.
	tabColors map[string]int
	// Tab drag-reorder: tabDragFrom is the chip pressed (-1 = none); tabDragging flips past tabDragThreshold, then a
	// release reorders. tabPrevDown is separate from the wardrobe's tracker because the strip draws over every screen.
	tabDragFrom  int
	tabDragStart [2]int32
	tabDragging  bool
	tabPrevDown  bool
	// restoreQueue holds the tabs to reopen on launch, drained one reconnect per frame by pumpTabRestore
	// so blocking dials never pile into one boot freeze.
	restoreQueue []config.OpenTab
	// translogs are the transcript writers, one per tab session, keyed by server and logSession; keying by server alone
	// would interleave two tabs into one file. reapTranscripts deletes an entry once its session is gone, because
	// holding them to shutdown leaks a file and goroutine per reconnect. Bounded by transcriptWriterCap.
	translogs map[translogKey]*transcriptWriter
	// nextLogSession mints sessionState.logSession. Ids are never reused: a new tab could otherwise adopt a dead tab's
	// open writer and append to its file.
	nextLogSession int
	// spriteCapBase is the display-derived decode-downscale base height, on App because every connect rebuilds
	// sessionState.
	spriteCapBase int
	// partColors and partColorOn are the parsed per-part layout tints, on App like spriteCapBase.
	partColors  [config.LayoutPartColorCount]sdl.Color
	partColorOn [config.LayoutPartColorCount]bool

	// Self-update: the probe fires once on the first frame, off the boot path; a newer release lands on updateRes,
	// is stored in updateRel and opens the What's New modal once.
	updateRes       chan *update.Release
	updateChecked   bool
	updateRel       *update.Release
	updateShow      bool
	updateFenceOn   bool // WE hold the modal fence for the open What's New modal (released on close — see updateModalFence)
	updateScroll    int32
	updateChipLabel string
	// Update apply flow: updateBusy while it runs, updateStaged once the new binary is in place, updateErr on failure.
	updateApplyRes chan error
	updateBusy     bool
	updateStaged   bool
	updateErr      string
	relaunchOnExit bool // relaunch the binary after a clean shutdown ("Restart to apply", a GPU device loss)
	// relaunchArgs are the relaunched client's arguments; relaunchKeepTabs saves tabs even with restore-on-launch off.
	relaunchArgs     []string
	relaunchKeepTabs bool

	// IC/OOC log text selection, anchored to wrapped-line index and rune offset. logSelWhich names the owning log;
	// logSelPressed is the once-per-frame press edge, so whichever log draws first can't take it from the other.
	logSelWhich    int
	logSelActive   bool
	logSelDragging bool
	logSelAnchor   selPoint
	logSelHead     selPoint
	logSelPressed  bool
	logSelPrevDown bool
	logSelFill     sdl.Color // configured highlight colour, cached per frame
	// Chatbox selection: drag a range, double-click a word, triple-click the message. chatSelA/B are source-rune
	// boundaries (anchor, head). The animated-text path (msAnim) selects the whole message.
	chatSelActive   bool
	chatSelDragging bool
	chatSelPrevDown bool
	chatSelDownX    int32
	chatSelDownY    int32
	chatSelA        int
	chatSelB        int
	// Highlight-colour picker: the hue/sat wheel texture, built once, and the in-progress hex text.
	colorWheel     *sdl.Texture
	colorHex       string
	chromeEditSlot int    // which custom-chrome kit colour the wheel edits (0..6)
	chromeHexBuf   string // in-progress hex for the custom-chrome wheel's hex field
	// Mayo portrait on the About page, baked to the exact physical pixel size so it draws 1:1; rebuilt only when the
	// UI scale changes. mayoTexFailed stops a failed decode from retrying every frame.
	mayoTex            *sdl.Texture
	mayoLogW, mayoLogH int32 // on-screen (logical) size; the texture itself is physical px
	mayoTexScale       int   // UIScale() the texture was baked at
	mayoTexFailed      bool
	// Clicking the About portrait pets Mayo: mayoPets is the session count, mayoPetAt drives the wiggle and caption.
	mayoPets  int
	mayoPetAt time.Time
	// Each pet launches a roaming Mayo. Bounded; mayoRoamAt makes the motion frame-rate independent.
	mayoRoamers []mayoRoamer
	mayoRoamAt  time.Time

	// spriteTintHex is the in-progress sprite-tint hex; reflected from the pref when the field isn't focused.
	spriteTintHex string

	// lastOSToast rate-limits friend desktop toasts (osToastMinInterval).
	lastOSToast time.Time

	// lastModSFX debounces mod-command sounds per render.ModAction, collapsing the actor confirm plus area broadcast.
	lastModSFX [3]time.Time

	// Scene recording: when recActive, the event loop copies scene-mutating events into rec.
	recActive bool
	rec       *sceneRecording
	recStart  time.Time

	// Instant replay: when enabled, scene events also go into the bounded replayBuf ring, so the clip key can save the
	// last window without a prior recording. Nilled when the origin changes: a clip must never mix two servers' assets.
	// replayBufW is the next write index, replayBufN the entries held, replayBufOrigin the CDN they came from.
	replayBuf       []replayBufEntry
	replayBufW      int
	replayBufN      int
	replayBufOrigin string

	// Scene replay: a throwaway replayRoom, pointed at the recorded origin, is driven instead of the live room.
	// The driver feeds replayEvents[replayIdx] into it whenever it returns to idle.
	replaying    bool
	replayRoom   *courtroom.Courtroom
	replayEvents []recEvent
	replayIdx    int
	replayName   string
	replayPaused bool            // player ⏸ — freeze the scene (Next/Restart still work)
	replayRec    *sceneRecording // the source, kept so ⏮ Restart can rebuild from the top
	// Auto-chapters: the jump list derived once per replay, and whether its panel is open.
	replayChapters     []replayChapter
	replayChaptersOpen bool
	// replayWarm loads a flat recording's sprites and music before the first event plays, so nothing pops in or plays
	// silent. Non-nil only during that phase.
	replayWarm *replayWarmState

	// Scene maker: an in-app editor over the .aorec model, previewed through the replay engine.
	// Its state is allocated only while makerOpen; it draws as a full-window overlay.
	makerOpen       bool
	makerScene      *sceneRecording
	makerSel        int    // selected event index
	makerName       string // working filename stem (sanitized on Save)
	makerScroll     int32  // event-list scroll offset (px)
	makerPickerOpen bool   // the in-maker "Open a recording" list is showing
	makerExportOpen bool   // the in-maker "⚙ Export options" panel is showing
	makerTrimStart  int    // crop In point (event index), -1 = scene start
	makerTrimEnd    int    // crop Out point (event index), -1 = scene end
	// Timeline strip: proportional widths from recorded OffsetMs, with reused layout buffers so the draw stays
	// alloc-free. makerDragHandle is the crop handle being dragged (0 none, 1 In, 2 Out). makerDragSeg is the segment
	// being reordered: -1 = none and 0 is a valid index, so it must be initialised to -1. makerDragX is the
	// press-origin x.
	makerSegX       []int32
	makerSegW       []int32
	makerTLDur      []float64
	makerTLScroll   int32
	makerDragHandle int
	makerPrevDown   bool
	makerDragSeg    int
	makerDragMoved  bool
	makerDragX      int32
	makerScrub      bool // #68: dragging the timeline's scrub lane (selection follows the cursor)
	// makerPreviewRoom is a throwaway courtroom rendering the selected line; makerPreviewIdx is the line it reflects,
	// so the pane is rebuilt only when the selection or scene changes.
	makerPreviewRoom *courtroom.Courtroom
	makerPreviewIdx  int
	makerPreviewOrig string          // origin the preview room was built for (rebuild on change)
	makerPreviewKey  makerPreviewKey // visual identity of the previewed line (rebuild on change)
	// Self-contained archive: export bundles a scene's assets into a folder; replayBundled marks a replay whose assets
	// stream from that folder (Manager archive-source override) instead of the origin.
	replayBundled  bool
	makerExporting bool
	makerExportCh  chan string // archive-export goroutine → UI (result line)
	// Scene GIF export: renders offscreen across several frame-loop ticks, then encodes off-thread.
	gifExporting bool
	// exportSavedDevScale holds the live text device scale while an export forces ctx.textDevPct to 100, since the UI
	// scale must never leak into export pixels. 0 = not exporting; restored and zeroed by every finisher.
	exportSavedDevScale int
	gif                 *gifExportJob
	gifResultCh         chan exportResult // off-thread encode → UI (result line + artifact path)
	// Content report: probe a recording's assets at the origin with bounded workers, then optionally bundle the found
	// bytes. contentGen invalidates a cancelled job's late sends. Nil unless a job runs.
	content     *contentJob
	contentBusy bool
	contentGen  int

	// Background slideshow: while enabled and the courtroom is idle, slideBG holds the rotation URL ("" = not
	// overriding). The viewport renders the shallow copy slideScene, so the real scene is never mutated and a message
	// reverts instantly.
	slideIdx   int
	slideAt    time.Time
	slideBG    string
	slideScene courtroom.Scene

	// Applied theme. themeRes holds the newest off-thread load; themeGen ordering stops a slow stale load overwriting a
	// fresh one, because loads complete out of order.
	themeRes atomic.Pointer[themeApply]
	themeGen atomic.Uint64
	// themeCat is the off-thread theme catalog as an immutable snapshot: the scan reads directories, the draw path
	// may not (hard rules 2 and 5).
	themeCat atomic.Pointer[themeCatalog]
	// themesDirReady is a themes folder created off-thread and waiting to be opened.
	themesDirReady atomic.Pointer[string]
	// themeCatBusy single-flights the scan; atomic because the scanner goroutine clears it.
	themeCatBusy atomic.Bool
	// themeCatFocusStat records a window focus-gain for Settings to rescan on; render and event thread only, so a plain
	// bool.
	themeCatFocusStat bool
	// themeReport is the last apply's import report: every degrade, missing family and unbound key.
	// Bounded by themeReportCap and replaced at each landing, never appended to.
	themeReport []string
	// themeImp is the .aotheme import state; on App because a drop lands on any screen and must survive leaving it.
	themeImp themeImport
	// themeCopy is the one in-flight copy-for-editing, on App because Settings and the editor both start one.
	// The pointer is the single-flight flag: a second press is refused with a line.
	themeCopy *themeCopyJob
	// themeCreate is the one in-flight theme creation, on App for the same reasons as themeCopy,
	// so a second press can't race onto the same collision-suffixed name.
	themeCreate *themeCreateJob
	// themeEditArmGen names the one apply generation that opens the editor over a finished copy, so it cannot leak onto
	// a heal or boot apply. themeEditArmFrom keeps the editor from opening over a user who walked away.
	themeEditArmGen  uint64
	themeEditArmFrom Screen
	// themeAppliedGen is the newest gen pollThemeApply landed. The publish-time guard only protects an unconsumed
	// result; comparing here drops an older load published after the Swap, which would revert the theme and clear its
	// font.
	themeAppliedGen uint64
	themeHealAt     time.Time       // last eviction heal (themeHealPeriod pacing)
	themeTex        map[string]bool // theme:// stems resident in T1
	themeSounds     map[string]string
	themeMsgCol     sdl.Color
	themeHasMsg     bool
	themePalette    theme.Palette // last theme's chrome palette, kept so a #M3 chrome-preset change can re-overlay it
	themeNameCol    sdl.Color
	themeHasName    bool
	// themeShownameAlign is the theme's `showname_align`. The zero value Left is AO2's fallback.
	themeShownameAlign shownameAlign
	// themeShownameExtra is the theme's `showname_extra_width` in design pixels, the step of AO2's widen-and-swap
	// sizing. themeLayoutIn scales and bakes it, so the draw path never touches a float. Zero disables the widening.
	themeShownameExtra int
	// themeFonts is the applied per-element courtroom_fonts.ini table. The zero table means the theme styles nothing.
	themeFonts themeFontTable
	// Theme courtroom geometry: design-space rects and emote grid metrics; themeLay caches the window-scaled rects.
	// themeRectsOrig keeps the pristine geometry so the layout editor's Reset can restore it.
	themeRects     map[string]theme.Rect
	themeRectsOrig map[string]theme.Rect
	themeEmoteCell [2]int
	themeEmoteGap  [2]int
	themeLay       themeLayoutCache
	// themeSidecar is the applied theme's parsed asyncao_theme.ini, nil for a stock AO2 theme, so reads are nil-safe.
	// Read only by the cold bake in themeLayoutIn.
	themeSidecar *theme.Sidecar
	// themeElemGen stamps each element bake; monotonic so the condition memo can't mistake a fresh cache for its own.
	themeElemGen uint64
	// elemBakeBand and elemBakeZ are the bake's sort keys, kept beside the baked array because the draw path uses
	// neither.
	elemBakeBand [theme.ElementCap]theme.ElementBand
	elemBakeZ    [theme.ElementCap]int16
	// condSrc and condLive memoize this frame's visible_when resolution: the live string per axis and its interned
	// index (condAlwaysVisible = no match). condGen is their bake generation. Refreshed once per themed pass.
	condSrc  [theme.ConditionAxisCount]string
	condLive [theme.ConditionAxisCount]int16
	condGen  uint64
	// condSpeaking is the one value-less condition axis: a message is on the stage.
	condSpeaking bool
	// themeClocks is the fixed authoring-clock pool. Clock 0 is the shared themeAt anchor, so the zero value means
	// every element runs as themeFrame does and needs no initialising. Reloaded beside a.themeAt in pollThemeApply.
	themeClocks [theme.ClockCap]themeClock
	// themeFX is the stateful-effect pool for the decaying one-shots; periodic kinds need none.
	// fxState.owner is the baked index plus one, so 0 means free.
	themeFX [themeFXCap]fxState
	// themeFXFrame is the pool's pass counter, advancing once per element pass.
	themeFXFrame uint64
	// themeFXOver records that the pool ran out during the last pass; every sweep clears it.
	themeFXOver bool
	// themeFXFrozen holds ReduceMotion for the frame: one prefs read per frame, never per element or draw site.
	// Set by App.Frame, because themeFrame is reached from screens with no element pass, and by beginThemeFXPass,
	// because an offscreen pass may run without a Frame. Setting it twice in a frame is harmless.
	themeFXFrozen bool
	// themeSidecarErr and themeSidecarTip are the applied theme's sidecar refusal, as debug-log line and hover text;
	// built on the apply goroutine because both format. "" = loaded or absent. A refused sidecar is otherwise
	// invisible: the AO2 half renders and everything in the file is silently gone.
	themeSidecarErr string
	themeSidecarTip string
	// themeSidecarErrAck is the chip's dismissal, cleared by every apply that still refuses and never on a timer,
	// because the condition does not expire.
	themeSidecarErrAck bool
	// themeMediaKeys is the theme:// media and generator keys this client uploaded, for the swap prune to reconcile. A
	// UI-side set like themeTex: the prune needs what the outgoing theme declared, the store only knows what is
	// resident.
	themeMediaKeys map[string]bool
	// themeMediaPlan is the applied plan, kept so the inspector, budget bar and F8 line quote the planner's numbers;
	// the store cannot say why something is absent.
	themeMediaPlan themeMediaPlan
	// Char select's separate design table and window-scaled cache, not folded into themeRects/themeLay: those keys live
	// in a different design space, and everything in themeRects becomes an editable box in the courtroom layout editor.
	charSelDesign charSelectDesign
	charSelLay    charSelectLayoutCache
	// tabBarSeeded records that seedTabBarDesignRect synthesized "asyncao_tabbar". tabStripThemeParked needs it: a
	// theme author's placement must move the strip, a synthesized default must leave it in the client chrome band.
	tabBarSeeded bool
	// themeResizeArmGen is the themeGen of the one apply allowed to move the window; 0 = disarmed. It names its apply
	// because boot, healTheme, the Purge recovery and server theme binding all re-enter the pipeline; a bare flag would
	// be consumed by a heal already in flight and snap the window to the previous theme's canvas. Armed at
	// user-initiated sites and once at boot by the ThemeFit move, consumed by maybeResizeToThemeDesign.
	themeResizeArmGen uint64

	// Live layout editor for the themed courtroom; overrides persist per theme.
	layoutEdit bool
	// editTgt is the themed editor's selection, carrying its space explicitly (edittarget.go).
	// Nothing constructs an element target yet, so it holds a spaceDesign key or nothing.
	editTgt  editTarget
	editDrag int // 0 none, 1 move, 2 resize
	// editEdges is which edges a themed resize drags (edgeL/R/T/B), remembered from the press because the drag
	// arithmetic, grid snap and magnet all key off them.
	editEdges    uint8
	editPrev     bool
	editStart    [2]int32
	editBase     theme.Rect
	layoutSnap   bool // snap edits to a design-space grid (toggle in the editor)
	layoutAspect bool // lock the viewport (stage) to 4:3 while resizing it in the editor
	// layoutMagnetOff backs the "Magnet: on/off" chip, disabling only snapToSiblings; edge snapping and de-overlap stay
	// on. Stored as "off" so the zero value is magnet on, and float panels snap in normal play with no setup.
	layoutMagnetOff bool
	// Overlap cycling: Tab steps through the boxes under the cursor, smallest first, so a hidden box is reachable.
	// editPickSig is the stack's fingerprint; the index resets when it changes.
	editPickIdx int
	editPickSig string
	// Layout-editor undo/redo: each edit snapshots the rect map plus the tab strip's placed flag, which cannot be
	// derived from the rects (layoutSnapshot). Bounded.
	editUndo []layoutSnapshot
	editRedo []layoutSnapshot

	// Classic-layout editor: edits screen-space rects persisted as window fractions (config.ClassicLayout) so they
	// survive resizes. classicOv is a snapshot read by slotRect, with the editor as sole writer. slotReg is populated
	// only while editing, so the common frame stays alloc-free.
	classicEdit     bool
	classicOv       map[string][4]float64
	classicOvLoaded bool
	// tornZ is the tear-off panel z-order, a permutation of tornTabTable indices back-to-front; empty = table order.
	tornZ []int
	// classicAnchor holds per-element window pins: a pinned override rebases to pixel offsets from its window corner or
	// centre on resize instead of scaling with the fractions.
	classicAnchor map[string]anchorRef
	// classicRot holds per-element rotation bytes. An absent entry means angle 0, the plain Copy path.
	classicRot map[string]uint8
	slotReg    map[string]slotInfo
	// classicTgt is the classic editor's selection, in screen pixels and persisted as a window fraction.
	classicTgt       editTarget
	classicEditDrag  int // 0 none, 1 move, 2 resize
	classicEditStart [2]int32
	classicEditBase  sdl.Rect
	classicEditEdges uint8 // which edges a resize drags (edgeL/R/T/B bitmask); 0 mid-move
	classicEditPrev  bool  // press-edge latch (mouse-down rising edge)
	classicEditMoved bool  // a drag actually moved/resized (else a click persists nothing)
	// Undo/redo of the whole override map, edit mode only, bounded by layoutUndoCap.
	// classicPickIdx/Sig Tab-cycle the stack under the cursor.
	classicUndo    []map[string][4]float64
	classicRedo    []map[string][4]float64
	classicPickIdx int
	classicPickSig string
	// classicChromeBot is the bottom Y of the editor's top chrome; name tags clamp below it so they can't cover its
	// controls.
	classicChromeBot int32
	// alignGuides and alignScratch are reused per drag frame so a long drag allocates nothing after warm-up.
	// alignGuides is shared with the live and themed magnets. That is safe: drawFloatingPanels returns early while
	// classicEdit is on, and the themed editor populates and renders its guides before drawFloatingPanels empties them.
	alignGuides  []alignGuide
	alignScratch []sdl.Rect
	// panelMagnetRects is the live magnet's candidate buffer, rebuilt per frame only while a drag is active and bounded
	// by panelMagnetCap. themeAlignScratch is the themed editor's design-space buffer.
	panelMagnetRects  []sdl.Rect
	themeAlignScratch []sdl.Rect
	// layoutPresetName is the Settings field for saving the current layout as a named preset.
	layoutPresetName string
	// layoutProfileCursor is the banner Profile chip's index into sorted saved-profile names; -1 = none applied this
	// session. Names are read each draw, so a deleted or renamed profile can't strand it.
	layoutProfileCursor int
	// The bottom-right toolbox is the single show/hide and editor surface. toolboxPinned holds the flyout open; while
	// pinned, toolboxPieces reveals the per-piece panel. The zero value is collapsed.
	toolboxPinned       bool
	toolboxPieces       bool
	toolboxPiecesScroll int32
	// toolboxFilter is the pieces-panel text filter; toolboxFilterLower is recomputed only when it changes,
	// so per-row matching allocates nothing.
	toolboxFilter      string
	toolboxFilterLower string
	// toolboxThemeRect is the toolbox's per-frame themed position, from the optional "asyncao_toolbox" design key.
	// Absent key means toolboxThemeRectOn is false and the classic default path runs. Set and cleared each themed
	// frame.
	toolboxThemeRect   sdl.Rect
	toolboxThemeRectOn bool
	// toolboxFence is this frame's single toolbox decision: set at the top of the courtroom pass and replayed by the
	// draw at the bottom, so overlay blocking and painted pixels agree even with handleHotkeys and the modal return
	// between them. Cleared at the top of every drawCourtroom so other frames can't replay a stale one.
	toolboxFence compactToolboxLatch
	// tabStripPaint is this frame's answer to which of the tab strip's two draw sites paints it, so a mid-frame change
	// can neither paint it twice nor drop it. Reset by App.Frame; a frame with no courtroom pass uses the
	// over-everything site.
	tabStripPaint tabStripPaintLatch
	// App-wide menu bar. menuOpen and menuSub are the title and label of the open menu and submenu: strings, so the
	// zero App is closed and reordering the model can't change which pane is up. menuFenceOn records that we set
	// c.modalOn; that flag is shared with sibling popups, so the release must check them too (menuBarModalFence,
	// otherModalFenceHeld). menuBar is computed by menuBarFrame above the screen dispatch and replayed by drawMenuBar
	// after the screens.
	menuOpen    string
	menuSub     string
	menuFenceOn bool
	menuBar     menuBarLatch
	// editorBannerPainted says a layout editor's banner reached the screen this frame; reset by App.Frame,
	// read once by drawMenuBar (noteEditorBanner explains why armed flags don't suffice).
	editorBannerPainted bool
	// pathStroke is the in-progress freehand stroke for the "draw a path" box, as box-relative points sampled to at
	// most 6 waypoints on release. pathPrevDown is its press-edge tracker. Bounded.
	pathStroke   []sdl.Point
	pathDrawing  bool
	pathPrevDown bool
	// themePages is the generation-keyed page cache for theme:// textures, taking no store locks while unchanged.
	themePages    map[string]*render.TexturePage
	themePagesGen uint64
	// Chrome shape masks: resolved once per frame by refreshShapeMasks so widget draws read pointers, never a store
	// Get. shapeMasksGen mirrors the store generation so an eviction of the pinned pages triggers a paced rebuild.
	// shapeMaskShape is the preset the resident masks were built for, so a Settings change re-rasters.
	shapeMasksBuilt bool
	shapeMasksGen   uint64
	shapeMaskShape  string
	shapeMaskAt     time.Time // last (re)build for eviction-heal pacing
	// shapeResolved* memoize the last mask resolve (shape, adjusted tier, store generation), so settled frames skip the
	// store Gets and stay 0-alloc. Safe because every store mutation that could invalidate the pointers bumps the
	// generation.
	shapeResolvedShape string
	shapeResolvedTier  int
	shapeResolvedGen   uint64
	// themeAt anchors the looping clock for animated theme art.
	themeAt time.Time
	// themeAppliedName is the last theme that landed, so per-server bindings only load when something changes.
	themeAppliedName string

	// showWidgets opens the Extras box: every AsyncAO feature an AO2 theme has no button for. themedHintShown limits
	// the hint pointing players at it to once, since legacy themes carry no AsyncAO element keys.
	showWidgets     bool
	themedHintShown bool
	// Floating Extras box: extrasPlaced false means centered until first dragged or seeded.
	// Position and size persist via slotExtras (seedExtrasFromSlot, persistExtrasSlot).
	extrasBoxX, extrasBoxY     int32
	extrasPlaced               bool
	extrasDragging             bool
	extrasGrabDX, extrasGrabDY int32
	extrasPrevDown             bool
	// Torn-off widgets, each in its own floating box; closing one returns it to the grid. The zero App is idle.
	extrasDetached       []detachedWidget // bounded: one box per widget at most
	extrasTornRebuilt    bool             // one-shot latch: torn widgets reconstructed from persisted slots this session (reset by applyProfile)
	extrasDetachDragging bool             // a detached box is being moved
	extrasDetachResizing bool             // a detached box is being resized
	extrasDetachIdx      int              // which extrasDetached index, while dragging/resizing
	extrasPressing       bool             // a grid cell is held (pending click-or-tear)
	extrasPressID        int              // that cell's widget id
	extrasPressX         int32            // press origin, for the tear threshold
	extrasPressY         int32
	extrasUserW          int32 // user-resized main box size (0 = default extrasBoxW/H)
	extrasUserH          int32
	extrasResizing       bool           // the bottom-right resize grip is being dragged
	extrasCloseHintShown bool           // one-shot "how to reopen" toast on first × close
	extrasWidgetCache    []extrasWidget // canonical widget table, built once
	// extrasScroll is the widget grid's scroll offset in pixels; the widgets don't fit any sane box height.
	extrasScroll int32
	// Floating-window geometries for non-blocking panels, so chat stays live behind them.
	// Geometry is global; open state is per tab or per flag.
	pairWin, modWin, cmWin, hkWin, evidWin, modcallWin, msgWin, voiceWin, banWin, debugWin floatWin
	// Timer, login and pair popups are floating too, so the scene stays visible while they are open.
	timerWin, loginWin, pairPopWin floatWin
	// Voice chat (Nyathena VS_* relay): showVoice is global; membership and mic state are per session.
	showVoice bool
	// voiceAudio is the capture and playback engine, non-nil only while joined to a voice channel.
	voiceAudio *voiceEngine
	// micTest is the Settings "Test microphone" capture device, non-nil only during a test.
	// micTestSidetone remembers the "hear myself" toggle.
	micTest         *micTester
	micTestSidetone bool
	// makerExportPack > 0 means the "include your own files?" dialog holds the scene until answered.
	// makerExportPackOK is the one-shot consent it grants.
	makerExportPack   int
	makerExportScene  *sceneRecording
	makerExportName   string
	makerExportPackOK bool

	// showQuitConfirm means the "Quit AsyncAO?" dialog is up; skipped when QuitConfirmSkip is set.
	showQuitConfirm bool
	// Group Chat panel, global. msgSel is the selected partner char name; msgInput is the compose buffer.
	showMessages   bool
	msgSel         string
	msgSelGroup    uint32 // selected group id (0 = a DM is selected via msgSel)
	msgGroupManage bool   // group view is showing the members / invite panel
	msgInput       string
	msgListScroll  int32
	msgGroups      map[uint32]*msgGroup // client-side group chats (keyed by group id)
	pendingInvites []groupInvite        // group invites awaiting Accept / Decline
	msgIconAsk     map[string]time.Time // paced char-icon demands for the messaging surfaces (bounded)
	// Remote-speaker char.ini metadata: URL-keyed, bounded cache plus async results. Per-server by key structure.
	charMetaCache map[string]charMeta
	charMetaRes   chan charMetaFetch
	// AO2 screen-effect overlays: the per-(origin, misc folder) roster cache, async results, theme-tier art loads and
	// the dropdown's icon pages. Bounded, and keyed so a server switch can't hand one origin's rosters to another.
	overlayRosters map[string]*overlayRoster
	// overlayRosterInFlight is kept separate from the settled cache: its cap reset must never wipe a marker, or the
	// next request starts a second goroutine whose result races the first; and overlayRosterFetchCap needs a countable
	// set.
	overlayRosterInFlight map[string]bool
	overlayRosterRes      chan overlayRosterFetch
	overlayRosterGen      uint64
	// overlayTheme is the applied theme's snapshot, read off-thread by roster resolves instead of re-running
	// theme.Load. *theme.Theme is read-only after Load, so sharing it with the fetch goroutine is race-free.
	overlayTheme      *theme.Theme
	overlayTexRes     chan overlayTexLoad
	overlayTexLoading map[string]bool
	overlayIconPages  []*render.TexturePage
	overlayIconGen    uint64
	overlayIconAsk    []time.Time
	overlayThumbFn    func(idx int, r sdl.Rect)
	// overlayPreviewBase is the base the right-click preview opened the shared sprite-preview box on ("" = none).
	// The box must close with the list, but previewBase is shared, so only a box that is still ours may be closed.
	overlayPreviewBase string
	// Per-frame memos: drawICInputRow asks for the roster every frame and must not allocate, so the cache key, char.ini
	// URL and choice slice are baked on a state change. pollOverlayRosters and pollCharMeta invalidate them.
	overlayMemoOrigin    string
	overlayMemoFolder    string
	overlayMemoValid     bool
	overlayMemoRoster    *overlayRoster
	overlayMyFolder      string
	overlayMyFolderChar  string
	overlayMyFolderValid bool
	overlayChoicesCache  []string
	overlayChoicesRoster *overlayRoster
	overlayChoicesValid  bool
	// overlayIconKeys and overlayIconPaths are the per-row icon page key and theme-tier local file, baked with
	// overlayChoicesCache because dropdownEx paints the closed control's pick every frame and must not allocate.
	overlayIconKeys  []string
	overlayIconPaths []string
	// overlayIconFolder is the misc folder those keys were baked for: cachedPage keys by index, so a swap into another
	// effects pack with the same row count would keep the old pages.
	overlayIconFolder string
	// overlayIconSize is `effects_icon_size` (courtroom.cpp:979). Zero means the strip is sized from the row height.
	overlayIconSize [2]int
	// Command palette (Ctrl+Space).
	paletteOpen  bool
	paletteQuery string
	paletteSel   int
	// hkPrevDown is the hotkey sheet's own press edge: it draws over every screen, outside the courtroom box pass.
	hkPrevDown bool
	hkScroll   int32 // hotkey cheat-sheet scroll offset (it's a scrollable list now)
	// piecesWin and piecesPrevDown are the Hide-UI-pieces panel's floating window and press edge.
	piecesWin      floatWin
	piecesPrevDown bool
	// Multi-server split: splitTab pins a background tab rendered by splitRoom, on courtroom.NopAudio so only the
	// focused pane makes sound, and splitVP over the shared store, whose origin-qualified keys can't clash. nil = no
	// split.
	splitTab  *courtTab
	splitRoom *courtroom.Courtroom
	splitVP   *render.Viewport
	// Pinned-pane chat log: rows cached by width, pinned log seq and pin epoch, so steady frames are 0-alloc.
	// splitPinEpoch bumps on every pin change, since a different tab's icLogSeq can equal the last one's (see
	// logViewEpoch).
	splitPinEpoch  uint64
	splitWrapW     int32
	splitWrapSeq   uint64
	splitWrapEpoch uint64
	splitWrapRows  []splitLogRow
	// The pinned pane wraps in the log face, so the cache also keys on the log zoom and font chain generation.
	splitWrapPct int
	splitWrapGen int
	// The pinned server draws in a movable, resizable floating window. clientHdr caches the title by server name
	// so the per-frame draw never concatenates.
	clientWin     floatWin
	clientHdr     string
	clientHdrName string
	// clientFull selects the full-theme view of the pinned client over compact (stage, log, input). It renders into
	// clientTex at logical screen size and shrinks into the window; recreated only on a size change.
	// View-only by necessity: the kit is single-instance, so widget ids would collide and modal writes would leak.
	clientFull bool
	clientTex  *sdl.Texture
	clientTexW int32
	clientTexH int32
	// pinnedPass is true during a view-only drawCourtroom over a non-live session (pinned client, theme editor
	// preview). Single-consumer channel drains (pollCharINI) skip while set, so the pass can't eat a result meant for
	// the primary.
	pinnedPass bool
	// pendingControlSwap requests making the floating client the live courtroom; applied at the next frame's top,
	// never mid-render, since it swaps the active session and room.
	pendingControlSwap bool
	// Full-theme view zoom and pan: clientZoom 1 = fit; clientPan{X,Y} is the view centre in texture space (0..1).
	// clientZoomLbl memoizes the readout.
	clientZoom                     float64
	clientPanX, clientPanY         float64
	clientPanning                  bool
	clientPanGrabX, clientPanGrabY int32
	clientPanBaseX, clientPanBaseY float64
	clientZoomLbl                  string
	clientZoomLblPct               int
	// Theme-fit Custom preview in Settings: drag to pan.
	themeFitDrag      bool
	themeFitDragStart [2]int32
	themeFitDragBase  [2]int
	// Sprite-preview box: wheel zoom and left-drag, handled by handlePreviewInput before screens draw so it claims
	// input ahead of what lies underneath. previewFrameRect caches last frame's box for that hit-test.
	previewOffX, previewOffY int32
	previewDrag              bool
	previewDragMoved         bool
	previewDragStart         [2]int32
	previewDragBase          [2]int32
	previewFrameRect         sdl.Rect
	// emoteGridDrewPreview says the sprite-preview box was painted this frame, so drawSpritePreviewFallback can stand
	// in when the grid did not run without compositing twice. Cleared once per frame by pollOverlayRosters, ahead of
	// screen draws.
	emoteGridDrewPreview bool
	// previewFence is this frame's overlay-blocking answer for the box, reset by handlePreviewInput at the top of every
	// frame so a pass that never publishes can't replay a stale mark.
	previewFence previewFenceLatch
	// Every character previews at the same height (previewBaseH, AO's native 192) whatever its source resolution;
	// the corner grip sets previewUserH (0 = default).
	previewUserH      int32
	previewResize     bool
	previewResizeFrom int32 // mouseY at grip press
	previewResizeBase int32 // previewUserH (resolved) at grip press
	// Close-on-leave: previewTriggerRect is the cell that opened the box, previewEntered is set once the cursor reaches
	// it. Together they let the cursor cross the gap without the box vanishing, then close it when the cursor leaves.
	previewTriggerRect sdl.Rect
	previewEntered     bool
	previewPinned      bool // right-click pins the sprite-preview box open until you close it (its x)
	// previewWinFedBase is the previewBase whose current frame the detached window was last fed, so a new pick feeds
	// once and going not-ready clears it. App-level because previewBase is this package's concept, not the window's.
	previewWinFedBase string
	// previewWinFedPage is the page last fed from. A T1 eviction and re-decode of the same base makes a new page
	// pointer, which must trigger a feed even on the same frame index, or the window keeps showing stale content.
	previewWinFedPage *render.TexturePage
	// previewWinFedFrame is the frame index last fed, always 0 for a static page. feedDetachedPreviewFrame does GPU
	// work only when the index differs, so a looping animation's readback follows actual frame changes.
	previewWinFedFrame int
	// previewWinWasOpen detects the single frame the detached window closed, so its position and size persist once.
	// It is not a second "is detached" state: those queries go to a.d.Preview.IsOpen().
	previewWinWasOpen bool
	vpExactBuf        string // edit buffer for the Settings exact-stage-width px field (commit on Enter)
	hidden            map[string]bool

	iniRes chan iniswapFetch

	// Layout scales in percent; mirror prefs, saved on change.
	vpPct, chatPct, boxPct, logPct, inputPct int
	oocPct                                   int // OOC log text size, independent of logPct (IC log)
	musicPct                                 int // music-list font scale, independent of the log so long titles can shrink without shrinking IC
	areaPct                                  int // areas-list font scale (Ctrl+wheel); mirrors musicPct — on App (global, survives tab switch), NOT sessionState, so it never resets on a session swap
	modDashPct                               int // mod/CM dashboard list font scale (Ctrl+wheel); mirrors musicPct — on App (global), independent of the log
	playerPct                                int // Players-tab/pair-popup text zoom (Ctrl+wheel); moved OUT of sessionState to mirror musicPct — session-swap resets silently discarded the zoom (playtest: the zoom did not take; the docked tab also needed its own case in the panel scale switch, screens.go)
	uiScalePct                               int // global renderer scale (manual)
	// The manual UI-scale slider commits on release: applying live rescales the slider under the cursor.
	// These hold the in-drag value.
	uiScalePending  int
	uiScaleDragging bool
	// detectedScalePct is the display-derived scale (96 dpi = 100%), snapped to the settings step; UIScale() prefers it
	// while auto-HiDPI is on. 0 = detection unavailable.
	detectedScalePct int
	// dpiScalePct is the DPI-only part of the auto scale, floored at 100; SetAutoScaleFromWindow combines it with
	// window size.
	dpiScalePct int
	// lastDPIDisplayIndex is the display last queried, so a move within one monitor costs no syscall or font rebuild.
	// -1 = never queried.
	lastDPIDisplayIndex int
	// theaterOn is the borderless viewport-only mode. Session-only, so it can't persist someone into a chrome-less
	// client.
	theaterOn bool

	// Jukebox: the library is shared across servers and must survive disconnects and tab switches, so not in
	// sessionState.
	juke         *config.Jukebox      // global store, loaded off-thread
	jukeRes      chan *config.Jukebox // off-thread load landing
	jukeIORes    chan string          // off-thread export/import result → in-app toast
	jukeCache    []config.Playlist    // rev-keyed Playlists() snapshot for drawing
	jukeCacheRev int64
	jukeOpen     int    // -1 = playlist list; else the open playlist index
	jukeSearch   string // filters playlists (top) or songs (inside one)
	jukeScroll   int32
	aboutScroll  int32 // About screen scroll offset (the page outgrew small windows)
	// About prose is reflowed with WrapText and cached by column width, so the wrap runs only on a resize.
	aboutFlatW int32           // content width the cache was built for (0 = none)
	aboutFlat  []aboutFlatLine // flattened, wrapped render lines
	// Changelog: same width-keyed reflow cache as About.
	changelogScroll int32
	changelogFlatW  int32
	changelogFlat   []aboutFlatLine
	// Help screen: same width-keyed reflow cache.
	helpTab       int
	helpDocScroll int32
	helpFlatW     int32
	helpFlat      []aboutFlatLine
	// Settings content region right of the sidebar, set once per frame by drawSettings; helpers rebase onto formX.
	formX, formW    int32
	jukeNewName     string // "new playlist" input
	jukeAddURL      string // "add song" URL input
	jukeAddTitle    string // "add song" optional title input
	jukeDelPlaylist int    // playlist index awaiting delete confirmation (-1 = none)
	jukeBindFor     string // key-capture target: "" / "p:<i>" / "e:<pl>:<i>"
	jukeFiltered    []int  // memoized matching entry indices in the open playlist
	jukeFilteredKey jukeFilterKey
	musicHist       []musicHistEntry // session-only "recently played" ring (MRU, capped)
	jukeShowRecent  bool             // top-level jukebox view: recently-played vs playlists
	jukeHistScroll  int32
	jukeRecentLbl   string            // cached "Recently played (N)" toggle label (rebuilt only on change, so the per-frame draw never Sprintfs)
	musicHostInput  string            // Settings: "add a domain" field for the music-history allowlist
	jukeGroupRows   []jukeGroupRow    // memoized domain-grouped row layout for the "Music history" playlist
	jukeGroupKey    jukeGroupCacheKey // (playlist, revision) the grouped layout was built for
	jukeShowFav     bool              // top-level jukebox view: ★ favorites vs playlists/recent
	jukeFavScroll   int32
	jukeFavs        []favRef // memoized list of starred songs across all playlists
	jukeFavRev      int64    // library revision the favorites list was built for
	jukeFavLbl      string   // cached "★ Favorites (N)" toggle label (rebuilt only on change)

	// dlPaused is the download worker's pause flag, outside sessionState because that is copied per tab and an atomic
	// can't be. It is the only state shared with the worker; everything else is snapshotted at launch, so they never
	// race.
	dlPaused atomic.Bool
}

// The room is deliberately not parked with sessionState: background tabs don't animate, and activation rebuilds it.

// NewApp builds the UI over deps.
func NewApp(ctx *Ctx, d Deps) *App {
	a := &App{
		ctx:                 ctx,
		d:                   d,
		screen:              ScreenLobby,
		lastDPIDisplayIndex: -1, // #77 Part B: no DPI query yet (SeedDisplayDPIScale sets it)
		lobbyResult:         make(chan lobbyFetch, 1),
		pingRes:             make(chan pingResult, pingResBuf),
		charINIres:          make(chan charINIFetch, 1),
		previewEmoteRes:     make(chan previewEmoteFetch, 1),
		updateRes:           make(chan *update.Release, 1),
		updateApplyRes:      make(chan error, 1),
		iniRes:              make(chan iniswapFetch, 1),
		manifestRes:         make(chan manifestFetch, 1),
		casingRes:           make(chan casingProbeResult, 1),
		mountIndexRes:       make(chan mountIndexResult, 1),
		fontRes:             make(chan fontLoad, 1),
		logBrowserRes:       make(chan logbrowser.Result, 1),
		emojiFontRes:        make(chan []byte, 1),
		fallbackFontRes:     make(chan [][]byte, 1),
		cjkFontRes:          make(chan [][]byte, 1),
		errorSpriteRes:      make(chan errorSpriteLoad, 1),
		notebookRes:         make(chan notebookLoad, 1),
		jukeRes:             make(chan *config.Jukebox, 1),
		jukeIORes:           make(chan string, 4),
		jukeOpen:            -1,
		jukeDelPlaylist:     -1,
		selServer:           -1,
		activeTab:           -1,
		tabDragFrom:         -1,
		macroBind:           -1,
		themeTex:            map[string]bool{},
		themeMediaKeys:      map[string]bool{},
		themePages:          map[string]*render.TexturePage{},
		hidden:              map[string]bool{},
	}
	a.resetSessionState()
	// A dropdown row or lobby label can be the first emoji on screen, so the widget kit must be able to start the face
	// read.
	ctx.emojiLoad = a.ensureEmojiFontLoad
	// Wake the event-driven render loop when a payload delivers, so the pump uploads it on the next pass.
	if d.Manager != nil {
		d.Manager.SetDeliveryNotify(PushWake)
	}
	// Held-frame bridge: when the LRU must evict an on-screen stage layer to fit a cap-sized page, the store moves one
	// frame to its pinned tier and the viewport draws that instead of exposing the black stage fill
	// (render/textures.go).
	if d.Store != nil {
		d.Store.SetLiveScenery(a.IsLiveStageBase)
	}
	// Pin the embedded placeholder first, so a failed custom load leaves a valid page in place.
	a.uploadEmbeddedMissingno()
	a.uploadEmbeddedSpeedlines() // #126 bundled zoom speedline fallback
	if p := d.Prefs.Load().ErrorSpriteFile; p != "" {
		a.applyErrorSprite(p)
	}
	a.seedHiddenFromPrefs() // hidden-chrome set + the A6 no-strand normalization
	// The jukebox can be large, so it loads off-thread and lands via pollJukebox.
	go func() {
		if j, err := config.LoadJukebox(); err == nil {
			a.jukeRes <- j
		}
	}()
	a.armBootThemeResize(a.applyThemeAsync())                                          // chatbox skin + font colors from the saved theme (+ the one-time #29 upgrade snap)
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = d.Prefs.Load().LayoutScales() // per-session view state (pair, OOC name, volume views) seeds in resetSessionState (above)
	a.oocPct = d.Prefs.Load().OOCScalePct                                              // OOC log text size, independent of the IC log
	a.musicPct = a.logPct                                                              // starts matching the log; ctrl+wheel over the Music tab tunes it apart
	a.playerPct = a.logPct                                                             // same for the Players tab + pair popup
	a.areaPct = a.logPct                                                               // same for the Areas list (all three hosts: docked tab, torn, themed)
	a.modDashPct = a.logPct                                                            // same for the Mod/CM dashboard lists
	a.uiScalePct = d.Prefs.Load().UIScalePct
	ctx.SetUIScale(a.uiScalePct)
	a.applyFontConfig()                                            // dyslexia toggle or manual font path, resolved once
	a.dndOn = d.Prefs.Load().DNDPersist && d.Prefs.Load().DNDSaved // else session-only: clears each launch
	a.RefreshServers()
	// Restore-on-launch: Frame reconnects one tab per frame. Capped so we never queue a connect allocateTab would
	// reject.
	if d.Prefs.Load().RestoreTabs || d.RestoreTabs {
		q := d.Prefs.Load().OpenTabList()
		if cap := d.Prefs.Load().TabCap(); len(q) > cap {
			q = q[:cap]
		}
		a.restoreQueue = q
		if !d.Prefs.Load().RestoreTabs {
			d.Prefs.SetOpenTabs(nil) // saved only for this relaunch: restore-on-launch is off
		}
	}
	// Auto-connect-on-launch dials the last server only if tab restore queued nothing; tab restore wins if both are on.
	if d.Prefs.Load().AutoConnectOnLaunch && len(a.restoreQueue) == 0 {
		if _, url := d.Prefs.Load().LastServer(); url != "" {
			a.autoConnectPending = true
		}
	}
	a.applyChromePreset(d.Prefs.Load().ChromeTheme()) // #M3: apply the saved client chrome theme at launch
	a.refreshPartColors()                             // per-part layout tints: parse the saved hexes once
	// Config renamed a corrupt settings file aside so the saver can't overwrite the only copy; say so once via
	// warnLine.
	if q := d.ConfigQuarantine; q != nil {
		msg := "Settings file was corrupt — reset to defaults."
		if q.BackupPath != "" {
			// Basename only: a full Windows path would pass clampLine's cap and truncate the important part.
			msg += " Your old file was saved as " + filepath.Base(q.BackupPath) + " (beside it)."
		}
		a.warnLine = clampLine(msg)
		a.warnAt = time.Now()
	} else if line := mutedChannelsBootNotice(a.masterMuted, a.musicMuted, a.sfxMuted, a.blipMuted); line != "" {
		// The mute flags are persisted but the volume strip showing them defaults hidden, so a user who quit muted
		// would get silence with no visible reason. The corrupt-prefs notice is more consequential, so it wins
		// warnLine.
		a.warnLine = line
		a.warnAt = time.Now()
	}
	// Warm the local-pack index now: starting on connect would race the character-icon prefetch and lose to the
	// resident-texture short-circuit. Starts no goroutine when no mounts are configured.
	a.applyMountLayer()
	return a
}

// UIScale returns the global scale percent: the DPI-detected value under auto-HiDPI, the manual setting otherwise.
func (a *App) UIScale() int {
	if a.detectedScalePct > 0 && a.d.Prefs.Load().UIScaleAutoOn {
		return a.detectedScalePct
	}
	return a.uiScalePct
}

// autoScaleRefWidth and autoScaleRefHeight are the physical window size treated as 100% UI scale.
// SetAutoScaleFromWindow scales by the more constrained axis, so the UI never overflows a narrow or tall window.
const (
	autoScaleRefWidth  = 1280
	autoScaleRefHeight = 800
)

// SetDisplayDPIScale records the DPI-derived auto scale, floored at 100 so an unreliable DPI reading can't shrink the
// UI.
func (a *App) SetDisplayDPIScale(pct int) {
	a.dpiScalePct = max(config.MinAutoUIScalePercent, pct)
}

// SeedDisplayDPIScale records the auto scale from the OS DPI of the window's monitor; render thread, at boot and on
// display change. It prefers host.WindowDPI because sdl.GetDisplayDPI reports a flat 96 under per-monitor-v2 awareness.
// Runtime-only and auto path only: a saved manual scale still wins.
func (a *App) SeedDisplayDPIScale() {
	win := a.ctx.win
	if win == nil {
		return
	}
	// Remember the display so NoteDisplayChanged can skip a same-monitor move.
	if idx, err := win.GetDisplayIndex(); err == nil {
		a.lastDPIDisplayIndex = idx
	}
	dpi, ok := host.WindowDPI(win)
	if !ok {
		// Cross-platform fallback.
		idx, err := win.GetDisplayIndex()
		if err != nil {
			return
		}
		ddpi, _, _, derr := sdl.GetDisplayDPI(idx)
		if derr != nil || ddpi <= 0 {
			return
		}
		dpi = float64(ddpi)
	}
	a.SetDisplayDPIScale(config.DPIScalePercent(dpi))
}

// NoteDisplayChanged re-seeds the auto scale once the window has moved to a different monitor.
// WINDOWEVENT_MOVED fires per drag, so a same-monitor move must cost no syscall or font rebuild. Render thread.
func (a *App) NoteDisplayChanged() {
	win := a.ctx.win
	if win == nil {
		return
	}
	idx, err := win.GetDisplayIndex()
	if err != nil {
		return
	}
	if idx == a.lastDPIDisplayIndex {
		return // same monitor — not a DPI change
	}
	a.SeedDisplayDPIScale() // updates lastDPIDisplayIndex + re-detects DPI
}

// NoteFocusLost drops the focused text field, so clicking away doesn't leave the client looking ready to type.
// The auto-refocus in chatfocus.go rearms on the next in-app keystroke.
func (a *App) NoteFocusLost() {
	if a.ctx == nil {
		return
	}
	a.ctx.focusID = ""
	a.ctx.selAnchor = -1
}

// SetAutoScaleFromWindow updates the auto UI scale from the physical window size while auto-scale is on: the larger of
// DPI and window scale, floored at 100, snapped to the step, capped at the manual bounds.
//
// Text stays crisp across a step because SetUIScale re-rasterizes at device size and blits stay device-exact
// (MessageRaster.DrawScaled, see deviceExact). Image surfaces and procedural textures (colorwheel.go, shapemask.go,
// gentex.go) have no higher-resolution source and stretch.
func (a *App) SetAutoScaleFromWindow(winW, winH int32) {
	if !a.d.Prefs.Load().UIScaleAutoOn {
		return // manual scale governs; the detected value is unused
	}
	winPct := 100
	if winW > 0 && winH > 0 {
		// The more constrained axis, so a portrait window can't push the UI past its width and clip fixed-size widgets.
		winPct = min(int(winW)*100/autoScaleRefWidth, int(winH)*100/autoScaleRefHeight)
	}
	pct := max(100, a.dpiScalePct, winPct)
	pct = pct / config.UIScaleStepPercent * config.UIScaleStepPercent
	pct = value.Clamp(pct, config.MinUIScalePercent, config.MaxUIScalePercent)
	if pct != a.detectedScalePct {
		a.detectedScalePct = pct
		a.ctx.SetUIScale(a.UIScale()) // mouse unprojection follows immediately
	}
}

// setTheater flips the borderless viewport-only mode; every caller is on the render thread, so the SDL call is legal.
func (a *App) setTheater(on bool) {
	a.theaterOn = on
	if on {
		// No toolbox draws in theater, so clear the pieces panel and pin: an invisible armed panel would swallow
		// clicks.
		a.toolboxPieces, a.toolboxPinned = false, false
	}
	if a.ctx.win != nil {
		a.ctx.win.SetBordered(!on)
	}
}

// setDND flips Do Not Disturb, saving it only when the user opted into persistence; the default clears every launch.
func (a *App) setDND(on bool) {
	a.dndOn = on
	if a.d.Prefs.Load().DNDPersist {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {
			sn.
				DNDSaved =

				on
		})
	}
}

// saveLayout persists the courtroom layout scales through the debounced saver.
func (a *App) saveLayout() {
	a.d.Prefs.SetLayoutScales(a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct)
	oocPct := a.oocPct
	a.d.Prefs.Update(func(sn *config.Settings) {
		sn.OOCScalePct = oocPct
	})
}

func (a *App) inputFieldH() int32 {
	return fieldH * int32(a.inputPct) / DefaultScalePct
}

// ensureCharLower keeps the lowercase name cache in sync with the char list.
func (a *App) ensureCharLower() {
	if a.sess == nil || len(a.charLower) == len(a.sess.Chars) {
		return
	}
	a.charLower = make([]string, len(a.sess.Chars))
	a.charHoverIDs = make([]string, len(a.sess.Chars))
	for i := range a.sess.Chars {
		a.charLower[i] = strings.ToLower(a.sess.Chars[i].Name)
		a.charHoverIDs[i] = charHoverIDPrefix + a.sess.Chars[i].Name
	}
}

// charHoverIDPrefix namespaces the grid's HoverPreview ids: the kit keys dwell state by id across the frame,
// so a bare character name could collide with other widgets built from it.
const charHoverIDPrefix = "char:"

// charHoverID is char i's cached HoverPreview id. Out of range it rebuilds rather than panic in a draw loop.
func (a *App) charHoverID(i int) string {
	if i >= 0 && i < len(a.charHoverIDs) {
		return a.charHoverIDs[i]
	}
	if a.sess == nil || i < 0 || i >= len(a.sess.Chars) {
		return charHoverIDPrefix
	}
	return charHoverIDPrefix + a.sess.Chars[i].Name
}

// charSelectTitle is the char-select heading, rebuilt only when the server name changes.
func (a *App) charSelectTitle() string {
	if a.charSelTitle != "" && a.charSelTitleFor == a.serverName {
		return a.charSelTitle
	}
	a.charSelTitleFor = a.serverName
	a.charSelTitle = charSelectHeading
	if a.serverName != "" {
		a.charSelTitle = charSelectHeading + " — " + a.serverName
	}
	return a.charSelTitle
}

// Background runs the per-frame engine work without drawing, called instead of Frame while minimized.
func (a *App) Background(dt time.Duration) {
	// Restamp the frame clock: Frame is the only other place it is set, so minimizing would freeze every a.now()
	// deadline, including the music-await release in settleAwaitedMusic, and a failed music fetch would stay silent
	// forever.
	a.frameNow = time.Now()
	a.pumpConnection()
	a.pumpBackgroundTabs()
	// Drain the OOC queue here as well as in Frame: producers keep queueing while minimized, and flushing them as one
	// burst on restore reads as OOC flooding, which servers answer with a silent kick.
	a.processOOCQueue()
	a.drainWarnings()
	a.pollThemesFolder()
	a.drainMusicFailures() // transient music-fetch failures → jukebox warn line (§1.1)
	// Auto-reconnect is deliberately not polled here: the retry fires only from Frame, so a drop while minimized waits
	// at the lobby instead of silently reconnecting and stranding the user at char select.
	a.pollCharINI()    // drain the async char.ini result here too, so the emote list appears at idle=0 (a skipped courtroom frame never reaches the draw-time poll)
	a.pollLogBrowser() // same for the log browser's off-thread scope load (session list + log area) — else it stays blank at idle=0 until cursor motion
	a.pollUpdate()     // and the self-update result — else "Downloading…" never flips to "Restart to apply" at idle=0 until cursor motion
	if a.room != nil {
		a.room.Update(dt)
		// Re-derive the duck here too, or a message finishing while minimized leaves music quiet until restore.
		a.rederiveMessageDuck()
		a.noteOverlayStart()
	}
	a.d.Audio.Frame()
	a.settleAwaitedMusic() // delivery-time cross-tab un-duck: lift once the awaited track is what's playing
	a.d.Pump.Frame()
	// After the upload pump, so the touch wins recency over this tick's upload burst, as on a drawn frame.
	a.keepActiveAssetsWarm()
	a.keepSceneAssetsWarm() // the un-drawn stage must survive the burst too
	a.d.Store.DrainDestroyQueue()
}

// noteOverlayStart wakes the renderer the moment a screen effect is triggered while nothing is drawn.
// Pacing is discovered at draw sites, so a parked event-driven loop would otherwise sleep out its idle period (forever
// at idle=off). Scene.Overlay.Gen bumps on every trigger, including a repeat of the same effect, which Base would miss.
func (a *App) noteOverlayStart() {
	if gen := a.room.Scene.Overlay.Gen; gen != a.drawnOverlayGen {
		a.drawnOverlayGen = gen
		a.NoteDeadline() // damage → the main loop renders on the very next pass
	}
}

// rederiveMessageDuck dips music to duckMusicPercent while a message is on stage, touching volumes only when the state
// flips. Callers must hold a.room != nil. Shared by Frame and Background so a message ending while minimized can't
// stick.
func (a *App) rederiveMessageDuck() {
	p := a.room.Phase()
	wantDuck := p != courtroom.PhaseIdle && p != courtroom.PhaseLinger && a.d.Prefs.Load().MusicDuckingOn
	if wantDuck != a.musicDucked {
		a.musicDucked = wantDuck
		a.applyAudioVolumes()
	}
}

// keepActiveAssetsWarm keeps the selected emote's buttons and the char icon in T1 while minimized, when no render
// touches them. Background-only: the URL build and map ops would break the 0-alloc render path.
func (a *App) keepActiveAssetsWarm() {
	if a.room == nil || a.sess == nil {
		return
	}
	me := a.activeCharName()
	if me == "" {
		return
	}
	// A new character or emote warms different bases, so the per-base request counters start over.
	if me != a.activeWarmChar || a.emoteIdx != a.activeWarmIdx {
		a.activeWarmChar, a.activeWarmIdx = me, a.emoteIdx
		clear(a.activeWarmDemands)
	}
	if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
		a.warmBase(a.urls.EmoteButton(me, a.emoteIdx+1, true), assets.AssetTypeEmoteButton)  // selected "on" image
		a.warmBase(a.urls.EmoteButton(me, a.emoteIdx+1, false), assets.AssetTypeEmoteButton) // "off" fallback
	}
	a.warmBase(a.urls.CharIcon(me), assets.AssetTypeCharIcon)
}

// The scene-heal futility budget, shared by keepSceneAssetsWarm, healScenery and keepActiveAssetsWarm. Two bounds,
// because a single counter is burned by one slow load:
//   - warmMaxHealChurns bounds landed-then-evicted cycles: the working set exceeds the T1 main tier and each heal
//     evicts another warm page, burning a whole core. Only a scene change fixes that, so stop quickly.
//   - warmMaxHealAsks bounds requests without a landing: a slow origin, a decoding page, a permanent 404. Roomier,
//     since repeats are cheap, and a landing resets it. An in-flight fetch still delivers, so a slow load can't strand
//     a base.
//
// Both reset when the warm set changes or a new message begins.
const (
	warmMaxHealChurns = 3
	warmMaxHealAsks   = 8
)

// sceneHealState is one warm base's heal history within its warm-set epoch.
type sceneHealState struct {
	asks         int  // demands since the base last landed
	churns       int  // landed-then-evicted-again cycles
	seenResident bool // the base landed at some point since the last demand
}

// sceneHealAllowed checks and counts one live-scene re-request against the shared budget. healScenery and
// keepSceneAssetsWarm share it because they heal the same bases and either alone could sustain the churn.
func (a *App) sceneHealAllowed(base string) bool {
	st := a.sceneWarmDemands[base]
	if st.seenResident {
		// It landed since the last request and is missing again: a real eviction cycle.
		// A set seenResident implies a prior write, so the map exists.
		st.churns++
		st.asks = 0
		st.seenResident = false
		a.sceneWarmDemands[base] = st
	}
	// Exactly warmMaxHealChurns cycles get a heal; warmMaxHealAsks requests may run without a landing between.
	if st.churns > warmMaxHealChurns || st.asks >= warmMaxHealAsks {
		return false
	}
	st.asks++
	if a.sceneWarmDemands == nil {
		a.sceneWarmDemands = make(map[string]sceneHealState) // bounded: scene bases only, reset per scene
	}
	a.sceneWarmDemands[base] = st
	return true
}

// markSceneResident records that a requested base is resident, resetting the asks bound and arming the churn bound.
// Writes nothing for bases never requested or already marked.
func (a *App) markSceneResident(base string) {
	st, ok := a.sceneWarmDemands[base]
	if !ok || (st.seenResident && st.asks == 0) {
		return
	}
	st.seenResident = true
	st.asks = 0
	a.sceneWarmDemands[base] = st
}

// warmBase keeps base resident across a minimized upload burst: touch it if in T1, else re-request, since a Get can't
// bring back an evicted page. A plain asks bound suffices: buttons and icons live in the small-UI shield tier where
// eviction cycling can't happen, so the only futile case is a permanent 404.
func (a *App) warmBase(base string, t assets.AssetType) {
	if base == "" {
		return
	}
	if a.d.Store.Contains(base) {
		a.d.Store.Get(base)
		if len(a.activeWarmDemands) != 0 {
			delete(a.activeWarmDemands, base) // it landed: re-arm the heal budget
		}
		return
	}
	if time.Since(a.activeWarmLastDemand) < sceneWarmRedemandEvery ||
		a.activeWarmDemands[base] >= warmMaxHealAsks {
		return
	}
	if a.activeWarmDemands == nil {
		a.activeWarmDemands = make(map[string]int) // ≤3 keys: the two button spellings + the icon
	}
	a.activeWarmDemands[base]++
	a.activeWarmLastDemand = time.Now()
	a.d.Manager.Prefetch(base, t, network.PriorityHigh)
}

// sceneWarmRedemandEvery throttles re-requests across the scene so the per-frame warm can't spam the pool during
// streaming. The message's own HIGH prefetch stays the loader; this is only the net for evicted bases.
const sceneWarmRedemandEvery = 250 * time.Millisecond

// keepSceneAssetsWarm touches the live stage's textures to the MRU end every tick: resolve() only re-Gets on a store
// generation change, so after a stable stretch one upload burst could evict the picture on screen.
// Runs after Pump.Frame on both drawn and minimized paths, so recency beats this tick's burst.
func (a *App) keepSceneAssetsWarm() {
	if a.room == nil && !(a.replaying && a.replayRoom != nil) {
		return // no stage on screen (lobby) — nothing to pin
	}
	sc := a.renderScene()
	// Reset the counters when a new message or room swaps the warm set, and on a new message with an unchanged set:
	// begin() skips its HIGH prefetch for resident bases, so if evicted a moment later they would have no loader.
	warmSet := [6]string{sc.BackgroundBase, sc.DeskBase,
		sc.Speaker.Active, sc.Speaker.IdleBase, sc.Pair.Active, sc.ChatSkinBase}
	if warmSet != a.sceneWarmSet {
		a.sceneWarmSet = warmSet
		clear(a.sceneWarmDemands)
	}
	if a.room != nil {
		if p := a.room.Phase(); p != a.sceneWarmPhase {
			if a.sceneWarmPhase == courtroom.PhaseIdle {
				clear(a.sceneWarmDemands) // idle → ceremony: a new message began
			}
			a.sceneWarmPhase = p
		}
	}
	now := time.Now()
	throttleOpen := now.Sub(a.sceneWarmLastDemand) >= sceneWarmRedemandEvery
	demanded := false
	warm := func(base string, t assets.AssetType) {
		if base == "" {
			return
		}
		if a.d.Store.Contains(base) {
			// Touch makes it the last to evict; a requested base being resident is also what resets the heal budget.
			a.d.Store.Get(base)
			a.markSceneResident(base)
			return
		}
		// A desk that 404'd conclusively can never become Contains and would re-request every tick. The courtroom's
		// per-message Prefetch is the deliberate retry. Only sprites and desks are ever MarkMissing'd, so no other kind
		// needs this.
		if t == assets.AssetTypeDeskOverlay && a.d.Store.IsMissing(base) {
			return
		}
		// Evicted or never landed: re-request, throttled scene-wide and bounded per base (sceneHealAllowed); unbounded,
		// an over-budget scene re-decodes and evicts forever at ~4 cycles/sec, burning a whole core.
		//
		// Char sprites use the full courtroom.SpriteHealAlts chain: a Warning (Store.MarkMissing, placeholder flash)
		// must fire only on full chain exhaustion, or a sprite existing under only the bare or folder spelling flashes
		// the placeholder. missChainFor keys on exact chain identity too. Others have no alternate spelling, so a plain
		// Prefetch.
		if throttleOpen && !demanded && a.sceneHealAllowed(base) {
			if t == assets.AssetTypeCharSprite {
				a.d.Manager.PrefetchChain(base, courtroom.SpriteHealAlts(base), t, network.PriorityHigh) // AssetType: CharSprite (bare + folder, so no spelling ever false-flags missing)
			} else {
				a.d.Manager.Prefetch(base, t, network.PriorityHigh)
			}
			demanded = true
			a.sceneWarmLastDemand = now
		}
	}
	warm(sc.BackgroundBase, assets.AssetTypeBackground)
	if sc.ShowDesk {
		warm(sc.DeskBase, assets.AssetTypeDeskOverlay)
	}
	if sc.Speaker.Visible {
		warm(sc.Speaker.Active, assets.AssetTypeCharSprite)
		if sc.Speaker.IdleBase != sc.Speaker.Active {
			warm(sc.Speaker.IdleBase, assets.AssetTypeCharSprite)
		}
	}
	if sc.PairActive {
		warm(sc.Pair.Active, assets.AssetTypeCharSprite)
	}
	warm(sc.ChatSkinBase, assets.AssetTypeMisc)
}

// frameCrashLog writes an unrecovered frame panic and full stack to recordings\scene-maker-crash.log, then re-panics
// so behaviour is unchanged.
func (a *App) frameCrashLog() {
	if r := recover(); r != nil {
		writeCrashLog("UNRECOVERED frame panic: ", r)
		panic(r)
	}
}

// SetPump injects the upload pump, built after App for the liveness probe.
func (a *App) SetPump(p *render.Pump) { a.d.Pump = p }

// Screen returns the active screen.
func (a *App) Screen() Screen { return a.screen }

// Room exposes the courtroom (nil before joining).
func (a *App) Room() *courtroom.Courtroom { return a.room }

// IsLiveBase reports whether base belongs to the on-screen message, which bypasses the upload budget.
func (a *App) IsLiveBase(base string) bool {
	if a.room == nil {
		return false
	}
	sc := &a.room.Scene
	switch base {
	case sc.BackgroundBase, sc.DeskBase, sc.ShoutBase,
		sc.Speaker.Active, sc.Pair.Active:
		return true
	}
	return false
}

// IsLiveStageBase reports whether base is a drawn stage layer, deciding what the held-frame bridge keeps (onEvict).
// Speaker and pair count: drawSprite's lastGood equals the evicted base, so it could not otherwise avoid a black flash.
// Render thread only.
func (a *App) IsLiveStageBase(base string) bool {
	if base == "" || (a.room == nil && !(a.replaying && a.replayRoom != nil)) {
		return false
	}
	sc := a.renderScene()
	switch base {
	case sc.BackgroundBase:
		return true
	case sc.DeskBase:
		return sc.ShowDesk
	case sc.Speaker.Active:
		return sc.Speaker.Visible
	case sc.Pair.Active:
		return sc.PairActive
	}
	return false
}
