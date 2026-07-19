package ui

// contentjob.go — the data/engine half of two Studio features over an imported
// recording (a .demo/.aorec):
//
//   1. "Missing-content report": enumerate EVERYTHING the recording references
//      (character sprites, backgrounds + desks, music, SFX, chat blips), probe
//      what actually exists at the origin (or a local:// mount), and produce a
//      structured found/missing/unreachable report per category.
//   2. "Package this RP": fetch every FOUND asset's raw bytes and write a
//      self-contained bundle folder in the EXISTING archive format (byte-
//      compatible with archive.ExportAssets / beginBundledReplay) so the folder
//      replays offline forever. The report doubles as the package's gap list,
//      written into the bundle as a plain-text file recipients can read.
//
// Design (mirrors the wave-1 gif-export machinery, gifexport.go):
//   - ONE contentJob at a time (single-flight; a second request is refused
//     politely). It runs through phases enumerate → probe → [report ready] →
//     optionally package.
//   - Probing runs OFF the render thread: Manager.ResolveRaw / FetchRaw are
//     synchronous blocking calls (hard rule: no sync net/disk on the render
//     path), so a bounded, NAMED set of probe goroutines drains a bounded work
//     channel and reports each result on a bounded result channel the render
//     thread polls once per frame (tickContentJob). This keeps the wave-1
//     "bounded in-flight window + hard wall-clock ceiling" discipline: the
//     window is the worker count, and a dead origin still produces a report
//     because probing stops at contentProbeHardCap.
//   - One network probe per asset stays the law: ResolveRaw/FetchRaw go through
//     T2 → T3 → source, and the 404-TTL + singleflight layers below dedupe, so
//     nothing here re-probes.
//   - Packaging runs in ONE goroutine (bounded 1-buffered delivery, generation-
//     checked): it drains the already-probed bytes and writes the folder; disk
//     writes happen in the goroutine, never on the render thread.
//   - Cancel is safe at every phase: the job is replaced by generation, buffered
//     channels always accept, and every spawned goroutine finishes and dies
//     (never leaks blocked on a send).
//
// Everything except the render-thread poll is pure / headless-testable.

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

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

// ---------------------------------------------------------------------------
// Named caps (hard rule §17.4 — every goroutine/channel/queue has a named cap).
// ---------------------------------------------------------------------------

const (
	// contentProbeWorkers is the number of probe goroutines AND therefore the
	// OUTSTANDING probe window: work is handed out one item per idle worker, so
	// at most this many ResolveRaw/FetchRaw calls are ever in flight. It mirrors
	// wave-1's warmInFlightWindow discipline (a bounded in-flight set that a
	// Submit can never overflow) — here the bound is structural: a worker can't
	// pull new work until it has delivered its current result. Small so a probe
	// storm can't saturate the shared network pool the live session also uses.
	contentProbeWorkers = 8

	// contentWorkBuf buffers the enumerate→worker channel. Sized to the whole
	// enumerated set is impossible (unbounded), so it is a fixed window: the
	// feeder blocks until a worker takes an item, which is exactly the
	// backpressure that bounds outstanding work. A small buffer keeps every
	// worker busy without letting the queue grow without bound.
	contentWorkBuf = contentProbeWorkers

	// contentResultBuf buffers the worker→render-thread result channel. It holds
	// at most one result per worker plus a little slack, so a worker never blocks
	// on delivery for more than the time between two render-thread polls (one
	// frame). The render thread drains it every tick.
	contentResultBuf = contentProbeWorkers * 2

	// contentDoneBuf is the 1-slot buffer of the package goroutine's → render
	// thread result channel: one package run sends exactly once and the render
	// thread drains it every frame, so the goroutine blocks ≤1 frame and then
	// dies (a Cancel that drops the job leaves the send landing in a buffer
	// nobody polls — never a leak).
	contentDoneBuf = 1

	// contentProbeHardCap is the ABSOLUTE wall-clock ceiling on the whole probe
	// phase, measured from job creation. It is the backstop a dead/slow origin
	// needs: without it a scene whose every asset times out (a missing-origin
	// imported .demo, exactly the case the report exists to diagnose) would keep
	// the phase alive for workers × per-probe-timeout. When it fires the phase
	// ends and whatever probed so far becomes the report, with the not-yet-probed
	// assets marked unreachable ("N unreachable"). Generous over a warm-cache run
	// (which finishes in well under a second) so a large, still-progressing probe
	// isn't guillotined early.
	contentProbeHardCap = 45 * time.Second

	// contentPackageMaxBytes is the running total-bytes ceiling for one package
	// (hard rule §17.4): a pathological scene (thousands of large sprites) can't
	// fill the disk without bound. 2 GiB is far larger than any real RP bundle
	// (the biggest real archives are tens of MB) yet finite; crossing it aborts
	// the package honestly rather than writing forever.
	contentPackageMaxBytes = 2 << 30

	// contentReportFileName is the plain-text gap list written INTO the bundle so
	// a recipient sees exactly what was and wasn't packaged.
	contentReportFileName = "MISSING-CONTENT.txt"

	// contentBundleSuffix names the package folder: recordings\<stem>-bundle\.
	contentBundleSuffix = "-bundle"
)

