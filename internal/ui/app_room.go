package ui

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

func (a *App) buildRoom() {
	if a.link.sess == nil {
		return
	}
	a.link.room = a.newRoom(a.link.urls, a.link.sess, a.d.Audio) // construction IS wiring (newroom.go): char.ini callbacks, the overlay engine, the visual prefs
	// Per-server audio: apply THIS server's volume profile (or the global one) now,
	// so the music re-seeded below plays at the right level and switching between
	// two in-court tabs carries each server's own volumes / muted blips.
	a.applyAudioVolumes()
	urls := a.link.urls
	mgr := a.d.Manager
	a.link.room.Predictor = assets.NewPrefetcher(func(character, emote string) {
		if emote == "" {
			emote = "normal" // no chain signal yet: the default loop
		}
		// Warm BOTH the idle and the (b) talk sprite through the full spelling
		// chain — bare-named packs only answer the bare/(b) spellings, and the
		// talking loop is the sprite seen FIRST when the predicted speaker
		// starts. Speculative variant: a total miss must NOT raise a missing-
		// asset warning for a sprite no one demanded. PriorityLow so the
		// speculation sheds under backpressure (§10).
		idle := urls.Emote(character, emote, courtroom.EmoteIdle)
		talk := urls.Emote(character, emote, courtroom.EmoteTalk)
		mgr.PrefetchChainSpeculative(idle, urls.EmoteAlts(character, emote, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker, idle)
		mgr.PrefetchChainSpeculative(talk, urls.EmoteAlts(character, emote, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (predicted next speaker, talk)
	})
	a.link.room.Predictor.SetAggressiveness(a.d.Prefs.Load().PrefetchAggressiveness()) // #100 predictive-prefetch level
	a.d.Viewport.OnPreanimDone = a.link.room.NotifyPreanimDone
	a.d.Viewport.OnPreanimStart = a.link.room.NotifyPreanimStarted // extend the fallback for long decoded preanims (phase-guarded — safe if a preview later shares this viewport)
	// #17 frame-synced effects: OnFrameShown reports the CURRENTLY-PLAYING sprite's
	// frame, so it must track the room that OWNS the on-screen sprite — the same
	// convention as OnPreanimDone (swapped by replay/maker/gifexport), NOT the
	// phase-guarded OnPreanimStart. NotifyFrameShown has no stale-safe guard, so a
	// mis-pointed callback would feed a replay sprite's frames into the live room's
	// trigger table and fire its SFX.
	a.d.Viewport.OnFrameShown = a.link.room.NotifyFrameShown
	if a.link.sess.Background != "" {
		a.link.room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: a.link.sess.Background})
	}
	// Force the SHARED viewport's scenery layers onto THIS room's background/desk now.
	// buildRoom runs on a tab switch, and the viewport is App-global: without this the
	// sticky scenery gate (syncAnimSticky keeps the last good bg until the new one is
	// resident) would keep painting the PREVIOUS tab's background under the newly-
	// activated tab (the "both tabs show the same background" bug). Characters already
	// restore correctly because their layers reset unconditionally (syncAnim); scenery
	// is the sticky exception, so it needs this explicit rebind at the rebuild seam. A
	// brief blank of the CORRECT base (the HIGH-priority setBackground prefetch above +
	// the held:// bridge fill it a frame later) beats the WRONG base persisting.
	a.d.Viewport.RebindScenery(a.link.room.Scene.BackgroundBase, a.link.room.Scene.DeskBase)
	// Resume the area's current song into the fresh room — symmetric with the background
	// above. The track was announced (MC) before this room existed (the join handshake, or
	// a tab reactivation), so without this re-seed the music fell silent on (re)entry while
	// the background came back.
	if a.link.sess.MusicTrack != "" {
		// Sync the streaming toggle BEFORE the resume: applyTimingToRoom (which
		// normally pushes it) runs below, but this resume event fires first — with
		// StreamMusic left at NewCourtroom's default ON, a resumed track would play
		// even when the user disabled streaming. §1.3.
		a.link.room.StreamMusic = a.d.Prefs.Load().MusicStreaming
		a.resumeActiveTabMusic()
	}
	a.applyTimingToRoom()
	a.pushRealizationToRoom()
	// The third session→room re-seed, beside the background and the song: the
	// last IC message, re-staged SETTLED (idle sprite, full text, no ceremony).
	// A rebuilt room used to come back BLANK even though the stage was
	// mid-conversation when it parked (tab switch) or messages landed while
	// backgrounded / at char select; now it shows what a live watcher would
	// have ended on. AFTER applyTimingToRoom on purpose: begin() bakes
	// ForceCharNames / HideSpriteStyles / ReduceMotion into the scene
	// (showname vs char name, transmitted styles, motion strip), so the
	// restore must run under the user's prefs, not NewCourtroom's zero values.
	if m := a.stageRestoreMsg(); m != nil {
		a.link.room.RestoreMessage(m)
	}
	a.d.Prefs.RememberServerChar(a.srvMeta.serverKey, a.myCharName())
	// Court-extras state is per-room: splash/badge clear, penalty-sfx
	// direction tracking re-arms at the session's current bar values.
	a.courtxtra.wtceName, a.courtxtra.testimonyOn = "", false
	a.courtxtra.hpPrev = [2]int{a.link.sess.HPDef, a.link.sess.HPPro}
	a.courtxtra.showModcall, a.modUI.modReason = false, ""
	a.evid.showEvid, a.evid.evidEditing, a.evid.evidPresent, a.evid.evidIdx = false, false, false, -1
	a.evid.evidPresentImage = ""
	a.evid.evidCtxMenu = false
	a.evid.evidAsk = nil
	a.evid.evShowImg = ""
	a.screen = ScreenCourtroom
	a.loadCharINI()
	a.updatePresence()                // character (and server) just became known
	a.roster.liveDetailsArea = "\x00" // force a fresh rich-roster pull for the new session
	a.rebuildLiveRoster()             // seed the live player list from the handshake's CharsCheck
}

// stageRestoreMsg is the message buildRoom re-stages into a rebuilt room — the
// session's last IC message, unless the ACTIVE session's ignore list drops its
// speaker (parity with handleSessionEvents, which `continue`s past
// room.HandleEvent for ignored speakers — #81: no log, no sprite). nil = leave
// the stage blank. Split out so the gate is unit-pinnable without a room.
func (a *App) stageRestoreMsg() *protocol.ChatMessage {
	if a.link.sess == nil {
		return nil
	}
	m := a.link.sess.LastIC
	if m == nil || a.ignoreSpeaker(m) {
		return nil
	}
	return m
}

// --- multi-server floating client (passes 2a–2c + the float rework) ---------
// A pinned BACKGROUND tab renders as a free-floating, movable + resizable "second
// client" window (clientWin, the floatWin pattern) overlaying the full-size
// primary courtroom — drag it ANYWHERE, resize it, overlap the primary however
// you like (a strict superset of the old fixed split). It carries that server's
// live stage + chat log + IC input; you type into either client (click to focus).

const splitInputH = 30 // pinned client's IC input row height (pass 2c)
const (
	clientWinDefW = 520 // floating client window default size
	clientWinDefH = 470
	clientWinMinW = 360 // resize floor: stage + log + input stay usable
	clientWinMinH = 300
	// Full-theme view zoom: 1 = the whole client shrunk to fit the window, up to
	// clientZoomMax magnification (scroll to zoom around the cursor, drag to pan).
	clientZoomMin  = 1.0
	clientZoomMax  = 6.0
	clientZoomStep = 1.15 // wheel multiplier per notch
	// clientClickSlop is the max cursor travel (logical px) for a press+release on the
	// floating client to count as a CLICK ("control this server") rather than a pan drag.
	clientClickSlop = 4
)

// splitActive reports whether the floating client window should render: a pinned
// background tab with a live session + room (and not disconnected).
func (a *App) splitActive() bool {
	return a.splitTab != nil && a.splitRoom != nil && a.splitVP != nil &&
		a.splitTab.state.link.sess != nil && !a.splitTab.dead
}

// clearSplit tears the floating client down (keeps the viewport for reuse) and
// frees the full-theme render target + the pinned tab's chat raster (built by the
// full-view pass; it lives in the tab's sessionState, so this tab is its sole owner).
func (a *App) clearSplit() {
	if a.splitTab != nil && a.splitTab.state.raster.msRaster != nil {
		a.splitTab.state.raster.msRaster.Destroy()
		a.splitTab.state.raster.msRaster = nil
	}
	a.splitTab, a.splitRoom = nil, nil
	if a.ctx != nil && a.ctx.focusID == "ic-split" {
		a.ctx.focusID = "" // the client's field is gone — don't leave focus dangling on it
	}
	if a.client.clientTex != nil {
		_ = a.client.clientTex.Destroy()
		a.client.clientTex = nil
	}
}

// controlPinnedClient makes the floating (pinned) server the LIVE, fully interactive
// courtroom and demotes the current primary into the float — the "click to control"
// gesture. The two servers trade places; both stay on screen, you drive whichever you
// clicked. Reuses the proven tab machinery (activateTab + pinToSplit), so the new
// primary is the REAL courtroom — real emotes, IC bar, every button — with no second
// interactive-render machinery (which would collide widget ids / leak App-global state).
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

// clientControlClick requests a control-swap when the floating client's view rect gets
// a CLICK (press+release with little travel — a drag there pans/zooms instead). The
// swap is deferred to the next frame top (pendingControlSwap) so we never mutate the
// active session/room mid-render.
func (client1 *clientState) clientControlClick(e *env, view sdl.Rect) {
	c := e.ctx
	if c.clicked && !client1.clientPanning && pointIn(c.mouseX, c.mouseY, view) &&
		absInt(int(c.mouseX-c.downX))+absInt(int(c.mouseY-c.downY)) <= clientClickSlop {
		client1.pendingControlSwap = true
	}
}

// pinToSplit pins a BACKGROUND tab as the floating client window (toggle:
// re-pinning the same tab closes it). Builds a throwaway room over the tab's OWN
// session + URL builder with NopAudio, plus a second viewport over the shared
// store, and seeds the current background + song + last message (settled) so
// the window isn't blank on pin.
func (a *App) pinToSplit(t *courtTab) {
	if t == nil || t.state.link.sess == nil || t.dead {
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
	// A different tab is now the pinned pane: bump the pin epoch so the split-wrap
	// row cache (keyed by the pinned session's icLogSeq) can't serve the previously
	// pinned tab's rows when the new tab's seq happens to match (same seq-collision
	// class as the main log caches — see logViewEpoch / splitPinEpoch).
	a.splitPinEpoch++
	// Through newRoom like every other mode (newroom.go). The pinned pane used to
	// be built raw, so it was one of the two rooms that never got the overlay
	// engine: an effect played in the tab you were watching and not in the pane
	// beside it. NopAudio keeps the pane silent; the visual half is now identical.
	a.splitRoom = a.newRoom(t.state.link.urls, t.state.link.sess, courtroom.NopAudio{})
	// The pinned pane is a DIFFERENT server's session, so a wiping BN there must
	// re-scene at THAT tab's side, never the active tab's (#23). Reading through t
	// keeps it correct for the pane's whole lifetime; a.mySide would be the wrong
	// tab entirely.
	a.splitRoom.Env = envResolver{a: a, side: func() string {
		if t.state.compose.sidePref != "" {
			return t.state.compose.sidePref
		}
		return defaultSide
	}}
	a.splitVP.OnPreanimDone = a.splitRoom.NotifyPreanimDone
	a.splitVP.OnPreanimStart = a.splitRoom.NotifyPreanimStarted
	a.splitVP.OnFrameShown = a.splitRoom.NotifyFrameShown                     // #17 (NopAudio here → shake/flash only; sfx is silent for the pinned view)
	a.client.clientZoom, a.clientPanX, a.clientPanY = clientZoomMin, 0.5, 0.5 // fresh pin starts at fit
	if t.state.link.sess.Background != "" {
		a.splitRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: t.state.link.sess.Background})
	}
	// The split viewport is REUSED across pins (created once above), so force its
	// scenery onto THIS pinned room's background/desk — otherwise the sticky gate
	// keeps the previously pinned tab's background under the new one (same cross-view
	// scenery-hold bug the primary viewport had in buildRoom).
	a.splitVP.RebindScenery(a.splitRoom.Scene.BackgroundBase, a.splitRoom.Scene.DeskBase)
	if t.state.link.sess.MusicTrack != "" {
		a.splitRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventMusic, Text: t.state.link.sess.MusicTrack, Loop: true}) // NopAudio: silent (#15 loop default)
	}
	// Last message too, settled — same blank-until-someone-talks fix as the
	// bg+song seeds. Deliberately UNfiltered: the live background pump routes
	// this tab's events to splitRoom without the ignore gate (which keys on the
	// ACTIVE tab's server anyway), so the pin seed matches what the pane shows.
	a.splitRoom.RestoreMessage(t.state.link.sess.LastIC)
}

// placeClientAt positions the floating client window centred under (mx,my) — used
// by the drag-tab tear-off so the window lands where you drop it (Chrome-style).
// Uses the window's current width if it was resized, else the default.
func (a *App) placeClientAt(mx, my int32) {
	w := a.wins.clientWin.w
	if w <= 0 {
		w = clientWinDefW
	}
	a.wins.clientWin.x = mx - w/2
	a.wins.clientWin.y = my - floatTitleH/2
	a.wins.clientWin.placed = true
}

