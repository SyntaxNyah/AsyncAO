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
}

// App is the whole client UI state machine. Render thread only.
type App struct {
	d   Deps
	ctx *Ctx

	screen     Screen
	prevScreen Screen // for settings/about back navigation

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
	oocName  string
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

	// --- server format manifest (extensions.json autodetect) ---
	manifestRes chan manifestFetch

	// --- font override pipeline (file bytes read off-thread) ---
	fontRes chan fontLoad
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
	// translog is the detailed-transcript writer (opt-in): one file for all
	// servers (each line names the server), opened lazily on the first logged
	// message, closed at shutdown. nil = off / not yet opened.
	translog *transcriptWriter

	// --- M13 self-update (one-shot launch check; see internal/update) ---
	// updateRes carries a newer release found by the off-thread probe; the
	// drain stores it in updateRel and auto-opens the What's New modal once.
	// updateChecked fires the probe EXACTLY ONCE on the first frame (after the
	// window is up) so the check never touches the boot critical path.
	updateRes       chan *update.Release
	updateChecked   bool
	updateRel       *update.Release
	updateShow      bool
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
	// Highlight-colour picker (Settings): the hue/sat wheel texture (built once)
	// and the in-progress hex field text.
	colorWheel *sdl.Texture
	colorHex   string
	// Mayo mascot portrait on the About page: a high-quality Catmull-Rom downscale
	// of the embedded art, uploaded once (lazily, render thread). Baked to the exact
	// PHYSICAL pixel size (logical × UI scale) so it draws 1:1 — crisp at any scale —
	// and rebuilt only when the UI scale changes. mayoTexFailed latches a failed
	// decode/upload so it isn't retried every frame. Zero per-frame cost. (mascot.go)
	mayoTex            *sdl.Texture
	mayoLogW, mayoLogH int32 // on-screen (logical) size; the texture itself is physical px
	mayoTexScale       int   // UIScale() the texture was baked at
	mayoTexFailed      bool

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
	gifResultCh  chan string // off-thread encode → UI (result line)

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
	layoutEdit bool
	editKey    string
	editDrag   int // 0 none, 1 move, 2 resize
	editPrev   bool
	editStart  [2]int32
	editBase   theme.Rect
	layoutSnap bool // snap edits to a design-space grid (toggle in the editor)
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
	classicEdit      bool
	classicOv        map[string][4]float64
	classicOvLoaded  bool
	slotReg          map[string]slotInfo
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
	// Close-on-leave travel state: previewTriggerRect is the cell that opened the box,
	// previewEntered latches once the cursor has reached the box. Together they let the
	// cursor cross the gap from the cell to the bottom-right box without it vanishing,
	// then close it the moment the cursor leaves the box (beta feedback).
	previewTriggerRect sdl.Rect
	previewEntered     bool
	hidden             map[string]bool

	iniRes chan iniswapFetch

	// layout scales (percent; mirrors prefs, saved on change)
	vpPct, chatPct, boxPct, logPct, inputPct int
	musicPct                                 int // music-list font scale, independent of the log so long titles can shrink without shrinking IC
	uiScalePct                               int // global renderer scale (manual)
	// detectedScalePct is the display-DPI-derived scale (96 dpi = 100%),
	// snapped to the settings step; UIScale() prefers it while the
	// auto-HiDPI preference is on. 0 = detection unavailable.
	detectedScalePct int
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
	showModDash        bool // the mod (ban/kick) dashboard panel is open
	showCMPanel        bool // the separate CM (area control) panel is open
	amICMNow           bool // cached amICM() — refreshed on ARUP/PU events, read per-frame by the
	// corner badge (0-alloc): the CM column lives in AreaInfo (ARUP), so a roster-stamp memo would
	// miss a /cm that doesn't change the roster — we recompute on the area/player events instead.
	modDashTargetUID string // selected target's UID ("" = none) — keyed by UID, never a roster
	// index: rebuildLiveRoster replaces the slice on every join/leave, so an index would repoint
	// a ban at whoever shifted into that slot.
	// Ban/Kick box (#130): a FROZEN snapshot of the target, taken when the box opens, so a roster
	// rebuild while the reason is being typed can never repoint a destructive command at someone
	// else. Only the IPID is allowed to fill in later (re-resolved by the frozen UID — same person).
	banBoxKind     int                   // 0 = closed, 1 = ban box, 2 = kick box
	banBoxUID      string                // snapshot: target UID (the identity anchor)
	banBoxIPID     string                // snapshot: target IPID (mod-only; "" until a /getarea enrich)
	banBoxName     string                // snapshot: display name for the box header
	banBoxDur      courtroom.BanDuration // chosen duration (ban only)
	banBoxReason   string                // typed reason
	modDashScroll  int32                 // mod dashboard roster scroll offset
	cmRosterScroll int32                 // CM panel roster scroll offset
	serverName     string
	serverKey      string // ws URL: keys the per-server warm state in prefs
	connErr        string
	lastConnName   string // M2: the server we were dropped from, for one-click Reconnect
	lastConnURL    string // its ws URL (serverKey), captured before Disconnect clears it
	// M2 auto-reconnect: after an unexpected drop, retry lastConnURL with backoff.
	// autoReconnectAt is the next attempt (zero = not retrying); pollAutoReconnect
	// fires it from the frame loop (a single time compare when idle — 0 per-frame
	// cost). autoReconnectMsg is the cached lobby status (rebuilt per attempt only).
	autoReconnectAt    time.Time
	autoReconnectTries int
	autoReconnectMsg   string
	connAt             time.Time // session start (Rich Presence elapsed timer)
	curArea            string    // last area WE clicked (Rich Presence, best-effort)
	presenceInit       bool      // false until the first lobby presence push (so "Playing AsyncAO" shows on launch, not only in-court)
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
	icInput  string
	oocInput string
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
	icExtColor  int
	icImmediate bool  // MS Immediate: preanim plays without holding the text (session toggle)
	icEffect    uint8 // #M5 sticky Text FX (courtroom.TextEffect*); 0 = off. Wraps every message you send.
	// pair placement (session-scoped: each tab keeps its own, seeded from prefs in
	// resetSessionState, so it can't leak across tabs like the App-global version
	// did). pairOffXText/Y are the typed edit buffers (commit on valid parse).
	pairOffX, pairOffY         int
	pairFlip                   bool
	pairOffXText, pairOffYText string
	emotes                     []courtroom.Emote
	emoteIdx                   int
	emotePage                  int // emote grid paging (classic + themed)
	emotePerPage               int // emotes per page last frame (number-key select)
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
	logSearch    string
	oocSeq       uint64
	oocLog       []string
	oocSpeakers  []string // parallel to oocLog: speaker per line ("" = system line); for name colours
	oocScroll    int32
	musicScroll  int32
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
	sfxMuted           bool
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
	// Player-list profile card popover (#101): which profile to show + its title.
	profileCardShow       bool
	profileCardPr         config.ProfilePref
	profileCardName       string
	liveDetailsArea       string    // area of the last auto /getarea pull; re-pull on area change
	lastRosterFetch       time.Time // debounce for the join/leave re-pull (rosterRefetchDebounce)
	suppressAreaEchoUntil time.Time // keep /gas/getarea reply lines out of OOC until this time — the WHOLE reply burst (a multi-area /gas spans several messages), not just the first
	rosterCmdUnsupported  bool      // this server rejected /gas ("unknown command") — stop sending it (the live PR/PU roster still works without it)
	// Follow-a-player (M3): followUID is the player we trail across areas ("" =
	// off); we auto-jump to their area on each PR/PU update, debounced.
	followUID      string
	lastFollowJump time.Time
	followShow     bool // FollowEnabled pref, read once per player-list frame (no per-row lock)
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
}

