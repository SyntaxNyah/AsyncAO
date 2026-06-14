package ui

// Built-in single-asset downloader (opt-in, off by default). It exists so a
// player who only wants one character or background can pull it straight from
// the server's asset host instead of scrambling a whole multi-GB pack for one
// file. It rides the autoindex listing (same discovery as the background
// picker) to walk a folder, and uses network.Client.Fetch directly — NOT the
// asset cache — so a bulk grab never evicts live T2/T3 entries.
//
// Scope is deliberately offline/local: downloads write a structured base
// (downloads/{characters,background,sounds}/...) usable via the "Read assets
// from local folders" mode (or rehearsal). Using a downloaded asset LIVE
// alongside streaming would need a local-overlay resolver — a separate change.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// Download bounds (rule §17.4 — a hostile or self-referential listing
	// must never run away on disk, bytes, or recursion).
	dlMaxFiles = 20000   // files per job
	dlMaxBytes = 2 << 30 // 2 GiB per job
	dlMaxDepth = 8       // folder recursion depth
	dlDirCap   = 8192    // entries parsed per directory listing
	dlSoundCap = 4096    // distinct char.ini sounds resolved per job
	dlQueueCap = 128     // grabs queued behind the active one (rule §17.4)

	// dlPausePoll is how often a paused worker re-checks the pause flag (and
	// cancellation) — short enough to feel instant on resume, idle otherwise.
	dlPausePoll = 150 * time.Millisecond

	dlDirPerm  = 0o755
	dlFilePerm = 0o644

	// downloadGlyph is the grid-cell download badge icon (↓ — same Arrows
	// block as the font-chain "→" the kit already renders).
	downloadGlyph = "↓"
	// charINIFileName is the per-character config the downloader re-reads to
	// resolve a character's sfx/blip dependencies.
	charINIFileName = "char.ini"
)

// downloader is the opt-in downloader's runtime state. One job runs at a time;
// further grabs wait in queue (v2). Progress lands on a latest-wins channel
// drained each frame by pollDownload, which also starts the next queued job.
type downloader struct {
	active   bool
	label    string // "character X" / "background Y" — shown on the indicator
	target   string // the raw asset name (slot.Name / bg) for per-cell marking
	status   string
	chip     string // cached indicator text (rebuilt only when progress changes)
	files    int    // latest progress (drives the floating indicator)
	bytes    int64
	cancel   context.CancelFunc
	progress chan dlProgress
	queue    []dlReq // grabs waiting behind the active one (render-thread only)
}

// dlReq is one queued grab — the args launchDownload needs. The queue is
// render-thread-only App state; the worker goroutine never touches it.
type dlReq struct {
	label, rootURL, destBase, soundChar, target string
}

// dlProgress is one progress snapshot from the worker goroutine.
type dlProgress struct {
	label string
	files int
	bytes int64
	errs  int
	done  bool
}

// downloadsRootMemo caches downloadsRoot's result: the settings screen calls
// it every frame, and os.Executable is a syscall + allocation. Only ever
// touched on the render thread (settings draw, startDownload) — no lock.
var downloadsRootMemo string

// downloadsRoot is the structured local base downloads write into. Point the
// "Read assets from local folders" mounts at it to use the files offline.
func downloadsRoot() string {
	if downloadsRootMemo != "" {
		return downloadsRootMemo
	}
	if exe, err := os.Executable(); err == nil {
		downloadsRootMemo = filepath.Join(filepath.Dir(exe), "downloads")
	} else {
		downloadsRootMemo = "downloads"
	}
	return downloadsRootMemo
}

// pollDownload drains the latest progress snapshot into the status line, flips
// active off when a job finishes, and starts the next queued grab when idle and
// not paused. Runs every frame (Frame). The single per-frame queue check covers
// done-advance AND resume, and a paused downloader simply doesn't advance it —
// one rule, no special resume path (advisor #2).
func (a *App) pollDownload() {
	if a.dl.progress != nil {
		select {
		case p := <-a.dl.progress:
			a.dl.files, a.dl.bytes = p.files, p.bytes
			if p.done {
				a.dl.active = false
				a.dl.cancel = nil
				if len(a.dl.queue) > 0 {
					a.dl.status = fmt.Sprintf("Downloaded %s — %d files, %.1f MiB%s. (%d queued)",
						p.label, p.files, float64(p.bytes)/(1<<20), dlErrSuffix(p.errs), len(a.dl.queue))
				} else {
					a.dl.status = fmt.Sprintf("Downloaded %s — %d files, %.1f MiB%s. (Add the downloads folder to local mounts to use offline.)",
						p.label, p.files, float64(p.bytes)/(1<<20), dlErrSuffix(p.errs))
				}
			} else {
				a.dl.status = fmt.Sprintf("Downloading %s... %d files, %.1f MiB", p.label, p.files, float64(p.bytes)/(1<<20))
				// Build the compact chip string here (only when progress changes),
				// so the per-frame indicator draw stays allocation-free.
				a.dl.chip = fmt.Sprintf("%s Downloading %s — %d files, %.1f MiB%s",
					downloadGlyph, p.label, p.files, float64(p.bytes)/(1<<20), dlQueued(len(a.dl.queue)))
			}
		default:
		}
	}
	// Advance the queue when idle and not paused (covers done-advance and
	// resume; pause stops it here).
	if !a.dl.active && !a.dlPaused.Load() && len(a.dl.queue) > 0 {
		next := a.dl.queue[0]
		a.dl.queue = append(a.dl.queue[:0], a.dl.queue[1:]...)
		a.launchDownload(next)
	}
}

func dlErrSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d skipped)", n)
}

// dlQueued is the " (+N queued)" suffix on the live indicator chip.
func dlQueued(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (+%d queued)", n)
}

// cancelDownload stops everything: the in-flight job AND the pending queue
// (Cancel button, disconnect, quit). Clearing the queue first means the done
// snapshot won't auto-start the next one.
func (a *App) cancelDownload() {
	a.dl.queue = a.dl.queue[:0]
	a.dlPaused.Store(false) // a paused worker wakes on ctx.Done(); clear so the next grab isn't silently paused
	if a.dl.cancel != nil {
		a.dl.cancel()
	}
}

// dlQueuedTarget reports whether an asset is waiting in the download queue (for
// the per-cell "queued" marker). Linear over the bounded queue (dlQueueCap),
// only for visible cells — cheap.
func (a *App) dlQueuedTarget(target string) bool {
	for _, q := range a.dl.queue {
		if q.target == target {
			return true
		}
	}
	return false
}

// drawDownloadBadge paints the ↓ download badge in a grid cell's bottom-right
// corner (with a hover hint) and reports a click. Shared by the char-select
// and background-picker grids when the opt-in downloader is on; it claims its
// own click so the cell's primary action (pick / select) never co-fires.
func (a *App) drawDownloadBadge(cell sdl.Rect, tip string) bool {
	c := a.ctx
	b := sdl.Rect{X: cell.X + cell.W - 22, Y: cell.Y + cell.H - 20, W: 20, H: 18}
	c.Fill(b, sdl.Color{R: 0, G: 0, B: 0, A: 200})
	c.Border(b, ColPanelHi)
	c.Label(b.X+6, b.Y+1, downloadGlyph, ColAccent)
	c.Tooltip(b, tip)
	return c.hovering(b) && c.clicked
}

// drawDownloadIndicator floats a live progress chip (top-center, just under
// the tab strip) while a download runs, so a grab is visible from any screen.
// Progress-only — Cancel lives in Settings (a button in this post-screen
// overlay would race clicks with the widgets drawn underneath it).
func (a *App) drawDownloadIndicator(w int32) {
	if !a.dl.active {
		return
	}
	c := a.ctx
	bw := c.TextWidth(a.dl.chip) + 20
	x := (w - bw) / 2
	if x < 0 {
		x = 0
	}
	chip := sdl.Rect{X: x, Y: tabBarH + 4, W: bw, H: btnH}
	c.Fill(chip, sdl.Color{R: ColPanel.R, G: ColPanel.G, B: ColPanel.B, A: 240})
	col := ColAccent
	if a.dlPaused.Load() {
		col = ColTextDim // dimmed while paused (progress isn't advancing)
	}
	c.Border(chip, col)
	c.Label(chip.X+10, chip.Y+5, a.dl.chip, col)
}

// startCharDownload grabs one character's folder plus the sfx/blips its
// char.ini references — those live in sounds/general and sounds/blips,
// OUTSIDE the character folder, so a plain folder grab leaves it silent.
func (a *App) startCharDownload(char string) {
	dest := filepath.Join(downloadsRoot(), "characters", strings.ToLower(char))
	a.enqueueDownload(dlReq{label: "character " + char, rootURL: a.urls.CharFolder(char), destBase: dest, soundChar: char, target: char})
}

// startBgDownload grabs one background's folder (no external sound deps).
func (a *App) startBgDownload(bg string) {
	dest := filepath.Join(downloadsRoot(), "background", strings.ToLower(bg))
	a.enqueueDownload(dlReq{label: "background " + bg, rootURL: a.urls.BackgroundFolder(bg), destBase: dest, target: bg})
}

