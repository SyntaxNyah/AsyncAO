package ui

// The Settings → Assets "Asset source" card: one list of everything an asset is searched in, top to bottom. The
// player's folders and .zip packs come first, then the auto-mounted packages, then a divider, then the web: the
// picked server's own host and its mirrors.
// Two things must stay spelled out in the UI rather than only in the docs: .zip packs work as mounts, and the user's
// own files win over the server's at the same path.

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/platform"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Asset-source modes. Storage is two independent bools (config.LayeredAssets) so no saved prefs.json changes
// meaning; the card shows them as "Use my folders" and "Stream".
const (
	assetSrcStream  = iota // stream everything from the web (the default)
	assetSrcLayered        // read the user's folders first, then stream the rest
	assetSrcLocal          // never stream (legacy servers with no asset URL)
)

// Whether a row's kind label opens a line listing what the folder contains.
const (
	foldNone = iota
	foldClosed
	foldOpen
)

// noPosition draws a row without a place in the search order.
const noPosition = -1

// What a row's buttons asked for.
const (
	rowKeep = iota
	rowUp
	rowDown
	rowRemove
)

const (
	// asrcServerPickW is the server dropdown on the "Stream from the web" line.
	asrcServerPickW int32 = 260
	// asrcHoverAlpha is the strength of the hovered row's highlight; a row's buttons show only while it is hovered.
	asrcHoverAlpha uint8 = 90
	// mountRowH is the pitch of a list row; mountNumW the width of its position number.
	mountRowH int32 = 30
	mountNumW int32 = 28
	// mountCtlH sizes a row's buttons, mountMoveW the square move buttons, mountRemoveW the Remove button.
	mountCtlH    int32 = 24
	mountMoveW   int32 = 28
	mountRemoveW int32 = 76
	mountCtlGap  int32 = 6
	// mountBadgeW reserves the kind column, wide enough for "Folder" and its arrow.
	mountBadgeW int32 = 92
	// foldArrowRows is the height of the arrow after a kind label; foldArrowW the room it takes.
	foldArrowRows int32 = 4
	foldArrowW    int32 = 14
	// mountAddBtnW is the Add button beside the path field.
	mountAddBtnW int32 = 70
	// mountArrowRows is the height of a move button's triangle; it is drawn because the UI font may lack ▲▼.
	mountArrowRows int32 = 5
	// mountNoteGap separates a row's path from the status note right-aligned after it.
	mountNoteGap int32 = 12
	// mountCheckH is the line under a folder showing what it contains or why it can't be used; mountBadgeGap
	// separates its pills.
	mountCheckH   int32 = 28
	mountBadgeGap int32 = 6
	// packageNameW is the least room a package's name takes before its contents, so short names line up.
	packageNameW int32 = 140
)

// The card's copy. It must keep saying what a .zip does, that the user's files win, and that order decides.
const (
	asrcLayeredHint = "Your files replace the server's at the same path, and anything you don't have still streams from the server."
	asrcLocalHint   = "Nothing streams, so every asset must come from your folders."
	asrcStreamHint  = "Every asset comes from the web."
	asrcUseFolders  = "Use my folders and packs"
	asrcStream      = "Stream from the web"
	asrcPackages    = "Use packages"
	mountOrderHint  = "Folders and .zip packs are searched top to bottom, and the first one with the file wins."
)

// assetSourceMode is what the three switches add up to: nothing local read is streaming, local read with streaming
// off is local-only, and anything else layers local files over the stream.
func (a *App) assetSourceMode() int {
	if local, _ := a.localAssets(); local {
		return assetSrcLocal
	}
	if layered, _ := a.layeredAssets(); layered {
		return assetSrcLayered
	}
	return assetSrcStream
}

// setSources sets "Use my folders and packs", streaming and the player's folders together.
func (a *App) setSources(folders, stream bool, mounts []string) {
	a.d.Prefs.SetLayeredAssets(folders)
	a.d.Prefs.SetLocalAssets(!stream, mounts)
	a.applySources()
}

