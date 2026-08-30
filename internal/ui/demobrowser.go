package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// In-app .demo file browser (v1.72.2). The native OpenFileDialog shell-out
// (pickDemoForVideo, removed) failed the user LIVE twice — a GUI-subsystem
// CREATE_NO_WINDOW child can't reliably win foreground rights, so the dialog
// opened behind the app or not at all (the "button does nothing" reports). An
// in-app browser kills that whole failure class and is cross-platform (the
// native path was Windows-only). It lists directories + .demo/.aorec files,
// descends on click, and PICKS a recording straight into the shared Studio
// import tail (importDroppedRecording → importRecordingToVideo) — the exact
// same logic the drag-and-drop path uses, so there is one owner of the import.
//
// Perf/hard-rule discipline (spec §17): the entry list is read by ONE bounded
// loader goroutine per navigation (never per-frame ReadDir on the render thread
// — a network drive would freeze the UI), cached in the state, and every
// per-navigation string is built once. Quick-jump paths resolve once at open.

const (
	// browseResCap bounds the loader→render result channel: exactly one result
	// in flight per navigation (single-flight is guarded by browse.loading), so a
	// cap of 1 can never block the loader goroutine or drop a result. Named per
	// hard rule §17.4 (no unbounded channels).
	browseResCap = 1
	// maxBrowseEntries caps how many rows one directory contributes (§17.4: no
	// unbounded caches/lists). A huge asset folder (miku.pizza ships 4000+ char
	// dirs) must not build an unbounded slice or blow the label cache; past the
	// cap a single "… and N more" tail row shows the overflow. 2000 is far above
	// any real recordings\ folder yet cheap to hold and sort.
	maxBrowseEntries = 2000
	// browseRowH is the pitch of one entry/quick-jump row (matches the maker
	// picker's 26px rows so the two in-app lists feel identical).
	browseRowH = 26
	// browseIconColW is the fixed x-offset (from the row's text start) where the
	// entry NAME begins, leaving a constant column for the 📁/🎞/⬆ icon label so
	// the icon and name draw as two separate cached labels (no per-frame concat).
	browseIconColW = 30
)

// browsePurpose is what a picked recording is used FOR — the same in-app browser
// serves three Studio flows now (v1.74). The video flow (the original, and the
// drag-onto-Studio default) stays byte-identical: purposeVideo is the zero value,
// so an un-set browser behaves exactly as before. check/package route to the
// content-report engine (contentjob.go) instead of the video exporter.
type browsePurpose int

const (
	purposeVideo   browsePurpose = iota // pick → importRecordingToVideo (the original flow)
	purposeCheck                        // pick → StartContentReport + open the report panel
	purposePackage                      // pick → StartContentReport, then package once it's ready
	// purposeThemeBundle (v1.90.0 W8): pick → handleThemeBundleDrop, the SAME
	// funnel a dragged .aotheme goes through. It lists bundles instead of
	// recordings, which is the first time this browser's file filter has had to
	// depend on what it was opened for — see browseKeepRule.
	purposeThemeBundle
	// purposeThemeImage (v1.90.0 W10): pick → editorTakeImage, the SAME funnel a
	// dragged .png / .webp / .gif enters by. The browse half of the theme editor's
	// image intake, and it exists for the reason purposeThemeBundle does: a drop is
	// undiscoverable, and a picture already saved into Downloads has no window to be
	// dragged onto without going and finding it again.
	purposeThemeImage
	// purposeBaseFolder: pick → the base-folder wizard's setBasePath (issue #72).
	// The FIRST purpose whose answer is a DIRECTORY rather than a file, which is
	// why browsePicksDir exists — the rows are still files and folders, but the
	// pick is "the folder I am standing in".
	//
	// It is in-app for the reason the .demo picker moved in-app: the native folder
	// dialog is a PowerShell shell-out that exists only on Windows and has failed
	// to win foreground live before. browseForFolder's own comment names extending
	// this browser to directories as the escalation path; this is it.
	purposeBaseFolder
)

// browsePicksDir reports a purpose whose answer is the CURRENT DIRECTORY rather
// than a row in it. Such a browser grows a "Use this folder" button beside the
// path line; everything else about it is unchanged, so there is still one file
// navigation surface rather than two.
func browsePicksDir(p browsePurpose) bool { return p == purposeBaseFolder }

// browseEntry is one row in the file browser: a directory to descend into or a
// recording (.demo/.aorec) to pick. Built once per navigation by the loader.
type browseEntry struct {
	name  string
	isDir bool
}

