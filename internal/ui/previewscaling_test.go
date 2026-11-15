package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// TestSpritePreviewUsesTheCharactersScaling: the preview draws a character's sprite with its char.ini filter before
// the sprite has ever been on stage (#88).
func TestSpritePreviewUsesTheCharactersScaling(t *testing.T) {
	const winW, winH = 800, 600
	for _, tc := range []struct {
		name    string
		scaling courtroom.ScalingMode
		before  sdl.ScaleMode
		want    sdl.ScaleMode
	}{
		{"pixel", courtroom.ScalingPixel, sdl.ScaleModeLinear, sdl.ScaleModeNearest},
		{"smooth", courtroom.ScalingSmooth, sdl.ScaleModeNearest, sdl.ScaleModeLinear},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newPreviewFeedApp(t)
			a.urls = courtroom.NewURLBuilder("http://test.example/base/")
			a.charMetaCache = map[string]charMeta{a.charINIURL(tc.name): {done: true, scaling: tc.scaling}}
			page, ok := a.d.Store.Get(previewFeedFixtureBase)
			if !ok {
				t.Fatal("fixture page missing")
			}
			page.ApplyScaleMode(a.ctx.Ren, tc.before)

			a.notePreviewChar(tc.name)
			a.drawSpritePreview(winW, winH, "")

			got, err := page.Frames[0].GetScaleMode()
			if err != nil {
				t.Skipf("scale mode unreadable: %v", err)
			}
			if got != tc.want {
				t.Errorf("preview filter = %d, want %d", got, tc.want)
			}
		})
	}
}
