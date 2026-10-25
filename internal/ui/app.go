package ui

import (
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/logbrowser"
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
	// ScreenThemeEditor is the theme editor: the live courtroom at full
	// window size with two floating rails over it (themeeditor.go). APPENDED, which
	// is safe because the enum is not persisted anywhere — no preference, no wire
	// field and no on-disk cache stores a Screen value.
	ScreenThemeEditor
)

const (
	lobbyRefreshTimeout = 15 * time.Second
	// lobbyAutoRefreshMinInterval caps the on-open lobby auto-refresh: entering
	// the lobby screen refetches the master list only if this long has passed
	// since the last fetch STARTED (every start — boot, manual Refresh, master-URL
	// change, auto — arms the stamp inside RefreshServers). One minute keeps the
	// list from going stale over a play session while bounding master-server load
	// from rapid menu/tab bouncing to at most one request a minute; past the cap
	// the refresh fires immediately on entry (edge-triggered, no polling timer).
	lobbyAutoRefreshMinInterval = time.Minute
	logLines                    = 8
	logLineMax                  = 120 // single-line UI (toasts/warnings) — NOT the IC log
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

	// warnShowDuration keeps a transient toast readable without turning the
	// HUD into a ticker.
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

	// restoreDialTimeout bounds each restore-on-launch reconnect so a dead
	// remembered server can't freeze boot (manual Join keeps Dial's full 10s).
	restoreDialTimeout = 4 * time.Second

	// maxDescLines caps the expanded lobby description box.
	maxDescLines = 6
	// spriteOvCap bounds the client-side sprite override table.
	spriteOvCap = 128

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
	// Preview is the detachable emote-preview OS window (
	// preview-window-wire). NEVER nil in production (main.go always
	// constructs one via render.NewPreviewWindow, closed by default — the
	// "closed = free" contract), but every consumer must stay nil-safe
	// (render.PreviewWindow's own methods all are) so headless tests that
	// never set this field keep working unmodified.
	Preview *render.PreviewWindow
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
	// AutoMounts are the auto-discovered packages/ folders (issue #128),
	// appended after the user's manual mounts. Nil in tests and when there is
	// no packages/ folder beside the executable.
	AutoMounts []string
}

// env is the minimal cross-cutting state every App component shares: engine
// deps (d), the per-frame render context (ctx), the one-line notice banner
// (warnLine/warnAt), and screen navigation (screen/prevScreen). One instance,
// created once in NewApp; App embeds env (a value, so a zero-value App stays
// nil-safe) and components share it via &a.env.
type env struct {
	d   Deps
	ctx *Ctx

	screen     Screen // navigation: current screen
	prevScreen Screen // navigation: back target (settings/about)

	warnLine string    // notice: one-line banner text ("" = none)
	warnAt   time.Time // notice: banner expiry (zero = hidden)

	// uiDirty marks work that changed UI-visible state since the last drawn
	// frame — the connection pumps set it per drained packet — so the
	// experimental event-driven loop renders exactly one frame in response
	// instead of waiting out its idle tick. App-level, NOT sessionState: the
	// flag is about the window, so it must survive a tab park/restore. Frame
	// clears it.
	uiDirty bool

	// wins holds the floating-window geometry (a leaf service). Window state is
	// global, so it lives in env and survives a tab park/restore.
	wins wins
	// keys is the key registry (a leaf service): one method, no state of its own.
	keys keys
	// debug is the bounded failure log the overlay draws. DELIBERATELY
	// CLIENT-GLOBAL, unlike the per-tab IC transcripts: most of what lands in it
	// has no tab at all (theme apply, extensions seeding, casing detection, the
	// updater), and triaging the client wants every tab's failures in one stream.
	// Memory-only (debugLogCap ring, never a file).
	debug debugSink

	// h is the narrow set of App capabilities a component needs beyond env's
	// own shared state. Set once in wireComponents; nil in a bare or
	// literal-built rig, which is why every accessor below is nil-safe.
	h host
}

// host is the deliberately tiny capability surface *App hands to components:
// reading the top chrome band (which positions top-of-window affordances such
// as the update chip). Anything bigger belongs in env as shared state, not in a
// component reaching back into App — the dirty flag already moved into env
// (uiDirty above, set by env.dirty), which is why this is one method, not two.
type host interface {
	chromeBandH() int32 // height of the menu bar + tab strip
}

// dirty marks UI-visible state changed since the last drawn frame. It is env
// state rather than a host call, so it works in a bare &App{} rig too and does
// not depend on the host being wired.
func (e *env) dirty() {
	e.uiDirty = true
}

// chromeTop is the top chrome band height. Nil-safe: 0 when no host is wired
// (a bare &App{} rig has no chrome to clear), which keeps the chip's layout
// arithmetic total instead of panicking.
func (e *env) chromeTop() int32 {
	if e.h == nil {
		return 0
	}
	return e.h.chromeBandH()
}

// chromeBandH is App's half of host: the real chrome height, so a component
// lays its top-of-window rect against the same band every screen uses.
func (a *App) chromeBandH() int32 { return a.topChromeH() }

