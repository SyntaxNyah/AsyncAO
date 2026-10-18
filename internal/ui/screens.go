package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	pad      int32 = 8 // base unit, rebased ~0.8 toward classic-Windows compactness (was 10)
	rowH     int32 = 22
	fieldH   int32 = 21
	btnH     int32 = 22
	iconCell int32 = 64
	iconGap  int32 = 8
	// previewZoomMax caps the preview magnifier (× the fit scale).
	previewZoomMax = 8
	// Playtest sizing (consistent previews): every character previews at the
	// SAME height — the Settings default (config.PreviewHeightPx; shipped at
	// 384 px, double AO's native 192 stage: "it's really tiny" + re-dragging
	// it each session "is stinky") — regardless of source resolution. The
	// corner grip still resizes per session within [previewMinH, previewMaxH]
	// (bounds shared with the Settings slider via config); previewMaxW /
	// previewMinW keep extreme aspects on screen; the previewCaptionH strip
	// reports "source × shown-scale" AO2-style.
	previewMinH     int32 = config.MinPreviewHeightPx
	previewMaxH     int32 = config.MaxPreviewHeightPx
	previewMaxW     int32 = 960 // fits 4:3 art at the 720 px height cap (was 560; the window clamp still applies)
	previewMinW     int32 = 48
	previewCaptionH int32 = 20
	// previewNameStripH is the top-of-box emote-name caption band (ARM 2): the
	// emote-grid / fav-box preview reports WHICH emote it's showing, not only the
	// bottom resolution/scale strip. One text row's height.
	previewNameStripH int32 = 18
	// emoteBtnCell matches AO2's 40×40 emotions/button<N> art.
	emoteBtnCell int32 = 40
	// emoteTextCellW is the fixed cell width for text-mode emote chips, so
	// they page in the same uniform grid as image chips (labels clip).
	emoteTextCellW int32 = 104
	// scrollBarW/Gap reserve the scrollbar lane beside scrolling lists.
	scrollBarW   int32 = 12
	scrollBarGap int32 = 6
	// cleanOOCMinH floors the new-layout OOC box (and the IC log above it) so neither is crushed;
	// cleanGapPx is the breathing room between stacked clean-layout panels.
	cleanOOCMinH int32 = 96
	cleanGapPx   int32 = 6
	cleanHeaderH int32 = 20 // titled-header bar height on clean-layout boxes
	// boxBodyInsetPx is the side inset a titled, BORDERED box gives its body — the
	// clean-layout OOC box and every torn-off tab panel. It clears both the accent
	// outline and the header bar's own 1px frame, which is why it's a pixel wider than
	// logPanelInsetPx. Historical value, kept exactly: the classic layout must not move.
	boxBodyInsetPx int32 = 5
	// boxHeaderGapPx separates a titled box's header bar from the body beneath it.
	boxHeaderGapPx int32 = 4
	// cleanOOCBottomInsetPx is the clean OOC box's BOTTOM inset. It is one pixel
	// tighter than boxBodyInsetPx for no reason beyond history; named rather than
	// unified so this stays a naming change and not a pixel move.
	cleanOOCBottomInsetPx int32 = 4
	// courtBottomFurnitureH is the vertical budget the classic courtroom keeps below
	// the stage for the furniture it always draws there: the IC input row, the two
	// control-button rows and the emote grid. The stage is capped so it can never eat
	// that band. Historical value (it was a bare `h-220` in two places), named here
	// per hard rule 9 — and now measured against the height left UNDER the app-chrome
	// band rather than the raw window.
	courtBottomFurnitureH int32 = 220
)

// --- LOBBY ------------------------------------------------------------------------

// The lobby header's right-hand utility cluster. It is laid out by a RIGHT-TO-LEFT
// cursor rather than the raw `w - <literal> - pad` rects it used to carry, because
// those literals ran off the left edge of the window long before config.MinWindowW:
// "What's New" landed at x=-78 and "Logs" at x=-198 on a 640-wide window.
//
// Six of the cluster's buttons — Refresh, Settings, About, Ping, What's New,
// Logs — are rows of the app-wide menu bar now (menubar.go); only the Phone
// Book toggle and the centred Privacy/Glossary/What's New trio stay in the
// header.
//
// PHONE BOOK STAYS. Its label flips to "← All servers" on the phone-book page, so it
// is not really a command — it is that page's only way BACK, and burying a back
// affordance inside a menu strands anyone who does not know the menu is there. It is
// in the Servers menu as well; both surfaces call setPhoneBookPage, so they agree.
// Privacy and Glossary stay too, deliberately centre-stage for newcomers.
const (
	// lobbyUtilBtnW is the comfortable width when the window has room for the whole
	// cluster — the historical width, so a normal window is pixel-identical.
	lobbyUtilBtnW int32 = 110
	// lobbyUtilBtnGap separates two buttons of the cluster. Also the historical
	// value (the old literals stepped by 120 = width + gap).
	lobbyUtilBtnGap int32 = 10
	// lobbyUtilBtnMinW is the floor the shared-width shrink stops at, so a squeezed
	// button still shows a few glyphs (Ctx.Button clips its own label) instead of
	// collapsing to a sliver.
	lobbyUtilBtnMinW int32 = 64
	// lobbyUtilBtnCount is how many buttons the cluster lays out. It MUST match the
	// number of cursor steps in drawLobby; it is what turns "does the row fit?" into
	// arithmetic instead of a measured pass.
	//
	// One. Refresh and the connect-time sort came back from the Servers menu by
	// request, but NOT to this cluster: it shares the header row with the
	// Privacy/Glossary/What's New trio, and three utility buttons squeezed that row
	// to nothing at config.MinWindowW — which would strand What's New, whose only
	// other home is that row. They sit on their own strip above the server list
	// instead, which is both where they act and out of the header's way.
	lobbyUtilBtnCount int32 = 1
	// lobbyDirectRowDropY is how far below the header the direct-connect row sits;
	// lobbyListActionsDropY is how far below THAT the server list's own action strip
	// (Refresh / Sort) sits; lobbyListActionW is each button's width there. The
	// strip owns the full window width, so unlike the header cluster it needs no
	// shrink ladder. Named so lobbyListTop can be the single source both drawLobby
	// and its callers read — duplicating this arithmetic as a literal broke the
	// moment the strip pushed the list down.
	lobbyDirectRowDropY   int32 = 56
	lobbyListActionsDropY int32 = 34
	lobbyListActionW      int32 = 110
	// lobbyListActionsGapY is the breathing room under the action strip.
	lobbyListActionsGapY int32 = 8
	// directConnectFieldID is the lobby's direct-connect TextField id. Named because
	// the Servers menu focuses it by id from outside this file, and FocusField
	// silently drops an id nothing draws.
	directConnectFieldID = "direct"
)

