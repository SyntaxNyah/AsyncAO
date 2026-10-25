package ui

import (
	"image"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
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

	charsel charGridState
	rcache  iconCacheState
	emoji   emojiUIState

	reactUI reactUIState

	compose   composeState
	pairbox   pairBoxState
	emotepick emotePickState
	musicTab  musicTabState
	areaTab   areaTabState
	favstyle  favStyleState
	chipUI    chipRowState
	chatlog   chatLogState

	// --- case notebook (per-server pins) ---
	notebook     *config.Notebook
	noteInput    string
	noteScroll   int32
	noteCache    []string // rev-keyed Lines() snapshot
	noteCacheRev int64

	courtkeys keyBindState

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

	cam cameraState

	courtxtra courtExtrasState
	timer     timerState
	evid      evidenceState
	// The evidence image picker USED TO LIVE HERE as evidPickerOpen/Search/Scroll/
	// Files/Lower/Busy/Scanned/Res (a modal thumbnail grid fed by a local scan plus
	// the server's evidence/ autoindex). Both of its sources were wrong for the
	// playtest — it offered the CASE's existing images and the STREAM instead of the
	// user's own folders, listed credit.txt among them, and showed no folders at all
	// — so the "Choose" thumbnail grid was retired and its button merged into Browse,
	// which one purpose now backs: the merged local asset source browser
	// (demobrowser.go + mergebrowse.go, purposeEvidenceImage).

	inibrowse iniBrowseState

	wardrobe wardrobePickState

	// bgPick is the "change background" modal (background/ autoindex grid).
	bgPick bgPicker

	audio audioLevelState

	dialogs     dialogPromptState
	musicDucked bool

	demand demandClockState

	roster rosterState

	raster rasterCacheState

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
	env        *env
	open       bool        // the panel is open
	section    int         // active tab: 0 Session · 1 Packets · 2 Perf · 3 Cache · 4 Log · 5 Fonts
	pktScroll  int32       // Packets list scroll offset
	logScroll  int32       // Log list scroll offset
	pktBuf     []packetRec // reused recent()-into buffer (alloc-free draw)
	fontScroll int32       // Fonts list scroll offset
	fontBuf    []fontPickRow
}

// modDashState is the mod dashboard's per-session view and selection state (#13):
type modDashState struct {
	env         *env
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
