package render

// Per-texture scale mode, for AO2's `scaling` key (text_file_functions.cpp:175-191); render thread only.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// setTextureScaleMode sets one texture's filter and reports whether it took; render thread only.
func setTextureScaleMode(tex *sdl.Texture, mode sdl.ScaleMode) bool {
	return tex != nil && tex.SetScaleMode(mode) == nil
}

// spriteScaleMode applies AO2's rule (animationlayer.cpp:229-273): smooth, but point-sampled when enlarged.
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

// applyScaleMode sets every frame's filter, memoised so the steady state does no cgo; render thread only.
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
	// Set even if a frame failed: retrying would flush every draw, and failure comes from the SDL build, not the page.
	p.scaleMode, p.scaleModeSet = mode, true
}

// textureScaleMode reads a texture's filter back; render thread only.
func textureScaleMode(tex *sdl.Texture) (sdl.ScaleMode, bool) {
	if tex == nil {
		return sdl.ScaleModeNearest, false
	}
	mode, err := tex.GetScaleMode()
	return mode, err == nil
}
