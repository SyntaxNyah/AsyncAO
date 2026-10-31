package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
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
	// Every character previews at the same height (config.Settings.PreviewHeightPx); the corner grip resizes per
	// session within [previewMinH, previewMaxH]. previewMaxW / previewMinW keep extreme aspects on screen.
	previewMinH     int32 = config.MinPreviewHeightPx
	previewMaxH     int32 = config.MaxPreviewHeightPx
	previewMaxW     int32 = 960 // fits 4:3 art at the 720 px height cap (was 560; the window clamp still applies)
	previewMinW     int32 = 48
	previewCaptionH int32 = 20
	// previewNameStripH is the top-of-box emote-name caption band, one text row high.
	previewNameStripH int32 = 18
	// emoteBtnCell matches AO2's 40×40 emotions/button<N> art.
	emoteBtnCell int32 = 40
	// emoteTextCellW is the fixed cell width for text-mode emote chips, so they page in the same grid as image chips.
	emoteTextCellW int32 = 104
	// scrollBarW/Gap reserve the scrollbar lane beside scrolling lists.
	scrollBarW   int32 = 12
	scrollBarGap int32 = 6
	// cleanOOCMinH floors the new-layout OOC box and the IC log above it; cleanGapPx is the gap between stacked panels.
	cleanOOCMinH int32 = 96
	cleanGapPx   int32 = 6
	cleanHeaderH int32 = 20 // titled-header bar height on clean-layout boxes
	// boxBodyInsetPx is the side inset a titled, bordered box gives its body, clearing the outline and the header bar's
	// frame.
	boxBodyInsetPx int32 = 5
	// boxHeaderGapPx separates a titled box's header bar from the body.
	boxHeaderGapPx int32 = 4
	// cleanOOCBottomInsetPx is the clean OOC box's bottom inset, a pixel tighter than boxBodyInsetPx; unifying them
	// moves pixels.
	cleanOOCBottomInsetPx int32 = 4
	// courtBottomFurnitureH is the height the classic courtroom keeps below the stage for the IC input, control rows
	// and emote grid; the stage is capped so it can't eat that band.
	courtBottomFurnitureH int32 = 220
)

// The lobby header's right-hand utility cluster, laid out by a right-to-left cursor so it stays on screen at
// config.MinWindowW.
// The Phone Book toggle stays here: its label flips to "← All servers", and it is the phone-book page's only way back.
const (
	// lobbyUtilBtnW is the width when the window has room for the whole cluster.
	lobbyUtilBtnW int32 = 110
	// lobbyUtilBtnGap separates two buttons of the cluster.
	lobbyUtilBtnGap int32 = 10
	// lobbyUtilBtnMinW is the floor the shared-width shrink stops at, so a squeezed button still shows a few glyphs.
	lobbyUtilBtnMinW int32 = 64
	// lobbyUtilBtnCount must match the number of cursor steps in drawLobby. More buttons would squeeze the shared
	// header row to nothing at config.MinWindowW, stranding What's New, so Refresh and Sort sit on their own strip.
	lobbyUtilBtnCount int32 = 1
	// lobbyDirectRowDropY is how far below the header the direct-connect row sits; lobbyListActionsDropY how far below
	// that the Refresh / Sort strip sits; lobbyListActionW each button's width there. Named so lobbyListTop is the
	// single source.
	lobbyDirectRowDropY   int32 = 56
	lobbyListActionsDropY int32 = 34
	lobbyListActionW      int32 = 110
	// lobbyListActionsGapY is the gap under the action strip.
	lobbyListActionsGapY int32 = 8
	// directConnectFieldID is named because the Servers menu focuses it from outside this file.
	directConnectFieldID = "direct"
)

// The direct-connect row's sizes; Connect is the row's whole point, so it is the one control that may never leave the
// window.
const (
	// dcFieldWMax is the input's comfortable width; dcFieldWMin is the squeeze floor, enough for "127.0.0.1:50001".
	dcFieldWMax int32 = 280
	dcFieldWMin int32 = 120
	// dcGapPx is the gap between any two controls of the row.
	dcGapPx int32 = 10
	// dcConnectW is the Connect button's width.
	dcConnectW int32 = 100
	// dcLabelFull / dcLabelShort are the row's caption; a squeezed window gets the short one instead of losing the
	// button.
	dcLabelFull  = "Direct connect (ip:port, url:port, ws:// or wss://):"
	dcLabelShort = "Direct connect:"
)

