package ui

import "github.com/veandco/go-sdl2/sdl"

// Stage frames (#56): a decorative border drawn around the viewport, picked in
// Settings → Display (0 = Off, the default — nothing draws). Pure chrome: a
// handful of Fill/Border rects inside the stage rect, zero per-frame
// allocations, drawn under the chatbox overlay (the frame belongs to the stage,
// the chatbox sits on it). The style list must stay in step with config's
// stageFrameKindMax — TestStageFrameKindMaxMatchesUI pins the contract.

// stageFrameNames is the Settings dropdown, index == the persisted style id.
var stageFrameNames = []string{
	"Off", "Thin", "Accent", "Brass", "Neon", "Film strip", "Wood", "Shadow",
}

const (
	stageFrameOff = iota
	stageFrameThin
	stageFrameAccent
	stageFrameBrass
	stageFrameNeon
	stageFrameFilm
	stageFrameWood
	stageFrameShadow
)

const (
	// stageFrameFilmBand / stageFrameFilmHole size the film-strip matte bands and
	// their sprocket holes; stageFrameFilmPitch spaces the holes.
	stageFrameFilmBand  = int32(12)
	stageFrameFilmHole  = int32(6)
	stageFrameFilmPitch = int32(26)
	// stageFrameWoodThick is the wood frame's board width.
	stageFrameWoodThick = int32(8)
	// stageFrameShadowSteps is how many 1-px inward passes the shadow fades over.
	stageFrameShadowSteps = int32(6)
)

// insetRect shrinks r by n px on every side (never inverting).
func insetRect(r sdl.Rect, n int32) sdl.Rect {
	if r.W <= 2*n || r.H <= 2*n {
		return sdl.Rect{X: r.X + r.W/2, Y: r.Y + r.H/2}
	}
	return sdl.Rect{X: r.X + n, Y: r.Y + n, W: r.W - 2*n, H: r.H - 2*n}
}

// drawStageFrame paints the selected decorative frame just inside vp. Called at
// the end of renderViewportZoomed so every stage (classic, themed, theater) gets
// it; a zoomed camera keeps the frame fixed (it's chrome, not scene).
func (a *App) drawStageFrame(vp sdl.Rect) {
	kind := a.d.Prefs.StageFrame()
	if kind == stageFrameOff || vp.W < 40 || vp.H < 40 {
		return
	}
	c := a.ctx
	switch kind {
	case stageFrameThin:
		c.Border(vp, ColPanelHi)
		c.Border(insetRect(vp, 1), sdl.Color{R: 0, G: 0, B: 0, A: 160})
	case stageFrameAccent:
		c.Border(vp, ColAccent)
		c.Border(insetRect(vp, 1), ColAccent)
		c.Border(insetRect(vp, 2), sdl.Color{R: 0, G: 0, B: 0, A: 120})
	case stageFrameBrass:
		// Courtroom brass: dark bronze band, bright gold keyline, corner studs.
		dark := sdl.Color{R: 92, G: 66, B: 24, A: 255}
		gold := sdl.Color{R: 212, G: 175, B: 55, A: 255}
		for i := int32(0); i < 3; i++ {
			c.Border(insetRect(vp, i), dark)
		}
		c.Border(insetRect(vp, 3), gold)
		const stud = int32(7)
		for _, p := range [4][2]int32{
			{vp.X, vp.Y}, {vp.X + vp.W - stud, vp.Y},
			{vp.X, vp.Y + vp.H - stud}, {vp.X + vp.W - stud, vp.Y + vp.H - stud},
		} {
			c.Fill(sdl.Rect{X: p[0], Y: p[1], W: stud, H: stud}, gold)
		}
	case stageFrameNeon:
		// Soft inward glow (fading passes) under one crisp accent line.
		glow := ColAccent
		for i := int32(1); i <= 3; i++ {
			glow.A = uint8(90 - 25*i)
			c.Border(insetRect(vp, i), glow)
		}
		c.Border(vp, ColAccent)
	case stageFrameFilm:
		// Matte bands top + bottom with sprocket holes — the film-strip look.
		black := sdl.Color{R: 8, G: 8, B: 10, A: 230}
		hole := sdl.Color{R: 70, G: 70, B: 76, A: 255}
		c.Fill(sdl.Rect{X: vp.X, Y: vp.Y, W: vp.W, H: stageFrameFilmBand}, black)
		c.Fill(sdl.Rect{X: vp.X, Y: vp.Y + vp.H - stageFrameFilmBand, W: vp.W, H: stageFrameFilmBand}, black)
		hy0 := vp.Y + (stageFrameFilmBand-stageFrameFilmHole)/2
		hy1 := vp.Y + vp.H - stageFrameFilmBand + (stageFrameFilmBand-stageFrameFilmHole)/2
		for x := vp.X + stageFrameFilmPitch/2; x+stageFrameFilmHole < vp.X+vp.W; x += stageFrameFilmPitch {
			c.Fill(sdl.Rect{X: x, Y: hy0, W: stageFrameFilmHole, H: stageFrameFilmHole}, hole)
			c.Fill(sdl.Rect{X: x, Y: hy1, W: stageFrameFilmHole, H: stageFrameFilmHole}, hole)
		}
	case stageFrameWood:
		// Wooden boards: dark rim, mid board fill, light bevel on the inner lip.
		rim := sdl.Color{R: 52, G: 34, B: 18, A: 255}
		board := sdl.Color{R: 96, G: 64, B: 34, A: 255}
		bevel := sdl.Color{R: 150, G: 108, B: 64, A: 255}
		t := stageFrameWoodThick
		c.Fill(sdl.Rect{X: vp.X, Y: vp.Y, W: vp.W, H: t}, board)                      // top
		c.Fill(sdl.Rect{X: vp.X, Y: vp.Y + vp.H - t, W: vp.W, H: t}, board)           // bottom
		c.Fill(sdl.Rect{X: vp.X, Y: vp.Y + t, W: t, H: vp.H - 2*t}, board)            // left
		c.Fill(sdl.Rect{X: vp.X + vp.W - t, Y: vp.Y + t, W: t, H: vp.H - 2*t}, board) // right
		c.Border(vp, rim)
		c.Border(insetRect(vp, t-1), bevel)
		c.Border(insetRect(vp, t), sdl.Color{R: 0, G: 0, B: 0, A: 140})
	case stageFrameShadow:
		// Inward vignette-shadow: 1-px passes fading toward the middle.
		for i := int32(0); i < stageFrameShadowSteps; i++ {
			c.Border(insetRect(vp, i), sdl.Color{R: 0, G: 0, B: 0, A: uint8(150 - i*22)})
		}
	}
}
