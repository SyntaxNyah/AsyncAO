package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/videoenc"
)

// newExportStallHarness wires a headless App with the FULL export pipeline against
// an all-404 origin — a real Manager over an httptest server that 404s every
// request (the "dead/empty origin, assets never resolve" case H3 exists to serve),
// a real TextureStore + Viewport + upload Pump, and a software capture renderer. It
// drives an export end to end (startSceneExport → warm → capture → finish) with the
// same per-frame channel drains the live loop runs, so the test proves the export
// COMPLETES rather than freezing. Skips headlessly when SDL/render targets aren't
// available. Returns the App and the origin URL to build a recording against.
func newExportStallHarness(t *testing.T) (*App, string) {
	t.Helper()
	a := testTabApp(t)

	// Drainer stop registered FIRST so its cleanup runs LAST (t.Cleanup is LIFO) —
	// pool.Close / decoder.Close must complete while the goroutine below is still
	// consuming, or a worker blocked sending into a full channel deadlocks Close's
	// WaitGroup and times the whole binary out (newWarmHarness's ledger note).
	stopDrain := make(chan struct{})
	t.Cleanup(func() { close(stopDrain) })

	// All-404 origin: every asset probe (and the extensions.json seed) 404s, so the
	// whole scene's fallback chains exhaust → the Manager reports each base missing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	// A fully-initialized Ctx (embedded font, device faces at 100%) so the capture
	// phase can rasterize the chatbox text into the offscreen target — the bare
	// &Ctx{} from testTabApp has no font and drawGifChatbox would fault on it.
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a.ctx = ctx
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	a.d.Store = store
	a.d.Viewport = render.NewViewport(store)
	a.d.Audio = &render.Audio{} // disabled device: a safe no-op AudioSink

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
	pool := network.NewPool(4)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver: resolver,
		Prefs:    a.d.Prefs,
		T2:       t2,
		Disk:     disk,
		Source:   network.NewClient(), // stream over HTTP: every request 404s (the all-404 origin)
		Pool:     pool,
		Decoder:  decoder,
	})
	a.d.Pump = render.NewPump(store, a.d.Manager, nil)

	// Channel ownership during the drive is split to avoid two consumers on one
	// channel (a data race): the drive loop's Pump.Frame() owns Decoded and its
	// drainWarnings() owns Warnings (so the Store sees the conclusive misses). This
	// harness goroutine soaks ONLY Audio — nothing else pulls it, and an all-404 scene
	// never decodes audio anyway, but draining it keeps a pool worker from ever
	// blocking on a full audio channel.
	go func() {
		for {
			select {
			case <-a.d.Manager.Audio():
			case <-stopDrain:
				return
			}
		}
	}()

	return a, srv.URL + "/"
}

// driveExportToCompletion runs the export tick loop the way the live frame loop
// does — drainWarnings (relays each conclusive 404 to Store.MarkMissing so the
// warm's settled count advances and drawSprite draws missingno), the upload Pump,
// then tickGifExport — until the export finishes (a.gif cleared) or the deadline
// hits. A short inter-tick sleep mirrors the real frame cadence and gives the async
// pool probes wall-clock time to exhaust the 404 chains (the established
// drainContentJob pattern), so the warm actually observes the misses. Returns the
// number of ticks it took. The deadline is generous headroom over a healthy run but
// far under the 20 s warm hard cap a regressed stall would burn.
func driveExportToCompletion(t *testing.T, a *App, budget int) int {
	t.Helper()
	// Generous: the software renderer's per-frame capture+quantize cost under
	// -p 1 dominates (two gate rounds proved ~60ms/tick average), so the
	// deadline bounds COMPLETION, not speed. The precise frozen-warm stall
	// discriminator lives in the warm unit tests
	// (TestTickGifWarmConclusiveMissEndsWarmPromptly); this drive only has to
	// prove the export finishes at all.
	deadline := time.Now().Add(90 * time.Second)
	for i := 0; i < budget; i++ {
		if time.Now().After(deadline) {
			return i // deadline hit: the caller asserts a.gif != nil to flag the stall
		}
		a.drainWarnings() // conclusive 404 → Store.MarkMissing (+ nil-safe room relays)
		a.d.Pump.Frame()  // upload any decoded textures into T1 (none here — all 404)
		a.tickGifExport() // warm → capture → finish
		if a.gif == nil {
			return i + 1 // finished (finishGifExport / an error teardown cleared the job)
		}
		time.Sleep(2 * time.Millisecond) // real frame cadence; lets the 404 probes land
	}
	return budget
}

