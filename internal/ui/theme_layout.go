package ui

// Theme layout engine: when the active theme ships AO2's
// courtroom_design.ini geometry ("courtroom" + "viewport" rects at
// minimum), the courtroom adopts it wholesale — every widget the theme
// positions draws at its design rect (scaled to the window, letterboxed),
// with the theme's own button art. Elements the theme doesn't define fall
// back per-widget; themes without the geometry keep the classic layout
// entirely. That is what "applying a theme" means to an AO player.

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// AO2 themes "hide" elements by flinging them off the design space
// (11037 by convention, on either axis); anything starting past the
// design bounds is treated as absent. Qt also CLIPPED overhanging
// children at the fixed window edge — themes rely on that, so scaled
// rects clamp into the courtroom area instead of drawing into the void.
const (
	// minThemedElementPx rejects degenerate post-scale rects (a 3 px
	// "button" is sloppy theme data, not a widget).
	minThemedElementPx = 6
	// logMergeOverlapFrac: when the IC and OOC log rects overlap this
	// much, the theme meant them as one toggled widget (AO2's ooc_toggle)
	// — we draw them tabbed in the shared rect.
	logMergeOverlapFrac = 0.6
)

// clampRectInto shifts r inside bounds, shrinking only when r is larger
// than bounds on an axis. Qt amputated these at the window edge; shifting
// keeps the whole widget visible and usable. ok=false when r never
// intersected bounds at all (treated as hidden).
func clampRectInto(r, bounds sdl.Rect) (sdl.Rect, bool) {
	if _, hits := r.Intersect(&bounds); !hits {
		return sdl.Rect{}, false
	}
	if r.W > bounds.W {
		r.W = bounds.W
	}
	if r.H > bounds.H {
		r.H = bounds.H
	}
	if r.X < bounds.X {
		r.X = bounds.X
	}
	if r.Y < bounds.Y {
		r.Y = bounds.Y
	}
	if r.X+r.W > bounds.X+bounds.W {
		r.X = bounds.X + bounds.W - r.W
	}
	if r.Y+r.H > bounds.Y+bounds.H {
		r.Y = bounds.Y + bounds.H - r.H
	}
	return r, true
}

// rectOverlapFrac reports how much of the smaller rect lies inside the
// other (0..1).
func rectOverlapFrac(a, b sdl.Rect) float64 {
	sect, hits := a.Intersect(&b)
	if !hits {
		return 0
	}
	areaA := int64(a.W) * int64(a.H)
	areaB := int64(b.W) * int64(b.H)
	small := areaA
	if areaB < small {
		small = areaB
	}
	if small <= 0 {
		return 0
	}
	return float64(int64(sect.W)*int64(sect.H)) / float64(small)
}

// themeLayoutCache holds the window-scaled design rects; recomputed only
// when the window size or the theme changes (render loop reads are plain
// map probes on pre-scaled rects).
type themeLayoutCache struct {
	valid          bool
	winW, winH     int32
	designW        int32
	scaleX, scaleY float64 // per-axis: equal for Native/Letterbox/Crop/Custom, independent for Stretch
	// textPct is the canvas scale the CHATBOX TEXT folds in, as an integer percent,
	// baked at rebuild time (see canvasTextScalePct / chatboxfit.go for the AO2
	// provenance). It is stored rather than derived per frame for two reasons: the
	// draw path then stays integer-only, so a font size cannot wobble between two
	// frames of an unchanged window; and the float rounding happens exactly once per
	// window/fit/theme change instead of twice per frame.
	//
	// 0 means "no canvas": the early return above for a theme with no courtroom /
	// viewport rect leaves it zero, and so does every hand-built cache in a test.
	// textScalePct reads that as 100 — no fold, byte-identical to the old geometry.
	textPct int
	// shownameExtra is `showname_extra_width` in WINDOW pixels — the theme's
	// design-space value already multiplied by the canvas scale, baked here for the
	// same two reasons textPct is: the draw path stays integer-only, and the float
	// rounding happens once per window/fit/theme change instead of once per frame.
	//
	// AO2 does NOT scale it (get_element_dimensions applies themeScalingFactor to
	// the showname rect, get_design_element hands back extra_width raw), but AO2
	// also ships that factor at 1, so the two agree everywhere AO2 actually runs.
	// Our canvas scale is not 1 the moment a window stops matching the theme's
	// design size, and an unscaled extra would widen the box by a different
	// fraction of the art at every window size — the exact parity defect §4.7a
	// describes, reintroduced one rect at a time.
	shownameExtra int32
	offX, offY    int32
	// topOff is the app-chrome band reserved ABOVE the canvas (#14; topChromeH).
	// Part of the cache key: the live courtroom builds with the band, an export
	// frame builds without it, and both share this one cache — so a stale entry
	// built for the other would silently draw the canvas at the wrong height.
	topOff int32
	// tabStripW is the server-tab strip's INTRINSIC width (tabStripIntrinsicW) at build
	// time, and part of the cache key. The strip is the one entry in r that is not a
	// transform of a design rect — it is sized by its chips — so opening, closing or
	// renaming a tab resizes it with no window, band, fit or theme change to invalidate
	// on. Without this the themed editor would keep handing out a box the strip has
	// outgrown, and a box that is not on the widget is worse than no box.
	tabStripW int32
	// tabStripParked is tabStripThemeParked() at build time, and part of the key for
	// exactly the same reason as the width: it selects which of the strip's TWO
	// completely different placements r holds (the docked chrome band, or the theme
	// canvas), and it can flip with no window, band, fit or theme change behind it.
	//
	// The flip that proved it: a press-move-release whose net design delta rounds to
	// nothing is discarded at release, so parked-ness goes true (the in-flight drag
	// arm) → false, and the cache kept the parked box for good. Measured at Letterbox
	// 1000x900, the editor's box sat at {459,23,79,22} while the strip painted
	// {460,22,79,22}, and idle editor frames never healed it — only a window, fit,
	// theme or chip-width change did. The two reset paths and restoreLayout already
	// invalidated by hand; the release was the one mutation of parked-ness that did
	// not. In the key it cannot be forgotten again.
	tabStripParked bool
	// area is the canvas: the scaled "courtroom" design rect at (offX, offY). Stored
	// rather than re-derived, because the old `W: w-2*offX, H: h-2*offY` trick only
	// worked while the bars were symmetric — with a band reserved at the top they
	// are not, and that arithmetic would stretch the backdrop past the bottom edge.
	area sdl.Rect
	fit  int                 // ThemeFit mode it was built for (rebuild on change)
	r    map[string]sdl.Rect // scaled; absolute except showname/message (chatbox-relative)
	// ang holds each rotatable key's persisted rotation angle byte (A4), baked in
	// alongside r at rebuild time from the active theme's ThemeRectRotations so the
	// draw path resolves angles lock-free (a plain map probe, never a prefs read).
	// nil / an absent key means angle 0 = the plain, byte-identical Copy path.
	ang map[string]uint8
	// toggleLabel holds each AO2 toggle's label already shortened to its own design
	// rect (truncateLabelTo, ui.go — AO2's truncate_label_text). Baked here rather
	// than computed at draw time because a theme legitimately declares rects too
	// narrow for their labels — aceattorney2x gives `flip` 51x19, leaving 29 px —
	// and truncation allocates, which the themed frame's zero-alloc gate forbids.
	// An absent key means "the label fits": the draw site falls back to the
	// constant, which is the byte-identical path for every theme with roomy rects.
	toggleLabel map[string]string
}

// themeLayout builds the cache for a canvas laid out inside the FULL (w, h) box,
// origin-anchored — no client chrome reserved. That is what an offscreen export
// frame wants (gifexport.go: a video frame is not the window and must never carry
// the client's own furniture), and it is the plain geometry the fit-mode tests pin.
// The live window goes through themeWindowLayout instead.
func (a *App) themeLayout(w, h int32) *themeLayoutCache {
	return a.themeLayoutIn(w, h, 0)
}

// themeWindowLayout is the WINDOW's layout: the design canvas is fitted into the
// height LEFT under the app-chrome band and offset down by it (#14).
//
// In the three modes that cannot overflow — Native, Letterbox, Stretch — that puts
// the canvas itself wholly below the band. Crop scales UP by design and Custom can be
// panned anywhere, so in those two the ART may run up behind the band and the band's
// owners simply paint over it. What holds in ALL FIVE modes is the property that
// matters: no widget the theme places is ever laid out above the band, where it would
// be both painted over and fenced out of every hit test (see themeLayoutIn's bounds).
func (a *App) themeWindowLayout(w, h int32) *themeLayoutCache {
	return a.themeLayoutIn(w, h, a.topChromeH())
}

// invalidateThemeCanvases drops BOTH scaled-geometry caches — the courtroom's
// (themeLayoutCache) and character select's (charSelectLayoutCache).
//
// ONE function instead of an assignment repeated at every mutation site, because the
// two caches have DIFFERENT keys and the difference is invisible where the mutation
// happens. themeLayoutCache keys on (w, h, band, fit, strip width, strip parked);
// charSelectLayoutCache keys on (w, h, band, fit, themed) and on nothing else. So
// ThemeZoom and ThemePan — which only ThemeFitCustom reads, inside the shared
// fitDesignCanvas — are in NEITHER key, and every site that moves them has to say so
// out loud. Five of them did not: the three Custom-fit sliders and the fit preview's
// wheel-zoom and drag-pan cleared themeLay alone, so the courtroom re-fitted while
// char select's canvas, its eleven widget rects and its whole grid stayed at the old
// zoom and pan until some unrelated rebuild happened to clear them. (Repro: Settings →
// drag the fit preview → re-pick a character.)
//
// Both fields are plain structs on App and clearing a bool costs nothing, so the safe
// rule is to clear both at every site and never ask the question again. A rebuild is
// LAZY — the cache only re-derives when its screen next asks for it — so invalidating
// a canvas that is not on screen is free.
//
// charselectlayout_test.go pins that no other file assigns either `valid` flag.
func (a *App) invalidateThemeCanvases() {
	a.themeLay.valid = false
	a.charSelLay.valid = false
}

