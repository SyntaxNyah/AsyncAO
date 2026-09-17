package ui

// Evidence image picker (Issue #119): the "Choose" button beside the editor's
// Image-file field opens a modal grid of evidence images so you can pick one by
// its icon instead of typing a filename. Discovery is source-aware — the local
// mount's evidence/ folder in local/layered mode, the server's evidence/
// autoindex in stream/layered mode — plus the case evidence already on hand.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	evidPickerCap      = 4096
	evidPickerFetchTO  = 15 * time.Second
	evidPickerCell     = int32(64)
	evidPickerCellGap  = int32(8)
	evidPickerNameBand = int32(16)
)

// openEvidencePicker opens the picker and starts discovery for the right source.
func (a *App) openEvidencePicker() {
	if a.evidPickerRes == nil {
		a.evidPickerRes = make(chan []string, 4)
	}
	if a.evidPickerFiles == nil {
		// Seed with the case evidence already on hand so the modal is never empty
		// while the local scan / server index runs.
		seen := map[string]bool{}
		for _, it := range a.sess.Evidence {
			name := strings.TrimSpace(it.Image)
			if name == "" || seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			a.evidPickerFiles = append(a.evidPickerFiles, name)
		}
		a.evidPickerLower = make([]string, len(a.evidPickerFiles))
		for i, n := range a.evidPickerFiles {
			a.evidPickerLower[i] = strings.ToLower(n)
		}
	}
	a.evidPickerOpen = true
	a.evidPickerScroll = 0
	a.evidPickerSearch = ""
	a.discoverEvidencePicker()
}

// discoverEvidencePicker kicks off the source-appropriate, off-thread discovery
// (hard rule 2: no synchronous disk/network on the render path). Each source
// posts to evidPickerRes and pollEvidencePicker merges it.
func (a *App) discoverEvidencePicker() {
	mode := a.assetSourceMode()
	if (mode == assetSrcLocal || mode == assetSrcLayered) && !a.evidPickerScanned {
		a.evidPickerScanned = true
		go a.scanLocalEvidenceFiles()
	}
	if (mode == assetSrcStream || mode == assetSrcLayered) && !a.evidPickerBusy && a.urls.Origin() != "" {
		a.evidPickerBusy = true
		go a.fetchEvidenceIndex()
	}
}

