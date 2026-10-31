package ui

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// buildRoom rebuilds the courtroom from session state, keeping iniChar and sidePref so a tab keeps its iniswap and /pos.
// hpPrev re-arms at the current bars because background tabs apply HP without the penalty sound.
func (a *App) buildRoom() {
	if a.sess == nil {
		return
	}
	a.room = a.newRoom(a.urls, a.sess, a.d.Audio) // construction IS wiring (newroom.go): char.ini callbacks, the overlay engine, the visual prefs
	// Apply this server's volume profile before the music below is re-seeded.
	a.applyAudioVolumes()
	urls := a.urls
	mgr := a.d.Manager
	a.room.Predictor = assets.NewPrefetcher(func(character, emote string) {
		if emote == "" {
			emote = "normal" // no chain signal yet: the default loop
		}
		// Bare-named packs answer only the bare and (b) spellings, and the talk sprite shows first.
		// Speculative: a total miss must not warn. PriorityLow so it sheds under backpressure.
		idle := urls.Emote(character, emote, courtroom.EmoteIdle)
		talk := urls.Emote(character, emote, courtroom.EmoteTalk)
		mgr.PrefetchChainSpeculative(idle, urls.EmoteAlts(character, emote, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker, idle)
		mgr.PrefetchChainSpeculative(talk, urls.EmoteAlts(character, emote, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker, talk)
	})
	a.room.Predictor.SetAggressiveness(a.d.Prefs.Load().PrefetchAggressiveness()) // #100 predictive-prefetch level
	a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
	a.d.Viewport.OnPreanimStart = a.room.NotifyPreanimStarted // extend the fallback for long decoded preanims (phase-guarded — safe if a preview later shares this viewport)
	// Must track the room owning the on-screen sprite, like OnPreanimDone: NotifyFrameShown has no stale check,
	// so a replay sprite's frames would otherwise fire SFX from the live room's trigger table.
	a.d.Viewport.OnFrameShown = a.room.NotifyFrameShown
	if a.sess.Background != "" {
		a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: a.sess.Background})
	}
	// The viewport is App-global and its scenery is sticky until the new background is resident, so without this
	// a tab switch keeps painting the previous tab's background. A brief blank beats the wrong background.
	a.d.Viewport.RebindScenery(a.room.Scene.BackgroundBase, a.room.Scene.DeskBase)
	// The track was announced before this room existed, so without this the music stays silent on entry.
	if a.sess.MusicTrack != "" {
		// Must precede the resume: applyTimingToRoom runs later, and NewCourtroom's default is streaming on.
		a.room.StreamMusic = a.d.Prefs.Load().MusicStreaming
		a.resumeActiveTabMusic()
	}
	a.applyTimingToRoom()
	a.pushRealizationToRoom()
	// Re-stage the last IC message settled so a rebuilt room is not blank. After applyTimingToRoom on purpose:
	// begin() bakes ForceCharNames, HideSpriteStyles and ReduceMotion into the scene.
	if m := a.stageRestoreMsg(); m != nil {
		a.room.RestoreMessage(m)
	}
	a.d.Prefs.RememberServerChar(a.serverKey, a.myCharName())
	// Court extras are per room; the penalty sound tracking restarts from the session's current bar values.
	a.wtceName, a.testimonyOn = "", false
	a.hpPrev = [2]int{a.sess.HPDef, a.sess.HPPro}
	a.showModcall, a.modReason = false, ""
	a.showEvid, a.evidEditing, a.evidPresent, a.evidIdx = false, false, false, -1
	a.evidPresentImage = ""
	a.evidCtxMenu = false
	a.evidAsk = nil
	a.evShowImg = ""
	a.screen = ScreenCourtroom
	a.loadCharINI()
	a.updatePresence()         // character (and server) just became known
	a.liveDetailsArea = "\x00" // force a fresh rich-roster pull for the new session
	a.rebuildLiveRoster()      // seed the live player list from the handshake's CharsCheck
}

// stageRestoreMsg returns the last IC message to re-stage, or nil to stay blank if the active tab ignores its speaker.
// The session keeps only one message, so an ignored last speaker restores blank rather than an earlier line.
func (a *App) stageRestoreMsg() *protocol.ChatMessage {
	if a.sess == nil {
		return nil
	}
	m := a.sess.LastIC
	if m == nil || a.ignoreSpeaker(m) {
		return nil
	}
	return m
}

// A pinned background tab renders as a floating second client window over the primary courtroom.
// Its room uses courtroom.NopAudio so the one music stream stays with the primary; texture keys are origin-qualified.

const splitInputH = 30 // pinned client's IC input row height (pass 2c)
const (
	clientWinDefW = 520 // floating client window default size
	clientWinDefH = 470
	clientWinMinW = 360 // resize floor: stage + log + input stay usable
	clientWinMinH = 300
	// Full-theme view zoom: 1 is the whole client shrunk to fit the window.
	clientZoomMin  = 1.0
	clientZoomMax  = 6.0
	clientZoomStep = 1.15 // wheel multiplier per notch
	// clientClickSlop is the most the cursor may travel (logical px) for a press to count as a click, not a pan.
	clientClickSlop = 4
)

// splitActive reports whether a pinned background tab with a live session and room should render.
func (a *App) splitActive() bool {
	return a.splitTab != nil && a.splitRoom != nil && a.splitVP != nil &&
		a.splitTab.state.sess != nil && !a.splitTab.dead
}

// clearSplit closes the floating client, keeping the viewport for reuse; this tab alone owns its chat raster.
func (a *App) clearSplit() {
	if a.splitTab != nil && a.splitTab.state.msRaster != nil {
		a.splitTab.state.msRaster.Destroy()
		a.splitTab.state.msRaster = nil
	}
	a.splitTab, a.splitRoom = nil, nil
	if a.ctx != nil && a.ctx.focusID == "ic-split" {
		a.ctx.focusID = "" // the client's field is gone — don't leave focus dangling on it
	}
	if a.clientTex != nil {
		_ = a.clientTex.Destroy()
		a.clientTex = nil
	}
}

// controlPinnedClient swaps the pinned server and the primary using activateTab and pinToSplit.
// A second interactive courtroom would collide widget ids and leak App-global state.
func (a *App) controlPinnedClient() {
	if !a.splitActive() {
		return
	}
	pinned := a.splitTab
	pi := -1
	for i, t := range a.tabs {
		if t == pinned {
			pi = i
			break
		}
	}
	if pi < 0 {
		return
	}
	prev := a.activeTab
	a.activateTab(pi) // parks the current primary, activates the pinned (this clears the split)
	if prev >= 0 && prev < len(a.tabs) && a.tabs[prev] != a.splitTab {
		a.pinToSplit(a.tabs[prev]) // the old primary becomes the new floating view
	}
}

// clientControlClick requests a control swap on a click, deferred so the session never changes mid-render.
func (a *App) clientControlClick(view sdl.Rect) {
	c := a.ctx
	if c.clicked && !a.clientPanning && pointIn(c.mouseX, c.mouseY, view) &&
		absInt(int(c.mouseX-c.downX))+absInt(int(c.mouseY-c.downY)) <= clientClickSlop {
		a.pendingControlSwap = true
	}
}

// pinToSplit pins a background tab as the floating client, or closes it when the same tab is pinned again.
func (a *App) pinToSplit(t *courtTab) {
	if t == nil || t.state.sess == nil || t.dead {
		return
	}
	if a.splitTab == t { // toggle off
		a.clearSplit()
		return
	}
	if a.splitVP == nil {
		a.splitVP = render.NewViewport(a.d.Store)
	}
	a.splitTab = t
	// The split row cache is keyed by the pinned session's icLogSeq, which can collide across tabs.
	a.splitPinEpoch++
	// NopAudio keeps the pane silent; newRoom gives it the same overlay engine as every other room.
	a.splitRoom = a.newRoom(t.state.urls, t.state.sess, courtroom.NopAudio{})
	// The pane is a different server's session, so a wiping BN must re-scene at that tab's side, not a.mySide.
	a.splitRoom.Env = envResolver{a: a, side: func() string {
		if t.state.sidePref != "" {
			return t.state.sidePref
		}
		return defaultSide
	}}
	a.splitVP.OnPreanimDone = a.splitRoom.NotifyPreanimDone
	a.splitVP.OnPreanimStart = a.splitRoom.NotifyPreanimStarted
	a.splitVP.OnFrameShown = a.splitRoom.NotifyFrameShown              // #17 (NopAudio here → shake/flash only; sfx is silent for the pinned view)
	a.clientZoom, a.clientPanX, a.clientPanY = clientZoomMin, 0.5, 0.5 // fresh pin starts at fit
	if t.state.sess.Background != "" {
		a.splitRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: t.state.sess.Background})
	}
	// The split viewport is reused across pins, so without this its sticky scenery keeps the previous tab's background.
	a.splitVP.RebindScenery(a.splitRoom.Scene.BackgroundBase, a.splitRoom.Scene.DeskBase)
	if t.state.sess.MusicTrack != "" {
		a.splitRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventMusic, Text: t.state.sess.MusicTrack, Loop: true}) // NopAudio: silent (#15 loop default)
	}
	// Unfiltered on purpose: the live pump routes this tab's events to splitRoom without the ignore check,
	// which keys on the active tab's server anyway.
	a.splitRoom.RestoreMessage(t.state.sess.LastIC)
}

// placeClientAt centres the floating client under (mx,my), for dropping a torn-off tab.
func (a *App) placeClientAt(mx, my int32) {
	w := a.clientWin.w
	if w <= 0 {
		w = clientWinDefW
	}
	a.clientWin.x = mx - w/2
	a.clientWin.y = my - floatTitleH/2
	a.clientWin.placed = true
}

func (a *App) clientWinRect(w, h int32) sdl.Rect {
	if r, ok := a.seedPanelFromSlot(&a.clientWin, slotPanelClient, clientWinDefW, clientWinDefH, clientWinMinW, clientWinMinH, w, h); ok {
		return r
	}
	return a.clientWin.rect(clientWinDefW, clientWinDefH, clientWinMinW, clientWinMinH, w, h)
}