// demoBrowserState lives package-level like `settings` (one browser, the
// Settings screen is single-instance). Session-remembered dir means reopening
// the browser lands where you left off — deliberately NOT a saved pref: prefs
// need prefsJSON + load-overlay wiring (memory: "new prefs MUST join prefsJSON
// + the load overlay or they save-but-don't-load"), and a browse cursor doesn't
// warrant that surface. First open seeds dir from UserHomeDir (openDemoBrowser).
type demoBrowserState struct {
	open bool
	// purpose is what the picked file feeds (video export / content check /
	// package / theme import). Set once at open (openDemoBrowserFor); the pick
	// action branches on it. Defaults to purposeVideo so the historic Import-.demo
	// button is unchanged.
	purpose browsePurpose
	// keep is the purpose's FILE FILTER, resolved once at open and captured by the
	// loader goroutine. A field rather than a per-load read of `purpose`: the
	// loader runs off the render thread, and reading a field the render thread can
	// write is a race the detector would find the first time somebody re-opened the
	// browser while a slow network directory was still listing.
	keep func(string) bool
	// dir is the directory being listed. "" is the Windows DRIVES view (a row per
	// existing volume); off Windows there is no drives view and ".." stops at "/".
	dir string
	// entries is the loaded+cached listing for dir (NO per-frame ReadDir). more is
	// the overflow count when the directory exceeded maxBrowseEntries (0 = none).
	entries []browseEntry
	more    int
	// loading guards single-flight: a navigation only kicks a loader when no load
	// is already in flight, so rapid clicks can't spawn unbounded goroutines (§17).
	loading bool
	loadErr string
	scroll  int32
	// res carries the loader goroutine's result back to the render thread; drained
	// in drawDemoBrowser (never blocks or stats on the render thread).
	res chan browseResult
	// Quick-jump targets, resolved ONCE at open (os.UserHomeDir + Join, recordingsDir)
	// so the per-frame draw never touches the filesystem or allocates these.
	homeDir, downloadsDir, desktopDir, recDir string
}

// browseResult is one loader outcome: the listing for dir (or an error), tagged
// with the dir it was loading so a stale result from a superseded navigation is
// ignored (the user can click fast while a slow network dir loads).
type browseResult struct {
	dir     string
	entries []browseEntry
	more    int
	err     string
}

var demoBrowser = demoBrowserState{res: make(chan browseResult, browseResCap)}

// isDrivesView reports the sentinel dir ("" = the Windows drives list). Off
// Windows this is never entered (openDemoBrowser/parentBrowseDir never yield "").
func (s *demoBrowserState) isDrivesView() bool { return s.dir == "" }

// openDemoBrowser opens the browser for the ORIGINAL .demo → video flow (the
// Studio call-out's Import button on every OS). Thin wrapper over the
// purpose-taking variant so existing callers stay one-liners and the video flow
// is unmistakably the default.
func (a *App) openDemoBrowser() { a.openDemoBrowserFor(purposeVideo) }

// openDemoBrowserFor opens the browser bound to a pick PURPOSE (video / content
// check / package), resolving the quick-jump paths once, and navigates to the
// session-remembered dir (first open → the user's home). The purpose steers what
// picking a recording does (pickBrowsedRecording); the rest of the browser is
// identical across purposes so there's one file-navigation surface, not three.
func (a *App) openDemoBrowserFor(purpose browsePurpose) {
	s := &demoBrowser
	s.purpose = purpose
	s.keep = browseKeepRule(purpose)
	home, _ := os.UserHomeDir() // "" is tolerated: the quick-jump button just no-ops
	s.homeDir = home
	if home != "" {
		s.downloadsDir = filepath.Join(home, "Downloads")
		s.desktopDir = filepath.Join(home, "Desktop")
	} else {
		s.downloadsDir, s.desktopDir = "", ""
	}
	s.recDir = recordingsDir()
	start := s.dir
	if start == "" && !s.open {
		// Very first open this session: seed from home (drives view stays reachable
		// via the 💾 button on Windows). A remembered dir survives re-opens.
		start = home
	}
	s.open = true
	a.navBrowseTo(start)
}

