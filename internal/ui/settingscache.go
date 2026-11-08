package ui

// The Settings → Assets "Cache" card: every cache AsyncAO keeps, one row each, with what it holds now, its one action
// and its settings folded under the row's arrow. Disk sizes are counted off the render thread when the card shows.

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The card's rows, in the order they draw; the values index settings.cacheOpen.
const (
	cacheRowArt = iota
	cacheRowFiles
	cacheRowDisk
	cacheRowMissing
	cacheRowLearned
	cacheRowCount
)

const (
	// cacheNameW is the column for a row's name; cacheActionW its action button; cacheFoldW the square arrow button.
	cacheNameW   int32 = 150
	cacheActionW int32 = 76
	cacheFoldW   int32 = 28
	// cacheSliderX and cacheSliderW place a folded setting's slider; cacheValueGap separates its value text.
	cacheSliderX  int32 = 210
	cacheSliderW  int32 = 230
	cacheValueGap int32 = 6
	cacheSliderH  int32 = 16
	// cacheButtonW is a folded row's buttons.
	cacheButtonW int32 = 170
	// cacheTexStepMiB is the art memory budget's -/+ step.
	cacheTexStepMiB = 16
	// cacheMeasureEvery spaces the disk counts; a clear measures again at once.
	cacheMeasureEvery = time.Minute
	// cacheSizeResCap: one count runs at a time (settings.cacheMeasuring), so 1 can never block.
	cacheSizeResCap = 1
	// percent turns a ratio into a percentage.
	percent = 100
	// bytesPerGiB scales byte counts for display, beside themeimport.go's bytesPerMiB and bytesPerKiB.
	bytesPerGiB = 1 << 30
)

// cacheSizes is what the disk cache holds.
type cacheSizes struct {
	bytes int64
	files int
}

// drawCacheSettings is the Cache card.
func (a *App) drawCacheSettings(y int32) int32 {
	a.measureCachesIfDue()
	sizes, measured := settings.cacheSizes, !settings.cacheMeasuredAt.IsZero()

	title := "Measuring the disk cache…"
	if measured {
		title = formatBytes(sizes.bytes) + " on disk"
	}
	limit := "No limit"
	if mib := a.d.Prefs.Load().DiskCacheBudgetMiBVal; mib > 0 {
		limit = "Limit " + diskCacheBudgetLabel(mib)
	}
	a.badgeHeading(y, title, limit, ColAccent)
	y += settHeadingH + settDetailGap + settDetailGap

	artHolds := "Not running"
	if a.d.Store != nil { // nil only in headless tests
		art := a.d.Store.Stats()
		artHolds = formatBytes(art.Bytes) + " of " + formatBytes(art.Budget) + "  ·  " + plural(art.Entries, "image")
	}
	y = a.drawCacheRow(y, cacheRowArt, "Art in memory", artHolds, a.drawArtMemorySettings)

	files := a.d.Manager.T2Stats()
	found := 0
	if total := files.Hits + files.Misses; total > 0 {
		found = int(files.Hits * percent / total)
	}
	y = a.drawCacheRow(y, cacheRowFiles, "Files in memory",
		formatBytes(files.Bytes)+" of "+formatBytes(files.Budget)+"  ·  "+strconv.Itoa(found)+"% of requests found here", nil)

	disk := "Measuring…"
	if measured {
		disk = formatBytes(sizes.bytes) + " in " + plural(sizes.files, "file")
	}
	if a.drawCacheAction(y, "Clear") {
		if err := a.d.Manager.ClearDisk(); err != nil {
			settings.statusLine = "Couldn't clear the disk cache: " + err.Error()
		} else {
			settings.statusLine = "Disk cache cleared."
		}
		settings.cacheMeasuredAt = time.Time{}
	}
	y = a.drawCacheRow(y, cacheRowDisk, "Disk", disk, a.drawDiskCacheSettings)

	if a.drawCacheAction(y, "Retry") {
		n := a.d.Manager.ForgetMissing()
		if a.d.Store != nil { // nil only in headless tests
			a.d.Store.ForgetMissing() // the render side's copy, or the viewport keeps drawing placeholders
		}
		settings.statusLine = "Asking again for " + plural(n, "missing asset") + "."
	}
	y = a.drawCacheRow(y, cacheRowMissing, "Missing assets",
		plural(int(a.d.Manager.Stats().Missing), "asset")+" not found this session", a.drawMissingSettings)

	if a.drawCacheAction(y, "Clear") {
		a.d.Prefs.ClearLearned()
		a.d.Resolver.InvalidateAll()
		settings.statusLine = "Learned formats cleared."
	}
	y = a.drawCacheRow(y, cacheRowLearned, "Learned formats",
		"for "+plural(learnedServerCount(a.d.Prefs.Load().LearnedFormats), "server"), a.drawLearnedSettings)

	return a.settingsDesc(a.formX, y+settDetailGap,
		"Clear the disk cache if a server updated its art and you still see the old version.", ColTextDim) + settSectionGap
}

