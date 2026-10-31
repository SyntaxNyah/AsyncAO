package ui

// contentjob.go is the data/engine half of two Studio features over an imported recording:
// the missing-content report (enumerate every referenced asset, probe the origin or a local:// mount, report
// found/missing/unreachable per category) and "Package this RP" (write every found asset into a bundle folder in
// the archive format, with the report as MISSING-CONTENT.txt).
// One contentJob runs at a time (enumerate, probe, report ready, optionally package). Probing runs off the render
// thread because Manager.ResolveRaw / FetchRaw block; a bounded set of workers feeds a bounded result channel that
// tickContentJob polls once per frame, and contentProbeHardCap ends the phase for a dead origin.
// ResolveRaw/FetchRaw go through T2, T3, then source, so nothing here re-probes.
// Packaging runs in one generation-checked goroutine with a 1-buffered delivery and writes to disk off the render
// thread. Cancel is safe at every phase: the job is replaced by generation and every goroutine finishes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/archive"
	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// Named caps (hard rule 4).

const (
	// contentProbeWorkers is the probe goroutine count and so the in-flight window: a worker takes no new work until
	// it has delivered its result. Small so probing cannot saturate the network pool the live session shares.
	contentProbeWorkers = 8

	// contentWorkBuf buffers the enumerate to worker channel; the feeder blocks until a worker takes an item, which
	// bounds outstanding work.
	contentWorkBuf = contentProbeWorkers

	// contentResultBuf buffers the worker to render-thread results: one per worker plus slack, drained every tick.
	contentResultBuf = contentProbeWorkers * 2

	// contentDoneBuf is the package goroutine's result buffer; it sends once, so a Cancel that drops the job leaves
	// the send in a buffer nobody polls and the goroutine still ends.
	contentDoneBuf = 1

	// contentSeedBuf is the same for the seed goroutine, which fetches <origin>/extensions.json and seeds the
	// learned table.
	contentSeedBuf = 1

	// contentProbeHardCap is the absolute wall-clock ceiling on the probe phase, from job creation. It is the
	// backstop
	// for a dead origin (an imported .demo with no origin): when it fires, what probed so far becomes the report and
	// the rest is marked unreachable. Generous so a large, progressing probe is not cut off.
	contentProbeHardCap = 45 * time.Second

	// contentPackageMaxBytes is the running byte ceiling for one package (hard rule 4); crossing it aborts the
	// package.
	contentPackageMaxBytes = 2 << 30

	// contentReportFileName is the plain-text gap list written into the bundle.
	contentReportFileName = "MISSING-CONTENT.txt"

	// contentBundleSuffix names the package folder: recordings\<stem>-bundle\.
	contentBundleSuffix = "-bundle"
)

// Report data model.

// AssetStatus is the probe outcome for one enumerated asset.
type AssetStatus int

const (
	// StatusUnknown is the pre-probe state, also an asset the hard cap stopped us probing (reported as unreachable).
	StatusUnknown AssetStatus = iota
	// StatusFound: at least one candidate resolved to real bytes.
	StatusFound
	// StatusMissing: every candidate 404'd.
	StatusMissing
	// StatusUnreachable: the probe never completed (timeout, error or hard cap). Unlike Missing, absence is
	// unconfirmed.
	StatusUnreachable
)

// String renders a status for the report.
func (s AssetStatus) String() string {
	switch s {
	case StatusFound:
		return "found"
	case StatusMissing:
		return "missing"
	case StatusUnreachable:
		return "unreachable"
	default:
		return "?"
	}
}

// AssetItem is one enumerated asset: Name is the bare AO reference from the recording, URL the resolved or
// candidate URL, Status its probe outcome, Cat its report category.
type AssetItem struct {
	Name   string
	URL    string // resolved URL once found; the primary candidate before probing
	Status AssetStatus
	Cat    ContentCategory
	// FromPack marks an asset the user's own local folders answered rather than the server; a recipient will not
	// have it.
	FromPack bool
	ref      courtroom.AssetRef // how to resolve/fetch (base+alts+type, or exact)
}

// ContentCategory groups assets in the report, in report order; the enumeration assigns one per ref from its
// AssetType.
type ContentCategory int

const (
	CatCharacter  ContentCategory = iota // character idle/talk/preanim sprites
	CatBackground                        // background + desk overlay art
	CatMusic                             // playable music tracks (exact URLs)
	CatSFX                               // emote/effect sound effects
	CatBlip                              // chat blip sound sets
	contentCatCount
)

// catNames labels each category (index order matches the enum).
var catNames = [contentCatCount]string{
	CatCharacter:  "Character sprites",
	CatBackground: "Backgrounds & desks",
	CatMusic:      "Music",
	CatSFX:        "Sound effects",
	CatBlip:       "Chat blips",
}

// Name returns the category label.
func (c ContentCategory) Name() string {
	if c < 0 || c >= contentCatCount {
		return "Other"
	}
	return catNames[c]
}

// categoryForType maps an AssetType to its report category; sprites and backgrounds/desks each fold two sub-types.
func categoryForType(t assets.AssetType) ContentCategory {
	switch t {
	case assets.AssetTypeCharSprite:
		return CatCharacter
	case assets.AssetTypeBackground, assets.AssetTypeDeskOverlay:
		return CatBackground
	case assets.AssetTypeMusic:
		return CatMusic
	case assets.AssetTypeSFX:
		return CatSFX
	case assets.AssetTypeBlip:
		return CatBlip
	default:
		return CatCharacter
	}
}

