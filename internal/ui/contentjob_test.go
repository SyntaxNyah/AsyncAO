package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// ---------------------------------------------------------------------------
// Test fixtures.
// ---------------------------------------------------------------------------

// msgEvent builds an EventMessage recEvent referencing a character/emote (+
// optional preanim / sfx / blip), so enumeration has something to walk.
func msgEvent(char, emote, pre, sfx, blip string) recEvent {
	return recEvent{
		Kind: int(courtroom.EventMessage),
		Message: &protocol.ChatMessage{
			CharName: char,
			Emote:    emote,
			PreEmote: pre,
			SFXName:  sfx,
			Blipname: blip,
			Side:     "def",
		},
	}
}

// musicEvent builds an EventMusic recEvent.
func musicEvent(track string) recEvent {
	return recEvent{Kind: int(courtroom.EventMusic), Text: track}
}

// synthRecording is the synthetic scene the enumeration/package tests use:
// two characters, a background, a music track, an SFX, and a blip set.
func synthRecording(origin string) *sceneRecording {
	return &sceneRecording{
		Version: recordingVersion,
		Origin:  origin,
		StartBg: "courtroom",
		Events: []recEvent{
			msgEvent("Phoenix", "normal", "", "shock", "male"),
			msgEvent("Edgeworth", "confident", "confront", "", "female"),
			musicEvent("Trial.opus"),
		},
	}
}

func recEvents(rec *sceneRecording) []courtroom.Event {
	evs := make([]courtroom.Event, 0, len(rec.Events))
	for _, re := range rec.Events {
		evs = append(evs, eventFromRec(re))
	}
	return evs
}

// ---------------------------------------------------------------------------
// Enumeration.
// ---------------------------------------------------------------------------

// TestEnumerateContentCompleteness pins that enumeration walks EVERY category a
// recording references — character sprites (idle+talk+preanim), backgrounds +
// desks, music, sfx, and blips — not just the render spine SceneAssets bundles.
func TestEnumerateContentCompleteness(t *testing.T) {
	const origin = "http://cdn.example/"
	rec := synthRecording(origin)
	r := enumerateContent(rec.Origin, rec.StartBg, recEvents(rec))

	if r.OriginMissing {
		t.Fatal("origin was set — OriginMissing must be false")
	}

	// Each category must have at least the references we planted.
	byCat := map[ContentCategory]int{}
	for i := range r.Categories {
		byCat[r.Categories[i].Cat] = r.Categories[i].Total()
	}
	if byCat[CatCharacter] == 0 {
		t.Error("no character sprites enumerated")
	}
	if byCat[CatBackground] == 0 {
		t.Error("no backgrounds/desks enumerated")
	}
	if byCat[CatMusic] == 0 {
		t.Error("no music enumerated")
	}
	if byCat[CatSFX] == 0 {
		t.Error("no SFX enumerated (SceneAssets omits these — enumeration must add them)")
	}
	if byCat[CatBlip] == 0 {
		t.Error("no blips enumerated (SceneAssets omits these — enumeration must add them)")
	}

	// The SFX and blip bases must be the courtroom URL-convention URLs.
	urls := courtroom.NewURLBuilder(origin)
	wantSFX := urls.SFX("shock")
	wantBlip := urls.Blip("male")
	if !hasItemURL(r, CatSFX, wantSFX) {
		t.Errorf("SFX %q not enumerated; got %v", wantSFX, itemURLs(r, CatSFX))
	}
	if !hasItemURL(r, CatBlip, wantBlip) {
		t.Errorf("blip %q not enumerated; got %v", wantBlip, itemURLs(r, CatBlip))
	}

	// Music is an EXACT ref (carries its own extension); it must be marked so.
	for _, it := range r.Categories[CatMusic].Items {
		if !it.ref.Exact {
			t.Errorf("music ref %q should be Exact", it.Name)
		}
	}
}

// TestEnumerateContentDedupes pins that a repeated (char, emote) or repeated blip
// is enumerated once per category, not once per message (small bundles).
func TestEnumerateContentDedupes(t *testing.T) {
	const origin = "http://cdn.example/"
	rec := &sceneRecording{
		Origin:  origin,
		StartBg: "courtroom",
		Events: []recEvent{
			msgEvent("Phoenix", "normal", "", "shock", "male"),
			msgEvent("Phoenix", "normal", "", "shock", "male"), // identical line
		},
	}
	r := enumerateContent(rec.Origin, rec.StartBg, recEvents(rec))
	// Exactly one SFX and one blip despite two identical messages.
	if n := r.Categories[CatSFX].Total(); n != 1 {
		t.Errorf("SFX enumerated %d times, want 1 (dedup by base)", n)
	}
	if n := r.Categories[CatBlip].Total(); n != 1 {
		t.Errorf("blip enumerated %d times, want 1 (dedup by base)", n)
	}
}