// themeLayoutIn returns the cache for a (w, h) box with `top` px reserved at its top
// edge, rebuilding on window resize, band change, fit change or theme swap
// (pollThemeApply invalidates).
func (a *App) themeLayoutIn(w, h, top int32) *themeLayoutCache {
	lay := &a.themeLay
	fit := a.d.Prefs.ThemeFitMode()
	// Measured before the validity test, because they ARE part of the key (see
	// themeLayoutCache.tabStripW / .tabStripParked). Both are alloc-free and
	// independent of every value built below, so neither can recurse into this
	// rebuild — and parked-ness is resolved ONCE here and handed down to
	// tabStripCacheRect rather than probed twice per rebuild.
	stripW := a.tabStripIntrinsicW()
	stripParked := a.tabStripThemeParked()
	if lay.valid && lay.winW == w && lay.winH == h && lay.topOff == top && lay.fit == fit &&
		lay.tabStripW == stripW && lay.tabStripParked == stripParked {
		return lay
	}
	*lay = themeLayoutCache{winW: w, winH: h, topOff: top, fit: fit, tabStripW: stripW, tabStripParked: stripParked}
	court, okCourt := a.themeRects["courtroom"]
	_, okVP := a.themeRects["viewport"]
	if !okCourt || !okVP || court.W <= 0 || court.H <= 0 {
		return lay
	}
	lay.designW = int32(court.W)
	// The canvas placement itself is fitDesignCanvas (below) — shared verbatim with
	// char select's own canvas (#20), which honours the same ThemeFit by product
	// decision and must never round differently.
	f := a.fitDesignCanvas(int32(court.W), int32(court.H), w, h, top, fit)
	lay.scaleX, lay.scaleY = f.scaleX, f.scaleY
	// Rounded HERE, on the cold rebuild path, so the chatbox's font resolution is
	// pure integer arithmetic on every frame that follows (chatboxfit.go).
	lay.textPct = canvasTextScalePct(f.scaleX, f.scaleY)
	// Scaled by scaleX because it is ADDED TO a width that was scaled by scaleX
	// (the showname rect, below) — the two have to move together or the widened
	// plate stops lining up with the art the swap brings in.
	//
	// Floored at one pixel rather than truncated to nothing: a rung that rounds away
	// would take the SKIN SWAP with it, so a heavily downscaled canvas would drop
	// back to the narrow chatbox art mid-message. One pixel of widening with the
	// right art beats none with the wrong art.
	if a.themeShownameExtra > 0 {
		lay.shownameExtra = int32(float64(a.themeShownameExtra) * f.scaleX)
		if lay.shownameExtra < 1 {
			lay.shownameExtra = 1
		}
	}
	lay.offX, lay.offY = f.area.X, f.area.Y
	lay.area = f.area
	// WIDGET BOUNDS = the canvas, minus whatever part of it sits above the reserved
	// chrome band. offY is only >= top while the canvas FITS the room under the band:
	// Crop scales UP on purpose and Custom's pan can push the canvas anywhere, so in
	// those two modes the centring term goes negative and the canvas top rises past
	// the band. That is fine for the ART — the band's owners (menu bar, tab strip)
	// paint over it afterwards — but NOT for widgets: lay.area is the clamp target
	// below, so a themed button could be clamped up under an opaque strip, where it is
	// painted over AND fenced out of every hit test. Clamping offY itself would be the
	// wrong cure: it would forbid Custom's upward pan (the whole point of the mode) and
	// re-anchor Crop's overflow to the top edge.
	bounds := lay.area
	if bounds.Y < top {
		bounds.H -= top - bounds.Y
		bounds.Y = top
	}
	lay.r = make(map[string]sdl.Rect, len(a.themeRects))
	for key, r := range a.themeRects {
		if r.X > court.W || r.Y > court.H {
			continue // off-design = hidden (the 11037 convention, both axes)
		}
		sr := sdl.Rect{
			X: int32(float64(r.X) * lay.scaleX),
			Y: int32(float64(r.Y) * lay.scaleY),
			W: int32(float64(r.W) * lay.scaleX),
			H: int32(float64(r.H) * lay.scaleY),
		}
		// AO2 declares FOUR coordinate systems in courtroom_design.ini — its own
		// section banners say so. Only courtroom-relative rects become
		// window-absolute here and clamp into the stage the way Qt's window edge
		// clipped them; chatbox-, viewport- and music_display-relative rects stay
		// raw and are re-based by whichever widget owns their parent. This was a
		// hard-coded `key != "showname" && key != "message"` before the themeSlots
		// registry, which had no way to express the other two.
		if themeSlotRel(key) == relCourtroom {
			sr.X += lay.offX
			sr.Y += lay.offY
			clamped, ok := clampRectInto(sr, bounds)
			if !ok {
				continue // never touched the stage: hidden
			}
			sr = clamped
			if sr.W < minThemedElementPx || sr.H < minThemedElementPx {
				continue // sloppy theme data, not a usable widget
			}
		}
		lay.r[key] = sr
	}
	// The server-tab strip (#14) is INTRINSICALLY SIZED CLIENT chrome that happens to
	// carry a design-space key, so the generic transform above is never right for it:
	// its size comes from the chips (so a stored W/H is meaningless), and while nobody
	// has parked it its home is the reserved band ABOVE this canvas — a place design
	// coordinates cannot name. tabStripCacheRect therefore OVERWRITES the transform
	// with the live painted rect in both states, which is the invariant the themed
	// editor depends on: its drag box, its drag origin, its Tab-cycle stacking order,
	// its resize-grip probe and its right-click reset all read lay.r[themeTabBarKey],
	// and a box that is not on the widget is worse than no box.
	//
	// A degenerate strip (no tabs, or chips too small to be a usable drag target) drops
	// the key entirely rather than leaving the stale transform behind — nothing is
	// painted there, so nothing may be editable there either.
	if _, present := a.themeRects[themeTabBarKey]; present {
		if strip, ok := a.tabStripCacheRect(lay, w, h, stripW, stripParked); ok {
			lay.r[themeTabBarKey] = strip
		} else {
			delete(lay.r, themeTabBarKey)
		}
	}
	// Bake the per-key rotation angles for the active theme (A4). Read once here
	// on the cold rebuild path (win/theme/fit change) — the R-key sets
	// a.themeLay.valid=false so a fresh rotation reaches the draw path via this
	// rebuild, never a per-frame prefs read. nil when the theme carries none.
	if themeName, _ := a.d.Prefs.Theme(); themeName != "" {
		lay.ang = a.d.Prefs.ThemeRectRotationSnapshot(themeName)
	}
	a.bakeToggleLabels(lay)
	lay.valid = true
	return lay
}

// sessHasCCCC reports whether the server advertises cccc_ic_support, the 2.6
// feature that carries pairing, shownames and the immediate flag. AO2 shows or
// hides ui_pair_button / ui_immediate / ui_ic_chat_name together on it
// (courtroom.cpp:747-759); a toggle for a field the wire drops is a control that
// silently does nothing.
func (a *App) sessHasCCCC() bool {
	return a.sess != nil && a.sess.Features.Has(protocol.FeatureCCCCIC)
}

// themedSwatchW is the colour swatch's width inside AO2's text_color rect. AO2
// draws no separate swatch — its combo box tints itself — but AsyncAO's free-hex
// wheel anchors on one (icSwatchRect), so the swatch takes a fixed bite off the
// left of the rect and the dropdown gets the remainder. Named per hard rule 9.
const themedSwatchW = int32(14)

// themedToggles are the AO2 checkbox rects whose labels have to survive a rect the
// theme sized for a shorter word. key is the AO2 design key, alt its second
// spelling (AO2 probes "immediate" then "pre_no_interrupt", courtroom.cpp:1072),
// and override the asyncao_* author-override rect, which WINS when declared.
var themedToggles = [...]struct{ key, alt, override, label string }{
	{key: "pre", override: "asyncao_ic_pre", label: "Pre"},
	{key: "immediate", alt: "pre_no_interrupt", override: "asyncao_ic_immediate", label: "Immediate"},
	{key: "flip", label: "Flip"},
	{key: "additive", label: "Additive"},
	{key: "slide_enable", label: "Slide"},
	{key: "showname_enable", label: "Shownames"},
	{key: "guard", label: "Guard"},
}

// bakeToggleLabels shortens each toggle's label to the rect the theme gave it,
// once per cold rebuild. See themeLayoutCache.toggleLabel for why this cannot
// happen at draw time.
func (a *App) bakeToggleLabels(lay *themeLayoutCache) {
	lay.toggleLabel = nil
	for _, t := range themedToggles {
		r, ok := themedToggleRect(lay, t.key, t.alt, t.override)
		if !ok {
			continue
		}
		short := truncateLabelTo(t.label, a.ctx.CheckboxLabelAvail(r), a.ctx.TextWidth)
		if short == t.label {
			continue // fits: leave the key absent so the draw site uses the constant
		}
		if lay.toggleLabel == nil {
			lay.toggleLabel = make(map[string]string, len(themedToggles))
		}
		lay.toggleLabel[t.key] = short
	}
}

// themedToggleRect resolves one toggle's rect in AO2's own precedence, with the
// AsyncAO override on top (#21 rule (d)): the asyncao_* key WINS when a theme
// author declares it, then the AO2 key, then AO2's own second spelling. A theme
// that declares none of the three draws nothing (rule (e)).
func themedToggleRect(lay *themeLayoutCache, key, alt, override string) (sdl.Rect, bool) {
	if override != "" {
		if r, ok := lay.rect(override); ok {
			return r, true
		}
	}
	if r, ok := lay.rect(key); ok {
		return r, true
	}
	if alt != "" {
		if r, ok := lay.rect(alt); ok {
			return r, true
		}
	}
	return sdl.Rect{}, false
}

// themedToggleLabel is the baked label for key, or the constant when it fits.
func (lay *themeLayoutCache) themedToggleLabel(key, full string) string {
	if s, ok := lay.toggleLabel[key]; ok {
		return s
	}
	return full
}

// themeCanvasFit is where ONE design canvas lands inside a window box: the
// per-axis scale, and the scaled canvas rect at its centred origin. Returned by
// value — a plain struct of four words, so the shared arithmetic costs neither
// caller an allocation on its rebuild path (hard rule 4's spirit, and both
// callers' rebuilds sit under AllocsPerRun gates).
type themeCanvasFit struct {
	scaleX, scaleY float64
	area           sdl.Rect
}

// fitDesignCanvas places a designW×designH design canvas inside the (w, h) box
// with `top` px reserved along its top edge, under ThemeFit mode `fit`.
//
// SHARED, DELIBERATELY NOT FORKED. AO2 has TWO fixed-size canvases, not one: the
// courtroom (Courtroom::set_courtroom_size) and character select
// (`this->setFixedSize(f_charselect.width, f_charselect.height)`, AO2-Client
// src/charselect.cpp:83, falling back to setFixedSize(714, 668) at :79). The
// locked product decision is that both honour the same ThemeFit, so both come
// through here. A second copy of this arithmetic would drift the moment a mode is
// added or a rounding rule changes — and a char-select backdrop that disagreed
// with the grid drawn on top of it, even by one pixel, is literally what issue
// #20 reported.
func (a *App) fitDesignCanvas(designW, designH, w, h, top int32, fit int) themeCanvasFit {
	// Everything below lays the canvas out inside the box UNDER the reserved band;
	// `top` is added back to the vertical origin at the end. A band taller than the
	// window would invert the box, so floor the usable height at one pixel — the
	// scale then collapses toward zero and the callers' minThemedElementPx floor
	// hides the widgets, which is the same degenerate handling a 0×0 theme gets.
	availH := h - top
	if availH < 1 {
		availH = 1
	}
	var f themeCanvasFit
	// Per-axis scale by fit mode: Native pins scale at 1.0 and only ever shrinks;
	// Stretch fills both axes independently (no bars, slight distortion); Crop
	// scales UP uniformly and lets the overflow run off-screen; Letterbox scales
	// DOWN uniformly with centered bars.
	sx, sy := float64(w)/float64(designW), float64(availH)/float64(designH)
	switch fit {
	case config.ThemeFitNative:
		// Stock AO2 never scales: setFixedSize makes the window BE the canvas
		// (AO2-Client charselect.cpp:83 for char select, set_courtroom_size() for the
		// courtroom), so a theme's art lands on screen pixels untouched. Our window is
		// resizable, so we reproduce that at scale exactly 1.0 and only ever scale
		// DOWN — a window smaller than the theme's canvas shrinks uniformly rather
		// than clipping anything, which matters because config.MinWindowW/H (640×480)
		// is BELOW several real themes' design sizes and ABOVE several others'. The
		// centring below already supplies the bars.
		s := math.Min(1, math.Min(sx, sy))
		f.scaleX, f.scaleY = s, s
	case config.ThemeFitStretch:
		f.scaleX, f.scaleY = sx, sy
	case config.ThemeFitCrop:
		s := math.Max(sx, sy)
		f.scaleX, f.scaleY = s, s
	case config.ThemeFitCustom:
		s := math.Min(sx, sy) * float64(a.d.Prefs.ThemeZoom()) / 100 // manual zoom over the fit
		f.scaleX, f.scaleY = s, s
	default: // ThemeFitLetterbox
		s := math.Min(sx, sy)
		f.scaleX, f.scaleY = s, s
	}
	f.area.W = int32(float64(designW) * f.scaleX)
	f.area.H = int32(float64(designH) * f.scaleY)
	f.area.X = (w - f.area.W) / 2
	// Centred in the room UNDER the band, then pushed past it. With top==0 this is
	// byte-identical to plain window centring. It puts the canvas below the band
	// whenever the canvas FITS that room; Crop and Custom deliberately overflow, and
	// each caller's own clamp/clip is what keeps their widgets out of the band.
	f.area.Y = top + (availH-f.area.H)/2
	if fit == config.ThemeFitCustom { // pan to crop where you like
		px, py := a.d.Prefs.ThemePan()
		f.area.X += int32(px) * w / 100
		f.area.Y += int32(py) * availH / 100
	}
	return f
}

