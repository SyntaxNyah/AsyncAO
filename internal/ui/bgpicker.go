package ui

// Background picker: a "change background" modal. AO has no packet listing a server's backgrounds (BN carries only
// the current one), so the list comes from the asset host's background/ autoindex; an explicit "/bg" button asks the
// server to change it.

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// bgListCap bounds the parsed background list (hard rule 4) against a huge autoindex.
	bgListCap = 4096
	// bgFetchTimeout caps the directory-listing download.
	bgFetchTimeout = 15 * time.Second
	// bgThumbPart is the part rendered as each thumbnail; a miss falls back to the folder name.
	bgThumbPart = "defenseempty"
	// Landscape thumbnail cells (classic 4:3 court aspect).
	bgCellW      int32 = 160
	bgCellH      int32 = 120
	bgCellGap    int32 = 10
	bgCellLabelH int32 = 14
	// bgThumbBudgetBytes caps resident thumbnail textures. Thumbnails are full-resolution backgrounds, so too many
	// thrash T1 and cells flicker black; overflow cells show the name. Byte-based, so small backgrounds all still show.
	bgThumbBudgetBytes int64 = 52 << 20
	// bgThumbEstBytes seeds the per-thumb estimate before any has resolved.
	bgThumbEstBytes int64 = 4 << 20
)

// bgFetch is one directory-listing result, tagged with its server so a fetch landing after a tab switch is ignored.
type bgFetch struct {
	key   string
	names []string
	err   error
}

// bgPicker holds the modal state; the App embeds one as a.bgPick.
type bgPicker struct {
	show     bool
	list     []string // current + remembered + favorites, then the autoindex names
	lower    []string // lowercased, parallel — search filter
	fav      []bool   // parallel to list — starred (favorite) on this server
	favOnly  bool     // grid filter: show only starred backgrounds
	server   []string // names parsed from the host's background/ autoindex
	sel      string   // the selected background (the /bg target + preview)
	listErr  string
	status   string
	busy     bool
	fetched  bool // a fresh autoindex landed this session (gates the one-shot fetch)
	search   string
	scroll   int32
	ask      []time.Time // demand pacing, parallel to list
	pages    []*render.TexturePage
	pagesGen uint64
	thumbMax int64 // largest observed thumbnail footprint (bytes), for the demand budget
	q        loweredCache
	res      chan bgFetch
	forKey   string // serverKey the current list belongs to
}

// openBgPicker shows the modal and starts (or reuses) the listing fetch.
func (a *App) openBgPicker() {
	a.bgPick.show = true
	if a.bgPick.sel == "" {
		a.bgPick.sel = a.sess.Background // default the /bg target to what's up now
	}
	a.ensureBgList()
}

// ensureBgList rebuilds the list from local seeds at once and fetches background/ once via Manager.FetchRaw (cached).
func (a *App) ensureBgList() {
	if a.bgPick.forKey != a.serverKey {
		// New server: seed from last session's cached discovery, then refresh. Cloned so prefs stay unmutated.
		a.bgPick.server = append([]string(nil), a.d.Prefs.Load().ServerWarmInfoFor(a.serverKey).Backgrounds...)
		a.bgPick.fetched = false
		a.bgPick.listErr = ""
		a.bgPick.forKey = a.serverKey
	}
	a.rebuildBgList()
	if a.bgPick.res == nil {
		a.bgPick.res = make(chan bgFetch, 1)
	}
	// Fetch once per server per session, gated on fetched, not on the seeded list.
	if a.bgPick.fetched || a.bgPick.busy || a.urls.Origin() == "" {
		return
	}
	a.bgPick.busy = true
	a.bgPick.listErr = ""
	listURL := a.urls.BackgroundsRoot()
	key := a.serverKey
	ch := a.bgPick.res
	mgr := a.d.Manager
	go func() {
		// Off the main thread, so no caller recovers a panic here: log it and show no listing.
		defer func() {
			if r := recover(); r != nil {
				writeCrashLog("background list goroutine panic: ", r)
				ch <- bgFetch{key: key, err: fmt.Errorf("background list failed: %v", r)}
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), bgFetchTimeout)
		defer cancel()
		data, err := mgr.FetchRaw(ctx, listURL)
		if err != nil {
			ch <- bgFetch{key: key, err: err}
			return
		}
		ch <- bgFetch{key: key, names: parseAutoindexDirs(data)}
	}()
}

