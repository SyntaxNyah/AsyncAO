package ui

// Character select's own design geometry and canvas. AO2's char select is a second fixed-size canvas
// (charselect.cpp:83 setFixedSize(f_charselect.width, f_charselect.height); fallback 714x668 at :79).
// AO2 falls back per key to the default theme (text_file_functions.cpp:245).
//
// Not in a.themeRects, for two reasons. Every key there becomes an editable box in the courtroom layout editor, and
// themeLayoutIn discards keys outside the courtroom design space (spectator Y=640 > 579) and clamps char_select's
// height.
//
// Only fitDesignCanvas is shared, so both canvases honour one ThemeFit. The art stretches at the exact ratio while
// scaled rects truncate, and that drift grows with the coordinate. Widget rects tolerate it and keep the canvas scale;
// the grid maps every cell through the canvas rect (charselectgrid.go, scaleToCanvas) to stay on the art.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// The courtroom_design.ini keys AO2's char select reads: rects (charselect.cpp:89-93, plus construct_char_select) and
// charSelectSpacingKey, an "x, y" tuple (charselect.cpp:156).
const (
	// charSelectCanvasKey sizes the whole screen. AO2 uses only width and height, so x/y are ignored:
	// corpus themes declare `char_select = 0, 121, ...` and would otherwise shift by 121 px.
	charSelectCanvasKey = "char_select"
	// charSelectButtonsKey is the grid area; buttons are placed relative to it (charselect.cpp:246-276).
	charSelectButtonsKey = "char_buttons"
	// charSelectSpacingKey is the "x, y" gutter between buttons.
	charSelectSpacingKey = "char_button_spacing"
	// charSelectListKey is the left-hand name list (a QTreeWidget in AO2).
	charSelectListKey = "char_list"
	// charSelectSearchKey is the name filter field.
	charSelectSearchKey = "char_search"
	// AO2's two filter checkboxes, both default checked (charselect.cpp:99-100).
	charSelectPasswordedKey = "char_passworded"
	charSelectTakenKey      = "char_taken"
	// charSelectBackKey leaves for the server list.
	charSelectBackKey = "back_to_lobby"
	// charSelectPasswordKey is the per-character password field.
	charSelectPasswordKey = "char_password"
	// Page arrows: AO2's char select is paged, not scrolled (charselect.cpp:116-117).
	charSelectLeftKey  = "char_select_left"
	charSelectRightKey = "char_select_right"
	// charSelectSpectatorKey joins without a character.
	charSelectSpectatorKey = "spectator"
)

// charSelectDefaultRects is the stock AO2 default theme's table, from bin/base/themes/default/courtroom_design.ini.
// AO2 falls back per key, not per file, so partial themes need the rest; a streaming client has no base/themes/ tree.
// Matches the stock art to the pixel: 7 columns from x=226 and 9 rows from y=36, 60 px cells at pitch 67.
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
	// courtroom_design.ini:155's `char_button_spacing = 7, 7`, the same per-key fallback.
	charSelectDefaultSpacingX = 7
	charSelectDefaultSpacingY = 7
	// charSelectButtonPx is AO2's button edge in design pixels (courtroom.h:561), the cell size in the grid
	// formula at charselect.cpp:160-162.
	charSelectButtonPx = 60
)

// charSelectDesign is one theme's geometry in design pixels with every key resolved, so a partial theme and a
// complete one draw identically.
type charSelectDesign struct {
	r       map[string]theme.Rect
	spacing [2]int
}

// readCharSelectDesign resolves the keys for a loaded theme on the theme-apply goroutine (hard rules 1 and 2). A short
// tuple is rejected by theme.ElementRect, matching AO2 (text_file_functions.cpp:220-232), and falls to the defaults.
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
		// designPair, not a private parser: it honours a legitimate 0 (themes declare `emote_button_spacing = 0, 0`).
		// A missing key falls back to the stock 7, 7.
		d.spacing = designPair(t, charSelectSpacingKey, charSelectDefaultSpacingX, charSelectDefaultSpacingY)
	}
	return d
}

// canvasSize is char_select's width and height. A degenerate size falls back to stock 714x668, as AO2 does
// (charselect.cpp:77-80).
func (d charSelectDesign) canvasSize() (int32, int32) {
	if r, ok := d.r[charSelectCanvasKey]; ok && r.W > 0 && r.H > 0 {
		return int32(r.W), int32(r.H)
	}
	def := charSelectDefaultRects[charSelectCanvasKey]
	return int32(def.W), int32(def.H)
}

