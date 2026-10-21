package ui

// CHARACTER SELECT'S OWN DESIGN GEOMETRY AND ITS OWN CANVAS TRANSFORM (#20).
// WHAT WENT WRONG (the issue #20 screenshot, reproduced from source, not guessed).

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The twelve courtroom_design.ini entries AO2's char select reads. Eleven are
// rects (Courtroom::set_char_select's set_size_and_pos calls at
// AO2-Client src/charselect.cpp:89-93, plus the four widgets built in
// construct_char_select); charSelectSpacingKey is the odd one out, an "x, y"
// tuple read through get_button_spacing (charselect.cpp:156).
const (
	// charSelectCanvasKey sizes the whole screen. AO2 uses its WIDTH and HEIGHT
	// ONLY — setFixedSize(w, h) takes no position — so the x/y components are
	// deliberately ignored here too. That is not pedantry: three corpus themes
	// (`WebAO Client`, `webao client comic sans edition`,
	// `microsoft surface 4k webao`) declare `char_select = 0, 121, ...`, and
	// treating the rect as positioned would offset all three by 121 px.
	charSelectCanvasKey = "char_select"
	// charSelectButtonsKey is the grid area. Every character button is placed
	// RELATIVE to it (put_button_in_place, charselect.cpp:246-276).
	charSelectButtonsKey = "char_buttons"
	// charSelectSpacingKey is the "x, y" gutter between buttons.
	charSelectSpacingKey = "char_button_spacing"
	// charSelectListKey is the left-hand name list (a QTreeWidget in AO2).
	charSelectListKey = "char_list"
	// charSelectSearchKey is the name filter field.
	charSelectSearchKey = "char_search"
	// charSelectPasswordedKey / charSelectTakenKey are AO2's two filter
	// checkboxes, both default CHECKED (charselect.cpp:99-100).
	charSelectPasswordedKey = "char_passworded"
	charSelectTakenKey      = "char_taken"
	// charSelectBackKey leaves for the server list.
	charSelectBackKey = "back_to_lobby"
	// charSelectPasswordKey is the per-character password field.
	charSelectPasswordKey = "char_password"
	// charSelectLeftKey / charSelectRightKey are the PAGE arrows: AO2's char
	// select is paged, not scrolled (charselect.cpp:116-117, :146-154).
	charSelectLeftKey  = "char_select_left"
	charSelectRightKey = "char_select_right"
	// charSelectSpectatorKey joins without a character.
	charSelectSpectatorKey = "spectator"
)

// charSelectDefaultRects is the stock AO2 default theme's char-select table,
// transcribed from AO2-Client/bin/base/themes/default/courtroom_design.ini with
// the source line beside each value.
// theme in issue #20's screenshot, among them.
// Verified against the stock art: a dark-run scan of the default theme's
// 714×668 charselect_background.png finds exactly 7 horizontal runs starting at
// x=226 and 9 vertical runs starting at y=36, each 60 px, pitch 67 — which is
var charSelectDefaultRects = map[string]theme.Rect{
	charSelectCanvasKey:     {X: 0, Y: 0, W: 714, H: 668},    // courtroom_design.ini:150
	charSelectListKey:       {X: 25, Y: 36, W: 190, H: 596},  // :151
	charSelectBackKey:       {X: 5, Y: 5, W: 91, H: 23},      // :152
	charSelectPasswordKey:   {X: 297, Y: 7, W: 120, H: 22},   // :153
	charSelectButtonsKey:    {X: 226, Y: 36, W: 518, H: 596}, // :154
	charSelectLeftKey:       {X: 100, Y: 5, W: 43, H: 24},    // :156
	charSelectRightKey:      {X: 146, Y: 5, W: 43, H: 24},    // :157
	charSelectSpectatorKey:  {X: 317, Y: 640, W: 80, H: 23},  // :158
	charSelectSearchKey:     {X: 420, Y: 7, W: 120, H: 22},   // :188
	charSelectPasswordedKey: {X: 545, Y: 7, W: 100, H: 22},   // :193
	charSelectTakenKey:      {X: 635, Y: 7, W: 80, H: 22},    // :196
}

const (
	// charSelectDefaultSpacingX / Y are courtroom_design.ini:155's
	// `char_button_spacing = 7, 7`, the same per-key fallback as the table above.
	charSelectDefaultSpacingX = 7
	charSelectDefaultSpacingY = 7
	// charSelectButtonPx is AO2's character-button edge in DESIGN pixels:
	// `const int button_width = 60;` (AO2-Client src/courtroom.h:561). It is the
	// cell size in the grid formula at charselect.cpp:160-162 and the size
	// AOCharButton stretches its icon into, so the whole grid hangs off it.
	charSelectButtonPx = 60
)

