package render

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// stageFrameMargin is the logical border kept around the stage, for draws whose edges round outward.
const stageFrameMargin = 1

// StageFrame redraws only the stage on SDL's software renderer, whose window keeps the last frame's pixels. The frame
// draws into a stage-sized texture under a viewport shifted so window coordinates land on it; anything outside the
// stage falls off the texture's edge, whatever clip the drawing sets. End copies the texture into the window.
type StageFrame struct {
	tex        *sdl.Texture
	texW, texH int32
	viewport   sdl.Rect
	dst        sdl.Rect
}

// Begin points a frame drawn at scalePct onto the stage texture, for a window of outW×outH pixels in which only the
// logical rect stage changed. The texture's corner is moved to a logical point whose pixel position is whole, so every
// draw lands on the same pixels as in a full frame. False leaves the frame to draw in full.
func (f *StageFrame) Begin(ren *sdl.Renderer, stage sdl.Rect, scalePct, outW, outH int32) bool {
	if scalePct <= 0 || stage.W <= 0 || stage.H <= 0 {
		return false
	}
	step := percentMax / gcd(scalePct, percentMax) // the smallest logical step that is a whole number of pixels
	x0 := max(snapDown(stage.X-stageFrameMargin, step), 0)
	y0 := max(snapDown(stage.Y-stageFrameMargin, step), 0)
	x1 := snapUp(stage.X+stage.W+stageFrameMargin, step)
	y1 := snapUp(stage.Y+stage.H+stageFrameMargin, step)
	px0, py0 := x0*scalePct/percentMax, y0*scalePct/percentMax
	px1, py1 := min(x1*scalePct/percentMax, outW), min(y1*scalePct/percentMax, outH)
	if px1 <= px0 || py1 <= py0 {
		return false
	}
	if !fitTarget(ren, &f.tex, &f.texW, &f.texH, px1-px0, py1-py0) || ren.SetRenderTarget(f.tex) != nil {
		return false
	}
	s := float32(scalePct) / percentMax
	_ = ren.SetScale(s, s)
	f.viewport = sdl.Rect{X: -x0, Y: -y0, W: x0 + outW*percentMax/scalePct + 1, H: y0 + outH*percentMax/scalePct + 1}
	_ = ren.SetViewport(&f.viewport)
	_ = ren.SetDrawColor(0, 0, 0, channelMax)
	_ = ren.Clear()
	f.dst = sdl.Rect{X: px0, Y: py0, W: px1 - px0, H: py1 - py0}
	return true
}

// End copies the redrawn stage into the window and leaves the window at scale 1.
func (f *StageFrame) End(ren *sdl.Renderer) {
	_ = ren.SetRenderTarget(nil)
	_ = ren.SetScale(1, 1)
	_ = f.tex.SetBlendMode(sdl.BLENDMODE_NONE)
	_ = ren.Copy(f.tex, nil, &f.dst)
}

// Release destroys the stage texture; render thread only.
func (f *StageFrame) Release() {
	if f.tex != nil {
		_ = f.tex.Destroy()
	}
	*f = StageFrame{}
}

func gcd(a, b int32) int32 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// snapDown and snapUp round n to a multiple of step, toward minus and plus infinity.
func snapDown(n, step int32) int32 {
	if n < 0 {
		return -snapUp(-n, step)
	}
	return n / step * step
}

func snapUp(n, step int32) int32 {
	if n < 0 {
		return -snapDown(-n, step)
	}
	return (n + step - 1) / step * step
}