// CategoryReport is the per-category rollup: counts plus per-item detail.
type CategoryReport struct {
	Cat         ContentCategory
	Found       int
	Missing     int
	Unreachable int
	// FromPack counts items the user's own folders answered; kept apart from Found because those are the assets a
	// recipient lacks.
	FromPack int
	Items    []AssetItem
}

// Total is the number of enumerated assets in this category.
func (cr *CategoryReport) Total() int { return len(cr.Items) }

// ContentReport is the finished or in-progress report over a recording.
type ContentReport struct {
	// Origin is the asset host the recording streams from ("" = none recorded).
	Origin string
	// OriginMissing is true when Origin is empty: an imported .demo carries no host, so nothing can be probed and the
	// report says so instead of listing everything missing.
	OriginMissing bool
	// Seed records how the pre-probe format-manifest seed went for this origin (applied, absent, failed or skipped).
	// Cosmetic only: probing reads the learned table. FormatReport surfaces it.
	Seed seedStatus
	// Categories is the per-category rollup, in ContentCategory order.
	Categories []CategoryReport
}

// Totals sums found/missing/unreachable across every category.
func (r *ContentReport) Totals() (found, missing, unreachable, total int) {
	for i := range r.Categories {
		c := &r.Categories[i]
		found += c.Found
		missing += c.Missing
		unreachable += c.Unreachable
		total += c.Total()
	}
	return found, missing, unreachable, total
}

// recount refreshes a category's counts from its items after a probe updates a status.
func (cr *CategoryReport) recount() {
	cr.Found, cr.Missing, cr.Unreachable, cr.FromPack = 0, 0, 0, 0
	for _, it := range cr.Items {
		if it.FromPack {
			cr.FromPack++
		}
		switch it.Status {
		case StatusFound:
			cr.Found++
		case StatusMissing:
			cr.Missing++
		case StatusUnreachable:
			cr.Unreachable++
		}
	}
}

// Enumeration.

// enumerateContent walks a recording and returns every distinct asset it references as a ContentReport (all
// StatusUnknown until probed). It uses courtroom.SceneAssets for sprites, backgrounds, desks and music, then adds
// the SFX and chat-blip references that SceneAssets omits. origin is resolved on the render thread
// (demoDefaultOrigin); "" is flagged OriginMissing rather than probed.
func enumerateContent(origin, startBg string, events []courtroom.Event) *ContentReport {
	r := &ContentReport{
		Origin:        origin,
		OriginMissing: strings.TrimSpace(origin) == "",
		Categories:    make([]CategoryReport, contentCatCount),
	}
	for i := range r.Categories {
		r.Categories[i].Cat = ContentCategory(i)
	}

	urls := courtroom.NewURLBuilder(origin)
	seen := make(map[string]struct{}) // dedupe by category+base across both walks

	add := func(name, primary string, ref courtroom.AssetRef) {
		if ref.Base == "" {
			return
		}
		cat := categoryForType(ref.Type)
		key := fmt.Sprintf("%d\x00%s", cat, ref.Base)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		r.Categories[cat].Items = append(r.Categories[cat].Items, AssetItem{
			Name: name,
			URL:  primary,
			Cat:  cat,
			ref:  ref,
		})
	}

	// The render spine, from the same SceneAssets the archive exporter uses, so what we probe is what replay
	// requests.
	for _, ref := range courtroom.SceneAssets(urls, startBg, events) {
		add(spriteFriendlyName(ref, origin), ref.Base, ref)
	}

	// SFX and blips are per-message audio SceneAssets omits. They are bases needing format probing, not exact URLs.
	// SFX mirrors courtroom.go's playSFX (SFXName != "", "0", "1"); the blip uses URLBuilder.BlipRef, the same mint
	// as the live path.
	for _, ev := range events {
		if ev.Kind != courtroom.EventMessage || ev.Message == nil {
			continue
		}
		m := ev.Message
		// "0"/"1" are legacy no-sound flags; "NO-SFX"/"-1" are modern sentinels. None is a real asset.
		if sfx := m.SFXName; sfx != "" && sfx != "0" && sfx != "1" && !strings.EqualFold(sfx, "NO-SFX") {
			add(sfx, urls.SFX(sfx), courtroom.AssetRef{Base: urls.SFX(sfx), Type: assets.AssetTypeSFX})
		}
		// BlipRef applies the sentinel and path-safety guard (a KFO "<id>^0" or bare "0" is not an asset) and the AO2
		// spelling ladder.
		if ref := urls.BlipRef(m.Blipname); ref.Base != "" {
			add(m.Blipname, ref.Base, ref)
		}
	}

	for i := range r.Categories {
		r.Categories[i].recount()
	}
	return r
}

// spriteFriendlyName strips the origin and leading slash so a ref reads as "characters/phoenix/(a)normal".
// Music refs are exact URLs, so the tail shows.
func spriteFriendlyName(ref courtroom.AssetRef, origin string) string {
	name := ref.Base
	if origin != "" {
		if rel, ok := strings.CutPrefix(name, origin); ok {
			name = strings.TrimPrefix(rel, "/")
		}
	}
	return name
}

// Formatter, shared by the UI and the bundle's text file.

