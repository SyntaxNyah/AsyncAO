package render

// The shout splash's geometry: AO2 fits it BY HEIGHT and centres it. It does not
// stretch it across the viewport.
//
// CANON, exactly. `ui_vp_objection` is a `kal::SplashAnimationLayer`
// (AO2-Client src/courtroom.cpp:111), moved and resized to the viewport
// (courtroom.cpp:808-809). Splash layers never turn stretching on:
// SplashAnimationLayer::loadAndPlayAnimation (src/animationlayer.cpp:632-637)
// sets the file, the resize mode and starts playback, and nothing in it touches
// setStretchToFit — so `m_stretch_to_fit` keeps the `false` it is declared with
// (src/animationlayer.h:91). Only the speedlines, the interface layers and an
// effect whose own INI says `stretch = true` ever set it (courtroom.cpp:59,
// animationlayer.cpp:672, courtroom.cpp:3206).
//
// With stretching off, AnimationLayer::calculateFrameGeometry
// (src/animationlayer.cpp:229-260) takes the non-stretch arm:
//
//	m_scaled_frame_size = m_frame_size;
//	double scale = double(widget_size.height()) / double(m_scaled_frame_size.height());
//	m_scaled_frame_size *= scale;
//
// — one uniform factor from the HEIGHT alone, applied to both axes, so the art
// keeps the aspect its author drew. The scaled pixmap then goes to a QLabel that
// was constructed with `setAlignment(Qt::AlignCenter)` (animationlayer.cpp:16),
// which is what centres it horizontally in the viewport.
//
// AsyncAO blitted the shout through drawFill, the full-viewport path the
// BACKGROUND and the DESK legitimately use, so a 4:3 "Objection!" bubble was
// stretched across a 16:9 stage — wider the wider the window got. This is the
// separate geometry that path never had.
//
// The desk and the background are NOT changed here, deliberately. Their layer
// (BackgroundLayer) reads `stretch` out of the background's own design.ini
// (animationlayer.cpp:616), so their correct behaviour is per-background theme
// data rather than a constant, and it is a different asset's INI in a different
// package. Recorded, not half-done.

import "github.com/veandco/go-sdl2/sdl"

// splashFitRect is AO2's non-stretch AnimationLayer geometry: scale the frame by
// the viewport's HEIGHT over the frame's own height, keep the aspect, centre the
// result in the viewport.
//
// Falls back to the viewport itself when the source has no usable size — a page
// that reported 0x0 is a page we cannot reason about, and the pre-existing
// full-fill blit is a better answer than a zero-area rect nobody can see.
//
// Integer arithmetic only, and it runs once per shout frame on the render thread:
// no allocation, nothing to cache.
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
//
// It shares layerFrame with drawFill so the two can never disagree about WHICH
// frame is showing — the held-frame bridge, the animation cursor and the
// "nothing resident, draw nothing" arm are one implementation. The only
// difference between the two draws is the destination rect, which is exactly the
// difference AO2 has between a stretched layer and an unstretched one.
//
// THE CLIP IS HALF THE GEOMETRY, not a safety net. `ui_vp_objection` is a QLabel
// child of the viewport (courtroom.cpp:808-809 moves and resizes it to the
// viewport rect), and Qt clips a widget's painting to its own rect — so
// AlignCenter (animationlayer.cpp:16) both CENTRES an oversized pixmap and cuts
// off what hangs over the edge. Fitting by height means art wider than the stage's
// aspect comes out wider than the stage: 2000x40 art on a 960x540 stage fits to
// 27000 px. Unclipped, SDL_RenderCopy paints that straight over the chatbox and
// the side panels — which drawFill, blitting the viewport rect itself, could never
// do. The fit rect stays the full pixmap (that is what Qt scales to and what
// splashFitRect is tested on); the clip is what makes it a QLabel.
//
// Contained only when nothing else already owns a clip, and restored to none —
// the SAME idiom and the same restore discipline as drawOverlayInStage and the
// viewport sprite mask, and for the same reason: the camera-zoom clip
// (internal/ui/vpzoom.go) is itself the stage and is TIGHTER than the vp this is
// handed on that path (the zoomed scene rect), so stomping it with vp would paint
// outside the stage instead of inside it.
//
// It clips to the vp it is HANDED, which is where it differs from
// drawOverlayInStage (that one deliberately clips to the UN-punched stage). The
// shout punch and the screenshake inflate and jitter the whole stage frame, and
// the background rides the very same rect through drawFill — cropping only the
// bubble there would make the pop visibly desync from the scene it is popping.
// What this owes containment for is the ASPECT overhang, which is unbounded in
// the art's own width; the punch is a bounded, deliberate, whole-stage effect.
//
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
//
// Extracted from drawFill when the shout stopped sharing its destination rect: it
// is the ONE place that knows the resolve order — the live animation cursor
// first, then the held bridge frame that covers a page evicted mid-scene, then
// "nothing resident, draw nothing" (which leaves the pre-filled black stage, the
// state the bridge exists to avoid). Two copies of that order is how a fix to one
// layer silently stops applying to another.
//
// ok=false means there is nothing to draw at all. The width and height are the
// PAGE's, i.e. the art's own pixels, which is what an aspect-preserving fit needs
// and what a full-viewport fill ignores.
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
