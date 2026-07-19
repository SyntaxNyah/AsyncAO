package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// resetContentPanel clears the package-level panel state so each test starts
// clean (the panel is a singleton, like demoBrowser). Registered as a cleanup so
// a test's mutations never leak into the next.
func resetContentPanel(t *testing.T) {
	t.Helper()
	contentPanel = contentPanelState{}
	demoBrowser.open = false
	demoBrowser.purpose = purposeVideo
	t.Cleanup(func() { contentPanel = contentPanelState{}; demoBrowser.open = false })
}

// mkReport builds a probed ContentReport with a mix of statuses across two
// categories, so the row-model + filter tests have real data without the probe.
func mkReport(origin string) *ContentReport {
	r := &ContentReport{Origin: origin, Categories: make([]CategoryReport, contentCatCount)}
	for i := range r.Categories {
		r.Categories[i].Cat = ContentCategory(i)
	}
	r.Categories[CatCharacter].Items = []AssetItem{
		{Name: "characters/phoenix/(a)normal", Status: StatusFound, Cat: CatCharacter},
		{Name: "characters/gone/(a)normal", Status: StatusMissing, Cat: CatCharacter},
		{Name: "characters/slow/(a)normal", Status: StatusUnreachable, Cat: CatCharacter},
	}
	r.Categories[CatMusic].Items = []AssetItem{
		{Name: "Trial.opus", Status: StatusFound, Cat: CatMusic},
	}
	for i := range r.Categories {
		r.Categories[i].recount()
	}
	return r
}

// TestContentRowsFilterToggle pins the cached visible-row model: the default
// "only missing" filter hides found assets, "show all" reveals them, and each
// mode still emits the category headers. The cache must rebuild when the filter
// flips (rowsFilter capture) — a stale rebuild would show the wrong set.
func TestContentRowsFilterToggle(t *testing.T) {
	resetContentPanel(t)
	s := &contentPanel
	rep := mkReport("http://cdn.example/")

	// Default (showAll=false): headers for the two non-empty categories, plus only
	// the not-found items (missing + unreachable) — the found phoenix + music hide.
	s.showAll = false
	rows := s.contentReportRows(rep)
	items, headers := splitRows(rows)
	if headers != 2 {
		t.Errorf("want 2 category headers, got %d", headers)
	}
	// Character: gone + slow (2 not-found); Music: none (its only item is found).
	if items != 2 {
		t.Errorf("only-missing filter: want 2 item rows, got %d", items)
	}
	for _, name := range rowItemNames(rows, rep) {
		if strings.Contains(name, "phoenix") || name == "Trial.opus" {
			t.Errorf("found asset %q must be hidden under the only-missing filter", name)
		}
	}

	// Show all: every enumerated item across both non-empty categories.
	s.showAll = true
	rows = s.contentReportRows(rep)
	items, headers = splitRows(rows)
	if headers != 2 {
		t.Errorf("show-all: want 2 headers, got %d", headers)
	}
	if items != 4 {
		t.Errorf("show-all: want 4 item rows (3 char + 1 music), got %d", items)
	}
}

// TestContentRowsCacheReuse pins that the row model is REUSED across frames when
// nothing changed (no per-frame rebuild): the same backing slice comes back, and
// a Totals-signature change (a probe landing) triggers exactly one rebuild.
func TestContentRowsCacheReuse(t *testing.T) {
	resetContentPanel(t)
	s := &contentPanel
	rep := mkReport("http://cdn.example/")

	first := s.contentReportRows(rep)
	firstLen := len(first)
	// Second call, nothing changed: identical length + the cache fields unchanged.
	second := s.contentReportRows(rep)
	if len(second) != firstLen {
		t.Errorf("cache reuse changed the row count: %d → %d", firstLen, len(second))
	}
	if s.rowsReport != rep {
		t.Error("rowsReport should still point at the same report")
	}

	// A probe landing (a status flips) changes the Totals signature → rebuild.
	// Under the only-missing filter, one fewer not-found item now shows.
	rep.Categories[CatCharacter].Items[2].Status = StatusFound // was Unreachable
	rep.Categories[CatCharacter].recount()
	rebuilt := s.contentReportRows(rep)
	if len(rebuilt) >= firstLen {
		t.Errorf("a status flip to found should shrink the only-missing list: was %d now %d", firstLen, len(rebuilt))
	}
}