// drawFloatClient draws the pinned server's window; pressed is the per-frame press edge, so one press grabs one window.
func (a *App) drawFloatClient(w, h int32, pressed *bool) {
	if !a.splitActive() {
		return
	}
	c := a.ctx
	wasActive := a.clientWin.dragging || a.clientWin.resizing // detect the drag/resize-end frame for slot persistence
	r := a.clientWinRect(w, h)
	c.Fill(r, ColBackground)
	c.Border(r, ColAccent)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	if a.clientHdrName != a.splitTab.state.serverName { // memoized: no per-frame concat
		a.clientHdrName = a.splitTab.state.serverName
		a.clientHdr = "▣ " + a.clientHdrName
	}
	c.LabelClipped(r.X+10, r.Y+8, r.W-102, a.clientHdr, ColAccent)
	tgl := sdl.Rect{X: r.X + r.W - 92, Y: r.Y + 5, W: 60, H: floatTitleH - 10}
	tglLabel := "Full view"
	if a.clientFull {
		tglLabel = "Compact"
	}
	if c.Button(tgl, tglLabel) {
		a.clientFull = !a.clientFull
	}
	c.TooltipAfter("cli-full", tgl, "Show the WHOLE theme (emote grid, buttons…) — view-only; chat in the strip below. Roughly doubles render cost while open.")
	if c.Button(sdl.Rect{X: r.X + r.W - 26, Y: r.Y + 5, W: 20, H: floatTitleH - 10}, "x") {
		a.clearSplit()
		return
	}
	a.floatWinDrag(&a.clientWin, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 98, H: floatTitleH}, pressed)
	// Before the body, so a corner press resizes rather than landing in the log or input beneath it.
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.clientWin, grip, r, clientWinMinW, clientWinMinH, pressed)
	if wasActive && !a.clientWin.dragging && !a.clientWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelClient, r, w, h)
	}
	body := sdl.Rect{X: r.X, Y: r.Y + floatTitleH, W: r.W, H: r.H - floatTitleH}
	if a.clientFull && a.canRenderFullClient() {
		a.drawClientFull(w, h, body, pressed)
	} else {
		a.drawClientBody(body)
	}
	a.drawResizeGrip(grip)
}

// canRenderFullClient is false in theater and the layout editors: they reskin drawCourtroom globally.
func (a *App) canRenderFullClient() bool {
	return !a.theaterOn && !a.classicEdit && !a.layoutEdit
}

// drawClientBody lays out the pinned server's stage, chat log and IC input; clicking the stage takes control.
func (a *App) drawClientBody(r sdl.Rect) {
	c := a.ctx
	inputRow := sdl.Rect{X: r.X, Y: r.Y + r.H - splitInputH, W: r.W, H: splitInputH}
	sceneH := r.W * 3 / 4
	if maxH := (r.H - splitInputH) * 55 / 100; sceneH > maxH {
		sceneH = maxH
	}
	stage := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: sceneH}
	a.splitVP.Render(c.Ren, &a.splitRoom.Scene, stage)
	a.clientControlClick(stage) // click the stage → take control of this server
	logTop := stage.Y + stage.H + 3
	logRect := sdl.Rect{X: r.X, Y: logTop, W: r.W, H: inputRow.Y - logTop}
	c.Fill(sdl.Rect{X: logRect.X, Y: logRect.Y - 2, W: logRect.W, H: 1}, ColPanelHi) // rule above the log
	a.drawSplitLog(logRect, &a.splitTab.state)
	a.drawSplitInput(inputRow)
}

// drawClientFull blits the view-only pinned courtroom into the window above a live IC input strip.
func (a *App) drawClientFull(w, h int32, body sdl.Rect, pressed *bool) {
	c := a.ctx
	inputRow := sdl.Rect{X: body.X, Y: body.Y + body.H - splitInputH, W: body.W, H: splitInputH}
	view := sdl.Rect{X: body.X, Y: body.Y, W: body.W, H: body.H - splitInputH}
	a.renderFullClientTexture(w, h)
	if a.clientTex == nil {
		c.LabelClipped(view.X+10, view.Y+10, view.W-20, "Full-theme view unavailable on this renderer.", ColTextDim)
		a.drawSplitInput(inputRow)
		return
	}
	a.handleClientZoomPan(view, pressed) // scroll = zoom (cursor-anchored), drag = pan
	a.clientControlClick(view)           // a plain click on the client = take control of that server
	src := a.clientViewSrc()             // sub-rect of the texture from zoom + pan
	_ = c.Ren.Copy(a.clientTex, &src, &view)
	const ctlHint = "▶ Click to control this server (buttons + chat)"
	hintPill := sdl.Rect{X: view.X, Y: view.Y, W: c.TextWidth(ctlHint) + 12, H: 18}
	c.Fill(hintPill, sdl.Color{R: 0, G: 0, B: 0, A: 150})
	c.LabelClipped(hintPill.X+6, hintPill.Y+3, view.W-12, ctlHint, ColAccent)
	// Memoized so the per-frame draw never calls Sprintf.
	pct := int(a.clientZoom*100 + 0.5)
	if pct != a.clientZoomLblPct {
		a.clientZoomLblPct = pct
		a.clientZoomLbl = fmt.Sprintf("Zoom %d%%  ·  scroll to zoom · drag to pan", pct)
	}
	pill := sdl.Rect{X: view.X, Y: view.Y + view.H - 18, W: c.TextWidth(a.clientZoomLbl) + 12, H: 18}
	c.Fill(pill, sdl.Color{R: 0, G: 0, B: 0, A: 150})
	c.LabelClipped(pill.X+6, pill.Y+3, view.W-12, a.clientZoomLbl, ColAccent)
	a.drawSplitInput(inputRow) // quick-chat strip (sendICSplit) — fire a line without taking control
}

// clientViewSrc is the sub-rectangle of clientTex shown in the window for the current zoom and pan.
func (a *App) clientViewSrc() sdl.Rect {
	z := a.clientZoom
	if z < clientZoomMin {
		z = clientZoomMin
	}
	tw, th := float64(a.clientTexW), float64(a.clientTexH)
	sw, sh := tw/z, th/z
	sx := a.clientPanX*tw - sw/2
	sy := a.clientPanY*th - sh/2
	sx = value.Clamp(sx, 0, tw-sw)
	sy = value.Clamp(sy, 0, th-sh)
	return sdl.Rect{X: int32(sx), Y: int32(sy), W: int32(sw), H: int32(sh)}
}

// handleClientZoomPan applies wheel zoom at the cursor, drag pan and double-click reset over the full view.
func (a *App) handleClientZoomPan(view sdl.Rect, pressed *bool) {
	c := a.ctx
	if a.clientZoom < clientZoomMin {
		a.clientZoom = clientZoomMin
	}
	hover := pointIn(c.mouseX, c.mouseY, view)
	if hover && c.wheelY != 0 { // zoom around the cursor
		old := a.clientZoom
		nz := old * clientZoomStep
		if c.wheelY < 0 {
			nz = old / clientZoomStep
		}
		nz = value.Clamp(nz, clientZoomMin, clientZoomMax)
		if nz != old {
			src := a.clientViewSrc()
			fx := float64(c.mouseX-view.X) / float64(view.W)
			fy := float64(c.mouseY-view.Y) / float64(view.H)
			texX := float64(src.X) + fx*float64(src.W) // texture point under the cursor
			texY := float64(src.Y) + fy*float64(src.H)
			tw, th := float64(a.clientTexW), float64(a.clientTexH)
			sw, sh := tw/nz, th/nz
			a.clientZoom = nz
			a.clientPanX = (texX - fx*sw + sw/2) / tw // keep that point under the cursor
			a.clientPanY = (texY - fy*sh + sh/2) / th
			a.clampClientPan()
		}
		c.wheelY = 0 // consumed
	}
	if a.clientZoom > clientZoomMin { // drag to pan (only meaningful when zoomed)
		if *pressed && hover {
			*pressed = false
			a.clientPanning = true
			a.clientPanGrabX, a.clientPanGrabY = c.mouseX, c.mouseY
			a.clientPanBaseX, a.clientPanBaseY = a.clientPanX, a.clientPanY
		}
		if !c.mouseDown {
			a.clientPanning = false
		}
		if a.clientPanning {
			tw, th := float64(a.clientTexW), float64(a.clientTexH)
			sw, sh := tw/a.clientZoom, th/a.clientZoom
			a.clientPanX = a.clientPanBaseX - float64(c.mouseX-a.clientPanGrabX)/float64(view.W)*sw/tw
			a.clientPanY = a.clientPanBaseY - float64(c.mouseY-a.clientPanGrabY)/float64(view.H)*sh/th
			a.clampClientPan()
		}
	} else {
		a.clientPanning = false
		a.clientPanX, a.clientPanY = 0.5, 0.5
	}
}

// clampClientPan keeps the zoomed source window inside the texture (half-extent is 0.5/zoom, normalized).
func (a *App) clampClientPan() {
	half := 0.5 / a.clientZoom
	a.clientPanX = value.Clamp(a.clientPanX, half, 1-half)
	a.clientPanY = value.Clamp(a.clientPanY, half, 1-half)
}

// renderFullClientTexture draws the pinned courtroom into clientTex at the logical screen size, with input cleared.
// View-only because the kit is single-instance: an interactive second courtroom collides widget ids and App state.
func (a *App) renderFullClientTexture(w, h int32) {
	c := a.ctx
	if w <= 0 || h <= 0 {
		return
	}
	if a.clientTex == nil || a.clientTexW != w || a.clientTexH != h {
		if a.clientTex != nil {
			_ = a.clientTex.Destroy()
			a.clientTex = nil
		}
		tex, err := c.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
		if err != nil {
			return
		}
		a.clientTex, a.clientTexW, a.clientTexH = tex, w, h
	}

	// Deferred so a panic inside drawCourtroom cannot leave the screen rendering into the texture.
	prevTarget := c.Ren.GetRenderTarget()
	if c.Ren.SetRenderTarget(a.clientTex) != nil {
		return
	}
	// Inherits the ambient text device scale: changing textDevPct here would purge the font caches every frame.
	// clientTex is logical-sized and blitLabel divides back to logical, so text stays its logical size.
	defer func() {
		_ = c.Ren.SetRenderTarget(prevTarget)
		s := float32(a.UIScale()) / 100 // restore main's per-frame render scale
		if s <= 0 {
			s = 1 // never strand the renderer at scale 0 (UIScale unset)
		}
		_ = c.Ren.SetScale(s, s)
		c.endRenderScaleOverride()
	}()
	// Without this the chatbox's device-exact blit reads the live UI scale and draws pinned chat text too big.
	c.beginRenderScaleOverride(DefaultScalePct)
	_ = c.Ren.SetScale(1, 1)
	_ = c.Ren.SetDrawColor(0, 0, 0, 255)
	_ = c.Ren.Clear()

	// msRaster lives in sessionState, so the swap gives the pass its own; a copy would double-free on clearSplit.
	savedSess := a.sessionState
	savedVP := a.d.Viewport
	savedScreen := a.screen // view-only pass must not change the primary's screen…
	// …tooltip. c.tipAnchor needs no save: the mouse is parked off-screen and only Tooltip's hover check writes it.
	// If anything ever writes tipText outside that check, save tipAnchor with it.
	savedTip := c.tipText
	savedSeqLen := len(c.fieldSeq)
	in := a.snapshotInput() // …focus order, or handle any input

	a.sessionState = a.splitTab.state // promotes a.sess / a.icInput / a.emote* / msRaster … to the pinned tab
	a.room = a.splitRoom              // drive the pinned LIVE room (a parked tab's sessionState.room is nil)
	a.d.Viewport = a.splitVP
	a.pinnedPass = true

	a.drawCourtroom(w, h)

	a.pinnedPass = false
	a.room = nil                      // a parked tab keeps no room — don't persist splitRoom into its slot
	a.splitTab.state = a.sessionState // capture emote/scroll/raster/etc. side effects of the view-only pass
	a.sessionState = savedSess        // restores a.room + a.sess + msRaster + everything promoted
	a.d.Viewport = savedVP
	a.screen = savedScreen
	c.tipText = savedTip
	if len(c.fieldSeq) > savedSeqLen {
		c.fieldSeq = c.fieldSeq[:savedSeqLen] // drop the pinned client's field ids from Tab-cycle
	}
	a.restoreInput(in)
}

