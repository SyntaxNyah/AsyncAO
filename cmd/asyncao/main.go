// Command asyncao is the AsyncAO client: a maximum-performance, zero-fallback
// Attorney Online 2 client. See docs/ARCHITECTURE.md.
package main

import (
	"flag"
	"log"
	"net/http"
	_ "net/http/pprof"
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

	client := network.NewClient()
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
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_AUDIO | sdl.INIT_EVENTS); err != nil {
		return err
	}
	defer sdl.Quit()
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
	if di, err := window.GetDisplayIndex(); err == nil {
		if db, err := sdl.GetDisplayBounds(di); err == nil && db.H > 0 {
			spriteCap := int(db.H) - stageBottomReservePx
			if spriteCap < minSpriteCapPx {
				spriteCap = int(db.H) // tiny display: keep full height, don't over-shrink
			}
			decoder.SetSpriteCap(spriteCap)
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

	store, err := render.NewTextureStore(ren)
	if err != nil {
		return err
	}
	defer store.Purge()

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
	uiCtx.SetWindow(window) // modcall/case-alert taskbar flashing

	// Discord Rich Presence: stdlib-only local IPC, fully optional at
	// build (-tags nodiscord) AND runtime — it idles silently when the
	// Settings toggle is off or Discord isn't running.
	pres := presence.New(prefs.Discord().AppID)
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

	// HiDPI: derive the auto UI scale from the desktop DPI (96 dpi =
	// 100%); the app snaps it to the settings step and the auto-scale
	// preference (default ON) decides whether it governs.
	if ddpi, _, _, err := sdl.GetDisplayDPI(0); err == nil && ddpi > 0 {
		app.SetDetectedUIScale(int(ddpi/baselineDPI*100 + 0.5))
	}

	if serverURL != "" {
		app.Connect(serverURL, serverURL)
	}

	// --- main loop: fixed-cadence update + single render pass ---
	last := time.Now()
	running := true
	for running {
		now := time.Now()
		dt := now.Sub(last)
		last = now
		// Pacing guard: after a stall (window drag, AV freeze, sleep
		// wake) an unbounded dt would dump the typewriter's whole backlog
		// and machine-gun its blips in one frame. Clamping keeps the
		// reveal cadence smooth; animation clocks just resume.
		if dt > maxFrameDelta {
			dt = maxFrameDelta
		}

		// Order matters: BeginFrame resets the per-frame input snapshot
		// that HandleEvent fills, so it must run before the event poll.
		// (Inverting these erased every click before the UI saw it.)
		uiCtx.BeginFrame(dt)
		for ev := sdl.PollEvent(); ev != nil; ev = sdl.PollEvent() {
			if _, ok := ev.(*sdl.QuitEvent); ok {
				running = false
			}
			uiCtx.HandleEvent(ev)
		}

		if window.GetFlags()&sdl.WINDOW_MINIMIZED != 0 {
			app.Background(dt) // keep the session alive, draw nothing
			time.Sleep(minimizedNap)
			continue
		}

		// Global UI scale: render at logical size, let the GPU scale the
		// whole frame; the kit unprojects the mouse through the same
		// factor, so every widget scales without per-element math.
		scale := float32(app.UIScale()) / 100
		_ = ren.SetScale(scale, scale)
		w, h := window.GetSize()
		lw := int32(float32(w) / scale)
		lh := int32(float32(h) / scale)
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		app.Frame(dt, lw, lh)
		ren.Present()

		if !vsync {
			if sleep := frameCap - time.Since(now); sleep > 0 {
				time.Sleep(sleep)
			}
		}
	}

	app.RememberOpenTabs() // restore-on-launch: snapshot tabs before the final flush (no-op when off)
	app.CloseTranscript()  // flush + close the detailed-log file (no-op when off/unopened)
	app.CloseJukebox()     // flush the global music library (no-op until loaded)
	_ = prefs.SaveNow()
	return nil
}