// clientWinRect is the floating client window's clamped screen rect.
func (a *App) clientWinRect(w, h int32) sdl.Rect {
	if r, ok := a.classic.seedPanelFromSlot(&a.wins.clientWin, slotPanelClient, clientWinDefW, clientWinDefH, clientWinMinW, clientWinMinH, w, h); ok {
		return r
	}
	return a.wins.clientWin.rect(clientWinDefW, clientWinDefH, clientWinMinW, clientWinMinH, w, h)
}

// drawFloatClient draws the pinned server as a movable + resizable floating
// "second client" window over the primary courtroom: a title bar (drag handle +
// Full/Compact toggle + ✕ close), then either the COMPACT body (stage + log +
// input) or the FULL-THEME view (the whole pinned UI, view-only, + the input
// strip). Shares the per-frame press edge from drawFloatingPanels (so one press
// moves/grabs one window).
func (a *App) drawFloatClient(w, h int32, pressed *bool) {
	if !a.splitActive() {
		return
	}
	c := a.ctx
	wasActive := a.wins.clientWin.dragging || a.wins.clientWin.resizing // detect the drag/resize-end frame for slot persistence
	r := a.clientWinRect(w, h)
	c.Fill(r, ColBackground)
	c.Border(r, ColAccent)
	// Title bar = drag handle + Full/Compact toggle + close.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	if a.client.clientHdrName != a.splitTab.state.srvMeta.serverName { // memoized: no per-frame concat
		a.client.clientHdrName = a.splitTab.state.srvMeta.serverName
		a.client.clientHdr = "▣ " + a.client.clientHdrName
	}
	c.LabelClipped(r.X+10, r.Y+8, r.W-102, a.client.clientHdr, ColAccent)
	// Full ⇄ Compact: show the whole theme (view-only) or just stage+log+input.
	tgl := sdl.Rect{X: r.X + r.W - 92, Y: r.Y + 5, W: 60, H: floatTitleH - 10}
	tglLabel := "Full view"
	if a.client.clientFull {
		tglLabel = "Compact"
	}
	if c.Button(tgl, tglLabel) {
		a.client.clientFull = !a.client.clientFull
	}
	c.TooltipAfter("cli-full", tgl, "Show the WHOLE theme (emote grid, buttons…) — view-only; chat in the strip below. Roughly doubles render cost while open.")
	if c.Button(sdl.Rect{X: r.X + r.W - 26, Y: r.Y + 5, W: 20, H: floatTitleH - 10}, "x") {
		a.clearSplit()
		return
	}
	a.floatWinDrag(&a.wins.clientWin, sdl.Rect{X: r.X, Y: r.Y, W: r.W - 98, H: floatTitleH}, pressed)
	// Bottom-right resize grip — handled before the body so a corner press resizes
	// rather than landing in the log / input beneath it.
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.wins.clientWin, grip, r, clientWinMinW, clientWinMinH, pressed)
	if wasActive && !a.wins.clientWin.dragging && !a.wins.clientWin.resizing { // drag/resize just ended → remember where
		a.classic.persistPanelSlot(&a.env, slotPanelClient, r, w, h)
	}
	body := sdl.Rect{X: r.X, Y: r.Y + floatTitleH, W: r.W, H: r.H - floatTitleH}
	if a.client.clientFull && a.canRenderFullClient() {
		a.drawClientFull(w, h, body, pressed)
	} else {
		a.drawClientBody(body)
	}
	a.drawResizeGrip(grip)
}

// canRenderFullClient gates the full-theme render off the primary's exclusive
// modes (theater / either layout editor): those reskin drawCourtroom globally, so
// the pinned pass would fight them. Compact view is shown instead while they're on.
func (a *App) canRenderFullClient() bool {
	return !a.theaterOn && !a.classic.classicEdit && !a.layoutEdit
}

// drawClientBody lays the pinned server's live stage (4:3, capped), chat log, and IC
// input into the client window's body rect. Clicking the stage takes control of that
// server (the full, real courtroom); the strip lets you fire a quick line without
// swapping.
func (a *App) drawClientBody(r sdl.Rect) {
	c := a.ctx
	inputRow := sdl.Rect{X: r.X, Y: r.Y + r.H - splitInputH, W: r.W, H: splitInputH}
	sceneH := r.W * 3 / 4
	if maxH := (r.H - splitInputH) * 55 / 100; sceneH > maxH {
		sceneH = maxH
	}
	stage := sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: sceneH}
	a.splitVP.Render(c.Ren, &a.splitRoom.Scene, stage)
	a.client.clientControlClick(&a.env, stage) // click the stage → take control of this server
	logTop := stage.Y + stage.H + 3
	logRect := sdl.Rect{X: r.X, Y: logTop, W: r.W, H: inputRow.Y - logTop}
	c.Fill(sdl.Rect{X: logRect.X, Y: logRect.Y - 2, W: logRect.W, H: 1}, ColPanelHi) // rule above the log
	a.drawSplitLog(logRect, &a.splitTab.state)
	a.drawSplitInput(inputRow)
}

// drawClientFull renders the WHOLE pinned client (the full theme — emote grid,
// buttons, themed chatbox, everything) into the window: the pinned drawCourtroom is
// drawn to clientTex (view-only) and blitted into the view area, with a live IC input
// strip below it so you can still chat. Scroll to ZOOM (around the cursor) and drag to
// PAN for a closer look; double-click resets to fit.
func (a *App) drawClientFull(w, h int32, body sdl.Rect, pressed *bool) {
	c := a.ctx
	inputRow := sdl.Rect{X: body.X, Y: body.Y + body.H - splitInputH, W: body.W, H: splitInputH}
	view := sdl.Rect{X: body.X, Y: body.Y, W: body.W, H: body.H - splitInputH}
	a.renderFullClientTexture(w, h)
	if a.client.clientTex == nil {
		c.LabelClipped(view.X+10, view.Y+10, view.W-20, "Full-theme view unavailable on this renderer.", ColTextDim)
		a.drawSplitInput(inputRow)
		return
	}
	a.handleClientZoomPan(view, pressed)      // scroll = zoom (cursor-anchored), drag = pan
	a.client.clientControlClick(&a.env, view) // a plain click on the client = take control of that server
	src := a.clientViewSrc()                  // sub-rect of the texture from zoom + pan
	_ = c.Ren.Copy(a.client.clientTex, &src, &view)
	// "Click to control" hint (top-left pill) — the rendered theme is a live view; a
	// click makes this server the real, fully-interactive courtroom (swaps with primary).
	const ctlHint = "▶ Click to control this server (buttons + chat)"
	hintPill := sdl.Rect{X: view.X, Y: view.Y, W: c.TextWidth(ctlHint) + 12, H: 18}
	c.Fill(hintPill, sdl.Color{R: 0, G: 0, B: 0, A: 150})
	c.LabelClipped(hintPill.X+6, hintPill.Y+3, view.W-12, ctlHint, ColAccent)
	// Zoom readout / hint (memoized so the per-frame draw never Sprintfs), on a small
	// dark pill for legibility over the rendered client.
	pct := int(a.client.clientZoom*100 + 0.5)
	if pct != a.client.clientZoomLblPct {
		a.client.clientZoomLblPct = pct
		a.client.clientZoomLbl = fmt.Sprintf("Zoom %d%%  ·  scroll to zoom · drag to pan", pct)
	}
	pill := sdl.Rect{X: view.X, Y: view.Y + view.H - 18, W: c.TextWidth(a.client.clientZoomLbl) + 12, H: 18}
	c.Fill(pill, sdl.Color{R: 0, G: 0, B: 0, A: 150})
	c.LabelClipped(pill.X+6, pill.Y+3, view.W-12, a.client.clientZoomLbl, ColAccent)
	a.drawSplitInput(inputRow) // quick-chat strip (sendICSplit) — fire a line without taking control
}

// clientViewSrc is the sub-rectangle of clientTex shown in the window, derived from
// the zoom factor and pan centre. At zoom 1 it's the whole texture (shrunk to fit);
// zoomed in it's a centred-on-pan window that magnifies.
func (a *App) clientViewSrc() sdl.Rect {
	z := a.client.clientZoom
	if z < clientZoomMin {
		z = clientZoomMin
	}
	tw, th := float64(a.client.clientTexW), float64(a.client.clientTexH)
	sw, sh := tw/z, th/z
	sx := a.clientPanX*tw - sw/2
	sy := a.clientPanY*th - sh/2
	sx = clampF(sx, 0, tw-sw)
	sy = clampF(sy, 0, th-sh)
	return sdl.Rect{X: int32(sx), Y: int32(sy), W: int32(sw), H: int32(sh)}
}

// handleClientZoomPan applies wheel-zoom (anchored at the cursor) and drag-pan over
// the full-view area, and double-click-to-reset. Pan/zoom are clamped so the source
// window never leaves the texture.
func (a *App) handleClientZoomPan(view sdl.Rect, pressed *bool) {
	c := a.ctx
	if a.client.clientZoom < clientZoomMin {
		a.client.clientZoom = clientZoomMin
	}
	hover := pointIn(c.mouseX, c.mouseY, view)
	if hover && c.wheelY != 0 { // zoom around the cursor
		old := a.client.clientZoom
		nz := old * clientZoomStep
		if c.wheelY < 0 {
			nz = old / clientZoomStep
		}
		nz = clampF(nz, clientZoomMin, clientZoomMax)
		if nz != old {
			src := a.clientViewSrc()
			fx := float64(c.mouseX-view.X) / float64(view.W)
			fy := float64(c.mouseY-view.Y) / float64(view.H)
			texX := float64(src.X) + fx*float64(src.W) // texture point under the cursor
			texY := float64(src.Y) + fy*float64(src.H)
			tw, th := float64(a.client.clientTexW), float64(a.client.clientTexH)
			sw, sh := tw/nz, th/nz
			a.client.clientZoom = nz
			a.clientPanX = (texX - fx*sw + sw/2) / tw // keep that point under the cursor
			a.clientPanY = (texY - fy*sh + sh/2) / th
			a.clampClientPan()
		}
		c.wheelY = 0 // consumed
	}
	if a.client.clientZoom > clientZoomMin { // drag to pan (only meaningful when zoomed)
		if *pressed && hover {
			*pressed = false
			a.client.clientPanning = true
			a.clientPanGrabX, a.clientPanGrabY = c.mouseX, c.mouseY
			a.clientPanBaseX, a.clientPanBaseY = a.clientPanX, a.clientPanY
		}
		if !c.mouseDown {
			a.client.clientPanning = false
		}
		if a.client.clientPanning {
			tw, th := float64(a.client.clientTexW), float64(a.client.clientTexH)
			sw, sh := tw/a.client.clientZoom, th/a.client.clientZoom
			a.clientPanX = a.clientPanBaseX - float64(c.mouseX-a.clientPanGrabX)/float64(view.W)*sw/tw
			a.clientPanY = a.clientPanBaseY - float64(c.mouseY-a.clientPanGrabY)/float64(view.H)*sh/th
			a.clampClientPan()
		}
	} else {
		a.client.clientPanning = false
		a.clientPanX, a.clientPanY = 0.5, 0.5
	}
}

// clampClientPan keeps the pan centre so the zoomed source window stays inside the
// texture (its half-extent is 0.5/zoom in normalized space).
func (a *App) clampClientPan() {
	half := 0.5 / a.client.clientZoom
	a.clientPanX = clampF(a.clientPanX, half, 1-half)
	a.clientPanY = clampF(a.clientPanY, half, 1-half)
}