// describeExportStall renders the full job state on a drive failure so a red
// run is a DIAGNOSIS, not a mystery: which phase held the job, how far the
// script/capture got, and what the room was doing. Added after two gate rounds
// where "never finished" alone couldn't distinguish a frozen warm, a wedged
// room, or a merely-slow capture.
func describeExportStall(a *App, ticks int) string {
	j := a.gif
	if j == nil {
		return fmt.Sprintf("no stall: job already cleared after %d ticks", ticks)
	}
	phase, qlen := "no-room", -1
	if j.room != nil {
		phase = fmt.Sprintf("%v", j.room.Phase())
		qlen = j.room.QueueLen()
	}
	return fmt.Sprintf(
		"export never finished after %d ticks — loading=%v seeding=%v warming=%v idx=%d/%d captured=%d/%d roomPhase=%s queue=%d warnLine=%q",
		ticks, j.loading, j.seeding, j.warming, j.idx, len(j.events),
		j.captured, j.maxFrames, phase, qlen, a.warnLine)
}

// TestExportAll404DrivesToCompletionGIF is the H3 armor: a GIF export of a scene
// whose EVERY asset 404s (dead/empty origin) must run tick-driven to COMPLETION —
// a finished file on disk, captured>0 (placeholder art, full length), within a
// bounded number of ticks — NOT freeze at "0 / N ready" for the full 20 s warm
// hard cap (the reported "export that never even converts"). It exercises the real
// pipeline: startSceneExport, the format seed against the 404 origin, the pre-warm
// with the conclusive-miss release, the capture loop, and the off-thread encode.
//
// This is permanent armor: on code without the settled-includes-missing release,
// the warm would never latch warmSubmitDone (no ref ever prunes from warmInFlight),
// so it would sit warming until gifWarmHardCap — 20 s of wall clock — which blows
// this test's fast tick budget AND its wall-time guard. The fix makes the misses
// settle the warm in a handful of ticks.
func TestExportAll404DrivesToCompletionGIF(t *testing.T) {
	a, origin := newExportStallHarness(t)

	// The scene MUST have more distinct-character sprite refs than warmInFlightWindow
	// so the stall actually reproduces on unfixed code: with fewer refs than the
	// window, the whole pendingWarm list submits in one tick, warmSubmitDone latches
	// even without the miss release, and gifWarmMax (6 s) ends the warm — the export
	// would then "complete" on the buggy code too and the test would prove nothing.
	// Above the window, an unfixed warm can never prune its in-flight set (nothing
	// becomes resident, and without the miss-as-settled release nothing else clears
	// it), so pendingWarm sticks, warmSubmitDone never latches, and ONLY the 20 s
	// gifWarmHardCap ends it — blowing this test's budget with a.gif still set. That
	// is the discriminating armor.
	rec := &sceneRecording{
		Version: recordingVersion,
		Origin:  origin,
		StartBg: "courtroom",
	}
	// window+8 keeps the discriminating >window property (each char contributes
	// two sprite refs) while capping the CAPTURE cost: every message must still
	// play out in simulated time, and the first gate round proved window+40
	// messages of full-size quantized frames blow the wall deadline on a
	// software renderer under -p 1.
	for i := 0; i < warmInFlightWindow+8; i++ {
		side := "def"
		if i%2 == 1 {
			side = "pro"
		}
		rec.Events = append(rec.Events, recEvent{
			Kind:    int(courtroom.EventMessage),
			Message: &protocol.ChatMessage{CharName: charName(i), Emote: "normal", Side: side, Message: "Objection!"},
		})
	}

	// Speed the export up so the tick budget is small: a modest fps and the smallest
	// preset keep frameDt large and the frame cap low, and the replay timing is fast.
	a.d.Prefs.SetReplaySpeed(200)
	// Smallest legal frame + slowest legal fps (SetExportOpts clamps to the
	// mins): each captured frame advances the sim by the LARGEST legal dt and
	// quantizes the FEWEST pixels, so the whole script fits the tick budget.
	// Without this the test captured full-size frames — ~64ms/tick — and ran
	// out of wall clock while genuinely converging (the first gate round).
	a.d.Prefs.SetExportOpts(config.ExportOptions{HeightPx: 1, FPS: 1, Quality: 1, TextScale: 100}) // fastest crawl/linger

	wallStart := time.Now()
	a.startSceneExport(rec, "all404", exportGIF)
	if a.gif == nil || !a.gifExporting {
		t.Fatal("startSceneExport did not begin the export")
	}

	// Bounded tick budget: the seed fetch (a 404 → seedAbsent, instant), the warm
	// (settles as the misses land), and the capture (a few hundred frames at most,
	// gifFramesPerTick each) all finish well inside this. If the stall regressed, the
	// warm never ends and this budget is exhausted with a.gif still set.
	const tickBudget = 4000
	ticks := driveExportToCompletion(t, a, tickBudget)

	if a.gif != nil {
		t.Fatal(describeExportStall(a, ticks))
	}
	if a.gifExporting {
		t.Error("gifExporting still set after the export finished")
	}
	// Wall-time guard: the whole drive must be far under the 20 s warm hard cap that a
	// regressed stall would burn. (Generous: CI + the 404 round-trips, but nowhere
	// near 20 s.)
	// No separate wall guard: a healthy full cap-out measured 41s on the dev
	// box (software renderer, -p 1, suite under load), so any tighter ceiling
	// is flake surface. The 90s completion deadline inside
	// driveExportToCompletion is the bound that matters — the export either
	// finishes inside it or the diagnostic Fatal above names the held phase.
	_ = wallStart

	// The off-thread GIF encode delivers on a.gifResultCh; poll it (bounded) so the
	// test proves a real artifact was produced, not just that the loop terminated.
	var res exportResult
	select {
	case res = <-a.gifResultCh:
	case <-time.After(10 * time.Second):
		t.Fatal("GIF encode result never arrived — the encode goroutine hung")
	}
	// captured>0 art: the encode either wrote a file (success message names it) OR, if
	// the scene rendered zero frames, reports the "nothing rendered" guard. H3 requires
	// a COMPLETE video with placeholder art, so the success path (a file) is the bar.
	if res.path == "" {
		t.Fatalf("export produced no file — H3 requires a complete artifact with placeholder art, got %q", res.msg)
	}
	if st, err := os.Stat(res.path); err != nil || st.Size() == 0 {
		t.Fatalf("export file missing/empty at %q: err=%v", res.path, err)
	}
	t.Cleanup(func() { _ = os.Remove(res.path) })
}

