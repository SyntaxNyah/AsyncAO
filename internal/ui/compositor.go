package ui

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// The compositor's damage model (selective rendering).
//
// The main loop keeps the whole UI in a cached render-target texture
// (render.Canvas) and blits it to the backbuffer EVERY pass — presents stay
// dense at a steady cadence, which is what this driver class needs (the
// playtest evidence: sparse presents themselves flicker; continuous full
// redraws burn GPU). What made the old builds expensive — the UI walk and
// its fill — happens only when this file says so, and only inside the
// regions it accumulates:
//
//   - nothing changed            → no walk at all (blit + present only)
//   - a hover boundary crossed   → walk clipped to the crossed rects
//   - the caret blinked          → walk clipped to the focused field
//   - the stage animated         → walk clipped to the viewport rect
//   - a message is playing       → walks at the talk cadence, clipped to
//     viewport ∪ chatbox ∪ log
//   - anything discrete/unknown  → full-frame walk (input, packets,
//     texture traffic, reveals, resets)
//
// Every check here is a few compares on render-thread-owned state: no locks,
// no allocations, no syscalls (rule §17). The walk itself always runs the
// FULL immediate-mode pass — widget logic must fire exactly once per drawn
// frame — the damage clip only bounds which pixels it may touch.

// dmgRectCap bounds the per-pass damage list; past it the pass promotes to a
// full-frame walk (conservative: extra pixels, never a stale region).
const dmgRectCap = 8

// timerTickBudget paces the full-frame walks that keep mm:ss readouts honest
// while a local alarm or a server TI clock is visible: one per displayed
// second (the classic loop's per-second wake, expressed as a walk budget).
const timerTickBudget = time.Second

// diagTickBudget paces the diagnostics surfaces' own-rect refresh (the F8
// Debug panel, the Settings debug overlay): 4 Hz keeps their tenth-of-a-
// second readouts (packet ages, "last pkt Ns") visibly live at the cost of
// one panel-sized clipped repaint per tick. Deliberately NOT the continuous
// full-frame tier the first compositor build used — holding full-rate walks
// while a diagnostics view is up is the observer effect that made every
// test11 measurement read "same as before" (and the F8 panel, which had NO
// census at all, froze until a hover crossing repainted it).
const diagTickBudget = 250 * time.Millisecond

// damageAll marks the whole frame stale — the next walk repaints everything.
func (a *App) damageAll() { a.dmgFull = true }

// DamageAll is damageAll for the main loop (canvas recreation on resize,
// RENDER_TARGETS_RESET/DEVICE_RESET, UI-scale changes).
func (a *App) DamageAll() { a.damageAll() }

// damageRect adds one stale region (logical coords). Empty rects are layout
// signals ("nothing drawn there"), not damage; overflow promotes to full.
func (a *App) damageRect(r sdl.Rect) {
	if r.W <= 0 || r.H <= 0 || a.dmgFull {
		return
	}
	if a.dmgCount >= dmgRectCap {
		a.dmgFull = true
		return
	}
	a.dmgRects[a.dmgCount] = r
	a.dmgCount++
}

// damageCeremony marks the three regions a playing message touches: the
// stage (talking sprites, shout bubbles), the chatbox (the typewriter's
// crawl), and the log column (the live line mirrors there). Their rects are
// wherever those regions last drew — layout moves arrive via input, which is
// full damage anyway.
func (a *App) damageCeremony() {
	a.damageRect(a.drawnVPRect)
	a.damageRect(a.drawnChatRect)
	a.damageRect(a.drawnLogRect)
}

