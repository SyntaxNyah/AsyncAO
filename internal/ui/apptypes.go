package ui

import (
	"image"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

type sessionState struct {
	link  linkState
	modUI modPanelState
	// Debug panel (Extras → Debug): sectioned diagnostics view (#333 packet
	// inspector + perf + log); see debugPanelState.
	debugPanel debugPanelState
	lookUI     lookToggleState
	frame      framePaceState
	// sceneWarm is the scene-residency keeper's memory (throttle, futility
	// latches, message phase); see sceneWarmState.
	sceneWarm sceneWarmState
	// activeWarm is the active-emote keeper's memory (identity, index, throttle);
	// see activeWarmState.
	activeWarm activeWarmState
	srvMeta    serverMetaState
	// corner badge (0-alloc): the CM column lives in AreaInfo (ARUP), so a roster-stamp memo would
	// miss a /cm that doesn't change the roster — we recompute on the area/player events instead.
	// Ban/Kick box (#130): a FROZEN snapshot of the target, taken when the box opens, so a roster
	// rebuild while the reason is being typed can never repoint a destructive command at someone
	// else. Only the IPID is allowed to fill in later (re-resolved by the frozen UID — same person).
	banBox banBoxState
	// modDash is the mod dashboard's per-session view and selection state; see
	// modDashState.
	modDash modDashState

	// --- char select ---
	charSearch string
	charScroll int32
	charTab    int // charTabServer | charTabWardrobe (grid contents swap)
	// charShowTaken is AO2's char_taken FILTER checkbox (charselect.cpp:99 seeds it
	// CHECKED; :360-364 hides taken characters when it is cleared). It is not an
	// indicator and it is not persisted: AO2 resets it per join, and so does a fresh
	// session here. Only the themed canvas draws the checkbox, so the classic screen
	// passes `true` explicitly rather than reading this — a filter with no control
	// would be a trap.
	charShowTaken bool
	// charListSel is the roster index highlighted in the themed name column
	// (char_list), -1 for none. It also outlines the matching grid cell, which is the
	// whole point of drawing the column: the two views are one selection. Reset on a
	// tab switch, because the two tabs index different slices.
	charListSel int
	// charListScroll is the name column's own scroll offset. Independent of the
	// grid's: the column is one name per row and scrolls far faster.
	charListScroll int32
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
	// charHoverIDs caches the char-select grid's per-cell HoverPreview id
	// ("char:"+name). It was a runtime concat at the call site, i.e. one heap
	// allocation per VISIBLE CELL per frame — 63 of them on a themed 7x9 page and
	// more on a big classic window, for a string that only changes when the roster
	// does. Built and invalidated in lockstep with charLower.
	charHoverIDs []string
	// charSelTitle caches the char-select heading ("Choose a character — <server>"),
	// which was two allocations per frame for a string that changes once per connect.
	// charSelTitleFor is the serverName it was built for.
	charSelTitle    string
	charSelTitleFor string
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
	// invalidates everything" idiom SetTextDevScale uses for the label/emoji caches (#77);
	reactionFloats     []reactionFloat
	reactBadges        map[uint8]*render.Badge
	reactBadgeDevScale int32
	reactRect          sdl.Rect
	reactSpawnSeq      int32
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

	compose   composeState
	pairbox   pairBoxState
	emotepick emotePickState
	// musicCount / areaCount memoize the "<shown> / <total>" header counters of
	// the Music and Areas lists (shownTotalMemo, screens.go) — same reason as
	// emotePageLabel: a bare Sprintf there is one allocation per frame for as long
	// as the tab is open. Separate slots because both panels can be on screen at
	// once. Per-SESSION (the counts come from a.sess.Music / a.sess.Areas), so
	// they park and restore with the tab exactly like the emote memo above.
	musicCount shownTotalMemo
	areaCount  shownTotalMemo
	// musicNowLabel memoizes the Music header's "Now playing: <track>" readout for
	// exactly the same reason (nowPlayingMemo, screens.go).
	musicNowLabel nowPlayingMemo
	favstyle      favStyleState
	chipUI        chipRowState
	chatlog       chatLogState
	musicScroll   int32
	// Music-list search (AO2/webAO parity): the query plus a memoized filter so
	// a list of thousands isn't re-scanned (and re-lowercased — that allocates)
	// every frame. musicFiltered holds matching indices into a.sess.Music.
	musicSearch     string
	musicFiltered   []int
	musicFilterMemo musicFilterKey
	// Music-list category collapse (Issue #17 — AO2/KFO parity: categories
	// were flat with no fold triangle and no Expand/Collapse All). Collapsed
	// state is keyed by the category HEADER's index into a.sess.Music (not
	// its text — servers can reuse a category name, and the index is unique
	// per occurrence). Absent from the map = expanded (the default AO2 look).
	musicCollapsed   map[int]bool
	musicCollapseGen uint64
	musicRows        []musicRow
	musicGroupMemo   musicGroupKey
	// Areas-list search (#20 — parity with the Music tab beside it). Same
	// memoized-filter shape: the query + a cheap Areas-list identity so a
	// hub server's hundreds of areas aren't re-scanned (and re-lowercased —
	// that allocates) every frame. areaFiltered holds matching indices into
	// a.sess.Areas (which parallels a.sess.AreaInfo, so the ORIGINAL index
	// is what indexes both).
	areaSearch     string
	areaFiltered   [areaQuerySlots][]int
	areaFilterMemo [areaQuerySlots]areaFilterKey
	areaScroll     int32
	// areaInfoSeq bumps on every EventAreasUpdated (ARUP) so areaWrapped
	// (screens.go) knows when to re-wrap; it deliberately excludes cosmetic
	// per-frame state (a.curArea) which the draw loop re-reads live instead.
	areaInfoSeq  uint64
	volStripOn   bool // the log panel's toggleable on-screen volume strip
	musicVolMode bool // Music tab shows the volume sliders instead of the track list

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
	oocQueue []oocSend // paced OOC sends (≤ macroQueueCap)
	// lastAutoOOC stamps the previous automated OOC send so processOOCQueue can
	// space the NEXT one by oocSendMinGap. The pacing has to live at the DRAIN,
	// not only in each entry's queue-time `due`: a queue that built up while the
	// drain was stalled comes due all at once, and releasing it as one burst is
	// exactly what tripped server OOC flood guards (see oocSendMinGap).
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
	// evidDescScroll (Issue #15) scrolls the inspector's description text
	// instead of forcing the whole panel to be resized to read it.
	// evidDescScrollIdx remembers which item it belongs to so switching the
	// selection resets the scroll rather than carrying over a stale offset.
	evidDescScroll    int32
	evidDescScrollIdx int
	evidAsk           []time.Time // thumbnail demand pacing, parallel to Evidence
	// evShow is the incoming presented-evidence pop-up.
	evShowImg string
	evShowAt  time.Time
	// evidPresentImage is the #59 present-image override: when armed and
	// non-empty, presenting shows THIS image instead of the selected evidence's
	// own image (decouples the presented picture from the evidence id).
	evidPresentImage string
	// evidCtxMenu opens a small right-click context menu over the evidence list
	// (Edit / Delete / Present / Pin), positioned at evidCtxX/evidCtxY.
	evidCtxMenu bool
	evidCtxX    int32
	evidCtxY    int32
	// evidInspectorDetached pops the inspector (the right column, which now
	// hosts editing as a view/edit toggle) out into its own floating window;
	// evidInspectorWin is that window's geometry. One at a time for now — the
	// inspector edits a single selection, so "multiple edited evidences" would
	// mean multiple of these windows later.
	evidInspectorDetached bool
	evidInspectorWin      floatWin
	// evidGridView toggles the evidence browser between the DRO-style icon
	// list and the AO2 thumbnail grid. In-memory (not persisted): the earlier
	// evidenceMode preference was dropped and this is a quick layout flip, not
	// a stored setting.
	evidGridView bool
	// evidIconSize scales the evidence list icons and grid cells (32–96 px;
	// default 40). In-memory like evidGridView — the icon-size slider in the
	// evidence window drives it and both layouts rescale together.
	evidIconSize int32
	// evidStream toggles the evidence panel's image source: OFF (default) reads
	// evidence images from the user's local mounts (local://); ON streams them
	// from the server. Session-scoped. evidLocalOrigin caches the local:// origin
	// (rebuilt on mount change); "" when there are no local mounts, so the toggle
	// falls back to streaming.
	evidStream      bool
	evidLocalOrigin string
	// Server evidence image picker (#119 stream mode): the evidence editor's
	// "Browse…" button, when streaming is on, opens this modal grid of the server's
	// evidence/ autoindex instead of the local folder browser.
	evidServerPickerOpen bool
	evidServerNames      []string
	evidServerLower      []string
	evidServerRes        chan []string
	evidServerBusy       bool
	evidServerSearch     string
	evidServerScroll     int32
	// evidDiscardConfirm is the "Stop editing?" modal: swapping the selection
	// (or Add new) while the editor is open first asks. evidDiscardTarget is
	// the index the swap would land on (-1 = Add new).
	evidDiscardConfirm bool
	evidDiscardTarget  int
	// evidConflict is the incoming-edit compare view: an EE for the item being
	// edited arrived while the editor was open. evidIncoming snapshots the
	// server's new {name,desc,image} so the user can Keep mine / Accept theirs
	// instead of silently losing the unsaved work.
	evidConflict bool
	evidIncoming courtroom.EvidenceItem
	// The evidence image picker USED TO LIVE HERE as evidPickerOpen/Search/Scroll/
	// Files/Lower/Busy/Scanned/Res (a modal thumbnail grid fed by a local scan plus
	// the server's evidence/ autoindex). Both of its sources were wrong for the
	// playtest — it offered the CASE's existing images and the STREAM instead of the
	// user's own folders, listed credit.txt among them, and showed no folders at all
	// — so the "Choose" thumbnail grid was retired and its button merged into Browse,
	// which one purpose now backs: the merged local asset source browser
	// (demobrowser.go + mergebrowse.go, purposeEvidenceImage).

	// --- wardrobe / iniswap (client favourites + server iniswap.txt) ---
	iniChar      string   // active override folder ("" = picked character)
	pendingIni   string   // wear this once PV confirms (char-select joins)
	iniServer    []string // the server's iniswap.txt names (may be empty)
	iniList      []string // merged menu: wardrobe first, then server extras
	iniWardrobe  []bool   // parallel to iniList: wardrobe membership (star)
	iniServerMem []bool   // parallel to iniList: is in the server's iniswap.txt (Iniswaps tab filter)
	iniLower     []string // lowercased names for the search filter
	iniFolders   []string // parallel to iniList: each entry's folder ("" = unsorted)
	// iniHoverIDs caches each wardrobe cell's HoverPreview id ("iniswap:"+name),
	// parallel to iniList. It was a runtime concat inside the cell draw, i.e. one heap
	// allocation per VISIBLE CELL per frame on three grids — the exact twin of the
	// Characters grid's charHoverIDs, and it has to be the twin now that both tabs of
	// char select lay out on one plan. Rebuilt with the list (rebuildIniMenu).
	iniHoverIDs []string
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
	// iniBase is the Iniswaps tab's "From your base" source: the server's character
	// ROSTER plus the character folders found under the user's local mounts. It is a
	// separate browser (iniswapbase.go) rather than more entries in iniList on
	// purpose — iniList is the wardrobe's index-keyed backing store, ranged whole by
	// char select's Wardrobe tab, so a 4000-name roster merged into it would flood
	// that grid and invalidate every cached icon page.
	iniBase iniBaseBrowser

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

	// sfxMuted is the SFX mute (Mute SFX hotkey); showHotkeys toggles the F1
	// hotkey cheat-sheet overlay; musicDucked tracks whether music is
	// currently ducked under a playing message (transition-driven).
	// masterMuted/musicMuted/blipMuted are the per-channel mute toggles in the
	// volume strip (#10). All four PERSIST (config.AssetPreferences'
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
	// musicStartedAt stamps when the ACTIVE tab's current area track began playing
	// (frame clock). It is App-level and single-valued — it does NOT park per tab
	// (an earlier comment claimed it did; it never has). Re-stamped by the active-tab
	// EventMusic handler on every track change. Its ONLY reader is resumeActiveTabMusic's
	// FALLBACK seek estimate (now-minus-start, capped) for the case where no per-URL
	// SwappedOutSnap exists; SwappedOutSnap is the drift-free primary. Because it tracks
	// the active tab, the estimate is only rough for a tab whose song changed while
	// backgrounded — acceptable: that path also lacks a snapshot, and a backgrounded
	// change that IS followed audibly (MusicAcrossTabs ON, routeBackgroundEvent) makes
	// the return a same-track no-op that never seeks. Zero = unknown (resume from top).
	musicStartedAt time.Time

	// scenery self-heal stamps (healScenery pacing)
	bgAskBase   string
	bgAskAt     time.Time
	deskAskBase string
	deskAskAt   time.Time
	spkAskBase  string // speaker sprite heal pacing
	spkAskAt    time.Time
	pairAskBase string // pair sprite heal pacing
	pairAskAt   time.Time

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
	rosterMenuOpen    bool
	rosterMenuFenceOn bool             // WE hold modalOn for the open menu (released on close)
	rosterMenuMe      bool             // the snapshot row is our own client
	rosterMenuAt      sdl.Point        // preferred top-left (clamped at draw)
	rosterMenuTab     int              // tab the menu opened on — auto-close on switch
	rosterMenuP       areaPlayer       // identity snapshot (uid/name/showname/ooc/ipid)
	rosterMenuItems   []rosterMenuItem // built at open; bounded by the action-kind count
	// Music panel overflow ("kebab") menu (musicheader.go): the anchored popup that
	// took the playback flags and the volume-view toggle off the header row. Same
	// modal-fence + tab-latch discipline as the roster menu above.
	musicMenuOpen         bool
	musicMenuFenceOn      bool      // WE hold modalOn for the open menu (released on close)
	musicMenuAt           sdl.Point // preferred top-left (clamped at draw)
	musicMenuTab          int       // tab the menu opened on — auto-close on switch
	musicMenuThemed       bool      // the panel drew inside a theme canvas when it opened (gates the volume-view row)
	liveDetailsArea       string    // area of the last auto /getarea pull; re-pull on area change
	suppressAreaEchoUntil time.Time // keep /gas/getarea reply lines out of OOC until this time — the WHOLE reply burst (a multi-area /gas spans several messages), not just the first
	// lastRosterFetch (the 3 s poll debounce) and rosterCmdUnsupported (the "this
	// server rejected /gas — stop sending it" latch) are DELETED along with the
	// automatic poll they served; see liveroster.go. The latch could only do harm
	// now: with every surviving fetch coming from a button, silently disabling one
	// for the rest of the session would turn a press into nothing at all.
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
	pairListScroll int32
	playerScroll   int32 // Players-tab roster scroll
	// Area-jump scroll follow (the "click to jump →" fix): the grouped roster
	// floats the just-jumped area to the top only on the next memo REBUILD (a
	// server round-trip — a PR/PU roster bump or /gas refresh reorders the list),
	// so a one-shot scroll at click time would target the pre-reorder position and
	// the row would then slide out from under it. Instead we latch the target and
	// re-nudge it into view every frame until it expires: the frame after the
	// rebuild reorders the list, the nudge follows the row to its new spot.
	areaJumpFollow      string    // target area name to keep scrolled into view ("" = disarmed)
	areaJumpFollowUntil time.Time // hard expiry: a lost/rejected transfer can never pin the scroll past this
	playerSort          int       // roster sort (players): 0=UID, 1=name, 2=speakers-first
	playerAreaSort      int       // /gas area-group order: 0=/gas, 1=A→Z, 2=most-populated
	shownameAdd         string    // M6: Settings "save a showname preset" input
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
	rasterScale      int
	rasterW          int32
	rasterSkinned    bool      // a drawn chatbox skin (theme OR per-character) gates theme text colors (readability)
	rasterChatMsg    theme.RGB // per-character chatbox ink the message raster was built with (cache key)
	rasterChatMsgHas bool
	rasterDevPct     int32 // #77: the device font scale the raster was built at — a UI-scale change must rebuild it

	msRaster   *render.MessageRaster
	rasterText string
	// rasterFailedText is the message whose raster could NOT be built, arming the
	// chatbox's plain-label fallback. Empty in the normal case. It exists because
	// the failure is otherwise invisible AND permanent: msRaster stays nil, the
	// cache key is never recorded, so every frame retries, fails the same way, and
	// draws no text at all under a showname that still paints — which reads as
	// "the client stopped receiving IC" rather than as a font/texture fault.
	rasterFailedText string
	// chatDiag is the last frame's chatbox geometry, shown in Debug → Session.
	// It exists because the "message text is cut off at a scaled UI" report could
	// not be reproduced on the machines the developers have: every candidate was
	// eliminated by measurement EXCEPT the numbers on the reporter's own screen,
	// which nobody could see. Printing them turns the affected machine into the
	// measuring instrument instead of asking it for another screenshot.
	chatDiag struct {
		vpH, boxY, boxH     int32
		textY, wrapW        int32
		lines, lineH        int32
		devScale, renderPct int32
		visible, total      int32
		grew                bool
	}
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
	// Creator easter eggs: the detection cache (text + resolved kind) is its own
	// component (eggstate.go), parked with the tab.
	eggs eggs
}

// banBoxState is the frozen snapshot the moderator ban/kick box works from (#130).
// It lives on sessionState so a tab switch parks and restores it with the rest of
// the session, and so a roster rebuild cannot repoint a destructive command.
type banBoxState struct {
	env       *env
	kind      int                   // 0 = closed, 1 = ban, 2 = kick, 3 = bulk ban, 4 = bulk kick
	uid       string                // snapshot: target UID (the identity anchor)
	ipid      string                // snapshot: target IPID (mod-only; "" until a /getarea enrich)
	name      string                // snapshot: display name for the box header
	dur       courtroom.BanDuration // chosen duration (ban only)
	customDur string                // a saved CUSTOM duration chip's canonical token ("45m"); "" = the enum preset dur applies
	reason    string                // typed reason
}

// sceneWarmState is the scene-residency keeper's memory. Warm-keeper futility
// latches (warmMaxHealChurns/warmMaxHealAsks) keep a per-base heal history,
// reset when the warm set changes or a new ceremony begins; it is bounded by the
// set itself (≤6 scene bases) and lazily allocated.
type sceneWarmState struct {
	env        *env
	demands    map[string]sceneHealState // per-base heal history; nil until first use
	set        [6]string                 // the warm set demands counts against
	lastDemand time.Time                 // evicted-base heal: one pool job per sceneWarmRedemandEvery, never per frame
	phase      courtroom.MessagePhase    // tracks the room's message phase so an idle→ceremony transition resets the heal budget even when the warm set is unchanged (the blankpost-same-emote case)
}

// activeWarmState is the active-emote keeper's memory: which character/emote the
// keeper is keeping resident, its per-base demand counts, and the throttle that
// pulls its re-demands onto the scene keeper's cadence (the old path submitted a
// pool job per 50 ms tick).
type activeWarmState struct {
	env        *env
	demands    map[string]int // per-base demand counts; nil until first use
	char       string         // identity: character folder…
	idx        int            // …and selected emote index
	lastDemand time.Time      // throttles re-demands to the scene keeper's cadence
}

// debugPanelState is the Extras → Debug panel's per-session view state: which
// section is showing, each list's scroll offset, and the two buffers reused
// across frames so the draw is alloc-free (each is built and read ONLY while its
// own tab is open). The font tab readout resolves through the same calls
// drawICLogList makes (fontpickdebug.go, the "funky fonts" readout).
type debugPanelState struct {
	open       bool        // the panel is open
	section    int         // active tab: 0 Session · 1 Packets · 2 Perf · 3 Cache · 4 Log · 5 Fonts
	pktScroll  int32       // Packets list scroll offset
	logScroll  int32       // Log list scroll offset
	pktBuf     []packetRec // reused recent()-into buffer (alloc-free draw)
	fontScroll int32       // Fonts list scroll offset
	fontBuf    []fontPickRow
}

// modDashState is the mod dashboard's per-session view and selection state (#13):
// the selected target, the roster and audit scroll offsets, the left-column view
// switch, the bounded session audit log, and the bulk tick set. All per-session
// (per-tab): a new server connection starts with an empty log and selection.
//
// targetUID is the selection, never a roster index — rebuildLiveRoster replaces
// the slice on every join/leave, so an index would repoint a ban at whoever
// shifted into that slot.
type modDashState struct {
	targetUID   string          // selected target's UID ("" = none)
	scroll      int32           // roster scroll offset
	showAudit   bool            // left column shows the session audit log instead of the roster
	audit       []modAuditEntry // bounded, session-scoped record of commands sent from the dash
	auditScroll int32           // audit list scroll offset
	selected    map[string]bool // UIDs ticked for a bulk ban / kick (lazily inited; ≤ modBulkCap)
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

// manifestFetch is one extensions.json autodetect result. status carries the
// RAW outcome (applied / absent / failed) alongside err, which the sender
// deliberately overwrites for the non-applied cases so the debug lane keeps its
// old wording — Settings → Formats needs to tell "this server publishes no
// format manifest" (normal, and exactly the case that may need one switch) apart
// from "we could not reach it" (transient), and err alone cannot.
type manifestFetch struct {
	host   string
	seeded int
	status seedStatus
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
	layout map[string]theme.Rect
	// tabBarSeeded records that seedTabBarDesignRect had to synthesize
	// "asyncao_tabbar" because the theme is silent about it (#14). Resolved on
	// the apply goroutine, WITH the seed itself and before the [overrides] fold,
	// so an author who writes that row is answered once instead of four times.
	tabBarSeeded bool
	emoteCell    [2]int // emote_button_size (w, h)
	emoteGap     [2]int // emote_button_spacing (x, y)
	// effectsIcon is courtroom_design.ini's `effects_icon_size` (AO2
	// courtroom.cpp:979) — the per-row icon size in the effects dropdown. A PAIR,
	// not a rect, so like emoteCell it cannot ride `layout` (and is invisible to
	// the unbound-key report, which only walks 4-component rects). 0,0 = the theme
	// is silent; the picker then sizes its strip from the row height.
	effectsIcon [2]int
	// themeObj is the loaded theme itself, carried through so the effects-overlay
	// roster resolve can walk its ladder off-thread WITHOUT re-running theme.Load
	// per resolve (spec §2.4: the theme-tier parse rides the theme apply). Safe to
	// share: *theme.Theme is read-only after Load — directory list plus four parsed
	// INIs, no lazily-filled caches — and every method the resolve calls is a pure
	// os.Stat walk. nil when the load failed; the roster then carries the origin
	// manifest alone, exactly as it did when the theme was unreadable.
	themeObj *theme.Theme
	// sidecar is the theme's asyncao_theme.ini, parsed on this goroutine (hard rule
	// 2 — it walks maps and allocates strings). nil for a stock AO2 theme, which is
	// most of them; the render side is nil-safe throughout.
	sidecar *theme.Sidecar
	// sidecarErr / sidecarTip are a REFUSED sidecar, rendered here into the two
	// strings the chip needs (themeerrchip.go). Both empty in the ordinary case.
	//
	// The distinction the pair carries is "the file exists and could not be read",
	// which is emphatically not "there is no file": a theme with no sidecar is a
	// stock AO2 theme and reports nothing. Rendered on this goroutine because both
	// format, and because the error's own text is the only place the failing section
	// and its numbers exist.
	sidecarErr string
	sidecarTip string
	// mediaPlan is the theme's declared art, PLANNED against the media budget before
	// a byte was read (thememedia.go). media holds what the plan admitted, decoded
	// and rasterised, keyed by the final theme:// key — so the render thread's job is
	// an upload loop and nothing else.
	mediaPlan themeMediaPlan
	media     map[string]*assets.Decoded
	// themeID is the folder name sanitized into the cache-key namespace. Resolved on
	// this goroutine because the folder is what identity IS (docs/THEME-FORMAT.md §1)
	// and the render thread must not re-derive it differently.
	themeID string
	// shownameAlign is courtroom_design.ini's `showname_align` — not a rect, so it
	// cannot ride `layout`. Parsed here, off the render thread, because
	// parseShownameAlign lowercases the raw value (hard rule 2).
	shownameAlign shownameAlign
	// shownameExtra is courtroom_design.ini's `showname_extra_width` in DESIGN
	// pixels: how much wider the showname box becomes at each rung of AO2's
	// widen-and-swap ladder (courtroom.cpp:3357). Zero or absent disables the
	// ladder outright — nine of the 74 reference themes do exactly that — which is
	// AO2's own behaviour and not a gap to fill in.
	shownameExtra int
	// charSel is char select's OWN geometry (#20), read from the same
	// courtroom_design.ini but kept apart from `layout` — see charselectlayout.go
	// for why it must never join a.themeRects. Every one of its twelve keys is
	// already resolved here (theme value, else the embedded default-theme table),
	// because AO2's per-key fallback needs a base/themes/ tree a streaming client
	// does not have.
	charSel charSelectDesign
	// palette is the courtroom_stylesheets.css color scheme (the "css
	// stuff"): applied over the kit colors, restored on theme switch.
	palette theme.Palette

	// diagnostics: where the skin came from, how many INI keys loaded,
	// and the dirs probed (so "nothing found" names the actual paths).
	chatboxFile string
	chatboxDir  string
	// chatboxStem is the base skin's file stem WITHOUT its extension — "chat" on
	// 64 of the 74 reference themes, "chatbox" on P5Theme, "chatblank" on the two
	// that ship only the blank plate. It is load-time state, not diagnostics: the
	// med/big ladder art is named after it (see chatboxMedSuffix), so hardcoding
	// "chat" would silently skip the ladder on every theme that spells it
	// differently.
	chatboxStem string
	iniKeys     int
	// unbound is the #21 unbound-design-key report: rect-shaped keys this theme
	// declares that reach no widget at all. "" when the theme is fully covered.
	// Built on the apply goroutine (it walks every design key), pushed to the
	// DEBUG LOG on land — never to the player's screen.
	unbound  string
	probed   []string
	inkGuard string // readability guard verdict ("" = colors kept)
	fontPath string // the active theme's bundled font file (.ttf/.otf), "" = none (#6)

	// Per-element courtroom_fonts.ini parity (#39): the resolved size/face/bold
	// table plus the DISTINCT face files it points at (≤ themeFaceCap), read
	// off-thread here so the render thread never touches disk. facePaths is the
	// dedupe key; faceNames feeds the settings status line.
	fontTable themeFontTable
	faceData  [][]byte
	facePaths []string
	faceNames []string
	// Per-panel USER font overrides: how many were applied, and the ones whose
	// family did not resolve to a file. The second is reported to the Settings
	// screen — a font name that silently does nothing is the single most likely
	// way this feature confuses someone, so it has to be visible where they typed
	// it rather than only in a debug log.
	panelFontsAsked   int
	panelFontsMissing []string
	// themeFontsMissing are families the THEME declared that resolved to no file
	// on this machine, and themeFontWarn is the ready-made sentence about them.
	// Built off-thread because deciding the wording needs a disk probe
	// (theme.HasAnyFontFile).
	themeFontsMissing []string
	themeFontWarn     string
	// report is the IMPORT REPORT (themereport.go): every degrade the sidecar
	// reader recorded, plus the missing families and the unbound design keys.
	// Built on this goroutine, bounded by themeReportCap, read once at the landing.
	report []string
	// userFontsDir is AsyncAO's own font drop folder, created on this pass so the
	// warning can name a path that actually exists — telling someone to put files
	// somewhere that is not there yet is most of the way to telling them nothing.
	userFontsDir string
	// panelFace / panelPct are the RESOLVED per-panel overrides: a 1-based index
	// into faceData and a percent, both 0 for "the user said nothing about this
	// element". Recorded here rather than written straight into fontTable so the
	// precedence ladder lives in one readable place on the render thread — see
	// landThemeFonts.
	panelFace [themeFontElemCount]int
	panelPct  [themeFontElemCount]int
	// backdropLuma is the average luma of the theme's own art behind each font
	// element's rect, and backdropSampled says whether there was any art to
	// measure. Filled here (res.images is only reachable on the apply goroutine —
	// hard rule 2) and consumed by the render-thread ink guard.
	// TWO arrays rather than one plus a sentinel, because the ZERO VALUE has to
	// mean "unknown": an int sentinel makes an unfilled themeApply — a theme that
	// failed to load, or one a test built by hand — read as a BLACK backdrop, and
	backdropLuma    [themeFontElemCount]int
	backdropSampled [themeFontElemCount]bool
}

// themeStemChatbox is the chatbox skin's stem in themeTex / T1.
const themeStemChatbox = "chatbox"

// themeStemChatboxMed / themeStemChatboxBig are the two WIDER chatbox skins of
// AO2's showname widen-and-swap ladder, as T1 stems.
//
// They are T1 KEYS, not file names: the files are named after whichever base
// candidate resolved (chatmed / chatboxmed / ...), which is only known once the
// theme has been walked, so they cannot live in themeImageStems' static table.
// The keys are fixed so every draw site names the same two textures.
const (
	themeStemChatboxMed = "chatboxmed"
	themeStemChatboxBig = "chatboxbig"
)

// chatboxMedSuffix / chatboxBigSuffix are what AO2 appends to the RESOLVED base
// skin's path to name the two wider variants (AO2-Client courtroom.cpp:3358 and
// :3362, `current_path + "med"` / `+ "big"`). The base is whichever of
// chat / chatbox / chatblank the theme actually shipped, so these are suffixes
// rather than whole names.
const (
	chatboxMedSuffix = "med"
	chatboxBigSuffix = "big"
)

// themeStemCharSelectBG is the char-select backdrop's stem in themeTex / T1
// (charselect_background.* on disk). Named because two files reach for it: the
// loader here and the canvas blit in charselectlayout.go, which must not drift.
const themeStemCharSelectBG = "charselect_bg"

// Readability guard for theme ink (playtest: chat text drew black however the
// colour was set). A theme's message/showname colors are designed
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
	return avgSkinLumaRect(img, img.Bounds(), step)
}

// avgSkinLumaRect is the same average over ONE sub-rectangle of img — what the
// per-element ink guard needs, because a courtroom background is one image and
// each font element sits over its own patch of it.
//
// Takes the rect rather than an (*image.RGBA).SubImage: SubImage allocates a
// fresh header per call, and this runs once per element per theme apply.
// PixOffset already accounts for the image origin, so the sampling body is
// unchanged from the whole-image case.
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
// speaking character's own char.ini (`get_chat(customchar)`, courtroom.cpp:3299)
// path_functions.cpp:181-189). theme.FindAssetMisc is exactly those two rungs and
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
		// The music plate (#21 label 2). It rides themeImageStems and NOT a slot's art
		// field on purpose: art feeds themeButtonStems, whose extension order is
		// PNG-first, and this is an animation layer in AO2 (InterfaceAnimationLayer,
		// courtroom.cpp:166) that themes often ship as a .gif. themeImageExts walks
		// webp -> apng -> gif -> png, which is the right ladder for a plate.
		"music_display":       {"music_display"},
		"lobbybackground":     {"lobbybackground", "loadingbackground"},
		themeStemCharSelectBG: {"charselect_background"},
		// The two char-select cell overlays AOCharButton ships as theme art (#20):
		// char_taken covers a taken button, char_selector is the 62x62 hover halo
		// (AO2-Client aocharbutton.cpp). Both optional — 40 and 38 of the 74 reference
		// themes ship them — and both tiny (60x60 / 62x62), so pinning them costs a few
		// KiB for the two most visible pieces of char-select parity.
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

// themeButtonStems maps courtroom_design.ini element keys to their AOButton
// art file stems (courtroom.cpp set_widgets setImage calls). DERIVED from the
// themeSlots registry (themeslots.go) so a key cannot carry art without a row.
// Loaded with a PNG-FIRST ext order: the bare splash names
// ("crossexamination.gif") and these button files ("crossexamination.png")
// collide by stem, and on every real theme the button is the png.
func themeButtonStems() map[string][]string {
	m := make(map[string][]string, len(themeSlots))
	for i := range themeSlots {
		if s := &themeSlots[i]; len(s.art) > 0 {
			m[s.key] = s.art
		}
	}
	return m
}

// themeButtonExts: png first — see themeButtonStems.
var themeButtonExts = []string{".png", ".webp", ".apng", ".gif"}

// themeLayoutKeys are the courtroom_design.ini rects the themed courtroom
// ingests, named exactly as AO2-Client set_size_and_pos reads them, DERIVED from
// the themeSlots registry (themeslots.go). "courtroom" + "viewport" are mandatory
// for the layout to activate; every other key resolves per element, and a key the
// theme omits simply has no widget — AO2 hides it too (courtroom.cpp:1334-1338).
// Sorted so a failing table test names a stable key rather than a map-order one.
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

// themeBtnStems is the precomputed theme:// stem ("btn/<design key>") for every
// courtroom_design.ini key the themed courtroom can draw as a button.
var themeBtnStems = func() map[string]string {
	m := make(map[string]string, len(themeLayoutKeys))
	for _, k := range themeLayoutKeys {
		m[k] = themeBtnPrefix + k
	}
	return m
}()

// themeBtnStem is themeBtnPrefix+key without the per-frame allocation. A key
// outside themeLayoutKeys falls back to the concat — still correct, just not
// free; no draw site reaches that today.
func themeBtnStem(key string) string {
	if s, ok := themeBtnStems[key]; ok {
		return s
	}
	return themeBtnPrefix + key
}

// themeTexKey is the T1 key for a theme texture stem; the scheme prefix can
// never collide with real asset URLs.
func themeTexKey(stem string) string { return "theme://" + stem }

const (
	// defaultEmoteCellPx / defaultEmoteGapPx are AO2's stock emote-grid metrics,
	// used when the design INI omits the tuples. Both are transcribed from
	// AO2-Client/bin/base/themes/default/courtroom_design.ini:63-64
	// (emote_button_spacing = 9, 9 / emote_button_size = 40, 40), because that is
	// literally what AO2 resolves for a theme that omits them: get_button_spacing
	// falls through get_config_value to the bundled DEFAULT THEME before it gives
	// up and returns 0,0. A streaming client has no such tree on disk, so these
	// constants stand in for it — see designPair.
	defaultEmoteCellPx = 40
	defaultEmoteGapPx  = 9
)

// designPairComponents is how many numbers a "x, y" design tuple carries. AO2's
// get_button_spacing bails when the split yields fewer (AO2-Client
// text_file_functions.cpp:208-211); we bail at the same point but NOT to the same
// value, and the difference is deliberate.
// text_file_functions.cpp:198-199 and the short-split branch returns it
const designPairComponents = 2

// designPair parses a "x, y" design tuple (emote_button_size and friends).
// (AO2-Client text_file_functions.cpp:193-216) converts both components with
// Negative components pass through, exactly as AO2 lets them. The consumer
// floors them — see minEmoteGapPx — because AO2 itself does not: emotes.cpp:70
// and charselect.cpp:160 both divide by (spacing + button size) with no guard,
// unlike evidence.cpp:211 which wraps the same expression in qMax(1, ...). A
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
