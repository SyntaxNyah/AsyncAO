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

	dlDirPerm  = 0o755
	dlFilePerm = 0o644
)

// downloader is the opt-in downloader's runtime state. One job at a time;
// progress lands on a latest-wins channel drained each frame by pollDownload.
type downloader struct {
	active   bool
	label    string
	status   string
	cancel   context.CancelFunc
	progress chan dlProgress
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

// pollDownload drains the latest progress snapshot into the status line and
// flips active off when the job finishes. Runs every frame (Frame) so the
// status is current and "active" can't wedge after a job ends off-screen.
func (a *App) pollDownload() {
	if a.dl.progress == nil {
		return
	}
	select {
	case p := <-a.dl.progress:
		if p.done {
			a.dl.active = false
			a.dl.cancel = nil
			a.dl.status = fmt.Sprintf("Downloaded %s — %d files, %.1f MiB%s. (Add the downloads folder to local mounts to use offline.)",
				p.label, p.files, float64(p.bytes)/(1<<20), dlErrSuffix(p.errs))
		} else {
			a.dl.status = fmt.Sprintf("Downloading %s... %d files, %.1f MiB", p.label, p.files, float64(p.bytes)/(1<<20))
		}
	default:
	}
}

func dlErrSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d skipped)", n)
}

// cancelDownload stops an in-flight job (Cancel button, disconnect, quit).
func (a *App) cancelDownload() {
	if a.dl.cancel != nil {
		a.dl.cancel()
	}
}

// startCharDownload grabs one character's folder plus the sfx/blips its
// char.ini references — those live in sounds/general and sounds/blips,
// OUTSIDE the character folder, so a plain folder grab leaves it silent.
func (a *App) startCharDownload(char string) {
	dest := filepath.Join(downloadsRoot(), "characters", strings.ToLower(char))
	a.startDownload("character "+char, a.urls.CharFolder(char), dest, char)
}

// startBgDownload grabs one background's folder (no external sound deps).
func (a *App) startBgDownload(bg string) {
	dest := filepath.Join(downloadsRoot(), "background", strings.ToLower(bg))
	a.startDownload("background "+bg, a.urls.BackgroundFolder(bg), dest, "")
}

// startDownload launches one recursive folder download off-thread (rule
// §17.2 — disk writes are fine OFF the render thread). soundChar non-empty
// means "after the folder, resolve the char.ini sound dependencies".
func (a *App) startDownload(label, rootURL, destBase, soundChar string) {
	if !a.d.Prefs.CharDownloaderEnabled() {
		return
	}
	if a.dl.active {
		a.dl.status = "Already downloading " + a.dl.label + " — wait for it to finish."
		return
	}
	if a.d.Client == nil || a.urls.Origin() == "" {
		a.dl.status = "Downloader needs a connected server with an asset URL."
		return
	}
	if a.dl.progress == nil {
		a.dl.progress = make(chan dlProgress, 1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.dl.active = true
	a.dl.label = label
	a.dl.cancel = cancel
	a.dl.status = "Starting " + label + "..."

	// Snapshot everything the goroutine touches; it must not read App fields.
	job := &dlJob{label: label, base: downloadsRoot(), progress: a.dl.progress}
	client := a.d.Client
	urls := a.urls
	exts := audioProbeExts()
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
}

func (j *dlJob) overBudget() bool { return j.files >= dlMaxFiles || j.bytes >= dlMaxBytes }

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
	data, err := os.ReadFile(filepath.Join(j.base, "characters", strings.ToLower(char), "char.ini"))
	if err != nil {
		return // no char.ini downloaded → nothing to resolve
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
		if ctx.Err() != nil || j.overBudget() || n >= dlSoundCap {
			return
		}
		j.fetchSound(ctx, client, urls.SFX(name), filepath.Join(j.base, "sounds", "general"), strings.ToLower(name), exts)
		n++
		j.publish(false)
	}
	for name := range blips {
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
