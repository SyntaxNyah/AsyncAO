package ui

// THE THEME EDITOR'S PREVIEW, DRIVEN (field batch 9).
//
// Every gate here goes through the REAL frame (frameharness_test.go) and the real
// input path: the button is pressed at the pixels drawEditorHeader publishes, Esc
// arrives as a key on Ctx, and the demo is advanced by App.Frame rather than by
// calling driveEditorPreview. That is deliberate — the wiring is what this feature
// IS. A preview whose drive case were deleted from App.Frame's scene switch, whose
// Esc rung were deleted from app.go's ladder, or whose draw arm were deleted from
// drawThemeEditor would still have a perfectly working editorPreview type, and a
// gate that called those functions itself would never notice.
//
// Rule 11's encapsulation gates are at the bottom: one construction site, one door
// out, and a call-site census that fails if the seam is unwired anywhere.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// previewDriveFrames is how many real frames a gate spends watching the demo.
	// At frameHarnessDt each that is ~0.5 s of simulated time — comfortably past
	// the typewriter's first few reveals and far short of the rest-and-loop.
	previewDriveFrames = 30
	// previewLoopFrames is long enough to cross editorPreviewRest and see the demo
	// play its line a SECOND time (the crawl plus the rest, with headroom).
	previewLoopFrames = 400
)

// enterPreviewByButton presses the real Preview button at the geometry the header
// band publishes, through one real frame. It is how every gate here enters, so a
// re-spaced band moves the press with it instead of silently missing.
func enterPreviewByButton(t *testing.T, a *App) {
	t.Helper()
	x, y := frameHarnessRectCentre(editorPreviewBtnRect(frameHarnessW))
	driveClickAt(a, x, y)
	if !a.editorPreviewOn() {
		t.Fatal("the Preview button did not enter the demo — the press missed the header band")
	}
}

// pressEscFrame delivers one real Esc through Ctx and App.Frame.
func pressEscFrame(a *App) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.escPressed = true
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// ---------------------------------------------------------------------------
// (1) The button itself
// ---------------------------------------------------------------------------

// TestPreviewButtonIsWideLabelledAndBesideShare pins requirement 1: a PROMINENT
// control, not a small toggle, in the band that already carries Save and Share.
//
// The three properties are pinned together because each one alone is satisfiable
// by the thing the brief refused: a wide button somewhere else, a narrow button
// beside Share, or a labelled control that is not a button at all.
func TestPreviewButtonIsWideLabelledAndBesideShare(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)

	r := editorPreviewBtnRect(frameHarnessW)
	if r.W < editorHeaderBtnW*2 {
		t.Errorf("the Preview button is %d px wide, want at least twice a header button (%d) — "+
			"the brief asks for a prominent control, not another 62 px chip", r.W, editorHeaderBtnW*2)
	}
	if r.H != editBannerH-4 || r.Y != 2 {
		t.Errorf("the Preview button sits at y=%d h=%d, outside the header band (y=2 h=%d) — "+
			"it must be in the row with Save and Share", r.Y, r.H, editBannerH-4)
	}
	// Beside Share: Share is slot 6 from the right, so its left edge is exactly one
	// gap to the right of this button's right edge.
	shareX := frameHarnessW - editRowPad - editorHeaderBtnW - 6*(editorHeaderBtnW+editorHeaderBtnGap)
	if got := r.X + r.W + editorHeaderBtnGap; got != shareX {
		t.Errorf("the Preview button ends at %d and Share starts at %d — they must be adjacent, "+
			"or the wide box has pushed the row out from under every gate that counts slots", got, shareX)
	}
	if !strings.Contains(editorPreviewEnterLabel, "Preview") || !strings.Contains(editorPreviewExitLabel, "preview") {
		t.Errorf("the two captions are %q / %q — a control nobody can name is not discoverable",
			editorPreviewEnterLabel, editorPreviewExitLabel)
	}
	// And it works: the press at those pixels enters the demo.
	enterPreviewByButton(t, a)
}

// TestTheHeaderRowKeepsItsExistingSlots is the CLOSED half of open–closed. The
// seven buttons that were in the band before Preview must not have moved — every
// existing gate presses them by slot index from the right edge.
func TestTheHeaderRowKeepsItsExistingSlots(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	if a.te.doc.dirty {
		t.Fatal("fixture: the document must be clean, or Back arms a confirmation instead of leaving")
	}
	// Back is slot 0 and still leaves — the same press TestBackDoesNotOutliveTheRails
	// makes, repeated here so a re-spaced band fails in BOTH files rather than
	// silently in one.
	x, y := editorHeaderBtnCentre(frameHarnessW, 0)
	driveClickAt(a, x, y)
	if a.te != nil {
		t.Fatal("Back moved: the press at slot 0 no longer leaves the editor")
	}
}

// ---------------------------------------------------------------------------
// (2) Preview hides every piece of editing chrome
// ---------------------------------------------------------------------------

// TestPreviewHidesEveryPieceOfEditingChrome pins requirement 2 from both sides.
//
// BEHAVIOURALLY: a click where the element rail's Live/Frozen toggle sits, and a
// click where a rail row sits, must do nothing at all — no toggle, no selection.
// If a rail were still drawn under the demo, those presses would land.
//
// STRUCTURALLY: drawThemeEditor's preview arm must RETURN, above the key handler,
// the canvas input, the overlay (handles, selection outlines, magnet guides) and
// both rails. Hiding by not-calling is the only version that stays true when the
// next overlay is added, and a behavioural gate can only ever cover the surfaces
// it already knows about.
func TestPreviewHidesEveryPieceOfEditingChrome(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	liveBefore := a.editorLiveOn()
	enterPreviewByButton(t, a)
	driveFrames(a, 2)

	// The Live/Frozen toggle's own rect, from the rail's published geometry.
	liveR := sdl.Rect{
		X: editRailW - editRowPad - editRailLiveBtnW,
		Y: editBannerH + 2,
		W: editRailLiveBtnW,
		H: editRowH - 2,
	}
	lx, ly := frameHarnessRectCentre(liveR)
	driveClickAt(a, lx, ly)
	if a.editorLiveOn() != liveBefore {
		t.Error("a click in the element rail's Live/Frozen box flipped the toggle — the rail is still drawn under the demo")
	}
	if !a.editorPreviewOn() {
		t.Fatal("the click left the demo — nothing in the rails should have been reachable at all")
	}
	// A rail ROW, and the inspector rail on the far side.
	driveClickAt(a, editRailW/2, editBannerH+editRowH*2)
	driveClickAt(a, frameHarnessW-editRailW/2, editBannerH+editFieldRowH*2)
	if a.te.selN != 0 {
		t.Errorf("clicks in the rails selected %d target(s) — the element rail is live under the demo", a.te.selN)
	}

	body := funcBodyInPackage(t, "func (a *App) drawThemeEditor(")
	arm := strings.Index(body, "a.drawEditorPreviewScreen(w, h)")
	if arm < 0 {
		t.Fatal("drawThemeEditor no longer draws the preview screen — the demo has no draw site")
	}
	ret := strings.Index(body[arm:], "return")
	if ret < 0 {
		t.Fatal("drawThemeEditor's preview arm does not return — everything below it still draws over the demo")
	}
	// Every chrome draw the arm must sit ABOVE. Named, so the failure says which
	// surface would leak rather than "something moved".
	for _, chrome := range []string{
		"a.handleEditorKeys()",
		"a.editorCanvasInput(",
		"a.drawEditorCanvasOverlay(",
		"a.drawEditorHeader(",
		"a.drawEditorElementRail(",
		"a.drawEditorInspector(",
		"a.drawEditorFontPanel(",
		"a.drawThemeGallery(",
		"a.drawThemeReportPanel(",
	} {
		at := strings.Index(body, chrome)
		if at < 0 {
			t.Errorf("drawThemeEditor no longer calls %s at all — this census has stopped covering it", chrome)
			continue
		}
		if at < arm {
			t.Errorf("%s runs BEFORE the preview arm — it would draw over the demo. The arm must preempt "+
				"every editing surface, not compete with them", chrome)
		}
	}
}