// ---------------------------------------------------------------------------
// Report data model (pure — no SDL, no App).
// ---------------------------------------------------------------------------

// AssetStatus is the probe outcome for one enumerated asset.
type AssetStatus int

const (
	// StatusUnknown is the pre-probe state (also the honest state for an asset
	// the hard cap stopped us from probing — reported as "unreachable").
	StatusUnknown AssetStatus = iota
	// StatusFound: at least one candidate resolved to real bytes at the origin.
	StatusFound
	// StatusMissing: every candidate 404'd — the asset genuinely isn't there.
	StatusMissing
	// StatusUnreachable: the probe never completed (origin timed out / errored,
	// or the hard cap ended the phase first). Distinct from Missing: we don't
	// KNOW it's absent, only that we couldn't confirm it.
	StatusUnreachable
)

// String renders a status for the report/formatter.
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

// AssetItem is one enumerated asset: a friendly Name (the bare AO reference the
// recording carried, e.g. "Phoenix/normal" or "Objection.opus"), the resolved
// or candidate URL, its probe Status, and the enumeration ref used to fetch it.
// Cat groups it into a category for the report.
type AssetItem struct {
	Name   string
	URL    string // resolved URL once found; the primary candidate before probing
	Status AssetStatus
	Cat    ContentCategory
	ref    courtroom.AssetRef // how to resolve/fetch (base+alts+type, or exact)
}

// ContentCategory groups assets in the report. Ordered for a stable, readable
// report; the enumeration assigns one per ref from its AssetType.
type ContentCategory int

const (
	CatCharacter  ContentCategory = iota // character idle/talk/preanim sprites
	CatBackground                        // background + desk overlay art
	CatMusic                             // playable music tracks (exact URLs)
	CatSFX                               // emote/effect sound effects
	CatBlip                              // chat blip sound sets
	contentCatCount
)

// catNames labels each category in the report (index order matches the enum).
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

// categoryForType maps an AssetType (carried on every enumerated ref) to its
// report category. SFX and Blip are their own categories; character sprites and
// backgrounds/desks fold their two AO sub-types into one visible group each.
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

// CategoryReport is the per-category rollup: counts plus the per-item detail.
type CategoryReport struct {
	Cat         ContentCategory
	Found       int
	Missing     int
	Unreachable int
	Items       []AssetItem
}

// Total is the number of enumerated assets in this category.
func (cr *CategoryReport) Total() int { return len(cr.Items) }