// seedStatusLine returns the one-line note on how asset formats were resolved, or "" when it would be noise. Only
// seedAbsent gets a line: a server with no extensions.json whose assets still resolved via the full probe chain.
func seedStatusLine(s seedStatus) string {
	switch s {
	case seedAbsent:
		return "Note: this server publishes no format manifest (extensions.json) — every format was probed to find each asset."
	case seedFailed:
		return "Note: the server's format manifest couldn't be read — every format was probed to find each asset."
	default:
		return ""
	}
}

// FormatReport renders a ContentReport to plain lines (header, then per-category items). Shared by the Studio panel
// and the bundle's MISSING-CONTENT.txt so they cannot drift.
func FormatReport(r *ContentReport) []string {
	if r == nil {
		return nil
	}
	var out []string
	if r.OriginMissing {
		out = append(out,
			"No server/origin recorded for this file — nothing could be checked.",
			"Connect to the recording's server, or set Origin/CDN in the Scene Maker, then re-run.",
			"")
	} else {
		found, missing, unreachable, total := r.Totals()
		out = append(out, fmt.Sprintf("Origin: %s", r.Origin))
		line := fmt.Sprintf("Total referenced: %d — %d found, %d missing", total, found, missing)
		if unreachable > 0 {
			line += fmt.Sprintf(", %d unreachable", unreachable)
		}
		out = append(out, line)
		// An absent manifest means unlearned types were probed with the full format chain; say so. Silent when the
		// manifest applied or was skipped.
		if note := seedStatusLine(r.Seed); note != "" {
			out = append(out, note)
		}
		out = append(out, "")
	}
	for i := range r.Categories {
		c := &r.Categories[i]
		if c.Total() == 0 {
			continue // don't list an empty category
		}
		head := fmt.Sprintf("%s: %d/%d found", c.Cat.Name(), c.Found, c.Total())
		if c.Missing > 0 {
			head += fmt.Sprintf(", %d missing", c.Missing)
		}
		if c.Unreachable > 0 {
			head += fmt.Sprintf(", %d unreachable", c.Unreachable)
		}
		out = append(out, head)
		for _, it := range c.Items {
			out = append(out, fmt.Sprintf("  [%s] %s", it.Status.String(), it.Name))
		}
		out = append(out, "")
	}
	return out
}

// Job state machine.

// contentPhase is where a contentJob is in its lifecycle.
type contentPhase int

const (
	phaseSeeding   contentPhase = iota // one goroutine fetching <origin>/extensions.json → seeding learned formats
	phaseProbing                       // workers resolving refs; render thread polling results
	phaseReport                        // probe done — report ready; may package next
	phasePackaging                     // package goroutine draining bytes → writing the folder
	phaseDone                          // package finished (or report-only end); result delivered
)

// contentProbeResult is one worker's finding, keyed by (category, item index). Only the URL and status travel
// back; the bytes stay in T2/T3 and packaging re-drains them as a cache hit, so the report never holds every found
// asset in RAM.
type contentProbeResult struct {
	cat      ContentCategory
	idx      int
	url      string
	status   AssetStatus
	fromPack bool // the user's local folders answered, not the server
}

// contentJob is the in-flight report/package state, allocated only while a job runs. App holds at most one,
// guarded by a.contentBusy.
type contentJob struct {
	gen    int // generation: a Cancel bumps App.contentGen so late sends are ignored
	stem   string
	report *ContentReport

	phase contentPhase

	// Probe phase (off-thread workers to render thread).
	created  time.Time               // anchors contentProbeHardCap
	workCh   chan contentProbeItem   // enumerate → workers (bounded)
	resultCh chan contentProbeResult // workers → render thread (bounded)
	stop     chan struct{}           // closed to release the feeder + workers (phase end / cancel)
	stopped  bool                    // guards a double close of stop (idempotent teardown)
	pending  int                     // outstanding items not yet resulted (probe done at 0)
	// probeLive counts the job's own live goroutines (feeder + workers), the leak observable: runtime.NumGoroutine
	// deltas are polluted by HTTP keep-alive goroutines.
	probeLive atomic.Int32

	// Seed phase (one goroutine to render thread). seedItems and seedMgr are snapshotted on the render thread so
	// startProbePhase can run after the seed lands or immediately.
	seedDone  chan seedResult
	seedItems []contentProbeItem
	seedMgr   *assets.Manager

	// Package phase (one goroutine to render thread).
	pkgDone chan contentPackageResult
	// pkgCancel is closed by CancelContentJob so the writer stops early and deletes what it wrote.
	pkgCancel    chan struct{}
	pkgCancelled bool // render thread only, guards the single close

	// Auto-bundle: set when stopRecording's auto-build started this job, not a Studio panel. tickContentJob's
	// phaseReport case then packages autoRec as soon as the probe settles (no panel drives that transition, so the
	// job
	// would wedge with contentBusy stuck), and the intermediate warnLine writes are suppressed so the completion
	// toast
	// is the only one. autoRec carries the Events/StartBg the bundled .aorec needs.
	auto    bool
	autoRec *sceneRecording
}

// releaseProbe closes the probe stop channel once, releasing the feeder and workers blocked on a full resultCh.
// Idempotent; a no-op when probing never started (stop is nil).
func (j *contentJob) releaseProbe() {
	if j == nil || j.stopped || j.stop == nil {
		return
	}
	j.stopped = true
	close(j.stop)
}

// contentProbeItem is one unit of probe work: the ref to resolve and where to write the result in the report.
type contentProbeItem struct {
	cat ContentCategory
	idx int
	ref courtroom.AssetRef
}

// contentPackageResult is the package goroutine's handoff: a user-facing line and the bundle folder ("" on failure).
type contentPackageResult struct {
	msg string
	dir string // "" on failure
}

