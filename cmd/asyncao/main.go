// Command asyncao is the AsyncAO client: a maximum-performance, zero-fallback
// Attorney Online 2 client. See docs/ARCHITECTURE.md.
package main

import (
	"flag"
	"log"
	"net/http"
	_ "net/http/pprof"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/metrics"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/presence"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/ui"
)

const (
	// memoryBudgetBytes is the soft heap limit (spec §1: < 256 MiB on
	// a 200-character server).
	memoryBudgetBytes = 256 << 20

	windowTitle    = "AsyncAO"
	windowWidth    = 1152
	windowHeight   = 864
	frameCap       = 16667 * time.Microsecond // ~60 FPS when vsync is off
	debugPprofAddr = "localhost:6060"
	// minimizedNap paces the loop while the window is minimized: the
	// session still pumps (keepalives, queues) but nothing draws — no
	// point compositing 60 fps to nowhere.
	minimizedNap = 50 * time.Millisecond
	// maxFrameDelta clamps one frame's dt after a stall so time-driven
	// state (typewriter, blips, effect countdowns) resumes smoothly
	// instead of bursting the backlog (§perf frame pacing).
	maxFrameDelta = 100 * time.Millisecond
	// baselineDPI is the desktop "100%" DPI Windows and SDL report at
	// standard scaling — the divisor for the HiDPI auto UI scale.
	baselineDPI = 96.0
)

func main() {
	// SDL demands the main OS thread for the whole lifetime (spec §12).
	runtime.LockOSThread()

	var (
		flagServer = flag.String("server", "", "skip the lobby and connect to this ws:// or wss:// URL")
		flagMaster = flag.String("master", network.DefaultMasterServerURL, "master server list endpoint")
		flagDebug  = flag.Bool("debug", false, "expose pprof on "+debugPprofAddr)
		flagVsync  = flag.Bool("vsync", true, "use vsync presentation")
	)
	flag.Parse()

	debug.SetMemoryLimit(memoryBudgetBytes)

	if *flagDebug {
		go func() {
			log.Printf("pprof listening on http://%s/debug/pprof/", debugPprofAddr)
			log.Println(http.ListenAndServe(debugPprofAddr, nil))
		}()
	}

	if err := run(*flagServer, *flagMaster, *flagVsync, *flagDebug); err != nil {
		log.Fatal(err)
	}
}

// logSamples prints the 1 Hz profiler snapshot in --debug mode.
func logSamples(p *metrics.Profiler) {
	ticker := time.NewTicker(metrics.SampleInterval)
	defer ticker.Stop()
	for range ticker.C {
		s := p.Latest()
		if s == nil {
			continue
		}
		log.Printf("heap=%dMiB gcP99=%s hitRate=%.0f%% probes=%d cached404=%d",
			s.HeapBytes>>20, s.GCPauseP99, s.CacheHitRate*100, s.Probes, s.Cached404s)
	}
}

