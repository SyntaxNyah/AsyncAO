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
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
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
	// ao2MusicNameMarginPx is AO2's ScrollText left/right inset for the
	// now-playing name. Its source is `leftMargin = height() / 3`
	// (../AO2-Client/src/scrolltext.cpp:10), computed in the CONSTRUCTOR while the
	// widget still has Qt's default 100x30 child geometry and never recomputed
	// (resizeEvent, :104, does not touch it) — so it is a hard 10 px, not a
	// function of the theme's declared music_name height. It is applied at every
	// draw (:75 scrolling, :100 static) and reserved on BOTH sides by the fit test
	// (:47), which is why a name that fits AO2's plate could still clip in ours.
	ao2MusicNameMarginPx = 10
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
	textPct int
	// shownameExtra is `showname_extra_width` in WINDOW pixels — the theme's
	// design-space value already multiplied by the canvas scale, baked here for the
	// same two reasons textPct is: the draw path stays integer-only, and the float
	// rounding happens once per window/fit/theme change instead of once per frame.
	shownameExtra int32
	offX, offY    int32
	// topOff is the app-chrome band reserved ABOVE the canvas (#14; topChromeH).
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
	// The flip that proved it: a press-move-release whose net design delta rounds to
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
	ang map[string]uint8
	// toggleLabel holds each AO2 toggle's label already shortened to its own design
	// rect (truncateLabelTo, ui.go — AO2's truncate_label_text). Baked here rather
	// than computed at draw time because a theme legitimately declares rects too
	// narrow for their labels — aceattorney2x gives `flip` 51x19, leaving 29 px —
	// and truncation allocates, which the themed frame's zero-alloc gate forbids.
	toggleLabel map[string]string
	// --- free elements (v1.90.0 W3; design §2, docs/THEME-FORMAT.md §4) ---------
	//
	// el is the baked, z-resolved, anchor-resolved element array — see bakedElement
	// (themeelements.go). A FIXED ARRAY, not a slice: the draw loop reads only
	// values, never a map, never a slice header, never a sort, never a string op.
	// It sits INLINE (~10 KiB) rather than behind a pointer because a pointer would
	// be an allocation per rebuild, a nil check per band and a second lifetime to
	// reason about, and because "a fixed array that never grows" is the property the
	el  [theme.ElementCap]bakedElement
	elN int
	// band[b] is the half-open index range into el for one theme.ElementBand,
	// sorted ONCE at bake (insertion sort over <= 96 entries, cold path). Elements
	// within a band paint in ascending z, ties broken by declaration order.
	band [theme.ElementBandCount]elemBandRange
	// condVal holds the DISTINCT visible_when values this theme names, interned at
	// bake; bakedElement.cond is an index into it. Bounded by ElementCap because at
	// most one new value can arrive per element. Interning is what makes the
	// per-element visibility gate an integer compare (elementVisible) instead of a
	// string compare inside the band loops.
	condVal [theme.ElementCap]string
	condN   int
	// condGen stamps THIS intern table. App memoises the live value of each
	// condition axis (App.condSrc / condLive) and the memo must not survive a
	// rebuild that re-interned the values under it — the indices would point at the
	// wrong strings. Monotonic on App (themeElemGen), so a cache zeroed by a
	// rebuild can never collide with the memo's stamp.
	condGen uint64
}

// themeLayout builds the cache for a canvas laid out inside the FULL (w, h) box,
// origin-anchored — no client chrome reserved. That is what an offscreen export
// frame wants (gifexport.go: a video frame is not the window and must never carry
// the client's own furniture), and it is the plain geometry the fit-mode tests pin.
func (a *App) themeLayout(w, h int32) *themeLayoutCache {
	return a.themeLayoutIn(w, h, 0)
}

// themeWindowLayout is the WINDOW's layout: the design canvas is fitted into the
// height LEFT under the app-chrome band and offset down by it (#14).
func (a *App) themeWindowLayout(w, h int32) *themeLayoutCache {
	return a.themeLayoutIn(w, h, a.topChromeH())
}

// invalidateThemeCanvases drops BOTH scaled-geometry caches — the courtroom's
// (themeLayoutCache) and character select's (charSelectLayoutCache).
// ONE function instead of an assignment repeated at every mutation site, because the
// two caches have DIFFERENT keys and the difference is invisible where the mutation
// happens. themeLayoutCache keys on (w, h, band, fit, strip width, strip parked);
func (a *App) invalidateThemeCanvases() {
	a.skin.themeLay.valid = false
	a.charSelLay.valid = false
}

