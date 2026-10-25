package render

// The shout splash's geometry: AO2 fits it BY HEIGHT and centres it. It does not
// stretch it across the viewport.
// (AO2-Client src/courtroom.cpp:111), moved and resized to the viewport
// (courtroom.cpp:808-809). Splash layers never turn stretching on:
// SplashAnimationLayer::loadAndPlayAnimation (src/animationlayer.cpp:632-637)
// (src/animationlayer.h:91). Only the speedlines, the interface layers and an
// effect whose own INI says `stretch = true` ever set it (courtroom.cpp:59,
// animationlayer.cpp:672, courtroom.cpp:3206).
// (src/animationlayer.cpp:229-260) takes the non-stretch arm:
// was constructed with `setAlignment(Qt::AlignCenter)` (animationlayer.cpp:16),
// which is what centres it horizontally in the viewport.
// (animationlayer.cpp:616), so their correct behaviour is per-background theme
// data rather than a constant, and it is a different asset's INI in a different
// package. Recorded, not half-done.

import "github.com/veandco/go-sdl2/sdl"

// splashFitRect is AO2's non-stretch AnimationLayer geometry: scale the frame by
// the viewport's HEIGHT over the frame's own height, keep the aspect, centre the
// result in the viewport.
func splashFitRect(vp sdl.Rect, srcW, srcH int32) sdl.Rect {
	if srcW <= 0 || srcH <= 0 || vp.H <= 0 {
		return vp
	}
	// Width from the HEIGHT factor, rounded to the nearest pixel. Qt computes
	// `QSize *= double`, which rounds; truncating would shave a pixel off wide
	// art and leave a hairline of stage showing through a bubble meant to cover
	// the frame edge-to-edge at its own aspect.
	w := int32((int64(srcW)*int64(vp.H)*2 + int64(srcH)) / (int64(srcH) * 2))
	if w <= 0 {
		w = 1
	}
	return sdl.Rect{X: vp.X + (vp.W-w)/2, Y: vp.Y, W: w, H: vp.H}
}

// drawSplash blits a shout bubble with the geometry above, CONTAINED to the stage.
// child of the viewport (courtroom.cpp:808-809 moves and resizes it to the
// viewport rect), and Qt clips a widget's painting to its own rect — so
// AlignCenter (animationlayer.cpp:16) both CENTRES an oversized pixmap and cuts
// off what hangs over the edge. Fitting by height means art wider than the stage's
// aspect comes out wider than the stage: 2000x40 art on a 960x540 stage fits to
// 27000 px. Unclipped, SDL_RenderCopy paints that straight over the chatbox and
// the side panels — which drawFill, blitting the viewport rect itself, could never
// do. The fit rect stays the full pixmap (that is what Qt scales to and what
// splashFitRect is tested on); the clip is what makes it a QLabel.
// The clip rides v.splashClip and the destination v.fillRect because &localRect
// heap-escapes through cgo on every call; SDL copies both during the call and
// retains neither (the cgoRect contract), and nothing runs between the
// assignments and the Copy.
func (v *Viewport) drawSplash(ren *sdl.Renderer, base string, anim *animState, vp sdl.Rect) {
	if base == "" {
		return
	}
	tex, w, h, ok := v.layerFrame(anim)
	if !ok {
		return
	}
	v.fillRect = splashFitRect(vp, w, h)
	clip := !ren.IsClipEnabled()
	if clip {
		v.splashClip = vp
		_ = ren.SetClipRect(&v.splashClip)
	}
	_ = ren.Copy(tex, nil, &v.fillRect)
	if clip {
		_ = ren.SetClipRect(nil)
	}
}

// layerFrame resolves the texture a full-viewport layer should blit this frame,
// plus that page's source pixel size.
func (v *Viewport) layerFrame(anim *animState) (tex *sdl.Texture, w, h int32, ok bool) {
	frame := 0
	page, live := anim.resolve(v.store)
	if live && len(page.Frames) > 0 {
		frame = clampFrame(anim.frame, len(page.Frames))
	} else if page, live = v.resolveHeld(anim); !live {
		return nil, 0, 0, false
	}
	if len(page.Frames) == 0 {
		return nil, 0, 0, false
	}
	return page.Frames[frame], page.W, page.H, true
}