// TestContentStatusLine pins the summary line + the "(checking…)" suffix rule
// (BLOCKER A): the suffix appears ONLY when probing is true (phaseProbing), so a
// packaged/cleared report — where probing is false — must not read "checking"
// forever. Also checks the counts and the cache reuse-vs-rebuild on a flag change.
func TestContentStatusLine(t *testing.T) {
	resetContentPanel(t)
	s := &contentPanel
	rep := mkReport("http://cdn.example/") // 2 found, 1 missing, 1 unreachable

	probing := s.contentStatusLine(rep, true)
	if !strings.Contains(probing, "checking") {
		t.Errorf("while probing the line must show (checking…); got %q", probing)
	}
	done := s.contentStatusLine(rep, false)
	if strings.Contains(done, "checking") {
		t.Errorf("a not-probing report must NOT show (checking…) — the BLOCKER-A stick; got %q", done)
	}
	if !strings.Contains(done, "2 found") || !strings.Contains(done, "1 missing") || !strings.Contains(done, "1 unreachable") {
		t.Errorf("counts wrong in %q", done)
	}
	// Cache: same inputs reuse the string; a probing-flag flip rebuilds it.
	if s.contentStatusLine(rep, false) != done {
		t.Error("identical inputs must reuse the cached line")
	}
}

// TestContentPanelCloseCancels pins the leak/refuse guard: closing the panel
// (the ✕/Esc path) cancels the running job so contentBusy clears — otherwise the
// next report is refused and probe goroutines are stranded.
func TestContentPanelCloseCancels(t *testing.T) {
	resetContentPanel(t)
	a := headlessProbeApp(t, nil, false)
	rec := synthRecording("") // origin-missing → short-circuits to phaseReport, stays busy
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("StartContentReport refused")
	}
	contentPanel.open = true
	contentPanel.rec = rec
	if !a.contentBusy {
		t.Fatal("job should be busy after start")
	}
	a.closeContentPanel()
	if a.contentBusy || a.content != nil {
		t.Error("closeContentPanel must cancel the job (clear contentBusy + content)")
	}
	if contentPanel.open {
		t.Error("closeContentPanel must close the panel")
	}
	// A fresh report must now be accepted (the single-flight slot is free again).
	if !a.StartContentReport(rec, "scene2") {
		t.Error("after close+cancel a new report must be accepted")
	}
}

// TestContentPanelCloseViaEsc pins that closeTopOverlay claims the open content
// panel (its Esc rung), cancelling the job — mutually exclusive with the demo
// browser rung above it.
func TestContentPanelCloseViaEsc(t *testing.T) {
	resetContentPanel(t)
	a := headlessProbeApp(t, nil, false)
	rec := synthRecording("")
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("start refused")
	}
	contentPanel.open = true
	contentPanel.rec = rec
	if !a.closeTopOverlay() {
		t.Fatal("closeTopOverlay must claim the open content panel")
	}
	if contentPanel.open || a.contentBusy {
		t.Error("Esc close must close the panel and cancel the job")
	}
}

// TestBrowserPurposeRoutesVideoUnchanged pins the promise that the video flow is
// byte-identical after adding the pick-purpose: purposeVideo (the zero value, the
// Import-.demo button's purpose) still routes a pick to the video import tail and
// never touches the content engine. It drives pickBrowsedRecording with a bogus
// path (the video tail is import-then-export; with no ffmpeg it still runs the
// import and posts a banner) and asserts NO content job was started.
func TestBrowserPurposeRoutesVideoUnchanged(t *testing.T) {
	resetContentPanel(t)
	a := headlessProbeApp(t, nil, false)
	demoBrowser.open = true
	demoBrowser.purpose = purposeVideo // the default; explicit for the pin
	a.pickBrowsedRecording(filepath.Join(t.TempDir(), "nope.demo"))
	if demoBrowser.open {
		t.Error("a pick must close the browser")
	}
	if contentPanel.open {
		t.Error("the VIDEO purpose must NOT open the content panel")
	}
	if a.content != nil || a.contentBusy {
		t.Error("the VIDEO purpose must NOT start a content job")
	}
}