// Render-thread entry points.

// StartContentReport begins the enumerate and probe pass over a loaded recording. Render thread only. Single
// flight: a second request is refused via warnLine and returns false. rec.Origin was filled by demoDefaultOrigin.
// Enumeration is synchronous (pure, no I/O); an empty origin goes straight to phaseReport with OriginMissing.
func (a *App) StartContentReport(rec *sceneRecording, stem string) bool {
	if a.contentBusy {
		a.warnLine = "A content report / package is already running — let it finish first."
		a.warnAt = time.Now()
		return false
	}
	if rec == nil || len(rec.Events) == 0 {
		a.warnLine = "Nothing to check — the recording has no events."
		a.warnAt = time.Now()
		return false
	}
	evs := make([]courtroom.Event, 0, len(rec.Events))
	for _, re := range rec.Events {
		evs = append(evs, eventFromRec(re))
	}
	report := enumerateContent(rec.Origin, rec.StartBg, evs)

	a.contentGen++
	j := &contentJob{
		gen:     a.contentGen,
		stem:    sanitizeStem(stem),
		report:  report,
		created: time.Now(),
		phase:   phaseProbing,
	}

	if report.OriginMissing {
		// Nothing to probe: items stay StatusUnknown and OriginMissing makes the formatter say so.
		j.phase = phaseReport
		a.content = j
		a.contentBusy = true
		a.warnLine = "No server recorded — nothing to check. See the report."
		a.warnAt = time.Now()
		return true
	}

	for ci := range report.Categories {
		j.pending += report.Categories[ci].Total()
	}

	// Snapshot the work items here so the goroutines never touch the report; only the render thread reads and writes
	// report.Categories[*].Items (applyProbeResult). AssetRef.Alts is never mutated after enumeration.
	items := make([]contentProbeItem, 0, j.pending)
	for ci := range report.Categories {
		for ii := range report.Categories[ci].Items {
			items = append(items, contentProbeItem{
				cat: ContentCategory(ci),
				idx: ii,
				ref: report.Categories[ci].Items[ii].ref,
			})
		}
	}
	j.seedItems = items
	j.seedMgr = a.d.Manager
	a.content = j
	a.contentBusy = true

	// Seed first when the origin is a network host and auto-detect is on: fetch <origin>/extensions.json and seed
	// this
	// host's learned formats before probing, as the live connect path does. A .demo's recorded origin is never the
	// session origin, so otherwise the probe would try one format against a server that ships .gif/.png. Other
	// origins
	// skip to probing; probeRef's full-chain fallback still finds assets. Deduped per origin via the shared marker.
	if strings.HasPrefix(report.Origin, "http") && a.d.Prefs.Load().AutoDetectFormats && !a.originSeeded(report.Origin) {
		a.markOriginSeeded(report.Origin)
		a.startContentSeed(j)
		a.warnLine = "Checking the server's format manifest…"
		a.warnAt = time.Now()
		return true
	}
	a.startProbePhase(j)
	return true
}

// startAutoBundle begins the automatic "recordings keep their assets" build for a just-saved recording, through
// the same machinery as the Studio jobs; j.auto silences the intermediate toasts and lets tickContentJob package
// headlessly, and j.autoRec carries the Events/StartBg. If a job is already running it skips with a debug note
// only. An empty origin (offline recording) skips silently. Render thread only; called after stopRecording's async
// .aorec write. rec.Origin is the origin stamped at startRecording, correct even if the session later disconnected.
func (a *App) startAutoBundle(rec *sceneRecording, stem string) {
	if a.contentBusy {
		// A Studio job or earlier auto-build is mid-flight: skip rather than queue or stomp a manual report.
		a.pushDebug("recording auto-bundle: a content job is already running — skipped")
		return
	}
	if rec == nil || len(rec.Events) == 0 || strings.TrimSpace(rec.Origin) == "" {
		return // offline recording (no origin) or empty: nothing to package, land the flat .aorec only
	}

	evs := make([]courtroom.Event, 0, len(rec.Events))
	for _, re := range rec.Events {
		evs = append(evs, eventFromRec(re))
	}
	report := enumerateContent(rec.Origin, rec.StartBg, evs)
	if report.OriginMissing {
		return // defensive: TrimSpace above already guarded this — nothing to probe
	}

	a.contentGen++
	j := &contentJob{
		gen:     a.contentGen,
		stem:    sanitizeStem(stem),
		report:  report,
		created: time.Now(),
		phase:   phaseProbing,
		auto:    true,
		autoRec: rec,
	}
	for ci := range report.Categories {
		j.pending += report.Categories[ci].Total()
	}
	// Snapshot the probe items on the render thread, as in StartContentReport.
	items := make([]contentProbeItem, 0, j.pending)
	for ci := range report.Categories {
		for ii := range report.Categories[ci].Items {
			items = append(items, contentProbeItem{
				cat: ContentCategory(ci),
				idx: ii,
				ref: report.Categories[ci].Items[ii].ref,
			})
		}
	}
	j.seedItems = items
	j.seedMgr = a.d.Manager
	a.content = j
	a.contentBusy = true

	// Seed first for a network origin with auto-detect on that isn't already seeded; an in-app recording's origin is
	// usually the live session origin, seeded on connect. Same condition as StartContentReport.
	if strings.HasPrefix(report.Origin, "http") && a.d.Prefs.Load().AutoDetectFormats && !a.originSeeded(report.Origin) {
		a.markOriginSeeded(report.Origin)
		a.startContentSeed(j)
		return
	}
	a.startProbePhase(j)
}