// charSelectDesign is ONE theme's char-select geometry, in DESIGN-space pixels,
// with every one of the twelve keys already resolved: the theme's value where it
// declares one, charSelectDefaultRects / charSelectDefaultSpacing* where it does
// not. Resolving at parse time (off the render thread) is what lets the draw path
// treat a partial theme and a complete one identically.
type charSelectDesign struct {
	r       map[string]theme.Rect
	spacing [2]int
}

// readCharSelectDesign resolves the twelve keys for a loaded theme. Called from
// the theme-apply GOROUTINE (hard rules 1 and 2: it reads INIs, never SDL, never
// App state), and landed on the render thread by pollThemeApply.
// A theme value wins only when the INI parses it as a rect at all; theme.ElementRect
// already rejects a short tuple, matching AO2's get_element_dimensions, which
// leaves width/height at -1 when the split yields fewer than four components
func readCharSelectDesign(t *theme.Theme) charSelectDesign {
	d := charSelectDesign{
		r:       make(map[string]theme.Rect, len(charSelectDefaultRects)),
		spacing: [2]int{charSelectDefaultSpacingX, charSelectDefaultSpacingY},
	}
	for key, def := range charSelectDefaultRects {
		d.r[key] = def
		if t == nil {
			continue
		}
		if r, ok := t.ElementRect(key); ok {
			d.r[key] = r
		}
	}
	if t != nil {
		// designPair, not a private parser: it is the same "x, y" shape
		// get_button_spacing reads, and it was fixed in an earlier wave to honour a
		// LEGITIMATE 0 (twelve shipped themes declare `emote_button_spacing = 0, 0`
		// and mean flush buttons). Re-implementing the parse here would re-break
		// exactly that. A missing key falls back to the stock 7, 7 for the same
		// reason the rect table exists: AO2 would have found it in the default theme.
		d.spacing = designPair(t, charSelectSpacingKey, charSelectDefaultSpacingX, charSelectDefaultSpacingY)
	}
	return d
}

// canvasSize is the design canvas: char_select's WIDTH and HEIGHT, x/y discarded
// (see charSelectCanvasKey). A theme declaring a degenerate size falls back to the
// stock 714×668 rather than collapsing the screen — the same shape as AO2's
// `if (f_charselect.width < 0 || f_charselect.height < 0) setFixedSize(714, 668)`
// at charselect.cpp:77-80.
func (d charSelectDesign) canvasSize() (int32, int32) {
	if r, ok := d.r[charSelectCanvasKey]; ok && r.W > 0 && r.H > 0 {
		return int32(r.W), int32(r.H)
	}
	def := charSelectDefaultRects[charSelectCanvasKey]
	return int32(def.W), int32(def.H)
}

// charSelectLayoutCache is the window-scaled char-select geometry — the exact twin
// of themeLayoutCache, for the other canvas. Rebuilt only when the window size, the
// app-chrome band, the fit mode or the theme changes; every render-loop read is a
// plain map probe on pre-scaled rects.
type charSelectLayoutCache struct {
	// valid means "this screen draws on the theme's canvas, and the numbers below
	// are current". False keeps char select on its classic full-window layout —
	// which is the right answer for a themeless install, where there is no canvas
	// and no backdrop art to align to.
	valid      bool
	winW, winH int32
	// topOff is the app-chrome band reserved ABOVE the canvas (#14, topChromeH), part
	// of the cache key for the same reason it is in themeLayoutCache: the band can
	// change with no window, fit or theme change behind it (a tab opening is enough).
	topOff int32
	fit    int // ThemeFit mode it was built for
	// scaleX/scaleY are per-axis and differ ONLY in Stretch, exactly as in
	// themeLayoutCache. The character cell must scale by ONE factor whatever the
	// window shape, because AO2 multiplies button_width by the single
	// themeScalingFactor() — see cellScale.
	scaleX, scaleY float64
	// area is the whole scaled canvas. The backdrop blits into precisely this rect:
	// AO2 resizes ui_char_select_background to the char_select rect and
	// AOImage::setImage then scales the art into it with Qt::IgnoreAspectRatio
	// (aoimage.cpp:29-31), so the art's own native size is irrelevant. That is what
	// makes `alter ego (mobi)` — 576×639 art on a 537×1080 canvas — look right.
	area sdl.Rect
	// clip is area intersected with the window BELOW the app-chrome band: the region
	// this screen may paint and hit-test in. Native, Letterbox and Stretch always put
	// the whole canvas under the band, so clip == area there; Crop scales UP by design
	// and Custom can be panned anywhere, and in those two the canvas legitimately runs
	// off every window edge, the band's included.
	clip sdl.Rect
	// spacingX/spacingY are char_button_spacing at the canvas scale — pre-scaled here
	// so the grid stage never repeats the multiply per frame.
	spacingX, spacingY int32
	r                  map[string]sdl.Rect // scaled, window-absolute

	// --- DESIGN-SPACE values, kept because the grid cannot be laid out from the
	// scaled ones without drifting off the artwork (see the header comment).
	//
	// designW/designH are the canvas AO2 would have called setFixedSize with. Every
	// mapping in charselectgrid.go is `design * area.<extent> / design<extent>`, so
	// both halves of that ratio have to be reachable from the cache.
	designW, designH int32
	// spacingDesignX/Y are char_button_spacing AS DECLARED, before any scaling.
	// AO2 computes its column and row COUNTS from the design rect and the design
	// spacing (charselect.cpp:160-162 runs on unscaled widget geometry at scale 1,
	// and on values multiplied by one INTEGER themeScalingFactor above it), so the
	// count is scale-invariant there. Deriving it from three independently truncated
	// scaled integers, as the first version did, is not: the stock table yields 8
	// columns at 800x600 where the art paints 7.
	spacingDesignX, spacingDesignY int32
	// buttonsDesign is char_buttons in design pixels, and is set ONLY when the
	// scaled twin survived into r — so the grid stage's usability gate stays the
	// single `rect(charSelectButtonsKey)` probe it always was.
	buttonsDesign sdl.Rect
}