// ctxInput holds the per-frame Ctx input fields, cleared for the view-only pass with the mouse parked off-screen.
type ctxInput struct {
	mouseX, mouseY, downX, downY, wheelY      int32
	clicked, dblClick, tripleClick            bool
	rightClicked                              bool
	mouseDown, middleHeld                     bool
	backspace, enter, tabPressed, escPressed  bool
	keyPressed, hotkey                        sdl.Keycode
	typed, pasted, pastedRaw, dropped, dragID string
	copyReq, cutReq, selectAll, wheelTaken    bool
	undoReq, redoReq                          bool
	wordBack                                  bool
}

func (a *App) snapshotInput() ctxInput {
	c := a.ctx
	in := ctxInput{
		mouseX: c.mouseX, mouseY: c.mouseY, downX: c.downX, downY: c.downY, wheelY: c.wheelY,
		clicked: c.clicked, dblClick: c.dblClick, tripleClick: c.tripleClick, rightClicked: c.rightClicked,
		mouseDown: c.mouseDown, middleHeld: c.middleHeld,
		backspace: c.backspace, enter: c.enter, tabPressed: c.tabPressed, escPressed: c.escPressed,
		keyPressed: c.keyPressed, hotkey: c.hotkey,
		typed: c.typed, pasted: c.pasted, pastedRaw: c.pastedRaw, dropped: c.dropped, dragID: c.dragID,
		copyReq: c.copyReq, cutReq: c.cutReq, selectAll: c.selectAll, wheelTaken: c.wheelTaken,
		undoReq: c.undoReq, redoReq: c.redoReq,
		wordBack: c.wordBack,
	}
	c.mouseX, c.mouseY = -30000, -30000 // park off-screen: every hovering()/pointIn is false
	c.downX, c.downY, c.wheelY = -30000, -30000, 0
	c.clicked, c.dblClick, c.tripleClick, c.rightClicked = false, false, false, false
	c.mouseDown, c.middleHeld = false, false
	c.backspace, c.enter, c.tabPressed, c.escPressed = false, false, false, false
	c.keyPressed, c.hotkey = 0, 0
	c.typed, c.pasted, c.pastedRaw, c.dropped, c.dragID = "", "", "", "", ""
	c.copyReq, c.cutReq, c.selectAll, c.wheelTaken = false, false, false, false
	c.undoReq, c.redoReq = false, false
	c.wordBack = false
	return in
}

func (a *App) restoreInput(in ctxInput) {
	c := a.ctx
	c.mouseX, c.mouseY, c.downX, c.downY, c.wheelY = in.mouseX, in.mouseY, in.downX, in.downY, in.wheelY
	c.clicked, c.dblClick, c.tripleClick, c.rightClicked = in.clicked, in.dblClick, in.tripleClick, in.rightClicked
	c.mouseDown, c.middleHeld = in.mouseDown, in.middleHeld
	c.backspace, c.enter, c.tabPressed, c.escPressed = in.backspace, in.enter, in.tabPressed, in.escPressed
	c.keyPressed, c.hotkey = in.keyPressed, in.hotkey
	c.typed, c.pasted, c.pastedRaw, c.dropped, c.dragID = in.typed, in.pasted, in.pastedRaw, in.dropped, in.dragID
	c.copyReq, c.cutReq, c.selectAll, c.wheelTaken = in.copyReq, in.cutReq, in.selectAll, in.wheelTaken
	c.undoReq, c.redoReq = in.undoReq, in.redoReq
	c.wordBack = in.wordBack
}

// drawSplitInput draws the pinned pane's IC field, which edits that tab's own icInput and sends through sendICSplit.
func (a *App) drawSplitInput(r sdl.Rect) {
	c := a.ctx
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, ColPanelHi) // rule above the input
	box := sdl.Rect{X: r.X + 6, Y: r.Y + 5, W: r.W - 12, H: r.H - 10}
	s := &a.splitTab.state
	primary, emoji := a.icFieldFonts(s.icInput)
	var send bool
	s.icInput, send = c.TextFieldEmoji("ic-split", box, s.icInput, "Chat in the pinned server — click to focus", primary, emoji)
	// The notice box blocks the pointer for the whole frame, leaving a focused field's Enter able to fire during a ban
	// notice; a line the user did not see themselves send is wrong on whichever server receives it.
	if send && !a.serverNoticeDlg.open {
		a.sendICSplit()
	}
}

// sendICSplit sends on the pinned server by swapping its state into the embedded sessionState around sendIC.
// sendIC and its helpers never touch a.room; this is synchronous, so no frame or tab pump can interleave.
func (a *App) sendICSplit() {
	if a.splitTab == nil {
		return
	}
	savedFocus := a.ctx.focusID
	saved := a.sessionState
	a.sessionState = a.splitTab.state
	a.sendIC()
	a.splitTab.state = a.sessionState // capture cleared input, lastSent* markers, emote roll, rate-limit stamp
	a.sessionState = saved
	// sendIC's random-emote path refocuses "ic"; keep the caret in the pinned pane.
	if savedFocus == "ic-split" {
		a.ctx.focusNext = "ic-split"
	}
}

// splitLogWrapMaxLines caps one entry's wrapped rows, derived so a legitimate message is never clipped.
// wrapToWidth needs a finite cap: maxLines <= 0 cuts to nothing, unlike WrapText.
const splitLogWrapMaxLines = icLineCap / oocWrapMinCharsPerRow

type splitLogRow struct {
	text string
	col  sdl.Color
}

// drawSplitLog renders the pinned session's IC log into r, with wrapping cached by width and log seq.
func (a *App) drawSplitLog(r sdl.Rect, s *sessionState) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := r.W - 8
	if wrapW != a.splitWrapW || s.icLogSeq != a.splitWrapSeq || a.splitPinEpoch != a.splitWrapEpoch ||
		a.logPct != a.splitWrapPct || c.fontChainGen != a.splitWrapGen {
		a.splitWrapW, a.splitWrapSeq, a.splitWrapEpoch = wrapW, s.icLogSeq, a.splitPinEpoch
		a.splitWrapPct, a.splitWrapGen = a.logPct, c.fontChainGen
		a.splitWrapRows = a.splitWrapRows[:0]
		for i := range s.icLog {
			e := &s.icLog[i]
			col := ColText
			if e.color > 0 {
				col = render.TextColor(e.color)
			}
			// Measure in the face this pane draws in, not WrapText's: they differ with a custom font or zoom.
			// constFont because every row uses the log's primary face, with no per-row font pick or emoji raster.
			for _, ln := range wrapToWidth(constFont(font), e.text, wrapW, splitLogWrapMaxLines) {
				a.splitWrapRows = append(a.splitWrapRows, splitLogRow{text: ln, col: col})
			}
		}
	}
	rows := a.splitWrapRows
	clipPrev, clipHad := c.pushClip(r)
	y := r.Y + r.H - lineH // newest line pinned to the bottom, drawing upward
	for ri := len(rows) - 1; ri >= 0; ri-- {
		if y < r.Y-lineH {
			break
		}
		c.LabelClippedFont(font, r.X+4, y, wrapW, rows[ri].text, rows[ri].col)
		y -= lineH
	}
	c.popClip(clipPrev, clipHad)
}

// crossfadeDur is the speaker-swap blend from prefs, zero under Reduce motion.
func (a *App) crossfadeDur() time.Duration {
	if a.d.Prefs.Load().ReduceMotionOn {
		return 0
	}
	return time.Duration(a.d.Prefs.Load().CrossfadeMsVal) * time.Millisecond
}

// Frame pacing: FramePace gives the main loop a per-frame budget by what is on screen, and input restores full rate.

// inputGraceFrameDuration is one frame of the post-input hold counted by InputGraceFrames, a fixed 60 fps so the
// count means the same at any cap. Mouse motion uses motionInputGrace instead.
const inputGraceFrameDuration = time.Second / 60

// NoteInput records that an SDL event just arrived, starting the full-rate grace.
func (a *App) NoteInput() { a.lastInputAt = time.Now() }

// NoteAnimating marks that this draw rendered a self-driven UI animation, so frames continue at the active cap.
// Each animating frame arms the next. For a one-off change set a.uiDirty instead, which earns one frame.
func (a *App) NoteAnimating() { a.frameAnimChrome = true }

// audioFineTick is how often the courtroom advances and plays blips while typing, independent of the present rate.
const audioFineTick = time.Second / 60

// AudioActive reports whether the live courtroom is typing a message; replay, maker and GIF export pace themselves.
func (a *App) AudioActive() bool {
	if a.replaying || a.makerOpen || a.gifExporting || a.room == nil {
		return false
	}
	return a.room.AudioActive()
}

// AudioPaceActive reports whether to sub-step the pacing sleep, since one long sleep batches blips into an instant.
func (a *App) AudioPaceActive(budget time.Duration) bool {
	return budget > audioFineTick && a.AudioActive()
}

// AudioFineTick is the audio advance cadence for the main loop's sub-step sleep.
func (a *App) AudioFineTick() time.Duration { return audioFineTick }

