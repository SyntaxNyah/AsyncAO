// Command asyncao is the AsyncAO client: a maximum-performance, zero-fallback
// Attorney Online 2 client. See docs/ARCHITECTURE.md.
package main

import (
	"flag"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/metrics"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/presence"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
	"github.com/SyntaxNyah/AsyncAO/internal/ui"
)

const (
	// memoryBudgetBytes fits a full animated decode plus T2, so the GC doesn't thrash.
	memoryBudgetBytes = 700 << 20

	windowTitle    = "AsyncAO"
	windowWidth    = 1152
	windowHeight   = 864
	frameCap       = 16667 * time.Microsecond // ~60 FPS when vsync is off
	debugPprofAddr = "localhost:6060"
	// minimizedNap paces the loop while nothing draws; the session still pumps.
	minimizedNap = 50 * time.Millisecond
	// maxFrameDelta clamps dt after a stall so the typewriter and blips don't burst.
	maxFrameDelta = 100 * time.Millisecond
)

func main() {
	// SDL demands the main OS thread for the whole lifetime (spec §12).
	runtime.LockOSThread()

	var (
		flagServer      = flag.String("server", "", "skip the lobby and connect to this ws:// or wss:// URL")
		flagMaster      = flag.String("master", network.DefaultMasterServerURL, "master server list endpoint")
		flagDebug       = flag.Bool("debug", false, "expose pprof on "+debugPprofAddr)
		flagVsync       = flag.Bool("vsync", true, "use vsync presentation")
		flagSoftGPU     = flag.Bool("software-gpu", false, "draw with the bundled software GPU driver (for broken GPU drivers)")
		flagGPURecovery = flag.Int("gpu-recovery", 0, "set by the client when it restarts after a GPU device loss: losses so far")
	)
	flag.Parse()

	debug.SetMemoryLimit(memoryBudgetBytes)
	// Crash reports need every goroutine's stack, even in -H=windowsgui where stderr goes nowhere.
	debug.SetTraceback("all")
	setupCrashOutput()

	if *flagDebug {
		go func() {
			log.Printf("pprof listening on http://%s/debug/pprof/", debugPprofAddr)
			log.Println(http.ListenAndServe(debugPprofAddr, nil))
		}()
	}

	gpuOpts := render.BackendOptions{
		VSync:              *flagVsync,
		ForceSoftware:      *flagSoftGPU,
		SoftwareDriverFile: softwareVulkanDriverFile(),
	}
	if err := run(*flagServer, *flagMaster, gpuOpts, *flagGPURecovery, *flagDebug); err != nil {
		log.Fatal(err)
	}
}

// setupCrashOutput writes crashes the per-path recover sites miss to asyncao-crash.log.
func setupCrashOutput() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), "asyncao-crash.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	// SetCrashOutput duplicates f's descriptor; we can close f immediately.
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		return
	}
	_ = f.Close()
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
		log.Printf("heap=%dMiB sys=%dMiB gcP99=%s hitRate=%.0f%% probes=%d cached404=%d",
			s.HeapBytes>>20, s.TotalBytes>>20, s.GCPauseP99, s.CacheHitRate*100, s.Probes, s.Cached404s)
	}
}