// previewOwnerMark is the mark for pushOverlayOwner: our publication, else the current registry depth, which suspends
// nothing. A zero mark would suspend every rect in the registry.
func (a *App) previewOwnerMark() int {
	if a.previewFence.on {
		return a.previewFence.mark
	}
	return a.ctx.overlayFenceMark()
}

// handlePreviewInput drives the preview box's wheel zoom and drag. It runs before any screen draws, against last
// frame's box rect, so it claims input ahead of the widgets the box overlaps.
func (a *App) handlePreviewInput() {
	c := a.ctx
	// Before either early return: the detached window must keep receiving frames and get its close reconciled when the
	// in-app box doesn't draw.
	a.syncPreviewWindow()
	// Clear before any early return, so a pass that publishes nothing can't replay a courtroom frame's stale mark into
	// pushOverlayOwner.
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
	// Claim the wheel so the list or grid under the box doesn't also scroll.
	if c.wheelY != 0 && c.hovering(box) {
		a.previewZoom = int(value.Clamp(int32(a.previewZoom)+c.wheelY, 1, previewZoomMax))
		c.wheelY = 0
		c.wheelTaken = true
	}
	// Corner-grip resize: drag vertically to change the height; width follows the art's aspect on the next draw. Claims
	// the press before the body drag below.
	grip := previewGripRect(box)
	if c.mouseDown && !a.previewResize && !a.previewDrag && c.hovering(grip) {
		a.previewResize = true
		a.previewResizeFrom = c.mouseY
		a.previewResizeBase = a.previewUserH
		if a.previewResizeBase == 0 {
			a.previewResizeBase = int32(a.d.Prefs.Load().PreviewHeightPx()) // grip starts from the Settings default
		}
	}
	if a.previewResize {
		if c.mouseDown {
			a.previewUserH = value.Clamp(a.previewResizeBase+(c.mouseY-a.previewResizeFrom), previewMinH, previewMaxH)
		} else {
			a.previewResize = false
			c.clicked = false // a finished resize isn't a dismiss/selection
		}
		return // resizing owns the pointer — no move-drag underneath
	}
	// Left-drag to reposition, starting only on the body. Chrome buttons are excluded: a press on one has c.clicked
	// false until release, so it would start a drag and return above with previewDrag still set.
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
			// A press that started on the box owns its release, moved or not; otherwise the dismissing click also lands
			// on what the box floats over, such as a music row.
			if !a.previewDragMoved && !a.previewIsPinned() {
				a.closeSpritePreview()
			}
			c.clicked = false
			a.previewDrag = false
		}
	}
}

// drawPreviewZoom draws the bottom caption strip (source resolution and scale) with the − / + buttons, whose clicks are
// consumed so click-to-dismiss doesn't fire.
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

// drawPreviewEmoteNav draws the [<] caption [>] bar above the preview and handles its clicks and Left/Right keys. Arrow
// clicks are consumed so click-to-dismiss doesn't fire.
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

