package ui

// "Swap from your base": browses the server's SC roster and the character folders in local mounts. A remote HTTP base
// has no directory listing, and probing names is the fallback storm this client avoids, so the type-a-name field stays.
// Separate from a.iniList because that is index-keyed (a.iniPages / a.iniAsk are parallel, cachedPage does not re-check
// the URL) and char select's Wardrobe tab ranges it whole. The local scan runs on a goroutine (hard rule 2).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// iniBaseCharsDir is the sub-folder of a mount that holds character folders; mounts point at a base.
	iniBaseCharsDir = "characters"
	// iniBaseLocalFolderCap bounds one scan's result (hard rule 4).
	iniBaseLocalFolderCap = 4096
	// iniBaseNameCap bounds the merged list, above the local cap so a full scan can still join a large server roster.
	iniBaseNameCap = 8192
	// iniBaseSourceChipW is one source chip in the tab's two-way switch.
	iniBaseSourceChipW = int32(150)
)

type iniBaseScan struct {
	mounts    []string // the mount list this scan was made from (the staleness key)
	names     []string // character folder names, de-duplicated, sorted
	truncated bool     // the walk stopped at iniBaseLocalFolderCap
	err       string   // "" = fine; a human line for the status row
}

// iniBaseBrowser is the tab's state: render thread only, except `res`, which the scan goroutine sends on.
type iniBaseBrowser struct {
	show   bool // the "From your base" source is the selected one
	scroll int32
	// autoDone / autoKey record the one automatic source choice per server, so a deliberate switch back to "Server
	// list" is not undone on the next frame. A server with no iniswap.txt opens on the base browser.
	autoDone bool
	autoKey  string

	res      chan iniBaseScan
	scanning bool
	// scanned is the mount list `local` came from; comparing the list itself keeps the per-frame check allocation-free.
	scanned   []string
	local     []string
	truncated bool
	scanErr   string

	// The merged list, memoized on the three things that can change it.
	names      []string
	lower      []string
	namesKey   string // a.serverKey the list was built for
	namesChars int    // len(a.sess.Chars) it was built from
	namesLocal int    // len(local) it was built from
	truncatedN bool   // the merge itself hit iniBaseNameCap

	// Strings drawn every frame the tab is open, memoized on their counts to keep the render loop allocation-free.
	labelServer  string // "Server list (N)"
	labelBase    string // "From your base (N)"
	labelStatus  string // "<N> on this server · ★ adds one …"
	labelServerN int    // the serverN they were built from
	labelBaseN   int    // ...and the baseN

	// Char icons index-keyed into names; separate because this list's indices must never alias a.iniPages.
	pages    []*render.TexturePage
	pagesGen uint64
	ask      []time.Time
}

// sourceLabels rebuilds the chip labels only when a count moved; labelServer is never legitimately empty, so "" =
// unset.
func (b *iniBaseBrowser) sourceLabels(serverN, baseN int) (server, base, status string) {
	if b.labelServer == "" || b.labelServerN != serverN || b.labelBaseN != baseN {
		b.labelServerN, b.labelBaseN = serverN, baseN
		b.labelServer = fmt.Sprintf("Server list (%d)", serverN)
		b.labelBase = fmt.Sprintf("From your base (%d)", baseN)
		b.labelStatus = fmt.Sprintf("%d on this server · ★ adds one to your Characters wardrobe", serverN)
	}
	return b.labelServer, b.labelBase, b.labelStatus
}

// scanLocalCharFolders returns each mount's character folder names, skipping mounts that can't be opened. Blocking:
// goroutine only.
func scanLocalCharFolders(mounts []string) iniBaseScan {
	out := iniBaseScan{mounts: append([]string(nil), mounts...)}
	seen := make(map[string]struct{})
	skipped := 0
	for _, m := range mounts {
		if strings.TrimSpace(m) == "" {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(m, iniBaseCharsDir))
		if err != nil {
			// An archive mount lands here too; enumerating one would mean opening it on this goroutine, deliberately
			// out of scope.
			skipped++
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			key := strings.ToLower(name)
			if _, dup := seen[key]; dup {
				continue // an earlier mount already offered this folder; first hit wins
			}
			if len(seen) >= iniBaseLocalFolderCap {
				out.truncated = true
				break
			}
			seen[key] = struct{}{}
			out.names = append(out.names, name)
		}
		if out.truncated {
			break
		}
	}
	sort.SliceStable(out.names, func(i, j int) bool {
		return strings.ToLower(out.names[i]) < strings.ToLower(out.names[j])
	})
	switch {
	case skipped > 0 && len(out.names) == 0:
		out.err = "No characters/ folder in your local mounts (zip packs aren't listed here)."
	case out.truncated:
		out.err = "Showing the first folders found — the pack is larger than this browser lists."
	}
	return out
}