// ContentReport is the finished (or in-progress) report over a recording.
type ContentReport struct {
	// Origin is the asset host the recording streams from ("" = none recorded).
	Origin string
	// OriginMissing is true when Origin is empty: an imported .demo carries no
	// host, so NOTHING can be probed and the report says so up front rather than
	// listing 100% "missing" as if it had checked (the silent-demo case).
	OriginMissing bool
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

// recount refreshes a category's found/missing/unreachable counts from its
// items (called after a probe updates an item's status).
func (cr *CategoryReport) recount() {
	cr.Found, cr.Missing, cr.Unreachable = 0, 0, 0
	for _, it := range cr.Items {
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

// ---------------------------------------------------------------------------
// Enumeration (pure).
// ---------------------------------------------------------------------------

// enumerateContent walks a recording and returns every DISTINCT asset it
// references, grouped into a ContentReport (all items StatusUnknown until
// probed). It reuses courtroom.SceneAssets for the render spine (sprites +
// backgrounds/desks + music), then adds the SFX and chat-blip references
// SceneAssets deliberately omits (it bundles only the spine) — those ARE part
// of "everything a recording references", so the report enumerates them too.
//
// origin is the asset host resolved on the render thread (demoDefaultOrigin);
// "" is honest and flagged (OriginMissing) rather than probed as all-missing.
// Pure over its inputs so enumeration completeness unit-tests without SDL.
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

	// The render spine: sprites, backgrounds, desks, music — exactly what a
	// bundle needs to render, enumerated by the same SceneAssets the archive
	// exporter uses (symmetry: what we probe/package is what replay requests).
	for _, ref := range courtroom.SceneAssets(urls, startBg, events) {
		add(spriteFriendlyName(ref, origin), ref.Base, ref)
	}

	// SFX + blips: per-message audio SceneAssets omits. These are BASES needing
	// format probing (like sprites), not exact URLs — SFXName/Blipname carry no
	// extension. Mirrors courtroom.go's playSFX (SFXName != "", "0", "1") and the
	// blip base (urls.Blip(Blipname)).
	for _, ev := range events {
		if ev.Kind != courtroom.EventMessage || ev.Message == nil {
			continue
		}
		m := ev.Message
		if sfx := m.SFXName; sfx != "" && sfx != "0" && sfx != "1" {
			add(sfx, urls.SFX(sfx), courtroom.AssetRef{Base: urls.SFX(sfx), Type: assets.AssetTypeSFX})
		}
		if blip := m.Blipname; blip != "" {
			// The authored-casing spelling is the chain alt (blips split lowercase
			// vs raw-case on different mirrors — see URLBuilder.BlipAuthored).
			add(blip, urls.Blip(blip), courtroom.AssetRef{
				Base: urls.Blip(blip),
				Alts: []string{urls.BlipAuthored(blip)},
				Type: assets.AssetTypeBlip,
			})
		}
	}

	for i := range r.Categories {
		r.Categories[i].recount()
	}
	return r
}

// spriteFriendlyName derives a short human label for a spine ref: strip the
// origin + leading dir so "http://h/characters/phoenix/(a)normal" reads as
// "characters/phoenix/(a)normal". Music refs are exact URLs — show the tail.
func spriteFriendlyName(ref courtroom.AssetRef, origin string) string {
	name := ref.Base
	if origin != "" {
		if rel, ok := strings.CutPrefix(name, origin); ok {
			name = strings.TrimPrefix(rel, "/")
		}
	}
	return name
}

// ---------------------------------------------------------------------------
// Formatter (pure — shared by the UI and the bundle's text file).
// ---------------------------------------------------------------------------

// FormatReport renders a ContentReport to plain lines: a header (origin +
// totals, or the honest "no origin" line), then a per-category summary with
// each item's status. Shared by the Studio UI panel and the bundle's
// MISSING-CONTENT.txt so the two can never drift. Pure.
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
		out = append(out, line, "")
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

// ---------------------------------------------------------------------------
// Job state machine.
// ---------------------------------------------------------------------------

// contentPhase is where a contentJob is in its lifecycle.
type contentPhase int

const (
	phaseProbing   contentPhase = iota // workers resolving refs; render thread polling results
	phaseReport                        // probe done — report ready; may package next
	phasePackaging                     // package goroutine draining bytes → writing the folder
	phaseDone                          // package finished (or report-only end); result delivered
)

// contentProbeResult is one worker's finding for one enumerated asset, keyed by
// (category, item index) so the render thread updates the exact report slot. Only
// the URL + status travel back: the probe's bytes stay in the cache tiers (T2/T3)
// it just filled, and packaging re-drains them from there — so the report never
// holds every found asset in RAM (a big scene could be tens of MB), yet still
// costs exactly one network probe per asset (the redrain is a cache hit).
type contentProbeResult struct {
	cat    ContentCategory
	idx    int
	url    string
	status AssetStatus
}

// contentJob is the in-flight report/package state (allocated only while a job
// runs; nil otherwise, so it costs nothing on the live path). Single-flight:
// App holds at most one, guarded by a.contentBusy.
type contentJob struct {
	gen    int // generation: a Cancel bumps App.contentGen so late sends are ignored
	stem   string
	report *ContentReport

	phase contentPhase

	// Probe phase (off-thread workers → render thread).
	created  time.Time               // anchors contentProbeHardCap
	workCh   chan contentProbeItem   // enumerate → workers (bounded)
	resultCh chan contentProbeResult // workers → render thread (bounded)
	stop     chan struct{}           // closed to release the feeder + workers (phase end / cancel)
	stopped  bool                    // guards a double close of stop (idempotent teardown)
	pending  int                     // outstanding items not yet resulted (probe done at 0)
	// probeLive counts the job's OWN live goroutines (feeder + workers). It is
	// the structural leak-free observable: global runtime.NumGoroutine deltas
	// drown in HTTP keep-alive/retry goroutines under a loaded full-suite run
	// (the flake that hit the gate twice), while this counts exactly the nine
	// goroutines the cancel contract promises to reap.
	probeLive atomic.Int32

	// Package phase (one goroutine → render thread).
	pkgDone chan contentPackageResult
}

// releaseProbe closes the probe stop channel exactly once, unblocking the feeder
// and any workers wedged on a full resultCh so they exit. Idempotent (a Cancel
// after a hard-cap end, or vice versa, is safe). No-op for the origin-missing
// short-circuit, which never started the probe workers (stop stays nil).
func (j *contentJob) releaseProbe() {
	if j == nil || j.stopped || j.stop == nil {
		return
	}
	j.stopped = true
	close(j.stop)
}

// contentProbeItem is one unit of probe work handed to a worker: the ref to
// resolve + where to write the result back in the report.
type contentProbeItem struct {
	cat ContentCategory
	idx int
	ref courtroom.AssetRef
}

// contentPackageResult is the package goroutine's handoff to the render thread:
// a user-facing line plus the bundle folder path on success ("" on failure).
type contentPackageResult struct {
	msg string
	dir string // "" on failure
}

// ---------------------------------------------------------------------------
// Render-thread entry points (called by the UI agent's screens).
// ---------------------------------------------------------------------------

// StartContentReport begins the enumerate→probe pass over a loaded recording.
// Render thread only (reads a.d.Manager, spawns the probe goroutines). Single-
// flight: a second request while one runs is refused politely via warnLine,
// returning false so the caller can keep its button disabled. rec must be
// non-nil; the origin comes from rec.Origin (the caller filled it via
// demoDefaultOrigin on load), so this touches no other App fields.
//
// The report is enumerated synchronously here (pure + fast — no I/O), then the
// probe work is fed to a bounded set of goroutines. If the origin is empty the
// job short-circuits to phaseReport with everything StatusUnknown flagged
// OriginMissing (nothing to probe).
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
		// Nothing to probe: the report is already complete (every item stays
		// StatusUnknown, and OriginMissing tells the formatter to say so up front).
		j.phase = phaseReport
		a.content = j
		a.contentBusy = true
		a.warnLine = "No server recorded — nothing to check. See the report."
		a.warnAt = time.Now()
		return true
	}

	// Bounded fan-out: a small feeder plus contentProbeWorkers workers. The
	// feeder blocks on the bounded workCh (backpressure = the in-flight window),
	// so outstanding probes never exceed the worker count. Results land on the
	// bounded resultCh the render thread drains each tick.
	j.workCh = make(chan contentProbeItem, contentWorkBuf)
	j.resultCh = make(chan contentProbeResult, contentResultBuf)
	j.stop = make(chan struct{})
	for ci := range report.Categories {
		j.pending += report.Categories[ci].Total()
	}
	a.content = j
	a.contentBusy = true

	// Snapshot the work items HERE (render thread) so neither the feeder nor the
	// workers ever touch the shared report: the render thread alone reads/writes
	// report.Categories[*].Items (applyProbeResult), and the goroutines see only
	// this immutable local slice. AssetRef.Alts is enumeration-owned and never
	// mutated after this, so sharing the ref value is race-clean.
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

	mgr := a.d.Manager
	resultCh := j.resultCh
	workCh := j.workCh
	stop := j.stop
	// Feeder: pushes every work item, then closes workCh so idle workers exit. ONE
	// bounded goroutine; every blocking send also selects on `stop` (closed by
	// finishContentProbe / CancelContentJob), so once the render thread stops
	// draining resultCh — the hard-cap end, or a Cancel — the feeder and workers
	// unblock immediately and die instead of leaking wedged on a full channel.
	// Without that stop, a hard-cap end with > contentResultBuf un-drained items
	// would strand every worker on its send forever.
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

	a.warnLine = "Checking assets against the server…"
	a.warnAt = time.Now()
	return true
}

