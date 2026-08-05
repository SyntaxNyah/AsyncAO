package ui

// "Swap from your base" — the Iniswaps tab's second source.
//
// THE GAP THIS CLOSES. The wardrobe's browse tab only ever listed the server's
// `iniswap.txt`, a file most servers do not publish; on those servers the tab was a
// paragraph explaining that you could type a folder name if you happened to know
// one. But the client already KNOWS two complete lists of folders that exist on the
// base:
//
//   - the server's character ROSTER (the SC packet — every character folder the
//     server offers, taken or not), and
//   - the character folders inside the user's own LOCAL MOUNTS, which is the whole
//     point of having mounted a content pack.
//
// Both are now browsable and wearable. What CANNOT be listed is a plain remote HTTP
// base: there is no directory listing over HTTP, and probing for names is exactly
// the fallback-storm this client is built to avoid — so the type-a-name field stays,
// and now carries a visible line saying why.
//
// WHY A SEPARATE BROWSER, not more entries in a.iniList. iniList is the wardrobe's
// backing store: it is index-keyed (a.iniPages / a.iniAsk are parallel slices, and
// cachedPage does not re-check the URL behind an index), and char select's Wardrobe
// tab ranges it WHOLE. Merging a 4000-character roster into it would flood that tab
// with the entire server and invalidate every cached icon page on each rebuild. This
// browser keeps its own list, draws name buttons instead of index-keyed art cells,
// and touches nothing the wardrobe owns.
//
// THREADING. The local scan is os.ReadDir on the user's disk, so it runs on a
// goroutine and lands through a buffered channel that the draw drains — hard rule 2
// (no synchronous disk I/O on a draw path). It is bounded twice: at most
// config.LocalMountCap mounts are walked, and at most iniBaseLocalFolderCap names
// come back (hard rule 4).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	// iniBaseCharsDir is the sub-folder of a mount that holds character folders.
	// AO2-Client mount paths point at a BASE (the folder holding characters/,
	// background/, sounds/ …), which is the same layout the streaming URL builder
	// assumes, so this is the one place a mount is turned into a character list.
	iniBaseCharsDir = "characters"
	// iniBaseLocalFolderCap bounds ONE scan's result. A content pack with more
	// character folders than this is already past anything a person browses; the
	// browser says so rather than growing without limit (hard rule 4).
	iniBaseLocalFolderCap = 4096
	// iniBaseNameCap bounds the MERGED list (roster + local folders). Sized above
	// iniBaseLocalFolderCap so a full local scan can still be joined by a large
	// server roster; hub servers in the wild run to a few thousand characters.
	iniBaseNameCap = 8192
	// iniBaseCellW / iniBaseCellH / iniBaseCellGap size one name button. Wide enough
	// for a long folder name at the chrome face, and a plain 22 px kit row tall.
	iniBaseCellW   = int32(186)
	iniBaseCellH   = int32(22)
	iniBaseCellGap = int32(6)
	// iniBaseSourceChipW is one source chip in the tab's own two-way switch.
	iniBaseSourceChipW = int32(150)
)

// iniBaseScan is one completed local-mount scan, delivered from the goroutine.
type iniBaseScan struct {
	mounts    []string // the mount list this scan was made from (the staleness key)
	names     []string // character folder names, de-duplicated, sorted
	truncated bool     // the walk stopped at iniBaseLocalFolderCap
	err       string   // "" = fine; a human line for the status row
}

// iniBaseBrowser is the tab's state. One value on App (app.go); everything in it is
// touched from the render thread only, except `res`, which is the goroutine's seam.
type iniBaseBrowser struct {
	show   bool // the "From your base" source is the selected one
	scroll int32
	// autoDone / autoKey latch the ONE automatic source choice: a server that
	// publishes no iniswap.txt opens straight on the base browser, because the
	// alternative is the empty state this whole file exists to replace. A LATCH and
	// not a live test, so a deliberate switch back to "Server list" is not undone on
	// the next frame — and keyed by server, so joining a different one decides again.
	autoDone bool
	autoKey  string

	res      chan iniBaseScan
	scanning bool
	// scanned is the mount list `local` came from — the staleness key. Comparing the
	// list itself (rather than a hash or a joined string) keeps the per-frame check
	// allocation-free.
	scanned   []string
	local     []string
	truncated bool
	scanErr   string

	// The merged (roster + local) list, memoized on the three things that can change
	// it. Rebuilding a few thousand names every frame would be absurd on a draw path.
	names      []string
	lower      []string
	namesKey   string // a.serverKey the list was built for
	namesChars int    // len(a.sess.Chars) it was built from
	namesLocal int    // len(local) it was built from
	truncatedN bool   // the merge itself hit iniBaseNameCap

	// The count-bearing strings this tab draws EVERY frame it is open, memoized on
	// the counts they were built from — the same doctrine as themeFoundText
	// (settings.go): a per-frame fmt.Sprintf on a draw path is exactly the
	// allocation the zero-allocation render loop exists to keep out, and these
	// numbers change only when a fetch or a mount scan lands.
	labelServer  string // "Server list (N)"
	labelBase    string // "From your base (N)"
	labelStatus  string // "<N> on this server · ★ adds one …"
	labelServerN int    // the serverN they were built from
	labelBaseN   int    // ...and the baseN
}

