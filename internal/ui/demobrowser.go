package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// In-app file browser: a native dialog shell-out can't reliably win foreground rights, and this is cross-platform.
// One bounded loader goroutine per navigation lists entries (never ReadDir per frame;
// a network drive would freeze the UI).

const (
	// browseResCap is 1: single-flight on browse.loading means one result in flight, so the loader never blocks.
	browseResCap = 1
	// maxBrowseEntries caps the rows per directory (hard rule 4); past it a single "and N more" row shows the overflow.
	maxBrowseEntries = 2000
	// browseRowH is one row's pitch, matching the maker picker's rows.
	browseRowH = 26
	// browseIconColW is where the name starts,
	// so icon and name draw as separate cached labels with no per-frame concat.
	browseIconColW = 30
)

// browsePurpose is what a picked file is used for; purposeVideo is the zero value, so an unset browser imports video.
type browsePurpose int

const (
	purposeVideo   browsePurpose = iota // pick → importRecordingToVideo (the original flow)
	purposeCheck                        // pick → StartContentReport + open the report panel
	purposePackage                      // pick → StartContentReport, then package once it's ready
	// purposeThemeBundle: pick goes to handleThemeBundleDrop, as a dragged .aotheme does;
	// lists bundles (see browseKeepRule).
	purposeThemeBundle
	// purposeThemeImage: pick goes to editorTakeImage, as a dragged picture does.
	purposeThemeImage
	// purposeBaseFolder: pick goes to the base-folder wizard's setBasePath;
	// its answer is a directory (see browsePicksDir).
	purposeBaseFolder
	// purposeEvidenceImage: pick goes to the evidence editor's Image-file field; it navigates the merged local mounts,
	// with an earlier mount shadowing a later one, and cannot leave them (see mergebrowse.go).
	purposeEvidenceImage
)

// browsePicksDir reports a purpose whose answer is the current directory, adding a "Use this folder" button.
func browsePicksDir(p browsePurpose) bool { return p == purposeBaseFolder }

// browseEntry is one row: a directory to descend into or a file to pick.
type browseEntry struct {
	name  string
	isDir bool
}

// demoBrowserState is package-level like settings; the remembered dir is session-only, deliberately not a pref.
type demoBrowserState struct {
	open bool
	// purpose is what the picked file feeds; set once at open.
	purpose browsePurpose
	// keep is the purpose's file filter, resolved at open so the loader goroutine doesn't race a re-open reading
	// purpose.
	keep func(string) bool
	// dir is the directory listed; "" is the Windows drives view, and off Windows ".." stops at "/".
	dir string
	// merged marks the evidence browse: dir is then a mount-relative asset path,
	// and "" the merged root (see mergebrowse.go).
	merged bool
	// entries is the cached listing for dir; more is the overflow past maxBrowseEntries.
	entries []browseEntry
	more    int
	// loading guards single-flight, so rapid clicks can't spawn unbounded goroutines.
	loading bool
	loadErr string
	scroll  int32
	// res carries the loader's result to the render thread; drained in drawDemoBrowser.
	res chan browseResult
	// Quick-jump targets, resolved once at open so the draw never touches the filesystem.
	homeDir, downloadsDir, desktopDir, recDir string
}

// browseResult is one loader outcome, tagged with its dir so a superseded navigation's result is ignored.
type browseResult struct {
	dir     string
	entries []browseEntry
	more    int
	err     string
}

var demoBrowser = demoBrowserState{res: make(chan browseResult, browseResCap)}

// isDrivesView reports the "" sentinel; in the merged browse "" is the merged root, which offers no volume list.
func (s *demoBrowserState) isDrivesView() bool { return !s.merged && s.dir == "" }

// openDemoBrowser opens the browser for the .demo to video flow.
func (a *App) openDemoBrowser() { a.openDemoBrowserFor(purposeVideo) }