// contentProbeWorker resolves each work item exactly once and sends the finding.
// One of contentProbeWorkers bounded goroutines; it exits when workCh closes OR
// stop closes (the phase ended / was cancelled and the render thread no longer
// drains resultCh). The send selects on stop so a worker never wedges on a full
// resultCh after the render thread walks away — it dies instead of leaking. Off
// the render thread only.
func contentProbeWorker(mgr *assets.Manager, workCh <-chan contentProbeItem, resultCh chan<- contentProbeResult, stop <-chan struct{}) {
	for item := range workCh {
		url, status := probeRef(mgr, item.ref)
		select {
		case resultCh <- contentProbeResult{
			cat:    item.cat,
			idx:    item.idx,
			url:    url,
			status: status,
		}:
		case <-stop:
			return
		}
	}
}

// probeRef resolves one enumerated ref to a status + (on found) the URL it lives
// at. Exact refs (music) are a direct FetchRaw; bases probe the learned-first
// candidate list via ResolveRaw, walking the alt spellings (bare X / "(a)/X"
// for sprites, authored-case for blips) — the SAME resolution archive.ExportAssets
// uses, so a found asset's URL is exactly the origin-relative path a bundle
// writes. All of it goes through T2 → T3 → source with the 404-TTL + singleflight
// below, so this is one network probe per asset; the fetched bytes stay in T2/T3
// for packaging to re-drain (a cache hit, not a second probe). A transport
// failure (dead host) is StatusUnreachable, distinct from a real all-formats-404
// (StatusMissing).
func probeRef(mgr *assets.Manager, ref courtroom.AssetRef) (string, AssetStatus) {
	if mgr == nil {
		return "", StatusUnreachable
	}
	if ref.Exact {
		// Bounded, timeout-guarded fetch (a dead host must not stall a worker past
		// the phase hard cap). A clean not-found is Missing; any other error is a
		// transport fault → Unreachable (we never claim absence we didn't confirm).
		// Empty-but-no-error is Missing.
		ctx, cancel := context.WithTimeout(context.Background(), musicHTTPTimeout)
		defer cancel()
		data, err := mgr.FetchRaw(ctx, ref.Base)
		if err != nil {
			if assetLikelyMissing(err) {
				return ref.Base, StatusMissing
			}
			return ref.Base, StatusUnreachable
		}
		if len(data) == 0 {
			return ref.Base, StatusMissing
		}
		return ref.Base, StatusFound
	}
	if url, _, ok := mgr.ResolveRaw(ref.Base, ref.Type); ok {
		return url, StatusFound
	}
	for _, alt := range ref.Alts {
		if alt == "" {
			continue
		}
		if url, _, ok := mgr.ResolveRaw(alt, ref.Type); ok {
			return url, StatusFound
		}
	}
	// ResolveRaw returns ok=false both for a genuine all-404 and for a transport
	// failure (it swallows the error). We can't distinguish here, so a base that
	// didn't resolve is reported Missing — the common, correct case (the asset
	// isn't on the server). A wholly dead origin surfaces instead via the phase
	// hard cap, which marks the not-yet-probed tail Unreachable.
	return ref.Base, StatusMissing
}

