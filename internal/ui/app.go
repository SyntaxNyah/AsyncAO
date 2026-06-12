package ui

import (
	"context"
	"fmt"
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
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// Screen identifies the active top-level view.
type Screen int

const (
	ScreenLobby Screen = iota
	ScreenCharSelect
	ScreenCourtroom
	ScreenSettings
	ScreenAbout
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
	selServer int
	descLines []string

	// --- connection / session ---
	conn       *protocol.Conn
	sess       *courtroom.Session
	room       *courtroom.Courtroom
	urls       courtroom.URLBuilder
	serverName string
	serverKey  string // ws URL: keys the per-server warm state in prefs
	connErr    string

	// --- char select state ---
	charSearch  string
	charScroll  int32
	charTab     int // charTabServer | charTabWardrobe (grid contents swap)
	previewBase string
	// iconAsk[i] is when char i's icon was last demanded by the visible
	// grid (bounded by the server's char list length); iconAskBudget is
	// the per-frame submission allowance, reset each Frame.
	iconAsk       []time.Time
	iconAskBudget int
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

	// --- courtroom chrome state ---
	icInput    string
	oocInput   string
	oocName    string
	sidePref   string    // OUR side (char.ini default, /pos override)
	lastPing   time.Time // CH keepalive pacing
	lastICSend time.Time // chat_ratelimit window
	iniWarmed  string    // last char.ini hover-warmed (dedupe)
	icColor    int       // outgoing MS text_color (swatch cycler)
	// pair offset edit buffers (typed text commits on valid parse)
	pairOffXText, pairOffYText string
	emotes                     []courtroom.Emote
	emoteIdx                   int
	charBlips                  string // char.ini blips/gender (outgoing default)
	// 2.10 custom shouts ([Shouts] in char.ini): customIdx −1 = the base
	// "custom" art, ≥ 0 indexes customShouts.
	customShouts []courtroom.CustomShout
	customIdx    int
	customName   string
	charINIBusy  bool
	charINIres   chan charINIFetch
	icLog        []icEntry
	icScroll     int32
	logSearch    string
	oocLog       []string
	musicScroll  int32
	areaScroll   int32
	logTab       int
	// emoteAsk[i] paces demand for emote i's button art (drawEmoteRow).
	emoteAsk []time.Time

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
	manifestFor string // origin already fetched this session (dedupe)

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

	// --- court extras (HP / WTCE / timers / judge / modcall / evidence) ---
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
	showUICfg   bool        // hide-chrome popup
	hidden      map[string]bool
	// evShow is the incoming presented-evidence pop-up (display_evidence_image).
	evShowImg string
	evShowAt  time.Time

	// --- wardrobe / iniswap (client favourites + server iniswap.txt) ---
	iniChar     string   // active override folder ("" = picked character)
	pendingIni  string   // wear this once PV confirms (char-select joins)
	iniServer   []string // the server's iniswap.txt names (may be empty)
	iniList     []string // merged menu: wardrobe first, then server extras
	iniWardrobe []bool   // parallel to iniList: wardrobe membership (star)
	iniLower    []string // lowercased names for the search filter
	iniListErr  string
	iniBusy     bool
	iniRes      chan iniswapFetch
	showIni     bool
	iniSearch   string
	iniAdd      string // "add folder to wardrobe" input
	iniScroll   int32
	iniAsk      []time.Time // demand pacing stamps, parallel to iniList

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

	// layout scales (percent; mirrors prefs, saved on change)
	vpPct, chatPct, boxPct, logPct, inputPct int
	uiScalePct                               int // global renderer scale
	// chat raster invalidation extras (text/color tracked separately)
	rasterScale   int
	rasterW       int32
	rasterSkinned bool // theme skin gates theme text colors (readability)
	oocScroll     int32

	// pairing panel
	pairSearch string
	pairScroll int32
	pairWith   int
	pairOrder  int
	pairOffX   int
	pairOffY   int
	pairFlip   bool
	showPair   bool
	msRaster   *render.MessageRaster
	rasterText string

	// last-applied scene text color (raster invalidation)
	rasterColor int
}

type lobbyFetch struct {
	entries []network.ServerEntry
	err     error
}

type iniswapFetch struct {
	names []string
	err   error
}

type charINIFetch struct {
	ini *courtroom.CharINI
	err error
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

	// diagnostics: where the skin came from, how many INI keys loaded,
	// and the dirs probed (so "nothing found" names the actual paths).
	chatboxFile string
	chatboxDir  string
	iniKeys     int
	probed      []string
}

// themeStemChatbox is the chatbox skin's stem in themeTex / T1.
const themeStemChatbox = "chatbox"

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
	}
	for i := 0; i <= courtroom.HPBarMax; i++ {
		d := "defensebar" + strconv.Itoa(i)
		p := "prosecutionbar" + strconv.Itoa(i)
		m[d], m[p] = []string{d}, []string{p}
	}
	return m
}

