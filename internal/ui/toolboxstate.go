package ui

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// toolboxState owns toolbox state.
type toolboxState struct {
	env *env
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
	toolboxFence compactToolboxLatch
	// pathStroke is the in-progress freehand stroke for the Sprite Style "draw a path" box
	// (#34 B2): raw box-relative points captured while dragging, sampled to <=6 waypoints on
	// release. pathDrawing = mid-stroke; pathPrevDown is its press-edge latch. Bounded.
	pathStroke   []sdl.Point
	pathDrawing  bool
	pathPrevDown bool
	// piecesPrevDown is the Hide-UI-pieces panel's press edge (its geometry moved to
	// env.wins).
	piecesPrevDown bool
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
	showWidgets bool
	// tabStripPaint is THIS FRAME's answer to which of the server-tab strip's two draw
	// sites paints it (chrometop.go). Taken once at the top of the courtroom pass and
	// replayed by both sites, so a mid-frame arm/disarm can neither paint it twice nor
	// drop it. Reset by App.Frame, so a frame with no courtroom pass falls back to the
	// over-everything site. Value type, App-level — the strip is client chrome.
	tabStripPaint tabStripPaintLatch
	// editorBannerPainted is "a layout editor's banner reached the screen this
	// frame" — set by the editors' own banner paint, reset by App.Frame, and read
	// once by drawMenuBar (menuBarPaintsNow). See noteEditorBanner in menubar.go for
	// why the paint decision needs this rather than the armed flags.
	editorBannerPainted bool
}