// clampF clamps a float64 to [lo, hi] (lo wins if the range is inverted).
func clampF(v, lo, hi float64) float64 {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// renderFullClientTexture renders the pinned client's full drawCourtroom into
// clientTex at the logical screen size (w,h) — so the theme lays out exactly as the
// primary — for the shrink-blit. The pinned session (incl. its own chat-raster cache,
// which lives in sessionState) + live room + viewport are temp-swapped into the
// embedded slots, input is neutralized, and a.screen / ctx.fieldSeq / ctx.tipText are
// snapshotted+restored so the VIEW-ONLY pass can't leak navigation, Tab-focus order,
// or tooltips into the primary. View-only is deliberate: the kit is single-instance,
// so a fully interactive second courtroom would collide widget ids AND leak App-global
// writes (a.screen, the zoom-% knobs, warnings) — interaction is via the control bar
// (drawClientControls), which drives only the isolated pinned sessionState.
func (a *App) renderFullClientTexture(w, h int32) {
	c := a.ctx
	if w <= 0 || h <= 0 {
		return
	}
	if a.client.clientTex == nil || a.client.clientTexW != w || a.client.clientTexH != h {
		if a.client.clientTex != nil {
			_ = a.client.clientTex.Destroy()
			a.client.clientTex = nil
		}
		tex, err := c.Ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
		if err != nil {
			return
		}
		a.client.clientTex, a.client.clientTexW, a.client.clientTexH = tex, w, h
	}

	// Bind the target and draw at 1:1 logical scale. The render-target + scale restore
	// is DEFERRED so even a panic inside drawCourtroom can't leave the screen rendering
	// into the texture (a black primary) — the worst silent failure of this path.
	prevTarget := c.Ren.GetRenderTarget()
	if c.Ren.SetRenderTarget(a.client.clientTex) != nil {
		return
	}
	// #77: the pinned pass INHERITS the ambient text device scale (it does NOT
	// bracket it — toggling textDevPct here would purge/rebuild the font caches
	// every frame a tab is pinned, a real perf regression). It composes correctly:
	// clientTex is LOGICAL-sized and SetScale(1,1) draws 1:1, while blitLabel/Draw
	// divide the device glyphs back to logical — so text lands at its logical size
	// (filling the logical clientTex), just rasterized at the device size (crisper,
	// never larger). The SetScale save/restore below is unchanged.
	defer func() {
		_ = c.Ren.SetRenderTarget(prevTarget)
		s := float32(a.UIScale()) / 100 // restore main's per-frame render scale
		if s <= 0 {
			s = 1 // never strand the renderer at scale 0 (UIScale unset)
		}
		_ = c.Ren.SetScale(s, s)
		c.endRenderScaleOverride()
	}()
	// Tell the kit the renderer is at 1:1 for the duration. The chatbox's device-exact
	// message blit keys off RenderScalePct; without this it would still read the live
	// UI scale, match the raster's device scale, and blit the message at device size
	// into this LOGICAL-sized target — i.e. draw a pinned tab's chat text scale× too big.
	c.beginRenderScaleOverride(DefaultScalePct)
	_ = c.Ren.SetScale(1, 1)
	_ = c.Ren.SetDrawColor(0, 0, 0, 255)
	_ = c.Ren.Clear()

	// --- swap the pinned client into the embedded slots ---
	// The chat-raster cache (msRaster + its keys) lives in sessionState, so the swap
	// below already gives the pinned pass its OWN raster — no thrash with the primary's
	// and no separate per-instance copy (a separate copy would alias splitTab.state's
	// pointer and double-free on clearSplit).
	savedSess := a.sessionState
	savedVP := a.d.Viewport
	savedScreen := a.screen // view-only pass must not change the primary's screen…
	// …tooltip… The companion c.tipAnchor is deliberately NOT saved, and that is
	// inert rather than a hole: snapshotInput parks the mouse at -30000 for the
	// whole pass, and tipAnchor is only ever written beside tipText inside
	// Tooltip/TooltipAfter's `c.hovering(r)` gate — so the pinned pass cannot arm
	// either one. This save is belt-and-braces; if a future surface ever writes
	// tipText off the hover gate, tipAnchor has to be saved with it.
	savedTip := c.tipText
	savedSeqLen := len(c.fieldSeq)
	in := a.snapshotInput() // …focus order, or handle any input

	a.sessionState = a.splitTab.state // promotes a.sess / a.icInput / a.emote* / msRaster … to the pinned tab
	a.link.room = a.splitRoom         // drive the pinned LIVE room (a parked tab's sessionState.room is nil)
	a.d.Viewport = a.splitVP
	a.client.pinnedPass = true

	a.drawCourtroom(w, h)

	// --- capture the pinned pass's side effects, then restore the primary ---
	a.client.pinnedPass = false
	a.link.room = nil                 // a parked tab keeps no room — don't persist splitRoom into its slot
	a.splitTab.state = a.sessionState // capture emote/scroll/raster/etc. side effects of the view-only pass
	a.sessionState = savedSess        // restores a.room + a.sess + msRaster + everything promoted
	a.d.Viewport = savedVP
	a.screen = savedScreen
	c.tipText = savedTip
	if len(c.fieldSeq) > savedSeqLen {
		c.fieldSeq = c.fieldSeq[:savedSeqLen] // drop the pinned client's field ids from Tab-cycle
	}
	a.restoreInput(in)
	// (render target + scale restored by the deferred func above)
}

// ctxInput snapshots the per-frame Ctx input fields so the view-only full-client
// pass can be run with every click/key/wheel signal cleared (mouse parked far
// off-screen → hovering() is false everywhere), then restored. Opt-in path, so the
// small value copy is fine.
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

// drawSplitInput draws the pinned pane's IC field. It edits the PINNED tab's own
// icInput (its sessionState, not the embedded primary's), and on Enter routes to
// sendICSplit, which momentarily swaps that state into the embedded slot so the
// untouched sendIC sends on the pinned conn. The field id "ic-split" is distinct
// from the primary "ic", so the kit's ID-keyed focus makes it click-to-focus for
// free — no side-swap. 0-alloc: icFieldFonts is nil for ASCII and the placeholder
// is a constant.
func (a *App) drawSplitInput(r sdl.Rect) {
	c := a.ctx
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, ColPanelHi) // rule above the input
	box := sdl.Rect{X: r.X + 6, Y: r.Y + 5, W: r.W - 12, H: r.H - 10}
	s := &a.splitTab.state
	primary, emoji := a.icFieldFonts(s.compose.icInput)
	var send bool
	s.compose.icInput, send = c.TextFieldEmoji("ic-split", box, s.compose.icInput, "Chat in the pinned server — click to focus", primary, emoji)
	// Fenced under the notice box like both single-pane send sites. The pinned pane is a
	// DIFFERENT socket, so this one is not about writing to a dead connection: the box is
	// App-level and pointer-blinds the whole frame, which leaves a still-focused field's
	// Enter as the one action that can fire while the user is reading a ban. A line the
	// user did not see themselves send is the same defect whichever server receives it.
	if send && !a.font.serverNoticeDlg.open {
		a.sendICSplit()
	}
}

// sendICSplit sends an IC message on the PINNED (right-pane) server. Every field
// sendIC reads lives in sessionState (verified: emotes/emoteIdx, pair*, icColor,
// charBlips, evid*, lastSent*, shownameOverride, sidePref…), and sendIC + all its
// helpers + handleChatCommand never touch a.room — so we momentarily swap the
// pinned tab's state into the embedded slot, reuse the untouched sendIC (its
// SendChat then targets the pinned conn), capture the side effects back into the
// tab, and restore the primary. Synchronous + single-threaded: no frame renders
// and pumpBackgroundTabs cannot interleave between the two swaps.
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
	// sendIC's random-emote path (off by default) refocuses "ic"; keep the caret
	// in the right pane so the user can keep typing where they were.
	if savedFocus == "ic-split" {
		a.ctx.focusNext = "ic-split"
	}
}

// splitLogWrapMaxLines bounds ONE pinned-pane entry's wrapped rows. wrapToWidth
// needs a finite cap (maxLines ≤ 0 cuts to nothing, unlike the kit's WrapText),
// and this is DERIVED from the entry cap so a legitimate message can never be
// clipped: an IC entry is at most icLineCap runes and, at the conservative floor
// of oocWrapMinCharsPerRow runes per row, needs at most this many rows.
const splitLogWrapMaxLines = icLineCap / oocWrapMinCharsPerRow

// splitLogRow is one wrapped, coloured line of the pinned pane's chat log.
type splitLogRow struct {
	text string
	col  sdl.Color
}

