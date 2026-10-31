package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// drawDownloaderSettings renders the opt-in single-asset downloader section, returning the next y.
func (a *App) drawDownloaderSettings(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	on := a.d.Prefs.Load().CharDownloaderOn
	if next := c.Checkbox(pad, y, "Built-in downloader (OFF by default) — grab one character or background straight from the server", on); next != on {
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				CharDownloaderOn =

				next

		})
	}
	y += 24
	c.Label(pad+20, y, "Tired of a multi-GB pack download for one file? Turn this on and a Download button appears on each", ColTextDim)
	y += 18
	c.Label(pad+20, y, "character (char-select) and background (Background menu) cell. Characters also pull the sfx/blips their", ColTextDim)
	y += 18
	c.Label(pad+20, y, "char.ini references (those live outside the folder). Files save below — point \"Read assets from local", ColTextDim)
	y += 18
	c.Label(pad+20, y, "folders\" (above) at this folder to use them offline / in rehearsal.", ColTextDim)
	y += 24

	if on {
		// KiB/s, 0 = unlimited; average rate, applied per grab.
		capKBps := a.d.Prefs.DownloadCapKBps()
		if next := a.numberRow(y, "Bandwidth cap", capKBps, 256, 0, 1<<20); next != capKBps {
			a.d.Prefs.Update(func(
				sn *config.
					Settings) {

				sn.
					DownloadKBps =

					next

			})

		}
		c.Label(pad+270, y+4, "KiB/s — 0 = unlimited (full speed)", ColTextDim)
		y += 30
		root := downloadsRoot()
		c.LabelClipped(pad+20, y+4, w-pad-360-scrollBarW, "Folder: "+root, ColText)
		if c.Button(sdl.Rect{X: a.formX + a.formW - 340, Y: y, W: 150, H: btnH}, "Open folder") {
			_ = exec.Command("explorer.exe", root).Start()
		}
		if c.Button(sdl.Rect{X: a.formX + a.formW - 180, Y: y, W: 180, H: btnH}, "Add to local mounts") {
			mode := a.assetSourceMode()
			_, mounts := a.d.Prefs.LocalAssets()
			if !a.d.Prefs.SetLocalAssets(mode == assetSrcLocal, append(append([]string{}, mounts...), root)) {
				settings.statusLine = "Up to " + strconv.Itoa(config.LocalMountCap) + " folders. Remove one in Asset source first."
			} else if mode == assetSrcStream {
				// A mount that is never read is a dead end, so turn layering on and say so.
				a.setAssetSourceMode(assetSrcLayered, append(append([]string{}, mounts...), root))
				settings.statusLine = `Added the downloads folder and switched Asset source to "Use my folders first".`
			} else {
				a.rebuildAssetOrigin()
				settings.statusLine = "Added the downloads folder to local mounts."
			}
		}
		y += 30
		if a.dl.active || len(a.dl.queue) > 0 {
			status := a.dl.status
			pauseLabel := "Pause"
			if a.dlPaused.Load() {
				pauseLabel = "Resume"
				status = "Paused — " + status
			}
			c.LabelClipped(pad+20, y+4, w-pad-240-scrollBarW, status, ColAccent)
			if c.Button(sdl.Rect{X: a.formX + a.formW - 218, Y: y, W: 100, H: btnH}, pauseLabel) {
				a.dlPaused.Store(!a.dlPaused.Load())
			}
			if c.Button(sdl.Rect{X: a.formX + a.formW - 110, Y: y, W: 110, H: btnH}, "Cancel") {
				a.cancelDownload()
			}
			y += 30
		} else if a.dl.status != "" {
			c.LabelClipped(pad+20, y+4, w-pad-40-scrollBarW, a.dl.status, ColTextDim)
			y += 26
		}
	}
	return y + 8
}

// casingRoles drives the per-role subscription checkboxes, in wire order.
var casingRoles = []struct {
	bit   int
	label string
}{
	{courtroom.CaseRoleDef, "def"},
	{courtroom.CaseRolePro, "pro"},
	{courtroom.CaseRoleJudge, "judge"},
	{courtroom.CaseRoleJury, "jury"},
	{courtroom.CaseRoleSteno, "steno"},
}

