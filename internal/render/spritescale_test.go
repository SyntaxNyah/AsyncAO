package render

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestSpriteScaleModeFollowsAO2 pins the full resolution chain at the draw site:
// user mode, then char.ini, then AO2's geometry rule.
//
// The geometry rows are the ones that matter for issue #21 label 15. Nobody edits
// a char.ini to fix a character they downloaded, so the reported "smoothie" has to
// be fixed by the last rule alone: art SHORTER than the stage is being enlarged and
// gets point-sampled (AnimationLayer::calculateFrameGeometry — the default is
// smooth, and only the enlarging branch flips it).
func TestSpriteScaleModeFollowsAO2(t *testing.T) {
	const stageH = 1080

	for _, tc := range []struct {
		name       string
		user, char courtroom.ScalingMode
		nativeH    int32
		want       sdl.ScaleMode
	}{
		// The reported case: small hand-pixelled art on a big stage, nothing configured.
		{"tiny art enlarged onto the stage", courtroom.ScalingAuto, courtroom.ScalingAuto, 256, sdl.ScaleModeNearest},
		// Art already at or above stage height is being shrunk — AO2 keeps that smooth.
		{"art shrunk to the stage", courtroom.ScalingAuto, courtroom.ScalingAuto, 2000, sdl.ScaleModeLinear},
		{"art exactly stage height", courtroom.ScalingAuto, courtroom.ScalingAuto, stageH, sdl.ScaleModeLinear},

		// char.ini speaks while the user is on Auto.
		{"char asks for pixel", courtroom.ScalingAuto, courtroom.ScalingPixel, 2000, sdl.ScaleModeNearest},
		{"char asks for smooth", courtroom.ScalingAuto, courtroom.ScalingSmooth, 256, sdl.ScaleModeLinear},

		// An explicit user choice wins over both char.ini and geometry.
		{"user pixel beats geometry", courtroom.ScalingPixel, courtroom.ScalingAuto, 2000, sdl.ScaleModeNearest},
		{"user smooth beats geometry", courtroom.ScalingSmooth, courtroom.ScalingAuto, 256, sdl.ScaleModeLinear},
		{"user smooth beats char pixel", courtroom.ScalingSmooth, courtroom.ScalingPixel, 256, sdl.ScaleModeLinear},

		// A page with no known height must not be read as "infinitely small".
		{"unknown native height", courtroom.ScalingAuto, courtroom.ScalingAuto, 0, sdl.ScaleModeLinear},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &Viewport{fx: SpriteFX{UserScaling: tc.user}}
			layer := &courtroom.SpriteLayer{Scaling: tc.char}
			if got := v.spriteScaleMode(layer, tc.nativeH, stageH); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestApplyScaleModeIsMemoised guards the cost, not the correctness. The apply
// runs on the render loop for every sprite of every frame, and each real change
// forces a renderer flush (mandatory under HINT_RENDER_BATCHING — see
// applyScaleMode). Without the memo a stable stage would flush twice per frame
// forever, quietly undoing the batching the client turns on at startup.
func TestApplyScaleModeIsMemoised(t *testing.T) {
	if !scaleModeSupported {
		t.Skip("SDL < 2.0.12: per-texture scale mode is compiled out by design")
	}
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 64<<20)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	const base = "live://characters/furio/(a)normal"
	if err := store.Upload(base, decodedFixture()); err != nil {
		t.Fatalf("upload: %v", err)
	}
	page, ok := store.Get(base)
	if !ok {
		t.Fatal("test setup: the page must be resident")
	}

	if page.scaleModeSet {
		t.Error("a fresh page must start unlatched — otherwise the first apply is skipped")
	}
	page.applyScaleMode(ren, sdl.ScaleModeNearest)
	if !page.scaleModeSet || page.scaleMode != sdl.ScaleModeNearest {
		t.Fatalf("first apply did not latch: set=%v mode=%d", page.scaleModeSet, page.scaleMode)
	}
	for _, tex := range page.Frames {
		got, ok := textureScaleMode(tex)
		if !ok {
			t.Fatalf("read back: %v", sdl.GetError())
		}
		if got != sdl.ScaleModeNearest {
			t.Errorf("EVERY frame of an animation must get the filter, got %d", got)
		}
	}

	// Re-applying the same mode is the steady state and must do NOTHING. Asserting
	// the latch is still Nearest would pass even if the memo were deleted, so make
	// the skip observable instead: change one texture's real filter behind the
	// memo's back, then ask for the memoised mode. If the memo works nothing runs
	// and the sabotage survives; if it were removed the loop would overwrite it.
	setTextureScaleMode(page.Frames[0], sdl.ScaleModeLinear)
	page.applyScaleMode(ren, sdl.ScaleModeNearest)
	if got, _ := textureScaleMode(page.Frames[0]); got != sdl.ScaleModeLinear {
		t.Error("re-applying the memoised mode did real work (cgo + a mandatory renderer flush) " +
			"— that runs per sprite per frame and defeats HINT_RENDER_BATCHING")
	}

	// A genuine change still takes.
	page.applyScaleMode(ren, sdl.ScaleModeLinear)
	if page.scaleMode != sdl.ScaleModeLinear {
		t.Errorf("a changed mode must re-apply, got %d", page.scaleMode)
	}
	for _, tex := range page.Frames {
		if got, _ := textureScaleMode(tex); got != sdl.ScaleModeLinear {
			t.Errorf("frame kept the old filter after a change: %d", got)
		}
	}
}

// TestSpriteScaleModeZeroAlloc keeps the resolver honest against the
// zero-allocation render loop (CLAUDE.md hard rule). It runs per sprite per frame.
func TestSpriteScaleModeZeroAlloc(t *testing.T) {
	v := &Viewport{}
	layer := &courtroom.SpriteLayer{Scaling: courtroom.ScalingAuto}
	if n := testing.AllocsPerRun(200, func() {
		_ = v.spriteScaleMode(layer, 256, 1080)
	}); n != 0 {
		t.Errorf("spriteScaleMode allocates %v/op, want 0", n)
	}
}