// themeLayoutIn returns the cache for a (w, h) box with `top` px reserved at its top
// edge, rebuilding on window resize, band change, fit change or theme swap
// (pollThemeApply invalidates).
func (a *App) themeLayoutIn(w, h, top int32) *themeLayoutCache {
	lay := &a.skin.themeLay
	fit := a.d.Prefs.Load().ThemeFit
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
	court, okCourt := a.skin.themeRects["courtroom"]
	_, okVP := a.skin.themeRects["viewport"]
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
	if a.skin.themeShownameExtra > 0 {
		lay.shownameExtra = int32(float64(a.skin.themeShownameExtra) * f.scaleX)
		if lay.shownameExtra < 1 {
			lay.shownameExtra = 1
		}
	}
	lay.offX, lay.offY = f.area.X, f.area.Y
	lay.area = f.area
	// WIDGET BOUNDS = the canvas, minus whatever part of it sits above the reserved
	// chrome band. offY is only >= top while the canvas FITS the room under the band:
	bounds := lay.area
	if bounds.Y < top {
		bounds.H -= top - bounds.Y
		bounds.Y = top
	}
	lay.r = make(map[string]sdl.Rect, len(a.skin.themeRects))
	for key, r := range a.skin.themeRects {
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
	if _, present := a.skin.themeRects[themeTabBarKey]; present {
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
	if themeName, _ := a.d.Prefs.Load().Theme(); themeName != "" {
		lay.ang = a.d.Prefs.Load().ThemeRectRotationSnapshot(themeName)
	}
	a.bakeToggleLabels(lay)
	// The free-element bake (v1.90.0 W3), LAST in the rebuild: it reads the finished
	// lay.r (an anchored element resolves against the widget's resolved box) and it
	// REWRITES lay.r for every pane host before it resolves anything (design §6.6 R8
	// — decoration follows its widget wherever it lives). Nothing below it may read
	// a slot rect it has not seen. See themeelementbake.go.
	a.bakeThemeElements(lay)
	lay.valid = true
	return lay
}

// sessHasCCCC reports whether the server advertises cccc_ic_support, the 2.6
// feature that carries pairing, shownames and the immediate flag. AO2 shows or
// hides ui_pair_button / ui_immediate / ui_ic_chat_name together on it
// (courtroom.cpp:747-759); a toggle for a field the wire drops is a control that
// silently does nothing.
func (a *App) sessHasCCCC() bool {
	return a.link.sess != nil && a.link.sess.Features.Has(protocol.FeatureCCCCIC)
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
		s := math.Min(1, math.Min(sx, sy))
		f.scaleX, f.scaleY = s, s
	case config.ThemeFitStretch:
		f.scaleX, f.scaleY = sx, sy
	case config.ThemeFitCrop:
		s := math.Max(sx, sy)
		f.scaleX, f.scaleY = s, s
	case config.ThemeFitCustom:
		s := math.Min(sx, sy) * float64(a.d.Prefs.Load().ThemeZoom()) / 100 // manual zoom over the fit
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
		px, py := a.d.Prefs.Load().ThemePan()
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
	if stem == "lobbybackground" && a.d.Prefs.Load().PlainLobby {
		c.Fill(dst, ColBackground)
		return
	}
	if page, ok := a.themePage(stem); ok {
		// &dst into cgo would heap-allocate dst every frame — even on the
		// plain-fill paths above/below (escape analysis is static).
		c.cgoRect = dst
		// A4: structured angle==0 → Copy / else CopyEx. The lobby/charselect
		// backdrops aren't editor-hoverable keys, so this resolves 0 (plain Copy)
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
	if next, changed := c.Dropdown("sfxdd", rect, a.emotepick.sfxChoices, a.emotepick.sfxChoiceIdx); changed {
		a.applySFXPick(next) // pick + audition + focus bounce (icsfx.go; courtroom.cpp:5498-5502)
	}
	a.sfxRowRightClickPreview()
	c.TooltipAfter("sfxdd-tip", rect, "Sound for your NEXT message — 'auto' uses the emote's own sound, or pick one to override. Right-click a row to hear it without picking it. Extras → SFX Browser for favourites & any sound by name.")
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
	if tr, ok := lay.rect("asyncao_toolbox"); ok {
		a.toolbox.toolboxThemeRect, a.toolbox.toolboxThemeRectOn = tr, true
	} else {
		a.toolbox.toolboxThemeRectOn = false
	}
	// Latch this frame's toolbox decision and occlude whatever the strip covers
	// (#26 — the reported symptom is a theme whose "Call Mod" button sits under it).
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
	// visible_when, resolved ONCE for the whole pass: each condition axis's live
	// value becomes an interned index here, so all three band loops below gate on an
	// integer compare. Ahead of band 1 because every band reads the same answer, and
	// a mid-pass change (a message settling between bands) would make one band's
	// decoration disagree with another's inside a single frame.
	a.refreshElementConditions(lay)

	// BAND 1 of 3 (design §2; docs/THEME-FORMAT.md §4): above the courtroom
	// backdrop, below every widget. This is where a theme's wallpaper, vignette or
	// desk plate goes — anything that must sit UNDER the stage the client draws next.
	a.drawElementBands(lay, theme.BandBackdrop)

	vp, _ := lay.rect("viewport")
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.renderViewportZoomed(vp)
	inChat := false
	if box, ok := lay.rect("ao2_chatbox"); ok {
		inChat = c.hovering(box)
	}
	a.handleViewportZoom(vp, inChat)
	if a.cam.vpZoom <= 1 {
		a.handleSpriteDrag(vp)
		a.handleSpriteHide(vp) // right-click → hide-sprite confirm (default ON)
	}
	a.handleHotkeys()
	if a.srvMeta.rehearsal {
		c.Label(vp.X+8, vp.Y+8, rehearsalBadge, ColTierYellow)
	}

	// Chatbox at its design rect; classic overlay when the theme has none.
	if box, ok := lay.rect("ao2_chatbox"); ok {
		a.drawThemedChatBox(box, lay)
	} else {
		a.drawChatOverlay(vp, false, 0, 0) // themed layout owns its chatbox geometry
	}
	a.drawOverlayChatLayer(vp) // effects.ini layer=chat: over the chatbox (courtroom.cpp:3234-3237)
	a.drawCourtOverlays(vp, lay)
	a.drawReactionFloats(vp) // #2: emoji reactions rising over the stage (0-alloc when none)

	// BAND 2 of 3: between the stage and the panels — over the viewport and the
	// chatbox, BEHIND the chat logs and every panel widget the second half of this
	// pass paints.
	a.drawElementBands(lay, theme.BandMid)

	// Modal popups: same shared list as the classic path, so the two can't
	// drift (the bg picker once drew in classic but was missing here).
	if !a.modalReturnSkippedWhileEditing() && a.drawCourtroomModals(w, h) {
		return
	}

	// Logs: IC at ic_chatlog, OOC at server_chatlog. Themes that stack the two on
	// the same rect meant them as one toggled widget (AO2's ooc_toggle) — detect
	// the overlap and draw them tabbed instead of on top of each other.
	icRect, okIC := lay.rect("ic_chatlog")
	oocRect, okOOC := lay.rect("server_chatlog")
	merged := okIC && okOOC && rectOverlapFrac(icRect, oocRect) >= logMergeOverlapFrac
	if merged && !a.classic.panelHidden(panelLog) {
		// ONE inset for the whole panel (#25): the chip strip and the list body both
		// start from the inset body, so they share the theme's margin and the gap
		// between them is exactly themedChipGap. Insetting only the body would leave
		// the chips flush against the theme's panel art and silently double the gap.
		panelBody := insetThemedBody(icRect)
		// fitThemedChips owns the strip's geometry (themechips.go): a design rect's
		// width is theme data, and a straight line of chip constants ran past the
		// panel's right edge the moment a theme sized this log narrower than 152 px.
		chips, n := fitThemedChips(panelBody, themedLogViews[:], themedLogViews[:], a.chatlog.logTab)
		for i := int32(0); i < n; i++ {
			if c.Button(chips[i].r, chips[i].label) {
				a.chatlog.logTab = chips[i].tab
			}
		}
		inner := themedStripBody(panelBody, n)
		switch a.chatlog.logTab {
		case logTabOOC:
			a.drawOOCLogList(inner, true)
		case logTabNotes:
			a.drawNotesTab(inner)
		default:
			a.drawICLogList(inner, true)
		}
	} else if okIC && !a.classic.panelHidden(panelLog) {
		a.drawICLogList(insetThemedBody(icRect), true)
	}
	// ooc_toggle swaps the server chat panel for the debug log, exactly as AO2
	// does (on_ooc_toggle_clicked, courtroom.cpp:5197). The debug log draws at
	// ms_chatlog — AO2's kept-for-compatibility key for ui_debug_log (:831) — and
	// a theme declaring only one of the two rects gets only that panel, per
	// set_size_and_pos's hide-when-missing rule.
	dbgRect, okDbg := lay.rect("ms_chatlog")
	showDebug := a.chatlog.debugOOC && okDbg
	if okOOC && !merged && !showDebug && !a.classic.panelHidden(panelOOC) {
		a.drawOOCLogList(insetThemedBody(oocRect), true)
	}
	if showDebug && !a.classic.panelHidden(panelOOC) {
		a.drawThemedDebugLog(insetThemedBody(dbgRect))
	}
	if tr, ok := lay.rect("ooc_toggle"); ok && !a.classic.panelHidden(panelOOC) {
		// The label names the panel currently ON SCREEN, not what the click will do
		// (AO2 writes "Server" while server chat is up, courtroom.cpp:1014). The
		// button is inert without an ms_chatlog rect: with nowhere to draw the debug
		// log, toggling would just blank the panel.
		if c.Button(tr, a.oocToggleLabel()) && okDbg {
			a.chatlog.debugOOC = !a.chatlog.debugOOC
			a.chatlog.debugOOCScroll = 0
		}
	}
	// OOC inputs draw wherever the theme put them — independent of how
	// the log rects resolved (merged tabs still need a send box).
	if !a.classic.panelHidden(panelOOC) {
		if in, ok := lay.rect("ooc_chat_message"); ok {
			var send bool
			a.compose.oocInput, send = c.TextField("ooc", in, a.compose.oocInput, "OOC chat...")
			if send {
				a.submitOOC()
			}
			a.recallOOC() // Up/Down recall recently-sent OOC lines when the themed field is focused
		}
		if nameR, ok := lay.rect("ooc_chat_name"); ok {
			a.ensureNameOpts()
			dd := int32(0)
			if len(a.budget.nameOpts) > 1 && nameR.W > 60 { // tiny ▾ picker, fitted inside the theme's box
				dd = 20
				nameR.W -= dd + 2
			}
			// TAB-LOCAL (like the classic OOC box): the saved default is the
			// Settings → Identity field only, so a name typed here can't
			// follow you into other tabs.
			onP, onE := a.icFieldFonts(a.compose.oocName) // CJK-safe OOC name (ASCII stays fast path)
			a.compose.oocName, _ = c.TextFieldEmoji("oocname", nameR, a.compose.oocName, "OOC name", onP, onE)
			if name := a.pickNameDropdown("oocpick", sdl.Rect{X: nameR.X + nameR.W + 2, Y: nameR.Y, W: dd, H: nameR.H}); name != "" {
				a.compose.oocName = name
			}
		}
	}

	// music_search is its OWN rect in AO2 (a frameless QLineEdit outside the track
	// list — courtroom.cpp:219-222), and this theme puts it in a dedicated band
	// between the IC log and the list. Drawing it here rather than inside
	// drawMusicList both fills that band and gives the list back the ~27px our own
	// search row was taking out of the theme's budgeted height.
	searchExternal := false
	if r, ok := lay.rect("music_search"); ok && !a.classic.panelHidden(panelLog) {
		searchExternal = true
		a.musicTab.musicSearch, _ = c.TextField("musicsearch", r, a.musicTab.musicSearch, "Search")
	}
	// The music plate, and the now-playing name AO2 parents inside it. Drawn before
	// the panel so the panel can drop its own row when this one has taken over — and
	// the answer that matters is whether the NAME drew, not the plate art: a plate
	// with no usable music_name paints nothing readable, so the panel keeps its row.
	nameDrew := false
	if !a.classic.panelHidden(panelLog) {
		nameDrew = a.drawThemedMusicPlate(lay)
	}
	// switch_area_music is AO2's "A/M" button (courtroom.cpp:1060-1061). AO2 places
	// ui_area_list at the SAME music_list rect (:864), so this button is how it
	// swaps the two — a declared rect that painted nothing until now.
	// chip beside it are duplicate controls inside the design canvas (#21's parity
	if r, ok := lay.rect("switch_area_music"); ok && !a.classic.panelHidden(panelLog) {
		if a.drawThemeButton("switch_area_music", "A/M", r) {
			a.chatlog.logTab = themedSwitchAreaMusic(a.chatlog.logTab)
		}
	}

	// THE ROSTER'S RULING (#21 parity law, applied). The player list is NOT an
	// AsyncAO extra squatting an AO2 rect: AO2 declares a design key for it,
	// `player_list` (courtroom.cpp:879), separate from the music_list rect that
	// ui_music_list (:867) and ui_area_list (:864) share. So the rule is the
	// canonical one, in two halves:
	rosterOwnRect := false
	if r, ok := lay.rect("player_list"); ok && !a.classic.panelHidden(panelLog) {
		a.drawPlayerList(insetThemedBody(r))
		rosterOwnRect = true
	}

	// Music / Areas (and the roster too, unless it just drew at its own rect) share
	// the music_list rect — AO2 toggles them; we chip.
	if r, ok := lay.rect("music_list"); ok && !a.classic.panelHidden(panelLog) {
		// One inset for chips AND body, exactly as the log panel above (#25):
		// drawMusicList / drawAreaList / drawPlayerList also place their first widget
		// at r.X/r.Y, so without it a theme showed padded logs beside a flush list.
		panelBody := insetThemedBody(r)
		// The strip carries only what AO2's own buttons cannot reach (themechips.go):
		chips, n := fitThemedChips(panelBody, themedListStrip(lay), themedListViews[:], a.chatlog.logTab)
		for i := int32(0); i < n; i++ {
			if c.Button(chips[i].r, chips[i].label) {
				a.chatlog.logTab = chips[i].tab
			}
		}
		inner := themedStripBody(panelBody, n)
		switch {
		case a.chatlog.logTab == logTabAreas:
			a.drawAreaList(inner, true)
		case a.chatlog.logTab == logTabPlayers && !rosterOwnRect:
			a.drawPlayerList(inner)
		default:
			// rosterOwnRect lands here on purpose: with the roster already painted at
			// its own rect, this panel shows the music list rather than the SAME roster
			// twice (and a second drawPlayerList would fight the first over the shared
			// scroll/drag ids).
			a.drawMusicList(inner, true, searchExternal, nameDrew)
		}
	}
	// The theme's own volume band. Independent of the music panel above — AO2
	// declares these six rects outside music_list, and each resolves on its own.
	a.drawThemedVolumeSliders(lay)

	// THE IC BAR IS AO2'S, NOT A CRAM OF ASYNCAO CHROME (#21, rules (a)/(b)/(e)).
	if in, ok := lay.rect("ao2_ic_chat_message"); ok {
		var send bool
		icPrimary, icEmoji := a.icFieldFonts(a.compose.icInput) // #M5: show typed emoji/unicode, not tofu
		// AO2's own placeholder (courtroom.cpp ui_ic_chat_message).
		// Same counter/chip reservation the classic bar makes (screens.go): the band
		// is declared before the field so typed text clips short of it.
		counterOn := a.d.Prefs.Load().MessageCounter
		c.ReserveFieldTail(icFieldID, a.icFieldTailReserve(counterOn))
		a.compose.icInput, send = c.TextFieldEmoji(icFieldID, in, a.compose.icInput, "Message in-character", icPrimary, icEmoji)
		a.recallIC() // #8: Up/Down recall recently-sent lines when the IC field is focused
		a.drawMsgCounter(in, counterOn)
		// Same notice fence as the classic bar's send site (screens.go), and the twin is
		// the whole point: "behavior is shared with the classic path" is this function's
		// stated contract, so a guard that lands on only one of the two pairs is a
		// themed-only hole — a user on an AO2 theme could Enter a line into a room the
		// server has already removed them from. fencePointer blanks the mouse and leaves
		// the keyboard alone, so the box's pointer fence cannot cover this.
		if send && !a.font.serverNoticeDlg.open {
			a.sendIC()
		}
	}

	// text_color (AO2 ui_text_color, courtroom.cpp:1138) — swatch + dropdown.
	if r, ok := themedToggleRect(lay, "text_color", "", "asyncao_ic_color"); ok {
		icSel, sw := a.icColorSelected()
		swatch := sdl.Rect{X: r.X, Y: r.Y, W: themedSwatchW, H: r.H}
		c.Fill(swatch, sw)
		c.Border(swatch, ColPanelHi)
		a.lookUI.icSwatchRect = swatch // the free-hex wheel anchors here (v1.52.0)
		if a.compose.icCustomOn && c.clicked && c.hovering(swatch) {
			a.lookUI.showICColorWheel = !a.lookUI.showICColorWheel // re-open to adjust (same as the classic row)
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
	if r, ok := themedToggleRect(lay, "pre", "", "asyncao_ic_pre"); ok && !a.classic.panelHidden(slotICPre) {
		// bounceOnToggle: ui_pre → focus_ic_input (courtroom.cpp:502), the same wiring
		// the classic row carries — both IC bars or neither.
		a.compose.icPreanim = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("pre", "Pre"), a.compose.icPreanim), a.compose.icPreanim)
		c.Tooltip(r, "Pre: play this emote's pre-animation before the line. Follows each emote you pick; uncheck to skip an emote's intro.")
	}

	// immediate (AO2 ui_immediate, courtroom.cpp:1072-1084 — it reads "immediate"
	// first and falls back to "pre_no_interrupt"; aceattorney2x declares only the
	// latter, which is exactly why the alt spelling has to be probed).
	// server does not advertise it (courtroom.cpp:747-759). Offering the toggle on
	// a server that drops the field is a control that silently does nothing.
	if r, ok := themedToggleRect(lay, "immediate", "pre_no_interrupt", "asyncao_ic_immediate"); ok && a.sessHasCCCC() {
		a.compose.icImmediate = c.CheckboxIn(r, lay.themedToggleLabel("immediate", "Immediate"), a.compose.icImmediate)
		c.Tooltip(r, "Immediate: the preanim plays without holding back the text")
	}

	// flip (AO2 ui_flip, courtroom.cpp:1629-1636) — shown only when the server
	// advertises FLIPPING, as AO2 hides ui_flip otherwise. ONE bool, TWO views: it
	// mirrors a.pairFlip (the Pair panel's checkbox), so an unclicked CheckboxIn
	// just rewrites the same value and the two cannot drift. The field is left
	// ALONE when the toggle is hidden — a stale flip stops being shown, never
	// silently cleared, because pairFlip is real pair state that parks per tab.
	if r, ok := themedToggleRect(lay, "flip", "", ""); ok &&
		!a.classic.panelHidden(slotICFlip) && a.link.sess != nil && a.link.sess.Features.Has(protocol.FeatureFlipping) {
		a.pairbox.pairFlip = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("flip", "Flip"), a.pairbox.pairFlip), a.pairbox.pairFlip) // ui_flip → focus_ic_input (courtroom.cpp:503)
		c.Tooltip(r, "Flip: mirror your character's emotes. Same setting as the Pair panel's flip toggle.")
	}

	// additive (AO2 ui_additive, courtroom.cpp:1089): the next message APPENDS to
	// your last instead of replacing it. AO2 shows it whenever the server
	// advertises additive; AsyncAO also honours its master pref, matching the
	// classic row. Forced off when hidden so a stale check cannot ride a message —
	// the same rule the classic row applies, and the reason the crammed bar used to
	// clear it unconditionally (it had nowhere to draw the toggle at all).
	if r, ok := themedToggleRect(lay, "additive", "", ""); ok &&
		a.link.sess != nil && a.link.sess.Features.Has(protocol.FeatureAdditive) && a.d.Prefs.Load().AdditiveText {
		a.compose.icAdditive = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("additive", "Additive"), a.compose.icAdditive), a.compose.icAdditive) // :504 → :6572
		c.Tooltip(r, "Additive: this message adds to your last one instead of replacing it (2.8 narration-style RP).")
	} else {
		a.compose.icAdditive = false
	}

	// slide_enable (AO2 ui_slide, courtroom.cpp:1096): slide the character into
	// position rather than cutting. The send site clears it afterwards — AO2 says
	// "Slides can't be sticky for nausea reasons" (courtroom.cpp:2362).
	if r, ok := themedToggleRect(lay, "slide_enable", "", ""); ok {
		a.compose.icSlide = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("slide_enable", "Slide"), a.compose.icSlide), a.compose.icSlide) // ui_slide_enable → focus_ic_input (courtroom.cpp:506)
		c.Tooltip(r, "Slide: the character slides into position for this message instead of cutting.")
	}

	// showname_enable: show custom shownames, or always fall back to character
	// names. AsyncAO stores the INVERSE (ForceCharNames), so the checkbox reads and
	// writes the negation — binding to the AO2 key rather than inventing a second
	// setting is rule (b). The live room is pushed too, so the change shows on the
	// next line without waiting for a rejoin.
	if r, ok := themedToggleRect(lay, "showname_enable", "", ""); ok {
		show := !a.d.Prefs.Load().ForceCharNames
		if next := c.CheckboxIn(r, lay.themedToggleLabel("showname_enable", "Shownames"), show); next != show {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					ForceCharNames =

					!next

			})

			// Push the LIVE room too, as the Settings surface does. Without this the
			// pref lands but nothing re-reads it until an unrelated applyPrefsToRoom,
			// so the toggle appears to do nothing for the rest of the session.
			if a.link.room != nil {
				a.link.room.ForceCharNames = !next
			}
		}
		c.Tooltip(r, "Shownames: show players' custom shownames. Off falls back to character names, which makes impersonation obvious.")
	}

	// guard (AO2 ui_guard, courtroom.cpp:1092): silence modcall ALERTS for this
	// session. Mod-only, as AO2 shows it only once you hold the role.
	if r, ok := themedToggleRect(lay, "guard", "", ""); ok && a.amIMod() {
		a.compose.modcallGuard = c.CheckboxIn(r, lay.themedToggleLabel("guard", "Guard"), a.compose.modcallGuard)
		c.Tooltip(r, "Guard: silence the modcall sound and window flash. The OOC line and the auto-clip still arrive.")
	}

	// realization (AO2 ui_realization, courtroom.cpp:1108) and screenshake
	// (:1113) — ONE-SHOT arms for the next message: the white flash and the camera
	// shake. Both are toggles rather than fire-buttons because AO2 arms them and
	// sends on the NEXT message; the send site clears them (courtroom.cpp:2328).
	if r, ok := themedToggleRect(lay, "realization", "", ""); ok {
		if a.drawThemeButton("realization", "Flash", r) {
			a.toggleRealization() // arms the WIRE FLAG *and* the effects row — icarms.go
		}
		a.drawArmedOverlay(r, a.compose.icRealize)
		c.Tooltip(r, "Realization: a white flash + sound on your next message. Clears after it sends.")
	}
	if r, ok := themedToggleRect(lay, "screenshake", "", ""); ok {
		if a.drawThemeButton("screenshake", "Shake", r) {
			a.toggleScreenshake()
		}
		a.drawArmedOverlay(r, a.compose.icShake)
		c.Tooltip(r, "Screenshake: shake the courtroom on your next message. Clears after it sends.")
	}

	// sfx_dropdown (AO2 ui_sfx_dropdown, courtroom.cpp:952): a sound for your NEXT
	// message, overriding the emote's own until set back to "auto". Picking one
	// previews it.
	if r, ok := themedToggleRect(lay, "sfx_dropdown", "", "asyncao_ic_sfx"); ok {
		a.ensureSFXChoices()
		a.drawThemedSFXPicker(r)
	}

	// effects_dropdown (AO2 ui_effects_dropdown, courtroom.cpp:968): the screen
	// effect that rides your NEXT message, with the theme's own per-effect icons in
	// the open rows. Hidden while spectating and when the roster is empty, exactly
	// as AO2 hides it (:5586-5598).
	if r, ok := themedToggleRect(lay, "effects_dropdown", "", "asyncao_ic_effects"); ok {
		a.drawEffectsPicker(r)
	}

	// Text FX and the emoji picker have NO AO2 design key, so under rule (c) their
	// home is the Extras menu — they are not crammed into an AO2 rect any more. A
	// theme author who WANTS them inside the canvas still can, via the asyncao_*
	// override rects, which is the whole point of that tier (rule (d)).
	if r, ok := lay.rect("asyncao_ic_fx"); ok {
		a.fxButton(r)
	}
	if r, ok := lay.rect("asyncao_ic_emoji"); ok && !a.classic.panelHidden(slotICEmoji) {
		if a.drawEmojiBarButton(r) {
			a.emoji.showEmojiPicker = !a.emoji.showEmojiPicker
		}
	}
	if nameR, ok := lay.rect("ao2_ic_chat_name"); ok {
		// Session override box (matches the classic layout): blank falls
		// back to — and shows — the persisted Settings showname.
		namePlaceholder := a.d.Prefs.Load().Showname
		if namePlaceholder == "" {
			namePlaceholder = "Showname"
		}
		a.ensureNameOpts()
		dd := int32(0)
		if len(a.budget.nameOpts) > 1 && nameR.W > 60 { // tiny ▾ saved-name picker, fitted inside the theme's box
			dd = 20
			nameR.W -= dd + 2
		}
		// CJK showname renders as real glyphs while typed (same route as the classic
		// layout's box and the IC input) — plain ASCII keeps the single-font fast path.
		snPrimary, snEmoji := a.icFieldFonts(a.compose.shownameOverride)
		if a.compose.shownameOverride == "" {
			snPrimary, snEmoji = a.icFieldFonts(namePlaceholder) // route a CJK placeholder too
		}
		a.compose.shownameOverride, _ = c.TextFieldEmoji("icshownameov", nameR, a.compose.shownameOverride, namePlaceholder, snPrimary, snEmoji)
		if name := a.pickNameDropdown("snpick", sdl.Rect{X: nameR.X + nameR.W + 2, Y: nameR.Y, W: dd, H: nameR.H}); name != "" {
			a.compose.shownameOverride = name
		}
	}

	// Shouts at their design rects, themed art when shipped.
	if !a.classic.panelHidden(panelShouts) {
		shouts := []struct {
			key, label string
			mod        int
		}{
			{"hold_it", "Hold It!", protocol.ShoutHoldIt},
			{"objection", "Objection!", protocol.ShoutObjection},
			{"take_that", "Take That!", protocol.ShoutTakeThat},
		}
		// ARM, never send: the button sets objection_state and the interjection
		// rides the next message (courtroom.cpp:6048-6127, built at :2136-2147).
		for _, s := range shouts {
			if r, ok := lay.rect(s.key); ok {
				if a.drawThemeButton(s.key, s.label, r) {
					a.toggleShout(s.mod)
				}
				a.drawArmedOverlay(r, a.compose.icShout == s.mod)
			}
		}
		if a.link.sess.Features.Has(protocol.FeatureCustomObjections) && a.hasCustomShout() {
			if r, ok := lay.rect("custom_objection"); ok {
				if a.drawThemeButton("custom_objection", a.customShoutLabel(), r) {
					a.toggleShout(protocol.ShoutCustom)
				}
				a.drawArmedOverlay(r, a.compose.icShout == protocol.ShoutCustom)
				if len(a.compose.customShouts) > 0 {
					cyc := sdl.Rect{X: r.X + r.W + 2, Y: r.Y, W: 18, H: r.H}
					if c.Button(cyc, "▾") {
						a.compose.customIdx++
						if a.compose.customIdx >= len(a.compose.customShouts) {
							a.compose.customIdx = -1
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
			{"witness_testimony", "WT", func() { a.link.sess.SendWTCE("testimony1", 0) }},
			{"cross_examination", "CE", func() { a.link.sess.SendWTCE("testimony2", 0) }},
			{"not_guilty", "NG", func() { a.link.sess.SendWTCE("judgeruling", 0) }},
			{"guilty", "G", func() { a.link.sess.SendWTCE("judgeruling", 1) }},
			{"defense_minus", "D-", func() { a.link.sess.SendHP(1, a.link.sess.HPDef-1) }},
			{"defense_plus", "D+", func() { a.link.sess.SendHP(1, a.link.sess.HPDef+1) }},
			{"prosecution_minus", "P-", func() { a.link.sess.SendHP(2, a.link.sess.HPPro-1) }},
			{"prosecution_plus", "P+", func() { a.link.sess.SendHP(2, a.link.sess.HPPro+1) }},
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
		if rr, ok := lay.rect("pos_remove"); ok && posRemoveVisible(a.mySide(), a.defaultSide()) {
			if a.drawThemeButton("pos_remove", "X", rr) {
				a.applySide(a.defaultSide())
			}
		}
	}
	// emote_dropdown: the same emote list as the button grid, as a dropdown at the
	// theme's own rect (AO2-Client courtroom.cpp:925-926; emotes.cpp:314-317
	// `on_emote_dropdown_changed(int p_index) { select_emote(p_index); }`).
	// AO2 wires `activated`, not `currentIndexChanged` (emotes.cpp:30) — it acts on
	// a USER pick and not on a programmatic reselect, which is exactly what
	// Ctx.Dropdown's `changed` already reports. selectEmote IS AO2's select_emote,
	if r, ok := lay.rect("emote_dropdown"); ok {
		a.ensureEmoteChoices()
		if next, changed := c.Dropdown("emotedd", r, a.emotepick.emoteChoices, a.emotepick.emoteIdx); changed {
			a.selectEmote(next)
		}
	}
	// iniswap_dropdown + iniswap_remove (AO2-Client courtroom.cpp:937-939, :945-950).
	// Index 0 is always the player's own folder, so "remove" is offered only while
	// something else is worn — AO2 resets the box to 0 on remove (:5436-5452).
	if r, ok := lay.rect("iniswap_dropdown"); ok {
		a.ensureIniswapChoices()
		cur := iniswapCurrentIndex(a.emotepick.iniswapChoices, a.activeCharName())
		if next, changed := c.Dropdown("inidd", r, a.emotepick.iniswapChoices, cur); changed {
			if next == 0 {
				a.setIniswap("") // back to the player's own character
			} else if next < len(a.emotepick.iniswapChoices) {
				a.setIniswap(a.emotepick.iniswapChoices[next])
			}
			// AO2 refocuses IC on EVERY iniswap change, not only on remove
			// (courtroom.cpp:5361-5363, the first statement of the handler).
			c.FocusField("ic")
		}
		if rr, ok := lay.rect("iniswap_remove"); ok && cur != 0 && cur < len(a.emotepick.iniswapChoices) {
			if a.drawThemeButton("iniswap_remove", "X", rr) {
				// Drop it from the saved wardrobe AND stop wearing it. RemoveWardrobe
				// is a no-op for a folder that was only ever worn (a server
				// iniswap.txt entry never added to the wardrobe), which is exactly
				// the case the trailing choices entry exists for.
				a.d.Prefs.RemoveWardrobe(a.srvMeta.serverKey, a.emotepick.iniswapChoices[cur])
				a.setIniswap("")
				c.FocusField("ic")
			}
		}
	}
	if r, ok := lay.rect("pair_button"); ok {
		if a.drawThemeButton("pair_button", "Pair", r) {
			a.pairbox.showPair = !a.pairbox.showPair
		}
	}
	if r, ok := lay.rect("call_mod"); ok {
		if a.drawThemeButton("call_mod", "Call Mod", r) {
			a.courtxtra.showModcall = true
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
	if a.evid.evidPresent {
		evLabel = "Evidence ●"
	}
	if r, ok := lay.rect("evidence_button"); ok {
		if a.drawThemeButton("evidence_button", evLabel, r) {
			a.toggleEvidence()
		}
	}

	// Emote grid at the theme's metrics, with page arrows.
	if r, ok := lay.rect("emotes"); ok && !a.classic.panelHidden(panelEmotes) {
		a.drawEmoteGridThemed(r, lay, vp)
	}
	// A themed layout that declares no `emotes` rect (or a hidden grid, or a
	// still-loading char.ini) never reaches the grid's own preview site — see
	// drawSpritePreviewFallback. Right-clicking an effects-dropdown row must still
	// show its box.
	a.drawSpritePreviewFallback(true)

	// BAND 3 of 3: above every AO2 widget this pass painted, and below CLIENT
	// chrome — the compact toolbox, the warning line, the server-tab strip and the
	// layout editor all draw after it. That ordering is the AO2-full-parity rule
	// expressed in paint order: an element may decorate anything inside the design
	// canvas, but it may never bury the controls that get the player back OUT of it.
	a.drawElementBands(lay, theme.BandOverlay)

	// NOTHING of AsyncAO's own draws inside the design canvas (#21, rule (c)).

	// Compact hover toolbox (#27/A1): slim bottom-right grip → Theater / Edit /
	// Hide-UI icon chips on hover or pin. Normal play only — skipped while the
	// themed editor is armed. The pinned per-piece panel is NOT drawn here (it
	// needs real input this fenced pass can't give it — see the classic path in
	// screens.go); it draws post-courtroom in app.go alongside drawFloatingPanels.
	a.drawCompactToolboxInPass()

	if a.warnActive() {
		c.LabelClipped(pad, h-44, w-2*pad, a.warnLine, ColDanger)
	}

	// Layout editor overlay: last, above everything it edits.
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
	sc := &a.link.room.Scene
	// Blankpost hides the whole chatbox (frame + showname + text) so only the
	// sprite shows; the second clause is the unchanged idle/no-message case.
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	// Per-character chatbox wins over the theme skin (AO2 get_chat priority),
	// exactly like the classic overlay; the theme's ink still applies over it
	// (skinned below is "any skin drew", not "the theme's skin drew").
	var charSkin *sdl.Texture
	if sc.ChatSkinBase != "" {
		if page, ok := a.d.Store.Get(sc.ChatSkinBase); ok && len(page.Frames) > 0 {
			charSkin = page.Frames[0]
		}
	}
	skinPage, themeSkinned := a.themePage(themeStemChatbox)
	skinned := charSkin != nil || themeSkinned
	// ONE resolution, shared verbatim with the export's themed chatbox
	// (drawGifThemedChatbox) so video and comic frames can never disagree with what
	// the player was looking at. It also lands the Qt insets AO2 gets for free:
	// +4 on every side of the message, nothing on the showname (chatboxfit.go).
	// Resolved BEFORE the skin blit because of the ladder below: which chatbox art
	// gets painted depends on how wide the showname measures, so the name's box and
	// its face have to exist first. AO2 has the same dependency and solves it the
	nameBox, msgBox := chatboxTextRects(box, lay, box.Y+chatBoxTopStrip)

	nameCol := ColAccent
	if skinned && sc.ChatSkinNameHas {
		nameCol = sdl.Color{R: sc.ChatSkinNameColor.R, G: sc.ChatSkinNameColor.G, B: sc.ChatSkinNameColor.B, A: 255}
	} else if skinned && a.skin.themeHasName {
		nameCol = a.skin.themeNameCol
	}
	if a.d.Prefs.Load().NameColors { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.Load().NameSat)/100, float64(a.d.Prefs.Load().NameVal)/100)
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
	// integer arithmetic — see chatboxSkinLadder for the residency contract. These
	// variant picks are THEME art: a per-character skin has none, so both are skipped
	// for it (the name simply stays in its base rect).
	if charSkin == nil {
		nameBox, skinPage = a.chatboxSkinLadder(nameBox, lay, skinPage, snFont, snEmoji, sc.ShownameText, nameCol)
		// AO2's OTHER per-message skin decision, and the one that left narration posts
		// wearing an empty name plate: a blank showname takes the plate-less `chatblank`
		// art and hides the label (courtroom.cpp:3320-3331). AFTER the widen ladder
		// because AO2's ladder lives in the non-blank arm — a blank name can never widen
		// — and chatboxSkinLadder already returns the base rung for empty text, so the
		// two can never disagree about which page won.
		skinPage, _ = a.chatboxSkinForShowname(sc.ShownameText, skinPage)
	}

	if charSkin != nil {
		c.cgoRect = box
		// Same A4 tilt contract as the theme skin below: the skin art rotates, the
		// showname/message text stays axis-aligned.
		if ang := lay.angle(themeChatboxKey); ang == 0 {
			_ = c.Ren.Copy(charSkin, nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(charSkin, nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
	} else if themeSkinned {
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
		if a.d.Prefs.Load().ChatboxTint && sc.ShownameText != "" { // #14 per-character tint
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
	// alignment (courtroom.cpp:93, and again in the block above), which CLEARS
	// QLabel's default AlignVCenter, so AO2 draws the name at the TOP of the
	nameX, nameW := a.shownameSpanFor(nameBox, snFont, snEmoji, sc.ShownameText, nameCol)
	// Only the THEME's showname_bold bolds here. The classic overlay also honours the
	// client's "Bold names" pref; the themed box never has, and quietly turning that
	// on for every themed user is a look change nobody asked for — kept out of #39's
	// scope deliberately.
	a.labelEmojiWeight(snFont, snEmoji, nameX, nameBox.Y, nameW, sc.ShownameText, nameCol, a.elemBold(elemShowname))

	// The message folds the canvas scale in for the same reason the showname does:
	a.ensureChatRaster(wrapW, skinned, a.themedChatPct(elemMessage, a.chatPct, lay))
	// Text selection works on the themed chatbox too (drag a range / dbl-click
	// a word / triple-click all; Ctrl+C / right-click copies) — same handler
	// as the classic overlay, fed this layout's message rect.
	textRect := sdl.Rect{X: msgX, Y: msgY, W: wrapW, H: box.Y + box.H - msgY}
	// The box follows its own crawl — AO2's ensureCursorVisible on every tick
	// (courtroom.cpp:4514-4521, chatcrawlscroll.go). textRect.H is the room the text
	// really has (the message origin down to the bottom of the chatbox, which is also
	// the clip set below), and the offset is an exact 0 for a message that fits, so a
	// theme's ordinary post draws byte-identically to before.
	msgScroll := chatCrawlOffset(a.raster.msRaster, sc.VisibleRunes, textRect.H)
	msgDrawY := msgY - msgScroll
	// The clip's TOP is msgY for the same reason the height runs to the bottom of
	// the box: this is Qt's message-widget viewport, and it clips at the widget's
	// own top edge. Without it a scrolled message paints above msgY, where the
	// theme's showname sits (nameBox above). Only the top moves — chatcrawlscroll.go
	// keeps the other three edges the box's so the overhanging design rects above
	// are still drawn in full, and hands the #M5 animated painter the whole box
	// because it lifts glyphs off their row and can never be scrolled.
	msgClip := chatCrawlClip(box, msgY, a.raster.msAnim)
	a.handleChatSelect(textRect, msgScroll, sc)
	if a.raster.msAnim != nil || a.raster.msRaster != nil {
		// Shared scratch, same as pushClip (ui.go): SDL_RenderSetClipRect takes a
		// `const SDL_Rect *` (SDL_render.h:930) and the renderer keeps its OWN copy —
		// SDL_RenderGetClipRect (:946) fills a caller struct from it rather than
		// handing the pointer back — so the highlight/raster draws below are free to
		// overwrite cgoRect while this clip is in force.
		c.cgoRect = msgClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
		if a.sel.chatSelActive { // selection highlight, UNDER the text so it reads through
			a.drawChatSelHighlight(msgX, msgDrawY, wrapW, sc) // scrolled with the text it marks
		}
		if a.raster.msAnim != nil { // #M5 animated message
			reduce := a.d.Prefs.Load().ReduceMotionOn
			if a.raster.msAnim.Animates(reduce) {
				a.NoteAnimating() // clock-driven text FX must not freeze at idle=0 — same census as the classic chatbox (screens.go)
			}
			a.raster.msAnim.Draw(c.Ren, a.raster.glyphCache, a.raster.msAnimFont, a.d.Viewport.AnimClock(), sc.VisibleRunes, msgX, msgDrawY, reduce)
		} else {
			a.raster.msRaster.DrawScaled(c.Ren, sc.VisibleRunes, msgX, msgDrawY, c.RenderScalePct(), &msgClip) // device-exact crawl + the clip it must re-assert (msgClip, the one set above) — see the classic chatbox (screens.go)
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
const minEmoteGapPx int32 = 0

// emoteCellScale is the ONE factor the themed emote cell and its gap scale by.
// per-axis therefore squashed every emote button flat on a wide window (#33).
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
	if a.compose.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	a.refreshEmoteView() // favourite set + visible-index list (#77)
	vis := a.favstyle.emoteVisible
	// The rect r stays per-axis scaled (it is the theme's container, and the
	// grid reflows/pages into whatever shape it takes), but the CELL scales
	// uniformly so the button art keeps its designed aspect — see
	// emoteCellScale for why (#33).
	grid := emoteGridLayout(r, a.skin.themeEmoteCell, a.skin.themeEmoteGap, emoteCellScale(lay.scaleX, lay.scaleY))
	a.setEmoteGridMetrics(int(grid.cols), int(grid.rows), len(vis))
	perPage := a.emotepick.emotePerPage
	if a.emoteGridScrolls() { // mouse-wheel scrolls BY ROW (parity with the classic row)
		if d := c.WheelIn(r); d != 0 {
			a.scrollEmoteRows(int(-d) * emoteWheelRows)
		}
	}
	start := a.emoteFirstSlot()

	me := a.activeCharName()
	useImages := a.d.Prefs.Load().EmoteButtonImages
	// Emote-name hover tooltip config, read ONCE (RLock off the per-cell loop).
	hoverNamesOn := a.d.Prefs.Load().EmoteHoverNamesOn
	hoverNamesDelay := a.d.Prefs.EmoteHoverNamesDelay()
	for slot := start; slot < len(vis) && slot < start+perPage; slot++ {
		i := vis[slot] // real index into a.emotes (favs-only filters which show)
		e := &a.emotepick.emotes[i]
		btn := grid.cellRect(r, int32(slot-start))
		selected := i == a.emotepick.emoteIdx
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
			a.emotepick.emoteIdx = i
			a.compose.icPreanim = emoteHasPreanim(e) // "Pre" auto-follows the pick (AO2-Client ui_pre)
			a.speculateEmote(me, e)
			a.followPinnedPreview(me, e) // a pinned box follows the pick; a closed one stays closed
			c.FocusField("ic")           // AO2 focus_ic_input: pick emote, keep typing
		}
		// Right-click pins the preview open, exactly as the CLASSIC emote row does.
		if c.rightClicked && c.hovering(btn) {
			a.previewPinned = true
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
	if len(vis) == 0 && a.d.Prefs.Load().EmoteFavOnly {
		c.Label(r.X, r.Y+4, "No favourite emotes — turn off \"favourites only\" in Settings, then click an emote's ★.", ColTextDim)
	}

	// AO2's own emote_left / emote_right keep PAGE granularity (courtroom.cpp
	// on_emote_left_clicked steps current_emote_page); only the wheel is per-row.
	if a.emoteGridScrolls() {
		if lr, ok := lay.rect("emote_left"); ok {
			if a.drawThemeButton("emote_left", "<", lr) && a.emoteCanScrollUp() {
				a.scrollEmotePages(-1)
			}
		}
		if rr, ok := lay.rect("emote_right"); ok {
			if a.drawThemeButton("emote_right", ">", rr) && a.emoteCanScrollDown() {
				a.scrollEmotePages(1)
			}
		}
	}
	if a.budget.previewBase != "" {
		// Clamp to the WINDOW, not the stage — the box is draggable anywhere
		// (playtest: the preview could not be moved out of the viewport).
		a.drawSpritePreview(a.frame.winW, a.frame.winH, false, a.previewedEmoteName()) // ARM 2: caption the emote name
		// Pin-aware, and routed through closeSpritePreview so the trigger's dwell
		// id is disarmed (a raw previewBase="" re-opened the box the next frame).
		a.dismissPreviewOnClick()
		a.emoteGridDrewPreview = true // tell drawSpritePreviewFallback to stand down
	}
}

// themedExtrasHint fires a one-time-per-session toast when a themed (AO2) layout
// is in use, pointing players at where AsyncAO's own features live — a legacy AO2
// theme carries no element key for any of them, so they would otherwise be lost.
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
// OWNER DECISION: inside a theme's canvas these ARE the volume surface, and the
// music panel's own Volume toggle is not offered (drawMusicList's themed flag).
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
// Returns whether the track NAME was drawn here — not whether the plate art was.
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
		// The plate alone IS a legitimate theme choice — but then nothing here
		// draws the track name, so the panel's own "Now playing" row has to take it
		// back. Reporting true made the plate swallow the name outright: this
		// return is what drawMusicList skips its row on (nameDrew, not plateDrew).
		return false
	}
	// #21 label 16: "music_name_color" is this line's ink, and it is the case the
	// colour key most obviously exists for — the theme drew the plate underneath, so
	// it knows exactly what the text sits on. sampleThemeBackdrops measures THIS
	// art, not the courtroom background, for the same reason. The empty state keeps
	// ColTextDim: "Nothing playing" is AsyncAO's own placeholder (AO2 leaves the
	// label blank), so the theme's colour has no claim on it.
	label, col := musicNothingPlaying, ColTextDim
	if now := a.nowPlayingName(); now != "" {
		label, col = now, a.elemInkOr(elemMusicName, true, ColAccent)
	}
	// The FACE folds the canvas scale in, exactly as the themed chatbox's children
	// do (themedChatPct). music_name's rect was multiplied by lay.scaleX/scaleY at
	// build time, so on any fit that isn't 1:1 — Letterbox / Stretch / Crop /
	// Custom, or Native in a window narrower than the design canvas — leaving the
	// glyphs at the theme's declared point size clipped the track name early. This
	// was the only themed text surface that never got the fold.
	font := a.elemLabelFontAtPct(elemMusicName, a.themedChatPct(elemMusicName, DefaultScalePct, lay))
	dst := sdl.Rect{X: md.X + mn.X, Y: md.Y + mn.Y, W: mn.W, H: mn.H}
	// AO2's ScrollText insets its text by leftMargin on BOTH sides and reserves
	// both in its fit test (../AO2-Client/src/scrolltext.cpp:47, drawn at :75/:78/
	// :100). leftMargin is height()/3 computed in the CONSTRUCTOR, while the child
	// still has Qt's default 100x30 geometry, and it is never recomputed — so it is
	// the constant 10, not a function of the theme's music_name height.
	inset := int32(float64(ao2MusicNameMarginPx) * lay.scaleX)
	avail := dst.W - 2*inset
	if avail <= 0 {
		// A music_name narrower than its own two margins: nothing legible fits, so
		// nothing is drawn — and the return has to say so. Reporting true here (the
		// plate DID draw) suppressed the panel's "Now playing" row as well, hiding
		// the track name everywhere on that theme.
		return false
	}
	// Vertically centred in music_name's own height, like ScrollText (:78/:100);
	// we used to pin the label to the rect's top edge.
	ty := dst.Y + (dst.H-fontLineH(font))/2
	// Qt clips ui_music_name to its parent ui_music_display. Ours is an intersecting
	// pushClip, so a theme whose music_name is wider than its plate no longer spills
	// the name past the plate art onto the background.
	clipPrev, clipHad := c.pushClip(md)
	bold := a.elemBold(elemMusicName)
	// SCROLLED, like AO2. ui_music_name is a ScrollText and an overlong title
	// marquees there (scrolltext.cpp; ported in musicmarquee.go), which is the whole
	// reason AO2 titles are never cut. AsyncAO ellipsized, and the deviation was
	// recorded right here — it is retired.
	w, measured := c.fontTextWidthWeight(font, label, bold)
	if measured && marqueeScrolls(w, avail) && !a.d.Prefs.Load().ReduceMotionOn {
		a.drawMusicMarquee(font, dst, inset, ty, label, col, bold)
	} else {
		shown := label
		if measured && w > avail {
			// truncateLabelTo ellipsizes so the cut reads as "there is more"
			// instead of stopping mid-glyph; the full name stays in the list. It
			// measures at the drawn weight too, or the "fitted" string still
			// overflows by the emboldening.
			shown = c.fitLabelToFontWeight(font, label, avail, bold)
		}
		// music_name_bold rides the raster (one weighted draw), not a 1 px-offset
		// second pass — that offset scaled with the UI and smeared into a double
		// image (F1b).
		c.LabelClippedFontWeight(font, dst.X+inset, ty, avail, shown, col, bold)
	}
	c.popClip(clipPrev, clipHad)
	return true
}

// drawMusicMarquee paints the scrolling title: AO2's ScrollText::paintEvent
// (scrolltext.cpp:64-98), tiled from marqueeFirstX at the tiled width until the
// widget is covered.
// PACING CENSUS. This is clock-driven motion on a surface that is otherwise static,
// so it calls NoteAnimating for exactly the reason the animated chatbox text does:
// The 15-px edge alpha ramp AO2 composites over both ends (scrolltext.cpp:81-92) is
// NOT reproduced: it is a per-pixel destination-in pass over an offscreen buffer,
// and our text goes through the shared glyph atlas where a per-label alpha is the
func (a *App) drawMusicMarquee(font *ttf.Font, dst sdl.Rect, inset, ty int32, label string, col sdl.Color, bold bool) {
	c := a.ctx
	// AO2 tiles `m_text + separator` and steps by the width of THAT string
	// (scrolltext.cpp:52, :61), so the separator is what sits between repeats. The
	// tiled string and the scroll epoch come out of the slot TOGETHER: both are
	// invalidated by exactly one event (the title changing), and building the
	// concatenation here instead cost one heap allocation on every scrolling frame.
	tiled, elapsed := c.musicMarquee.frame(label, a.d.Viewport.AnimClock())
	// MEASURED AT THE WEIGHT IT IS DRAWN AT. AO2 takes wholeTextSize from
	// fontMetrics() of the widget's own font (scrolltext.cpp:62), so under
	// music_name_bold the tile width IS the bold advance. Measured plain while
	// LabelClippedFontWeight below draws bold, wholeW is short twice over: it
	// clamps the blit (ui.go LabelClippedFontWeight caps the source width at
	// maxW), which cuts the tail of every tile — AO2's "   ---   " separator —
	// and it under-steps `x += wholeW`, so consecutive copies creep together
	// instead of repeating at wholeTextSize.width() (scrolltext.cpp:79).
	wholeW, ok := c.fontTextWidthWeight(font, tiled, bold)
	if !ok || wholeW <= 0 {
		return
	}
	// The clip is the music_name rect, INSIDE the plate clip the caller pushed:
	clipPrev, clipHad := c.pushClip(dst)
	defer c.popClip(clipPrev, clipHad)

	x := dst.X + marqueeFirstX(marqueeScrollPos(elapsed, wholeW), inset)
	right := dst.X + dst.W
	for tile := 0; tile < marqueeMaxTiles && x < right; tile++ {
		// wholeW as the width bound, not the remaining room: the scissor above is
		// what cuts the copy, and passing a shrinking bound would truncate the
		// last copy's texture instead of clipping it.
		c.LabelClippedFontWeight(font, x, ty, wholeW, tiled, col, bold)
		x += wholeW
	}
	a.NoteAnimating()
}

// nowPlayingName is the short display name of the current track, or "" when
// nothing is playing — the same name the list rows and the IC "has played a song"
// line use, so all three agree.
func (a *App) nowPlayingName() string {
	if a.link.room == nil {
		return ""
	}
	now := a.link.room.Scene.MusicTrack
	if now == "" {
		return ""
	}
	return courtroom.MusicDisplayName(now)
}
