package ui

import (
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// drawSettingsPowerUser is the advanced Settings card (its own left-sidebar tab): TLS validation,
// the Asset Origin header, character-folder casing, and image-format tuning — options that can
// BREAK connections or asset fetching if set wrong. The whole card hides behind a reveal button and
// its warnings are red. Kept out of Account / Assets so everyday settings stay uncluttered.
func (a *App) drawSettingsPowerUser(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	// Diagnostics — the Debug panel only READS state, so it sits ABOVE the
	// dangerous reveal gate below. Opening it returns to the courtroom, where the
	// floating panel draws. Also on F8 anywhere, and Extras → Debug.
	y = a.settingsSection(y, w, "Diagnostics")
	if c.Button(sdl.Rect{X: pad, Y: y, W: 260, H: btnH}, "Open Debug panel (F8)") {
		a.debugPanel.open = true
		a.screen = a.prevScreen // back to the courtroom, where the panel is drawn
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "Server software, live packet inspector, performance (frame graph, heap vs budget, GC), the three-tier asset cache, and the failure log — one floating panel. Also opens with F8 anywhere, or Extras → Debug.", ColTextDim)
	y += 10

	y = a.settingsSection(y, w, "Power user (advanced)")
	y = a.settingsDesc(pad, y, "⚠ Everything here is for people who KNOW what their server needs. The wrong value can make characters fetch 0 assets or stop you connecting. If unsure, leave it all at the defaults.", ColDanger)
	y += 4
	if c.Button(sdl.Rect{X: pad, Y: y, W: 270, H: btnH}, powerUserToggleLabel(a.lookUI.showPowerUser)) {
		a.lookUI.showPowerUser = !a.lookUI.showPowerUser
		a.lookUI.powerNukeArm = false // never carry an armed nuke across a hide/reveal
	}
	y += btnH + 8
	if !a.lookUI.showPowerUser {
		return y
	}

	// The nuke: reset EVERY option on this tab to its shipped default (two-click
	// confirm — arming flips the label). Scoped to this tab; the image-format block
	// keeps its own controls (its learned state is per-server, not a "knob").
	nukeLabel := "⟲ Reset ALL power-user options to defaults"
	if a.lookUI.powerNukeArm {
		nukeLabel = "⚠ Click again to confirm the reset"
	}
	if c.Button(sdl.Rect{X: pad, Y: y, W: 340, H: btnH}, nukeLabel) {
		if a.lookUI.powerNukeArm {
			a.d.Prefs.ResetPowerUser()
			a.d.Manager.SetAssetOrigin("") // the live network override too, not just the pref
			a.rebuildAssetOrigin()         // re-derive the URL builder (character casing reset)
			a.applyTimingToRoom()          // push the reset renderer/timing knobs to the live room
			if th := a.d.Manager.Thumbs(); th != nil {
				th.SetEnabled(false) // the thumbnail store follows its reset pref (stored thumbs are kept — Clear is separate)
				th.SetParams(0, 0)
				th.SetBudget(0)
			}
			a.d.Manager.SetAdaptiveLatencyMultiple(0) // back to the ×8 default, live
			a.applySpriteCap()                        // re-derive the downscale cap from defaults
			a.warnLine = "Power-user options reset to defaults."
			a.warnAt = a.now()
		}
		a.lookUI.powerNukeArm = !a.lookUI.powerNukeArm
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "Puts every option on this tab back to its default. Image-format probing keeps its own controls below; saved presets and mod chips are untouched.", ColTextDim)
	y += 10

	// Update channel: stable releases vs the experimental test-branch feed.
	y = a.settingsSection(y, w, "Update channel")
	expCh := a.d.Prefs.Load().UpdateExperimental
	if next := c.Checkbox(pad, y, "Get experimental test builds (the MayAO-Test branch) instead of stable releases", expCh); next != expCh {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.UpdateExperimental = next })
		// Re-arm the one-shot launch check so the swap takes effect NOW (next
		// frame), not on the next restart — and drop a stale offer from the
		// other channel so its modal can't push the wrong build.
		a.updates.resetForChannel()
	}
	y += 26
	y = a.settingsDesc(pad, y, "OFF (default): the launch check follows stable releases and only ever moves forward. ON: it follows the newest published build INCLUDING prereleases cut from the test branch — riskier, less tested, and it may offer a lower version than you run (that's also how you get back: turn this off and take the next stable offer). Toggling re-checks immediately.", ColTextDim)
	y += 10

	// Message sequencing — how each IC message is presented while its assets are
	// still streaming + decoding. Four named modes; Default (strict sequencing) is
	// the shipped default. Fully isolated (it can't break a connection or an asset
	// fetch), but it touches the render/message path, so it sits behind the reveal
	// with the rest of the advanced kit.
	y = a.settingsSection(y, w, "Message sequencing")
	slm := a.d.Prefs.Load().SpriteLoadModeVal
	if c.Button(sdl.Rect{X: pad, Y: y, W: 520, H: btnH}, spriteLoadModeLabel(slm)) {
		a.d.Prefs.SetSpriteLoadMode((slm + 1) % config.SpriteLoadModeCount)
		a.applyTimingToRoom() // the wait gate lives on the live room — flip it now
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, spriteLoadModeDesc(slm), ColTextDim)
	y += 6
	if slm == config.SpriteLoadWait {
		c.Label(pad, y+4, "Max hold per message:", ColText)
		waitMs := a.d.Prefs.Load().SpriteWaitMs()
		wtrack := sdl.Rect{X: pad + 170, Y: y + 2, W: 260, H: 16}
		nw := int(clampI32(c.Slider("spritewaitms", wtrack, int32(waitMs), config.SpriteWaitMaxMs), config.SpriteWaitMinMs, config.SpriteWaitMaxMs))
		c.Tooltip(wtrack, "How long one message may wait for its sprite before playing anyway (50 ms – 30 s).")
		c.Label(pad+170+266, y+4, formatWaitMs(nw), ColTextDim)
		if nw != waitMs {
			a.d.Prefs.SetSpriteWaitMs(nw)
			a.applyTimingToRoom()
		}
		y += 26
		wp := a.d.Prefs.Load().SpriteWaitPair
		if next := c.Checkbox(pad, y, "Also wait for the pair partner's sprite", wp); next != wp {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteWaitPair = next })
			a.applyTimingToRoom()
		}
		y += 24
		wpre := a.d.Prefs.Load().SpriteWaitPreanim
		if next := c.Checkbox(pad, y, "Also wait for the pre-animation", wpre); next != wpre {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteWaitPreanim = next })
			a.applyTimingToRoom()
		}
		y += 26
	}
	if slm != config.SpriteLoadBlank { // hold-previous applies in Hold mode AND as Wait's timeout fallback
		c.Label(pad, y+4, "Keep the previous sprite for:", ColText)
		ageMs := a.d.Prefs.Load().HoldPrevMaxAgeMsVal
		atrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
		na := int(clampI32(c.Slider("holdmaxage", atrack, int32(ageMs), config.HoldPrevMaxAgeMaxMs), 0, config.HoldPrevMaxAgeMaxMs))
		if na != 0 && na < config.HoldPrevMaxAgeMinMs {
			na = 0 // the bottom of the track snaps to "forever" (0)
		}
		c.Tooltip(atrack, "How long the stand-in may bridge a still-loading sprite before giving up to blank. Far left = forever.")
		ageLabel := "forever"
		if na != 0 {
			ageLabel = formatWaitMs(na)
		}
		c.Label(pad+200+236, y+4, ageLabel, ColTextDim)
		if na != ageMs {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					HoldPrevMaxAgeMsVal =

					na

			})

		}
		y += 26
		tint := a.d.Prefs.Load().HoldDebugTint
		if next := c.Checkbox(pad, y, "Tint stand-in sprites amber (diagnostic: shows when a stand-in is bridging a load)", tint); next != tint {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					HoldDebugTint =

					next

			})

		}
		y += 26
	}
	c.Label(pad, y+4, "Speaker-swap crossfade:", ColText)
	cfMs := a.d.Prefs.Load().CrossfadeMsVal
	cftrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	ncf := int(clampI32(c.Slider("crossfadems", cftrack, int32(cfMs), config.CrossfadeMaxMs), 0, config.CrossfadeMaxMs))
	if ncf != 0 && ncf < config.CrossfadeMinMs {
		ncf = 0 // bottom of the track = off (the default hard swap)
	}
	c.Tooltip(cftrack, "Blend a sprite change: the new sprite fades in over this long instead of snapping (0 = off). Suppressed by Reduce motion.")
	cfLabel := "off (hard swap)"
	if ncf != 0 {
		cfLabel = formatWaitMs(ncf)
	}
	c.Label(pad+200+236, y+4, cfLabel, ColTextDim)
	if ncf != cfMs {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CrossfadeMsVal =

				ncf

		})

	}
	y += 26
	y = a.settingsDesc(pad, y, "Fades the new sprite in over the old on every character/emote change instead of hard-swapping. Local only; a still-loading sprite starts its fade when it arrives. With a crossfade set, the idle frame-rate cap stands down so fades stay smooth.", ColTextDim)
	y += 10

	// Shouts — AO2's default is that an interjection (Hold it!/Objection!/…) skips
	// the queue and plays immediately. The toggle off queues shouts like any other
	// message, so under the Default mode they wait for their bubble art + cry too.
	y = a.settingsSection(y, w, "Shouts")
	shoutBypass := a.d.Prefs.Load().ShoutBypassQueue
	if next := c.Checkbox(pad, y, "Shouts skip the queue and play immediately (AO2 default)", shoutBypass); next != shoutBypass {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShoutBypassQueue = next })
		a.applyTimingToRoom()
	}
	y += 26
	y = a.settingsDesc(pad, y, "ON (default): a shout clears the backlog and plays at once, exactly like AO2. OFF: shouts queue in order and, under the Default loading mode, wait for their bubble art and cry like any other message.", ColTextDim)
	y += 10

	// (The missing-sprite "missingno" placeholder controls are on Settings → Assets.)

	// Frame rate & GPU — the adaptive pacing that fixed the idle GPU burn.
	y = a.settingsSection(y, w, "Frame rate & GPU")
	y = a.settingsDesc(pad, y, "AsyncAO renders adaptively: the active rate while you interact or anything animates, the idle rate when nothing needs redrawing, the unfocused rate when another window has focus (minimized draws nothing). Any change returns to the active rate instantly. Each rate takes a typed exact number; 0 on Idle/Unfocused means never redraw in that state (0 GPU when nothing moves), and ∞ removes the cap — for Active that means vsync paces the frames.", ColTextDim)
	y += 6
	y = a.fpsSettingRow(y, "fpscap", "Active frame rate:",
		config.FPSCapMin, config.FPSCapMax, config.FPSCapUnlimitedOff, config.FPSCapMin,
		func() int { return a.d.Prefs.Load().FPSCap() }, func(v int) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.FPSCapVal = v
			})
		}, &settings.fpsBufs[0],
		"The ceiling while interacting or animating (1–240, type an exact number, or ∞ = uncapped/vsync). Default is ∞ (vsync); turning ∞ off drops to 60, plenty for AO.")
	y = a.fpsSettingRow(y, "idlefps", "Idle frame rate:",
		config.IdleFPSMin, config.IdleFPSMax, config.IdleFPSDefault, config.FPSOff,
		func() int { return a.d.Prefs.Load().IdleFPS() }, func(v int) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.IdleFPSVal = v
			})
		}, &settings.fpsBufs[1],
		"The rate when nothing needs redrawing (1–120, 0 = never redraw when idle, or ∞ = uncapped). Any change returns to the active rate instantly.")
	y = a.fpsSettingRow(y, "unfocusedfps", "Unfocused frame rate:",
		config.UnfocusedFPSMin, config.UnfocusedFPSMax, config.UnfocusedFPSDefault, config.FPSOff,
		func() int { return a.d.Prefs.Load().UnfocusedFPS() }, func(v int) {
			a.d.Prefs.Update(func(sn *config.Settings) {
				sn.UnfocusedFPSVal = v
			})
		}, &settings.fpsBufs[2],
		"The rate while another window has focus (1–60, 0 = never redraw when unfocused, or ∞ = uncapped). Minimized draws nothing.")
	// The "ignore the background rate entirely" escape hatch (pacingfocus.go). An
	// OPTION, never a default change: it overrides the focus boolean the whole pacer
	// branches on, so the background rate above, the static park and the wake
	// schedule all stop applying at once.
	ufr := a.d.Prefs.Load().UnfocusedFullRate
	if next := c.Checkbox(pad, y, "Keep full speed when another window has focus (OFF by default)", ufr); next != ufr {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.UnfocusedFullRate = next })
	}
	y += 26
	y = a.settingsDesc(pad, y, "On, the client paces an unfocused window exactly like a focused one — the Unfocused frame rate above stops applying, including its 0 = never-redraw setting. For a second monitor, or for capture software recording AsyncAO while you work in another app. Costs power: leave it off unless you can see the window.", ColTextDim)
	y += 6
	// Post-input full-rate hold, in frames — a slider (the playtest ask). Default
	// 1 frame: the input's own frame shows, then the rate drops straight to idle.
	c.Label(pad, y+4, "Input responsiveness:", ColText)
	ig := a.d.Prefs.Load().InputGraceFrames()
	igtrack := sdl.Rect{X: pad + 210, Y: y + 2, W: 220, H: 16}
	nig := int(clampI32(c.Slider("inputgrace", igtrack, int32(ig), config.InputGraceFramesMax), 0, config.InputGraceFramesMax))
	c.Tooltip(igtrack, "How long the frame rate stays at the active cap after a click or keypress, in frames (0–60). 0 = off (just the input's own frame), 1 = snappiest, higher = a smoother tail. Mouse movement isn't affected.")
	igLabel := strconv.Itoa(nig) + " frame"
	if nig != 1 {
		igLabel += "s"
	}
	if nig == 0 {
		igLabel = "off"
	}
	c.Label(pad+210+226, y+4, igLabel, ColTextDim)
	if nig != ig {
		a.d.Prefs.SetInputGraceFrames(nig)
	}
	y += 26
	y = a.settingsDesc(pad, y, "After a click or keypress the client holds the active rate for this many frames, then falls back to the idle rate. 0 turns the hold off entirely; 1 (the default) is nearly as snappy — the input still shows instantly either way, it just doesn't keep the frame rate up afterward. Mouse movement has its own brief hold and isn't affected.", ColTextDim)
	y += 6
	y = a.settingsDesc(pad, y, "\"Animating\" = messages typing or queued, shouts/preanims, shakes/flashes, replays, the Scene Maker, exports, voice, reactions, toasts, the pinned second courtroom, the F3 graph, and any always-moving effect you enable. Sprite loops and theme art run at their own ≤15 fps timings, so the idle default never visibly slows them; raise it if something looks choppy while idle.", ColTextDim)
	y += 10
	edl := a.d.Prefs.Load().EventDrivenLoop
	if next := c.Checkbox(pad, y, "Event-driven renderer (EXPERIMENTAL, default ON on this test build)", edl); next != edl {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				EventDrivenLoop =

				next

		})

	}
	y += 26
	y = a.settingsDesc(pad, y, "Static screens stop redrawing entirely: input wakes the renderer instantly, incoming messages and finished loads push their own wake, and a blinking caret or ticking clock redraws one frame exactly when it's due. Sounds and the connection never slow down — only wasted redraws go. Applies live. If anything looks frozen or choppy, turn this off (the classic pacing loop) and report what it was.", ColTextDim)
	y += 10
	mrp := a.d.Prefs.Load().MotionRedrawPerEvent
	if next := c.Checkbox(pad, y, "Redraw once per mouse-move event (less power on cursor motion)", mrp); next != mrp {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MotionRedrawPerEvent =

				next

		})

	}
	y += 26
	y = a.settingsDesc(pad, y, "Event-driven renderer only. On (default): each mouse-move event draws a single frame and the renderer parks again — the cursor still tracks, but sweeping it over static UI stops burning frames and power. Off: moving the mouse holds the full frame rate for a moment, so a hover sweep renders at your active cap. Clicks, keys and the scroll wheel keep their normal full-rate response.", ColTextDim)
	y += 10
	nolimit := a.d.Prefs.Load().DisableFrameLimiter
	if next := c.Checkbox(pad, y, "Disable the frame limiter entirely (render every frame — vsync only, HIGH GPU)", nolimit); next != nolimit {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				DisableFrameLimiter =

				next

		})

	}
	y += 26
	y = a.settingsDesc(pad, y, "Off by default. On = no idle skipping and no pacing at all: the client redraws continuously, paced only by vsync (or a 60 fps floor with -vsync=false). The opposite of everything above — it trades GPU for the last drop of responsiveness. Leave it off unless you have a specific reason.", ColTextDim)
	y += 10

	// Sprite thumbnail cache — the persistent low-q stand-in store.
	y = a.settingsSection(y, w, "Sprite thumbnail cache")
	th := a.d.Manager.Thumbs()
	if th == nil {
		y = a.settingsDesc(pad, y, "Unavailable in this build (the thumbnail store could not open).", ColTextDim)
		y += 10
	} else {
		tcOn := a.d.Prefs.Load().ThumbCache
		if next := c.Checkbox(pad, y, "Keep tiny low-quality thumbnails of every sprite (default OFF)", tcOn); next != tcOn {
			a.d.Prefs.Update(func(sn *config.Settings) { sn.ThumbCache = next })
			th.SetEnabled(next)
		}
		y += 26
		y = a.settingsDesc(pad, y, "Every sprite that finishes loading leaves a ~1 KB low-quality thumbnail on disk (its own folder, kept after the full sprite is evicted). The next cold load shows the thumbnail instantly while the real one streams, then swaps — covering the case where there's no previous sprite to hold. Costs a little disk and a moment of visibly crunchy art.", ColTextDim)
		y += 6
		if a.d.Prefs.Load().ThumbCache {
			c.Label(pad, y+4, "Thumbnail height:", ColText)
			tHp := a.d.Prefs.Load().ThumbHeightPx()
			ttrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
			nth := int(clampI32(c.Slider("thumbheight", ttrack, int32(tHp), config.ThumbHeightMaxPx), 0, config.ThumbHeightMaxPx))
			if nth != 0 && nth < config.ThumbHeightMinPx {
				nth = 0 // bottom of the track = the default (64 px)
			}
			c.Tooltip(ttrack, "How tall a stored thumbnail is (32–160 px). Bigger = sharper stand-in, bigger files. Applies to NEWLY stored thumbnails.")
			thLabel := "default (64 px)"
			if nth != 0 {
				thLabel = strconv.Itoa(nth) + " px"
			}
			c.Label(pad+200+236, y+4, thLabel, ColTextDim)
			if nth != tHp {
				a.d.Prefs.Update(func(sn *config.Settings) { sn.ThumbHeightPxVal = nth })
				th.SetParams(a.d.Prefs.Load().ThumbHeightPx(), a.d.Prefs.Load().ThumbQuality())
			}
			y += 26
			c.Label(pad, y+4, "Thumbnail quality:", ColText)
			tq := a.d.Prefs.Load().ThumbQuality()
			qtrack2 := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
			ntq := int(clampI32(c.Slider("thumbquality", qtrack2, int32(tq), config.ThumbQualityMax), 0, config.ThumbQualityMax))
			if ntq != 0 && ntq < config.ThumbQualityMin {
				ntq = 0
			}
			c.Tooltip(qtrack2, "WebP quality of a stored thumbnail (5–60). Lower = smaller + crunchier; the default 20 lands ~1 KB.")
			tqLabel := "default (20)"
			if ntq != 0 {
				tqLabel = strconv.Itoa(ntq)
			}
			c.Label(pad+200+236, y+4, tqLabel, ColTextDim)
			if ntq != tq {
				a.d.Prefs.Update(func(sn *config.Settings) { sn.ThumbQualityVal = ntq })
				th.SetParams(a.d.Prefs.Load().ThumbHeightPx(), a.d.Prefs.Load().ThumbQuality())
			}
			y += 26
			c.Label(pad, y+4, "Thumbnail store budget:", ColText)
			tb := a.d.Prefs.Load().ThumbBudgetMiB()
			btrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
			ntb := int(clampI32(c.Slider("thumbbudget", btrack, int32(tb), config.ThumbBudgetMaxMiB), 0, config.ThumbBudgetMaxMiB))
			if ntb != 0 && ntb < config.ThumbBudgetMinMiB {
				ntb = 0 // bottom of the track = the default (64 MiB)
			}
			c.Tooltip(btrack, "Hard cap on the thumbnail folder (8–512 MiB): past it the OLDEST thumbnails auto-delete. 64 MiB ≈ ~60,000 thumbnails.")
			tbLabel := "default (64 MiB)"
			if ntb != 0 {
				tbLabel = strconv.Itoa(ntb) + " MiB"
			}
			c.Label(pad+200+236, y+4, tbLabel, ColTextDim)
			if ntb != tb {
				a.d.Prefs.Update(func(sn *config.Settings) { sn.ThumbBudgetMiBVal = ntb })
				th.SetBudget(int64(a.d.Prefs.Load().ThumbBudgetMiB()) << 20)
			}
			y += 26
			if c.Button(sdl.Rect{X: pad, Y: y, W: 200, H: btnH}, "Clear stored thumbnails") {
				if err := th.Clear(); err != nil {
					a.warnLine = clampLine("Clear thumbnails: " + err.Error())
				} else {
					a.warnLine = "Stored thumbnails cleared."
				}
				a.warnAt = a.now()
			}
			y += btnH + 6
		}
		y += 10
	}

	// Core message timings — the courtroom ceremony's hard-coded spans, exposed.
	y = a.settingsSection(y, w, "Core message timings")
	c.Label(pad, y+4, "Shout bubble duration:", ColText)
	shoutMs := a.d.Prefs.Load().ShoutDurationMsVal
	strack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	ns := int(clampI32(c.Slider("shoutdurms", strack, int32(shoutMs), config.ShoutDurationMaxMs), 0, config.ShoutDurationMaxMs))
	if ns != 0 && ns < config.ShoutDurationMinMs {
		ns = 0 // bottom of the track = the canonical default
	}
	c.Tooltip(strack, "How long an Objection!/Hold It!/Take That! bubble holds the stage. Far left = the canonical AO2 timing (~0.72 s).")
	shoutLabel := "default (0.72 s)"
	if ns != 0 {
		shoutLabel = formatWaitMs(ns)
	}
	c.Label(pad+200+236, y+4, shoutLabel, ColTextDim)
	if ns != shoutMs {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ShoutDurationMsVal = ns })
		a.applyTimingToRoom()
	}
	y += 26
	c.Label(pad, y+4, "Pre-animation wait cap:", ColText)
	preMs := a.d.Prefs.Load().PreanimTimeoutMsVal
	ptrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	np := int(clampI32(c.Slider("preanimms", ptrack, int32(preMs), config.PreanimTimeoutMaxMs), 0, config.PreanimTimeoutMaxMs))
	if np != 0 && np < config.PreanimTimeoutMinMs {
		np = 0
	}
	c.Tooltip(ptrack, "How long a pre-animation may play before the text starts anyway when its real length is unknown (still decoding). Far left = the canonical default (2.5 s).")
	preLabel := "default (2.5 s)"
	if np != 0 {
		preLabel = formatWaitMs(np)
	}
	c.Label(pad+200+236, y+4, preLabel, ColTextDim)
	if np != preMs {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PreanimTimeoutMsVal = np })
		a.applyTimingToRoom()
	}
	y += 26
	c.Label(pad, y+4, "IC backlog queue depth:", ColText)
	qcap := a.d.Prefs.Load().ICQueueCapVal
	qtrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	nq := int(clampI32(c.Slider("icqueuecap", qtrack, int32(qcap), config.ICQueueCapMax), 0, config.ICQueueCapMax))
	if nq != 0 && nq < config.ICQueueCapMin {
		nq = 0 // bottom of the track = the canonical default (64)
	}
	c.Tooltip(qtrack, "How many unplayed messages a packed room may stack before the OLDEST drops (the IC log records everything regardless). Far left = the default (64).")
	qLabel := "default (64)"
	if nq != 0 {
		qLabel = strconv.Itoa(nq) + " messages"
	}
	c.Label(pad+200+236, y+4, qLabel, ColTextDim)
	if nq != qcap {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ICQueueCapVal =

				nq

		})

		a.applyTimingToRoom()
	}
	y += 26
	c.Label(pad, y+4, "Catch-up flash linger:", ColText)
	cul := a.d.Prefs.Load().CatchUpLingerMsVal
	ctrack := sdl.Rect{X: pad + 200, Y: y + 2, W: 230, H: 16}
	nc := int(clampI32(c.Slider("catchuplinger", ctrack, int32(cul), config.CatchUpLingerMaxMs), 0, config.CatchUpLingerMaxMs))
	c.Tooltip(ctrack, "How long each fast-forwarded backlog message stays up while catch-up drains a pile-up. 0 (default) = one per frame — fastest; a little linger makes the backlog readable as it flashes past.")
	culLabel := "default (one per frame)"
	if nc != 0 {
		culLabel = formatWaitMs(nc)
	}
	c.Label(pad+200+236, y+4, culLabel, ColTextDim)
	if nc != cul {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CatchUpLingerMsVal =

				nc

		})

		a.applyTimingToRoom()
	}
	y += 26
	y = a.settingsDesc(pad, y, "Shout and preanim pace the message ceremony; queue depth bounds how much backlog is kept; catch-up linger holds each fast-forwarded backlog message a touch longer. Far left on every slider = the canonical AO2 default.", ColTextDim)
	y += 10

	// Viewport sprite mask — confine offset sprites to the stage.
	y = a.settingsSection(y, w, "Viewport sprite mask")
	clipS := a.d.Prefs.Load().ClipSpritesToStage
	if next := c.Checkbox(pad, y, "Clip sprites to the stage (default ON)", clipS); next != clipS {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				ClipSpritesToStage =

				next

		})

	}
	y += 24
	y = a.settingsDesc(pad, y, "Masks character sprites to the stage so a large pair/reposition offset can't spill over the chatbox or log. Off = free placement past the stage edges.", ColTextDim)
	y += 10

	// TLS certificate validation.
	y = a.settingsSection(y, w, "TLS certificate")
	validate := a.d.Prefs.Load().ValidateTLSCerts
	if next := c.Checkbox(pad, y, "Validate server certificates (wss) — OFF by default", validate); next != validate {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.ValidateTLSCerts = next })
	}
	y += 24
	y = a.settingsDesc(pad, y, "Strictly verify the TLS certificate when connecting over wss://. Most community AO servers use self-signed certs, so turning this ON can make them UNREACHABLE — it's only for confirming the encrypted connection is to the real server.", ColDanger)
	y += 10

	// Origin overrides (asset fetches + the WS handshake).
	y = a.settingsSection(y, w, "Origin overrides")
	c.Label(pad, y+4, "Assets Origin/Referer:", ColText)
	origin := a.d.Prefs.Load().AssetOrigin
	if next, _ := c.TextField("assetorigin", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, origin, "https://webao.example  (blank = off)"); next != origin {
		a.d.Prefs.SetAssetOriginHeader(next)
		a.d.Manager.SetAssetOrigin(next)
	}
	y += 24
	y = a.settingsDesc(pad, y, "Sends this Origin/Referer header on asset downloads, so a server that only serves its base to its own web client still streams to AsyncAO. Wrong value = assets stop loading — leave it blank unless you know you need it.", ColDanger)
	y += 8
	c.Label(pad, y+4, "Connection Origin:", ColText)
	wsOrigin := a.d.Prefs.Load().WSOrigin
	if next, _ := c.TextField("wsorigin", sdl.Rect{X: pad + 160, Y: y, W: 340, H: fieldH}, wsOrigin, "https://webao.example  (blank = off)"); next != wsOrigin {
		a.d.Prefs.SetWSOriginHeader(next)
	}
	y += 24
	y = a.settingsDesc(pad, y, "Sends this Origin header on the SERVER CONNECTION itself (the WebSocket handshake), for the rare server that only accepts its own web client — e.g. one that requires \"webao.<its domain>\". Applies on the next connect. Wrong value can get the connection refused — leave it blank unless a server demands it.", ColDanger)
	y += 10

	// Network tuning — the streaming pipeline's self-tuning bounds, exposed.
	y = a.settingsSection(y, w, "Network tuning")
	c.Label(pad, y+4, "Missing-asset (404) memory:", ColText)
	ttl := a.d.Prefs.Load().NotFoundTTLSecVal
	ntrack := sdl.Rect{X: pad + 210, Y: y + 2, W: 220, H: 16}
	nttl := int(clampI32(c.Slider("notfoundttl", ntrack, int32(ttl), config.NotFoundTTLMaxSec), 0, config.NotFoundTTLMaxSec))
	if nttl != 0 && nttl < config.NotFoundTTLMinSec {
		nttl = 0 // bottom of the track = the default (5 min)
	}
	c.Tooltip(ntrack, "How long a 404'd asset stays \"missing\" before AsyncAO will probe it again (30 s – 60 min). Applies on RESTART.")
	ttlLabel := "default (5 min)"
	if nttl != 0 {
		ttlLabel = formatWaitMs(nttl * 1000)
	}
	c.Label(pad+210+226, y+4, ttlLabel, ColTextDim)
	if nttl != ttl {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				NotFoundTTLSecVal =

				nttl

		})

	}
	y += 26
	y = a.settingsDesc(pad, y, "How long a missing (404) asset is remembered before it may be probed again. Shorter picks up newly-uploaded art sooner; longer wastes fewer probes on sparse packs. Applies on restart.", ColTextDim)
	y += 8
	c.Label(pad, y+4, "Slow-host patience:", ColText)
	lm := a.d.Prefs.Load().AdaptiveLatMultipleVal
	ltrack := sdl.Rect{X: pad + 210, Y: y + 2, W: 220, H: 16}
	nlm := int(clampI32(c.Slider("latmultiple", ltrack, int32(lm), config.AdaptiveLatMultipleMax), 0, config.AdaptiveLatMultipleMax))
	if nlm != 0 && nlm < config.AdaptiveLatMultipleMin {
		nlm = 0 // bottom of the track = the default (×8)
	}
	c.Tooltip(ltrack, "Each asset host's request deadline = THIS × its observed response-time average (2–32). Applies immediately.")
	lmLabel := "default (×8)"
	if nlm != 0 {
		lmLabel = "×" + strconv.Itoa(nlm)
	}
	c.Label(pad+210+226, y+4, lmLabel, ColTextDim)
	if nlm != lm {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AdaptiveLatMultipleVal =

				nlm

		})

		a.d.Manager.SetAdaptiveLatencyMultiple(a.d.Prefs.Load().AdaptiveLatMultipleVal)
	}
	y += 26
	y = a.settingsDesc(pad, y, "Each asset host's request deadline is N × its average response time, so one dying mirror can't stall the download lane. Lower gives up sooner (jittery links may see spurious timeouts); higher is more patient.", ColTextDim)
	y += 10

	// Decode downscale + texture memory — how big decoded art lives on the GPU.
	y = a.settingsSection(y, w, "Decode downscale & texture memory")
	dsOff := a.d.Prefs.Load().SpriteDownscaleOff
	if next := c.Checkbox(pad, y, "Disable the automatic sprite downscale entirely (keep EXACT source art)", dsOff); next != dsOff {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteDownscaleOff = next })
		a.applySpriteCap()
	}
	y += 24
	y = a.numSliderRow(y, "downscalepct", "Downscale target:", config.SpriteDownscaleMinPct, config.SpriteDownscaleMaxPct, 5, 100, func() int { return a.d.Prefs.Load().SpriteDownscalePctVal }, func(n int) {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.SpriteDownscalePctVal = n })
		a.applySpriteCap()
	}, &settings.memBufs[0], "%", "The decode-time downscale shrinks huge art toward your display height; this scales that target (50-200%). Applies to NEWLY loaded art.")
	y = a.settingsDesc(pad, y, "Art taller than your display is downscaled once at load (high quality) instead of every frame on the GPU. Lower % = smaller textures, less memory; higher % or the off-switch = more source detail for heavy zooming, at memory cost. Applies to newly loaded art.", ColTextDim)
	y += 8
	y = a.numSliderRow(y, "animspritecap", "Animated sprite height cap:", config.AnimatedSpriteCapMinPx, config.AnimatedSpriteCapMaxPx, 16, 0, func() int { return a.d.Prefs.Load().AnimatedSpriteCapPxVal }, func(n int) {
		a.d.Prefs.SetAnimatedSpriteCap(n)
		a.applySpriteCap()
	}, &settings.memBufs[1], " px", "Optional: downscale ANIMATED sprites harder than stills to save memory (default OFF). 0 = use the still cap; otherwise this caps animated sprite height in pixels. Applies to NEWLY loaded art.")
	y = a.settingsDesc(pad, y, "Animations are no longer truncated to fit the texture budget, so one full high-res preanimation can hold a few hundred MB of GPU memory. This caps animated sprite HEIGHT (still art is untouched) to shrink that footprint; leave off for AO2-identical full quality.", ColTextDim)
	y += 8
	y = a.numSliderRow(y, "animbudget", "Animated sprite memory:", config.AnimatedBudgetMinMiB, config.AnimatedBudgetMaxMiB, 32, config.AnimatedBudgetDefaultMiB, func() int { return a.d.Prefs.Load().AnimatedBudgetMiB() }, func(n int) {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AnimatedBudgetMiBVal =

				n

		})

		a.applySpriteCap()
	}, &settings.memBufs[2], " MiB", "How much decoded memory ONE animated sprite may use before its frames are downscaled to fit (64-512 MiB). 128 = the shipped default (very large clips downscale to fit); lower = softer sprites, far less RAM. Applies to NEWLY loaded art (LIVE).")
	y = a.settingsDesc(pad, y, "Decoded memory allowed for ONE animated sprite before its frames are downscaled to fit. 500 MiB keeps full-size high-res clips native (sharpest, most RAM); lower shrinks them to fit (softer, a fraction of the RAM). The per-frame decoded pixels of several such clips are what spiked RAM, so this is the biggest lever. Applies to newly loaded art.", ColTextDim)
	y += 8
	y = a.numSliderRow(y, "animdeccon", "Concurrent animated decodes:", config.AnimDecodeConcurrencyMin, config.AnimDecodeConcurrencyMax, 1, config.AnimDecodeConcurrencyDefault, func() int { return a.d.Prefs.Load().AnimDecodeConcurrency() }, func(n int) {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				AnimDecodeConcurrencyVal =

				n

		})

		a.applySpriteCap()
	}, &settings.memBufs[3], "", "How many long animations decode at once. The decoded pixels of a full clip are the RAM spike, so fewer = less peak memory (slower loads); more = faster loads (more RAM). Applies LIVE.")
	y = a.settingsDesc(pad, y, "How many full animations may decode at once. Each long clip holds every frame in memory until it uploads, so capping this flattens the memory spike while a character's animations stream in (at the cost of slightly slower loads). Applies live.", ColTextDim)
	y += 8
	y = a.numSliderRow(y, "texbudget", "Texture memory budget:", config.TexBudgetMinMiB, config.TexBudgetMaxMiB, 16, config.TexBudgetDefaultMiB, func() int { return a.d.Prefs.Load().TexBudgetMiB() }, func(v int) {
		a.d.Prefs.Update(func(sn *config.Settings) {
			sn.TexBudgetMiBVal = v
		})
	}, &settings.memBufs[4], " MiB", "How much GPU-texture memory decoded art may occupy before the least-recently-used is evicted (32-256 MiB). Above 128 is EXPERIMENTAL and may stutter or crash on low-RAM machines. Applies on RESTART.")
	y = a.settingsDesc(pad, y, "GPU memory for decoded art; past it the least-recently-used is evicted (and re-fetched if needed again). Bigger = fewer evictions in packed rooms AND longer animations decode before they truncate; smaller = lighter footprint. The default (64) and anything up to 128 fit the 256 MiB memory budget — above 128 is EXPERIMENTAL and deliberately exceeds it, so only raise it there if long animations are cut and you have RAM to spare. Applies on restart.", ColTextDim)
	y += 10

	// Texture compression (restart-applied; lossy).
	y += 8
	c.Label(pad, y+4, "Texture compression:", ColText)
	txc := a.d.Prefs.TexCompression()
	next := txc
	switch txc {
	case config.TexCompressOff:
		next = config.TexCompressDXT5
	case config.TexCompressDXT5:
		next = config.TexCompressDXT1
	case config.TexCompressDXT1:
		next = config.TexCompressOff
	}
	if c.Button(sdl.Rect{X: pad + 210, Y: y, W: 220, H: btnH}, texCompressLabel(txc)) {
		a.d.Prefs.SetTexCompression(next)
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "How decoded art is stored on the GPU. Off = raw RGBA (lossless). DXT5 (BC3) = ~4x smaller with smooth alpha; DXT1 (BC1) = ~8x smaller but 1-bit alpha (jagged soft edges). Both are lossy 4x4-block formats, only apply to art whose width and height are multiples of 4, and only when your GPU supports them (otherwise it silently stays Off). Applies on RESTART.", ColTextDim)
	y += 10

	// Character-folder casing.
	y = a.settingsSection(y, w, "Character folder casing")
	cs := a.d.Prefs.Load().AssetCharCase
	if c.Button(sdl.Rect{X: pad, Y: y, W: 260, H: btnH}, charCaseLabel(cs)) {
		a.d.Prefs.SetAssetCharCasing((cs + 1) % courtroom.CharCaseCount)
		a.rebuildAssetOrigin() // apply the new casing to the URL builder immediately
	}
	y += btnH + 6
	y = a.settingsDesc(pad, y, "How the character FOLDER is capitalised in asset URLs. The VAST MAJORITY of servers are lowercase (the default) — CHECK YOUR SERVER FIRST: the wrong choice makes EVERY character fetch 0 assets. \"First cap\" = Phoenix wright · \"Title\" = Phoenix Wright. \"Auto-detect\" (OFF unless you pick it) probes one character per server once and learns the casing, staying on lowercase unless lowercase actually fails.", ColDanger)
	y += 10

	y = a.drawProxySettings(y, w)

	// Image formats have their own tab. They are
	// NOT power-user knobs: art failing to render is a first-week problem, and
	// burying its fix behind a tab called Power user is why it went unfound. A
	// pointer stays because the casing control above is the OTHER reason a server's
	// art fetches nothing, so someone who lands here is one click away.
	y = a.settingsSection(y, w, "Image & audio formats")
	y = a.settingsDesc(pad, y, "Which file types AsyncAO asks a server for — and the one-click fix for a server whose art isn't loading — live on their own tab now.", ColTextDim)
	y += 6
	y = a.formatsTabPointer(y)
	return y
}