type lobbyFetch struct {
	entries []network.ServerEntry
	err     error
}

type iniswapFetch struct {
	key   string // serverKey the fetch was made for (tab-switch guard)
	names []string
	err   error
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
		fontRes:         make(chan fontLoad, 1),
		emojiFontRes:    make(chan []byte, 1),
		fallbackFontRes: make(chan [][]byte, 1),
		cjkFontRes:      make(chan [][]byte, 1),
		notebookRes:     make(chan notebookLoad, 1),
		jukeRes:         make(chan *config.Jukebox, 1),
		jukeIORes:       make(chan string, 4),
		jukeOpen:        -1,
		jukeDelPlaylist: -1,
		oocName:         d.Prefs.SavedShowname(),
		selServer:       -1,
		activeTab:       -1,
		tabDragFrom:     -1,
		macroBind:       -1,
		themeTex:        map[string]bool{},
		themePages:      map[string]*render.TexturePage{},
		hidden:          map[string]bool{},
	}
	a.resetSessionState()
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
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = d.Prefs.LayoutScales() // pair placement is seeded per-session in resetSessionState (above)
	a.musicPct = a.logPct                                                       // starts matching the log; ctrl+wheel over the Music tab tunes it apart
	a.playerPct = a.logPct                                                      // same for the Players tab + pair popup
	a.uiScalePct = d.Prefs.UIScale()
	ctx.SetUIScale(a.uiScalePct)
	a.applyFontConfig()                                      // dyslexia toggle or manual font path, resolved once
	a.dndOn = d.Prefs.DNDPersistOn() && d.Prefs.DNDSavedOn() // else session-only: clears each launch
	if saved := d.Prefs.SavedOOCName(); saved != "" {
		a.oocName = saved
	}
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

