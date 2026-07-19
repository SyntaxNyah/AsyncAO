package ui

// contentpanel.go — the Studio "Check demo content" / "Package this RP" surface
// over the content engine (contentjob.go). It is a MODAL overlay drawn last in
// drawSettings, built to the same idioms as the in-app .demo browser
// (demobrowser.go): one package-level state (Settings is single-instance), a
// single-flight async job it drives via the render-thread tick, a pushClip'd
// scrollable list, kit VScrollbar, and Esc routed through closeTopOverlay so one
// press peels it off (floatbox.go doctrine — a fenced popup MUST answer Esc or it
// silently reappears on the next Settings open).
//
// Data comes entirely from the engine's exported model: ContentReport /
// CategoryReport / AssetItem + the shared FormatReport (used verbatim for the
// Copy button, so the panel text and the bundle's MISSING-CONTENT.txt never
// drift). This file owns NO probing/packaging logic — it only starts, queries,
// packages, and cancels the job through the engine's API.
//
// Perf/hard-rule discipline (spec §17): the draw is alloc-free. The item list is
// a CACHED "visible rows" slice rebuilt only when the filter toggles or the
// report mutates (a probe result landing changes the Totals signature) — never a
// fresh filtered slice per frame. Rows draw as separate cached labels (status
// tag + name), never a per-frame fmt.Sprintf (that both allocates and misses the
// label texture cache — the demobrowser lesson). No filesystem/network touch on
// the render thread: the pick loads the rec once (event-loop one-off, like
// importDroppedRecording), and the probe is the async single-flight job.

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	// cpRowH is the pitch of one list row (header or item) — matches the demo
	// browser's 26px rows so the two Studio lists feel identical.
	cpRowH = 26
	// cpStatusColW is the fixed x-offset (from a row's text start) where an item
	// NAME begins, leaving a constant column for the cached [status] tag so the tag
	// and name draw as two separate cached labels (no per-frame concat). Wide
	// enough for "[unreachable]".
	cpStatusColW = 96
	// cpModalMargin is the inset of the centered modal from the window edges (the
	// demo browser's browseModalMargin sibling).
	cpModalMargin = 40
)

// cpRowKind tags a visible row: a category header or one asset item.
type cpRowKind int

const (
	cpRowHeader cpRowKind = iota // a "Character sprites: 3/5 found" header
	cpRowItem                    // one asset row (status tag + name)
)

// cpVisibleRow is one flattened, filtered row in the list. For a header it
// carries the category index (into report.Categories) AND its pre-built summary
// label (so the draw never re-formats a header per frame — the counts only change
// when the row model rebuilds, which is exactly when this is rebuilt). For an item
// it carries the category + item index so the draw reads the live AssetItem
// directly (its Name is already a cached string; the status tag is a constant), so
// item rows need no stored label. Built by contentReportRows and cached until the
// report/filter changes.
type cpVisibleRow struct {
	kind  cpRowKind
	cat   int    // category index for both kinds
	item  int    // item index within the category (cpRowItem only)
	label string // header summary text (cpRowHeader only) — pre-built, not per frame
}

