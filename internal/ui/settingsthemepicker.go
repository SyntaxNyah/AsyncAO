package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// drawDownloaderSettings renders the opt-in single-asset downloader section:
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
		// Bandwidth cap (KiB/s; 0 = unlimited — the default, so grabs run full
		// speed unless you throttle them). Average-rate, applied per grab.
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
				// Clicking a button named "Add to local mounts" right after downloading
				// a character is unambiguous intent, and adding a mount that is never
				// read is a silent dead end — so turn layering on and SAY so, rather
				// than leaving the user to discover the extra step.
				a.setAssetSourceMode(assetSrcLayered, append(append([]string{}, mounts...), root))
				settings.statusLine = `Added the downloads folder and switched Asset source to "Use my folders first".`
			} else {
				a.rebuildAssetOrigin()
				settings.statusLine = "Added the downloads folder to local mounts."
			}
		}
		y += 30
		if a.audio.dl.active || len(a.audio.dl.queue) > 0 {
			status := a.audio.dl.status
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
		} else if a.audio.dl.status != "" {
			c.LabelClipped(pad+20, y+4, w-pad-40-scrollBarW, a.audio.dl.status, ColTextDim)
			y += 26
		}
	}
	return y + 8
}

// casingRoles drives the per-role subscription checkboxes (wire order).
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

// drawCasingRow renders the case-announcement subscription (SETCASE roles);
// changes re-subscribe live when connected. Returns the next y.
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

// --- theme picker -----------------------------------------------------------