// ---------------------------------------------------------------------------
// (3) The canned scene drives the REAL painters
// ---------------------------------------------------------------------------

// TestPreviewDemoDrivesTheRealCourtroomPainters pins requirement 3: the demo is
// the real typewriter, the real showname plate, the real music banner and the real
// IC log, not a mock renderer.
//
// The typewriter assertion is the load-bearing one: VisibleRunes is what the
// chatbox painter reveals, it is strictly between "nothing" and "everything" while
// the line crawls, and it only moves because App.Frame advanced the demo room.
func TestPreviewDemoDrivesTheRealCourtroomPainters(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	enterPreviewByButton(t, a)

	sc := &a.te.preview.state.room.Scene
	if sc.VisibleRunes != 0 {
		t.Fatalf("the demo revealed %d runes before a single frame was driven — the crawl is not the typewriter's",
			sc.VisibleRunes)
	}
	driveFrames(a, previewDriveFrames)

	total := len([]rune(sc.MessageText))
	if sc.VisibleRunes <= 0 || sc.VisibleRunes >= total {
		t.Errorf("after %d frames the typewriter shows %d of %d runes — a demo that is instantly done "+
			"(or never starts) is not crawling through the real typewriter",
			previewDriveFrames, sc.VisibleRunes, total)
	}
	if sc.MessageText != editorPreviewLine {
		t.Errorf("the staged line is %q, want the canned script's %q", sc.MessageText, editorPreviewLine)
	}
	if sc.ShownameText != editorPreviewShowname {
		t.Errorf("the showname plate reads %q, want %q — an empty plate is the one thing the demo must not show",
			sc.ShownameText, editorPreviewShowname)
	}
	if sc.MusicTrack != editorPreviewTrack {
		t.Errorf("the music banner is playing %q, want the canned title %q", sc.MusicTrack, editorPreviewTrack)
	}
	if got := len(a.te.preview.state.icLog); got != len(editorPreviewLog) {
		t.Errorf("the demo's IC scrollback holds %d rows, want the canned %d", got, len(editorPreviewLog))
	}
	// The LIVE session's own log is untouched: the demo appended into its own
	// sessionState, which is the whole point of the swap.
	if strings.Contains(strings.Join(icLogTexts(a.icLog), "\n"), editorPreviewLog[0].line) {
		t.Error("a canned demo line landed in the LIVE session's IC log — the pass leaked out of the demo state")
	}
}

// icLogTexts is the log's rendered lines, for a containment assertion.
func icLogTexts(log []icEntry) []string {
	out := make([]string, 0, len(log))
	for i := range log {
		out = append(out, log[i].text)
	}
	return out
}

// TestPreviewScrollsALongTitleThroughTheRealMarquee is the marquee half of
// requirement 3, and it needs the theme's music plate to be RESIDENT — the plate
// is what parents music_name, and AO2's ScrollText only exists inside it
// (drawThemedMusicPlate). So the fixture uploads a plate page exactly as a theme
// apply would, then drives the demo and reads the ONE marquee slot the client has.
func TestPreviewScrollsALongTitleThroughTheRealMarquee(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	stageThemePageForTest(t, a, "music_display")

	enterPreviewByButton(t, a)
	driveFrames(a, previewDriveFrames)

	want := courtroom.MusicDisplayName(editorPreviewTrack)
	if a.ctx.musicMarquee.label != want {
		t.Fatalf("the marquee slot holds %q, want the demo's title %q — the banner is not being fed by the demo",
			a.ctx.musicMarquee.label, want)
	}
	// AND IT ACTUALLY SCROLLS. marqueeScrolls is AO2's own scrollEnabled: the title
	// travels only when it does not fit. A short canned title would leave the author
	// staring at a marquee that never moves, which is the failure this pins.
	avail := ao2DefaultDesignRects["music_name"].W
	if !marqueeScrolls(a.ctx.TextWidth(want), int32(avail)) {
		t.Errorf("the canned title fits inside a %d px music_name at this face — it would sit still. "+
			"editorPreviewTrack has to be longer than the plate", avail)
	}
}

// stageThemePageForTest makes one theme stem RESIDENT, through the production key
// helper and the production texture store, so a themed draw path that gates on
// themePage() runs for real.
func stageThemePageForTest(t *testing.T, a *App, stem string) {
	t.Helper()
	dec := decodedPixelForTest()
	if err := a.d.Store.Upload(themeTexKey(stem), dec); err != nil {
		t.Fatalf("upload theme://%s: %v", stem, err)
	}
	if a.themeTex == nil {
		a.themeTex = map[string]bool{}
	}
	if a.themePages == nil {
		// The per-stem page cache the apply mints; a hand-built fixture never runs
		// that path, and themePage writes into it unconditionally.
		a.themePages = map[string]*render.TexturePage{}
	}
	a.themeTex[stem] = true
}

// ---------------------------------------------------------------------------
// (4) Zero network
// ---------------------------------------------------------------------------

// countingFetcher is the canary: an assets.Fetcher that records every probe the
// manager makes of its byte source and forwards nothing.
//
// A COUNTER AND NOT A LOG, because what is being asserted is a zero. It answers
// every URL with a conclusive miss so a demo that DID probe would be counted once
// per URL rather than blocking.
type countingFetcher struct{ n atomic.Int64 }

func (f *countingFetcher) Fetch(_ context.Context, _ string) ([]byte, error) {
	f.n.Add(1)
	return nil, network.ErrAssetNotFound
}

