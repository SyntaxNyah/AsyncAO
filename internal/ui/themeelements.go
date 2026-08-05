package ui

// The free-element render model (v1.90.0 W3) — docs/wip/THEME-EDITOR-DESIGN.md §2,
// docs/THEME-FORMAT.md §4.
//
// An AsyncAO theme may declare up to theme.ElementCap FREE ELEMENTS: boxes the AO2
// courtroom_design.ini registry has no row for. They are structurally disjoint from
// themeSlots — different file, different identity (an INDEX, never a design key),
// different geometry cache, different persistence — which is why AO2 parity here is
// by CONSTRUCTION rather than by discipline: the totality census over
// courtroom_design.ini's root keys structurally cannot see an element, and
// applyRectOverrides / themeKeyEditable never meet one.
//
// THIS FILE IS THE DRAW PATH. The bake that fills the array lives beside it in
// themeelementbake.go, and the split is load-bearing: TestElementDrawUsesNoStringBuilding
// scans THIS file for string work, so the cold path has somewhere to be without
// weakening the scan.
//
// THREE THINGS MAKE THIS SAFE INSIDE A ZERO-ALLOCATION FRAME:
//
//  1. BAKE, DON'T SORT. Everything an element needs to paint — its absolute rect
//     after the anchor delta and the canvas fit, its resolved page pointer, its
//     colours, its already-truncated label — is resolved ONCE on themeLayoutIn's
//     cold rebuild into a FIXED ARRAY beside the slot rects. The draw loop reads
//     values: never the map, never a slice header, never a sort, never a string op.
//  2. THREE BANDS, NOT A GLOBAL Z-SORT. The themed pass is hand-ordered across
//     phaseStage/phasePanels (themeslots.go) and carries a mid-sequence abort
//     (drawCourtroomModals' early return). A flat sortable list cannot reproduce
//     that, so elements paint at three fixed insertion points and sort only WITHIN
//     a band, at bake time.
//  3. A TOP-LEVEL PAINTER TABLE. elemPainters is indexed by kind, and every entry
//     is a top-level func — never a method value, which would allocate a closure the
//     first time the table were read (themeslots.go's `draw` field carries the same
//     rule for the same reason).
//
// Zero base cost: a stock AO2 theme declares no elements, so lay.band is all-zero
// and the three loops are three `for i := 0; i < 0`. That is BENCHMARKED
// (BenchmarkThemedFrameNoElements), not asserted — the v1.89.0 "0 base = 0 cost"
// rule.

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// bakedElementByteCeil bounds one baked entry, and through it the whole array:
// theme.ElementCap (96) entries at this ceiling is ~12 KiB of App, a fixed
// allocation that never grows and never churns. It is a NAMED ceiling rather than a
// comment because the array is inline in themeLayoutCache — every byte added here is
// multiplied by 96 and paid by every install, themed or not, so growing the baked
// row has to be a decision somebody takes on purpose. Pinned by
// TestBakedElementStaysSmall.
const bakedElementByteCeil = 128

// bakedEffect is an element's motion, resolved at bake time: the effect id plus its
// two parameters. The RESOLVER that turns this into (offset, scale, alpha, rot,
// static) is W5 (design §6.6 R2 — the rot term is part of the signature from the
// start); until then the fields are carried and not read, which is the format's rule
// 4 in the render model: a zero value behaves exactly as the version before it.
type bakedEffect struct {
	kind     theme.EffectKind
	periodMs int32
	ampPct   int16
}