// drawCacheAction draws a row's action button, if it has one, before the row itself; true when pressed.
func (a *App) drawCacheAction(y int32, label string) bool {
	r := sdl.Rect{X: a.formX + a.formW - cacheActionW, Y: y, W: cacheActionW, H: mountCtlH}
	return a.ctx.Button(r, label)
}

// drawCacheRow draws a row's name and what it holds, its fold arrow when it has settings, and the settings when open,
// indented under what it holds. Its action button, if any, was drawn by drawCacheAction.
func (a *App) drawCacheRow(y int32, row int, name, holds string, settingsFn func(int32) int32) int32 {
	c := a.ctx
	pad := a.formX
	// The action column is kept on every row, so the fold arrows line up whether a row has an action or not.
	right := pad + a.formW - cacheActionW - mountCtlGap
	if settingsFn != nil {
		fold := sdl.Rect{X: right - cacheFoldW, Y: y, W: cacheFoldW, H: mountCtlH}
		if a.moveButton(fold, settings.cacheOpen[row], true) {
			settings.cacheOpen[row] = !settings.cacheOpen[row]
		}
		c.Tooltip(fold, "Settings")
		right = fold.X - mountCtlGap
	}
	textY := y + (mountCtlH-settBadgeH)/2 + settBadgeDY
	c.Label(pad, textY, name, ColText)
	c.LabelClipped(pad+cacheNameW, textY, right-pad-cacheNameW-mountCtlGap, holds, ColTextDim)
	y += mountRowH
	if settingsFn != nil && settings.cacheOpen[row] {
		// The settings draw against the form, so the form is moved in for them.
		formX, formW := a.formX, a.formW
		a.formX, a.formW = formX+cacheNameW, formW-cacheNameW
		y = settingsFn(y) + settDetailGap
		a.formX, a.formW = formX, formW
	}
	return y
}

// drawArtMemorySettings is the texture memory budget.
func (a *App) drawArtMemorySettings(y int32) int32 {
	y = a.numSliderRow(y, "texbudget", "Memory for art:", config.TexBudgetMinMiB, config.TexBudgetMaxMiB, cacheTexStepMiB, config.TexBudgetDefaultMiB, func() int { return a.d.Prefs.Load().TexBudgetMiB() }, func(v int) {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.TexBudgetMiBVal = v })
	}, &settings.memBufs[4], " MiB", "Above 128 MiB may stutter on machines with little memory.")
	return a.settingsDesc(a.formX, y, "Applies after a restart.", ColTextDim)
}

// drawDiskCacheSettings is the disk cache's size limit and folder.
func (a *App) drawDiskCacheSettings(y int32) int32 {
	c := a.ctx
	pad := a.formX
	cur := a.d.Prefs.Load().DiskCacheBudgetMiBVal
	next := a.cacheSlider(y, "diskcachebudget", "Size limit:", cur, config.DiskCacheBudgetMaxMiB,
		"Past the limit the oldest files are deleted; far left keeps everything.")
	if next != 0 { // a coarse grid, and nothing below the floor
		next = max(next/diskCacheBudgetStepMiB*diskCacheBudgetStepMiB, config.DiskCacheBudgetMinMiB)
	}
	text := "No limit"
	if next != 0 {
		text = diskCacheBudgetLabel(next)
	}
	a.cacheSliderValue(y, text)
	if next != cur {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.DiskCacheBudgetMiBVal = next })
		a.d.Manager.SetDiskBudget(int64(next) * bytesPerMiB)
	}
	y += settRowH

	if c.Button(sdl.Rect{X: pad, Y: y, W: cacheButtonW, H: btnH}, "Open cache folder") {
		if root := a.d.Manager.DiskRoot(); root != "" {
			a.openInFileManager(root)
		}
	}
	return y + btnH
}

// drawMissingSettings is how long a missing asset is remembered before it is asked for again.
func (a *App) drawMissingSettings(y int32) int32 {
	cur := a.d.Prefs.Load().NotFoundTTLSecVal
	next := a.cacheSlider(y, "notfoundttl", "Ask again after:", cur, config.NotFoundTTLMaxSec,
		"Shorter finds newly uploaded art sooner; longer asks sparse servers less.")
	if next != 0 && next < config.NotFoundTTLMinSec {
		next = 0 // far left is the default
	}
	text := "5 min (default)"
	if next != 0 {
		text = formatWaitMs(next * int(time.Second/time.Millisecond))
	}
	a.cacheSliderValue(y, text)
	if next != cur {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.NotFoundTTLSecVal = next })
	}
	y += settRowH
	return a.settingsDesc(a.formX, y, "Applies after a restart.", ColTextDim)
}

