package render

import (
	"image/color"
	"math"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// TestPartialFrameMatchesFullFrame pins a partial frame to a full one: inside the redrawn region, here the stage, every
// pixel matches what a full frame draws, at render scales whose region corner isn't a whole pixel, and the rest of the
// window is left as it was. The pre-scaled sprites switch to scale 1 mid-frame, so the shifted viewport must survive
// that.
func TestPartialFrameMatchesFullFrame(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, 0)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	red, green := color.RGBA{R: channelMax, A: channelMax}, color.RGBA{G: channelMax, A: channelMax}
	blue, clear := color.RGBA{B: channelMax, A: channelMax}, color.RGBA{}
	const spriteW, spriteH, bgW, bgH = 48, 64, 64, 48
	for base, d := range map[string]*assets.Decoded{
		"bg":  halvesDecoded(bgW, bgH, blue, green),
		"spk": halvesDecoded(spriteW, spriteH, red, clear),
	} {
		if err := store.Upload(base, d); err != nil {
			t.Fatalf("upload %s: %v", base, err)
		}
	}
	scene := &courtroom.Scene{BackgroundBase: "bg",
		Speaker: courtroom.SpriteLayer{Active: "spk", Visible: true, Flip: true}}
	stage := sdl.Rect{X: 37, Y: 21, W: 200, H: 150}
	// farX, farY is a window pixel outside every scale's stage and margin.
	const farX, farY = prescaleWinW - 1, prescaleWinH - 1
	magenta := [argbBytes]byte{channelMax, 0, channelMax, channelMax} // B, G, R, A of ARGB8888 in memory

	for _, pct := range []int32{100, 133, 170, 175} {
		s := float32(pct) / percent
		v := NewViewport(store, false)
		v.Update(scene, 0)

		_ = ren.SetScale(1, 1)
		_ = ren.SetDrawColor(0, 0, 0, channelMax)
		_ = ren.Clear()
		_ = ren.SetScale(s, s)
		v.Render(ren, scene, stage)
		_ = ren.SetScale(1, 1)
		want := readWindow(t, ren)

		_ = ren.SetDrawColor(magenta[2], magenta[1], magenta[0], channelMax)
		_ = ren.Clear()
		var f PartialFrame
		if !f.Begin(ren, stage, pct, prescaleWinW, prescaleWinH) {
			t.Fatalf("%d%%: Begin refused a stage inside the window", pct)
		}
		v.Render(ren, scene, stage)
		f.End(ren)
		got := readWindow(t, ren)
		f.Release()

		x0, y0 := int(math.Floor(float64(stage.X)*float64(s))), int(math.Floor(float64(stage.Y)*float64(s)))
		x1 := int(math.Ceil(float64(stage.X+stage.W) * float64(s)))
		y1 := int(math.Ceil(float64(stage.Y+stage.H) * float64(s)))
		off := 0
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				i := (y*prescaleWinW + x) * argbBytes
				for c := 0; c < argbBytes-1; c++ {
					if d := int(got[i+c]) - int(want[i+c]); d > prescaleTolerance || d < -prescaleTolerance {
						off++
						break
					}
				}
			}
		}
		if off > 0 {
			t.Errorf("%d%%: %d stage pixels differ from a full frame", pct, off)
		}
		i := (farY*prescaleWinW + farX) * argbBytes
		if [argbBytes]byte(got[i:i+argbBytes]) != magenta {
			t.Errorf("%d%%: a pixel outside the stage was redrawn: % x", pct, got[i:i+argbBytes])
		}
	}
}
