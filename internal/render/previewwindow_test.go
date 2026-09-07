package render

import (
	"image"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// previewFixtureImage builds a small opaque RGBA fixture — a stand-in for
// what a real caller would hand SetFrame (an assets.Decoded frame).
func previewFixtureImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for p := 0; p < len(img.Pix); p += 4 {
		img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 0x40, 0x80, 0xC0, 0xFF
	}
	return img
}

// --- the "closed = free" contract -------------------------------------------

// TestPreviewWindowClosedPresentIsZeroAlloc is the structural half of the
// "closed = free" rule for the preview window — the SAME pattern as
// internal/assets/mountserve_test.go's TestNoMountsIsExactlyOneAtomicLoad for
// local mounts: with no window open, the per-frame touch point the main loop
// calls (Present) must resolve to a nil check and stop, touching no SDL call
// and allocating nothing. Deliberately needs no sdl.Init at all — that is
// the point: a closed PreviewWindow never touches SDL.
func TestPreviewWindowClosedPresentIsZeroAlloc(t *testing.T) {
	pw := NewPreviewWindow()
	if pw.IsOpen() {
		t.Fatal("a freshly constructed PreviewWindow reports open")
	}
	if id := pw.ID(); id != 0 {
		t.Fatalf("closed PreviewWindow.ID() = %d, want 0 (the sentinel eventForPreviewWindow relies on)", id)
	}
	n := testing.AllocsPerRun(1000, func() { pw.Present() })
	if n != 0 {
		t.Errorf("closed PreviewWindow.Present allocates %.1f objects/op, want 0", n)
	}
}

// BenchmarkPreviewWindowPresentClosed / BenchmarkPreviewWindowPresentOpen are
// the closed-vs-open comparison pair, modeled directly on
// internal/assets/mountserve_test.go's BenchmarkActiveMountLayerNoMounts /
// BenchmarkActiveMountLayerConfigured. Run together (same process, same
// machine) so the ratio is the meaningful number, not either absolute:
//
//	go test ./internal/render/... -run NONE -bench PreviewWindowPresent -benchmem
func BenchmarkPreviewWindowPresentClosed(b *testing.B) {
	pw := NewPreviewWindow()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pw.Present()
	}
}