// assetLikelyMissing reports whether a FetchRaw error is a clean 404 (asset
// absent) rather than a transport problem (host down). The network layer wraps a
// 404 as network.ErrAssetNotFound; anything else is a transport fault, which the
// caller reports Unreachable so we never claim absence we didn't confirm.
func assetLikelyMissing(err error) bool {
	return errors.Is(err, network.ErrAssetNotFound)
}

// tickContentJob advances a running content job on the render thread: drain
// probe results, apply the hard cap, and poll the package goroutine. No-op when
// no job runs (zero live-path cost). The UI agent calls this each frame while
// its Studio panel is up; it is also registered in App.Update's poll list.
func (a *App) tickContentJob() {
	j := a.content
	if j == nil {
		return
	}
	switch j.phase {
	case phaseProbing:
		a.tickContentProbe(j)
	case phasePackaging:
		a.tickContentPackage(j)
	}
}

// tickContentProbe drains any ready probe results into the report and ends the
// phase when every item has resulted OR the hard cap fires (a dead origin). On
// the hard cap the not-yet-resulted items are marked Unreachable so the report
// is honest ("N unreachable") instead of hanging. Render thread only.
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
		// The origin is (partly) unreachable: mark every un-resulted item and end
		// the phase so a report still lands. Workers keep draining workCh and die;
		// their late sends fall into resultCh, which nobody reads after this — the
		// buffered channel accepts them and they're GC'd with the job.
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