// MarkRoomPreAdvanced records that the loop already advanced the courtroom this cycle, so Frame won't advance it again.
func (a *App) MarkRoomPreAdvanced() { a.roomPreAdvanced = true }

// motionInputGrace holds full rate after bare pointer motion in the experimental loop: drags keep re-arming it,
// and waving over dead space stops costing frames soon after.
const motionInputGrace = 200 * time.Millisecond

// NoteMotion marks a pointer-motion-only pass; in the classic loop motion is plain input.
func (a *App) NoteMotion() {
	if a.d.Prefs != nil && a.d.Prefs.Load().EventDrivenLoop {
		// The motion event already earns one frame (SkipFrame refuses on sawEvent), so don't arm the full-rate grace.
		if a.d.Prefs.Load().MotionRedrawPerEvent {
			return
		}
		a.lastMotionAt = time.Now()
		return
	}
	a.lastInputAt = time.Now()
}

// staticTalkFPS paces a message over all-static art: runes reveal at about 40 ms or slower, so 30 fps samples them.
const staticTalkFPS = 30

// maxHousekeepingGap bounds how long the event-driven loop parks even at FPSOff: Background must keep pumping
// (keepalive at 45 s, queue drains). A wait timing out at this floor pumps only; it does not render.
const maxHousekeepingGap = 500 * time.Millisecond

// paceBudget converts an fps knob to a frame budget (0 = uncapped); for the active cap, which is never off.
func paceBudget(fps int) time.Duration {
	if fps <= 0 {
		return 0
	}
	return time.Second / time.Duration(fps)
}

// rateBudget converts a rate knob to a frame budget: FPSUnlimited gives (0, false), FPSOff (0, true), N (1s/N, false).
// Callers must check off first, because a 0 budget otherwise means uncapped, the opposite of off.
func rateBudget(fps int) (budget time.Duration, off bool) {
	switch fps {
	case config.FPSOff:
		return 0, true
	case config.FPSUnlimited:
		return 0, false
	}
	if fps <= 0 {
		return 0, false // stray non-positive (never a sentinel) → uncapped, never off
	}
	return time.Second / time.Duration(fps), false
}

// HardCapBudget is the minimum time between rendered frames for this focus state, 0 if uncapped.
// The main loop sleeps it uninterruptibly so an input flood such as mouse motion cannot drive the loop past the cap.
func (a *App) HardCapBudget(focused bool) time.Duration {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	if focused {
		// The active cap has no off: 0 fps while interacting is meaningless.
		return paceBudget(a.d.Prefs.Load().FPSCap())
	}
	unf, off := rateBudget(a.d.Prefs.Load().UnfocusedFPS())
	if off {
		// Off means no redraw while tabbed out, but a frame that renders anyway still needs a ceiling.
		return paceBudget(a.d.Prefs.Load().FPSCap())
	}
	return unf // ∞ → 0 (uncapped); N → 1s/N
}

// roomBusy reports a message mid-ceremony on the active room, or anything still queued.
func (a *App) roomBusy() bool {
	return a.room != nil && (a.room.Phase() != courtroom.PhaseIdle || a.room.QueueLen() > 0)
}

// serverTimersLive reports any visible TI timer chip, whose readout changes every second.
func (a *App) serverTimersLive() bool {
	if a.sess == nil {
		return false
	}
	for i := range a.sess.Timers {
		if a.sess.Timers[i].Visible {
			return true
		}
	}
	return false
}

// talkBudget is the budget for a message over static art, revealing at most one rune per frame:
// blips fire from the per-frame Update, so a slower frame audibly coalesces them.
func (a *App) talkBudget(full time.Duration) time.Duration {
	talk := paceBudget(staticTalkFPS)
	if a.room != nil {
		if iv := a.room.Typewriter.Interval; iv > 0 && iv < talk {
			talk = iv
		}
	}
	if talk < full {
		talk = full // never above the cap
	}
	return talk
}

// pacerTier labels which FramePace branch set the budget, for the F8 panel only; it must not feed any pacing decision.
// The return sites record it because re-deriving it in the panel would drift from the logic it diagnoses.
type pacerTier uint8

const (
	pacerUnfocused     pacerTier = iota // window not focused (its own trickle tier)
	pacerFullInput                      // recent input / effects in flight (full cap)
	pacerAnimChrome                     // self-driven UI animation census (backstopped cap)
	pacerTalk                           // a message is playing (typewriter/blip cadence)
	pacerContentAnim                    // only stage animations move (next frame flip)
	pacerIdleActiveCap                  // rendered-but-idle, active cap (event-driven/∞ idle)
	pacerIdleNap                        // classic idle nap at the idle rate
)

// pacerTierLabel renders a pacerTier for the F8 panel.
func pacerTierLabel(t pacerTier) string {
	switch t {
	case pacerUnfocused:
		return "unfocused (trickle)"
	case pacerFullInput:
		return "full — input/effects in flight"
	case pacerAnimChrome:
		return "anim-chrome census (backstopped cap)"
	case pacerTalk:
		return "talk — message playing"
	case pacerContentAnim:
		return "content — stage animation cadence"
	case pacerIdleActiveCap:
		return "idle → active cap (event-driven/∞ idle)"
	case pacerIdleNap:
		return "idle nap (classic idle rate)"
	default:
		return "?"
	}
}

// FramePace returns the sleep between rendered frames (0 = no cap), trying these tiers, fastest first.
// Full rate: wantsFullRate. UI animation (NoteAnimating): the cap, floored by backstopBudget.
// Talk: talkBudget, which a speaker's frame flip may only tighten. Content: the next stage frame flip.
// Idle: the event-driven loop's damage frame uses the cap so sleep adds no input latency; the classic loop uses idle.
// FPSOff means the active cap here; not rendering at idle is decided in the skip and park path.
func (a *App) FramePace(focused bool) time.Duration {
	focused = a.pacingFocused(focused)            // the "full speed while unfocused" option (pacingfocus.go)
	full := paceBudget(a.d.Prefs.Load().FPSCap()) // active cap: ∞/off → 0 (uncapped); N → 1s/N
	nextAnimDue := func() (time.Duration, bool) {
		if a.room == nil || a.d.Viewport == nil {
			return 0, false
		}
		return a.d.Viewport.NextAnimDue(a.renderScene())
	}
	if !focused {
		a.lastPacerTier = pacerUnfocused
		unf, unfOff := rateBudget(a.d.Prefs.Load().UnfocusedFPS())
		if unf == 0 && !unfOff {
			return 0 // ∞ unfocused: never throttle
		}
		if unfOff {
			// Off: an idle unfocused window skips and parks, so a frame here is content. Pace at the cap.
			unf = full
		}
		// A message playing while tabbed out is still audible: hold the blip cadence.
		// Set the tier as each deadline wins, so the F8 readout names why the budget left the trickle rate.
		if a.roomBusy() {
			if tb := a.talkBudget(full); tb < unf {
				unf = tb
				a.lastPacerTier = pacerTalk
			}
		}
		// An unfocused window is still visible on a second monitor, so stage animation keeps its own schedule.
		if due, ok := nextAnimDue(); ok && due < unf {
			unf = value.Clamp(due, full, unf)
			a.lastPacerTier = pacerContentAnim // the stage-anim deadline set the budget
		}
		return unf
	}
	if a.wantsFullRate() {
		a.lastPacerTier = pacerFullInput
		return full // recent input / effects genuinely in flight
	}
	if a.drawnAnimChrome {
		// NoteAnimating can repeat indefinitely (FX chat text, animated theme), so the budget must never be zero:
		// with the infinite cap, every pacing sleep is skipped and vsync does not block on some windowed present paths.
		a.lastPacerTier = pacerAnimChrome
		return backstopBudget(full)
	}
	idle, idleOff := rateBudget(a.d.Prefs.Load().IdleFPS())
	idleUnlimited := !idleOff && idle == 0 // ∞ idle
	if a.roomBusy() {
		// Order matters: the stage animation may only tighten the talk budget, or a slow lipflap crawls text and blips.
		talk := a.talkBudget(full)
		switch {
		case idleOff || idleUnlimited:
			talk = full // off/∞ idle → ceremonies pace at the active cap
		case idle < talk:
			talk = idle // a high idle rate lifts the ceremony to match its smoothness
		}
		if due, ok := nextAnimDue(); ok && due < talk {
			talk = value.Clamp(due, full, talk) // a fast speaker anim gets its frames
		}
		a.lastPacerTier = pacerTalk
		return talk
	}
	if due, ok := nextAnimDue(); ok {
		a.lastPacerTier = pacerContentAnim
		if idleOff || idleUnlimited {
			return value.Clamp(due, full, due) // the content's own cadence (never above the cap)
		}
		return value.Clamp(due, full, idle) // content cadence, never slower than the idle rate
	}
	// Must not reach a zero budget for a frame nothing demanded: under the infinite cap the classic loop would spin.
	// Infinite idle resolves through the cap so a finite cap still rules.
	if a.d.Prefs.Load().EventDrivenLoop || idleOff || idleUnlimited {
		a.lastPacerTier = pacerIdleActiveCap
		return backstopBudget(full)
	}
	a.lastPacerTier = pacerIdleNap
	return idle // classic idle nap (N → 1s/N)
}

// backstopBudget returns b when finite, else about 60 fps (FPSCapUnlimitedOff): a zero budget leaves only vsync.
// Demanded frames get the uncapped budget from wantsFullRate before reaching this, so active use loses nothing.
func backstopBudget(b time.Duration) time.Duration {
	if b > 0 {
		return b
	}
	return paceBudget(config.FPSCapUnlimitedOff)
}