// TestEditorPreviewProbesNothing pins requirement 4: a driven demo must not put a
// single probe on the fetch path. No character art, no background, no desk, no
// blip file, no music stream — nothing.
//
// The manager is rebuilt around the canary so the count is the WHOLE truth for
// this App, and the demo is driven long enough to rest and play its line a second
// time, because a loop that re-staged the message would re-run the prefetch fan-out
// on every pass.
func TestEditorPreviewProbesNothing(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	canary := stageCountingFetcher(t, a)

	before := canary.n.Load()
	enterPreviewByButton(t, a)
	driveFrames(a, previewLoopFrames)
	if !a.editorPreviewOn() {
		t.Fatal("the demo left on its own — the gate measured the wrong thing")
	}
	// It really did loop, so the prefetch fan-out really did run more than once.
	if a.te.preview.state.room.Scene.VisibleRunes == 0 {
		t.Fatal("the demo never revealed a rune — nothing was driven, so zero probes proves nothing")
	}
	if got := canary.n.Load() - before; got != 0 {
		t.Errorf("a driven demo made %d probe(s) on the fetch path, want 0 — the preview is not offline. "+
			"Both guarantees have to hold: no fetchable content in the canned scene, and the manager's own "+
			"egress gate closed for the demo's lifetime", got)
	}
	// And the gate is put back exactly as it was found.
	a.exitEditorPreview()
	if a.d.Manager.Offline() {
		t.Error("the demo left the manager offline — a preview taken from a live session would silently stop streaming assets")
	}
}

// TestPreviewRestoresARehearsalSessionsOfflineGate is the other half of the
// bracket: entering a preview from inside rehearsal mode (which is ALREADY
// offline) must not re-open the network on the way out.
func TestPreviewRestoresARehearsalSessionsOfflineGate(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	a.d.Manager.SetOffline(true) // as startRehearsal leaves it
	enterPreviewByButton(t, a)
	driveFrames(a, 3)
	pressEscFrame(a)
	if a.editorPreviewOn() {
		t.Fatal("Esc did not leave the demo")
	}
	if !a.d.Manager.Offline() {
		t.Error("leaving the demo re-opened the network on a session that was offline before it — " +
			"a save/restore pair that assumes the prior state instead of reading it")
	}
	a.d.Manager.SetOffline(false)
}

// stageCountingFetcher rebuilds the App's Manager around the canary, keeping every
// other dependency the frame harness built. It is the production constructor with
// one substituted collaborator — nothing here re-implements the manager.
func stageCountingFetcher(t *testing.T, a *App) *countingFetcher {
	t.Helper()
	canary := &countingFetcher{}
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
	pool := network.NewPool(frameHarnessPoolWorkers)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(frameHarnessPoolWorkers)
	t.Cleanup(decoder.Close)
	// The production constructor with ONE substituted collaborator. Streaming mode
	// (not LocalMode), because that is the mode a real install runs in and the mode
	// whose egress the offline gate stands in front of.
	a.d.Manager = assets.NewManager(assets.ManagerDeps{
		Resolver: resolver,
		Prefs:    a.d.Prefs,
		T2:       t2,
		Disk:     disk,
		Source:   canary,
		Pool:     pool,
		Decoder:  decoder,
	})
	return canary
}

// ---------------------------------------------------------------------------
// (5) The pacing census, taken and released
// ---------------------------------------------------------------------------

// TestEditorPreviewRidesAndReleasesThePacingCensus pins requirement 5, both
// halves. While the demo animates it must mark the anim-chrome census every frame
// (the editor's screen is skippable — expScreenSkippable returns true for it — so
// without the census the crawl would run at the idle rate). And leaving must stop
// marking it, or the client burns the active cap forever on a still editor.
func TestEditorPreviewRidesAndReleasesThePacingCensus(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	enterPreviewByButton(t, a)
	driveFrames(a, 3)
	if !a.drawnAnimChrome {
		t.Fatal("a driven demo does not mark the pacing census — the crawl would be paced at the idle rate")
	}
	pressEscFrame(a)
	// One more frame so the exit's own frame cannot be the one being read.
	driveFrames(a, 2)
	if a.drawnAnimChrome {
		t.Error("the census is still marked after leaving the demo — the editor would hold the active cap " +
			"forever on a screen where a still theme is a still picture")
	}
}

// ---------------------------------------------------------------------------
// (6) Reduce motion freezes it
// ---------------------------------------------------------------------------

// TestReduceMotionFreezesTheEditorPreview pins requirement 6. Frozen means the
// scene is still STAGED — the plate is filled, the banner has its title, the theme
// is fully previewable — and simply does not move, which is what the accessibility
// setting asks for. It must also stop marking the pacing census: a frozen
// animation that still held the frame rate up would be the worst of both.
func TestReduceMotionFreezesTheEditorPreview(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	a.d.Prefs.SetReduceMotion(true)
	defer a.d.Prefs.SetReduceMotion(false)

	enterPreviewByButton(t, a)
	sc := &a.te.preview.state.room.Scene
	if sc.ShownameText != editorPreviewShowname || sc.MusicTrack != editorPreviewTrack {
		t.Fatal("the frozen demo staged nothing — reduce motion must still show the theme, just still")
	}
	driveFrames(a, previewDriveFrames)
	if sc.VisibleRunes != 0 {
		t.Errorf("the typewriter advanced to %d runes under reduce motion — the demo is a self-driven "+
			"animation the user asked not to have", sc.VisibleRunes)
	}
	if a.drawnAnimChrome {
		t.Error("a frozen demo still marks the pacing census — nothing is moving for it to pace")
	}
}

// ---------------------------------------------------------------------------
// (7) Both doors out, with the selection restored
// ---------------------------------------------------------------------------

