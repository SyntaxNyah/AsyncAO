package ui

// The Settings → Assets "Asset source" panel: choose between streaming, layering the user's own folders over the
// stream, and the legacy folders-only mode, and manage the folders and .zip packs those modes read.
// Two things must stay spelled out here rather than only in the docs: .zip packs work as mounts, and the user's
// own files win over the server's at the same path.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Asset-source modes as the three-way control presents them. Storage is two independent bools (config.LayeredAssets)
// so no saved prefs.json changes meaning; this enum is UI only.
const (
	assetSrcStream  = iota // stream everything from the server (the default)
	assetSrcLayered        // read the user's folders first, then stream the rest
	assetSrcLocal          // never stream (legacy servers with no asset URL)
)

// assetSourceMode collapses the two stored bools into the radio's mode. Local-only wins when both are set,
// matching config.LayeredAssets.
func (a *App) assetSourceMode() int {
	enabled, _ := a.d.Prefs.LocalAssets()
	if enabled {
		return assetSrcLocal
	}
	if a.d.Prefs.Load().LocalAssetsLayered {
		return assetSrcLayered
	}
	return assetSrcStream
}

// setAssetSourceMode applies a radio selection, keeping the two stored bools mutually exclusive.
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
	// rebuildAssetOrigin re-points the URL builder and (deferred) reconciles the mount layer, covering both halves of
	// a mode change.
	a.rebuildAssetOrigin()
}

// drawAssetSourceSettings renders the whole panel and returns the new y.
func (a *App) drawAssetSourceSettings(y, w int32) int32 {
	pad := a.formX
	_, mounts := a.d.Prefs.LocalAssets()
	y = a.drawAssetSourceHeadline(pad, y, mounts)
	return a.drawAssetSourceAdvanced(pad, y, mounts)
}

// drawAssetSourceHeadline is what a user who has never set this up sees: one sentence on what is happening now and
// one button that starts the guided setup (basewizard.go). It is the whole panel for that user; the controls below
// are not actionable until you know what they do (a path field with no picker, a warning that appears only once
// you're half right).
func (a *App) drawAssetSourceHeadline(pad, y int32, mounts []string) int32 {
	c := a.ctx
	y = a.settingsDesc(pad, y, assetSourceSummary(a.assetSourceMode(), mounts), ColAccent)
	y += 6
	const label = "Use my own AO files…"
	if c.Button(sdl.Rect{X: pad, Y: y, W: 260, H: btnH + 8}, label) {
		a.openBaseWizard()
	}
	a.noteSearchRow(label, y)
	y += btnH + 14
	return a.settingsDesc(pad, y,
		"Point AsyncAO at a base folder on this computer — your own sprites, backgrounds and char.ini "+
			"files, read straight off the disk. It shows you what is in the folder before changing anything.",
		ColTextDim) + 10
}

// assetSourceSummary states the current setup in one specific present-tense line: what it is doing right now.
func assetSourceSummary(mode int, mounts []string) string {
	switch {
	case mode == assetSrcLocal && len(mounts) > 0:
		return "Now: reading " + mounts[0] + only(len(mounts)) + " only. Nothing is streamed."
	case mode == assetSrcLayered && len(mounts) > 0:
		return "Now: reading " + mounts[0] + only(len(mounts)) + " first, then streaming the rest."
	case len(mounts) > 0:
		// Configured but inert: the most confusing state this panel can be in, so it is the headline.
		return "Now: streaming everything. You have folders set up, but they are not being used."
	default:
		return "Now: streaming everything from the server."
	}
}

// only names the folders past the first without listing them; the full list is further down.
func only(n int) string {
	if n <= 1 {
		return ""
	}
	return " (+" + strconv.Itoa(n-1) + " more)"
}

// drawAssetSourceAdvanced is the original panel, moved below the button. It is not hidden behind a disclosure: the
// settings search only indexes rows that draw (noteSearchRow runs at draw time), and a collapsed row would be
// unfindable.
func (a *App) drawAssetSourceAdvanced(pad, y int32, mounts []string) int32 {
	c := a.ctx
	mode := a.assetSourceMode()
	haveMounts := len(mounts) > 0

	c.Label(pad, y+4, "Advanced", ColTextDim)
	c.Fill(sdl.Rect{X: pad + 80, Y: y + 11, W: a.formW - 80, H: 1}, ColPanelHi)
	y += 24

	// The three-way choice. Rows are drawn muted and inert, not hidden, when no folder is configured, so the option
	// stays visible and findable by the settings search.
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
			// Muted: drawn so the user can see what adding a folder would unlock.
			c.Checkbox(pad, y, opt.label, on)
			c.Fill(sdl.Rect{X: pad, Y: y, W: a.formW, H: 24}, sdl.Color{R: 0, G: 0, B: 0, A: 120})
		} else if next := c.Checkbox(pad, y, opt.label, on); next != on && !on {
			// Radio semantics over checkbox rows: selecting an unchecked row moves the mode; clicking the selected
			// row does
			// nothing.
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

	// the mount list
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
			// Shrinking always succeeds; the cap only refuses growth, so a user over the cap can prune back under it.
			a.d.Prefs.SetLocalAssets(a.assetSourceMode() == assetSrcLocal, next)
			a.rebuildAssetOrigin()
			break
		}
		y += 28
	}

	// Auto-mounted packages: discovered beside the exe at startup and appended after the manual mounts, so a manual
	// folder always wins. Read-only; managed by dropping or removing folders in packages/.
	if len(a.d.AutoMounts) > 0 {
		y += 4
		c.Label(pad, y+4, "Auto-mounted packages (from the packages/ folder next to the exe)", ColText)
		y += 24
		for i, m := range a.d.AutoMounts {
			c.LabelClipped(pad+20, y+4, a.formW-190, fmt.Sprintf("%d. %s", len(mounts)+i+1, m), ColTextDim)
			y += 28
		}
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

	// The nudge: folders configured but unused is a silent dead end. The headline states it too, but it has scrolled
	// off by the time the list is on screen.
	if haveMounts && mode == assetSrcStream {
		y += 4
		y = a.settingsDesc(pad, y, "These folders aren't being used. Pick \"Use my folders first\" above, or press \"Use my own AO files…\" at the top of this section.", ColTierYellow)
	}
	return y + 10
}

// drawMountIndexStatus reports what the index holds plus anything that went wrong. Every number is real state.
func (a *App) drawMountIndexStatus(y int32) int32 {
	c := a.ctx
	pad := a.formX

	switch {
	case a.mountIndexing:
		c.Label(pad, y+4, "Indexing your folders…", ColTextDim)
	case a.mountIndex != nil:
		files, mounts := a.mountIndex.Stats()
		line := fmt.Sprintf("Indexed %d file(s) in %d mount(s).", files, mounts)
		// Served-this-session, not a "shadow count": the client never enumerates the server's files, so how many of
		// these
		// replace a server asset is unknowable.
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

// filepathExt is filepath.Ext without importing path/filepath for one call; it only sees a user-typed mount string.
func filepathExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && !strings.ContainsAny(p[i:], `/\`) {
		return p[i:]
	}
	return ""
}