// drawCasingRow renders the SETCASE role subscription, re-subscribing live when connected; returns the next y.
func (a *App) drawCasingRow(y int32) int32 {
	c := a.ctx
	pad := a.formX
	enabled, roles := a.d.Prefs.Load().Casing()
	changed := false
	if next := c.Checkbox(pad, y, "Case announcements (get notified when someone needs your role)", enabled); next != enabled {
		enabled = next
		changed = true
	}
	y += 26
	if enabled {
		x := pad + 20
		for _, r := range casingRoles {
			on := roles&r.bit != 0
			if next := c.Checkbox(x, y, r.label, on); next != on {
				roles ^= r.bit
				changed = true
			}
			x += c.TextWidth(r.label) + 52
		}
		y += 26
	}
	if changed {
		a.d.Prefs.SetCasing(enabled, roles)
		a.sendCasingPrefs() // live re-subscribe (no-op when disconnected)
	}
	return y + 8
}

// drawThemePreview renders the applied theme's chatbox skin or flat panel with sample text; returns the next y.
func (a *App) drawThemePreview(y int32) int32 {
	c := a.ctx
	pad := a.formX // rebase into the settings content card
	const prevW, prevH = 340, 70
	prev := sdl.Rect{X: pad, Y: y, W: prevW, H: prevH}
	skinned := false
	if page, ok := a.themePage(themeStemChatbox); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &prev)
		skinned = true
	}
	if !skinned {
		c.Fill(prev, sdl.Color{R: 16, G: 16, B: 24, A: 215})
		c.Border(prev, ColAccent)
	}
	// Same skin-only color rule as the live chatbox.
	nameCol := ColAccent
	if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	msgCol := ColText
	if skinned && a.themeHasMsg {
		msgCol = a.themeMsgCol
	}
	c.Label(prev.X+8, prev.Y+6, "Showname", nameCol)
	c.Label(prev.X+8, prev.Y+30, "Message text preview.", msgCol)
	label := "preview: theme chatbox skin"
	if !skinned {
		label = "preview: no chatbox skin in this theme (flat panel)"
	}
	c.Label(prev.X+prevW+12, prev.Y+6, label, ColTextDim)
	return y + prevH + 10
}

// drawThemeBindRow binds the picked theme to a server: joining applies it, leaving restores the global one.
func (a *App) drawThemeBindRow(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	names, keys := a.loginTargets()
	if len(names) == 0 {
		return y
	}
	cur := 0
	if settings.themeBindKey == "" && a.serverKey != "" {
		settings.themeBindKey = a.serverKey
	}
	for i, k := range keys {
		if k == settings.themeBindKey {
			cur = i
			break
		}
	}
	settings.themeBindKey = keys[cur]
	c.Label(pad, y+4, "Bind theme to server:", ColText)
	if next, changed := c.Dropdown("themebindsrv", sdl.Rect{X: pad + 170, Y: y, W: 260, H: btnH}, names, cur); changed {
		settings.themeBindKey = keys[next]
	}
	bound := a.d.Prefs.Load().ServerWarmInfoFor(settings.themeBindKey).Theme
	if c.Button(sdl.Rect{X: pad + 440, Y: y, W: 150, H: btnH}, "Bind "+clampLine(settings.themeName)) {
		a.d.Prefs.SetServerTheme(settings.themeBindKey, settings.themeName)
		if settings.themeBindKey == a.serverKey && a.sess != nil {
			a.themeBound = settings.themeName
			a.ensureThemeForSession()
		}
		settings.statusLine = "Theme " + settings.themeName + " bound — that server always uses it now."
	}
	if bound != "" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Unbind") {
			a.d.Prefs.SetServerTheme(settings.themeBindKey, "")
			if settings.themeBindKey == a.serverKey && a.sess != nil {
				a.themeBound = ""
				a.ensureThemeForSession()
			}
			settings.statusLine = "Theme binding removed."
		}
		c.LabelClipped(pad+700, y+4, w-pad-700-scrollBarW, "bound: "+bound, ColAccent)
	} else {
		c.Label(pad+600, y+4, "no binding (uses the global theme)", ColTextDim)
	}
	return y + 32
}

func (a *App) cycleTheme(step int) {
	list := settings.themeList
	if len(list) == 0 {
		return
	}
	idx := 0
	for i, name := range list {
		if name == settings.themeName {
			idx = i
			break
		}
	}
	idx = (idx + step + len(list)) % len(list)
	settings.themeName = list[idx]
	a.d.Prefs.SetTheme(settings.themeName, strings.TrimSpace(settings.themeDir))
	// Stepping is a deliberate pick, like the dropdown, so arm the window snap for this apply's generation.
	a.themeResizeArmGen = a.applyThemeAsync()
}