// TestBothDoorsLeavePreviewWithTheSelectionRestored pins requirement 7. Esc and
// the button are the same door (exitEditorPreview), and both must put the
// selection back — a preview that silently deselected your work would be an
// expensive button to press.
//
// The Esc half also pins the RUNG ORDER: the press must be eaten by the preview
// and must not fall through to the editor's own ladder, which would arm the
// discard chip (or leave the editor) on a document the user cannot even see.
func TestBothDoorsLeavePreviewWithTheSelectionRestored(t *testing.T) {
	for _, door := range []struct {
		name  string
		leave func(a *App)
	}{
		{"Esc", pressEscFrame},
		{"the button", func(a *App) {
			x, y := frameHarnessRectCentre(editorPreviewBtnRect(frameHarnessW))
			driveClickAt(a, x, y)
		}},
	} {
		t.Run(door.name, func(t *testing.T) {
			a, cleanup := stageEditorApp(t)
			defer cleanup()
			driveFrame(a)
			tgt := elementTarget(len(a.themeSidecar.Elements) - 1)
			a.te.selectOnly(tgt)
			enterPreviewByButton(t, a)
			if a.te.selN != 0 {
				t.Fatal("the demo kept the selection live — the outlines it hides are drawn from it")
			}
			driveFrames(a, 3)

			door.leave(a)
			if a.editorPreviewOn() {
				t.Fatalf("%s did not leave the demo", door.name)
			}
			if a.te == nil {
				t.Fatalf("%s left the EDITOR, not the demo — the Esc press fell through the preview rung", door.name)
			}
			if a.te.selN != 1 || a.te.sel[0] != tgt {
				t.Errorf("%s came back with %d selected target(s) (%v), want the one that was selected before "+
					"(%v)", door.name, a.te.selN, a.te.sel[:a.te.selN], tgt)
			}
			if !a.te.exitAt.IsZero() {
				t.Errorf("%s armed the editor's discard chip — the press was consumed twice", door.name)
			}
			driveFrame(a) // and the frame after the exit is an ordinary editing frame
			if a.screen != ScreenThemeEditor {
				t.Errorf("leaving the demo landed on screen %d, want the editor (%d)", a.screen, ScreenThemeEditor)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// (8) A brand-new BLANK theme, through the real New-theme flow
// ---------------------------------------------------------------------------

// TestBlankThemeFromTheRealNewFlowCanPreview pins requirement 8, end to end and
// with nothing faked: startThemeCreateFromTemplate(templateBlank, …) is the
// Settings row's own call, pollThemeCreate lands it from inside App.Frame, and the
// editor arms itself over the applied theme. Only then is Preview pressed.
//
// It is the case most likely to break, because a blank theme has NO
// courtroom_design.ini: the courtroom falls to the classic layout, and a demo that
// only worked over themed geometry would look perfect in every other gate here.
func TestBlankThemeFromTheRealNewFlowCanPreview(t *testing.T) {
	a, cleanup := stageFrameDrivenApp(t)
	defer cleanup()
	driveFrame(a)
	_ = seedWritableThemeRootForTest(t, a)

	sc, err := newThemeSidecarFrom(templateBlank, "Field Batch Nine Blank", theme.PresetPair{})
	if err != nil {
		t.Fatalf("the blank template built no model: %v", err)
	}
	if !a.startThemeCreate("Field Batch Nine Blank", sc, themeCreateThenOpen) {
		t.Fatal("startThemeCreate refused the blank theme — the real New-theme flow is unreachable from this fixture")
	}
	// REAL FRAMES land it: pollThemeCreate is App.Frame's own call.
	for i := 0; i < frameHarnessApplyFrames && a.themeCreate != nil; i++ {
		driveFrame(a)
	}
	if a.themeCreate != nil {
		t.Fatal("the create never landed")
	}
	// The landing applies the theme and arms the editor; drive until it is open.
	for i := 0; i < frameHarnessApplyFrames && a.te == nil; i++ {
		driveFrame(a)
	}
	if a.te == nil {
		t.Fatal("the real New-theme flow did not open the editor over the theme it just created")
	}
	if len(a.themeSidecar.Elements) != 0 {
		t.Fatalf("the created theme carries %d elements — this is meant to be the BLANK template",
			len(a.themeSidecar.Elements))
	}

	enterPreviewByButton(t, a)
	driveFrames(a, previewDriveFrames)
	if !a.editorPreviewOn() {
		t.Fatal("the demo left on its own over a blank theme")
	}
	sceneNow := &a.te.preview.state.room.Scene
	if sceneNow.VisibleRunes <= 0 {
		t.Error("the demo never crawled a rune over a brand-new blank theme — the scene is not playing")
	}
	if sceneNow.ShownameText != editorPreviewShowname {
		t.Errorf("the blank theme's demo shows showname %q, want %q", sceneNow.ShownameText, editorPreviewShowname)
	}
	if a.screen != ScreenThemeEditor {
		t.Errorf("previewing a blank theme left the app on screen %d, want the editor (%d)", a.screen, ScreenThemeEditor)
	}
}

// seedWritableThemeRootForTest points BOTH halves of the real create flow at a
// temp folder: the catalog snapshot startThemeCreate reads to decide where this
// install may write, and the preferences' custom theme root the subsequent apply
// resolves the new folder under.
//
// Nothing about the create is faked — the funnel, the goroutine, themepack's own
// CreateFromSidecar, the landing and the apply all run. What is substituted is a
// machine fact (where this install's themes live), exactly as editorFixtureCatalog
// substitutes it for the save gates.
func seedWritableThemeRootForTest(t *testing.T, a *App) string {
	t.Helper()
	root := t.TempDir()
	name, _ := a.d.Prefs.Theme()
	a.d.Prefs.SetTheme(name, root)
	deadline := time.Now().Add(editorGateSaveWait)
	for a.themeCatBusy.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	a.themeCat.Store(&themeCatalog{WriteRoot: root})
	return root
}

// ---------------------------------------------------------------------------
// (9) Typing and clicking during the demo mutate NOTHING
// ---------------------------------------------------------------------------

// themeDocFingerprint is the document's whole observable content plus its
// timeline: the sidecar serialized by the SAME writer a Save uses, and the ring's
// position. Two runs of this either agree byte for byte or the document moved.
func themeDocFingerprint(t *testing.T, a *App) string {
	t.Helper()
	b, err := a.te.doc.side.Bytes()
	if err != nil {
		t.Fatalf("serialize the theme document: %v", err)
	}
	r := &a.te.ring
	return string(b) + "\x00ring:" + itoa(r.n) + "/" + itoa(r.applied) + "/" + itoa(int(r.nextGroup)) +
		"\x00dirty:" + boolWord(a.te.doc.dirty)
}

// TestPreviewIgnoresAKeyAndClickStorm pins requirement 9 through the REAL event
// path: SDL keys and Ctrl chords folded in by Ctx.HandleEvent, text input, and
// clicks all over the window, every one of them delivered to App.Frame.
//
// The chords matter most. Ctrl+Z / Ctrl+Y are claimed PRE-SCREEN (app.go, gated on
// themeEditorOpen), so they are the one input that reaches the document without
// passing through any surface the demo hides — a preview that only hid the rails
// would silently rewind the author's work while they watched a demo.
func TestPreviewIgnoresAKeyAndClickStorm(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0}, [2]int{200, 0})
	defer cleanup()
	tgt := tgts[0]
	// A real edit first, so there IS a timeline to damage: an undo that fired would
	// change the fingerprint, and so would a nudge.
	base, ok := a.te.doc.read(tgt, editFieldZ)
	if !ok {
		t.Fatal("the fixture element has no z to edit")
	}
	if !a.editorApply(mutateElemField(tgt, editFieldZ, editValueInt(base.i[0]+4))) {
		t.Fatal("the fixture edit did nothing")
	}
	a.te.selectOnly(tgt)
	driveFrame(a)

	enterPreviewByButton(t, a)
	driveFrames(a, 3)
	want := themeDocFingerprint(t, a)

	// The storm. Arrows are the editor's nudge, Delete is its delete, Ctrl+Z/Y its
	// timeline, and the clicks sweep the whole window including both rails and the
	// canvas.
	for _, k := range []sdl.Keycode{sdl.K_LEFT, sdl.K_RIGHT, sdl.K_UP, sdl.K_DOWN, sdl.K_DELETE, sdl.K_BACKSPACE} {
		driveKeyFrame(a, k)
	}
	driveHotkey(a, sdl.K_z)
	driveHotkey(a, sdl.K_y)
	driveTypedText(a, "hello theme editor")
	for _, pt := range [][2]int32{
		{editRailW / 2, editBannerH + editRowH},
		{frameHarnessW - editRailW/2, editBannerH + editFieldRowH},
		{frameHarnessW / 2, frameHarnessH / 2},
		{frameHarnessW / 4, frameHarnessH * 3 / 4},
		{editRowPad + 4, editBannerH / 2},
	} {
		driveClickAt(a, pt[0], pt[1])
	}
	driveFrames(a, 3)

	if !a.editorPreviewOn() {
		t.Fatal("the storm left the demo — the gate stopped measuring what it claims to")
	}
	if got := themeDocFingerprint(t, a); got != want {
		t.Error("a storm of keys and clicks during the demo MOVED the theme document. " +
			"Preview is a view state: the funnel (editorApply) and both timeline steps must refuse " +
			"while it is up, and no editing surface may be drawn under it.")
	}
}

// driveKeyFrame runs one real frame carrying a bare keysym, seeded where
// HandleEvent seeds it.
func driveKeyFrame(a *App, key sdl.Keycode) {
	a.ctx.BeginFrame(frameHarnessDt)
	a.ctx.keyPressed = key
	a.Frame(frameHarnessDt, frameHarnessW, frameHarnessH)
}

// ---------------------------------------------------------------------------
// (10) The live session is not disturbed
// ---------------------------------------------------------------------------

// TestPreviewLeavesTheLiveSessionAndItsViewportAlone pins requirement 10. The
// editor here is opened OVER a connected courtroom (the fixture has a live room
// and session), so the demo has something to disturb: it must run on its own room,
// leave the live room's scene untouched, and hand the viewport's one-shot
// callbacks back on the way out — the rebind teardownMakerPreview makes, for the
// same reason.
func TestPreviewLeavesTheLiveSessionAndItsViewportAlone(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	if a.room == nil || a.sess == nil {
		t.Skip("the fixture has no live session — there is nothing for the demo to disturb")
	}
	liveRoom := a.room
	liveSess := a.sess
	liveText := liveRoom.Scene.MessageText
	liveLog := len(a.icLog)

	enterPreviewByButton(t, a)
	if a.te.preview.state.room == liveRoom {
		t.Fatal("the demo is driving the LIVE room — a preview must own its own offline room")
	}
	if a.te.preview.state.sess == liveSess {
		t.Fatal("the demo is using the LIVE session")
	}
	driveFrames(a, previewDriveFrames)

	if a.room != liveRoom || a.sess != liveSess {
		t.Fatal("the demo pass did not put the live session back — the swap leaked")
	}
	if liveRoom.Scene.MessageText != liveText {
		t.Errorf("the live room's staged line changed to %q during the demo", liveRoom.Scene.MessageText)
	}
	if len(a.icLog) != liveLog {
		t.Errorf("the live IC log grew from %d to %d rows during the demo", liveLog, len(a.icLog))
	}

	pressEscFrame(a)
	if a.te == nil {
		t.Fatal("Esc left the editor rather than the demo")
	}
	if a.d.Viewport.OnPreanimDone == nil || a.d.Viewport.OnFrameShown == nil {
		t.Fatal("leaving the demo cleared the viewport's callbacks over a LIVE room — its preanims and " +
			"frame effects would never fire again")
	}
	// WHICH room they point at cannot be compared as a value (two method values on
	// the same method share a code pointer), so the binding is pinned where it is
	// written: the exit must name the live room, exactly as teardownMakerPreview does.
	body := funcBodyInPackage(t, "func (a *App) exitEditorPreview(")
	for _, want := range []string{"a.room.NotifyPreanimDone", "a.room.NotifyFrameShown"} {
		if !strings.Contains(body, want) {
			t.Errorf("exitEditorPreview no longer rebinds %s — the viewport would keep delivering to the "+
				"dead demo room", want)
		}
	}
	driveFrames(a, 3) // and the frames after the exit are ordinary editor frames
}

// ---------------------------------------------------------------------------
// (11) The funnel refuses, and the work that arrives meanwhile is KEPT
// ---------------------------------------------------------------------------

// TestPreviewRefusesTheMutationFunnelItself drives editorApply — the ONE place the
// screen mutates the document — while the demo is up.
//
// The storm gate above cannot cover this: every input it fires is already stopped
// at a surface the demo does not draw, so it stays green with the funnel's refusal
// deleted. The refusal is reachable all the same, by anything that lands from OFF
// the input path (the intake polls below are exactly that), and it is the last line
// between "a view state" and "a mode".
//
// The second half is what makes the first half mean something: the very same op
// lands once the demo is left, so what is being pinned is the STATE's refusal and
// not an op the document would have rejected anyway.
func TestPreviewRefusesTheMutationFunnelItself(t *testing.T) {
	a, cleanup, tgts := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	tgt := tgts[0]
	base, ok := a.te.doc.read(tgt, editFieldZ)
	if !ok {
		t.Fatal("the fixture element has no z to edit")
	}
	op := mutateElemField(tgt, editFieldZ, editValueInt(base.i[0]+7))
	driveFrame(a)

	enterPreviewByButton(t, a)
	driveFrames(a, 3)
	want := themeDocFingerprint(t, a)
	if a.editorApply(op) {
		t.Error("editorApply performed an edit while the demo was up — Preview is a view state, and a " +
			"view state that can write the document is a mode")
	}
	if got := themeDocFingerprint(t, a); got != want {
		t.Error("the theme document moved under the demo through the mutation funnel")
	}

	pressEscFrame(a)
	if a.editorPreviewOn() {
		t.Fatal("Esc did not leave the demo")
	}
	if !a.editorApply(op) {
		t.Error("the same op was refused after leaving the demo too — the refusal is the op's, not the " +
			"preview's, so this gate proves nothing about the guard")
	}
}

// TestPreviewHoldsAFinishedFontLandingInsteadOfLosingIt is the other side of that
// refusal, and it is a defect the refusal CREATED.
//
// drawThemeEditor runs the poll cluster ABOVE the preview arm on purpose (a save
// started before Preview must still land), so a face whose copy finishes during the
// demo was drained by pollEditorFont, refused by the funnel, and chipped as "the
// [fonts] table is full" — a lost file, a false reason, and a chip drawn nowhere
// the author could see it. A finished result is deferred, never dropped (hard rule
// 7's shape): the job waits and the first editing frame after the demo lands it.
func TestPreviewHoldsAFinishedFontLandingInsteadOfLosingIt(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	editorFixtureCatalog(t, a, t.TempDir(), themeOriginYours)

	src := filepath.Join(t.TempDir(), "Court Serif.otf")
	if err := os.WriteFile(src, []byte("not really a face; the LANDING is what is under test"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(a.te.doc.side.Fonts)
	if !a.editorTakeFontDrop(src) {
		t.Fatal("the open editor did not claim a dropped face")
	}
	if a.te.font == nil {
		t.Fatal("no copy was started")
	}
	// The result is taken off the channel and put back AFTER the demo is up, which is
	// the timing this is about: a face dropped a second before Preview was pressed,
	// whose copy finishes while the demo plays. Deterministic — the entering frame
	// cannot race the goroutine for it.
	if err := <-a.te.font.done; err != nil {
		t.Fatalf("the face copy failed: %v", err)
	}
	enterPreviewByButton(t, a)
	a.te.font.done <- nil
	driveFrames(a, previewDriveFrames)
	if got := len(a.te.doc.side.Fonts); got != before {
		t.Fatalf("the demo landed a [fonts] row (%d → %d) — the document moved while the author was "+
			"watching a preview", before, got)
	}
	if a.te.font == nil {
		t.Fatal("the finished copy was consumed during the demo and refused by the funnel — the dropped " +
			"face is gone, and the chip that says so blamed a table that is not full")
	}

	pressEscFrame(a)
	driveFrames(a, 3)
	fonts := a.te.doc.side.Fonts
	if len(fonts) != before+1 || fonts[len(fonts)-1].Family != "Court Serif" {
		t.Fatalf("after leaving the demo the theme carries %+v, want the deferred face landed as one more "+
			"family", fonts)
	}
	if a.te.font != nil {
		t.Error("the job is still pending after the landing — it would land a second time")
	}
}

// TestPreviewHoldsAFinishedImageIntakeInsteadOfLosingIt is the same rule on the
// intake that costs more to lose: landEditorImage's refusal arm Releases the decoded
// page, so a picture drained during the demo took its decode with it.
func TestPreviewHoldsAFinishedImageIntakeInsteadOfLosingIt(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	editorFixtureCatalog(t, a, t.TempDir(), themeOriginYours)

	srcDir := t.TempDir()
	writeThemePNG(t, srcDir, "drop.png", 8) // a REAL decodable picture: the intake decodes it
	before := len(a.te.doc.side.Media)
	if !a.editorTakeImage(filepath.Join(srcDir, "drop.png")) {
		t.Fatal("the open editor did not claim a dropped picture")
	}
	if a.te.image == nil {
		t.Fatal("no intake was started")
	}
	res := <-a.te.image.done
	if res.err != nil {
		t.Fatalf("the intake failed: %v", res.err)
	}
	// Handed back only once the demo is up — the copy finishing DURING the preview is
	// the case, and taking it off the channel first is what makes that deterministic.
	enterPreviewByButton(t, a)
	a.te.image.done <- res
	driveFrames(a, previewDriveFrames)
	if got := len(a.te.doc.side.Media); got != before {
		t.Fatalf("the demo landed a [media] row (%d → %d) — the document moved during a view state",
			before, got)
	}
	if a.te.image == nil {
		t.Fatal("the finished intake was consumed during the demo — the picture AND its decoded page are " +
			"gone, under a sentence that named a table that is not full")
	}

	pressEscFrame(a)
	driveFrames(a, 3)
	if got := len(a.te.doc.side.Media); got != before+1 {
		t.Errorf("after leaving the demo the theme declares %d images, want the deferred one landed (%d)",
			got, before+1)
	}
}

// ---------------------------------------------------------------------------
// (12) The pass does not eat the PRIMARY session's single-consumer results
// ---------------------------------------------------------------------------

// TestThePreviewPassDoesNotEatThePrimarysCharINI pins beginEditorPreviewPass's
// pinnedPass, which has nothing to do with drawing and everything to do with a
// channel.
//
// drawCourtroom drains App-WIDE single-consumer channels (pollCharINI), and the
// pass swaps the SESSION state under it — serverKey included. So a demo pass with
// pinnedPass down receives the live session's char.ini, finds res.key != a.serverKey
// (the demo's is empty), and returns: the result is consumed and thrown away, for
// good, leaving the real courtroom on "Loading emotes…" forever. Hard rule 7's own
// sentence — results are never dropped or stolen.
func TestThePreviewPassDoesNotEatThePrimarysCharINI(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	if a.room == nil || a.sess == nil {
		t.Skip("the fixture has no live session — there is no primary result for the demo to eat")
	}
	if a.charINIres == nil {
		// The hand-built fixture App carries no channels; this is newApp's own
		// construction (app.go:3309) — ONE slot, single consumer, which is exactly the
		// property that makes a stolen result unrecoverable.
		a.charINIres = make(chan charINIFetch, 1)
	}
	// A serverKey the demo cannot possibly match, and a result fetched FOR it.
	const primaryKey = "wss://preview.gate.invalid/ws"
	a.serverKey = primaryKey
	a.charINIBusy = true

	// THE RESULT ARRIVES WHILE THE DEMO IS UP, which is the whole case: the frame
	// that ENTERS the demo still draws the live courtroom under it (the press lands
	// mid-frame), and that pass is entitled to the result.
	enterPreviewByButton(t, a)
	a.emotes = nil
	a.charINIres <- charINIFetch{key: primaryKey, ini: &courtroom.CharINI{
		Emotes: []courtroom.Emote{{Comment: "pinnedpass", Anim: "pinnedpass", Preanim: "-"}},
	}}
	driveFrames(a, previewDriveFrames)
	if len(a.charINIres) != 1 {
		t.Fatal("the demo pass drained the primary session's char.ini result. The pass is view-only: it " +
			"must raise pinnedPass, or every poll drawCourtroom makes on an App-wide channel steals a " +
			"result that belongs to the live courtroom")
	}
	if len(a.emotes) != 0 {
		t.Errorf("the demo pass applied the primary's emote list (%d emotes) to the App", len(a.emotes))
	}

	pressEscFrame(a)
	driveFrames(a, 3)
	if len(a.emotes) != 1 || a.emotes[0].Anim != "pinnedpass" {
		t.Errorf("after the demo the live session holds %d emote(s) (%v), want the char.ini result it was "+
			"waiting for — a dropped result never comes back", len(a.emotes), a.emotes)
	}
	if a.charINIBusy {
		t.Error("the live session is still waiting for a char.ini that has already landed")
	}
}

// ---------------------------------------------------------------------------
// (13) The demo's own textures are freed on the way out
// ---------------------------------------------------------------------------

// TestLeavingPreviewFreesTheDemosChatTextures pins the release half of the swap.
//
// The demo draws through the real chatbox painter, so it builds its OWN
// render.MessageRaster into its own sessionState; endEditorPreviewPass parks it back
// into the preview, and exitEditorPreview then drops the pointer. Without a Destroy
// there, every sdl.Texture that raster holds leaks — one raster's worth per Preview
// press, unbounded across a session, on a 256 MiB budget (hard rule 4). clearSplit
// already does exactly this for the pinned tab's parked state; the swap was copied
// from there and the release has to come with it.
func TestLeavingPreviewFreesTheDemosChatTextures(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	live := a.msRaster

	enterPreviewByButton(t, a)
	driveFrames(a, previewDriveFrames)
	demo := a.te.preview.state.msRaster
	if demo == nil {
		t.Skip("this build's demo pass rasterised no chat text — there is nothing to leak, so the gate " +
			"would pass for the wrong reason")
	}
	if demo == live {
		t.Fatal("the demo drew into the LIVE session's chat raster — the swap is not isolating the pass")
	}
	if demo.Lines() == 0 {
		t.Fatal("the demo's raster holds no line textures — nothing measurable would leak")
	}

	pressEscFrame(a)
	if a.editorPreviewOn() {
		t.Fatal("Esc did not leave the demo")
	}
	if n := demo.Lines(); n != 0 {
		t.Errorf("the demo's chat raster still holds %d line texture(s) after the exit — they are "+
			"unreachable now, so that is one raster's worth of SDL textures leaked per Preview press", n)
	}
	// And the LIVE session's raster is untouched: the exit frees the demo's textures,
	// not the ones the courtroom behind it is still drawing with.
	if live != nil && live.Lines() == 0 {
		t.Error("the exit destroyed the LIVE session's chat raster — the courtroom behind the editor " +
			"would draw a blank chatbox under a showname that still paints")
	}
}

// ---------------------------------------------------------------------------
// Package-scan helpers
// ---------------------------------------------------------------------------

// funcsContainingInPackage names every PRODUCTION function in this package whose
// body contains lit, sorted. The twin of funcBodyInPackage (theme_roots_test.go):
// that one asks "does this function still call X", this one asks "who else does",
// which is the question a one-construction-site gate is made of.
func funcsContainingInPackage(t *testing.T, lit string) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []string
	files := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name == nil || fn.Body == nil {
				continue
			}
			from, to := fset.Position(fn.Body.Pos()).Offset, fset.Position(fn.Body.End()).Offset
			if from >= 0 && to <= len(src) && strings.Contains(string(src[from:to]), lit) {
				out = append(out, fn.Name.Name)
			}
		}
	}
	if files == 0 {
		t.Fatal("the scan found no production files — this gate is vacuous")
	}
	sort.Strings(out)
	return out
}

// decodedPixelForTest is the smallest thing the texture store will take: one
// opaque pixel, one frame. Enough to make a theme stem RESIDENT so a draw path
// gated on themePage() runs for real.
func decodedPixelForTest() *assets.Decoded {
	return &assets.Decoded{
		Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, 1, 1))},
		Delays: []time.Duration{0},
		Width:  1, Height: 1,
	}
}

