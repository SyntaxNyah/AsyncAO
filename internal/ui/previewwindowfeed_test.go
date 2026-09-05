package ui

// The detachable emote-preview OS window's App-side wiring (v1.93.0
// preview-window-wire): the pop-out toggle, the in-app box's suppression
// while detached, the per-frame feed/reconcile (syncPreviewWindow), and the
// "boring but fatal" edge cases the phase brief names by name — detaching
// with nothing selected, detaching then disconnecting, detaching then
// switching tabs, and closing the detached window with its own titlebar X.

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// previewFeedFixtureBase is the fixture texture's key — a real URL shape is
// not needed, only a stable, unique string every helper agrees on.
const previewFeedFixtureBase = "preview-fixture://sprite"

// newPreviewFeedApp stages a real Ctx + TextureStore (headless SDL, software
// renderer) with ONE resident fixture texture under previewFeedFixtureBase,
// a real (closed) render.PreviewWindow, and real temp-file preferences —
// everything feedDetachedPreviewFrame/syncPreviewWindow actually touch.
// previewBase starts pointed at the fixture, as if a hover had already
// opened the in-app box before the user detached it.
func newPreviewFeedApp(t testing.TB) *App {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	store, err := render.NewTextureStore(ren)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for p := 0; p < len(img.Pix); p += 4 {
		img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 0x10, 0x20, 0x30, 0xFF
	}
	dec := &assets.Decoded{Frames: []*image.RGBA{img}, Delays: []time.Duration{0}, Width: 40, Height: 30}
	if err := store.Upload(previewFeedFixtureBase, dec); err != nil {
		t.Skipf("texture upload unavailable: %v", err)
	}
	prefs, err := config.New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatalf("prefs: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{ctx: ctx, activeTab: -1}
	a.d.Prefs = prefs
	a.d.Store = store
	a.d.Preview = render.NewPreviewWindow()
	t.Cleanup(a.d.Preview.Close)
	a.resetSessionState()
	a.previewBase = previewFeedFixtureBase
	return a
}

// --- requirement 1: pop-out toggle -------------------------------------------

func TestToggleDetachPreviewOpensAndReattaches(t *testing.T) {
	a := newPreviewFeedApp(t)
	if a.previewIsDetached() {
		t.Fatal("a fresh preview must start attached")
	}
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		t.Fatal("toggleDetachPreview did not open the window")
	}
	a.toggleDetachPreview()
	if a.previewIsDetached() {
		t.Fatal("a second toggle did not re-attach (close the window)")
	}
}

// TestToggleDetachPreviewWithNoEmoteSelectedIsANoOp is the phase brief's own
// "boring but fatal" case #1: detaching with nothing previewed must not open
// a window that nothing could ever fill (only feedDetachedPreviewFrame, gated
// on previewBase, ever calls SetFrame).
func TestToggleDetachPreviewWithNoEmoteSelectedIsANoOp(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.previewBase = ""
	a.toggleDetachPreview()
	if a.previewIsDetached() {
		t.Fatal("toggling with nothing previewed opened a window anyway")
	}
}

// --- requirement 2: the in-app box does not draw while detached -------------

func TestDetachedPreviewSuppressesTheInAppBox(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		t.Fatal("setup: toggle did not detach")
	}
	a.previewFrameRect = sdl.Rect{X: 1, Y: 2, W: 3, H: 4} // poison: must be cleared, not left stale
	a.drawSpritePreview(800, 600, false, "")
	if a.previewFrameRect != (sdl.Rect{}) {
		t.Errorf("drawSpritePreview left a hit rect (%+v) while detached — the in-app box must not draw at all", a.previewFrameRect)
	}
}

// TestDrawSpritePreviewReattachedDrawsAgain is the positive control for the
// test above: an early-return that fired for the WRONG reason (a bug, not
// detaching) would make TestDetachedPreviewSuppressesTheInAppBox pass
// vacuously. Re-attaching must bring the box straight back.
func TestDrawSpritePreviewReattachedDrawsAgain(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.toggleDetachPreview()
	a.toggleDetachPreview() // re-attach
	if a.previewIsDetached() {
		t.Fatal("setup: did not re-attach")
	}
	a.drawSpritePreview(800, 600, false, "")
	if a.previewFrameRect.W <= 0 || a.previewFrameRect.H <= 0 {
		t.Errorf("previewFrameRect = %+v after re-attaching with a ready sprite — the box must draw", a.previewFrameRect)
	}
}

// --- feeding the detached window ---------------------------------------------