// SkipFrame reports that the last drawn frame is still right, so the loop may skip render and present and just pump.
// A wrong skip heals on the next damage or deadline, never a hang. The event-driven loop also skips menu screens
// and turns caret and clock refusals into wakes (NextWakeDelay); the classic loop re-renders at the idle rate.
func (a *App) SkipFrame(focused, sawEvent bool) bool {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	if sawEvent {
		return false // input this pass (incl. a focus-regain window event)
	}
	// Background cap off and unfocused: a 0 fps ceiling, overriding animation, damage and the input grace.
	// Voice and mic capture are the exception because their audio engine is frame-driven.
	if !focused && !a.voiceJoined && a.micTest == nil {
		if _, off := rateBudget(a.d.Prefs.Load().UnfocusedFPS()); off {
			return true
		}
	}
	exp := a.d.Prefs != nil && a.d.Prefs.Load().EventDrivenLoop
	if exp {
		if !a.expScreenSkippable() {
			return false
		}
		// The event-driven loop parks on NextWakeDelay, whose idle tick is the periodic render.
	} else {
		if a.room == nil || a.sess == nil {
			return false // classic: only the courtroom skips (the lobby keeps its idle render)
		}
		// The classic loop does not track damage, so a static courtroom re-renders at the idle rate; off opts out.
		if idleB, idleOff := rateBudget(a.d.Prefs.Load().IdleFPS()); !idleOff && idleB > 0 {
			if time.Since(a.lastFrameDrawn) >= idleB {
				return false
			}
		}
	}
	if a.wantsFullRate() || a.roomBusy() || a.warnActive() || a.fontWorkPending() {
		return false
	}
	// voicePump is driven from Frame, so frames must keep coming or the audio engine starves.
	if a.voiceJoined || a.micTest != nil {
		return false
	}
	if a.drawnAnimChrome {
		// The last frame drew time-stepped art outside the viewport scheduler, so render on.
		// The sprite preview reports from its draw site, not a previewBase check here: a preview orphaned by a screen
		// switch has no one left to close it, and a state check would hold the event-driven loop at the cap forever.
		return false
	}
	if exp {
		if a.RenderNeeded() {
			return false // pending damage: packets landed / textures moved / a caret flip is showing
		}
	} else {
		if a.ctx != nil {
			// Check that the blink is stale, not that a field is focused: the IC input is usually focused, which would
			// render every pass. The skip nap re-polls well inside a blink period, so the caret still flips on time.
			if on, focused := a.ctx.CaretVisible(); focused && on != a.drawnCaretOn {
				return false // the focused caret's blink phase flipped — draw it
			}
		}
		if a.timerActive() || a.serverTimersLive() {
			return false // countdown readouts change every second
		}
	}
	if a.d.Viewport != nil && a.room != nil {
		if _, ok := a.d.Viewport.NextAnimDue(a.renderScene()); ok {
			return false
		}
	}
	_ = focused // both focused and unfocused static screens skip identically
	return true
}

// expScreenSkippable reports whether the current screen may skip in the experimental loop; others always render.
// Settings' moving parts still force frames through SkipFrame's later checks; its T2 cache-stat line refreshes at idle.
func (a *App) expScreenSkippable() bool {
	switch a.screen {
	case ScreenCourtroom, ScreenCharSelect:
		return a.sess != nil
	case ScreenLobby:
		return !a.lobbyFetching && !a.pinging
	case ScreenSettings, ScreenAbout, ScreenChangelog, ScreenServerHelp, ScreenHelp, ScreenLogs:
		return true
	case ScreenThemeEditor:
		// Not the courtroom pair's `a.sess != nil`: the editor is often opened from Settings with no server.
		// NoteAnimating and RenderNeeded force the frames it needs.
		return true
	default:
		return false
	}
}

// RenderNeeded reports damage since the last drawn frame: a packet, a texture store generation move, or a stale caret.
func (a *App) RenderNeeded() bool {
	if a.uiDirty {
		return true
	}
	if a.d.Store != nil && a.d.Store.Generation() != a.drawnGen {
		return true
	}
	if a.ctx != nil {
		if on, focused := a.ctx.CaretVisible(); focused && on != a.drawnCaretOn {
			return true
		}
	}
	return false
}

// RenderSuppressed reports that SkipFrame refuses to render regardless of damage: unfocused with background cap off.
// The main loop must park then, because Background sets uiDirty on every packet and only a drawn frame clears it.
func (a *App) RenderSuppressed(focused bool) bool {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	if focused || a.voiceJoined || a.micTest != nil {
		return false
	}
	_, off := rateBudget(a.d.Prefs.Load().UnfocusedFPS())
	return off
}

// NoteDeadline marks a scheduled wake that fired, so the next pass renders one frame.
func (a *App) NoteDeadline() { a.uiDirty = true }

// NoteInteraction forces one follow-up render after real input, because a change made during a frame's draw
// only appears on the next frame.
func (a *App) NoteInteraction() { a.uiDirty = true }

const (
	// minWakeDelay floors the event wait so a just-passed deadline cannot busy-spin the loop.
	minWakeDelay = 5 * time.Millisecond
	// timerTickSlack lands a clock wake just past the second boundary, so the frame draws the new second.
	timerTickSlack = 20 * time.Millisecond
	// assetDemandWakeInterval re-renders while a demand-streamed grid shows blank cells, so asks keep going.
	// Must stay below maxHousekeepingGap: considerRender only marks a render when the due time is nearer than that.
	assetDemandWakeInterval = 250 * time.Millisecond
)

// NextWakeDelay is how long the experimental loop may park: until the nearest deadline, floored against busy-spin.
func (a *App) NextWakeDelay(focused bool) (wait time.Duration, render bool) {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	wait = maxHousekeepingGap          // Background-only floor (no frame) — see the const
	// Background cap off and unfocused: no render wakes at all, or a 0 fps cap would run at the idle rate.
	if !focused {
		if _, off := rateBudget(a.d.Prefs.Load().UnfocusedFPS()); off {
			return wait, false
		}
	}
	considerRender := func(due time.Duration) {
		if due < wait {
			wait = due
			render = true // the nearest wake is a real redraw deadline, not the floor
		}
	}
	// Idle redraw cadence. Off: no periodic render. Infinite: due immediately.
	switch idleB, idleOff := rateBudget(a.d.Prefs.Load().IdleFPS()); {
	case idleOff:
		// no periodic idle render; only the deadlines below wake a frame
	case idleB == 0:
		considerRender(0) // ∞ idle
	default:
		considerRender(idleB - time.Since(a.lastFrameDrawn))
	}
	if a.ctx != nil {
		if due, ok := a.ctx.NextCaretFlip(); ok {
			considerRender(due)
		}
		if due, ok := a.ctx.NextHoverDue(); ok {
			considerRender(due)
		}
	}
	now := time.Now()
	// Wake just after each running timer's next second. Must mark render: nothing else redraws the chip at idle off.
	if a.sess != nil {
		for i := range a.sess.Timers {
			t := &a.sess.Timers[i]
			if t.Visible && t.Running {
				if rem := t.Remaining(now); rem > 0 {
					considerRender(rem%time.Second + timerTickSlack)
				}
			}
		}
	}
	// The local alarm's due-fire (pollTimer, driven by Frame) rides these wakes.
	if a.timerRunning() {
		if rem := a.timerRemaining(); rem > 0 {
			considerRender(rem%time.Second + timerTickSlack)
		}
	}
	// SkipFrame refusing does nothing for a parked loop, so schedule the stage's next frame flip here, or animation
	// runs at the idle rate, and not at all with idle off. A static stage answers ok=false and still parks.
	if a.d.Viewport != nil && a.room != nil {
		if due, ok := a.d.Viewport.NextAnimDue(a.renderScene()); ok {
			considerRender(due)
		}
	}
	// A batch that all 404s uploads nothing and bumps no generation, so without this the asks stall at idle off.
	if a.drawnDemandPending {
		considerRender(assetDemandWakeInterval)
	}
	// Keeps tickContentJob draining and the Studio panel responsive; a job on a dead origin causes no other damage.
	if a.contentJobRunning() {
		considerRender(assetDemandWakeInterval)
	}
	if wait < minWakeDelay {
		wait = minWakeDelay
	}
	return wait, render
}

// wantsFullRate reports whether something needs the full frame rate: recent input, stage effects, or an open surface.
// Ambient motion counts only while a stage draws it (Viewport.AmbientAnimating), or every screen holds full rate.
func (a *App) wantsFullRate() bool {
	if time.Since(a.lastInputAt) < time.Duration(a.d.Prefs.Load().InputGraceFrames())*inputGraceFrameDuration {
		return true // configurable post-input hold (default 1 frame); 0 would be the input's own frame only
	}
	if time.Since(a.lastMotionAt) < motionInputGrace {
		return true // a moving pointer renders live; the short grace ends with it
	}
	if a.room != nil {
		sc := &a.room.Scene
		if sc.ShakeLeft > 0 || sc.FlashLeft > 0 {
			return true // sub-second effect countdowns — self-terminating
		}
	}
	// voiceJoined, not the panel's open flag: only a live call's audio engine needs steady frames at full rate.
	if a.replaying || a.makerOpen || a.gifExporting || a.voiceJoined || a.perfHUD {
		return true
	}
	return len(a.reactionFloats) > 0 || a.splitActive()
}

// SetSpriteCapBase stores the display-derived decode-downscale base, for applySpriteCap to re-derive the cap live.
func (a *App) SetSpriteCapBase(px int) { a.spriteCapBase = px }

// applySpriteCap pushes the effective decode-downscale cap to the decoder pool; decoded textures keep their size.
func (a *App) applySpriteCap() {
	a.d.Manager.SetSpriteCap(config.EffectiveSpriteCap(a.spriteCapBase, a.d.Prefs.Load().SpriteDownscaleOff, a.d.Prefs.Load().SpriteDownscalePctVal))
	a.d.Manager.SetAnimatedSpriteCap(a.d.Prefs.Load().AnimatedSpriteCapPxVal)
	a.d.Manager.SetAnimatedBudgetMiB(a.d.Prefs.Load().AnimatedBudgetMiB())
	a.d.Manager.SetAnimatedDecodeConcurrency(a.d.Prefs.Load().AnimDecodeConcurrency())
}

// vpSpriteLoadMode maps the loading-mode pref to the renderer: Wait and Default are courtroom message gates,
// so they fall back to hold-previous and a timed-out hold keeps the previous sprite instead of a blank.
func (a *App) vpSpriteLoadMode() int {
	mode := a.d.Prefs.Load().SpriteLoadModeVal
	if mode == config.SpriteLoadWait || mode == config.SpriteLoadDefault {
		return config.SpriteLoadHoldPrev
	}
	return mode
}

