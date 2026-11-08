package ui

// The player's folders: which origins they answer for, and what a change or a re-check makes stale on screen.
// Render-thread code; mounting reads nothing, as the asset workers look at a folder only when it is first needed.

import (
	"path"
	"strconv"
	"strings"
	"time"
)

// localRecheckMinInterval stops rapid alt-tabbing from re-asking for every missing asset each time.
const localRecheckMinInterval = 2 * time.Second

// applyFolders mounts the player's folders for the local:// origin, which recordings, evidence images and the
// "only use my folders" mode read through, and for every live server's asset origin when they layer over streaming.
func (a *App) applyFolders() {
	if a.d.Manager == nil || a.d.Prefs == nil {
		return
	}
	_, mounts := a.localAssets()
	if len(mounts) == 0 {
		a.d.Manager.SetFolders(nil, nil)
		return
	}
	var origins []string
	if layered, _ := a.layeredAssets(); layered {
		// Each tab's URL builder origin: the raw ASS field often lacks its trailing slash, so it would never match.
		if o := a.urls.Origin(); o != "" {
			origins = append(origins, o)
		}
		for _, t := range a.tabs {
			if t == nil {
				continue
			}
			if o := t.state.urls.Origin(); o != "" {
				origins = append(origins, o)
			}
		}
	}
	a.d.Manager.SetFolders(mounts, origins)
}

// recheckLocalFiles runs on focus gain, as the player may have just added or edited files: the next request for a
// missing asset looks in the folders again, files already shown are checked for edits off-thread, and the Asset
// source list's folder checks run again. It reads no disk itself.
func (a *App) recheckLocalFiles() {
	if a.d.Manager == nil {
		return
	}
	if mounts, _, _ := a.d.Manager.FolderStatus(); len(mounts) == 0 {
		return
	}
	if time.Since(a.lastLocalRecheck) < localRecheckMinInterval {
		return
	}
	a.lastLocalRecheck = time.Now()
	a.d.Manager.RecheckFolders()
	if a.d.Store != nil { // nil only in headless tests
		a.d.Store.ForgetMissing() // or the viewport keeps drawing placeholders without asking again
	}
	staleFolderScans()
	a.startFolderChangeCheck()
}

// startFolderChangeCheck asks which files read from the folders changed on disk, one check at a time; the answer
// lands on folderChangeRes for pollFolderChanges.
func (a *App) startFolderChangeCheck() {
	if a.folderChangeRunning || a.folderChangeRes == nil {
		return
	}
	a.folderChangeRunning = true
	mgr := a.d.Manager
	go func() {
		a.folderChangeRes <- mgr.ChangedFolderFiles()
		PushWake()
	}()
}

// pollFolderChanges reloads what was drawn from files edited since they were read. Only those textures are dropped,
// so an alt-tab with nothing edited costs nothing.
func (a *App) pollFolderChanges() {
	select {
	case changed := <-a.folderChangeRes:
		a.folderChangeRunning = false
		a.reloadChangedFiles(changed)
	default:
	}
}

// reloadChangedFiles drops the textures drawn from changed files, and re-reads char.ini files if one of them changed.
func (a *App) reloadChangedFiles(changed []string) {
	if len(changed) == 0 {
		return
	}
	bases := make(map[string]bool, len(changed))
	iniChanged := false
	for _, url := range changed {
		bases[url] = true
		bases[strings.TrimSuffix(url, path.Ext(url))] = true // chain assets are held under the base without extension
		iniChanged = iniChanged || strings.EqualFold(path.Base(url), charINIFileName)
	}
	if iniChanged {
		a.forgetParsedINIs()
	}
	if a.d.Store == nil {
		return
	}
	a.d.Store.ForgetFailed() // a file that failed to decode may be fixed now
	n := a.d.Store.RemoveWhere(func(base string) bool { return bases[base] })
	a.pushDebug("local folders: " + strconv.Itoa(len(changed)) + " file(s) changed on disk, " + strconv.Itoa(n) + " texture(s) reloaded")
}

// reloadFromFolders re-checks every folder on its next use and reloads everything drawn from them, for when which
// folder wins may have changed.
// It doesn't rebuild the room: buildRoom re-arms the live-roster poll, whose OOC /gas is a known server flood-kick
// vector. It doesn't cancel queued work either, which would take live message work with it.
func (a *App) reloadFromFolders() {
	if a.d.Manager == nil {
		return
	}
	a.d.Manager.RecheckFolders()
	a.forgetParsedINIs()
	if a.d.Store == nil {
		return
	}
	// A missing or undecodable server copy is what a folder file is added to supply.
	a.d.Store.ForgetMissing()
	a.d.Store.ForgetFailed()
	if n := a.d.Store.RemoveWhere(a.d.Manager.PackHeld); n > 0 {
		a.pushDebug("local folders: dropped " + strconv.Itoa(n) + " texture(s) drawn from them to reload")
	}
}

// forgetParsedINIs drops everything derived from a char.ini so a source change shows without a reconnect; those parsed
// copies are in URL-keyed maps no texture invalidation touches.
// It re-reads the active character's ini directly rather than rebuilding the room, for reloadFromFolders' flood-kick
// reason.
func (a *App) forgetParsedINIs() {
	a.charMetaCache = nil // remote speakers: refetched on their next message
	// Or the next hover over the same character is dropped as already showing, keeping the old emote list.
	a.previewChar, a.previewAnims, a.previewLabels = "", nil, nil
	a.previewEmoteIdx = 0
	a.iniWarmed = "" // the hover warm's latch, same reason
	if a.sess != nil && a.activeCharName() != "" {
		a.loadCharINI() // the emote strip in front of the user, reloaded in place
	}
}