// ---------------------------------------------------------------------------
// Rule 11 — the seam's encapsulation gates
// ---------------------------------------------------------------------------

// TestEditorPreviewHasExactlyOneConstructionSite is the seam's first gate: the
// preview state may be born in exactly one function.
//
// Same shape and same reason as TestRoomConstructionHasExactlyOneCallSite: a state
// that can be constructed two ways will one day be constructed half-wired — this
// one owns an offline session, a room, the viewport's callbacks, a saved selection
// and the manager's network gate, and a second birth site that forgot any of them
// would leak that thing for the rest of the session.
func TestEditorPreviewHasExactlyOneConstructionSite(t *testing.T) {
	const wantFunc = "enterEditorPreview"
	sites := funcsContainingInPackage(t, "&editorPreview{")
	if len(sites) != 1 || sites[0] != wantFunc {
		t.Fatalf("editorPreview is constructed in %v, want exactly [%s] — every entry into the demo must go "+
			"through the one door that saves the selection, closes the panels, binds the viewport and "+
			"brackets the manager's network gate", sites, wantFunc)
	}
}

// TestEditorPreviewHasExactlyOneReleaseSite is the same gate on the way out. The
// exit restores the selection, the viewport bindings and the network gate; a
// second site that dropped the pointer directly would leave all three behind.
func TestEditorPreviewHasExactlyOneReleaseSite(t *testing.T) {
	const wantFunc = "exitEditorPreview"
	sites := funcsContainingInPackage(t, "a.te.preview = nil")
	if len(sites) != 1 || sites[0] != wantFunc {
		t.Fatalf("the preview pointer is released in %v, want exactly [%s] — a release that skipped the "+
			"restore is how a preview leaves the client offline", sites, wantFunc)
	}
}