// setStreaming flips "Stream from the web"; switching it off switches the folders and packages on, so there is
// something to read.
func (a *App) setStreaming(on bool, mounts []string) {
	if !on {
		a.d.Prefs.SetLayeredAssets(true)
		a.d.Prefs.Update(func(sn *config.Settings) { sn.PackagesOff = false })
	}
	a.d.Prefs.SetLocalAssets(!on, mounts)
	a.applySources()
}

// applySources re-points the asset origin after a switch or the folder list changed, switching streaming back on
// first when nothing local is left to read.
func (a *App) applySources() {
	if streamOff, mounts := a.d.Prefs.LocalAssets(); streamOff && !a.haveLocalSources() {
		a.d.Prefs.SetLocalAssets(false, mounts)
	}
	// rebuildAssetOrigin re-points the URL builder and (deferred) reconciles the mount layer.
	a.rebuildAssetOrigin()
}

// saveMounts stores the player's folders and applies them; false past the cap. A folder added to an empty list is
// meant to be used, so it switches folders on.
func (a *App) saveMounts(next []string) bool {
	_, prev := a.d.Prefs.LocalAssets()
	if !a.d.Prefs.SetLocalMounts(next) {
		return false
	}
	if len(prev) == 0 && len(next) > 0 {
		a.d.Prefs.SetLayeredAssets(true)
	}
	a.applySources()
	return true
}

// drawAssetSourceSettings is the whole card: what is happening now and the one ordered list. A row's buttons show
// only while it is hovered, and the card's explanations sit behind the ? beside the heading.
func (a *App) drawAssetSourceSettings(y, _ int32) int32 {
	pad := a.formX
	_, mounts := a.d.Prefs.LocalAssets()
	mode := a.assetSourceMode()

	title, kind, bg := assetSourceHeading(mode, len(mounts) > 0 || len(a.d.AutoMounts) > 0)
	a.badgeHeading(y, title, kind, bg)
	a.drawSourceHelp(y, title, kind, mounts, mode)
	y += settHeadingH + settDetailGap + settDetailGap

	streamOff, _ := a.d.Prefs.LocalAssets()
	useFolders, stream := a.d.Prefs.Load().LocalAssetsLayered, !streamOff
	if next := a.ctx.Checkbox(pad, y, asrcUseFolders, useFolders); next != useFolders {
		a.setSources(next, stream, mounts)
		return y
	}
	a.noteSearchRow(asrcUseFolders, y)
	y += settRowH

	checkFolders(mounts, a.d.AutoMounts)
	y, changed := a.drawMountRows(y, mounts, useFolders)
	if changed {
		return y
	}
	y = a.drawFolderProblems(y)

	next, key := a.drawWebDivider(y+settDetailGap, stream)
	if next != stream {
		a.setStreaming(next, mounts)
		return y
	}
	y += settDetailGap + mountRowH
	if key == "" {
		y = a.settingsDesc(pad+mountNumW, y, "Refresh the lobby once to see each server's host here.", ColTextDim) + settDetailGap
	}
	y = a.drawHostRows(y, key, len(searchedFolders(mounts, useFolders))+packageRows(a.packagesInUse()), stream)
	return a.drawAddSourceRow(y+settDetailGap, key, mounts) + settSectionGap
}

// drawSourceHelp is the ? after the heading's pill: the summary and the card's explanations, on hover.
func (a *App) drawSourceHelp(y int32, title, kind string, mounts []string, mode int) {
	c := a.ctx
	x := a.formX + a.headingWidth(title) + settBadgeGap + c.TextWidth(kind) + 2*settBadgePadX + settBadgeGap
	r := sdl.Rect{X: x, Y: y + settHeadingBadgeDY, W: settBadgeH, H: settBadgeH}
	c.Border(r, ColTextDim)
	c.Label(r.X+(r.W-c.TextWidth("?"))/2, r.Y+settBadgeDY, "?", ColTextDim)
	if c.hovering(r) {
		summary := mountSummary(mounts, len(a.packagesInUse()), a.d.Manager.Stats().MountFetches)
		c.Tooltip(r, summary+"\n"+mountOrderHint+"\n"+assetModeHint(mode))
	}
}