// angle returns a key's baked rotation angle in degrees (0 = unrotated → the
// plain Copy path). A plain map probe on the pre-built cache.
func (l *themeLayoutCache) angle(key string) float64 {
	if l.ang == nil {
		return 0
	}
	b, ok := l.ang[key]
	if !ok || b == 0 {
		return 0
	}
	return config.RotationByteToDeg(b)
}

// textScalePct is the canvas scale the chatbox text folds in, as an integer
// percent — DefaultScalePct (a no-op fold) for a cache that never carried a
// canvas. See themeLayoutCache.textPct and chatboxfit.go.
func (l *themeLayoutCache) textScalePct() int {
	if l.textPct <= 0 {
		return DefaultScalePct
	}
	return l.textPct
}

// shownameExtraPx is `showname_extra_width` in window pixels — one rung of AO2's
// widen-and-swap ladder (chatboxfit.go). Zero means the theme declared no ladder
// (nine of the 74 reference themes), and is also what a cache that never carried
// a canvas reports, so the ladder is off by default everywhere.
func (l *themeLayoutCache) shownameExtraPx() int32 { return l.shownameExtra }

// rect fetches a usable scaled rect (absent when the theme hides or omits
// it — hidden/overhang filtering happened at build time).
func (l *themeLayoutCache) rect(key string) (sdl.Rect, bool) {
	r, ok := l.r[key]
	if !ok || r.W <= 0 || r.H <= 0 {
		return sdl.Rect{}, false
	}
	return r, true
}