// The direct-connect row, the OTHER half of the lobby's off-screen arithmetic. The
// utility cluster above was pinned by `w - <literal> - pad`; this row was pinned by
// `fieldX + <literal>` walking the opposite way, with the same result at the small
// end: on a config.MinWindowW window the TLS checkbox started past the right edge and
// "Save to phone book" and Connect were entirely outside the window. Connect is the
// row's whole point, so it is the one thing that may never leave.
const (
	// dcFieldWMax is the input's comfortable width — the historical 280, so a normal
	// window is pixel-identical to before. dcFieldWMin is the floor a squeeze stops
	// at: still enough for "127.0.0.1:50001".
	dcFieldWMax int32 = 280
	dcFieldWMin int32 = 120
	// dcGapPx is the gap between any two controls of the row. It is the historical
	// input→checkbox spacing (the field ended at +280, the first checkbox began at
	// +290), now applied uniformly; the caption→input gap was 12, so it tightens by
	// two pixels — the only pixel change this rewrite makes at a normal window size.
	dcGapPx int32 = 10
	// dcConnectW is the Connect button's width (historical).
	dcConnectW int32 = 100
	// dcLabelFull / dcLabelShort are the row's caption. The long one spells out every
	// accepted form, which is worth the width when there is width; the short one is
	// what a squeezed window gets instead of losing the button.
	dcLabelFull  = "Direct connect (ip:port, url:port, ws:// or wss://):"
	dcLabelShort = "Direct connect:"
)

// previewOwnerMark is the mark drawSpritePreview hands pushOverlayOwner: our own
// publication when we made one, else the CURRENT registry depth. That fallback is
// pushOverlayOwner's documented rule for an occluder that published nothing — it
// suspends nothing at all, so the box drawn on a pass with no preview fence (char
// select, the wardrobe, the background picker) still yields to the menu bar's
// open pane above it. Passing the latch's bare zero mark instead would suspend
// EVERY fence in the registry.
func (a *App) previewOwnerMark() int {
	if a.previewFence.on {
		return a.previewFence.mark
	}
	return a.ctx.overlayFenceMark()
}

// handlePreviewInput drives the sprite-preview box's mouse-wheel zoom and
// left-drag reposition. It runs in App.Frame BEFORE any screen draws, against
// last frame's cached box rect, so it claims the wheel and the mouse press ahead
// of the grid/list scroll and the icon clicks UNDER the box (it overlaps them).
// Off the hot path — a no-op whenever no preview is up.
func (a *App) handlePreviewInput() {
	c := a.ctx
	// Runs EVERY frame, before either early return below: the detached
	// window must keep receiving frames / get its close reconciled even on
	// passes where the in-app box itself never draws (previewBase=="" after
	// a screen switch force-cleared it while still detached, or simply no
	// box drawn yet this frame) — see syncPreviewWindow's own doc.
	a.syncPreviewWindow()
	// Clear last frame's fence answer FIRST, before any early return: this runs once
	// per frame above every screen, so a pass that publishes nothing (char select,
	// settings) can never replay a courtroom frame's stale mark into pushOverlayOwner.
	a.previewFence = previewFenceLatch{}
	if a.previewBase == "" {
		a.previewDrag = false
		return
	}
	box := a.previewFrameRect
	if box.W == 0 { // no preview drawn yet
		return
	}
	// The top-right chrome claims the click before the drag-start below.
	detachB, pinB, closeB := previewDetachRect(box), previewPinRect(box), previewCloseRect(box)
	if c.clicked && c.hovering(detachB) {
		a.toggleDetachPreview()
		c.clicked = false
		return
	}
	if c.clicked && c.hovering(pinB) {
		a.togglePreviewPin()
		c.clicked = false
		return
	}
	if c.clicked && c.hovering(closeB) {
		a.closeSpritePreview()
		c.clicked = false
		return
	}
	// Wheel zoom in/out over the box; claim the wheel so the list/grid under it
	// doesn't also scroll (same range as the − / + buttons).
	if c.wheelY != 0 && c.hovering(box) {
		a.previewZoom = int(clampI32(int32(a.previewZoom)+c.wheelY, 1, previewZoomMax))
		c.wheelY = 0
		c.wheelTaken = true
	}
	// Corner-grip resize (playtest: a consistent default size that is still
	// resizable): drag
	// the ◢ vertically to change the box height; width follows the art's aspect
	// on the next draw. Claims the press before the body move-drag below.
	grip := previewGripRect(box)
	if c.mouseDown && !a.previewResize && !a.previewDrag && c.hovering(grip) {
		a.previewResize = true
		a.previewResizeFrom = c.mouseY
		a.previewResizeBase = a.previewUserH
		if a.previewResizeBase == 0 {
			a.previewResizeBase = int32(a.d.Prefs.PreviewHeightPx()) // grip starts from the Settings default
		}
	}
	if a.previewResize {
		if c.mouseDown {
			a.previewUserH = clampI32(a.previewResizeBase+(c.mouseY-a.previewResizeFrom), previewMinH, previewMaxH)
		} else {
			a.previewResize = false
			c.clicked = false // a finished resize isn't a dismiss/selection
		}
		return // resizing owns the pointer — no move-drag underneath
	}
	// Left-drag to reposition. Start only on the body (not the bottom zoom-button
	// strip). Claiming the press here keeps a drag from selecting whatever icon is
	// underneath, and a real move swallows the release so click-to-dismiss/select
	// doesn't fire.
	// The chrome buttons are excluded from the drag-start test: a PRESS on one has
	// c.clicked == false (the click arrives on the release frame), so without this
	// the press would start a body drag and the release would return through the
	// branch above with previewDrag still latched.
	body := sdl.Rect{X: box.X, Y: box.Y, W: box.W, H: box.H - previewDragBottomReserve}
	if c.mouseDown && !a.previewDrag && c.hovering(body) && !c.hovering(detachB) && !c.hovering(pinB) && !c.hovering(closeB) {
		a.previewDrag = true
		a.previewDragMoved = false
		a.previewDragStart = [2]int32{c.mouseX, c.mouseY}
		a.previewDragBase = [2]int32{a.previewOffX, a.previewOffY}
	}
	if a.previewDrag {
		if c.mouseDown {
			dx, dy := c.mouseX-a.previewDragStart[0], c.mouseY-a.previewDragStart[1]
			if dx != 0 || dy != 0 {
				a.previewDragMoved = true
			}
			a.previewOffX = a.previewDragBase[0] + dx
			a.previewOffY = a.previewDragBase[1] + dy
		} else {
			// A press that STARTED on the box owns its own release, moved or not.
			// Unmoved, this used to fall through unconsumed: the box dismissed itself
			// from closeSpritePreviewOnLeave's "any click dismisses" arm, but the very
			// same click ALSO landed on whatever the box was floating over — over the
			// right-hand column that is a music row, so dismissing the preview started
			// playing a track (#37). Dismissing HERE keeps the old feel and makes the
			// consume unconditional, which is the actual fix.
			if !a.previewDragMoved && !a.previewIsPinned() {
				a.closeSpritePreview()
			}
			c.clicked = false
			a.previewDrag = false
		}
	}
}

