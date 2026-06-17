package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestClickedIn pins press-origin gating. A committed click requires the button
// to be pressed AND released inside the rect; a release that drifted in from a
// drag begun elsewhere (a scrollbar grab, a panel move) must NOT fire. This is
// the fix for "I only hovered over the jukebox / player list and ended up in
// another area" — the navigational rows (area list, area-jump header, music
// track) all route through ClickedIn so a stray release can't transfer areas.
func TestClickedIn(t *testing.T) {
	row := sdl.Rect{X: 100, Y: 100, W: 200, H: 20}
	cases := []struct {
		name         string
		downX, downY int32 // where the left button went down
		upX, upY     int32 // where it came up (this frame's cursor)
		clicked      bool  // a mouse-up landed this frame
		want         bool
	}{
		{"press+release inside fires", 150, 110, 160, 112, true, true},
		{"no mouse-up this frame", 150, 110, 160, 112, false, false},
		{"press outside (drag-in) ignored", 40, 300, 160, 112, true, false},
		{"release drifted outside ignored", 150, 110, 400, 112, true, false},
		{"both outside ignored", 10, 10, 20, 20, true, false},
	}
	for _, tc := range cases {
		c := &Ctx{}
		c.downX, c.downY = tc.downX, tc.downY
		c.mouseX, c.mouseY = tc.upX, tc.upY
		c.clicked = tc.clicked
		if got := c.ClickedIn(row); got != tc.want {
			t.Errorf("%s: ClickedIn=%v, want %v", tc.name, got, tc.want)
		}
	}
}