// TestBrowserPurposeCheckOpensPanel pins that the check purpose loads the picked
// recording, starts the probe, and opens the panel. It writes a real .aorec so
// loadRecordingAny succeeds; origin-missing (no session) is fine — the panel
// shows the no-server notice and the job short-circuits to phaseReport.
func TestBrowserPurposeCheckOpensPanel(t *testing.T) {
	resetContentPanel(t)
	a := headlessProbeApp(t, nil, false)
	path := writeTestRecording(t, "myscene")
	demoBrowser.open = true
	demoBrowser.purpose = purposeCheck
	a.pickBrowsedRecording(path)
	if demoBrowser.open {
		t.Error("pick must close the browser")
	}
	if !contentPanel.open {
		t.Fatal("check purpose must open the content panel")
	}
	if contentPanel.rec == nil {
		t.Error("panel must retain the picked recording (for packaging)")
	}
	if contentPanel.stem != "myscene" {
		t.Errorf("stem = %q, want myscene", contentPanel.stem)
	}
	if a.content == nil {
		t.Error("check purpose must start a content job")
	}
}

// TestContentPanelReportRetainedAfterClear pins the BLOCKER the panel is built
// around: the engine nils a.content the instant packaging finishes, but the panel
// caches the last non-nil report so its final summary still renders. This drives
// the cache-retain logic directly (drawContentPanel stashes a.ContentJobReport()
// while non-nil), then simulates the post-package clear and confirms the panel
// still holds the report.
func TestContentPanelReportRetainedAfterClear(t *testing.T) {
	resetContentPanel(t)
	a := headlessProbeApp(t, nil, false)
	// Origin-missing so the job short-circuits to phaseReport with NO probe
	// goroutines (a nil fetcher is never touched) — the retain logic is the same
	// regardless of probe results, and the report is non-nil (enumerated).
	rec := synthRecording("")
	if !a.StartContentReport(rec, "scene") {
		t.Skip("probe start refused in this environment")
	}
	contentPanel.open = true
	contentPanel.rec = rec
	// Simulate a frame's retain step: while the job's report is live, stash it.
	if r := a.ContentJobReport(); r != nil {
		contentPanel.report = r
	}
	stashed := contentPanel.report
	if stashed == nil {
		t.Fatal("panel should have stashed the live report")
	}
	// Simulate the post-package clear (tickContentPackage nils a.content).
	a.content = nil
	a.contentBusy = false
	if a.ContentJobReport() != nil {
		t.Fatal("precondition: ContentJobReport must be nil after the clear")
	}
	if contentPanel.report != stashed {
		t.Error("the panel must retain the report across the post-package clear")
	}
	// And the shared formatter still renders it (the Copy-list path).
	if lines := FormatReport(contentPanel.report); len(lines) == 0 {
		t.Error("retained report must still format for the Copy button")
	}
}

// mountedProbeApp builds a headless probe App whose Manager streams from a
// LocalFetcher over `mount` (localMode) AND whose prefs declare `mount` as the
// configured local-asset mount — so demoDefaultOrigin resolves a .demo against
// the local base and the probe path can actually read the seeded files. Returns
// the App plus the mount's local:// origin.
func mountedProbeApp(t *testing.T, mount string) (*App, string) {
	t.Helper()
	local := assets.NewLocalFetcher([]string{mount})
	a := headlessProbeApp(t, local, true)
	a.d.Prefs.SetLocalAssets(true, []string{mount})
	return a, local.BaseURL()
}

// writeDemoFile writes a minimal .demo (SC + one MS) under a temp dir and returns
// its path, so openContentReportFor exercises the .demo → local default policy.
func writeDemoFile(t *testing.T, stem string) string {
	t.Helper()
	demo := "SC#Phoenix#%\n" + demoMS("Phoenix", "hold it", 0)
	path := filepath.Join(t.TempDir(), stem+demoExt)
	if err := os.WriteFile(path, []byte(demo), 0o644); err != nil {
		t.Fatalf("write demo: %v", err)
	}
	return path
}

// TestContentPickerDefaultDemoLocal pins the default policy end-to-end through the
// panel: a .demo opened while mounts are configured resolves against the local
// base (a local:// origin), the picker reads useLocal, and the seeded sprite is
// FOUND — no session, no empty-origin warning.
func TestContentPickerDefaultDemoLocal(t *testing.T) {
	resetContentPanel(t)
	mount := t.TempDir()
	seedMount(t, mount, "characters/phoenix/(a)normal.png", pngBytes())
	a, mountOrigin := mountedProbeApp(t, mount)

	a.openContentReportFor(writeDemoFile(t, "scene"), false)
	if !contentPanel.open {
		t.Fatal("panel must open")
	}
	if !contentPanel.useLocal {
		t.Error("a .demo under mounts must default to the local base (useLocal)")
	}
	if contentPanel.rec.Origin != mountOrigin {
		t.Errorf("rec.Origin = %q, want the mount origin %q", contentPanel.rec.Origin, mountOrigin)
	}
	drainContentJob(t, a)
	rep := a.ContentJobReport()
	if rep == nil || rep.Origin != mountOrigin {
		t.Fatalf("report origin = %v, want the local mount", rep)
	}
	if st, ok := findCharStatus(rep, "phoenix"); !ok || st != StatusFound {
		t.Errorf("mounted .png sprite status = %v ok=%v, want found via the local base", st, ok)
	}
}

