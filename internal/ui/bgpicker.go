package ui

// Background picker: a "change background" modal modeled on the wardrobe /
// iniswap menu. AO has no protocol packet that lists a server's backgrounds
// (BN only ever carries the CURRENT one), so the list is discovered the same
// way iniswap.txt seeds the wardrobe — by fetching the asset host's
// background/ directory and parsing its autoindex (nginx/apache/caddy all
// emit <a href="folder/"> links). Each folder shows a defenseempty
// thumbnail; hovering or selecting one shows a large preview, and an explicit
// "/bg" button asks the server to change it for the area.

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

const (
	// bgListCap bounds the parsed background list (rule §17.4, like
	// iniswapListCap) — a hostile or huge autoindex can't grow it unbounded.
	bgListCap = 4096
	// bgFetchTimeout caps the directory-listing download.
	bgFetchTimeout = 15 * time.Second
	// bgThumbPart is the background part rendered as each cell's thumbnail.
	// defenseempty ships in essentially every AO courtroom background; a
	// miss just falls back to the folder name (same as a char with no icon).
	bgThumbPart = "defenseempty"
	// Landscape thumbnail cells (AO's classic 4:3 court aspect).
	bgCellW      int32 = 160
	bgCellH      int32 = 120
	bgCellGap    int32 = 10
	bgCellLabelH int32 = 14
)

// bgFetch is one background-directory-listing result, tagged with the server
// it was made for so a fetch that lands after a tab switch is ignored.
type bgFetch struct {
	key   string
	names []string
	err   error
}

// bgPicker holds the "change background" modal state. Self-contained
// (mirrors settingsState): the App embeds one as a.bgPick.
type bgPicker struct {
	show     bool
	list     []string // current + remembered, then the autoindex names
	lower    []string // lowercased, parallel — search filter
	server   []string // names parsed from the host's background/ autoindex
	sel      string   // the selected background (the /bg target + preview)
	listErr  string
	status   string
	busy     bool
	search   string
	scroll   int32
	ask      []time.Time // demand pacing, parallel to list
	pages    []*render.TexturePage
	pagesGen uint64
	q        loweredCache
	res      chan bgFetch
	forKey   string // serverKey the current list belongs to
}

// openBgPicker shows the modal and kicks off (or reuses) the listing fetch.
func (a *App) openBgPicker() {
	a.bgPick.show = true
	if a.bgPick.sel == "" {
		a.bgPick.sel = a.sess.Background // default the /bg target to what's up now
	}
	a.ensureBgList()
}

// ensureBgList rebuilds the list from local seeds immediately (so the modal
// is never empty) and fetches the server's background/ autoindex once. The
// fetch rides Manager.FetchRaw (T2 + disk cached, singleflight), so a reopen
// is a memory hit — same path as the iniswap.txt fetch.
func (a *App) ensureBgList() {
	if a.bgPick.forKey != a.serverKey {
		// New server since the last open: drop the stale list/caches.
		a.bgPick.server = nil
		a.bgPick.listErr = ""
		a.bgPick.forKey = a.serverKey
	}
	a.rebuildBgList()
	if a.bgPick.res == nil {
		a.bgPick.res = make(chan bgFetch, 1)
	}
	if a.bgPick.server != nil || a.bgPick.busy || a.urls.Origin() == "" {
		return
	}
	a.bgPick.busy = true
	a.bgPick.listErr = ""
	listURL := a.urls.BackgroundsRoot()
	key := a.serverKey
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), bgFetchTimeout)
		defer cancel()
		data, err := a.d.Manager.FetchRaw(ctx, listURL)
		if err != nil {
			a.bgPick.res <- bgFetch{key: key, err: err}
			return
		}
		a.bgPick.res <- bgFetch{key: key, names: parseAutoindexDirs(data)}
	}()
}

// pollBgList drains a finished listing fetch (key-guarded against tab
// switches), then rebuilds the merged list.
func (a *App) pollBgList() {
	select {
	case res := <-a.bgPick.res:
		if res.key != a.serverKey {
			return // landed after a tab switch: not this server's list
		}
		a.bgPick.busy = false
		if res.err != nil {
			a.bgPick.listErr = "no directory listing (" + res.err.Error() + ") — type/preview a known name or use the current one"
			a.bgPick.server = nil
		} else {
			a.bgPick.server = res.names
		}
		a.rebuildBgList()
	default:
	}
}

// rebuildBgList merges the menu: the current background first, then the last
// remembered one, then every name from the autoindex (de-duplicated,
// case-insensitively). Seeds float to the top so "what I'm looking at" is
// always reachable even on a host with no listing.
func (a *App) rebuildBgList() {
	seen := make(map[string]struct{}, len(a.bgPick.server)+2)
	names := make([]string, 0, len(a.bgPick.server)+2)
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
	add(a.sess.Background)
	add(a.d.Prefs.ServerWarmInfoFor(a.serverKey).Background)
	for _, n := range a.bgPick.server {
		add(n)
	}
	a.bgPick.list = names
	a.bgPick.lower = make([]string, len(names))
	for i, n := range names {
		a.bgPick.lower[i] = strings.ToLower(n)
	}
	a.bgPick.ask = nil
}

