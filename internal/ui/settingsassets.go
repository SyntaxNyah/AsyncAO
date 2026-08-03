package ui

// The Settings → Assets "Asset source" panel: where the user chooses between
// streaming, layering their own folders over the stream, and the legacy
// folders-only mode, and where they manage the folders and .zip packs those two
// modes read.
//
// Copy note: two things MUST stay spelled out here rather than only in the docs —
// that .zip packs work as mounts, and that the user's own files WIN over the
// server's at the same path. Both are things people otherwise discover by being
// confused.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Asset-source modes, as the three-way control presents them. The STORAGE is two
// independent bools (see config.LayeredAssets) so no saved prefs.json changes
// meaning on upgrade; this enum exists only for the UI.
const (
	assetSrcStream  = iota // stream everything from the server (the default)
	assetSrcLayered        // read the user's folders first, then stream the rest
	assetSrcLocal          // never stream (legacy servers with no asset URL)
)

// assetSourceMode collapses the two stored bools into the mode the radio shows.
// Local-only wins when both are somehow set, matching config.LayeredAssets.
func (a *App) assetSourceMode() int {
	enabled, _ := a.d.Prefs.LocalAssets()
	if enabled {
		return assetSrcLocal
	}
	if a.d.Prefs.LocalAssetsLayered {
		return assetSrcLayered
	}
	return assetSrcStream
}

// setAssetSourceMode applies a radio selection, keeping the two stored bools
// mutually exclusive so the ambiguous both-set combination is never persisted.
func (a *App) setAssetSourceMode(mode int, mounts []string) {
	switch mode {
	case assetSrcLocal:
		a.d.Prefs.SetLayeredAssets(false)
		a.d.Prefs.SetLocalAssets(true, mounts)
	case assetSrcLayered:
		a.d.Prefs.SetLocalAssets(false, mounts)
		a.d.Prefs.SetLayeredAssets(true)
	default:
		a.d.Prefs.SetLayeredAssets(false)
		a.d.Prefs.SetLocalAssets(false, mounts)
	}
	// rebuildAssetOrigin re-points the URL builder AND (deferred) reconciles the
	// mount layer, so one call covers both halves of a mode change.
	a.rebuildAssetOrigin()
}