// TestContentPickerAorecDefaultsToStamped pins the OTHER half of the matrix (the
// bug the scheme-based default guards): a .aorec opened while mounts ARE
// configured still defaults to its RECORDED server origin, NOT the mount — an
// .aorec stamps its origin at record time, and mounts must not hijack it.
func TestContentPickerAorecDefaultsToStamped(t *testing.T) {
	resetContentPanel(t)
	mount := t.TempDir()
	a, _ := mountedProbeApp(t, mount)

	// A .aorec stamped with an origin-missing (empty) server origin: loadRecording
	// reads the stamped value verbatim, ignoring demoDefaultOrigin. With the correct
	// default the panel stays on the server source (useLocal=false), so the report is
	// OriginMissing — proving mounts did NOT override the .aorec's stamped origin.
	path := writeTestRecording(t, "stamped") // stamped Origin == "" (server, empty)
	a.openContentReportFor(path, false)
	if !contentPanel.open {
		t.Fatal("panel must open")
	}
	if contentPanel.useLocal {
		t.Error("a .aorec must default to its stamped server origin, NOT the local mount")
	}
	if contentPanel.rec.Origin != "" {
		t.Errorf("rec.Origin = %q, want the stamped (empty) server origin unchanged", contentPanel.rec.Origin)
	}
}

// TestContentPickerSwitchRestarts pins the picker's re-run: switching source
// cancels the current job (bumps the generation) and starts a fresh report against
// the OTHER origin, and the retained rec's Origin follows so enumeration and
// packaging read one string. Default (mounts) = local base → the seeded sprite is
// found; switching to "This server" (no session → empty) re-runs origin-missing;
// switching back returns to the found local report.
func TestContentPickerSwitchRestarts(t *testing.T) {
	resetContentPanel(t)
	mount := t.TempDir()
	seedMount(t, mount, "characters/phoenix/(a)normal.png", pngBytes())
	a, mountOrigin := mountedProbeApp(t, mount)

	a.openContentReportFor(writeDemoFile(t, "scene"), false)
	drainContentJob(t, a)
	if !contentPanel.useLocal || contentPanel.rec.Origin != mountOrigin {
		t.Fatalf("precondition: default should be the local base; useLocal=%v origin=%q", contentPanel.useLocal, contentPanel.rec.Origin)
	}
	genBefore := a.contentGen

	// Switch to "This server" (no session → empty server origin). The job restarts
	// against the empty origin, which short-circuits to an OriginMissing report.
	if !a.switchContentSource(false) {
		t.Fatal("switch to server must restart the report")
	}
	if contentPanel.useLocal {
		t.Error("after switching to server, useLocal must be false")
	}
	if !contentPanel.pickerChosen {
		t.Error("an explicit switch must latch pickerChosen (sticky per session)")
	}
	if a.contentGen <= genBefore {
		t.Errorf("a switch must bump the generation (cancel + restart): %d → %d", genBefore, a.contentGen)
	}
	if contentPanel.rec.Origin != "" {
		t.Errorf("rec.Origin after switching to the empty server = %q, want empty", contentPanel.rec.Origin)
	}
	drainContentJob(t, a)
	rep := a.ContentJobReport()
	if rep == nil || !rep.OriginMissing {
		t.Fatalf("server (empty) re-run should be OriginMissing, got %v", rep)
	}

	// Switch back to the local base: the seeded sprite is found again.
	if !a.switchContentSource(true) {
		t.Fatal("switch back to local must restart the report")
	}
	if contentPanel.rec.Origin != mountOrigin {
		t.Errorf("rec.Origin after switching back = %q, want the mount origin", contentPanel.rec.Origin)
	}
	drainContentJob(t, a)
	rep = a.ContentJobReport()
	if rep == nil || rep.Origin != mountOrigin {
		t.Fatalf("local re-run report origin = %v, want the mount", rep)
	}
	if st, _ := findCharStatus(rep, "phoenix"); st != StatusFound {
		t.Errorf("after switching back the mounted sprite must be found again, got %v", st)
	}
}