// TakeDamage consumes the accumulator and reports the walk's clip: the union
// of the damaged rects bounded to the logical frame, or clipped=false for a
// full-frame walk. One union, one pass — never multiple clipped passes,
// because immediate-mode widget logic may only run once per frame.
func (a *App) TakeDamage(lw, lh int32) (clip sdl.Rect, clipped bool) {
	full, n := a.dmgFull, a.dmgCount
	a.dmgFull, a.dmgCount = false, 0
	if a.dmgOvOn {
		// The damage X-ray records the PRE-union list (per-rect colors) —
		// off, this whole feature is one bool compare on the walk path.
		a.recordDamageOverlay(full, n)
	}
	if full || n == 0 {
		// n == 0 on a walk means a source forgot its region — repaint
		// everything rather than nothing.
		return sdl.Rect{}, false
	}
	u := a.dmgRects[0]
	for i := 1; i < n; i++ {
		u = unionRect(u, a.dmgRects[i]) // screens.go's zero-area-safe union
	}
	frame := sdl.Rect{X: 0, Y: 0, W: lw, H: lh}
	if eff, ok := u.Intersect(&frame); ok {
		if a.dmgOvOn {
			a.dmgOvClip = eff
		}
		return eff, true
	}
	// Damage entirely off the logical frame (a shrink mid-accumulation):
	// the walk still runs its logic, paints nothing.
	if a.dmgOvOn {
		a.dmgOvClip = sdl.Rect{}
	}
	return sdl.Rect{}, true
}

// SetFrameClip mirrors the walk's damage clip to everything that owns raw
// scissor state: the kit (layout clips intersect with it) and both stage
// viewports (their sprite masks must treat it as a pixel bound, not layout).
func (a *App) SetFrameClip(r *sdl.Rect) {
	a.ctx.SetFrameClip(r)
	if a.d.Viewport != nil {
		a.d.Viewport.SetFrameClip(r)
	}
	if a.splitVP != nil {
		a.splitVP.SetFrameClip(r)
	}
}

// noteVPRect records where the stage drew this walk (renderViewportZoomed —
// the classic, themed and theater paths all pass through it): the damage
// region for anim-flip redraws.
func (a *App) noteVPRect(vp sdl.Rect) { a.drawnVPRect = vp }

// noteLogRect accumulates where live-log surfaces drew this walk (the clean
// right column, the tabbed log panel — a frame can draw more than one), so
// the ceremony damage covers every mirror of the typing line. Frame resets
// it each walk; unions across one walk (zero-area contributes nothing).
func (a *App) noteLogRect(r sdl.Rect) {
	a.drawnLogRect = unionRect(a.drawnLogRect, r)
}