// baseMounts is empty unless a local mode is on: both accessors return the stored paths whatever the flag says, and
// listing mounts the asset pipeline isn't reading offers characters that fail to load. LayeredAssets normalizes the
// both-set state, so asking it second is enough. This copies; the mode-to-list mapping must agree with
// config.BaseMountsEqual.
func (a *App) baseMounts() []string {
	if on, mounts := a.d.Prefs.LocalAssets(); on {
		return mounts
	}
	if layered, mounts := a.d.Prefs.LayeredAssets(); layered {
		return mounts
	}
	return nil
}

// ensureLocalCharFolders starts a scan when the mount list changed. It uses config.BaseMountsEqual on the unchanged
// path because baseMounts copies under the lock, which would allocate every frame the tab is open.
func (a *App) ensureLocalCharFolders() {
	b := &a.iniBase
	if b.scanning {
		return
	}
	if b.scanned != nil && a.d.Prefs.BaseMountsEqual(b.scanned) {
		return
	}
	mounts := a.baseMounts()
	if len(mounts) == 0 { // nothing mounted: settle on an empty result, no goroutine
		b.scanned, b.local, b.truncated, b.scanErr = []string{}, nil, false, ""
		b.namesLocal = -1 // force the merged list to rebuild
		return
	}
	if b.res == nil {
		b.res = make(chan iniBaseScan, 1) // buffered 1: the scan never blocks on us
	}
	b.scanning = true
	ch := b.res
	go func() {
		defer func() {
			if r := recover(); r != nil {
				writeCrashLog("local character-folder scan panic: ", r)
				ch <- iniBaseScan{mounts: mounts, err: "Could not read your local mounts."}
			}
		}()
		ch <- scanLocalCharFolders(mounts)
	}()
}

func (a *App) pollLocalCharFolders() {
	b := &a.iniBase
	if b.res == nil {
		return
	}
	select {
	case res := <-b.res:
		b.scanning = false
		b.scanned = res.mounts
		b.local = res.names
		b.truncated = res.truncated
		b.scanErr = res.err
		b.namesLocal = -1 // invalidate the merged list
	default:
	}
}

// baseNames is the merged, sorted, de-duplicated folder list: the roster first (authoritative for what the server
// will accept), then any local folder it did not name.
func (a *App) baseNames() ([]string, []string) {
	b := &a.iniBase
	chars := 0
	if a.sess != nil {
		chars = len(a.sess.Chars)
	}
	if b.names != nil && b.namesKey == a.serverKey && b.namesChars == chars && b.namesLocal == len(b.local) {
		return b.names, b.lower
	}
	names := make([]string, 0, chars+len(b.local))
	lower := make([]string, 0, chars+len(b.local))
	seen := make(map[string]struct{}, chars+len(b.local))
	b.truncatedN = false
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n == "" {
			return
		}
		key := strings.ToLower(n)
		if _, dup := seen[key]; dup {
			return
		}
		if len(names) >= iniBaseNameCap {
			b.truncatedN = true
			return
		}
		seen[key] = struct{}{}
		names = append(names, n)
		lower = append(lower, key)
	}
	if a.sess != nil {
		for i := range a.sess.Chars {
			add(a.sess.Chars[i].Name)
		}
	}
	for _, n := range b.local {
		add(n)
	}
	b.names, b.lower = names, lower
	b.namesKey, b.namesChars, b.namesLocal = a.serverKey, chars, len(b.local)
	b.pages, b.pagesGen, b.ask = nil, 0, nil // the list changed: drop the index-keyed icon cache
	return b.names, b.lower
}

func (a *App) baseSourceCounts() (serverList, base int) {
	n := 0
	for _, in := range a.iniServerMem {
		if in {
			n++
		}
	}
	names, _ := a.baseNames()
	return n, len(names)
}

