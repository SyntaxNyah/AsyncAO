package ui

import (
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/veandco/go-sdl2/sdl"
)

// skinState is App's "applied theme (chatbox" banner group lifted into a named component: the fields
// used to sit flat on App, readable at depth 1 from every ui file. They are
// reached as a.skin.<field> now.
type skinState struct {
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
	themeShownameExtra int
	// themeFonts is the applied theme's per-element courtroom_fonts.ini table
	// (#39): declared point size folded to a percent, the resolved family's face
	// slot, and <id>_bold. The ZERO table means "this theme dresses nothing",
	// which every accessor in themefonts.go turns back into the pre-#39 path.
	themeFonts themeFontTable
	// Theme courtroom geometry (courtroom_design.ini): design-space rects
	// + emote grid metrics; themeLay caches the window-scaled rects.
	themeRects     map[string]theme.Rect
	themeRectsOrig map[string]theme.Rect
	themeEmoteCell [2]int
	themeEmoteGap  [2]int
	themeLay       themeLayoutCache
}