// scanLocalEvidenceFiles lists image files under each folder mount's evidence/
// directory (zip mounts are skipped — the server index covers those in layered
// mode). Bounded by evidPickerCap.
func (a *App) scanLocalEvidenceFiles() {
	defer func() {
		if r := recover(); r != nil {
			writeCrashLog("evidence picker local scan panic: ", r)
		}
	}()
	_, mounts := a.d.Prefs.LocalAssets()
	seen := map[string]bool{}
	var out []string
	for _, m := range mounts {
		if strings.EqualFold(filepath.Ext(m), ".zip") {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(m, "evidence"))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if name == "" || strings.HasPrefix(name, ".") || seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			out = append(out, name)
			if len(out) >= evidPickerCap {
				break
			}
		}
		if len(out) >= evidPickerCap {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	a.evidPickerRes <- out
}

// fetchEvidenceIndex fetches the server's evidence/ autoindex and extracts image
// filenames (the same discovery trick as the background picker — AO has no list
// packet). Bounded by evidPickerCap.
func (a *App) fetchEvidenceIndex() {
	defer func() {
		if r := recover(); r != nil {
			writeCrashLog("evidence picker index panic: ", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), evidPickerFetchTO)
	defer cancel()
	data, err := a.d.Manager.FetchRaw(ctx, a.urls.EvidenceRoot())
	if err != nil {
		return
	}
	entries := parseAutoindexEntries(data, evidPickerCap)
	var out []string
	for _, e := range entries {
		if e.dir {
			continue // only image files, not nested folders
		}
		out = append(out, e.name)
	}
	a.evidPickerRes <- out
}

// pollEvidencePicker drains finished discovery results into the file list.
func (a *App) pollEvidencePicker() {
	if a.evidPickerRes == nil {
		return
	}
	for {
		select {
		case names := <-a.evidPickerRes:
			a.mergeEvidencePickerFiles(names)
		default:
			return
		}
	}
}

// mergeEvidencePickerFiles appends discovered names, de-duplicated case-
// insensitively and capped at evidPickerCap.
func (a *App) mergeEvidencePickerFiles(names []string) {
	have := make(map[string]bool, len(a.evidPickerFiles))
	for _, n := range a.evidPickerFiles {
		have[strings.ToLower(n)] = true
	}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || len(a.evidPickerFiles) >= evidPickerCap {
			break
		}
		k := strings.ToLower(n)
		if have[k] {
			continue
		}
		have[k] = true
		a.evidPickerFiles = append(a.evidPickerFiles, n)
		a.evidPickerLower = append(a.evidPickerLower, k)
	}
}

// drawEvidencePicker is the "Choose evidence image" modal: a search field above
// a scrollable grid of evidence-image thumbnails; clicking a cell fills the
// editor's Image file and closes.
func (a *App) drawEvidencePicker(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 160})
	const mw, mh = 520, 440
	m := sdl.Rect{X: (w - mw) / 2, Y: (h - mh) / 2, W: mw, H: mh}
	c.Fill(m, ColPanel)
	c.Border(m, ColAccent)
	c.Heading(m.X+pad, m.Y+pad, "Choose evidence image", ColText)
	if c.Button(sdl.Rect{X: m.X + mw - 80 - pad, Y: m.Y + pad - 4, W: 80, H: btnH}, "Close") {
		a.evidPickerOpen = false
		return
	}
	y := m.Y + pad + 40
	a.evidPickerSearch, _ = c.TextField("evidpickersearch", sdl.Rect{X: m.X + pad, Y: y, W: mw - 2*pad, H: fieldH}, a.evidPickerSearch, "filter images…")
	y += fieldH + 10

	grid := sdl.Rect{X: m.X + pad, Y: y, W: mw - 2*pad, H: m.Y + mh - pad - y}
	clipPrev, clipHad := c.pushClip(grid)
	cellW := grid.W - scrollBarW
	cols := cellW / (evidPickerCell + evidPickerCellGap)
	if cols < 1 {
		cols = 1
	}
	q := strings.ToLower(a.evidPickerSearch)
	names := make([]string, 0, len(a.evidPickerFiles))
	for i, n := range a.evidPickerFiles {
		if q == "" || strings.Contains(a.evidPickerLower[i], q) {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		c.LabelClipped(grid.X, grid.Y+4, grid.W, "No evidence images found.", ColTextDim)
		c.popClip(clipPrev, clipHad)
		return
	}
	rows := (int32(len(names)) + cols - 1) / cols
	contentH := rows*(evidPickerCell+evidPickerCellGap+evidPickerNameBand) - evidPickerCellGap
	a.evidPickerScroll -= c.WheelIn(grid) * scrollStepPx
	track := sdl.Rect{X: grid.X + grid.W - scrollBarW, Y: grid.Y, W: scrollBarW, H: grid.H}
	a.evidPickerScroll = c.VScrollbar("evidpicker", track, a.evidPickerScroll, contentH, grid.H)
	for i, name := range names {
		col, row := int32(i)%cols, int32(i)/cols
		cx := grid.X + col*(evidPickerCell+evidPickerCellGap)
		cy := grid.Y + row*(evidPickerCell+evidPickerCellGap+evidPickerNameBand) - a.evidPickerScroll
		if cy+evidPickerCell+evidPickerNameBand <= grid.Y || cy >= grid.Y+grid.H {
			continue
		}
		rc := sdl.Rect{X: cx, Y: cy, W: evidPickerCell, H: evidPickerCell}
		url := a.urls.Evidence(name)
		if page, ok := a.d.Store.Get(url); ok && len(page.Frames) > 0 {
			_ = c.Ren.Copy(page.Frames[0], nil, &rc)
		} else {
			c.Fill(rc, ColPanelHi)
			a.d.Manager.PrefetchExact(url, assets.AssetTypeMisc, network.PriorityLow) // AssetType: Misc (evidence picker thumbnail)
		}
		c.Border(rc, ColPanelHi)
		c.LabelClipped(cx, cy+evidPickerCell+2, evidPickerCell, name, ColTextDim)
		if c.hovering(rc) && c.clicked {
			a.evidImage = name
			a.evidPickerOpen = false
		}
	}
	c.popClip(clipPrev, clipHad)
}

