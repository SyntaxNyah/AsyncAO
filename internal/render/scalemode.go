package render

// Per-texture scale mode: the missing half of AO2's `scaling` support.
// (../AO2-Client/src/text_file_functions.cpp:175-191): a pixel-art character
// declares `scaling = pixel` and is point-sampled no matter what the rest of the
// client does. Issue #21 label 15 is exactly this — a hand-pixelled character
// rendered through our global linear filter "becomes a smoothie".
// Callers must be on the render thread (hard rule 1) — every one of them is,
// since a *sdl.Texture only exists there.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// setTextureScaleMode points one texture at one filter. Reports whether it took.
//
// Render thread only.
func setTextureScaleMode(tex *sdl.Texture, mode sdl.ScaleMode) bool {
	return tex != nil && tex.SetScaleMode(mode) == nil
}

// spriteScaleMode settles one layer's filter, finishing the resolution chain
// that internal/courtroom/scaling.go starts.
// ../AO2-Client/src/animationlayer.cpp:229-273): the default is smooth, but art
// SHORTER than the space it fills is being enlarged, and enlarging is point-sampled
// so the pixels stay square. That single rule is what fixes the reported character
// with no pref and no char.ini edit — hand-pixelled art is small, and small art on a
// 1080p stage is always being enlarged.
func (v *Viewport) spriteScaleMode(layer *courtroom.SpriteLayer, nativeH, targetH int32) sdl.ScaleMode {
	switch courtroom.ResolveScalingMode(v.fx.UserScaling, layer.Scaling) {
	case courtroom.ScalingPixel:
		return sdl.ScaleModeNearest
	case courtroom.ScalingSmooth:
		return sdl.ScaleModeLinear
	}
	if nativeH > 0 && nativeH < targetH {
		return sdl.ScaleModeNearest
	}
	return sdl.ScaleModeLinear
}

// applyScaleMode points every frame of a page at one filter, memoised so the
// steady state costs one comparison per draw and no cgo at all.
// Render thread only.
func (p *TexturePage) applyScaleMode(ren *sdl.Renderer, mode sdl.ScaleMode) {
	if p == nil || ren == nil {
		return
	}
	if p.scaleModeSet && p.scaleMode == mode {
		return
	}
	_ = ren.Flush()
	for _, tex := range p.Frames {
		setTextureScaleMode(tex, mode)
	}
	// Latched even if some frame failed: retrying every draw would flush every
	// frame forever, and the failure is a property of the SDL build, not the page.
	p.scaleMode, p.scaleModeSet = mode, true
}

// textureScaleMode reads a texture's filter back, so tests can ask SDL directly
// whether a page's filter really took. Render thread only.
func textureScaleMode(tex *sdl.Texture) (sdl.ScaleMode, bool) {
	if tex == nil {
		return sdl.ScaleModeNearest, false
	}
	mode, err := tex.GetScaleMode()
	return mode, err == nil
}
