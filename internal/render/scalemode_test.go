package render

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// TestSetTextureScaleModeRoundTrips proves a texture's filter really reaches
// SDL: it sets each mode on a real texture and reads it back. The failure mode
// is silent (every sprite would keep the default filter while the code claimed
// otherwise), so it is worth a real test.
func TestSetTextureScaleModeRoundTrips(t *testing.T) {
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