// navBrowseTo points the browser at dir and kicks the single loader goroutine
// for it. Single-flight: a navigation while a load is in flight is dropped (the
// in-flight result, once drained, will not match and gets ignored, and the user
// can click again). Resets scroll and clears the previous error immediately so
// the UI reads responsive.
func (a *App) navBrowseTo(dir string) {
	s := &demoBrowser
	if s.loading {
		return // one bounded loader at a time (§17.4)
	}
	s.dir = dir
	s.scroll = 0
	s.loadErr = ""
	s.loading = true
	// CAPTURED, not read from the state inside the goroutine: the filter belongs to
	// the open browser and the loader must not touch a field the render thread owns.
	keep := s.keep
	if keep == nil {
		keep = isRecordingName // a browser opened before W8's purpose field existed
	}
	go func(target string, keep func(string) bool) {
		ents, more, err := loadBrowseDir(target, keep)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		// Cap 1 + single-flight: this send can never block (the render thread drains
		// exactly one result before clearing s.loading and allowing the next nav).
		s.res <- browseResult{dir: target, entries: ents, more: more, err: errStr}
	}(dir, keep)
}

// loadBrowseDir reads one directory OFF the render thread: the Windows drives
// view when dir=="", else os.ReadDir filtered to sub-directories + recordings.
// Returns the capped, sorted entries and the overflow count. Any I/O error
// (an unreadable/rights-denied dir) comes back so the draw can show it while
// leaving ".." and the quick-jumps navigable.
func loadBrowseDir(dir string, keep func(string) bool) (entries []browseEntry, more int, err error) {
	if dir == "" { // Windows drives view (never reached off Windows)
		return listDrives(), 0, nil
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	return filterBrowseEntries(des, keep), overflowCount(des, keep), nil
}

// browseKeepRule is the FILE FILTER for a pick purpose — the one place a purpose
// decides what a listing shows.
//
// A FUNCTION OF THE PURPOSE rather than a branch inside the filter, because the
// filter is pure and generic over its entries (that is what makes it unit-testable
// without a filesystem) and the purpose is state. The default is deliberately the
// recording rule: a purpose added without a rule lists recordings, which is wrong
// but harmless, rather than listing everything.
func browseKeepRule(p browsePurpose) func(string) bool {
	switch p {
	case purposeThemeBundle:
		return isThemeBundleName
	case purposeThemeImage:
		return isThemeImageName
	case purposeBaseFolder:
		return isBaseMountName
	}
	return isRecordingName
}

// isBaseMountName reports a file that can be mounted as a base in its own right,
// which is .zip and nothing else. It exists so the base-folder browser is not
// filtered down to directories only: a pack is a legitimate answer to "where are
// your AO files", and hiding every file would make a folder full of them look
// empty to somebody checking they had opened the right one.
func isBaseMountName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".zip")
}

// isThemeBundleName reports a theme bundle by its lowercased extension.
//
// PLAIN .zip IS LISTED, and that is not the same decision dropclaim.go made. A
// DROP must not be claimed by extension alone — a .zip is far more likely to be
// meant for something else, and stealing it would be a drop that vanished — but a
// BROWSE is the user pointing at one file and saying "this one". The importer
// never trusts either extension anyway: themepack.Inspect sniffs the archive's own
// magic bytes (the assets.Sniff doctrine), so a .aotheme that is really a
// screenshot is refused by name whichever way it arrived.
func isThemeBundleName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == themePackExt || ext == ".zip"
}

// browseDirEntry is the tiny surface filterBrowseEntries needs from an entry, so
// the filter/sort is pure and unit-testable without a real os.DirEntry.
type browseDirEntry interface {
	Name() string
	IsDir() bool
}

// filterBrowseEntries keeps sub-directories (skipping hidden dotfiles) and files
// the caller's keep rule accepts (recordings, or theme bundles — browseKeepRule),
// sorts directories first then case-insensitively by name, and caps the result at
// maxBrowseEntries. Pure over its input (no filesystem) so it unit-tests directly.
func filterBrowseEntries[E browseDirEntry](des []E, keep func(string) bool) []browseEntry {
	out := make([]browseEntry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue // hidden dotfiles (and "." / "..") never listed
		}
		if de.IsDir() {
			out = append(out, browseEntry{name: name, isDir: true})
			continue
		}
		if keep(name) {
			out = append(out, browseEntry{name: name, isDir: false})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].isDir != out[j].isDir {
			return out[i].isDir // directories first
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	if len(out) > maxBrowseEntries {
		out = out[:maxBrowseEntries]
	}
	return out
}

// overflowCount is how many rows past maxBrowseEntries a directory holds (the
// "… and N more" tail). It mirrors filterBrowseEntries' keep rule so the count
// matches the truncation exactly.
func overflowCount[E browseDirEntry](des []E, keep func(string) bool) int {
	kept := 0
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if de.IsDir() || keep(name) {
			kept++
		}
	}
	if kept > maxBrowseEntries {
		return kept - maxBrowseEntries
	}
	return 0
}