func BenchmarkPreviewWindowPresentOpen(b *testing.B) {
	_, cleanup := newHeadlessRenderer(b) // sdl.Init(dummy) + a throwaway main-style window/renderer
	defer cleanup()
	pw := NewPreviewWindow()
	if err := pw.Open("bench-preview", 320, 240); err != nil {
		b.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()
	if err := pw.SetFrame(previewFixtureImage(320, 240)); err != nil {
		b.Fatalf("SetFrame: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pw.Present()
	}
}

// --- lifecycle ---------------------------------------------------------------

// TestPreviewWindowOpenCloseLifecycle drives Open/IsOpen/ID/Close through a
// real (headless, dummy-driver) second SDL window — proving two windows from
// one thread actually works on this build (measured fact 5) and that Close
// really returns the type to its documented closed state rather than a
// half-torn-down one.
func TestPreviewWindowOpenCloseLifecycle(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t) // sdl.Init(dummy) + a throwaway main-style window/renderer
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.Open("test-preview", 320, 240); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	if !pw.IsOpen() {
		t.Fatal("Open succeeded but IsOpen reports false")
	}
	if id := pw.ID(); id == 0 {
		t.Fatal("an open PreviewWindow reports id 0, indistinguishable from closed")
	}

	// A second Open replaces rather than leaks the first (idempotent-replace,
	// not additive) — the new id must still be a real, nonzero id.
	firstID := pw.ID()
	if err := pw.Open("test-preview-2", 200, 150); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if pw.ID() == 0 {
		t.Fatal("PreviewWindow after a second Open reports id 0")
	}
	_ = firstID // the two windows are independent SDL objects; only non-zero-ness is pinned here

	pw.Close()
	if pw.IsOpen() {
		t.Fatal("IsOpen still reports true after Close")
	}
	if id := pw.ID(); id != 0 {
		t.Fatalf("ID() = %d after Close, want 0", id)
	}
	// Close is idempotent.
	pw.Close()
	// Present on a closed window must never panic.
	pw.Present()
}

// TestPreviewWindowSetFrameRespectsBudget pins the "no unbounded cache" rule
// (CLAUDE.md hard rule 4): a frame that would exceed the preview's single-
// texture budget is refused with an error rather than silently uploaded, and
// a frame within budget uploads without error.
func TestPreviewWindowSetFrameRespectsBudget(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.Open("test-preview-budget", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	if err := pw.SetFrame(previewFixtureImage(64, 64)); err != nil {
		t.Fatalf("SetFrame of an in-budget frame failed: %v", err)
	}

	// 2100x2100x4 bytes ≈ 16.8 MiB, just over previewWindowTexBudgetBytes (16 MiB).
	const overW, overH = 2100, 2100
	if err := pw.SetFrame(previewFixtureImage(overW, overH)); err == nil {
		t.Fatalf("SetFrame of a %dx%d (%d MiB) frame succeeded, want a budget refusal (cap is %d MiB)",
			overW, overH, (overW*overH*4)>>20, previewWindowTexBudgetBytes>>20)
	}

	// SetFrame on a closed window (and with a nil image) is a documented
	// no-op, not a panic or an error.
	pw.Close()
	if err := pw.SetFrame(previewFixtureImage(64, 64)); err != nil {
		t.Errorf("SetFrame on a closed PreviewWindow returned %v, want nil (documented no-op)", err)
	}
}

// --- ShowAnimFrame: the animated-preview cache (v1.94.0) ---------------------

// previewAnimFillCounter returns a fill func for ShowAnimFrame that counts
// its own invocations and returns a real wxh fixture image — the exact shape
// internal/ui's real caller (readTexturePixels wrapped as a closure) hands
// ShowAnimFrame, just instrumented so the test can observe how many times
// the "expensive readback" step actually ran.
func previewAnimFillCounter(w, h int, calls *int) func() (*image.RGBA, error) {
	return func() (*image.RGBA, error) {
		*calls++
		return previewFixtureImage(w, h), nil
	}
}

// TestPreviewWindowShowAnimFrameCachesAfterFirstPass is the performance claim
// itself, pinned as a BEHAVIOR (not just a benchmark): driving a real 3-frame
// looping animation through ShowAnimFrame three full loops must call fill
// exactly 3 times total — once per DISTINCT frame ordinal, never once per
// visit. Deleting the cache (making ShowAnimFrame always call fill,
// discarding animFrames) makes fillCalls grow to 9 instead of 3 and this
// test fails; so does keying the cache by idx alone if a future edit forgets
// the base/source identity gate (that variant is TestPreviewWindow
// ShowAnimFrameInvalidatesOnBaseOrSourceChange below).
func TestPreviewWindowShowAnimFrameCachesAfterFirstPass(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.Open("test-preview-anim-cache", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	fillCalls := 0
	fill := previewAnimFillCounter(32, 32, &fillCalls)
	source := &TexturePage{} // an opaque identity token — ShowAnimFrame never inspects it

	// Three full loops through a 3-frame animation: 0,1,2 three times over.
	order := []int{0, 1, 2, 0, 1, 2, 0, 1, 2}
	for i, idx := range order {
		if err := pw.ShowAnimFrame("anim-fixture", source, idx, fill); err != nil {
			t.Fatalf("ShowAnimFrame(idx=%d) call #%d: %v", idx, i, err)
		}
	}
	if fillCalls != 3 {
		t.Fatalf("fill called %d times across %d ShowAnimFrame calls of a 3-frame loop, want exactly 3 "+
			"(one readback per DISTINCT frame, cached thereafter)", fillCalls, len(order))
	}
}

// TestPreviewWindowShowAnimFrameInvalidatesOnBaseOrSourceChange is the other
// half of the cache contract: a cached texture must never be reused across a
// DIFFERENT pick (base changed) or a re-decode/eviction of the SAME pick's
// source page (base unchanged, source pointer changed) — both are "start
// over," not "reuse slot idx." Simplifying the invalidation gate to base-only
// (dropping the source check) would make the third ShowAnimFrame call below
// a cache hit instead of a refill, and this test would fail.
func TestPreviewWindowShowAnimFrameInvalidatesOnBaseOrSourceChange(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.Open("test-preview-anim-invalidate", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	fillCalls := 0
	fill := previewAnimFillCounter(32, 32, &fillCalls)
	srcA := &TexturePage{}

	if err := pw.ShowAnimFrame("baseA", srcA, 0, fill); err != nil {
		t.Fatalf("first ShowAnimFrame: %v", err)
	}
	if fillCalls != 1 {
		t.Fatalf("fillCalls = %d after the first call, want 1", fillCalls)
	}
	// Same base, same source, same idx: a cache hit — no refill.
	if err := pw.ShowAnimFrame("baseA", srcA, 0, fill); err != nil {
		t.Fatalf("repeat ShowAnimFrame: %v", err)
	}
	if fillCalls != 1 {
		t.Fatalf("fillCalls = %d after a repeat of the same (base,source,idx), want 1 (cache hit)", fillCalls)
	}
	// A DIFFERENT base at the SAME idx must not reuse baseA's cached slot 0.
	if err := pw.ShowAnimFrame("baseB", srcA, 0, fill); err != nil {
		t.Fatalf("ShowAnimFrame after a base change: %v", err)
	}
	if fillCalls != 2 {
		t.Fatalf("fillCalls = %d after switching to a different base at the same idx, want 2 "+
			"(the old base's cache must not be reused)", fillCalls)
	}
	// The SAME base, but a NEW source identity (simulating a T1 re-decode of
	// the same asset — a fresh *TexturePage pointer) at the SAME idx must
	// also refill, not reuse baseB's slot 0.
	srcB2 := &TexturePage{}
	if err := pw.ShowAnimFrame("baseB", srcB2, 0, fill); err != nil {
		t.Fatalf("ShowAnimFrame after a source-page change: %v", err)
	}
	if fillCalls != 3 {
		t.Fatalf("fillCalls = %d after a source-page identity change at the same (base,idx), want 3 "+
			"(a re-decode must invalidate the cache, not reuse the stale slot)", fillCalls)
	}
}

// TestPreviewWindowShowAnimFrameRespectsCacheBudget pins the "no unbounded
// cache" rule (CLAUDE.md hard rule 4) for the AGGREGATE animation cache,
// mirroring TestPreviewWindowSetFrameRespectsBudget's real-oversized-input
// pattern rather than mocking the size check. Each individual frame here is
// safely under previewWindowTexBudgetBytes (the PER-FRAME cap SetFrame also
// enforces); the cap under test is previewWindowAnimCacheBudgetBytes, the
// SUM across every distinct cached frame of one pick.
func TestPreviewWindowShowAnimFrameRespectsCacheBudget(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.Open("test-preview-anim-budget", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	// 1900x1900x4 ≈ 13.77 MiB: comfortably under the 16 MiB per-frame cap, but
	// two of them (≈27.5 MiB) fit the 32 MiB aggregate cache cap while a
	// third (≈41.3 MiB total) does not.
	const w, h = 1900, 1900
	fillCalls := 0
	fill := previewAnimFillCounter(w, h, &fillCalls)
	source := &TexturePage{}

	for idx := 0; idx < 3; idx++ {
		if err := pw.ShowAnimFrame("big-anim", source, idx, fill); err != nil {
			t.Fatalf("ShowAnimFrame(idx=%d): %v (want no error — an over-cap frame degrades, it does not fail)", idx, err)
		}
	}
	if fillCalls != 3 {
		t.Fatalf("fillCalls = %d after the first visit to 3 distinct frames, want 3", fillCalls)
	}

	// idx 0 and 1 together (~27.5 MiB) fit under the cache budget and must
	// stay cached: revisiting them must NOT call fill again.
	if err := pw.ShowAnimFrame("big-anim", source, 0, fill); err != nil {
		t.Fatalf("ShowAnimFrame(idx=0) revisit: %v", err)
	}
	if err := pw.ShowAnimFrame("big-anim", source, 1, fill); err != nil {
		t.Fatalf("ShowAnimFrame(idx=1) revisit: %v", err)
	}
	if fillCalls != 3 {
		t.Fatalf("fillCalls = %d after revisiting the two IN-BUDGET frames, want 3 (both must stay cached)", fillCalls)
	}

	// idx 2 pushed the running total over the 32 MiB cache cap, so it must
	// NOT have been retained: every revisit calls fill again — graceful
	// degrade (still shows the correct frame, still bounded — never grows
	// the cache past its cap) rather than an error or an unbounded cache.
	if err := pw.ShowAnimFrame("big-anim", source, 2, fill); err != nil {
		t.Fatalf("ShowAnimFrame(idx=2) revisit: %v", err)
	}
	if fillCalls != 4 {
		t.Fatalf("fillCalls = %d after revisiting the OVER-BUDGET frame once more, want 4 "+
			"(it must not have been cached — every visit re-reads it)", fillCalls)
	}
	if err := pw.ShowAnimFrame("big-anim", source, 2, fill); err != nil {
		t.Fatalf("ShowAnimFrame(idx=2) second revisit: %v", err)
	}
	if fillCalls != 5 {
		t.Fatalf("fillCalls = %d after a second revisit of the over-budget frame, want 5", fillCalls)
	}
}

// TestPreviewWindowShowAnimFrameClosedIsNoOp mirrors SetFrame's own
// documented "closed = free, no-op" contract: a closed window must not call
// fill at all, and must not error.
func TestPreviewWindowShowAnimFrameClosedIsNoOp(t *testing.T) {
	pw := NewPreviewWindow()
	fillCalls := 0
	fill := previewAnimFillCounter(32, 32, &fillCalls)
	if err := pw.ShowAnimFrame("base", &TexturePage{}, 0, fill); err != nil {
		t.Errorf("ShowAnimFrame on a closed PreviewWindow returned %v, want nil (documented no-op)", err)
	}
	if fillCalls != 0 {
		t.Errorf("fill was called %d times on a closed PreviewWindow, want 0", fillCalls)
	}
}

// BenchmarkPreviewWindowShowAnimFrameCacheHit / ...CacheMiss are the
// steady-state-vs-refill comparison the task's benchmark requirement asks
// for, modeled on BenchmarkPreviewWindowPresentClosed/Open: run together so
// the RATIO (a cache hit must be dramatically cheaper than a fill+upload) is
// the meaningful evidence, not either absolute.
//
//	go test ./internal/render/... -run NONE -bench PreviewWindowShowAnimFrame -benchmem
func BenchmarkPreviewWindowShowAnimFrameCacheHit(b *testing.B) {
	_, cleanup := newHeadlessRenderer(b)
	defer cleanup()
	pw := NewPreviewWindow()
	if err := pw.Open("bench-preview-anim-hit", 320, 240); err != nil {
		b.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	source := &TexturePage{}
	fill := func() (*image.RGBA, error) { return previewFixtureImage(320, 240), nil }
	const frames = 3
	for i := 0; i < frames; i++ {
		if err := pw.ShowAnimFrame("bench-anim", source, i, fill); err != nil {
			b.Fatalf("warmup ShowAnimFrame(idx=%d): %v", i, err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pw.ShowAnimFrame("bench-anim", source, i%frames, fill)
	}
}

func BenchmarkPreviewWindowShowAnimFrameCacheMiss(b *testing.B) {
	_, cleanup := newHeadlessRenderer(b)
	defer cleanup()
	pw := NewPreviewWindow()
	if err := pw.Open("bench-preview-anim-miss", 320, 240); err != nil {
		b.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	fill := func() (*image.RGBA, error) { return previewFixtureImage(320, 240), nil }

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A fresh source EVERY call forces a cache invalidation (and
		// therefore a fill+upload) on every single ShowAnimFrame call — the
		// worst case the cache is meant to avoid in steady state.
		_ = pw.ShowAnimFrame("bench-anim-miss", &TexturePage{}, 0, fill)
	}
}

// TestPreviewWindowCloseEventClosesItself pins HandleEvent's entire
// contract: a WINDOWEVENT_CLOSE closes the window. cmd/asyncao's
// eventForPreviewWindow/dispatchEvent own deciding WHETHER an event belongs
// to this window; this only pins what happens once one arrives.
func TestPreviewWindowCloseEventClosesItself(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.Open("test-preview-close", 64, 64); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}

	pw.HandleEvent(&sdl.WindowEvent{Type: sdl.WINDOWEVENT, Event: sdl.WINDOWEVENT_CLOSE, WindowID: pw.ID()})
	if pw.IsOpen() {
		t.Fatal("PreviewWindow stayed open after its own WINDOWEVENT_CLOSE")
	}
}

// --- position/size persistence (requirement 4/5) -----------------------------

// TestPreviewWindowOpenAtPlacesTheWindow pins OpenAt's whole contract: the
// window lands at the requested desktop position, not wherever the window
// manager would have chosen. This is the primitive requirement 4's "reopens
// where the user left it" is built on.
func TestPreviewWindowOpenAtPlacesTheWindow(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.OpenAt("test-preview-openat", 111, 222, 320, 240); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	defer pw.Close()

	x, y, ok := pw.Position()
	if !ok {
		t.Fatal("Position() ok=false on a freshly opened window")
	}
	if x != 111 || y != 222 {
		t.Errorf("Position() = (%d,%d), want (111,222) — OpenAt did not place the window", x, y)
	}
	w, h, ok := pw.Size()
	if !ok || w != 320 || h != 240 {
		t.Errorf("Size() = (%d,%d,ok=%v), want (320,240,true)", w, h, ok)
	}
}

// TestPreviewWindowPositionSizeClosedReportNotOK pins the "closed reports
// nothing" half of Position/Size/LastRect — a caller must be able to tell
// "no window" apart from a legitimate (0,0) rect without a separate bool of
// its own.
func TestPreviewWindowPositionSizeClosedReportNotOK(t *testing.T) {
	pw := NewPreviewWindow()
	if _, _, ok := pw.Position(); ok {
		t.Error("Position() ok=true on a never-opened window")
	}
	if _, _, ok := pw.Size(); ok {
		t.Error("Size() ok=true on a never-opened window")
	}
	if _, _, _, _, ok := pw.LastRect(); ok {
		t.Error("LastRect() ok=true on a window that has never been closed")
	}
}

// TestPreviewWindowLastRectSurvivesItsOwnClose is the exact mechanism
// requirement's "closing the detached window with its own X" edge case
// depends on: Close() must capture the final position/size BEFORE it
// destroys the SDL window, because Position()/Size() report nothing once
// closed. Without this, internal/ui would have no way to learn (and
// persist) where the user left a window they closed via its titlebar X
// rather than the in-app pop-out toggle.
func TestPreviewWindowLastRectSurvivesItsOwnClose(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	pw := NewPreviewWindow()
	if err := pw.OpenAt("test-preview-lastrect", 50, 60, 300, 200); err != nil {
		t.Skipf("preview window unavailable: %v", err)
	}
	pw.Close()

	x, y, w, h, ok := pw.LastRect()
	if !ok {
		t.Fatal("LastRect() ok=false right after Close")
	}
	if x != 50 || y != 60 || w != 300 || h != 200 {
		t.Errorf("LastRect() = (%d,%d,%d,%d), want (50,60,300,200)", x, y, w, h)
	}
	// Still readable after the window is gone (this is the whole point).
	if pw.IsOpen() {
		t.Fatal("PreviewWindow reports open right after Close")
	}
}

// --- ClampPositionToDisplays (requirement 5) ---------------------------------

// TestClampPositionToDisplaysLeavesAnOnScreenPositionAlone pins the common
// case: a position genuinely on a connected display must round-trip
// unchanged, or every ordinary re-detach would get silently recentred.
func TestClampPositionToDisplaysLeavesAnOnScreenPositionAlone(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	// The dummy driver reports exactly one display at (0,0,1024,768).
	cx, cy := ClampPositionToDisplays(100, 100, 320, 240)
	if cx != 100 || cy != 100 {
		t.Errorf("ClampPositionToDisplays(100,100,...) = (%d,%d), want unchanged (100,100)", cx, cy)
	}
}

// TestClampPositionToDisplaysRecentresAnUnreachablePosition is requirement 5
// itself: a saved position that lands on no currently connected display
// (the monitor it was saved on is gone) must not strand the window — it
// recentres on the primary display instead.
func TestClampPositionToDisplaysRecentresAnUnreachablePosition(t *testing.T) {
	_, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	const w, h = 320, 240
	// Far outside the dummy driver's single 1024x768 display.
	cx, cy := ClampPositionToDisplays(50000, 50000, w, h)
	wantX, wantY := int32((1024-w)/2), int32((768-h)/2)
	if cx != wantX || cy != wantY {
		t.Errorf("ClampPositionToDisplays(50000,50000,...) = (%d,%d), want the primary-display centre (%d,%d)", cx, cy, wantX, wantY)
	}
}

// --- previewFitRect (aspect-preserving, non-stretch Present) -----------------

func TestPreviewFitRectPreservesAspectAndCentres(t *testing.T) {
	cases := []struct {
		name                   string
		winW, winH, srcW, srcH int32
		wantW, wantH           int32
	}{
		{"wide window, square art: height-bound", 400, 100, 100, 100, 100, 100},
		{"tall window, square art: width-bound", 100, 400, 100, 100, 100, 100},
		{"exact match", 200, 100, 200, 100, 200, 100},
		{"2:1 art in a square window: width-bound", 200, 200, 200, 100, 200, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := previewFitRect(c.winW, c.winH, c.srcW, c.srcH)
			if got.W != c.wantW || got.H != c.wantH {
				t.Fatalf("previewFitRect(%d,%d,%d,%d) size = %dx%d, want %dx%d",
					c.winW, c.winH, c.srcW, c.srcH, got.W, got.H, c.wantW, c.wantH)
			}
			// Centred on both axes.
			if gotX, wantX := got.X, (c.winW-got.W)/2; gotX != wantX {
				t.Errorf("X = %d, want %d (centred)", gotX, wantX)
			}
			if gotY, wantY := got.Y, (c.winH-got.H)/2; gotY != wantY {
				t.Errorf("Y = %d, want %d (centred)", gotY, wantY)
			}
		})
	}
}

// TestPreviewFitRectDegenerateInputsFillTheWindow pins the fallback: a
// source with no usable size must not divide by zero or return a
// zero-area rect nobody can see — it fills the window instead.
func TestPreviewFitRectDegenerateInputsFillTheWindow(t *testing.T) {
	for _, c := range []struct{ srcW, srcH int32 }{{0, 100}, {100, 0}, {-5, 100}} {
		got := previewFitRect(320, 240, c.srcW, c.srcH)
		if got.W != 320 || got.H != 240 {
			t.Errorf("previewFitRect(320,240,%d,%d) = %+v, want the whole 320x240 window", c.srcW, c.srcH, got)
		}
	}
}
