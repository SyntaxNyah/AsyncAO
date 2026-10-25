package main

import (
	"os"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/ui"
)

// --- pure unit tests on eventForPreviewWindow --------------------------------

// TestEventForPreviewWindowRoutesOnID pins the routing predicate itself,
// independent of any real window: an event carrying the preview's id routes
// to it, one carrying any other id (including the main window's) does not,
// and a closed preview (id 0, PreviewWindow.ID's documented sentinel) never
// matches anything — every event stays on the main Ctx, exactly like before
// a preview window could exist.
func TestEventForPreviewWindowRoutesOnID(t *testing.T) {
	const previewID = uint32(7)
	ev := &sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, WindowID: previewID}

	if !eventForPreviewWindow(ev, previewID) {
		t.Error("an event carrying the preview's own id did not route to it")
	}
	if eventForPreviewWindow(ev, previewID+1) {
		t.Error("an event carrying a DIFFERENT window's id routed to the preview")
	}
	if eventForPreviewWindow(ev, 0) {
		t.Error("previewWindowID 0 (closed) must never match any event")
	}
	// The specific hazard the previewWindowID==0 guard exists for: an event
	// that itself carries WindowID 0 must never be treated as "belongs to
	// the closed preview" just because both sides happen to be the zero
	// value. Without the guard, id==previewWindowID (0==0) would be true.
	zeroIDEvent := &sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, WindowID: 0}
	if eventForPreviewWindow(zeroIDEvent, 0) {
		t.Error("an event whose own WindowID is 0 must not match a closed (id-0) preview")
	}
	// An event type with no WindowID field at all (QuitEvent) never routes,
	// however previewWindowID compares.
	if eventForPreviewWindow(&sdl.QuitEvent{Type: sdl.QUIT}, previewID) {
		t.Error("a QuitEvent (no WindowID) must never route to the preview")
	}
}

// --- real two-window integration: close-safety + PreviewWindow together -----

// newHeadlessSDLVideo initializes SDL video with the dummy driver — enough
// for sdl.CreateWindow/CreateRenderer (PreviewWindow.Open's needs) — and
// nothing else. Skips (never fails) without a video driver, per CLAUDE.md.
func newHeadlessSDLVideo(t testing.TB) func() {
	t.Helper()
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	return sdl.Quit
}