// isRecordingName reports a .demo/.aorec file by its lowercased extension (the
// same two extensions HandleFileDrop and the recordings picker accept).
func isRecordingName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == demoExt || ext == recordingExt
}

// listDrives probes A:\ … Z:\ (os.Stat off the render thread) and returns one
// directory row per existing Windows volume. Empty off Windows (the drives view
// is never entered there).
func listDrives() []browseEntry {
	if runtime.GOOS != "windows" {
		return nil
	}
	var out []browseEntry
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + ":\\"
		if _, err := os.Stat(root); err == nil {
			out = append(out, browseEntry{name: root, isDir: true})
		}
	}
	return out
}

// parentBrowseDir walks one level up from dir. On Windows a drive root (C:\)
// walks to the drives sentinel (""); elsewhere ".." stops at "/". filepath.Dir
// is idempotent at a root, so we detect "already at the root" and branch.
func parentBrowseDir(dir string) string {
	if dir == "" {
		return "" // already the drives view: no parent
	}
	parent := filepath.Dir(dir)
	if parent == dir { // filepath.Dir fixed-points at a root
		if runtime.GOOS == "windows" {
			return "" // C:\ → drives view
		}
		return "/" // POSIX root has no parent; stay at "/"
	}
	return parent
}

// childBrowseDir joins a directory entry's name onto dir. In the drives view the
// entry name is already a full root ("C:\"), so it IS the target.
func childBrowseDir(dir, name string) string {
	if dir == "" {
		return name // drives view: the row name is the volume root itself
	}
	return filepath.Join(dir, name)
}

// browseTitle names the modal for its current purpose. Each string is a package
// const built once (no per-frame alloc — the switch returns a constant); the
// video wording is unchanged from the original single-purpose browser.
func browseTitle(p browsePurpose) string {
	switch p {
	case purposeCheck:
		return "Pick a .demo/.aorec to check for missing content"
	case purposePackage:
		return "Pick a .demo/.aorec to package into a self-contained folder"
	case purposeThemeBundle:
		return "Pick a theme bundle to import"
	case purposeThemeImage:
		return "Pick an image to add to this theme (PNG, WebP, GIF, APNG, AVIF — animated is fine)"
	case purposeBaseFolder:
		return "Open your base folder, then press Use this folder"
	default:
		return "Pick a .demo to turn into a video"
	}
}

// browseFileIcon is the glyph for a non-directory row. A constant per purpose, so
// the flat draw loop still costs no allocation.
func browseFileIcon(p browsePurpose) string {
	switch p {
	case purposeThemeBundle, purposeBaseFolder:
		return "📦"
	case purposeThemeImage:
		return "🖼"
	}
	return "🎞"
}