// drawLearnedSettings is the learned formats' repair, export and import.
func (a *App) drawLearnedSettings(y int32) int32 {
	c := a.ctx
	pad := a.formX
	const fixLabel, exportLabel, importLabel = "Fix stuck images", "Export…", "Import…"
	r := sdl.Rect{X: pad, Y: y, W: cacheButtonW, H: btnH}
	// A server repack or stale CDN needs the learned format and the cached file dropped together; either alone
	// leaves the other stuck.
	if c.Button(r, fixLabel) {
		a.d.Prefs.ClearLearned()
		a.d.Resolver.InvalidateAll()
		settings.statusLine = "Cleared learned formats and the disk cache, so images load fresh."
		if err := a.d.Manager.ClearDisk(); err != nil {
			settings.statusLine = "Cleared learned formats, but the disk cache couldn't clear: " + err.Error()
		}
		settings.cacheMeasuredAt = time.Time{}
	}
	c.Tooltip(r, "Clears learned formats and the disk cache together, for art that is wrong everywhere.")
	a.noteSearchRow(fixLabel, y)
	r.X += cacheButtonW + settBtnGap
	if c.Button(r, exportLabel) {
		exportLearnedAsync(a.d)
	}
	c.Tooltip(r, "Writes learned formats beside the program, to carry to another computer.")
	r.X += cacheButtonW + settBtnGap
	if c.Button(r, importLabel) {
		importLearnedAsync(a.d)
	}
	c.Tooltip(r, "Merges in an exported file, so its servers load right from the first visit.")
	return y + btnH
}

// cacheSlider draws a folded setting's label and slider and returns the value the slider is at.
func (a *App) cacheSlider(y int32, id, label string, cur, maxVal int, tip string) int {
	c := a.ctx
	c.Label(a.formX, y+settBadgeDY, label, ColText)
	track := sdl.Rect{X: a.formX + cacheSliderX, Y: y + settDetailGap, W: cacheSliderW, H: cacheSliderH}
	c.Tooltip(track, tip)
	return int(value.Clamp(c.Slider(id, track, int32(cur), int32(maxVal)), 0, int32(maxVal)))
}

// cacheSliderValue draws the value beside a cacheSlider.
func (a *App) cacheSliderValue(y int32, text string) {
	a.ctx.Label(a.formX+cacheSliderX+cacheSliderW+cacheValueGap, y+settBadgeDY, text, ColTextDim)
}

// measureCachesIfDue lands a finished count and starts the next once the last is older than cacheMeasureEvery.
func (a *App) measureCachesIfDue() {
	select {
	case sizes := <-settings.cacheSizeRes:
		settings.cacheSizes = sizes
		settings.cacheMeasuredAt = time.Now()
		settings.cacheMeasuring = false
	default:
	}
	if settings.cacheMeasuring || (!settings.cacheMeasuredAt.IsZero() && time.Since(settings.cacheMeasuredAt) < cacheMeasureEvery) {
		return
	}
	settings.cacheMeasuring = true
	root := a.d.Manager.DiskRoot()
	go func() {
		var s cacheSizes
		s.bytes, s.files = folderSize(root)
		settings.cacheSizeRes <- s
		PushWake()
	}()
}

// folderSize totals the files under root; "" or an unreadable folder counts as empty.
func folderSize(root string) (bytes int64, files int) {
	if root == "" {
		return 0, 0
	}
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort count
		}
		if info, ierr := d.Info(); ierr == nil {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return bytes, files
}

// learnedServerCount counts the servers with learned formats; keys are host, separator, asset type. The count is kept
// for the map's size, so a frame that changed nothing builds no set.
func learnedServerCount(learned map[string][]string) int {
	if len(learned) == settings.learnedKeys && settings.learnedKeys > 0 {
		return settings.learnedServers
	}
	hosts := make(map[string]struct{}, len(learned))
	for key := range learned {
		host, _, _ := strings.Cut(key, config.LearnedKeySeparator)
		hosts[host] = struct{}{}
	}
	settings.learnedKeys, settings.learnedServers = len(learned), len(hosts)
	return len(hosts)
}

// formatBytes shows a byte count in the largest unit that keeps it above one.
func formatBytes(n int64) string {
	switch {
	case n >= bytesPerGiB:
		return fmt.Sprintf("%.1f GiB", float64(n)/bytesPerGiB)
	case n >= bytesPerMiB:
		return fmt.Sprintf("%.0f MiB", float64(n)/bytesPerMiB)
	case n >= bytesPerKiB:
		return fmt.Sprintf("%.0f KiB", float64(n)/bytesPerKiB)
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}