// applyTimingToRoom pushes the persisted prefs into the live courtroom; the crawl applies from the next message.
func (a *App) applyTimingToRoom() {
	if a.room == nil {
		return
	}
	crawlMs, stayMs, _ := a.d.Prefs.Load().Timing()
	a.room.Typewriter.Interval = time.Duration(crawlMs) * time.Millisecond
	a.room.TextStay = time.Duration(stayMs) * time.Millisecond
	a.room.Typewriter.BlipRate, a.room.Typewriter.BlipOnSpaces = a.d.Prefs.Load().BlipTyping() // #7: blip cadence + skip-whitespace
	a.room.CatchUp, a.room.CatchUpThreshold = a.d.Prefs.Load().CatchUp()
	a.room.SpriteWait = a.d.Prefs.Load().SpriteLoadModeVal == config.SpriteLoadWait              // "Hold the message": hold until its speaker sprite decodes
	a.room.SpriteWaitTimeout = time.Duration(a.d.Prefs.Load().SpriteWaitMs()) * time.Millisecond // its user-tunable hold cap
	a.room.SpriteWaitPair = a.d.Prefs.Load().SpriteWaitPair                                      // strictness: gate on the pair partner too
	a.room.SpriteWaitPreanim = a.d.Prefs.Load().SpriteWaitPreanim                                // strictness: gate on the preanim too
	a.room.SequentialWait = a.d.Prefs.Load().SpriteLoadModeVal == config.SpriteLoadDefault       // "Default": wait for every asset a message presents
	a.room.ShoutBypassQueue = a.d.Prefs.Load().ShoutBypassQueue                                  // AO2 parity: shouts skip the queue (toggleable)
	a.room.ShoutDuration = courtroom.DefaultShoutDuration                                        // core-timing knobs: 0 = the canonical defaults
	if ms := a.d.Prefs.Load().ShoutDurationMsVal; ms != 0 {
		a.room.ShoutDuration = time.Duration(ms) * time.Millisecond
	}
	a.room.PreanimTimeout = courtroom.DefaultPreanimTimeout
	if ms := a.d.Prefs.Load().PreanimTimeoutMsVal; ms != 0 {
		a.room.PreanimTimeout = time.Duration(ms) * time.Millisecond
	}
	a.room.QueueCap = courtroom.DefaultQueueCap // 0-pref = the canonical depth; always assigned so a nuke-reset restores it
	if n := a.d.Prefs.Load().ICQueueCapVal; n != 0 {
		a.room.QueueCap = n
	}
	a.room.CatchUpLinger = time.Duration(a.d.Prefs.Load().CatchUpLingerMsVal) * time.Millisecond
	// Shared with newRoom, which seeds every other room from it, so there is one definition of what a room shows.
	a.applyVisualPrefsToRoom(a.room)
	a.room.AdditiveText = a.d.Prefs.Load().AdditiveText  // #14 2.8 additive: honor incoming ADDITIVE=1 append (default ON)
	a.room.StreamMusic = a.d.Prefs.Load().MusicStreaming // §1.3: OFF = never fetch a /play track (Now-Playing still tracks it)
}

// applyMusicStreaming persists the streaming toggle and pushes it to the room; off stops the current track at once.
func (a *App) applyMusicStreaming(on bool) {
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			MusicStreaming =

			on

	})

	if a.room != nil {
		a.room.StreamMusic = on
	}
	if !on {
		a.d.Audio.StopMusic()           // halt the current track and cancel any pending fetch
		a.musicTabDucked = false        // stream stopped: not ducked (invariant)
		a.musicAwaitURL = ""            // and no pending un-duck (nothing will be delivered)
		a.musicAwaitSince = time.Time{} // clear the timeout stamp with the await it belongs to
	}
}

// applyMusicAcrossTabs persists the cross-tab music toggle; on clears both silencers, off re-ducks at once.
// On is unconditional so it also frees a stream stuck behind an await that never arrives (see settleAwaitedMusic).
// Off keeps the stream playing with its volume down, so flipping back on restores it in place.
func (a *App) applyMusicAcrossTabs(on bool) {
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			MusicAcrossTabs =

			on

	})

	if on {
		a.musicTabDucked = false
		a.musicAwaitURL = ""
		a.musicAwaitSince = time.Time{}
		a.applyAudioVolumes()
	} else {
		a.musicTabDucked = true
		a.applyAudioVolumes()
	}
}

// musicResumeMaxSeekSec caps the resume seek only when no takeover snapshot exists, so the duration is unknown:
// past this a seek likely overshoots the end. The snapshot path in resumeSeek wraps by real duration instead.
const musicResumeMaxSeekSec = 600 // 10 minutes

// resumeSeek returns where to seek a resumed stream from a swap-out snapshot and whether to play it at all.
// Unknown duration restarts from 0, because a clock seek could land past the end. A loop wraps by duration.
// A non-looping track that ended while parked does not play. Negative elapsed clamps to 0: a negative math.Mod
// would fail the seek and silently restart.
func resumeSeek(snapPos, snapDur, elapsedSec float64, loop bool) (seekSec float64, play bool) {
	if elapsedSec < 0 {
		elapsedSec = 0
	}
	if snapDur <= 0 {
		return 0, true // unknown length: honest restart-from-top
	}
	base := snapPos + elapsedSec
	if loop {
		return math.Mod(base, snapDur), true // multi-wrap is fine
	}
	if base >= snapDur {
		return 0, false // one-shot finished while parked: don't restart
	}
	return base, true
}

// resumeActiveTabMusic re-seeds the active tab's area song into the single stream on a room build.
// The same track just un-ducks, picking up where it was; a different one is fetched and seeks to where it would be.
// With StreamMusic off nothing is fetched and only Now-Playing is tracked.
func (a *App) resumeActiveTabMusic() {
	track := a.sess.MusicTrack
	if track == "" {
		return
	}
	a.room.Scene.MusicTrack = track
	if !a.room.StreamMusic || a.d.Audio == nil {
		return // §1.3: OFF fetches/plays nothing; no device = nothing to duck or seek
	}
	// The MC's loop flags are not kept on the session, so a resumed area track loops on the client.
	// No fade (effects = 0): to the listener the track was already playing.
	const resumeLoop = true
	const resumeEffects = 0
	url := a.urls.MusicURL(track)
	sameTrack := a.d.Audio.CurrentMusicURL() == url && a.d.Audio.MusicPlaying()
	if sameTrack {
		// The stream is already this track, kept rolling across the switch, so no delivery is coming: un-duck now.
		a.applyResumeDuck(true, url)
		return
	}
	// Decide seekSec and whether to play first, so an ended one-shot does not leave a dangling await below.
	seekSec := 0.0
	play := true
	if snapPos, snapDur, snapAt, ok := a.d.Audio.SwappedOutSnap(url); ok {
		// Exact path: the snapshot position plus the time played during the download, wrapped by real duration.
		elapsed := a.now().Sub(snapAt).Seconds()
		seekSec, play = resumeSeek(snapPos, snapDur, elapsed, resumeLoop)
	} else if !a.musicStartedAt.IsZero() {
		// No snapshot (resolver miss, mod or midi with unknown length, or a first play): capped wall-clock estimate.
		if el := a.now().Sub(a.musicStartedAt).Seconds(); el > 0 && el <= musicResumeMaxSeekSec {
			seekSec = el
		}
	}
	if !play {
		// The parked one-shot ended, reachable only if resumeLoop is false. Leave the stream and clear the await:
		// nothing will be delivered: a held await would duck this stream forever.
		a.musicAwaitURL = ""
		a.musicAwaitSince = time.Time{}
		a.musicTabDucked = false
		a.applyAudioVolumes()
		return
	}
	// Hold the duck and set the await before the fetch, so settleAwaitedMusic un-ducks only when the bytes land.
	a.applyResumeDuck(false, url)
	a.d.Audio.PlayMusicAt(url, resumeLoop, resumeEffects, seekSec) // AssetType: Music
}

// applyResumeDuck is the duck decision: on the same track un-duck now; otherwise hold the duck and await url,
// because un-ducking would make the foreign stream audible during the fetch.
func (a *App) applyResumeDuck(sameTrack bool, url string) {
	if sameTrack {
		a.musicAwaitURL = ""
		a.musicAwaitSince = time.Time{}
		a.musicTabDucked = false
	} else if a.musicTabDucked {
		// Arm only when there is a duck to hold, like the DJ /play path. Under MusicAcrossTabs it is already down,
		// and a track that never arrives would trip settleAwaitedMusic's timeout, silencing an audible stream.
		// There is no reliable 404 signal at release time, so this must be decided when arming.
		a.musicAwaitURL = url       // un-duck only once THIS track is the one playing
		a.musicAwaitSince = a.now() // stamp for the never-arrives timeout release
	}
	// The active tab now owns the stream, so its per-server music volume governs it.
	a.musicOwnerKey = a.serverKey
	a.applyAudioVolumes()
}

// activeCharName is the character folder outgoing messages use: the iniswap override if set (AO2-Client set_iniswap
// semantics, where the server keeps the list character), else our picked character.
func (a *App) activeCharName() string {
	if a.iniChar != "" {
		return a.iniChar
	}
	return a.myCharName()
}

// setIniswap applies a custom character override ("" reverts) and reloads the emote list.
func (a *App) setIniswap(name string) {
	a.iniChar = name
	a.charDefaultSide = "" // the new folder's char.ini decides the default side; until it lands there is none
	a.emoteAsk = nil
	a.emoteIdx, a.emoteRow = 0, 0 // new active folder = new emote list: start at the first
	a.loadCharINI()
}

// wardrobeAct is the resolved action for clicking a Characters-tab favourite.
type wardrobeAct int

const (
	actSwitch  wardrobeAct = iota // take the character's slot (a real char change)
	actIniswap                    // wear its look without taking a slot
)

// wardrobeAction switches by default, and iniswaps only in iniswap mode, for a non-server name (slot < 0), or if taken.
func wardrobeAction(iniswapMode bool, slot int, taken bool) wardrobeAct {
	if iniswapMode || slot < 0 || taken {
		return actIniswap
	}
	return actSwitch
}

// charSlotByName finds the character whose folder matches case-insensitively, or -1.
func (a *App) charSlotByName(name string) int {
	want := strings.ToLower(strings.TrimSpace(name))
	for i := range a.sess.Chars {
		if strings.ToLower(a.sess.Chars[i].Name) == want {
			return i
		}
	}
	return -1
}

