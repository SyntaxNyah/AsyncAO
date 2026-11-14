package render

import (
	"testing"
	"unsafe"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// copyTestTexW, copyTestTexH is the test texture: a gradient with a transparent band, so scale, mirror, tint and
	// fade all show in the pixels.
	copyTestTexW, copyTestTexH = 40, 30
	copyTestBand               = 10
	copyTestDstX, copyTestDstY = 23, 17
	copyTestDstW, copyTestDstH = 71, 53
	copyTestTint               = 160
	copyTestFade               = 120
	copyTestScale              = 1.7
	copyTestAllocRuns          = 50
	// copyTestClipInset keeps the clip inside the drawn rect, so a lost clip shows.
	copyTestClipInset = 8
)

// copyTestTexture is a streaming gradient texture whose leftmost columns are transparent.
func copyTestTexture(t *testing.T, ren *sdl.Renderer, shift byte) *sdl.Texture {
	t.Helper()
	tex, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ARGB8888), sdl.TEXTUREACCESS_STATIC, copyTestTexW, copyTestTexH)
	if err != nil {
		t.Fatalf("texture: %v", err)
	}
	copyTestFill(t, tex, shift)
	_ = tex.SetBlendMode(sdl.BLENDMODE_BLEND)
	return tex
}

func copyTestFill(t *testing.T, tex *sdl.Texture, shift byte) {
	t.Helper()
	pix := make([]byte, copyTestTexW*copyTestTexH*argbBytes)
	for y := 0; y < copyTestTexH; y++ {
		for x := 0; x < copyTestTexW; x++ {
			i := (y*copyTestTexW + x) * argbBytes
			pix[i] = byte(x*channelMax/copyTestTexW) + shift        // B
			pix[i+1] = byte(y * channelMax / copyTestTexH)          // G
			pix[i+2] = channelMax - byte(x*channelMax/copyTestTexW) // R
			pix[i+3] = channelMax
			if x < copyTestBand {
				pix[i+3] = 0
			}
		}
	}
	if err := tex.Update(nil, unsafe.Pointer(&pix[0]), copyTestTexW*argbBytes); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// copyTestDraw clears the window to grey, sets the scale and a clip, draws with draw, and reads the window back.
func copyTestDraw(t *testing.T, ren *sdl.Renderer, scale float32, draw func(dst *sdl.Rect)) []byte {
	t.Helper()
	_ = ren.SetScale(1, 1)
	_ = ren.SetClipRect(nil)
	_ = ren.SetDrawColor(percentMax, percentMax, percentMax, channelMax)
	_ = ren.Clear()
	_ = ren.SetScale(scale, scale)
	clip := sdl.Rect{X: copyTestDstX + copyTestClipInset, Y: copyTestDstY, W: copyTestDstW, H: copyTestDstH - copyTestClipInset}
	_ = ren.SetClipRect(&clip)
	dst := sdl.Rect{X: copyTestDstX, Y: copyTestDstY, W: copyTestDstW, H: copyTestDstH}
	draw(&dst)
	_ = ren.SetClipRect(nil)
	_ = ren.SetScale(1, 1)
	return readWindow(t, ren)
}

// TestCopyCacheMatchesDirectDraw pins a cached copy to SDL's own draw: scaled, tinted, faded and mirrored copies
// land on the same pixels, at a render scale and inside a clip; a texture update shows instead of the old copy; a
// cached draw allocates nothing; and an animating fade and a render target are never kept.
func TestCopyCacheMatchesDirectDraw(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	cache := NewCopyCache(ren)
	if !cache.software {
		t.Skip("not SDL's software renderer")
	}
	defer cache.Release()
	tex := copyTestTexture(t, ren, 0)
	defer tex.Destroy()

	cases := []struct {
		name       string
		tint, fade uint8
		flip       sdl.RendererFlip
	}{
		{"scaled", channelMax, channelMax, sdl.FLIP_NONE},
		{"tinted and faded", copyTestTint, copyTestFade, sdl.FLIP_NONE},
		{"mirrored", channelMax, channelMax, sdl.FLIP_HORIZONTAL},
		{"all of them", copyTestTint, copyTestFade, sdl.FLIP_VERTICAL},
	}
	for _, scale := range []float32{1, copyTestScale} {
		for _, tc := range cases {
			_ = tex.SetColorMod(tc.tint, channelMax, tc.tint)
			_ = tex.SetAlphaMod(tc.fade)
			want := copyTestDraw(t, ren, scale, func(dst *sdl.Rect) { _ = ren.CopyEx(tex, nil, dst, 0, nil, tc.flip) })
			held := len(cache.entries)
			cached := func(dst *sdl.Rect) { _ = cache.CopyFlip(ren, tex, nil, dst, tc.flip) }
			copyTestDraw(t, ren, scale, cached) // the first draw of a region only learns its mods
			got := copyTestDraw(t, ren, scale, cached)
			if len(cache.entries) == held && (scale != 1 || tc.tint != channelMax || tc.flip != sdl.FLIP_NONE) {
				t.Errorf("%s at %.1fx: drawn twice without a copy being kept", tc.name, scale)
			}
			if pct := offPct(got, want); pct > prescaleMaxOffPct {
				t.Errorf("%s at %.1fx: %d%% of pixels differ from SDL's own draw", tc.name, scale, pct)
			}
		}
	}

	_ = tex.SetColorMod(channelMax, channelMax, channelMax)
	_ = tex.SetAlphaMod(channelMax)
	before := copyTestDraw(t, ren, copyTestScale, func(dst *sdl.Rect) { _ = cache.Copy(ren, tex, nil, dst) })
	copyTestDraw(t, ren, copyTestScale, func(dst *sdl.Rect) { _ = cache.Copy(ren, tex, nil, dst) })
	copyTestFill(t, tex, channelMax/2)
	want := copyTestDraw(t, ren, copyTestScale, func(dst *sdl.Rect) { _ = ren.Copy(tex, nil, dst) })
	copyTestDraw(t, ren, copyTestScale, func(dst *sdl.Rect) { _ = cache.Copy(ren, tex, nil, dst) })
	got := copyTestDraw(t, ren, copyTestScale, func(dst *sdl.Rect) { _ = cache.Copy(ren, tex, nil, dst) })
	if offPct(before, want) <= prescaleMaxOffPct {
		t.Fatal("the update changed nothing visible, so it can't show a stale copy")
	}
	if pct := offPct(got, want); pct > prescaleMaxOffPct {
		t.Errorf("after an update %d%% of pixels differ: the old copy was drawn", pct)
	}

	dst := sdl.Rect{X: copyTestDstX, Y: copyTestDstY, W: copyTestDstW, H: copyTestDstH}
	_ = ren.SetScale(copyTestScale, copyTestScale)
	if n := testing.AllocsPerRun(copyTestAllocRuns, func() { _ = cache.Copy(ren, tex, nil, &dst) }); n != 0 {
		t.Errorf("a cached draw allocates %.1f times, want 0", n)
	}
	_ = ren.SetScale(1, 1)

	// A fade that moves every draw is animating: drawn directly, it never fills the cache with one-frame copies.
	held := len(cache.entries)
	_ = ren.SetScale(copyTestScale, copyTestScale)
	for i := range copyTestAllocRuns {
		_ = tex.SetAlphaMod(uint8(i))
		_ = cache.Copy(ren, tex, nil, &dst)
	}
	_ = ren.SetScale(1, 1)
	_ = tex.SetAlphaMod(channelMax)
	if len(cache.entries) != held {
		t.Errorf("an animating fade kept %d copies, want none", len(cache.entries)-held)
	}

	target, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ARGB8888), sdl.TEXTUREACCESS_TARGET, copyTestTexW, copyTestTexH)
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	defer target.Destroy()
	held = len(cache.entries)
	_ = cache.Copy(ren, target, nil, &dst)
	_ = cache.Copy(ren, target, nil, &dst)
	if len(cache.entries) != held {
		t.Error("a render target was cached; its pixels change without an update")
	}
}