// assetSourceHeading names the current mode for the card's heading and pill.
func assetSourceHeading(mode int, haveMounts bool) (title, kind string, bg sdl.Color) {
	switch {
	case mode == assetSrcLocal && haveMounts:
		return "Your files only", "Local only", ColTierYellow
	case mode == assetSrcLayered && haveMounts:
		return "Your files, then the web", "Layered", ColAccent
	case haveMounts:
		return "Streaming from the web", "Folders off", ColTextDim
	default:
		return "Streaming from the web", "Stream", ColAccent
	}
}

// mountSummary is the dim line under the heading: what is mounted and what it has served this session.
func mountSummary(mounts []string, auto int, served int64) string {
	if len(mounts) == 0 && auto == 0 {
		return "No folders or packs added."
	}
	packs := 0
	for _, m := range mounts {
		if isZipMount(m) {
			packs++
		}
	}
	var parts []string
	if n := len(mounts) - packs; n > 0 {
		parts = append(parts, plural(n, "folder"))
	}
	if packs > 0 {
		parts = append(parts, plural(packs, "pack"))
	}
	if auto > 0 {
		parts = append(parts, strconv.Itoa(auto)+" auto-mounted")
	}
	if served > 0 {
		parts = append(parts, plural(int(served), "asset")+" from them this session")
	}
	return strings.Join(parts, "  ·  ")
}

// plural is "1 folder" or "3 folders".
func plural(n int, noun string) string {
	s := strconv.Itoa(n) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}

// assetModeHint is the line under the list saying what the current mode does.
func assetModeHint(mode int) string {
	switch mode {
	case assetSrcLayered:
		return asrcLayeredHint
	case assetSrcLocal:
		return asrcLocalHint
	default:
		return asrcStreamHint
	}
}

// drawMountRows draws the player's mounts and the auto-mounted packages after them, and returns the y below them and
// whether a button changed the list. Auto-mounted packages come last, so a manual folder always wins; they are
// managed in packages/.
func (a *App) drawMountRows(y int32, mounts []string, on bool) (int32, bool) {
	col := sourceRowColor(on)
	for i, m := range mounts {
		kind := "Folder"
		if isZipMount(m) {
			kind = "ZIP"
		}
		hovered := a.hoverRow(y)
		pos := i
		if !on {
			pos = noPosition // not searched, so no place in the order
		}
		if fix := a.drawFoldingLine(y, pos, m, kind, col, hovered, true); fix != "" {
			a.replaceMount(i, fix)
			return y, true
		}
		if hovered {
			if act := a.rowControls(y, i, len(mounts)); act != rowKeep {
				a.applyMountAction(act, i, mounts)
				return y, true
			}
		}
		y = a.drawFolderContents(y+mountRowH, m)
	}
	if len(mounts) == 0 {
		y = a.settingsDesc(a.formX+mountNumW, y, "No folders or packs yet.", ColTextDim) + settDetailGap
	}
	return a.drawPackagesLine(y, mounts, on)
}