// TestEditorPreviewSeamIsWiredEverywhereItHasToBe is the census, and it is the
// TestThemeApplyRunsTheOverridesTier model applied here: a seam can be perfectly
// written, perfectly unit-tested and still never run. These are the four call
// sites that make the demo a feature rather than a type, each named with what its
// deletion would look like on screen.
func TestEditorPreviewSeamIsWiredEverywhereItHasToBe(t *testing.T) {
	for _, w := range []struct {
		fn, call, breaks string
	}{
		{"drawEditorHeader", "editorPreviewBtnRect(w), editorPreviewEnterLabel",
			"the button disappears from the band and the demo is unreachable"},
		{"drawThemeEditor", "a.drawEditorPreviewScreen(w, h)",
			"the demo never draws and the rails stay up over it"},
		{"Frame", "a.driveEditorPreview(dt)",
			"the demo freezes: no crawl, no blips, no marquee, no census"},
		{"drawEditorPreviewCanvas", "a.drawCourtroom(w, h)",
			"the demo stops being the REAL courtroom and becomes a mock"},
		{"renderViewportZoomed", "a.drawEditorPreviewFigure(vp)",
			"the demo's stage is empty — no stand-in where a character would stand"},
	} {
		body := funcBodyInPackage(t, funcDeclFor(w.fn))
		if !strings.Contains(body, w.call) {
			t.Errorf("%s no longer calls %s — %s", w.fn, w.call, w.breaks)
		}
	}
	// The Esc rung is inside App.Frame's ladder, and its ORDER is the property:
	// the preview arm has to be reached before the rungs that close panels, clear
	// the selection or leave the editor.
	frame := funcBodyInPackage(t, funcDeclFor("Frame"))
	rung := strings.Index(frame, "case a.editorPreviewOn():\n\t\t\t\t\ta.exitEditorPreview()")
	if rung < 0 {
		t.Fatal("app.go's ScreenThemeEditor Esc arm has no preview rung — Esc in the demo would fall " +
			"through and arm the discard chip on a document the user cannot see")
	}
	for _, later := range []string{"a.closeEditorPanels()", "a.te.selectClear()", "a.requestEditorExit()"} {
		at := strings.Index(frame, later)
		if at >= 0 && at < rung {
			t.Errorf("the Esc ladder reaches %s before the preview rung — the demo would not be the "+
				"first thing Esc leaves", later)
		}
	}
}