// openDemoBrowserFor opens the browser for a pick purpose and navigates to the session-remembered dir.
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
	// Switching path models drops the remembered dir, since neither is meaningful to the other; the filesystem browser
	// restarts at home, because "" would open the drives view.
	if merged := purpose == purposeEvidenceImage; s.merged != merged {
		s.merged = merged
		s.dir = home
	}
	start := s.dir
	if s.merged {
		// Both entry buttons open here every time, so a remembered merged dir can't make them differ.
		start = evidenceBrowseSeed
	} else if start == "" && !s.open {
		// First open this session: seed from home. A remembered dir survives re-opens.
		start = home
	}
	s.open = true
	a.navBrowseTo(start)
}

// navBrowseTo points the browser at dir and kicks one loader; a navigation during a load is dropped.
func (a *App) navBrowseTo(dir string) {
	s := &demoBrowser
	if s.loading {
		return // one bounded loader at a time (§17.4)
	}
	s.dir = dir
	s.scroll = 0
	s.loadErr = ""
	s.loading = true
	// Captured: the loader must not touch a field the render thread owns.
	keep := s.keep
	if keep == nil {
		keep = isRecordingName // a browser opened before W8's purpose field existed
	}
	if s.merged {
		// The merged listing reads the mount set here, on the render thread; the loader may not touch prefs.
		a.startMergedBrowseLoad(dir, keep)
		return
	}
	go func(target string, keep func(string) bool) {
		ents, more, err := loadBrowseDir(target, keep)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		// Cap 1 and single-flight: this send can never block.
		s.res <- browseResult{dir: target, entries: ents, more: more, err: errStr}
	}(dir, keep)
}

// loadBrowseDir reads one directory off the render thread;
// "" lists drives, and an I/O error returns for the draw to show.
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

// browseKeepRule is the file filter for a pick purpose;
// the default lists recordings, wrong but harmless for a new purpose.
func browseKeepRule(p browsePurpose) func(string) bool {
	switch p {
	case purposeThemeBundle:
		return isThemeBundleName
	case purposeThemeImage:
		return isThemeImageName
	case purposeBaseFolder:
		return isBaseMountName
	case purposeEvidenceImage:
		return isEvidenceImageName
	}
	return isRecordingName
}

// isBaseMountName reports a .zip, which mounts as a base; hiding files would make a folder of packs look empty.
func isBaseMountName(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".zip")
}

// isEvidenceImageName reports a file the theme image intake's decode list accepts.
func isEvidenceImageName(name string) bool {
	return isThemeImageExt(strings.ToLower(filepath.Ext(name)))
}

// isThemeBundleName lists plain .zip too: unlike a drop, a browse is the user pointing at one file,
// and themepack.Inspect sniffs the magic bytes regardless.
func isThemeBundleName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == themePackExt || ext == ".zip"
}

// browseDirEntry is what filterBrowseEntries needs from an entry, so it needs no real os.DirEntry.
type browseDirEntry interface {
	Name() string
	IsDir() bool
}

// filterBrowseEntries keeps non-hidden directories and files keep accepts, directories first,
// capped at maxBrowseEntries.
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

// overflowCount is how many rows past maxBrowseEntries a directory holds, using filterBrowseEntries' rule.
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

func isRecordingName(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == demoExt || ext == recordingExt
}

// listDrives probes A:\ to Z:\ off the render thread for one row per Windows volume; empty off Windows.
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

// parentBrowseDir walks one level up; a Windows drive root goes to "", and filepath.Dir fixed-points at a root.
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

// childBrowseDir joins a name onto dir; in the drives view the name is already a full root.
func childBrowseDir(dir, name string) string {
	if dir == "" {
		return name // drives view: the row name is the volume root itself
	}
	return filepath.Join(dir, name)
}

// parentDir climbs one level in the open browser's path model; the merged model clamps at the mount root.
func (s *demoBrowserState) parentDir() string {
	if s.merged {
		return mergedParentDir(s.dir)
	}
	return parentBrowseDir(s.dir)
}