// rect fetches a usable scaled rect. Absent when the theme hid the element (the
// 11037 convention) or the canvas scale shrank it below minThemedElementPx —
// both filtered at build time, so this stays a map probe.
func (l *charSelectLayoutCache) rect(key string) (sdl.Rect, bool) {
	r, ok := l.r[key]
	if !ok || r.W <= 0 || r.H <= 0 {
		return sdl.Rect{}, false
	}
	return r, true
}

// cellScale is the ONE factor the character cell and its gutter scale by — the
// char-select twin of emoteCellScale, and for the identical reason. AO2 multiplies
// button_width and char_button_spacing by the single Options::themeScalingFactor()
// (charselect.cpp:158, text_file_functions.cpp:212-213), so the cell is square at
// every window shape; our scale is per-axis and, in Stretch only, scaleX != scaleY.
// min() rather than max(): a cell grown by the looser axis would overflow
// char_buttons on the tighter one and silently drop a whole column off the grid.
func (l *charSelectLayoutCache) cellScale() float64 {
	if l.scaleY < l.scaleX {
		return l.scaleY
	}
	return l.scaleX
}

// charSelectThemed reports whether char select draws on the theme's canvas.
// Every corpus theme that ships any geometry at all declares both keys (courtroom
// and viewport are the only two rects present in 100% of the corpus), so this
// costs no real theme its canvas.
func (a *App) charSelectThemed() bool {
	if len(a.charSelDesign.r) == 0 || !a.d.Prefs.Load().ThemeLayoutOn {
		return false
	}
	if r, ok := a.themeRects["courtroom"]; !ok || r.W <= 0 || r.H <= 0 {
		return false
	}
	_, ok := a.themeRects["viewport"]
	return ok
}

// charSelectLayout is THE accessor: the char-select canvas fitted into the window
// with the app-chrome band (#14) reserved above it. Cached; the render loop calls
// it once per frame.
func (a *App) charSelectLayout(w, h int32) *charSelectLayoutCache {
	return a.charSelectLayoutIn(w, h, a.topChromeH())
}

