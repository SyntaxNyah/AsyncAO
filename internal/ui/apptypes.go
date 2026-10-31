package ui

import (
	"image"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

type sessionState struct {
	// --- connection / session ---
	conn *protocol.Conn
	sess *courtroom.Session
	room *courtroom.Courtroom
	urls courtroom.URLBuilder
	// CM/mod dashboard: per-server software override (SoftwareUnknown = auto-detect from the ID packet).
	cmSoftwareOverride courtroom.ServerSoftware
	// autoLoginTried makes the auto-login fire once per session; some servers re-send DONE mid-session.
	autoLoginTried bool
	// voiceJoined = we sent VS_JOIN; voiceMicOn = our speak state is on (announced via VS_SPEAK).
	voiceJoined bool
	voiceMicOn  bool
	// voiceCapsAnnounced: the one-time "this area supports voice" OOC hint was posted this session.
	voiceCapsAnnounced bool
	showModDash        bool // the mod (ban/kick) dashboard panel is open
	showCMPanel        bool // the separate CM (area control) panel is open
	// Debug panel (Extras → Debug): packet inspector, perf and log sections.
	showDebugPanel bool
	debugSection   int         // active tab: 0 Session · 1 Packets · 2 Perf · 3 Cache · 4 Log · 5 Fonts
	debugPktScroll int32       // Packets list scroll offset
	debugLogScroll int32       // Log list scroll offset
	debugPktBuf    []packetRec // reused recent()-into buffer (alloc-free draw)
	// Fonts tab: resolved font/colour for recent IC log rows; debugFontBuf is built only while the tab is open.
	debugFontScroll int32
	debugFontBuf    []fontPickRow
	// The FX button opens a floating effect list.
	showFxPicker bool
	// icSwatchRect is where the IC colour swatch drew this frame; the colour wheel anchors to it.
	showICColorWheel bool
	icSwatchRect     sdl.Rect
	icColorHexBuf    string
	fxBtnRect        sdl.Rect // last-drawn FX button rect (the picker anchors above it + the fence finds it)
	// The Settings wheel's selected layout part and its hex-field edit buffer.
	partEditSlot int
	partHexBuf   string
	// showPowerUser reveals the advanced Settings options (TLS, Asset Origin, asset casing); session-only.
	showPowerUser bool
	// powerNukeArm is the two-click confirm on "Reset all power-user options": first click arms, second fires.
	powerNukeArm bool
	// lastInputAt is the most recent SDL input event (frame pacing holds full rate after it).
	lastInputAt time.Time
	// lastMotionAt is the most recent bare pointer motion; it holds full rate only through motionInputGrace.
	lastMotionAt time.Time
	// lastFrameDrawn is when Frame last rendered; both loops measure idle cadence from it.
	lastFrameDrawn time.Time
	// uiDirty marks UI-visible state changed since the last drawn frame. Render thread only; Frame clears it.
	uiDirty bool
	// drawnGen is the texture-store generation at the last drawn frame; a mismatch means a redraw is due.
	drawnGen uint64
	// drawnCaretOn is the caret blink state at the last drawn frame; a flip schedules a redraw.
	drawnCaretOn bool
	// drawnOverlayGen is Scene.Overlay.Gen at the last frame that drew the stage or saw the trigger from Background.
	// A screen effect armed while the loop is parked has no draw site, so the Gen bump wakes it.
	drawnOverlayGen uint32
	// drawnScreen is the screen at the last Frame; noteScreenTransition compares against it to close hover-preview
	// state on a screen switch. The same edge detects lobby entry for the master-list auto-refresh.
	drawnScreen Screen
	// frameAnimChrome is set via NoteAnimating when a draw pass renders a self-driven timed UI animation outside the
	// viewport's scheduler. drawnAnimChrome is the last frame's value: while true, SkipFrame and FramePace use the
	// active cap.
	frameAnimChrome bool
	drawnAnimChrome bool
	// lastPacerTier records which FramePace branch set the last budget — a
	// lastPacerTier records which FramePace branch set the last budget; diagnostic only, never read by pacing.
	lastPacerTier pacerTier
	// roomPreAdvanced is set by the audio-paced sub-stepping in the main loop: the room is already current, so the
	// next Frame skips room.Update and clears it.
	roomPreAdvanced bool
	// lastSpeakerDiagBase is the speaker sprite base last written to the debug log, so it logs once per change.
	lastSpeakerDiagBase string
	// frameDemandPending is set when a draw pass leaves a demand-streamed emote cell blank; drawnDemandPending is the
	// last frame's value and makes NextWakeDelay schedule a re-render so demandAsset keeps asking. A batch of
	// 404s uploads nothing and bumps no generation, so nothing else would wake it.
	frameDemandPending bool
	drawnDemandPending bool
	// sceneWarmLastDemand throttles keepSceneAssetsWarm's heal to one pool job per sceneWarmRedemandEvery.
	sceneWarmLastDemand time.Time
	// Warm-keeper heal history per base (warmMaxHealChurns/warmMaxHealAsks), reset when its warm set changes or a
	// new ceremony begins. Bounded by the sets; lazily allocated.
	sceneWarmDemands map[string]sceneHealState
	sceneWarmSet     [6]string // the warm set sceneWarmDemands counts against
	// sceneWarmPhase is the room's message phase; an idle to ceremony transition resets the heal budget.
	sceneWarmPhase    courtroom.MessagePhase
	activeWarmDemands map[string]int
	activeWarmChar    string // active-warm identity: character folder…
	activeWarmIdx     int    // …and selected emote index
	// activeWarmLastDemand throttles keepActiveAssetsWarm's re-demands to the scene keeper's cadence.
	activeWarmLastDemand time.Time
	// winW/winH are the logical window size Frame was last given, for deep draw helpers that need bounds.
	winW, winH int32
	amICMNow   bool // cached amICM() — refreshed on ARUP/PU events, read per-frame by the
	// corner badge; the CM column lives in AreaInfo (ARUP), so a roster-stamp memo would miss a /cm.
	modDashTargetUID string // selected target's UID ("" = none) — keyed by UID, never a roster
	// index: rebuildLiveRoster replaces the slice on every join/leave, so an index could repoint a ban.
	// Ban/Kick box: a frozen snapshot of the target taken when the box opens, so a roster rebuild can't repoint a
	// destructive command. Only the IPID may fill in later (re-resolved by the frozen UID).
	banBoxKind      int                   // 0 = closed, 1 = ban, 2 = kick, 3 = bulk ban, 4 = bulk kick
	banBoxUID       string                // snapshot: target UID (the identity anchor)
	banBoxIPID      string                // snapshot: target IPID (mod-only; "" until a /getarea enrich)
	banBoxName      string                // snapshot: display name for the box header
	banBoxDur       courtroom.BanDuration // chosen duration (ban only)
	banBoxCustomDur string                // a saved CUSTOM duration chip's canonical token ("45m"); "" = the enum preset banBoxDur applies
	banBoxReason    string                // typed reason
	modDashScroll   int32                 // mod dashboard roster scroll offset
	cmRosterScroll  int32                 // CM panel roster scroll offset
	// Mod dashboard audit log, bulk targeting and left-column view switch; all per tab.
	modDashShowAudit bool            // left column shows the session audit log instead of the roster
	modAudit         []modAuditEntry // bounded, session-scoped record of commands sent from the dash
	modAuditScroll   int32           // audit list scroll offset
	modDashSelected  map[string]bool // UIDs ticked for a bulk ban / kick (lazily inited; ≤ modBulkCap)
	bulkBoxUIDs      []string        // frozen snapshot of the ticked UIDs when a bulk box opens
	modTemplatesEdit bool            // the ban box's reason-template editor is open (× removes a chip)
	modDurEdit       bool            // the ban box's custom-duration editor is open (× removes a chip)
	modDurInput      string          // the "add custom duration" field's draft ("45m", "2 days", …)
	serverName       string
	serverKey        string // ws URL: keys the per-server warm state in prefs
	// logSession is this tab's transcript identity, a process-unique ordinal minted by resetSessionState, so two tabs
	// on one server log to separate files. It lives here so it follows the tab through park/activate.
	logSession   int
	connAt       time.Time // session start (Rich Presence elapsed timer)
	curArea      string    // last area WE clicked (Rich Presence, best-effort)
	presenceInit bool      // false until the first lobby presence push (so "Playing AsyncAO" shows on launch, not only in-court)
	// Per-area IC scrollback (opt-in): areaLogs holds each visited area's icLog; areaLogOrder is the visit order
	// for FIFO eviction (areaLogCacheMax).
	areaLogs     map[string][]icEntry
	areaLogOrder []string
	lastICSend   time.Time // chat_ratelimit window
	// manifestFor: Settings writes "" to force fetchManifestAsync to re-fetch the session origin's extensions.json.
	manifestFor string
	// manifestSeeded holds origins whose extensions.json was already fetched and seeded this session, so each is
	// seeded once. Bounded by the distinct asset origins visited.
	manifestSeeded map[string]struct{}
	// manifestStatus records per asset host how the extensions.json probe ended; Settings → Formats reads it.
	// Render thread only, same bound as manifestSeeded.
	manifestStatus map[string]seedStatus
	// themeBound is this server's bound theme ("" = the global pick);
	// themeBound is this server's bound theme ("" = the global pick); it wins in applyThemeAsync.
	themeBound string
	// rehearsal marks the offline cached-asset browser; the manager's network access is closed while set, so it never
	// parks.
	rehearsal bool

	// --- char select ---
	charSearch string
	charScroll int32
	charTab    int // charTabServer | charTabWardrobe (grid contents swap)
	// charShowTaken is AO2's char_taken filter checkbox (charselect.cpp:99 seeds it checked; :360-364 hides taken
	// characters when cleared). Not persisted. Only the themed canvas draws it, so classic passes true.
	charShowTaken bool
	// charListSel is the roster index highlighted in the themed name column, -1 for none; it outlines the matching
	// grid cell. Reset on a tab switch because the tabs index different slices.
	charListSel int
	// charListScroll is the name column's own scroll offset.
	charListScroll int32
	// wardrobeMembers is the lowercased wardrobe set for the current server, rebuilt only when the server or
	// wardrobe generation changes (ensureWardrobeMembers).
	wardrobeMembers    map[string]bool
	wardrobeMembersFor string // serverKey the set was built for
	wardrobeMembersGen uint64 // config.WardrobeGeneration() it was built at
	// Lowered-query memos for the per-frame search filters.
	charQ loweredCache
	pairQ loweredCache
	iniQ  loweredCache
	// iconAsk[i] is when char i's icon was last demanded by the visible grid.
	iconAsk []time.Time
	// charLower caches lowercased char names for the search filter; invalidated on EventCharsUpdated.
	charLower []string
	// charHoverIDs caches the grid's per-cell HoverPreview id ("char:"+name); built and invalidated with charLower.
	charHoverIDs []string
	// charSelTitle caches the char-select heading; charSelTitleFor is the serverName it was built for.
	charSelTitle    string
	charSelTitleFor string
	// Generation-keyed texture page caches: while the store generation is unchanged, redraws do no LRU lookups.
	iconPages    []*render.TexturePage
	iconPagesGen uint64
	iniPages     []*render.TexturePage
	iniPagesGen  uint64
	// Emote button art (off/on) uses the same generation-keyed cache.
	emoteBtnOff    []*render.TexturePage
	emoteBtnOffGen uint64
	emoteBtnOn     []*render.TexturePage
	emoteBtnOnGen  uint64
	// emoteIconPages is the active character's icon, drawn behind the emote label when button art is absent.
	// Nulled on a character change.
	emoteIconPages []*render.TexturePage
	emoteIconGen   uint64
	emoteIconAsk   []time.Time
	// Emoji picker: emojiBtnRect anchors the overlay; emojiFenceOn tracks that we set the modal fence, so it is
	// released on close (modalOn persists across frames).
	showEmojiPicker bool
	emojiFenceOn    bool
	emojiBtnRect    sdl.Rect

	// Reactions: reactionFloats is the bounded ring of floating emoji; reactBadges caches one fadeable texture per
	// palette index; reactRect is reused draw scratch (a fresh local would heap-escape through cgo).
	// reactBadgeDevScale is the ctx.textDevPct the badges were built at; ensureReactBadge purges the map on a
	// mismatch. It lives here with reactBadges so the two invalidate together.
	reactionFloats     []reactionFloat
	reactBadges        map[uint8]*render.Badge
	reactBadgeDevScale int32
	reactRect          sdl.Rect
	reactSpawnSeq      int32
	// React picker: reactFenceOn tracks our modal fence; reactTarget{Ref,Name} is the message snapshotted when the
	// palette opened; pendingReact rides the next IC send; lastReact{Ref,Name} is the latest IC message.
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
	// icPendingSent is icInput at send time. The box clears only when the server echoes our message
	// (CHAR_ID == m_cid, courtroom.cpp handle_chatmessage); servers may swallow an MS sent inside a delay window.
	icPendingSent string
	// oocInput is shared by every OOC field.
	oocInput string
	// oocName is this tab's OOC name, seeded from the saved default in resetSessionState; only Settings writes the
	// default.
	oocName string
	// IC recall: Up/Down walks sent messages; icRecallIdx -1 = editing the live draft, which icRecallDraft stashes.
	icSentHist    []string
	icRecallIdx   int
	icRecallDraft string
	// OOC recall: the same walk for every OOC input (they share oocInput), with its own history separate from IC.
	oocSentHist    []string
	oocRecallIdx   int
	oocRecallDraft string
	// Friends-tab PM composer; sends "/pm <their uid> <message>" over OOC.
	pmTarget string
	pmInput  string
	// pmThreads is the per-partner PM history keyed by pmThreadKey. Incoming lines are best-effort parsed from OOC
	// (detectIncomingPM); a miss stays in the OOC log. Session-scoped and bounded.
	pmThreads map[string][]pmLine
	// shownameOverride is the in-courtroom showname box; when non-blank it wins over the saved showname for this
	// session.
	shownameOverride string
	sidePref         string // OUR side (char.ini default, /pos override)
	// lastSentStyle is the sprite style we last transmitted. The style marker is appended only when the current style
	// differs (EncodeChangeMarker); the zero value means nothing sent yet.
	lastSentStyle courtroom.SpriteStyle
	// lastSentProfile is the cross-client character profile we last transmitted; it is sent on change like
	// lastSentStyle.
	lastSentProfile courtroom.WireProfile
	// myStatus is our chosen presence flag; lastSentStatus is what we last transmitted. A fresh connect starts at
	// None.
	myStatus       courtroom.Status
	lastSentStatus courtroom.Status
	iniWarmed      string // last char.ini hover-warmed (dedupe)
	icColor        int    // outgoing MS text_color (dropdown)
	// icExtColor selects an extended AsyncAO colour, 1-based into render.ExtColorAt; 0 = none. Mutually exclusive
	// with the wire palette, Rainbow and Random.
	icExtColor int
	// icCustomOn/icCustomRGB: the free hex chat colour, sent as inline `\c#RRGGBB` markup with a nearest-standard
	// wire fallback (funColor). The last pick also persists globally (Prefs.ICCustomColorRGB seeds a fresh tab).
	icCustomOn  bool
	icCustomRGB int
	// icColorSel freezes the IC field's selection so the colour dropdown can wrap it (AO2 on_text_color_changed).
	// captureICColorSel snapshots it every frame before the dropdown draws, because the dropdown's open-click
	// unfocuses the field and clears the live selection before the pick lands. applyICColorPick consumes it.
	icColorSel  icColorSel
	icImmediate bool  // MS Immediate: preanim plays without holding the text (session toggle)
	icPreanim   bool  // "Pre" toggle: send the selected emote's preanimation (session toggle; AUTO-FOLLOWS each emote pick — set when the new emote defines a usable preanim, mirroring AO2-Client ui_pre; the user can override until the next pick). Seeded true in resetSessionState so an untouched fresh join reproduces today's always-play-preanim behavior.
	icAdditive  bool  // #14 2.8 ADDITIVE: this message appends to your last (session toggle; shown only when the server advertises additive + the pref is on)
	icEffect    uint8 // #M5 sticky Text FX (courtroom.TextEffect*); 0 = off. Wraps every message you send.
	// icSlide is AO2's ui_slide: the next message slides the character into position. AO2 resets it after each send
	// (courtroom.cpp:2362); the send site does that, not this field.
	icSlide bool
	// icRealize / icShake are AO2's realization_state / screenshake_state: one-shot arms that ride the next message.
	// AO2 clears them when the packet is built, beside objection_state (courtroom.cpp:2327-2329).
	icRealize bool
	icShake   bool
	// icShout is AO2's objection_state (courtroom.cpp:6048-6127), the armed interjection riding the next message:
	// 0 none, 1 Hold It!, 2 Objection!, 3 Take That!, 4 custom (protocol.Shout*). One int keeps the buttons
	// mutually exclusive. Cleared beside icRealize/icShake when the packet is built (:2327).
	icShout int
	// modcallGuard is AO2's ui_guard: silences the modcall alert for this session (courtroom.cpp:316-319). It is a
	// per-session mute, not a preference. The OOC line and the auto-clip still land.
	modcallGuard bool
	// Pair placement is per tab, seeded from prefs in resetSessionState. pairOffXText/Y are the typed edit buffers
	// (commit on a valid parse).
	pairOffX, pairOffY         int
	pairFlip                   bool
	pairOffXText, pairOffYText string
	// Pair-preview background cache (drawPairGhost); pairBgKey invalidates it when the bg or position changes.
	pairBgPages []*render.TexturePage
	pairBgGen   uint64
	pairBgKey   string
	// Pos-dropdown thumbnail cache (drawPosSelect): one bg thumbnail per row; posBgKey (the bg name) invalidates the
	// set.
	posBgPages []*render.TexturePage
	posBgGen   uint64
	posBgKey   string
	posBgAsk   []time.Time               // demandAsset pacing for the pos-dropdown thumbnails
	posThumbFn func(idx int, r sdl.Rect) // stable (one-time) pos-thumbnail draw fn — avoids a per-frame closure alloc on the hot Pos selector
	emotes     []courtroom.Emote
	emoteIdx   int
	// Emote-grid scroll position (emotescroll.go owns every derived answer). emoteRow is the first visible row, not
	// a page. cols/rowsPerPage/totalRows are stamped by whichever grid drew last (classic or themed).
	emoteRow         int
	emoteCols        int
	emoteRowsPerPage int
	emoteTotalRows   int
	emotePerPage     int // cols*rowsPerPage, kept as its own field: several sites ask "is a grid laid out yet?"
	// SFX picker: the chosen sound overrides the character's emote sound on every send until set back to "auto".
	// sfxChoices[0] = "SFX: auto", then the character's distinct emote sounds. Rebuilt by ensureSFXChoices when
	// the character or emote count changes, tracked as two plain fields: a concatenated key would allocate per frame.
	sfxChoiceIdx       int
	sfxChoices         []string
	sfxChoicesForName  string
	sfxChoicesForCount int
	// effects_dropdown selection for the next message (AO2 ui_effects_dropdown). Index 0 is the "None" row, which
	// sends "". It survives the send: AO2's `stickyeffects` defaults true, so courtroom.cpp:2348's reset never
	// fires (see clearEffectsAfterSend).
	overlayChoiceIdx int
	// charDefaultSide is the side the active character's char.ini declares (AO2 default_side,
	// courtroom.cpp:4723-4726).
	// Empty means unknown or none declared; defaultSide() reports "wit" (text_file_functions.cpp:480-483).
	// It must be cleared when the active character changes, before the new char.ini lands: pos_remove compares
	// against it and clicking it sends /pos, so a stale value puts a wrong packet on the wire.
	charDefaultSide string
	// emote_dropdown's option list, cached with the same plain-field guard as sfxChoices because it is rebuilt from a
	// per-frame draw site. Per tab.
	emoteChoices         []string
	emoteChoicesForName  string
	emoteChoicesForCount int
	// iniswap_dropdown's option list, same guarded cache. The wardrobe guard is a generation counter because
	// WardrobeList clones.
	iniswapChoices        []string
	iniswapChoicesForChar string
	iniswapChoicesWorn    string
	iniswapChoicesGen     uint64
	// SFX Browser: an opt-in modal with favourites, per-row preview and a free-text sound name. It picks into the
	// same
	// sfxChoiceIdx override (find-or-append).
	showSfxBrowser   bool
	sfxBrowserQuery  string // the search / free-text field (filters the list; usable as an exact name)
	sfxBrowserScroll int32
	// emotePageLabel memoizes the "page x/y · N emotes" counter; rebuilt only when {page, pages, total} change.
	emotePageLabel    string
	emotePageLabelKey [3]int
	// musicCount / areaCount memoize the "<shown> / <total>" header counters (shownTotalMemo, screens.go). Separate
	// because both panels can be on screen at once. Per session, so they park with the tab.
	musicCount shownTotalMemo
	areaCount  shownTotalMemo
	// musicNowLabel memoizes the Music header's "Now playing: <track>" readout (nowPlayingMemo, screens.go).
	musicNowLabel nowPlayingMemo
	// Emote favourites view: emoteFavSet holds the active character's favourited emote indices; emoteVisible is the
	// reused list of indices the grid shows (all, or favs-only). refreshEmoteView rebuilds both only when the guard
	// key (char/favs-only/emote count/edit-rev) changes.
	emoteFavSet      map[int]struct{}
	emoteVisible     []int
	favBoxList       []int  // current character's favourite emote indices (always; for the floating box)
	emoteFavRev      int    // bumped on every favourite toggle to invalidate the view
	emoteViewChar    string // guard: character the view was built for
	emoteViewFavOnly bool   // guard: favs-only state the view was built for
	emoteViewLen     int    // guard: len(emotes) the view was built for
	emoteViewRev     int    // guard: emoteFavRev the view was built for
	// Floating favourite-emotes box: movable geometry (session) and the drag grab offset. Open state is the
	// FavEmoteBox pref.
	favBoxX, favBoxY int32
	favBoxPlaced     bool
	favBoxDragging   bool
	favBoxGrabDX     int32
	favBoxGrabDY     int32
	// Floating Sprite Style box: non-modal, draggable picker for the transmitted sprite style; session-only.
	showStyleBox     bool
	styleBoxX        int32
	styleBoxY        int32
	styleBoxPlaced   bool
	styleBoxDragging bool
	styleBoxResizing bool  // the right-edge width grip is being dragged
	styleBoxUserW    int32 // user-set width (0 = the default styleBoxW); height stays content-driven
	styleBoxScroll   int32 // vertical scroll of the box body (the box clamps to the window; the body scrolls inside — #C); clamped to [0, maxScroll] every frame
	styleBoxGrabDX   int32
	styleBoxGrabDY   int32
	// Custom glitch fringe-colour hex buffers (the style box's A/B fields): in-progress typing; mirror the pref when
	// unfocused.
	glitchHexA string
	glitchHexB string
	// Style-box live preview: collapse state, the background page cache (keys are full URLs), and the sprite prefetch
	// dedupe (one warm per emote base).
	stylePrevOff     bool
	stylePrevBgPages []*render.TexturePage
	stylePrevBgGen   uint64
	stylePrevBgKey   string
	stylePrevWarm    string
	// showAreaWheel expands the area-colour row's inline colour wheel (Settings → Area list); session-only.
	showAreaWheel bool
	// Server-clock chip memo: the "Tn mm:ss" labels are rebuilt only when the displayed second changes.
	timerChips     []string
	timerLabels    [courtroom.TimerCount]string
	timerLabelSecs [courtroom.TimerCount]int
	charBlips      string // char.ini blips/gender (outgoing default)
	// 2.10 custom shouts ([Shouts] in char.ini): customIdx -1 = the base "custom" art, ≥ 0 indexes customShouts.
	customShouts []courtroom.CustomShout
	customIdx    int
	customName   string
	charINIBusy  bool
	icLog        []icEntry
	icLogSeq     uint64 // bumps per mutation: keys the filter cache
	icScroll     int32
	// icScrollVis is the eased on-screen scroll offset; icScroll is the target the wheel/stick/bar writes.
	// frameDtMs (stamped per drawn frame) keeps the easing frame-rate independent.
	icScrollVis float64
	frameDtMs   float32
	logSearch   string
	// Advanced log filters: show only entries whose speaker matches logFilterShowname and whose position matches
	// logFilterPos ("" = no filter), on top of the logSearch match.
	logFilterShowname string
	logFilterPos      string
	logFilterColor    int  // colour filter (0 = "Any color"); index into logColorFilterChoices (#49)
	logFilterOpen     bool // the filter sub-panel is expanded
	oocSeq            uint64
	oocLog            []string
	oocSpeakers       []string // parallel to oocLog: speaker per line ("" = system line); for name colours
	oocScroll         int32
	musicScroll       int32
	// Music-list search: the query plus a memoized filter. musicFiltered holds matching indices into a.sess.Music.
	musicSearch     string
	musicFiltered   []int
	musicFilterMemo musicFilterKey
	// Music-list category collapse, keyed by the category header's index into a.sess.Music (servers can reuse a
	// category name). Absent from the map = expanded. musicCollapseGen bumps on every toggle so musicRows
	// (screens.go refreshMusicGroups) rebuilds; the grouped layout is memoized like musicFiltered.
	musicCollapsed   map[int]bool
	musicCollapseGen uint64
	musicRows        []musicRow
	musicGroupMemo   musicGroupKey
	// Areas-list search: the query plus a cheap Areas-list identity as a memoized filter. areaFiltered holds matching
	// indices into a.sess.Areas, which parallels a.sess.AreaInfo, so the original index indexes both.
	// One memo per query source (areaQuerySlots, screens.go): a torn-off Areas panel and the themed canvas copy
	// (AO2 music_search) can draw in the same frame under different queries and would evict a single memo each frame.
	areaSearch     string
	areaFiltered   [areaQuerySlots][]int
	areaFilterMemo [areaQuerySlots]areaFilterKey
	areaScroll     int32
	// areaInfoSeq bumps on every EventAreasUpdated (ARUP) so areaWrapped (screens.go) re-wraps; it excludes
	// a.curArea.
	areaInfoSeq uint64
	// areaWrap caches the word-wrapped ARUP row text; rebuilt only when the ARUP state, query, panel width, zoom or
	// font chain changes. One per query source because the two drawers differ in width as well as query.
	areaWrap [areaQuerySlots]areaWrapCache
	logTab   int
	// debugOOC swaps the themed server_chatlog panel for the debug log, like AO2's ooc_toggle button
	// (on_ooc_toggle_clicked, courtroom.cpp:5197). Stored inverted relative to AO2's server_ooc so the zero value is
	// AO2's initial state without a seed in resetSessionState.
	debugOOC       bool
	debugOOCScroll int32 // the debug panel's own scroll offset (per tab, like the logs)
	volStripOn     bool  // the log panel's toggleable on-screen volume strip
	musicVolMode   bool  // Music tab shows the volume sliders instead of the track list
	// Stick flags: the logs follow new lines while true; scrolling up releases, scrolling to the bottom re-sticks.
	icStick  bool
	oocStick bool
	// icReadMark is the IC entry count when last caught up to the bottom; len(icLog)-icReadMark is the "N new" pill
	// count.
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
	// bindingFor is the character a wardrobe key-capture is armed for ("" = none); charKeys/charKeysRev cache this
	// server's binds, refreshed on connect and edits only.
	bindingFor  string
	hkCapture   string            // settings Controls tab: hotkey id a key-capture is armed for ("" = none)
	charKeys    map[string]string // key name → character
	charKeysRev map[string]string // character → key name (badges)

	// --- showname keybinds (global) ---
	// shownameBindFor is the showname a Settings key-capture is armed for ("" = none); shownameKeys/Rev cache the
	// global binds, refreshed like charKeys.
	shownameBindFor string
	shownameKeys    map[string]string // key name → showname
	shownameKeysRev map[string]string // showname → key name (badges)
	// Custom style presets: stylePresetBindFor is the preset name awaiting a key-capture ("" = none);
	// stylePresetNameInput is the Style box's name field.
	stylePresetBindFor   string
	stylePresetNameInput string
	// IC quick-phrases (ic_phrase.go): a bare key sends a canned IC line. icPhraseBindFor is the phrase a Settings
	// key-capture is armed for; icPhraseKeys caches the key to phrase binds.
	icPhraseBindFor string
	icPhraseKeys    map[string]string // key name → IC phrase

	// ghostWarm dedupes the pair-panel ghost editor's sprite prefetches.
	ghostWarm map[string]string

	// --- OOC automation (login flows + macros share the send pipeline) ---
	oocQueue []oocSend // paced OOC sends (≤ macroQueueCap)
	// lastAutoOOC stamps the previous automated OOC send so processOOCQueue spaces the next by oocSendMinGap. The
	// pacing must live at the drain: a queue built up during a stall comes due at once, and that burst trips server
	// OOC flood guards.
	lastAutoOOC time.Time
	showLogin   bool
	loginUser   string
	loginPass   string
	loginAuto   bool

	// --- viewport camera (hyperfocus zoom; 0 or 1 = off) ---
	vpZoom   float64
	vpPanX   float64 // pan fractions of the zoom overflow (0..1)
	vpPanY   float64
	zoomDrag bool
	zoomPrev bool // last frame's mouseDown (edge detect)

	// Drag the viewport's right edge to resize it (vpPct); dragVpDivider is the active grab.
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
	// Local alarm/timer (opt-in): a personal countdown, app-global across tabs. timerEndAt zero = not counting down;
	// timerPausedLeft > 0 = paused; both at rest = idle.
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
	// evidDescScroll scrolls the inspector's description; evidDescScrollIdx is the item it belongs to, so changing
	// the selection resets the scroll.
	evidDescScroll    int32
	evidDescScrollIdx int
	evidAsk           []time.Time // thumbnail demand pacing, parallel to Evidence
	// evShow is the incoming presented-evidence pop-up.
	evShowImg string
	evShowAt  time.Time
	// evidPresentImage is the present-image override: when armed and non-empty, presenting shows this image instead
	// of the selected evidence's own.
	evidPresentImage string
	// evidCtxMenu opens the right-click menu over the evidence list at evidCtxX/evidCtxY.
	evidCtxMenu bool
	evidCtxX    int32
	evidCtxY    int32
	// evidInspectorDetached pops the inspector out into its own floating window; evidInspectorWin is its geometry.
	evidInspectorDetached bool
	evidInspectorWin      floatWin
	// evidGridView toggles the evidence browser between the DRO-style icon list and the AO2 thumbnail grid. Not
	// persisted.
	evidGridView bool
	// evidIconSize scales the evidence list icons and grid cells (32-96 px, default 40). Not persisted.
	evidIconSize int32
	// evidStream selects the evidence image source: off reads local mounts (local://), on streams from the server.
	// evidLocalOrigin caches the local:// origin (rebuilt on mount change); "" with no mounts, which falls back to
	// streaming.
	evidStream      bool
	evidLocalOrigin string
	// Server evidence image picker: with streaming on, the editor's "Browse…" opens a modal grid of the server's
	// evidence/ autoindex instead of the local folder browser.
	evidServerPickerOpen bool
	evidServerNames      []string
	evidServerLower      []string
	evidServerRes        chan []string
	evidServerBusy       bool
	evidServerSearch     string
	evidServerScroll     int32
	// evidDiscardConfirm is the "Stop editing?" modal shown when the selection changes while the editor is open.
	// evidDiscardTarget is the index the swap would land on (-1 = Add new).
	evidDiscardConfirm bool
	evidDiscardTarget  int
	// evidConflict is the incoming-edit compare view: an EE for the item being edited arrived mid-edit.
	// evidIncoming snapshots the server's new {name,desc,image} for Keep mine / Accept theirs.
	evidConflict bool
	evidIncoming courtroom.EvidenceItem
	// The evidence image picker is the merged local asset source browser (demobrowser.go + mergebrowse.go,
	// purposeEvidenceImage).

	// --- wardrobe / iniswap (client favourites + server iniswap.txt) ---
	iniChar      string   // active override folder ("" = picked character)
	pendingIni   string   // wear this once PV confirms (char-select joins)
	iniServer    []string // the server's iniswap.txt names (may be empty)
	iniList      []string // merged menu: wardrobe first, then server extras
	iniWardrobe  []bool   // parallel to iniList: wardrobe membership (star)
	iniServerMem []bool   // parallel to iniList: is in the server's iniswap.txt (Iniswaps tab filter)
	iniLower     []string // lowercased names for the search filter
	iniFolders   []string // parallel to iniList: each entry's folder ("" = unsorted)
	// iniHoverIDs caches each wardrobe cell's HoverPreview id ("iniswap:"+name), parallel to iniList, like
	// charHoverIDs.
	// Rebuilt with the list (rebuildIniMenu).
	iniHoverIDs []string
	// A wardrobe star toggle is deferred until after the grid loop: it rebuilds and shrinks the slices the loop
	// ranges over.
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
	// Drag-to-file: drag a character cell onto a folder chip. iniDragChar is armed on press; iniDragging flips once
	// the move passes iniDragThreshold and then suppresses the wear-click.
	iniDragChar     string
	iniDragStart    [2]int32
	iniDragging     bool
	iniPrevDown     bool // mouse-held tracker for press detection in the modal
	iniPressed      bool // mouse went down this frame (computed per frame)
	iniScroll       int32
	iniBrowseScroll int32       // separate scroll for the Iniswaps (browse-all) tab
	iniAsk          []time.Time // demand pacing stamps, parallel to iniList
	// iniBase is the Iniswaps tab's "From your base" source: the server roster plus character folders under the local
	// mounts. It is separate from iniList (iniswapbase.go) because iniList is the wardrobe's index-keyed store, and
	// merging a large roster would flood that grid and invalidate every cached icon page.
	iniBase iniBaseBrowser

	// wardSection selects the open wardrobe tab (wardSectionCharacters / wardSectionBackgrounds).
	wardSection int
	// wardDelFolder is the folder a delete confirmation is open for ("" = none).
	wardDelFolder string
	// Backgrounds section: bgFavList is the favourites in one stable order (FavBackgroundList); navigation filters by
	// predicate and never rebuilds the list, so the index-keyed bgFavPages cache stays valid. bgFavPages is its own
	// cache, never shared with bgPick.pages (different index space). Nulled only in rebuildBgFav.
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

	// dl is the opt-in single-asset downloader. Its pause flag lives in App (a.dlPaused): this struct is copied by
	// value on tab park/activate, which can't copy an atomic.
	dl downloader

	// sfxMuted is the Mute SFX hotkey; showHotkeys toggles the F1 cheat-sheet; musicDucked tracks music ducking under
	// a playing message. masterMuted/musicMuted/blipMuted are the per-channel mutes; all four persist and zero the
	// channel in applyAudioVolumes without touching the stored slider level. resetSessionState reseeds them from
	// prefs.
	sfxMuted           bool
	masterMuted        bool
	musicMuted         bool
	blipMuted          bool
	showHotkeys        bool
	hkCache            []hkEntry           // hotkey cheat-sheet rows, rebuilt once per open (not per frame)
	confirmDisconnect  bool                // a Disconnect confirm popup is open (unless instant-disconnect is set)
	disconnectDlg      disconnectDialog    // involuntary-disconnect modal over the FROZEN courtroom (zero value = closed). Lives on sessionState DELIBERATELY: it is a per-tab session modal (it belongs to the frozen room/logs it draws over), so it parks/restores with its OWN tab rather than bleeding onto whatever tab is active, and resetSessionState clears it for free — no App-level special-casing. Nothing needs it to outlive resetSessionState: closeDisconnectDialogToLobby / reconnectFromDisconnectDialog snapshot name/url into locals and clear the dialog BEFORE the teardown that resets. Because it's per-tab, EVERY park/swap surface must be inert while it's open, or a park would copy the open dialog into a slot and resetSessionState would wipe the live copy — a stale modal resurfacing cross-tab. Two surfaces do a PRIMARY-session switch and both are now guarded: handleTabBar (tabs.go) and the pinned-client control-swap consume (a.pendingControlSwap, above); with both guarded an open dialog can never park at all. Do NOT move this to App even so: an App-level open dialog would paint tab 0's reason + redial target over a DIFFERENT pinned server's live courtroom, and its Reconnect would tear down that healthy session to dial the wrong one — the per-tab placement is what makes it belong to the frozen server, and the guards are a second line, not the reason.
	pendingCloseTab    *courtTab           // a "close this tab?" confirm is open for this background tab (nil = none). A POINTER, not an index: tabs reorder/reap/tear-off between the ✕-click and the confirm, so the index would go stale — revalidate the pointer is still in a.tabs before acting (see confirmPendingCloseTab). Doubles as the open flag, like hidePrompt. ⚠ Lives on sessionState (parked/copied per tab), safe ONLY because every live park/swap path is strip-guarded, pointer-fenced, or resets session state — a NEW park/swap path must keep one of those protections or this field moves to App (the conn-error-state precedent), else a parked slot could resurface a stale modal.
	hidePrompt         string              // a "hide this sprite?" confirm is open for this char name ("" = none)
	hiddenSprites      map[string]struct{} // chars hidden from the viewport this session (lowercased); nil until first hide
	autoConnectPending bool                // fire auto-connect-to-last-server once, on the first frame
	musicDucked        bool
	// musicStartedAt stamps when the active tab's current area track began playing; it does not park per tab.
	// Re-stamped on every track change. Its only reader is resumeActiveTabMusic's fallback seek estimate for when no
	// SwappedOutSnap exists. Zero = unknown (resume from the top).
	musicStartedAt time.Time

	// Scenery self-heal stamps (healScenery pacing).
	bgAskBase   string
	bgAskAt     time.Time
	deskAskBase string
	deskAskAt   time.Time
	spkAskBase  string // speaker sprite heal pacing
	spkAskAt    time.Time
	pairAskBase string // pair sprite heal pacing
	pairAskAt   time.Time

	// Click-to-pair (/pair <uid> for servers that sync pairs via OOC). areaUIDs/areaPlayers are parsed from /getarea
	// in
	// pushOOC; the popup pre-fills the UID only when it can confidently match the clicked char.
	pairPopupOpen bool
	pairPopupChar string
	pairPopupUID  string
	areaUIDs      map[string]string
	areaPlayers   []areaPlayer
	areaLastUID   string    // last "[uid]" parsed, so a following Showname/OOC/IPID line attaches to it
	areaCurName   string    // area currently being parsed in a /gas block (tags each player's .area)
	areaListAt    time.Time // when the current roster snapshot was parsed ("as of HH:MM")
	pairAreaReset bool
	// Live player list: rosterLegacy off (default) = the CharsCheck/ARUP roster; on = the /getarea snapshot.
	// shownameFor caches char to showname from incoming IC so a live row shows the showname.
	rosterLegacy  bool
	livePlayersOn bool         // PR/PU server roster is the live source (else the CharsCheck fallback)
	liveRoster    []areaPlayer // M1 live roster (PR/PU players, or CharsCheck taken chars + ARUP spectators)
	liveRosterAt  time.Time    // live roster's last change — the rows/order memo key
	// joinFlash[uid] = when a UID first appeared in the live roster (zero = present at first population, never
	// flashed).
	// Updated only on a roster change (updateJoinFlash).
	joinFlash     map[string]time.Time
	joinFlashInit bool
	// pairPartners maps a lowercased character to who they last spoke paired with (opt-in player-list chip). A paired
	// IC message sets it; a solo one clears it. Per tab; bounded by pairPartnersCap.
	pairPartners map[string]string
	// Player-list profile card popover: which profile to show and its title.
	profileCardShow bool
	profileCardPr   config.ProfilePref
	profileCardName string
	// Player-row action menu (rostermenu.go), the "…" / right-click popup. Holds an identity snapshot because the
	// roster can refresh under an open menu, plus the modal-fence flag.
	rosterMenuOpen    bool
	rosterMenuFenceOn bool             // WE hold modalOn for the open menu (released on close)
	rosterMenuMe      bool             // the snapshot row is our own client
	rosterMenuAt      sdl.Point        // preferred top-left (clamped at draw)
	rosterMenuTab     int              // tab the menu opened on — auto-close on switch
	rosterMenuP       areaPlayer       // identity snapshot (uid/name/showname/ooc/ipid)
	rosterMenuItems   []rosterMenuItem // built at open; bounded by the action-kind count
	// Music panel overflow menu (musicheader.go): the popup holding the playback flags and volume-view toggle. Same
	// modal-fence and tab handling as the roster menu above.
	musicMenuOpen         bool
	musicMenuFenceOn      bool      // WE hold modalOn for the open menu (released on close)
	musicMenuAt           sdl.Point // preferred top-left (clamped at draw)
	musicMenuTab          int       // tab the menu opened on — auto-close on switch
	musicMenuThemed       bool      // the panel drew inside a theme canvas when it opened (gates the volume-view row)
	liveDetailsArea       string    // area of the last auto /getarea pull; re-pull on area change
	suppressAreaEchoUntil time.Time // keep /gas/getarea reply lines out of OOC until this time — the WHOLE reply burst (a multi-area /gas spans several messages), not just the first
	// Follow-a-player: followUID is the player we trail across areas ("" = off); we auto-jump to their area on each
	// PR/PU update, debounced.
	followUID      string
	lastFollowJump time.Time
	followShow     bool // FollowEnabled pref, read once per player-list frame (no per-row lock)
	pairStatusShow bool // ShowPairStatus pref (#20), read once per player-list frame (no per-row lock)
	// areaHistory is the most recently visited areas (index 0 = current), driven by our own PR/PU area; the Areas tab
	// shows the rest as jump-back chips. Bounded by areaHistoryCap.
	areaHistory    []string
	shownameFor    map[string]string
	lastSFXName    string // M11: most-recent emote SFX name, for one-click "Mute last SFX"
	lastBlipChar   string // M11: most-recent speaker char, for one-click per-character blip volume
	icCountN       int    // M5 IC char counter: cached count + its string, reformatted
	icCountStr     string // only when the length changes so the frame stays 0-alloc
	pairListScroll int32
	playerScroll   int32 // Players-tab roster scroll
	// Area-jump scroll follow: the grouped roster floats the just-jumped area to the top only on the next memo
	// rebuild (after a server round-trip), so a one-shot scroll would target the pre-reorder position. Instead the
	// target is re-nudged into view every frame until it expires. Parks per tab; the zero value is inert.
	areaJumpFollow      string    // target area name to keep scrolled into view ("" = disarmed)
	areaJumpFollowUntil time.Time // hard expiry: a lost/rejected transfer can never pin the scroll past this
	playerSort          int       // roster sort (players): 0=UID, 1=name, 2=speakers-first
	playerAreaSort      int       // /gas area-group order: 0=/gas, 1=A→Z, 2=most-populated
	shownameAdd         string    // M6: Settings "save a showname preset" input
	// playerOrder is the memoized display order (indices into areaPlayers); recomputed only when the roster, sort
	// mode
	// or current speaker changes.
	playerOrder     []int
	playerOrderSort int
	playerOrderLen  int
	playerOrderSpk  string
	playerOrderAt   time.Time
	// playerRows is the memoized grouped display (area headers + players) for a /gas spanning areas; same
	// invalidation keys as playerOrder.
	playerRows         []rosterRow
	playerRowsSort     int
	playerRowsAreaSort int
	playerRowsLen      int
	playerRowsSpk      string
	playerRowsAt       time.Time
	playerRowsAreaAt   time.Time // areaListAt the rows were built at (a /gas reorders the default)
	// Player-row char icons: same demand/cache pipeline as the char grid, keyed by the areaPlayers index. Nulled on
	// every /ga replace so a same-length new roster re-resolves (cachedPage reorder invariant).
	playerIconPages    []*render.TexturePage
	playerIconPagesGen uint64
	playerIconAsk      []time.Time
	// voiceIconAsk paces char-icon fetches for the voice panel rows; textures come straight from the URL-keyed Store.
	// Sized to the live peer count.
	voiceIconAsk []time.Time

	// Client-side sprite position overrides, keyed by lowercased character folder; they win over the server's
	// per-message positions (drag in the viewport; right-click a sprite resets).
	spriteOv  map[string][2]int
	dragName  string // character being dragged ("" = none)
	dragStart [2]int32
	dragBase  [2]int
	prevDown  bool // mouseDown edge detection for drag begin

	// Chat raster invalidation extras (text and colour tracked separately).
	rasterScale      int
	rasterW          int32
	rasterSkinned    bool      // a drawn chatbox skin (theme OR per-character) gates theme text colors (readability)
	rasterChatMsg    theme.RGB // per-character chatbox ink the message raster was built with (cache key)
	rasterChatMsgHas bool
	rasterDevPct     int32 // #77: the device font scale the raster was built at — a UI-scale change must rebuild it

	// pairing panel
	pairSearch string
	pairScroll int32
	pairWith   int
	pairOrder  int
	showPair   bool
	msRaster   *render.MessageRaster
	rasterText string
	// rasterFailedText is the message whose raster could not be built; it arms the chatbox's plain-label fallback.
	// Without it msRaster stays nil and every frame retries and draws no text.
	rasterFailedText string
	// chatDiag is the last frame's chatbox geometry, shown in Debug → Session.
	chatDiag struct {
		vpH, boxY, boxH     int32
		textY, wrapW        int32
		lines, lineH        int32
		devScale, renderPct int32
		visible, total      int32
		grew                bool
	}
	// rasterRaw is the pre-strip message the cached raster was built from; two differently coloured messages can
	// share the same stripped MessageText.
	rasterRaw string

	// Last-applied scene text colour (raster invalidation).
	rasterColor int

	// Animated chat text: when the message carries effect spans, msAnim replaces msRaster (per-glyph path that can
	// displace and recolour). msAnimFont is the face it was laid out with (the glyph cache keys on it); glyphCache
	// holds shared, bounded white glyph textures. rasterEffSig folds the effect spans into the raster cache key.
	msAnim       *render.AnimatedText
	msAnimFont   *ttf.Font
	glyphCache   *render.GlyphCache
	rasterEffSig uint64
	// rasterCentered folds the webAO "~~" centre flag into the raster cache key.
	rasterCentered bool
	// Creator easter eggs (drawChatOverlay): when the displayed IC text mentions a creator name, the chatbox grows a
	// glow ring. The scan runs on the displayed text, once per message; eggMsg is the text it was computed for and
	// eggKind the resolved egg. refreshEggKind owns the compare-and-store; creatorEgg is the pure scan.
	eggMsg  string
	eggKind uint8
}

type lobbyFetch struct {
	entries []network.ServerEntry
	err     error
}

type iniswapFetch struct {
	key   string // serverKey the fetch was made for (tab-switch guard)
	names []string
	// bgs is the background/ autoindex fetched alongside the txt (nil without a listing): pollIniswap filters
	// background folder names out of the iniswap list, since some servers publish one combined file.
	bgs []string
	err error
}

type charINIFetch struct {
	key string // serverKey the fetch was made for (tab-switch guard)
	ini *courtroom.CharINI
	err error
}

// previewEmoteFetch is one previewed character's emote list for the try-before-wear cycle; char guards against
// a newer hover landing first.
type previewEmoteFetch struct {
	key    string
	char   string
	anims  []string // per-emote animation name (sprite stem)
	labels []string // per-emote display comment (caption)
}

// manifestFetch is one extensions.json autodetect result. status carries the raw outcome (applied / absent /
// failed) because err is overwritten for the non-applied cases; Settings → Formats needs to tell "no manifest
// published" from "could not reach it".
type manifestFetch struct {
	host   string
	seeded int
	status seedStatus
	err    error
}

// themeApply is one off-thread theme load: resident theme textures, courtroom/penalty sound names, message and
// showname font colours, and the diagnostics for the status line and debug overlay.
type themeApply struct {
	gen     uint64                     // themeGen stamp; older never overwrites newer
	name    string                     // theme that was loaded
	images  map[string]*assets.Decoded // stem → decoded (themeImageStems keys)
	sounds  map[string]string          // sound key → streamed sfx name
	msgCol  sdl.Color
	hasMsg  bool
	nameCol sdl.Color
	hasName bool

	// layout is the courtroom_design.ini geometry (themeLayoutKeys the theme defines, in design-space pixels);
	// showname/message stay chatbox-relative as in AO2.
	layout map[string]theme.Rect
	// tabBarSeeded records that seedTabBarDesignRect synthesized "asyncao_tabbar" because the theme is silent about
	// it. Resolved on the apply goroutine with the seed, before the [overrides] fold.
	tabBarSeeded bool
	emoteCell    [2]int // emote_button_size (w, h)
	emoteGap     [2]int // emote_button_spacing (x, y)
	// effectsIcon is courtroom_design.ini's `effects_icon_size` (courtroom.cpp:979), the per-row icon size in the
	// effects dropdown. A pair, so it cannot ride `layout`. 0,0 = the theme is silent.
	effectsIcon [2]int
	// themeObj is the loaded theme, carried so the effects-overlay roster resolve can walk its ladder off-thread
	// without re-running theme.Load. Safe to share: *theme.Theme is read-only after Load. nil when the load failed.
	themeObj *theme.Theme
	// sidecar is the theme's asyncao_theme.ini, parsed on this goroutine (hard rule 2). nil for a stock AO2 theme.
	sidecar *theme.Sidecar
	// sidecarErr / sidecarTip are a refused sidecar rendered into the two strings the chip needs (themeerrchip.go).
	// Both empty normally. A theme with no sidecar reports nothing; only an existing but unreadable one is an error.
	sidecarErr string
	sidecarTip string
	// mediaPlan is the theme's declared art, planned against the media budget before any byte is read
	// (thememedia.go). media holds what the plan admitted, keyed by the final theme:// key.
	mediaPlan themeMediaPlan
	media     map[string]*assets.Decoded
	// themeID is the folder name sanitized into the cache-key namespace (docs/THEME-FORMAT.md); resolved here so
	// the render thread cannot derive it differently.
	themeID string
	// shownameAlign is courtroom_design.ini's `showname_align`, not a rect so it cannot ride `layout`. Parsed off the
	// render thread because parseShownameAlign lowercases the raw value (hard rule 2).
	shownameAlign shownameAlign
	// shownameExtra is courtroom_design.ini's `showname_extra_width` in design pixels: how much wider the showname
	// box becomes at each rung of AO2's widen-and-swap ladder (courtroom.cpp:3357). Zero or absent disables it.
	shownameExtra int
	// charSel is char select's own geometry, from the same courtroom_design.ini but kept apart from `layout` (see
	// charselectlayout.go). Every key is already resolved here (theme value, else the embedded default-theme table),
	// because AO2's per-key fallback needs a base/themes/ tree.
	charSel charSelectDesign
	// palette is the courtroom_stylesheets.css colour scheme, applied over the kit colours and restored on theme
	// switch.
	palette theme.Palette

	// Diagnostics: where the skin came from and the dirs probed.
	chatboxFile string
	chatboxDir  string
	// chatboxStem is the base skin's file stem without extension ("chat", "chatbox" or "chatblank"). The med/big
	// ladder art is named after it (see chatboxMedSuffix), so it is load-time state, not diagnostics.
	chatboxStem string
	iniKeys     int
	// unbound reports rect-shaped design keys this theme declares that reach no widget; "" when fully covered.
	// Built on the apply goroutine and pushed to the debug log on land.
	unbound  string
	probed   []string
	inkGuard string // readability guard verdict ("" = colors kept)
	fontPath string // the active theme's bundled font file (.ttf/.otf), "" = none (#6)

	// Per-element courtroom_fonts.ini: the resolved size/face/bold table plus the distinct face files it points at
	// (at most themeFaceCap), read off-thread. facePaths is the dedupe key; faceNames feeds the settings status line.
	fontTable themeFontTable
	faceData  [][]byte
	facePaths []string
	faceNames []string
	// Per-panel user font overrides: how many were applied, and those whose family resolved to no file. The second is
	// reported on the Settings screen so a font name that does nothing is visible where it was typed.
	panelFontsAsked   int
	panelFontsMissing []string
	// themeFontsMissing are families the theme declared that resolved to no file here; themeFontWarn is the sentence
	// about them. Built off-thread because the wording needs a disk probe (theme.HasAnyFontFile).
	themeFontsMissing []string
	themeFontWarn     string
	// report is the import report (themereport.go): sidecar degrades, missing families and unbound design keys.
	// Bounded by themeReportCap, read once at the landing.
	report []string
	// userFontsDir is AsyncAO's font drop folder, created on this pass so the warning names a path that exists.
	userFontsDir string
	// panelFace / panelPct are the resolved per-panel overrides: a 1-based index into faceData and a percent, both 0
	// for "no override". Kept apart from fontTable so the precedence ladder lives in landThemeFonts.
	panelFace [themeFontElemCount]int
	panelPct  [themeFontElemCount]int
	// backdropLuma is the average luma of the theme's art behind each font element's rect; backdropSampled says
	// whether there was any art. Filled here because res.images is only reachable on the apply goroutine (hard
	// rule 2); used by the render-thread ink guard. Two arrays so the zero value means unknown: an int sentinel
	// would read an unfilled themeApply as a black backdrop, which silently keeps black ink. Unsampled falls back
	// to ColPanel (theme_layout.go's stage else-branch).
	backdropLuma    [themeFontElemCount]int
	backdropSampled [themeFontElemCount]bool
}

// themeStemChatbox is the chatbox skin's stem in themeTex / T1.
const themeStemChatbox = "chatbox"

// themeStemChatboxMed / themeStemChatboxBig are the two wider chatbox skins of AO2's showname widen-and-swap
// ladder. They are T1 keys, not file names: the files are named after whichever base candidate resolved, which
// is only known after the theme is walked.
const (
	themeStemChatboxMed = "chatboxmed"
	themeStemChatboxBig = "chatboxbig"
)

// chatboxMedSuffix / chatboxBigSuffix are appended to the resolved base skin's path to name the wider variants
// (courtroom.cpp:3358 and :3362).
const (
	chatboxMedSuffix = "med"
	chatboxBigSuffix = "big"
)

// themeStemCharSelectBG is the char-select backdrop's stem in themeTex / T1; the loader and charselectlayout.go
// share it.
const themeStemCharSelectBG = "charselect_bg"

// Readability guard for theme ink. Real themes ship mismatched pairs (dark ink on a dark skin) that render
// invisible. At load the ink's luma is compared with the skin's average luma, and the theme colour is dropped
// when the gap is below the floor. AO2 renders the pair as authored; readable text wins here.
const (
	// lumaSampleStep samples every Nth pixel on both axes, once per theme apply, off-thread.
	lumaSampleStep = 8
	// minInkSkinContrast is the minimum |ink - skin| luma gap (0..255); below it text is declared unreadable.
	minInkSkinContrast = 48
	// transparentSkinLuma stands in for see-through skin pixels: the chatbox overlays a dark viewport or panel.
	transparentSkinLuma = 20
)

// colLuma is Rec. 601 perceptual luma on the 0..255 scale.
func colLuma(c sdl.Color) int {
	return (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
}

// avgSkinLuma samples a skin frame's average luma, compositing alpha against the dark backdrop.
func avgSkinLuma(img *image.RGBA, step int) int {
	return avgSkinLumaRect(img, img.Bounds(), step)
}

// avgSkinLumaRect is the same average over one sub-rectangle, for the per-element ink guard. It takes the rect
// rather than a SubImage, which allocates a header per call.
func avgSkinLumaRect(img *image.RGBA, r image.Rectangle, step int) int {
	b := r.Intersect(img.Bounds())
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

// absInt is the integer |x|.
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// themeImageExts is the per-stem probe order, matching AO2-Client get_image_suffix (webp, apng, gif, png).
var themeImageExts = []string{".webp", ".apng", ".gif", ".png"}

// themeImageStems maps each resident theme texture stem to the candidate file stems probed in order. Chatbox
// candidates mirror courtroom.cpp:3328 ("chat", "chatbox", then "chatblank"); splash stems mirror handle_wtce's
// filenames with the bare legacy spelling second.
// The per-character misc chatbox is deliberately not wired here. AO2 reads `p_misc` from the speaking character's
// char.ini on every message (courtroom.cpp:3299, :4243) and inserts `misc/<p_misc>/` before the theme rungs
// (path_functions.cpp:181-189); theme.FindAssetMisc implements those rungs. This table is pinned, resolved once
// per theme apply and counted against the theme media budget, while a per-speaker chatbox needs its own cap and
// lifetime so a large roster cannot pin that many skins.
func themeImageStems() map[string][]string {
	m := map[string][]string{
		themeStemChatbox:   {"chat", "chatbox", "chatblank"},
		"witnesstestimony": {"witnesstestimony_bubble", "witnesstestimony"},
		"crossexamination": {"crossexamination_bubble", "crossexamination"},
		"notguilty":        {"notguilty_bubble", "notguilty"},
		"guilty":           {"guilty_bubble", "guilty"},
		"testimony":        {"testimony"},
		// Screen backdrops.
		"courtroombackground": {"courtroombackground"},
		// The music plate (label 2) is an animation layer in AO2 (courtroom.cpp:166) that themes often ship as .gif,
		// so it rides themeImageStems (webp, apng, gif, png) rather than the PNG-first themeButtonStems.
		"music_display":       {"music_display"},
		"lobbybackground":     {"lobbybackground", "loadingbackground"},
		themeStemCharSelectBG: {"charselect_background"},
		// The two char-select cell overlays AOCharButton ships as theme art: char_taken covers a taken button,
		// char_selector is the 62x62 hover halo (aocharbutton.cpp). Both optional and tiny.
		themeStemCharTaken:    {"char_taken"},
		themeStemCharSelector: {"char_selector"},
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

// themeButtonStems maps courtroom_design.ini element keys to their AOButton art stems (courtroom.cpp set_widgets
// setImage calls), derived from the themeSlots registry (themeslots.go). Loaded PNG-first: bare splash names
// ("crossexamination.gif") collide by stem with button files ("crossexamination.png"), and the button is the png.
func themeButtonStems() map[string][]string {
	m := make(map[string][]string, len(themeSlots))
	for i := range themeSlots {
		if s := &themeSlots[i]; len(s.art) > 0 {
			m[s.key] = s.art
		}
	}
	return m
}

// themeButtonExts: png first, see themeButtonStems.
var themeButtonExts = []string{".png", ".webp", ".apng", ".gif"}

// themeLayoutKeys are the courtroom_design.ini rects the themed courtroom ingests, named as AO2-Client
// set_size_and_pos reads them, derived from the themeSlots registry (themeslots.go). "courtroom" and "viewport"
// are mandatory for the layout to activate; a key the theme omits has no widget (courtroom.cpp:1334-1338).
// The asyncao_* opt-ins are author overrides, not the layout mechanism. Sorted for stable test output.
var themeLayoutKeys = func() []string {
	keys := make([]string, 0, 2*len(themeSlots))
	for i := range themeSlots {
		s := &themeSlots[i]
		keys = append(keys, s.key)
		if s.alt != "" {
			keys = append(keys, s.alt)
		}
	}
	sort.Strings(keys)
	return keys
}()

// themeBtnStems is the precomputed theme:// stem ("btn/<design key>") for every key the themed courtroom can draw
// as a button. drawThemeButton runs a dozen times per themed frame and a runtime concat would allocate each time.
// Built once from a fixed table, so it is bounded (hard rule 4).
var themeBtnStems = func() map[string]string {
	m := make(map[string]string, len(themeLayoutKeys))
	for _, k := range themeLayoutKeys {
		m[k] = themeBtnPrefix + k
	}
	return m
}()

// themeBtnStem is themeBtnPrefix+key without the per-frame allocation; a key outside themeLayoutKeys falls
// back to the concat.
func themeBtnStem(key string) string {
	if s, ok := themeBtnStems[key]; ok {
		return s
	}
	return themeBtnPrefix + key
}

// themeTexKey is the T1 key for a theme texture stem; the scheme prefix never collides with asset URLs.
func themeTexKey(stem string) string { return "theme://" + stem }

const (
	// defaultEmoteCellPx / defaultEmoteGapPx are AO2's stock emote-grid metrics, used when the design INI omits the
	// tuples. They come from AO2-Client/bin/base/themes/default/courtroom_design.ini:63-64 (spacing 9, 9; size 40,
	// 40):
	// get_button_spacing falls through to the bundled default theme before giving up with 0,0, and a streaming client
	// has no such tree (see designPair).
	defaultEmoteCellPx = 40
	defaultEmoteGapPx  = 9
)

// designPairComponents is how many numbers a "x, y" design tuple carries. AO2's get_button_spacing returns its
// pre-set 0,0 (text_file_functions.cpp:198-199) when the split is short (text_file_functions.cpp:208-211). That
// 0,0 is its give-up value after the key resolved nowhere, not a declaration of flush buttons, so we use the
// caller's stock constant instead, as designPair does for a missing key.
const designPairComponents = 2

// designPair parses a "x, y" design tuple (emote_button_size and friends).
// Zero is a legitimate value: AO2's get_button_spacing converts with QString::toInt() and applies no positivity
// test (text_file_functions.cpp:193-216), and twelve shipped themes declare "0, 0" for flush buttons.
// A missing key falls back to the caller's default, which is AO2's stock default-theme value (see
// defaultEmoteGapPx / defaultEmoteCellPx), not its give-up 0,0.
// An unparseable component also gets the default; Qt would read it as 0 (HDF-Standard's "8, 8SS" is 8, 0).
// Negative components pass through as in AO2; the consumer floors them (see minEmoteGapPx) because AO2 divides by
// (spacing + button size) unguarded (emotes.cpp:70, charselect.cpp:160; evidence.cpp:211 guards it), so -60 crashes it.
func designPair(t *theme.Theme, key string, defX, defY int) [2]int {
	raw, ok := t.DesignValue(key)
	if !ok {
		return [2]int{defX, defY}
	}
	parts := strings.Split(raw, ",")
	if len(parts) < designPairComponents {
		return [2]int{defX, defY}
	}
	x, errX := strconv.Atoi(strings.TrimSpace(parts[0]))
	y, errY := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errX != nil {
		x = defX
	}
	if errY != nil {
		y = defY
	}
	return [2]int{x, y}
}

// themeSoundKeys lists the courtroom_sounds.ini entries the UI plays, with the aliases handle_wtce probes and
// the stock AO2 default-theme values as fallbacks.
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

// themePenaltyKeys are the penalty/penalty.ini sfx entries (no stock fallback: AO2 ships none).
var themePenaltyKeys = []string{"hp_increased_sfx", "hp_decreased_sfx"}