// themeTexKey is the T1 key for a theme texture stem; the scheme prefix can
// never collide with real asset URLs.
func themeTexKey(stem string) string { return "theme://" + stem }

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
		ctx:         ctx,
		d:           d,
		screen:      ScreenLobby,
		lobbyResult: make(chan lobbyFetch, 1),
		charINIres:  make(chan charINIFetch, 1),
		iniRes:      make(chan iniswapFetch, 1),
		manifestRes: make(chan manifestFetch, 1),
		pairWith:    protocol.UnpairedCharID,
		oocName:     d.Prefs.SavedShowname(),
		selServer:   -1,
		spriteOv:    map[string][2]int{},
		themeTex:    map[string]bool{},
		hidden:      map[string]bool{},
		evidIdx:     -1,
		hpPrev:      [2]int{courtroom.HPBarMax, courtroom.HPBarMax},
	}
	for _, id := range d.Prefs.HiddenPanels() {
		a.hidden[id] = true
	}
	a.applyThemeAsync() // chatbox skin + font colors from the saved theme
	a.pairOffX, a.pairOffY = d.Prefs.PairOffsets()
	a.pairFlip = d.Prefs.PairFlipped()
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = d.Prefs.LayoutScales()
	a.uiScalePct = d.Prefs.UIScale()
	ctx.SetUIScale(a.uiScalePct)
	if saved := d.Prefs.SavedOOCName(); saved != "" {
		a.oocName = saved
	}
	a.RefreshServers()
	return a
}

// UIScale exposes the global scale percent (main sets the renderer scale
// from it each frame and sizes the logical canvas accordingly).
func (a *App) UIScale() int { return a.uiScalePct }

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

// Connect dials a server and resets session state.
func (a *App) Connect(name, wsURL string) {
	a.Disconnect()
	a.serverName = name
	a.serverKey = wsURL
	a.connErr = ""
	conn, err := protocol.Dial(context.Background(), wsURL)
	if err != nil {
		a.connErr = err.Error()
		return
	}
	a.conn = conn
	a.lastPing = time.Now()
	a.sess = courtroom.NewSession(func(p protocol.Packet) error {
		return conn.Send(context.Background(), p)
	}, hdid())
	a.screen = ScreenCharSelect
	a.icLog = a.icLog[:0]
	a.oocLog = a.oocLog[:0]
}

// Disconnect tears the connection down and returns to the lobby.
func (a *App) Disconnect() {
	if a.conn != nil {
		a.conn.Close()
		a.conn = nil
	}
	a.sess = nil
	a.room = nil
	a.emotes = nil
	a.iconAsk = nil
	a.emoteAsk = nil
	a.charLower = nil
	// Server-side iniswap state resets per server (the wardrobe is global
	// and persists); drain any in-flight fetch so a stale txt can't land
	// after a reconnect elsewhere.
	a.iniChar, a.pendingIni = "", ""
	a.iniServer, a.iniList, a.iniWardrobe, a.iniLower, a.iniAsk = nil, nil, nil, nil, nil
	a.spriteOv = map[string][2]int{} // drag overrides are per-server
	a.dragName = ""
	a.selServer, a.descLines = -1, nil
	a.iniListErr, a.iniSearch, a.iniAdd = "", "", ""
	a.showIni, a.iniBusy, a.iniScroll = false, false, 0
	a.charTab = charTabServer
	select {
	case <-a.iniRes:
	default:
	}
	a.manifestFor = ""   // next connect re-checks its manifest
	a.d.Pool.BumpEpoch() // cancel queued speculation for the old server
	a.screen = ScreenLobby
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
				a.pushIC(icLogLine(ev.Message), ev.Message.TextColor)
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

func icLogLine(m *protocol.ChatMessage) string {
	name := m.Showname
	if name == "" {
		name = m.CharName
	}
	return name + ": " + m.Message
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
	if host := hostOfURL(origin); host != "" {
		a.d.Client.PreResolve(context.Background(), strings.Split(host, ":")[0])
	}
	a.fetchManifestAsync()
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
	a.sess.PickCharacter(free)
}

// openIniswap shows the wardrobe menu (courtroom modal).
func (a *App) openIniswap() {
	a.showIni = true
	a.ensureIniList()
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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		data, err := a.d.Manager.FetchRaw(ctx, url)
		if err != nil {
			a.iniRes <- iniswapFetch{err: err}
			return
		}
		a.iniRes <- iniswapFetch{names: parseIniswapList(data)}
	}()
}