// applyProbeResult writes one worker finding into the report and decrements the
// outstanding count. Ignores an out-of-range slot defensively.
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
	if res.url != "" {
		it.URL = res.url // the resolved URL — packaging re-drains its bytes from T2/T3
	}
	c.recount()
	j.pending--
}

// finishContentProbe transitions a probe phase to phaseReport, releases the
// probe goroutines (so any still wedged on a full resultCh exit), and announces
// the result.
func (a *App) finishContentProbe(j *contentJob, msg string) {
	j.releaseProbe()
	j.phase = phaseReport
	a.warnLine = msg
	a.warnAt = time.Now()
}

// ContentReportReady reports whether a probed report is available to display
// (probe finished; not yet or not being packaged). For the UI's panel gate.
func (a *App) ContentReportReady() bool {
	return a.content != nil && a.content.phase == phaseReport
}

// ContentJobReport returns the current job's report (nil if none). Read-only for
// the UI panel + the formatter; the render thread owns it.
func (a *App) ContentJobReport() *ContentReport {
	if a.content == nil {
		return nil
	}
	return a.content.report
}

// CancelContentJob drops the running job at any phase, leak-free: it bumps the
// generation (so any late goroutine send is ignored at poll time), clears the
// job, and lets the buffered channels + closed workCh drain the workers to
// death. Idempotent. Render thread only.
func (a *App) CancelContentJob() {
	if a.content == nil {
		return
	}
	a.contentGen++           // invalidate the dropped job's generation
	a.content.releaseProbe() // unblock any probe goroutines so they die (no leak)
	a.content = nil
	a.contentBusy = false
}

// ---------------------------------------------------------------------------
// Packaging.
// ---------------------------------------------------------------------------