func (a *App) drawSettingsAccount(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Login")
	// Auto-login: ITS OWN automation, not a macro — per-server creds,
	// software-detected wire flow, fires on join (or via hotkey/button).
	y = a.drawLoginSettings(y, w)
	y += 8

	y = a.settingsSection(y, w, "Master list")
	// Master list override (blank = official). Refresh in the lobby applies.
	c.Label(pad, y+4, "URL:", ColTextDim)
	master := a.d.Prefs.Load().MasterListURL
	if next, _ := c.TextField("masterurl", sdl.Rect{X: pad + 130, Y: y, W: 400, H: fieldH}, master, network.DefaultMasterServerURL); next != master {
		a.d.Prefs.SetMasterList(next)
	}
	y += 34

	// Discord Rich Presence: the whole section lives in a build-tagged file, so a
	// -tags nodiscord binary compiles it out entirely (no section, no code).
	y = a.drawDiscordSection(y, w)
	return y
}

// drawSettingsHotkeys: hotkey rebinds, macros, and the whole-settings bundle.
func (a *App) drawSettingsHotkeys(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	y = a.settingsSection(y, w, "Hotkeys")
	y = a.settingsDesc(pad, y, "Click a binding, then press a key (Ctrl + that key triggers it). Esc cancels · right-click resets to default.", ColTextDim)
	y += 8
	// Hotkeys are opt-in: every Ctrl+key chord below is OFF until this is ticked,
	// so a fresh install fires none of them (no accidental shout / FX / menu). The
	// bindings stay editable either way.
	hk := a.d.Prefs.Load().HotkeysEnabled
	if next := c.Checkbox(pad, y, "Enable hotkeys (Ctrl+key shortcuts) — OFF by default", hk); next != hk {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				HotkeysEnabled =

				next

		})

	}
	y += 28
	// Conflict scan: two actions resolving to the same key clash — only the first
	// in the dispatch switch fires — so flag both rather than fail silently.
	hkConflicts := a.hotkeyConflictKeys()
	hx := pad
	for _, def := range hotkeyDefs {
		c.LabelClipped(hx, y+4, 190, def.label, ColText)
		key := a.hotkeyFor(def.id)
		lbl := "Ctrl+" + strings.ToUpper(key)
		switch {
		case a.courtkeys.hkCapture == def.id:
			lbl = "press a key…"
		case key == "":
			lbl = "(unset)"
		}
		br := sdl.Rect{X: hx + 198, Y: y, W: 110, H: btnH}
		if c.Button(br, lbl) {
			a.courtkeys.hkCapture = def.id // arm capture for this action
			a.courtkeys.bindingFor = ""    // don't also arm a character keybind
			c.focusID = ""                 // the capture owns the next keypress
		}
		if c.rightClicked && c.hovering(br) {
			a.d.Prefs.SetHotkey(def.id, "") // reset to the built-in default
			if a.courtkeys.hkCapture == def.id {
				a.courtkeys.hkCapture = ""
			}
		}
		if a.courtkeys.hkCapture != def.id && hkConflicts[key] {
			c.Border(br, ColDanger) // clash: another action shares this key
			c.Tooltip(br, "Ctrl+"+strings.ToUpper(key)+" is bound to more than one action — only the first fires. Rebind one.")
		}
		hx += 340
		if hx > 560 {
			hx = pad
			y += 30
		}
	}
	if hx != pad {
		y += 30 // finish a partial row
	}
	a.consumeHotkeyCapture()
	y += 8
	if c.Button(sdl.Rect{X: pad, Y: y, W: 170, H: btnH}, "Reset all to defaults") {
		for _, def := range hotkeyDefs {
			a.d.Prefs.SetHotkey(def.id, "")
		}
		settings.statusLine = "All hotkeys reset to their defaults."
	}
	y += 36

	y = a.settingsSection(y, w, "Macros")
	// Macros: user-defined OOC command sequences with optional keybinds.
	y = a.drawMacroSettings(y, w)
	y += 8

	y = a.settingsSection(y, w, "IC quick-phrases")
	// Bind a key to a canned IC line your CHARACTER says (the IC counterpart to macros).
	y = a.drawICPhraseSettings(y, w)
	y += 8
	// (Export / Import / Open config are on the "Data" tab.)
	return y
}