// contentPanelState is the single Studio content panel (package-level like
// demoBrowser — the Settings screen is single-instance). It holds no report of
// its own: the engine owns that. It DOES cache the last non-nil report pointer,
// because tickContentPackage nils a.content the instant packaging finishes — a
// panel reading a.ContentJobReport() straight would blank exactly when it should
// show the "Packaged N…" summary. It also retains the picked recording so the
// in-panel "Package this RP" button (and the package-purpose latch) have the
// Events/StartBg the report itself doesn't carry.
type contentPanelState struct {
	open bool
	// rec is the recording the report was started from — retained so packaging has
	// its Events/StartBg (the report is asset-only). stem is its display/bundle name.
	rec  *sceneRecording
	stem string
	// report is the last non-nil report seen (see the struct doc): the engine's
	// pointer while the job lives, retained across the post-package clear so the
	// final summary still renders. Cleared to nil only when a fresh pick opens the
	// panel, so a stale report can't flash into a new run.
	report *ContentReport
	// packageWhenReady latches the "Package this RP" purpose: set when the panel is
	// opened for packaging (or the in-panel Package button is pressed before the
	// probe finished), consumed once ContentReportReady() flips true. One-shot so a
	// second probe pass can't auto-package.
	packageWhenReady bool
	// showAll toggles the item filter: false (default) lists only not-found assets
	// (missing/unreachable — what a recipient needs to fetch); true lists every
	// enumerated asset. The header counts always reflect the full category.
	showAll bool

	scroll int32
	// rows is the cached visible-row model (headers + filtered items). Rebuilt only
	// when the report identity, its Totals signature, or the filter changes — never
	// per frame. rowsFilter / rowsSig capture what it was built for.
	rows       []cpVisibleRow
	rowsFilter bool
	rowsReport *ContentReport
	rowsSig    [4]int // (found, missing, unreachable, total) — mutates as probes land

	// summary caches the "Referenced: N · F found, M missing …" status line so the
	// per-frame draw doesn't re-format it (a live-count concat). Rebuilt when the
	// Totals signature OR the still-probing flag changes — its own cache keys, since
	// the "(checking…)" suffix depends on ContentReportReady, not just the counts.
	summary        string
	summaryReport  *ContentReport
	summarySig     [4]int
	summaryProbing bool
	// originLine caches "Origin: <host>" — the origin is immutable per report, so
	// this rebuilds only when the report pointer changes (not per frame).
	originLine   string
	originReport *ContentReport
	// title caches the modal title ("Content report — <stem>"), rebuilt only when
	// the stem changes (once per open) — not the per-frame concat.
	title    string
	titleFor string
	// sawJob latches that a job existed this session; doneMsg captures the engine's
	// final warnLine (the "Packaged N assets (X MB) into <folder>." summary + bundle
	// path) at the moment the job clears — the engine nils a.content and exposes the
	// path ONLY through warnLine, so the panel snapshots it there to keep showing it
	// after the toast fades. Cleared when a fresh pick opens the panel.
	sawJob  bool
	doneMsg string
}

var contentPanel contentPanelState