// requestBackground applies a pick: a live session asks the server via /bg
// (the server enforces permissions and broadcasts BN to everyone); rehearsal
// has no server, so it applies locally to preview the scene on the stage.
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
	panel := sdl.Rect{X: pad * 3, Y: pad * 3, W: w - pad*6, H: h - pad*6}
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
	statusX := panel.X + pad + 250
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

	// Grid of landscape thumbnails (clone of the iniswap layout).
	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (bgCellW + bgCellGap)
	if cols < 1 {
		cols = 1
	}
	query := a.bgPick.q.get(a.bgPick.search)
	// No search → every entry matches; skip the per-frame full-list scan.
	matches := int32(len(a.bgPick.list))
	if query != "" {
		matches = 0
		for i := range a.bgPick.list {
			if strings.Contains(a.bgPick.lower[i], query) {
				matches++
			}
		}
	}
	cellStride := bgCellH + bgCellGap + bgCellLabelH
	contentH := (matches + cols - 1) / cols * cellStride
	visibleH := panel.Y + panel.H - gridTop - pad

	a.bgPick.scroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.bgPick.scroll = c.VScrollbar("bgscroll", track, a.bgPick.scroll, contentH, visibleH)

	// Clip the grid so a partially scrolled top/bottom row stays inside the
	// panel (same overspill guard as the log lists).
	dlOn := a.d.Prefs.CharDownloaderEnabled() // read once per frame, not per cell
	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	col, row := int32(0), int32(0)
	for i := range a.bgPick.list {
		if query != "" && !strings.Contains(a.bgPick.lower[i], query) {
			continue
		}
		x := panel.X + pad + col*(bgCellW+bgCellGap)
		yy := gridTop + row*cellStride - a.bgPick.scroll
		if yy > gridTop-bgCellH && yy < panel.Y+panel.H {
			a.drawBgCell(i, sdl.Rect{X: x, Y: yy, W: bgCellW, H: bgCellH}, dlOn)
		}
		col++
		if col >= cols {
			col = 0
			row++
		}
	}
	c.popClip(clipPrev, clipHad)

	if a.previewBase != "" {
		a.drawSpritePreview(w, h)
		if c.clicked {
			a.previewBase = ""
		}
	}
}

func (a *App) drawBgCell(idx int, cell sdl.Rect, downloaderOn bool) {
	c := a.ctx
	name := a.bgPick.list[idx]
	c.Fill(cell, ColBackground)
	base := a.urls.Background(name, bgThumbPart)
	if page, ok := a.cachedPage(&a.bgPick.pages, &a.bgPick.pagesGen, len(a.bgPick.list), idx, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &cell)
	} else {
		a.demandAsset(&a.bgPick.ask, len(a.bgPick.list), idx, base, assets.AssetTypeBackground) // AssetType: Background (picker thumb)
		c.LabelClipped(cell.X+4, cell.Y+cell.H/2-8, cell.W-8, name, ColTextDim)
	}
	c.Border(cell, ColPanelHi)

	// The selected cell gets the accent ring; the live background gets a
	// "current" tag so the two states read at a glance.
	if strings.EqualFold(name, a.bgPick.sel) {
		c.Border(sdl.Rect{X: cell.X - 2, Y: cell.Y - 2, W: cell.W + 4, H: cell.H + 4}, ColAccent)
	}
	if strings.EqualFold(name, a.sess.Background) {
		tag := sdl.Rect{X: cell.X + 1, Y: cell.Y + 1, W: c.TextWidth("current") + 8, H: 16}
		c.Fill(tag, sdl.Color{R: 0, G: 0, B: 0, A: 190})
		c.Label(tag.X+4, tag.Y+1, "current", ColAccent)
	}
	c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, name, ColTextDim)

	// Download badge (only when the opt-in downloader is on): grabs this
	// background's whole folder for offline use. Claims its own click so it
	// never doubles as a select.
	if downloaderOn {
		get := sdl.Rect{X: cell.X + cell.W - 38, Y: cell.Y + cell.H - 20, W: 36, H: 18}
		c.Fill(get, sdl.Color{R: 0, G: 0, B: 0, A: 200})
		c.Label(get.X+4, get.Y+1, "Get", ColAccent)
		if c.hovering(get) && c.clicked {
			a.startBgDownload(name)
			return
		}
	}

	// Hover → large preview (the actual defenseempty image, high priority);
	// click → select it as the /bg target (and keep the preview up).
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

// autoindexHref captures href targets from an HTML directory listing. nginx,
// Apache and Caddy all emit <a href="...">; [^"?#] drops Apache's
// ?C=N;O=A column-sort links and fragment targets.
var autoindexHref = regexp.MustCompile(`(?i)href="([^"?#]+)"`)

// cleanAutoindexEntry normalizes one raw href into an entry and reports
// whether it's a usable, one-level-deep, non-escaping link. Pure (unit
// tested) — it is the SECURITY boundary for the downloader: parent (../),
// self (./), absolute (/x), external (x://), nested (a/b) and any ".."
// (raw OR percent-encoded like %2e%2e) are rejected so a hostile listing
// can never write outside the destination folder.
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

// parseAutoindexEntries returns up to limit unique files+dirs from a listing
// page. On a non-autoindex response (custom 403/404 HTML) it finds no
// qualifying links and returns nothing — never garbage names.
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

// parseAutoindexDirs is the background picker's discovery list: the sorted,
// de-duplicated folder names from a directory listing.
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
