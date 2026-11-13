package ui

// Free-element bake (docs/THEME-FORMAT.md): everything expensive about an element happens here, once per cold rebuild
// of themeLayoutCache: pane reparenting, anchor resolution, space transform, canvas fit, clamp, label truncation,
// condition interning and band sort. The draw path (themeelements.go) then reads values.
//
// This is a separate file from the painters because the draw path must build no strings, and this cold path is where
// string operations are correct.
//
// The rebuild runs on the render thread during a frame (resize, fit change, theme apply), so the bake is bounded: fixed
// arrays, one pass per element, an insertion sort over at most theme.ElementCap entries, no map growth, no disk.

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// elemShapeNames maps the format's `shape =` wire names onto the draw path's ids. Wire names are the format:
// append-only, never renamed. An unknown name degrades to elemShapeSharp rather than skipping the element, so the
// author keeps fill, stroke, colour and position.
//
// The first three are shapemask.go's shipped vocabulary (shapemask.go:38-40, and internal/config), and every `shape =`
// line in the shipped themes uses one of them. `shape` is read as free text, not an enum, so a rename would degrade
// silently with no note in the import report.
var elemShapeNames = [elemShapeCount]string{"sharp", "rounded", "hex", "ribbon", "tape", "pill"}

// elemShapeAliases are additional accepted spellings of an id that has a canonical name. Append-only: a spelling that
// once resolved must keep resolving.
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

// elemShapeIDOK resolves a wire name and reports whether it is in the vocabulary; ok=false means the caller holds the
// degrade value.
//
// Case-insensitive, since `shape` is free text (sidecar_read.go) and "Rounded" should not degrade to a plain box.
// Folded per compare, not lowered once: strings.ToLower allocates on an upper-case rune and this runs per element per
// rebuild, while EqualFold and TrimSpace allocate nothing. elemShoutStem does the same.
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

// elemFullCanvasPct: an empty-anchor courtroom-space element covering at least this percentage of the active canvas
// after the uniform fit is a full-canvas element and is snapped flush to the canvas. A wallpaper authored for one
// canvas lands a pixel short on another after fit rounding, and a seam of client colour at one edge is a defect.
const elemFullCanvasPct = 90

// elemZSortCap is the insertion sort's bound, theme.ElementCap by construction. An insertion sort allocates nothing,
// where sort.SliceStable allocates a closure and a reflect swapper.
const elemZSortCap = theme.ElementCap

// bakeElement is one element mid-bake: the baked row plus two sort keys the draw path doesn't need. The keys live in
// two parallel arrays on App (elemBakeBand / elemBakeZ) rather than in a second copy of the baked array; the sort
// permutes all three in lockstep.
type bakeElement struct {
	el   bakedElement
	band theme.ElementBand
	z    int16
}

// bakeThemeElements fills lay.el / lay.elN / lay.band / lay.condVal from the active theme's sidecar, after reparenting
// every pane host into lay.r.
//
// Order is load-bearing: panes rewrite the slot rects, and an anchored element resolves against the reparented (final)
// rect.
//
// A theme with no sidecar (every stock AO2 theme) leaves the arrays zeroed and returns immediately, so the feature
// costs nothing.
func (a *App) bakeThemeElements(lay *themeLayoutCache) {
	// Every rebuild gets a fresh generation so App's per-axis condition memo can't carry an index resolved against the
	// previous intern table (a.condGen).
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
	// [effect.<widget key>] last, so a theme that filled the array with its own elements loses its binds, not the
	// elements the author positioned by hand.
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
	// Band ranges over the sorted array: one pass records each band's first and last index.
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

// bakeOneElement resolves one element into its baked row. ok=false means inert: its anchor or parent space is absent
// from this layout, so it can't be positioned and must not paint.
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
			// The tint takes the opacity fold like every other colour: it is the only route `opacity` has into image
			// and generator art (paintElementArt's one alpha knob is SetAlphaMod(tint.A)). Read through sdlColorOf, an
			// absent tint became opaque white and a wash authored at `opacity = 60` drew at full strength.
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
		// The art, resolved once. The key comes from the plan by index: deriving a generator key lowercases a name and
		// hashes params, and the bake's allocation ceiling is constant.
		//
		// nil is the ordinary answer for art that hasn't landed, was evicted or was refused: the painter draws nothing,
		// and themeMediaPage has already kicked the paced heal.
		be.el.page, _ = a.themeMediaPage(a.themeMediaPlan.ElemKey(idx))
	case theme.ElemText:
		// The ink is `color`, not `fill`, and the one colour an absent value resolves to white for (EffectiveColor): a
		// theme that sizes and places a string but forgets the colour still meant it to be read.
		be.el.col = fadeColor(src.EffectiveColor(), op)
		be.el.label = a.bakeElementLabel(src.Text, r.W)
	case theme.ElemPane:
		// The pane's own divider, folded in when the element declares no stroke; the author's element wins.
		if p, found := sc.FindPane(src.Anchor); found && !src.Stroke.Opaque() && p.DividerPx > 0 {
			be.el.stroke = fadeColor(p.Divider, op)
			be.el.strokePx = int32(p.DividerPx)
		}
	}
	be.el.cond = a.internCondition(lay, src.VisibleWhen)
	return be, true
}

