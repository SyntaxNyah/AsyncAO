package ui

import (
	"context"
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
	// The manual Refresh button deliberately bypasses this — explicit user intent.
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

// App is the whole client UI state machine. Render thread only.
type App struct {
	d   Deps
	ctx *Ctx

	screen     Screen
	prevScreen Screen // for settings/about back navigation

	// --- the theme editor (themeeditor.go) --------------------------
	//
	// ONE POINTER, nil while the editor is shut, and that is the whole of its cost
	// when closed: no goroutine, no timer, no per-frame read from any courtroom draw
	// site. This is the "0 base = 0 cost" rule.
	te *themeEditor

	// --- connection lifecycle (lobby-global; deliberately NOT in sessionState) ---
	// These outlive any single session: the lobby shows the last disconnect
	// reason and a one-click Reconnect, and auto-reconnect retries ACROSS the
	// very resetSessionState() that Disconnect runs. They must live on App, not
	// sessionState — a past refactor swept them into sessionState, so Disconnect
	// wiped them mid-teardown and silently killed the reason text, the Reconnect
	// button and the backoff counter. Keep them here.
	connErr string // last lobby error/notice (disconnect reason, dial failure, …)
	// connErrLine is the drawable one-line view of connErr, memoized against
	// connErrSrc. Memoized because building it clamps runes and so ALLOCATES, and
	// this is read from the lobby's per-frame draw — an allocation there is what the
	// lobby alloc gate exists to catch. See connErrLabel.
	connErrLine  string
	connErrSrc   string
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
	// pendingSessionCarry is the one-shot "we are coming back" snapshot of the
	// log-content fields (sessioncarry.go) captured at a teardown that means
	// exactly that — the disconnect dialog's Reconnect, or an involuntary drop
	// about to auto-retry — and reapplied by connectWith only when the redial
	// lands on the SAME server. Lives here, not in sessionState, for the same
	// reason lastConnURL does two fields up: it must outlive the very
	// resetSessionState() call (Disconnect, then connectWith's own reset) that
	// it exists to protect content from — sessionState is what that call
	// replaces wholesale. pendingSessionCarryKey is the serverKey (dialled
	// address, not display name) it was captured from; nil/"" together mean
	// nothing is pending.
	pendingSessionCarry    *sessionCarry
	pendingSessionCarryKey string

	// --- lobby state ---
	masterEntries []network.ServerEntry // raw master list (no favorites)
	servers       []network.ServerEntry
	lobbyStatus   string
	lobbyFetching bool
	lobbyResult   chan lobbyFetch
	// lastLobbyRefreshAt is when the last master-list fetch STARTED (stamped in
	// RefreshServers, past its dedup gate, so every path arms it). App-global,
	// not sessionState: the lobby is a cross-tab surface. It feeds the on-open
	// auto-refresh cap — see lobbyAutoRefreshDue / noteScreenTransition.
	lastLobbyRefreshAt time.Time
	directInput        string
	directSecure       bool
	directSave         bool
	lobbyScroll        int32
	// AsyncAO Server Phone Book: a dedicated lobby page over the user's saved
	// (favorite) servers — manual add + connect, persisted in Favorites (survives
	// Reset, cleared by Wipe), with its own export/import.
	phoneBookPage bool
	pbName, pbURL string // phone-book "add server" form
	// Inline row edit (phone book only): pbEditURL is the identity (stored
	// Favorites URL) of the row currently open for editing — empty = none; the
	// two working buffers hold the in-progress name/address until Save.
	pbEditURL, pbEditName, pbEditAddr string
	// "Phone Fanat" chip (phone-book page only): a dim bottom-right button that
	// pops a quip when clicked. phoneFanatLine holds the currently-shown line and
	// phoneFanatShownAt stamps the click so it lingers for fanatShowDuration.
	// Ephemeral — never persisted.
	phoneFanatLine    string
	phoneFanatShownAt time.Time
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
	// musicTabDucked silences the single music stream because it belongs to a
	// BACKGROUNDED tab (cross-tab music continuity). It's App-level, not per-tab:
	// the one music stream is a global resource, and the flag describes whether
	// the ACTIVE tab owns it. Set true in parkActive when the outgoing tab's
	// still-rolling stream is handed to the background (so it keeps its position
	// but goes inaudible — tabs stay acoustically isolated); cleared when the
	// active tab takes the stream back (its resumed track is what's playing).
	// The tab-isolation work (ecd6d9e isolated logs/scenery) deliberately did NOT
	// isolate music — continuity is the agreed exception; this duck is how it stays
	// isolated ACOUSTICALLY without killing the stream. Suppressed entirely when
	// the MusicAcrossTabs pref is ON (then a backgrounded stream stays audible).
	musicTabDucked bool
	// musicOwnerKey is the serverKey of the tab whose track is in the single music
	// stream right now — stamped at the play sites (the active-tab EventMusic handler
	// and applyResumeDuck, both while a.serverKey is the tab starting the track).
	// Cross-tab continuity (MusicAcrossTabs ON) lets a BACKGROUNDED owner keep the
	// stream audible while a DIFFERENT tab is active; applyAudioVolumes must then scale
	// that stream by the OWNER's per-server music volume, not the active tab's — else an
	// active tab whose per-server music=0 would deterministically silence the owner's
	// continuity stream (a second "toggle does nothing" path). "" = unset: the volume
	// read falls back to the active tab's serverKey (the pre-owner behaviour, correct
	// when the active tab owns its own stream). A stale owner after a stop is harmless
	// (nothing is playing to mis-scale) and the next play re-stamps it.
	musicOwnerKey string
	// musicAwaitURL is the MusicURL the ACTIVE tab has requested while the single
	// stream is still ducked-foreign (musicTabDucked true): the duck may only lift
	// once THIS url is the one the mixer is actually playing, else the un-duck would
	// make a DIFFERENT (still-loading-away) track audible during the download gap —
	// the cross-tab "wrong song plays for a while" bug. "" = nothing awaited (the
	// common idle state). settleAwaitedMusic (per frame, after Audio.Frame drains the
	// delivery) lifts the duck when Audio.CurrentMusicURL() catches up to this. It's
	// cleared at every duck/stream reset (StopMusic, disconnect, tab close/park,
	// streaming-off) so a stale await can never un-duck a later foreign track — the
	// invariant is: stopped ⇒ not ducked, no await.
	musicAwaitURL string
	// musicAwaitSince stamps when musicAwaitURL was armed, so a never-arriving track
	// can't wedge the stream silent forever. Every OTHER release is event-driven
	// (settle-on-match, park, close, disconnect, stop, streaming-off) — but a fetch
	// that SILENTLY fails (a 404, an unsupported format, a URL the resolver builds
	// that CurrentMusicURL never equals, or a stalled download) fires no event, so
	// the awaited URL simply never becomes current and settleAwaitedMusic never
	// lifts the duck. settleAwaitedMusic uses this stamp as the last-resort release:
	// past musicAwaitTimeout it un-ducks anyway so a backgrounded stream can't stay
	// inaudible with no way out. Stamped at BOTH arm sites (the DJ /play handler and
	// resumeActiveTabMusic); the zero value is "nothing armed" (await == "" too).
	musicAwaitSince time.Time
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
	// previewName caches the display name of the emote that produced previewBase,
	// stamped by previewEmote at SET time (it holds the *Emote right there) so the
	// preview-box caption never reverse-maps the URL — the old lookup linear-scanned
	// a.emotes building 1-2 URLs per emote EVERY frame a preview was up (per-frame
	// alloc churn on 4000+ emote packs). previewNameFor records which base the name
	// belongs to: consumers return the name only while it equals previewBase, so
	// every OTHER previewBase setter (bgpicker, wardrobe cycle) auto-invalidates
	// the caption without having to know these fields exist.
	previewName    string
	previewNameFor string
	previewFor     string    // base the preview clock was started for
	previewAt      time.Time // loop anchor — animated previews play, not freeze
	previewZoom    int       // magnifier factor (1 = fit; >1 shows a cursor-panned window)
	// Try-before-wear: cycle a previewed (non-worn) wardrobe character's
	// emotes in the preview box. previewChar guards a one-shot char.ini parse;
	// the capped anim/label slices drive the ‹ › buttons and arrow keys.
	previewChar     string
	previewAnims    []string
	previewLabels   []string
	previewEmoteIdx int
	// previewEmoteCycle records WHICH surface asked for the emote list, because
	// the two want different landings when the parse comes back:
	//   true  (previewEmoteNav)      — the wardrobe's ‹ › navigator: always jump
	//         to emote 0 so the caption index matches the sprite on screen.
	//   false (previewEmotePortrait) — the char-select grid's single portrait:
	//         keep the optimistic "normal" probe (right for almost every pack,
	//         and already resolved) and repoint ONLY when the char.ini names no
	//         emote "normal" — see correctPortraitPreview.
	previewEmoteCycle bool

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

	// last transient toast surfaced to the user. Missing-asset reports left this
	// shared channel in #27 — they own the menu-bar chip below (spec §4).
	warnLine string
	warnAt   time.Time

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
	// DELIBERATELY CLIENT-GLOBAL, unlike the per-tab IC transcripts (translogs).
	// It is a CLIENT diagnostic, not a chat record: most of what lands in it has no
	// tab at all (theme apply, extensions.json seeding, asset casing detection, the
	// updater), and the entries that do — a connect failure, a disconnect reason —
	// are read while triaging the client, where seeing every tab's failures in one
	// stream is the point. Splitting it per tab would hide a background tab's drop
	// behind a tab switch. It is also memory-only (debugLogCap ring, never a file),
	// so the unbounded-file-handle concern that drove the transcript split does not
	// apply here.
	debugLog   []debugEntry // ring of stamped failure rows, debugLogCap max (see pushDebugAt)
	lastPktHdr string       // newest server packet header (health line)
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
	fontRes chan fontLoad
	// themeFontFile is the active theme's bundled font path (.ttf/.otf), resolved
	// off-thread on theme apply; applyFontConfig uses it when no manual font / the
	// dyslexia toggle is set (#6).
	themeFontFile string
	// themeFaceNames / panelFontMissing are Settings-screen diagnostics for the
	// per-panel font overrides: the file each element resolved to, and the
	// families that resolved to NOTHING. Landed on theme apply and read only
	// while the Settings screen is open — never on a draw path.
	themeFaceNames   []string
	panelFontMissing []string
	// themeFontWarn is the ready-made sentence about the THEME's own unresolved
	// families. Kept after the toast has faded so the Settings screen can still
	// explain it — the toast is gone by the time anyone goes looking for why the
	// font is wrong.
	themeFontWarn string
	// fontWarnDlg is the missing-fonts modal. On App and NOT on sessionState,
	// unlike disconnectDlg: that dialog belongs to one tab's frozen session, while
	// a theme is global to the client — one theme, one warning, whichever tab
	// happens to be in front when it applies.
	fontWarnDlg fontWarnDialog
	// serverNoticeDlg is the server's-own-words modal (servernotice.go): a kick/ban
	// reason, a lockdown message, a BB popup notice. On App for the same reason
	// fontWarnDlg is, only more sharply: resetSessionState rebuilds sessionState
	// wholesale on DISCONNECT, so a removal reason parked there would be erased by
	// the very teardown that produced it, before the lobby ever drew.
	serverNoticeDlg serverNoticeDialog
	// chromeMeasure is the shared modal-wrap metric, built once on first use because
	// a closure allocates and it is consulted every frame a modal is open. See
	// chromeWrapMeasure (servernotice.go).
	chromeMeasure func(string) int32
	// panelSizeEdit is the RAW text of each per-panel size box while it is being
	// typed in, keyed by the AO2 element id. It exists because a half-typed number
	// is not a size, and clamping one into range mid-keystroke made anything below
	// ten impossible to enter. Read only while that box has focus; the saved
	// preference is the truth otherwise, so nothing here has to be cleaned up.
	panelSizeEdit map[string]string
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
	// System-font census (fontcensus.go): the LAST resort, reached only when a rune
	// nothing in the curated chain covers actually reaches the draw. The render
	// thread sends those runes on censusReq and installs whatever face comes back on
	// censusRes; the goroutine that walks the machine's font directories is started
	// on the first miss, so a session that never hits one never scans anything.
	// missingBuf is the reusable drain buffer — the drain runs every frame, so it
	// must not allocate.
	censusReq     chan rune
	censusRes     chan []byte
	censusStarted bool
	missingBuf    []rune

	// Custom missingno placeholder (user-chosen image path, default = the embedded
	// AO2 glitch). The file read + decode runs OFF-THREAD (rule §17.2 — no
	// synchronous disk I/O on the render path); the decoded page lands on
	// errorSpriteRes and pollErrorSprite UploadPinned's it over render.MissingKey
	// on the render thread. errorSpriteErr carries the inline Settings error label
	// on a failed load (kept for the Settings row to show). See missingno.go.
	errorSpriteRes chan errorSpriteLoad
	errorSpriteErr string

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
	// Bounded by transcriptWriterCap. nil/empty = off / nothing logged yet.
	//
	// An entry lives exactly as long as the session that owns it: reapTranscripts
	// retires and deletes it the moment nothing does (tab closed, session reset),
	// and CloseTranscript drains whatever is left at exit. Holding them to shutdown
	// instead leaked a file + goroutine per reconnect and hit the cap after 64.
	translogs map[translogKey]*transcriptWriter
	// nextLogSession mints sessionState.logSession. Monotonic for the process, so an
	// id is never reused even after a tab closes — a reused id would let a new tab
	// adopt a dead tab's still-open writer and start appending to its file.
	nextLogSession int

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
	// H5 replay pre-warm: a FLAT (non-bundled) recording streams its sprites +
	// music from the origin, so a bounded warm phase loads them BEFORE the first
	// event plays — nothing pops in or plays silent. Non-nil only while that phase
	// runs (allocated in startReplay, cleared when it ends or the replay stops). All
	// its state + tick logic lives in replaywarm.go; see replayWarmState.
	replayWarm *replayWarmState

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
	// exportSavedDevScale remembers the live text device scale (#77) while an
	// export session brackets ctx.textDevPct to 100 (native offscreen resolution —
	// the live UI scale must never leak into export pixels). 0 = not exporting;
	// set at export start, restored (and zeroed) at every finisher. gif/webp/video/
	// comic all render offscreen and share this bracket.
	exportSavedDevScale int
	gif                 *gifExportJob
	gifResultCh         chan exportResult // off-thread encode → UI (result line + artifact path)
	// Content report / "Package this RP" (contentjob.go): enumerate a recording's
	// assets, probe found/missing at the origin (bounded off-thread workers,
	// render-thread polled), then optionally package the found bytes into a
	// self-contained bundle folder. Single-flight (contentBusy); contentGen
	// invalidates a cancelled job's late goroutine sends. Allocated only while a
	// job runs — nil, zero-cost, otherwise.
	content     *contentJob
	contentBusy bool
	contentGen  int

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
	themeRes atomic.Pointer[themeApply]
	themeGen atomic.Uint64
	// themeCat is the off-thread theme CATALOG (themecatalog.go): what themes
	// exist, where they came from, what shape they are in. Same discipline as
	// themeRes and for the same reason — the scan reads directories, the draw
	// path may not (hard rules 2 and 5), so the answer arrives as an immutable
	// snapshot behind a pointer.
	themeCat atomic.Pointer[themeCatalog]
	// themeCatBusy single-flights the scan. Atomic because the scanner
	// goroutine is the one that clears it.
	themeCatBusy atomic.Bool
	// themeCatFocusStat latches a window focus-gain for the settings screen to
	// consume ("I dropped a theme in Explorer and came back"). Render/event
	// thread on both ends, so a plain bool is correct.
	themeCatFocusStat bool
	// themeReport is the applied theme's IMPORT REPORT (themereport.go) — every
	// degrade, missing family and unbound key the last apply noticed. Bounded by
	// themeReportCap and replaced wholesale at each landing, never appended to.
	//
	// TWO READERS, both load-bearing: noteThemeReport says the headline on the warn
	// line, and the editor's rail chip plus in-canvas panel (themereportpanel.go)
	// show all of it. Before that panel existed this field was written on every
	// apply and read by one test — the parsed-but-never-applied shape rule 11 names
	// by example.
	themeReport []string
	// themeImp is the .aotheme TRANSPORT state machine (themeimport.go): the
	// pending consent, and the one bundle job that may be in flight. On App
	// rather than on a screen because a drop lands on whatever is visible and
	// the import has to survive the user walking to another screen while it runs.
	themeImp themeImport
	// themeCopy is the one in-flight copy-for-editing (themecopy.go). On App and
	// not on the editor because BOTH surfaces start one — the Settings row with no
	// editor open, and the editor's refused Save — and the pointer IS the
	// single-flight flag, so a second press while one runs is refused with a line
	// rather than racing it.
	themeCopy *themeCopyJob
	// themeCreate is the one in-flight theme MINT (themecreate.go). On App and not
	// on the editor for the same two reasons themeCopy is: both surfaces start one
	// — the Settings ▸ Theme editor tab with no editor open, and the editor's own
	// preset gallery — and the pointer IS the single-flight flag, so a second press
	// while one runs is refused with a line rather than racing it onto the same
	// collision-suffixed name.
	themeCreate *themeCreateJob
	// themeEditArmGen / themeEditArmFrom are the one-shot that opens the editor
	// over a finished copy, armed exactly as themeResizeArmGen is: it names ONE
	// apply generation, so it cannot leak onto healTheme's eviction re-kick or a
	// boot apply, and the screen it was armed from is remembered so an editor
	// never opens itself over a user who walked away.
	themeEditArmGen  uint64
	themeEditArmFrom Screen
	// themeAppliedGen is the gen of the newest load pollThemeApply has already
	// landed. The publish-time newest-wins guard only protects an UNCONSUMED
	// result; once pollThemeApply Swaps one to nil, a slower older-gen load can
	// still publish afterward. Comparing against this on the consume side drops
	// that stale landing (which would revert the theme + clear its font).
	themeAppliedGen uint64
	themeHealAt     time.Time       // last eviction heal (themeHealPeriod pacing)
	themeTex        map[string]bool // theme:// stems resident in T1
	themeSounds     map[string]string
	themeMsgCol     sdl.Color
	themeHasMsg     bool
	themePalette    theme.Palette // last theme's chrome palette, kept so a #M3 chrome-preset change can re-overlay it
	themeNameCol    sdl.Color
	themeHasName    bool
	// themeShownameAlign is the active theme's `showname_align`, the one piece of
	// chatbox text placement that is design data rather than a Qt default
	// (chatboxfit.go). Parsed off-thread with the rest of courtroom_design.ini and
	// landed by pollThemeApply; the zero value is Left, which is both AO2's
	// fallback and AsyncAO's historical behaviour.
	themeShownameAlign shownameAlign
	// themeShownameExtra is the active theme's `showname_extra_width` in DESIGN
	// pixels — the rung height of AO2's widen-and-swap ladder (chatboxfit.go).
	// Design space, not window space: themeLayoutIn scales it with the rect it
	// widens and bakes the result into the layout cache, so the draw path never
	// touches a float. Zero (the value nine reference themes declare, and the value
	// a themeless App carries) disables the ladder.
	themeShownameExtra int
	// themeFonts is the applied theme's per-element courtroom_fonts.ini table
	// (#39): declared point size folded to a percent, the resolved family's face
	// slot, and <id>_bold. The ZERO table means "this theme dresses nothing",
	// which every accessor in themefonts.go turns back into the pre-#39 path.
	themeFonts themeFontTable
	// Theme courtroom geometry (courtroom_design.ini): design-space rects
	// + emote grid metrics; themeLay caches the window-scaled rects.
	// themeRectsOrig keeps the theme's PRISTINE geometry so the layout
	// editor's right-click/Reset can restore it under the user overrides.
	themeRects     map[string]theme.Rect
	themeRectsOrig map[string]theme.Rect
	themeEmoteCell [2]int
	themeEmoteGap  [2]int
	themeLay       themeLayoutCache
	// --- free elements (themeelements.go / themeelementbake.go) -----
	//
	// themeSidecar is the applied theme's parsed asyncao_theme.ini, or nil for a
	// stock AO2 theme — which is every theme that has never been opened in the
	// editor, so nil is the ordinary case and every read of it is nil-safe. Loaded
	// on the theme-apply goroutine (theme.Load), landed here by pollThemeApply, and
	// read ONLY by the cold bake in themeLayoutIn.
	themeSidecar *theme.Sidecar
	// themeElemGen stamps each element bake. Monotonic across rebuilds, so the
	// per-frame condition memo below cannot mistake a fresh (zeroed) cache for the
	// one its indices were interned against.
	themeElemGen uint64
	// elemBakeBand / elemBakeZ are the bake's two SORT KEYS, held beside the baked
	// array instead of inside it: the draw path has no use for either, and a second
	// full copy of the array would be 11 KiB of App for one insertion sort.
	elemBakeBand [theme.ElementCap]theme.ElementBand
	elemBakeZ    [theme.ElementCap]int16
	// condSrc / condLive memoise this frame's visible_when resolution: the live
	// string each axis was last resolved from, and the interned index it resolved
	// to (condAlwaysVisible = nothing in this theme matches). condGen is the bake
	// generation they belong to. Refreshed once per themed pass, never per element.
	condSrc  [theme.ConditionAxisCount]string
	condLive [theme.ConditionAxisCount]int16
	condGen  uint64
	// condSpeaking is the one VALUE-LESS condition axis: a message is on the stage.
	condSpeaking bool
	// --- element clocks + effects (themeclock.go) -------------------
	//
	// themeClocks is the FIXED authoring-clock pool. Clock 0 is the shared themeAt
	// anchor, so the ZERO VALUE of this array is "every element runs exactly as
	// themeFrame always has" — which is why nothing has to initialise it and a stock
	// AO2 theme costs nothing for it. Re-loaded beside a.themeAt in pollThemeApply.
	themeClocks [theme.ClockCap]themeClock
	// themeFX is the STATEFUL-effect pool: the three decaying one-shots need an
	// origin, the five periodic kinds are pure arithmetic and take no slot. Fixed,
	// self-clearing (see beginThemeFXPass), and zero-value-free by construction —
	// fxState.owner is the baked index PLUS ONE so that 0 means "free".
	themeFX [themeFXCap]fxState
	// themeFXFrame is the pool's own pass counter, and the sweep's only input. Not a
	// global frame counter: it advances once per ELEMENT pass, which is the only
	// cadence the pool cares about.
	themeFXFrame uint64
	// themeFXOver records that the pool ran out during THIS pass — the editor's chip
	// and the F8 line read it. Cleared by every sweep, so it describes the last
	// pass rather than the session.
	themeFXOver bool
	// themeFXFrozen latches ReduceMotion for the FRAME. ONE prefs read per frame,
	// never per element or per draw site (the two AllocsPerRun gates forbid the
	// latter, and the theme-chrome draw sites number about ten), and it is what
	// makes the accessibility answer and the pacing census the same decision.
	//
	// TWO latch points, both calling reduceMotionNow so there is one spelling of
	// the read: App.Frame, because themeFrame is reached from screens that never
	// open an element pass (char select, settings previews, the export capture);
	// and beginThemeFXPass, because an offscreen pass may run without a Frame
	// around it. Latching twice in a frame is harmless — the second read of an
	// unchanged pref is the same answer.
	themeFXFrozen bool
	// themeSidecarErr / themeSidecarTip are the applied theme's sidecar REFUSAL, as
	// the two strings the chip needs (themeerrchip.go): the debug-log line and the
	// hover text. Both are built on the apply goroutine because both format; "" is a
	// theme whose extension tier loaded (or which has none at all), which is almost
	// every theme.
	//
	// They exist because theme.Theme.SidecarErr had no consumer and a refused sidecar
	// is INVISIBLE: the theme applies, its AO2 half renders, and every element,
	// override, palette role and pane in the file is silently gone.
	themeSidecarErr string
	themeSidecarTip string
	// themeSidecarErrAck is the chip's acknowledgement. Re-armed (cleared) by every
	// apply that still refuses, so dismissing it can never lose the state — and never
	// cleared on a timer, because the condition does not expire on its own.
	themeSidecarErrAck bool
	// --- theme media (thememedia.go) --------------------------------
	//
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
	//
	// It NAMES its apply rather than being a bare flag because the apply pipeline is
	// re-entered constantly by things that must never move the window — boot,
	// healTheme's eviction repair (fired from any theme-art draw, including the
	// Settings screen's own live preview), the texture-filter Purge recovery, and a
	// server binding its own theme. A bare flag is consumed by whichever apply lands
	// first, so a heal that was already in flight when the user opened the theme
	// dropdown would eat the arming and snap the window to the PREVIOUS theme's
	// canvas, leaving the theme they actually picked unsnapped. Comparing gens makes
	// the arming unforgeable: only the apply the user's click started can spend it.
	//
	// Armed at the USER-INITIATED sites (the Settings dropdown, the < > cycle
	// arrows, a user-driven theme-folder scan landing) and once at boot when the
	// ThemeFit default move ran for this prefs file, consumed by
	// maybeResizeToThemeDesign. Render thread on both ends, so it needs no
	// synchronisation (hard rules 1 and 8).
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
	// when true it OR's into the sibling-magnet bypass (siblingMagnetOff), disabling
	// the forced piece-to-piece snap without holding Shift per drag. It gates ONLY
	// snapToSiblings — screen-edge snapping and the de-overlap pass are separate
	// concerns and stay on. Session state (not persisted), mirroring layoutSnap's
	// treatment. Stored as the "off" sense deliberately: the zero value is false =
	// magnet ON, so float panels snap in normal play (before any editor opens)
	// exactly as before this chip existed — no init/reset wiring needed.
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

	// --- classic-layout slot editor (the default/non-themed courtroom) ---
	// Mirrors the themed editor's feel, but edits SCREEN-space "slots" persisted
	// as window FRACTIONS (config.ClassicLayout) so they survive window resizes.
	// classicOv is an App-local lock-free snapshot read by slotRect on the render
	// path (the editor is the sole writer); slotReg is this frame's registered
	// slots, populated ONLY while editing so the common frame stays alloc-free.
	classicEdit     bool
	classicOv       map[string][4]float64
	classicOvLoaded bool
	// tornZ is the session-scoped tear-off panel z-order (torntabs.go), a
	// permutation of tornTabTable indices back-to-front. Empty = table order.
	// It exists so clicking a floating panel brings it to the front instead of
	// the fixed order always drawing Players over Music/Areas.
	tornZ []int
	// classicAnchor holds per-slot window pins (parsed from
	// config.ClassicAnchors alongside classicOv): a pinned slot's override
	// re-bases to pixel offsets from its window corner/centre on resize
	// instead of scaling with the fractions (classicanchor.go).
	classicAnchor map[string]anchorRef
	// classicRot holds per-slot ROTATION angle bytes (A4), snapshotted from
	// config.ClassicRotations alongside classicOv/classicAnchor. Read by the draw
	// path when resolving a texture-backed classic element's angle (the HP bar);
	// an absent entry means angle 0 → the plain Copy path (byte-identical to the
	// unrotated draw). The editor is the sole writer thereafter.
	classicRot map[string]uint8
	slotReg    map[string]slotInfo
	// classicTgt is the classic editor's selection, the spaceClassic half of the
	// shared edit-target model (edittarget.go). It was a bare slot-name string;
	// the space it lives in — SCREEN px, persisted as a window fraction — is now part
	// of the value rather than something the reader had to know.
	classicTgt       editTarget
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
	// (banner + tab tray), set to classicTabTrayBot each edit frame — slot name
	// tags clamp below it so a box parked in the top strip can't plaster its tag
	// over the editor's own controls.
	classicChromeBot int32
	// alignGuides/alignScratch are the editor's per-drag-frame alignment
	// results and its reusable other-slots buffer (classicalign.go). Editor
	// path only; reused so a long drag allocates nothing after warm-up.
	// alignGuides is ALSO reused by the live piece-to-piece magnet (magnet.go)
	// and the themed editor's magnet: safe because (a) the CLASSIC editor's guides
	// and the live magnet never share a frame — drawFloatingPanels early-returns
	// while classicEdit is on (blockingCourtPopup); and (b) the THEMED editor both
	// populates AND renders its guides inside drawLayoutEditor before the later
	// drawFloatingPanels harmlessly re-slices the (already-drawn) buffer to empty
	// (no live panel drags under the editor fence, so the live guide draw no-ops).
	alignGuides  []alignGuide
	alignScratch []sdl.Rect
	// panelMagnetRects is the live-magnet candidate buffer (magnet.go): every
	// OTHER open floating surface's rect, rebuilt once per frame at the top of
	// drawFloatingPanels ONLY while a drag is active (empty on settled frames).
	// Reused so a long drag allocates nothing; bounded by panelMagnetCap.
	// themeAlignScratch is the themed layout editor's design-space candidate buffer
	// (the classic editor already has alignScratch).
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
	// A1 — the compact bottom-right toolbox is the single show/hide + editor
	// surface. toolboxPinned latches the flyout open (press the grip / pin chip);
	// while pinned, toolboxPieces reveals the in-flyout per-piece hide/show panel
	// (the retired drawUICfgPanel's replacement). toolboxPiecesScroll is that
	// panel's vertical scroll. All three are session state (the &App{} zero value
	// is "collapsed, no pieces"), so no ctor wiring is needed.
	toolboxPinned       bool
	toolboxPieces       bool
	toolboxPiecesScroll int32
	// toolboxFilter is the pieces-panel text filter (Phase 3 "easier to
	// navigate"): the raw box text as typed. toolboxFilterLower is its lowered
	// form, re-derived ONLY when toolboxFilter changes (drawToolboxPieces caches
	// it), so per-row matching in the (per-frame) panel draw allocates nothing —
	// it compares two already-lowered strings via strings.Contains. Session state
	// (zero value = empty filter = every row shown).
	toolboxFilter      string
	toolboxFilterLower string
	// toolboxThemeRect is the compact toolbox's per-frame themed position: a themed
	// courtroom whose design INI ships the OPTIONAL "asyncao_toolbox" key sets this
	// (from lay.rect) before drawing the toolbox, so the strip anchors where the
	// theme/themed-editor placed it — the dual-key twin of the classic slotToolbox
	// override (mirrors slotICFx's asyncao_ic_fx). Absent key ⇒ ok=false ⇒ the
	// classic-slot / bottom-right default path. Set + cleared each themed frame; the
	// value type (rect + bool) allocates nothing.
	toolboxThemeRect   sdl.Rect
	toolboxThemeRectOn bool
	// toolboxFence is THIS FRAME's single toolbox decision (courttoolbox.go):
	// fenceCompactToolbox latches it at the top of the courtroom pass and the draw
	// at the bottom of the pass replays it, so the published overlay fence and the
	// painted pixels are the same answer even though handleHotkeys and the modal
	// return sit between them. Cleared at the top of every drawCourtroom, so a
	// theater / lobby / dropped-session frame can never replay a stale one. Value
	// type, App-level (the toolbox is client chrome, not per-tab state).
	toolboxFence compactToolboxLatch
	// tabStripPaint is THIS FRAME's answer to which of the server-tab strip's two draw
	// sites paints it (chrometop.go). Taken once at the top of the courtroom pass and
	// replayed by both sites, so a mid-frame arm/disarm can neither paint it twice nor
	// drop it. Reset by App.Frame, so a frame with no courtroom pass falls back to the
	// over-everything site. Value type, App-level — the strip is client chrome.
	tabStripPaint tabStripPaintLatch
	// App-wide menu bar (#14, menubar.go). menuOpen is the TITLE of the open
	// top-level menu and menuSub the LABEL of the open submenu row — strings, not
	// indices, so the &App{} zero value is "closed" (index 0 would mean "Servers is
	// open" on every freshly built App, test fixtures included) and so reordering
	// the model can't silently reinterpret which pane is up. menuFenceOn records
	// that WE set c.modalOn; because that flag is SHARED with four sibling popups,
	// the release also has to check that none of them is holding it (see
	// menuBarModalFence / otherModalFenceHeld).
	// menuBar is THIS FRAME's latched decision: menuBarFrame computes it above the
	// screen dispatch (the fence-ordering precondition — see menubar.go's header)
	// and drawMenuBar replays it after the screens. All value types, App-level (the
	// bar is client chrome, not per-tab state).
	menuOpen    string
	menuSub     string
	menuFenceOn bool
	menuBar     menuBarLatch
	// editorBannerPainted is "a layout editor's banner reached the screen this
	// frame" — set by the editors' own banner paint, reset by App.Frame, and read
	// once by drawMenuBar (menuBarPaintsNow). See noteEditorBanner in menubar.go for
	// why the paint decision needs this rather than the armed flags.
	editorBannerPainted bool
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
	// Chrome SHAPE masks (A5): procedural alpha silhouettes for the kit chrome,
	// resolved into these fields ONCE per frame by refreshShapeMasks so the
	// per-widget FillShaped/ButtonCol draws read pointers (never a store Get,
	// which would clear on every streaming generation bump). shapeMasksBuilt
	// latches after the pinned masks upload; shapeMasksGen mirrors the store
	// generation so an eviction that dropped the pinned pages re-triggers a
	// (paced) rebuild. shapeMaskShape records which preset the resident masks
	// were built for so a Settings change re-rasters. See shapemask.go.
	shapeMasksBuilt bool
	shapeMasksGen   uint64
	shapeMaskShape  string
	shapeMaskAt     time.Time // last (re)build for eviction-heal pacing
	// shapeResolved* memoize the last successful per-frame mask resolve
	// (shape, ADJUSTED tier, store generation): when none of the three moved
	// and the shaped path is still enabled, refreshShapeMasks skips the
	// key-concat store Gets entirely, so the always-on frame hook is 0-alloc
	// on settled frames. Safe because EVERY store mutation that could
	// invalidate the pointers (eviction, our build/sweep, UploadPinned
	// replacing a key, Purge) bumps the generation.
	shapeResolvedShape string
	shapeResolvedTier  int
	shapeResolvedGen   uint64
	// themeAt anchors the looping animation clock for animated theme art
	// (chatbox/buttons/backdrops shipped as animated webp/gif/apng).
	themeAt time.Time
	// themeAppliedName is the last theme that actually landed —
	// ensureThemeForSession compares against it so per-server bindings
	// only trigger loads when something really changes.
	themeAppliedName string

	// showWidgets opens the "Extras" box: every AsyncAO feature an AO2 theme has
	// no button for. The themed Extras button opens it; so does the Extras
	// hotkey, which works in EITHER mode (in the classic path it's just a quick
	// launcher, since those features already have buttons). themedHintShown
	// gates the one-time hint that points players at it (legacy themes carry no
	// AsyncAO element keys).
	showWidgets     bool
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
	// pairWin / modWin / cmWin / hkWin / evidWin are floating-window geometries
	// (floatwin.go) for the Pairing, Mod dashboard, CM, Hotkey-sheet, and Evidence
	// panels — each a movable/resizable, non-blocking box (chat stays live behind it)
	// rather than a modal. Geometry is global; open state is per-tab / per-flag.
	pairWin, modWin, cmWin, hkWin, evidWin, modcallWin, msgWin, voiceWin, banWin, debugWin floatWin
	// timerWin / loginWin / pairPopWin are the three that used to be return-to-top
	// modals (#31: "bringing up the Extras → Timer hides all the other UI"). They took
	// the whole courtroom pass for a countdown, a two-field credential form and a UID
	// box, which is exactly the wrong trade — you want to watch the scene while the
	// timer runs. Same floatWin treatment as the panels above.
	timerWin, loginWin, pairPopWin floatWin
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
	// AO2 screen-effect overlays (effectspicker.go): the per-(origin, misc folder)
	// roster cache and its async resolve results, the theme-tier art loads, and the
	// dropdown's icon page cache. All bounded, all keyed so a server switch cannot
	// hand one origin's rosters to another; overlayRosterGen drops the lot when the
	// theme changes.
	overlayRosters map[string]*overlayRoster
	// overlayRosterInFlight is the in-flight marker set, kept SEPARATE from the
	// settled cache above. Two reasons, both structural: the cap-reset that keeps
	// overlayRosters bounded must never wipe a marker (dropping one lets the very
	// next ask spawn a SECOND goroutine for a key already being resolved, whose
	// two results then race and let the loser's stale paths win), and a bound on
	// CONCURRENT resolves (overlayRosterFetchCap) is only expressible when the
	// in-flight set is countable on its own.
	overlayRosterInFlight map[string]bool
	overlayRosterRes      chan overlayRosterFetch
	overlayRosterGen      uint64
	// overlayTheme is the APPLIED theme's immutable snapshot, published by
	// pollThemeApply. The roster resolve reads its ladder off-thread instead of
	// re-running theme.Load per resolve (spec §2.4: the theme-tier parse rides the
	// theme apply). *theme.Theme is read-only after Load — dirs + four parsed INIs,
	// no lazily-filled caches — so sharing it with the fetch goroutine is race-free.
	overlayTheme      *theme.Theme
	overlayTexRes     chan overlayTexLoad
	overlayTexLoading map[string]bool
	overlayIconPages  []*render.TexturePage
	overlayIconGen    uint64
	overlayIconAsk    []time.Time
	overlayThumbFn    func(idx int, r sdl.Rect)
	// overlayPreviewBase records the base the right-click effect PREVIEW opened the
	// shared sprite-preview box on ("" = the picker owns no box). It is the close
	// edge's ownership check: the box is pinned while the list is open, so it has to
	// be taken down when the list closes (spec §1.d) — but previewBase is one shared
	// slot, so only a box that is still OURS may be closed.
	overlayPreviewBase string
	// Per-frame memos. drawICInputRow asks "is there an effects roster" on EVERY
	// frame and the courtroom screen is zero-alloc-gated, so neither the cache key
	// (a concat), nor the char.ini URL (a concat), nor the choice slice (a copy +
	// a prepend) may be rebuilt per frame. All three are baked on a state change
	// and compared with plain string/pointer equality, which allocates nothing.
	// pollOverlayRosters and pollCharMeta invalidate them when their inputs land.
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
	// overlayIconKeys / overlayIconPaths are the per-ROW icon page key and (theme
	// tier only) the LOCAL file it loads from, baked beside overlayChoicesCache
	// when the roster changes — spec §3, "icon page keys are precomputed when the
	// list is built, not per frame". Load-bearing, not tidiness: dropdownEx paints
	// the CLOSED control's current pick through the same thumb painter on EVERY
	// frame (ui.go:3938-3942), so building the key inside the painter allocated on
	// every settled courtroom frame as soon as an effect was picked.
	overlayIconKeys  []string
	overlayIconPaths []string
	// overlayIconFolder is the misc folder those keys were baked for. An iniswap or
	// character swap into a DIFFERENT effects pack with the same row count would
	// otherwise leave the old pages in place — cachedPage keys by INDEX.
	overlayIconFolder string
	// overlayIconSize is courtroom_design.ini's `effects_icon_size` (AO2
	// courtroom.cpp:979). Zero = the theme is silent; the picker then sizes the
	// strip from the row height like every other thumbnail dropdown.
	overlayIconSize [2]int
	// #39 command palette (Ctrl+Space): open state, live query, keyboard selection.
	paletteOpen  bool
	paletteQuery string
	paletteSel   int
	// hkPrevDown is the hotkey sheet's own mouse-press edge: it draws over EVERY
	// screen (outside the courtroom box pass), so it can't share that pass's edge.
	hkPrevDown bool
	hkScroll   int32 // hotkey cheat-sheet scroll offset (it's a scrollable list now)
	// piecesWin / piecesPrevDown are the Hide-UI-pieces panel's floating window and
	// its own press edge. It used to be nailed to the bottom-right corner with no
	// way to move it — dragging did nothing and the layout editor did not offer it
	// — so a panel that landed awkwardly could not be shifted at all.
	piecesWin      floatWin
	piecesPrevDown bool
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
	splitWrapPct int
	splitWrapGen int
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
	// pinnedPass is true during a VIEW-ONLY drawCourtroom pass over a session that is
	// not the live one: the full-theme render of the pinned client, and the theme
	// editor's canned preview (themeeditorpreview.go). Single-consumer App channel
	// drains (pollCharINI) skip while it's set, so such a pass can't eat an async
	// result meant for the primary — the pinned client is already fully loaded in its
	// parked sessionState, and the preview's demo session never asks for one.
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
	// emoteGridDrewPreview latches "the sprite-preview box was already painted this
	// frame", so drawSpritePreviewFallback can stand in for the emote grid's draw
	// site on the passes where the grid did not run (panel hidden, no themed
	// `emotes` rect, char.ini still loading) without ever compositing the box twice.
	// Set by the two grid painters and by the fallback itself; cleared once per
	// frame by pollOverlayRosters, ahead of every screen draw.
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
	// Compared against previewBase every frame (only while detached — see
	// syncPreviewWindow) so a new pick re-feeds exactly once, and going
	// not-ready (still loading) clears it so the eventual ready flip feeds
	// too. Deliberately App-level, not a field on render.PreviewWindow
	// itself: it names a COMPARISON this package owns (previewBase is
	// internal/ui's own concept), not preview-window state.
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
	// feedDetachedPreviewFrame recomputes the current index every call
	// (cheap, pure — pageFrameLoop) but only does GPU work when it differs
	// from this, which is what lets a looping animation's readback+cache
	// stay keyed on ACTUAL frame changes instead of firing every call.
	previewWinFedFrame int
	// previewWinWasOpen is syncPreviewWindow's OWN falling-edge latch — "was
	// the detached window open as of last frame" — used ONLY to detect the
	// single frame it just closed (by its own titlebar X or the pop-out
	// toggle) so that frame's position/size gets persisted once. It is NOT a
	// second copy of "is it detached": every query of that goes straight to
	// a.d.Preview.IsOpen() (previewIsDetached), so there is nothing here for
	// the pop-out button to disagree with if the window closes itself.
	previewWinWasOpen bool
	vpExactBuf        string // edit buffer for the Settings exact-stage-width px field (commit on Enter)
	hidden            map[string]bool

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
	a.seedHiddenFromPrefs() // hidden-chrome set + the A6 no-strand normalization
	// Global jukebox library loads off-thread (it can be large); it lands via
	// pollJukebox so the keybinds and wardrobe tab have it ready.
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
	// Restore-on-launch (opt-in, OFF by default): queue the remembered tabs;
	// Frame reconnects them one per frame. Capped to the live tab cap so we
	// never queue a connect that allocateTab would reject. Off ⇒ nil queue ⇒
	// pumpTabRestore is a single length check, so boot is byte-identical.
	if d.Prefs.Load().RestoreTabs {
		q := d.Prefs.Load().OpenTabList()
		if cap := d.Prefs.TabCap(); len(q) > cap {
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
			a.autoConnectPending = true
		}
	}
	a.applyChromePreset(d.Prefs.ChromeTheme()) // #M3: apply the saved client chrome theme at launch
	a.refreshPartColors()                      // per-part layout tints: parse the saved hexes once
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
	} else if line := mutedChannelsBootNotice(a.masterMuted, a.musicMuted, a.sfxMuted, a.blipMuted); line != "" {
		// Silent-launch trap: the mute flags just seeded in resetSessionState
		// (above) are real and persisted, but the volume strip that shows a
		// muted channel (the red "muted" label) defaults HIDDEN
		// (VolStripOn OFF) — so a user who quit muted a week ago would get
		// total silence on this launch with no on-screen sign why. One line,
		// same corrupt-prefs mechanism, no new panel/modal/settings row.
		// `else`: the corrupt-prefs notice is rarer and more consequential
		// (you lost settings), so it wins the single warnLine slot.
		a.warnLine = line
		a.warnAt = time.Now()
	}
	// Warm the local-pack index at startup rather than waiting for the first
	// connect. The walk runs on a goroutine and takes real time on a large pack, so
	// kicking it here means it is usually already resident by the time the first
	// room's cold-load storm demands anything; starting it on connect would race
	// the character-icon prefetch and lose to the resident-texture short-circuit.
	// A no-op — not even a goroutine — when no mounts are configured.
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
//
// This only affects the auto path: an explicitly saved scale wins because the
// manual slider requires UIScaleAuto off, and UIScale() then ignores the DPI-
// derived value entirely (nothing here writes a preference — the seed is
// runtime-only, so moving to a new monitor keeps re-seeding). Render thread
// (touches the SDL window handle); call at boot and on a display change.
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
//
// WHAT IS AND IS NOT CRISP ACROSS THIS STEP (the "everything above 1345x840 goes
// blurry" report; the numbers are this function's arithmetic exactly —
// 1280x1.05 = 1344, 800x1.05 = 840, and UIScaleStepPercent is 5):
//
//   - TEXT is crisp, and re-rasterizes here. SetUIScale → SetTextDevScale reopens
//     every face at pt x scale and purges the label/width/raster caches, so glyphs
//     are rasterized at DEVICE size (#77 Part A). Getting the raster built at device
//     size is only half of it, though: the BLIT also has to stay device-exact
//     (MessageRaster.DrawScaled, gated on the renderer's scale matching the raster's
//     — see deviceExact's own doc for why the plain Draw resamples even a
//     device-rasterized texture). That bracket now reaches every live text/emoji
//     surface — the chatbox body, IC/OOC log lines and shownames (labelEmoji /
//     labelCoveringCentered), the text field's emoji-fallback branch, and the
//     floating reaction badges (Badge.Draw, render/badge.go) — not just the chatbox
//     body it started on; a source scan bans the plain Draw everywhere outside the
//     two export passes (which legitimately bracket their own scale), and the
//     raster-rebuild wiring is pinned at this exact threshold.
//   - RECT CHROME (panels, borders, bars) is resolution-independent: SDL rasterizes
//     a filled rect at the scaled coordinates, so there is no resample to soften —
//     only an edge that can land a pixel either way.
//   - WHAT WAS NOT: the faux-bold second draw pass. A 1 px LOGICAL offset is
//     multiplied by this scale, so the two copies of a bold name sat 1.05 device px
//     apart on different sub-pixel phases and read as a doubled smear beside message
//     text on the same row that stayed crisp. Fixed by rasterizing the weight into
//     the glyphs (Ctx.textTextureBold) — this was the one text surface still built
//     at one scale and drawn at another.
//   - WHAT STILL IS NOT, deliberately: IMAGE surfaces — character sprites,
//     backgrounds, theme pages, evidence and character icons. There is no
//     higher-resolution source to re-rasterize from; the UI scale is one more factor
//     in a stretch that already happens at 100% (a 256 px icon in a 40 px cell), so
//     "re-raster at device scale" is not a thing that can be done for them.
//   - WHAT STILL IS NOT, and IS worth doing (correcting this comment's earlier
//     verdict): the PROCEDURAL textures — colorwheel.go, shapemask.go, and the
//     theme GENERATOR TILES (gentex.go) — are built at logical size and stretched
//     through the ambient scale exactly like sprite art. The generator tiles are
//     NOT settings-only decoration: they back entire chatbox/panel/background skins
//     for shipped community-style themes (vaporwave, cyberpunk, danganronpa,
//     newspaper, …), so their softness is visible in ordinary themed-courtroom use,
//     not just a floating colour-picker disc. Left unfixed this pass — a device-px
//     rebake needs a rebuild-on-scale-change trigger (mirroring SetTextDevScale's
//     no-op-on-unchanged-scale idiom) plus re-clamping the existing hostile-parameter
//     DoS bound (theme.GenTileMaxPx / clampTilePx) through whatever multiplies the
//     target size, and that is a wider-blast-radius change than this pass's
//     text/badge fixes — recorded here, still, so the next person does not have to
//     re-derive either the mechanism or the risk.
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
		a.toolboxPieces, a.toolboxPinned = false, false
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

// charHoverIDPrefix namespaces the char-select grid's HoverPreview ids. The kit keys
// dwell state by id across the whole frame, so a bare character name could collide
// with any other widget id built from the same name (the pair menu, the wardrobe).
const charHoverIDPrefix = "char:"

// charHoverID is char i's cached HoverPreview id. The out-of-range arm rebuilds the
// string rather than indexing past the cache: every live caller runs behind
// ensureCharLower, but this is a DRAW loop, and paying one allocation on a path that
// should be unreachable beats panicking the client if it ever is reached.
func (a *App) charHoverID(i int) string {
	if i >= 0 && i < len(a.charHoverIDs) {
		return a.charHoverIDs[i]
	}
	if a.sess == nil || i < 0 || i >= len(a.sess.Chars) {
		return charHoverIDPrefix
	}
	return charHoverIDPrefix + a.sess.Chars[i].Name
}

// charSelectTitle is the char-select heading, rebuilt only when the server name
// changes. The concat used to run every frame on a screen that is otherwise pure
// integer layout.
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
	// never trips while minimized, so a silently-failed music fetch stays ducked-
	// silent forever; auto-reconnect and the resume-seek elapsed math (musicStartedAt,
	// stamped via pumpConnection→handleSessionEvents) drift the same way. Nothing
	// reachable here WANTS a stale clock — every consumer is a timer/animation clock
	// that is strictly more correct with real time — so restamp unconditionally, the
	// same real-time snapshot Frame takes.
	a.frameNow = time.Now()
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
	a.pollCharINI()    // drain the async char.ini result here too, so the emote list appears at idle=0 (a skipped courtroom frame never reaches the draw-time poll)
	a.pollLogBrowser() // same for the log browser's off-thread scope load (session list + log area) — else it stays blank at idle=0 until cursor motion
	a.pollUpdate()     // and the self-update result — else "Downloading…" never flips to "Restart to apply" at idle=0 until cursor motion
	if a.room != nil {
		a.room.Update(dt)
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
//
// The pacing censuses are draw-site censuses by design (Viewport.AmbientAnimating →
// NoteAnimating, and NextAnimDue's schedule), which means every one of them needs a
// drawn frame to notice anything. That is fine for something already on screen and
// wrong for the moment something STARTS: Background advances the room but never calls
// Viewport.Update, so the viewport is still bound to the previous stage, NextAnimDue
// still answers for it, and a parked event-driven loop sleeps out its whole idle period
// before the effect's first frame — an eternity at the 10 fps idle default, and forever
// at idle=off. Same shape as NotifyPreanimStarted: the thing that begins has to announce
// itself once, rather than being discovered a tick later.
//
// Scene.Overlay.Gen is exactly that announcement and costs one uint32 compare per
// Background pass: the courtroom bumps it on EVERY trigger (overlayfx.go), including a
// repeat of the same effect, which is the case a Base comparison would miss.
func (a *App) noteOverlayStart() {
	if gen := a.room.Scene.Overlay.Gen; gen != a.drawnOverlayGen {
		a.drawnOverlayGen = gen
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
	p := a.room.Phase()
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
// shapes and conflating them regressed (shipped a single demand
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
		//
		// Char sprites re-demand through the FULL courtroom.SpriteHealAlts chain
		// (bare, then folder — the same one healSpriteLayer and the primary
		// per-message demand walk via EmoteAlts), NOT a single-spelling Prefetch.
		// That is the missingno no-blip guarantee: a Warning (→ Store.MarkMissing
		// → a missingno flash) fires ONLY on FULL chain exhaustion, so a sprite
		// that exists ONLY under the bare or the "(a)/"-folder spelling must have
		// that spelling probed here too, or this warm's single prefixed Prefetch
		// would 404, report missing, and flash the placeholder over a sprite that
		// DOES exist. Routing char warms through the SAME chain the primary
		// resolve used makes "a char Warning fired" mean "every spelling, every
		// format, exhausted" — and keeps a heal pass from recording its OWN
		// conclusive-miss verdict on a shorter chain than the one that actually
		// resolved the sprite (missChainFor keys on exact chain identity). The
		// 404 cache absorbs the extra probes inside their TTL and the latch stops
		// the repeats. bg/desk/chatskin have no alt spelling, so they keep the
		// plain single Prefetch.
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

// --- connection lifecycle -------------------------------------------------------

// Connect dials a server in a NEW tab. Whatever was active parks and
// keeps running in the background (rehearsal disconnects instead — it
// can't background). At the tab cap the connect refuses with a visible
// reason and the current session stays untouched.