// drawPreviewZoom draws the caption strip along the preview's bottom — the
// source resolution + the scale it's shown at (the playtest ask for the resolution
// scale line existing AO clients show) — flanked by the − / + magnifier buttons, whose
// clicks are consumed so the caller's click-to-dismiss doesn't fire. At >1× the
// cursor pans the magnified view.
func (a *App) drawPreviewZoom(frame sdl.Rect, srcW, srcH, shownH int32) {
	c := a.ctx
	const bh, bw int32 = 18, 22
	y := frame.Y + frame.H - bh - 1
	minus := sdl.Rect{X: frame.X + 2, Y: y, W: bw, H: bh}
	plus := sdl.Rect{X: frame.X + frame.W - bw - 2, Y: y, W: bw, H: bh}
	pct := int32(100)
	if srcH > 0 {
		pct = shownH * 100 / srcH
	}
	caption := fmt.Sprintf("%d×%d · %d%%", srcW, srcH, pct)
	if a.previewZoom > 1 {
		caption += fmt.Sprintf(" · %dx", a.previewZoom)
	}
	capW := c.TextWidth(caption) + 12
	lvl := sdl.Rect{X: frame.X + (frame.W-capW)/2, Y: y, W: capW, H: bh}
	for _, b := range []struct {
		r     sdl.Rect
		label string
	}{{minus, "-"}, {lvl, caption}, {plus, "+"}} {
		c.Fill(b.r, sdl.Color{R: 0, G: 0, B: 0, A: 205})
		c.LabelClipped(b.r.X+6, b.r.Y+1, b.r.W-8, b.label, ColAccent)
	}
	c.Tooltip(minus, "Zoom out (or scroll the wheel over the box)")
	c.Tooltip(plus, "Zoom in (or scroll the wheel) — at >1× move the mouse to pan. Drag the box to move it.")
	if c.hovering(minus) && c.clicked {
		if a.previewZoom > 1 {
			a.previewZoom--
		}
		c.clicked = false
	}
	if c.hovering(plus) && c.clicked {
		if a.previewZoom < previewZoomMax {
			a.previewZoom++
		}
		c.clicked = false
	}
}

// drawPreviewEmoteNav draws the try-before-wear control bar above the preview
// box — [<] caption [>] — and handles its clicks plus Left/Right keys (only
// when no text field owns the keyboard). Clicks on the arrows are consumed so
// the caller's "click dismisses the preview" check doesn't also fire.
func (a *App) drawPreviewEmoteNav(frame sdl.Rect) {
	c := a.ctx
	const barH = 22
	const navW = 22
	bar := sdl.Rect{X: frame.X, Y: frame.Y - barH - 2, W: frame.W, H: barH}
	c.Fill(bar, sdl.Color{R: 0, G: 0, B: 0, A: 210})
	caption := ""
	if a.previewEmoteIdx < len(a.previewLabels) {
		caption = a.previewLabels[a.previewEmoteIdx]
	}
	caption = fmt.Sprintf("%s (%d/%d)", caption, a.previewEmoteIdx+1, len(a.previewAnims))
	c.LabelClipped(bar.X+navW+4, bar.Y+3, bar.W-2*navW-8, caption, ColAccent)
	if c.Button(sdl.Rect{X: bar.X, Y: bar.Y, W: navW, H: barH}, "<") {
		a.cyclePreviewEmote(-1)
		c.clicked = false
	}
	if c.Button(sdl.Rect{X: bar.X + bar.W - navW, Y: bar.Y, W: navW, H: barH}, ">") {
		a.cyclePreviewEmote(1)
		c.clicked = false
	}
	if c.focusID == "" { // arrow keys cycle too, unless the search field has focus
		switch c.keyPressed {
		case sdl.K_LEFT:
			a.cyclePreviewEmote(-1)
		case sdl.K_RIGHT:
			a.cyclePreviewEmote(1)
		}
	}
}

// --- COURTROOM ----------------------------------------------------------------------