// pollBgList drains a finished fetch (key-guarded against tab switches), then rebuilds the merged list.
func (a *App) pollBgList() {
	select {
	case res := <-a.bgPick.res:
		if res.key != a.serverKey {
			return // landed after a tab switch: not this server's list
		}
		a.bgPick.busy = false
		a.bgPick.fetched = true // one fetch per server per session
		if res.err != nil {
			// Keep any cached seed; flag an error only when there is nothing to show.
			if len(a.bgPick.server) == 0 {
				a.bgPick.listErr = "no directory listing (" + res.err.Error() + ") — type/preview a known name or use the current one"
			}
		} else {
			a.bgPick.server = res.names
			a.d.Prefs.RememberServerBackgrounds(a.serverKey, res.names) // cache for next session
		}
		a.rebuildBgList()
	default:
	}
}

// rebuildBgList merges the menu: current background, last remembered, starred favorites, then autoindex names
// (case-insensitive dedup). fav is parallel to the list for the per-cell star.
func (a *App) rebuildBgList() {
	favList := a.d.Prefs.Load().FavBackgroundList(a.serverKey)
	favSet := make(map[string]struct{}, len(favList))
	for _, n := range favList {
		favSet[strings.ToLower(strings.TrimSpace(n))] = struct{}{}
	}
	seen := make(map[string]struct{}, len(a.bgPick.server)+len(favList)+2)
	names := make([]string, 0, len(a.bgPick.server)+len(favList)+2)
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n == "" {
			return
		}
		k := strings.ToLower(n)
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		names = append(names, n)
	}
	if a.sess != nil { // nil when the Scene Maker is opened offline (Settings → Studio → Edit)
		add(a.sess.Background)
	}
	add(a.d.Prefs.Load().ServerWarmInfoFor(a.serverKey).Background)
	for _, n := range favList {
		add(n)
	}
	for _, n := range a.bgPick.server {
		add(n)
	}
	a.bgPick.list = names
	a.bgPick.lower = make([]string, len(names))
	a.bgPick.fav = make([]bool, len(names))
	for i, n := range names {
		a.bgPick.lower[i] = strings.ToLower(n)
		_, a.bgPick.fav[i] = favSet[a.bgPick.lower[i]]
	}
	a.bgPick.ask = nil
	// Starring reorders at the same length and cachedPage keys by index without re-checking the URL, so drop the
	// idx-to-page cache or a reorder shows the previous name's thumbnail.
	a.bgPick.pages = nil
}

// requestBackground applies a pick: a live session asks the server via /bg; rehearsal applies it locally.
func (a *App) requestBackground(name string) {
	if name == "" {
		return
	}
	if a.rehearsal || a.sess == nil {
		if a.sess != nil {
			a.sess.Background = name
		}
		if a.room != nil {
			a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: name})
		}
		a.bgPick.status = "Background set locally: " + name
		return
	}
	a.sess.SendOOC(a.oocNameOrDefault(), "/bg "+name)
	a.bgPick.status = "Requested /bg " + name + " — the server applies it if your area is unlocked (or you're a mod)."
}