// TestEnumerateContentEmptyOrigin pins the silent-demo honesty: an empty origin
// flags OriginMissing so the report says so up front instead of listing
// everything as if it had probed and found it all missing.
func TestEnumerateContentEmptyOrigin(t *testing.T) {
	rec := synthRecording("")
	r := enumerateContent(rec.Origin, rec.StartBg, recEvents(rec))
	if !r.OriginMissing {
		t.Fatal("empty origin must set OriginMissing")
	}
	lines := FormatReport(r)
	if len(lines) == 0 || !strings.Contains(lines[0], "No server") {
		t.Errorf("empty-origin report must lead with the no-server line; got %q", lines)
	}
}

// ---------------------------------------------------------------------------
// Formatter.
// ---------------------------------------------------------------------------

// TestFormatReportShape pins the formatter's headline + per-item lines so the UI
// panel and the bundle's text file share one stable rendering.
func TestFormatReportShape(t *testing.T) {
	r := &ContentReport{
		Origin:     "http://cdn.example/",
		Categories: make([]CategoryReport, contentCatCount),
	}
	for i := range r.Categories {
		r.Categories[i].Cat = ContentCategory(i)
	}
	r.Categories[CatCharacter].Items = []AssetItem{
		{Name: "characters/phoenix/(a)normal", Status: StatusFound, Cat: CatCharacter},
		{Name: "characters/gone/(a)normal", Status: StatusMissing, Cat: CatCharacter},
	}
	r.Categories[CatMusic].Items = []AssetItem{
		{Name: "Trial.opus", Status: StatusUnreachable, Cat: CatMusic},
	}
	for i := range r.Categories {
		r.Categories[i].recount()
	}

	lines := FormatReport(r)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "1 found, 1 missing") {
		t.Errorf("character summary missing found/missing counts:\n%s", joined)
	}
	if !strings.Contains(joined, "1 unreachable") {
		t.Errorf("unreachable count must surface:\n%s", joined)
	}
	if !strings.Contains(joined, "[found] characters/phoenix/(a)normal") {
		t.Errorf("found item line missing:\n%s", joined)
	}
	if !strings.Contains(joined, "[missing] characters/gone/(a)normal") {
		t.Errorf("missing item line missing:\n%s", joined)
	}
	// An empty category must not print a header.
	if strings.Contains(joined, "Chat blips") {
		t.Errorf("empty category must be omitted:\n%s", joined)
	}
}

// ---------------------------------------------------------------------------
// Probe batching — bounded window (mirrors TestTickGifWarm... shape).
// ---------------------------------------------------------------------------

// TestContentProbeWindowBounded pins that the probe never runs more than
// contentProbeWorkers ResolveRaw/FetchRaw calls concurrently, however many
// assets a scene references — the wave-1 in-flight-window discipline. It wires a
// REAL Manager over a slow origin that COUNTS concurrent in-flight fetches; the
// observed peak must never exceed the worker window.
func TestContentProbeWindowBounded(t *testing.T) {
	var (
		gate     = make(chan struct{}) // released once, to let all held fetches proceed
		inFlight int32
		peak     int32
		mu       = make(chan struct{}, 1) // 1-slot mutex
	)
	mu <- struct{}{}
	bump := func(d int32) int32 {
		<-mu
		inFlight += d
		cur := inFlight
		if cur > peak {
			peak = cur
		}
		mu <- struct{}{}
		return cur
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bump(1)
		<-gate // hold every request open so concurrency is observable
		bump(-1)
		w.WriteHeader(http.StatusNotFound) // all missing — we're testing the window, not content
	}))
	t.Cleanup(srv.Close)

	a := headlessProbeApp(t, network.NewClient(), false)
	// A big scene — far more refs than the window — so an unbounded probe would
	// blow past contentProbeWorkers.
	rec := &sceneRecording{Origin: srv.URL + "/", StartBg: "courtroom"}
	for i := 0; i < 60; i++ {
		rec.Events = append(rec.Events, msgEvent(fmt.Sprintf("Char%d", i), "normal", "", "", ""))
	}

	if !a.StartContentReport(rec, "scene") {
		t.Fatal("StartContentReport refused")
	}
	// Let workers pile up against the gate, sampling the peak, then release.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		<-mu
		p := peak
		mu <- struct{}{}
		if p >= int32(contentProbeWorkers) {
			break // the window is saturated — enough to check the ceiling
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(gate) // let everything finish

	// Drive the render-thread poll until the probe completes.
	drainContentJob(t, a)

	<-mu
	got := peak
	mu <- struct{}{}
	if got > int32(contentProbeWorkers) {
		t.Fatalf("probe peak concurrency %d exceeded the worker window %d", got, contentProbeWorkers)
	}
}

