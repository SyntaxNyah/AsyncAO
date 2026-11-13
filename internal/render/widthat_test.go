package render

import "testing"

// A sprite's width at the viewport's height rounds to the nearest pixel, as AO2's QSize scaling does, so it is
// centred where AO2 centres it.
func TestWidthAtRoundsToTheNearestPixelLikeAO2(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pageW, pageH int32
		height, want int32
	}{
		{name: "a .54 width rounds up", pageW: 642, pageH: 930, height: 192, want: 133},
		{name: "a .5 width rounds up", pageW: 3, pageH: 2, height: 3, want: 5},
		{name: "a .4 width rounds down", pageW: 642, pageH: 930, height: 600, want: 414},
		{name: "an exact width stays", pageW: 256, pageH: 192, height: 384, want: 512},
		{name: "an empty page has no width", pageW: 0, pageH: 0, height: 192, want: 0},
	} {
		p := &TexturePage{W: tc.pageW, H: tc.pageH}
		if got := p.WidthAt(tc.height); got != tc.want {
			t.Errorf("%s: WidthAt(%d) of %dx%d = %d, want %d", tc.name, tc.height, tc.pageW, tc.pageH, got, tc.want)
		}
	}
}