// App is the whole client UI state machine. Render thread only.
type App struct {
	env

	// updates is the M13 self-update component (update_ui.go): the one-shot
	// check, the update chip and the What's New modal. A NAMED field, not an
	// embed, so its state is never promoted into App's namespace and every
	// access reads a.updates.<name> — component state is visible from the call
	// site (the pilot for the decomposition; more components follow).
	updates selfUpdate

	// elemBake is the theme element bake's own state (free elements: the parsed
	// sidecar, the bake generation, the two sort keys and the visible_when memo).
	// Lifted off App's flat namespace: it is read only by the themed element pass.
	elemBake elemBakeState
	conn     connState
	font     fontState
	skin     skinState
	budget   budgetState
	sel      selState
	// fxPool is the fixed clock pool plus the stateful-effect pool and the sidecar
	// refusal the chip quotes (element clocks / effects).
	fxPool themeFXState

	// --- the theme editor (themeeditor.go) --------------------------
	//
	// ONE POINTER, nil while the editor is shut, and that is the whole of its cost
	// when closed: no goroutine, no timer, no per-frame read from any courtroom draw
	// site. This is the "0 base = 0 cost" rule.
	te *themeEditor

	// --- connection lifecycle (lobby-global; deliberately NOT in sessionState) ---

	// --- the active server session (every per-server field) ---
	// sessionState is EMBEDDED so the whole session parks/restores as one
	// struct move when tabs switch — see tabs.go. Field names stay
	// promoted, so call sites read exactly as before tabs existed.
	sessionState

	// --- global per-frame budgets / transient UI not tied to a server ---

	// --- async result channels (App-global plumbing; payloads carry the
	// serverKey they were fetched for, and polls drop mismatches so a tab
	// switch can never land another server's data) ---
	charINIres chan charINIFetch
	// previewEmoteRes carries a previewed character's parsed emote list for
	// the try-before-wear cycle (parsed off-thread, key+char guarded).
	previewEmoteRes chan previewEmoteFetch
	// logViewEpoch identifies WHICH tab's session is currently promoted into
	// App, folded into every log-derived cache key below (filter, IC wrap, OOC
	// wrap, split wrap). The per-session log seqs (icLogSeq/oocSeq) both start at
	// 0 in every tab, so two tabs at the same seq would COLLIDE on the shared
	// App-global caches — a switch could serve tab A's wrapped/coloured lines under
	// tab B (the "logs cross over, colours wrong" bug). The epoch bumps on every
	// session swap (park/activate/reset), so a switch is always a guaranteed cache
	// MISS instead of a false hit; within a tab it is stable, so steady-frame draws
	// stay 0-alloc (the caches keep their per-frame purpose). Never key a log cache
	// by seq alone.
	logViewEpoch uint64
	// icLogFiltered cache: rebuilt only when the log or the query moved
	// (a 1024-line scan + slice alloc per frame otherwise).
	icFilter      []int
	icFilterSeq   uint64
	icFilterEpoch uint64
	icFilterQuery string
	// The filter cache also keys on the advanced filters (#129), so editing a
	// showname/pos filter rebuilds without touching the log.
	icFilterShowname string
	icFilterPos      string
	// icFilterColor is the colour filter baked into the filter cache key (#49);
	// editing the colour dropdown rebuilds without touching the log.
	icFilterColor int
	// IC wrapped-rows cache (playtest: log lines must break to the log's own
	// width): filtered entries wrap to the list width;
	// rebuilt only when the log, query, width, or font scale moved.
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
	// OOC wrapped-lines cache: long entries (MOTDs) wrap instead of
	// truncating; rebuilt only when the log, width, or font scale moved.
	// (App-global like the IC caches above, but keyed by logViewEpoch too so a
	// tab switch — where both tabs' oocSeq can be equal — is a guaranteed miss,
	// never a cross-tab false hit; see the logViewEpoch field doc.)
	oocWrap     []string
	oocWrapName []string // parallel to oocWrap: speaker to tint on each entry's FIRST display line ("" otherwise)
	oocWrapCont []bool   // parallel to oocWrap: true = a wrap continuation row (hanging indent); a paragraph's own newline starts a fresh row, not a continuation
	oocWrapSrc  []int    // parallel to oocWrap: the source oocLog entry index of each display line (mirrors icWrapLine.entry)
	// Link spans (#38): every URL in a paragraph is separately hoverable and
	// clickable, tinting only ITS characters instead of the whole message.
	// oocWrapLink is the flat table in row order; oocWrapLinkAt has len(rows)+1
	// entries so row r's spans are oocWrapLink[oocWrapLinkAt[r]:oocWrapLinkAt[r+1]]
	// (see oocRowLinks — an O(1), allocation-free lookup for the draw loop).
	oocWrapLink   []oocLinkSpan
	oocWrapLinkAt []int32
	oocWrapSeq    uint64
	oocWrapEpoch  uint64
	oocWrapW      int32
	oocWrapPct    int
	oocWrapMask   bool // streamer-mode masking baked into the cache
	oocWrapGen    int  // font chain generation baked into the wrap

	// --- GH #27: missing assets get their own chrome chip, not the shared toast
	// channel above (assetmisschip.go). The count is what the chip shows; the ring
	// is the burst suppressor; the label/tip are memoized so the chip's per-frame
	// draw allocates nothing.
	assetMissSeen   assetMissRing
	assetMissCount  int
	assetMissAt     time.Time
	assetMissLabel  string
	assetMissLabelN int // count the memoized label was built for
	assetMissTip    string

	// dndOn is Do Not Disturb: session-only (resets on launch by design, so it
	// can't silently kill callwords days later) — silences the personal-ping
	// alerts (callword + friend) while leaving duty signals (modcalls, case
	// alerts, server notices) through. Gated in checkCallwords + signalFriend.
	dndOn   bool
	loginAt time.Time // when a login flow last fired; a brief callword grace so the server's login replies don't self-ping

	// --- debug overlay (Settings toggle): bounded failure log ---
	//
	// The ring itself is env.debug (client-global, like the rationale below says);
	// these two are the newest server packet header for the health line.
	lastPktHdr string // newest server packet header (health line)
	lastPktAt  time.Time

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

	// --- local-pack layer: see mountlayer.go ---
	mountIndex     *assets.MountIndex // the live index (nil = none built); its own Mounts() is the identity, so no separate key can drift
	mountIndexing  bool               // a walk is in flight (single-flight latch)
	mountIndexRes  chan mountIndexResult
	mountIndexErrs []error // per-mount build failures, for the Settings status line

	// --- font override pipeline (file bytes read off-thread) ---

	// --- log browser (ScreenLogs): search saved transcripts across servers ---
	// Global (not per-session): the loader runs off-thread, lands on logBrowserRes.
	logBrowser    logBrowserState
	logBrowserRes chan logbrowser.Result

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
	// translogs are the detailed-transcript writers (opt-in), ONE PER TAB SESSION:
	// logs/<server>/<session>.log, opened lazily on that session's first logged
	// message. Keyed by (server, sessionState.logSession) — keyed by server ALONE,
	// two tabs on one server shared a writer and interleaved into a single file.
	translogs map[translogKey]*transcriptWriter
	// nextLogSession mints sessionState.logSession. Monotonic for the process, so an
	// id is never reused even after a tab closes — a reused id would let a new tab
	// adopt a dead tab's still-open writer and start appending to its file.
	nextLogSession int

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
	// --- free elements (themeelements.go / themeelementbake.go) -----
	// --- element clocks + effects (themeclock.go) -------------------
	// --- theme media (thememedia.go) --------------------------------
	// themeMediaKeys is the set of theme:// media and generator keys THIS client
	// uploaded — the declared set the swap prune reconciles against. It is a UI-side
	// mirror rather than a store query for the same reason themeTex is: the prune
	// needs to know what the OUTGOING theme declared, and the store only knows what
	// is resident.
	themeMediaKeys map[string]bool
	// themeMediaPlan is the applied theme's plan, kept so the inspector, the budget
	// bar and the F8 line can all quote the numbers the PLANNER used rather than
	// re-deriving them from the store (which cannot say why something is absent).
	themeMediaPlan themeMediaPlan
	// Char select's SEPARATE design geometry and canvas cache (#20,
	// charselectlayout.go). Deliberately not folded into themeRects/themeLay: those
	// twelve keys live in a different design space (char_select, commonly 714×668,
	// against the courtroom's 714×579) and everything in themeRects becomes an
	// editable box in the courtroom layout editor. charSelDesign is the resolved
	// design-space table, charSelLay its window-scaled cache.
	charSelDesign charSelectDesign
	charSelLay    charSelectLayoutCache
	// tabBarSeeded records that the active theme did NOT declare "asyncao_tabbar" and
	// seedTabBarDesignRect synthesized the entry (tabs.go). It is what lets
	// tabStripThemeParked tell a theme author's own placement — which must move the
	// strip — from a synthesized default nobody has touched, which must leave the strip
	// in the client chrome band. Written on the render thread, in pollThemeApply,
	// beside the geometry it describes.
	tabBarSeeded bool
	// themeResizeArmGen is the one-shot that arms the #35 auto-resize. It holds the
	// themeGen of the ONE apply allowed to move the window; 0 = disarmed.
	// It NAMES its apply rather than being a bare flag because the apply pipeline is
	// re-entered constantly by things that must never move the window — boot,
	// healTheme's eviction repair (fired from any theme-art draw, including the
	// Armed at the USER-INITIATED sites (the Settings dropdown, the < > cycle
	// arrows, a user-driven theme-folder scan landing) and once at boot when the
	// ThemeFit default move ran for this prefs file, consumed by
	themeResizeArmGen uint64

	// --- live layout editor (themed courtroom; overrides persist per theme) ---
	layoutEdit bool
	// editTgt is the themed editor's selection — the generalized "thing being
	// dragged" (edittarget.go). It replaces the bare design-key string this
	// field used to be: the space is now carried explicitly, so the editor can
	// select a FREE ELEMENT (spaceElement, identity = the sidecar index) through the
	// same field, the same nudge router and the same handle machinery. Nothing
	// constructs an element target — the wave ships no new UI — so this holds a
	// spaceDesign key or nothing, exactly as before.
	editTgt  editTarget
	editDrag int // 0 none, 1 move, 2 resize
	// editEdges is which edges a themed RESIZE drags (edgeL/R/T/B), the design-space
	// twin of classicEditEdges. Before the themed editor had one grip (the
	// bottom-right corner) and this mask was implicit — hard-coded as edgeR|edgeB at
	// the two sites that needed it. With all eight handles wired the gripped edges
	// have to be remembered from the press, because the drag arithmetic, the grid
	// snap and the magnet all key off them.
	editEdges    uint8
	editPrev     bool
	editStart    [2]int32
	editBase     theme.Rect
	layoutSnap   bool // snap edits to a design-space grid (toggle in the editor)
	layoutAspect bool // lock the viewport (stage) to 4:3 while resizing it in the editor
	// layoutMagnetOff backs the persistent "Magnet: on/off" banner chip (Phase 3):
	layoutMagnetOff bool
	// Overlap cycling: Tab steps through the stack of boxes under the cursor (smallest first) so a
	// big box hidden under a small one is still reachable. editPickSig is the current stack's
	// fingerprint — the index resets when the stack changes.
	editPickIdx int
	editPickSig string
	// Layout-editor undo/redo: each edit snapshots the whole rect map (≈40
	// entries) plus the server-tab strip's placed flag before changing it (see
	// layoutSnapshot for why the flag cannot be re-derived from the rects);
	// Ctrl+Z restores, Ctrl+Y redoes. Bounded.
	editUndo []layoutSnapshot
	editRedo []layoutSnapshot

	classic classicState
	// alignGuides/alignScratch are the editor's per-drag-frame alignment
	// results and its reusable other-slots buffer (classicalign.go). Editor
	// path only; reused so a long drag allocates nothing after warm-up.
	// alignGuides is ALSO reused by the live piece-to-piece magnet (magnet.go)
	alignGuides  []alignGuide
	alignScratch []sdl.Rect
	// panelMagnetRects is the live-magnet candidate buffer (magnet.go): every
	// OTHER open floating surface's rect, rebuilt once per frame at the top of
	// drawFloatingPanels ONLY while a drag is active (empty on settled frames).
	// Reused so a long drag allocates nothing; bounded by panelMagnetCap.
	panelMagnetRects  []sdl.Rect
	themeAlignScratch []sdl.Rect
	// layoutPresetName is the Settings name field for saving the current layout as a
	// named preset (#34, internal/ui/layoutpresets.go + the Theme settings section).
	layoutPresetName string
	// layoutProfileCursor is the editor banner "Profile" chip's cycle position: an
	// index into the sorted saved-profile names (Phase 3). -1 = none applied yet
	// this session. Clicking the chip advances it and applies that profile via the
	// existing applyProfile — the banner is a shortcut for Settings → Theme, not a
	// parallel mechanism. Session state (the chip re-derives from live names each
	// draw, so a deleted/renamed profile can't strand it).
	layoutProfileCursor int
	toolbox             toolboxState
	// App-wide menu bar (#14, menubar.go). menuOpen is the TITLE of the open
	// top-level menu and menuSub the LABEL of the open submenu row — strings, not
	// indices, so the &App{} zero value is "closed" (index 0 would mean "Servers is
	// open" on every freshly built App, test fixtures included) and so reordering
	// the model can't silently reinterpret which pane is up. menuFenceOn records
	// that WE set c.modalOn; because that flag is SHARED with four sibling popups,
	// the release also has to check that none of them is holding it (see
	// menuBarModalFence / otherModalFenceHeld).
	menuOpen    string
	menuSub     string
	menuFenceOn bool
	menuBar     menuBarLatch
	// themePages is the generation-keyed page cache for theme:// textures
	// (zero store locks while the generation is unchanged).
	themePages    map[string]*render.TexturePage
	themePagesGen uint64

	themedHintShown bool
	// Floating Extras box (floatbox.go): movable geometry. extrasPlaced=false
	// means "centered default until first dragged/seeded"; the rest tracks a
	// title-bar drag. Position + size persist via the classic slot slotExtras
	// (seedExtrasFromSlot on first open, persistExtrasSlot on gesture end).
	extrasBoxX, extrasBoxY     int32
	extrasPlaced               bool
	extrasDragging             bool
	extrasGrabDX, extrasGrabDY int32
	extrasPrevDown             bool
	// Torn-off widgets: each lives in its own small floating box you drag out
	// of the main grid, move freely, and close (which returns it to the grid).
	// Flags use bool+index so the &App{} zero value is "idle" (no ctor needed).
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
	// extrasScroll is the widget grid's scroll offset in px (#30). The grid is a
	// scroll container now — the 23 widgets no longer fit any sane box height, and
	// without one the cells drew straight out through the bottom edge.
	extrasScroll int32
	// Voice chat (Nyathena VS_* relay): showVoice = the floating panel's open state
	// (global); membership/mic state is per-session (sessionState.voiceJoined/MicOn).
	showVoice bool
	// voiceAudio is the live capture/playback engine — non-nil ONLY while joined to
	// a voice channel (opt-in); nil = presence-only / not in voice (voiceaudio.go).
	voiceAudio *voiceEngine
	// micTest is the Settings → Voice "Test microphone" tool (#84): an independent
	// capture (+ optional sidetone playback) device, non-nil only while a test runs.
	// micTestSidetone remembers the "hear myself" toggle.
	micTest         *micTester
	micTestSidetone bool
	// makerExportPack > 0: the "include your own files in this bundle?" dialog is
	// up, holding the scene the user asked to export until they answer.
	// makerExportPackOK is the one-shot consent it grants.
	makerExportPack   int
	makerExportScene  *sceneRecording
	makerExportName   string
	makerExportPackOK bool

	// showQuitConfirm: the "Quit AsyncAO?" dialog is up (Esc in the lobby, or a
	// quit hotkey). Skipped when QuitConfirmSkip ("don't ask again") is set.
	showQuitConfirm bool
	msgs            msgState
	overlays        overlayState
	palette         paletteState
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
	// Pinned-pane chat log: wrapped rows cached by (width, pinned log seq, pin
	// epoch) so the steady-state right-pane render is 0-alloc — rebuilt only when a
	// message lands or the pane resizes (the pinned "slow chat" rarely changes). The
	// splitPinEpoch bumps on every pin change so re-pinning a DIFFERENT tab whose
	// icLogSeq happens to equal the last pinned tab's can't serve stale rows (the
	// same seq-collision class as the main log caches — see logViewEpoch).
	splitPinEpoch  uint64
	splitWrapW     int32
	splitWrapSeq   uint64
	splitWrapEpoch uint64
	splitWrapRows  []splitLogRow
	// The pinned pane wraps in the LOG face, so its cache keys on that face's
	// identity too: the log zoom (logPct) and the override-chain generation each
	// change the metric its rows were broken at (#42, same rule as the main log
	// wrap caches).
	splitWrapPct                   int
	splitWrapGen                   int
	client                         clientState
	clientPanX, clientPanY         float64
	clientPanGrabX, clientPanGrabY int32
	clientPanBaseX, clientPanBaseY float64
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
	// emoteGridDrewPreview latches "the sprite-preview box was already painted this
	// frame", so drawSpritePreviewFallback can stand in for the emote grid's draw
	// site on the passes where the grid did not run (panel hidden, no themed
	// `emotes` rect, char.ini still loading) without ever compositing the box twice.
	emoteGridDrewPreview bool
	// This frame's overlay-fence answer for the box (#37, fenceSpritePreview). Reset
	// by handlePreviewInput at the top of every frame, so a pass that never publishes
	// can't replay a stale mark.
	previewFence previewFenceLatch
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
	previewPinned      bool // right-click pins the sprite-preview box open until you close it (its x)
	// previewWinFedBase is the previewBase whose CURRENT frame the detached
	// preview OS window (a.d.Preview) was last fed via a texture readback.
	previewWinFedBase string
	// previewWinFedPage is the *render.TexturePage feedDetachedPreviewFrame
	// last fed FROM, for the same base above. Tracked separately from the
	// base STRING so a T1 eviction+re-decode of the same base (a NEW
	// TexturePage pointer, the OLD one's textures already destroyed) is
	// noticed and re-fed immediately, rather than waiting for the animation's
	// frame index to happen to change — without this, a re-decode landing on
	// the same frame index the window was already showing would silently
	// keep displaying content read back from a page that no longer exists in
	// T1 (still valid memory — the preview owns its own uploaded copy — just
	// stale) until the index next moved.
	previewWinFedPage *render.TexturePage
	// previewWinFedFrame is the page-relative frame INDEX last fed for
	// previewWinFedBase/previewWinFedPage, animated pages only (always 0 for
	// a static/1-frame page, matching pageFrameLoop's own "no loop" answer).
	previewWinFedFrame int
	// previewWinWasOpen is syncPreviewWindow's OWN falling-edge latch — "was
	// the detached window open as of last frame" — used ONLY to detect the
	// single frame it just closed (by its own titlebar X or the pop-out
	// toggle) so that frame's position/size gets persisted once. It is NOT a
	// second copy of "is it detached": every query of that goes straight to
	// a.d.Preview.IsOpen() (previewIsDetached), so there is nothing here for
	// the pop-out button to disagree with if the window closes itself.
	previewWinWasOpen bool

	iniRes chan iniswapFetch

	// layout scales (percent; mirrors prefs, saved on change)
	vpPct, chatPct, boxPct, logPct, inputPct int
	oocPct                                   int // OOC log text size, independent of logPct (IC log)
	musicPct                                 int // music-list font scale, independent of the log so long titles can shrink without shrinking IC
	areaPct                                  int // areas-list font scale (Ctrl+wheel); mirrors musicPct — on App (global, survives tab switch), NOT sessionState, so it never resets on a session swap
	modDashPct                               int // mod/CM dashboard list font scale (Ctrl+wheel); mirrors musicPct — on App (global), independent of the log
	playerPct                                int // Players-tab/pair-popup text zoom (Ctrl+wheel); moved OUT of sessionState to mirror musicPct — session-swap resets silently discarded the zoom (playtest: the zoom did not take; the docked tab also needed its own case in the panel scale switch, screens.go)
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
	// lastDPIDisplayIndex is the display the DPI was last queried for (#77
	// Part B). NoteDisplayChanged re-seeds the DPI only when the window has
	// actually moved to a DIFFERENT display, so a window-move within one
	// monitor costs no syscall and no font rebuild. -1 = never queried.
	lastDPIDisplayIndex int
	// theaterOn is the borderless viewport-only mode (Esc exits).
	// Deliberately session-only: it can never persist someone into a
	// chrome-less client across runs.
	theaterOn bool

	jukebox jukeboxState
	docs    docsState
	// Settings: the content region (right of the sidebar) the section/row helpers
	// draw into. Set once per frame by drawSettings; the helpers rebase their
	// pad-relative layout onto formX so every box lands inside the content card.
	formX, formW int32

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

// NewApp builds the UI over deps.
func NewApp(ctx *Ctx, d Deps) *App {
	a := &App{
		env: env{
			d:      d,
			ctx:    ctx,
			screen: ScreenLobby,
		},
		updates: selfUpdate{
			res:      make(chan *update.Release, 1),
			applyRes: make(chan error, 1),
		},
		lastDPIDisplayIndex: -1, // #77 Part B: no DPI query yet (SeedDisplayDPIScale sets it)
		conn:                connState{lobby: lobbyState{lobbyResult: make(chan lobbyFetch, 1), selServer: -1, pingRes: make(chan pingResult, pingResBuf)}},
		charINIres:          make(chan charINIFetch, 1),
		previewEmoteRes:     make(chan previewEmoteFetch, 1),
		iniRes:              make(chan iniswapFetch, 1),
		manifestRes:         make(chan manifestFetch, 1),
		casingRes:           make(chan casingProbeResult, 1),
		mountIndexRes:       make(chan mountIndexResult, 1),
		font:                fontState{fontRes: make(chan fontLoad, 1), emojiFontRes: make(chan []byte, 1), fallbackFontRes: make(chan [][]byte, 1), cjkFontRes: make(chan [][]byte, 1), errorSpriteRes: make(chan errorSpriteLoad, 1)},
		logBrowserRes:       make(chan logbrowser.Result, 1),

		notebookRes:    make(chan notebookLoad, 1),
		jukebox:        jukeboxState{jukeRes: make(chan *config.Jukebox, 1), jukeDelPlaylist: -1, jukeOpen: -1, jukeIORes: make(chan string, 4)},
		activeTab:      -1,
		tabDragFrom:    -1,
		budget:         budgetState{macroBind: -1},
		skin:           skinState{themeTex: map[string]bool{}},
		themeMediaKeys: map[string]bool{},
		themePages:     map[string]*render.TexturePage{},
		classic:        classicState{hidden: map[string]bool{}},
	}
	a.wireComponents()
	a.resetSessionState()
	// Let the widget kit kick the colour-emoji face read. A dropdown row or a
	// lobby label can be the first emoji on screen, and a widget with no way to
	// ask for the face would box it forever while the chatbox got colour.
	ctx.emojiLoad = a.ensureEmojiFontLoad
	// Wake the render loop when a decode/audio payload delivers (experimental
	// event-driven loop): the pump uploads it on the very next pass instead of
	// waiting out an idle tick. A queued no-op event when the loop pref is off.
	if d.Manager != nil {
		d.Manager.SetDeliveryNotify(PushWake)
	}
	// Held-frame bridge (the black-flash fix): when the LRU must evict any
	// ON-SCREEN stage layer (background, desk, or the current speaker/pair
	// sprite) to fit a cap-sized incoming page, the store steals one frame into
	// its pinned tier and the viewport draws that instead of exposing the black
	// stage fill (render/textures.go). IsLiveStageBase widens the old
	// scenery-only gate to the drawn character bases too (the last black-flash
	// hole — a mid-display speaker eviction had no other fallback).
	if d.Store != nil {
		d.Store.SetLiveScenery(a.IsLiveStageBase)
	}
	// missingno: pin the embedded default placeholder ONCE (render thread), then
	// kick the off-thread custom-image load if the user chose one. Default first so
	// a failed custom load leaves a valid page in place (missingno.go).
	a.uploadEmbeddedMissingno()
	a.uploadEmbeddedSpeedlines() // #126 bundled zoom speedline fallback
	if p := d.Prefs.Load().ErrorSpriteFile; p != "" {
		a.applyErrorSprite(p)
	}
	a.classic.seedHiddenFromPrefs(&a.env) // hidden-chrome set + the A6 no-strand normalization
	// Global jukebox library loads off-thread (it can be large); it lands via
	// pollJukebox so the keybinds and wardrobe tab have it ready.
	go func() {
		if j, err := config.LoadJukebox(); err == nil {
			a.jukebox.jukeRes <- j
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
	// Restore-on-launch (opt-in, OFF by default): queue the remembered tabs;
	// Frame reconnects them one per frame. Capped to the live tab cap so we
	// never queue a connect that allocateTab would reject. Off ⇒ nil queue ⇒
	// pumpTabRestore is a single length check, so boot is byte-identical.
	if d.Prefs.Load().RestoreTabs {
		q := d.Prefs.Load().OpenTabList()
		if cap := d.Prefs.Load().TabCap(); len(q) > cap {
			q = q[:cap]
		}
		a.restoreQueue = q
	}
	// Auto-connect-on-launch (opt-in, OFF by default): if tab-restore didn't queue
	// anything, dial the last server on the first frames so you land straight on
	// your chosen server — even when no tab was open at shutdown. Tab-restore wins
	// if both are on (it reopens exactly what you had).
	if d.Prefs.Load().AutoConnectOnLaunch && len(a.restoreQueue) == 0 {
		if _, url := d.Prefs.Load().LastServer(); url != "" {
			a.dialogs.autoConnectPending = true
		}
	}
	a.applyChromePreset(d.Prefs.Load().ChromeTheme()) // #M3: apply the saved client chrome theme at launch
	a.refreshPartColors()                             // per-part layout tints: parse the saved hexes once
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
	} else if line := mutedChannelsBootNotice(a.audio.masterMuted, a.audio.musicMuted, a.audio.sfxMuted, a.audio.blipMuted); line != "" {
		// Silent-launch trap: the mute flags just seeded in resetSessionState
		// (above) are real and persisted, but the volume strip that shows a
		// muted channel (the red "muted" label) defaults HIDDEN
		// (VolStripOn OFF) — so a user who quit muted a week ago would get
		// total silence on this launch with no on-screen sign why. One line,
		// same corrupt-prefs mechanism, no new panel/modal/settings row.
		a.warnLine = line
		a.warnAt = time.Now()
	}
	// Warm the local-pack index at startup rather than waiting for the first
	// connect. The walk runs on a goroutine and takes real time on a large pack, so
	// kicking it here means it is usually already resident by the time the first
	// room's cold-load storm demands anything; starting it on connect would race
	// the character-icon prefetch and lose to the resident-texture short-circuit.
	a.applyMountLayer()
	return a
}

// UIScale exposes the global scale percent (main sets the renderer scale
// from it each frame and sizes the logical canvas accordingly): the
// DPI-detected value under auto-HiDPI, the manual setting otherwise.
func (a *App) UIScale() int {
	if a.detectedScalePct > 0 && a.d.Prefs.Load().UIScaleAutoOn {
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
	a.dpiScalePct = max(config.MinAutoUIScalePercent, pct)
}

// SeedDisplayDPIScale queries the OS DPI for the monitor the window is on and
// records the DPI-derived auto scale (#77 Part B), so a HiDPI monitor's DEFAULT
// physical size is right without the user finding the slider. It prefers the
// reliable platform query (Windows GetDpiForWindow — sdl.GetDisplayDPI reports a
// flat 96 under per-monitor-v2 awareness and left detection stuck at 100), and
// falls back to sdl.GetDisplayDPI for the window's display where the native path
// is unavailable. The result flows through SetDisplayDPIScale, so the never-
// below-100 floor and the window-size combination are unchanged.
// This only affects the auto path: an explicitly saved scale wins because the
// manual slider requires UIScaleAuto off, and UIScale() then ignores the DPI-
// derived value entirely (nothing here writes a preference — the seed is
func (a *App) SeedDisplayDPIScale() {
	win := a.ctx.win
	if win == nil {
		return
	}
	// Remember the display we sampled so NoteDisplayChanged can skip a
	// same-monitor window move (it isn't a DPI change).
	if idx, err := win.GetDisplayIndex(); err == nil {
		a.lastDPIDisplayIndex = idx
	}
	dpi, ok := queryWindowDPI(win)
	if !ok {
		// Cross-platform fallback: sdl.GetDisplayDPI for this window's display.
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

// NoteDisplayChanged re-seeds the DPI-derived auto scale after the window has
// moved to a DIFFERENT monitor (#77 Part B): dragging a window from a 100% to a
// 150% monitor should re-detect. Gated on the display index actually changing so
// a same-monitor move (WINDOWEVENT_MOVED fires per drag) costs no syscall and no
// font rebuild; SetAutoScaleFromWindow only re-applies when the effective value
// differs, and Part A's SetTextDevScale is a no-op on an unchanged scale, so a
// same-DPI move never repays a raster rebuild. Render thread; call from the
// WINDOWEVENT_MOVED / DISPLAY_CHANGED handler.
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

// NoteFocusLost is called from the event loop when the window loses input focus
// (WINDOWEVENT_FOCUS_LOST). It drops the focused text field so the caret stops
// blinking and typing is no longer captured by the IC/OOC box — clicking away to
// Discord (or anywhere) must not leave the client looking ready to type. The
// Discord-style auto-refocus (chatfocus.go) re-arms on the next in-app keystroke,
// so returning and typing works without a click.
func (a *App) NoteFocusLost() {
	if a.ctx == nil {
		return
	}
	a.ctx.focusID = ""
	a.ctx.selAnchor = -1
}

// SetAutoScaleFromWindow updates the auto UI scale from the current PHYSICAL
// window size (only while the auto-scale preference is on). The UI is fixed
// logical pixels, so on a large window it reads as tiny — scaling up with the
// window keeps it proportional (the "text too small on big monitors" reports,
// and why shrinking the window already looked right). It takes the larger of the
// DPI scale and the window-height scale, floors at 100 (never shrink), snaps to
// the step, and caps at the manual bounds.
func (a *App) SetAutoScaleFromWindow(winW, winH int32) {
	if !a.d.Prefs.Load().UIScaleAutoOn {
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
	if on {
		// Theater is stage-only: no toolbox draws there. Clear the pinned pieces
		// panel + the pin latch so an invisible-but-armed panel can't swallow a
		// click in theater — drawToolboxPieces self-gates on these, and its fence
		// (boxFencesPointer) reads the same flags, so both go silent together.
		a.toolbox.toolboxPieces, a.toolbox.toolboxPinned = false, false
	}
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

// saveLayout persists the courtroom layout knobs (debounced saver flushes).
func (a *App) saveLayout() {
	a.d.Prefs.SetLayoutScales(a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct)
	oocPct := a.oocPct
	a.d.Prefs.Update(func(sn *config.Settings) {
		sn.OOCScalePct = oocPct
	})

	// independent OOC log text size
}

// inputFieldH is the scaled IC/OOC input height.
func (a *App) inputFieldH() int32 {
	return fieldH * int32(a.inputPct) / DefaultScalePct
}

// ensureCharLower keeps the lowercase name cache in sync with the char
// list (shared by char select filtering and the pairing search).
func (a *App) ensureCharLower() {
	if a.link.sess == nil || len(a.charsel.charLower) == len(a.link.sess.Chars) {
		return
	}
	a.charsel.charLower = make([]string, len(a.link.sess.Chars))
	a.charsel.charHoverIDs = make([]string, len(a.link.sess.Chars))
	for i := range a.link.sess.Chars {
		a.charsel.charLower[i] = strings.ToLower(a.link.sess.Chars[i].Name)
		a.charsel.charHoverIDs[i] = charHoverIDPrefix + a.link.sess.Chars[i].Name
	}
}

// charHoverIDPrefix namespaces the char-select grid's HoverPreview ids. The kit keys
// dwell state by id across the whole frame, so a bare character name could collide
// with any other widget id built from the same name (the pair menu, the wardrobe).
const charHoverIDPrefix = "char:"

// charHoverID is char i's cached HoverPreview id. The out-of-range arm rebuilds the
// string rather than indexing past the cache: every live caller runs behind
// ensureCharLower, but this is a DRAW loop, and paying one allocation on a path that
// should be unreachable beats panicking the client if it ever is reached.
func (a *App) charHoverID(i int) string {
	if i >= 0 && i < len(a.charsel.charHoverIDs) {
		return a.charsel.charHoverIDs[i]
	}
	if a.link.sess == nil || i < 0 || i >= len(a.link.sess.Chars) {
		return charHoverIDPrefix
	}
	return charHoverIDPrefix + a.link.sess.Chars[i].Name
}

// charSelectTitle is the char-select heading, rebuilt only when the server name
// changes. The concat used to run every frame on a screen that is otherwise pure
// integer layout.
func (a *App) charSelectTitle() string {
	if a.charsel.charSelTitle != "" && a.charsel.charSelTitleFor == a.srvMeta.serverName {
		return a.charsel.charSelTitle
	}
	a.charsel.charSelTitleFor = a.srvMeta.serverName
	a.charsel.charSelTitle = charSelectHeading
	if a.srvMeta.serverName != "" {
		a.charsel.charSelTitle = charSelectHeading + " — " + a.srvMeta.serverName
	}
	return a.charsel.charSelTitle
}

// Background runs the per-frame engine work without drawing — the main
// loop calls it instead of Frame while the window is minimized, so the
// session keeps pumping (keepalives answered, queues drained) at zero
// render cost.
func (a *App) Background(dt time.Duration) {
	// Stamp the frame clock even though we draw nothing this tick. a.now() reads
	// frameNow, and Frame is the ONLY other place it's stamped — so a minimized
	// window (which runs Background, never Frame) would otherwise freeze the clock
	// at the last foreground timestamp. Every a.now()-driven deadline reachable from
	// here is then wedged: the 10s music-await self-heal (settleAwaitedMusic below)
	a.budget.frameNow = time.Now()
	a.pumpConnection()
	a.pumpBackgroundTabs()
	// Drain the OOC automation queue HERE as well as in Frame. The producers (macros,
	// auto-login; historically the live-roster /gas poll, whose deletion is recorded in
	// liveroster.go) are driven by inbound packets and by the frame clock restamped just
	// above, so they keep queueing while the window is minimized — but Frame, the queue's
	// only other drain, never runs in that state. That producer/consumer split let the
	// queue fill for the whole occlusion and then flush as one burst on the first restored
	// frame, which servers read as OOC flooding and answer with a silent kick. Draining on
	// both paths keeps the send rate identical in every window state — and it stays
	// necessary without the poll, because any producer at all can queue while minimized.
	a.processOOCQueue()
	a.drainWarnings()
	a.drainMusicFailures() // transient music-fetch failures → jukebox warn line (§1.1)
	// Auto-reconnect is deliberately NOT polled here (behaviour, restored by
	// user request): pumpConnection above still detects a drop and lands us on the
	// lobby with the retry armed, but the retry itself fires only from the foreground
	// Frame loop (pollAutoReconnect). So a drop taken while the window is minimized
	// waits at the lobby until the user returns — instead of the behaviour of
	// silently reconnecting in the background and stranding them at char-select on a
	// churn of retries before they even restore the window.
	a.pollCharINI()        // drain the async char.ini result here too, so the emote list appears at idle=0 (a skipped courtroom frame never reaches the draw-time poll)
	a.pollLogBrowser()     // same for the log browser's off-thread scope load (session list + log area) — else it stays blank at idle=0 until cursor motion
	a.updates.pollUpdate() // and the self-update result — else "Downloading…" never flips to "Restart to apply" at idle=0 until cursor motion
	if a.link.room != nil {
		a.link.room.Update(dt)
		// Re-derive the message duck here too. Frame does this inside its own
		// `case a.room != nil` block; without the same call here, advancing the
		// message lifecycle while minimized (room.Update above) would finish a
		// message and return to Idle/Linger but NEVER lift the 35% duck — music
		// stays quiet the whole time the window is minimized, healing only on
		// restore. Same missing-in-Background pattern as the frozen clock above.
		a.rederiveMessageDuck()
		a.noteOverlayStart()
	}
	a.d.Audio.Frame()
	a.settleAwaitedMusic() // delivery-time cross-tab un-duck: lift once the awaited track is what's playing
	a.d.Pump.Frame()
	// AFTER the upload pump, so the touch wins recency over THIS tick's upload
	// burst — exactly what a drawn frame does (the emote row touches these after
	// Pump.Frame too). Touching before the pump let the burst supersede it.
	a.keepActiveAssetsWarm()
	a.keepSceneAssetsWarm() // the un-drawn stage must survive the burst too
	a.d.Store.DrainDestroyQueue()
}

// noteOverlayStart rings the render doorbell the instant a screen effect is TRIGGERED
// while nothing is being drawn.
// Scene.Overlay.Gen is exactly that announcement and costs one uint32 compare per
// Background pass: the courtroom bumps it on EVERY trigger (overlayfx.go), including a
// repeat of the same effect, which is the case a Base comparison would miss.
func (a *App) noteOverlayStart() {
	if gen := a.link.room.Scene.Overlay.Gen; gen != a.frame.drawnOverlayGen {
		a.frame.drawnOverlayGen = gen
		a.NoteDeadline() // damage → the main loop renders on the very next pass
	}
}

// rederiveMessageDuck dips music to duckMusicPercent while a message is on stage
// (shout/preanim/talking) and restores it at idle/linger. Transition-driven — it
// touches SetVolumes (applyAudioVolumes) only when the duck state actually flips, and
// the MusicDucking prefs read is short-circuited so it costs nothing between messages.
// Callers must hold a.room != nil. Shared by BOTH the drawn (Frame) and minimized
// (Background) loops so the derivation can't drift between them: if only Frame ran it,
// a message that finished while minimized would leave the duck stuck at 35% until the
// window was restored.
func (a *App) rederiveMessageDuck() {
	p := a.link.room.Phase()
	wantDuck := p != courtroom.PhaseIdle && p != courtroom.PhaseLinger && a.d.Prefs.Load().MusicDuckingOn
	if wantDuck != a.musicDucked {
		a.musicDucked = wantDuck
		a.applyAudioVolumes()
	}
}

// keepActiveAssetsWarm keeps the user's persistently-shown assets resident while
// the window is minimized — the SELECTED emote's "on" + "off" button images and
// the char icon — so T1's LRU never drops them mid-tab-out (Background draws
// nothing, so a render never touches them; an incoming message then uploads over
// the selected button and it blanks for a moment on restore). Background-only:
func (a *App) keepActiveAssetsWarm() {
	if a.link.room == nil || a.link.sess == nil {
		return
	}
	me := a.activeCharName()
	if me == "" {
		return
	}
	// Futility latch reset: a new character/emote pick warms different bases, so
	// the per-base demand counters start over (see warmMaxDemandsPerBase).
	if me != a.activeWarm.char || a.emotepick.emoteIdx != a.activeWarm.idx {
		a.activeWarm.char, a.activeWarm.idx = me, a.emotepick.emoteIdx
		clear(a.activeWarm.demands)
	}
	if a.emotepick.emoteIdx >= 0 && a.emotepick.emoteIdx < len(a.emotepick.emotes) {
		a.activeWarm.warmBase(&a.env, a.link.urls.EmoteButton(me, a.emotepick.emoteIdx+1, true), assets.AssetTypeEmoteButton)  // selected "on" image
		a.activeWarm.warmBase(&a.env, a.link.urls.EmoteButton(me, a.emotepick.emoteIdx+1, false), assets.AssetTypeEmoteButton) // "off" fallback
	}
	a.activeWarm.warmBase(&a.env, a.link.urls.CharIcon(me), assets.AssetTypeCharIcon)
}

// The scene-heal futility budget: how the heal machinery (keepSceneAssetsWarm,
// healScenery, keepActiveAssetsWarm) distinguishes a heal worth repeating from
// a futile one. Two independent bounds, because there are two distinct failure
// shapes and conflating them regressed (shipped a single demand
// counter, and a merely-SLOW load burned it against ONE in-flight fetch —
// three 250 ms keeper ticks — leaving a healthy scene latched for the epoch):
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

// healAllowed gates one live-scene re-demand against the shared per-scene
// futility budget and counts it. healScenery and keepSceneAssetsWarm share the
// budget: they heal the same bases, and either one alone could sustain the
// over-budget eviction churn. Counters reset on a warm-set change or a new
// ceremony (keepSceneAssetsWarm, which runs on both the drawn and the
// skipped/minimized paths and marks landings via markResident).
func (s *sceneWarmState) healAllowed(base string) bool {
	st := s.demands[base]
	if st.seenResident {
		// It landed since the last demand and is missing AGAIN: a genuine
		// eviction-churn cycle — the thing the hard bound exists for. Persist
		// the consumed fold (a set seenResident implies a prior write, so the
		// map exists).
		st.churns++
		st.asks = 0
		st.seenResident = false
		s.demands[base] = st
	}
	// Exactly warmMaxHealChurns churn cycles get a heal (refused from the
	// N+1th), and warmMaxHealAsks demands may run without a landing between.
	if st.churns > warmMaxHealChurns || st.asks >= warmMaxHealAsks {
		return false
	}
	st.asks++
	if s.demands == nil {
		s.demands = make(map[string]sceneHealState) // bounded: scene bases only, reset per scene
	}
	s.demands[base] = st
	return true
}

// markResident records that a demanded base is resident right now — the
// landing proof healAllowed's asks bound resets on, and the arming edge
// for its churn bound. No-op for bases never demanded (no map growth) and for
// already-marked entries (no map write on the steady state).
func (s *sceneWarmState) markResident(base string) {
	st, ok := s.demands[base]
	if !ok || (st.seenResident && st.asks == 0) {
		return
	}
	st.seenResident = true
	st.asks = 0
	s.demands[base] = st
}

// warmBase keeps base resident across a minimized upload burst: touch it (→
// most-recent, the LAST to evict) if it's in T1, else re-demand it — once it has
// evicted, a Get alone can't bring it back, so the asset would stay gone until
// the post-restore render heals it. Re-demands are throttled to the scene
// keeper's cadence and ask-latched per base (warmMaxHealAsks — a plain asks
// bound suffices here: buttons/icons live in the small-UI shield tier where
// eviction ping-pong is structurally impossible, so the only futile shape is a
// permanent 404). A landing clears the counter: the asks were not futile.
func (activewarm *activeWarmState) warmBase(e *env, base string, t assets.AssetType) {
	if base == "" {
		return
	}
	if e.d.Store.Contains(base) {
		e.d.Store.Get(base)
		activewarm.landed(base)
		return
	}
	now := time.Now()
	if !activewarm.mayDemand(base, now) {
		return
	}
	activewarm.noteDemand(base, now)
	e.d.Manager.Prefetch(base, t, network.PriorityHigh)
}

// landed clears the ask counter for a base that is resident again: the asks were
// not futile. Only the keeper's own map is touched, and only when it is non-empty
// (the steady state allocates and writes nothing).
func (w *activeWarmState) landed(base string) {
	if len(w.demands) != 0 {
		delete(w.demands, base)
	}
}

// mayDemand is the throttle: at most one re-demand per sceneWarmRedemandEvery, and
// at most warmMaxHealAsks asks per base without a landing. Buttons and icons live
// in the small-UI shield tier where eviction ping-pong is structurally impossible,
// so a plain asks bound suffices — the only futile shape there is a permanent 404.
func (w *activeWarmState) mayDemand(base string, now time.Time) bool {
	if now.Sub(w.lastDemand) < sceneWarmRedemandEvery {
		return false
	}
	return w.demands[base] < warmMaxHealAsks
}

// noteDemand records the demand the caller is about to submit (bounded: ≤3 keys —
// the two button spellings plus the icon).
func (w *activeWarmState) noteDemand(base string, now time.Time) {
	if w.demands == nil {
		w.demands = make(map[string]int)
	}
	w.demands[base]++
	w.lastDemand = now
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
	if a.link.room == nil && !(a.sel.replaying && a.sel.replayRoom != nil) {
		return // no stage on screen (lobby) — nothing to pin
	}
	sc := a.renderScene()
	// Futility latch resets. (1) A new message / room rebuild swaps the warm
	// set — a fixed array compare, no allocation on the settled steady state.
	warmSet := [6]string{sc.BackgroundBase, sc.DeskBase,
		sc.Speaker.Active, sc.Speaker.IdleBase, sc.Pair.Active, sc.ChatSkinBase}
	if warmSet != a.sceneWarm.set {
		a.sceneWarm.set = warmSet
		clear(a.sceneWarm.demands)
	}
	if a.link.room != nil {
		if p := a.link.room.Phase(); p != a.sceneWarm.phase {
			if a.sceneWarm.phase == courtroom.PhaseIdle {
				clear(a.sceneWarm.demands) // idle → ceremony: a new message began
			}
			a.sceneWarm.phase = p
		}
	}
	now := time.Now()
	throttleOpen := now.Sub(a.sceneWarm.lastDemand) >= sceneWarmRedemandEvery
	demanded := false
	warm := func(base string, t assets.AssetType) {
		if base == "" {
			return
		}
		if a.d.Store.Contains(base) {
			// Touch → most-recent, the last to evict; a demanded base being
			// resident is also the landing proof the heal budget re-arms on.
			a.d.Store.Get(base)
			a.sceneWarm.markResident(base)
			return
		}
		// A desk that 404'd CONCLUSIVELY can never become Contains, so without this
		// it re-entered the re-demand arm below on every tick of every message —
		// healScenery's desk arm already carries exactly this gate and this, its
		// twin, was left without it, so #27's fix only ever covered half the
		// re-demand sites. That is the "one missing <pos>_overlay logs a miss every
		// few seconds forever" report: the courtroom's per-message Prefetch is the
		// deliberate re-try for a desk that may reappear, not this keeper. The set
		// self-heals (clearMissing on upload), and only sprites and desks are ever
		// MarkMissing'd — Background deliberately is not (it would change the
		// viewport's hold-previous semantics), so no other warm arm is affected.
		if t == assets.AssetTypeDeskOverlay && a.d.Store.IsMissing(base) {
			return
		}
		// Evicted (or never landed): re-demand, throttled scene-wide AND
		// futility-latched per base (sceneHealAllowed). Without the latch, a
		// settled scene whose decoded working set exceeds the T1 main tier
		// churned forever: each re-demand fully re-decoded a large animated
		// asset, re-uploaded up to a cap-sized page, and evicted ANOTHER warm
		// base — which the next tick re-demanded, at ~4 whole-core-burning
		// cycles/sec while the app sat idle or minimized (the CPU-burn report;
		// the "stage blinks" report is the same churn's visible face).
		if throttleOpen && !demanded && a.sceneWarm.healAllowed(base) {
			if t == assets.AssetTypeCharSprite {
				a.d.Manager.PrefetchChain(base, courtroom.SpriteHealAlts(base), t, network.PriorityHigh) // AssetType: CharSprite (bare + folder, so no spelling ever false-flags missing)
			} else {
				a.d.Manager.Prefetch(base, t, network.PriorityHigh)
			}
			demanded = true
			a.sceneWarm.lastDemand = now
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
// with no trace — the reported crash that left no log behind. This writes the
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
func (a *App) Room() *courtroom.Courtroom { return a.link.room }

// IsLiveBase reports whether base belongs to the on-screen message (upload
// budget bypass).
func (a *App) IsLiveBase(base string) bool {
	if a.link.room == nil {
		return false
	}
	sc := &a.link.room.Scene
	switch base {
	case sc.BackgroundBase, sc.DeskBase, sc.ShoutBase,
		sc.Speaker.Active, sc.Pair.Active:
		return true
	}
	return false
}

// IsLiveStageBase reports whether base is any DRAWN stage layer — the
// background, the desk, or the current speaker/pair sprite — i.e. the held-frame
// bridge's steal gate (render/textures.go onEvict). Originally scenery-only
// (bg + desk), it now includes the drawn speaker/pair bases too: when the
// CURRENT speaker's page is evicted mid-display (a cap-sized incoming upload can
// force it), drawSprite's hold-previous fallback can't help — lastGood equals
// the base that was just drawn — and the thumbnail path is opt-in default-OFF,
// so the character blanked until the futility-latched heal re-decoded (or
// forever, once the heal churn budget was spent). Stealing the speaker/pair
// frame here lets drawSprite's miss path draw the frozen sprite instead of the
// black stage fill (the last black-flash hole). Uses the same scene the
// viewport draws (renderScene covers the replay/slideshow overrides). Render
// thread only — onEvict runs on every T1 mutation, all render-thread.
func (a *App) IsLiveStageBase(base string) bool {
	if base == "" || (a.link.room == nil && !(a.sel.replaying && a.sel.replayRoom != nil)) {
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

// --- connection lifecycle -------------------------------------------------------

// Connect dials a server in a NEW tab. Whatever was active parks and
// keeps running in the background (rehearsal disconnects instead — it
// can't background). At the tab cap the connect refuses with a visible
// reason and the current session stays untouched.