// startContentSeed spawns the one seed goroutine: it fetches <origin>/extensions.json and seeds the learned table,
// delivering on the 1-buffered seedDone channel drained by tickContentSeed. A Cancel nils a.content so the send
// lands in an unpolled buffer and the goroutine ends. Render thread only.
func (a *App) startContentSeed(j *contentJob) {
	j.phase = phaseSeeding
	j.seedDone = make(chan seedResult, contentSeedBuf)
	done := j.seedDone
	mgr := j.seedMgr
	prefs := a.d.Prefs
	origin := j.report.Origin
	j.probeLive.Add(1)
	go func() {
		defer j.probeLive.Add(-1)
		done <- seedOriginFormats(mgr, prefs, origin) // 1-buffered: always accepts
	}()
}

// startProbePhase starts the feeder and contentProbeWorkers workers from the job's seedItems. Called directly or
// from tickContentSeed once the seed lands, so a seeded host's learned format is live before any candidate is built.
// Render thread only.
func (a *App) startProbePhase(j *contentJob) {
	j.phase = phaseProbing
	// The feeder blocks on the bounded workCh, which keeps in-flight probes at the worker count. Results land on the
	// bounded resultCh.
	j.workCh = make(chan contentProbeItem, contentWorkBuf)
	j.resultCh = make(chan contentProbeResult, contentResultBuf)
	j.stop = make(chan struct{})

	items := j.seedItems
	mgr := j.seedMgr
	resultCh := j.resultCh
	workCh := j.workCh
	stop := j.stop
	// Feeder: pushes every item, then closes workCh. Every blocking send also selects on stop (closed by
	// finishContentProbe / CancelContentJob); without it a hard-cap end with more than contentResultBuf undrained
	// results would strand every worker on its send.
	j.probeLive.Add(1)
	go func() {
		defer j.probeLive.Add(-1)
		for _, it := range items {
			select {
			case workCh <- it:
			case <-stop:
				close(workCh)
				return
			}
		}
		close(workCh)
	}()
	for w := 0; w < contentProbeWorkers; w++ {
		j.probeLive.Add(1)
		go func() {
			defer j.probeLive.Add(-1)
			contentProbeWorker(mgr, workCh, resultCh, stop)
		}()
	}

	if !j.auto { // an auto-build stays silent until its single completion toast
		a.warnLine = "Checking assets against the server…"
		a.warnAt = time.Now()
	}
}

// contentProbeWorker resolves each work item once and sends the finding. It exits when workCh or stop closes; the
// send selects on stop so it never wedges on a full resultCh. Off the render thread only.
func contentProbeWorker(mgr *assets.Manager, workCh <-chan contentProbeItem, resultCh chan<- contentProbeResult, stop <-chan struct{}) {
	for item := range workCh {
		url, status, fromPack := probeRef(mgr, item.ref)
		select {
		case resultCh <- contentProbeResult{
			cat:      item.cat,
			idx:      item.idx,
			url:      url,
			status:   status,
			fromPack: fromPack,
		}:
		case <-stop:
			return
		}
	}
}

// probeRef resolves one enumerated ref to a status and, if found, its URL. Exact refs (music) are a direct
// FetchRaw; bases probe via ResolveRawFull over the alt spellings (bare X / "(a)/X" for sprites, authored case for
// blips). ResolveRawFull tries a learned host's format first and full-chain-walks a host with no learned entry or
// whose learned format missed. That walk keeps a no-manifest host with mixed per-asset formats honest: workers run
// concurrently, so one sprite's RecordSuccess must not lock a sibling of another format out. The live path
// deliberately won't do this. Still one probe per candidate (404-TTL and singleflight dedupe), and the winner is
// learned for the export warm and later re-reports. A transport failure surfaces as StatusUnreachable via the hard
// cap, distinct from an all-formats 404 (StatusMissing).
func probeRef(mgr *assets.Manager, ref courtroom.AssetRef) (string, AssetStatus, bool) {
	if mgr == nil {
		return "", StatusUnreachable, false
	}
	// The user's own folders answer first, as while playing, so the report matches what this client renders.
	// PackCovers is a pure index lookup.
	fromPack := mgr.PackCovers(ref.Base)
	if ref.Exact {
		// A bounded fetch (a dead host must not stall a worker past the hard cap). A clean not-found is Missing; any
		// other error is a transport fault, Unreachable. Empty without error is Missing.
		ctx, cancel := context.WithTimeout(context.Background(), musicHTTPTimeout)
		defer cancel()
		data, err := mgr.FetchRaw(ctx, ref.Base)
		if err != nil {
			if fromPack {
				return ref.Base, StatusFound, true // your folders have it even though the server does not
			}
			if assetLikelyMissing(err) {
				return ref.Base, StatusMissing, false
			}
			return ref.Base, StatusUnreachable, false
		}
		if len(data) == 0 {
			if fromPack {
				return ref.Base, StatusFound, true
			}
			return ref.Base, StatusMissing, false
		}
		return ref.Base, StatusFound, fromPack
	}
	if url, _, ok := mgr.ResolveRawFull(ref.Base, ref.Type); ok {
		return url, StatusFound, fromPack
	}
	for _, alt := range ref.Alts {
		if alt == "" {
			continue
		}
		if url, _, ok := mgr.ResolveRawFull(alt, ref.Type); ok {
			return url, StatusFound, fromPack
		}
	}
	if fromPack {
		// The server has none of the spellings but the user's folders do: Found for this client, and FromPack stops
		// the
		// report implying a recipient would see it.
		return ref.Base, StatusFound, true
	}
	// ResolveRawFull returns ok=false for both an all-format 404 and a swallowed transport failure, so an unresolved
	// base is reported Missing. A wholly dead origin surfaces via the hard cap, which marks the unprobed tail
	// Unreachable.
	return ref.Base, StatusMissing, false
}