// SetDetectedUIScale feeds the display-DPI scale measured by main after
// SDL init (96 dpi = 100%), snapped to the settings step so auto and
// manual values share one scale, clamped to the manual bounds.
func (a *App) SetDetectedUIScale(pct int) {
	pct = pct / config.UIScaleStepPercent * config.UIScaleStepPercent
	a.detectedScalePct = clampInt(pct, config.MinUIScalePercent, config.MaxUIScalePercent)
	a.ctx.SetUIScale(a.UIScale()) // mouse unprojection follows immediately
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
	if a.room != nil {
		a.room.Update(dt)
	}
	a.d.Audio.Frame()
	a.d.Pump.Frame()
	// AFTER the upload pump, so the touch wins recency over THIS tick's upload
	// burst — exactly what a drawn frame does (the emote row touches these after
	// Pump.Frame too). Touching before the pump let the burst supersede it.
	a.keepActiveAssetsWarm()
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
	if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
		a.warmBase(a.urls.EmoteButton(me, a.emoteIdx+1, true), assets.AssetTypeEmoteButton)  // selected "on" image
		a.warmBase(a.urls.EmoteButton(me, a.emoteIdx+1, false), assets.AssetTypeEmoteButton) // "off" fallback
	}
	a.warmBase(a.urls.CharIcon(me), assets.AssetTypeCharIcon)
}