// elemBindZ is the z every [effect.*] bind bakes at, the top of the overlay band: a bind highlights a widget, so it
// must sit above free elements in the same band or a plate would cover the glow. It is int16's maximum because "on top
// of everything" is the intent. Binds keep declaration order among themselves because the insertion sort is stable.
const elemBindZ = int16(32767)

// bakeEffectBind turns one [effect.<widget key>] section into a baked element.
//
// A bind is additive paint like every element ("Elements are additive paint only", docs/THEME-FORMAT.md): a plate over
// the widget's rect driven by the bind's effect, not a transform on the widget. lay.r is the geometry the widget draws
// and hit-tests at, so an effect that offset or scaled it would slide a button's pixels away from its click target.
//
// `amp_pct` is the wash's peak opacity, folded into the plate's alpha: shipped `[effect.*]` colours are bare opaque
// `#rrggbb`, and read literally that is an opaque rectangle over the widget. Peak opacity times the effect's envelope
// (bakedElement.wash) keeps a bind from burying its widget.
//
// ok=false is inert: no effect, no amplitude, no colour, or the target is absent from this layout.
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
	// The baked row: a flat plate (a bind's shape is the widget's, not the author's); col is the authored hue at peak
	// opacity; wash is the alpha rule that makes the plate the effect (themeelements.go); tint is opaque white; cond is
	// always visible, so the one-shot resolves off the theme clock's zero (fxAlwaysOrigin).
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

// bakeElementLabel truncates an element's string to its own box once; truncateLabelTo allocates when it shortens, so it
// can't run at draw time (as with themeLayoutCache.toggleLabel).
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

// elemShoutConditions is the `visible_when = shout:<name>` vocabulary. AO2 spells a shout two ways: the design key a
// theme author types (`anchor = hold_it`; courtroom_design.ini, themeslots.go:177-180, courtroom.cpp:995 / :1001 /
// :1007 / :1100) and the asset stem the wire and art use, which courtroom.ShoutName returns (holdit, objection,
// takethat, custom; urlbuilder.go:543). Three of four differ, so a condition in the design-key spelling would never
// match the live value. The stems come from courtroom.ShoutName so this table can't drift.
var elemShoutConditions = [...]struct {
	key       string // the courtroom_design.ini / themeslots.go design key
	objection int    // the MS objection modifier ShoutName maps to the asset stem
}{
	{key: "hold_it", objection: 1},
	{key: "objection", objection: 2},
	{key: "take_that", objection: 3},
	{key: "custom_objection", objection: 4},
}

// elemConditionValue is the value a condition interns under: the live spelling. Only `shout:` normalises; other axes
// compare a value the session hands over verbatim.
func elemConditionValue(cond theme.Condition) string {
	if cond.Axis != theme.CondShout {
		return cond.Value
	}
	return elemShoutStem(cond.Value)
}

// elemShoutStem folds either spelling of a shout onto the asset stem Courtroom.CurrentShout reports. An unrecognised
// value is returned verbatim and never matches. Allocation-free (EqualFold, constants), against the bake's
// elemBakeAllocCeil.
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

// internCondition maps a VisibleWhen value to an index into lay.condVal, adding it on first sight; bounded by
// theme.ElementCap (at most one new value per element). Value-less axes (always, speaking) intern nothing:
// elementVisible switches on the axis first.
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

// resolveElementRect resolves an element's absolute rect (docs/THEME-FORMAT.md).
//
// With a non-empty anchor the authored rect is a signed delta on the anchor's resolved box, in design pixels for every
// space except `window`. With an empty anchor it is absolute in its declared space.
//
// Elements are not clamped into the canvas, unlike widgets: the inflate idiom (`-6, -6, 12, 12`) deliberately reaches
// outside its anchor, and SDL clips what runs off the canvas.
func (a *App) resolveElementRect(lay *themeLayoutCache, sc *theme.Sidecar, src *theme.Element) (sdl.Rect, bool) {
	// SpaceWindow is the one space outside the design canvas: window pixels, no scale, offset or canvas clamp ("extras
	// go in chrome outside the canvas").
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

	// Everything else is design space; the delta (or absolute rect) scales with the canvas as a widget's does.
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

// elementAnchorRect resolves an element's anchor to an absolute box: a pane id for `space = pane`, else a themeSlots
// key. Anchors may not chain (docs/THEME-FORMAT.md), so one bake pass suffices.
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

// elementSpaceRect is the origin box of a space for an element with no anchor; an absent parent makes the element
// inert.
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
		// `space = pane` with no anchor names no pane: inert, not defaulted.
		return sdl.Rect{}, false
	}
	return sdl.Rect{}, false
}

