package ui

// Server evidence image picker (#119, stream mode): the evidence editor's "Browse…"
// button, when "Stream from server" is on, opens this modal grid of the server's
// evidence/ autoindex instead of the local folder browser. Discovery is one
// off-thread fetch of the autoindex (hard rule 2: no sync network on the render
// path); the modal is seeded with the case's own evidence so it is never empty
// while the index runs.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	evidServerCap      = 4096
	evidServerFetchTO  = 15 * time.Second
	evidServerCell     = int32(64)
	evidServerCellGap  = int32(8)
	evidServerNameBand = int32(16)
)

// openServerEvidencePicker opens the modal and starts the server-index discovery.
func (a *App) openServerEvidencePicker() {
	if a.evidServerRes == nil {
		a.evidServerRes = make(chan []string, 4)
	}
	if a.evidServerNames == nil {
		// Seed with the case evidence already on hand so the modal is never empty
		// while the index fetch runs.
		seen := map[string]bool{}
		for _, it := range a.sess.Evidence {
			name := strings.TrimSpace(it.Image)
			if name == "" || seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			a.evidServerNames = append(a.evidServerNames, name)
		}
		a.evidServerLower = make([]string, len(a.evidServerNames))
		for i, n := range a.evidServerNames {
			a.evidServerLower[i] = strings.ToLower(n)
		}
	}
	a.evidServerPickerOpen = true
	a.evidServerScroll = 0
	a.evidServerSearch = ""
	if !a.evidServerBusy && a.urls.Origin() != "" {
		a.evidServerBusy = true
		go a.fetchServerEvidenceIndex()
	}
}

// fetchServerEvidenceIndex fetches the server's evidence/ autoindex and extracts
// image filenames (AO has no evidence-list packet). Bounded by evidServerCap.
func (a *App) fetchServerEvidenceIndex() {
	defer func() {
		if r := recover(); r != nil {
			writeCrashLog("server evidence picker index panic: ", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), evidServerFetchTO)
	defer cancel()
	data, err := a.d.Manager.FetchRaw(ctx, a.urls.EvidenceRoot())
	if err != nil {
		a.evidServerRes <- nil
		return
	}
	entries := parseAutoindexEntries(data, evidServerCap)
	var out []string
	for _, e := range entries {
		if e.dir {
			continue // only image files, not nested folders
		}
		out = append(out, e.name)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	a.evidServerRes <- out
}

// pollServerEvidencePicker drains a finished index fetch into the name list.
func (a *App) pollServerEvidencePicker() {
	if a.evidServerRes == nil {
		return
	}
	for {
		select {
		case names := <-a.evidServerRes:
			a.mergeServerEvidenceNames(names)
			a.evidServerBusy = false
		default:
			return
		}
	}
}

// mergeServerEvidenceNames appends discovered names, de-duplicated case-insensitively
// and capped at evidServerCap.
func (a *App) mergeServerEvidenceNames(names []string) {
	have := make(map[string]bool, len(a.evidServerNames))
	for _, n := range a.evidServerNames {
		have[strings.ToLower(n)] = true
	}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || len(a.evidServerNames) >= evidServerCap {
			break
		}
		k := strings.ToLower(n)
		if have[k] {
			continue
		}
		have[k] = true
		a.evidServerNames = append(a.evidServerNames, n)
		a.evidServerLower = append(a.evidServerLower, k)
	}
}

// serverEvidenceEmptyLabel picks the empty-grid message: "Loading…" while the
// index fetch is still in flight, a no-match message when a filter is set, and
// the plain empty message otherwise. Pure so the three states are unit-testable.
func serverEvidenceEmptyLabel(busy, hasFilter bool) string {
	switch {
	case busy:
		return "Loading server evidence…"
	case hasFilter:
		return "No server evidence images match."
	default:
		return "No server evidence images found."
	}
}

// drawServerEvidencePicker draws the "Choose evidence image" modal over the server
// autoindex: a search field above a scrollable grid of thumbnails. Clicking a cell
// fills the editor's Image file and closes.
func (a *App) drawServerEvidencePicker(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 520, 440
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Choose evidence image — server", ColText)
	if c.Button(sdl.Rect{X: m.X + mw - 80 - pad, Y: m.Y + pad - 4, W: 80, H: btnH}, "Close") {
		a.evidServerPickerOpen = false
		return
	}
	y := m.Y + pad + 40
	a.evidServerSearch, _ = c.TextField("evidserverpickersearch", sdl.Rect{X: m.X + pad, Y: y, W: mw - 2*pad, H: fieldH}, a.evidServerSearch, "filter server images…")
	y += fieldH + 10

	grid := sdl.Rect{X: m.X + pad, Y: y, W: mw - 2*pad, H: m.Y + mh - pad - y}
	cellW := grid.W - scrollBarW
	cols := cellW / (evidServerCell + evidServerCellGap)
	if cols < 1 {
		cols = 1
	}
	q := strings.ToLower(a.evidServerSearch)
	names := make([]string, 0, len(a.evidServerNames))
	for i, n := range a.evidServerNames {
		if q == "" || strings.Contains(a.evidServerLower[i], q) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		c.LabelClipped(grid.X, grid.Y+4, grid.W, serverEvidenceEmptyLabel(a.evidServerBusy, a.evidServerSearch != ""), ColTextDim)
		return
	}
	rows := (int32(len(names)) + cols - 1) / cols
	rowPitch := evidServerCell + evidServerCellGap + evidServerNameBand
	contentH := rows*rowPitch - evidServerCellGap
	a.evidServerScroll -= c.WheelIn(grid) * rowPitch
	track := sdl.Rect{X: grid.X + grid.W - scrollBarW, Y: grid.Y, W: scrollBarW, H: grid.H}
	a.evidServerScroll = c.VScrollbar("evidserverpicker", track, a.evidServerScroll, contentH, grid.H)
	// Clip AFTER the wheel/scrollbar resolve, like the other icon grids
	// (charGridScroll, drawEvidenceGrid, drawSfxBrowser): hovering() honours clipOn,
	// and the scrollbar's grab slop extends past the grid's right edge, so pushing
	// the clip first leaves the wheel band and the thumb un-hoverable.
	clipPrev, clipHad := c.pushClip(grid)
	for i, name := range names {
		col, row := int32(i)%cols, int32(i)/cols
		cx := grid.X + col*(evidServerCell+evidServerCellGap)
		cy := grid.Y + row*rowPitch - a.evidServerScroll
		if cy+evidServerCell+evidServerNameBand <= grid.Y || cy >= grid.Y+grid.H {
			continue
		}
		rc := sdl.Rect{X: cx, Y: cy, W: evidServerCell, H: evidServerCell}
		url := a.urls.Evidence(name)
		if page, ok := a.d.Store.Get(url); ok && len(page.Frames) > 0 {
			_ = c.Ren.Copy(page.Frames[0], nil, &rc)
		} else {
			c.Fill(rc, ColPanelHi)
			a.d.Manager.PrefetchExact(url, assets.AssetTypeMisc, network.PriorityHigh) // AssetType: Misc (server evidence picker thumbnail)
		}
		c.Border(rc, ColPanelHi)
		c.LabelClipped(cx, cy+evidServerCell+2, evidServerCell, name, ColTextDim)
		if c.hovering(rc) && c.clicked {
			a.evidImage = name
			a.evidServerPickerOpen = false
		}
	}
	c.popClip(clipPrev, clipHad)
}