// enqueueDownload starts a grab immediately when idle, else queues it behind
// the active one (v2). It refuses politely when the downloader is off, the
// server has no asset URL, the same target is already active/queued, or the
// queue is full — all render-thread state, no goroutine touches the queue.
func (a *App) enqueueDownload(req dlReq) {
	if !a.d.Prefs.CharDownloaderEnabled() {
		return
	}
	if a.d.Client == nil || a.urls.Origin() == "" {
		a.dl.status = "Downloader needs a connected server with an asset URL."
		return
	}
	if !a.dl.active {
		a.launchDownload(req)
		return
	}
	if a.dl.target == req.target {
		a.dl.status = req.label + " is already downloading."
		return
	}
	for _, q := range a.dl.queue {
		if q.target == req.target {
			a.dl.status = req.label + " is already queued."
			return
		}
	}
	if len(a.dl.queue) >= dlQueueCap {
		a.dl.status = "Download queue is full — let some finish first."
		return
	}
	a.dl.queue = append(a.dl.queue, req)
	a.dl.status = fmt.Sprintf("Queued %s (%d waiting).", req.label, len(a.dl.queue))
}

// launchDownload runs one recursive folder download off-thread (rule §17.2 —
// disk writes are fine OFF the render thread). Caller ensured the downloader is
// on, a server is connected, and nothing is active. soundChar non-empty means
// "after the folder, resolve the char.ini sound dependencies".
func (a *App) launchDownload(req dlReq) {
	if a.dl.progress == nil {
		a.dl.progress = make(chan dlProgress, 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.dl.active = true
	a.dl.label = req.label
	a.dl.target = req.target
	a.dl.files, a.dl.bytes = 0, 0
	a.dl.cancel = cancel
	a.dl.status = "Starting " + req.label + "..."
	a.dl.chip = downloadGlyph + " Starting " + req.label + "..." // seeded; progress refreshes it

	// Snapshot everything the goroutine touches; it must not read App fields.
	// dlPaused is the one exception — an atomic, safe to share (advisor #3).
	// The bandwidth cap is snapshot here (applies per job; a slider change hits
	// the next grab, which with a queue is soon enough).
	job := &dlJob{
		label: req.label, base: downloadsRoot(), progress: a.dl.progress,
		paused: &a.dlPaused, capBPS: int64(a.d.Prefs.DownloadCapKBps()) * 1024, start: time.Now(),
	}
	client := a.d.Client
	urls := a.urls
	exts := audioProbeExts()
	soundChar, rootURL, destBase := req.soundChar, req.rootURL, req.destBase
	go func() {
		defer cancel()
		job.walk(ctx, client, rootURL, destBase, 0)
		if soundChar != "" && ctx.Err() == nil {
			job.resolveCharSounds(ctx, client, urls, soundChar, exts)
		}
		job.publish(true)
	}()
}

// audioProbeExts is the FULL audio extension set (default + every legacy
// fallback), so a download finds the sound whatever format the server ships —
// independent of the user's current zero-fallback probe order.
func audioProbeExts() []string {
	exts := config.DefaultFormatOrder(config.TypeSFX)
	return append(exts, config.LegacyFallbackChain(config.TypeSFX)...)
}

// dlJob accumulates one download's progress. It never touches App state — the
// goroutine communicates only through the progress channel.
type dlJob struct {
	label    string
	base     string // downloadsRoot, for the containment check
	files    int
	bytes    int64
	errs     int
	progress chan dlProgress
	paused   *atomic.Bool // shared with the render thread (Pause button)
	capBPS   int64        // bandwidth cap in bytes/sec (0 = unlimited), snapshot at launch
	start    time.Time    // job start, for the cumulative average-rate throttle
}

// throttle holds the job to its average-rate bandwidth cap. client.Fetch
// returns whole files, so this paces BETWEEN files (a single big file fetches
// at full speed, then we sleep) — an average-rate limiter, not an instantaneous
// one. Cumulative form self-corrects; the wait selects on ctx.Done() so cancel
// never wedges it. No-op when uncapped (the default).
func (j *dlJob) throttle(ctx context.Context) {
	if j.capBPS <= 0 {
		return
	}
	expected := time.Duration(float64(j.bytes) / float64(j.capBPS) * float64(time.Second))
	if d := expected - time.Since(j.start); d > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
	}
}

func (j *dlJob) overBudget() bool { return j.files >= dlMaxFiles || j.bytes >= dlMaxBytes }

// waitWhilePaused blocks the worker while the user has it paused, waking on
// resume OR on cancellation (so Cancel / disconnect / quit never wedge a paused
// job). Called at the same per-file checkpoints as the cancel/budget tests.
func (j *dlJob) waitWhilePaused(ctx context.Context) {
	for j.paused != nil && j.paused.Load() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(dlPausePoll):
		}
	}
}

