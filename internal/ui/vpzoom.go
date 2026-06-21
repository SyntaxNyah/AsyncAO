package ui

// Viewport camera zoom ("hyperfocus"): Ctrl+wheel over the stage zooms
// toward the cursor, Ctrl+drag pans while zoomed, the 1× chip resets.
// Pure draw-side trick — the scene renders into an EXPANDED destination
// rect under a clip rect, so sprites, preanims, shakes, and effects all
// magnify together with zero render-package changes. Sprite dragging
// pauses while zoomed (zoom is a viewing mode; hit rects assume 1×).

import "github.com/veandco/go-sdl2/sdl"

const (
	// vpZoomMax bounds the camera (6× turns a 256-px-tall sprite into
	// pixel-art appreciation; past that it's just mush).
	vpZoomMax = 6.0
	// vpZoomStep is the per-wheel-tick multiplier.
	vpZoomStep = 1.15
	// vpZoomSnap collapses near-1× back to exactly 1× (clears the clip
	// path and re-enables sprite drag).
	vpZoomSnap = 1.05
)

// zoomDst maps the viewport rect to the zoomed destination: vp scaled by
// vpZoom with the pan fractions picking which slice stays visible.
func (a *App) zoomDst(vp sdl.Rect) sdl.Rect {
	if a.vpZoom <= 1 {
		return vp
	}
	zw := int32(float64(vp.W) * a.vpZoom)
	zh := int32(float64(vp.H) * a.vpZoom)
	return sdl.Rect{
		X: vp.X - int32(a.vpPanX*float64(zw-vp.W)),
		Y: vp.Y - int32(a.vpPanY*float64(zh-vp.H)),
		W: zw,
		H: zh,
	}
}

// renderViewportZoomed draws the scene at the current camera (clip set
// only while zoomed) and the 1× reset chip.
func (a *App) renderViewportZoomed(vp sdl.Rect) {
	c := a.ctx
	sc := a.renderScene() // real scene, or the slideshow's background override
	if a.vpZoom <= 1 {
		a.d.Viewport.Render(c.Ren, sc, vp)
		return
	}
	_ = c.Ren.SetClipRect(&vp)
	a.d.Viewport.Render(c.Ren, sc, a.zoomDst(vp))
	_ = c.Ren.SetClipRect(nil)
	// Reset chip, top-right of the stage.
	chip := sdl.Rect{X: vp.X + vp.W - 40, Y: vp.Y + 4, W: 36, H: 20}
	if c.Button(chip, "1×") {
		a.vpZoom, a.vpPanX, a.vpPanY = 1, 0, 0
	}
}

// handleViewportZoom owns the camera input: Ctrl+wheel zooms toward the
// cursor (the point under it stays put), Ctrl+drag pans. Skips when the
// cursor sits in the chat box band (that area's Ctrl+wheel resizes text).
func (a *App) handleViewportZoom(vp sdl.Rect, inChatBox bool) {
	c := a.ctx
	if vp.W <= 0 || vp.H <= 0 || a.dragVpDivider || a.courtModalOpen() {
		return // a blocking popup (Pair menu, evidence…) fences the stage
	}

	// Zoom: Ctrl+wheel over the stage (outside the chat text band).
	if c.ctrlHeld && !c.wheelTaken && c.wheelY != 0 && c.hovering(vp) && !inChatBox {
		c.wheelTaken = true
		old := a.vpZoom
		if old < 1 {
			old = 1
		}
		z := old
		for i := c.wheelY; i > 0; i-- {
			z *= vpZoomStep
		}
		for i := c.wheelY; i < 0; i++ {
			z /= vpZoomStep
		}
		if z > vpZoomMax {
			z = vpZoomMax
		}
		if z < vpZoomSnap {
			a.vpZoom, a.vpPanX, a.vpPanY = 1, 0, 0
			return
		}
		// Zoom-to-cursor: keep the scene point under the mouse fixed.
		// u = fraction of the zoomed image at the cursor before the step.
		oldDst := a.zoomDst(vp)
		u := float64(c.mouseX-oldDst.X) / float64(oldDst.W)
		v := float64(c.mouseY-oldDst.Y) / float64(oldDst.H)
		a.vpZoom = z
		zw := float64(vp.W) * z
		zh := float64(vp.H) * z
		// Solve pan so u stays under the cursor: dst.X = mouseX - u*zw.
		if zw > float64(vp.W) {
			a.vpPanX = clampFrac((u*zw - float64(c.mouseX-vp.X)) / (zw - float64(vp.W)))
		}
		if zh > float64(vp.H) {
			a.vpPanY = clampFrac((v*zh - float64(c.mouseY-vp.Y)) / (zh - float64(vp.H)))
		}
	}

	// Pan: Ctrl+drag while zoomed.
	if a.vpZoom <= 1 {
		a.zoomDrag = false
		return
	}
	pressed := c.mouseDown && !a.zoomPrev
	a.zoomPrev = c.mouseDown
	if pressed && c.ctrlHeld && c.hovering(vp) {
		a.zoomDrag = true
		a.zoomStart = [2]int32{c.mouseX, c.mouseY}
		a.zoomBase = [2]float64{a.vpPanX, a.vpPanY}
	}
	if !c.mouseDown {
		a.zoomDrag = false
	}
	if a.zoomDrag {
		overX := float64(vp.W) * (a.vpZoom - 1)
		overY := float64(vp.H) * (a.vpZoom - 1)
		if overX > 0 {
			a.vpPanX = clampFrac(a.zoomBase[0] - float64(c.mouseX-a.zoomStart[0])/overX)
		}
		if overY > 0 {
			a.vpPanY = clampFrac(a.zoomBase[1] - float64(c.mouseY-a.zoomStart[1])/overY)
		}
	}
}

// clampFrac clamps to [0, 1].
func clampFrac(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