// WalkNeeded decides whether this pass needs a UI walk, accumulating the
// damage region as it goes. sawInput is real user input this pass (clicks,
// keys, wheel, text, drops — window housekeeping and bare motion excluded;
// motion goes through the hover census instead). Called once per pass by the
// compositor loop, render thread only.
func (a *App) WalkNeeded(focused, sawInput bool) bool {
	// Damage accumulated OUTSIDE this decision — the loop's DamageAll on a
	// canvas rebuild / device reset / UI-scale change, or a pass whose walk
	// couldn't run — is already walk-worthy on its own.
	walk := a.dmgFull || a.dmgCount > 0

	// Discrete, whole-frame sources. Input can change anything (a click
	// opens a menu); uiDirty is packet/deadline damage with no region
	// attribution; texture traffic repaints whoever was showing a
	// placeholder (region attribution for uploads is a future refinement —
	// the win today is that a QUIET screen no longer pays for them).
	if sawInput {
		a.damageAll()
		walk = true
	}
	if a.uiDirty {
		a.damageAll()
		walk = true
	}
	if a.d.Store != nil && a.d.Store.Generation() != a.drawnGen {
		a.damageAll()
		walk = true
	}

	// Async results already buffered for their Frame-driven polls: work
	// another goroutine finished while the screen was static (a found
	// update, a loaded log scope, a landed font). Without this they'd sit
	// until unrelated damage — the polls only run inside Frame.
	if a.pendingPollResults() {
		a.damageAll()
		walk = true
	}

	// Continuously-animating surfaces walk at the active cap: replays, the
	// maker, exports, voice (its engine pump is Frame-driven), the perf
	// HUD, always-on FX, the sprite preview box, animated theme chrome,
	// warning toasts, an active download (progress chip + queue advance),
	// and every screen the static skip never covered (Settings' live
	// meters, handshake spinners, lobby sweeps). drawnContinuous — the
	// snapshot of this decision at the LAST walk — buys every one of those
	// surfaces exactly one more full walk after it ends on its own (a
	// replay finishing, a toast expiring): the walk that ERASES it. Without
	// that, the last-drawn overlay would sit on the canvas forever.
	full := paceBudget(a.d.Prefs.FPSCap())
	if (a.compositorContinuous() || a.drawnContinuous) && a.walkBudgetDue(full) {
		a.damageAll()
		walk = true
	}

	// A playing message walks at the talk cadence (the blip-exact tier —
	// room state itself advances every pass regardless), touching only the
	// regions a ceremony draws. Screen shake and transmitted sprite motion
	// are in animatedSurfaces (full damage above); viewport ramps promote
	// to full in the census below.
	if a.roomBusy() && a.walkBudgetDue(a.talkBudget(full)) {
		a.damageCeremony()
		walk = true
	}

	// Room state that landed on a skipped pass — the linger expiring back
	// to idle (the chatbox must clear), a queue pop, a catch-up jump. The
	// room advances in Background on every pass, so a change here is real.
	if a.room != nil &&
		(a.room.Phase() != a.drawnRoomPhase ||
			a.room.QueueLen() != a.drawnRoomQueue ||
			a.room.Scene.VisibleRunes != a.drawnRoomShown) {
		a.damageCeremony()
		walk = true
	}

	// The stage census: AnimGen moves exactly when the scene visibly
	// changed (frame flips, textures streaming in, visibility flips, ramp
	// steps — viewport.go). Content-exact, no budget: a 2 s blinker walks
	// twice a second, a 20 fps loop walks at 20. Ramps (shout punch
	// inflates PAST the stage rect) and an unmasked stage (the clip-sprites
	// pref off lets offsets spill anywhere) promote to full.
	if a.d.Viewport != nil && a.d.Viewport.AnimGen() != a.drawnAnimGen {
		if a.d.Viewport.RampActive() || !a.d.Prefs.ClipSpritesToStageOn() {
			a.damageAll()
		} else {
			a.damageRect(a.drawnVPRect)
		}
		walk = true
	}

	// The caret blinked (BeginFrame flips it on schedule every pass): one
	// field-sized walk. The typed-0 frozen knobs deliberately don't apply
	// here — under the compositor a blink costs a field-sized fill twice a
	// second, not a frame rate.
	if on, fieldFocused := a.ctx.CaretVisible(); fieldFocused && on != a.drawnCaretOn {
		if r, ok := a.ctx.FocusFieldRect(); ok && r.W > 0 {
			a.damageRect(r)
		} else {
			a.damageAll()
		}
		walk = true
	}

	// Pointer motion through the hover census: dead-space motion is
	// provably changeless and walks NOTHING; a crossing damages exactly the
	// rects that react. BeginFrame reseeds the pointer from SDL every pass,
	// so this works whether the move arrived as an event or not.
	if a.hoverMotionDamage() {
		walk = true
	}

	// Hover reveals are due (a resting pointer generates no events): the
	// tooltip dwell or the sprite-preview dwell elapsed and nothing is
	// showing yet. Where they'll paint isn't known until they draw → full.
	if a.ctx.TooltipRevealDue() && !a.drawnTipShowing {
		a.damageAll()
		walk = true
	}
	if a.ctx.PreviewRevealDue() && a.previewBase == "" {
		a.damageAll()
		walk = true
	}

	// Ticking readouts: one walk per displayed second while a local alarm
	// or a visible server clock runs (also how the alarm's Frame-driven
	// due-fire keeps ringing on time). Chip rects vary per layout → full.
	if (a.timerActive() || a.serverTimersLive()) && a.walkBudgetDue(timerTickBudget) {
		a.damageAll()
		walk = true
	}

	// Diagnostics surfaces with live readouts but no damage source of their
	// own: the F8 Debug panel and the Settings debug overlay tick exactly
	// the rect they last drew, on their own clock (walkBudgetDue would reset
	// on every unrelated walk and starve the tick during ceremonies).
	// Open/close/drag/toggle all arrive as input or a Settings click (full
	// damage), so appear/erase/move walks are already covered. The F3 perf
	// HUD deliberately stays in the continuous tier instead
	// (animatedSurfaces): it graphs per-frame dt, so it must hold the walk
	// rate to have anything to show — F8 is the observer-safe view.
	if dbgOv := a.d.Prefs.DebugOverlayEnabled(); (a.showDebugPanel || dbgOv) && time.Since(a.diagTickAt) >= diagTickBudget {
		a.diagTickAt = time.Now()
		if a.showDebugPanel {
			a.damageRect(a.drawnDebugPanelRect)
		}
		if dbgOv {
			a.damageRect(a.drawnDebugOvRect)
		}
		walk = true
	}

	// The ping chip's bars read a transport-level RTT atomic (no packet, no
	// poll channel — invisible to every census above, so under the first
	// compositor build the chip held a stale tier): repaint the chip when
	// the tier it drew no longer matches. drawnPingRect is zero unless the
	// chip drew last walk, so the off/undrawn case is one compare.
	if a.drawnPingRect.W > 0 {
		rtt := a.pingRTT.Load()
		if bars, _ := pingQuality(int(rtt/int64(time.Millisecond)), rtt == 0); bars != a.drawnPingBars {
			a.damageRect(a.drawnPingRect)
			walk = true
		}
	}

	// A due auto-reconnect (lobby): pollAutoReconnect is Frame-driven, so
	// the retry needs a walk to fire at its scheduled moment.
	if !a.autoReconnectAt.IsZero() && a.screen == ScreenLobby && !a.now().Before(a.autoReconnectAt) {
		a.damageAll()
		walk = true
	}

	if walk {
		// Riders on every walk: the focused field (the canvas must match
		// drawnCaretOn, which Frame snapshots for the WHOLE frame — a clip
		// that excluded the field would strand stale caret pixels), and
		// the tooltip's last box (any walk may hide or move it).
		if r, ok := a.ctx.FocusFieldRect(); ok {
			a.damageRect(r)
		}
		if a.drawnTipShowing {
			a.damageRect(a.ctx.TipBoxRect())
		}
	}
	return walk
}