// sourceLabels returns the two source-chip labels and the server-count status line
// for these counts, rebuilding the strings only when a count actually moved.
// labelServer is never legitimately empty, so it doubles as the "unset" marker (the
// zero counts are a real pair — an empty server list beside no mounts).
func (b *iniBaseBrowser) sourceLabels(serverN, baseN int) (server, base, status string) {
	if b.labelServer == "" || b.labelServerN != serverN || b.labelBaseN != baseN {
		b.labelServerN, b.labelBaseN = serverN, baseN
		b.labelServer = fmt.Sprintf("Server list (%d)", serverN)
		b.labelBase = fmt.Sprintf("From your base (%d)", baseN)
		b.labelStatus = fmt.Sprintf("%d on this server · ★ adds one to your Characters wardrobe", serverN)
	}
	return b.labelServer, b.labelBase, b.labelStatus
}

// scanLocalCharFolders walks each mount's characters/ folder and returns the folder
// names. BLOCKING — goroutine only.
//
// A mount that cannot be opened is skipped, not fatal: mounts go missing (an
// unplugged drive, a deleted folder, an archive rather than a folder) and whatever
// else resolved is still worth showing — the same degradation BuildMountIndex makes.
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
			// An ARCHIVE mount lands here too (a .zip is not a directory). Archives are
			// indexed for ASSET reads by internal/assets; enumerating one for names
			// would mean opening it on this goroutine and is deliberately out of scope.
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

// baseMounts is the mount list the browser scans — and it is empty unless a local
// mode is actually ON. Both accessors hand back the stored paths whatever their
// mode flag says, so the flag is what must be tested: offering folders from mounts
// the asset pipeline is not reading would list characters that then fail to load.
// LayeredAssets normalizes the reachable both-set state, so asking it second is
// enough (config.LayeredAssets).
//
// This COPIES (both accessors do), so it is for callers that keep the list. The
// staleness check does not: it asks config.BaseMountsEqual, which answers the same
// question against the stored slice under the read lock. The two must agree on
// which mode yields which list — TestBaseMountsEqualMatchesBaseMounts walks the
// mode matrix to keep them from drifting apart.
func (a *App) baseMounts() []string {
	if on, mounts := a.d.Prefs.LocalAssets(); on {
		return mounts
	}
	if layered, mounts := a.d.Prefs.LayeredAssets(); layered {
		return mounts
	}
	return nil
}

// ensureLocalCharFolders starts a scan when the mount list has changed since the
// last one. Cheap and allocation-free when nothing changed (a slice compare), which
// is what lets a draw path call it every frame.
//
// That "allocation-free" is why the unchanged path asks config.BaseMountsEqual
// instead of comparing against baseMounts(): baseMounts routes through
// LocalAssets/LayeredAssets, and BOTH of those take a make+copy of the stored
// paths under the lock. Fetching the list only to throw it away would have cost a
// user with mounts — the exact user this feature exists for — one or two heap
// allocations EVERY frame the Iniswaps tab was open. The copy is now taken only
// on the changed path, which runs once per mount edit and hands the slice to a
// goroutine that must own it anyway.
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

// pollLocalCharFolders lands a finished scan. Non-blocking, like pollIniswap.
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

// baseNames is the merged, sorted, de-duplicated list of every character folder the
// client knows exists on this base: the server's roster first (it is authoritative
// for what the SERVER will accept), then any local folder the roster did not name.
// Memoized — see iniBaseBrowser.names.
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
	return b.names, b.lower
}

// baseSourceCounts is what the two source chips advertise.
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

// drawIniswapBaseBody draws the "From your base" source: a search-filtered grid of
// name buttons, one per character folder, click to wear.
//
// Deliberately NOT the wardrobe's art cells. Those are index-keyed into a.iniPages,
// and a second list under the same indices is the cachedPage reorder trap the
// wardrobe's own comments describe; a name button also costs no asset demand at all,
// which is what keeps browsing a four-thousand-character roster free.
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
	// The one thing a remote base can never offer, said out loud where the field that
	// works around it is (the search box doubles as the wear-by-name field).
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
	cols := gridW / (iniBaseCellW + iniBaseCellGap)
	if cols < 1 {
		cols = 1
	}
	slots := int32(0)
	for i := range names {
		if visible(i) {
			slots++
		}
	}
	rowPitch := iniBaseCellH + iniBaseCellGap
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
		x := panel.X + pad + (slot%cols)*(iniBaseCellW+iniBaseCellGap)
		yy := gridTop + (slot/cols)*rowPitch - a.iniBase.scroll
		slot++
		if yy <= gridTop-iniBaseCellH || yy >= panel.Y+panel.H {
			continue
		}
		if c.Button(sdl.Rect{X: x, Y: yy, W: iniBaseCellW, H: iniBaseCellH}, names[i]) {
			a.wearFromMenu(names[i]) // closes the modal and wears it (or claims a slot at char select)
		}
	}
	c.popClip(clipPrev, clipHad)
	if slots == 0 {
		c.Label(panel.X+pad, gridTop+6, "Nothing on this base matches your search.", ColTextDim)
	}
}