// drawSplitLog renders the pinned session's IC log (read-only, newest at the
// bottom) into r. Wrapping is cached by (width, log seq), so a steady frame is
// 0-alloc; it rebuilds only when a message lands or the pane resizes.
func (a *App) drawSplitLog(r sdl.Rect, s *sessionState) {
	c := a.ctx
	font := c.LogFont(a.logPct)
	lineH := int32(font.Height()) + 2
	wrapW := r.W - 8
	if wrapW != a.splitWrapW || s.chatlog.icLogSeq != a.splitWrapSeq || a.splitPinEpoch != a.splitWrapEpoch ||
		a.logPct != a.splitWrapPct || c.fontChainGen != a.splitWrapGen {
		a.splitWrapW, a.splitWrapSeq, a.splitWrapEpoch = wrapW, s.chatlog.icLogSeq, a.splitPinEpoch
		a.splitWrapPct, a.splitWrapGen = a.logPct, c.fontChainGen
		a.splitWrapRows = a.splitWrapRows[:0]
		for i := range s.chatlog.icLog {
			e := &s.chatlog.icLog[i]
			col := ColText
			if e.color > 0 {
				col = render.TextColor(e.color)
			}
			// Measure in the face this pane DRAWS in (LabelClippedFont(font) below),
			// not the chrome face WrapText measures: with a custom font chain
			// installed — or the log simply zoomed — the two differ and every row
			// overflowed the pane (#42's bug class, same trigger). constFont because
			// this draw uses the log PRIMARY face for every row, with no per-row
			// covering pick and no emoji raster, so one face IS the whole metric.
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

// crossfadeDur is the speaker-swap blend the viewports get: the pref, zeroed
// under Reduce motion (a fade is motion — the accessibility floor wins).
func (a *App) crossfadeDur() time.Duration {
	if a.d.Prefs.Load().ReduceMotionOn {
		return 0
	}
	return time.Duration(a.d.Prefs.Load().CrossfadeMsVal) * time.Millisecond
}

// --- frame pacing (the GPU-burn fix) -------------------------------------------
// The loop used to re-render + present the whole UI every pass: vsync tied it to
// the monitor (144/165 Hz laptop panels = a full-screen composite 165×/sec while
// IDLE), and where vsync doesn't block (small windowed presents on some drivers)

// inputGraceFrameDuration is one "frame" of the post-input full-rate hold — the
// InputGraceFrames pref counts these. A fixed 60 fps reference, so the frame
// count means the same thing regardless of the active cap. After a click/key the
// rate holds full for InputGraceFrames of them, then drops straight back to idle
// (the playtest ask was one frame of full rate, not a whole second). Mouse MOTION is separate
// — it keeps its own short motionInputGrace.
const inputGraceFrameDuration = time.Second / 60

// NoteInput marks "the user just interacted" — the main loop calls it whenever
// an SDL event arrives, and wantsFullRate holds full rate through the grace.
func (a *App) NoteInput() { a.frame.lastInputAt = time.Now() }

// NoteAnimating marks that THIS draw pass rendered a self-driven, time-stepped
// UI animation — theme chrome, a splash, a looping badge, the layout editor's
// ghost, or any FUTURE widget that tweens on its own clock. It is the general
// hook for "uncap for the duration of any self-driven UI animation": while a
// draw keeps calling it, SkipFrame won't skip AND FramePace paces at the ACTIVE
// cap (both loop modes), so the motion stays smooth; the frame the draw stops
// calling it, the screen falls back to idle. Retrospective and self-sustaining
// (each animating frame re-arms the next). For an INSTANT off-frame change (a
// packet updated a list, a screen switched) set a.uiDirty instead — that earns
// exactly one follow-up frame. Alloc-free; call it from the draw pass.
func (a *App) NoteAnimating() { a.frame.frameAnimChrome = true }

// audioFineTick is the cadence the courtroom is advanced — and its blips played —
// at while a message types, independent of the (possibly much slower) PRESENT rate.
// ~60 Hz: fine enough that a fast blip stream never audibly quantizes, cheap because
// it's logic + audio only (no GPU). The playtest ask: blips should sound as if the
// UI were at 60 fps even with the frame rate capped low (down to 1 fps).
const audioFineTick = time.Second / 60

// AudioActive reports whether the LIVE courtroom is streaming audio-timed events
// (a message typing → blips firing). Replay, the scene maker and GIF export drive
// their own rooms at their own pacing, so they're excluded — only the live blip
// stream needs the fine audio cadence. A static-but-open courtroom is NOT active,
// so it still parks at true idle.
func (a *App) AudioActive() bool {
	if a.sel.replaying || a.sel.makerOpen || a.sel.gifExporting || a.link.room == nil {
		return false
	}
	return a.link.room.AudioActive()
}

// AudioPaceActive reports whether this pass's pacing sleep should be spent advancing
// the courtroom at the fine audio cadence (Background sub-steps) instead of one long
// sleep: a message is streaming blips AND the present budget is slower than the fine
// tick, so a single sleep would batch a whole present-period of blips into one
// instant (the "blips only on every screen refresh at a 1 fps cap" report). budget
// is the pass's total intended sleep. Render thread; alloc-free.
func (a *App) AudioPaceActive(budget time.Duration) bool {
	return budget > audioFineTick && a.AudioActive()
}

// AudioFineTick is the fine audio-advance cadence, exported for the main loop's
// sub-step sleep.
func (a *App) AudioFineTick() time.Duration { return audioFineTick }

// MarkRoomPreAdvanced records that the loop already advanced the courtroom this
// present cycle (audio-paced sub-stepping), so the next Frame draws without
// re-advancing the room (it's current) while still advancing the UI. Render thread.
func (a *App) MarkRoomPreAdvanced() { a.frame.roomPreAdvanced = true }

// motionInputGrace holds full rate briefly after BARE pointer motion under the
// experimental loop: long enough that continuous movement (hover sweeps, drags
// — their motion stream keeps re-arming it) renders at full rate throughout,
// short enough that waving the mouse over dead space stops costing frames
// almost as soon as it stops. Clicks/keys/wheel keep the InputGraceFrames hold.
const motionInputGrace = 200 * time.Millisecond

// NoteMotion marks a pointer-motion-only pass. Under the experimental loop it
// arms the short motion grace; under the classic loop motion is plain input
// (byte-identical pacing to before).
func (a *App) NoteMotion() {
	if a.d.Prefs != nil && a.d.Prefs.Load().EventDrivenLoop {
		// Per-event motion redraw (default ON): don't arm the full-rate
		// grace. The motion event already earns its single wake-frame (SkipFrame refuses on
		// sawEvent), then the loop re-parks — so a moving cursor redraws once per
		// motion event instead of holding full rate through a 200 ms tail. Saves
		// power on hover sweeps that would otherwise pin the rate to the cap.
		if a.d.Prefs.Load().MotionRedrawPerEvent {
			return
		}
		a.frame.lastMotionAt = time.Now()
		return
	}
	a.frame.lastInputAt = time.Now()
}

// staticTalkFPS paces a message whose stage is ALL STATIC art (single-frame
// sprites, no effects): the only motion is the typewriter's text crawl, which
// reveals runes at ≥ ~40 ms — 30 fps samples it cleanly, so full rate is pure
// waste there (the playtest ask: a still image should not be redrawn at high
// fps). The effective rate never drops below the user's idle rate and
// never exceeds their cap.
const staticTalkFPS = 30

// maxHousekeepingGap bounds how long the event-driven loop may park doing
// NOTHING: even with the idle redraw rate off (config.FPSOff), Background must
// keep pumping this often (keepalive pacing at 45 s, queue drains), but a wait
// that times out at this floor pumps Background ONLY — it does not render a
// frame. This replaces the old fixed 2 fps "heartbeat" that forced a real frame
// every 500 ms regardless of the idle-rate setting (the two "fought", per the
// report): the idle rate is the single idle cadence now, so 0/off means a
// genuinely static screen redraws zero times until real damage or a deadline.
const maxHousekeepingGap = 500 * time.Millisecond

// paceBudget converts an fps knob to a per-frame budget (0 = uncapped). Used for
// the ACTIVE cap, which is never "off" (0 fps while interacting is meaningless).
func paceBudget(fps int) time.Duration {
	if fps <= 0 {
		return 0
	}
	return time.Second / time.Duration(fps)
}

// rateBudget converts a rate knob to a per-frame budget WITH the widened
// domain's trichotomy — paceBudget alone can't express it. A 0 budget means
// "uncapped", but a knob set to config.FPSOff means "off / never redraw in this
// state", the exact opposite — so every idle/unfocused consumer asks here and
// branches on off BEFORE treating a 0 as uncapped:
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

// HardCapBudget is the INVIOLABLE minimum time between two rendered frames for
// the current focus state: the active FPS cap when focused, the unfocused cap
// when not. Unlike FramePace's adaptive tiers (idle/talk/anim, which only ever
// SLOW rendering down toward a floor), this is the hard ceiling the user set —
// the main loop sleeps it UNINTERRUPTIBLY, so an input flood (above all mouse
// motion, which streams an event every few ms) can never interrupt the pace and
// drive the loop past the cap. The "caps are ALWAYS obeyed" contract lives here;
// it also enforces the unfocused cap even when FramePace lifts an unfocused
// animation toward the active rate. 0 = uncapped (the ∞ sentinel — no floor).
func (a *App) HardCapBudget(focused bool) time.Duration {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	if focused {
		// The active cap has no "off" (0 fps while interacting is meaningless);
		// ∞ → 0 (uncapped), N → 1s/N.
		return paceBudget(a.d.Prefs.Load().FPSCap())
	}
	unf, off := rateBudget(a.d.Prefs.Load().UnfocusedFPS())
	if off {
		// Unfocused "off" means "don't redraw while tabbed out" — the static skip
		// handles the idle case. A frame that renders anyway (an audible ceremony)
		// still obeys SOMETHING: fall back to the active cap as the backstop
		// ceiling, mirroring FramePace's unfocused-off branch (unf = full).
		return paceBudget(a.d.Prefs.Load().FPSCap())
	}
	return unf // ∞ → 0 (uncapped); N → 1s/N
}

// clampDur bounds d to [lo, hi] (lo = the fastest allowed budget).
func clampDur(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

// roomBusy reports a message mid-ceremony on the ACTIVE room — typewriter,
// preanim, shout, linger, or anything still queued.
func (a *App) roomBusy() bool {
	return a.link.room != nil && (a.link.room.Phase() != courtroom.PhaseIdle || a.link.room.QueueLen() > 0)
}

// serverTimersLive reports any visible courtroom timer chip (TI packets) — its
// clock readout changes every second, so a static skip would freeze it.
func (a *App) serverTimersLive() bool {
	if a.link.sess == nil {
		return false
	}
	for i := range a.link.sess.Timers {
		if a.link.sess.Timers[i].Visible {
			return true
		}
	}
	return false
}

// talkBudget is the frame budget while a message plays over all-static art:
// fast enough that AT MOST ONE typewriter rune reveals per frame, so every blip
// boundary lands its own frame — blips fire from the per-frame room Update, so
// a frame slower than the rune interval audibly coalesces them (playtest: the
// blip rate fell with the frame rate). Base cadence is
// staticTalkFPS; faster text (the speed slider / {} spans) tightens it; full
// (the cap's budget) still bounds it.
func (a *App) talkBudget(full time.Duration) time.Duration {
	talk := paceBudget(staticTalkFPS)
	if a.link.room != nil {
		if iv := a.link.room.Typewriter.Interval; iv > 0 && iv < talk {
			talk = iv
		}
	}
	if talk < full {
		talk = full // never above the cap
	}
	return talk
}

// pacerTier is a DIAGNOSTIC label of which FramePace branch decided the current
// budget — recorded at each return in FramePace, read only by the F8 panel (#50).
// It is write-only from FramePace and never feeds wantsFullRate / SkipFrame /
// NextWakeDelay: the pacer's census has regressed repeatedly and had no readout
// of WHY it sat at a tier, so this names the branch WITHOUT becoming a new latch
// (a re-derivation in the panel would drift with the very logic it's meant to
// diagnose, so the actual return sites stamp it instead). Bounded by design —
// one value per branch, no unbounded set.
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

// pacerTierLabel renders a pacerTier for the F8 panel. Panel-only formatting
// (never on the hot path), so it can't add a per-frame allocation.
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

// FramePace returns the frame budget the main loop should pace to (0 = no cap):
// the sleep between CONSECUTIVE rendered frames. focused is the window's input-
// focus state. Tiers, fastest first:
// Rate knobs carry the widened trichotomy (rateBudget): ∞ → 0 (uncapped), off
// (FPSOff) → the active cap in THIS render path (the "no idle render" behaviour
// lives in the skip/park path, not here), N → 1s/N.
func (a *App) FramePace(focused bool) time.Duration {
	focused = a.pacingFocused(focused)            // the "full speed while unfocused" option (pacingfocus.go)
	full := paceBudget(a.d.Prefs.Load().FPSCap()) // active cap: ∞/off → 0 (uncapped); N → 1s/N
	// nextAnimDue is the stage's earliest scheduled frame flip (ok=false on a
	// static stage / no room) — consulted by both focus branches.
	nextAnimDue := func() (time.Duration, bool) {
		if a.link.room == nil || a.d.Viewport == nil {
			return 0, false
		}
		return a.d.Viewport.NextAnimDue(a.renderScene())
	}
	if !focused {
		a.frame.lastPacerTier = pacerUnfocused
		unf, unfOff := rateBudget(a.d.Prefs.Load().UnfocusedFPS())
		if unf == 0 && !unfOff {
			return 0 // ∞ unfocused: never throttle
		}
		if unfOff {
			// off: an idle unfocused window SKIPS + parks, so a rendered frame
			// here is content (an audible ceremony / a visible stage anim) — pace
			// it at the cap and let talk/anim tighten below.
			unf = full
		}
		// A message playing while tabbed out is still AUDIBLE: hold the blip cadence.
		// Stamp the tightening tier as each deadline actually wins (the focus state
		// shows separately via the F8 focusSuffix), so the readout names WHY the
		// budget moved off trickle — a bare pacerUnfocused hid an audible ceremony
		// / live stage anim while claiming "trickle" (#50 diagnostic precision).
		if a.roomBusy() {
			if tb := a.talkBudget(full); tb < unf {
				unf = tb
				a.frame.lastPacerTier = pacerTalk
			}
		}
		// An unfocused window is still VISIBLE (second monitor): a live stage
		// animation keeps its OWN schedule — the flat trickle rate here was the
		// reported cause of choppy idle animations on an unfocused window.
		if due, ok := nextAnimDue(); ok && due < unf {
			unf = clampDur(due, full, unf)
			a.frame.lastPacerTier = pacerContentAnim // the stage-anim deadline set the budget
		}
		return unf
	}
	if a.wantsFullRate() {
		a.frame.lastPacerTier = pacerFullInput
		return full // recent input / effects genuinely in flight
	}
	if a.frame.drawnAnimChrome {
		// A self-driven UI animation (NoteAnimating census) re-arms every frame
		// it draws — some indefinitely (FX chat text on a settled message, an
		// animated theme page, the viewport's ambient-FX census). Smooth frames,
		// but never a ZERO budget: with the ∞ default cap, pace=hardCap=0 skips
		// every pacing sleep and vsync is the only brake — which main.go's own
		// GPU-burn note records as non-blocking on some windowed present paths.
		a.frame.lastPacerTier = pacerAnimChrome
		return backstopBudget(full)
	}
	idle, idleOff := rateBudget(a.d.Prefs.Load().IdleFPS())
	idleUnlimited := !idleOff && idle == 0 // ∞ idle
	if a.roomBusy() {
		// The talk tier paces the ceremony; the earliest OTHER deadline may only
		// tighten it. Order matters: consulting the stage anim FIRST returned a
		// slow lipflap's flip (clamped to the idle rate) as the whole frame
		// budget, crawling the text and coalescing the blips.
		talk := a.talkBudget(full)
		switch {
		case idleOff || idleUnlimited:
			talk = full // off/∞ idle → ceremonies pace at the active cap
		case idle < talk:
			talk = idle // a high idle rate lifts the ceremony to match its smoothness
		}
		if due, ok := nextAnimDue(); ok && due < talk {
			talk = clampDur(due, full, talk) // a fast speaker anim gets its frames
		}
		a.frame.lastPacerTier = pacerTalk
		return talk
	}
	if due, ok := nextAnimDue(); ok {
		a.frame.lastPacerTier = pacerContentAnim
		if idleOff || idleUnlimited {
			return clampDur(due, full, due) // the content's own cadence (never above the cap)
		}
		return clampDur(due, full, idle) // content cadence, never slower than the idle rate
	}
	// Rendered but nothing is animating — see the "active cap" tier note above.
	// Backstopped: a frame NOTHING demanded (no input, no transition, no anim)
	// must never leave the pacer with a zero budget — under the ∞ default cap
	// the classic loop's lobby/caret renders became a zero-sleep spin here, and
	// ∞ idle ("as fast as the active cap", the Settings tooltip's own words)
	if a.d.Prefs.Load().EventDrivenLoop || idleOff || idleUnlimited {
		a.frame.lastPacerTier = pacerIdleActiveCap
		return backstopBudget(full)
	}
	a.frame.lastPacerTier = pacerIdleNap
	return idle // classic idle nap (N → 1s/N)
}

// backstopBudget resolves a rendered-but-undemanded frame's budget: the given
// cap when finite, else a concrete ~60 fps floor (the Settings ∞-toggle's own
// FPSCapUnlimitedOff). Frames that reach it were NOT demanded by input or a
// running transition — wantsFullRate returns the true uncapped `full` before
// any caller — so flooring here costs active use nothing, while a perpetual
// census or an idle re-render can no longer disable pacing entirely (with
// pace=hardCap=0 the main loop sleeps zero and vsync is the only brake, which
// its own GPU-burn comment records as unreliable for windowed presents).
func backstopBudget(b time.Duration) time.Duration {
	if b > 0 {
		return b
	}
	return paceBudget(config.FPSCapUnlimitedOff)
}

// SkipFrame reports that the last drawn frame is still exactly right, so the
// main loop may skip render+present entirely this pass (GPU cost → zero) and
// just keep the session pumping via Background. Any SDL event, any ceremony,
// any scheduled animation, a live toast, a blinking caret, or a ticking timer
// forces a real frame. Conservative by construction: a wrongly SKIPPED signal
// heals on the next damage/deadline, never a hang.
func (a *App) SkipFrame(focused, sawEvent bool) bool {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	if sawEvent {
		return false // input this pass (incl. a focus-regain window event)
	}
	// Background cap OFF + window unfocused: render NOTHING while tabbed out. The
	// background cap is a hard ceiling exactly like the active cap, and its "off"
	// position means a 0-fps ceiling — so a stage animation must NOT keep driving
	// frames (the "bg=0 but still 60 fps in another window" report). Highest
	// priority after a live event: this overrides animation, damage, and the
	// full-rate input grace. Voice / mic capture is the sole exception — its audio
	// engine is frame-driven — and a focus-regain event (sawEvent above) resumes
	// normal rendering; the park keeps the session pumping meanwhile.
	if !focused && !a.link.voiceJoined && a.micTest == nil {
		if _, off := rateBudget(a.d.Prefs.Load().UnfocusedFPS()); off {
			return true
		}
	}
	exp := a.d.Prefs != nil && a.d.Prefs.Load().EventDrivenLoop
	if exp {
		if !a.expScreenSkippable() {
			return false
		}
		// No heartbeat here: the event-driven loop parks on NextWakeDelay, whose
		// idle-rate tick IS the periodic idle render (idle=off → genuinely none).
	} else {
		if a.link.room == nil || a.link.sess == nil {
			return false // classic: only the courtroom skips (the lobby keeps its idle render)
		}
		// Classic idle heartbeat: the classic loop naps and re-polls rather than
		// waking on damage, so a static courtroom re-renders at the idle rate to
		// surface async changes it doesn't otherwise track. The idle rate is the
		// single cadence now (no separate fixed 500 ms); idle=off opts out and
		// leans on caret/timer/input, exactly like the event-driven loop's 0.
		if idleB, idleOff := rateBudget(a.d.Prefs.Load().IdleFPS()); !idleOff && idleB > 0 {
			if time.Since(a.frame.lastFrameDrawn) >= idleB {
				return false
			}
		}
	}
	if a.wantsFullRate() || a.roomBusy() || a.warnActive() {
		return false
	}
	// voicePump (mic capture, mixer, the Settings mic test) is driven from
	// Frame: while a voice session or mic test is live, frames must keep
	// coming at the idle rate or the audio engine starves. (Latent in the old
	// courtroom-only skip too — the usually-focused IC field masked it.)
	if a.link.voiceJoined || a.micTest != nil {
		return false
	}
	if a.frame.drawnAnimChrome {
		// The last frame drew time-stepped art outside the viewport scheduler
		// (animated theme chrome, the testimony badge, a WT/CE splash, the
		// editor ghost, an ANIMATED sprite-preview box): freezing it between
		// heartbeats was a latent classic hole (usually masked by the focused
		// IC field). Both modes render on. The sprite preview reports through
		// this census from its DRAW site (drawSpritePreview) — deliberately NOT
		// via a bare previewBase check here: every preview close path is a
		// per-screen draw tail, so a preview orphaned by a screen switch (char
		// pick → PV, a pointer-fenced confirm click, a hotkey jump) had no
		// owner left to clear it, and the old state check held the event-driven
		// loop at the ACTIVE cap until restart (the stuck-at-active-cap
		// report). A static or undrawn box needs no frames: its pop-in rides
		// store-generation damage, zoom/drag ride input events.
		return false
	}
	if exp {
		if a.RenderNeeded() {
			return false // pending damage: packets landed / textures moved / a caret flip is showing
		}
	} else {
		if a.ctx != nil {
			// State-gated on the blink actually being STALE (the comparison
			// RenderNeeded uses), not on "a field is focused" — the IC input is
			// habitually focused in the courtroom, and the blanket refusal made
			// the classic loop render every pass forever (with the ∞ default
			// cap, a zero-sleep spin). The skip nap re-polls well inside a
			// blink period, so the caret still flips on time.
			if on, focused := a.ctx.CaretVisible(); focused && on != a.frame.drawnCaretOn {
				return false // the focused caret's blink phase flipped — draw it
			}
		}
		if a.timerActive() || a.serverTimersLive() {
			return false // countdown readouts change every second
		}
	}
	if a.d.Viewport != nil && a.link.room != nil {
		if _, ok := a.d.Viewport.NextAnimDue(a.renderScene()); ok {
			return false
		}
	}
	_ = focused // both focused and unfocused static screens skip identically
	return true
}

// expScreenSkippable reports whether the CURRENT screen is safe for the
// experimental static skip. Court surfaces skip exactly like the classic
// path; the static menu screens join them; the lobby joins while no async
// sweep is repainting rows. Anything unlisted defaults to classic
// always-render.
func (a *App) expScreenSkippable() bool {
	switch a.screen {
	case ScreenCourtroom, ScreenCharSelect:
		return a.link.sess != nil
	case ScreenLobby:
		return !a.conn.lobby.lobbyFetching && !a.conn.lobby.pinging
	case ScreenSettings, ScreenAbout, ScreenChangelog, ScreenServerHelp, ScreenHelp, ScreenLogs:
		return true
	case ScreenThemeEditor:
		// ITS OWN CASE, not a join onto the courtroom pair above. That pair returns
		// `a.sess != nil`, and the editor's headline entry point is Settings ▸ Theme
		// with no server attached — joining it would make the editor NON-skippable in
		// exactly the case it is most often opened in, burning full CPU on a screen
		// where a still theme is a still picture. It relies on the anim-chrome census
		// (a live element notes it, a still one does not) and on RenderNeeded damage to
		// force the frames it does need, which is the same contract every other static
		// screen here runs on.
		return true
	default:
		return false
	}
}

// RenderNeeded reports redraw-worthy damage accumulated since the last drawn
// frame (experimental loop): a drained packet touched UI state, the texture
// store's generation moved (something streamed in / evicted), or the focused
// caret's blink state no longer matches what's on screen. Render thread only.
func (a *App) RenderNeeded() bool {
	if a.uiDirty {
		return true
	}
	if a.d.Store != nil && a.d.Store.Generation() != a.frame.drawnGen {
		return true
	}
	if a.ctx != nil {
		if on, focused := a.ctx.CaretVisible(); focused && on != a.frame.drawnCaretOn {
			return true
		}
	}
	return false
}

// RenderSuppressed reports that SkipFrame will keep refusing to render
// REGARDLESS of pending damage: the unfocused window with the background cap
// OFF (its 0-fps ceiling deliberately overrides damage; the state re-syncs on
// the focus-regain render). The main loop's "RenderNeeded → loop around and
// draw it" shortcut must park instead in that state — Background's own pumps
// set uiDirty on every packet and only a drawn frame clears it, so the
// shortcut otherwise spins with zero sleep until a real window event. Voice /
// mic capture never suppress, mirroring SkipFrame's own exception.
func (a *App) RenderSuppressed(focused bool) bool {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	if focused || a.link.voiceJoined || a.micTest != nil {
		return false
	}
	_, off := rateBudget(a.d.Prefs.Load().UnfocusedFPS())
	return off
}

// NoteDeadline marks a scheduled wake that just fired (the main loop's event
// wait timed out at a deadline NextWakeDelay chose): the next pass renders one
// frame to show whatever was due — a caret flip, a clock second, a hover
// reveal, or the plain staleness heartbeat.
func (a *App) NoteDeadline() { a.uiDirty = true }

// NoteInteraction forces exactly one follow-up render after a real (non-motion)
// input event. A click or keypress almost always changes UI-visible state DURING
// its own frame's draw — a screen switch, a menu open, a toggle — and that change
// only appears on the NEXT frame. The main loop calls this after a rendered input
// frame so that frame is guaranteed regardless of the input-grace timing, instead
// of leaving the new state stranded until cursor motion or the idle tick reveals
// it (the report of a screen that only repaints on the next mouse movement).
func (a *App) NoteInteraction() { a.uiDirty = true }

const (
	// minWakeDelay floors the event wait so a just-passed deadline can never
	// busy-spin the loop.
	minWakeDelay = 5 * time.Millisecond
	// timerTickSlack lands a clock wake just PAST the second boundary, so the
	// frame it triggers draws the new second (not a re-render of the old one).
	timerTickSlack = 20 * time.Millisecond
	// assetDemandWakeInterval re-renders while a demand-streamed grid still shows
	// blank cells (drawnDemandPending), so demandAsset keeps issuing asks until
	// they fill. ~4 fps — brisk enough that a grid fills smoothly, cheap enough to
	// ignore, and it stops the instant no cell is blank. MUST stay strictly below
	// maxHousekeepingGap: considerRender only marks a render when the due time is
	// nearer than that Background-only floor, so an equal value would never draw.
	assetDemandWakeInterval = 250 * time.Millisecond
)

// NextWakeDelay is how long the experimental loop may park on its event wait:
// the time to the nearest scheduled deadline — caret flip, a RUNNING clock's
// next displayed second, a pending tooltip/preview dwell — capped by the
// staleness heartbeat and floored against busy-spin. Input and wake events
// interrupt the wait regardless; this only bounds how long "nothing happens"
// may last.
func (a *App) NextWakeDelay(focused bool) (wait time.Duration, render bool) {
	focused = a.pacingFocused(focused) // the "full speed while unfocused" option (pacingfocus.go)
	wait = maxHousekeepingGap          // Background-only floor (no frame) — see the const
	// Background cap OFF + unfocused: schedule NO render wakes — the window is
	// asleep while tabbed out (SkipFrame parks it), so even the idle tick and the
	// caret/clock deadlines stay silent until focus returns. This is what makes a
	// 0-fps background cap actually reach 0 fps instead of the idle rate.
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
	// Idle redraw cadence: the missed-damage backstop and the "redraw at the idle
	// fps when nothing's happening" rate. off → no periodic idle render (0 GPU on
	// a static screen); ∞ → due immediately (render as fast as the loop allows).
	switch idleB, idleOff := rateBudget(a.d.Prefs.Load().IdleFPS()); {
	case idleOff:
		// no periodic idle render — only the real deadlines below wake a frame
	case idleB == 0:
		considerRender(0) // ∞ idle
	default:
		considerRender(idleB - time.Since(a.frame.lastFrameDrawn))
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
	// Server clocks (TI): wake right after each RUNNING timer's next whole second
	// so the mm:ss chip ticks on the second. (Paused clocks display a frozen
	// remainder — nothing scheduled.) These MUST mark render: with the old fixed
	// heartbeat gone, a clock tick is the only thing that redraws it, so at
	// idle=off it would otherwise freeze (the timer-freeze trap).
	if a.link.sess != nil {
		for i := range a.link.sess.Timers {
			t := &a.link.sess.Timers[i]
			if t.Visible && t.Running {
				if rem := t.Remaining(now); rem > 0 {
					considerRender(rem%time.Second + timerTickSlack)
				}
			}
		}
	}
	// The local alarm chip ticks the same way — and its due-fire (pollTimer,
	// Frame-driven) rides these wakes, so the alarm lands within a second
	// even while everything else is static.
	if a.timerRunning() {
		if rem := a.timerRemaining(); rem > 0 {
			considerRender(rem%time.Second + timerTickSlack)
		}
	}
	// The STAGE's own next frame flip — a live screen effect, an animated sprite,
	// background or shout. SkipFrame already refuses to skip for these (it consults
	// NextAnimDue and the anim-chrome census), but refusing to skip only matters on a
	// pass the loop actually takes: parked on the OS event wait, the ONLY thing that
	// brought a frame back was the idle-rate tick — so an animation ran at the idle
	// knob (10 fps by default) and, with idle=off, did not run at all until unrelated
	// damage arrived. Scheduling the flip here is what makes "wake exactly when the
	// content needs a frame" true for the parked loop as well as the running one; it
	// is the same call FramePace makes, and it answers ok=false on a static stage, so
	// a still screen still parks to zero.
	if a.d.Viewport != nil && a.link.room != nil {
		if due, ok := a.d.Viewport.NextAnimDue(a.renderScene()); ok {
			considerRender(due)
		}
	}
	// Demand-pump keepalive: while a streaming grid still shows blank cells,
	// re-render at the demand cadence so demandAsset keeps issuing asks. A batch
	// that all 404s uploads nothing (no generation bump, no self-wake), so without
	// this the pump stalls at idle=off until unrelated damage or input arrives.
	if a.frame.drawnDemandPending {
		considerRender(assetDemandWakeInterval)
	}
	// Content report / package keepalive: while a job probes or packages OFF the
	// render thread (contentjob.go), re-render at the demand cadence so tickContentJob
	// drains results and the Studio panel's live counts + Cancel stay responsive.
	// Without it, a job on a dead origin (no input, no other damage) would sit frozen
	// at idle=off until the 45s hard cap — the "progress looks stuck" trap. Reuses
	// assetDemandWakeInterval (4 Hz — ample for a progress readout).
	if a.contentJobRunning() {
		considerRender(assetDemandWakeInterval)
	}
	if wait < minWakeDelay {
		wait = minWakeDelay
	}
	return wait, render
}

// wantsFullRate reports whether something on screen genuinely needs the full
// frame rate: recent input, stage effects in flight (shake/flash), an open
// replay/maker/export surface, a live voice call, floating reactions, the
// pinned second courtroom, or the perf HUD's scrolling graph. Message ceremony
// moved to FramePace's talk/content tiers; a crossfade counts only while one
// actually RUNS (Viewport.NextAnimDue); and the perpetual ambient motions —
// the rainbow/wobble/spin/breath washes, weather, transmitted sprite styles —
// count only while a stage that shows them actually DRAWS (the viewport
// census, Viewport.AmbientAnimating → NoteAnimating, paced by FramePace's
// anim-chrome tier). The old knob-not-state checks here held full rate forever
// on every screen — lobby and Settings included — and were the "redraws for no
// reason" playtest report, back for a second round as the idle-CPU-burn one.
func (a *App) wantsFullRate() bool {
	if time.Since(a.frame.lastInputAt) < time.Duration(a.d.Prefs.Load().InputGraceFrames())*inputGraceFrameDuration {
		return true // configurable post-input hold (default 1 frame); 0 would be the input's own frame only
	}
	if time.Since(a.frame.lastMotionAt) < motionInputGrace {
		return true // a moving pointer renders live; the short grace ends with it
	}
	if a.link.room != nil {
		sc := &a.link.room.Scene
		if sc.ShakeLeft > 0 || sc.FlashLeft > 0 {
			return true // sub-second effect countdowns — self-terminating
		}
	}
	// voiceJoined, not the panel's open flag: a merely-open panel rides damage
	// and input like any other chrome; only a live call's audio engine needs
	// steady frames (SkipFrame's voice exception keeps them coming regardless —
	// this keeps their PACE at the full rate the engine was tuned for).
	if a.sel.replaying || a.sel.makerOpen || a.sel.gifExporting || a.link.voiceJoined || a.budget.perfHUD {
		return true
	}
	return len(a.reactUI.reactionFloats) > 0 || a.splitActive()
}

// SetSpriteCapBase hands the App the display-derived decode-downscale base
// (cmd/asyncao computes it once at boot), so the Settings downscale sliders can
// re-derive the effective cap live via applySpriteCap.
func (a *App) SetSpriteCapBase(px int) { a.frame.spriteCapBase = px }

// applySpriteCap pushes the effective decode-downscale cap (base × the two
// power-user knobs) to the decoder pool. Called from the Settings rows; new
// decodes pick it up (already-decoded textures keep their size until eviction).
func (a *App) applySpriteCap() {
	a.d.Manager.SetSpriteCap(config.EffectiveSpriteCap(a.frame.spriteCapBase, a.d.Prefs.Load().SpriteDownscaleOff, a.d.Prefs.Load().SpriteDownscalePctVal))
	a.d.Manager.SetAnimatedSpriteCap(a.d.Prefs.Load().AnimatedSpriteCapPxVal)
	a.d.Manager.SetAnimatedBudgetMiB(a.d.Prefs.Load().AnimatedBudgetMiB())
	a.d.Manager.SetAnimatedDecodeConcurrency(a.d.Prefs.Load().AnimDecodeConcurrency())
}

// vpSpriteLoadMode maps the 4-way loading-mode pref onto the renderer's 2-way switch:
// Wait and Default are MESSAGE-lifecycle gates (courtroom), so for the renderer they
// fall back to hold-previous — if a hold times out (404 / dead link) the stage keeps
// the previous sprite instead of flashing blank, composing the two mitigations.
func (a *App) vpSpriteLoadMode() int {
	mode := a.d.Prefs.Load().SpriteLoadModeVal
	if mode == config.SpriteLoadWait || mode == config.SpriteLoadDefault {
		return config.SpriteLoadHoldPrev
	}
	return mode
}

// applyTimingToRoom pushes the persisted crawl/stay knobs into the live
// courtroom (the crawl applies from the next message — Start precomputes
// per-rune delays).
func (a *App) applyTimingToRoom() {
	if a.link.room == nil {
		return
	}
	crawlMs, stayMs, _ := a.d.Prefs.Load().Timing()
	a.link.room.Typewriter.Interval = time.Duration(crawlMs) * time.Millisecond
	a.link.room.TextStay = time.Duration(stayMs) * time.Millisecond
	a.link.room.Typewriter.BlipRate, a.link.room.Typewriter.BlipOnSpaces = a.d.Prefs.Load().BlipTyping() // #7: blip cadence + skip-whitespace
	a.link.room.CatchUp, a.link.room.CatchUpThreshold = a.d.Prefs.Load().CatchUp()
	a.link.room.SpriteWait = a.d.Prefs.Load().SpriteLoadModeVal == config.SpriteLoadWait              // "Hold the message": hold until its speaker sprite decodes
	a.link.room.SpriteWaitTimeout = time.Duration(a.d.Prefs.Load().SpriteWaitMs()) * time.Millisecond // its user-tunable hold cap
	a.link.room.SpriteWaitPair = a.d.Prefs.Load().SpriteWaitPair                                      // strictness: gate on the pair partner too
	a.link.room.SpriteWaitPreanim = a.d.Prefs.Load().SpriteWaitPreanim                                // strictness: gate on the preanim too
	a.link.room.SequentialWait = a.d.Prefs.Load().SpriteLoadModeVal == config.SpriteLoadDefault       // "Default": wait for every asset a message presents
	a.link.room.ShoutBypassQueue = a.d.Prefs.Load().ShoutBypassQueue                                  // AO2 parity: shouts skip the queue (toggleable)
	a.link.room.ShoutDuration = courtroom.DefaultShoutDuration                                        // core-timing knobs: 0 = the canonical defaults
	if ms := a.d.Prefs.Load().ShoutDurationMsVal; ms != 0 {
		a.link.room.ShoutDuration = time.Duration(ms) * time.Millisecond
	}
	a.link.room.PreanimTimeout = courtroom.DefaultPreanimTimeout
	if ms := a.d.Prefs.Load().PreanimTimeoutMsVal; ms != 0 {
		a.link.room.PreanimTimeout = time.Duration(ms) * time.Millisecond
	}
	a.link.room.QueueCap = courtroom.DefaultQueueCap // 0-pref = the canonical depth; always assigned so a nuke-reset restores it
	if n := a.d.Prefs.Load().ICQueueCapVal; n != 0 {
		a.link.room.QueueCap = n
	}
	a.link.room.CatchUpLinger = time.Duration(a.d.Prefs.Load().CatchUpLingerMsVal) * time.Millisecond
	// The four pref-driven VISUAL gates (including the master off-switch's
	// precedence over both effect knobs) live in applyVisualPrefsToRoom, because
	// newRoom seeds every OTHER room with them at construction and two spellings
	// of "what a room shows" is how four of the five ended up on a hardcoded
	// default. This call is the LIVE room's re-push when a setting changes.
	a.applyVisualPrefsToRoom(a.link.room)
	a.link.room.AdditiveText = a.d.Prefs.Load().AdditiveText  // #14 2.8 additive: honor incoming ADDITIVE=1 append (default ON)
	a.link.room.StreamMusic = a.d.Prefs.Load().MusicStreaming // §1.3: OFF = never fetch a /play track (Now-Playing still tracks it)
}

// applyMusicStreaming persists the custom-/play-music streaming toggle and pushes
// it onto the live room, so both settings surfaces (Settings → Audio and the quick
// volume popover) share one behaviour. On the OFF transition it also stops any
// currently-streaming track (and cancels a pending fetch) so it goes silent at
// once instead of on the next song change.
func (a *App) applyMusicStreaming(on bool) {
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			MusicStreaming =

			on

	})

	if a.link.room != nil {
		a.link.room.StreamMusic = on
	}
	if !on {
		a.d.Audio.StopMusic()                  // halt the current track and cancel any pending fetch
		a.budget.musicTabDucked = false        // stream stopped: not ducked (invariant)
		a.budget.musicAwaitURL = ""            // and no pending un-duck (nothing will be delivered)
		a.budget.musicAwaitSince = time.Time{} // clear the timeout stamp with the await it belongs to
	}
}

// applyMusicAcrossTabs persists the cross-tab music-continuity toggle and, on the
// ON transition, un-wedges a backgrounded stream that's currently silenced. It's a
// method (not inline in the settings row) so a headless test can drive exactly what
// the checkbox does — the same shape as applyMusicStreaming.
func (a *App) applyMusicAcrossTabs(on bool) {
	a.d.Prefs.Update(func(
		sn *config.
			Settings) {

		sn.
			MusicAcrossTabs =

			on

	})

	if on {
		a.budget.musicTabDucked = false
		a.budget.musicAwaitURL = ""
		a.budget.musicAwaitSince = time.Time{}
		a.applyAudioVolumes()
	} else {
		a.budget.musicTabDucked = true
		a.applyAudioVolumes()
	}
}

// musicResumeMaxSeekSec caps the cross-tab resume seek ON THE FALLBACK PATH ONLY
// — when no takeover snapshot exists (an old build, a mod/midi with no knowable
// duration, or the track was never parked). There, duration is unknown, so a very
// stale backgrounded track can't be wrapped by length; past a few minutes a seek
// is as likely to overshoot the end (→ Mix_SetMusicPosition fails → restart) as to
// help, and even when it lands it's musically meaningless. Beyond this we just
// restart from the top. The exact, loop-aware path (Audio.SwappedOutSnap +
// resumeSeek) does NOT use this cap: it wraps by the real duration instead.
const musicResumeMaxSeekSec = 600 // 10 minutes

// resumeSeek is the exact, loop-aware cross-tab resume math, split out
// as a pure func so it's unit-pinnable headlessly like applyResumeDuck /
// mixChannels. Given a takeover snapshot — the outgoing track's position snapPos
// and total duration snapDur (seconds) captured at swap-out — plus elapsedSec of
// wall time since, it returns where to seek the resumed stream and whether to
// play it at all:
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

// resumeActiveTabMusic re-seeds the newly-active tab's area song into the single
// music stream on a room (re)build. It's the cross-tab continuity core:
// StreamMusic OFF means nothing is fetched or played (§1.3): we only track
// Now-Playing then, and leave the (nonexistent) stream's duck alone. Render thread
// only (buildRoom runs there); no new goroutines.
func (a *App) resumeActiveTabMusic() {
	track := a.link.sess.MusicTrack
	if track == "" {
		return
	}
	// Now-Playing reflects the room's intended track even when streaming is off.
	a.link.room.Scene.MusicTrack = track
	if !a.link.room.StreamMusic || a.d.Audio == nil {
		return // §1.3: OFF fetches/plays nothing; no device = nothing to duck or seek
	}
	// Loop: true — a resumed persistent area track keeps AsyncAO's client-side loop
	// (#15; the original MC's loop/effect flags aren't retained on the session, and a
	// room-rebuild resume has always looped). No fade on a resume (effects = 0): the
	// track was already "playing" from the listener's view, so it shouldn't ramp in.
	const resumeLoop = true
	const resumeEffects = 0
	url := a.link.urls.MusicURL(track)
	sameTrack := a.d.Audio.CurrentMusicURL() == url && a.d.Audio.MusicPlaying()
	if sameTrack {
		// The stream is already this exact track, still rolling (kept alive, ducked,
		// across the switch). No delivery is coming — the RIGHT song is already
		// playing — so the duck decision below un-ducks IMMEDIATELY (PRESERVE clause).
		a.applyResumeDuck(true, url)
		return
	}
	// The stream isn't this track (it was swapped to another tab's song, or this is a
	// fresh entry): (re)fetch and seek to where it would be by now. Derive seekSec +
	// whether to play at all FIRST, so the duck/await state below stays coherent (an
	// ended one-shot must NOT hold a dangling await).
	seekSec := 0.0
	play := true
	if snapPos, snapDur, snapAt, ok := a.d.Audio.SwappedOutSnap(url); ok {
		// Exact, loop-aware resume: the outgoing track's TRUE position at
		// swap-out plus the wall time it kept playing during the download, wrapped by
		// its real duration. This is the drift-free path the users were promised.
		elapsed := a.now().Sub(snapAt).Seconds()
		seekSec, play = resumeSeek(snapPos, snapDur, elapsed, resumeLoop)
	} else if !a.musicTab.musicStartedAt.IsZero() {
		// No snapshot (old build, resolver miss, mod/midi with unknown length, or a
		// first play): keep the legacy wall-clock estimate, capped, exactly as before.
		if el := a.now().Sub(a.musicTab.musicStartedAt).Seconds(); el > 0 && el <= musicResumeMaxSeekSec {
			seekSec = el
		}
	}
	if !play {
		// The parked one-shot ENDED while backgrounded (only reachable when
		// resumeLoop is false — future-proofing; it's const true today). Don't
		// restart it: leave whatever the stream is. But we're taking the swap path,
		// so nothing new will be delivered — clear any await and drop the duck so the
		// state stays coherent (a held await with no incoming url would silently duck
		// this stream forever). applyAudioVolumes reflects the un-ducked decision.
		a.budget.musicAwaitURL = ""
		a.budget.musicAwaitSince = time.Time{}
		a.budget.musicTabDucked = false
		a.applyAudioVolumes()
		return
	}
	// The duck decision is the same bug-class guard as the DJ /play path — it HOLDS
	// the duck and awaits this url, so settleAwaitedMusic un-ducks only when the new
	// track's bytes land (never the foreign stream mid-fetch). Set the await + hold
	// the duck BEFORE issuing the fetch so the guard is in place first.
	a.applyResumeDuck(false, url)
	a.d.Audio.PlayMusicAt(url, resumeLoop, resumeEffects, seekSec) // AssetType: Music
}

// applyResumeDuck is resumeActiveTabMusic's pure duck decision (unit-pinnable like
// mixChannels), split from the Audio reads so the flag transitions — the part with
// the "wrong song plays" bug history — are testable headlessly. sameTrack=true means
// the stream is already the active tab's exact track (kept rolling across the
// switch): un-duck NOW and drop any stale await (no delivery is coming). sameTrack=
// false means a foreign/other-tab track is live (or a fresh fetch was just issued):
// HOLD the duck and AWAIT url — un-ducking now would make the foreign stream audible
// during the fetch; settleAwaitedMusic lifts the duck when url is delivered. With the
// MusicAcrossTabs pref ON the duck is already down (no isolation wanted); the await is
func (a *App) applyResumeDuck(sameTrack bool, url string) {
	if sameTrack {
		// Nothing awaited once we're on our own track; the right song is audible now.
		a.budget.musicAwaitURL = ""
		a.budget.musicAwaitSince = time.Time{}
		a.budget.musicTabDucked = false
	} else if a.budget.musicTabDucked {
		// Swap path, and there's a real duck to hold: arm the await so the duck lifts
		// only once THIS track is the one the mixer is playing (never a foreign track
		// mid-fetch). GATED on the duck deliberately, mirroring the DJ /play arm (which
		// arms only `if a.musicTabDucked`). Under MusicAcrossTabs ON the duck is already
		// down (no acoustic isolation wanted) — arming an await then is pure liability:
		a.budget.musicAwaitURL = url       // un-duck only once THIS track is the one playing
		a.budget.musicAwaitSince = a.now() // stamp for the never-arrives timeout release
	}
	// The active tab just claimed the single stream with its own track (resume,
	// either the same track it kept rolling or a freshly-fetched one): it now owns
	// the stream, so its per-server music volume governs it. a.serverKey is the
	// active tab's key here — see musicOwnerKey.
	a.budget.musicOwnerKey = a.srvMeta.serverKey
	a.applyAudioVolumes()
}

// activeCharName is the character folder OUTGOING messages use: the
// iniswap override when set (AO iniswapping — the server slot keeps the
// list character, the wire carries the custom folder; AO2-Client
// set_iniswap semantics), else our picked character.
func (a *App) activeCharName() string {
	if a.inibrowse.iniChar != "" {
		return a.inibrowse.iniChar
	}
	return a.myCharName()
}

// setIniswap applies a custom character override ("" reverts to the picked
// character) and reloads the emote list for the new active folder.
func (a *App) setIniswap(name string) {
	a.inibrowse.iniChar = name
	a.compose.charDefaultSide = "" // the new folder's char.ini decides the default side; until it lands there is none
	a.emotepick.emoteAsk = nil
	a.emotepick.emoteIdx, a.emotepick.emoteRow = 0, 0 // new active folder = new emote list: start at the first
	a.loadCharINI()
}

// wardrobeAct is the resolved action for clicking a Characters-tab favourite.
// Pure + table-tested because this is the bit users are
// touchy about — a favourite must never silently iniswap when it could switch.
type wardrobeAct int

const (
	actSwitch  wardrobeAct = iota // take the character's slot (a real char change)
	actIniswap                    // wear its look without taking a slot
)

// wardrobeAction decides what a Characters-tab click does. Switching to the
// character is the default; we fall back to an iniswap only when the user ticked
// iniswap mode, when the name isn't one of this server's characters (slot < 0),
// or when that character's slot is already taken (can't claim an occupied one).
func wardrobeAction(iniswapMode bool, slot int, taken bool) wardrobeAct {
	if iniswapMode || slot < 0 || taken {
		return actIniswap
	}
	return actSwitch
}

// charSlotByName finds the server character slot whose folder name matches
// (case-insensitive), or -1. Linear, but only runs on an actual wardrobe click.
func (a *App) charSlotByName(name string) int {
	want := strings.ToLower(strings.TrimSpace(name))
	for i := range a.link.sess.Chars {
		if strings.ToLower(a.link.sess.Chars[i].Name) == want {
			return i
		}
	}
	return -1
}

// wardrobeClick handles a click on a Characters-tab favourite: switch to that
// character (claim its slot — the same CC → PV → enterCourtroom flow as the
// "Character" button) by default, or iniswap it when iniswap mode is ticked. A
// switch we can't perform (taken slot, or a name that isn't a server character)
// falls back to an iniswap — with a toast, so a favourite never silently swaps.
func (a *App) wardrobeClick(name string) {
	a.inibrowse.showIni = false
	if a.link.room == nil {
		// Char-select Wardrobe tab: pick the REAL free character (the switch a
		// favourite is expected to do) when it's a claimable server slot; only
		// fall back to an iniswap (free slot + its look) when it's taken or not a
		// server character. Same decision as the courtroom path below, via
		// wardrobeAction — this is the half the original fix missed, so picking a
		// favourite on reconnect iniswapped instead of switching.
		slot := -1
		taken := false
		if a.link.sess != nil {
			slot = a.charSlotByName(name)
			taken = slot >= 0 && a.link.sess.Chars[slot].Taken
		}
		if wardrobeAction(a.inibrowse.iniSwapMode, slot, taken) == actSwitch {
			a.pickCharacter(slot)
			return
		}
		a.wearFromMenu(name)
		return
	}
	slot := a.charSlotByName(name)
	taken := slot >= 0 && a.link.sess.Chars[slot].Taken
	if wardrobeAction(a.inibrowse.iniSwapMode, slot, taken) == actSwitch {
		a.pickCharacter(slot) // CC → PV → EventCharPicked → enterCourtroom (iniswap clears)
		return
	}
	if !a.inibrowse.iniSwapMode { // a switch was wanted but impossible — say why, don't swap silently
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

// wearFromMenu handles a wardrobe pick from either screen. In the
// courtroom it's an instant swap; on char select (fresh join) an iniswap
// still needs a server SLOT, so claim the first free one and wear the
// custom when PV confirms (enterCourtroom applies pendingIni).
func (a *App) wearFromMenu(name string) {
	a.inibrowse.showIni = false
	if a.link.room != nil {
		a.setIniswap(name)
		a.screen = ScreenCourtroom
		return
	}
	free := -1
	for i := range a.link.sess.Chars {
		if !a.link.sess.Chars[i].Taken {
			free = i
			break
		}
	}
	if free < 0 {
		a.warnLine = "No free character slots to host an iniswap — every slot is taken"
		a.warnAt = time.Now()
		return
	}
	a.inibrowse.pendingIni = name
	a.pickCharacter(free) // rehearsal resolves locally
}

// openIniswap shows the wardrobe menu (courtroom modal).
func (a *App) openIniswap() {
	a.inibrowse.showIni = true
	a.ensureIniList()
	if a.wardrobe.wardSection == wardSectionBackgrounds {
		a.rebuildBgFav() // favourites may have changed since this section last drew
	}
}

// ensureIniList makes the merged wardrobe menu current: the wardrobe half
// renders instantly from prefs; the server's iniswap.txt merges in when
// its fetch lands (FetchRaw: T2 + disk cached, singleflight — a reopen
// is a memory hit).
func (a *App) ensureIniList() {
	a.rebuildIniMenu() // wardrobe is local: usable before (or without) the txt
	if a.inibrowse.iniServer != nil || a.inibrowse.iniBusy || a.link.urls.Origin() == "" {
		return
	}
	a.inibrowse.iniBusy = true
	a.inibrowse.iniListErr = ""
	url := a.link.urls.Origin() + iniswapFileName
	bgURL := a.link.urls.BackgroundsRoot()
	key := a.srvMeta.serverKey
	mgr := a.d.Manager
	ch := a.iniRes
	go func() {
		// Same guard as the background-list goroutine: a malformed autoindex
		// tripping parseAutoindexDirs must degrade, never hard-crash off-thread.
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
		// The backgrounds root rides the same cached fetch path (the bg picker
		// shares the URL, so at most one network hit between them) purely to
		// FILTER the list; a host with no listing is not an error (nil = the
		// remembered names alone do the filtering).
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
		if res.key != a.srvMeta.serverKey {
			return // landed after a tab switch: not this server's list
		}
		a.inibrowse.iniBusy = false
		if res.err != nil {
			a.inibrowse.iniListErr = "no server list (" + res.err.Error() + ") — your wardrobe still works"
			a.inibrowse.iniServer = nil
		} else {
			a.inibrowse.iniServer = filterServerIniswaps(res.names, res.bgs,
				a.d.Prefs.Load().ServerWarmInfoFor(a.srvMeta.serverKey).Backgrounds)
		}
		a.rebuildIniMenu()
	default:
	}
}

// rebuildIniMenu merges the menu: wardrobe first (the user's saved
// favourites — instant swaps, persisted across sessions and servers),
// then server-list entries not already in the wardrobe.
func (a *App) rebuildIniMenu() {
	names, fromWardrobe, inServer := mergeWardrobe(a.d.Prefs.Load().WardrobeList(a.srvMeta.serverKey), a.inibrowse.iniServer)
	a.inibrowse.iniList = names
	a.inibrowse.iniWardrobe = fromWardrobe
	a.inibrowse.iniServerMem = inServer
	a.inibrowse.iniLower = make([]string, len(names))
	folders := a.d.Prefs.Load().WardrobeFolderMap(a.srvMeta.serverKey)
	a.inibrowse.iniFolders = make([]string, len(names))
	a.inibrowse.iniHoverIDs = make([]string, len(names))
	for i, n := range names {
		lower := strings.ToLower(n)
		a.inibrowse.iniLower[i] = lower
		a.inibrowse.iniFolders[i] = folders[lower] // "" when unsorted / not filed
		a.inibrowse.iniHoverIDs[i] = iniHoverIDPrefix + n
	}
	a.inibrowse.iniAsk = nil
	// Toggling a star reorders the list at the SAME length (wardrobe entries
	// float up), and cachedPage keys its icon slice by index without re-checking
	// the URL — so drop the idx→page cache here or a reorder would paint the
	// previous name's icon. Icons re-resolve from T1 next frame (a map hit).
	a.rcache.iniPages = nil
}

// iniHoverIDPrefix namespaces a wardrobe cell's HoverPreview id, exactly as
// charHoverIDPrefix does for the Characters grid: the kit keys dwell state by id
// across the whole frame, and the two grids can be on screen at the same time (the
// wardrobe modal over the courtroom), so their ids must not collide.
const iniHoverIDPrefix = "iniswap:"

// iniHoverID is wardrobe entry i's cached HoverPreview id. The out-of-range arm
// rebuilds rather than indexing past the cache: every caller draws behind
// rebuildIniMenu, but this is a DRAW loop, and paying one allocation on a path that
// should be unreachable beats panicking the client if it ever is reached.
func (a *App) iniHoverID(i int) string {
	if i >= 0 && i < len(a.inibrowse.iniHoverIDs) {
		return a.inibrowse.iniHoverIDs[i]
	}
	if i < 0 || i >= len(a.inibrowse.iniList) {
		return iniHoverIDPrefix
	}
	return iniHoverIDPrefix + a.inibrowse.iniList[i]
}

// applyPendingFav applies a wardrobe ★ toggle that a cell DEFERRED this frame. It
// rebuilds (and shrinks/grows) the iniList/iniWardrobe/iniFolders slices, so it MUST
// run after the grid loop that ranges them — toggling inside the cell shrank those
// slices mid-iteration and panicked on a remove (the reported crash). A no-op when
// nothing was deferred.
func (a *App) applyPendingFav() {
	if a.inibrowse.iniFavPending == "" {
		return
	}
	name := a.inibrowse.iniFavPending
	a.inibrowse.iniFavPending = ""
	if a.inibrowse.iniFavPendingAdd {
		a.d.Prefs.AddWardrobe(a.srvMeta.serverKey, name)
	} else {
		a.d.Prefs.RemoveWardrobe(a.srvMeta.serverKey, name)
	}
	a.rebuildIniMenu()
}

// ensureWardrobeMembers rebuilds the current server's lowercased wardrobe set
// when the server changed or the wardrobe was edited (generation bump). It
// reuses the map (clear-in-place) so the steady state allocates nothing; the
// per-cell star lookup in drawCharCell then reads it lock-free.
func (a *App) ensureWardrobeMembers() {
	gen := a.d.Prefs.WardrobeGeneration()
	if a.charsel.wardrobeMembers != nil && a.charsel.wardrobeMembersFor == a.srvMeta.serverKey && a.charsel.wardrobeMembersGen == gen {
		return
	}
	if a.charsel.wardrobeMembers == nil {
		a.charsel.wardrobeMembers = make(map[string]bool)
	} else {
		clear(a.charsel.wardrobeMembers)
	}
	for _, name := range a.d.Prefs.Load().WardrobeList(a.srvMeta.serverKey) {
		a.charsel.wardrobeMembers[strings.ToLower(name)] = true
	}
	a.charsel.wardrobeMembersFor = a.srvMeta.serverKey
	a.charsel.wardrobeMembersGen = gen
}

// mergeWardrobe combines your wardrobe favourites with the server's iniswap
// list for the wardrobe grid: favourites come first (starred), then the server
// entries the favourites didn't already cover. The Iniswaps tab shows only the
// server list — a favourite that ISN'T a server iniswap stays out of it, and a
// server with no list shows nothing. Server duplicates collapse into their
// wardrobe entry, case-insensitively.
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

// filterServerIniswaps drops background folder names from the server's parsed
// iniswap list. Some servers publish one combined "everything streamable" txt
// (playtest: 187 of one server's 376 iniswap entries were its background/ dir),
// and a background wears like a broken character. The exclusion set is the live
// background/ autoindex (fetched alongside the txt) plus last session's
// remembered names — either half may be missing (no listing / first visit), so
// they union. Tradeoff, deliberately accepted: a real character sharing a
// background's exact folder name is hidden from the grids too, but it can
// still be worn by name (the Iniswaps search box or the wardrobe Add field).
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

// parseIniswapList parses iniswap.txt: one character folder name per line,
// blanks skipped, case-insensitive dedupe, capped, sorted for the menu.
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

// cachedPage resolves a grid cell's texture page through a
// generation-keyed cache: pointers stay valid exactly while the store
// generation holds (evictions bump it, and evicted textures only destroy
// via the queue after the bump — same safety argument as the viewport's
// animState). Slices clear in place on a generation move (no per-move
// allocation; reallocate only when the list length changes).
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

// demandAsset paces one visible cell's asset demand: shared per-frame
// budget, one ask per slot per charIconRetryInterval (self-heals shed
// low-lane jobs). stamps resizes lazily to n; callers tag the asset type.
// The conclusive-miss check comes before the pacing, not after, because it is
// the cadence itself that had to stop. This retry loop only ever terminated on
// residency, and a 404'd base can never become resident, so a roster whose
func (a *App) demandAsset(stamps *[]time.Time, n, idx int, base string, t assets.AssetType) bool {
	if idx < 0 || idx >= n {
		return false
	}
	if a.d.Manager.IsConclusiveMiss(base, t) {
		return false
	}
	if a.budget.iconAskBudget <= 0 {
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
	a.budget.iconAskBudget--
	a.d.Manager.Prefetch(base, t, network.PriorityLow)
	return true
}

// charINIURL builds a character's char.ini location.
// Through the URLBuilder's CharFolder, never a hand-rolled origin+"characters/"+name: that is the
func (a *App) charINIURL(name string) string {
	return a.link.urls.CharFolder(name) + charINIFileName
}

// warmCharINI speculatively fetches a hovered character's char.ini so the
// eventual pick costs a memory hit instead of an RTT. One submit per
// hovered name; the manager dedupes and the 404 cache absorbs misses.
func (a *App) warmCharINI(name string) {
	if name == "" || name == a.compose.iniWarmed || a.link.urls.Origin() == "" {
		return
	}
	a.compose.iniWarmed = name
	a.d.Manager.PrefetchRaw(a.charINIURL(name), network.PriorityLow) // raw text: char.ini
}

// previewEmoteCap bounds one previewed character's try-before-wear emote cycle
// (rule §17.4) — a pathological char.ini can't grow the slice unbounded.
const previewEmoteCap = 256

// previewIdleAnim is the emote name AO packs conventionally give the resting
// pose, and the one both preview surfaces probe optimistically on hover
// (characters/<x>/(a)normal — the webAO URL convention). Most packs have it, so
// the box fills in a single probe; the ones that don't are corrected from their
// char.ini (correctPortraitPreview).
const previewIdleAnim = "normal"

// ensurePreviewEmotes' cycle argument, named so the call sites read as prose
// (App.previewEmoteCycle documents what each landing does).
const (
	previewEmoteNav      = true  // wardrobe: drives the ‹ › try-before-wear navigator
	previewEmotePortrait = false // char-select grid: one hover portrait, no navigator
)

// ensurePreviewEmotes loads a previewed (non-worn) character's emote list ONCE
// so the preview box can cycle it (try-before-wear) or correct a pack that has
// no "normal" emote (char-select). The char.ini was just warmed on hover
// (warmCharINI → PrefetchRaw), so the fetch rides the cache; the parse runs
// off-thread and lands in previewEmoteRes. previewChar guards it to one parse
// per character.
func (a *App) ensurePreviewEmotes(name string, cycle bool) {
	if name == "" || name == a.budget.previewChar || a.link.urls.Origin() == "" {
		return
	}
	a.budget.previewChar = name
	a.budget.previewEmoteCycle = cycle
	a.budget.previewAnims = nil
	a.budget.previewLabels = nil
	a.budget.previewEmoteIdx = 0
	url := a.charINIURL(name)
	key := a.srvMeta.serverKey
	mgr := a.d.Manager
	ch := a.previewEmoteRes
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), iniswapFetchTimeout)
		defer cancel()
		// LAYERED, like loadCharINI: previewing a character the user's own pack
		// supplies must cycle THEIR emotes, not the server's idea of that folder.
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

// pollPreviewEmotes drains a finished preview-emote parse (dropped on a tab
// switch or a newer hover — the key/char guards). For the wardrobe's ‹ ›
// navigator it then advances to emote 0 so the index always matches what's
// displayed; for the char-select portrait it only corrects a pack that has no
// "normal" emote (correctPortraitPreview). Every screen that can call
// ensurePreviewEmotes must call this, or the parse goroutine parks on the
// one-slot channel.
func (a *App) pollPreviewEmotes() {
	select {
	case res := <-a.previewEmoteRes:
		if res.key != a.srvMeta.serverKey || res.char != a.budget.previewChar {
			return
		}
		a.budget.previewAnims = res.anims
		a.budget.previewLabels = res.labels
		a.budget.previewEmoteIdx = 0
		if len(res.anims) == 0 {
			return
		}
		if !a.budget.previewEmoteCycle {
			// char-select portrait. The box outlives the trigger cell (the travel
			// corridor keeps it up once the cursor leaves), so a late parse has to
			// correct it here instead of waiting for another hover frame.
			a.correctPortraitPreview(res.char)
			return
		}
		if a.budget.previewBase == a.link.urls.Emote(res.char, previewIdleAnim, courtroom.EmoteIdle) {
			a.setPreviewEmote(0)
		}
	default:
	}
}

// previewPortraitAnim returns the emote the char-select grid should show for
// name: the conventional "normal" idle pose, or — once the char.ini has been
// parsed and turns out NOT to name one — the pack's FIRST real emote.
// Until the parse lands it answers the convention, so the common pack still
// fills the box from ONE optimistic probe and never pays for a second.
func (a *App) previewPortraitAnim(name string) string {
	if name != a.budget.previewChar || len(a.budget.previewAnims) == 0 {
		return previewIdleAnim // not parsed (yet): the optimistic convention
	}
	for _, anim := range a.budget.previewAnims {
		// Case-insensitive: emote anims are lowercased into the URL (segPath), so
		// a char.ini "Normal" resolves to the very base already probed.
		if strings.EqualFold(anim, previewIdleAnim) {
			return previewIdleAnim
		}
	}
	return a.budget.previewAnims[0]
}

// setCharPortraitPreview points the char-select hover box at name's portrait and
// demands it. Idempotent by construction (it derives the emote from state, never
// advances an index), because HoverPreview fires EVERY frame the cursor rests on
// a cell — the same shape the grid always had, so a hovered cell still costs one
// demand per frame and no more.
func (a *App) setCharPortraitPreview(name string) {
	anim := a.previewPortraitAnim(name)
	a.budget.previewBase = a.link.urls.Emote(name, anim, courtroom.EmoteIdle)
	a.d.Manager.PrefetchChain(a.budget.previewBase, a.link.urls.EmoteAlts(name, anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview)
}

// correctPortraitPreview repoints an ALREADY-OPEN char-select portrait after its
// char.ini landed. It moves the box only when the optimistic "normal" base is
// still what's displayed AND the pack has no such emote — so a pack that does
// have "normal" costs no second probe, and a box another surface has since
// claimed is left alone.
func (a *App) correctPortraitPreview(name string) {
	if a.budget.previewBase != a.link.urls.Emote(name, previewIdleAnim, courtroom.EmoteIdle) {
		return
	}
	if a.previewPortraitAnim(name) == previewIdleAnim {
		return
	}
	a.setCharPortraitPreview(name)
}

// setPreviewEmote points the preview box at emote i (wrapping) of the previewed
// character — the talk (b) pose, like the courtroom emote strip's hover.
func (a *App) setPreviewEmote(i int) {
	n := len(a.budget.previewAnims)
	if n == 0 || a.budget.previewChar == "" {
		return
	}
	a.budget.previewEmoteIdx = ((i % n) + n) % n
	anim := a.budget.previewAnims[a.budget.previewEmoteIdx]
	a.budget.previewBase = a.link.urls.Emote(a.budget.previewChar, anim, courtroom.EmoteTalk)
	a.d.Manager.PrefetchChain(a.budget.previewBase, a.link.urls.EmoteAlts(a.budget.previewChar, anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite (preview cycle)
}

// cyclePreviewEmote steps the try-before-wear preview by delta (wrapping).
func (a *App) cyclePreviewEmote(delta int) { a.setPreviewEmote(a.budget.previewEmoteIdx + delta) }

// clampEmoteSel returns the emote index / grid scroll row to use after an emote
// list of length n is (re)loaded. A still-valid index is PRESERVED — so a tab
// reactivation (activateTab → buildRoom → loadCharINI lands for the SAME
// character) keeps the user's selection instead of snapping back to the first
// emote. An out-of-range index (a shorter list, e.g. an iniswap) resets to the
// first emote at the top of the grid. Index and scroll move together so a kept
// index can't desync from a stale scroll position. Pure so the session-isolation
// behaviour is pinned by a unit test, not a manual rebuild.
func clampEmoteSel(idx, row, n int) (int, int) {
	if idx < 0 || idx >= n {
		return 0, 0
	}
	return idx, row
}

// loadCharINI fetches the ACTIVE character's char.ini for the emote list
// (the iniswap override when set).
func (a *App) loadCharINI() {
	name := a.activeCharName()
	if a.link.sess == nil || name == "" {
		return
	}
	url := a.charINIURL(name)
	a.compose.charINIBusy = true
	key := a.srvMeta.serverKey
	mgr := a.d.Manager
	ch := a.charINIres
	go func() {
		// LAYERED: a character served out of the user's own folders must bring its
		// own emote list, or their sprites load under somebody else's poses (#72).
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
	if a.client.pinnedPass {
		return // the full-theme pinned render must not drain the primary's char.ini result
	}
	select {
	case res := <-a.charINIres:
		if res.key != a.srvMeta.serverKey {
			return // landed after a tab switch: another server's char.ini
		}
		a.compose.charINIBusy = false
		a.uiDirty = true // the emote list just changed (and "Loading emotes…" must clear): force one redraw, like the character list does on its packet
		// New emote list = new button art; the gen-keyed page caches key
		// by INDEX, so a same-length iniswap would show the old char's
		// buttons without this.
		a.rcache.emoteBtnOff, a.rcache.emoteBtnOn, a.rcache.emoteIconPages = nil, nil, nil
		if res.err != nil || res.ini == nil {
			// Surface WHY the emote list is a bare default (better than a
			// silent single "normal" chip).
			reason := "empty char.ini"
			if res.err != nil {
				reason = res.err.Error()
			}
			a.warnLine = clampLine("char.ini for " + a.activeCharName() + ": " + reason + " — using a default emote")
			a.warnAt = time.Now()
			a.emotepick.emotes = []courtroom.Emote{{Comment: "normal", Anim: "normal", Preanim: "-"}}
			a.emotepick.emoteIdx, a.emotepick.emoteRow = clampEmoteSel(a.emotepick.emoteIdx, a.emotepick.emoteRow, len(a.emotepick.emotes))
			// The char.ini never arrived, so this character declares no default side.
			// CLEARING is the whole point: without it the PREVIOUS character's default
			// survives, pos_remove paints for a side the user never overrode, and one
			// click sends a /pos the character never asked for. AO2 has no such window —
			// get_char_side simply returns "wit" for an unreadable ini
			// (text_file_functions.cpp:480-483), which is what defaultSide() reports for
			// an empty value.
			a.compose.charDefaultSide = ""
			return
		}
		a.emotepick.emotes = res.ini.Emotes
		if len(a.emotepick.emotes) == 0 {
			a.emotepick.emotes = []courtroom.Emote{{Comment: "normal", Anim: "normal", Preanim: "-"}}
		}
		// OUR side comes from our char.ini (AO2-Client semantics), never
		// from whoever spoke last; /pos overrides it.
		// Lifted out of the if-init so BOTH fields can be written: charDefaultSide
		// records what this character declares (AO2's default_side, courtroom.cpp:4723),
		// and it must be assigned even when the value is empty so a previous
		// character's default cannot survive an iniswap.
		side := strings.ToLower(strings.TrimSpace(res.ini.Side))
		a.compose.charDefaultSide = side
		if side != "" {
			a.compose.sidePref = side
		}
		a.compose.charBlips = res.ini.Blips
		// Custom shout menu: the base "custom" entry (renamed by
		// custom_name) plus the named 2.10 interjections.
		a.compose.customShouts = res.ini.CustomShouts
		a.compose.customIdx = -1 // base custom
		a.compose.customName = res.ini.CustomName
		// Preserve the per-tab emote selection across a REACTIVATION reload: snap
		// to the first emote only when the restored index no longer fits the
		// freshly loaded list. A real character change (enterCourtroom / setIniswap)
		// zeroes these synchronously before the fetch, so a fresh pick still starts
		// at emote 0 — see clampEmoteSel.
		a.emotepick.emoteIdx, a.emotepick.emoteRow = clampEmoteSel(a.emotepick.emoteIdx, a.emotepick.emoteRow, len(a.emotepick.emotes))
		a.emotepick.emoteAsk = nil // fresh char: re-demand its button art from scratch
		// Speculatively prefetch emote sprites at LOW priority (sheds under load, never blocks
		// live HIGH fetches). #127: with the bundle toggle on, grab this character's FULL set —
		// every emote's idle AND talk (with the bare-spelling fallback) — so switching emotes is
		// instant; off keeps the lightweight first-few-idles default. Fired once per char load
		// (this branch runs on the char.ini result), and singleflight collapses any that are
		// already cached/in-flight.
		me := a.myCharName()
		if a.d.Prefs.Load().CharBundlePrefetch {
			for _, e := range a.emotepick.emotes {
				a.d.Manager.PrefetchChain(a.link.urls.Emote(me, e.Anim, courtroom.EmoteIdle), a.link.urls.EmoteAlts(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
				a.d.Manager.PrefetchChain(a.link.urls.Emote(me, e.Anim, courtroom.EmoteTalk), a.link.urls.EmoteAlts(me, e.Anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (#127 bundle)
			}
		} else {
			for i, e := range a.emotepick.emotes {
				if i >= 8 {
					break
				}
				// Bare-spelling fallback like the bundle path above: a mixed-naming
				// pack ships this emote only under the unprefixed name, and a single-
				// spelling prefetch would 404 the prefixed base and (once displayed)
				// false-flag it missing → a missingno flash over a sprite that exists.
				// The full chain makes "chain exhausted" mean "truly absent."
				a.d.Manager.PrefetchChain(a.link.urls.Emote(me, e.Anim, courtroom.EmoteIdle), a.link.urls.EmoteAlts(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite
			}
		}
	default:
	}
}

func (a *App) myCharName() string {
	if a.link.sess == nil || a.link.sess.MyCharID < 0 || a.link.sess.MyCharID >= len(a.link.sess.Chars) {
		return ""
	}
	return a.link.sess.Chars[a.link.sess.MyCharID].Name
}

// --- lobby ----------------------------------------------------------------------

// RefreshServers fetches the master list asynchronously.