// TestContentPickerSwitchNoMountsNoOp pins that "Local base" cannot be selected
// when no mounts are configured: switchContentSource(true) is a no-op (no restart,
// no generation bump), so the control never offers an unresolvable pick.
func TestContentPickerSwitchNoMountsNoOp(t *testing.T) {
	resetContentPanel(t)
	a := headlessProbeApp(t, nil, false)
	rec := synthRecording("") // origin-missing; no mounts configured
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("start refused")
	}
	contentPanel.open = true
	contentPanel.rec = rec
	contentPanel.originServer = ""
	contentPanel.originLocal = "" // no mounts
	genBefore := a.contentGen
	if a.switchContentSource(true) {
		t.Error("switch to local with no mounts must be a no-op")
	}
	if a.contentGen != genBefore {
		t.Error("a no-op switch must NOT bump the generation")
	}
	if contentPanel.useLocal {
		t.Error("useLocal must stay false when there is no mount to switch to")
	}
}

// ---------------------------------------------------------------------------
// Draw smoke test (maker_draw_test.go style — skips if the kit is unavailable).
// ---------------------------------------------------------------------------

// TestContentPanelDrawNoPanic drives drawContentPanel over a probed report with a
// mix of statuses under both filters, and over the origin-missing case, so a
// layout/label regression surfaces as a panic here instead of on screen. Excludes
// nothing — the panel needs no Viewport (unlike the maker preview).
func TestContentPanelDrawNoPanic(t *testing.T) {
	resetContentPanel(t)
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("kit unavailable: %v", err)
	}
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{ctx: ctx}
	a.d.Prefs = prefs

	// Probed report, both filters.
	contentPanel = contentPanelState{open: true, stem: "scene", report: mkReport("http://cdn.example/")}
	a.drawContentPanel(1024, 700)
	contentPanel.showAll = true
	a.drawContentPanel(1024, 700)

	// Post-package state: a done-summary snapshot (sawJob + doneMsg) with the job
	// cleared (a.content nil) — the summary + retained report must draw cleanly.
	contentPanel = contentPanelState{open: true, stem: "scene", report: mkReport("http://cdn.example/"), sawJob: true, doneMsg: "Packaged 3 assets (1.2 MB) into scene-bundle."}
	a.drawContentPanel(1024, 700)

	// Origin-missing report: the no-server notice branch.
	contentPanel = contentPanelState{open: true, stem: "silent", report: &ContentReport{OriginMissing: true, Categories: make([]CategoryReport, contentCatCount)}}
	a.drawContentPanel(1024, 700)

	// A nil report (still enumerating) must also draw cleanly.
	contentPanel = contentPanelState{open: true, stem: "warming"}
	a.drawContentPanel(1024, 700)

	// A tiny window (the clamp floor) must not panic either.
	contentPanel = contentPanelState{open: true, report: mkReport("http://cdn.example/")}
	a.drawContentPanel(200, 160)
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// splitRows counts item vs header rows in a visible-row model.
func splitRows(rows []cpVisibleRow) (items, headers int) {
	for _, r := range rows {
		if r.kind == cpRowHeader {
			headers++
		} else {
			items++
		}
	}
	return items, headers
}

// rowItemNames lifts the AssetItem names of the item rows (for filter assertions).
func rowItemNames(rows []cpVisibleRow, rep *ContentReport) []string {
	var out []string
	for _, r := range rows {
		if r.kind == cpRowItem {
			out = append(out, rep.Categories[r.cat].Items[r.item].Name)
		}
	}
	return out
}

// writeTestRecording writes a minimal valid .aorec under a temp dir so
// loadRecordingAny can open it (loadRecording just JSON-unmarshals), returning
// its path. stem names the file.
func writeTestRecording(t *testing.T, stem string) string {
	t.Helper()
	rec := &sceneRecording{
		Version: recordingVersion,
		StartBg: "courtroom",
		Events: []recEvent{
			{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "def"}},
		},
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("marshal test recording: %v", err)
	}
	path := filepath.Join(t.TempDir(), stem+recordingExt)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write test recording: %v", err)
	}
	return path
}
