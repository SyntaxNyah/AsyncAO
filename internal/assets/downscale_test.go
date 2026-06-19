package assets

import (
	"image"
	"testing"
)

// TestDownscaleDecodedAspect pins the high-res sprite cap geometry: the height
// binds to the cap, the aspect ratio is preserved, and an asset already within
// the cap (or a non-positive cap) passes through untouched (downscale-only).
func TestDownscaleDecodedAspect(t *testing.T) {
	mk := func(w, h int) *Decoded {
		return &Decoded{
			Width:  w,
			Height: h,
			Frames: []*image.RGBA{image.NewRGBA(image.Rect(0, 0, w, h))},
		}
	}

	// The real Skrapegropen case: a 2003×1966 sprite capped at 1080.
	out := downscaleDecodedAspect(mk(2003, 1966), 1080)
	wantW := 2003 * 1080 / 1966
	if out.Height != 1080 || out.Width != wantW {
		t.Errorf("got %dx%d, want %dx%d (height-bound, aspect-preserved)", out.Width, out.Height, wantW, 1080)
	}
	if len(out.Frames) != 1 || out.Frames[0].Rect.Dy() != 1080 || out.Frames[0].Rect.Dx() != wantW {
		t.Errorf("frame rect = %v, want %dx%d", out.Frames[0].Rect, wantW, 1080)
	}

	// Downscale-only: an asset within the cap is returned as-is (same pointer).
	small := mk(800, 600)
	if got := downscaleDecodedAspect(small, 1080); got != small {
		t.Error("asset within the cap must pass through unchanged")
	}
	// A non-positive cap disables the path.
	if got := downscaleDecodedAspect(small, 0); got != small {
		t.Error("maxH<=0 must be a no-op")
	}
}
