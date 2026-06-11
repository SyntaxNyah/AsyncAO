package ui

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
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
	icLogCap            = 256

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
	selectedDesc  string

	// --- connection / session ---
	conn       *protocol.Conn
	sess       *courtroom.Session
	room       *courtroom.Courtroom
	urls       courtroom.URLBuilder
	serverName string
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

	// --- courtroom chrome state ---
	icInput    string
	oocInput   string
	oocName    string
	sidePref   string    // OUR side (char.ini default, /pos override)
	lastPing   time.Time // CH keepalive pacing
	lastICSend time.Time // chat_ratelimit window
	// pair offset edit buffers (typed text commits on valid parse)
	pairOffXText, pairOffYText string
	emotes                     []courtroom.Emote
	emoteIdx                   int
	charINIBusy                bool
	charINIres                 chan charINIFetch
	icLog                      []string
	oocLog                     []string
	musicScroll                int32
	areaScroll                 int32
	logTab                     int
	// emoteAsk[i] paces demand for emote i's button art (drawEmoteRow).
	emoteAsk []time.Time

	// last missing-asset warning surfaced to the user (spec §4).
	warnLine string
	warnAt   time.Time

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

	// layout scales (percent; mirrors prefs, saved on change)
	vpPct, chatPct, boxPct, logPct, inputPct int
	// chat raster invalidation extras (text/color tracked separately)
	rasterScale int
	rasterW     int32
	oocScroll   int32

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

// NewApp builds the UI over deps.
func NewApp(ctx *Ctx, d Deps) *App {
	a := &App{
		ctx:         ctx,
		d:           d,
		screen:      ScreenLobby,
		lobbyResult: make(chan lobbyFetch, 1),
		charINIres:  make(chan charINIFetch, 1),
		iniRes:      make(chan iniswapFetch, 1),
		pairWith:    protocol.UnpairedCharID,
		oocName:     d.Prefs.SavedShowname(),
	}
	a.pairOffX, a.pairOffY = d.Prefs.PairOffsets()
	a.pairFlip = d.Prefs.PairFlipped()
	a.vpPct, a.chatPct, a.boxPct, a.logPct, a.inputPct = d.Prefs.LayoutScales()
	if saved := d.Prefs.SavedOOCName(); saved != "" {
		a.oocName = saved
	}
	a.RefreshServers()
	return a
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
	a.iniListErr, a.iniSearch, a.iniAdd = "", "", ""
	a.showIni, a.iniBusy, a.iniScroll = false, false, 0
	a.charTab = charTabServer
	select {
	case <-a.iniRes:
	default:
	}
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
				a.Disconnect()
				return
			}
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
		case courtroom.EventCharsUpdated:
			a.charLower = nil // names may have changed; rebuild lazily
			// icons refresh lazily as textures land
		case courtroom.EventCharPicked:
			a.enterCourtroom()
		case courtroom.EventOOC:
			a.pushOOC(ev.Name + ": " + ev.Text)
		case courtroom.EventMessage:
			if ev.Message != nil {
				a.pushIC(icLogLine(ev.Message))
			}
		case courtroom.EventDisconnect:
			a.connErr = ev.Text
			a.Disconnect()
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
	a.room.Predictor = assets.NewPrefetcher(a.d.Manager, func(character string) string {
		return urls.Emote(character, "normal", courtroom.EmoteIdle)
	})
	a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
	if a.sess.Background != "" {
		a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: a.sess.Background})
	}
	a.applyTimingToRoom()
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

// loadCharINI fetches the ACTIVE character's char.ini for the emote list
// (the iniswap override when set).
func (a *App) loadCharINI() {
	name := a.activeCharName()
	if a.sess == nil || name == "" {
		return
	}
	url := a.urls.Origin() + "characters/" + strings.ToLower(name) + "/char.ini"
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
	a.iconAskBudget = charIconAskPerFrame // shared demand budget (icons, emote buttons)
	if a.room != nil {
		a.healScenery()
		a.room.Update(dt)
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
		default:
			return
		}
	}
}

// warnActive reports whether the warning banner should still draw.
func (a *App) warnActive() bool {
	return a.warnLine != "" && time.Since(a.warnAt) < warnShowDuration
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

func (a *App) pushIC(line string) {
	a.icLog = appendCapped(a.icLog, clampLine(line), icLogCap)
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
