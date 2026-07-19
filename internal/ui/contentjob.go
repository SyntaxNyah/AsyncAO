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

	// contentSeedBuf is the 1-slot buffer of the seed goroutine's → render thread
	// result channel: the seed pass runs once (fetch <origin>/extensions.json,
	// seed the learned table) and sends exactly once, drained every frame. Same
	// leak-free contract as contentDoneBuf — a Cancel that nils the job leaves the
	// send in a buffer nobody polls, so the goroutine finishes and dies.
	contentSeedBuf = 1

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
	// Seed records how the pre-probe format-manifest seed went for this origin —
	// applied (extensions.json seeded N classes), absent (the host publishes
	// none, so unlearned types fall back to the full diagnostic probe chain), or
	// failed/skipped. Cosmetic only: probing reads the learned table directly, not
	// this value. Surfaced by FormatReport so the panel + bundle text can explain
	// why a host with no manifest still resolved (or didn't).
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

// seedStatusLine returns the one-line note on how asset formats were resolved,
// or "" when the note would be noise (manifest applied = the normal path;
// skipped = a local mount / auto-detect off, which the full-chain walk already
// covers silently). Only seedAbsent gets a line — it's the case the field report
// is about: a server with no extensions.json whose assets still resolved via the
// full diagnostic probe chain.
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
		out = append(out, line)
		// One honest word on how formats were resolved: an absent manifest means
		// unlearned types were probed with the FULL format chain, so a .png/.gif
		// server still resolved. Silent when the manifest applied (the normal case)
		// or wasn't attempted (skipped) — no noise for the common path.
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

// ---------------------------------------------------------------------------
// Job state machine.
// ---------------------------------------------------------------------------

// contentPhase is where a contentJob is in its lifecycle.
type contentPhase int

const (
	phaseSeeding   contentPhase = iota // one goroutine fetching <origin>/extensions.json → seeding learned formats
	phaseProbing                       // workers resolving refs; render thread polling results
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

	// Seed phase (one goroutine → render thread). seedItems + seedMgr are snapshot
	// on the render thread so startProbePhase can run either after the seed lands
	// or immediately (seeding skipped), from the same stored inputs.
	seedDone  chan seedResult
	seedItems []contentProbeItem
	seedMgr   *assets.Manager

	// Package phase (one goroutine → render thread).
	pkgDone chan contentPackageResult

	// Auto-bundle (H2): set when this job was started by stopRecording's
	// "recordings keep their assets" auto-build, NOT by a Studio panel. It changes
	// two behaviors: (1) tickContentJob's phaseReport case auto-packages autoRec
	// the instant the probe settles (the panel drives that transition for a manual
	// job, but an auto-build draws no panel — without this the job would wedge in
	// phaseReport with contentBusy stuck, blocking every future job); (2) the four
	// intermediate warnLine writes along the pipeline are suppressed so the single
	// completion toast in tickContentPackage is the only one the user sees — the
	// "Recording saved" toast from stopRecording must survive, not be stomped by a
	// "Checking assets…" line the same frame. autoRec carries the Events/StartBg
	// the bundled .aorec needs (the report is asset-only).
	auto    bool
	autoRec *sceneRecording
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

	for ci := range report.Categories {
		j.pending += report.Categories[ci].Total()
	}

	// Snapshot the work items HERE (render thread) so neither the seed goroutine,
	// the feeder, nor the workers ever touch the shared report: the render thread
	// alone reads/writes report.Categories[*].Items (applyProbeResult), and the
	// goroutines see only this immutable local slice. AssetRef.Alts is
	// enumeration-owned and never mutated after this, so sharing the ref value is
	// race-clean.
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

	// SEED FIRST when the origin is a network host and auto-detect is on: fetch
	// <origin>/extensions.json and seed this host's learned formats BEFORE probing,
	// exactly as the live session's connect path does — a .demo's recorded origin
	// is never the session origin, so without this its host is never seeded and the
	// probe walks the zero-fallback single format against a server whose sprites are
	// e.g. .gif/.png. A non-http origin (a local:// mount) or auto-detect-off skips
	// straight to probing; probeRef's full-chain fallback still finds the assets on
	// an unlearned host (the seed only makes the FIRST format correct — it doesn't
	// gate correctness). Dedupe per origin per session via the shared marker.
	if strings.HasPrefix(report.Origin, "http") && a.d.Prefs.FormatAutoDetect() && !a.originSeeded(report.Origin) {
		a.markOriginSeeded(report.Origin)
		a.startContentSeed(j)
		a.warnLine = "Checking the server's format manifest…"
		a.warnAt = time.Now()
		return true
	}
	a.startProbePhase(j)
	return true
}