// TestPreviewWindowCloseNeverQuitsTheAppButClosesThePreview drives the THREE
// real pieces this phase's requirement 3 depends on — together, the way
// run()'s handleEv actually calls them — against a REAL second SDL window:
// mainWindowShouldQuit (close-safety phase), eventForPreviewWindow, and
// PreviewWindow.HandleEvent. It is the "WINDOWEVENT_CLOSE on the preview id
// closes ONLY the preview" requirement turned into a test that would fail if
// any one of the three regressed.
func TestPreviewWindowCloseNeverQuitsTheAppButClosesThePreview(t *testing.T) {
	cleanup := newHeadlessSDLVideo(t)
	defer cleanup()

	// A real "main" window FIRST, so mainWindowID is a genuine SDL id rather
	// than a guessed constant — in a fresh SDL session the first window
	// created is assigned id 1, and a hand-picked mainWindowID could
	// collide with whatever id the preview happens to get otherwise.
	mainWin, err := sdl.CreateWindow("test-main", 0, 0, 64, 64, sdl.WINDOW_HIDDEN)
	if err != nil {
		t.Skipf("main window unavailable: %v", err)
	}
	defer mainWin.Destroy()
	mainWindowID, err := mainWin.GetID()
	if err != nil {
		t.Fatalf("main window id: %v", err)
	}

	preview := render.NewPreviewWindow()
	if err := preview.Open("test-preview", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	if preview.ID() == mainWindowID {
		t.Fatalf("preview and main windows share id %d — the test fixture cannot tell them apart", mainWindowID)
	}

	ev := &sdl.WindowEvent{Type: sdl.WINDOWEVENT, Event: sdl.WINDOWEVENT_CLOSE, WindowID: preview.ID()}

	if mainWindowShouldQuit(ev, mainWindowID) {
		t.Fatal("closing the preview window must not signal an app quit")
	}
	if !eventForPreviewWindow(ev, preview.ID()) {
		t.Fatal("a CLOSE event carrying the preview's own id must route to the preview")
	}
	preview.HandleEvent(ev)
	if preview.IsOpen() {
		t.Fatal("PreviewWindow did not close itself on its own WINDOWEVENT_CLOSE")
	}
}

// --- the coordinate-aliasing gate, driven through the REAL dispatch ---------

// newHeadlessMainCtx builds a real ui.Ctx bound to a real (hidden, dummy-
// driver) SDL window+renderer — the same construction cmd/asyncao's run()
// performs, minus the on-screen window. Skips without SDL/SDL_ttf.
func newHeadlessMainCtx(t *testing.T) (*ui.Ctx, func()) {
	t.Helper()
	os.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	if err := ttf.Init(); err != nil {
		sdl.Quit()
		t.Skipf("SDL_ttf unavailable: %v", err)
	}
	win, err := sdl.CreateWindow("preview-route-test", 0, 0, 320, 240, sdl.WINDOW_HIDDEN)
	if err != nil {
		ttf.Quit()
		sdl.Quit()
		t.Skipf("window unavailable: %v", err)
	}
	ren, err := sdl.CreateRenderer(win, -1, sdl.RENDERER_SOFTWARE)
	if err != nil {
		win.Destroy()
		ttf.Quit()
		sdl.Quit()
		t.Skipf("software renderer unavailable: %v", err)
	}
	uiCtx, err := ui.NewCtx(ren)
	if err != nil {
		ren.Destroy()
		win.Destroy()
		ttf.Quit()
		sdl.Quit()
		t.Skipf("Ctx unavailable: %v", err)
	}
	return uiCtx, func() {
		uiCtx.Destroy()
		ren.Destroy()
		win.Destroy()
		ttf.Quit()
		sdl.Quit()
	}
}

// TestPreviewWindowMouseNeverReachesMainCtx is THE encapsulation test the
// phase brief calls for by name: feed a synthetic preview-window-tagged
// MouseMotionEvent through the REAL dispatch (dispatchEvent — the exact
// function run()'s handleEv calls, not a re-implementation of its branch)
// and assert the main Ctx's cursor state is untouched.
//
// Root cause this pins: ui.Ctx.HandleEvent writes MouseMotionEvent.X/Y
// straight into the shared c.mouseX/c.mouseY with no window-origin check of
// its own (ui.go). SDL's coordinates are window-CLIENT-relative, so without
// dispatchEvent's gate a second window's raw mouse motion would silently
// alias onto the main window's hit-testing — phantom hovers and clicks.
//
// Both directions are asserted, not just the negative: a main-window (or
// preview-closed) event MUST still reach uiCtx (the positive control — a
// dispatchEvent that dropped every event on the floor would pass a
// negative-only version of this test for the wrong reason).
func TestPreviewWindowMouseNeverReachesMainCtx(t *testing.T) {
	uiCtx, cleanupCtx := newHeadlessMainCtx(t)
	defer cleanupCtx()

	preview := render.NewPreviewWindow()
	if err := preview.Open("test-preview-aliasing", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer preview.Close()
	if preview.ID() == 0 {
		t.Fatal("an open PreviewWindow reports id 0 — cannot distinguish it from closed")
	}

	// Positive control: an event NOT carrying the preview's id (here, an
	// arbitrary third id — the same case as today's single-window client, or
	// the main window once a second window exists) must still reach uiCtx.
	uiCtx.BeginFrame(time.Millisecond)
	const someOtherWindowID = uint32(999)
	consumed := dispatchEvent(&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, WindowID: someOtherWindowID, X: 42, Y: 99}, uiCtx, preview)
	if consumed {
		t.Fatal("dispatchEvent claimed a non-preview event as consumedByPreview")
	}
	if x, y := uiCtx.MousePos(); x != 42 || y != 99 {
		t.Fatalf("a main-window mouse event did not reach uiCtx: MousePos() = %d,%d, want 42,99 "+
			"(dispatchEvent must feed non-preview events to uiCtx.HandleEvent)", x, y)
	}

	// The aliasing case: an event carrying the preview's REAL id. BeginFrame
	// reseeds mouseX/mouseY from SDL's (dummy-driver) global mouse state —
	// (0,0) — before this frame's events arrive, so a leak would visibly
	// become (777,888); anything else proves it never reached uiCtx.
	uiCtx.BeginFrame(time.Millisecond)
	if x, y := uiCtx.MousePos(); x != 0 || y != 0 {
		t.Fatalf("BeginFrame did not reseed MousePos to the dummy driver's (0,0): got %d,%d", x, y)
	}
	consumed = dispatchEvent(&sdl.MouseMotionEvent{Type: sdl.MOUSEMOTION, WindowID: preview.ID(), X: 777, Y: 888}, uiCtx, preview)
	if !consumed {
		t.Fatal("dispatchEvent did not claim an event carrying the preview's own id")
	}
	if x, y := uiCtx.MousePos(); x != 0 || y != 0 {
		t.Fatalf("preview-window mouse motion aliased onto the main Ctx: MousePos() = %d,%d, want unchanged 0,0", x, y)
	}
}