// bakedElement is ONE element, resolved. Every field is a value the painter can use
// without a lookup: no map probe, no string build, no prefs read, no allocation.
//
// FIELD ORDER IS BY SIZE, not by theme.Element's authoring order — this struct is
// replicated 96 times inside App and padding is the only cost nobody notices.
type bakedElement struct {
	// r is ABSOLUTE window pixels: post-anchor (design §6.6 R1 — a non-empty anchor
	// makes the authored rect a signed DELTA on the anchor's resolved box), post
	// space transform, post canvas fit, post clamp.
	r sdl.Rect
	// page is the element's art, resolved ONCE at bake. nil is the ordinary case for
	// every non-image kind, and is also what an image whose media has not landed (or
	// was evicted) resolves to — the painter draws nothing rather than probing.
	page *render.TexturePage
	// label is an ElemText's string, ALREADY truncated to r at bake (truncateLabelTo,
	// AO2's truncate_label_text). Truncation allocates, so it can never be a draw-path
	// operation — the same reason themeLayoutCache.toggleLabel exists.
	label string
	// phase is the element's own clock offset. Free: it consumes no clock group.
	phase    time.Duration
	slice    [4]int32  // 9-slice insets, source px, (left, top, right, bottom) — R9
	strokePx int32     // outline width; ElemPane reads it as the divider width
	col      sdl.Color // Fill:  gradient stop A / shape fill / pane plate / text ink
	col2     sdl.Color // Fill2: gradient stop B
	stroke   sdl.Color
	tint     sdl.Color // multiplied into image/gen art at draw
	fx       bakedEffect
	// cond is the INTERNED index of VisibleWhen.Value, or condAlwaysVisible. Interning
	// at bake is what keeps the visibility gate an integer compare instead of a string
	// compare on the frame path.
	cond     int16
	kind     theme.ElementKind
	fit      theme.ElementFit
	condAxis theme.ConditionAxis
	opacity  uint8
	ang      uint8 // 360/256 angle byte — the ThemeRectRotations convention
	// fontIdx is the resolved face index for ElemText. W4 (font intake) fills it;
	// W3 draws every element string in the client chrome face and leaves it at
	// elemFontChrome — see paintElementText for why a per-element point size cannot
	// ride the existing per-element font sets.
	fontIdx uint8
	clock   uint8
	// grad is the gradient axis as a 360/256 angle byte (0 = left-to-right,
	// 64 = top-to-bottom), quantised to a cardinal direction at draw. Separate from
	// ang, which is the element's own ROTATION.
	grad uint8
	// shape is the resolved elemShape silhouette id for ElemShape (never a string
	// on the draw path).
	shape uint8
	// align is the resolved theme.TextAlign for ElemText.
	align  uint8
	radial bool // gradient: radial instead of linear
	loop   bool
}

// condAlwaysVisible is bakedElement.cond for an element with no condition, and for
// one whose condition names a value the live session has never mentioned. -1 rather
// than 0 because 0 is a legitimate interned index.
const condAlwaysVisible = int16(-1)

// elemBandRange is one band's half-open [lo, hi) window into themeLayoutCache.el.
// int16 because ElementCap is 96: two of these per band is 12 bytes for the whole
// paint order, and the draw loop's induction variable never touches memory.
type elemBandRange struct{ lo, hi int16 }

// elemPainters dispatches by kind. A FIXED ARRAY, indexed — never a map, never a
// switch that a new kind can silently fall out of, and never a method value (that
// allocates a closure; themeslots.go carries the same rule for themeSlot.draw).
//
// Every entry is non-nil by contract, because drawElementBands calls through it
// without a nil check: TestEveryElementKindHasAPainter is what keeps that true, and
// paintElementStub is what a kind whose painter has not landed yet points at.
var elemPainters = [theme.ElementKindCount]func(*App, *bakedElement){
	theme.ElemImage:    paintElementStub,     // W4 — theme://m/<themeid>/<id> media
	theme.ElemShape:    paintElementShape,    // W3 — the banded silhouette family
	theme.ElemGradient: paintElementGradient, // W3 — two-stop linear/radial plate
	theme.ElemGen:      paintElementStub,     // W4 — generator tiles (gentex.go)
	theme.ElemText:     paintElementText,     // W3 — a baked, pre-truncated string
	theme.ElemPane:     paintElementPane,     // W3 — the split-screen plate + divider
}

// paintElementStub is the placeholder painter: it draws nothing, deliberately.
//
// It exists so the table is TOTAL from the first commit of the wave — the band loop
// indexes elemPainters with no nil check, and a nil entry would be a panic on the
// render thread the first time a stranger's theme used that kind. It is also the
// marker TestEveryElementKindHasAPainter compares against, so "which kinds do not
// paint yet" is a mechanical fact rather than a comment somebody has to maintain.
func paintElementStub(*App, *bakedElement) {}

