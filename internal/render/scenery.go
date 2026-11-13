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
	v.fillRect = sceneryRect(vp, page.W, page.H, scene.SceneryStretch, v.panorama, v.origin)
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

// placeStage sets this frame's panorama placement, the slide's eased origin while one runs, and returns how far the
// new and the last position's characters sit from the stage's centre, as they ride the pan (courtroom.cpp:3072-3089).
func (v *Viewport) placeStage(scene *courtroom.Scene, vp sdl.Rect) (newDX, lastDX int32) {
	v.panorama, v.origin = scene.SceneryPanorama, scene.SceneryOrigin
	slide := &scene.Slide
	if !slide.Active || slide.Total <= 0 {
		return 0, 0
	}
	v.panorama = true
	done := 1 - float64(slide.Left)/float64(slide.Total)
	v.origin = slide.From + int(float64(slide.To-slide.From)*inOutCubic(done))
	page, _, ok := v.layerPage(&v.bgAnim)
	if !ok || page.H <= 0 {
		return 0, 0
	}
	at := func(origin int) int32 { return int32(int64(origin-v.origin) * int64(vp.H) / int64(page.H)) }
	return at(slide.To), at(slide.From)
}

// inOutCubic is Qt's QEasingCurve::InOutCubic, the slide's curve (courtroom.cpp:3065).
func inOutCubic(t float64) float64 {
	const half, cubicScale, cubicShift = 0.5, 4, 2
	if t < half {
		return cubicScale * t * t * t
	}
	u := -cubicShift*t + cubicShift
	return 1 - u*u*u/cubicShift
}