// assetLikelyMissing reports whether a FetchRaw error is a clean 404 (network.ErrAssetNotFound) rather than a
// transport problem, which is reported Unreachable.
func assetLikelyMissing(err error) bool {
	return errors.Is(err, network.ErrAssetNotFound)
}

// tickContentJob advances a running content job on the render thread: drain probe results, apply the hard cap,
// poll the package goroutine. No-op when no job runs.
func (a *App) tickContentJob() {
	j := a.content
	if j == nil {
		return
	}
	switch j.phase {
	case phaseSeeding:
		a.tickContentSeed(j)
	case phaseProbing:
		a.tickContentProbe(j)
	case phaseReport:
		// An auto-build has no panel to drive the report to package transition, so fire it on the first tick after
		// the
		// probe settles. A manual Studio job leaves autoRec nil and waits for the panel.
		if j.auto && j.autoRec != nil {
			rec := j.autoRec
			j.autoRec = nil // one-shot: consume so a stalled package can't re-fire it
			if !a.PackageContentBundle(rec) {
				// PackageContentBundle refused (here only when there is no recordings dir). It set its own warnLine;
				// clear the
				// job so contentBusy can't stick and block every later job. The flat .aorec already landed.
				a.pushDebug("recording auto-bundle: package could not start — " + a.warnLine)
				a.content = nil
				a.contentBusy = false
			}
		}
	case phasePackaging:
		a.tickContentPackage(j)
	}
}

// tickContentSeed polls the seed goroutine without blocking; on delivery it republishes the resolver table, notes
// the outcome on the report and starts the probe phase. A Cancel nils a.content, so a stale generation never
// reaches here. Render thread only.
func (a *App) tickContentSeed(j *contentJob) {
	select {
	case res := <-j.seedDone:
		// WarmFromPrefs on the render thread: SeedLearned wrote prefs off-thread, and this publishes them into the
		// resolver's atomic table before any candidate is built.
		if res.status == seedApplied {
			a.d.Resolver.WarmFromPrefs()
		}
		j.report.Seed = res.status
		a.startProbePhase(j)
	default:
		// Still seeding; the warn line keeps its "Checking … manifest" note.
	}
}

// tickContentProbe drains ready results and ends the phase when every item has resulted or the hard cap fires.
// On the hard cap the unresulted items are marked Unreachable. Render thread only.
func (a *App) tickContentProbe(j *contentJob) {
	for {
		select {
		case res := <-j.resultCh:
			a.applyProbeResult(j, res)
		default:
			goto drained
		}
	}
drained:
	if j.pending <= 0 {
		a.finishContentProbe(j, "Report ready.")
		return
	}
	if time.Since(j.created) > contentProbeHardCap {
		// The origin is partly unreachable: mark every unresulted item and end the phase. Late worker sends land in
		// the
		// buffered resultCh and are collected with the job.
		for ci := range j.report.Categories {
			c := &j.report.Categories[ci]
			for ii := range c.Items {
				if c.Items[ii].Status == StatusUnknown {
					c.Items[ii].Status = StatusUnreachable
				}
			}
			c.recount()
		}
		j.pending = 0
		a.finishContentProbe(j, "Report ready (the server was slow — some assets couldn't be checked).")
	}
}

// applyProbeResult writes one worker finding into the report and decrements the outstanding count.
func (a *App) applyProbeResult(j *contentJob, res contentProbeResult) {
	if res.cat < 0 || int(res.cat) >= len(j.report.Categories) {
		return
	}
	c := &j.report.Categories[res.cat]
	if res.idx < 0 || res.idx >= len(c.Items) {
		return
	}
	it := &c.Items[res.idx]
	if it.Status != StatusUnknown {
		return // already resulted (shouldn't happen — each item is fed once)
	}
	it.Status = res.status
	it.FromPack = res.fromPack
	if res.url != "" {
		it.URL = res.url // the resolved URL — packaging re-drains its bytes from T2/T3
	}
	c.recount()
	j.pending--
}

// finishContentProbe moves to phaseReport, releases the probe goroutines and announces the result.
func (a *App) finishContentProbe(j *contentJob, msg string) {
	j.releaseProbe()
	j.phase = phaseReport
	if !j.auto { // an auto-build stays silent until its single completion toast
		a.warnLine = msg
		a.warnAt = time.Now()
	}
}

// ContentReportReady reports whether a probed report is available to display (the panel's condition).
func (a *App) ContentReportReady() bool {
	return a.content != nil && a.content.phase == phaseReport
}

// ContentJobReport returns the current job's report (nil if none); the render thread owns it.
func (a *App) ContentJobReport() *ContentReport {
	if a.content == nil {
		return nil
	}
	return a.content.report
}