// funcDeclFor spells one function's declaration for funcBodyInPackage. App.Frame
// is the only one here with a different receiver shape, so it is named rather than
// pattern-matched.
func funcDeclFor(name string) string {
	if name == "Frame" {
		return "func (a *App) Frame("
	}
	return "func (a *App) " + name + "("
}

// TestEditorPreviewPassIsAlwaysPaired is the swap's own encapsulation gate. Being
// the demo session is a BRACKET: every beginEditorPreviewPass must have an
// endEditorPreviewPass, or the App is left pointing at a session that no longer
// exists — a live courtroom rendering a dead room, which is the worst failure this
// feature can produce.
func TestEditorPreviewPassIsAlwaysPaired(t *testing.T) {
	opens := funcsContainingInPackage(t, "a.beginEditorPreviewPass()")
	closes := funcsContainingInPackage(t, "a.endEditorPreviewPass(")
	if len(opens) == 0 {
		t.Fatal("nothing opens a preview pass — the seam has been deleted")
	}
	if strings.Join(opens, ",") != strings.Join(closes, ",") {
		t.Errorf("the preview pass is opened in %v and closed in %v — every open must be paired in its "+
			"own function, or the live session is never put back", opens, closes)
	}
}

// TestClosedPreviewCostsOneNilPointer keeps the zero-cost discipline the editor
// already runs on: with no demo up, the predicate is a nil compare and nothing on
// the frame path allocates for it.
func TestClosedPreviewCostsOneNilPointer(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	if a.editorPreviewOn() {
		t.Fatal("a freshly opened editor is already previewing")
	}
	if n := testing.AllocsPerRun(50, func() { _ = a.editorPreviewOn() }); n != 0 {
		t.Errorf("the closed-preview predicate allocates %.0f/op, want 0", n)
	}
}

