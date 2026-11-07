package render

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	sceneryVPW     = 1600
	sceneryVPH     = 900
	sceneryArtW    = 1200
	sceneryArtH    = 900
	sceneryWideW   = 4800
	sceneryOrigin  = 2400
	sceneryShortH  = 450
	sceneryNoValue = 0
)

func TestSceneryRectFollowsSetScene(t *testing.T) {
	vp := sdl.Rect{W: sceneryVPW, H: sceneryVPH}
	if got := sceneryRect(vp, sceneryArtW, sceneryArtH, true, false, sceneryNoValue); got != vp {
		t.Errorf("stretch = %+v, want the whole stage", got)
	}
	fit := sceneryRect(vp, sceneryArtW, sceneryArtH, false, false, sceneryNoValue)
	if fit.H != vp.H || fit.W != sceneryArtW || fit.X != (vp.W-sceneryArtW)/2 {
		t.Errorf("fit = %+v, want height-fit and centred", fit)
	}
	pano := sceneryRect(vp, sceneryWideW, sceneryArtH, false, true, sceneryOrigin)
	if pano.W != sceneryWideW || pano.X != vp.W/2-sceneryOrigin {
		t.Errorf("panorama = %+v, want the origin at the stage's centre", pano)
	}
}

func TestSceneryScaleModeFollowsAO2(t *testing.T) {
	cases := []struct {
		user, design courtroom.ScalingMode
		stretch      bool
		nativeH      int32
		want         sdl.ScaleMode
	}{
		{courtroom.ScalingAuto, courtroom.ScalingAuto, false, sceneryShortH, sdl.ScaleModeNearest},
		{courtroom.ScalingAuto, courtroom.ScalingAuto, false, sceneryArtH, sdl.ScaleModeLinear},
		{courtroom.ScalingAuto, courtroom.ScalingAuto, true, sceneryShortH, sdl.ScaleModeLinear},
		{courtroom.ScalingAuto, courtroom.ScalingSmooth, false, sceneryShortH, sdl.ScaleModeLinear},
		{courtroom.ScalingPixel, courtroom.ScalingSmooth, false, sceneryArtH, sdl.ScaleModeNearest},
	}
	for i, c := range cases {
		if got := sceneryScaleMode(c.user, c.design, c.stretch, c.nativeH, sceneryVPH); got != c.want {
			t.Errorf("case %d: got %v, want %v", i, got, c.want)
		}
	}
}