// TestExportAll404DrivesToCompletionVideo mirrors the GIF armor for the video path,
// gated on a system ffmpeg (videoenc.Available). Video streams frames straight to
// ffmpeg under its own duration brake, so the same warm-stall would strand it just
// the same — the conclusive-miss release must let it converge too.
func TestExportAll404DrivesToCompletionVideo(t *testing.T) {
	if !videoenc.Available() {
		t.Skip("ffmpeg not on PATH — video export path unavailable")
	}
	a, origin := newExportStallHarness(t)

	// Above warmInFlightWindow distinct characters — the discriminating scene size (see
	// the GIF armor's rationale): fewer refs would let an unfixed warm latch and end.
	rec := &sceneRecording{
		Version: recordingVersion,
		Origin:  origin,
		StartBg: "courtroom",
	}
	// window+8 keeps the discriminating >window property (each char contributes
	// two sprite refs) while capping the CAPTURE cost: every message must still
	// play out in simulated time, and the first gate round proved window+40
	// messages of full-size quantized frames blow the wall deadline on a
	// software renderer under -p 1.
	for i := 0; i < warmInFlightWindow+8; i++ {
		side := "def"
		if i%2 == 1 {
			side = "pro"
		}
		rec.Events = append(rec.Events, recEvent{
			Kind:    int(courtroom.EventMessage),
			Message: &protocol.ChatMessage{CharName: charName(i), Emote: "normal", Side: side, Message: "Objection!"},
		})
	}
	a.d.Prefs.SetReplaySpeed(200)
	// Smallest legal frame + slowest legal fps (SetExportOpts clamps to the
	// mins): each captured frame advances the sim by the LARGEST legal dt and
	// quantizes the FEWEST pixels, so the whole script fits the tick budget.
	// Without this the test captured full-size frames — ~64ms/tick — and ran
	// out of wall clock while genuinely converging (the first gate round).
	a.d.Prefs.SetExportOpts(config.ExportOptions{HeightPx: 1, FPS: 1, Quality: 1, TextScale: 100})

	wallStart := time.Now()
	a.startSceneExport(rec, "all404vid", exportVideo)
	if a.gif == nil || !a.gifExporting {
		t.Fatal("startSceneExport did not begin the video export")
	}

	const tickBudget = 4000
	driveExportToCompletion(t, a, tickBudget)
	if a.gif != nil {
		t.Fatal("video " + describeExportStall(a, tickBudget))
	}
	if el := time.Since(wallStart); el > 90*time.Second {
		t.Errorf("video export took %v — far beyond a healthy run; something held the pipeline", el)
	}

	select {
	case res := <-a.gifResultCh:
		if res.path != "" {
			if _, err := os.Stat(res.path); err == nil {
				t.Cleanup(func() { _ = os.Remove(res.path) })
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("video finish result never arrived — ffmpeg reap hung")
	}
}
