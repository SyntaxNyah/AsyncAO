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
	logLineMax          = 120
	// icLogCap sizes the IC scrollback (a casing session's worth; ~100 KiB
	// worst case — the log is now scrollable/searchable/exportable).
	icLogCap = 1024

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
	logTabOOC
	logTabNotes
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
	// click-to-expand selection: first click opens the full description
	// under the row, a second click on the same row joins.
	selServer  int
	descLines  []string
	descLinks  []string // clickable URLs found in the description
	helpScroll int32    // ScreenServerHelp scroll position

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
	// macroBind ≥ 0 while the settings macro editor captures a key.
	macroBind int
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
	icWrapGen   int // font chain generation baked into the wrap
	// OOC wrapped-lines cache: long entries (MOTDs) wrap instead of
	// truncating; rebuilt only when the log, width, or font scale moved.
	// (Like the IC caches above, these stay App-global: their keys carry
	// the per-tab seq, so a tab switch is just a cache miss.)
	oocWrap     []string
	oocWrapSeq  uint64
	oocWrapW    int32
	oocWrapPct  int
	oocWrapMask bool // streamer-mode masking baked into the cache
	oocWrapGen  int  // font chain generation baked into the wrap

	// last missing-asset warning surfaced to the user (spec §4).
	warnLine string
	warnAt   time.Time

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

	// lastOSToast rate-limits friend desktop toasts (osToastMinInterval).
	lastOSToast time.Time

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
	// Layout-editor undo/redo: each edit snapshots the whole rect map (≈40
	// entries) before changing it; Ctrl+Z restores, Ctrl+Y redoes. Bounded.
	editUndo []map[string]theme.Rect
	editRedo []map[string]theme.Rect
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
	hidden    map[string]bool

	iniRes chan iniswapFetch

	// layout scales (percent; mirrors prefs, saved on change)
	vpPct, chatPct, boxPct, logPct, inputPct int
	uiScalePct                               int // global renderer scale (manual)
	// detectedScalePct is the display-DPI-derived scale (96 dpi = 100%),
	// snapped to the settings step; UIScale() prefers it while the
	// auto-HiDPI preference is on. 0 = detection unavailable.
	detectedScalePct int
	// theaterOn is the borderless viewport-only mode (Esc exits).
	// Deliberately session-only: it can never persist someone into a
	// chrome-less client across runs.
	theaterOn bool

	// pair placement mirrors prefs (persisted globally — your preferred
	// offsets follow you across servers like AO2's sliders).
	pairOffX int
	pairOffY int
	pairFlip bool

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
	conn        *protocol.Conn
	sess        *courtroom.Session
	room        *courtroom.Courtroom
	urls        courtroom.URLBuilder
	serverName  string
	serverKey   string // ws URL: keys the per-server warm state in prefs
	connErr     string
	connAt      time.Time // session start (Rich Presence elapsed timer)
	curArea     string    // last area WE clicked (Rich Presence, best-effort)
	lastPing    time.Time // CH keepalive pacing (active + background)
	lastICSend  time.Time // chat_ratelimit window
	manifestFor string    // origin already fetched this session (dedupe)
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

	// --- courtroom chrome ---
	icInput  string
	oocInput string
	// shownameOverride is the in-courtroom showname box: when non-blank
	// it wins over the persisted Settings showname for outgoing messages
	// (session-scoped — it never overwrites the saved one).
	shownameOverride string
	sidePref         string // OUR side (char.ini default, /pos override)
	iniWarmed        string // last char.ini hover-warmed (dedupe)
	icColor          int    // outgoing MS text_color (dropdown)
	// pair offset edit buffers (typed text commits on valid parse)
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
	oocScroll    int32
	musicScroll  int32
	areaScroll   int32
	logTab       int
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
	charKeys    map[string]string // key name → character
	charKeysRev map[string]string // character → key name (badges)

	// ghostWarm dedupes the pair-panel ghost editor's sprite prefetches.
	ghostWarm map[string]string

	// --- OOC automation (login flows + macros share the send pipeline) ---
	oocQueue  []oocSend // paced OOC sends (≤ macroQueueCap)
	showLogin bool
	loginUser string
	loginPass string
	loginAuto bool

	// --- viewport camera (hyperfocus zoom; 0 or 1 = off) ---
	vpZoom    float64
	vpPanX    float64 // pan fractions of the zoom overflow (0..1)
	vpPanY    float64
	zoomDrag  bool
	zoomPrev  bool // last frame's mouseDown (edge detect)
	zoomStart [2]int32
	zoomBase  [2]float64

	// --- court extras (HP / WTCE / modcall / evidence) ---
	wtceName    string    // active splash stem ("" = none)
	wtceAt      time.Time // splash start (frame stepping + expiry)
	testimonyOn bool      // persistent "Testimony" recording badge (RT 2.9)
	hpPrev      [2]int    // last drawn HP per bar — penalty sfx direction
	showModcall bool      // modcall reason dialog
	modReason   string
	showEvid    bool // evidence panel
	evidIdx     int  // selected evidence (-1 = none)
	evidPresent bool // armed: next IC message carries the selection
	evidEditing bool // editor open (add when evidIdx == -1)
	evidName    string
	evidDesc    string
	evidImage   string
	evidScroll  int32
	evidAsk     []time.Time // thumbnail demand pacing, parallel to Evidence
	// evShow is the incoming presented-evidence pop-up.
	evShowImg string
	evShowAt  time.Time

	// --- wardrobe / iniswap (client favourites + server iniswap.txt) ---
	iniChar      string   // active override folder ("" = picked character)
	pendingIni   string   // wear this once PV confirms (char-select joins)
	iniServer    []string // the server's iniswap.txt names (may be empty)
	iniList      []string // merged menu: wardrobe first, then server extras
	iniWardrobe  []bool   // parallel to iniList: wardrobe membership (star)
	iniLower     []string // lowercased names for the search filter
	iniFolders   []string // parallel to iniList: each entry's folder ("" = unsorted)
	iniListErr   string
	iniBusy      bool
	showIni      bool
	iniSearch    string
	iniAdd       string   // "add folder to wardrobe" input
	iniFolder    string   // open wardrobe folder ("" = top level/root, else folder name)
	iniNewFold   string   // "new folder" text input
	iniMenuChar  string   // wardrobe char with an open "move to folder" menu ("" = none)
	iniMenuAt    [2]int32 // that menu's top-left (cursor at right-click)
	iniHoverChar string   // wardrobe char under the cursor this frame (number-key quick-file)
	// Drag-to-file (app-drawer style): drag a character cell onto a folder
	// chip to file it. iniDragChar is armed on press; iniDragging flips once
	// the move passes iniDragThreshold (and then suppresses the wear-click).
	iniDragChar  string
	iniDragStart [2]int32
	iniDragging  bool
	iniPrevDown  bool // mouse-held tracker for press detection in the modal
	iniPressed   bool // mouse went down this frame (computed per frame)
	iniScroll    int32
	iniAsk       []time.Time // demand pacing stamps, parallel to iniList

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
	sfxMuted    bool
	showHotkeys bool
	musicDucked bool

	// scenery self-heal stamps (healScenery pacing)
	bgAskBase   string
	bgAskAt     time.Time
	deskAskBase string
	deskAskAt   time.Time

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
		charINIres:      make(chan charINIFetch, 1),
		previewEmoteRes: make(chan previewEmoteFetch, 1),
		updateRes:       make(chan *update.Release, 1),
		updateApplyRes:  make(chan error, 1),
		iniRes:          make(chan iniswapFetch, 1),
		manifestRes:     make(chan manifestFetch, 1),
		fontRes:         make(chan fontLoad, 1),
		notebookRes:     make(chan notebookLoad, 1),
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
	a.applyThemeAsync() // chatbox skin + font colors from the saved theme
	a.pairOffX, a.pairOffY = d.Prefs.PairOffsets()
	a.pairFlip = d.Prefs.PairFlipped()
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = d.Prefs.LayoutScales()
	a.uiScalePct = d.Prefs.UIScale()
	ctx.SetUIScale(a.uiScalePct)
	if paths := d.Prefs.FontPaths(); paths != "" {
		a.loadFontChainAsync(paths) // persisted override: bytes land async
	}
	if saved := d.Prefs.SavedOOCName(); saved != "" {
		a.oocName = saved
	}
	a.RefreshServers()
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
	a.d.Store.DrainDestroyQueue()
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
	conn, err := protocol.Dial(context.Background(), wsURL)
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