// drawIniswapBaseBody draws a search-filtered grid of char-icon cells, click to wear. Its own index-keyed page cache:
// a second list under a.iniPages' indices would hit the cachedPage reorder trap. Only visible cells ask for art.
func (a *App) drawIniswapBaseBody(panel sdl.Rect, y int32, query string) {
	c := a.ctx
	names, lower := a.baseNames()
	visible := func(i int) bool { return query == "" || strings.Contains(lower[i], query) }

	if len(names) == 0 {
		msg := "Nothing to list yet — this server sent no character roster and you have no local mounts."
		if a.iniBase.scanning {
			msg = "Reading your local mounts…"
		}
		c.LabelClipped(panel.X+pad, y+4, panel.W-2*pad, msg, ColTextDim)
		return
	}
	// A remote base cannot be listed; the search box doubles as the wear-by-name field.
	c.LabelClipped(panel.X+pad, y, panel.W-2*pad,
		"A plain web asset base can't be listed — type any folder name above and press Enter to wear it. "+
			"The names below come from this server's character roster and from your local mounts.", ColTextDim)
	y += 20
	if a.iniBase.scanErr != "" {
		c.LabelClipped(panel.X+pad, y, panel.W-2*pad, a.iniBase.scanErr, ColTextDim)
		y += 18
	} else if a.iniBase.truncatedN {
		c.LabelClipped(panel.X+pad, y, panel.W-2*pad,
			"Showing the first names found — this base has more than the browser lists.", ColTextDim)
		y += 18
	}

	gridTop := y
	gridW := panel.W - 2*pad - scrollBarW - scrollBarGap
	cols := gridW / (iconCell + iconGap)
	if cols < 1 {
		cols = 1
	}
	slots := int32(0)
	for i := range names {
		if visible(i) {
			slots++
		}
	}
	rowPitch := iconCell + iconGap + charCellCaptionH
	contentH := (slots + cols - 1) / cols * rowPitch
	visibleH := panel.Y + panel.H - gridTop - pad
	if visibleH <= 0 {
		return
	}
	a.iniBase.scroll -= c.WheelIn(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH}) * scrollStepPx
	track := sdl.Rect{X: panel.X + panel.W - pad - scrollBarW, Y: gridTop, W: scrollBarW, H: visibleH}
	a.iniBase.scroll = c.VScrollbar("inibasescroll", track, a.iniBase.scroll, contentH, visibleH)

	clipPrev, clipHad := c.pushClip(sdl.Rect{X: panel.X, Y: gridTop, W: panel.W, H: visibleH})
	slot := int32(0)
	for i := range names {
		if !visible(i) {
			continue
		}
		x := panel.X + pad + (slot%cols)*(iconCell+iconGap)
		yy := gridTop + (slot/cols)*rowPitch - a.iniBase.scroll
		slot++
		if yy <= gridTop-iconCell || yy >= panel.Y+panel.H-charCellCaptionH {
			continue
		}
		cell := sdl.Rect{X: x, Y: yy, W: iconCell, H: iconCell}
		c.Tooltip(cell, names[i])
		c.Fill(cell, ColBackground)
		base := a.urls.CharIcon(names[i])
		if page, ok := a.cachedPage(&a.iniBase.pages, &a.iniBase.pagesGen, len(names), i, base); ok && len(page.Frames) > 0 {
			c.cgoRect = cell
			_ = c.Ren.Copy(page.Frames[0], nil, &c.cgoRect)
		} else {
			a.demandAsset(&a.iniBase.ask, len(names), i, base, assets.AssetTypeCharIcon) // AssetType: CharIcon (base browser)
			initial := names[i]
			if len(initial) > 2 {
				initial = initial[:2]
			}
			c.Label(cell.X+cell.W/2-8, cell.Y+cell.H/2-8, initial, ColTextDim)
		}
		c.LabelClipped(cell.X, cell.Y+cell.H+1, cell.W, names[i], ColTextDim)
		if c.hovering(cell) && c.clicked {
			a.wearFromMenu(names[i]) // closes the modal and wears it (or claims a slot at char select)
		}
	}
	c.popClip(clipPrev, clipHad)
	if slots == 0 {
		c.Label(panel.X+pad, gridTop+6, "Nothing on this base matches your search.", ColTextDim)
	}
}