// startAutoBundle begins the automatic "recordings keep their assets" build for
// a just-saved in-app recording: enumerate → seed → probe → (on probe settle)
// package, reusing the EXACT Studio content-job machinery — the only differences
// are the j.auto flag (which silences the pipeline's intermediate toasts and lets
// tickContentJob's phaseReport case fire the package headlessly) and j.autoRec
// (the recording's Events/StartBg for the bundled .aorec). Single-flight with the
// Studio jobs: if one is already running this quietly skips (a debug note only —
// never a toast, never a queue) so the auto-build can't stomp a manual report or
// pile up unboundedly. Empty origin (an offline recording) skips silently: there
// is nothing to package, and the flat .aorec already landed. Render thread only
// (reads a.d.Manager/Prefs, spawns the seed/probe goroutines) — called AFTER
// stopRecording's async .aorec write, so it never blocks or slows the stop.
//
// rec.Origin is the SESSION origin stamped at startRecording (a.urls.Origin() at
// record start) — the right host even if the session later disconnected before
// stop, so we read rec.Origin, not the live a.urls.
func (a *App) startAutoBundle(rec *sceneRecording, stem string) {
	if a.contentBusy {
		// A Studio report/package (or a prior auto-build) is mid-flight: skip quietly.
		// Queuing would violate the single-flight/no-unbounded rule and stomping a
		// user's manual report would be worse than silently declining the freebie.
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
	// Snapshot the probe work items on the render thread (same discipline as
	// StartContentReport): the goroutines see only this immutable local slice, never
	// the shared report the render thread owns.
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

	// Seed the recording's origin first when it's a network host with auto-detect
	// on and not already seeded this session — an in-app recording's origin IS the
	// live session origin, so it was usually seeded on connect and this skips
	// straight to probing (no re-fetch). Same gate as StartContentReport.
	if strings.HasPrefix(report.Origin, "http") && a.d.Prefs.FormatAutoDetect() && !a.originSeeded(report.Origin) {
		a.markOriginSeeded(report.Origin)
		a.startContentSeed(j)
		return
	}
	a.startProbePhase(j)
}

// startContentSeed spawns the ONE bounded seed goroutine for a content job: it
// fetches <origin>/extensions.json and seeds the learned table off the render
// thread, delivering the outcome on the 1-buffered seedDone channel the render
// thread drains each tick (tickContentSeed). Same leak-free contract as the
// package goroutine — a Cancel nils a.content, so the send lands in a buffer
// nobody polls and the goroutine dies. Render thread only (spawns the goroutine).
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

// startProbePhase spins up the bounded probe fan-out (a feeder + contentProbeWorkers
// workers) from the job's snapshotted seedItems, transitioning it into phaseProbing.
// Called either directly (seeding skipped) or from tickContentSeed once the seed
// lands and the resolver table is republished — so by the time a worker builds a
// candidate, a seeded host's learned format is already live. Render thread only.
func (a *App) startProbePhase(j *contentJob) {
	j.phase = phaseProbing
	// Bounded fan-out: a small feeder plus contentProbeWorkers workers. The
	// feeder blocks on the bounded workCh (backpressure = the in-flight window),
	// so outstanding probes never exceed the worker count. Results land on the
	// bounded resultCh the render thread drains each tick.
	j.workCh = make(chan contentProbeItem, contentWorkBuf)
	j.resultCh = make(chan contentProbeResult, contentResultBuf)
	j.stop = make(chan struct{})

	items := j.seedItems
	mgr := j.seedMgr
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

	if !j.auto { // an auto-build stays silent until its single completion toast
		a.warnLine = "Checking assets against the server…"
		a.warnAt = time.Now()
	}
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
// at. Exact refs (music) are a direct FetchRaw; bases probe via ResolveRawFull,
// walking the alt spellings (bare X / "(a)/X" for sprites, authored-case for
// blips). ResolveRawFull probes a learned host's format FIRST (one probe on a
// HIT — the seed pass ran first, so a manifest-declared host is single-probed
// here), and full-chain-walks both a host with NO learned entry AND a learned
// host whose format MISSED. That learned-miss walk is what keeps the report
// honest across a no-manifest host with MIXED per-asset formats: the workers run
// concurrently (contentProbeWorkers), so the format one char's sprite wins via
// RecordSuccess must NOT lock a sibling char of a different format out — the
// sibling's learned-miss re-walks the full chain and is found at its own format.
// A server whose sprites are .gif/.png but publishes no extensions.json is thus
// found whether its formats are uniform or mixed, which the zero-fallback live
// path deliberately won't do. It's still one network probe per candidate (404s
// cached by the 404-TTL layer + collapsed by singleflight through FetchRaw), and
// the winner is learned (RecordSuccess inside ResolveRawFull) so the export warm
// right after this — AND any later re-report/re-export of the same demo — resolve
// learned-first for the common single-format server. A found asset's URL is
// exactly the origin-relative path a bundle writes. A transport failure (dead
// host) surfaces as StatusUnreachable via the phase hard cap, distinct from a
// real all-formats-404 (StatusMissing).
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
	if url, _, ok := mgr.ResolveRawFull(ref.Base, ref.Type); ok {
		return url, StatusFound
	}
	for _, alt := range ref.Alts {
		if alt == "" {
			continue
		}
		if url, _, ok := mgr.ResolveRawFull(alt, ref.Type); ok {
			return url, StatusFound
		}
	}
	// ResolveRawFull returns ok=false both for a genuine all-format 404 and for a
	// transport failure (it swallows the error). We can't distinguish here, so a
	// base that didn't resolve is reported Missing — the common, correct case (the
	// asset isn't on the server). A wholly dead origin surfaces instead via the
	// phase hard cap, which marks the not-yet-probed tail Unreachable.
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
	case phaseSeeding:
		a.tickContentSeed(j)
	case phaseProbing:
		a.tickContentProbe(j)
	case phaseReport:
		// An auto-build (H2) has no panel to drive the report→package transition, so
		// fire it here the first tick the probe settles. A manual Studio job leaves
		// autoRec nil and stays idle in phaseReport until the panel packages/cancels.
		if j.auto && j.autoRec != nil {
			rec := j.autoRec
			j.autoRec = nil // one-shot: consume so a stalled package can't re-fire it
			if !a.PackageContentBundle(rec) {
				// PackageContentBundle refused (the only reachable case here is no
				// recordings dir — OriginMissing is pre-guarded and the phase is
				// phaseReport by construction). It set its own honest warnLine; clear the
				// job so contentBusy can't stick and wedge every future content job. The
				// flat .aorec already landed, so nothing is lost.
				a.pushDebug("recording auto-bundle: package could not start — " + a.warnLine)
				a.content = nil
				a.contentBusy = false
			}
		}
	case phasePackaging:
		a.tickContentPackage(j)
	}
}