// PackageContentBundle begins writing a self-contained bundle from a report
// whose probe already ran (phaseReport). It reuses the EXISTING archive layout
// (recordings\<stem>-bundle\ with origin-relative asset paths + <stem>.aorec +
// the Formats manifest) so beginBundledReplay plays it byte-for-byte; the report
// is written into the bundle as MISSING-CONTENT.txt (the gap list). Single-flight
// (the same a.contentBusy job continues into packaging). rec supplies the events
// + StartBg the bundled .aorec needs. Returns false if no probed report is ready.
// Render thread only (spawns the goroutine; disk work happens inside it).
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
	// Never overwrite: <stem>-bundle, then -bundle-2, -bundle-3, … (nextBundleDir,
	// the directory analogue of nextRecordingDest's collision walk).
	destDir := nextBundleDir(dir, j.stem)

	// Snapshot everything the goroutine needs off App: the found items' resolved
	// URLs (their bytes stay in T2/T3 for the goroutine to re-drain), the recording
	// spine for the .aorec, and the report lines for the text file. The goroutine
	// must not touch App or the live report after this.
	urls := snapshotFoundURLs(j.report)
	reportLines := FormatReport(j.report)
	recCopy := *rec // shallow copy; Events slice shared read-only, never mutated
	mgr := a.d.Manager

	j.phase = phasePackaging
	j.pkgDone = make(chan contentPackageResult, contentDoneBuf)
	done := j.pkgDone
	stem := j.stem
	// ONE bounded goroutine. Generation isn't threaded through it: a Cancel nils
	// a.content, so tickContentPackage never reads pkgDone again, and this send
	// lands in the 1-slot buffer nobody polls — the goroutine finishes and dies
	// (no leak). Disk work (fetch-from-cache + write) happens HERE, never on the
	// render thread.
	go func() {
		done <- writeContentBundle(mgr, destDir, stem, &recCopy, urls, reportLines) // 1-buffered: always accepts
	}()

	a.warnLine = "Packaging the RP… writing found assets to a self-contained folder."
	a.warnAt = time.Now()
	return true
}

// snapshotFoundURLs copies out the FOUND items' resolved URLs so the package
// goroutine has no live-report dependency. Bytes are NOT copied — packaging
// re-drains each URL from the cache tiers the probe already filled.
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

// tickContentPackage polls the package goroutine without blocking; on delivery
// it announces the result and clears the job. A stale generation (a Cancel while
// packaging) is swallowed — the goroutine already wrote to a buffer nobody else
// reads. Render thread only.
func (a *App) tickContentPackage(j *contentJob) {
	select {
	case res := <-j.pkgDone:
		a.content = nil
		a.contentBusy = false
		a.warnLine = res.msg
		a.warnAt = time.Now()
	default:
	}
}