// TestEditorPreviewFrameStaysInsideItsChromeBudget is the alloc gate. The demo IS
// the courtroom frame (covered at zero by the whole-screen gates) plus one button
// and the stand-in, so it joins the editor's own named chrome budget rather than
// inventing a looser one.
func TestEditorPreviewFrameStaysInsideItsChromeBudget(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	enterPreviewByButton(t, a)
	// Reduce motion for the measurement ONLY: a crawling typewriter re-rasters its
	// message every few frames by design, which is the courtroom's cost and not the
	// preview's. Frozen, the demo is a settled courtroom frame plus this feature's
	// own chrome — which is exactly what the budget is about.
	a.d.Prefs.SetReduceMotion(true)
	defer a.d.Prefs.SetReduceMotion(false)
	driveFrames(a, 5)

	if n := allocsPerFrame(editorAllocFrames, editorChromeAllocBudget, func() { driveFrame(a) }); n > editorChromeAllocBudget {
		t.Fatalf("one preview frame allocates %.0f/op, past the editor's named chrome budget of %d — "+
			"the usual cause is a string or a rect slice built inside the stand-in's band loop",
			n, editorChromeAllocBudget)
	}
	if !a.editorPreviewOn() {
		t.Fatal("the measured frames were not preview frames")
	}
}

// previewFigureBandsAreBounded pins rule 4 on the one loop this feature draws: the
// stand-in's taper is a NAMED, fixed band count, never a function of the stage size.
func TestPreviewFigureBandCountIsBounded(t *testing.T) {
	if editorPreviewFigureBandCnt <= 0 || editorPreviewFigureBandCnt > 64 {
		t.Fatalf("the stand-in draws %d bands — the count must be a small named constant (rule 4)",
			editorPreviewFigureBandCnt)
	}
	// The taper divides by a third of the band count; a count under 3 would divide
	// by zero on the very first frame the demo drew.
	if editorPreviewFigureBandCnt/3 == 0 {
		t.Fatal("editorPreviewFigureBandCnt/3 is zero — the taper would divide by zero")
	}
}

// TestPreviewDoesNotTouchTheLiveToggle is the machinery decision, pinned. Preview
// was BUILT ON the editor's view-state machinery and left the Live/Frozen freeze
// switch alone; a future refactor that folded one into the other would give one
// control two meanings.
func TestPreviewDoesNotTouchTheLiveToggle(t *testing.T) {
	a, cleanup := stageEditorApp(t)
	defer cleanup()
	driveFrame(a)
	a.setEditorLive(false)
	if a.editorLiveOn() {
		t.Fatal("fixture: the freeze switch did not take")
	}
	enterPreviewByButton(t, a)
	driveFrames(a, 3)
	if a.editorLiveOn() {
		t.Error("entering the demo re-armed the live-preview freeze switch — they are separate states")
	}
	pressEscFrame(a)
	if a.editorLiveOn() {
		t.Error("leaving the demo re-armed the freeze switch — a preview must not decide whether the canvas re-bakes")
	}
	for _, fn := range []string{"enterEditorPreview", "exitEditorPreview", "driveEditorPreview"} {
		body := funcBodyInPackage(t, "func (a *App) "+fn+"(")
		if strings.Contains(body, "livePaused") || strings.Contains(body, "setEditorLive") {
			t.Errorf("%s writes the live-preview toggle — the demo is a view state, not a re-bake policy", fn)
		}
	}
}

// previewTimingsAreSane guards the two script timings against a value that would
// make the demo useless without anybody noticing on screen.
func TestPreviewScriptTimingsAreSane(t *testing.T) {
	if editorPreviewRest <= 0 || editorPreviewRest > 30*time.Second {
		t.Errorf("editorPreviewRest is %v — a non-positive rest loops the line every frame and a long one "+
			"looks like the demo stopped", editorPreviewRest)
	}
	if len([]rune(editorPreviewLine)) < 40 {
		t.Errorf("the sample line is %d runes — too short to see the typewriter crawl at all",
			len([]rune(editorPreviewLine)))
	}
	if len(editorPreviewLog) == 0 {
		t.Error("the demo seeds no log lines — requirement 3 asks for a few")
	}
	if editorPreviewChar != "" {
		t.Errorf("the demo speaker names character %q — a named character is art off the wire, which is "+
			"exactly what requirement 4 forbids", editorPreviewChar)
	}
}
