package render

import (
	"image"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// streamChunk builds a single-frame streaming chunk (Stream=true) at the given
// kept-frame offset, for an animation of `source` authored frames.
func streamChunk(offset int, partial bool, source int) *assets.Decoded {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for p := 3; p < len(img.Pix); p += 4 {
		img.Pix[p] = 0xFF
	}
	return &assets.Decoded{
		Frames:       []*image.RGBA{img},
		Delays:       []time.Duration{50 * time.Millisecond},
		Animated:     true,
		SourceFrames: source,
		Width:        64,
		Height:       64,
		Stream:       true,
		FrameOffset:  offset,
		Partial:      partial,
	}
}

// finalizeChunk is the stream's closing delivery: no frames, Partial=false.
func finalizeChunk(source int) *assets.Decoded {
	return &assets.Decoded{Stream: true, Partial: false, FrameOffset: source, SourceFrames: source}
}

// TestAppendStreamGrowsResidentPage drives the streaming store seam: an
// establishing chunk parks the page in the oversized map, appends grow its
// frame set in place, and the finalize clears Partial. This is the append-vs-
// replace contract the streaming decoder depends on (rule §17.11 encapsulation).
func TestAppendStreamGrowsResidentPage(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := "srv/characters/witch/(a)normal"
	if err := store.AppendStream(base, streamChunk(0, true, 3)); err != nil {
		t.Fatalf("establish: %v", err)
	}
	page, ok := store.Get(base)
	if !ok || len(page.Frames) != 1 || !page.Partial {
		t.Fatalf("after establish: ok=%v frames=%d partial=%v, want true/1/true", ok, len(page.Frames), page.Partial)
	}

	if err := store.AppendStream(base, streamChunk(1, true, 3)); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	page, _ = store.Get(base)
	if len(page.Frames) != 2 || !page.Partial {
		t.Fatalf("after append 1: frames=%d partial=%v, want 2/true", len(page.Frames), page.Partial)
	}

	if err := store.AppendStream(base, finalizeChunk(3)); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	page, _ = store.Get(base)
	if len(page.Frames) != 2 || page.Partial || page.SourceFrames != 3 {
		t.Fatalf("after finalize: frames=%d partial=%v source=%d, want 2/false/3", len(page.Frames), page.Partial, page.SourceFrames)
	}
}

// TestAppendStreamKeepsLatchedScaleMode pins the filter regression: once a
// streamed page's filter is latched (frame 0 drawn under Nearest), frames
// appended afterward must inherit that same filter instead of keeping the
// client-wide linear hint. Without the AppendStream fix, only frame 0 reads
// back Nearest and every appended frame stays Linear — the "frame 0 crisp,
// the rest blurry" report.
func TestAppendStreamKeepsLatchedScaleMode(t *testing.T) {
	if !scaleModeSupported {
		t.Skip("SDL < 2.0.12: per-texture scale mode is compiled out by design")
	}
	// The client runs HINT_RENDER_SCALE_QUALITY="1" (linear) — cmd/asyncao/main.go
	// — so every newly created texture defaults to bilinear. Reproduce that here,
	// or the bug can't be seen: the headless SDL default is "nearest", which would
	// make this test pass even without the AppendStream fix.
	sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, "1")
	defer sdl.SetHint(sdl.HINT_RENDER_SCALE_QUALITY, "0")
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := "srv/characters/witch/(a)normal"
	if err := store.AppendStream(base, streamChunk(0, true, 3)); err != nil {
		t.Fatalf("establish: %v", err)
	}
	page, ok := store.Get(base)
	if !ok {
		t.Fatal("establish: page must be resident")
	}
	// A draw latches the filter exactly the way viewport.go does.
	page.applyScaleMode(ren, sdl.ScaleModeNearest)

	if err := store.AppendStream(base, streamChunk(1, true, 3)); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.AppendStream(base, streamChunk(2, true, 3)); err != nil {
		t.Fatalf("append: %v", err)
	}
	page, _ = store.Get(base)
	if len(page.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(page.Frames))
	}
	for i, tex := range page.Frames {
		got, ok := textureScaleMode(tex)
		if !ok {
			t.Fatalf("frame %d read back: %v", i, sdl.GetError())
		}
		if got != sdl.ScaleModeNearest {
			t.Errorf("frame %d = %d, want nearest (%d) — appended frames must inherit the latched filter", i, got, sdl.ScaleModeNearest)
		}
	}
}

