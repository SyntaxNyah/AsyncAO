package ui

// The courtroom adopts a theme's courtroom_design.ini geometry when it has "courtroom" and "viewport" rects.
// Elements it omits fall back per widget; themes without those rects keep the classic layout.

import (
	"math"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// AO2 themes hide elements by placing them past the design bounds (11037 by convention, on either axis).
// Qt clipped overhanging children at the window edge, so scaled rects clamp into the courtroom area.
const (
	// minThemedElementPx rejects degenerate scaled rects: sloppy theme data, not a widget.
	minThemedElementPx = 6
	// logMergeOverlapFrac: IC and OOC log rects overlapping this much are one toggled widget (AO2's ooc_toggle), drawn
	// tabbed.
	logMergeOverlapFrac = 0.6
	// ao2MusicNameMarginPx is ScrollText's left/right inset, reserved on both sides. `leftMargin = height() / 3` runs
	// in the constructor at Qt's default 100x30 geometry and is never recomputed (scrolltext.cpp:10), so it is a hard
	// 10 px.
	ao2MusicNameMarginPx = 10
)

// clampRectInto shifts r inside bounds, shrinking only when r is larger; ok=false when r never intersected (hidden).
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

// rectOverlapFrac reports how much of the smaller rect lies inside the other (0..1).
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

// themeLayoutCache holds the window-scaled design rects, recomputed only when the window size or theme changes.
type themeLayoutCache struct {
	valid          bool
	winW, winH     int32
	designW        int32
	scaleX, scaleY float64 // per-axis: equal for Native/Letterbox/Crop/Custom, independent for Stretch
	// textPct is the chatbox text's canvas scale in integer percent, baked so a font size cannot wobble between frames.
	// 0 means no canvas, and textScalePct reads that as 100.
	textPct int
	// shownameExtra is `showname_extra_width` in window pixels, baked like textPct. AO2 does not scale it, but AO2's
	// scale factor is 1; ours is not, and an unscaled extra would widen the box by a different fraction of the art at
	// each size.
	shownameExtra int32
	offX, offY    int32
	// topOff is the app-chrome band above the canvas; in the key because live and export frames share this cache.
	topOff int32
	// tabStripW is the strip's intrinsic width at build time; in the key because the strip is sized by its chips, so a
	// tab change resizes it with no window, band, fit or theme change, and the themed editor's box must stay on the
	// widget.
	tabStripW int32
	// tabStripParked is tabStripThemeParked() at build time; in the key because it selects the strip's placement
	// (docked band or theme canvas) and can flip with no other key change, as when a zero-delta drag is discarded at
	// release.
	tabStripParked bool
	// area is the canvas: the scaled "courtroom" design rect at (offX, offY). Stored because the bars are not
	// symmetric.
	area sdl.Rect
	fit  int                 // ThemeFit mode it was built for (rebuild on change)
	r    map[string]sdl.Rect // scaled; absolute except showname/message (chatbox-relative)
	// ang holds each rotatable key's rotation angle byte, baked so the draw path never reads prefs; absent means angle
	// 0.
	ang map[string]uint8
	// toggleLabel holds each AO2 toggle's label shortened to its own rect (AO2's truncate_label_text). Baked because
	// truncation allocates and themes do declare rects too narrow; an absent key means the label fits.
	toggleLabel map[string]string
	// el is the baked, z-resolved, anchor-resolved free element array. Fixed and inline so the draw loop reads only
	// values, with no allocation per rebuild; a stock AO2 theme leaves elN 0 and pays nothing.
	el  [theme.ElementCap]bakedElement
	elN int
	// band[b] is the half-open index range into el for one theme.ElementBand, in ascending z, ties in declaration
	// order.
	band [theme.ElementBandCount]elemBandRange
	// condVal holds the distinct visible_when values, interned so visibility is an integer compare in the band loops.
	condVal [theme.ElementCap]string
	condN   int
	// condGen stamps this intern table: App's memo of live condition values must not survive a rebuild that re-interned
	// them.
	condGen uint64
}

// themeLayout builds the cache for a canvas in the full (w, h) box with no client chrome, for offscreen export frames.
func (a *App) themeLayout(w, h int32) *themeLayoutCache {
	return a.themeLayoutIn(w, h, 0)
}

// themeWindowLayout fits the canvas under the app-chrome band.
// Crop and Custom art may run behind the band, but in all modes no theme widget is laid out above it.
func (a *App) themeWindowLayout(w, h int32) *themeLayoutCache {
	return a.themeLayoutIn(w, h, a.topChromeH())
}

// invalidateThemeCanvases drops both scaled-geometry caches, the courtroom's and character select's.
// ThemeZoom and ThemePan are in neither key, so sites moving them must clear both; a rebuild is lazy, so this is free.
func (a *App) invalidateThemeCanvases() {
	a.themeLay.valid = false
	a.charSelLay.valid = false
}

// themeLayoutIn returns the cache for a (w, h) box with `top` px reserved, rebuilding when its key or the theme
// changes.
func (a *App) themeLayoutIn(w, h, top int32) *themeLayoutCache {
	lay := &a.themeLay
	fit := a.d.Prefs.Load().ThemeFit
	// Measured before the validity test because they are part of the key; neither can recurse into this rebuild.
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
	// Shared with char select's canvas, which honours the same ThemeFit and must never round differently.
	f := a.fitDesignCanvas(int32(court.W), int32(court.H), w, h, top, fit)
	lay.scaleX, lay.scaleY = f.scaleX, f.scaleY
	// Rounded here so the chatbox's font resolution is integer arithmetic on every later frame.
	lay.textPct = canvasTextScalePct(f.scaleX, f.scaleY)
	// Scaled by scaleX because it is added to a width scaled by scaleX.
	// Floored at one pixel: rounding to zero would drop the skin swap and revert to the narrow art mid-message.
	if a.themeShownameExtra > 0 {
		lay.shownameExtra = int32(float64(a.themeShownameExtra) * f.scaleX)
		if lay.shownameExtra < 1 {
			lay.shownameExtra = 1
		}
	}
	lay.offX, lay.offY = f.area.X, f.area.Y
	lay.area = f.area
	// Widget bounds are the canvas minus any part above the band: Crop and Custom can raise the canvas past it, and a
	// widget clamped there is painted over and unclickable. Clamping offY instead would forbid Custom's upward pan.
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
		// AO2 declares four coordinate systems in courtroom_design.ini. Only courtroom-relative rects become
		// window-absolute and clamp here; chatbox-, viewport- and music_display-relative rects are re-based by the
		// widget owning their parent.
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
	// The tab strip is client chrome sized by its chips, so tabStripCacheRect overwrites the transform with the painted
	// rect, which the themed editor's drag, stacking, grip and reset all read. A degenerate strip drops the key:
	// nothing paints there.
	if _, present := a.themeRects[themeTabBarKey]; present {
		if strip, ok := a.tabStripCacheRect(lay, w, h, stripW, stripParked); ok {
			lay.r[themeTabBarKey] = strip
		} else {
			delete(lay.r, themeTabBarKey)
		}
	}
	// Read once on the cold rebuild; the R key invalidates, so rotation reaches the draw path without a per-frame prefs
	// read.
	if themeName, _ := a.d.Prefs.Load().Theme(); themeName != "" {
		lay.ang = a.d.Prefs.Load().ThemeRectRotationSnapshot(themeName)
	}
	a.bakeToggleLabels(lay)
	// Last in the rebuild: it reads the finished lay.r and rewrites it for every pane host.
	a.bakeThemeElements(lay)
	lay.valid = true
	return lay
}

// sessHasCCCC reports cccc_ic_support; AO2 shows or hides the pair, immediate and showname controls on it
// (courtroom.cpp:747-759), since a toggle for a field the wire drops does nothing.
func (a *App) sessHasCCCC() bool {
	return a.sess != nil && a.sess.Features.Has(protocol.FeatureCCCCIC)
}

// themedSwatchW is the swatch's bite off AO2's text_color rect; AO2 tints its combo box, but our hex wheel anchors on
// one.
const themedSwatchW = int32(14)

// themedToggles are AO2 checkbox rects whose labels may not fit. alt is the key's second spelling (AO2 probes
// "immediate" then "pre_no_interrupt", courtroom.cpp:1072); override is the asyncao_* rect, which wins when declared.
var themedToggles = [...]struct{ key, alt, override, label string }{
	{key: "pre", override: "asyncao_ic_pre", label: "Pre"},
	{key: "immediate", alt: "pre_no_interrupt", override: "asyncao_ic_immediate", label: "Immediate"},
	{key: "flip", label: "Flip"},
	{key: "additive", label: "Additive"},
	{key: "slide_enable", label: "Slide"},
	{key: "showname_enable", label: "Shownames"},
	{key: "guard", label: "Guard"},
}

// bakeToggleLabels shortens each toggle's label to its rect, once per cold rebuild.
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

// themedToggleRect resolves a toggle's rect: the asyncao_* override, then the AO2 key, then AO2's second spelling.
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

// themeCanvasFit is where one design canvas lands in a window box: per-axis scale and the scaled, centred rect.
type themeCanvasFit struct {
	scaleX, scaleY float64
	area           sdl.Rect
}

// fitDesignCanvas places a design canvas in the (w, h) box with `top` px reserved, under ThemeFit mode `fit`.
// Shared by the courtroom and char select (setFixedSize(f_charselect.width, f_charselect.height), charselect.cpp:83).
func (a *App) fitDesignCanvas(designW, designH, w, h, top int32, fit int) themeCanvasFit {
	// A band taller than the window would invert the box, so floor the height; minThemedElementPx then hides the
	// widgets.
	availH := h - top
	if availH < 1 {
		availH = 1
	}
	var f themeCanvasFit
	// Native only shrinks; Stretch fills each axis; Crop scales up and overflows; Letterbox scales down with bars.
	sx, sy := float64(w)/float64(designW), float64(availH)/float64(designH)
	switch fit {
	case config.ThemeFitNative:
		// Stock AO2 never scales: setFixedSize makes the window the canvas (charselect.cpp:83, set_courtroom_size()).
		// Our window is resizable, so scale is 1.0 and only shrinks, because MinWindowW/H is below several themes'
		// design sizes.
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
	// Centred under the band. Crop and Custom overflow on purpose; each caller's clamp keeps its widgets out of the
	// band.
	f.area.Y = top + (availH-f.area.H)/2
	if fit == config.ThemeFitCustom { // pan to crop where you like
		px, py := a.d.Prefs.Load().ThemePan()
		f.area.X += int32(px) * w / 100
		f.area.Y += int32(py) * availH / 100
	}
	return f
}

// angle returns a key's baked rotation angle in degrees (0 = unrotated, the plain Copy path).
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

// textScalePct is the chatbox text's canvas scale in percent, or DefaultScalePct for a cache that never carried a
// canvas.
func (l *themeLayoutCache) textScalePct() int {
	if l.textPct <= 0 {
		return DefaultScalePct
	}
	return l.textPct
}

// shownameExtraPx is `showname_extra_width` in window pixels; zero means the theme declared none, or no canvas.
func (l *themeLayoutCache) shownameExtraPx() int32 { return l.shownameExtra }

// rect fetches a usable scaled rect; absent when the theme hides or omits it.
func (l *themeLayoutCache) rect(key string) (sdl.Rect, bool) {
	r, ok := l.r[key]
	if !ok || r.W <= 0 || r.H <= 0 {
		return sdl.Rect{}, false
	}
	return r, true
}

// drawScreenBackdrop paints a theme screen background stretched to the window, or the flat client color.
func (a *App) drawScreenBackdrop(w, h int32, stem string) {
	c := a.ctx
	dst := sdl.Rect{X: 0, Y: 0, W: w, H: h}
	// A busy AO2 lobbybackground often makes our server list unreadable, so the plain backdrop is the default.
	if stem == "lobbybackground" && a.d.Prefs.Load().PlainLobby {
		c.Fill(dst, ColBackground)
		return
	}
	if page, ok := a.themePage(stem); ok {
		// &dst into cgo would heap-allocate dst every frame, even on the plain-fill paths (escape analysis is static).
		c.cgoRect = dst
		// themedRotationDeg reads the baked cache map, never prefs; these backdrops are not editor keys, so this
		// resolves 0.
		if ang := a.themedRotationDeg(stem); ang == 0 {
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(a.themeFrame(page), nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
		return
	}
	c.Fill(dst, ColBackground)
}

// drawThemeButton draws the theme's art when resident (hover = accent border), else the kit's chip button; reports
// clicks.
func (a *App) drawThemeButton(key, label string, r sdl.Rect) bool {
	c := a.ctx
	if page, ok := a.themePage(themeBtnStem(key)); ok {
		// &r into cgo would heap-allocate the parameter on every call.
		c.cgoRect = r
		// Only the art rotates: the hover border and hit test stay the un-rotated rect.
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

// drawThemedSFXPicker draws the per-message SFX dropdown, shared by the themed IC row's crammed and theme-placed paths.
func (a *App) drawThemedSFXPicker(rect sdl.Rect) {
	c := a.ctx
	if next, changed := c.Dropdown("sfxdd", rect, a.sfxChoices, a.sfxChoiceIdx); changed {
		a.applySFXPick(next) // pick + audition + focus bounce (icsfx.go; courtroom.cpp:5498-5502)
	}
	a.sfxRowRightClickPreview()
	c.TooltipAfter("sfxdd-tip", rect, "Sound for your NEXT message — 'auto' uses the emote's own sound, or pick one to override. Right-click a row to hear it without picking it. Extras → SFX Browser for favourites & any sound by name.")
}

// Themed panel body metrics. A theme's rect is the whole widget, but the shared draw helpers start content at list.X/Y
// because the classic path insets first, so the themed path insets itself and reserves room for its chip strip.
const (
	// themedLogInsetPx is Qt's QTextDocument::documentMargin, the only inset left on AO2's frameless chatlogs
	// (courtroom.cpp:828, courtroom.cpp:835); the list bodies share it so logs and lists match.
	themedLogInsetPx = int32(4)
	// themedChipH is the chip strip's height; AO2 has no toggle rects for our extra tabs (Notes, Players), so chips sit
	// inside.
	themedChipH = int32(22)
	// themedChipGap separates two chips in the strip, and the strip from the body.
	themedChipGap = int32(4)
	// themedStripH is the chips plus one gap, measured from insetThemedBody(rect): insetting after offsetting would
	// double it.
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

// insetThemedBody applies themedLogInsetPx to a themed panel body before a shared draw helper.
// The right edge stays put: Qt keeps a scrollbar flush at the widget's right edge, and wrapW already reserves a margin.
// A rect too small for the margin is returned untouched, because an inverted rect draws worse than a flush one.
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

// drawCourtroomThemed is the design-driven courtroom; behavior is shared with the classic path.
func (a *App) drawCourtroomThemed(w, h int32, lay *themeLayoutCache) {
	c := a.ctx
	// Layout edit mode keeps every widget below inert; the editor overlay paints and interacts at the very end.
	a.layoutEditFence()
	// A theme's optional "asyncao_toolbox" rect anchors the compact toolbox for the whole pass.
	// The toolbox draws post-court, so the flag must outlive this function and is cleared in app.go.
	if tr, ok := lay.rect("asyncao_toolbox"); ok {
		a.toolboxThemeRect, a.toolboxThemeRectOn = tr, true
	} else {
		a.toolboxThemeRectOn = false
	}
	// Occlude what the toolbox strip covers before any themed widget draws, because occlusion is read at hit-test time.
	// drawCompactToolboxInPass replays this frame's decision rather than re-deriving it, so occlusion and pixels agree.
	a.fenceCompactToolbox(w, h)
	// After the toolbox on purpose: it paints above the sprite-preview box, so its owner-suspend must cover this
	// publication.
	a.fenceSpritePreview()
	a.themedExtrasHint() // one-time-per-session: point players at the Extras box

	// Letterbox fill, then the theme's window art over the design area.
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	court := lay.area
	if page, ok := a.themePage("courtroombackground"); ok {
		// &court into cgo would heap-allocate court on every themed frame, so use the shared scratch: SDL copies the
		// rect during the call. Nothing between here and the Copy touches cgoRect.
		c.cgoRect = court
		// courtroombackground is a fixed stage frame, so this resolves 0; themedRotationDeg reads the baked map, never
		// prefs.
		if ang := a.themedRotationDeg("courtroombackground"); ang == 0 {
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(a.themeFrame(page), nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
	} else {
		c.Fill(court, ColPanel)
	}
	// Resolved once for the whole pass: a mid-pass change would make one band's decoration disagree with another's.
	a.refreshElementConditions(lay)

	// Band 1 of 3: above the courtroom backdrop, below every widget.
	a.drawElementBands(lay, theme.BandBackdrop)

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
	a.drawOverlayChatLayer(vp) // effects.ini layer=chat: over the chatbox (courtroom.cpp:3234-3237)
	a.drawCourtOverlays(vp, lay)
	a.drawReactionFloats(vp) // #2: emoji reactions rising over the stage (0-alloc when none)

	// Band 2 of 3: over the viewport and chatbox, behind the logs and panels.
	// Before the modal return because it dresses the stage, which stays on screen under a modal.
	a.drawElementBands(lay, theme.BandMid)

	// Same modal list and editor guard as the classic path: with an editor armed, returning here would leave it undrawn
	// while its input blocking had already killed the modal's buttons.
	if !a.modalReturnSkippedWhileEditing() && a.drawCourtroomModals(w, h) {
		return
	}

	// Themes that stack the IC and OOC logs on one rect meant one toggled widget (AO2's ooc_toggle), so draw them
	// tabbed. ms_chatlog is not a server_chatlog alias: AO2 places the debug log there (courtroom.cpp:831), and a theme
	// declaring only it gets no server chatlog, because set_size_and_pos hides a widget whose rect is missing
	// (courtroom.cpp:1334-1338).
	icRect, okIC := lay.rect("ic_chatlog")
	oocRect, okOOC := lay.rect("server_chatlog")
	merged := okIC && okOOC && rectOverlapFrac(icRect, oocRect) >= logMergeOverlapFrac
	if merged && !a.panelHidden(panelLog) {
		// One inset for the whole panel, so chips and list share the theme's margin and the gap is exactly
		// themedChipGap.
		panelBody := insetThemedBody(icRect)
		// fitThemedChips owns the strip's geometry: a theme's narrow log rect would overflow a straight line of chip
		// constants.
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
	// ooc_toggle swaps server chat for the debug log (on_ooc_toggle_clicked, courtroom.cpp:5197), which draws at
	// ms_chatlog. A theme declaring only one of the two rects gets only that panel.
	dbgRect, okDbg := lay.rect("ms_chatlog")
	showDebug := a.debugOOC && okDbg
	if okOOC && !merged && !showDebug && !a.panelHidden(panelOOC) {
		a.drawOOCLogList(insetThemedBody(oocRect), true)
	}
	if showDebug && !a.panelHidden(panelOOC) {
		a.drawThemedDebugLog(insetThemedBody(dbgRect))
	}
	if tr, ok := lay.rect("ooc_toggle"); ok && !a.panelHidden(panelOOC) {
		// The label names the panel on screen, not what the click does (AO2 writes "Server" under server chat,
		// courtroom.cpp:1014). Inert without an ms_chatlog rect: toggling would just blank the panel.
		if c.Button(tr, a.oocToggleLabel()) && okDbg {
			a.debugOOC = !a.debugOOC
			a.debugOOCScroll = 0
		}
	}
	// OOC inputs draw wherever the theme put them: merged tabs still need a send box.
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
			// Tab-local like the classic OOC box: the saved default is only the Settings field, so a name here can't
			// follow to other tabs.
			onP, onE := a.icFieldFonts(a.oocName) // CJK-safe OOC name (ASCII stays fast path)
			a.oocName, _ = c.TextFieldEmoji("oocname", nameR, a.oocName, "OOC name", onP, onE)
			if name := a.pickNameDropdown("oocpick", sdl.Rect{X: nameR.X + nameR.W + 2, Y: nameR.Y, W: dd, H: nameR.H}); name != "" {
				a.oocName = name
			}
		}
	}

	// music_search is its own rect in AO2, a frameless QLineEdit outside the track list (courtroom.cpp:219-222).
	searchExternal := false
	if r, ok := lay.rect("music_search"); ok && !a.panelHidden(panelLog) {
		searchExternal = true
		a.musicSearch, _ = c.TextField("musicsearch", r, a.musicSearch, "Search")
	}
	// Drawn before the panel so it can drop its own row, which depends on whether the name drew, not the plate art.
	nameDrew := false
	if !a.panelHidden(panelLog) {
		nameDrew = a.drawThemedMusicPlate(lay)
	}
	// switch_area_music is AO2's "A/M" button (courtroom.cpp:1060-1061); ui_area_list shares the music_list rect
	// (:864). When placed, the chip strip drops its Music and Areas chips as duplicates (themedListStrip); the roster
	// chip stays unless the theme declares player_list, because it is otherwise the only way to reach the roster.
	if r, ok := lay.rect("switch_area_music"); ok && !a.panelHidden(panelLog) {
		if a.drawThemeButton("switch_area_music", "A/M", r) {
			a.logTab = themedSwitchAreaMusic(a.logTab)
		}
	}

	// The roster draws at `player_list` when declared, AO2's own key (courtroom.cpp:879), separate from the music_list
	// rect that ui_music_list (:867) and ui_area_list (:864) share; otherwise it shares music_list, as AO2's area list
	// does.
	rosterOwnRect := false
	if r, ok := lay.rect("player_list"); ok && !a.panelHidden(panelLog) {
		a.drawPlayerList(insetThemedBody(r))
		rosterOwnRect = true
	}

	// Music / Areas (and the roster, unless it drew at its own rect) share music_list: AO2 toggles them; we chip.
	if r, ok := lay.rect("music_list"); ok && !a.panelHidden(panelLog) {
		// One inset for chips and body, as in the log panel above, so logs and lists match.
		panelBody := insetThemedBody(r)
		// The strip carries only what AO2's buttons cannot reach: the roster alone when switch_area_music drew, else
		// all three.
		chips, n := fitThemedChips(panelBody, themedListStrip(lay), themedListViews[:], a.logTab)
		for i := int32(0); i < n; i++ {
			if c.Button(chips[i].r, chips[i].label) {
				a.logTab = chips[i].tab
			}
		}
		inner := themedStripBody(panelBody, n)
		switch {
		case a.logTab == logTabAreas:
			a.drawAreaList(inner, true)
		case a.logTab == logTabPlayers && !rosterOwnRect:
			a.drawPlayerList(inner)
		default:
			// rosterOwnRect lands here on purpose: a second drawPlayerList would fight the first over the shared scroll
			// and drag ids.
			a.drawMusicList(inner, true, searchExternal, nameDrew)
		}
	}
	// AO2 declares these six volume rects outside music_list, and each resolves on its own.
	a.drawThemedVolumeSliders(lay)

	// ao2_ic_chat_message is a bar of its own in every AO2 theme, with each IC control at its own rect on the rows
	// below. Each resolves through themedToggleRect; a theme declaring none draws nothing, as AO2 does
	// (courtroom.cpp:1334).
	if in, ok := lay.rect("ao2_ic_chat_message"); ok {
		var send bool
		icPrimary, icEmoji := a.icFieldFonts(a.icInput) // #M5: show typed emoji/unicode, not tofu
		// AO2's own placeholder (courtroom.cpp ui_ic_chat_message). The counter band is declared before the field so
		// text clips short of it.
		counterOn := a.d.Prefs.Load().MessageCounter
		c.ReserveFieldTail(icFieldID, a.icFieldTailReserve(counterOn))
		a.icInput, send = c.TextFieldEmoji(icFieldID, in, a.icInput, "Message in-character", icPrimary, icEmoji)
		a.recallIC() // #8: Up/Down recall recently-sent lines when the IC field is focused
		a.drawMsgCounter(in, counterOn)
		// Mirrors the classic send site. fencePointer blanks only the mouse, so this keeps Enter from sending into a
		// room the server has already removed the user from.
		if send && !a.serverNoticeDlg.open {
			a.sendIC()
		}
	}

	// text_color (AO2 ui_text_color, courtroom.cpp:1138): swatch + dropdown.
	if r, ok := themedToggleRect(lay, "text_color", "", "asyncao_ic_color"); ok {
		icSel, sw := a.icColorSelected()
		swatch := sdl.Rect{X: r.X, Y: r.Y, W: themedSwatchW, H: r.H}
		c.Fill(swatch, sw)
		c.Border(swatch, ColPanelHi)
		a.icSwatchRect = swatch // the free-hex wheel anchors here (v1.52.0)
		if a.icCustomOn && c.clicked && c.hovering(swatch) {
			a.showICColorWheel = !a.showICColorWheel // re-open to adjust (same as the classic row)
		}
		// Freeze the IC field's selection so a picked colour can wrap it (AO2's on_text_color_changed).
		a.captureICColorSel()
		dd := sdl.Rect{X: r.X + themedSwatchW, Y: r.Y, W: r.W - themedSwatchW, H: r.H}
		if next, changed := c.Dropdown(icColorDDID, dd, icColorChoices, icSel); changed {
			a.applyICColorPick(next)
		}
	}

	// pre (AO2 ui_pre, courtroom.cpp:1065) follows each emote pick; unchecked forces idle at the send site, skipping
	// the intro.
	if r, ok := themedToggleRect(lay, "pre", "", "asyncao_ic_pre"); ok && !a.panelHidden(slotICPre) {
		// ui_pre focuses the IC input (courtroom.cpp:502), the same wiring as the classic row.
		a.icPreanim = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("pre", "Pre"), a.icPreanim), a.icPreanim)
		c.Tooltip(r, "Pre: play this emote's pre-animation before the line. Follows each emote you pick; uncheck to skip an emote's intro.")
	}

	// immediate (AO2 ui_immediate, courtroom.cpp:1072-1084) reads "immediate", then "pre_no_interrupt"; aceattorney2x
	// declares only the latter. AO2 hides it when the server lacks cccc_ic_support, since the field only exists there
	// (courtroom.cpp:747-759).
	if r, ok := themedToggleRect(lay, "immediate", "pre_no_interrupt", "asyncao_ic_immediate"); ok && a.sessHasCCCC() {
		a.icImmediate = c.CheckboxIn(r, lay.themedToggleLabel("immediate", "Immediate"), a.icImmediate)
		c.Tooltip(r, "Immediate: the preanim plays without holding back the text")
	}

	// flip (AO2 ui_flip, courtroom.cpp:1629-1636) shows only when the server advertises FLIPPING. It mirrors
	// a.pairFlip, and is left alone when hidden because pairFlip is real pair state that parks per tab.
	if r, ok := themedToggleRect(lay, "flip", "", ""); ok &&
		!a.panelHidden(slotICFlip) && a.sess != nil && a.sess.Features.Has(protocol.FeatureFlipping) {
		a.pairFlip = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("flip", "Flip"), a.pairFlip), a.pairFlip) // ui_flip → focus_ic_input (courtroom.cpp:503)
		c.Tooltip(r, "Flip: mirror your character's emotes. Same setting as the Pair panel's flip toggle.")
	}

	// additive (AO2 ui_additive, courtroom.cpp:1089): the next message appends to your last. Forced off whenever
	// hidden, so a stale check cannot ride a message; applyAO2DefaultRects always supplies the rect, so the feature and
	// pref tests decide.
	if r, ok := themedToggleRect(lay, "additive", "", ""); ok &&
		a.sess != nil && a.sess.Features.Has(protocol.FeatureAdditive) && a.d.Prefs.Load().AdditiveText {
		a.icAdditive = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("additive", "Additive"), a.icAdditive), a.icAdditive) // :504 → :6572
		c.Tooltip(r, "Additive: this message adds to your last one instead of replacing it (2.8 narration-style RP).")
	} else {
		a.icAdditive = false
	}

	// slide_enable (AO2 ui_slide, courtroom.cpp:1096): the send site clears it, since "Slides can't be sticky for
	// nausea reasons" (courtroom.cpp:2362).
	if r, ok := themedToggleRect(lay, "slide_enable", "", ""); ok {
		a.icSlide = a.bounceOnToggle(c.CheckboxIn(r, lay.themedToggleLabel("slide_enable", "Slide"), a.icSlide), a.icSlide) // ui_slide_enable → focus_ic_input (courtroom.cpp:506)
		c.Tooltip(r, "Slide: the character slides into position for this message instead of cutting.")
	}

	// showname_enable: AsyncAO stores the inverse (ForceCharNames), so the checkbox reads and writes the negation.
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

			// Push the live room too: nothing re-reads the pref until an unrelated applyPrefsToRoom.
			if a.room != nil {
				a.room.ForceCharNames = !next
			}
		}
		c.Tooltip(r, "Shownames: show players' custom shownames. Off falls back to character names, which makes impersonation obvious.")
	}

	// guard (AO2 ui_guard, courtroom.cpp:1092) silences modcall alerts for this session; mod-only, as in AO2.
	if r, ok := themedToggleRect(lay, "guard", "", ""); ok && a.amIMod() {
		a.modcallGuard = c.CheckboxIn(r, lay.themedToggleLabel("guard", "Guard"), a.modcallGuard)
		c.Tooltip(r, "Guard: silence the modcall sound and window flash. The OOC line and the auto-clip still arrive.")
	}

	// realization (AO2 ui_realization, courtroom.cpp:1108) and screenshake (:1113) are toggles because AO2 arms them
	// for the next message; the send site clears them (courtroom.cpp:2328). AO2 uses the theme's art (setImage,
	// courtroom.cpp:1109) and falls back to text like AOButton.
	if r, ok := themedToggleRect(lay, "realization", "", ""); ok {
		if a.drawThemeButton("realization", "Flash", r) {
			a.toggleRealization() // arms the WIRE FLAG *and* the effects row — icarms.go
		}
		a.drawArmedOverlay(r, a.icRealize)
		c.Tooltip(r, "Realization: a white flash + sound on your next message. Clears after it sends.")
	}
	if r, ok := themedToggleRect(lay, "screenshake", "", ""); ok {
		if a.drawThemeButton("screenshake", "Shake", r) {
			a.toggleScreenshake()
		}
		a.drawArmedOverlay(r, a.icShake)
		c.Tooltip(r, "Screenshake: shake the courtroom on your next message. Clears after it sends.")
	}

	// sfx_dropdown (AO2 ui_sfx_dropdown, courtroom.cpp:952): a sound for the next message, overriding the emote's until
	// "auto".
	if r, ok := themedToggleRect(lay, "sfx_dropdown", "", "asyncao_ic_sfx"); ok {
		a.ensureSFXChoices()
		a.drawThemedSFXPicker(r)
	}

	// effects_dropdown (AO2 ui_effects_dropdown, courtroom.cpp:968): AO2 hides it while spectating or when the roster
	// is empty (:5586-5598).
	if r, ok := themedToggleRect(lay, "effects_dropdown", "", "asyncao_ic_effects"); ok {
		a.drawEffectsPicker(r)
	}

	// Text FX and the emoji picker have no AO2 design key, so they live in Extras unless a theme declares the asyncao_*
	// rects.
	if r, ok := lay.rect("asyncao_ic_fx"); ok {
		a.fxButton(r)
	}
	if r, ok := lay.rect("asyncao_ic_emoji"); ok && !a.panelHidden(slotICEmoji) {
		if a.drawEmojiBarButton(r) {
			a.showEmojiPicker = !a.showEmojiPicker
		}
	}
	if nameR, ok := lay.rect("ao2_ic_chat_name"); ok {
		// Session override box, as in the classic layout: blank shows and falls back to the persisted showname.
		namePlaceholder := a.d.Prefs.Load().Showname
		if namePlaceholder == "" {
			namePlaceholder = "Showname"
		}
		a.ensureNameOpts()
		dd := int32(0)
		if len(a.nameOpts) > 1 && nameR.W > 60 { // tiny ▾ saved-name picker, fitted inside the theme's box
			dd = 20
			nameR.W -= dd + 2
		}
		// CJK showname renders as real glyphs while typed; plain ASCII keeps the single-font fast path.
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
		// Arm, never send: the interjection rides the next message (courtroom.cpp:6048-6127, built at :2136-2147).
		// drawArmedOverlay stands in for canon's "*_selected" sprite swap, which a streaming client has no stem tier
		// for.
		for _, s := range shouts {
			if r, ok := lay.rect(s.key); ok {
				if a.drawThemeButton(s.key, s.label, r) {
					a.toggleShout(s.mod)
				}
				a.drawArmedOverlay(r, a.icShout == s.mod)
			}
		}
		if a.sess.Features.Has(protocol.FeatureCustomObjections) && a.hasCustomShout() {
			if r, ok := lay.rect("custom_objection"); ok {
				if a.drawThemeButton("custom_objection", a.customShoutLabel(), r) {
					a.toggleShout(protocol.ShoutCustom)
				}
				a.drawArmedOverlay(r, a.icShout == protocol.ShoutCustom)
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
		// Dropdown at the theme's pos_dropdown rect.
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
		// pos_remove reverts to the character's own side, offered only while the side differs
		// (courtroom.cpp:5285-5292). defaultSide() reads "wit" while a char.ini is in flight (get_char_side's
		// fallback), which keeps this hidden.
		if rr, ok := lay.rect("pos_remove"); ok && posRemoveVisible(a.mySide(), a.defaultSide()) {
			if a.drawThemeButton("pos_remove", "X", rr) {
				a.applySide(a.defaultSide())
			}
		}
	}
	// emote_dropdown: the emote list as a dropdown (courtroom.cpp:925-926; emotes.cpp:314-317). AO2 wires `activated`,
	// not `currentIndexChanged` (emotes.cpp:30): a user pick only, which is what `changed` reports.
	if r, ok := lay.rect("emote_dropdown"); ok {
		a.ensureEmoteChoices()
		if next, changed := c.Dropdown("emotedd", r, a.emoteChoices, a.emoteIdx); changed {
			a.selectEmote(next)
		}
	}
	// iniswap_dropdown + iniswap_remove (courtroom.cpp:937-939, :945-950). Index 0 is always the player's own folder,
	// so "remove" is offered only while something else is worn; AO2 resets the box to 0 on remove (:5436-5452).
	if r, ok := lay.rect("iniswap_dropdown"); ok {
		a.ensureIniswapChoices()
		cur := iniswapCurrentIndex(a.iniswapChoices, a.activeCharName())
		if next, changed := c.Dropdown("inidd", r, a.iniswapChoices, cur); changed {
			if next == 0 {
				a.setIniswap("") // back to the player's own character
			} else if next < len(a.iniswapChoices) {
				a.setIniswap(a.iniswapChoices[next])
			}
			// AO2 refocuses IC on every iniswap change, not only on remove (courtroom.cpp:5361-5363).
			c.FocusField("ic")
		}
		if rr, ok := lay.rect("iniswap_remove"); ok && cur != 0 && cur < len(a.iniswapChoices) {
			if a.drawThemeButton("iniswap_remove", "X", rr) {
				// Drop it from the wardrobe and stop wearing it; RemoveWardrobe is a no-op for a folder that was only
				// ever worn.
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
	// AO2's three utility buttons beside Call Mod; labels match AO2's setText (courtroom.cpp:1036-1056).
	if r, ok := lay.rect("change_character"); ok {
		if a.drawThemeButton("change_character", "Change character", r) {
			a.screen = ScreenCharSelect // hotkeyCharMenu (qol.go)
		}
	}
	if r, ok := lay.rect("reload_theme"); ok {
		if a.drawThemeButton("reload_theme", "Reload theme", r) {
			// pollThemeApply refreshes the rects and invalidates the canvases, so re-deriving anything here would race
			// its result.
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
			a.toggleEvidence()
		}
	}

	// Emote grid at the theme's metrics, with page arrows.
	if r, ok := lay.rect("emotes"); ok && !a.panelHidden(panelEmotes) {
		a.drawEmoteGridThemed(r, lay, vp)
	}
	// A themed layout with no reachable emote grid never hits the grid's preview site, and effects-row previews still
	// need it.
	a.drawSpritePreviewFallback(true)

	// Band 3 of 3: above every AO2 widget, below client chrome, so an element can never bury the controls that exit the
	// canvas.
	a.drawElementBands(lay, theme.BandOverlay)

	// Nothing of AsyncAO's own draws inside the design canvas: it would paint and click through theme rects like
	// music_list.

	// Compact hover toolbox, skipped while the themed editor is armed. The pinned panel needs real input this pass
	// can't give, so it draws post-courtroom in app.go.
	a.drawCompactToolboxInPass()

	if a.warnActive() {
		c.LabelClipped(pad, h-44, w-2*pad, a.warnLine, ColDanger)
	}

	// Layout editor overlay: last, above everything it edits. The tab strip paints here while an editor is armed,
	// before the overlay so the editor's chips sit on top. It replays the frame's one paint-site decision (not the
	// layoutEdit flag), so a mid-pass arm or disarm cannot make both sites paint, or neither.
	a.drawTabBarInPass(w, h)
	if a.layoutEdit {
		a.drawLayoutEditor(w, h, lay)
	}
}

// drawThemedChatBox stretches the skin to the ao2_chatbox rect, with showname/message at chatbox-relative rects.
func (a *App) drawThemedChatBox(box sdl.Rect, lay *themeLayoutCache) {
	c := a.ctx
	sc := &a.room.Scene
	// Blankpost hides the whole chatbox so only the sprite shows.
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	// Per-character chatbox wins over the theme skin (AO2 get_chat priority); skinned means any skin drew.
	var charSkin *sdl.Texture
	if sc.ChatSkinBase != "" {
		if page, ok := a.d.Store.Get(sc.ChatSkinBase); ok && len(page.Frames) > 0 {
			charSkin = page.Frames[0]
		}
	}
	skinPage, themeSkinned := a.themePage(themeStemChatbox)
	skinned := charSkin != nil || themeSkinned
	// Shared with the export's themed chatbox so frames match what the player saw. Resolved before the skin blit
	// because the art depends on the showname's measured width; AO2 orders it the same way (courtroom.cpp:3301-3373).
	nameBox, msgBox := chatboxTextRects(box, lay, box.Y+chatBoxTopStrip)

	nameCol := ColAccent
	if skinned && sc.ChatSkinNameHas {
		nameCol = sdl.Color{R: sc.ChatSkinNameColor.R, G: sc.ChatSkinNameColor.G, B: sc.ChatSkinNameColor.B, A: 255}
	} else if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	if a.d.Prefs.Load().NameColors { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.Load().NameSat)/100, float64(a.d.Prefs.Load().NameVal)/100)
	}
	// themedChatFace, not elemFontFor: AO2 scales rects and courtroom_fonts.ini sizes by one themeScalingFactor, so the
	// font folds with the canvas.
	snFont, snEmoji := a.themedChatFace(elemShowname, DefaultScalePct, lay, sc.ShownameText)
	// AO2's widen-and-swap ladder: a wide showname widens by showname_extra_width and swaps to the wider plate art.
	// The variants are theme art, so a per-character skin skips them.
	if charSkin == nil {
		nameBox, skinPage = a.chatboxSkinLadder(nameBox, lay, skinPage, snFont, snEmoji, sc.ShownameText, nameCol)
		// A blank showname takes the plate-less `chatblank` art and hides the label (courtroom.cpp:3320-3331). After
		// the ladder because AO2's ladder lives in the non-blank arm; chatboxSkinLadder returns the base rung for empty
		// text.
		skinPage, _ = a.chatboxSkinForShowname(sc.ShownameText, skinPage)
	}

	if charSkin != nil {
		c.cgoRect = box
		// The skin art rotates; the showname/message text stays axis-aligned.
		if ang := lay.angle(themeChatboxKey); ang == 0 {
			_ = c.Ren.Copy(charSkin, nil, &c.cgoRect)
		} else {
			_ = c.Ren.CopyEx(charSkin, nil, &c.cgoRect, ang, nil, sdl.FLIP_NONE)
		}
	} else if themeSkinned {
		// &box into cgo would heap-allocate the parameter on every themed frame, so use the shared scratch: SDL copies
		// the rect during the call, and only a map read and a slice index run before the Copy.
		c.cgoRect = box
		// Only the skin rotates; text stays axis-aligned. The destination is the ao2_chatbox rect on every rung: AO2
		// stretches swapped art into the widget it already sized (AOImage::setImage, aoimage.cpp:29-31).
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
	// After the whole skin resolution, not in the !skinned branch: full AO2 themes usually ship a chatbox.png.
	a.drawChatEgg(box, sc.MessageText)

	msgX, msgY := msgBox.X, msgBox.Y
	wrapW := msgBox.W

	// Clipped so a long showname can't spill out of the theme's box; elemFontFor resolves a covering face for non-Latin
	// names. showname_align spans the label's widget rect (courtroom.cpp:3338-3354). Y stays at the rect's top: AO2's
	// horizontal-only alignment clears QLabel's default AlignVCenter (courtroom.cpp:93).
	nameX, nameW := a.shownameSpanFor(nameBox, snFont, snEmoji, sc.ShownameText, nameCol)
	// Only the theme's showname_bold bolds here, unlike the classic overlay's "Bold names" pref.
	// The weight is in the raster: a one-pixel second pass lands on a different sub-pixel phase at fractional UI scale.
	a.labelEmojiWeight(snFont, snEmoji, nameX, nameBox.Y, nameW, sc.ShownameText, nameCol, a.elemBold(elemShowname))

	// The message folds the canvas scale in too: its wrap width comes from a scaled design rect.
	a.ensureChatRaster(wrapW, skinned, a.themedChatPct(elemMessage, a.chatPct, lay))
	// Same text selection handler as the classic overlay. The height runs to the bottom of the chatbox, not msgBox.H:
	// AO2 only offsets this widget by the chatbox origin (courtroom.cpp:3310), and several themes' message rects
	// overhang their chatbox.
	textRect := sdl.Rect{X: msgX, Y: msgY, W: wrapW, H: box.Y + box.H - msgY}
	// The box follows its own crawl: AO2's ensureCursorVisible on every tick (courtroom.cpp:4514-4521); 0 for a message
	// that fits.
	msgScroll := chatCrawlOffset(a.msRaster, sc.VisibleRunes, textRect.H)
	msgDrawY := msgY - msgScroll
	// The clip's top is msgY because Qt's message viewport clips at the widget's top edge; otherwise a scrolled message
	// paints over the showname. The animated painter gets the whole box because it lifts glyphs off their row.
	msgClip := chatCrawlClip(box, msgY, a.msAnim)
	a.handleChatSelect(textRect, msgScroll, sc)
	if a.msAnim != nil || a.msRaster != nil {
		// Shared scratch: SDL_RenderSetClipRect takes a const pointer (SDL_render.h:930) and keeps its own copy, so the
		// draws below may overwrite cgoRect while this clip is in force.
		c.cgoRect = msgClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
		if a.chatSelActive { // selection highlight, UNDER the text so it reads through
			a.drawChatSelHighlight(msgX, msgDrawY, wrapW, sc) // scrolled with the text it marks
		}
		if a.msAnim != nil { // #M5 animated message
			reduce := a.d.Prefs.Load().ReduceMotionOn
			if a.msAnim.Animates(reduce) {
				a.NoteAnimating() // clock-driven text FX must not freeze at idle=0 — same census as the classic chatbox (screens.go)
			}
			a.msAnim.Draw(c.Ren, a.glyphCache, a.msAnimFont, a.d.Viewport.AnimClock(), sc.VisibleRunes, msgX, msgDrawY, reduce)
		} else {
			a.msRaster.DrawScaled(c.Ren, sc.VisibleRunes, msgX, msgDrawY, c.RenderScalePct(), &msgClip) // device-exact crawl + the clip it must re-assert (msgClip, the one set above) — see the classic chatbox (screens.go)
		}
		_ = c.Ren.SetClipRect(nil)
	}
	a.chatZoomWheel(box)
}

// minEmoteCellPx rejects degenerate emote_button_size data; below it the grid falls back to AO2's stock emoteBtnCell
// square.
const minEmoteCellPx int32 = 8

// minEmoteGapPx floors emote_button_spacing at zero: AO2 applies no positivity test (text_file_functions.cpp:193-216)
// and themes declare "0, 0". Not negative: AO2 divides by (spacing + size) unguarded (emotes.cpp:70-71,
// charselect.cpp:160-161; only evidence.cpp:211 uses qMax), so with minEmoteCellPx this keeps (cell + gap) > 0 and the
// divisions cannot trap.
const minEmoteGapPx int32 = 0

// emoteCellScale is the one factor the themed emote cell and gap scale by. AO2 multiplies both axes by one factor
// (text_file_functions.cpp:212-213) and stretches the art (aobutton.cpp), keeping the designed aspect; min rather than
// max because a cell grown by the looser axis would overflow the emotes rect and drop a row or column. Only Stretch
// differs.
func emoteCellScale(scaleX, scaleY float64) float64 { return math.Min(scaleX, scaleY) }

// emoteGridMetrics is one themed emote page's geometry.
type emoteGridMetrics struct {
	cellW, cellH int32 // emote_button_size × emoteCellScale (designed aspect kept)
	gapX, gapY   int32 // emote_button_spacing, same uniform factor
	cols, rows   int32 // whole cells that fit in the rect, each ≥ 1
}

// emoteGridLayout is the pure geometry behind drawEmoteGridThemed, free of App and SDL state.
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

// cellRect is the n-th cell's rect (0-based, row-major); draw and hit test share it, so a click can't miss a drawn
// rect.
func (m emoteGridMetrics) cellRect(r sdl.Rect, n int32) sdl.Rect {
	return sdl.Rect{
		X: r.X + (n%m.cols)*(m.cellW+m.gapX),
		Y: r.Y + (n/m.cols)*(m.cellH+m.gapY),
		W: m.cellW, H: m.cellH,
	}
}

// drawEmoteGridThemed lays the emote buttons out on the theme's grid, paging with emote_left/right.
func (a *App) drawEmoteGridThemed(r sdl.Rect, lay *themeLayoutCache, vp sdl.Rect) {
	c := a.ctx
	if a.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	a.refreshEmoteView() // favourite set + visible-index list (#77)
	vis := a.emoteVisible
	// r stays per-axis scaled because the grid reflows into it; only the cell scales uniformly.
	grid := emoteGridLayout(r, a.themeEmoteCell, a.themeEmoteGap, emoteCellScale(lay.scaleX, lay.scaleY))
	a.setEmoteGridMetrics(int(grid.cols), int(grid.rows), len(vis))
	perPage := a.emotePerPage
	if a.emoteGridScrolls() { // mouse-wheel scrolls BY ROW (parity with the classic row)
		if d := c.WheelIn(r); d != 0 {
			a.scrollEmoteRows(int(-d) * emoteWheelRows)
		}
	}
	start := a.emoteFirstSlot()

	me := a.activeCharName()
	useImages := a.d.Prefs.Load().EmoteButtonImages
	// Read once, to keep the RLock off the per-cell loop.
	hoverNamesOn := a.d.Prefs.Load().EmoteHoverNamesOn
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
		// The favourite star wins the click over selecting the emote.
		if a.drawEmoteFavStar(btn, i) {
			// star toggled — swallow this cell's select for the frame
		} else if picked {
			a.emoteIdx = i
			a.icPreanim = emoteHasPreanim(e) // "Pre" auto-follows the pick (AO2-Client ui_pre)
			a.speculateEmote(me, e)
			a.followPinnedPreview(me, e) // a pinned box follows the pick; a closed one stays closed
			c.FocusField("ic")           // AO2 focus_ic_input: pick emote, keep typing
		}
		// Right-click pins the preview, as the classic emote row does; it sets previewPinned only, never the
		// preference.
		if c.rightClicked && c.hovering(btn) {
			a.previewPinned = true
		}
		// Hover preview: the scrubbed preanim when the emote has one, else the talking sprite.
		if c.HoverPreview("emote:"+e.Anim, btn) {
			a.previewEmote(me, e)
		}
		// Gated on hovering(btn) so the id concat runs only for the hovered cell.
		if hoverNamesOn && c.hovering(btn) {
			c.TooltipAfterDelay("emotename:"+e.Anim, btn, label, hoverNamesDelay)
		}
	}
	// Favs-only with nothing starred: tell the user how to recover.
	if len(vis) == 0 && a.d.Prefs.Load().EmoteFavOnly {
		c.Label(r.X, r.Y+4, "No favourite emotes — turn off \"favourites only\" in Settings, then click an emote's ★.", ColTextDim)
	}

	// AO2's emote_left / emote_right step pages (courtroom.cpp on_emote_left_clicked); only the wheel is per-row.
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
	if a.previewBase != "" {
		// Clamp to the window, not the stage: the box is draggable anywhere.
		a.drawSpritePreview(a.winW, a.winH, false, a.previewedEmoteName()) // ARM 2: caption the emote name
		// closeSpritePreview disarms the trigger's dwell id; a raw previewBase="" would re-open the box next frame.
		a.dismissPreviewOnClick()
		a.emoteGridDrewPreview = true // tell drawSpritePreviewFallback to stand down
	}
}

// themedExtrasHint toasts once per session where AsyncAO's features live, since an AO2 theme has no element key for
// them.
func (a *App) themedExtrasHint() {
	if a.themedHintShown {
		return
	}
	a.themedHintShown = true
	a.warnLine = clampLine("AO2 theme — Wardrobe, Jukebox & all AsyncAO extras are in the Extras menu (or press [" +
		a.hotkeyFor(hotkeyExtras) + "])")
	a.warnAt = time.Now()
}

// AO2 declares music_slider / sfx_slider / blip_slider with a label beside each (courtroom.cpp:1163-1175). Inside a
// theme's canvas these are the volume surface; master volume and blip rate have no AO2 rect and stay in Settings.
const (
	// Stable widget ids: concatenating as the classic strips do would allocate three strings per slider per frame.
	themedMusicSliderID = "themed:music_slider"
	themedSFXSliderID   = "themed:sfx_slider"
	themedBlipSliderID  = "themed:blip_slider"

	// themedVolumeMax is the slider range: AO2's sliders are 0..100 (courtroom.cpp:1165 setMaximum), as are our prefs.
	themedVolumeMax = 100
)

// drawThemedVolumeSliders paints the declared sliders and labels; each rect resolves independently.
func (a *App) drawThemedVolumeSliders(lay *themeLayoutCache) {
	c := a.ctx
	master, music, sfx, blip := a.effectiveVolumes()
	changed := false

	// Clipped because AO2 runs truncate_label_text on all three (courtroom.cpp:1173-1175) and a label can bleed across
	// its track.
	for _, l := range [...]struct{ key, text string }{
		{"music_label", "Music"},
		{"sfx_label", "Sfx"},
		{"blip_label", "Blip"},
	} {
		if r, ok := lay.rect(l.key); ok {
			c.LabelClipped(r.X, r.Y, r.W, l.text, ColText)
		}
	}

	// AO2's SFX handler also drives ambience and the objection player, but we have no separate ambience stream.
	// Do not route sfx into music.
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

// drawThemedMusicPlate draws ui_music_display and its child ui_music_name (courtroom.cpp:166-173, :885), reporting
// whether the name drew, not the art: on false the panel's "Now playing" row takes the name back.
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
	// &md into cgo would heap-allocate the parameter every frame.
	c.cgoRect = md
	_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)

	// music_name is a child rect, offset by the plate's origin.
	mn, ok := lay.rect("music_name")
	if !ok {
		// The plate alone is a legitimate theme choice, but the panel's "Now playing" row must then show the name.
		return false
	}
	// music_name_color is the ink for the plate underneath. "Nothing playing" is our own placeholder (AO2 leaves the
	// label blank), so it keeps ColTextDim.
	label, col := musicNothingPlaying, ColTextDim
	if now := a.nowPlayingName(); now != "" {
		label, col = now, a.elemInkOr(elemMusicName, true, ColAccent)
	}
	// The face folds the canvas scale in, as the themed chatbox does: music_name's rect is scaled, so a fixed size
	// clips early.
	font := a.elemLabelFontAtPct(elemMusicName, a.themedChatPct(elemMusicName, DefaultScalePct, lay), label)
	dst := sdl.Rect{X: md.X + mn.X, Y: md.Y + mn.Y, W: mn.W, H: mn.H}
	// ScrollText insets by leftMargin on both sides and reserves both in its fit test (scrolltext.cpp:47): see
	// ao2MusicNameMarginPx.
	inset := int32(float64(ao2MusicNameMarginPx) * lay.scaleX)
	avail := dst.W - 2*inset
	if avail <= 0 {
		// Narrower than its own two margins: nothing legible fits, so report false and let the panel's row show the
		// name.
		return false
	}
	// Vertically centred in music_name's own height, like ScrollText (:78/:100).
	ty := dst.Y + (dst.H-fontLineH(font))/2
	// Qt clips ui_music_name to its parent ui_music_display.
	clipPrev, clipHad := c.pushClip(md)
	bold := a.elemBold(elemMusicName)
	// An overlong title marquees, as in ScrollText; a title that fits, or ReduceMotion, takes the ellipsized draw.
	// Measured at the drawn weight: AO2's fit test uses the widget's font (scrolltext.cpp:45-47), and a title that fits
	// only plain would otherwise get neither remedy and be cut mid-glyph.
	w, measured := c.fontTextWidthWeight(font, label, bold)
	if measured && marqueeScrolls(w, avail) && !a.d.Prefs.Load().ReduceMotionOn {
		a.drawMusicMarquee(font, dst, inset, ty, label, col, bold)
	} else {
		shown := label
		if measured && w > avail {
			// truncateLabelTo ellipsizes so the cut reads as "there is more"; it measures at the drawn weight too.
			shown = c.fitLabelToFontWeight(font, label, avail, bold)
		}
		// music_name_bold rides the raster: a 1 px-offset second pass scales with the UI and smears into a double
		// image.
		c.LabelClippedFontWeight(font, dst.X+inset, ty, avail, shown, col, bold)
	}
	c.popClip(clipPrev, clipHad)
	return true
}

// drawMusicMarquee paints the scrolling title: ScrollText::paintEvent (scrolltext.cpp:64-98) tiled from marqueeFirstX.
// NoteAnimating is called from this draw site so the pacer doesn't idle, and stops when the title no longer reaches
// here. AO2's 15-px edge alpha ramp (scrolltext.cpp:81-92) is not reproduced: the glyph atlas allows one alpha per
// copy.
func (a *App) drawMusicMarquee(font *ttf.Font, dst sdl.Rect, inset, ty int32, label string, col sdl.Color, bold bool) {
	c := a.ctx
	// AO2 tiles `m_text + separator` and steps by that width (scrolltext.cpp:52, :61). The cached string saves one heap
	// allocation per scrolling frame; both values are invalidated by the title changing.
	tiled, elapsed := c.musicMarquee.frame(label, a.d.Viewport.AnimClock())
	// Measured at the drawn weight: AO2's tile width uses the widget's font (scrolltext.cpp:62). A plain width would
	// cut each tile's separator at the blit and under-step the repeat (scrolltext.cpp:79).
	wholeW, ok := c.fontTextWidthWeight(font, tiled, bold)
	if !ok || wholeW <= 0 {
		return
	}
	// Qt confines ScrollText's painting to its own widget, so copies are cut at the widget inside the caller's plate
	// clip.
	clipPrev, clipHad := c.pushClip(dst)
	defer c.popClip(clipPrev, clipHad)

	x := dst.X + marqueeFirstX(marqueeScrollPos(elapsed, wholeW), inset)
	right := dst.X + dst.W
	for tile := 0; tile < marqueeMaxTiles && x < right; tile++ {
		// wholeW as the bound: a shrinking one would truncate the last copy's texture instead of letting the scissor
		// clip it.
		c.LabelClippedFontWeight(font, x, ty, wholeW, tiled, col, bold)
		x += wholeW
	}
	a.NoteAnimating()
}

// nowPlayingName is the current track's short display name, or "", the same name the list and IC line use.
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
