package render

// AO2 builds the splash layer (src/courtroom.cpp:111), sized to the viewport (courtroom.cpp:808-809).
// It never stretches (animationlayer.cpp:229-260, animationlayer.cpp:632-637, animationlayer.h:91).
// AlignCenter centres it (animationlayer.cpp:16). Only speedlines, interface layers and `stretch = true`
// effects stretch (courtroom.cpp:59, animationlayer.cpp:672, courtroom.cpp:3206).
// Desk and background stretch comes from each background's design.ini (animationlayer.cpp:616).

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// splashFitRect is AO2's non-stretch geometry: scale by viewport height over frame height, keep the aspect, centre it.
func splashFitRect(vp sdl.Rect, srcW, srcH int32) sdl.Rect {
	if srcW <= 0 || srcH <= 0 || vp.H <= 0 {
		return vp
	}
	// Round like Qt's QSize *= double; truncating would leave a hairline of stage showing through a bubble.
	w := int32((int64(srcW)*int64(vp.H)*2 + int64(srcH)) / (int64(srcH) * 2))
	if w <= 0 {
		w = 1
	}
	return sdl.Rect{X: vp.X + (vp.W-w)/2, Y: vp.Y, W: w, H: vp.H}
}

// drawSplash blits a bubble clipped to the stage, as Qt clips a child to its rect (courtroom.cpp:808-809).
// Wide art fit by height would otherwise paint over the chatbox.
// The clip and destination are fields because a local rect heap-escapes through cgo; SDL retains neither.
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

// layerFrame resolves a full-viewport layer's texture this frame and its source size.
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