// writeContentBundle writes the self-contained folder OFF the render thread:
// every found asset at its origin-relative path (archive layout), a bundled
// .aorec, and the plain-text gap list. Byte-compatible with archive.ExportAssets
// / beginBundledReplay. A running total-bytes counter enforces
// contentPackageMaxBytes with an honest abort; per-file write errors are
// collected (not fatal) so one bad path doesn't sink the whole bundle.
func writeContentBundle(mgr *assets.Manager, destDir, stem string, rec *sceneRecording, foundURLs, reportLines []string) contentPackageResult {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return contentPackageResult{msg: "Package failed: " + err.Error()}
	}
	origin := rec.Origin
	var (
		written  int
		total    int64
		skipped  int
		writeErr int
		formats  = make(map[string]string)
	)
	for _, url := range foundURLs {
		rel, under := strings.CutPrefix(url, origin)
		if !under {
			skipped++ // external host (a direct http music link) — not part of THIS origin
			continue
		}
		rel = strings.TrimPrefix(rel, "/")
		// Re-drain the bytes from the cache tiers the probe already filled (T2/T3):
		// this is a cache hit, not a second network probe (one-probe-per-asset holds).
		// A cache/transport miss here (a rare T2+T3 eviction on a huge scene) is a
		// per-file error, not fatal — the rest of the bundle still writes.
		data, err := fetchBundleBytes(mgr, url)
		if err != nil {
			writeErr++
			continue
		}
		if total+int64(len(data)) > contentPackageMaxBytes {
			// Honest abort: stop writing rather than overrun the disk budget. What
			// wrote so far stays; the message names the cap.
			return contentPackageResult{
				dir: destDir,
				msg: fmt.Sprintf("Package stopped at the %d MiB size cap — wrote %d files (%.1f MB). The rest was skipped.",
					contentPackageMaxBytes>>20, written, float64(total)/(1024*1024)),
			}
		}
		if err := writeBundleFile(destDir, rel, data); err != nil {
			writeErr++
			continue
		}
		written++
		total += int64(len(data))
		// Learn the winning extension so a bundled replay seeds its resolver (the
		// archive Formats manifest). Keyed by AssetType.Name() from the ext; only
		// image assets carry a meaningful learned ext (audio ships its own).
		if ext := filepath.Ext(rel); ext != "" {
			if t, ok := assetTypeFromBundlePath(rel); ok {
				formats[t.Name()] = ext
			}
		}
	}

	// The bundled .aorec: mark Bundled, carry Formats, so replay plays from the
	// folder. Same shape runArchiveExport writes. This file is LOAD-BEARING — replay
	// keys entirely off it (beginBundledReplay reads Bundled+Formats from here), so a
	// bundle whose .aorec didn't write is non-replayable no matter how many assets
	// landed. A failure here is therefore a HARD failure (dir:"" per the field doc),
	// not a soft per-file writeErr like a stray asset: reporting "Packaged N assets…"
	// for a folder that can never replay would be a lie.
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

	// The gap list INTO the bundle so recipients see what's missing.
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
	// The bundle is byte-compatible with archive.ExportAssets' output on purpose:
	// beginBundledReplay / archive.SeedFormats read the SAME Bundled+Formats .aorec
	// and the SAME origin-relative asset layout, so this folder replays offline
	// exactly like a maker-exported archive. The write path is its own (not a call
	// into archive) because it must re-drain the ALREADY-probed bytes from the cache
	// tiers by their resolved URL (so no asset is format-probed twice), enforce the
	// running byte cap, and collect per-file errors — none of which the synchronous
	// exporter does.
	return contentPackageResult{dir: destDir, msg: msg}
}

// fetchBundleBytes re-drains one found asset's bytes for packaging. The URL is
// the resolved one the probe already fetched, so FetchRaw hits T2 (memory) or T3
// (disk) — the tiers the probe filled — and only re-touches the network if BOTH
// evicted (a huge scene), where it fetches the SAME known-good URL directly (no
// format probing, so the one-probe-per-asset law is untouched). Bounded by the
// music timeout so a package can't hang on a slow tier.
func fetchBundleBytes(mgr *assets.Manager, url string) ([]byte, error) {
	if mgr == nil {
		return nil, fmt.Errorf("contentjob: no asset manager")
	}
	ctx, cancel := context.WithTimeout(context.Background(), musicHTTPTimeout)
	defer cancel()
	return mgr.FetchRaw(ctx, url)
}

// writeBundleFile writes one bundled asset under destDir at its forward-slash
// relative path, refusing path escapes (mirrors archive.writeAsset).
func writeBundleFile(destDir, rel string, data []byte) error {
	if rel == "" || strings.Contains(rel, "..") {
		return fmt.Errorf("contentjob: refusing bad relative path %q", rel)
	}
	full := filepath.Join(destDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// assetTypeFromBundlePath infers the AssetType of a bundled file from its
// origin-relative directory (characters/ → CharSprite, background/ →
// Background), so the Formats manifest can seed a bundled replay's resolver.
// Audio dirs return false (audio carries its own extension; the resolver
// doesn't format-probe it). Best-effort — an unknown path just omits a hint.
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

// nextBundleDir returns a non-colliding bundle DIRECTORY under dir for
// <stem>-bundle: <stem>-bundle if free, else <stem>-bundle-2, -3, … Mirrors
// nextRecordingDest's collision walk but for a directory (never overwrite an
// existing bundle).
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