func (a *App) drawBgPanel(w, h int32) {
	c := a.ctx
	a.pollBgList()
	// Kept clear of the app-chrome band (chrometop.go), which paints after the screens.
	top := a.topChromeH()
	panel := sdl.Rect{X: pad * 3, Y: top + pad*3, W: w - pad*6, H: h - top - pad*6}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+8, "Backgrounds — preview & change", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: panel.Y + 8, W: 90, H: btnH}, "Close") {
		a.bgPick.show = false
		return
	}

	y := panel.Y + 44
	cur := a.sess.Background
	if cur == "" {
		cur = "(none)"
	}
	c.LabelClipped(panel.X+pad, y+4, 300, "Current: "+cur, ColAccent)
	if a.bgPick.sel != "" {
		label := "Change to '" + clampLine(a.bgPick.sel) + "' (/bg)"
		if a.rehearsal {
			label = "Show '" + clampLine(a.bgPick.sel) + "' (local)"
		}
		bw := c.TextWidth(label) + 24
		if c.Button(sdl.Rect{X: panel.X + panel.W - pad - bw, Y: y, W: bw, H: btnH}, label) {
			a.requestBackground(a.bgPick.sel)
		}
	}
	y += 32

	a.bgPick.search, _ = c.TextField("bgsearch", sdl.Rect{X: panel.X + pad, Y: y, W: 230, H: fieldH}, a.bgPick.search, "Search...")
	// Favorites-only filter: stars are per server, so this works even on a host with no listing.
	a.bgPick.favOnly = c.Checkbox(panel.X+pad+244, y+(fieldH-16)/2, "Favorites only", a.bgPick.favOnly)
	statusX := panel.X + pad + 380
	switch {
	case a.bgPick.busy:
		c.Label(statusX, y+4, "Fetching background list...", ColTextDim)
	case a.bgPick.status != "":
		c.LabelClipped(statusX, y+4, panel.X+panel.W-statusX-pad, a.bgPick.status, ColAccent)
	case a.bgPick.listErr != "":
		c.LabelClipped(statusX, y+4, panel.X+panel.W-statusX-pad, a.bgPick.listErr, ColTextDim)
	default:
		c.Label(statusX, y+4, fmt.Sprintf("%d backgrounds — hover to preview, click to select", len(a.bgPick.list)), ColTextDim)
	}
	y += 36

	// Grid of landscape thumbnails.
	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (bgCellW + bgCellGap)
	if cols < 1 {
		cols = 1
	}
	query := a.bgPick.q.get(a.bgPick.search)
	// No search and no favorites filter: every entry matches, so skip the full-list scan.
	matches := int32(len(a.bgPick.list))
	if query != "" || a.bgPick.favOnly {
		matches = 0
		for i := range a.bgPick.list {
			if query != "" && !strings.Contains(a.bgPick.lower[i], query) {
				continue
			}
			if a.bgPick.favOnly && !a.bgPick.fav[i] {
				continue
			}
			matches++
		}
	}
	cellStride := bgCellH + bgCellGap + bgCellLabelH
	contentH := (matches + cols - 1) / cols * cellStride
	visibleH := panel.Y + panel.H - gridTop - pad

	a.bgPick.scroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.bgPick.scroll = c.VScrollbar("bgscroll", track, a.bgPick.scroll, contentH, visibleH)

	// Clip the grid so a partially scrolled row stays inside the panel.
	dlOn := a.d.Prefs.Load().CharDownloaderOn // read once per frame, not per cell
	// Texture budget for this frame's visible thumbnails; once spent, cells show their name. See bgThumbBudgetBytes.
	thumbBudget := bgThumbBudgetBytes
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	col, row := int32(0), int32(0)
	for i := range a.bgPick.list {
		if query != "" && !strings.Contains(a.bgPick.lower[i], query) {
			continue
		}
		if a.bgPick.favOnly && !a.bgPick.fav[i] {
			continue
		}
		x := panel.X + pad + col*(bgCellW+bgCellGap)
		yy := gridTop + row*cellStride - a.bgPick.scroll
		if yy > gridTop-bgCellH && yy < panel.Y+panel.H {
			a.drawBgCell(i, sdl.Rect{X: x, Y: yy, W: bgCellW, H: bgCellH}, dlOn, &thumbBudget)
		}
		col++
		if col >= cols {
			col = 0
			row++
		}
	}
	c.popClip(clipPrev, clipHad)

	if a.previewBase != "" {
		a.drawSpritePreview(w, h, false, "") // background preview: no emote-name caption
		a.dismissPreviewOnClick()            // pin-aware, and disarms the trigger's dwell id
	}
}