// openContentReportFor is the pick tail for the check/package browser purposes:
// load the recording (fills Origin from the live session via demoDefaultOrigin,
// like every other loader), start the single-flight probe, and open the panel. A
// bad load or a refused start (a job already running, empty recording) leaves the
// panel closed and the engine's warnLine explaining why. pkg latches the package
// build to fire once the probe is ready.
//
// Render thread only: loadRecordingAny emits its import notes on this thread, and
// StartContentReport spawns the probe goroutines here. The load is a one-off
// event-loop read (the importDroppedRecording precedent), never a render-path I/O.
func (a *App) openContentReportFor(path string, pkg bool) {
	rec, err := a.loadRecordingAny(path) // .aorec, or an AO2 .demo converted on the fly
	if err != nil {
		a.warnLine = "Couldn't load recording: " + err.Error()
		a.warnAt = a.now()
		return
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if !a.StartContentReport(rec, stem) {
		return // StartContentReport set warnLine (busy / empty) and returned false
	}
	// Fresh run: reset the cached report + rows so a prior panel's data can't flash.
	s := &contentPanel
	s.open = true
	s.rec = rec
	s.stem = stem
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
}

// closeContentPanel closes the panel AND cancels the running job (idempotent —
// CancelContentJob no-ops when no job runs). Both the ✕ and the Esc path route
// here: leaving contentBusy set would refuse the next report and strand the probe
// goroutines. The retained rec/report are dropped so the next open starts clean.
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

// contentReportRows rebuilds the cached visible-row model when the report
// identity, its Totals signature, or the filter changed since last build. Headers
// always show (per category with any enumerated item); items are included only if
// they pass the filter (all, or not-found-only). Cheap and bounded — one pass over
// the report — and skipped entirely on a settled frame (the guard returns early).
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

// contentStatusLine returns the cached "Referenced: N · F found, M missing …"
// summary for a probed report, rebuilt only when the Totals signature or the
// still-probing flag changes (so the live-count concat runs once per change, not
// once per frame). probing carries whether the report is still being probed (the
// "(checking…)" suffix) — a separate cache key from the counts.
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

// contentOriginLine returns the cached "Origin: <host>" line (rebuilt only when
// the report pointer changes — the origin is immutable per report).
func (s *contentPanelState) contentOriginLine(r *ContentReport) string {
	if s.originReport != r {
		s.originLine, s.originReport = "Origin: "+r.Origin, r
	}
	return s.originLine
}

// contentTitle returns the cached modal title (rebuilt only when the stem changes
// — once per open, not per frame).
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

// cpStatusColor maps a probe status to the row-tag colour (found = accent,
// missing/unreachable = danger, unknown = dim while still probing).
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

// cpStatusTag is the bracketed status label ("[found]", "[missing]", …). Each is
// a package const (the String() switch returns a constant), so building it costs
// no alloc and hits the label texture cache like any static string.
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

// drawContentPanel draws the modal content-report panel LAST in drawSettings
// (topmost), with the page's modal fence already released by the caller so its
// c.Button / c.VScrollbar / c.hovering widgets work normally. No-op when closed.
// Purely reads the engine's report (cached across the post-package clear); it
// never mutates the report or touches the filesystem/network.
func (a *App) drawContentPanel(w, h int32) {
	s := &contentPanel
	if !s.open {
		return
	}
	c := a.ctx

	// Retain the last non-nil report: tickContentPackage nils a.content when
	// packaging finishes, so read the live one while it exists and keep it after.
	if r := a.ContentJobReport(); r != nil {
		s.report = r
		s.sawJob = true
	} else if s.sawJob && s.doneMsg == "" {
		// The job just cleared (packaging finished, or a report-only end): the engine
		// exposes the "Packaged N… into <folder>" summary + bundle path ONLY via
		// warnLine, so snapshot it once here to keep showing it after the toast fades.
		// A close/Cancel can't reach this (it clears sawJob + closes the panel).
		s.doneMsg = a.warnLine
	}
	// Package-when-ready latch: fire the build the first frame the probe is ready.
	if s.packageWhenReady && a.ContentReportReady() {
		s.packageWhenReady = false
		a.PackageContentBundle(s.rec)
	}

	// Centered modal panel, clamped so it never overflows a tiny window (the demo
	// browser's sizing, a touch wider for the longer item names).
	const (
		cpMaxW = 720
		cpMaxH = 560
		cpMinW = 360
		cpMinH = 260
	)
	pw := clampI32(w-2*cpModalMargin, cpMinW, cpMaxW)
	ph := clampI32(h-2*cpModalMargin, cpMinH, cpMaxH)
	px := (w - pw) / 2
	py := (h - ph) / 2
	panel := sdl.Rect{X: px, Y: py, W: pw, H: ph}
	c.Fill(panel, ColBackground)
	c.Border(panel, ColAccent)

	inX := px + 12
	inW := pw - 24
	y := py + 10

	// Title + ✕ close (top-right). Close cancels the job (closeContentPanel).
	c.LabelClipped(inX, y, inW-32, s.contentTitle(), ColText)
	if c.Button(sdl.Rect{X: px + pw - 30, Y: py + 8, W: 22, H: 22}, "✕") {
		a.closeContentPanel()
		return
	}
	y += 26

	rep := s.report

	// Origin / status line. An origin-missing report (a silent imported demo) shows
	// the no-server notice, NOT a 100%-missing list — the engine flags OriginMissing.
	switch {
	case rep == nil:
		c.LabelClipped(inX, y, inW, "Enumerating the recording…", ColTextDim)
		y += 20
	case rep.OriginMissing:
		c.LabelClipped(inX, y, inW, "No server recorded for this file — nothing could be checked.", ColDanger)
		y += 18
		c.LabelClipped(inX, y, inW, "Connect to the recording's server, or set Origin/CDN in the Scene Maker, then re-run.", ColTextDim)
		y += 20
	default:
		c.LabelClipped(inX, y, inW, s.contentOriginLine(rep), ColTextDim)
		y += 18
		// Both lines come from per-change caches (contentStatusLine), so the live-count
		// concat runs once per probe result, never once per frame. "probing" is the
		// ACTUAL phase — NOT !ContentReportReady(): that reads false during packaging
		// AND after the post-package clear, which would stick "(checking…)" on forever.
		probing := a.content != nil && a.content.phase == phaseProbing
		c.LabelClipped(inX, y, inW, s.contentStatusLine(rep, probing), ColText)
		y += 20
	}

	// Action row: filter toggle, Copy list, Package (when a probed report is ready
	// and has a real origin), and Cancel while the job is still running.
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
			// SDL clipboard is render-thread only (the exportPhoneBook precedent). The
			// shared formatter is used verbatim so the copied text equals the bundle's
			// MISSING-CONTENT.txt — one rendering, two sinks. Confirm via the standard
			// warnLine toast (the house feedback channel — no bespoke flicker label).
			_ = sdl.SetClipboardText(strings.Join(FormatReport(rep), "\n"))
			a.warnLine = "Copied the content report to the clipboard."
			a.warnAt = a.now()
		}
		// Package: shown ONLY while a probed report is idle-ready (phaseReport) — not
		// during probing, not during packaging, and not after the post-package clear
		// (all leave ContentReportReady false). So the button always packages directly;
		// the packageWhenReady latch stays exclusively for the browser's purposePackage
		// flow (consumed in the ready-transition check at the top of the draw).
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

	// The packaged summary + bundle path (snapshotted from warnLine when the job
	// cleared), so "Packaged N assets (X MB) into <folder>." stays visible after the
	// toast fades — the task's "when a package build finishes" line.
	if s.doneMsg != "" {
		c.LabelClipped(inX, y, inW, s.doneMsg, ColAccent)
		y += 20
	}

	a.drawContentList(s, rep, inX, y, inW, py+ph-10)
}