// wardrobeClick switches to a favourite or iniswaps it per wardrobeAction, with a toast when a switch falls back.
func (a *App) wardrobeClick(name string) {
	a.showIni = false
	if a.room == nil {
		// Char-select Wardrobe tab: same decision as the courtroom path below.
		slot := -1
		taken := false
		if a.sess != nil {
			slot = a.charSlotByName(name)
			taken = slot >= 0 && a.sess.Chars[slot].Taken
		}
		if wardrobeAction(a.iniSwapMode, slot, taken) == actSwitch {
			a.pickCharacter(slot)
			return
		}
		a.wearFromMenu(name)
		return
	}
	slot := a.charSlotByName(name)
	taken := slot >= 0 && a.sess.Chars[slot].Taken
	if wardrobeAction(a.iniSwapMode, slot, taken) == actSwitch {
		a.pickCharacter(slot) // CC → PV → EventCharPicked → enterCourtroom (iniswap clears)
		return
	}
	if !a.iniSwapMode { // a switch was wanted but impossible — say why, don't swap silently
		switch {
		case slot < 0:
			a.warnLine = name + " isn't a character on this server — worn as an iniswap"
		default:
			a.warnLine = name + " is taken — worn as an iniswap"
		}
		a.warnAt = time.Now()
	}
	a.wearFromMenu(name)
}

// wearFromMenu handles a wardrobe pick; on char select an iniswap still needs a server character, so claim the first
// free one and wear the custom when PV confirms (enterCourtroom applies pendingIni).
func (a *App) wearFromMenu(name string) {
	a.showIni = false
	if a.room != nil {
		a.setIniswap(name)
		a.screen = ScreenCourtroom
		return
	}
	free := -1
	for i := range a.sess.Chars {
		if !a.sess.Chars[i].Taken {
			free = i
			break
		}
	}
	if free < 0 {
		a.warnLine = "No free character slots to host an iniswap — every slot is taken"
		a.warnAt = time.Now()
		return
	}
	a.pendingIni = name
	a.pickCharacter(free) // rehearsal resolves locally
}

func (a *App) openIniswap() {
	a.showIni = true
	a.ensureIniList()
	if a.wardSection == wardSectionBackgrounds {
		a.rebuildBgFav() // favourites may have changed since this section last drew
	}
}