// themeDropdownID is the theme picker's widget id, also used by the catalog trigger's open check.
const themeDropdownID = "themedd"

const subthemeDropdownID = "subthemedd"

// subthemeNoneLabel is the subtheme dropdown's first row, the AO2 default (Options::subTheme() unset).
const subthemeNoneLabel = "(none)"

// subthemeMissingSuffix marks the row added when the saved subtheme is not in the active theme.
// AO2 keeps the subtheme across theme changes, so this is normal, and "(none)" would misstate the preference.
const subthemeMissingSuffix = " (not in this theme)"

const subthemeHint = "a variant folder inside the theme — its files win over the theme's, and anything it leaves out still comes from the theme"

const themeDropNote = "drop a theme folder in here (or anywhere on this screen) and it shows up in the list above"

// themeGetNote points theme authors at the format reference in the source repository (docs/THEME-FORMAT.md).
const themeGetNote = "themes are not bundled with the client — the source repository carries the theme format reference (docs/THEME-FORMAT.md) if you want to write one"

// themeRowLabelW is the label gutter shared by the catalog rows.
const themeRowLabelW = 130

// subthemeClearBtnW is the Clear button shown when the pick is missing and the theme has no variants to list it.
const subthemeClearBtnW = 70

const (
	// themeBadgeW fits the longest "kind · origin" pair ("no theme files · read-only") at the stock face.
	themeBadgeW = 220
	// themeBadgeGap keeps the badge off the path when a big UI font fills the column.
	themeBadgeGap = 8
	// subthemeDropdownW fits a folder name plus subthemeMissingSuffix.
	subthemeDropdownW = 200
	// subthemeHintGap separates that dropdown from the hint.
	subthemeHintGap = 10
	// subthemeClearGap keeps the "no such folder" line off the Clear button.
	subthemeClearGap = 6
	// themeFolderBtnsW is the write-root row's strip for its two buttons.
	themeFolderBtnsW = 200
	// Refresh is right-anchored, so its width is also its offset from the edge.
	themeOpenFolderBtnW = 110
	themeRefreshBtnW    = 84
	// themeGetBtnW is the discovery row's button; themeGetBtnsW is its strip including the gap.
	themeGetBtnW  = 130
	themeGetBtnsW = 140
)

const (
	// Row advances for the catalog rows and the note under them, separate because they are different measurements.
	themeBadgeRowH    = 26
	themeSubthemeRowH = 30
	themeFolderRowH   = 26
	themeNoteRowH     = 22
	// themeGetRowH is the discovery row's advance.
	themeGetRowH = 26
	// themeFolderErrRowH is the unresolved write-root row, which returns early and so carries the trailing gap.
	themeFolderErrRowH = 30
)

// themeRowText memoizes every string drawThemeCatalogRows draws, so the Theme tab does not allocate per frame.
// The key holds the catalog pointer: the snapshot is immutable and republished by swap, so identity is a full test.
type themeRowText struct {
	valid bool
	cat   *themeCatalog
	name  string
	sub   string

	// have reports that the catalog holds name; badge and dir are its two label columns.
	have  bool
	badge string
	dir   string

	// subOpts is the dropdown's options, with any missing-pick row at subStale; empty when the theme has no variants.
	subOpts  []string
	subSel   int
	subStale int // index of the synthesized row, -1 when there is none
	subMsg   string

	// root is the themes write root; note is the truncation-aware line under it.
	root string
	note string
}

// stale reports whether the memo must be rebuilt for these inputs.
func (r *themeRowText) stale(cat *themeCatalog, name, sub string) bool {
	return !r.valid || r.cat != cat || r.name != name || r.sub != sub
}