// walk recursively downloads a directory listing into destDir.
func (j *dlJob) walk(ctx context.Context, client *network.Client, dirURL, destDir string, depth int) {
	if depth > dlMaxDepth || j.overBudget() || ctx.Err() != nil {
		return
	}
	data, err := client.Fetch(ctx, dirURL)
	if err != nil {
		j.errs++
		return
	}
	for _, e := range parseAutoindexEntries(data, dlDirCap) {
		j.waitWhilePaused(ctx)
		if ctx.Err() != nil || j.overBudget() {
			return
		}
		child := dirURL + e.href // href is already URL-escaped; dirs keep the trailing /
		if e.dir {
			j.walk(ctx, client, child, filepath.Join(destDir, e.name), depth+1)
		} else if !j.saveURL(ctx, client, child, filepath.Join(destDir, e.name)) {
			j.errs++ // a listed file we couldn't fetch/write
		}
		j.publish(false)
	}
}

// resolveCharSounds reads the just-downloaded char.ini and pulls the
// sounds/general sfx and sounds/blips blips it names (the ones that live
// outside the character folder).
func (j *dlJob) resolveCharSounds(ctx context.Context, client *network.Client, urls courtroom.URLBuilder, char string, exts []string) {
	// Read char.ini fresh from the server so sound resolution never depends on
	// the folder walk's on-disk copy (path/case/partial-write quirks); the
	// just-walked disk copy is the fallback if the network read fails.
	data, err := client.Fetch(ctx, urls.CharFolder(char)+charINIFileName)
	if err != nil {
		data, err = os.ReadFile(filepath.Join(j.base, "characters", strings.ToLower(char), charINIFileName))
		if err != nil {
			return // no char.ini anywhere → nothing to resolve
		}
	}
	ini, err := courtroom.ParseCharINI(data)
	if err != nil {
		return
	}
	sfx := map[string]struct{}{}
	blips := map[string]struct{}{}
	if ini.Blips != "" {
		blips[ini.Blips] = struct{}{}
	}
	for _, e := range ini.Emotes {
		// "" / "0" / "1" are AO's "silent" sentinels (courtroom.go).
		if e.SFXName != "" && e.SFXName != "0" && e.SFXName != "1" {
			sfx[e.SFXName] = struct{}{}
		}
		if e.Blip != "" {
			blips[e.Blip] = struct{}{}
		}
	}
	n := 0
	for name := range sfx {
		j.waitWhilePaused(ctx)
		if ctx.Err() != nil || j.overBudget() || n >= dlSoundCap {
			return
		}
		j.fetchSound(ctx, client, urls.SFX(name), filepath.Join(j.base, "sounds", "general"), strings.ToLower(name), exts)
		n++
		j.publish(false)
	}
	for name := range blips {
		j.waitWhilePaused(ctx)
		if ctx.Err() != nil || j.overBudget() || n >= dlSoundCap {
			return
		}
		j.fetchSound(ctx, client, urls.Blip(name), filepath.Join(j.base, "sounds", "blips"), strings.ToLower(name), exts)
		n++
		j.publish(false)
	}
}

// fetchSound probes the audio formats for one named sound; the first that
// exists is saved with its extension (so the local resolver, which probes the
// same way, finds it). A sound missing in every format counts one skip.
func (j *dlJob) fetchSound(ctx context.Context, client *network.Client, urlBase, destDir, diskBase string, exts []string) {
	for _, ext := range exts {
		if ctx.Err() != nil || j.overBudget() {
			return
		}
		if j.saveURL(ctx, client, urlBase+ext, filepath.Join(destDir, diskBase+ext)) {
			return // first format that exists wins
		}
	}
	j.errs++
}

// saveURL fetches one URL and writes it under destPath, returning whether it
// landed. The containment check is defense in depth on top of
// cleanAutoindexEntry's "no .." rule: the cleaned path must stay strictly
// under base (trailing separator so a sibling like downloads-evil can't pass).
func (j *dlJob) saveURL(ctx context.Context, client *network.Client, url, destPath string) bool {
	clean := filepath.Clean(destPath)
	if !strings.HasPrefix(clean, filepath.Clean(j.base)+string(os.PathSeparator)) {
		return false
	}
	data, err := client.Fetch(ctx, url)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(clean), dlDirPerm); err != nil {
		return false
	}
	if err := os.WriteFile(clean, data, dlFilePerm); err != nil {
		return false
	}
	j.files++
	j.bytes += int64(len(data))
	j.throttle(ctx) // hold to the bandwidth cap (no-op when uncapped)
	return true
}

// publish sends a progress snapshot (latest-wins; dropped if the consumer
// hasn't drained the previous one — pollDownload reads the freshest each frame).
func (j *dlJob) publish(done bool) {
	if j.progress == nil {
		return
	}
	select {
	case j.progress <- dlProgress{label: j.label, files: j.files, bytes: j.bytes, errs: j.errs, done: done}:
	default:
	}
}