// TestContentProbeFoundMissing pins the core report semantics over a real
// origin: a character whose sprite exists is found; one that doesn't is missing.
func TestContentProbeFoundMissing(t *testing.T) {
	sprite := pngBytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// .webp, NOT .png: an unlearned host probes ONLY the default format list,
		// and CharSprite's default is exactly [.webp] (the zero-fallback pillar —
		// TestFormatListZeroFallbackIsExactConfiguredList). Serving .png here
		// would test a fallback walk the client refuses by design. The payload
		// bytes are opaque to probe+package (no decode), so pngBytes() is fine.
		if r.URL.Path == "/characters/phoenix/(a)normal.webp" {
			_, _ = w.Write(sprite)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	a := headlessProbeApp(t, network.NewClient(), false)
	rec := &sceneRecording{
		Origin:  srv.URL + "/",
		StartBg: "courtroom",
		Events: []recEvent{
			msgEvent("Phoenix", "normal", "", "", ""),
			msgEvent("Ghost", "normal", "", "", ""),
		},
	}
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("StartContentReport refused")
	}
	drainContentJob(t, a)

	rep := a.ContentJobReport()
	if rep == nil {
		t.Fatal("no report after probe")
	}
	var foundPhoenix, missingGhost bool
	for _, it := range rep.Categories[CatCharacter].Items {
		if strings.Contains(it.Name, "phoenix") && it.Status == StatusFound {
			foundPhoenix = true
		}
		if strings.Contains(it.Name, "ghost") && it.Status == StatusMissing {
			missingGhost = true
		}
	}
	if !foundPhoenix {
		t.Errorf("phoenix sprite should be found; items: %v", itemStatuses(rep, CatCharacter))
	}
	if !missingGhost {
		t.Errorf("ghost sprite should be missing; items: %v", itemStatuses(rep, CatCharacter))
	}
}

// ---------------------------------------------------------------------------
// Packaging — golden-shape + byte round-trip.
// ---------------------------------------------------------------------------

