package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/veandco/go-sdl2/sdl"
)

// overlayState owns overlays state.
type overlayState struct {
	env *env
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
	// Remote-speaker char.ini metadata (blips + chatbox skin) — URL-keyed cache
	// + async fetch results (charmeta.go). Bounded; per-server by key structure.
	charMetaCache map[string]charMeta
	charMetaRes   chan charMetaFetch
}
