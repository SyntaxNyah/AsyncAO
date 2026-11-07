package render

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// sceneryRect is AO2's set_scene geometry for the background and desk (courtroom.cpp:4631-4670, animationlayer.cpp:241-262):
// stretched over the stage when design.ini says so, else fit by height and centred, or on a panorama placed so the
// position's origin is the stage's centre.
func sceneryRect(vp sdl.Rect, w, h int32, stretch, panorama bool, origin int) sdl.Rect {
	if stretch || w <= 0 || h <= 0 || vp.H <= 0 {
		return vp
	}
	r := splashFitRect(vp, w, h)
	if panorama {
		r.X = vp.X + vp.W/2 - int32(int64(origin)*int64(vp.H)/int64(h))
	}
	return r
}

// sceneryScaleMode is AO2's filter for the background and desk (animationlayer.cpp:233-270): the user's mode, else
// design.ini's, else point-sampled when a fitted image is enlarged and smooth otherwise.
func sceneryScaleMode(user, design courtroom.ScalingMode, stretch bool, nativeH, targetH int32) sdl.ScaleMode {
	switch courtroom.ResolveScalingMode(user, design) {
	case courtroom.ScalingPixel:
		return sdl.ScaleModeNearest
	case courtroom.ScalingSmooth:
		return sdl.ScaleModeLinear
	}
	if !stretch && nativeH < targetH {
		return sdl.ScaleModeNearest
	}
	return sdl.ScaleModeLinear
}

// drawScenery draws the background or desk with the background design.ini's geometry and filter; dimPct's ColorMod is
// restored as the page is shared.
func (v *Viewport) drawScenery(ren *sdl.Renderer, scene *courtroom.Scene, base string, anim *animState, vp sdl.Rect, dimPct int) {
	if base == "" {
		return
	}
	page, frame, ok := v.layerPage(anim)
	if !ok {
		return
	}
	v.fillRect = sceneryRect(vp, page.W, page.H, scene.SceneryStretch, scene.SceneryPanorama, scene.SceneryOrigin)
	page.applyScaleMode(ren, sceneryScaleMode(v.fx.UserScaling, scene.SceneryScaling, scene.SceneryStretch, page.H, v.fillRect.H))
	tex := page.Frames[frame]
	if dimPct < percentMax {
		d := scaleChannel(channelMax, dimPct)
		_ = tex.SetColorMod(d, d, d)
	}
	_ = ren.Copy(tex, nil, &v.fillRect)
	if dimPct < percentMax {
		_ = tex.SetColorMod(channelMax, channelMax, channelMax)
	}
}