func (a *App) drawCourtroom(w, h int32) {
	c := a.ctx
	// (reported by Nightingale: hidden buttons still showed handles in the
	// editor view): the slot registry is rebuilt from THIS frame's draws while editing, so a
	// piece hidden mid-session stops ghosting handles the moment it stops
	// drawing — stale registrations from when it was visible used to live in
	// the map forever. Edit-mode only (the registry isn't read otherwise).
	if a.classicEdit {
		clear(a.slotReg)
	}
	// Load the slot overrides and re-tear last session's torn-off Extras widgets
	// BEFORE the fence below reads them: boxFencesPointer walks a.classicOv (torn
	// tabs) and a.extrasDetached (torn widgets), so both must be populated first or
	// frame one fences a stale/empty set (the same statelessness torn tabs already
	// enjoy). Neither call needs room/sess; both are latched, so this stays
	// alloc-free after the first courtroom frame.
	a.ensureClassicOv()
	a.reconstructTornWidgets(w, h)
	// Fence the courtroom under the non-blocking Extras box: while the pointer is
	// over the box, this whole pass runs pointer-blind so a click in the box can't
	// also land on the scene/log. The deferred unfence is a direct method (no
	// closure) and a no-op when the box is closed, so the common frame is alloc-free.
	if a.boxFencesPointer(w, h) {
		c.fencePointer()
	}
	defer c.unfencePointer()
	// Overlay pointer exclusivity (#26/#37, overlayfence.go): fences published
	// during THIS pass — the compact toolbox strip today — release when the pass
	// returns, because the post-courtroom overlays in app.go (floating panels,
	// palette, pickers, the pinned pieces panel) paint ABOVE the toolbox and must
	// not inherit its fence. Taking a mark rather than clearing outright leaves
	// room for an App-level publisher (the menu bar) to outlive this pass.
	fenceMark := c.overlayFenceMark()
	defer c.overlayFenceRelease(fenceMark)
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	// Clear the toolbox themed-rect flag every frame; only drawCourtroomThemed
	// re-arms it (when the theme ships "asyncao_toolbox"). Without this reset a
	// stale true from a prior themed frame would wrongly apply to the classic /
	// theater toolbox after the theme toggled off (the flag outlives the themed
	// pass because the toolbox draws post-court in app.go).
	a.toolboxThemeRectOn = false
	// Same reset discipline for the toolbox's per-frame fence/draw latch: only
	// fenceCompactToolbox (inside this pass, below the theme dispatch) sets it, so
	// clearing here means a theater / dropped-session / lobby frame can never replay
	// last frame's answer — and the post-courtroom draw site can trust !set as
	// "the in-pass site did not own the toolbox this frame".
	a.toolboxFence = compactToolboxLatch{}
	// The server-tab strip's paint site is decided ONCE here, at the top of the pass,
	// and replayed by both sites (chrometop.go). It must be taken BEFORE anything can
	// arm or disarm an editor mid-pass — handleHotkeys, the force-stops below, and the
	// editors' own Done/Esc all run later — or the two sites can disagree and paint the
	// strip twice or not at all.
	a.latchTabStripPaint()
	a.pollCharINI()
	if a.room == nil || a.sess == nil {
		if a.classicEdit {
			a.stopClassicEdit() // session dropped mid-edit — release the fence so the lobby isn't stuck
		}
		a.screen = ScreenLobby
		return
	}

	// Theater mode preempts BOTH layouts: stage only, Esc exits.
	if a.theaterOn {
		if a.classicEdit {
			a.stopClassicEdit() // can't edit the layout from theater mode — release the fence
		}
		a.drawTheater(w, h)
		return
	}

	// Theme-driven geometry: when the theme ships courtroom_design.ini
	// (and the toggle is on), the courtroom IS the theme's layout.
	// themeWindowLayout, not themeLayout: the design canvas is fitted into the height
	// left UNDER the app-chrome band and offset down by it, so it can never slide
	// beneath the menu bar / server-tab strip (#14).
	if lay := a.themeWindowLayout(w, h); lay.valid && a.d.Prefs.Load().ThemeLayoutOn {
		if a.classicEdit {
			a.stopClassicEdit() // a themed layout took over; classic edit is default-only
		}
		a.drawCourtroomThemed(w, h, lay)
		return
	}
	// The themed editor only exists over themed geometry; release its fence if
	// the theme (or the toggle) went away mid-edit.
	if a.layoutEdit {
		a.stopLayoutEdit()
	}
	// Classic slot editor (default layout only): fence the pointer BEFORE any
	// widget draws so its clicks reach the editor, not the courtroom; the overlay
	// itself draws LAST (after drawICControls).
	a.classicEditFence()
	// Classic path's toolbox latch + occlusion (#26). It must sit HERE, below the
	// theme dispatch above: compactToolboxStripRect consults a.toolboxThemeRectOn,
	// which this function force-clears each frame and only drawCourtroomThemed
	// re-arms — so publishing any earlier would fence the bottom-right DEFAULT rect
	// even under a theme that parks the toolbox somewhere else entirely.
	// drawCompactToolboxInPass (bottom of this function) replays the latch rather
	// than re-deriving it, so the fence and the pixels agree even though
	// handleHotkeys and the modal return both run in between.
	a.fenceCompactToolbox(w, h)
	// …and the sprite-preview box (#37), which floats over the right-hand column.
	// AFTER the toolbox deliberately: the toolbox paints above the box, so its
	// mark-scoped owner-suspend must cover this publication and not the reverse.
	a.fenceSpritePreview()

	// The app-chrome band (menu bar + docked server-tab strip) owns the top of the
	// window; everything the classic courtroom lays out starts below it (#14).
	top := a.topChromeH()
	// Viewport: AO 4:3 at the user's width percent (View −/+ buttons).
	vpW := w * int32(a.vpPct) / DefaultScalePct
	vpH := vpW * 3 / 4
	if vpH > h-top-courtBottomFurnitureH {
		vpH = h - top - courtBottomFurnitureH
		vpW = vpH * 4 / 3
	}
	// Precise (exact-px) sizing overrides the % knob: the native stage art is
	// 256×192, so an exact width that's an integer multiple stays crisp where the
	// %-of-window size lands between multiples and blurs. If the chosen size doesn't
	// fit the window, snap DOWN to the largest 256×192 multiple that does — clamping
	// to raw px would re-introduce the very blur this control removes.
	if ew := int32(a.d.Prefs.Load().ViewportExactW); ew > 0 {
		vpW, vpH = ew, ew*config.ViewportArtH/config.ViewportArtW
		if vpW > w-2*pad || vpH > h-top-courtBottomFurnitureH {
			fitM := (h - top - courtBottomFurnitureH) / config.ViewportArtH
			if wM := (w - 2*pad) / config.ViewportArtW; wM < fitM {
				fitM = wM
			}
			if fitM < 1 {
				fitM = 1
			}
			vpW, vpH = fitM*config.ViewportArtW, fitM*config.ViewportArtH
		}
	}
	vpDef := sdl.Rect{X: pad, Y: top + pad, W: vpW, H: vpH}
	// Movable + resizable stage. With no override the View knob / divider own its
	// 4:3 size (vpDef); once you drag a viewport handle in the editor, that override
	// wins (free position + size; the scene fills it) until you reset the box.
	vp := a.slotRect(slotViewport, vpDef, w, h)
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.renderViewportZoomed(vp)
	a.drawStageRecordButton(vp)
	a.handleVpDivider(vp, w) // drag the right edge to resize (claims the press BEFORE zoom/sprite-drag)
	// Camera input first (Ctrl+wheel zoom / Ctrl+drag pan); sprite drag
	// only at 1× — zoomed hit rects would lie.
	chatBandH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	a.handleViewportZoom(vp, c.mouseY >= vp.Y+vp.H-chatBandH)
	if a.vpZoom <= 1 {
		a.handleSpriteDrag(vp)
		a.handleSpriteHide(vp) // right-click → hide-sprite confirm (default ON)
	}
	a.handleHotkeys() // Ctrl-chords (shouts, pos, music, screenshot...)
	if a.rehearsal {
		c.Label(vp.X+8, vp.Y+8, rehearsalBadge, ColTierYellow)
	}
	a.drawChatOverlay(vp, true, w, h) // classic layout: the chatbox is a movable slot
	a.drawOverlayChatLayer(vp)        // effects.ini layer=chat: over the chatbox (courtroom.cpp:3234-3237)
	a.drawCourtOverlays(vp, nil)      // HP bars, clocks, badges, splashes
	a.drawReactionFloats(vp)          // #2: emoji reactions rising over the stage (0-alloc when none)

	// Modal popups: the kit has no z-aware input, so the controls
	// underneath simply don't draw (and don't see clicks) — same pattern
	// as the iniswap menu. Shared with the themed path (drawCourtroomModals).
	//
	// layoutEditorArmed, not classicEdit: the same guard has to hold in BOTH passes
	// (its absence from the themed twin is what made a modal-plus-editor state an
	// unrecoverable lock), and only one predicate can hold in both without drifting.
	// See modalReturnSkippedWhileEditing for the whole rule.
	if !a.modalReturnSkippedWhileEditing() && a.drawCourtroomModals(w, h) {
		return // skipped while editing: modals are closed on entry, so the editor is never stranded
	}

	// Right column: log + music. In the new (default) layout the OOC gets its OWN always-visible
	// box under the IC log; the Legacy Developer theme keeps OOC as a tab (its old handling).
	// Anchored to the DEFAULT stage spot (vpDef), not the moved viewport — each box is placed
	// independently, so dragging the stage doesn't drag the column with it. The whole column is a
	// slot ("rightcol"), draggable/resizable on BOTH the default and Legacy themes.
	rx := vpDef.X + vpDef.W + pad
	rw := w - rx - pad
	if !a.panelHidden(panelLog) {
		rcolDef := sdl.Rect{X: rx, Y: vpDef.Y, W: rw, H: vpDef.H}
		rcol := a.slotRect(slotRightCol, rcolDef, w, h)
		// OOC lives in its own box by default; the Legacy theme — or the opt-in
		// "OOC in the log tab" toggle (the old layout, set in the layout editor) —
		// instead routes the whole column to the tabbed log with an OOC tab.
		if a.d.Prefs.Load().LegacyDevTheme || a.d.Prefs.Load().OOCInLogTab {
			a.drawLogPanel(rcol, vpDef)
		} else {
			a.drawCleanRightColumn(rcol, vpDef, w, h)
		}
	}

	// Bottom: IC input, emotes, controls — anchored to the default stage spot so moving the
	// viewport doesn't drag them (they become their own slots in a later slice).
	a.drawICControls(w, h, vpDef)

	// Compact hover toolbox (#27/A1): slim bottom-right grip → Theater / Edit /
	// Hide-UI icon chips on hover or pin. Normal play only — the editor shows its
	// own full chip strip. The pinned per-piece panel is NOT drawn here: it must
	// draw with real input, which this pass can't give it — boxFencesPointer runs
	// the whole courtroom pointer-blind while the cursor is over the panel, so its
	// checkboxes/scrollbar/Close would be dead here. It draws post-courtroom in
	// app.go (alongside drawFloatingPanels, where input is restored) instead.
	// Unconditional: the `!a.classicEdit` gate that used to stand here now lives in
	// the latch (fenceCompactToolbox), so the fence published at the top of this pass
	// and the draw can no longer disagree when a hotkey arms the editor mid-pass.
	a.drawCompactToolboxInPass()

	// Live slot editor overlay (default layout only) — drawn LAST so it sits over every widget.
	// Torn-off tab panels draw HERE while editing (before the editor) so slotRect registers them
	// and the editor hands them drag/resize handles this same frame; their content shows (inert)
	// so you see what you're arranging. In normal play they draw post-courtroom (app.go) where the
	// content is interactive and the pointer is fenced over them. (torntabs.go)
	//
	// The strip is painted just BEFORE it, replaying the frame's paint-site latch
	// (chrometop.go) rather than riding the classicEdit flag: this pass force-stops the
	// classic editor when a themed layout takes over, and the latch — taken at the top,
	// while the flag was still set — is what keeps the strip painted exactly once even
	// on that frame. App.Frame's over-everything site replays the same latch.
	a.drawTabBarInPass(w, h)
	if a.classicEdit {
		a.drawTornTabs(w, h)
		a.drawMessagesSlotGhost(w, h) // Group Chat panel: inert placeholder so the editor gives it handles
		a.drawClassicEditor(w, h)
	}
}

