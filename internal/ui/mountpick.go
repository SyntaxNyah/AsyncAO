package ui

import (
	"slices"
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// mountPickBtnX and mountPickBtnW place the picker right of the Asset source Add button.
	mountPickBtnX = 548
	mountPickBtnW = 130
)

// mountPick is what the Asset source folder dialog returned.
type mountPick struct {
	paths []string
	err   error
}

// mountPickLabel names the button for what the platform's dialog can do.
func mountPickLabel(many bool) string {
	if many {
		return "Add folders…"
	}
	return "Add folder…"
}

// browseForMounts shows the folder dialog; the result lands on mountPickRes for pollMountPick.
func (a *App) browseForMounts(many bool) {
	if settings.mountPickBusy {
		settings.statusLine = "The folder picker is already open — finish or close it first (Alt-Tab if you don't see it)."
		return
	}
	settings.mountPickBusy = true
	a.d.Desktop.PickFolders(sdl.DialogOptions{Window: a.ctx.win, Title: mountPickLabel(many), Many: many},
		func(paths []string, err error) {
			// mountPickBusy leaves the channel's one place free, so SDL's callback never blocks here.
			settings.mountPickRes <- mountPick{paths: paths, err: err}
			PushWake()
		})
}

// pollMountPick adds the chosen folders to the mount list on the render thread.
func (a *App) pollMountPick() {
	select {
	case pick := <-settings.mountPickRes:
		settings.mountPickBusy = false
		if pick.err != nil {
			settings.statusLine = "Couldn't open the folder picker — type the path instead."
			return
		}
		if len(pick.paths) == 0 {
			return // cancelled
		}
		_, mounts := a.d.Prefs.LocalAssets()
		next, added, refused := mergeMounts(mounts, pick.paths, config.LocalMountCap)
		if added > 0 {
			a.d.Prefs.SetLocalAssets(a.assetSourceMode() == assetSrcLocal, next)
			a.rebuildAssetOrigin()
		}
		settings.statusLine = "Added " + strconv.Itoa(added) + " to your folders."
		if refused > 0 {
			settings.statusLine += " " + strconv.Itoa(refused) + " didn't fit: the limit is " + strconv.Itoa(config.LocalMountCap) + "."
		}
	default:
	}
}

// mergeMounts appends picked after mounts in order, skipping folders already listed and stopping at limit.
func mergeMounts(mounts, picked []string, limit int) (next []string, added, refused int) {
	next = slices.Clone(mounts)
	for _, path := range picked {
		switch {
		case slices.Contains(next, path):
		case len(next) >= limit:
			refused++
		default:
			next = append(next, path)
			added++
		}
	}
	return next, added, refused
}