// drawThemePreview renders the applied theme's chatbox skin (or the flat
// fallback panel) with sample text in the theme's colors — instant visual
// proof of what the current pick actually changes. Returns the next y.
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
	// Same skin-gated color rule as the live chatbox (readability).
	nameCol := ColAccent
	if skinned && a.skin.themeHasName {
		nameCol = a.skin.themeNameCol
	}
	msgCol := ColText
	if skinned && a.skin.themeHasMsg {
		msgCol = a.skin.themeMsgCol
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

// drawThemeBindRow binds the PICKED theme to a chosen server: joining
// that server applies the bound theme, leaving restores the global one.
// Works for any known server (same picker as the login section).
func (a *App) drawThemeBindRow(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	names, keys := a.loginTargets()
	if len(names) == 0 {
		return y
	}
	cur := 0
	if settings.themeBindKey == "" && a.srvMeta.serverKey != "" {
		settings.themeBindKey = a.srvMeta.serverKey
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
		if settings.themeBindKey == a.srvMeta.serverKey && a.link.sess != nil {
			a.srvMeta.themeBound = settings.themeName
			a.ensureThemeForSession()
		}
		settings.statusLine = "Theme " + settings.themeName + " bound — that server always uses it now."
	}
	if bound != "" {
		if c.Button(sdl.Rect{X: pad + 600, Y: y, W: 90, H: btnH}, "Unbind") {
			a.d.Prefs.SetServerTheme(settings.themeBindKey, "")
			if settings.themeBindKey == a.srvMeta.serverKey && a.link.sess != nil {
				a.srvMeta.themeBound = ""
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

// cycleTheme steps through the scanned theme list and persists the pick.
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
	// #35: stepping the arrows is a deliberate pick, like the dropdown — arm the
	// gen of the apply the step itself started. Also loads the chatbox skin +
	// colors, so the pick shows live.
	a.themeResizeArmGen = a.applyThemeAsync()
}

// themeDropdownID is the theme picker's widget id. Named because two places
// need it now: the control itself, and the "is the picker open?" catalog
// trigger that draws beside it.
const themeDropdownID = "themedd"

// subthemeDropdownID is the AO2 subtheme picker's widget id.
const subthemeDropdownID = "subthemedd"

// subthemeNoneLabel is the first row of the subtheme dropdown — the AO2 default
// (Options::subTheme() unset). A named empty-ish choice rather than a blank row,
// because a blank row in a list reads as a broken entry.
const subthemeNoneLabel = "(none)"

// subthemeMissingSuffix decorates the SYNTHESIZED row the dropdown grows when the
// saved subtheme names a folder the active theme does not have. AO2 keeps the
// subtheme across theme changes, so that state is normal, not an error — but a
// dropdown that quietly fell back to "(none)" told the user their pick was gone
// while the preference still held it (and would silently return the moment they
// switched back). The row says both true things at once: the pick, and that this
// theme has no such folder.
const subthemeMissingSuffix = " (not in this theme)"

// subthemeHint is the one-line explanation beside the dropdown. A const so the
// row costs no allocation to draw.
const subthemeHint = "a variant folder inside the theme — its files win over the theme's, and anything it leaves out still comes from the theme"

// themeDropNote is the line under the write root when nothing is truncated.
const themeDropNote = "drop a theme folder in here (or anywhere on this screen) and it shows up in the list above"

// themeGetNote is the discovery row's one line. A const, so the row costs no
// allocation to draw (the settings-text doctrine — see themeRowText).
// The source repository still carries the theme format reference
// (docs/THEME-FORMAT.md) for theme authors, which is what this row points at.
const themeGetNote = "themes are not bundled with the client — the source repository carries the theme format reference (docs/THEME-FORMAT.md) if you want to write one"

// themeRowLabelW is the label gutter shared by the catalog rows below, so
// "Subtheme:" and "Your themes:" line their controls up with each other.
const themeRowLabelW = 130

// subthemeClearBtnW is the width of the "Clear" button that appears when a
// subtheme is set but the ACTIVE theme has no such folder AND that theme ships no
// variants at all — the one state where the pick has no row of its own to clear
// from (a theme WITH variants grows the synthesized row above instead).
const subthemeClearBtnW = 70

const (
	// themeBadgeW is the column the "kind · origin" badge owns before the theme's
	// folder path starts. It fits the longest pair the two label sets can make
	// ("no theme files · read-only") at the stock face.
	themeBadgeW = 220
	// themeBadgeGap keeps the badge from touching the path beside it when a big
	// UI font pushes the text to the column's edge — the badge clips there rather
	// than drawing over its neighbour.
	themeBadgeGap = 8
	// subthemeDropdownW is the variant picker's width: wide enough for a folder
	// name plus subthemeMissingSuffix before the row clips.
	subthemeDropdownW = 200
	// subthemeHintGap separates that dropdown from the explanation beside it.
	subthemeHintGap = 10
	// subthemeClearGap keeps the "no such folder" line off the Clear button.
	subthemeClearGap = 6
	// themeFolderBtnsW is the strip on the right of the write-root row reserved
	// for its two buttons ("Open folder" + "Refresh"), so the path label clips
	// before it reaches them.
	themeFolderBtnsW = 200
	// themeOpenFolderBtnW / themeRefreshBtnW are those two buttons; Refresh is
	// right-anchored, so its own width doubles as its offset from the edge.
	themeOpenFolderBtnW = 110
	themeRefreshBtnW    = 84
	// themeGetBtnW is the "Open the repo" button on the discovery row above the
	// write root; themeGetBtnsW is the strip it owns — the button plus the gap that
	// keeps the sentence beside it from touching the button, so the text clips
	// before it reaches one.
	themeGetBtnW  = 130
	themeGetBtnsW = 140
)

const (
	// Row advances for the three catalog rows and the note under them. Named
	// per row rather than shared, because they are not the same measurement: the
	// badge and folder rows are plain label rows, the subtheme row carries a
	// dropdown, and the note is a trailing hint line.
	themeBadgeRowH    = 26
	themeSubthemeRowH = 30
	themeFolderRowH   = 26
	themeNoteRowH     = 22
	// themeGetRowH is the discovery row's advance. Same measurement as the folder
	// row — a label, a sentence and a right-anchored button — and named separately
	// for the same reason the others are: they are not one shared number.
	themeGetRowH = 26
	// themeFolderErrRowH is the advance of the write-root row when the location
	// could not be resolved at all: that arm draws no note under it and RETURNS,
	// so it carries the section's trailing gap itself.
	themeFolderErrRowH = 30
)

// themeRowText memoizes every string drawThemeCatalogRows draws. Those rows are a
// pure function of three inputs — the published catalog snapshot, the selected
// theme name and the subtheme preference — none of which changes per frame, while
// building them concatenates two labels, allocates an options slice and formats
// the truncation note. Uncached that is four allocations on every frame the Theme
// tab is up, which is exactly what the settings-text doctrine (see fmtOrderText)
type themeRowText struct {
	valid bool
	cat   *themeCatalog
	name  string
	sub   string

	// have reports that the catalog holds an entry for name; badge and dir are
	// that entry's two label columns.
	have  bool
	badge string
	dir   string

	// subOpts is the subtheme dropdown's option list — subthemeNoneLabel, the
	// theme's variants, and (when the saved pick is not one of them) the
	// synthesized row at subStale. Empty when the theme ships no variants, which
	// is the arm that draws subMsg and the Clear button instead.
	subOpts  []string
	subSel   int
	subStale int // index of the synthesized row, -1 when there is none
	subMsg   string

	// root is the themes write root as this build resolved it; note is the line
	// under it (truncation-aware).
	root string
	note string
}

// stale reports whether the memo has to be rebuilt for these inputs. Split from
// the draw so the memo key is testable without a renderer.
func (r *themeRowText) stale(cat *themeCatalog, name, sub string) bool {
	return !r.valid || r.cat != cat || r.name != name || r.sub != sub
}

// rebuild recomputes the whole row set from the three inputs.
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
			// The saved pick belongs to another theme. Show it as itself rather
			// than letting the control read "(none)" over a preference that is set.
			r.subStale = len(r.subOpts)
			r.subOpts = append(r.subOpts, sub+subthemeMissingSuffix)
			r.subSel = r.subStale
		}
	case sub != "":
		// No variants at all: there is no list to carry the pick, so the row says
		// it in words and the Clear button beside it is the way out.
		r.subMsg = "\"" + sub + "\" is set, but this theme has no such folder"
	default:
		r.subMsg = "this theme ships no variant folders"
	}
	if cat != nil {
		r.root = cat.WriteRoot
	}
	if r.root == "" {
		// Fallback for the frames before the first catalog publishes (and forever
		// on a machine whose config location never resolves). config.UserThemesDir
		// joins two memoized answers — ConfigBaseDir's probe and executableDir's
		// EvalSymlinks walk — and BOTH are already warm here: prefs loading resolves
		// them during boot, long before this screen can be drawn. Reached once per
		// rebuild, never per frame. Do not "simplify" either memo away, and do not
		// add a stat/exists check here (hard rule 2).
		r.root = config.UserThemesDir()
	}
	r.note = themeDropNote
	if cat != nil && cat.Truncated {
		r.note = fmt.Sprintf("showing the first %d themes found — the rest are still on disk and still load by name", themeCatalogCap)
	}
}

// drawThemeCatalogRows draws everything the off-thread theme catalog answers:
func (a *App) drawThemeCatalogRows(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule: without this the row draws under the sidebar
	cat := a.themeCatalogSnapshot()
	sub := a.d.Prefs.Load().SubthemeName
	rows := &settings.themeRows
	if rows.stale(cat, settings.themeName, sub) {
		rows.rebuild(cat, settings.themeName, sub)
	}

	// --- badge: what this theme is, and whether it is ours to edit ----------
	if c.onRow != nil {
		c.onRow("Theme badge", y)
	}
	c.Label(pad, y+4, "This theme:", ColText)
	switch {
	case cat == nil:
		c.Label(pad+themeRowLabelW, y+4, "reading your themes folder…", ColTextDim)
	case !rows.have:
		// The built-in default has no folder anywhere, and a bare-folder import
		// (#21) resolves outside themes/ — neither is an error.
		c.Label(pad+themeRowLabelW, y+4, "built in (no theme folder of its own)", ColTextDim)
	default:
		c.LabelClipped(pad+themeRowLabelW, y+4, themeBadgeW-themeBadgeGap, rows.badge, ColAccent)
		c.LabelClipped(pad+themeRowLabelW+themeBadgeW, y+4, w-pad-themeRowLabelW-themeBadgeW-scrollBarW, rows.dir, ColTextDim)
	}
	y += themeBadgeRowH

	// --- subtheme: AO2's own variant selector -------------------------------
	if c.onRow != nil {
		c.onRow("Subtheme", y)
	}
	c.Label(pad, y+4, "Subtheme:", ColText)
	if len(rows.subOpts) > 0 {
		if next, changed := c.Dropdown(subthemeDropdownID, sdl.Rect{X: pad + themeRowLabelW, Y: y, W: subthemeDropdownW, H: btnH}, rows.subOpts, rows.subSel); changed {
			pick := ""
			switch {
			case next == rows.subStale:
				// The synthesized row is the pick it already holds — re-picking it
				// must not write the DECORATED label into the preference.
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

	// --- where to GET themes, which is the row nobody could find -----------
	//
	// THE DISCOVERY POINT, and it still is one even though it no longer sells
	// anything. It used to advertise the fourteen hand-authored themes that lived
	// in the source repository's themes/ folder and nowhere else (never in the
	// release archives — they were content, not code), which is what made "drop a
	// theme folder in here" two rows down a dead end for anyone with no theme
	// folder to drop. That corpus has been retired, so the row now answers the
	// same dead end with the one thing the repository can still give a theme
	// author: the format reference.
	// It STAYS rather than being deleted along with the corpus, because the
	// question it exists for — "I have none, where do these come from?" — is
	// exactly as real when the answer is "you write one". There is deliberately no
	if c.onRow != nil {
		c.onRow("Get themes", y)
	}
	c.Label(pad, y+4, "Get themes:", ColText)
	c.LabelClipped(pad+themeRowLabelW, y+4, w-pad-themeRowLabelW-themeGetBtnsW-scrollBarW, themeGetNote, ColTextDim)
	if c.Button(sdl.Rect{X: w - themeGetBtnW - scrollBarW, Y: y, W: themeGetBtnW, H: btnH}, "Open the repo") {
		// The About screen's own launcher, not a second copy of the per-OS shellout
		// (about.go) — and aboutRepoURL, so the two surfaces can never drift to
		// different repositories.
		openBrowser(aboutRepoURL)
	}
	y += themeGetRowH

	// --- THE AUTHORING CROSS-LINK ------------------------------------------
	//
	// A user who remembers the old "Import:" / "Theme editor:" rows will look
	// here for them; the cross-tab hint machinery points them at the Theme editor
	// tab (drawThemeAuthoringHint).
	y = a.drawThemeAuthoringHint(y, w)

	// --- the write root, and the button that opens it ----------------------
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
		// fromUser stays FALSE on purpose. It does not gate the rescan — the list
		// refreshes either way — it arms the #35 window snap (pollThemeScan sets
		// themeResizeArmGen), and ResizeWindow also RECENTERS, yanking a window the
		// user had positioned. "Refresh" must only re-read the folder; the snap
		// belongs to the deliberate acts that ask for a theme, i.e. "Apply & rescan",
		// the folder picker and a drop.
		a.scanThemes(false)
	}
	y += themeFolderRowH
	c.LabelClipped(pad, y, w-pad-scrollBarW, rows.note, ColTextDim)
	y += themeNoteRowH
	return y
}

const (
	// themeArtBarTrackW / themeArtBarTrackH are the budget meter's track. It sits
	// in the label gutter's right-hand column like every other catalog row, so the
	// three rows above it and this one line up.
	themeArtBarTrackW = 220
	themeArtBarTrackH = 10
	// themeArtBarLabelGap keeps the "31.4 / 48.0 MiB · …" text off the track.
	themeArtBarLabelGap = 10
	// themeArtBarRowH / themeArtNoteRowH are the meter row and the hint under it.
	themeArtBarRowH  = 24
	themeArtNoteRowH = 22
	// themeArtBarTrackYOff centres the 10 px track against the text baseline of the
	// label beside it (btnH-tall rows put their text at +4).
	themeArtBarTrackYOff = 5
)

// themeArtNote is the line under the meter. A const, so the row costs no
// allocation to draw (the settings-text doctrine — see themeRowText).
const themeArtNote = "images and generator tiles an AsyncAO theme declares; they are pinned, so this is memory the streaming cache never gets back. The allowance scales with Settings ▸ General ▸ Texture memory budget."

// themeArtBarText memoizes the budget bar's label on the four numbers it is built
// from. Without it, drawing the Theme tab would format a string every frame — the
// exact cost the catalog rows above went to some length to avoid.
// Keyed on the VALUES rather than on a generation, because all four are already
// integers the store and the plan publish: the label is a pure function of them, so
// equality IS the validity test and there is no third thing to keep in step.
type themeArtBarText struct {
	valid         bool
	bytes, budget int64
	images, gens  int
	label         string
}

// rebuild recomputes the label if any of the four inputs moved.
func (r *themeArtBarText) refresh(bytes, budget int64, images, gens int) {
	if r.valid && r.bytes == bytes && r.budget == budget && r.images == images && r.gens == gens {
		return
	}
	*r = themeArtBarText{
		valid: true, bytes: bytes, budget: budget, images: images, gens: gens,
		label: themeArtBarLabel(bytes, images, gens, budget),
	}
}

// drawThemeArtBudgetRow is design §Q2's budget bar: what the active theme's own art
// is spending, out of what, permanently visible.
func (a *App) drawThemeArtBudgetRow(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule (see drawThemeCatalogRows)
	if c.onRow != nil {
		c.onRow("Theme art budget", y)
	}
	budget := render.ThemeMediaByteCap(a.d.Prefs.Load().TexBudgetMiB())
	// The store is nil in a headless App (and would be during the search index's
	// harvest pass on one), and the row's HEIGHT must not depend on that: a settings
	// row that sometimes advances y and sometimes does not is a scroll bug, and this
	// one is also an index entry the search jumps to by offset. Zeros draw the same
	// row the pre-first-theme case draws.
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
		// int64 throughout: bytes × track width overflows int32 at ~2 GiB of art,
		// which the caps make impossible today and would make this silently negative
		// the day somebody raised one.
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

// scanThemes lists themes/<name> directories under every root themeSearchRoots
// names — the custom folder, the executable's directory and the config base, in
// that order — off-thread; pollThemeScan picks up the result. It builds from the
// same function the LOADER does on purpose: this list is the only source for the
// theme dropdown and for cycleTheme's ◀ ▶, so a root the loader searches and this
// one does not is a theme that resolves by name yet cannot be picked (the bug
// on every non-portable install). TestThemePickerAndLoaderSearchTheSameRoots.
// false and the user's #35 auto-snap goes with no trace — see
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
		// The custom root is passed even when the current theme is the stock
		// "default": themeLoadRoots drops it for that ONE name (#87), but the picker
		// lists every name there is, and a custom folder's other themes must appear.
		roots := themeSearchRootsNow(root)
		settings.themeRes <- themeScan{names: scanThemeDirs(roots, pick), root: root, pickName: pick, in: customRoot, fromUser: fromUser}
	}()
}

func (a *App) pollThemeScan() {
	select {
	case res := <-settings.themeRes:
		settings.themeBusy = false
		settings.themeList = res.names
		// A dropped request that also changed the theme FOLDER cannot be answered by
		// re-labelling this result: the scan in flight carried the OLD folder, so its
		// root would be written back over the folder the user just picked (below),
		// and the latched intent would then arm a window snap for a theme root they
		// never chose — while the folder they did choose is never scanned at all.
		if settings.themeScanUserWant && res.in != strings.TrimSpace(settings.themeDir) {
			a.scanThemes(true)
			return
		}
		// A request the single-flight dropped set the latch but could not set
		// res.fromUser, so the two have to be OR'd.
		fromUser := res.fromUser || settings.themeScanUserWant
		// The scanner may have normalized the pasted path (the themes
		// folder itself, or one theme inside it) into the root
		// theme.Load expects — reflect and persist it.
		if res.root != "" && res.root != strings.TrimSpace(settings.themeDir) {
			settings.themeDir = res.root
			settings.statusLine = "Theme folder normalized to " + res.root
		}
		if res.pickName != "" {
			settings.themeName = res.pickName
		}
		if res.root != "" || res.pickName != "" {
			a.d.Prefs.SetTheme(settings.themeName, settings.themeDir)
			// #35: this is the import landing — the user pointed at a theme folder
			// (picker, drag-drop or Apply & rescan) and it resolved. Note that
			// pickName is EMPTY when they pointed at a themes/ parent, so the theme
			// NAME often doesn't change here even though a real import happened;
			// that is why the arming is explicit and not a name comparison.
			// Arms, never disarms: an automatic scan landing must not swallow a
			// pick the user made while it was in flight.
			gen := a.applyThemeAsync()
			if fromUser {
				// Clear the latch HERE, on the one branch that consumes it. A landing
				// that resolved no root and no pick (the default state: no custom
				// theme folder at all, so "Apply & rescan" resolves nothing) takes no
				// action, and clearing there would swallow the user's intent for a
				// rescan that never happened.
				settings.themeScanUserWant = false
				a.themeResizeArmGen = gen
			}
		}
	default:
	}
}

// themeINIFiles marks a directory as a single theme folder.
var themeINIFiles = []string{theme.DesignFileName, theme.FontsFileName, theme.SoundsFileName}

// normalizeThemeRoot turns whatever the user pasted or dropped into the
// root theme.Load expects (the folder CONTAINING themes/). Users paste
// all three shapes — the root, the themes folder itself, or a single
// theme inside it (returned as pickName and auto-selected). Runs off the
// render thread (it stats directories).
func normalizeThemeRoot(path string) (root, pickName string) {
	// Explorer's "Copy as path" wraps in quotes — strip them or every
	// stat below misses and the root never normalizes.
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return "", ""
	}
	path = filepath.Clean(path)
	// A single theme folder? Its name is the pick. The root is two up when the
	// folder really sits under a themes/ dir (…/root/themes/<name> → …/root); it
	// is ONE up otherwise, because a theme downloaded as a zip unpacks to a bare
	// folder with no themes/ parent, and inventing one made theme.Load probe a
	// directory that never existed — literally the import path in issue #21's
	// title. theme.Load's flat tier resolves <root>/<name> for that case.
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
	// The themes folder itself → its parent is the root.
	if strings.EqualFold(filepath.Base(path), theme.ThemesDirName) {
		return filepath.Dir(path), ""
	}
	return path, ""
}