// TestPackageBundleLayout pins the package's on-disk shape against the EXISTING
// bundle format: the found asset lands at its origin-relative path, the bundled
// .aorec is present + marked Bundled, and the plain-text gap list is written in.
// It also round-trips the sprite bytes to prove packaging reuses the probed
// bytes verbatim.
func TestPackageBundleLayout(t *testing.T) {
	sprite := pngBytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// .webp, NOT .png: an unlearned host probes ONLY the default format list,
		// and CharSprite's default is exactly [.webp] (the zero-fallback pillar —
		// TestFormatListZeroFallbackIsExactConfiguredList). Serving .png here
		// would test a fallback walk the client refuses by design. The payload
		// bytes are opaque to probe+package (no decode), so pngBytes() is fine.
		if r.URL.Path == "/characters/phoenix/(a)normal.webp" {
			_, _ = w.Write(sprite)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	// PackageContentBundle writes under recordingsDir() (the test-binary's dir);
	// register the bundle folder for cleanup so the exe dir isn't polluted.
	dir := recordingsDir()
	if dir == "" {
		t.Skip("no recordings dir resolvable in this environment")
	}
	// Clean any stale folder so the pre-computed path matches the one packaging
	// picks (nextBundleDir is deterministic over the current dir state).
	base := filepath.Join(dir, "myscene"+contentBundleSuffix)
	_ = os.RemoveAll(base)
	bundle := nextBundleDir(dir, "myscene")
	t.Cleanup(func() { _ = os.RemoveAll(bundle) })

	a := headlessProbeApp(t, network.NewClient(), false)
	rec := &sceneRecording{
		Origin:  srv.URL + "/",
		StartBg: "courtroom",
		Events: []recEvent{
			msgEvent("Phoenix", "normal", "", "", ""),
			msgEvent("Ghost", "normal", "", "", ""),
		},
	}
	if !a.StartContentReport(rec, "myscene") {
		t.Fatal("report refused")
	}
	drainContentJob(t, a)
	if !a.PackageContentBundle(rec) {
		t.Fatal("package refused")
	}
	drainContentJob(t, a)

	if _, err := os.Stat(bundle); err != nil {
		t.Fatalf("bundle folder %q not created: %v", bundle, err)
	}
	// The sprite at its origin-relative path, bytes intact.
	spritePath := filepath.Join(bundle, "characters", "phoenix", "(a)normal.webp")
	got, err := os.ReadFile(spritePath)
	if err != nil {
		t.Fatalf("bundled sprite missing: %v", err)
	}
	if string(got) != string(sprite) {
		t.Errorf("bundled sprite bytes differ from origin (%d vs %d)", len(got), len(sprite))
	}
	// The .aorec, marked Bundled.
	aorecData, err := os.ReadFile(filepath.Join(bundle, "myscene"+recordingExt))
	if err != nil {
		t.Fatalf("bundled .aorec missing: %v", err)
	}
	var out sceneRecording
	if err := json.Unmarshal(aorecData, &out); err != nil {
		t.Fatalf("bundled .aorec unparseable: %v", err)
	}
	if !out.Bundled {
		t.Error("bundled .aorec must set Bundled=true")
	}
	// The gap list.
	if _, err := os.Stat(filepath.Join(bundle, contentReportFileName)); err != nil {
		t.Errorf("missing-content text file not written: %v", err)
	}
}

// TestWriteContentBundleDirect exercises the writer without the App/probe
// plumbing (layout + gap-list + never-overwrite + external-skip + byte round-
// trip). It re-drains bytes from a LocalFetcher-backed Manager, mirroring how
// packaging pulls the probe's already-cached bytes.
func TestWriteContentBundleDirect(t *testing.T) {
	dir := t.TempDir()
	mount := t.TempDir()
	mgr, origin := localMgr(t, mount)
	seedMount(t, mount, "characters/phoenix/(a)normal.png", []byte("PNGDATA"))

	rec := &sceneRecording{Origin: origin, StartBg: "courtroom"}
	foundURLs := []string{
		origin + "characters/phoenix/(a)normal.png",
		"http://other.host/track.opus", // external — skipped (not under this origin)
	}
	report := []string{"Origin: " + origin, "Total: 2 — 1 found"}

	dest := nextBundleDir(dir, "scene")
	res := writeContentBundle(mgr, dest, "scene", rec, foundURLs, report)
	if res.dir == "" {
		t.Fatalf("package reported failure: %s", res.msg)
	}
	// Local asset written at its origin-relative path, bytes intact.
	if b, err := os.ReadFile(filepath.Join(dest, "characters", "phoenix", "(a)normal.png")); err != nil || string(b) != "PNGDATA" {
		t.Errorf("local asset not written verbatim: b=%q err=%v", b, err)
	}
	if !strings.Contains(res.msg, "external") {
		t.Errorf("skip of the external link should be reported; msg=%q", res.msg)
	}
	// Gap list present.
	if b, err := os.ReadFile(filepath.Join(dest, contentReportFileName)); err != nil || !strings.Contains(string(b), "Origin:") {
		t.Errorf("gap list not written: b=%q err=%v", b, err)
	}
	// Bundled .aorec present + marked.
	if b, err := os.ReadFile(filepath.Join(dest, "scene"+recordingExt)); err != nil {
		t.Errorf(".aorec not written: %v", err)
	} else {
		var out sceneRecording
		if json.Unmarshal(b, &out) != nil || !out.Bundled {
			t.Errorf(".aorec must be valid + Bundled; got %s", b)
		}
	}

	// Never overwrite: dest now exists, so a second bundle for the same stem gets a
	// distinct folder.
	dest2 := nextBundleDir(dir, "scene")
	if dest2 == dest {
		t.Fatalf("nextBundleDir returned the same path twice: %q", dest2)
	}
	if filepath.Base(dest2) != "scene"+contentBundleSuffix+"-2" {
		t.Errorf("second bundle should be -2, got %q", filepath.Base(dest2))
	}
}

// TestPackageCountsAndTotals pins the running total: every under-cap asset is
// written and the result line reports the count. (The 2 GiB byte-cap abort can't
// be exercised without allocating past the cap, so its arithmetic is covered by
// inspection: total+len(data) > contentPackageMaxBytes returns the abort line.)
func TestPackageCountsAndTotals(t *testing.T) {
	dir := t.TempDir()
	mount := t.TempDir()
	mgr, origin := localMgr(t, mount)
	seedMount(t, mount, "a/one.png", []byte("one"))
	seedMount(t, mount, "a/two.png", []byte("two"))

	rec := &sceneRecording{Origin: origin}
	foundURLs := []string{origin + "a/one.png", origin + "a/two.png"}
	dest := nextBundleDir(dir, "cap")
	res := writeContentBundle(mgr, dest, "cap", rec, foundURLs, []string{"x"})
	if res.dir == "" {
		t.Fatalf("package failed: %s", res.msg)
	}
	if !strings.Contains(res.msg, "Packaged 2 assets") {
		t.Errorf("both under-cap assets should be packaged; msg=%q", res.msg)
	}
}

// ---------------------------------------------------------------------------
// Cancel — leak-free mid-probe and mid-package.
// ---------------------------------------------------------------------------

// TestCancelMidProbeLeakFree pins that cancelling while probe workers are held
// open drops the job and lets every probe goroutine exit — no leak. It holds the
// origin's requests open (so the feeder + workers are genuinely in flight),
// cancels, releases the gate, and checks the goroutine count settles back near
// the post-build baseline. (An exact count is impossible — the shared HTTP
// transport keeps a few idle-conn readers alive after the held fetches return —
// so the margin allows those while still catching the feeder+workers, 9
// goroutines, failing to exit.)
func TestCancelMidProbeLeakFree(t *testing.T) {
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	a := headlessProbeApp(t, network.NewClient(), false)
	rec := &sceneRecording{Origin: srv.URL + "/", StartBg: "courtroom"}
	for i := 0; i < 40; i++ {
		rec.Events = append(rec.Events, msgEvent(fmt.Sprintf("C%d", i), "normal", "", "", ""))
	}
	if !a.StartContentReport(rec, "scene") {
		t.Fatal("report refused")
	}
	// The STRUCTURAL leak observable: the job's own live-goroutine counter.
	// Global runtime.NumGoroutine deltas flaked the full gate twice — HTTP
	// keep-alive readers linger past any deadline, and force-closing conns
	// just trades that for the client's transport-retry backoff parking the
	// workers. probeLive counts exactly the feeder + workers the cancel
	// contract promises to reap, and nothing else.
	j := a.content
	if j == nil {
		t.Fatal("no job after start")
	}
	// Wait until the workers are genuinely in flight inside their held fetches.
	deadline := time.Now().Add(5 * time.Second)
	for j.probeLive.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if j.probeLive.Load() == 0 {
		t.Fatal("probe goroutines never started")
	}
	a.CancelContentJob()
	if a.content != nil || a.contentBusy {
		t.Fatal("cancel must clear the job immediately")
	}
	close(gate) // let the held fetches 404 cleanly; workers then observe stop / drained workCh

	// Every job goroutine must exit. Generous window: under the full race
	// suite a worker still finishes its in-flight probe (cancel is
	// cooperative-after-current-probe by design) before observing stop.
	deadline = time.Now().Add(20 * time.Second)
	for j.probeLive.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := j.probeLive.Load(); n != 0 {
		t.Errorf("probe goroutines leaked: %d of the feeder+workers still live", n)
	}
}

// TestCancelMidPackageLeakFree pins that cancelling during packaging is safe:
// the goroutine's result lands in the 1-slot buffer nobody polls and it dies.
func TestCancelMidPackageLeakFree(t *testing.T) {
	sprite := pngBytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			_, _ = w.Write(sprite)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	dir := recordingsDir()
	if dir == "" {
		t.Skip("no recordings dir resolvable in this environment")
	}
	cancelBundle := filepath.Join(dir, "cancelpkg"+contentBundleSuffix)
	_ = os.RemoveAll(cancelBundle)
	t.Cleanup(func() { _ = os.RemoveAll(cancelBundle) })
	a := headlessProbeApp(t, network.NewClient(), false)
	rec := &sceneRecording{
		Origin: srv.URL + "/", StartBg: "courtroom",
		Events: []recEvent{msgEvent("Phoenix", "normal", "", "", "")},
	}
	if !a.StartContentReport(rec, "cancelpkg") {
		t.Fatal("report refused")
	}
	drainContentJob(t, a)
	// Baseline AFTER the probe phase (its goroutines are gone; the app's pools +
	// any idle HTTP conns remain), so only the package goroutine moves the count.
	time.Sleep(50 * time.Millisecond)
	base := runtime.NumGoroutine()
	if !a.PackageContentBundle(rec) {
		t.Fatal("package refused")
	}
	a.CancelContentJob() // drop before the goroutine may have delivered
	if a.content != nil {
		t.Fatal("cancel must clear the packaging job")
	}
	// The lone package goroutine writes a tiny bundle and exits; the count returns
	// to baseline (small slack for scheduler timing).
	const pkgSlack = 4
	if !waitGoroutines(base+pkgSlack, 3*time.Second) {
		t.Errorf("package goroutine leaked: base=%d now=%d", base, runtime.NumGoroutine())
	}
}

// TestSecondJobRefused pins single-flight: a second StartContentReport while one
// runs is refused politely (returns false, warns).
func TestSecondJobRefused(t *testing.T) {
	a := headlessProbeApp(t, assets.NewLocalFetcher([]string{t.TempDir()}), true)
	rec := synthRecording("") // origin-missing → short-circuits to phaseReport, job stays busy
	if !a.StartContentReport(rec, "one") {
		t.Fatal("first report refused")
	}
	if a.StartContentReport(rec, "two") {
		t.Error("second report must be refused while one is active")
	}
	if !strings.Contains(a.warnLine, "already running") {
		t.Errorf("refusal should warn; got %q", a.warnLine)
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

// headlessProbeApp wires a headless App with a REAL streaming Manager (no SDL /
// no TextureStore — the content probe path is pure Manager: ResolveRaw/FetchRaw
// never touch T1 or the pool's async delivery, so no channel drainer is needed).
func headlessProbeApp(t *testing.T, source assets.Fetcher, localMode bool) *App {
	t.Helper()
	a := testTabApp(t)
	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	a.d.Resolver = resolver
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    source,
		LocalMode: localMode,
		Pool:      pool,
		Decoder:   decoder,
	})
	return a
}

// localMgr builds a bare Manager over a LocalFetcher mount and returns it plus
// the mount's origin (BaseURL). FetchRaw over that origin reads files written by
// seedMount — standing in for the cache tiers the probe would have filled.
func localMgr(t *testing.T, mount string) (*assets.Manager, string) {
	t.Helper()
	a := testTabApp(t) // reuse its prefs (a real config); we only need the Manager
	resolver := assets.NewResolver(a.d.Prefs)
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	pool := network.NewPool(1)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(1)
	t.Cleanup(decoder.Close)
	local := assets.NewLocalFetcher([]string{mount})
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver:  resolver,
		Prefs:     a.d.Prefs,
		T2:        t2,
		Disk:      disk,
		Source:    local,
		LocalMode: true,
		Pool:      pool,
		Decoder:   decoder,
	})
	return mgr, local.BaseURL()
}