func (a *App) drawCourtroom(w, h int32) {
	c := a.ctx
	// The registry is rebuilt from this frame's draws while editing, so a piece hidden mid-session stops showing
	// handles.
	if a.classicEdit {
		clear(a.slotReg)
	}
	// Before the pointer block below, which walks a.classicOv and a.extrasDetached; otherwise frame one blocks a stale
	// set. Both are memoized, so this stays alloc-free.
	a.ensureClassicOv()
	a.reconstructTornWidgets(w, h)
	// While the pointer is over the Extras box, this pass runs pointer-blind so a click can't also land on the scene or
	// log.
	if a.boxFencesPointer(w, h) {
		c.fencePointer()
	}
	defer c.unfencePointer()
	// Rects published during this pass release when it returns, because the post-courtroom overlays paint above the
	// toolbox and must not inherit them. A mark, not a clear, lets the menu bar outlive this pass.
	fenceMark := c.overlayFenceMark()
	defer c.overlayFenceRelease(fenceMark)
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	// Only drawCourtroomThemed re-arms this; a stale true would misplace the classic or theater toolbox, which draws
	// post-court in app.go.
	a.toolboxThemeRectOn = false
	// Only fenceCompactToolbox sets this, so a theater, dropped-session or lobby frame can't replay last frame's
	// answer.
	a.toolboxFence = compactToolboxLatch{}
	// Decided once here and replayed by both paint sites (chrometop.go). It must precede anything that arms or disarms
	// an editor mid-pass, or the strip paints twice or not at all.
	a.latchTabStripPaint()
	a.pollCharINI()
	if a.room == nil || a.sess == nil {
		if a.classicEdit {
			a.stopClassicEdit() // session dropped mid-edit — release the fence so the lobby isn't stuck
		}
		a.screen = ScreenLobby
		return
	}

	// Theater mode preempts both layouts.
	if a.theaterOn {
		if a.classicEdit {
			a.stopClassicEdit() // can't edit the layout from theater mode — release the fence
		}
		a.drawTheater(w, h)
		return
	}

	// When the theme ships courtroom_design.ini and the toggle is on, the courtroom is the theme's layout.
	// themeWindowLayout fits the canvas under the app-chrome band.
	if lay := a.themeWindowLayout(w, h); lay.valid && a.d.Prefs.Load().ThemeLayoutOn {
		if a.classicEdit {
			a.stopClassicEdit() // a themed layout took over; classic edit is default-only
		}
		a.drawCourtroomThemed(w, h, lay)
		return
	}
	// The themed editor only exists over themed geometry; release it if the theme or toggle went away mid-edit.
	if a.layoutEdit {
		a.stopLayoutEdit()
	}
	// Classic editor: block the pointer before any widget draws so clicks reach the editor; its overlay draws last.
	a.classicEditFence()
	// Must sit below the theme dispatch: compactToolboxStripRect consults a.toolboxThemeRectOn, so publishing earlier
	// would block the default rect under a theme that parks the toolbox elsewhere. drawCompactToolboxInPass replays
	// this decision.
	a.fenceCompactToolbox(w, h)
	// After the toolbox: it paints above the preview box, so its mark-scoped owner-suspend must cover this publication.
	a.fenceSpritePreview()

	// The app-chrome band owns the top of the window; the classic courtroom lays out below it.
	top := a.topChromeH()
	vpW := w * int32(a.vpPct) / DefaultScalePct
	vpH := vpW * 3 / 4
	if vpH > h-top-courtBottomFurnitureH {
		vpH = h - top - courtBottomFurnitureH
		vpW = vpH * 4 / 3
	}
	// Exact-px sizing overrides the % knob, since integer multiples of the 256×192 art stay crisp. If it doesn't fit,
	// snap down to the largest multiple that does; clamping to raw px would blur.
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
	// With no override the View knob and divider own the 4:3 size; a viewport handle drag in the editor wins until
	// reset.
	vp := a.slotRect(slotViewport, vpDef, w, h)
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.renderViewportZoomed(vp)
	a.drawStageRecordButton(vp)
	a.handleVpDivider(vp, w) // drag the right edge to resize (claims the press BEFORE zoom/sprite-drag)
	// Camera input first; sprite drag only at 1×, because zoomed hit rects would be wrong.
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

	// The kit has no z-aware input, so controls under a modal simply don't draw. Skipped for either layout editor; see
	// modalReturnSkippedWhileEditing.
	if !a.modalReturnSkippedWhileEditing() && a.drawCourtroomModals(w, h) {
		return // skipped while editing: modals are closed on entry, so the editor is never stranded
	}

	// Right column: anchored to the default stage spot (vpDef), so dragging the stage doesn't drag the column.
	rx := vpDef.X + vpDef.W + pad
	rw := w - rx - pad
	if !a.panelHidden(panelLog) {
		rcolDef := sdl.Rect{X: rx, Y: vpDef.Y, W: rw, H: vpDef.H}
		rcol := a.slotRect(slotRightCol, rcolDef, w, h)
		// OOC gets its own box unless the Legacy theme or the "OOC in the log tab" toggle routes it to a log tab.
		if a.d.Prefs.Load().LegacyDevTheme || a.d.Prefs.Load().OOCInLogTab {
			a.drawLogPanel(rcol, vpDef)
		} else {
			a.drawCleanRightColumn(rcol, vpDef, w, h)
		}
	}

	// Bottom controls are anchored to the default stage spot so moving the viewport doesn't drag them.
	a.drawICControls(w, h, vpDef)

	// The pinned per-piece panel can't draw here: boxFencesPointer runs this pass pointer-blind while the cursor is
	// over it, so it draws post-courtroom in app.go.
	a.drawCompactToolboxInPass()

	// Torn-off tabs draw here while editing, so slotRect registers them and the editor gives them handles this frame;
	// in normal play they draw post-courtroom.
	// The strip replays the paint decision taken at the top, because this pass can force-stop the classic editor.
	a.drawTabBarInPass(w, h)
	if a.classicEdit {
		a.drawTornTabs(w, h)
		a.drawMessagesSlotGhost(w, h) // Group Chat panel: inert placeholder so the editor gives it handles
		a.drawClassicEditor(w, h)
	}
}

