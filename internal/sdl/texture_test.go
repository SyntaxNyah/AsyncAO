package sdl

import (
	"testing"
	"unsafe"
)

const (
	texTestWinW, texTestWinH = 16, 16
	texTestTexW              = 4
	bytesPerPixel            = 4
	// texTestPixels is the test surface's width: one half-transparent pixel and one opaque.
	texTestPixels = 2
	// texTestDepth is the bits per pixel CreateRGBSurfaceWithFormat is handed; SDL3 ignores it.
	texTestDepth = 32
	halfAlpha    = 128
	fullChannel  = 255
	// blendTolerance absorbs the software blender's rounding of a half-alpha channel.
	blendTolerance = 2
)

// newSoftwareRenderer opens SDL's software renderer on a hidden dummy-driver window; it skips without SDL.
func newSoftwareRenderer(t *testing.T) *Renderer {
	t.Helper()
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := Init(INIT_VIDEO); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	win, err := CreateWindow("texture test", 0, 0, texTestWinW, texTestWinH, WINDOW_HIDDEN)
	if err != nil {
		Quit()
		t.Skipf("window unavailable: %v", err)
	}
	ren, err := CreateRenderer(win, -1, RENDERER_SOFTWARE)
	if err != nil {
		_ = win.Destroy()
		Quit()
		t.Skipf("software renderer unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = ren.Destroy()
		_ = win.Destroy()
		Quit()
	})
	return ren
}

// TestSoftwareTexturesAreStreaming pins the software renderer's textures to streaming ones, which SDL doesn't
// run-length encode: CreateTexture asked for a static texture and CreateTextureFromSurface both give streaming ones,
// and a surface's texture draws its own pixels blended by their alpha, from either byte order.
func TestSoftwareTexturesAreStreaming(t *testing.T) {
	ren := newSoftwareRenderer(t)

	static, err := ren.CreateTexture(uint32(PIXELFORMAT_ABGR8888), TEXTUREACCESS_STATIC, texTestTexW, texTestTexW)
	if err != nil {
		t.Fatalf("CreateTexture: %v", err)
	}
	defer static.Destroy()
	if got := static.Access(); got != TEXTUREACCESS_STREAMING {
		t.Errorf("a static texture on the software renderer has access %d, want streaming (%d)", got,
			TEXTUREACCESS_STREAMING)
	}

	// Pixel 0 is half-transparent red, pixel 1 opaque green; byte order follows the format.
	for _, tc := range []struct {
		name   string
		format uint32
		red    [bytesPerPixel]byte
		green  [bytesPerPixel]byte
	}{
		{"ARGB8888", uint32(PIXELFORMAT_ARGB8888), [4]byte{0, 0, fullChannel, halfAlpha}, [4]byte{0, fullChannel, 0, fullChannel}},
		{"ABGR8888", uint32(PIXELFORMAT_ABGR8888), [4]byte{fullChannel, 0, 0, halfAlpha}, [4]byte{0, fullChannel, 0, fullChannel}},
	} {
		surf, err := CreateRGBSurfaceWithFormat(0, texTestPixels, 1, texTestDepth, tc.format)
		if err != nil {
			t.Fatalf("%s: surface: %v", tc.name, err)
		}
		pix := surf.Pixels()
		copy(pix[0:bytesPerPixel], tc.red[:])
		copy(pix[bytesPerPixel:texTestPixels*bytesPerPixel], tc.green[:])
		tex, err := ren.CreateTextureFromSurface(surf)
		surf.Free()
		if err != nil {
			t.Fatalf("%s: CreateTextureFromSurface: %v", tc.name, err)
		}
		if got := tex.Access(); got != TEXTUREACCESS_STREAMING {
			t.Errorf("%s: a surface's texture has access %d, want streaming", tc.name, got)
		}
		if bm, _ := tex.GetBlendMode(); bm != BLENDMODE_BLEND {
			t.Errorf("%s: blend mode %d, want the surface's blend (%d)", tc.name, bm, BLENDMODE_BLEND)
		}
		_ = ren.SetDrawColor(0, 0, 0, fullChannel)
		_ = ren.Clear()
		_ = ren.Copy(tex, nil, &Rect{W: texTestPixels, H: 1})
		_ = tex.Destroy()

		var out [texTestPixels * bytesPerPixel]byte // ARGB8888 in memory: B, G, R, A
		if err := ren.ReadPixels(&Rect{W: texTestPixels, H: 1}, uint32(PIXELFORMAT_ARGB8888), unsafe.Pointer(&out[0]),
			texTestPixels*bytesPerPixel); err != nil {
			t.Skipf("ReadPixels: %v", err)
		}
		if r := int(out[2]); r < halfAlpha-blendTolerance || r > halfAlpha+blendTolerance || out[0] != 0 || out[1] != 0 {
			t.Errorf("%s: half-transparent red over black drew B,G,R %d,%d,%d, want about 0,0,%d", tc.name,
				out[0], out[1], out[2], halfAlpha)
		}
		if g := out[bytesPerPixel:]; g[0] != 0 || g[1] != fullChannel || g[2] != 0 {
			t.Errorf("%s: opaque green drew B,G,R %d,%d,%d, want 0,%d,0", tc.name, g[0], g[1], g[2], fullChannel)
		}
	}
}