// drawPackagesLine is the packages folder after the player's folders: its own "Use packages" switch, then one row
// with the packages it holds and a problem with any of them. The row's arrow opens a line per package.
func (a *App) drawPackagesLine(y int32, mounts []string, on bool) (int32, bool) {
	c := a.ctx
	// Its own switch, like the folders': packages are read with it on, whatever the folder switch says. It shows even
	// with no packages yet, so the switch is always where people look for it.
	switchOn := !a.d.Prefs.Load().PackagesOff
	if next := c.Checkbox(a.formX, y, asrcPackages, switchOn); next != switchOn {
		a.setPackagesInUse(next)
		return y, true
	}
	c.Tooltip(sdl.Rect{X: a.formX, Y: y, W: c.CheckboxWidth(asrcPackages), H: settRowH},
		"Packages are the folders in \"packages\" next to AsyncAO, searched after yours.")
	a.noteSearchRow(asrcPackages, y)
	y += settRowH
	if len(a.d.AutoMounts) == 0 {
		return a.settingsDesc(a.formX+mountNumW, y, "No packages yet: put package folders in a \"packages\" folder next to AsyncAO.", ColTextDim) + settDetailGap, false
	}
	inUse := len(a.packagesInUse()) > 0
	line := sourceLine{pos: len(searchedFolders(mounts, on)), kind: "Auto", col: sourceRowColor(inUse), fold: foldClosed}
	if !inUse {
		line.pos = noPosition // not searched, so no place in the order
	}
	if settings.packagesOpen {
		line.fold = foldOpen
	}
	names := make([]string, 0, len(a.d.AutoMounts))
	problems := 0
	for _, m := range a.d.AutoMounts {
		names = append(names, filepath.Base(m))
		if scan, ok := settings.folderScans[m]; ok && !scan.looksLikeBase() {
			problems++
		}
	}
	line.text = "Packages: " + strings.Join(names, ", ")
	if problems > 0 {
		line.note, line.noteCol = plural(problems, "package")+" can't be used", ColTierYellow
	}
	a.hoverRow(y)
	kindR, _ := a.drawSourceLine(y, line)
	if c.hovering(kindR) {
		c.Tooltip(kindR, "What each package contains")
		if c.clicked {
			settings.packagesOpen = !settings.packagesOpen
		}
	}
	y += mountRowH
	if settings.packagesOpen {
		y = a.drawPackageContents(y)
	}
	return y, false
}

// drawPackageContents lists each package under the packages row with the kinds of media in it, or why it can't be
// used.
func (a *App) drawPackageContents(y int32) int32 {
	c := a.ctx
	x := a.formX + mountNumW
	for _, m := range a.d.AutoMounts {
		name := filepath.Base(m)
		c.Label(x, y+settBadgeDY, name, ColTextDim)
		bx := x + max(c.TextWidth(name)+settBtnGap, packageNameW)
		scan, ok := settings.folderScans[m]
		switch {
		case !ok:
		case scan.err != "":
			c.Label(bx, y+settBadgeDY, "Can't read", ColDanger)
		case !scan.looksLikeBase():
			c.Label(bx, y+settBadgeDY, "Not an AO base", ColTierYellow)
		default:
			for _, badge := range scan.badges {
				bx += a.settingsBadge(bx, y, badge, ColTextDim) + mountBadgeGap
			}
		}
		y += mountCheckH
	}
	return y
}

// packageRows is how many places the packages take in the order: one row for all of them, none while off.
func packageRows(inUse []string) int {
	if len(inUse) == 0 {
		return 0
	}
	return 1
}

// setPackagesInUse switches the packages folder in or out of the search order, whatever the folder switch says.
func (a *App) setPackagesInUse(on bool) {
	a.d.Prefs.Update(func(sn *config.Settings) { sn.PackagesOff = !on })
	a.applySources()
}

// searchedFolders is the player's folders while their switch is on, for numbering what comes after them.
func searchedFolders(mounts []string, on bool) []string {
	if !on {
		return nil
	}
	return mounts
}

// drawFoldingLine is drawSourceLine for a folder. Once its contents are known its kind label opens and closes the
// line that lists them; a folder that can't be used says why on its own line, with the fix when there is one. It
// returns the folder to switch to when the user takes the fix.
func (a *App) drawFoldingLine(y int32, pos int, path, kind string, col sdl.Color, hovered, fixable bool) string {
	c := a.ctx
	line := sourceLine{pos: pos, text: path, kind: kind, col: col}
	scan, scanned := settings.folderScans[path]
	fixLabel := ""
	switch {
	case !scanned:
	case scan.err != "":
		line.note, line.noteCol = "Can't read", ColDanger
		if hovered {
			c.Tooltip(a.rowRect(y), scan.err)
		}
	case !scan.looksLikeBase():
		line.note, line.noteCol = "Not an AO base", ColTierYellow
		if fixable && scan.suggest != "" {
			fixLabel = "Use " + filepath.Base(scan.suggest)
			line.reserve = c.TextWidth(fixLabel) + labelBtnPadX
		}
	default:
		line.fold = foldClosed
		if settings.folderOpen[path] {
			line.fold = foldOpen
		}
	}
	kindR, fixR := a.drawSourceLine(y, line)
	if fixLabel != "" {
		c.Tooltip(fixR, scan.suggest)
		if c.Button(fixR, fixLabel) {
			return scan.suggest
		}
	}
	if line.fold != foldNone && c.hovering(kindR) {
		c.Tooltip(kindR, "What it contains")
		if c.clicked {
			settings.folderOpen[path] = line.fold == foldClosed
		}
	}
	return ""
}