// charSelectLayoutCache is the window-scaled char-select geometry, the twin of themeLayoutCache.
// Rebuilt when window size, chrome band, fit mode or theme changes; render reads are map probes.
type charSelectLayoutCache struct {
	// valid means this screen draws on the theme's canvas. False keeps the classic full-window layout.
	valid      bool
	winW, winH int32
	// topOff is the chrome band above the canvas; in the key because a tab opening can change it alone.
	topOff int32
	fit    int // ThemeFit mode it was built for
	// scaleX and scaleY differ only in Stretch. The cell scales by one factor (cellScale), as AO2 uses one
	// themeScalingFactor().
	scaleX, scaleY float64
	// area is the scaled canvas the backdrop blits into. AO2 scales the art with Qt::IgnoreAspectRatio
	// (aoimage.cpp:29-31), so the art's native size is irrelevant.
	area sdl.Rect
	// clip is area intersected with the window below the chrome band, the region this screen may paint and hit-test.
	// It differs from area only under Crop and Custom, where the canvas can run off the window.
	clip sdl.Rect
	// spacingX/Y are char_button_spacing at canvas scale, so the grid never repeats the multiply.
	spacingX, spacingY int32
	r                  map[string]sdl.Rect // scaled, window-absolute

	// Design-space values follow, because the grid drifts off the art if laid out from scaled ones.
	// Mappings in charselectgrid.go are `design * area.<extent> / design<extent>`.
	designW, designH int32
	// spacingDesignX/Y are char_button_spacing as declared. AO2's counts (charselect.cpp:160-162) are scale-invariant,
	// while three truncated scaled integers are not: the stock table would give 8 columns at 800x600 where the art
	// paints 7.
	spacingDesignX, spacingDesignY int32
	// buttonsDesign is char_buttons in design pixels, set only when the scaled rect survived into r.
	buttonsDesign sdl.Rect
}

// rect fetches a usable scaled rect; absent when the theme hid the element (the 11037 convention) or it scaled below
// minThemedElementPx.
func (l *charSelectLayoutCache) rect(key string) (sdl.Rect, bool) {
	r, ok := l.r[key]
	if !ok || r.W <= 0 || r.H <= 0 {
		return sdl.Rect{}, false
	}
	return r, true
}

// cellScale is the one factor the cell and gutter scale by. AO2 uses a single factor (charselect.cpp:158,
// text_file_functions.cpp:212-213). min, because the looser axis would overflow char_buttons and drop a column.
func (l *charSelectLayoutCache) cellScale() float64 {
	if l.scaleY < l.scaleX {
		return l.scaleY
	}
	return l.scaleX
}

// charSelectThemed reports whether char select draws on the theme's canvas, by the courtroom's own condition: "use the
// theme's layout" is one preference. Tested directly, since themeLayout() would build the courtroom's cache from this
// screen.
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

// charSelectLayout is the char-select canvas fitted into the window below the chrome band; cached, called once per
// frame.
func (a *App) charSelectLayout(w, h int32) *charSelectLayoutCache {
	return a.charSelectLayoutIn(w, h, a.topChromeH())
}

// charSelectLayoutIn builds the cache for a (w, h) box with `top` px reserved at its top edge.
func (a *App) charSelectLayoutIn(w, h, top int32) *charSelectLayoutCache {
	lay := &a.charSelLay
	fit := a.d.Prefs.Load().ThemeFit
	// charSelectThemed is in the cache test because the preference can flip with nothing else changing,
	// which would leave the screen letterboxed. It is alloc-free.
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
	// The clip is trimmed, not the canvas, so Crop's overflow stays anchored and Custom can pan upward, yet this screen
	// never paints or hit-tests inside chrome. Integer arithmetic: sdl.Rect.Intersect takes pointers, which would
	// escape to the heap. W or H can reach 0 under an extreme Custom pan, meaning nothing to draw; callers must not
	// clip to it.
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

// These rects are not run through clampRectInto. The stock char_buttons overhangs its canvas by 30 px by design: Qt
// clips the paint and keeps the origin, and shifting left would move every column off the art. The draw clips to
// lay.clip. Counts are AO2's, in design pixels (charselect.cpp:160-162). AO2 divides with no guard, unlike
// evidence.cpp:211, so negative spacing crashes it; here the divisor is floored at charGridMinPitchPx.

// drawCharSelectBackdrop paints the background. On the theme canvas the art goes into lay.area with flat colour around
// it, as AO2 resizes the background to the char_select rect (charselect.cpp:85) and stretches into it
// (aoimage.cpp:29-31).
func (a *App) drawCharSelectBackdrop(w, h int32, lay *charSelectLayoutCache) {
	c := a.ctx
	if !lay.valid {
		// No theme canvas: stretch to the window.
		a.drawScreenBackdrop(w, h, themeStemCharSelectBG)
		return
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground) // the bars around the canvas
	page, ok := a.themePage(themeStemCharSelectBG)
	if !ok {
		return // theme ships no charselect art; the canvas is just empty room
	}
	// &lay.area into cgo would allocate per frame, so use the shared scratch; SDL doesn't retain the pointer.
	// No rotation branch: per-key angles exist only in the courtroom layout cache.
	c.cgoRect = lay.area
	_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
}