func (a *App) drawBgCell(idx int, cell sdl.Rect, downloaderOn bool, budget *int64) {
	c := a.ctx
	name := a.bgPick.list[idx]
	c.Fill(cell, ColBackground)
	base := a.urls.Background(name, bgThumbPart)
	if page, ok := a.cachedPage(&a.bgPick.pages, &a.bgPick.pagesGen, len(a.bgPick.list), idx, base); ok && len(page.Frames) > 0 {
		// Resident: always draw it and bill its footprint.
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
		b := int64(page.W) * int64(page.H) * 4
		*budget -= b
		if b > a.bgPick.thumbMax {
			a.bgPick.thumbMax = b // learn the real per-thumb size for the estimate
		}
	} else if est := max(a.bgPick.thumbMax, bgThumbEstBytes); *budget >= est {
		// Not resident and budget remains: demand it, billing the estimate to avoid over-committing.
		*budget -= est
		a.demandAsset(&a.bgPick.ask, len(a.bgPick.list), idx, base, assets.AssetTypeBackground) // AssetType: Background (picker thumb)
		c.LabelClipped(cell.X+4, cell.Y+cell.H/2-8, cell.W-8, name, ColTextDim)
	} else {
		// Budget spent: show the name (scrolling brings it into budget) without demanding a texture.
		c.LabelClipped(cell.X+4, cell.Y+cell.H/2-8, cell.W-8, name, ColTextDim)
	}
	c.Border(cell, ColPanelHi)

	// The selected cell gets the accent ring; the live background gets a "current" tag.
	if strings.EqualFold(name, a.bgPick.sel) {
		c.Border(sdl.Rect{X: cell.X - 2, Y: cell.Y - 2, W: cell.W + 4, H: cell.H + 4}, ColAccent)
	}
	if strings.EqualFold(name, a.sess.Background) {
		tag := sdl.Rect{X: cell.X + 1, Y: cell.Y + 1, W: c.TextWidth("current") + 8, H: 16}
		c.Fill(tag, sdl.Color{R: 0, G: 0, B: 0, A: 190})
		c.Label(tag.X+4, tag.Y+1, "current", ColAccent)
	}
	c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, name, ColTextDim)

	// Favorite star: pins this background on this server (persisted); starred ones float to the top.
	starred := idx < len(a.bgPick.fav) && a.bgPick.fav[idx]
	star := sdl.Rect{X: cell.X + cell.W - 22, Y: cell.Y + 2, W: 20, H: 18}
	c.Fill(star, sdl.Color{R: 0, G: 0, B: 0, A: 150}) // backing: the star must read over busy thumbnails
	starCol := ColTextDim
	if starred {
		starCol = ColStar
	}
	c.IconLabel(star, "★", starCol)
	c.Tooltip(star, "Favorite this background (saved per server)")
	if c.hovering(star) && c.clicked {
		if starred {
			a.d.Prefs.RemoveFavBackground(a.serverKey, name)
		} else {
			a.d.Prefs.AddFavBackground(a.serverKey, name)
		}
		a.rebuildBgList()
		return // the star claimed the click; don't also select this background
	}

	// Mark the cell while this background is downloading or queued.
	if a.dl.active && a.dl.target == name {
		c.Fill(cell, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 70})
		c.Label(cell.X+4, cell.Y+4, downloadGlyph+"…", ColText)
	} else if downloaderOn && a.dlQueuedTarget(name) {
		c.Label(cell.X+4, cell.Y+4, downloadGlyph+"·", ColTextDim) // waiting in queue
	}
	// Download badge (opt-in downloader only): grabs the whole folder; right-clicking the cell does the same.
	if downloaderOn && a.drawDownloadBadge(cell, "Download / queue this background (or right-click the cell)") {
		a.startBgDownload(name)
		return
	}
	if downloaderOn && c.rightClicked && c.hovering(cell) {
		a.startBgDownload(name)
		return
	}

	// Hover shows the large preview at high priority; click selects the /bg target.
	if c.HoverPreview("bg:"+name, cell) {
		a.previewBase = base
		a.d.Manager.Prefetch(base, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background (preview)
	}
	if c.hovering(cell) && c.clicked {
		a.bgPick.sel = name
		a.bgPick.status = ""
		a.previewBase = base
		a.d.Manager.Prefetch(base, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background (selected preview)
	}
}

// autoindexEntry is one parsed directory-listing link.
type autoindexEntry struct {
	href string // raw URL segment from the listing (already escaped; dirs keep the trailing /)
	name string // decoded display / filesystem name
	dir  bool   // trailing slash in the listing
}

// autoindexHref captures href targets from an HTML directory listing; [^"?#] drops Apache's ?C=N;O=A sort links.
var autoindexHref = regexp.MustCompile(`(?i)href="([^"?#]+)"`)

// cleanAutoindexEntry normalizes one raw href into an entry and reports whether it is usable, one level deep and
// non-escaping. It is the security boundary for the downloader: ../, ./, /x, x://, a/b and any raw or percent-encoded
// ".." are rejected.
func cleanAutoindexEntry(raw string) (autoindexEntry, bool) {
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, "://") {
		return autoindexEntry{}, false
	}
	dir := strings.HasSuffix(raw, "/")
	name := strings.TrimSuffix(raw, "/")
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.Contains(name, "..") {
		return autoindexEntry{}, false
	}
	if strings.ContainsAny(name, `/\`) { // one level deep only
		return autoindexEntry{}, false
	}
	return autoindexEntry{href: raw, name: name, dir: dir}, true
}

// parseAutoindexEntries returns up to limit unique files and dirs; a non-autoindex response yields nothing.
func parseAutoindexEntries(data []byte, limit int) []autoindexEntry {
	out := make([]autoindexEntry, 0)
	seen := make(map[string]struct{})
	for _, m := range autoindexHref.FindAllSubmatch(data, -1) {
		e, ok := cleanAutoindexEntry(string(m[1]))
		if !ok {
			continue
		}
		key := strings.ToLower(e.name)
		if e.dir {
			key += "/"
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// parseAutoindexDirs is the picker's discovery list: sorted, de-duplicated folder names from a listing.
func parseAutoindexDirs(data []byte) []string {
	entries := parseAutoindexEntries(data, bgListCap)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.dir {
			names = append(names, e.name)
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	return names
}