// TestFeedDetachedPreviewFrameUploadsOnce pins the "once per pick, not once
// per render frame" contract: SetFrame's own comment says a rebuild is a
// whole GPU texture, so a steady-state loop with nothing new to show must
// stop calling it.
func TestFeedDetachedPreviewFrameUploadsOnce(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		t.Fatal("setup: toggle did not detach")
	}
	if a.previewWinFedBase != previewFeedFixtureBase {
		t.Fatalf("previewWinFedBase = %q after opening with a ready sprite, want the fixture base fed immediately", a.previewWinFedBase)
	}
	// A second feed attempt for the SAME base must be a no-op (nothing to
	// assert on the SDL side directly, but previewWinFedBase must not churn,
	// and calling it again must not panic or error out silently).
	a.feedDetachedPreviewFrame()
	if a.previewWinFedBase != previewFeedFixtureBase {
		t.Fatalf("previewWinFedBase changed on a repeat feed of the same base: %q", a.previewWinFedBase)
	}

	// A NEW pick re-feeds.
	a.previewBase = "preview-fixture://sprite-2" // not resident: feed must retry, not crash
	a.feedDetachedPreviewFrame()
	if a.previewWinFedBase == "preview-fixture://sprite-2" {
		t.Fatal("feedDetachedPreviewFrame marked a NON-resident base as fed")
	}
}

// --- requirement 4/5: position persists, and reconciles on self-close -------

// TestSyncPreviewWindowPersistsOnReattach drives requirement 4 end to end
// through the real toggle: detach, move the OS window (simulating the user
// dragging it), re-attach, and the LAST position/size must be what
// SetPreviewWindowRect actually wrote — proving the debounced-save wiring
// (not a mirror of it) fires from the real close path.
func TestSyncPreviewWindowPersistsOnReattach(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		t.Fatal("setup: toggle did not detach")
	}
	a.d.Preview.OpenAt("moved", 77, 88, 222, 111) // Open* is idempotent-replace: same window, new geometry

	a.toggleDetachPreview() // re-attach
	if a.previewIsDetached() {
		t.Fatal("setup: did not re-attach")
	}
	a.syncPreviewWindow() // the real per-frame call site notices the falling edge

	x, y, w, h, ok := a.d.Prefs.PreviewWindowRect()
	if !ok {
		t.Fatal("no preview window rect was persisted after re-attaching")
	}
	if x != 77 || y != 88 || w != 222 || h != 111 {
		t.Errorf("persisted rect = (%d,%d,%d,%d), want (77,88,222,111)", x, y, w, h)
	}
}

// TestClosingTheDetachedWindowWithItsOwnXReconciles is the phase brief's
// "boring but fatal" case #4 verbatim: closing the OS window via its own
// titlebar X (simulated the same way cmd/asyncao's dispatchEvent would —
// calling Close() DIRECTLY, never through toggleDetachPreview) must both (a)
// make previewIsDetached() — the pop-out button's ENTIRE truth — report
// false again, and (b) get its final position persisted, exactly like an
// explicit re-attach does. There is no separate "detached" bool that could
// disagree with reality here; this proves that by driving the real
// discrepancy path (Close() called out of band) and checking the single
// source of truth.
func TestClosingTheDetachedWindowWithItsOwnXReconciles(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		t.Fatal("setup: toggle did not detach")
	}
	a.d.Preview.OpenAt("moved", 40, 50, 200, 150)

	// The window closing ITSELF — e.g. cmd/asyncao's PreviewWindow.HandleEvent
	// reacting to WINDOWEVENT_CLOSE — never calls back into internal/ui.
	a.d.Preview.Close()

	if a.previewIsDetached() {
		t.Fatal("previewIsDetached() still true after the window closed itself")
	}
	a.syncPreviewWindow() // the real per-frame poll that notices the edge

	x, y, w, h, ok := a.d.Prefs.PreviewWindowRect()
	if !ok {
		t.Fatal("a self-close (titlebar X) did not persist the window's final rect")
	}
	if x != 40 || y != 50 || w != 200 || h != 150 {
		t.Errorf("persisted rect = (%d,%d,%d,%d), want (40,50,200,150)", x, y, w, h)
	}
	// The pop-out button's whole state is previewIsDetached(); drawing the
	// box must work again exactly as if the user had used the toggle.
	a.drawSpritePreview(800, 600, false, "")
	if a.previewFrameRect.W <= 0 {
		t.Error("the in-app box did not resume drawing after the window's own close")
	}
}

// --- boring but fatal: disconnect / tab switch -------------------------------