// drawCleanRightColumn draws the IC log with the OOC as its own box below, about a third of the height with a floor.
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
	// A hidden or moved OOC box leaves the column free, so the log reclaims all of it.
	_, oocMoved := a.classicOv[slotOOC]
	oocHidden := a.panelHidden(panelOOC)
	if oocMoved || oocHidden {
		logH = rcol.H
	}
	a.drawLogPanel(sdl.Rect{X: rcol.X, Y: rcol.Y, W: rcol.W, H: logH}, vp)
	if oocHidden {
		return // the log fills the column; there's no OOC box to draw
	}
	box := a.slotRect(slotOOC, boxDef, w, h)
	// A stale override saved at another window size can drop the OOC box onto the log's tab strip; keep its top below
	// the tab row.
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

// courtroomModal is one return-to-top courtroom popup. A table, so "is one open?", "draw it" and "close it" share one
// list: a rect over pixels nothing painted eats clicks, and a modal left open locks a layout editor out of drawing.
type courtroomModal struct {
	open  func(*App) bool
	draw  func(*App, int32, int32)
	close func(*App)
}

// courtroomModals is that list in priority order; plain function values, so walking it allocates nothing. A row here
// ends the courtroom pass, so non-blocking panels belong in drawFloatingPanels instead.
var courtroomModals = []courtroomModal{
	{func(a *App) bool { return a.showIni }, (*App).drawIniswapPanel, func(a *App) { a.showIni = false }},
	{func(a *App) bool { return a.bgPick.show }, (*App).drawBgPanel, func(a *App) { a.bgPick.show = false }},
	{func(a *App) bool { return a.showSfxBrowser }, (*App).drawSfxBrowser, func(a *App) { a.showSfxBrowser = false }}, // #12 SFX Browser (preview + favourites)
}

// closeCourtroomModals shuts every popup in the table; both layout editors call it as they arm.
func (a *App) closeCourtroomModals() {
	for i := range courtroomModals {
		courtroomModals[i].close(a)
	}
}

// modalReturnSkippedWhileEditing reports that the pass must skip its modal return because a layout editor owns the
// screen.
// An open modal would remove the editor from the frame, and with kit widgets inert and the bar stood down nothing would
// answer. One predicate holds in both passes.
func (a *App) modalReturnSkippedWhileEditing() bool { return a.layoutEditorArmed() }

// courtroomModalUp reports whether drawCourtroomModals will take the screen, for decisions made before the pass reaches
// it.
// Not blockingCourtPopup (floatbox.go): that set omits showSfxBrowser and adds classicEdit, so it would answer wrongly
// here.
func (a *App) courtroomModalUp() bool {
	for i := range courtroomModals {
		if courtroomModals[i].open(a) {
			return true
		}
	}
	return false
}

// drawCourtroomModals draws the open popup and reports whether one took the screen; the caller then returns. Shared by
// both courtroom layouts.
func (a *App) drawCourtroomModals(w, h int32) bool {
	for i := range courtroomModals {
		if courtroomModals[i].open(a) {
			courtroomModals[i].draw(a, w, h)
			return true
		}
	}
	return false
}

// handleVpDivider drags the viewport's right edge to resize the viewport/log split (vpPct). Called before zoom and
// sprite drag so a grab on the edge wins. Past the height clamp the edge stops tracking the mouse.
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
		// Grabbing the edge takes control from an exact-px pin, or the drag would silently do nothing.
		if a.d.Prefs.Load().ViewportExactW != 0 {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.ViewportExactW = 0 })
		}
		pct := int(int64(c.mouseX-vp.X) * int64(DefaultScalePct) / int64(w))
		pct = value.Clamp(pct, config.MinViewportPercent, config.MaxViewportPercent)
		a.vpPct = pct
	}
}