func (r *themeRowText) rebuild(cat *themeCatalog, name, sub string) {
	*r = themeRowText{valid: true, cat: cat, name: name, sub: sub, subStale: -1}
	entry, have := cat.Entry(name)
	r.have = have
	if have {
		r.badge = themeKindLabel(entry.Kind) + " · " + themeOriginLabel(entry.Origin)
		r.dir = entry.Dir
	}
	switch {
	case have && len(entry.Subthemes) > 0:
		r.subOpts = make([]string, 0, len(entry.Subthemes)+2) // +2: "(none)" and the possible stale row
		r.subOpts = append(r.subOpts, subthemeNoneLabel)
		r.subOpts = append(r.subOpts, entry.Subthemes...)
		for i, s := range entry.Subthemes {
			if s == sub {
				r.subSel = i + 1
				break
			}
		}
		if sub != "" && r.subSel == 0 {
			// The saved pick belongs to another theme; show it rather than "(none)" over a set preference.
			r.subStale = len(r.subOpts)
			r.subOpts = append(r.subOpts, sub+subthemeMissingSuffix)
			r.subSel = r.subStale
		}
	case sub != "":
		// No variants: no list can carry the pick, so say it in words, with Clear beside it.
		r.subMsg = "\"" + sub + "\" is set, but this theme has no such folder"
	default:
		r.subMsg = "this theme ships no variant folders"
	}
	if cat != nil {
		r.root = cat.WriteRoot
	}
	if r.root == "" {
		// Fallback before the first catalog publishes. config.UserThemesDir's memos are warm from prefs at boot.
		// Do not remove either memo, and do not add a stat here (hard rule 2).
		r.root = config.UserThemesDir()
	}
	r.note = themeDropNote
	if cat != nil && cat.Truncated {
		r.note = fmt.Sprintf("showing the first %d themes found — the rest are still on disk and still load by name", themeCatalogCap)
	}
}

// drawThemeCatalogRows draws the selected theme's badge, subtheme variant and write-root folder, all without disk I/O.
func (a *App) drawThemeCatalogRows(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule: without this the row draws under the sidebar
	cat := a.themeCatalogSnapshot()
	sub := a.d.Prefs.Load().SubthemeName
	rows := &settings.themeRows
	if rows.stale(cat, settings.themeName, sub) {
		rows.rebuild(cat, settings.themeName, sub)
	}

	if c.onRow != nil {
		c.onRow("Theme badge", y)
	}
	c.Label(pad, y+4, "This theme:", ColText)
	switch {
	case cat == nil:
		c.Label(pad+themeRowLabelW, y+4, "reading your themes folder…", ColTextDim)
	case !rows.have:
		// The built-in default has no folder, and a bare-folder import resolves outside themes/: neither is an error.
		c.Label(pad+themeRowLabelW, y+4, "built in (no theme folder of its own)", ColTextDim)
	default:
		c.LabelClipped(pad+themeRowLabelW, y+4, themeBadgeW-themeBadgeGap, rows.badge, ColAccent)
		c.LabelClipped(pad+themeRowLabelW+themeBadgeW, y+4, w-pad-themeRowLabelW-themeBadgeW-scrollBarW, rows.dir, ColTextDim)
	}
	y += themeBadgeRowH

	if c.onRow != nil {
		c.onRow("Subtheme", y)
	}
	c.Label(pad, y+4, "Subtheme:", ColText)
	if len(rows.subOpts) > 0 {
		if next, changed := c.Dropdown(subthemeDropdownID, sdl.Rect{X: pad + themeRowLabelW, Y: y, W: subthemeDropdownW, H: btnH}, rows.subOpts, rows.subSel); changed {
			pick := ""
			switch {
			case next == rows.subStale:
				// Must not write the decorated label into the preference.
				pick = sub
			case next > 0:
				pick = rows.subOpts[next]
			}
			a.d.Prefs.SetSubtheme(pick)
			a.applyThemeAsync() // the variant changes rects, art and fonts: reload like a theme swap
		}
		c.LabelClipped(pad+themeRowLabelW+subthemeDropdownW+subthemeHintGap, y+4,
			w-pad-themeRowLabelW-subthemeDropdownW-subthemeHintGap-scrollBarW, subthemeHint, ColTextDim)
	} else {
		msgW := w - pad - themeRowLabelW - scrollBarW
		if sub != "" {
			msgW -= subthemeClearBtnW + subthemeClearGap // leave the Clear button its own room
		}
		c.LabelClipped(pad+themeRowLabelW, y+4, msgW, rows.subMsg, ColTextDim)
		if sub != "" {
			if c.Button(sdl.Rect{X: w - subthemeClearBtnW - scrollBarW, Y: y, W: subthemeClearBtnW, H: btnH}, "Clear") {
				a.d.Prefs.SetSubtheme("")
				a.applyThemeAsync()
			}
		}
	}
	y += themeSubthemeRowH

	// Where to get themes. Above the write root so it still draws when that could not be resolved.
	if c.onRow != nil {
		c.onRow("Get themes", y)
	}
	c.Label(pad, y+4, "Get themes:", ColText)
	c.LabelClipped(pad+themeRowLabelW, y+4, w-pad-themeRowLabelW-themeGetBtnsW-scrollBarW, themeGetNote, ColTextDim)
	if c.Button(sdl.Rect{X: w - themeGetBtnW - scrollBarW, Y: y, W: themeGetBtnW, H: btnH}, "Open the repo") {
		// The About screen's launcher and aboutRepoURL, so the two surfaces cannot name different repositories.
		openBrowser(aboutRepoURL)
	}
	y += themeGetRowH

	// The cross-tab hint points at the Theme editor tab.
	y = a.drawThemeAuthoringHint(y, w)

	if c.onRow != nil {
		c.onRow("Your themes folder", y)
	}
	c.Label(pad, y+4, "Your themes:", ColText)
	root := rows.root
	if root == "" {
		c.LabelClipped(pad+themeRowLabelW, y+4, w-pad-themeRowLabelW-scrollBarW,
			"couldn't work out where to keep themes on this machine", ColTextDim)
		y += themeFolderErrRowH
		return y
	}
	c.LabelClipped(pad+themeRowLabelW, y+4, w-pad-themeRowLabelW-themeFolderBtnsW-scrollBarW, root, ColAccent)
	if c.Button(sdl.Rect{X: w - themeFolderBtnsW - scrollBarW, Y: y, W: themeOpenFolderBtnW, H: btnH}, "Open folder") {
		openThemesFolder(root)
	}
	if c.Button(sdl.Rect{X: w - themeRefreshBtnW - scrollBarW, Y: y, W: themeRefreshBtnW, H: btnH}, "Refresh") {
		a.requestThemeCatalog(themeScanExplicit) // trigger 4: unconditional
		// fromUser stays false: it does not affect the rescan, but arms the window snap, and ResizeWindow recenters.
		a.scanThemes(false)
	}
	y += themeFolderRowH
	c.LabelClipped(pad, y, w-pad-scrollBarW, rows.note, ColTextDim)
	y += themeNoteRowH
	return y
}