// absSlotRect is a themeSlots key's absolute window box. lay.r holds absolutes only for courtroom-relative keys:
// showname and message stay chatbox-relative, music_name plate-relative and the evidence icons viewport-relative,
// because their owners re-base at draw. An anchored element must re-base too, or a badge on the showname would land
// top-left.
func absSlotRect(lay *themeLayoutCache, key string) (sdl.Rect, bool) {
	r, ok := lay.rect(key)
	if !ok {
		// AO2's second spelling, the same fold the [overrides] applier does (sidecarOverrideTarget): one widget has two
		// design names, so anchoring to `immediate` on a layout filled as `pre_no_interrupt` would otherwise go inert
		// silently.
		alt, hasAlt := themeSlotOtherSpelling(key)
		if !hasAlt {
			return sdl.Rect{}, false
		}
		if r, ok = lay.rect(alt); !ok {
			return sdl.Rect{}, false
		}
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

// snapFullCanvas applies the full-canvas snap. See elemFullCanvasPct.
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

// bakePanes reparents every pane host: the host's entry in lay.r is rewritten to a box inside the pane, and the
// widget's own draw site, unchanged and called once, paints there. It can't call the draw site twice with two rects:
// the kit has one drag owner (c.dragID) and a single wheel consumer (c.WheelIn), and hovering() would register twice.
//
// Exactly once per host per rebuild: host sets are disjoint (the reader drops a duplicate claim), so no key is
// rewritten twice; a second reparent would compose the pane offset onto an already-reparented rect and walk the widget
// off screen.
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
			// Courtroom-relative widgets only: showname and message ride the chatbox, music_name the plate, the
			// evidence icons the stage. Their lay.r entries are parent-relative and re-based at draw, so re-homing one
			// would add the pane offset on top of that re-basing. A child moves with its parent.
			if themeSlotRel(key) != relCourtroom {
				continue
			}
			cur, present := lay.r[key]
			if !present {
				continue // the theme does not place this widget; there is nothing to re-home
			}
			// A host keeps its [overrides] rect read pane-relative, else its design rect re-based onto the pane's
			// origin (so a toggle pair can share a sub-rect by overriding both).
			//
			// Read from lay.r, never sc.Overrides: lay.r already carries foldSidecarOverrides, the player's drags
			// (applyRectOverrides) and themeLayoutIn's scaling. Asking the sidecar again was a second copy of the
			// policy that disagreed (inert rows like `player_list`, AO2's alias fold for `immediate`, the theme author
			// overruling a player's drag against themeoverrides.go:28-34).
			//
			// lay.r holds window-absolute rects for courtroom-relative keys; subtract the design offset before
			// re-basing onto the pane.
			rel := cur
			rel.X -= lay.area.X
			rel.Y -= lay.area.Y
			r := sdl.Rect{X: pane.X + rel.X, Y: pane.Y + rel.Y, W: rel.W, H: rel.H}
			if clamped, fits := clampRectInto(r, pane); fits {
				r = clamped
			}
			lay.r[key] = r
		}
	}
}

// paneRect is a pane's absolute box: its rect in a space, as an element declares one, minus the anchor (panes don't
// nest).
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

// sortBakedElements orders lay.el[0:n] by (band, z, declaration index) with an insertion sort: stable, allocation-free,
// and faster over at most elemZSortCap entries than sort.SliceStable's closure and reflect swapper.
//
// Stability is the format (docs/THEME-FORMAT.md): within a band elements paint in ascending z, ties by declaration
// order. The input is already in declaration order, so the draw path never sorts.
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

// bakedAfter reports that (xBand, xZ) must paint after (yBand, yZ). Strict: equal keys return false, leaving ties in
// declaration order.
func bakedAfter(xBand theme.ElementBand, xZ int16, yBand theme.ElementBand, yZ int16) bool {
	if xBand != yBand {
		return xBand > yBand
	}
	return xZ > yZ
}

// sdlColorOf converts a sidecar colour to SDL's: two structs with the same fields and no shared type, since
// internal/theme is SDL-free (hard rule 1).
func sdlColorOf(c theme.RGBA) sdl.Color { return sdl.Color{R: c.R, G: c.G, B: c.B, A: c.A} }

// fadeColor folds the element's opacity into a colour's alpha at bake time so the painters multiply nothing.
func fadeColor(c theme.RGBA, opacity uint8) sdl.Color {
	out := sdlColorOf(c)
	if opacity != theme.OpacityOpaque {
		out.A = uint8(int(out.A) * int(opacity) / theme.OpacityOpaque)
	}
	return out
}