func (a *App) pollIniswap() {
	select {
	case res := <-a.iniRes:
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
	names, fromWardrobe := mergeWardrobe(a.d.Prefs.WardrobeList(), a.iniServer)
	a.iniList = names
	a.iniWardrobe = fromWardrobe
	a.iniLower = make([]string, len(names))
	for i, n := range names {
		a.iniLower[i] = strings.ToLower(n)
	}
	a.iniAsk = nil
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

// loadCharINI fetches the ACTIVE character's char.ini for the emote list
// (the iniswap override when set).
func (a *App) loadCharINI() {
	name := a.activeCharName()
	if a.sess == nil || name == "" {
		return
	}
	url := a.charINIURL(name)
	a.charINIBusy = true
	go func() {
		data, err := a.d.Manager.FetchRaw(context.Background(), url)
		if err != nil {
			a.charINIres <- charINIFetch{err: err}
			return
		}
		ini, err := courtroom.ParseCharINI(data)
		a.charINIres <- charINIFetch{ini: ini, err: err}
	}()
}

func (a *App) pollCharINI() {
	select {
	case res := <-a.charINIres:
		a.charINIBusy = false
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
	a.pumpConnection()
	a.drainWarnings()
	a.pollThemeApply()
	a.pollManifest()
	a.iconAskBudget = charIconAskPerFrame // shared demand budget (icons, emote buttons)
	if a.room != nil {
		a.healScenery()
		a.room.Update(dt)
		a.applySpriteOverrides()
		a.d.Viewport.Update(&a.room.Scene, dt)
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
	}
	// Debug overlay paints over every screen (allocs are acceptable here:
	// it's an opt-in diagnostics path, never on by default).
	if a.d.Prefs.DebugOverlayEnabled() {
		a.drawDebugOverlay(winW, winH)
	}
}

// applyThemeAsync loads the selected theme's visible pieces off-thread —
// the chatbox skin (chatbox.webp/png in the theme dir, AO2 convention)
// and the message/showname font colors — and publishes them to the render
// thread via themeRes. Settings re-triggers it on every theme change.
func (a *App) applyThemeAsync() {
	name, dir := a.d.Prefs.Theme()
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
			for stem, candidates := range themeImageStems() {
				for _, cand := range candidates {
					path, ok := t.FindAsset(cand, themeImageExts)
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
					break
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
	for stem := range themeImageStems() {
		key := themeTexKey(stem)
		if d := res.images[stem]; d != nil {
			if err := a.d.Store.Upload(key, d); err == nil {
				a.themeTex[stem] = true
			}
		} else if a.themeTex[stem] {
			a.d.Store.Remove(key)
			delete(a.themeTex, stem)
		}
	}
	a.themeChatbox = a.themeTex[themeStemChatbox]
	a.themeSounds = res.sounds
	a.pushRealizationToRoom()
	a.themeMsgCol, a.themeHasMsg = res.msgCol, res.hasMsg
	a.themeNameCol, a.themeHasName = res.nameCol, res.hasName
	a.rasterText = "" // re-raster the current message with theme colors
	line := themeApplySummary(res)
	settings.statusLine = clampLine(line)
	a.pushDebug(line)
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

// themePage fetches a resident theme texture page, healing on eviction.
func (a *App) themePage(stem string) (*render.TexturePage, bool) {
	if !a.themeTex[stem] {
		return nil, false
	}
	page, ok := a.d.Store.Get(themeTexKey(stem))
	if !ok || len(page.Frames) == 0 {
		a.healTheme()
		return nil, false
	}
	return page, true
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
	text  string
	color int
}

func (a *App) pushIC(line string, color int) {
	a.icLog = append(a.icLog, icEntry{text: clampLine(line), color: color})
	if len(a.icLog) > icLogCap {
		copy(a.icLog, a.icLog[len(a.icLog)-icLogCap:])
		a.icLog = a.icLog[:icLogCap]
	}
}

func (a *App) pushOOC(line string) {
	a.oocLog = appendCapped(a.oocLog, clampLine(line), icLogCap)
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