// ensureIniList makes the wardrobe menu current: prefs render at once, and iniswap.txt merges in when its fetch lands.
func (a *App) ensureIniList() {
	a.rebuildIniMenu() // wardrobe is local: usable before (or without) the txt
	if a.iniServer != nil || a.iniBusy || a.urls.Origin() == "" {
		return
	}
	a.iniBusy = true
	a.iniListErr = ""
	url := a.urls.Origin() + iniswapFileName
	bgURL := a.urls.BackgroundsRoot()
	key := a.serverKey
	mgr := a.d.Manager
	ch := a.iniRes
	go func() {
		// A malformed autoindex in parseAutoindexDirs must degrade, never crash off-thread.
		defer func() {
			if r := recover(); r != nil {
				writeCrashLog("iniswap list goroutine panic: ", r)
				ch <- iniswapFetch{key: key, err: fmt.Errorf("iniswap list failed: %v", r)}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		data, err := mgr.FetchRaw(ctx, url)
		if err != nil {
			ch <- iniswapFetch{key: key, err: err}
			return
		}
		names := parseIniswapList(data)
		// Fetched only to filter the list; a host with no listing is not an error (nil).
		var bgs []string
		if bgData, bgErr := mgr.FetchRaw(ctx, bgURL); bgErr == nil {
			bgs = parseAutoindexDirs(bgData)
		}
		ch <- iniswapFetch{key: key, names: names, bgs: bgs}
	}()
}

func (a *App) pollIniswap() {
	select {
	case res := <-a.iniRes:
		if res.key == a.serverKey {
			a.applyIniswap(&a.sessionState, res)
			a.rebuildIniMenu()
			return
		}
		// Landed after a tab switch: left undelivered, the parked tab stays busy and never fetches its list again.
		for _, tab := range a.tabs {
			if tab.state.serverKey == res.key && tab.state.iniBusy {
				a.applyIniswap(&tab.state, res)
				return
			}
		}
	default:
	}
}

func (a *App) applyIniswap(s *sessionState, res iniswapFetch) {
	s.iniBusy = false
	if res.err != nil {
		s.iniListErr = "no server list (" + res.err.Error() + ") — your wardrobe still works"
		s.iniServer = nil
		return
	}
	s.iniServer = filterServerIniswaps(res.names, res.bgs,
		a.d.Prefs.Load().ServerWarmInfoFor(res.key).Backgrounds)
}

// rebuildIniMenu merges the menu: wardrobe favourites first, then server entries not already in the wardrobe.
func (a *App) rebuildIniMenu() {
	names, fromWardrobe, inServer := mergeWardrobe(a.d.Prefs.Load().WardrobeList(a.serverKey), a.iniServer)
	a.iniList = names
	a.iniWardrobe = fromWardrobe
	a.iniServerMem = inServer
	a.iniLower = make([]string, len(names))
	folders := a.d.Prefs.Load().WardrobeFolderMap(a.serverKey)
	a.iniFolders = make([]string, len(names))
	a.iniHoverIDs = make([]string, len(names))
	for i, n := range names {
		lower := strings.ToLower(n)
		a.iniLower[i] = lower
		a.iniFolders[i] = folders[lower] // "" when unsorted / not filed
		a.iniHoverIDs[i] = iniHoverIDPrefix + n
	}
	a.iniAsk = nil
	// cachedPage keys icons by index without checking the URL, so a same-length reorder would paint the wrong icon.
	a.iniPages = nil
}

// iniHoverIDPrefix keeps wardrobe hover ids apart from the Characters grid's: the wardrobe modal can show over it.
const iniHoverIDPrefix = "iniswap:"

// iniHoverID is wardrobe entry i's cached hover id; out of range it rebuilds, since a draw loop must not panic.
func (a *App) iniHoverID(i int) string {
	if i >= 0 && i < len(a.iniHoverIDs) {
		return a.iniHoverIDs[i]
	}
	if i < 0 || i >= len(a.iniList) {
		return iniHoverIDPrefix
	}
	return iniHoverIDPrefix + a.iniList[i]
}

// applyPendingFav applies a deferred star toggle; must run after the grid loop, since it resizes the slices it ranges.
func (a *App) applyPendingFav() {
	if a.iniFavPending == "" {
		return
	}
	name := a.iniFavPending
	a.iniFavPending = ""
	if a.iniFavPendingAdd {
		a.d.Prefs.AddWardrobe(a.serverKey, name)
	} else {
		a.d.Prefs.RemoveWardrobe(a.serverKey, name)
	}
	a.rebuildIniMenu()
}

// ensureWardrobeMembers rebuilds the server's lowercased wardrobe set on change, reusing the map to avoid allocating.
func (a *App) ensureWardrobeMembers() {
	gen := a.d.Prefs.WardrobeGeneration()
	if a.wardrobeMembers != nil && a.wardrobeMembersFor == a.serverKey && a.wardrobeMembersGen == gen {
		return
	}
	if a.wardrobeMembers == nil {
		a.wardrobeMembers = make(map[string]bool)
	} else {
		clear(a.wardrobeMembers)
	}
	for _, name := range a.d.Prefs.Load().WardrobeList(a.serverKey) {
		a.wardrobeMembers[strings.ToLower(name)] = true
	}
	a.wardrobeMembersFor = a.serverKey
	a.wardrobeMembersGen = gen
}

// mergeWardrobe lists favourites first (starred), then server entries they did not cover, case-insensitively.
func mergeWardrobe(wardrobe, server []string) (names []string, stars, inServer []bool) {
	names = make([]string, 0, len(wardrobe)+len(server))
	stars = make([]bool, 0, len(wardrobe)+len(server))
	inServer = make([]bool, 0, len(wardrobe)+len(server))
	serverSet := make(map[string]struct{}, len(server))
	for _, n := range server {
		serverSet[strings.ToLower(n)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(wardrobe))
	sort.SliceStable(wardrobe, func(i, j int) bool {
		return strings.ToLower(wardrobe[i]) < strings.ToLower(wardrobe[j])
	})
	for _, n := range wardrobe {
		key := strings.ToLower(n)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, n)
		stars = append(stars, true)
		_, srv := serverSet[key]
		inServer = append(inServer, srv) // a favourite that's also a server iniswap
	}
	for _, n := range server {
		if _, dup := seen[strings.ToLower(n)]; dup {
			continue
		}
		names = append(names, n)
		stars = append(stars, false)
		inServer = append(inServer, true)
	}
	return names, stars, inServer
}

// filterServerIniswaps drops background folder names, since some servers publish one combined txt.
// A real character sharing a background's name is hidden from the grids but can still be worn by name.
func filterServerIniswaps(names, liveBgs, warmBgs []string) []string {
	if len(liveBgs) == 0 && len(warmBgs) == 0 {
		return names
	}
	bgSet := make(map[string]struct{}, len(liveBgs)+len(warmBgs))
	for _, b := range liveBgs {
		bgSet[strings.ToLower(b)] = struct{}{}
	}
	for _, b := range warmBgs {
		bgSet[strings.ToLower(b)] = struct{}{}
	}
	kept := names[:0] // in place: names is pollIniswap's own parse result
	for _, n := range names {
		if _, isBg := bgSet[strings.ToLower(n)]; !isBg {
			kept = append(kept, n)
		}
	}
	return kept
}

// parseIniswapList parses one character folder per line: deduped case-insensitively, capped and sorted.
func parseIniswapList(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	names := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
		if len(names) >= iniswapListCap {
			break
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}

// cachedPage resolves a grid cell's texture page through a generation-keyed cache: evictions bump the generation
// and textures are destroyed only after that, so pointers are valid while it holds.
func (a *App) cachedPage(pages *[]*render.TexturePage, gen *uint64, n, idx int, base string) (*render.TexturePage, bool) {
	g := a.d.Store.Generation()
	if len(*pages) != n {
		*pages = make([]*render.TexturePage, n)
		*gen = g
	} else if *gen != g {
		for i := range *pages {
			(*pages)[i] = nil
		}
		*gen = g
	}
	if idx < 0 || idx >= n {
		return nil, false
	}
	if p := (*pages)[idx]; p != nil {
		return p, true
	}
	page, ok := a.d.Store.Get(base)
	if !ok {
		return nil, false
	}
	(*pages)[idx] = page
	return page, true
}

// demandAsset paces one visible cell's asset asks, reporting false only if every format was probed to exhaustion.
// That check precedes the pacing: a 404'd base never becomes resident, so asks would otherwise repeat forever.
// "Not asked yet" still answers true.
func (a *App) demandAsset(stamps *[]time.Time, n, idx int, base string, t assets.AssetType) bool {
	if idx < 0 || idx >= n {
		return false
	}
	if a.d.Manager.IsConclusiveMiss(base, t) {
		return false
	}
	if a.iconAskBudget <= 0 {
		return true // budget spent this frame, not a verdict on the asset
	}
	if len(*stamps) != n {
		*stamps = make([]time.Time, n)
	}
	now := time.Now()
	if now.Sub((*stamps)[idx]) < charIconRetryInterval {
		return true
	}
	(*stamps)[idx] = now
	a.iconAskBudget--
	a.d.Manager.Prefetch(base, t, network.PriorityLow)
	return true
}

// charINIURL builds a character's char.ini location.
//
// Through CharFolder, which alone decides casing, per-segment escaping and nested names, so the cache key matches.
func (a *App) charINIURL(name string) string {
	return a.urls.CharFolder(name) + charINIFileName
}

// warmCharINI speculatively fetches a hovered character's char.ini so the eventual pick is a memory hit.
func (a *App) warmCharINI(name string) {
	if name == "" || name == a.iniWarmed || a.urls.Origin() == "" {
		return
	}
	a.iniWarmed = name
	a.d.Manager.PrefetchRaw(a.charINIURL(name), network.PriorityLow) // raw text: char.ini
}

// previewEmoteCap bounds one previewed character's emote cycle (hard rule 4).
const previewEmoteCap = 256

// previewIdleAnim is the conventional resting pose both preview surfaces probe first on hover (webAO URL convention);
// packs without it are corrected from their char.ini.
const previewIdleAnim = "normal"

// ensurePreviewEmotes' cycle argument.
const (
	previewEmoteNav      = true  // wardrobe: drives the ‹ › try-before-wear navigator
	previewEmotePortrait = false // char-select grid: one hover portrait, no navigator
)

// ensurePreviewEmotes loads a previewed character's emote list once, parsing off-thread into previewEmoteRes.
func (a *App) ensurePreviewEmotes(name string, cycle bool) {
	if name == "" || name == a.previewChar || a.urls.Origin() == "" {
		return
	}
	a.previewChar = name
	a.previewEmoteCycle = cycle
	a.previewAnims = nil
	a.previewLabels = nil
	a.previewEmoteIdx = 0
	url := a.charINIURL(name)
	key := a.serverKey
	mgr := a.d.Manager
	ch := a.previewEmoteRes
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		// Layered: a character from the user's own pack must cycle their emotes, not the server's.
		data, err := mgr.FetchRawLayered(ctx, url)
		if err != nil {
			return
		}
		ini, err := courtroom.ParseCharINI(data)
		if err != nil || ini == nil {
			return
		}
		anims := make([]string, 0, len(ini.Emotes))
		labels := make([]string, 0, len(ini.Emotes))
		for i, e := range ini.Emotes {
			if i >= previewEmoteCap {
				break
			}
			anims = append(anims, e.Anim)
			labels = append(labels, e.Comment)
		}
		ch <- previewEmoteFetch{key: key, char: name, anims: anims, labels: labels}
	}()
}

// pollPreviewEmotes drains a finished parse. Every screen that can call ensurePreviewEmotes must call this,
// or the parse goroutine blocks on the one-element channel.
func (a *App) pollPreviewEmotes() {
	select {
	case res := <-a.previewEmoteRes:
		if res.key != a.serverKey || res.char != a.previewChar {
			return
		}
		a.previewAnims = res.anims
		a.previewLabels = res.labels
		a.previewEmoteIdx = 0
		if len(res.anims) == 0 {
			return
		}
		if !a.previewEmoteCycle {
			// Char-select portrait: the box outlives the trigger cell, so a late parse must correct it here.
			a.correctPortraitPreview(res.char)
			return
		}
		if a.previewBase == a.urls.Emote(res.char, previewIdleAnim, courtroom.EmoteIdle) {
			a.setPreviewEmote(0)
		}
	default:
	}
}

// previewPortraitAnim returns the emote the char-select grid shows: "normal", or the pack's first emote once the
// parsed char.ini lacks it. Until then it answers "normal", so a common pack costs one probe.
func (a *App) previewPortraitAnim(name string) string {
	if name != a.previewChar || len(a.previewAnims) == 0 {
		return previewIdleAnim // not parsed (yet): the optimistic convention
	}
	for _, anim := range a.previewAnims {
		// Case-insensitive: emote anims are lowercased into the URL (segPath), so "Normal" is the base already probed.
		if strings.EqualFold(anim, previewIdleAnim) {
			return previewIdleAnim
		}
	}
	return a.previewAnims[0]
}

// setCharPortraitPreview points the hover box at name's portrait; idempotent, as HoverPreview fires every frame.
func (a *App) setCharPortraitPreview(name string) {
	anim := a.previewPortraitAnim(name)
	a.previewBase = a.urls.Emote(name, anim, courtroom.EmoteIdle)
	a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(name, anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
}

// correctPortraitPreview moves an open portrait only if "normal" is still shown and the pack has no such emote.
func (a *App) correctPortraitPreview(name string) {
	if a.previewBase != a.urls.Emote(name, previewIdleAnim, courtroom.EmoteIdle) {
		return
	}
	if a.previewPortraitAnim(name) == previewIdleAnim {
		return
	}
	a.setCharPortraitPreview(name)
}

// setPreviewEmote points the preview box at emote i (wrapping), in the talk (b) pose.
func (a *App) setPreviewEmote(i int) {
	n := len(a.previewAnims)
	if n == 0 || a.previewChar == "" {
		return
	}
	a.previewEmoteIdx = ((i % n) + n) % n
	anim := a.previewAnims[a.previewEmoteIdx]
	a.previewBase = a.urls.Emote(a.previewChar, anim, courtroom.EmoteTalk)
	a.d.Manager.PrefetchChain(a.previewBase, a.urls.EmoteAlts(a.previewChar, anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview cycle)
}

func (a *App) cyclePreviewEmote(delta int) { a.setPreviewEmote(a.previewEmoteIdx + delta) }

// clampEmoteSel keeps a still-valid emote index and row across a reload, and otherwise zeroes both.
func clampEmoteSel(idx, row, n int) (int, int) {
	if idx < 0 || idx >= n {
		return 0, 0
	}
	return idx, row
}

// loadCharINI fetches the active character's char.ini, using the iniswap override if set.
func (a *App) loadCharINI() {
	name := a.activeCharName()
	if a.sess == nil || name == "" {
		return
	}
	url := a.charINIURL(name)
	a.charINIBusy = true
	key := a.serverKey
	mgr := a.d.Manager
	ch := a.charINIres
	go func() {
		// Layered: a character from the user's own folders must bring its own emote list.
		data, err := mgr.FetchRawLayered(context.Background(), url)
		if err != nil {
			ch <- charINIFetch{key: key, err: err}
			PushWake() // wake the event-driven loop so Background drains this at idle=0
			return
		}
		ini, err := courtroom.ParseCharINI(data)
		ch <- charINIFetch{key: key, ini: ini, err: err}
		PushWake() // wake the event-driven loop so Background drains this at idle=0
	}()
}

func (a *App) pollCharINI() {
	if a.pinnedPass {
		return // the full-theme pinned render must not drain the primary's char.ini result
	}
	select {
	case res := <-a.charINIres:
		if res.key != a.serverKey {
			return // landed after a tab switch: another server's char.ini
		}
		a.charINIBusy = false
		a.uiDirty = true // the emote list just changed (and "Loading emotes…" must clear): force one redraw, like the character list does on its packet
		// The page caches key by index, so a same-length iniswap would show the old character's buttons.
		a.emoteBtnOff, a.emoteBtnOn, a.emoteIconPages = nil, nil, nil
		if res.err != nil || res.ini == nil {
			// Say why the emote list is a bare default.
			reason := "empty char.ini"
			if res.err != nil {
				reason = res.err.Error()
			}
			a.warnLine = clampLine("char.ini for " + a.activeCharName() + ": " + reason + " — using a default emote")
			a.warnAt = time.Now()
			a.emotes = []courtroom.Emote{{Comment: "normal", Anim: "normal", Preanim: "-"}}
			a.emoteIdx, a.emoteRow = clampEmoteSel(a.emoteIdx, a.emoteRow, len(a.emotes))
			// Clear it, or the previous character's default survives and pos_remove offers a /pos it never asked for.
			// AO2 returns "wit" for an unreadable ini (text_file_functions.cpp:480-483), like defaultSide().
			a.charDefaultSide = ""
			return
		}
		a.emotes = res.ini.Emotes
		if len(a.emotes) == 0 {
			a.emotes = []courtroom.Emote{{Comment: "normal", Anim: "normal", Preanim: "-"}}
		}
		// Our side comes from our char.ini (AO2-Client semantics), never from whoever spoke last; /pos overrides it.
		// charDefaultSide is AO2's default_side (courtroom.cpp:4723), set even when empty to not survive an iniswap.
		side := strings.ToLower(strings.TrimSpace(res.ini.Side))
		a.charDefaultSide = side
		if side != "" {
			a.sidePref = side
		}
		a.charBlips = res.ini.Blips
		// Custom shout menu: the base "custom" entry (renamed by custom_name) plus the named 2.10 interjections.
		a.customShouts = res.ini.CustomShouts
		a.customIdx = -1 // base custom
		a.customName = res.ini.CustomName
		// Keep the selection across a reactivation reload. A real character change zeroes these before the fetch.
		a.emoteIdx, a.emoteRow = clampEmoteSel(a.emoteIdx, a.emoteRow, len(a.emotes))
		a.emoteAsk = nil // fresh char: re-demand its button art from scratch
		// LOW priority sheds under load. The bundle toggle fetches every emote's idle and talk; off, the first few.
		me := a.myCharName()
		if a.d.Prefs.Load().CharBundlePrefetch {
			for _, e := range a.emotes {
				a.d.Manager.PrefetchChain(a.urls.Emote(me, e.Anim, courtroom.EmoteIdle), a.urls.EmoteAlts(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
				a.d.Manager.PrefetchChain(a.urls.Emote(me, e.Anim, courtroom.EmoteTalk), a.urls.EmoteAlts(me, e.Anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
			}
		} else {
			for i, e := range a.emotes {
				if i >= 8 {
					break
				}
				// A mixed-naming pack may ship this emote unprefixed; one spelling would 404 and flag it missing.
				a.d.Manager.PrefetchChain(a.urls.Emote(me, e.Anim, courtroom.EmoteIdle), a.urls.EmoteAlts(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite
			}
		}
	default:
	}
}

func (a *App) myCharName() string {
	if a.sess == nil || a.sess.MyCharID < 0 || a.sess.MyCharID >= len(a.sess.Chars) {
		return ""
	}
	return a.sess.Chars[a.sess.MyCharID].Name
}