// drawCleanRightColumn is the new-default right column: the IC log on top and the OOC as its own
// bordered, always-visible box below (instead of Legacy's OOC-in-a-tab). The split keeps the OOC
// roughly a third of the height with a sane floor so its input never gets crushed on short windows.
func (a *App) drawCleanRightColumn(rcol sdl.Rect, vp sdl.Rect, w, h int32) {
	c := a.ctx
	oocH := rcol.H * 32 / 100
	if oocH < cleanOOCMinH {
		oocH = cleanOOCMinH
	}
	if oocH > rcol.H-cleanOOCMinH { // never starve the IC log either
		oocH = rcol.H - cleanOOCMinH
	}
	logH := rcol.H - oocH - cleanGapPx
	boxDef := sdl.Rect{X: rcol.X, Y: rcol.Y + logH + cleanGapPx, W: rcol.W, H: oocH}
	// If the OOC box is hidden, or has been dragged to its own spot (a slot override), it
	// no longer sits under the log — so the log reclaims the FULL column instead of leaving
	// a dead gap where the OOC used to be (playtest: unpinning the OOC box left the IC
	// log with permanent empty space).
	_, oocMoved := a.classicOv[slotOOC]
	oocHidden := a.panelHidden(panelOOC)
	if oocMoved || oocHidden {
		logH = rcol.H
	}
	a.drawLogPanel(sdl.Rect{X: rcol.X, Y: rcol.Y, W: rcol.W, H: logH}, vp)
	if oocHidden {
		return // the log fills the column; there's no OOC box to draw
	}
	// The OOC box is its OWN slot — drag it anywhere / resize it independently of the log.
	box := a.slotRect(slotOOC, boxDef, w, h)
	// Guard against a stale slot override (saved at a different window size / UI scale)
	// dropping the OOC box onto the log's tab strip — the "OOC overlapping the menu
	// bars" report. While it sits over the right column, keep its top below the tab row
	// so Log / Music / Areas / … stay visible and clickable.
	if box.X < rcol.X+rcol.W && box.X+box.W > rcol.X {
		if top := rcol.Y + btnH + 4; box.Y < top {
			box.H -= top - box.Y
			box.Y = top
			if box.H < cleanOOCMinH {
				box.H = cleanOOCMinH
			}
		}
	}
	c.Fill(box, a.partPanelOr(partOOC, ColPanel)) // per-part tint
	c.Border(box, ColAccent)
	// Titled header bar so it reads as a clean, distinct box (brighter label = legible at a glance).
	hdr := sdl.Rect{X: box.X + 1, Y: box.Y + 1, W: box.W - 2, H: cleanHeaderH}
	c.Fill(hdr, ColPanelHi)
	c.Label(hdr.X+7, hdr.Y+3, "OOC", ColText)
	a.drawOOCPanel(sdl.Rect{
		X: box.X + boxBodyInsetPx,
		Y: box.Y + cleanHeaderH + boxHeaderGapPx,
		W: box.W - 2*boxBodyInsetPx,
		H: box.H - cleanHeaderH - boxHeaderGapPx - cleanOOCBottomInsetPx,
	}, true)
}

// courtroomModal is one return-to-top courtroom popup: the flag that opens it, the
// draw that paints it, and the close that dismisses it. A TABLE rather than a switch
// so the "is one open?" question and the "draw it" action are literally the same list
// — the fence a later-drawn occluder publishes has to agree with whether the pass
// reaches its draw at all, and a second hand-maintained copy of this set is exactly
// how the two drift (a fence over pixels nothing painted eats clicks, the mirror of
// the leak the fence exists to stop).
//
// `close` joins them for the same reason. The layout editors have to shut every one
// of these before they arm — a modal that survives ends the courtroom pass at
// drawCourtroomModals, so the editor's own overlay never draws — and the hand-listed
// copy of the set they used to close had drifted three entries behind this table
// (showTimer, pairPopupOpen and showSfxBrowser were all missing), which is a HARD
// LOCK: no Done chip, no banner, and a modal whose own kit buttons the editor's
// input fence has already killed.
type courtroomModal struct {
	open  func(*App) bool
	draw  func(*App, int32, int32)
	close func(*App)
}