const (
	// themeArtBarTrackW and themeArtBarTrackH are the budget meter's track.
	themeArtBarTrackW = 220
	themeArtBarTrackH = 10
	// themeArtBarLabelGap keeps the label text off the track.
	themeArtBarLabelGap = 10
	// themeArtBarRowH and themeArtNoteRowH are the meter row and the hint under it.
	themeArtBarRowH  = 24
	themeArtNoteRowH = 22
	// themeArtBarTrackYOff centres the 10 px track on the label's text baseline.
	themeArtBarTrackYOff = 5
)

const themeArtNote = "images and generator tiles an AsyncAO theme declares; they are pinned, so this is memory the streaming cache never gets back. The allowance scales with Settings ▸ General ▸ Texture memory budget."

// themeArtBarText memoizes the budget bar's label, keyed on the four integers it is a pure function of.
type themeArtBarText struct {
	valid         bool
	bytes, budget int64
	images, gens  int
	label         string
}

// refresh recomputes the label if any of the four inputs moved.
func (r *themeArtBarText) refresh(bytes, budget int64, images, gens int) {
	if r.valid && r.bytes == bytes && r.budget == budget && r.images == images && r.gens == gens {
		return
	}
	*r = themeArtBarText{
		valid: true, bytes: bytes, budget: budget, images: images, gens: gens,
		label: themeArtBarLabel(bytes, images, gens, budget),
	}
}

// drawThemeArtBudgetRow shows the active theme's art spend, always visible because a pinned tier has no eviction:
// seeing the number first is the only guard against starving the streaming cache. Figures come from internal/render.
func (a *App) drawThemeArtBudgetRow(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule (see drawThemeCatalogRows)
	if c.onRow != nil {
		c.onRow("Theme art budget", y)
	}
	budget := render.ThemeMediaByteCap(a.d.Prefs.Load().TexBudgetMiB())
	// The store is nil in a headless App, but the height must not change: search jumps to this row by offset.
	var bytes int64
	var images, gens int
	if a.d.Store != nil {
		bytes, images, gens = a.d.Store.ThemeMediaBytes(), a.d.Store.ThemeMediaCount(), a.d.Store.ThemeGenCount()
	}
	settings.themeArt.refresh(bytes, budget, images, gens)

	c.Label(pad, y+4, "Theme art:", ColText)
	track := sdl.Rect{X: pad + themeRowLabelW, Y: y + themeArtBarTrackYOff, W: themeArtBarTrackW, H: themeArtBarTrackH}
	c.Fill(track, ColPanel)
	col := ColAccent
	if themeArtOverWarnPct(bytes, budget) {
		col = ColDanger
	}
	if budget > 0 && bytes > 0 {
		// int64: bytes × track width overflows int32 at about 2 GiB of art.
		fill := int32(bytes * int64(track.W) / budget)
		if fill > track.W {
			fill = track.W // a store over its own ceiling still draws a full bar, never a longer one
		}
		if fill < 1 {
			fill = 1 // anything resident at all is visible: "0 px" and "nothing loaded" must not look alike
		}
		c.Fill(sdl.Rect{X: track.X, Y: track.Y, W: fill, H: track.H}, col)
	}
	labelX := track.X + track.W + themeArtBarLabelGap
	c.LabelClipped(labelX, y+4, w-labelX-scrollBarW, settings.themeArt.label, col)
	y += themeArtBarRowH
	c.LabelClipped(pad, y, w-pad-scrollBarW, themeArtNote, ColTextDim)
	return y + themeArtNoteRowH
}