// drawScreenBackdrop paints a theme screen background (lobbybackground /
// charselect_background) stretched to the window, or the flat client
// color when the theme ships none.
func (a *App) drawScreenBackdrop(w, h int32, stem string) {
	c := a.ctx
	dst := sdl.Rect{X: 0, Y: 0, W: w, H: h}
	// The lobby/server list defaults to the plain client backdrop: a busy AO2
	// lobbybackground (built for AO2's own list) often renders our server list
	// unreadable. Untick "plain lobby" in Settings → Theme to use the theme's.
	if stem == "lobbybackground" && a.d.Prefs.PlainLobbyOn() {
		c.Fill(dst, ColBackground)
		return
	}
	if page, ok := a.themePage(stem); ok {
		// &dst into cgo would heap-allocate dst every frame — even on the
		// plain-fill paths above/below (escape analysis is static).
		c.cgoRect = dst
		// A4: structured angle==0 → Copy / else CopyEx. The lobby/charselect
		// backdrops aren't editor-hoverable keys, so this resolves 0 (plain Copy)
		// today — the branch keeps the backdrop bolt-on-ready. themedRotationDeg
		// reads the baked cache map, never prefs.
		if ang := a.themedRotationDeg(stem); ang == 0 {
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(a.themeFrame(page), nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
		return
	}
	c.Fill(dst, ColBackground)
}

// drawThemeButton draws one themed widget: the theme's art when resident
// (hover = accent border), the kit's chip button otherwise. Reports clicks.
func (a *App) drawThemeButton(key, label string, r sdl.Rect) bool {
	c := a.ctx
	if page, ok := a.themePage(themeBtnStem(key)); ok {
		// &r into cgo would heap-allocate the parameter on every call (same
		// escape class as drawScreenBackdrop above).
		c.cgoRect = r
		// A4: angle 0 keeps the original Copy path (byte-identical); a persisted
		// nonzero angle rotates the button art via CopyEx (center=nil, no scratch
		// Point). The hover border and hit-test stay the un-rotated rect — only the
		// art tilts (the click surface is unchanged, per the cheap-tier scope).
		if ang := a.themedRotationDeg(key); ang == 0 {
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(a.themeFrame(page), nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
		hov := c.hovering(r)
		if hov {
			c.Border(r, ColAccent)
		}
		return hov && c.clicked
	}
	return c.Button(r, label)
}

// drawThemedSFXPicker draws the per-message SFX dropdown at rect — shared by the themed
// IC row's crammed and theme-placed (asyncao_ic_sfx, #4b) paths so they can't drift.
func (a *App) drawThemedSFXPicker(rect sdl.Rect) {
	c := a.ctx
	if next, changed := c.Dropdown("sfxdd", rect, a.sfxChoices, a.sfxChoiceIdx); changed {
		a.sfxChoiceIdx = next
		if next > 0 && next < len(a.sfxChoices) {
			a.d.Audio.PlaySFX(a.urls.SFX(a.sfxChoices[next]), 0) // preview the picked sound
		}
	}
	c.TooltipAfter("sfxdd-tip", rect, "Sound for your NEXT message — 'auto' uses the emote's own sound, or pick one to override. Extras → SFX Browser for favourites & any sound by name.")
}

// Themed panel body metrics. A theme's design rect is the WHOLE widget (AO2 sizes a
// bare Qt widget to it); the shared draw helpers below — drawICLogList,
// drawOOCLogList, drawNotesTab, drawMusicList, drawAreaList, drawPlayerList — start
// their content at list.X/list.Y, because the CLASSIC path already insets before
// calling them. So the themed path has to do its own insetting, plus reserve room for
// the chip strip it draws where AO2 had dedicated toggle buttons we have no rects for.
const (
	// themedLogInsetPx is the inner margin AO2's chatlogs get for free. ui_ic_chatlog
	// is a QTextEdit and ui_server_chatlog an AOTextArea, and BOTH are frameless —
	// AO2-Client src/courtroom.cpp:828 `ui_ic_chatlog->setFrameShape(QFrame::NoFrame);`
	// and :835 `ui_server_chatlog->setFrameShape(QFrame::NoFrame);` — so the only inset
	// left is Qt's QTextDocument::documentMargin, documented as "The margin around the
	// document. The default is 4." It is NOT theme-CSS-derived: the sole `padding` rule
	// in any shipped AO2 theme stylesheet is AOClockLabel{padding:0px}. Without it our
	// themed logs drew flush against the panel art (issue #25). The music/areas/players
	// body shares the value so one theme can't show padded logs beside a flush list.
	themedLogInsetPx = int32(4)
	// themedChipH is the height of the chip strip the themed panels draw across the top
	// of their design rect. AO2 toggles IC/OOC with ooc_toggle and the music/area/player
	// lists with its own theme-placed buttons; those rects don't exist for our extra
	// tabs (Notes, Players), so we draw compact chips INSIDE the panel instead. Kept
	// small so it costs little of a stock AO2 chatlog rect (default theme: 220px tall).
	themedChipH = int32(22)
	// themedChipGap separates two chips in the strip, and the strip from the body.
	themedChipGap = int32(4)
	// themedStripH is the vertical room the chip strip claims out of the panel's
	// INSET body before the list starts: the chips plus one gap. It is measured from
	// insetThemedBody(rect), not from the raw design rect — the chips and the list
	// share one margin, so the chip-to-list gap really is themedChipGap. Offsetting
	// the raw rect and THEN insetting would silently double it (a 4px design margin
	// becoming an 8px gap under chips that still sat flush against the theme art).
	themedStripH = themedChipH + themedChipGap
	// themedLogTabW fits the short "IC" / "OOC" chip labels.
	themedLogTabW = int32(44)
	// themedNotesTabW fits the longer "Notes" label in the same strip.
	themedNotesTabW = int32(56)
	// themedListTabW fits "Music" / "Areas".
	themedListTabW = int32(60)
	// themedPlayersTabW fits the longest label in the list strip, "Player List".
	themedPlayersTabW = int32(96)
)

// insetThemedBody applies themedLogInsetPx to a themed panel body before it goes to one
// of the shared (classic-facing) draw helpers.
//
// The LEFT edge insets but the RIGHT edge deliberately does NOT — X and W move together
// so X+W is unchanged. Two reasons: (1) the list scrollbar track is built at
// `X: list.X + list.W - scrollBarW` (drawICLogList / drawOOCLogList), and Qt keeps a
// QAbstractScrollArea's bar flush at the widget's right edge — floating ours 4px off the
// panel art would be un-AO2 and visibly wrong; (2) text already gets its right margin
// from `wrapW := list.W - scrollBarW - scrollBarGap`, so a second subtraction would
// double-count it.
//
// A rect too small to hold the margin is returned untouched: an inverted rect draws
// worse than a flush one, and rect() only guarantees minThemedElementPx.
func insetThemedBody(r sdl.Rect) sdl.Rect {
	if r.W <= themedLogInsetPx || r.H <= 2*themedLogInsetPx {
		return r
	}
	r.X += themedLogInsetPx
	r.W -= themedLogInsetPx
	r.Y += themedLogInsetPx
	r.H -= 2 * themedLogInsetPx
	return r
}

// drawCourtroomThemed is the design-driven courtroom. Geometry comes from
// the theme; behavior is shared with the classic path (same state, same
// send/poll helpers, same modals).
func (a *App) drawCourtroomThemed(w, h int32, lay *themeLayoutCache) {
	c := a.ctx
	// Layout edit mode fences every widget below (they draw, but stay
	// inert); the editor overlay paints and interacts at the very end.
	a.layoutEditFence()
	// Toolbox themed twin (A1 Phase 2): if this theme ships the optional
	// "asyncao_toolbox" design rect, the compact toolbox anchors there and the
	// themed editor drags it (it's a normal key in lay.r). Set for the whole pass —
	// the editor's over-toolbox suppression + the post-court draw both read
	// compactToolboxStripRect, which consults this. Absent ⇒ classic-slot default.
	// Cleared after the themed pass (drawCompactToolbox for THIS courtroom draws
	// post-court in app.go, so the flag must outlive this function — cleared there).
	if tr, ok := lay.rect("asyncao_toolbox"); ok {
		a.toolboxThemeRect, a.toolboxThemeRectOn = tr, true
	} else {
		a.toolboxThemeRectOn = false
	}
	// Latch this frame's toolbox decision and occlude whatever the strip covers
	// (#26 — the reported symptom is a theme whose "Call Mod" button sits under it).
	// HERE, right after the rect above is resolved and before a single themed widget
	// draws: the strip's position depends on that flag, and the fence is only
	// consulted at hit-test time, so it has to exist before the widgets it occludes
	// are drawn. drawCompactToolboxInPass (bottom of this function) replays the latch
	// rather than re-deriving it, so the fence and the pixels agree.
	a.fenceCompactToolbox(w, h)
	// …and the sprite-preview box (#37), which floats over the theme's music list.
	// AFTER the toolbox deliberately: the toolbox paints above the box, so its
	// mark-scoped owner-suspend must cover this publication and not the reverse.
	a.fenceSpritePreview()
	a.themedExtrasHint() // one-time-per-session: point players at the Extras box

	// Stage: letterbox fill, then the theme's window art over the design
	// area (this alone makes the whole screen read as "themed").
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	// The canvas comes from the cache (lay.area). It used to be re-derived here as
	// `w-2*offX, h-2*offY`, which silently assumed the letterbox bars were symmetric
	// — they are not once the app-chrome band is reserved at the top (#14), and the
	// backdrop would have run that far past the bottom edge.
	court := lay.area
	if page, ok := a.themePage("courtroombackground"); ok {
		// &court into cgo would heap-allocate court on EVERY themed frame (escape
		// analysis is static, so the plain-fill else-branch pays it too) — the same
		// class the whole-screen gate caught in the classic path. Point SDL at the
		// shared scratch instead (ui.go cgoRect contract: SDL copies the rect during
		// the call and never retains it). Nothing between here and the Copy touches
		// cgoRect: themedRotationDeg reads a map, themeFrame indexes a slice.
		c.cgoRect = court
		// A4: structured angle==0 → Copy / else CopyEx. courtroombackground is the
		// stage frame (a `fixed` themeSlots row), so it's not editor-hoverable and resolves
		// 0 every frame — themedRotationDeg reads the baked cache map (NOT prefs), so
		// this stays alloc-free on the always-drawn themed path.
		if ang := a.themedRotationDeg("courtroombackground"); ang == 0 {
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(a.themeFrame(page), nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
	} else {
		c.Fill(court, ColPanel)
	}

	vp, _ := lay.rect("viewport")
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.renderViewportZoomed(vp)
	inChat := false
	if box, ok := lay.rect("ao2_chatbox"); ok {
		inChat = c.hovering(box)
	}
	a.handleViewportZoom(vp, inChat)
	if a.vpZoom <= 1 {
		a.handleSpriteDrag(vp)
		a.handleSpriteHide(vp) // right-click → hide-sprite confirm (default ON)
	}
	a.handleHotkeys()
	if a.rehearsal {
		c.Label(vp.X+8, vp.Y+8, rehearsalBadge, ColTierYellow)
	}

	// Chatbox at its design rect; classic overlay when the theme has none.
	if box, ok := lay.rect("ao2_chatbox"); ok {
		a.drawThemedChatBox(box, lay)
	} else {
		a.drawChatOverlay(vp, false, 0, 0) // themed layout owns its chatbox geometry
	}
	a.drawCourtOverlays(vp, lay)
	a.drawReactionFloats(vp) // #2: emoji reactions rising over the stage (0-alloc when none)

	// Modal popups: same shared list as the classic path, so the two can't
	// drift (the bg picker once drew in classic but was missing here).
	//
	// And the same EDITOR GUARD as the classic path, which this return never had: a
	// modal open when the themed editor armed ended the pass here, so the editor never
	// drew — no banner, no Done chip, no Esc handler — while its fence had already
	// killed the modal's own buttons and the menu bar and tab strip had both stood
	// down for it. See modalReturnSkippedWhileEditing (screens.go).
	if !a.modalReturnSkippedWhileEditing() && a.drawCourtroomModals(w, h) {
		return
	}

	// Logs: IC at ic_chatlog, OOC at server_chatlog. Themes that stack the two on
	// the same rect meant them as one toggled widget (AO2's ooc_toggle) — detect
	// the overlap and draw them tabbed instead of on top of each other.
	//
	// ms_chatlog is NOT a server_chatlog alias, and the fallback that treated it
	// as one is gone. AO2 places the DEBUG log there — set_size_and_pos(
	// ui_debug_log, "ms_chatlog"); // Old name, still use it to not break
	// compatibility (courtroom.cpp:831) — and the server chatlog at its own key
	// (:834). A theme declaring only ms_chatlog gets NO server chatlog in AO2,
	// because set_size_and_pos hides a widget whose rect is missing
	// (courtroom.cpp:1334-1338); falling back drew the OOC log in the debug log's
	// box instead. Measured before removing it: of the courtroom_design.ini files
	// AO2-Client ships, every single one declares BOTH keys, so this fallback was
	// unreachable for every shipped theme.
	icRect, okIC := lay.rect("ic_chatlog")
	oocRect, okOOC := lay.rect("server_chatlog")
	merged := okIC && okOOC && rectOverlapFrac(icRect, oocRect) >= logMergeOverlapFrac
	if merged && !a.panelHidden(panelLog) {
		// ONE inset for the whole panel (#25): the chip strip and the list body both
		// start from the inset body, so they share the theme's margin and the gap
		// between them is exactly themedChipGap. Insetting only the body would leave
		// the chips flush against the theme's panel art and silently double the gap.
		panelBody := insetThemedBody(icRect)
		// fitThemedChips owns the strip's geometry (themechips.go): a design rect's
		// width is theme data, and a straight line of chip constants ran past the
		// panel's right edge the moment a theme sized this log narrower than 152 px.
		chips, n := fitThemedChips(panelBody, themedLogViews[:], themedLogViews[:], a.logTab)
		for i := int32(0); i < n; i++ {
			if c.Button(chips[i].r, chips[i].label) {
				a.logTab = chips[i].tab
			}
		}
		inner := themedStripBody(panelBody, n)
		switch a.logTab {
		case logTabOOC:
			a.drawOOCLogList(inner, true)
		case logTabNotes:
			a.drawNotesTab(inner)
		default:
			a.drawICLogList(inner, true)
		}
	} else if okIC && !a.panelHidden(panelLog) {
		a.drawICLogList(insetThemedBody(icRect), true)
	}
	// ooc_toggle swaps the server chat panel for the debug log, exactly as AO2
	// does (on_ooc_toggle_clicked, courtroom.cpp:5197). The debug log draws at
	// ms_chatlog — AO2's kept-for-compatibility key for ui_debug_log (:831) — and
	// a theme declaring only one of the two rects gets only that panel, per
	// set_size_and_pos's hide-when-missing rule.
	dbgRect, okDbg := lay.rect("ms_chatlog")
	showDebug := a.debugOOC && okDbg
	if okOOC && !merged && !showDebug && !a.panelHidden(panelOOC) {
		a.drawOOCLogList(insetThemedBody(oocRect), true)
	}
	if showDebug && !a.panelHidden(panelOOC) {
		a.drawThemedDebugLog(insetThemedBody(dbgRect))
	}
	if tr, ok := lay.rect("ooc_toggle"); ok && !a.panelHidden(panelOOC) {
		// The label names the panel currently ON SCREEN, not what the click will do
		// (AO2 writes "Server" while server chat is up, courtroom.cpp:1014). The
		// button is inert without an ms_chatlog rect: with nowhere to draw the debug
		// log, toggling would just blank the panel.
		if c.Button(tr, a.oocToggleLabel()) && okDbg {
			a.debugOOC = !a.debugOOC
			a.debugOOCScroll = 0
		}
	}
	// OOC inputs draw wherever the theme put them — independent of how
	// the log rects resolved (merged tabs still need a send box).
	if !a.panelHidden(panelOOC) {
		if in, ok := lay.rect("ooc_chat_message"); ok {
			var send bool
			a.oocInput, send = c.TextField("ooc", in, a.oocInput, "OOC chat...")
			if send {
				a.submitOOC()
			}
			a.recallOOC() // Up/Down recall recently-sent OOC lines when the themed field is focused
		}
		if nameR, ok := lay.rect("ooc_chat_name"); ok {
			a.ensureNameOpts()
			dd := int32(0)
			if len(a.nameOpts) > 1 && nameR.W > 60 { // tiny ▾ picker, fitted inside the theme's box
				dd = 20
				nameR.W -= dd + 2
			}
			// TAB-LOCAL (like the classic OOC box): the saved default is the
			// Settings → Identity field only, so a name typed here can't
			// follow you into other tabs.
			onP, onE := a.icFieldFonts(a.oocName) // CJK-safe OOC name (ASCII stays fast path)
			a.oocName, _ = c.TextFieldEmoji("oocname", nameR, a.oocName, "OOC name", onP, onE)
			if name := a.pickNameDropdown("oocpick", sdl.Rect{X: nameR.X + nameR.W + 2, Y: nameR.Y, W: dd, H: nameR.H}); name != "" {
				a.oocName = name
			}
		}
	}

	// music_search is its OWN rect in AO2 (a frameless QLineEdit outside the track
	// list — courtroom.cpp:219-222), and this theme puts it in a dedicated band
	// between the IC log and the list. Drawing it here rather than inside
	// drawMusicList both fills that band and gives the list back the ~27px our own
	// search row was taking out of the theme's budgeted height.
	searchExternal := false
	if r, ok := lay.rect("music_search"); ok && !a.panelHidden(panelLog) {
		searchExternal = true
		a.musicSearch, _ = c.TextField("musicsearch", r, a.musicSearch, "Search")
	}
	// The music plate, and the now-playing name AO2 parents inside it. Drawn before
	// the panel so the panel can drop its own row when this one has taken over.
	plateDrew := false
	if !a.panelHidden(panelLog) {
		plateDrew = a.drawThemedMusicPlate(lay)
	}
	// switch_area_music is AO2's "A/M" button (courtroom.cpp:1060-1061). AO2 places
	// ui_area_list at the SAME music_list rect (:864), so this button is how it
	// swaps the two — a declared rect that painted nothing until now.
	//
	// Whether the theme placed it decides what the chip strip below carries
	// (themedListStrip): with AO2's own switch on screen, a Music chip and an Areas
	// chip beside it are duplicate controls inside the design canvas (#21's parity
	// rule), and dropping them is what finally fits the strip inside a stock
	// 216–224 px music_list. The ROSTER chip stays either way — AsyncAO's roster
	// shares that panel, AO2 has no equivalent at this rect (its player_list is a
	// separate rect and a written deferral), and the Extras box has no roster to
	// move it to, so removing it would delete the only way to reach the roster
	// inside a theme. themedSwitchAreaMusic is what guarantees the way back OUT of
	// the roster, and the strip's own cycle rule the way back IN.
	if r, ok := lay.rect("switch_area_music"); ok && !a.panelHidden(panelLog) {
		if a.drawThemeButton("switch_area_music", "A/M", r) {
			a.logTab = themedSwitchAreaMusic(a.logTab)
		}
	}

	// Music / Areas / Players share the music_list rect (AO2 toggles them; we chip).
	if r, ok := lay.rect("music_list"); ok && !a.panelHidden(panelLog) {
		// One inset for chips AND body, exactly as the log panel above (#25):
		// drawMusicList / drawAreaList / drawPlayerList also place their first widget
		// at r.X/r.Y, so without it a theme showed padded logs beside a flush list.
		panelBody := insetThemedBody(r)
		// The strip carries only what AO2's own buttons cannot reach (themechips.go):
		// the roster alone when switch_area_music drew, all three views when it did
		// not — and in EITHER case fitThemedChips keeps it inside the panel. Three
		// chips at their natural widths want 224 px; this theme's music_list body is
		// 212 and AO2's own stock default is 220, which is the reported overflow.
		chips, n := fitThemedChips(panelBody, themedListStrip(lay), themedListViews[:], a.logTab)
		for i := int32(0); i < n; i++ {
			if c.Button(chips[i].r, chips[i].label) {
				a.logTab = chips[i].tab
			}
		}
		inner := themedStripBody(panelBody, n)
		switch a.logTab {
		case logTabAreas:
			a.drawAreaList(inner, true)
		case logTabPlayers:
			a.drawPlayerList(inner)
		default:
			a.drawMusicList(inner, true, searchExternal, plateDrew)
		}
	}
	// The theme's own volume band. Independent of the music panel above — AO2
	// declares these six rects outside music_list, and each resolves on its own.
	a.drawThemedVolumeSliders(lay)

	// THE IC BAR IS AO2'S, NOT A CRAM OF ASYNCAO CHROME (#21, rules (a)/(b)/(e)).
	//
	// ao2_ic_chat_message is a bar of its OWN in every AO2 theme — aceattorney2x
	// gives it 216,384,512,23, the full width of the stage — and AO2 places the
	// colour picker, the SFX picker and the Pre/Immediate/Flip toggles at their own
	// design rects on the rows below it. AsyncAO used to pack all of them INTO the
	// message rect, so the input the theme sized at 512 px ended up a ~136 px
	// leftover on the right. That is the issue's headline complaint.
	//
	// Each control now resolves its own rect in AO2's precedence with the AsyncAO
	// author-override on top (themedToggleRect): asyncao_* wins when a theme
	// declares it, then the AO2 key, then AO2's second spelling. A theme that
	// declares none of them draws nothing there — it does NOT fall back to
	// AsyncAO's old arrangement, which is rule (e) and is what AO2 does too
	// (set_size_and_pos hides a widget whose rect is missing, courtroom.cpp:1334).
	// Commit 5's AO2 stock-default backstop is what keeps that honest: a theme that
	// omits text_color still gets AO2's own rect for it.
	if in, ok := lay.rect("ao2_ic_chat_message"); ok {
		var send bool
		icPrimary, icEmoji := a.icFieldFonts(a.icInput) // #M5: show typed emoji/unicode, not tofu
		// AO2's own placeholder (courtroom.cpp ui_ic_chat_message).
		a.icInput, send = c.TextFieldEmoji(icFieldID, in, a.icInput, "Message in-character", icPrimary, icEmoji)
		a.recallIC() // #8: Up/Down recall recently-sent lines when the IC field is focused
		a.drawMsgCounter(in, a.d.Prefs.MessageCounterOn())
		if send {
			a.sendIC(0)
		}
	}

	// text_color (AO2 ui_text_color, courtroom.cpp:1138) — swatch + dropdown.
	if r, ok := themedToggleRect(lay, "text_color", "", "asyncao_ic_color"); ok {
		icSel, sw := a.icColorSelected()
		swatch := sdl.Rect{X: r.X, Y: r.Y, W: themedSwatchW, H: r.H}
		c.Fill(swatch, sw)
		c.Border(swatch, ColPanelHi)
		a.icSwatchRect = swatch // the free-hex wheel anchors here (v1.52.0)
		if a.icCustomOn && c.clicked && c.hovering(swatch) {
			a.showICColorWheel = !a.showICColorWheel // re-open to adjust (same as the classic row)
		}
		// Freeze the IC field's selection so a picked colour can wrap it (§3.8,
		// folded into the dropdown to match AO2's on_text_color_changed); shared
		// with the classic row so the two can't drift.
		a.captureICColorSel()
		dd := sdl.Rect{X: r.X + themedSwatchW, Y: r.Y, W: r.W - themedSwatchW, H: r.H}
		if next, changed := c.Dropdown(icColorDDID, dd, icColorChoices, icSel); changed {
			a.applyICColorPick(next)
		}
	}

	// pre (AO2 ui_pre, courtroom.cpp:1065). AUTO-FOLLOWS each emote pick
	// (selectEmote); unchecked forces idle at the send site so the intro is
	// skipped even when the emote defines one.
	if r, ok := themedToggleRect(lay, "pre", "", "asyncao_ic_pre"); ok && !a.panelHidden(slotICPre) {
		a.icPreanim = c.CheckboxIn(r, lay.themedToggleLabel("pre", "Pre"), a.icPreanim)
		c.Tooltip(r, "Pre: play this emote's pre-animation before the line. Follows each emote you pick; uncheck to skip an emote's intro.")
	}

	// immediate (AO2 ui_immediate, courtroom.cpp:1072-1084 — it reads "immediate"
	// first and falls back to "pre_no_interrupt"; aceattorney2x declares only the
	// latter, which is exactly why the alt spelling has to be probed).
	//
	// Gated on cccc_ic_support: the Immediate FIELD only exists on the 2.6 wire, so
	// AO2 hides ui_immediate (with ui_pair_button and ui_ic_chat_name) when the
	// server does not advertise it (courtroom.cpp:747-759). Offering the toggle on
	// a server that drops the field is a control that silently does nothing.
	if r, ok := themedToggleRect(lay, "immediate", "pre_no_interrupt", "asyncao_ic_immediate"); ok && a.sessHasCCCC() {
		a.icImmediate = c.CheckboxIn(r, lay.themedToggleLabel("immediate", "Immediate"), a.icImmediate)
		c.Tooltip(r, "Immediate: the preanim plays without holding back the text")
	}

	// flip (AO2 ui_flip, courtroom.cpp:1629-1636) — shown only when the server
	// advertises FLIPPING, as AO2 hides ui_flip otherwise. ONE bool, TWO views: it
	// mirrors a.pairFlip (the Pair panel's checkbox), so an unclicked CheckboxIn
	// just rewrites the same value and the two cannot drift. The field is left
	// ALONE when the toggle is hidden — a stale flip stops being shown, never
	// silently cleared, because pairFlip is real pair state that parks per tab.
	if r, ok := themedToggleRect(lay, "flip", "", ""); ok &&
		!a.panelHidden(slotICFlip) && a.sess != nil && a.sess.Features.Has(protocol.FeatureFlipping) {
		a.pairFlip = c.CheckboxIn(r, lay.themedToggleLabel("flip", "Flip"), a.pairFlip)
		c.Tooltip(r, "Flip: mirror your character's emotes. Same setting as the Pair panel's flip toggle.")
	}

	// additive (AO2 ui_additive, courtroom.cpp:1089): the next message APPENDS to
	// your last instead of replacing it. AO2 shows it whenever the server
	// advertises additive; AsyncAO also honours its master pref, matching the
	// classic row. Forced off when hidden so a stale check cannot ride a message —
	// the same rule the classic row applies, and the reason the crammed bar used to
	// clear it unconditionally (it had nowhere to draw the toggle at all).
	// The force-off is a plain `else`, matching the classic row. An earlier
	// `else if !ok` was DEAD CODE: applyAO2DefaultRects supplies an `additive` rect
	// for every theme that reaches this path at all (its gate is courtroom+viewport,
	// exactly themeLayoutIn's validity pair), so !ok never fired — and turning the
	// master pref off, or moving to a server without the feature, hid the box while
	// icAdditive stayed true and kept riding every message.
	if r, ok := themedToggleRect(lay, "additive", "", ""); ok &&
		a.sess != nil && a.sess.Features.Has(protocol.FeatureAdditive) && a.d.Prefs.AdditiveTextOn() {
		a.icAdditive = c.CheckboxIn(r, lay.themedToggleLabel("additive", "Additive"), a.icAdditive)
		c.Tooltip(r, "Additive: this message adds to your last one instead of replacing it (2.8 narration-style RP).")
	} else {
		a.icAdditive = false
	}

	// slide_enable (AO2 ui_slide, courtroom.cpp:1096): slide the character into
	// position rather than cutting. The send site clears it afterwards — AO2 says
	// "Slides can't be sticky for nausea reasons" (courtroom.cpp:2362).
	if r, ok := themedToggleRect(lay, "slide_enable", "", ""); ok {
		a.icSlide = c.CheckboxIn(r, lay.themedToggleLabel("slide_enable", "Slide"), a.icSlide)
		c.Tooltip(r, "Slide: the character slides into position for this message instead of cutting.")
	}

	// showname_enable: show custom shownames, or always fall back to character
	// names. AsyncAO stores the INVERSE (ForceCharNames), so the checkbox reads and
	// writes the negation — binding to the AO2 key rather than inventing a second
	// setting is rule (b). The live room is pushed too, so the change shows on the
	// next line without waiting for a rejoin.
	if r, ok := themedToggleRect(lay, "showname_enable", "", ""); ok {
		show := !a.d.Prefs.ForceCharNamesOn()
		if next := c.CheckboxIn(r, lay.themedToggleLabel("showname_enable", "Shownames"), show); next != show {
			a.d.Prefs.SetForceCharNames(!next)
			// Push the LIVE room too, as the Settings surface does. Without this the
			// pref lands but nothing re-reads it until an unrelated applyPrefsToRoom,
			// so the toggle appears to do nothing for the rest of the session.
			if a.room != nil {
				a.room.ForceCharNames = !next
			}
		}
		c.Tooltip(r, "Shownames: show players' custom shownames. Off falls back to character names, which makes impersonation obvious.")
	}

	// guard (AO2 ui_guard, courtroom.cpp:1092): silence modcall ALERTS for this
	// session. Mod-only, as AO2 shows it only once you hold the role.
	if r, ok := themedToggleRect(lay, "guard", "", ""); ok && a.amIMod() {
		a.modcallGuard = c.CheckboxIn(r, lay.themedToggleLabel("guard", "Guard"), a.modcallGuard)
		c.Tooltip(r, "Guard: silence the modcall sound and window flash. The OOC line and the auto-clip still arrive.")
	}

	// realization (AO2 ui_realization, courtroom.cpp:1108) and screenshake
	// (:1113) — ONE-SHOT arms for the next message: the white flash and the camera
	// shake. Both are toggles rather than fire-buttons because AO2 arms them and
	// sends on the NEXT message; the send site clears them (courtroom.cpp:2328).
	// Bordered while armed, which is how the evidence chip already signals the same
	// "rides your next line" state.
	// Drawn through drawThemeButton so the theme's own realization.png /
	// screenshake.png is used where it ships one (AO2 setImage, courtroom.cpp:1109
	// and :1114) — these are 42x42 ICON rects, and a text chip in one is the
	// AsyncAO-inside-an-AO2-rect the parity rule exists to stop. drawThemeButton
	// falls back to a text chip when the art is missing, which is AOButton's own
	// behaviour, so a theme without the files still gets a usable control.
	if r, ok := themedToggleRect(lay, "realization", "", ""); ok {
		if a.drawThemeButton("realization", "Flash", r) {
			a.icRealize = !a.icRealize
		}
		if a.icRealize {
			c.Border(r, ColAccent)
		}
		c.Tooltip(r, "Realization: a white flash + sound on your next message. Clears after it sends.")
	}
	if r, ok := themedToggleRect(lay, "screenshake", "", ""); ok {
		if a.drawThemeButton("screenshake", "Shake", r) {
			a.icShake = !a.icShake
		}
		if a.icShake {
			c.Border(r, ColAccent)
		}
		c.Tooltip(r, "Screenshake: shake the courtroom on your next message. Clears after it sends.")
	}

	// sfx_dropdown (AO2 ui_sfx_dropdown, courtroom.cpp:952): a sound for your NEXT
	// message, overriding the emote's own until set back to "auto". Picking one
	// previews it.
	if r, ok := themedToggleRect(lay, "sfx_dropdown", "", "asyncao_ic_sfx"); ok {
		a.ensureSFXChoices()
		a.drawThemedSFXPicker(r)
	}

	// Text FX and the emoji picker have NO AO2 design key, so under rule (c) their
	// home is the Extras menu — they are not crammed into an AO2 rect any more. A
	// theme author who WANTS them inside the canvas still can, via the asyncao_*
	// override rects, which is the whole point of that tier (rule (d)).
	if r, ok := lay.rect("asyncao_ic_fx"); ok {
		a.fxButton(r)
	}
	if r, ok := lay.rect("asyncao_ic_emoji"); ok && !a.panelHidden(slotICEmoji) {
		if a.drawEmojiBarButton(r) {
			a.showEmojiPicker = !a.showEmojiPicker
		}
	}
	if nameR, ok := lay.rect("ao2_ic_chat_name"); ok {
		// Session override box (matches the classic layout): blank falls
		// back to — and shows — the persisted Settings showname.
		namePlaceholder := a.d.Prefs.SavedShowname()
		if namePlaceholder == "" {
			namePlaceholder = "Showname"
		}
		a.ensureNameOpts()
		dd := int32(0)
		if len(a.nameOpts) > 1 && nameR.W > 60 { // tiny ▾ saved-name picker, fitted inside the theme's box
			dd = 20
			nameR.W -= dd + 2
		}
		// CJK showname renders as real glyphs while typed (same route as the classic
		// layout's box and the IC input) — plain ASCII keeps the single-font fast path.
		snPrimary, snEmoji := a.icFieldFonts(a.shownameOverride)
		if a.shownameOverride == "" {
			snPrimary, snEmoji = a.icFieldFonts(namePlaceholder) // route a CJK placeholder too
		}
		a.shownameOverride, _ = c.TextFieldEmoji("icshownameov", nameR, a.shownameOverride, namePlaceholder, snPrimary, snEmoji)
		if name := a.pickNameDropdown("snpick", sdl.Rect{X: nameR.X + nameR.W + 2, Y: nameR.Y, W: dd, H: nameR.H}); name != "" {
			a.shownameOverride = name
		}
	}

	// Shouts at their design rects, themed art when shipped.
	if !a.panelHidden(panelShouts) {
		shouts := []struct {
			key, label string
			mod        int
		}{
			{"hold_it", "Hold It!", protocol.ShoutHoldIt},
			{"objection", "Objection!", protocol.ShoutObjection},
			{"take_that", "Take That!", protocol.ShoutTakeThat},
		}
		for _, s := range shouts {
			if r, ok := lay.rect(s.key); ok {
				if a.drawThemeButton(s.key, s.label, r) {
					a.sendIC(s.mod)
				}
			}
		}
		if a.sess.Features.Has(protocol.FeatureCustomObjections) && a.hasCustomShout() {
			if r, ok := lay.rect("custom_objection"); ok {
				if a.drawThemeButton("custom_objection", a.customShoutLabel(), r) {
					a.sendIC(protocol.ShoutCustom)
				}
				if len(a.customShouts) > 0 {
					cyc := sdl.Rect{X: r.X + r.W + 2, Y: r.Y, W: 18, H: r.H}
					if c.Button(cyc, "▾") {
						a.customIdx++
						if a.customIdx >= len(a.customShouts) {
							a.customIdx = -1
						}
					}
				}
			}
		}
	}

	// Judge strip (grant- or pos-gated exactly like classic).
	if a.judgeVisible() {
		judge := []struct {
			key, label string
			run        func()
		}{
			{"witness_testimony", "WT", func() { a.sess.SendWTCE("testimony1", 0) }},
			{"cross_examination", "CE", func() { a.sess.SendWTCE("testimony2", 0) }},
			{"not_guilty", "NG", func() { a.sess.SendWTCE("judgeruling", 0) }},
			{"guilty", "G", func() { a.sess.SendWTCE("judgeruling", 1) }},
			{"defense_minus", "D-", func() { a.sess.SendHP(1, a.sess.HPDef-1) }},
			{"defense_plus", "D+", func() { a.sess.SendHP(1, a.sess.HPDef+1) }},
			{"prosecution_minus", "P-", func() { a.sess.SendHP(2, a.sess.HPPro-1) }},
			{"prosecution_plus", "P+", func() { a.sess.SendHP(2, a.sess.HPPro+1) }},
		}
		for _, j := range judge {
			if r, ok := lay.rect(j.key); ok {
				if a.drawThemeButton(j.key, j.label, r) {
					j.run()
				}
			}
		}
	}

	// Pos / pair / modcall / evidence at their design homes.
	if r, ok := lay.rect("pos_dropdown"); ok {
		// A real dropdown at the theme's pos_dropdown rect (AO2 parity).
		choices := a.posChoices()
		cur := 0
		for i, p := range choices {
			if p == a.mySide() {
				cur = i
				break
			}
		}
		if next, changed := c.Dropdown("posdd", r, choices, cur); changed {
			a.applySide(choices[next]) // also /pos the server so the move is instant, not next-message
		}
		// pos_remove reverts to the character's OWN declared side, and is offered
		// only while the current side differs from it (AO2-Client courtroom.cpp:5285-5292).
		// While a char.ini is still in flight defaultSide() reads "wit", which is
		// get_char_side's own fallback — so this stays hidden rather than offering to
		// revert to a side we have not read yet.
		if rr, ok := lay.rect("pos_remove"); ok && posRemoveVisible(a.mySide(), a.defaultSide()) {
			if a.drawThemeButton("pos_remove", "X", rr) {
				a.applySide(a.defaultSide())
			}
		}
	}
	// emote_dropdown: the same emote list as the button grid, as a dropdown at the
	// theme's own rect (AO2-Client courtroom.cpp:925-926; emotes.cpp:314-317
	// `on_emote_dropdown_changed(int p_index) { select_emote(p_index); }`).
	//
	// AO2 wires `activated`, not `currentIndexChanged` (emotes.cpp:30) — it acts on
	// a USER pick and not on a programmatic reselect, which is exactly what
	// Ctx.Dropdown's `changed` already reports. selectEmote IS AO2's select_emote,
	// down to the Pre checkbox following the pick, so this is a binding and not a
	// reimplementation.
	if r, ok := lay.rect("emote_dropdown"); ok {
		a.ensureEmoteChoices()
		if next, changed := c.Dropdown("emotedd", r, a.emoteChoices, a.emoteIdx); changed {
			a.selectEmote(next)
		}
	}
	// iniswap_dropdown + iniswap_remove (AO2-Client courtroom.cpp:937-939, :945-950).
	// Index 0 is always the player's own folder, so "remove" is offered only while
	// something else is worn — AO2 resets the box to 0 on remove (:5436-5452).
	if r, ok := lay.rect("iniswap_dropdown"); ok {
		a.ensureIniswapChoices()
		cur := iniswapCurrentIndex(a.iniswapChoices, a.activeCharName())
		if next, changed := c.Dropdown("inidd", r, a.iniswapChoices, cur); changed {
			if next == 0 {
				a.setIniswap("") // back to the player's own character
			} else if next < len(a.iniswapChoices) {
				a.setIniswap(a.iniswapChoices[next])
			}
			// AO2 refocuses IC on EVERY iniswap change, not only on remove
			// (courtroom.cpp:5361-5363, the first statement of the handler).
			c.FocusField("ic")
		}
		if rr, ok := lay.rect("iniswap_remove"); ok && cur != 0 && cur < len(a.iniswapChoices) {
			if a.drawThemeButton("iniswap_remove", "X", rr) {
				// Drop it from the saved wardrobe AND stop wearing it. RemoveWardrobe
				// is a no-op for a folder that was only ever worn (a server
				// iniswap.txt entry never added to the wardrobe), which is exactly
				// the case the trailing choices entry exists for.
				a.d.Prefs.RemoveWardrobe(a.serverKey, a.iniswapChoices[cur])
				a.setIniswap("")
				c.FocusField("ic")
			}
		}
	}
	if r, ok := lay.rect("pair_button"); ok {
		if a.drawThemeButton("pair_button", "Pair", r) {
			a.showPair = !a.showPair
		}
	}
	if r, ok := lay.rect("call_mod"); ok {
		if a.drawThemeButton("call_mod", "Call Mod", r) {
			a.showModcall = true
		}
	}
	// The three utility buttons AO2 puts beside Call Mod. Labels match AO2's own
	// setText so a theme with no art still reads the same (courtroom.cpp:1036-1056),
	// and each action is the one the equivalent hotkey already runs — these are
	// bindings, not second implementations.
	if r, ok := lay.rect("change_character"); ok {
		if a.drawThemeButton("change_character", "Change character", r) {
			a.screen = ScreenCharSelect // hotkeyCharMenu (qol.go)
		}
	}
	if r, ok := lay.rect("reload_theme"); ok {
		if a.drawThemeButton("reload_theme", "Reload theme", r) {
			// applyThemeAsync alone: pollThemeApply refreshes the rects and
			// invalidates the canvases when the apply lands, so re-deriving
			// anything here would race its own result.
			a.applyThemeAsync()
		}
	}
	if r, ok := lay.rect("settings"); ok {
		if a.drawThemeButton("settings", "Settings", r) {
			a.prevScreen = ScreenCourtroom // hotkeySettings, so Back returns to the room
			a.screen = ScreenSettings
		}
	}
	evLabel := "Evidence"
	if a.evidPresent {
		evLabel = "Evidence ●"
	}
	if r, ok := lay.rect("evidence_button"); ok {
		if a.drawThemeButton("evidence_button", evLabel, r) {
			a.showEvid = true
		}
	}

	// Emote grid at the theme's metrics, with page arrows.
	if r, ok := lay.rect("emotes"); ok && !a.panelHidden(panelEmotes) {
		a.drawEmoteGridThemed(r, lay, vp)
	}

	// NOTHING of AsyncAO's own draws inside the design canvas (#21, rule (c)).
	// A row of ★ Extras / Hotkeys / Restyle / Mod / CM chips used to be pinned
	// here at the raw window's bottom-left, which under a theme like
	// aceattorney2x painted AND clicked straight through
	// music_list = 0,323,216,277. No design key existed for a theme author to
	// move it, so relocating was the only answer: those commands now live in the
	// menu bar, above the canvas, reachable from every screen.

	// Compact hover toolbox (#27/A1): slim bottom-right grip → Theater / Edit /
	// Hide-UI icon chips on hover or pin. Normal play only — skipped while the
	// themed editor is armed. The pinned per-piece panel is NOT drawn here (it
	// needs real input this fenced pass can't give it — see the classic path in
	// screens.go); it draws post-courtroom in app.go alongside drawFloatingPanels.
	// Unconditional: the `!a.layoutEdit` gate now lives in the latch, so the fence
	// published at the top of this pass and the draw share one decision.
	a.drawCompactToolboxInPass()

	if a.warnActive() {
		c.LabelClipped(pad, h-44, w-2*pad, a.warnLine, ColDanger)
	}

	// Layout editor overlay: last, above everything it edits.
	//
	// The server-tab strip paints HERE while an editor is armed — the twin of the
	// classic path's site in screens.go, and the reason App.Frame's over-everything
	// paint stands down. Drawn before the overlay so the editor's banner and its Done /
	// Reset all / Snap chips sit on top of the chips instead of under them.
	//
	// It replays the frame's paint-site latch (chrometop.go) rather than the layoutEdit
	// flag, so the two sites are exact complements of ONE decision taken at the top of
	// the pass: a mid-pass arm (the command palette draws after this site) or disarm
	// (Done / Esc, below) can no longer make both sites paint or neither.
	a.drawTabBarInPass(w, h)
	if a.layoutEdit {
		a.drawLayoutEditor(w, h, lay)
	}
}

// drawThemedChatBox is the design-rect chatbox: the skin stretches to the
// ao2_chatbox rect, showname/message sit at their chatbox-relative design
// rects (AO2 child-widget semantics).
func (a *App) drawThemedChatBox(box sdl.Rect, lay *themeLayoutCache) {
	c := a.ctx
	sc := &a.room.Scene
	// Blankpost hides the whole chatbox (frame + showname + text) so only the
	// sprite shows; the second clause is the unchanged idle/no-message case.
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	skinPage, skinned := a.themePage(themeStemChatbox)
	// ONE resolution, shared verbatim with the export's themed chatbox
	// (drawGifThemedChatbox) so video and comic frames can never disagree with what
	// the player was looking at. It also lands the Qt insets AO2 gets for free:
	// +4 on every side of the message, nothing on the showname (chatboxfit.go).
	//
	// Resolved BEFORE the skin blit because of the ladder below: which chatbox art
	// gets painted depends on how wide the showname measures, so the name's box and
	// its face have to exist first. AO2 has the same dependency and solves it the
	// same way round — set_size_and_pos, then the font, then the measure, then
	// setImage (courtroom.cpp:3301-3373).
	nameBox, msgBox := chatboxTextRects(box, lay, box.Y+chatBoxTopStrip)

	nameCol := ColAccent
	if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	if a.d.Prefs.NameColorsOn() { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.NameColorSat())/100, float64(a.d.Prefs.NameColorVal())/100)
	}
	// themedChatFace, not elemFontFor: inside a theme's own chatbox the point size
	// folds with the canvas scale, because AO2 multiplies design rects and
	// courtroom_fonts.ini sizes by the one themeScalingFactor and the ratio between
	// them is what the theme author actually drew (chatboxfit.go). At the shipped
	// Native default on a window that fits the canvas the factor is exactly 1 and
	// this resolves to what elemFontFor returned.
	snFont, snEmoji := a.themedChatFace(elemShowname, DefaultScalePct, lay, sc.ShownameText)
	// AO2's widen-and-swap ladder: a showname wider than the box the theme drew
	// widens by showname_extra_width and swaps the chatbox art for the wider plate
	// the theme author drew for exactly that case, twice over if the theme shipped
	// a `big` as well. Both variants are pinned theme art, so this is map reads and
	// integer arithmetic — see chatboxSkinLadder for the residency contract.
	nameBox, skinPage = a.chatboxSkinLadder(nameBox, lay, skinPage, snFont, snEmoji, sc.ShownameText, nameCol)

	if skinned {
		// &box into cgo would heap-allocate the PARAMETER on every themed frame that
		// has a message up — and because escape analysis is static, on the unskinned
		// frames too. Shared scratch instead (ui.go cgoRect contract: SDL copies the
		// rect during the call and never retains it); nothing runs between the
		// assignment and the Copy but a map read and a slice index.
		c.cgoRect = box
		// A4: angle 0 keeps the original Copy path; a persisted nonzero angle tilts
		// the SKIN art via CopyEx. Only the skin rotates — the showname/message text
		// drawn below stays axis-aligned (opt-in cosmetic mismatch, accepted for the
		// cheap tier, same as any rotated themed chrome).
		//
		// The DESTINATION is the ao2_chatbox rect whichever rung is showing: AO2
		// stretches the swapped art into the widget it already sized
		// (AOImage::setImage scales the pixmap to size() with IgnoreAspectRatio,
		// aoimage.cpp:29-31), and every variant in the reference corpus is drawn at
		// the base skin's own dimensions anyway — the wider name plate is painted
		// INTO the same canvas, it does not make the canvas wider.
		if ang := lay.angle(themeChatboxKey); ang == 0 {
			_ = c.Ren.Copy(a.themeFrame(skinPage), nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(a.themeFrame(skinPage), nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
	}
	if !skinned {
		bg := sdl.Color{R: 16, G: 16, B: 24, A: 215}
		if col, ok := a.partPanel(partChatbox); ok { // per-part tint (v1.52.0): custom base, opacity kept
			bg = sdl.Color{R: col.R, G: col.G, B: col.B, A: bg.A}
		}
		if a.d.Prefs.ChatboxTintOn() && sc.ShownameText != "" { // #14 per-character tint
			bg = chatboxTintFor(sc.ShownameText, bg)
		}
		c.Fill(box, bg)
		c.Border(box, ColAccent)
	}
	// Creator easter egg — same helper the classic overlay uses, so a themed
	// layout (which reaches this path only when the theme defines an ao2_chatbox
	// rect) lights the exact same glow. Placed after the whole skin-resolution
	// block (skin OR flat panel), NOT inside the !skinned branch: full AO2 themes
	// usually ship a chatbox.png and take the skinned branch, so an egg gated on
	// !skinned would never draw for them.
	a.drawChatEgg(box, sc.MessageText)

	msgX, msgY := msgBox.X, msgBox.Y
	wrapW := msgBox.W

	// Clipped: a long showname must never spill past the theme's box (widened by the
	// ladder above, if this theme shipped one). elemFontFor (not the fixed chrome
	// font) so a non-Latin name resolves to a covering face; emoji-aware so a
	// colour-emoji name renders the glyphs, not tofu. #39: the theme's own showname
	// family / point size / bold, matching the classic overlay.
	//
	// showname_align, honoured at last: AO2 places the label's text across its own
	// widget rect (courtroom.cpp:3338-3354), and half the reference corpus's design
	// files ask for `center` — which AsyncAO drew hard left. Resolved ONCE for both
	// draw passes so the faux-bold shadow cannot land on a different offset than
	// the glyphs it thickens.
	//
	// The Y is AO2's, unchanged: ui_vp_showname is given a horizontal-only
	// alignment (courtroom.cpp:93, and again in the block above), which CLEARS
	// QLabel's default AlignVCenter, so AO2 draws the name at the TOP of the
	// showname rect however tall the theme made it. Verified on a Qt 6.5.3 build:
	// the same label in a 232x120 box inks at y=54 with Qt's default alignment and
	// at y=2 with AlignLeft alone — the value AO2 actually sets.
	nameX, nameW := a.shownameSpanFor(nameBox, snFont, snEmoji, sc.ShownameText, nameCol)
	// Only the THEME's showname_bold adds the faux-bold pass here. The classic
	// overlay also honours the client's "Bold names" pref; the themed box never
	// has, and quietly turning that on for every themed user is a look change
	// nobody asked for — kept out of #39's scope deliberately.
	if a.elemBold(elemShowname) {
		a.labelEmoji(snFont, snEmoji, nameX+chatOverlayBoldNudge, nameBox.Y, nameW, sc.ShownameText, nameCol)
	}
	a.labelEmoji(snFont, snEmoji, nameX, nameBox.Y, nameW, sc.ShownameText, nameCol)

	// The message folds the canvas scale in for the same reason the showname does:
	// its wrap width came from a design rect that the canvas already scaled, so a
	// font that did not scale with it changed how much text fits a theme-authored
	// box — overrunning the frame on one side of 1:1 and rattling around inside it
	// on the other.
	a.ensureChatRaster(wrapW, skinned, a.themedChatPct(elemMessage, a.chatPct, lay))
	// Text selection works on the themed chatbox too (drag a range / dbl-click
	// a word / triple-click all; Ctrl+C / right-click copies) — same handler
	// as the classic overlay, fed this layout's message rect.
	//
	// The HEIGHT deliberately runs to the bottom of the chatbox rather than using
	// msgBox.H: AO2 does not confine this text to the message rect either — the
	// widget is re-parented to the Courtroom and only offset by the chatbox origin
	// (AO2-Client courtroom.cpp:3310), which is why eight design files in the
	// reference corpus ship a message rect that overhangs its own chatbox (CCSmol's
	// right edge is 259 in a 256-wide box; Mobile's 393 in 392; HDF-Fullscreen
	// 1080p's 770 in 765). Tightening either the selection rect or the clip below
	// to msgBox would start cutting those themes' last column and last line.
	textRect := sdl.Rect{X: msgX, Y: msgY, W: wrapW, H: box.Y + box.H - msgY}
	a.handleChatSelect(textRect, sc)
	if a.msAnim != nil || a.msRaster != nil {
		// Shared scratch, same as pushClip (ui.go): SDL_RenderSetClipRect takes a
		// `const SDL_Rect *` (SDL_render.h:930) and the renderer keeps its OWN copy —
		// SDL_RenderGetClipRect (:946) fills a caller struct from it rather than
		// handing the pointer back — so the highlight/raster draws below are free to
		// overwrite cgoRect while this clip is in force.
		c.cgoRect = box
		_ = c.Ren.SetClipRect(&c.cgoRect)
		if a.chatSelActive { // selection highlight, UNDER the text so it reads through
			a.drawChatSelHighlight(msgX, msgY, wrapW, sc)
		}
		if a.msAnim != nil { // #M5 animated message
			reduce := a.d.Prefs.ReduceMotion()
			if a.msAnim.Animates(reduce) {
				a.NoteAnimating() // clock-driven text FX must not freeze at idle=0 — same census as the classic chatbox (screens.go)
			}
			a.msAnim.Draw(c.Ren, a.glyphCache, a.msAnimFont, a.d.Viewport.AnimClock(), sc.VisibleRunes, msgX, msgY, reduce)
		} else {
			a.msRaster.DrawScaled(c.Ren, sc.VisibleRunes, msgX, msgY, c.RenderScalePct(), &box) // device-exact crawl + the clip it must re-assert — see the classic chatbox (screens.go)
		}
		_ = c.Ren.SetClipRect(nil)
	}
	a.chatZoomWheel(box)
}

// minEmoteCellPx rejects degenerate emote_button_size data (a 3 px "button"
// is sloppy theme data, not a widget) — below it the grid falls back to AO2's
// stock emoteBtnCell square.
const minEmoteCellPx int32 = 8

// minEmoteGapPx floors emote_button_spacing, and it is ZERO on purpose.
//
// AO2 honours a spacing of 0 — get_button_spacing (AO2-Client
// text_file_functions.cpp:193-216) applies no positivity test — and a dozen
// shipped themes declare "0, 0" to butt their emote buttons together, so the
// floor must not be 1. It cannot go below zero either: AO2's grid arithmetic
// divides by (spacing + button size) with NO guard, both here
// (emotes.cpp:70-71) and on the char-select grid (charselect.cpp:160-161) —
// only evidence.cpp:211 wraps the same expression in qMax(1, ...). A theme
// declaring a spacing that cancels the button size divides by zero in AO2; we
// clamp instead. Together with the minEmoteCellPx fallback below, this makes
// (cell + gap) ≥ minEmoteCellPx > 0, so the column/row divisions cannot trap.
const minEmoteGapPx int32 = 0

// emoteCellScale is the ONE factor the themed emote cell and its gap scale by.
//
// AO2 reads emote_button_size / emote_button_spacing through
// AOApplication::get_button_spacing (text_file_functions.cpp:212-213), which
// multiplies BOTH axes by the single Options::themeScalingFactor(); the button
// art is then stretched into that cell with Qt::IgnoreAspectRatio
// (aobutton.cpp AOButton::updateIcon). So in AO2 the cell ALWAYS keeps the
// aspect ratio the theme designed, whatever the window shape.
//
// Our themeLayoutCache scale is per-axis and, in Stretch fit-mode only,
// scaleX != scaleY (see the field comment on the struct). Scaling the cell
// per-axis therefore squashed every emote button flat on a wide window (#33).
// min() is the uniform stand-in rather than max(): a cell grown by the looser
// axis would overflow the emotes rect on the tighter one, silently dropping a
// whole row or column of buttons off the theme's grid. Native/Letterbox/Crop/
// Custom already have scaleX == scaleY, so this is a no-op there — only Stretch
// moves.
func emoteCellScale(scaleX, scaleY float64) float64 { return math.Min(scaleX, scaleY) }

// emoteGridMetrics is one themed emote page's geometry: the uniformly scaled
// button cell, its gap, and how many whole cells tile the theme's rect.
type emoteGridMetrics struct {
	cellW, cellH int32 // emote_button_size × emoteCellScale (designed aspect kept)
	gapX, gapY   int32 // emote_button_spacing, same uniform factor
	cols, rows   int32 // whole cells that fit in the rect, each ≥ 1
}

// emoteGridLayout is the pure geometry behind drawEmoteGridThemed: available
// rect + designed cell/gap + the uniform scale → cell size and grid extent.
// Kept free of App/SDL state so the layout is table-testable headlessly.
func emoteGridLayout(r sdl.Rect, cellPx, gapPx [2]int, scale float64) emoteGridMetrics {
	m := emoteGridMetrics{
		cellW: int32(float64(cellPx[0]) * scale),
		cellH: int32(float64(cellPx[1]) * scale),
		gapX:  int32(float64(gapPx[0]) * scale),
		gapY:  int32(float64(gapPx[1]) * scale),
	}
	if m.cellW < minEmoteCellPx || m.cellH < minEmoteCellPx {
		m.cellW, m.cellH = emoteBtnCell, emoteBtnCell // degenerate metrics: AO2 stock 40×40
	}
	if m.gapX < minEmoteGapPx {
		m.gapX = minEmoteGapPx
	}
	if m.gapY < minEmoteGapPx {
		m.gapY = minEmoteGapPx
	}
	m.cols = (r.W + m.gapX) / (m.cellW + m.gapX)
	m.rows = (r.H + m.gapY) / (m.cellH + m.gapY)
	if m.cols < 1 {
		m.cols = 1
	}
	if m.rows < 1 {
		m.rows = 1
	}
	return m
}

// perPage is how many emote buttons one page of this grid holds.
func (m emoteGridMetrics) perPage() int { return int(m.cols) * int(m.rows) }

// cellRect is the pixel rect of the n-th cell of a page (0-based, row-major)
// laid out in r. Draw AND hit-test go through this one function, so a click
// can never land on a rect the grid didn't draw.
func (m emoteGridMetrics) cellRect(r sdl.Rect, n int32) sdl.Rect {
	return sdl.Rect{
		X: r.X + (n%m.cols)*(m.cellW+m.gapX),
		Y: r.Y + (n/m.cols)*(m.cellH+m.gapY),
		W: m.cellW, H: m.cellH,
	}
}

// drawEmoteGridThemed lays the emote buttons out on the theme's grid
// (emote_button_size/spacing scaled), paging with emote_left/right.
func (a *App) drawEmoteGridThemed(r sdl.Rect, lay *themeLayoutCache, vp sdl.Rect) {
	c := a.ctx
	if a.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	a.refreshEmoteView() // favourite set + visible-index list (#77)
	vis := a.emoteVisible
	// The rect r stays per-axis scaled (it is the theme's container, and the
	// grid reflows/pages into whatever shape it takes), but the CELL scales
	// uniformly so the button art keeps its designed aspect — see
	// emoteCellScale for why (#33).
	grid := emoteGridLayout(r, a.themeEmoteCell, a.themeEmoteGap, emoteCellScale(lay.scaleX, lay.scaleY))
	perPage := grid.perPage()
	pages := (len(vis) + perPage - 1) / perPage
	if pages < 1 {
		pages = 1 // favs-only with nothing starred yet: one empty page
	}
	if a.emotePage >= pages {
		a.emotePage = 0
	}
	if pages > 1 { // mouse-wheel pages emotes (parity with the classic row)
		if d := c.WheelIn(r); d > 0 && a.emotePage > 0 {
			a.emotePage--
		} else if d < 0 && a.emotePage < pages-1 {
			a.emotePage++
		}
	}
	start := a.emotePage * perPage

	me := a.activeCharName()
	useImages := a.d.Prefs.EmoteButtonImagesEnabled()
	// Emote-name hover tooltip config, read ONCE (RLock off the per-cell loop).
	hoverNamesOn := a.d.Prefs.EmoteHoverNamesEnabled()
	hoverNamesDelay := a.d.Prefs.EmoteHoverNamesDelay()
	for slot := start; slot < len(vis) && slot < start+perPage; slot++ {
		i := vis[slot] // real index into a.emotes (favs-only filters which show)
		e := &a.emotes[i]
		btn := grid.cellRect(r, int32(slot-start))
		selected := i == a.emoteIdx
		if selected {
			c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColAccent)
		}
		label := e.Comment
		if label == "" {
			label = e.Anim
		}
		var picked bool
		if useImages {
			picked = a.drawEmoteImageButton(btn, me, i, selected, label)
		} else {
			picked = c.Button(btn, label)
		}
		// The favourite ★ (drawn on top) wins the click over selecting the emote.
		if a.drawEmoteFavStar(btn, i) {
			// star toggled — swallow this cell's select for the frame
		} else if picked {
			a.emoteIdx = i
			a.icPreanim = emoteHasPreanim(e) // "Pre" auto-follows the pick (AO2-Client ui_pre)
			a.speculateEmote(me, e)
			c.FocusField("ic") // AO2 focus_ic_input: pick emote, keep typing
		}
		// Same hover-preview behavior as the classic row: the preanim (scrubbed)
		// when the emote has one, else the talking sprite — warmed on demand.
		if c.HoverPreview("emote:"+e.Anim, btn) {
			a.previewEmote(me, e)
		}
		// Emote-name tooltip (parity with the classic row): hover dwell shows the
		// name. Gated on hovering(btn) so the id concat runs only for the hovered cell.
		if hoverNamesOn && c.hovering(btn) {
			c.TooltipAfterDelay("emotename:"+e.Anim, btn, label, hoverNamesDelay)
		}
	}
	// Favs-only with nothing starred yet: tell the user how to recover (the
	// toggle lives in Settings for themed layouts, which are pixel-precise).
	if len(vis) == 0 && a.d.Prefs.EmoteFavOnlyOn() {
		c.Label(r.X, r.Y+4, "No favourite emotes — turn off \"favourites only\" in Settings, then click an emote's ★.", ColTextDim)
	}

	if pages > 1 {
		if lr, ok := lay.rect("emote_left"); ok {
			if a.drawThemeButton("emote_left", "<", lr) && a.emotePage > 0 {
				a.emotePage--
			}
		}
		if rr, ok := lay.rect("emote_right"); ok {
			if a.drawThemeButton("emote_right", ">", rr) && a.emotePage < pages-1 {
				a.emotePage++
			}
		}
	}
	if a.previewBase != "" {
		// Clamp to the WINDOW, not the stage — the box is draggable anywhere
		// (playtest: the preview could not be moved out of the viewport).
		a.drawSpritePreview(a.winW, a.winH, false, a.previewedEmoteName()) // ARM 2: caption the emote name
		if c.clicked {
			a.previewBase = ""
		}
	}
}

// themedExtrasHint fires a one-time-per-session toast when a themed (AO2) layout
// is in use, pointing players at where AsyncAO's own features live — a legacy AO2
// theme carries no element key for any of them, so they would otherwise be lost.
//
// It names the Extras MENU now, not a button: the bottom-left chip row this used
// to point at was deleted because it painted over the theme's own canvas (#21,
// rule (c)). The hotkey still opens the box directly, so it stays in the line.
func (a *App) themedExtrasHint() {
	if a.themedHintShown {
		return
	}
	a.themedHintShown = true
	a.warnLine = clampLine("AO2 theme — Wardrobe, Jukebox & all AsyncAO extras are in the Extras menu (or press [" +
		a.hotkeyFor(hotkeyExtras) + "])")
	a.warnAt = time.Now()
}

// drawWidgetsPanel moved to floatbox.go as the non-blocking drawFloatingExtras.

// The theme's own volume sliders (#21 labels 12/13).
//
// AO2 declares music_slider / sfx_slider / blip_slider with a matching
// music_label / sfx_label / blip_label beside each, and drives them from
// on_music_slider_moved and friends (courtroom.cpp:1163-1175). AsyncAO ingested
// all six rects and painted none, so a 216px band of the theme's canvas was blank.
//
// OWNER DECISION: inside a theme's canvas these ARE the volume surface, and the
// music panel's own Volume toggle is not offered (drawMusicList's themed flag).
// Master volume and blip rate have no AO2 rect and stay in Settings.
const (
	// Stable widget ids. Ctx.Slider keys its drag state by id, and the classic
	// strips build theirs by concatenation ("volstrip:"+id) — doing that here
	// would allocate three strings per slider per frame on the settled render
	// path, which the whole-screen zero-alloc gate would catch.
	themedMusicSliderID = "themed:music_slider"
	themedSFXSliderID   = "themed:sfx_slider"
	themedBlipSliderID  = "themed:blip_slider"

	// themedVolumeMax is the slider range. AO2's sliders are 0..100
	// (courtroom.cpp:1165 setMaximum) and so are AsyncAO's volume prefs.
	themedVolumeMax = 100
)

// drawThemedVolumeSliders paints the three declared sliders and their labels.
// Each rect resolves INDEPENDENTLY — rule (e) means a theme that declares only
// some of them gets only those, never a synthesised row.
func (a *App) drawThemedVolumeSliders(lay *themeLayoutCache) {
	c := a.ctx
	master, music, sfx, blip := a.effectiveVolumes()
	changed := false

	// Labels are CLIPPED, not plain. AO2 runs truncate_label_text on all three
	// (courtroom.cpp:1173-1175), and the geometry is why: in the theme under test
	// music_label is 41px wide and its slider starts 3px later, so an unclipped
	// label bleeds straight across the track.
	for _, l := range [...]struct{ key, text string }{
		{"music_label", "Music"},
		{"sfx_label", "Sfx"},
		{"blip_label", "Blip"},
	} {
		if r, ok := lay.rect(l.key); ok {
			c.LabelClipped(r.X, r.Y, r.W, l.text, ColText)
		}
	}

	// Channels map one-to-one. AO2's SFX handler also drives the ambience streams
	// and the objection player, but AsyncAO has no separate ambience stream
	// (applyAudioVolumes mixes music/sfx/blip/alert only), so there is nothing to
	// mirror — do not "fix" this by routing sfx into music.
	if r, ok := lay.rect("music_slider"); ok {
		if v := int(c.Slider(themedMusicSliderID, r, int32(music), themedVolumeMax)); v != music {
			music, changed = v, true
		}
	}
	if r, ok := lay.rect("sfx_slider"); ok {
		if v := int(c.Slider(themedSFXSliderID, r, int32(sfx), themedVolumeMax)); v != sfx {
			sfx, changed = v, true
		}
	}
	if r, ok := lay.rect("blip_slider"); ok {
		if v := int(c.Slider(themedBlipSliderID, r, int32(blip), themedVolumeMax)); v != blip {
			blip, changed = v, true
		}
	}
	if changed {
		a.setEffectiveVolumes(master, music, sfx, blip)
		a.applyAudioVolumes()
	}
}

// The theme's music plate (#21 label 2).
//
// AO2 builds ui_music_display as an InterfaceAnimationLayer at the music_display
// rect and parents ui_music_name — a ScrollText — INSIDE it
// (courtroom.cpp:166-173, :885). That parenting is why music_name's declared
// coordinates are relative to the plate and not to the courtroom, which the
// registry records as relMusicDisplay.
//
// Returns whether the plate drew, so the music panel can skip its own
// "Now playing" row and give those pixels back to the track list.
func (a *App) drawThemedMusicPlate(lay *themeLayoutCache) bool {
	c := a.ctx
	md, ok := lay.rect("music_display")
	if !ok {
		return false // rule (e): no rect, no plate — and no rebase target for music_name
	}
	page, resident := a.themePage("music_display")
	if !resident {
		return false // art still streaming, or the theme ships none
	}
	// &md into cgo would heap-allocate the parameter every frame — the same escape
	// class drawThemeButton documents.
	c.cgoRect = md
	_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)

	// music_name is a CHILD rect, so it is offset by the plate's origin. Resolved
	// only inside this branch: without the plate there is nothing to be relative to.
	mn, ok := lay.rect("music_name")
	if !ok {
		return true // the plate alone is a legitimate theme choice
	}
	// #21 label 16: "music_name_color" is this line's ink, and it is the case the
	// colour key most obviously exists for — the theme drew the plate underneath, so
	// it knows exactly what the text sits on. sampleThemeBackdrops measures THIS
	// art, not the courtroom background, for the same reason. The empty state keeps
	// ColTextDim: "Nothing playing" is AsyncAO's own placeholder (AO2 leaves the
	// label blank), so the theme's colour has no claim on it.
	label, col := "Nothing playing", ColTextDim
	if now := a.nowPlayingName(); now != "" {
		label, col = now, a.elemInkOr(elemMusicName, true, ColAccent)
	}
	font := a.elemLabelFont(elemMusicName, DefaultScalePct)
	dst := sdl.Rect{X: md.X + mn.X, Y: md.Y + mn.Y, W: mn.W, H: mn.H}
	// Clipped, not scrolled: AO2's ScrollText marquees an overlong track name, and
	// that is a deliberate deviation — a marquee is per-frame motion on a surface
	// that is otherwise static, and ReduceMotion users would need an opt-out for it.
	// Clipping keeps the plate honest at rest; the full name stays in the list.
	if a.elemBold(elemMusicName) {
		c.LabelClippedFont(font, dst.X+1, dst.Y, dst.W, label, col)
	}
	c.LabelClippedFont(font, dst.X, dst.Y, dst.W, label, col)
	return true
}

// nowPlayingName is the short display name of the current track, or "" when
// nothing is playing — the same name the list rows and the IC "has played a song"
// line use, so all three agree.
func (a *App) nowPlayingName() string {
	if a.room == nil {
		return ""
	}
	now := a.room.Scene.MusicTrack
	if now == "" {
		return ""
	}
	return courtroom.MusicDisplayName(now)
}