// charSelectLayoutIn builds the cache for a (w, h) box with `top` px reserved at its
// top edge. Split out from charSelectLayout so the band is an explicit parameter the
// tests can drive, mirroring themeLayoutIn.
func (a *App) charSelectLayoutIn(w, h, top int32) *charSelectLayoutCache {
	lay := &a.charSelLay
	fit := a.d.Prefs.Load().ThemeFit
	// charSelectThemed() is part of the TEST, not just the build: it reads a
	// preference ("use the theme's layout") that the user can flip with no window,
	// band, fit or theme change behind it. Left out, a cache built while the canvas
	// was on would keep the screen letterboxed after the toggle went off — the same
	// class of stale-key bug the server-tab strip's parked-ness flag was added for.
	// It is alloc-free and short-circuits on the commonest case (no design table at
	// all) before it touches prefs, so the cache-hit path stays cheap.
	if lay.valid && lay.winW == w && lay.winH == h && lay.topOff == top && lay.fit == fit &&
		a.charSelectThemed() {
		return lay
	}
	*lay = charSelectLayoutCache{winW: w, winH: h, topOff: top, fit: fit}
	if !a.charSelectThemed() {
		return lay // classic full-window char select; nothing to scale
	}
	designW, designH := a.charSelDesign.canvasSize()
	f := a.fitDesignCanvas(designW, designH, w, h, top, fit)
	lay.scaleX, lay.scaleY, lay.area = f.scaleX, f.scaleY, f.area
	// PAINT/HIT REGION = the canvas intersected with the window BELOW the app-chrome
	// band. In Native, Letterbox and Stretch the canvas already sits wholly inside
	// that box and this is a no-op (clip == area). Crop scales UP on purpose and
	// Custom's pan can push the canvas anywhere, so in those two the canvas
	// legitimately runs off every window edge, the band's included — where the menu
	// bar and the server-tab strip paint over it anyway. Trimming the CLIP rather
	// than the canvas is what keeps Crop's overflow anchored where the mode says and
	// leaves Custom's upward pan possible, while still guaranteeing this screen never
	// paints into, or hit-tests inside, chrome that belongs to somebody else.
	// A degenerate result (W or H == 0) is possible — an extreme Custom pan can carry
	// the whole canvas off-screen — and means exactly what it says: nothing to draw
	// this frame. Callers must treat it as such rather than clipping to it.
	lay.clip = lay.area
	if lay.clip.Y < top {
		lay.clip.H -= top - lay.clip.Y
		lay.clip.Y = top
	}
	if lay.clip.X < 0 {
		lay.clip.W += lay.clip.X
		lay.clip.X = 0
	}
	if over := lay.clip.X + lay.clip.W - w; over > 0 {
		lay.clip.W -= over
	}
	if over := lay.clip.Y + lay.clip.H - h; over > 0 {
		lay.clip.H -= over
	}
	if lay.clip.W < 0 {
		lay.clip.W = 0
	}
	if lay.clip.H < 0 {
		lay.clip.H = 0
	}
	cs := lay.cellScale()
	lay.designW, lay.designH = designW, designH
	lay.spacingDesignX = int32(a.charSelDesign.spacing[0])
	lay.spacingDesignY = int32(a.charSelDesign.spacing[1])
	lay.spacingX = int32(float64(lay.spacingDesignX) * cs)
	lay.spacingY = int32(float64(lay.spacingDesignY) * cs)
	lay.r = make(map[string]sdl.Rect, len(a.charSelDesign.r))
	for key, r := range a.charSelDesign.r {
		if key == charSelectCanvasKey {
			continue // the canvas itself is lay.area, never a widget
		}
		if int32(r.X) > designW || int32(r.Y) > designH {
			continue // off-design = hidden (AO2's 11037 convention, both axes)
		}
		sr := sdl.Rect{
			X: lay.area.X + int32(float64(r.X)*lay.scaleX),
			Y: lay.area.Y + int32(float64(r.Y)*lay.scaleY),
			W: int32(float64(r.W) * lay.scaleX),
			H: int32(float64(r.H) * lay.scaleY),
		}
		if sr.W < minThemedElementPx || sr.H < minThemedElementPx {
			continue // sloppy theme data (or a canvas scaled to nothing), not a widget
		}
		lay.r[key] = sr
		if key == charSelectButtonsKey {
			lay.buttonsDesign = sdl.Rect{X: int32(r.X), Y: int32(r.Y), W: int32(r.W), H: int32(r.H)}
		}
	}
	lay.valid = true
	return lay
}

// NOTE FOR THE GRID STAGE — why these rects are NOT run through clampRectInto.
// The courtroom SHIFTS an overhanging widget back inside its canvas, because Qt
// amputated it at the window edge and a half-widget is worse than a moved one. Doing
// that here would be a bug, and the stock default theme proves it: char_buttons is
// the desync issue #20 reported, reintroduced by the cure. The grid keeps the design

// drawCharSelectBackdrop paints the char-select background.
// Sharing lay.area is the structural half of the #20 fix: the backdrop, the widgets
// and the grid are all placed inside ONE rectangle, so they move and resize as a
// unit in every fit mode — Stretch included, where the two axes scale differently.
func (a *App) drawCharSelectBackdrop(w, h int32, lay *charSelectLayoutCache) {
	c := a.ctx
	if !lay.valid {
		// No theme canvas: the pre-#20 behaviour, unchanged.
		a.drawScreenBackdrop(w, h, themeStemCharSelectBG)
		return
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground) // the bars around the canvas
	page, ok := a.themePage(themeStemCharSelectBG)
	if !ok {
		return // theme ships no charselect art; the canvas is just empty room
	}
	// &lay.area into cgo would heap-allocate per frame even on the branches above
	// (escape analysis is static) — the shared scratch is the house idiom, and SDL
	// copies the rect during the call without retaining the pointer.
	//
	// Plain Copy, no CopyEx rotation branch (unlike drawScreenBackdrop): A4's
	// per-key angles are baked into the COURTROOM layout cache from the themed
	// editor's key set, and char select is deliberately outside both — there is no
	// key here that can ever carry a nonzero angle.
	c.cgoRect = lay.area
	_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
}