// spriteHitRect mirrors Viewport.drawSprite's placement math, so drags hit-test what's drawn.
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

// handleSpriteDrag repositions a sprite client-side; the override, keyed by character, wins over server positions. Math
// runs on press and drag edges only.
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

	// Front-most first, matching render z-order; the chat box area belongs to the text.
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

	// Right-click drops the move override, only when right-click-to-hide is off so the two don't both fire.
	if c.rightClicked && !a.d.Prefs.Load().RightClickHideSprite && len(a.spriteOv) > 0 && c.hovering(vp) && !inChatBox {
		for _, layer := range layers {
			if r, ok := a.spriteHitRect(vp, layer); ok && c.hovering(r) {
				delete(a.spriteOv, strings.ToLower(layer.Name))
				break
			}
		}
	}
}

// requestDisconnect acts at once if "instant disconnect" is set, else opens a confirm. Internal disconnects call
// Disconnect() directly.
func (a *App) requestDisconnect() {
	if a.d.Prefs.Load().InstantDisconnect {
		a.deliberateClose = true // user chose to leave — the drop paths must not auto-reconnect (#1)
		a.Disconnect()
		return
	}
	a.confirmDisconnect = true
}

// drawDisconnectConfirm paints the confirm modal, top-level so it works from any Disconnect button.
func (a *App) drawDisconnectConfirm(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 480, 176
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Disconnect from the server?", ColText)
	c.Label(m.X+pad, m.Y+50, "Yes will disconnect you from the server (you'll return to the lobby).", ColText)
	// "Don't ask again" ticks the Instant-disconnect pref, which requestDisconnect reads.
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

// drawCloseTabConfirm paints the confirm for a manual background-tab ✕. Unlike the disconnect modal the user stays on
// their screen, so the copy must not say "return to the lobby".
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
	// The name is variable-length on a fixed-width panel, so clip it.
	name := t.state.serverName
	if name == "" {
		name = "this server"
	}
	c.Heading(m.X+pad, m.Y+pad, "Close this tab?", ColText)
	c.LabelClipped(m.X+pad, m.Y+50, mw-2*pad, "Close and disconnect from "+name+"? You'll stay where you are.", ColText)
	// "Don't ask again" ticks the same Instant-disconnect pref as the disconnect modal.
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

// requestQuit quits at once if "don't ask again" is ticked, else opens the confirm.
func (a *App) requestQuit() {
	if a.d.Prefs.Load().QuitConfirmSkip {
		a.doQuit()
		return
	}
	a.showQuitConfirm = true
}

// doQuit pushes an SDL quit event, which the main loop drains to shut down cleanly.
func (a *App) doQuit() {
	_, _ = sdl.PushEvent(&sdl.QuitEvent{Type: sdl.QUIT})
}

// drawQuitConfirm is the "Quit AsyncAO?" modal, drawn top-level.
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

// handleSpriteHide opens the hide confirm on right-clicking a sprite, only at 1× zoom and not over the chatbox.
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

// drawHideSpriteConfirm paints the hide modal; Yes hides the sprite for the session.
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

// reshowSprites un-hides every sprite hidden this session.
func (a *App) reshowSprites() {
	if len(a.hiddenSprites) == 0 {
		return
	}
	a.hiddenSprites = nil
	a.warnLine = "Hidden sprites are showing again."
	a.warnAt = time.Now()
}

// chatBoxTopStrip is the showname strip above the message text and chatBoxBottomPad the air under the last line. These
// are AsyncAO's own furniture, not courtroom_design.ini rects; they also serve as the fallback for a theme with an
// ao2_chatbox but no showname or message child (chatboxfit.go).
const (
	chatBoxTopStrip  = int32(26)
	chatBoxBottomPad = int32(8)
	// chatOverlayPadX is the left/right gutter; the usable width loses 2× it.
	chatOverlayPadX = int32(8)
	// chatOverlayNameY is the showname row's offset from the box's top edge.
	chatOverlayNameY = int32(4)
)

// grownChatBoxH is the height showing lines of lineH text in full, between baseH and 3/5 of vpH. A theme's own skin
// keeps its authored size.
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
