package render

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestSetTextureScaleModeRoundTrips proves the cgo binding actually reaches SDL:
// it sets each mode on a real texture and reads it back with the SDL getter that
// go-sdl2 doesn't bind either.
//
// Worth a real test rather than inspection, because the failure mode is silent.
// The shim casts a Go *sdl.Texture straight to a C SDL_Texture* — if go-sdl2 ever
// stops making that an identity (it is one today: Texture is a struct{} handle),
// the call would corrupt rather than error, and every sprite would keep drawing
// with the client-wide filter while the code claimed otherwise.
func TestSetTextureScaleModeRoundTrips(t *testing.T) {
	if !scaleModeSupported {
		t.Skip("SDL < 2.0.12: per-texture scale mode is compiled out by design")
	}
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()

	tex, err := ren.CreateTexture(sdl.PIXELFORMAT_ARGB8888, sdl.TEXTUREACCESS_STATIC, 8, 8)
	if err != nil {
		t.Skipf("texture unavailable: %v", err)
	}
	defer func() { _ = tex.Destroy() }()

	for _, mode := range []sdl.ScaleMode{sdl.ScaleModeNearest, sdl.ScaleModeLinear, sdl.ScaleModeNearest} {
		if !setTextureScaleMode(tex, mode) {
			t.Fatalf("setTextureScaleMode(%d) failed on a live texture: %v", mode, sdl.GetError())
		}
		got, ok := textureScaleMode(tex)
		if !ok {
			t.Fatalf("SDL_GetTextureScaleMode failed: %v", sdl.GetError())
		}
		if got != mode {
			t.Errorf("scale mode did not stick: set %d, SDL reports %d", mode, got)
		}
	}
	// nil is the "page has no texture at this index" case the memo will hit.
	if setTextureScaleMode(nil, sdl.ScaleModeNearest) {
		t.Error("a nil texture must report failure, not success")
	}
}