// CancelContentJob drops the running job at any phase: it bumps the generation so late sends are ignored, clears
// the job and lets the buffered channels and closed workCh drain the workers. Idempotent. Render thread only.
func (a *App) CancelContentJob() {
	if a.content == nil {
		return
	}
	a.contentGen++           // invalidate the dropped job's generation
	a.content.releaseProbe() // unblock any probe goroutines so they die (no leak)
	if c := a.content.pkgCancel; c != nil && !a.content.pkgCancelled {
		a.content.pkgCancelled = true
		close(c) // the package writer removes its partial folder and returns
	}
	a.content = nil
	a.contentBusy = false
}

// Packaging.

// PackageContentBundle writes a self-contained bundle from a probed report (phaseReport) in the existing archive
// layout (recordings\<stem>-bundle\ with origin-relative asset paths, <stem>.aorec and the Formats manifest), so
// beginBundledReplay plays it as is; the report goes in as MISSING-CONTENT.txt. The same a.contentBusy job
// continues into packaging. rec supplies the events and StartBg. Returns false if no probed report is ready.
// Render thread only; disk work happens in the goroutine.
func (a *App) PackageContentBundle(rec *sceneRecording) bool {
	j := a.content
	if j == nil || j.phase != phaseReport {
		a.warnLine = "Run the content report first, then package."
		a.warnAt = time.Now()
		return false
	}
	if j.report.OriginMissing {
		a.warnLine = "No server recorded — there are no assets to package."
		a.warnAt = time.Now()
		return false
	}
	dir := recordingsDir()
	if dir == "" {
		a.warnLine = "No recordings folder — can't write a bundle."
		a.warnAt = time.Now()
		return false
	}
	// Never overwrite: <stem>-bundle, then -bundle-2, -bundle-3, and so on (nextBundleDir).
	destDir := nextBundleDir(dir, j.stem)

	// Snapshot what the goroutine needs off App: the found items' resolved URLs (bytes stay in T2/T3), the recording
	// spine for the .aorec and the report lines. The goroutine must not touch App or the live report afterwards.
	urls := snapshotFoundURLs(j.report)
	reportLines := FormatReport(j.report)
	recCopy := *rec // shallow copy; Events slice shared read-only, never mutated
	mgr := a.d.Manager

	j.phase = phasePackaging
	j.pkgDone = make(chan contentPackageResult, contentDoneBuf)
	j.pkgCancel = make(chan struct{})
	done := j.pkgDone
	cancel := j.pkgCancel
	stem := j.stem
	// One goroutine; disk work happens here, never on the render thread. A Cancel closes cancel, so the writer stops
	// at a file boundary and removes the partial folder; its send still lands in the one-element buffer.
	go func() {
		done <- packageWriteBundle(mgr, destDir, stem, &recCopy, urls, reportLines, cancel) // 1-buffered: always accepts
	}()

	if !j.auto { // an auto-build stays silent until its single completion toast
		a.warnLine = "Packaging the RP… writing found assets to a self-contained folder."
		a.warnAt = time.Now()
	}
	return true
}

// snapshotFoundURLs copies out the found items' resolved URLs so the package goroutine has no live-report
// dependency. Bytes are re-drained from the cache tiers.
func snapshotFoundURLs(r *ContentReport) []string {
	var out []string
	for i := range r.Categories {
		for _, it := range r.Categories[i].Items {
			if it.Status == StatusFound && it.URL != "" {
				out = append(out, it.URL)
			}
		}
	}
	return out
}

// tickContentPackage polls the package goroutine without blocking; on delivery it announces the result and clears
// the job. A Cancel while packaging leaves the send in a buffer nobody reads. Render thread only.
func (a *App) tickContentPackage(j *contentJob) {
	select {
	case res := <-j.pkgDone:
		auto := j.auto
		a.content = nil
		a.contentBusy = false
		// The single toast for an auto-build: on a clean success name the bundle folder. The size-cap abort returns a
		// partial dir, so it keeps the writer's "stopped at the cap" line, as does any hard failure (dir == ""). A
		// manual
		// Studio job always keeps the engine's "Packaged N assets…" line.
		if auto {
			if res.dir != "" && !strings.Contains(res.msg, "size cap") {
				a.warnLine = "Recording packaged with its assets — recordings\\" + filepath.Base(res.dir)
			} else {
				a.warnLine = res.msg
			}
		} else {
			a.warnLine = res.msg
		}
		a.warnAt = time.Now()
	default:
	}
}

// writeContentBundle writes the self-contained folder off the render thread: every found asset at its
// origin-relative path, a bundled .aorec and the gap list, compatible with archive.ExportAssets /
// beginBundledReplay. contentPackageMaxBytes aborts honestly; per-file write errors are collected, not fatal.
func writeContentBundle(mgr *assets.Manager, destDir, stem string, rec *sceneRecording, foundURLs, reportLines []string) contentPackageResult {
	return writeContentBundleCancel(mgr, destDir, stem, rec, foundURLs, reportLines, nil)
}

// packageCancelled reports whether the cancel channel is closed; a nil channel never cancels.
func packageCancelled(cancel <-chan struct{}) bool {
	if cancel == nil {
		return false
	}
	select {
	case <-cancel:
		return true
	default:
		return false
	}
}

// removePartial deletes a folder the writer created, and only then: a pre-existing folder may hold the user's files.
func removePartial(destDir string, created bool) {
	if created {
		_ = os.RemoveAll(destDir)
	}
}

// packageWriteBundle is the writer the package goroutine runs; a test swaps it to observe cancellation.
var packageWriteBundle = writeContentBundleCancel