// drawFolderContents draws the open line under a folder listing the kinds of media in it, and returns the y below.
func (a *App) drawFolderContents(y int32, path string) int32 {
	scan, scanned := settings.folderScans[path]
	if !scanned || !scan.looksLikeBase() || !settings.folderOpen[path] {
		return y
	}
	x := a.formX + mountNumW
	for _, badge := range scan.badges {
		x += a.settingsBadge(x, y, badge, ColTextDim) + mountBadgeGap
	}
	return y + mountCheckH
}

// sourceRowColor is a row's text colour while its half of the list is on or off.
func sourceRowColor(on bool) sdl.Color {
	if on {
		return ColText
	}
	return ColTextDim
}

// rowRect is the whole row at y, buttons included.
func (a *App) rowRect(y int32) sdl.Rect {
	return sdl.Rect{X: a.formX, Y: y - (mountRowH-mountCtlH)/2, W: a.formW, H: mountRowH}
}

// hoverRow highlights the row at y while the cursor is on it and reports whether it is; a row's buttons and details
// show only then.
func (a *App) hoverRow(y int32) bool {
	r := a.rowRect(y)
	if !a.ctx.hovering(r) {
		return false
	}
	hl := ColPanelHi
	hl.A = asrcHoverAlpha
	a.ctx.Fill(r, hl)
	return true
}

// applyMountAction moves or removes the mount at i.
func (a *App) applyMountAction(act, i int, mounts []string) {
	switch act {
	case rowUp:
		a.moveMount(i, i-1)
	case rowDown:
		a.moveMount(i, i+1)
	case rowRemove:
		// Shrinking always succeeds; the cap only refuses growth, so a user over the cap can prune back under it.
		a.saveMounts(slices.Delete(slices.Clone(mounts), i, i+1))
	}
}

// moveMount moves the mount at from to position to, then reloads what the folders drew, as the winner may change.
func (a *App) moveMount(from, to int) {
	_, mounts := a.d.Prefs.LocalAssets()
	a.saveMounts(moveItem(mounts, from, to))
	a.reloadFromFolders()
}

// moveItem returns a copy of list with the item at from moved to position to.
func moveItem(list []string, from, to int) []string {
	out := slices.Delete(slices.Clone(list), from, from+1)
	return slices.Insert(out, to, list[from])
}

// drawWebDivider draws the Stream toggle at the left edge, like the folders' toggle, then the server the web rows
// belong to and the dividing line. It returns the toggle's value and the server's key, "" when none is known.
func (a *App) drawWebDivider(y int32, stream bool) (bool, string) {
	c := a.ctx
	pad := a.formX
	toggleW := c.CheckboxWidth(asrcStream)
	next := c.Checkbox(pad, y, asrcStream, stream)
	c.Tooltip(sdl.Rect{X: pad, Y: y, W: toggleW, H: settRowH}, "Off reads only your folders, for servers with no asset URL.")
	a.noteSearchRow("Extra asset hosts", y)
	x := pad + toggleW + settBtnGap
	key, names, keys := a.hostServerKey()
	if key != "" {
		c.Label(x, y, "for", ColTextDim)
		x += c.TextWidth("for") + settBtnGap
		pick := sdl.Rect{X: x, Y: y - (btnH-checkboxBoxPx)/2, W: asrcServerPickW, H: btnH}
		if i, changed := c.Dropdown("assethostsrv", pick, names, slices.Index(keys, key)); changed {
			settings.assetHostKey = keys[i]
			key = keys[i]
		}
		x = pick.X + pick.W + settBtnGap
	}
	if x < pad+a.formW {
		c.Fill(sdl.Rect{X: x, Y: y + checkboxBoxPx/2, W: pad + a.formW - x, H: 1}, ColPanelHi)
	}
	return next, key
}

