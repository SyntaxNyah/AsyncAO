package ui

// The free-element BAKE (v1.90.0 W3) — docs/wip/THEME-EDITOR-DESIGN.md §2,
// docs/THEME-FORMAT.md §3-4.
//
// Everything expensive about an element happens HERE, exactly once per cold
// rebuild of themeLayoutCache: the pane reparenting, the anchor resolution, the
// space transform, the canvas fit, the clamp, the label truncation, the condition
// interning and the band sort. The draw path (themeelements.go) then reads values.
//
// SEPARATE FILE FROM THE PAINTERS, on purpose. TestElementDrawUsesNoStringBuilding
// scans themeelements.go for fmt/strconv/strings/bytes; this file is the cold path
// where a string operation is not merely allowed but correct, and keeping it out of
// the scanned file means the scan never has to be weakened with an exemption list.
//
// COLD PATH, NOT A FREE PATH. themeLayoutIn's rebuild runs on the render thread
// during a frame (a window resize, a fit change, a theme apply), so the bake is
// bounded rather than merely "not per frame": fixed arrays throughout, one pass per
// element, an insertion sort over at most 96 entries, no map growth, no disk.
// BenchmarkElementBandBake pins its allocation ceiling — a preset swap must not
// hitch.

import (
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// elemShapeNames maps the format's `shape =` wire names onto the draw path's
// integer ids. WIRE NAMES ARE THE FORMAT: append-only, never renamed. An unknown
// name degrades to elemShapeSharp (format rule 3) rather than skipping the element —
// the author still gets their fill, stroke, colour and position, which is the
// difference between a theme that looks slightly plainer on an older client and one
// with a hole in it.
//
// THE FIRST THREE ARE shapemask.go's SHIPPED VOCABULARY (shapemask.go:38-40, and
// the same three names in internal/config), because design §2 says ElemShape is
// "the shapemask.go family, EXTENDED" and because every `shape =` line in the 14
// themes shipped at HEAD is one of them. Renaming sharp→rect (W3's first spelling)
// would have degraded ~180 authored lines to the fall-through and dropped `pill`
// entirely — SILENTLY, since `shape` is read as free text rather than as an enum,
// so no degrade note reaches the import report. TestShippedThemeShapesAllResolve
// is what keeps that from happening again.
var elemShapeNames = [elemShapeCount]string{"sharp", "rounded", "hex", "ribbon", "tape", "pill"}

// elemShapeAliases are ADDITIONAL accepted spellings of an id that already has a
// canonical name. Append-only, and for the same reason the names are: a spelling
// that once resolved must keep resolving, or a theme authored against that build
// loses its silhouette on an upgrade.
var elemShapeAliases = [...]struct {
	name string
	id   uint8
}{
	{"rect", elemShapeSharp}, // W3's original spelling of the flat box
}

// elemShapeID resolves a wire name, degrading an unknown one. See elemShapeIDOK.
func elemShapeID(name string) uint8 {
	id, _ := elemShapeIDOK(name)
	return id
}

// elemShapeIDOK resolves a wire name and reports whether it is in the vocabulary at
// all. ok=false means the caller is holding the DEGRADE value, not a match — which
// is the distinction a census needs and a draw path does not.
//
// Forgiving about case, because `shape` is read as free text rather than as an
// enum (sidecar_read.go): a theme spelling it "Rounded" reaches us verbatim, and
// degrading that to a plain box would be a silent downgrade of a file that says
// exactly what it means.
//
// FOLDED PER COMPARE, not lowered once. strings.ToLower ALLOCATES whenever the
// input has an upper-case rune, and this runs once per element per rebuild —
// theme.ElementCap of them behind BenchmarkElementBandBake's ceiling, which is the
// bake's whole allocation budget for a theme of any size. strings.EqualFold does
// the same fold with no string to allocate, and strings.TrimSpace only ever
// reslices, so the whole resolve is allocation-free. elemShoutStem is the model,
// for the same reason and against the same ceiling.
func elemShapeIDOK(name string) (uint8, bool) {
	want := strings.TrimSpace(name)
	for i, n := range elemShapeNames {
		if strings.EqualFold(n, want) {
			return uint8(i), true
		}
	}
	for _, al := range elemShapeAliases {
		if strings.EqualFold(al.name, want) {
			return al.id, true
		}
	}
	return elemShapeSharp, false
}

// elemFullCanvasPct is design §6.6 R7: an empty-anchor courtroom-space element that
// covers at least this percentage of the ACTIVE canvas AFTER the uniform fit is a
// FULL-CANVAS element, and is snapped flush to the canvas.
//
// The snap is what makes the rule mean something at render time rather than only in
// W9's coherence matrix. A wallpaper authored against a 714x579 canvas and applied
// on a 1512x648 one lands a pixel or two short on one axis after the fit rounds;
// judged post-fit against the active canvas it is still ≥ 90% covering, so it is
// what the author said it was — the whole backdrop — and a seam of client colour
// down one edge is a defect, not fidelity.
const elemFullCanvasPct = 90

// elemZSortCap is the insertion sort's bound. It is theme.ElementCap by
// construction (the array cannot hold more), named so the O(n²) is a decision with
// a number attached: 96² is ~9k compares on a rebuild, which is nothing beside the
// map build the same rebuild already does, and an insertion sort allocates nothing
// where sort.SliceStable allocates a closure and a reflect swapper.
const elemZSortCap = theme.ElementCap

// bakeElement is one element mid-bake: the baked row plus the two SORT KEYS the
// draw path has no use for. It is a return VALUE, never an array — the keys live in
// two small parallel arrays on App (elemBakeBand / elemBakeZ, 288 bytes together)
// rather than in a second 11 KiB copy of the baked array, and the sort permutes all
// three in lockstep.
type bakeElement struct {
	el   bakedElement
	band theme.ElementBand
	z    int16
}

// bakeThemeElements fills lay.el / lay.elN / lay.band / lay.condVal from the active
// theme's sidecar, and REPARENTS every pane host into lay.r before it does.
//
// Order is load-bearing: panes rewrite the slot rects, and design §6.6 R8 says an
// anchored element resolves against the REPARENTED (final) rect — decoration follows
// its widget wherever it lives.
//
// A theme with no sidecar (every stock AO2 theme) leaves the array zeroed, elN 0 and
// all three band ranges {0, 0}: the three band loops become three `for i := 0; i < 0`
// and the feature costs nothing. That is the v1.89.0 "0 base = 0 cost" rule, and it
// is why this returns immediately rather than walking an empty slice.
func (a *App) bakeThemeElements(lay *themeLayoutCache) {
	// Every rebuild gets a fresh generation, so App's per-axis condition memo cannot
	// carry an index resolved against the PREVIOUS intern table (a.condGen).
	a.themeElemGen++
	lay.condGen = a.themeElemGen

	sc := a.themeSidecar
	if sc == nil {
		return
	}
	a.bakePanes(lay, sc)
	if len(sc.Elements) == 0 && len(sc.Effects) == 0 {
		return
	}

	// Baked straight into the cache's own array; the two sort keys ride beside it.
	n := 0
	for i := range sc.Elements {
		if n >= len(lay.el) {
			break // the reader already refuses past ElementCap; this is the belt
		}
		src := &sc.Elements[i]
		if src.Inert() {
			continue // hidden, or nothing to paint — never reaches the array at all
		}
		be, ok := a.bakeOneElement(lay, sc, src, i)
		if !ok {
			continue // an absent anchor is INERT, not an error (design §6.6 R10)
		}
		lay.el[n] = be.el
		a.elemBakeBand[n] = be.band
		a.elemBakeZ[n] = be.z
		n++
	}
	// [effect.<widget key>] LAST, so a theme that filled the array with its own
	// elements loses its binds rather than its elements — the array is one bound for
	// both, and the elements are the half the author positioned by hand.
	for i := range sc.Effects {
		if n >= len(lay.el) {
			break
		}
		be, ok := a.bakeEffectBind(lay, &sc.Effects[i])
		if !ok {
			continue
		}
		lay.el[n] = be.el
		a.elemBakeBand[n] = be.band
		a.elemBakeZ[n] = be.z
		n++
	}
	a.sortBakedElements(lay, n)
	lay.elN = n
	// Band ranges over the sorted array: the sort already grouped them, so one pass
	// records each band's first and last index.
	for b := range lay.band {
		lay.band[b] = elemBandRange{}
	}
	var started [theme.ElementBandCount]bool
	for i := 0; i < n; i++ {
		b := a.elemBakeBand[i]
		if int(b) >= len(lay.band) {
			continue // a band the reader could not produce; paints nowhere
		}
		if !started[b] {
			started[b] = true
			lay.band[b].lo = int16(i)
		}
		lay.band[b].hi = int16(i + 1)
	}
}

// bakeOneElement resolves ONE element into its baked row. ok=false means inert: the
// element's anchor or parent space is absent from this layout, so nothing about it
// can be positioned and it must not paint (design §6.6 R10 — skip-if-absent).
func (a *App) bakeOneElement(lay *themeLayoutCache, sc *theme.Sidecar, src *theme.Element, idx int) (bakeElement, bool) {
	r, ok := a.resolveElementRect(lay, sc, src)
	if !ok || r.W <= 0 || r.H <= 0 {
		return bakeElement{}, false
	}
	op := src.EffectiveOpacity()
	be := bakeElement{
		band: src.Band,
		z:    src.Z,
		el: bakedElement{
			r:        r,
			kind:     src.Kind,
			fit:      src.Fit,
			opacity:  op,
			ang:      src.Rot,
			grad:     src.GradAngle,
			radial:   src.GradRadial,
			align:    uint8(src.Align),
			shape:    elemShapeID(src.Shape),
			fontIdx:  elemFontChrome, // format 1: `font`/`size` are carried, not drawn (paintElementText)
			clock:    src.Clock,
			loop:     src.Loop,
			phase:    time.Duration(src.PhaseMs) * time.Millisecond,
			strokePx: int32(src.StrokePx),
			col:      fadeColor(src.Fill, op),
			col2:     fadeColor(src.Fill2, op),
			stroke:   fadeColor(src.Stroke, op),
			// The tint takes the opacity fold like every other colour on the row, and
			// it is the ONLY route `opacity` has into image and generator art:
			// paintElementArt's one alpha knob is SetAlphaMod(tint.A). Read straight
			// through sdlColorOf, an absent tint resolved to opaque white and a wash
			// authored at `opacity = 60` drew at full strength — silently, on every
			// vignette, scrim and overlay tile in the shipped themes, which reach for
			// exactly that idiom. fadeColor is the same folding the fill, fill2 and
			// stroke above already take, so all six kinds now mean one thing by it.
			tint:     fadeColor(src.EffectiveTint(), op),
			cond:     condAlwaysVisible,
			condAxis: src.VisibleWhen.Axis,
			fx: bakedEffect{
				kind:     src.Effect,
				periodMs: src.EffectPeriodMs,
				ampPct:   src.EffectAmpPct,
			},
		},
	}
	for i := range src.Slice {
		be.el.slice[i] = int32(src.Slice[i])
	}
	switch src.Kind {
	case theme.ElemImage, theme.ElemGen:
		// The art, resolved ONCE. The KEY comes out of the plan by INDEX rather than
		// being re-derived here: deriving a generator key means lowercasing a name and
		// hashing a param list, and the bake's allocation ceiling is a constant rather
		// than a per-element budget so that a preset swap cannot hitch its frame.
		//
		// nil is the ordinary answer for art that has not landed, was evicted by a
		// window recreate, or was refused by the budget — the painter draws nothing
		// rather than probing, and themeMediaPage has already kicked the paced heal.
		be.el.page, _ = a.themeMediaPage(a.themeMediaPlan.ElemKey(idx))
	case theme.ElemText:
		// The ink is `color`, not `fill` — and it is the ONE colour an absent value
		// resolves to white for (EffectiveColor), because a theme that sizes and
		// places a string but forgets the colour still meant to be read.
		be.el.col = fadeColor(src.EffectiveColor(), op)
		be.el.label = a.bakeElementLabel(src.Text, r.W)
	case theme.ElemPane:
		// The pane's own divider, folded in when the element declares no stroke of
		// its own: two spellings of one line, and the author's element wins.
		if p, found := sc.FindPane(src.Anchor); found && !src.Stroke.Opaque() && p.DividerPx > 0 {
			be.el.stroke = fadeColor(p.Divider, op)
			be.el.strokePx = int32(p.DividerPx)
		}
	}
	be.el.cond = a.internCondition(lay, src.VisibleWhen)
	return be, true
}

// elemBindZ is the z every [effect.*] bind bakes at: the top of the overlay band.
//
// A bind is a HIGHLIGHT ON a widget, so it belongs above the free elements an author
// placed in the same band — a glow that a decorative plate covered would be a glow
// nobody can see. int16's maximum rather than a chosen number, because "on top of
// everything in this band" is the actual intent and any smaller value is a guess that
// one more element could overtake. Binds keep their declaration order among
// themselves, because the insertion sort is stable and they all share this key.
const elemBindZ = int16(32767)

// bakeEffectBind turns one [effect.<widget key>] section into a baked element.
//
// A BIND IS ADDITIVE PAINT, exactly like every other element, and the format says so
// in as many words: "Elements are additive paint only. They cannot move, hide or
// restyle an AO2 widget" (docs/THEME-FORMAT.md §3). So a bind bakes into a plate over
// the widget's own rect, driven by the bind's effect — a pulsing highlight on the
// objection button, a fading wash over the chatbox — and NOT into a transform on the
// widget itself.
//
// That is a correctness decision, not a shortcut. lay.r is the geometry the widget
// DRAWS at and the geometry it HIT-TESTS at, both: an effect that offset or scaled
// the entry would slide a button's pixels away from its own click target, which is
// the draw-versus-hit-test divergence class this client has been bitten by before.
// The only sanctioned ways to move an AO2 widget stay [overrides] and [pane.*], both
// explicit and both static.
//
// `amp_pct` IS THE WASH'S PEAK OPACITY, folded into the plate's alpha here — and the
// shipped corpus is what settles that reading. Every `[effect.*]` colour in the
// fourteen native themes is a bare `#rrggbb`, i.e. fully opaque, at amplitudes from 12
// to 100; taken literally that is an opaque rectangle over the objection button. What
// those authors wrote, and say they wrote in their own comments, is "this widget glows
// at 45%" and "the stage comes up once when the theme applies". Peak opacity × the
// effect's envelope (bakedElement.wash) is that sentence, and it is the only reading
// under which a bind can never permanently bury the widget it decorates.
//
// ok=false is INERT, for the four reasons a bind cannot paint: it names no effect, it
// has no amplitude (the wash would be invisible), it names no colour (the format lists
// `color` as the bind's own key for exactly this), or its target is absent from this
// layout (design §6.6 R10, skip-if-absent).
func (a *App) bakeEffectBind(lay *themeLayoutCache, b *theme.EffectBind) (bakeElement, bool) {
	if b.Effect == theme.FXNone || b.AmpPct <= 0 || !b.Color.Opaque() {
		return bakeElement{}, false
	}
	r, ok := absSlotRect(lay, b.Target)
	if !ok || r.W <= 0 || r.H <= 0 {
		return bakeElement{}, false
	}
	peak := sdlColorOf(b.Color)
	peak.A = uint8(int(peak.A) * int(b.AmpPct) / theme.AmpMaxPct)
	// The baked row, in one piece:
	//   kind/shape — a flat plate, the silhouette family's most neutral member,
	//     because a bind's shape is the WIDGET's and not one the author chose;
	//   col        — the authored hue at its peak opacity (see above);
	//   wash       — the alpha rule that makes the plate the effect (themeelements.go);
	//   tint       — opaque white, the neutral value every baked row carries;
	//   cond       — always visible, so the one-shot resolves off the theme clock's
	//     own zero and needs no pool slot (fxAlwaysOrigin).
	return bakeElement{
		band: theme.BandOverlay,
		z:    elemBindZ,
		el: bakedElement{
			r:        r,
			kind:     theme.ElemShape,
			shape:    elemShapeSharp,
			opacity:  theme.OpacityOpaque,
			col:      peak,
			wash:     true,
			tint:     sdl.Color{R: 255, G: 255, B: 255, A: 255},
			clock:    b.Clock,
			cond:     condAlwaysVisible,
			condAxis: theme.CondAlways,
			fx: bakedEffect{
				kind:     b.Effect,
				periodMs: b.PeriodMs,
				ampPct:   b.AmpPct,
			},
		},
	}, true
}

// bakeElementLabel truncates an element's string to its own box, ONCE.
// truncateLabelTo allocates when it actually shortens, which is exactly why this
// cannot happen at draw time (the same reason themeLayoutCache.toggleLabel exists).
func (a *App) bakeElementLabel(text string, availW int32) string {
	if text == "" {
		return ""
	}
	c := a.ctx
	if c == nil {
		return text // headless construction; the draw path clips anyway
	}
	return c.fitLabelToFont(c.chromeFaceFor(text), text, availW)
}

// elemShoutConditions is the `visible_when = shout:<name>` vocabulary, and it exists
// because AO2 SPELLS A SHOUT TWO WAYS.
//
// The DESIGN KEY is what a theme author types everywhere else — `anchor = hold_it`,
// and the courtroom_design.ini row itself (themeslots.go:177-180, courtroom.cpp:995
// / :1001 / :1007 / :1100). The ASSET STEM is what the wire and the art use, and it
// is what courtroom.ShoutName returns off the message's objection modifier: holdit,
// objection, takethat, custom (urlbuilder.go:543).
//
// Three of the four differ, so a condition written in the spelling the rest of the
// file uses could never match the live value — `shout:hold_it`, `shout:take_that`
// and `shout:custom_objection` were dead, and only `shout:objection` worked, by the
// coincidence of the two spellings agreeing on exactly one member. Every one of the
// 14 shipped themes writes the design key.
//
// The stems are taken from courtroom.ShoutName rather than written out again, so
// this table cannot drift from the one function that decides what a shout is called.
var elemShoutConditions = [...]struct {
	key       string // the courtroom_design.ini / themeslots.go design key
	objection int    // the MS objection modifier ShoutName maps to the asset stem
}{
	{key: "hold_it", objection: 1},
	{key: "objection", objection: 2},
	{key: "take_that", objection: 3},
	{key: "custom_objection", objection: 4},
}

// elemConditionValue is the value a condition INTERNS under — the live spelling, not
// necessarily the authored one. Only `shout:` normalises today (see
// elemShoutConditions); every other axis compares against a value the session hands
// over verbatim (a pos, a character folder), where second-guessing the author would
// be the bug.
func elemConditionValue(cond theme.Condition) string {
	if cond.Axis != theme.CondShout {
		return cond.Value
	}
	return elemShoutStem(cond.Value)
}

// elemShoutStem folds either spelling of a shout onto the ASSET STEM that
// Courtroom.CurrentShout reports. An unrecognised value is returned verbatim: it
// simply never matches, which is the format's rule for a condition naming something
// this build has no idea about.
//
// Allocation-free by construction (EqualFold compares, ShoutName returns constants),
// which matters because the bake's whole allocation budget is elemBakeAllocCeil for
// a theme of any size.
func elemShoutStem(v string) string {
	name := strings.TrimSpace(v)
	for _, s := range elemShoutConditions {
		stem := courtroom.ShoutName(s.objection)
		if strings.EqualFold(name, s.key) || strings.EqualFold(name, stem) {
			return stem
		}
	}
	return v
}

// internCondition maps a VisibleWhen value to an index into lay.condVal, adding it
// on first sight. The table is bounded by theme.ElementCap because at most one new
// value can arrive per element.
//
// The VALUE-LESS axes (always, speaking) intern nothing: elementVisible switches on
// the axis before it ever looks at the index.
func (a *App) internCondition(lay *themeLayoutCache, cond theme.Condition) int16 {
	val := elemConditionValue(cond)
	if cond.Axis == theme.CondAlways || cond.Axis == theme.CondSpeaking || val == "" {
		return condAlwaysVisible
	}
	for i := 0; i < lay.condN; i++ {
		if lay.condVal[i] == val {
			return int16(i)
		}
	}
	if lay.condN >= len(lay.condVal) {
		return condAlwaysVisible // cannot happen below the cap; degrade to "visible"
	}
	lay.condVal[lay.condN] = val
	lay.condN++
	return int16(lay.condN - 1)
}

// resolveElementRect is the whole of §6.6 R1 plus the space table (§4).
//
// With a NON-EMPTY anchor the authored rect is a SIGNED DELTA on the anchor's
// resolved box, component by component — and the delta is in DESIGN pixels for every
// space except `window`, so `-6, -6, 12, 12` inflates a frame by six DESIGN px and
// keeps its proportion at any window size. With an EMPTY anchor the rect is absolute
// in its declared space.
//
// ELEMENTS ARE NOT CLAMPED INTO THE CANVAS, and that is the difference between them
// and a widget. themeLayoutIn clamps every courtroom-relative WIDGET into the stage
// because Qt amputated overhanging children at the window edge and themes rely on
// it — but the inflate idiom (`-6, -6, 12, 12`) is an element deliberately reaching
// OUTSIDE its anchor, and clamping it would collapse the very thing the anchor delta
// exists for. Decoration that runs off the canvas is clipped by SDL and costs
// nothing; decoration silently pulled back inside is a frame that no longer frames.
func (a *App) resolveElementRect(lay *themeLayoutCache, sc *theme.Sidecar, src *theme.Element) (sdl.Rect, bool) {
	// SpaceWindow is the one space outside the design canvas entirely: window
	// pixels, no scale, no offset, no canvas clamp. It is the AO2-full-parity rule
	// ("extras go in chrome OUTSIDE the canvas") expressed as a coordinate space.
	if src.Space == theme.SpaceWindow {
		r := sdl.Rect{X: int32(src.Rect.X), Y: int32(src.Rect.Y), W: int32(src.Rect.W), H: int32(src.Rect.H)}
		if src.Anchor != "" {
			base, ok := a.elementAnchorRect(lay, sc, src)
			if !ok {
				return sdl.Rect{}, false
			}
			r = sdl.Rect{X: base.X + r.X, Y: base.Y + r.Y, W: base.W + r.W, H: base.H + r.H}
		}
		win := sdl.Rect{X: 0, Y: 0, W: lay.winW, H: lay.winH}
		if _, hits := r.Intersect(&win); !hits {
			return sdl.Rect{}, false // entirely off the window: hidden, not clamped
		}
		return r, true
	}

	// Everything else is design space. The delta (or the absolute rect) scales with
	// the canvas exactly as a widget's does.
	d := sdl.Rect{
		X: int32(float64(src.Rect.X) * lay.scaleX),
		Y: int32(float64(src.Rect.Y) * lay.scaleY),
		W: int32(float64(src.Rect.W) * lay.scaleX),
		H: int32(float64(src.Rect.H) * lay.scaleY),
	}
	if src.Anchor != "" {
		base, ok := a.elementAnchorRect(lay, sc, src)
		if !ok {
			return sdl.Rect{}, false
		}
		return sdl.Rect{X: base.X + d.X, Y: base.Y + d.Y, W: base.W + d.W, H: base.H + d.H}, true
	}
	// No anchor: absolute in the declared space, re-based onto that space's origin.
	origin, ok := a.elementSpaceRect(lay, src.Space)
	if !ok {
		return sdl.Rect{}, false
	}
	r := sdl.Rect{X: origin.X + d.X, Y: origin.Y + d.Y, W: d.W, H: d.H}
	if src.Space == theme.SpaceCourtroom {
		r = snapFullCanvas(r, lay.area)
	}
	return r, true
}

// elementAnchorRect resolves an element's anchor to an ABSOLUTE box: a pane id when
// the element declared `space = pane`, otherwise a themeSlots key.
//
// ANCHORS MAY NOT CHAIN (docs/THEME-FORMAT.md §3), which is what makes one bake pass
// enough: no cycle detection, no depth cap, no ordering constraint between elements.
func (a *App) elementAnchorRect(lay *themeLayoutCache, sc *theme.Sidecar, src *theme.Element) (sdl.Rect, bool) {
	if src.Space == theme.SpacePane {
		p, found := sc.FindPane(src.Anchor)
		if !found {
			return sdl.Rect{}, false
		}
		return a.paneRect(lay, p)
	}
	return absSlotRect(lay, src.Anchor)
}

// elementSpaceRect is the ORIGIN box of a space, for an element that declared no
// anchor. An absent parent makes the element inert, exactly as an absent anchor
// does: a chatbox-space decoration on a theme with no chatbox has nowhere to be.
func (a *App) elementSpaceRect(lay *themeLayoutCache, space theme.Space) (sdl.Rect, bool) {
	switch space {
	case theme.SpaceCourtroom:
		if lay.area.W <= 0 || lay.area.H <= 0 {
			return sdl.Rect{}, false
		}
		return lay.area, true
	case theme.SpaceChatbox:
		return lay.rect("ao2_chatbox")
	case theme.SpaceViewport:
		return lay.rect("viewport")
	case theme.SpaceMusicDisplay:
		return lay.rect("music_display")
	case theme.SpacePane:
		// `space = pane` with no anchor names no pane. Inert rather than defaulted:
		// guessing a pane would put the element somewhere the author never wrote.
		return sdl.Rect{}, false
	}
	return sdl.Rect{}, false
}

// absSlotRect is a themeSlots key's ABSOLUTE window box.
//
// lay.r does NOT hold absolutes for every key: AO2 declares four coordinate systems
// in courtroom_design.ini and only the courtroom-relative ones are made
// window-absolute at rebuild time — showname/message stay chatbox-relative,
// music_name stays plate-relative and the two evidence icons stay viewport-relative,
// because the widgets that own those parents re-base them at draw. An element
// anchored to one of them has to do the same re-basing, or a badge pinned to the
// showname would land at the top-left of the screen.
func absSlotRect(lay *themeLayoutCache, key string) (sdl.Rect, bool) {
	r, ok := lay.rect(key)
	if !ok {
		return sdl.Rect{}, false
	}
	var parent string
	switch themeSlotRel(key) {
	case relCourtroom:
		return r, true
	case relChatbox:
		parent = "ao2_chatbox"
	case relViewport:
		parent = "viewport"
	case relMusicDisplay:
		parent = "music_display"
	}
	p, ok := lay.rect(parent)
	if !ok {
		return sdl.Rect{}, false
	}
	r.X += p.X
	r.Y += p.Y
	return r, true
}

// snapFullCanvas applies design §6.6 R7. See elemFullCanvasPct.
func snapFullCanvas(r, canvas sdl.Rect) sdl.Rect {
	if canvas.W <= 0 || canvas.H <= 0 {
		return r
	}
	sect, hits := r.Intersect(&canvas)
	if !hits {
		return r
	}
	covered := int64(sect.W) * int64(sect.H) * 100
	if covered >= int64(canvas.W)*int64(canvas.H)*elemFullCanvasPct {
		return canvas
	}
	return r
}

// ---------------------------------------------------------------------------
// Panes
// ---------------------------------------------------------------------------

// bakePanes REPARENTS every pane host: the host's entry in lay.r is rewritten to a
// box inside the pane, and the widget's own draw site — unchanged, called once —
// then paints there.
//
// NOT "call the host's draw site twice with two rects" (design §Q1): the
// immediate-mode kit has ONE drag owner (c.dragID) and a documented single wheel
// consumer (c.WheelIn), and hovering() would register twice. A widget cannot be in
// two places.
//
// EXACTLY ONCE per host per rebuild. Host sets are disjoint by construction (the
// reader drops a duplicate claim), and this walks each pane's hosts in order, so no
// key can be rewritten twice — TestPaneReparentsExactlyOnce pins it, because a
// second reparent would compose the pane offset onto an already-reparented rect and
// walk the widget off the screen one rebuild at a time.
func (a *App) bakePanes(lay *themeLayoutCache, sc *theme.Sidecar) {
	if len(sc.Panes) == 0 {
		return
	}
	for i := range sc.Panes {
		p := &sc.Panes[i]
		pane, ok := a.paneRect(lay, p)
		if !ok {
			continue // a pane whose space is absent re-homes nothing
		}
		for h := 0; h < p.NHosts && h < len(p.Hosts); h++ {
			key := p.Hosts[h]
			if key == "" {
				continue
			}
			// COURTROOM-RELATIVE WIDGETS ONLY. showname/message ride the chatbox,
			// music_name rides the plate and the two evidence icons ride the stage:
			// their entries in lay.r are PARENT-relative and their draw sites re-base
			// them, so re-homing one would compose the pane offset on top of a
			// re-basing that still happens — the widget would land at pane + parent.
			// A child moves by moving its parent, which is what a pane hosting
			// ao2_chatbox already does.
			if themeSlotRel(key) != relCourtroom {
				continue
			}
			cur, present := lay.r[key]
			if !present {
				continue // the theme does not place this widget; there is nothing to re-home
			}
			// §6.6 R8: a host KEEPS its [overrides] rect, interpreted PANE-RELATIVE.
			// Without one it keeps its own design rect, re-based onto the pane's
			// origin instead of the canvas's — which is the definition of a
			// reparent, and what makes a toggle pair share a sub-rect by overriding
			// both to the same box.
			rel := cur
			if ov, has := sc.OverrideRect(key); has {
				rel = sdl.Rect{
					X: int32(float64(ov.X) * lay.scaleX),
					Y: int32(float64(ov.Y) * lay.scaleY),
					W: int32(float64(ov.W) * lay.scaleX),
					H: int32(float64(ov.H) * lay.scaleY),
				}
			} else {
				// A courtroom-relative rect in lay.r is already window-absolute; take
				// its design-space offset back off before re-basing it.
				rel.X -= lay.area.X
				rel.Y -= lay.area.Y
			}
			r := sdl.Rect{X: pane.X + rel.X, Y: pane.Y + rel.Y, W: rel.W, H: rel.H}
			if clamped, fits := clampRectInto(r, pane); fits {
				r = clamped
			}
			lay.r[key] = r
		}
	}
}

// paneRect is a pane's ABSOLUTE box. A pane declares its rect in a space exactly as
// an element does, minus the anchor (panes do not nest, so there is nothing to
// anchor to).
func (a *App) paneRect(lay *themeLayoutCache, p *theme.Pane) (sdl.Rect, bool) {
	if p.Space == theme.SpaceWindow {
		r := sdl.Rect{X: int32(p.Rect.X), Y: int32(p.Rect.Y), W: int32(p.Rect.W), H: int32(p.Rect.H)}
		if r.W <= 0 || r.H <= 0 {
			return sdl.Rect{}, false
		}
		return r, true
	}
	origin, ok := a.elementSpaceRect(lay, p.Space)
	if !ok {
		return sdl.Rect{}, false
	}
	r := sdl.Rect{
		X: origin.X + int32(float64(p.Rect.X)*lay.scaleX),
		Y: origin.Y + int32(float64(p.Rect.Y)*lay.scaleY),
		W: int32(float64(p.Rect.W) * lay.scaleX),
		H: int32(float64(p.Rect.H) * lay.scaleY),
	}
	if r.W <= 0 || r.H <= 0 {
		return sdl.Rect{}, false
	}
	return r, true
}

// ---------------------------------------------------------------------------
// Sort
// ---------------------------------------------------------------------------

// sortBakedElements orders lay.el[0:n] by (band, z, declaration index) — an
// INSERTION SORT, which is stable, allocation-free and, over at most elemZSortCap
// entries on a cold path, faster than the machinery sort.SliceStable would set up
// (a closure and a reflect swapper, both allocations).
//
// STABILITY IS THE FORMAT. docs/THEME-FORMAT.md §4: "within a band, elements paint
// in ascending z, ties broken by declaration order". The input is already in
// declaration order, so stability is the whole of that second clause — and it is
// what TestElementDrawOrderIsBakedNotSorted checks stays true without the draw path
// ever sorting anything.
func (a *App) sortBakedElements(lay *themeLayoutCache, n int) {
	if n > elemZSortCap {
		n = elemZSortCap
	}
	for i := 1; i < n; i++ {
		el, band, z := lay.el[i], a.elemBakeBand[i], a.elemBakeZ[i]
		j := i - 1
		for j >= 0 && bakedAfter(a.elemBakeBand[j], a.elemBakeZ[j], band, z) {
			lay.el[j+1], a.elemBakeBand[j+1], a.elemBakeZ[j+1] = lay.el[j], a.elemBakeBand[j], a.elemBakeZ[j]
			j--
		}
		lay.el[j+1], a.elemBakeBand[j+1], a.elemBakeZ[j+1] = el, band, z
	}
}

// bakedAfter reports that (xBand, xZ) must paint AFTER (yBand, yZ). Strict: equal
// keys return false, so the insertion sort leaves ties in their arrival
// (declaration) order.
func bakedAfter(xBand theme.ElementBand, xZ int16, yBand theme.ElementBand, yZ int16) bool {
	if xBand != yBand {
		return xBand > yBand
	}
	return xZ > yZ
}

// ---------------------------------------------------------------------------
// Value conversion
// ---------------------------------------------------------------------------

// sdlColorOf converts a sidecar colour to SDL's. Two structs with the same four
// fields and no shared type, because internal/theme is SDL-free by hard rule 1.
func sdlColorOf(c theme.RGBA) sdl.Color { return sdl.Color{R: c.R, G: c.G, B: c.B, A: c.A} }

// fadeColor folds the element's opacity into a colour's alpha at BAKE time, so the
// painters never multiply anything and a transparent fill stays transparent.
func fadeColor(c theme.RGBA, opacity uint8) sdl.Color {
	out := sdlColorOf(c)
	if opacity != theme.OpacityOpaque {
		out.A = uint8(int(out.A) * int(opacity) / theme.OpacityOpaque)
	}
	return out
}