// writeContentBundleCancel is writeContentBundle that stops when cancel closes, deleting a folder it created.
func writeContentBundleCancel(mgr *assets.Manager, destDir, stem string, rec *sceneRecording, foundURLs, reportLines []string, cancel <-chan struct{}) contentPackageResult {
	if packageCancelled(cancel) {
		return contentPackageResult{msg: "Packaging cancelled."}
	}
	// Only a folder this writer created may be removed on cancel; a pre-existing one can hold the user's files.
	_, statErr := os.Stat(destDir)
	created := os.IsNotExist(statErr)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return contentPackageResult{msg: "Package failed: " + err.Error()}
	}
	dropPartial := func() { removePartial(destDir, created) }
	origin := rec.Origin
	var (
		written  int
		total    int64
		skipped  int
		writeErr int
		formats  = make(map[string]string)
	)
	for _, url := range foundURLs {
		if packageCancelled(cancel) {
			break
		}
		rel, under := strings.CutPrefix(url, origin)
		if !under {
			skipped++ // external host (a direct http music link) — not part of THIS origin
			continue
		}
		rel = strings.TrimPrefix(rel, "/")
		// Re-drain the bytes from T2/T3, which the probe filled: a cache hit, not a second probe. A miss (rare
		// eviction on a huge scene) is a per-file error, not fatal.
		data, err := fetchBundleBytes(mgr, url)
		if err != nil {
			writeErr++
			continue
		}
		if total+int64(len(data)) > contentPackageMaxBytes {
			// Stop writing rather than overrun the cap. What wrote so far stays.
			return contentPackageResult{
				dir: destDir,
				msg: fmt.Sprintf("Package stopped at the %d MiB size cap — wrote %d files (%.1f MB). The rest was skipped.",
					contentPackageMaxBytes>>20, written, float64(total)/(1024*1024)),
			}
		}
		if err := archive.WriteAsset(destDir, rel, data); err != nil {
			writeErr++
			continue
		}
		written++
		total += int64(len(data))
		// Learn the winning extension so a bundled replay seeds its resolver (the archive Formats manifest). Only
		// image
		// assets carry a meaningful learned extension.
		if ext := filepath.Ext(rel); ext != "" {
			if t, ok := assetTypeFromBundlePath(rel); ok {
				formats[t.Name()] = ext
			}
		}
	}

	// The bundled .aorec marks Bundled and carries Formats. Replay keys entirely off it (beginBundledReplay), so a
	// failed write is a hard failure (dir "") rather than a per-file writeErr: a folder that can never replay must
	// not
	// be reported as packaged.
	if packageCancelled(cancel) {
		dropPartial()
		return contentPackageResult{msg: "Packaging cancelled."}
	}
	out := *rec
	out.Bundled = true
	out.Formats = formats
	aorecErr := func() error {
		data, err := json.MarshalIndent(&out, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destDir, stem+recordingExt), data, 0o644)
	}()
	if aorecErr != nil {
		return contentPackageResult{msg: "Package failed: the replay descriptor (.aorec) couldn't be written — " + aorecErr.Error()}
	}

	// The gap list goes into the bundle so recipients see what's missing.
	reportText := strings.Join(reportLines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(destDir, contentReportFileName), []byte(reportText), 0o644); err != nil {
		writeErr++
	}

	msg := fmt.Sprintf("Packaged %d assets (%.1f MB) into %s.", written, float64(total)/(1024*1024), filepath.Base(destDir))
	if skipped > 0 {
		msg += fmt.Sprintf(" %d external links weren't bundled.", skipped)
	}
	if writeErr > 0 {
		msg += fmt.Sprintf(" %d files couldn't be written.", writeErr)
	}
	// The write path is its own, not a call into archive: it re-drains the already-probed bytes from the cache tiers
	// by resolved URL (no asset is format-probed twice), enforces the byte cap and collects per-file errors. The
	// output layout matches archive.ExportAssets, so the folder replays offline like a maker-exported archive.
	return contentPackageResult{dir: destDir, msg: msg}
}

// fetchBundleBytes re-drains one found asset's bytes. The URL is the resolved one the probe fetched, so FetchRaw
// hits T2 or T3 and touches the network only if both evicted, fetching the same URL with no format probing.
// Bounded by the music timeout.
func fetchBundleBytes(mgr *assets.Manager, url string) ([]byte, error) {
	if mgr == nil {
		return nil, fmt.Errorf("contentjob: no asset manager")
	}
	ctx, cancel := context.WithTimeout(context.Background(), musicHTTPTimeout)
	defer cancel()
	return mgr.FetchRaw(ctx, url)
}

// assetTypeFromBundlePath infers the AssetType from the origin-relative directory so the Formats manifest can seed
// a bundled replay's resolver. Audio dirs return false (audio carries its extension). Best-effort.
func assetTypeFromBundlePath(rel string) (assets.AssetType, bool) {
	switch {
	case strings.HasPrefix(rel, "characters/"):
		return assets.AssetTypeCharSprite, true
	case strings.HasPrefix(rel, "background/"):
		return assets.AssetTypeBackground, true
	default:
		return 0, false
	}
}

// nextBundleDir returns a non-colliding bundle directory: <stem>-bundle, else <stem>-bundle-2, -3, and so on.
func nextBundleDir(dir, stem string) string {
	base := stem + contentBundleSuffix
	cand := filepath.Join(dir, base)
	for n := 2; ; n++ {
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
		cand = filepath.Join(dir, fmt.Sprintf("%s-%d", base, n))
	}
}
