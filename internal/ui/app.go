package ui

import (
	"context"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/hwid"
	"github.com/SyntaxNyah/AsyncAO/internal/metrics"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/presence"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
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
	// ScreenServerHelp is the legacy-server-owner upgrade guide (reached
	// from the lobby's NOT SUPPORTED notice).
	ScreenServerHelp
	// ScreenChangelog is the "What's New" version-history view (reached from the
	// lobby top bar); it renders the embedded CHANGELOG.md.
	ScreenChangelog
	// ScreenLogs is the transcript log browser / search (reached from the lobby
	// top bar and Extras); it reads logs/<server>/*.log written by detailed logging.
	ScreenLogs
	// ScreenHelp is the newcomer Help screen (glossary of AO terms + a plain-English
	// privacy explainer), reached from the lobby top bar and the courtroom Extras menu.
	ScreenHelp
)

const (
	lobbyRefreshTimeout = 15 * time.Second
	logLines            = 8
	logLineMax          = 120 // single-line UI (toasts/warnings) — NOT the IC log
	// icLineCap bounds ONE stored IC entry (hostile-server guard). Long lines
	// word-WRAP at draw time (icWrapped, up to 16 rows) instead of the old
	// 120-char "…" truncation — real IC messages (≤256 on the wire) never cut.
	icLineCap = 1024
	// icLogCap sizes the IC scrollback (a casing session's worth; ~100 KiB
	// worst case — the log is now scrollable/searchable/exportable).
	icLogCap = 1024

	// areaLogCacheMax bounds how many visited areas keep their own IC scrollback
	// (per-area scrollback, opt-in) — oldest evicted FIFO (rule §17.4).
	areaLogCacheMax = 64

	// charIconWarmup caps the connect-time speculative icon burst. Servers
	// with 4000+ characters exist; blasting them all floods the low lane
	// (lowLaneCap 256) and sheds nearly everything for zero benefit. Past
	// the warmup, the visible-demand path in drawCharCell fetches exactly
	// what's on screen.
	charIconWarmup = 128
	// charIconAskPerFrame bounds demand submissions per frame from the char
	// grid — the render thread must never flood (or block on) the pool.
	charIconAskPerFrame = 32
	// charIconRetryInterval spaces re-asks for a visible icon that hasn't
	// landed yet. Shed low-lane jobs are never re-run by the pool, so the
	// grid re-demands on this cadence until the texture arrives (or keeps
	// hitting the client's 404 cache, which never re-probes inside its TTL).
	charIconRetryInterval = 2 * time.Second

	// warnShowDuration keeps a missing-asset warning readable without
	// turning the HUD into a ticker.
	warnShowDuration = 12 * time.Second

	// iniswapFileName is the server-curated custom character list at the
	// asset origin root: one character folder name per line. Characters
	// here stream like list characters but never occupy a server slot.
	iniswapFileName = "iniswap.txt"
	// iniswapListCap bounds the parsed list (rule §17.4: no unbounded
	// anything — a hostile txt cannot balloon memory or the ask pacer).
	iniswapListCap = 4096
	// iniswapFetchTimeout caps the txt download.
	iniswapFetchTimeout = 15 * time.Second

	// keepalivePeriod matches AO2-Client's CH ping timer (courtroom.cpp
	// keepalive_timer->start(45000)): servers idle-kick silent clients,
	// which used to hit us whenever the window sat minimized.
	keepalivePeriod = 45 * time.Second

	// restoreDialTimeout bounds each restore-on-launch reconnect so a dead
	// remembered server can't freeze boot (manual Join keeps Dial's full 10s).
	restoreDialTimeout = 4 * time.Second

	// maxDescLines caps the expanded lobby description box.
	maxDescLines = 6
	// spriteOvCap bounds the client-side sprite override table.
	spriteOvCap = 128

	// debugLogCap bounds the in-app failure log (rule §17.4: nothing is
	// unbounded); the oldest lines fall off.
	debugLogCap = 120
	// debugOverlayLines is how many of the newest log lines the overlay
	// draws (the full ring stays browsable in Settings).
	debugOverlayLines = 10
	// themeHealPeriod paces re-loading the theme chatbox after T1 evicted
	// it (same trick as healScenery: one ask per period, never a flood).
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
	// Presence is the OPTIONAL Discord Rich Presence client (nil in
	// tests; never required to build or run — see internal/presence).
	Presence *presence.Client
	// Profiler is the 1 Hz sampler the F3 perf HUD reads (nil in tests).
	Profiler *metrics.Profiler
	// MasterURL is the server-list endpoint.
	MasterURL string
	// ConfigQuarantine, when non-nil, means the preferences file was corrupt
	// at startup and was renamed aside (defaults are in effect). NewApp raises
	// a one-time startup notice from it. Nil on a clean/first-run load.
	ConfigQuarantine *config.Quarantine
}

// App is the whole client UI state machine. Render thread only.
type App struct {
	d   Deps
	ctx *Ctx

	screen     Screen
	prevScreen Screen // for settings/about back navigation

	// --- connection lifecycle (lobby-global; deliberately NOT in sessionState) ---
	// These outlive any single session: the lobby shows the last disconnect
	// reason and a one-click Reconnect, and auto-reconnect retries ACROSS the
	// very resetSessionState() that Disconnect runs. They must live on App, not
	// sessionState — a past refactor swept them into sessionState, so Disconnect
	// wiped them mid-teardown and silently killed the reason text, the Reconnect
	// button and the backoff counter. Keep them here.
	connErr      string // last lobby error/notice (disconnect reason, dial failure, …)
	lastConnName string // M2: the server we were dropped from, for one-click Reconnect
	lastConnURL  string // its ws URL (serverKey); set on connect, re-captured at Disconnect
	// M2 auto-reconnect: after an unexpected drop, retry lastConnURL with backoff.
	// autoReconnectAt is the next attempt (zero = not retrying); pollAutoReconnect
	// fires it from the frame loop (a single time compare when idle — 0 per-frame
	// cost). autoReconnectMsg is the cached lobby status (rebuilt per attempt only).
	autoReconnectAt    time.Time
	autoReconnectTries int
	autoReconnectMsg   string
	// deliberateClose marks the CURRENT teardown as user-initiated (Disconnect
	// button, tab close, quit, rehearsal end) so the closed-channel/SendErr drop
	// paths suppress auto-reconnect. Today Disconnect() nils a.conn synchronously,
	// so the socket-close branch is only reached on genuinely unexpected drops —
	// this flag is defense-in-depth (belt-and-suspenders against a future refactor
	// that pumps between conn.Close() and the nil-out) and the explicit intent
	// signal shouldAutoReconnect reads. Set by the deliberate paths, cleared on a
	// fresh connect.
	deliberateClose bool

	// --- lobby state ---
	masterEntries []network.ServerEntry // raw master list (no favorites)
	servers       []network.ServerEntry
	lobbyStatus   string
	lobbyFetching bool
	lobbyResult   chan lobbyFetch
	directInput   string
	directSecure  bool
	directSave    bool
	lobbyScroll   int32
	// AsyncAO Server Phone Book: a dedicated lobby page over the user's saved
	// (favorite) servers — manual add + connect, persisted in Favorites (survives
	// Reset, cleared by Wipe), with its own export/import.
	phoneBookPage bool
	pbName, pbURL string // phone-book "add server" form
	// click-to-expand selection: first click opens the full description
	// under the row, a second click on the same row joins.
	selServer  int
	descLines  []string
	descLinks  []string // clickable URLs found in the description
	helpScroll int32    // ScreenServerHelp scroll position
	// Ping-sort (M7, opt-in via the lobby "Ping" button): pings holds each
	// joinable server's TCP-connect RTT by URL (0 = unprobed, <0 = unreachable);
	// pingMode sorts the lobby by it; pinging gates a re-probe; pingGen drops
	// stale results after a re-probe. A bounded worker pool feeds pingResult.
	pings    map[string]time.Duration
	pingRes  chan pingResult
	pingMode bool
	pinging  bool
	pingGen  int

	// #128 connection-quality chip (optional, off by default). A single background goroutine
	// pings the ACTIVE conn and stores the round-trip time; the frame loop (updatePingLoop)
	// starts/stops/retargets it as the active conn changes (connect / tab switch / reconnect /
	// disconnect) or the chip toggles. pingRTT is the only field the goroutine touches (atomic,
	// read by the render thread); the rest are render-thread-only. When the chip is off there's
	// no goroutine at all, so it's truly zero-cost by default.
	pingRTT     atomic.Int64       // round-trip nanoseconds (0 = unknown); goroutine→render
	pingConn    *protocol.Conn     // the conn the live loop targets (nil = no loop)
	pingCancel  context.CancelFunc // stops the live loop
	pingLabel   string             // cached tooltip text, rebuilt only when the ms bucket changes
	pingLabelMs int                // the ms the cached label was built for (-1 = none)

	// --- the active server session (every per-server field) ---
	// sessionState is EMBEDDED so the whole session parks/restores as one
	// struct move when tabs switch — see tabs.go. Field names stay
	// promoted, so call sites read exactly as before tabs existed.
	sessionState

	// --- global per-frame budgets / transient UI not tied to a server ---
	iconAskBudget int // per-frame icon demand allowance, reset each Frame
	// frameNow is this frame's single clock snapshot: animation clocks
	// (theme art, previews, splashes) read it instead of hitting the OS
	// clock per draw site.
	frameNow time.Time
	// defaultOOC is the sticky AsyncAO<n> fallback OOC name, minted once
	// per run on first use (commands/macros need SOME name to send).
	defaultOOC string
	// nameOpts caches the courtroom name-picker dropdown options ("▾" then the
	// saved showname presets); nameOptsLen is the preset count it was built at, so
	// the per-frame chat row never re-copies the list (ensureNameOpts).
	nameOpts    []string
	nameOptsLen int
	// macroBind ≥ 0 while the settings macro editor captures a key.
	macroBind int
	// holdKeyArmed: the Settings hold-to-clear rebind is capturing the next key.
	holdKeyArmed bool
	// voicePTTBindArmed: the Voice settings push-to-talk rebind is capturing a key.
	voicePTTBindArmed bool
	// F3 perf HUD: toggle + the frame-time ring it graphs.
	perfHUD     bool
	frameDts    [perfHUDFrames]float32 // milliseconds
	frameDtIdx  int
	previewBase string
	previewFor  string    // base the preview clock was started for
	previewAt   time.Time // loop anchor — animated previews play, not freeze
	previewZoom int       // magnifier factor (1 = fit; >1 shows a cursor-panned window)
	// Try-before-wear: cycle a previewed (non-worn) wardrobe character's
	// emotes in the preview box. previewChar guards a one-shot char.ini parse;
	// the capped anim/label slices drive the ‹ › buttons and arrow keys.
	previewChar     string
	previewAnims    []string
	previewLabels   []string
	previewEmoteIdx int

	// --- async result channels (App-global plumbing; payloads carry the
	// serverKey they were fetched for, and polls drop mismatches so a tab
	// switch can never land another server's data) ---
	charINIres chan charINIFetch
	// previewEmoteRes carries a previewed character's parsed emote list for
	// the try-before-wear cycle (parsed off-thread, key+char guarded).
	previewEmoteRes chan previewEmoteFetch
	// icLogFiltered cache: rebuilt only when the log or the query moved
	// (a 1024-line scan + slice alloc per frame otherwise).
	icFilter      []int
	icFilterSeq   uint64
	icFilterQuery string
	// IC wrapped-rows cache (playtest: "make the log break lines
	// according to its size"): filtered entries wrap to the list width;
	// rebuilt only when the log, query, width, or font scale moved.
	icWrap      []icWrapLine
	icWrapSeq   uint64
	icWrapQuery string
	icWrapW     int32
	icWrapPct   int
	icWrapGen   int  // font chain generation baked into the wrap
	icWrapStamp bool // ICTimestamps state baked into the wrap (toggling rewraps)
	// OOC wrapped-lines cache: long entries (MOTDs) wrap instead of
	// truncating; rebuilt only when the log, width, or font scale moved.
	// (Like the IC caches above, these stay App-global: their keys carry
	// the per-tab seq, so a tab switch is just a cache miss.)
	oocWrap     []string
	oocWrapName []string // parallel to oocWrap: speaker to tint on each entry's FIRST display line ("" otherwise)
	oocWrapURL  []string // parallel to oocWrap: the entry's first link on each of its display lines ("" = none) — so a URL the wrap hard-split still opens whole (mirrors icEntry.url)
	oocWrapCont []bool   // parallel to oocWrap: true = a wrap continuation row (hanging indent); a paragraph's own newline starts a fresh row, not a continuation
	oocWrapSeq  uint64
	oocWrapW    int32
	oocWrapPct  int
	oocWrapMask bool // streamer-mode masking baked into the cache
	oocWrapGen  int  // font chain generation baked into the wrap

	// last missing-asset warning surfaced to the user (spec §4).
	warnLine string
	warnAt   time.Time

	// dndOn is Do Not Disturb: session-only (resets on launch by design, so it
	// can't silently kill callwords days later) — silences the personal-ping
	// alerts (callword + friend) while leaving duty signals (modcalls, case
	// alerts, server notices) through. Gated in checkCallwords + signalFriend.
	dndOn   bool
	loginAt time.Time // when a login flow last fired; a brief callword grace so the server's login replies don't self-ping

	// --- debug overlay (Settings toggle): bounded failure log ---
	debugLog    []string // ring of stamped failure lines, debugLogCap max
	debugLast   string   // last raw line, for consecutive-duplicate collapse
	debugRepeat int      // how many times debugLast repeated
	lastPktHdr  string   // newest server packet header (health line)
	lastPktAt   time.Time

	// --- packet inspector (#333): Debug panel's live packet ring + counts ---
	pkts    packetLog      // bounded ring + per-header in/out counts (active conn)
	pktConn *protocol.Conn // the conn pkts currently reflects; a change resets it

	// --- server format manifest (extensions.json autodetect) ---
	manifestRes chan manifestFetch

	// --- asset-casing auto-detect (OFF unless AssetCharCasing == CharCaseAuto) ---
	charCaseLearned  map[string]uint8 // host → learned character-folder casing
	casingProbing    bool             // a casing probe is in flight
	casingProbedHost string           // host already probed this session (probe once per host)
	casingRes        chan casingProbeResult

	// --- font override pipeline (file bytes read off-thread) ---
	fontRes chan fontLoad
	// themeFontFile is the active theme's bundled font path (.ttf/.otf), resolved
	// off-thread on theme apply; applyFontConfig uses it when no manual font / the
	// dyslexia toggle is set (#6).
	themeFontFile string
	// Color-emoji fallback face: the system emoji font (e.g. Segoe UI Emoji) is read
	// off-thread the FIRST time a message needs it (emojiLoadStarted gates the one
	// read), landing on emojiFontRes → ctx.SetEmojiFont. Lazy so a user who never
	// types emoji never pays the 11.9 MB read.
	emojiFontRes     chan []byte
	emojiLoadStarted bool
	// Broad-Unicode TEXT fallback: system fonts (Segoe UI, Ebrima, Nirmala, Segoe UI
	// Symbol) covering scripts the embedded font lacks (Cyrillic→Tifinagh→Indic→
	// symbols). Read off-thread the FIRST time a message has a non-ASCII rune
	// (fallbackLoadStarted gates the one read), landing on fallbackFontRes →
	// ctx.SetFallbackFonts. Lazy so a pure-ASCII session never loads them and the
	// single-font fast path (pickSet len==1) is untouched — zero perf change.
	fallbackFontRes     chan [][]byte
	fallbackLoadStarted bool
	// CJK tier (Han/Kana + Hangul), read off-thread only when a CJK letter is drawn —
	// independent latch so the broad load can't swallow it (a Cyrillic name usually
	// comes first). 13-35 MB, so kept out of the common non-ASCII path.
	cjkFontRes     chan [][]byte
	cjkLoadStarted bool

	// --- log browser (ScreenLogs): search saved transcripts across servers ---
	// Global (not per-session): the loader runs off-thread, lands on logBrowserRes.
	logBrowser    logBrowserState
	logBrowserRes chan logBrowserLoad

	// --- case notebook loads (per-server; payload routes by key) ---
	notebookRes chan notebookLoad

	// Offset ghost editor (pair panel): transient drag state.
	ghostDrag  bool
	ghostPrev  bool // last frame's mouseDown (edge detect)
	ghostStart [2]int32
	ghostBase  [2]int

	// --- multi-server tabs (bounded maxTabs; see tabs.go) ---
	// tabs hold PARKED sessions; the active one lives in sessionState
	// above. activeTab indexes tabs, −1 = no active session (lobby).
	tabs      []*courtTab
	activeTab int
	// tabColors tints a tab chip by a palette index (#22), keyed by the tab's serverKey (stable
	// across park/activate), so colour-coding survives switching tabs. 0 = default (no tint).
	// Lazily inited, bounded by tabColorsCap. Ctrl+click a chip to cycle it.
	tabColors map[string]int
	// Tab drag-reorder: tabDragFrom is the chip armed on press (−1 = none),
	// tabDragging flips once the cursor passes tabDragThreshold (then a release
	// reorders instead of switching). tabPrevDown is this strip's own
	// mouse-held tracker (separate from the wardrobe's, since the strip draws
	// over every screen).
	tabDragFrom  int
	tabDragStart [2]int32
	tabDragging  bool
	tabPrevDown  bool
	// restoreQueue holds the tabs to reopen on launch (M7, opt-in): populated
	// once in NewApp when RestoreTabs is on, drained one reconnect per frame by
	// pumpTabRestore so the blocking dials never pile into a single boot freeze.
	restoreQueue []config.OpenTab
	// translogs are the detailed-transcript writers (opt-in), ONE PER SERVER:
	// logs/<server>/<session>.log, opened lazily on that server's first logged
	// message and closed at shutdown. Bounded by transcriptServerCap. nil/empty
	// = off / nothing logged yet.
	translogs map[string]*transcriptWriter

	// --- M13 self-update (one-shot launch check; see internal/update) ---
	// updateRes carries a newer release found by the off-thread probe; the
	// drain stores it in updateRel and auto-opens the What's New modal once.
	// updateChecked fires the probe EXACTLY ONCE on the first frame (after the
	// window is up) so the check never touches the boot critical path.
	updateRes       chan *update.Release
	updateChecked   bool
	updateRel       *update.Release
	updateShow      bool
	updateFenceOn   bool // WE hold the modal fence for the open What's New modal (released on close — see updateModalFence)
	updateScroll    int32
	updateChipLabel string
	// Self-update apply flow (the "Get the update" button → async download,
	// verify, staged swap): updateBusy while it runs, updateStaged once the new
	// binary is in place (restart pending), updateErr on failure. The goroutine
	// reports on updateApplyRes. Inert unless a stamped build found a release.
	updateApplyRes chan error
	updateBusy     bool
	updateStaged   bool
	updateErr      string
	relaunchOnExit bool // "Restart to apply": relaunch the new binary after a clean shutdown

	// --- IC/OOC log text selection (drag to highlight, Ctrl+C to copy) ---
	// Anchored to (wrapped-line index, rune offset in that line); see
	// logselect.go. logSelWhich names the owning log so only it highlights/
	// copies; logSelPressed is the once-per-frame press edge (both logs read
	// the same value, so whichever draws first can't steal the arm).
	logSelWhich    int
	logSelActive   bool
	logSelDragging bool
	logSelAnchor   selPoint
	logSelHead     selPoint
	logSelPressed  bool
	logSelPrevDown bool
	logSelFill     sdl.Color // configured highlight colour, cached per frame
	// Chatbox (in-viewport IC message) selection: drag across the message to
	// highlight the RANGE you want (the raster exposes per-line rune geometry
	// now — RuneAt/LineSpanX), double-click a word, triple-click the whole
	// message; Ctrl+C / right-click copies just the selection. chatSelA/B are
	// source-rune boundaries (anchor/head). Only the animated-text path
	// (msAnim, per-glyph motion) keeps the old whole-message behavior.
	chatSelActive   bool
	chatSelDragging bool
	chatSelPrevDown bool
	chatSelDownX    int32
	chatSelDownY    int32
	chatSelA        int
	chatSelB        int
	// Highlight-colour picker (Settings): the hue/sat wheel texture (built once)
	// and the in-progress hex field text.
	colorWheel     *sdl.Texture
	colorHex       string
	chromeEditSlot int    // which custom-chrome kit colour the wheel edits (0..6)
	chromeHexBuf   string // in-progress hex for the custom-chrome wheel's hex field
	// Mayo mascot portrait on the About page: a high-quality Catmull-Rom downscale
	// of the embedded art, uploaded once (lazily, render thread). Baked to the exact
	// PHYSICAL pixel size (logical × UI scale) so it draws 1:1 — crisp at any scale —
	// and rebuilt only when the UI scale changes. mayoTexFailed latches a failed
	// decode/upload so it isn't retried every frame. Zero per-frame cost. (mascot.go)
	mayoTex            *sdl.Texture
	mayoLogW, mayoLogH int32 // on-screen (logical) size; the texture itself is physical px
	mayoTexScale       int   // UIScale() the texture was baked at
	mayoTexFailed      bool
	// #234 pet-the-gopher easter egg: clicking the About portrait "pets" Mayo.
	// mayoPets is the session pet count; mayoPetAt drives the wiggle + floating
	// caption. Session-only — a bit of joy that resets each launch.
	mayoPets  int
	mayoPetAt time.Time
	// #234 "…and all of that": each pet also launches a bouncing, spinning Mayo
	// that roams the About screen DVD-logo style. Bounded pile; mayoRoamAt stamps
	// the last update so the motion is frame-rate independent.
	mayoRoamers []mayoRoamer
	mayoRoamAt  time.Time

	// spriteTintHex is the in-progress hex text for the solid sprite-tint field
	// (Settings); reflected from the pref when the field isn't focused.
	spriteTintHex string

	// lastOSToast rate-limits friend desktop toasts (osToastMinInterval).
	lastOSToast time.Time

	// lastModSFX debounces the mod-command feedback sounds per action (#60),
	// indexed by render.ModAction — collapses the OOC actor-confirm + area
	// broadcast burst into a single play. See modsfx.go.
	lastModSFX [3]time.Time

	// Scene recording (M16, see replay.go): when recActive, the event loop taps
	// the scene-mutating events into rec; recStart anchors the relative offsets.
	recActive bool
	rec       *sceneRecording
	recStart  time.Time

	// Instant replay (rolling clip buffer, see replay.go): when the opt-in pref is
	// on, every scene-mutating event is also stamped into replayBuf — a bounded
	// ring — so the clip key can save the last window WITHOUT a prior recording.
	// Allocated lazily on first capture, nilled when disabled or the origin changes
	// (a clip must never mix two servers' assets). replayBufW = next write slot,
	// replayBufN = entries held (<= cap), replayBufOrigin = the CDN they came from.
	replayBuf       []replayBufEntry
	replayBufW      int
	replayBufN      int
	replayBufOrigin string

	// Scene replay (M16 [2/2], see replay.go): while replaying, a throwaway
	// replayRoom (pointed at the recorded asset origin) is driven instead of the
	// live room and the viewport/renderScene read its scene. The driver feeds
	// replayEvents[replayIdx] into it whenever it returns to idle.
	replaying    bool
	replayRoom   *courtroom.Courtroom
	replayEvents []recEvent
	replayIdx    int
	replayName   string
	replayPaused bool            // player ⏸ — freeze the scene (Next/Restart still work)
	replayRec    *sceneRecording // the source, kept so ⏮ Restart can rebuild from the top
	// #70 auto-chapters: the jump list derived once per replay (bg/music/shout
	// beats) and whether its panel is open in the overlay player.
	replayChapters     []replayChapter
	replayChaptersOpen bool

	// Scene maker (M16 [3/3], see scenemaker.go): an in-app editor over the SAME
	// .aorec model — build a scene from scratch or edit a recording, preview it
	// through the replay engine, and Save a new .aorec. All state lives here and
	// is allocated only while makerOpen, so the maker costs nothing on the live
	// render path (it's drawn as a full-window overlay, like a replay).
	makerOpen       bool
	makerScene      *sceneRecording
	makerSel        int    // selected event index
	makerName       string // working filename stem (sanitized on Save)
	makerScroll     int32  // event-list scroll offset (px)
	makerPickerOpen bool   // the in-maker "Open a recording" list is showing
	makerExportOpen bool   // the in-maker "⚙ Export options" panel is showing
	makerTrimStart  int    // crop In point (event index), -1 = scene start
	makerTrimEnd    int    // crop Out point (event index), -1 = scene end
	// Timeline strip (#75): a proportional film-strip of the scene events, widths
	// from the recorded OffsetMs pacing. Reused per-frame layout buffers (segX/W
	// in px, dur in ms) so the maker draw stays alloc-free; horizontal scroll;
	// makerDragHandle is which crop handle is being dragged (0 none, 1 In, 2 Out);
	// makerPrevDown carries the mouse-press edge for grabbing a handle.
	// makerDragSeg is the segment being drag-reordered (-1 = none; 0 is a valid
	// index, so it MUST be initialised to -1); makerDragMoved marks the armed press
	// as a real drag once it crosses the threshold; makerDragX is the press-origin x.
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
	// makerPreviewRoom is a throwaway courtroom that renders the selected line
	// into the maker's live preview pane (the "studio" WYSIWYG). makerPreviewIdx
	// is the line it currently reflects, so the pane is rebuilt only when the
	// selection/scene changes. Driven by driveMakerPreview while makerOpen.
	makerPreviewRoom *courtroom.Courtroom
	makerPreviewIdx  int
	makerPreviewOrig string          // origin the preview room was built for (rebuild on change)
	makerPreviewKey  makerPreviewKey // visual identity of the previewed line (rebuild on change)
	// Self-contained archive (CDN-proof): export bundles a scene's assets into a
	// folder; replayBundled marks a replay whose assets stream from that folder
	// (Manager archive-source override) instead of the origin.
	replayBundled  bool
	makerExporting bool
	makerExportCh  chan string // archive-export goroutine → UI (result line)
	// Scene GIF export (gifexport.go): renders a scene offscreen into paletted
	// frames across several frame-loop ticks, then encodes off-thread. Allocated
	// only while exporting; zero cost on the live path otherwise.
	gifExporting bool
	gif          *gifExportJob
	gifResultCh  chan exportResult // off-thread encode → UI (result line + artifact path)

	// --- M5 background slideshow (idle ambiance, off by default) ---
	// While enabled AND the courtroom is idle, slideBG holds the current
	// rotation background URL ("" = not overriding). The viewport renders a
	// shallow Scene copy (slideScene) carrying that background, so the real
	// scene is never mutated and a live message reverts instantly.
	slideIdx   int
	slideAt    time.Time
	slideBG    string
	slideScene courtroom.Scene

	// --- applied theme (chatbox skin, splashes, bars, colors, sounds) ---
	// themeRes holds the newest off-thread theme load; gen ordering means a
	// slow stale load can never clobber a fresh one (rapid theme cycling
	// spawns several loads and completion order is not start order).
	themeRes     atomic.Pointer[themeApply]
	themeGen     atomic.Uint64
	themeHealAt  time.Time       // last eviction heal (themeHealPeriod pacing)
	themeTex     map[string]bool // theme:// stems resident in T1
	themeSounds  map[string]string
	themeChatbox bool // theme://chatbox resident (mirror of themeTex)
	themeMsgCol  sdl.Color
	themeHasMsg  bool
	themePalette theme.Palette // last theme's chrome palette, kept so a #M3 chrome-preset change can re-overlay it
	themeNameCol sdl.Color
	themeHasName bool
	// Theme courtroom geometry (courtroom_design.ini): design-space rects
	// + emote grid metrics; themeLay caches the window-scaled rects.
	// themeRectsOrig keeps the theme's PRISTINE geometry so the layout
	// editor's right-click/Reset can restore it under the user overrides.
	themeRects     map[string]theme.Rect
	themeRectsOrig map[string]theme.Rect
	themeEmoteCell [2]int
	themeEmoteGap  [2]int
	themeLay       themeLayoutCache

	// --- live layout editor (themed courtroom; overrides persist per theme) ---
	layoutEdit   bool
	editKey      string
	editDrag     int // 0 none, 1 move, 2 resize
	editPrev     bool
	editStart    [2]int32
	editBase     theme.Rect
	layoutSnap   bool // snap edits to a design-space grid (toggle in the editor)
	layoutAspect bool // lock the viewport (stage) to 4:3 while resizing it in the editor
	// Overlap cycling: Tab steps through the stack of boxes under the cursor (smallest first) so a
	// big box hidden under a small one is still reachable. editPickSig is the current stack's
	// fingerprint — the index resets when the stack changes.
	editPickIdx int
	editPickSig string
	// Layout-editor undo/redo: each edit snapshots the whole rect map (≈40
	// entries) before changing it; Ctrl+Z restores, Ctrl+Y redoes. Bounded.
	editUndo []map[string]theme.Rect
	editRedo []map[string]theme.Rect

	// --- classic-layout slot editor (the default/non-themed courtroom) ---
	// Mirrors the themed editor's feel, but edits SCREEN-space "slots" persisted
	// as window FRACTIONS (config.ClassicLayout) so they survive window resizes.
	// classicOv is an App-local lock-free snapshot read by slotRect on the render
	// path (the editor is the sole writer); slotReg is this frame's registered
	// slots, populated ONLY while editing so the common frame stays alloc-free.
	classicEdit     bool
	classicOv       map[string][4]float64
	classicOvLoaded bool
	// classicAnchor holds per-slot window pins (parsed from
	// config.ClassicAnchors alongside classicOv): a pinned slot's override
	// re-bases to pixel offsets from its window corner/centre on resize
	// instead of scaling with the fractions (classicanchor.go).
	classicAnchor map[string]anchorRef
	slotReg       map[string]slotInfo
	// dockLeftX caches the docked log/tab strip's left edge (rcol.X), refreshed by
	// drawCourtroom each frame. The floating server-tab strip centres its DEFAULT
	// position in the space LEFT of it (over the stage) so it no longer overlaps the
	// dock tabs (issue #2). Zero until the first courtroom frame ⇒ window-centre.
	dockLeftX        int32
	classicEditKey   string
	classicEditDrag  int // 0 none, 1 move, 2 resize
	classicEditStart [2]int32
	classicEditBase  sdl.Rect
	classicEditEdges uint8 // which edges a resize drags (edgeL/R/T/B bitmask); 0 mid-move
	classicEditPrev  bool  // press-edge latch (mouse-down rising edge)
	classicEditMoved bool  // a drag actually moved/resized (else a click persists nothing)
	// Undo/redo history of the whole override map (Ctrl+Z / Ctrl+Y), edit-mode only and
	// bounded by layoutUndoCap. classicPickIdx/Sig drive Tab-cycling the stack of slots
	// under the cursor so a big box hidden under a small one is still reachable.
	classicUndo    []map[string][4]float64
	classicRedo    []map[string][4]float64
	classicPickIdx int
	classicPickSig string
	// classicChromeBot is the bottom Y of the editor's stacked top chrome
	// (banner + tray + toolbox), recorded by drawClassicToolbox each edit
	// frame — slot name tags clamp below it so a box parked in the top strip
	// can't plaster its tag over the editor's own controls.
	classicChromeBot int32
	// alignGuides/alignScratch are the editor's per-drag-frame alignment
	// results and its reusable other-slots buffer (classicalign.go). Editor
	// path only; reused so a long drag allocates nothing after warm-up.
	alignGuides  []alignGuide
	alignScratch []sdl.Rect
	// layoutPresetName is the Settings name field for saving the current layout as a
	// named preset (#34, internal/ui/layoutpresets.go + the Theme settings section).
	layoutPresetName string
	// toolboxDrag* track a chip dragged out of the editor toolbox (#27 slice 2b): press
	// a chip and drag it onto the stage to SHOW that piece; a release without moving is a
	// plain hide/show toggle. toolboxDragID is "" when no chip drag is active.
	toolboxDragID    string
	toolboxDragStart [2]int32
	toolboxDragMoved bool
	// pathStroke is the in-progress freehand stroke for the Sprite Style "draw a path" box
	// (#34 B2): raw box-relative points captured while dragging, sampled to <=6 waypoints on
	// release. pathDrawing = mid-stroke; pathPrevDown is its press-edge latch. Bounded.
	pathStroke   []sdl.Point
	pathDrawing  bool
	pathPrevDown bool
	// themePages is the generation-keyed page cache for theme:// textures
	// (zero store locks while the generation is unchanged).
	themePages    map[string]*render.TexturePage
	themePagesGen uint64
	// themeAt anchors the looping animation clock for animated theme art
	// (chatbox/buttons/backdrops shipped as animated webp/gif/apng).
	themeAt time.Time
	// themeAppliedName is the last theme that actually landed —
	// ensureThemeForSession compares against it so per-server bindings
	// only trigger loads when something really changes.
	themeAppliedName string

	showUICfg bool // hide-chrome popup
	// showWidgets opens the "Extras" box: every AsyncAO feature an AO2 theme has
	// no button for. The themed Extras button opens it; so does the Extras
	// hotkey, which works in EITHER mode (in the classic path it's just a quick
	// launcher, since those features already have buttons). themedHintShown
	// gates the one-time hint that points players at it (legacy themes carry no
	// AsyncAO element keys).
	showWidgets     bool
	themedHintShown bool
	// Floating Extras box (floatbox.go): movable geometry. extrasPlaced=false
	// means "centered default until first dragged"; the rest tracks a title-bar
	// drag. (Position persistence lands in a later slice.)
	extrasBoxX, extrasBoxY     int32
	extrasPlaced               bool
	extrasDragging             bool
	extrasGrabDX, extrasGrabDY int32
	extrasPrevDown             bool
	// Torn-off widgets: each lives in its own small floating box you drag out
	// of the main grid, move freely, and close (which returns it to the grid).
	// Flags use bool+index so the &App{} zero value is "idle" (no ctor needed).
	extrasDetached       []detachedWidget // bounded: one box per widget at most
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
	// pairWin / modWin / cmWin / hkWin / evidWin are floating-window geometries
	// (floatwin.go) for the Pairing, Mod dashboard, CM, Hotkey-sheet, and Evidence
	// panels — each a movable/resizable, non-blocking box (chat stays live behind it)
	// rather than a modal. Geometry is global; open state is per-tab / per-flag.
	pairWin, modWin, cmWin, hkWin, evidWin, modcallWin, msgWin, voiceWin, banWin, debugWin floatWin
	// Voice chat (Nyathena VS_* relay): showVoice = the floating panel's open state
	// (global); membership/mic state is per-session (sessionState.voiceJoined/MicOn).
	showVoice bool
	// voiceAudio is the live capture/playback engine — non-nil ONLY while joined to
	// a voice channel (opt-in); nil = presence-only / not in voice (voiceaudio.go).
	voiceAudio *voiceEngine
	// micTest is the Settings → Voice "Test microphone" tool (#84): an independent
	// capture (+ optional sidetone playback) device, non-nil only while a test runs
	// (voicemictest.go). micTestSidetone remembers the "hear myself" toggle.
	micTest         *micTester
	micTestSidetone bool
	// showQuitConfirm: the "Quit AsyncAO?" dialog is up (Esc in the lobby, or a
	// quit hotkey). Skipped when QuitConfirmSkip ("don't ask again") is set.
	showQuitConfirm bool
	// Group Chat panel (messaging): floating, non-blocking, global (not per-session).
	// msgSel = selected conversation (partner char name); msgInput = compose buffer.
	showMessages   bool
	msgSel         string
	msgSelGroup    uint32 // selected group id (0 = a DM is selected via msgSel)
	msgGroupManage bool   // group view is showing the members / invite panel
	msgInput       string
	msgListScroll  int32
	msgGroups      map[uint32]*msgGroup // client-side group chats (keyed by group id)
	pendingInvites []groupInvite        // group invites awaiting Accept / Decline
	msgIconAsk     map[string]time.Time // paced char-icon demands for the messaging surfaces (bounded)
	// Remote-speaker char.ini metadata (blips + chatbox skin) — URL-keyed cache
	// + async fetch results (charmeta.go). Bounded; per-server by key structure.
	charMetaCache map[string]charMeta
	charMetaRes   chan charMetaFetch
	// #39 command palette (Ctrl+Space): open state, live query, keyboard selection.
	paletteOpen  bool
	paletteQuery string
	paletteSel   int
	// hkPrevDown is the hotkey sheet's own mouse-press edge: it draws over EVERY
	// screen (outside the courtroom box pass), so it can't share that pass's edge.
	hkPrevDown bool
	hkScroll   int32 // hotkey cheat-sheet scroll offset (it's a scrollable list now)
	// Multi-server split (pass 2a): splitTab pins a BACKGROUND tab whose live stage
	// renders in the RIGHT pane (splitRoom over that tab's own session + URL builder
	// on courtroom.NopAudio so only the focused left pane makes sound; splitVP over
	// the shared store — origin-qualified keys, so no texture clash) while you play
	// in the left. nil = no split; the single-pane path stays byte-identical.
	splitTab  *courtTab
	splitRoom *courtroom.Courtroom
	splitVP   *render.Viewport
	// Pinned-pane chat log: wrapped rows cached by (width, pinned log seq) so the
	// steady-state right-pane render is 0-alloc — rebuilt only when a message lands
	// or the pane resizes (the pinned "slow chat" rarely changes).
	splitWrapW    int32
	splitWrapSeq  uint64
	splitWrapRows []splitLogRow
	// The pinned server draws in a free-floating, movable/resizable "second client"
	// window (clientWin geometry; the floatWin pattern) overlaying the primary —
	// drag it anywhere, not a fixed split. clientHdr caches the title by server name
	// so the per-frame draw never re-concats (0-alloc, like the single-pane path).
	clientWin     floatWin
	clientHdr     string
	clientHdrName string
	// clientFull toggles the full-theme view (the WHOLE pinned client UI, view-only)
	// vs compact (stage + log + input). Full-theme renders the pinned drawCourtroom
	// into clientTex at the logical screen size (so the theme lays out exactly as the
	// primary), then shrink-blits it into the window — recreated only on a size change.
	// VIEW-ONLY by necessity: the kit is single-instance (fixed widget ids would
	// collide with the primary's, and a.screen / modal writes would leak), so input is
	// neutralized during that pass; you watch the whole theme and chat via the input
	// strip below it. Opt-in (default compact) — the primary path is untouched.
	clientFull bool
	clientTex  *sdl.Texture
	clientTexW int32
	clientTexH int32
	// pinnedPass is true ONLY during the full-theme drawCourtroom render of the
	// pinned client. Single-consumer App channel drains (pollCharINI) skip while it's
	// set, so the view-only pass can't eat an async result meant for the primary —
	// the pinned client is already fully loaded in its parked sessionState.
	pinnedPass bool
	// pendingControlSwap: a click on the floating client's view requests "control
	// this server" (make it the live, fully-interactive courtroom). Applied at the
	// NEXT frame's top — never mid-render — since it swaps the active session/room.
	pendingControlSwap bool
	// Full-theme view zoom/pan: clientZoom magnifies (1 = fit), clientPan{X,Y} is the
	// view centre in texture space (0..1) when zoomed. clientPanning tracks a drag.
	// clientZoomLbl memoizes the readout so the per-frame draw doesn't Sprintf.
	clientZoom                     float64
	clientPanX, clientPanY         float64
	clientPanning                  bool
	clientPanGrabX, clientPanGrabY int32
	clientPanBaseX, clientPanBaseY float64
	clientZoomLbl                  string
	clientZoomLblPct               int
	// theme-fit Custom preview (Settings → Theme): drag to pan the big preview.
	themeFitDrag      bool
	themeFitDragStart [2]int32
	themeFitDragBase  [2]int
	// Sprite-preview box: wheel zoom + left-drag to reposition. Handled centrally
	// (handlePreviewInput) before screens draw so it claims the wheel/press ahead
	// of the grid scroll and icon clicks underneath. previewFrameRect caches last
	// frame's box for that hit-test; the offset shifts it from the default corner.
	previewOffX, previewOffY int32
	previewDrag              bool
	previewDragMoved         bool
	previewDragStart         [2]int32
	previewDragBase          [2]int32
	previewFrameRect         sdl.Rect
	// Playtest sizing: every character previews at the same height (previewBaseH,
	// AO's native 192) regardless of source resolution; the corner grip resizes
	// (previewUserH, 0 = the default) and a caption line reports source × scale.
	previewUserH      int32
	previewResize     bool
	previewResizeFrom int32 // mouseY at grip press
	previewResizeBase int32 // previewUserH (resolved) at grip press
	// Close-on-leave travel state: previewTriggerRect is the cell that opened the box,
	// previewEntered latches once the cursor has reached the box. Together they let the
	// cursor cross the gap from the cell to the bottom-right box without it vanishing,
	// then close it the moment the cursor leaves the box (beta feedback).
	previewTriggerRect sdl.Rect
	previewEntered     bool
	previewPinned      bool   // right-click pins the sprite-preview box open until you close it (its x)
	vpExactBuf         string // edit buffer for the Settings exact-stage-width px field (commit on Enter)
	hidden             map[string]bool

	iniRes chan iniswapFetch

	// layout scales (percent; mirrors prefs, saved on change)
	vpPct, chatPct, boxPct, logPct, inputPct int
	oocPct                                   int // OOC log text size, independent of logPct (IC log)
	musicPct                                 int // music-list font scale, independent of the log so long titles can shrink without shrinking IC
	uiScalePct                               int // global renderer scale (manual)
	// Manual UI-scale slider COMMITS ON RELEASE: applying live rescales the slider
	// under the cursor (feedback loop → "super hard to use"). These hold the in-drag
	// preview value and drag state so the scale applies once, on mouse-up.
	uiScalePending  int
	uiScaleDragging bool
	// detectedScalePct is the display-DPI-derived scale (96 dpi = 100%),
	// snapped to the settings step; UIScale() prefers it while the
	// auto-HiDPI preference is on. 0 = detection unavailable.
	detectedScalePct int
	// dpiScalePct is the display-DPI-only component of the auto scale (floored at
	// 100); SetAutoScaleFromWindow combines it with a window-size factor each
	// frame so a maximized window fills out, and a resize recomputes cleanly.
	dpiScalePct int
	// theaterOn is the borderless viewport-only mode (Esc exits).
	// Deliberately session-only: it can never persist someone into a
	// chrome-less client across runs.
	theaterOn bool

	// --- jukebox (global DJ /play music-link library) ---
	// App-global like dlPaused and OUTSIDE sessionState: the library is shared
	// across every server and must survive disconnects and tab switches.
	juke         *config.Jukebox      // global store, loaded off-thread
	jukeRes      chan *config.Jukebox // off-thread load landing
	jukeIORes    chan string          // off-thread export/import result → in-app toast
	jukeCache    []config.Playlist    // rev-keyed Playlists() snapshot for drawing
	jukeCacheRev int64
	jukeOpen     int    // -1 = playlist list; else the open playlist index
	jukeSearch   string // filters playlists (top) or songs (inside one)
	jukeScroll   int32
	aboutScroll  int32 // About screen scroll offset (the page outgrew small windows)
	// About: the prose is reflowed to the current column width with WrapText and
	// cached here (keyed by width), so the wrap runs only on a resize, never per
	// frame (the page is off the hot path, but this repo keeps UI draws alloc-free).
	aboutFlatW int32           // content width the cache was built for (0 = none)
	aboutFlat  []aboutFlatLine // flattened, wrapped render lines
	// Changelog ("What's New" version history): same width-keyed reflow cache as
	// About — the embedded CHANGELOG.md is wrapped only on a resize, never per frame.
	changelogScroll int32
	changelogFlatW  int32
	changelogFlat   []aboutFlatLine
	// Help screen (glossary + privacy explainer): the same width-keyed reflow cache.
	helpTab       int
	helpDocScroll int32
	helpFlatW     int32
	helpFlat      []aboutFlatLine
	// Settings: the content region (right of the sidebar) the section/row helpers
	// draw into. Set once per frame by drawSettings; the helpers rebase their
	// pad-relative layout onto formX so every box lands inside the content card.
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

	// dlPaused is the download worker's pause flag — App-global (one worker at
	// a time) and OUTSIDE sessionState (which is copied per tab, and an atomic
	// can't be copied). The Pause button flips it; the worker polls it. It is
	// the ONLY state shared with the worker goroutine; everything else the
	// worker touches is snapshotted at launch, so the two never race.
	dlPaused atomic.Bool
}

// sessionState is EVERYTHING scoped to one server session. The active
// session's state lives embedded in App (field names promoted — call
// sites read `a.icInput` exactly as before tabs existed); switching tabs
// parks it into a courtTab and restores another with two struct moves
// (slice headers and pointers — no deep copies). The room (scene,
// typewriter, message raster) is deliberately NOT parked: background
// tabs don't animate, and activation rebuilds it via enterCourtroom.
type sessionState struct {
	// --- connection / session ---
	conn *protocol.Conn
	sess *courtroom.Session
	room *courtroom.Courtroom
	urls courtroom.URLBuilder
	// #130 CM/mod dashboard: per-server software override (SoftwareUnknown = auto-detect from
	// the ID packet). Parks per tab; a fresh connect's new sessionState resets it to auto.
	cmSoftwareOverride courtroom.ServerSoftware
	// autoLoginTried latches the auto-login to fire AT MOST ONCE per session.
	// EventReady fires on every DONE packet, and some servers re-send DONE
	// mid-session (the WAP/Akashi fork, area changes) — without this the saved
	// login re-queued on every one and spammed OOC. resetSessionState clears it,
	// so a genuine reconnect logs in again. See autoLoginOnReady.
	autoLoginTried bool
	// Voice chat membership (per-server, so it parks per tab): voiceJoined = we've
	// sent VS_JOIN; voiceMicOn = our speak state is on (announced via VS_SPEAK).
	voiceJoined bool
	voiceMicOn  bool
	// voiceCapsAnnounced: we've posted the one-time "this area supports voice" OOC
	// hint this session (so it confirms VS_CAPS arrived + points at the button).
	voiceCapsAnnounced bool
	showModDash        bool // the mod (ban/kick) dashboard panel is open
	showCMPanel        bool // the separate CM (area control) panel is open
	// Debug panel (Extras → Debug): sectioned diagnostics view (#333 packet inspector + perf + log).
	showDebugPanel bool
	debugSection   int         // active tab: 0 Session · 1 Packets · 2 Perf · 3 Log
	debugPktScroll int32       // Packets list scroll offset
	debugLogScroll int32       // Log list scroll offset
	debugPktBuf    []packetRec // reused recent()-into buffer (alloc-free draw)
	// Text FX picker (#M5): the FX button opens a floating effect list instead of cycling 13 effects.
	showFxPicker bool
	// showICColorWheel floats the free-hex colour wheel (v1.52.0) anchored to
	// the IC colour swatch; icSwatchRect remembers where that swatch drew this
	// frame (either layout), icColorHexBuf is the wheel's hex-field edit buffer.
	showICColorWheel bool
	icSwatchRect     sdl.Rect
	icColorHexBuf    string
	fxBtnRect        sdl.Rect // last-drawn FX button rect (the picker anchors above it + the fence finds it)
	// Per-part layout colours (v1.52.0): the parsed draw cache (refreshPartColors),
	// the Settings wheel's selected part, and its hex-field edit buffer.
	partColors   [config.LayoutPartColorCount]sdl.Color
	partColorOn  [config.LayoutPartColorCount]bool
	partEditSlot int
	partHexBuf   string
	// showPowerUser reveals the advanced/power-user Settings options (TLS, Asset Origin, asset
	// casing) — hidden by default so they're not changed by accident. Session-only.
	showPowerUser bool
	// powerNukeArm is the two-click confirm on "Reset all power-user options": the first
	// click arms (label flips), the second fires ResetPowerUser. Cleared on the reveal
	// toggle and on fire, so a stale arm can't linger across visits.
	powerNukeArm bool
	// spriteCapBase is the display-derived decode-downscale base height (set once
	// from cmd/asyncao); the downscale knobs scale it (config.EffectiveSpriteCap).
	spriteCapBase int
	// lastInputAt is the most recent SDL input event (frame pacing: full rate
	// through the InputGraceFrames hold after any interaction).
	lastInputAt time.Time
	// lastMotionAt is the most recent BARE pointer motion (experimental loop):
	// it holds full rate only through motionInputGrace, so circling the mouse
	// over dead space stops costing frames the moment it stops — clicks, keys
	// and the wheel still get the full input grace via lastInputAt.
	lastMotionAt time.Time
	// lastFrameDrawn is when Frame last actually rendered — the idle-cadence
	// anchor both loops measure from: the classic loop re-renders a static
	// courtroom once per idle-budget, and the event-driven loop schedules its
	// idle-rate wake from it (NextWakeDelay). idle=off → neither fires.
	lastFrameDrawn time.Time
	// uiDirty marks work that changed UI-visible state since the last drawn
	// frame — the connection pumps set it per drained packet — so the
	// experimental event-driven loop renders exactly one frame in response
	// instead of waiting out its idle tick. Render thread only; Frame clears it.
	uiDirty bool
	// drawnGen is the texture-store generation as of the last drawn frame: a
	// mismatch means textures uploaded/evicted since the screen was painted
	// (streamed-in icons, healed pages), which is redraw-worthy damage under
	// the experimental loop.
	drawnGen uint64
	// drawnCaretOn is the caret blink state as of the last drawn frame; a flip
	// since then is the scheduled damage that redraws a focused text field at
	// 2 Hz instead of holding the whole idle frame rate (experimental loop).
	drawnCaretOn bool
	// drawnScreen is the screen as of the last Frame — noteScreenTransition
	// compares against it to tear down hover-preview state that would otherwise
	// ride across a screen switch with no draw site left to close it (the
	// orphaned-preview cap latch; distinct from prevScreen, the back-nav memory).
	drawnScreen Screen
	// frameAnimChrome is set (via NoteAnimating) during a draw pass whenever it
	// renders a self-driven, TIME-STEPPED UI animation outside the viewport's
	// anim scheduler — an animated theme page (themeFrame), the looping testimony
	// badge, a WT/CE splash, the layout editor's ghost, or any future widget that
	// tweens on its own clock. drawnAnimChrome is the last completed frame's
	// value: while true, SkipFrame keeps frames coming AND FramePace paces at the
	// active cap, so the motion stays smooth (the general "self-driven animation →
	// uncap" mechanism); it falls back to idle the frame the draw stops marking it.
	frameAnimChrome bool
	drawnAnimChrome bool
	// roomPreAdvanced is set by the main loop's audio-paced sub-stepping: while a
	// message types at a low PRESENT rate, the loop advances the courtroom (and
	// plays its blips) at the fine audio cadence via Background between presents, so
	// audio never batches to the frame rate. When set, the next Frame skips its own
	// room.Update (the room is already current) but still advances the UI + draws.
	// Consumed (cleared) by that Frame. See AudioPaceActive / MarkRoomPreAdvanced.
	roomPreAdvanced bool
	// frameDemandPending is set during a draw pass whenever it leaves a
	// DEMAND-STREAMED cell (an emote-grid button with neither its art nor its
	// fallback icon resident yet) truly blank. drawnDemandPending is the last
	// completed frame's value: while true, NextWakeDelay schedules a re-render at
	// the demand cadence so demandAsset keeps issuing asks. Without it the pump
	// stalls at idle=0 — a batch of asks that all 404 uploads nothing, so nothing
	// bumps the store generation and no self-wake fires, and the grid can sit
	// half-filled until unrelated damage or input. Clears the moment every cell
	// shows something (art or the shared icon), so it costs nothing at rest.
	frameDemandPending bool
	drawnDemandPending bool
	// sceneWarmLastDemand throttles keepSceneAssetsWarm's evicted-base heal
	// (one pool job per sceneWarmRedemandEvery scene-wide, never per frame).
	sceneWarmLastDemand time.Time
	// Warm-keeper futility latches (warmMaxHealChurns/warmMaxHealAsks):
	// per-base heal history, reset when the respective warm set changes or a
	// new ceremony begins. Bounded by the sets themselves (≤6 scene bases /
	// ≤3 active-emote bases); lazily allocated.
	sceneWarmDemands map[string]sceneHealState
	sceneWarmSet     [6]string // the warm set sceneWarmDemands counts against
	// sceneWarmPhase tracks the room's message phase so an idle→ceremony
	// transition resets the heal budget even when the warm set is unchanged
	// (the blankpost-same-emote case).
	sceneWarmPhase    courtroom.MessagePhase
	activeWarmDemands map[string]int
	activeWarmChar    string // active-warm identity: character folder…
	activeWarmIdx     int    // …and selected emote index
	// activeWarmLastDemand throttles keepActiveAssetsWarm's re-demands to the
	// scene keeper's cadence (the old path submitted a pool job per 50 ms tick).
	activeWarmLastDemand time.Time
	// winW/winH cache the logical window size Frame was last given, for draw
	// helpers deep in a call chain that need window bounds (e.g. the sprite
	// preview's on-screen clamp) without threading params through every layer.
	winW, winH int32
	amICMNow   bool // cached amICM() — refreshed on ARUP/PU events, read per-frame by the
	// corner badge (0-alloc): the CM column lives in AreaInfo (ARUP), so a roster-stamp memo would
	// miss a /cm that doesn't change the roster — we recompute on the area/player events instead.
	modDashTargetUID string // selected target's UID ("" = none) — keyed by UID, never a roster
	// index: rebuildLiveRoster replaces the slice on every join/leave, so an index would repoint
	// a ban at whoever shifted into that slot.
	// Ban/Kick box (#130): a FROZEN snapshot of the target, taken when the box opens, so a roster
	// rebuild while the reason is being typed can never repoint a destructive command at someone
	// else. Only the IPID is allowed to fill in later (re-resolved by the frozen UID — same person).
	banBoxKind      int                   // 0 = closed, 1 = ban, 2 = kick, 3 = bulk ban, 4 = bulk kick
	banBoxUID       string                // snapshot: target UID (the identity anchor)
	banBoxIPID      string                // snapshot: target IPID (mod-only; "" until a /getarea enrich)
	banBoxName      string                // snapshot: display name for the box header
	banBoxDur       courtroom.BanDuration // chosen duration (ban only)
	banBoxCustomDur string                // a saved CUSTOM duration chip's canonical token ("45m"); "" = the enum preset banBoxDur applies
	banBoxReason    string                // typed reason
	modDashScroll   int32                 // mod dashboard roster scroll offset
	cmRosterScroll  int32                 // CM panel roster scroll offset
	// #13 mod dashboard v2: a session audit log, bulk targeting, and the left-column view switch.
	// All per-session (per-tab): a new server connection starts with an empty log and selection.
	modDashShowAudit bool            // left column shows the session audit log instead of the roster
	modAudit         []modAuditEntry // bounded, session-scoped record of commands sent from the dash
	modAuditScroll   int32           // audit list scroll offset
	modDashSelected  map[string]bool // UIDs ticked for a bulk ban / kick (lazily inited; ≤ modBulkCap)
	bulkBoxUIDs      []string        // frozen snapshot of the ticked UIDs when a bulk box opens
	modTemplatesEdit bool            // the ban box's reason-template editor is open (× removes a chip)
	modDurEdit       bool            // the ban box's custom-duration editor is open (× removes a chip)
	modDurInput      string          // the "add custom duration" field's draft ("45m", "2 days", …)
	serverName       string
	serverKey        string    // ws URL: keys the per-server warm state in prefs
	connAt           time.Time // session start (Rich Presence elapsed timer)
	curArea          string    // last area WE clicked (Rich Presence, best-effort)
	presenceInit     bool      // false until the first lobby presence push (so "Playing AsyncAO" shows on launch, not only in-court)
	// Per-area IC scrollback (opt-in): areaLogs holds each visited area's saved
	// icLog, areaLogOrder is the visit order for bounded FIFO eviction
	// (areaLogCacheMax). Driven by the area-click switch; both park per tab.
	areaLogs     map[string][]icEntry
	areaLogOrder []string
	lastPing     time.Time // CH keepalive pacing (active + background)
	lastICSend   time.Time // chat_ratelimit window
	manifestFor  string    // origin already fetched this session (dedupe)
	// themeBound is this server's bound theme ("" = the global pick);
	// loaded from ServerWarmInfo.Theme on connect, wins in
	// applyThemeAsync while the session lives.
	themeBound string
	// rehearsal marks the offline cached-asset browser (no connection;
	// the manager's network gate is closed while set). Rehearsal never
	// parks — backgrounding it would hold the global gate closed.
	rehearsal bool

	// --- char select ---
	charSearch string
	charScroll int32
	charTab    int // charTabServer | charTabWardrobe (grid contents swap)
	// wardrobeMembers is the lowercased wardrobe set for the current server,
	// powering the ★ star state in the Characters grid. Rebuilt only when the
	// server or the wardrobe generation changes (ensureWardrobeMembers), so the
	// per-cell star lookup stays a lock-free, alloc-free map read.
	wardrobeMembers    map[string]bool
	wardrobeMembersFor string // serverKey the set was built for
	wardrobeMembersGen uint64 // config.WardrobeGeneration() it was built at
	// lowered-query memos: search filters run per frame; re-lowering the
	// query allocated on every one of them.
	charQ loweredCache
	pairQ loweredCache
	iniQ  loweredCache
	// iconAsk[i] is when char i's icon was last demanded by the visible
	// grid (bounded by the server's char list length).
	iconAsk []time.Time
	// charLower caches lowercased char names for the search filter —
	// without it a 4000-char grid pays two ToLower allocations per char
	// per frame while a query is active. Invalidated on EventCharsUpdated.
	charLower []string
	// Generation-keyed texture page caches (the viewport's animState
	// trick applied to grids): while the store generation is unchanged a
	// grid redraw costs ZERO LRU lookups/locks for resident icons.
	iconPages    []*render.TexturePage
	iconPagesGen uint64
	iniPages     []*render.TexturePage
	iniPagesGen  uint64
	// Emote button art (off/on variants) rides the same gen-keyed trick:
	// the grid was the last draw path paying an LRU lock per cell/frame.
	emoteBtnOff    []*render.TexturePage
	emoteBtnOffGen uint64
	emoteBtnOn     []*render.TexturePage
	emoteBtnOnGen  uint64
	// emoteIconPages: the active character's icon (one slot), drawn behind the emote
	// label when an emotions/button<N> image is absent/streaming — a face beats a bare
	// grey box. Same gen-/index-keyed page cache, so it's nulled on a character change.
	emoteIconPages []*render.TexturePage
	emoteIconGen   uint64
	emoteIconAsk   []time.Time
	// #M2 S1: the IC-bar emoji picker (local; insert emoji into your message).
	// showEmojiPicker toggles the grid overlay; emojiBtnRect anchors it above the button.
	// emojiFenceOn tracks that WE set the modal fence so it's RELEASED when the picker
	// closes — modalOn persists across frames, so an un-released fence freezes the UI.
	showEmojiPicker bool
	emojiFenceOn    bool
	emojiBtnRect    sdl.Rect

	// #2 real reactions. reactionFloats is the bounded ring of emoji rising over the stage
	// (drawReactionFloats); reactBadges caches one alpha-fadeable texture per palette index
	// (built once the colour-emoji face lands); reactRect is the reused draw scratch (a fresh
	// local would heap-escape through cgo). reactSpawnSeq scatters floats horizontally.
	reactionFloats []reactionFloat
	reactBadges    map[uint8]*render.Badge
	reactRect      sdl.Rect
	reactSpawnSeq  int32
	// React trigger: showReactPicker toggles the palette (reactFenceOn tracks our modal
	// fence so it's released on close, like the emoji picker); reactBtnRect anchors it.
	// reactTarget{Ref,Name} is the message snapshotted when the palette opened. pendingReact
	// (+set) is the queued reaction that rides the next IC send; lastReact{Ref,Name} tracks
	// the most recent IC message, the snapshot source.
	showReactPicker bool
	reactFenceOn    bool
	reactBtnRect    sdl.Rect
	reactTargetRef  uint32
	reactTargetName string
	pendingReact    courtroom.WireReaction
	pendingReactSet bool
	lastReactRef    uint32
	lastReactName   string

	// --- courtroom chrome ---
	icInput string
	// icPendingSent snapshots icInput at IC send time. AO2-Client parity
	// (courtroom.cpp handle_chatmessage): the input box clears only when the
	// server ECHOES our message back (CHAR_ID == m_cid) — tsuserver-family
	// servers silently swallow an MS that lands inside another message's
	// area-wide delay window (area.can_send_message), so clearing at send
	// time threw the whole typed line away whenever two people sent at once.
	icPendingSent string
	// (The old one-slot icUndoText/oocUndoText Ctrl+Z stash is gone: every
	// text field now has a real undo history — fieldhistory.go — whose
	// out-of-band detector catches the same clears, with redo.)
	oocInput string
	// oocName is THIS TAB's OOC chat name, seeded from the saved default in
	// resetSessionState — it lived on App and a name typed in one tab showed
	// up in every other (playtest). Only the Settings field writes the saved
	// default; the courtroom fields are tab-local.
	oocName string
	// IC message recall (#8): Up/Down in the IC field walk your recently-sent messages,
	// shell-style. icRecallIdx == -1 = editing the live draft; icRecallDraft stashes that
	// draft while you browse history. Per-tab (lives in sessionState).
	icSentHist    []string
	icRecallIdx   int
	icRecallDraft string
	// OOC message recall: the same Up/Down history walk for the OOC inputs (the OOC box,
	// the bottom OOC bar, and the themed OOC field — they all share oocInput). Its own ring,
	// separate from IC, so OOC and IC histories never bleed together.
	oocSentHist    []string
	oocRecallIdx   int
	oocRecallDraft string
	// Friends-tab PM composer (#friends): the selected friend + the message being
	// typed. Sends as "/pm <their uid> <message>" over OOC.
	pmTarget string
	pmInput  string
	// pmThreads is the per-conversation PM history (keyed by the partner's
	// canonical name via pmThreadKey) the Friends tab shows as a little DM thread.
	// Outgoing lines append on send; incoming ones are best-effort parsed out of
	// OOC (detectIncomingPM — server formats vary, a miss just stays in the OOC
	// log). Session-scoped (PMs are per-server) and bounded.
	pmThreads map[string][]pmLine
	// shownameOverride is the in-courtroom showname box: when non-blank
	// it wins over the persisted Settings showname for outgoing messages
	// (session-scoped — it never overwrites the saved one).
	shownameOverride string
	sidePref         string // OUR side (char.ini default, /pos override)
	// lastSentStyle is the sprite style we last TRANSMITTED this session. Send-on-
	// change appends the marker only when the current style differs (EncodeChangeMarker),
	// so the invisible run rides change-messages only — zero value = nothing sent yet,
	// so the first active style (re)transmits on a fresh connection.
	lastSentStyle courtroom.SpriteStyle
	// lastSentProfile is the cross-client character profile (#101 slice 2) we last
	// TRANSMITTED this session — send-on-change like lastSentStyle (a profile rarely
	// changes, so the invisible run rides at most our first post-join message).
	lastSentProfile courtroom.WireProfile
	// myStatus is our chosen presence flag (#M1); lastSentStatus is what we last
	// TRANSMITTED (send-on-change). Session-scoped — a fresh connect starts at None.
	myStatus       courtroom.Status
	lastSentStatus courtroom.Status
	iniWarmed      string // last char.ini hover-warmed (dedupe)
	icColor        int    // outgoing MS text_color (dropdown)
	// icExtColor selects an extended AsyncAO colour (#98), 1-based into
	// render.ExtColorAt (icExtColor-1); 0 = none, so the &App{} zero value stays
	// idle. Mutually exclusive with the wire palette / Rainbow / Random.
	icExtColor int
	// icCustomOn/icCustomRGB: the free hex chat colour (v1.52.0, Tifera). The
	// exact colour ships as inline `\c#RRGGBB` markup with a nearest-standard
	// wire fallback (funColor); per-tab like every other colour pick. The last
	// pick also persists globally (Prefs.ICCustomColorRGB seeds a fresh tab).
	icCustomOn  bool
	icCustomRGB int
	icImmediate bool  // MS Immediate: preanim plays without holding the text (session toggle)
	icEffect    uint8 // #M5 sticky Text FX (courtroom.TextEffect*); 0 = off. Wraps every message you send.
	// pair placement (session-scoped: each tab keeps its own, seeded from prefs in
	// resetSessionState, so it can't leak across tabs like the App-global version
	// did). pairOffXText/Y are the typed edit buffers (commit on valid parse).
	pairOffX, pairOffY         int
	pairFlip                   bool
	pairOffXText, pairOffYText string
	// Pair-preview background cache (drawPairGhost): the stage bg drawn behind the
	// ghost sprites via cachedPage; pairBgKey invalidates it when the bg/position changes.
	pairBgPages []*render.TexturePage
	pairBgGen   uint64
	pairBgKey   string
	// Pos-dropdown thumbnail cache (drawPosSelect): one bg thumbnail per position,
	// keyed by row index; posBgKey (the current bg name) invalidates the whole set
	// when the background changes so a stale stage can't show under the wrong bg.
	posBgPages   []*render.TexturePage
	posBgGen     uint64
	posBgKey     string
	posBgAsk     []time.Time               // demandAsset pacing for the pos-dropdown thumbnails
	posThumbFn   func(idx int, r sdl.Rect) // stable (one-time) pos-thumbnail draw fn — avoids a per-frame closure alloc on the hot Pos selector
	emotes       []courtroom.Emote
	emoteIdx     int
	emotePage    int // emote grid paging (classic + themed)
	emotePerPage int // emotes per page last frame (number-key select)
	// SFX picker (IC-bar dropdown): the chosen sound that OVERRIDES this character's
	// emote sound on every send until set back to "auto". sfxChoices[0]="SFX: auto"
	// (the emote's own sound), then the character's distinct emote sounds; idx 0 = auto.
	// Rebuilt by ensureSFXChoices when the character (sfxChoicesFor) changes.
	sfxChoiceIdx  int
	sfxChoices    []string
	sfxChoicesFor string
	// #12 SFX Browser: an opt-in modal that expands the dropdown with persisted favourites,
	// per-row preview, and a free-text "use any sound name" entry. It picks into the SAME
	// sfxChoiceIdx override (find-or-append), so the dropdown reflects whatever it chose.
	showSfxBrowser   bool
	sfxBrowserQuery  string // the search / free-text field (filters the list; usable as an exact name)
	sfxBrowserScroll int32
	// emotePageLabel memoizes the "page x/y · N emotes" counter so the per-frame
	// emote-grid draw allocates nothing while paging is stable; rebuilt only when
	// {page, pages, total} change (same idiom as the generation-cached pages).
	emotePageLabel    string
	emotePageLabelKey [3]int
	// Emote favourites view (#77): emoteFavSet holds the active character's
	// favourited emote indices for lock-free O(1) star lookups, and emoteVisible
	// is the list of emote indices the grid shows (all, or favs-only) — a reused
	// buffer. Both are rebuilt by refreshEmoteView ONLY when the guard key
	// (char/favs-only/emote count/edit-rev) changes, so a steady-state frame does
	// one cheap compare and nothing else.
	emoteFavSet      map[int]struct{}
	emoteVisible     []int
	favBoxList       []int  // current character's favourite emote indices (always; for the floating box)
	emoteFavRev      int    // bumped on every favourite toggle to invalidate the view
	emoteViewChar    string // guard: character the view was built for
	emoteViewFavOnly bool   // guard: favs-only state the view was built for
	emoteViewLen     int    // guard: len(emotes) the view was built for
	emoteViewRev     int    // guard: emoteFavRev the view was built for
	// Floating favourite-emotes box (#85): movable geometry (session, like the
	// Extras box) + the drag grab offset. Open state is the FavEmoteBox pref.
	favBoxX, favBoxY int32
	favBoxPlaced     bool
	favBoxDragging   bool
	favBoxGrabDX     int32
	favBoxGrabDY     int32
	// Floating Sprite Style box (#104): the non-modal, draggable picker for the
	// transmitted sprite style — open state + movable geometry are session-only.
	showStyleBox     bool
	styleBoxX        int32
	styleBoxY        int32
	styleBoxPlaced   bool
	styleBoxDragging bool
	styleBoxResizing bool  // the right-edge width grip is being dragged
	styleBoxUserW    int32 // user-set width (0 = the default styleBoxW); height stays content-driven
	styleBoxGrabDX   int32
	styleBoxGrabDY   int32
	// Custom glitch fringe-colour hex buffers (the style box's A/B fields): they
	// hold in-progress typing and mirror the pref's effective colour when unfocused.
	glitchHexA string
	glitchHexB string
	// Style-box live preview: collapse state, the background page cache (the
	// pair-ghost pattern; keys are full URLs so per-server separation is
	// structural), and the sprite prefetch dedupe (one warm per emote base).
	stylePrevOff     bool
	stylePrevBgPages []*render.TexturePage
	stylePrevBgGen   uint64
	stylePrevBgKey   string
	stylePrevWarm    string
	// showAreaWheel expands the area-colour row's inline colour wheel (Settings →
	// Area list) — session-only chrome, like the other settings expanders.
	showAreaWheel bool
	// Server-clock chip memo: the "Tn mm:ss" labels are rebuilt only when their
	// displayed second changes, into a reused scratch slice — so a visible (esp.
	// paused) clock costs nothing on the always-on courtroom draw.
	timerChips     []string
	timerLabels    [courtroom.TimerCount]string
	timerLabelSecs [courtroom.TimerCount]int
	charBlips      string // char.ini blips/gender (outgoing default)
	// 2.10 custom shouts ([Shouts] in char.ini): customIdx −1 = the base
	// "custom" art, ≥ 0 indexes customShouts.
	customShouts []courtroom.CustomShout
	customIdx    int
	customName   string
	charINIBusy  bool
	icLog        []icEntry
	icLogSeq     uint64 // bumps per mutation: keys the filter cache
	icScroll     int32
	// icScrollVis is the EASED on-screen scroll offset (#22 smooth scrolling):
	// icScroll is the target (wheel/stick/bar writes it), the visual glides
	// toward it exponentially so wheel steps and the sticky-bottom's new-line
	// jumps stop teleporting. frameDtMs (stamped per drawn frame) drives the
	// time constant so the feel is frame-rate independent.
	icScrollVis float64
	frameDtMs   float32
	logSearch   string
	oocSeq      uint64
	oocLog      []string
	oocSpeakers []string // parallel to oocLog: speaker per line ("" = system line); for name colours
	oocScroll   int32
	musicScroll int32
	// Music-list search (AO2/webAO parity): the query plus a memoized filter so
	// a list of thousands isn't re-scanned (and re-lowercased — that allocates)
	// every frame. musicFiltered holds matching indices into a.sess.Music.
	musicSearch     string
	musicFiltered   []int
	musicFilterMemo musicFilterKey
	areaScroll      int32
	logTab          int
	volStripOn      bool // the log panel's toggleable on-screen volume strip
	musicVolMode    bool // Music tab shows the volume sliders instead of the track list
	// Stick flags: the logs FOLLOW new lines while true; scrolling up
	// releases, scrolling back to the bottom re-sticks. (The old "within
	// one line of the bottom" heuristic broke whenever one wrapped
	// message added several rows at once.)
	icStick  bool
	oocStick bool
	// icReadMark is the IC entry count when last caught up to the bottom; while
	// scrolled up, len(icLog)-icReadMark is the unread count the "N new" pill shows.
	icReadMark int
	// emoteAsk[i] paces demand for emote i's button art (drawEmoteRow).
	emoteAsk []time.Time

	// --- case notebook (per-server pins) ---
	notebook     *config.Notebook
	noteInput    string
	noteScroll   int32
	noteCache    []string // rev-keyed Lines() snapshot
	noteCacheRev int64

	// --- character keybinds (per server) ---
	// bindingFor is the character a wardrobe key-capture is armed for
	// ("" = none); charKeys/charKeysRev cache this server's binds for
	// per-frame lookups (refreshed on connect + edits only).
	bindingFor  string
	hkCapture   string            // settings Controls tab: hotkey id a key-capture is armed for ("" = none)
	charKeys    map[string]string // key name → character
	charKeysRev map[string]string // character → key name (badges)

	// --- showname keybinds (M6, global) ---
	// shownameBindFor is the showname a Settings key-capture is armed for
	// ("" = none); shownameKeys/Rev cache the global binds for per-frame
	// lookups (refreshed on connect + edits only, like charKeys).
	shownameBindFor string
	shownameKeys    map[string]string // key name → showname
	shownameKeysRev map[string]string // showname → key name (badges)
	// #126 custom style presets: stylePresetBindFor is the preset NAME awaiting a key-capture
	// ("" = none); stylePresetNameInput is the Style box's "name this mood" field.
	stylePresetBindFor   string
	stylePresetNameInput string
	// IC quick-phrases (ic_phrase.go): a bare key sends a canned IC line.
	// icPhraseBindFor is the phrase a Settings key-capture is armed for ("" = none);
	// icPhraseKeys caches the global key→phrase binds for the per-frame dispatch.
	icPhraseBindFor string
	icPhraseKeys    map[string]string // key name → IC phrase

	// ghostWarm dedupes the pair-panel ghost editor's sprite prefetches.
	ghostWarm map[string]string

	// --- OOC automation (login flows + macros share the send pipeline) ---
	oocQueue  []oocSend // paced OOC sends (≤ macroQueueCap)
	showLogin bool
	loginUser string
	loginPass string
	loginAuto bool

	// --- viewport camera (hyperfocus zoom; 0 or 1 = off) ---
	vpZoom   float64
	vpPanX   float64 // pan fractions of the zoom overflow (0..1)
	vpPanY   float64
	zoomDrag bool
	zoomPrev bool // last frame's mouseDown (edge detect)

	// M16: drag the viewport's right edge to resize it (vpPct), the mouse
	// alternative to the View knob. dragVpDivider is the active grab.
	dragVpDivider  bool
	dividerPrevDwn bool // mouseDown edge detect for the divider grab
	zoomStart      [2]int32
	zoomBase       [2]float64

	// --- court extras (HP / WTCE / modcall / evidence) ---
	wtceName    string    // active splash stem ("" = none)
	wtceAt      time.Time // splash start (frame stepping + expiry)
	testimonyOn bool      // persistent "Testimony" recording badge (RT 2.9)
	hpPrev      [2]int    // last drawn HP per bar — penalty sfx direction
	showModcall bool      // modcall reason dialog
	modReason   string
	// Local alarm/timer (#97, opt-in): a personal countdown distinct from the
	// server courtroom timers. App-global (one timer across all tabs, like DND).
	// timerEndAt zero = not counting down; timerPausedLeft > 0 = paused; both at
	// rest = idle, so pollTimer and the on-stage chip cost nothing when unused.
	showTimer       bool
	timerEndAt      time.Time     // when the running countdown fires (zero = not running)
	timerPausedLeft time.Duration // frozen remainder while paused (0 = not paused)
	timerSetSec     int           // configured duration, seeded from prefs on first open
	timerRepeat     bool          // restart from the same duration on fire
	timerSeeded     bool          // timerSetSec loaded from prefs once
	showEvid        bool          // evidence panel
	evidIdx         int           // selected evidence (-1 = none)
	evidPresent     bool          // armed: next IC message carries the selection
	evidEditing     bool          // editor open (add when evidIdx == -1)
	evidName        string
	evidDesc        string
	evidImage       string
	evidScroll      int32
	evidAsk         []time.Time // thumbnail demand pacing, parallel to Evidence
	// evShow is the incoming presented-evidence pop-up.
	evShowImg string
	evShowAt  time.Time

	// --- wardrobe / iniswap (client favourites + server iniswap.txt) ---
	iniChar      string   // active override folder ("" = picked character)
	pendingIni   string   // wear this once PV confirms (char-select joins)
	iniServer    []string // the server's iniswap.txt names (may be empty)
	iniList      []string // merged menu: wardrobe first, then server extras
	iniWardrobe  []bool   // parallel to iniList: wardrobe membership (star)
	iniServerMem []bool   // parallel to iniList: is in the server's iniswap.txt (Iniswaps tab filter)
	iniLower     []string // lowercased names for the search filter
	iniFolders   []string // parallel to iniList: each entry's folder ("" = unsorted)
	// A wardrobe ★ toggle is DEFERRED out of the cell to after the grid loop: it
	// rebuilds (and shrinks) the iniList/iniWardrobe/iniFolders slices the loop is
	// ranging, so toggling mid-loop panicked on a remove (the crash report).
	iniFavPending    string // char to toggle ("" = none)
	iniFavPendingAdd bool   // true = add to wardrobe, false = remove
	iniListErr       string
	iniBusy          bool
	showIni          bool
	showReset        bool // factory-reset confirmation pop-up (Settings)
	iniSearch        string
	iniAdd           string   // "add folder to wardrobe" input
	iniSwapMode      bool     // Characters tab: click iniswaps instead of switching char (off=switch; per session)
	iniFolder        string   // open wardrobe folder ("" = top level/root, else folder name)
	iniNewFold       string   // "new folder" text input
	iniMenuChar      string   // wardrobe char with an open "move to folder" menu ("" = none)
	iniMenuAt        [2]int32 // that menu's top-left (cursor at right-click)
	iniHoverChar     string   // wardrobe char under the cursor this frame (number-key quick-file)
	// Drag-to-file (app-drawer style): drag a character cell onto a folder
	// chip to file it. iniDragChar is armed on press; iniDragging flips once
	// the move passes iniDragThreshold (and then suppresses the wear-click).
	iniDragChar     string
	iniDragStart    [2]int32
	iniDragging     bool
	iniPrevDown     bool // mouse-held tracker for press detection in the modal
	iniPressed      bool // mouse went down this frame (computed per frame)
	iniScroll       int32
	iniBrowseScroll int32       // separate scroll for the Iniswaps (browse-all) tab
	iniAsk          []time.Time // demand pacing stamps, parallel to iniList

	// wardSection selects the open wardrobe tab (wardSectionCharacters /
	// wardSectionBackgrounds). The Backgrounds section organizes favourite
	// backgrounds into the same navigable folders as characters.
	wardSection int
	// wardDelFolder is the folder a delete confirmation is open for ("" = none);
	// the active section decides whether it deletes characters or backgrounds.
	wardDelFolder string
	// Backgrounds-section state. bgFavList is the favourites in ONE stable
	// order (FavBackgroundList); navigation filters via a predicate, never by
	// rebuilding the list, so the index-keyed bgFavPages cache stays valid
	// (see the cachedPage reorder invariant). bgFavPages is its OWN cache —
	// never share bgPick.pages (different index space). Nulled only when the
	// favourites set/order changes (rebuildBgFav), not on folder navigation.
	bgFavList     []string
	bgFavLower    []string // lowercased, parallel — search
	bgFavFolders  []string // folder per favourite, parallel — nav filter
	bgFavFolder   string   // open background folder ("" = top level)
	bgFavNewFold  string   // "new folder" text input (Backgrounds section)
	bgFavSearch   string
	bgFavScroll   int32
	bgFavAsk      []time.Time // demand pacing stamps, parallel to bgFavList
	bgFavPages    []*render.TexturePage
	bgFavPagesGen uint64

	// bgPick is the "change background" modal (background/ autoindex grid).
	bgPick bgPicker

	// dl is the opt-in single-asset downloader (off by default). The pause flag
	// shared with the worker lives in App proper (a.dlPaused), NOT here — this
	// struct is copied by value on tab park/activate, which can't copy an
	// atomic, and the worker needs one stable global flag anyway.
	dl downloader

	// sfxMuted is a session-only SFX mute (Mute SFX hotkey); showHotkeys
	// toggles the F1 hotkey cheat-sheet overlay; musicDucked tracks whether
	// music is currently ducked under a playing message (transition-driven).
	// masterMuted/musicMuted/blipMuted are the per-channel mute toggles in the
	// volume strip (#10) — session-only, like sfxMuted; they zero the channel in
	// applyAudioVolumes WITHOUT touching the stored slider level (master mutes all).
	sfxMuted           bool
	masterMuted        bool
	musicMuted         bool
	blipMuted          bool
	showHotkeys        bool
	hkCache            []hkEntry           // hotkey cheat-sheet rows, rebuilt once per open (not per frame)
	confirmDisconnect  bool                // a Disconnect confirm popup is open (unless instant-disconnect is set)
	hidePrompt         string              // a "hide this sprite?" confirm is open for this char name ("" = none)
	hiddenSprites      map[string]struct{} // chars hidden from the viewport this session (lowercased); nil until first hide
	autoConnectPending bool                // fire auto-connect-to-last-server once, on the first frame
	musicDucked        bool

	// scenery self-heal stamps (healScenery pacing)
	bgAskBase   string
	bgAskAt     time.Time
	deskAskBase string
	deskAskAt   time.Time
	spkAskBase  string // speaker sprite heal pacing
	spkAskAt    time.Time
	pairAskBase string // pair sprite heal pacing
	pairAskAt   time.Time

	// Click-to-pair (/pair <uid> for servers that sync pairs via the OOC command).
	// areaUIDs/areaPlayers are parsed from /getarea in pushOOC; the popup pre-fills
	// the UID when it can confidently match the clicked char, else manual entry.
	pairPopupOpen bool
	pairPopupChar string
	pairPopupUID  string
	areaUIDs      map[string]string
	areaPlayers   []areaPlayer
	areaLastUID   string    // last "[uid]" parsed, so a following Showname/OOC/IPID line attaches to it
	areaCurName   string    // area currently being parsed in a /gas block (tags each player's .area)
	areaListAt    time.Time // when the current roster snapshot was parsed ("as of HH:MM")
	pairAreaReset bool
	// Live player list (M1): rosterLegacy off (default) = the CharsCheck/ARUP
	// roster that updates as people join/leave with no extra traffic; on = the
	// rich /getarea snapshot. shownameFor caches char→showname from incoming IC
	// so a live row shows the showname, not the bare character folder.
	rosterLegacy  bool
	livePlayersOn bool         // PR/PU server roster is the live source (else the CharsCheck fallback)
	liveRoster    []areaPlayer // M1 live roster (PR/PU players, or CharsCheck taken chars + ARUP spectators)
	liveRosterAt  time.Time    // live roster's last change — the rows/order memo key
	// New-joiner highlight (#107): joinFlash[uid] = when a UID first appeared in the live
	// roster (zero value = present at the first population, never flashed). Updated only on a
	// roster change (updateJoinFlash); the per-frame row draw just reads it (0-alloc).
	joinFlash     map[string]time.Time
	joinFlashInit bool
	// pairPartners maps a lowercased character → who they last spoke PAIRED with (#20, opt-in
	// player-list chip). A paired IC message sets it; a solo one clears it, so it tracks the
	// player's CURRENT pair as of their latest line. Per-tab; bounded by pairPartnersCap.
	pairPartners map[string]string
	// Player-list profile card popover (#101): which profile to show + its title.
	profileCardShow bool
	profileCardPr   config.ProfilePref
	profileCardName string
	// Player-row action menu (rostermenu.go): the "…" / right-click popup that
	// replaced the per-row button cluster. Holds an identity SNAPSHOT (the
	// roster can refresh under an open menu) and the modal-fence latch.
	rosterMenuOpen        bool
	rosterMenuFenceOn     bool             // WE hold modalOn for the open menu (released on close)
	rosterMenuMe          bool             // the snapshot row is our own client
	rosterMenuAt          sdl.Point        // preferred top-left (clamped at draw)
	rosterMenuTab         int              // tab the menu opened on — auto-close on switch
	rosterMenuP           areaPlayer       // identity snapshot (uid/name/showname/ooc/ipid)
	rosterMenuItems       []rosterMenuItem // built at open; bounded by the action-kind count
	liveDetailsArea       string           // area of the last auto /getarea pull; re-pull on area change
	lastRosterFetch       time.Time        // debounce for the join/leave re-pull (rosterRefetchDebounce)
	suppressAreaEchoUntil time.Time        // keep /gas/getarea reply lines out of OOC until this time — the WHOLE reply burst (a multi-area /gas spans several messages), not just the first
	rosterCmdUnsupported  bool             // this server rejected /gas ("unknown command") — stop sending it (the live PR/PU roster still works without it)
	// Follow-a-player (M3): followUID is the player we trail across areas ("" =
	// off); we auto-jump to their area on each PR/PU update, debounced.
	followUID      string
	lastFollowJump time.Time
	followShow     bool // FollowEnabled pref, read once per player-list frame (no per-row lock)
	pairStatusShow bool // ShowPairStatus pref (#20), read once per player-list frame (no per-row lock)
	// areaHistory is the most-recently-visited areas (MRU, index 0 = current),
	// driven by our own PR/PU area; the Areas tab shows the rest as jump-back
	// chips (M3). Bounded by areaHistoryCap.
	areaHistory    []string
	shownameFor    map[string]string
	lastSFXName    string // M11: most-recent emote SFX name, for one-click "Mute last SFX"
	lastBlipChar   string // M11: most-recent speaker char, for one-click per-character blip volume
	icCountN       int    // M5 IC char counter: cached count + its string, reformatted
	icCountStr     string // only when the length changes so the frame stays 0-alloc
	pairListScroll int32
	playerScroll   int32  // Players-tab roster scroll
	playerSort     int    // roster sort (players): 0=UID, 1=name, 2=speakers-first
	playerAreaSort int    // /gas area-group order: 0=/gas, 1=A→Z, 2=most-populated
	playerPct      int    // Players-tab text zoom (Ctrl+wheel); starts at the log scale
	shownameAdd    string // M6: Settings "save a showname preset" input
	// playerOrder is the memoized display order (indices into areaPlayers); it
	// recomputes only when the roster, sort mode, or current speaker change, so
	// the Players tab never sorts per-frame.
	playerOrder     []int
	playerOrderSort int
	playerOrderLen  int
	playerOrderSpk  string
	playerOrderAt   time.Time
	// playerRows is the memoized GROUPED display (area headers + players) used
	// when a /gas spans areas; same invalidation keys as playerOrder.
	playerRows         []rosterRow
	playerRowsSort     int
	playerRowsAreaSort int
	playerRowsLen      int
	playerRowsSpk      string
	playerRowsAt       time.Time
	playerRowsAreaAt   time.Time // areaListAt the rows were built at (a /gas reorders the default)
	// player-row char icons: same demand/cache pipeline as the char grid, but
	// keyed by the areaPlayers index (sort-stable). NULLED on every /ga replace
	// so a same-length new roster re-resolves (cachedPage reorder invariant).
	playerIconPages    []*render.TexturePage
	playerIconPagesGen uint64
	playerIconAsk      []time.Time
	// voiceIconAsk paces char-icon fetches for the voice panel rows (timing stamps
	// only — the texture itself comes straight from the URL-keyed Store, so there's
	// no index→page cache to corrupt). Sized to the live peer count.
	voiceIconAsk []time.Time

	// client-side sprite position overrides, keyed by lowercased character
	// folder: the server keeps setting positions per message, the client
	// wins afterwards (drag in the viewport; right-click a sprite resets).
	spriteOv  map[string][2]int
	dragName  string // character being dragged ("" = none)
	dragStart [2]int32
	dragBase  [2]int
	prevDown  bool // mouseDown edge detection for drag begin

	// chat raster invalidation extras (text/color tracked separately)
	rasterScale   int
	rasterW       int32
	rasterSkinned bool // theme skin gates theme text colors (readability)

	// pairing panel
	pairSearch string
	pairScroll int32
	pairWith   int
	pairOrder  int
	showPair   bool
	msRaster   *render.MessageRaster
	rasterText string
	// rasterRaw is the pre-strip message the cached raster was built from — the
	// cache key, since two differently-colored messages can share the same
	// stripped MessageText (the cachedPage index-key class of bug).
	rasterRaw string

	// last-applied scene text color (raster invalidation)
	rasterColor int

	// Animated chat text (#M5): when the message carries effect spans, msAnim
	// replaces msRaster for this message (the per-glyph path that can displace +
	// recolour). msAnimFont is the face it was laid out with (the glyph cache keys
	// on it); glyphCache holds the white glyph textures, shared + bounded. A plain
	// message never builds msAnim, so its draw path is byte-identical to before.
	// rasterEffSig folds the effect spans into the raster cache key.
	msAnim       *render.AnimatedText
	msAnimFont   *ttf.Font
	glyphCache   *render.GlyphCache
	rasterEffSig uint64
	// rasterCentered folds the webAO "~~" centre flag into the raster cache key, so a
	// centred "hi" and a plain "hi" (same stripped text) don't share one raster.
	rasterCentered bool
}

type lobbyFetch struct {
	entries []network.ServerEntry
	err     error
}

type iniswapFetch struct {
	key   string // serverKey the fetch was made for (tab-switch guard)
	names []string
	// bgs is the background/ autoindex fetched alongside the txt (nil when the
	// host has no listing): pollIniswap filters background folder names OUT of
	// the iniswap list — some servers publish one combined "everything
	// streamable" file (playtest: half of one server's iniswap.txt was its
	// background dir, and a background wears like a broken character).
	bgs []string
	err error
}

type charINIFetch struct {
	key string // serverKey the fetch was made for (tab-switch guard)
	ini *courtroom.CharINI
	err error
}

// previewEmoteFetch is one previewed character's emote list for the
// try-before-wear cycle. char guards against a newer hover landing first.
type previewEmoteFetch struct {
	key    string
	char   string
	anims  []string // per-emote animation name (sprite stem)
	labels []string // per-emote display comment (caption)
}

// manifestFetch is one extensions.json autodetect result.
type manifestFetch struct {
	host   string
	seeded int
	err    error
}

// themeApply is one off-thread theme load: every resident theme texture
// (chatbox skin, WT/CE/verdict splashes, testimony badge, HP bar states),
// the courtroom/penalty sound names, the message/showname font colors, and
// the diagnostics the status line / debug overlay report.
type themeApply struct {
	gen     uint64                     // themeGen stamp; older never overwrites newer
	name    string                     // theme that was loaded
	images  map[string]*assets.Decoded // stem → decoded (themeImageStems keys)
	sounds  map[string]string          // sound key → streamed sfx name
	msgCol  sdl.Color
	hasMsg  bool
	nameCol sdl.Color
	hasName bool

	// layout is the courtroom_design.ini geometry (themeLayoutKeys that
	// the theme defines, design-space pixels; showname/message stay
	// chatbox-relative exactly as AO2 positions those child widgets).
	layout    map[string]theme.Rect
	emoteCell [2]int // emote_button_size (w, h)
	emoteGap  [2]int // emote_button_spacing (x, y)
	// palette is the courtroom_stylesheets.css color scheme (the "css
	// stuff"): applied over the kit colors, restored on theme switch.
	palette theme.Palette

	// diagnostics: where the skin came from, how many INI keys loaded,
	// and the dirs probed (so "nothing found" names the actual paths).
	chatboxFile string
	chatboxDir  string
	iniKeys     int
	probed      []string
	inkGuard    string // readability guard verdict ("" = colors kept)
	fontPath    string // the active theme's bundled font file (.ttf/.otf), "" = none (#6)
}

// themeStemChatbox is the chatbox skin's stem in themeTex / T1.
const themeStemChatbox = "chatbox"

// Readability guard for theme ink (playtest: "displaying black text even
// when I choose white"). A theme's message/showname colors are designed
// against its own chatbox art, but real themes ship mismatched pairs —
// dark ink with a dark skin — that render invisible. At load time we
// compare the ink's luma against the skin's average luma and drop the
// theme color when the gap is below the floor (the kit default, white on
// dark, takes over). AO2 renders the broken pair as-authored; readable
// text beats parity here.
const (
	// lumaSampleStep samples every Nth pixel on both axes — a 512²
	// chatbox costs ≤ ~4k samples, once per theme apply, off-thread.
	lumaSampleStep = 8
	// minInkSkinContrast is the minimum |ink − skin| luma gap (0..255):
	// below it text is declared unreadable on the skin. 48 ≈ the gap
	// between mid-gray pairs that already strain at chat font sizes.
	minInkSkinContrast = 48
	// transparentSkinLuma stands in for see-through skin pixels: the
	// chatbox overlays the (dark) viewport / flat panel, so transparent
	// regions read dark, not black.
	transparentSkinLuma = 20
)

// colLuma is Rec. 601 perceptual luma on the 0..255 scale.
func colLuma(c sdl.Color) int {
	return (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
}

// avgSkinLuma samples a decoded skin frame's average luma, compositing
// alpha against the dark backdrop the chatbox actually draws over.
func avgSkinLuma(img *image.RGBA, step int) int {
	b := img.Bounds()
	if b.Empty() {
		return transparentSkinLuma
	}
	sum, n := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y += step {
		row := img.PixOffset(b.Min.X, y)
		for x := b.Min.X; x < b.Max.X; x += step {
			i := row + (x-b.Min.X)*4
			pix := (299*int(img.Pix[i]) + 587*int(img.Pix[i+1]) + 114*int(img.Pix[i+2])) / 1000
			a := int(img.Pix[i+3])
			sum += (pix*a + transparentSkinLuma*(255-a)) / 255
			n++
		}
	}
	if n == 0 {
		return transparentSkinLuma
	}
	return sum / n
}

// absInt is the integer |x| (no float detour on the guard path).
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// themeImageExts is the per-stem probe order, matching AO2-Client
// get_image_suffix (webp → apng → gif → png).
var themeImageExts = []string{".webp", ".apng", ".gif", ".png"}

// themeImageStems maps each resident theme texture stem to the candidate
// file stems probed in order. Chatbox candidates mirror AO2-Client
// courtroom.cpp:3328 ("chat" falling back to "chatbox"; "chatblank" last
// for themes that ship only the blank skin); splash stems mirror
// handle_wtce's filenames with the bare legacy spelling second.
func themeImageStems() map[string][]string {
	m := map[string][]string{
		themeStemChatbox:   {"chat", "chatbox", "chatblank"},
		"witnesstestimony": {"witnesstestimony_bubble", "witnesstestimony"},
		"crossexamination": {"crossexamination_bubble", "crossexamination"},
		"notguilty":        {"notguilty_bubble", "notguilty"},
		"guilty":           {"guilty_bubble", "guilty"},
		"testimony":        {"testimony"},
		// Screen backdrops: the single biggest "the theme applied" signal.
		"courtroombackground": {"courtroombackground"},
		"lobbybackground":     {"lobbybackground", "loadingbackground"},
		"charselect_bg":       {"charselect_background"},
	}
	for i := 0; i <= courtroom.HPBarMax; i++ {
		d := "defensebar" + strconv.Itoa(i)
		p := "prosecutionbar" + strconv.Itoa(i)
		m[d], m[p] = []string{d}, []string{p}
	}
	return m
}

// themeBtnPrefix namespaces themed widget art ("theme://btn/<design key>").
const themeBtnPrefix = "btn/"

// themeButtonStems maps courtroom_design.ini element keys to their AOButton
// art file stems (courtroom.cpp set_widgets setImage calls). Loaded with a
// PNG-FIRST ext order: the bare splash names ("crossexamination.gif") and
// these button files ("crossexamination.png") collide by stem, and on
// every real theme the button is the png.
func themeButtonStems() map[string][]string {
	return map[string][]string{
		"hold_it":           {"holdit"},
		"objection":         {"objection"},
		"take_that":         {"takethat"},
		"custom_objection":  {"custom"},
		"witness_testimony": {"witnesstestimony"},
		"cross_examination": {"crossexamination"},
		"not_guilty":        {"notguilty"},
		"guilty":            {"guilty"},
		"call_mod":          {"call_mod", "callmod"},
		"evidence_button":   {"evidencebutton", "addevidence"},
		"emote_left":        {"arrow_left"},
		"emote_right":       {"arrow_right"},
	}
}

// themeButtonExts: png first — see themeButtonStems.
var themeButtonExts = []string{".png", ".webp", ".apng", ".gif"}

// themeLayoutKeys are the courtroom_design.ini rects the themed courtroom
// consumes (names exactly as AO2-Client set_size_and_pos reads them).
// "courtroom" + "viewport" are mandatory for the layout to activate;
// everything else falls back per element.
var themeLayoutKeys = []string{
	"courtroom", "viewport", "ao2_chatbox", "showname", "message",
	"ic_chatlog", "server_chatlog", "ms_chatlog",
	"music_list", "music_search",
	"ooc_chat_message", "ooc_chat_name",
	"ao2_ic_chat_message", "ao2_ic_chat_name",
	// AsyncAO-only IC controls (#4b, Crystalwarrior): OPTIONAL keys a theme can add to
	// place these where it likes instead of having AsyncAO cram them into
	// ao2_ic_chat_message. Absent ⇒ the classic crammed row (every existing theme is
	// unchanged). x,y,w,h in design space, same as any AO2 element.
	"asyncao_ic_color", "asyncao_ic_immediate", "asyncao_ic_sfx",
	"asyncao_ic_emoji", "asyncao_ic_fx", "asyncao_ic_react",
	"pos_dropdown", "pair_button",
	"hold_it", "objection", "take_that", "custom_objection",
	"witness_testimony", "cross_examination", "not_guilty", "guilty",
	"defense_plus", "defense_minus", "prosecution_plus", "prosecution_minus",
	"defense_bar", "prosecution_bar",
	"call_mod", "evidence_button",
	"emotes", "emote_left", "emote_right",
}

// themeTexKey is the T1 key for a theme texture stem; the scheme prefix can
// never collide with real asset URLs.
func themeTexKey(stem string) string { return "theme://" + stem }

const (
	// defaultEmoteCellPx / defaultEmoteGapPx are AO2's stock emote-grid
	// metrics, used when the design INI omits the tuples.
	defaultEmoteCellPx = 40
	defaultEmoteGapPx  = 1
)

// designPair parses a "x, y" design tuple (emote_button_size and friends).
func designPair(t *theme.Theme, key string, defX, defY int) [2]int {
	raw, ok := t.DesignValue(key)
	if !ok {
		return [2]int{defX, defY}
	}
	parts := strings.Split(raw, ",")
	if len(parts) < 2 {
		return [2]int{defX, defY}
	}
	x, errX := strconv.Atoi(strings.TrimSpace(parts[0]))
	y, errY := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errX != nil || x <= 0 {
		x = defX
	}
	if errY != nil || y <= 0 {
		y = defY
	}
	return [2]int{x, y}
}

// themeSoundKeys lists the courtroom_sounds.ini entries the UI plays, with
// the lookup aliases handle_wtce probes and the stock AO2 default-theme
// values as fallbacks so themeless installs still sound right.
var themeSoundKeys = []struct {
	key      string
	aliases  []string
	fallback string
}{
	{"witness_testimony", []string{"witness_testimony", "witnesstestimony"}, "sfx-testimony2"},
	{"cross_examination", []string{"cross_examination", "crossexamination"}, "sfx-testimony"},
	{"not_guilty", []string{"not_guilty", "notguilty"}, "sfx-notguilty"},
	{"guilty", []string{"guilty"}, "sfx-guilty"},
	{"mod_call", []string{"mod_call"}, "sfx-gallery"},
	{"case_call", []string{"case_call"}, "sfx-evidenceadd"},
	{"realization", []string{"realization"}, "sfx-realization"},
	{"word_call", []string{"word_call"}, "call"},
}

// themePenaltyKeys are the penalty/penalty.ini sfx entries (no stock
// fallback: AO2 ships none and silence is the canonical default).
var themePenaltyKeys = []string{"hp_increased_sfx", "hp_decreased_sfx"}

// NewApp builds the UI over deps.
func NewApp(ctx *Ctx, d Deps) *App {
	a := &App{
		ctx:             ctx,
		d:               d,
		screen:          ScreenLobby,
		lobbyResult:     make(chan lobbyFetch, 1),
		pingRes:         make(chan pingResult, pingResBuf),
		charINIres:      make(chan charINIFetch, 1),
		previewEmoteRes: make(chan previewEmoteFetch, 1),
		updateRes:       make(chan *update.Release, 1),
		updateApplyRes:  make(chan error, 1),
		iniRes:          make(chan iniswapFetch, 1),
		manifestRes:     make(chan manifestFetch, 1),
		casingRes:       make(chan casingProbeResult, 1),
		fontRes:         make(chan fontLoad, 1),
		logBrowserRes:   make(chan logBrowserLoad, 1),
		emojiFontRes:    make(chan []byte, 1),
		fallbackFontRes: make(chan [][]byte, 1),
		cjkFontRes:      make(chan [][]byte, 1),
		notebookRes:     make(chan notebookLoad, 1),
		jukeRes:         make(chan *config.Jukebox, 1),
		jukeIORes:       make(chan string, 4),
		jukeOpen:        -1,
		jukeDelPlaylist: -1,
		selServer:       -1,
		activeTab:       -1,
		tabDragFrom:     -1,
		macroBind:       -1,
		themeTex:        map[string]bool{},
		themePages:      map[string]*render.TexturePage{},
		hidden:          map[string]bool{},
	}
	a.resetSessionState()
	// Wake the render loop when a decode/audio payload delivers (experimental
	// event-driven loop): the pump uploads it on the very next pass instead of
	// waiting out an idle tick. A queued no-op event when the loop pref is off.
	if d.Manager != nil {
		d.Manager.SetDeliveryNotify(PushWake)
	}
	// Held-frame bridge (the black-flash fix): when the LRU must evict the
	// ON-SCREEN background/desk to fit a cap-sized incoming sprite page, the
	// store steals one frame into its pinned tier and the viewport draws that
	// instead of exposing the black stage fill (render/textures.go).
	if d.Store != nil {
		d.Store.SetLiveScenery(a.IsLiveScenery)
	}
	for _, id := range d.Prefs.HiddenPanels() {
		a.hidden[id] = true
	}
	// Global jukebox library loads off-thread (it can be large); it lands via
	// pollJukebox so the keybinds and wardrobe tab have it ready.
	go func() {
		if j, err := config.LoadJukebox(); err == nil {
			a.jukeRes <- j
		}
	}()
	a.applyThemeAsync()                                                         // chatbox skin + font colors from the saved theme
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = d.Prefs.LayoutScales() // per-session view state (pair, OOC name, volume views) seeds in resetSessionState (above)
	a.oocPct = d.Prefs.OOCScale()                                               // OOC log text size, independent of the IC log
	a.musicPct = a.logPct                                                       // starts matching the log; ctrl+wheel over the Music tab tunes it apart
	a.playerPct = a.logPct                                                      // same for the Players tab + pair popup
	a.uiScalePct = d.Prefs.UIScale()
	ctx.SetUIScale(a.uiScalePct)
	a.applyFontConfig()                                      // dyslexia toggle or manual font path, resolved once
	a.dndOn = d.Prefs.DNDPersistOn() && d.Prefs.DNDSavedOn() // else session-only: clears each launch
	a.RefreshServers()
	// Restore-on-launch (opt-in, OFF by default): queue the remembered tabs;
	// Frame reconnects them one per frame. Capped to the live tab cap so we
	// never queue a connect that allocateTab would reject. Off ⇒ nil queue ⇒
	// pumpTabRestore is a single length check, so boot is byte-identical.
	if d.Prefs.RestoreTabsOn() {
		q := d.Prefs.OpenTabList()
		if cap := d.Prefs.TabCap(); len(q) > cap {
			q = q[:cap]
		}
		a.restoreQueue = q
	}
	// Auto-connect-on-launch (opt-in, OFF by default): if tab-restore didn't queue
	// anything, dial the last server on the first frames so you land straight on
	// your chosen server — even when no tab was open at shutdown. Tab-restore wins
	// if both are on (it reopens exactly what you had).
	if d.Prefs.AutoConnectOnLaunchOn() && len(a.restoreQueue) == 0 {
		if _, url := d.Prefs.LastServer(); url != "" {
			a.autoConnectPending = true
		}
	}
	a.applyChromePreset(d.Prefs.ChromeTheme()) // #M3: apply the saved client chrome theme at launch
	a.refreshPartColors()                      // per-part layout tints (v1.52.0): parse the saved hexes once
	// Corrupt-prefs notice (#3): if the settings file failed to parse at
	// startup, config quarantined it (renamed aside) so the saver couldn't
	// overwrite the only copy with defaults. Surface a one-time banner via the
	// existing warnLine machinery — it draws on the lobby (drawLobby), where
	// the app starts, as well as the courtroom/char-select.
	if q := d.ConfigQuarantine; q != nil {
		msg := "Settings file was corrupt — reset to defaults."
		if q.BackupPath != "" {
			// Show the backup's basename (the full Windows path would blow
			// past clampLine's cap and truncate the important part).
			msg += " Your old file was saved as " + filepath.Base(q.BackupPath) + " (beside it)."
		}
		a.warnLine = clampLine(msg)
		a.warnAt = time.Now()
	}
	return a
}

// UIScale exposes the global scale percent (main sets the renderer scale
// from it each frame and sizes the logical canvas accordingly): the
// DPI-detected value under auto-HiDPI, the manual setting otherwise.
func (a *App) UIScale() int {
	if a.detectedScalePct > 0 && a.d.Prefs.UIScaleAuto() {
		return a.detectedScalePct
	}
	return a.uiScalePct
}

// autoScaleRefWidth / autoScaleRefHeight are the window size (physical px) treated
// as 100% UI scale. A larger window scales the UI up proportionally (capped) so a
// maximized window on a big display isn't a tiny island of fixed-pixel widgets —
// the recurring "text is too small" reports. SetAutoScaleFromWindow scales by the
// MORE-constrained axis (min of the two) so the UI never overflows a narrow / tall
// (portrait) window. A ~1280x800 window stays at 1:1 (crisp); a 1080p-maximized
// window lifts to ~130%.
const (
	autoScaleRefWidth  = 1280
	autoScaleRefHeight = 800
)

// SetDisplayDPIScale records the display-DPI-derived auto scale (96 dpi = 100%),
// floored at 100 so an unreliable / low GetDisplayDPI reading can never auto-
// SHRINK the UI (#6). Combined with the window-size factor each frame in
// SetAutoScaleFromWindow.
func (a *App) SetDisplayDPIScale(pct int) {
	a.dpiScalePct = max(100, pct)
}

// SetAutoScaleFromWindow updates the auto UI scale from the current PHYSICAL
// window size (only while the auto-scale preference is on). The UI is fixed
// logical pixels, so on a large window it reads as tiny — scaling up with the
// window keeps it proportional (the "text too small on big monitors" reports,
// and why shrinking the window already looked right). It takes the larger of the
// DPI scale and the window-height scale, floors at 100 (never shrink), snaps to
// the step, and caps at the manual bounds. ren.SetScale does the upscale —
// slightly soft on non-integer factors; crisp resolution-independent scaling is
// a roadmap item (docs/ROADMAP.md).
func (a *App) SetAutoScaleFromWindow(winW, winH int32) {
	if !a.d.Prefs.UIScaleAuto() {
		return // manual scale governs; the detected value is unused
	}
	winPct := 100
	if winW > 0 && winH > 0 {
		// Scale by the MORE-constrained axis so a tall / narrow (portrait) window
		// can't drive the UI past its width and clip the fixed-size widgets.
		winPct = min(int(winW)*100/autoScaleRefWidth, int(winH)*100/autoScaleRefHeight)
	}
	pct := max(100, a.dpiScalePct, winPct)
	pct = pct / config.UIScaleStepPercent * config.UIScaleStepPercent
	pct = clampInt(pct, config.MinUIScalePercent, config.MaxUIScalePercent)
	if pct != a.detectedScalePct {
		a.detectedScalePct = pct
		a.ctx.SetUIScale(a.UIScale()) // mouse unprojection follows immediately
	}
}

// setTheater flips the borderless viewport-only mode. The SDL border
// call is legal here — every caller is on the render thread.
func (a *App) setTheater(on bool) {
	a.theaterOn = on
	if a.ctx.win != nil {
		a.ctx.win.SetBordered(!on)
	}
}

// setDND flips Do Not Disturb (mutes callword + friend pings this session) and,
// when the user opted into persistence, saves the state so it survives a restart
// — the default is session-only (clears every launch). The single chokepoint for
// the Settings toggle, the Ctrl+D hotkey, and the badge's click-to-undo.
func (a *App) setDND(on bool) {
	a.dndOn = on
	if a.d.Prefs.DNDPersistOn() {
		a.d.Prefs.SetDNDSaved(on)
	}
}

// saveLayout persists the courtroom layout knobs (debounced saver flushes).
func (a *App) saveLayout() {
	a.d.Prefs.SetLayoutScales(a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct)
	a.d.Prefs.SetOOCScale(a.oocPct) // independent OOC log text size
}

// inputFieldH is the scaled IC/OOC input height.
func (a *App) inputFieldH() int32 {
	return fieldH * int32(a.inputPct) / DefaultScalePct
}

// ensureCharLower keeps the lowercase name cache in sync with the char
// list (shared by char select filtering and the pairing search).
func (a *App) ensureCharLower() {
	if a.sess == nil || len(a.charLower) == len(a.sess.Chars) {
		return
	}
	a.charLower = make([]string, len(a.sess.Chars))
	for i := range a.sess.Chars {
		a.charLower[i] = strings.ToLower(a.sess.Chars[i].Name)
	}
}

// Background runs the per-frame engine work without drawing — the main
// loop calls it instead of Frame while the window is minimized, so the
// session keeps pumping (keepalives answered, queues drained) at zero
// render cost.
func (a *App) Background(dt time.Duration) {
	a.pumpConnection()
	a.pumpBackgroundTabs()
	a.drainWarnings()
	a.pollCharINI()    // drain the async char.ini result here too, so the emote list appears at idle=0 (a skipped courtroom frame never reaches the draw-time poll)
	a.pollLogBrowser() // same for the log browser's off-thread scope load (session list + log area) — else it stays blank at idle=0 until cursor motion
	a.pollUpdate()     // and the self-update result — else "Downloading…" never flips to "Restart to apply" at idle=0 until cursor motion
	if a.room != nil {
		a.room.Update(dt)
	}
	a.d.Audio.Frame()
	a.d.Pump.Frame()
	// AFTER the upload pump, so the touch wins recency over THIS tick's upload
	// burst — exactly what a drawn frame does (the emote row touches these after
	// Pump.Frame too). Touching before the pump let the burst supersede it.
	a.keepActiveAssetsWarm()
	a.keepSceneAssetsWarm() // the un-drawn stage must survive the burst too
	a.d.Store.DrainDestroyQueue()
}

// keepActiveAssetsWarm keeps the user's persistently-shown assets resident while
// the window is minimized — the SELECTED emote's "on" + "off" button images and
// the char icon — so T1's LRU never drops them mid-tab-out (Background draws
// nothing, so a render never touches them; an incoming message then uploads over
// the selected button and it blanks for a moment on restore). Background-only:
// the URL build + map ops here would break the 0-alloc render path (a drawn
// frame's emote row already touches these).
func (a *App) keepActiveAssetsWarm() {
	if a.room == nil || a.sess == nil {
		return
	}
	me := a.activeCharName()
	if me == "" {
		return
	}
	// Futility latch reset: a new character/emote pick warms different bases, so
	// the per-base demand counters start over (see warmMaxDemandsPerBase).
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

// The scene-heal futility budget: how the heal machinery (keepSceneAssetsWarm,
// healScenery, keepActiveAssetsWarm) distinguishes a heal worth repeating from
// a futile one. Two independent bounds, because there are two distinct failure
// shapes and conflating them regressed (v1.56.0 shipped a single demand
// counter, and a merely-SLOW load burned it against ONE in-flight fetch —
// three 250 ms keeper ticks — leaving a healthy scene latched for the epoch):
//
//   - warmMaxHealChurns bounds LANDED-THEN-EVICTED-AGAIN cycles: the base
//     demonstrably loaded and got pushed out again, which means the settled
//     working set exceeds the T1 main tier and every further heal just evicts
//     another warm page (the whole-core decode→upload→evict→re-demand churn —
//     the idle/minimized CPU-burn report). Nothing but a scene change fixes
//     that; stop quickly.
//   - warmMaxHealAsks bounds demands WITHOUT a landing in between: a slow
//     origin, a still-decoding page, or a permanent 404. Roomier, because
//     re-asks here are cheap (in-flight passes collapse; 404s hit the TTL
//     cache) and the case usually resolves itself — and a LANDING resets it
//     (the asks were by definition not futile), so a slow load can never
//     strand a base: its own in-flight fetch still delivers (accepted jobs
//     are never dropped) and the landing re-arms the budget.
//
// Both reset when the warm set changes or a new message ceremony begins.
const (
	warmMaxHealChurns = 3
	warmMaxHealAsks   = 8
)

// sceneHealState is one warm base's heal history within its warm-set epoch —
// the futility budget's memory (see the consts above).
type sceneHealState struct {
	asks         int  // demands since the base last landed
	churns       int  // landed-then-evicted-again cycles
	seenResident bool // the base landed at some point since the last demand
}

// sceneHealAllowed gates one live-scene re-demand against the shared per-scene
// futility budget and counts it. healScenery and keepSceneAssetsWarm share the
// budget: they heal the same bases, and either one alone could sustain the
// over-budget eviction churn. Counters reset on a warm-set change or a new
// ceremony (keepSceneAssetsWarm, which runs on both the drawn and the
// skipped/minimized paths and marks landings via markSceneResident).
func (a *App) sceneHealAllowed(base string) bool {
	st := a.sceneWarmDemands[base]
	if st.seenResident {
		// It landed since the last demand and is missing AGAIN: a genuine
		// eviction-churn cycle — the thing the hard bound exists for. Persist
		// the consumed fold (a set seenResident implies a prior write, so the
		// map exists).
		st.churns++
		st.asks = 0
		st.seenResident = false
		a.sceneWarmDemands[base] = st
	}
	// Exactly warmMaxHealChurns churn cycles get a heal (refused from the
	// N+1th), and warmMaxHealAsks demands may run without a landing between.
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

// markSceneResident records that a demanded base is resident right now — the
// landing proof sceneHealAllowed's asks bound resets on, and the arming edge
// for its churn bound. No-op for bases never demanded (no map growth) and for
// already-marked entries (no map write on the steady state).
func (a *App) markSceneResident(base string) {
	st, ok := a.sceneWarmDemands[base]
	if !ok || (st.seenResident && st.asks == 0) {
		return
	}
	st.seenResident = true
	st.asks = 0
	a.sceneWarmDemands[base] = st
}

// warmBase keeps base resident across a minimized upload burst: touch it (→
// most-recent, the LAST to evict) if it's in T1, else re-demand it — once it has
// evicted, a Get alone can't bring it back, so the asset would stay gone until
// the post-restore render heals it. Re-demands are throttled to the scene
// keeper's cadence and ask-latched per base (warmMaxHealAsks — a plain asks
// bound suffices here: buttons/icons live in the small-UI shield tier where
// eviction ping-pong is structurally impossible, so the only futile shape is a
// permanent 404). A landing clears the counter: the asks were not futile.
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

// sceneWarmRedemandEvery throttles keepSceneAssetsWarm's heal path: a cold
// stage base re-demands at most this often (across the whole scene), so the
// per-frame warm can never spam the pool with duplicate jobs while a sprite
// is still streaming in — the message's own HIGH prefetch stays the loader;
// this is only the safety net for the already-evicted edge.
const sceneWarmRedemandEvery = 250 * time.Millisecond

// keepSceneAssetsWarm pins the LIVE stage's textures at the MRU end every
// tick: background, desk, both character layers (plus the speaker's idle
// base — the imminent talk→idle swap target), and the chat skin. The draw
// path's resolve() only re-Gets on a store-generation change, so after a
// long stable stretch these sit LRU-cold and one big upload burst (a
// char-select scroll, a background tab, the prefetcher) could evict the
// picture currently ON SCREEN — the "window randomly redraws / stage blinks
// for a beat" report. Runs right after Pump.Frame in BOTH the drawn and the
// skipped/minimized paths (recency must win over this tick's burst — same
// reasoning as keepActiveAssetsWarm). Steady state is a handful of map
// probes on the render-thread-owned store: no locks, no allocations, no I/O.
func (a *App) keepSceneAssetsWarm() {
	if a.room == nil && !(a.replaying && a.replayRoom != nil) {
		return // no stage on screen (lobby) — nothing to pin
	}
	sc := a.renderScene()
	// Futility latch resets. (1) A new message / room rebuild swaps the warm
	// set — a fixed array compare, no allocation on the settled steady state.
	// (2) A new ceremony resets even an UNCHANGED set: a blankpost repeating
	// the same emote stages identical bases, which the compare can't see, and
	// begin()'s one-shot HIGH prefetch is skipped for bases resident at that
	// instant — evicted a moment later they'd be loaderless (and latched from
	// the previous epoch) for the whole message.
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
			// Touch → most-recent, the last to evict; a demanded base being
			// resident is also the landing proof the heal budget re-arms on.
			a.d.Store.Get(base)
			a.markSceneResident(base)
			return
		}
		// Evicted (or never landed): re-demand, throttled scene-wide AND
		// futility-latched per base (sceneHealAllowed). Without the latch, a
		// settled scene whose decoded working set exceeds the T1 main tier
		// churned forever: each re-demand fully re-decoded a large animated
		// asset, re-uploaded up to a cap-sized page, and evicted ANOTHER warm
		// base — which the next tick re-demanded, at ~4 whole-core-burning
		// cycles/sec while the app sat idle or minimized (the CPU-burn report;
		// the "stage blinks" report is the same churn's visible face). A bare-
		// spelling sprite may 404 here where the message's fallback chain
		// succeeded — harmless: the 404 cache absorbs it, the latch stops the
		// repeats, and the next message re-runs the full chain.
		if throttleOpen && !demanded && a.sceneHealAllowed(base) {
			a.d.Manager.Prefetch(base, t, network.PriorityHigh)
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

// frameCrashLog is the last-resort diagnostic: a panic that escaped every
// per-feature recover (replay/preview/maker) would otherwise hard-crash the app
// with no trace — the exact "it crashes, no crash log" report. This writes the
// panic + full stack to recordings\scene-maker-crash.log, then RE-PANICS so the
// behaviour is unchanged (it does NOT mask the bug or leave SDL in a half-bound
// state — it just makes the next crash diagnosable).
func (a *App) frameCrashLog() {
	if r := recover(); r != nil {
		writeCrashLog("UNRECOVERED frame panic: ", r)
		panic(r)
	}
}

// SetPump injects the upload pump (built after App for the liveness probe).
func (a *App) SetPump(p *render.Pump) { a.d.Pump = p }

// Screen returns the active screen.
func (a *App) Screen() Screen { return a.screen }

// Room exposes the courtroom (nil before joining).
func (a *App) Room() *courtroom.Courtroom { return a.room }

// IsLiveBase reports whether base belongs to the on-screen message (upload
// budget bypass).
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

// IsLiveScenery reports whether base is the DRAWN stage's background or desk —
// the held-frame bridge's steal gate (render/textures.go onEvict): scenery has
// no hold-previous/thumb fallback, so evicting it mid-scene exposed the black
// stage fill (the black-flash report). Uses the same scene the viewport draws
// (renderScene covers the replay/slideshow overrides). Render thread only —
// onEvict runs on every T1 mutation, all render-thread.
func (a *App) IsLiveScenery(base string) bool {
	if base == "" || (a.room == nil && !(a.replaying && a.replayRoom != nil)) {
		return false
	}
	sc := a.renderScene()
	return base == sc.BackgroundBase || (sc.ShowDesk && base == sc.DeskBase)
}

// --- connection lifecycle -------------------------------------------------------

// Connect dials a server in a NEW tab. Whatever was active parks and
// keeps running in the background (rehearsal disconnects instead — it
// can't background). At the tab cap the connect refuses with a visible
// reason and the current session stays untouched.
func (a *App) Connect(name, wsURL string) {
	a.cancelAutoReconnect() // a deliberate Join/Reconnect takes over from any pending auto-retry
	a.connectWith(name, wsURL, context.Background())
}

// friendlyConnError turns a raw dial error into plain lobby guidance (#9) — the raw
// error still goes to the debug log. Matches substrings of the lowercased error (Go's
// net / crypto-tls / websocket wording is stable enough); falls back to the raw text.
func friendlyConnError(wsURL string, err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "no such host") || strings.Contains(s, "lookup"):
		return "Couldn't find that server — double-check the address."
	case strings.Contains(s, "refused"):
		return "Connection refused — the server may be offline, or the port is wrong."
	case strings.Contains(s, "timeout") || strings.Contains(s, "deadline exceeded") || strings.Contains(s, "timed out"):
		return "Timed out — the server didn't respond (it may be down or unreachable)."
	case strings.Contains(s, "certificate") || strings.Contains(s, "x509") || strings.Contains(s, "tls"):
		msg := "Secure (wss://) connection failed — the server's certificate looks invalid or expired."
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(wsURL)), "wss://") {
			msg += " If the server isn't actually secure, try ws:// instead."
		}
		return msg
	case strings.Contains(s, "network is unreachable") || strings.Contains(s, "no route"):
		return "Network unreachable — check your internet connection."
	case strings.Contains(s, "bad handshake") || strings.Contains(s, "unexpected") || strings.Contains(s, " status") || strings.Contains(s, "426") || strings.Contains(s, "websocket"):
		return "The server didn't accept a WebSocket connection — it may not be a WebSocket (AO2 2.11) server. AsyncAO can't use legacy raw-TCP servers."
	case strings.Contains(s, "unsupported") || strings.Contains(s, "invalid") || strings.Contains(s, "missing") || strings.Contains(s, "parse"):
		return "That address doesn't look right — use ws:// or wss:// (or host:port)."
	default:
		return "Couldn't connect: " + err.Error()
	}
}

// connectWith is Connect with a caller-chosen dial context. Manual joins pass
// context.Background() (Dial's full 10s budget); restore-on-launch passes a
// short timeout so a dead remembered server can't freeze boot for long.
func (a *App) connectWith(name, wsURL string, dialCtx context.Context) {
	a.connErr = ""                              // a fresh attempt clears any stale lobby error/reason
	a.deliberateClose = false                   // a fresh connection starts with no pending deliberate-close intent
	a.lastConnName, a.lastConnURL = name, wsURL // remember for one-click Reconnect on a drop/failure
	a.d.Prefs.SetLastServer(name, wsURL)        // persist for auto-connect / quick-connect on next launch
	a.pinging = false                           // leaving the lobby: don't let a dropped done-sentinel wedge re-ping
	a.parkActive()
	if !a.allocateTab() {
		return // connErr set; lobby shows it
	}
	a.resetSessionState()
	a.serverName = name
	a.serverKey = wsURL
	a.connAt = time.Now()
	// One-time wardrobe migration: the first server joined after the
	// per-server split inherits the old flat collection.
	a.d.Prefs.ClaimLegacyWardrobe(wsURL)
	a.refreshCharKeys()
	a.refreshShownameKeys()
	a.refreshICPhraseKeys()
	a.syncLoginBuffers() // settings/dialog boxes show this server's creds
	// Per-server theme binding: this server always uses that theme.
	a.themeBound = a.d.Prefs.ServerWarmInfoFor(wsURL).Theme
	a.ensureThemeForSession()
	// Case notebook: per-server pins load off-thread, land via the poll
	// (the payload carries the key so it routes even after a tab switch).
	go func(key string) {
		if nb, err := config.LoadNotebook(key); err == nil {
			select {
			case a.notebookRes <- notebookLoad{key: key, nb: nb}:
			default:
			}
		}
	}(wsURL)
	// TLS: default OFF accepts self-signed wss certs (most community AO servers);
	// the power-user Security toggle flips on strict verification.
	conn, err := protocol.Dial(dialCtx, wsURL, protocol.DialOptions{
		SkipTLSVerify: !a.d.Prefs.ValidateTLSCertsOn(),
		Origin:        a.d.Prefs.WSOriginHeader(), // power-user: servers that allowlist their web client's origin ("" = none)
	})
	if err != nil {
		a.connErr = friendlyConnError(wsURL, err) // #9: human guidance, not a raw Go error
		a.pushDebug("connect failed: " + err.Error())
		a.closeActiveTab()
		a.screen = ScreenLobby
		return
	}
	a.conn = conn
	// Wake the render loop the instant a packet lands (experimental
	// event-driven loop; a queued no-op event otherwise — the wake is never
	// treated as user input in either mode).
	conn.SetNotify(PushWake)
	a.lastPing = time.Now()
	a.pkts.reset() // a fresh connection starts a clean packet history
	a.pktConn = conn
	a.sess = courtroom.NewSession(func(p protocol.Packet) error {
		if conn == a.conn { // record only the ACTIVE connection's sends (a parked tab's Ping won't match)
			a.recordPacket(p, true)
		}
		return conn.Send(context.Background(), p)
	}, hdid())
	a.screen = ScreenCharSelect
}

// pumpTabRestore reconnects one remembered tab per frame (restore-on-launch),
// spreading the blocking dials so they never pile into one boot freeze, each on
// a short dial budget. A no-op (single length check) once the queue is empty,
// so it costs nothing after startup and nothing at all when the feature is off.
func (a *App) pumpTabRestore() {
	if len(a.restoreQueue) == 0 {
		return
	}
	tab := a.restoreQueue[0]
	a.restoreQueue = a.restoreQueue[1:]
	ctx, cancel := context.WithTimeout(context.Background(), restoreDialTimeout)
	defer cancel()
	a.connectWith(tab.Name, tab.URL, ctx)
}

// collectOpenTabs snapshots the currently open server tabs (name + ws URL) for
// restore-on-launch: the live active tab from the session fields, parked tabs
// from their stored state. Skips rehearsal/dead/blank-URL slots and dedups by
// URL. Pure over App state (no prefs/IO) so it unit-tests directly. activeTab
// == -1 (sitting on the lobby) simply contributes no "active" entry.
func (a *App) collectOpenTabs() []config.OpenTab {
	out := make([]config.OpenTab, 0, len(a.tabs))
	seen := make(map[string]struct{}, len(a.tabs))
	add := func(name, url string, dead, rehearse bool) {
		if dead || rehearse || url == "" {
			return
		}
		if _, dup := seen[url]; dup {
			return
		}
		seen[url] = struct{}{}
		out = append(out, config.OpenTab{Name: name, URL: url})
	}
	for i, t := range a.tabs {
		if i == a.activeTab {
			add(a.serverName, a.serverKey, false, a.rehearsal)
		} else {
			add(t.state.serverName, t.state.serverKey, t.dead, t.state.rehearsal)
		}
	}
	return out
}

// RememberOpenTabs persists the open tabs for next launch's restore — called
// once at shutdown (before the final SaveNow). No-op unless restore-on-launch
// is enabled, so the feature is truly zero-cost when off.
func (a *App) RememberOpenTabs() {
	if !a.d.Prefs.RestoreTabsOn() {
		return
	}
	a.d.Prefs.SetOpenTabs(a.collectOpenTabs())
}

// Disconnect tears the ACTIVE session down (its tab closes; other tabs
// keep running) and returns to the lobby.
func (a *App) Disconnect() {
	a.cancelAutoReconnect() // teardown cancels any pending retry; the EventDisconnect path re-arms after
	if a.conn != nil {
		a.conn.Close()
	}
	// The server's area music shouldn't outlive the connection (manual
	// disconnect, kick, or a dropped socket all land here).
	if a.d.Audio != nil {
		a.d.Audio.StopMusic()
	}
	a.stopVoiceAudio() // free the voice capture/playback devices with the connection
	a.voiceJoined, a.voiceMicOn = false, false
	// Rehearsal mode ends with the session: reopen the network gate.
	if a.rehearsal {
		a.d.Manager.SetOffline(false)
	}
	// Notebook: flush pending pins off-thread.
	if a.notebook != nil {
		go func(nb *config.Notebook) { _ = nb.Flush() }(a.notebook)
	}
	if a.msRaster != nil {
		a.msRaster.Destroy()
	}
	if a.glyphCache != nil {
		a.glyphCache.Purge() // free the #M5 white-glyph textures
	}
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = nil
		a.d.Viewport.PurgePostFX() // #10: free the cached retro-overlay textures
	}
	// Remember which server this was so the lobby's Reconnect button and
	// auto-reconnect target the session that actually dropped — correct even
	// with other tabs still connected (lastConn* is global, so set-at-connect
	// alone would hold whatever was connected most recently). Guarded on a live
	// socket so rehearsal / an already-dead tab can't claim a phantom target;
	// connectWith seeds lastConn* for the dial-FAILURE case (no session here).
	if a.conn != nil {
		a.lastConnName, a.lastConnURL = a.serverName, a.serverKey
	}
	a.closeActiveTab()
	a.resetSessionState()
	a.selServer, a.descLines = -1, nil
	// Drain in-flight fetches so stale payloads can't land later (polls
	// also key-guard, this just frees the slots).
	select {
	case <-a.iniRes:
	default:
	}
	a.bgPick.show = false
	select {
	case <-a.bgPick.res:
	default:
	}
	a.cancelDownload()   // a download targets this server's assets; stop it
	if a.d.Pool != nil { // nil in headless tests
		a.d.Pool.BumpEpoch() // cancel queued speculation for the old server
	}
	a.updatePresence() // sess is nil now → clears the Discord activity
	// A bound theme leaves with its server; the global pick returns.
	a.ensureThemeForSession()
	a.screen = ScreenLobby
	// Consume the deliberate-close intent: it applied only to THIS teardown. A
	// later unexpected drop on a fresh connection must be treated as unexpected.
	a.deliberateClose = false
}

// applyWindowSize resizes the window to a clamped, recentered size and persists
// it, so a too-big or off-screen window snaps back into view. Leaves fullscreen
// first (a windowed resize is meaningless while fullscreen).
func (a *App) applyWindowSize(w, h int) {
	if a.d.Prefs.WindowFullscreen() {
		a.ctx.SetWindowFullscreen(false)
		a.d.Prefs.SetWindowFullscreen(false)
	}
	uw, uh := a.ctx.WindowDisplayUsable()
	w, h = config.ClampWindowSize(w, h, uw, uh)
	a.ctx.ResizeWindow(w, h)
	a.d.Prefs.SetWindowSize(w, h)
}

// fitWindowToScreen sizes the window to (most of) the display's usable area and
// recenters — the one-click rescue for a window bigger than the monitor.
func (a *App) fitWindowToScreen() {
	uw, uh := a.ctx.WindowDisplayUsable()
	if uw <= 0 || uh <= 0 {
		a.applyWindowSize(config.DefaultWindowW, config.DefaultWindowH)
		return
	}
	a.applyWindowSize(uw, uh) // ClampWindowSize keeps it within the usable bounds
}

// applyFullscreen toggles borderless fullscreen and persists it. Leaving
// fullscreen snaps back to a clamped windowed size so a stranded one can't return.
func (a *App) applyFullscreen(on bool) {
	a.ctx.SetWindowFullscreen(on)
	a.d.Prefs.SetWindowFullscreen(on)
	if !on {
		w, h := a.d.Prefs.WindowSize()
		if w <= 0 || h <= 0 {
			w, h = config.DefaultWindowW, config.DefaultWindowH
		}
		uw, uh := a.ctx.WindowDisplayUsable()
		w, h = config.ClampWindowSize(w, h, uw, uh)
		a.ctx.ResizeWindow(w, h)
	}
}

// toggleFullscreen flips fullscreen — shared by the F11 escape and the Settings
// checkbox.
func (a *App) toggleFullscreen() { a.applyFullscreen(!a.d.Prefs.WindowFullscreen()) }

// effectiveShowname is the name outgoing messages carry: the courtroom
// override box when filled, the persisted Settings showname otherwise.
func (a *App) effectiveShowname() string {
	if s := strings.TrimSpace(a.shownameOverride); s != "" {
		return s
	}
	return a.d.Prefs.SavedShowname()
}

// ensureNameOpts (re)builds the cached name-picker dropdown options — a leading
// "▾" (the no-op the closed control shows) then the saved showname presets —
// rebuilt only when the preset count changes, so the per-frame chat row reads a
// cached slice instead of copying the preset list every draw.
func (a *App) ensureNameOpts() {
	if n := a.d.Prefs.ShownameCount(); a.nameOpts == nil || n != a.nameOptsLen {
		a.nameOptsLen = n
		a.nameOpts = append([]string{"▾"}, a.d.Prefs.ShownameList()...)
	}
}

// pickNameDropdown draws the tiny ▾ showname picker at r and returns the chosen
// preset (or "" if nothing was picked). Shown only when presets exist; reused by
// the IC showname box and the OOC name box.
func (a *App) pickNameDropdown(id string, r sdl.Rect) string {
	if len(a.nameOpts) <= 1 {
		return ""
	}
	if next, changed := a.ctx.Dropdown(id, r, a.nameOpts, 0); changed && next > 0 {
		return a.nameOpts[next]
	}
	return ""
}

// updatePresence pushes (or clears) the Discord activity from the user's
// per-field choices. Cheap — a channel swap; the presence worker owns all
// I/O and silently idles when Discord isn't running.
func (a *App) updatePresence() {
	if a.d.Presence == nil {
		return
	}
	dp := a.d.Prefs.Discord()
	if !dp.Enabled {
		a.d.Presence.Clear()
		return
	}
	if a.sess == nil {
		// In the lobby / server list — keep "Playing AsyncAO" up (the user asked for
		// presence to persist there too, not vanish until you join a room). No
		// server/character/showname applies yet, and no elapsed timer (it'd reset on
		// every connect); just a neutral line.
		a.d.Presence.Set(presence.Activity{Details: "In the lobby"})
		return
	}
	act := presence.Activity{Start: a.connAt, Details: "In court"}
	if dp.ShowServer && a.serverName != "" {
		act.Details = "On " + a.serverName
	}
	var parts []string
	if dp.ShowName {
		if n := strings.TrimSpace(a.effectiveShowname()); n != "" {
			parts = append(parts, n)
		}
	}
	if dp.ShowChar {
		if ch := a.myCharName(); ch != "" {
			if len(parts) > 0 {
				parts = append(parts, "as "+ch)
			} else {
				parts = append(parts, ch)
			}
		}
	}
	if dp.ShowArea && a.curArea != "" {
		parts = append(parts, "— "+a.curArea)
	}
	act.State = strings.Join(parts, " ")
	a.d.Presence.Set(act)
}

// hdid is the device ID AO servers key bans on. It derives from stable
// per-machine/account roots (hwid.Compute) rather than the hostname — see
// internal/hwid for the roots, hashing and graceful fallback.
func hdid() string { return hwid.Compute() }

// pumpConnection drains incoming packets into the session each frame.
func (a *App) pumpConnection() {
	if a.conn == nil || a.sess == nil {
		return
	}
	// Client-initiated keepalive (AO2-Client parity, 45 s): runs from
	// Frame AND Background, so minimized sessions stay alive too.
	if time.Since(a.lastPing) >= keepalivePeriod {
		a.lastPing = time.Now()
		a.sess.Ping()
	}
	// Half-dead write side (#7a): Session.reply records the FIRST failed outgoing
	// write into sendErr and then silently swallows every later packet. A non-nil
	// SendErr means our writes are going into a dead socket (the keepalive above,
	// or any IC send) — surface it and tear down, then auto-reconnect (a write
	// failure is a transport drop, never a ban/kick, so it's not deliberate).
	if err := a.sess.SendErr(); err != nil {
		reason := "connection lost: " + err.Error()
		a.connErr = reason
		a.pushDebug("disconnected: " + reason)
		deliberate := a.deliberateClose
		a.Disconnect()
		if shouldAutoReconnect(reason, deliberate) {
			a.scheduleAutoReconnect()
		}
		return
	}
	for {
		// A handled packet can tear the session down mid-drain: a server
		// kick/ban (KK/KB/BD → EventDisconnect) calls Disconnect(), whose
		// resetSessionState() zeroes conn/sess. Re-check before the select
		// touches a.conn.Incoming()/a.sess.HandlePacket again, or the next
		// iteration nil-derefs (conn.go:73). The plain socket-close path
		// returns immediately below, so only the kick/ban packet hit this.
		if a.conn == nil || a.sess == nil {
			return
		}
		select {
		case p, ok := <-a.conn.Incoming():
			a.uiDirty = true // packets (or the drop below) change UI-visible state — redraw-worthy damage
			if !ok {
				// The Incoming channel closed: a genuine transport drop (Wi-Fi
				// blip, server restart, read error, or the stale-link watchdog
				// giving up). This branch is NOT reached on a deliberate
				// Disconnect (that nils a.conn first, so pumpConnection early-
				// returns) — so unless deliberateClose was set by a refactor
				// mid-teardown, this is exactly the case auto-reconnect exists
				// for (#1: previously this path only Disconnected and never
				// rearmed a retry).
				reason := "connection closed"
				if err := a.conn.Err(); err != nil {
					reason = err.Error()
				}
				a.connErr = reason
				a.pushDebug("disconnected: " + reason)
				deliberate := a.deliberateClose
				a.Disconnect()
				if shouldAutoReconnect(reason, deliberate) {
					a.scheduleAutoReconnect() // Disconnect just cancelled any pending retry
				}
				return
			}
			a.lastPktHdr, a.lastPktAt = p.Header, time.Now()
			a.notePktConn() // reset the packet ring if the active conn changed (tab switch / reconnect)
			a.recordPacket(p, false)
			a.handleSessionEvents(a.sess.HandlePacket(p))
		default:
			return
		}
	}
}

func (a *App) handleSessionEvents(events []courtroom.Event) {
	for _, ev := range events {
		switch ev.Kind {
		case courtroom.EventAssetURL:
			a.rebuildAssetOrigin()
		case courtroom.EventReady:
			a.rebuildAssetOrigin()
			a.prefetchCharIcons()
			a.sendCasingPrefs()
			a.prewarmServer()
			// Remember enough to rehearse this server offline later.
			a.d.Prefs.RememberServerOrigin(a.serverKey, a.sess.AssetURL)
			names := make([]string, len(a.sess.Chars))
			for i := range a.sess.Chars {
				names[i] = a.sess.Chars[i].Name
			}
			a.d.Prefs.RememberServerChars(a.serverKey, names)
			a.autoLoginOnReady()
		case courtroom.EventBackground:
			// Remember it for next visit's pre-warm; the room still
			// consumes the event below (no continue).
			a.d.Prefs.RememberServerBackground(a.serverKey, ev.Text)
		case courtroom.EventMusic:
			// Log "<name> has played a song: <song>" in the IC log like webAO and
			// AO2-Client (handle_song). The room still plays the track below.
			a.logMusicChange(ev)
			a.noteMusicHistory(ev) // M12 slice: session "recently played" history
		case courtroom.EventCharsUpdated:
			a.charLower = nil      // names may have changed; rebuild lazily
			a.rebuildLiveRoster()  // pre-snapshot fallback only
			a.maybeRefetchRoster() // someone joined/left → re-pull the rich /getarea snapshot (debounced)
		case courtroom.EventAreasUpdated:
			a.rebuildLiveRoster()
			a.maybeRefetchRoster() // ARUP head-count moved (covers spectator join/leave)
			a.amICMNow = a.amICM() // the ARUP CM column may have changed — refresh the cached flag
		case courtroom.EventPlayersUpdated:
			a.rebuildLiveRoster()
			a.amICMNow = a.amICM() // a PU may have moved us to another area — refresh the cached flag  // server-pushed PR/PU: the live roster's primary source
			a.maybeRefetchRoster() // a mod still missing IPIDs re-pulls /getareas (self-gated, debounced)
			a.maybeFollowJump()    // follow-a-player (M3): trail the followed UID across areas
			a.recordAreaHistory()  // area history (M3): note our own area into the MRU list
		case courtroom.EventCharPicked:
			// #88 diagnostic: the server's PV echo — the char_id it actually
			// assigned us. If this differs from the CC char_id logged on pick, the
			// desync is on the wire/server; if it matches but the player list still
			// shows the wrong char, it's in how the roster reports it.
			if ev.Int >= 0 && ev.Int < len(a.sess.Chars) {
				a.pushDebug(fmt.Sprintf("server assigned char_id=%d (%s)", ev.Int, a.sess.Chars[ev.Int].Name))
			}
			a.enterCourtroom()
		case courtroom.EventOOC:
			if a.serverKey != "" && a.d.Prefs.ServerIgnoreMatch(a.serverKey, ev.Name) {
				continue // #81: ignored player's OOC — drop it (room ignores OOC, so this is a clean skip)
			}
			// Received private message (Nyathena / Athena attribute the sender in the
			// CT name as "[PM] [UID n] <name>"): file it in its DM thread. It also
			// stays in the OOC log below, so a miss loses nothing.
			if uid, sender, ok := courtroom.ParsePMSender(ev.Name); ok {
				a.routeIncomingPM(uid, sender, ev.Text)
			}
			a.pushOOC(ev.Name+": "+ev.Text, ev.Name)
			if a.d.Prefs.CallwordsOOCOn() && !looksLikeAreaList(ev.Text) { // OOC callwords opt-in (default OFF); /ga roster never self-pings
				names := a.mentionNames()
				a.checkCallwords(ev.Text, names, isSelfName(ev.Name, names))
			}
			a.scanModActionOOC(ev.Name, ev.Text) // #60: optional ban/kick/mute feedback sound
		case courtroom.EventMessage:
			if ev.Message != nil {
				// Our own message echoed back — the server accepted it, so NOW
				// the input clears (keep-until-echo, see noteOwnICEcho). Above
				// the ignore filter: it's our line, filtering is display-only.
				if a.sess != nil && ev.Message.CharID == a.sess.MyCharID {
					a.noteOwnICEcho()
				}
				if a.serverKey != "" && a.ignoreSpeaker(ev.Message) {
					continue // #81: ignored player — drop IC entirely (no log, no sprite/blip via room, no recording)
				}
				fr, fc := a.friendMessage(a.serverKey, ev.Message)
				force := a.d.Prefs.ForceCharNamesOn()
				line, speaker := icLogLineDisplay(ev.Message, force, a.friendNick(ev.Message))
				a.pushIC(line, ev.Message.TextColor, fr, fc, speaker)
				// #2 reactions: stamp this entry with its CONTENT-STABLE ref, computed from the
				// RAW wire fields (CharName + marker-free text) — NOT the display line, which
				// bakes in client-local showname / nick / timestamp and so wouldn't match a
				// peer's ref. A real speaker line becomes a reaction target; track it as the
				// "last message" the React button snapshots, and float any reaction this very
				// message carries (the reactor piggybacks on their next line).
				if speaker != "" {
					ref := courtroom.MakeReactionRef(ev.Message.CharName, courtroom.StripSpriteStyle(ev.Message.Message))
					a.icLog[len(a.icLog)-1].ref = ref
					a.lastReactRef, a.lastReactName = ref, speaker
				}
				if r, ok := courtroom.DecodeReactionMarker(ev.Message.Message); ok {
					a.onIncomingReaction(r)
				}
				a.noteShowname(ev.Message.CharName, ev.Message.Showname) // live-roster name cache
				a.notePairPartner(ev.Message)                            // #20: track each player's current pair
				if sn := ev.Message.SFXName; sn != "" && sn != "0" && sn != "1" {
					a.lastSFXName = sn // M11: remember the most-recent SFX for one-click "Mute last SFX"
				}
				if cn := ev.Message.CharName; cn != "" {
					a.lastBlipChar = cn // M11: remember the most-recent speaker for one-click per-char blip volume
				}
				if fr {
					a.signalFriend(a.serverName, ev.Message)
				}
				a.logDetailed(a.serverName, ev.Message) // detailed transcript (opt-in)
				a.noteEvidencePresented(ev.Message)
				names := a.mentionNames()
				a.checkCallwords(ev.Message.Message, names, isSelfName(ev.Message.CharName, names))
			}
		case courtroom.EventHP:
			// Direction decides the penalty sfx (set_hp_bar compares the
			// new state against the previous one).
			if idx := ev.Int - 1; idx >= 0 && idx < len(a.hpPrev) {
				if ev.Int2 > a.hpPrev[idx] {
					a.playThemeSFX("hp_increased_sfx")
				} else if ev.Int2 < a.hpPrev[idx] {
					a.playThemeSFX("hp_decreased_sfx")
				}
				a.hpPrev[idx] = ev.Int2
			}
		case courtroom.EventWTCE:
			a.handleWTCE(ev.Text, ev.Int)
		case courtroom.EventVoiceCaps:
			// Voice just became available here (VS_CAPS) — point the user at the
			// Voice button once per session (also confirms VS_CAPS reached us).
			if a.sess != nil && a.sess.VoiceAvailable() && !a.voiceCapsAnnounced {
				a.voiceCapsAnnounced = true
				a.pushOOC("[Voice] This area supports voice chat — use the Voice button (bottom row / Extras) to join.", "")
			}
		case courtroom.EventVoiceAudio:
			a.voiceOnAudio(ev.Int, ev.Text) // decode + queue a peer's frame for the mixer
		case courtroom.EventVoicePeers:
			a.voiceReconcilePeers() // someone joined/left voice — sync the decoder set
		case courtroom.EventModcall:
			a.pushOOC("[MOD CALL] "+ev.Text, "")
			a.playThemeSFX("mod_call")
			a.ctx.FlashWindow()
			a.signalModcall(a.serverName, ev.Text)            // desktop toast (opt-in)
			a.autoClipModcall(a.serverName, a.icLog, ev.Text) // freeze IC context for mods (opt-out)
		case courtroom.EventAuth:
			// AO2 surfaces auth transitions as CLIENT lines in the OOC log
			// (on_authentication_state_received).
			switch {
			case ev.Int >= 1:
				a.pushOOC("CLIENT: Logged in as a moderator.", "")
				// Mod now: re-pull /getarea so IPIDs (mod-only, absent from PR/PU)
				// grab onto the roster. The first auto-pull on tab-open can fire
				// BEFORE this auto-login lands, so its reply carried no IPIDs.
				if a.sess != nil {
					a.fetchRoster()
				}
			case ev.Int == 0:
				a.pushOOC("CLIENT: Login unsuccessful.", "")
			default:
				a.pushOOC("CLIENT: You were logged out.", "")
			}
		case courtroom.EventSetPos:
			a.sidePref = ev.Text // SP: the server moved us
		case courtroom.EventCase:
			a.pushOOC("[CASE] "+ev.Text, "")
			if enabled, roles := a.d.Prefs.Casing(); enabled && ev.Int&roles != 0 {
				a.playThemeSFX("case_call")
				a.ctx.FlashWindow()
			}
		case courtroom.EventNotice:
			a.pushOOC("[SERVER] "+ev.Text, "")
			a.ctx.FlashWindow()
		case courtroom.EventEvidence:
			a.evidAsk = nil // list replaced; thumbnail pacing resets
			if a.evidIdx >= len(a.sess.Evidence) {
				a.evidIdx = -1
			}
		case courtroom.EventDisconnect:
			a.connErr = ev.Text
			a.pushDebug("disconnected: " + ev.Text)
			// #60: KK/KB/BD surface here as "Kicked: …" / "Banned: …" — play the
			// matching feedback sound (cooldown-gated, so an auto-reconnect retry
			// loop can't machine-gun it) before the session tears down. The audio
			// device is app-level, so it keeps playing across the disconnect.
			switch {
			case strings.HasPrefix(ev.Text, "Kicked"):
				a.playModActionSFX(render.ModKick)
			case strings.HasPrefix(ev.Text, "Banned"):
				a.playModActionSFX(render.ModBan)
			}
			a.Disconnect()
			// #1: EventDisconnect is ONLY ever a KK/KB/BD kick/ban
			// (session.go:696-701) — the server removing us on purpose. Do NOT
			// auto-reconnect: retrying a ban reads as ban evasion, and re-joining
			// after a kick is bad optics. shouldAutoReconnect returns false for
			// both prefixes; genuine transport drops rearm via the pumpConnection
			// closed-channel / SendErr paths instead.
			if shouldAutoReconnect(ev.Text, a.deliberateClose) {
				a.scheduleAutoReconnect()
			}
			continue
		case courtroom.EventDebug:
			// Protocol-level diagnostics (unhandled headers, dropped MS):
			// the room has no use for these — debug overlay only.
			a.pushDebug("server: " + ev.Text)
			continue
		}
		if a.recActive { // M16: tap the scene event stream for a replay recording
			a.recordEvent(ev)
		}
		a.bufferReplayEvent(ev) // instant-replay rolling buffer (no-op unless the opt-in pref is on)
		if a.room != nil {
			a.room.HandleEvent(ev)
		}
	}
}

// musicLogColor is the IC colour of the "has played a song" line — AO default
// white (0), matching AO2-Client handle_song's p_color.
const musicLogColor = 0

// logMusicChange adds AO2/webAO's "<name> has played a song: <song>" (or "has
// stopped the music") line to the IC log when a real player changed the music.
// System/area music (no valid charID) and area-name transfers don't log. The
// room still plays the track — this only mirrors the on-screen notice.
func (a *App) logMusicChange(ev courtroom.Event) {
	cid := ev.Int
	if a.sess == nil || cid < 0 || cid >= len(a.sess.Chars) {
		return // no player to attribute it to (system/area change or bad id)
	}
	action, song, ok := courtroom.MusicAction(ev.Text)
	if !ok {
		return // an area-name transfer, not a song
	}
	name := ev.Name // the MC packet's showname (field 2)
	if name == "" {
		name = a.sess.Chars[cid].Name // fall back to the character name
	}
	if name == "" {
		name = "Someone"
	}
	line := name + " " + action
	if song != "" {
		disp := song
		if a.d.Prefs.ShowSongURLOn() && ev.Text != "" {
			disp = ev.Text // opt-in: the full song URL / raw track instead of the name
		}
		line += ": " + disp
	}
	a.pushIC(line, musicLogColor, false, -1, "") // system line: no friend tint, no name-tint/pair
}

func icLogLine(m *protocol.ChatMessage, forceChar bool) string {
	// Strip inline markup so the log reads like the chatbox (no raw \cN / { }).
	return icSpeakerName(m, forceChar) + ": " + icMessageBody(m)
}

// icMessageBody is an IC message's display text for the log: markup stripped (no raw
// \cN / { }) and known :shortcode: inline emotes (#18) expanded to their emoji — the same
// expansion the live chatbox does (Courtroom.InlineEmote), so the log and the box agree.
func icMessageBody(m *protocol.ChatMessage) string {
	return courtroom.ExpandInlineEmotes(courtroom.StripChatMarkup(m.Message), inlineEmoteFor)
}

// icSpeakerName is the displayed name an IC log line is prefixed with — the
// showname, or the character (force-char-names / no showname). Stored on the
// entry so per-speaker name colours tint exactly that prefix.
func icSpeakerName(m *protocol.ChatMessage, forceChar bool) string {
	name := m.Showname
	if forceChar || name == "" {
		name = m.CharName // force-char-names mirrors the chatbox (anti-impersonation)
	}
	return name
}

// friendNick returns the personal nickname set for m's speaker if they're a
// friend with one (#82 follow-up) — independent of the friend-highlight signals,
// since a nickname is a label, not an alert. Showname-else-character match, the
// identity the MS wire carries.
func (a *App) friendNick(m *protocol.ChatMessage) string {
	if m == nil || a.serverKey == "" {
		return ""
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	_, _, nick := a.d.Prefs.ServerFriendInfo(a.serverKey, name)
	return nick
}

// icLogLineDisplay builds the IC log line text and its speaker field. When a
// friend has a nickname AND we're not in force-char (anti-impersonation) mode,
// the line reads "nick (showname): msg" so you see your own label for them — but
// the SPEAKER field stays the REAL name, so double-click-to-pair (UID lookup) and
// the per-speaker colour still key off the true identity. Pure, for testing.
func icLogLineDisplay(m *protocol.ChatMessage, force bool, nick string) (line, speaker string) {
	speaker = icSpeakerName(m, force)
	if !force && nick != "" {
		return nick + " (" + speaker + "): " + icMessageBody(m), speaker
	}
	return icLogLine(m, force), speaker
}

// rebuildAssetOrigin wires the URL builder to local mounts or the server's
// asset URL, in that priority (the no-streaming checkbox wins).
func (a *App) rebuildAssetOrigin() {
	if enabled, mounts := a.d.Prefs.LocalAssets(); enabled && len(mounts) > 0 {
		local := assets.NewLocalFetcher(mounts)
		a.urls = courtroom.NewURLBuilder(local.BaseURL()).WithCharCase(a.charCasingFor(local.BaseURL()))
		log.Printf("ui: local asset mode over %d mounts", len(mounts))
		return
	}
	origin := ""
	if a.sess != nil {
		origin = a.sess.AssetURL
	}
	if origin == "" {
		a.connErr = "server provided no asset URL — enable local assets in Settings"
		return
	}
	a.urls = courtroom.NewURLBuilder(origin).WithCharCase(a.charCasingFor(origin))
	if a.rehearsal {
		return // offline: no DNS warm, no manifest fetch
	}
	if host := hostOfURL(origin); host != "" {
		a.d.Client.PreResolve(context.Background(), strings.Split(host, ":")[0])
	}
	a.fetchManifestAsync()
}

// rehearsalBadge labels the offline mode in the courtroom viewport.
const rehearsalBadge = "REHEARSAL — offline, nothing sends"

// startRehearsal enters the offline cached-asset browser for a server
// visited before: the remembered character list + asset origin, with the
// manager's network gate closed — emotes and sprites play from T2/T3,
// misses just say so. Disconnect (or any connect) exits the mode.
func (a *App) startRehearsal(name, key string, info config.ServerWarmInfo) {
	a.parkActive()
	if !a.allocateTab() {
		return
	}
	a.resetSessionState()
	a.serverName = name + " (rehearsal)"
	a.serverKey = key
	a.connAt = time.Now()
	a.rehearsal = true
	a.d.Manager.SetOffline(true)
	a.sess = courtroom.NewRehearsalSession(info.Origin, info.Chars)
	a.rebuildAssetOrigin()
	a.refreshCharKeys()
	a.refreshShownameKeys()
	a.refreshICPhraseKeys()
	a.themeBound = info.Theme // rehearsal wears the server's bound theme too
	a.ensureThemeForSession()
	a.screen = ScreenCharSelect
	go func(k string) {
		if nb, err := config.LoadNotebook(k); err == nil {
			select {
			case a.notebookRes <- notebookLoad{key: k, nb: nb}:
			default:
			}
		}
	}(key)
	a.pushDebug("rehearsal: offline browse of " + name + " (cached assets only)")
}

// pickCharacter routes a char-select pick: live sessions ask the server
// (CC → PV → EventCharPicked); rehearsal resolves locally — no PV will
// ever arrive offline.
func (a *App) pickCharacter(idx int) {
	// Diagnostic for the "player list shows the wrong character" report (#88):
	// log the exact char_id we put on the wire in CC, named from our own list, so
	// a playtest with the Debug overlay on shows whether the index we SEND matches
	// the character picked (vs the server-assigned id logged at EventCharPicked).
	if idx >= 0 && idx < len(a.sess.Chars) {
		a.pushDebug(fmt.Sprintf("char pick → CC char_id=%d (%s)", idx, a.sess.Chars[idx].Name))
	}
	if a.sess.Rehearsal {
		a.sess.MyCharID = idx
		a.enterCourtroom()
		return
	}
	a.sess.PickCharacter(idx)
}

// fetchManifestAsync grabs <origin>/extensions.json (webAO convention —
// every server ships its own format mix) and seeds this host's learned
// formats so the first asset of each class costs exactly one probe even
// stone cold. Default ON (Settings → auto-detect); switching it off keeps
// the manual per-type probing in full control, and manual orders still
// govern servers without a manifest either way.
// extProfileFor returns the effective format profile (extensions.json text) for
// an asset host: a user-set per-server profile wins; otherwise the AO official
// vanilla base/server gets the bundled vanilla example. "" = no profile, so the
// server's own extensions.json is fetched (and the global default still covers a
// server that ships none).
func (a *App) extProfileFor(host string) string {
	if p := a.d.Prefs.ExtProfile(host); p != "" {
		return p
	}
	if a.isOfficialVanilla(host) {
		return assets.BundledVanillaManifestJSON
	}
	return ""
}

// isOfficialVanilla matches the AO official vanilla base by asset host, or the
// official vanilla server by the connected server key, so joining it uses the
// vanilla format profile (the example).
func (a *App) isOfficialVanilla(assetHost string) bool {
	h := strings.ToLower(assetHost)
	return strings.Contains(h, "attorneyoffline.de") ||
		strings.Contains(strings.ToLower(a.serverKey), "vanilla.aceattorneyonline.com")
}

// casingProbeResult carries an asset-casing probe's outcome back to the render thread.
type casingProbeResult struct {
	host   string
	casing uint8
}

// charCasingFor resolves the character-folder casing for origin's host: the manual pref as-is, or —
// only when the user selected CharCaseAuto — the LEARNED casing for that host (lowercase until it's
// learned). Resolving Auto here keeps it out of the SDL-free URLBuilder.
func (a *App) charCasingFor(origin string) uint8 {
	c := a.d.Prefs.AssetCharCasing()
	if c != courtroom.CharCaseAuto {
		return c // manual (incl. the lowercase default) — no probing, no learning
	}
	if host := hostOfURL(origin); host != "" {
		if learned, ok := a.charCaseLearned[host]; ok {
			return learned
		}
	}
	return courtroom.CharCaseLower // Auto, not yet learned → the safe default
}

// maybeProbeCasing kicks off a ONE-TIME casing probe for the current server, but ONLY when the user
// has selected Auto casing, the char list is known, and this host hasn't been probed yet. A cheap
// per-frame no-op otherwise, so it costs nothing unless Auto is on.
func (a *App) maybeProbeCasing() {
	if a.d.Prefs.AssetCharCasing() != courtroom.CharCaseAuto || a.casingProbing || a.sess == nil {
		return
	}
	origin := a.urls.Origin()
	host := hostOfURL(origin)
	if host == "" || !strings.HasPrefix(origin, "http") || a.casingProbedHost == host {
		return // not a network origin, or already handled this host this session
	}
	name := ""
	for i := range a.sess.Chars {
		if n := strings.TrimSpace(a.sess.Chars[i].Name); n != "" {
			name = n
			break
		}
	}
	if name == "" {
		return // no character names yet — try again next frame
	}
	a.casingProbing = true
	a.casingProbedHost = host
	go a.probeCasing(host, name, origin)
}

// probeCasing fetches character name's icon in each casing (lowercase → first-cap → title) and
// reports the FIRST that exists — so Auto stays on lowercase unless lowercase actually 404s. Two
// common icon formats are tried per casing; the whole burst is one-time per server (the sanctioned
// learning exception to one-probe-per-asset). Background goroutine — touches only the result channel.
func (a *App) probeCasing(host, name, origin string) {
	ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
	defer cancel()
	winner := courtroom.CharCaseLower
	for _, cc := range []uint8{courtroom.CharCaseLower, courtroom.CharCaseFirstCap, courtroom.CharCaseTitle} {
		base := courtroom.NewURLBuilder(origin).WithCharCase(cc).CharIcon(name)
		found := false
		for _, ext := range []string{config.ExtPNG, config.ExtWebP} {
			if _, err := a.d.Manager.FetchRaw(ctx, base+ext); err == nil {
				found = true
				break
			}
		}
		if found {
			winner = cc
			break
		}
	}
	select {
	case <-a.casingRes:
	default:
	}
	a.casingRes <- casingProbeResult{host: host, casing: winner}
}

// pollCasingProbe lands a casing probe: record the learned casing for its host and, if we're still
// on that server, apply it to the URL builder so subsequent fetches use it (a no-op when it stayed
// lowercase). Render thread.
func (a *App) pollCasingProbe() {
	select {
	case res := <-a.casingRes:
		a.casingProbing = false
		if a.charCaseLearned == nil {
			a.charCaseLearned = map[string]uint8{}
		}
		a.charCaseLearned[res.host] = res.casing
		if hostOfURL(a.urls.Origin()) == res.host { // still on this server → apply now
			a.urls = a.urls.WithCharCase(res.casing)
		}
		a.pushDebug("asset casing auto-detect: " + res.host + " → " + charCaseName(res.casing))
	default:
	}
}

// charCaseName is a short label for a resolved casing (debug log).
func charCaseName(c uint8) string {
	switch c {
	case courtroom.CharCaseFirstCap:
		return "First cap"
	case courtroom.CharCaseTitle:
		return "Title"
	default:
		return "lowercase"
	}
}

func (a *App) fetchManifestAsync() {
	origin := a.urls.Origin()
	if a.manifestFor == origin || !strings.HasPrefix(origin, "http") {
		return
	}
	a.manifestFor = origin
	host := hostOfURL(origin)
	// A per-host format profile (user-set, or the built-in official-vanilla example)
	// applies even when auto-detect is OFF — it's an explicit per-server override, not
	// the autodetect fetch. It seeds INSTANTLY (no network race) so the very first probe
	// already uses the right formats; it wins over the global default for THIS host only.
	if profile := a.extProfileFor(host); profile != "" {
		if m, err := assets.ParseManifest([]byte(profile)); err == nil {
			n := m.SeedLearned(a.d.Prefs, host)
			a.d.Resolver.WarmFromPrefs()
			a.pushDebug(fmt.Sprintf("format profile: seeded %d classes for %s (instant)", n, host))
			return
		}
	}
	// The NETWORK fetch of the server's own extensions.json is gated on auto-detect
	// (the per-server profile above is not — it always applies).
	if !a.d.Prefs.FormatAutoDetect() {
		return
	}
	url := origin + assets.ManifestFileName
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		res := manifestFetch{host: host}
		data, err := a.d.Manager.FetchRaw(ctx, url)
		switch {
		case err != nil:
			res.err = err
		default:
			m, perr := assets.ParseManifest(data)
			if perr != nil {
				res.err = perr
			} else {
				res.seeded = m.SeedLearned(a.d.Prefs, host)
			}
		}
		select {
		case <-a.manifestRes:
		default:
		}
		a.manifestRes <- res
	}()
}

// pollManifest lands autodetect results: republish the resolver snapshot
// (now containing the seeds) and report on the debug lane.
func (a *App) pollManifest() {
	select {
	case res := <-a.manifestRes:
		if res.err != nil {
			a.pushDebug("extensions.json: " + res.err.Error() + " — formats learn per probe instead")
			return
		}
		a.d.Resolver.WarmFromPrefs()
		a.pushDebug(fmt.Sprintf("extensions.json: seeded %d format classes for %s", res.seeded, res.host))
	default:
	}
}

// fontLoad is one font-override read result: file bytes in chain order
// plus a one-line verdict for the settings status.
type fontLoad struct {
	names []string
	data  [][]byte
	note  string
}

// fontFileMaxBytes bounds one override font file (CJK TTCs run ~20 MiB;
// past 64 MiB it's not a font, it's a mistake).
const fontFileMaxBytes = 64 << 20

// applyFontConfig installs the IC/OOC override font chain from prefs through one
// resolver, so the launch path and the live Settings toggles can never disagree:
// the dyslexia toggle wins (embedded OpenDyslexic, set synchronously — no disk),
// else the manual font-path chain (read off-thread), else the built-in font.
// Called at launch and on every change to either source.
func (a *App) applyFontConfig() {
	switch fontChainSource(a.d.Prefs.DyslexiaFontOn(), a.d.Prefs.FontPaths()) {
	case fontSourceDyslexia:
		a.ctx.SetFontChain([]string{dyslexiaFontName}, [][]byte{openDyslexicOTF})
		// Font-everywhere (opt-in): the same face drives the chrome too;
		// otherwise make sure the chrome is back on the embedded font.
		if a.d.Prefs.FontEverywhereOn() {
			a.ctx.SetChromeFont(openDyslexicOTF)
		} else {
			a.ctx.SetChromeFont(nil)
		}
		a.rasterText = "" // re-raster the visible message in the new font
	default:
		// Manual font paths win; otherwise the active theme's own bundled font
		// (an AO theme that ships a .ttf — #6, Crystalwarrior); otherwise the
		// embedded font (loadFontChainAsync clears on an empty string).
		paths := strings.TrimSpace(a.d.Prefs.FontPaths())
		if paths == "" {
			paths = a.themeFontFile
		}
		a.loadFontChainAsync(paths)
	}
}

// loadFontChainAsync reads the override font files off-thread (semicolon-
// or comma-separated paths, chain order, ≤ fontChainCap) and lands them on
// fontRes. An empty list clears the override immediately.
func (a *App) loadFontChainAsync(raw string) {
	paths := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	if len(paths) == 0 {
		a.ctx.SetFontChain(nil, nil)
		a.ctx.SetChromeFont(nil) // no override chain → the chrome is embedded again
		a.rasterText = ""        // re-raster the visible message with the embedded font
		return
	}
	if len(paths) > fontChainCap {
		paths = paths[:fontChainCap]
	}
	go func() {
		var res fontLoad
		var failed []string
		for _, p := range paths {
			p = strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"`))
			if p == "" {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil || len(data) == 0 || len(data) > fontFileMaxBytes {
				failed = append(failed, filepath.Base(p))
				continue
			}
			res.names = append(res.names, filepath.Base(p))
			res.data = append(res.data, data)
		}
		switch {
		case len(res.data) == 0:
			res.note = "Font override: no readable font files — keeping the embedded font"
		case len(failed) > 0:
			res.note = fmt.Sprintf("Font chain: %s (skipped: %s)",
				strings.Join(res.names, " → "), strings.Join(failed, ", "))
		default:
			res.note = "Font chain: " + strings.Join(res.names, " → ")
		}
		select {
		case a.fontRes <- res:
		default: // a newer load superseded this one
		}
	}()
}

// pollFontChain lands override font bytes on the render thread: install
// the chain (fonts build lazily per scale) and force a chat re-raster.
func (a *App) pollFontChain() {
	select {
	case res := <-a.fontRes:
		// Precedence guard: if the dyslexia toggle flipped on while this manual
		// chain was loading off-thread, the embedded font already won — don't
		// let the stale result clobber it.
		if len(res.data) > 0 && !a.d.Prefs.DyslexiaFontOn() {
			a.ctx.SetFontChain(res.names, res.data)
			// Font-everywhere (opt-in): the chain's PRIMARY face drives the
			// chrome too; otherwise restore the embedded chrome (the toggle
			// may have just been switched off). On a failed open the chrome
			// keeps the embedded font — say so instead of silently ignoring.
			if !a.d.Prefs.FontEverywhereOn() {
				a.ctx.SetChromeFont(nil)
			} else if !a.ctx.SetChromeFont(res.data[0]) {
				res.note += " — chrome font failed to open, whole-UI font stays embedded"
			}
		}
		a.rasterText = ""
		settings.statusLine = clampLine(res.note)
		a.pushDebug(res.note)
	default:
	}
}

// prefetchCharIcons warms the first screenfuls of icons speculatively.
// Deliberately capped: the rest load on demand from drawCharCell, because
// a 4000-char burst would only shed itself out of the low lane.
func (a *App) prefetchCharIcons() {
	if a.sess == nil || a.urls.Origin() == "" {
		return
	}
	for i, c := range a.sess.Chars {
		if i >= charIconWarmup {
			break
		}
		a.d.Manager.Prefetch(a.urls.CharIcon(c.Name), assets.AssetTypeCharIcon, network.PriorityLow) // AssetType: CharIcon
	}
}

func (a *App) enterCourtroom() {
	if a.sess == nil {
		return
	}
	// FRESH entry from char select: a wardrobe pick rides in as pendingIni (the
	// slot was auto-claimed); a plain pick starts clean, and the side waits on the
	// new char.ini. A tab REACTIVATION must NOT run these — activateTab calls
	// buildRoom directly so the parked iniswap + /pos override survive (they were
	// leaking across tabs: re-entering a tab un-iniswapped you and reset your /pos).
	a.iniChar = a.pendingIni
	a.pendingIni = ""
	a.sidePref = ""
	// FRESH entry / character switch starts at the first emote (page 0). The
	// reactivation path (activateTab → buildRoom) deliberately skips this so each
	// tab keeps its own emote selection; pollCharINI then clamps rather than
	// resets, so the parked index survives the reload.
	a.emoteIdx, a.emotePage = 0, 0
	a.buildRoom()
}

// buildRoom (re)constructs the render-coupled courtroom from the CURRENT session
// state WITHOUT touching the per-entry bits (iniChar / sidePref) — so a tab
// reactivation rebuilds the room while the parked iniswap and /pos ride along
// (loadCharINI below re-applies the active, possibly iniswapped, character).
// enterCourtroom layers the fresh-entry resets on top; activateTab calls this
// directly. The court-extras reset + hpPrev re-arm stay here on PURPOSE: they must
// run on reactivation too, because background tabs apply HP via HandlePacket but
// never route it to the penalty-sfx logic, so hpPrev must re-arm at the session's
// current bars or the next HP packet fires a spurious sound.
func (a *App) buildRoom() {
	if a.sess == nil {
		return
	}
	a.room = courtroom.NewCourtroom(a.urls, a.d.Manager, a.sess, a.d.Audio)
	a.room.SFXMuted = func(name string) bool { return a.d.Prefs.IsSFXMuted(name) }        // M11 per-SFX mute (reads live prefs)
	a.room.BlipVolumeFor = func(char string) int { return a.d.Prefs.BlipVolumeFor(char) } // M11 per-character blip volume (reads live prefs)
	a.wireRoomCharMeta(a.room)                                                            // per-character blips + chatbox skins from the speaker's char.ini
	a.room.InlineEmote = inlineEmoteFor                                                   // #18: expand :shortcode: emotes in the chatbox (registry lives in ui)
	a.room.SpriteReady = func(base string) bool { return a.d.Store.Contains(base) }       // wait-mode residency probe (same-thread T1 map hit; the flags ride applyTimingToRoom)
	// Per-server audio: apply THIS server's volume profile (or the global one) now,
	// so the music re-seeded below plays at the right level and switching between
	// two in-court tabs carries each server's own volumes / muted blips.
	a.applyAudioVolumes()
	urls := a.urls
	mgr := a.d.Manager
	a.room.Predictor = assets.NewPrefetcher(func(character, emote string) {
		if emote == "" {
			emote = "normal" // no chain signal yet: the default loop
		}
		// Warm BOTH the idle and the (b) talk sprite through the full spelling
		// chain — bare-named packs only answer the bare/(b) spellings, and the
		// talking loop is the sprite seen FIRST when the predicted speaker
		// starts. Speculative variant: a total miss must NOT raise a missing-
		// asset warning for a sprite no one demanded. PriorityLow so the
		// speculation sheds under backpressure (§10).
		idle := urls.Emote(character, emote, courtroom.EmoteIdle)
		talk := urls.Emote(character, emote, courtroom.EmoteTalk)
		mgr.PrefetchChainSpeculative(idle, urls.EmoteAlts(character, emote, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker, idle)
		mgr.PrefetchChainSpeculative(talk, urls.EmoteAlts(character, emote, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker, talk)
	})
	a.room.Predictor.SetAggressiveness(a.d.Prefs.PrefetchAggressiveness()) // #100 predictive-prefetch level
	a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
	a.d.Viewport.OnPreanimStart = a.room.NotifyPreanimStarted // extend the fallback for long decoded preanims (phase-guarded — safe if a preview later shares this viewport)
	if a.sess.Background != "" {
		a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: a.sess.Background})
	}
	// Resume the area's current song into the fresh room — symmetric with the background
	// above. The track was announced (MC) before this room existed (the join handshake, or
	// a tab reactivation), so without this re-seed the music fell silent on (re)entry while
	// the background came back. A direct HandleEvent (not handleSessionEvents) so it plays
	// without re-logging "has played a song". (It restarts the song from the top — acceptable.)
	if a.sess.MusicTrack != "" {
		a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMusic, Text: a.sess.MusicTrack})
	}
	a.applyTimingToRoom()
	a.pushRealizationToRoom()
	// The third session→room re-seed, beside the background and the song: the
	// last IC message, re-staged SETTLED (idle sprite, full text, no ceremony).
	// A rebuilt room used to come back BLANK even though the stage was
	// mid-conversation when it parked (tab switch) or messages landed while
	// backgrounded / at char select; now it shows what a live watcher would
	// have ended on. AFTER applyTimingToRoom on purpose: begin() bakes
	// ForceCharNames / HideSpriteStyles / ReduceMotion into the scene
	// (showname vs char name, transmitted styles, motion strip), so the
	// restore must run under the user's prefs, not NewCourtroom's zero values.
	if m := a.stageRestoreMsg(); m != nil {
		a.room.RestoreMessage(m)
	}
	a.d.Prefs.RememberServerChar(a.serverKey, a.myCharName())
	// Court-extras state is per-room: splash/badge clear, penalty-sfx
	// direction tracking re-arms at the session's current bar values.
	a.wtceName, a.testimonyOn = "", false
	a.hpPrev = [2]int{a.sess.HPDef, a.sess.HPPro}
	a.showModcall, a.modReason = false, ""
	a.showEvid, a.evidEditing, a.evidPresent, a.evidIdx = false, false, false, -1
	a.evidAsk = nil
	a.evShowImg = ""
	a.screen = ScreenCourtroom
	a.loadCharINI()
	a.updatePresence()         // character (and server) just became known
	a.liveDetailsArea = "\x00" // force a fresh rich-roster pull for the new session
	a.rebuildLiveRoster()      // seed the live player list from the handshake's CharsCheck
}

// stageRestoreMsg is the message buildRoom re-stages into a rebuilt room — the
// session's last IC message, unless the ACTIVE session's ignore list drops its
// speaker (parity with handleSessionEvents, which `continue`s past
// room.HandleEvent for ignored speakers — #81: no log, no sprite). nil = leave
// the stage blank. Split out so the gate is unit-pinnable without a room.
// Single-seed caveat: an ignored LAST speaker shadows the previous non-ignored
// line (the session can't keep "last non-ignored under the active tab's
// filter" — the list is per-tab UI state), so that corner restores blank: the
// pre-restore status quo, never a wrong render.
func (a *App) stageRestoreMsg() *protocol.ChatMessage {
	if a.sess == nil {
		return nil
	}
	m := a.sess.LastIC
	if m == nil || a.ignoreSpeaker(m) {
		return nil
	}
	return m
}

// --- multi-server floating client (passes 2a–2c + the float rework) ---------
// A pinned BACKGROUND tab renders as a free-floating, movable + resizable "second
// client" window (clientWin, the floatWin pattern) overlaying the full-size
// primary courtroom — drag it ANYWHERE, resize it, overlap the primary however
// you like (a strict superset of the old fixed split). It carries that server's
// live stage + chat log + IC input; you type into either client (click to focus).
// The pinned room runs on courtroom.NopAudio so the single music stream stays
// with the primary; its second viewport shares the texture store (origin-
// qualified keys, so two servers' assets never clash).

const splitInputH = 30 // pinned client's IC input row height (pass 2c)
const (
	clientWinDefW = 520 // floating client window default size
	clientWinDefH = 470
	clientWinMinW = 360 // resize floor: stage + log + input stay usable
	clientWinMinH = 300
	// Full-theme view zoom: 1 = the whole client shrunk to fit the window, up to
	// clientZoomMax magnification (scroll to zoom around the cursor, drag to pan).
	clientZoomMin  = 1.0
	clientZoomMax  = 6.0
	clientZoomStep = 1.15 // wheel multiplier per notch
	// clientClickSlop is the max cursor travel (logical px) for a press+release on the
	// floating client to count as a CLICK ("control this server") rather than a pan drag.
	clientClickSlop = 4
)

// splitActive reports whether the floating client window should render: a pinned
// background tab with a live session + room (and not disconnected).
func (a *App) splitActive() bool {
	return a.splitTab != nil && a.splitRoom != nil && a.splitVP != nil &&
		a.splitTab.state.sess != nil && !a.splitTab.dead
}

// clearSplit tears the floating client down (keeps the viewport for reuse) and
// frees the full-theme render target + the pinned tab's chat raster (built by the
// full-view pass; it lives in the tab's sessionState, so this tab is its sole owner).
func (a *App) clearSplit() {
	if a.splitTab != nil && a.splitTab.state.msRaster != nil {
		a.splitTab.state.msRaster.Destroy()
		a.splitTab.state.msRaster = nil
	}
	a.splitTab, a.splitRoom = nil, nil
	if a.ctx != nil && a.ctx.focusID == "ic-split" {
		a.ctx.focusID = "" // the client's field is gone — don't leave focus dangling on it
	}
	if a.clientTex != nil {
		_ = a.clientTex.Destroy()
		a.clientTex = nil
	}
}

// controlPinnedClient makes the floating (pinned) server the LIVE, fully interactive
// courtroom and demotes the current primary into the float — the "click to control"
// gesture. The two servers trade places; both stay on screen, you drive whichever you
// clicked. Reuses the proven tab machinery (activateTab + pinToSplit), so the new
// primary is the REAL courtroom — real emotes, IC bar, every button — with no second
// interactive-render machinery (which would collide widget ids / leak App-global state).
func (a *App) controlPinnedClient() {
	if !a.splitActive() {
		return
	}
	pinned := a.splitTab
	pi := -1
	for i, t := range a.tabs {
		if t == pinned {
			pi = i
			break
		}
	}
	if pi < 0 {
		return
	}
	prev := a.activeTab
	a.activateTab(pi) // parks the current primary, activates the pinned (this clears the split)
	if prev >= 0 && prev < len(a.tabs) && a.tabs[prev] != a.splitTab {
		a.pinToSplit(a.tabs[prev]) // the old primary becomes the new floating view
	}
}

// clientControlClick requests a control-swap when the floating client's view rect gets
// a CLICK (press+release with little travel — a drag there pans/zooms instead). The
// swap is deferred to the next frame top (pendingControlSwap) so we never mutate the
// active session/room mid-render.
func (a *App) clientControlClick(view sdl.Rect) {
	c := a.ctx
	if c.clicked && !a.clientPanning && pointIn(c.mouseX, c.mouseY, view) &&
		absInt(int(c.mouseX-c.downX))+absInt(int(c.mouseY-c.downY)) <= clientClickSlop {
		a.pendingControlSwap = true
	}
}

// pinToSplit pins a BACKGROUND tab as the floating client window (toggle:
// re-pinning the same tab closes it). Builds a throwaway room over the tab's OWN
// session + URL builder with NopAudio, plus a second viewport over the shared
// store, and seeds the current background + song + last message (settled) so
// the window isn't blank on pin.
func (a *App) pinToSplit(t *courtTab) {
	if t == nil || t.state.sess == nil || t.dead {
		return
	}
	if a.splitTab == t { // toggle off
		a.clearSplit()
		return
	}
	if a.splitVP == nil {
		a.splitVP = render.NewViewport(a.d.Store)
	}
	a.splitTab = t
	a.splitRoom = courtroom.NewCourtroom(t.state.urls, a.d.Manager, t.state.sess, courtroom.NopAudio{})
	a.splitVP.OnPreanimDone = a.splitRoom.NotifyPreanimDone
	a.splitVP.OnPreanimStart = a.splitRoom.NotifyPreanimStarted
	a.clientZoom, a.clientPanX, a.clientPanY = clientZoomMin, 0.5, 0.5 // fresh pin starts at fit
	if t.state.sess.Background != "" {
		a.splitRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: t.state.sess.Background})
	}
	if t.state.sess.MusicTrack != "" {
		a.splitRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventMusic, Text: t.state.sess.MusicTrack}) // NopAudio: silent
	}
	// Last message too, settled — same blank-until-someone-talks fix as the
	// bg+song seeds. Deliberately UNfiltered: the live background pump routes
	// this tab's events to splitRoom without the ignore gate (which keys on the
	// ACTIVE tab's server anyway), so the pin seed matches what the pane shows.
	a.splitRoom.RestoreMessage(t.state.sess.LastIC)
}

// placeClientAt positions the floating client window centred under (mx,my) — used
// by the drag-tab tear-off so the window lands where you drop it (Chrome-style).
// Uses the window's current width if it was resized, else the default.
func (a *App) placeClientAt(mx, my int32) {
	w := a.clientWin.w
	if w <= 0 {
		w = clientWinDefW
	}
	a.clientWin.x = mx - w/2
	a.clientWin.y = my - floatTitleH/2
	a.clientWin.placed = true
}

// clientWinRect is the floating client window's clamped screen rect.
func (a *App) clientWinRect(w, h int32) sdl.Rect {
	return a.clientWin.rect(clientWinDefW, clientWinDefH, clientWinMinW, clientWinMinH, w, h)
}

// drawFloatClient draws the pinned server as a movable + resizable floating
// "second client" window over the primary courtroom: a title bar (drag handle +
// Full/Compact toggle + ✕ close), then either the COMPACT body (stage + log +
// input) or the FULL-THEME view (the whole pinned UI, view-only, + the input
// strip). Shares the per-frame press edge from drawFloatingPanels (so one press
// moves/grabs one window).
func (a *App) drawFloatClient(w, h int32, pressed *bool) {
	if !a.splitActive() {
		return
	}
	c := a.ctx
	r := a.clientWinRect(w, h)
	c.Fill(r, ColBackground)
	c.Border(r, ColAccent)
	// Title bar = drag handle + Full/Compact toggle + close.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	if a.clientHdrName != a.splitTab.state.serverName { // memoized: no per-frame concat
		a.clientHdrName = a.splitTab.state.serverName
		a.clientHdr = "▣ " + a.clientHdrName
	}
	c.LabelClipped(r.X+10, r.Y+8, r.W-102, a.clientHdr, ColAccent)
	// Full ⇄ Compact: show the whole theme (view-only) or just stage+log+input.
	tgl := sdl.Rect{X: r.X + r.W - 92, Y: r.Y + 5, W: 60, H: floatTitleH - 10}
	tglLabel := "Full view"
	if a.clientFull {
		tglLabel = "Compact"
	}
	if c.Button(tgl, tglLabel) {
		a.clientFull = !a.clientFull
	}
	c.TooltipAfter("cli-full", tgl, "Show the WHOLE theme (emote grid, buttons…) — view-only; chat in the strip below. Roughly doubles render cost while open.")
	if c.Button(sdl.Rect{X: r.X + r.W - 26, Y: r.Y + 5, W: 20, H: floatTitleH - 10}, "x") {
		a.clearSplit()
		return
	}
	a.floatWinDrag(&a.clientWin, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 98, H: floatTitleH}, pressed)
	// Bottom-right resize grip — handled before the body so a corner press resizes
	// rather than landing in the log / input beneath it.
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.clientWin, grip, r, clientWinMinW, clientWinMinH, pressed)
	body := sdl.Rect{X: r.X, Y: r.Y + floatTitleH, W: r.W, H: r.H - floatTitleH}
	if a.clientFull && a.canRenderFullClient() {
		a.drawClientFull(w, h, body, pressed)
	} else {
		a.drawClientBody(body)
	}
	a.drawResizeGrip(grip)
}

// canRenderFullClient gates the full-theme render off the primary's exclusive
// modes (theater / either layout editor): those reskin drawCourtroom globally, so
// the pinned pass would fight them. Compact view is shown instead while they're on.
func (a *App) canRenderFullClient() bool {
	return !a.theaterOn && !a.classicEdit && !a.layoutEdit
}

// drawClientBody lays the pinned server's live stage (4:3, capped), chat log, and IC
// input into the client window's body rect. Clicking the stage takes control of that
// server (the full, real courtroom); the strip lets you fire a quick line without
// swapping.
func (a *App) drawClientBody(r sdl.Rect) {
	c := a.ctx
	inputRow := sdl.Rect{X: r.X, Y: r.Y + r.H - splitInputH, W: r.W, H: splitInputH}
	sceneH := r.W * 3 / 4
	if maxH := (r.H - splitInputH) * 55 / 100; sceneH > maxH {
		sceneH = maxH
	}
	stage := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: sceneH}
	a.splitVP.Render(c.Ren, &a.splitRoom.Scene, stage)
	a.clientControlClick(stage) // click the stage → take control of this server
	logTop := stage.Y + stage.H + 3
	logRect := sdl.Rect{X: r.X, Y: logTop, W: r.W, H: inputRow.Y - logTop}
	c.Fill(sdl.Rect{X: logRect.X, Y: logRect.Y - 2, W: logRect.W, H: 1}, ColPanelHi) // rule above the log
	a.drawSplitLog(logRect, &a.splitTab.state)
	a.drawSplitInput(inputRow)
}

// drawClientFull renders the WHOLE pinned client (the full theme — emote grid,
// buttons, themed chatbox, everything) into the window: the pinned drawCourtroom is
// drawn to clientTex (view-only) and blitted into the view area, with a live IC input
// strip below it so you can still chat. Scroll to ZOOM (around the cursor) and drag to
// PAN for a closer look; double-click resets to fit.
func (a *App) drawClientFull(w, h int32, body sdl.Rect, pressed *bool) {
	c := a.ctx
	inputRow := sdl.Rect{X: body.X, Y: body.Y + body.H - splitInputH, W: body.W, H: splitInputH}
	view := sdl.Rect{X: body.X, Y: body.Y, W: body.W, H: body.H - splitInputH}
	a.renderFullClientTexture(w, h)
	if a.clientTex == nil {
		c.LabelClipped(view.X+10, view.Y+10, view.W-20, "Full-theme view unavailable on this renderer.", ColTextDim)
		a.drawSplitInput(inputRow)
		return
	}
	a.handleClientZoomPan(view, pressed) // scroll = zoom (cursor-anchored), drag = pan
	a.clientControlClick(view)           // a plain click on the client = take control of that server
	src := a.clientViewSrc()             // sub-rect of the texture from zoom + pan
	_ = c.Ren.Copy(a.clientTex, &src, &view)
	// "Click to control" hint (top-left pill) — the rendered theme is a live view; a
	// click makes this server the real, fully-interactive courtroom (swaps with primary).
	const ctlHint = "▶ Click to control this server (buttons + chat)"
	hintPill := sdl.Rect{X: view.X, Y: view.Y, W: c.TextWidth(ctlHint) + 12, H: 18}
	c.Fill(hintPill, sdl.Color{R: 0, G: 0, B: 0, A: 150})
	c.LabelClipped(hintPill.X+6, hintPill.Y+3, view.W-12, ctlHint, ColAccent)
	// Zoom readout / hint (memoized so the per-frame draw never Sprintfs), on a small
	// dark pill for legibility over the rendered client.
	pct := int(a.clientZoom*100 + 0.5)
	if pct != a.clientZoomLblPct {
		a.clientZoomLblPct = pct
		a.clientZoomLbl = fmt.Sprintf("Zoom %d%%  ·  scroll to zoom · drag to pan", pct)
	}
	pill := sdl.Rect{X: view.X, Y: view.Y + view.H - 18, W: c.TextWidth(a.clientZoomLbl) + 12, H: 18}
	c.Fill(pill, sdl.Color{R: 0, G: 0, B: 0, A: 150})
	c.LabelClipped(pill.X+6, pill.Y+3, view.W-12, a.clientZoomLbl, ColAccent)
	a.drawSplitInput(inputRow) // quick-chat strip (sendICSplit) — fire a line without taking control
}

// clientViewSrc is the sub-rectangle of clientTex shown in the window, derived from
// the zoom factor and pan centre. At zoom 1 it's the whole texture (shrunk to fit);
// zoomed in it's a centred-on-pan window that magnifies.
func (a *App) clientViewSrc() sdl.Rect {
	z := a.clientZoom
	if z < clientZoomMin {
		z = clientZoomMin
	}
	tw, th := float64(a.clientTexW), float64(a.clientTexH)
	sw, sh := tw/z, th/z
	sx := a.clientPanX*tw - sw/2
	sy := a.clientPanY*th - sh/2
	sx = clampF(sx, 0, tw-sw)
	sy = clampF(sy, 0, th-sh)
	return sdl.Rect{X: int32(sx), Y: int32(sy), W: int32(sw), H: int32(sh)}
}

// handleClientZoomPan applies wheel-zoom (anchored at the cursor) and drag-pan over
// the full-view area, and double-click-to-reset. Pan/zoom are clamped so the source
// window never leaves the texture.
func (a *App) handleClientZoomPan(view sdl.Rect, pressed *bool) {
	c := a.ctx
	if a.clientZoom < clientZoomMin {
		a.clientZoom = clientZoomMin
	}
	hover := pointIn(c.mouseX, c.mouseY, view)
	if hover && c.wheelY != 0 { // zoom around the cursor
		old := a.clientZoom
		nz := old * clientZoomStep
		if c.wheelY < 0 {
			nz = old / clientZoomStep
		}
		nz = clampF(nz, clientZoomMin, clientZoomMax)
		if nz != old {
			src := a.clientViewSrc()
			fx := float64(c.mouseX-view.X) / float64(view.W)
			fy := float64(c.mouseY-view.Y) / float64(view.H)
			texX := float64(src.X) + fx*float64(src.W) // texture point under the cursor
			texY := float64(src.Y) + fy*float64(src.H)
			tw, th := float64(a.clientTexW), float64(a.clientTexH)
			sw, sh := tw/nz, th/nz
			a.clientZoom = nz
			a.clientPanX = (texX - fx*sw + sw/2) / tw // keep that point under the cursor
			a.clientPanY = (texY - fy*sh + sh/2) / th
			a.clampClientPan()
		}
		c.wheelY = 0 // consumed
	}
	if a.clientZoom > clientZoomMin { // drag to pan (only meaningful when zoomed)
		if *pressed && hover {
			*pressed = false
			a.clientPanning = true
			a.clientPanGrabX, a.clientPanGrabY = c.mouseX, c.mouseY
			a.clientPanBaseX, a.clientPanBaseY = a.clientPanX, a.clientPanY
		}
		if !c.mouseDown {
			a.clientPanning = false
		}
		if a.clientPanning {
			tw, th := float64(a.clientTexW), float64(a.clientTexH)
			sw, sh := tw/a.clientZoom, th/a.clientZoom
			a.clientPanX = a.clientPanBaseX - float64(c.mouseX-a.clientPanGrabX)/float64(view.W)*sw/tw
			a.clientPanY = a.clientPanBaseY - float64(c.mouseY-a.clientPanGrabY)/float64(view.H)*sh/th
			a.clampClientPan()
		}
	} else {
		a.clientPanning = false
		a.clientPanX, a.clientPanY = 0.5, 0.5
	}
}

// clampClientPan keeps the pan centre so the zoomed source window stays inside the
// texture (its half-extent is 0.5/zoom in normalized space).
func (a *App) clampClientPan() {
	half := 0.5 / a.clientZoom
	a.clientPanX = clampF(a.clientPanX, half, 1-half)
	a.clientPanY = clampF(a.clientPanY, half, 1-half)
}

// clampF clamps a float64 to [lo, hi] (lo wins if the range is inverted).
func clampF(v, lo, hi float64) float64 {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// renderFullClientTexture renders the pinned client's full drawCourtroom into
// clientTex at the logical screen size (w,h) — so the theme lays out exactly as the
// primary — for the shrink-blit. The pinned session (incl. its own chat-raster cache,
// which lives in sessionState) + live room + viewport are temp-swapped into the
// embedded slots, input is neutralized, and a.screen / ctx.fieldSeq / ctx.tipText are
// snapshotted+restored so the VIEW-ONLY pass can't leak navigation, Tab-focus order,
// or tooltips into the primary. View-only is deliberate: the kit is single-instance,
// so a fully interactive second courtroom would collide widget ids AND leak App-global
// writes (a.screen, the zoom-% knobs, warnings) — interaction is via the control bar
// (drawClientControls), which drives only the isolated pinned sessionState.
func (a *App) renderFullClientTexture(w, h int32) {
	c := a.ctx
	if w <= 0 || h <= 0 {
		return
	}
	if a.clientTex == nil || a.clientTexW != w || a.clientTexH != h {
		if a.clientTex != nil {
			_ = a.clientTex.Destroy()
			a.clientTex = nil
		}
		tex, err := c.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
		if err != nil {
			return
		}
		a.clientTex, a.clientTexW, a.clientTexH = tex, w, h
	}

	// Bind the target and draw at 1:1 logical scale. The render-target + scale restore
	// is DEFERRED so even a panic inside drawCourtroom can't leave the screen rendering
	// into the texture (a black primary) — the worst silent failure of this path.
	prevTarget := c.Ren.GetRenderTarget()
	if c.Ren.SetRenderTarget(a.clientTex) != nil {
		return
	}
	defer func() {
		_ = c.Ren.SetRenderTarget(prevTarget)
		s := float32(a.UIScale()) / 100 // restore main's per-frame render scale
		if s <= 0 {
			s = 1 // never strand the renderer at scale 0 (UIScale unset)
		}
		_ = c.Ren.SetScale(s, s)
	}()
	_ = c.Ren.SetScale(1, 1)
	_ = c.Ren.SetDrawColor(0, 0, 0, 255)
	_ = c.Ren.Clear()

	// --- swap the pinned client into the embedded slots ---
	// The chat-raster cache (msRaster + its keys) lives in sessionState, so the swap
	// below already gives the pinned pass its OWN raster — no thrash with the primary's
	// and no separate per-instance copy (a separate copy would alias splitTab.state's
	// pointer and double-free on clearSplit).
	savedSess := a.sessionState
	savedVP := a.d.Viewport
	savedScreen := a.screen // view-only pass must not change the primary's screen…
	savedTip := c.tipText   // …tooltip…
	savedSeqLen := len(c.fieldSeq)
	in := a.snapshotInput() // …focus order, or handle any input

	a.sessionState = a.splitTab.state // promotes a.sess / a.icInput / a.emote* / msRaster … to the pinned tab
	a.room = a.splitRoom              // drive the pinned LIVE room (a parked tab's sessionState.room is nil)
	a.d.Viewport = a.splitVP
	a.pinnedPass = true

	a.drawCourtroom(w, h)

	// --- capture the pinned pass's side effects, then restore the primary ---
	a.pinnedPass = false
	a.room = nil                      // a parked tab keeps no room — don't persist splitRoom into its slot
	a.splitTab.state = a.sessionState // capture emote/scroll/raster/etc. side effects of the view-only pass
	a.sessionState = savedSess        // restores a.room + a.sess + msRaster + everything promoted
	a.d.Viewport = savedVP
	a.screen = savedScreen
	c.tipText = savedTip
	if len(c.fieldSeq) > savedSeqLen {
		c.fieldSeq = c.fieldSeq[:savedSeqLen] // drop the pinned client's field ids from Tab-cycle
	}
	a.restoreInput(in)
	// (render target + scale restored by the deferred func above)
}

// ctxInput snapshots the per-frame Ctx input fields so the view-only full-client
// pass can be run with every click/key/wheel signal cleared (mouse parked far
// off-screen → hovering() is false everywhere), then restored. Opt-in path, so the
// small value copy is fine.
type ctxInput struct {
	mouseX, mouseY, downX, downY, wheelY     int32
	clicked, dblClick, tripleClick           bool
	rightClicked                             bool
	mouseDown, middleHeld                    bool
	backspace, enter, tabPressed, escPressed bool
	keyPressed, hotkey                       sdl.Keycode
	typed, pasted, dropped, dragID           string
	copyReq, cutReq, selectAll, wheelTaken   bool
	undoReq, redoReq                         bool
}

func (a *App) snapshotInput() ctxInput {
	c := a.ctx
	in := ctxInput{
		mouseX: c.mouseX, mouseY: c.mouseY, downX: c.downX, downY: c.downY, wheelY: c.wheelY,
		clicked: c.clicked, dblClick: c.dblClick, tripleClick: c.tripleClick, rightClicked: c.rightClicked,
		mouseDown: c.mouseDown, middleHeld: c.middleHeld,
		backspace: c.backspace, enter: c.enter, tabPressed: c.tabPressed, escPressed: c.escPressed,
		keyPressed: c.keyPressed, hotkey: c.hotkey,
		typed: c.typed, pasted: c.pasted, dropped: c.dropped, dragID: c.dragID,
		copyReq: c.copyReq, cutReq: c.cutReq, selectAll: c.selectAll, wheelTaken: c.wheelTaken,
		undoReq: c.undoReq, redoReq: c.redoReq,
	}
	c.mouseX, c.mouseY = -30000, -30000 // park off-screen: every hovering()/pointIn is false
	c.downX, c.downY, c.wheelY = -30000, -30000, 0
	c.clicked, c.dblClick, c.tripleClick, c.rightClicked = false, false, false, false
	c.mouseDown, c.middleHeld = false, false
	c.backspace, c.enter, c.tabPressed, c.escPressed = false, false, false, false
	c.keyPressed, c.hotkey = 0, 0
	c.typed, c.pasted, c.dropped, c.dragID = "", "", "", ""
	c.copyReq, c.cutReq, c.selectAll, c.wheelTaken = false, false, false, false
	c.undoReq, c.redoReq = false, false
	return in
}

func (a *App) restoreInput(in ctxInput) {
	c := a.ctx
	c.mouseX, c.mouseY, c.downX, c.downY, c.wheelY = in.mouseX, in.mouseY, in.downX, in.downY, in.wheelY
	c.clicked, c.dblClick, c.tripleClick, c.rightClicked = in.clicked, in.dblClick, in.tripleClick, in.rightClicked
	c.mouseDown, c.middleHeld = in.mouseDown, in.middleHeld
	c.backspace, c.enter, c.tabPressed, c.escPressed = in.backspace, in.enter, in.tabPressed, in.escPressed
	c.keyPressed, c.hotkey = in.keyPressed, in.hotkey
	c.typed, c.pasted, c.dropped, c.dragID = in.typed, in.pasted, in.dropped, in.dragID
	c.copyReq, c.cutReq, c.selectAll, c.wheelTaken = in.copyReq, in.cutReq, in.selectAll, in.wheelTaken
	c.undoReq, c.redoReq = in.undoReq, in.redoReq
}

// drawSplitInput draws the pinned pane's IC field. It edits the PINNED tab's own
// icInput (its sessionState, not the embedded primary's), and on Enter routes to
// sendICSplit, which momentarily swaps that state into the embedded slot so the
// untouched sendIC sends on the pinned conn. The field id "ic-split" is distinct
// from the primary "ic", so the kit's ID-keyed focus makes it click-to-focus for
// free — no side-swap. 0-alloc: icFieldFonts is nil for ASCII and the placeholder
// is a constant.
func (a *App) drawSplitInput(r sdl.Rect) {
	c := a.ctx
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, ColPanelHi) // rule above the input
	box := sdl.Rect{X: r.X + 6, Y: r.Y + 5, W: r.W - 12, H: r.H - 10}
	s := &a.splitTab.state
	primary, emoji := a.icFieldFonts(s.icInput)
	var send bool
	s.icInput, send = c.TextFieldEmoji("ic-split", box, s.icInput, "Chat in the pinned server — click to focus", primary, emoji)
	if send {
		a.sendICSplit(0)
	}
}

// sendICSplit sends an IC message on the PINNED (right-pane) server. Every field
// sendIC reads lives in sessionState (verified: emotes/emoteIdx, pair*, icColor,
// charBlips, evid*, lastSent*, shownameOverride, sidePref…), and sendIC + all its
// helpers + handleChatCommand never touch a.room — so we momentarily swap the
// pinned tab's state into the embedded slot, reuse the untouched sendIC (its
// SendChat then targets the pinned conn), capture the side effects back into the
// tab, and restore the primary. Synchronous + single-threaded: no frame renders
// and pumpBackgroundTabs cannot interleave between the two swaps.
func (a *App) sendICSplit(shout int) {
	if a.splitTab == nil {
		return
	}
	savedFocus := a.ctx.focusID
	saved := a.sessionState
	a.sessionState = a.splitTab.state
	a.sendIC(shout)
	a.splitTab.state = a.sessionState // capture cleared input, lastSent* markers, emote roll, rate-limit stamp
	a.sessionState = saved
	// sendIC's random-emote path (off by default) refocuses "ic"; keep the caret
	// in the right pane so the user can keep typing where they were.
	if savedFocus == "ic-split" {
		a.ctx.focusNext = "ic-split"
	}
}

// splitLogRow is one wrapped, coloured line of the pinned pane's chat log.
type splitLogRow struct {
	text string
	col  sdl.Color
}

// drawSplitLog renders the pinned session's IC log (read-only, newest at the
// bottom) into r. Wrapping is cached by (width, log seq), so a steady frame is
// 0-alloc; it rebuilds only when a message lands or the pane resizes.
func (a *App) drawSplitLog(r sdl.Rect, s *sessionState) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := r.W - 8
	if wrapW != a.splitWrapW || s.icLogSeq != a.splitWrapSeq {
		a.splitWrapW, a.splitWrapSeq = wrapW, s.icLogSeq
		a.splitWrapRows = a.splitWrapRows[:0]
		for i := range s.icLog {
			e := &s.icLog[i]
			col := ColText
			if e.color > 0 {
				col = render.TextColor(e.color)
			}
			for _, ln := range c.WrapText(e.text, wrapW, 0) {
				a.splitWrapRows = append(a.splitWrapRows, splitLogRow{text: ln, col: col})
			}
		}
	}
	rows := a.splitWrapRows
	clipPrev, clipHad := c.pushClip(r)
	y := r.Y + r.H - lineH // newest line pinned to the bottom, drawing upward
	for ri := len(rows) - 1; ri >= 0; ri-- {
		if y < r.Y-lineH {
			break
		}
		c.LabelClippedFont(font, r.X+4, y, wrapW, rows[ri].text, rows[ri].col)
		y -= lineH
	}
	c.popClip(clipPrev, clipHad)
}

// crossfadeDur is the speaker-swap blend the viewports get: the pref, zeroed
// under Reduce motion (a fade is motion — the accessibility floor wins).
func (a *App) crossfadeDur() time.Duration {
	if a.d.Prefs.ReduceMotion() {
		return 0
	}
	return time.Duration(a.d.Prefs.CrossfadeMs()) * time.Millisecond
}

// --- frame pacing (the GPU-burn fix) -------------------------------------------
// The loop used to re-render + present the whole UI every pass: vsync tied it to
// the monitor (144/165 Hz laptop panels = a full-screen composite 165×/sec while
// IDLE), and where vsync doesn't block (small windowed presents on some drivers)
// it spun far past that (high GPU use even in a tiny window). FramePace hands
// the main loop a per-frame budget instead: the foreground cap while you're
// interacting or anything is animating, the idle rate when the client is a
// static image, the unfocused rate when another window has focus. Input snaps it
// back to full instantly (NoteInput + a grace window), so responsiveness is
// untouched — a flat low cap would be a band-aid; only wasted redraws go.

// inputGraceFrameDuration is one "frame" of the post-input full-rate hold — the
// InputGraceFrames pref counts these. A fixed 60 fps reference, so the frame
// count means the same thing regardless of the active cap. After a click/key the
// rate holds full for InputGraceFrames of them, then drops straight back to idle
// (the playtest ask was "1 frame, not a whole second"). Mouse MOTION is separate
// — it keeps its own short motionInputGrace.
const inputGraceFrameDuration = time.Second / 60

// NoteInput marks "the user just interacted" — the main loop calls it whenever
// an SDL event arrives, and wantsFullRate holds full rate through the grace.
func (a *App) NoteInput() { a.lastInputAt = time.Now() }

// NoteAnimating marks that THIS draw pass rendered a self-driven, time-stepped
// UI animation — theme chrome, a splash, a looping badge, the layout editor's
// ghost, or any FUTURE widget that tweens on its own clock. It is the general
// hook for "uncap for the duration of any self-driven UI animation": while a
// draw keeps calling it, SkipFrame won't skip AND FramePace paces at the ACTIVE
// cap (both loop modes), so the motion stays smooth; the frame the draw stops
// calling it, the screen falls back to idle. Retrospective and self-sustaining
// (each animating frame re-arms the next). For an INSTANT off-frame change (a
// packet updated a list, a screen switched) set a.uiDirty instead — that earns
// exactly one follow-up frame. Alloc-free; call it from the draw pass.
func (a *App) NoteAnimating() { a.frameAnimChrome = true }

// audioFineTick is the cadence the courtroom is advanced — and its blips played —
// at while a message types, independent of the (possibly much slower) PRESENT rate.
// ~60 Hz: fine enough that a fast blip stream never audibly quantizes, cheap because
// it's logic + audio only (no GPU). The playtest ask: blips should sound as if the
// UI were at 60 fps even with the frame rate capped low (down to 1 fps).
const audioFineTick = time.Second / 60

// AudioActive reports whether the LIVE courtroom is streaming audio-timed events
// (a message typing → blips firing). Replay, the scene maker and GIF export drive
// their own rooms at their own pacing, so they're excluded — only the live blip
// stream needs the fine audio cadence. A static-but-open courtroom is NOT active,
// so it still parks at true idle.
func (a *App) AudioActive() bool {
	if a.replaying || a.makerOpen || a.gifExporting || a.room == nil {
		return false
	}
	return a.room.AudioActive()
}

// AudioPaceActive reports whether this pass's pacing sleep should be spent advancing
// the courtroom at the fine audio cadence (Background sub-steps) instead of one long
// sleep: a message is streaming blips AND the present budget is slower than the fine
// tick, so a single sleep would batch a whole present-period of blips into one
// instant (the "blips only on every screen refresh at a 1 fps cap" report). budget
// is the pass's total intended sleep. Render thread; alloc-free.
func (a *App) AudioPaceActive(budget time.Duration) bool {
	return budget > audioFineTick && a.AudioActive()
}

// AudioFineTick is the fine audio-advance cadence, exported for the main loop's
// sub-step sleep.
func (a *App) AudioFineTick() time.Duration { return audioFineTick }

// MarkRoomPreAdvanced records that the loop already advanced the courtroom this
// present cycle (audio-paced sub-stepping), so the next Frame draws without
// re-advancing the room (it's current) while still advancing the UI. Render thread.
func (a *App) MarkRoomPreAdvanced() { a.roomPreAdvanced = true }

// motionInputGrace holds full rate briefly after BARE pointer motion under the
// experimental loop: long enough that continuous movement (hover sweeps, drags
// — their motion stream keeps re-arming it) renders at full rate throughout,
// short enough that waving the mouse over dead space stops costing frames
// almost as soon as it stops. Clicks/keys/wheel keep the InputGraceFrames hold.
const motionInputGrace = 200 * time.Millisecond

// NoteMotion marks a pointer-motion-only pass. Under the experimental loop it
// arms the short motion grace; under the classic loop motion is plain input
// (byte-identical pacing to before).
func (a *App) NoteMotion() {
	if a.d.Prefs != nil && a.d.Prefs.EventDrivenLoopOn() {
		// Per-event motion redraw (default ON since v1.55.1): don't arm the full-rate
		// grace. The motion event already earns its single wake-frame (SkipFrame refuses on
		// sawEvent), then the loop re-parks — so a moving cursor redraws once per
		// motion event instead of holding full rate through a 200 ms tail. Saves
		// power on hover sweeps that would otherwise pin the rate to the cap.
		if a.d.Prefs.MotionRedrawPerEventOn() {
			return
		}
		a.lastMotionAt = time.Now()
		return
	}
	a.lastInputAt = time.Now()
}

// staticTalkFPS paces a message whose stage is ALL STATIC art (single-frame
// sprites, no effects): the only motion is the typewriter's text crawl, which
// reveals runes at ≥ ~40 ms — 30 fps samples it cleanly, so full rate is pure
// waste there (the playtest ask: "if it's just an image it shouldn't render at
// high fps"). The effective rate never drops below the user's idle rate and
// never exceeds their cap.
const staticTalkFPS = 30

// maxHousekeepingGap bounds how long the event-driven loop may park doing
// NOTHING: even with the idle redraw rate off (config.FPSOff), Background must
// keep pumping this often (keepalive pacing at 45 s, queue drains), but a wait
// that times out at this floor pumps Background ONLY — it does not render a
// frame. This replaces the old fixed 2 fps "heartbeat" that forced a real frame
// every 500 ms regardless of the idle-rate setting (the two "fought", per the
// report): the idle rate is the single idle cadence now, so 0/off means a
// genuinely static screen redraws zero times until real damage or a deadline.
const maxHousekeepingGap = 500 * time.Millisecond

// paceBudget converts an fps knob to a per-frame budget (0 = uncapped). Used for
// the ACTIVE cap, which is never "off" (0 fps while interacting is meaningless).
func paceBudget(fps int) time.Duration {
	if fps <= 0 {
		return 0
	}
	return time.Second / time.Duration(fps)
}

// rateBudget converts a rate knob to a per-frame budget WITH the widened
// domain's trichotomy — paceBudget alone can't express it. A 0 budget means
// "uncapped", but a knob set to config.FPSOff means "off / never redraw in this
// state", the exact opposite — so every idle/unfocused consumer asks here and
// branches on off BEFORE treating a 0 as uncapped:
//
//	FPSUnlimited (∞) → (0, false)   // uncapped, same as paceBudget's 0
//	FPSOff           → (0, true)    // off — the caller decides (park, or the cap)
//	N                → (1s/N, false)
func rateBudget(fps int) (budget time.Duration, off bool) {
	switch fps {
	case config.FPSOff:
		return 0, true
	case config.FPSUnlimited:
		return 0, false
	}
	if fps <= 0 {
		return 0, false // stray non-positive (never a sentinel) → uncapped, never off
	}
	return time.Second / time.Duration(fps), false
}

// HardCapBudget is the INVIOLABLE minimum time between two rendered frames for
// the current focus state: the active FPS cap when focused, the unfocused cap
// when not. Unlike FramePace's adaptive tiers (idle/talk/anim, which only ever
// SLOW rendering down toward a floor), this is the hard ceiling the user set —
// the main loop sleeps it UNINTERRUPTIBLY, so an input flood (above all mouse
// motion, which streams an event every few ms) can never interrupt the pace and
// drive the loop past the cap. The "caps are ALWAYS obeyed" contract lives here;
// it also enforces the unfocused cap even when FramePace lifts an unfocused
// animation toward the active rate. 0 = uncapped (the ∞ sentinel — no floor).
func (a *App) HardCapBudget(focused bool) time.Duration {
	if focused {
		// The active cap has no "off" (0 fps while interacting is meaningless);
		// ∞ → 0 (uncapped), N → 1s/N.
		return paceBudget(a.d.Prefs.FPSCap())
	}
	unf, off := rateBudget(a.d.Prefs.UnfocusedFPS())
	if off {
		// Unfocused "off" means "don't redraw while tabbed out" — the static skip
		// handles the idle case. A frame that renders anyway (an audible ceremony)
		// still obeys SOMETHING: fall back to the active cap as the backstop
		// ceiling, mirroring FramePace's unfocused-off branch (unf = full).
		return paceBudget(a.d.Prefs.FPSCap())
	}
	return unf // ∞ → 0 (uncapped); N → 1s/N
}

// clampDur bounds d to [lo, hi] (lo = the fastest allowed budget).
func clampDur(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// roomBusy reports a message mid-ceremony on the ACTIVE room — typewriter,
// preanim, shout, linger, or anything still queued.
func (a *App) roomBusy() bool {
	return a.room != nil && (a.room.Phase() != courtroom.PhaseIdle || a.room.QueueLen() > 0)
}

// serverTimersLive reports any visible courtroom timer chip (TI packets) — its
// clock readout changes every second, so a static skip would freeze it.
func (a *App) serverTimersLive() bool {
	if a.sess == nil {
		return false
	}
	for i := range a.sess.Timers {
		if a.sess.Timers[i].Visible {
			return true
		}
	}
	return false
}

// talkBudget is the frame budget while a message plays over all-static art:
// fast enough that AT MOST ONE typewriter rune reveals per frame, so every blip
// boundary lands its own frame — blips fire from the per-frame room Update, so
// a frame slower than the rune interval audibly coalesces them (playtest: "at
// a lower framerate the blips are ALSO at a lower framerate"). Base cadence is
// staticTalkFPS; faster text (the speed slider / {} spans) tightens it; full
// (the cap's budget) still bounds it.
func (a *App) talkBudget(full time.Duration) time.Duration {
	talk := paceBudget(staticTalkFPS)
	if a.room != nil {
		if iv := a.room.Typewriter.Interval; iv > 0 && iv < talk {
			talk = iv
		}
	}
	if talk < full {
		talk = full // never above the cap
	}
	return talk
}

// FramePace returns the frame budget the main loop should pace to (0 = no cap):
// the sleep between CONSECUTIVE rendered frames. focused is the window's input-
// focus state. Tiers, fastest first:
//   - full rate: interaction or effects genuinely in flight (wantsFullRate)
//   - anim-chrome rate: a self-driven UI animation (NoteAnimating census) —
//     the cap, backstopped to a finite ~60 fps when the cap is ∞ (perpetual
//     censuses must never zero the budget — see backstopBudget)
//   - talk rate: a message is playing → the text crawl's cadence (talkBudget),
//     which a fast speaker's frame flip may only TIGHTEN (earliest deadline
//     wins, so a slow sprite loop never drags the typewriter/blips down — the
//     "text renders at idle fps over animated sprites" report)
//   - content cadence: only stage animations move → the next frame flip
//   - active cap: a rendered-but-idle frame. Under the event-driven loop a
//     genuinely idle screen SKIPS and parks (NextWakeDelay honours the idle
//     rate there), so reaching this tier means a lone damage frame or a screen
//     that declares itself non-skippable — pacing it at the active cap keeps the
//     post-render sleep from injecting a whole idle period of input latency. The
//     classic loop idle-renders here, so it keeps the idle rate.
//
// Rate knobs carry the widened trichotomy (rateBudget): ∞ → 0 (uncapped), off
// (FPSOff) → the active cap in THIS render path (the "no idle render" behaviour
// lives in the skip/park path, not here), N → 1s/N.
func (a *App) FramePace(focused bool) time.Duration {
	full := paceBudget(a.d.Prefs.FPSCap()) // active cap: ∞/off → 0 (uncapped); N → 1s/N
	// nextAnimDue is the stage's earliest scheduled frame flip (ok=false on a
	// static stage / no room) — consulted by both focus branches.
	nextAnimDue := func() (time.Duration, bool) {
		if a.room == nil || a.d.Viewport == nil {
			return 0, false
		}
		return a.d.Viewport.NextAnimDue(a.renderScene())
	}
	if !focused {
		unf, unfOff := rateBudget(a.d.Prefs.UnfocusedFPS())
		if unf == 0 && !unfOff {
			return 0 // ∞ unfocused: never throttle
		}
		if unfOff {
			// off: an idle unfocused window SKIPS + parks, so a rendered frame
			// here is content (an audible ceremony / a visible stage anim) — pace
			// it at the cap and let talk/anim tighten below.
			unf = full
		}
		// A message playing while tabbed out is still AUDIBLE: hold the blip cadence.
		if a.roomBusy() {
			if tb := a.talkBudget(full); tb < unf {
				unf = tb
			}
		}
		// An unfocused window is still VISIBLE (second monitor): a live stage
		// animation keeps its OWN schedule — the flat trickle rate here was the
		// "idle animations go choppy the moment I click into another window" report.
		if due, ok := nextAnimDue(); ok && due < unf {
			unf = clampDur(due, full, unf)
		}
		return unf
	}
	if a.wantsFullRate() {
		return full // recent input / effects genuinely in flight
	}
	if a.drawnAnimChrome {
		// A self-driven UI animation (NoteAnimating census) re-arms every frame
		// it draws — some indefinitely (FX chat text on a settled message, an
		// animated theme page, the viewport's ambient-FX census). Smooth frames,
		// but never a ZERO budget: with the ∞ default cap, pace=hardCap=0 skips
		// every pacing sleep and vsync is the only brake — which main.go's own
		// GPU-burn note records as non-blocking on some windowed present paths.
		return backstopBudget(full)
	}
	idle, idleOff := rateBudget(a.d.Prefs.IdleFPS())
	idleUnlimited := !idleOff && idle == 0 // ∞ idle
	if a.roomBusy() {
		// The talk tier paces the ceremony; the earliest OTHER deadline may only
		// tighten it. Order matters: consulting the stage anim FIRST returned a
		// slow lipflap's flip (clamped to the idle rate) as the whole frame
		// budget, crawling the text and coalescing the blips.
		talk := a.talkBudget(full)
		switch {
		case idleOff || idleUnlimited:
			talk = full // off/∞ idle → ceremonies pace at the active cap
		case idle < talk:
			talk = idle // a high idle rate lifts the ceremony to match its smoothness
		}
		if due, ok := nextAnimDue(); ok && due < talk {
			talk = clampDur(due, full, talk) // a fast speaker anim gets its frames
		}
		return talk
	}
	if due, ok := nextAnimDue(); ok {
		if idleOff || idleUnlimited {
			return clampDur(due, full, due) // the content's own cadence (never above the cap)
		}
		return clampDur(due, full, idle) // content cadence, never slower than the idle rate
	}
	// Rendered but nothing is animating — see the "active cap" tier note above.
	// Backstopped: a frame NOTHING demanded (no input, no transition, no anim)
	// must never leave the pacer with a zero budget — under the ∞ default cap
	// the classic loop's lobby/caret renders became a zero-sleep spin here, and
	// ∞ idle ("as fast as the active cap", the Settings tooltip's own words)
	// resolves through the cap so a finite cap still rules.
	if a.d.Prefs.EventDrivenLoopOn() || idleOff || idleUnlimited {
		return backstopBudget(full)
	}
	return idle // classic idle nap (N → 1s/N)
}

// backstopBudget resolves a rendered-but-undemanded frame's budget: the given
// cap when finite, else a concrete ~60 fps floor (the Settings ∞-toggle's own
// FPSCapUnlimitedOff). Frames that reach it were NOT demanded by input or a
// running transition — wantsFullRate returns the true uncapped `full` before
// any caller — so flooring here costs active use nothing, while a perpetual
// census or an idle re-render can no longer disable pacing entirely (with
// pace=hardCap=0 the main loop sleeps zero and vsync is the only brake, which
// its own GPU-burn comment records as unreliable for windowed presents).
func backstopBudget(b time.Duration) time.Duration {
	if b > 0 {
		return b
	}
	return paceBudget(config.FPSCapUnlimitedOff)
}

// SkipFrame reports that the last drawn frame is still exactly right, so the
// main loop may skip render+present entirely this pass (GPU cost → zero) and
// just keep the session pumping via Background. Any SDL event, any ceremony,
// any scheduled animation, a live toast, a blinking caret, or a ticking timer
// forces a real frame. Conservative by construction: a wrongly SKIPPED signal
// heals on the next damage/deadline, never a hang.
//
// Under the EXPERIMENTAL event-driven loop the gate widens two ways: static
// menu screens skip too (not just the courtroom), and the caret/clock refusals
// become scheduled wakes (NextWakeDelay) — the loop parks on an OS event wait
// and renders exactly when the blink/tick is due instead of idle-rendering a
// static screen to animate a cursor. Damage that arrived since the last drawn
// frame (packets, texture uploads/evictions — RenderNeeded) still refuses. The
// classic loop instead re-renders at the idle rate (it doesn't track damage);
// idle=off makes both lean on caret/timer/input for a static courtroom.
func (a *App) SkipFrame(focused, sawEvent bool) bool {
	if sawEvent {
		return false // input this pass (incl. a focus-regain window event)
	}
	// Background cap OFF + window unfocused: render NOTHING while tabbed out. The
	// background cap is a hard ceiling exactly like the active cap, and its "off"
	// position means a 0-fps ceiling — so a stage animation must NOT keep driving
	// frames (the "bg=0 but still 60 fps in another window" report). Highest
	// priority after a live event: this overrides animation, damage, and the
	// full-rate input grace. Voice / mic capture is the sole exception — its audio
	// engine is frame-driven — and a focus-regain event (sawEvent above) resumes
	// normal rendering; the park keeps the session pumping meanwhile.
	if !focused && !a.voiceJoined && a.micTest == nil {
		if _, off := rateBudget(a.d.Prefs.UnfocusedFPS()); off {
			return true
		}
	}
	exp := a.d.Prefs != nil && a.d.Prefs.EventDrivenLoopOn()
	if exp {
		if !a.expScreenSkippable() {
			return false
		}
		// No heartbeat here: the event-driven loop parks on NextWakeDelay, whose
		// idle-rate tick IS the periodic idle render (idle=off → genuinely none).
	} else {
		if a.room == nil || a.sess == nil {
			return false // classic: only the courtroom skips (the lobby keeps its idle render)
		}
		// Classic idle heartbeat: the classic loop naps and re-polls rather than
		// waking on damage, so a static courtroom re-renders at the idle rate to
		// surface async changes it doesn't otherwise track. The idle rate is the
		// single cadence now (no separate fixed 500 ms); idle=off opts out and
		// leans on caret/timer/input, exactly like the event-driven loop's 0.
		if idleB, idleOff := rateBudget(a.d.Prefs.IdleFPS()); !idleOff && idleB > 0 {
			if time.Since(a.lastFrameDrawn) >= idleB {
				return false
			}
		}
	}
	if a.wantsFullRate() || a.roomBusy() || a.warnActive() {
		return false
	}
	// voicePump (mic capture, mixer, the Settings mic test) is driven from
	// Frame: while a voice session or mic test is live, frames must keep
	// coming at the idle rate or the audio engine starves. (Latent in the old
	// courtroom-only skip too — the usually-focused IC field masked it.)
	if a.voiceJoined || a.micTest != nil {
		return false
	}
	if a.drawnAnimChrome {
		// The last frame drew time-stepped art outside the viewport scheduler
		// (animated theme chrome, the testimony badge, a WT/CE splash, the
		// editor ghost, an ANIMATED sprite-preview box): freezing it between
		// heartbeats was a latent classic hole (usually masked by the focused
		// IC field). Both modes render on. The sprite preview reports through
		// this census from its DRAW site (drawSpritePreview) — deliberately NOT
		// via a bare previewBase check here: every preview close path is a
		// per-screen draw tail, so a preview orphaned by a screen switch (char
		// pick → PV, a pointer-fenced confirm click, a hotkey jump) had no
		// owner left to clear it, and the old state check held the event-driven
		// loop at the ACTIVE cap until restart (the stuck-at-active-cap
		// report). A static or undrawn box needs no frames: its pop-in rides
		// store-generation damage, zoom/drag ride input events.
		return false
	}
	if exp {
		if a.RenderNeeded() {
			return false // pending damage: packets landed / textures moved / a caret flip is showing
		}
	} else {
		if a.ctx != nil {
			// State-gated on the blink actually being STALE (the comparison
			// RenderNeeded uses), not on "a field is focused" — the IC input is
			// habitually focused in the courtroom, and the blanket refusal made
			// the classic loop render every pass forever (with the ∞ default
			// cap, a zero-sleep spin). The skip nap re-polls well inside a
			// blink period, so the caret still flips on time.
			if on, focused := a.ctx.CaretVisible(); focused && on != a.drawnCaretOn {
				return false // the focused caret's blink phase flipped — draw it
			}
		}
		if a.timerActive() || a.serverTimersLive() {
			return false // countdown readouts change every second
		}
	}
	if a.d.Viewport != nil && a.room != nil {
		if _, ok := a.d.Viewport.NextAnimDue(a.renderScene()); ok {
			return false
		}
	}
	_ = focused // both focused and unfocused static screens skip identically
	return true
}

// expScreenSkippable reports whether the CURRENT screen is safe for the
// experimental static skip. Court surfaces skip exactly like the classic
// path; the static menu screens join them; the lobby joins while no async
// sweep is repainting rows. Anything unlisted defaults to classic
// always-render.
//
// Settings skips too (the user's ask: "the one place where virtually nothing
// moves"). Its self-driven surfaces still force frames through SkipFrame's own
// later gates — the mic-test meter (micTest), a DRAWN animated sprite preview
// (the anim-chrome census via drawSpritePreview), input + the caret, and
// store-generation damage as art streams in. The lone
// passive live readout is the T2 cache-stat line; it now refreshes at the idle
// rate / on the next interaction rather than every frame (the perf HUD stays
// the always-full-rate live-metrics surface for anyone watching numbers move).
func (a *App) expScreenSkippable() bool {
	switch a.screen {
	case ScreenCourtroom, ScreenCharSelect:
		return a.sess != nil
	case ScreenLobby:
		return !a.lobbyFetching && !a.pinging
	case ScreenSettings, ScreenAbout, ScreenChangelog, ScreenServerHelp, ScreenHelp, ScreenLogs:
		return true
	default:
		return false
	}
}

// RenderNeeded reports redraw-worthy damage accumulated since the last drawn
// frame (experimental loop): a drained packet touched UI state, the texture
// store's generation moved (something streamed in / evicted), or the focused
// caret's blink state no longer matches what's on screen. Render thread only.
func (a *App) RenderNeeded() bool {
	if a.uiDirty {
		return true
	}
	if a.d.Store != nil && a.d.Store.Generation() != a.drawnGen {
		return true
	}
	if a.ctx != nil {
		if on, focused := a.ctx.CaretVisible(); focused && on != a.drawnCaretOn {
			return true
		}
	}
	return false
}

// RenderSuppressed reports that SkipFrame will keep refusing to render
// REGARDLESS of pending damage: the unfocused window with the background cap
// OFF (its 0-fps ceiling deliberately overrides damage; the state re-syncs on
// the focus-regain render). The main loop's "RenderNeeded → loop around and
// draw it" shortcut must park instead in that state — Background's own pumps
// set uiDirty on every packet and only a drawn frame clears it, so the
// shortcut otherwise spins with zero sleep until a real window event. Voice /
// mic capture never suppress, mirroring SkipFrame's own exception.
func (a *App) RenderSuppressed(focused bool) bool {
	if focused || a.voiceJoined || a.micTest != nil {
		return false
	}
	_, off := rateBudget(a.d.Prefs.UnfocusedFPS())
	return off
}

// NoteDeadline marks a scheduled wake that just fired (the main loop's event
// wait timed out at a deadline NextWakeDelay chose): the next pass renders one
// frame to show whatever was due — a caret flip, a clock second, a hover
// reveal, or the plain staleness heartbeat.
func (a *App) NoteDeadline() { a.uiDirty = true }

// NoteInteraction forces exactly one follow-up render after a real (non-motion)
// input event. A click or keypress almost always changes UI-visible state DURING
// its own frame's draw — a screen switch, a menu open, a toggle — and that change
// only appears on the NEXT frame. The main loop calls this after a rendered input
// frame so that frame is guaranteed regardless of the input-grace timing, instead
// of leaving the new state stranded until cursor motion or the idle tick reveals
// it (the "click Settings and the screen doesn't change until I wiggle the mouse"
// report). Idle-safe: no input, no follow-up.
func (a *App) NoteInteraction() { a.uiDirty = true }

const (
	// minWakeDelay floors the event wait so a just-passed deadline can never
	// busy-spin the loop.
	minWakeDelay = 5 * time.Millisecond
	// timerTickSlack lands a clock wake just PAST the second boundary, so the
	// frame it triggers draws the new second (not a re-render of the old one).
	timerTickSlack = 20 * time.Millisecond
	// assetDemandWakeInterval re-renders while a demand-streamed grid still shows
	// blank cells (drawnDemandPending), so demandAsset keeps issuing asks until
	// they fill. ~4 fps — brisk enough that a grid fills smoothly, cheap enough to
	// ignore, and it stops the instant no cell is blank. MUST stay strictly below
	// maxHousekeepingGap: considerRender only marks a render when the due time is
	// nearer than that Background-only floor, so an equal value would never draw.
	assetDemandWakeInterval = 250 * time.Millisecond
)

// NextWakeDelay is how long the experimental loop may park on its event wait:
// the time to the nearest scheduled deadline — caret flip, a RUNNING clock's
// next displayed second, a pending tooltip/preview dwell — capped by the
// staleness heartbeat and floored against busy-spin. Input and wake events
// interrupt the wait regardless; this only bounds how long "nothing happens"
// may last.
func (a *App) NextWakeDelay(focused bool) (wait time.Duration, render bool) {
	wait = maxHousekeepingGap // Background-only floor (no frame) — see the const
	// Background cap OFF + unfocused: schedule NO render wakes — the window is
	// asleep while tabbed out (SkipFrame parks it), so even the idle tick and the
	// caret/clock deadlines stay silent until focus returns. This is what makes a
	// 0-fps background cap actually reach 0 fps instead of the idle rate.
	if !focused {
		if _, off := rateBudget(a.d.Prefs.UnfocusedFPS()); off {
			return wait, false
		}
	}
	considerRender := func(due time.Duration) {
		if due < wait {
			wait = due
			render = true // the nearest wake is a real redraw deadline, not the floor
		}
	}
	// Idle redraw cadence: the missed-damage backstop and the "redraw at the idle
	// fps when nothing's happening" rate. off → no periodic idle render (0 GPU on
	// a static screen); ∞ → due immediately (render as fast as the loop allows).
	switch idleB, idleOff := rateBudget(a.d.Prefs.IdleFPS()); {
	case idleOff:
		// no periodic idle render — only the real deadlines below wake a frame
	case idleB == 0:
		considerRender(0) // ∞ idle
	default:
		considerRender(idleB - time.Since(a.lastFrameDrawn))
	}
	if a.ctx != nil {
		if due, ok := a.ctx.NextCaretFlip(); ok {
			considerRender(due)
		}
		if due, ok := a.ctx.NextHoverDue(); ok {
			considerRender(due)
		}
	}
	now := time.Now()
	// Server clocks (TI): wake right after each RUNNING timer's next whole second
	// so the mm:ss chip ticks on the second. (Paused clocks display a frozen
	// remainder — nothing scheduled.) These MUST mark render: with the old fixed
	// heartbeat gone, a clock tick is the only thing that redraws it, so at
	// idle=off it would otherwise freeze (the timer-freeze trap).
	if a.sess != nil {
		for i := range a.sess.Timers {
			t := &a.sess.Timers[i]
			if t.Visible && t.Running {
				if rem := t.Remaining(now); rem > 0 {
					considerRender(rem%time.Second + timerTickSlack)
				}
			}
		}
	}
	// The local alarm chip ticks the same way — and its due-fire (pollTimer,
	// Frame-driven) rides these wakes, so the alarm lands within a second
	// even while everything else is static.
	if a.timerRunning() {
		if rem := a.timerRemaining(); rem > 0 {
			considerRender(rem%time.Second + timerTickSlack)
		}
	}
	// Demand-pump keepalive: while a streaming grid still shows blank cells,
	// re-render at the demand cadence so demandAsset keeps issuing asks. A batch
	// that all 404s uploads nothing (no generation bump, no self-wake), so without
	// this the pump stalls at idle=off until unrelated damage or input arrives.
	if a.drawnDemandPending {
		considerRender(assetDemandWakeInterval)
	}
	if wait < minWakeDelay {
		wait = minWakeDelay
	}
	return wait, render
}

// wantsFullRate reports whether something on screen genuinely needs the full
// frame rate: recent input, stage effects in flight (shake/flash), an open
// replay/maker/export surface, a live voice call, floating reactions, the
// pinned second courtroom, or the perf HUD's scrolling graph. Message ceremony
// moved to FramePace's talk/content tiers; a crossfade counts only while one
// actually RUNS (Viewport.NextAnimDue); and the perpetual ambient motions —
// the rainbow/wobble/spin/breath washes, weather, transmitted sprite styles —
// count only while a stage that shows them actually DRAWS (the viewport
// census, Viewport.AmbientAnimating → NoteAnimating, paced by FramePace's
// anim-chrome tier). The old knob-not-state checks here held full rate forever
// on every screen — lobby and Settings included — and were the "redraws for no
// reason" playtest report, back for a second round as the idle-CPU-burn one.
func (a *App) wantsFullRate() bool {
	if time.Since(a.lastInputAt) < time.Duration(a.d.Prefs.InputGraceFrames())*inputGraceFrameDuration {
		return true // configurable post-input hold (default 1 frame); 0 would be the input's own frame only
	}
	if time.Since(a.lastMotionAt) < motionInputGrace {
		return true // a moving pointer renders live; the short grace ends with it
	}
	if a.room != nil {
		sc := &a.room.Scene
		if sc.ShakeLeft > 0 || sc.FlashLeft > 0 {
			return true // sub-second effect countdowns — self-terminating
		}
	}
	// voiceJoined, not the panel's open flag: a merely-open panel rides damage
	// and input like any other chrome; only a live call's audio engine needs
	// steady frames (SkipFrame's voice exception keeps them coming regardless —
	// this keeps their PACE at the full rate the engine was tuned for).
	if a.replaying || a.makerOpen || a.gifExporting || a.voiceJoined || a.perfHUD {
		return true
	}
	return len(a.reactionFloats) > 0 || a.splitActive()
}

// SetSpriteCapBase hands the App the display-derived decode-downscale base
// (cmd/asyncao computes it once at boot), so the Settings downscale sliders can
// re-derive the effective cap live via applySpriteCap.
func (a *App) SetSpriteCapBase(px int) { a.spriteCapBase = px }

// applySpriteCap pushes the effective decode-downscale cap (base × the two
// power-user knobs) to the decoder pool. Called from the Settings rows; new
// decodes pick it up (already-decoded textures keep their size until eviction).
func (a *App) applySpriteCap() {
	a.d.Manager.SetSpriteCap(config.EffectiveSpriteCap(a.spriteCapBase, a.d.Prefs.SpriteDownscaleOffOn(), a.d.Prefs.SpriteDownscalePct()))
}

// vpSpriteLoadMode maps the 3-way cold-load pref onto the renderer's 2-way switch:
// Wait is a MESSAGE-lifecycle gate (courtroom), so for the renderer it falls back to
// hold-previous — if a hold times out (404 / dead link) the stage keeps the previous
// sprite instead of flashing blank, composing the two mitigations.
func (a *App) vpSpriteLoadMode() int {
	mode := a.d.Prefs.SpriteLoadMode()
	if mode == config.SpriteLoadWait {
		return config.SpriteLoadHoldPrev
	}
	return mode
}

// applyTimingToRoom pushes the persisted crawl/stay knobs into the live
// courtroom (the crawl applies from the next message — Start precomputes
// per-rune delays).
func (a *App) applyTimingToRoom() {
	if a.room == nil {
		return
	}
	crawlMs, stayMs, _ := a.d.Prefs.Timing()
	a.room.Typewriter.Interval = time.Duration(crawlMs) * time.Millisecond
	a.room.TextStay = time.Duration(stayMs) * time.Millisecond
	a.room.Typewriter.BlipRate, a.room.Typewriter.BlipOnSpaces = a.d.Prefs.BlipTyping() // #7: blip cadence + skip-whitespace
	a.room.CatchUp, a.room.CatchUpThreshold = a.d.Prefs.CatchUp()
	a.room.SpriteWait = a.d.Prefs.SpriteLoadMode() == config.SpriteLoadWait               // cold-load mode 3: hold a message until its sprite decodes
	a.room.SpriteWaitTimeout = time.Duration(a.d.Prefs.SpriteWaitMs()) * time.Millisecond // its user-tunable hold cap
	a.room.SpriteWaitPair = a.d.Prefs.SpriteWaitPairOn()                                  // strictness: gate on the pair partner too
	a.room.SpriteWaitPreanim = a.d.Prefs.SpriteWaitPreanimOn()                            // strictness: gate on the preanim too
	a.room.ShoutDuration = courtroom.DefaultShoutDuration                                 // core-timing knobs: 0 = the canonical defaults
	if ms := a.d.Prefs.ShoutDurationMs(); ms != 0 {
		a.room.ShoutDuration = time.Duration(ms) * time.Millisecond
	}
	a.room.PreanimTimeout = courtroom.DefaultPreanimTimeout
	if ms := a.d.Prefs.PreanimTimeoutMs(); ms != 0 {
		a.room.PreanimTimeout = time.Duration(ms) * time.Millisecond
	}
	a.room.QueueCap = courtroom.DefaultQueueCap // 0-pref = the canonical depth; always assigned so a nuke-reset restores it
	if n := a.d.Prefs.ICQueueCap(); n != 0 {
		a.room.QueueCap = n
	}
	a.room.CatchUpLinger = time.Duration(a.d.Prefs.CatchUpLingerMs()) * time.Millisecond
	a.room.ReduceMotion = a.d.Prefs.ReduceMotion()
	a.room.ScreenEffects = a.d.Prefs.ScreenEffectsOn() // AO2 \s/\f + field shake/flash (default ON)
	a.room.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
	a.room.HideSpriteStyles = a.d.Prefs.HideSpriteStylesOn() // #103: viewer opt-out of others' styles
}

// activeCharName is the character folder OUTGOING messages use: the
// iniswap override when set (AO iniswapping — the server slot keeps the
// list character, the wire carries the custom folder; AO2-Client
// set_iniswap semantics), else our picked character.
func (a *App) activeCharName() string {
	if a.iniChar != "" {
		return a.iniChar
	}
	return a.myCharName()
}

// setIniswap applies a custom character override ("" reverts to the picked
// character) and reloads the emote list for the new active folder.
func (a *App) setIniswap(name string) {
	a.iniChar = name
	a.emoteAsk = nil
	a.emoteIdx, a.emotePage = 0, 0 // new active folder = new emote list: start at the first
	a.loadCharINI()
}

// wardrobeAct is the resolved action for clicking a Characters-tab favourite.
// Pure + table-tested (TestWardrobeAction) because this is the bit users are
// touchy about — a favourite must never silently iniswap when it could switch.
type wardrobeAct int

const (
	actSwitch  wardrobeAct = iota // take the character's slot (a real char change)
	actIniswap                    // wear its look without taking a slot
)

// wardrobeAction decides what a Characters-tab click does. Switching to the
// character is the default; we fall back to an iniswap only when the user ticked
// iniswap mode, when the name isn't one of this server's characters (slot < 0),
// or when that character's slot is already taken (can't claim an occupied one).
func wardrobeAction(iniswapMode bool, slot int, taken bool) wardrobeAct {
	if iniswapMode || slot < 0 || taken {
		return actIniswap
	}
	return actSwitch
}

// charSlotByName finds the server character slot whose folder name matches
// (case-insensitive), or -1. Linear, but only runs on an actual wardrobe click.
func (a *App) charSlotByName(name string) int {
	want := strings.ToLower(strings.TrimSpace(name))
	for i := range a.sess.Chars {
		if strings.ToLower(a.sess.Chars[i].Name) == want {
			return i
		}
	}
	return -1
}

// wardrobeClick handles a click on a Characters-tab favourite: switch to that
// character (claim its slot — the same CC → PV → enterCourtroom flow as the
// "Character" button) by default, or iniswap it when iniswap mode is ticked. A
// switch we can't perform (taken slot, or a name that isn't a server character)
// falls back to an iniswap — with a toast, so a favourite never silently swaps.
func (a *App) wardrobeClick(name string) {
	a.showIni = false
	if a.room == nil {
		// Char-select Wardrobe tab: pick the REAL free character (the switch a
		// favourite is expected to do) when it's a claimable server slot; only
		// fall back to an iniswap (free slot + its look) when it's taken or not a
		// server character. Same decision as the courtroom path below, via
		// wardrobeAction — this is the half the original fix missed, so picking a
		// favourite on reconnect iniswapped instead of switching.
		slot := -1
		taken := false
		if a.sess != nil {
			slot = a.charSlotByName(name)
			taken = slot >= 0 && a.sess.Chars[slot].Taken
		}
		if wardrobeAction(a.iniSwapMode, slot, taken) == actSwitch {
			a.pickCharacter(slot)
			return
		}
		a.wearFromMenu(name)
		return
	}
	slot := a.charSlotByName(name)
	taken := slot >= 0 && a.sess.Chars[slot].Taken
	if wardrobeAction(a.iniSwapMode, slot, taken) == actSwitch {
		a.pickCharacter(slot) // CC → PV → EventCharPicked → enterCourtroom (iniswap clears)
		return
	}
	if !a.iniSwapMode { // a switch was wanted but impossible — say why, don't swap silently
		switch {
		case slot < 0:
			a.warnLine = name + " isn't a character on this server — worn as an iniswap"
		default:
			a.warnLine = name + " is taken — worn as an iniswap"
		}
		a.warnAt = time.Now()
	}
	a.wearFromMenu(name)
}

// wearFromMenu handles a wardrobe pick from either screen. In the
// courtroom it's an instant swap; on char select (fresh join) an iniswap
// still needs a server SLOT, so claim the first free one and wear the
// custom when PV confirms (enterCourtroom applies pendingIni).
func (a *App) wearFromMenu(name string) {
	a.showIni = false
	if a.room != nil {
		a.setIniswap(name)
		a.screen = ScreenCourtroom
		return
	}
	free := -1
	for i := range a.sess.Chars {
		if !a.sess.Chars[i].Taken {
			free = i
			break
		}
	}
	if free < 0 {
		a.warnLine = "No free character slots to host an iniswap — every slot is taken"
		a.warnAt = time.Now()
		return
	}
	a.pendingIni = name
	a.pickCharacter(free) // rehearsal resolves locally
}

// openIniswap shows the wardrobe menu (courtroom modal).
func (a *App) openIniswap() {
	a.showIni = true
	a.ensureIniList()
	if a.wardSection == wardSectionBackgrounds {
		a.rebuildBgFav() // favourites may have changed since this section last drew
	}
}

// ensureIniList makes the merged wardrobe menu current: the wardrobe half
// renders instantly from prefs; the server's iniswap.txt merges in when
// its fetch lands (FetchRaw: T2 + disk cached, singleflight — a reopen
// is a memory hit).
func (a *App) ensureIniList() {
	a.rebuildIniMenu() // wardrobe is local: usable before (or without) the txt
	if a.iniServer != nil || a.iniBusy || a.urls.Origin() == "" {
		return
	}
	a.iniBusy = true
	a.iniListErr = ""
	url := a.urls.Origin() + iniswapFileName
	bgURL := a.urls.BackgroundsRoot()
	key := a.serverKey
	go func() {
		// Same guard as the background-list goroutine: a malformed autoindex
		// tripping parseAutoindexDirs must degrade, never hard-crash off-thread.
		defer func() {
			if r := recover(); r != nil {
				writeCrashLog("iniswap list goroutine panic: ", r)
				a.iniRes <- iniswapFetch{key: key, err: fmt.Errorf("iniswap list failed: %v", r)}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		data, err := a.d.Manager.FetchRaw(ctx, url)
		if err != nil {
			a.iniRes <- iniswapFetch{key: key, err: err}
			return
		}
		names := parseIniswapList(data)
		// The backgrounds root rides the same cached fetch path (the bg picker
		// shares the URL, so at most one network hit between them) purely to
		// FILTER the list; a host with no listing is not an error (nil = the
		// remembered names alone do the filtering).
		var bgs []string
		if bgData, bgErr := a.d.Manager.FetchRaw(ctx, bgURL); bgErr == nil {
			bgs = parseAutoindexDirs(bgData)
		}
		a.iniRes <- iniswapFetch{key: key, names: names, bgs: bgs}
	}()
}

func (a *App) pollIniswap() {
	select {
	case res := <-a.iniRes:
		if res.key != a.serverKey {
			return // landed after a tab switch: not this server's list
		}
		a.iniBusy = false
		if res.err != nil {
			a.iniListErr = "no server list (" + res.err.Error() + ") — your wardrobe still works"
			a.iniServer = nil
		} else {
			a.iniServer = filterServerIniswaps(res.names, res.bgs,
				a.d.Prefs.ServerWarmInfoFor(a.serverKey).Backgrounds)
		}
		a.rebuildIniMenu()
	default:
	}
}

// rebuildIniMenu merges the menu: wardrobe first (the user's saved
// favourites — instant swaps, persisted across sessions and servers),
// then server-list entries not already in the wardrobe.
func (a *App) rebuildIniMenu() {
	names, fromWardrobe, inServer := mergeWardrobe(a.d.Prefs.WardrobeList(a.serverKey), a.iniServer)
	a.iniList = names
	a.iniWardrobe = fromWardrobe
	a.iniServerMem = inServer
	a.iniLower = make([]string, len(names))
	folders := a.d.Prefs.WardrobeFolderMap(a.serverKey)
	a.iniFolders = make([]string, len(names))
	for i, n := range names {
		lower := strings.ToLower(n)
		a.iniLower[i] = lower
		a.iniFolders[i] = folders[lower] // "" when unsorted / not filed
	}
	a.iniAsk = nil
	// Toggling a star reorders the list at the SAME length (wardrobe entries
	// float up), and cachedPage keys its icon slice by index without re-checking
	// the URL — so drop the idx→page cache here or a reorder would paint the
	// previous name's icon. Icons re-resolve from T1 next frame (a map hit).
	a.iniPages = nil
}

// applyPendingFav applies a wardrobe ★ toggle that a cell DEFERRED this frame. It
// rebuilds (and shrinks/grows) the iniList/iniWardrobe/iniFolders slices, so it MUST
// run after the grid loop that ranges them — toggling inside the cell shrank those
// slices mid-iteration and panicked on a remove (the reported crash). A no-op when
// nothing was deferred.
func (a *App) applyPendingFav() {
	if a.iniFavPending == "" {
		return
	}
	name := a.iniFavPending
	a.iniFavPending = ""
	if a.iniFavPendingAdd {
		a.d.Prefs.AddWardrobe(a.serverKey, name)
	} else {
		a.d.Prefs.RemoveWardrobe(a.serverKey, name)
	}
	a.rebuildIniMenu()
}

// ensureWardrobeMembers rebuilds the current server's lowercased wardrobe set
// when the server changed or the wardrobe was edited (generation bump). It
// reuses the map (clear-in-place) so the steady state allocates nothing; the
// per-cell star lookup in drawCharCell then reads it lock-free.
func (a *App) ensureWardrobeMembers() {
	gen := a.d.Prefs.WardrobeGeneration()
	if a.wardrobeMembers != nil && a.wardrobeMembersFor == a.serverKey && a.wardrobeMembersGen == gen {
		return
	}
	if a.wardrobeMembers == nil {
		a.wardrobeMembers = make(map[string]bool)
	} else {
		clear(a.wardrobeMembers)
	}
	for _, name := range a.d.Prefs.WardrobeList(a.serverKey) {
		a.wardrobeMembers[strings.ToLower(name)] = true
	}
	a.wardrobeMembersFor = a.serverKey
	a.wardrobeMembersGen = gen
}

// mergeWardrobe combines your wardrobe favourites with the server's iniswap
// list for the wardrobe grid: favourites come first (starred), then the server
// entries the favourites didn't already cover. The Iniswaps tab shows only the
// server list — a favourite that ISN'T a server iniswap stays out of it, and a
// server with no list shows nothing. Server duplicates collapse into their
// wardrobe entry, case-insensitively.
func mergeWardrobe(wardrobe, server []string) (names []string, stars, inServer []bool) {
	names = make([]string, 0, len(wardrobe)+len(server))
	stars = make([]bool, 0, len(wardrobe)+len(server))
	inServer = make([]bool, 0, len(wardrobe)+len(server))
	serverSet := make(map[string]struct{}, len(server))
	for _, n := range server {
		serverSet[strings.ToLower(n)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(wardrobe))
	sort.SliceStable(wardrobe, func(i, j int) bool {
		return strings.ToLower(wardrobe[i]) < strings.ToLower(wardrobe[j])
	})
	for _, n := range wardrobe {
		key := strings.ToLower(n)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, n)
		stars = append(stars, true)
		_, srv := serverSet[key]
		inServer = append(inServer, srv) // a favourite that's also a server iniswap
	}
	for _, n := range server {
		if _, dup := seen[strings.ToLower(n)]; dup {
			continue
		}
		names = append(names, n)
		stars = append(stars, false)
		inServer = append(inServer, true)
	}
	return names, stars, inServer
}

// filterServerIniswaps drops background folder names from the server's parsed
// iniswap list. Some servers publish one combined "everything streamable" txt
// (playtest: 187 of one server's 376 iniswap entries were its background/ dir),
// and a background wears like a broken character. The exclusion set is the live
// background/ autoindex (fetched alongside the txt) plus last session's
// remembered names — either half may be missing (no listing / first visit), so
// they union. Tradeoff, deliberately accepted: a real character sharing a
// background's exact folder name is hidden from the grids too, but it can
// still be worn by name (the Iniswaps search box or the wardrobe Add field).
func filterServerIniswaps(names, liveBgs, warmBgs []string) []string {
	if len(liveBgs) == 0 && len(warmBgs) == 0 {
		return names
	}
	bgSet := make(map[string]struct{}, len(liveBgs)+len(warmBgs))
	for _, b := range liveBgs {
		bgSet[strings.ToLower(b)] = struct{}{}
	}
	for _, b := range warmBgs {
		bgSet[strings.ToLower(b)] = struct{}{}
	}
	kept := names[:0] // in place: names is pollIniswap's own parse result
	for _, n := range names {
		if _, isBg := bgSet[strings.ToLower(n)]; !isBg {
			kept = append(kept, n)
		}
	}
	return kept
}

// parseIniswapList parses iniswap.txt: one character folder name per line,
// blanks skipped, case-insensitive dedupe, capped, sorted for the menu.
func parseIniswapList(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	names := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
		if len(names) >= iniswapListCap {
			break
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}

// cachedPage resolves a grid cell's texture page through a
// generation-keyed cache: pointers stay valid exactly while the store
// generation holds (evictions bump it, and evicted textures only destroy
// via the queue after the bump — same safety argument as the viewport's
// animState). Slices clear in place on a generation move (no per-move
// allocation; reallocate only when the list length changes).
func (a *App) cachedPage(pages *[]*render.TexturePage, gen *uint64, n, idx int, base string) (*render.TexturePage, bool) {
	g := a.d.Store.Generation()
	if len(*pages) != n {
		*pages = make([]*render.TexturePage, n)
		*gen = g
	} else if *gen != g {
		for i := range *pages {
			(*pages)[i] = nil
		}
		*gen = g
	}
	if idx < 0 || idx >= n {
		return nil, false
	}
	if p := (*pages)[idx]; p != nil {
		return p, true
	}
	page, ok := a.d.Store.Get(base)
	if !ok {
		return nil, false
	}
	(*pages)[idx] = page
	return page, true
}

// demandAsset paces one visible cell's asset demand: shared per-frame
// budget, one ask per slot per charIconRetryInterval (self-heals shed
// low-lane jobs). stamps resizes lazily to n; callers tag the asset type.
func (a *App) demandAsset(stamps *[]time.Time, n, idx int, base string, t assets.AssetType) {
	if a.iconAskBudget <= 0 || idx < 0 || idx >= n {
		return
	}
	if len(*stamps) != n {
		*stamps = make([]time.Time, n)
	}
	now := time.Now()
	if now.Sub((*stamps)[idx]) < charIconRetryInterval {
		return
	}
	(*stamps)[idx] = now
	a.iconAskBudget--
	a.d.Manager.Prefetch(base, t, network.PriorityLow)
}

// charINIURL builds a character's char.ini location.
func (a *App) charINIURL(name string) string {
	return a.urls.Origin() + "characters/" + strings.ToLower(name) + "/char.ini"
}

// warmCharINI speculatively fetches a hovered character's char.ini so the
// eventual pick costs a memory hit instead of an RTT. One submit per
// hovered name; the manager dedupes and the 404 cache absorbs misses.
func (a *App) warmCharINI(name string) {
	if name == "" || name == a.iniWarmed || a.urls.Origin() == "" {
		return
	}
	a.iniWarmed = name
	a.d.Manager.PrefetchRaw(a.charINIURL(name), network.PriorityLow) // raw text: char.ini
}

// previewEmoteCap bounds one previewed character's try-before-wear emote cycle
// (rule §17.4) — a pathological char.ini can't grow the slice unbounded.
const previewEmoteCap = 256

// ensurePreviewEmotes loads a previewed (non-worn) wardrobe character's emote
// list ONCE so the preview box can cycle it (try-before-wear). The char.ini was
// just warmed on hover (warmCharINI → PrefetchRaw), so the fetch rides the
// cache; the parse runs off-thread and lands in previewEmoteRes. previewChar
// guards it to one parse per character.
func (a *App) ensurePreviewEmotes(name string) {
	if name == "" || name == a.previewChar || a.urls.Origin() == "" {
		return
	}
	a.previewChar = name
	a.previewAnims = nil
	a.previewLabels = nil
	a.previewEmoteIdx = 0
	url := a.charINIURL(name)
	key := a.serverKey
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		data, err := a.d.Manager.FetchRaw(ctx, url)
		if err != nil {
			return
		}
		ini, err := courtroom.ParseCharINI(data)
		if err != nil || ini == nil {
			return
		}
		anims := make([]string, 0, len(ini.Emotes))
		labels := make([]string, 0, len(ini.Emotes))
		for i, e := range ini.Emotes {
			if i >= previewEmoteCap {
				break
			}
			anims = append(anims, e.Anim)
			labels = append(labels, e.Comment)
		}
		a.previewEmoteRes <- previewEmoteFetch{key: key, char: name, anims: anims, labels: labels}
	}()
}

// pollPreviewEmotes drains a finished preview-emote parse (dropped on a tab
// switch or a newer hover). If the idle is still on screen for this character,
// it advances to emote 0 so the ‹ › index always matches what's displayed.
func (a *App) pollPreviewEmotes() {
	select {
	case res := <-a.previewEmoteRes:
		if res.key != a.serverKey || res.char != a.previewChar {
			return
		}
		a.previewAnims = res.anims
		a.previewLabels = res.labels
		a.previewEmoteIdx = 0
		if len(res.anims) > 0 && a.previewBase == a.urls.Emote(res.char, "normal", courtroom.EmoteIdle) {
			a.setPreviewEmote(0)
		}
	default:
	}
}

// setPreviewEmote points the preview box at emote i (wrapping) of the previewed
// character — the talk (b) pose, like the courtroom emote strip's hover.
func (a *App) setPreviewEmote(i int) {
	n := len(a.previewAnims)
	if n == 0 || a.previewChar == "" {
		return
	}
	a.previewEmoteIdx = ((i % n) + n) % n
	anim := a.previewAnims[a.previewEmoteIdx]
	a.previewBase = a.urls.Emote(a.previewChar, anim, courtroom.EmoteTalk)
	a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(a.previewChar, anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview cycle)
}

// cyclePreviewEmote steps the try-before-wear preview by delta (wrapping).
func (a *App) cyclePreviewEmote(delta int) { a.setPreviewEmote(a.previewEmoteIdx + delta) }

// clampEmoteSel returns the emote index/page to use after an emote list of
// length n is (re)loaded. A still-valid index is PRESERVED — so a tab
// reactivation (activateTab → buildRoom → loadCharINI lands for the SAME
// character) keeps the user's selection instead of snapping back to the first
// emote. An out-of-range index (a shorter list, e.g. an iniswap) resets to the
// first emote on page 0. Index and page move together so a kept index can't
// desync from a stale page. Pure so the session-isolation behaviour is pinned
// by a unit test, not a manual rebuild.
func clampEmoteSel(idx, page, n int) (int, int) {
	if idx < 0 || idx >= n {
		return 0, 0
	}
	return idx, page
}

// loadCharINI fetches the ACTIVE character's char.ini for the emote list
// (the iniswap override when set).
func (a *App) loadCharINI() {
	name := a.activeCharName()
	if a.sess == nil || name == "" {
		return
	}
	url := a.charINIURL(name)
	a.charINIBusy = true
	key := a.serverKey
	go func() {
		data, err := a.d.Manager.FetchRaw(context.Background(), url)
		if err != nil {
			a.charINIres <- charINIFetch{key: key, err: err}
			PushWake() // wake the event-driven loop so Background drains this at idle=0
			return
		}
		ini, err := courtroom.ParseCharINI(data)
		a.charINIres <- charINIFetch{key: key, ini: ini, err: err}
		PushWake() // wake the event-driven loop so Background drains this at idle=0
	}()
}

func (a *App) pollCharINI() {
	if a.pinnedPass {
		return // the full-theme pinned render must not drain the primary's char.ini result
	}
	select {
	case res := <-a.charINIres:
		if res.key != a.serverKey {
			return // landed after a tab switch: another server's char.ini
		}
		a.charINIBusy = false
		a.uiDirty = true // the emote list just changed (and "Loading emotes…" must clear): force one redraw, like the character list does on its packet
		// New emote list = new button art; the gen-keyed page caches key
		// by INDEX, so a same-length iniswap would show the old char's
		// buttons without this.
		a.emoteBtnOff, a.emoteBtnOn, a.emoteIconPages = nil, nil, nil
		if res.err != nil || res.ini == nil {
			// Surface WHY the emote list is a bare default (better than a
			// silent single "normal" chip).
			reason := "empty char.ini"
			if res.err != nil {
				reason = res.err.Error()
			}
			a.warnLine = clampLine("char.ini for " + a.activeCharName() + ": " + reason + " — using a default emote")
			a.warnAt = time.Now()
			a.emotes = []courtroom.Emote{{Comment: "normal", Anim: "normal", Preanim: "-"}}
			a.emoteIdx, a.emotePage = clampEmoteSel(a.emoteIdx, a.emotePage, len(a.emotes))
			return
		}
		a.emotes = res.ini.Emotes
		if len(a.emotes) == 0 {
			a.emotes = []courtroom.Emote{{Comment: "normal", Anim: "normal", Preanim: "-"}}
		}
		// OUR side comes from our char.ini (AO2-Client semantics), never
		// from whoever spoke last; /pos overrides it.
		if side := strings.ToLower(strings.TrimSpace(res.ini.Side)); side != "" {
			a.sidePref = side
		}
		a.charBlips = res.ini.Blips
		// Custom shout menu: the base "custom" entry (renamed by
		// custom_name) plus the named 2.10 interjections.
		a.customShouts = res.ini.CustomShouts
		a.customIdx = -1 // base custom
		a.customName = res.ini.CustomName
		// Preserve the per-tab emote selection across a REACTIVATION reload: snap
		// to the first emote only when the restored index no longer fits the
		// freshly loaded list. A real character change (enterCourtroom / setIniswap)
		// zeroes these synchronously before the fetch, so a fresh pick still starts
		// at emote 0 — see clampEmoteSel.
		a.emoteIdx, a.emotePage = clampEmoteSel(a.emoteIdx, a.emotePage, len(a.emotes))
		a.emoteAsk = nil // fresh char: re-demand its button art from scratch
		// Speculatively prefetch emote sprites at LOW priority (sheds under load, never blocks
		// live HIGH fetches). #127: with the bundle toggle on, grab this character's FULL set —
		// every emote's idle AND talk (with the bare-spelling fallback) — so switching emotes is
		// instant; off keeps the lightweight first-few-idles default. Fired once per char load
		// (this branch runs on the char.ini result), and singleflight collapses any that are
		// already cached/in-flight.
		me := a.myCharName()
		if a.d.Prefs.CharBundlePrefetchOn() {
			for _, e := range a.emotes {
				a.d.Manager.PrefetchChain(a.urls.Emote(me, e.Anim, courtroom.EmoteIdle), a.urls.EmoteAlts(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
				a.d.Manager.PrefetchChain(a.urls.Emote(me, e.Anim, courtroom.EmoteTalk), a.urls.EmoteAlts(me, e.Anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
			}
		} else {
			for i, e := range a.emotes {
				if i >= 8 {
					break
				}
				a.d.Manager.Prefetch(a.urls.Emote(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite
			}
		}
	default:
	}
}

func (a *App) myCharName() string {
	if a.sess == nil || a.sess.MyCharID < 0 || a.sess.MyCharID >= len(a.sess.Chars) {
		return ""
	}
	return a.sess.Chars[a.sess.MyCharID].Name
}

// --- lobby ----------------------------------------------------------------------

// RefreshServers fetches the master list asynchronously.
func (a *App) RefreshServers() {
	if a.lobbyFetching {
		return
	}
	a.lobbyFetching = true
	a.lobbyStatus = "Fetching server list..."
	// Fresh list = stale connect-times: back to player sort, drop the cache
	// (keeps a.pings bounded, rule §17.4), and abandon any in-flight sweep.
	a.pings, a.pingMode, a.pinging = nil, false, false
	a.pingGen++
	// The Settings override wins over the built-in default, but an
	// explicit --master flag (anything non-default in Deps) wins over both.
	url := a.d.MasterURL
	if alt := a.d.Prefs.MasterList(); alt != "" && url == network.DefaultMasterServerURL {
		url = alt
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), lobbyRefreshTimeout)
		defer cancel()
		entries, err := network.FetchServerList(ctx, url)
		a.lobbyResult <- lobbyFetch{entries: entries, err: err}
	}()
}

func (a *App) pollLobbyFetch() {
	select {
	case res := <-a.lobbyResult:
		a.lobbyFetching = false
		a.selServer, a.descLines = -1, nil // indices change with the list
		if res.err != nil {
			a.lobbyStatus = "Master list error: " + res.err.Error()
			a.masterEntries = nil
			a.servers = a.mergedFavorites()
			return
		}
		a.lobbyStatus = fmt.Sprintf("%d servers", len(res.entries))
		a.masterEntries = res.entries
		a.servers = a.mergedFavorites()
	default:
	}
}

// mergedFavorites merges the phone book into a fresh copy of the master
// list and sorts for display (favorites pinned top, legacy pinned bottom).
func (a *App) mergedFavorites() []network.ServerEntry {
	entries := make([]network.ServerEntry, len(a.masterEntries))
	copy(entries, a.masterEntries)
	entries = network.MergeFavorites(entries, a.d.Prefs.FavoriteServers())
	if a.pingMode {
		sortByPing(entries, a.pings) // lobby "Ping" sort: connect-time ascending
	} else {
		network.SortServers(entries)
	}
	return entries
}

// --- shared frame ------------------------------------------------------------------

// Frame runs one UI frame: connection pump, screen logic, drawing.
func (a *App) Frame(dt time.Duration, winW, winH int32) {
	defer a.frameCrashLog() // last-resort: capture the stack of any unrecovered panic before it kills the app
	a.frameNow = time.Now()
	a.lastFrameDrawn = a.frameNow // SkipFrame's heartbeat: a real frame was drawn
	// Damage bookkeeping (experimental loop): this frame absorbs everything
	// pending. Snapshot the store generation at frame START — uploads during
	// THIS frame (the pump below) bump it and earn at most one follow-up
	// frame, whose own snapshot then matches (self-limiting, never misses).
	a.uiDirty = false
	if a.d.Store != nil {
		a.drawnGen = a.d.Store.Generation()
	}
	a.drawnCaretOn = a.ctx.caretOn
	// Animated-chrome census: the draw sites below mark the flag; promote it
	// at frame end so SkipFrame reads what THIS frame actually put on screen.
	a.frameAnimChrome = false
	// Demand-pump census (experimental loop): a draw that leaves a streaming grid
	// cell blank marks frameDemandPending so NextWakeDelay keeps re-rendering at
	// the demand cadence until the cell fills. Promoted at frame end beside the
	// anim-chrome flag.
	a.frameDemandPending = false
	defer func() {
		a.drawnAnimChrome = a.frameAnimChrome
		a.drawnDemandPending = a.frameDemandPending
	}()
	a.ctx.BeginDraw()           // the visible-field order rebuilds as this frame's TextFields register
	a.winW, a.winH = winW, winH // cached for deep draw helpers (preview clamp)
	a.frameDtMs = float32(dt.Seconds() * 1000)
	a.recordFrameDt(a.frameDtMs)
	// One-time: push the initial Discord presence once the app is up, so "Playing
	// AsyncAO — In the lobby" appears immediately on launch (not only after you join
	// a room). updatePresence no-ops when presence is unset or the toggle is off.
	if !a.presenceInit {
		a.presenceInit = true
		a.updatePresence()
	}
	// Stamp the sprite-preview config onto the context once per frame so every
	// HoverPreview call this frame reads a consistent, lock-free value.
	a.ctx.SetHoverPreview(a.d.Prefs.SpritePreviewsOn(), a.d.Prefs.PreviewHoverDelay())
	// Hold-to-clear: stamp the bound key + threshold so the kit's text fields can
	// wipe on a long hold (the timer runs in BeginFrame).
	if on, keyName, ms := a.d.Prefs.HoldClear(); on {
		a.ctx.SetHoldClear(true, sdl.GetKeyFromName(keyName), time.Duration(ms)*time.Millisecond)
	} else {
		a.ctx.SetHoldClear(false, sdl.K_UNKNOWN, 0)
	}
	// F3 toggles the perf HUD on any screen; consumed so plain-key
	// macro/character binds named "f3" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F3 {
		a.perfHUD = !a.perfHUD
		a.ctx.keyPressed = 0
	}
	// F1 toggles the hotkey cheat-sheet on any screen (consumed so a plain-key
	// macro/char bind named "f1" can't double-fire).
	if a.ctx.keyPressed == sdl.K_F1 {
		if a.showHotkeys {
			a.showHotkeys = false
			a.hkCache = nil
		} else {
			a.openHotkeyCheatSheet() // (re)builds the rows so it reflects current bindings
		}
		a.ctx.keyPressed = 0
	}
	// F8 toggles the interactive Debug panel on any screen — the superset of the F3
	// perf HUD, also reachable from Extras → Debug and Settings → Power user.
	// Consumed so a plain-key macro/char bind named "f8" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F8 {
		a.toggleDebugPanel()
		a.ctx.keyPressed = 0
	}
	// ESC leaves a full-screen menu back to where it opened — the instinctive
	// keystroke (the Back button still works). Two-step so it ALWAYS makes
	// progress out: a focused text field (a search / callword box, the common
	// case) releases on the first ESC, then the second ESC leaves — instead of
	// the field silently swallowing the key ("ESC did nothing in Settings").
	// Scoped to the menu screens so the courtroom's own ESC behaviour is
	// untouched; skipped while a key-bind capture or an ESC-owning overlay
	// (hotkey sheet / update modal) is up so it never steals their key.
	// Esc backs out of whatever's open — one layer per press. On a full-screen MENU
	// it closes a dropdown / field then leaves; on the courtroom or lobby it closes
	// the topmost popup or floating panel (Group Chat, Voice, Evidence, Pair, Mod,
	// CM, Call Mod, Timer, …) via closeTopOverlay. Skipped while a key-bind capture
	// or the layout editor owns Esc.
	// NOTE: Esc arrives as ctx.escPressed (HandleEvent maps K_ESCAPE there, NOT to
	// keyPressed) — the old keyPressed==K_ESCAPE test never fired, which is why "Esc
	// did nothing". Gate on escPressed.
	if a.ctx.escPressed && !a.capturingKey() && !a.classicEdit {
		// Close the topmost popup / panel first (works on any screen — e.g. the
		// reset-confirm or a dropdown over Settings); only if nothing was open do we
		// fall back to leaving a full-screen menu (drop a focused field, then exit).
		handled := a.closeTopOverlay()
		if !handled {
			switch a.screen {
			case ScreenSettings, ScreenAbout, ScreenChangelog, ScreenServerHelp, ScreenLogs, ScreenHelp:
				if a.ctx.focusID != "" {
					a.ctx.focusID = "" // first ESC: drop a focused field
				} else {
					a.screen = a.prevScreen // then leave the menu
				}
				handled = true
			case ScreenLobby:
				// Lobby with nothing open: Esc offers to quit — the escape hatch when
				// you're fullscreen and can't reach the window's close button.
				a.requestQuit()
				handled = true
			case ScreenCourtroom:
				// AsyncAO is built for fullscreen, so Esc is the keyboard exit (playtest):
				// the bare courtroom — nothing else open — leaves the server THROUGH the
				// confirm (requestDisconnect), so an accidental tap can't instantly drop you
				// ("you press it by accident and you're dead"). A focused field (the IC input)
				// eats the first Esc, exactly like every menu screen.
				if a.ctx.focusID != "" {
					a.ctx.focusID = ""
				} else {
					a.requestDisconnect()
				}
				handled = true
			case ScreenCharSelect:
				// Same fullscreen keyboard exit on char-select. Re-picking from the courtroom
				// backs out to it (matches the top-right Back, non-destructive); a fresh
				// char-select's only exit is leaving the server, through the confirm.
				if a.ctx.focusID != "" {
					a.ctx.focusID = ""
				} else if a.room != nil {
					a.screen = ScreenCourtroom
				} else {
					a.requestDisconnect()
				}
				handled = true
			}
		}
		if handled {
			a.ctx.escPressed = false // consume it so a draw-time Esc handler can't double-fire
		}
	}
	// F11 toggles fullscreen on any screen — the keyboard escape when a too-big
	// window has dragged the Settings controls off the edge of the monitor.
	if a.ctx.fullscreenReq {
		a.toggleFullscreen()
	}
	// Log-selection press edge, computed once so both logs (which may both be
	// on screen) read the same value — whichever draws first can't steal it.
	a.logSelPressed = a.ctx.mouseDown && !a.logSelPrevDown
	a.logSelPrevDown = a.ctx.mouseDown
	// Ctrl+Z / Ctrl+Y with a text field focused become the FIELD's undo/redo
	// (fieldhistory.go): consumed here, pre-screen, so no later chord consumer
	// — the courtroom dispatcher, quick-connect, a user hotkey bound to z/y —
	// can double-fire while typing. Ctrl+Shift+Z is redo too. The armed layout
	// editors keep their own Ctrl+Z (editorUndoChord); they never run with a
	// focused field, but the gate makes that explicit.
	if a.ctx.focusID != "" && !a.classicEdit && !a.layoutEdit {
		switch a.ctx.hotkey {
		case sdl.K_z:
			a.ctx.undoReq = !a.ctx.shiftHeld
			a.ctx.redoReq = a.ctx.shiftHeld
			a.ctx.hotkey = 0
		case sdl.K_y:
			a.ctx.redoReq = true
			a.ctx.hotkey = 0
		}
	}
	a.pumpTabRestore()  // restore-on-launch: one reconnect/frame, then idle
	a.fireAutoConnect() // one-shot: auto-connect to the last server on launch (opt-in)
	// Quick-connect key: the courtroom hotkey handler is sess-gated and never runs
	// in the lobby, so dispatch this one here, only while offline.
	if a.sess == nil && a.ctx.hotkey != 0 && strings.ToLower(sdl.GetKeyName(a.ctx.hotkey)) == a.hotkeyFor(hotkeyQuickConnect) {
		a.quickConnect()
	}
	a.maybeKickUpdateCheck()        // one-shot, off the boot path (fires on frame 1)
	a.pollUpdate()                  // drain a found release
	a.handleUpdateInput(winW, winH) // modal/chip clicks resolve before screens
	a.pumpConnection()
	a.pumpBackgroundTabs()
	a.voicePump()              // live voice: drain mic→encode→send + decode→mix→play (no-op unless joined)
	a.handleTabBar(winW, winH) // chip clicks resolve BEFORE screens see them
	if a.pendingControlSwap {  // a click on the floating client last frame → take control now (before any draw)
		a.pendingControlSwap = false
		a.controlPinnedClient()
	}
	a.drainWarnings()
	a.drainThumbs() // loaded low-q stand-ins → T1 (render thread; bounded per frame)
	a.pollThemeApply()
	a.pollManifest()
	a.maybeProbeCasing() // OFF unless the user picked Auto casing (cheap no-op otherwise)
	a.pollCasingProbe()
	a.pollFontChain()
	a.pollLogBrowser()
	a.pollEmojiFont()
	a.updatePingLoop()             // #128: keep the ping loop targeting the active conn (no-op/no goroutine when the chip is off)
	if a.ctx.TakeWantsFallback() { // a non-ASCII rune was drawn → kick the one broad-font read
		a.ensureFallbackFontLoad()
	}
	a.pollFallbackFont()
	if a.ctx.TakeWantsCJK() { // a CJK letter was drawn → kick the (independent) CJK-font read
		a.ensureCJKFontLoad()
	}
	a.pollCJKFont()
	a.pollNotebook()
	a.pollJukebox()
	a.pollCharBind()
	a.pollJukeBind()
	a.pollMacroBind()
	a.pollShownameBind()
	a.pollICPhraseBind()
	a.pollVoicePTT()        // voice push-to-talk: capture the rebind, or toggle the mic on the bound key
	a.pollStylePresetBind() // #126
	a.pollAutoReconnect()   // M2: due auto-retry fires from the lobby; a single time-compare otherwise
	a.pollTimer()           // #97 local alarm: one compare while running, zero cost when idle
	a.pollDownload()
	a.pollMakerExport() // M16: deliver the self-contained archive export result
	a.pollGifExport()   // M16: deliver the off-thread GIF encode result
	a.pollCharMeta()    // land remote char.ini fetches (per-character blips + chatbox skins)
	a.pollBgList()      // drain bg discovery even when the picker is closed (slideshow)
	a.processOOCQueue()
	a.iconAskBudget = charIconAskPerFrame // shared demand budget (icons, emote buttons)
	switch {
	case a.gifExporting:
		// M16 GIF export drives the viewport itself in the draw-phase tick — skip
		// the normal scene driving so they don't fight over the shared viewport.
	case a.replaying && a.replayRoom != nil:
		// M16 replay: drive the recorded scene instead of the live one (feed the
		// next event when the room goes idle so the courtroom's own pacing times
		// it; the viewport reads the replay scene via renderScene too). Wrapped
		// so a panic in a bad/edge recording stops the replay, not the app.
		a.driveReplay(dt)
	case a.makerOpen:
		// M16 scene maker: drive the preview-pane room (NOT the live room) so the
		// shared viewport's anim state tracks the previewed line, not whatever the
		// live courtroom is doing behind the overlay.
		a.driveMakerPreview(dt)
	case a.room != nil:
		a.healScenery()
		if a.roomPreAdvanced {
			a.roomPreAdvanced = false // the audio-paced loop already advanced the room this present cycle; just draw it
		} else {
			a.room.Update(dt)
		}
		a.applySpriteOverrides()
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.SetSpriteLoadMode(a.vpSpriteLoadMode())                                           // cold-load flash mitigation (default hold-previous = webAO-style bridge; Blank = the original byte-identical gap)
		a.d.Viewport.SetClipSprites(a.d.Prefs.ClipSpritesToStageOn())                                  // viewport sprite mask (default ON): offsets can't spill past the stage
		a.d.Viewport.SetHoldMaxAge(time.Duration(a.d.Prefs.HoldPrevMaxAgeMs()) * time.Millisecond)     // hold-previous stand-in cap (0 = forever)
		a.d.Viewport.SetHoldDebugTint(a.d.Prefs.HoldDebugTintOn())                                     // amber-tint stand-ins (diagnostics)
		a.d.Viewport.SetThumbSprites(a.d.Prefs.ThumbCacheOn())                                         // opt-in low-q thumbnail stand-ins on the cold miss path
		a.d.Viewport.SetCrossfade(a.crossfadeDur())                                                    // speaker-swap blend (0 = off; zeroed under Reduce motion)
		a.d.Viewport.SetPostFX(a.postFX())                                                             // #10 retro overlays
		a.d.Viewport.SetWeather(render.Weather(a.d.Prefs.WeatherType()), a.d.Prefs.WeatherIntensity()) // #124 ambient weather
		a.d.Viewport.Update(&a.room.Scene, dt)
		if a.splitActive() { // drive the pinned right-pane stage on its OWN viewport
			a.splitRoom.Update(dt)
			a.splitVP.SetSpriteFX(a.spriteFX())
			a.splitVP.SetSpriteLoadMode(a.vpSpriteLoadMode())
			a.splitVP.SetClipSprites(a.d.Prefs.ClipSpritesToStageOn())
			a.splitVP.SetHoldMaxAge(time.Duration(a.d.Prefs.HoldPrevMaxAgeMs()) * time.Millisecond)
			a.splitVP.SetHoldDebugTint(a.d.Prefs.HoldDebugTintOn())
			a.splitVP.SetThumbSprites(a.d.Prefs.ThumbCacheOn())
			a.splitVP.SetCrossfade(a.crossfadeDur())
			a.splitVP.Update(&a.splitRoom.Scene, dt)
		}
		// Music ducking: dip music while a message is on stage (shout/preanim/
		// talking), restore at idle/linger. Transition-driven — SetVolumes is
		// touched only when the duck state flips, and the prefs read is
		// short-circuited so it costs nothing between messages.
		p := a.room.Phase()
		wantDuck := p != courtroom.PhaseIdle && p != courtroom.PhaseLinger && a.d.Prefs.MusicDucking()
		if wantDuck != a.musicDucked {
			a.musicDucked = wantDuck
			a.applyAudioVolumes()
		}
		a.updateSlideshow(p)
	}
	a.d.Audio.Frame()
	a.d.Pump.Frame()
	// Touch AFTER the pump so the live stage wins recency over this tick's
	// upload burst — an eviction of the picture on screen was the "window
	// randomly redraws" blink. Map probes only; see keepSceneAssetsWarm.
	a.keepSceneAssetsWarm()
	a.d.Store.DrainDestroyQueue()

	// Screen switch: tear down any hover-preview riding across it. Every close
	// path is a per-screen draw tail, so a preview still open when the screen
	// changes under it (char pick → PV lands, a pointer-fenced confirm-modal
	// click, a hotkey / server-driven jump) has NO surviving owner: invisible on
	// the new screen, its stale box rect still claimed wheel/press below, and
	// its stale trigger id pinned close-on-leave open forever. Must run before
	// handlePreviewInput so the ghost box never eats the new screen's input.
	a.noteScreenTransition()
	// Sprite-preview wheel zoom + drag, claimed before any screen draws so it
	// wins the wheel/press over the grid scroll and icon clicks under the box.
	a.handlePreviewInput()

	// While a confirm modal (Disconnect / hide-sprite) is up, the modal OWNS the
	// pointer: fence it so the screen + overlays behind draw click-proof (no
	// fat-finger underneath). Restored just before the modal draws, below.
	if a.confirmDisconnect || a.hidePrompt != "" || a.showQuitConfirm {
		a.ctx.fencePointer()
	} else if a.hkSheetFencesPointer(winW, winH) {
		// The hotkey sheet floats over EVERY screen and draws at the frame tail:
		// while the cursor sits on it (or its drag/resize is in flight), the
		// screens draw pointer-blind and the sheet unfences for its own pass
		// below — without this, scrolling the sheet also scrolled the lobby /
		// settings list underneath it (the kit has no z-aware input; the fence
		// IS the layering). The courtroom pass already applies the same rule
		// via boxFencesPointer, so this only changes the other screens.
		a.ctx.fencePointer()
	}

	// #M2 S1: set/RELEASE the emoji-picker modal fence before any screen draws. modalOn
	// persists across frames, so an un-released fence freezes the whole UI (the reported
	// open-then-close bug).
	a.emojiPickerFence(a.ctx)
	a.reactPickerFence(a.ctx) // #2: same modal-fence discipline as the emoji picker
	a.rosterMenuFence(a.ctx)  // player-row … menu: same discipline (rostermenu.go)
	a.updateModalFence(a.ctx) // What's New modal: modal fence on ANY screen (raw pointIn hit tests inside)

	if a.gifExporting {
		// M16 GIF export: owns the viewport (renders the scene offscreen) — tick a
		// batch of frames on the render thread, behind a progress overlay, instead
		// of any screen. Highest precedence: gif > replay > maker > screen.
		a.tickGifExport()
		a.drawGifProgress(winW, winH)
	} else if a.replaying && a.replayRoom != nil {
		// M16: a replay takes over the whole window via the guarded overlay,
		// drawn INSTEAD of any screen — so its controls own the input AND every
		// replay render path is the recover-wrapped one (a themed courtroom or a
		// missing theme can't crash a replay this way). Precedence is
		// replaying > makerOpen > screen: a Preview launched from the maker shows
		// the replay; on ■ Stop the maker (still makerOpen) reappears intact.
		a.drawReplayOverlay(winW, winH)
	} else if a.makerOpen {
		// M16 scene maker: a full-window editor overlay (scenemaker.go), drawn
		// instead of any screen so it owns input.
		a.drawSceneMaker(winW, winH)
	} else {
		switch a.screen {
		case ScreenLobby:
			a.drawLobby(winW, winH)
		case ScreenCharSelect:
			a.drawCharSelect(winW, winH)
		case ScreenCourtroom:
			a.drawCourtroom(winW, winH)      // primary always full-screen; the pinned server floats on top
			a.drawFloatingPanels(winW, winH) // non-blocking Extras + Pair + the floating client window, on top of the live courtroom (input already restored)
			if a.extrasSurfaceLive() {       // torn-off tab panels: live court, no modal, not editing (edit-mode draws them inside drawCourtroom)
				a.drawTornTabs(winW, winH) // interactive content, fenced by boxFencesPointer (torntabs.go)
			}
			a.drawPalette(winW, winH)          // #39: command palette (Ctrl+Space), above panels, below pickers
			a.drawEmojiPicker(winW, winH)      // #M2 S1: emoji picker overlay (modal-fenced in drawCourtroom)
			a.drawReactPalette(winW, winH)     // #2: reaction palette overlay (modal-fenced)
			a.drawRosterMenu(winW, winH)       // player-row … menu (modal-fenced; docked AND torn-off players tabs)
			a.drawGroupInviteToast(winW, winH) // group invite Accept/Decline banner (only when one is pending)
		case ScreenSettings:
			a.drawSettings(winW, winH)
		case ScreenAbout:
			a.drawAbout(winW, winH)
		case ScreenChangelog:
			a.drawChangelog(winW, winH)
		case ScreenServerHelp:
			a.drawServerHelp(winW, winH)
		case ScreenLogs:
			a.drawLogBrowser(winW, winH)
		case ScreenHelp:
			a.drawHelp(winW, winH)
		}
	}
	// The tab strip floats over every screen (input was consumed at the
	// top of the frame; this is just paint) — EXCEPT while the classic layout
	// editor is armed: there it paints inside drawCourtroom, under the editor
	// overlay, so its chips can't cover the editor's banner and controls (the
	// "layering mess" playtest shot: a server chip parked top-right sat on the
	// Snap/Done buttons).
	if !a.classicEdit {
		a.drawTabBar(winW, winH)
	}
	// Download progress chip floats under the strip while a grab runs.
	a.drawDownloadIndicator(winW)
	a.drawPingChip(8, winH-16) // #128 connection-quality chip (bottom-left; off by default)
	// Perf HUD (F3) and the debug overlay paint over every screen
	// (allocs acceptable: opt-in diagnostics paths, never on by default).
	if a.perfHUD {
		a.drawPerfHUD(winW, winH)
	}
	if a.d.Prefs.DebugOverlayEnabled() {
		a.drawDebugOverlay(winW, winH)
	}
	if a.showHotkeys {
		// Restore the pointer for the sheet's own pass — it was fenced above
		// while hovered/dragged so the screens beneath drew pointer-blind.
		// Skipped while a confirm modal is up: that fence belongs to the modal
		// (drawn after), and the sheet must stay inert under it.
		if !a.confirmDisconnect && a.hidePrompt == "" && !a.showQuitConfirm {
			a.ctx.unfencePointer()
		}
		a.drawHotkeyCheatSheet(winW, winH)
	}
	// M13: a found update shows a persistent chip (reopen) and, the first time,
	// the What's New patch-notes modal. Both no-op when no update was found.
	a.drawUpdateAvailable(winW, winH)
	// Confirm modals: restore the pointer (fenced above) for the modal's own
	// buttons, then paint it over everything. One at a time.
	if a.confirmDisconnect || a.hidePrompt != "" || a.showQuitConfirm {
		a.ctx.unfencePointer()
		switch {
		case a.showQuitConfirm:
			a.drawQuitConfirm(winW, winH)
		case a.confirmDisconnect:
			a.drawDisconnectConfirm(winW, winH)
		default:
			a.drawHideSpriteConfirm(winW, winH)
		}
	}
	// Deferred kit overlays (open dropdown lists) stack above everything.
	a.ctx.FinishFrame()
	// Hover hints paint last so they sit above every cell/overlay.
	a.ctx.drawTooltip(winW, winH)
}

// applyThemeAsync loads the selected theme's visible pieces off-thread —
// the chatbox skin (chatbox.webp/png in the theme dir, AO2 convention)
// and the message/showname font colors — and publishes them to the render
// thread via themeRes. Settings re-triggers it on every theme change.
// themeLoadRoots orders the content roots theme.Load searches for theme `name`.
// A custom themes folder (customRoot) is searched FIRST for named themes — but is
// dropped for the stock "default": theme packs ship their own themes/default, and
// since the custom root wins, that custom default would otherwise SHADOW the
// built-in one, making the stock default unreachable once you've set a folder
// (#87). The app directory is always the built-in fallback. Pure, for testing.
func themeLoadRoots(name, customRoot, exeDir string) []string {
	roots := make([]string, 0, 2)
	if customRoot != "" && name != theme.DefaultThemeName {
		roots = append(roots, customRoot)
	}
	if exeDir != "" {
		roots = append(roots, exeDir)
	}
	return roots
}

func (a *App) applyThemeAsync() {
	name, dir := a.d.Prefs.Theme()
	// Per-server theme binding: while this session declares one, it wins
	// over the global pick (set on connect from ServerWarmInfo.Theme;
	// Disconnect/resetSessionState clears it and re-applies the global).
	if a.themeBound != "" {
		name = a.themeBound
	}
	anims := a.d.Prefs.AnimationsEnabled()
	gen := a.themeGen.Add(1)
	go func() {
		res := themeApply{
			gen:    gen,
			name:   name,
			images: map[string]*assets.Decoded{},
			sounds: map[string]string{},
		}
		// Users persist every shape of path (the root, the themes\ folder
		// itself, one theme inside it, quoted Copy-as-Path) — normalize
		// HERE so every apply resolves like the settings scanner does,
		// not only applies that happened to follow a finished scan.
		root, _ := normalizeThemeRoot(dir)
		exeDir := ""
		if exe, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(exe)
		}
		t, err := theme.Load(name, themeLoadRoots(name, root, exeDir))
		if err == nil {
			res.iniKeys = t.KeyCount()
			res.probed = t.Dirs()
			res.fontPath = t.FontFile() // the theme's own bundled font, if any (#6)
			if msg := t.Font("message"); t.HasFont("message") {
				res.msgCol = sdl.Color{R: msg.Color.R, G: msg.Color.G, B: msg.Color.B, A: 255}
				res.hasMsg = true
			}
			if sn := t.Font("showname"); t.HasFont("showname") {
				res.nameCol = sdl.Color{R: sn.Color.R, G: sn.Color.G, B: sn.Color.B, A: 255}
				res.hasName = true
			}
			loadStem := func(stem string, candidates []string, exts []string) {
				for _, cand := range candidates {
					path, ok := t.FindAsset(cand, exts)
					if !ok {
						continue
					}
					data, rerr := os.ReadFile(path)
					if rerr != nil {
						continue
					}
					d, derr := assets.DecodeImage(data, anims)
					if derr != nil {
						continue
					}
					res.images[stem] = d
					if stem == themeStemChatbox {
						res.chatboxFile = filepath.Base(path)
						res.chatboxDir = filepath.Dir(path)
					}
					return
				}
			}
			for stem, candidates := range themeImageStems() {
				loadStem(stem, candidates, themeImageExts)
			}
			for key, candidates := range themeButtonStems() {
				loadStem(themeBtnPrefix+key, candidates, themeButtonExts)
			}
			// Readability guard: drop theme ink that has no contrast
			// against the skin it ships with (see the constants above).
			if box := res.images[themeStemChatbox]; box != nil && len(box.Frames) > 0 {
				skin := avgSkinLuma(box.Frames[0], lumaSampleStep)
				var dropped []string
				if res.hasMsg && absInt(colLuma(res.msgCol)-skin) < minInkSkinContrast {
					res.hasMsg = false
					dropped = append(dropped, "message")
				}
				if res.hasName && absInt(colLuma(res.nameCol)-skin) < minInkSkinContrast {
					res.hasName = false
					dropped = append(dropped, "showname")
				}
				if len(dropped) > 0 {
					res.inkGuard = fmt.Sprintf("theme %s color unreadable on its chatbox skin (luma %d) — using client default",
						strings.Join(dropped, "+"), skin)
				}
			}
			// Courtroom geometry (the part that makes a theme LOOK like
			// itself): rects in design-space pixels, plus the emote grid
			// cell metrics.
			res.layout = map[string]theme.Rect{}
			for _, key := range themeLayoutKeys {
				if r, ok := t.ElementRect(key); ok {
					res.layout[key] = r
				}
			}
			res.emoteCell = designPair(t, "emote_button_size", defaultEmoteCellPx, defaultEmoteCellPx)
			res.emoteGap = designPair(t, "emote_button_spacing", defaultEmoteGapPx, defaultEmoteGapPx)
			// The QSS palette ("css stuff"): AO2 ≥ 2.10 themes color the
			// client through courtroom_stylesheets.css.
			if path, ok := t.FindAsset("courtroom_stylesheets", []string{".css"}); ok {
				if data, rerr := os.ReadFile(path); rerr == nil {
					res.palette = theme.ParseStylesheet(data)
				}
			}
		}
		// Sound names resolve even with no theme on disk: the stock
		// fallbacks keep WT/CE/verdict/modcall audible on bare installs.
		for _, sk := range themeSoundKeys {
			val := sk.fallback
			if t != nil {
				for _, alias := range sk.aliases {
					if v, ok := t.SoundName(alias); ok && v != "" {
						val = v
						break
					}
				}
			}
			if val != "" {
				res.sounds[sk.key] = val
			}
		}
		if t != nil {
			for _, pk := range themePenaltyKeys {
				if v, ok := t.PenaltyValue(pk); ok && v != "" {
					res.sounds[pk] = v
				}
			}
		}
		// Newest-wins publish: never overwrite a higher-gen result (this
		// load may have been outraced by a later pick).
		for {
			old := a.themeRes.Load()
			if old != nil && old.gen > gen {
				return
			}
			if a.themeRes.CompareAndSwap(old, &res) {
				return
			}
		}
	}()
}

// pollThemeApply lands theme pieces on the render thread: upload (or
// drop) the chatbox skin, adopt the colors, force a chat re-raster, and
// report what happened (settings status line + debug log).
func (a *App) pollThemeApply() {
	res := a.themeRes.Swap(nil)
	if res == nil {
		return
	}
	// Upload every loaded stem into the PINNED tier — theme chrome must
	// never lose an eviction fight against streaming sprites (the cause
	// of the black-flashing backdrop / glitching buttons) — and drop
	// residents the new theme doesn't ship (plain stems and btn/ art).
	for stem, d := range res.images {
		if err := a.d.Store.UploadPinned(themeTexKey(stem), d); err == nil {
			a.themeTex[stem] = true
		}
	}
	for stem := range a.themeTex {
		if res.images[stem] == nil {
			a.d.Store.Remove(themeTexKey(stem))
			delete(a.themeTex, stem)
		}
	}
	a.themeChatbox = a.themeTex[themeStemChatbox]
	// Geometry: pristine design rects kept aside, the user's layout-editor
	// overrides applied on a copy, scaled cache invalidated.
	a.themeRectsOrig = res.layout
	rects := make(map[string]theme.Rect, len(res.layout))
	for k, v := range res.layout {
		rects[k] = v
	}
	a.themeRects = a.applyRectOverrides(rects)
	a.themeEmoteCell, a.themeEmoteGap = res.emoteCell, res.emoteGap
	a.themeLay.valid = false
	a.themeSounds = res.sounds
	a.themeAt = time.Now() // restart the theme-art animation clock
	// Apply (or restore) the stylesheet palette; label textures are
	// color-keyed, so purge the text cache to re-rasterize in new colors.
	a.themePalette = res.palette // remember it so a chrome-preset change can re-overlay it (#M3)
	applyThemePalette(res.palette)
	a.ctx.purgeTextCache()
	a.pushRealizationToRoom()
	a.themeMsgCol, a.themeHasMsg = res.msgCol, res.hasMsg
	a.themeNameCol, a.themeHasName = res.nameCol, res.hasName
	a.rasterText = "" // re-raster the current message with theme colors
	a.themeAppliedName = res.name
	// Theme font (#6): an AO theme that ships its own .ttf now applies it (below a
	// manual font / the dyslexia toggle). Re-resolve only when it actually changed,
	// so a same-font theme switch doesn't re-kick the off-thread font read.
	if a.themeFontFile != res.fontPath {
		a.themeFontFile = res.fontPath
		a.applyFontConfig()
	}
	line := themeApplySummary(res)
	settings.statusLine = clampLine(line)
	a.pushDebug(line)
	if res.inkGuard != "" {
		a.pushDebug(res.inkGuard)
	}
	// Theme chrome uploads PINNED (above), so applying a theme can evict
	// emote-button art from T1. The gen-keyed page cache re-Gets on the
	// bumped generation, but evicted slots then sit in the text fallback
	// until demandAsset's per-slot retry window elapses. Force a fresh
	// re-demand so the emote grid heals immediately after a theme switch
	// (the same invariant pollCharINI applies on a character change).
	a.emoteBtnOff, a.emoteBtnOn, a.emoteIconPages = nil, nil, nil
	a.emoteAsk, a.emoteIconAsk = nil, nil
}

// ensureThemeForSession re-applies the theme whenever the session's
// binding (or its absence) disagrees with what is on screen — connect,
// tab switches, and disconnect funnel through here. No-op when the
// right theme is already applied (loads are async but not free).
func (a *App) ensureThemeForSession() {
	want := a.themeBound
	if want == "" {
		want, _ = a.d.Prefs.Theme()
	}
	if want != "" && want != a.themeAppliedName {
		a.applyThemeAsync()
	}
}

// themeApplySummary turns one apply into a human-readable verdict, so
// "nothing happened" is always distinguishable from "applied fine but this
// theme only restyles the courtroom".
func themeApplySummary(res *themeApply) string {
	switch {
	case res.chatboxFile != "":
		return fmt.Sprintf("Theme %q applied: %s + %d theme images + %d INI keys (%s)",
			res.name, res.chatboxFile, len(res.images)-1, res.iniKeys, res.chatboxDir)
	case len(res.images) > 0 || res.iniKeys > 0:
		return fmt.Sprintf("Theme %q applied: %d theme images + %d INI keys, no chatbox skin (flat panel)",
			res.name, len(res.images), res.iniKeys)
	default:
		return fmt.Sprintf("Theme %q: nothing found — probed %s",
			res.name, strings.Join(res.probed, " ; "))
	}
}

// themePage fetches a resident theme texture page through a generation-
// keyed cache (the grid trick): while the store generation is unchanged,
// repeat lookups cost one plain map probe — no LRU lock, no recency churn
// from overlay draws that run every frame. Misses heal (paced) and cache
// negatively until the next upload/eviction bumps the generation.
func (a *App) themePage(stem string) (*render.TexturePage, bool) {
	if !a.themeTex[stem] {
		return nil, false
	}
	gen := a.d.Store.Generation()
	if gen != a.themePagesGen {
		clear(a.themePages)
		a.themePagesGen = gen
	}
	if page, cached := a.themePages[stem]; cached {
		return page, page != nil
	}
	page, ok := a.d.Store.Get(themeTexKey(stem))
	if !ok || len(page.Frames) == 0 {
		a.themePages[stem] = nil
		a.healTheme()
		return nil, false
	}
	a.themePages[stem] = page
	return page, true
}

// now is the frame's clock snapshot (real time for callers outside a
// frame, e.g. headless tests).
func (a *App) now() time.Time {
	if a.frameNow.IsZero() {
		return time.Now()
	}
	return a.frameNow
}

// themeElapsed is the animation clock for looping theme art: time since
// the theme applied, so every animated stem (chatbox, buttons, backdrops)
// steps with pageFrameLoop instead of freezing on Frames[0]. Reads the
// frame snapshot — themed frames step this at ~10 draw sites.
func (a *App) themeElapsed() time.Duration {
	if a.themeAt.IsZero() {
		return 0
	}
	return a.now().Sub(a.themeAt)
}

// themeFrame picks the current animation frame for a theme page — static
// pages cost one len check, animated ones loop on the theme clock (and mark
// the frame's animated-chrome census so the static skip keeps stepping them).
func (a *App) themeFrame(page *render.TexturePage) *sdl.Texture {
	if len(page.Frames) == 1 {
		return page.Frames[0]
	}
	a.NoteAnimating()
	return page.Frames[pageFrameLoop(page, a.themeElapsed())]
}

// pushRealizationToRoom hands the courtroom the resolved realization sound
// URL (the state machine plays it at message-display time, where AO2's
// handle_ic_message does — the theme INI itself lives UI-side).
func (a *App) pushRealizationToRoom() {
	if a.room == nil {
		return
	}
	if name := a.themeSounds["realization"]; name != "" {
		a.room.RealizationSFX = a.urls.SFX(name) // AssetType: SFX (realization)
	} else {
		a.room.RealizationSFX = ""
	}
}

// healTheme re-runs the theme load when T1 evicted a theme texture the UI
// still needs — paced to one ask per themeHealPeriod (healScenery pattern).
func (a *App) healTheme() {
	if time.Since(a.themeHealAt) > themeHealPeriod {
		a.themeHealAt = time.Now()
		a.applyThemeAsync()
	}
}

// pushDebug appends one line to the bounded failure log (debug overlay).
// Consecutive duplicates collapse into a ×N suffix so a chatty source
// (an ARUP every few seconds, say) can't flush real failures out of the
// ring. Render thread only.
func (a *App) pushDebug(line string) {
	if line == a.debugLast && len(a.debugLog) > 0 {
		a.debugRepeat++
		a.debugLog[len(a.debugLog)-1] = time.Now().Format("15:04:05 ") +
			line + fmt.Sprintf("  ×%d", a.debugRepeat)
		return
	}
	a.debugLast = line
	a.debugRepeat = 1
	a.debugLog = append(a.debugLog, time.Now().Format("15:04:05 ")+line)
	if len(a.debugLog) > debugLogCap {
		a.debugLog = a.debugLog[len(a.debugLog)-debugLogCap:]
	}
}

// drainThumbs uploads loaded low-q sprite thumbnails into T1 under their
// thumb:// keys (render thread — Upload's home). Bounded per frame so a burst
// of thumb loads can't stall a frame; the channel itself is bounded too.
func (a *App) drainThumbs() {
	th := a.d.Manager.Thumbs()
	if th == nil {
		return
	}
	const perFrame = 2 // thumbs are tiny; two uploads/frame clears any real burst
	for i := 0; i < perFrame; i++ {
		select {
		case r := <-th.Results():
			if r.Asset != nil {
				_ = a.d.Store.Upload(render.ThumbKeyPrefix+r.Base, r.Asset)
			}
		default:
			return
		}
	}
}

// drainWarnings empties the manager's missing-asset lane (bounded by its
// channel cap), keeping the newest for the §4 on-screen banner.
func (a *App) drainWarnings() {
	// The red banner is opt-in (default OFF — players found it noisy on
	// sparse-pack servers); read the toggle once per drain. We still drain
	// the channel (it's bounded) and log every failure to the debug overlay
	// regardless, so nothing is lost — only the on-screen banner is gated.
	show := a.d.Prefs.AssetWarningsOn()
	for {
		select {
		case w := <-a.d.Manager.Warnings():
			// A conclusively-missing char sprite also feeds the message
			// ceremony: a preanimation that can never arrive must not hold
			// the stage for the full PreanimTimeout (AO2-Client skips missing
			// preanims instantly — Courtroom.NotifyAssetMissing). One Manager
			// serves every room, so this is the single relay point; each room
			// compares the base against its own current message, so wrong-room
			// bases are a string-compare no-op.
			if w.Type == assets.AssetTypeCharSprite {
				if a.room != nil {
					a.room.NotifyAssetMissing(w.Base)
				}
				if a.splitRoom != nil {
					a.splitRoom.NotifyAssetMissing(w.Base)
				}
				if a.replayRoom != nil {
					a.replayRoom.NotifyAssetMissing(w.Base)
				}
				if a.makerPreviewRoom != nil {
					a.makerPreviewRoom.NotifyAssetMissing(w.Base)
				}
			}
			line := "Missing asset: " + w.Base
			if len(w.Tried) > 0 {
				line += " (tried " + strings.Join(w.Tried, " ") + " — see Settings → formats)"
			}
			a.pushDebug(line)
			if show {
				a.warnLine = clampLine(line)
				a.warnAt = time.Now()
			}
		default:
			return
		}
	}
}

// warnActive reports whether the warning banner should still draw.
func (a *App) warnActive() bool {
	return a.warnLine != "" && time.Since(a.warnAt) < warnShowDuration
}

// spriteFX builds the optional sprite colour-FX struct from the user prefs
// (all off by default). A handful of uncontended RLocks once per frame — far
// cheaper than any snapshot/cache layer, and the render path stays 0-alloc
// regardless (pinned by TestRenderFrameRainbowZeroAllocs). The colour is only
// fetched when the solid wash is actually active. Shared by the live + replay
// render paths.
func (a *App) spriteFX() render.SpriteFX {
	fx := render.SpriteFX{
		Rainbow:         a.d.Prefs.RainbowSpritesOn(),
		Solid:           a.d.Prefs.SpriteSolidTintOn(),
		Glow:            a.d.Prefs.RainbowSpriteGlowOn(),
		PairDesync:      a.d.Prefs.RainbowPairDesyncOn(),
		PerCharHue:      a.d.Prefs.RainbowPerCharOn(),
		Wobble:          a.d.Prefs.SpriteWobbleOn(),
		Spin:            a.d.Prefs.SpriteSpinOn(),
		ShoutPunch:      a.d.Prefs.ShoutPunchOn(),
		Entrance:        a.d.Prefs.AnimateEntrancesOn(),
		DoF:             a.d.Prefs.DepthOfFieldOn(),
		Spotlight:       a.d.Prefs.SpotlightOn(),
		IdleBreath:      a.d.Prefs.IdleBreathOn() && !a.d.Prefs.ReduceMotion(), // #122; ReduceMotion suppresses (accessibility)
		BreathBob:       a.d.Prefs.BreathBobOn(),
		BreathScale:     a.d.Prefs.BreathScaleOn(),
		Reflection:      a.d.Prefs.ReflectionOn(),
		Speed:           a.d.Prefs.RainbowSpeed(),
		Vividness:       a.d.Prefs.RainbowVividness(),
		SpotlightLevel:  a.d.Prefs.SpotlightLevel(),
		BreathAmp:       a.d.Prefs.BreathAmp(),
		BreathSpeed:     a.d.Prefs.BreathSpeed(),
		ReflectStrength: a.d.Prefs.ReflectStrength(),
	}
	if fx.Solid && !fx.Rainbow { // colour only matters for the solid wash, and rainbow wins
		rgb := a.d.Prefs.SpriteTintColorRGB()
		fx.SolidR = uint8(rgb >> 16 & 0xFF)
		fx.SolidG = uint8(rgb >> 8 & 0xFF)
		fx.SolidB = uint8(rgb & 0xFF)
	}
	return fx
}

// cycleWeather advances the #124 ambient weather (None → Snow → Rain → Sakura → Embers → None)
// and flashes the new name — the hands-free keybind.
func (a *App) cycleWeather() {
	w := (a.d.Prefs.WeatherType() + 1) % int(render.WeatherCount)
	a.d.Prefs.SetWeatherType(w)
	a.warnLine = "Weather: " + render.WeatherName(render.Weather(w))
	a.warnAt = time.Now()
}

// postFX mirrors the user's #10 post-processing toggles onto the viewport each frame.
func (a *App) postFX() render.PostFX {
	return render.PostFX{
		Vignette:  a.d.Prefs.PostVignetteOn(),
		Scanlines: a.d.Prefs.PostScanlinesOn(),
		Grain:     a.d.Prefs.PostGrainOn(),
		CRT:       a.d.Prefs.PostCRTOn(),
	}
}

// applySpriteOverrides lets the user's drag positions win over the
// message's offsets every frame (one map probe per visible layer; free
// while no overrides exist).
func (a *App) applySpriteOverrides() {
	hideDesk := a.d.Prefs.HideDeskOn()
	if !hideDesk && len(a.spriteOv) == 0 && len(a.hiddenSprites) == 0 {
		return // nothing hidden/moved (the common case): one pref read, then out
	}
	sc := &a.room.Scene
	if hideDesk {
		sc.ShowDesk = false // hide-desk option (Settings toggle + keybind)
	}
	for _, layer := range [...]*courtroom.SpriteLayer{&sc.Speaker, &sc.Pair} {
		if layer.Name == "" {
			continue
		}
		key := strings.ToLower(layer.Name)
		// Hidden sprites ("Missingno"): drop the layer entirely this session. The
		// check is before the !Visible skip — a hidden sprite is otherwise visible.
		if _, hidden := a.hiddenSprites[key]; hidden {
			layer.Visible = false
			continue
		}
		if !layer.Visible {
			continue
		}
		if ov, ok := a.spriteOv[key]; ok {
			layer.OffsetX, layer.OffsetY = ov[0], ov[1]
		}
	}
}

// healScenery re-demands a live scene asset when T1 lost it (LRU eviction, or
// a prefetch that never landed): without it the viewport can only show black
// until the next position change. Covers the background, the desk, AND the
// speaker/pair sprites — the same eviction that blanks the background can drop
// a character sprite (a hover-preview HIGH fetch, or churn in a busy room), and
// nothing else re-demands it, so it vanishes mid-message. Paced one ask per
// base per charIconRetryInterval; HIGH because this is the live scene.
func (a *App) healScenery() {
	sc := &a.room.Scene
	now := time.Now()
	// Each heal consults the shared per-scene futility budget (sceneHealAllowed):
	// when the settled working set exceeds the T1 main tier, every heal evicts
	// another live base and this loop — paced or not — churned decode→upload→
	// evict→re-demand forever (the focused half of the idle-CPU-burn report).
	if sc.BackgroundBase != "" && !(sc.BackgroundBase == a.bgAskBase && now.Sub(a.bgAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.BackgroundBase) && a.sceneHealAllowed(sc.BackgroundBase) {
		a.bgAskBase, a.bgAskAt = sc.BackgroundBase, now
		a.d.Manager.Prefetch(sc.BackgroundBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	}
	if sc.DeskBase != "" && !(sc.DeskBase == a.deskAskBase && now.Sub(a.deskAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.DeskBase) && a.sceneHealAllowed(sc.DeskBase) {
		a.deskAskBase, a.deskAskAt = sc.DeskBase, now
		a.d.Manager.Prefetch(sc.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	}
	a.healSpriteLayer(&sc.Speaker, &a.spkAskBase, &a.spkAskAt, now)
	a.healSpriteLayer(&sc.Pair, &a.pairAskBase, &a.pairAskAt, now)
}

// healSpriteLayer re-demands one visible character sprite layer if its active
// base was evicted from T1. Uses the prefix→bare fallback the lifecycle uses
// (bareSpriteBase reconstructs the unprefixed spelling), paced per layer like
// the scenery above. No-op while the sprite is resident or recently asked.
func (a *App) healSpriteLayer(layer *courtroom.SpriteLayer, askBase *string, askAt *time.Time, now time.Time) {
	if !layer.Visible || layer.Active == "" {
		return
	}
	if *askBase == layer.Active && now.Sub(*askAt) < charIconRetryInterval {
		return // recently asked for this exact base; let it land
	}
	if a.d.Store.Contains(layer.Active) {
		return // resident — nothing to heal
	}
	if !a.sceneHealAllowed(layer.Active) {
		return // per-scene futility budget spent (over-tier churn / permanent 404) — quiet until the scene changes
	}
	*askBase, *askAt = layer.Active, now
	a.d.Manager.PrefetchWithFallback(layer.Active, bareSpriteBase(layer.Active), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (heal evicted live sprite)
	// Cold sprite + thumbnails on: also ask for the low-q stand-in (paced by the
	// same retry window above; the loader reads + decodes off-thread and
	// drainThumbs uploads under the thumb:// key the renderer probes).
	if th := a.d.Manager.Thumbs(); th != nil && th.Enabled() && !a.d.Store.Contains(render.ThumbKeyPrefix+layer.Active) {
		th.RequestLoad(layer.Active)
	}
}

// bareSpriteBase reconstructs the unprefixed sprite spelling from an active
// base: it strips a leading "(a)"/"(b)" from the final path segment, turning
// URLBuilder.Emote's output back into EmoteBare's. Preanim/already-bare bases
// (no prefix) pass through unchanged, so PrefetchWithFallback just retries the
// same URL.
func bareSpriteBase(active string) string {
	i := strings.LastIndexByte(active, '/')
	seg := active[i+1:]
	if strings.HasPrefix(seg, "(a)") || strings.HasPrefix(seg, "(b)") {
		return active[:i+1] + seg[len("(a)"):]
	}
	return active
}

// icEntry is one IC log line with its AO text color preserved (rich
// scrollback: render, search, and export keep the color).
type icEntry struct {
	text        string
	color       int
	url         string // first http(s) link in the line ("" = none); makes the line clickable
	friend      bool   // sender is a highlighted friend (showname match) — glows in the log
	friendColor int32  // per-friend glow RGB (0xRRGGBB) from a `name=hex` entry; -1 = default friend tint
	speaker     string // displayed name prefix (for per-speaker name colours); "" = system/evidence line
	stamp       string // local arrival time ("15:04"), formatted once on append; prefixed in the log when ICTimestamps is on
	ref         uint32 // content-stable reaction ref (#2): MakeReactionRef(CharName, clean text); 0 = system line
}

// icStampLayout formats the IC log's per-line local time (24-hour HH:MM).
const icStampLayout = "15:04"

// icStamp is the local arrival time for a new IC log line, formatted ONCE here on
// append — never in the draw loop — so the timestamp prefix costs nothing per frame.
func (a *App) icStamp() string { return a.now().Format(icStampLayout) }

// friendMessage reports whether a message is from a highlighted friend on the
// given server — its showname, falling back to CharName (the displayName rule),
// matched case-insensitively — and the friend's per-entry glow colour (0xRRGGBB,
// or -1 for the default tint). Gated on the master toggle FIRST, so it's a cheap
// no-op when the feature is off (the default) — and the membership scan
// allocates nothing, so it's safe per message even in a catch-up burst.
// ignoreSpeaker reports whether an IC message is from an ignored player (#81),
// matched by showname-else-character — the only identity the MS wire carries (no
// UID). Called once per incoming IC message (not per frame); the empty-list
// default is one RLock and zero iterations, so an un-used ignore list is free.
func (a *App) ignoreSpeaker(m *protocol.ChatMessage) bool {
	if m == nil || a.serverKey == "" {
		return false
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	return a.d.Prefs.ServerIgnoreMatch(a.serverKey, name)
}

func (a *App) friendMessage(serverKey string, m *protocol.ChatMessage) (bool, int32) {
	if m == nil || serverKey == "" {
		return false, -1
	}
	// Cheap gate: if NO friend signal (glow / notify / sound) is enabled, skip
	// entirely — the default, so it costs nothing.
	if !a.d.Prefs.FriendHighlightOn() && !a.d.Prefs.FriendNotifyOn() && !a.d.Prefs.FriendSoundOn() && !a.d.Prefs.FriendOSToastOn() {
		return false, -1
	}
	name := strings.TrimSpace(m.Showname)
	if name == "" {
		name = strings.TrimSpace(m.CharName)
	}
	if name == "" {
		return false, -1
	}
	friend, color := a.d.Prefs.ServerFriendMatch(serverKey, name)
	return friend, int32(color)
}

// signalFriend fires the opt-in alert signals when a friend speaks: an in-app
// toast + window flash (FriendNotify) and a sound (FriendSound — a custom file,
// else the built-in ping). Streamer mode suppresses them (same as callwords).
// Called at the message seam when friendMessage is true (active OR background
// tab, so you're alerted even while looking at another server). The glow is
// drawn separately in the log.
func (a *App) signalFriend(serverName string, m *protocol.ChatMessage) {
	if a.d.Prefs.StreamerMode() || a.dndOn {
		return
	}
	if a.d.Prefs.FriendNotifyOn() {
		name := strings.TrimSpace(m.Showname)
		if name == "" {
			name = strings.TrimSpace(m.CharName)
		}
		line := name + " just spoke"
		if serverName != "" {
			line += " on " + serverName
		}
		a.warnLine = line
		a.warnAt = time.Now()
		a.ctx.FlashWindow()
	}
	if a.d.Prefs.FriendSoundOn() {
		// Custom sound if set, else the built-in ping — always audible. Same fix
		// as callwords: routing through the theme's word_call silenced the alert
		// on themes that name it but ship no loadable file.
		if f := a.d.Prefs.FriendSoundPath(); f != "" {
			a.d.Audio.PlayFile(f)
		} else {
			a.d.Audio.PlayAlert()
		}
	}
	// Desktop (OS) toast — only while you're tabbed away (#M4), rate-limited so a chatty
	// friend can't storm it.
	if a.d.Prefs.FriendOSToastOn() && !a.ctx.WindowFocused() && time.Since(a.lastOSToast) >= osToastMinInterval {
		a.lastOSToast = time.Now()
		name := strings.TrimSpace(m.Showname)
		if name == "" {
			name = strings.TrimSpace(m.CharName)
		}
		body := name + " just spoke"
		if serverName != "" {
			body += " on " + serverName
		}
		showOSToast("AsyncAO — friend online", body)
	}
}

// signalModcall pops a desktop toast when a modcall arrives, if the option is on
// (OFF by default) — a "you're being called" ping for mods who alt-tabbed away.
// Streamer mode suppresses it (modcall lines can carry IPs) and it shares the
// friend toast's rate limit so a modcall storm can't flood the desktop. Called
// at both seams (active + background tab).
func (a *App) signalModcall(serverName, notice string) {
	if !a.d.Prefs.ModcallToastOn() || a.d.Prefs.StreamerMode() || a.ctx.WindowFocused() {
		return // #M4: only toast when tabbed away (the in-app OOC + flash cover the focused case)
	}
	if time.Since(a.lastOSToast) < osToastMinInterval {
		return
	}
	a.lastOSToast = time.Now()
	body := strings.TrimSpace(notice)
	if serverName != "" {
		body = serverName + ": " + body
	}
	showOSToast("AsyncAO — mod call", body)
}

// switchAreaScrollback swaps the IC log to toArea's own history when per-area
// scrollback is on (opt-in). Best-effort: AO only tells us the area on an
// explicit area click (curArea), so server-initiated moves keep the current log
// — same as the default continuous behavior. Runs only on an area click, never
// per frame, and no-ops entirely when off, so it costs the default path nothing.
// Bounded FIFO over visited areas (rule §17.4). Both maps park per tab.
func (a *App) switchAreaScrollback(toArea string) {
	if !a.d.Prefs.PerAreaScrollbackOn() || toArea == a.curArea {
		return
	}
	if a.areaLogs == nil {
		a.areaLogs = map[string][]icEntry{}
	}
	// Save the area we're leaving (curArea); track its key order for eviction.
	if _, seen := a.areaLogs[a.curArea]; !seen {
		a.areaLogOrder = append(a.areaLogOrder, a.curArea)
	}
	a.areaLogs[a.curArea] = a.icLog
	for len(a.areaLogOrder) > areaLogCacheMax {
		oldest := a.areaLogOrder[0]
		a.areaLogOrder = a.areaLogOrder[1:]
		if oldest != toArea { // never evict the log we're about to load
			delete(a.areaLogs, oldest)
		}
	}
	// Load the target area's saved log (nil = unvisited = a fresh empty log).
	a.icLog = a.areaLogs[toArea]
	a.icLogSeq++ // invalidate the wrap + filter caches
	a.icScroll, a.icStick = 0, true
	a.icReadMark = len(a.icLog)
}

// logDetailed appends an IC message to the transcript when detailed logging is
// on (opt-in). The writer is opened lazily and does the disk write on its own
// goroutine, so the message seam never blocks. A no-op (one pref read) when off,
// so the default path is unaffected. Called at both message seams (active +
// background tab), so the transcript captures every server you're connected to.
func (a *App) logDetailed(server string, m *protocol.ChatMessage) {
	if m == nil || !a.d.Prefs.DetailedLogOn() {
		return
	}
	if w := a.transcriptFor(server); w != nil {
		w.write(detailedLogLine(time.Now(), m))
	}
}

// transcriptFor returns this server's transcript writer, opening its session file
// (logs/<server>/<date_time>.log) on first use. Bounded by transcriptServerCap so
// a churn of servers can't open unbounded files/goroutines. nil on error / at cap.
func (a *App) transcriptFor(server string) *transcriptWriter {
	if a.translogs == nil {
		a.translogs = make(map[string]*transcriptWriter)
	}
	if w, ok := a.translogs[server]; ok {
		return w
	}
	if len(a.translogs) >= transcriptServerCap {
		return nil
	}
	path, err := transcriptPathFor(server, time.Now())
	if err != nil {
		return nil
	}
	w, err := newTranscriptWriter(path)
	if err != nil {
		return nil
	}
	a.translogs[server] = w
	return w
}

// CloseTranscript flushes and closes every detailed-log writer at shutdown.
func (a *App) CloseTranscript() {
	for _, w := range a.translogs {
		w.close()
	}
}

func (a *App) pushIC(line string, color int, friend bool, friendColor int32, speaker string) {
	url := ""
	if urls := extractURLs(line, 1); len(urls) > 0 {
		url = urls[0]
	}
	a.icLog = append(a.icLog, icEntry{text: capLogLine(line), color: color, url: url, friend: friend, friendColor: friendColor, speaker: speaker, stamp: a.icStamp()})
	if len(a.icLog) > icLogCap {
		copy(a.icLog, a.icLog[len(a.icLog)-icLogCap:])
		a.icLog = a.icLog[:icLogCap]
	}
	a.icLogSeq++ // invalidate the filter cache (len alone lies at the cap)
}

// oocLineCap bounds ONE OOC entry (hostile-server guard). MOTDs are long
// multi-line texts — the old clampLine cut them at 120 chars, which is
// why MOTDs arrived truncated; they now wrap at draw time instead.
const oocLineCap = 4096

// pushOOC appends an OOC log line. speaker is the sender's name for a real OOC
// message (used by name colours), "" for system/[MOD CALL]/CLIENT lines. The
// two slices stay parallel (same cap), so display can look the speaker up by
// entry index without re-parsing the ": " (which would mis-tint system lines).
func (a *App) pushOOC(line, speaker string) {
	// Harvest /getarea "[uid] name" rows for click-to-pair, from the RAW text:
	// strip the "speaker: " prefix first, else when a server sends /getarea line
	// by line every "[uid]" row hides behind "ServerName: " and parses as nothing.
	text := line
	if speaker != "" && strings.HasPrefix(line, speaker+": ") {
		text = line[len(speaker)+2:]
	}
	a.parseAreaBlock(text)
	isAreaList := looksLikeAreaList(text)
	if isAreaList {
		// A /getarea snapshot just landed: re-merge its IPIDs (the one field PR/PU
		// lacks) into the live roster NOW, so a mod's Refresh shows them at once
		// instead of lagging until the next PR/PU packet. No-op off the PR/PU path
		// (rosterEqual gates it), so an ordinary chat line never pays for this.
		a.rebuildLiveRoster()
	}
	// An AUTO /getarea (the live list's silent fetch) is parsed for its data but
	// kept OUT of the OOC log so the refresh never spams the channel; a MANUAL
	// /getarea (the fetch buttons) doesn't set the flag, so it still shows.
	if isAreaList && a.now().Before(a.suppressAreaEchoUntil) {
		return // the entire /gas reply burst stays out of OOC, not just its first line
	}
	// /gas isn't supported on this server: the reply is a command error, not an
	// area list. Swallow it AND learn — so fetchRoster stops sending /gas (no
	// repeat "unknown command" spam); the live PR/PU roster works without it.
	if a.now().Before(a.suppressAreaEchoUntil) && looksLikeCommandError(text) {
		a.rosterCmdUnsupported = true
		return
	}
	// Best-effort: mirror a received PM into the Friends-tab DM thread too (it also
	// stays in the OOC log). On the real-OOC path only — AFTER the /gas suppression
	// returns above — so a suppressed area burst can't double-fire it.
	a.detectIncomingPM(text)
	if len(line) > oocLineCap {
		line = line[:oocLineCap] + "…"
	}
	a.oocLog = appendCapped(a.oocLog, line, icLogCap)
	a.oocSpeakers = appendCapped(a.oocSpeakers, speaker, icLogCap)
	a.oocSeq++ // invalidate the wrapped-lines cache
}

func appendCapped(list []string, line string, cap int) []string {
	list = append(list, line)
	if len(list) > cap {
		copy(list, list[len(list)-cap:])
		list = list[:cap]
	}
	return list
}

// capLogLine bounds one stored IC entry (hostile-server guard) WITHOUT the
// old 120-char "…" truncation — the IC log word-wraps long lines at draw time
// (icWrapped). Real IC messages (≤256 on the wire) are never cut. Byte-length
// fast path; the rune conversion only runs on a genuinely huge line.
func capLogLine(s string) string {
	if len(s) <= icLineCap {
		return s
	}
	if r := []rune(s); len(r) > icLineCap {
		return string(r[:icLineCap]) + "…"
	}
	return s
}

func clampLine(s string) string {
	runes := []rune(s)
	if len(runes) > logLineMax {
		return string(runes[:logLineMax]) + "…"
	}
	return s
}

func hostOfURL(u string) string {
	const sep = "://"
	i := strings.Index(u, sep)
	if i < 0 {
		return ""
	}
	rest := u[i+len(sep):]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// tierColor maps a server entry to its lobby color.
func tierColor(e network.ServerEntry) sdl.Color {
	switch e.Security() {
	case network.SecurityWSS:
		return ColTierGreen
	case network.SecurityWS:
		return ColTierYellow
	default:
		return ColTierBlack
	}
}
