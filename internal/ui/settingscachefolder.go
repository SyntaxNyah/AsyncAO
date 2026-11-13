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
	// cacheFolderBtns is how many buttons the folder row holds at most.
	cacheFolderBtns = 3
)

// cacheMoveResult is a finished move: dir is the folder the player chose ("" for the default) and moved whether the
// cache now lives there.
type cacheMoveResult struct {
	dir   string
	moved bool
	err   error
}

// drawCacheFolderSettings is the folder row, laid out like the size limit above it: the path where the slider sits and
// the row's buttons at the right. While a pick waits on the move question, the buttons answer it instead.
func (a *App) drawCacheFolderSettings(y int32) int32 {
	c := a.ctx
	if a.d.Disk == nil {
		return y
	}
	confirming := settings.cacheNewDir != "" || settings.cacheToDefault
	path := a.d.Disk.Root()
	switch {
	case confirming && settings.cacheToDefault:
		path = "Default folder"
	case confirming:
		path = cache.DiskRootIn(settings.cacheNewDir)
	}
	right := a.formX + a.formW
	btnsX := right - cacheFolderBtns*cacheActionW - (cacheFolderBtns-1)*mountCtlGap
	c.Label(a.formX, y+settBadgeDY, "Folder:", ColText)
	pathX := a.formX + cacheSliderX
	c.LabelClipped(pathX, y+settBadgeDY, btnsX-mountCtlGap-pathX, path, ColTextDim)
	c.Tooltip(sdl.Rect{X: pathX, Y: y, W: btnsX - mountCtlGap - pathX, H: mountCtlH}, path)

	// Buttons fill from the right, so a missing one leaves no hole between the path and the rest.
	btn := func(i int32) sdl.Rect {
		return sdl.Rect{X: right - (i+1)*cacheActionW - i*mountCtlGap, Y: y, W: cacheActionW, H: mountCtlH}
	}
	switch {
	case settings.cacheMoving:
	case confirming:
		if c.Button(btn(0), "Cancel") {
			settings.cacheNewDir, settings.cacheToDefault = "", false
		}
		drop := btn(1)
		if c.ButtonCol(drop, "Delete", ColPanel, ColPanelHi, ColDanger, ColDanger) {
			a.startCacheMove(settings.cacheNewDir, false)
		}
		c.Tooltip(drop, "Delete the files already cached; they are fetched again when needed")
		move := btn(2)
		if c.Button(move, "Move") {
			a.startCacheMove(settings.cacheNewDir, true)
		}
		c.Tooltip(move, "Move the files already cached to the new folder")
	default:
		i := int32(0)
		if a.d.Prefs.Load().CacheDir() != "" {
			def := btn(i)
			if c.Button(def, "Default") {
				settings.cacheToDefault = true
			}
			c.Tooltip(def, "Move the cache back to the system's cache folder")
			i++
		}
		change := btn(i)
		if c.Button(change, "Change…") {
			a.browseCacheFolder()
		}
		c.Tooltip(change, "Pick a folder; the cache goes in an AsyncAO folder inside it")
		open := btn(i + 1)
		if c.Button(open, "Open") {
			a.openInFileManager(a.d.Disk.Root())
		}
		c.Tooltip(open, "Open the cache folder")
	}
	y += settRowH

	switch {
	case settings.cacheMoving:
		return a.settingsDesc(a.formX, y, "Moving the cache… you can keep playing; files not moved yet are fetched again.", ColAccent)
	case confirming:
		return a.settingsDesc(a.formX, y, "Move the files already cached there, or delete them?", ColText)
	case settings.cacheStatus != "":
		col := ColTextDim
		if settings.cacheStatusBad {
			col = ColDanger
		}
		return a.settingsDesc(a.formX, y, settings.cacheStatus, col)
	}
	return y
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