// courtroomModals is that list, in the switch's original priority order. Package
// level with plain function values, so walking it allocates nothing per frame (the
// compactToolboxChips precedent).
// The Timer, the auto-login dialog and the click-to-pair popup USED to be rows here
// and are non-blocking floatWin panels now (#31: "bringing up the Extras → Timer
// hides all the other UI"). A row in this table ends the courtroom pass outright, so
// a countdown, a two-field credential form and a UID box each blanked the scene, the
// logs, the music list and every button while they were open. They are drawn from
// drawFloatingPanels instead, and the courtroom stays live behind them.
var courtroomModals = []courtroomModal{
	{func(a *App) bool { return a.showIni }, (*App).drawIniswapPanel, func(a *App) { a.showIni = false }},
	{func(a *App) bool { return a.bgPick.show }, (*App).drawBgPanel, func(a *App) { a.bgPick.show = false }},
	{func(a *App) bool { return a.showSfxBrowser }, (*App).drawSfxBrowser, func(a *App) { a.showSfxBrowser = false }}, // #12 SFX Browser (preview + favourites)
}

// closeCourtroomModals shuts EVERY return-to-top courtroom popup by walking the one
// table that defines the set. Both layout editors call it as they arm.
//
// Exhaustive BY CONSTRUCTION rather than by hand: a new row added to courtroomModals
// is closed by the editors the moment it exists, so the set that can strand an editor
// and the set the editors clear can never diverge again.
func (a *App) closeCourtroomModals() {
	for i := range courtroomModals {
		courtroomModals[i].close(a)
	}
}

// modalReturnSkippedWhileEditing reports whether the courtroom pass must SKIP its
// return-to-top modal check this frame because a layout editor owns the screen.
//
// Both courtroom passes end at `if drawCourtroomModals(...) { return }`, and both
// editors' overlays draw at the very END of their pass — so an open modal deletes the
// editor from the frame entirely. That is not merely cosmetic: the editor's input
// fence (c.modalOn) has already made every kit widget in the frame inert, so the
// modal that took the screen cannot be clicked away either, the menu bar and the
// server-tab strip have both stood down for the editor, and the editor's own Esc
// handler lives inside the draw that never ran. Nothing on screen answers.
//
// The classic pass has carried this guard since the editor existed; the themed twin
// never did. Sharing ONE predicate is the point — a guard that holds in one pass and
// not the other is exactly how the lock was reachable.
func (a *App) modalReturnSkippedWhileEditing() bool { return a.layoutEditorArmed() }

// courtroomModalUp reports whether drawCourtroomModals will take the screen — the
// predicate half of the table above, for anything that must decide BEFORE the pass
// reaches the modal check (the compact toolbox's overlay fence).
//
// Deliberately NOT blockingCourtPopup (floatbox.go): that set is a different
// question (which popups the Extras box and torn-off tabs yield to), it OMITS
// showSfxBrowser, and it ADDS classicEdit. Reusing it would fence a band of the SFX
// browser dead and mis-answer while the editor is armed.
func (a *App) courtroomModalUp() bool {
	for i := range courtroomModals {
		if courtroomModals[i].open(a) {
			return true
		}
	}
	return false
}

// drawCourtroomModals draws whichever return-to-top courtroom popup is open and
// reports whether one took the screen — the caller then returns, because the
// kit has no z-aware input so nothing underneath should draw or take clicks.
// SHARED by the classic and themed courtroom so the two lists can't drift: the
// background picker once drew in the classic path but was missing from the
// themed switch, so on a custom theme the Extras → Background button opened a
// picker that never rendered. (showPair is NOT here — it's a non-returning
// overlay drawn on top in both paths.)
func (a *App) drawCourtroomModals(w, h int32) bool {
	for i := range courtroomModals {
		if courtroomModals[i].open(a) {
			courtroomModals[i].draw(a, w, h)
			return true
		}
	}
	return false
}

// handleVpDivider lets the user drag the viewport's right edge to resize the
// viewport↔log split (vpPct) — the mouse alternative to the View knob. Called
// BEFORE the zoom/sprite-drag handlers so a grab on the edge wins, and only in
// drag-resize mode (the default; the knob panel covers it when off). It scales
// the whole 4:3 viewport, so past the height clamp the edge stops tracking the
// mouse (the grip then sits "stuck" at the limit — expected, not a bug). Reuses
// the View knob's own clamp so the two never disagree.
func (a *App) handleVpDivider(vp sdl.Rect, w int32) {
	c := a.ctx
	if !a.d.Prefs.Load().DragLayout || a.vpZoom > 1 || a.courtModalOpen() || a.viewportOverridden() { // off, zoomed, a blocking popup, or the stage has an editor size override (the divider would change the now-shadowed vpPct)
		a.dragVpDivider = false
		a.dividerPrevDwn = c.mouseDown
		return
	}
	handle := sdl.Rect{X: vp.X + vp.W - 4, Y: vp.Y, W: 12, H: vp.H}
	hot := c.hovering(handle)
	grip := ColPanelHi
	if hot || a.dragVpDivider {
		grip = ColAccent
	}
	c.Fill(sdl.Rect{X: vp.X + vp.W - 1, Y: vp.Y + vp.H/2 - 24, W: 3, H: 48}, grip) // a subtle edge grip
	pressed := c.mouseDown && !a.dividerPrevDwn
	a.dividerPrevDwn = c.mouseDown
	if pressed && hot {
		a.dragVpDivider = true
	}
	if !c.mouseDown {
		if a.dragVpDivider {
			a.saveLayout() // persist the new width on release
		}
		a.dragVpDivider = false
		return
	}
	if a.dragVpDivider && w > 0 {
		// Grabbing the edge is a deliberate "size it freely" gesture, so it takes
		// back control from an exact-px pin (else the drag would silently no-op
		// against the shadowing fixed size).
		if a.d.Prefs.Load().ViewportExactW != 0 {
			a.d.Prefs.SetViewportExactWidth(0)
		}
		pct := int(int64(c.mouseX-vp.X) * int64(DefaultScalePct) / int64(w))
		if pct < config.MinViewportPercent {
			pct = config.MinViewportPercent
		}
		if pct > config.MaxViewportPercent {
			pct = config.MaxViewportPercent
		}
		a.vpPct = pct
	}
}

// spriteHitRect mirrors Viewport.drawSprite's placement math so drags
// hit-test exactly what's drawn. Runs only on press/right-click edges.
func (a *App) spriteHitRect(vp sdl.Rect, layer *courtroom.SpriteLayer) (sdl.Rect, bool) {
	if !layer.Visible || layer.Active == "" {
		return sdl.Rect{}, false
	}
	page, ok := a.d.Store.Get(layer.Active)
	if !ok || page.H == 0 {
		return sdl.Rect{}, false
	}
	scaledW := vp.H * page.W / page.H
	return sdl.Rect{
		X: vp.X + (vp.W-scaledW)/2 + vp.W*int32(layer.OffsetX)/100,
		Y: vp.Y + vp.H*int32(layer.OffsetY)/100,
		W: scaledW,
		H: vp.H,
	}, true
}

