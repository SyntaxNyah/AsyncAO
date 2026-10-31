package ui

// The Studio "Check demo content" / "Package this RP" modal over the content engine (contentjob.go).
// Esc must route through closeTopOverlay, or a popup that blocks input silently reappears on the next Settings open.
// The draw is allocation-free: rows and summaries are cached until the report or filter changes.

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// cpRowH is one list row's height, matching the demo browser.
	cpRowH = 26
	// cpStatusColW is where an item name begins, leaving room for the "[unreachable]" tag.
	cpStatusColW = 96
	// cpPackTagW reserves the right-hand column for the "your folders" tag.
	cpPackTagW = 90
	// cpModalMargin insets the centered modal from the window edges.
	cpModalMargin = 40
	// cpSourceLabelW is the column the "Resolve from:" label occupies before the source buttons.
	cpSourceLabelW = 96
	// cpSourceBtnW is one source button's width.
	cpSourceBtnW = 96
	// cpBtnLabelInset is a disabled button's label inset, matching an enabled button's baseline.
	cpBtnLabelInset = 4
	// cpLocalSetupFieldW is the width of the inline local base folder field.
	cpLocalSetupFieldW = 300
	// cpLocalSetupUseW is the width of the "Use" button beside that field.
	cpLocalSetupUseW = 60
	// cpLocalSetupLabelPad clears the setup row's label, which is wider than the one cpSourceLabelW fits.
	cpLocalSetupLabelPad = 40
)

// cpRowKind tags a visible row: a category header or one asset item.
type cpRowKind int

const (
	cpRowHeader cpRowKind = iota // a "Character sprites: 3/5 found" header
	cpRowItem                    // one asset row (status tag + name)
)

// cpVisibleRow is one flattened, filtered row; a header carries its pre-built label, an item is read live through its
// indexes.
type cpVisibleRow struct {
	kind  cpRowKind
	cat   int    // category index for both kinds
	item  int    // item index within the category (cpRowItem only)
	label string // header summary text (cpRowHeader only) — pre-built, not per frame
}

// contentPanelState is the single content panel. It keeps the last non-nil report because tickContentPackage nils
// a.content when packaging finishes, which is exactly when the summary must still show.
type contentPanelState struct {
	open bool
	// rec is the recording the report came from; packaging needs its Events and StartBg, which the report doesn't
	// carry.
	rec  *sceneRecording
	stem string
	// report is the last non-nil report, cleared only when a fresh pick opens the panel.
	report *ContentReport
	// packageWhenReady is a one-shot request to package once ContentReportReady is true, so a second probe can't
	// auto-package.
	packageWhenReady bool
	// showAll lists every asset; by default only not-found ones. Header counts always cover the full category.
	showAll bool

	// Source picker. Both origins are captured on open; the choice mutates rec.Origin and restarts the job, so
	// enumeration and packaging read the same string: otherwise writeContentBundle's CutPrefix(url, rec.Origin) would
	// count every asset external and write an empty bundle.
	// An empty origin means none is available. useLocal sticks for the session.
	originServer string
	originLocal  string
	useLocal     bool
	// pickerChosen is set once the user picks a source; until then each open follows the default (demoDefaultOrigin).
	pickerChosen bool

	// localSetupOpen expands the inline local base row, shown when no mounts are configured; localSetupInput is its
	// text.
	localSetupOpen  bool
	localSetupInput string

	scroll int32
	// rows is the cached visible-row model, rebuilt when the report, its Totals or the filter changes; never per frame.
	rows       []cpVisibleRow
	rowsFilter bool
	rowsReport *ContentReport
	rowsSig    [4]int // (found, missing, unreachable, total) — mutates as probes land

	// summary caches the status line, with its own keys because the "(checking…)" suffix depends on probing, not just
	// the counts.
	summary        string
	summaryReport  *ContentReport
	summarySig     [4]int
	summaryProbing bool
	// originLine caches "Origin: <host>"; the origin is immutable per report.
	originLine   string
	originReport *ContentReport
	// title caches the modal title, rebuilt when the stem changes.
	title    string
	titleFor string
	// sawJob records that a job existed; doneMsg snapshots its final warnLine when the job clears, because the engine
	// exposes the bundle path only there and the toast fades.
	sawJob  bool
	doneMsg string
}