// drawDemoBrowser draws the modal file browser LAST in drawSettings (topmost),
// with the page's modal fence already released by the caller so its
// c.Button/c.VScrollbar/c.hovering widgets work normally (the app.go:6926
// release-and-restore idiom). No-op when closed. Every navigation is a state
// mutation that kicks the loader; the draw itself never touches the filesystem.
func (a *App) drawDemoBrowser(w, h int32) {
	s := &demoBrowser
	if !s.open {
		return
	}
	c := a.ctx

	// Drain a pending loader result (render thread; never blocks). A result whose
	// dir no longer matches s.dir is from a superseded navigation — clear loading
	// so the next nav can proceed, but keep the current (newer) listing.
	select {
	case r := <-s.res:
		s.loading = false
		if r.dir == s.dir {
			s.entries, s.more, s.loadErr = r.entries, r.more, r.err
		}
	default:
	}

	// Centered modal panel. Sized to a comfortable fraction of the window, clamped
	// so it never overflows a tiny window.
	const (
		bwMaxW = 640
		bwMaxH = 520
		bwMinW = 320
		bwMinH = 240
	)
	pw := clampI32(w-2*browseModalMargin, bwMinW, bwMaxW)
	ph := clampI32(h-2*browseModalMargin, bwMinH, bwMaxH)
	px := (w - pw) / 2
	py := (h - ph) / 2
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)

	inX := px + 12
	inW := pw - 24
	y := py + 10

	// Title + ✕ close (top-right).
	c.Label(inX, y, browseTitle(s.purpose), ColText)
	closeR := sdl.Rect{X: px + pw - 30, Y: py + 8, W: 22, H: 22}
	if c.Button(closeR, "✕") {
		s.open = false
		return
	}
	y += 26

	// Current path line (the drives view has no path). A directory-picking purpose
	// puts its commit button here, on the same row as the path it commits, so what
	// the button means is the line next to it. It is hidden in the drives view,
	// where there is no directory to pick.
	pathText := s.dir
	pathW := inW
	if s.isDrivesView() {
		pathText = "This PC (drives)"
	} else if browsePicksDir(s.purpose) {
		const useBtnW = 130
		pathW = inW - useBtnW - 8
		if c.Button(sdl.Rect{X: inX + inW - useBtnW, Y: y - 2, W: useBtnW, H: btnH}, "Use this folder") {
			a.pickBrowsedFile(s.dir)
		}
	}
	c.LabelClipped(inX, y, pathW, pathText, ColTextDim)
	y += 22

	// Quick-jump row: Home / Downloads / Desktop / recordings\ (+ Drives on
	// Windows). Each is a no-op when its path didn't resolve at open (empty).
	qx := inX
	quick := func(label, target string, enabled bool) {
		bw := c.TextWidth(label) + 16
		if bw < 40 {
			bw = 40
		}
		r := sdl.Rect{X: qx, Y: y, W: bw, H: btnH}
		if c.Button(r, label) && enabled {
			a.navBrowseTo(target)
		}
		qx += bw + 6
	}
	quick("🏠 Home", s.homeDir, s.homeDir != "")
	quick("⬇ Downloads", s.downloadsDir, s.downloadsDir != "")
	quick("🖥 Desktop", s.desktopDir, s.desktopDir != "")
	quick("📼 recordings\\", s.recDir, s.recDir != "")
	if runtime.GOOS == "windows" {
		quick("💾 Drives", "", true) // dir="" is the drives view
	}
	y += btnH + 8

	// Status line: loading spinner-text or an error (an unreadable dir), leaving
	// the list navigable below via ".." and the quick-jumps.
	if s.loading {
		c.Label(inX, y, "Loading…", ColTextDim)
		y += 18
	} else if s.loadErr != "" {
		c.LabelClipped(inX, y, inW, "Can't open this folder: "+s.loadErr, ColDanger)
		y += 18
	}

	// --- clipped, scrollable entry list --------------------------------------
	listTop := y
	listH := (py + ph - 10) - listTop
	if listH < browseRowH {
		listH = browseRowH
	}
	listRect := sdl.Rect{X: inX, Y: listTop, W: inW, H: listH}
	c.Fill(listRect, ColPanel)

	// Row model: a leading "⬆ .." row (except in the drives view, which is the
	// top of the tree on Windows), then the entries, then an optional "… and N
	// more" tail. rowCount sizes the scrollbar content height.
	showUp := !s.isDrivesView()
	rowCount := len(s.entries)
	if showUp {
		rowCount++
	}
	if s.more > 0 {
		rowCount++ // the "… and N more" tail
	}
	contentH := int32(rowCount) * browseRowH
	trackW := scrollBarW
	// Reserve the scrollbar gutter only when the list actually overflows.
	rowW := inW - 8
	if contentH > listH {
		rowW -= trackW + 4
	}

	// Wheel scrolls the list ONLY while hovered; mark it consumed (wheel
	// single-consumer memory) though nothing after this reads it.
	if c.hovering(listRect) && c.wheelY != 0 {
		c.wheelTaken = true
		s.scroll -= c.wheelY * browseRowH
	}
	maxScroll := contentH - listH
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}
	if s.scroll < 0 {
		s.scroll = 0
	}

	// Clip the rows to the list rect: raw Ren.SetClipRect clips DRAW only, so
	// clicks/hovers would leak past the edge (project memory) — pushClip mirrors
	// the clip into hovering()/ClickedIn(). Rows are drawn in a flat INDEX loop
	// (no per-frame closure — sibling drawMakerList does the same for alloc-free
	// draw): index 0..(showUp?1:0) is the ⬆ .. row, then the entries, then the
	// optional "… and N more" tail.
	clipPrev, clipHad := c.pushClip(listRect)
	upRows := int32(0)
	if showUp {
		upRows = 1
	}
	for i := int32(0); i < int32(rowCount); i++ {
		ry := listTop - s.scroll + i*browseRowH
		if ry+browseRowH < listTop || ry > listTop+listH { // cull rows outside the window
			continue
		}
		row := sdl.Rect{X: inX + 4, Y: ry, W: rowW, H: browseRowH - 2}

		// Tail row ("… and N more"): a dim, non-clickable label at the very end.
		if s.more > 0 && i == int32(rowCount)-1 {
			c.LabelClipped(row.X+8, row.Y+4, row.W-16, browseMoreLabel(s.more), ColTextDim)
			continue
		}

		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
		}

		// Resolve which model row this index is: the ⬆ parent row, or an entry.
		if showUp && i == 0 {
			if c.ClickedIn(row) {
				a.navBrowseTo(parentBrowseDir(s.dir))
			}
			c.Label(row.X+8, row.Y+4, "⬆", ColText)
			c.LabelClipped(row.X+browseIconColW, row.Y+4, row.W-browseIconColW-8, "..", ColText)
			continue
		}
		e := s.entries[i-upRows]
		if c.ClickedIn(row) {
			if e.isDir {
				a.navBrowseTo(childBrowseDir(s.dir, e.name))
			} else {
				a.pickBrowsedFile(childBrowseDir(s.dir, e.name))
			}
		}
		// Icon and name are SEPARATE labels (constant/cached icon + cached name) so
		// no per-frame `icon+" "+name` concat allocates — the icon column is a fixed
		// offset, matching drawMakerList's tag+text split.
		icon := browseFileIcon(s.purpose)
		if e.isDir {
			icon = "📁"
		}
		c.Label(row.X+8, row.Y+4, icon, ColText)
		c.LabelClipped(row.X+browseIconColW, row.Y+4, row.W-browseIconColW-8, e.name, ColText)
	}
	c.popClip(clipPrev, clipHad)

	// Scrollbar on the right edge of the list when it overflows.
	if contentH > listH {
		track := sdl.Rect{X: inX + inW - trackW, Y: listTop, W: trackW, H: listH}
		s.scroll = c.VScrollbar("demobrowse", track, s.scroll, contentH, listH)
	}
}