// TestAppendStreamDropsOutOfOrder pins the ordering guard: an append that skips
// the next kept-frame index is stale and must be dropped, so a fuller page
// never regresses.
func TestAppendStreamDropsOutOfOrder(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, _ := NewTextureStoreBudget(ren, 16<<20)
	defer store.Purge()

	base := "srv/characters/witch/(a)normal"
	if err := store.AppendStream(base, streamChunk(0, true, 3)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendStream(base, streamChunk(2, true, 3)); err != nil {
		t.Fatal(err)
	}
	page, _ := store.Get(base)
	if len(page.Frames) != 1 {
		t.Fatalf("out-of-order append must be dropped: frames=%d, want 1", len(page.Frames))
	}
}

// TestStreamingPreanimHoldsOnPrefix pins the viewport's prefix boundary: a
// playOnce layer over a still-growing page must HOLD on its last resident
// frame (never latch finished, never wrap) until the stream completes, then
// play to its natural end.
func TestStreamingPreanimHoldsOnPrefix(t *testing.T) {
	v := NewViewport(nil)
	v.speakerAnim.reset("characters/x/intro")

	prefix := &TexturePage{
		Frames:  make([]*sdl.Texture, 2),
		Delays:  []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		Partial: true,
	}
	for i := 0; i < 5; i++ {
		if v.advanceSpeaker(prefix, 100*time.Millisecond, true) {
			t.Fatalf("a growing prefix must not fire OnPreanimDone (step %d)", i)
		}
		if v.speakerAnim.finished {
			t.Fatalf("a growing prefix must not latch finished (step %d)", i)
		}
	}
	if v.speakerAnim.frame != 1 {
		t.Fatalf("the prefix must hold on its last frame: frame=%d, want 1", v.speakerAnim.frame)
	}

	// The stream completes; the same 3-frame clip plays out from frame 1.
	full := threeFramePage()
	if v.advanceSpeaker(full, 100*time.Millisecond, true) {
		t.Fatal("full preanim completed a frame early")
	}
	if !v.advanceSpeaker(full, 100*time.Millisecond, true) {
		t.Fatal("full preanim must complete on its natural last frame")
	}
	if !v.speakerAnim.finished {
		t.Fatal("full preanim must latch finished at its natural end")
	}
}

// TestReportSpeakerFrameIdentityWhileStreaming pins the #17 frame-effect
// mapping during streaming: a growing prefix maps its kept ordinal as identity
// (no decimation happened), and only a COMPLETE page uses the decimation map.
func TestReportSpeakerFrameIdentityWhileStreaming(t *testing.T) {
	v := NewViewport(nil)
	v.speakerAnim.reset("x")
	var got int
	v.OnFrameShown = func(src int) { got = src }

	page := &TexturePage{
		Frames:       make([]*sdl.Texture, 3),
		Delays:       []time.Duration{10, 10, 10},
		SourceFrames: 100,
		Partial:      true,
	}
	v.speakerAnim.frame = 2
	v.speakerAnim.shownSrc = -1
	v.reportSpeakerFrame(page)
	if got != 2 {
		t.Fatalf("streaming identity mapping = %d, want 2", got)
	}

	page.Partial = false
	v.speakerAnim.shownSrc = -1
	v.reportSpeakerFrame(page)
	if want := assets.FrameKeepIndex(2, 100, 3); got != want {
		t.Fatalf("completed decimation mapping = %d, want %d", got, want)
	}
}

// TestAppendStreamDropsRedundantEstablish pins the crash fix: a redundant
// establishing chunk (a racing re-decode of a still-streaming base) must be
// dropped, not shrink the fuller resident page — shrinking is what left the
// viewport's playback cursor pointing past the page's frame count.
func TestAppendStreamDropsRedundantEstablish(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := "srv/characters/witch/(a)normal"
	if err := store.AppendStream(base, streamChunk(0, true, 3)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendStream(base, streamChunk(1, true, 3)); err != nil {
		t.Fatal(err)
	}
	page, _ := store.Get(base)
	if len(page.Frames) != 2 {
		t.Fatalf("after grow: frames=%d, want 2", len(page.Frames))
	}

	// A second stream re-establishing the same base must be ignored.
	if err := store.AppendStream(base, streamChunk(0, true, 3)); err != nil {
		t.Fatal(err)
	}
	page, _ = store.Get(base)
	if len(page.Frames) != 2 {
		t.Fatalf("redundant establish shrank the page: frames=%d, want 2", len(page.Frames))
	}
}

// TestResolveRestartsOnReplacement pins the defensive cursor reset: when the
// resident page is replaced (pointer change, same base), playback restarts so
// a.frame can never run past the new page's frame count.
func TestResolveRestartsOnReplacement(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := "srv/characters/witch/(a)normal"
	for i := 0; i < 3; i++ {
		if err := store.AppendStream(base, streamChunk(i, true, 3)); err != nil {
			t.Fatal(err)
		}
	}

	a := &animState{base: base}
	if _, ok := a.resolve(store); !ok {
		t.Fatal("resolve failed")
	}
	a.frame = 2 // cursor on the last resident frame

	// Replace the page with a shorter one (simulate a full upload / re-decode).
	short := &TexturePage{
		Frames:  make([]*sdl.Texture, 1),
		Delays:  []time.Duration{100 * time.Millisecond},
		Partial: true,
	}
	store.storeOversized(base, short)
	store.generation.Add(1)

	p, ok := a.resolve(store)
	if !ok || p != short {
		t.Fatalf("resolve did not return the replacement page: ok=%v", ok)
	}
	if a.frame != 0 {
		t.Fatalf("cursor not reset on replacement: frame=%d, want 0", a.frame)
	}
}

// TestResolveRestartsFromNilCursor pins the cold-load restart hole: a previous
// miss leaves a.page == nil while a.frame still points at a longer, evicted
// page. When a shorter replacement lands, resolve must restart the cursor from
// nil — not leave a.frame stale, which is the panic the frame pacer hit on a
// decimated re-decode.
func TestResolveRestartsFromNilCursor(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Purge()

	base := "srv/characters/witch/(a)normal"
	for i := 0; i < 3; i++ {
		if err := store.AppendStream(base, streamChunk(i, true, 3)); err != nil {
			t.Fatal(err)
		}
	}

	a := &animState{base: base}
	if _, ok := a.resolve(store); !ok {
		t.Fatal("resolve failed")
	}
	a.frame = 2 // cursor advanced while the page was resident

	// Evict the page and resolve the miss: a.page becomes nil, a.frame stays put.
	store.Remove(base)
	if _, ok := a.resolve(store); ok {
		t.Fatal("resolve should miss after eviction")
	}
	if a.frame != 2 {
		t.Fatalf("a miss must leave the cursor stale: frame=%d, want 2", a.frame)
	}

	// A shorter replacement arrives; resolving from a nil page must restart.
	short := &TexturePage{
		Frames:  make([]*sdl.Texture, 1),
		Delays:  []time.Duration{100 * time.Millisecond},
		Partial: true,
	}
	store.storeOversized(base, short)
	store.generation.Add(1)

	p, ok := a.resolve(store)
	if !ok || p != short {
		t.Fatalf("resolve did not return the replacement page: ok=%v", ok)
	}
	if a.frame != 0 {
		t.Fatalf("cursor not reset when resolving from a nil page: frame=%d, want 0", a.frame)
	}
}

// TestAdvanceMaxClampsStaleCursor pins the belt-and-suspenders guard: a frame
// cursor that somehow exceeds the page's frame count is clamped instead of
// panicking on the Delays index.
func TestAdvanceMaxClampsStaleCursor(t *testing.T) {
	v := NewViewport(nil)
	v.speakerAnim.reset("x")
	page := &TexturePage{
		Frames:  make([]*sdl.Texture, 2),
		Delays:  []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		Partial: true,
	}
	v.speakerAnim.frame = 47 // stale cursor from a shrunken page
	if v.speakerAnim.advance(page, 50*time.Millisecond, true) {
		t.Fatal("a 2-frame partial must not complete")
	}
	if v.speakerAnim.frame > 1 {
		t.Fatalf("cursor not clamped: frame=%d, want <= 1", v.speakerAnim.frame)
	}
}