// tickContentSeed polls the seed goroutine without blocking; on delivery it
// republishes the resolver table (so a probe worker built right after sees the
// seeded formats), notes the outcome on the report for the status line, and
// starts the probe phase. Nothing to do until the goroutine sends. Render thread
// only. A stale generation (a Cancel while seeding) never reaches here: Cancel
// nils a.content, so tickContentJob's j==nil guard fires and the buffered send is
// GC'd with the job.
func (a *App) tickContentSeed(j *contentJob) {
	select {
	case res := <-j.seedDone:
		// WarmFromPrefs on the RENDER thread (matches pollManifest discipline):
		// SeedLearned wrote prefs off-thread; this publishes them into the resolver's
		// atomic table before any candidate is built. The channel receive happens-
		// before this, so the table is fully seeded when probing starts.
		if res.status == seedApplied {
			a.d.Resolver.WarmFromPrefs()
		}
		j.report.Seed = res.status
		a.startProbePhase(j)
	default:
		// Still seeding — the overlay/warn line keeps its "Checking … manifest" note.
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
	if !j.auto { // an auto-build stays silent until its single completion toast
		a.warnLine = msg
		a.warnAt = time.Now()
	}
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

	if !j.auto { // an auto-build stays silent until its single completion toast
		a.warnLine = "Packaging the RP… writing found assets to a self-contained folder."
		a.warnAt = time.Now()
	}
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
		auto := j.auto
		a.content = nil
		a.contentBusy = false
		// The SINGLE toast for the whole auto-build (steps 3+4): on a clean success name
		// the bundle folder in the recordings-relative form the other Studio toasts use.
		// The size-cap abort returns dir != "" (a PARTIAL write) — don't claim a clean
		// "packaged with its assets" for it; fall through to the writer's honest
		// "Package stopped at the … cap" line instead. Any hard failure (dir == "")
		// surfaces the writer's own message too. A manual Studio job always keeps the
		// engine's verbose "Packaged N assets…" line as before.
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