var contentPanel contentPanelState

// openContentReportFor loads a recording, starts the single-flight probe and opens the panel; pkg requests packaging
// once ready.
// Render thread only. A bad load or refused start leaves the panel closed with warnLine explaining why.
func (a *App) openContentReportFor(path string, pkg bool) {
	rec, err := a.loadRecordingAny(path) // .aorec, or an AO2 .demo converted on the fly
	if err != nil {
		a.warnLine = "Couldn't load recording: " + err.Error()
		a.warnAt = a.now()
		return
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	s := &contentPanel
	// Capture both source candidates before the probe starts.
	s.originServer = a.contentServerOrigin(rec)
	s.originLocal = a.mountOrigin()
	// Default from rec.Origin's scheme, which keeps the policy in demoDefaultOrigin alone; an explicit pick sticks for
	// the session.
	if !s.pickerChosen {
		s.useLocal = strings.HasPrefix(rec.Origin, assets.LocalScheme)
	}
	// A no-op unless there's an explicit sticky pick.
	rec.Origin = s.chosenOrigin()

	if !a.StartContentReport(rec, stem) {
		return // StartContentReport set warnLine (busy / empty) and returned false
	}
	// Fresh run: reset the cached report + rows so a prior panel's data can't flash.
	s.open = true
	s.rec = rec
	s.stem = stem
	a.resetContentPanelRun(pkg)
}

// resetContentPanelRun clears per-run caches so a fresh probe can't flash a prior run's data; pkg re-requests
// packaging.
func (a *App) resetContentPanelRun(pkg bool) {
	s := &contentPanel
	s.report = nil
	s.rows = s.rows[:0]
	s.rowsReport = nil
	s.rowsSig = [4]int{}
	s.summaryReport = nil // drop the per-report string caches so a new run rebuilds
	s.originReport = nil
	s.scroll = 0
	s.showAll = false
	s.packageWhenReady = pkg
	s.sawJob = false // a fresh run: forget the prior job's done-summary
	s.doneMsg = ""
	s.localSetupOpen = false // collapse any stale inline base-setup row
	s.localSetupInput = ""
}

// contentServerOrigin recovers the non-local origin for the picker: rec.Origin if not local://, else the live session's
// AssetURL, since a .demo stores none. Render thread only.
func (a *App) contentServerOrigin(rec *sceneRecording) string {
	if rec != nil && !strings.HasPrefix(rec.Origin, assets.LocalScheme) {
		return rec.Origin // already the server origin (stamped .aorec, or mounts-off .demo)
	}
	if a.sess != nil {
		return a.sess.AssetURL // a .demo under local mounts: the live session is the server candidate
	}
	return ""
}

// chosenOrigin resolves the selection to an origin, falling back to the server if mounts were cleared between opens.
func (s *contentPanelState) chosenOrigin() string {
	if s.useLocal && s.originLocal != "" {
		return s.originLocal
	}
	return s.originServer
}

// switchContentSource re-runs the report against the other origin, reporting whether it restarted; a no-op when the
// requested state already holds, so a running probe isn't torn down.
// Packaging requested before the switch still happens. Render thread only.
func (a *App) switchContentSource(useLocal bool) bool {
	s := &contentPanel
	if s.rec == nil {
		return false // nothing loaded to re-run
	}
	if useLocal && s.originLocal == "" {
		return false // no mounts to switch to — leave the current run alone
	}
	if useLocal == s.useLocal && s.pickerChosen {
		return false // already on this source by explicit choice: nothing to do
	}
	s.useLocal = useLocal
	s.pickerChosen = true
	origin := s.chosenOrigin()
	if s.rec.Origin == origin && s.report != nil {
		// A report already exists for this origin: don't tear down a good run.
		return false
	}
	a.CancelContentJob()  // free the single-flight slot + reap the current probe
	s.rec.Origin = origin // the ONE origin enumeration + packaging both read
	pkg := s.packageWhenReady
	if !a.StartContentReport(s.rec, s.stem) {
		// Refused, which should not happen as the job was just cancelled; the caches are not yet cleared.
		return false
	}
	a.resetContentPanelRun(pkg)
	return true
}

// closeContentPanel closes the panel and cancels the job; leaving contentBusy set would refuse the next report and
// strand the probe goroutines.
func (a *App) closeContentPanel() {
	s := &contentPanel
	s.open = false
	s.rec = nil
	s.report = nil
	s.packageWhenReady = false
	s.sawJob = false // so the done-summary snapshot can't leak into a later open
	s.doneMsg = ""
	a.CancelContentJob()
}

// contentReportRows rebuilds the cached rows when the report, its Totals or the filter changed.
func (s *contentPanelState) contentReportRows(r *ContentReport) []cpVisibleRow {
	if r == nil {
		s.rows = s.rows[:0]
		s.rowsReport = nil
		return s.rows
	}
	found, missing, unreachable, total := r.Totals()
	sig := [4]int{found, missing, unreachable, total}
	if s.rowsReport == r && s.rowsFilter == s.showAll && s.rowsSig == sig {
		return s.rows // unchanged: reuse the cached model (no per-frame alloc)
	}
	s.rows = s.rows[:0]
	for ci := range r.Categories {
		c := &r.Categories[ci]
		if c.Total() == 0 {
			continue // don't list an empty category (matches FormatReport)
		}
		s.rows = append(s.rows, cpVisibleRow{kind: cpRowHeader, cat: ci, label: contentHeaderLabel(c)})
		for ii := range c.Items {
			if !s.showAll && c.Items[ii].Status == StatusFound {
				continue // default filter: hide the found assets (a recipient has them)
			}
			s.rows = append(s.rows, cpVisibleRow{kind: cpRowItem, cat: ci, item: ii})
		}
	}
	s.rowsReport, s.rowsFilter, s.rowsSig = r, s.showAll, sig
	return s.rows
}

// contentStatusLine returns the cached summary, rebuilt when the Totals or probing flag changes.
func (s *contentPanelState) contentStatusLine(r *ContentReport, probing bool) string {
	found, missing, unreachable, total := r.Totals()
	sig := [4]int{found, missing, unreachable, total}
	if s.summaryReport == r && s.summarySig == sig && s.summaryProbing == probing {
		return s.summary
	}
	line := "Referenced: " + strconv.Itoa(total) + "  ·  " + strconv.Itoa(found) + " found, " + strconv.Itoa(missing) + " missing"
	if unreachable > 0 {
		line += ", " + strconv.Itoa(unreachable) + " unreachable"
	}
	if probing {
		line += "  (checking…)" // still probing: counts grow as results land
	}
	s.summary, s.summaryReport, s.summarySig, s.summaryProbing = line, r, sig, probing
	return line
}

// contentOriginLine returns the cached "Origin: <host>" line.
func (s *contentPanelState) contentOriginLine(r *ContentReport) string {
	if s.originReport != r {
		s.originLine, s.originReport = "Origin: "+r.Origin, r
	}
	return s.originLine
}

// contentTitle returns the cached modal title.
func (s *contentPanelState) contentTitle() string {
	if s.titleFor != s.stem {
		if s.stem == "" {
			s.title = "Demo content report"
		} else {
			s.title = "Content report — " + s.stem
		}
		s.titleFor = s.stem
	}
	return s.title
}

// cpStatusColor maps a probe status to the row-tag colour; unknown is dim while still probing.
func cpStatusColor(st AssetStatus) sdl.Color {
	switch st {
	case StatusFound:
		return ColAccent
	case StatusMissing, StatusUnreachable:
		return ColDanger
	default:
		return ColTextDim
	}
}

// cpStatusTag is the bracketed status label, a constant so it hits the label texture cache.
func cpStatusTag(st AssetStatus) string {
	switch st {
	case StatusFound:
		return "[found]"
	case StatusMissing:
		return "[missing]"
	case StatusUnreachable:
		return "[unreachable]"
	default:
		return "[checking]"
	}
}

// drawContentSourcePicker draws the "Resolve from" control and returns the y past it.
// "This server" needs a streaming Manager: a local-mode one has no network source, so probes would give false rows.
// "Local base" works in any mode once a base exists; with none, clicking in streaming mode expands the inline setup
// row.
func (a *App) drawContentSourcePicker(s *contentPanelState, inX, y int32) int32 {
	c := a.ctx
	// A local-mode Manager has no network client; nil-guarded for draws with no Manager.
	localModeMgr := a.d.Manager != nil && a.d.Manager.LocalMode()
	c.Label(inX, y+4, "Resolve from:", ColTextDim)
	bx := inX + cpSourceLabelW
	// A disabled segment draws dim and never fires.
	seg := func(label string, active, enabled bool) bool {
		r := sdl.Rect{X: bx, Y: y, W: cpSourceBtnW, H: btnH}
		bx += cpSourceBtnW + 6
		if active {
			// Re-selecting the active source still reports a click, but switchContentSource ignores it.
			return c.ButtonCol(r, label, ColAccent, ColAccent, ColAccent, ColBackground)
		}
		if !enabled {
			// Disabled: draw only, swallowing the click.
			c.Fill(r, ColPanel)
			c.Border(r, ColPanelHi)
			c.LabelClipped(r.X+8, r.Y+cpBtnLabelInset, r.W-12, label, ColTextDim)
			return false
		}
		return c.Button(r, label)
	}
	useLocal := s.useLocal && s.originLocal != ""
	// "Local base" stays enabled in streaming mode with no base, because the click opens the setup row.
	serverServable := !localModeMgr
	localServable := (!localModeMgr) || s.originLocal != ""
	if seg("This server", !useLocal, serverServable) {
		a.switchContentSource(false)
	}
	if seg("Local base", useLocal, localServable) {
		if s.originLocal == "" {
			// No base yet: open the setup row; switchContentSource(true) would no-op on an empty originLocal.
			s.localSetupOpen = !s.localSetupOpen
		} else {
			s.localSetupOpen = false // a real base exists: switch, close any stale setup row
			a.switchContentSource(true)
		}
	}
	y += btnH + 6
	if s.localSetupOpen && s.originLocal == "" {
		y = a.drawLocalSetupRow(s, inX, y)
	}
	return y
}

// drawLocalSetupRow draws the inline local base row: a folder field and a Use button, with no path validation, as in
// Settings.
func (a *App) drawLocalSetupRow(s *contentPanelState, inX, y int32) int32 {
	c := a.ctx
	c.Label(inX, y+4, "Local base folder:", ColTextDim)
	fx := inX + cpSourceLabelW + cpLocalSetupLabelPad // clears the (wider) label before the field begins
	s.localSetupInput, _ = c.TextField("contentLocalBase",
		sdl.Rect{X: fx, Y: y, W: cpLocalSetupFieldW, H: fieldH}, s.localSetupInput,
		`C:\AO2\base or /home/you/ao2/base`)
	if c.Button(sdl.Rect{X: fx + cpLocalSetupFieldW + 6, Y: y, W: cpLocalSetupUseW, H: btnH}, "Use") {
		a.useLocalSetupPath()
	}
	return y + btnH + 6
}

// useLocalSetupPath adds the input path as a mount (the enabled flag unchanged: it's only a resolution base), rebuilds
// the asset origin and re-runs the report; empty input is a no-op. Render thread only.
func (a *App) useLocalSetupPath() {
	s := &contentPanel
	path := strings.TrimSpace(s.localSetupInput)
	if path == "" {
		return // empty input: nothing to set
	}
	enabled, mounts := a.d.Prefs.LocalAssets()
	a.d.Prefs.SetLocalAssets(enabled, append(mounts, path)) // enabled UNCHANGED
	a.rebuildAssetOrigin()                                  // pushes the new overlay into the Manager
	s.originLocal = a.mountOrigin()                         // recompute against the now-configured base
	s.localSetupInput = ""
	s.localSetupOpen = false
	// Clear any stale sticky useLocal, which would trip switchContentSource's "already on this source" check and skip
	// the re-run.
	s.useLocal = false
	a.switchContentSource(true) // re-run the report against the new base
}

// drawContentPanel draws the modal last in drawSettings, after the caller unblocked input, so its c.Button and
// c.hovering work.
func (a *App) drawContentPanel(w, h int32) {
	s := &contentPanel
	if !s.open {
		return
	}
	c := a.ctx

	// Keep the last non-nil report: tickContentPackage nils a.content when packaging finishes.
	if r := a.ContentJobReport(); r != nil {
		s.report = r
		s.sawJob = true
	} else if s.sawJob && s.doneMsg == "" {
		// The job just cleared: snapshot the summary, only exposed through warnLine, before the toast fades.
		s.doneMsg = a.warnLine
	}
	// Fire the build the first frame the probe is ready.
	if s.packageWhenReady && a.ContentReportReady() {
		s.packageWhenReady = false
		a.PackageContentBundle(s.rec)
	}

	// Centered modal, clamped so it never overflows a tiny window.
	const (
		cpMaxW = 720
		cpMaxH = 560
		cpMinW = 360
		cpMinH = 260
	)
	pw := value.Clamp(w-2*cpModalMargin, cpMinW, cpMaxW)
	ph := value.Clamp(h-2*cpModalMargin, cpMinH, cpMaxH)
	px := (w - pw) / 2
	py := (h - ph) / 2
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)

	inX := px + 12
	inW := pw - 24
	y := py + 10

	// Title and close; closing cancels the job.
	c.LabelClipped(inX, y, inW-32, s.contentTitle(), ColText)
	if c.Button(sdl.Rect{X: px + pw - 30, Y: py + 8, W: 22, H: 22}, "✕") {
		a.closeContentPanel()
		return
	}
	y += 26

	y = a.drawContentSourcePicker(s, inX, y)

	rep := s.report

	// An origin-missing report shows the no-server notice, not a 100%-missing list.
	switch {
	case rep == nil:
		c.LabelClipped(inX, y, inW, "Enumerating the recording…", ColTextDim)
		y += 20
	case rep.OriginMissing:
		c.LabelClipped(inX, y, inW, "No server recorded for this file — nothing could be checked.", ColDanger)
		y += 18
		c.LabelClipped(inX, y, inW, "Connect to the recording's server, set Origin/CDN in the Scene Maker, or pick Local base and set a local assets folder, then re-run.", ColTextDim)
		y += 20
	default:
		c.LabelClipped(inX, y, inW, s.contentOriginLine(rep), ColTextDim)
		y += 18
		// "probing" is the actual phase, not !ContentReportReady: that is also false during and after packaging, which
		// would stick "(checking…)".
		probing := a.content != nil && a.content.phase == phaseProbing
		c.LabelClipped(inX, y, inW, s.contentStatusLine(rep, probing), ColText)
		y += 20
	}

	bx := inX
	btn := func(label string, wide int32) bool {
		r := sdl.Rect{X: bx, Y: y, W: wide, H: btnH}
		bx += wide + 6
		return c.Button(r, label)
	}
	if rep != nil && !rep.OriginMissing {
		filterLabel := "Show all"
		if s.showAll {
			filterLabel = "Only missing"
		}
		if btn(filterLabel, 108) {
			s.showAll = !s.showAll
			s.scroll = 0
		}
		if btn("Copy list", 96) {
			// SDL clipboard is render-thread only; FormatReport keeps the copied text equal to the bundle's
			// MISSING-CONTENT.txt.
			_ = sdl.SetClipboardText(strings.Join(FormatReport(rep), "\n"))
			a.warnLine = "Copied the content report to the clipboard."
			a.warnAt = a.now()
		}
		// Shown only while a probed report is idle, so this packages directly; packageWhenReady stays for the browser's
		// flow.
		if a.ContentReportReady() {
			if btn("Package this RP", 140) {
				a.PackageContentBundle(s.rec)
			}
		}
	}
	// Cancel while any phase of the job is still live (probing or packaging).
	if a.contentJobRunning() {
		if btn("Cancel", 80) {
			a.closeContentPanel()
			return
		}
	}
	y += btnH + 8

	// Keep the packaged summary visible after the toast fades.
	if s.doneMsg != "" {
		c.LabelClipped(inX, y, inW, s.doneMsg, ColAccent)
		y += 20
	}

	a.drawContentList(s, rep, inX, y, inW, py+ph-10)
}