// sourceLine is one row of the list: its place in the search order, what it is, and a note on its state.
type sourceLine struct {
	pos     int // noPosition when it isn't searched
	text    string
	kind    string
	col     sdl.Color
	note    string
	noteCol sdl.Color
	fold    int
	reserve int32 // room kept after the note for a button the caller draws
}

// drawSourceLine draws a row's position, path, right-aligned note and kind, leaving the row's right end for its
// buttons. It returns the kind label's rect, which opens a folding row, and the room reserved for a button.
func (a *App) drawSourceLine(y int32, line sourceLine) (kindR, reserved sdl.Rect) {
	c := a.ctx
	pad := a.formX
	ctlW := mountRemoveW + 2*(mountMoveW+mountCtlGap) + mountCtlGap
	pathW := a.formW - mountNumW - mountBadgeW - ctlW - settBtnGap
	textY := y + (mountCtlH-settBadgeH)/2 + settBadgeDY
	if line.pos != noPosition {
		c.Label(pad, textY, strconv.Itoa(line.pos+1), ColTextDim)
	}
	end := pad + mountNumW + pathW
	reserved = sdl.Rect{X: end - line.reserve, Y: y + (mountCtlH-settBadgeH)/2, W: line.reserve, H: settBadgeH}
	if line.reserve > 0 {
		end = reserved.X - mountNoteGap
		pathW -= line.reserve + mountNoteGap
	}
	if line.note != "" {
		noteW := c.TextWidth(line.note)
		c.Label(end-noteW, textY, line.note, line.noteCol)
		pathW -= noteW + mountNoteGap
	}
	c.LabelClipped(pad+mountNumW, textY, pathW, line.text, line.col)
	// One column for every kind, so the labels line up whatever the text or arrow.
	kindR = sdl.Rect{X: pad + a.formW - ctlW - mountBadgeW, Y: y + (mountCtlH-settBadgeH)/2, W: c.TextWidth(line.kind), H: settBadgeH}
	c.Label(kindR.X, textY, line.kind, ColTextDim)
	if line.fold != foldNone {
		fillArrow(c, kindR.X+kindR.W+foldArrowW/2, kindR.Y+(kindR.H-foldArrowRows)/2, foldArrowRows, line.fold == foldOpen, ColTextDim)
		kindR.W += foldArrowW
	}
	return kindR, reserved
}

// fillArrow draws a filled triangle rows tall, centred on cx, pointing up or down.
func fillArrow(c *Ctx, cx, top, rows int32, up bool, col sdl.Color) {
	for row := range rows {
		half := row
		if !up {
			half = rows - 1 - row
		}
		c.Fill(sdl.Rect{X: cx - half, Y: top + row, W: 2*half + 1, H: 1}, col)
	}
}

// rowControls draws a row's move and Remove buttons and says which one was pressed.
func (a *App) rowControls(y int32, i, n int) int {
	c := a.ctx
	removeR := sdl.Rect{X: a.formX + a.formW - mountRemoveW, Y: y, W: mountRemoveW, H: mountCtlH}
	downR := sdl.Rect{X: removeR.X - mountCtlGap - mountMoveW, Y: y, W: mountMoveW, H: mountCtlH}
	upR := sdl.Rect{X: downR.X - mountCtlGap - mountMoveW, Y: y, W: mountMoveW, H: mountCtlH}
	act := rowKeep
	if a.moveButton(upR, true, i > 0) {
		act = rowUp
	}
	c.Tooltip(upR, "Search this one earlier")
	if a.moveButton(downR, false, i < n-1) {
		act = rowDown
	}
	c.Tooltip(downR, "Search this one later")
	if c.Button(removeR, "Remove") {
		act = rowRemove
	}
	return act
}