// seedMount writes an asset file into a LocalFetcher mount at its forward-slash
// relative path, so FetchRaw(origin+rel) reads it back.
func seedMount(t *testing.T, mount, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(mount, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// drainContentJob pumps tickContentJob until the job leaves the probe/package
// phase (report ready, or job cleared after packaging), bounded so a bug can't
// hang the test.
func drainContentJob(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		a.tickContentJob()
		j := a.content
		if j == nil || j.phase == phaseReport || j.phase == phaseDone {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("content job did not settle within the deadline")
}

// waitGoroutines polls until NumGoroutine drops to <= want (leak check),
// returning false on timeout.
func waitGoroutines(want int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return runtime.NumGoroutine() <= want
}

func hasItemURL(r *ContentReport, cat ContentCategory, url string) bool {
	for _, it := range r.Categories[cat].Items {
		if it.URL == url || it.ref.Base == url {
			return true
		}
	}
	return false
}

func itemURLs(r *ContentReport, cat ContentCategory) []string {
	var out []string
	for _, it := range r.Categories[cat].Items {
		out = append(out, it.URL)
	}
	return out
}

func itemStatuses(r *ContentReport, cat ContentCategory) []string {
	var out []string
	for _, it := range r.Categories[cat].Items {
		out = append(out, it.Name+":"+it.Status.String())
	}
	return out
}

// pngBytes returns a minimal valid PNG (the decoder never runs on the probe
// path, so any non-empty bytes with a png magic suffice for the resolver's
// magic-byte learning; a real PNG header keeps it honest).
func pngBytes() []byte {
	// 8-byte PNG signature + a trivial tail; the content probe only needs the
	// bytes to be non-empty and fetchable — it does not decode them.
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("asyncao-test-sprite")...)
}