// TestDetachedPreviewSurvivesDisconnect is case #2: Disconnect() must not
// panic with a detached preview open, and must not silently close it either
// — the window is showing sprite art, not session data, and the user's whole
// point in detaching it was to keep it up regardless of what else happens in
// the app (matches the box's own PRE-EXISTING behavior: Disconnect never
// called closeSpritePreview even before this feature existed).
func TestDetachedPreviewSurvivesDisconnect(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		t.Fatal("setup: toggle did not detach")
	}

	a.Disconnect() // must not panic on a mostly-nil Deps

	if !a.previewIsDetached() {
		t.Error("Disconnect closed the detached preview window — it must survive a disconnect")
	}
}

// TestDetachedPreviewSurvivesSessionReset is case #3 (detaching then
// switching server tabs): resetSessionState is the SAME primitive
// activateTab/parkActive/Disconnect all use to install a fresh per-tab
// session. The preview window and its App-level bookkeeping
// (previewBase, previewWinFedBase, previewIsDetached) are deliberately
// App-global — matching the PRE-EXISTING precedent for previewPinned/
// previewOffX/areaPct/musicPct, all of which already survive a session swap
// by design — so resetSessionState must leave every one of them untouched:
// if the preview ever leaked INTO sessionState, switching tabs would
// silently close or blank it, exactly the class of bug "per-session state
// on App, not sessionState" guards against in the OTHER direction too.
func TestDetachedPreviewSurvivesSessionReset(t *testing.T) {
	a := newPreviewFeedApp(t)
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		t.Fatal("setup: toggle did not detach")
	}
	fedBefore := a.previewWinFedBase
	baseBefore := a.previewBase

	a.resetSessionState() // the primitive every tab switch / disconnect / new-tab path calls

	if !a.previewIsDetached() {
		t.Error("resetSessionState closed the detached preview window")
	}
	if a.previewBase != baseBefore {
		t.Errorf("previewBase changed across resetSessionState: %q -> %q", baseBefore, a.previewBase)
	}
	if a.previewWinFedBase != fedBefore {
		t.Errorf("previewWinFedBase changed across resetSessionState: %q -> %q", fedBefore, a.previewWinFedBase)
	}
}

// --- "closed = free": the real per-frame call site --------------------------

// TestSyncPreviewWindowClosedIsZeroAlloc is the phase brief's own explicit
// bar, modeled on internal/assets/mountserve_test.go's
// TestNoMountsIsExactlyOneAtomicLoad: with nothing ever detached, the REAL
// per-frame touch point handlePreviewInput calls unconditionally
// (syncPreviewWindow) must resolve to nil checks and stop — no allocation,
// no SDL call. Deliberately built with a.d.Preview as a freshly-constructed
// (never-opened) PreviewWindow, needing no SDL init at all: a closed preview
// never touches SDL, on either side of this seam.
func TestSyncPreviewWindowClosedIsZeroAlloc(t *testing.T) {
	a := &App{ctx: &Ctx{}, activeTab: -1}
	prefs, err := config.New(filepath.Join(t.TempDir(), "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a.d.Prefs = prefs
	a.d.Preview = render.NewPreviewWindow()
	a.resetSessionState()

	n := testing.AllocsPerRun(1000, func() { a.syncPreviewWindow() })
	if n != 0 {
		t.Errorf("syncPreviewWindow allocates %.1f objects/op while closed, want 0", n)
	}
}

// BenchmarkSyncPreviewWindowClosed / BenchmarkSyncPreviewWindowOpen are the
// closed-vs-open comparison pair the phase brief asks for, modeled directly
// on internal/assets/mountserve_test.go's BenchmarkActiveMountLayerNoMounts /
// BenchmarkActiveMountLayerConfigured — run together so the RATIO is the
// meaningful number:
//
//	go test ./internal/ui/... -run NONE -bench SyncPreviewWindow -benchmem
func BenchmarkSyncPreviewWindowClosed(b *testing.B) {
	a := &App{ctx: &Ctx{}, activeTab: -1}
	prefs, err := config.New(filepath.Join(b.TempDir(), "prefs.json"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = prefs.Close() })
	a.d.Prefs = prefs
	a.d.Preview = render.NewPreviewWindow()
	a.resetSessionState()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.syncPreviewWindow()
	}
}

func BenchmarkSyncPreviewWindowOpen(b *testing.B) {
	a := newPreviewFeedApp(b)
	a.toggleDetachPreview()
	if !a.previewIsDetached() {
		b.Skip("preview window unavailable")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.syncPreviewWindow()
	}
}