func run(serverURL, masterURL string, gpuOpts render.BackendOptions, gpuRecoveries int, debugMode bool) error {
	vsync := gpuOpts.VSync
	// --- engine singletons (SDL-free) ---
	prefsPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	prefs, err := config.New(prefsPath)
	if err != nil {
		log.Printf("config: %v", err) // defaults already applied
	}
	// Corrupt settings were renamed aside; tell the user, since release builds have no console.
	configQuarantine := prefs.Quarantine()
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
	// Pass the cap up front so the at-open sweep enforces it (0 = unlimited).
	disk, err := cache.NewDiskCache(diskRoot, int64(prefs.Load().DiskCacheBudgetMiBVal)<<20)
	if err != nil {
		return err
	}
	defer disk.Close()
	// Thumbnails live beside T3 and outlive pruned assets. Failing to open them is not fatal.
	thumbs, err := assets.NewThumbCache(filepath.Join(filepath.Dir(diskRoot), "thumbs"))
	if err != nil {
		log.Printf("thumbcache: %v (thumbnails disabled)", err)
		thumbs = nil
	} else {
		defer thumbs.Close()
		thumbs.SetEnabled(prefs.Load().ThumbCache)
		thumbs.SetParams(prefs.Load().ThumbHeightPx(), prefs.Load().ThumbQuality())
		thumbs.SetBudget(int64(prefs.Load().ThumbBudgetMiB()) << 20)
	}

	// Resolve the proxy before anything can dial, or the earliest requests would go direct.
	proxyPolicy := netproxy.Configure(netproxy.Mode(prefs.Load().ProxyModeVal), prefs.Load().ProxyURL)
	log.Printf("proxy: %s", proxyPolicy.Describe())

	// The 404 TTL is set at construction, so changing it needs a restart.
	client := network.NewClientNotFoundTTL(time.Duration(prefs.Load().NotFoundTTLSecVal) * time.Second)
	client.SetAssetOrigin(prefs.Load().AssetOrigin) // power-user Origin/CORS override for asset streaming
	client.SetAdaptiveLatencyMultiple(prefs.Load().AdaptiveLatMultipleVal)
	pool := network.NewPool(network.DefaultWorkers)
	defer pool.Close()
	decoder := assets.NewDecoderPool(assets.DecodeWorkers())
	defer decoder.Close()
	decoder.SetAnimatedBudgetMiB(prefs.Load().AnimatedBudgetMiB())
	decoder.SetAnimatedDecodeConcurrency(prefs.Load().AnimDecodeConcurrency())

	// --- SDL (render thread = this thread, forever) ---
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_AUDIO | sdl.INIT_EVENTS); err != nil {
		return err
	}
	defer sdl.Quit()
	// Packet arrivals and finished decodes push this event to wake a parked loop.
	ui.EnsureWakeEvent()
	if err := ttf.Init(); err != nil {
		return err
	}
	defer ttf.Quit()

	// Start at the saved windowed size if any, else the default.
	winW, winH := int32(windowWidth), int32(windowHeight)
	if sw, sh := prefs.Load().WindowSize(); sw > 0 && sh > 0 {
		winW, winH = int32(sw), int32(sh)
	}
	// The primary display is right here only: no window exists yet and no position is saved.
	window, err := sdl.CreateWindow(windowTitle,
		sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		winW, winH, sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE|highDPIFlags())
	if err != nil {
		return err
	}
	defer window.Destroy()
	// SDL3 sends no text-input events until this is on.
	if err := window.StartTextInput(); err != nil {
		log.Printf("text input unavailable: %v", err)
	}
	mainWindowID, idErr := window.GetID()
	if idErr != nil {
		// 0 is no real window id, so quitting falls back to SDL_QUIT alone.
		mainWindowID = 0
	}
	// Clamp a stale saved size to the display we landed on.
	if di, err := window.GetDisplayIndex(); err == nil {
		if ub, err := sdl.GetDisplayUsableBounds(di); err == nil {
			if cw, ch := config.ClampWindowSize(int(winW), int(winH), int(ub.W), int(ub.H)); int32(cw) != winW || int32(ch) != winH {
				// Use the measured display, or the window lands on one monitor sized for another.
				pos := ui.CenteredWindowPos(di)
				window.SetSize(int32(cw), int32(ch))
				window.SetPosition(pos, pos)
			}
		}
	}
	if prefs.Load().WindowFull {
		_ = window.SetFullscreen(sdl.WINDOW_FULLSCREEN_DESKTOP)
	}

	// Cap textures at the stage height, which keeps the per-frame GPU scale gentle.
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
			// config.EffectiveSpriteCap applies the user's downscale override to this base.
			decoder.SetSpriteCap(config.EffectiveSpriteCap(spriteCapBase, prefs.Load().SpriteDownscaleOff, prefs.Load().SpriteDownscalePctVal))
			decoder.SetAnimatedSpriteCap(prefs.Load().AnimatedSpriteCapPxVal)
		}
	}

	backend, err := render.OpenBackend(window, gpuOpts)
	if err != nil {
		return err
	}
	defer backend.Close()
	log.Printf("renderer: %s", backend.Name())
	ren := backend.Renderer()
	gpuWatcher := render.NewGPUWatcher(gpuRecoveries, backend.Software(), time.Now)
	// Linear or nearest, for every texture created from now on.
	_ = ren.SetDefaultTextureScaleMode(ui.TextureScaleMode(prefs.Load().SmoothScaling))
	// macOS Retina: fold the 2x drawable into the render scale, since SetLogicalSize would break hit-testing.
	retinaScale := retinaScaleFactor(ren, window)
	// SDL defaults to no blending, which draws every alpha fill opaque.
	_ = ren.SetDrawBlendMode(sdl.BLENDMODE_BLEND)

	// Only compress to formats this renderer can upload; the software fallback cannot.
	texCompressMode := assets.CompressOff
	if mode := prefs.TexCompression(); mode != config.TexCompressOff {
		if info, err := ren.GetInfo(); err == nil {
			dxt5 := slices.Contains(info.TextureFormats, assets.DXT5FourCC)
			dxt1 := slices.Contains(info.TextureFormats, assets.DXT1FourCC)
			switch {
			case mode == config.TexCompressDXT5 && dxt5:
				texCompressMode = assets.CompressDXT5
			case mode == config.TexCompressDXT1 && dxt1:
				texCompressMode = assets.CompressDXT1
			}
		}
	}
	decoder.SetTextureCompression(texCompressMode)

	// Applied at restart.
	store, err := render.NewTextureStoreBudget(ren, int64(prefs.Load().TexBudgetMiB())<<20)
	if err != nil {
		return err
	}
	defer store.Purge()

	// --- asset pipeline ---
	// Appended after the manual mounts so an explicit folder wins.
	enabled, manualMounts := prefs.LocalAssets()
	autoMounts := config.DiscoverPackages(config.ExecutableDir())
	allMounts := config.CombineMounts(manualMounts, autoMounts)
	var localMode bool
	var source assets.Fetcher = client
	if enabled && len(allMounts) > 0 {
		source = assets.NewLocalFetcher(allMounts)
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
		T1Contains: store.ContainsAnimated,
		T1Failed:   store.FailedRecently,
	})
	// Mounts also resolve recordings, so seed the overlay regardless of the local-folders flag.
	if len(allMounts) > 0 {
		manager.SetLocalOverlay(assets.NewLocalFetcher(allMounts))
	}
	manager.SetDiskCompression(prefs.Load().DiskZstd)
	// 0 = unlimited.
	manager.SetDiskBudget(int64(prefs.Load().DiskCacheBudgetMiBVal) << 20)

	viewport := render.NewViewport(store)
	audio := render.NewAudio(manager)
	defer audio.Close()
	audio.SetVolumes(prefs.Load().AudioVolumes())

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
	uiCtx.SetWindow(window) // modcall/case-alert taskbar flashing
	// preview stays closed, at no cost, until the user pops it out.
	preview := render.NewPreviewWindow()
	defer preview.Close()
	ui.SetWindowIcon(window) // window / taskbar icon = the Mayo mascot (mascot.go)

	// Enabled is checked in updatePresence, so dialing here is safe; it idles without Discord.
	pres := presence.New(config.DefaultDiscordAppID)
	defer pres.Close()

	app := ui.NewApp(uiCtx, ui.Deps{
		Profiler:         profiler,
		Prefs:            prefs,
		Resolver:         resolver,
		Manager:          manager,
		Pool:             pool,
		Client:           client,
		Store:            store,
		Viewport:         viewport,
		Pump:             nil, // set below (needs app for liveness)
		Audio:            audio,
		Preview:          preview,
		Presence:         pres,
		MasterURL:        masterURL,
		RestoreTabs:      gpuRecoveries > 0, // restarted after a GPU device loss: reopen the saved tabs
		ConfigQuarantine: configQuarantine,
		AutoMounts:       autoMounts,
	})
	pump := render.NewPump(store, manager, app.IsLiveBase)
	app.SetPump(pump)
	app.SetSpriteCapBase(spriteCapBase)     // the Settings downscale sliders re-derive the cap from this live
	uiCtx.SetDeviceScaleFactor(retinaScale) // macOS Retina: device pixel scale for text raster only (no-op off macOS)

	// Auto UI scale combines display DPI and window size, floored at 100% so a bad reading never shrinks the UI.
	// sdl.GetDisplayDPI reports a flat 96 under per-monitor-v2 awareness, hence the per-monitor query.
	app.SeedDisplayDPIScale()

	if serverURL != "" {
		app.Connect(serverURL, serverURL)
	}

	// --- main loop: fixed-cadence update + single render pass ---
	last := time.Now()
	// scheduledNap is the sleep we chose last pass. The stall guard must allow it in full,
	// or slow pacing tiers play animations in slow motion.
	scheduledNap := time.Duration(0)
	// pendingEv is the event a wait dequeued; the next pass handles it first.
	var pendingEv sdl.Event
	running := true
	for running {
		now := time.Now()
		dt := now.Sub(last)
		last = now
		// Clamp a stall (window drag, driver freeze, sleep wake) so the typewriter doesn't burst.
		if limit := scheduledNap + maxFrameDelta; dt > limit {
			dt = limit
		}
		scheduledNap = 0

		// BeginFrame resets the input snapshot HandleEvent fills, so it must run first.
		uiCtx.BeginFrame(dt)
		sawEvent := false
		sawInput := false // any non-motion event (click, key, wheel, window)
		handleEv := func(ev sdl.Event) {
			if ui.IsWakeEvent(ev) {
				return // a background doorbell (packet/decode), never user input
			}
			if mainWindowShouldQuit(ev, mainWindowID) {
				running = false
			}
			// Preview events stay out of uiCtx and the main-window state below (see dispatchEvent).
			if dispatchEvent(ev, uiCtx, preview) {
				return
			}
			switch e := ev.(type) {
			case *sdl.DropEvent:
				if e.Type == sdl.DROPFILE {
					app.HandleFileDrop(e.File)
				}
			case *sdl.WindowEvent:
				// DISPLAY_CHANGED is exact; MOVED covers SDL builds without it.
				if e.Event == sdl.WINDOWEVENT_DISPLAY_CHANGED || e.Event == sdl.WINDOWEVENT_MOVED {
					app.NoteDisplayChanged()
				}
				// The user may have changed files in another program: recheck the theme catalog.
				if e.Event == sdl.WINDOWEVENT_FOCUS_GAINED {
					app.NoteFocusGained()
				}
				if e.Event == sdl.WINDOWEVENT_FOCUS_LOST {
					app.NoteFocusLost()
				}
			}
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
		// Real input restores full rate; bare motion only gets a short grace.
		if sawInput {
			app.NoteInput()
		} else if sawEvent {
			app.NoteMotion()
		}

		if window.GetFlags()&sdl.WINDOW_MINIMIZED != 0 {
			app.Background(dt) // keep the session alive, draw nothing
			// The preview is its own OS window and must keep animating while this one is minimized.
			app.AdvanceDetachedPreview()
			preview.Present()
			nap := minimizedNap
			// Don't freeze an animating preview for the whole nap.
			if wakeCap := app.PreviewAnimWakeCap(); wakeCap > 0 && nap > wakeCap {
				nap = wakeCap
			}
			scheduledNap = nap
			// Interruptible, so restoring the window redraws at once.
			if ev := sdl.WaitEventTimeout(int(nap / time.Millisecond)); ev != nil {
				pendingEv = ev
			}
			continue
		}

		// Nothing new to show: skip render and present, and keep the session pumping.
		focused := window.GetFlags()&sdl.WINDOW_INPUT_FOCUS != 0
		// Limiter off: render every pass and let vsync pace.
		limiterOff := prefs.Load().DisableFrameLimiter
		if !limiterOff && app.SkipFrame(focused, sawEvent) {
			app.Background(dt)
			// Keep the preview animating on passes the main window skips.
			app.AdvanceDetachedPreview()
			preview.Present()
			if prefs.Load().EventDrivenLoop {
				// Background may have produced work: render it. Otherwise wait for input, a wake or a deadline.
				if app.RenderNeeded() && !app.RenderSuppressed(focused) {
					continue
				}
				wait, renderDue := app.NextWakeDelay(focused)
				// Only shortens the wait; it never forces a main-window redraw.
				if wakeCap := app.PreviewAnimWakeCap(); wakeCap > 0 && wait > wakeCap {
					wait = wakeCap
				}
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
			if wakeCap := app.PreviewAnimWakeCap(); wakeCap > 0 && nap > wakeCap {
				nap = wakeCap
			}
			scheduledNap = nap
			time.Sleep(nap)
			continue
		}

		// Draw at logical size and let the GPU scale the frame; the kit unprojects the mouse to match.
		w, h := window.GetSize()
		app.SetAutoScaleFromWindow(w, h) // window-relative auto scale (when auto-scale is on)
		scale := float32(app.UIScale()) / 100
		_ = ren.SetScale(scale*retinaScale, scale*retinaScale)
		lw := int32(float32(w) / scale)
		lh := int32(float32(h) / scale)
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		app.Frame(dt, lw, lh)
		switch recovery := gpuWatcher.Observe(backend.Present()); recovery {
		case render.GPUWorking:
		case render.GPUGiveUp:
			log.Printf("gpu: the software driver lost its device too; quitting instead of restarting again")
			_ = sdl.ShowErrorBox(windowTitle, gpuGiveUpMessage, window)
			running = false
		default:
			app.RequestRelaunch(gpuRecoveryArgs(recovery, masterURL, gpuOpts, gpuRecoveries, debugMode), true)
		}
		preview.Present()
		// A click's state change only appears on the next frame, so force one.
		if sawInput {
			app.NoteInteraction()
		}

		// vsync can't be the throttle: it follows the panel's rate, and some windowed presents don't block.
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
		// While a message types, advance the room and blips in fine steps through the sleep, or a low
		// frame cap fires a whole period of blips at once. The hard cap stays uninterruptible.
		audioPace := !limiterOff && app.AudioPaceActive(budget)
		// fineAdvance sleeps total in audio-sized steps; only the surplus tier yields to input.
		fineAdvance := func(total time.Duration, interruptible bool) {
			end := time.Now().Add(total)
			for {
				rem := time.Until(end)
				if rem <= 0 {
					return
				}
				step := app.AudioFineTick()
				if step > rem {
					step = rem
				}
				app.Background(step)      // advance room + audio at the fine cadence, no draw
				app.MarkRoomPreAdvanced() // the next Frame draws the advanced room without re-advancing it
				if interruptible {
					if ev := sdl.WaitEventTimeout(int(step / time.Millisecond)); ev != nil {
						pendingEv = ev // input / background wake: render the next frame now
						return
					}
				} else {
					time.Sleep(step)
				}
			}
		}
		// The ceiling, uninterruptibly: nothing renders faster than the cap.
		if s := hardCap - elapsed; s > 0 {
			if audioPace {
				fineAdvance(s, false) // cap floor preserved; audio still advances finely
			} else {
				time.Sleep(s)
			}
		}
		// Surplus beyond the cap: input or a wake renders at once.
		if pace > hardCap {
			if sleep := pace - time.Since(now); sleep > 0 {
				switch {
				case audioPace:
					fineAdvance(sleep, prefs.Load().EventDrivenLoop)
				case prefs.Load().EventDrivenLoop:
					if ev := sdl.WaitEventTimeout(int(sleep / time.Millisecond)); ev != nil {
						pendingEv = ev
					}
				default:
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