// warmBase keeps base resident across a minimized upload burst: touch it (→
// most-recent, the LAST to evict) if it's in T1, else re-demand it — once it has
// evicted, a Get alone can't bring it back, so the asset would stay gone until
// the post-restore render heals it.
func (a *App) warmBase(base string, t assets.AssetType) {
	if base == "" {
		return
	}
	if a.d.Store.Contains(base) {
		a.d.Store.Get(base)
		return
	}
	a.d.Manager.Prefetch(base, t, network.PriorityHigh)
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

// --- connection lifecycle -------------------------------------------------------

// Connect dials a server in a NEW tab. Whatever was active parks and
// keeps running in the background (rehearsal disconnects instead — it
// can't background). At the tab cap the connect refuses with a visible
// reason and the current session stays untouched.
func (a *App) Connect(name, wsURL string) {
	a.cancelAutoReconnect() // a deliberate Join/Reconnect takes over from any pending auto-retry
	a.connectWith(name, wsURL, context.Background())
}

// connectWith is Connect with a caller-chosen dial context. Manual joins pass
// context.Background() (Dial's full 10s budget); restore-on-launch passes a
// short timeout so a dead remembered server can't freeze boot for long.
func (a *App) connectWith(name, wsURL string, dialCtx context.Context) {
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
	conn, err := protocol.Dial(dialCtx, wsURL)
	if err != nil {
		a.connErr = err.Error()
		a.closeActiveTab()
		a.screen = ScreenLobby
		return
	}
	a.conn = conn
	a.lastPing = time.Now()
	a.sess = courtroom.NewSession(func(p protocol.Packet) error {
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

// hdid derives a stable hardware-ish ID (AO servers key bans on it).
func hdid() string {
	host, err := hostName()
	if err != nil {
		host = "asyncao"
	}
	return fmt.Sprintf("asyncao-%x", stringHash(host))
}

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
	for {
		select {
		case p, ok := <-a.conn.Incoming():
			if !ok {
				reason := "connection closed"
				if err := a.conn.Err(); err != nil {
					reason = err.Error()
				}
				a.connErr = reason
				a.pushDebug("disconnected: " + reason)
				a.Disconnect()
				return
			}
			a.lastPktHdr, a.lastPktAt = p.Header, time.Now()
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
			a.pushOOC(ev.Name+": "+ev.Text, ev.Name)
			if !looksLikeAreaList(ev.Text) { // your own /ga roster lists your name — don't self-ping
				a.checkCallwords(ev.Text)
			}
			a.scanModActionOOC(ev.Name, ev.Text) // #60: optional ban/kick/mute feedback sound
		case courtroom.EventMessage:
			if ev.Message != nil {
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
				if sn := ev.Message.SFXName; sn != "" && sn != "0" && sn != "1" {
					a.lastSFXName = sn // M11: remember the most-recent SFX for one-click "Mute last SFX"
				}
				if cn := ev.Message.CharName; cn != "" {
					a.lastBlipChar = cn // M11: remember the most-recent speaker for one-click per-char blip volume
				}
				if fr {
					a.signalFriend(a.serverName, ev.Message)
				}
				a.logDetailed(a.serverName, a.curArea, ev.Message) // detailed transcript (opt-in)
				a.noteEvidencePresented(ev.Message)
				a.checkCallwords(ev.Message.Message)
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
		case courtroom.EventModcall:
			a.pushOOC("[MOD CALL] "+ev.Text, "")
			a.playThemeSFX("mod_call")
			a.ctx.FlashWindow()
			a.signalModcall(a.serverName, ev.Text) // desktop toast (opt-in)
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
			a.scheduleAutoReconnect() // M2: unexpected drop → auto-retry this server (Disconnect just cancelled it)
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
		a.urls = courtroom.NewURLBuilder(local.BaseURL())
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
	a.urls = courtroom.NewURLBuilder(origin)
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
func (a *App) fetchManifestAsync() {
	origin := a.urls.Origin()
	if !a.d.Prefs.FormatAutoDetect() || a.manifestFor == origin ||
		!strings.HasPrefix(origin, "http") {
		return
	}
	a.manifestFor = origin
	host := hostOfURL(origin)
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
		a.rasterText = "" // re-raster the visible message in the new font
	default: // manual path or built-in — loadFontChainAsync clears on empty
		a.loadFontChainAsync(a.d.Prefs.FontPaths())
	}
}

// loadFontChainAsync reads the override font files off-thread (semicolon-
// or comma-separated paths, chain order, ≤ fontChainCap) and lands them on
// fontRes. An empty list clears the override immediately.
func (a *App) loadFontChainAsync(raw string) {
	paths := strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ',' })
	if len(paths) == 0 {
		a.ctx.SetFontChain(nil, nil)
		a.rasterText = "" // re-raster the visible message with the embedded font
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
	a.room.InlineEmote = inlineEmoteFor                                                   // #18: expand :shortcode: emotes in the chatbox (registry lives in ui)
	// Per-server audio: apply THIS server's volume profile (or the global one) now,
	// so the music re-seeded below plays at the right level and switching between
	// two in-court tabs carries each server's own volumes / muted blips.
	a.applyAudioVolumes()
	urls := a.urls
	a.room.Predictor = assets.NewPrefetcher(a.d.Manager, func(character, emote string) string {
		if emote == "" {
			emote = "normal" // no chain signal yet: the default loop
		}
		return urls.Emote(character, emote, courtroom.EmoteIdle)
	})
	a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
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
	a.room.CatchUp, a.room.CatchUpThreshold = a.d.Prefs.CatchUp()
	a.room.ReduceMotion = a.d.Prefs.ReduceMotion()
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
	key := a.serverKey
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		data, err := a.d.Manager.FetchRaw(ctx, url)
		if err != nil {
			a.iniRes <- iniswapFetch{key: key, err: err}
			return
		}
		a.iniRes <- iniswapFetch{key: key, names: parseIniswapList(data)}
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
			a.iniServer = res.names
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
	a.d.Manager.PrefetchWithFallback(a.previewBase, a.urls.EmoteBare(a.previewChar, anim), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview cycle)
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
			return
		}
		ini, err := courtroom.ParseCharINI(data)
		a.charINIres <- charINIFetch{key: key, ini: ini, err: err}
	}()
}

func (a *App) pollCharINI() {
	select {
	case res := <-a.charINIres:
		if res.key != a.serverKey {
			return // landed after a tab switch: another server's char.ini
		}
		a.charINIBusy = false
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
				bare := a.urls.EmoteBare(me, e.Anim)
				a.d.Manager.PrefetchWithFallback(a.urls.Emote(me, e.Anim, courtroom.EmoteIdle), bare, assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
				a.d.Manager.PrefetchWithFallback(a.urls.Emote(me, e.Anim, courtroom.EmoteTalk), bare, assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
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
	a.recordFrameDt(float32(dt.Seconds() * 1000))
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
	// F11 toggles fullscreen on any screen — the keyboard escape when a too-big
	// window has dragged the Settings controls off the edge of the monitor.
	if a.ctx.fullscreenReq {
		a.toggleFullscreen()
	}
	// Log-selection press edge, computed once so both logs (which may both be
	// on screen) read the same value — whichever draws first can't steal it.
	a.logSelPressed = a.ctx.mouseDown && !a.logSelPrevDown
	a.logSelPrevDown = a.ctx.mouseDown
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
	a.handleTabBar(winW) // chip clicks resolve BEFORE screens see them
	a.drainWarnings()
	a.pollThemeApply()
	a.pollManifest()
	a.pollFontChain()
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
	a.pollStylePresetBind() // #126
	a.pollAutoReconnect()   // M2: due auto-retry fires from the lobby; a single time-compare otherwise
	a.pollTimer()           // #97 local alarm: one compare while running, zero cost when idle
	a.pollDownload()
	a.pollMakerExport() // M16: deliver the self-contained archive export result
	a.pollGifExport()   // M16: deliver the off-thread GIF encode result
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
		a.room.Update(dt)
		a.applySpriteOverrides()
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.SetPostFX(a.postFX())                                                             // #10 retro overlays
		a.d.Viewport.SetWeather(render.Weather(a.d.Prefs.WeatherType()), a.d.Prefs.WeatherIntensity()) // #124 ambient weather
		a.d.Viewport.Update(&a.room.Scene, dt)
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
	a.d.Store.DrainDestroyQueue()

	// Sprite-preview wheel zoom + drag, claimed before any screen draws so it
	// wins the wheel/press over the grid scroll and icon clicks under the box.
	a.handlePreviewInput()

	// While a confirm modal (Disconnect / hide-sprite) is up, the modal OWNS the
	// pointer: fence it so the screen + overlays behind draw click-proof (no
	// fat-finger underneath). Restored just before the modal draws, below.
	if a.confirmDisconnect || a.hidePrompt != "" {
		a.ctx.fencePointer()
	}

	// #M2 S1: set/RELEASE the emoji-picker modal fence before any screen draws. modalOn
	// persists across frames, so an un-released fence freezes the whole UI (the reported
	// open-then-close bug).
	a.emojiPickerFence(a.ctx)
	a.reactPickerFence(a.ctx) // #2: same modal-fence discipline as the emoji picker

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
			a.drawCourtroom(winW, winH)
			a.drawFloatingExtras(winW, winH) // non-blocking, on top of the live courtroom (input already restored)
			if a.extrasSurfaceLive() {       // torn-off tab panels: live court, no modal, not editing (edit-mode draws them inside drawCourtroom)
				a.drawTornTabs(winW, winH) // interactive content, fenced by boxFencesPointer (torntabs.go)
			}
			a.drawEmojiPicker(winW, winH)  // #M2 S1: emoji picker overlay (modal-fenced in drawCourtroom)
			a.drawReactPalette(winW, winH) // #2: reaction palette overlay (modal-fenced)
		case ScreenSettings:
			a.drawSettings(winW, winH)
		case ScreenAbout:
			a.drawAbout(winW, winH)
		case ScreenServerHelp:
			a.drawServerHelp(winW, winH)
		}
	}
	// The tab strip floats over every screen (input was consumed at the
	// top of the frame; this is just paint).
	a.drawTabBar(winW)
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
		a.drawHotkeyCheatSheet(winW, winH)
	}
	// M13: a found update shows a persistent chip (reopen) and, the first time,
	// the What's New patch-notes modal. Both no-op when no update was found.
	a.drawUpdateAvailable(winW, winH)
	// Confirm modals: restore the pointer (fenced above) for the modal's own
	// buttons, then paint it over everything. One at a time.
	if a.confirmDisconnect || a.hidePrompt != "" {
		a.ctx.unfencePointer()
		if a.confirmDisconnect {
			a.drawDisconnectConfirm(winW, winH)
		} else {
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
// pages cost one len check, animated ones loop on the theme clock.
func (a *App) themeFrame(page *render.TexturePage) *sdl.Texture {
	if len(page.Frames) == 1 {
		return page.Frames[0]
	}
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
	if sc.BackgroundBase != "" && !(sc.BackgroundBase == a.bgAskBase && now.Sub(a.bgAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.BackgroundBase) {
		a.bgAskBase, a.bgAskAt = sc.BackgroundBase, now
		a.d.Manager.Prefetch(sc.BackgroundBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	}
	if sc.DeskBase != "" && !(sc.DeskBase == a.deskAskBase && now.Sub(a.deskAskAt) < charIconRetryInterval) && !a.d.Store.Contains(sc.DeskBase) {
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
	*askBase, *askAt = layer.Active, now
	a.d.Manager.PrefetchWithFallback(layer.Active, bareSpriteBase(layer.Active), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (heal evicted live sprite)
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
func (a *App) logDetailed(server, area string, m *protocol.ChatMessage) {
	if m == nil || !a.d.Prefs.DetailedLogOn() {
		return
	}
	if a.translog == nil {
		path, err := transcriptPath()
		if err != nil {
			return
		}
		w, err := newTranscriptWriter(path)
		if err != nil {
			return
		}
		a.translog = w
	}
	a.translog.write(detailedLogLine(time.Now(), server, area, m))
}

// CloseTranscript flushes and closes the detailed-log writer at shutdown.
func (a *App) CloseTranscript() { a.translog.close() }

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

func stringHash(s string) uint64 {
	// FNV-1a, dependency-free (cache.Key would drag xxhash here for one id).
	const offset, prime = 14695981039346656037, 1099511628211
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func hostName() (string, error) {
	return osHostname()
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