// drawElementBands paints one band of free elements.
//
// The design doc sketches this as `drawElementBand`; the shipped name is plural
// because it is the whole of the element draw surface and its gates are named for
// it (TestDrawElementBandsZeroAllocWithNoElements / …With96Elements).
//
// Called at THREE fixed points in drawCourtroomThemed (theme_layout.go), which is
// the entire contract with the hand-ordered themed pass:
//
//	backdrop art          → BandBackdrop → stage widgets
//	                      → BandMid      → the modal early-return
//	                      → panel widgets → BandOverlay → client chrome
//
// ZERO ALLOCATIONS, ALWAYS. `&lay.el[i]` is a pointer INTO the cache (which lives on
// App and is therefore already off the stack), so handing it to an opaque function
// pointer cannot heap-allocate — passing the 100-odd-byte value instead would copy
// it 96 times a frame. Nothing here builds a string, probes a map, reads prefs or
// touches the disk. Gated by the two AllocsPerRun tests named above; every painter
// added to the table inherits both.
func (a *App) drawElementBands(lay *themeLayoutCache, b theme.ElementBand) {
	if lay == nil || int(b) >= len(lay.band) {
		return
	}
	rg := lay.band[b]
	// The bake is the only producer of these indices and it fills el[0:elN]. The
	// clamp costs one compare per band per frame and makes a mis-bake paint nothing
	// instead of painting stale rows from a previous theme.
	if hi := int16(lay.elN); rg.hi > hi {
		rg.hi = hi
	}
	for i := rg.lo; i < rg.hi; i++ {
		e := &lay.el[i]
		// Defence in depth: kind ultimately comes from a file a stranger wrote. The
		// reader SKIPS an element whose kind it does not know (docs/THEME-FORMAT.md
		// §7.4) so this can only fire on a bug in the bake — but an out-of-range index
		// here would panic the render thread, and one predictable compare is cheaper
		// than that risk.
		if e.kind >= theme.ElementKindCount {
			continue
		}
		if !a.elementVisible(e) {
			continue
		}
		elemPainters[e.kind](a, e)
	}
}

// ---------------------------------------------------------------------------
// visible_when — one axis, one interned value, an integer compare
// ---------------------------------------------------------------------------

// elementVisible is the per-element half of the visibility gate: an INTEGER
// COMPARE against an index the bake interned and refreshElementConditions
// resolved once for this frame. Deliberately not an expression language —
// anything richer needs a parser on the frame path (docs/THEME-FORMAT.md §3).
//
// An unknown axis leaves the element VISIBLE, which is the format's own rule:
// "an element a future condition would have hidden is better shown than lost".
func (a *App) elementVisible(e *bakedElement) bool {
	switch e.condAxis {
	case theme.CondAlways:
		return true
	case theme.CondSpeaking:
		// The one value-less axis: no interning, just the live flag.
		return a.condSpeaking
	case theme.CondPos, theme.CondChar, theme.CondSide, theme.CondShout:
		// cond < 0 means the element's value never interned, which the bake cannot
		// produce for these axes — treat it as "never matches" rather than "always",
		// because a gate that silently opens is the failure nobody notices.
		return e.cond >= 0 && a.condLive[e.condAxis] == e.cond
	}
	return true
}

// refreshElementConditions resolves the LIVE value of every condition axis to its
// interned index, once per themed frame, before the first band paints.
//
// ONCE PER FRAME, NOT PER ELEMENT, and memoised on the live string: the scan below
// only re-runs when the session actually changed pos / character / side / shout, so
// a settled frame costs one string compare per axis and nothing else. That is what
// lets the per-element gate be an integer compare (elementVisible) — the design's
// whole reason for interning at bake.
//
// Zero allocations: every source is a field read or a switch over constants, and
// the scan walks strings already resident in the layout cache.
func (a *App) refreshElementConditions(lay *themeLayoutCache) {
	if lay == nil {
		return
	}
	// Speaking is a flag, not a value: nothing to intern, nothing to memoise.
	a.condSpeaking = a.room != nil && a.room.Phase() != courtroom.PhaseIdle
	if lay.condN == 0 {
		return // no valued condition anywhere in this theme — skip the whole pass
	}
	// The generation guard: a cold rebuild re-interns every value, so an index
	// resolved against the OLD table would point at the wrong string.
	if a.condGen != lay.condGen {
		a.condGen = lay.condGen
		for i := range a.condSrc {
			a.condSrc[i] = condSrcUnresolved
			a.condLive[i] = condAlwaysVisible
		}
	}
	a.resolveConditionAxis(lay, theme.CondPos, a.mySide())
	a.resolveConditionAxis(lay, theme.CondChar, a.myCharName())
	a.resolveConditionAxis(lay, theme.CondSide, a.stageSide())
	a.resolveConditionAxis(lay, theme.CondShout, a.stageShout())
}