// handleSpriteDrag implements client-side sprite repositioning: grab any
// character in the viewport and put them wherever you want — the server
// keeps setting positions per message, the override (keyed by character)
// wins afterwards. Right-click a sprite to reset it. All math runs on
// press/drag edges only; idle frames cost one bool check.
func (a *App) handleSpriteDrag(vp sdl.Rect) {
	if a.dragVpDivider || a.courtModalOpen() {
		return // the resize divider owns this press, or a blocking popup fences the stage
	}
	if !a.d.Prefs.Load().SpriteMoveOn {
		return // opt-in (Settings → General); off by default so stray clicks can't nudge sprites
	}
	c := a.ctx
	sc := &a.room.Scene
	pressed := c.mouseDown && !a.prevDown
	a.prevDown = c.mouseDown
	if !c.mouseDown {
		a.dragName = "" // release keeps the override, ends the drag
	}

	// Front-most first, matching render z-order; the chat box area is
	// the text's, not the sprites'.
	boxH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	inChatBox := c.mouseY >= vp.Y+vp.H-boxH
	layers := [2]*courtroom.SpriteLayer{&sc.Speaker, &sc.Pair}
	if sc.PairActive && !sc.SpeakerInFront {
		layers[0], layers[1] = &sc.Pair, &sc.Speaker
	}

	if pressed && c.hovering(vp) && !inChatBox && a.dragName == "" {
		for _, layer := range layers {
			if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) && layer.Name != "" {
				if _, have := a.spriteOv[strings.ToLower(layer.Name)]; !have && len(a.spriteOv) >= spriteOvCap {
					break // table full; never unbounded
				}
				a.dragName = strings.ToLower(layer.Name)
				a.dragStart = [2]int32{c.mouseX, c.mouseY}
				a.dragBase = [2]int{layer.OffsetX, layer.OffsetY}
				break
			}
		}
	}

	if a.dragName != "" && c.mouseDown && vp.W > 0 && vp.H > 0 {
		dx := int(c.mouseX-a.dragStart[0]) * 100 / int(vp.W)
		dy := int(c.mouseY-a.dragStart[1]) * 100 / int(vp.H)
		a.spriteOv[a.dragName] = [2]int{
			clampOffset(a.dragBase[0] + dx),
			clampOffset(a.dragBase[1] + dy),
		}
	}

	// Right-click a sprite: drop its move override (back to server placement) —
	// but only when right-click-to-hide is OFF, so the two don't both fire.
	if c.rightClicked && !a.d.Prefs.Load().RightClickHideSprite && len(a.spriteOv) > 0 && c.hovering(vp) && !inChatBox {
		for _, layer := range layers {
			if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) {
				delete(a.spriteOv, strings.ToLower(layer.Name))
				break
			}
		}
	}
}

// requestDisconnect is what the user-facing Disconnect buttons call: it acts at
// once when "instant disconnect" is set, otherwise opens a confirm modal (the
// button is easy to hit by accident). Internal disconnects — a closed tab, a
// dropped connection, auto-reconnect teardown — keep calling Disconnect() direct.
func (a *App) requestDisconnect() {
	if a.d.Prefs.Load().InstantDisconnect {
		a.deliberateClose = true // user chose to leave — the drop paths must not auto-reconnect (#1)
		a.Disconnect()
		return
	}
	a.confirmDisconnect = true
}

// drawDisconnectConfirm paints the "are you sure" overlay: a dimmed screen + a
// centered modal warning that Yes leaves the server, with Yes / Cancel and a note
// about the instant-disconnect setting. Drawn top-level (Frame) so it works from
// any Disconnect button; the pointer is fenced around it so clicks can't reach the
// screen behind. Off the render hot path (only while open).
func (a *App) drawDisconnectConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 480, 176
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Disconnect from the server?", ColText)
	c.Label(m.X+pad, m.Y+50, "Yes will disconnect you from the server (you'll return to the lobby).", ColText)
	// "Don't ask again" reuses the Quit-modal pattern: it ticks the existing
	// Instant-disconnect pref, so future Disconnects — the button AND Esc, both
	// routed through requestDisconnect — skip straight through. Untickable here too.
	inst := a.d.Prefs.Load().InstantDisconnect
	if next := c.Checkbox(m.X+pad, m.Y+78, "Don't ask again (disconnect instantly from now on)", inst); next != inst {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				InstantDisconnect =

				next

		})

	}
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 170, H: btnH}, "Yes, disconnect") {
		a.confirmDisconnect = false
		a.deliberateClose = true // user confirmed leaving — suppress auto-reconnect on this teardown (#1)
		a.Disconnect()
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.confirmDisconnect = false
	}
}

// drawCloseTabConfirm paints the "close this tab?" overlay: the sibling of
// drawDisconnectConfirm for a manual background-tab ✕ (requestCloseTab). Same
// layout, fence discipline (drawn top-level, pointer restored just before it, one
// modal at a time), and "Don't ask again" → Instant-disconnect pref — but its own
// copy naming the server, and Yes closes just THIS tab (confirmPendingCloseTab)
// instead of the live session. You stay on whatever screen you're on, so the copy
// deliberately does NOT say "return to the lobby" (unlike the disconnect modal).
// Off the render hot path (only while a close is pending).
func (a *App) drawCloseTabConfirm(w, h int32) {
	c := a.ctx
	t := a.pendingCloseTab
	if t == nil {
		return // defensive: the outer guard only draws this while pending
	}
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 480, 176
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	// The server name is variable-length and drawn on a fixed-width panel, so clip
	// it (LabelClipped) — an over-long name must not spill past the border onto the
	// scene behind (the §3.4 class of bug that just landed one commit upstream).
	name := t.state.serverName
	if name == "" {
		name = "this server"
	}
	c.Heading(m.X+pad, m.Y+pad, "Close this tab?", ColText)
	c.LabelClipped(m.X+pad, m.Y+50, mw-2*pad, "Close and disconnect from "+name+"? You'll stay where you are.", ColText)
	// "Don't ask again" reuses the disconnect modal's pattern: it ticks the same
	// Instant-disconnect pref, so future tab-✕ clicks (AND the Disconnect button /
	// Esc) skip straight through. Untickable here too.
	inst := a.d.Prefs.Load().InstantDisconnect
	if next := c.Checkbox(m.X+pad, m.Y+78, "Don't ask again (close/disconnect instantly from now on)", inst); next != inst {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				InstantDisconnect =

				next

		})

	}
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 170, H: btnH}, "Yes, close tab") {
		a.confirmPendingCloseTab() // revalidates the pointer's current index, then closeParkedTab
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.pendingCloseTab = nil
	}
}

