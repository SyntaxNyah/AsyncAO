package render

// The shout splash's aspect-preserving fit, and the gate that keeps it off the
// full-fill path it used to share with the background.

import (
	"os"
	"strings"
	"testing"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// TestSplashFitRectKeepsTheAspectAndCentres is the reported defect, as numbers.
//
// A 256x192 (4:3) shout bubble on a 960x540 (16:9) stage: AO2 scales it by the
// HEIGHT factor 540/192 = 2.8125, giving 720x540, and its centre-aligned QLabel
// puts it at x = (960-720)/2 = 120. The old full-fill blit gave 960x540 at x=0 —
// the bubble stretched 33% wide.
func TestSplashFitRectKeepsTheAspectAndCentres(t *testing.T) {
	vp := sdl.Rect{X: 40, Y: 10, W: 960, H: 540}
	got := splashFitRect(vp, 256, 192)
	want := sdl.Rect{X: 40 + 120, Y: 10, W: 720, H: 540}
	if got != want {
		t.Errorf("4:3 bubble on a 16:9 stage: got %+v, want %+v", got, want)
	}
	// The height is always the viewport's — AO2 scales BY height, so the bubble
	// fills the stage vertically at any aspect.
	for _, src := range [][2]int32{{16, 9}, {4, 3}, {1, 1}, {3, 4}, {2000, 40}} {
		r := splashFitRect(vp, src[0], src[1])
		if r.H != vp.H || r.Y != vp.Y {
			t.Errorf("src %dx%d: vertical fit broken, got %+v", src[0], src[1], r)
		}
		if wantW := int32((int64(src[0])*int64(vp.H)*2 + int64(src[1])) / (int64(src[1]) * 2)); r.W != wantW {
			t.Errorf("src %dx%d: W = %d, want %d", src[0], src[1], r.W, wantW)
		}
		if r.X+r.W/2 != vp.X+vp.W/2 && r.W%2 == 0 && vp.W%2 == 0 {
			t.Errorf("src %dx%d: not centred, got %+v in %+v", src[0], src[1], r, vp)
		}
	}
	// Art WIDER than the stage still fits by height and overhangs symmetrically,
	// which is what Qt's centre alignment does with an oversized pixmap: the pixmap
	// really is 400 px wide and really does start 150 px left of the label. What Qt
	// does with the overhang is CLIP it, because the splash is a child widget of the
	// viewport — the subtest below is that half, and without it this rect is a blit
	// straight over the chatbox.
	wide := splashFitRect(sdl.Rect{W: 100, H: 100}, 400, 100)
	if wide.W != 400 || wide.X != -150 {
		t.Errorf("oversized art: got %+v, want W=400 X=-150", wide)
	}

	// The drawn AREA, against real pixels. Fitting by height means any art wider
	// than the stage's aspect produces a destination rect wider than the stage, and
	// `ui_vp_objection` is a QLabel child moved and resized to the viewport
	// (courtroom.cpp:808-809) — Qt clips a child's painting to its own rect, so in
	// canon those pixels do not exist. Ours are a plain SDL_RenderCopy, so they land
	// on the chatbox and the side panels unless drawSplash brackets the blit.
	t.Run("the blit never leaves the stage", func(t *testing.T) {
		ren, cleanup := newHeadlessRenderer(t)
		defer cleanup()
		store, err := NewTextureStore(ren)
		if err != nil {
			t.Skipf("texture store unavailable: %v", err)
		}
		defer store.Purge()

		// 4:1 art — the shape a wide "OBJECTION!" banner actually is, and far enough
		// past the stage's 4:3 that the overhang is readable on both sides.
		const base = "live://misc/Phoenix/objection"
		if err := store.Upload(base, opaqueDecoded(400, 100)); err != nil {
			t.Fatalf("upload: %v", err)
		}
		page, ok := store.Get(base)
		if !ok {
			t.Fatal("the shout page did not survive the upload")
		}
		// newHeadlessRenderer opens a 640x480 window; the stage is a 4:3 box well
		// inside it, so every direction the bubble could spill is READABLE.
		const winW, winH = 640, 480
		stage := sdl.Rect{X: 160, Y: 120, W: 320, H: 240}

		dst := splashFitRect(stage, page.W, page.H)
		if dst.X >= stage.X && dst.X+dst.W <= stage.X+stage.W {
			t.Fatalf("dst %+v is already inside the stage %+v — this case proves nothing", dst, stage)
		}

		v := NewViewport(store)
		v.syncAnim(&v.shoutAnim, base)
		_ = ren.SetClipRect(nil)
		_ = ren.SetDrawColor(0, 0, 0, 255)
		_ = ren.Clear()
		v.drawSplash(ren, base, &v.shoutAnim, stage)

		pix := make([]byte, winW*winH*4)
		if err := ren.ReadPixels(&sdl.Rect{X: 0, Y: 0, W: winW, H: winH},
			uint32(sdl.PIXELFORMAT_ARGB8888), unsafe.Pointer(&pix[0]), winW*4); err != nil {
			t.Skipf("ReadPixels unavailable on this backend: %v", err)
		}
		minX, minY, maxX, maxY := winW, winH, -1, -1
		for y := 0; y < winH; y++ {
			for x := 0; x < winW; x++ {
				p := (y*winW + x) * 4
				if pix[p] == 0 && pix[p+1] == 0 && pix[p+2] == 0 {
					continue
				}
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
		if maxX < 0 {
			t.Fatal("the shout drew NOTHING — a clip that eats the whole bubble is not containment")
		}
		if int32(minX) < stage.X || int32(minY) < stage.Y ||
			int32(maxX) >= stage.X+stage.W || int32(maxY) >= stage.Y+stage.H {
			t.Errorf("shout ink spans (%d,%d)-(%d,%d), outside the stage %+v — the bubble painted over "+
				"the chatbox / the side panels (dst was %+v)", minX, minY, maxX, maxY, stage, dst)
		}
		// It must still fill the stage vertically and be centred on it: containment
		// that also shifted or shrank the bubble would pass the bounds test above.
		if int32(minY) > stage.Y || int32(maxY) < stage.Y+stage.H-1 {
			t.Errorf("shout ink spans rows %d-%d, want the full stage band %d-%d",
				minY, maxY, stage.Y, stage.Y+stage.H-1)
		}
		if int32(minX) != stage.X || int32(maxX) != stage.X+stage.W-1 {
			t.Errorf("a bubble wider than the stage must cover it edge to edge, got columns %d-%d for %+v",
				minX, maxX, stage)
		}

		// The camera-zoom path already owns a clip (internal/ui/vpzoom.go) and it IS
		// the stage, while the vp Render is handed there is the LARGER zoomed scene
		// rect: containment must defer to it rather than stomp it, exactly like the
		// sprite mask and the overlay rungs.
		outer := sdl.Rect{X: 8, Y: 9, W: 100, H: 101}
		_ = ren.SetClipRect(&outer)
		v.drawSplash(ren, base, &v.shoutAnim, stage)
		if got := ren.GetClipRect(); got != outer {
			t.Errorf("the caller's clip came back as %+v, want %+v", got, outer)
		}
		_ = ren.SetClipRect(nil)

		// And the bracket costs nothing per frame: both rects ride persistent
		// scratches, because &localRect heap-escapes through cgo on every call.
		draw := func() { v.drawSplash(ren, base, &v.shoutAnim, stage) }
		draw()
		if n := testing.AllocsPerRun(200, draw); n != 0 {
			t.Errorf("a shout frame allocates %.1f/op, want 0 — the clip bracket is taking &localRect", n)
		}
	})
}

// TestSplashFitRectFallsBackToTheViewport: a page that reports no size is a page
// we cannot reason about, and the pre-existing full-fill blit is a better answer
// than a zero-area rect. This is also what keeps a degenerate viewport from
// dividing by zero.
func TestSplashFitRectFallsBackToTheViewport(t *testing.T) {
	vp := sdl.Rect{X: 1, Y: 2, W: 300, H: 200}
	for _, src := range [][2]int32{{0, 0}, {0, 100}, {100, 0}, {-4, 8}} {
		if got := splashFitRect(vp, src[0], src[1]); got != vp {
			t.Errorf("src %dx%d: got %+v, want the viewport %+v", src[0], src[1], got, vp)
		}
	}
	if got := splashFitRect(sdl.Rect{W: 10, H: 0}, 4, 3); got.H != 0 {
		t.Errorf("a zero-height viewport must not be scaled into: %+v", got)
	}
}

// TestTheShoutDoesNotGoThroughTheFullFillPath is the encapsulation gate.
//
// drawFill is the FULL-VIEWPORT blit, and it is right for exactly two layers: the
// background and the desk. The shout used it too, and that was the defect. A
// source gate rather than a behavioural one because the failure mode is a call
// that reverts to the wrong function — which no pixel assertion can see without a
// GPU, and which a future edit to Render is one line away from re-introducing.
func TestTheShoutDoesNotGoThroughTheFullFillPath(t *testing.T) {
	src, err := os.ReadFile("viewport.go")
	if err != nil {
		t.Fatalf("viewport.go: %v", err)
	}
	text := string(src)
	if strings.Contains(text, "v.drawFill(ren, shoutBase") {
		t.Error("the shout bubble is back on drawFill — it will stretch across a wide stage again; " +
			"AO2's splash layer never stretches (splashfit.go cites animationlayer.cpp:229-260)")
	}
	if !strings.Contains(text, "v.drawSplash(ren, shoutBase, &v.shoutAnim, vp)") {
		t.Error("Render no longer draws the shout through drawSplash")
	}
	// And drawFill must keep resolving its frame through the SHARED helper, or the
	// two layers can drift apart on the held-frame bridge.
	if !strings.Contains(text, "tex, _, _, ok := v.layerFrame(anim)") {
		t.Error("drawFill stopped sharing layerFrame with drawSplash — the held-frame bridge is now duplicated")
	}
}