// condSrcUnresolved is the memo's "never resolved" marker. It cannot collide with a
// live value because no pos, character, side or shout is spelled with a NUL.
const condSrcUnresolved = "\x00"

// resolveConditionAxis memoises ONE axis: unchanged live value ⇒ one string compare
// and out; changed ⇒ a linear scan of the interned table (≤ 96 entries, and only on
// the frame the session actually moved).
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

// stageSide is the side the STAGE is currently showing — the speaker's pos, which
// is what `visible_when = side:<name>` means. Distinct from mySide(), which is the
// local player's own pos dropdown and is what `pos:<name>` means: a decoration that
// dresses the defence bench wants the first, one that dresses YOUR controls wants
// the second.
func (a *App) stageSide() string {
	if a.room == nil {
		return ""
	}
	return a.room.Scene.Position
}

// stageShout is the shout stem of the message on screen ("" when idle / none) —
// holdit / objection / takethat / custom (courtroom.ShoutName). A theme that spelled
// its condition with the DESIGN key (`shout:hold_it`, the spelling its own `anchor =`
// lines use) interned the stem anyway, at bake: elemShoutConditions folds the two
// spellings together so this side stays one field read.
func (a *App) stageShout() string {
	if a.room == nil {
		return ""
	}
	return a.room.CurrentShout()
}

// ---------------------------------------------------------------------------
// Painters
// ---------------------------------------------------------------------------

// elemShape ids. The wire names live in the bake (elemShapeNames); the draw path
// only ever sees these integers.
//
// APPEND-ONLY: the id is baked, and a theme reloaded mid-session must resolve to
// the same silhouette it did a frame ago. elemShapePill is appended rather than
// slotted beside elemShapeRounded for exactly that reason.
//
// THE VOCABULARY IS shapemask.go's, EXTENDED (design §2): sharp / rounded / pill
// are the three names this client already persists (shapemask.go:38-40, and the
// same three in internal/config), and the 14 shipped themes were authored against
// them. hex / ribbon / tape are W3's additions. "rect" is an accepted ALIAS of
// sharp (elemShapeAliases) — it was this id's original spelling in W3 and a name
// that once resolved has to keep resolving.
const (
	elemShapeSharp   uint8 = iota // a plain box — also what an unknown name degrades to
	elemShapeRounded              // rounded rectangle
	elemShapeHex                  // flat-top hexagon
	elemShapeRibbon               // banner: a V notched into each end
	elemShapeTape                 // washi tape: the two ends slanted the same way
	elemShapePill                 // full-radius capsule: the corner IS half the short side
	elemShapeCount
)

// elemShapeBands bounds the horizontal strips ONE silhouette draws.
//
// A silhouette is painted as a stack of axis-aligned band fills, because the frame
// this runs inside is gated at zero allocations and SDL's primitive API is rects.
// 24 is smooth enough for a badge or a frame at any realistic element height and
// small enough that the pathological theme — theme.ElementCap shapes, all visible,
// every frame — is a bounded number of draw calls rather than a function of the
// window height. (The mask-backed, anti-aliased path is W4's, where the theme-media
// budget brings a texture tier with it.)
const elemShapeBands = int32(24)

// elemShapeChamferDiv is how deep a hex/ribbon/tape cut goes: one quarter of the
// element's SHORT side. Named because three shapes share it and because a chamfer
// deeper than half the side would invert the silhouette.
const elemShapeChamferDiv = int32(4)

// elemShapeCornerDiv is the rounded corner radius as a fraction of the short side.
// A sixth reads as "rounded" at a badge's size without eating a frame's interior.
const elemShapeCornerDiv = int32(6)