// requestQuit asks to close AsyncAO: straight to quit if the user ticked "don't
// ask again", otherwise the confirm dialog. The Esc-in-lobby escape hatch (you
// can't always reach the window's X in fullscreen) routes through here.
func (a *App) requestQuit() {
	if a.d.Prefs.Load().QuitConfirmSkip {
		a.doQuit()
		return
	}
	a.showQuitConfirm = true
}

// doQuit pushes an SDL quit event, which the main loop drains to shut down
// cleanly (prefs flush, etc.) — same path as the window's close button.
func (a *App) doQuit() {
	_, _ = sdl.PushEvent(&sdl.QuitEvent{Type: sdl.QUIT})
}

// drawQuitConfirm is the "Quit AsyncAO?" modal (Esc in the lobby): Quit / Cancel
// plus a "Don't ask again" tick. Drawn top-level so it fences the screen behind.
func (a *App) drawQuitConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 460, 184
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Quit AsyncAO?", ColText)
	c.Label(m.X+pad, m.Y+50, "Close the AsyncAO window?", ColText)
	skip := a.d.Prefs.Load().QuitConfirmSkip
	if next := c.Checkbox(m.X+pad, m.Y+82, "Don't ask again", skip); next != skip {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.QuitConfirmSkip = next })
	}
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 120, H: btnH}, "Quit") {
		a.showQuitConfirm = false
		a.doQuit()
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.showQuitConfirm = false
	}
}

// handleSpriteHide opens the "hide this sprite?" confirm when the user right-clicks
// a character sprite (default ON; Settings can disable). Hidden sprites are dropped
// from the viewport for the session — for players who'd rather not see certain art.
// Only at 1× zoom (zoomed hit rects lie) and not over the chatbox area.
func (a *App) handleSpriteHide(vp sdl.Rect) {
	c := a.ctx
	if !c.rightClicked || a.room == nil || !a.d.Prefs.Load().RightClickHideSprite || a.courtModalOpen() {
		return // a blocking popup fences the stage
	}
	boxH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	if !c.hovering(vp) || c.mouseY >= vp.Y+vp.H-boxH {
		return
	}
	sc := &a.room.Scene
	layers := [2]*courtroom.SpriteLayer{&sc.Speaker, &sc.Pair}
	if sc.PairActive && !sc.SpeakerInFront {
		layers[0], layers[1] = &sc.Pair, &sc.Speaker
	}
	for _, layer := range layers {
		if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) && layer.Name != "" {
			a.hidePrompt = layer.Name // open the confirm for this sprite
			break
		}
	}
}

// drawHideSpriteConfirm paints the "hide this sprite?" modal. Yes hides it for the
// session; the pointer is fenced around it (Frame) so the click can't reach the
// courtroom behind. Off the render hot path (only while open).
func (a *App) drawHideSpriteConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 520, 184
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Hide this sprite?", ColText)
	c.Label(m.X+pad, m.Y+50, "Hide \""+a.hidePrompt+"\" from the viewport for this session?", ColText)
	c.Label(m.X+pad, m.Y+74, "Reshow all with the \"Reshow hidden sprites\" key, or turn this off in Settings → General.", ColTextDim)
	if c.Button(sdl.Rect{X: m.X + pad, Y: m.Y + mh - btnH - pad, W: 150, H: btnH}, "Hide it") {
		if a.hiddenSprites == nil {
			a.hiddenSprites = make(map[string]struct{}, 4)
		}
		a.hiddenSprites[strings.ToLower(a.hidePrompt)] = struct{}{}
		a.hidePrompt = ""
		return
	}
	if c.Button(sdl.Rect{X: m.X + mw - pad - 110, Y: m.Y + mh - btnH - pad, W: 110, H: btnH}, "Cancel") {
		a.hidePrompt = ""
	}
}

// reshowSprites un-hides every sprite hidden this session (the Reshow key + the
// Settings button).
func (a *App) reshowSprites() {
	if len(a.hiddenSprites) == 0 {
		return
	}
	a.hiddenSprites = nil
	a.warnLine = "Hidden sprites are showing again."
	a.warnAt = time.Now()
}

// chatBoxTopStrip is the showname strip above the message text (text draws at box.Y+chatBoxTopStrip);
// chatBoxBottomPad leaves a little air under the last line. Used to grow the box to fit its message.
//
// chatOverlayPadX / chatOverlayNameY name the rest of the classic overlay's inset,
// which drawChatOverlay used to spell as bare 8 / 4 / 16 literals sprinkled across a
// dozen expressions (hard rule 9). VALUES ARE UNCHANGED — this is naming only, and
// the classic box draws at the exact pixels it drew before.
//
// There WAS a chatOverlayBoldNudge here — the 1 px right shift of a faux-bold second
// draw pass. It is gone (F1b): weight belongs in the glyphs, not in a second blit,
// because a LOGICAL pixel offset is multiplied by the UI scale and the two copies
// then sit on different sub-pixel phases. See Ctx.textTextureBold.
//
// These are AsyncAO's OWN furniture, not AO2 geometry: the classic overlay
// invents a chatbox where the theme declares none, so nothing here is a
// courtroom_design.ini rect. They double as the themed chatbox's fallback for a
// theme that declares an ao2_chatbox but no showname / message child (see
// chatboxfit.go), which is the role they always played.
const (
	chatBoxTopStrip  = int32(26)
	chatBoxBottomPad = int32(8)
	// chatOverlayPadX is the left/right gutter; the usable width loses 2× it.
	chatOverlayPadX = int32(8)
	// chatOverlayNameY is the showname row's offset from the box's top edge.
	chatOverlayNameY = int32(4)
)

// grownChatBoxH is the chatbox height needed to show `lines` lines of `lineH`-tall text in full
// (plus the showname strip + bottom pad) — never below baseH (the MsgBox-knob band) and never above
// 3/5 of the stage height vpH. Pins the "grow to fit so a resized/long message isn't cut off at the
// bottom" rule; drawChatOverlay applies it to the flat fallback panel (a theme's own skin keeps its
// authored size). Pure + unit-tested.
func grownChatBoxH(baseH, vpH, lines, lineH int32) int32 {
	needH := chatBoxTopStrip + lines*lineH + chatBoxBottomPad
	if maxH := vpH * 3 / 5; needH > maxH {
		needH = maxH
	}
	if needH < baseH {
		return baseH
	}
	return needH
}

// text returns the cached label, rebuilding it only when a count moves.
func (m *shownTotalMemo) text(shown, total int) string {
	if m.label == "" || m.shown != shown || m.total != total {
		m.label = fmt.Sprintf("%d / %d", shown, total)
		m.shown, m.total = shown, total
	}
	return m.label
}

func (m *nowPlayingMemo) text(track string) string {
	if m.label == "" || m.track != track {
		m.label = musicNowPrefix + courtroom.MusicDisplayName(track)
		m.track = track
	}
	return m.label
}