// compositorContinuous reports the surfaces that need walks at the full
// budget for as long as they're on screen — the compositor twin of the
// static skip's refusal list (SkipFrame), minus everything expressed as
// damage above.
func (a *App) compositorContinuous() bool {
	if !a.expScreenSkippable() {
		return true // Settings' live preview/mic meter, handshake, lobby sweeps, unlisted screens
	}
	if a.animatedSurfaces() {
		return true // replay/maker/export/voice UI, perf HUD, reactions, split pane, FX, weather, shake
	}
	if a.voiceJoined || a.micTest != nil {
		return true // the audio engine pumps from Frame — walks must keep coming
	}
	if a.previewBase != "" {
		return true // the sprite preview box animates at draw time
	}
	if a.drawnAnimChrome {
		return true // time-stepped art outside the viewport scheduler
	}
	if a.warnActive() {
		return true // warning banner (timed fade)
	}
	if a.dl.active {
		return true // download progress chip + the Frame-driven queue advance
	}
	// The F8 Debug panel and the Settings debug overlay are NOT here: they
	// tick their own drawn rects in WalkNeeded (diagTickBudget). Keeping
	// them in this tier held full-frame walks at the cap whenever
	// diagnostics were up — so every "did selective rendering help?"
	// measurement answered "no" by construction (test11's report).
	return false
}

// pendingPollResults reports async results already buffered on a channel
// whose poll runs inside Frame. len() on a channel is a legal cross-goroutine
// size hint: a result racing in just after a zero read lands next pass, one
// panel period later — the same latency as an input event. len(nil) is 0, so
// channels that lazily construct need no guards. Every Frame-driven poll
// channel belongs here; a new pollX with its own channel must join or its
// results stall on static screens until unrelated damage lands.
func (a *App) pendingPollResults() bool {
	if len(a.lobbyResult) > 0 || len(a.pingRes) > 0 || len(a.charINIres) > 0 ||
		len(a.previewEmoteRes) > 0 || len(a.manifestRes) > 0 || len(a.casingRes) > 0 ||
		len(a.fontRes) > 0 || len(a.emojiFontRes) > 0 || len(a.fallbackFontRes) > 0 ||
		len(a.cjkFontRes) > 0 || len(a.logBrowserRes) > 0 || len(a.notebookRes) > 0 ||
		len(a.updateRes) > 0 || len(a.updateApplyRes) > 0 || len(a.makerExportCh) > 0 ||
		len(a.gifResultCh) > 0 || len(a.charMetaRes) > 0 || len(a.iniRes) > 0 ||
		len(a.jukeRes) > 0 || len(a.jukeIORes) > 0 || len(a.bgPick.res) > 0 ||
		len(a.dl.progress) > 0 {
		return true
	}
	if a.themeRes.Load() != nil {
		return true // an off-thread theme apply landed (atomic mailbox, not a channel)
	}
	if a.d.Manager != nil {
		if th := a.d.Manager.Thumbs(); th != nil && len(th.Results()) > 0 {
			return true // finished low-q thumbnails wait for drainThumbs
		}
	}
	return false
}