// elemShapePillDiv is the PILL's corner radius as a fraction of the short side: a
// half, which is the definition of a capsule (shapemask.go's pill preset resolves
// its corner as min(w,h)/2 for the same reason). It is the one silhouette whose
// full radius is its identity — themes/thh_trial/asyncao_theme.ini:1200-1209 says
// so about its own register plate — so it is a separate id rather than "rounded
// with a big number", and it can never quietly degrade to a rectangle.
const elemShapePillDiv = int32(2)

// paintElementShape draws a procedural silhouette: the fill first, then the outline
// as per-band edge posts plus two caps.
//
// The outline is drawn as edges rather than as "fill the outer silhouette in the
// stroke colour, then the inner one in the fill colour", because that trick needs an
// OPAQUE fill — and a frame element is precisely a stroke over a TRANSPARENT fill,
// which is why theme.RGBA carries alpha at all.
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
	if maxPen := min32(e.r.W, e.r.H) / 2; pen > maxPen {
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
		// The two edge posts. They are as wide as the pen, so a slanted edge reads as
		// a staircase of pen-wide blocks — the same trade the band fill makes.
		p := pen
		if p > w {
			p = w
		}
		c.Fill(sdl.Rect{X: e.r.X + l, Y: y0, W: p, H: y1 - y0}, e.stroke)
		c.Fill(sdl.Rect{X: e.r.X + e.r.W - r - p, Y: y0, W: p, H: y1 - y0}, e.stroke)
		// A band whose inset JUMPED from the previous one leaves a horizontal gap in
		// the outline; bridge it so a steep chamfer is a closed shape, not a comb.
		if i > 0 {
			elemShapeBridge(c, e, y0, pen, prevL, l, prevR, r)
		}
		prevL, prevR = l, r
	}
	if !line {
		return
	}
	// Caps: the top and bottom edges of the silhouette, at the first and last band's
	// insets, so the outline closes.
	tl, tr := elemShapeInset(e.shape, 0, n, e.r.W, e.r.H)
	bl, br := elemShapeInset(e.shape, n-1, n, e.r.W, e.r.H)
	if w := e.r.W - tl - tr; w > 0 {
		c.Fill(sdl.Rect{X: e.r.X + tl, Y: e.r.Y, W: w, H: pen}, e.stroke)
	}
	if w := e.r.W - bl - br; w > 0 {
		c.Fill(sdl.Rect{X: e.r.X + bl, Y: e.r.Y + e.r.H - pen, W: w, H: pen}, e.stroke)
	}
}

// elemShapeBridge closes the horizontal gap between two bands whose insets differ,
// on whichever side moved. Called only on the stroked path.
func elemShapeBridge(c *Ctx, e *bakedElement, y int32, pen, prevL, l, prevR, r int32) {
	if d := abs32(l - prevL); d > pen {
		x := min32(l, prevL)
		c.Fill(sdl.Rect{X: e.r.X + x, Y: y, W: d, H: pen}, e.stroke)
	}
	if d := abs32(r - prevR); d > pen {
		x := e.r.W - max32(r, prevR)
		c.Fill(sdl.Rect{X: e.r.X + x, Y: y, W: d, H: pen}, e.stroke)
	}
}

// elemShapeInset is the left and right inset of band i of n, for a w×h silhouette.
// Pure integer/float arithmetic over constants — no allocation, no state.
func elemShapeInset(shape uint8, i, n, w, h int32) (int32, int32) {
	if n <= 1 {
		return 0, 0
	}
	// t is the band CENTRE's position down the element, in [0, 1].
	t := (float64(i) + 0.5) / float64(n)
	short := min32(w, h)
	switch shape {
	case elemShapeRounded, elemShapePill:
		// One quarter-circle for both: the only difference is how big it is, and a
		// pill is the case where it is as big as the silhouette allows.
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
		// Both ends slant the SAME way, which is what makes it read as a torn strip
		// rather than as a hexagon.
		return int32(math.Round(t * float64(ch))), int32(math.Round((1 - t) * float64(ch)))
	}
	return 0, 0
}

// elemGradientBands bounds a gradient plate's strips. 32 is finer than the kit's own
// FillGradient (48 over a whole panel) needs to be for element-sized art, and it is
// the same bounded-strip idiom for the same reason.
const elemGradientBands = int32(32)

// elemGradientDirCount is the number of cardinal directions a linear gradient
// quantises to (left→right, top→bottom, right→left, bottom→top).
const elemGradientDirCount = 4

