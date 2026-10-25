package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/veandco/go-sdl2/sdl"
)

// rosterState owns roster state.
type rosterState struct {
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
}