func run(serverURL, masterURL string, vsync, debugMode bool) error {
	// --- engine singletons (SDL-free) ---
	prefsPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	prefs, err := config.New(prefsPath)
	if err != nil {
		log.Printf("config: %v", err) // defaults already applied
	}
	defer prefs.Close()

	resolver := assets.NewResolver(prefs)

	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		return err
	}
	diskRoot, err := cache.DefaultDiskRoot()
	if err != nil {
		return err
	}
	disk, err := cache.NewDiskCache(diskRoot)
	if err != nil {
		return err
	}
	defer disk.Close()
	// The opt-in low-q sprite thumbnail store (power user, default OFF) lives in
	// its own directory BESIDE the T3 asset cache — independent lifetime by
	// design: thumbs are ~100× smaller than their sprites, so they stay useful
	// long after the full asset was pruned. Non-fatal: no thumbs ≠ no client.
	thumbs, err := assets.NewThumbCache(filepath.Join(filepath.Dir(diskRoot), "thumbs"))
	if err != nil {
		log.Printf("thumbcache: %v (thumbnails disabled)", err)
		thumbs = nil
	} else {
		defer thumbs.Close()
		thumbs.SetEnabled(prefs.ThumbCacheOn())
		thumbs.SetParams(prefs.ThumbHeightPx(), prefs.ThumbQuality())
		thumbs.SetBudget(int64(prefs.ThumbBudgetMiB()) << 20)
	}

	// Power-user network knobs: the 404 TTL is boot-applied (the negative-cache
	// LRU takes its TTL at construction — the Settings row says "restart");
	// the per-host deadline multiple is live but seeded here too.
	client := network.NewClientNotFoundTTL(time.Duration(prefs.NotFoundTTLSec()) * time.Second)
	client.SetAssetOrigin(prefs.AssetOriginHeader()) // power-user Origin/CORS override for asset streaming
	client.SetAdaptiveLatencyMultiple(prefs.AdaptiveLatMultiple())
	pool := network.NewPool(network.DefaultWorkers)
	defer pool.Close()
	decoder := assets.NewDecoderPool(assets.DecodeWorkers())
	defer decoder.Close()

	// --- SDL (render thread = this thread, forever) ---
	// Texture scale quality must be hinted before textures exist: "1" =
	// linear filtering (sprites stretched to the viewport stop
	// shimmering), "0" = nearest (the SDL default). The Settings toggle
	// re-hints and re-streams live. Batching cuts draw-call overhead on
	// label/icon-heavy frames.
	scaleHint := "1"
	if !prefs.SmoothScalingEnabled() {
		scaleHint = "0"
	}
	sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, scaleHint)
	sdl.SetHint(sdl.HINT_RENDER_BATCHING, "1")
	// Mark the process per-monitor DPI-aware BEFORE video init, so Windows renders
	// us at NATIVE resolution instead of bitmap-upscaling the whole window at
	// 125%/150% system scaling (the blurry-UI report). No-op off Windows.
	sdl.SetHint("SDL_WINDOWS_DPI_AWARENESS", "permonitorv2")
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_AUDIO | sdl.INIT_EVENTS); err != nil {
		return err
	}
	defer sdl.Quit()
	// The cross-thread render-loop doorbell (experimental event-driven loop):
	// packet arrivals and finished decodes push this user event so a loop
	// parked on WaitEventTimeout reacts instantly. Registered once, up front.
	ui.EnsureWakeEvent()
	if err := ttf.Init(); err != nil {
		return err
	}
	defer ttf.Quit()

	// Start at the saved windowed size if any, else the default.
	winW, winH := int32(windowWidth), int32(windowHeight)
	if sw, sh := prefs.WindowSize(); sw > 0 && sh > 0 {
		winW, winH = int32(sw), int32(sh)
	}
	window, err := sdl.CreateWindow(windowTitle,
		sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		winW, winH, sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE)
	if err != nil {
		return err
	}
	defer window.Destroy()
	// Clamp the (possibly stale/oversize) saved size to the display we landed on
	// and recenter — the startup half of the "too big to drag smaller" fix.
	if di, err := window.GetDisplayIndex(); err == nil {
		if ub, err := sdl.GetDisplayUsableBounds(di); err == nil {
			if cw, ch := config.ClampWindowSize(int(winW), int(winH), int(ub.W), int(ub.H)); int32(cw) != winW || int32(ch) != winH {
				window.SetSize(int32(cw), int32(ch))
				window.SetPosition(sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED)
			}
		}
	}
	if prefs.WindowFullscreen() {
		_ = window.SetFullscreen(sdl.WINDOW_FULLSCREEN_DESKTOP)
	}

	// Cap full-size textures (sprites/backgrounds) at the courtroom STAGE
	// height — the display height minus the chatbox/UI strip the layout always
	// reserves below the stage (screens.go: vpH = h-220). The stage never fills
	// the whole display, so capping to the real stage height makes the final
	// per-frame GPU scale gentler in EVERY window size (sharper, esp. windowed)
	// AND cheaper than the full display height. Strictly 0 per-frame cost: the
	// cached texture is smaller, and the single decode-time downscale is
	// unchanged. High-res art (e.g. 2000px sprites) is high-quality downscaled
	// once at decode; downscale-only, so a no-op for already-small art. Safe at
	// the common UI scale >= 1 (the real reserve only grows with scale); a tall-
	// stage theme at fullscreen costs at most a mild ~1.1:1 upscale, far below
	// the gap this fixes. The floor avoids over-shrinking on a tiny display.
	const (
		stageBottomReservePx = 220 // mirrors screens.go default-courtroom chatbox reserve
		minSpriteCapPx       = 480
	)
	spriteCapBase := 0 // display-derived downscale base (0 = display unknown → no cap)
	if di, err := window.GetDisplayIndex(); err == nil {
		if db, err := sdl.GetDisplayBounds(di); err == nil && db.H > 0 {
			spriteCapBase = int(db.H) - stageBottomReservePx
			if spriteCapBase < minSpriteCapPx {
				spriteCapBase = int(db.H) // tiny display: keep full height, don't over-shrink
			}
			// Power-user downscale override rides ON TOP of the display-derived
			// base: a percent scales the target, the off switch drops the cap
			// entirely (config.EffectiveSpriteCap owns the math; the App re-derives
			// it live when the Settings sliders move).
			decoder.SetSpriteCap(config.EffectiveSpriteCap(spriteCapBase, prefs.SpriteDownscaleOffOn(), prefs.SpriteDownscalePct()))
		}
	}

	renFlags := uint32(sdl.RENDERER_ACCELERATED)
	if vsync {
		renFlags |= sdl.RENDERER_PRESENTVSYNC
	}
	ren, err := sdl.CreateRenderer(window, -1, renFlags)
	if err != nil {
		// VMs/headless (dummy video driver) have no accelerated backend.
		log.Printf("accelerated renderer unavailable (%v); falling back to software", err)
		ren, err = sdl.CreateRenderer(window, -1, sdl.RENDERER_SOFTWARE)
		if err != nil {
			return err
		}
	}
	defer ren.Destroy()
	// Draw-op alpha (taken overlays, chat box, selection highlights) needs
	// the renderer's blend mode set: SDL defaults to NONE and renders
	// every alpha Fill opaque. Textures set their own mode at upload.
	_ = ren.SetDrawBlendMode(sdl.BLENDMODE_BLEND)

	// Power-user T1 budget (restart-applied; the default 64 MiB fits the 256 MiB
	// memory budget alongside T2's 128 MiB).
	store, err := render.NewTextureStoreBudget(ren, int64(prefs.TexBudgetMiB())<<20)
	if err != nil {
		return err
	}
	defer store.Purge()
	// Scale the per-asset decode cap off the SAME (live) texture budget — half of
	// it, the eviction-safe ratio — so raising the texture budget lets a longer
	// animation decode in full instead of truncating past ~5 s (the decoder
	// otherwise used a fixed cap off the DEFAULT budget, ignoring this setting).
	assets.SetMaxDecodedAssetBytes((int64(prefs.TexBudgetMiB()) << 20) / 2)

	// --- asset pipeline ---
	var localMode bool
	var source assets.Fetcher = client
	if enabled, mounts := prefs.LocalAssets(); enabled && len(mounts) > 0 {
		source = assets.NewLocalFetcher(mounts)
		localMode = true
	}
	manager := assets.NewManager(assets.ManagerDeps{
		Resolver:   resolver,
		Prefs:      prefs,
		T2:         t2,
		Disk:       disk,
		Source:     source,
		LocalMode:  localMode,
		Pool:       pool,
		Decoder:    decoder,
		Thumbs:     thumbs, // opt-in low-q sprite thumbnails (nil when unavailable)
		T1Contains: store.Contains,
		T1Failed:   store.FailedRecently,
	})
	manager.SetDiskCompression(prefs.DiskZstdEnabled())

	viewport := render.NewViewport(store)
	audio := render.NewAudio(manager)
	defer audio.Close()
	audio.SetVolumes(prefs.AudioVolumes())

	// 1 Hz sampler: heap, GC pause p99, cache hit rate, probe counts.
	profiler := metrics.NewProfiler(metrics.StatsSource{
		CacheHits: func() (int64, int64) {
			s := t2.Stats()
			return s.Hits, s.Misses
		},
		NetRequests: func() (int64, int64) {
			s := client.Stats()
			return s.Requests, s.Cached404s
		},
		LearnedHits: func() (int64, int64) {
			s := resolver.Stats()
			return s.LearnedHits, s.LearnedMisses
		},
	})
	profiler.Start()
	defer profiler.Stop()
	if debugMode {
		go logSamples(profiler)
	}

	uiCtx, err := ui.NewCtx(ren)
	if err != nil {
		return err
	}
	defer uiCtx.Destroy()
	uiCtx.SetWindow(window)  // modcall/case-alert taskbar flashing
	ui.SetWindowIcon(window) // window / taskbar icon = the Mayo mascot (mascot.go)

	// Discord Rich Presence: stdlib-only local IPC, fully optional at
	// build (-tags nodiscord) AND runtime — it idles silently when the
	// Settings toggle is off or Discord isn't running. The app identity is the
	// baked-in official AsyncAO app (not user-editable); the Enabled toggle is
	// enforced dynamically in updatePresence, so dialing it unconditionally here
	// is safe and works even for saved prefs whose AppID predates the bake.
	pres := presence.New(config.DefaultDiscordAppID)
	defer pres.Close()

	app := ui.NewApp(uiCtx, ui.Deps{
		Profiler:  profiler,
		Prefs:     prefs,
		Resolver:  resolver,
		Manager:   manager,
		Pool:      pool,
		Client:    client,
		Store:     store,
		Viewport:  viewport,
		Pump:      nil, // set below (needs app for liveness)
		Audio:     audio,
		Presence:  pres,
		MasterURL: masterURL,
	})
	pump := render.NewPump(store, manager, app.IsLiveBase)
	app.SetPump(pump)
	app.SetSpriteCapBase(spriteCapBase) // the Settings downscale sliders re-derive the cap from this live

	// Auto UI scale has two inputs, combined per frame in SetAutoScaleFromWindow:
	// the display DPI (HiDPI laptops) and the WINDOW SIZE (a maximized window on a
	// big display would otherwise show a tiny island of fixed-pixel widgets — the
	// "text is too small" reports; it's also why shrinking the window already
	// looked right). Both are floored at 100% so we never auto-SHRINK: SDL's
	// GetDisplayDPI is unreliable and once reported low, shrinking the whole UI
	// below 100% (#6). Record the DPI component here; the per-frame call in the
	// loop adds the window factor, snaps to the step, and caps at MaxUIScalePercent.
	if ddpi, _, _, err := sdl.GetDisplayDPI(0); err == nil && ddpi > 0 {
		app.SetDisplayDPIScale(int(ddpi/baselineDPI*100 + 0.5))
	}

	if serverURL != "" {
		app.Connect(serverURL, serverURL)
	}

	// --- main loop: fixed-cadence update + single render pass ---
	last := time.Now()
	// scheduledNap is the sleep WE chose last pass (pacing tier / skip nap /
	// minimized nap): the stall guard below must allow it in full, or every
	// pacing tier slower than maxFrameDelta plays animation clocks in slow
	// motion (dt loss ate real time — the "anims crawl at 5 fps unfocused"
	// class of bug).
	scheduledNap := time.Duration(0)
	// pendingEv carries the one event a WaitEventTimeout park dequeued (the
	// experimental loop's wake): it feeds the NEXT pass's input phase first,
	// so nothing is lost or reordered against the regular poll drain.
	var pendingEv sdl.Event
	running := true
	for running {
		now := time.Now()
		dt := now.Sub(last)
		last = now
		// Pacing guard: after a STALL (window drag, driver freeze, sleep
		// wake) an unbounded dt would dump the typewriter's whole backlog
		// and machine-gun its blips in one frame. A stall is time beyond
		// what we scheduled: the allowance is the chosen nap plus the usual
		// margin, so deliberate slow cadences stay honest while real stalls
		// still clamp. Animation clocks just resume.
		if limit := scheduledNap + maxFrameDelta; dt > limit {
			dt = limit
		}
		scheduledNap = 0

		// Order matters: BeginFrame resets the per-frame input snapshot
		// that HandleEvent fills, so it must run before the event poll.
		// (Inverting these erased every click before the UI saw it.)
		uiCtx.BeginFrame(dt)
		sawEvent := false
		sawInput := false // any non-motion event (click, key, wheel, window)
		handleEv := func(ev sdl.Event) {
			if ui.IsWakeEvent(ev) {
				return // a background doorbell (packet/decode), never user input
			}
			switch e := ev.(type) {
			case *sdl.QuitEvent:
				running = false
			case *sdl.DropEvent:
				// Drag-and-drop import (#73): a .aorec / AO2 .demo dropped on the
				// window imports into recordings\ and starts playing.
				if e.Type == sdl.DROPFILE {
					app.HandleFileDrop(e.File)
				}
			}
			uiCtx.HandleEvent(ev)
			sawEvent = true
			if _, motion := ev.(*sdl.MouseMotionEvent); !motion {
				sawInput = true
			}
		}
		if pendingEv != nil {
			handleEv(pendingEv)
			pendingEv = nil
		}
		for ev := sdl.PollEvent(); ev != nil; ev = sdl.PollEvent() {
			handleEv(ev)
		}
		// Frame pacing: real interaction snaps back to full rate; bare pointer
		// motion gets the short motion grace instead (experimental loop), so
		// waving the cursor over nothing stops costing frames when it stops.
		if sawInput {
			app.NoteInput()
		} else if sawEvent {
			app.NoteMotion()
		}

		if window.GetFlags()&sdl.WINDOW_MINIMIZED != 0 {
			app.Background(dt) // keep the session alive, draw nothing
			scheduledNap = minimizedNap
			// Interruptible nap: a restore / focus-gain / alt-tab-in event returns
			// immediately so the window wakes and redraws on the spot, instead of
			// finishing out the nap first (a felt delay coming back to the window).
			if ev := sdl.WaitEventTimeout(int(minimizedNap / time.Millisecond)); ev != nil {
				pendingEv = ev
			}
			continue
		}

		// Static skip (the deepest pacing tier): nothing new to show — no input
		// this pass, nothing animating, nobody talking, no toast/caret/timer — so
		// the last presented frame is still exactly right. Skip render+present
		// entirely (GPU cost → zero) and keep the session pumping. The event-
		// driven loop then parks until something actually happens; the classic
		// loop naps at the idle cadence and re-polls.
		focused := window.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
		// #5 bypass: with the frame limiter disabled the loop renders every pass
		// (no static skip, no adaptive pacing) — vsync alone paces it. The
		// deliberate high-GPU escape hatch; default OFF.
		limiterOff := prefs.FrameLimiterDisabled()
		if !limiterOff && app.SkipFrame(focused, sawEvent) {
			app.Background(dt)
			if prefs.EventDrivenLoopOn() {
				// EXPERIMENTAL event-driven wait. The Background pumps above may
				// have produced redraw-worthy work (packets, texture uploads):
				// loop straight around and render it — the next pass's SkipFrame
				// refuses on RenderNeeded. Otherwise park on the OS event wait
				// until input, a wake doorbell, or the nearest scheduled deadline.
				// NextWakeDelay reports both HOW LONG to park and whether a plain
				// timeout is a real redraw deadline (idle-rate tick, caret flip,
				// clock second) or just the Background-only housekeeping floor —
				// the floor pumps the session and re-parks WITHOUT drawing, which
				// is how idle=off reaches genuinely zero redraws. Input/wake events
				// interrupt the wait regardless, for instant response at zero cost.
				if app.RenderNeeded() {
					continue
				}
				wait, renderDue := app.NextWakeDelay(focused)
				scheduledNap = wait
				if ev := sdl.WaitEventTimeout(int(wait / time.Millisecond)); ev != nil {
					pendingEv = ev
				} else if renderDue {
					app.NoteDeadline() // a real deadline is due — draw one frame for it
				}
				continue
			}
			nap := app.FramePace(focused)
			if nap <= 0 || nap > maxFrameDelta {
				nap = maxFrameDelta
			}
			scheduledNap = nap
			time.Sleep(nap)
			continue
		}

		// Global UI scale: render at logical size, let the GPU scale the
		// whole frame; the kit unprojects the mouse through the same
		// factor, so every widget scales without per-element math.
		w, h := window.GetSize()
		app.SetAutoScaleFromWindow(w, h) // window-relative auto scale (when auto-scale is on)
		scale := float32(app.UIScale()) / 100
		_ = ren.SetScale(scale, scale)
		lw := int32(float32(w) / scale)
		lh := int32(float32(h) / scale)
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		app.Frame(dt, lw, lh)
		ren.Present()
		// A real interaction (click / key / wheel) almost always changed UI-visible
		// state DURING the draw above — a screen switch, a menu open, a toggle — which
		// only appears on the NEXT frame. Force that one frame so it's never stranded
		// until cursor motion or the idle tick reveals it. Motion is excluded (it has
		// its own live grace); idle-safe (no input, no follow-up).
		if sawInput {
			app.NoteInteraction()
		}

		// Frame pacing (the GPU-burn fix): sleep the frame's remaining budget.
		// vsync stays on for tear-free presents, but it CANNOT be the throttle —
		// it ties the loop to the panel (165 Hz laptops burned GPU while idle)
		// and some windowed present paths don't block at all (the "54% GPU in a
		// tiny window" report). Two nested budgets:
		//
		//   hardCap — the user's active (focused) or unfocused FPS ceiling. It is
		//     INVIOLABLE: slept UNINTERRUPTIBLY, so an input flood — above all
		//     mouse motion, which streams an event every few ms — can never
		//     interrupt the pace and drive the loop past the cap. This is the
		//     "caps are ALWAYS obeyed" contract, and it also enforces the
		//     unfocused cap even when FramePace lifts an unfocused animation
		//     toward the active rate.
		//   pace   — FramePace's adaptive tier (full while interacting/animating,
		//     the idle rate on a static screen). Always ≥ hardCap when focused;
		//     the SURPLUS beyond the ceiling is slept INTERRUPTIBLY so input during
		//     a slow idle/animation frame still responds within one ceiling instead
		//     of waiting the whole slow budget out (the low-idle input-lag fix).
		//
		// With vsync off both fall back to the old fixed ~60 fps sleep.
		pace := app.FramePace(focused)
		hardCap := app.HardCapBudget(focused)
		if limiterOff {
			pace, hardCap = 0, 0 // #5 bypass: no adaptive cap AND no ceiling — vsync paces the presents
		}
		if !vsync {
			if pace == 0 || pace > frameCap {
				pace = frameCap // -vsync=false keeps at least its old 60 fps sleep (bypass included)
			}
			if hardCap == 0 || hardCap > frameCap {
				hardCap = frameCap
			}
		}
		elapsed := time.Since(now)
		budget := pace
		if hardCap > budget {
			budget = hardCap // unfocused: the ceiling can be slower than the tier
		}
		if nap := budget - elapsed; nap > 0 {
			scheduledNap = nap // total intended sleep — the stall guard must allow it in full
		}
		// Audio-paced sub-stepping (audio independent of the frame rate): while a
		// message types at a low present rate, a single pacing sleep would advance
		// the courtroom once with a big dt and machine-gun a whole present-period of
		// blips at one instant ("blips only every screen refresh at a 1 fps cap").
		// Instead spend the pacing budget advancing the room — and playing its blips
		// — at the fine audio cadence: Background advances room + drains audio without
		// drawing, and the NEXT Frame draws the already-current room (MarkRoomPreAdvanced
		// makes it skip its own room.Update). Interruptible, so input still renders the
		// next frame early; bounded by the budget deadline (rule §17.4). Only while the
		// live courtroom is actually streaming blips, so a static screen still idles.
		if !limiterOff && app.AudioPaceActive(budget) {
			fine := app.AudioFineTick()
			deadline := now.Add(budget)
			for {
				rem := time.Until(deadline)
				if rem <= 0 {
					break
				}
				step := fine
				if step > rem {
					step = rem
				}
				app.Background(step)      // advance room + audio at the fine cadence, no draw
				app.MarkRoomPreAdvanced() // the next Frame draws the advanced room without re-advancing it
				if ev := sdl.WaitEventTimeout(int(step / time.Millisecond)); ev != nil {
					pendingEv = ev // input / background wake: render the next frame now
					break
				}
			}
			continue
		}
		// The ceiling, uninterruptibly: nothing renders faster than the cap.
		if s := hardCap - elapsed; s > 0 {
			time.Sleep(s)
		}
		// The surplus beyond the ceiling, interruptibly (slower idle/anim tiers only):
		// input or a background wake arriving here renders NOW rather than waiting the
		// whole slow budget out. The classic loop keeps a plain sleep.
		if pace > hardCap {
			if sleep := pace - time.Since(now); sleep > 0 {
				if prefs.EventDrivenLoopOn() {
					if ev := sdl.WaitEventTimeout(int(sleep / time.Millisecond)); ev != nil {
						pendingEv = ev
					}
				} else {
					time.Sleep(sleep)
				}
			}
		}
	}

	app.RememberOpenTabs() // restore-on-launch: snapshot tabs before the final flush (no-op when off)
	app.CloseTranscript()  // flush + close the detailed-log file (no-op when off/unopened)
	app.CloseJukebox()     // flush the global music library (no-op until loaded)
	_ = prefs.SaveNow()
	app.MaybeRelaunch() // "Restart to apply": start the new binary now that we've shut down cleanly
	return nil
}