// elemGradientDirStep is the angle-byte span of one cardinal direction
// (theme.AngleCount / elemGradientDirCount), i.e. 90 degrees.
const elemGradientDirStep = theme.AngleCount / elemGradientDirCount

// paintElementGradient draws a two-stop plate: linear along a cardinal axis, or a
// concentric ring ramp when the element asked for radial.
//
// LINEAR GRADIENTS QUANTISE TO THE NEAREST CARDINAL DIRECTION in W3, and that is a
// deliberate deviation rather than an oversight: an arbitrary angle needs rotated
// geometry (SDL_RenderGeometry), which would be a brand-new cgo surface inside the
// one frame gated at exactly zero allocations. The format's own answer for a true
// diagonal is the `gradient` GENERATOR (docs/THEME-FORMAT.md §5), which rasterises a
// tile in W4 and costs nothing per frame.
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

// paintElementRadial ramps from stop B at the edge to stop A at the centre as
// concentric RECTANGLE rings, drawn outside-in so each one covers the last.
//
// Rectangular, not circular, for the same reason the linear case is cardinal: a true
// circle needs geometry. It reads correctly as a vignette or a centre glow inside a
// box, which is what the two-stop plate is for; `generator = glow` is the circular
// one, and it lands in W4 as a tile.
func paintElementRadial(c *Ctx, e *bakedElement) {
	n := elemGradientBands
	if half := min32(e.r.W, e.r.H) / 2; half > 0 && half < n {
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

// elemStopColor is the i-of-n step from a to b, ALPHA INCLUDED — which is why it
// is not the kit's own lerpColor (eggs.go), whose alpha stays at a.A. A two-stop
// plate ramping from opaque to transparent is the vignette idiom, and it is the
// whole reason theme.RGBA carries alpha at all.
//
// Integer arithmetic, and no closure. Both are deliberate: a ramp that wobbled with
// float rounding would shimmer between two frames of an unchanged window, and a
// per-channel closure is exactly the allocation lerpColor's own comment warns about
// on a per-frame draw.
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

// elemStopByte interpolates one channel; split out so elemStopColor needs no
// closure (the same rule, and the same reason, as lerpByte beside lerpColor).
func elemStopByte(x, y uint8, i, n int32) uint8 {
	return uint8(int32(x) + (int32(y)-int32(x))*i/n)
}

// elemFontChrome is bakedElement.fontIdx's "no theme face" value: the client's own
// chrome face. It is deliberately OUT of themeFontElem's range so a future W4 value
// cannot collide with it.
const elemFontChrome = uint8(255)

// paintElementText draws a baked, already-truncated string.
//
// W3 DRAWS EVERY ELEMENT STRING IN THE CLIENT CHROME FACE, and `font`/`size` are
// W4's (the wave that owns font intake). That is a bounded deferral with a measured
// reason: every other face in this client lives in a fontSet that holds exactly ONE
// point size (ui.go buildSet), so an element asking a panel's set for a different
// size would rebuild that set — and purge the text cache with it — on every frame
// the panel and the element were both on screen. That is the divergent-zoom defect
// TestDrawCourtroomThemedDivergentZoomZeroAlloc exists to catch, and it is not worth
// re-opening for a watermark. The chrome face has no such set: one face, one scale,
// memoised width, nothing to thrash.
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
	// Vertically centred in the element's box: an author sizes the box, not the
	// baseline, and a string pinned to the top of a tall plate reads as a bug.
	y := e.r.Y
	if lh := int32(f.Height()); lh > 0 && lh < e.r.H {
		y += (e.r.H - lh) / 2
	}
	c.LabelClippedFont(f, x, y, e.r.W, e.label, e.col)
}

// paintElementPane draws a split-screen region's PLATE and its divider frame.
//
// The [pane.*] section itself paints nothing — it reparents its host widgets and
// that is all it does (design §Q1: "the host widget draws EXACTLY ONCE, at the
// pane's rect"). A theme that wants the region to LOOK like a region declares a
// `kind = pane` element anchored to it; the bake then folds the pane's own
// `divider` / `divider_px` into that element's stroke when the element declares
// none, so the two spellings agree instead of competing.
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
	if maxPen := min32(e.r.W, e.r.H) / 2; pen > maxPen {
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