// drawAssetSourceSettings renders the whole panel and returns the new y.
func (a *App) drawAssetSourceSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX

	_, mounts := a.d.Prefs.LocalAssets()
	mode := a.assetSourceMode()
	haveMounts := len(mounts) > 0

	// The three-way choice. Rows are drawn MUTED (greyed, inert) rather than
	// hidden when no folder is configured yet, so the option is still visible and
	// still findable by the settings search — a hidden row is never registered.
	for _, opt := range []struct {
		mode  int
		label string
	}{
		{assetSrcStream, "Stream everything from the server"},
		{assetSrcLayered, "Use my folders first, then stream the rest"},
		{assetSrcLocal, "Only use my folders — never stream (for servers with no asset URL)"},
	} {
		on := mode == opt.mode
		enabledRow := opt.mode == assetSrcStream || haveMounts
		if !enabledRow {
			// Muted: drawn so the user can SEE what adding a folder would unlock.
			c.Checkbox(pad, y, opt.label, on)
			c.Fill(sdl.Rect{X: pad, Y: y, W: a.formW, H: 24}, sdl.Color{R: 0, G: 0, B: 0, A: 120})
		} else if next := c.Checkbox(pad, y, opt.label, on); next != on && !on {
			// Radio semantics over checkbox rows: selecting an unchecked row moves
			// the mode; clicking the already-selected row does nothing (a radio group
			// has no "none" state).
			a.setAssetSourceMode(opt.mode, mounts)
		}
		a.noteSearchRow(opt.label, y)
		y += 26
	}
	y += 4

	switch mode {
	case assetSrcLayered:
		y = a.settingsDesc(pad, y, "Files in your folders REPLACE the server's copies, at the same paths the server uses. Anything you don't have still streams from the server. Your folders are never uploaded and never written to the disk cache.", ColTextDim)
	case assetSrcLocal:
		y = a.settingsDesc(pad, y, "Nothing is streamed. Every asset must come from your folders — for legacy servers that provide no asset URL at all.", ColTextDim)
	default:
		y = a.settingsDesc(pad, y, "Every asset comes from the server's asset URL. Add folders below and pick \"Use my folders first\" to layer your own content over it.", ColTextDim)
	}
	y += 6

	// --- the mount list -----------------------------------------------------------
	c.Label(pad, y+4, "Folders and .zip packs (searched in order — the first one with the file wins)", ColText)
	y += 24
	y = a.settingsDesc(pad, y, "A mount can be a folder or a .zip pack. Paths inside must match the server's layout (characters/, background/, sounds/, evidence/).", ColTextDim)
	y += 4

	c.Label(pad, y+4, "Folder or .zip:", ColText)
	settings.mountInput, _ = c.TextField("mount", sdl.Rect{X: pad + 130, Y: y, W: 320, H: fieldH}, settings.mountInput, `C:\AO2\base, or C:\AO2\mypack.zip`)
	if c.Button(sdl.Rect{X: pad + 460, Y: y, W: 80, H: btnH}, "Add") && strings.TrimSpace(settings.mountInput) != "" {
		next := append(append([]string{}, mounts...), strings.TrimSpace(settings.mountInput))
		if a.d.Prefs.SetLocalAssets(a.assetSourceMode() == assetSrcLocal, next) {
			settings.mountInput = ""
			a.rebuildAssetOrigin()
		} else {
			settings.statusLine = "Up to " + strconv.Itoa(config.LocalMountCap) + " folders. Remove one first."
		}
	}
	a.noteSearchRow("Folder or .zip", y)
	y += 32

	for i, m := range mounts {
		kind := "folder"
		if strings.EqualFold(filepathExt(m), ".zip") {
			kind = "zip"
		}
		c.LabelClipped(pad+20, y+4, a.formW-190, fmt.Sprintf("%d. %s", i+1, m), ColText)
		c.Label(a.formX+a.formW-170, y+4, kind, ColTextDim)
		if c.Button(sdl.Rect{X: a.formX + a.formW - 90, Y: y, W: 90, H: 24}, "Remove") {
			next := append(append([]string{}, mounts[:i]...), mounts[i+1:]...)
			// Shrinking always succeeds — the cap only refuses growth, so a user who
			// somehow has more than the cap can still prune back under it.
			a.d.Prefs.SetLocalAssets(a.assetSourceMode() == assetSrcLocal, next)
			a.rebuildAssetOrigin()
			break
		}
		y += 28
	}

	if haveMounts {
		y += 4
		y = a.drawMountIndexStatus(y)
		if c.Button(sdl.Rect{X: pad, Y: y, W: 150, H: btnH}, "Rescan folders") {
			a.RescanLocalPacks()
			settings.statusLine = "Rescanning your folders…"
		}
		a.noteSearchRow("Rescan folders", y)
		y += btnH + 4
		y = a.settingsDesc(pad, y, "Rescan after adding or changing files. Art your folders cover reloads right away; everything else updates as it reloads. Rescan also re-tries files that failed to load last time.", ColTextDim)
	}

	// The nudge: folders configured but not actually being used is a silent
	// dead end otherwise — the user sees their pack listed and nothing happening.
	if haveMounts && mode == assetSrcStream {
		y += 4
		y = a.settingsDesc(pad, y, "You have folders configured but they aren't being used. Pick \"Use my folders first\" above to layer them over the server.", ColTierYellow)
	}
	return y + 10
}

// drawMountIndexStatus reports what the index actually holds, plus anything that
// went wrong. Every number here comes from real state — nothing is estimated.
func (a *App) drawMountIndexStatus(y int32) int32 {
	c := a.ctx
	pad := a.formX

	switch {
	case a.mountIndexing:
		c.Label(pad, y+4, "Indexing your folders…", ColTextDim)
	case a.mountIndex != nil:
		files, mounts, truncated := a.mountIndex.Stats()
		line := fmt.Sprintf("Indexed %d file(s) in %d mount(s).", files, mounts)
		if truncated {
			line += " Size cap reached — the rest streams from the server."
		}
		// Served-this-session, not a "shadow count": the client never enumerates the
		// server's files, so how many of these REPLACE a server asset is unknowable
		// without crawling it. This number is one we actually have.
		if n := a.d.Manager.Stats().MountFetches; n > 0 {
			line += fmt.Sprintf(" %d asset(s) served from your folders this session.", n)
		}
		c.Label(pad, y+4, line, ColTextDim)
	default:
		c.Label(pad, y+4, "Not indexed yet.", ColTextDim)
	}
	y += 22

	if n := a.d.Manager.Stats().PackQuarantined; n > 0 {
		c.Label(pad, y+4, fmt.Sprintf("%d file(s) in your folders couldn't be read — those are streaming from the server instead. Fix them and press Rescan folders.", n), ColTierYellow)
		y += 22
	}
	for _, err := range a.mountIndexErrs {
		c.LabelClipped(pad, y+4, a.formW, "Mount unusable: "+err.Error(), ColDanger)
		y += 22
	}
	return y
}

// filepathExt is filepath.Ext without pulling the import into this file's
// namespace for one call — it only ever sees a user-typed mount string.
func filepathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && !strings.ContainsAny(p[i:], `/\`) {
		return p[i:]
	}
	return ""
}
