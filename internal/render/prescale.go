package render

import (
	"math"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Pre-scaled frames for SDL's software renderer, which stretches and blends every pixel of a scaled copy on one
// thread: a layer's frame is scaled, mirrored and tinted once to its size in pixels and then copied 1:1, as AO2
// scales a frame once when it loads it (animationlayer.cpp:241-262). GPU renderers scale for free and skip this.

// maxScaledFramePixels caps one cached frame (4K); a bigger draw, such as a zoomed sprite, takes the scaled copy.
const maxScaledFramePixels = 3840 * 2160

// scaledKey is what a cached frame was built from; any change rebuilds it.
type scaledKey struct {
	// page is held, so its address can't be reused by another page while the cache names it.
	page             *TexturePage
	frame            int
	src              *sdl.Texture
	w, h             int32
	mode             sdl.ScaleMode
	flip             sdl.RendererFlip
	modR, modG, modB uint8
	// alpha is a fixed fade, such as a reflection's, baked into the pixels: SDL's software renderer blends a texture
	// with an alpha mod about 27 times slower than one without.
	alpha uint8
}

// scaledFrame is one layer's frame at its size in pixels, owned by that layer's animState.
type scaledFrame struct {
	tex, mirror      *sdl.Texture
	texW, texH       int32
	mirrorW, mirrorH int32
	key              scaledKey
	built            bool
}

// release destroys the cached textures; render thread only.
func (f *scaledFrame) release() {
	if f.tex != nil {
		_ = f.tex.Destroy()
	}
	if f.mirror != nil {
		_ = f.mirror.Destroy()
	}
	*f = scaledFrame{}
}

// fitTarget makes *tex a render target of w×h, recreating it only when the size changes.
func fitTarget(ren *sdl.Renderer, tex **sdl.Texture, texW, texH *int32, w, h int32) bool {
	if *tex != nil && *texW == w && *texH == h {
		return true
	}
	if *tex != nil {
		_ = (*tex).Destroy()
		*tex = nil
	}
	t, err := ren.CreateTexture(uint32(sdl.PIXELFORMAT_ABGR8888), sdl.TEXTUREACCESS_TARGET, w, h)
	if err != nil {
		return false
	}
	*tex, *texW, *texH = t, w, h
	return true
}

// build draws key's frame into the cache. The source keeps its colour mod, which key records; mirroring happens at
// the source's size first, because SDL's software renderer mirrors a scaled copy through its slow rotation path.
func (f *scaledFrame) build(ren *sdl.Renderer, key scaledKey, opaque bool) bool {
	f.built = false
	if !fitTarget(ren, &f.tex, &f.texW, &f.texH, key.w, key.h) {
		return false
	}
	prev := ren.GetRenderTarget()
	src := key.src
	_ = src.SetBlendMode(sdl.BLENDMODE_NONE) // an exact copy: the cache keeps the frame's own alpha
	if key.flip != sdl.FLIP_NONE {
		if !fitTarget(ren, &f.mirror, &f.mirrorW, &f.mirrorH, key.page.W, key.page.H) ||
			ren.SetRenderTarget(f.mirror) != nil {
			_ = src.SetBlendMode(sdl.BLENDMODE_BLEND)
			_ = ren.SetRenderTarget(prev)
			return false
		}
		_ = ren.CopyEx(src, nil, nil, 0, nil, key.flip)
		_ = src.SetBlendMode(sdl.BLENDMODE_BLEND)
		_ = f.mirror.SetBlendMode(sdl.BLENDMODE_NONE)
		_ = f.mirror.SetScaleMode(key.mode)
		src = f.mirror
	}
	ok := ren.SetRenderTarget(f.tex) == nil
	if ok {
		_ = src.SetAlphaMod(key.alpha) // with blending off, the fade lands in the cached pixels' alpha
		_ = ren.Copy(src, nil, nil)
		_ = src.SetAlphaMod(channelMax)
	}
	_ = key.src.SetBlendMode(sdl.BLENDMODE_BLEND)
	_ = ren.SetRenderTarget(prev)
	if !ok {
		return false
	}
	blend := sdl.BlendMode(sdl.BLENDMODE_BLEND)
	if opaque && key.alpha == channelMax {
		blend = sdl.BLENDMODE_NONE
	}
	_ = f.tex.SetBlendMode(blend)
	f.key, f.built = key, true
	return true
}

// scaledPixels converts a coordinate in render units to pixels under the render scale s.
func scaledPixels(n int32, s float32) int32 {
	return int32(math.Round(float64(n) * float64(s)))
}

// drawScaled copies frame of page to dst through sf, a pre-scaled copy, and reports whether it drew; false leaves the
// draw to the caller (not SDL's software renderer, or too big to keep). src carries the colour mod to bake in, and
// page's applyScaleMode must have run, as its remembered filter is the one baked in. alpha fades the copy.
func (v *Viewport) drawScaled(ren *sdl.Renderer, sf *scaledFrame, page *TexturePage, frame int, src *sdl.Texture,
	dst sdl.Rect, flip sdl.RendererFlip, modR, modG, modB, alpha uint8) bool {
	if !v.prescale {
		return false
	}
	sx, sy := ren.GetScale()
	v.scaledDst = sdl.Rect{X: scaledPixels(dst.X, sx), Y: scaledPixels(dst.Y, sy), W: scaledPixels(dst.W, sx), H: scaledPixels(dst.H, sy)}
	if v.scaledDst.W <= 0 || v.scaledDst.H <= 0 || int64(v.scaledDst.W)*int64(v.scaledDst.H) > maxScaledFramePixels {
		return false
	}
	key := scaledKey{page: page, frame: frame, src: src, w: v.scaledDst.W, h: v.scaledDst.H, mode: page.scaleMode, flip: flip,
		modR: modR, modG: modG, modB: modB, alpha: alpha}
	if (!sf.built || sf.key != key) && !sf.build(ren, key, page.Opaque) {
		return false
	}
	if sx == 1 && sy == 1 {
		_ = ren.Copy(sf.tex, nil, &v.scaledDst)
		return true
	}
	// The copy runs in pixels, so a clip set in render units is converted for it and put back after.
	clipped := ren.IsClipEnabled()
	if clipped {
		v.scaledClip = ren.GetClipRect()
	}
	_ = ren.SetScale(1, 1)
	if clipped {
		logical := v.scaledClip
		v.scaledClip = sdl.Rect{X: scaledPixels(logical.X, sx), Y: scaledPixels(logical.Y, sy),
			W: scaledPixels(logical.W, sx), H: scaledPixels(logical.H, sy)}
		_ = ren.SetClipRect(&v.scaledClip)
		v.scaledClip = logical
	}
	_ = ren.Copy(sf.tex, nil, &v.scaledDst)
	_ = ren.SetScale(sx, sy)
	if clipped {
		_ = ren.SetClipRect(&v.scaledClip)
	}
	return true
}

// isSoftwareRenderer reports SDL's software renderer, the one drawScaled helps.
func isSoftwareRenderer(ren *sdl.Renderer) bool {
	if ren == nil {
		return false
	}
	info, err := ren.GetInfo()
	return err == nil && info.Name == sdl.SoftwareRendererName
}
