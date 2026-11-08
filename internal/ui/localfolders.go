package ui

// The player's folders: which origins they answer for, and what a change or a re-check makes stale on screen.
// Render-thread code; mounting reads nothing, as the asset workers look at a folder only when it is first needed.

import (
	"strconv"
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

// recheckLocalFiles makes the next request for a missing asset look in the player's folders again, as on focus gain
// they may have just dropped files in. It reads no disk itself: the asset workers check each folder's modified time.
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
}

// CheckFoldersNow is the Settings button: re-check every folder on its next use, and reload what was drawn from them
// so changed files show.
// It doesn't rebuild the room: buildRoom re-arms the live-roster poll, whose OOC /gas is a known server flood-kick
// vector. It doesn't cancel queued work either, which would take live message work with it.
func (a *App) CheckFoldersNow() {
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
// It re-reads the active character's ini directly rather than rebuilding the room, for CheckFoldersNow's flood-kick
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