// drawContentList draws the pushClip'd, scrollable list of category headers +
// filtered item rows between listTop and listBottom. Rows draw in a flat INDEX
// loop over the cached visible-row model (no per-frame closure/alloc — the demo
// browser pattern). Item rows are non-clickable (report is read-only); the list
// only scrolls.
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

	// Wheel scrolls only while hovered (single-consumer memory).
	if c.hovering(listRect) && c.wheelY != 0 {
		c.wheelTaken = true
		s.scroll -= c.wheelY * cpRowH
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
	if rowCount == 0 {
		c.LabelClipped(inX+8, listTop+6, inW-16, "No missing content — every referenced asset was found.", ColAccent)
		return
	}

	// Clip the rows: pushClip mirrors the clip into hovering() so a wheel over the
	// list doesn't leak past its edge (the demo browser's clip discipline).
	clipPrev, clipHad := c.pushClip(listRect)
	for i := int32(0); i < rowCount; i++ {
		ry := listTop - s.scroll + i*cpRowH
		if ry+cpRowH < listTop || ry > listTop+listH {
			continue // cull rows outside the window
		}
		vr := rows[i]
		if vr.kind == cpRowHeader {
			// Category header: a filled strip with the PRE-BUILT "Name: F/T found, …"
			// summary (vr.label — never re-formatted per frame; see cpVisibleRow).
			hr := sdl.Rect{X: inX + 4, Y: ry, W: rowW, H: cpRowH - 2}
			c.Fill(hr, ColPanelHi)
			c.LabelClipped(hr.X+6, hr.Y+4, hr.W-12, vr.label, ColText)
			continue
		}
		it := &rep.Categories[vr.cat].Items[vr.item]
		// Status tag + name as two separate cached labels (no per-frame concat).
		c.Label(inX+8, ry+4, cpStatusTag(it.Status), cpStatusColor(it.Status))
		c.LabelClipped(inX+cpStatusColW, ry+4, rowW-cpStatusColW, it.Name, ColText)
	}
	c.popClip(clipPrev, clipHad)

	if contentH > listH {
		track := sdl.Rect{X: inX + inW - scrollBarW, Y: listTop, W: scrollBarW, H: listH}
		s.scroll = c.VScrollbar("contentlist", track, s.scroll, contentH, listH)
	}
}

// contentHeaderLabel is the per-category header text ("Character sprites: 3/5
// found, 2 missing"). Built once per row model rebuild (never per frame — the row
// model is cached), so the single concat here rides the same budget as the demo
// browser's overflow-tail label.
func contentHeaderLabel(c *CategoryReport) string {
	head := c.Cat.Name() + ": " + strconv.Itoa(c.Found) + "/" + strconv.Itoa(c.Total()) + " found"
	if c.Missing > 0 {
		head += ", " + strconv.Itoa(c.Missing) + " missing"
	}
	if c.Unreachable > 0 {
		head += ", " + strconv.Itoa(c.Unreachable) + " unreachable"
	}
	return head
}

// contentJobRunning reports whether a content job exists in ANY live phase
// (probing or packaging) — the Cancel button's gate. Distinct from
// ContentReportReady (probe done, idle in phaseReport): a ready-but-idle report
// is still cancellable via the ✕/Esc close, but the Cancel BUTTON only shows
// while work is actually in flight.
func (a *App) contentJobRunning() bool {
	return a.content != nil && (a.content.phase == phaseProbing || a.content.phase == phasePackaging)
}
