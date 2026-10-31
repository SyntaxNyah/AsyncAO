package ui

// Viewport camera zoom: Ctrl+wheel over the stage zooms toward the cursor, Ctrl+drag pans while zoomed,
// the 1x chip resets.
// The scene renders into an expanded destination rect under a clip, so everything magnifies together. Sprite
// dragging pauses while zoomed, since hit rects assume 1x.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// vpZoomMax bounds the camera.
	vpZoomMax = 6.0
	// vpZoomStep is the per-wheel-tick multiplier.
	vpZoomStep = 1.15
	// vpZoomSnap collapses near-1x back to exactly 1x, clearing the clip path and re-enabling sprite drag.
	vpZoomSnap = 1.05
)

// zoomDst maps the viewport rect to the zoomed destination: vp scaled by vpZoom,
// with the pan fractions picking the slice.
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

// renderViewportZoomed draws the scene at the current camera (clip set only while zoomed) and the 1x reset chip.
func (a *App) renderViewportZoomed(vp sdl.Rect) {
	c := a.ctx
	sc := a.renderScene() // real scene, or the slideshow's background override
	// Report ambient FX from the draw site, like drawSpritePreview: while the drawn stage animates on the free-running
	// clock (washes, a transmitted sprite style's motion, live weather). It fires only while such a stage actually
	// renders, so the lobby, Settings and an empty stage park like any static screen.
	// Paced by FramePace's anim-chrome tier.
	if a.d.Viewport != nil && a.d.Viewport.AmbientAnimating(sc) {
		a.NoteAnimating()
	}
	if a.vpZoom <= 1 {
		a.d.Viewport.Render(c.Ren, sc, vp)
		a.drawEditorPreviewFigure(vp) // theme-editor demo stand-in (no-op unless previewing)
		a.drawStageEgg(vp, sc)        // Scatterflower petals, behind the frame and the chatbox
		a.drawStageFrame(vp)          // #56 decorative frame (Off by default → no-op)
		return
	}
	// &vp into cgo would heap-allocate on every call even at 1x (escape analysis is static);
	// the Ctx scratch rect avoids it.
	c.cgoRect = vp
	_ = c.Ren.SetClipRect(&c.cgoRect)
	a.d.Viewport.Render(c.Ren, sc, a.zoomDst(vp))
	_ = c.Ren.SetClipRect(nil)
	// The stand-in rides the stage rect: it is a placeholder for art, so the camera must not magnify it.
	a.drawEditorPreviewFigure(vp)
	// Petals ride the stage rect, not the zoomed scene rect: they are chrome over the stage, like the frame below.
	a.drawStageEgg(vp, sc)
	a.drawStageFrame(vp) // frame is chrome — it stays fixed while the camera zooms
	// Reset chip, top-right of the stage.
	chip := sdl.Rect{X: vp.X + vp.W - 40, Y: vp.Y + 4, W: 36, H: 20}
	if c.Button(chip, "1×") {
		a.vpZoom, a.vpPanX, a.vpPanY = 1, 0, 0
	}
}

// handleViewportZoom owns the camera input: Ctrl+wheel zooms toward the cursor, Ctrl+drag pans. It skips when the
// cursor is in the chat box band, where Ctrl+wheel resizes text.
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
		// u is the fraction of the zoomed image at the cursor.
		oldDst := a.zoomDst(vp)
		u := float64(c.mouseX-oldDst.X) / float64(oldDst.W)
		v := float64(c.mouseY-oldDst.Y) / float64(oldDst.H)
		a.vpZoom = z
		zw := float64(vp.W) * z
		zh := float64(vp.H) * z
		// Solve pan so u stays under the cursor: dst.X = mouseX - u*zw.
		if zw > float64(vp.W) {
			a.vpPanX = value.Clamp((u*zw-float64(c.mouseX-vp.X))/(zw-float64(vp.W)), 0, 1)
		}
		if zh > float64(vp.H) {
			a.vpPanY = value.Clamp((v*zh-float64(c.mouseY-vp.Y))/(zh-float64(vp.H)), 0, 1)
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
			a.vpPanX = value.Clamp(a.zoomBase[0]-float64(c.mouseX-a.zoomStart[0])/overX, 0, 1)
		}
		if overY > 0 {
			a.vpPanY = value.Clamp(a.zoomBase[1]-float64(c.mouseY-a.zoomStart[1])/overY, 0, 1)
		}
	}
}
