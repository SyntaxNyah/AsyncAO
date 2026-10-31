package ui

// Free elements: theme boxes the AO2 design registry has no row for, drawn inside a zero-allocation frame.
// Everything is baked once into a fixed array (themeelementbake.go, which holds all string work); the draw path reads
// values only, paints at three fixed points because the themed pass is hand-ordered, and dispatches through a table
// of top-level funcs.

import (
	"math"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// bakedEffect is an element's motion resolved at bake time; resolveElementEffect turns it into per-frame terms.
type bakedEffect struct {
	kind     theme.EffectKind
	periodMs int32
	ampPct   int16
}

// bakedElement is one resolved element, so the painter needs no lookup; fields are ordered by size as App holds 96.
type bakedElement struct {
	// r is absolute window pixels, after anchor, space transform, canvas fit and clamp.
	r sdl.Rect
	// page is nil for non-image kinds and for media not loaded or evicted; the painter then draws nothing.
	page *render.TexturePage
	// label is already truncated to r at bake (AO2's truncate_label_text), because truncation allocates.
	label string
	// phase is the element's own clock offset.
	phase    time.Duration
	slice    [4]int32  // 9-slice insets, source px, (left, top, right, bottom) — R9
	strokePx int32     // outline width; ElemPane reads it as the divider width
	col      sdl.Color // Fill:  gradient stop A / shape fill / pane plate / text ink
	col2     sdl.Color // Fill2: gradient stop B
	stroke   sdl.Color
	tint     sdl.Color // multiplied into image/gen art at draw
	fx       bakedEffect
	// cond is the interned index of VisibleWhen.Value, or condAlwaysVisible; interning keeps the check an integer.
	cond     int16
	kind     theme.ElementKind
	fit      theme.ElementFit
	condAxis theme.ConditionAxis
	opacity  uint8
	ang      uint8 // 360/256 angle byte — the ThemeRectRotations convention
	// fontIdx is the face index for ElemText; format 1 leaves it at elemFontChrome (see paintElementText).
	fontIdx uint8
	clock   uint8
	// grad is the gradient axis as a 360/256 angle byte (0 left-to-right, 64 top-to-bottom); ang is the rotation.
	grad uint8
	// shape is the elemShape silhouette id for ElemShape.
	shape uint8
	// align is the resolved theme.TextAlign for ElemText.
	align uint8
	// fxDim is the alpha this frame's effect takes off an ElemText, 0 neutral; kept out of the folded colours because
	// the label cache keys on colour, so folding would rasterise a new texture every frame of a fade.
	fxDim  uint8
	radial bool // gradient: radial instead of linear
	loop   bool
	// wash marks an [effect.<widget key>] plate that exists only for its effect: its fill alpha is the effect's curve,
	// not a modulation, so `[effect.viewport] effect = fade` clears rather than settling opaque over the stage.
	wash bool
}

// condAlwaysVisible marks no condition, or a value the session never mentioned; -1 because 0 is a valid interned index.
const condAlwaysVisible = int16(-1)

// elemBandRange is one band's half-open [lo, hi) window into themeLayoutCache.el; int16 because ElementCap is 96.
type elemBandRange struct{ lo, hi int16 }

// elemPainters dispatches by kind: a fixed array, never a method value (that allocates a closure);
// every entry must be non-nil because drawElement calls through it with no check.
var elemPainters = [theme.ElementKindCount]func(*App, *bakedElement){
	theme.ElemImage:    paintElementArt,      // W4 — theme://m/<themeid>/<id> media
	theme.ElemShape:    paintElementShape,    // W3 — the banded silhouette family
	theme.ElemGradient: paintElementGradient, // W3 — two-stop linear/radial plate
	theme.ElemGen:      paintElementArt,      // W4 — generator tiles (gentex.go)
	theme.ElemText:     paintElementText,     // W3 — a baked, pre-truncated string
	theme.ElemPane:     paintElementPane,     // W3 — the split-screen plate + divider
}

// paintElementStub draws nothing; it keeps the table total for a kind with no painter, since a nil entry
// would panic the render thread.
func paintElementStub(*App, *bakedElement) {}

// drawElementBands paints one band, at three fixed points in drawCourtroomThemed:
// backdrop art, BandBackdrop, stage widgets, BandMid, the modal early return, panels, BandOverlay, client chrome.
// &lay.el[i] points into the cache on App, so handing it to a painter does not allocate.
func (a *App) drawElementBands(lay *themeLayoutCache, b theme.ElementBand) {
	if lay == nil || int(b) >= len(lay.band) {
		return
	}
	rg := lay.band[b]
	// The clamp makes a mis-bake paint nothing instead of stale rows from a previous theme.
	if hi := int16(lay.elN); rg.hi > hi {
		rg.hi = hi
	}
	for i := rg.lo; i < rg.hi; i++ {
		a.drawElement(lay, &lay.el[i], i)
	}
}

// drawElement paints one element: visibility check, motion, painter; motion is applied in place and restored,
// not drawn through a scratch copy, so the painter gets &lay.el[i] without allocating.
func (a *App) drawElement(lay *themeLayoutCache, e *bakedElement, idx int16) {
	// kind comes from a theme file; the reader skips unknown kinds, but a bad index would panic the render thread.
	if e.kind >= theme.ElementKindCount {
		return
	}
	if !a.elementVisible(e) {
		// The FX pool entry is not released here: themeFXIdleFrames lets a brief off and on keep its one-shot's place.
		return
	}
	// Fast path: no effect. The upper bound guards the effectIsStateful index against a bad theme file.
	if e.fx.kind == theme.FXNone || e.fx.kind >= theme.EffectKindCount || a.themeFXFrozen {
		if e.wash {
			// A wash is its effect: with no live effect, including under ReduceMotion, painting would leave the baked
			// peak as a permanent sheet over the widget.
			return
		}
		elemPainters[e.kind](a, e)
		return
	}
	el := a.elementElapsed(e)
	// An always-visible one-shot began at theme apply, so its origin is elapsed zero; only a conditional one needs a
	// pool entry, to replay each time its condition opens, and keeping the rest out stops them exhausting the pool.
	st := fxAlwaysOrigin
	if effectIsStateful[e.fx.kind] && e.condAxis != theme.CondAlways {
		// A nil st (pool exhausted) freezes the effect at its first frame. lay.condGen is the bake generation:
		// a rebuild renumbers the array, so an entry from the previous bake must not answer for a new element there.
		st, _ = a.acquireElementFX(idx, lay.condGen, el)
	}
	fx := resolveElementEffect(e.fx, el, st)
	// Note animation only when the next frame will differ: a still picture must not pin the frame rate.
	// noteElementAnimating also covers a paused clock, which the resolver cannot see; terms still apply while paused.
	if !fx.static {
		a.noteElementAnimating(e.clock)
	}
	// Apply terms even when static: a static effect's terms are its final state, so a finished wash fade resolves to
	// env 0 and must paint nothing. A free element's static terms are neutral.
	saved := applyElementFX(e, fx)
	elemPainters[e.kind](a, e)
	restoreElementFX(e, saved)
}

// elementVisible compares an interned index resolved once per frame; an unknown axis stays visible, per the format.
func (a *App) elementVisible(e *bakedElement) bool {
	switch e.condAxis {
	case theme.CondAlways:
		return true
	case theme.CondSpeaking:
		return a.condSpeaking
	case theme.CondPos, theme.CondChar, theme.CondSide, theme.CondShout:
		// cond < 0 cannot come from the bake for these axes; treat it as no match, since a check that silently opens
		// goes unnoticed.
		return e.cond >= 0 && a.condLive[e.condAxis] == e.cond
	}
	return true
}

// elemCondSource is every condition axis's live value for one pass: the live courtroom reads the session, but the
// video export drives its own Room and must resolve side, shout and speaking against that.
type elemCondSource struct {
	speaking bool
	// pos and char are client state with no recorded counterpart, so both passes read the live values.
	pos, char string
	// side / shout are STAGE axes: whichever room this pass is drawing.
	side, shout string
}

// liveCondSource is the source for the live courtroom pass.
func (a *App) liveCondSource() elemCondSource {
	return elemCondSource{
		speaking: a.room != nil && a.room.Phase() != courtroom.PhaseIdle,
		pos:      a.mySide(),
		char:     a.myCharName(),
		side:     a.stageSide(),
		shout:    a.stageShout(),
	}
}

// refreshElementConditions resolves each live axis to its interned index, once per themed frame before the first band.
func (a *App) refreshElementConditions(lay *themeLayoutCache) {
	a.refreshElementConditionsFrom(lay, a.liveCondSource())
}

// refreshElementConditionsFrom interns one pass's axis values, memoised on the live string so a settled frame
// costs one string compare per axis.
func (a *App) refreshElementConditionsFrom(lay *themeLayoutCache, src elemCondSource) {
	// Both live and export passes start here, so the FX pool hook rides along; before the nil guard because a pass
	// with no layout must still sweep, or a theme swap would strand the outgoing theme's entries.
	a.beginThemeFXPass()
	if lay == nil {
		return
	}
	a.condSpeaking = src.speaking
	if lay.condN == 0 {
		return // no valued condition anywhere in this theme — skip the whole pass
	}
	// A cold rebuild re-interns every value, so an index from the old table would point at the wrong string.
	if a.condGen != lay.condGen {
		a.condGen = lay.condGen
		for i := range a.condSrc {
			a.condSrc[i] = condSrcUnresolved
			a.condLive[i] = condAlwaysVisible
		}
	}
	a.resolveConditionAxis(lay, theme.CondPos, src.pos)
	a.resolveConditionAxis(lay, theme.CondChar, src.char)
	a.resolveConditionAxis(lay, theme.CondSide, src.side)
	a.resolveConditionAxis(lay, theme.CondShout, src.shout)
}

// condSrcUnresolved marks a memo never resolved; no pos, character, side or shout contains a NUL.
const condSrcUnresolved = "\x00"

// resolveConditionAxis memoises one axis: an unchanged value is one compare, a changed one scans the interned table.
func (a *App) resolveConditionAxis(lay *themeLayoutCache, axis theme.ConditionAxis, live string) {
	if a.condSrc[axis] == live {
		return
	}
	a.condSrc[axis] = live
	a.condLive[axis] = condAlwaysVisible
	if live == "" {
		return
	}
	for i := 0; i < lay.condN; i++ {
		if lay.condVal[i] == live {
			a.condLive[axis] = int16(i)
			return
		}
	}
}

// stageSide is the speaker's pos, what `side:<name>` means; mySide() is the local player's, what `pos:<name>` means.
func (a *App) stageSide() string {
	if a.room == nil {
		return ""
	}
	return a.room.Scene.Position
}

// stageShout is the shout stem on screen, "" if none; elemShoutConditions folds `shout:hold_it` spellings at bake.
func (a *App) stageShout() string {
	if a.room == nil {
		return ""
	}
	return a.room.CurrentShout()
}

// elemShape ids, append-only: the id is baked and a theme reloaded mid-session must resolve to the same silhouette.
// Names extend shapemask.go's vocabulary (sharp, rounded, pill); "rect" stays an alias of sharp for older themes.
const (
	elemShapeSharp   uint8 = iota // a plain box — also what an unknown name degrades to
	elemShapeRounded              // rounded rectangle
	elemShapeHex                  // flat-top hexagon
	elemShapeRibbon               // banner: a V notched into each end
	elemShapeTape                 // washi tape: the two ends slanted the same way
	elemShapePill                 // full-radius capsule: the corner IS half the short side
	elemShapeCount
)

// elemShapeBands bounds the strips one silhouette draws: SDL's primitive API is rects and the frame must not allocate,
// so a silhouette is a stack of band fills, with a draw-call count independent of window height.
const elemShapeBands = int32(24)

// elemShapeChamferDiv is the depth of a hex, ribbon or tape cut, a quarter of the short side;
// deeper than half would invert the silhouette.
const elemShapeChamferDiv = int32(4)

// elemShapeCornerDiv is the rounded corner radius as a fraction of the short side.
const elemShapeCornerDiv = int32(6)

// elemShapePillDiv is the pill's corner radius as a fraction of the short side: a half, the definition of a capsule.
const elemShapePillDiv = int32(2)

// paintElementShape draws a silhouette fill, then the outline as per-band edge posts plus two caps;
// edges, not overpainted silhouettes, because a frame element has a transparent fill.
func paintElementShape(a *App, e *bakedElement) {
	if e.r.W <= 0 || e.r.H <= 0 {
		return
	}
	c := a.ctx
	n := elemShapeBands
	if e.r.H < n {
		n = e.r.H // never more bands than pixels
	}
	fill := e.col.A > 0
	line := e.stroke.A > 0 && e.strokePx > 0
	if !fill && !line {
		return
	}
	pen := e.strokePx
	if maxPen := min(e.r.W, e.r.H) / 2; pen > maxPen {
		pen = maxPen
	}
	var prevL, prevR int32
	for i := int32(0); i < n; i++ {
		y0 := e.r.Y + i*e.r.H/n
		y1 := e.r.Y + (i+1)*e.r.H/n
		l, r := elemShapeInset(e.shape, i, n, e.r.W, e.r.H)
		w := e.r.W - l - r
		if w <= 0 {
			continue
		}
		if fill {
			c.Fill(sdl.Rect{X: e.r.X + l, Y: y0, W: w, H: y1 - y0}, e.col)
		}
		if !line {
			continue
		}
		// Edge posts are pen-wide, so a slanted edge reads as a staircase.
		p := pen
		if p > w {
			p = w
		}
		c.Fill(sdl.Rect{X: e.r.X + l, Y: y0, W: p, H: y1 - y0}, e.stroke)
		c.Fill(sdl.Rect{X: e.r.X + e.r.W - r - p, Y: y0, W: p, H: y1 - y0}, e.stroke)
		// An inset jump leaves a gap in the outline; bridge it so a steep chamfer stays closed.
		if i > 0 {
			elemShapeBridge(c, e, y0, pen, prevL, l, prevR, r)
		}
		prevL, prevR = l, r
	}
	if !line {
		return
	}
	// Caps close the outline at the first and last band's insets.
	tl, tr := elemShapeInset(e.shape, 0, n, e.r.W, e.r.H)
	bl, br := elemShapeInset(e.shape, n-1, n, e.r.W, e.r.H)
	if w := e.r.W - tl - tr; w > 0 {
		c.Fill(sdl.Rect{X: e.r.X + tl, Y: e.r.Y, W: w, H: pen}, e.stroke)
	}
	if w := e.r.W - bl - br; w > 0 {
		c.Fill(sdl.Rect{X: e.r.X + bl, Y: e.r.Y + e.r.H - pen, W: w, H: pen}, e.stroke)
	}
}

// elemShapeBridge closes the gap between two bands whose insets differ, on whichever side moved.
func elemShapeBridge(c *Ctx, e *bakedElement, y int32, pen, prevL, l, prevR, r int32) {
	if d := abs32(l - prevL); d > pen {
		x := min(l, prevL)
		c.Fill(sdl.Rect{X: e.r.X + x, Y: y, W: d, H: pen}, e.stroke)
	}
	if d := abs32(r - prevR); d > pen {
		x := e.r.W - max(r, prevR)
		c.Fill(sdl.Rect{X: e.r.X + x, Y: y, W: d, H: pen}, e.stroke)
	}
}

// elemShapeInset is the left and right inset of band i of n for a w×h silhouette.
func elemShapeInset(shape uint8, i, n, w, h int32) (int32, int32) {
	if n <= 1 {
		return 0, 0
	}
	// t is the band CENTRE's position down the element, in [0, 1].
	t := (float64(i) + 0.5) / float64(n)
	short := min(w, h)
	switch shape {
	case elemShapeRounded, elemShapePill:
		// One quarter-circle for both; a pill's is as big as the silhouette allows.
		div := elemShapeCornerDiv
		if shape == elemShapePill {
			div = elemShapePillDiv
		}
		radius := short / div
		if radius <= 0 {
			return 0, 0
		}
		// Distance from the nearer end of the element, in pixels.
		d := t * float64(h)
		if far := float64(h) - d; far < d {
			d = far
		}
		if d >= float64(radius) {
			return 0, 0
		}
		// The corner's quarter-circle: inset = r - sqrt(r² - (r-d)²).
		k := float64(radius) - d
		in := int32(math.Round(float64(radius) - math.Sqrt(float64(radius*radius)-k*k)))
		return in, in
	case elemShapeHex:
		ch := short / elemShapeChamferDiv
		if ch <= 0 {
			return 0, 0
		}
		// Full chamfer at the two ends, none at mid-height.
		ramp := math.Abs(2*t-1) * float64(ch)
		in := int32(math.Round(ramp))
		return in, in
	case elemShapeRibbon:
		ch := short / elemShapeChamferDiv
		if ch <= 0 {
			return 0, 0
		}
		// The inverse of the hexagon: the V bites deepest at mid-height.
		in := int32(math.Round((1 - math.Abs(2*t-1)) * float64(ch)))
		return in, in
	case elemShapeTape:
		ch := short / elemShapeChamferDiv
		if ch <= 0 {
			return 0, 0
		}
		// Both ends slant the same way, so it reads as a torn strip, not a hexagon.
		return int32(math.Round(t * float64(ch))), int32(math.Round((1 - t) * float64(ch)))
	}
	return 0, 0
}

// elemGradientBands bounds a gradient plate's strips.
const elemGradientBands = int32(32)

// elemGradientDirCount is the number of cardinal directions a linear gradient quantises to.
const elemGradientDirCount = 4

// elemGradientDirStep is the angle-byte span of one cardinal direction, 90 degrees.
const elemGradientDirStep = theme.AngleCount / elemGradientDirCount

// paintElementGradient draws a two-stop plate, linear along the nearest cardinal axis or a ring ramp if radial;
// a true angle needs SDL_RenderGeometry, a new cgo surface in a zero-allocation frame (use the `gradient` generator).
func paintElementGradient(a *App, e *bakedElement) {
	if e.r.W <= 0 || e.r.H <= 0 {
		return
	}
	if e.col.A == 0 && e.col2.A == 0 {
		return
	}
	c := a.ctx
	if e.radial {
		paintElementRadial(c, e)
		return
	}
	// Nearest cardinal: +half a step, then integer-divide.
	dir := ((int32(e.grad) + elemGradientDirStep/2) / elemGradientDirStep) % elemGradientDirCount
	vertical := dir == 1 || dir == 3
	span := e.r.W
	if vertical {
		span = e.r.H
	}
	n := elemGradientBands
	if span < n {
		n = span
	}
	if n <= 1 {
		c.Fill(e.r, e.col)
		return
	}
	// dir 2 and 3 are the same axes reversed, so the stops swap rather than the loop.
	from, to := e.col, e.col2
	if dir >= 2 {
		from, to = to, from
	}
	for i := int32(0); i < n; i++ {
		col := elemStopColor(from, to, i, n-1)
		lo := i * span / n
		hi := (i + 1) * span / n
		if vertical {
			c.Fill(sdl.Rect{X: e.r.X, Y: e.r.Y + lo, W: e.r.W, H: hi - lo}, col)
		} else {
			c.Fill(sdl.Rect{X: e.r.X + lo, Y: e.r.Y, W: hi - lo, H: e.r.H}, col)
		}
	}
}

// paintElementRadial ramps from stop B at the edge to stop A at the centre as concentric rectangles, drawn outside-in;
// rectangular because a circle needs geometry (`generator = glow` is the circular one).
func paintElementRadial(c *Ctx, e *bakedElement) {
	n := elemGradientBands
	if half := min(e.r.W, e.r.H) / 2; half > 0 && half < n {
		n = half
	}
	if n <= 1 {
		c.Fill(e.r, e.col)
		return
	}
	for i := int32(0); i < n; i++ {
		insetX := i * (e.r.W / 2) / n
		insetY := i * (e.r.H / 2) / n
		ring := sdl.Rect{X: e.r.X + insetX, Y: e.r.Y + insetY, W: e.r.W - 2*insetX, H: e.r.H - 2*insetY}
		if ring.W <= 0 || ring.H <= 0 {
			return
		}
		c.Fill(ring, elemStopColor(e.col2, e.col, i, n-1))
	}
}

// elemStopColor is the i-of-n step from a to b including alpha, unlike lerpColor; integer maths so the ramp cannot
// shimmer between frames, no closure so it cannot allocate.
func elemStopColor(a, b sdl.Color, i, n int32) sdl.Color {
	if n <= 0 {
		return a
	}
	return sdl.Color{
		R: elemStopByte(a.R, b.R, i, n),
		G: elemStopByte(a.G, b.G, i, n),
		B: elemStopByte(a.B, b.B, i, n),
		A: elemStopByte(a.A, b.A, i, n),
	}
}

// elemStopByte interpolates one channel, split out so elemStopColor needs no closure.
func elemStopByte(x, y uint8, i, n int32) uint8 {
	return uint8(int32(x) + (int32(y)-int32(x))*i/n)
}

// elemFontChrome is fontIdx's "no theme face" value, the client's chrome face, outside themeFontElem's range.
const elemFontChrome = uint8(255)

// paintElementText draws a baked string, always in the chrome face: `font` and `size` are parsed but not read, because
// a fontSet holds one point size, so a different size would rebuild it and purge the text cache every frame.
// No shipped theme carries a face file, so honouring `font` alone would change nothing.
func paintElementText(a *App, e *bakedElement) {
	if e.label == "" || e.r.W <= 0 || e.r.H <= 0 || e.col.A == 0 {
		return
	}
	c := a.ctx
	f := c.chromeFaceFor(e.label)
	if f == nil {
		return
	}
	x := e.r.X
	if e.align != uint8(theme.AlignLeft) {
		if w, ok := c.fontTextWidth(f, e.label); ok && w < e.r.W {
			switch theme.TextAlign(e.align) {
			case theme.AlignCenter:
				x += (e.r.W - w) / 2
			case theme.AlignRight:
				x += e.r.W - w
			}
		}
	}
	// Vertically centred: an author sizes the box, not the baseline.
	y := e.r.Y
	if lh := int32(f.Height()); lh > 0 && lh < e.r.H {
		y += (e.r.H - lh) / 2
	}
	// Effect alpha rides a texture alpha mod, not the ink colour; see bakedElement.fxDim.
	c.LabelClippedFontAlpha(f, x, y, e.r.W, e.label, e.col, 255-e.fxDim)
}

// elemTileCap bounds blits per tiled element; `fit = tile` at native size scales with the window (hard rule 4),
// so past the cap the element falls back to one stretched blit.
const elemTileCap = 4096

// paintElementArt draws an ElemImage's media page or an ElemGen's tile, the same thing after the bake.
// Every *sdl.Rect across cgo is a persistent scratch field (cgoRect), since &local would allocate on every blit.
func paintElementArt(a *App, e *bakedElement) {
	if e.page == nil || e.r.W <= 0 || e.r.H <= 0 || len(e.page.Frames) == 0 {
		return
	}
	// elementFrame, not themeFrame: an element has its own clock group and phase, and honours `loop = no`.
	tex := a.elementFrame(e)
	if tex == nil {
		return
	}
	c := a.ctx
	// Tint multiplies into the art and opacity is already in its alpha; both are restored because the page is shared
	// with other elements and the theme's chrome.
	_ = tex.SetColorMod(e.tint.R, e.tint.G, e.tint.B)
	_ = tex.SetAlphaMod(e.tint.A)
	switch e.fit {
	case theme.FitTile:
		paintElementTiled(c, e, tex)
	case theme.FitNine:
		paintElementNineSlice(c, e, tex)
	case theme.FitContain, theme.FitCover:
		paintElementFitted(c, e, tex)
	default:
		// FitStretch, and any unknown fit: fill the rect, ignoring aspect, so the element still draws (format rule 3).
		paintElementBlit(c, e, tex, e.r)
	}
	_ = tex.SetColorMod(255, 255, 255)
	_ = tex.SetAlphaMod(255)
}

// paintElementBlit is the one place rotation is applied: stretch, contain, cover and tile's over-cap fallback use it.
// tile and nine draw many rects with their own Ren.Copy and drop it: CopyEx spins each rect about its own centre,
// so the pieces would come apart.
func paintElementBlit(c *Ctx, e *bakedElement, tex *sdl.Texture, dst sdl.Rect) {
	c.cgoRect = dst
	if e.ang == 0 {
		_ = c.Ren.Copy(tex, nil, &c.cgoRect)
		return
	}
	// The 360/256 angle byte, as ThemeRectRotations.
	_ = c.Ren.CopyEx(tex, nil, &c.cgoRect, float64(e.ang)*360/float64(theme.AngleCount), nil, sdl.FLIP_NONE)
}

// paintElementTiled repeats the source at native size, clipped to the element so an edge tile is cropped.
func paintElementTiled(c *Ctx, e *bakedElement, tex *sdl.Texture) {
	tw, th := e.page.W, e.page.H
	if tw <= 0 || th <= 0 {
		return
	}
	cols := (e.r.W + tw - 1) / tw
	rows := (e.r.H + th - 1) / th
	if cols*rows > elemTileCap {
		// The repeat count depends on the window, not the theme; one stretched blit keeps draw calls bounded.
		paintElementBlit(c, e, tex, e.r)
		return
	}
	prev, had := c.pushClip(e.r)
	for row := int32(0); row < rows; row++ {
		for col := int32(0); col < cols; col++ {
			c.cgoRect = sdl.Rect{X: e.r.X + col*tw, Y: e.r.Y + row*th, W: tw, H: th}
			_ = c.Ren.Copy(tex, nil, &c.cgoRect)
		}
	}
	c.popClip(prev, had)
}

// paintElementFitted is `contain` (letterboxed) and `cover` (cropped), which differ only in which scale wins.
func paintElementFitted(c *Ctx, e *bakedElement, tex *sdl.Texture) {
	sw, sh := e.page.W, e.page.H
	if sw <= 0 || sh <= 0 {
		return
	}
	// Integer maths: a float scale that wobbled between frames of an unchanged window would shimmer.
	byW := e.r.W * sh
	byH := e.r.H * sw
	fitW, fitH := e.r.W, e.r.W*sh/sw
	if (e.fit == theme.FitContain) == (byW > byH) {
		fitW, fitH = e.r.H*sw/sh, e.r.H
	}
	dst := sdl.Rect{
		X: e.r.X + (e.r.W-fitW)/2,
		Y: e.r.Y + (e.r.H-fitH)/2,
		W: fitW,
		H: fitH,
	}
	if e.fit == theme.FitCover {
		// Cover overflows the rect, so it is clipped to it.
		prev, had := c.pushClip(e.r)
		paintElementBlit(c, e, tex, dst)
		c.popClip(prev, had)
		return
	}
	paintElementBlit(c, e, tex, dst)
}

// paintElementNineSlice draws a 9-slice with insets (left, top, right, bottom): corners at source size, edges keeping
// source thickness and stretching along their axis only, because hairline elements depend on that; centre both ways.
func paintElementNineSlice(c *Ctx, e *bakedElement, tex *sdl.Texture) {
	sw, sh := e.page.W, e.page.H
	if sw <= 0 || sh <= 0 {
		return
	}
	l, t, r, b := e.slice[0], e.slice[1], e.slice[2], e.slice[3]
	// Clamp overlapping insets so a careless theme gets a plausible frame, not negative geometry.
	if l+r >= sw {
		l, r = sw/2, sw-sw/2-1
	}
	if t+b >= sh {
		t, b = sh/2, sh-sh/2-1
	}
	if l < 0 || t < 0 || r < 0 || b < 0 {
		paintElementBlit(c, e, tex, e.r)
		return
	}
	// Edge bands keep source thickness unless the element is smaller than its frame; then they scale down together.
	dl, dt, dr, db := l, t, r, b
	if dl+dr > e.r.W && dl+dr > 0 {
		dl, dr = dl*e.r.W/(dl+dr), e.r.W-dl*e.r.W/(dl+dr)
	}
	if dt+db > e.r.H && dt+db > 0 {
		dt, db = dt*e.r.H/(dt+db), e.r.H-dt*e.r.H/(dt+db)
	}
	// The nine regions, as (source, destination) pairs walked in one pass.
	sx := [3]int32{0, l, sw - r}
	sWs := [3]int32{l, sw - l - r, r}
	sy := [3]int32{0, t, sh - b}
	sHs := [3]int32{t, sh - t - b, b}
	dx := [3]int32{e.r.X, e.r.X + dl, e.r.X + e.r.W - dr}
	dWs := [3]int32{dl, e.r.W - dl - dr, dr}
	dy := [3]int32{e.r.Y, e.r.Y + dt, e.r.Y + e.r.H - db}
	dHs := [3]int32{dt, e.r.H - dt - db, db}
	for row := 0; row < 3; row++ {
		if sHs[row] <= 0 || dHs[row] <= 0 {
			continue
		}
		for col := 0; col < 3; col++ {
			if sWs[col] <= 0 || dWs[col] <= 0 {
				continue
			}
			c.cgoSrc = sdl.Rect{X: sx[col], Y: sy[row], W: sWs[col], H: sHs[row]}
			c.cgoRect = sdl.Rect{X: dx[col], Y: dy[row], W: dWs[col], H: dHs[row]}
			_ = c.Ren.Copy(tex, &c.cgoSrc, &c.cgoRect)
		}
	}
}

// paintElementPane draws a `kind = pane` element's plate and divider frame; [pane.*] itself only reparents its widgets.
func paintElementPane(a *App, e *bakedElement) {
	if e.r.W <= 0 || e.r.H <= 0 {
		return
	}
	c := a.ctx
	if e.col.A > 0 {
		c.Fill(e.r, e.col)
	}
	if e.stroke.A == 0 || e.strokePx <= 0 {
		return
	}
	pen := e.strokePx
	if maxPen := min(e.r.W, e.r.H) / 2; pen > maxPen {
		pen = maxPen
	}
	if pen <= 0 {
		return
	}
	c.Fill(sdl.Rect{X: e.r.X, Y: e.r.Y, W: e.r.W, H: pen}, e.stroke)
	c.Fill(sdl.Rect{X: e.r.X, Y: e.r.Y + e.r.H - pen, W: e.r.W, H: pen}, e.stroke)
	c.Fill(sdl.Rect{X: e.r.X, Y: e.r.Y, W: pen, H: e.r.H}, e.stroke)
	c.Fill(sdl.Rect{X: e.r.X + e.r.W - pen, Y: e.r.Y, W: pen, H: e.r.H}, e.stroke)
}