// walkBudgetDue reports the pace budget elapsed since the last walk
// (lastFrameDrawn — Frame stamps it). A 0 budget means uncapped.
func (a *App) walkBudgetDue(b time.Duration) bool {
	return b <= 0 || time.Since(a.lastFrameDrawn) >= b
}

// hoverMotionDamage is the census decision for pointer motion: false (and no
// damage) when the move changes no pixels — the dead-space case that used to
// cost one full frame per motion event. Drags track the pointer anywhere and
// the cursor-anchored tooltip moves with it → full; otherwise exactly the
// crossed rects.
func (a *App) hoverMotionDamage() bool {
	c := a.ctx
	if c == nil || (c.mouseX == a.drawnMouseX && c.mouseY == a.drawnMouseY) {
		return false
	}
	if c.mouseDown {
		a.damageAll() // a drag (slider, selection, window move) redraws what it moves — unknowable region
		return true
	}
	if c.modalOn {
		// A modal owner (open dropdown list, the pickers) hit-tests with raw
		// pointIn — hovering() early-returns under a modal, so its rows never
		// enter the census. Motion while a modal is up redraws in full, like
		// the pre-compositor loop did.
		a.damageAll()
		return true
	}
	if a.drawnTipShowing {
		a.damageAll() // the tooltip is glued to the cursor: it moves (and repaints under itself)
		return true
	}
	var hits [dmgRectCap]sdl.Rect
	n, crossed, complete := c.HoverCrossings(a.drawnMouseX, a.drawnMouseY, c.mouseX, c.mouseY, hits[:])
	if !complete {
		a.damageAll() // census overflow (or too many rects flipped): conservative full
		return true
	}
	for i := 0; i < n; i++ {
		a.damageRect(hits[i])
	}
	return crossed
}

// AdvanceStage keeps the stage's animation clocks real-time-true on passes
// that SKIP the walk: Background advances the room, this advances the
// viewport, and each pass advances each exactly once (walk passes do both
// inside Frame; the loop calls this only on skips). The census bump a flip
// causes here is picked up by the NEXT pass's WalkNeeded — the draw lags one
// pass (≤ one vsync period), the clocks never lag at all, and no deadline
// compensation is needed anywhere. Mirrors Frame's drive switch: the GIF
// export, a replay and the scene maker own the shared viewport on their own
// surfaces (all of which force continuous walks, so skips never happen
// there — the guard is for the transition passes).
func (a *App) AdvanceStage(dt time.Duration) {
	if a.gifExporting || (a.replaying && a.replayRoom != nil) || a.makerOpen {
		return
	}
	if a.room == nil || a.d.Viewport == nil {
		return
	}
	a.d.Viewport.Update(&a.room.Scene, dt)
	// The split pane's viewport needs no advancing here: splitActive is an
	// animatedSurfaces state, so the split never coexists with a skip.
}

// NotePresent counts a Present call into the rolling one-second window (F8
// "presFps"). With the compositor on this should sit at the panel's refresh
// rate whatever drawnFps does — dense presents ARE the design.
func (a *App) NotePresent() {
	now := time.Now()
	a.presWindowCount++
	if a.presWindowStart.IsZero() || now.Sub(a.presWindowStart) >= time.Second {
		a.presFPS = a.presWindowCount
		a.presWindowCount = 0
		a.presWindowStart = now
	}
}
