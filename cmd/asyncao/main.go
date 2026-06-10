// Command asyncao is the AsyncAO client: a maximum-performance, zero-fallback
// Attorney Online 2 client. See PROMPT.md / docs/ARCHITECTURE.md.
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
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/ui"
)

const (
	// memoryBudgetBytes is the soft heap limit (PROMPT.md §1: < 256 MiB on
	// a 200-character server).
	memoryBudgetBytes = 256 << 20

	windowTitle    = "AsyncAO"
	windowWidth    = 1152
	windowHeight   = 864
	frameCap       = 16667 * time.Microsecond // ~60 FPS when vsync is off
	debugPprofAddr = "localhost:6060"
)

func main() {
	// SDL demands the main OS thread for the whole lifetime (PROMPT.md §12).
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

	if err := run(*flagServer, *flagMaster, *flagVsync); err != nil {
		log.Fatal(err)
	}
}

func run(serverURL, masterURL string, vsync bool) error {
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
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_AUDIO | sdl.INIT_EVENTS); err != nil {
		return err
	}
	defer sdl.Quit()
	if err := ttf.Init(); err != nil {
		return err
	}
	defer ttf.Quit()

	window, err := sdl.CreateWindow(windowTitle,
		sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		windowWidth, windowHeight, sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE)
	if err != nil {
		return err
	}
	defer window.Destroy()

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
	})

	viewport := render.NewViewport(store)
	audio := render.NewAudio(manager)
	defer audio.Close()

	uiCtx, err := ui.NewCtx(ren)
	if err != nil {
		return err
	}
	defer uiCtx.Destroy()

	app := ui.NewApp(uiCtx, ui.Deps{
		Prefs:     prefs,
		Resolver:  resolver,
		Manager:   manager,
		Pool:      pool,
		Client:    client,
		Store:     store,
		Viewport:  viewport,
		Pump:      nil, // set below (needs app for liveness)
		Audio:     audio,
		MasterURL: masterURL,
	})
	pump := render.NewPump(store, manager, app.IsLiveBase)
	app.SetPump(pump)

	if serverURL != "" {
		app.Connect(serverURL, serverURL)
	}

	// --- main loop: fixed-cadence update + single render pass ---
	last := time.Now()
	running := true
	for running {
		for ev := sdl.PollEvent(); ev != nil; ev = sdl.PollEvent() {
			switch e := ev.(type) {
			case *sdl.QuitEvent:
				running = false
			default:
				_ = e
			}
			uiCtx.HandleEvent(ev)
		}

		now := time.Now()
		dt := now.Sub(last)
		last = now

		w, h := window.GetSize()
		uiCtx.BeginFrame(dt)
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		app.Frame(dt, w, h)
		ren.Present()

		if !vsync {
			if sleep := frameCap - time.Since(now); sleep > 0 {
				time.Sleep(sleep)
			}
		}
	}

	_ = prefs.SaveNow()
	return nil
}