// browseModalMargin is the inset of the centered modal from the window edges.
const browseModalMargin = 40

// browseMoreLabel formats the overflow tail row. Only reached when a directory
// exceeded maxBrowseEntries (rare) and only one row draws it, so the single
// fmt.Sprintf mirrors drawMakerList's own "… and N more" tail (sibling budget).
func browseMoreLabel(more int) string {
	return fmt.Sprintf("… and %d more (open a subfolder to narrow it down)", more)
}

// pickBrowsedFile is the browser's PICK action, routed by the browser's purpose.
// Close the browser first (every branch does), then:
//   - purposeVideo: run the SHARED Studio import tail (importDroppedRecording →
//     importRecordingToVideo) — byte-identical to the drag-onto-Studio path.
//   - purposeCheck / purposePackage: hand off to the content-report engine
//     (openContentReportFor), which loads the recording, starts the probe, and
//     opens the report panel.
//   - purposeThemeBundle: hand off to handleThemeBundleDrop — the SAME funnel a
//     dragged bundle enters by, so browsing and dropping cannot diverge about
//     caps, consent or where a theme lands (v1.90.0 W8).
//
// EVERY BRANCH IS A HAND-OFF TO AN EXISTING OWNER; none of them does the work
// itself. That is the property that has kept this function honest through four
// purposes — it was renamed from pickBrowsedRecording when the fourth arrived,
// because "recording" had stopped being true.
//
// The video branch is deliberately untouched from the original single-purpose
// browser so that flow can't regress (pinned by a test).
func (a *App) pickBrowsedFile(path string) {
	purpose := demoBrowser.purpose
	demoBrowser.open = false
	switch purpose {
	case purposeCheck:
		a.openContentReportFor(path, false)
	case purposePackage:
		a.openContentReportFor(path, true)
	case purposeThemeBundle:
		a.handleThemeBundleDrop(path)
	case purposeThemeImage:
		// The SAME funnel a dragged picture enters by (thememediaintake.go). It
		// returns false only when no editor is open over a writable theme, which
		// cannot happen from here — this browser is opened from the editor's own rail
		// — so the refusal is a chip rather than a branch.
		a.editorTakeImage(path)
	case purposeBaseFolder:
		// The SAME funnel a dropped folder enters by. path is the current DIRECTORY
		// for the Use-this-folder button and a .zip pack for a row click; setBasePath
		// takes either, and the scan reports which it got.
		baseWizard.setBasePath(path)
	default:
		a.importRecordingToVideo(importDroppedRecording(path))
	}
}
