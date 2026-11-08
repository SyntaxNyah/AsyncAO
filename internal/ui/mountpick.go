package ui

import (
	"slices"
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// dialogPick is what a file or folder dialog returned.
type dialogPick struct {
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

// packPickLabel is the button that picks .zip packs.
const packPickLabel = "Add packs…"

// packFilters limits the pack dialog to .zip files, the one kind of pack AsyncAO mounts.
var packFilters = []sdl.DialogFilter{{Name: ".zip packs", Pattern: "zip"}}

// browseForMounts shows the folder dialog; the result lands on mountPickRes for pollMountPick.
func (a *App) browseForMounts(many bool) {
	if a.mountPickOpen() {
		a.d.Desktop.PickFolders(sdl.DialogOptions{Window: a.ctx.win, Title: mountPickLabel(many), Many: many}, deliverMountPick)
	}
}

// browseForPacks shows the file dialog for .zip packs; the picks land on mountPickRes like picked folders.
func (a *App) browseForPacks() {
	if a.mountPickOpen() {
		a.d.Desktop.PickFiles(sdl.DialogOptions{Window: a.ctx.win, Title: "Add .zip packs", Many: true, Filters: packFilters}, deliverMountPick)
	}
}

// mountPickOpen claims the one folder-or-pack dialog, or says it is already open.
func (a *App) mountPickOpen() bool {
	if settings.mountPickBusy {
		settings.statusLine = "A picker is already open, possibly behind this window."
		return false
	}
	settings.mountPickBusy = true
	return true
}

// deliverMountPick hands a dialog's answer to pollMountPick; mountPickBusy leaves the channel's one place free, so
// SDL's callback never blocks here.
func deliverMountPick(paths []string, err error) {
	settings.mountPickRes <- dialogPick{paths: paths, err: err}
	PushWake()
}

// pollMountPick adds the chosen folders or packs to the mount list on the render thread.
func (a *App) pollMountPick() {
	select {
	case pick := <-settings.mountPickRes:
		settings.mountPickBusy = false
		if pick.err != nil {
			settings.statusLine = "Couldn't open the folder picker, so type the path instead."
			return
		}
		if len(pick.paths) == 0 {
			return // cancelled
		}
		_, mounts := a.d.Prefs.LocalAssets()
		next, added, refused := mergeMounts(mounts, pick.paths, config.LocalMountCap)
		if added > 0 {
			a.saveMounts(next)
		}
		settings.statusLine = "Added " + strconv.Itoa(added) + " to your folders."
		if refused > 0 {
			settings.statusLine = "Added " + strconv.Itoa(added) + " to your folders, but " + strconv.Itoa(refused) +
				" didn't fit under the limit of " + strconv.Itoa(config.LocalMountCap) + "."
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
