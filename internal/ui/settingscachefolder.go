package ui

// Settings ▸ Assets ▸ Cache ▸ Disk cache: where the disk cache lives. The cache moves while the client runs
// (cache.DiskCache.Relocate); the player picks whether the files already cached come along or are deleted.

import (
	"errors"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// cacheMoveBtnW sizes the move confirmation's buttons.
	cacheMoveBtnW int32 = 150
	// cacheFolderGap separates the folder row's buttons.
	cacheFolderGap int32 = 8
	// cacheLabelPadY lines a label up with the controls in its row.
	cacheLabelPadY int32 = 4
)

// cacheMoveResult is a finished move: dir is the folder the player chose ("" for the default) and moved whether the
// cache now lives there.
type cacheMoveResult struct {
	dir   string
	moved bool
	err   error
}

// drawCacheFolderSettings is the folder row, its move confirmation, and the status of a move under way or done.
func (a *App) drawCacheFolderSettings(y int32) int32 {
	c := a.ctx
	pad := a.formX
	if a.d.Disk == nil {
		return y
	}
	c.Label(pad, y+cacheLabelPadY, "Folder:", ColText)
	c.LabelClipped(pad+cacheNameW/2, y+cacheLabelPadY, cacheSliderX+cacheSliderW-cacheNameW/2, a.d.Disk.Root(), ColTextDim)
	y += settRowH
	switch {
	case settings.cacheMoving:
		y = a.settingsDesc(pad, y, "Moving the cache… you can keep playing; files not moved yet are fetched again.", ColAccent)
	case settings.cacheNewDir != "" || settings.cacheToDefault:
		y = a.drawCacheMoveConfirm(y)
	default:
		x := pad
		change := sdl.Rect{X: x, Y: y, W: cacheButtonW, H: btnH}
		if c.Button(change, "Change…") {
			a.browseCacheFolder()
		}
		c.Tooltip(change, "Pick a folder; the cache goes in an AsyncAO folder inside it")
		x += cacheButtonW + cacheFolderGap
		if a.d.Prefs.Load().CacheDir() != "" {
			def := sdl.Rect{X: x, Y: y, W: cacheButtonW, H: btnH}
			if c.Button(def, "Use default") {
				settings.cacheToDefault = true
			}
			c.Tooltip(def, "Back to the system's cache folder")
		}
		y += btnH + cacheFolderGap
	}
	if settings.cacheStatus != "" {
		col := ColTextDim
		if settings.cacheStatusBad {
			col = ColDanger
		}
		y = a.settingsDesc(pad, y, settings.cacheStatus, col)
	}
	return y
}

// drawCacheMoveConfirm asks whether the files already cached move to the new folder or are deleted.
func (a *App) drawCacheMoveConfirm(y int32) int32 {
	c := a.ctx
	pad := a.formX
	target := settings.cacheNewDir
	if settings.cacheToDefault {
		target = "the default folder"
	}
	y = a.settingsDesc(pad, y, "Move the files already cached to "+target+"? If you don't, they are deleted.", ColText)
	x := pad
	move := c.Button(sdl.Rect{X: x, Y: y, W: cacheMoveBtnW, H: btnH}, "Move them")
	x += cacheMoveBtnW + cacheFolderGap
	drop := c.ButtonCol(sdl.Rect{X: x, Y: y, W: cacheMoveBtnW, H: btnH}, "Delete them", ColPanel, ColPanelHi, ColDanger, ColDanger)
	x += cacheMoveBtnW + cacheFolderGap
	cancel := c.Button(sdl.Rect{X: x, Y: y, W: cacheMoveBtnW, H: btnH}, "Cancel")
	switch {
	case move || drop:
		a.startCacheMove(settings.cacheNewDir, move)
	case cancel:
		settings.cacheNewDir, settings.cacheToDefault = "", false
	}
	return y + btnH + cacheFolderGap
}

// browseCacheFolder opens the folder picker; the pick lands in pollCacheFolder.
func (a *App) browseCacheFolder() {
	if settings.cachePickBusy {
		settings.cacheStatus, settings.cacheStatusBad = "The folder picker is already open, possibly behind this window.", false
		return
	}
	settings.cachePickBusy = true
	ch := settings.cachePickRes
	a.d.Desktop.PickFolders(sdl.DialogOptions{Window: a.ctx.win, Title: "Pick a folder for the cache"}, func(paths []string, err error) {
		// cachePickBusy keeps the channel's one place free, so SDL's callback never blocks here.
		ch <- dialogPick{paths: paths, err: err}
		PushWake()
	})
}

// startCacheMove moves the cache off the render thread (it walks and copies files, hard rule 2).
func (a *App) startCacheMove(dir string, move bool) {
	root, err := cache.DefaultDiskRoot()
	if dir != "" {
		root, err = cache.DiskRootIn(dir), nil
	}
	settings.cacheNewDir, settings.cacheToDefault = "", false
	if err != nil {
		settings.cacheStatus, settings.cacheStatusBad = "Couldn't find the default cache folder: "+err.Error(), true
		return
	}
	settings.cacheMoving = true
	settings.cacheStatus = ""
	disk, ch := a.d.Disk, settings.cacheMoveRes
	go func() {
		err := disk.Relocate(root, move)
		ch <- cacheMoveResult{dir: dir, moved: disk.Root() == root, err: err}
		PushWake()
	}()
}

// pollCacheFolder lands a folder pick and a finished move. It runs every frame, so a move finishing after Settings
// closed is still saved.
func (a *App) pollCacheFolder() {
	select {
	case pick := <-settings.cachePickRes:
		settings.cachePickBusy = false
		if pick.err == nil && len(pick.paths) > 0 {
			settings.cacheNewDir = pick.paths[0]
		}
	case res := <-settings.cacheMoveRes:
		settings.cacheMoving = false
		if res.moved {
			dir := res.dir
			a.d.Prefs.Update(func(sn *config.Settings) { sn.CacheDirVal = dir })
			settings.cacheMeasuredAt = time.Time{} // count the new folder now
		}
		switch {
		case errors.Is(res.err, cache.ErrRelocateNested):
			settings.cacheStatus, settings.cacheStatusBad = "That folder is inside the cache, or the cache is inside it. Pick another one.", true
		case res.err != nil && res.moved:
			settings.cacheStatus, settings.cacheStatusBad = "The cache moved, but some old files couldn't be moved or deleted: "+res.err.Error(), true
		case res.err != nil:
			settings.cacheStatus, settings.cacheStatusBad = "The cache stayed where it was: "+res.err.Error(), true
		default:
			settings.cacheStatus, settings.cacheStatusBad = "The cache now lives in "+a.d.Disk.Root(), false
		}
	default:
	}
}