// moveButton is a square button with a drawn triangle; a disabled one draws muted and never fires.
func (a *App) moveButton(r sdl.Rect, up, enabled bool) bool {
	c := a.ctx
	col := ColText
	clicked := false
	if enabled {
		clicked = c.Button(r, "")
	} else {
		c.Fill(r, ColPanel)
		c.Border(r, ColPanelHi)
		col = ColPanelHi
	}
	fillArrow(c, r.X+r.W/2, r.Y+(r.H-mountArrowRows)/2, mountArrowRows, up, col)
	return clicked
}

// drawAddSourceRow is the one field that adds a folder, a .zip pack or a web mirror, and the folder and pack pickers.
func (a *App) drawAddSourceRow(y int32, key string, mounts []string) int32 {
	c := a.ctx
	pad := a.formX
	picker := a.d.Capabilities.Has(platform.PickFolder)
	packPicker := a.d.Capabilities.Has(platform.PickFile)
	many := a.d.Capabilities.Has(platform.PickManyFolders)
	pickLabel := mountPickLabel(many)
	pickW := c.TextWidth(pickLabel) + labelBtnPadX
	packW := c.TextWidth(packPickLabel) + labelBtnPadX
	fieldW := a.formW - mountAddBtnW - settBtnGap
	if picker {
		fieldW -= pickW + settBtnGap
	}
	if packPicker {
		fieldW -= packW + settBtnGap
	}
	var commit bool
	settings.mountInput, commit = c.TextField("mount", sdl.Rect{X: pad, Y: y, W: fieldW, H: fieldH}, settings.mountInput,
		`C:\AO2\base, C:\AO2\mypack.zip or https://mirror.example/base/`)
	x := pad + fieldW + settBtnGap
	if c.Button(sdl.Rect{X: x, Y: y, W: mountAddBtnW, H: btnH}, "Add") || commit {
		if msg, ok := a.addSource(key, settings.mountInput, mounts); ok {
			settings.mountInput = ""
			settings.statusLine = msg
		} else if msg != "" {
			settings.statusLine = msg
		}
	}
	x += mountAddBtnW + settBtnGap
	if picker {
		if c.Button(sdl.Rect{X: x, Y: y, W: pickW, H: btnH}, pickLabel) {
			a.browseForMounts(many)
		}
		x += pickW + settBtnGap
	}
	if packPicker {
		r := sdl.Rect{X: x, Y: y, W: packW, H: btnH}
		if c.Button(r, packPickLabel) {
			a.browseForPacks()
		}
		c.Tooltip(r, "Pick .zip packs; they're searched like folders.")
	}
	a.noteSearchRow("Folder or .zip", y)
	return y + btnH + settBtnGap
}

// addSource adds what was typed: an http(s) address as a mirror of the picked server, anything else as a folder or
// .zip pack. It returns the status line and whether the list changed.
func (a *App) addSource(key, input string, mounts []string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}
	if isWebAddress(input) {
		return a.addHost(key, input)
	}
	if slices.Contains(mounts, input) {
		return input + " is already in the list.", false
	}
	if !a.saveMounts(append(slices.Clone(mounts), input)) {
		return "Only " + strconv.Itoa(config.LocalMountCap) + " folders fit, so remove one first.", false
	}
	return "Added " + input + ".", true
}

// isWebAddress reports whether typed text names a web host rather than a folder.
func isWebAddress(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// drawFolderProblems lists folder files that failed to decode and mounts that could not open.
func (a *App) drawFolderProblems(y int32) int32 {
	pad := a.formX
	if n := a.d.Manager.Stats().PackQuarantined; n > 0 {
		y = a.settingsDesc(pad, y, plural(int(n), "file")+" in your folders couldn't be read and are streaming instead.", ColTierYellow)
	}
	_, _, errs := a.d.Manager.FolderStatus()
	for _, err := range errs {
		y = a.settingsDesc(pad, y, "Can't use: "+err.Error(), ColDanger)
	}
	return y
}