// drawSettingsData is the dedicated "where's my stuff / back it up" tab — one place
// for the settings file, its folder, and export/import, so nobody hunts through
// %AppData% (the recurring confusion).
func (a *App) drawSettingsData(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()

	y = a.settingsSection(y, w, "Your settings file")
	y = a.settingsDesc(pad, y, "Every setting is ONE editable JSON file (asset_preferences.json). Close AsyncAO before hand-editing — it autosaves.", ColTextDim)
	y += 6
	c.LabelClipped(pad, y, w-pad, configDir(), ColAccent) // the actual path
	y += 24
	if config.ConfigIsPortable() {
		y = a.settingsDesc(pad, y, "Storage: PORTABLE — this folder sits next to AsyncAO, so it travels with a copied install / USB stick.", ColAccent)
	} else {
		y = a.settingsDesc(pad, y, "Storage: OS config folder. Use \"Make portable\" to keep settings beside AsyncAO (USB stick / copy).", ColTextDim)
	}
	y += 6
	if c.Button(sdl.Rect{X: pad, Y: y, W: 180, H: btnH}, "Open config folder") {
		openConfigFolder()
	}
	if c.Button(sdl.Rect{X: pad + 190, Y: y, W: 180, H: btnH}, "Open settings file") {
		openSettingsFile()
	}
	y += 38
	if !config.ConfigIsPortable() {
		if c.Button(sdl.Rect{X: pad, Y: y, W: 260, H: btnH}, "Make portable (copy beside AsyncAO)") {
			makePortableAsync(a.d)
		}
		c.Label(pad+270, y+6, "Copies settings next to the program; takes effect on restart.", ColTextDim)
		y += 38
	}

	y = a.settingsSection(y, w, "Back up / move to another PC")
	y = a.settingsDesc(pad, y, "Export everything (favourites, layout, hotkeys, wardrobes, learned formats — NOT passwords) to a portable JSON; import it elsewhere.", ColTextDim)
	y += 6
	if c.Button(sdl.Rect{X: pad, Y: y, W: 180, H: btnH}, "Export settings") {
		exportSettingsAsync(a.d)
	}
	importLabel := "Import settings..."
	if settings.importArmed {
		importLabel = "Drop the .json here"
	}
	if c.Button(sdl.Rect{X: pad + 190, Y: y, W: 200, H: btnH}, importLabel) {
		settings.importArmed = !settings.importArmed
		if settings.importArmed {
			settings.statusLine = "Drop an exported asyncao-settings .json anywhere on this window."
		}
	}
	y += 38

	// Setting presets (Nightingale): named bundles of the WHOLE settings file.
	y = a.settingsSection(y, w, "Setting presets")
	y = a.settingsDesc(pad, y, "Save your current settings under a name and switch between bundles. A preset is a full snapshot (passwords excluded); applying one replaces your settings on the next restart, like an import. Save your current setup as its own preset first if you'll want it back.", ColTextDim)
	y += 6
	settings.presetName, _ = c.TextField("presetname", sdl.Rect{X: pad, Y: y, W: 220, H: fieldH}, settings.presetName, "preset name (e.g. casing)")
	if c.Button(sdl.Rect{X: pad + 228, Y: y, W: 170, H: btnH}, "💾 Save as preset") {
		savePresetAsync(a.d, settings.presetName)
		settings.presetName = ""
	}
	y += btnH + 8
	for _, name := range a.d.Prefs.ListPresets() {
		c.Label(pad, y+4, name, ColText)
		if c.Button(sdl.Rect{X: pad + 240, Y: y, W: 150, H: btnH}, "Apply (restart)") {
			applyPresetAsync(a.d, name)
		}
		if c.Button(sdl.Rect{X: pad + 396, Y: y, W: 30, H: btnH}, "×") {
			_ = a.d.Prefs.DeletePreset(name)
		}
		y += btnH + 4
	}
	y += 6

	y = a.settingsSection(y, w, "Other data")
	y = a.settingsDesc(pad, y, "logs / recordings / screenshots sit next to the AsyncAO program (portable).", ColTextDim)
	y = a.settingsDesc(pad, y, "The streamed-asset cache is separate — view or clear it under Assets → Cache.", ColTextDim)
	y += 8
	return y
}