// scanThemes lists themes/<name> under every themeSearchRoots root off-thread; pollThemeScan picks up the result.
// Same roots as the loader, or a theme resolves by name yet cannot be picked. fromUser is recorded before the
// single-flight bail, so a request during a scan is not lost.
func (a *App) scanThemes(fromUser bool) {
	if fromUser {
		settings.themeScanUserWant = true
	}
	if settings.themeBusy {
		return
	}
	settings.themeBusy = true
	customRoot := strings.TrimSpace(settings.themeDir)
	go func() {
		root, pick := normalizeThemeRoot(customRoot)
		// themeLoadRoots drops the custom root only for "default"; the picker must list the folder's other themes.
		roots := themeSearchRootsNow(root)
		settings.themeRes <- themeScan{names: scanThemeDirs(roots, pick), root: root, pickName: pick, in: customRoot, fromUser: fromUser}
	}()
}

func (a *App) pollThemeScan() {
	select {
	case res := <-settings.themeRes:
		settings.themeBusy = false
		settings.themeList = res.names
		// A dropped request that changed the folder: this scan's root would overwrite the user's pick, so re-issue.
		// It cannot spin: the re-issue uses the current folder, and single-flight allows one scan at a time.
		if settings.themeScanUserWant && res.in != strings.TrimSpace(settings.themeDir) {
			a.scanThemes(true)
			return
		}
		// A request single-flight dropped set themeScanUserWant but not res.fromUser, so OR them.
		fromUser := res.fromUser || settings.themeScanUserWant
		// The scanner may have normalized the pasted path into the root theme.Load expects; reflect and persist it.
		if res.root != "" && res.root != strings.TrimSpace(settings.themeDir) {
			settings.themeDir = res.root
			settings.statusLine = "Theme folder normalized to " + res.root
		}
		if res.pickName != "" {
			settings.themeName = res.pickName
		}
		if res.root != "" || res.pickName != "" {
			a.d.Prefs.SetTheme(settings.themeName, settings.themeDir)
			// An import landing. pickName is empty for a themes/ parent, so arm explicitly rather than compare names.
			// Arms, never disarms: an automatic scan must not swallow a pick made while it was in flight.
			gen := a.applyThemeAsync()
			if fromUser {
				// Clear only here: a landing with no root and no pick takes no action and must not swallow the intent.
				settings.themeScanUserWant = false
				a.themeResizeArmGen = gen
			}
		}
	default:
	}
}

var themeINIFiles = []string{theme.DesignFileName, theme.FontsFileName, theme.SoundsFileName}

// normalizeThemeRoot turns a pasted root, themes folder or single theme (pickName) into theme.Load's root; it stats.
func normalizeThemeRoot(path string) (root, pickName string) {
	// Explorer's "Copy as path" wraps in quotes, and every stat below would miss.
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return "", ""
	}
	path = filepath.Clean(path)
	// A single theme folder: the root is two up under a themes/ dir, else one up, because a zip unpacks to a bare
	// folder. theme.Load's flat tier resolves <root>/<name> for that case.
	for _, ini := range themeINIFiles {
		if _, err := os.Stat(filepath.Join(path, ini)); err != nil {
			continue
		}
		parent := filepath.Dir(path)
		if strings.EqualFold(filepath.Base(parent), theme.ThemesDirName) {
			return filepath.Dir(parent), filepath.Base(path)
		}
		return parent, filepath.Base(path)
	}
	if strings.EqualFold(filepath.Base(path), theme.ThemesDirName) {
		return filepath.Dir(path), ""
	}
	return path, ""
}
