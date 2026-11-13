package ui

// Each folder in the Asset source list is scanned once, off the render thread, so its row can show what it holds or
// that it isn't a base. One scan runs at a time, and results are kept only for folders still listed (hard rule 4).

import (
	"path/filepath"
	"slices"
	"strings"
)

const (
	// folderScanResCap: one scan runs at a time (settings.folderScanRunning), so 1 can never block.
	folderScanResCap = 1
	// mountDropResCap holds one resolved drop; the next frame's poll empties it.
	mountDropResCap = 1
)

// checkFolders lands a finished scan, forgets folders no longer listed and starts the next missing scan.
func checkFolders(mounts, auto []string) {
	listed := func(path string) bool { return slices.Contains(mounts, path) || slices.Contains(auto, path) }
	select {
	case res := <-settings.folderScanRes:
		settings.folderScanRunning = false
		if listed(res.path) {
			settings.folderScans[res.path] = res
		}
	default:
	}
	gen := settings.folderScanGen
	for path := range settings.folderScans {
		if !listed(path) {
			delete(settings.folderScans, path)
		}
	}
	for path := range settings.folderOpen {
		if !listed(path) {
			delete(settings.folderOpen, path)
		}
	}
	if !settings.folderScanRunning {
		if path, ok := firstUnscanned(mounts, auto); ok {
			settings.folderScanRunning = true
			go func() {
				res := scanBaseFolder(path)
				res.gen = gen
				settings.folderScanRes <- res
				PushWake()
			}()
		}
	}
}

// firstUnscanned is the first listed folder with no report, or one older than the last staleFolderScans.
func firstUnscanned(mounts, auto []string) (string, bool) {
	for _, list := range [][]string{mounts, auto} {
		for _, path := range list {
			if scan, ok := settings.folderScans[path]; !ok || scan.gen != settings.folderScanGen {
				return path, true
			}
		}
	}
	return "", false
}

// forgetFolderScans drops every report, so each folder shows as unchecked until scanned again.
func forgetFolderScans() { clear(settings.folderScans) }

// staleFolderScans makes every listed folder scan again, keeping its old report on screen until the new one lands.
func staleFolderScans() { settings.folderScanGen++ }

// replaceMount swaps the mount at i for path, keeping its place in the search order.
func (a *App) replaceMount(i int, path string) {
	_, mounts := a.d.Prefs.LocalAssets()
	if i < 0 || i >= len(mounts) {
		return
	}
	next := slices.Clone(mounts)
	next[i] = path
	a.saveMounts(next)
}

// resolveDroppedMount turns a drop on the Assets tab into a folder off-thread, as a stat on a network share must not
// stall the frame. A dropped file means its folder, except a .zip, which is a pack itself.
func resolveDroppedMount(path string) {
	go func() {
		if !strings.EqualFold(filepath.Ext(path), ".zip") && !isDir(path) {
			path = filepath.Dir(path)
		}
		settings.mountDropRes <- path
		PushWake()
	}()
}

// pollMountDrop adds a resolved drop to the list.
func (a *App) pollMountDrop() {
	select {
	case path := <-settings.mountDropRes:
		_, mounts := a.d.Prefs.LocalAssets()
		if msg, _ := a.addSource("", path, mounts); msg != "" {
			settings.statusLine = msg
		}
	default:
	}
}