// Disconnect tears the ACTIVE session down (its tab closes; other tabs
// keep running) and returns to the lobby.
func (a *App) Disconnect() {
	if a.conn != nil {
		a.conn.Close()
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
	if a.d.Viewport != nil {
		a.d.Viewport.OnPreanimDone = nil
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

// effectiveShowname is the name outgoing messages carry: the courtroom
// override box when filled, the persisted Settings showname otherwise.
func (a *App) effectiveShowname() string {
	if s := strings.TrimSpace(a.shownameOverride); s != "" {
		return s
	}
	return a.d.Prefs.SavedShowname()
}

// updatePresence pushes (or clears) the Discord activity from the user's
// per-field choices. Cheap — a channel swap; the presence worker owns all
// I/O and silently idles when Discord isn't running.
func (a *App) updatePresence() {
	if a.d.Presence == nil {
		return
	}
	dp := a.d.Prefs.Discord()
	if !dp.Enabled || a.sess == nil {
		a.d.Presence.Clear()
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
		case courtroom.EventCharsUpdated:
			a.charLower = nil // names may have changed; rebuild lazily
			// icons refresh lazily as textures land
		case courtroom.EventCharPicked:
			a.enterCourtroom()
		case courtroom.EventOOC:
			a.pushOOC(ev.Name + ": " + ev.Text)
			a.checkCallwords(ev.Text)
		case courtroom.EventMessage:
			if ev.Message != nil {
				fr, fc := a.friendMessage(a.serverKey, ev.Message)
				a.pushIC(icLogLine(ev.Message, a.d.Prefs.ForceCharNamesOn()), ev.Message.TextColor, fr, fc)
				if fr {
					a.signalFriend(a.serverName, ev.Message)
				}
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
			a.pushOOC("[MOD CALL] " + ev.Text)
			a.playThemeSFX("mod_call")
			a.ctx.FlashWindow()
		case courtroom.EventAuth:
			// AO2 surfaces auth transitions as CLIENT lines in the OOC log
			// (on_authentication_state_received).
			switch {
			case ev.Int >= 1:
				a.pushOOC("CLIENT: Logged in as a moderator.")
			case ev.Int == 0:
				a.pushOOC("CLIENT: Login unsuccessful.")
			default:
				a.pushOOC("CLIENT: You were logged out.")
			}
		case courtroom.EventSetPos:
			a.sidePref = ev.Text // SP: the server moved us
		case courtroom.EventCase:
			a.pushOOC("[CASE] " + ev.Text)
			if enabled, roles := a.d.Prefs.Casing(); enabled && ev.Int&roles != 0 {
				a.playThemeSFX("case_call")
				a.ctx.FlashWindow()
			}
		case courtroom.EventNotice:
			a.pushOOC("[SERVER] " + ev.Text)
			a.ctx.FlashWindow()
		case courtroom.EventEvidence:
			a.evidAsk = nil // list replaced; thumbnail pacing resets
			if a.evidIdx >= len(a.sess.Evidence) {
				a.evidIdx = -1
			}
		case courtroom.EventDisconnect:
			a.connErr = ev.Text
			a.pushDebug("disconnected: " + ev.Text)
			a.Disconnect()
			continue
		case courtroom.EventDebug:
			// Protocol-level diagnostics (unhandled headers, dropped MS):
			// the room has no use for these — debug overlay only.
			a.pushDebug("server: " + ev.Text)
			continue
		}
		if a.room != nil {
			a.room.HandleEvent(ev)
		}
	}
}

func icLogLine(m *protocol.ChatMessage, forceChar bool) string {
	name := m.Showname
	if forceChar || name == "" {
		name = m.CharName // force-char-names mirrors the chatbox (anti-impersonation)
	}
	// Strip inline markup so the log reads like the chatbox (no raw \cN / { }).
	return name + ": " + courtroom.StripChatMarkup(m.Message)
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
		if len(res.data) > 0 {
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
	// A wardrobe pick from char select rides in as pendingIni (the slot
	// was auto-claimed); a plain pick starts clean.
	a.iniChar = a.pendingIni
	a.pendingIni = ""
	a.sidePref = "" // until the new char.ini reports its side
	a.room = courtroom.NewCourtroom(a.urls, a.d.Manager, a.sess, a.d.Audio)
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
	a.updatePresence() // character (and server) just became known
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
	a.loadCharINI()
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
	names, fromWardrobe := mergeWardrobe(a.d.Prefs.WardrobeList(a.serverKey), a.iniServer)
	a.iniList = names
	a.iniWardrobe = fromWardrobe
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

// mergeWardrobe builds the wardrobe-first menu list; the bool slice marks
// wardrobe membership (the star). Server duplicates collapse into their
// wardrobe entry, case-insensitively.
func mergeWardrobe(wardrobe, server []string) ([]string, []bool) {
	names := make([]string, 0, len(wardrobe)+len(server))
	stars := make([]bool, 0, len(wardrobe)+len(server))
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
	}
	for _, n := range server {
		if _, dup := seen[strings.ToLower(n)]; dup {
			continue
		}
		names = append(names, n)
		stars = append(stars, false)
	}
	return names, stars
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
		a.emoteBtnOff, a.emoteBtnOn = nil, nil
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
		a.emoteIdx = 0
		a.emotePage = 0
		a.emoteAsk = nil // fresh char: re-demand its button art from scratch
		// Prefetch the first few emotes speculatively.
		me := a.myCharName()
		for i, e := range a.emotes {
			if i >= 8 {
				break
			}
			a.d.Manager.Prefetch(a.urls.Emote(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite
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
	network.SortServers(entries)
	return entries
}

// --- shared frame ------------------------------------------------------------------

// Frame runs one UI frame: connection pump, screen logic, drawing.
func (a *App) Frame(dt time.Duration, winW, winH int32) {
	a.frameNow = time.Now()
	a.recordFrameDt(float32(dt.Seconds() * 1000))
	// F3 toggles the perf HUD on any screen; consumed so plain-key
	// macro/character binds named "f3" can't double-fire.
	if a.ctx.keyPressed == sdl.K_F3 {
		a.perfHUD = !a.perfHUD
		a.ctx.keyPressed = 0
	}
	// F1 toggles the hotkey cheat-sheet on any screen (consumed so a plain-key
	// macro/char bind named "f1" can't double-fire).
	if a.ctx.keyPressed == sdl.K_F1 {
		a.showHotkeys = !a.showHotkeys
		a.ctx.keyPressed = 0
	}
	// Log-selection press edge, computed once so both logs (which may both be
	// on screen) read the same value — whichever draws first can't steal it.
	a.logSelPressed = a.ctx.mouseDown && !a.logSelPrevDown
	a.logSelPrevDown = a.ctx.mouseDown
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
	a.pollNotebook()
	a.pollCharBind()
	a.pollMacroBind()
	a.pollDownload()
	a.pollBgList() // drain bg discovery even when the picker is closed (slideshow)
	a.processOOCQueue()
	a.iconAskBudget = charIconAskPerFrame // shared demand budget (icons, emote buttons)
	if a.room != nil {
		a.healScenery()
		a.room.Update(dt)
		a.applySpriteOverrides()
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

	switch a.screen {
	case ScreenLobby:
		a.drawLobby(winW, winH)
	case ScreenCharSelect:
		a.drawCharSelect(winW, winH)
	case ScreenCourtroom:
		a.drawCourtroom(winW, winH)
	case ScreenSettings:
		a.drawSettings(winW, winH)
	case ScreenAbout:
		a.drawAbout(winW, winH)
	case ScreenServerHelp:
		a.drawServerHelp(winW, winH)
	}
	// The tab strip floats over every screen (input was consumed at the
	// top of the frame; this is just paint).
	a.drawTabBar(winW)
	// Download progress chip floats under the strip while a grab runs.
	a.drawDownloadIndicator(winW)
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
	// Deferred kit overlays (open dropdown lists) stack above everything.
	a.ctx.FinishFrame()
	// Hover hints paint last so they sit above every cell/overlay.
	a.ctx.drawTooltip(winW, winH)
}

// applyThemeAsync loads the selected theme's visible pieces off-thread —
// the chatbox skin (chatbox.webp/png in the theme dir, AO2 convention)
// and the message/showname font colors — and publishes them to the render
// thread via themeRes. Settings re-triggers it on every theme change.
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
		roots := make([]string, 0, 2)
		if root != "" {
			roots = append(roots, root)
		}
		if exe, err := os.Executable(); err == nil {
			roots = append(roots, filepath.Dir(exe))
		}
		t, err := theme.Load(name, roots)
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
	for {
		select {
		case w := <-a.d.Manager.Warnings():
			line := "Missing asset: " + w.Base
			if len(w.Tried) > 0 {
				line += " (tried " + strings.Join(w.Tried, " ") + " — see Settings → formats)"
			}
			a.warnLine = clampLine(line)
			a.warnAt = time.Now()
			a.pushDebug(line)
		default:
			return
		}
	}
}

// warnActive reports whether the warning banner should still draw.
func (a *App) warnActive() bool {
	return a.warnLine != "" && time.Since(a.warnAt) < warnShowDuration
}

// applySpriteOverrides lets the user's drag positions win over the
// message's offsets every frame (one map probe per visible layer; free
// while no overrides exist).
func (a *App) applySpriteOverrides() {
	if len(a.spriteOv) == 0 {
		return
	}
	sc := &a.room.Scene
	for _, layer := range [...]*courtroom.SpriteLayer{&sc.Speaker, &sc.Pair} {
		if !layer.Visible || layer.Name == "" {
			continue
		}
		if ov, ok := a.spriteOv[strings.ToLower(layer.Name)]; ok {
			layer.OffsetX, layer.OffsetY = ov[0], ov[1]
		}
	}
}

// healScenery re-demands the scene's background/desk when T1 lost them
// (LRU eviction, or a prefetch that never landed): without it the viewport
// can only show black until the next position change. Paced one ask per
// base per charIconRetryInterval; HIGH because this is the live scene.
func (a *App) healScenery() {
	sc := &a.room.Scene
	now := time.Now()
	if sc.BackgroundBase != "" && sc.BackgroundBase == a.bgAskBase && now.Sub(a.bgAskAt) < charIconRetryInterval {
		// recently asked for this exact base; let it land
	} else if sc.BackgroundBase != "" && !a.d.Store.Contains(sc.BackgroundBase) {
		a.bgAskBase, a.bgAskAt = sc.BackgroundBase, now
		a.d.Manager.Prefetch(sc.BackgroundBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	}
	if sc.DeskBase != "" && sc.DeskBase == a.deskAskBase && now.Sub(a.deskAskAt) < charIconRetryInterval {
		return
	}
	if sc.DeskBase != "" && !a.d.Store.Contains(sc.DeskBase) {
		a.deskAskBase, a.deskAskAt = sc.DeskBase, now
		a.d.Manager.Prefetch(sc.DeskBase, assets.AssetTypeDeskOverlay, network.PriorityHigh) // AssetType: DeskOverlay
	}
}

// icEntry is one IC log line with its AO text color preserved (rich
// scrollback: render, search, and export keep the color).
type icEntry struct {
	text        string
	color       int
	url         string // first http(s) link in the line ("" = none); makes the line clickable
	friend      bool   // sender is a highlighted friend (showname match) — glows in the log
	friendColor int32  // per-friend glow RGB (0xRRGGBB) from a `name=hex` entry; -1 = default friend tint
}

// friendMessage reports whether a message is from a highlighted friend on the
// given server — its showname, falling back to CharName (the displayName rule),
// matched case-insensitively — and the friend's per-entry glow colour (0xRRGGBB,
// or -1 for the default tint). Gated on the master toggle FIRST, so it's a cheap
// no-op when the feature is off (the default) — and the membership scan
// allocates nothing, so it's safe per message even in a catch-up burst.
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
// else the theme word_call). Streamer mode suppresses them (same as callwords).
// Called at the message seam when friendMessage is true (active OR background
// tab, so you're alerted even while looking at another server). The glow is
// drawn separately in the log.
func (a *App) signalFriend(serverName string, m *protocol.ChatMessage) {
	if a.d.Prefs.StreamerMode() {
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
		if f := a.d.Prefs.FriendSoundPath(); f != "" {
			a.d.Audio.PlayFile(f)
		} else {
			a.playThemeSFX("word_call")
		}
	}
	// Desktop (OS) toast — rate-limited so a chatty friend can't storm it.
	if a.d.Prefs.FriendOSToastOn() && time.Since(a.lastOSToast) >= osToastMinInterval {
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

func (a *App) pushIC(line string, color int, friend bool, friendColor int32) {
	url := ""
	if urls := extractURLs(line, 1); len(urls) > 0 {
		url = urls[0]
	}
	a.icLog = append(a.icLog, icEntry{text: clampLine(line), color: color, url: url, friend: friend, friendColor: friendColor})
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

func (a *App) pushOOC(line string) {
	if len(line) > oocLineCap {
		line = line[:oocLineCap] + "…"
	}
	a.oocLog = appendCapped(a.oocLog, line, icLogCap)
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