// drawContentList draws the scrollable, read-only list of category headers and filtered items.
func (a *App) drawContentList(s *contentPanelState, rep *ContentReport, inX, listTop, inW, listBottom int32) {
	c := a.ctx
	listH := listBottom - listTop
	if listH < cpRowH {
		listH = cpRowH
	}
	listRect := sdl.Rect{X: inX, Y: listTop, W: inW, H: listH}
	c.Fill(listRect, ColPanel)

	// An origin-missing / not-yet-enumerated report has no rows to list.
	if rep == nil || rep.OriginMissing {
		if rep != nil && rep.OriginMissing {
			c.LabelClipped(inX+8, listTop+6, inW-16, "Nothing to list — no assets were checked.", ColTextDim)
		}
		return
	}

	rows := s.contentReportRows(rep)
	rowCount := int32(len(rows))
	contentH := rowCount * cpRowH
	rowW := inW - 8
	if contentH > listH {
		rowW -= scrollBarW + 4 // reserve the scrollbar gutter only when it overflows
	}

	// Wheel scrolls only while hovered.
	if c.hovering(listRect) && c.wheelY != 0 {
		c.wheelTaken = true
		s.scroll -= c.wheelY * cpRowH
	}
	maxScroll := contentH - listH
	if maxScroll < 0 {
		maxScroll = 0
	}
	s.scroll = value.Clamp(s.scroll, 0, maxScroll)
	if rowCount == 0 {
		c.LabelClipped(inX+8, listTop+6, inW-16, "No missing content — every referenced asset was found.", ColAccent)
		return
	}

	// pushClip mirrors the clip into hovering(), so a wheel over the list doesn't leak past its edge.
	clipPrev, clipHad := c.pushClip(listRect)
	for i := int32(0); i < rowCount; i++ {
		ry := listTop - s.scroll + i*cpRowH
		if ry+cpRowH < listTop || ry > listTop+listH {
			continue // cull rows outside the window
		}
		vr := rows[i]
		if vr.kind == cpRowHeader {
			// Category header with its pre-built summary.
			hr := sdl.Rect{X: inX + 4, Y: ry, W: rowW, H: cpRowH - 2}
			c.Fill(hr, ColPanelHi)
			c.LabelClipped(hr.X+6, hr.Y+4, hr.W-12, vr.label, ColText)
			continue
		}
		it := &rep.Categories[vr.cat].Items[vr.item]
		// Status tag + name as two separate cached labels (no per-frame concat).
		c.Label(inX+8, ry+4, cpStatusTag(it.Status), cpStatusColor(it.Status))
		nameW := rowW - cpStatusColW
		if it.FromPack {
			// The one fact a recipient needs: this asset came from the sender's folders, so they won't have it.
			const packTag = "your folders"
			c.Label(inX+inW-cpPackTagW, ry+4, packTag, ColTierYellow)
			nameW -= cpPackTagW
		}
		c.LabelClipped(inX+cpStatusColW, ry+4, nameW, it.Name, ColText)
	}
	c.popClip(clipPrev, clipHad)

	if contentH > listH {
		track := sdl.Rect{X: inX + inW - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
		s.scroll = c.VScrollbar("contentlist", track, s.scroll, contentH, listH)
	}
}

// contentHeaderLabel is a category's header text, built once per row rebuild.
func contentHeaderLabel(c *CategoryReport) string {
	head := c.Cat.Name() + ": " + strconv.Itoa(c.Found) + "/" + strconv.Itoa(c.Total()) + " found"
	if c.Missing > 0 {
		head += ", " + strconv.Itoa(c.Missing) + " missing"
	}
	if c.Unreachable > 0 {
		head += ", " + strconv.Itoa(c.Unreachable) + " unreachable"
	}
	if c.FromPack > 0 {
		// Counted apart from found: a recipient of this recording won't have these.
		head += ", " + strconv.Itoa(c.FromPack) + " from your folders"
	}
	return head
}

// contentJobRunning reports whether work is in flight (probing or packaging), which is when the Cancel button shows.
func (a *App) contentJobRunning() bool {
	return a.content != nil && (a.content.phase == phaseProbing || a.content.phase == phasePackaging)
}