// joinBrowseDir joins in the open browser's model;
// merged uses a forward slash, as a backslash matches nothing on the wire.
func (s *demoBrowserState) joinBrowseDir(name string) string {
	if s.merged {
		if s.dir == "" {
			return name
		}
		return s.dir + "/" + name
	}
	return childBrowseDir(s.dir, name)
}

// browseTitle names the modal for its purpose; each is a constant, so no per-frame alloc.
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
	case purposeEvidenceImage:
		return "Pick an evidence image from your local asset sources"
	case purposeBaseFolder:
		return "Open your base folder, then press Use this folder"
	default:
		return "Pick a .demo to turn into a video"
	}
}

// browseFileIcon is the glyph for a non-directory row; a constant per purpose, so the draw doesn't allocate.
func browseFileIcon(p browsePurpose) string {
	switch p {
	case purposeThemeBundle, purposeBaseFolder:
		return "📦"
	case purposeThemeImage, purposeEvidenceImage:
		return "🖼"
	}
	return "🎞"
}

// drawDemoBrowser draws the modal browser last in drawSettings,
// with the caller having released the page's modal capture.
func (a *App) drawDemoBrowser(w, h int32) {
	s := &demoBrowser
	if !s.open {
		return
	}
	c := a.ctx

	// A result for a dir other than s.dir is superseded: clear loading but keep the current listing.
	select {
	case r := <-s.res:
		s.loading = false
		if r.dir == s.dir {
			s.entries, s.more, s.loadErr = r.entries, r.more, r.err
		}
	default:
	}

	// Clamped so it never overflows a tiny window.
	const (
		bwMaxW = 640
		bwMaxH = 520
		bwMinW = 320
		bwMinH = 240
	)
	pw := value.Clamp(w-2*browseModalMargin, bwMinW, bwMaxW)
	ph := value.Clamp(h-2*browseModalMargin, bwMinH, bwMaxH)
	px := (w - pw) / 2
	py := (h - ph) / 2
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)

	inX := px + 12
	inW := pw - 24
	y := py + 10

	c.Label(inX, y, browseTitle(s.purpose), ColText)
	closeR := sdl.Rect{X: px + pw - 30, Y: py + 8, W: 22, H: 22}
	if c.Button(closeR, "✕") {
		s.open = false
		return
	}
	y += 26

	// A directory-picking purpose puts its commit button beside the path it commits; hidden in the drives view.
	// The merged browse labels the row with its own root.
	pathW := inW
	if s.merged {
		const srcLabel = "asset sources ▸"
		sw := c.TextWidth(srcLabel) + 8
		c.Label(inX, y, srcLabel, ColTextDim)
		if s.dir == "" {
			c.LabelClipped(inX+sw, y, pathW-sw, "(merged root)", ColTextDim)
		} else {
			c.LabelClipped(inX+sw, y, pathW-sw, s.dir, ColText)
		}
	} else {
		pathText := s.dir
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
	}
	y += 22

	// Each quick jump no-ops if its path didn't resolve. The merged browse has none,
	// because every one leaves the mounts;
	// its row says the listing is local only.
	if s.merged {
		c.LabelClipped(inX, y+4, inW, mergedBrowseTip, ColAccent)
		y += btnH + 8
	} else {
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
	}

	if s.loading {
		c.Label(inX, y, "Loading…", ColTextDim)
		y += 18
	} else if s.loadErr != "" {
		// The merged browse's one refusal is the no-sources state, already a sentence,
		// so the filesystem wording is skipped.
		note := "Can't open this folder: "
		if s.merged {
			note = ""
		}
		c.LabelClipped(inX, y, inW, note+s.loadErr, ColDanger)
		y += 18
	}

	listTop := y
	listH := (py + ph - 10) - listTop
	if listH < browseRowH {
		listH = browseRowH
	}
	listRect := sdl.Rect{X: inX, Y: listTop, W: inW, H: listH}
	c.Fill(listRect, ColPanel)

	// A leading ".." row, except at the drives view and the merged root,
	// where the clamp keeps the browser in its mounts.
	showUp := !s.isDrivesView() && !(s.merged && s.dir == "")
	rowCount := len(s.entries)
	if showUp {
		rowCount++
	}
	if s.more > 0 {
		rowCount++ // the "… and N more" tail
	}
	contentH := int32(rowCount) * browseRowH
	trackW := scrollBarW
	rowW := inW - 8
	if contentH > listH {
		rowW -= trackW + 4
	}

	if c.hovering(listRect) && c.wheelY != 0 {
		c.wheelTaken = true
		s.scroll -= c.wheelY * browseRowH
	}
	maxScroll := contentH - listH
	if maxScroll < 0 {
		maxScroll = 0
	}
	s.scroll = value.Clamp(s.scroll, 0, maxScroll)

	// Raw SetClipRect clips drawing only; pushClip mirrors the clip into hovering and ClickedIn. A flat loop,
	// so no closure.
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

		if s.more > 0 && i == int32(rowCount)-1 {
			c.LabelClipped(row.X+8, row.Y+4, row.W-16, browseMoreLabel(s.more), ColTextDim)
			continue
		}

		if c.hovering(row) {
			c.Fill(row, ColPanelHi)
		}

		if showUp && i == 0 {
			if c.ClickedIn(row) {
				a.navBrowseTo(s.parentDir())
			}
			c.Label(row.X+8, row.Y+4, "⬆", ColText)
			c.LabelClipped(row.X+browseIconColW, row.Y+4, row.W-browseIconColW-8, "..", ColText)
			continue
		}
		e := s.entries[i-upRows]
		if c.ClickedIn(row) {
			if e.isDir {
				a.navBrowseTo(s.joinBrowseDir(e.name))
			} else {
				a.pickBrowsedFile(s.joinBrowseDir(e.name))
			}
		}
		// Separate labels, so no per-frame icon+name concat allocates.
		icon := browseFileIcon(s.purpose)
		if e.isDir {
			icon = "📁"
		}
		c.Label(row.X+8, row.Y+4, icon, ColText)
		c.LabelClipped(row.X+browseIconColW, row.Y+4, row.W-browseIconColW-8, e.name, ColText)
	}
	c.popClip(clipPrev, clipHad)

	if contentH > listH {
		track := sdl.Rect{X: inX + inW - trackW, Y: listTop, W: trackW, H: listH}
		s.scroll = c.VScrollbar("demobrowse", track, s.scroll, contentH, listH)
	}
}

const browseModalMargin = 40

// browseMoreLabel formats the overflow row; only one row draws it, so the Sprintf is rare.
func browseMoreLabel(more int) string {
	return fmt.Sprintf("… and %d more (open a subfolder to narrow it down)", more)
}

// pickBrowsedFile closes the browser and hands the pick to the existing owner for its purpose; no branch does the work.
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
		// Same as a dragged picture. False means no editor over a writable theme,
		// impossible here as the editor opened this.
		a.editorTakeImage(path)
	case purposeBaseFolder:
		// path is the directory for Use-this-folder and a .zip for a row click; setBasePath takes either.
		baseWizard.setBasePath(path)
	case purposeEvidenceImage:
		// An origin-relative asset path re-rooted at the mounts' evidence/ folder; no filesystem path is involved.
		a.setEvidenceImageFromMergedPath(path)
	default:
		a.importRecordingToVideo(importDroppedRecording(path))
	}
}

// setEvidenceImageFromMergedPath fills the Image-file field from a merged-browse pick (see evidenceRelFromMerged).
func (a *App) setEvidenceImageFromMergedPath(path string) {
	a.evidImage = evidenceRelFromMerged(path)
}
